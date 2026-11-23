package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

const (
	webDAVVerifyAttempts = 5
	webDAVVerifyDelay    = time.Second
	webDAVVerifyTimeout  = 10 * time.Second
)

type backupVerification struct {
	VerifiedAt   int64  `json:"verified_at"`
	Bytes        int64  `json:"bytes"`
	SHA256       string `json:"sha256"`
	Type         string `json:"type"`
	WarningCount int    `json:"warning_count"`
}

type backupExportResult struct {
	Status        string              `json:"status"`
	FileURL       string              `json:"file_url"`
	LastSyncAt    int64               `json:"last_sync_at"`
	Verification  *backupVerification `json:"verification,omitempty"`
	MetadataSaved bool                `json:"metadata_saved"`
	Warnings      []string            `json:"warnings"`
	config        *model.BackupConfig
}

type pendingBackupVerification struct {
	expected model.BackupConfig
	proof    backupVerification
}

// Holds one encrypted configuration reference and a small digest, never the
// exported JSON or decrypted credentials. All metadata writes follow the same
// lock order: observation state, then the configuration transaction.
type backupVerificationState struct {
	mu      sync.Mutex
	pending *pendingBackupVerification
}

// readWebDAVBody is shared by restore preview and upload verification. It
// returns only allowlisted error categories and never a remote response body.
func readWebDAVBody(ctx context.Context, client *http.Client, targetURL, username, authValue string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, errors.New("webdav_request_failed")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Cache-Control", "no-cache, no-store")
	request.Header.Set("Pragma", "no-cache")
	if username != "" || authValue != "" {
		request.SetBasicAuth(username, authValue)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("webdav_request_failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return nil, webDAVHTTPError(response.StatusCode, "download")
	}
	if webDAVHTMLResponse(response) {
		return nil, errors.New("webdav_html_response")
	}
	if response.ContentLength > backupMaxImportBytes {
		return nil, errors.New("backup_too_large")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, backupMaxImportBytes+1))
	if err != nil {
		return nil, errors.New("webdav_request_failed")
	}
	if len(body) > backupMaxImportBytes {
		return nil, errors.New("backup_too_large")
	}
	return body, nil
}

func verifyWebDAVUpload(ctx context.Context, client *http.Client, targetURL, username, authValue string, expected []byte) error {
	verifyCtx, cancel := context.WithTimeout(ctx, webDAVVerifyTimeout)
	defer cancel()
	expectedDigest := sha256.Sum256(expected)
	for attempt := range webDAVVerifyAttempts {
		if verifyCtx.Err() != nil {
			return webDAVVerificationContextError(verifyCtx)
		}
		body, err := readWebDAVBody(verifyCtx, client, targetURL, username, authValue)
		if verifyCtx.Err() != nil {
			return webDAVVerificationContextError(verifyCtx)
		}
		if err == nil {
			if len(body) == len(expected) && sha256.Sum256(body) == expectedDigest {
				return nil
			}
			err = errors.New("webdav_verify_mismatch")
		}
		if attempt == webDAVVerifyAttempts-1 || !retryableWebDAVReadback(err) {
			return err
		}
		timer := time.NewTimer(webDAVVerifyDelay)
		select {
		case <-verifyCtx.Done():
			timer.Stop()
			return webDAVVerificationContextError(verifyCtx)
		case <-timer.C:
		}
	}
	return errors.New("webdav_verify_mismatch")
}

func webDAVVerificationContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return errors.New("webdav_verify_cancelled")
	}
	return errors.New("webdav_verify_timeout")
}

func retryableWebDAVReadback(err error) bool {
	code := strings.SplitN(err.Error(), ":", 2)[0]
	if code == "webdav_request_failed" || code == "webdav_verify_mismatch" {
		return true
	}
	status, parseErr := strconv.Atoi(strings.TrimPrefix(code, "webdav_http_"))
	return strings.HasPrefix(code, "webdav_http_") && parseErr == nil && (status == 404 || status == 425 || status == 423 || status == 429 || status >= 500 && status <= 599)
}

func newBackupVerification(body []byte, document *backupDocument) *backupVerification {
	digest := sha256.Sum256(body)
	return &backupVerification{
		VerifiedAt: time.Now().UnixMilli(), Bytes: int64(len(body)), SHA256: hex.EncodeToString(digest[:]),
		Type: document.Type, WarningCount: len(document.Warnings),
	}
}

type backupConfigurationIdentity struct {
	enabled, automatic                        bool
	url, user, sealed, keyVersion, exportType string
	interval                                  int
}

func backupIdentity(config *model.BackupConfig) backupConfigurationIdentity {
	return backupConfigurationIdentity{
		enabled: config.Enabled, automatic: config.AutoSyncEnabled, url: config.FileURL, user: config.Username,
		sealed: config.PasswordCiphertext, keyVersion: config.PasswordKeyVersion, exportType: config.ExportType,
		interval: config.AutoSyncIntervalHours,
	}
}

func sameBackupDestination(a, b *model.BackupConfig) bool {
	return a.FileURL == b.FileURL && a.Username == b.Username && a.PasswordCiphertext == b.PasswordCiphertext && a.PasswordKeyVersion == b.PasswordKeyVersion
}

func clearBackupObservations(config *model.BackupConfig) {
	config.LastSyncAt, config.LastVerifiedAt, config.LastVerifiedBytes = 0, 0, 0
	config.LastVerifiedSHA256, config.LastVerifiedType, config.LastError = "", "", ""
	config.LastVerifiedWarningCount = 0
}

// Equal upload times cannot identify a legacy write or different content. Only
// the same verified bytes may repair incomplete scope/warning metadata.
func backupProofSuperseded(config *model.BackupConfig, proof *backupVerification) bool {
	return config.LastSyncAt > proof.VerifiedAt || config.LastVerifiedAt > proof.VerifiedAt ||
		(config.LastSyncAt == proof.VerifiedAt && config.LastVerifiedAt != proof.VerifiedAt) ||
		(config.LastVerifiedAt == proof.VerifiedAt && (config.LastVerifiedSHA256 != proof.SHA256 || config.LastVerifiedBytes != proof.Bytes))
}

func backupProofMatches(config *model.BackupConfig, proof *backupVerification) bool {
	return config.LastSyncAt == proof.VerifiedAt && config.LastVerifiedAt == proof.VerifiedAt &&
		config.LastVerifiedSHA256 == proof.SHA256 && config.LastVerifiedBytes == proof.Bytes &&
		config.LastVerifiedType == proof.Type && config.LastVerifiedWarningCount == proof.WarningCount
}

// Proof is already observed before this transaction. Storage errors therefore
// produce an independent warning instead of causing another remote write.
func (s *Server) persistBackupVerification(ctx context.Context, expected *model.BackupConfig, proof *backupVerification, clearError bool) (bool, string) {
	matched, saved, superseded := false, false, false
	err := storage.WithConfigurationTransaction(ctx, s.store, func(txCtx context.Context, store storage.Store) error {
		current, err := store.GetBackupConfig(txCtx)
		if err != nil {
			return err
		}
		if backupIdentity(current) != backupIdentity(expected) {
			return nil
		}
		matched = true
		if backupProofSuperseded(current, proof) {
			superseded = true
			return nil
		}
		if backupProofMatches(current, proof) {
			// A subsequent failure does not invalidate already persisted proof.
			// Confirming it must preserve the newer error observation.
			saved = true
			return nil
		}
		current.LastSyncAt, current.LastVerifiedAt = proof.VerifiedAt, proof.VerifiedAt
		current.LastVerifiedBytes, current.LastVerifiedSHA256 = proof.Bytes, proof.SHA256
		current.LastVerifiedType, current.LastVerifiedWarningCount = proof.Type, proof.WarningCount
		if clearError {
			current.LastError = ""
		}
		if err := store.UpsertBackupConfig(txCtx, current); err != nil {
			return err
		}
		saved = true
		return nil
	})
	if storage.IsConfigurationCacheDegraded(err) {
		return true, "backup_status_cache_degraded"
	}
	if storage.IsConfigurationCommitUnknown(err) {
		return false, "backup_status_unknown"
	}
	if err != nil {
		return false, "backup_status_not_saved"
	}
	if !matched {
		return false, "backup_config_changed"
	}
	if superseded {
		return false, "backup_status_superseded"
	}
	return saved, ""
}

func (s *Server) completeBackupVerification(ctx context.Context, expected *model.BackupConfig, proof *backupVerification) (bool, string) {
	s.backupVerificationState.mu.Lock()
	defer s.backupVerificationState.mu.Unlock()
	saved, warning := s.persistBackupVerification(ctx, expected, proof, true)
	if !saved && warning != "backup_config_changed" && warning != "backup_status_superseded" {
		s.backupVerificationState.pending = &pendingBackupVerification{expected: *expected, proof: *proof}
	} else {
		s.backupVerificationState.pending = nil
	}
	return saved, warning
}

func (s *Server) discardChangedBackupObservation(config *model.BackupConfig) {
	s.backupVerificationState.mu.Lock()
	defer s.backupVerificationState.mu.Unlock()
	if pending := s.backupVerificationState.pending; pending != nil && backupIdentity(&pending.expected) != backupIdentity(config) {
		s.backupVerificationState.pending = nil
	}
}

func (s *Server) pendingBackupObservationWarning(config *model.BackupConfig) string {
	s.backupVerificationState.mu.Lock()
	defer s.backupVerificationState.mu.Unlock()
	pending := s.backupVerificationState.pending
	if pending == nil {
		return ""
	}
	if backupIdentity(&pending.expected) != backupIdentity(config) || backupProofSuperseded(config, &pending.proof) {
		s.backupVerificationState.pending = nil
		return ""
	}
	if backupProofMatches(config, &pending.proof) {
		s.backupVerificationState.pending = nil
		return ""
	}
	return "backup_status_pending"
}

func (s *Server) backupSyncDue(ctx context.Context, config *model.BackupConfig, now time.Time) bool {
	if config == nil {
		return false
	}
	lastSyncAt := config.LastSyncAt
	s.backupVerificationState.mu.Lock()
	if pending := s.backupVerificationState.pending; pending != nil {
		if backupIdentity(&pending.expected) != backupIdentity(config) {
			s.backupVerificationState.pending = nil
		} else {
			lastSyncAt = max(lastSyncAt, pending.proof.VerifiedAt)
			stateCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			saved, warning := s.persistBackupVerification(stateCtx, &pending.expected, &pending.proof, false)
			cancel()
			if saved || warning == "backup_config_changed" || warning == "backup_status_superseded" {
				s.backupVerificationState.pending = nil
			}
		}
	}
	s.backupVerificationState.mu.Unlock()
	interval := time.Duration(config.AutoSyncIntervalHours) * time.Hour
	if interval <= 0 {
		interval = backupDefaultIntervalHour * time.Hour
	}
	return config.Enabled && config.AutoSyncEnabled && (lastSyncAt <= 0 || now.Sub(time.UnixMilli(lastSyncAt)) >= interval)
}

func safeBackupExportError(err error) error {
	if err == nil {
		return nil
	}
	code := strings.SplitN(err.Error(), ":", 2)[0]
	switch code {
	case "backup_in_progress", "invalid_backup_type", "credential_locked", "backup_too_large", "webdav_not_configured", "webdav_request_failed", "webdav_verify_mismatch", "webdav_verify_timeout", "webdav_verify_cancelled", "webdav_html_response":
		return errors.New(code)
	}
	if strings.HasPrefix(code, "webdav_http_") {
		status, parseErr := strconv.Atoi(strings.TrimPrefix(code, "webdav_http_"))
		if parseErr == nil && status >= 100 && status <= 599 {
			return errors.New(code)
		}
	}
	return errors.New("backup_export_failed")
}
