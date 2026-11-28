package app

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

type intervalHistoryStore struct {
	storage.Store
	legacy, samples []*model.SiteAccountDailyBalance
	sampleErr       error
}

func (s *intervalHistoryStore) ListSiteAccountDailyBalances(context.Context, string, string) ([]*model.SiteAccountDailyBalance, error) {
	return s.legacy, nil
}

func (s *intervalHistoryStore) ListSiteAccountBalanceSamples(context.Context, string, string) ([]*model.SiteAccountDailyBalance, error) {
	return s.samples, s.sampleErr
}

func intervalObservation(day string, hour int, account int64, currency string, amount float64) *model.SiteAccountDailyBalance {
	at, _ := time.ParseInLocation("2006-01-02", day, dailyBalanceLocation())
	at = at.Add(time.Duration(hour)*time.Hour + 5*time.Minute)
	return &model.SiteAccountDailyBalance{SiteAccountID: account, LocalDay: day, Currency: currency,
		Balance: &amount, Status: "success", ScheduledAt: at.UnixMilli(), CollectedAt: at.Add(time.Second).UnixMilli()}
}

func intervalHistory(t *testing.T, rows ...*model.SiteAccountDailyBalance) []model.SiteBalanceHistoryPoint {
	t.Helper()
	server := &Server{store: &intervalHistoryStore{samples: rows}}
	end := time.Date(2026, 11, 28, 23, 0, 0, 0, dailyBalanceLocation())
	return server.siteBalanceHistory(context.Background(), end.Add(-time.Hour), end, "today")
}

func TestIntervalBalanceShowsSameDayIncreaseBeforeTomorrow(t *testing.T) {
	rows := []*model.SiteAccountDailyBalance{intervalObservation("2026-11-28", 8, 1, "USD", 100)}
	first := intervalHistory(t, rows...)
	if len(first) != 1 || first[0].DailyDelta != nil || first[0].ComparisonStatus != "baseline" {
		t.Fatalf("first observation must only establish a baseline: %+v", first)
	}
	rows = append(rows, intervalObservation("2026-11-28", 9, 1, "USD", 105))
	history := intervalHistory(t, rows...)
	if len(history) != 1 || history[0].DailyDelta == nil || *history[0].DailyDelta != 5 || history[0].ComparisonStatus != "partial" {
		t.Fatalf("same-day signup gain must appear without waiting for tomorrow: %+v", history)
	}
	encoded, err := json.Marshal(history[0])
	if err != nil || !strings.Contains(string(encoded), `"change_basis":"first_sample"`) {
		t.Fatalf("partial observation must disclose its basis: %s, %v", encoded, err)
	}
	rows = append(rows, intervalObservation("2026-11-28", 10, 1, "USD", 102))
	history = intervalHistory(t, rows...)
	if len(history) != 1 || history[0].DailyDelta == nil || *history[0].DailyDelta != 2 {
		t.Fatalf("later consumption must update the same daily point: %+v", history)
	}
}

func TestIntervalBalanceUsesPreviousDayLatestAndPerAccountDeltas(t *testing.T) {
	history := intervalHistory(t,
		intervalObservation("2026-11-27", 8, 1, "USD", 1e16), intervalObservation("2026-11-27", 23, 1, "USD", 1e16),
		intervalObservation("2026-11-27", 8, 2, "USD", 0), intervalObservation("2026-11-27", 23, 2, "USD", 10),
		intervalObservation("2026-11-28", 8, 1, "USD", 1e16), intervalObservation("2026-11-28", 9, 1, "USD", 1e16),
		intervalObservation("2026-11-28", 8, 2, "USD", 8), intervalObservation("2026-11-28", 9, 2, "USD", 11))
	if len(history) != 2 || history[1].DailyDelta == nil || *history[1].DailyDelta != 1 || history[1].ComparisonStatus != "complete" {
		t.Fatalf("must compare previous day's final observation, preserving small changes: %+v", history)
	}
}

func TestIntervalBalanceTrueZeroAndNegativeChangesRemainComparable(t *testing.T) {
	for _, item := range []struct {
		name   string
		latest float64
		want   float64
	}{
		{"unchanged", 100, 0}, {"consumption", 96.25, -3.75},
	} {
		t.Run(item.name, func(t *testing.T) {
			history := intervalHistory(t, intervalObservation("2026-11-28", 8, 1, "USD", 100), intervalObservation("2026-11-28", 9, 1, "USD", item.latest))
			if len(history) != 1 || history[0].ComparisonStatus != "partial" || history[0].ChangeBasis != "first_sample" || history[0].DailyDelta == nil || *history[0].DailyDelta != item.want {
				t.Fatalf("valid same-day comparison was lost: %+v", history)
			}
		})
	}
}

func TestIntervalBalanceDoesNotInventZeroAcrossFailuresCohortsOrCurrencies(t *testing.T) {
	base := intervalObservation("2026-11-28", 8, 1, "USD", 100)
	failed := intervalObservation("2026-11-28", 9, 1, "USD", 0)
	failed.Status, failed.Balance = "failed", nil
	for name, rows := range map[string][]*model.SiteAccountDailyBalance{
		"failed_latest":   {base, failed},
		"new_account":     {base, intervalObservation("2026-11-28", 9, 1, "USD", 105), intervalObservation("2026-11-28", 9, 2, "USD", 200)},
		"currency_change": {base, intervalObservation("2026-11-28", 9, 1, "CNY", 700)},
		"overflow":        {intervalObservation("2026-11-28", 8, 1, "USD", -math.MaxFloat64), intervalObservation("2026-11-28", 9, 1, "USD", math.MaxFloat64)},
	} {
		t.Run(name, func(t *testing.T) {
			history := intervalHistory(t, rows...)
			if len(history) == 0 {
				t.Fatal("observed but noncomparable days must remain represented")
			}
			for _, point := range history {
				if point.DailyDelta != nil {
					t.Fatalf("invented a difference: %+v", point)
				}
			}
		})
	}
}

func TestIntervalBalanceLatestUnsupportedCannotRestoreEarlierSuccess(t *testing.T) {
	first := intervalObservation("2026-11-28", 8, 1, "USD", 100)
	second := intervalObservation("2026-11-28", 9, 1, "USD", 105)
	unsupported := intervalObservation("2026-11-28", 10, 1, "USD", 0)
	unsupported.Status, unsupported.ErrorCode, unsupported.Balance = "failed", provider.CodeUnsupported, nil
	history := intervalHistory(t, first, second, unsupported)
	if len(history) != 1 || history[0].ComparisonStatus != "incomplete" || history[0].DailyDelta != nil || history[0].UpdatedAt != unsupported.CollectedAt {
		t.Fatalf("latest unsupported failure restored an earlier success: %+v", history)
	}
	previous := intervalObservation("2026-11-27", 23, 1, "USD", 99)
	history = intervalHistory(t, previous, unsupported)
	if len(history) != 2 || history[1].ComparisonStatus != "incomplete" || history[1].DailyDelta != nil {
		t.Fatalf("unsupported first read today removed a previously participating account: %+v", history)
	}
	history = intervalHistory(t, first, second, unsupported, intervalObservation("2026-11-28", 11, 1, "USD", 106))
	if len(history) != 1 || history[0].ComparisonStatus != "partial" || history[0].DailyDelta == nil || *history[0].DailyDelta != 6 {
		t.Fatalf("a later successful observation did not restore valid comparison: %+v", history)
	}
}

func TestIntervalBalanceNeverSupportedAccountsDoNotBlockSupportedBalances(t *testing.T) {
	unsupported := intervalObservation("2026-11-28", 8, 2, "USD", 0)
	unsupported.Status, unsupported.ErrorCode, unsupported.Balance = "failed", provider.CodeUnsupported, nil
	history := intervalHistory(t, intervalObservation("2026-11-28", 8, 1, "USD", 100), intervalObservation("2026-11-28", 9, 1, "USD", 105), unsupported)
	if len(history) != 1 || history[0].Accounts != 1 || history[0].ComparisonStatus != "partial" || history[0].DailyDelta == nil || *history[0].DailyDelta != 5 {
		t.Fatalf("a balance-unsupported account blocked supported accounts: %+v", history)
	}
}

func TestIntervalBalanceReadFailureCannotRestoreConflictingLegacyDelta(t *testing.T) {
	store := &intervalHistoryStore{
		legacy:  dailyHistoryRows(historyBalance("2026-11-27", 1, "USD", 90), historyBalance("2026-11-28", 1, "USD", 100)),
		samples: []*model.SiteAccountDailyBalance{intervalObservation("2026-11-28", 8, 1, "USD", 105), intervalObservation("2026-11-28", 9, 1, "USD", 107)},
	}
	server := &Server{store: store}
	end := time.Date(2026, 11, 28, 12, 0, 0, 0, dailyBalanceLocation())
	history := server.siteBalanceHistory(context.Background(), end.Add(-time.Hour), end, "today")
	if len(history) != 2 || history[1].DailyDelta == nil || *history[1].DailyDelta != 2 {
		t.Fatalf("hourly records must supersede conflicting legacy delta: %+v", history)
	}
	store.sampleErr = errors.New("synthetic interval read failure")
	if history = server.siteBalanceHistory(context.Background(), end.Add(-time.Hour), end, "today"); len(history) != 0 {
		t.Fatalf("unavailable interval precedence restored a conflicting legacy result: %+v", history)
	}
}

func TestIntervalBalanceKeepsLegacyDaysAndDoesNotGuessNewValuesOnReadError(t *testing.T) {
	store := &intervalHistoryStore{legacy: dailyHistoryRows(historyBalance("2026-11-26", 1, "USD", 90), historyBalance("2026-11-27", 1, "USD", 100)),
		samples: []*model.SiteAccountDailyBalance{intervalObservation("2026-11-28", 8, 1, "USD", 105), intervalObservation("2026-11-28", 9, 1, "USD", 107)}}
	server := &Server{store: store}
	end := time.Date(2026, 11, 28, 12, 0, 0, 0, dailyBalanceLocation())
	history := server.siteBalanceHistory(context.Background(), end.Add(-time.Hour), end, "today")
	if len(history) != 3 || history[1].DailyDelta == nil || *history[1].DailyDelta != 10 || history[2].DailyDelta == nil || *history[2].DailyDelta != 2 {
		t.Fatalf("migration must preserve earlier days without treating old opening samples as new closing balances: %+v", history)
	}
	store.sampleErr = errors.New("synthetic interval read failure")
	history = server.siteBalanceHistory(context.Background(), end.Add(-time.Hour), end, "today")
	for _, point := range history {
		if point.Day == "2026-11-28" && point.DailyDelta != nil {
			t.Fatalf("read error fabricated today's difference: %+v", point)
		}
	}
}

func TestIntervalBalanceSamplingSettingValidation(t *testing.T) {
	for _, value := range []string{"15", "30", "60", "120", "360", "720", "1440"} {
		if err := validateSettingValue("site_balance_sample_interval_minutes", "int", value); err != nil {
			t.Fatalf("valid interval %q: %v", value, err)
		}
	}
	for _, value := range []string{"0", "1", "14", "16", "59", "61", "1441", "-1", "NaN"} {
		if err := validateSettingValue("site_balance_sample_interval_minutes", "int", value); err == nil {
			t.Fatalf("invalid interval %q was accepted", value)
		}
	}
}
