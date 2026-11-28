package app

import (
	"slices"
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

type intervalAccountHistory struct {
	first, latest   *model.SiteAccountDailyBalance
	firstSlot       int64
	currencyChanged bool
}

func validIntervalBalance(sample *model.SiteAccountDailyBalance) bool {
	return sample != nil && sample.Status == "success" && sample.Balance != nil && finiteBalance(*sample.Balance) && strings.TrimSpace(sample.Currency) != ""
}

func intervalAccountDays(samples []*model.SiteAccountDailyBalance) map[string]map[int64]*intervalAccountHistory {
	days := make(map[string]map[int64]*intervalAccountHistory)
	supported := make(map[int64]bool)
	for _, sample := range samples {
		if validIntervalBalance(sample) {
			supported[sample.SiteAccountID] = true
		}
	}
	for _, sample := range samples {
		if sample == nil || sample.SiteAccountID <= 0 || sample.ScheduledAt <= 0 || sample.Status == "failed" && sample.ErrorCode == provider.CodeUnsupported && !supported[sample.SiteAccountID] {
			continue
		}
		day, err := time.Parse("2006-01-02", sample.LocalDay)
		if err != nil || day.Format("2006-01-02") != sample.LocalDay {
			continue
		}
		accounts := days[sample.LocalDay]
		if accounts == nil {
			accounts = make(map[int64]*intervalAccountHistory)
			days[sample.LocalDay] = accounts
		}
		account := accounts[sample.SiteAccountID]
		if account == nil {
			account = &intervalAccountHistory{firstSlot: sample.ScheduledAt}
			accounts[sample.SiteAccountID] = account
		}
		account.firstSlot = min(account.firstSlot, sample.ScheduledAt)
		if account.latest == nil || sample.ScheduledAt > account.latest.ScheduledAt || sample.ScheduledAt == account.latest.ScheduledAt && sample.CollectedAt > account.latest.CollectedAt {
			account.latest = sample
		}
		if validIntervalBalance(sample) {
			if account.first != nil && !strings.EqualFold(strings.TrimSpace(account.first.Currency), strings.TrimSpace(sample.Currency)) {
				account.currencyChanged = true
			}
			if account.first == nil || sample.ScheduledAt < account.first.ScheduledAt {
				account.first = sample
			}
		}
	}
	return days
}

func mergeIntervalBalanceHistory(legacy, samples []*model.SiteAccountDailyBalance, visibleStart string) []model.SiteBalanceHistoryPoint {
	days := intervalAccountDays(samples)
	latest := make([]*model.SiteAccountDailyBalance, 0)
	for _, accounts := range days {
		for _, account := range accounts {
			copy := *account.latest
			if copy.Status == "failed" && copy.ErrorCode == provider.CodeUnsupported {
				// This account previously supplied balances. Keep its latest
				// unavailability in the comparison instead of letting the legacy
				// aggregator's never-supported filter restore an older success.
				// Only this projection changes; the stored failure stays intact.
				copy.Status = "unknown"
			}
			copy.Currency = strings.ToUpper(strings.TrimSpace(copy.Currency))
			if copy.Currency == "" && account.first != nil {
				copy.Currency = strings.ToUpper(strings.TrimSpace(account.first.Currency))
			}
			latest = append(latest, &copy)
		}
	}
	points := aggregateDailyBalanceHistory(latest, visibleStart)
	for index := range points {
		point := &points[index]
		if point.ComparisonStatus == "incomplete" || point.ComparisonStatus == "overflow" {
			continue
		}
		day, _ := time.Parse("2006-01-02", point.Day)
		previous := days[day.AddDate(0, 0, -1).Format("2006-01-02")]
		accounts := days[point.Day]
		cohort := make([]*intervalAccountHistory, 0, point.Accounts)
		for id, account := range accounts {
			if !strings.EqualFold(strings.TrimSpace(account.latest.Currency), point.Currency) {
				continue
			}
			cohort = append(cohort, account)
			if account.currencyChanged || previous[id] != nil && !strings.EqualFold(strings.TrimSpace(previous[id].latest.Currency), point.Currency) {
				point.ComparisonStatus, point.DailyDelta = "coverage_changed", nil
			}
		}
		if point.ComparisonStatus == "coverage_changed" {
			continue
		}
		if point.ComparisonStatus == "complete" {
			point.ChangeBasis = "previous_day"
			continue
		}
		// No previous-day interval baseline: show only a measured first-to-latest
		// change, explicitly partial. Never borrow a legacy day's opening value.
		firstSlot := int64(0)
		for _, account := range cohort {
			if firstSlot == 0 || account.firstSlot < firstSlot {
				firstSlot = account.firstSlot
			}
		}
		changes := make([]float64, 0, len(cohort))
		point.ComparisonStatus, point.DailyDelta = "baseline", nil
		for _, account := range cohort {
			if account.firstSlot != firstSlot {
				point.ComparisonStatus = "coverage_changed"
				break
			}
			if account.first == nil || account.first.ScheduledAt != firstSlot {
				point.ComparisonStatus = "incomplete"
				break
			}
			if account.latest.ScheduledAt > account.first.ScheduledAt {
				changes = append(changes, *account.latest.Balance-*account.first.Balance)
			}
		}
		if point.ComparisonStatus == "baseline" && len(cohort) > 0 && len(changes) == len(cohort) {
			point.DailyDelta = finiteBalanceSum(changes)
			if point.DailyDelta == nil {
				point.ComparisonStatus = "overflow"
			} else {
				point.ComparisonStatus, point.ChangeBasis = "partial", "first_sample"
			}
		}
	}
	// Existing daily observations are immutable and retain their original meaning.
	// Only dates with new interval observations switch to the new calculation.
	for _, point := range aggregateDailyBalanceHistory(legacy, visibleStart) {
		if days[point.Day] == nil {
			points = append(points, point)
		}
	}
	slices.SortFunc(points, func(a, b model.SiteBalanceHistoryPoint) int {
		if a.Day != b.Day {
			return strings.Compare(a.Day, b.Day)
		}
		return strings.Compare(a.Currency, b.Currency)
	})
	return points
}
