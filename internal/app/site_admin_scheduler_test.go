package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"

	"github.com/gin-gonic/gin"
)

func TestSiteTaskCancelHandlerCancelsRunningWorker(t *testing.T) {
	store, err := storage.CreateSQLiteStore(filepath.Join(t.TempDir(), "admin-cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()
	var wg sync.WaitGroup
	service := &siteControlService{store: store, baseCtx: baseCtx, wg: &wg, tasks: make(map[string]context.CancelFunc)}
	task := &model.SiteTask{ID: "st_admin_cancel", Kind: "refresh", Status: model.SiteTaskStatusQueued, ProgressJSON: `{}`, CreatedAt: time.Now().UnixMilli()}
	if err := store.CreateSiteTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finished := make(chan struct{})
	if !service.runAsync(task.ID, func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(finished)
	}) {
		t.Fatal("worker was rejected")
	}
	<-started
	router := gin.New()
	router.POST("/admin/site-tasks/:id/cancel", service.handleSiteTaskCancel)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/admin/site-tasks/"+task.ID+"/cancel", nil)
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("admin cancellation did not cancel the running worker")
	}
	stored, err := store.GetSiteTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.SiteTaskStatusCancelled {
		t.Fatalf("status=%q, want cancelled", stored.Status)
	}
	service.stopTasks()
	wg.Wait()
}

type blockingCheckinAdapter struct {
	release chan struct{}
	started chan string

	mu           sync.Mutex
	active       int
	maxActive    int
	activeBySite map[string]int
	maxBySite    map[string]int
	total        int
}

func (a *blockingCheckinAdapter) ID() string { return model.SitePlatformNewAPIFamily }
func (a *blockingCheckinAdapter) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{ServerCheckin: true}
}
func (a *blockingCheckinAdapter) Detect(context.Context, string) (provider.DetectionResult, error) {
	return provider.DetectionResult{Matched: true, ProviderID: a.ID()}, nil
}
func (a *blockingCheckinAdapter) RefreshAccount(context.Context, provider.RefreshAccountRequest) (provider.AccountSnapshot, error) {
	return provider.AccountSnapshot{Status: model.SiteAccountStatusHealthy}, nil
}
func (a *blockingCheckinAdapter) ListModels(context.Context, provider.AccountRequest) ([]provider.ModelSnapshot, error) {
	return []provider.ModelSnapshot{{Model: "gpt-test", RouteType: "openai_chat", Source: "test"}}, nil
}
func (a *blockingCheckinAdapter) ListAnnouncements(context.Context, provider.AccountRequest) ([]provider.Announcement, error) {
	return nil, nil
}
func (a *blockingCheckinAdapter) Checkin(ctx context.Context, req provider.AccountRequest) (provider.CheckinResult, error) {
	a.mu.Lock()
	a.active++
	a.total++
	a.activeBySite[req.BaseURL]++
	if a.active > a.maxActive {
		a.maxActive = a.active
	}
	if a.activeBySite[req.BaseURL] > a.maxBySite[req.BaseURL] {
		a.maxBySite[req.BaseURL] = a.activeBySite[req.BaseURL]
	}
	a.mu.Unlock()
	a.started <- req.BaseURL
	select {
	case <-a.release:
	case <-ctx.Done():
		return provider.CheckinResult{Status: provider.CheckinFailed}, ctx.Err()
	}
	a.mu.Lock()
	a.active--
	a.activeBySite[req.BaseURL]--
	a.mu.Unlock()
	return provider.CheckinResult{Status: provider.CheckinSuccess}, nil
}

func TestSiteSchedulerLimitsGlobalConcurrencyAndSerializesEachSite(t *testing.T) {
	store, err := storage.CreateSQLiteStore(filepath.Join(t.TempDir(), "scheduler.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "scheduler-test")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal(provider.Credentials{AccessToken: "session-token"})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &blockingCheckinAdapter{
		release:      make(chan struct{}),
		started:      make(chan string, 16),
		activeBySite: make(map[string]int),
		maxBySite:    make(map[string]int),
	}
	service := &siteControlService{store: store, cipher: cipher, registry: provider.NewRegistry(adapter)}
	ctx := context.Background()
	accountCount := 0
	for siteIndex := 0; siteIndex < 5; siteIndex++ {
		site, err := store.CreateSite(ctx, &model.Site{Name: "site-" + string(rune('a'+siteIndex)), Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://site-" + string(rune('a'+siteIndex)) + ".example", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]", LastProbeStatus: "success"})
		if err != nil {
			t.Fatal(err)
		}
		accountsForSite := 1
		if siteIndex == 0 {
			accountsForSite = 2
		}
		for accountIndex := 0; accountIndex < accountsForSite; accountIndex++ {
			_, err := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "account-" + string(rune('a'+accountIndex)), CredentialType: model.CredentialTypeAccessToken, CredentialCiphertext: sealed, CredentialKeyVersion: cipher.Version(), Enabled: true, AutoCheckin: true, AutoRefresh: false, Status: model.SiteAccountStatusHealthy, BalanceCurrency: "CNY", LastRefreshStatus: "unknown", LastCheckinStatus: "unknown"})
			if err != nil {
				t.Fatal(err)
			}
			accountCount++
		}
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		service.runSchedule(ctx, time.Date(2026, 8, 10, 9, 0, 0, 0, location))
		close(done)
	}()
	for i := 0; i < siteSchedulerConcurrency; i++ {
		select {
		case <-adapter.started:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d scheduled tasks started", i)
		}
	}
	select {
	case extra := <-adapter.started:
		t.Fatalf("global concurrency exceeded before release: %s", extra)
	case <-time.After(100 * time.Millisecond):
	}
	close(adapter.release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not finish")
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.maxActive != siteSchedulerConcurrency {
		t.Fatalf("max active=%d, want %d", adapter.maxActive, siteSchedulerConcurrency)
	}
	if adapter.total != accountCount {
		t.Fatalf("check-ins=%d, want %d", adapter.total, accountCount)
	}
	for siteURL, maximum := range adapter.maxBySite {
		if maximum != 1 {
			t.Fatalf("site %s concurrency=%d, want 1", siteURL, maximum)
		}
	}
}

func TestDailyCheckinTimeBoundaries(t *testing.T) {
	srv := newInMemoryServerWithSettings(t, map[string]string{"site_daily_checkin_time": "09:30"})
	if got := srv.siteControl.dailyCheckinMinute(); got != 9*60+30 {
		t.Fatalf("daily check-in minute=%d, want %d", got, 9*60+30)
	}
	before := time.Date(2026, 8, 18, 9, 29, 0, 0, time.UTC)
	atTime := time.Date(2026, 8, 18, 9, 30, 0, 0, time.UTC)
	if dailyCheckinDue(before, srv.siteControl.dailyCheckinMinute()) {
		t.Fatal("09:29 must not trigger a 09:30 check-in")
	}
	if !dailyCheckinDue(atTime, srv.siteControl.dailyCheckinMinute()) {
		t.Fatal("09:30 must trigger a 09:30 check-in")
	}
	if dailyCheckinDue(time.Date(2026, 8, 18, 7, 59, 0, 0, time.UTC), 8*60) {
		t.Fatal("07:59 must not trigger the default 08:00 check-in")
	}
}

// gateTrackingAdapter blocks inside RefreshAccount so a test can observe how
// many refreshes for the same site actually overlap at the adapter boundary.
type gateTrackingAdapter struct {
	blockingCheckinAdapter

	release chan struct{}
	started chan struct{}

	mu           sync.Mutex
	active       int
	maxActive    int
	activeBySite map[string]int
	maxBySite    map[string]int
	calls        int
}

func (a *gateTrackingAdapter) RefreshAccount(ctx context.Context, req provider.RefreshAccountRequest) (provider.AccountSnapshot, error) {
	a.mu.Lock()
	a.active++
	a.calls++
	if a.active > a.maxActive {
		a.maxActive = a.active
	}
	a.activeBySite[req.BaseURL]++
	if a.activeBySite[req.BaseURL] > a.maxBySite[req.BaseURL] {
		a.maxBySite[req.BaseURL] = a.activeBySite[req.BaseURL]
	}
	a.mu.Unlock()
	a.started <- struct{}{}
	select {
	case <-a.release:
	case <-ctx.Done():
		return provider.AccountSnapshot{}, ctx.Err()
	}
	a.mu.Lock()
	a.active--
	a.activeBySite[req.BaseURL]--
	a.mu.Unlock()
	return provider.AccountSnapshot{Status: model.SiteAccountStatusHealthy}, nil
}

func TestManualAccountRefreshesSerializePerSite(t *testing.T) {
	store, err := storage.CreateSQLiteStore(filepath.Join(t.TempDir(), "site-gate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "site-gate-test")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal(provider.Credentials{AccessToken: "session-token"})
	if err != nil {
		t.Fatal(err)
	}
	adapter := &gateTrackingAdapter{
		release:      make(chan struct{}),
		started:      make(chan struct{}, 8),
		activeBySite: make(map[string]int),
		maxBySite:    make(map[string]int),
	}
	var wg sync.WaitGroup
	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()
	service := &siteControlService{
		store:    store,
		cipher:   cipher,
		registry: provider.NewRegistry(adapter),
		baseCtx:  baseCtx,
		wg:       &wg,
		tasks:    make(map[string]context.CancelFunc),
	}
	ctx := context.Background()
	var accounts []int64
	for siteIndex := 0; siteIndex < 2; siteIndex++ {
		site, err := store.CreateSite(ctx, &model.Site{Name: "gate-site-" + string(rune('a'+siteIndex)), Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://gate-" + string(rune('a'+siteIndex)) + ".example", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]", LastProbeStatus: "success"})
		if err != nil {
			t.Fatal(err)
		}
		accountsForSite := 1
		if siteIndex == 0 {
			accountsForSite = 2
		}
		for accountIndex := 0; accountIndex < accountsForSite; accountIndex++ {
			account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "gate-account-" + string(rune('a'+accountIndex)), CredentialType: model.CredentialTypeAccessToken, CredentialCiphertext: sealed, CredentialKeyVersion: cipher.Version(), Enabled: true, AutoCheckin: false, AutoRefresh: false, Status: model.SiteAccountStatusHealthy, BalanceCurrency: "CNY", LastRefreshStatus: "unknown", LastCheckinStatus: "unknown"})
			if err != nil {
				t.Fatal(err)
			}
			accounts = append(accounts, account.ID)
		}
	}
	router := gin.New()
	router.POST("/admin/site-accounts/:id/refresh", service.handleAccountRefresh)

	taskIDs := make([]string, 0, len(accounts))
	for _, id := range accounts {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/admin/site-accounts/"+strconv.FormatInt(id, 10)+"/refresh", nil)
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		var payload struct {
			Data struct {
				TaskID string `json:"task_id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil || payload.Data.TaskID == "" {
			t.Fatalf("unexpected enqueue response: %s", recorder.Body.String())
		}
		taskIDs = append(taskIDs, payload.Data.TaskID)
	}
	// Two refreshes (one per site) may run; the second same-site one must queue.
	for i := 0; i < 2; i++ {
		select {
		case <-adapter.started:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d refreshes started", i)
		}
	}
	select {
	case <-adapter.started:
		t.Fatal("two refreshes for the same site started concurrently")
	case <-time.After(150 * time.Millisecond):
	}
	close(adapter.release)
	select {
	case <-adapter.started:
	case <-time.After(2 * time.Second):
		t.Fatal("queued same-site refresh did not start after release")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		pending := false
		for _, id := range taskIDs {
			task, err := store.GetSiteTask(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			switch task.Status {
			case model.SiteTaskStatusSuccess, model.SiteTaskStatusFailed, model.SiteTaskStatusPartial, model.SiteTaskStatusCancelled:
			default:
				pending = true
			}
		}
		if !pending {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refresh tasks did not reach a terminal status")
		}
		time.Sleep(20 * time.Millisecond)
	}
	wg.Wait()
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if adapter.maxActive > 2 {
		t.Fatalf("global manual refresh concurrency=%d, want <= 2", adapter.maxActive)
	}
	for siteURL, maximum := range adapter.maxBySite {
		if maximum != 1 {
			t.Fatalf("site %s refresh concurrency=%d, want 1", siteURL, maximum)
		}
	}
	if adapter.calls != len(accounts) {
		t.Fatalf("refresh calls=%d, want %d", adapter.calls, len(accounts))
	}
}

// timeoutOnceAdapter fails the first balance refresh with a throttled timeout,
// then succeeds — refreshAccount must retry instead of flagging the account.
type timeoutOnceAdapter struct {
	blockingCheckinAdapter

	mu    sync.Mutex
	calls int
}

func (a *timeoutOnceAdapter) RefreshAccount(context.Context, provider.RefreshAccountRequest) (provider.AccountSnapshot, error) {
	a.mu.Lock()
	a.calls++
	calls := a.calls
	a.mu.Unlock()
	if calls == 1 {
		return provider.AccountSnapshot{}, &provider.Error{Code: provider.CodeTimeout, Message: "throttled"}
	}
	balance := 123.45
	return provider.AccountSnapshot{Status: model.SiteAccountStatusHealthy, Balance: &balance, Currency: "CNY"}, nil
}

func newSiteRefreshTestService(t *testing.T, adapter provider.SiteAdapter) (*siteControlService, *model.Site, *model.SiteAccount) {
	t.Helper()
	store, err := storage.CreateSQLiteStore(filepath.Join(t.TempDir(), "site-retry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "site-retry-test")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal(provider.Credentials{AccessToken: "session-token"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{Name: "retry-site", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://retry.example", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]", LastProbeStatus: "success"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "retry-account", CredentialType: model.CredentialTypeAccessToken, CredentialCiphertext: sealed, CredentialKeyVersion: cipher.Version(), Enabled: true, AutoCheckin: false, AutoRefresh: false, Status: model.SiteAccountStatusHealthy, BalanceCurrency: "CNY", LastRefreshStatus: "unknown", LastCheckinStatus: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	return &siteControlService{store: store, cipher: cipher, registry: provider.NewRegistry(adapter), baseCtx: context.Background()}, site, account
}

func runSiteRefreshTask(t *testing.T, service *siteControlService, accountID int64) *model.SiteTask {
	t.Helper()
	task := &model.SiteTask{ID: newSiteTaskID(), Kind: "refresh", Status: model.SiteTaskStatusQueued, ProgressJSON: "{}", CreatedAt: time.Now().UnixMilli()}
	if err := service.store.CreateSiteTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	service.refreshAccount(context.Background(), task, accountID, false)
	stored, err := service.store.GetSiteTask(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return stored
}

func TestRefreshAccountRetriesOnTimeout(t *testing.T) {
	adapter := &timeoutOnceAdapter{}
	service, _, account := newSiteRefreshTestService(t, adapter)
	stored := runSiteRefreshTask(t, service, account.ID)
	if stored.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("task status=%q error=%q, want success after timeout retry", stored.Status, stored.Error)
	}
	adapter.mu.Lock()
	calls := adapter.calls
	adapter.mu.Unlock()
	if calls != 2 {
		t.Fatalf("refresh calls=%d, want 2 (one timeout + one retry)", calls)
	}
	updated, err := service.store.GetSiteAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastRefreshStatus != "success" || updated.ConsecutiveFailures != 0 || updated.Status != model.SiteAccountStatusHealthy {
		t.Fatalf("account status=%q lastError=%q failures=%d, want healthy after retry", updated.LastRefreshStatus, updated.LastError, updated.ConsecutiveFailures)
	}
}

func TestRefreshAccountGivesUpAfterTimeoutRetries(t *testing.T) {
	adapter := &timeoutAlwaysAdapter{}
	service, _, account := newSiteRefreshTestService(t, adapter)
	stored := runSiteRefreshTask(t, service, account.ID)
	if stored.Status != model.SiteTaskStatusFailed {
		t.Fatalf("task status=%q, want failed after exhausted retries", stored.Status)
	}
	adapter.mu.Lock()
	calls := adapter.calls
	adapter.mu.Unlock()
	if calls != 3 {
		t.Fatalf("refresh calls=%d, want 3", calls)
	}
	updated, err := service.store.GetSiteAccount(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastError != provider.CodeTimeout || updated.Status != model.SiteAccountStatusError {
		t.Fatalf("account lastError=%q status=%q, want provider_timeout/error", updated.LastError, updated.Status)
	}
}

type timeoutAlwaysAdapter struct {
	blockingCheckinAdapter

	mu    sync.Mutex
	calls int
}

func (a *timeoutAlwaysAdapter) RefreshAccount(context.Context, provider.RefreshAccountRequest) (provider.AccountSnapshot, error) {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	return provider.AccountSnapshot{}, &provider.Error{Code: provider.CodeTimeout, Message: "throttled"}
}

func TestSiteRefreshDueJitter(t *testing.T) {
	now := time.Now()
	if !siteRefreshDue(&model.SiteAccount{}, now) {
		t.Fatal("an account never refreshed must be due immediately")
	}
	refreshedAt := now.Add(-siteRefreshInterval).UnixMilli()
	if !siteRefreshDue(&model.SiteAccount{ID: 30, LastRefreshAt: refreshedAt}, now) {
		t.Fatal("jitter 0 accounts must fall due exactly at the 6h mark")
	}
	if siteRefreshDue(&model.SiteAccount{ID: 1, LastRefreshAt: refreshedAt}, now) {
		t.Fatal("account 1 must wait 6h plus its jitter, not plain 6h")
	}
	due := &model.SiteAccount{ID: 1, LastRefreshAt: now.Add(-siteRefreshInterval - siteRefreshJitter(1)).UnixMilli()}
	if !siteRefreshDue(due, now) {
		t.Fatal("account 1 must be due once 6h plus its jitter elapsed")
	}
}
