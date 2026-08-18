package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestHandleSiteInventoryReturnsAccountsAndLatestCheckins(t *testing.T) {
	srv := newInMemoryServer(t)
	ctx := context.Background()
	site, err := srv.store.CreateSite(ctx, &model.Site{Name: "inventory", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://inventory.example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := srv.store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "main", CredentialType: model.CredentialTypeAccessToken, Enabled: true, Status: model.SiteAccountStatusHealthy, BalanceCurrency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := srv.store.CreateCheckinRun(ctx, &model.CheckinRun{Trigger: "manual", LocalDay: "2026-08-18", Timezone: "Asia/Shanghai", Status: model.SiteTaskStatusSuccess, Total: 2})
	if err != nil {
		t.Fatal(err)
	}
	for index, day := range []string{"2026-08-17", "2026-08-18"} {
		if _, err = srv.store.CreateCheckinAttempt(ctx, &model.CheckinAttempt{RunID: run.ID, SiteAccountID: account.ID, ProviderID: "test", LocalDay: day, TriggerScope: "manual", Status: "success", StartedAt: int64(index + 1), FinishedAt: int64(index + 2), AttemptNo: 1}); err != nil {
			t.Fatal(err)
		}
	}

	c, recorder := newTestContext(t, newRequest(http.MethodGet, "/admin/site-inventory", nil))
	srv.siteControl.handleSiteInventory(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := mustParseAPIResponse[siteInventoryResponse](t, recorder.Body.Bytes())
	if len(response.Data.Sites) != 1 || len(response.Data.Accounts) != 1 {
		t.Fatalf("inventory sites=%d accounts=%d", len(response.Data.Sites), len(response.Data.Accounts))
	}
	latest := response.Data.LatestCheckins[account.ID]
	if latest == nil || latest.LocalDay != "2026-08-18" {
		t.Fatalf("latest=%+v, want 2026-08-18", latest)
	}

	c, recorder = newTestContext(t, newRequest(http.MethodGet, "/admin/checkin-attempts?limit=100", nil))
	srv.siteControl.handleCheckinAttempts(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	attempts := mustParseAPIResponse[[]*model.CheckinAttempt](t, recorder.Body.Bytes())
	if len(attempts.Data) != 2 || attempts.Count != 2 {
		t.Fatalf("attempts=%d count=%d", len(attempts.Data), attempts.Count)
	}
}
