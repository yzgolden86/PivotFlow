package app

import (
	"math"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestDailyBalanceHistoryUsesSignedChangesAndPreviousDayBaseline(t *testing.T) {
	rows := dailyHistoryRows(
		historyBalance("2026-11-20", 1, "USD", 10),
		historyBalance("2026-11-21", 1, "USD", 13),
		historyBalance("2026-11-22", 1, "USD", 8),
		historyBalance("2026-11-23", 1, "USD", 8),
		historyBalance("2026-11-21", 2, "CNY", 30),
		historyBalance("2026-11-22", 2, "CNY", 31),
	)
	history := aggregateDailyBalanceHistory(rows, "2026-11-21")
	if len(history) != 5 {
		t.Fatalf("history=%+v", history)
	}
	var usdChanges []float64
	for _, point := range history {
		if point.Currency == "USD" {
			if point.DailyDelta == nil || point.ComparisonStatus != "complete" {
				t.Fatalf("lost previous-day baseline: %+v", point)
			}
			usdChanges = append(usdChanges, *point.DailyDelta)
		}
	}
	if len(usdChanges) != 3 || usdChanges[0] != 3 || usdChanges[1] != -5 || usdChanges[2] != 0 {
		t.Fatalf("signed changes=%v", usdChanges)
	}
}

func TestDailyBalanceHistorySameCountDifferentAccountsIsNotAnIncrease(t *testing.T) {
	rows := dailyHistoryRows(historyBalance("2026-11-23", 1, "USD", 10), historyBalance("2026-11-24", 2, "USD", 200))
	history := aggregateDailyBalanceHistory(rows, "2026-11-23")
	if len(history) != 2 || history[1].Accounts != 1 || history[1].DailyDelta != nil || history[1].ComparisonStatus != "coverage_changed" {
		t.Fatalf("different one-account cohorts compared: %+v", history)
	}
}

func TestDailyBalanceHistoryMissingOrFailedSamplesBreakComparisons(t *testing.T) {
	failed := historyBalance("2026-11-22", 1, "USD", 0)
	failed.Status, failed.Balance = "failed", nil
	rows := dailyHistoryRows(historyBalance("2026-11-21", 1, "USD", 10), failed, historyBalance("2026-11-23", 1, "USD", 8), historyBalance("2026-11-25", 1, "USD", 6))
	history := aggregateDailyBalanceHistory(rows, "2026-11-21")
	if len(history) != 4 {
		t.Fatalf("history=%+v", history)
	}
	for _, point := range history {
		if point.DailyDelta != nil {
			t.Fatalf("invented comparison across a baseline/failure/gap: %+v", point)
		}
	}
	if history[0].ComparisonStatus != "baseline" || history[1].Balance != nil || history[1].KnownAccounts != 0 || history[1].ComparisonStatus != "incomplete" || history[2].ComparisonStatus != "incomplete" || history[3].ComparisonStatus != "missing_previous" {
		t.Fatalf("lost honest unknown states: %+v", history)
	}
}

func TestDailyBalanceHistorySumsPerAccountChangesWithoutLosingSmallChanges(t *testing.T) {
	rows := dailyHistoryRows(historyBalance("2026-11-23", 1, "USD", 1e16), historyBalance("2026-11-24", 1, "USD", 1e16), historyBalance("2026-11-23", 2, "USD", 0), historyBalance("2026-11-24", 2, "USD", 1))
	history := aggregateDailyBalanceHistory(rows, "2026-11-23")
	if len(history) != 2 || history[1].DailyDelta == nil || *history[1].DailyDelta != 1 {
		t.Fatalf("a large unchanged account hid another account's +1: %+v", history)
	}
}

func TestDailyBalanceHistoryCannotEmitNonfiniteJSON(t *testing.T) {
	for _, rows := range [][]*model.SiteAccountDailyBalance{
		dailyHistoryRows(historyBalance("2026-11-23", 1, "USD", -math.MaxFloat64), historyBalance("2026-11-24", 1, "USD", math.MaxFloat64)),
		dailyHistoryRows(historyBalance("2026-11-24", 1, "USD", math.MaxFloat64), historyBalance("2026-11-24", 2, "USD", math.MaxFloat64)),
	} {
		history := aggregateDailyBalanceHistory(rows, "2026-11-23")
		for _, point := range history {
			if point.Balance != nil && (math.IsNaN(*point.Balance) || math.IsInf(*point.Balance, 0)) || point.DailyDelta != nil && (math.IsNaN(*point.DailyDelta) || math.IsInf(*point.DailyDelta, 0)) {
				t.Fatalf("nonfinite amount: %+v", point)
			}
		}
		if history[len(history)-1].DailyDelta != nil || history[len(history)-1].ComparisonStatus != "overflow" {
			t.Fatalf("overflow not made explicit: %+v", history)
		}
	}
}

func TestDailyBalanceHistoryUnsupportedCredentialsDoNotPoisonSupportedAccounts(t *testing.T) {
	rows := dailyHistoryRows(historyBalance("2026-11-23", 1, "USD", 10), historyBalance("2026-11-24", 1, "USD", 14))
	for _, day := range []string{"2026-11-23", "2026-11-24"} {
		unsupported := historyBalance(day, 2, "USD", 0)
		unsupported.Status, unsupported.Balance, unsupported.ErrorCode = "failed", nil, "unsupported"
		rows = append(rows, unsupported)
	}
	history := aggregateDailyBalanceHistory(rows, "2026-11-23")
	if len(history) != 2 || history[1].Accounts != 1 || history[1].DailyDelta == nil || *history[1].DailyDelta != 4 {
		t.Fatalf("API-key-only credentials prevented supported balance comparisons: %+v", history)
	}
	failed := historyBalance("2026-11-24", 3, "USD", 0)
	failed.Status, failed.Balance, failed.ErrorCode = "failed", nil, "provider_timeout"
	history = aggregateDailyBalanceHistory(append(rows, failed), "2026-11-23")
	if history[1].DailyDelta != nil || history[1].ComparisonStatus != "incomplete" {
		t.Fatalf("a real failure was incorrectly excluded: %+v", history[1])
	}
}

func historyBalance(day string, account int64, currency string, amount float64) *model.SiteAccountDailyBalance {
	return &model.SiteAccountDailyBalance{LocalDay: day, SiteAccountID: account, Currency: currency, Balance: &amount, Status: "success", ScheduledAt: 1000, CollectedAt: 2000}
}

func dailyHistoryRows(rows ...*model.SiteAccountDailyBalance) []*model.SiteAccountDailyBalance {
	return rows
}
