package app

import (
	"encoding/json"
	"math"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/util"
)

// Cost details contain only frozen numeric pricing evidence. No upstream table,
// account, credential or raw provider response is retained in the public DTO.
type requestCostDetails struct {
	ModelCostUSD float64                     `json:"model_cost_usd"`
	ToolCostUSD  float64                     `json:"tool_cost_usd"`
	Currency     string                      `json:"currency"`
	Breakdown    *util.StandardCostBreakdown `json:"breakdown,omitempty"`
}

type siteCostEstimate struct {
	cost      float64
	basis     string
	breakdown *util.StandardCostBreakdown
}

type requestCostSnapshot struct {
	cost       float64
	multiplier float64
	source     string
	status     string
	basis      string
	detailJSON string
}

func validRequestCostInputs(res *fwResult) bool {
	return res != nil && res.InputTokens >= 0 && res.OutputTokens >= 0 &&
		res.CacheReadInputTokens >= 0 && res.CacheCreationInputTokens >= 0 &&
		res.Cache5mInputTokens >= 0 && res.Cache1hInputTokens >= 0 &&
		model.IsValidCostAmount(res.ToolCostUSD)
}

func hasCostUsage(res *fwResult) bool {
	return res != nil && (res.InputTokens > 0 || res.OutputTokens > 0 || res.CacheReadInputTokens > 0 ||
		res.CacheCreationInputTokens > 0 || res.Cache5mInputTokens > 0 || res.Cache1hInputTokens > 0)
}

func resolveRequestCost(billingModel string, res *fwResult, multiplier float64, siteCost *float64, siteEstimate *siteCostEstimate) requestCostSnapshot {
	snapshot := requestCostSnapshot{multiplier: multiplier, source: costSourceLocal, basis: model.CostBasisUnknown, status: model.CostStatusUsageMissing}
	if multiplier < 0 || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		snapshot.multiplier = 1
	}
	if res == nil {
		return snapshot
	}
	if siteCost != nil {
		snapshot.source = costSourceSite
	}
	if !validRequestCostInputs(res) || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		snapshot.status = model.CostStatusInvalid
		return snapshot
	}

	details := requestCostDetails{ToolCostUSD: res.ToolCostUSD, Currency: "USD"}
	modelPriced := false
	if siteCost != nil {
		if !model.IsValidCostAmount(*siteCost) {
			snapshot.status = model.CostStatusInvalid
			return snapshot
		}
		details.ModelCostUSD = *siteCost
		modelPriced = true
		if siteEstimate != nil {
			snapshot.basis = siteEstimate.basis
			details.Breakdown = siteEstimate.breakdown
		} else if hasCostUsage(res) {
			// Internal compatibility callers have an amount but no per-call
			// evidence; token counts can only establish a token basis.
			snapshot.basis = model.CostBasisToken
		}
	} else {
		cache5m, cache1h := res.Cache5mInputTokens, res.Cache1hInputTokens
		if cache5m+cache1h == 0 {
			cache5m = res.CacheCreationInputTokens
		}
		estimate := util.CalculateStandardCostEstimate(billingModel, res.ServiceTier, res.InputTokens, res.OutputTokens, res.CacheReadInputTokens, cache5m, cache1h)
		modelPriced = estimate.Priced
		details.ModelCostUSD = estimate.Breakdown.Total
		if modelPriced {
			if estimate.PerCall {
				snapshot.basis = model.CostBasisPerCall
			} else {
				snapshot.basis = model.CostBasisToken
				details.Breakdown = &estimate.Breakdown
			}
		}
	}
	snapshot.cost = details.ModelCostUSD + details.ToolCostUSD
	if model.ValidateTokenSettlementCosts(snapshot.cost, effectiveCost(snapshot.cost, multiplier, snapshot.source)) != nil {
		snapshot.cost = 0
		snapshot.status = model.CostStatusInvalid
		return snapshot
	}
	modelComplete := modelPriced && (snapshot.basis == model.CostBasisPerCall || hasCostUsage(res) || details.ModelCostUSD > 0)
	if details.ToolCostUSD > 0 {
		snapshot.basis = model.CostBasisMixed
	}
	switch {
	case snapshot.source == costSourceLocal && multiplier == 0:
		snapshot.status = model.CostStatusLocalFree
	case !modelComplete && details.ToolCostUSD > 0:
		snapshot.status = model.CostStatusPartialEstimate
	case !modelPriced:
		snapshot.status = model.CostStatusUnpricedModel
	case !modelComplete:
		snapshot.status = model.CostStatusUsageMissing
	case snapshot.cost == 0:
		snapshot.status = model.CostStatusZeroPrice
	default:
		snapshot.status = model.CostStatusEstimated
	}
	if !modelComplete {
		details.Breakdown = nil
	}
	if encoded, err := json.Marshal(details); err == nil {
		snapshot.detailJSON = string(encoded)
	} else {
		snapshot.cost = 0
		snapshot.status = model.CostStatusInvalid
	}
	return snapshot
}

func applyRequestCostSnapshot(entry *model.LogEntry, snapshot requestCostSnapshot) {
	entry.Cost = snapshot.cost
	entry.CostMultiplier = snapshot.multiplier
	entry.CostSource = snapshot.source
	entry.CostMetadataVersion = model.CostMetadataVersion
	entry.CostStatus = snapshot.status
	entry.CostBasis = snapshot.basis
	entry.CostDetailJSON = snapshot.detailJSON
}

func readLogCostDetails(entry *model.LogEntry) *requestCostDetails {
	if entry == nil || entry.CostMetadataVersion != model.CostMetadataVersion || entry.CostDetailJSON == "" || len(entry.CostDetailJSON) > 16*1024 {
		return nil
	}
	status := model.LogCostStatus(entry)
	if status == model.CostStatusInvalid || status == model.CostStatusHistoricalUnknown || status == model.CostStatusNotCharged {
		return nil
	}
	var details requestCostDetails
	if json.Unmarshal([]byte(entry.CostDetailJSON), &details) != nil || details.Currency != "USD" ||
		!model.IsValidCostAmount(details.ModelCostUSD) || !model.IsValidCostAmount(details.ToolCostUSD) {
		return nil
	}
	if breakdown := details.Breakdown; breakdown != nil {
		if !model.IsValidCostAmount(breakdown.Total) || !model.IsValidCostAmount(breakdown.ServiceTierMultiplier) {
			return nil
		}
		for _, component := range []util.CostComponent{breakdown.Input, breakdown.Output, breakdown.CacheRead, breakdown.CacheWrite} {
			if component.Quantity < 0 || !model.IsValidCostAmount(component.PricePerMillion) || !model.IsValidCostAmount(component.Cost) {
				return nil
			}
		}
	}
	return &details
}
