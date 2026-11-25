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

func requireStoredTraceChannel(t testing.TB, trace *model.RequestTrace, wantID int64, wantName string) {
	t.Helper()
	if trace == nil {
		t.Fatal("request trace is missing")
	}
	encoded, err := json.Marshal(trace)
	if err != nil {
		t.Fatal(err)
	}
	var fields struct {
		ID   int64  `json:"channel_id"`
		Name string `json:"channel_name"`
	}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if fields.ID != wantID || fields.Name != wantName {
		t.Fatalf("saved channel=(%d,%q), want (%d,%q)", fields.ID, fields.Name, wantID, wantName)
	}
}

func TestRequestHistoryChannelSummaryPreservesFinalTransportBeforeTimelineLimit(t *testing.T) {
	store := createTestSQLiteStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	trace := testRequestTrace("pf_req_channel_full_input", "canonical", "error", time.Now().Add(-time.Second), 7)
	for n := 1; n <= 140; n++ {
		trace.Attempts = append(trace.Attempts, model.RequestAttempt{Number: n, ChannelID: int64(n),
			ChannelName: "渠道" + strconv.Itoa(n), ActualModel: "vendor/variant", Upstream: true, Outcome: "error"})
	}
	trace.Attempts = append(trace.Attempts, model.RequestAttempt{Number: 141, ChannelID: 999, ChannelName: "未发送候选", Outcome: "skipped"})
	trace.AttemptCount, trace.UpstreamAttemptCount = 141, 140
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatal(err)
	}
	rows, count, err := store.ListRequestTraces(ctx, time.Time{}, time.Now(), 20, 0, nil)
	if err != nil || count != 1 || len(rows) != 1 {
		t.Fatalf("summary count=%d rows=%d err=%v", count, len(rows), err)
	}
	requireStoredTraceChannel(t, rows[0], 140, "渠道140")
	if rows[0].Attempts != nil {
		t.Fatal("list returned full attempts")
	}
	detail, err := store.GetRequestTrace(ctx, trace.RequestID, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireStoredTraceChannel(t, detail, 140, "渠道140")
	if !detail.Truncated || len(detail.Attempts) != 128 || trace.Truncated || len(trace.Attempts) != 141 {
		t.Fatal("storage did not preserve an immutable bounded timeline")
	}
}

func TestRequestHistoryChannelSummaryReadsScalarWithoutTimeline(t *testing.T) {
	store := createTestSQLiteStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	trace := testRequestTrace("pf_req_channel_scalar", "canonical", "success", time.Now().Add(-time.Second), 7,
		model.RequestAttempt{Number: 1, ChannelID: 42, ChannelName: "当时的渠道名称", ActualModel: "vendor/variant", Upstream: true, Outcome: "success"})
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatal(err)
	}
	// A corrupted timeline would break a detail read. A new summary must remain
	// readable because its channel observation is stored independently.
	if _, err := store.ExecContext(ctx, "UPDATE request_traces SET attempts_json = 'invalid-json' WHERE request_id = ?", trace.RequestID); err != nil {
		t.Fatal(err)
	}
	rows, _, err := store.ListRequestTraces(ctx, time.Time{}, time.Now(), 20, 0, nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("scalar summary rows=%d err=%v", len(rows), err)
	}
	requireStoredTraceChannel(t, rows[0], 42, "当时的渠道名称")
	if rows[0].ActualModel != "vendor/variant" || rows[0].Attempts != nil {
		t.Fatal("summary lost actual model or exposed a timeline")
	}
}

func TestRequestHistoryLegacyChannelRestoresLastTransportOnly(t *testing.T) {
	store := createTestSQLiteStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	trace := testRequestTrace("pf_req_channel_legacy", "canonical", "error", time.Now().Add(-time.Second), 7,
		model.RequestAttempt{Number: 3, ChannelID: 3, ChannelName: "跳过候选", Outcome: "skipped"},
		model.RequestAttempt{Number: 2, ChannelID: 2, ChannelName: "最后实际渠道", Upstream: true, ActualModel: "vendor/final", Outcome: "error"},
		model.RequestAttempt{Number: 1, ChannelID: 1, ChannelName: "首次渠道", Upstream: true, ActualModel: "vendor/first", Outcome: "error"})
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecContext(ctx, "UPDATE request_traces SET channel_id = 0, channel_name = '' WHERE request_id = ?", trace.RequestID); err != nil {
		t.Fatal(err)
	}
	rows, _, err := store.ListRequestTraces(ctx, time.Time{}, time.Now(), 20, 0, nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("legacy rows=%d err=%v", len(rows), err)
	}
	requireStoredTraceChannel(t, rows[0], 2, "最后实际渠道")
	if rows[0].Attempts != nil {
		t.Fatal("legacy fallback exposed attempts")
	}
	if _, err := store.ExecContext(ctx, "UPDATE request_traces SET truncated = 1 WHERE request_id = ?", trace.RequestID); err != nil {
		t.Fatal(err)
	}
	rows, _, err = store.ListRequestTraces(ctx, time.Time{}, time.Now(), 20, 0, nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("truncated legacy rows=%d err=%v", len(rows), err)
	}
	requireStoredTraceChannel(t, rows[0], 0, "")
	if rows[0].ActualModel != "vendor/final" {
		t.Fatal("a known scalar model disappeared with an unknown legacy channel")
	}
}

func TestRequestHistoryChannelNamesAreBoundedAndNoUpstreamHasNoChannel(t *testing.T) {
	store := createTestSQLiteStore(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	trace := testRequestTrace("pf_req_channel_label", "canonical", "success", time.Now().Add(-time.Second), 7,
		model.RequestAttempt{Number: 1, ChannelID: 42, ChannelName: "渠\x00" + strings.Repeat("渠", 240), Upstream: true, Outcome: "success"})
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetRequestTrace(ctx, trace.RequestID, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireStoredTraceChannel(t, detail, 42, strings.Repeat("渠", 191))
	unsent := testRequestTrace("pf_req_channel_unsent", "canonical", "error", time.Now().Add(-time.Second), 7,
		model.RequestAttempt{Number: 1, ChannelID: 42, ChannelName: "候选渠道", Outcome: "skipped"})
	unsent.UpstreamAttemptCount, unsent.UpstreamSuccessCount = 0, 0
	if err := json.Unmarshal([]byte(`{"channel_id":42,"channel_name":"候选渠道"}`), unsent); err != nil {
		t.Fatal(err)
	}
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{unsent}); err != nil {
		t.Fatal(err)
	}
	detail, err = store.GetRequestTrace(ctx, unsent.RequestID, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireStoredTraceChannel(t, detail, 0, "")
}
