package app

import (
	"context"
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
