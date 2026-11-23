package storage

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestHybridStore_ApplyTokenSettlementRetryRepairsReplicaWithoutDoubleApply(t *testing.T) {
	primary := createTestSQLiteStore(t)
	replica := createTestSQLiteStore(t)
	t.Cleanup(func() {
		_ = replica.Close()
		_ = primary.Close()
	})

	hybrid := NewHybridStore(replica, primary)
	t.Cleanup(func() { _ = hybrid.Close() })

	ctx := context.Background()
	tokenHash := model.HashToken("hybrid-token-settlement")
	if err := hybrid.CreateAuthToken(ctx, &model.AuthToken{
		Token:       tokenHash,
		Description: "hybrid settlement",
		CreatedAt:   time.Now().Add(-time.Hour),
		IsActive:    true,
	}); err != nil {
		t.Fatalf("create auth token: %v", err)
	}

	if _, err := replica.ExecContext(ctx, `
		CREATE TRIGGER reject_auth_token_update
		BEFORE UPDATE ON auth_tokens
		BEGIN
			SELECT RAISE(ABORT, 'replica unavailable');
		END
	`); err != nil {
		t.Fatalf("create replica failure trigger: %v", err)
	}

	occurredAt := time.Now().Add(-time.Minute).UnixMilli()
	event := model.TokenSettlement{
		ID:                  "hybrid-settlement-replay",
		TokenHash:           tokenHash,
		OccurredAt:          occurredAt,
		Duration:            1.25,
		PromptTokens:        9,
		CompletionTokens:    4,
		CacheReadTokens:     2,
		CacheCreationTokens: 1,
		CostUSD:             0.12,
		EffectiveCostUSD:    0.06,
	}

	err := hybrid.ApplyTokenSettlement(ctx, event)
	if err == nil {
		t.Fatal("replica failure was silently acknowledged")
	}
	primaryToken, err := primary.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get primary token: %v", err)
	}
	if primaryToken.SuccessCount != 1 {
		t.Fatalf("primary success_count=%d, want 1", primaryToken.SuccessCount)
	}
	replicaToken, err := replica.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatalf("get replica token: %v", err)
	}
	if replicaToken.SuccessCount != 0 {
		t.Fatalf("replica success_count=%d before retry, want 0", replicaToken.SuccessCount)
	}

	if _, err := replica.ExecContext(ctx, "DROP TRIGGER reject_auth_token_update"); err != nil {
		t.Fatalf("drop replica failure trigger: %v", err)
	}
	if err := hybrid.ApplyTokenSettlement(ctx, event); err != nil {
		t.Fatalf("retry settlement: %v", err)
	}
	if err := hybrid.ApplyTokenSettlement(ctx, event); err != nil {
		t.Fatalf("second duplicate settlement: %v", err)
	}

	for name, store := range map[string]interface {
		GetAuthTokenByValue(context.Context, string) (*model.AuthToken, error)
	}{
		"primary": primary,
		"replica": replica,
	} {
		got, err := store.GetAuthTokenByValue(ctx, tokenHash)
		if err != nil {
			t.Fatalf("get %s token: %v", name, err)
		}
		if got.SuccessCount != 1 || got.PromptTokensTotal != event.PromptTokens {
			t.Fatalf("%s token was double applied: %+v", name, got)
		}
		if got.LastUsedAt == nil || *got.LastUsedAt != occurredAt {
			t.Fatalf("%s last_used_at=%v, want %d", name, got.LastUsedAt, occurredAt)
		}
	}
}

func TestHybridStore_InterleavedFailureAndSettlementKeepCountersOnce(t *testing.T) {
	primary := createTestSQLiteStore(t)
	replica := createTestSQLiteStore(t)
	t.Cleanup(func() {
		_ = replica.Close()
		_ = primary.Close()
	})

	hybrid := NewHybridStore(replica, primary)
	t.Cleanup(func() { _ = hybrid.Close() })

	ctx := context.Background()
	tokenHash := model.HashToken("hybrid-interleaved-settlement")
	if err := hybrid.CreateAuthToken(ctx, &model.AuthToken{
		Token:       tokenHash,
		Description: "hybrid interleaved settlement",
		CreatedAt:   time.Now().Add(-time.Hour),
		IsActive:    true,
	}); err != nil {
		t.Fatalf("create auth token: %v", err)
	}

	if _, err := replica.ExecContext(ctx, `
		CREATE TRIGGER reject_auth_token_update
		BEFORE UPDATE ON auth_tokens
		BEGIN
			SELECT RAISE(ABORT, 'replica unavailable');
		END
	`); err != nil {
		t.Fatalf("create replica failure trigger: %v", err)
	}

	occurredAt := time.Now().Add(-time.Minute).UnixMilli()
	event := model.TokenSettlement{
		ID:                  "hybrid-settlement-interleaved",
		TokenHash:           tokenHash,
		OccurredAt:          occurredAt,
		Duration:            1.25,
		PromptTokens:        9,
		CompletionTokens:    4,
		CacheReadTokens:     2,
		CacheCreationTokens: 1,
		CostUSD:             0.12,
		EffectiveCostUSD:    0.06,
	}

	if err := hybrid.ApplyTokenSettlement(ctx, event); err == nil {
		t.Fatal("replica failure was silently acknowledged")
	}
	if _, err := replica.ExecContext(ctx, "DROP TRIGGER reject_auth_token_update"); err != nil {
		t.Fatalf("drop replica failure trigger: %v", err)
	}
	if err := hybrid.UpdateTokenStats(ctx, tokenHash, false, 1.5, false, 0, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("interleaved failed request: %v", err)
	}

	laterLastUsed := time.Now().Add(time.Hour).UnixMilli()
	if err := replica.UpdateTokenLastUsed(ctx, tokenHash, time.UnixMilli(laterLastUsed)); err != nil {
		t.Fatalf("advance replica last used: %v", err)
	}
	if err := hybrid.ApplyTokenSettlement(ctx, event); err != nil {
		t.Fatalf("retry settlement: %v", err)
	}

	for name, store := range map[string]interface {
		GetAuthTokenByValue(context.Context, string) (*model.AuthToken, error)
	}{
		"primary": primary,
		"replica": replica,
	} {
		got, err := store.GetAuthTokenByValue(ctx, tokenHash)
		if err != nil {
			t.Fatalf("get %s token: %v", name, err)
		}
		if got.SuccessCount != 1 || got.FailureCount != 1 {
			t.Fatalf("%s counters=%d/%d, want success/failure=1/1", name, got.SuccessCount, got.FailureCount)
		}
		if got.PromptTokensTotal != event.PromptTokens || got.CompletionTokensTotal != event.CompletionTokens {
			t.Fatalf("%s settlement tokens were lost or doubled: %+v", name, got)
		}
		if got.TotalCostUSD != event.CostUSD || got.EffectiveCostUSD != event.EffectiveCostUSD {
			t.Fatalf("%s settlement cost was lost or doubled: total=%v effective=%v", name, got.TotalCostUSD, got.EffectiveCostUSD)
		}
		if got.LastUsedAt == nil {
			t.Fatalf("%s last_used_at was cleared", name)
		}
		if name == "replica" && *got.LastUsedAt != laterLastUsed {
			t.Fatalf("replica last_used_at=%v, want later timestamp %d", got.LastUsedAt, laterLastUsed)
		}
	}
}

func TestHybridStore_ConcurrentAccountingMutationsConverge(t *testing.T) {
	primary := createTestSQLiteStore(t)
	replica := createTestSQLiteStore(t)
	t.Cleanup(func() {
		_ = replica.Close()
		_ = primary.Close()
	})

	hybrid := NewHybridStore(replica, primary)
	t.Cleanup(func() { _ = hybrid.Close() })

	ctx := context.Background()
	tokenHash := model.HashToken("hybrid-concurrent-settlement")
	if err := hybrid.CreateAuthToken(ctx, &model.AuthToken{
		Token:       tokenHash,
		Description: "hybrid concurrent settlement",
		CreatedAt:   time.Now().Add(-time.Hour),
		IsActive:    true,
	}); err != nil {
		t.Fatalf("create auth token: %v", err)
	}

	const (
		settlements     = 12
		legacySuccesses = 6
		legacyFailures  = 6
	)
	start := make(chan struct{})
	errs := make(chan error, settlements+legacySuccesses+legacyFailures)
	var wg sync.WaitGroup
	wg.Add(settlements + legacySuccesses + legacyFailures)

	occurredAt := time.Now().Add(-2 * time.Minute).UnixMilli()
	for i := 0; i < settlements; i++ {
		event := model.TokenSettlement{
			ID:                  fmt.Sprintf("hybrid-concurrent-%02d", i),
			TokenHash:           tokenHash,
			OccurredAt:          occurredAt + int64(i),
			Duration:            1.25,
			PromptTokens:        11,
			CompletionTokens:    7,
			CacheReadTokens:     2,
			CacheCreationTokens: 1,
			CostUSD:             0.4,
			EffectiveCostUSD:    0.2,
		}
		go func() {
			defer wg.Done()
			<-start
			errs <- hybrid.ApplyTokenSettlement(ctx, event)
		}()
	}
	for i := 0; i < legacySuccesses+legacyFailures; i++ {
		isSuccess := i < legacySuccesses
		go func() {
			defer wg.Done()
			<-start
			errs <- hybrid.UpdateTokenStats(ctx, tokenHash, isSuccess, 2, false, 0, 10, 20, 3, 4, 0.5, 0.25)
		}()
	}

	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent accounting mutation: %v", err)
		}
	}

	for name, store := range map[string]interface {
		GetAuthTokenByValue(context.Context, string) (*model.AuthToken, error)
	}{
		"primary": primary,
		"replica": replica,
	} {
		got, err := store.GetAuthTokenByValue(ctx, tokenHash)
		if err != nil {
			t.Fatalf("get %s token: %v", name, err)
		}
		if got.SuccessCount != settlements+legacySuccesses || got.FailureCount != legacyFailures {
			t.Fatalf(
				"%s counters=%d/%d, want success/failure=%d/%d",
				name, got.SuccessCount, got.FailureCount,
				settlements+legacySuccesses, legacyFailures,
			)
		}
		if got.PromptTokensTotal != settlements*11+legacySuccesses*10 ||
			got.CompletionTokensTotal != settlements*7+legacySuccesses*20 {
			t.Fatalf("%s token totals diverged: %+v", name, got)
		}
		wantTotalCost := settlements*0.4 + legacySuccesses*0.5
		wantEffectiveCost := settlements*0.2 + legacySuccesses*0.25
		if math.Abs(got.TotalCostUSD-wantTotalCost) > 1e-9 ||
			math.Abs(got.EffectiveCostUSD-wantEffectiveCost) > 1e-9 {
			t.Fatalf("%s cost totals diverged: total=%v effective=%v", name, got.TotalCostUSD, got.EffectiveCostUSD)
		}
	}
}
