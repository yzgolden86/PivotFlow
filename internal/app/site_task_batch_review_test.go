package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

type siteTaskBatchReviewStore struct {
	storage.Store
	createChild func(context.Context, *model.SiteTask) (error, bool)
	readTask    func(context.Context, string) (*model.SiteTask, error, bool)
}

func (s *siteTaskBatchReviewStore) CreateSiteTask(ctx context.Context, task *model.SiteTask) error {
	if s.createChild != nil && task.Kind == "announcement_refresh" && task.SiteID > 0 {
		if err, handled := s.createChild(ctx, task); handled {
			return err
		}
	}
	return s.Store.CreateSiteTask(ctx, task)
}

func (s *siteTaskBatchReviewStore) GetSiteTask(ctx context.Context, id string) (*model.SiteTask, error) {
	if s.readTask != nil {
		if task, err, handled := s.readTask(ctx, id); handled {
			return task, err
		}
	}
	return s.Store.GetSiteTask(ctx, id)
}

type siteTaskBatchReviewProgress struct {
	Completed int                 `json:"completed"`
	Total     int                 `json:"total"`
	Items     []siteTaskBatchItem `json:"items"`
}

func waitSiteTaskBatchReviewProgress(t *testing.T, store storage.Store, taskID string, ready func(siteTaskBatchReviewProgress) bool) siteTaskBatchReviewProgress {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		task, err := store.GetSiteTask(context.Background(), taskID)
		if err == nil {
			var progress siteTaskBatchReviewProgress
			if json.Unmarshal([]byte(task.ProgressJSON), &progress) == nil && ready(progress) {
				return progress
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("batch progress did not become ready: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func createSiteTaskBatchReviewSite(t *testing.T, store storage.Store, name string) *model.Site {
	t.Helper()
	site, err := store.CreateSite(context.Background(), &model.Site{
		Name: name, Platform: model.SitePlatformNewAPIFamily,
		BaseURL: fmt.Sprintf("https://%s.example", name), Enabled: true,
		Timezone: "Asia/Shanghai", TagsJSON: "[]", LastProbeStatus: "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	return site
}

func TestSiteTaskBatchReviewObservationFailureIsUnknownAndBounded(t *testing.T) {
	adapter := &queueAnnouncementAdapter{started: make(chan struct{}, 4), release: make(chan struct{})}
	service, site, _ := newSiteRefreshTestService(t, adapter)
	var wg sync.WaitGroup
	service.wg = &wg
	defer func() { service.stopTasks(); wg.Wait() }()

	var batchMu sync.Mutex
	batchID := ""
	deadlines := make(chan time.Duration, 1)
	service.store = &siteTaskBatchReviewStore{
		Store: service.store,
		readTask: func(ctx context.Context, id string) (*model.SiteTask, error, bool) {
			batchMu.Lock()
			current := batchID
			batchMu.Unlock()
			if current == "" || id == current {
				return nil, nil, false
			}
			remaining := time.Duration(0)
			if deadline, ok := ctx.Deadline(); ok {
				remaining = time.Until(deadline)
			}
			select {
			case deadlines <- remaining:
			default:
			}
			return nil, errors.New("injected child terminal read failure"), true
		},
	}

	batch, _, err := service.submitAnnouncementBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	batchMu.Lock()
	batchID = batch.id
	batchMu.Unlock()

	select {
	case <-adapter.started:
	case <-time.After(3 * time.Second):
		t.Fatal("announcement child did not start")
	}
	close(adapter.release)
	<-batch.done

	stored, err := service.store.GetSiteTask(context.Background(), batch.id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "unknown" {
		t.Fatalf("observation failure parent status=%q, want unknown: %+v", stored.Status, stored)
	}
	progress := waitSiteTaskBatchReviewProgress(t, service.store, batch.id, func(progress siteTaskBatchReviewProgress) bool {
		return progress.Completed == 1 && progress.Total == 1 && len(progress.Items) == 1
	})
	item := progress.Items[0]
	if item.SiteID != site.ID || item.TaskID == "" || item.Status != "unknown" || item.Error == "" {
		t.Fatalf("observation failure item=%+v", item)
	}
	select {
	case remaining := <-deadlines:
		if remaining <= 0 || remaining > 3100*time.Millisecond {
			t.Fatalf("terminal read deadline remaining=%v, want bounded near 3s", remaining)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal read did not observe a bounded context")
	}
}

func TestSiteTaskBatchReviewAllChildrenCancelledReturnsCancelled(t *testing.T) {
	adapter := &queueAnnouncementAdapter{started: make(chan struct{}, 4), release: make(chan struct{})}
	service, _, _ := newSiteRefreshTestService(t, adapter)
	createSiteTaskBatchReviewSite(t, service.store, "batch-review-second")
	var wg sync.WaitGroup
	service.wg = &wg
	defer func() { service.stopTasks(); wg.Wait() }()

	batch, _, err := service.submitAnnouncementBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	progress := waitSiteTaskBatchReviewProgress(t, service.store, batch.id, func(progress siteTaskBatchReviewProgress) bool {
		if progress.Total != 2 || len(progress.Items) != 2 {
			return false
		}
		for _, item := range progress.Items {
			if item.TaskID == "" {
				return false
			}
		}
		return true
	})
	for _, item := range progress.Items {
		service.cancelTask(item.TaskID)
	}
	<-batch.done

	stored, err := service.store.GetSiteTask(context.Background(), batch.id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.SiteTaskStatusCancelled {
		t.Fatalf("all-cancelled parent status=%q, want cancelled: %+v", stored.Status, stored)
	}
	progress = waitSiteTaskBatchReviewProgress(t, service.store, batch.id, func(progress siteTaskBatchReviewProgress) bool {
		return progress.Completed == 2 && progress.Total == 2
	})
	for _, item := range progress.Items {
		if item.Status != model.SiteTaskStatusCancelled {
			t.Fatalf("all-cancelled item=%+v", item)
		}
	}
}

func TestSiteTaskBatchReviewCancelledSubmissionMarksItemCancelled(t *testing.T) {
	service, site, _ := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	var wg sync.WaitGroup
	service.wg = &wg
	defer func() { service.stopTasks(); wg.Wait() }()

	createStarted := make(chan struct{}, 1)
	releaseCreate := make(chan struct{})
	service.store = &siteTaskBatchReviewStore{
		Store: service.store,
		createChild: func(ctx context.Context, _ *model.SiteTask) (error, bool) {
			select {
			case createStarted <- struct{}{}:
			default:
			}
			<-releaseCreate
			return ctx.Err(), true
		},
	}

	batch, _, err := service.submitAnnouncementBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-createStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("child submission did not reach CreateSiteTask")
	}
	service.cancelTask(batch.id)
	close(releaseCreate)
	<-batch.done

	progress := waitSiteTaskBatchReviewProgress(t, service.store, batch.id, func(progress siteTaskBatchReviewProgress) bool {
		return progress.Completed == 1 && progress.Total == 1 && len(progress.Items) == 1
	})
	item := progress.Items[0]
	if item.SiteID != site.ID || item.TaskID != "" || item.Status != model.SiteTaskStatusCancelled {
		t.Fatalf("cancelled submission item=%+v", item)
	}
	stored, err := service.store.GetSiteTask(context.Background(), batch.id)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.SiteTaskStatusCancelled {
		t.Fatalf("cancelled submission parent=%+v", stored)
	}
}

func TestSiteTaskBatchReviewResidualActiveChildIsUnknown(t *testing.T) {
	for _, residual := range []string{model.SiteTaskStatusQueued, model.SiteTaskStatusRunning} {
		t.Run(residual, func(t *testing.T) {
			adapter := &queueAnnouncementAdapter{started: make(chan struct{}, 4), release: make(chan struct{})}
			service, site, _ := newSiteRefreshTestService(t, adapter)
			var wg sync.WaitGroup
			service.wg = &wg
			defer func() { service.stopTasks(); wg.Wait() }()

			var batchMu sync.Mutex
			batchID := ""
			service.store = &siteTaskBatchReviewStore{
				Store: service.store,
				readTask: func(_ context.Context, id string) (*model.SiteTask, error, bool) {
					batchMu.Lock()
					current := batchID
					batchMu.Unlock()
					if current == "" || id == current {
						return nil, nil, false
					}
					return &model.SiteTask{ID: id, Kind: "announcement_refresh", Status: residual}, nil, true
				},
			}

			batch, _, err := service.submitAnnouncementBatch(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			batchMu.Lock()
			batchID = batch.id
			batchMu.Unlock()

			select {
			case <-adapter.started:
			case <-time.After(3 * time.Second):
				t.Fatal("announcement child did not start")
			}
			close(adapter.release)
			<-batch.done

			stored, err := service.store.GetSiteTask(context.Background(), batch.id)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != "unknown" {
				t.Fatalf("residual %s parent status=%q, want unknown: %+v", residual, stored.Status, stored)
			}
			progress := waitSiteTaskBatchReviewProgress(t, service.store, batch.id, func(progress siteTaskBatchReviewProgress) bool {
				return progress.Completed == 1 && progress.Total == 1 && len(progress.Items) == 1
			})
			item := progress.Items[0]
			if item.SiteID != site.ID || item.TaskID == "" || item.Status != "unknown" || !strings.Contains(item.Error, "terminal") {
				t.Fatalf("residual %s item=%+v", residual, item)
			}
		})
	}
}
