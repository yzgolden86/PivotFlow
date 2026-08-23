package app

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestProxy_AgentRouterPreservesNativeAnthropicWireIdentity(t *testing.T) {
	type capturedRequest struct {
		header http.Header
		body   []byte
	}
	captured := make(chan capturedRequest, 1)

	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read AgentRouter request body: %v", err)
		}
		captured <- capturedRequest{header: r.Header.Clone(), body: body}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_agentrouter","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"claude-opus-4-8","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	defer upstream.Close()

	env := setupProxyTestEnv(t, []testChannel{{
		name:                  "agentrouter",
		upstreamProtocol:      "anthropic",
		protocolTransformMode: model.ProtocolTransformModeLocal,
		models:                "claude-opus-4-8",
	}}, map[int]string{0: upstream.URL})

	originalBody := []byte(`{"model":"claude-opus-4-8","system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.42.603; cc_entrypoint=cli; cch=abc12;"},{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude."},{"type":"text","text":"Inspect and improve the current project."}],"messages":[{"role":"user","content":[{"type":"text","text":"check the routing implementation"}]}],"max_tokens":1024,"stream":false}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(originalBody))
	req.Header.Set("Authorization", "Bearer test-api-key")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-cli/2.1.42 (external, cli)")
	req.Header.Set("X-App", "cli")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "claude-code-20250219,interleaved-thinking-2025-05-14")
	req.Header.Set("X-Claude-Code-Session-Id", "11111111-1111-4111-8111-111111111111")

	response := httptest.NewRecorder()
	env.engine.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}

	got := <-captured
	if !bytes.Equal(got.body, originalBody) {
		t.Fatalf("AgentRouter Anthropic body changed:\nwant=%s\n got=%s", originalBody, got.body)
	}
	if got.header.Get("User-Agent") != "claude-cli/2.1.42 (external, cli)" {
		t.Fatalf("User-Agent=%q, want native Claude Code identity", got.header.Get("User-Agent"))
	}
	if got.header.Get("X-App") != "cli" {
		t.Fatalf("X-App=%q, want cli", got.header.Get("X-App"))
	}
	if got.header.Get("Anthropic-Version") != "2023-06-01" {
		t.Fatalf("Anthropic-Version=%q", got.header.Get("Anthropic-Version"))
	}
	if got.header.Get("Anthropic-Beta") != "claude-code-20250219,interleaved-thinking-2025-05-14" {
		t.Fatalf("Anthropic-Beta=%q, want no AnyRouter beta injection", got.header.Get("Anthropic-Beta"))
	}
	if got.header.Get("X-Claude-Code-Session-Id") != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("X-Claude-Code-Session-Id=%q", got.header.Get("X-Claude-Code-Session-Id"))
	}
	if got.header.Get("X-Api-Key") != "sk-test-0" || got.header.Get("Authorization") != "Bearer sk-test-0" {
		t.Fatalf("upstream auth headers were not replaced: x-api-key=%q authorization=%q", got.header.Get("X-Api-Key"), got.header.Get("Authorization"))
	}
}
