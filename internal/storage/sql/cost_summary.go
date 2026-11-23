package sql

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// GetCostSummary aggregates successful request costs using the same SQL
// filters as log list/count/stats/trend. It never loads individual logs.
func (s *SQLStore) GetCostSummary(
	ctx context.Context,
	since, until time.Time,
	filter *model.LogFilter,
) (*model.CostSummary, error) {
	statusSQL := costStatusSQL("")
	basisSQL := costBasisSQL("")
	recordedCostSQL := effectiveCostSQL("")
	completeCost := func(statuses ...string) string {
		condition := ""
		for i, status := range statuses {
			if i > 0 {
				condition += ","
			}
			condition += "'" + status + "'"
		}
		return fmt.Sprintf(
			"CASE WHEN %s IN (%s) THEN %s ELSE 0 END",
			statusSQL,
			condition,
			recordedCostSQL,
		)
	}

	baseQuery := fmt.Sprintf(`
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN %[1]s = '%[2]s' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN %[1]s IN ('%[3]s','%[4]s') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN %[1]s = '%[4]s' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN %[1]s = '%[5]s' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN %[1]s NOT IN ('%[2]s','%[3]s','%[4]s') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN %[1]s = '%[6]s' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN COALESCE(cost_source, '') = '%[7]s' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN COALESCE(cost_source, '') = '%[8]s' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN %[9]s = '%[10]s' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(%[11]s), 0),
			COALESCE(SUM(%[12]s), 0),
			COALESCE(SUM(%[13]s), 0),
			COALESCE(SUM(%[14]s), 0)
		FROM logs`,
		statusSQL,
		model.CostStatusEstimated,
		model.CostStatusZeroPrice,
		model.CostStatusLocalFree,
		model.CostStatusPartialEstimate,
		model.CostStatusHistoricalUnknown,
		model.CostSourceSite,
		model.CostSourceLocal,
		basisSQL,
		model.CostBasisMixed,
		recordedCostSQL,
		completeCost(model.CostStatusEstimated, model.CostStatusZeroPrice, model.CostStatusLocalFree),
		fmt.Sprintf(
			"CASE WHEN %s = '%s' THEN %s ELSE 0 END",
			statusSQL,
			model.CostStatusPartialEstimate,
			recordedCostSQL,
		),
		fmt.Sprintf(
			"CASE WHEN %s = '%s' THEN %s ELSE 0 END",
			statusSQL,
			model.CostStatusHistoricalUnknown,
			recordedCostSQL,
		),
	)

	qb := NewQueryBuilder(baseQuery).
		Where("time >= ?", since.UnixMilli()).
		Where("time <= ?", until.UnixMilli()).
		Where("channel_id > 0").
		Where("status_code >= 200").
		Where("status_code < 300")
	isEmpty, err := s.applyChannelFilter(ctx, qb, filter)
	if err != nil {
		return nil, fmt.Errorf("resolve cost summary channel filter: %w", err)
	}
	if isEmpty {
		return emptyCostSummary(), nil
	}
	qb.ApplyFilter(filter, s.driverName)

	query, args := qb.Build()
	summary := emptyCostSummary()
	err = s.QueryRowContext(ctx, query, args...).Scan(
		&summary.SuccessfulRequests,
		&summary.EstimatedRequests,
		&summary.ZeroRequests,
		&summary.LocalFreeRequests,
		&summary.PartialRequests,
		&summary.UnknownRequests,
		&summary.HistoricalRequests,
		&summary.SitePricingRequests,
		&summary.LocalEstimateRequests,
		&summary.MixedRequests,
		&summary.RecordedCostUSD,
		&summary.EstimatedCostUSD,
		&summary.PartialCostUSD,
		&summary.HistoricalCostUSD,
	)
	if err != nil {
		return nil, fmt.Errorf("query cost summary: %w", err)
	}
	if err := validateCostSummaryAmounts(summary); err != nil {
		return nil, err
	}
	return summary, nil
}

func emptyCostSummary() *model.CostSummary {
	return &model.CostSummary{
		Version:        1,
		Currency:       "USD",
		Reconciliation: "not_reconciled",
	}
}

func validateCostSummaryAmounts(summary *model.CostSummary) error {
	amounts := []struct {
		field string
		value float64
	}{
		{field: "recorded_cost_usd", value: summary.RecordedCostUSD},
		{field: "estimated_cost_usd", value: summary.EstimatedCostUSD},
		{field: "partial_cost_usd", value: summary.PartialCostUSD},
		{field: "historical_cost_usd", value: summary.HistoricalCostUSD},
	}
	for _, amount := range amounts {
		if math.IsNaN(amount.value) || math.IsInf(amount.value, 0) || amount.value < 0 {
			return fmt.Errorf("cost summary %s is non-finite or negative: %v", amount.field, amount.value)
		}
	}
	return nil
}
