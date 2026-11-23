package storage

import (
	"context"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestHybridStoreCostSummaryReadsLocalLogSnapshot(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()
	now := time.Now()
	channel, err := hybrid.CreateConfig(ctx, &model.Config{
		Name:         "hybrid-cost-summary",
		URLs:         model.ChannelURLs{{URL: "https://hybrid-cost.example"}},
		Enabled:      true,
		ModelEntries: []model.ModelEntry{{Model: "gpt-cost"}},
	})
	if err != nil {
		t.Fatalf("CreateConfig: %v", err)
	}
	if err := hybrid.AddLog(ctx, &model.LogEntry{
		Time:                model.JSONTime{Time: now},
		Model:               "gpt-cost",
		LogSource:           model.LogSourceProxy,
		ChannelID:           channel.ID,
		StatusCode:          200,
		Message:             "hybrid-summary",
		CostMetadataVersion: 1,
		CostStatus:          model.CostStatusEstimated,
		CostBasis:           model.CostBasisToken,
		CostSource:          model.CostSourceLocal,
		Cost:                0.25,
		CostMultiplier:      2,
	}); err != nil {
		t.Fatalf("AddLog: %v", err)
	}

	summary, err := hybrid.GetCostSummary(ctx, now.Add(-time.Minute), now.Add(time.Minute), nil)
	if err != nil {
		t.Fatalf("GetCostSummary: %v", err)
	}
	if summary.SuccessfulRequests != 1 || summary.EstimatedRequests != 1 {
		t.Fatalf("summary counts=%+v, want one estimated success", summary)
	}
	if summary.RecordedCostUSD != 0.5 || summary.EstimatedCostUSD != 0.5 {
		t.Fatalf("summary costs=(%v,%v), want 0.5", summary.RecordedCostUSD, summary.EstimatedCostUSD)
	}
}
