package app

import (
	"context"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

// A browser challenge is its own outcome, not a failure. New API delivers it as
// a 200 response body rather than a transport error, so the provider used to
// hand back CheckinFailed and the whole day's bookkeeping followed suit: the run
// reported one failure, browser_required_count stayed at zero, and the account
// looked broken while it was really just waiting for a human.
func TestBrowserChallengeIsRecordedAsItsOwnOutcome(t *testing.T) {
	adapter := &flakyCheckinAdapter{browserUntil: 1}
	service, account := newCheckinRetryTestService(t, adapter)
	ctx := context.Background()
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().In(location).Format("2006-01-02")

	service.runSchedule(ctx, time.Now())

	attempt, err := service.store.GetDailyCheckinAttempt(ctx, account.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if attempt == nil || attempt.Status != provider.CheckinBrowserRequired {
		t.Fatalf("daily attempt=%+v, want a browser_required attempt", attempt)
	}
	run, err := service.store.GetCheckinRun(ctx, attempt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.BrowserRequiredCount != 1 {
		t.Fatalf("browser_required_count=%d, want 1", run.BrowserRequiredCount)
	}
	if run.FailedCount != 0 {
		t.Fatalf("failed_count=%d, want 0: a challenge is not a failure", run.FailedCount)
	}
	updated, err := service.store.GetSiteAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastCheckinStatus != provider.CheckinBrowserRequired {
		t.Fatalf("last check-in status=%q, want %q", updated.LastCheckinStatus, provider.CheckinBrowserRequired)
	}
}

// A genuine failure must keep landing in failed_count, so the two outcomes stay
// distinguishable.
func TestHardFailureIsRecordedAsAFailure(t *testing.T) {
	adapter := &failingCheckinAdapter{}
	service, account := newCheckinRetryTestService(t, adapter)
	ctx := context.Background()
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	day := time.Now().In(location).Format("2006-01-02")

	service.runSchedule(ctx, time.Now())

	attempt, err := service.store.GetDailyCheckinAttempt(ctx, account.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if attempt == nil || attempt.Status != provider.CheckinFailed {
		t.Fatalf("daily attempt=%+v, want a failed attempt", attempt)
	}
	run, err := service.store.GetCheckinRun(ctx, attempt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.FailedCount != 1 || run.BrowserRequiredCount != 0 {
		t.Fatalf("failed_count=%d browser_required_count=%d, want 1 and 0", run.FailedCount, run.BrowserRequiredCount)
	}
}

type failingCheckinAdapter struct {
	checkinMethodTestAdapter
}

func (a *failingCheckinAdapter) Checkin(context.Context, provider.AccountRequest) (provider.CheckinResult, error) {
	return provider.CheckinResult{Status: provider.CheckinFailed, Message: "upstream rejected the request"},
		&provider.Error{Code: provider.CodeRequestFailed, Message: "upstream rejected the request"}
}
