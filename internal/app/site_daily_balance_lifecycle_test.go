package app

import (
	"context"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestDailyBalanceHistoryFirstScheduledSampleIsOnlyABaseline(t *testing.T) {
	service, account, _ := dailyBalanceService(t, &dailyBalanceAdapter{})
	ctx := context.Background()
	site, err := service.store.GetSite(ctx, account.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: service.store}
	location := dailyBalanceLocation()
	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, 11, day, hour, minute, 0, 0, location)
	}
	refresh := func(amount float64, observedAt time.Time) {
		t.Helper()
		if err := service.store.UpdateSiteAccountBalance(ctx, account.ID, amount, "USD", observedAt.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		current, err := service.store.GetSiteAccount(ctx, account.ID)
		if err != nil {
			t.Fatal(err)
		}
		service.recordBalanceSnapshot(ctx, current, site, observedAt.UnixMilli())
	}
	collect := func(amount float64, scheduled time.Time) {
		t.Helper()
		// Persist an actual fixed-time observation through the real store. A
		// historical fixture must not ask today's provider for a past balance.
		sample := &model.SiteAccountDailyBalance{
			SiteAccountID: account.ID, LocalDay: scheduled.Format("2006-01-02"),
			Currency: "USD", ScheduledAt: scheduled.UnixMilli(), Status: "unknown",
		}
		if claimed, err := service.store.CreateSiteAccountDailyBalance(ctx, sample); err != nil || !claimed {
			t.Fatalf("claim=%v err=%v", claimed, err)
		}
		sample.Balance, sample.Status, sample.CollectedAt = &amount, "success", scheduled.Add(time.Second).UnixMilli()
		if completed, err := service.store.CompleteSiteAccountDailyBalance(ctx, sample); err != nil || !completed {
			t.Fatalf("completion=%v err=%v", completed, err)
		}
	}

	// A daytime installation/refresh has a current balance and a legacy
	// closing observation, but neither is a fixed 00:05 observation.
	refresh(110, at(27, 18, 0))
	if history := server.siteBalanceHistory(ctx, at(27, 0, 0), at(27, 18, 0), "today"); len(history) != 0 {
		t.Fatalf("daytime refresh fabricated scheduled history: %+v", history)
	}

	collect(100, at(28, 0, 5))
	refresh(90, at(28, 12, 0))
	history := server.siteBalanceHistory(ctx, at(28, 0, 0), at(28, 18, 0), "today")
	if len(history) != 1 || history[0].Day != "2026-11-28" || history[0].ComparisonStatus != "baseline" || history[0].DailyDelta != nil {
		t.Fatalf("first scheduled observation must only establish a baseline: %+v", history)
	}
	if history[0].Balance == nil || *history[0].Balance != 100 || history[0].KnownAccounts != 1 || history[0].UpdatedAt != at(28, 0, 5).Add(time.Second).UnixMilli() {
		t.Fatalf("intraday refresh rewrote the fixed-time baseline: %+v", history[0])
	}
	current, err := service.store.GetSiteAccount(ctx, account.ID)
	if err != nil || current == nil || current.Balance == nil || *current.Balance != 90 {
		t.Fatalf("current balance must remain separate from the daily baseline: account=%+v err=%v", current, err)
	}

	collect(84.5, at(29, 0, 5))
	history = server.siteBalanceHistory(ctx, at(29, 0, 0), at(29, 0, 6), "today")
	if len(history) != 2 || history[1].Day != "2026-11-29" || history[1].ComparisonStatus != "complete" || history[1].DailyDelta == nil || *history[1].DailyDelta != -15.5 {
		t.Fatalf("adjacent fixed samples must compare 84.5 against 100, not an intraday refresh: %+v", history)
	}
	if history[0].ComparisonStatus != "baseline" || history[0].DailyDelta != nil {
		t.Fatalf("a later sample retroactively invented the first day's delta: %+v", history[0])
	}
}

func TestDailyBalanceHistoryInterruptedFirstSampleIsNotAUsableBaseline(t *testing.T) {
	service, account, _ := dailyBalanceService(t, &dailyBalanceAdapter{})
	ctx := context.Background()
	location := dailyBalanceLocation()
	first := time.Date(2026, 11, 28, 0, 5, 0, 0, location)
	for index, amount := range []float64{100, 90, 85} {
		scheduled := first.AddDate(0, 0, index)
		sample := &model.SiteAccountDailyBalance{
			SiteAccountID: account.ID, LocalDay: scheduled.Format("2006-01-02"),
			Currency: "USD", ScheduledAt: scheduled.UnixMilli(), Status: "unknown",
		}
		if claimed, err := service.store.CreateSiteAccountDailyBalance(ctx, sample); err != nil || !claimed {
			t.Fatalf("claim=%v err=%v", claimed, err)
		}
		if index == 0 {
			continue // The process stopped after the durable claim, before completion.
		}
		sample.Balance, sample.Status, sample.CollectedAt = &amount, "success", scheduled.Add(time.Second).UnixMilli()
		if completed, err := service.store.CompleteSiteAccountDailyBalance(ctx, sample); err != nil || !completed {
			t.Fatalf("completion=%v err=%v", completed, err)
		}
	}
	server := &Server{store: service.store}
	history := server.siteBalanceHistory(ctx, first, first.AddDate(0, 0, 2).Add(time.Minute), "today")
	if len(history) != 3 {
		t.Fatalf("history=%+v, want all three scheduled days", history)
	}
	for _, point := range history[:2] {
		if point.ComparisonStatus != "incomplete" || point.DailyDelta != nil {
			t.Fatalf("interrupted observation became a usable baseline: %+v", point)
		}
	}
	if history[2].ComparisonStatus != "complete" || history[2].DailyDelta == nil || *history[2].DailyDelta != -5 {
		t.Fatalf("two subsequent complete observations must restore comparisons: %+v", history[2])
	}
}
