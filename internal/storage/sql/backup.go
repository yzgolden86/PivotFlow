package sql

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func (s *SQLStore) GetBackupConfig(ctx context.Context) (*model.BackupConfig, error) {
	var config model.BackupConfig
	var enabled, autoSyncEnabled int
	err := s.QueryRowContext(ctx, `SELECT id,enabled,file_url,username,password_ciphertext,password_key_version,export_type,auto_sync_enabled,auto_sync_interval_hours,last_sync_at,last_error,created_at,updated_at FROM backup_settings WHERE id=1`).Scan(
		&config.ID, &enabled, &config.FileURL, &config.Username, &config.PasswordCiphertext, &config.PasswordKeyVersion, &config.ExportType, &autoSyncEnabled, &config.AutoSyncIntervalHours, &config.LastSyncAt, &config.LastError, &config.CreatedAt, &config.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("not found")
	}
	if err != nil {
		return nil, err
	}
	config.Enabled = enabled != 0
	config.AutoSyncEnabled = autoSyncEnabled != 0
	config.PasswordConfigured = strings.TrimSpace(config.PasswordCiphertext) != ""
	return &config, nil
}

func (s *SQLStore) UpsertBackupConfig(ctx context.Context, config *model.BackupConfig) error {
	if config == nil {
		return errors.New("backup config is required")
	}
	now := siteNow()
	config.ID = 1
	if config.CreatedAt == 0 {
		config.CreatedAt = now
	}
	config.UpdatedAt = now
	args := []any{config.ID, config.Enabled, config.FileURL, config.Username, config.PasswordCiphertext, config.PasswordKeyVersion, config.ExportType, config.AutoSyncEnabled, config.AutoSyncIntervalHours, config.LastSyncAt, config.LastError, config.CreatedAt, config.UpdatedAt}
	query := `INSERT INTO backup_settings(id,enabled,file_url,username,password_ciphertext,password_key_version,export_type,auto_sync_enabled,auto_sync_interval_hours,last_sync_at,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`
	if s.IsMySQL() {
		query += ` ON DUPLICATE KEY UPDATE enabled=VALUES(enabled),file_url=VALUES(file_url),username=VALUES(username),password_ciphertext=VALUES(password_ciphertext),password_key_version=VALUES(password_key_version),export_type=VALUES(export_type),auto_sync_enabled=VALUES(auto_sync_enabled),auto_sync_interval_hours=VALUES(auto_sync_interval_hours),last_sync_at=VALUES(last_sync_at),last_error=VALUES(last_error),updated_at=VALUES(updated_at)`
	} else {
		query += ` ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled,file_url=excluded.file_url,username=excluded.username,password_ciphertext=excluded.password_ciphertext,password_key_version=excluded.password_key_version,export_type=excluded.export_type,auto_sync_enabled=excluded.auto_sync_enabled,auto_sync_interval_hours=excluded.auto_sync_interval_hours,last_sync_at=excluded.last_sync_at,last_error=excluded.last_error,updated_at=excluded.updated_at`
	}
	_, err := s.ExecContext(ctx, query, args...)
	return err
}
