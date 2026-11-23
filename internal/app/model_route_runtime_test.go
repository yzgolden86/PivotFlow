package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func TestModelRouteDiagnosticsUseCachedKeyScopeThatActualAttemptUses(t *testing.T) {
	s, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	c := createDiagnosticChannel(t, s, "cached-scope", 10, 1)
	s.channelCache = storage.NewChannelCache(store, time.Hour)
	keys, err := s.getAPIKeys(context.Background(), c.ID)
	if err != nil || len(keys) != 1 || !keys[0].AllowsModel("gpt-route") {
		t.Fatalf("prime actual key cache: %v %v", keys, err)
	}
	if err := store.UpdateAPIKeyMetadata(context.Background(), c.ID, map[int]model.APIKey{0: {AllowedModels: []string{"different"}}}); err != nil {
		t.Fatal(err)
	}
	result, err := s.buildChannelRouteDiagnostics(context.Background(), c.ID, "gpt-route", "openai", 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Target.ModelEligibleKeyCount != 1 || !result.Target.Attemptable {
		t.Fatalf("diagnostic must observe actual cached scope until invalidation: %+v", result.Target)
	}
}

func TestModelRouteDiagnosticsAgreeWithSyntheticMappedFailoverHistory(t *testing.T) {
	var requests atomic.Int32
	primary := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"error":"retry channel"}`)
	}))
	fallback := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "vendor/gpt-route" {
			t.Errorf("forwarded alias identity=%v", body["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	env := setupProxyTestEnvWithSettings(t, []testChannel{
		{name: "primary", models: "gpt-route", priority: 100},
		{name: "mapped-fallback", models: "vendor/gpt-route", priority: 90},
	}, map[int]string{0: primary.URL, 1: fallback.URL}, map[string]string{"model_alias_groups": `[{"canonical":"gpt-route","aliases":["vendor/gpt-route"],"enabled":true}]`})
	injectAdminToken(env.server.authService, "model-routes-admin", time.Now().Add(time.Hour))
	var before modelDiagnosticTestView
	readModelRouteAPI(t, env.engine, "/admin/routing/diagnostics?model=gpt-route&client_protocol=openai", 200, &before)
	if before.Policy.CandidateCount != 2 || requests.Load() != 0 {
		t.Fatalf("initial read-only candidate snapshot: %+v hits=%d", before, requests.Load())
	}
	response := doProxyRequest(t, env.engine, "/v1/chat/completions", map[string]any{"model": "gpt-route", "messages": []any{map[string]any{"role": "user", "content": "hello"}}}, nil)
	if response.Code != 200 {
		t.Fatalf("synthetic request: %d %s", response.Code, response.Body.String())
	}
	trace := awaitRequestTrace(t, env, response.Header().Get(requestIDHeader))
	if trace.Model != "gpt-route" || trace.ClientProtocol != "openai" || len(trace.Attempts) != 2 {
		t.Fatalf("persisted identity/sequence: %+v", trace)
	}
	if trace.Attempts[0].ChannelID != before.Channels[0].ChannelID || trace.Attempts[1].ChannelID != before.Channels[1].ChannelID || trace.Attempts[1].ActualModel != "vendor/gpt-route" || trace.Attempts[1].UpstreamProtocol != "openai" {
		t.Fatalf("diagnosis differs from actual failover: before=%+v trace=%+v", before.Channels, trace.Attempts)
	}
	var rows []requestTraceSummaryView
	query := url.Values{"model": {"gpt-route"}, "client_protocol": {"openai"}}.Encode()
	readModelRouteAPI(t, env.engine, "/admin/request-traces?"+query, 200, &rows)
	if len(rows) != 1 || rows[0].RequestID != trace.RequestID {
		t.Fatalf("workspace exact history: %+v", rows)
	}
	if requests.Load() != 2 {
		t.Fatalf("diagnostic/history viewing sent upstream requests: %d", requests.Load())
	}
}

func TestModelRouteDiagnosticsCooldownProbeAndLongGroupPreview(t *testing.T) {
	s, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	c := createDiagnosticChannel(t, s, "probe", 10, 1)
	if err := store.SetKeyCooldown(context.Background(), c.ID, 0, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	aliases := make([]string, 60)
	for i := range aliases {
		aliases[i] = strings.Repeat("x", i+1)
	}
	s.modelAliases = newModelAliasRegistry([]model.ModelAliasGroup{{Canonical: "gpt-route", Aliases: aliases, Enabled: true}})
	g := modelRoutesTestEngine(t, s)
	var result modelDiagnosticTestView
	readModelRouteAPI(t, g, "/admin/routing/diagnostics?model=gpt-route", 200, &result)
	if result.Policy.PoolMode != "cooldown_fallback" || !result.Channels[0].Attemptable || result.Channels[0].AttemptStatus != "probe" || result.Channels[0].ActiveKeyCount != 0 {
		t.Fatalf("cooldown probe must be explicit: %+v", result)
	}
	if result.GroupNameCount != 61 || len(result.GroupNames) != 50 {
		t.Fatalf("bounded group preview: %+v", result)
	}
	longName := strings.Repeat("模", 192)
	changeDiagnosticConfig(t, s, c.ID, func(cfg *model.Config) { cfg.ModelEntries = []model.ModelEntry{{Model: longName}} })
	readModelRouteAPI(t, g, "/admin/routing/diagnostics?model="+url.QueryEscape(longName), 200, &result)
	if result.Policy.ActualKnown || !slices.Contains(result.Warnings, "history_model_identity_too_long") {
		t.Fatalf("long-name history limitation: %+v", result)
	}
}

func TestModelRouteDiagnosticsReadDoesNotPruneRPMState(t *testing.T) {
	s, _, cleanup := setupAdminTestServer(t)
	defer cleanup()
	c := createDiagnosticChannel(t, s, "capacity", 10, 1)
	changeDiagnosticConfig(t, s, c.ID, func(cfg *model.Config) { cfg.RPMLimit = 1; cfg.MaxConcurrency = 1 })
	now := time.Now()
	s.channelRPMLimiter = newChannelRPMLimiter(func() time.Time { return now })
	s.channelRPMLimiter.requests[c.ID] = []time.Time{now.Add(-2 * time.Minute), now}
	s.channelConcurrencyLimiter = newChannelConcurrencyLimiter()
	release, _, _, ok := s.channelConcurrencyLimiter.acquire(c.ID, 1)
	if !ok {
		t.Fatal("failed to reserve synthetic concurrent attempt")
	}
	defer release()
	result, err := s.buildChannelRouteDiagnostics(context.Background(), c.ID, "gpt-route", "openai", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Target.Candidate || result.Target.Attemptable || result.Target.CurrentRPM != 1 || result.Target.ActiveConcurrency != 1 {
		t.Fatalf("capacity belongs to attempt stage: %+v", result.Target)
	}
	if len(s.channelRPMLimiter.requests[c.ID]) != 2 || s.channelConcurrencyLimiter.snapshot(c.ID) != 1 {
		t.Fatal("reading diagnostics mutated RPM or concurrency reservations")
	}
}

func TestModelRouteDiagnosticsExpiredRuntimeTokenDoesNotRevokeState(t *testing.T) {
	s, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	c := createDiagnosticChannel(t, s, "expired-token", 10, 1)
	token := &model.AuthToken{Token: "synthetic-expired-route-token", IsActive: true}
	if err := store.CreateAuthToken(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	s.authService = newTestAuthService(t)
	s.authService.store = store
	if err := s.authService.ReloadAuthTokens(); err != nil {
		t.Fatal(err)
	}
	s.authService.authTokens[token.Token] = time.Now().Add(-time.Second).UnixMilli()
	result, err := s.buildChannelRouteDiagnostics(context.Background(), c.ID, "gpt-route", "openai", token.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Token.Status != "expired" || result.Target.Attemptable || result.Target.BlockedStage != "admission" {
		t.Fatalf("expired runtime admission: %+v", result)
	}
	if _, present := s.authService.authTokens[token.Token]; !present {
		t.Fatal("diagnostic revoked an expired token")
	}
}
