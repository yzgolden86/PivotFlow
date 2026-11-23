package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/util"

	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
)

func reliabilityChannelIDByName(t testing.TB, env *proxyTestEnv, name string) int64 {
	t.Helper()
	configs, err := env.store.ListConfigs(context.Background())
	if err != nil {
		t.Fatalf("list configs: %v", err)
	}
	for _, cfg := range configs {
		if cfg.Name == name {
			return cfg.ID
		}
	}
	t.Fatalf("channel %q not found", name)
	return 0
}

func requireNoReliabilityCooldowns(t testing.TB, env *proxyTestEnv) {
	t.Helper()
	ctx := context.Background()
	channelCooldowns, err := env.store.GetAllChannelCooldowns(ctx)
	if err != nil {
		t.Fatalf("get channel cooldowns: %v", err)
	}
	keyCooldowns, err := env.store.GetAllKeyCooldowns(ctx)
	if err != nil {
		t.Fatalf("get key cooldowns: %v", err)
	}
	modelCooldowns, err := env.store.GetAllModelCooldowns(ctx)
	if err != nil {
		t.Fatalf("get model cooldowns: %v", err)
	}
	if len(channelCooldowns) != 0 || len(keyCooldowns) != 0 || len(modelCooldowns) != 0 {
		t.Fatalf(
			"unexpected cooldowns channel=%v key=%v model=%v",
			channelCooldowns,
			keyCooldowns,
			modelCooldowns,
		)
	}
}

func TestReliabilityClientCancellationDoesNotRetryOrCool(t *testing.T) {
	started := make(chan struct{})
	var primaryCalls atomic.Int32
	primary := newTestHTTPServer(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		primaryCalls.Add(1)
		close(started)
		<-r.Context().Done()
	}))
	var fallbackCalls atomic.Int32
	fallback := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","response":{"id":"resp-fallback","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`+"\n\n")
	}))

	env := setupProxyTestEnv(t, []testChannel{
		{
			name: "cancel-primary", upstreamProtocol: "codex", models: "gpt-test",
			apiKey: "sk-cancel-primary", priority: 100,
		},
		{
			name: "cancel-fallback", upstreamProtocol: "codex", models: "gpt-test",
			apiKey: "sk-cancel-fallback", priority: 90,
		},
	}, map[int]string{0: primary.URL, 1: fallback.URL})

	ctx, cancel := context.WithCancel(context.Background())
	body, err := json.Marshal(map[string]any{
		"model": "gpt-test", "stream": true, "input": "cancel me",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-api-key")

	go func() {
		select {
		case <-started:
			cancel()
		case <-time.After(time.Second):
			cancel()
		}
	}()

	recorder := httptest.NewRecorder()
	env.engine.ServeHTTP(recorder, req)

	if recorder.Code != StatusClientClosedRequest {
		t.Fatalf("cancel status=%d, want %d; body=%s", recorder.Code, StatusClientClosedRequest, recorder.Body.String())
	}
	if primaryCalls.Load() != 1 || fallbackCalls.Load() != 0 {
		t.Fatalf("upstream calls primary=%d fallback=%d, want 1/0", primaryCalls.Load(), fallbackCalls.Load())
	}
	requireNoReliabilityCooldowns(t, env)
}

func TestReliabilityResponsesTruncatedSSEIsFailureButTerminalSSEIsSuccess(t *testing.T) {
	t.Run("semantic delta then clean EOF", func(t *testing.T) {
		var primaryCalls atomic.Int32
		primary := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			primaryCalls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"type":"response.output_text.delta","delta":"partial"}`+"\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}))
		var fallbackCalls atomic.Int32
		fallback := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fallbackCalls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"type":"response.completed","response":{"id":"resp-fallback","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`+"\n\n")
		}))

		env := setupProxyTestEnv(t, []testChannel{
			{
				name: "truncated-primary", upstreamProtocol: "codex", models: "gpt-test",
				apiKey: "sk-truncated-primary", priority: 100,
			},
			{
				name: "truncated-fallback", upstreamProtocol: "codex", models: "gpt-test",
				apiKey: "sk-truncated-fallback", priority: 90,
			},
		}, map[int]string{0: primary.URL, 1: fallback.URL})

		response := doProxyRequest(t, env.engine, "/v1/responses", map[string]any{
			"model": "gpt-test", "stream": true, "input": "truncate",
		}, nil)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"delta":"partial"`) {
			t.Fatalf("truncated response status=%d body=%q", response.Code, response.Body.String())
		}
		if primaryCalls.Load() != 1 || fallbackCalls.Load() != 0 {
			t.Fatalf("truncated stream calls primary=%d fallback=%d, want 1/0", primaryCalls.Load(), fallbackCalls.Load())
		}

		entry := waitForProxyLog(t, env, "gpt-test")
		if entry.StatusCode != util.StatusStreamIncomplete {
			t.Fatalf("truncated stream log status=%d message=%q, want %d", entry.StatusCode, entry.Message, util.StatusStreamIncomplete)
		}

		channelID := reliabilityChannelIDByName(t, env, "truncated-primary")
		modelCooldowns, err := env.store.GetAllModelCooldowns(context.Background())
		if err != nil {
			t.Fatalf("get model cooldowns: %v", err)
		}
		if _, ok := modelCooldowns[channelID]["gpt-test"]; !ok {
			t.Fatalf("truncated Responses stream did not cool model: %v", modelCooldowns)
		}
	})

	t.Run("successful terminal", func(t *testing.T) {
		var primaryCalls atomic.Int32
		primary := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			primaryCalls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"type":"response.output_text.delta","delta":"complete"}`+"\n\n")
			_, _ = io.WriteString(w, `data: {"type":"response.completed","response":{"id":"resp-complete","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"complete"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`+"\n\n")
		}))
		var fallbackCalls atomic.Int32
		fallback := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fallbackCalls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		}))

		env := setupProxyTestEnv(t, []testChannel{
			{
				name: "terminal-primary", upstreamProtocol: "codex", models: "gpt-test",
				apiKey: "sk-terminal-primary", priority: 100,
			},
			{
				name: "terminal-fallback", upstreamProtocol: "codex", models: "gpt-test",
				apiKey: "sk-terminal-fallback", priority: 90,
			},
		}, map[int]string{0: primary.URL, 1: fallback.URL})

		response := doProxyRequest(t, env.engine, "/v1/responses", map[string]any{
			"model": "gpt-test", "stream": true, "input": "complete",
		}, nil)
		if response.Code != http.StatusOK ||
			!strings.Contains(response.Body.String(), `"type":"response.completed"`) {
			t.Fatalf("completed response status=%d body=%q", response.Code, response.Body.String())
		}
		if primaryCalls.Load() != 1 || fallbackCalls.Load() != 0 {
			t.Fatalf("completed stream calls primary=%d fallback=%d, want 1/0", primaryCalls.Load(), fallbackCalls.Load())
		}
		entry := waitForProxyLog(t, env, "gpt-test")
		if entry.StatusCode != http.StatusOK {
			t.Fatalf("completed stream log status=%d, want 200", entry.StatusCode)
		}
		requireNoReliabilityCooldowns(t, env)
	})
}

func TestReliabilityNativeWebsocketToHTTPReplayUsesFullTranscript(t *testing.T) {
	var websocketHandshakes atomic.Int32
	var httpCalls atomic.Int32
	replayBody := make(chan []byte, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			handshake := websocketHandshakes.Add(1)
			if handshake > 1 {
				w.WriteHeader(http.StatusUpgradeRequired)
				return
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Errorf("upgrade transport fallback websocket: %v", err)
				return
			}
			defer func() { _ = conn.Close() }()

			var first map[string]any
			if err = conn.ReadJSON(&first); err != nil {
				t.Errorf("read first transport request: %v", err)
				return
			}
			if err = conn.WriteJSON(map[string]any{
				"type": "response.completed",
				"response": map[string]any{
					"id": "resp-transport-first",
					"output": []any{map[string]any{
						"type": "message", "role": "assistant",
						"content": []any{map[string]any{"type": "output_text", "text": "first answer"}},
					}},
					"usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
				},
			}); err != nil {
				t.Errorf("write first transport response: %v", err)
				return
			}
			var second map[string]any
			if err = conn.ReadJSON(&second); err != nil {
				t.Errorf("read second transport request: %v", err)
			}
			return
		}

		httpCalls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read HTTP replay: %v", err)
			return
		}
		replayBody <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"type":"response.completed","response":{"id":"resp-transport-http","output":[],"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}`+"\n\n")
	}))
	defer upstream.Close()

	env := setupProxyTestEnv(t, []testChannel{{
		name: "transport-fallback", upstreamProtocol: "codex", websockets: true,
		models: "gpt-test", priority: 100,
	}}, map[int]string{0: upstream.URL})
	env.server.client = upstream.Client()

	downstream := dialResponsesWebsocketWithSessionID(t, env.engine, "transport-fallback")
	if err := downstream.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set transport fallback deadline: %v", err)
	}
	if err := downstream.WriteJSON(map[string]any{
		"type": "response.create", "model": "gpt-test",
		"input": []any{map[string]any{"type": "message", "role": "user", "content": "first"}},
	}); err != nil {
		t.Fatalf("write first transport turn: %v", err)
	}
	readWebsocketUntilType(t, downstream, "response.completed")

	if err := downstream.WriteJSON(map[string]any{
		"type": "response.create", "model": "gpt-test", "previous_response_id": "resp-transport-first",
		"input": []any{map[string]any{"type": "message", "role": "user", "content": "second"}},
	}); err != nil {
		t.Fatalf("write second transport turn: %v", err)
	}
	completed := readWebsocketUntilType(t, downstream, "response.completed")
	response, _ := completed["response"].(map[string]any)
	if response["id"] != "resp-transport-http" {
		t.Fatalf("transport fallback response=%#v", completed)
	}

	if websocketHandshakes.Load() != 2 || httpCalls.Load() != 1 {
		t.Fatalf(
			"transport fallback handshakes=%d HTTP calls=%d, want 2/1",
			websocketHandshakes.Load(),
			httpCalls.Load(),
		)
	}
	body := <-replayBody
	if gjson.GetBytes(body, "previous_response_id").Exists() {
		t.Fatalf("HTTP replay leaked a response ID bound to the old socket: %s", body)
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() || len(input.Array()) != 3 {
		t.Fatalf("HTTP replay input=%s, want user+assistant+user transcript", input.Raw)
	}
	if input.Array()[0].Get("content").String() != "first" ||
		input.Array()[1].Get("content.0.text").String() != "first answer" ||
		input.Array()[2].Get("content").String() != "second" {
		t.Fatalf("HTTP replay transcript order/content is wrong: %s", body)
	}
}

func TestResponsesSSETerminalTrackerEventAndDataTypeAcrossChunks(t *testing.T) {
	tests := []struct {
		name   string
		stream string
	}{
		{
			name: "event line",
			stream: "event: response.created\n" +
				`data: {"id":"resp-1"}` + "\n\n" +
				"event: response.completed\n" +
				`data: {"response":{"id":"resp-1"}}` + "\n\n",
		},
		{
			name: "data type",
			stream: `data: {"type":"response.created","id":"resp-1"}` + "\n\n" +
				`data: {"type":"response.completed","response":{"id":"resp-1"}}` + "\n\n",
		},
		{
			name: "mixed event and data types",
			stream: "event: response.created\n" +
				`data: {"id":"resp-1"}` + "\n\n" +
				"event: response.output_text.delta\n" +
				`data: {"type":"response.output_text.delta","delta":"hello"}` + "\n\n" +
				"event: response.completed\n" +
				`data: {"type":"response.completed","response":{"id":"resp-1"}}` + "\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, chunkSize := range []int{1, 3, 7, 64, SSEBufferSize, len(tt.stream)} {
				t.Run("chunk_"+strconv.Itoa(chunkSize), func(t *testing.T) {
					tracker := &responsesSSETerminalTracker{}
					for offset := 0; offset < len(tt.stream); offset += chunkSize {
						end := min(offset+chunkSize, len(tt.stream))
						tracker.observe([]byte(tt.stream[offset:end]))
						if tracker.eventBuffer.Len() > maxSSEEventSize {
							t.Fatalf(
								"tracker buffer grew to %d bytes after %d input bytes",
								tracker.eventBuffer.Len(),
								end,
							)
						}
					}
					if !tracker.sawResponseEvent {
						t.Fatal("Responses event was not detected")
					}
					if !tracker.sawTerminalEvent {
						t.Fatal("Responses terminal event was not detected")
					}
				})
			}
		})
	}
}

func TestResponsesSSETerminalTrackerBoundsOversizedEvents(t *testing.T) {
	hugePayload := strings.Repeat("x", 2*maxSSEEventSize)
	stream := "event: response.created\n" +
		`data: {"type":"response.created"}` + "\n\n" +
		"event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"` + hugePayload + `"}` + "\n\n" +
		"event: response.completed\n" +
		`data: {"response":{"output":[{"type":"image_generation_call","result":"` + hugePayload + `"}]}}` + "\n\n"

	tracker := &responsesSSETerminalTracker{}
	for offset := 0; offset < len(stream); offset += SSEBufferSize {
		end := min(offset+SSEBufferSize, len(stream))
		tracker.observe([]byte(stream[offset:end]))
		if tracker.eventBuffer.Len() > maxSSEEventSize {
			t.Fatalf(
				"tracker buffer grew to %d bytes after %d input bytes",
				tracker.eventBuffer.Len(),
				end,
			)
		}
	}
	if !tracker.sawResponseEvent || !tracker.sawTerminalEvent {
		t.Fatalf(
			"oversized terminal event detection response=%v terminal=%v",
			tracker.sawResponseEvent,
			tracker.sawTerminalEvent,
		)
	}
	if cap(tracker.jsonType.stringBuffer) > maxResponsesJSONTokenBytes {
		t.Fatalf("tracker JSON token buffer capacity grew to %d bytes", cap(tracker.jsonType.stringBuffer))
	}
}

func TestStreamCopySSELargeResponsesEventsRemainTransparent(t *testing.T) {
	hugePayload := strings.Repeat("y", 2*maxSSEEventSize)
	stream := []byte("event: response.created\n" +
		`data: {"type":"response.created"}` + "\n\n" +
		"event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"` + hugePayload + `"}` + "\n\n" +
		"event: response.completed\n" +
		`data: {"response":{"output":[{"type":"image_generation_call","result":"` + hugePayload + `"}]}}` + "\n\n")

	recorder := newRecorder()
	var forwarded int
	err := streamCopySSE(context.Background(), bytes.NewReader(stream), recorder, func(data []byte) error {
		forwarded += len(data)
		return nil
	})
	if err != nil {
		t.Fatalf("streamCopySSE returned %v", err)
	}
	if forwarded != len(stream) {
		t.Fatalf("onData forwarded %d bytes, want %d", forwarded, len(stream))
	}
	if !bytes.Equal(recorder.Body.Bytes(), stream) {
		t.Fatalf("stream was not forwarded byte-for-byte: got %d bytes, want %d", recorder.Body.Len(), len(stream))
	}
}

func TestStreamCopySSELargeDataOnlyResponsesTerminalRemainsSuccessful(t *testing.T) {
	hugeOutput := strings.Repeat("z", maxSSEEventSize+1)
	stream := []byte(`data: {"type":"response.created"}` + "\n\n" +
		`data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n" +
		`data: {"response":{"output":[{"content":"` + hugeOutput + `"}]},"type":"response.completed"}` + "\n\n")

	recorder := newRecorder()
	err := streamCopySSE(context.Background(), bytes.NewReader(stream), recorder, nil)
	if err != nil {
		t.Fatalf("data-only oversized terminal returned %v", err)
	}
	if !bytes.Equal(recorder.Body.Bytes(), stream) {
		t.Fatalf("data-only oversized terminal was not forwarded byte-for-byte: got %d bytes, want %d", recorder.Body.Len(), len(stream))
	}
}

func TestStreamCopySSEResponsesTerminalEventsDoNotReportUnexpectedEOF(t *testing.T) {
	terminalTypes := []string{
		"response.completed",
		"response.done",
		"response.incomplete",
		"response.failed",
		"response.cancelled",
		"response.canceled",
	}

	for _, withEventLine := range []bool{true, false} {
		for _, terminalType := range terminalTypes {
			name := terminalType + "/data"
			if withEventLine {
				name = terminalType + "/event"
			}
			t.Run(name, func(t *testing.T) {
				terminalEvent := `data: {"type":"` + terminalType + `","response":{"id":"resp-1"}}` + "\n\n"
				if withEventLine {
					terminalEvent = "event: " + terminalType + "\n" +
						`data: {"response":{"id":"resp-1"}}` + "\n\n"
				}
				stream := []byte("event: response.created\n" +
					`data: {"type":"response.created"}` + "\n\n" +
					terminalEvent)

				recorder := newRecorder()
				err := streamCopySSE(context.Background(), bytes.NewReader(stream), recorder, nil)
				if err != nil {
					t.Fatalf("terminal %s returned %v", terminalType, err)
				}
				if !bytes.Equal(recorder.Body.Bytes(), stream) {
					t.Fatalf("terminal %s was not forwarded byte-for-byte", terminalType)
				}
			})
		}
	}
}

func TestStreamCopySSEResponsesMissingTerminalReturnsUnexpectedEOF(t *testing.T) {
	stream := []byte("event: response.created\n" +
		`data: {"type":"response.created"}` + "\n\n" +
		"event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n")

	recorder := newRecorder()
	err := streamCopySSE(context.Background(), bytes.NewReader(stream), recorder, nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("streamCopySSE returned %v, want io.ErrUnexpectedEOF", err)
	}
	if !bytes.Equal(recorder.Body.Bytes(), stream) {
		t.Fatal("truncated Responses stream was not forwarded byte-for-byte")
	}
}

func TestStreamCopySSENonResponsesProtocolsAreUnaffected(t *testing.T) {
	tests := []struct {
		name   string
		stream string
	}{
		{
			name: "OpenAI chat",
			stream: `data: {"choices":[{"delta":{"content":"hello"},"finish_reason":null}]}` + "\n\n" +
				`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
				"data: [DONE]\n\n",
		},
		{
			name: "Anthropic messages",
			stream: "event: message_start\n" +
				`data: {"type":"message_start","message":{"id":"msg-1"}}` + "\n\n" +
				"event: content_block_delta\n" +
				`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}` + "\n\n" +
				"event: message_stop\n" +
				`data: {"type":"message_stop"}` + "\n\n",
		},
		{
			name: "Gemini generateContent",
			stream: `data: {"candidates":[{"content":{"parts":[{"text":"hello"}]}}]}` + "\n\n" +
				`data: {"candidates":[{"content":{"parts":[{"text":" world"}]},"finishReason":"STOP"}]}` + "\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := newRecorder()
			err := streamCopySSE(context.Background(), strings.NewReader(tt.stream), recorder, nil)
			if err != nil {
				t.Fatalf("streamCopySSE returned %v", err)
			}
			if recorder.Body.String() != tt.stream {
				t.Fatal("stream was not forwarded byte-for-byte")
			}
		})
	}
}
