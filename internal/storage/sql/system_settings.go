package sql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// GetSetting 获取单个配置项
func (s *SQLStore) GetSetting(ctx context.Context, key string) (*model.SystemSetting, error) {
	k, v := s.quoteIdent("key"), s.quoteIdent("value")
	row := s.QueryRowContext(ctx, `
		SELECT `+k+`, `+v+`, value_type, description, default_value, updated_at
		FROM system_settings
		WHERE `+k+` = ?
	`, key)

	var setting model.SystemSetting
	if err := row.Scan(&setting.Key, &setting.Value, &setting.ValueType, &setting.Description, &setting.DefaultValue, &setting.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, model.ErrSettingNotFound
		}
		return nil, fmt.Errorf("query setting: %w", err)
	}

	return &setting, nil
}

// ListAllSettings 获取所有配置项
func (s *SQLStore) ListAllSettings(ctx context.Context) ([]*model.SystemSetting, error) {
	k, v := s.quoteIdent("key"), s.quoteIdent("value")
	rows, err := s.QueryContext(ctx, `
		SELECT `+k+`, `+v+`, value_type, description, default_value, updated_at
		FROM system_settings
		ORDER BY `+k+` ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query all settings: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var settings []*model.SystemSetting
	for rows.Next() {
		var setting model.SystemSetting
		if err := rows.Scan(&setting.Key, &setting.Value, &setting.ValueType, &setting.Description, &setting.DefaultValue, &setting.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		settings = append(settings, &setting)
	}

	return settings, rows.Err()
}

// UpdateSetting 更新配置项(仅更新value和updated_at)
func (s *SQLStore) UpdateSetting(ctx context.Context, key, value string) error {
	now := timeToUnix(time.Now())
	k, v := s.quoteIdent("key"), s.quoteIdent("value")

	result, err := s.ExecContext(ctx, `
		UPDATE system_settings
		SET `+v+` = ?, updated_at = ?
		WHERE `+k+` = ?
	`, value, now, key)
	if err != nil {
		return fmt.Errorf("update setting: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rows == 0 {
		return model.ErrSettingNotFound
	}

	return nil
}

// CompareAndSwapSetting updates one setting only while its current persisted
// value equals expectedValue. A false result means another writer won.
func (s *SQLStore) CompareAndSwapSetting(ctx context.Context, key, expectedValue, newValue string) (bool, error) {
	now := timeToUnix(time.Now())
	k, v := s.quoteIdent("key"), s.quoteIdent("value")
	exactValue := exactTextEqual(s.driverName, v)

	result, err := s.ExecContext(ctx, `
		UPDATE system_settings
		SET `+v+` = ?, updated_at = ?
		WHERE `+k+` = ? AND `+exactValue+`
	`, newValue, now, key, expectedValue)
	if err != nil {
		return false, fmt.Errorf("compare and swap setting: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check compare and swap result: %w", err)
	}
	return rows > 0, nil
}

// BatchUpdateSettings 批量更新配置项(事务保护)
func (s *SQLStore) BatchUpdateSettings(ctx context.Context, updates map[string]string) error {
	return s.WithTransaction(ctx, func(tx *sql.Tx) error {
		now := timeToUnix(time.Now())
		k, v := s.quoteIdent("key"), s.quoteIdent("value")

		stmt, err := s.prepareTx(ctx, tx, `
			UPDATE system_settings
			SET `+v+` = ?, updated_at = ?
			WHERE `+k+` = ?
		`)
		if err != nil {
			return fmt.Errorf("prepare statement: %w", err)
		}
		defer func() { _ = stmt.Close() }()

		for key, value := range updates {
			result, err := stmt.ExecContext(ctx, value, now, key)
			if err != nil {
				return fmt.Errorf("update setting %s: %w", key, err)
			}

			rows, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("check rows affected for %s: %w", key, err)
			}
			if rows == 0 {
				return model.ErrSettingNotFound
			}
		}

		return nil
	})
}

// UpsertSettingRecord writes a complete setting row. It is used when a hybrid
// cache mirrors a setting created or changed by an authoritative transaction.
func (s *SQLStore) UpsertSettingRecord(ctx context.Context, setting *model.SystemSetting) error {
	if setting == nil || strings.TrimSpace(setting.Key) == "" {
		return fmt.Errorf("setting record and key are required")
	}
	updatedAt := setting.UpdatedAt
	if updatedAt == 0 {
		updatedAt = timeToUnix(time.Now())
	}
	k, v := s.quoteIdent("key"), s.quoteIdent("value")
	query := `INSERT INTO system_settings(` + k + `,` + v + `,value_type,description,default_value,updated_at)
		VALUES(?,?,?,?,?,?)`
	if s.IsMySQL() {
		query += ` ON DUPLICATE KEY UPDATE ` + v + `=VALUES(` + v + `), value_type=VALUES(value_type), description=VALUES(description), default_value=VALUES(default_value), updated_at=VALUES(updated_at)`
	} else {
		query += ` ON CONFLICT(` + k + `) DO UPDATE SET ` + v + `=excluded.` + v + `, value_type=excluded.value_type, description=excluded.description, default_value=excluded.default_value, updated_at=excluded.updated_at`
	}
	if _, err := s.ExecContext(ctx, query, setting.Key, setting.Value, setting.ValueType, setting.Description, setting.DefaultValue, updatedAt); err != nil {
		return fmt.Errorf("upsert setting %s: %w", setting.Key, err)
	}
	return nil
}

// DeleteSetting removes one cached setting row.
func (s *SQLStore) DeleteSetting(ctx context.Context, key string) error {
	k := s.quoteIdent("key")
	if _, err := s.ExecContext(ctx, `DELETE FROM system_settings WHERE `+k+` = ?`, key); err != nil {
		return fmt.Errorf("delete setting %s: %w", key, err)
	}
	return nil
}
