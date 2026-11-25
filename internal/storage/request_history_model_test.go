//go:build sonic

package storage

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func storedTraceActualModel(t testing.TB, trace *model.RequestTrace) string {
	t.Helper()
	if trace == nil {
		t.Fatal("request trace is missing")
		return ""
	}
	encoded, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	actual, _ := payload["actual_model"].(string)
	return actual
}

func TestRequestHistoryActualModelSummaryPreservesFullInputBeforeTimelineLimit(t *testing.T) {
	store := createTestSQLiteStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	at := time.Now().Add(-time.Second)
	trace := testRequestTrace("pf_req_actual_summary", "deepseek-v4.1-flash", "success", at, 7)
	for n := 1; n <= 140; n++ {
		trace.Attempts = append(trace.Attempts, model.RequestAttempt{Number: n, Upstream: true, ActualModel: "vendor/variant-" + strconv.Itoa(n), Outcome: "success", Transport: "http", UpstreamProtocol: "openai"})
	}
	trace.AttemptCount, trace.UpstreamAttemptCount = 140, 140
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatal(err)
	}
	rows, count, err := store.ListRequestTraces(ctx, time.Time{}, time.Now(), 20, 0, nil)
	if err != nil || count != 1 || len(rows) != 1 {
		t.Fatalf("summary count=%d rows=%d err=%v", count, len(rows), err)
		return
	}
	if actual := storedTraceActualModel(t, rows[0]); actual != "vendor/variant-140" {
		t.Fatalf("summary model=%q; final upstream beyond the 128 saved events must remain visible", actual)
	}
	if rows[0].Attempts != nil {
		t.Fatal("list returned the full attempt timeline")
	}
	detail, err := store.GetRequestTrace(ctx, trace.RequestID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if storedTraceActualModel(t, detail) != "vendor/variant-140" {
		t.Fatal("detail and list actual models differ")
	}
	if detail.Model != "deepseek-v4.1-flash" || !detail.Truncated || len(detail.Attempts) != 128 {
		t.Fatalf("canonical model or timeline limit changed: %+v", detail)
	}
	if trace.Truncated || len(trace.Attempts) != 140 {
		t.Fatal("normalization mutated the caller's trace")
	}
}

func TestRequestHistoryLegacyModelsComeFromLastTransmittedAttemptOnly(t *testing.T) {
	store := createTestSQLiteStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	at := time.Now().Add(-time.Second)
	trace := testRequestTrace("pf_req_actual_legacy", "canonical", "error", at, 7,
		model.RequestAttempt{Number: 3, Upstream: false, ActualModel: "unsent", Outcome: "skipped"},
		model.RequestAttempt{Number: 2, Upstream: true, ActualModel: "vendor/final-failed-variant", Outcome: "error"},
		model.RequestAttempt{Number: 1, Upstream: true, ActualModel: "vendor/first-variant", Outcome: "error"},
	)
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatal(err)
	}
	// Empty new scalar emulates an already stored row from the previous schema.
	if _, err := store.ExecContext(ctx, "UPDATE request_traces SET actual_model = '' WHERE request_id = ?", trace.RequestID); err != nil {
		t.Fatal(err)
	}
	rows, _, err := store.ListRequestTraces(ctx, time.Time{}, time.Now(), 20, 0, nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("legacy summaries: rows=%d err=%v", len(rows), err)
		return
	}
	if actual := storedTraceActualModel(t, rows[0]); actual != "vendor/final-failed-variant" {
		t.Fatalf("legacy summary actual=%q", actual)
	}
	if _, err := store.ExecContext(ctx, "UPDATE request_traces SET truncated = 1 WHERE request_id = ?", trace.RequestID); err != nil {
		t.Fatal(err)
	}
	rows, _, err = store.ListRequestTraces(ctx, time.Time{}, time.Now(), 20, 0, nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("truncated summaries: rows=%d err=%v", len(rows), err)
		return
	}
	if actual := storedTraceActualModel(t, rows[0]); actual != "" {
		t.Fatalf("truncated legacy prefix was misrepresented as final upstream %q", actual)
	}
}

func TestRequestHistoryStatsUseCoveringIndexesAndStablePageOrdering(t *testing.T) {
	store := createTestSQLiteStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	for _, query := range []string{
		"SELECT COUNT(*), SUM(CASE WHEN outcome='success' THEN 1 ELSE 0 END), SUM(CASE WHEN outcome='error' THEN 1 ELSE 0 END), SUM(CASE WHEN outcome='canceled' THEN 1 ELSE 0 END), SUM(upstream_attempt_count), SUM(upstream_success_count) FROM request_traces WHERE started_at >= 0 AND started_at <= 1000",
		"SELECT COUNT(*), SUM(CASE WHEN outcome='success' THEN 1 ELSE 0 END), SUM(CASE WHEN outcome='error' THEN 1 ELSE 0 END), SUM(CASE WHEN outcome='canceled' THEN 1 ELSE 0 END), SUM(upstream_attempt_count), SUM(upstream_success_count) FROM request_traces WHERE started_at >= 0 AND started_at <= 1000 AND auth_token_id = 7",
	} {
		rows, err := store.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query)
		if err != nil {
			t.Fatal(err)
		}
		covering := false
		for rows.Next() {
			var id, parent, unused int
			var plan string
			if err := rows.Scan(&id, &parent, &unused, &plan); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			covering = covering || strings.Contains(plan, "COVERING INDEX")
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			t.Fatal(err)
		}
		_ = rows.Close()
		if !covering {
			t.Fatal("request stats touch the large timeline table instead of summary-only covering indexes")
		}
	}
	rows, err := store.QueryContext(ctx, "EXPLAIN QUERY PLAN SELECT request_id, model FROM request_traces WHERE started_at >= 0 AND started_at <= 1000 ORDER BY started_at DESC, request_id DESC LIMIT 20")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, parent, unused int
		var plan string
		if err := rows.Scan(&id, &parent, &unused, &plan); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(plan, "TEMP B-TREE") {
			t.Fatalf("latest-page ordering requires a temporary sort: %s", plan)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
