package sql_test

import (
	"context"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestListCheckinAttemptsBatchLimitsEachAccount(t *testing.T) {
	store := newTestStore(t, "site-checkin-batch.db")
	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{Name: "batch-site", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://batch.example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	createAccount := func(label string) *model.SiteAccount {
		account, createErr := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: label, CredentialType: model.CredentialTypeAccessToken, Enabled: true, Status: model.SiteAccountStatusHealthy, BalanceCurrency: "USD"})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return account
	}
	first := createAccount("first")
	second := createAccount("second")
	run, err := store.CreateCheckinRun(ctx, &model.CheckinRun{Trigger: "manual", LocalDay: "2026-08-18", Timezone: "Asia/Shanghai", Status: model.SiteTaskStatusSuccess, Total: 4})
	if err != nil {
		t.Fatal(err)
	}
	createAttempt := func(accountID int64, day, status string) *model.CheckinAttempt {
		attempt, createErr := store.CreateCheckinAttempt(ctx, &model.CheckinAttempt{RunID: run.ID, SiteAccountID: accountID, ProviderID: "test", LocalDay: day, TriggerScope: "manual", Status: status, StartedAt: 1, FinishedAt: 2, AttemptNo: 1})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return attempt
	}
	_ = createAttempt(first.ID, "2026-08-16", "failed")
	firstLatest := createAttempt(first.ID, "2026-08-18", "success")
	_ = createAttempt(second.ID, "2026-08-15", "failed")
	secondLatest := createAttempt(second.ID, "2026-08-17", "already_checked")

	latest, err := store.ListCheckinAttemptsBatch(ctx, []int64{first.ID, second.ID}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 {
		t.Fatalf("latest count=%d, want 2", len(latest))
	}
	byAccount := make(map[int64]*model.CheckinAttempt, len(latest))
	for _, attempt := range latest {
		byAccount[attempt.SiteAccountID] = attempt
	}
	if got := byAccount[first.ID]; got == nil || got.ID != firstLatest.ID {
		t.Fatalf("first latest=%+v, want id=%d", got, firstLatest.ID)
	}
	if got := byAccount[second.ID]; got == nil || got.ID != secondLatest.ID {
		t.Fatalf("second latest=%+v, want id=%d", got, secondLatest.ID)
	}

	all, err := store.ListCheckinAttemptsBatch(ctx, []int64{first.ID, second.ID}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 4 {
		t.Fatalf("all count=%d, want 4", len(all))
	}
	empty, err := store.ListCheckinAttemptsBatch(ctx, nil, 2)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty=(%d,%v), want (0,nil)", len(empty), err)
	}
}
