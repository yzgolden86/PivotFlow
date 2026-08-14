package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

const (
	siteSchedulerTick        = time.Minute
	siteRefreshInterval      = 6 * time.Hour
	siteTaskLeaseDuration    = 90 * time.Second
	siteDailyCheckinHour     = 8
	siteSchedulerConcurrency = 4
)

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
			for _, account := range accounts {
				if account == nil || !account.Enabled {
					continue
				}
				loc := loadSiteLocation(account.Timezone, site.Timezone)
				localNow := now.In(loc)
				day := localNow.Format("2006-01-02")
				if account.AutoCheckin && localNow.Hour() >= siteDailyCheckinHour {
					done, err := s.store.HasDailyCheckinAttempt(ctx, account.ID, day)
					if err == nil && !done {
						s.runScheduledAccountTask(ctx, sem, site, account, "checkin")
					}
				}
				if account.AutoRefresh && (account.LastRefreshAt == 0 || now.UnixMilli()-account.LastRefreshAt >= siteRefreshInterval.Milliseconds()) {
					s.runScheduledAccountTask(ctx, sem, site, account, "refresh")
				}
			}
		}(site, accounts)
	}
	wg.Wait()
}

func (s *siteControlService) runScheduledAccountTask(ctx context.Context, sem chan struct{}, site *model.Site, account *model.SiteAccount, kind string) {
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
