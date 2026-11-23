package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func TestSiteTaskQueueCoalescesAndCreatesFreshTaskAfterCompletion(t *testing.T) {
	service, site, account := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	var workers, submitters sync.WaitGroup
	service.wg = &workers
	defer func() { service.stopTasks(); workers.Wait() }()
	var calls atomic.Int32
	ids := make(chan string, 16)
	for i := 0; i < 16; i++ {
		submitters.Add(1)
		go func() {
			defer submitters.Done()
			run, _, err := service.submitSiteTask(context.Background(), siteTaskSpec{key: "same", kind: "refresh", siteID: site.ID, accountID: account.ID, total: 1}, func(ctx context.Context, task *model.SiteTask) {
				calls.Add(1)
				<-ctx.Done()
				service.updateTask(ctx, task, model.SiteTaskStatusCancelled, "", "")
			})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- run.id
		}()
	}
	submitters.Wait()
	close(ids)
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Errorf("duplicate task IDs: %s != %s", id, first)
		}
	}
	if got := countRows(t, service.store, "SELECT COUNT(*) FROM site_tasks"); got != 1 {
		t.Fatalf("rows=%d, want 1", got)
	}
	service.cancelTask(first)
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatalf("executions=%d", calls.Load())
	}
	run, merged, err := service.submitSiteTask(context.Background(), siteTaskSpec{key: "same", kind: "refresh", total: 1}, func(ctx context.Context, task *model.SiteTask) {
		service.updateTask(ctx, task, model.SiteTaskStatusSuccess, "", "")
	})
	if err != nil || merged || run.id == first {
		t.Fatalf("fresh run=%+v merged=%v err=%v", run, merged, err)
	}
	<-run.done
}

func TestSiteTaskQueueBoundAndShutdown(t *testing.T) {
	service, _, _ := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	var wg sync.WaitGroup
	service.wg = &wg
	defer func() { service.stopTasks(); wg.Wait() }()
	for i := 0; i < 128; i++ {
		_, _, err := service.submitSiteTask(context.Background(), siteTaskSpec{key: fmt.Sprint(i), kind: "refresh", total: 1}, func(ctx context.Context, task *model.SiteTask) {
			<-ctx.Done()
			service.updateTask(ctx, task, model.SiteTaskStatusCancelled, "", "")
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := service.submitSiteTask(context.Background(), siteTaskSpec{key: "overflow", kind: "refresh"}, func(context.Context, *model.SiteTask) {}); !errors.Is(err, errSiteTaskQueueFull) {
		t.Fatalf("overflow error=%v", err)
	}
	if got := countRows(t, service.store, "SELECT COUNT(*) FROM site_tasks"); got != 128 {
		t.Fatalf("overflow created row: %d", got)
	}
	service.stopTasks()
	wg.Wait()
	if _, _, err := service.submitSiteTask(context.Background(), siteTaskSpec{key: "late", kind: "refresh"}, func(context.Context, *model.SiteTask) {}); !errors.Is(err, errSiteTaskStopped) {
		t.Fatalf("stopped error=%v", err)
	}
}

func TestSiteTaskQueueGlobalExecutionLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		service := &siteControlService{}
		var wg sync.WaitGroup
		starts := make(chan func(), 5)
		for i := int64(1); i <= 5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, finish, err := service.acquireManualSiteTask(context.Background(), i)
				if err != nil {
					t.Error(err)
					return
				}
				starts <- finish
			}()
		}
		synctest.Wait()
		if len(starts) != 4 {
			t.Fatalf("running=%d, want 4", len(starts))
		}
		(<-starts)()
		synctest.Wait()
		wg.Wait()
		for len(starts) > 0 {
			(<-starts)()
		}
	})
}

func TestSiteTaskQueueScheduledTaskIsCancelableAndSharesManualCheckin(t *testing.T) {
	adapter := &blockingCheckinAdapter{release: make(chan struct{}), started: make(chan string, 4), activeBySite: make(map[string]int), maxBySite: make(map[string]int)}
	service, site, account := newSiteRefreshTestService(t, adapter)
	var wg sync.WaitGroup
	service.wg = &wg
	defer func() { service.stopTasks(); wg.Wait() }()
	finished := make(chan struct{})
	go func() {
		service.runScheduledAccountTask(context.Background(), make(chan struct{}, 4), site, account, "checkin")
		close(finished)
	}()
	select {
	case <-adapter.started:
	case <-time.After(3 * time.Second):
		t.Fatal("schedule did not start")
	}
	router := gin.New()
	router.POST("/accounts/:id/checkin", service.handleAccountCheckin)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/accounts/"+strconv.FormatInt(account.ID, 10)+"/checkin", nil))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("manual status=%d %s", recorder.Code, recorder.Body)
	}
	if got := countRows(t, service.store, "SELECT COUNT(*) FROM site_tasks"); got != 1 {
		t.Fatalf("manual duplicated schedule: %d rows", got)
	}
	raw := service.store.(rawQueryStore)
	rows, err := raw.QueryContext(context.Background(), "SELECT id FROM site_tasks")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("missing task")
	}
	var id string
	if err := rows.Scan(&id); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	service.cancelTask(id)
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("scheduled work did not observe cancellation")
	}
	stored, err := service.store.GetSiteTask(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.SiteTaskStatusCancelled {
		t.Fatalf("cancelled schedule status=%s", stored.Status)
	}
	day := time.Now().In(loadSiteLocation(account.Timezone, site.Timezone)).Format("2006-01-02")
	attempt, err := service.store.GetDailyCheckinAttempt(context.Background(), account.ID, day)
	if err != nil || attempt == nil || attempt.Status != "cancelled" || attempt.FinishedAt == 0 {
		t.Fatalf("cancelled attempt remained in flight: %+v err=%v", attempt, err)
	}
	if due, _ := checkinRetryDue(attempt, time.Now().Add(time.Minute)); due {
		t.Fatal("scheduler immediately re-enqueued cancelled check-in")
	}
}

type queueRateLimitedCheckin struct {
	checkinMethodTestAdapter
	calls atomic.Int32
}

func (a *queueRateLimitedCheckin) Checkin(context.Context, provider.AccountRequest) (provider.CheckinResult, error) {
	a.calls.Add(1)
	return provider.CheckinResult{Status: provider.CheckinFailed}, &provider.Error{Code: provider.CodeRateLimited, StatusCode: 429, RetryAfter: time.Now().Add(time.Second)}
}
func TestSiteTaskQueueCheckinDoesNotReplayRateLimitedPost(t *testing.T) {
	adapter := &queueRateLimitedCheckin{}
	service, site, account := newSiteRefreshTestService(t, adapter)
	runCheckinTask(t, service, site, account)
	if got := adapter.calls.Load(); got != 1 {
		t.Fatalf("429 checkin requests=%d, want 1", got)
	}
}

func TestSiteTaskQueueActiveListIncludesScheduledWork(t *testing.T) {
	service, site, account := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	var wg sync.WaitGroup
	service.wg = &wg
	defer func() { service.stopTasks(); wg.Wait() }()
	run, _, err := service.submitSiteTask(context.Background(), siteTaskSpec{key: "visible", kind: "checkin", siteID: site.ID, accountID: account.ID, total: 1}, func(ctx context.Context, task *model.SiteTask) {
		<-ctx.Done()
		service.updateTask(ctx, task, model.SiteTaskStatusCancelled, "", "")
	})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/tasks", service.handleActiveSiteTasks)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/tasks", nil))
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), run.id) || !strings.Contains(recorder.Body.String(), `"site_account_id":`+strconv.FormatInt(account.ID, 10)) {
		t.Fatalf("missing task identity: %d %s", recorder.Code, recorder.Body)
	}
	service.cancelTask(run.id)
	<-run.done
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/tasks", nil))
	if strings.Contains(recorder.Body.String(), run.id) {
		t.Fatalf("finished task in active list: %s", recorder.Body)
	}
}

type queuePricingAdapter struct {
	checkinMethodTestAdapter
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (a *queuePricingAdapter) FetchPricing(ctx context.Context, _ provider.AccountRequest) (provider.SitePricing, error) {
	a.calls.Add(1)
	a.started <- struct{}{}
	select {
	case <-ctx.Done():
		return provider.SitePricing{}, ctx.Err()
	case <-a.release:
		return provider.SitePricing{Models: []provider.ModelPrice{{Model: "synthetic-model"}}}, nil
	}
}

func TestSiteTaskQueuePricingCoalescesAndWaiterCancellationIsIndependent(t *testing.T) {
	adapter := &queuePricingAdapter{started: make(chan struct{}, 4), release: make(chan struct{})}
	service, site, _ := newSiteRefreshTestService(t, adapter)
	server := &Server{store: service.store, siteControl: service}
	firstCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { _, err := server.fetchSitePricing(firstCtx, site.ID); first <- err }()
	select {
	case <-adapter.started:
	case <-time.After(time.Second):
		t.Fatal("pricing did not start")
	}
	go func() {
		pricing, err := server.fetchSitePricing(context.Background(), site.ID)
		if err == nil && (len(pricing.Models) != 1 || pricing.Models[0].Model != "synthetic-model") {
			t.Error("missing shared price")
		}
		second <- err
	}()
	// Both call paths must reach the coordinator before the shared read finishes.
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first waiter=%v", err)
	}
	close(adapter.release)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if got := adapter.calls.Load(); got != 1 {
		t.Fatalf("price upstream calls=%d, want 1", got)
	}
}

func TestSiteTaskQueuePricingStopsWithControlService(t *testing.T) {
	adapter := &queuePricingAdapter{started: make(chan struct{}, 1), release: make(chan struct{})}
	service, site, _ := newSiteRefreshTestService(t, adapter)
	var wg sync.WaitGroup
	service.wg = &wg
	server := &Server{store: service.store, siteControl: service}
	done := make(chan error, 1)
	go func() { _, err := server.fetchSitePricing(context.Background(), site.ID); done <- err }()
	<-adapter.started
	service.stopTasks()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown pricing error=%v", err)
		}
	case <-time.After(200 * time.Millisecond):
		close(adapter.release)
		<-done
		wg.Wait()
		t.Fatal("shared pricing request outlived the stopped control service")
	}
	wg.Wait()
}

type queueAnnouncementAdapter struct {
	checkinMethodTestAdapter
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (a *queueAnnouncementAdapter) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Announcements: true}
}
func (a *queueAnnouncementAdapter) ListAnnouncements(ctx context.Context, _ provider.AccountRequest) ([]provider.Announcement, error) {
	a.calls.Add(1)
	a.started <- struct{}{}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.release:
		return nil, nil
	}
}

func TestSiteTaskQueueAnnouncementBatchSharesChildAndReportsItems(t *testing.T) {
	adapter := &queueAnnouncementAdapter{started: make(chan struct{}, 4), release: make(chan struct{})}
	service, site, _ := newSiteRefreshTestService(t, adapter)
	var wg sync.WaitGroup
	service.wg = &wg
	defer func() { service.stopTasks(); wg.Wait() }()
	child, _, err := service.submitAnnouncementTask(context.Background(), site.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	<-adapter.started
	batch, _, err := service.submitAnnouncementBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	again, merged, err := service.submitAnnouncementBatch(context.Background())
	if err != nil || !merged || again.id != batch.id {
		t.Fatalf("batch not merged: %v %v", merged, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		stored, err := service.store.GetSiteTask(context.Background(), batch.id)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stored.ProgressJSON, child.id) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("batch did not attach child")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(adapter.release)
	<-child.done
	<-batch.done
	if adapter.calls.Load() != 1 {
		t.Fatalf("bulk repeated child: %d", adapter.calls.Load())
	}
	stored, err := service.store.GetSiteTask(context.Background(), batch.id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "success" || !strings.Contains(stored.ProgressJSON, child.id) || !strings.Contains(stored.ProgressJSON, `"completed":1`) {
		t.Fatalf("batch result=%+v", stored)
	}
}
