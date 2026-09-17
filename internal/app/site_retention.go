package app

import (
	"context"
	"log"
	"time"
)

const (
	// Terminal task rows and finished check-in runs are history, not state: the
	// scheduler only ever consults the current local day. A month is enough for
	// the console to answer "what happened recently" without letting the tables
	// grow without bound — which matters more now that a blocked check-in
	// retries through the day and writes a run per attempt.
	siteHistoryRetention = 30 * 24 * time.Hour

	// How often the sweep is allowed to run. Tracked in memory on purpose: a
	// restart costs one extra sweep, and the prune is idempotent, so persisting
	// the marker would buy nothing.
	siteHistoryPruneInterval = 24 * time.Hour

	// Rows removed per statement, and the cap on statements per table per sweep.
	// 500 keeps each DELETE short so a sweep never holds a long write lock. 200
	// passes (100k rows) far exceeds a day's steady-state volume, so the cap
	// only ever bites while draining the backlog that accumulated before this
	// sweep existed; whatever is left is picked up by the next sweep.
	siteHistoryPruneBatch     = 500
	siteHistoryPruneMaxPasses = 200

	// Key the sweep registers with runAsync. It never becomes a task row, so the
	// site_tasks ID space stays untouched.
	siteHistoryPruneTaskKey = "maintenance:site-history-prune"
)

// pruneSiteHistoryIfDue runs the retention sweep at most once per
// siteHistoryPruneInterval. It is launched through runAsync so that a long first
// sweep — draining the pre-existing backlog — cannot stall the scheduler tick,
// and so shutdown cancels it the same way it cancels any other site task.
func (s *siteControlService) pruneSiteHistoryIfDue(now time.Time) {
	if s == nil || s.store == nil {
		return
	}
	s.historyPruneMu.Lock()
	if s.historyPruneAt > 0 && now.UnixMilli()-s.historyPruneAt < siteHistoryPruneInterval.Milliseconds() {
		s.historyPruneMu.Unlock()
		return
	}
	s.historyPruneAt = now.UnixMilli()
	s.historyPruneMu.Unlock()

	cutoff := now.Add(-siteHistoryRetention).UnixMilli()
	expiredBefore := now.UnixMilli()
	s.runAsync(siteHistoryPruneTaskKey, func(runCtx context.Context) {
		s.pruneSiteHistory(runCtx, cutoff, expiredBefore)
	})
}

// pruneSiteHistory removes history older than the cutoff, one bounded batch at a
// time. A table that fails ends its own pass without aborting the others: this
// is housekeeping, and a partial sweep is still progress.
func (s *siteControlService) pruneSiteHistory(ctx context.Context, cutoff, expiredBefore int64) {
	tasks := s.pruneSiteHistoryTable("site_tasks", func() (int64, error) {
		return s.store.DeleteFinishedSiteTasks(ctx, cutoff, siteHistoryPruneBatch)
	})
	runs := s.pruneSiteHistoryTable("checkin_runs", func() (int64, error) {
		return s.store.DeleteFinishedCheckinRuns(ctx, cutoff, siteHistoryPruneBatch)
	})
	leases := s.pruneSiteHistoryTable("site_task_leases", func() (int64, error) {
		return s.store.DeleteExpiredSiteTaskLeases(ctx, expiredBefore, siteHistoryPruneBatch)
	})
	if tasks+runs+leases > 0 {
		log.Printf("[SITE] history prune: tasks=%d runs=%d leases=%d (older than %s)",
			tasks, runs, leases, time.UnixMilli(cutoff).Format("2006-01-02"))
	}
}

func (s *siteControlService) pruneSiteHistoryTable(table string, prune func() (int64, error)) int64 {
	var total int64
	for pass := 0; pass < siteHistoryPruneMaxPasses; pass++ {
		removed, err := prune()
		if err != nil {
			log.Printf("[SITE] history prune %s stopped after %d rows: %v", table, total, err)
			return total
		}
		total += removed
		if removed < siteHistoryPruneBatch {
			return total
		}
	}
	log.Printf("[SITE] history prune %s hit the %d-pass cap at %d rows; the rest goes next sweep",
		table, siteHistoryPruneMaxPasses, total)
	return total
}
