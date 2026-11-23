package app

import (
	"context"
	"math"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/util"
)

func TestCostSemanticsProxySnapshotSurvivesPricingAndMultiplierChanges(t *testing.T) {
	for _, source := range []string{costSourceSite, costSourceLocal} {
		t.Run(source, func(t *testing.T) {
			store, hash := newBillingTestStore(t)
			journal := openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db"))
			cache := newSitePricingCache()
			cache.storeChannelBindings(map[int64]channelBinding{}, time.Now())
			prices := tokenPricing("vip", provider.ModelPrice{Model: "gpt-5.6", ModelRatio: 1, CompletionRatio: 2})
			if source == costSourceSite {
				cache.storeChannelBindings(map[int64]channelBinding{17: {siteID: 5, group: "vip"}}, time.Now())
				cache.store(5, prices.pricing, false, time.Now())
			}
			server := &Server{store: store, sitePricing: cache, siteControl: &siteControlService{}, billing: newBillingService(store, journal)}
			cfg := &model.Config{ID: 17, CostMultiplier: 3}
			req := &proxyRequestContext{originalModel: "gpt-5.6", requestPath: "/v1/chat/completions", tokenHash: hash}
			result := &fwResult{Status: http.StatusOK, InputTokens: 1000, OutputTokens: 500, ToolCostUSD: 0.03}
			snapshot := server.costSnapshotForProxy(req, cfg, "gpt-5.6", result)
			entry := buildLogEntry(logEntryParams{
				RequestModel: req.originalModel, StatusCode: http.StatusOK, Result: result,
				CostMultiplier: cfg.CostMultiplier, CostSnapshot: snapshot,
			})
			if entry.CostSource != source || entry.Cost <= 0 {
				t.Fatalf("fixture did not freeze the intended source: %+v", entry)
			}
			// A later price fetch/config edit may affect the next attempt, but
			// cannot reinterpret this completed attempt between its consumers.
			cfg.CostMultiplier = 8
			cache.storeChannelBindings(map[int64]channelBinding{17: {siteID: 5, group: "vip"}}, time.Now())
			prices.pricing.Models[0].ModelRatio = 100
			cache.store(5, prices.pricing, false, time.Now())
			server.updateTokenStatsForProxy(req, cfg, true, 1, result, "gpt-5.6")
			pending, err := journal.Pending(context.Background(), 10)
			if err != nil || len(pending) != 1 {
				t.Fatalf("missing settlement: %v %v", pending, err)
			}
			if pending[0].CostUSD != entry.Cost || pending[0].EffectiveCostUSD != effectiveLogCost(entry) {
				t.Fatalf("one request changed amount between log and settlement: logged=%g effective=%g settled=%g effective=%g", entry.Cost, effectiveLogCost(entry), pending[0].CostUSD, pending[0].EffectiveCostUSD)
			}
		})
	}
}

func TestCostSemanticsExistingSnapshotRetainsAccountingSource(t *testing.T) {
	store, hash := newBillingTestStore(t)
	journal := openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db"))
	server := &Server{store: store, billing: newBillingService(store, journal)}
	result := &fwResult{Status: http.StatusOK, InputTokens: 10, ToolCostUSD: 0.02}
	siteAmount := 0.25
	snapshot := resolveRequestCost("gpt-5.6", result, 7, &siteAmount, nil)
	result.costSnapshot = &snapshot
	entry := buildLogEntry(logEntryParams{RequestModel: "gpt-5.6", StatusCode: http.StatusOK, Result: result, CostMultiplier: 7, CostSnapshot: &snapshot})
	// A compatibility caller can omit the already-resolved site pointer.
	// The existing snapshot must still determine the effective-cost rule.
	server.updateTokenStatsAsync(hash, 7, nil, true, 1, false, result, "gpt-5.6")
	pending, err := journal.Pending(context.Background(), 10)
	if err != nil || len(pending) != 1 || pending[0].CostUSD != entry.Cost || pending[0].EffectiveCostUSD != entry.Cost {
		t.Fatalf("frozen site source was replaced by the caller's current source: %v %v", pending, err)
	}
}

func TestCostSemanticsFrozenDetailsSurviveLocalCatalogueReplacement(t *testing.T) {
	util.RestoreEmbeddedModelCatalog()
	t.Cleanup(util.RestoreEmbeddedModelCatalog)
	install := func(price float64) {
		t.Helper()
		if err := util.InstallModelCatalog(&util.ModelCatalogSnapshot{
			Version: util.ModelCatalogSchemaVersion,
			Models:  []util.ModelCatalogEntry{{ID: "cost-catalog-fixture", Provider: "openai", Pricing: util.ModelPricing{InputPrice: price, OutputPrice: price, HasCacheReadPrice: true}}},
		}, "synthetic-test"); err != nil {
			t.Fatal(err)
		}
	}
	install(2)
	result := &fwResult{Status: http.StatusOK, InputTokens: 1000, CacheCreationInputTokens: 100}
	entry := buildLogEntry(logEntryParams{RequestModel: "cost-catalog-fixture", StatusCode: http.StatusOK, Result: result, CostMultiplier: 1})
	frozen := readLogCostDetails(entry)
	if frozen == nil || frozen.Breakdown == nil || frozen.Breakdown.CacheWrite.Quantity != 100 || entry.Cost <= 0 {
		t.Fatalf("local combined-cache fixture did not freeze evidence: %+v", frozen)
	}
	amount, detail := entry.Cost, entry.CostDetailJSON
	install(200)
	if now := util.CalculateStandardCostEstimate("cost-catalog-fixture", "", 1000, 0, 0, 100, 0); now.Breakdown.Total <= amount {
		t.Fatal("synthetic catalogue replacement did not change current pricing")
	}
	if logCostStatus(entry) != model.CostStatusEstimated || entry.Cost != amount || entry.CostDetailJSON != detail || buildLogCostBreakdown(entry).Total != amount {
		t.Fatal("read projection reinterpreted frozen local prices")
	}
	install(0)
	zero := buildLogEntry(logEntryParams{RequestModel: "cost-catalog-fixture", StatusCode: http.StatusOK, Result: result, CostMultiplier: 1})
	install(200)
	if logCostStatus(zero) != model.CostStatusZeroPrice || zero.Cost != 0 {
		t.Fatal("known request-time zero was reclassified after a catalogue update")
	}
}

func TestCostSemanticsCacheOnlyAndInvalidAmountsKeepTheirMeaning(t *testing.T) {
	cacheOnly := buildLogEntry(logEntryParams{RequestModel: "gpt-5.6", StatusCode: http.StatusOK, Result: &fwResult{CacheReadInputTokens: 100}, CostMultiplier: 1})
	if logCostStatus(cacheOnly) != model.CostStatusEstimated || cacheOnly.Cost <= 0 {
		t.Fatal("cache-only usage was classified as absent usage")
	}
	for _, cost := range []float64{-1, math.NaN(), math.Inf(1), 1e30} {
		result := &fwResult{InputTokens: 10, OutputTokens: 20}
		snapshot := resolveRequestCost("gpt-5.6", result, 1, &cost, nil)
		entry := buildLogEntry(logEntryParams{RequestModel: "gpt-5.6", StatusCode: http.StatusOK, Result: result, CostMultiplier: 1, CostSnapshot: &snapshot})
		if logCostStatus(entry) != model.CostStatusInvalid || entry.Cost != 0 || entry.InputTokens != 10 || readLogCostDetails(entry) != nil {
			t.Fatalf("invalid monetary input lost usage or claimed a valid zero: cost=%g status=%s", cost, logCostStatus(entry))
		}
	}
}

func TestCostSemanticsMixedAmountMatrixPreservesAccounting(t *testing.T) {
	for _, source := range []string{costSourceLocal, costSourceSite} {
		for _, multiplier := range []float64{0, 0.5, 2} {
			t.Run(source+"/"+strconv.FormatFloat(multiplier, 'f', -1, 64), func(t *testing.T) {
				store, hash := newBillingTestStore(t)
				journal := openBillingTestJournal(t, filepath.Join(t.TempDir(), "pending.db"))
				server := &Server{store: store, billing: newBillingService(store, journal)}
				result := &fwResult{InputTokens: 1000, OutputTokens: 500, ToolCostUSD: 0.03}
				var siteCost *float64
				wantStandard := 0.0405 // Sonnet: 1000*$3/M + 500*$15/M + $0.03 tool.
				if source == costSourceSite {
					siteCost = floatPtr(0.2)
					wantStandard = 0.23
				}
				snapshot := resolveRequestCost("claude-sonnet-4-5", result, multiplier, siteCost, nil)
				result.costSnapshot = &snapshot
				entry := buildLogEntry(logEntryParams{RequestModel: "claude-sonnet-4-5", StatusCode: http.StatusOK, Result: result, CostMultiplier: multiplier, CostSnapshot: &snapshot})
				wantEffective := wantStandard * multiplier
				if source == costSourceSite {
					wantEffective = wantStandard
				}
				if entry.CostBasis != model.CostBasisMixed || entry.CostSource != source || math.Abs(entry.Cost-wantStandard) > 1e-12 || math.Abs(effectiveLogCost(entry)-wantEffective) > 1e-12 {
					t.Fatalf("mixed pricing changed its formula: standard=%g effective=%g basis=%s source=%s", entry.Cost, effectiveLogCost(entry), entry.CostBasis, entry.CostSource)
				}
				server.updateTokenStatsAsync(hash, multiplier, siteCost, true, 1, false, result, "claude-sonnet-4-5")
				pending, err := journal.Pending(context.Background(), 10)
				if err != nil || len(pending) != 1 || math.Abs(pending[0].CostUSD-wantStandard) > 1e-12 || math.Abs(pending[0].EffectiveCostUSD-wantEffective) > 1e-12 {
					t.Fatalf("mixed settlement changed its formula: %v %v", pending, err)
				}
			})
		}
	}
}
