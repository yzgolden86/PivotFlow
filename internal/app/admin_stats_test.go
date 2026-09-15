package app

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func TestFillHealthTimeline_UsesSecondsForAvgTimes(t *testing.T) {
	store, err := storage.CreateSQLiteStore(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("创建测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	s := &Server{store: store}

	now := time.Now().Truncate(time.Second)
	startTime := now.Add(-24 * time.Hour)
	endTime := now

	channelID64 := int64(1)
	channelID := int(channelID64)
	modelName := "claude-test"

	logTime := now.Add(-12 * time.Hour)
	if err := store.AddLog(context.Background(), &model.LogEntry{
		Time:          model.JSONTime{Time: logTime},
		Model:         modelName,
		ActualModel:   modelName,
		ChannelID:     channelID64,
		StatusCode:    200,
		Message:       "ok",
		Duration:      2.3,
		IsStreaming:   true,
		FirstByteTime: 1.5,
	}); err != nil {
		t.Fatalf("写入日志失败: %v", err)
	}

	stats := []model.StatsEntry{
		{
			ChannelID: ptrInt(channelID),
			Model:     modelName,
		},
	}
	filter := &model.LogFilter{
		ChannelID: ptrInt64(channelID64),
		Model:     modelName,
	}

	s.fillHealthTimeline(context.Background(), stats, startTime, endTime, filter, false)

	if len(stats) == 0 {
		t.Fatal("stats 切片为空")
	}
	if len(stats[0].HealthTimeline) != 48 {
		t.Fatalf("期望 health timeline 长度=48，实际=%d", len(stats[0].HealthTimeline))
	}

	var found bool
	for _, point := range stats[0].HealthTimeline {
		if point.SuccessCount == 1 && point.ErrorCount == 0 {
			found = true
			if math.Abs(point.AvgFirstByteTime-1.5) > 1e-9 {
				t.Fatalf("AvgFirstByteTime 期望≈1.5(秒)，实际=%v", point.AvgFirstByteTime)
			}
			if math.Abs(point.AvgDuration-2.3) > 1e-9 {
				t.Fatalf("AvgDuration 期望≈2.3(秒)，实际=%v", point.AvgDuration)
			}
			break
		}
	}
	if !found {
		t.Fatalf("未找到包含写入日志的时间桶")
	}
}

func TestSiteBalanceHistoryAggregatesDailyLatestSnapshots(t *testing.T) {
	store, err := storage.CreateSQLiteStore(t.TempDir() + "/balance-history.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{Name: "stats-balance", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "first", CredentialType: model.CredentialTypeAPIKey, CredentialCiphertext: "one", CredentialKeyVersion: "v1", BalanceCurrency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "second", CredentialType: model.CredentialTypeAPIKey, CredentialCiphertext: "two", CredentialKeyVersion: "v1", BalanceCurrency: "CNY"})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := start.AddDate(0, 0, -1).Format("2006-01-02")
	today := start.Format("2006-01-02")
	for _, snapshot := range []*model.SiteAccountBalanceSnapshot{
		{SiteAccountID: first.ID, LocalDay: yesterday, Currency: "CNY", Balance: 8, UpdatedAt: 100},
		{SiteAccountID: first.ID, LocalDay: today, Currency: "CNY", Balance: 10, UpdatedAt: 200},
		{SiteAccountID: first.ID, LocalDay: today, Currency: "CNY", Balance: 12, UpdatedAt: 300},
		{SiteAccountID: second.ID, LocalDay: today, Currency: "CNY", Balance: 6, UpdatedAt: 250},
	} {
		if err := store.UpsertSiteAccountBalanceSnapshot(ctx, snapshot); err != nil {
			t.Fatal(err)
		}
	}

	server := &Server{store: store}
	history := server.siteBalanceHistory(ctx, start, now, "today")
	if len(history) != 2 {
		t.Fatalf("history=%+v, want yesterday and today", history)
	}
	if history[0].Day != yesterday || history[0].Balance != 8 || history[0].Accounts != 1 {
		t.Fatalf("yesterday=%+v", history[0])
	}
	if history[1].Day != today || history[1].Balance != 18 || history[1].Accounts != 2 || history[1].UpdatedAt != 300 {
		t.Fatalf("today=%+v", history[1])
	}
}

func ptrInt64(v int64) *int64 { return &v }

func ptrInt(v int) *int { return &v }
