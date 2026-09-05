package app

import (
	"context"
	"fmt"
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
					done, err := s.store.HasDailyCheckinAttempt(ctx, account.ID, day)
					if err == nil && !done {
						s.runScheduledAccountTask(ctx, sem, site, account, "checkin")
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
	task, err := s.createTask(ctx, "announcement_refresh", site.ID, 0, 1)
	if err != nil {
		return
	}
	leaseKey := fmt.Sprintf("site:%d:announcements:%s", site.ID, localDay)
	now := time.Now().UnixMilli()
	acquired, err := s.store.AcquireSiteTaskLease(ctx, leaseKey, task.ID, now, now+int64((26*time.Hour).Milliseconds()))
	if err != nil || !acquired {
		s.updateTask(ctx, task, model.SiteTaskStatusCancelled, "", "已由其他任务执行")
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
	task, err := s.createTask(ctx, kind, site.ID, account.ID, 1)
	if err != nil {
		return
	}
	leaseKey := fmt.Sprintf("site:%d:account:%d:%s", site.ID, account.ID, kind)
	now := time.Now().UnixMilli()
	acquired, err := s.store.AcquireSiteTaskLease(ctx, leaseKey, task.ID, now, now+siteTaskLeaseDuration.Milliseconds())
	if err != nil || !acquired {
		s.updateTask(ctx, task, model.SiteTaskStatusCancelled, "", "conflict")
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
	s.refreshAccount(taskCtx, task, account.ID, true)
}
