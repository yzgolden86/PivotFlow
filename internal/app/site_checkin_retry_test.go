package app

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func TestCheckinRetryDue(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	attemptAt := func(status string, attemptNo int, age time.Duration) *model.CheckinAttempt {
		return &model.CheckinAttempt{Status: status, AttemptNo: attemptNo, FinishedAt: now.Add(-age).UnixMilli()}
	}
	cases := []struct {
		name    string
		attempt *model.CheckinAttempt
		want    bool
	}{
		{
			name:    "no attempt yet runs",
			attempt: nil,
			want:    true,
		},
		{
			name:    "collected reward settles the day",
			attempt: attemptAt(provider.CheckinSuccess, 1, time.Minute),
			want:    false,
		},
		{
			name:    "upstream already checked settles the day",
			attempt: attemptAt(provider.CheckinAlreadyChecked, 2, time.Minute),
			want:    false,
		},
		{
			name:    "browser challenge waits out the cooldown",
			attempt: attemptAt(provider.CheckinBrowserRequired, 1, time.Minute),
			want:    false,
		},
		{
			// A challenge is not a transient error. Retrying it on the generic
			// hourly pacing is what makes an account look like it is hammering an
			// endpoint it can never pass, so it must not come due that early.
			name:    "browser challenge ignores the generic hourly pacing",
			attempt: attemptAt(provider.CheckinBrowserRequired, 1, siteCheckinRetryInterval),
			want:    false,
		},
		{
			name:    "browser challenge retries at its own slower pace",
			attempt: attemptAt(provider.CheckinBrowserRequired, 1, siteCheckinBrowserRetryInterval),
			want:    true,
		},
		{
			name:    "plain failure retries once the cooldown elapsed",
			attempt: attemptAt(provider.CheckinFailed, 2, 2*siteCheckinRetryInterval),
			want:    true,
		},
		{
			name:    "unsupported result still gets retried",
			attempt: attemptAt(provider.CheckinUnsupported, 1, 2*siteCheckinRetryInterval),
			want:    true,
		},
		{
			name:    "an attempt that never finished recovers immediately",
			attempt: &model.CheckinAttempt{Status: "running", AttemptNo: 1},
			want:    true,
		},
		{
			name:    "a browser challenge stops well before the generic retry limit",
			attempt: attemptAt(provider.CheckinBrowserRequired, siteCheckinBrowserRetryLimit, 10*siteCheckinBrowserRetryInterval),
			want:    false,
		},
		{
			name:    "a transient failure still uses the full retry budget",
			attempt: attemptAt(provider.CheckinFailed, siteCheckinRetryLimit-1, 2*siteCheckinRetryInterval),
			want:    true,
		},
		{
			name:    "retry limit stops a permanently failing site",
			attempt: attemptAt(provider.CheckinFailed, siteCheckinRetryLimit, 10*siteCheckinRetryInterval),
			want:    false,
		},
		{
			name:    "a settled day stays settled even past the retry limit",
			attempt: attemptAt(provider.CheckinSuccess, siteCheckinRetryLimit, 10*siteCheckinRetryInterval),
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := checkinRetryDue(tc.attempt, now)
			if got != tc.want {
				t.Fatalf("checkinRetryDue(%+v)=%v (reason %q), want %v", tc.attempt, got, reason, tc.want)
			}
		})
	}
}

// A challenged day must cost the site almost nothing. Every attempt against a
// site that rejects them is a request its abuse detection can count, and a
// banned account is far worse than a check-in the operator has to trigger by
// hand. So the daily budget is pinned behaviourally rather than by asserting a
// constant: walk two local days at the scheduler's own tick resolution,
// modelling the fresh row each new day gets, and count what is actually
// attempted. Asserting per day rather than in total matters — the day boundary
// resets attempt_no, so a total would let a budget that is only ever reached on
// the second day slip through.
func TestChallengedDayIsCappedAtTwoAttempts(t *testing.T) {
	location := time.FixedZone("test", 8*60*60)
	start := time.Date(2026, 9, 17, 0, 0, 0, 0, location)
	attemptsPerDay := map[string]int{}
	var attempt *model.CheckinAttempt
	day := ""
	for tick := 0; tick < 48*60; tick++ {
		tickNow := start.Add(time.Duration(tick) * time.Minute)
		if localDay := tickNow.Format("2006-01-02"); localDay != day {
			// A new local day starts from a clean slate, exactly as the store's
			// one-row-per-account-per-day rule does.
			day = localDay
			attempt = nil
		}
		if !dailyCheckinDue(tickNow, 0) {
			continue
		}
		due, _ := checkinRetryDue(attempt, tickNow)
		if !due {
			continue
		}
		attemptsPerDay[day]++
		no := 1
		if attempt != nil {
			no = attempt.AttemptNo + 1
		}
		attempt = &model.CheckinAttempt{
			Status:     provider.CheckinBrowserRequired,
			AttemptNo:  no,
			FinishedAt: tickNow.UnixMilli(),
		}
	}
	if len(attemptsPerDay) != 2 {
		t.Fatalf("simulated %d local days, want 2: %v", len(attemptsPerDay), attemptsPerDay)
	}
	for localDay, count := range attemptsPerDay {
		if count != 2 {
			t.Fatalf("%s made %d attempts, want 2 (one scheduled, one late retry)", localDay, count)
		}
	}
}

func TestFailedDayIsCappedAtThreeAttempts(t *testing.T) {
	location := time.FixedZone("test", 8*60*60)
	start := time.Date(2026, 9, 17, 0, 0, 0, 0, location)
	attemptsPerDay := map[string]int{}
	var attempt *model.CheckinAttempt
	day := ""
	for tick := 0; tick < 48*60; tick++ {
		tickNow := start.Add(time.Duration(tick) * time.Minute)
		if localDay := tickNow.Format("2006-01-02"); localDay != day {
			day = localDay
			attempt = nil
		}
		if !dailyCheckinDue(tickNow, 0) {
			continue
		}
		due, _ := checkinRetryDue(attempt, tickNow)
		if !due {
			continue
		}
		attemptsPerDay[day]++
		no := 1
		if attempt != nil {
			no = attempt.AttemptNo + 1
		}
		attempt = &model.CheckinAttempt{
			Status:     provider.CheckinFailed,
			AttemptNo:  no,
			FinishedAt: tickNow.UnixMilli(),
		}
	}
	if len(attemptsPerDay) != 2 {
		t.Fatalf("simulated %d local days, want 2: %v", len(attemptsPerDay), attemptsPerDay)
	}
	for localDay, count := range attemptsPerDay {
		if count != 3 {
			t.Fatalf("%s made %d attempts, want 3 (one scheduled, two hourly retries)", localDay, count)
		}
	}
}

// flakyCheckinAdapter mimics a site whose first scheduled check-in is answered
// with an interactive browser challenge, and which reports the account as
// already checked in once a human cleared that challenge in a real browser.
type flakyCheckinAdapter struct {
	blockingCheckinAdapter

	mu           sync.Mutex
	calls        int
	browserUntil int
}

func (a *flakyCheckinAdapter) Checkin(context.Context, provider.AccountRequest) (provider.CheckinResult, error) {
	a.mu.Lock()
	a.calls++
	calls := a.calls
	a.mu.Unlock()
	if calls <= a.browserUntil {
		return provider.CheckinResult{Status: provider.CheckinBrowserRequired, Message: "browser verification required"},
			&provider.Error{Code: provider.CodeBrowserRequired, Message: "browser verification required"}
	}
	return provider.CheckinResult{Status: provider.CheckinAlreadyChecked, Message: "上游记录今日已签到"}, nil
}

func (a *flakyCheckinAdapter) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

// newCheckinRetryTestService builds a scheduler-backed service whose daily
// check-in is due around the clock, so the assertions never depend on the
// wall clock the test happens to run at.
func newCheckinRetryTestService(t *testing.T, adapter provider.SiteAdapter) (*siteControlService, *model.SiteAccount) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.CreateSQLiteStore(filepath.Join(t.TempDir(), "checkin-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.BatchUpdateSettings(ctx, map[string]string{"site_daily_checkin_time": "00:00"}); err != nil {
		t.Fatal(err)
	}
	config := NewConfigService(store)
	if err := config.LoadDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "checkin-retry-test")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal(provider.Credentials{AccessToken: "session-token"})
	if err != nil {
		t.Fatal(err)
	}
	site, err := store.CreateSite(ctx, &model.Site{Name: "checkin-retry-site", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://checkin-retry.example", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]", LastProbeStatus: "success"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "checkin-retry-account", CredentialType: model.CredentialTypeAccessToken, CredentialCiphertext: sealed, CredentialKeyVersion: cipher.Version(), Enabled: true, AutoCheckin: true, AutoRefresh: false, Status: model.SiteAccountStatusHealthy, BalanceCurrency: "CNY", LastRefreshStatus: "unknown", LastCheckinStatus: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	service := &siteControlService{store: store, cipher: cipher, registry: provider.NewRegistry(adapter), baseCtx: ctx, configService: config}
	return service, account
}

func TestScheduledCheckinRetriesUntilTheDaySettles(t *testing.T) {
	adapter := &flakyCheckinAdapter{browserUntil: 1}
	service, account := newCheckinRetryTestService(t, adapter)
	ctx := context.Background()
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	day := start.In(location).Format("2006-01-02")

	// The 08:00 run hits the browser challenge and is recorded as such.
	service.runSchedule(ctx, start)
	if got := adapter.count(); got != 1 {
		t.Fatalf("first run made %d check-in calls, want 1", got)
	}
	first, err := service.store.GetDailyCheckinAttempt(ctx, account.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || first.Status != provider.CheckinBrowserRequired || first.AttemptNo != 1 {
		t.Fatalf("daily attempt after first run=%+v, want one browser_required attempt", first)
	}
	updated, err := service.store.GetSiteAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastCheckinStatus != provider.CheckinBrowserRequired {
		t.Fatalf("last check-in status=%q, want %q", updated.LastCheckinStatus, provider.CheckinBrowserRequired)
	}

	// The next tick must respect the cooldown instead of hammering the site.
	service.runSchedule(ctx, start.Add(time.Minute))
	if got := adapter.count(); got != 1 {
		t.Fatalf("cooldown tick made %d check-in calls, want 1", got)
	}

	// The operator clears the challenge in a browser; the next paced retry
	// notices the upstream already counts the account as checked in, so the
	// day converges without a second manual click. The browser pacing is aged
	// onto the row rather than added to the clock: an attempt records a real
	// finished_at, so advancing the clock six hours would both cross local
	// midnight on an evening run and leave the elapsed time wrong.
	first.FinishedAt = time.Now().Add(-siteCheckinBrowserRetryInterval - time.Minute).UnixMilli()
	if err := service.store.UpdateCheckinAttempt(ctx, first); err != nil {
		t.Fatal(err)
	}
	service.runSchedule(ctx, start.Add(time.Minute))
	if got := adapter.count(); got != 2 {
		t.Fatalf("retry made %d check-in calls, want 2", got)
	}
	second, err := service.store.GetDailyCheckinAttempt(ctx, account.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	// The retry must reuse the day's row rather than insert a second one: the
	// table only allows one row per account, day, and trigger scope.
	if second == nil || second.ID != first.ID {
		t.Fatalf("retry row=%+v, want the first row %d updated in place", second, first.ID)
	}
	if second.AttemptNo != 2 || second.Status != provider.CheckinAlreadyChecked {
		t.Fatalf("retry row status=%q attempt_no=%d, want already_checked/2", second.Status, second.AttemptNo)
	}
	updated, err = service.store.GetSiteAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastCheckinStatus != provider.CheckinAlreadyChecked {
		t.Fatalf("last check-in status=%q, want %q", updated.LastCheckinStatus, provider.CheckinAlreadyChecked)
	}

	// Once the day is settled nothing runs again, however many ticks pass. The
	// row is aged far past the cooldown first, so this proves settling wins over
	// pacing rather than merely that the cooldown was still running.
	second.FinishedAt = time.Now().Add(-10 * siteCheckinBrowserRetryInterval).UnixMilli()
	if err := service.store.UpdateCheckinAttempt(ctx, second); err != nil {
		t.Fatal(err)
	}
	service.runSchedule(ctx, start.Add(2*time.Minute))
	service.runSchedule(ctx, start.Add(3*time.Minute))
	if got := adapter.count(); got != 2 {
		t.Fatalf("settled day made %d check-in calls, want 2", got)
	}
	attempts, err := service.store.ListCheckinAttempts(ctx, account.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 {
		t.Fatalf("account history has %d rows, want the single daily row", len(attempts))
	}
}
