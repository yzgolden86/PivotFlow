package model

import (
	"math"
	"testing"
)

func TestLogCostMetadataUsesPersistedEvidence(t *testing.T) {
	entry := &LogEntry{StatusCode: 200, Cost: 0, CostMultiplier: 1, CostMetadataVersion: 1, CostStatus: CostStatusZeroPrice, CostBasis: CostBasisPerCall}
	if LogCostStatus(entry) != CostStatusZeroPrice || LogCostBasis(entry) != CostBasisPerCall {
		t.Fatal("frozen zero-price per-call evidence was lost")
	}
	entry.Model = "model-no-longer-priced"
	if LogCostStatus(entry) != CostStatusZeroPrice {
		t.Fatal("model catalogue identity changed the historical state")
	}
	entry.CostMetadataVersion = 2
	if LogCostStatus(entry) != CostStatusHistoricalUnknown || LogCostBasis(entry) != CostBasisUnknown {
		t.Fatal("unknown metadata version invented evidence")
	}
}

func TestLogCostMetadataRejectsInvalidAmounts(t *testing.T) {
	for _, value := range []float64{-1, math.NaN(), math.Inf(1)} {
		entry := &LogEntry{StatusCode: 200, Cost: value, CostMultiplier: 1, CostMetadataVersion: 1, CostStatus: CostStatusEstimated}
		if LogCostStatus(entry) != CostStatusInvalid {
			t.Errorf("invalid amount %g was reported as priced", value)
		}
	}
	if LogCostStatus(&LogEntry{StatusCode: 599, Cost: math.Inf(1)}) != CostStatusNotCharged {
		t.Fatal("failed request claimed a successful charge")
	}
}
