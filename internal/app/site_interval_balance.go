package app

import "time"

const defaultBalanceSampleInterval = 60

func validBalanceSampleInterval(minutes int) bool {
	switch minutes {
	case 15, 30, 60, 120, 360, 720, 1440:
		return true
	}
	return false
}

func (s *siteControlService) balanceSampleInterval() int {
	if s != nil && s.configService != nil {
		if value := s.configService.GetInt("site_balance_sample_interval_minutes", defaultBalanceSampleInterval); validBalanceSampleInterval(value) {
			return value
		}
	}
	return defaultBalanceSampleInterval
}

// Slots are aligned to the configured Beijing-time phase, not process uptime.
// Only the current slot can be collected; missed slots are never backfilled.
func intervalBalanceWindow(now time.Time, minute, interval int) (time.Time, bool) {
	now = now.In(dailyBalanceLocation())
	anchor := time.Date(now.Year(), now.Month(), now.Day(), minute/60, minute%60, 0, 0, now.Location())
	if now.Before(anchor) {
		anchor = anchor.AddDate(0, 0, -1)
	}
	period := time.Duration(interval) * time.Minute
	scheduled := anchor.Add(now.Sub(anchor) / period * period)
	// An old day's observation must not be populated from this day's balance.
	return scheduled, scheduled.Format("2006-01-02") == now.Format("2006-01-02")
}

func intervalBalanceCutoff(scheduled time.Time, interval int) time.Time {
	end := scheduled.Add(time.Duration(interval) * time.Minute)
	nextDay := time.Date(scheduled.Year(), scheduled.Month(), scheduled.Day()+1, 0, 0, 0, 0, scheduled.Location())
	if nextDay.Before(end) {
		return nextDay
	}
	return end
}
