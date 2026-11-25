package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func TestSiteTaskOutcomePreservesKnownSuccessAfterCancellation(t *testing.T) {
	service, site, account := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	for _, status := range []string{model.SiteTaskStatusSuccess, model.SiteTaskStatusPartial} {
		task := newSiteTask("checkin", site.ID, account.ID, 1)
		if err := service.store.CreateSiteTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		service.updateTask(ctx, task, status, "observed_result", "")
		stored, err := service.store.GetSiteTask(context.Background(), task.ID)
		if err != nil || stored.Status != status {
			t.Fatalf("known outcome overwritten: %+v, %v", stored, err)
		}
	}
}

type outcomeCheckinAdapter struct {
	checkinMethodTestAdapter
	reads       atomic.Int32
	postRefresh chan struct{}
}

func (a *outcomeCheckinAdapter) RefreshAccount(ctx context.Context, _ provider.RefreshAccountRequest) (provider.AccountSnapshot, error) {
	if a.reads.Add(1) == 2 {
		close(a.postRefresh)
		<-ctx.Done()
		return provider.AccountSnapshot{}, ctx.Err()
	}
	return provider.AccountSnapshot{Status: model.SiteAccountStatusHealthy}, nil
}

func TestSiteTaskOutcomeCancelAfterCheckinSuccessReturnsSuccess(t *testing.T) {
	adapter := &outcomeCheckinAdapter{postRefresh: make(chan struct{})}
	service, _, account := newSiteRefreshTestService(t, adapter)
	var wg sync.WaitGroup
	service.wg = &wg
	defer func() { service.stopTasks(); wg.Wait() }()
	run, _, err := service.submitAccountTask(context.Background(), account, "checkin", false, false)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-adapter.postRefresh:
	case <-time.After(3 * time.Second):
		t.Fatal("post-checkin refresh did not start")
	}
	router := gin.New()
	router.POST("/tasks/:id/cancel", service.handleSiteTaskCancel)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/tasks/"+run.id+"/cancel", nil))
	<-run.done
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"success"`) || !strings.Contains(response.Body.String(), `"cancelled":false`) {
		t.Fatalf("observed upstream success misreported: %d %s", response.Code, response.Body)
	}
	updated, err := service.store.GetSiteAccount(context.Background(), account.ID)
	if err != nil || updated.LastCheckinStatus != provider.CheckinSuccess {
		t.Fatalf("lost confirmed checkin result: %+v %v", updated, err)
	}
}

func TestSiteTaskOutcomeQueuedScheduledCancellationRecordsDailyAttempt(t *testing.T) {
	service, site, account := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	var wg sync.WaitGroup
	service.wg = &wg
	defer func() { service.stopTasks(); wg.Wait() }()
	release, ok := service.acquireSiteGate(context.Background(), site.ID)
	if !ok {
		t.Fatal("site gate unavailable")
	}
	defer release()
	run, _, err := service.submitAccountTask(context.Background(), account, "checkin", false, true)
	if err != nil {
		t.Fatal(err)
	}
	service.cancelTask(run.id)
	<-run.done
	day := time.Now().In(loadSiteLocation(account.Timezone, site.Timezone)).Format("2006-01-02")
	attempt, err := service.store.GetDailyCheckinAttempt(context.Background(), account.ID, day)
	if err != nil || attempt == nil || attempt.Status != model.SiteTaskStatusCancelled {
		t.Fatalf("queued cancelled daily attempt missing: %+v %v", attempt, err)
	}
	if due, _ := checkinRetryDue(attempt, time.Now().Add(time.Minute)); due {
		t.Fatal("cancelled daily checkin will replay next tick")
	}
	checkinRun, err := service.store.GetCheckinRun(context.Background(), attempt.RunID)
	if err != nil || checkinRun.FinishedAt == 0 || checkinRun.Status != model.SiteTaskStatusCancelled {
		t.Fatalf("queued cancelled checkin run unfinished: %+v %v", checkinRun, err)
	}
}

func TestSiteTaskOutcomeOrphanedActiveRowIsUnknown(t *testing.T) {
	service, site, account := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	task := newSiteTask("refresh", site.ID, account.ID, 1)
	if err := service.store.CreateSiteTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/tasks/:id", service.handleSiteTask)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID, nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"unknown"`) {
		t.Fatalf("orphaned row still shown executing: %d %s", response.Code, response.Body)
	}
	router.POST("/tasks/:id/cancel", service.handleSiteTaskCancel)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/cancel", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"unknown"`) || strings.Contains(response.Body.String(), `"cancelled":true`) {
		t.Fatalf("unowned task falsely cancelled: %s", response.Body)
	}
}

func TestSiteTaskOutcomePayloadDoesNotExposeRawError(t *testing.T) {
	service, site, _ := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	task := newSiteTask("announcement_refresh", site.ID, 0, 1)
	task.Status = model.SiteTaskStatusFailed
	task.Error = "request_failed: synthetic-private-body"
	task.ProgressJSON = `{"completed":1,"total":1,"items":[{"site_id":1,"status":"failed","error":"request_failed: synthetic-private-body"}]}`
	data, err := json.Marshal(service.siteTaskPayload(context.Background(), task))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "synthetic-private-body") || !strings.Contains(string(data), "request_failed") {
		t.Fatalf("unsafe task DTO: %s", data)
	}
}

func TestSiteTaskOutcomePayloadProjectsSafeChannelResults(t *testing.T) {
	service, site, account := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	task := newSiteTask("model_refresh", site.ID, account.ID, 1)
	task.Status = model.SiteTaskStatusPartial
	task.ProgressJSON = `{"completed":1,"total":1,"private":"synthetic-private-progress","routes":[{"name":"Configured channel A","status":"updated","api_key":"synthetic-private-key"},{"name":"Configured channel B","status":"failed","error":"provider_rate_limited: synthetic-private-body"},{"name":"Configured channel C","status":"failed","error":"synthetic-private-error"},{"name":"Configured channel D","status":"deactivated"},{"name":"synthetic-private-unknown-status","status":"synthetic-private-status"}]}`
	data, err := json.Marshal(service.siteTaskPayload(context.Background(), task))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Progress struct {
			Completed int `json:"completed"`
			Total     int `json:"total"`
			Routes    []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Error  string `json:"error"`
			} `json:"routes"`
		} `json:"progress"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Progress.Completed != 1 || payload.Progress.Total != 1 || len(payload.Progress.Routes) != 4 {
		t.Fatalf("known channel results missing: %s", data)
	}
	if payload.Progress.Routes[0].Name != "Configured channel A" || payload.Progress.Routes[0].Status != "updated" ||
		payload.Progress.Routes[1].Error != provider.CodeRateLimited || payload.Progress.Routes[2].Error != provider.CodeRequestFailed ||
		payload.Progress.Routes[3].Status != "deactivated" {
		t.Fatalf("channel result projection lost identity or safe outcome: %s", data)
	}
	if strings.Contains(string(data), "synthetic-private") || strings.Contains(string(data), "api_key") {
		t.Fatalf("channel result projection exposed private fields: %s", data)
	}
}

func TestSiteTaskOutcomeActiveListToleratesDeletedTarget(t *testing.T) {
	service, site, account := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	var wg sync.WaitGroup
	service.wg = &wg
	defer func() { service.stopTasks(); wg.Wait() }()
	run, _, err := service.submitSiteTask(context.Background(), siteTaskSpec{key: "deleted", kind: "refresh", siteID: site.ID, accountID: account.ID}, func(ctx context.Context, task *model.SiteTask) {
		<-ctx.Done()
		service.updateTask(ctx, task, model.SiteTaskStatusCancelled, "", "cancelled")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.store.DeleteSite(context.Background(), site.ID); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/tasks", service.handleActiveSiteTasks)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/tasks", nil))
	service.cancelTask(run.id)
	<-run.done
	if response.Code != 200 || strings.Contains(response.Body.String(), run.id) {
		t.Fatalf("deleted target broke discovery: %d %s", response.Code, response.Body)
	}
}

func TestSiteTaskOutcomeCancellationCauseIsPreserved(t *testing.T) {
	service, site, account := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	for _, test := range []struct {
		cause        error
		status, code string
	}{
		{errSiteTaskLeaseLost, model.SiteTaskStatusFailed, "site_task_lease_lost"},
		{errSiteTaskStopped, model.SiteTaskStatusCancelled, "server_shutting_down"},
	} {
		task := newSiteTask("refresh", site.ID, account.ID, 1)
		if err := service.store.CreateSiteTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(test.cause)
		service.updateTask(ctx, task, model.SiteTaskStatusFailed, "", "request_failed")
		stored, err := service.store.GetSiteTask(context.Background(), task.ID)
		if err != nil || stored.Status != test.status || stored.Error != test.code {
			t.Fatalf("wrong cancellation cause: %+v %v", stored, err)
		}
	}
	var wg sync.WaitGroup
	service.wg = &wg
	causes := make(chan error, 1)
	service.runAsync("cause_shutdown", func(ctx context.Context) { <-ctx.Done(); causes <- context.Cause(ctx) })
	service.stopTasks()
	wg.Wait()
	if !errors.Is(<-causes, errSiteTaskStopped) {
		t.Fatal("shutdown lost its cause")
	}
}

func TestSiteTaskOutcomeRegisteredCancelWaitsForWorkerVerdict(t *testing.T) {
	service, site, account := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	var wg sync.WaitGroup
	service.wg = &wg
	release := make(chan struct{})
	defer func() { close(release); service.stopTasks(); wg.Wait() }()
	run, _, err := service.submitSiteTask(context.Background(), siteTaskSpec{key: "verdict", kind: "refresh", siteID: site.ID, accountID: account.ID}, func(ctx context.Context, task *model.SiteTask) {
		<-ctx.Done()
		<-release
		service.updateTask(ctx, task, model.SiteTaskStatusSuccess, "observed", "")
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	router := gin.New()
	router.POST("/tasks/:id/cancel", service.handleSiteTaskCancel)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/tasks/"+run.id+"/cancel", nil).WithContext(ctx))
	stored, err := service.store.GetSiteTask(context.Background(), run.id)
	if err != nil || stored.Status == model.SiteTaskStatusCancelled || strings.Contains(response.Body.String(), `"cancelled":true`) {
		t.Fatalf("unconfirmed cancellation made terminal: %+v %v %s", stored, err, response.Body)
	}
}

func TestSiteTaskOutcomeQueuedRefreshCancellationWaitsForNextInterval(t *testing.T) {
	service, site, account := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	var wg sync.WaitGroup
	service.wg = &wg
	defer func() { service.stopTasks(); wg.Wait() }()
	release, _ := service.acquireSiteGate(context.Background(), site.ID)
	defer release()
	run, _, err := service.submitAccountTask(context.Background(), account, "refresh", true, true)
	if err != nil {
		t.Fatal(err)
	}
	service.cancelTask(run.id)
	<-run.done
	before := countRows(t, service.store, "SELECT COUNT(*) FROM site_tasks")
	service.runScheduledAccountTask(context.Background(), nil, site, account, "refresh")
	if after := countRows(t, service.store, "SELECT COUNT(*) FROM site_tasks"); after != before {
		t.Fatalf("cancelled refresh requeued: %d -> %d", before, after)
	}
}
