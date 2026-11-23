package app

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/accounting"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

type settlementFailureStore struct {
	storage.Store
	mu                  sync.Mutex
	down                bool
	loseAcknowledgement bool
}

func (s *settlementFailureStore) ApplyTokenSettlement(ctx context.Context, event model.TokenSettlement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.down {
		return errors.New("database unavailable")
	}
	if err := s.Store.ApplyTokenSettlement(ctx, event); err != nil {
		return err
	}
	if s.loseAcknowledgement {
		s.loseAcknowledgement = false
		return errors.New("commit acknowledgement lost")
	}
	return nil
}

func newBillingTestStore(t *testing.T) (storage.Store, string) {
	t.Helper()
	store, err := storage.CreateSQLiteStore(filepath.Join(t.TempDir(), "primary.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	hash := strings.Repeat("c", 64)
	if err := store.CreateAuthToken(context.Background(), &model.AuthToken{
		Token: hash, IsActive: true, CostLimitMicroUSD: 1_000_000, MaxConcurrency: 2,
	}); err != nil {
		t.Fatal(err)
	}
	return store, hash
}

func billingTestEvent(hash string) model.TokenSettlement {
	return model.TokenSettlement{ID: "billing-event-1", TokenHash: hash,
		OccurredAt: time.Now().UnixMilli(), Duration: 1, PromptTokens: 10,
		CompletionTokens: 20, CostUSD: 0.25, EffectiveCostUSD: 0.25}
}

func openBillingTestJournal(t *testing.T, path string) *accounting.Journal {
	t.Helper()
	j, err := accounting.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

func TestBillingRetriesAmbiguousCommitWithoutDoubleCharging(t *testing.T) {
	store, hash := newBillingTestStore(t)
	j := openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db"))
	failure := &settlementFailureStore{Store: store, loseAcknowledgement: true}
	b := newBillingService(failure, j)
	event := billingTestEvent(hash)
	b.record(event)
	if err := b.flush(context.Background()); err == nil {
		t.Fatal("expected ambiguous commit error")
	}
	if n, _ := j.Count(context.Background()); n != 1 {
		t.Fatalf("pending=%d, want 1", n)
	}
	if err := b.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetAuthTokenByValue(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.SuccessCount != 1 || got.CostUsedMicroUSD != 250_000 || got.PromptTokensTotal != 10 {
		t.Fatalf("replay duplicated accounting: count=%d cost=%d input=%d", got.SuccessCount, got.CostUsedMicroUSD, got.PromptTokensTotal)
	}
	if n, _ := j.Count(context.Background()); n != 0 {
		t.Fatalf("pending=%d after successful retry", n)
	}
}

func TestBillingPendingSurvivesReopenAndReloadsLimit(t *testing.T) {
	store, hash := newBillingTestStore(t)
	path := filepath.Join(t.TempDir(), "pending.db")
	j := openBillingTestJournal(t, path)
	failure := &settlementFailureStore{Store: store, down: true}
	b := newBillingService(failure, j)
	b.record(billingTestEvent(hash))
	if err := b.flush(context.Background()); err == nil {
		t.Fatal("expected database failure")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(store)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := srv.EnableDurableBilling(path); err != nil {
		t.Fatal(err)
	}
	got, _, _ := srv.authService.IsCostLimitExceeded(hash)
	if got != 250_000 {
		t.Fatalf("recovered cost cache=%d, want 250000", got)
	}
	if n := srv.billing.stats().Pending; n != 0 {
		t.Fatalf("pending after startup=%d", n)
	}
}

type failingBillingJournal struct {
	billingJournal
	mu   sync.Mutex
	fail bool
}

func (j *failingBillingJournal) Append(ctx context.Context, event model.TokenSettlement) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.fail {
		return errors.New("journal disk unavailable")
	}
	return j.billingJournal.Append(ctx, event)
}

func (j *failingBillingJournal) Check(ctx context.Context) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.fail {
		return errors.New("journal disk unavailable")
	}
	return j.billingJournal.Check(ctx)
}

func TestBillingJournalFailureBlocksNewWorkUntilRecovered(t *testing.T) {
	store, hash := newBillingTestStore(t)
	j := &failingBillingJournal{billingJournal: openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db")), fail: true}
	failure := &settlementFailureStore{Store: store, down: true}
	b := newBillingService(failure, j)
	b.record(billingTestEvent(hash))
	if _, err := b.acquire(); err == nil {
		t.Fatal("new work accepted while accounting is not durable")
	}
	srv := &Server{billing: b}
	c, w := newTestContext(t, newRequest(http.MethodPost, "/v1/chat/completions", nil))
	if _, ok := srv.acquireBillingForHTTP(c); ok || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("billing admission: allowed=%v status=%d", ok, w.Code)
	}
	if !strings.Contains(w.Body.String(), "billing_unavailable") {
		t.Fatalf("wrong error body: %s", w.Body.String())
	}
	if stats := b.stats(); stats.Unpersisted != 1 || stats.JournalFailures != 1 || !stats.Blocked {
		t.Fatalf("missing degraded metrics: %+v", stats)
	}
	j.mu.Lock()
	j.fail = false
	j.mu.Unlock()
	failure.mu.Lock()
	failure.down = false
	failure.mu.Unlock()
	if err := b.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	release, err := b.acquire()
	if err != nil {
		t.Fatalf("recovered journal still blocked: %v", err)
	}
	release()
	got, err := store.GetAuthTokenByValue(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.SuccessCount != 1 || got.CostUsedMicroUSD != 250_000 {
		t.Fatalf("recovered event: %+v", got)
	}
}

func TestBillingJournalFailureStillSettlesAvailablePrimary(t *testing.T) {
	store, hash := newBillingTestStore(t)
	j := &failingBillingJournal{billingJournal: openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db")), fail: true}
	b := newBillingService(store, j)
	b.record(billingTestEvent(hash))
	if err := b.flush(context.Background()); err == nil {
		t.Fatal("unavailable journal must still report failure")
	}
	got, err := store.GetAuthTokenByValue(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.SuccessCount != 1 || got.CostUsedMicroUSD != 250_000 {
		t.Fatalf("healthy primary did not protect the event: %+v", got)
	}
	if stats := b.stats(); stats.Unpersisted != 0 || !stats.Blocked {
		t.Fatalf("direct settlement must remain paused until journal recovery: %+v", stats)
	}
	j.mu.Lock()
	j.fail = false
	j.mu.Unlock()
	if err := b.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.stats().Blocked {
		t.Fatal("write probe should reopen admission")
	}
}

func TestBillingUpdatesLimitBeforeWorkerAndNeverAgainOnRetry(t *testing.T) {
	store, hash := newBillingTestStore(t)
	failure := &settlementFailureStore{Store: store, down: true}
	j := openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db"))
	auth := newTestAuthService(t)
	auth.authTokenCostLimits[hash] = tokenCostLimit{limitMicroUSD: 1_000_000}
	srv := &Server{store: failure, authService: auth, billing: newBillingService(failure, j)}
	srv.updateTokenStatsAsync(hash, 3000, floatPtr(0.25), true, 1, false, &fwResult{InputTokens: 10}, "test-model")
	used, _, _ := auth.IsCostLimitExceeded(hash)
	if used != 250_000 {
		t.Fatalf("before worker cost=%d, want site price 250000", used)
	}
	if err := srv.billing.flush(context.Background()); err == nil {
		t.Fatal("expected database failure")
	}
	failure.mu.Lock()
	failure.down = false
	failure.mu.Unlock()
	if err := srv.billing.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	used, _, _ = auth.IsCostLimitExceeded(hash)
	if used != 250_000 {
		t.Fatalf("retry changed cache to %d", used)
	}
}

func TestBillingShutdownLeavesDurablePendingForNextStart(t *testing.T) {
	store, hash := newBillingTestStore(t)
	path := filepath.Join(t.TempDir(), "pending.db")
	j := openBillingTestJournal(t, path)
	b := newBillingService(&settlementFailureStore{Store: store, down: true}, j)
	b.start()
	release, err := b.acquire()
	if err != nil {
		t.Fatal(err)
	}
	b.record(billingTestEvent(hash))
	release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := b.close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened := openBillingTestJournal(t, path)
	if n, _ := reopened.Count(context.Background()); n != 1 {
		t.Fatalf("pending after close=%d", n)
	}
	if _, err := b.acquire(); err == nil {
		t.Fatal("closed service admitted new work")
	}
}

func TestBillingAdmissionCoversHTTPAndExistingWebsocketTurns(t *testing.T) {
	env := setupProxyTestEnv(t, nil, nil)
	j := &failingBillingJournal{billingJournal: openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db")), fail: true}
	b := newBillingService(env.store, j)
	env.server.billing = b
	// Connect before the persistence failure: each subsequent turn must still
	// consult billing admission, rather than relying on handshake-time state.
	conn := dialResponsesWebsocket(t, env.engine)
	t.Cleanup(func() {
		j.mu.Lock()
		j.fail = false
		j.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := b.close(ctx); err != nil {
			t.Error(err)
		}
	})
	b.record(billingTestEvent(model.HashToken("test-api-key")))
	httpResponse := doProxyRequest(t, env.engine, "/v1/chat/completions", map[string]any{
		"model": "test-model", "messages": []any{},
	}, nil)
	if httpResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("HTTP status=%d", httpResponse.Code)
	}
	if err := conn.WriteJSON(map[string]any{"type": "response.create", "model": "test-model", "input": "hello"}); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatal(err)
	}
	if event["type"] != "error" || event["status"] != float64(http.StatusServiceUnavailable) {
		t.Fatalf("WS billing error=%v", event)
	}
	errorBody, ok := event["error"].(map[string]any)
	if !ok || errorBody["code"] != "billing_unavailable" {
		t.Fatalf("wrong WS error=%v", event)
	}
	// Restore so cleanup can durably retain/ack the event.
	j.mu.Lock()
	j.fail = false
	j.mu.Unlock()
}

func TestBillingMetricsDoNotExposeSettlementIdentity(t *testing.T) {
	store, hash := newBillingTestStore(t)
	j := openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db"))
	b := newBillingService(store, j)
	b.record(billingTestEvent(hash))
	srv := &Server{billing: b}
	c, w := newTestContext(t, newRequest(http.MethodGet, "/admin/runtime-metrics", nil))
	srv.HandleRuntimeMetrics(c)
	resp := mustParseAPIResponse[map[string]any](t, w.Body.Bytes())
	stats, ok := resp.Data["billing"].(map[string]any)
	if !ok || stats["pending"] != float64(1) || stats["enabled"] != true {
		t.Fatalf("billing metrics=%v", resp.Data)
	}
	if strings.Contains(w.Body.String(), hash) || strings.Contains(w.Body.String(), "billing-event-1") {
		t.Fatal("metrics exposed settlement identity")
	}
}

func TestBillingShutdownWaitsForAdmittedCompletion(t *testing.T) {
	store, hash := newBillingTestStore(t)
	path := filepath.Join(t.TempDir(), "pending.db")
	j := openBillingTestJournal(t, path)
	b := newBillingService(&settlementFailureStore{Store: store, down: true}, j)
	release, err := b.acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown with in-flight request: %v", err)
	}
	// A deadline stops waiting, but must not close the journal under a turn
	// already admitted. Its late completion remains durable for restart.
	b.record(billingTestEvent(hash))
	release()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.close(ctx); err != nil {
		t.Fatal(err)
	}
	reopened := openBillingTestJournal(t, path)
	events, err := reopened.Pending(context.Background(), 10)
	if err != nil || len(events) != 1 || events[0].TokenHash != hash {
		t.Fatalf("late completion lost: events=%v err=%v", events, err)
	}
}

type ambiguousAckJournal struct {
	billingJournal
	loseAcknowledgement bool
}

func (j *ambiguousAckJournal) Ack(ctx context.Context, id string) error {
	if err := j.billingJournal.Ack(ctx, id); err != nil {
		return err
	}
	if j.loseAcknowledgement {
		j.loseAcknowledgement = false
		return errors.New("journal ack committed but confirmation lost")
	}
	return nil
}

func TestBillingRecoversAmbiguousJournalAcknowledgement(t *testing.T) {
	store, hash := newBillingTestStore(t)
	j := &ambiguousAckJournal{
		billingJournal:      openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db")),
		loseAcknowledgement: true,
	}
	b := newBillingService(store, j)
	b.record(billingTestEvent(hash))
	if err := b.flush(context.Background()); err == nil {
		t.Fatal("expected acknowledgement failure")
	}
	if _, err := b.acquire(); err == nil {
		t.Fatal("accepted new work before journal write probe")
	}
	if err := b.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stats := b.stats(); stats.Pending != 0 || stats.Blocked {
		t.Fatalf("ambiguous ack recovery: %+v", stats)
	}
	got, err := store.GetAuthTokenByValue(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.SuccessCount != 1 || got.CostUsedMicroUSD != 250_000 {
		t.Fatalf("ack failure repeated accounting: %+v", got)
	}
}

func TestBillingSuccessfulProxyPersistsUsageAndFreeKeyPrice(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "pending.db")
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-billing","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":20}}`)
	}))
	env := setupProxyTestEnv(t, []testChannel{{name: "free", models: "gpt-4o", costMultiplier: 0}}, map[int]string{0: upstream.URL})
	channels, err := env.store.ListConfigs(context.Background())
	if err != nil || len(channels) != 1 {
		t.Fatalf("test channel: %v %v", channels, err)
	}
	if err := env.store.UpdateAPIKeyMetadata(context.Background(), channels[0].ID, map[int]model.APIKey{
		0: {CostMultiplier: 0, CostMultiplierSet: true},
	}); err != nil {
		t.Fatal(err)
	}
	hash := model.HashToken("test-api-key")
	if err := env.store.CreateAuthToken(context.Background(), &model.AuthToken{
		Token: hash, IsActive: true, CostLimitMicroUSD: 1_000_000, MaxConcurrency: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := env.server.EnableDurableBilling(journalPath); err != nil {
		t.Fatal(err)
	}
	response := doProxyRequest(t, env.engine, "/v1/chat/completions", map[string]any{
		"model": "gpt-4o", "messages": []any{map[string]any{"role": "user", "content": "hello"}},
	}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("proxy response=%d %s", response.Code, response.Body.String())
	}
	if err := env.server.billing.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := env.store.GetAuthTokenByValue(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.SuccessCount != 1 || got.PromptTokensTotal != 10 || got.CompletionTokensTotal != 20 || got.TotalCostUSD <= 0 {
		t.Fatalf("successful route usage not settled: %+v", got)
	}
	if got.CostUsedMicroUSD != 0 || got.EffectiveCostUSD != 0 {
		t.Fatalf("free key was charged: %+v", got)
	}
}

func TestBillingJournalPathIsSeparateFromPrimary(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "primary.db")
	t.Setenv("SQLITE_PATH", primary)
	t.Setenv("PIVOTFLOW_BILLING_JOURNAL_PATH", "")
	path, err := BillingJournalPath()
	if err != nil || path != filepath.Join(dir, "billing-pending.db") {
		t.Fatalf("default path=%q err=%v", path, err)
	}
	t.Setenv("PIVOTFLOW_BILLING_JOURNAL_PATH", primary)
	if _, err := BillingJournalPath(); err == nil {
		t.Fatal("journal must not open the primary SQLite file")
	}
	t.Setenv("SQLITE_PATH", "file:primary?mode=memory")
	t.Setenv("PIVOTFLOW_BILLING_JOURNAL_PATH", "")
	if _, err := BillingJournalPath(); err == nil {
		t.Fatal("URI needs an explicit persistent journal path")
	}
	t.Setenv("PIVOTFLOW_BILLING_JOURNAL_PATH", filepath.Join(dir, "durable.db"))
	if _, err := BillingJournalPath(); err != nil {
		t.Fatalf("explicit journal with URI primary: %v", err)
	}
}

func TestBillingNegativeUpstreamUsageDoesNotPoisonJournal(t *testing.T) {
	store, hash := newBillingTestStore(t)
	j := openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db"))
	b := newBillingService(store, j)
	srv := &Server{store: store, billing: b}
	srv.updateTokenStatsAsync(hash, 1, nil, true, 1, false, &fwResult{
		InputTokens: -10, OutputTokens: 20, CacheReadInputTokens: -1,
	}, "gpt-4o")
	if err := b.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b.stats().Blocked {
		t.Fatal("upstream negative counters blocked healthy journal")
	}
	got, err := store.GetAuthTokenByValue(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.SuccessCount != 1 || got.PromptTokensTotal != 0 || got.CacheReadTokensTotal != 0 || got.CompletionTokensTotal != 20 {
		t.Fatalf("invalid usage affected valid counters: %+v", got)
	}
}

func TestBillingInvalidAmountsPersistUsageWithoutBlocking(t *testing.T) {
	for name, cost := range map[string]float64{
		"negative": -1, "NaN": math.NaN(), "infinite": math.Inf(1),
		"out of microdollar range": 1e30,
	} {
		t.Run(name, func(t *testing.T) {
			store, hash := newBillingTestStore(t)
			j := openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db"))
			failure := &settlementFailureStore{Store: store, loseAcknowledgement: true}
			b := newBillingService(failure, j)
			auth := newTestAuthService(t)
			auth.authTokenCostLimits[hash] = tokenCostLimit{limitMicroUSD: 1_000_000}
			srv := &Server{store: failure, billing: b, authService: auth}
			srv.updateTokenStatsAsync(hash, 1, &cost, true, 1, false, &fwResult{InputTokens: 10, OutputTokens: 20}, "gpt-4o")
			pending, err := j.Pending(context.Background(), 10)
			if err != nil || len(pending) != 1 || !pending[0].CostInvalid {
				t.Fatalf("invalid amount needs a durable marker: %v %v", pending, err)
			}
			if err := b.flush(context.Background()); err == nil {
				t.Fatal("expected ambiguous primary commit")
			}
			if err := b.flush(context.Background()); err != nil {
				t.Fatalf("invalid amount must not poison persistence: %v", err)
			}
			release, err := b.acquire()
			if err != nil {
				t.Fatalf("invalid upstream amount stopped new work: %v", err)
			}
			release()
			got, err := store.GetAuthTokenByValue(context.Background(), hash)
			if err != nil {
				t.Fatal(err)
			}
			if got.SuccessCount != 1 || got.PromptTokensTotal != 10 || got.CompletionTokensTotal != 20 ||
				got.CostUsedMicroUSD != 0 || got.TotalCostUSD != 0 || got.EffectiveCostUSD != 0 {
				t.Fatalf("invalid amount contaminated accounting: %+v", got)
			}
			if used, _, _ := auth.IsCostLimitExceeded(hash); used != 0 {
				t.Fatalf("invalid amount contaminated limit: %d", used)
			}
			if b.stats().InvalidCosts != 1 {
				t.Fatalf("replay changed invalid amount count: %+v", b.stats())
			}
		})
	}
}

func TestBillingInvalidAmountDoesNotHoldOtherEventsDuringJournalFailure(t *testing.T) {
	store, hash := newBillingTestStore(t)
	j := &failingBillingJournal{billingJournal: openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db")), fail: true}
	b := newBillingService(store, j)
	srv := &Server{store: store, billing: b}
	invalid := math.Inf(1)
	srv.updateTokenStatsAsync(hash, 1, &invalid, true, 1, false, &fwResult{InputTokens: 10}, "gpt-4o")
	srv.updateTokenStatsAsync(hash, 1, floatPtr(0.25), true, 1, false, &fwResult{InputTokens: 10}, "gpt-4o")
	if err := b.flush(context.Background()); err == nil {
		t.Fatal("expected journal failure")
	}
	if stats := b.stats(); stats.Unpersisted != 0 || stats.InvalidCosts != 1 || !stats.Blocked {
		t.Fatalf("mixed events failed to settle through primary: %+v", stats)
	}
	j.mu.Lock()
	j.fail = false
	j.mu.Unlock()
	if err := b.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetAuthTokenByValue(context.Background(), hash)
	if err != nil {
		t.Fatal(err)
	}
	if got.SuccessCount != 2 || got.PromptTokensTotal != 20 || got.CostUsedMicroUSD != 250_000 {
		t.Fatalf("mixed fallback events lost or duplicated: %+v", got)
	}
}

func TestBillingProxyInvalidUsageStillAcceptsNextRequest(t *testing.T) {
	for name, usage := range map[string]string{
		"negative counters": `{"prompt_tokens":-10,"completion_tokens":20}`,
		"overflowing cost":  `{"prompt_tokens":10,"completion_tokens":1000000000000000000}`,
	} {
		t.Run(name, func(t *testing.T) {
			journalPath := filepath.Join(t.TempDir(), "pending.db")
			var calls atomic.Int32
			upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				currentUsage := usage
				if calls.Add(1) > 1 {
					currentUsage = `{"prompt_tokens":10,"completion_tokens":20}`
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"chatcmpl-usage","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":`+currentUsage+`}`)
			}))
			env := setupProxyTestEnv(t, []testChannel{{name: "usage", models: "gpt-4o", costMultiplier: 1}}, map[int]string{0: upstream.URL})
			hash := model.HashToken("test-api-key")
			if err := env.store.CreateAuthToken(context.Background(), &model.AuthToken{
				Token: hash, IsActive: true, CostLimitMicroUSD: 1_000_000, MaxConcurrency: 2,
			}); err != nil {
				t.Fatal(err)
			}
			if err := env.server.EnableDurableBilling(journalPath); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				response := doProxyRequest(t, env.engine, "/v1/chat/completions", map[string]any{
					"model": "gpt-4o", "messages": []any{map[string]any{"role": "user", "content": "hello"}},
				}, nil)
				if response.Code != http.StatusOK {
					t.Fatalf("following request blocked: %d %s", response.Code, response.Body.String())
				}
			}
			if err := env.server.billing.flush(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, err := env.store.GetAuthTokenByValue(context.Background(), hash)
			if err != nil {
				t.Fatal(err)
			}
			if got.SuccessCount != 2 || got.CostUsedMicroUSD != 225 {
				t.Fatalf("malformed then normal accounting: %+v", got)
			}
			if name == "overflowing cost" && env.server.billing.stats().InvalidCosts != 1 {
				t.Fatalf("missing invalid cost alert: %+v", env.server.billing.stats())
			}
		})
	}
}
