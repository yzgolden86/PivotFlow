package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"

	"github.com/gin-gonic/gin"
)

func (s *siteControlService) runAsync(taskID string, fn func(context.Context)) bool {
	s.taskMu.Lock()
	if s.stopped {
		s.taskMu.Unlock()
		return false
	}
	ctx, cancel := context.WithTimeout(s.baseCtx, 2*time.Minute)
	s.tasks[taskID] = cancel
	if s.wg != nil {
		s.wg.Add(1)
	}
	s.taskMu.Unlock()

	go func() {
		defer func() {
			cancel()
			s.taskMu.Lock()
			delete(s.tasks, taskID)
			s.taskMu.Unlock()
			if s.wg != nil {
				s.wg.Done()
			}
		}()
		fn(ctx)
	}()
	return true
}

func (s *siteControlService) cancelTask(taskID string) {
	s.taskMu.Lock()
	cancel := s.tasks[taskID]
	s.taskMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *siteControlService) stopTasks() {
	s.taskMu.Lock()
	s.stopped = true
	cancels := make([]context.CancelFunc, 0, len(s.tasks))
	for _, cancel := range s.tasks {
		cancels = append(cancels, cancel)
	}
	s.taskMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *siteControlService) handleSiteProbe(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, 400, "invalid_request")
		return
	}
	site, err := s.store.GetSite(c.Request.Context(), id)
	if err != nil {
		RespondErrorMsg(c, 404, "not_found")
		return
	}
	var result provider.DetectionResult
	if strings.EqualFold(strings.TrimSpace(site.Platform), model.SitePlatformUnknown) || strings.TrimSpace(site.Platform) == "" {
		result, err = s.registry.Detect(c.Request.Context(), site.BaseURL)
	} else {
		var adapter provider.SiteAdapter
		adapter, err = s.adapter(site)
		if err == nil {
			result, err = adapter.Detect(c.Request.Context(), site.BaseURL)
		}
	}
	if err != nil {
		site.LastProbeStatus = "failed"
		site.LastError = provider.ErrorCode(err)
		_, _ = s.store.UpdateSite(c.Request.Context(), id, site)
		RespondErrorMsg(c, 502, provider.ErrorCode(err))
		return
	}
	if result.Matched {
		site.LastProbeStatus = "success"
		site.LastError = ""
		site.Platform = result.ProviderID
	} else {
		site.LastProbeStatus = "unsupported"
	}
	_, _ = s.store.UpdateSite(c.Request.Context(), id, site)
	RespondJSON(c, 200, result)
}

func (s *siteControlService) handleSiteAccounts(c *gin.Context) {
	siteID, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, 400, "invalid_request")
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
		items, err := s.store.ListSiteAccounts(c.Request.Context(), siteID, false)
		if err != nil {
			RespondError(c, 500, err)
			return
		}
		for _, item := range items {
			s.decorateAccountCredentialMetadata(item)
		}
		RespondJSON(c, 200, items)
	case http.MethodPost:
		var req accountCreateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			RespondErrorMsg(c, 400, "invalid_request")
			return
		}
		item, err := s.createAccount(c.Request.Context(), siteID, req)
		if err != nil {
			code := 400
			if strings.Contains(err.Error(), "credential_locked") {
				code = 423
			}
			RespondErrorMsg(c, code, err.Error())
			return
		}
		s.decorateAccountCredentialMetadata(item)
		RespondJSON(c, 201, item)
	}
}

type siteInventoryResponse struct {
	Sites          []*model.Site                   `json:"sites"`
	Accounts       []*model.SiteAccount            `json:"accounts"`
	LatestCheckins map[int64]*model.CheckinAttempt `json:"latest_checkins"`
}

func (s *siteControlService) handleSiteInventory(c *gin.Context) {
	ctx := c.Request.Context()
	sites, err := s.store.ListSites(ctx, model.SiteListFilter{})
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	accounts, err := s.store.ListSiteAccounts(ctx, 0, false)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	accountIDs := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		s.decorateAccountCredentialMetadata(account)
		accountIDs = append(accountIDs, account.ID)
	}
	latestAttempts, err := s.store.ListCheckinAttemptsBatch(ctx, accountIDs, 1)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	latest := make(map[int64]*model.CheckinAttempt, len(latestAttempts))
	for _, attempt := range latestAttempts {
		latest[attempt.SiteAccountID] = attempt
	}
	RespondJSON(c, http.StatusOK, siteInventoryResponse{Sites: sites, Accounts: accounts, LatestCheckins: latest})
}

func (s *siteControlService) handleSiteAccountByID(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, 400, "invalid_request")
		return
	}
	account, err := s.store.GetSiteAccount(c.Request.Context(), id)
	if err != nil {
		RespondErrorMsg(c, 404, "not_found")
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
		s.decorateAccountCredentialMetadata(account)
		RespondJSON(c, 200, account)
	case http.MethodPatch:
		var req accountPatchRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			RespondErrorMsg(c, 400, "invalid_request")
			return
		}
		if req.Label != nil {
			account.Label = strings.TrimSpace(*req.Label)
		}
		if req.Enabled != nil {
			account.Enabled = *req.Enabled
		}
		if req.AutoCheckin != nil {
			account.AutoCheckin = *req.AutoCheckin
		}
		if req.AutoRefresh != nil {
			account.AutoRefresh = *req.AutoRefresh
		}
		if req.Timezone != nil {
			account.Timezone = strings.TrimSpace(*req.Timezone)
		}
		if req.CredentialType != nil || req.Credential != nil {
			if req.Credential == nil {
				RespondErrorMsg(c, http.StatusBadRequest, "credential_required")
				return
			}
			credentialType := account.CredentialType
			if req.CredentialType != nil {
				credentialType = strings.TrimSpace(*req.CredentialType)
			}
			if credentialType == model.CredentialTypeAccessToken {
				s.preserveCredentialRefresh(account, req.Credential)
			}
			site, siteErr := s.store.GetSite(c.Request.Context(), account.SiteID)
			if siteErr != nil {
				RespondErrorMsg(c, http.StatusNotFound, "not_found")
				return
			}
			preparedType, prepared, prepareErr := s.prepareAccountCredential(c.Request.Context(), site, credentialType, *req.Credential)
			if prepareErr != nil {
				respondSiteProviderError(c, http.StatusBadRequest, prepareErr)
				return
			}
			sealed, sealErr := s.cipher.Seal(prepared)
			if sealErr != nil {
				RespondErrorMsg(c, http.StatusLocked, "credential_locked")
				return
			}
			if credentialErr := s.store.UpdateSiteAccountCredential(c.Request.Context(), id, preparedType, sealed, s.cipher.Version()); credentialErr != nil {
				RespondError(c, http.StatusBadRequest, credentialErr)
				return
			}
			account.CredentialType = preparedType
			account.CredentialCiphertext = sealed
			account.CredentialKeyVersion = s.cipher.Version()
			account.Status = model.SiteAccountStatusUnknown
			account.LastError = ""
			account.ConsecutiveFailures = 0
			account.LastRefreshStatus = "unknown"
			if preparedType == model.CredentialTypeAPIKey {
				account.AutoCheckin = false
				account.AutoRefresh = false
				account.LastCheckinStatus = provider.CheckinUnsupported
			} else if account.LastCheckinStatus == provider.CheckinUnsupported {
				account.LastCheckinStatus = "unknown"
			}
		}
		site, siteErr := s.store.GetSite(c.Request.Context(), account.SiteID)
		if siteErr == nil {
			if adapter, adapterErr := s.adapter(site); adapterErr == nil && !adapter.Capabilities().ServerCheckin {
				account.AutoCheckin = false
				account.LastCheckinStatus = provider.CheckinUnsupported
			}
		}
		out, err := s.store.UpdateSiteAccount(c.Request.Context(), id, account)
		if err != nil {
			RespondError(c, 400, err)
			return
		}
		// Account updates can disable projected channels in the store. Evict the
		// router snapshots so the data plane observes that change immediately.
		s.projectionChanged()
		s.decorateAccountCredentialMetadata(out)
		RespondJSON(c, 200, out)
	case http.MethodDelete:
		if err := s.store.DeleteSiteAccount(c.Request.Context(), id); err != nil {
			RespondError(c, 500, err)
			return
		}
		s.projectionChanged()
		RespondJSON(c, 200, gin.H{"id": id, "deleted": true})
	}
}

func (s *siteControlService) handleSiteAccountCredentialVerify(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid_request")
		return
	}
	account, err := s.store.GetSiteAccount(c.Request.Context(), id)
	if err != nil {
		RespondErrorMsg(c, http.StatusNotFound, "not_found")
		return
	}
	var req accountCredentialVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid_request")
		return
	}
	site, err := s.store.GetSite(c.Request.Context(), account.SiteID)
	if err != nil {
		RespondErrorMsg(c, http.StatusNotFound, "not_found")
		return
	}
	credentialType := strings.TrimSpace(req.CredentialType)
	if credentialType == "" {
		credentialType = account.CredentialType
	}
	if credentialType == model.CredentialTypeAccessToken {
		s.preserveCredentialRefresh(account, &req.Credential)
	}
	preparedType, prepared, err := s.prepareAccountCredential(c.Request.Context(), site, credentialType, req.Credential)
	if err != nil {
		respondSiteProviderError(c, http.StatusBadRequest, err)
		return
	}
	adapter, err := s.adapter(site)
	if err != nil {
		respondSiteProviderError(c, http.StatusBadRequest, err)
		return
	}
	result := gin.H{
		"credential_type":       preparedType,
		"user_id":               prepared.UserID,
		"routing_key_available": strings.TrimSpace(prepared.APIKey) != "",
		"model_count":           0,
	}
	verifyCtx, cancel := context.WithTimeout(c.Request.Context(), 35*time.Second)
	defer cancel()
	if preparedType == model.CredentialTypeAPIKey {
		models, listErr := adapter.ListModels(verifyCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: prepared})
		if listErr != nil {
			respondSiteProviderError(c, http.StatusBadRequest, listErr)
			return
		}
		result["model_count"] = len(models)
	} else {
		snapshot, refreshErr := adapter.RefreshAccount(verifyCtx, provider.RefreshAccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: prepared})
		if refreshErr != nil {
			respondSiteProviderError(c, http.StatusBadRequest, refreshErr)
			return
		}
		result["username"] = snapshot.Username
		result["balance"] = snapshot.Balance
		result["currency"] = snapshot.Currency
	}
	RespondJSON(c, http.StatusOK, result)
}

func respondSiteProviderError(c *gin.Context, status int, err error) {
	code := provider.ErrorCode(err)
	message := provider.ErrorMessage(err)
	RespondErrorWithData(c, status, code, gin.H{
		"code":                 code,
		"message":              message,
		"upstream_status_code": provider.ErrorStatusCode(err),
	})
}

func (s *siteControlService) enqueueAccountTask(c *gin.Context, kind string, withModels bool) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, 400, "invalid_request")
		return
	}
	account, err := s.store.GetSiteAccount(c.Request.Context(), id)
	if err != nil {
		RespondErrorMsg(c, 404, "not_found")
		return
	}
	task, err := s.createTask(c.Request.Context(), kind, account.SiteID, id, 1)
	if err != nil {
		RespondError(c, 500, err)
		return
	}
	if !s.runAsync(task.ID, func(ctx context.Context) {
		leaseKey := fmt.Sprintf("site:%d:account:%d:%s", account.SiteID, id, kind)
		now := time.Now().UnixMilli()
		acquired, leaseErr := s.store.AcquireSiteTaskLease(ctx, leaseKey, task.ID, now, now+siteTaskLeaseDuration.Milliseconds())
		if leaseErr != nil || !acquired {
			s.updateTask(ctx, task, model.SiteTaskStatusCancelled, "", "conflict")
			return
		}
		defer func() { _ = s.store.ReleaseSiteTaskLease(context.Background(), leaseKey, task.ID) }()
		taskCtx, stopLease := s.leaseContext(ctx, leaseKey, task.ID)
		defer stopLease()
		s.updateTask(taskCtx, task, model.SiteTaskStatusRunning, "", "")
		if kind == "checkin" {
			s.checkin(taskCtx, task, id)
		} else {
			s.refreshAccount(taskCtx, task, id, withModels)
		}
	}) {
		_, _ = s.store.CancelSiteTask(context.Background(), task.ID, time.Now().UnixMilli())
		RespondErrorMsg(c, http.StatusServiceUnavailable, "server_shutting_down")
		return
	}
	RespondJSON(c, http.StatusAccepted, gin.H{"task_id": task.ID})
}

func (s *siteControlService) handleAccountRefresh(c *gin.Context) {
	s.enqueueAccountTask(c, "refresh", false)
}
func (s *siteControlService) handleAccountModelsRefresh(c *gin.Context) {
	s.enqueueAccountTask(c, "model_refresh", true)
}
func (s *siteControlService) handleAccountCheckin(c *gin.Context) {
	s.enqueueAccountTask(c, "checkin", false)
}

func (s *siteControlService) handleAccountCheckinRuns(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, 400, "invalid_request")
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	items, err := s.store.ListCheckinAttempts(c.Request.Context(), id, limit)
	if err != nil {
		RespondError(c, 500, err)
		return
	}
	RespondJSONWithCount(c, 200, items, len(items))
}

func (s *siteControlService) handleCheckinAttempts(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	accounts, err := s.store.ListSiteAccounts(c.Request.Context(), 0, false)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	accountIDs := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		accountIDs = append(accountIDs, account.ID)
	}
	items, err := s.store.ListCheckinAttemptsBatch(c.Request.Context(), accountIDs, limit)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	RespondJSONWithCount(c, http.StatusOK, items, len(items))
}

type announcementsRefreshRequest struct {
	SiteID int64 `json:"site_id"`
}

func (s *siteControlService) handleAnnouncements(c *gin.Context) {
	siteID, _ := strconv.ParseInt(c.Query("site_id"), 10, 64)
	var unread *bool
	if raw := c.Query("unread"); raw != "" {
		v := raw == "true"
		unread = &v
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	items, count, err := s.store.ListSiteAnnouncements(c.Request.Context(), model.SiteAnnouncementFilter{SiteID: siteID, Unread: unread, Limit: limit, Offset: max(0, offset)})
	if err != nil {
		RespondError(c, 500, err)
		return
	}
	RespondPaginated(c, 200, items, count)
}
func (s *siteControlService) handleAnnouncementsRefresh(c *gin.Context) {
	var req announcementsRefreshRequest
	_ = c.ShouldBindJSON(&req)
	task, err := s.createTask(c.Request.Context(), "announcement_refresh", req.SiteID, 0, 1)
	if err != nil {
		RespondError(c, 500, err)
		return
	}
	if !s.runAsync(task.ID, func(ctx context.Context) {
		var refreshErr error
		s.updateTask(ctx, task, model.SiteTaskStatusRunning, "", "")
		if req.SiteID > 0 {
			refreshErr = s.refreshAnnouncements(ctx, req.SiteID)
		} else {
			sites, e := s.store.ListSites(ctx, model.SiteListFilter{})
			if e != nil {
				refreshErr = e
			} else {
				var (
					failures  []string
					refreshed int
					mu        sync.Mutex
					wg        sync.WaitGroup
				)
				semaphore := make(chan struct{}, 4)
				for _, site := range sites {
					if !site.Enabled {
						continue
					}
					site := site
					wg.Add(1)
					go func() {
						defer wg.Done()
						select {
						case semaphore <- struct{}{}:
						case <-ctx.Done():
							return
						}
						defer func() { <-semaphore }()
						if e := s.refreshAnnouncements(ctx, site.ID); e != nil {
							if provider.ErrorCode(e) == provider.CodeUnsupported {
								return
							}
							mu.Lock()
							failures = append(failures, fmt.Sprintf("%s: %s", site.Name, siteTaskError(e)))
							mu.Unlock()
							return
						}
						mu.Lock()
						refreshed++
						mu.Unlock()
					}()
				}
				wg.Wait()
				if len(failures) > 1 {
					// Concurrent refreshes finish in arbitrary order; keep task messages stable.
					slices.Sort(failures)
				}
				if len(failures) > 0 {
					refreshErr = fmt.Errorf("%s", strings.Join(failures, "; "))
					if refreshed > 0 {
						s.updateTask(ctx, task, model.SiteTaskStatusPartial, "announcements", refreshErr.Error())
						return
					}
				}
			}
		}
		if refreshErr != nil {
			s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", siteTaskError(refreshErr))
			return
		}
		s.updateTask(ctx, task, model.SiteTaskStatusSuccess, "announcements", "")
	}) {
		_, _ = s.store.CancelSiteTask(context.Background(), task.ID, time.Now().UnixMilli())
		RespondErrorMsg(c, http.StatusServiceUnavailable, "server_shutting_down")
		return
	}
	RespondJSON(c, 202, gin.H{"task_id": task.ID})
}
func (s *siteControlService) handleAnnouncementRead(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, 400, "invalid_request")
		return
	}
	if err := s.store.MarkSiteAnnouncementRead(c.Request.Context(), id); err != nil {
		RespondError(c, 500, err)
		return
	}
	RespondJSON(c, 200, gin.H{"id": id, "read": true})
}
func (s *siteControlService) handleAnnouncementsReadAll(c *gin.Context) {
	var req announcementsRefreshRequest
	_ = c.ShouldBindJSON(&req)
	if err := s.store.MarkAllSiteAnnouncementsRead(c.Request.Context(), req.SiteID); err != nil {
		RespondError(c, 500, err)
		return
	}
	RespondJSON(c, 200, gin.H{"read": true})
}

func (s *siteControlService) handleSiteModels(c *gin.Context) {
	siteID, _ := strconv.ParseInt(c.Query("site_id"), 10, 64)
	accountID, _ := strconv.ParseInt(c.Query("account_id"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "500"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	items, err := s.store.ListSiteAccountModels(c.Request.Context(), model.SiteModelFilter{SiteID: siteID, SiteAccountID: accountID, IncludeDisabled: c.Query("include_disabled") == "true", Limit: limit, Offset: max(0, offset)})
	if err != nil {
		RespondError(c, 500, err)
		return
	}
	RespondJSONWithCount(c, 200, items, len(items))
}

func (s *siteControlService) handleSiteChannelBindings(c *gin.Context) {
	items, err := s.store.ListSiteChannelBindings(c.Request.Context())
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	accountID, _ := strconv.ParseInt(c.Query("account_id"), 10, 64)
	if accountID > 0 {
		filtered := make([]*model.SiteChannelBinding, 0, 1)
		for _, item := range items {
			if item.SiteAccountID == accountID {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	RespondJSONWithCount(c, http.StatusOK, items, len(items))
}

func (s *siteControlService) handleSiteTask(c *gin.Context) {
	task, err := s.store.GetSiteTask(c.Request.Context(), c.Param("id"))
	if err != nil {
		RespondErrorMsg(c, 404, "not_found")
		return
	}
	var progress any = gin.H{}
	_ = json.Unmarshal([]byte(task.ProgressJSON), &progress)
	RespondJSON(c, 200, gin.H{"id": task.ID, "kind": task.Kind, "status": task.Status, "progress": progress, "result_ref": task.ResultRef, "error": task.Error, "created_at": task.CreatedAt, "started_at": task.StartedAt, "finished_at": task.FinishedAt})
}
func (s *siteControlService) handleSiteTaskCancel(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		RespondErrorMsg(c, 400, "invalid_request")
		return
	}
	task, err := s.store.GetSiteTask(c.Request.Context(), id)
	if err != nil {
		RespondErrorMsg(c, 404, "not_found")
		return
	}
	cancelled, err := s.store.CancelSiteTask(c.Request.Context(), id, time.Now().UnixMilli())
	if err != nil {
		RespondError(c, 500, err)
		return
	}
	if cancelled {
		s.cancelTask(id)
	}
	status := task.Status
	if cancelled {
		status = model.SiteTaskStatusCancelled
	}
	RespondJSON(c, 200, gin.H{"id": id, "cancelled": cancelled, "status": status})
}

type projectAccountRequest struct {
	ProjectionKey string `json:"projection_key"`
	APIKey        string `json:"api_key"`
	Name          string `json:"name"`
	Force         bool   `json:"force"`
}

func (s *siteControlService) handleAccountProjection(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, 400, "invalid_request")
		return
	}
	var req projectAccountRequest
	_ = c.ShouldBindJSON(&req)
	account, err := s.store.GetSiteAccount(c.Request.Context(), id)
	if err != nil {
		RespondErrorMsg(c, 404, "not_found")
		return
	}
	site, err := s.store.GetSite(c.Request.Context(), account.SiteID)
	if err != nil {
		RespondErrorMsg(c, 404, "not_found")
		return
	}
	adapter, err := s.adapter(site)
	if err != nil {
		RespondErrorMsg(c, 400, provider.ErrorCode(err))
		return
	}
	creds, err := s.operationCredentials(c.Request.Context(), account, site, adapter)
	if err != nil {
		RespondErrorMsg(c, http.StatusLocked, "credential_locked")
		return
	}
	if key := strings.TrimSpace(req.APIKey); key != "" {
		creds.APIKey = key
	}
	result, err := s.projectAccount(c.Request.Context(), account, site, adapter, creds, req.ProjectionKey, req.Name, req.Force)
	if err != nil {
		message := provider.ErrorCode(err)
		if strings.Contains(err.Error(), "models_required") {
			message = "models_required"
		}
		RespondErrorMsg(c, http.StatusBadRequest, message)
		return
	}
	RespondJSON(c, 200, result)
}
