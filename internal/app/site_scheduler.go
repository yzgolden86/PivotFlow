package app

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

const (
	siteSchedulerTick        = time.Minute
	siteRefreshInterval      = 6 * time.Hour
	siteTaskLeaseDuration    = 90 * time.Second
	siteSchedulerConcurrency = 4
	siteDailyCheckinTime     = "08:00"

	// A scheduled check-in that ended in a recoverable failure — a transient
	// upstream error, or an attempt that never finished — is re-checked through
	// the day instead of being written off. Without this the first attempt's
	// verdict stuck until tomorrow, which is exactly why an automatic check-in
	// could "fail" while a manual click minutes later succeeded. A permanently
	// blocked site stops after the cap instead of being hammered once a minute.
	siteCheckinRetryInterval = time.Hour
	siteCheckinRetryLimit    = 3

	// An interactive browser challenge is a different kind of failure. No number
	// of server-side retries can clear it — only a human can — so each retry is
	// another POST the site's own guard is built to reject. Repeating that hourly
	// for a whole day is exactly the pattern an abuse detector notices, and a
	// banned account costs far more than a delayed check-in. So a challenged day
	// gets exactly one automatic retry, placed late enough that an operator who
	// signs in during the day is still noticed; anything sooner is operator-driven
	// from the console's retry action. One attempt costs three requests (self,
	// checkin, status), so this keeps a Turnstile site at six a day.
	siteCheckinBrowserRetryInterval = 12 * time.Hour
	siteCheckinBrowserRetryLimit    = 2
)

func (s *siteControlService) dailyCheckinMinute() int {
	value := siteDailyCheckinTime
	if s != nil && s.configService != nil {
		value = s.configService.GetString("site_daily_checkin_time", siteDailyCheckinTime)
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		parsed, _ = time.Parse("15:04", siteDailyCheckinTime)
	}
	return parsed.Hour()*60 + parsed.Minute()
}

func (s *siteControlService) dailyAnnouncementMinute() int {
	value := "09:00"
	if s != nil && s.configService != nil {
		value = s.configService.GetString("site_daily_announcement_time", value)
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		parsed, _ = time.Parse("15:04", "09:00")
	}
	return parsed.Hour()*60 + parsed.Minute()
}

func (s *Server) startSiteScheduler() {
	if s.siteControl == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(siteSchedulerTick)
		defer ticker.Stop()
		// Startup catch-up is delayed one minute by design.
		for {
			select {
			case <-s.shutdownCh:
				return
			case now := <-ticker.C:
				s.siteControl.runSchedule(s.baseCtx, now)
			}
		}
	}()
}

func (s *siteControlService) runSchedule(ctx context.Context, now time.Time) {
	if s.locked() {
		return
	}
	// Housekeeping first: the sweep is rate-limited internally to once a day, so
	// this is a mutex check on every other tick.
	s.pruneSiteHistoryIfDue(now)
	sites, err := s.store.ListSites(ctx, model.SiteListFilter{})
	if err != nil {
		return
	}
	sem := make(chan struct{}, siteSchedulerConcurrency)
	checkinMinute := s.dailyCheckinMinute()
	announcementMinute := s.dailyAnnouncementMinute()
	var wg sync.WaitGroup
	for _, site := range sites {
		if site == nil || !site.Enabled {
			continue
		}
		accounts, err := s.store.ListSiteAccounts(ctx, site.ID, false)
		if err != nil {
			continue
		}
		wg.Add(1)
		go func(site *model.Site, accounts []*model.SiteAccount) {
			defer wg.Done()
			localNow := now.In(loadSiteLocation(site.Timezone, site.Timezone))
			if dailyCheckinDue(localNow, announcementMinute) {
				s.runScheduledAnnouncementTask(ctx, sem, site, localNow.Format("2006-01-02"))
			}
			for _, account := range accounts {
				if account == nil || !account.Enabled {
					continue
				}
				loc := loadSiteLocation(account.Timezone, site.Timezone)
				localNow := now.In(loc)
				day := localNow.Format("2006-01-02")
				if account.AutoCheckin && dailyCheckinDue(localNow, checkinMinute) {
					if attempt, attemptErr := s.store.GetDailyCheckinAttempt(ctx, account.ID, day); attemptErr == nil {
						if run, _ := checkinRetryDue(attempt, now); run {
							if attempt != nil {
								log.Printf("[SITE] re-check account %d on %s: attempt %d after %q", account.ID, day, attempt.AttemptNo+1, attempt.Status)
							}
							s.runScheduledAccountTask(ctx, sem, site, account, "checkin")
						}
					}
				}
				if account.AutoRefresh && siteRefreshDue(account, now) {
					s.runScheduledAccountTask(ctx, sem, site, account, "refresh")
				}
			}
		}(site, accounts)
	}
	wg.Wait()
}

func (s *siteControlService) runScheduledAnnouncementTask(ctx context.Context, sem chan struct{}, site *model.Site, localDay string) {
	releaseGate, ok := s.acquireSiteGate(ctx, site.ID)
	if !ok {
		return
	}
	defer releaseGate()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-sem }()
	// The lease is what decides whether today's refresh already ran, so it has
	// to be taken before the task is recorded. Doing it the other way round
	// left a cancelled row on every tick for the rest of the day — see
	// newSiteTask.
	task := newSiteTask("announcement_refresh", site.ID, 0, 1)
	leaseKey := fmt.Sprintf("site:%d:announcements:%s", site.ID, localDay)
	now := time.Now().UnixMilli()
	acquired, err := s.store.AcquireSiteTaskLease(ctx, leaseKey, task.ID, now, now+int64((26*time.Hour).Milliseconds()))
	if err != nil || !acquired {
		return
	}
	if !s.persistTask(ctx, task, leaseKey) {
		return
	}
	taskCtx, stopLease := s.leaseContext(ctx, leaseKey, task.ID)
	defer stopLease()
	s.updateTask(taskCtx, task, model.SiteTaskStatusRunning, "", "")
	if err := s.refreshAnnouncements(taskCtx, site.ID); err != nil {
		if provider.ErrorCode(err) == provider.CodeUnsupported {
			s.updateTask(taskCtx, task, model.SiteTaskStatusSuccess, "announcements", "该站点不提供公告接口")
			return
		}
		s.updateTask(taskCtx, task, model.SiteTaskStatusFailed, "", siteTaskError(err))
		return
	}
	s.updateTask(taskCtx, task, model.SiteTaskStatusSuccess, "announcements", "公告已刷新")
}

func dailyCheckinDue(localNow time.Time, scheduledMinute int) bool {
	return localNow.Hour()*60+localNow.Minute() >= scheduledMinute
}

// checkinSettled reports whether a day's check-in already reached a state that
// must never be repeated: the reward was collected, or the upstream confirmed
// the account was checked in today. Everything else — a browser challenge, a
// transient upstream error, even an attempt that never finished because the
// process died mid-flight — is worth another look later the same day.
func checkinSettled(status string) bool {
	return status == provider.CheckinSuccess || status == provider.CheckinAlreadyChecked
}

// checkinRetryDue decides whether today's scheduled check-in should run again,
// and returns a short reason for observability. Retrying is deliberately paced
// rather than immediate: the point is to notice a human clearing the challenge
// hours later, not to spin on a site that is genuinely down. A browser challenge
// gets its own, much slower pacing — see siteCheckinBrowserRetryInterval.
func checkinRetryDue(attempt *model.CheckinAttempt, now time.Time) (bool, string) {
	if attempt == nil {
		return true, "first_attempt"
	}
	if checkinSettled(attempt.Status) {
		return false, "settled"
	}
	interval, limit := siteCheckinRetryInterval, siteCheckinRetryLimit
	if attempt.Status == provider.CheckinBrowserRequired {
		interval, limit = siteCheckinBrowserRetryInterval, siteCheckinBrowserRetryLimit
	}
	if attempt.AttemptNo >= limit {
		return false, "retry_limit"
	}
	// A running attempt has no finished_at yet, so it never waits out the
	// cooldown: a crashed attempt recovers on the next tick. Concurrent runs
	// are already serialized by the per-account task lease.
	if attempt.FinishedAt > 0 && now.UnixMilli()-attempt.FinishedAt < interval.Milliseconds() {
		return false, "cooldown"
	}
	return true, "retry"
}

// siteRefreshJitter staggers the 6h refresh expiry by account ID so accounts
// provisioned together do not all fall due in the same minute and contend
// for the scheduler's global slots. Deterministic: no persisted state.
func siteRefreshJitter(accountID int64) time.Duration {
	return time.Duration(accountID%30) * time.Minute
}

func siteRefreshDue(account *model.SiteAccount, now time.Time) bool {
	if account.LastRefreshAt == 0 {
		return true
	}
	return now.UnixMilli()-account.LastRefreshAt >= (siteRefreshInterval + siteRefreshJitter(account.ID)).Milliseconds()
}

func (s *siteControlService) runScheduledAccountTask(ctx context.Context, sem chan struct{}, site *model.Site, account *model.SiteAccount, kind string) {
	releaseGate, ok := s.acquireSiteGate(ctx, site.ID)
	if !ok {
		return
	}
	defer releaseGate()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-sem }()
	// Lease first, then record: a run that never starts is not a cancelled task,
	// it is simply work that was already in flight under the same per-account
	// key. Recording it anyway only added noise the operator could not act on.
	task := newSiteTask(kind, site.ID, account.ID, 1)
	leaseKey := fmt.Sprintf("site:%d:account:%d:%s", site.ID, account.ID, kind)
	now := time.Now().UnixMilli()
	acquired, err := s.store.AcquireSiteTaskLease(ctx, leaseKey, task.ID, now, now+siteTaskLeaseDuration.Milliseconds())
	if err != nil || !acquired {
		return
	}
	if !s.persistTask(ctx, task, leaseKey) {
		return
	}
	defer func() { _ = s.store.ReleaseSiteTaskLease(context.Background(), leaseKey, task.ID) }()
	taskCtx, stopLease := s.leaseContext(ctx, leaseKey, task.ID)
	defer stopLease()
	s.updateTask(taskCtx, task, model.SiteTaskStatusRunning, "", "")
	if kind == "checkin" {
		s.checkinWithTrigger(taskCtx, task, account.ID, "schedule", "daily")
		return
	}
	s.refreshAccountScheduled(taskCtx, task, account.ID, true)
}
