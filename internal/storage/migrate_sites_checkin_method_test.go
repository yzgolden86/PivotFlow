//go:build sonic

package storage

import (
	"context"
	"testing"
)

// A site created before the check-in capability was recorded must survive the
// upgrade: the columns are added in place and existing rows keep their data.
func TestMigrateSQLite_LegacySiteGetsCheckinMethodColumns(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	if _, err := db.ExecContext(ctx, `
		CREATE TABLE sites (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			platform TEXT NOT NULL,
			base_url TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
			use_system_proxy INTEGER NOT NULL DEFAULT 1,
			proxy_url TEXT,
			external_checkin_url TEXT,
			tags_json TEXT NOT NULL,
			last_probe_status TEXT NOT NULL DEFAULT 'unknown',
			last_error TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			deleted_at INTEGER NOT NULL DEFAULT 0
		)
	`); err != nil {
		t.Fatalf("create legacy sites: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sites (name, platform, base_url, tags_json, last_error, created_at, updated_at)
		VALUES ('legacy-site', 'new-api-family', 'https://legacy.example', '[]', '', 1, 1)
	`); err != nil {
		t.Fatalf("insert legacy site: %v", err)
	}

	if err := migrate(ctx, db, DialectSQLite); err != nil {
		t.Fatalf("migrate legacy sites: %v", err)
	}

	cols, err := sqliteExistingColumns(ctx, db, "sites")
	if err != nil {
		t.Fatalf("sqliteExistingColumns: %v", err)
	}
	for _, column := range []string{"checkin_method", "checkin_method_checked_at"} {
		if !cols[column] {
			t.Fatalf("%s column not found in sites", column)
		}
	}

	var method string
	var checkedAt int64
	if err := db.QueryRowContext(ctx,
		"SELECT checkin_method, checkin_method_checked_at FROM sites WHERE name='legacy-site'",
	).Scan(&method, &checkedAt); err != nil {
		t.Fatalf("read migrated site: %v", err)
	}
	// "Never discovered" has to read as empty rather than as some default
	// capability, so the scheduler probes once and caches instead of trusting a
	// value nobody ever observed.
	if method != "" || checkedAt != 0 {
		t.Fatalf("migrated site checkin_method=%q checkedAt=%d, want empty/0", method, checkedAt)
	}

	// Re-running the migration must stay a no-op.
	if err := ensureSitesCheckinMethodColumns(ctx, db, DialectSQLite); err != nil {
		t.Fatalf("ensureSitesCheckinMethodColumns: %v", err)
	}
}
