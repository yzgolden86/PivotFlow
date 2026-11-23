package app

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"

	"github.com/gin-gonic/gin"
)

const requestCacheOpenAIReply = `{"id":"chat-cache-synthetic","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"same answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110}}`

func setupRequestCacheEnv(t *testing.T, upstreamURL string, enabled bool) *proxyTestEnv {
	t.Helper()
	store, err := storage.CreateSQLiteStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	// The RED fixture supplies the future setting before startup, so the test
	// reaches the real proxy instead of failing on unregistered setting setup.
	sqlStore, ok := store.(*sqlstore.SQLStore)
	if !ok {
		t.Fatal("request cache fixture requires its isolated SQLite store")
	}
	if _, err := sqlStore.ExecContext(context.Background(), `INSERT OR IGNORE INTO system_settings(key,value,value_type,description,default_value,updated_at) VALUES ('request_cache_enabled','false','bool','synthetic cache fixture','false',0)`); err != nil {
		t.Fatal(err)
	}
	value := "false"
	if enabled {
		value = "true"
	}
	if err := store.UpdateSetting(context.Background(), "request_cache_enabled", value); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(store)
	srv.client = newTestHTTPClient()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	cfg, err := store.CreateConfig(context.Background(), &model.Config{
		Name: "cache-native", Enabled: true, Priority: 100, CostMultiplier: 1,
		ModelEntries:          []model.ModelEntry{{Model: "gpt-4o"}},
		URLs:                  model.ChannelURLs{{URL: upstreamURL, Protocols: []string{"openai"}}},
		ProtocolTransformMode: model.ProtocolTransformModeLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKeysBatch(context.Background(), []*model.APIKey{{ChannelID: cfg.ID, KeyIndex: 0, APIKey: "sk-test-cache-upstream"}}); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	srv.SetupRoutes(engine)
	return &proxyTestEnv{server: srv, store: store, engine: engine}
}

func requestCacheTestBody() map[string]any {
	return map[string]any{"model": "gpt-4o", "temperature": 0,
		"messages": []any{map[string]any{"role": "user", "content": "repeat this text"}}}
}

func createRequestCacheToken(t *testing.T, env *proxyTestEnv, plain string, token *model.AuthToken) {
	t.Helper()
	token.Token = model.HashToken(plain)
	token.IsActive = true
	token.CreatedAt = time.Now()
	if err := env.store.CreateAuthToken(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if err := env.server.authService.ReloadAuthTokens(); err != nil {
		t.Fatal(err)
	}
}

// Removing the local hit path would forward both requests and duplicate usage.
func TestRequestCacheHTTPRepeatedTextSkipsUpstream(t *testing.T) {
	billingDir := t.TempDir()
	var calls atomic.Int64
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-PivotFlow-Request-Id", "upstream-synthetic-id")
		w.Header().Set("X-Ratelimit-Remaining", "9")
		_, _ = io.WriteString(w, requestCacheOpenAIReply)
	}))
	env := setupRequestCacheEnv(t, upstream.URL, true)
	createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "cache-owner"})
	if err := env.server.EnableDurableBilling(filepath.Join(billingDir, "cache-billing.db")); err != nil {
		t.Fatal(err)
	}
	body := requestCacheTestBody()
	headers := map[string]string{"X-PivotFlow-Cache": "use"}
	first := doProxyRequest(t, env.engine, "/v1/chat/completions", body, headers)
	second := doProxyRequest(t, env.engine, "/v1/chat/completions", body, headers)
	if first.Code != 200 || second.Code != 200 {
		t.Fatalf("statuses=%d/%d bodies=%s / %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("repeated opt-in deterministic text reached upstream %d times; want 1", got)
	}
	if first.Body.String() != requestCacheOpenAIReply || second.Body.String() != requestCacheOpenAIReply {
		t.Fatal("cached response changed the final protocol body")
	}
	if first.Header().Get("X-PivotFlow-Cache") != "miss" || second.Header().Get("X-PivotFlow-Cache") != "hit" {
		t.Fatalf("cache status headers=%q/%q", first.Header().Get("X-PivotFlow-Cache"), second.Header().Get("X-PivotFlow-Cache"))
	}
	firstID, secondID := first.Header().Get(requestIDHeader), second.Header().Get(requestIDHeader)
	if !requestTraceIDPattern.MatchString(firstID) || !requestTraceIDPattern.MatchString(secondID) || firstID == secondID {
		t.Fatal("each downstream request must retain its own new gateway request ID")
	}
	if second.Header().Get("X-Ratelimit-Remaining") != "" {
		t.Fatal("cache replay included a stale upstream rate-limit header")
	}
	trace := awaitRequestTrace(t, env, secondID)
	if trace.CacheStatus != "hit" || trace.CacheReason != "reused" || trace.Outcome != "success" || trace.AttemptCount != 0 || trace.UpstreamAttemptCount != 0 || len(trace.Attempts) != 0 {
		t.Fatalf("cache hit invented upstream activity or lost metadata: %+v", trace)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := env.server.billing.flush(ctx); err != nil {
		t.Fatal(err)
	}
	token, err := env.store.GetAuthTokenByValue(ctx, model.HashToken("test-api-key"))
	if err != nil {
		t.Fatal(err)
	}
	// One real gpt-4o call: 100 * $2.50/1M + 10 * $10/1M = $0.00035.
	if token.SuccessCount != 2 || token.PromptTokensTotal != 100 || token.CompletionTokensTotal != 10 ||
		token.CacheReadTokensTotal != 0 || token.CacheCreationTokensTotal != 0 || math.Abs(token.TotalCostUSD-0.00035) > 1e-9 || math.Abs(token.EffectiveCostUSD-0.00035) > 1e-9 {
		t.Fatalf("cached response duplicated billing or provider prompt-cache usage: %+v", token)
	}
	firstTrace := awaitRequestTrace(t, env, firstID)
	if firstTrace.UpstreamAttemptCount != 1 || firstTrace.UpstreamSuccessCount != 1 {
		t.Fatalf("real call did not record its actual attempt: %+v", firstTrace)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		logs, listErr := env.store.ListLogs(ctx, time.Now().Add(-time.Minute), 50, 0, nil)
		if listErr != nil {
			t.Fatal(listErr)
		}
		if len(logs) > 0 {
			if len(logs) != 1 || logs[0].RequestID != firstID {
				t.Fatalf("cache polluted upstream/health logs: %+v", logs)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual upstream log was not persisted")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRequestCacheHTTPDefaultOffAndUnsafeRequestsStillForward(t *testing.T) {
	cases := []struct {
		name, reason string
		enabled      bool
		modify       func(map[string]any)
		headers      map[string]string
	}{
		{"default off", "disabled", false, nil, map[string]string{requestCacheHeader: "use"}},
		{"no opt in", "not_opted_in", true, nil, nil},
		{"explicit bypass", "client_bypass", true, nil, map[string]string{requestCacheHeader: "bypass"}},
		{"tools", "tools_or_state", true, func(body map[string]any) { body["tools"] = []any{} }, map[string]string{requestCacheHeader: "use"}},
		{"session", "tools_or_state", true, nil, map[string]string{requestCacheHeader: "use", "Session-Id": "synthetic-session"}},
		{"cookie", "tools_or_state", true, nil, map[string]string{requestCacheHeader: "use", "Cookie": "session_id=synthetic-session"}},
		{"unknown option", "unsupported_request", true, func(body map[string]any) { body["new_feature"] = true }, map[string]string{requestCacheHeader: "use"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Header.Get(requestCacheHeader) != "" {
					t.Error("gateway cache control leaked to upstream")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, requestCacheOpenAIReply)
			}))
			env := setupRequestCacheEnv(t, upstream.URL, tc.enabled)
			createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "cache-owner"})
			body := requestCacheTestBody()
			if tc.modify != nil {
				tc.modify(body)
			}
			for range 2 {
				response := doProxyRequest(t, env.engine, "/v1/chat/completions", body, tc.headers)
				if response.Code != 200 {
					t.Fatalf("normal proxy behavior changed: %d %s", response.Code, response.Body.String())
				}
				trace := awaitRequestTrace(t, env, response.Header().Get(requestIDHeader))
				if trace.CacheStatus != "bypass" || trace.CacheReason != tc.reason {
					t.Fatalf("bypass not explained: %+v", trace)
				}
			}
			if calls.Load() != 2 || env.server.requestCache.stats(time.Now()).Entries != 0 {
				t.Fatal("unsafe/default-off request was replayed or stored")
			}
		})
	}
}

func TestRequestCacheHTTPRejectsRevokedModelProtocolChannelAndBudgetPermissions(t *testing.T) {
	for _, restriction := range []string{"inactive", "expired", "model", "protocol", "channel", "cost"} {
		t.Run(restriction, func(t *testing.T) {
			var calls atomic.Int64
			upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, requestCacheOpenAIReply)
			}))
			env := setupRequestCacheEnv(t, upstream.URL, true)
			createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "cache-owner"})
			headers := map[string]string{requestCacheHeader: "use"}
			if response := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), headers); response.Code != 200 {
				t.Fatalf("warm request failed: %d %s", response.Code, response.Body.String())
			}
			token, err := env.store.GetAuthTokenByValue(context.Background(), model.HashToken("test-api-key"))
			if err != nil {
				t.Fatal(err)
			}
			want := http.StatusForbidden
			switch restriction {
			case "inactive":
				token.IsActive, want = false, http.StatusUnauthorized
			case "expired":
				past := time.Now().Add(-time.Second).UnixMilli()
				token.ExpiresAt, want = &past, http.StatusUnauthorized
			case "model":
				token.AllowedModels = []string{"other-model"}
			case "protocol":
				token.AllowedProtocols = []string{"anthropic"}
			case "channel":
				token.AllowedChannelIDs, token.ChannelRestrictionMode = []int64{1}, "deny"
			case "cost":
				token.CostLimitMicroUSD, want = 100, http.StatusTooManyRequests
				token.MaxConcurrency = 1
			}
			if err := env.store.UpdateAuthToken(context.Background(), token); err != nil {
				t.Fatal(err)
			}
			if err := env.server.authService.ReloadAuthTokens(); err != nil {
				t.Fatal(err)
			}
			response := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), headers)
			if response.Code != want || calls.Load() != 1 || response.Header().Get(requestCacheHeader) == "hit" {
				t.Fatalf("cache bypassed %s admission: status=%d calls=%d body=%s", restriction, response.Code, calls.Load(), response.Body.String())
			}
		})
	}
}

func TestRequestCacheHTTPDoesNotCrossTokensOrSemanticHeaders(t *testing.T) {
	var calls atomic.Int64
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, requestCacheOpenAIReply)
	}))
	env := setupRequestCacheEnv(t, upstream.URL, true)
	createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "owner-a"})
	createRequestCacheToken(t, env, "synthetic-owner-b", &model.AuthToken{Description: "owner-b"})
	for _, headers := range []map[string]string{
		{requestCacheHeader: "use"},
		{requestCacheHeader: "use", "Authorization": "Bearer synthetic-owner-b"},
		{requestCacheHeader: "use", "X-Client-Variant": "different"},
		{requestCacheHeader: "use"},
	} {
		if response := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), headers); response.Code != 200 {
			t.Fatalf("request failed: %d %s", response.Code, response.Body.String())
		}
	}
	if calls.Load() != 3 || env.server.requestCache.stats(time.Now()).Hits != 1 {
		t.Fatalf("token/header isolation calls=%d stats=%+v", calls.Load(), env.server.requestCache.stats(time.Now()))
	}
}

func TestRequestCacheHTTPRefreshSupersedesSlowOlderFill(t *testing.T) {
	var calls atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := calls.Add(1)
		answer := "fresh answer"
		if call == 1 {
			close(started)
			<-release
			answer = "old answer"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, strings.Replace(requestCacheOpenAIReply, "same answer", answer, 1))
	}))
	env := setupRequestCacheEnv(t, upstream.URL, true)
	createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "owner"})
	oldResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		oldResult <- doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), map[string]string{requestCacheHeader: "use"})
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("older request never reached the synthetic upstream")
	}
	fresh := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), map[string]string{requestCacheHeader: "refresh"})
	if fresh.Code != 200 || !strings.Contains(fresh.Body.String(), "fresh answer") {
		t.Fatalf("refresh failed: %d %s", fresh.Code, fresh.Body.String())
	}
	// Release once without deferring a second channel close.
	release <- struct{}{}
	select {
	case old := <-oldResult:
		if old.Code != 200 {
			t.Fatalf("older normal request was interrupted: %d %s", old.Code, old.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("older request failed to finish")
	}
	hit := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), map[string]string{requestCacheHeader: "use"})
	if hit.Code != 200 || !strings.Contains(hit.Body.String(), "fresh answer") || calls.Load() != 2 {
		t.Fatalf("slow old fill replaced refresh: status=%d calls=%d body=%s", hit.Code, calls.Load(), hit.Body.String())
	}
}

func TestRequestCacheHTTPFailedHitWriteHasNoSuccessSettlement(t *testing.T) {
	var calls atomic.Int64
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, requestCacheOpenAIReply)
	}))
	env := setupRequestCacheEnv(t, upstream.URL, true)
	createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "owner"})
	if response := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), map[string]string{requestCacheHeader: "use"}); response.Code != 200 {
		t.Fatal("warmup failed")
	}
	request := newJSONRequest(t, "POST", "/v1/chat/completions", requestCacheTestBody())
	request.Header.Set("Authorization", "Bearer test-api-key")
	request.Header.Set(requestCacheHeader, "use")
	writer := cacheFailingWriter{httptest.NewRecorder()}
	env.engine.ServeHTTP(writer, request)
	trace := awaitRequestTrace(t, env, writer.Header().Get(requestIDHeader))
	if trace.Outcome != "error" || trace.CacheReason != "write_failed" || trace.UpstreamAttemptCount != 0 {
		t.Fatalf("failed cache write incorrectly completed: %+v", trace)
	}
	token, err := env.store.GetAuthTokenByValue(context.Background(), model.HashToken("test-api-key"))
	if err != nil {
		t.Fatal(err)
	}
	if token.SuccessCount != 1 || calls.Load() != 1 || env.server.requestCache.stats(time.Now()).Hits != 0 {
		t.Fatalf("failed cache write counted as success: token=%+v calls=%d", token, calls.Load())
	}
}

func TestRequestCacheHTTPHitDoesNotAdvanceRoutingCursor(t *testing.T) {
	var firstCalls, secondCalls atomic.Int64
	upstreamA := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		firstCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, strings.Replace(requestCacheOpenAIReply, "same answer", "channel A", 1))
	}))
	upstreamB := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, strings.Replace(requestCacheOpenAIReply, "same answer", "channel B", 1))
	}))
	env := setupRequestCacheEnv(t, upstreamA.URL, true)
	cfg, err := env.store.CreateConfig(context.Background(), &model.Config{Name: "second", Enabled: true, Priority: 100, CostMultiplier: 1,
		ModelEntries: []model.ModelEntry{{Model: "gpt-4o"}}, URLs: model.ChannelURLs{{URL: upstreamB.URL, Protocols: []string{"openai"}}}, ProtocolTransformMode: model.ProtocolTransformModeLocal})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.CreateAPIKeysBatch(context.Background(), []*model.APIKey{{ChannelID: cfg.ID, KeyIndex: 0, APIKey: "sk-test-second"}}); err != nil {
		t.Fatal(err)
	}
	createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "owner"})
	headers := map[string]string{requestCacheHeader: "use"}
	first := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), headers)
	hit := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), headers)
	body := requestCacheTestBody()
	body["messages"] = []any{map[string]any{"role": "user", "content": "different request"}}
	next := doProxyRequest(t, env.engine, "/v1/chat/completions", body, headers)
	if !strings.Contains(first.Body.String(), "channel A") || hit.Header().Get(requestCacheHeader) != "hit" || !strings.Contains(next.Body.String(), "channel B") || firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("hit consumed upstream route selection: A=%d B=%d first=%s hit=%s next=%s", firstCalls.Load(), secondCalls.Load(), first.Body.String(), hit.Body.String(), next.Body.String())
	}
}
