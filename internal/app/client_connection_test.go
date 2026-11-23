package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestClientConnectionPublicProxyProtocols(t *testing.T) {
	cases := []struct {
		protocol, path, output string
		body                   map[string]any
	}{
		{"openai", "/v1/chat/completions", `{"id":"chat-synthetic","choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`, map[string]any{"model": "synthetic-model", "messages": []any{map[string]any{"role": "user", "content": "请只回复 OK。"}}, "stream": false, "max_completion_tokens": 256}},
		{"codex", "/v1/responses", `{"id":"resp-synthetic","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`, map[string]any{"model": "synthetic-model", "input": "请只回复 OK。", "stream": false, "max_output_tokens": 256}},
		{"anthropic", "/v1/messages", `{"id":"msg-synthetic","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}`, map[string]any{"model": "synthetic-model", "messages": []any{map[string]any{"role": "user", "content": "请只回复 OK。"}}, "max_tokens": 256, "stream": false}},
		{"gemini", "/v1beta/models/synthetic-model:generateContent", `{"candidates":[{"content":{"parts":[{"text":"OK"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}`, map[string]any{"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "请只回复 OK。"}}}}, "generationConfig": map[string]any{"maxOutputTokens": 256}}},
	}
	for _, item := range cases {
		t.Run(item.protocol, func(t *testing.T) {
			var calls atomic.Int32
			upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != item.path {
					t.Errorf("upstream path=%q want=%q", r.URL.Path, item.path)
				}
				if r.Header.Get("Authorization") == "Bearer test-api-key" || r.Header.Get("x-goog-api-key") == "test-api-key" {
					t.Error("downstream token leaked to upstream")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, item.output)
			}))
			env := setupProxyTestEnv(t, []testChannel{{name: "synthetic-onboarding", upstreamProtocol: item.protocol, models: "synthetic-model", apiKey: "synthetic-upstream-key"}}, map[int]string{0: upstream.URL})
			headers := map[string]string{}
			if item.protocol == "anthropic" {
				headers["anthropic-version"] = "2023-06-01"
			}
			if item.protocol == "gemini" {
				headers["Authorization"] = ""
				headers["x-goog-api-key"] = "test-api-key"
			}
			response := doProxyRequest(t, env.engine, item.path, item.body, headers)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "OK") || calls.Load() != 1 {
				t.Fatalf("status=%d calls=%d response=%s", response.Code, calls.Load(), response.Body.String())
			}
			id := response.Header().Get(requestIDHeader)
			if !strings.HasPrefix(id, "pf_req_") {
				t.Fatalf("missing public request ID: %q", id)
			}
			trace := awaitRequestTrace(t, env, id)
			if trace.AuthTokenID != 1 || trace.Model != "synthetic-model" || trace.ClientProtocol != item.protocol || trace.Outcome != "success" || trace.UpstreamAttemptCount != 1 {
				t.Fatalf("unexpected actual proxy trace: %+v", trace)
			}
		})
	}
}

func TestClientConnectionPermissionsRejectBeforeUpstream(t *testing.T) {
	var calls atomic.Int32
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"stop","message":{"content":"OK"}}]}`)
	}))
	env := setupProxyTestEnv(t, []testChannel{{name: "synthetic-restrictions", models: "allowed,blocked"}}, map[int]string{0: upstream.URL})
	hash := model.HashToken("test-api-key")
	env.server.authService.authTokensMux.Lock()
	env.server.authService.authTokenModels[hash] = []string{"allowed"}
	env.server.authService.authTokenProtocols[hash] = []string{"openai"}
	env.server.authService.authTokensMux.Unlock()
	for _, item := range []struct {
		path, model, auth string
		status            int
	}{
		{"/v1/chat/completions", "allowed", "Bearer invalid-synthetic-token", 401},
		{"/v1/chat/completions", "blocked", "Bearer test-api-key", 403},
		{"/v1/responses", "allowed", "Bearer test-api-key", 403},
	} {
		response := doProxyRequest(t, env.engine, item.path, map[string]any{"model": item.model, "input": "OK", "messages": []any{map[string]any{"role": "user", "content": "OK"}}}, map[string]string{"Authorization": item.auth})
		if response.Code != item.status {
			t.Fatalf("status=%d want=%d body=%s", response.Code, item.status, response.Body.String())
		}
		if calls.Load() != 0 {
			t.Fatal("a denied downstream request reached upstream")
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/models?protocol=openai", nil)
	req.Header.Set("Authorization", "Bearer test-api-key")
	w := httptest.NewRecorder()
	env.engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "allowed") || strings.Contains(w.Body.String(), "blocked") {
		t.Fatalf("public model list did not enforce token permissions: %s", w.Body.String())
	}
	if calls.Load() != 0 {
		t.Fatal("local model metadata reached upstream")
	}
}
