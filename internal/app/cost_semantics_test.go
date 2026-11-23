package app

import (
	"encoding/json"
	"math"
	"net/http"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func TestCostSemanticsNeverInventsHistoricalPricingEvidence(t *testing.T) {
	for _, entry := range []*model.LogEntry{
		{StatusCode: http.StatusOK, Model: "gpt-5.6", InputTokens: 1000, Cost: 0.1, CostMultiplier: 1, CostSource: costSourceLocal},
		{StatusCode: http.StatusOK, Model: "gpt-5.6", CostMultiplier: 1, CostSource: costSourceSite},
		{StatusCode: http.StatusOK, Model: "unknown-at-request-time", InputTokens: 1000, CostMultiplier: 1},
	} {
		if got := logCostStatus(entry); got != "historical_unknown" {
			t.Errorf("legacy row classified using present-day prices: status=%q, want historical_unknown", got)
		}
		if got := buildLogCostBreakdown(entry); got != nil {
			t.Errorf("legacy row invented a current-price breakdown: %+v", got)
		}
	}
}

func TestCostSemanticsFailedRequestsDoNotClaimEstimatedZero(t *testing.T) {
	entry := &model.LogEntry{StatusCode: http.StatusBadGateway, CostMultiplier: 1}
	if got := logCostStatus(entry); got != "not_charged" {
		t.Fatalf("failed request cost state=%q, want not_charged", got)
	}
}

func TestCostSemanticsNilResultDoesNotInventPricingEvidence(t *testing.T) {
	entry := buildLogEntry(logEntryParams{
		RequestModel: "gpt-5.6", StatusCode: http.StatusOK, CostMultiplier: 1,
	})
	if entry.CostMetadataVersion != 0 || entry.CostStatus != "" || entry.CostBasis != "" || entry.CostDetailJSON != "" {
		t.Fatalf("missing response froze pricing evidence that was never obtained: version=%d status=%q basis=%q",
			entry.CostMetadataVersion, entry.CostStatus, entry.CostBasis)
	}
	if got := model.LogCostStatus(entry); got != model.CostStatusHistoricalUnknown {
		t.Fatalf("unversioned missing response cost status=%q, want historical_unknown", got)
	}
	if entry.Cost != 0 || readLogCostDetails(entry) != nil {
		t.Fatal("missing response invented a monetary charge or detail")
	}
}

func TestCostSemanticsInvalidLogAmountsRemainJSONSafe(t *testing.T) {
	for _, cost := range []float64{-1, math.NaN(), math.Inf(1)} {
		entry := &model.LogEntry{StatusCode: http.StatusOK, Cost: cost, CostMultiplier: 1}
		if got := logCostStatus(entry); got != "invalid" {
			t.Errorf("invalid amount %g cost state=%q, want invalid", cost, got)
		}
	}
}

func TestCostSemanticsInvalidProjectionDoesNotExposeAnEffectiveCharge(t *testing.T) {
	entry := &model.LogEntry{StatusCode: http.StatusOK, Cost: 0.25, CostMultiplier: 2, CostMetadataVersion: 1, CostStatus: model.CostStatusInvalid}
	admin := projectDashboardLogs([]*model.LogEntry{entry})
	if len(admin) != 1 || admin[0].EffectiveCost != 0 {
		t.Fatal("invalid admin log still exposes a positive effective charge")
	}
	token := projectTokenLogs([]*model.LogEntry{entry}, nil)
	if len(token) != 1 || token[0].EffectiveCost != 0 {
		t.Fatal("invalid token log still exposes a positive effective charge")
	}
	if entry.Cost != 0.25 || entry.CostMultiplier != 2 {
		t.Fatal("public projection changed the original audit amount")
	}
}

func TestCostSemanticsSnapshotClassifiesKnownAndUnknownAmounts(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		result     *fwResult
		multiplier float64
		want       string
		basis      string
	}{
		{"positive standard cost zero local multiplier", "gpt-5.6", &fwResult{InputTokens: 1000}, 0, "local_free", "token"},
		{"token price without usage", "gpt-5.6", &fwResult{}, 1, "usage_missing", "token"},
		{"known free model with usage", "qwen3.6-plus:free", &fwResult{InputTokens: 1000}, 1, "zero_price", "token"},
		{"known free model without usage", "qwen3.6-plus:free", &fwResult{}, 1, "usage_missing", "token"},
		{"no price", "not-a-priced-model", &fwResult{InputTokens: 1000}, 1, "unpriced_model", "unknown"},
		{"no model price only tool", "not-a-priced-model", &fwResult{InputTokens: 1000, ToolCostUSD: 0.02}, 1, "partial_estimate", "mixed"},
		{"missing model usage only tool", "gpt-5.6", &fwResult{ToolCostUSD: 0.02}, 1, "partial_estimate", "mixed"},
		{"per call no token", "search_call", &fwResult{}, 1, "estimated", "per_call"},
		{"negative quantity", "gpt-5.6", &fwResult{InputTokens: -1, OutputTokens: 100}, 1, "invalid", "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entry := buildLogEntry(logEntryParams{RequestModel: tc.model, StatusCode: 200, Result: tc.result, CostMultiplier: tc.multiplier})
			if got := logCostStatus(entry); got != tc.want {
				t.Fatalf("status=%q, want %q", got, tc.want)
			}
			if got := model.LogCostBasis(entry); got != tc.basis {
				t.Fatalf("basis=%q, want %q", got, tc.basis)
			}
			if entry.CostMetadataVersion != 1 {
				t.Fatalf("metadata version=%d, want 1", entry.CostMetadataVersion)
			}
		})
	}
}

func TestCostSemanticsSitePerCallZeroIsKnownWithoutTokenUsage(t *testing.T) {
	price := provider.ModelPrice{Model: "per-call-zero", QuotaType: 1, PerCallPrice: 0}
	estimate, ok := computeSiteCostEstimate(tokenPricing("default", price), price.Model, &fwResult{})
	if !ok || estimate.basis != model.CostBasisPerCall {
		t.Fatal("site per-call zero lost its pricing basis")
	}
	entry := buildLogEntry(logEntryParams{RequestModel: price.Model, StatusCode: 200, Result: &fwResult{}, CostMultiplier: 0, SiteCost: &estimate.cost, SiteEstimate: &estimate})
	if got := logCostStatus(entry); got != model.CostStatusZeroPrice {
		t.Fatalf("site zero-price per-call state=%q, want zero_price", got)
	}
}

func TestCostSemanticsFreezesSiteAndLocalDetails(t *testing.T) {
	price := provider.ModelPrice{Model: "gpt-5.6", ModelRatio: 1, CompletionRatio: 2, CacheRatio: 0.25, CacheCreationRatio: 1.25}
	result := &fwResult{InputTokens: 1000, OutputTokens: 500, CacheReadInputTokens: 100, ToolCostUSD: 0.03}
	estimate, ok := computeSiteCostEstimate(tokenPricing("vip", price), price.Model, result)
	if !ok {
		t.Fatal("test site pricing did not resolve")
	}
	entry := buildLogEntry(logEntryParams{RequestModel: price.Model, StatusCode: 200, Result: result, CostMultiplier: 5, SiteCost: &estimate.cost, SiteEstimate: &estimate})
	details := readLogCostDetails(entry)
	if details == nil || details.Breakdown == nil || details.Breakdown.Total != estimate.cost || details.ToolCostUSD != 0.03 {
		t.Fatalf("site details did not match frozen amount: %+v", details)
	}
	if entry.CostSource != costSourceSite || effectiveLogCost(entry) != entry.Cost || model.LogCostBasis(entry) != model.CostBasisMixed {
		t.Fatal("mixed site/tool accounting multiplied the site estimate twice")
	}
	// Once persisted, changing the model identity cannot replace the exact
	// frozen site ratios with today's local vendor list prices.
	entry.ActualModel = "unpriced-new-model"
	if breakdown := buildLogCostBreakdown(entry); breakdown == nil || breakdown.Total != estimate.cost {
		t.Fatal("read projection recomputed the stored site breakdown")
	}
	encoded, err := json.Marshal(projectDashboardLogs([]*model.LogEntry{entry}))
	if err != nil || len(encoded) == 0 {
		t.Fatalf("safe log projection did not marshal: %v", err)
	}
}
