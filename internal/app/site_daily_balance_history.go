package app

import (
	"context"
	"log"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func (s *Server) siteBalanceHistory(ctx context.Context, startTime, endTime time.Time, rangeName string) []model.SiteBalanceHistoryPoint {
	location := dailyBalanceLocation()
	end := endTime.In(location)
	endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, location)
	visibleStart := startTime.In(location)
	// Daily observations remain useful on the default “今日” request view.
	// Show the last fourteen calendar days; other ranges still use their start.
	if rangeName == "today" || rangeName == "" || visibleStart.Before(endDay.AddDate(0, 0, -13)) {
		visibleStart = endDay.AddDate(0, 0, -13)
	}
	samples, err := s.store.ListSiteAccountDailyBalances(ctx, visibleStart.AddDate(0, 0, -1).Format("2006-01-02"), endDay.Format("2006-01-02"))
	if err != nil {
		log.Printf("[WARN] load fixed daily balance history failed")
		return nil
	}
	return aggregateDailyBalanceHistory(samples, visibleStart.Format("2006-01-02"))
}

type dailyBalanceGroup struct {
	point    model.SiteBalanceHistoryPoint
	accounts map[int64]*model.SiteAccountDailyBalance
}

func finiteBalance(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

// Scaling before summation avoids overflow from intermediate addition. Changes
// are summed per account so an unchanged large balance cannot hide a small one.
func finiteBalanceSum(values []float64) *float64 {
	scale := 0.0
	for _, value := range values {
		if !finiteBalance(value) {
			return nil
		}
		scale = math.Max(scale, math.Abs(value))
	}
	if scale == 0 {
		zero := 0.0
		return &zero
	}
	sum, correction := 0.0, 0.0
	for _, value := range values {
		value /= scale
		next := sum + value
		if math.Abs(sum) >= math.Abs(value) {
			correction += (sum - next) + value
		} else {
			correction += (value - next) + sum
		}
		sum = next
	}
	result := (sum + correction) * scale
	if !finiteBalance(result) {
		return nil
	}
	return &result
}

func aggregateDailyBalanceHistory(samples []*model.SiteAccountDailyBalance, visibleStart string) []model.SiteBalanceHistoryPoint {
	groups := make(map[string]*dailyBalanceGroup)
	for _, sample := range samples {
		if sample == nil || sample.SiteAccountID <= 0 || sample.Status == "failed" && sample.ErrorCode == provider.CodeUnsupported {
			continue
		}
		day, err := time.Parse("2006-01-02", sample.LocalDay)
		if err != nil || day.Format("2006-01-02") != sample.LocalDay {
			continue
		}
		currency := strings.ToUpper(strings.TrimSpace(sample.Currency))
		key := sample.LocalDay + "\x00" + currency
		group := groups[key]
		if group == nil {
			group = &dailyBalanceGroup{point: model.SiteBalanceHistoryPoint{Day: sample.LocalDay, Currency: currency}, accounts: make(map[int64]*model.SiteAccountDailyBalance)}
			groups[key] = group
		}
		group.accounts[sample.SiteAccountID] = sample
		group.point.ScheduledAt = max(group.point.ScheduledAt, sample.ScheduledAt)
		group.point.UpdatedAt = max(group.point.UpdatedAt, sample.CollectedAt)
	}
	for _, group := range groups {
		group.point.Accounts = len(group.accounts)
		values := make([]float64, 0, len(group.accounts))
		ids := make([]int64, 0, len(group.accounts))
		for id := range group.accounts {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			sample := group.accounts[id]
			if sample.Status == "success" && sample.Balance != nil && finiteBalance(*sample.Balance) {
				group.point.KnownAccounts++
				values = append(values, *sample.Balance)
			}
		}
		if group.point.KnownAccounts == group.point.Accounts {
			group.point.Balance = finiteBalanceSum(values)
		}
	}
	result := make([]model.SiteBalanceHistoryPoint, 0, len(groups))
	for _, group := range groups {
		if group.point.Day < visibleStart {
			continue
		}
		point := group.point
		point.ComparisonStatus = "baseline"
		day, _ := time.Parse("2006-01-02", point.Day)
		previous := groups[day.AddDate(0, 0, -1).Format("2006-01-02")+"\x00"+point.Currency]
		switch {
		case point.KnownAccounts != point.Accounts:
			point.ComparisonStatus = "incomplete"
		case point.Balance == nil:
			point.ComparisonStatus = "overflow"
		case previous == nil:
			for _, earlier := range groups {
				if earlier.point.Currency == point.Currency && earlier.point.Day < point.Day {
					point.ComparisonStatus = "missing_previous"
					break
				}
			}
		case previous.point.KnownAccounts != previous.point.Accounts:
			point.ComparisonStatus = "incomplete"
		case previous.point.Balance == nil:
			point.ComparisonStatus = "overflow"
		default:
			point.ComparisonStatus = "complete"
			changes := make([]float64, 0, len(group.accounts))
			ids := make([]int64, 0, len(group.accounts))
			for id := range group.accounts {
				ids = append(ids, id)
			}
			slices.Sort(ids)
			if len(previous.accounts) != len(group.accounts) {
				point.ComparisonStatus = "coverage_changed"
			} else {
				for _, id := range ids {
					before := previous.accounts[id]
					if before == nil {
						point.ComparisonStatus = "coverage_changed"
						break
					}
					changes = append(changes, *group.accounts[id].Balance-*before.Balance)
				}
			}
			if point.ComparisonStatus == "complete" {
				point.DailyDelta = finiteBalanceSum(changes)
				if point.DailyDelta == nil {
					point.ComparisonStatus = "overflow"
				}
			}
		}
		result = append(result, point)
	}
	slices.SortFunc(result, func(a, b model.SiteBalanceHistoryPoint) int {
		if a.Day != b.Day {
			return strings.Compare(a.Day, b.Day)
		}
		return strings.Compare(a.Currency, b.Currency)
	})
	return result
}
