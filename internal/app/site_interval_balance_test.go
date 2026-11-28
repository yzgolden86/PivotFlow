package app

import (
	"context"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestIntervalBalanceAlignedWindowsAndMidnightBoundary(t *testing.T) {
	for _, item := range []struct {
		now, want string
		due       bool
	}{
		{"2026-11-28 09:37", "2026-11-28 09:05", true},
		{"2026-11-28 10:04", "2026-11-28 09:05", true},
		{"2026-11-28 10:05", "2026-11-28 10:05", true},
		{"2026-11-28 00:03", "2026-11-27 23:05", false},
		{"2026-11-28 00:05", "2026-11-28 00:05", true},
	} {
		now, err := time.ParseInLocation("2006-01-02 15:04", item.now, dailyBalanceLocation())
		if err != nil {
			t.Fatal(err)
		}
		at, due := intervalBalanceWindow(now.UTC(), 5, 60)
		if at.Format("2006-01-02 15:04") != item.want || due != item.due {
			t.Fatalf("now=%s slot=%v due=%v", item.now, at, due)
		}
	}
	late, _ := time.ParseInLocation("2006-01-02 15:04", "2026-11-28 23:05", dailyBalanceLocation())
	if got := intervalBalanceCutoff(late, 60).Format("2006-01-02 15:04"); got != "2026-11-29 00:00" {
		t.Fatalf("a previous day's balance must not be read after midnight: %s", got)
	}
}

func TestIntervalBalanceSupportedIntervalsUseTheConfiguredPhase(t *testing.T) {
	now := time.Date(2026, 11, 28, 14, 49, 0, 0, dailyBalanceLocation())
	for _, item := range []struct {
		interval int
		want     string
	}{
		{15, "14:35"}, {30, "14:20"}, {60, "14:20"},
		{120, "13:20"}, {360, "13:20"}, {720, "13:20"},
	} {
		at, due := intervalBalanceWindow(now.UTC(), 80, item.interval)
		if !due || at.Format("15:04") != item.want || !at.Add(time.Duration(item.interval)*time.Minute).After(now) {
			t.Errorf("interval=%d slot=%v due=%v, want %s", item.interval, at, due, item.want)
		}
	}
}

func hourlyBalanceService(t *testing.T, adapter *dailyBalanceAdapter) (*siteControlService, *model.SiteAccount, time.Time) {
	t.Helper()
	service, account, now := dailyBalanceService(t, adapter)
	if err := service.store.BatchUpdateSettings(context.Background(), map[string]string{"site_balance_sample_interval_minutes": "60"}); err != nil {
		t.Fatal(err)
	}
	service.configService = NewConfigService(service.store)
	if err := service.configService.LoadDefaults(context.Background()); err != nil {
		t.Fatal(err)
	}
	return service, account, now
}

func TestIntervalBalanceDefaultAndDurableOncePerSlot(t *testing.T) {
	if got := (*siteControlService)(nil).balanceSampleInterval(); got != 60 {
		t.Fatalf("default interval=%d, want hourly", got)
	}
	amount := 103.0
	adapter := &dailyBalanceAdapter{amount: &amount}
	service, account, now := hourlyBalanceService(t, adapter)
	service.runSchedule(context.Background(), now)
	amount = 110
	service.activeSiteTasks = nil
	service.runSchedule(context.Background(), now.Add(time.Second))
	rows, err := service.store.ListSiteAccountBalanceSamples(context.Background(), "", "")
	if err != nil || len(rows) != 1 || rows[0].SiteAccountID != account.ID || rows[0].Status != "success" || rows[0].Balance == nil || *rows[0].Balance != 103 || adapter.reads != 1 || adapter.models != 0 {
		t.Fatalf("slot must be read once even after in-memory task reset: rows=%+v reads=%d models=%d err=%v", rows, adapter.reads, adapter.models, err)
	}
	legacy, err := service.store.ListSiteAccountDailyBalances(context.Background(), "", "")
	if err != nil || len(legacy) != 0 {
		t.Fatalf("hourly collection must not overwrite old fixed daily history: %+v, %v", legacy, err)
	}
}

func TestIntervalBalanceEarlierSlotDoesNotPreventCurrentRead(t *testing.T) {
	amount := 110.0
	adapter := &dailyBalanceAdapter{amount: &amount}
	service, account, now := hourlyBalanceService(t, adapter)
	_, _, minute := service.dailyBalanceSettings()
	at, _ := intervalBalanceWindow(now, minute, 60)
	previous := at.Add(-time.Hour)
	claim := &model.SiteAccountDailyBalance{SiteAccountID: account.ID, LocalDay: previous.Format("2006-01-02"), Currency: "USD", ScheduledAt: previous.UnixMilli(), Status: "unknown"}
	if created, err := service.store.CreateSiteAccountBalanceSample(context.Background(), claim); err != nil || !created {
		t.Fatalf("previous interrupted slot: %v %v", created, err)
	}
	service.runSchedule(context.Background(), now)
	rows, err := service.store.ListSiteAccountBalanceSamples(context.Background(), "", "")
	if err != nil || len(rows) != 2 || adapter.reads != 1 {
		t.Fatalf("a previous slot blocked the current observation: rows=%+v reads=%d err=%v", rows, adapter.reads, err)
	}
}

func TestIntervalBalanceFailedSlotRemainsUnknownAndDoesNotSpin(t *testing.T) {
	adapter := &dailyBalanceAdapter{}
	service, _, now := hourlyBalanceService(t, adapter)
	service.runSchedule(context.Background(), now)
	service.activeSiteTasks = nil
	service.runSchedule(context.Background(), now.Add(time.Second))
	rows, err := service.store.ListSiteAccountBalanceSamples(context.Background(), "", "")
	if err != nil || len(rows) != 1 || rows[0].Status != "failed" || rows[0].Balance != nil || adapter.reads != 1 {
		t.Fatalf("failed hourly read became zero or retried every tick: rows=%+v reads=%d err=%v", rows, adapter.reads, err)
	}
}

func TestIntervalBalanceRetentionPreservesRecentAndLegacyObservations(t *testing.T) {
	service, account, now := hourlyBalanceService(t, &dailyBalanceAdapter{})
	ctx := context.Background()
	for _, days := range []int{-91, -90, -89} {
		at := now.AddDate(0, 0, days)
		entry := &model.SiteAccountDailyBalance{SiteAccountID: account.ID, LocalDay: at.Format("2006-01-02"), Currency: "USD", ScheduledAt: at.UnixMilli(), Status: "unknown"}
		if created, err := service.store.CreateSiteAccountBalanceSample(ctx, entry); err != nil || !created {
			t.Fatalf("create %d: %v %v", days, created, err)
		}
		if created, err := service.store.CreateSiteAccountDailyBalance(ctx, entry); err != nil || !created {
			t.Fatalf("create legacy %d: %v %v", days, created, err)
		}
	}
	service.pruneSiteHistory(ctx, now.Add(-siteHistoryRetention).UnixMilli(), now.UnixMilli())
	rows, err := service.store.ListSiteAccountBalanceSamples(ctx, "", "")
	if err != nil || len(rows) != 2 {
		t.Fatalf("90-day retention must keep boundary/recent rows: %+v, %v", rows, err)
	}
	legacy, err := service.store.ListSiteAccountDailyBalances(ctx, "", "")
	if err != nil || len(legacy) != 3 {
		t.Fatalf("interval retention changed legacy history: %+v, %v", legacy, err)
	}
}
