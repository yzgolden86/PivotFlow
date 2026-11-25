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

func traceActualModelJSON(t testing.TB, trace *model.RequestTrace) string {
	t.Helper()
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

func TestRequestTraceActualModelSurvivesTruncationAndSkippedCandidates(t *testing.T) {
	trace := newRequestTrace(time.Now(), 1, "http")
	trace.setRequest("deepseek-v4.1-flash", "openai", false)
	cfg := &model.Config{ID: 5, Name: "synthetic"}
	for i := 1; i <= 140; i++ {
		trace.begin(cfg, 0, "https://example.invalid", "deepseek-ai/DeepSeek-V4.1-Flash-"+strconv.Itoa(i), "openai")
		markTraceUpstream(trace.attemptContext(context.Background()), "http")
		trace.finishAttempt(&proxyResult{status: 502}, cooldown.Decision{}, nil, nil)
	}
	trace.skipped(cfg, 0, "https://example.invalid", "channel_concurrency_limit", 429)
	result := trace.snapshot(503, false)
	if result.AttemptCount != 141 || !result.Truncated || len(result.Attempts) != 128 {
		t.Fatalf("unexpected bounded timeline: %+v", result)
	}
	if actual := traceActualModelJSON(t, result); actual != "deepseek-ai/DeepSeek-V4.1-Flash-140" {
		t.Fatalf("last executed upstream model=%q; a later skipped candidate must not replace the 140th transmitted model", actual)
	}
}

func TestRequestTraceDoesNotInventActualModelBeforeUpstream(t *testing.T) {
	trace := newRequestTrace(time.Now(), 1, "http")
	trace.setRequest("deepseek-v4.1-flash", "openai", false)
	trace.begin(&model.Config{ID: 5}, 0, "https://example.invalid", "candidate-never-sent", "openai")
	trace.finishAttempt(nil, cooldown.Decision{}, nil, ErrChannelRPMExceeded)
	if actual := traceActualModelJSON(t, trace.snapshot(503, false)); actual != "" {
		t.Fatalf("unsent candidate became actual upstream model %q", actual)
	}
}

func TestRequestTraceAdminSummaryIncludesActualModelWithoutAttempts(t *testing.T) {
	trace := sensitiveRequestTraceForAPI()
	server := &Server{store: &requestTraceAPIStore{traces: []*model.RequestTrace{trace}, detail: trace}}
	c, recorder := newTestContext(t, newRequest(http.MethodGet, "/admin/request-traces", nil))
	server.HandleRequestTraces(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("summary status=%d", recorder.Code)
	}
	payload := mustParseAPIResponse[[]map[string]any](t, recorder.Body.Bytes())
	if len(payload.Data) != 1 {
		t.Fatalf("summary count=%d", len(payload.Data))
		return
	}
	if actual := payload.Data[0]["actual_model"]; actual != "private-upstream-model" {
		t.Fatalf("admin summary actual_model=%v, want the recorded upstream variant", actual)
	}
	if _, present := payload.Data[0]["attempts"]; present {
		t.Fatal("summary should not include the attempt timeline")
	}
	if payload.Data[0]["model"] != "requested-model" {
		t.Fatalf("canonical request model was replaced: %v", payload.Data[0]["model"])
	}
}
