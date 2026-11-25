package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func TestModelDiscoveryIncompleteRealProviderPreservesSupplyAndAliases(t *testing.T) {
	for _, adapter := range []provider.SiteAdapter{
		provider.NewNewAPI(provider.ClientFactory{AllowPrivate: true}),
		provider.NewOpenAICompatible(provider.ClientFactory{AllowPrivate: true}),
	} {
		t.Run(adapter.ID(), func(t *testing.T) {
			var body atomic.Value
			body.Store(`{"data":[{"id":"vendor/complete-model"}]}`)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/models" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body.Load().(string)))
			}))
			defer upstream.Close()
			srv, site, account := newSiteSyncPreviewFixture(t, adapter)
			ctx := context.Background()
			site.Platform, site.BaseURL = adapter.ID(), upstream.URL
			if _, err := srv.store.UpdateSite(ctx, site.ID, site); err != nil {
				t.Fatal(err)
			}
			sealed, err := srv.siteControl.cipher.Seal(provider.Credentials{APIKey: "completeness-lifecycle-key"})
			if err != nil {
				t.Fatal(err)
			}
			if err := srv.store.UpdateSiteAccountCredential(ctx, account.ID, model.CredentialTypeAPIKey, sealed, srv.siteControl.cipher.Version()); err != nil {
				t.Fatal(err)
			}
			setModelAliasGroupsForReconcileTest(t, srv, []model.ModelAliasGroup{{Canonical: reconcileTestCanonical,
				Aliases: []string{"vendor/complete-model"}, Enabled: true}})
			refresh := func() *model.SiteTask {
				task := newSiteTask("model_refresh", site.ID, account.ID, 1)
				srv.siteControl.refreshAccount(ctx, task, account.ID, true)
				return task
			}
			if task := refresh(); task.Status != model.SiteTaskStatusSuccess {
				t.Fatalf("initial refresh=%+v", task)
			}
			binding, err := srv.store.GetSiteChannelBinding(ctx, account.ID, "key:account")
			if err != nil {
				t.Fatal(err)
			}
			beforeAliases := persistedModelAliasGroupsForReconcileTest(t, srv)
			for _, response := range []string{
				`{"data":[],"total":1,"has_more":true}`,
				`{"data":[],"next_page":2}`,
				`{"data":[{"id":"vendor/partial-model"}],"total":2}`,
				`{"data":[],"meta":{"pagination":{"total":1}}}`,
			} {
				body.Store(response)
				if task := refresh(); task.Status != model.SiteTaskStatusFailed {
					t.Fatalf("incomplete response %s refresh=%+v, want failed discovery", response, task)
				}
				channel, err := srv.store.GetConfig(ctx, binding.ChannelID)
				if err != nil {
					t.Fatal(err)
				}
				if !channel.Enabled || !slices.Equal(channel.GetModels(), []string{"vendor/complete-model"}) ||
					!slices.Equal(visibleModelsForReconcileTest(t, srv), []string{reconcileTestCanonical}) {
					t.Fatalf("incomplete discovery changed live supply: %+v", channel)
				}
				if got := persistedModelAliasGroupsForReconcileTest(t, srv); !reflect.DeepEqual(got, beforeAliases) {
					t.Fatalf("incomplete discovery changed aliases: got=%+v before=%+v", got, beforeAliases)
				}
			}
			body.Store(`{"data":[],"total":0,"has_more":false,"page":1,"total_pages":0}`)
			if task := refresh(); task.Status != model.SiteTaskStatusSuccess {
				t.Fatalf("complete empty refresh=%+v", task)
			}
			channel, err := srv.store.GetConfig(ctx, binding.ChannelID)
			if err != nil || channel.Enabled || len(channel.ModelEntries) != 0 || len(visibleModelsForReconcileTest(t, srv)) != 0 {
				t.Fatalf("complete empty failed to remove supply: channel=%+v err=%v", channel, err)
			}
		})
	}
}
