package app

import (
	"context"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestHandleAdminDashboardAggregatesControlAndDataPlanes(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()

	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{
		Name:     "Primary Relay",
		Platform: model.SitePlatformNewAPIFamily,
		BaseURL:  "https://relay.example.com",
		Enabled:  true,
		Timezone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("CreateSite failed: %v", err)
	}
	balance := 88.5
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{
		SiteID:               site.ID,
		Label:                "personal",
		CredentialType:       model.CredentialTypeAPIKey,
		CredentialCiphertext: "encrypted-test-value",
		Enabled:              true,
		Status:               model.SiteAccountStatusHealthy,
		Balance:              &balance,
		BalanceCurrency:      "CNY",
	})
	if err != nil {
		t.Fatalf("CreateSiteAccount failed: %v", err)
	}

	protocols := []string{"anthropic"}
	models := []string{"claude-sonnet-4-5"}
	apiKey := "sk-test-dashboard"
	projection, err := store.UpsertSiteProjection(ctx, model.SiteProjectionInput{
		SiteAccountID: account.ID,
		ProjectionKey: "default",
		Name:          "Primary Relay / personal",
		BaseURL:       site.BaseURL,
		Protocols:     protocols,
		Models:        models,
		APIKey:        apiKey,
		SourceHash:    model.SiteProjectionSourceHash(site.BaseURL, protocols, models, apiKey, true),
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("UpsertSiteProjection failed: %v", err)
	}

	now := time.Now()
	logs := []*model.LogEntry{
		{
			Time:           model.JSONTime{Time: now.Add(-2 * time.Minute)},
			LogSource:      model.LogSourceProxy,
			ChannelID:      projection.Channel.ID,
			Model:          "claude-sonnet-4-5",
			StatusCode:     http.StatusOK,
			ClientProtocol: "anthropic",
			InputTokens:    120,
			OutputTokens:   30,
			Cost:           0.25,
			CostMultiplier: 1,
		},
		{
			Time:           model.JSONTime{Time: now.Add(-time.Minute)},
			LogSource:      model.LogSourceProxy,
			ChannelID:      projection.Channel.ID,
			Model:          "claude-sonnet-4-5",
			StatusCode:     http.StatusBadGateway,
			ClientProtocol: "anthropic",
			InputTokens:    10,
			CostMultiplier: 1,
		},
	}
	if err := store.BatchAddLogs(ctx, logs); err != nil {
		t.Fatalf("BatchAddLogs failed: %v", err)
	}

	c, w := newTestContext(t, newRequest(http.MethodGet, "/admin/dashboard?range=today", nil))
	server.HandleAdminDashboard(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", w.Code, w.Body.String())
	}
	response := mustParseAPIResponse[model.DashboardSnapshot](t, w.Body.Bytes())
	snapshot := response.Data
	if snapshot.Totals.Requests != 2 || snapshot.Totals.Success != 1 || snapshot.Totals.Errors != 1 {
		t.Fatalf("unexpected totals: %+v", snapshot.Totals)
	}
	if snapshot.Totals.InputTokens != 130 || snapshot.Totals.OutputTokens != 30 {
		t.Fatalf("unexpected token totals: %+v", snapshot.Totals)
	}
	if snapshot.Totals.EffectiveCost != 0.25 {
		t.Fatalf("effective cost=%v, want 0.25", snapshot.Totals.EffectiveCost)
	}
	if len(snapshot.Balances) != 1 || snapshot.Balances[0].Currency != "USD" || snapshot.Balances[0].Amount != balance {
		t.Fatalf("unexpected balances: %+v", snapshot.Balances)
	}
	if len(snapshot.ModelUsage) != 1 || snapshot.ModelUsage[0].Label != "claude-sonnet-4-5" {
		t.Fatalf("unexpected model usage: %+v", snapshot.ModelUsage)
	}
	if len(snapshot.SiteUsage) != 1 || snapshot.SiteUsage[0].SiteID != site.ID || snapshot.SiteUsage[0].Requests != 2 {
		t.Fatalf("unexpected site usage: %+v", snapshot.SiteUsage)
	}
	if len(snapshot.ClientUsage) != 1 || snapshot.ClientUsage[0].Label != "Claude Code" || snapshot.ClientUsage[0].Requests != 2 {
		t.Fatalf("unexpected client usage: %+v", snapshot.ClientUsage)
	}
	if snapshot.SiteCount != 1 || snapshot.AccountCount != 1 || snapshot.ChannelCount != 1 {
		t.Fatalf("unexpected resource counts: sites=%d accounts=%d channels=%d", snapshot.SiteCount, snapshot.AccountCount, snapshot.ChannelCount)
	}
}

func TestHandleAdminDashboardDoesNotDoubleApplySitePricingMultiplier(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()

	ctx := context.Background()
	channel, err := store.CreateConfig(ctx, &model.Config{
		Name:           "relay-priced-channel",
		URLs:           model.ChannelURLs{{URL: "https://relay-priced.example"}},
		Priority:       10,
		Enabled:        true,
		CostMultiplier: 3000,
		ModelEntries:   []model.ModelEntry{{Model: "deepseek-v4.1-flash-1017"}},
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}

	now := time.Now()
	if err := store.BatchAddLogs(ctx, []*model.LogEntry{
		{
			Time:           model.JSONTime{Time: now},
			LogSource:      model.LogSourceProxy,
			ChannelID:      channel.ID,
			Model:          "deepseek-v4.1-flash-1017",
			StatusCode:     http.StatusOK,
			Cost:           0.56,
			CostMultiplier: 3000,
			CostSource:     costSourceSite,
		},
		{
			Time:           model.JSONTime{Time: now},
			LogSource:      model.LogSourceProxy,
			ChannelID:      channel.ID,
			Model:          "deepseek-v4.1-flash-1017",
			StatusCode:     http.StatusOK,
			Cost:           0.01,
			CostMultiplier: 2,
			CostSource:     costSourceLocal,
		},
	}); err != nil {
		t.Fatalf("add logs: %v", err)
	}

	c, w := newTestContext(t, newRequest(http.MethodGet, "/admin/dashboard?range=today", nil))
	server.HandleAdminDashboard(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", w.Code, w.Body.String())
	}
	snapshot := mustParseAPIResponse[model.DashboardSnapshot](t, w.Body.Bytes()).Data
	if math.Abs(snapshot.Totals.Cost-0.57) > 1e-9 || math.Abs(snapshot.Totals.EffectiveCost-0.58) > 1e-9 {
		t.Fatalf("dashboard costs=(standard %.2f, effective %.2f), want (0.57, 0.58)", snapshot.Totals.Cost, snapshot.Totals.EffectiveCost)
	}
	var trendEffectiveCost float64
	for _, point := range snapshot.Trend {
		if point.EffectiveCost != nil {
			trendEffectiveCost += *point.EffectiveCost
		}
	}
	if math.Abs(trendEffectiveCost-0.58) > 1e-9 {
		t.Fatalf("trend effective cost=%v, want 0.58", trendEffectiveCost)
	}
}

func TestHandleAdminDashboardAppliesChannelAndModelFilters(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()

	ctx := context.Background()
	selected, err := store.CreateConfig(ctx, &model.Config{
		Name:         "selected-relay",
		URLs:         model.ChannelURLs{{URL: "https://selected.example"}},
		Priority:     10,
		Enabled:      true,
		ModelEntries: []model.ModelEntry{{Model: "selected-model"}},
	})
	if err != nil {
		t.Fatalf("create selected channel: %v", err)
	}
	other, err := store.CreateConfig(ctx, &model.Config{
		Name:         "other-relay",
		URLs:         model.ChannelURLs{{URL: "https://other.example"}},
		Priority:     10,
		Enabled:      true,
		ModelEntries: []model.ModelEntry{{Model: "other-model"}},
	})
	if err != nil {
		t.Fatalf("create other channel: %v", err)
	}

	now := model.JSONTime{Time: time.Now()}
	if err := store.BatchAddLogs(ctx, []*model.LogEntry{
		{Time: now, LogSource: model.LogSourceProxy, ChannelID: selected.ID, Model: "selected-model", StatusCode: http.StatusOK},
		{Time: now, LogSource: model.LogSourceProxy, ChannelID: other.ID, Model: "other-model", StatusCode: http.StatusOK},
	}); err != nil {
		t.Fatalf("add logs: %v", err)
	}

	c, w := newTestContext(t, newRequest(http.MethodGet, "/admin/dashboard?range=today&channel_name=selected-relay&model=selected-model", nil))
	server.HandleAdminDashboard(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200; body=%s", w.Code, w.Body.String())
	}
	snapshot := mustParseAPIResponse[model.DashboardSnapshot](t, w.Body.Bytes()).Data
	if snapshot.Totals.Requests != 1 {
		t.Fatalf("filtered requests=%d, want 1", snapshot.Totals.Requests)
	}
	if len(snapshot.ModelUsage) != 1 || snapshot.ModelUsage[0].Label != "selected-model" {
		t.Fatalf("filtered model usage=%+v, want selected-model only", snapshot.ModelUsage)
	}
	if len(snapshot.ClientUsage) != 1 || snapshot.ClientUsage[0].Requests != 1 {
		t.Fatalf("filtered client usage=%+v, want one request", snapshot.ClientUsage)
	}
}
