package storage

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func dailyBalanceTestStore(t *testing.T) (Store, int64) {
	t.Helper()
	store, err := CreateSQLiteStore(filepath.Join(t.TempDir(), "daily-balance.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{Name: "daily-test", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://example.invalid", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "daily-test", CredentialType: model.CredentialTypeAPIKey, CredentialCiphertext: "synthetic", CredentialKeyVersion: "test", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return store, account.ID
}

func TestDailyBalanceClaimAndCompletionAreImmutable(t *testing.T) {
	store, id := dailyBalanceTestStore(t)
	ctx := context.Background()
	entry := &model.SiteAccountDailyBalance{SiteAccountID: id, LocalDay: "2026-11-24", ScheduledAt: 1795449900000, Status: "unknown"}
	claimed, err := store.CreateSiteAccountDailyBalance(ctx, entry)
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	claimed, err = store.CreateSiteAccountDailyBalance(ctx, entry)
	if err != nil || claimed {
		t.Fatalf("duplicate claim=%v err=%v", claimed, err)
	}
	amount := -1.25
	entry.Balance, entry.Currency, entry.Status, entry.CollectedAt = &amount, " usd ", "success", entry.ScheduledAt+1000
	completed, err := store.CompleteSiteAccountDailyBalance(ctx, entry)
	if err != nil || !completed {
		t.Fatalf("complete=%v err=%v", completed, err)
	}
	changed := 99.0
	entry.Balance = &changed
	completed, err = store.CompleteSiteAccountDailyBalance(ctx, entry)
	if err != nil || completed {
		t.Fatalf("second completion=%v err=%v", completed, err)
	}
	rows, err := store.ListSiteAccountDailyBalances(ctx, "2026-11-24", "2026-11-24")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if rows[0].Balance == nil || *rows[0].Balance != amount || rows[0].Currency != "USD" || rows[0].Status != "success" {
		t.Fatalf("frozen record=%+v", rows[0])
	}
	if err := store.UpsertSiteAccountBalanceSnapshot(ctx, &model.SiteAccountBalanceSnapshot{SiteAccountID: id, LocalDay: entry.LocalDay, Currency: "USD", Balance: 200}); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListSiteAccountDailyBalances(ctx, "", "")
	if err != nil || len(rows) != 1 || rows[0].Balance == nil || *rows[0].Balance != amount {
		t.Fatalf("manual refresh changed frozen daily history: rows=%+v err=%v", rows, err)
	}
}

func TestDailyBalanceUnknownAndFailedNeverBecomeZero(t *testing.T) {
	store, id := dailyBalanceTestStore(t)
	ctx := context.Background()
	for _, day := range []string{"2026-11-23", "2026-11-24"} {
		entry := &model.SiteAccountDailyBalance{SiteAccountID: id, LocalDay: day, ScheduledAt: 1000, Status: "unknown", Currency: "CNY"}
		if claimed, err := store.CreateSiteAccountDailyBalance(ctx, entry); err != nil || !claimed {
			t.Fatalf("claim=%v err=%v", claimed, err)
		}
		if day == "2026-11-24" {
			entry.Status, entry.ErrorCode, entry.CollectedAt = "failed", "provider_timeout", 2000
			if completed, err := store.CompleteSiteAccountDailyBalance(ctx, entry); err != nil || !completed {
				t.Fatalf("failed completion=%v err=%v", completed, err)
			}
		}
	}
	rows, err := store.ListSiteAccountDailyBalances(ctx, "2026-11-23", "2026-11-24")
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if rows[0].Status != "unknown" || rows[1].Status != "failed" || rows[0].Balance != nil || rows[1].Balance != nil {
		t.Fatalf("unknown amounts must remain NULL: %+v", rows)
	}
	if err := store.DeleteSiteAccount(ctx, id); err != nil {
		t.Fatal(err)
	}
	rows, err = store.ListSiteAccountDailyBalances(ctx, "2026-11-24", "2026-11-24")
	if err != nil || len(rows) != 1 {
		t.Fatalf("soft delete rewrote historical coverage: rows=%+v err=%v", rows, err)
	}
}

func TestDailyBalanceRejectsInvalidDatesAndNonFiniteSuccess(t *testing.T) {
	store, id := dailyBalanceTestStore(t)
	ctx := context.Background()
	for _, day := range []string{"2026-02-30", "bad", "2026-1-01"} {
		_, err := store.CreateSiteAccountDailyBalance(ctx, &model.SiteAccountDailyBalance{SiteAccountID: id, LocalDay: day, ScheduledAt: 1000, Status: "unknown"})
		if err == nil {
			t.Fatalf("accepted invalid date %q", day)
		}
	}
	entry := &model.SiteAccountDailyBalance{SiteAccountID: id, LocalDay: "2026-11-24", ScheduledAt: 1000, Status: "unknown"}
	if _, err := store.CreateSiteAccountDailyBalance(ctx, entry); err != nil {
		t.Fatal(err)
	}
	entry.Status, entry.Currency, entry.CollectedAt = "success", "USD", 2000
	for _, amount := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		entry.Balance = &amount
		if _, err := store.CompleteSiteAccountDailyBalance(ctx, entry); err == nil {
			t.Fatalf("accepted nonfinite success %v", amount)
		}
	}
	zero := 0.0
	entry.Balance = &zero
	if completed, err := store.CompleteSiteAccountDailyBalance(ctx, entry); err != nil || !completed {
		t.Fatalf("real zero rejected: complete=%v err=%v", completed, err)
	}
}

func TestDailyBalanceObservationPreservesNewerBalanceAndManagementFields(t *testing.T) {
	store, id := dailyBalanceTestStore(t)
	ctx := context.Background()
	account, err := store.GetSiteAccount(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	account.Enabled, account.Label, account.AutoRefresh = false, "edited", true
	if _, err := store.UpdateSiteAccount(ctx, id, account); err != nil {
		t.Fatal(err)
	}
	for _, observation := range []struct {
		amount float64
		at     int64
	}{{12.25, 3000}, {99, 2000}, {77, 3000}} {
		if err := store.UpdateSiteAccountBalance(ctx, id, observation.amount, " usd ", observation.at); err != nil {
			t.Fatal(err)
		}
	}
	account, err = store.GetSiteAccount(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if account.Balance == nil || *account.Balance != 12.25 || account.BalanceUpdatedAt != 3000 || account.BalanceCurrency != "USD" || account.Enabled || account.Label != "edited" || !account.AutoRefresh {
		t.Fatalf("stale observation or management rewrite: balance=%v at=%d currency=%q enabled=%v label=%q auto_refresh=%v", account.Balance, account.BalanceUpdatedAt, account.BalanceCurrency, account.Enabled, account.Label, account.AutoRefresh)
	}
}
