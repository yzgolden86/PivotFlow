package app

import (
	"math"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func nearly(t *testing.T, got, want float64, label string) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

func tokenPricing(group string, models ...provider.ModelPrice) channelPricing {
	return channelPricing{
		pricing: provider.SitePricing{
			Models:     models,
			GroupRatio: map[string]float64{"default": 1, "vip": 0.5, "svip": 0.25},
		},
		group: group,
	}
}

// The headline case: cost must match the quota arithmetic the upstream performs.
// New API charges tokens × ratio quota units, and quota_per_unit is 500000.
func TestComputeSiteCostMatchesUpstreamQuotaArithmetic(t *testing.T) {
	// model_ratio 15, completion_ratio 5 — Anthropic Opus-like terms.
	cp := tokenPricing("default", provider.ModelPrice{
		Model: "claude-opus-5", QuotaType: 0,
		ModelRatio: 15, CompletionRatio: 5, CacheRatio: 0.1, CacheCreationRatio: 1.25,
	})
	res := &fwResult{InputTokens: 1_000_000, OutputTokens: 200_000}

	got, ok := computeSiteCost(cp, "claude-opus-5", res)
	if !ok {
		t.Fatal("expected the site price to apply")
	}

	// Cross-check independently of our own constant: quota = tokens × ratio,
	// output additionally × completion_ratio; USD = quota / 500000.
	const quotaPerUnit = 500000.0
	wantQuota := 1_000_000*15.0 + 200_000*15.0*5.0
	nearly(t, got, wantQuota/quotaPerUnit, "cost")
	// Sanity: 15 ratio is $30/M input, so 1M input alone is $30.
	nearly(t, got, 30+200_000*150.0/1_000_000, "cost via per-million prices")
}

// Cache reads are billed at cache_ratio and must not also be charged as input.
func TestComputeSiteCostChargesCacheAtItsOwnRatio(t *testing.T) {
	cp := tokenPricing("default", provider.ModelPrice{
		Model: "m", QuotaType: 0,
		ModelRatio: 10, CompletionRatio: 1, CacheRatio: 0.1, CacheCreationRatio: 1.25,
	})
	res := &fwResult{
		InputTokens:          100_000,
		OutputTokens:         50_000,
		CacheReadInputTokens: 400_000,
		Cache5mInputTokens:   80_000,
		Cache1hInputTokens:   20_000,
	}

	got, ok := computeSiteCost(cp, "m", res)
	if !ok {
		t.Fatal("expected the site price to apply")
	}

	// $20/M base (ratio 10). Cache read $2/M, cache write $25/M.
	want := 100_000*20.0/1e6 + 50_000*20.0/1e6 + 400_000*2.0/1e6 + 100_000*25.0/1e6
	nearly(t, got, want, "cost with cache")
}

// Some upstreams report only the combined cache-creation total; missing it would
// undercount cache writes to zero.
func TestComputeSiteCostFallsBackToCombinedCacheWrite(t *testing.T) {
	cp := tokenPricing("default", provider.ModelPrice{
		Model: "m", QuotaType: 0, ModelRatio: 1, CompletionRatio: 1, CacheRatio: 1, CacheCreationRatio: 2,
	})
	split := &fwResult{Cache5mInputTokens: 60_000, Cache1hInputTokens: 40_000}
	combined := &fwResult{CacheCreationInputTokens: 100_000}

	splitCost, _ := computeSiteCost(cp, "m", split)
	combinedCost, _ := computeSiteCost(cp, "m", combined)
	nearly(t, combinedCost, splitCost, "combined cache-write total")
	nearly(t, combinedCost, 100_000*4.0/1e6, "cache write at ratio 2 => $4/M")
}

// The group multiplier scales everything; this is the 倍率 the user was hitting.
func TestComputeSiteCostAppliesGroupRatio(t *testing.T) {
	price := provider.ModelPrice{
		Model: "m", QuotaType: 0, ModelRatio: 10, CompletionRatio: 2, CacheRatio: 1, CacheCreationRatio: 1,
	}
	res := &fwResult{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	base, _ := computeSiteCost(tokenPricing("default", price), "m", res)
	vip, _ := computeSiteCost(tokenPricing("vip", price), "m", res)
	svip, _ := computeSiteCost(tokenPricing("svip", price), "m", res)

	nearly(t, base, 20+40, "default group")
	nearly(t, vip, base*0.5, "vip halves the cost")
	nearly(t, svip, base*0.25, "svip quarters the cost")
}

// An unknown group must fall back to default, never to zero.
func TestComputeSiteCostUnknownGroupFallsBackToDefault(t *testing.T) {
	price := provider.ModelPrice{Model: "m", QuotaType: 0, ModelRatio: 5, CompletionRatio: 1, CacheRatio: 1, CacheCreationRatio: 1}
	res := &fwResult{InputTokens: 1_000_000}

	unknown, ok := computeSiteCost(tokenPricing("no-such-group", price), "m", res)
	if !ok {
		t.Fatal("expected pricing to apply")
	}
	nearly(t, unknown, 10, "unknown group priced at default ratio")
	if unknown == 0 {
		t.Error("an unknown group must not zero the cost")
	}
}

// Per-call models charge a flat amount and must ignore token counts entirely.
func TestComputeSiteCostPerCallIgnoresTokens(t *testing.T) {
	cp := tokenPricing("default", provider.ModelPrice{
		Model: "image-model", QuotaType: 1, PerCallPrice: 0.04,
		ModelRatio: 999, CompletionRatio: 999,
	})
	small := &fwResult{InputTokens: 10}
	huge := &fwResult{InputTokens: 5_000_000, OutputTokens: 5_000_000}

	smallCost, ok := computeSiteCost(cp, "image-model", small)
	if !ok {
		t.Fatal("expected per-call price to apply")
	}
	hugeCost, _ := computeSiteCost(cp, "image-model", huge)
	nearly(t, smallCost, 0.04, "per-call cost")
	nearly(t, hugeCost, 0.04, "per-call cost is token-independent")
}

func TestComputeSiteCostPerCallAppliesGroupRatio(t *testing.T) {
	price := provider.ModelPrice{Model: "m", QuotaType: 1, PerCallPrice: 0.08}
	cost, _ := computeSiteCost(tokenPricing("vip", price), "m", &fwResult{})
	nearly(t, cost, 0.04, "per-call with vip ratio")
}

// A model the site does not price must report false so the caller falls back to
// local estimation rather than recording zero.
func TestComputeSiteCostUnknownModelReportsFalse(t *testing.T) {
	cp := tokenPricing("default", provider.ModelPrice{Model: "known", QuotaType: 0, ModelRatio: 1, CompletionRatio: 1, CacheRatio: 1, CacheCreationRatio: 1})
	if _, ok := computeSiteCost(cp, "unknown-model", &fwResult{InputTokens: 100}); ok {
		t.Error("an unpriced model must not be priced from the site table")
	}
	if _, ok := computeSiteCost(cp, "", &fwResult{InputTokens: 100}); ok {
		t.Error("an empty model name must not resolve")
	}
	if _, ok := computeSiteCost(cp, "known", nil); ok {
		t.Error("a nil result must not be priced")
	}
}

// Model lookup tolerates case differences, matching the routing layer.
func TestSitePriceForIsCaseInsensitiveFallback(t *testing.T) {
	pricing := provider.SitePricing{Models: []provider.ModelPrice{
		{Model: "GLM-5.2", ModelRatio: 3},
		{Model: "gpt-5.6-sol", ModelRatio: 1},
	}}

	exact, ok := sitePriceFor(pricing, "gpt-5.6-sol")
	if !ok || exact.Model != "gpt-5.6-sol" {
		t.Error("exact match failed")
	}
	folded, ok := sitePriceFor(pricing, "glm-5.2")
	if !ok || folded.Model != "GLM-5.2" {
		t.Error("case-insensitive fallback failed")
	}
	// Exact match must win over a fold when both exist.
	pricing.Models = append(pricing.Models, provider.ModelPrice{Model: "glm-5.2", ModelRatio: 99})
	preferred, _ := sitePriceFor(pricing, "glm-5.2")
	nearly(t, preferred.ModelRatio, 99, "exact match preferred over fold")
}

// Negative token counts (corrupt upstream usage) must not produce a credit.
func TestComputeSiteCostRejectsNegativeTotals(t *testing.T) {
	cp := tokenPricing("default", provider.ModelPrice{
		Model: "m", QuotaType: 0, ModelRatio: 1, CompletionRatio: 1, CacheRatio: 1, CacheCreationRatio: 1,
	})
	res := &fwResult{InputTokens: -1_000_000, OutputTokens: 0}
	cost, ok := computeSiteCost(cp, "m", res)
	if ok && cost < 0 {
		t.Errorf("cost=%v, must never be negative", cost)
	}
}

// The cache must not serve a stale table, and a failed fetch must be throttled
// rather than retried on every request.
func TestSitePricingCacheTTLAndFailureThrottle(t *testing.T) {
	cache := newSitePricingCache()
	now := time.Now()
	table := provider.SitePricing{
		Models:     []provider.ModelPrice{{Model: "m", ModelRatio: 1}},
		GroupRatio: map[string]float64{"default": 1},
	}

	cache.store(7, table, false, now)
	if got, ok := cache.lookup(7, now); !ok || len(got.Models) != 1 {
		t.Fatal("fresh entry should be served")
	}
	if _, ok := cache.lookup(7, now.Add(sitePricingTTL+time.Minute)); ok {
		t.Error("expired entry must not be served")
	}

	// A negative result resolves (so no immediate refetch) but yields no models.
	cache.store(8, provider.SitePricing{}, true, now)
	got, ok := cache.lookup(8, now)
	if !ok {
		t.Error("a fresh negative result should resolve to avoid refetching")
	}
	if len(got.Models) != 0 {
		t.Error("a negative result must carry no models")
	}
	if _, ok := cache.lookup(8, now.Add(sitePricingFailureTTL+time.Minute)); ok {
		t.Error("negative result must expire so the site is retried eventually")
	}

	// Defensive store: concurrent cache miss can race, must not let failure
	// overwrite a fresh success.
	successTable := provider.SitePricing{
		Models:     []provider.ModelPrice{{Model: "opus", ModelRatio: 1}},
		GroupRatio: map[string]float64{"default": 1},
	}
	cache.store(9, successTable, false, now)
	cache.store(9, provider.SitePricing{}, true, now) // racing failure
	recovered, ok := cache.lookup(9, now)
	if !ok {
		t.Error("failed store must not evict an unexpired success")
	}
	if len(recovered.Models) != 1 {
		t.Errorf("failed store overwrote success: got %d models, want 1", len(recovered.Models))
	}

	// Once the success expires, failure can store.
	expiredNow := now.Add(sitePricingTTL + time.Minute)
	cache.store(9, provider.SitePricing{}, true, expiredNow)
	if _, ok := cache.lookup(9, expiredNow); !ok {
		t.Error("failed store must succeed after success expires")
	}
}

func TestSitePricingCacheInvalidate(t *testing.T) {
	cache := newSitePricingCache()
	now := time.Now()
	cache.store(1, provider.SitePricing{Models: []provider.ModelPrice{{Model: "m"}}}, false, now)
	cache.storeChannelBindings(map[int64]channelBinding{5: {siteID: 1, group: "vip"}}, now)

	cache.invalidate()

	if _, ok := cache.lookup(1, now); ok {
		t.Error("invalidate must drop pricing entries")
	}
	if _, ok := cache.channelBindingsFresh(now); ok {
		t.Error("invalidate must drop channel bindings")
	}
}

// A nil cache must be safe: pricing is optional and every method is called on
// the hot path.
func TestSitePricingCacheNilSafe(t *testing.T) {
	var cache *sitePricingCache
	cache.invalidate()
	cache.store(1, provider.SitePricing{}, false, time.Now())
	cache.storeChannelBindings(nil, time.Now())
	if _, ok := cache.lookup(1, time.Now()); ok {
		t.Error("nil cache must not resolve")
	}
	if _, ok := cache.channelBindingsFresh(time.Now()); ok {
		t.Error("nil cache must not report fresh bindings")
	}
}

func TestCostSourceLabel(t *testing.T) {
	if costSourceLabel(true) != costSourceSite {
		t.Errorf("costSourceLabel(true)=%q, want %q", costSourceLabel(true), costSourceSite)
	}
	if costSourceLabel(false) != costSourceLocal {
		t.Errorf("costSourceLabel(false)=%q, want %q", costSourceLabel(false), costSourceLocal)
	}
}

// siteSourcedCost must degrade quietly when pricing is unavailable, since it is
// called for every successful request.
func TestSiteSourcedCostDegradesWithoutPricing(t *testing.T) {
	if _, ok := (*Server)(nil).siteSourcedCost("m", 1, &fwResult{}); ok {
		t.Error("nil server must not price")
	}
	server := &Server{}
	if _, ok := server.siteSourcedCost("m", 1, &fwResult{}); ok {
		t.Error("server without a pricing cache must not price")
	}
	server.sitePricing = newSitePricingCache()
	if _, ok := server.siteSourcedCost("m", 0, &fwResult{}); ok {
		t.Error("a zero channel id must not price")
	}
	if _, ok := server.siteSourcedCost("m", 1, nil); ok {
		t.Error("a nil result must not price")
	}
}
