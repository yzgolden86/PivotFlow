package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/protocol"
)

func TestRequestCachePolicyDoesNotSwallowToolsSessionsOrUnknownSemantics(t *testing.T) {
	chat := `{"model":"gpt-4o","temperature":0,"messages":[{"role":"user","content":"hello"}]}`
	cases := []struct {
		name, path, body, header, value, mode, reason string
		client                                        protocol.Protocol
	}{
		{"text chat", "/v1/chat/completions", chat, "X-PivotFlow-Cache", "use", "use", "", protocol.OpenAI},
		{"text messages", "/v1/messages", `{"model":"claude","temperature":0,"max_tokens":16,"system":[{"type":"text","text":"brief"}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`, "X-PivotFlow-Cache", "use", "use", "", protocol.Anthropic},
		{"missing opt in", "/v1/chat/completions", chat, "", "", "bypass", "not_opted_in", protocol.OpenAI},
		{"client bypass", "/v1/chat/completions", chat, "X-PivotFlow-Cache", "bypass", "bypass", "client_bypass", protocol.OpenAI},
		{"unknown mode", "/v1/chat/completions", chat, "X-PivotFlow-Cache", "sometimes", "bypass", "invalid_mode", protocol.OpenAI},
		{"tool definitions", "/v1/chat/completions", strings.TrimSuffix(chat, "}") + `,"tools":[]}`, "X-PivotFlow-Cache", "use", "bypass", "tools_or_state", protocol.OpenAI},
		{"tool result", "/v1/chat/completions", `{"model":"gpt-4o","temperature":0,"messages":[{"role":"tool","content":"done","tool_call_id":"call-1"}]}`, "X-PivotFlow-Cache", "use", "bypass", "tools_or_state", protocol.OpenAI},
		{"image content", "/v1/chat/completions", `{"model":"gpt-4o","temperature":0,"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"https://synthetic.example/image"}}]}]}`, "X-PivotFlow-Cache", "use", "bypass", "tools_or_state", protocol.OpenAI},
		{"stored completion", "/v1/chat/completions", strings.TrimSuffix(chat, "}") + `,"store":true}`, "X-PivotFlow-Cache", "use", "bypass", "tools_or_state", protocol.OpenAI},
		{"responses", "/v1/responses", `{"model":"gpt-4o","temperature":0,"input":"hello"}`, "X-PivotFlow-Cache", "use", "bypass", "unsupported_endpoint", protocol.Codex},
		{"unknown root field", "/v1/chat/completions", strings.TrimSuffix(chat, "}") + `,"new_feature":true}`, "X-PivotFlow-Cache", "use", "bypass", "unsupported_request", protocol.OpenAI},
		{"temperature omitted", "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`, "X-PivotFlow-Cache", "use", "bypass", "nondeterministic", protocol.OpenAI},
		{"nonzero temperature", "/v1/chat/completions", strings.Replace(chat, `"temperature":0`, `"temperature":0.5`, 1), "X-PivotFlow-Cache", "use", "bypass", "nondeterministic", protocol.OpenAI},
		{"multiple choices", "/v1/chat/completions", strings.TrimSuffix(chat, "}") + `,"n":2}`, "X-PivotFlow-Cache", "use", "bypass", "nondeterministic", protocol.OpenAI},
		{"session header", "/v1/chat/completions", chat, "Session-Id", "synthetic-session", "bypass", "tools_or_state", protocol.OpenAI},
		{"cookie session", "/v1/chat/completions", chat, "Cookie", "session_id=synthetic-session", "bypass", "tools_or_state", protocol.OpenAI},
		{"query state", "/v1/chat/completions?conversation=synthetic", chat, "X-PivotFlow-Cache", "use", "bypass", "request_query", protocol.OpenAI},
		{"request no store", "/v1/chat/completions", chat, "Cache-Control", "max-age=10, NO-STORE", "bypass", "request_no_store", protocol.OpenAI},
		{"request refresh", "/v1/chat/completions", chat, "Cache-Control", "no-cache", "refresh", "request_no_cache", protocol.OpenAI},
		{"encoded body", "/v1/chat/completions", chat, "Content-Encoding", "br", "bypass", "request_encoding", protocol.OpenAI},
		{"duplicate keys", "/v1/chat/completions", strings.TrimSuffix(chat, "}") + `,"temperature":0}`, "X-PivotFlow-Cache", "use", "bypass", "unsupported_request", protocol.OpenAI},
		{"invalid unicode", "/v1/chat/completions", strings.Replace(chat, "hello", `\ud800`, 1), "X-PivotFlow-Cache", "use", "bypass", "unsupported_request", protocol.OpenAI},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", tc.path, nil)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-PivotFlow-Cache", "use")
			if tc.header == "" {
				req.Header.Del("X-PivotFlow-Cache")
			} else {
				req.Header.Set(tc.header, tc.value)
			}
			mode, reason := classifyRequestCacheRequest(req, []byte(tc.body), tc.client, false)
			if mode != tc.mode || reason != tc.reason {
				t.Fatalf("decision=%s/%s, want %s/%s", mode, reason, tc.mode, tc.reason)
			}
		})
	}
}

func TestRequestCacheResponseRejectsPartialToolAndStatefulResults(t *testing.T) {
	cases := []struct {
		name, body, header, value, reason string
		status                            int
	}{
		{"completed", requestCacheOpenAIReply, "", "", "", 200},
		{"length limit", strings.Replace(requestCacheOpenAIReply, `"stop"`, `"length"`, 1), "", "", "unsafe_response", 200},
		{"tool result", strings.Replace(requestCacheOpenAIReply, `"content":"same answer"`, `"content":"same answer","tool_calls":[]`, 1), "", "", "unsafe_response", 200},
		{"partial json", strings.TrimSuffix(requestCacheOpenAIReply, "}"), "", "", "unsafe_response", 200},
		{"duplicate field", strings.TrimSuffix(requestCacheOpenAIReply, "}") + `,"choices":[]}`, "", "", "unsafe_response", 200},
		{"HTTP error", requestCacheOpenAIReply, "", "", "upstream_error", 503},
		{"upstream no store", requestCacheOpenAIReply, "Cache-Control", "no-store", "response_no_store", 200},
		{"upstream no cache", requestCacheOpenAIReply, "Cache-Control", "no-cache", "response_no_store", 200},
		{"vary star", requestCacheOpenAIReply, "Vary", "Accept, *", "response_no_store", 200},
		{"cookie", requestCacheOpenAIReply, "Set-Cookie", "synthetic=1", "response_no_store", 200},
		{"encoded response", requestCacheOpenAIReply, "Content-Encoding", "br", "unsafe_response", 200},
		{"event stream", requestCacheOpenAIReply, "Content-Type", "text/event-stream", "unsafe_response", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := http.Header{"Content-Type": []string{"application/json"}}
			if tc.header != "" {
				header.Set(tc.header, tc.value)
			}
			if got := requestCacheResponseReason(protocol.OpenAI, tc.status, header, []byte(tc.body)); got != tc.reason {
				t.Fatalf("reason=%q want %q", got, tc.reason)
			}
		})
	}
}
