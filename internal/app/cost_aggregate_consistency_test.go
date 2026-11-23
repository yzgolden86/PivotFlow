package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestCostSemanticsInvalidRowsKeepUsageButDoNotBecomeChartCharges(t *testing.T) {
	for _, handler := range costSemanticsHandlers() {
		t.Run(handler.name, func(t *testing.T) {
			server, store, cleanup := setupAdminTestServer(t)
			defer cleanup()
			ctx := context.Background()
			channel, err := store.CreateConfig(ctx, &model.Config{
				Name: "invalid-cost-fixture", Enabled: true, Priority: 1,
				URLs:         model.ChannelURLs{{URL: "https://synthetic.invalid"}},
				ModelEntries: []model.ModelEntry{{Model: "invalid-cost-fixture"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			// This models an invalid persisted amount with otherwise valid usage.
			// The status must be honored by every monetary projection, while the
			// original amount remains available for a storage-level audit.
			if err := store.AddLog(ctx, &model.LogEntry{
				Time: model.JSONTime{Time: time.Now().Add(-time.Second)}, ChannelID: channel.ID,
				Model: "invalid-cost-fixture", StatusCode: http.StatusOK, AuthTokenID: 42,
				LogSource: model.LogSourceProxy, ClientProtocol: "openai", InputTokens: 10, OutputTokens: 5,
				Cost: 0.25, CostMultiplier: 2, CostSource: model.CostSourceLocal,
				CostMetadataVersion: 1, CostStatus: model.CostStatusInvalid, CostBasis: model.CostBasisUnknown,
			}); err != nil {
				t.Fatal(err)
			}
			c, w := newTestContext(t, newRequest(http.MethodGet, "/dashboard/?range=today&cost_status=invalid", nil))
			c.Set(webIdentityContextKey, WebIdentity{Role: model.WebRoleAPIToken, AuthTokenID: 42})
			handler.handle(server, c)
			if w.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			response := mustParseAPIResponse[struct {
				CostSummary *model.CostSummary                   `json:"cost_summary"`
				Totals      model.DashboardTotals                `json:"totals"`
				Stats       []model.StatsEntry                   `json:"stats"`
				Clients     map[string]model.ClientProtocolStats `json:"by_client_protocol"`
			}](t, w.Body.Bytes()).Data
			if response.CostSummary == nil || response.CostSummary.RecordedCostUSD != 0 || response.CostSummary.UnknownRequests != 1 {
				t.Fatalf("invalid amount entered the summary: %+v", response.CostSummary)
			}
			if response.Totals.Cost != 0 || response.Totals.EffectiveCost != 0 {
				t.Fatalf("invalid amount entered overview totals: %+v", response.Totals)
			}
			for _, entry := range response.Stats {
				if float64Value(entry.TotalCost) != 0 || float64Value(entry.EffectiveCost) != 0 || entry.Success != 1 || int64Value(entry.TotalInputTokens) != 10 {
					t.Fatalf("statistics diverged from summary or discarded usage: cost=%g effective=%g successful=%d input=%d", float64Value(entry.TotalCost), float64Value(entry.EffectiveCost), entry.Success, int64Value(entry.TotalInputTokens))
				}
				for _, point := range entry.HealthTimeline {
					if point.TotalCost != 0 || point.EffectiveCost != 0 {
						t.Fatalf("invalid amount entered health chart: %+v", point)
					}
				}
			}
			for _, entry := range response.Clients {
				if entry.TotalCost != 0 || entry.EffectiveCost != 0 {
					t.Fatalf("invalid amount entered protocol summary: %+v", entry)
				}
			}
			logsCtx, logsOut := newTestContext(t, newRequest(http.MethodGet, "/admin/logs?range=today&cost_status=invalid", nil))
			server.HandleErrors(logsCtx)
			logs := mustParseAPIResponse[[]struct {
				Status        string  `json:"cost_status"`
				EffectiveCost float64 `json:"effective_cost"`
			}](t, logsOut.Body.Bytes()).Data
			if len(logs) != 1 || logs[0].Status != model.CostStatusInvalid || logs[0].EffectiveCost != 0 {
				t.Fatalf("log monetary projection diverged from summary: %+v", logs)
			}
			metricsCtx, metricsOut := newTestContext(t, newRequest(http.MethodGet, "/admin/metrics?range=today&cost_status=invalid", nil))
			server.HandleMetrics(metricsCtx)
			points := mustParseAPIResponse[[]model.MetricPoint](t, metricsOut.Body.Bytes()).Data
			for _, point := range points {
				if float64Value(point.TotalCost) != 0 || float64Value(point.EffectiveCost) != 0 {
					t.Fatalf("invalid amount entered metric chart: %+v", point)
				}
			}
		})
	}
}
