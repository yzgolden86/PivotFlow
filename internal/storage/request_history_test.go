package storage

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

type requestTraceGetter interface {
	GetRequestTrace(ctx context.Context, id string, authTokenID *int64) (*model.RequestTrace, error)
}

func requireRequestTraceMissing(t testing.TB, store requestTraceGetter, ctx context.Context, id string, authTokenID *int64) {
	t.Helper()
	trace, err := store.GetRequestTrace(ctx, id, authTokenID)
	if err != nil {
		t.Fatalf("GetRequestTrace(%q) error=%v, want nil", id, err)
	}
	if trace != nil {
		t.Fatalf("GetRequestTrace(%q)=%+v, want nil", id, trace)
	}
}

func testRequestTrace(id, modelName, outcome string, startedAt time.Time, authTokenID int64, attempts ...model.RequestAttempt) *model.RequestTrace {
	return &model.RequestTrace{
		RequestID:            id,
		StartedAt:            startedAt.UnixMilli(),
		AuthTokenID:          authTokenID,
		Model:                modelName,
		ClientProtocol:       "openai",
		Transport:            "http",
		IsStreaming:          true,
		FinalStatus:          200,
		Outcome:              outcome,
		Duration:             1.5,
		WaitDuration:         0.1,
		AttemptCount:         len(attempts),
		UpstreamAttemptCount: 2,
		UpstreamSuccessCount: 1,
		Attempts:             attempts,
	}
}

func TestRequestHistory_RequestTraceRoundTripIdempotenceFiltersAndStats(t *testing.T) {
	store := createTestSQLiteStore(t)
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	successTrace := testRequestTrace("pf_req_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "model-needle-a", "success", now.Add(-3*time.Minute), 7,
		model.RequestAttempt{
			Number:            1,
			StartedAt:         now.Add(-3 * time.Minute).UnixMilli(),
			Duration:          0.5,
			FirstByteTime:     0.1,
			ChannelID:         11,
			ChannelName:       "primary",
			KeyIndex:          0,
			URLIndex:          1,
			Endpoint:          "https://user:secret@api.example.com/v1/messages?token=secret",
			ActualModel:       "model-needle-upstream",
			UpstreamProtocol:  "anthropic",
			Transport:         "http",
			StatusCode:        200,
			Outcome:           "success",
			Upstream:          true,
			ResponseCommitted: true,
			Retry:             "none",
			Cooldown:          "none",
		},
	)
	errorTrace := testRequestTrace("pf_req_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "other-model", "error", now.Add(-2*time.Minute), 7,
		model.RequestAttempt{Number: 1, Outcome: "error", StatusCode: 500},
	)
	canceledTrace := testRequestTrace("pf_req_cccccccccccccccccccccccccccccccc", "model-needle-c", "canceled", now.Add(-time.Minute), 8,
		model.RequestAttempt{Number: 1, Outcome: "canceled", StatusCode: 499},
	)

	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{successTrace, errorTrace, canceledTrace}); err != nil {
		t.Fatalf("BatchAddRequestTraces: %v", err)
	}

	originalID := successTrace.RequestID
	originalModel := successTrace.Model
	successTrace.Model = "changed"
	successTrace.Outcome = "error"
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{successTrace}); err != nil {
		t.Fatalf("idempotent BatchAddRequestTraces: %v", err)
	}

	got, err := store.GetRequestTrace(ctx, originalID, nil)
	if err != nil {
		t.Fatalf("GetRequestTrace: %v", err)
	}
	if got == nil {
		t.Fatal("GetRequestTrace returned nil for committed trace")
	}
	if got.Model != originalModel || got.Outcome != "success" {
		t.Fatalf("duplicate insert replaced immutable trace: %+v", got)
	}
	if len(got.Attempts) != 1 {
		t.Fatalf("GetRequestTrace attempts=%d, want 1", len(got.Attempts))
	}
	if got.Attempts[0].Endpoint != "https://api.example.com" {
		t.Fatalf("stored endpoint=%q, want origin only", got.Attempts[0].Endpoint)
	}
	requireRequestTraceMissing(t, store, ctx, originalID, int64Pointer(8))

	traces, total, err := store.ListRequestTraces(
		ctx,
		now.Add(-5*time.Minute),
		now,
		10,
		0,
		&model.RequestTraceFilter{AuthTokenID: int64Pointer(7), Search: "needle"},
	)
	if err != nil {
		t.Fatalf("ListRequestTraces: %v", err)
	}
	if total != 1 || len(traces) != 1 || traces[0].RequestID != originalID {
		t.Fatalf("filtered traces total=%d traces=%+v, want only %s", total, traces, originalID)
	}
	if traces[0].Attempts != nil {
		t.Fatalf("list unexpectedly returned attempts: %+v", traces[0].Attempts)
	}

	stats, err := store.GetRequestTraceStats(
		ctx,
		now.Add(-5*time.Minute),
		now,
		&model.RequestTraceFilter{AuthTokenID: int64Pointer(7)},
	)
	if err != nil {
		t.Fatalf("GetRequestTraceStats: %v", err)
	}
	if stats.Requests != 2 || stats.Successes != 1 || stats.Errors != 1 || stats.Canceled != 0 {
		t.Fatalf("token stats=%+v, want requests=2 successes=1 errors=1 canceled=0", stats)
	}
	if stats.UpstreamAttempts != 4 || stats.UpstreamSuccesses != 2 {
		t.Fatalf("token upstream stats=%+v, want attempts=4 successes=2", stats)
	}

	if err := store.AddLog(ctx, &model.LogEntry{
		Time:       model.JSONTime{Time: now},
		Model:      "probe",
		LogSource:  model.LogSourceManualTest,
		StatusCode: 503,
	}); err != nil {
		t.Fatalf("seed probe log: %v", err)
	}
	statsWithProbe, err := store.GetRequestTraceStats(
		ctx,
		now.Add(-5*time.Minute),
		now,
		&model.RequestTraceFilter{AuthTokenID: int64Pointer(7)},
	)
	if err != nil {
		t.Fatalf("GetRequestTraceStats after probe log: %v", err)
	}
	if *statsWithProbe != *stats {
		t.Fatalf("probe log changed request stats: before=%+v after=%+v", stats, statsWithProbe)
	}
}

func TestRequestHistory_BatchValidationDoesNotPartiallyInsert(t *testing.T) {
	store := createTestSQLiteStore(t)
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	valid := testRequestTrace("pf_req_valid_batch", "valid", "success", now, 0)
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{
		valid,
		{StartedAt: now.UnixMilli(), Outcome: "success"},
	}); err == nil {
		t.Fatal("BatchAddRequestTraces accepted an empty request ID")
	}
	requireRequestTraceMissing(t, store, ctx, valid.RequestID, nil)
}

func TestRequestHistory_ValidationRejectsPoisonRowsBeforeInsertingAnyTrace(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	tests := []struct {
		name   string
		mutate func(*model.RequestTrace)
	}{
		{
			name: "request id too long",
			mutate: func(trace *model.RequestTrace) {
				trace.RequestID = strings.Repeat("r", 65)
			},
		},
		{
			name: "invalid trace transport",
			mutate: func(trace *model.RequestTrace) {
				trace.Transport = "pigeon"
			},
		},
		{
			name: "invalid client protocol",
			mutate: func(trace *model.RequestTrace) {
				trace.ClientProtocol = "soap"
			},
		},
		{
			name: "invalid trace outcome",
			mutate: func(trace *model.RequestTrace) {
				trace.Outcome = "maybe"
			},
		},
		{
			name: "non-finite duration",
			mutate: func(trace *model.RequestTrace) {
				trace.Duration = math.NaN()
			},
		},
		{
			name: "infinite wait duration",
			mutate: func(trace *model.RequestTrace) {
				trace.WaitDuration = math.Inf(1)
			},
		},
		{
			name: "negative attempt count",
			mutate: func(trace *model.RequestTrace) {
				trace.AttemptCount = -1
			},
		},
		{
			name: "attempt count overflow",
			mutate: func(trace *model.RequestTrace) {
				trace.AttemptCount = 5_000_000
			},
		},
		{
			name: "negative upstream count",
			mutate: func(trace *model.RequestTrace) {
				trace.UpstreamAttemptCount = -1
			},
		},
		{
			name: "upstream success count overflow",
			mutate: func(trace *model.RequestTrace) {
				trace.UpstreamSuccessCount = 5_000_000
			},
		},
		{
			name: "invalid attempt transport",
			mutate: func(trace *model.RequestTrace) {
				trace.Attempts[0].Transport = "udp"
			},
		},
		{
			name: "attempt number overflow",
			mutate: func(trace *model.RequestTrace) {
				trace.Attempts[0].Number = 5_000_000
			},
		},
		{
			name: "invalid upstream protocol",
			mutate: func(trace *model.RequestTrace) {
				trace.Attempts[0].UpstreamProtocol = "grpc"
			},
		},
		{
			name: "invalid attempt outcome",
			mutate: func(trace *model.RequestTrace) {
				trace.Attempts[0].Outcome = "maybe"
			},
		},
		{
			name: "non-finite attempt duration",
			mutate: func(trace *model.RequestTrace) {
				trace.Attempts[0].Duration = math.Inf(-1)
			},
		},
		{
			name: "non-finite first byte time",
			mutate: func(trace *model.RequestTrace) {
				trace.Attempts[0].FirstByteTime = math.NaN()
			},
		},
		{
			name: "invalid retry code",
			mutate: func(trace *model.RequestTrace) {
				trace.Attempts[0].Retry = "try something else"
			},
		},
		{
			name: "invalid cooldown code",
			mutate: func(trace *model.RequestTrace) {
				trace.Attempts[0].Cooldown = "forever"
			},
		},
		{
			name: "raw reason text",
			mutate: func(trace *model.RequestTrace) {
				trace.Attempts[0].Reason = "upstream said: secret response body"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := createTestSQLiteStore(t)
			defer func() { _ = store.Close() }()
			ctx := context.Background()

			valid := testRequestTrace("pf_req_poison_valid", "valid", "success", now, 0)
			poison := testRequestTrace("pf_req_poison_invalid", "invalid", "error", now, 0,
				model.RequestAttempt{
					Number:           1,
					StartedAt:        now.UnixMilli(),
					Duration:         0.2,
					ChannelID:        1,
					ChannelName:      "channel",
					KeyIndex:         0,
					URLIndex:         0,
					Endpoint:         "https://api.example.com",
					ActualModel:      "actual",
					UpstreamProtocol: "openai",
					Transport:        "http",
					StatusCode:       500,
					Outcome:          "error",
					Upstream:         true,
					Retry:            "none",
					Cooldown:         "none",
				},
			)
			test.mutate(poison)

			if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{valid, poison}); err == nil {
				t.Fatalf("BatchAddRequestTraces accepted malformed trace: %+v", poison)
			}
			requireRequestTraceMissing(t, store, ctx, valid.RequestID, nil)
			requireRequestTraceMissing(t, store, ctx, poison.RequestID, nil)
		})
	}
}

func TestRequestHistory_TruncatesLabelsToSchema191(t *testing.T) {
	store := createTestSQLiteStore(t)
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	longModel := "m\x00" + strings.Repeat("m", 255)
	longChannel := "c\x00" + strings.Repeat("c", 255)
	longActual := "a\x00" + strings.Repeat("a", 255)
	trace := testRequestTrace("pf_req_long_labels", longModel, "success", now, 0,
		model.RequestAttempt{
			Number:           1,
			StartedAt:        now.UnixMilli(),
			Duration:         0.2,
			ChannelID:        1,
			ChannelName:      longChannel,
			KeyIndex:         0,
			URLIndex:         0,
			Endpoint:         "https://api.example.com/v1/messages?secret=redacted",
			ActualModel:      longActual,
			UpstreamProtocol: "openai",
			Transport:        "http",
			StatusCode:       200,
			Outcome:          "success",
			Upstream:         true,
			Retry:            "none",
			Cooldown:         "none",
		},
	)
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatalf("BatchAddRequestTraces long labels: %v", err)
	}

	got, err := store.GetRequestTrace(ctx, trace.RequestID, nil)
	if err != nil {
		t.Fatalf("GetRequestTrace: %v", err)
	}
	if got == nil {
		t.Fatal("GetRequestTrace returned nil")
	}
	if len([]rune(got.Model)) != 191 || got.Model != strings.Repeat("m", 191) {
		t.Fatalf("stored model length=%d, want truncated to 191", len([]rune(got.Model)))
	}
	if strings.ContainsRune(got.Model, '\x00') {
		t.Fatalf("stored model retained NUL: %q", got.Model)
	}
	attempt := got.Attempts[0]
	if len([]rune(attempt.ChannelName)) != 191 || attempt.ChannelName != strings.Repeat("c", 191) {
		t.Fatalf("stored channel name length=%d, want truncated to 191", len([]rune(attempt.ChannelName)))
	}
	if strings.ContainsRune(attempt.ChannelName, '\x00') {
		t.Fatalf("stored channel name retained NUL: %q", attempt.ChannelName)
	}
	if len([]rune(attempt.ActualModel)) != 191 || attempt.ActualModel != strings.Repeat("a", 191) {
		t.Fatalf("stored actual model length=%d, want truncated to 191", len([]rune(attempt.ActualModel)))
	}
	if strings.ContainsRune(attempt.ActualModel, '\x00') {
		t.Fatalf("stored actual model retained NUL: %q", attempt.ActualModel)
	}
	if attempt.Endpoint != "https://api.example.com" {
		t.Fatalf("stored endpoint=%q, want origin only", attempt.Endpoint)
	}
}

func TestRequestHistory_TruncatesAttemptTimelineWithoutChangingCounters(t *testing.T) {
	store := createTestSQLiteStore(t)
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	attempts := make([]model.RequestAttempt, 130)
	for i := range attempts {
		attempts[i] = model.RequestAttempt{Number: i + 1, Outcome: "skipped"}
	}
	trace := testRequestTrace("pf_req_dddddddddddddddddddddddddddddddd", "truncated-model", "success", now, 0, attempts...)
	trace.AttemptCount = 130
	trace.UpstreamAttemptCount = 17
	trace.UpstreamSuccessCount = 3
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatalf("BatchAddRequestTraces: %v", err)
	}

	got, err := store.GetRequestTrace(ctx, trace.RequestID, nil)
	if err != nil {
		t.Fatalf("GetRequestTrace: %v", err)
	}
	if got == nil {
		t.Fatal("GetRequestTrace returned nil for truncated trace")
	}
	if len(got.Attempts) != 128 || !got.Truncated {
		t.Fatalf("attempts=%d truncated=%v, want 128/true", len(got.Attempts), got.Truncated)
	}
	if got.AttemptCount != 130 || got.UpstreamAttemptCount != 17 || got.UpstreamSuccessCount != 3 {
		t.Fatalf("truncation changed summary counters: %+v", got)
	}
}

func TestRequestHistory_CleanupLogsBeforeAlsoPrunesRequestTraces(t *testing.T) {
	store := createTestSQLiteStore(t)
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	cutoff := now.Add(-time.Hour)

	oldTrace := testRequestTrace("pf_req_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "old", "success", cutoff.Add(-time.Minute), 0)
	newTrace := testRequestTrace("pf_req_ffffffffffffffffffffffffffffffff", "new", "success", cutoff.Add(time.Minute), 0)
	if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{oldTrace, newTrace}); err != nil {
		t.Fatalf("seed traces: %v", err)
	}
	if err := store.CleanupLogsBefore(ctx, cutoff); err != nil {
		t.Fatalf("CleanupLogsBefore: %v", err)
	}
	requireRequestTraceMissing(t, store, ctx, oldTrace.RequestID, nil)
	if trace, err := store.GetRequestTrace(ctx, newTrace.RequestID, nil); err != nil || trace == nil {
		t.Fatalf("new trace was removed by cleanup: trace=%+v err=%v", trace, err)
	}
}

func TestRequestHistory_LogsPropagateCorrelationFields(t *testing.T) {
	store := createTestSQLiteStore(t)
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	requestID := "pf_req_11111111111111111111111111111111"

	if err := store.BatchAddLogs(ctx, []*model.LogEntry{
		{Time: model.JSONTime{Time: now.Add(-time.Second)}, Model: "gpt-5", StatusCode: 401, RequestID: requestID, AttemptNumber: 1},
		{Time: model.JSONTime{Time: now}, Model: "gpt-5", StatusCode: 200, RequestID: requestID, AttemptNumber: 2},
	}); err != nil {
		t.Fatalf("BatchAddLogs: %v", err)
	}
	if err := store.AddLog(ctx, &model.LogEntry{
		Time:          model.JSONTime{Time: now.Add(time.Second)},
		Model:         "gpt-5",
		StatusCode:    503,
		RequestID:     "pf_req_22222222222222222222222222222222",
		AttemptNumber: 1,
	}); err != nil {
		t.Fatalf("AddLog: %v", err)
	}

	logs, err := store.ListLogsRange(ctx, now.Add(-time.Minute), now.Add(time.Minute), 10, 0, &model.LogFilter{RequestID: requestID})
	if err != nil {
		t.Fatalf("ListLogsRange by RequestID: %v", err)
	}
	if len(logs) != 2 || logs[0].AttemptNumber != 2 || logs[1].AttemptNumber != 1 {
		t.Fatalf("correlated logs=%+v, want attempt numbers 2,1", logs)
	}
	for _, entry := range logs {
		if entry.RequestID != requestID {
			t.Fatalf("request_id=%q, want %q", entry.RequestID, requestID)
		}
	}

	searched, err := store.ListLogsRange(ctx, now.Add(-time.Minute), now.Add(time.Minute), 10, 0, &model.LogFilter{Search: requestID})
	if err != nil {
		t.Fatalf("ListLogsRange by Search: %v", err)
	}
	if len(searched) != 2 {
		t.Fatalf("search returned %d logs, want 2", len(searched))
	}
}

func int64Pointer(value int64) *int64 {
	return &value
}
