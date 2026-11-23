package util

import (
	"reflect"
	"testing"
)

func TestStandardCostEstimatePreservesExistingPricingAlgorithms(t *testing.T) {
	for _, tc := range []struct {
		model, tier                           string
		input, output, read, write5m, write1h int
	}{
		{"gpt-5.6", "priority", 1000, 500, 250, 100, 25},
		{"gpt-5.6", "flex", 300000, 1000, 200000, 0, 0},
		{"claude-opus-4-6", "fast", 400000, 300, 200000, 100, 50},
		{"claude-sonnet-4-5-20250929", "", 12, 73, 17558, 278, 100},
		{"gemini-2.5-pro", "", 250000, 1000, 0, 0, 0},
		{"qwen3.6-plus:free", "", 1000, 200, 0, 0, 0},
		{"search_call", "", 0, 0, 0, 0, 0},
	} {
		t.Run(tc.model+"/"+tc.tier, func(t *testing.T) {
			got := CalculateStandardCostEstimate(tc.model, tc.tier, tc.input, tc.output, tc.read, tc.write5m, tc.write1h)
			want := CalculateStandardCostBreakdown(tc.model, tc.tier, tc.input, tc.output, tc.read, tc.write5m, tc.write1h)
			if !got.Priced || !reflect.DeepEqual(got.Breakdown, want) {
				t.Fatalf("snapshot changed a valid pricing formula: got=%+v want=%+v", got, want)
			}
			if got.PerCall != (tc.model == "search_call") {
				t.Fatalf("pricing basis lost: per_call=%v", got.PerCall)
			}
		})
	}
	if got := CalculateStandardCostEstimate("cost-unknown-no-catalogue-entry", "", 1000, 200, 0, 0, 0); got.Priced {
		t.Fatal("unknown price was represented as a known zero")
	}
}
