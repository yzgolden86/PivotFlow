package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type sqliteSnapshotter interface {
	CreateSQLiteSnapshot(context.Context, string) error
}

// CreateSQLiteSnapshot creates and verifies a consistent snapshot. Site
// credentials remain encrypted and still require the original master key.
func CreateSQLiteSnapshot(ctx context.Context, store Store, destination string) error {
	snapshotter, ok := store.(sqliteSnapshotter)
	if !ok {
		return errors.New("store does not support SQLite snapshots")
	}
	if err := snapshotter.CreateSQLiteSnapshot(ctx, destination); err != nil {
		return err
	}
	if err := VerifySQLiteSnapshot(ctx, destination); err != nil {
		return fmt.Errorf("verify SQLite snapshot: %w", err)
	}
	return nil
}

// VerifySQLiteSnapshot validates file presence, SQLite integrity, and the
// control-plane/routing tables required for a usable restore.
func VerifySQLiteSnapshot(ctx context.Context, snapshotPath string) error {
	abs, err := filepath.Abs(filepath.Clean(snapshotPath))
	if err != nil {
		return fmt.Errorf("resolve snapshot path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("stat snapshot: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("snapshot is not a non-empty regular file")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(abs)+"?mode=ro")
	if err != nil {
		return fmt.Errorf("open snapshot: %w", err)
	}
	defer func() { _ = db.Close() }()
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return fmt.Errorf("run quick_check: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("quick_check returned %q", integrity)
	}
	required := []string{"channels", "api_keys", "sites", "site_accounts", "site_channel_bindings", "site_tasks", "webhook_endpoints", "webhook_event_states"}
	for _, table := range required {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil {
			return fmt.Errorf("verify table %s: %w", table, err)
		}
		if count != 1 {
			return fmt.Errorf("snapshot is missing table %s", table)
		}
	}
	return nil
}
