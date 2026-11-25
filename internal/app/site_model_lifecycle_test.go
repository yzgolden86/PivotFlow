package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func TestModelRefreshRealProviderEmptyAndInvalidModelDiscovery(t *testing.T) {
	for _, adapter := range []provider.SiteAdapter{
		provider.NewNewAPI(provider.ClientFactory{AllowPrivate: true}),
		provider.NewOpenAICompatible(provider.ClientFactory{AllowPrivate: true}),
	} {
		for _, tc := range []struct {
			name, body string
			status     int
			empty      bool
		}{
			{"empty", `{"data":[]}`, 200, true},
			{"missing", `{}`, 200, false},
			{"null", `{"data":null}`, 200, false},
			{"wrong type", `{"data":{}}`, 200, false},
			{"blank names", `{"data":[{"id":" "}]}`, 200, false},
			{"invalid", `{"data":`, 200, false},
			{"error payload", `{"success":false,"data":[]}`, 200, false},
			{"rejected", `{"error":{"message":"invalid key"}}`, 401, false},
			{"unsupported", `{}`, 404, false},
		} {
			t.Run(adapter.ID()+"/"+tc.name, func(t *testing.T) {
				body, status := `{"data":[{"id":"vendor/real-provider"}]}`, 200
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/v1/models" {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = w.Write([]byte(body))
				}))
				defer upstream.Close()
				srv, site, account := newSiteSyncPreviewFixture(t, adapter)
				ctx := context.Background()
				site.Platform, site.BaseURL = adapter.ID(), upstream.URL
				if _, err := srv.store.UpdateSite(ctx, site.ID, site); err != nil {
					t.Fatal(err)
				}
				sealed, err := srv.siteControl.cipher.Seal(provider.Credentials{APIKey: "sk-real-lifecycle"})
				if err != nil {
					t.Fatal(err)
				}
				if err := srv.store.UpdateSiteAccountCredential(ctx, account.ID, model.CredentialTypeAPIKey, sealed, srv.siteControl.cipher.Version()); err != nil {
					t.Fatal(err)
				}
				setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{Canonical: reconcileTestCanonical,
					Aliases: []string{"vendor/real-provider"}, Enabled: true}})
				refresh := func() *model.SiteTask {
					task := newSiteTask("model_refresh", account.SiteID, account.ID, 42)
					srv.siteControl.refreshAccount(ctx, task, account.ID, true)
					return task
				}
				if initial := refresh(); initial.Status != model.SiteTaskStatusSuccess {
					t.Fatalf("initial refresh=%+v", initial)
				}
				binding, err := srv.store.GetSiteChannelBinding(ctx, account.ID, "key:account")
				if err != nil {
					t.Fatal(err)
				}
				body, status = tc.body, tc.status
				final := refresh()
				if tc.empty && final.Status != model.SiteTaskStatusSuccess || !tc.empty && final.Status != model.SiteTaskStatusFailed {
					t.Fatalf("final refresh=%+v, authoritative empty=%t", final, tc.empty)
				}
				channel, err := srv.store.GetConfig(ctx, binding.ChannelID)
				if err != nil {
					t.Fatal(err)
				}
				if tc.empty {
					if channel.Enabled || len(channel.ModelEntries) != 0 || len(visibleModelsForReconcileTest(t, srv)) != 0 {
						t.Fatalf("authoritative empty retained supply=%+v", channel)
					}
					facts, err := srv.store.ListSiteAccountModels(ctx, model.SiteModelFilter{SiteAccountID: account.ID})
					if err != nil || len(facts) != 0 {
						t.Fatalf("empty snapshot=%v err=%v", facts, err)
					}
				} else if !channel.Enabled || !slices.Equal(channel.GetModels(), []string{"vendor/real-provider"}) ||
					!slices.Equal(visibleModelsForReconcileTest(t, srv), []string{reconcileTestCanonical}) {
					t.Fatalf("invalid discovery removed current supply=%+v", channel)
				}
			})
		}
	}
}

func TestModelRefreshRealProviderKeyDiscoveryRequiresCompleteValidList(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		empty      bool
	}{
		{"empty array", `{"success":true,"data":[]}`, true},
		{"empty items", `{"success":true,"data":{"items":[],"total":0}}`, true},
		{"disabled key", `{"success":true,"data":[{"id":7,"key":"synthetic-managed-route","status":2}]}`, true},
		{"missing", `{"success":true}`, false},
		{"null", `{"success":true,"data":null}`, false},
		{"wrong collection", `{"success":true,"data":{}}`, false},
		{"missing key", `{"success":true,"data":[{"id":7}]}`, false},
		{"mixed invalid", `{"success":true,"data":[{"id":7,"key":"synthetic-managed-route","status":1},{}]}`, false},
		{"incomplete empty", `{"success":true,"data":{"items":[],"total":1}}`, false},
		{"incomplete keys", `{"success":true,"data":{"items":[{"id":8,"key":"another-route","status":1}],"total":2}}`, false},
		{"more pages", `{"success":true,"data":{"items":[],"has_more":true}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var keyResponse atomic.Value
			keyResponse.Store(`{"success":true,"data":[{"id":7,"name":"managed","key":"synthetic-managed-route","status":1}]}`)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/token/":
					_, _ = w.Write([]byte(keyResponse.Load().(string)))
				case "/v1/models":
					_, _ = w.Write([]byte(`{"data":[{"id":"vendor/managed-model"}]}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()
			adapter := provider.NewNewAPI(provider.ClientFactory{AllowPrivate: true})
			srv, site, account := newSiteSyncPreviewFixture(t, adapter)
			ctx := context.Background()
			site.BaseURL = upstream.URL
			if _, err := srv.store.UpdateSite(ctx, site.ID, site); err != nil {
				t.Fatal(err)
			}
			sealed, err := srv.siteControl.cipher.Seal(provider.Credentials{AccessToken: "synthetic-management-session", UserID: 7})
			if err != nil {
				t.Fatal(err)
			}
			if err := srv.store.UpdateSiteAccountCredential(ctx, account.ID, model.CredentialTypeAccessToken, sealed, srv.siteControl.cipher.Version()); err != nil {
				t.Fatal(err)
			}
			setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{Canonical: reconcileTestCanonical,
				Aliases: []string{"vendor/managed-model"}, Enabled: true}})
			refresh := func() *model.SiteTask {
				task := newSiteTask("model_refresh", site.ID, account.ID, 1)
				srv.siteControl.refreshAccount(ctx, task, account.ID, true)
				return task
			}
			if task := refresh(); task.Status != model.SiteTaskStatusSuccess {
				t.Fatalf("initial management sync=%+v", task)
			}
			binding, err := srv.store.GetSiteChannelBinding(ctx, account.ID, "key:7")
			if err != nil {
				t.Fatal(err)
			}
			keyResponse.Store(tc.body)
			final := refresh()
			channel, err := srv.store.GetConfig(ctx, binding.ChannelID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.empty {
				if final.Status != model.SiteTaskStatusSuccess || channel.Enabled || len(visibleModelsForReconcileTest(t, srv)) != 0 {
					t.Fatalf("confirmed removal retained supply: task=%+v channel=%+v", final, channel)
				}
			} else {
				if final.Status != model.SiteTaskStatusPartial || !channel.Enabled || !slices.Equal(channel.GetModels(), []string{"vendor/managed-model"}) ||
					!slices.Equal(visibleModelsForReconcileTest(t, srv), []string{reconcileTestCanonical}) {
					t.Fatalf("invalid/incomplete key discovery changed current supply: task=%+v channel=%+v", final, channel)
				}
				facts, err := srv.store.ListSiteAccountModels(ctx, model.SiteModelFilter{SiteAccountID: account.ID})
				if err != nil || len(facts) != 1 || facts[0].Model != "vendor/managed-model" || facts[0].Stale {
					t.Fatalf("invalid key discovery changed model evidence: facts=%+v err=%v", facts, err)
				}
			}
		})
	}
}

func TestModelRefreshAuthoritativeEmptyProjectionPreservesHistory(t *testing.T) {
	for _, mode := range []string{"removed key", "disabled key", "empty models", "empty group"} {
		t.Run(mode, func(t *testing.T) {
			const emptyKey = "sk-empty-lifecycle"
			const survivorKey = "sk-survivor-lifecycle"
			base := siteSyncPreviewAdapter([]provider.RoutingKeySnapshot{
				{ID: "empty", Group: "empty", Key: emptyKey, Enabled: true},
				{ID: "survivor", Group: "survivor", Key: survivorKey, Enabled: true},
			}, map[string][]string{emptyKey: {"vendor/vanishing"}, survivorKey: {"vendor/survivor"}})
			var adapter provider.SiteAdapter = base
			var grouped *groupFallbackProjectionTestAdapter
			if mode == "empty group" {
				grouped = &groupFallbackProjectionTestAdapter{multiKeyProjectionTestAdapter: *base,
					modelsByGroup: map[string][]string{"empty": {"vendor/vanishing"}, "survivor": {"vendor/survivor"}}}
				base = &grouped.multiKeyProjectionTestAdapter
				adapter = grouped
			}
			srv, _, account := newSiteSyncPreviewFixture(t, adapter)
			ctx := context.Background()
			setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{Canonical: reconcileTestCanonical,
				Aliases: []string{"vendor/vanishing", "vendor/survivor"}, Enabled: true}})
			initial := newSiteTask("model_refresh", account.SiteID, account.ID, 40)
			srv.siteControl.refreshAccount(ctx, initial, account.ID, true)
			if initial.Status != model.SiteTaskStatusSuccess {
				t.Fatalf("initial refresh=%+v", initial)
			}
			binding, err := srv.store.GetSiteChannelBinding(ctx, account.ID, "key:empty")
			if err != nil {
				t.Fatal(err)
			}
			channelID := binding.ChannelID
			now := time.Now()
			const requestID = "lifecycle-history"
			if err := srv.store.AddLog(ctx, &model.LogEntry{Time: model.JSONTime{Time: now}, Model: "vendor/vanishing",
				ChannelID: channelID, StatusCode: 200, Cost: 0.125, CostMultiplier: 1, InputTokens: 11,
				RequestID: requestID, ClientProtocol: "openai"}); err != nil {
				t.Fatal(err)
			}
			if err := srv.store.BatchAddRequestTraces(ctx, []*model.RequestTrace{{RequestID: requestID,
				StartedAt: now.UnixMilli(), Model: "vendor/vanishing", ActualModel: "vendor/vanishing",
				ChannelID: channelID, ChannelName: "historical channel", ClientProtocol: "openai", Transport: "http",
				FinalStatus: 200, Outcome: "success", UpstreamAttemptCount: 1, UpstreamSuccessCount: 1}}); err != nil {
				t.Fatal(err)
			}
			manualID, _ := createManualSiteProjectionForTest(t, srv, account)
			// Warm routing before sync to verify the invalidation removes old supply.
			if channels, err := srv.GetEnabledChannelsByModel(ctx, "vendor/vanishing"); err != nil || len(channels) != 1 {
				t.Fatalf("initial supply=%+v err=%v", channels, err)
			}
			switch mode {
			case "removed key":
				base.keys = base.keys[1:]
			case "disabled key":
				base.keys[0].Enabled = false
			case "empty models":
				base.modelsByKey[emptyKey] = []string{}
				base.keys[0].Models = []string{"vendor/vanishing"}
				base.modelsByKey[""] = []string{"vendor/vanishing"} // global management must not resurrect it
			case "empty group":
				shared := []string{"vendor/vanishing", "vendor/survivor"}
				base.modelsByKey[emptyKey], base.modelsByKey[survivorKey] = shared, shared
				base.keys[0].Models = []string{"vendor/vanishing"}
				grouped.modelsByGroup["empty"] = []string{}
			}
			final := newSiteTask("model_refresh", account.SiteID, account.ID, 41)
			srv.siteControl.refreshAccount(ctx, final, account.ID, true)
			if final.Status != model.SiteTaskStatusSuccess {
				t.Fatalf("final refresh=%+v", final)
			}
			logs, err := srv.store.ListLogs(ctx, now.Add(-time.Minute), 10, 0, &model.LogFilter{ChannelID: &channelID})
			if err != nil || len(logs) != 1 || logs[0].Cost != 0.125 || logs[0].InputTokens != 11 || logs[0].RequestID != requestID {
				t.Fatalf("history logs=%+v err=%v, want original tokens/cost/request", logs, err)
			}
			costs, err := srv.store.GetTodayChannelCosts(ctx, now.Add(-time.Minute))
			if err != nil || costs[channelID] != 0.125 {
				t.Fatalf("history costs=%v err=%v", costs, err)
			}
			trace, err := srv.store.GetRequestTrace(ctx, requestID, nil)
			if err != nil || trace.ChannelID != channelID || trace.Model != "vendor/vanishing" || trace.ChannelName != "historical channel" {
				t.Fatalf("history trace=%+v err=%v", trace, err)
			}
			channel, err := srv.store.GetConfig(ctx, channelID)
			if err != nil || channel.Enabled {
				t.Fatalf("old projected channel=%+v err=%v, want retained disabled", channel, err)
			}
			binding, err = srv.store.GetSiteChannelBinding(ctx, account.ID, "key:empty")
			if err != nil || binding.Status != "inactive" || binding.Ownership != "projected" {
				t.Fatalf("old projected binding=%+v err=%v", binding, err)
			}
			groups := persistedModelAliasGroupsForReconcileTest(t, srv)
			if len(groups) != 1 || !slices.Equal(groups[0].Aliases, []string{"vendor/survivor"}) {
				t.Fatalf("confirmed disappearance retained alias variants=%+v", groups)
			}
			if len(channel.ModelEntries) != 0 {
				t.Fatalf("inactive projection retained routing models=%v", channel.ModelEntries)
			}
			manual, err := srv.store.GetConfig(ctx, manualID)
			if err != nil || !manual.Enabled || !slices.Equal(manual.GetModels(), []string{"gpt-old"}) {
				t.Fatalf("manual channel=%+v err=%v", manual, err)
			}
			if channels, err := srv.GetEnabledChannelsByModel(ctx, "vendor/vanishing"); err != nil || len(channels) != 0 {
				t.Fatalf("stale supply=%+v err=%v", channels, err)
			}
			if got := visibleModelsForReconcileTest(t, srv); !slices.Equal(got, []string{reconcileTestCanonical, "gpt-old"}) {
				t.Fatalf("public models=%v, want surviving canonical + manual model", got)
			}
			inventory, err := srv.modelRouteInventory(ctx, "", 50, 0, true)
			if err != nil || inventory.Total != 2 {
				t.Fatalf("enabled inventory=%+v err=%v", inventory, err)
			}
			for _, item := range inventory.Items {
				if item.Model == reconcileTestCanonical && (item.AliasCount != 1 || !slices.Equal(item.Aliases, []string{"vendor/survivor"})) {
					t.Fatalf("enabled inventory retained vanished variant=%+v", item)
				}
			}
		})
	}
}
