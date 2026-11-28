package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

const (
	siteDailyBalanceTime   = "00:05"
	siteDailyBalanceWindow = 15 * time.Minute
)

func dailyBalanceLocation() *time.Location {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return location
}

func dailyBalanceWindow(now time.Time, minute int) (time.Time, bool) {
	now = now.In(dailyBalanceLocation())
	scheduled := time.Date(now.Year(), now.Month(), now.Day(), minute/60, minute%60, 0, 0, now.Location())
	if now.Before(scheduled) {
		previous := scheduled.AddDate(0, 0, -1)
		if now.Before(previous.Add(siteDailyBalanceWindow)) {
			scheduled = previous
		}
	}
	return scheduled, !now.Before(scheduled) && now.Before(scheduled.Add(siteDailyBalanceWindow))
}

func (s *siteControlService) dailyBalanceSettings() (bool, string, int) {
	enabled, value := true, siteDailyBalanceTime
	if s != nil && s.configService != nil {
		enabled = s.configService.GetBool("site_daily_balance_enabled", true)
		value = s.configService.GetString("site_daily_balance_time", siteDailyBalanceTime)
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil || parsed.Format("15:04") != value {
		value = siteDailyBalanceTime
		parsed, _ = time.Parse("15:04", value)
	}
	return enabled, value, parsed.Hour()*60 + parsed.Minute()
}

func (s *siteControlService) runScheduledDailyBalance(ctx context.Context, site *model.Site, account *model.SiteAccount, scheduled time.Time) {
	s.runBalanceObservation(ctx, site, account, scheduled, 0)
}

func (s *siteControlService) runBalanceObservation(ctx context.Context, site *model.Site, account *model.SiteAccount, scheduled time.Time, interval int) {
	adapter, err := s.adapter(site)
	if err != nil || !adapter.Capabilities().Balance {
		return
	}
	day := scheduled.Format("2006-01-02")
	key := fmt.Sprintf("daily_balance:%d:%s", account.ID, day)
	create, complete := s.store.CreateSiteAccountDailyBalance, s.store.CompleteSiteAccountDailyBalance
	cutoff := scheduled.Add(siteDailyBalanceWindow)
	if interval > 0 {
		key = fmt.Sprintf("balance_sample:%d:%d", account.ID, scheduled.UnixMilli())
		create, complete = s.store.CreateSiteAccountBalanceSample, s.store.CompleteSiteAccountBalanceSample
		cutoff = intervalBalanceCutoff(scheduled, interval)
	}
	spec := siteTaskSpec{
		key: key, kind: "refresh", siteID: site.ID, accountID: account.ID, total: 1,
		// Share the ordinary refresh lease so this read cannot race account writes.
		leaseKey: fmt.Sprintf("site:%d:account:%d:refresh", site.ID, account.ID), leaseDuration: siteTaskLeaseDuration, parent: ctx,
	}
	run, _, err := s.submitSiteTask(ctx, spec, func(taskCtx context.Context, task *model.SiteTask) {
		runCtx, finish, err := s.acquireManualSiteTask(taskCtx, site.ID)
		if err != nil {
			s.updateTask(taskCtx, task, model.SiteTaskStatusFailed, "", siteTaskError(err))
			return
		}
		defer finish()
		// A long queue or restart must not masquerade as an on-time observation.
		now := time.Now()
		if now.Before(scheduled) || !now.Before(cutoff) {
			s.updateTask(runCtx, task, model.SiteTaskStatusCancelled, "", "cancelled")
			return
		}
		sampleCtx, cancelSample := context.WithDeadline(runCtx, cutoff)
		defer cancelSample()
		latest, err := s.store.GetSiteAccount(sampleCtx, account.ID)
		currentSite, siteErr := s.store.GetSite(sampleCtx, site.ID)
		if err != nil || siteErr != nil || !latest.Enabled || !currentSite.Enabled {
			s.updateTask(runCtx, task, model.SiteTaskStatusCancelled, "", "cancelled")
			return
		}
		adapter, err := s.adapter(currentSite)
		if err != nil || !adapter.Capabilities().Balance {
			s.updateTask(runCtx, task, model.SiteTaskStatusCancelled, "", "cancelled")
			return
		}
		sample := &model.SiteAccountDailyBalance{SiteAccountID: latest.ID, LocalDay: day, ScheduledAt: scheduled.UnixMilli(), Currency: strings.ToUpper(strings.TrimSpace(latest.BalanceCurrency)), Status: "unknown"}
		claimed, err := create(sampleCtx, sample)
		if err != nil || !claimed {
			if err != nil {
				s.updateTask(runCtx, task, model.SiteTaskStatusFailed, "", siteTaskError(err))
			} else {
				s.updateTask(runCtx, task, model.SiteTaskStatusSuccess, fmt.Sprintf("daily_balance:%s", day), "")
			}
			return
		}
		s.updateTask(runCtx, task, model.SiteTaskStatusRunning, "", "")
		creds, err := s.operationCredentials(sampleCtx, latest, currentSite, adapter)
		var snapshot provider.AccountSnapshot
		if contextErr := sampleCtx.Err(); contextErr != nil {
			err = contextErr
		} else if !time.Now().Before(cutoff) {
			err = context.DeadlineExceeded
		}
		if err == nil {
			// Exactly one read. The existing provider client enforces the shared
			// upstream budget; this job does not discover models or call check-in.
			callCtx, cancel := context.WithTimeout(sampleCtx, 35*time.Second)
			snapshot, err = adapter.RefreshAccount(callCtx, provider.RefreshAccountRequest{BaseURL: currentSite.BaseURL, ProxyURL: siteProxyURL(currentSite), Credentials: creds})
			if contextErr := callCtx.Err(); contextErr != nil {
				err = contextErr
			}
			cancel()
		}
		collectedAt := time.Now()
		// Recheck the actual return time even if a provider ignores cancellation
		// and reports a late success with a nil error.
		if !collectedAt.Before(cutoff) {
			err = context.DeadlineExceeded
		} else if contextErr := sampleCtx.Err(); contextErr != nil {
			err = contextErr
		}
		sample.CollectedAt, sample.Status = collectedAt.UnixMilli(), "failed"
		if err == nil && snapshot.Balance != nil && !math.IsNaN(*snapshot.Balance) && !math.IsInf(*snapshot.Balance, 0) && strings.TrimSpace(snapshot.Currency) != "" && len(strings.TrimSpace(snapshot.Currency)) <= 16 {
			balance := *snapshot.Balance
			sample.Balance, sample.Currency, sample.Status = &balance, strings.ToUpper(strings.TrimSpace(snapshot.Currency)), "success"
		} else if err != nil {
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				sample.ErrorCode = provider.CodeTimeout
			case errors.Is(err, context.Canceled):
				sample.ErrorCode = "cancelled"
			default:
				sample.ErrorCode = safeSiteTaskError(provider.ErrorCode(err))
			}
		} else {
			sample.ErrorCode = provider.CodeInvalidResponse
		}
		// Preserve the terminal observation even when the task was cancelled.
		persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		completed, persistErr := complete(persistCtx, sample)
		cancel()
		if persistErr != nil || !completed {
			log.Printf("[SITE] daily balance persistence failed for account %d on %s", account.ID, day)
			s.updateTask(runCtx, task, model.SiteTaskStatusFailed, "", provider.CodeRequestFailed)
			return
		}
		if sample.Status != "success" {
			s.updateTask(runCtx, task, model.SiteTaskStatusFailed, "", sample.ErrorCode)
			return
		}
		if err := s.store.UpdateSiteAccountBalance(sampleCtx, latest.ID, *sample.Balance, sample.Currency, sample.CollectedAt); err != nil {
			log.Printf("[SITE] latest balance observation could not be updated for account %d", latest.ID)
		}
		s.updateTask(runCtx, task, model.SiteTaskStatusSuccess, fmt.Sprintf("daily_balance:%s", day), "")
	})
	if err == nil {
		<-run.done
	}
}
