//go:build sonic

package storage

import (
	"context"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestMigrateAPIKeyHealthLegacyDefaultsAndIdempotence(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE api_keys (id INTEGER PRIMARY KEY, api_key TEXT NOT NULL);
		INSERT INTO api_keys VALUES (1, 'legacy-key')`); err != nil {
		t.Fatal(err)
	}
	if err := ensureAPIKeysHealth(ctx, db, DialectSQLite); err != nil {
		t.Fatal(err)
	}
	read := func() model.APIKeyHealth {
		t.Helper()
		var h model.APIKeyHealth
		if err := db.QueryRowContext(ctx, `SELECT health_status, health_reason, health_status_code, health_checked_at FROM api_keys WHERE id=1`).Scan(
			&h.Status, &h.Reason, &h.StatusCode, &h.CheckedAt); err != nil {
			t.Fatal(err)
		}
		return h
	}
	if h := read(); h != (model.APIKeyHealth{}) {
		t.Fatalf("legacy key should be unobserved: %+v", h)
	}
	if _, err := db.ExecContext(ctx, `UPDATE api_keys SET health_status='invalid', health_reason='test', health_status_code=401, health_checked_at=1234 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := ensureAPIKeysHealth(ctx, db, DialectSQLite); err != nil {
		t.Fatal(err)
	}
	if h := read(); h.Status != "invalid" || h.Reason != "test" || h.StatusCode != 401 || h.CheckedAt != 1234 {
		t.Fatalf("repeated migration lost health: %+v", h)
	}
}
