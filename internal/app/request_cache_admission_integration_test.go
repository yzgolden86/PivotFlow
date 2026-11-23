package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/accounting"
	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestRequestCacheHTTPStillHonorsTokenAndGlobalConcurrency(t *testing.T) {
	for _, scope := range []string{"token", "global"} {
		t.Run(scope, func(t *testing.T) {
			var calls atomic.Int64
			upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, requestCacheOpenAIReply)
			}))
			env := setupRequestCacheEnv(t, upstream.URL, true)
			createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "owner", MaxConcurrency: 1})
			headers := map[string]string{requestCacheHeader: "use"}
			if response := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), headers); response.Code != 200 {
				t.Fatal("warmup failed")
			}
			before := env.server.requestCache.stats(time.Now())
			var release func()
			want := http.StatusTooManyRequests
			request := newJSONRequest(t, "POST", "/v1/chat/completions", requestCacheTestBody())
			request.Header.Set("Authorization", "Bearer test-api-key")
			request.Header.Set(requestCacheHeader, "use")
			if scope == "token" {
				var acquired bool
				release, _, _, acquired = env.server.authService.acquireTokenConcurrencySlot(model.HashToken("test-api-key"))
				if !acquired {
					t.Fatal("unable to occupy the synthetic token slot")
				}
			} else {
				// The isolated fixture has no active work after warmup. Its one
				// global lease models an already-admitted request.
				env.server.concurrencySem = make(chan struct{}, 1)
				var err error
				release, err = env.server.acquireConcurrencySlotForContext(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
				request = request.WithContext(ctx)
				want = http.StatusGatewayTimeout
			}
			defer func() {
				if release != nil {
					release()
				}
			}()
			response := httptest.NewRecorder()
			env.engine.ServeHTTP(response, request)
			after := env.server.requestCache.stats(time.Now())
			if response.Code != want || calls.Load() != 1 || after.Lookups != before.Lookups || after.Hits != before.Hits {
				t.Fatalf("cache escaped %s concurrency admission: status=%d calls=%d stats=%+v", scope, response.Code, calls.Load(), after)
			}
			release()
			release = nil
			if hit := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), headers); hit.Code != 200 || hit.Header().Get(requestCacheHeader) != "hit" || calls.Load() != 1 {
				t.Fatalf("released admission did not recover the existing cache: %d %s", hit.Code, hit.Body.String())
			}
		})
	}
}

func TestRequestCacheHTTPJournalFailureBlocksHitsUntilRecovered(t *testing.T) {
	billingDir := t.TempDir()
	var calls atomic.Int64
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, requestCacheOpenAIReply)
	}))
	env := setupRequestCacheEnv(t, upstream.URL, true)
	createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "owner"})
	journal, err := accounting.Open(filepath.Join(billingDir, "pending.db"))
	if err != nil {
		t.Fatal(err)
	}
	failure := &failingBillingJournal{billingJournal: journal}
	env.server.billing = newBillingService(env.store, failure)
	env.server.billing.start()
	t.Cleanup(func() {
		failure.mu.Lock()
		failure.fail = false
		failure.mu.Unlock()
	})
	headers := map[string]string{requestCacheHeader: "use"}
	if response := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), headers); response.Code != 200 {
		t.Fatal("warmup failed")
	}
	if err := env.server.billing.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	failure.mu.Lock()
	failure.fail = true
	failure.mu.Unlock()
	// A completion already admitted before a disk outage cannot be journaled.
	// Drive the real failure path, rather than directly setting the blocked flag.
	env.server.billing.record(model.TokenSettlement{ID: "synthetic-cache-outage", TokenHash: model.HashToken("test-api-key"), OccurredAt: time.Now().UnixMilli()})
	before := env.server.requestCache.stats(time.Now())
	response := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), headers)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "billing_unavailable") || calls.Load() != 1 || env.server.requestCache.stats(time.Now()).Lookups != before.Lookups {
		t.Fatalf("cache bypassed durable billing admission: status=%d calls=%d body=%s", response.Code, calls.Load(), response.Body.String())
	}
	failure.mu.Lock()
	failure.fail = false
	failure.mu.Unlock()
	if err := env.server.billing.flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hit := doProxyRequest(t, env.engine, "/v1/chat/completions", requestCacheTestBody(), headers); hit.Code != 200 || hit.Header().Get(requestCacheHeader) != "hit" || calls.Load() != 1 {
		t.Fatalf("cache did not recover after journal repair: %d %s", hit.Code, hit.Body.String())
	}
}

type cacheCancelingWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w cacheCancelingWriter) Write(body []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(body)
	w.cancel()
	return n, err
}

func TestRequestCacheHTTPCanceledHitHasNoSuccessfulSettlement(t *testing.T) {
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := newJSONRequest(t, "POST", "/v1/chat/completions", requestCacheTestBody()).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer test-api-key")
	request.Header.Set(requestCacheHeader, "use")
	writer := cacheCancelingWriter{httptest.NewRecorder(), cancel}
	env.engine.ServeHTTP(writer, request)
	trace := awaitRequestTrace(t, env, writer.Header().Get(requestIDHeader))
	token, err := env.store.GetAuthTokenByValue(context.Background(), model.HashToken("test-api-key"))
	if err != nil {
		t.Fatal(err)
	}
	if trace.Outcome != "canceled" || trace.CacheReason != "canceled" || trace.UpstreamAttemptCount != 0 || token.SuccessCount != 1 || calls.Load() != 1 || env.server.requestCache.stats(time.Now()).Hits != 0 {
		t.Fatalf("canceled cache delivery was counted as success: trace=%+v token_count=%d calls=%d", trace, token.SuccessCount, calls.Load())
	}
}

func TestRequestCacheHTTPActualConversionAndCustomRulesNeverStore(t *testing.T) {
	for _, mode := range []string{"conversion", "custom rule", "anyrouter"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int64
			upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if mode == "conversion" {
					if r.URL.Path != "/v1/messages" {
						t.Errorf("request did not use actual protocol conversion: %s", r.URL.Path)
					}
					_, _ = io.WriteString(w, `{"id":"msg-synthetic-conversion","type":"message","role":"assistant","model":"gpt-4o","content":[{"type":"text","text":"same answer"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":100,"output_tokens":10}}`)
				} else {
					if mode == "custom rule" && r.Header.Get("X-Synthetic-Rule") != "changed" {
						t.Error("custom rule did not run on the actual forward path")
					}
					_, _ = io.WriteString(w, requestCacheOpenAIReply)
				}
			}))
			env := setupRequestCacheEnv(t, upstream.URL, true)
			cfg, err := env.store.GetConfig(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}
			reason := "protocol_transform"
			switch mode {
			case "conversion":
				cfg.URLs[0].Protocols = []string{"anthropic"}
			case "custom rule":
				cfg.CustomRequestRules = &model.CustomRequestRules{Headers: []model.CustomHeaderRule{{Action: model.RuleActionOverride, Name: "X-Synthetic-Rule", Value: "changed"}}}
				reason = "custom_rules"
			case "anyrouter":
				cfg.Name = "synthetic-anyrouter"
			}
			if _, err := env.store.UpdateConfig(context.Background(), 1, cfg); err != nil {
				t.Fatal(err)
			}
			env.server.InvalidateChannelListCache()
			createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "owner"})
			body := requestCacheTestBody()
			body["max_tokens"] = 16
			for range 2 {
				response := doProxyRequest(t, env.engine, "/v1/chat/completions", body, map[string]string{requestCacheHeader: "use"})
				if response.Code != 200 || !strings.Contains(response.Body.String(), "same answer") {
					t.Fatalf("normal forwarding changed: %d %s", response.Code, response.Body.String())
				}
				trace := awaitRequestTrace(t, env, response.Header().Get(requestIDHeader))
				if trace.CacheReason != reason || trace.UpstreamAttemptCount != 1 {
					t.Fatalf("actual %s path lost cache bypass reason: %+v", mode, trace)
				}
			}
			if calls.Load() != 2 || env.server.requestCache.stats(time.Now()).Entries != 0 {
				t.Fatalf("%s path was cached: calls=%d", mode, calls.Load())
			}
		})
	}
}
