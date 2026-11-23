package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

// These tests exercise the real upload handler against a local HTTP server.
// Removing the readback, accepting different bytes, or publishing an old
// target's result after an edit must each fail independently.
func configureVerificationWebDAV(t *testing.T, srv *Server, target string) {
	t.Helper()
	sealed, err := srv.siteControl.cipher.Seal(backupPasswordSecret{Value: "synthetic-dav-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpsertBackupConfig(context.Background(), &model.BackupConfig{
		Enabled: true, FileURL: target, Username: "synthetic-dav-user",
		PasswordCiphertext: sealed, PasswordKeyVersion: srv.siteControl.cipher.Version(),
		ExportType: "settings", AutoSyncIntervalHours: 24, LastSyncAt: 1700000000000,
	}); err != nil {
		t.Fatal(err)
	}
}

type verificationResponse struct {
	Status        string `json:"status"`
	MetadataSaved *bool  `json:"metadata_saved"`
	Verification  *struct {
		VerifiedAt   int64  `json:"verified_at"`
		Bytes        int64  `json:"bytes"`
		SHA256       string `json:"sha256"`
		Type         string `json:"type"`
		WarningCount int    `json:"warning_count"`
	} `json:"verification"`
	Warnings []string `json:"warnings"`
}

func uploadVerificationResponse(t *testing.T, srv *Server) APIResponse[verificationResponse] {
	t.Helper()
	c, recorder := newTestContext(t, newJSONRequest(t, http.MethodPost, "/admin/backup/webdav/export", map[string]string{"type": "settings"}))
	srv.HandleBackupWebDAVExport(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("upload operation status = %d, expected JSON operation response", recorder.Code)
	}
	return mustParseAPIResponse[verificationResponse](t, recorder.Body.Bytes())
}

func TestWebDAVUploadRequiresMatchingReadbackEvidence(t *testing.T) {
	var uploaded []byte
	var puts, gets atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, authValue, ok := r.BasicAuth()
		if !ok || user != "synthetic-dav-user" || authValue != "synthetic-dav-secret" {
			t.Error("upload/readback did not use the configured credentials")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPut {
			puts.Add(1)
			uploaded, _ = io.ReadAll(r.Body)
			// A 2xx status page on PUT is valid; GET must contain the file.
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("<html>created</html>"))
			return
		}
		gets.Add(1)
		if r.Header.Get("Cache-Control") != "no-cache, no-store" {
			t.Error("readback allows a cached response")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(uploaded)
	}))
	defer remote.Close()
	srv := backupPreviewServer(t)
	configureVerificationWebDAV(t, srv, remote.URL+"/backup.json")
	response := uploadVerificationResponse(t, srv)
	if !response.Success || puts.Load() != 1 || gets.Load() != 1 {
		t.Fatalf("success=%v PUT=%d GET=%d; expected one upload followed by one readback", response.Success, puts.Load(), gets.Load())
	}
	proof := response.Data.Verification
	if proof == nil || proof.VerifiedAt <= 1700000000000 || proof.Bytes != int64(len(uploaded)) || proof.Type != "settings" {
		t.Fatal("successful response is missing matching, scoped verification evidence")
	}
	digest := sha256.Sum256(uploaded)
	if proof.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("readback evidence does not identify the uploaded file")
	}
	if response.Data.MetadataSaved == nil || !*response.Data.MetadataSaved {
		t.Fatal("verified outcome was not recorded")
	}
}

func TestWebDAVUploadRejectsUnreadableOrChangedContent(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body, wantCode string
		status                            int
	}{
		{name: "changed JSON", status: http.StatusOK, contentType: "application/json", body: `{ "version": "1.0", "type": "settings" }`, wantCode: "webdav_verify_mismatch"},
		{name: "HTML login", status: http.StatusOK, contentType: "text/html", body: "<html>synthetic-secret-must-not-leak</html>", wantCode: "webdav_html_response"},
		{name: "read permission", status: http.StatusForbidden, body: "synthetic-secret-must-not-leak", wantCode: "webdav_http_403"},
		{name: "authentication", status: http.StatusUnauthorized, body: "synthetic-secret-must-not-leak", wantCode: "webdav_http_401"},
		{name: "missing file", status: http.StatusNotFound, body: "synthetic-secret-must-not-leak", wantCode: "webdav_http_404"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var puts, gets atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					puts.Add(1)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				gets.Add(1)
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer remote.Close()
			srv := backupPreviewServer(t)
			configureVerificationWebDAV(t, srv, remote.URL+"/backup.json")
			response := uploadVerificationResponse(t, srv)
			if response.Success || !strings.HasPrefix(response.Error, tc.wantCode) {
				t.Fatalf("success=%v error=%q; expected %s", response.Success, response.Error, tc.wantCode)
			}
			wantReads := int32(1)
			if tc.wantCode == "webdav_verify_mismatch" || tc.status == http.StatusNotFound {
				wantReads = 5
			}
			if puts.Load() != 1 || gets.Load() != wantReads || strings.Contains(response.Error, "synthetic-secret-must-not-leak") {
				t.Fatal("failed verification repeated a write, skipped reading, or exposed remote contents")
			}
			stored, err := srv.store.GetBackupConfig(context.Background())
			if err != nil || stored.LastSyncAt != 1700000000000 || !strings.HasPrefix(stored.LastError, tc.wantCode) {
				t.Fatalf("verification failure altered the last success or was not recorded: err=%v", err)
			}
		})
	}
}

func TestWebDAVUploadDoesNotOverwriteSettingsChangedDuringUpload(t *testing.T) {
	srv := backupPreviewServer(t)
	var uploaded []byte
	const replacementURL = "https://new-synthetic-target.invalid/backup.json"
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			uploaded, _ = io.ReadAll(r.Body)
			c, recorder := newTestContext(t, newJSONRequest(t, http.MethodPut, "/admin/backup/webdav", map[string]any{
				"enabled": true, "file_url": replacementURL, "username": "another-synthetic-user",
				"export_type": "settings", "auto_sync_interval_hours": 12,
			}))
			srv.HandleBackupWebDAV(c)
			if recorder.Code != http.StatusOK {
				t.Error("concurrent settings update failed")
			}
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(uploaded)
	}))
	defer remote.Close()
	configureVerificationWebDAV(t, srv, remote.URL+"/backup.json")
	response := uploadVerificationResponse(t, srv)
	stored, err := srv.store.GetBackupConfig(context.Background())
	if err != nil || stored.FileURL != replacementURL || stored.Username != "another-synthetic-user" || stored.AutoSyncIntervalHours != 12 {
		t.Fatal("completion of the old upload overwrote the newly saved target")
	}
	if stored.LastSyncAt != 0 || response.Data.MetadataSaved == nil || *response.Data.MetadataSaved || len(response.Data.Warnings) == 0 {
		t.Fatal("old target evidence was attributed to the replacement target or the scope warning is missing")
	}
}

func TestWebDAVUploadRejectsExportTooLargeToRestore(t *testing.T) {
	var puts atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		puts.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer remote.Close()
	srv := backupPreviewServer(t)
	configureVerificationWebDAV(t, srv, remote.URL+"/backup.json")
	settings, err := srv.store.ListAllSettings(context.Background())
	if err != nil || len(settings) == 0 {
		t.Fatal("fixture has no system settings")
	}
	if err := srv.store.UpdateSetting(context.Background(), settings[0].Key, strings.Repeat("x", backupMaxImportBytes)); err != nil {
		t.Fatal(err)
	}
	response := uploadVerificationResponse(t, srv)
	if response.Success || !strings.HasPrefix(response.Error, "backup_too_large") || puts.Load() != 0 {
		t.Fatal("export exceeded the restore size limit but still reached the remote target")
	}
}

func TestWebDAVUploadRetriesOnlyReadsUntilFileBecomesVisible(t *testing.T) {
	var uploaded []byte
	var puts, gets atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts.Add(1)
			uploaded, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			return
		}
		switch gets.Add(1) {
		case 1:
			w.WriteHeader(http.StatusTooEarly)
		case 2:
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(uploaded)
		}
	}))
	defer remote.Close()
	srv := backupPreviewServer(t)
	configureVerificationWebDAV(t, srv, remote.URL+"/backup.json")
	response := uploadVerificationResponse(t, srv)
	if !response.Success || puts.Load() != 1 || gets.Load() != 3 || response.Data.Verification == nil {
		t.Fatal("temporary readback failures did not converge through reads alone")
	}
}

func TestWebDAVUploadCancellationStopsReadbackWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var puts, gets atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts.Add(1)
			w.WriteHeader(http.StatusCreated)
			return
		}
		gets.Add(1)
		w.WriteHeader(http.StatusTooEarly)
		cancel()
	}))
	defer remote.Close()
	srv := backupPreviewServer(t)
	configureVerificationWebDAV(t, srv, remote.URL+"/backup.json")
	request := newJSONRequest(t, http.MethodPost, "/admin/backup/webdav/export", map[string]string{"type": "settings"}).WithContext(ctx)
	c, recorder := newTestContext(t, request)
	started := time.Now()
	srv.HandleBackupWebDAVExport(c)
	response := mustParseAPIResponse[verificationResponse](t, recorder.Body.Bytes())
	if response.Success || !strings.HasPrefix(response.Error, "webdav_verify_cancelled") || puts.Load() != 1 || gets.Load() != 1 || time.Since(started) > time.Second {
		t.Fatal("cancelled readback published success, retried a write, or kept waiting")
	}
	if srv.backupExportRunning.Load() {
		t.Fatal("cancelled verification left the export lock occupied")
	}
}

func TestWebDAVUploadRejectsOversizedReadbackBeforeReadingBody(t *testing.T) {
	var gets atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusCreated)
			return
		}
		gets.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "33554433")
		_, _ = w.Write([]byte("short body must not be read"))
	}))
	defer remote.Close()
	srv := backupPreviewServer(t)
	configureVerificationWebDAV(t, srv, remote.URL+"/backup.json")
	response := uploadVerificationResponse(t, srv)
	if response.Success || !strings.HasPrefix(response.Error, "backup_too_large") || gets.Load() != 1 {
		t.Fatal("oversized readback was accepted or retried")
	}
}

func TestWebDAVUploadClosesOwnedIdleConnection(t *testing.T) {
	var uploaded []byte
	closed := make(chan struct{})
	var closeOnce sync.Once
	remote := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			uploaded, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(uploaded)
	}))
	remote.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closeOnce.Do(func() { close(closed) })
		}
	}
	remote.Start()
	defer remote.Close()
	srv := backupPreviewServer(t)
	configureVerificationWebDAV(t, srv, remote.URL+"/backup.json")
	response := uploadVerificationResponse(t, srv)
	if !response.Success {
		t.Fatal("synthetic upload failed")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("completed WebDAV operation retained its owned idle connection")
	}
}

func TestWebDAVVerifiedUploadWithFailedMetadataSaveRetriesOnlyMetadata(t *testing.T) {
	var uploaded []byte
	var puts atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts.Add(1)
			uploaded, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(uploaded)
	}))
	defer remote.Close()
	srv := backupPreviewServer(t)
	configureVerificationWebDAV(t, srv, remote.URL+"/backup.json")
	config, _ := srv.store.GetBackupConfig(context.Background())
	config.AutoSyncEnabled = true
	if err := srv.store.UpsertBackupConfig(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	sqlDB := srv.store.(*sqlstore.SQLStore)
	if _, err := sqlDB.ExecContext(context.Background(), `CREATE TRIGGER reject_backup_observation BEFORE UPDATE OF last_sync_at ON backup_settings WHEN NEW.last_sync_at > 1700000000000 BEGIN SELECT RAISE(ABORT,'synthetic-private-dsn-must-not-leak'); END`); err != nil {
		t.Fatal(err)
	}
	response := uploadVerificationResponse(t, srv)
	if !response.Success || response.Data.Verification == nil || response.Data.MetadataSaved == nil || *response.Data.MetadataSaved || len(response.Data.Warnings) == 0 {
		t.Fatal("verified file and failed local observation were not distinguished")
	}
	for _, warning := range response.Data.Warnings {
		if strings.Contains(warning, "synthetic-private-dsn") {
			t.Fatal("metadata warning exposed the private storage failure")
		}
	}
	stored, _ := srv.store.GetBackupConfig(context.Background())
	now := time.UnixMilli(response.Data.Verification.VerifiedAt).Add(10 * time.Minute)
	if srv.backupSyncDue(context.Background(), stored, now) || puts.Load() != 1 {
		t.Fatal("unsaved verification timestamp caused an immediate scheduled re-upload")
	}
	if _, err := sqlDB.ExecContext(context.Background(), "DROP TRIGGER reject_backup_observation"); err != nil {
		t.Fatal(err)
	}
	if srv.backupSyncDue(context.Background(), stored, now) || puts.Load() != 1 {
		t.Fatal("metadata recovery retried the upload")
	}
	stored, err := srv.store.GetBackupConfig(context.Background())
	if err != nil || stored.LastSyncAt != response.Data.Verification.VerifiedAt || stored.LastVerifiedSHA256 != response.Data.Verification.SHA256 {
		t.Fatal("metadata-only retry did not save the original observed evidence")
	}
}

func TestWebDAVVerifiedUploadCacheFailureIsCommittedWithWarning(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	primary := newBackupRestoreDatabase(t, filepath.Join(directory, "primary.db"), "0123456789abcdef0123456789abcdef", "hybrid-verification")
	cache := newBackupRestoreDatabase(t, filepath.Join(directory, "cache.db"), "0123456789abcdef0123456789abcdef", "hybrid-verification")
	var uploaded []byte
	var puts atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts.Add(1)
			uploaded, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(uploaded)
	}))
	defer remote.Close()
	configureVerificationWebDAV(t, primary.server, remote.URL+"/backup.json")
	config, _ := primary.store.GetBackupConfig(ctx)
	if err := cache.store.UpsertBackupConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.store.ExecContext(ctx, `CREATE TRIGGER reject_cache_observation BEFORE UPDATE OF last_sync_at ON backup_settings BEGIN SELECT RAISE(ABORT,'synthetic-private-cache-failure'); END`); err != nil {
		t.Fatal(err)
	}
	hybrid := storage.NewHybridStore(cache.store, primary.store)
	primary.server.store, primary.server.siteControl.store = hybrid, hybrid
	response := uploadVerificationResponse(t, primary.server)
	if !response.Success || response.Data.Verification == nil || response.Data.MetadataSaved == nil || !*response.Data.MetadataSaved || puts.Load() != 1 {
		t.Fatal("post-commit cache failure was described as an uncommitted or repeated upload")
	}
	warning := false
	for _, code := range response.Data.Warnings {
		warning = warning || code == "backup_status_cache_degraded"
		if strings.Contains(code, "synthetic-private-cache-failure") {
			t.Fatal("cache warning exposed the storage failure")
		}
	}
	stored, _ := primary.store.GetBackupConfig(ctx)
	cached, _ := cache.store.GetBackupConfig(ctx)
	if !warning || stored.LastVerifiedSHA256 != response.Data.Verification.SHA256 || cached.LastSyncAt != 1700000000000 {
		t.Fatal("committed primary evidence and stale cache were not kept distinct")
	}
}

func TestWebDAVChangedSettingsDiscardPendingObservationEvenAfterTargetReused(t *testing.T) {
	ctx := context.Background()
	var uploaded []byte
	var puts atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts.Add(1)
			uploaded, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(uploaded)
	}))
	defer remote.Close()
	srv := backupPreviewServer(t)
	originalURL := remote.URL + "/backup.json"
	configureVerificationWebDAV(t, srv, originalURL)
	config, _ := srv.store.GetBackupConfig(ctx)
	config.AutoSyncEnabled = true
	if err := srv.store.UpsertBackupConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	sqlDB := srv.store.(*sqlstore.SQLStore)
	if _, err := sqlDB.ExecContext(ctx, `CREATE TRIGGER reject_pending_observation BEFORE UPDATE OF last_sync_at ON backup_settings WHEN NEW.last_sync_at > 1700000000000 BEGIN SELECT RAISE(ABORT,'synthetic-pending-failure'); END`); err != nil {
		t.Fatal(err)
	}
	response := uploadVerificationResponse(t, srv)
	if !response.Success || response.Data.MetadataSaved == nil || *response.Data.MetadataSaved || response.Data.Verification == nil {
		t.Fatal("fixture did not retain a verified but unsaved observation")
	}
	for _, targetURL := range []string{"https://synthetic-replacement.invalid/backup.json", originalURL} {
		c, recorder := newTestContext(t, newJSONRequest(t, http.MethodPut, "/admin/backup/webdav", map[string]any{
			"enabled": true, "file_url": targetURL, "username": "synthetic-dav-user", "export_type": "settings", "auto_sync_enabled": true, "auto_sync_interval_hours": 24,
		}))
		srv.HandleBackupWebDAV(c)
		if recorder.Code != http.StatusOK {
			t.Fatal("target edit failed")
		}
	}
	if _, err := sqlDB.ExecContext(ctx, "DROP TRIGGER reject_pending_observation"); err != nil {
		t.Fatal(err)
	}
	stored, _ := srv.store.GetBackupConfig(ctx)
	due := srv.backupSyncDue(ctx, stored, time.UnixMilli(response.Data.Verification.VerifiedAt).Add(10*time.Minute))
	stored, _ = srv.store.GetBackupConfig(ctx)
	if !due || stored.LastSyncAt != 0 || stored.LastVerifiedSHA256 != "" || puts.Load() != 1 {
		t.Fatal("reusing a previously edited target resurrected its discarded pending observation")
	}
}

func TestWebDAVUploadRejectsChunkedOversizedReadback(t *testing.T) {
	var puts, gets atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts.Add(1)
			w.WriteHeader(http.StatusCreated)
			return
		}
		gets.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.(http.Flusher).Flush() // Force chunked transfer without Content-Length.
		chunk := []byte(strings.Repeat("x", 64<<10))
		for range 513 {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer remote.Close()
	srv := backupPreviewServer(t)
	configureVerificationWebDAV(t, srv, remote.URL+"/backup.json")
	response := uploadVerificationResponse(t, srv)
	if response.Success || response.Error != "backup_too_large" || puts.Load() != 1 || gets.Load() != 1 {
		t.Fatal("chunked response bypassed the restore size limit or repeated a write")
	}
}

func TestWebDAVUploadReadbackHonorsEarlierContextDeadline(t *testing.T) {
	var puts, gets atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			puts.Add(1)
			w.WriteHeader(http.StatusCreated)
			return
		}
		gets.Add(1)
		<-r.Context().Done()
	}))
	defer remote.Close()
	srv := backupPreviewServer(t)
	configureVerificationWebDAV(t, srv, remote.URL+"/backup.json")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	request := newJSONRequest(t, http.MethodPost, "/admin/backup/webdav/export", map[string]string{"type": "settings"}).WithContext(ctx)
	c, recorder := newTestContext(t, request)
	started := time.Now()
	srv.HandleBackupWebDAVExport(c)
	response := mustParseAPIResponse[verificationResponse](t, recorder.Body.Bytes())
	if response.Success || response.Error != "webdav_verify_timeout" || puts.Load() != 1 || gets.Load() != 1 || time.Since(started) > 2*time.Second {
		t.Fatal("readback ignored the caller deadline or published success")
	}
}
