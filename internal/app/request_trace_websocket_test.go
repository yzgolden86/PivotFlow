package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/protocol"
	"github.com/yzgolden86/PivotFlow/internal/util"

	"github.com/gorilla/websocket"
)

func TestResponsesWebsocketRequestTraceFailed2xxIsNotSuccess(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusNoContent} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			result := &proxyResult{status: status, succeeded: false}
			trace := newRequestTrace(time.Now(), 1, "websocket")
			summary := trace.snapshot(responsesWebsocketTraceStatus(result), false)
			if summary.FinalStatus != http.StatusBadGateway || summary.Outcome != "error" {
				t.Fatalf("failed websocket result counted as success: status=%d summary=%+v", status, summary)
			}
		})
	}
}

func listWebsocketRequestTraces(t testing.TB, env *proxyTestEnv) []*model.RequestTrace {
	t.Helper()
	traces, _, err := env.store.ListRequestTraces(
		context.Background(),
		time.Now().Add(-10*time.Minute),
		time.Now().Add(10*time.Minute),
		100,
		0,
		nil,
	)
	if err != nil {
		t.Fatalf("list websocket request traces: %v", err)
	}
	return traces
}

func waitForWebsocketRequestTraces(t testing.TB, env *proxyTestEnv, want int) []*model.RequestTrace {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		traces := listWebsocketRequestTraces(t, env)
		if len(traces) >= want {
			return traces
		}
		if time.Now().After(deadline) {
			t.Fatalf("persisted websocket request traces=%d, want %d", len(traces), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func getWebsocketRequestTrace(t testing.TB, env *proxyTestEnv, id string) *model.RequestTrace {
	t.Helper()
	trace, err := env.store.GetRequestTrace(context.Background(), id, nil)
	if err != nil {
		t.Fatalf("get websocket request trace %q: %v", id, err)
	}
	if trace == nil {
		t.Fatalf("websocket request trace %q is missing", id)
	}
	return trace
}

func TestResponsesWebsocketRequestTraceUsesIndependentTurnIdentities(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		turn := upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(
			w,
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-trace-%d\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n",
			turn,
		)
	}))
	env := setupProxyTestEnv(t, []testChannel{{
		name: "trace-turns", upstreamProtocol: "codex", models: "gpt-test", priority: 100,
	}}, map[int]string{0: upstream.URL})

	const sessionID = "shared-websocket-session"
	conn := dialResponsesWebsocketWithSessionID(t, env.engine, sessionID)
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set websocket deadline: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{
		"type": "response.create", "model": "gpt-test",
		"input": []any{map[string]any{"role": "user", "content": "first turn"}},
	}); err != nil {
		t.Fatalf("write first turn: %v", err)
	}
	readWebsocketUntilType(t, conn, "response.completed")

	if err := conn.WriteJSON(map[string]any{
		"type": "response.create", "previous_response_id": "resp-trace-1",
		"input": []any{map[string]any{"role": "user", "content": "second turn"}},
	}); err != nil {
		t.Fatalf("write second turn: %v", err)
	}
	readWebsocketUntilType(t, conn, "response.completed")

	traces := waitForWebsocketRequestTraces(t, env, 2)
	if len(traces) != 2 {
		t.Fatalf("websocket traces=%d, want exactly 2", len(traces))
	}
	if traces[0].RequestID == traces[1].RequestID {
		t.Fatalf("two turns reused request trace id %q", traces[0].RequestID)
	}
	for _, summary := range traces {
		if summary.RequestID == sessionID || !strings.HasPrefix(summary.RequestID, "pf_req_") ||
			len(summary.RequestID) != len("pf_req_")+32 {
			t.Fatalf("invalid per-turn trace id %q for session %q", summary.RequestID, sessionID)
		}
		if summary.Transport != "websocket" || summary.Model != "gpt-test" ||
			summary.ClientProtocol != string(protocol.Codex) || !summary.IsStreaming ||
			summary.AuthTokenID != 1 || summary.FinalStatus != http.StatusOK || summary.Outcome != "success" {
			t.Fatalf("unexpected websocket trace summary: %+v", summary)
		}
		detail := getWebsocketRequestTrace(t, env, summary.RequestID)
		if detail.AttemptCount != 1 || len(detail.Attempts) != 1 {
			t.Fatalf("turn trace attempts=%d detail=%+v", detail.AttemptCount, detail.Attempts)
		}
		attempt := detail.Attempts[0]
		if !attempt.Upstream || attempt.Transport != "http" || attempt.Outcome != "success" ||
			attempt.StatusCode != http.StatusOK || attempt.ActualModel != "gpt-test" {
			t.Fatalf("unexpected per-turn attempt: %+v", attempt)
		}
	}
}

func TestResponsesWebsocketRequestTraceRecordsLocalPrewarmWithoutUpstream(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	env := setupProxyTestEnv(t, []testChannel{{
		name: "trace-prewarm", upstreamProtocol: "codex", models: "gpt-test", priority: 100,
	}}, map[int]string{0: upstream.URL})

	conn := dialResponsesWebsocket(t, env.engine)
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set prewarm deadline: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{
		"type": "response.create", "model": "gpt-test", "generate": false,
	}); err != nil {
		t.Fatalf("write prewarm request: %v", err)
	}
	readWebsocketUntilType(t, conn, "response.completed")

	traces := waitForWebsocketRequestTraces(t, env, 1)
	if len(traces) != 1 {
		t.Fatalf("prewarm traces=%d, want 1", len(traces))
	}
	trace := getWebsocketRequestTrace(t, env, traces[0].RequestID)
	if trace.FinalStatus != http.StatusOK || trace.Outcome != "success" ||
		trace.Transport != "websocket" || trace.Model != "gpt-test" || !trace.IsStreaming {
		t.Fatalf("unexpected prewarm trace: %+v", trace)
	}
	if trace.AttemptCount != 0 || trace.UpstreamAttemptCount != 0 || len(trace.Attempts) != 0 {
		t.Fatalf("local prewarm recorded upstream attempts: %+v", trace)
	}
	if upstreamCalls.Load() != 0 {
		t.Fatalf("local prewarm reached upstream %d times", upstreamCalls.Load())
	}
}

func TestResponsesWebsocketRequestTraceCompletedTurnWinsLateClientClose(t *testing.T) {
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(
			w,
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-trace-close-race\","+
				"\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n",
		)
	}))
	env := setupProxyTestEnv(t, []testChannel{{
		name: "trace-close-race", upstreamProtocol: "codex", models: "gpt-test", priority: 100,
	}}, map[int]string{0: upstream.URL})

	conn := dialResponsesWebsocket(t, env.engine)
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set close-race deadline: %v", err)
	}
	if err := conn.WriteJSON(map[string]any{
		"type": "response.create", "model": "gpt-test",
		"input": []any{map[string]any{"role": "user", "content": "complete then close"}},
	}); err != nil {
		t.Fatalf("write close-race request: %v", err)
	}
	readWebsocketUntilType(t, conn, "response.completed")
	if err := conn.Close(); err != nil {
		t.Fatalf("close completed websocket client: %v", err)
	}

	traces := waitForWebsocketRequestTraces(t, env, 1)
	trace := getWebsocketRequestTrace(t, env, traces[0].RequestID)
	if trace.FinalStatus != http.StatusOK || trace.Outcome != "success" {
		t.Fatalf("late client close changed completed trace: %+v", trace)
	}
}

func TestResponsesWebsocketRequestTraceRecordsFailedStreams(t *testing.T) {
	for _, testCase := range []struct {
		name           string
		events         string
		terminal       string
		committed      bool
		expectedStatus int
	}{
		{
			name:           "truncated",
			events:         "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n",
			terminal:       "error",
			committed:      true,
			expectedStatus: util.StatusStreamIncomplete,
		},
		{
			name: "failed terminal",
			events: "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp-trace-failed\"," +
				"\"status\":\"failed\",\"output\":[],\"error\":{\"code\":\"upstream_failure\"}}}\n\n",
			terminal:       "response.failed",
			expectedStatus: util.StatusSSEError,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, testCase.events)
			}))
			env := setupProxyTestEnv(t, []testChannel{{
				name: "trace-failed-" + testCase.name, upstreamProtocol: "codex", models: "gpt-test", priority: 100,
			}}, map[int]string{0: upstream.URL})

			conn := dialResponsesWebsocket(t, env.engine)
			if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatalf("set failed-stream deadline: %v", err)
			}
			if err := conn.WriteJSON(map[string]any{
				"type": "response.create", "model": "gpt-test",
				"input": []any{map[string]any{"role": "user", "content": "fail"}},
			}); err != nil {
				t.Fatalf("write failed-stream request: %v", err)
			}
			var event map[string]any
			if err := conn.ReadJSON(&event); err != nil {
				t.Fatalf("read first failed-stream event: %v", err)
			}
			if event["type"] != testCase.terminal && event["type"] != "response.output_text.delta" {
				t.Fatalf("first failed-stream event=%#v", event)
			}
			if event["type"] != testCase.terminal {
				if err := conn.ReadJSON(&event); err != nil {
					t.Fatalf("read terminal failed-stream event: %v", err)
				}
				if event["type"] != testCase.terminal {
					t.Fatalf("terminal failed-stream event=%#v, want %q", event, testCase.terminal)
				}
			}

			traces := waitForWebsocketRequestTraces(t, env, 1)
			if len(traces) != 1 {
				t.Fatalf("failed-stream traces=%d, want 1", len(traces))
			}
			trace := getWebsocketRequestTrace(t, env, traces[0].RequestID)
			if trace.Outcome != "error" || trace.FinalStatus != testCase.expectedStatus {
				t.Fatalf("failed stream recorded as status=%d outcome=%q", trace.FinalStatus, trace.Outcome)
			}
			if trace.AttemptCount != 1 || len(trace.Attempts) != 1 {
				t.Fatalf("failed stream attempt boundary missing: %+v", trace)
			}
			if testCase.committed && !trace.Attempts[0].ResponseCommitted {
				t.Fatalf("committed stream boundary missing: %+v", trace.Attempts[0])
			}
		})
	}
}

func TestResponsesWebsocketRequestTraceRecordsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusGatewayTimeout)
		}
	}))
	env := setupProxyTestEnv(t, []testChannel{{
		name: "trace-canceled", upstreamProtocol: "codex", models: "gpt-test", priority: 100,
	}}, map[int]string{0: upstream.URL})

	conn := dialResponsesWebsocket(t, env.engine)
	if err := conn.WriteJSON(map[string]any{
		"type": "response.create", "model": "gpt-test",
		"input": []any{map[string]any{"role": "user", "content": "cancel"}},
	}); err != nil {
		t.Fatalf("write cancellable request: %v", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream turn did not start")
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close websocket client: %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("upstream request was not canceled")
	}

	traces := waitForWebsocketRequestTraces(t, env, 1)
	if len(traces) != 1 {
		t.Fatalf("canceled traces=%d, want 1", len(traces))
	}
	trace := getWebsocketRequestTrace(t, env, traces[0].RequestID)
	if trace.FinalStatus != StatusClientClosedRequest || trace.Outcome != "canceled" ||
		trace.Transport != "websocket" {
		t.Fatalf("unexpected canceled trace: %+v", trace)
	}
}

func TestResponsesWebsocketRequestTraceRecordsNativeReconnectReplay(t *testing.T) {
	var handshakes atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade reconnect websocket: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read reconnect request: %v", err)
			return
		}
		if handshakes.Add(1) == 1 {
			// Close before any semantic event so the session performs one replay.
			return
		}
		if err := conn.WriteJSON(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": "resp-trace-reconnected", "output": []any{},
				"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
			},
		}); err != nil {
			t.Errorf("write reconnect completion: %v", err)
		}
	}))
	defer upstream.Close()

	env := setupProxyTestEnv(t, []testChannel{{
		name: "trace-native-reconnect", upstreamProtocol: "codex", websockets: true,
		models: "gpt-test", priority: 100,
	}}, map[int]string{0: upstream.URL})
	downstream := dialResponsesWebsocket(t, env.engine)
	if err := downstream.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set reconnect deadline: %v", err)
	}
	if err := downstream.WriteJSON(map[string]any{
		"type": "response.create", "model": "gpt-test",
		"input": []any{map[string]any{"role": "user", "content": "reconnect"}},
	}); err != nil {
		t.Fatalf("write reconnect request: %v", err)
	}
	readWebsocketUntilType(t, downstream, "response.completed")

	traces := waitForWebsocketRequestTraces(t, env, 1)
	trace := getWebsocketRequestTrace(t, env, traces[0].RequestID)
	if trace.Outcome != "success" || trace.FinalStatus != http.StatusOK ||
		trace.AttemptCount != 2 || trace.UpstreamAttemptCount != 2 || len(trace.Attempts) != 2 {
		t.Fatalf("unexpected reconnect trace: %+v", trace)
	}
	first, second := trace.Attempts[0], trace.Attempts[1]
	if first.Transport != "websocket" || first.StatusCode != http.StatusBadGateway ||
		first.Outcome != "error" || first.Reason != "websocket_reconnect" || !first.Upstream {
		t.Fatalf("first reconnect attempt=%+v", first)
	}
	if second.Transport != "websocket" || second.StatusCode != http.StatusOK ||
		second.Outcome != "success" || second.Reason != "" || !second.Upstream {
		t.Fatalf("replayed reconnect attempt=%+v", second)
	}
}

func TestResponsesWebsocketRequestTraceRecordsHandshakeHTTPFallbackWithoutCooldown(t *testing.T) {
	var websocketRejections atomic.Int32
	var httpCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			websocketRejections.Add(1)
			w.WriteHeader(http.StatusUpgradeRequired)
			return
		}
		httpCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(
			w,
			"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-trace-http-fallback\","+
				"\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n",
		)
	}))
	defer upstream.Close()

	env := setupProxyTestEnv(t, []testChannel{{
		name: "trace-handshake-fallback", upstreamProtocol: "codex", websockets: true,
		models: "gpt-test", priority: 100,
	}}, map[int]string{0: upstream.URL})
	env.server.client = upstream.Client()
	downstream := dialResponsesWebsocket(t, env.engine)
	if err := downstream.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set handshake fallback deadline: %v", err)
	}
	if err := downstream.WriteJSON(map[string]any{
		"type": "response.create", "model": "gpt-test",
		"input": []any{map[string]any{"role": "user", "content": "fallback"}},
	}); err != nil {
		t.Fatalf("write handshake fallback request: %v", err)
	}
	readWebsocketUntilType(t, downstream, "response.completed")

	traces := waitForWebsocketRequestTraces(t, env, 1)
	trace := getWebsocketRequestTrace(t, env, traces[0].RequestID)
	if trace.Outcome != "success" || trace.FinalStatus != http.StatusOK ||
		trace.AttemptCount != 2 || trace.UpstreamAttemptCount != 2 || len(trace.Attempts) != 2 {
		t.Fatalf("unexpected handshake fallback trace: %+v", trace)
	}
	first, second := trace.Attempts[0], trace.Attempts[1]
	if first.Transport != "websocket" || first.StatusCode != http.StatusUpgradeRequired ||
		first.Outcome != "capability" || first.Reason != "websocket_handshake_fallback" ||
		first.Cooldown != "none" {
		t.Fatalf("rejected handshake attempt=%+v", first)
	}
	if second.Transport != "http" || second.StatusCode != http.StatusOK ||
		second.Outcome != "success" || second.Cooldown != "none" {
		t.Fatalf("HTTP fallback attempt=%+v", second)
	}
	if websocketRejections.Load() != 1 || httpCalls.Load() != 1 {
		t.Fatalf("handshake attempts websocket=%d http=%d, want 1/1",
			websocketRejections.Load(), httpCalls.Load())
	}

	channelCooldowns, err := env.store.GetAllChannelCooldowns(context.Background())
	if err != nil {
		t.Fatalf("read channel cooldowns: %v", err)
	}
	if len(channelCooldowns) != 0 {
		t.Fatalf("handshake fallback applied channel cooldown: %v", channelCooldowns)
	}
	modelCooldowns, err := env.store.GetAllModelCooldowns(context.Background())
	if err != nil {
		t.Fatalf("read model cooldowns: %v", err)
	}
	if len(modelCooldowns[first.ChannelID]) != 0 {
		t.Fatalf("handshake fallback applied model cooldown: %v", modelCooldowns[first.ChannelID])
	}
	keys, err := env.store.GetAPIKeys(context.Background(), first.ChannelID)
	if err != nil {
		t.Fatalf("read fallback channel keys: %v", err)
	}
	for _, key := range keys {
		if key.CooldownUntil != 0 {
			t.Fatalf("handshake fallback applied key cooldown: %+v", key)
		}
	}
}
