//go:build sonic

package storage

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"

	_ "modernc.org/sqlite"
)

func TestRequestCacheHistoryRoundTripsCacheMetadataWithNoAttempts(t *testing.T) {
	store := createTestSQLiteStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	trace := &model.RequestTrace{
		RequestID:      "pf_req_cache_hit",
		StartedAt:      now.UnixMilli(),
		AuthTokenID:    17,
		Model:          "gpt-cacheable",
		ClientProtocol: "openai",
		Transport:      "http",
		FinalStatus:    200,
		Outcome:        "success",
		Duration:       0.002,
		CacheStatus:    "hit",
		CacheReason:    "reused",
		Attempts:       []model.RequestAttempt{},
	}
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatalf("BatchAddRequestTraces cache hit: %v", err)
	}

	got, err := store.GetRequestTrace(ctx, trace.RequestID, int64Pointer(trace.AuthTokenID))
	if err != nil {
		t.Fatalf("GetRequestTrace cache hit: %v", err)
	}
	if got == nil {
		t.Fatal("GetRequestTrace returned nil for cache hit")
	}
	if got.CacheStatus != "hit" || got.CacheReason != "reused" {
		t.Fatalf("cache metadata=(%q,%q), want hit/reused", got.CacheStatus, got.CacheReason)
	}
	if got.AttemptCount != 0 || got.UpstreamAttemptCount != 0 || got.UpstreamSuccessCount != 0 {
		t.Fatalf("cache hit invented attempt counters: %+v", got)
	}
	if len(got.Attempts) != 0 {
		t.Fatalf("cache hit invented timeline attempts: %+v", got.Attempts)
	}

	traces, total, err := store.ListRequestTraces(
		ctx,
		now.Add(-time.Minute),
		now.Add(time.Minute),
		10,
		0,
		&model.RequestTraceFilter{AuthTokenID: int64Pointer(trace.AuthTokenID)},
	)
	if err != nil {
		t.Fatalf("ListRequestTraces cache hit: %v", err)
	}
	if total != 1 || len(traces) != 1 {
		t.Fatalf("cache list total=%d len=%d, want 1", total, len(traces))
	}
	if traces[0].CacheStatus != "hit" || traces[0].CacheReason != "reused" {
		t.Fatalf("summary cache metadata=(%q,%q), want hit/reused", traces[0].CacheStatus, traces[0].CacheReason)
	}
	if traces[0].Attempts != nil {
		t.Fatalf("summary unexpectedly included attempts: %+v", traces[0].Attempts)
	}

	requireRequestTraceMissing(t, store, ctx, trace.RequestID, int64Pointer(trace.AuthTokenID+1))
}

func TestRequestCacheHistoryValidatorsUseFixedAllowlists(t *testing.T) {
	for _, value := range []string{"", "hit", "miss", "refresh", "bypass"} {
		if !model.ValidRequestCacheStatus(value) {
			t.Errorf("ValidRequestCacheStatus(%q)=false, want true", value)
		}
	}
	for _, value := range []string{"HIT", "stale", "hit,miss", "client_secret_status"} {
		if model.ValidRequestCacheStatus(value) {
			t.Errorf("ValidRequestCacheStatus(%q)=true, want false", value)
		}
	}

	reasons := []string{
		"",
		"disabled",
		"not_opted_in",
		"client_bypass",
		"invalid_mode",
		"request_no_store",
		"request_no_cache",
		"request_query",
		"request_encoding",
		"streaming",
		"unsupported_endpoint",
		"nondeterministic",
		"tools_or_state",
		"unsupported_request",
		"no_identity",
		"route_unavailable",
		"channel_changed",
		"custom_rules",
		"protocol_transform",
		"empty",
		"expired",
		"client_refresh",
		"unsafe_response",
		"response_no_store",
		"response_too_large",
		"capture_budget",
		"invalidated",
		"upstream_error",
		"canceled",
		"write_failed",
		"reused",
		"stored",
	}
	for _, value := range reasons {
		if !model.ValidRequestCacheReason(value) {
			t.Errorf("ValidRequestCacheReason(%q)=false, want true", value)
		}
	}
	for _, value := range []string{"DISABLED", "raw reason", "request body secret", "client_secret_reason"} {
		if model.ValidRequestCacheReason(value) {
			t.Errorf("ValidRequestCacheReason(%q)=true, want false", value)
		}
	}
}

func TestRequestCacheHistoryRejectsInvalidMetadataWithoutEchoingRawValue(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	tests := []struct {
		name   string
		mutate func(*model.RequestTrace)
		raw    string
	}{
		{
			name: "status",
			mutate: func(trace *model.RequestTrace) {
				trace.CacheStatus = "client_secret_status"
			},
			raw: "client_secret_status",
		},
		{
			name: "reason",
			mutate: func(trace *model.RequestTrace) {
				trace.CacheReason = "client_secret_reason"
			},
			raw: "client_secret_reason",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			trace := &model.RequestTrace{
				RequestID:      "pf_req_cache_invalid_" + test.name,
				StartedAt:      now.UnixMilli(),
				Model:          "model",
				ClientProtocol: "openai",
				Transport:      "http",
				FinalStatus:    200,
				Outcome:        "success",
				Attempts:       []model.RequestAttempt{},
			}
			test.mutate(trace)
			_, err := sqlstore.NormalizeRequestTraceForStorage(trace)
			if err == nil {
				t.Fatalf("NormalizeRequestTraceForStorage accepted invalid cache %s", test.name)
			}
			if strings.Contains(err.Error(), test.raw) {
				t.Fatalf("cache validation error exposed raw %s: %v", test.name, err)
			}
		})
	}
}

func TestRequestCacheHistoryCorruptStoredMetadataErrorsDoNotEchoValues(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := migrate(ctx, db, DialectSQLite); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}

	store := sqlstore.NewSQLStore(db, "sqlite")
	now := time.Now().Truncate(time.Millisecond)
	trace := &model.RequestTrace{
		RequestID:      "pf_req_cache_corrupt",
		StartedAt:      now.UnixMilli(),
		Model:          "model",
		ClientProtocol: "openai",
		Transport:      "http",
		FinalStatus:    200,
		Outcome:        "success",
		CacheStatus:    "hit",
		CacheReason:    "reused",
		Attempts:       []model.RequestAttempt{},
	}
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatalf("seed valid cache metadata: %v", err)
	}
	const raw = "client_secret_corrupt_value"
	if _, err := db.ExecContext(ctx, "UPDATE request_traces SET cache_status=? WHERE request_id=?", raw, trace.RequestID); err != nil {
		t.Fatalf("corrupt stored cache status: %v", err)
	}

	if _, err := store.GetRequestTrace(ctx, trace.RequestID, nil); err == nil {
		t.Fatal("GetRequestTrace accepted corrupt cache status")
	} else if strings.Contains(err.Error(), raw) {
		t.Fatalf("detail scan error exposed raw corrupt metadata: %v", err)
	}
	if _, _, err := store.ListRequestTraces(ctx, now.Add(-time.Minute), now.Add(time.Minute), 10, 0, nil); err == nil {
		t.Fatal("ListRequestTraces accepted corrupt cache status")
	} else if strings.Contains(err.Error(), raw) {
		t.Fatalf("summary scan error exposed raw corrupt metadata: %v", err)
	}
}

func TestRequestCacheHistoryLegacySQLiteMigrationAddsEmptyMetadata(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()

	_, err = db.ExecContext(ctx, `
		CREATE TABLE request_traces (
			request_id VARCHAR(64) PRIMARY KEY,
			started_at BIGINT NOT NULL,
			auth_token_id BIGINT NOT NULL DEFAULT 0,
			model VARCHAR(191) NOT NULL DEFAULT '',
			client_protocol VARCHAR(32) NOT NULL DEFAULT '',
			transport VARCHAR(16) NOT NULL DEFAULT '',
			is_streaming TINYINT NOT NULL DEFAULT 0,
			final_status INT NOT NULL DEFAULT 0,
			outcome VARCHAR(16) NOT NULL DEFAULT '',
			duration DOUBLE NOT NULL DEFAULT 0,
			wait_duration DOUBLE NOT NULL DEFAULT 0,
			attempt_count INT NOT NULL DEFAULT 0,
			upstream_attempt_count INT NOT NULL DEFAULT 0,
			upstream_success_count INT NOT NULL DEFAULT 0,
			truncated TINYINT NOT NULL DEFAULT 0,
			attempts_json LONGTEXT NOT NULL
		);
		INSERT INTO request_traces (
			request_id, started_at, auth_token_id, model, client_protocol,
			transport, is_streaming, final_status, outcome, duration,
			wait_duration, attempt_count, upstream_attempt_count,
			upstream_success_count, truncated, attempts_json
		) VALUES (
			'pf_req_cache_legacy', 1, 23, 'legacy-model', 'openai',
			'http', 0, 200, 'success', 0.5, 0, 0, 0, 0, 0, '[]'
		)
	`)
	if err != nil {
		t.Fatalf("create legacy request_traces fixture: %v", err)
	}

	if err := migrate(ctx, db, DialectSQLite); err != nil {
		t.Fatalf("migrate legacy request_traces: %v", err)
	}

	columns, err := sqliteExistingColumns(ctx, db, "request_traces")
	if err != nil {
		t.Fatalf("read migrated request_traces columns: %v", err)
	}
	if !columns["cache_status"] || !columns["cache_reason"] {
		t.Fatalf("cache metadata columns missing after real SQLite migration: %v", columns)
	}

	store := sqlstore.NewSQLStore(db, "sqlite")
	got, err := store.GetRequestTrace(ctx, "pf_req_cache_legacy", int64Pointer(23))
	if err != nil {
		t.Fatalf("scan legacy cache metadata: %v", err)
	}
	if got == nil {
		t.Fatal("legacy trace disappeared during migration")
	}
	if got.CacheStatus != "" || got.CacheReason != "" {
		t.Fatalf("legacy cache metadata=(%q,%q), want empty defaults", got.CacheStatus, got.CacheReason)
	}
	if got.AttemptCount != 0 || len(got.Attempts) != 0 {
		t.Fatalf("legacy zero-attempt trace changed: %+v", got)
	}
}
