package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func (s *siteControlService) siteTaskPayload(ctx context.Context, task *model.SiteTask) gin.H {
	var progress struct {
		Completed int                 `json:"completed"`
		Total     int                 `json:"total"`
		Items     []siteTaskBatchItem `json:"items,omitempty"`
	}
	_ = json.Unmarshal([]byte(task.ProgressJSON), &progress)
	for i := range progress.Items {
		progress.Items[i].Error = safeSiteTaskError(progress.Items[i].Error)
		if progress.Items[i].Status == "unknown" {
			progress.Items[i].Error = "observation_interrupted"
		}
	}
	status, message := task.Status, safeSiteTaskError(task.Error)
	if status == "unknown" {
		message = "observation_interrupted"
	}
	if (status == model.SiteTaskStatusQueued || status == model.SiteTaskStatusRunning) && s.registeredSiteTask(task.ID) == nil {
		status, message = "unknown", "observation_interrupted"
	}
	var budget any
	if task.SiteID > 0 && s.requestBudget != nil {
		if site, err := s.store.GetSite(ctx, task.SiteID); err == nil {
			budget = s.requestBudget.Snapshot(site.BaseURL)
		}
	}
	return gin.H{"id": task.ID, "kind": task.Kind, "site_id": task.SiteID, "site_account_id": task.SiteAccountID, "status": status, "progress": progress, "budget": budget, "result_ref": task.ResultRef, "error": message, "created_at": task.CreatedAt, "started_at": task.StartedAt, "finished_at": task.FinishedAt}
}

func safeSiteTaskError(message string) string {
	code, _, _ := strings.Cut(message, ":")
	switch code {
	case "", "cancelled", "server_shutting_down", "site_task_queue_full", "conflict", "not_found", "invalid_request", "credential_locked", "credential_required", "api_key_required", "models_required", "task_incomplete", "observation_interrupted", "site_task_lease_lost", provider.CodeUnsupported, provider.CodeBrowserRequired, provider.CodeExpired, provider.CodeUserIDRequired, provider.CodeTimeout, provider.CodeRateLimited, provider.CodeInvalidResponse, provider.CodeRequestFailed, provider.CodeRoutingKeyUnavailable:
		return code
	default:
		return provider.CodeRequestFailed
	}
}

func (s *siteControlService) registeredSiteTask(id string) *siteTaskRun {
	s.submitMu.Lock()
	defer s.submitMu.Unlock()
	for _, run := range s.activeSiteTasks {
		if run.id == id {
			return run
		}
	}
	return nil
}

// Only this process's registered work is active. Historical rows remain
// available by ID, but are never presented as executing after a restart.
func (s *siteControlService) handleActiveSiteTasks(c *gin.Context) {
	s.submitMu.Lock()
	ids := make([]string, 0, len(s.activeSiteTasks))
	for _, run := range s.activeSiteTasks {
		ids = append(ids, run.id)
	}
	s.submitMu.Unlock()
	slices.Sort(ids)
	items := make([]gin.H, 0, len(ids))
	for _, id := range ids {
		task, err := s.store.GetSiteTask(c.Request.Context(), id)
		if err != nil {
			if err.Error() == "not found" {
				continue
			}
			RespondErrorMsg(c, 500, "request_failed")
			return
		}
		if task.Status == model.SiteTaskStatusQueued || task.Status == model.SiteTaskStatusRunning {
			items = append(items, s.siteTaskPayload(c.Request.Context(), task))
		}
	}
	RespondJSON(c, 200, items)
}

var (
	errSiteTaskQueueFull = errors.New("site_task_queue_full")
	errSiteTaskStopped   = errors.New("server_shutting_down")
	errSiteTaskConflict  = errors.New("conflict")
	errSiteTaskLeaseLost = errors.New("site_task_lease_lost")
)

type siteTaskRun struct {
	id   string
	done chan struct{}
}

type siteTaskSpec struct {
	key, kind         string
	siteID, accountID int64
	total             int
	leaseKey          string
	leaseDuration     time.Duration
	retainLease       bool
	parent            context.Context // nil for HTTP submissions: request disconnect is not task cancellation.
}

// submitSiteTask serializes only submission, never execution. A task ID becomes
// observable after its lease and durable queued row exist. Active IDs are immutable.
func (s *siteControlService) submitSiteTask(ctx context.Context, spec siteTaskSpec, work func(context.Context, *model.SiteTask)) (*siteTaskRun, bool, error) {
	s.submitMu.Lock()
	defer s.submitMu.Unlock()
	s.taskMu.Lock()
	stopped := s.stopped
	s.taskMu.Unlock()
	if stopped {
		return nil, false, errSiteTaskStopped
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if previous := s.activeSiteTasks[spec.key]; previous != nil {
		return previous, true, nil
	}
	if len(s.activeSiteTasks) >= 128 {
		return nil, false, errSiteTaskQueueFull
	}
	task := newSiteTask(spec.kind, spec.siteID, spec.accountID, spec.total)
	releaseLease := func() {}
	if spec.leaseKey != "" {
		now := time.Now()
		acquired, err := s.store.AcquireSiteTaskLease(ctx, spec.leaseKey, task.ID, now.UnixMilli(), now.Add(spec.leaseDuration).UnixMilli())
		if err != nil {
			return nil, false, err
		}
		if !acquired {
			return nil, false, errSiteTaskConflict
		}
		releaseLease = func() {
			releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = s.store.ReleaseSiteTaskLease(releaseCtx, spec.leaseKey, task.ID)
		}
	}
	if err := s.store.CreateSiteTask(ctx, task); err != nil {
		releaseLease()
		return nil, false, err
	}
	run := &siteTaskRun{id: task.ID, done: make(chan struct{})}
	if s.activeSiteTasks == nil {
		s.activeSiteTasks = make(map[string]*siteTaskRun)
	}
	s.activeSiteTasks[spec.key] = run
	if !s.runAsync(task.ID, func(workerCtx context.Context) {
		defer func() {
			if !spec.retainLease {
				releaseLease()
			}
			s.submitMu.Lock()
			delete(s.activeSiteTasks, spec.key)
			close(run.done)
			s.submitMu.Unlock()
		}()
		taskCtx, cancel := context.WithCancel(workerCtx)
		defer cancel()
		if spec.parent != nil {
			stop := context.AfterFunc(spec.parent, cancel)
			defer stop()
			if spec.parent.Err() != nil {
				cancel()
			}
		}
		if spec.leaseKey != "" && !spec.retainLease {
			var stop context.CancelFunc
			taskCtx, stop = s.leaseContext(taskCtx, spec.leaseKey, task.ID)
			defer stop()
		}
		work(taskCtx, task)
		if task.Status == model.SiteTaskStatusQueued || task.Status == model.SiteTaskStatusRunning {
			s.updateTask(taskCtx, task, model.SiteTaskStatusFailed, "", "task_incomplete")
		}
	}) {
		delete(s.activeSiteTasks, spec.key)
		releaseLease()
		close(run.done)
		cancelCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_, _ = s.store.CancelSiteTask(cancelCtx, task.ID, time.Now().UnixMilli())
		return nil, false, errSiteTaskStopped
	}
	return run, false, nil
}

func respondSiteTaskSubmissionError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errSiteTaskQueueFull):
		RespondErrorMsg(c, http.StatusTooManyRequests, err.Error())
	case errors.Is(err, errSiteTaskStopped):
		RespondErrorMsg(c, http.StatusServiceUnavailable, err.Error())
	case errors.Is(err, errSiteTaskConflict):
		RespondErrorMsg(c, http.StatusConflict, err.Error())
	default:
		RespondErrorMsg(c, http.StatusInternalServerError, provider.CodeRequestFailed)
	}
}

func (s *siteControlService) submitAccountTask(ctx context.Context, account *model.SiteAccount, kind string, withModels, scheduled bool) (*siteTaskRun, bool, error) {
	key := fmt.Sprintf("account:%d:%s:%t", account.ID, kind, withModels)
	spec := siteTaskSpec{key: key, kind: kind, siteID: account.SiteID, accountID: account.ID, total: 1, leaseKey: fmt.Sprintf("site:%d:account:%d:%s", account.SiteID, account.ID, kind), leaseDuration: siteTaskLeaseDuration}
	if scheduled {
		spec.parent = ctx
	}
	return s.submitSiteTask(ctx, spec, func(taskCtx context.Context, task *model.SiteTask) {
		defer func() {
			if !scheduled || task.Status != model.SiteTaskStatusCancelled {
				return
			}
			if kind == "checkin" {
				s.recordCancelledScheduledCheckin(account)
			} else {
				s.cancelledRefreshUntil.Store(account.ID, time.Now().Add(siteRefreshInterval+siteRefreshJitter(account.ID)))
			}
		}()
		runCtx, finish, err := s.acquireManualSiteTask(taskCtx, account.SiteID)
		if err != nil {
			s.updateTask(taskCtx, task, model.SiteTaskStatusFailed, "", siteTaskError(err))
			return
		}
		defer finish()
		s.updateTask(runCtx, task, model.SiteTaskStatusRunning, "", "")
		if runCtx.Err() != nil {
			return
		}
		if kind == "checkin" {
			if scheduled {
				s.checkinWithTrigger(runCtx, task, account.ID, "schedule", "daily")
			} else {
				s.checkin(runCtx, task, account.ID)
			}
		} else if scheduled {
			s.refreshAccountScheduled(runCtx, task, account.ID, withModels)
		} else {
			s.refreshAccount(runCtx, task, account.ID, withModels)
		}
	})
}

func (s *siteControlService) submitAnnouncementTask(ctx context.Context, siteID int64, localDay string) (*siteTaskRun, bool, error) {
	spec := siteTaskSpec{key: fmt.Sprintf("announcements:%d", siteID), kind: "announcement_refresh", siteID: siteID, total: 1}
	if localDay != "" {
		spec.parent = ctx
		spec.leaseKey = fmt.Sprintf("site:%d:announcements:%s", siteID, localDay)
		spec.leaseDuration = 26 * time.Hour
		spec.retainLease = true
	}
	return s.submitSiteTask(ctx, spec, func(taskCtx context.Context, task *model.SiteTask) {
		runCtx, finish, err := s.acquireManualSiteTask(taskCtx, siteID)
		if err != nil {
			s.updateTask(taskCtx, task, model.SiteTaskStatusFailed, "", siteTaskError(err))
			return
		}
		defer finish()
		s.updateTask(runCtx, task, model.SiteTaskStatusRunning, "", "")
		if runCtx.Err() != nil {
			return
		}
		if err := s.refreshAnnouncements(runCtx, siteID); err != nil {
			if provider.ErrorCode(err) == provider.CodeUnsupported {
				s.updateTask(runCtx, task, model.SiteTaskStatusSuccess, "announcements", "该站点不提供公告接口")
				return
			}
			s.updateTask(runCtx, task, model.SiteTaskStatusFailed, "", siteTaskError(err))
			return
		}
		s.updateTask(runCtx, task, model.SiteTaskStatusSuccess, "announcements", "")
	})
}
