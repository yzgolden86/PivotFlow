package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func TestDailyBalanceFixedTimeWindow(t *testing.T) {
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	for _, item := range []struct {
		minute int
		second int
		due    bool
	}{{4, 59, false}, {5, 0, true}, {19, 59, true}, {20, 0, false}, {60, 0, false}} {
		now := time.Date(2026, 11, 24, 0, item.minute, item.second, 0, location)
		at, due := dailyBalanceWindow(now.UTC(), 5)
		if due != item.due || at.Hour() != 0 || at.Minute() != 5 || at.Day() != 24 {
			t.Fatalf("now=%v at=%v due=%v want=%v", now, at, due, item.due)
		}
	}
}

func TestDailyBalanceFixedWindowAcrossMidnight(t *testing.T) {
	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	for _, item := range []struct {
		now  string
		want string
		due  bool
	}{
		{"2026-11-23 23:54:59", "2026-11-23 23:55:00", false},
		{"2026-11-23 23:55:00", "2026-11-23 23:55:00", true},
		{"2026-11-24 00:01:00", "2026-11-23 23:55:00", true},
		{"2026-11-24 00:09:59", "2026-11-23 23:55:00", true},
		{"2026-11-24 00:10:00", "2026-11-24 23:55:00", false},
	} {
		t.Run(item.now, func(t *testing.T) {
			now, err := time.ParseInLocation("2006-01-02 15:04:05", item.now, location)
			if err != nil {
				t.Fatal(err)
			}
			at, due := dailyBalanceWindow(now.UTC(), 23*60+55)
			if at.Format("2006-01-02 15:04:05") != item.want || due != item.due {
				t.Fatalf("at=%v due=%v, want %s due=%v", at, due, item.want, item.due)
			}
		})
	}
}

type dailyBalanceAdapter struct {
	blockingCheckinAdapter
	reads   int
	models  int
	amount  *float64
	err     error
	started chan struct{}
	release chan struct{}
}

func (a *dailyBalanceAdapter) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Balance: true}
}
func (a *dailyBalanceAdapter) RefreshAccount(ctx context.Context, _ provider.RefreshAccountRequest) (provider.AccountSnapshot, error) {
	a.reads++
	if a.started != nil {
		close(a.started)
		select {
		case <-a.release:
		case <-ctx.Done():
			return provider.AccountSnapshot{}, ctx.Err()
		}
	}
	return provider.AccountSnapshot{Balance: a.amount, Currency: "USD", Status: model.SiteAccountStatusHealthy}, a.err
}

func TestDailyBalanceReadPreservesConcurrentAccountEdits(t *testing.T) {
	amount := 12.25
	adapter := &dailyBalanceAdapter{amount: &amount, started: make(chan struct{}), release: make(chan struct{})}
	service, account, now := dailyBalanceService(t, adapter)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(adapter.release) }) }
	t.Cleanup(release)
	done := make(chan struct{})
	go func() { service.runSchedule(context.Background(), now); close(done) }()
	select {
	case <-adapter.started:
	case <-time.After(3 * time.Second):
		t.Fatal("daily balance read did not start")
	}
	current, err := service.store.GetSiteAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	current.Enabled, current.Label, current.AutoRefresh = false, "edited-during-read", true
	if _, err := service.store.UpdateSiteAccount(context.Background(), current.ID, current); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("daily balance read did not finish")
	}
	current, err = service.store.GetSiteAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Enabled || current.Label != "edited-during-read" || !current.AutoRefresh {
		t.Fatalf("daily observation overwrote concurrent management edits: enabled=%v label=%q auto_refresh=%v", current.Enabled, current.Label, current.AutoRefresh)
	}
	if current.Balance == nil || *current.Balance != amount {
		t.Fatalf("latest balance was not updated: %+v", current.Balance)
	}
}
func (a *dailyBalanceAdapter) ListModels(context.Context, provider.AccountRequest) ([]provider.ModelSnapshot, error) {
	a.models++
	return nil, errors.New("daily sampling must never discover models")
}

func dailyBalanceService(t *testing.T, adapter provider.SiteAdapter) (*siteControlService, *model.SiteAccount, time.Time) {
	t.Helper()
	store, err := storage.CreateSQLiteStore(filepath.Join(t.TempDir(), "scheduled-balance.db"))
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "daily-balance-test")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal(provider.Credentials{AccessToken: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{Name: "daily", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://example.invalid", Enabled: true, Timezone: "UTC", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "daily", CredentialType: model.CredentialTypeAccessToken, CredentialCiphertext: sealed, CredentialKeyVersion: cipher.Version(), Enabled: true, AutoCheckin: false, AutoRefresh: false, BalanceCurrency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	wg := &sync.WaitGroup{}
	service := &siteControlService{store: store, cipher: cipher, registry: provider.NewRegistry(adapter), baseCtx: ctx, wg: wg}
	now := time.Now().In(dailyBalanceLocation())
	if err := store.BatchUpdateSettings(ctx, map[string]string{"site_daily_balance_time": now.Format("15:04"), "site_daily_balance_enabled": "true", "site_daily_announcement_time": "23:59"}); err != nil {
		t.Fatal(err)
	}
	service.configService = NewConfigService(store)
	if err := service.configService.LoadDefaults(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.stopTasks(); wg.Wait(); _ = store.Close() })
	return service, account, now
}

type dailyBalanceTimingAdapter struct {
	*dailyBalanceAdapter
	prepare func(context.Context, provider.AccountRequest) (provider.Credentials, error)
	refresh func(context.Context, provider.RefreshAccountRequest) (provider.AccountSnapshot, error)
}

func (a *dailyBalanceTimingAdapter) ResolveManagementCredentials(ctx context.Context, req provider.AccountRequest) (provider.Credentials, error) {
	if a.prepare != nil {
		return a.prepare(ctx, req)
	}
	return req.Credentials, nil
}

func (a *dailyBalanceTimingAdapter) RefreshAccount(ctx context.Context, req provider.RefreshAccountRequest) (provider.AccountSnapshot, error) {
	if a.refresh != nil {
		return a.refresh(ctx, req)
	}
	return a.dailyBalanceAdapter.RefreshAccount(ctx, req)
}

func TestDailyBalancePreparationDeadlinePreventsLateRead(t *testing.T) {
	amount := 12.25
	adapter := &dailyBalanceTimingAdapter{dailyBalanceAdapter: &dailyBalanceAdapter{amount: &amount}}
	service, account, _ := dailyBalanceService(t, adapter)
	site, err := service.store.GetSite(context.Background(), account.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	scheduled := time.Now().In(dailyBalanceLocation()).Add(-siteDailyBalanceWindow + 500*time.Millisecond)
	cutoff := scheduled.Add(siteDailyBalanceWindow)
	var preparationDeadline time.Time
	adapter.prepare = func(ctx context.Context, req provider.AccountRequest) (provider.Credentials, error) {
		preparationDeadline, _ = ctx.Deadline()
		// Deliberately ignore cancellation: a late preparation must still never
		// dispatch a balance read, even if this dependency misbehaves.
		time.Sleep(time.Until(cutoff.Add(50 * time.Millisecond)))
		return req.Credentials, nil
	}
	service.runScheduledDailyBalance(context.Background(), site, account, scheduled)
	if preparationDeadline.IsZero() || preparationDeadline.After(cutoff) {
		t.Errorf("preparation deadline=%v, want no later than %v", preparationDeadline, cutoff)
	}
	if adapter.reads != 0 {
		t.Errorf("expired preparation dispatched %d balance reads", adapter.reads)
	}
	rows, err := service.store.ListSiteAccountDailyBalances(context.Background(), "", "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if rows[0].Status != "failed" || rows[0].Balance != nil || rows[0].ErrorCode != provider.CodeTimeout {
		t.Errorf("late preparation became a valid sample: %+v", rows[0])
	}
}

func TestDailyBalanceLateProviderSuccessStaysUnknown(t *testing.T) {
	amount := 12.25
	adapter := &dailyBalanceTimingAdapter{dailyBalanceAdapter: &dailyBalanceAdapter{amount: &amount}}
	service, account, _ := dailyBalanceService(t, adapter)
	site, err := service.store.GetSite(context.Background(), account.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	scheduled := time.Now().In(dailyBalanceLocation()).Add(-siteDailyBalanceWindow + 500*time.Millisecond)
	cutoff := scheduled.Add(siteDailyBalanceWindow)
	var providerDeadline time.Time
	adapter.refresh = func(ctx context.Context, _ provider.RefreshAccountRequest) (provider.AccountSnapshot, error) {
		adapter.reads++
		providerDeadline, _ = ctx.Deadline()
		// A transport can finish with nil error after its cancellation. The
		// scheduler must validate the actual collection time independently.
		time.Sleep(time.Until(cutoff.Add(50 * time.Millisecond)))
		return provider.AccountSnapshot{Balance: &amount, Currency: "USD"}, nil
	}
	service.runScheduledDailyBalance(context.Background(), site, account, scheduled)
	if providerDeadline.IsZero() || providerDeadline.After(cutoff) {
		t.Errorf("provider deadline=%v, want no later than %v", providerDeadline, cutoff)
	}
	rows, err := service.store.ListSiteAccountDailyBalances(context.Background(), "", "")
	if err != nil || len(rows) != 1 || adapter.reads != 1 {
		t.Fatalf("rows=%+v err=%v reads=%d", rows, err, adapter.reads)
	}
	if rows[0].Status != "failed" || rows[0].Balance != nil || rows[0].ErrorCode != provider.CodeTimeout {
		t.Errorf("late provider success became a valid sample: %+v", rows[0])
	}
	latest, err := service.store.GetSiteAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Balance != nil {
		t.Errorf("late provider success updated the current balance: %v", *latest.Balance)
	}
}

type alternateDailyBalanceAdapter struct{ *dailyBalanceAdapter }

func (a *alternateDailyBalanceAdapter) ID() string { return model.SitePlatformSub2API }

func TestDailyBalanceQueuedPlatformEditUsesCurrentAdapter(t *testing.T) {
	oldAmount, currentAmount := 12.25, 17.5
	oldAdapter := &dailyBalanceAdapter{amount: &oldAmount}
	currentAdapter := &alternateDailyBalanceAdapter{&dailyBalanceAdapter{amount: &currentAmount}}
	service, account, _ := dailyBalanceService(t, oldAdapter)
	service.registry = provider.NewRegistry(oldAdapter, currentAdapter)
	site, err := service.store.GetSite(context.Background(), account.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	finishGate, ok := service.acquireSiteGate(context.Background(), site.ID)
	if !ok {
		t.Fatal("failed to hold the site gate")
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(finishGate) }
	t.Cleanup(release)
	done := make(chan struct{})
	go func() {
		service.runScheduledDailyBalance(context.Background(), site, account, time.Now().In(dailyBalanceLocation()))
		close(done)
	}()
	timeout, tick := time.NewTimer(3*time.Second), time.NewTicker(10*time.Millisecond)
	defer timeout.Stop()
	defer tick.Stop()
	for {
		service.submitMu.Lock()
		queued := len(service.activeSiteTasks) == 1
		service.submitMu.Unlock()
		if queued {
			break
		}
		select {
		case <-timeout.C:
			t.Fatal("daily sample was not queued behind the site gate")
		case <-tick.C:
		}
	}
	currentSite := *site
	currentSite.Platform = model.SitePlatformSub2API
	if _, err := service.store.UpdateSite(context.Background(), site.ID, &currentSite); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("queued daily sample did not finish")
	}
	rows, err := service.store.ListSiteAccountDailyBalances(context.Background(), "", "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if oldAdapter.reads != 0 || currentAdapter.reads != 1 || rows[0].Status != "success" || rows[0].Balance == nil || *rows[0].Balance != currentAmount {
		t.Fatalf("used stale queued provider: old_reads=%d current_reads=%d sample=%+v", oldAdapter.reads, currentAdapter.reads, rows[0])
	}
}

func TestDailyBalanceSchedulerSamplesOnceWithoutModelRefresh(t *testing.T) {
	amount := 12.25
	adapter := &dailyBalanceAdapter{amount: &amount}
	service, account, now := dailyBalanceService(t, adapter)
	service.runSchedule(context.Background(), now)
	amount = 99
	service.runSchedule(context.Background(), now.Add(time.Second))
	// Persisted claims, rather than an in-memory timestamp, prevent replay.
	service.activeSiteTasks = nil
	service.runSchedule(context.Background(), now.Add(2*time.Second))
	if adapter.reads != 1 || adapter.models != 0 {
		t.Fatalf("reads=%d models=%d, want one read and no models", adapter.reads, adapter.models)
	}
	rows, err := service.store.ListSiteAccountDailyBalances(context.Background(), "", "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if rows[0].SiteAccountID != account.ID || rows[0].LocalDay != now.Format("2006-01-02") || rows[0].Balance == nil || *rows[0].Balance != 12.25 || rows[0].Status != "success" {
		t.Fatalf("daily result=%+v", rows[0])
	}
}

func TestDailyBalanceFailedOrMissingBalanceStaysUnknown(t *testing.T) {
	for _, err := range []error{nil, &provider.Error{Code: provider.CodeTimeout}} {
		t.Run(fmt.Sprint(err), func(t *testing.T) {
			adapter := &dailyBalanceAdapter{err: err}
			service, _, now := dailyBalanceService(t, adapter)
			service.runSchedule(context.Background(), now)
			service.runSchedule(context.Background(), now.Add(time.Second))
			rows, loadErr := service.store.ListSiteAccountDailyBalances(context.Background(), "", "")
			if loadErr != nil || len(rows) != 1 || rows[0].Balance != nil || rows[0].Status != "failed" || adapter.reads != 1 {
				t.Fatalf("rows=%+v err=%v reads=%d", rows, loadErr, adapter.reads)
			}
		})
	}
}

func TestDailyBalanceDisabledAndLateStartupDoNotReadUpstream(t *testing.T) {
	for _, reason := range []string{"disabled_setting", "disabled_account", "late_startup"} {
		t.Run(reason, func(t *testing.T) {
			amount := 10.0
			adapter := &dailyBalanceAdapter{amount: &amount}
			service, account, now := dailyBalanceService(t, adapter)
			ctx := context.Background()
			switch reason {
			case "disabled_setting":
				if err := service.store.BatchUpdateSettings(ctx, map[string]string{"site_daily_balance_enabled": "false"}); err != nil {
					t.Fatal(err)
				}
				service.configService = NewConfigService(service.store)
				if err := service.configService.LoadDefaults(ctx); err != nil {
					t.Fatal(err)
				}
			case "disabled_account":
				account.Enabled = false
				if _, err := service.store.UpdateSiteAccount(ctx, account.ID, account); err != nil {
					t.Fatal(err)
				}
			case "late_startup":
				now = now.Add(16 * time.Minute)
			}
			service.runSchedule(ctx, now)
			if adapter.reads != 0 {
				t.Fatalf("%s read upstream %d times", reason, adapter.reads)
			}
		})
	}
}
