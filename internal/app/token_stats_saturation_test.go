package app

import (
	"context"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

// A full telemetry queue must not discard a completed, chargeable request.
func TestSuccessfulTokenStatsSurviveSaturatedQueue(t *testing.T) {
	store, err := storage.CreateSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	tokenHash := strings.Repeat("a", 64)
	if err := store.CreateAuthToken(ctx, &model.AuthToken{Token: tokenHash, IsActive: true}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{store: store, tokenStatsCh: make(chan tokenStatsUpdate, 1)}
	srv.tokenStatsCh <- tokenStatsUpdate{tokenHash: tokenHash}
	srv.updateTokenStatsAsync(tokenHash, 1, floatPtr(0.25), true, 1, false,
		&fwResult{InputTokens: 10, OutputTokens: 20}, "test-model")
	got, err := store.GetAuthTokenByValue(ctx, tokenHash)
	if err != nil {
		t.Fatal(err)
	}
	if got.SuccessCount != 1 || got.CostUsedMicroUSD != 250_000 || got.PromptTokensTotal != 10 {
		t.Fatalf("successful accounting was lost: count=%d cost=%d input=%d", got.SuccessCount, got.CostUsedMicroUSD, got.PromptTokensTotal)
	}
	if got := srv.tokenStatsDropCount.Load(); got != 0 {
		t.Fatalf("successful settlement must not be dropped: %d", got)
	}
}
