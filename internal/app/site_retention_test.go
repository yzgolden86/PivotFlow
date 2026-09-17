package app

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

// rawQueryStore exposes the query hook the SQL store already provides. Tests use
// it to count rows rather than widening the production Store interface with list
// methods only tests would call.
type rawQueryStore interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func countRows(t *testing.T, store storage.Store, query string) int {
	t.Helper()
	raw, ok := store.(rawQueryStore)
	if !ok {
		t.Fatalf("store %T cannot run raw queries", store)
	}
	rows, err := raw.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		t.Fatal("count query returned no row")
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func siteLocalDay(site *model.Site) string {
	return time.Now().In(loadSiteLocation(site.Timezone, site.Timezone)).Format("2006-01-02")
}

// The announcement refresh is gated by a 26-hour lease, so for the rest of the
// day every scheduler tick finds the lease taken. Recording a task row for those
// attempts produced one cancelled row per minute per site — hundreds a day whose
// only content was "another task already did this".
func TestScheduledAnnouncementRecordsNothingWhenTheDayLeaseIsHeld(t *testing.T) {
	service, site, _ := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	ctx := context.Background()
	day := siteLocalDay(site)

	leaseKey := fmt.Sprintf("site:%d:announcements:%s", site.ID, day)
	now := time.Now().UnixMilli()
	held, err := service.store.AcquireSiteTaskLease(ctx, leaseKey, "already-running", now, now+int64((26*time.Hour).Milliseconds()))
	if err != nil || !held {
		t.Fatalf("pre-acquire announcement lease: held=%v err=%v", held, err)
	}

	before := countRows(t, service.store, "SELECT COUNT(*) FROM site_tasks")
	service.runScheduledAnnouncementTask(ctx, make(chan struct{}, 1), site, day)
	if after := countRows(t, service.store, "SELECT COUNT(*) FROM site_tasks"); after != before {
		t.Fatalf("site_tasks rows %d -> %d; a run that never started must not be recorded", before, after)
	}
}

// The other half of the contract: when the lease is free the run really does
// happen and really is recorded, so "records nothing" cannot be satisfied by
// simply never writing a row at all.
func TestScheduledAnnouncementRecordsTheRunThatTakesTheLease(t *testing.T) {
	service, site, _ := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	ctx := context.Background()
	day := siteLocalDay(site)

	service.runScheduledAnnouncementTask(ctx, make(chan struct{}, 1), site, day)

	// The test adapter advertises no announcements capability, so the task
	// settles as an explicit success without reaching the network.
	if got := countRows(t, service.store, "SELECT COUNT(*) FROM site_tasks WHERE kind='announcement_refresh' AND status='success'"); got != 1 {
		t.Fatalf("successful announcement_refresh rows=%d, want 1", got)
	}

	// Every later tick the same day must add nothing: the lease taken by the run
	// above is still held.
	service.runScheduledAnnouncementTask(ctx, make(chan struct{}, 1), site, day)
	service.runScheduledAnnouncementTask(ctx, make(chan struct{}, 1), site, day)
	if got := countRows(t, service.store, "SELECT COUNT(*) FROM site_tasks"); got != 1 {
		t.Fatalf("site_tasks rows=%d after repeat ticks, want 1", got)
	}
}

// A scheduled check-in whose per-account lease is held is work already in
// flight, not a cancelled task.
func TestScheduledAccountTaskRecordsNothingWhenTheLeaseIsHeld(t *testing.T) {
	service, site, account := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	ctx := context.Background()

	leaseKey := fmt.Sprintf("site:%d:account:%d:checkin", site.ID, account.ID)
	now := time.Now().UnixMilli()
	held, err := service.store.AcquireSiteTaskLease(ctx, leaseKey, "already-running", now, now+siteTaskLeaseDuration.Milliseconds())
	if err != nil || !held {
		t.Fatalf("pre-acquire account lease: held=%v err=%v", held, err)
	}

	before := countRows(t, service.store, "SELECT COUNT(*) FROM site_tasks")
	service.runScheduledAccountTask(ctx, make(chan struct{}, 1), site, account, "checkin")
	if after := countRows(t, service.store, "SELECT COUNT(*) FROM site_tasks"); after != before {
		t.Fatalf("site_tasks rows %d -> %d; contended work must not be recorded as cancelled", before, after)
	}
}

// The sweep must age out history and nothing else: a task that is still running,
// or that finished recently, is not history.
func TestPruneSiteHistoryKeepsRunningAndRecentWork(t *testing.T) {
	service, site, account := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	ctx := context.Background()
	now := time.Now().UnixMilli()
	old := now - (siteHistoryRetention + time.Hour).Milliseconds()
	recent := now - time.Hour.Milliseconds()

	create := func(id, status string, finishedAt int64) {
		t.Helper()
		task := &model.SiteTask{ID: id, Kind: "refresh", Status: status, SiteID: site.ID, SiteAccountID: account.ID, ProgressJSON: "{}", CreatedAt: finishedAt, FinishedAt: finishedAt}
		if err := service.store.CreateSiteTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	create("st_old_success", model.SiteTaskStatusSuccess, old)
	create("st_old_failed", model.SiteTaskStatusFailed, old)
	create("st_old_cancelled", model.SiteTaskStatusCancelled, old)
	create("st_recent_success", model.SiteTaskStatusSuccess, recent)
	// An interrupted run has no finished_at and is still "running": pruning it
	// would silently discard work a restart may still resume.
	create("st_old_running", model.SiteTaskStatusRunning, old)
	create("st_old_queued", model.SiteTaskStatusQueued, old)

	service.pruneSiteHistory(ctx, now-siteHistoryRetention.Milliseconds(), now)

	for _, id := range []string{"st_old_success", "st_old_failed", "st_old_cancelled"} {
		if _, err := service.store.GetSiteTask(ctx, id); err == nil {
			t.Fatalf("task %s survived the sweep, want it pruned", id)
		}
	}
	for _, id := range []string{"st_recent_success", "st_old_running", "st_old_queued"} {
		if _, err := service.store.GetSiteTask(ctx, id); err != nil {
			t.Fatalf("task %s was pruned: %v", id, err)
		}
	}
}

// checkin_attempts cascades from checkin_runs, so a run's per-account results
// must age out with it — otherwise the busiest table in the feature would keep
// growing while its parent was pruned.
func TestPruneSiteHistoryCascadesRunsToAttempts(t *testing.T) {
	service, _, account := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	ctx := context.Background()
	now := time.Now().UnixMilli()
	old := now - (siteHistoryRetention + time.Hour).Milliseconds()

	run, err := service.store.CreateCheckinRun(ctx, &model.CheckinRun{Trigger: "schedule", LocalDay: "2020-01-01", Timezone: "Asia/Shanghai", Status: model.SiteTaskStatusSuccess, Total: 1})
	if err != nil {
		t.Fatal(err)
	}
	// CreateCheckinRun always starts a run as unfinished, so age it by hand.
	run.FinishedAt = old
	if err := service.store.UpdateCheckinRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	attempt, err := service.store.CreateCheckinAttempt(ctx, &model.CheckinAttempt{RunID: run.ID, SiteAccountID: account.ID, ProviderID: model.SitePlatformNewAPIFamily, LocalDay: "2020-01-01", TriggerScope: "daily", Status: "success", StartedAt: old, FinishedAt: old})
	if err != nil {
		t.Fatal(err)
	}

	service.pruneSiteHistory(ctx, now-siteHistoryRetention.Milliseconds(), now)

	if _, err := service.store.GetCheckinRun(ctx, run.ID); err == nil {
		t.Fatal("finished run survived the sweep, want it pruned")
	}
	attempts, err := service.store.ListCheckinAttempts(ctx, account.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range attempts {
		if item.ID == attempt.ID {
			t.Fatal("attempt survived its run; the cascade did not fire")
		}
	}
}

// Leases that nothing renews — the daily announcement lease outlives its run by
// design — must not sit in the table forever, but a live lease must be kept.
func TestPruneSiteHistoryRemovesExpiredLeasesOnly(t *testing.T) {
	service, _, _ := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	ctx := context.Background()
	now := time.Now().UnixMilli()

	for _, lease := range []struct {
		key   string
		until int64
	}{
		{"expired-lease", now - time.Hour.Milliseconds()},
		{"live-lease", now + time.Hour.Milliseconds()},
	} {
		acquired, err := service.store.AcquireSiteTaskLease(ctx, lease.key, "owner", now, lease.until)
		if err != nil || !acquired {
			t.Fatalf("seed lease %s: acquired=%v err=%v", lease.key, acquired, err)
		}
	}

	service.pruneSiteHistory(ctx, now-siteHistoryRetention.Milliseconds(), now)

	if got := countRows(t, service.store, "SELECT COUNT(*) FROM site_task_leases WHERE task_key='expired-lease'"); got != 0 {
		t.Fatalf("expired lease rows=%d, want 0", got)
	}
	if got := countRows(t, service.store, "SELECT COUNT(*) FROM site_task_leases WHERE task_key='live-lease'"); got != 1 {
		t.Fatalf("live lease rows=%d, want 1", got)
	}
}

// The sweep is rate-limited, and a second tick inside the window must not start
// another one.
func TestPruneSiteHistoryIfDueRunsAtMostOncePerInterval(t *testing.T) {
	service, _, _ := newSiteRefreshTestService(t, &checkinMethodTestAdapter{})
	// The sweep runs through runAsync, which accounts for itself here. Waiting on
	// it keeps the test from racing the store close in t.Cleanup.
	var wg sync.WaitGroup
	service.wg = &wg
	defer wg.Wait()

	now := time.Now()

	service.pruneSiteHistoryIfDue(now)
	service.historyPruneMu.Lock()
	first := service.historyPruneAt
	service.historyPruneMu.Unlock()
	if first == 0 {
		t.Fatal("first call did not record a sweep")
	}

	service.pruneSiteHistoryIfDue(now.Add(time.Hour))
	service.historyPruneMu.Lock()
	second := service.historyPruneAt
	service.historyPruneMu.Unlock()
	if second != first {
		t.Fatalf("historyPruneAt moved from %d to %d inside the interval", first, second)
	}

	service.pruneSiteHistoryIfDue(now.Add(siteHistoryPruneInterval + time.Minute))
	service.historyPruneMu.Lock()
	third := service.historyPruneAt
	service.historyPruneMu.Unlock()
	if third == first {
		t.Fatal("a sweep after the interval did not re-arm the marker")
	}
}
