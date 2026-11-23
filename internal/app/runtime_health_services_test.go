package app

import (
	"bytes"
	"context"
	"errors"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

type runtimeCleanupStore struct {
	storage.Store
	cleanupErr error
	settingErr error
	debugErr   error
	debugOn    bool
	cutoffs    []time.Time
}

func (s *runtimeCleanupStore) CleanupLogsBefore(ctx context.Context, cutoff time.Time) error {
	s.cutoffs = append(s.cutoffs, cutoff)
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.cleanupErr
}

func (s *runtimeCleanupStore) GetSetting(ctx context.Context, key string) (*model.SystemSetting, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.settingErr != nil {
		return nil, s.settingErr
	}
	if key == "debug_log_enabled" {
		value := "false"
		if s.debugOn {
			value = "true"
		}
		return &model.SystemSetting{Key: key, Value: value}, nil
	}
	return &model.SystemSetting{Key: key, Value: "60"}, nil
}

func (s *runtimeCleanupStore) CleanupDebugLogsBatch(_ context.Context, _ time.Time, _ int) (int64, error) {
	return 7, s.debugErr
}

func (s *runtimeCleanupStore) TruncateDebugLogs(_ context.Context) error {
	return s.debugErr
}

func newRuntimeHealthLogService(store storage.Store, retention int) *LogService {
	return NewLogService(store, 8, 1, retention, make(chan struct{}), &atomic.Bool{}, &sync.WaitGroup{})
}

func TestRuntimeHealthServicesLogCleanupFailureAndRecovery(t *testing.T) {
	store := &runtimeCleanupStore{cleanupErr: errors.New("private database path unavailable")}
	svc := newRuntimeHealthLogService(store, 3)
	if err := svc.cleanupOldLogs(context.Background()); err == nil {
		t.Fatal("cleanup must return the failure rather than discard it")
	}
	failed := svc.runtimeStats().Cleanup.Logs
	if !failed.Enabled || !failed.Operation.Failing || failed.Operation.Failed != 1 || failed.Operation.LastFailureAt == 0 {
		t.Fatalf("cleanup failure invisible to operators: %+v", failed)
	}
	if len(store.cutoffs) != 1 || time.Since(store.cutoffs[0]) < 71*time.Hour {
		t.Fatalf("cleanup changed the configured retention cutoff: %v", store.cutoffs)
	}
	store.cleanupErr = nil
	if err := svc.cleanupOldLogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := svc.runtimeStats().Cleanup.Logs
	if recovered.Operation.Failing || recovered.Operation.Failed != 1 || recovered.Operation.Succeeded != 1 || recovered.Operation.RecoveredAt == 0 {
		t.Fatalf("cleanup did not recover with historical failure intact: %+v", recovered)
	}
}

func TestRuntimeHealthServicesDisabledAndCancelledCleanup(t *testing.T) {
	store := &runtimeCleanupStore{}
	disabled := newRuntimeHealthLogService(store, 0)
	if err := disabled.cleanupOldLogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats := disabled.runtimeStats().Cleanup.Logs
	if stats.Enabled || stats.Operation.Attempts != 0 || len(store.cutoffs) != 0 {
		t.Fatalf("disabled retention performed cleanup: %+v", stats)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	svc := newRuntimeHealthLogService(store, 3)
	if err := svc.cleanupOldLogs(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cleanup cancellation: %v", err)
	}
	stats = svc.runtimeStats().Cleanup.Logs
	if stats.Operation.Failed != 0 || stats.Operation.Cancelled != 1 || stats.Operation.Failing {
		t.Fatalf("shutdown cleanup was falsely reported as a storage failure: %+v", stats)
	}
}

type runtimeCancelledCleanupStore struct {
	storage.Store
	cancel context.CancelFunc
}

func (s *runtimeCancelledCleanupStore) CleanupLogsBefore(_ context.Context, _ time.Time) error {
	s.cancel()
	return errors.Join(errors.New("cleanup storage failed during shutdown"), context.Canceled)
}

func TestRuntimeHealthServicesShutdownDoesNotHideCleanupFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := newRuntimeHealthLogService(&runtimeCancelledCleanupStore{cancel: cancel}, 3)
	var output bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(previousWriter)
	if err := svc.cleanupOldLogs(ctx); err == nil {
		t.Fatal("mixed shutdown failure was discarded")
	}
	stats := svc.runtimeStats().Cleanup.Logs.Operation
	if stats.Failed != 1 || !stats.Failing || stats.Cancelled != 0 {
		t.Errorf("shutdown concealed actual cleanup fault: %+v", stats)
	}
	if !strings.Contains(output.String(), "cleanup storage failed during shutdown") {
		t.Error("actual cleanup error was not logged when the context was cancelled")
	}
}

func TestRuntimeHealthServicesDebugCleanupResults(t *testing.T) {
	store := &runtimeCleanupStore{debugOn: true, settingErr: errors.New("setting read failed")}
	svc := newRuntimeHealthLogService(store, 3)
	svc.cleanupDebugLogs(context.Background())
	if got := svc.runtimeStats().Cleanup.DebugLogs.Operation; got.Failed != 1 || !got.Failing {
		t.Fatalf("debug setting failure invisible: %+v", got)
	}
	store.settingErr = nil
	store.debugErr = context.DeadlineExceeded
	svc.cleanupDebugLogs(context.Background())
	if got := svc.runtimeStats().Cleanup.DebugLogs.Operation; got.Failed != 2 || got.LastErrorCode != "timeout" {
		t.Fatalf("debug deletion timeout invisible: %+v", got)
	}
	store.debugErr = nil
	svc.cleanupDebugLogs(context.Background())
	got := svc.runtimeStats().Cleanup.DebugLogs
	if got.Operation.Failing || got.Operation.RecoveredAt == 0 || got.Deleted != 7 || got.RetentionMinutes != 60 {
		t.Fatalf("successful debug retention round was not recorded: %+v", got)
	}
	store.debugOn = false
	if done := svc.cleanupDebugLogs(context.Background()); !done {
		t.Fatal("disabled debug cleanup did not finish truncating")
	}
	got = svc.runtimeStats().Cleanup.DebugLogs
	if got.Enabled || got.Mode != "disabled" || got.Operation.Failing || got.Operation.Succeeded != 2 {
		t.Fatalf("disabled-and-cleared debug table must be distinct from a fault: %+v", got)
	}
}

type runtimeBlockingWriterStore struct {
	storage.Store
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *runtimeBlockingWriterStore) write(ctx context.Context) error {
	s.once.Do(func() { close(s.started) })
	select {
	case <-s.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *runtimeBlockingWriterStore) BatchAddLogs(ctx context.Context, _ []*model.LogEntry) error {
	return s.write(ctx)
}

func (s *runtimeBlockingWriterStore) BatchAddRequestTraces(ctx context.Context, _ []*model.RequestTrace) error {
	return s.write(ctx)
}

func TestRuntimeHealthServicesBufferedWritesRemainVisible(t *testing.T) {
	for _, kind := range []string{"logs", "traces"} {
		t.Run(kind, func(t *testing.T) {
			store := &runtimeBlockingWriterStore{started: make(chan struct{}), release: make(chan struct{})}
			svc := newRuntimeHealthLogService(store, 3)
			t.Cleanup(func() {
				close(store.release)
				svc.isShuttingDown.Store(true)
				close(svc.shutdownCh)
				svc.wg.Wait()
			})
			svc.StartWorkers()
			if kind == "logs" {
				svc.AddLogAsync(&model.LogEntry{Model: "buffered-model"})
			} else {
				svc.AddRequestTraceAsync(newRequestTraceWorkerTestTrace("pf_req_buffered"))
			}
			select {
			case <-store.started:
			case <-time.After(2 * time.Second):
				t.Fatal("background writer did not begin")
			}
			stats := svc.runtimeStats().Logs
			if kind == "traces" {
				stats = svc.runtimeStats().Traces
			}
			if stats.Queued != 0 || stats.Buffered != 1 || stats.Operation.Active != 1 || stats.Written != 0 {
				t.Fatalf("empty channel hid the unfinished batch: %+v", stats)
			}
		})
	}
}

func TestRuntimeHealthServicesFinalLogLossAndRecovery(t *testing.T) {
	store := &failThenSucceedStore{failN: 100}
	svc := newRuntimeHealthLogService(store, 3)
	batch := []*model.LogEntry{{Model: "one"}, {Model: "two"}}
	svc.flushLogs(batch)
	got := svc.runtimeStats().Logs
	if got.WriteFailed != 2 || got.Written != 0 || !got.Operation.Failing {
		t.Fatalf("finally lost log records not counted separately from attempts: %+v", got)
	}
	store.failN = 0
	svc.flushLogs(batch)
	got = svc.runtimeStats().Logs
	if got.WriteFailed != 2 || got.Written != 2 || got.Operation.Failing || got.Operation.RecoveredAt == 0 {
		t.Fatalf("later log success erased historical loss or remained failed: %+v", got)
	}
}

func TestRuntimeHealthServicesBillingBacklogFailureRecovery(t *testing.T) {
	store, hash := newBillingTestStore(t)
	failure := &settlementFailureStore{Store: store, down: true}
	journal := openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db"))
	b := newBillingService(failure, journal)
	event := billingTestEvent(hash)
	event.OccurredAt = time.Now().Add(-time.Minute).UnixMilli()
	b.record(event)
	if err := b.flush(context.Background()); err == nil {
		t.Fatal("primary outage did not retain the event")
	}
	stats := b.stats()
	if stats.Pending != 1 || stats.OldestPendingAt != event.OccurredAt || !stats.Settlement.Failing || stats.Settlement.LastFailureAt == 0 {
		t.Fatalf("settlement backlog/failure invisible: %+v", stats)
	}
	failure.mu.Lock()
	failure.down = false
	failure.mu.Unlock()
	if err := b.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats = b.stats()
	if stats.Pending != 0 || stats.OldestPendingAt != 0 || stats.StoreFailures != 1 || stats.Settlement.Failing || stats.Settlement.RecoveredAt == 0 {
		t.Fatalf("settlement recovery reported incorrectly: %+v", stats)
	}
}

func TestRuntimeHealthServicesJournalFailureRecovery(t *testing.T) {
	store, hash := newBillingTestStore(t)
	journal := &failingBillingJournal{billingJournal: openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db")), fail: true}
	b := newBillingService(store, journal)
	b.record(billingTestEvent(hash))
	if got := b.stats(); !got.Blocked || !got.Journal.Failing || got.Journal.Failed != 1 {
		t.Fatalf("journal pause not observable: %+v", got)
	}
	journal.mu.Lock()
	journal.fail = false
	journal.mu.Unlock()
	if err := b.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := b.stats(); got.Blocked || got.Journal.Failing || got.JournalFailures != 1 || got.Journal.RecoveredAt == 0 {
		t.Fatalf("journal recovery not observable: %+v", got)
	}
}
