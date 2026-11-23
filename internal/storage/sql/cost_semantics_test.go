package sql_test

import (
	"context"
	"database/sql"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestLogCostMetadataRoundTripsThroughSingleBatchAndDebugWrites(t *testing.T) {
	store := newTestStore(t, "cost_metadata_roundtrip.db")
	ctx := context.Background()
	channelID := createTestChannel(t, ctx, store, "cost-metadata")
	now := time.Now()

	entries := []*model.LogEntry{
		{
			Time:                model.JSONTime{Time: now.Add(-3 * time.Second)},
			Model:               "gpt-cost",
			LogSource:           model.LogSourceProxy,
			ChannelID:           channelID,
			StatusCode:          200,
			Message:             "single",
			RequestID:           "cost-single",
			CostMetadataVersion: 1,
			CostStatus:          "estimated",
			CostBasis:           "token",
			CostDetailJSON:      `{"model_cost_usd":0.1,"tool_cost_usd":0,"currency":"USD"}`,
			Cost:                0.1,
			CostMultiplier:      1,
			CostSource:          "local_estimate",
		},
		{
			Time:                model.JSONTime{Time: now.Add(-2 * time.Second)},
			Model:               "gpt-cost",
			LogSource:           model.LogSourceProxy,
			ChannelID:           channelID,
			StatusCode:          200,
			Message:             "batch",
			RequestID:           "cost-batch",
			CostMetadataVersion: 1,
			CostStatus:          "partial_estimate",
			CostBasis:           "mixed",
			CostDetailJSON:      `{"model_cost_usd":0,"tool_cost_usd":0.2,"currency":"USD"}`,
			Cost:                0.2,
			CostMultiplier:      1,
			CostSource:          "site_pricing",
		},
	}
	if err := store.AddLog(ctx, entries[0]); err != nil {
		t.Fatalf("AddLog: %v", err)
	}
	if err := store.BatchAddLogs(ctx, entries[1:]); err != nil {
		t.Fatalf("BatchAddLogs: %v", err)
	}

	debugEntry := &model.LogEntry{
		Time:                model.JSONTime{Time: now.Add(-time.Second)},
		Model:               "gpt-cost",
		LogSource:           model.LogSourceProxy,
		ChannelID:           channelID,
		StatusCode:          200,
		Message:             "debug",
		RequestID:           "cost-debug",
		CostMetadataVersion: 1,
		CostStatus:          "zero_price",
		CostBasis:           "per_call",
		CostDetailJSON:      `{"model_cost_usd":0,"tool_cost_usd":0,"currency":"USD"}`,
		CostMultiplier:      1,
		CostSource:          "site_pricing",
		DebugData: &model.DebugLogEntry{
			CreatedAt:  now.Unix(),
			ReqMethod:  "POST",
			ReqURL:     "https://example.invalid/v1/chat/completions",
			ReqHeaders: "{}",
			ReqBody:    []byte(`{}`),
		},
	}
	if err := store.AddLog(ctx, debugEntry); err != nil {
		t.Fatalf("AddLog with debug metadata: %v", err)
	}

	logs, total, err := store.ListLogsRangeWithCount(ctx, now.Add(-time.Minute), now.Add(time.Minute), 20, 0, nil)
	if err != nil {
		t.Fatalf("ListLogsRangeWithCount: %v", err)
	}
	if total != 3 || len(logs) != 3 {
		t.Fatalf("logs total=%d len=%d, want 3", total, len(logs))
	}
	byRequestID := make(map[string]*model.LogEntry, len(logs))
	for _, entry := range logs {
		byRequestID[entry.RequestID] = entry
	}
	for _, want := range append(entries, debugEntry) {
		got := byRequestID[want.RequestID]
		if got == nil {
			t.Fatalf("missing request %q", want.RequestID)
		}
		if got.CostMetadataVersion != want.CostMetadataVersion ||
			got.CostStatus != want.CostStatus ||
			got.CostBasis != want.CostBasis ||
			got.CostDetailJSON != want.CostDetailJSON {
			t.Fatalf("request %q metadata=(%d,%q,%q,%q), want (%d,%q,%q,%q)",
				want.RequestID,
				got.CostMetadataVersion, got.CostStatus, got.CostBasis, got.CostDetailJSON,
				want.CostMetadataVersion, want.CostStatus, want.CostBasis, want.CostDetailJSON)
		}
	}
}

func TestCostSummaryAggregatesStatusSourceAndSafeAmounts(t *testing.T) {
	store := newTestStore(t, "cost_summary.db")
	ctx := context.Background()
	channelID := createTestChannel(t, ctx, store, "cost-summary")
	now := time.Now()

	entries := []*model.LogEntry{
		costLog(now, channelID, 10, 200, "site-estimated", 1, "estimated", "token", "site_pricing", 0.5, 3000),
		costLog(now, channelID, 11, 200, "local-estimated", 1, "estimated", "per_call", "local_estimate", 0.2, 2),
		costLog(now, channelID, 12, 200, "zero-price", 1, "zero_price", "token", "site_pricing", 0, 1),
		costLog(now, channelID, 13, 200, "local-free", 1, "local_free", "token", "local_estimate", 0.3, 0),
		costLog(now, channelID, 14, 200, "usage-missing", 1, "usage_missing", "token", "local_estimate", 0, 1),
		costLog(now, channelID, 15, 200, "unpriced", 1, "unpriced_model", "unknown", "local_estimate", 0, 1),
		costLog(now, channelID, 16, 200, "partial", 1, "partial_estimate", "mixed", "local_estimate", 0.1, 1),
		costLog(now, channelID, 17, 200, "historical", 0, "estimated", "token", "local_estimate", 0.2, 1),
		costLog(now, channelID, 18, 200, "invalid", 1, "invalid", "unknown", "local_estimate", 0.9, 1),
		costLog(now, channelID, 19, 500, "failed", 1, "estimated", "token", "site_pricing", 5, 1),
	}
	if err := store.BatchAddLogs(ctx, entries); err != nil {
		t.Fatalf("BatchAddLogs: %v", err)
	}

	summary, err := store.GetCostSummary(ctx, now.Add(-time.Minute), now.Add(time.Minute), &model.LogFilter{
		LogSource: model.LogSourceProxy,
	})
	if err != nil {
		t.Fatalf("GetCostSummary: %v", err)
	}
	if summary.Version != 1 || summary.Currency != "USD" || summary.Reconciliation != "not_reconciled" {
		t.Fatalf("summary metadata=(%d,%q,%q)", summary.Version, summary.Currency, summary.Reconciliation)
	}
	assertInt64 := func(name string, got, want int64) {
		t.Helper()
		if got != want {
			t.Fatalf("%s=%d, want %d", name, got, want)
		}
	}
	assertFloat := func(name string, got, want float64) {
		t.Helper()
		if math.Abs(got-want) > 1e-9 {
			t.Fatalf("%s=%.12f, want %.12f", name, got, want)
		}
	}
	assertInt64("successful_requests", summary.SuccessfulRequests, 9)
	assertInt64("estimated_requests", summary.EstimatedRequests, 2)
	assertInt64("zero_requests", summary.ZeroRequests, 2)
	assertInt64("local_free_requests", summary.LocalFreeRequests, 1)
	assertInt64("partial_requests", summary.PartialRequests, 1)
	assertInt64("unknown_requests", summary.UnknownRequests, 5)
	assertInt64("historical_requests", summary.HistoricalRequests, 1)
	assertInt64("site_pricing_requests", summary.SitePricingRequests, 2)
	assertInt64("local_estimate_requests", summary.LocalEstimateRequests, 7)
	assertInt64("mixed_requests", summary.MixedRequests, 1)
	assertFloat("recorded_cost_usd", summary.RecordedCostUSD, 1.2)
	assertFloat("estimated_cost_usd", summary.EstimatedCostUSD, 0.9)
	assertFloat("partial_cost_usd", summary.PartialCostUSD, 0.1)
	assertFloat("historical_cost_usd", summary.HistoricalCostUSD, 0.2)
}

func TestCostSummaryEmptyRangeReturnsCompleteZeroShape(t *testing.T) {
	store := newTestStore(t, "cost_summary_empty.db")
	now := time.Now()
	summary, err := store.GetCostSummary(context.Background(), now, now.Add(time.Minute), nil)
	if err != nil {
		t.Fatalf("GetCostSummary: %v", err)
	}
	if summary == nil {
		t.Fatal("GetCostSummary returned nil")
	}
	if summary.Version != 1 || summary.Currency != "USD" || summary.Reconciliation != "not_reconciled" {
		t.Fatalf("summary metadata=(%d,%q,%q)", summary.Version, summary.Currency, summary.Reconciliation)
	}
	if summary.SuccessfulRequests != 0 ||
		summary.EstimatedRequests != 0 ||
		summary.ZeroRequests != 0 ||
		summary.LocalFreeRequests != 0 ||
		summary.PartialRequests != 0 ||
		summary.UnknownRequests != 0 ||
		summary.HistoricalRequests != 0 ||
		summary.SitePricingRequests != 0 ||
		summary.LocalEstimateRequests != 0 ||
		summary.MixedRequests != 0 ||
		summary.RecordedCostUSD != 0 ||
		summary.EstimatedCostUSD != 0 ||
		summary.PartialCostUSD != 0 ||
		summary.HistoricalCostUSD != 0 {
		t.Fatalf("empty summary is not zero-valued: %+v", summary)
	}
}

func TestCostFiltersUseModelStatusAndBasisAcrossLogStatsTrendAndSummary(t *testing.T) {
	store := newTestStore(t, "cost_filters.db")
	ctx := context.Background()
	channelID := createTestChannel(t, ctx, store, "cost-filters")
	now := time.Now().Truncate(time.Minute).Add(10 * time.Second)
	tokenOne, tokenTwo := int64(101), int64(202)

	entries := []*model.LogEntry{
		costLogWithToken(now, channelID, tokenOne, 20, 200, "estimated-site", 1, "estimated", "token", "site_pricing", 0.5, 10),
		costLogWithToken(now, channelID, tokenOne, 21, 200, "partial-local", 1, "partial_estimate", "mixed", "local_estimate", 0.1, 2),
		costLogWithToken(now, channelID, tokenOne, 22, 200, "historical", 0, "partial_estimate", "mixed", "local_estimate", 0.2, 2),
		costLogWithToken(now, channelID, tokenOne, 23, 500, "failed-persisted-estimate", 1, "estimated", "token", "site_pricing", 1, 1),
		costLogWithToken(now, channelID, tokenOne, 24, 200, "invalid-status", 1, "not-a-status", "token", "local_estimate", 0.3, 1),
		costLogWithToken(now, channelID, tokenOne, 25, 200, "usage-missing", 1, "usage_missing", "unknown", "local_estimate", 0, 1),
		costLogWithToken(now, channelID, tokenTwo, 26, 200, "other-token", 1, "estimated", "token", "site_pricing", 0.4, 1),
		costLogWithToken(now, channelID, tokenOne, 27, 200, "zero-price-per-call", 1, "zero_price", "per_call", "site_pricing", 0, 1),
		costLogWithToken(now, channelID, tokenOne, 28, 200, "local-free", 1, "local_free", "token", "local_estimate", 0.2, 0),
		costLogWithToken(now, channelID, tokenOne, 29, 200, "unpriced", 1, "unpriced_model", "unknown", "local_estimate", 0, 1),
		costLogWithToken(now, channelID, tokenOne, 30, 200, "negative-overrides-estimated", 1, "estimated", "token", "local_estimate", -0.1, 1),
		costLogWithToken(now, channelID, tokenOne, 31, 200, "not-charged-on-success", 1, "not_charged", "token", "site_pricing", 0.1, 1),
	}
	if err := store.BatchAddLogs(ctx, entries); err != nil {
		t.Fatalf("BatchAddLogs: %v", err)
	}

	statusFilters := []string{
		"estimated", "zero_price", "local_free", "usage_missing", "unpriced_model",
		"partial_estimate", "invalid", "historical_unknown", "not_charged",
		"not-a-status",
	}
	basisFilters := []string{"token", "per_call", "mixed", "unknown", "not-a-basis"}
	for _, status := range statusFilters {
		t.Run("status/"+status, func(t *testing.T) {
			assertCostFilterMatchesModel(t, store, entries, now, &model.LogFilter{
				AuthTokenID: &tokenOne,
				CostStatus:  status,
			})
		})
	}
	for _, basis := range basisFilters {
		t.Run("basis/"+basis, func(t *testing.T) {
			assertCostFilterMatchesModel(t, store, entries, now, &model.LogFilter{
				AuthTokenID: &tokenOne,
				CostBasis:   basis,
			})
		})
	}
	for _, source := range []string{"site_pricing", "local_estimate", "not-a-source"} {
		t.Run("source/"+source, func(t *testing.T) {
			assertCostFilterMatchesModel(t, store, entries, now, &model.LogFilter{
				AuthTokenID: &tokenOne,
				CostSource:  source,
			})
		})
	}
}

func TestCostSummaryExcludesNegativeAndNonFiniteRecordedAmounts(t *testing.T) {
	store := newTestStore(t, "cost_summary_invalid_amounts.db")
	ctx := context.Background()
	channelID := createTestChannel(t, ctx, store, "cost-invalid-amounts")
	now := time.Now()
	if err := store.BatchAddLogs(ctx, []*model.LogEntry{
		costLog(now, channelID, 30, 200, "negative", 1, "invalid", "unknown", "local_estimate", -1, 1),
		costLog(now, channelID, 31, 200, "finite", 1, "estimated", "token", "local_estimate", 0.25, 2),
	}); err != nil {
		t.Fatalf("BatchAddLogs: %v", err)
	}

	// Exercise the SQL guard directly with non-finite and legacy values. SQLite
	// accepts IEEE infinity even though normal writers reject it.
	executor, ok := store.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	})
	if !ok {
		t.Fatalf("store %T cannot execute fixture SQL", store)
	}
	const insertLog = `INSERT INTO logs(time, model, log_source, channel_id, status_code, message, cost, cost_multiplier, cost_source, cost_metadata_version, cost_status, cost_basis)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	fixtures := []struct {
		message        string
		cost           float64
		costMultiplier float64
	}{
		{message: "infinite-cost", cost: math.Inf(1), costMultiplier: 1},
		{message: "infinite-multiplier", cost: 0.75, costMultiplier: math.Inf(1)},
		{message: "negative-multiplier", cost: 0.5, costMultiplier: -3},
	}
	for _, fixture := range fixtures {
		if _, err := executor.ExecContext(ctx, insertLog,
			now.UnixMilli(), "gpt-cost", model.LogSourceProxy, channelID, 200, fixture.message,
			fixture.cost, fixture.costMultiplier, "local_estimate", 1, "estimated", "token",
		); err != nil {
			t.Fatalf("insert %s fixture: %v", fixture.message, err)
		}
	}

	summary, err := store.GetCostSummary(ctx, now.Add(-time.Minute), now.Add(time.Minute), nil)
	if err != nil {
		t.Fatalf("GetCostSummary: %v", err)
	}
	if summary.SuccessfulRequests != 5 {
		t.Fatalf("successful_requests=%d, want 5", summary.SuccessfulRequests)
	}
	if summary.EstimatedRequests != 2 {
		t.Fatalf("estimated_requests=%d, want 2", summary.EstimatedRequests)
	}
	if summary.UnknownRequests != 3 {
		t.Fatalf("unknown_requests=%d, want 3 invalid successes", summary.UnknownRequests)
	}
	if math.Abs(summary.RecordedCostUSD-1.0) > 1e-9 {
		t.Fatalf("recorded_cost_usd=%v, want 1.0", summary.RecordedCostUSD)
	}
	if math.IsNaN(summary.RecordedCostUSD) || math.IsInf(summary.RecordedCostUSD, 0) {
		t.Fatalf("recorded cost is non-finite: %v", summary.RecordedCostUSD)
	}
}

func TestInvalidCostRowsAreExcludedFromAllMonetaryAggregatesButKeepUsage(t *testing.T) {
	store := newTestStore(t, "cost_invalid_aggregate_paths.db")
	ctx := context.Background()
	channelID := createTestChannel(t, ctx, store, "invalid-cost-aggregates")
	authTokenID := int64(9_001)
	now := time.Now().Truncate(time.Minute).Add(10 * time.Second)
	start, end := now.Add(-time.Minute), now.Add(time.Minute)

	entries := []*model.LogEntry{
		costLogWithToken(now, channelID, authTokenID, 40, 200, "valid", 1, model.CostStatusEstimated, model.CostBasisToken, model.CostSourceLocal, 0.1, 2),
		costLogWithToken(now, channelID, authTokenID, 41, 200, "explicit-invalid", 1, model.CostStatusInvalid, model.CostBasisUnknown, model.CostSourceLocal, 0.25, 2),
		costLogWithToken(now, channelID, authTokenID, 42, 200, "negative-invalid", 1, model.CostStatusEstimated, model.CostBasisToken, model.CostSourceLocal, -0.4, 1),
	}
	for _, entry := range entries {
		entry.ClientProtocol = "openai"
		entry.InputTokens = 10
		entry.OutputTokens = 5
		entry.CacheReadInputTokens = 3
		entry.CacheCreationInputTokens = 2
	}
	if err := store.BatchAddLogs(ctx, entries); err != nil {
		t.Fatalf("BatchAddLogs: %v", err)
	}
	executor, ok := store.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	})
	if !ok {
		t.Fatalf("store %T cannot execute fixture SQL", store)
	}
	if _, err := executor.ExecContext(ctx, `
		INSERT INTO logs(
			time, minute_bucket, model, log_source, channel_id, status_code, message, auth_token_id,
			client_protocol, input_tokens, output_tokens,
			cache_read_input_tokens, cache_creation_input_tokens,
			cost, cost_multiplier, cost_source, cost_metadata_version, cost_status, cost_basis
		) VALUES(?, ?, 'gpt-cost', 'proxy', ?, 200, 'nonfinite-invalid', ?,
			'openai', 10, 5, 3, 2, ?, 1, 'local_estimate', 1, 'estimated', 'token')
	`, now.UnixMilli(), now.UnixMilli()/60_000, channelID, authTokenID, math.Inf(1)); err != nil {
		t.Fatalf("insert nonfinite invalid cost: %v", err)
	}

	filter := &model.LogFilter{
		AuthTokenID: &authTokenID,
		LogSource:   model.LogSourceProxy,
	}
	assertCosts := func(name string, total, effective float64) {
		t.Helper()
		if math.Abs(total-0.1) > 1e-9 || math.Abs(effective-0.2) > 1e-9 {
			t.Fatalf("%s costs=(%.12f,%.12f), want (0.1,0.2) from the valid row only", name, total, effective)
		}
	}
	costValue := func(name string, value *float64) float64 {
		t.Helper()
		if value == nil {
			t.Fatalf("%s cost is nil, want 0", name)
		}
		return *value
	}

	stats, err := store.GetStats(ctx, start, end, filter, false)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("GetStats rows=%d, want 1", len(stats))
	}
	if stats[0].Success != 4 || stats[0].Total != 4 ||
		stats[0].TotalInputTokens == nil || *stats[0].TotalInputTokens != 40 ||
		stats[0].TotalOutputTokens == nil || *stats[0].TotalOutputTokens != 20 {
		t.Fatalf("GetStats discarded valid usage: %+v", stats[0])
	}
	assertCosts("GetStats", costValue("total", stats[0].TotalCost), costValue("effective", stats[0].EffectiveCost))

	lite, err := store.GetStatsLite(ctx, start, end, filter)
	if err != nil {
		t.Fatalf("GetStatsLite: %v", err)
	}
	if len(lite) != 1 || lite[0].Success != 4 ||
		lite[0].TotalInputTokens == nil || *lite[0].TotalInputTokens != 40 {
		t.Fatalf("GetStatsLite rows=%+v, want one row with four successes and usage", lite)
	}
	assertCosts("GetStatsLite", costValue("total", lite[0].TotalCost), costValue("effective", lite[0].EffectiveCost))

	points, err := store.AggregateRangeWithFilter(ctx, start, end, time.Minute, filter)
	if err != nil {
		t.Fatalf("AggregateRangeWithFilter: %v", err)
	}
	var metricSuccess int
	var metricTotal, metricEffective float64
	for _, point := range points {
		for _, channel := range point.Channels {
			metricSuccess += channel.Success
			metricTotal += costValue("metric total", channel.TotalCost)
			metricEffective += costValue("metric effective", channel.EffectiveCost)
		}
	}
	if metricSuccess != 4 {
		t.Fatalf("metric successes=%d, want 4", metricSuccess)
	}
	assertCosts("AggregateRangeWithFilter", metricTotal, metricEffective)

	clients, err := store.GetClientProtocolStats(ctx, start, end, filter)
	if err != nil {
		t.Fatalf("GetClientProtocolStats: %v", err)
	}
	if len(clients) != 1 || clients[0].SuccessRequests != 4 ||
		clients[0].TotalInputTokens != 40 || clients[0].TotalOutputTokens != 20 {
		t.Fatalf("client protocol stats discarded usage: %+v", clients)
	}
	assertCosts("GetClientProtocolStats", clients[0].TotalCost, clients[0].EffectiveCost)

	timeline, err := store.GetHealthTimeline(ctx, model.HealthTimelineParams{
		SinceMs:  start.UnixMilli(),
		UntilMs:  end.UnixMilli(),
		BucketMs: time.Minute.Milliseconds(),
		Filter:   filter,
	})
	if err != nil {
		t.Fatalf("GetHealthTimeline: %v", err)
	}
	if len(timeline) != 1 || timeline[0].Success != 4 ||
		timeline[0].InputTokens != 40 || timeline[0].OutputTokens != 20 {
		t.Fatalf("health timeline discarded usage: %+v", timeline)
	}
	assertCosts("GetHealthTimeline", timeline[0].TotalCost, timeline[0].EffectiveCost)

	tokenStats, err := store.GetAuthTokenStatsInRange(ctx, start, end)
	if err != nil {
		t.Fatalf("GetAuthTokenStatsInRange: %v", err)
	}
	tokenStat := tokenStats[authTokenID]
	if tokenStat == nil || tokenStat.SuccessCount != 4 ||
		tokenStat.PromptTokens != 40 || tokenStat.CompletionTokens != 20 {
		t.Fatalf("token range stats discarded usage: %+v", tokenStat)
	}
	assertCosts("GetAuthTokenStatsInRange", tokenStat.TotalCost, tokenStat.EffectiveCost)

	dailyCosts, err := store.GetTodayChannelCosts(ctx, start)
	if err != nil {
		t.Fatalf("GetTodayChannelCosts: %v", err)
	}
	if math.Abs(dailyCosts[channelID]-0.2) > 1e-9 {
		t.Fatalf("GetTodayChannelCosts effective=%v, want 0.2 from the valid row only", dailyCosts[channelID])
	}

	summary, err := store.GetCostSummary(ctx, start, end, filter)
	if err != nil {
		t.Fatalf("GetCostSummary: %v", err)
	}
	if summary.SuccessfulRequests != 4 || summary.UnknownRequests != 3 {
		t.Fatalf("summary counts=%+v, want four successes and three invalid rows", summary)
	}
	if math.Abs(summary.RecordedCostUSD-0.2) > 1e-9 {
		t.Fatalf("GetCostSummary recorded=%v, want 0.2", summary.RecordedCostUSD)
	}
}

func TestCostSummaryReturnsErrorWhenAggregateAmountOverflows(t *testing.T) {
	store := newTestStore(t, "cost_summary_overflow.db")
	ctx := context.Background()
	channelID := createTestChannel(t, ctx, store, "cost-summary-overflow")
	now := time.Now()
	executor, ok := store.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	})
	if !ok {
		t.Fatalf("store %T cannot execute fixture SQL", store)
	}
	for _, message := range []string{"overflow-one", "overflow-two"} {
		if _, err := executor.ExecContext(ctx, `
			INSERT INTO logs(
				time, model, log_source, channel_id, status_code, message,
				cost, cost_multiplier, cost_source, cost_metadata_version, cost_status, cost_basis
			) VALUES(?, 'gpt-cost', 'proxy', ?, 200, ?, ?, 1, 'local_estimate', 1, 'estimated', 'token')
		`, now.UnixMilli(), channelID, message, math.MaxFloat64); err != nil {
			t.Fatalf("insert %s: %v", message, err)
		}
	}

	summary, err := store.GetCostSummary(ctx, now.Add(-time.Minute), now.Add(time.Minute), nil)
	if err == nil {
		t.Fatalf("GetCostSummary silently accepted nonfinite aggregate: %+v", summary)
	}
	if summary != nil {
		t.Fatalf("GetCostSummary returned a partial summary with error: %+v", summary)
	}
	if !strings.Contains(err.Error(), "recorded_cost_usd") || !strings.Contains(err.Error(), "non-finite") {
		t.Fatalf("overflow error=%q, want explicit recorded_cost_usd non-finite error", err)
	}
}

func assertCostFilterMatchesModel(t *testing.T, store interface {
	ListLogsRange(context.Context, time.Time, time.Time, int, int, *model.LogFilter) ([]*model.LogEntry, error)
	ListLogsRangeWithCount(context.Context, time.Time, time.Time, int, int, *model.LogFilter) ([]*model.LogEntry, int, error)
	CountLogsRange(context.Context, time.Time, time.Time, *model.LogFilter) (int, error)
	GetStats(context.Context, time.Time, time.Time, *model.LogFilter, bool) ([]model.StatsEntry, error)
	GetStatsLite(context.Context, time.Time, time.Time, *model.LogFilter) ([]model.StatsEntry, error)
	AggregateRangeWithFilter(context.Context, time.Time, time.Time, time.Duration, *model.LogFilter) ([]model.MetricPoint, error)
	GetCostSummary(context.Context, time.Time, time.Time, *model.LogFilter) (*model.CostSummary, error)
}, entries []*model.LogEntry, now time.Time, filter *model.LogFilter) {
	t.Helper()
	ctx := context.Background()
	start, end := now.Add(-time.Minute), now.Add(time.Minute)
	want := 0
	wantSuccessful := int64(0)
	for _, entry := range entries {
		if !costEntryMatchesFilter(entry, filter) {
			continue
		}
		want++
		if entry.StatusCode >= 200 && entry.StatusCode < 300 {
			wantSuccessful++
		}
	}

	logs, total, err := store.ListLogsRangeWithCount(ctx, start, end, 100, 0, filter)
	if err != nil {
		t.Fatalf("ListLogsRangeWithCount: %v", err)
	}
	if len(logs) != want || total != want {
		t.Fatalf("list len=%d total=%d, want %d", len(logs), total, want)
	}
	count, err := store.CountLogsRange(ctx, start, end, filter)
	if err != nil {
		t.Fatalf("CountLogsRange: %v", err)
	}
	if count != want {
		t.Fatalf("count=%d, want %d", count, want)
	}

	fullStats, err := store.GetStats(ctx, start, end, filter, false)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	fullStatsTotal := 0
	for _, entry := range fullStats {
		fullStatsTotal += entry.Total
	}
	if fullStatsTotal != want {
		t.Fatalf("full stats total=%d, want %d", fullStatsTotal, want)
	}

	stats, err := store.GetStatsLite(ctx, start, end, filter)
	if err != nil {
		t.Fatalf("GetStatsLite: %v", err)
	}
	statsTotal := 0
	for _, entry := range stats {
		statsTotal += entry.Total
	}
	if statsTotal != want {
		t.Fatalf("stats total=%d, want %d", statsTotal, want)
	}

	points, err := store.AggregateRangeWithFilter(ctx, start, end, time.Minute, filter)
	if err != nil {
		t.Fatalf("AggregateRangeWithFilter: %v", err)
	}
	trendTotal := 0
	for _, point := range points {
		for _, channel := range point.Channels {
			trendTotal += channel.Success + channel.Error
		}
	}
	if trendTotal != want {
		t.Fatalf("trend total=%d, want %d", trendTotal, want)
	}

	summary, err := store.GetCostSummary(ctx, start, end, filter)
	if err != nil {
		t.Fatalf("GetCostSummary: %v", err)
	}
	if summary.SuccessfulRequests != wantSuccessful {
		t.Fatalf("summary successful=%d, want %d", summary.SuccessfulRequests, wantSuccessful)
	}
}

func costEntryMatchesFilter(entry *model.LogEntry, filter *model.LogFilter) bool {
	if entry == nil || entry.ChannelID <= 0 {
		return false
	}
	if entry.LogSource != model.LogSourceProxy {
		return false
	}
	if filter != nil {
		if filter.AuthTokenID != nil && entry.AuthTokenID != *filter.AuthTokenID {
			return false
		}
		if filter.CostSource != "" && entry.CostSource != filter.CostSource {
			return false
		}
		if filter.CostStatus != "" && model.LogCostStatus(entry) != filter.CostStatus {
			return false
		}
		if filter.CostBasis != "" && model.LogCostBasis(entry) != filter.CostBasis {
			return false
		}
	}
	return true
}

func costLog(
	now time.Time,
	channelID int64,
	index int,
	status int,
	message string,
	version int,
	costStatus, costBasis, costSource string,
	cost, multiplier float64,
) *model.LogEntry {
	return costLogWithToken(now, channelID, int64(index), index, status, message, version, costStatus, costBasis, costSource, cost, multiplier)
}

func costLogWithToken(
	now time.Time,
	channelID, tokenID int64,
	index int,
	status int,
	message string,
	version int,
	costStatus, costBasis, costSource string,
	cost, multiplier float64,
) *model.LogEntry {
	return &model.LogEntry{
		Time:                model.JSONTime{Time: now.Add(time.Duration(index) * time.Millisecond)},
		Model:               "gpt-cost",
		LogSource:           model.LogSourceProxy,
		ChannelID:           channelID,
		StatusCode:          status,
		Message:             message,
		RequestID:           "cost-" + message,
		AuthTokenID:         tokenID,
		CostMetadataVersion: version,
		CostStatus:          costStatus,
		CostBasis:           costBasis,
		CostSource:          costSource,
		Cost:                cost,
		CostMultiplier:      multiplier,
		CostDetailJSON:      `{"model_cost_usd":0,"tool_cost_usd":0,"currency":"USD"}`,
	}
}
