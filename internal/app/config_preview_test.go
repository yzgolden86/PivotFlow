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
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func TestConfigPreviewAppliesOnceAndHidesCallback(t *testing.T) {
	s := &Server{}
	var calls atomic.Int32
	p, err := s.newConfigPreview("backup", 12, []configPreviewItem{{Key: "sites[0]", Entity: "site", Action: "create"}}, func(context.Context) (*configPreviewResult, error) {
		calls.Add(1)
		return &configPreviewResult{Status: "applied"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			result, applyErr := s.applyConfigPreview(context.Background(), p.ID)
			if applyErr != nil || result.Status != "applied" {
				t.Errorf("apply=%v err=%v", result, applyErr)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("applied %d times", calls.Load())
	}
	view, err := s.getConfigPreview(p.ID)
	if err != nil || view.Status != "applied" || view.Result == nil || view.CanApply {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "callback") || strings.Contains(string(raw), "fingerprint") {
		t.Fatalf("private state exposed: %s", raw)
	}
}

func TestConfigPreviewConflictsExpiryAndFailureAreTerminal(t *testing.T) {
	s := &Server{}
	called := false
	p, err := s.newConfigPreview("backup", 1, []configPreviewItem{{Key: "sites[0]", Action: "conflict"}}, func(context.Context) (*configPreviewResult, error) { called = true; return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.applyConfigPreview(context.Background(), p.ID); err == nil || called {
		t.Fatal("conflict preview was applied")
	}
	p, err = s.newConfigPreview("backup", 1, nil, func(context.Context) (*configPreviewResult, error) {
		return nil, previewFailure("channels[2]", "preview_stale")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.applyConfigPreview(context.Background(), p.ID); err == nil || !strings.Contains(err.Error(), "channels[2]") {
		t.Fatalf("missing safe location: %v", err)
	}
	view, _ := s.getConfigPreview(p.ID)
	if view.Status != "stale" || view.CanApply {
		t.Fatalf("stale=%+v", view)
	}
	p, err = s.newConfigPreview("backup", 1, nil, func(context.Context) (*configPreviewResult, error) { return nil, errors.New("secret-provider-body") })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.applyConfigPreview(context.Background(), p.ID); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe err=%v", err)
	}
	s.configPreviews.mu.Lock()
	s.configPreviews.records[p.ID].view.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	s.configPreviews.mu.Unlock()
	if _, err := s.getConfigPreview(p.ID); err == nil {
		t.Fatal("expired preview is available")
	}
}

func TestConfigPreviewMemoryIsBounded(t *testing.T) {
	s := &Server{}
	for range 16 {
		if _, err := s.newConfigPreview("backup", 1, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.newConfigPreview("backup", 1, nil, nil); err == nil {
		t.Fatal("accepted unlimited previews")
	}
	s = &Server{}
	if _, err := s.newConfigPreview("backup", 65<<20, nil, nil); err == nil {
		t.Fatal("accepted oversized payload")
	}
}

func TestConfigPreviewDiscardReleasesCapacityButNotRunningWork(t *testing.T) {
	s := &Server{}
	entered := make(chan struct{})
	release := make(chan struct{})
	p, err := s.newConfigPreview("backup", 32<<20, nil, func(context.Context) (*configPreviewResult, error) {
		close(entered)
		<-release
		return &configPreviewResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _, _ = s.applyConfigPreview(context.Background(), p.ID) }()
	<-entered
	if err = s.discardConfigPreview(p.ID); err == nil {
		t.Fatal("discarded in-flight operation")
	}
	close(release)
	<-done
	if err = s.discardConfigPreview(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.getConfigPreview(p.ID); err == nil {
		t.Fatal("retained discarded preview")
	}
}

func TestConfigPreviewCommitUncertaintyIsNotRetryable(t *testing.T) {
	s := &Server{}
	calls := 0
	p, err := s.newConfigPreview("backup", 1, nil, func(context.Context) (*configPreviewResult, error) {
		calls++
		return nil, storage.ErrConfigurationCommitUnknown
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = s.applyConfigPreview(context.Background(), p.ID)
	view, _ := s.getConfigPreview(p.ID)
	if view.Status != "unknown" || view.Error != "commit_unknown" {
		t.Fatalf("uncertain commit mislabeled: %+v", view)
	}
	_, _ = s.applyConfigPreview(context.Background(), p.ID)
	if calls != 1 {
		t.Fatal("retried uncertain commit")
	}
}

func TestConfigPreviewServerShutdownCancelsAndWaitsForApply(t *testing.T) {
	baseCtx, baseCancel := context.WithCancel(context.Background())
	s := &Server{baseCtx: baseCtx, baseCancel: baseCancel, shutdownCh: make(chan struct{}), shutdownDone: make(chan struct{})}
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	p, err := s.newConfigPreview("backup", 1, nil, func(ctx context.Context) (*configPreviewResult, error) {
		close(entered)
		select {
		case <-ctx.Done():
			close(canceled)
		case <-release:
			return nil, errors.New("test_release")
		}
		<-release
		return nil, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	applyDone := make(chan struct{})
	go func() { defer close(applyDone); _, _ = s.applyConfigPreview(context.Background(), p.ID) }()
	<-entered
	shutdownDone := make(chan error, 1)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { shutdownDone <- s.Shutdown(shutdownCtx) }()
	select {
	case <-canceled:
	case <-shutdownDone:
		close(release)
		baseCancel()
		<-applyDone
		t.Fatal("server closed before canceling configuration application")
	case <-shutdownCtx.Done():
		close(release)
		baseCancel()
		<-applyDone
		t.Fatal("configuration application did not observe server shutdown")
	}
	select {
	case <-shutdownDone:
		close(release)
		<-applyDone
		t.Fatal("server closed while configuration application was still running")
	default:
	}
	close(release)
	<-applyDone
	if err = <-shutdownDone; err != nil {
		t.Fatal(err)
	}
	if _, err = s.newConfigPreview("backup", 1, nil, nil); err == nil {
		t.Fatal("server accepted a new preview after shutdown")
	}
	s.configPreviews.mu.Lock()
	retained := len(s.configPreviews.records)
	s.configPreviews.mu.Unlock()
	if retained != 0 {
		t.Fatal("shutdown retained preview snapshots or timers")
	}
}

func TestConfigPreviewRetainsBoundedTerminalResultAfterLateApply(t *testing.T) {
	s := &Server{}
	var previewID string
	p, err := s.newConfigPreview("backup", 1, []configPreviewItem{{Key: "channels[0]", Action: "update", Label: strings.Repeat("x", 4096)}}, func(context.Context) (*configPreviewResult, error) {
		s.configPreviews.mu.Lock()
		s.configPreviews.records[previewID].view.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
		s.configPreviews.mu.Unlock()
		return &configPreviewResult{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	previewID = p.ID
	if _, err = s.applyConfigPreview(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	view, err := s.getConfigPreview(p.ID)
	if err != nil || view.Status != "applied" || view.ExpiresAt <= time.Now().UnixMilli() {
		t.Fatalf("late application result unavailable for recovery: view=%+v err=%v", view, err)
	}
	s.configPreviews.mu.Lock()
	retainedBytes := s.configPreviews.records[p.ID].size
	s.configPreviews.mu.Unlock()
	if retainedBytes < 4096 {
		t.Fatal("terminal preview metadata no longer counts toward the memory budget")
	}
}

func TestConfigPreviewUnexpectedPanicIsTerminalAndPrivate(t *testing.T) {
	s := &Server{}
	calls := 0
	p, err := s.newConfigPreview("backup", 1, nil, func(context.Context) (*configPreviewResult, error) {
		calls++
		panic("synthetic-private-provider-error")
	})
	if err != nil {
		t.Fatal(err)
	}
	escaped := false
	func() {
		defer func() { escaped = recover() != nil }()
		_, err = s.applyConfigPreview(context.Background(), p.ID)
	}()
	if escaped {
		t.Fatal("application panic escaped and left preview applying")
	}
	view, getErr := s.getConfigPreview(p.ID)
	if getErr != nil || view.Status != "unknown" || view.CanApply || err == nil || strings.Contains(err.Error(), "private") {
		t.Fatalf("panic outcome must be terminal, uncertain and sanitized: view=%+v getErr=%v err=%v", view, getErr, err)
	}
	if _, repeatErr := s.applyConfigPreview(context.Background(), p.ID); repeatErr == nil || calls != 1 {
		t.Fatal("repeated request retried a panicked application")
	}
}

func TestConfigPreviewRepeatedHTTPApplySchedulesOnlyOneRestart(t *testing.T) {
	s := &Server{}
	p, err := s.newConfigPreview("backup", 1, nil, func(context.Context) (*configPreviewResult, error) {
		return &configPreviewResult{Backup: &backupImportResult{Restart: true}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var restarts atomic.Int32
	restarted := make(chan struct{}, 8)
	previousRestart := RestartFunc
	RestartFunc = func() { restarts.Add(1); restarted <- struct{}{} }
	defer func() { RestartFunc = previousRestart }()
	router := gin.New()
	router.POST("/admin/config-previews/:id/apply", s.HandleApplyConfigPreview)
	for range 8 {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/config-previews/"+p.ID+"/apply", strings.NewReader("{}")))
		if response.Code != http.StatusOK {
			t.Fatalf("apply returned %d", response.Code)
		}
	}
	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("configuration application did not schedule its required restart")
	}
	// Allow the handler's restart goroutines to complete before restoring the
	// hook, including any duplicates scheduled by the eight requests.
	time.Sleep(150 * time.Millisecond)
	if restarts.Load() != 1 {
		t.Fatalf("repeated confirmation scheduled %d restarts", restarts.Load())
	}
}
