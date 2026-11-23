package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/accounting"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/monitoring"
)

const billingWriteTimeout = 3 * time.Second
const billingBatchSize = 100

var errBillingUnavailable = errors.New("billing persistence unavailable; retry after recovery")

type billingJournal interface {
	Append(context.Context, model.TokenSettlement) error
	Pending(context.Context, int) ([]model.TokenSettlement, error)
	Ack(context.Context, string) error
	Count(context.Context) (int64, error)
	Check(context.Context) error
	Close() error
}

type tokenSettlementStore interface {
	ApplyTokenSettlement(context.Context, model.TokenSettlement) error
}

type billingService struct {
	store   tokenSettlementStore
	journal billingJournal
	// Only persistence operations share this lock. A slow primary database
	// never holds it, so new completions can still be journaled.
	persistMu           sync.Mutex
	flushMu             sync.Mutex
	mu                  sync.Mutex
	unpersisted         map[string]model.TokenSettlement
	blocked             bool
	closing             bool
	active              int
	drained             chan struct{}
	wake                chan struct{}
	stop                chan struct{}
	done                chan struct{}
	startOnce           sync.Once
	stopOnce            sync.Once
	closeOnce           sync.Once
	closed              chan struct{}
	closeErr            error
	pending             atomic.Int64
	oldestPending       atomic.Int64
	journalOperation    monitoring.OperationMonitor
	settlementOperation monitoring.OperationMonitor
	journalFailures     atomic.Uint64
	storeFailures       atomic.Uint64
	invalidCosts        atomic.Uint64
	settled             atomic.Uint64
	deletedTokens       atomic.Uint64
	lastSuccess         atomic.Int64
}

type billingRuntimeStats struct {
	Enabled         bool                      `json:"enabled"`
	Pending         int64                     `json:"pending"`
	Unpersisted     int                       `json:"unpersisted"`
	Blocked         bool                      `json:"blocked"`
	JournalFailures uint64                    `json:"journal_failures"`
	StoreFailures   uint64                    `json:"store_failures"`
	InvalidCosts    uint64                    `json:"invalid_cost_events"`
	Settled         uint64                    `json:"settled"`
	DeletedTokens   uint64                    `json:"deleted_token_events"`
	LastSuccess     int64                     `json:"last_success_at"`
	Closing         bool                      `json:"closing"`
	OldestPendingAt int64                     `json:"oldest_pending_at"`
	Journal         monitoring.OperationStats `json:"journal"`
	Settlement      monitoring.OperationStats `json:"settlement"`
}

func newBillingService(store tokenSettlementStore, journal billingJournal) *billingService {
	return &billingService{store: store, journal: journal, unpersisted: make(map[string]model.TokenSettlement),
		wake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
		drained: make(chan struct{}), closed: make(chan struct{})}
}

// BillingJournalPath returns the bootstrap-only location of pending charges.
func BillingJournalPath() (string, error) {
	path := strings.TrimSpace(os.Getenv("PIVOTFLOW_BILLING_JOURNAL_PATH"))
	primaryPath := strings.TrimSpace(os.Getenv("SQLITE_PATH"))
	if primaryPath == "" {
		primaryPath = filepath.Join("data", "pivotflow.db")
	}
	if strings.HasPrefix(strings.ToLower(primaryPath), "file:") || primaryPath == ":memory:" {
		if path == "" {
			return "", errors.New("set PIVOTFLOW_BILLING_JOURNAL_PATH to a persistent file when SQLITE_PATH is a URI or in-memory database")
		}
		return path, nil
	}
	if path == "" {
		path = filepath.Join(filepath.Dir(primaryPath), "billing-pending.db")
	}
	journalAbs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	primaryAbs, err := filepath.Abs(primaryPath)
	if err != nil {
		return "", err
	}
	same := journalAbs == primaryAbs
	if journalInfo, err := os.Stat(journalAbs); err == nil {
		if primaryInfo, err := os.Stat(primaryAbs); err == nil {
			same = same || os.SameFile(journalInfo, primaryInfo)
		}
	}
	if same {
		return "", errors.New("billing journal must be separate from the primary or replica SQLite file")
	}
	return path, nil
}

// EnableDurableBilling must run before serving requests. Pending events are
// reconciled before loading limits, so a restart cannot forget unpaid usage.
func (s *Server) EnableDurableBilling(path string) error {
	if s.billing != nil {
		return errors.New("durable billing is already enabled")
	}
	j, err := accounting.Open(path)
	if err != nil {
		return fmt.Errorf("open billing journal: %w", err)
	}
	b := newBillingService(s.store, j)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	count, err := j.Count(ctx)
	if err == nil {
		b.pending.Store(count)
		for {
			if err = b.flush(ctx); err != nil || b.pending.Load() == 0 {
				break
			}
		}
	}
	if err == nil && s.authService != nil {
		err = s.authService.ReloadAuthTokens()
	}
	if err != nil {
		_ = j.Close()
		return fmt.Errorf("recover billing before accepting requests: %w", err)
	}
	s.billing = b
	b.start()
	return nil
}

// record accepts a new immutable event exactly once from a completed request.
// Retry paths only operate on that same event and never update the cost cache.
func (b *billingService) record(event model.TokenSettlement) {
	b.persistMu.Lock()
	b.mu.Lock()
	b.unpersisted[event.ID] = event
	alreadyBlocked := b.blocked
	b.mu.Unlock()
	if alreadyBlocked {
		b.persistMu.Unlock()
		select {
		case b.wake <- struct{}{}:
		default:
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), billingWriteTimeout)
	err := b.appendJournal(ctx, event)
	cancel()
	if err == nil {
		b.mu.Lock()
		delete(b.unpersisted, event.ID)
		b.mu.Unlock()
		b.pending.Add(1)
		b.notePendingTime(event.OccurredAt)
	} else {
		b.journalFailed(err)
	}
	b.persistMu.Unlock()
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *billingService) journalFailed(err error) {
	b.mu.Lock()
	b.blocked = true
	b.mu.Unlock()
	if n := b.journalFailures.Add(1); n%100 == 1 {
		log.Printf("[ERROR] Billing journal unavailable; new proxy work paused (failures=%d): %v", n, err)
	}
}

func (b *billingService) flush(ctx context.Context) error {
	b.flushMu.Lock()
	defer b.flushMu.Unlock()
	// Replay only a bounded batch per cycle; new completions can be appended
	// while the primary is slow. Acknowledgement failures leave receipts safe
	// to replay because primary accounting uses the immutable event ID.
	events, err := b.pendingBatch(ctx)
	if err != nil {
		// The independent journal may fail while the primary is healthy.
		// Save volatile events there with the same receipt IDs; an ambiguous
		// journal commit may later replay them safely. Admission stays closed
		// until a real journal write succeeds.
		b.settleUnpersisted(ctx)
		return err
	}
	for _, event := range events {
		applyCtx, cancel := context.WithTimeout(ctx, billingWriteTimeout)
		err = b.applySettlement(applyCtx, event)
		cancel()
		deleted := errors.Is(err, model.ErrAuthTokenNotFound)
		if err != nil && !deleted {
			if n := b.storeFailures.Add(1); n%100 == 1 {
				log.Printf("[WARN] Billing settlement pending; retrying (failures=%d): %v", n, err)
			}
			return err
		}
		b.persistMu.Lock()
		ackCtx, ackCancel := context.WithTimeout(ctx, billingWriteTimeout)
		finishAck := b.journalOperation.Begin()
		err = b.journal.Ack(ackCtx, event.ID)
		ackCancel()
		finishAck(err)
		if err == nil {
			if b.pending.Add(-1) == 0 {
				b.oldestPending.Store(0)
			}
		}
		b.persistMu.Unlock()
		if err != nil {
			b.journalFailed(err)
			return err
		}
		if deleted {
			b.deletedTokens.Add(1)
		} else {
			b.settled.Add(1)
		}
		b.lastSuccess.Store(time.Now().UnixMilli())
	}
	return nil
}

func (b *billingService) settleUnpersisted(ctx context.Context) {
	b.mu.Lock()
	events := make([]model.TokenSettlement, 0, min(len(b.unpersisted), billingBatchSize))
	for _, event := range b.unpersisted {
		events = append(events, event)
		if len(events) == billingBatchSize {
			break
		}
	}
	b.mu.Unlock()
	for _, event := range events {
		applyCtx, cancel := context.WithTimeout(ctx, billingWriteTimeout)
		err := b.applySettlement(applyCtx, event)
		cancel()
		deleted := errors.Is(err, model.ErrAuthTokenNotFound)
		if err != nil && !deleted {
			b.storeFailures.Add(1)
			return
		}
		b.mu.Lock()
		delete(b.unpersisted, event.ID)
		b.mu.Unlock()
		if deleted {
			b.deletedTokens.Add(1)
		} else {
			b.settled.Add(1)
		}
		b.lastSuccess.Store(time.Now().UnixMilli())
	}
}

func (b *billingService) pendingBatch(ctx context.Context) ([]model.TokenSettlement, error) {
	b.persistMu.Lock()
	defer b.persistMu.Unlock()
	b.mu.Lock()
	volatile := make([]model.TokenSettlement, 0, len(b.unpersisted))
	for _, event := range b.unpersisted {
		volatile = append(volatile, event)
	}
	wasBlocked := b.blocked
	b.mu.Unlock()
	for _, event := range volatile {
		writeCtx, cancel := context.WithTimeout(ctx, billingWriteTimeout)
		err := b.appendJournal(writeCtx, event)
		cancel()
		if err != nil {
			b.journalFailed(err)
			return nil, err
		}
		b.mu.Lock()
		delete(b.unpersisted, event.ID)
		b.mu.Unlock()
		b.pending.Add(1)
		b.notePendingTime(event.OccurredAt)
	}
	readCtx, cancel := context.WithTimeout(ctx, billingWriteTimeout)
	defer cancel()
	finishRead := b.journalOperation.Begin()
	events, err := b.journal.Pending(readCtx, billingBatchSize)
	if err == nil {
		var count int64
		count, err = b.journal.Count(readCtx)
		if err == nil {
			b.pending.Store(count)
		}
	}
	if err == nil && wasBlocked {
		err = b.journal.Check(readCtx)
	}
	finishRead(err)
	if err != nil {
		b.journalFailed(err)
		return nil, err
	}
	if len(events) > 0 {
		b.oldestPending.Store(events[0].OccurredAt)
	} else {
		b.oldestPending.Store(0)
	}
	if wasBlocked {
		b.mu.Lock()
		b.blocked = false
		b.mu.Unlock()
		log.Print("[INFO] Billing journal recovered; proxy work resumed")
	}
	return events, nil
}

func (b *billingService) stats() billingRuntimeStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	oldestPending := b.oldestPending.Load()
	if b.pending.Load() == 0 {
		oldestPending = 0
	}
	return billingRuntimeStats{Enabled: true, Pending: b.pending.Load(), Unpersisted: len(b.unpersisted),
		Blocked: b.blocked || b.closing, JournalFailures: b.journalFailures.Load(),
		StoreFailures: b.storeFailures.Load(), Settled: b.settled.Load(),
		InvalidCosts:  b.invalidCosts.Load(),
		DeletedTokens: b.deletedTokens.Load(), LastSuccess: b.lastSuccess.Load(),
		Closing: b.closing, OldestPendingAt: oldestPending,
		Journal: b.journalOperation.Snapshot(), Settlement: b.settlementOperation.Snapshot()}
}

func (b *billingService) appendJournal(ctx context.Context, event model.TokenSettlement) error {
	finish := b.journalOperation.Begin()
	err := b.journal.Append(ctx, event)
	finish(err)
	return err
}

func (b *billingService) applySettlement(ctx context.Context, event model.TokenSettlement) error {
	finish := b.settlementOperation.Begin()
	err := b.store.ApplyTokenSettlement(ctx, event)
	if errors.Is(err, model.ErrAuthTokenNotFound) {
		finish(nil) // A deleted Token is a terminal handled event, not a store fault.
	} else {
		finish(err)
	}
	return err
}

func (b *billingService) notePendingTime(occurredAt int64) {
	for {
		previous := b.oldestPending.Load()
		if previous != 0 && previous <= occurredAt {
			return
		}
		if b.oldestPending.CompareAndSwap(previous, occurredAt) {
			return
		}
	}
}

func (b *billingService) acquire() (func(), error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.blocked || b.closing {
		return nil, errBillingUnavailable
	}
	b.active++
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.active--
			if b.closing && b.active == 0 {
				close(b.drained)
			}
		})
	}, nil
}

func (s *Server) acquireBillingForHTTP(c *gin.Context) (func(), bool) {
	if s.billing == nil {
		return func() {}, true
	}
	release, err := s.billing.acquire()
	if err == nil {
		return release, true
	}
	c.Header("Retry-After", "5")
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{
		"type": "server_error", "code": "billing_unavailable", "message": err.Error(),
	}})
	return nil, false
}

func (b *billingService) start() {
	b.startOnce.Do(func() {
		go func() {
			defer close(b.done)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-b.stop:
					return
				case <-b.wake:
				case <-ticker.C:
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := b.flush(ctx)
				cancel()
				if err == nil && b.pending.Load() > 0 {
					select {
					case b.wake <- struct{}{}:
					default:
					}
				}
			}
		}()
	})
}

func (b *billingService) close(ctx context.Context) error {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.closing = true
		if b.active == 0 {
			close(b.drained)
		}
		b.mu.Unlock()
		// On a shutdown deadline, keep the journal open until admitted work
		// finishes. Main normally drains HTTP first; active WS turns release
		// their lease when the server cancels those connections.
		go func() {
			defer close(b.closed)
			<-b.drained
			b.stopOnce.Do(func() { close(b.stop) })
			b.start()
			<-b.done
			flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = b.flush(flushCtx)
			cancel()
			b.mu.Lock()
			volatile := len(b.unpersisted)
			b.mu.Unlock()
			if volatile != 0 {
				b.closeErr = fmt.Errorf("%d billing events could not be persisted before shutdown", volatile)
			}
			b.closeErr = errors.Join(b.closeErr, b.journal.Close())
		}()
	})
	select {
	case <-b.closed:
		return b.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newTokenSettlement(upd tokenStatsUpdate) model.TokenSettlement {
	event := model.TokenSettlement{ID: "bill_" + rand.Text(), TokenHash: upd.tokenHash,
		OccurredAt: time.Now().UnixMilli(), Duration: upd.duration, IsStreaming: upd.isStreaming,
		FirstByteTime: upd.firstByteTime, PromptTokens: max(0, upd.promptTokens),
		CompletionTokens: max(0, upd.completionTokens), CacheReadTokens: max(0, upd.cacheReadTokens),
		CacheCreationTokens: max(0, upd.cacheCreationTokens), CostUSD: upd.costUSD,
		EffectiveCostUSD: effectiveCost(upd.costUSD, upd.costMultiplier, costSourceForSiteCost(upd.siteCost)),
		CostInvalid:      upd.costInvalid}
	if err := model.ValidateTokenSettlementCosts(event.CostUSD, event.EffectiveCostUSD); err != nil {
		// Preserve usage and an explicit receipt marker for reconciliation.
		// Invalid upstream amounts must not poison a healthy durable queue.
		log.Printf("[ERROR] Billing amount invalid; monetary charge omitted pending reconciliation event_id=%s cost=%g effective_cost=%g: %v",
			event.ID, event.CostUSD, event.EffectiveCostUSD, err)
		event.CostUSD = 0
		event.EffectiveCostUSD = 0
		event.CostInvalid = true
	}
	return event
}
