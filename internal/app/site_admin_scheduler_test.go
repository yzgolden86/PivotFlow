package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ccLoad/internal/model"
	"ccLoad/internal/site/credential"
	"ccLoad/internal/site/provider"
	"ccLoad/internal/storage"

	"github.com/gin-gonic/gin"
)

func TestSiteTaskCancelHandlerCancelsRunningWorker(t *testing.T) {
	store, err := storage.CreateSQLiteStore(filepath.Join(t.TempDir(), "admin-cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
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
	defer store.Close()
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
