package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

type configPreviewItem struct {
	Key             string   `json:"key"`
	Entity          string   `json:"entity"`
	Label           string   `json:"label"`
	Action          string   `json:"action"`
	Fields          []string `json:"fields"`
	PreservedFields []string `json:"preserved_fields"`
	Message         string   `json:"message"`
}

type configPreviewResult struct {
	PreviewID     string                    `json:"preview_id"`
	Status        string                    `json:"status"`
	Items         []configPreviewItem       `json:"items"`
	Backup        *backupImportResult       `json:"backup,omitempty"`
	BrowserImport *browserImportApplyResult `json:"browser_import,omitempty"`
	Warning       string                    `json:"warning,omitempty"`
	// Set only on the first apply caller's returned copy, never in the cache
	// DTO. Repeated POSTs and GET recovery cannot schedule another restart.
	restartAfterResponse bool
}

type configPreview struct {
	ID            string                       `json:"id"`
	Kind          string                       `json:"kind"`
	ExpiresAt     int64                        `json:"expires_at"`
	CanApply      bool                         `json:"can_apply"`
	Items         []configPreviewItem          `json:"items"`
	Summary       map[string]int               `json:"summary"`
	Status        string                       `json:"status"`
	Error         string                       `json:"error,omitempty"`
	Result        *configPreviewResult         `json:"result,omitempty"`
	BrowserImport *browserImportPreviewContext `json:"browser_import,omitempty"`
}

type configPreviewRecord struct {
	view   configPreview
	size   int
	apply  func(context.Context) (*configPreviewResult, error)
	err    error
	done   chan struct{}
	expiry *time.Timer
}

type configPreviewCache struct {
	mu      sync.Mutex
	records map[string]*configPreviewRecord
	closed  bool
}

type configPreviewError struct{ key, code string }

func (e *configPreviewError) Error() string {
	if e.key != "" {
		return e.code + ": " + e.key
	}
	return e.code
}
func previewFailure(key, code string) error { return &configPreviewError{key: key, code: code} }

func (s *Server) newConfigPreview(kind string, size int, items []configPreviewItem, apply func(context.Context) (*configPreviewResult, error)) (*configPreview, error) {
	return s.newConfigPreviewRecord(kind, size, items, nil, apply)
}

func (s *Server) newConfigPreviewWithBrowserImport(size int, items []configPreviewItem, metadata *browserImportPreviewContext, apply func(context.Context) (*configPreviewResult, error)) (*configPreview, error) {
	return s.newConfigPreviewRecord("browser_import", size, items, metadata, apply)
}

func (s *Server) newConfigPreviewRecord(kind string, size int, items []configPreviewItem, metadata *browserImportPreviewContext, apply func(context.Context) (*configPreviewResult, error)) (*configPreview, error) {
	raw, err := json.Marshal(items)
	if err != nil {
		return nil, previewFailure("", "invalid_preview")
	}
	if size < 0 || size > (64<<20)-len(raw) {
		return nil, previewFailure("", "preview_capacity")
	}
	size += len(raw)
	s.configPreviews.mu.Lock()
	defer s.configPreviews.mu.Unlock()
	if s.configPreviews.closed || s.isShuttingDown.Load() {
		return nil, previewFailure("", "server_shutting_down")
	}
	s.pruneConfigPreviewsLocked()
	if metadata != nil {
		if err = s.browserImportPendingLocked(metadata.BaseURL, ""); err != nil {
			return nil, err
		}
	}
	total := size
	for _, item := range s.configPreviews.records {
		total += item.size
	}
	if len(s.configPreviews.records) >= 16 || total > 64<<20 {
		return nil, previewFailure("", "preview_capacity")
	}
	var random [24]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, previewFailure("", "request_failed")
	}
	if items == nil {
		items = []configPreviewItem{}
	}
	summary := map[string]int{"create": 0, "update": 0, "unchanged": 0, "preserve": 0, "conflict": 0, "deactivate": 0}
	for index, item := range items {
		if item.Fields == nil {
			items[index].Fields = []string{}
		}
		if item.PreservedFields == nil {
			items[index].PreservedFields = []string{}
		}
		summary[item.Action]++
	}
	view := configPreview{ID: hex.EncodeToString(random[:]), Kind: kind, ExpiresAt: time.Now().Add(10 * time.Minute).UnixMilli(), Items: items, Summary: summary, Status: "ready", CanApply: summary["conflict"] == 0, BrowserImport: metadata}
	if s.configPreviews.records == nil {
		s.configPreviews.records = make(map[string]*configPreviewRecord)
	}
	record := &configPreviewRecord{view: view, size: size, apply: apply}
	s.configPreviews.records[view.ID] = record
	// Bound secret lifetime even when the administrator leaves the page and no
	// subsequent request arrives to prune this cache.
	s.resetConfigPreviewExpiryLocked(record)
	return cloneConfigPreview(view), nil
}

func (s *Server) resetConfigPreviewExpiryLocked(record *configPreviewRecord) {
	if record.expiry != nil {
		record.expiry.Stop()
	}
	record.expiry = time.AfterFunc(time.Until(time.UnixMilli(record.view.ExpiresAt)), func() {
		s.configPreviews.mu.Lock()
		defer s.configPreviews.mu.Unlock()
		s.pruneConfigPreviewsLocked()
	})
}

func (s *Server) stopConfigPreviews() {
	s.configPreviews.mu.Lock()
	defer s.configPreviews.mu.Unlock()
	// The same lock guards admission and WaitGroup.Add, so Shutdown cannot
	// begin waiting while an application is still registering itself.
	s.configPreviews.closed = true
	for id, record := range s.configPreviews.records {
		if record.expiry != nil {
			record.expiry.Stop()
		}
		if record.view.Status != "applying" {
			delete(s.configPreviews.records, id)
		}
	}
}

func (s *Server) pruneConfigPreviewsLocked() {
	for id, item := range s.configPreviews.records {
		if item.view.ExpiresAt <= time.Now().UnixMilli() && item.view.Status != "applying" {
			if item.expiry != nil {
				item.expiry.Stop()
			}
			delete(s.configPreviews.records, id)
		}
	}
}

func (s *Server) discardConfigPreview(id string) error {
	return s.discardConfigPreviewWithAcknowledgement(id, false)
}

func (s *Server) discardConfigPreviewWithAcknowledgement(id string, acknowledged bool) error {
	s.configPreviews.mu.Lock()
	defer s.configPreviews.mu.Unlock()
	if record := s.configPreviews.records[id]; record != nil {
		if record.view.Status == "applying" {
			return previewFailure("", "preview_apply_in_progress")
		}
		if record.view.BrowserImport != nil && record.view.Status == "unknown" && !acknowledged {
			return previewFailure("", "browser_import_acknowledgement_required")
		}
		if record.expiry != nil {
			record.expiry.Stop()
		}
		delete(s.configPreviews.records, id)
	}
	return nil
}

func (s *Server) HandleDiscardConfigPreview(c *gin.Context) {
	if err := s.discardConfigPreviewWithAcknowledgement(c.Param("id"), c.Query("acknowledged") == "true"); err != nil {
		RespondErrorMsg(c, http.StatusConflict, err.Error())
		return
	}
	RespondJSON(c, http.StatusOK, gin.H{"deleted": true})
}

func cloneConfigPreview(view configPreview) *configPreview {
	// Only the public DTO is copied: callback captures and secret fingerprints
	// cannot accidentally be included in any HTTP response.
	raw, _ := json.Marshal(view)
	var copyView configPreview
	_ = json.Unmarshal(raw, &copyView)
	return &copyView
}

func (s *Server) getConfigPreview(id string) (*configPreview, error) {
	s.configPreviews.mu.Lock()
	defer s.configPreviews.mu.Unlock()
	s.pruneConfigPreviewsLocked()
	r := s.configPreviews.records[id]
	if r == nil {
		return nil, previewFailure("", "preview_expired")
	}
	return cloneConfigPreview(r.view), nil
}

func (s *Server) applyConfigPreview(ctx context.Context, id string) (*configPreviewResult, error) {
	s.configPreviews.mu.Lock()
	s.pruneConfigPreviewsLocked()
	r := s.configPreviews.records[id]
	if r == nil {
		s.configPreviews.mu.Unlock()
		return nil, previewFailure("", "preview_expired")
	}
	if r.view.Status == "applying" {
		done := r.done
		s.configPreviews.mu.Unlock()
		select {
		case <-done:
			return s.applyConfigPreview(ctx, id)
		case <-ctx.Done():
			return nil, previewFailure("", "observation_interrupted")
		}
	}
	if r.view.Status == "applied" {
		result := cloneConfigPreview(r.view).Result
		s.configPreviews.mu.Unlock()
		return result, nil
	}
	if r.err != nil {
		s.configPreviews.mu.Unlock()
		return nil, r.err
	}
	if s.configPreviews.closed || s.isShuttingDown.Load() {
		s.configPreviews.mu.Unlock()
		return nil, previewFailure("", "server_shutting_down")
	}
	if r.view.Status != "ready" || !r.view.CanApply || r.apply == nil {
		s.configPreviews.mu.Unlock()
		return nil, previewFailure("", "preview_conflict")
	}
	if r.view.BrowserImport != nil {
		if err := s.browserImportPendingLocked(r.view.BrowserImport.BaseURL, id); err != nil {
			s.configPreviews.mu.Unlock()
			return nil, err
		}
	}
	r.view.Status, r.view.CanApply, r.done = "applying", false, make(chan struct{})
	apply := r.apply
	s.wg.Add(1)
	s.configPreviews.mu.Unlock()
	defer s.wg.Done()
	// A disconnected browser does not imply a rolled back transaction. Keep a
	// bounded operation alive so GET can report its definitive outcome.
	applyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	if s.baseCtx != nil {
		stopServerCancel := context.AfterFunc(s.baseCtx, cancel)
		defer stopServerCancel()
	}
	result, err := invokeConfigPreviewApply(applyCtx, apply)
	s.configPreviews.mu.Lock()
	defer s.configPreviews.mu.Unlock()
	r.apply = nil
	if err != nil {
		if storage.IsConfigurationCommitUnknown(err) {
			err = previewFailure("", "commit_unknown")
		}
		var safe *configPreviewError
		if !errors.As(err, &safe) {
			err = previewFailure("", "apply_failed")
		}
		r.view.Status, r.view.Error = "failed", err.Error()
		if safe != nil && safe.code == "preview_stale" {
			r.view.Status = "stale"
		}
		if safe != nil && safe.code == "commit_unknown" {
			r.view.Status = "unknown"
		}
		r.err = err
	} else {
		if result == nil {
			result = &configPreviewResult{}
		}
		result.PreviewID, result.Status = id, "applied"
		if result.Items == nil {
			result.Items = r.view.Items
		}
		r.view.Status, r.view.Result = "applied", result
	}
	// Retain the outcome long enough to recover a lost response even if the
	// administrator confirmed just before the input snapshot expired.
	r.view.ExpiresAt = time.Now().Add(10 * time.Minute).UnixMilli()
	publicMetadata, _ := json.Marshal(r.view)
	r.size = len(publicMetadata)
	if s.configPreviews.closed {
		delete(s.configPreviews.records, id)
	} else {
		s.resetConfigPreviewExpiryLocked(r)
	}
	close(r.done)
	publicResult := cloneConfigPreview(r.view).Result
	if publicResult != nil && publicResult.Backup != nil {
		publicResult.restartAfterResponse = publicResult.Backup.Restart
	}
	return publicResult, err
}

func (s *Server) browserImportPending(baseURL, excludeID string) error {
	s.configPreviews.mu.Lock()
	defer s.configPreviews.mu.Unlock()
	if s.configPreviews.closed || s.isShuttingDown.Load() {
		return previewFailure("", "server_shutting_down")
	}
	s.pruneConfigPreviewsLocked()
	return s.browserImportPendingLocked(baseURL, excludeID)
}

func (s *Server) browserImportPendingLocked(baseURL, excludeID string) error {
	for id, record := range s.configPreviews.records {
		if id != excludeID && record.view.BrowserImport != nil && record.view.BrowserImport.BaseURL == baseURL && (record.view.Status == "applying" || record.view.Status == "unknown") {
			return previewFailure(id, "browser_import_pending")
		}
	}
	return nil
}

func invokeConfigPreviewApply(ctx context.Context, apply func(context.Context) (*configPreviewResult, error)) (result *configPreviewResult, err error) {
	defer func() {
		if recover() != nil {
			// A panic may occur after the database committed. Record uncertainty
			// without exposing its value or automatically rerunning the callback.
			result, err = nil, storage.ErrConfigurationCommitUnknown
		}
	}()
	return apply(ctx)
}

func (s *Server) HandleConfigPreview(c *gin.Context) {
	view, err := s.getConfigPreview(c.Param("id"))
	if err != nil {
		RespondErrorMsg(c, http.StatusGone, err.Error())
		return
	}
	RespondJSON(c, http.StatusOK, view)
}

func (s *Server) HandleApplyConfigPreview(c *gin.Context) {
	result, err := s.applyConfigPreview(c.Request.Context(), c.Param("id"))
	if err != nil {
		status := http.StatusConflict
		if strings.HasPrefix(err.Error(), "preview_expired") {
			status = http.StatusGone
		}
		RespondErrorMsg(c, status, err.Error())
		return
	}
	RespondJSON(c, http.StatusOK, result)
	if result.restartAfterResponse {
		go triggerRestart()
	}
}
