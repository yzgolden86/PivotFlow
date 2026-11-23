package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/protocol"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func modelRoutesTestEngine(t *testing.T, s *Server) *gin.Engine {
	t.Helper()
	if s.authService == nil {
		s.authService = newTestAuthService(t)
	}
	injectAdminToken(s.authService, "model-routes-admin", time.Now().Add(time.Hour))
	g := gin.New()
	s.SetupRoutes(g)
	return g
}

func readModelRouteAPI(t *testing.T, g *gin.Engine, path string, status int, target any) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer model-routes-admin")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("GET %s: status=%d, want %d; body=%s", path, w.Code, status, w.Body.String())
	}
	if target != nil {
		mustUnmarshalAPIResponseData(t, w.Body.Bytes(), target)
	}
	return w
}

type modelInventoryTestView struct {
	Version int `json:"version"`
	Total   int `json:"total"`
	Limit   int `json:"limit"`
	Offset  int `json:"offset"`
	Items   []struct {
		Model              string   `json:"model"`
		Kind               string   `json:"kind"`
		Aliases            []string `json:"aliases"`
		AliasCount         int      `json:"alias_count"`
		ConfiguredChannels int      `json:"configured_channels"`
		EnabledChannels    int      `json:"enabled_channels"`
	} `json:"items"`
}

type modelDiagnosticTestView struct {
	Version        int      `json:"version"`
	Model          string   `json:"model"`
	CanonicalModel string   `json:"canonical_model"`
	GroupNames     []string `json:"group_names"`
	GroupNameCount int      `json:"group_name_count"`
	Total          int      `json:"total"`
	Warnings       []string `json:"warnings"`
	Policy         struct {
		PoolMode       string `json:"pool_mode"`
		CandidateCount int    `json:"candidate_count"`
		ActualTotal    int64  `json:"actual_total_requests"`
		ActualKnown    bool   `json:"actual_known"`
	} `json:"policy"`
	Token struct {
		ID        int64                   `json:"id"`
		Status    string                  `json:"status"`
		Admission string                  `json:"admission"`
		Reasons   []RouteDiagnosticReason `json:"reasons"`
	} `json:"token"`
	Channels []struct {
		ChannelRouteDiagnostic
		Attemptable   bool   `json:"attemptable"`
		AttemptStatus string `json:"attempt_status"`
		URLs          []struct {
			Index     int      `json:"index"`
			Status    string   `json:"status"`
			Protocols []string `json:"protocols"`
		} `json:"urls"`
	} `json:"channels"`
}

func TestModelRouteInventoryPaginationFullAliasSearchAndEmptyGroups(t *testing.T) {
	s, _, cleanup := setupAdminTestServer(t)
	defer cleanup()
	c := createDiagnosticChannel(t, s, "inventory", 1, 1)
	changeDiagnosticConfig(t, s, c.ID, func(cfg *model.Config) {
		cfg.ModelEntries = nil
		for i := range 105 {
			cfg.ModelEntries = append(cfg.ModelEntries, model.ModelEntry{Model: fmt.Sprintf("m%03d", i)})
		}
		cfg.ModelEntries = append(cfg.ModelEntries, model.ModelEntry{Model: "m-stopped", Disabled: true}, model.ModelEntry{Model: "*"})
	})
	aliases := []string{"alias-0", "alias-1", "alias-2", "alias-3", "alias-4", "alias-5", "alias-6", "alias-7", "alias-8", "last-current-alias"}
	s.modelAliases = newModelAliasRegistry([]model.ModelAliasGroup{
		{Canonical: "m000", Aliases: aliases, Enabled: true},
		{Canonical: "empty-group", Aliases: []string{"empty-alias"}, Enabled: true},
	})
	g := modelRoutesTestEngine(t, s)
	var first, last, search, empty, stopped, missing modelInventoryTestView
	readModelRouteAPI(t, g, "/admin/routing/models", 200, &first)
	if first.Version != 1 || first.Total != 108 || first.Limit != 50 || len(first.Items) != 50 {
		t.Fatalf("default bounded inventory: %+v", first)
	}
	readModelRouteAPI(t, g, "/admin/routing/models?limit=100&offset=100", 200, &last)
	if last.Total != 108 || len(last.Items) != 8 || last.Offset != 100 {
		t.Fatalf("last page: %+v", last)
	}
	readModelRouteAPI(t, g, "/admin/routing/models?search=last-current-alias", 200, &search)
	if search.Total != 1 || search.Items[0].Model != "m000" || search.Items[0].AliasCount != 10 || len(search.Items[0].Aliases) != 8 {
		t.Fatalf("search must cover aliases beyond preview: %+v", search)
	}
	readModelRouteAPI(t, g, "/admin/routing/models?search=empty-group", 200, &empty)
	if empty.Total != 1 || empty.Items[0].ConfiguredChannels != 0 || empty.Items[0].EnabledChannels != 0 {
		t.Fatalf("empty configured group: %+v", empty)
	}
	readModelRouteAPI(t, g, "/admin/routing/models?search=m-stopped", 200, &stopped)
	if stopped.Total != 1 || stopped.Items[0].ConfiguredChannels != 1 || stopped.Items[0].EnabledChannels != 0 {
		t.Fatalf("disabled model coverage: %+v", stopped)
	}
	readModelRouteAPI(t, g, "/admin/routing/models?search=not-configured", 200, &missing)
	if missing.Total != 0 || missing.Items == nil {
		t.Fatalf("empty result must be an array: %+v", missing)
	}
}

func TestModelRouteDiagnosticsRelatedChannelsAndFullSetPagination(t *testing.T) {
	s, _, cleanup := setupAdminTestServer(t)
	defer cleanup()
	direct := createDiagnosticChannel(t, s, "direct", 10, 3)
	alias := createDiagnosticChannel(t, s, "alias", 10, 1)
	disabled := createDiagnosticChannel(t, s, "disabled", 20, 1)
	wildcard := createDiagnosticChannel(t, s, "wildcard", 10, 1)
	unrelated := createDiagnosticChannel(t, s, "unrelated", 1, 1)
	changeDiagnosticConfig(t, s, alias.ID, func(c *model.Config) { c.ModelEntries = []model.ModelEntry{{Model: "vendor/gpt-route"}} })
	changeDiagnosticConfig(t, s, disabled.ID, func(c *model.Config) { c.ModelEntries[0].Disabled = true })
	changeDiagnosticConfig(t, s, wildcard.ID, func(c *model.Config) { c.ModelEntries = []model.ModelEntry{{Model: "*"}} })
	changeDiagnosticConfig(t, s, unrelated.ID, func(c *model.Config) { c.ModelEntries = []model.ModelEntry{{Model: "other"}} })
	s.modelAliases = newModelAliasRegistry([]model.ModelAliasGroup{{Canonical: "gpt-route", Aliases: []string{"vendor/gpt-route"}, Enabled: true}})
	g := modelRoutesTestEngine(t, s)
	var result modelDiagnosticTestView
	readModelRouteAPI(t, g, "/admin/routing/diagnostics?model=gpt-route&client_protocol=openai&limit=1", 200, &result)
	if result.Version != 1 || result.Total != 4 || result.Policy.CandidateCount != 2 || len(result.Channels) != 1 {
		t.Fatalf("related set/page: %+v", result)
	}
	if result.Channels[0].ChannelID != direct.ID || result.Channels[0].EstimatedTrafficShare != .75 {
		t.Fatalf("pagination must retain full-set 3/4 reference: %+v", result.Channels)
	}
	readModelRouteAPI(t, g, "/admin/routing/diagnostics?model=gpt-route&client_protocol=openai&offset=2", 200, &result)
	if len(result.Channels) != 2 {
		t.Fatalf("excluded page: %+v", result.Channels)
	}
	for _, c := range result.Channels {
		if c.Candidate {
			t.Fatalf("excluded related channel unexpectedly candidate: %+v", c)
		}
	}
	readModelRouteAPI(t, g, "/admin/routing/diagnostics?model=absent&client_protocol=openai", 200, &result)
	if result.Total != 1 || result.Channels[0].ChannelID != wildcard.ID {
		t.Fatalf("unknown names may have wildcard coverage only: %+v", result)
	}
}

func TestModelRouteAPIRejectsInvalidRepeatedAndUnsafeQueries(t *testing.T) {
	s, _, cleanup := setupAdminTestServer(t)
	defer cleanup()
	g := modelRoutesTestEngine(t, s)
	paths := []string{
		"/admin/routing/diagnostics", "/admin/routing/diagnostics?model=x&model=y",
		"/admin/routing/diagnostics?model=x&client_protocol=other", "/admin/routing/diagnostics?model=x&client_protocol=openai&client_protocol=codex",
		"/admin/routing/diagnostics?model=x&token_id=0", "/admin/routing/diagnostics?model=x&token_id=invalid",
		"/admin/routing/diagnostics?model=" + url.QueryEscape(strings.Repeat("模", 513)),
		"/admin/routing/diagnostics?model=x%0Ay", "/admin/routing/diagnostics?model=x%00",
		"/admin/routing/models?search=x&search=y", "/admin/routing/models?limit=101",
		"/admin/routing/models?offset=-1", "/admin/routing/models?limit=1&limit=2",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) { readModelRouteAPI(t, g, path, 400, nil) })
	}
	readModelRouteAPI(t, g, "/admin/routing/diagnostics?model=x&token_id=999", 404, nil)
}

func TestModelRouteDiagnosticsAttemptStageScopeAndCapabilityAreReadOnly(t *testing.T) {
	s, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	keyScope := createDiagnosticChannel(t, s, "key-scope", 10, 1)
	noKey := createDiagnosticChannel(t, s, "no-key", 10, 0)
	unsupported := createDiagnosticChannel(t, s, "unsupported", 10, 1)
	if err := store.UpdateAPIKeyMetadata(context.Background(), keyScope.ID, map[int]model.APIKey{0: {AllowedModels: []string{"vendor/gpt-route"}}}); err != nil {
		t.Fatal(err)
	}
	s.modelAliases = newModelAliasRegistry([]model.ModelAliasGroup{{Canonical: "gpt-route", Aliases: []string{"vendor/gpt-route"}, Enabled: true}})
	cacheKey := protocolCapabilityKey{channelID: unsupported.ID, baseURL: unsupported.GetURLs()[0], clientProtocol: protocol.OpenAI, requestFamily: protocol.RequestFamilyChatCompletions}
	s.protocolCapabilities.set(cacheKey, protocolUnsupported)
	g := modelRoutesTestEngine(t, s)
	var result modelDiagnosticTestView
	readModelRouteAPI(t, g, "/admin/routing/diagnostics?model=gpt-route", 200, &result)
	if result.Policy.CandidateCount != 3 {
		t.Fatalf("attempt gates must retain real candidate membership: %+v", result)
	}
	for _, c := range result.Channels {
		if !c.Candidate || c.Attemptable || c.AttemptStatus != "blocked" {
			t.Fatalf("candidate without attempt: %+v", c)
		}
		var code string
		switch c.ChannelID {
		case keyScope.ID:
			code = "key_model_scope"
		case noKey.ID:
			code = "no_enabled_key"
		case unsupported.ID:
			code = "no_compatible_protocol"
		}
		if !slices.ContainsFunc(c.Reasons, func(r RouteDiagnosticReason) bool { return r.Code == code && r.Blocking }) {
			t.Fatalf("missing %s reason: %+v", code, c)
		}
	}
	s.protocolCapabilities.mu.Lock()
	entry := s.protocolCapabilities.entries[cacheKey]
	entry.retryAfter = time.Now().Add(-time.Second)
	s.protocolCapabilities.entries[cacheKey] = entry
	s.protocolCapabilities.mu.Unlock()
	readModelRouteAPI(t, g, "/admin/routing/diagnostics?model=gpt-route", 200, &result)
	for _, c := range result.Channels {
		if c.ChannelID == unsupported.ID && (!c.Attemptable || c.URLs[0].Status != "unobserved") {
			t.Fatalf("expired capability permits negotiation: %+v", c)
		}
	}
	s.protocolCapabilities.mu.Lock()
	_, retained := s.protocolCapabilities.entries[cacheKey]
	s.protocolCapabilities.mu.Unlock()
	if !retained {
		t.Fatal("reading diagnostics removed the expired capability sentinel")
	}
}

func TestModelRouteDiagnosticsUseLiveTokenAdmissionAndNeverBroadenMissingScope(t *testing.T) {
	s, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	allowed := createDiagnosticChannel(t, s, "allowed", 10, 1)
	denied := createDiagnosticChannel(t, s, "denied", 10, 1)
	s.modelAliases = newModelAliasRegistry([]model.ModelAliasGroup{{Canonical: "gpt-route", Aliases: []string{"vendor/gpt-route"}, Enabled: true}})
	token := &model.AuthToken{Token: "model-route-token", IsActive: true, AllowedModels: []string{"different"}, AllowedChannelIDs: []int64{allowed.ID}}
	if err := store.CreateAuthToken(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	s.authService = newTestAuthService(t)
	s.authService.store = store
	s.authService.modelAliasCandidates = s.modelAliases.namesFor
	if err := s.authService.ReloadAuthTokens(); err != nil {
		t.Fatal(err)
	}
	g := modelRoutesTestEngine(t, s)
	path := fmt.Sprintf("/admin/routing/diagnostics?model=gpt-route&token_id=%d", token.ID)
	var result modelDiagnosticTestView
	readModelRouteAPI(t, g, path, 200, &result)
	if result.Token.Admission != "blocked" || !slices.ContainsFunc(result.Token.Reasons, func(r RouteDiagnosticReason) bool { return r.Code == "token_model_restriction" }) {
		t.Fatalf("model admission: %+v", result.Token)
	}
	hash := token.Token
	s.authService.authTokensMux.Lock()
	s.authService.authTokenModels[hash] = []string{"vendor/gpt-route"}
	s.authService.authTokenProtocols = map[string][]string{hash: {"codex"}}
	s.authService.authTokenChannels[hash] = mustChannelRestriction(t, "deny", denied.ID)
	s.authService.authTokensMux.Unlock()
	readModelRouteAPI(t, g, path, 200, &result)
	if !slices.ContainsFunc(result.Token.Reasons, func(r RouteDiagnosticReason) bool { return r.Code == "token_protocol_restriction" }) {
		t.Fatalf("live protocol restriction: %+v", result.Token)
	}
	s.authService.authTokensMux.Lock()
	delete(s.authService.authTokenProtocols, hash)
	s.authService.authTokenCostLimits[hash] = tokenCostLimit{usedMicroUSD: 1000000, limitMicroUSD: 1000000}
	s.authService.authTokenMaxConns[hash] = 1
	s.authService.authTokenActiveReqs[hash] = 1
	s.authService.authTokensMux.Unlock()
	readModelRouteAPI(t, g, path, 200, &result)
	for _, code := range []string{"token_cost_limit", "token_concurrency_limit"} {
		if !slices.ContainsFunc(result.Token.Reasons, func(r RouteDiagnosticReason) bool { return r.Code == code }) {
			t.Fatalf("missing live %s: %+v", code, result.Token)
		}
	}
	if result.Policy.CandidateCount != 1 {
		t.Fatalf("live deny list must retain only allowed candidate: %+v", result)
	}
	s.authService.authTokensMux.Lock()
	delete(s.authService.authTokenHashes, token.ID)
	s.authService.authTokensMux.Unlock()
	readModelRouteAPI(t, g, path, 200, &result)
	if result.Token.Status != "not_loaded" || result.Token.Admission != "unknown" {
		t.Fatalf("not-loaded scope: %+v", result.Token)
	}
	for _, c := range result.Channels {
		if c.Attemptable {
			t.Fatalf("unknown token scope claimed attemptable: %+v", c)
		}
	}
}

type modelRouteFailureStore struct {
	storage.Store
	failConfigs bool
	failStats   bool
}

func (s modelRouteFailureStore) ListConfigs(ctx context.Context) ([]*model.Config, error) {
	if s.failConfigs {
		return nil, errors.New("SQL failed with credential-private-value")
	}
	return s.Store.ListConfigs(ctx)
}
func (s modelRouteFailureStore) GetStatsLite(ctx context.Context, from, to time.Time, filter *model.LogFilter) ([]model.StatsEntry, error) {
	if s.failStats {
		return nil, errors.New("SQL failed with credential-private-value")
	}
	return s.Store.GetStatsLite(ctx, from, to, filter)
}

func TestModelRouteAPIStorageFailureIsSafeAndHistoryFailureIndependent(t *testing.T) {
	s, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	createDiagnosticChannel(t, s, "safe-errors", 10, 1)
	g := modelRoutesTestEngine(t, s)
	s.store = modelRouteFailureStore{Store: store, failConfigs: true}
	for _, path := range []string{"/admin/routing/models", "/admin/routing/diagnostics?model=gpt-route"} {
		w := readModelRouteAPI(t, g, path, 500, nil)
		if strings.Contains(w.Body.String(), "credential-private-value") || strings.Contains(w.Body.String(), "SQL") {
			t.Fatalf("unsafe error body: %s", w.Body.String())
		}
	}
	s.store = modelRouteFailureStore{Store: store, failStats: true}
	var result modelDiagnosticTestView
	readModelRouteAPI(t, g, "/admin/routing/diagnostics?model=gpt-route", 200, &result)
	if !slices.Contains(result.Warnings, "history_unavailable") || result.Policy.ActualKnown || len(result.Channels) != 1 {
		t.Fatalf("history failure must not suppress current snapshot: %+v", result)
	}
}

func TestRequestTraceExactModelAndProtocolFilters(t *testing.T) {
	s, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	name := "model%_'quoted"
	now := time.Now().Add(-time.Second).UnixMilli()
	traces := []*model.RequestTrace{
		{RequestID: "pf_req_filter_one", StartedAt: now, AuthTokenID: 7, Model: name, ClientProtocol: "openai", Transport: "http", Outcome: "success", FinalStatus: 200},
		{RequestID: "pf_req_filter_prefix", StartedAt: now, AuthTokenID: 7, Model: name + "-next", ClientProtocol: "openai", Transport: "http", Outcome: "error", FinalStatus: 500},
		{RequestID: "pf_req_filter_protocol", StartedAt: now, AuthTokenID: 7, Model: name, ClientProtocol: "codex", Transport: "http", Outcome: "error", FinalStatus: 500},
		{RequestID: "pf_req_filter_token", StartedAt: now, AuthTokenID: 8, Model: name, ClientProtocol: "openai", Transport: "http", Outcome: "success", FinalStatus: 200},
	}
	if err := store.BatchAddRequestTraces(context.Background(), traces); err != nil {
		t.Fatal(err)
	}
	g := modelRoutesTestEngine(t, s)
	query := url.Values{"model": {name}, "client_protocol": {"openai"}, "auth_token_id": {"7"}}.Encode()
	var rows []requestTraceSummaryView
	readModelRouteAPI(t, g, "/admin/request-traces?"+query, 200, &rows)
	if len(rows) != 1 || rows[0].RequestID != "pf_req_filter_one" {
		t.Fatalf("exact list: %+v", rows)
	}
	var stats model.RequestTraceStats
	readModelRouteAPI(t, g, "/admin/request-traces/stats?"+query, 200, &stats)
	if stats.Requests != 1 || stats.Successes != 1 || stats.Errors != 0 {
		t.Fatalf("exact statistics: %+v", stats)
	}
	for _, bad := range []string{"model=" + url.QueryEscape(strings.Repeat("模", 192)), "model=x%0Ay", "model=x&model=y", "client_protocol=unknown", "client_protocol=openai&client_protocol=codex"} {
		readModelRouteAPI(t, g, "/admin/request-traces?"+bad, 400, nil)
	}
	// JSON decoding works before and after adding the filter fields; it exercises
	// actual SQL storage rather than asserting the builder's implementation.
	var exact model.RequestTraceFilter
	if err := json.Unmarshal([]byte(`{"model":"model%_'quoted","client_protocol":"openai","auth_token_id":7}`), &exact); err != nil {
		t.Fatal(err)
	}
	stored, count, err := store.ListRequestTraces(context.Background(), time.Time{}, time.Now(), 50, 0, &exact)
	if err != nil || count != 1 || len(stored) != 1 {
		t.Fatalf("bound SQL filters: count=%d rows=%d err=%v", count, len(stored), err)
	}
}
