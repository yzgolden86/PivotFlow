package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/cooldown"
	"github.com/yzgolden86/PivotFlow/internal/model"

	"github.com/gorilla/websocket"
)

func TestRequestTraceWebsocketCloseStopsPendingReplayBeforeReturning(t *testing.T) {
	var handshakes atomic.Int32
	var upstreamRequests atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade lifecycle websocket: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		connection := handshakes.Add(1)
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Errorf("read lifecycle request: %v", err)
			return
		}
		upstreamRequests.Add(1)
		if connection == 1 {
			// A disconnect without output permits the reader to choose a replay.
			return
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.completed","response":{"id":"resp-abandoned","output":[]}}`))
	}))
	t.Cleanup(upstream.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	cfg := &model.Config{ID: 1}
	trace := newRequestTrace(time.Now(), 1, "websocket")
	trace.begin(cfg, 0, upstream.URL, "gpt-test", "codex")
	ctx = trace.attemptContext(ctx)
	markTraceUpstream(ctx, "websocket")
	payload := []byte(`{"model":"gpt-test","input":[{"role":"user","content":"hello"}]}`)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL+"/v1/responses", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}

	reconnectStarted := make(chan struct{})
	reconnectStopped := make(chan struct{})
	releaseReconnect := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseReconnect) }) }
	t.Cleanup(release)
	var dials atomic.Int32
	dialer := &websocket.Dialer{
		HandshakeTimeout: 2 * time.Second,
		NetDialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
			if dials.Add(1) == 2 {
				// This barrier is after the reader passed its closed check.
				close(reconnectStarted)
				defer close(reconnectStopped)
				select {
				case <-releaseReconnect:
				case <-dialCtx.Done():
					return nil, dialCtx.Err()
				}
			}
			return (&net.Dialer{}).DialContext(dialCtx, network, address)
		},
	}
	session := newCodexUpstreamWebsocketSession(1<<20, 0)
	t.Cleanup(session.Close)
	response, _, _, err := session.roundTrip(ctx, cfg, dialer, request, payload, nil, nil, false,
		codexWebsocketTimeouts{idle: time.Second, ping: time.Second}, nil)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	select {
	case <-reconnectStarted:
	case <-ctx.Done():
		t.Fatal("reader did not reach reconnect barrier")
	}
	if err := response.Body.Close(); err != nil {
		t.Errorf("close response body: %v", err)
	}
	select {
	case <-reconnectStopped:
	default:
		t.Error("Close returned while the abandoned reader could still reconnect")
	}
	if session.turnMu.TryLock() {
		session.turnMu.Unlock()
	} else {
		t.Error("Close returned before the old reader released the turn")
	}
	if ctx.Err() != nil {
		t.Errorf("body close canceled its parent context: %v", ctx.Err())
	}
	trace.finishAttempt(&proxyResult{status: StatusClientClosedRequest}, cooldown.Decision{}, nil, context.Canceled)
	attemptsAtClose := trace.number()
	release()

	// Wait for the old reader to finish before asserting actual upstream work.
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !session.turnMu.TryLock() {
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("reader did not finish after releasing the reconnect barrier")
		}
	}
	session.turnMu.Unlock()
	if handshakes.Load() != 1 || upstreamRequests.Load() != 1 {
		t.Errorf("abandoned reader replayed upstream after Close: handshakes=%d requests=%d", handshakes.Load(), upstreamRequests.Load())
	}
	if trace.number() != attemptsAtClose {
		t.Errorf("closed reader appended a later attempt: at_close=%d now=%d", attemptsAtClose, trace.number())
	}
}

func TestRequestTraceCanceledWebsocketReplayDoesNotStartAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := &model.Config{ID: 1}
	trace := newRequestTrace(time.Now(), 1, "websocket")
	trace.begin(cfg, 0, "http://127.0.0.1:1", "gpt-test", "codex")
	ctx = trace.attemptContext(ctx)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:1/v1/responses", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	session := newCodexUpstreamWebsocketSession(1<<20, 0)
	t.Cleanup(session.Close)
	_, err = session.reconnectWithReplay(ctx, &websocket.Dialer{}, codexWebsocketTarget{}, request,
		[]byte(`{"model":"gpt-test","input":[]}`), codexWebsocketTimeouts{idle: time.Second, ping: time.Second})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled replay error=%v", err)
	}
	if trace.number() != 1 {
		t.Fatalf("canceled replay added an upstream attempt: number=%d", trace.number())
	}
}
