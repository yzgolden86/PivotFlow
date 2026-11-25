package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/cooldown"
	"github.com/yzgolden86/PivotFlow/internal/model"
)

func traceChannelFields(t testing.TB, value any) (int64, string) {
	t.Helper()
	encoded, err := json.Marshal(value)
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
	return fields.ID, fields.Name
}

func TestRequestTraceChannelSurvivesTruncationAndSkippedCandidates(t *testing.T) {
	trace := newRequestTrace(time.Now(), 7, "http")
	trace.setRequest("deepseek-v4.1-flash", "openai", false)
	for n := 1; n <= 140; n++ {
		cfg := &model.Config{ID: int64(n), Name: "渠道" + strconv.Itoa(n)}
		trace.begin(cfg, 0, "https://example.invalid", "vendor/variant", "openai")
		markTraceUpstream(trace.attemptContext(context.Background()), "http")
		trace.finishAttempt(&proxyResult{status: 502}, cooldown.Decision{}, nil, nil)
	}
	trace.skipped(&model.Config{ID: 999, Name: "未发送候选"}, 0, "https://example.invalid", "channel_concurrency_limit", 429)
	result := trace.snapshot(503, false)
	if result.AttemptCount != 141 || len(result.Attempts) != 128 || !result.Truncated {
		t.Fatal("the trace timeline is not bounded")
	}
	if id, name := traceChannelFields(t, result); id != 140 || name != "渠道140" {
		t.Fatalf("last actual channel=(%d,%q), want (140,渠道140)", id, name)
	}
}

func TestRequestTraceDoesNotInventChannelBeforeUpstream(t *testing.T) {
	trace := newRequestTrace(time.Now(), 7, "http")
	trace.begin(&model.Config{ID: 42, Name: "未发送候选"}, 0, "https://example.invalid", "vendor/variant", "openai")
	trace.finishAttempt(nil, cooldown.Decision{}, nil, ErrChannelRPMExceeded)
	if id, name := traceChannelFields(t, trace.snapshot(503, false)); id != 0 || name != "" {
		t.Fatalf("unsent candidate became the actual channel=(%d,%q)", id, name)
	}
}

func TestRequestTraceAdminSummaryIncludesChannelWithoutAttempts(t *testing.T) {
	trace := sensitiveRequestTraceForAPI()
	server := &Server{store: &requestTraceAPIStore{traces: []*model.RequestTrace{trace}}}
	c, recorder := newTestContext(t, newRequest(http.MethodGet, "/admin/request-traces", nil))
	server.HandleRequestTraces(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("summary status=%d", recorder.Code)
	}
	response := mustParseAPIResponse[[]map[string]any](t, recorder.Body.Bytes())
	if len(response.Data) != 1 {
		t.Fatalf("summary count=%d", len(response.Data))
	}
	if id, name := traceChannelFields(t, response.Data[0]); id != 987 || name != "private-channel-name" {
		t.Fatalf("admin summary channel=(%d,%q), want recorded channel", id, name)
	}
	if _, present := response.Data[0]["attempts"]; present {
		t.Fatal("summary included the large attempt timeline")
	}
}

func TestRequestTraceTokenProjectionHidesScalarChannel(t *testing.T) {
	trace := sensitiveRequestTraceForAPI()
	if err := json.Unmarshal([]byte(`{"channel_id":987,"channel_name":"private-channel-name","actual_model":"private-upstream-model"}`), trace); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{projectRequestTraceSummary(trace, false), projectTokenRequestTrace(trace)} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"channel_id", "channel_name", "actual_model"} {
			if _, present := fields[key]; present {
				t.Errorf("token projection exposed scalar %s", key)
			}
		}
	}
}
