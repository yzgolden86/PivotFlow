package provider

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func pricingTestServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pricing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") == "" {
			t.Errorf("pricing request missing Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func pricingRequest(baseURL string) AccountRequest {
	return AccountRequest{
		BaseURL:     baseURL,
		Credentials: Credentials{AccessToken: "management-token", UserID: 7},
	}
}

func newPricingProvider() *NewAPI {
	return &NewAPI{clients: ClientFactory{AllowPrivate: true}}
}

func findModel(pricing SitePricing, name string) (ModelPrice, bool) {
	for _, model := range pricing.Models {
		if model.Model == name {
			return model, true
		}
	}
	return ModelPrice{}, false
}

func almost(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

func TestFetchPricingParsesRatiosAndGroups(t *testing.T) {
	body := `{
	  "success": true,
	  "group_ratio": {"default": 1, "vip": 0.5},
	  "data": [
	    {"model_name":"claude-opus-5","quota_type":0,"model_ratio":15,"completion_ratio":5,
	     "cache_ratio":0.1,"cache_creation_ratio":1.25,"enable_groups":["default","vip"]},
	    {"model_name":"gpt-5.6-sol","quota_type":0,"model_ratio":1.25,"completion_ratio":4,
	     "enable_groups":["default"]}
	  ]
	}`
	server := pricingTestServer(t, body, http.StatusOK)

	pricing, err := newPricingProvider().FetchPricing(context.Background(), pricingRequest(server.URL))
	if err != nil {
		t.Fatalf("FetchPricing: %v", err)
	}
	if len(pricing.Models) != 2 {
		t.Fatalf("len(Models)=%d, want 2", len(pricing.Models))
	}

	opus, ok := findModel(pricing, "claude-opus-5")
	if !ok {
		t.Fatal("claude-opus-5 missing")
	}
	almost(t, opus.ModelRatio, 15, "ModelRatio")
	almost(t, opus.CompletionRatio, 5, "CompletionRatio")
	almost(t, opus.CacheRatio, 0.1, "CacheRatio")
	almost(t, opus.CacheCreationRatio, 1.25, "CacheCreationRatio")

	// 15 ratio × $2 per million × group 1 = $30 per million input.
	input, output, cacheRead, cacheWrite := opus.USDPerMillion(pricing.RatioFor("default"))
	almost(t, input, 30, "input USD/M")
	almost(t, output, 150, "output USD/M")
	almost(t, cacheRead, 3, "cache read USD/M")
	almost(t, cacheWrite, 37.5, "cache write USD/M")

	// The vip group halves everything.
	vipInput, vipOutput, _, _ := opus.USDPerMillion(pricing.RatioFor("vip"))
	almost(t, vipInput, 15, "vip input USD/M")
	almost(t, vipOutput, 75, "vip output USD/M")
}

// Missing cache ratios must default to 1, not to zero: a zero would make cached
// tokens look free and understate the cost.
func TestFetchPricingDefaultsMissingRatiosToOne(t *testing.T) {
	body := `{"success":true,"data":[{"model_name":"m","quota_type":0,"model_ratio":2}]}`
	server := pricingTestServer(t, body, http.StatusOK)

	pricing, err := newPricingProvider().FetchPricing(context.Background(), pricingRequest(server.URL))
	if err != nil {
		t.Fatalf("FetchPricing: %v", err)
	}
	model, ok := findModel(pricing, "m")
	if !ok {
		t.Fatal("model missing")
	}
	almost(t, model.CompletionRatio, 1, "CompletionRatio")
	almost(t, model.CacheRatio, 1, "CacheRatio")
	almost(t, model.CacheCreationRatio, 1, "CacheCreationRatio")
}

// A zero or negative ratio is treated as absent. Trusting a zero would report a
// paid model as free.
func TestFetchPricingRejectsNonPositiveRatios(t *testing.T) {
	body := `{"success":true,"data":[{"model_name":"m","quota_type":0,"model_ratio":0,"completion_ratio":-3}]}`
	server := pricingTestServer(t, body, http.StatusOK)

	pricing, err := newPricingProvider().FetchPricing(context.Background(), pricingRequest(server.URL))
	if err != nil {
		t.Fatalf("FetchPricing: %v", err)
	}
	model, _ := findModel(pricing, "m")
	almost(t, model.ModelRatio, 1, "ModelRatio fallback")
	almost(t, model.CompletionRatio, 1, "CompletionRatio fallback")
}

// Accept the casing variants seen across forks.
func TestFetchPricingAcceptsCacheRatioAliases(t *testing.T) {
	body := `{"success":true,"data":[
	  {"model_name":"camel","quota_type":0,"model_ratio":1,"cacheRatio":0.25,"cacheCreationRatio":2},
	  {"model_name":"alt","quota_type":0,"model_ratio":1,"create_cache_ratio":3}
	]}`
	server := pricingTestServer(t, body, http.StatusOK)

	pricing, err := newPricingProvider().FetchPricing(context.Background(), pricingRequest(server.URL))
	if err != nil {
		t.Fatalf("FetchPricing: %v", err)
	}
	camel, _ := findModel(pricing, "camel")
	almost(t, camel.CacheRatio, 0.25, "cacheRatio alias")
	almost(t, camel.CacheCreationRatio, 2, "cacheCreationRatio alias")
	alt, _ := findModel(pricing, "alt")
	almost(t, alt.CacheCreationRatio, 3, "create_cache_ratio alias")
}

// quota_type 1 bills a flat amount per call; the token ratios must not apply.
func TestFetchPricingHandlesPerCallModels(t *testing.T) {
	body := `{"success":true,"group_ratio":{"default":1,"cheap":0.5},"data":[
	  {"model_name":"scalar","quota_type":1,"model_price":0.05},
	  {"model_name":"object","quota_type":1,"model_price":{"input":0.02,"output":0.09}}
	]}`
	server := pricingTestServer(t, body, http.StatusOK)

	pricing, err := newPricingProvider().FetchPricing(context.Background(), pricingRequest(server.URL))
	if err != nil {
		t.Fatalf("FetchPricing: %v", err)
	}

	scalar, _ := findModel(pricing, "scalar")
	if scalar.QuotaType != 1 {
		t.Errorf("QuotaType=%d, want 1", scalar.QuotaType)
	}
	almost(t, scalar.PerCallUSD(1), 0.05, "scalar per call")
	almost(t, scalar.PerCallUSD(pricing.RatioFor("cheap")), 0.025, "scalar per call, cheap group")
	// Per-token prices must be zero so a caller cannot double-charge.
	input, output, _, _ := scalar.USDPerMillion(1)
	almost(t, input, 0, "per-call input USD/M")
	almost(t, output, 0, "per-call output USD/M")

	object, _ := findModel(pricing, "object")
	almost(t, object.PerCallUSD(1), 0.02, "object per call uses input side")
}

// An unknown group falls back to default rather than to zero.
func TestSitePricingRatioForFallsBackToDefault(t *testing.T) {
	pricing := SitePricing{GroupRatio: map[string]float64{"default": 1.5, "vip": 0.5}}
	almost(t, pricing.RatioFor("vip"), 0.5, "known group")
	almost(t, pricing.RatioFor("unknown"), 1.5, "unknown group falls back to default")
	almost(t, pricing.RatioFor(""), 1.5, "empty group falls back to default")

	empty := SitePricing{}
	almost(t, empty.RatioFor("anything"), 1, "missing table yields neutral 1")
}

// group_ratio always gains a default entry, and non-positive entries are dropped.
func TestNormalizeGroupRatioGuaranteesDefault(t *testing.T) {
	got := normalizeGroupRatio(map[string]float64{"vip": 0.5, "broken": 0, "negative": -1})
	if got[defaultPricingGroup] != 1 {
		t.Errorf("default=%v, want 1", got[defaultPricingGroup])
	}
	if _, ok := got["broken"]; ok {
		t.Error("zero ratio must be dropped")
	}
	if _, ok := got["negative"]; ok {
		t.Error("negative ratio must be dropped")
	}
	if got["vip"] != 0.5 {
		t.Errorf("vip=%v, want 0.5", got["vip"])
	}
}

// Models with no enable_groups are treated as default-group only.
func TestFetchPricingDefaultsEmptyGroups(t *testing.T) {
	body := `{"success":true,"data":[{"model_name":"m","quota_type":0,"model_ratio":1,"enable_groups":[]}]}`
	server := pricingTestServer(t, body, http.StatusOK)

	pricing, err := newPricingProvider().FetchPricing(context.Background(), pricingRequest(server.URL))
	if err != nil {
		t.Fatalf("FetchPricing: %v", err)
	}
	model, _ := findModel(pricing, "m")
	if len(model.Groups) != 1 || model.Groups[0] != defaultPricingGroup {
		t.Errorf("Groups=%v, want [default]", model.Groups)
	}
}

// A model-call API key cannot read this endpoint; say so rather than sending a
// request that will fail confusingly.
func TestFetchPricingRequiresManagementSession(t *testing.T) {
	provider := newPricingProvider()
	_, err := provider.FetchPricing(context.Background(), AccountRequest{
		BaseURL:     "https://example.com",
		Credentials: Credentials{APIKey: "sk-only"},
	})
	if ErrorCode(err) != CodeUnsupported {
		t.Fatalf("code=%q, want %q", ErrorCode(err), CodeUnsupported)
	}
}

// A site without the endpoint, or with an empty table, is reported as
// unsupported so the caller falls back to local estimation.
func TestFetchPricingReportsUnsupportedOnEmptyTable(t *testing.T) {
	for _, body := range []string{
		`{"success":true,"data":[]}`,
		`{"success":false,"message":"no"}`,
		`{"success":true,"data":[{"model_name":"  "}]}`,
	} {
		server := pricingTestServer(t, body, http.StatusOK)
		_, err := newPricingProvider().FetchPricing(context.Background(), pricingRequest(server.URL))
		if ErrorCode(err) != CodeUnsupported {
			t.Errorf("body %s: code=%q, want %q", body, ErrorCode(err), CodeUnsupported)
		}
	}
}

// Guards the ratio→USD constant against silent edits: New API's quota_per_unit
// is 500000 and one ratio unit is $0.002/1K, so a ratio of 1 is $2 per million.
func TestRatioToUSDConstant(t *testing.T) {
	price := ModelPrice{Model: "x", ModelRatio: 1, CompletionRatio: 1, CacheRatio: 1, CacheCreationRatio: 1}
	input, _, _, _ := price.USDPerMillion(1)
	almost(t, input, 2, "ratio 1 in USD per million")

	// Cross-check against the quota arithmetic the upstream actually performs:
	// 1,000,000 tokens × ratio 1 = 1,000,000 quota units; ÷ 500000 = $2.
	const quotaPerUnit = 500000.0
	almost(t, 1_000_000*1.0/quotaPerUnit, input, "quota-derived USD per million")
}

func TestParsePerCallPrice(t *testing.T) {
	cases := []struct {
		raw  string
		want float64
	}{
		{`0.07`, 0.07},
		{`{"input":0.03,"output":0.11}`, 0.03},
		{`"nonsense"`, 0},
		{`null`, 0},
		{``, 0},
	}
	for _, tc := range cases {
		got := parsePerCallPrice(json.RawMessage(tc.raw))
		almost(t, got, tc.want, "parsePerCallPrice("+tc.raw+")")
	}
}
