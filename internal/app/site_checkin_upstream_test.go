package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

// newTurnstileUpstreamServer reproduces the endpoints a Turnstile-protected
// New API site actually serves, with the wording taken from the upstream source.
// The guard is a *middleware*: it answers POST /api/user/checkin with HTTP 200
// and {"success":false,"message":"Turnstile token 为空"} before the handler ever
// runs. GET /api/user/checkin is deliberately not behind it, which is the only
// reason a server-side deployment can ever learn that a human signed in.
//
// checkedInToday stands in for the operator completing the challenge in a real
// browser.
func newTurnstileUpstreamServer(checkedInToday *atomic.Bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"system_name":"CUN.AI","checkin_enabled":true,` +
				`"turnstile_check":true,"turnstile_site_key":"0xKEY"}}`))
		case r.URL.Path == "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":42,"username":"operator","quota":500000}}`))
		case r.URL.Path == "/api/user/checkin" && r.Method == http.MethodPost:
			// middleware/turnstile-check.go, verbatim.
			_, _ = w.Write([]byte(`{"success":false,"message":"Turnstile token 为空"}`))
		case r.URL.Path == "/api/user/checkin" && r.Method == http.MethodGet:
			// model/checkin.go -> GetUserCheckinStats.
			_, _ = fmt.Fprintf(w, `{"success":true,"data":{"stats":{"checked_in_today":%t}}}`, checkedInToday.Load())
		default:
			http.NotFound(w, r)
		}
	}))
}

// newUpstreamCheckinService wires the *real* adapter into the scheduler, so the
// join between provider classification and orchestration is exercised rather
// than assumed. The other fixtures in this package stub the adapter out, which
// cannot catch a provider that reports the wrong status.
func newUpstreamCheckinService(t *testing.T, adapter provider.SiteAdapter, baseURL string) (*siteControlService, *model.SiteAccount) {
	t.Helper()
	ctx := context.Background()
	store, err := storage.CreateSQLiteStore(filepath.Join(t.TempDir(), "upstream-checkin.db"))
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
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "upstream-checkin-test")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal(provider.Credentials{AccessToken: "session-token", UserID: 42})
	if err != nil {
		t.Fatal(err)
	}
	site, err := store.CreateSite(ctx, &model.Site{
		Name: "turnstile-upstream-site", Platform: model.SitePlatformNewAPIFamily,
		BaseURL: baseURL, Enabled: true, Timezone: "Asia/Shanghai",
		UseSystemProxy: false, TagsJSON: "[]", LastProbeStatus: "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{
		SiteID: site.ID, Label: "turnstile-upstream-account",
		CredentialType: model.CredentialTypeAccessToken, CredentialCiphertext: sealed,
		CredentialKeyVersion: cipher.Version(), Enabled: true,
		AutoCheckin: true, AutoRefresh: false, Status: model.SiteAccountStatusHealthy,
		BalanceCurrency: "CNY", LastRefreshStatus: "unknown", LastCheckinStatus: "unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	service := &siteControlService{
		store: store, cipher: cipher, registry: provider.NewRegistry(adapter),
		baseCtx: ctx, configService: config,
	}
	return service, account
}

// The whole cun.ai scenario, end to end and through the real adapter: a Turnstile
// site whose server-side check-in can never succeed, an operator who signs in by
// hand, and a scheduler that has to notice that without ever passing the
// challenge itself.
func TestTurnstileUpstreamChallengeThenManualCheckinConverges(t *testing.T) {
	var checkedInToday atomic.Bool
	server := newTurnstileUpstreamServer(&checkedInToday)
	defer server.Close()

	service, account := newUpstreamCheckinService(t, provider.NewNewAPI(provider.ClientFactory{AllowPrivate: true}), server.URL)
	ctx := context.Background()
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	day := start.In(location).Format("2006-01-02")

	// The scheduled run: the site publishes Turnstile, so the attempt is made and
	// rejected. It must be filed as a challenge, not as a failure.
	service.runSchedule(ctx, start)

	attempt, err := service.store.GetDailyCheckinAttempt(ctx, account.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if attempt == nil || attempt.Status != provider.CheckinBrowserRequired {
		t.Fatalf("daily attempt=%+v, want a browser_required attempt", attempt)
	}
	if attempt.ErrorCode != provider.CodeBrowserRequired {
		t.Fatalf("error code=%q, want %q", attempt.ErrorCode, provider.CodeBrowserRequired)
	}
	run, err := service.store.GetCheckinRun(ctx, attempt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.BrowserRequiredCount != 1 || run.FailedCount != 0 {
		t.Fatalf("browser_required_count=%d failed_count=%d, want 1 and 0", run.BrowserRequiredCount, run.FailedCount)
	}
	updated, err := service.store.GetSiteAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastCheckinStatus != provider.CheckinBrowserRequired {
		t.Fatalf("last check-in status=%q, want %q", updated.LastCheckinStatus, provider.CheckinBrowserRequired)
	}

	// The operator clears the challenge in a browser. Nothing the server can send
	// will pass the guard, so the day can only converge through the status
	// endpoint the middleware does not cover.
	checkedInToday.Store(true)
	attempt.FinishedAt = time.Now().Add(-siteCheckinBrowserRetryInterval - time.Minute).UnixMilli()
	if err := service.store.UpdateCheckinAttempt(ctx, attempt); err != nil {
		t.Fatal(err)
	}
	service.runSchedule(ctx, start.Add(time.Minute))

	settled, err := service.store.GetDailyCheckinAttempt(ctx, account.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if settled == nil || settled.ID != attempt.ID {
		t.Fatalf("converged row=%+v, want the day's row %d reused", settled, attempt.ID)
	}
	if settled.Status != provider.CheckinAlreadyChecked || settled.AttemptNo != 2 {
		t.Fatalf("converged row status=%q attempt_no=%d, want already_checked/2", settled.Status, settled.AttemptNo)
	}
	updated, err = service.store.GetSiteAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastCheckinStatus != provider.CheckinAlreadyChecked {
		t.Fatalf("last check-in status=%q, want %q", updated.LastCheckinStatus, provider.CheckinAlreadyChecked)
	}
}
