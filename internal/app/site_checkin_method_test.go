package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

// checkinMethodTestAdapter reports a fixed discovered check-in method and counts
// the check-in POSTs, so a test can prove whether the request was really sent.
// It also counts discovery probes, so a test can prove the site-level cache is
// doing its job.
type checkinMethodTestAdapter struct {
	projectionTestAdapter
	method      provider.CheckinMethod
	checkins    atomic.Int32
	discoveries atomic.Int32
}

func (a *checkinMethodTestAdapter) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{ServerCheckin: true, Balance: true, Models: true}
}

func (a *checkinMethodTestAdapter) DiscoverCheckin(context.Context, provider.AccountRequest) (provider.CheckinMethod, error) {
	a.discoveries.Add(1)
	return a.method, nil
}

func (a *checkinMethodTestAdapter) Checkin(context.Context, provider.AccountRequest) (provider.CheckinResult, error) {
	a.checkins.Add(1)
	return provider.CheckinResult{Status: provider.CheckinSuccess, Message: "签到成功"}, nil
}

// turnstileCheckinAdapter keeps the upstream behaviour of a site that gates
// check-in behind an interactive challenge.
type turnstileCheckinAdapter struct {
	checkinMethodTestAdapter
}

func (a *turnstileCheckinAdapter) Checkin(context.Context, provider.AccountRequest) (provider.CheckinResult, error) {
	a.checkins.Add(1)
	return provider.CheckinResult{Status: provider.CheckinBrowserRequired}, &provider.Error{Code: provider.CodeBrowserRequired, Message: "browser verification is required"}
}

func runCheckinTask(t *testing.T, service *siteControlService, site *model.Site, account *model.SiteAccount) *model.SiteTask {
	t.Helper()
	ctx := context.Background()
	task := &model.SiteTask{ID: newSiteTaskID(), Kind: "checkin", Status: model.SiteTaskStatusRunning, SiteID: site.ID, SiteAccountID: account.ID, ProgressJSON: "{}"}
	if err := service.store.CreateSiteTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	service.checkinWithTrigger(ctx, task, account.ID, "schedule", "daily")
	stored, err := service.store.GetSiteTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestCheckinSkipsPostWhenSiteReportsCheckinDisabled(t *testing.T) {
	adapter := &checkinMethodTestAdapter{method: provider.CheckinMethod{Status: provider.CheckinMethodDisabled, Source: "api_status"}}
	service, site, account := newSiteRefreshTestService(t, adapter)
	stored := runCheckinTask(t, service, site, account)

	if got := adapter.checkins.Load(); got != 0 {
		t.Fatalf("check-in POSTs=%d, want 0 when the site publishes checkin_enabled=false", got)
	}
	if stored.Status != model.SiteTaskStatusFailed || stored.Error != provider.CodeUnsupported {
		t.Fatalf("task status=%q error=%q, want failed/%s", stored.Status, stored.Error, provider.CodeUnsupported)
	}
	updated, err := service.store.GetSiteAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastCheckinStatus != provider.CheckinUnsupported {
		t.Fatalf("last check-in status=%q, want %q", updated.LastCheckinStatus, provider.CheckinUnsupported)
	}
	attempts, err := service.store.ListCheckinAttempts(context.Background(), account.ID, 10)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts=%+v err=%v", attempts, err)
	}
	if attempts[0].Status != provider.CheckinUnsupported || attempts[0].ErrorCode != provider.CodeUnsupported {
		t.Fatalf("attempt=%+v", attempts[0])
	}
}

// A Turnstile site must still be attempted: the upstream may answer
// "already checked" once a human finished the browser step, and only a real
// failure should be reported. Discovery is used to explain the failure, not to
// skip the request.
func TestCheckinAttemptsTurnstileSiteAndNamesTheChallenge(t *testing.T) {
	adapter := &turnstileCheckinAdapter{checkinMethodTestAdapter: checkinMethodTestAdapter{
		method: provider.CheckinMethod{Status: provider.CheckinMethodTurnstile, TurnstileSiteKey: "0xKEY", Source: "api_status"},
	}}
	service, site, account := newSiteRefreshTestService(t, adapter)
	stored := runCheckinTask(t, service, site, account)

	if got := adapter.checkins.Load(); got != 1 {
		t.Fatalf("check-in POSTs=%d, want 1: discovery must not skip the request", got)
	}
	if stored.Status != model.SiteTaskStatusFailed || stored.Error != provider.CodeBrowserRequired {
		t.Fatalf("task status=%q error=%q, want failed/%s", stored.Status, stored.Error, provider.CodeBrowserRequired)
	}
	attempts, err := service.store.ListCheckinAttempts(context.Background(), account.ID, 10)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts=%+v err=%v", attempts, err)
	}
	if !strings.Contains(attempts[0].Message, "Turnstile") {
		t.Fatalf("attempt message=%q, want it to name the Turnstile challenge", attempts[0].Message)
	}
}
