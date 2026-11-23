package sql_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
	"github.com/yzgolden86/PivotFlow/internal/util"
)

func newTokenSettlementStore(t *testing.T, dbPath string) *sqlstore.SQLStore {
	t.Helper()

	store, err := storage.CreateSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	concrete, ok := store.(*sqlstore.SQLStore)
	if !ok {
		t.Fatalf("store type=%T, want *sql.SQLStore", store)
	}
	return concrete
}

func createSettlementToken(t *testing.T, ctx context.Context, store storage.Store, tokenHash string) {
	t.Helper()

	if err := store.CreateAuthToken(ctx, &model.AuthToken{
		Token:       tokenHash,
		Description: "settlement test",
		CreatedAt:   time.Now().Add(-24 * time.Hour),
		IsActive:    true,
	}); err != nil {
		t.Fatalf("create auth token: %v", err)
	}
}

func settlementEvent(id, tokenHash string, occurredAt int64) model.TokenSettlement {
	return model.TokenSettlement{
		ID:                  id,
		TokenHash:           tokenHash,
		OccurredAt:          occurredAt,
		Duration:            2.5,
		IsStreaming:         false,
		FirstByteTime:       0,
		PromptTokens:        11,
		CompletionTokens:    7,
		CacheReadTokens:     3,
		CacheCreationTokens: 2,
		CostUSD:             0.4,
		EffectiveCostUSD:    0.2,
	}
}

func TestTokenSettlement_JSONContract(t *testing.T) {
	data, err := json.Marshal(model.TokenSettlement{})
	if err != nil {
		t.Fatalf("marshal settlement: %v", err)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("unmarshal settlement JSON: %v", err)
	}
	want := []string{
		"id", "token_hash", "occurred_at", "duration", "is_streaming",
		"first_byte_time", "prompt_tokens", "completion_tokens",
		"cache_read_tokens", "cache_creation_tokens", "cost_usd",
		"effective_cost_usd",
	}
	for _, field := range want {
		if _, ok := fields[field]; !ok {
			t.Errorf("JSON contract missing %q: %s", field, data)
		}
	}
	if _, ok := fields["cost_invalid"]; ok {
		t.Fatalf("zero cost_invalid should be omitted: %s", data)
	}

	flaggedData, err := json.Marshal(model.TokenSettlement{CostInvalid: true})
	if err != nil {
		t.Fatalf("marshal flagged settlement: %v", err)
	}
	if err := json.Unmarshal(flaggedData, &fields); err != nil {
		t.Fatalf("unmarshal flagged settlement JSON: %v", err)
	}
	if value, ok := fields["cost_invalid"]; !ok || string(value) != "true" {
		t.Fatalf("cost_invalid=%s, want true in %s", value, flaggedData)
	}
}

func TestValidateTokenSettlementCosts(t *testing.T) {
	valid := []struct {
		name      string
		cost      float64
		effective float64
	}{
		{name: "zero", cost: 0, effective: 0},
		{name: "priced", cost: 0.75, effective: 0.3},
		{name: "large finite", cost: 1e20, effective: 1e6},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			if err := model.ValidateTokenSettlementCosts(tt.cost, tt.effective); err != nil {
				t.Fatalf("valid costs rejected: %v", err)
			}
		})
	}

	invalid := []struct {
		name      string
		cost      float64
		effective float64
	}{
		{name: "NaN cost", cost: math.NaN(), effective: 0},
		{name: "infinite cost", cost: math.Inf(1), effective: 0},
		{name: "negative cost", cost: -0.01, effective: 0},
		{name: "NaN effective", cost: 0, effective: math.NaN()},
		{name: "infinite effective", cost: 0, effective: math.Inf(1)},
		{name: "negative effective", cost: 0, effective: -0.01},
		{name: "effective exceeds microUSD int64", cost: 0, effective: math.MaxFloat64},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			if err := model.ValidateTokenSettlementCosts(tt.cost, tt.effective); err == nil {
				t.Fatal("invalid costs were accepted")
			}
		})
	}
}

func TestApplyTokenSettlement_DuplicateDoesNotDoubleIncrement(t *testing.T) {
	ctx := context.Background()
	store := newTokenSettlementStore(t, filepath.Join(t.TempDir(), "settlement-duplicate.db"))
	tokenHash := model.HashToken("settlement-duplicate")
	createSettlementToken(t, ctx, store, tokenHash)

	occurredAt := time.Now().Add(-2 * time.Hour).UnixMilli()
	event := settlementEvent("event-duplicate", tokenHash, occurredAt)
	if err := store.ApplyTokenSettlement(ctx, event); err != nil {
		t.Fatalf("first settlement: %v", err)
	}
	if err := store.ApplyTokenSettlement(ctx, event); err != nil {
		t.Fatalf("duplicate settlement must succeed: %v", err)
	}

	got, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get auth token: %v", err)
	}
	if got.SuccessCount != 1 {
		t.Fatalf("success_count=%d, want 1", got.SuccessCount)
	}
	if got.PromptTokensTotal != event.PromptTokens ||
		got.CompletionTokensTotal != event.CompletionTokens ||
		got.CacheReadTokensTotal != event.CacheReadTokens ||
		got.CacheCreationTokensTotal != event.CacheCreationTokens {
		t.Fatalf("token totals were double incremented: %+v", got)
	}
	if got.TotalCostUSD != event.CostUSD || got.EffectiveCostUSD != event.EffectiveCostUSD {
		t.Fatalf("cost was double incremented: total=%v effective=%v", got.TotalCostUSD, got.EffectiveCostUSD)
	}
	if got.CostUsedMicroUSD != util.USDToMicroUSD(event.EffectiveCostUSD) {
		t.Fatalf("cost_used_microusd=%d, want %d", got.CostUsedMicroUSD, util.USDToMicroUSD(event.EffectiveCostUSD))
	}
	if got.LastUsedAt == nil || *got.LastUsedAt != occurredAt {
		t.Fatalf("last_used_at=%v, want occurrence time %d", got.LastUsedAt, occurredAt)
	}
}

func TestApplyTokenSettlement_DuplicateIDMismatchedPayloadConflicts(t *testing.T) {
	ctx := context.Background()
	store := newTokenSettlementStore(t, filepath.Join(t.TempDir(), "settlement-conflict.db"))
	originalHash := model.HashToken("settlement-conflict-original")
	otherHash := model.HashToken("settlement-conflict-other")
	createSettlementToken(t, ctx, store, originalHash)
	createSettlementToken(t, ctx, store, otherHash)

	occurredAt := time.Now().Add(-time.Hour).UnixMilli()
	original := settlementEvent("event-conflict", originalHash, occurredAt)
	if err := store.ApplyTokenSettlement(ctx, original); err != nil {
		t.Fatalf("original settlement: %v", err)
	}

	tests := []struct {
		name  string
		event model.TokenSettlement
	}{
		{name: "different token", event: func() model.TokenSettlement {
			event := original
			event.TokenHash = otherHash
			return event
		}()},
		{name: "different missing token", event: func() model.TokenSettlement {
			event := original
			event.TokenHash = model.HashToken("settlement-conflict-missing")
			return event
		}()},
		{name: "different total cost", event: func() model.TokenSettlement {
			event := original
			event.CostUSD += 0.01
			return event
		}()},
		{name: "different effective cost", event: func() model.TokenSettlement {
			event := original
			event.EffectiveCostUSD += 0.01
			return event
		}()},
		{name: "different tokens", event: func() model.TokenSettlement {
			event := original
			event.PromptTokens += 1
			return event
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.ApplyTokenSettlement(ctx, tt.event)
			if err == nil {
				t.Fatal("mismatched duplicate payload was silently acknowledged")
			}
			if !errors.Is(err, sqlstore.ErrTokenSettlementConflict) {
				t.Fatalf("error=%v, want ErrTokenSettlementConflict", err)
			}
		})
	}

	originalToken, err := store.GetAuthTokenByValue(ctx, originalHash)
	if err != nil {
		t.Fatalf("get original token: %v", err)
	}
	if originalToken.SuccessCount != 1 || originalToken.PromptTokensTotal != original.PromptTokens {
		t.Fatalf("original receipt was applied more than once: %+v", originalToken)
	}
	if originalToken.TotalCostUSD != original.CostUSD || originalToken.EffectiveCostUSD != original.EffectiveCostUSD {
		t.Fatalf("original costs changed: total=%v effective=%v", originalToken.TotalCostUSD, originalToken.EffectiveCostUSD)
	}
	otherToken, err := store.GetAuthTokenByValue(ctx, otherHash)
	if err != nil {
		t.Fatalf("get other token: %v", err)
	}
	if otherToken.SuccessCount != 0 || otherToken.PromptTokensTotal != 0 || otherToken.EffectiveCostUSD != 0 {
		t.Fatalf("mismatched token receipt changed other token: %+v", otherToken)
	}
}

func TestApplyTokenSettlement_ConcurrentReplayAppliesOnce(t *testing.T) {
	ctx := context.Background()
	store := newTokenSettlementStore(t, filepath.Join(t.TempDir(), "settlement-concurrent.db"))
	tokenHash := model.HashToken("settlement-concurrent")
	createSettlementToken(t, ctx, store, tokenHash)

	event := settlementEvent("event-concurrent", tokenHash, time.Now().UnixMilli())
	const workers = 24
	start := make(chan struct{})
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			errs <- store.ApplyTokenSettlement(ctx, event)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent settlement: %v", err)
		}
	}

	got, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get auth token: %v", err)
	}
	if got.SuccessCount != 1 {
		t.Fatalf("success_count=%d, want 1 after concurrent replay", got.SuccessCount)
	}
	if got.PromptTokensTotal != event.PromptTokens ||
		got.CompletionTokensTotal != event.CompletionTokens ||
		got.CacheReadTokensTotal != event.CacheReadTokens ||
		got.CacheCreationTokensTotal != event.CacheCreationTokens {
		t.Fatalf("token totals=%+v, want one application", got)
	}
	if got.TotalCostUSD != event.CostUSD || got.EffectiveCostUSD != event.EffectiveCostUSD {
		t.Fatalf("costs were applied more than once: total=%v effective=%v", got.TotalCostUSD, got.EffectiveCostUSD)
	}
}

func TestApplyTokenSettlement_MissingTokenRollsBackReceipt(t *testing.T) {
	ctx := context.Background()
	store := newTokenSettlementStore(t, filepath.Join(t.TempDir(), "settlement-missing.db"))
	tokenHash := model.HashToken("settlement-missing")
	event := settlementEvent("event-missing", tokenHash, time.Now().UnixMilli())

	err := store.ApplyTokenSettlement(ctx, event)
	if !errors.Is(err, model.ErrAuthTokenNotFound) {
		t.Fatalf("missing token error=%v, want ErrAuthTokenNotFound", err)
	}
	var receipts int
	if err := store.QueryRowContext(ctx, "SELECT COUNT(*) FROM token_settlements WHERE id = ?", event.ID).Scan(&receipts); err != nil {
		t.Fatalf("count rolled-back receipts: %v", err)
	}
	if receipts != 0 {
		t.Fatalf("receipt rows=%d after missing token, want 0", receipts)
	}

	createSettlementToken(t, ctx, store, tokenHash)
	if err := store.ApplyTokenSettlement(ctx, event); err != nil {
		t.Fatalf("retry after token creation: %v", err)
	}
	got, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get auth token: %v", err)
	}
	if got.SuccessCount != 1 {
		t.Fatalf("success_count=%d after rollback and retry, want 1", got.SuccessCount)
	}

	if err := store.DeleteAuthToken(ctx, got.ID); err != nil {
		t.Fatalf("delete auth token: %v", err)
	}
	err = store.ApplyTokenSettlement(ctx, event)
	if !errors.Is(err, model.ErrAuthTokenNotFound) {
		t.Fatalf("deleted token replay error=%v, want ErrAuthTokenNotFound", err)
	}
	if _, err := store.GetAuthTokenByValue(ctx, tokenHash); !errors.Is(err, model.ErrAuthTokenNotFound) {
		t.Fatalf("settlement recreated deleted token: %v", err)
	}
	if err := store.QueryRowContext(ctx, "SELECT COUNT(*) FROM token_settlements WHERE id = ?", event.ID).Scan(&receipts); err != nil {
		t.Fatalf("count retained receipt: %v", err)
	}
	if receipts != 1 {
		t.Fatalf("receipt rows=%d after token deletion, want 1", receipts)
	}
}

func TestApplyTokenSettlement_RecordedTimeNeverRegresses(t *testing.T) {
	ctx := context.Background()
	store := newTokenSettlementStore(t, filepath.Join(t.TempDir(), "settlement-time.db"))
	tokenHash := model.HashToken("settlement-time")
	createSettlementToken(t, ctx, store, tokenHash)

	newer := time.Now().Add(-time.Hour).UnixMilli()
	older := newer - 30*time.Minute.Milliseconds()
	if err := store.ApplyTokenSettlement(ctx, settlementEvent("event-newer", tokenHash, newer)); err != nil {
		t.Fatalf("newer settlement: %v", err)
	}
	if err := store.ApplyTokenSettlement(ctx, settlementEvent("event-older", tokenHash, older)); err != nil {
		t.Fatalf("older settlement: %v", err)
	}

	got, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get auth token: %v", err)
	}
	if got.LastUsedAt == nil || *got.LastUsedAt != newer {
		t.Fatalf("last_used_at=%v, want newer occurrence %d", got.LastUsedAt, newer)
	}
	if got.SuccessCount != 2 {
		t.Fatalf("success_count=%d, want 2", got.SuccessCount)
	}
}

func TestApplyTokenSettlement_FreeAndSitePricingCountAsSuccess(t *testing.T) {
	ctx := context.Background()
	store := newTokenSettlementStore(t, filepath.Join(t.TempDir(), "settlement-pricing.db"))
	tokenHash := model.HashToken("settlement-pricing")
	createSettlementToken(t, ctx, store, tokenHash)

	now := time.Now()
	free := settlementEvent("event-free", tokenHash, now.Add(-time.Minute).UnixMilli())
	free.PromptTokens = 0
	free.CompletionTokens = 0
	free.CacheReadTokens = 0
	free.CacheCreationTokens = 0
	free.CostUSD = 0
	free.EffectiveCostUSD = 0
	if err := store.ApplyTokenSettlement(ctx, free); err != nil {
		t.Fatalf("free settlement: %v", err)
	}

	sitePriced := settlementEvent("event-site-priced", tokenHash, now.UnixMilli())
	sitePriced.CostUSD = 0.75
	sitePriced.EffectiveCostUSD = 0.3
	if err := store.ApplyTokenSettlement(ctx, sitePriced); err != nil {
		t.Fatalf("site-priced settlement: %v", err)
	}

	got, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get auth token: %v", err)
	}
	if got.SuccessCount != 2 {
		t.Fatalf("success_count=%d, want free and priced requests to count", got.SuccessCount)
	}
	if got.TotalCostUSD != sitePriced.CostUSD {
		t.Fatalf("total_cost_usd=%v, want %v", got.TotalCostUSD, sitePriced.CostUSD)
	}
	if got.EffectiveCostUSD != sitePriced.EffectiveCostUSD {
		t.Fatalf("effective_cost_usd=%v, want %v", got.EffectiveCostUSD, sitePriced.EffectiveCostUSD)
	}
	if got.CostUsedMicroUSD != util.USDToMicroUSD(sitePriced.EffectiveCostUSD) {
		t.Fatalf("cost_used_microusd=%d, want %d", got.CostUsedMicroUSD, util.USDToMicroUSD(sitePriced.EffectiveCostUSD))
	}
}

func TestApplyTokenSettlement_StreamingUpdatesStreamStatsOnly(t *testing.T) {
	ctx := context.Background()
	store := newTokenSettlementStore(t, filepath.Join(t.TempDir(), "settlement-streaming.db"))
	tokenHash := model.HashToken("settlement-streaming")
	createSettlementToken(t, ctx, store, tokenHash)

	event := settlementEvent("event-streaming", tokenHash, time.Now().UnixMilli())
	event.IsStreaming = true
	event.FirstByteTime = 0.25
	event.Duration = 3
	if err := store.ApplyTokenSettlement(ctx, event); err != nil {
		t.Fatalf("streaming settlement: %v", err)
	}

	got, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get auth token: %v", err)
	}
	if got.StreamCount != 1 || got.StreamAvgTTFB != event.FirstByteTime {
		t.Fatalf("stream stats=(%d, %v), want (1, %v)", got.StreamCount, got.StreamAvgTTFB, event.FirstByteTime)
	}
	if got.NonStreamCount != 0 || got.NonStreamAvgRT != 0 {
		t.Fatalf("non-stream stats changed for streaming request: count=%d avg=%v", got.NonStreamCount, got.NonStreamAvgRT)
	}
}

func TestSyncTokenAccounting_StaleSnapshotDoesNotRegressReplica(t *testing.T) {
	ctx := context.Background()
	store := newTokenSettlementStore(t, filepath.Join(t.TempDir(), "settlement-stale-copy.db"))
	tokenHash := model.HashToken("settlement-stale-copy")
	createSettlementToken(t, ctx, store, tokenHash)

	occurredAt := time.Now().UnixMilli()
	if err := store.ApplyTokenSettlement(ctx, settlementEvent("event-current", tokenHash, occurredAt)); err != nil {
		t.Fatalf("apply current settlement: %v", err)
	}
	current, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get current token: %v", err)
	}

	stale := *current
	stale.SuccessCount = 0
	stale.PromptTokensTotal = 0
	stale.CompletionTokensTotal = 0
	stale.CacheReadTokensTotal = 0
	stale.CacheCreationTokensTotal = 0
	stale.TotalCostUSD = 0
	stale.EffectiveCostUSD = 0
	stale.CostUsedMicroUSD = 0
	staleOccurredAt := occurredAt - 1
	stale.LastUsedAt = &staleOccurredAt
	if err := store.SyncTokenAccounting(ctx, &stale); err != nil {
		t.Fatalf("sync stale accounting snapshot: %v", err)
	}

	got, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get token after stale sync: %v", err)
	}
	if got.SuccessCount != current.SuccessCount ||
		got.PromptTokensTotal != current.PromptTokensTotal ||
		got.TotalCostUSD != current.TotalCostUSD {
		t.Fatalf("stale snapshot regressed replica: before=%+v after=%+v", current, got)
	}
}

func TestSyncTokenAccounting_AcceptsDelayedSettlementAfterLaterLastUsed(t *testing.T) {
	ctx := context.Background()
	store := newTokenSettlementStore(t, filepath.Join(t.TempDir(), "settlement-delayed.db"))
	tokenHash := model.HashToken("settlement-delayed")
	createSettlementToken(t, ctx, store, tokenHash)

	settledAt := time.Now().Add(-time.Hour)
	lastUsedAt := settledAt.Add(30 * time.Minute)
	if err := store.UpdateTokenLastUsed(ctx, tokenHash, lastUsedAt); err != nil {
		t.Fatalf("advance replica last used: %v", err)
	}
	current, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get replica token: %v", err)
	}

	canonical := *current
	canonical.SuccessCount = 1
	canonical.PromptTokensTotal = 17
	canonical.CompletionTokensTotal = 9
	canonical.TotalCostUSD = 0.6
	canonical.EffectiveCostUSD = 0.3
	canonical.CostUsedMicroUSD = util.USDToMicroUSD(0.3)
	settledAtMs := settledAt.UnixMilli()
	canonical.LastUsedAt = &settledAtMs

	if err := store.SyncTokenAccounting(ctx, &canonical); err != nil {
		t.Fatalf("sync delayed settlement: %v", err)
	}

	got, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get token after delayed sync: %v", err)
	}
	if got.SuccessCount != 1 || got.PromptTokensTotal != 17 || got.CompletionTokensTotal != 9 {
		t.Fatalf("delayed settlement counters were skipped: %+v", got)
	}
	if got.TotalCostUSD != canonical.TotalCostUSD ||
		got.EffectiveCostUSD != canonical.EffectiveCostUSD ||
		got.CostUsedMicroUSD != canonical.CostUsedMicroUSD {
		t.Fatalf("delayed settlement cost was skipped: %+v", got)
	}
	if got.LastUsedAt == nil || *got.LastUsedAt != lastUsedAt.UnixMilli() {
		t.Fatalf("last_used_at=%v, want later use timestamp %d", got.LastUsedAt, lastUsedAt.UnixMilli())
	}
}

func TestSyncTokenAccounting_EqualTimestampStaleSnapshotDoesNotRegress(t *testing.T) {
	ctx := context.Background()
	store := newTokenSettlementStore(t, filepath.Join(t.TempDir(), "settlement-equal-time.db"))
	tokenHash := model.HashToken("settlement-equal-time")
	createSettlementToken(t, ctx, store, tokenHash)

	occurredAt := time.Now().UnixMilli()
	event := settlementEvent("event-equal-time", tokenHash, occurredAt)
	if err := store.ApplyTokenSettlement(ctx, event); err != nil {
		t.Fatalf("apply settlement: %v", err)
	}
	current, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get current token: %v", err)
	}

	stale := *current
	stale.SuccessCount = 0
	stale.PromptTokensTotal = 0
	stale.CompletionTokensTotal = 0
	stale.CacheReadTokensTotal = 0
	stale.CacheCreationTokensTotal = 0
	stale.TotalCostUSD = 0
	stale.EffectiveCostUSD = 0
	stale.CostUsedMicroUSD = 0
	if err := store.SyncTokenAccounting(ctx, &stale); err != nil {
		t.Fatalf("sync equal-time stale snapshot: %v", err)
	}

	got, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get token after equal-time sync: %v", err)
	}
	if got.SuccessCount != current.SuccessCount ||
		got.PromptTokensTotal != current.PromptTokensTotal ||
		got.CompletionTokensTotal != current.CompletionTokensTotal ||
		got.TotalCostUSD != current.TotalCostUSD ||
		got.EffectiveCostUSD != current.EffectiveCostUSD ||
		got.CostUsedMicroUSD != current.CostUsedMicroUSD {
		t.Fatalf("equal-time stale snapshot regressed counters: before=%+v after=%+v", current, got)
	}
}

func TestSyncTokenAccounting_StaleSnapshotStillAdvancesLastUsed(t *testing.T) {
	ctx := context.Background()
	store := newTokenSettlementStore(t, filepath.Join(t.TempDir(), "settlement-stale-time.db"))
	tokenHash := model.HashToken("settlement-stale-time")
	createSettlementToken(t, ctx, store, tokenHash)

	occurredAt := time.Now().Add(-time.Hour).UnixMilli()
	if err := store.ApplyTokenSettlement(ctx, settlementEvent("event-stale-time", tokenHash, occurredAt)); err != nil {
		t.Fatalf("apply settlement: %v", err)
	}
	current, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get current token: %v", err)
	}

	stale := *current
	stale.SuccessCount = 0
	stale.PromptTokensTotal = 0
	stale.TotalCostUSD = 0
	stale.EffectiveCostUSD = 0
	stale.CostUsedMicroUSD = 0
	newer := occurredAt + time.Hour.Milliseconds()
	stale.LastUsedAt = &newer
	if err := store.SyncTokenAccounting(ctx, &stale); err != nil {
		t.Fatalf("sync stale accounting with newer last used: %v", err)
	}

	got, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get token after stale sync: %v", err)
	}
	if got.SuccessCount != current.SuccessCount ||
		got.PromptTokensTotal != current.PromptTokensTotal ||
		got.TotalCostUSD != current.TotalCostUSD {
		t.Fatalf("stale snapshot regressed counters: before=%+v after=%+v", current, got)
	}
	if got.LastUsedAt == nil || *got.LastUsedAt != newer {
		t.Fatalf("last_used_at=%v, want independently advanced timestamp %d", got.LastUsedAt, newer)
	}
}

func TestApplyTokenSettlement_ValidatesInput(t *testing.T) {
	ctx := context.Background()
	store := newTokenSettlementStore(t, filepath.Join(t.TempDir(), "settlement-validation.db"))
	valid := settlementEvent("event-valid", "token-hash", time.Now().UnixMilli())

	tests := []struct {
		name  string
		event model.TokenSettlement
	}{
		{name: "blank id", event: func() model.TokenSettlement {
			e := valid
			e.ID = " "
			return e
		}()},
		{name: "blank token hash", event: func() model.TokenSettlement {
			e := valid
			e.TokenHash = ""
			return e
		}()},
		{name: "missing occurrence time", event: func() model.TokenSettlement {
			e := valid
			e.OccurredAt = 0
			return e
		}()},
		{name: "negative duration", event: func() model.TokenSettlement {
			e := valid
			e.Duration = -1
			return e
		}()},
		{name: "NaN first byte time", event: func() model.TokenSettlement {
			e := valid
			e.FirstByteTime = math.NaN()
			return e
		}()},
		{name: "infinite cost", event: func() model.TokenSettlement {
			e := valid
			e.CostUSD = math.Inf(1)
			return e
		}()},
		{name: "negative effective cost", event: func() model.TokenSettlement {
			e := valid
			e.EffectiveCostUSD = -0.01
			return e
		}()},
		{name: "effective cost exceeds micro-dollar int64", event: func() model.TokenSettlement {
			e := valid
			e.EffectiveCostUSD = math.MaxFloat64
			return e
		}()},
		{name: "negative prompt tokens", event: func() model.TokenSettlement {
			e := valid
			e.PromptTokens = -1
			return e
		}()},
		{name: "negative completion tokens", event: func() model.TokenSettlement {
			e := valid
			e.CompletionTokens = -1
			return e
		}()},
		{name: "negative cache read tokens", event: func() model.TokenSettlement {
			e := valid
			e.CacheReadTokens = -1
			return e
		}()},
		{name: "negative cache creation tokens", event: func() model.TokenSettlement {
			e := valid
			e.CacheCreationTokens = -1
			return e
		}()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := store.ApplyTokenSettlement(ctx, tt.event); err == nil {
				t.Fatal("invalid settlement was accepted")
			}
		})
	}
}

func TestApplyTokenSettlement_RejectsCostBeyondMicroUSDRange(t *testing.T) {
	ctx := context.Background()
	store := newTokenSettlementStore(t, filepath.Join(t.TempDir(), "settlement-micro-usd-overflow.db"))
	tokenHash := model.HashToken("settlement-micro-usd-overflow")
	createSettlementToken(t, ctx, store, tokenHash)

	event := settlementEvent("event-micro-usd-overflow", tokenHash, time.Now().UnixMilli())
	event.EffectiveCostUSD = math.MaxFloat64
	event.CostInvalid = true
	if err := store.ApplyTokenSettlement(ctx, event); err == nil {
		t.Fatal("effective cost beyond int64 micro-dollars was accepted despite invalid flag")
	}

	got, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if got.SuccessCount != 0 || got.CostUsedMicroUSD != 0 {
		t.Fatalf("overflowing settlement changed accounting: %+v", got)
	}
	var receipts int
	if err := store.QueryRowContext(ctx, "SELECT COUNT(*) FROM token_settlements WHERE id = ?", event.ID).Scan(&receipts); err != nil {
		t.Fatalf("count rejected receipt: %v", err)
	}
	if receipts != 0 {
		t.Fatalf("rejected settlement stored %d receipts, want 0", receipts)
	}
}

func TestApplyTokenSettlement_CostInvalidFlagPersistsAndConflicts(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "settlement-cost-invalid.db")
	tokenHash := model.HashToken("settlement-cost-invalid")
	event := settlementEvent("event-cost-invalid", tokenHash, time.Now().Add(-time.Hour).UnixMilli())
	event.CostUSD = 0
	event.EffectiveCostUSD = 0
	event.CostInvalid = true

	first := newTokenSettlementStore(t, dbPath)
	createSettlementToken(t, ctx, first, tokenHash)
	if err := first.ApplyTokenSettlement(ctx, event); err != nil {
		t.Fatalf("apply invalid-cost placeholder: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	reopened := newTokenSettlementStore(t, dbPath)
	if err := reopened.ApplyTokenSettlement(ctx, event); err != nil {
		t.Fatalf("duplicate placeholder after reopen: %v", err)
	}

	var (
		costInvalid int
		costUSD     float64
		effective   float64
	)
	if err := reopened.QueryRowContext(ctx, `
		SELECT cost_invalid, cost_usd, effective_cost_usd
		FROM token_settlements
		WHERE id = ?
	`, event.ID).Scan(&costInvalid, &costUSD, &effective); err != nil {
		t.Fatalf("read persisted placeholder: %v", err)
	}
	if costInvalid != 1 || costUSD != 0 || effective != 0 {
		t.Fatalf("placeholder=(%d, %v, %v), want (1, 0, 0)", costInvalid, costUSD, effective)
	}

	changed := event
	changed.CostInvalid = false
	err := reopened.ApplyTokenSettlement(ctx, changed)
	if !errors.Is(err, sqlstore.ErrTokenSettlementConflict) {
		t.Fatalf("changed flag error=%v, want ErrTokenSettlementConflict", err)
	}

	got, err := reopened.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get auth token: %v", err)
	}
	if got.SuccessCount != 1 || got.PromptTokensTotal != event.PromptTokens {
		t.Fatalf("placeholder was applied more than once: %+v", got)
	}
}

func TestApplyTokenSettlement_ReceiptSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "settlement-reopen.db")
	tokenHash := model.HashToken("settlement-reopen")
	event := settlementEvent("event-reopen", tokenHash, time.Now().Add(-time.Hour).UnixMilli())

	first := newTokenSettlementStore(t, dbPath)
	createSettlementToken(t, ctx, first, tokenHash)
	if err := first.ApplyTokenSettlement(ctx, event); err != nil {
		t.Fatalf("initial settlement: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	reopened := newTokenSettlementStore(t, dbPath)
	if err := reopened.ApplyTokenSettlement(ctx, event); err != nil {
		t.Fatalf("replay after reopen: %v", err)
	}
	got, err := reopened.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get auth token after reopen: %v", err)
	}
	if got.SuccessCount != 1 || got.PromptTokensTotal != event.PromptTokens {
		t.Fatalf("receipt did not survive reopen: success=%d prompt=%d", got.SuccessCount, got.PromptTokensTotal)
	}
}
