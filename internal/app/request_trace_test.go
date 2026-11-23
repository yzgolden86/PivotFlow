package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/cooldown"
	"github.com/yzgolden86/PivotFlow/internal/model"
)

func awaitRequestTrace(t testing.TB, env *proxyTestEnv, id string) *model.RequestTrace {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		trace, err := env.store.GetRequestTrace(context.Background(), id, nil)
		if err != nil {
			t.Fatalf("read trace: %v", err)
		}
		if trace != nil {
			return trace
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("request trace %q was not persisted", id)
	return nil
}

func TestRequestTraceFailoverAndUnspoofableIdentity(t *testing.T) {
	primary := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"secret-upstream-error-body"}}`)
	}))
	fallback := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-PivotFlow-Request-Id", "upstream-forged")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	env := setupProxyTestEnv(t, []testChannel{
		{name: "primary", models: "gpt-test", apiKey: "sk-primary-secret", priority: 100},
		{name: "fallback", models: "gpt-test", apiKey: "sk-fallback-secret", priority: 90},
	}, map[int]string{0: primary.URL, 1: fallback.URL})
	response := doProxyRequest(t, env.engine, "/v1/chat/completions", map[string]any{
		"model": "gpt-test", "messages": []any{map[string]any{"role": "user", "content": "request-secret-prompt"}},
	}, map[string]string{"X-PivotFlow-Request-Id": "client-forged"})
	if response.Code != http.StatusOK {
		t.Fatalf("status %d: %s", response.Code, response.Body.String())
	}
	id := response.Header().Get("X-PivotFlow-Request-Id")
	if !strings.HasPrefix(id, "pf_req_") || len(id) != 39 || len(response.Header().Values("X-PivotFlow-Request-Id")) != 1 {
		t.Fatalf("invalid gateway identity: %q", response.Header().Values("X-PivotFlow-Request-Id"))
	}
	trace := awaitRequestTrace(t, env, id)
	if trace.Outcome != "success" || trace.FinalStatus != 200 || trace.UpstreamAttemptCount != 2 || trace.UpstreamSuccessCount != 1 || len(trace.Attempts) != 2 {
		t.Fatalf("incorrect failover summary: %+v", trace)
	}
	if trace.Attempts[0].StatusCode != 503 || trace.Attempts[1].StatusCode != 200 || trace.Attempts[0].Number != 1 || trace.Attempts[1].Number != 2 {
		t.Fatalf("incorrect attempt sequence: %+v", trace.Attempts)
	}
	encoded, _ := json.Marshal(trace)
	for _, secret := range []string{"sk-primary-secret", "sk-fallback-secret", "secret-upstream-error-body", "request-secret-prompt"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("trace contains secret %q", secret)
		}
	}
}

func TestRequestTraceTruncatedStreamIsNotClientSuccess(t *testing.T) {
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
		w.(http.Flusher).Flush()
	}))
	env := setupProxyTestEnv(t, []testChannel{{name: "partial", upstreamProtocol: "codex", models: "gpt-test"}}, map[int]string{0: upstream.URL})
	response := doProxyRequest(t, env.engine, "/v1/responses", map[string]any{"model": "gpt-test", "stream": true, "input": "hello"}, nil)
	trace := awaitRequestTrace(t, env, response.Header().Get("X-PivotFlow-Request-Id"))
	if response.Code != 200 || trace.Outcome != "error" || trace.FinalStatus != 599 || trace.UpstreamSuccessCount != 0 {
		t.Fatalf("partial output incorrectly counted: HTTP=%d trace=%+v", response.Code, trace)
	}
	if len(trace.Attempts) != 1 || !trace.Attempts[0].ResponseCommitted {
		t.Fatalf("partial output boundary missing: %+v", trace.Attempts)
	}
}

func TestRequestTraceRejectedBeforeUpstream(t *testing.T) {
	env := setupProxyTestEnv(t, nil, nil)
	response := doProxyRequest(t, env.engine, "/v1/chat/completions", map[string]any{"model": "unknown"}, nil)
	trace := awaitRequestTrace(t, env, response.Header().Get("X-PivotFlow-Request-Id"))
	if trace.FinalStatus != 503 || trace.Outcome != "error" || trace.UpstreamAttemptCount != 0 || trace.Model != "unknown" {
		t.Fatalf("local rejection trace: %+v", trace)
	}
}

func TestRequestTraceProtocolNegotiationAndLogAssociation(t *testing.T) {
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/chat/completions" {
			w.WriteHeader(405)
			_, _ = io.WriteString(w, `{"error":"not implemented"}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"msg_ok","type":"message","role":"assistant","model":"gpt-test","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	env := setupProxyTestEnv(t, []testChannel{{name: "negotiated", protocolTransformMode: "auto", models: "gpt-test"}}, map[int]string{0: upstream.URL})
	response := doProxyRequest(t, env.engine, "/v1/chat/completions", map[string]any{
		"model": "gpt-test", "messages": []any{map[string]any{"role": "user", "content": "hello"}},
	}, nil)
	trace := awaitRequestTrace(t, env, response.Header().Get(requestIDHeader))
	if trace.Outcome != "success" || len(trace.Attempts) != 2 || trace.Attempts[0].Outcome != "capability" ||
		trace.Attempts[0].Cooldown != "none" || trace.Attempts[1].UpstreamProtocol != "anthropic" {
		t.Fatalf("negotiation lost or misclassified: %+v", trace)
	}
	requireNoReliabilityCooldowns(t, env)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		logs, err := env.store.ListLogs(context.Background(), time.Now().Add(-time.Hour), 10, 0, &model.LogFilter{RequestID: trace.RequestID})
		if err != nil {
			t.Fatal(err)
		}
		if len(logs) > 0 {
			if len(logs) != 1 || logs[0].AttemptNumber != 2 || logs[0].RequestID != trace.RequestID {
				t.Fatalf("log association mismatch: %+v", logs)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("associated log not persisted")
}

func TestRequestTraceClientCancellationAndWaiting(t *testing.T) {
	env := setupProxyTestEnv(t, nil, nil)
	for len(env.server.concurrencySem) < cap(env.server.concurrencySem) {
		env.server.concurrencySem <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"unused"}`)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer test-api-key")
	req.Header.Set("Content-Type", "application/json")
	timer := time.AfterFunc(30*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()
	response := httptest.NewRecorder()
	env.engine.ServeHTTP(response, req)
	trace := awaitRequestTrace(t, env, response.Header().Get(requestIDHeader))
	if trace.Outcome != "canceled" || trace.FinalStatus != 499 || trace.UpstreamAttemptCount != 0 || trace.WaitDuration < .01 {
		t.Fatalf("waiting/cancel trace: %+v", trace)
	}
}

func TestRequestTraceBoundedTimelinePreservesCounts(t *testing.T) {
	trace := newRequestTrace(time.Now(), 1, "http")
	cfg := &model.Config{ID: 5, Name: "limited"}
	for i := 0; i < 140; i++ {
		trace.begin(cfg, 0, "https://user:password@example.test/path/secret?api_key=hidden", "gpt-test", "openai")
		ctx := trace.attemptContext(context.Background())
		markTraceUpstream(ctx, "http")
		trace.finishAttempt(&proxyResult{status: 200, succeeded: true}, cooldown.Decision{}, nil, nil)
	}
	result := trace.snapshot(200, false)
	if len(result.Attempts) != 128 || !result.Truncated || result.AttemptCount != 140 || result.UpstreamAttemptCount != 140 || result.UpstreamSuccessCount != 140 {
		t.Fatalf("unbounded or incorrect counts: %+v", result)
	}
	if result.Attempts[0].Endpoint != "https://example.test" {
		t.Fatalf("endpoint was not reduced to origin: %q", result.Attempts[0].Endpoint)
	}
}

func TestRequestTraceLateReconnectCannotMutateNextAttempt(t *testing.T) {
	trace := newRequestTrace(time.Now(), 1, "http")
	cfg := &model.Config{ID: 1}
	trace.begin(cfg, 0, "https://a.test", "model", "codex")
	stale := trace.attemptContext(context.Background())
	markTraceUpstream(stale, "websocket")
	trace.finishAttempt(&proxyResult{status: 502}, cooldown.Decision{}, nil, nil)
	trace.begin(cfg, 1, "https://b.test", "model", "codex")
	retryTraceFromContext(stale, 502, "error", "websocket_reconnect")
	markTraceUpstream(stale, "websocket")
	trace.finishAttempt(nil, cooldown.Decision{}, nil, ErrChannelRPMExceeded)
	result := trace.snapshot(503, false)
	if len(result.Attempts) != 2 || result.Attempts[1].Upstream || result.UpstreamAttemptCount != 1 {
		t.Fatalf("late reader modified the next attempt: %+v", result)
	}
}

func TestRequestTraceCompatibilityRetryIncludesBothAttempts(t *testing.T) {
	var calls atomic.Int32
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(422)
			_, _ = io.WriteString(w, `{"error":{"message":"unknown parameter: stream_options.include_usage"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	env := setupProxyTestEnv(t, []testChannel{{name: "compatibility", models: "gpt-test"}}, map[int]string{0: upstream.URL})
	response := doProxyRequest(t, env.engine, "/v1/chat/completions", map[string]any{
		"model": "gpt-test", "stream": true, "messages": []any{map[string]any{"role": "user", "content": "hello"}},
	}, nil)
	trace := awaitRequestTrace(t, env, response.Header().Get(requestIDHeader))
	if calls.Load() != 2 || trace.UpstreamAttemptCount != 2 || len(trace.Attempts) != 2 || trace.UpstreamSuccessCount != 1 ||
		trace.Attempts[0].Reason != "stream_options_unsupported" || trace.Attempts[0].Retry != "compatibility" ||
		trace.Attempts[1].Retry != "none" || trace.Outcome != "success" {
		t.Fatalf("compatibility attempts lost: %+v", trace)
	}
	requireNoReliabilityCooldowns(t, env)
}

func TestRequestTraceKeyRetryAndClientCancellation(t *testing.T) {
	t.Run("401 then alternate key", func(t *testing.T) {
		var calls atomic.Int32
		upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if calls.Add(1) == 1 {
				w.WriteHeader(401)
				_, _ = io.WriteString(w, `{"error":{"message":"invalid api key","type":"authentication_error"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
		}))
		env := setupProxyTestEnv(t, []testChannel{{name: "keys", models: "gpt-test", apiKey: "sk-first"}}, map[int]string{0: upstream.URL})
		id := reliabilityChannelIDByName(t, env, "keys")
		if err := env.store.CreateAPIKeysBatch(context.Background(), []*model.APIKey{{ChannelID: id, KeyIndex: 1, APIKey: "sk-second"}}); err != nil {
			t.Fatal(err)
		}
		env.server.InvalidateAPIKeysCache(id)
		response := doProxyRequest(t, env.engine, "/v1/chat/completions", map[string]any{"model": "gpt-test", "messages": []any{}}, nil)
		trace := awaitRequestTrace(t, env, response.Header().Get(requestIDHeader))
		if len(trace.Attempts) != 2 || trace.Attempts[0].Retry != "next_key" || trace.Attempts[0].Cooldown != "key" ||
			trace.Attempts[0].KeyIndex == trace.Attempts[1].KeyIndex || trace.Outcome != "success" {
			t.Fatalf("key retry metadata: %+v", trace)
		}
	})
	t.Run("cancel on upstream", func(t *testing.T) {
		started := make(chan struct{})
		upstream := newTestHTTPServer(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
		}))
		env := setupProxyTestEnv(t, []testChannel{{name: "cancel", models: "gpt-test"}}, map[int]string{0: upstream.URL})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			select {
			case <-started:
			case <-ctx.Done():
			}
			cancel()
		}()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test","messages":[]}`)).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer test-api-key")
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		env.engine.ServeHTTP(response, req)
		trace := awaitRequestTrace(t, env, response.Header().Get(requestIDHeader))
		if trace.Outcome != "canceled" || trace.UpstreamAttemptCount != 1 || trace.UpstreamSuccessCount != 0 ||
			len(trace.Attempts) != 1 || trace.Attempts[0].Outcome != "canceled" || trace.Attempts[0].Retry != "none" {
			t.Fatalf("canceled attempt: %+v", trace)
		}
		requireNoReliabilityCooldowns(t, env)
	})
}
