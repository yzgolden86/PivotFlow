package storage

import (
	"context"
	"testing"
)

func TestMigrateSQLiteAddsRequestHistoryColumnsAndTable(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if err := migrate(ctx, db, DialectSQLite); err != nil {
		t.Fatalf("initial migrate: %v", err)
	}

	if _, err := db.ExecContext(ctx, "INSERT INTO logs (time, status_code, message) VALUES (1, 200, 'legacy')"); err != nil {
		t.Fatalf("insert legacy log: %v", err)
	}
	if _, err := db.ExecContext(ctx, "DROP INDEX IF EXISTS idx_logs_request_id"); err != nil {
		t.Fatalf("drop request id index: %v", err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE logs DROP COLUMN request_id"); err != nil {
		t.Fatalf("drop request_id: %v", err)
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE logs DROP COLUMN attempt_number"); err != nil {
		t.Fatalf("drop attempt_number: %v", err)
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE request_traces"); err != nil {
		t.Fatalf("drop request_traces: %v", err)
	}

	if err := migrate(ctx, db, DialectSQLite); err != nil {
		t.Fatalf("upgrade migrate: %v", err)
	}

	columns, err := sqliteExistingColumns(ctx, db, "logs")
	if err != nil {
		t.Fatalf("read logs columns: %v", err)
	}
	if !columns["request_id"] || !columns["attempt_number"] {
		t.Fatalf("logs correlation columns missing: %v", columns)
	}
	var requestID string
	var attemptNumber int
	if err := db.QueryRowContext(ctx, "SELECT request_id, attempt_number FROM logs WHERE message='legacy' LIMIT 1").Scan(&requestID, &attemptNumber); err != nil {
		t.Fatalf("read migrated legacy row: %v", err)
	}
	if requestID != "" || attemptNumber != 0 {
		t.Fatalf("legacy row defaults=(%q,%d), want empty/0", requestID, attemptNumber)
	}
	var requestTraceCount int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM request_traces").Scan(&requestTraceCount); err != nil {
		t.Fatalf("request_traces table was not recreated: %v", err)
	}
	if requestTraceCount != 0 {
		t.Fatalf("legacy migration invented %d request traces", requestTraceCount)
	}
	for _, indexName := range []string{
		"idx_logs_request_id",
		"idx_request_traces_started_at",
		"idx_request_traces_token_started_at",
	} {
		var indexCount int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", indexName).Scan(&indexCount); err != nil {
			t.Fatalf("check index %s: %v", indexName, err)
		}
		if indexCount != 1 {
			t.Fatalf("index %s count=%d, want 1", indexName, indexCount)
		}
	}
}
