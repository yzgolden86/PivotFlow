//go:build sonic

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"

	_ "modernc.org/sqlite"
)

func TestBackupVerificationMigrationPreservesLegacyUploadAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-backup.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `CREATE TABLE backup_settings (
		id INTEGER PRIMARY KEY, enabled INTEGER NOT NULL DEFAULT 0,
		file_url TEXT NOT NULL, username TEXT NOT NULL DEFAULT '',
		password_ciphertext TEXT NOT NULL, password_key_version TEXT NOT NULL DEFAULT '',
		export_type TEXT NOT NULL DEFAULT 'all', auto_sync_enabled INTEGER NOT NULL DEFAULT 0,
		auto_sync_interval_hours INTEGER NOT NULL DEFAULT 24, last_sync_at INTEGER NOT NULL DEFAULT 0,
		last_error TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
	)`)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO backup_settings VALUES (1,1,'https://legacy-synthetic.invalid/dav','synthetic-user','synthetic-ciphertext','synthetic-v1','all',1,24,1700000000000,'legacy-error',100,200)`)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	for pass := range 2 {
		store, err := CreateSQLiteStore(path)
		if err != nil {
			t.Fatal(err)
		}
		config, err := store.GetBackupConfig(ctx)
		if err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		if config.FileURL != "https://legacy-synthetic.invalid/dav" || config.PasswordCiphertext != "synthetic-ciphertext" || config.LastSyncAt != 1700000000000 || config.LastError != "legacy-error" || config.CreatedAt != 100 {
			_ = store.Close()
			t.Fatal("migration changed the legacy target, credential, or upload observation")
		}
		encoded, _ := json.Marshal(config)
		var fields map[string]any
		_ = json.Unmarshal(encoded, &fields)
		for _, field := range []string{"last_verified_at", "last_verified_bytes", "last_verified_warning_count"} {
			value, exists := fields[field]
			if !exists || value != float64(0) {
				_ = store.Close()
				t.Fatalf("legacy upload has no explicit empty verification observation: %s", field)
			}
		}
		if pass == 0 {
			concrete := store.(*sqlstore.SQLStore)
			_, err = concrete.ExecContext(ctx, `UPDATE backup_settings SET last_verified_at=1800000000000,last_verified_bytes=321,last_verified_sha256=?,last_verified_type='connections',last_verified_warning_count=2 WHERE id=1`, strings.Repeat("a", 64))
			if err != nil {
				_ = store.Close()
				t.Fatal(err)
			}
			config, err = store.GetBackupConfig(ctx)
			if err == nil {
				err = store.UpsertBackupConfig(ctx, config)
			}
			if err != nil {
				_ = store.Close()
				t.Fatal(err)
			}
			// Reset observations to test the second legacy opening, then a
			// separate assertion below checks non-empty proof persistence.
			_, err = concrete.ExecContext(ctx, `UPDATE backup_settings SET last_verified_at=0,last_verified_bytes=0,last_verified_sha256='',last_verified_type='',last_verified_warning_count=0 WHERE id=1`)
			if err != nil {
				_ = store.Close()
				t.Fatal(err)
			}
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
	}

	store, err := CreateSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	concrete := store.(*sqlstore.SQLStore)
	_, err = concrete.ExecContext(ctx, `UPDATE backup_settings SET last_verified_at=1800000000000,last_verified_bytes=321,last_verified_sha256=?,last_verified_type='connections',last_verified_warning_count=2 WHERE id=1`, strings.Repeat("a", 64))
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	config, err := store.GetBackupConfig(ctx)
	if err == nil {
		err = store.UpsertBackupConfig(ctx, config)
	}
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := CreateSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	config, err = reopened.GetBackupConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(config)
	var fields map[string]any
	_ = json.Unmarshal(encoded, &fields)
	if fields["last_verified_at"] != float64(1800000000000) || fields["last_verified_bytes"] != float64(321) || fields["last_verified_sha256"] != strings.Repeat("a", 64) || fields["last_verified_type"] != "connections" || fields["last_verified_warning_count"] != float64(2) {
		t.Fatal("reopen or config upsert dropped saved verification evidence")
	}
}
