package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

const (
	siteTaskBatchReadTimeout = 3 * time.Second
	siteTaskBatchUnknown     = "unknown"
)

type siteTaskBatchItem struct {
	SiteID int64  `json:"site_id"`
	TaskID string `json:"task_id,omitempty"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func siteTaskBatchOutcome(items []siteTaskBatchItem) (string, string) {
	if len(items) == 0 {
		return model.SiteTaskStatusSuccess, ""
	}
	var success, partial, failed, cancelled, unknown int
	for _, item := range items {
		switch item.Status {
		case model.SiteTaskStatusSuccess:
			success++
		case model.SiteTaskStatusPartial:
			partial++
		case model.SiteTaskStatusFailed:
			failed++
		case model.SiteTaskStatusCancelled:
			cancelled++
		default:
			unknown++
		}
	}
	switch {
	case unknown > 0:
		return siteTaskBatchUnknown, "部分站点公告刷新结果未知，请查看逐项结果"
	case cancelled == len(items):
		return model.SiteTaskStatusCancelled, "cancelled"
	case success == len(items):
		return model.SiteTaskStatusSuccess, ""
	case success > 0 || partial > 0:
		return model.SiteTaskStatusPartial, "部分站点公告刷新失败或取消，请查看逐项结果"
	case failed > 0:
		return model.SiteTaskStatusFailed, "部分站点公告刷新失败，请查看逐项结果"
	default:
		return siteTaskBatchUnknown, "站点公告刷新结果无法确认，请查看逐项结果"
	}
}

func (s *siteControlService) submitAnnouncementBatch(ctx context.Context) (*siteTaskRun, bool, error) {
	return s.submitSiteTask(ctx, siteTaskSpec{key: "announcements:all", kind: "announcement_refresh"}, func(taskCtx context.Context, task *model.SiteTask) {
		sites, err := s.store.ListSites(taskCtx, model.SiteListFilter{})
		if err != nil {
			s.updateTask(taskCtx, task, model.SiteTaskStatusFailed, "", siteTaskError(err))
			return
		}
		items := make([]siteTaskBatchItem, 0, len(sites))
		for _, site := range sites {
			if site != nil && site.Enabled {
				items = append(items, siteTaskBatchItem{SiteID: site.ID, Status: model.SiteTaskStatusQueued})
			}
		}
		var mu sync.Mutex
		completed := 0
		publish := func() {
			data, _ := json.Marshal(struct {
				Completed int                 `json:"completed"`
				Total     int                 `json:"total"`
				Items     []siteTaskBatchItem `json:"items"`
			}{completed, len(items), items})
			task.ProgressJSON = string(data)
			s.updateTask(taskCtx, task, model.SiteTaskStatusRunning, "", "")
		}
		publish()
		jobs := make(chan int, len(items))
		for i := range items {
			jobs <- i
		}
		close(jobs)
		var wg sync.WaitGroup
		for worker := 0; worker < min(4, len(items)); worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for index := range jobs {
					if taskCtx.Err() != nil {
						mu.Lock()
						items[index].Status = model.SiteTaskStatusCancelled
						completed++
						publish()
						mu.Unlock()
						continue
					}
					run, _, submitErr := s.submitAnnouncementTask(taskCtx, items[index].SiteID, "")
					if submitErr != nil {
						mu.Lock()
						if errors.Is(submitErr, context.Canceled) || errors.Is(taskCtx.Err(), context.Canceled) {
							items[index].Status = model.SiteTaskStatusCancelled
							items[index].Error = "cancelled"
						} else {
							items[index].Status = model.SiteTaskStatusFailed
							items[index].Error = siteTaskError(submitErr)
						}
						completed++
						publish()
						mu.Unlock()
						continue
					}
					mu.Lock()
					items[index].TaskID = run.id
					items[index].Status = model.SiteTaskStatusRunning
					publish()
					mu.Unlock()
					stop := context.AfterFunc(taskCtx, func() { s.cancelTask(run.id) })
					if taskCtx.Err() != nil {
						s.cancelTask(run.id)
					}
					<-run.done
					stop()
					readCtx, cancel := context.WithTimeout(context.Background(), siteTaskBatchReadTimeout)
					child, readErr := s.store.GetSiteTask(readCtx, run.id)
					cancel()
					mu.Lock()
					switch {
					case readErr != nil:
						items[index].Status = siteTaskBatchUnknown
						items[index].Error = siteTaskError(readErr)
					case child == nil:
						items[index].Status = siteTaskBatchUnknown
						items[index].Error = "missing_site_task"
					case child.Status == model.SiteTaskStatusSuccess ||
						child.Status == model.SiteTaskStatusPartial ||
						child.Status == model.SiteTaskStatusFailed ||
						child.Status == model.SiteTaskStatusCancelled ||
						child.Status == siteTaskBatchUnknown:
						items[index].Status = child.Status
						items[index].Error = child.Error
					default:
						items[index].Status = siteTaskBatchUnknown
						items[index].Error = "site_task_not_terminal: " + child.Status
					}
					completed++
					publish()
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		status, message := siteTaskBatchOutcome(items)
		if status == siteTaskBatchUnknown {
			task.FinishedAt = time.Now().UnixMilli()
		}
		s.updateTask(taskCtx, task, status, "announcements", message)
	})
}
