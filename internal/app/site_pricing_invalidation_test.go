package app

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func newPricingSite(t *testing.T, srv *Server) *model.Site {
	t.Helper()
	site, err := srv.store.CreateSite(context.Background(), &model.Site{
		Name: "Pricing", BaseURL: "https://pricing.example", Platform: model.SitePlatformNewAPIFamily,
		Enabled: true, Timezone: "UTC", TagsJSON: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	return site
}

func seedSitePrices(srv *Server, siteID int64) {
	pricing := provider.SitePricing{Models: []provider.ModelPrice{{Model: "gpt-5", ModelRatio: 1}}}
	srv.sitePricing.store(siteID, pricing, false, time.Now())
	srv.sitePricing.store(999, pricing, false, time.Now())
}

func TestUnchangedRouteProjectionPreservesAllSitePrices(t *testing.T) {
	srv := newInMemoryServer(t)
	site := newPricingSite(t, srv)
	ctx := context.Background()
	account, err := srv.store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "main", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	project := func() {
		t.Helper()
		_, err := srv.siteControl.projectAccountWithModelsForProtocols(ctx, account, site,
			provider.Credentials{APIKey: "test-routing-key"}, "default", "Pricing / main",
			[]string{"openai"}, []string{"gpt-5"}, true, "default")
		if err != nil {
			t.Fatal(err)
		}
	}
	project()
	seedSitePrices(srv, site.ID)
	project()
	for _, id := range []int64{site.ID, 999} {
		if _, ok := srv.sitePricing.lookup(id, time.Now()); !ok {
			t.Errorf("unchanged route sync evicted site %d prices", id)
		}
	}
}

func TestSiteEditsInvalidateOnlyChangedPricingSources(t *testing.T) {
	for _, test := range []struct {
		name       string
		patch      map[string]any
		invalidate bool
	}{
		{"name", map[string]any{"name": "Renamed"}, false},
		{"same_url", map[string]any{"base_url": "https://pricing.example/"}, false},
		{"url", map[string]any{"base_url": "https://changed.example"}, true},
		{"platform", map[string]any{"platform": model.SitePlatformOpenAICompatible}, true},
		{"proxy", map[string]any{"use_system_proxy": true}, true},
		{"enabled", map[string]any{"enabled": false}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := newInMemoryServer(t)
			site := newPricingSite(t, srv)
			seedSitePrices(srv, site.ID)
			c, response := newTestContext(t, newJSONRequest(t, http.MethodPatch, "/admin/sites/"+strconv.FormatInt(site.ID, 10), test.patch))
			c.Params = gin.Params{{Key: "id", Value: strconv.FormatInt(site.ID, 10)}}
			srv.siteControl.handleSiteByID(c)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if _, ok := srv.sitePricing.lookup(site.ID, time.Now()); ok == test.invalidate {
				t.Errorf("own price cache present=%v, want %v", ok, !test.invalidate)
			}
			if _, ok := srv.sitePricing.lookup(999, time.Now()); !ok {
				t.Error("unrelated site's prices were evicted")
			}
		})
	}
}

func TestPricingSourceChangeRearmsOnlyAffectedSite(t *testing.T) {
	srv := newInMemoryServer(t)
	for _, id := range []int64{1, 2} {
		srv.sitePricing.store(id, provider.SitePricing{}, true, time.Now())
		srv.sitePricing.shouldLogUnsupported(id)
	}
	srv.siteControl.pricingSourceChanged(1)
	if _, ok := srv.sitePricing.lookup(1, time.Now()); ok {
		t.Error("changed source retained negative cache")
	}
	if !srv.sitePricing.shouldLogUnsupported(1) {
		t.Error("changed source did not rearm diagnostic")
	}
	if _, ok := srv.sitePricing.lookup(2, time.Now()); !ok || srv.sitePricing.shouldLogUnsupported(2) {
		t.Error("unrelated source lost its negative cache or log throttle")
	}
}
