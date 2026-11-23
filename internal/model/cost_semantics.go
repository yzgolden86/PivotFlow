package model

import "math"

const CostMetadataVersion = 1

const (
	CostSourceSite  = "site_pricing"
	CostSourceLocal = "local_estimate"

	CostStatusEstimated         = "estimated"
	CostStatusZeroPrice         = "zero_price"
	CostStatusLocalFree         = "local_free"
	CostStatusUsageMissing      = "usage_missing"
	CostStatusUnpricedModel     = "unpriced_model"
	CostStatusPartialEstimate   = "partial_estimate"
	CostStatusInvalid           = "invalid"
	CostStatusHistoricalUnknown = "historical_unknown"
	CostStatusNotCharged        = "not_charged"

	CostBasisToken   = "token"
	CostBasisPerCall = "per_call"
	CostBasisMixed   = "mixed"
	CostBasisUnknown = "unknown"
)

// CostSummary covers successful stored log rows under the same request filters.
// UnknownRequests includes partial and historical rows. LocalFreeRequests is a
// subset of ZeroRequests. Monetary values are local accounting in USD, and do
// not constitute an upstream invoice or a complete total when rows are unknown.
type CostSummary struct {
	Version               int     `json:"version"`
	Currency              string  `json:"currency"`
	Reconciliation        string  `json:"reconciliation"`
	SuccessfulRequests    int64   `json:"successful_requests"`
	EstimatedRequests     int64   `json:"estimated_requests"`
	ZeroRequests          int64   `json:"zero_requests"`
	LocalFreeRequests     int64   `json:"local_free_requests"`
	PartialRequests       int64   `json:"partial_requests"`
	UnknownRequests       int64   `json:"unknown_requests"`
	HistoricalRequests    int64   `json:"historical_requests"`
	SitePricingRequests   int64   `json:"site_pricing_requests"`
	LocalEstimateRequests int64   `json:"local_estimate_requests"`
	MixedRequests         int64   `json:"mixed_requests"`
	RecordedCostUSD       float64 `json:"recorded_cost_usd"`
	EstimatedCostUSD      float64 `json:"estimated_cost_usd"`
	PartialCostUSD        float64 `json:"partial_cost_usd"`
	HistoricalCostUSD     float64 `json:"historical_cost_usd"`
}

func IsCostStatus(status string) bool {
	switch status {
	case CostStatusEstimated, CostStatusZeroPrice, CostStatusLocalFree,
		CostStatusUsageMissing, CostStatusUnpricedModel, CostStatusPartialEstimate,
		CostStatusInvalid, CostStatusHistoricalUnknown, CostStatusNotCharged:
		return true
	default:
		return false
	}
}

func IsCostBasis(basis string) bool {
	switch basis {
	case CostBasisToken, CostBasisPerCall, CostBasisMixed, CostBasisUnknown:
		return true
	default:
		return false
	}
}

func IsValidCostAmount(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

// LogCostStatus uses only evidence frozen on this row. It never queries today's
// pricing catalogue, so updates cannot relabel historical zero costs as free.
func LogCostStatus(entry *LogEntry) string {
	if entry == nil || entry.StatusCode < 200 || entry.StatusCode >= 300 {
		return CostStatusNotCharged
	}
	if !IsValidCostAmount(entry.Cost) || math.IsNaN(entry.CostMultiplier) || math.IsInf(entry.CostMultiplier, 0) {
		return CostStatusInvalid
	}
	if entry.CostMetadataVersion != CostMetadataVersion || !IsCostStatus(entry.CostStatus) || entry.CostStatus == CostStatusNotCharged {
		return CostStatusHistoricalUnknown
	}
	return entry.CostStatus
}

func LogCostBasis(entry *LogEntry) string {
	if entry == nil || entry.CostMetadataVersion != CostMetadataVersion || !IsCostBasis(entry.CostBasis) {
		return CostBasisUnknown
	}
	return entry.CostBasis
}
