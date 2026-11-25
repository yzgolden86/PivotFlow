package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

const reconcileTestCanonical = "deepseek-v4.1-flash"

func setModelAliasGroupsForReconcileTest(t *testing.T, srv *Server, groups []model.ModelAliasGroup) string {
	t.Helper()
	raw, err := json.Marshal(groups)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := srv.configService.UpdateSetting(ctx, modelAliasGroupsSettingKey, string(raw)); err != nil {
		t.Fatal(err)
	}
	srv.applyLiveSettings(ctx, []string{modelAliasGroupsSettingKey})
	return string(raw)
}

func persistedModelAliasGroupsForReconcileTest(t *testing.T, srv *Server) []model.ModelAliasGroup {
	t.Helper()
	setting, err := srv.configService.GetSettingFresh(context.Background(), modelAliasGroupsSettingKey)
	if err != nil {
		t.Fatal(err)
	}
	var groups []model.ModelAliasGroup
	if err := json.Unmarshal([]byte(setting.Value), &groups); err != nil {
		t.Fatal(err)
	}
	return model.NormalizeModelAliasGroups(groups)
}

func visibleModelsForReconcileTest(t *testing.T, srv *Server) []string {
	t.Helper()
	c, w := newTestContext(t, newRequest(http.MethodGet, "/v1/models", nil))
	srv.handleListOpenAIModels(c)
	if w.Code != http.StatusOK {
		t.Fatalf("list models status=%d body=%s", w.Code, w.Body.String())
	}
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	mustUnmarshalJSON(t, w.Body.Bytes(), &response)
	models := make([]string, 0, len(response.Data))
	for _, item := range response.Data {
		models = append(models, item.ID)
	}
	sort.Strings(models)
	return models
}

func createReconcileTestChannel(t *testing.T, srv *Server, name string, enabled bool, entries ...model.ModelEntry) *model.Config {
	t.Helper()
	cfg, err := srv.store.CreateConfig(context.Background(), &model.Config{
		Name:         name,
		Enabled:      enabled,
		URLs:         model.ChannelURLs{{URL: "https://manual.example", Protocols: []string{"openai"}}},
		ModelEntries: entries,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.InvalidateChannelListCache()
	return cfg
}

func TestSiteModelRefreshReconcilesAliasesAcrossAllConfiguredChannels(t *testing.T) {
	const upstreamKey = "sk-alias-reconcile"
	initialModels := []string{
		"DeepSeek-V4.1-Flash",
		"other/DeepSeek-V4.1-Flash",
		"deepseek-v4.1-flash-thinking",
		"channel1/DeepSeek-V4.1-Flash-1017",
		"deepseek-v4.1-flash-1017",
		"opencode/deepseek-v4.1-flash-free",
	}
	aliases := append(append([]string(nil), initialModels...), "vendor/Manual-Never-Observed")
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "primary", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: initialModels},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{
		Canonical: reconcileTestCanonical,
		Aliases:   aliases,
		Enabled:   true,
	}})

	disabledChannel := createReconcileTestChannel(
		t,
		srv,
		"disabled manual topology",
		false,
		model.ModelEntry{Model: "opencode/deepseek-v4.1-flash-free"},
	)
	disabledEntryChannel := createReconcileTestChannel(
		t,
		srv,
		"disabled manual entry",
		true,
		model.ModelEntry{Model: "deepseek-v4.1-flash-1017", Disabled: true},
	)
	enabledManualChannel := createReconcileTestChannel(
		t,
		srv,
		"enabled manual topology",
		true,
		model.ModelEntry{Model: "channel1/DeepSeek-V4.1-Flash-1017"},
	)

	firstTask := newSiteTask("model_refresh", account.SiteID, account.ID, 1)
	srv.siteControl.refreshAccount(context.Background(), firstTask, account.ID, true)
	if firstTask.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("initial refresh status=%q error=%q", firstTask.Status, firstTask.Error)
	}

	adapter.modelsByKey[upstreamKey] = []string{"deepseek-v4.1-flash-thinking", "gpt-new"}
	secondTask := newSiteTask("model_refresh", account.SiteID, account.ID, 2)
	srv.siteControl.refreshAccount(context.Background(), secondTask, account.ID, true)
	if secondTask.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("authoritative refresh status=%q error=%q", secondTask.Status, secondTask.Error)
	}

	groups := persistedModelAliasGroupsForReconcileTest(t, srv)
	if len(groups) != 1 {
		t.Fatalf("alias groups=%+v, want one preserved group", groups)
	}
	if groups[0].Canonical != reconcileTestCanonical || !groups[0].Enabled {
		t.Fatalf("canonical group identity changed: %+v", groups[0])
	}
	wantAliases := []string{
		"deepseek-v4.1-flash-thinking",
		"channel1/DeepSeek-V4.1-Flash-1017",
		"deepseek-v4.1-flash-1017",
		"opencode/deepseek-v4.1-flash-free",
		"vendor/Manual-Never-Observed",
	}
	if !slices.Equal(groups[0].Aliases, wantAliases) {
		t.Fatalf("aliases=%v, want %v", groups[0].Aliases, wantAliases)
	}
	if got := visibleModelsForReconcileTest(t, srv); !slices.Equal(got, []string{reconcileTestCanonical, "gpt-new"}) {
		t.Fatalf("public models=%v, want canonical retained by surviving members", got)
	}

	disabled, err := srv.store.GetConfig(context.Background(), disabledChannel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || len(disabled.ModelEntries) != 1 || disabled.ModelEntries[0].Model != "opencode/deepseek-v4.1-flash-free" {
		t.Fatalf("disabled channel topology changed: %+v", disabled)
	}
	disabledEntry, err := srv.store.GetConfig(context.Background(), disabledEntryChannel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(disabledEntry.ModelEntries) != 1 || !disabledEntry.ModelEntries[0].Disabled {
		t.Fatalf("disabled model entry topology changed: %+v", disabledEntry)
	}
	enabledManual, err := srv.store.GetConfig(context.Background(), enabledManualChannel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabledManual.ModelEntries) != 1 || enabledManual.ModelEntries[0].Model != "channel1/DeepSeek-V4.1-Flash-1017" {
		t.Fatalf("enabled manual channel topology changed: %+v", enabledManual)
	}
}

func TestSiteModelRefreshPartialOrEmptyDiscoveryKeepsAliases(t *testing.T) {
	keptAliases := []string{"DeepSeek-V4.1-Flash", "deepseek-v4.1-flash-thinking"}

	t.Run("partial", func(t *testing.T) {
		const goodKey = "sk-partial-good"
		const badKey = "sk-partial-bad"
		adapter := siteSyncPreviewAdapter(
			[]provider.RoutingKeySnapshot{
				{ID: "good", Key: goodKey, Enabled: true},
				{ID: "bad", Key: badKey, Enabled: true},
			},
			map[string][]string{goodKey: {keptAliases[0]}},
		)
		adapter.errorsByKey[badKey] = &provider.Error{Code: provider.CodeRequestFailed, Message: "upstream unavailable"}
		srv, _, account := newSiteSyncPreviewFixture(t, adapter)
		setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{
			Canonical: reconcileTestCanonical,
			Aliases:   keptAliases,
			Enabled:   true,
		}})
		if err := srv.store.ReplaceSiteAccountModels(context.Background(), account.ID, []model.SiteAccountModel{
			{SiteAccountID: account.ID, Model: keptAliases[0], RouteType: "openai_chat", Source: "routing_key_models"},
			{SiteAccountID: account.ID, Model: keptAliases[1], RouteType: "openai_chat", Source: "routing_key_models"},
		}); err != nil {
			t.Fatal(err)
		}

		task := newSiteTask("model_refresh", account.SiteID, account.ID, 3)
		srv.siteControl.refreshAccount(context.Background(), task, account.ID, true)
		if task.Status != model.SiteTaskStatusPartial {
			t.Fatalf("partial refresh status=%q, want partial", task.Status)
		}
		groups := persistedModelAliasGroupsForReconcileTest(t, srv)
		if len(groups) != 1 || !slices.Equal(groups[0].Aliases, keptAliases) {
			t.Fatalf("partial refresh changed aliases: %+v", groups)
		}
	})

	t.Run("empty", func(t *testing.T) {
		const emptyKey = "sk-empty"
		adapter := siteSyncPreviewAdapter(
			[]provider.RoutingKeySnapshot{{ID: "empty", Key: emptyKey, Enabled: true}},
			map[string][]string{emptyKey: {}},
		)
		adapter.errorsByKey[emptyKey] = &provider.Error{Code: provider.CodeUnsupported, Message: "model discovery unavailable"}
		adapter.errorsByKey[""] = &provider.Error{Code: provider.CodeUnsupported, Message: "management model discovery unavailable"}
		srv, _, account := newSiteSyncPreviewFixture(t, adapter)
		setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{
			Canonical: reconcileTestCanonical,
			Aliases:   keptAliases,
			Enabled:   true,
		}})
		if err := srv.store.ReplaceSiteAccountModels(context.Background(), account.ID, []model.SiteAccountModel{
			{SiteAccountID: account.ID, Model: keptAliases[0], RouteType: "openai_chat", Source: "routing_key_models"},
			{SiteAccountID: account.ID, Model: keptAliases[1], RouteType: "openai_chat", Source: "routing_key_models"},
		}); err != nil {
			t.Fatal(err)
		}

		task := newSiteTask("model_refresh", account.SiteID, account.ID, 4)
		srv.siteControl.refreshAccount(context.Background(), task, account.ID, true)
		if task.Status != model.SiteTaskStatusFailed {
			t.Fatalf("empty refresh status=%q, want failed", task.Status)
		}
		groups := persistedModelAliasGroupsForReconcileTest(t, srv)
		if len(groups) != 1 || !slices.Equal(groups[0].Aliases, keptAliases) {
			t.Fatalf("empty discovery changed aliases: %+v", groups)
		}
	})
}

func TestSiteModelRefreshAuthoritativeEmptyKeySetRemovesStaleModels(t *testing.T) {
	const upstreamKey = "sk-authoritative-empty-keys"
	const removedModel = "vendor/vanishing-model"
	const grantedModel = "vendor/historical-grant"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "primary", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {grantedModel}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	ctx := context.Background()
	setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{
		Canonical: reconcileTestCanonical,
		Aliases:   []string{removedModel, grantedModel, "vendor/manual-never-observed"},
		Enabled:   true,
	}})

	initial := newSiteTask("model_refresh", account.SiteID, account.ID, 30)
	srv.siteControl.refreshAccount(ctx, initial, account.ID, true)
	if initial.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("initial refresh status=%q error=%q", initial.Status, initial.Error)
	}
	// Durable evidence from an earlier removed model survives until cleanup.
	// It has no remaining configured channel, unlike the historical grant.
	if err := srv.store.MergeSiteAccountModels(ctx, account.ID, []model.SiteAccountModel{
		{SiteAccountID: account.ID, Model: removedModel, Source: "routing_key_models"},
		{SiteAccountID: account.ID, Model: grantedModel, Source: "routing_key_models"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.CreateAuthToken(ctx, &model.AuthToken{
		Token:         model.HashToken("authoritative-empty-model-whitelist"),
		Description:   "retain historical model grant",
		IsActive:      true,
		AllowedModels: []string{grantedModel},
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.authService.ReloadAuthTokens(); err != nil {
		t.Fatal(err)
	}

	// The provider successfully reports no enabled keys. This is an
	// authoritative empty result, distinct from a failed or partial lookup.
	adapter.keys = []provider.RoutingKeySnapshot{}
	final := newSiteTask("model_refresh", account.SiteID, account.ID, 31)
	srv.siteControl.refreshAccount(ctx, final, account.ID, true)
	if final.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("empty authoritative refresh status=%q error=%q", final.Status, final.Error)
	}

	facts, err := srv.store.ListSiteAccountModels(ctx, model.SiteModelFilter{SiteAccountID: account.ID, IncludeDisabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("site model snapshot retained stale facts after authoritative empty sync: %+v", facts)
	}
	if got := visibleModelsForReconcileTest(t, srv); len(got) != 0 {
		t.Fatalf("public models after authoritative empty sync=%v, want none", got)
	}
	inventory, err := srv.modelRouteInventory(ctx, "", 50, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Total != 0 {
		t.Fatalf("enabled model inventory after authoritative empty sync=%+v, want empty", inventory)
	}
	groups := persistedModelAliasGroupsForReconcileTest(t, srv)
	wantAliases := []string{grantedModel, "vendor/manual-never-observed"}
	if len(groups) != 1 || !slices.Equal(groups[0].Aliases, wantAliases) {
		t.Fatalf("alias grants/manual entries after empty sync=%+v, want %v", groups, wantAliases)
	}
}

func TestSiteModelRefreshLeavesConfiguredEmptyAliasGroupAndHidesCanonical(t *testing.T) {
	const upstreamKey = "sk-canonical-disappears"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "primary", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"DeepSeek-V4.1-Flash"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{
		Canonical: reconcileTestCanonical,
		Aliases:   []string{"DeepSeek-V4.1-Flash"},
		Enabled:   true,
	}})

	firstTask := newSiteTask("model_refresh", account.SiteID, account.ID, 5)
	srv.siteControl.refreshAccount(context.Background(), firstTask, account.ID, true)
	adapter.modelsByKey[upstreamKey] = []string{"gpt-new"}
	secondTask := newSiteTask("model_refresh", account.SiteID, account.ID, 6)
	srv.siteControl.refreshAccount(context.Background(), secondTask, account.ID, true)

	groups := persistedModelAliasGroupsForReconcileTest(t, srv)
	if len(groups) != 1 || groups[0].Canonical != reconcileTestCanonical || !groups[0].Enabled {
		t.Fatalf("configured empty group identity changed: %+v", groups)
	}
	if len(groups[0].Aliases) != 0 {
		t.Fatalf("vanished aliases remain: %v", groups[0].Aliases)
	}
	if got := visibleModelsForReconcileTest(t, srv); !slices.Equal(got, []string{"gpt-new"}) {
		t.Fatalf("public models=%v, want canonical hidden with no real member", got)
	}
}

func TestSiteModelRefreshRetainsAliasReferencedByTokenWhitelist(t *testing.T) {
	const upstreamKey = "sk-token-whitelist-retention"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "primary", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"DeepSeek-V4.1-Flash"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{
		Canonical: reconcileTestCanonical,
		Aliases:   []string{"DeepSeek-V4.1-Flash", "deepseek-v4.1-flash-thinking"},
		Enabled:   true,
	}})
	createReconcileTestChannel(t, srv, "token survivor", true, model.ModelEntry{Model: "deepseek-v4.1-flash-thinking"})

	tokenHash := model.HashToken("token-whitelist-retention")
	if err := srv.store.CreateAuthToken(context.Background(), &model.AuthToken{
		Token:         tokenHash,
		Description:   "alias whitelist retention",
		IsActive:      true,
		AllowedModels: []string{"DeepSeek-V4.1-Flash"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := srv.authService.ReloadAuthTokens(); err != nil {
		t.Fatal(err)
	}

	firstTask := newSiteTask("model_refresh", account.SiteID, account.ID, 9)
	srv.siteControl.refreshAccount(context.Background(), firstTask, account.ID, true)
	adapter.modelsByKey[upstreamKey] = []string{"gpt-new"}
	secondTask := newSiteTask("model_refresh", account.SiteID, account.ID, 10)
	srv.siteControl.refreshAccount(context.Background(), secondTask, account.ID, true)

	groups := persistedModelAliasGroupsForReconcileTest(t, srv)
	if len(groups) != 1 || !slices.Contains(groups[0].Aliases, "DeepSeek-V4.1-Flash") {
		t.Fatalf("token-referenced alias was removed: %+v", groups)
	}
	if !srv.authService.IsModelAllowed(tokenHash, reconcileTestCanonical) {
		t.Fatal("existing token lost canonical access after alias cleanup")
	}
	filtered := srv.authService.FilterAllowedModels(tokenHash, []string{reconcileTestCanonical})
	if !slices.Equal(filtered, []string{reconcileTestCanonical}) {
		t.Fatalf("token-visible canonical models=%v, want canonical retained", filtered)
	}
}

func TestBatchModelRefreshReconcilesOnlyReplaceMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode string
		want []string
	}{
		{
			name: "replace",
			mode: "replace",
			want: []string{"deepseek-v4.1-flash-thinking", "vendor/Manual-Never-Observed"},
		},
		{
			name: "merge",
			mode: "merge",
			want: []string{"DeepSeek-V4.1-Flash", "deepseek-v4.1-flash-thinking", "vendor/Manual-Never-Observed"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":[{"id":"gpt-new"}]}`))
			}))
			t.Cleanup(upstream.Close)

			srv := newInMemoryServer(t)
			initialChannel := createReconcileTestChannel(
				t,
				srv,
				"batch replace",
				true,
				model.ModelEntry{Model: "DeepSeek-V4.1-Flash"},
			)
			initialURL := upstream.URL
			initialChannel.URLs = model.ChannelURLs{{URL: initialURL, Protocols: []string{"openai"}}}
			if _, err := srv.store.UpdateConfig(context.Background(), initialChannel.ID, initialChannel); err != nil {
				t.Fatal(err)
			}
			if err := srv.store.CreateAPIKeysBatch(context.Background(), []*model.APIKey{{
				ChannelID: initialChannel.ID, KeyIndex: 0, APIKey: "sk-batch", KeyStrategy: model.KeyStrategySequential,
			}}); err != nil {
				t.Fatal(err)
			}
			createReconcileTestChannel(
				t,
				srv,
				"surviving channel",
				true,
				model.ModelEntry{Model: "deepseek-v4.1-flash-thinking"},
			)
			setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{
				Canonical: reconcileTestCanonical,
				Aliases: []string{
					"DeepSeek-V4.1-Flash",
					"deepseek-v4.1-flash-thinking",
					"vendor/Manual-Never-Observed",
				},
				Enabled: true,
			}})

			c, w := newTestContext(t, newJSONRequest(t, http.MethodPost, "/admin/channels/models/refresh-batch", map[string]any{
				"channel_ids": []int64{initialChannel.ID},
				"mode":        tc.mode,
			}))
			srv.HandleBatchRefreshModels(c)
			if w.Code != http.StatusOK {
				t.Fatalf("batch refresh status=%d body=%s", w.Code, w.Body.String())
			}

			groups := persistedModelAliasGroupsForReconcileTest(t, srv)
			if len(groups) != 1 || !slices.Equal(groups[0].Aliases, tc.want) {
				t.Fatalf("%s aliases=%+v, want %v", tc.mode, groups, tc.want)
			}
			if got := visibleModelsForReconcileTest(t, srv); !slices.Contains(got, reconcileTestCanonical) {
				t.Fatalf("%s public models=%v, want canonical retained by surviving channel", tc.mode, got)
			}
		})
	}
}

func TestBatchModelReplaceRollbackPreservesAliasRetryEvidence(t *testing.T) {
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-new"}]}`))
	}))
	t.Cleanup(upstream.Close)
	srv := newInMemoryServer(t)
	ctx := context.Background()
	channel := createReconcileTestChannel(t, srv, "atomic alias cleanup", true, model.ModelEntry{Model: "vendor/vanishing"})
	channel.URLs = model.ChannelURLs{{URL: upstream.URL, Protocols: []string{"openai"}}}
	if _, err := srv.store.UpdateConfig(ctx, channel.ID, channel); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.CreateAPIKeysBatch(ctx, []*model.APIKey{{ChannelID: channel.ID, APIKey: "sk-atomic-fixture", KeyStrategy: model.KeyStrategySequential}}); err != nil {
		t.Fatal(err)
	}
	// An unresolved setting read/parse is a known cleanup failure, so the
	// authoritative topology must retain the same evidence for a later retry.
	if err := srv.store.UpdateSetting(ctx, modelAliasGroupsSettingKey, "not valid JSON"); err != nil {
		t.Fatal(err)
	}
	refresh := func() map[string]any {
		c, w := newTestContext(t, newJSONRequest(t, http.MethodPost, "/admin/channels/models/refresh-batch", map[string]any{
			"channel_ids": []int64{channel.ID}, "mode": "replace",
		}))
		srv.HandleBatchRefreshModels(c)
		var body struct {
			Data map[string]any `json:"data"`
		}
		mustUnmarshalJSON(t, w.Body.Bytes(), &body)
		return body.Data
	}
	failed := refresh()
	current, err := srv.store.GetConfig(ctx, channel.ID)
	if err != nil || current.ModelEntries[0].Model != "vendor/vanishing" || failed["failed"] != float64(1) {
		t.Fatalf("failed replace=%v channel=%+v err=%v, want topology rollback and retryable failure", failed, current, err)
	}
	setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{
		Canonical: reconcileTestCanonical, Aliases: []string{"vendor/vanishing", "vendor/never-observed"}, Enabled: true,
	}})
	completed := refresh()
	groups := persistedModelAliasGroupsForReconcileTest(t, srv)
	current, err = srv.store.GetConfig(ctx, channel.ID)
	if err != nil || current.ModelEntries[0].Model != "gpt-new" || completed["updated"] != float64(1) ||
		len(groups) != 1 || !slices.Equal(groups[0].Aliases, []string{"vendor/never-observed"}) {
		t.Fatalf("retry=%v channel=%+v groups=%+v err=%v", completed, current, groups, err)
	}
}

type modelAliasRaceCASStore struct {
	storage.Store
	onCAS      func(context.Context, string, string, string)
	collisions int
}

func (s *modelAliasRaceCASStore) CompareAndSwapSetting(ctx context.Context, key, expectedValue, newValue string) (bool, error) {
	if s.collisions > 0 {
		s.collisions--
		return false, nil
	}
	if s.onCAS != nil {
		callback := s.onCAS
		s.onCAS = nil
		callback(ctx, key, expectedValue, newValue)
		return false, nil
	}
	cas, ok := s.Store.(conditionalSettingStore)
	if !ok {
		return false, fmt.Errorf("embedded store does not support conditional setting updates")
	}
	return cas.CompareAndSwapSetting(ctx, key, expectedValue, newValue)
}

func TestModelAliasReconcileExhaustionReportsAndRetriesAfterFactsWereReplaced(t *testing.T) {
	for _, check := range []string{"reports exhaustion", "retries without old facts"} {
		t.Run(check, func(t *testing.T) {
			const upstreamKey = "sk-cas-exhaustion"
			adapter := siteSyncPreviewAdapter(
				[]provider.RoutingKeySnapshot{{ID: "primary", Key: upstreamKey, Enabled: true}},
				map[string][]string{upstreamKey: {"vendor/vanishing"}},
			)
			srv, _, account := newSiteSyncPreviewFixture(t, adapter)
			raceStore := &modelAliasRaceCASStore{Store: srv.store}
			srv.store, srv.siteControl.store, srv.configService.store = raceStore, raceStore, raceStore
			setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{
				Canonical: reconcileTestCanonical, Aliases: []string{"vendor/vanishing", "vendor/surviving", "vendor/never-observed"}, Enabled: true,
			}})
			createReconcileTestChannel(t, srv, "CAS survivor", true, model.ModelEntry{Model: "vendor/surviving"})
			first := newSiteTask("model_refresh", account.SiteID, account.ID, 20)
			srv.siteControl.refreshAccount(context.Background(), first, account.ID, true)
			adapter.modelsByKey[upstreamKey] = []string{"gpt-new"}
			raceStore.collisions = 3
			second := newSiteTask("model_refresh", account.SiteID, account.ID, 21)
			srv.siteControl.refreshAccount(context.Background(), second, account.ID, true)
			if check == "reports exhaustion" {
				if second.Status != model.SiteTaskStatusPartial || !strings.Contains(second.Error, "model_alias_reconcile_failed") {
					t.Fatalf("exhaustion status=%s error=%s, want visible retryable cleanup failure", second.Status, second.Error)
				}
				return
			}
			facts, err := srv.store.ListSiteAccountModels(context.Background(), model.SiteModelFilter{SiteAccountID: account.ID})
			if err != nil {
				t.Fatal(err)
			}
			var current, staleEvidence bool
			for _, fact := range facts {
				current = current || fact.Model == "gpt-new" && !fact.Stale
				staleEvidence = staleEvidence || fact.Model == "vendor/vanishing" && fact.Stale
			}
			if !current || !staleEvidence {
				t.Fatalf("facts=%v, want current replacement plus durable stale removal evidence", facts)
			}
			// A fresh configuration service has no in-memory retry state. The
			// persisted positive evidence must survive a process-cache replacement.
			srv.configService = NewConfigService(raceStore)
			srv.siteControl.configService = srv.configService
			third := newSiteTask("model_refresh", account.SiteID, account.ID, 22)
			srv.siteControl.refreshAccount(context.Background(), third, account.ID, true)
			if third.Status != model.SiteTaskStatusSuccess {
				t.Fatalf("retry status=%s error=%s", third.Status, third.Error)
			}
			groups := persistedModelAliasGroupsForReconcileTest(t, srv)
			if len(groups) != 1 || !slices.Equal(groups[0].Aliases, []string{"vendor/surviving", "vendor/never-observed"}) {
				t.Fatalf("retry groups=%+v, want vanished member removed and unobserved/configured names retained", groups)
			}
		})
	}
}

func TestModelAliasRefreshReconcileMergesConcurrentSettingEdit(t *testing.T) {
	const upstreamKey = "sk-concurrent-setting"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "primary", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"DeepSeek-V4.1-Flash"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	raceStore := &modelAliasRaceCASStore{Store: srv.store}
	srv.store = raceStore
	srv.siteControl.store = raceStore
	srv.configService.store = raceStore

	setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{
		Canonical: reconcileTestCanonical,
		Aliases:   []string{"DeepSeek-V4.1-Flash", "deepseek-v4.1-flash-thinking"},
		Enabled:   true,
	}})
	createReconcileTestChannel(t, srv, "concurrent survivor", true, model.ModelEntry{Model: "deepseek-v4.1-flash-thinking"})

	firstTask := newSiteTask("model_refresh", account.SiteID, account.ID, 7)
	srv.siteControl.refreshAccount(context.Background(), firstTask, account.ID, true)
	adapter.modelsByKey[upstreamKey] = []string{"gpt-new"}

	concurrentGroups := []model.ModelAliasGroup{{
		Canonical: reconcileTestCanonical,
		Aliases: []string{
			"DeepSeek-V4.1-Flash",
			"deepseek-v4.1-flash-thinking",
			"vendor/User-Added",
		},
		Enabled: true,
	}}
	concurrentRaw, err := json.Marshal(concurrentGroups)
	if err != nil {
		t.Fatal(err)
	}
	var raceErr error
	raceStore.onCAS = func(ctx context.Context, key, _, _ string) {
		if err := raceStore.UpdateSetting(ctx, key, string(concurrentRaw)); err != nil {
			raceErr = err
			return
		}
		if err := srv.configService.RefreshSetting(ctx, key); err != nil {
			raceErr = err
			return
		}
		srv.modelAliases.reload(srv.configService)
	}

	secondTask := newSiteTask("model_refresh", account.SiteID, account.ID, 8)
	srv.siteControl.refreshAccount(context.Background(), secondTask, account.ID, true)
	if raceErr != nil {
		t.Fatal(raceErr)
	}
	groups := persistedModelAliasGroupsForReconcileTest(t, srv)
	wantAliases := []string{"deepseek-v4.1-flash-thinking", "vendor/User-Added"}
	if len(groups) != 1 || !slices.Equal(groups[0].Aliases, wantAliases) {
		t.Fatalf("concurrent edit was not merged with cleanup: %+v", groups)
	}
	if names := srv.modelAliases.namesFor("deepseek-v4.1-flash-thinking"); !slices.Contains(names, reconcileTestCanonical) {
		t.Fatalf("live registry did not reload retried persisted mapping: %v", names)
	}
}

func TestModelAliasCleanupReadsAuthoritativeHybridSettingAndReportsCacheFailure(t *testing.T) {
	directory := t.TempDir()
	primaryStore, err := storage.CreateSQLiteStore(filepath.Join(directory, "primary.db"))
	if err != nil {
		t.Fatal(err)
	}
	cacheStore, err := storage.CreateSQLiteStore(filepath.Join(directory, "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	primary, ok := primaryStore.(*sqlstore.SQLStore)
	if !ok {
		t.Fatalf("primary store type=%T, want SQLStore", primaryStore)
	}
	cache, ok := cacheStore.(*sqlstore.SQLStore)
	if !ok {
		t.Fatalf("cache store type=%T, want SQLStore", cacheStore)
	}
	hybrid := storage.NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })

	ctx := context.Background()
	initial := []model.ModelAliasGroup{{
		Canonical: reconcileTestCanonical,
		Aliases:   []string{"DeepSeek-V4.1-Flash", "deepseek-v4.1-flash-thinking"},
		Enabled:   true,
	}}
	initialRaw, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := hybrid.UpdateSetting(ctx, modelAliasGroupsSettingKey, string(initialRaw)); err != nil {
		t.Fatal(err)
	}
	concurrent := []model.ModelAliasGroup{{
		Canonical: reconcileTestCanonical,
		Aliases: []string{
			"DeepSeek-V4.1-Flash",
			"deepseek-v4.1-flash-thinking",
			"vendor/User-Added",
		},
		Enabled: true,
	}}
	concurrentRaw, err := json.Marshal(concurrent)
	if err != nil {
		t.Fatal(err)
	}
	if err := primary.UpdateSetting(ctx, modelAliasGroupsSettingKey, string(concurrentRaw)); err != nil {
		t.Fatal(err)
	}
	if _, err := primary.CreateConfig(ctx, &model.Config{
		Name:         "authoritative survivor",
		Enabled:      true,
		URLs:         model.ChannelURLs{{URL: "https://manual.example", Protocols: []string{"openai"}}},
		ModelEntries: []model.ModelEntry{{Model: "deepseek-v4.1-flash-thinking"}},
	}); err != nil {
		t.Fatal(err)
	}

	// Keep the cache at the initial value and force the post-commit mirror to
	// fail. Cleanup must still compare and reload from the primary.
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		store:         hybrid,
		configService: NewConfigService(hybrid),
		modelAliases:  newModelAliasRegistry(nil),
	}
	updated, err := reconcileModelAliasMembers(
		ctx,
		hybrid,
		srv.configService,
		[]string{"DeepSeek-V4.1-Flash"},
		srv.reloadModelAliasesFromPersistedSetting,
	)
	if !updated || !errors.Is(err, errModelAliasReplicaRefresh) {
		t.Fatalf("cleanup updated=%t err=%v, want committed warning", updated, err)
	}

	setting, err := primary.GetSetting(ctx, modelAliasGroupsSettingKey)
	if err != nil {
		t.Fatal(err)
	}
	var groups []model.ModelAliasGroup
	if err := json.Unmarshal([]byte(setting.Value), &groups); err != nil {
		t.Fatal(err)
	}
	wantAliases := []string{"deepseek-v4.1-flash-thinking", "vendor/User-Added"}
	if len(groups) != 1 || !slices.Equal(groups[0].Aliases, wantAliases) {
		t.Fatalf("authoritative aliases=%+v, want %v", groups, wantAliases)
	}
	if names := srv.modelAliases.namesFor("deepseek-v4.1-flash-thinking"); !slices.Contains(names, reconcileTestCanonical) {
		t.Fatalf("live registry did not reload authoritative mapping: %v", names)
	}
}
