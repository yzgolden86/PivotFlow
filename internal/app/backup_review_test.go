package app

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func backupReviewFixture(t *testing.T) (*Server, *model.BackupConfig, backupVerification) {
	t.Helper()
	srv := backupPreviewServer(t)
	configureVerificationWebDAV(t, srv, "https://synthetic-review.invalid/backup.json")
	config, err := srv.store.GetBackupConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	config.AutoSyncEnabled = true
	if err := srv.store.UpsertBackupConfig(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	return srv, config, backupVerification{VerifiedAt: 1700000001000, Bytes: 2400, SHA256: strings.Repeat("a", 64), Type: "settings", WarningCount: 2}
}

func backupReviewStoreProof(config *model.BackupConfig, proof backupVerification) {
	config.LastSyncAt, config.LastVerifiedAt = proof.VerifiedAt, proof.VerifiedAt
	config.LastVerifiedBytes, config.LastVerifiedSHA256 = proof.Bytes, proof.SHA256
	config.LastVerifiedType, config.LastVerifiedWarningCount = proof.Type, proof.WarningCount
}

func backupReviewPending(srv *Server, config *model.BackupConfig, proof backupVerification) {
	srv.backupVerificationState.mu.Lock()
	defer srv.backupVerificationState.mu.Unlock()
	srv.backupVerificationState.pending = &pendingBackupVerification{expected: *config, proof: proof}
}

func backupReviewHasPending(srv *Server) bool {
	srv.backupVerificationState.mu.Lock()
	defer srv.backupVerificationState.mu.Unlock()
	return srv.backupVerificationState.pending != nil
}

func TestBackupReviewOlderOrAmbiguousProofCannotOverwriteCurrentRecord(t *testing.T) {
	for _, kind := range []string{"later legacy upload", "later verified upload", "equal time legacy upload", "equal time different content", "equal time different size"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			srv, config, proof := backupReviewFixture(t)
			expected := *config
			config.LastSyncAt = proof.VerifiedAt + 1000
			if kind == "equal time legacy upload" {
				config.LastSyncAt = proof.VerifiedAt
			} else if kind != "later legacy upload" {
				newer := proof
				newer.VerifiedAt += 1000
				newer.SHA256, newer.Bytes = strings.Repeat("b", 64), 3000
				if strings.HasPrefix(kind, "equal time") {
					newer.VerifiedAt = proof.VerifiedAt
				}
				if kind == "equal time different size" {
					newer.SHA256 = proof.SHA256
				}
				backupReviewStoreProof(config, newer)
			}
			config.LastError = "webdav_http_502"
			before := *config
			if err := srv.store.UpsertBackupConfig(ctx, config); err != nil {
				t.Fatal(err)
			}
			saved, warning := srv.completeBackupVerification(ctx, &expected, &proof)
			if saved || warning != "backup_status_superseded" {
				t.Errorf("unwritten older/ambiguous proof was reported saved or lacked supersession warning: saved=%v warning=%q", saved, warning)
			}
			stored, err := srv.store.GetBackupConfig(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if stored.LastSyncAt != before.LastSyncAt || stored.LastVerifiedAt != before.LastVerifiedAt || stored.LastVerifiedSHA256 != before.LastVerifiedSHA256 || stored.LastVerifiedBytes != before.LastVerifiedBytes || stored.LastVerifiedType != before.LastVerifiedType || stored.LastVerifiedWarningCount != before.LastVerifiedWarningCount || stored.LastError != before.LastError {
				t.Error("older or ambiguous evidence rewrote the current upload, proof or later error")
			}
			if backupReviewHasPending(srv) {
				t.Error("superseded evidence remained pending for future metadata retries")
			}
		})
	}
}

func TestBackupReviewScheduledPendingRetryPreservesLaterLegacyUpload(t *testing.T) {
	ctx := context.Background()
	srv, config, proof := backupReviewFixture(t)
	backupReviewPending(srv, config, proof)
	config.LastSyncAt = proof.VerifiedAt + 1000
	if err := srv.store.UpsertBackupConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	if srv.backupSyncDue(ctx, config, time.UnixMilli(config.LastSyncAt).Add(10*time.Minute)) {
		t.Error("newer upload was treated as due for immediate re-upload")
	}
	stored, err := srv.store.GetBackupConfig(ctx)
	if err != nil || stored.LastSyncAt != config.LastSyncAt || stored.LastVerifiedAt != 0 || backupReviewHasPending(srv) {
		t.Fatal("scheduled metadata retry replaced a later legacy upload or retained superseded evidence")
	}
}

func TestBackupReviewPendingReadRequiresCompleteProof(t *testing.T) {
	for _, kind := range []string{"missing scope", "missing scope with later failure", "wrong warning count", "all fields match", "later failure", "later legacy upload"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			srv, config, proof := backupReviewFixture(t)
			backupReviewPending(srv, config, proof)
			backupReviewStoreProof(config, proof)
			switch kind {
			case "missing scope", "missing scope with later failure":
				config.LastVerifiedType = ""
			case "wrong warning count":
				config.LastVerifiedWarningCount++
			case "later failure":
				config.LastError = "webdav_http_502"
			case "later legacy upload":
				config.LastSyncAt += 1000
			}
			if kind == "missing scope with later failure" {
				config.LastError = "webdav_http_502"
			}
			if err := srv.store.UpsertBackupConfig(ctx, config); err != nil {
				t.Fatal(err)
			}
			warning := srv.pendingBackupObservationWarning(config)
			wantPending := strings.HasPrefix(kind, "missing scope") || kind == "wrong warning count"
			if wantPending && (warning != "backup_status_pending" || !backupReviewHasPending(srv)) {
				t.Error("an incomplete proof erased the pending observation warning")
			}
			if !wantPending && (warning != "" || backupReviewHasPending(srv)) {
				t.Error("persisted or superseded proof remained pending")
			}
			if wantPending {
				_ = srv.backupSyncDue(ctx, config, time.UnixMilli(proof.VerifiedAt).Add(time.Hour))
				stored, err := srv.store.GetBackupConfig(ctx)
				if err != nil || stored.LastVerifiedType != proof.Type || stored.LastVerifiedWarningCount != proof.WarningCount || backupReviewHasPending(srv) {
					t.Error("metadata retry did not complete every missing proof field")
				}
			}
			if strings.Contains(kind, "later failure") {
				stored, err := srv.store.GetBackupConfig(ctx)
				if err != nil || stored.LastError != "webdav_http_502" {
					t.Error("confirming or repairing persisted evidence erased a later failed operation")
				}
			}
		})
	}
}

func TestBackupReviewSavingSamePasswordPreservesProofAndPending(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "persisted", true: "pending"}[pending], func(t *testing.T) {
			ctx := context.Background()
			srv, config, proof := backupReviewFixture(t)
			if pending {
				backupReviewPending(srv, config, proof)
			} else {
				backupReviewStoreProof(config, proof)
				if err := srv.store.UpsertBackupConfig(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			plaintext, err := srv.webDAVPassword(config)
			if err != nil {
				t.Fatal(err)
			}
			c, recorder := newTestContext(t, newJSONRequest(t, http.MethodPut, "/admin/backup/webdav", map[string]any{
				"enabled": config.Enabled, "file_url": config.FileURL, "username": config.Username, "password": plaintext,
				"export_type": config.ExportType, "auto_sync_enabled": config.AutoSyncEnabled, "auto_sync_interval_hours": config.AutoSyncIntervalHours,
			}))
			srv.HandleBackupWebDAV(c)
			stored, err := srv.store.GetBackupConfig(ctx)
			if recorder.Code != http.StatusOK || err != nil || stored.PasswordCiphertext != config.PasswordCiphertext || stored.PasswordKeyVersion != config.PasswordKeyVersion || stored.LastSyncAt != config.LastSyncAt || stored.LastVerifiedAt != config.LastVerifiedAt || stored.LastVerifiedSHA256 != config.LastVerifiedSHA256 {
				t.Fatal("saving unchanged plaintext replaced its envelope or erased upload/verification evidence")
			}
			if backupReviewHasPending(srv) != pending {
				t.Fatal("saving unchanged credentials discarded or invented a pending observation")
			}
			if strings.Contains(recorder.Body.String(), plaintext) {
				t.Fatal("settings response exposed the submitted credential")
			}
		})
	}
}

func TestBackupReviewImportReplacementRepairsUnreadablePassword(t *testing.T) {
	ctx := context.Background()
	target := newBackupRestoreDatabase(t, filepath.Join(t.TempDir(), "target.db"), "fedcba9876543210fedcba9876543210", "target")
	config := &model.BackupConfig{Enabled: true, FileURL: "https://synthetic-repair.invalid/backup.json", Username: "synthetic-repair-user", PasswordCiphertext: "synthetic-unreadable-envelope", PasswordKeyVersion: "older-key", ExportType: "all", AutoSyncIntervalHours: 24}
	backupReviewStoreProof(config, backupVerification{VerifiedAt: 1700000001000, Bytes: 2400, SHA256: strings.Repeat("a", 64), Type: "all"})
	if err := target.store.UpsertBackupConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	input := &backupTarget{Enabled: true, FileURL: config.FileURL, Username: config.Username, Password: "synthetic-observation-auth", ExportType: "all", AutoSyncIntervalHours: 24}
	withoutReplacement := *input
	withoutReplacement.Password = ""
	preview, err := target.server.previewBackupDocument(ctx, &backupDocument{Version: "1.0", Type: "settings", BackupTarget: &withoutReplacement})
	if err != nil || preview.CanApply {
		t.Fatal("unreadable credentials were accepted without an explicit replacement")
	}
	target.server.siteControl.cipher = nil
	_, err = target.server.previewBackupDocument(ctx, &backupDocument{Version: "1.0", Type: "settings", BackupTarget: input})
	if err == nil || !strings.Contains(err.Error(), "credential_locked") {
		t.Fatal("replacement credentials were accepted without a current encryption key")
	}
	target.server.siteControl.cipher = target.cipher
	applyRestoreFixture(t, target, &backupDocument{Version: "1.0", Type: "settings", BackupTarget: input})
	if err := target.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := newBackupRestoreDatabase(t, target.path, "fedcba9876543210fedcba9876543210", "target")
	stored, err := reopened.store.GetBackupConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := reopened.server.webDAVPassword(stored)
	if err != nil || plaintext != input.Password || stored.PasswordKeyVersion != reopened.cipher.Version() || stored.LastSyncAt != 0 || stored.LastVerifiedAt != 0 {
		t.Fatal("replacement password did not repair the target envelope durably under its current key")
	}
}

func TestBackupReviewLegacyDisabledChannelPreservesSharedHistoricalBindings(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	source := newBackupRestoreDatabase(t, filepath.Join(directory, "source.db"), "0123456789abcdef0123456789abcdef", "source")
	target := newBackupRestoreDatabase(t, filepath.Join(directory, "target.db"), "fedcba9876543210fedcba9876543210", "target")
	_, sourceAccount := restoreAccountFixture(t, source, "Legacy shared", "https://synthetic-legacy-shared.invalid", model.SitePlatformOpenAICompatible, "bound", model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-shared-management"})
	_, targetAccount := restoreAccountFixture(t, target, "Legacy shared", "https://synthetic-legacy-shared.invalid", model.SitePlatformOpenAICompatible, "bound", model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-shared-management"})
	_, historicalAccount := restoreAccountFixture(t, target, "Historical owner", "https://synthetic-historical-owner.invalid", model.SitePlatformOpenAICompatible, "history", model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-historical-management"})
	sourceChannel := restoreChannelFixture(t, source, "Legacy shared channel", false)
	targetChannel := restoreChannelFixture(t, target, "Legacy shared channel", false)
	restoreBindingFixture(t, source, sourceAccount.ID, sourceChannel.ID, "shared-first", "projected")
	restoreBindingFixture(t, target, targetAccount.ID, targetChannel.ID, "shared-first", "projected")
	restoreBindingFixture(t, target, historicalAccount.ID, targetChannel.ID, "shared-second", "manual")
	beforeBindings, err := target.store.ListSiteChannelBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeByID := make(map[int64]*model.SiteChannelBinding)
	for _, binding := range beforeBindings {
		beforeByID[binding.ID] = binding
	}
	if _, err := source.store.UpdateChannelEnabled(ctx, sourceChannel.ID, false); err != nil {
		t.Fatal(err)
	}
	document, err := source.server.buildBackupDocument(ctx, "connections")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(document)
	var shape map[string]any
	_ = json.Unmarshal(encoded, &shape)
	delete(shape["connections"].(map[string]any), "bindings")
	encoded, _ = json.Marshal(shape)
	var legacy backupDocument
	_ = json.Unmarshal(encoded, &legacy)
	applyRestoreFixture(t, target, &legacy)
	bindings, err := target.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 2 {
		t.Fatal("legacy import lost a historical binding")
	}
	for _, binding := range bindings {
		before := beforeByID[binding.ID]
		if before == nil || binding.SiteAccountID != before.SiteAccountID || binding.ProjectionKey != before.ProjectionKey || binding.PricingGroup != before.PricingGroup || binding.LastProjectedHash != before.LastProjectedHash || binding.LastSyncStatus != before.LastSyncStatus || binding.LastSyncError != before.LastSyncError {
			t.Fatal("legacy recovery replaced a local binding identity or synchronization history")
		}
		if binding.ChannelID != targetChannel.ID || binding.Ownership != "manual" || binding.Status != "disabled" || binding.CreatedAt != 100 {
			t.Fatal("legacy disabled-channel restore rewrote or reactivated a shared historical binding")
		}
	}
}
