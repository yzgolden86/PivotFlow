package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

const requestCacheNativeAnthropicReply = `{"id":"msg-cache-synthetic","type":"message","role":"assistant","model":"claude-cache-synthetic","content":[{"type":"text","text":"same native answer"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":128,"output_tokens":12}}`

const requestCacheUnsafeOpenAIReply = `{"id":"chat-cache-unsafe","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"partial answer"},"finish_reason":"length"}],"usage":{"prompt_tokens":100,"completion_tokens":2,"total_tokens":102}}`

func requestCacheOnlyChannel(t *testing.T, env *proxyTestEnv) *model.Config {
	t.Helper()
	configs, err := env.store.ListConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 {
		t.Fatalf("request-cache fixture has %d channels; want 1", len(configs))
	}
	return configs[0]
}

func requestCacheWarmAndAssertHit(t *testing.T, env *proxyTestEnv, path string, body map[string]any) {
	t.Helper()
	headers := map[string]string{requestCacheHeader: "use"}
	first := doProxyRequest(t, env.engine, path, body, headers)
	hit := doProxyRequest(t, env.engine, path, body, headers)
	if first.Code != http.StatusOK || hit.Code != http.StatusOK {
		t.Fatalf("warm/cache-hit statuses=%d/%d bodies=%s / %s", first.Code, hit.Code, first.Body.String(), hit.Body.String())
	}
	if first.Header().Get(requestCacheHeader) != "miss" || hit.Header().Get(requestCacheHeader) != "hit" {
		t.Fatalf("warm/cache-hit headers=%q/%q", first.Header().Get(requestCacheHeader), hit.Header().Get(requestCacheHeader))
	}
}

func TestRequestCacheHTTPNativeAnthropicReusesCompleteTextReply(t *testing.T) {
	var calls atomic.Int64
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, requestCacheNativeAnthropicReply)
	}))
	env := setupRequestCacheEnv(t, upstream.URL, true)
	channel := requestCacheOnlyChannel(t, env)
	channel.ModelEntries = []model.ModelEntry{{Model: "claude-cache-synthetic"}}
	channel.URLs = model.ChannelURLs{{URL: upstream.URL, Protocols: []string{"anthropic"}}}
	channel.ProtocolTransformMode = model.ProtocolTransformModeLocal
	if _, err := env.store.UpdateConfig(context.Background(), channel.ID, channel); err != nil {
		t.Fatal(err)
	}
	env.server.InvalidateChannelListCache()
	createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "anthropic-cache-owner"})

	body := map[string]any{
		"model":       "claude-cache-synthetic",
		"max_tokens":  64,
		"temperature": 0,
		"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "repeat native text"}}},
		},
	}
	headers := map[string]string{requestCacheHeader: "use"}
	first := doProxyRequest(t, env.engine, "/v1/messages", body, headers)
	second := doProxyRequest(t, env.engine, "/v1/messages", body, headers)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("native Anthropic statuses=%d/%d bodies=%s / %s", first.Code, second.Code, first.Body.String(), second.Body.String())
	}
	if calls.Load() != 1 {
		t.Fatalf("repeated native Anthropic text request reached upstream %d times; want 1", calls.Load())
	}
	if first.Body.String() != requestCacheNativeAnthropicReply || second.Body.String() != requestCacheNativeAnthropicReply {
		t.Fatal("native Anthropic cache replay changed the final reply bytes")
	}
	if second.Header().Get(requestCacheHeader) != "hit" {
		t.Fatalf("native Anthropic cache status=%q; want hit", second.Header().Get(requestCacheHeader))
	}
	firstID, secondID := first.Header().Get(requestIDHeader), second.Header().Get(requestIDHeader)
	if !requestTraceIDPattern.MatchString(firstID) || !requestTraceIDPattern.MatchString(secondID) || firstID == secondID {
		t.Fatalf("native Anthropic requests need fresh gateway IDs: %q / %q", firstID, secondID)
	}
	hitTrace := awaitRequestTrace(t, env, secondID)
	if hitTrace.CacheStatus != "hit" || hitTrace.CacheReason != "reused" || hitTrace.AttemptCount != 0 ||
		hitTrace.UpstreamAttemptCount != 0 || len(hitTrace.Attempts) != 0 {
		t.Fatalf("native Anthropic cache hit invented upstream attempts: %+v", hitTrace)
	}
}

func TestRequestCacheHTTPPublicChannelChangeInvalidatesWarmCache(t *testing.T) {
	var calls atomic.Int64
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, requestCacheOpenAIReply)
	}))
	env := setupRequestCacheEnv(t, upstream.URL, true)
	createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "routing-cache-owner"})
	channel := requestCacheOnlyChannel(t, env)
	body := requestCacheTestBody()
	requestCacheWarmAndAssertHit(t, env, "/v1/chat/completions", body)
	before := env.server.requestCache.stats(time.Now()).Invalidations["routing_changed"]

	session := requestCacheWebSession(env.server, "routing-change", model.WebRoleAdmin, 0)
	response := requestCacheAdminRequest(t, env, http.MethodPost,
		fmt.Sprintf("/admin/channels/%d/models", channel.ID), session,
		map[string]any{"models": []model.ModelEntry{{Model: "gpt-4o-cache-extra"}}})
	if response.Code != http.StatusOK {
		t.Fatalf("public channel update failed: %d %s", response.Code, response.Body.String())
	}
	next := doProxyRequest(t, env.engine, "/v1/chat/completions", body, map[string]string{requestCacheHeader: "use"})
	if next.Code != http.StatusOK || next.Header().Get(requestCacheHeader) == "hit" || calls.Load() != 2 {
		t.Fatalf("channel change reused old cache: status=%d header=%q calls=%d body=%s",
			next.Code, next.Header().Get(requestCacheHeader), calls.Load(), next.Body.String())
	}
	if got := env.server.requestCache.stats(time.Now()).Invalidations["routing_changed"]; got != before+1 {
		t.Fatalf("routing invalidation count=%d; want %d", got, before+1)
	}
}

func TestRequestCacheHTTPRuntimeCooldownRefreshKeepsSemanticCache(t *testing.T) {
	var calls atomic.Int64
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, requestCacheOpenAIReply)
	}))
	env := setupRequestCacheEnv(t, upstream.URL, true)
	createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "runtime-cache-owner"})
	channel := requestCacheOnlyChannel(t, env)
	store := env.store.(*sqlstore.SQLStore)
	// Make the timestamp change deterministic without sleeping for SQL seconds.
	if _, err := store.ExecContext(context.Background(), `UPDATE channels SET updated_at=1 WHERE id=?`, channel.ID); err != nil {
		t.Fatal(err)
	}
	env.server.getChannelCache().InvalidateCache()
	requestCacheWarmAndAssertHit(t, env, "/v1/chat/completions", requestCacheTestBody())
	if err := store.SetChannelCooldown(context.Background(), channel.ID, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.ResetChannelCooldown(context.Background(), channel.ID); err != nil {
		t.Fatal(err)
	}
	env.server.invalidateChannelRelatedCache(channel.ID)
	// This is the same storage-only snapshot reload used after cache TTL expiry,
	// without the public callback reserved for configured channel changes.
	env.server.getChannelCache().InvalidateCache()
	hit := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), map[string]string{requestCacheHeader: "use"})
	if hit.Code != http.StatusOK || hit.Header().Get(requestCacheHeader) != "hit" || calls.Load() != 1 {
		t.Fatalf("routine cooldown metadata forced a new upstream call: status=%d calls=%d body=%s", hit.Code, calls.Load(), hit.Body.String())
	}
}

func TestRequestCacheHTTPPublicKeyChangeInvalidatesWarmCache(t *testing.T) {
	var calls atomic.Int64
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, requestCacheOpenAIReply)
	}))
	env := setupRequestCacheEnv(t, upstream.URL, true)
	createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "key-cache-owner"})
	channel := requestCacheOnlyChannel(t, env)
	if err := env.store.CreateAPIKeysBatch(context.Background(), []*model.APIKey{{
		ChannelID: channel.ID, KeyIndex: 1, APIKey: "synthetic-secondary-upstream-key",
	}}); err != nil {
		t.Fatal(err)
	}
	env.server.InvalidateChannelListCache()
	body := requestCacheTestBody()
	requestCacheWarmAndAssertHit(t, env, "/v1/chat/completions", body)
	before := env.server.requestCache.stats(time.Now()).Invalidations["keys_changed"]

	session := requestCacheWebSession(env.server, "key-change", model.WebRoleAdmin, 0)
	response := requestCacheAdminRequest(t, env, http.MethodPost,
		fmt.Sprintf("/admin/channels/%d/key-disable", channel.ID), session,
		map[string]any{"key_index": 0})
	if response.Code != http.StatusOK {
		t.Fatalf("public key update failed: %d %s", response.Code, response.Body.String())
	}
	next := doProxyRequest(t, env.engine, "/v1/chat/completions", body, map[string]string{requestCacheHeader: "use"})
	if next.Code != http.StatusOK || next.Header().Get(requestCacheHeader) == "hit" || calls.Load() != 2 {
		t.Fatalf("key change reused old cache: status=%d header=%q calls=%d body=%s",
			next.Code, next.Header().Get(requestCacheHeader), calls.Load(), next.Body.String())
	}
	if got := env.server.requestCache.stats(time.Now()).Invalidations["keys_changed"]; got != before+1 {
		t.Fatalf("key invalidation count=%d; want %d", got, before+1)
	}
}

func TestRequestCacheHTTPUnifiedModelReloadInvalidatesWarmCache(t *testing.T) {
	var calls atomic.Int64
	receivedModels := make(chan string, 4)
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			receivedModels <- ""
		} else {
			receivedModels <- request.Model
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, requestCacheOpenAIReply)
	}))
	env := setupRequestCacheEnv(t, upstream.URL, true)
	createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "alias-cache-owner"})
	channel := requestCacheOnlyChannel(t, env)
	channel.ModelEntries = append(channel.ModelEntries, model.ModelEntry{Model: "gpt-4o-next"})
	if _, err := env.store.UpdateConfig(context.Background(), channel.ID, channel); err != nil {
		t.Fatal(err)
	}
	env.server.InvalidateChannelListCache()

	session := requestCacheWebSession(env.server, "alias-change", model.WebRoleAdmin, 0)
	updateAliases := func(group model.ModelAliasGroup) {
		t.Helper()
		groups, err := json.Marshal([]model.ModelAliasGroup{group})
		if err != nil {
			t.Fatal(err)
		}
		response := requestCacheAdminRequest(t, env, http.MethodPost, "/admin/settings/batch", session,
			map[string]string{modelAliasGroupsSettingKey: string(groups)})
		if response.Code != http.StatusOK {
			t.Fatalf("public model alias update failed: %d %s", response.Code, response.Body.String())
		}
	}
	updateAliases(model.ModelAliasGroup{Canonical: "route-alias", Aliases: []string{"gpt-4o"}, Enabled: true})
	body := requestCacheTestBody()
	body["model"] = "route-alias"
	requestCacheWarmAndAssertHit(t, env, "/v1/chat/completions", body)
	before := env.server.requestCache.stats(time.Now()).Invalidations["aliases_changed"]

	updateAliases(model.ModelAliasGroup{Canonical: "gpt-4o-next", Aliases: []string{"route-alias"}, Enabled: true})
	next := doProxyRequest(t, env.engine, "/v1/chat/completions", body, map[string]string{requestCacheHeader: "use"})
	if next.Code != http.StatusOK || next.Header().Get(requestCacheHeader) == "hit" || calls.Load() != 2 {
		t.Fatalf("unified model reload reused old cache: status=%d header=%q calls=%d body=%s",
			next.Code, next.Header().Get(requestCacheHeader), calls.Load(), next.Body.String())
	}
	if got := <-receivedModels; got != "gpt-4o" {
		t.Fatalf("first unified model resolved to %q; want gpt-4o", got)
	}
	if got := <-receivedModels; got != "gpt-4o-next" {
		t.Fatalf("reloaded unified model resolved to %q; want gpt-4o-next", got)
	}
	if got := env.server.requestCache.stats(time.Now()).Invalidations["aliases_changed"]; got != before+1 {
		t.Fatalf("alias invalidation count=%d; want %d", got, before+1)
	}
}

func TestRequestCacheHTTPPermissionReloadInvalidatesWarmCache(t *testing.T) {
	var calls atomic.Int64
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, requestCacheOpenAIReply)
	}))
	env := setupRequestCacheEnv(t, upstream.URL, true)
	token := &model.AuthToken{Description: "permission-cache-owner", AllowedModels: []string{"gpt-4o"}}
	createRequestCacheToken(t, env, "test-api-key", token)
	body := requestCacheTestBody()
	requestCacheWarmAndAssertHit(t, env, "/v1/chat/completions", body)
	before := env.server.requestCache.stats(time.Now()).Invalidations["permissions_changed"]

	session := requestCacheWebSession(env.server, "permission-change", model.WebRoleAdmin, 0)
	response := requestCacheAdminRequest(t, env, http.MethodPut,
		fmt.Sprintf("/admin/auth-tokens/%d", token.ID), session,
		map[string]any{"allowed_models": []string{"gpt-4o", "synthetic-extra"}})
	if response.Code != http.StatusOK {
		t.Fatalf("public token permission update failed: %d %s", response.Code, response.Body.String())
	}
	next := doProxyRequest(t, env.engine, "/v1/chat/completions", body, map[string]string{requestCacheHeader: "use"})
	if next.Code != http.StatusOK || next.Header().Get(requestCacheHeader) == "hit" || calls.Load() != 2 {
		t.Fatalf("permission change reused old cache: status=%d header=%q calls=%d body=%s",
			next.Code, next.Header().Get(requestCacheHeader), calls.Load(), next.Body.String())
	}
	if got := env.server.requestCache.stats(time.Now()).Invalidations["permissions_changed"]; got != before+1 {
		t.Fatalf("permission invalidation count=%d; want %d", got, before+1)
	}
}

func TestRequestCacheHTTPUnsafeSuccessRepliesAreNotRetained(t *testing.T) {
	cases := []struct {
		name   string
		reason string
		reply  string
		header http.Header
	}{
		{
			name:   "explicit no-store",
			reason: "response_no_store",
			reply:  requestCacheOpenAIReply,
			header: http.Header{"Cache-Control": []string{"no-store"}},
		},
		{
			name:   "incomplete success",
			reason: "unsafe_response",
			reply:  requestCacheUnsafeOpenAIReply,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				for name, values := range tc.header {
					w.Header()[name] = append([]string(nil), values...)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.reply)
			}))
			env := setupRequestCacheEnv(t, upstream.URL, true)
			createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "unsafe-cache-owner"})
			body := requestCacheTestBody()
			for range 2 {
				response := doProxyRequest(t, env.engine, "/v1/chat/completions", body,
					map[string]string{requestCacheHeader: "use"})
				if response.Code != http.StatusOK || response.Header().Get(requestCacheHeader) == "hit" {
					t.Fatalf("unsafe reply changed forwarding: status=%d header=%q body=%s",
						response.Code, response.Header().Get(requestCacheHeader), response.Body.String())
				}
				trace := awaitRequestTrace(t, env, response.Header().Get(requestIDHeader))
				if trace.CacheStatus != "miss" || trace.CacheReason != tc.reason {
					t.Fatalf("unsafe response trace=%+v; want miss/%s", trace, tc.reason)
				}
			}
			if calls.Load() != 2 {
				t.Fatalf("unsafe replies were retained after %d upstream calls; want 2", calls.Load())
			}
		})
	}
}
