package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func TestCostSemanticsFilterPreservesValuesAndTokenScope(t *testing.T) {
	for _, tc := range []struct {
		query, source, status, basis string
	}{
		{"cost_source=%20SITE_PRICING%20&cost_status=PARTIAL_ESTIMATE&cost_basis=Mixed", model.CostSourceSite, model.CostStatusPartialEstimate, model.CostBasisMixed},
		{"cost_source=unsupported&cost_status=unsupported&cost_basis=unsupported", "unsupported", "unsupported", "unsupported"},
	} {
		c, _ := newTestContext(t, newRequest(http.MethodGet, "/dashboard/logs?auth_token_id=99&"+tc.query, nil))
		c.Set(webIdentityContextKey, WebIdentity{Role: model.WebRoleAPIToken, AuthTokenID: 42})
		filter := BuildLogFilter(c)
		if filter.CostSource != tc.source || filter.CostStatus != tc.status || filter.CostBasis != tc.basis {
			t.Fatalf("cost filters dropped or changed: %+v", filter)
		}
		if filter.AuthTokenID == nil || *filter.AuthTokenID != 42 {
			t.Fatalf("cost query escaped token ownership: %+v", filter)
		}
	}
}

func TestCostSemanticsCacheKeyIncludesAllFiltersAndBoundaries(t *testing.T) {
	filters := []*model.LogFilter{
		{}, {Search: "probe"}, {RequestID: "probe"},
		{CostSource: model.CostSourceSite}, {CostStatus: model.CostStatusUsageMissing}, {CostBasis: model.CostBasisMixed},
		{Model: "gptmodel_like:claude"}, {Model: "gpt", ModelLike: "claude"},
	}
	seen := map[string]bool{}
	for _, filter := range filters {
		key := hashFilter(filter)
		if seen[key] {
			t.Fatalf("distinct result scopes share a cache key: %+v", filter)
		}
		seen[key] = true
	}
}

func TestCostSemanticsCacheKeyPreservesMillisecondWindows(t *testing.T) {
	start, end := time.Unix(1000, 10_000_000), time.Unix(2000, 10_000_000)
	base := buildCacheKey("cost_summary", start, end, nil)
	if base == buildCacheKey("cost_summary", start.Add(time.Millisecond), end, nil) ||
		base == buildCacheKey("cost_summary", start, end.Add(time.Millisecond), nil) {
		t.Fatal("distinct millisecond SQL windows share a cost summary cache key")
	}
}

func TestCostSemanticsRecentCacheWindowsPreserveSQLScope(t *testing.T) {
	ctx := context.Background()
	store, err := storage.CreateSQLiteStore(t.TempDir() + "/recent-cost-windows.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	cache := NewStatsCache(store)
	defer cache.Close()
	channel, err := store.CreateConfig(ctx, &model.Config{
		Name: "recent-window-fixture", Enabled: true, Priority: 1,
		URLs: model.ChannelURLs{{URL: "https://synthetic.invalid"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Both fixed end times fall inside the same recent 30-second TTL bucket.
	end := time.Now().Truncate(time.Minute).Add(-time.Minute + 5*time.Second)
	start := end.Add(-time.Hour)
	addLog := func(at time.Time) {
		t.Helper()
		if err := store.AddLog(ctx, &model.LogEntry{
			Time: model.JSONTime{Time: at}, ChannelID: channel.ID, Model: "cost-fixture",
			LogSource: model.LogSourceProxy, StatusCode: http.StatusOK,
			Cost: 0.25, CostMultiplier: 1, CostSource: model.CostSourceSite,
			CostMetadataVersion: model.CostMetadataVersion,
			CostStatus:          model.CostStatusEstimated, CostBasis: model.CostBasisToken,
		}); err != nil {
			t.Fatal(err)
		}
	}
	addLog(end.Add(-time.Millisecond))
	addLog(end.Add(time.Millisecond))
	first, err := cache.GetCostSummary(ctx, start, end, nil)
	if err != nil || first == nil || first.SuccessfulRequests != 1 || first.RecordedCostUSD != 0.25 {
		t.Fatalf("first fixed window summary=%+v error=%v", first, err)
	}
	laterEnd := end.Add(2 * time.Millisecond)
	second, err := cache.GetCostSummary(ctx, start, laterEnd, nil)
	if err != nil || second == nil || second.SuccessfulRequests != 2 || second.RecordedCostUSD != 0.5 {
		t.Fatalf("recent fixed windows reused another SQL scope: summary=%+v error=%v", second, err)
	}
	// Identical windows retain ordinary TTL cache reuse.
	addLog(end)
	repeated, err := cache.GetCostSummary(ctx, start, laterEnd, nil)
	if err != nil || repeated == nil || repeated.SuccessfulRequests != 2 || repeated.RecordedCostUSD != 0.5 {
		t.Fatalf("identical fixed window no longer reused its cache: summary=%+v error=%v", repeated, err)
	}
}

type costSummaryReader interface {
	GetCostSummary(context.Context, time.Time, time.Time, *model.LogFilter) (*model.CostSummary, error)
}

type costSummaryProbeStore struct {
	storage.Store
	mu      sync.Mutex
	summary *model.CostSummary
	err     error
	calls   int
}

func (s *costSummaryProbeStore) GetCostSummary(context.Context, time.Time, time.Time, *model.LogFilter) (*model.CostSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.summary, s.err
}

func TestCostSemanticsSummaryCacheCopiesAndDoesNotCacheFailures(t *testing.T) {
	ctx := context.Background()
	start, end := time.Unix(1000, 0), time.Unix(2000, 0)
	probe := &costSummaryProbeStore{summary: &model.CostSummary{Version: 1, RecordedCostUSD: 0.25}}
	cache := NewStatsCache(probe)
	defer cache.Close()
	reader, ok := any(cache).(costSummaryReader)
	if !ok {
		t.Fatal("StatsCache does not provide the scoped cost summary")
	}
	first, err := reader.GetCostSummary(ctx, start, end, nil)
	if err != nil {
		t.Fatal(err)
	}
	first.RecordedCostUSD = 99
	probe.summary.RecordedCostUSD = 55
	second, err := reader.GetCostSummary(ctx, start, end, nil)
	if err != nil || second.RecordedCostUSD != 0.25 || probe.calls != 1 {
		t.Fatalf("cache shared a mutable summary: summary=%+v calls=%d error=%v", second, probe.calls, err)
	}
	probe.err = errors.New("unavailable")
	filter := &model.LogFilter{CostStatus: model.CostStatusHistoricalUnknown}
	if _, err := reader.GetCostSummary(ctx, start, end, filter); err == nil {
		t.Fatal("failed summary query was presented as valid")
	}
	probe.err = nil
	if _, err := reader.GetCostSummary(ctx, start, end, filter); err != nil || probe.calls != 3 {
		t.Fatalf("failed summary was cached: calls=%d error=%v", probe.calls, err)
	}
	probe.summary = nil
	if _, err := reader.GetCostSummary(ctx, start, end, &model.LogFilter{RequestID: "missing"}); err == nil {
		t.Fatal("missing summary was presented as complete zero coverage")
	}
}

func TestCostSemanticsCachedStatsRemainImmutableAcrossResponses(t *testing.T) {
	start, end := time.Unix(1000, 0), time.Unix(2000, 0)
	for _, kind := range []string{"stats", "stats_lite"} {
		t.Run(kind, func(t *testing.T) {
			cache := NewStatsCache(nil)
			defer cache.Close()
			amount := 0.25
			cache.storeCache(buildCacheKey(kind, start, end, nil), &cachedStats{
				data:   []model.StatsEntry{{Total: 1, TotalCost: &amount, HealthTimeline: []model.HealthPoint{{SuccessRate: 50}}}},
				expiry: time.Now().Add(time.Hour),
			})
			read := func() []model.StatsEntry {
				t.Helper()
				var result []model.StatsEntry
				var err error
				if kind == "stats" {
					result, err = cache.GetStats(context.Background(), start, end, nil, false)
				} else {
					result, err = cache.GetStatsLite(context.Background(), start, end, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			first := read()
			first[0].Total = 99
			*first[0].TotalCost = 99
			first[0].HealthTimeline[0].SuccessRate = 99
			second := read()
			if second[0].Total != 1 || *second[0].TotalCost != 0.25 || second[0].HealthTimeline[0].SuccessRate != 50 {
				t.Fatalf("response mutation contaminated cached statistics: %+v", second[0])
			}
		})
	}
}

func TestCostSemanticsSummaryCacheCapacityBoundsDistinctFilters(t *testing.T) {
	cache := NewStatsCache(nil)
	defer cache.Close()
	for i := range maxCacheEntries + 8 {
		cache.storeCache(fmt.Sprintf("cost_summary:%d", i), &cachedStats{data: &model.CostSummary{}, expiry: time.Now().Add(time.Hour)})
	}
	entries := 0
	cache.cache.Range(func(_, _ any) bool { entries++; return true })
	if entries > maxCacheEntries || cache.entryCount.Load() > maxCacheEntries {
		t.Fatalf("distinct cost filters exceeded the shared cache budget: rows=%d count=%d limit=%d", entries, cache.entryCount.Load(), maxCacheEntries)
	}
}

func costSemanticsHandlers() []struct {
	name   string
	handle func(*Server, *gin.Context)
} {
	return []struct {
		name   string
		handle func(*Server, *gin.Context)
	}{
		{"stats", (*Server).HandleStats},
		{"overview", (*Server).HandleAdminDashboard},
		{"token summary", (*Server).HandleDashboardSummary},
	}
}

func TestCostSemanticsAPISummaryMatchesFiltersAndTokenOwnership(t *testing.T) {
	for _, handler := range costSemanticsHandlers() {
		t.Run(handler.name, func(t *testing.T) {
			server, store, cleanup := setupAdminTestServer(t)
			defer cleanup()
			ctx := context.Background()
			channel, err := store.CreateConfig(ctx, &model.Config{
				Name: "cost-fixture", Enabled: true, Priority: 1,
				URLs:         model.ChannelURLs{{URL: "https://synthetic.invalid"}},
				ModelEntries: []model.ModelEntry{{Model: "cost-fixture"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			base := model.LogEntry{
				Time: model.JSONTime{Time: time.Now().Add(-time.Second)}, ChannelID: channel.ID,
				Model: "cost-fixture", Message: "cost probe", StatusCode: http.StatusOK,
				AuthTokenID: 42, LogSource: model.LogSourceProxy, RequestID: "cost-target",
				InputTokens: 100, Cost: 0.25, CostMultiplier: 7, ClientProtocol: "openai",
				CostMetadataVersion: 1, CostSource: model.CostSourceSite,
				CostStatus: model.CostStatusPartialEstimate, CostBasis: model.CostBasisMixed,
			}
			for i := range 8 {
				row := base
				switch i {
				case 1:
					row.AuthTokenID, row.Cost = 99, 99
				case 2:
					row.CostSource = model.CostSourceLocal
				case 3:
					row.CostStatus = model.CostStatusEstimated
				case 4:
					row.CostBasis = model.CostBasisToken
				case 5:
					row.RequestID = "different-request"
				case 6:
					row.Message = "not selected"
				case 7:
					row.LogSource = model.LogSourceManualTest
				}
				if err := store.AddLog(ctx, &row); err != nil {
					t.Fatal(err)
				}
			}
			query := "?range=today&auth_token_id=99&cost_source=site_pricing&cost_status=partial_estimate&cost_basis=mixed&request_id=cost-target&search=cost+probe&log_source=all"
			c, w := newTestContext(t, newRequest(http.MethodGet, "/dashboard/"+query, nil))
			c.Set(webIdentityContextKey, WebIdentity{Role: model.WebRoleAPIToken, AuthTokenID: 42})
			handler.handle(server, c)
			if w.Code != http.StatusOK {
				t.Fatalf("summary status=%d body=%s", w.Code, w.Body.String())
			}
			response := mustParseAPIResponse[struct {
				CostSummary *model.CostSummary    `json:"cost_summary"`
				Totals      model.DashboardTotals `json:"totals"`
				Stats       []model.StatsEntry    `json:"stats"`
				Requests    int                   `json:"total_requests"`
			}](t, w.Body.Bytes()).Data
			summary := response.CostSummary
			if summary == nil || summary.Version != 1 || summary.Currency != "USD" || summary.Reconciliation != "not_reconciled" ||
				summary.SuccessfulRequests != 1 || summary.PartialRequests != 1 || summary.UnknownRequests != 1 ||
				summary.SitePricingRequests != 1 || summary.LocalEstimateRequests != 0 || summary.MixedRequests != 1 ||
				summary.RecordedCostUSD != 0.25 || summary.PartialCostUSD != 0.25 {
				t.Fatalf("summary escaped its request scope or invented completeness: %+v", summary)
			}
			switch handler.name {
			case "stats":
				if len(response.Stats) != 1 || response.Stats[0].Total != 1 {
					t.Fatalf("stats did not share cost filters: %+v", response.Stats)
				}
			case "overview":
				if response.Totals.Requests != 1 {
					t.Fatalf("overview did not share cost filters: %+v", response.Totals)
				}
			default:
				if response.Requests != 1 {
					t.Fatalf("token summary did not share cost filters: %d", response.Requests)
				}
			}
		})
	}
}

func TestCostSemanticsAPIRejectsSummaryFailureWithoutDatabaseDetails(t *testing.T) {
	for _, handler := range costSemanticsHandlers() {
		t.Run(handler.name, func(t *testing.T) {
			server, store, cleanup := setupAdminTestServer(t)
			defer cleanup()
			probe := &costSummaryProbeStore{Store: store, err: errors.New("private_database_cost_query_failed")}
			cache := NewStatsCache(probe)
			defer cache.Close()
			server.store, server.statsCache = probe, cache
			c, w := newTestContext(t, newRequest(http.MethodGet, "/dashboard/?range=today", nil))
			handler.handle(server, c)
			if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "private_database") {
				t.Fatalf("failed summary was hidden or exposed database details: status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestCostSemanticsDashboardSummaryUsesFiltersWithoutWebIdentity(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	if err := store.AddLog(context.Background(), &model.LogEntry{
		Time: model.JSONTime{Time: time.Now().Add(-time.Second)}, ChannelID: 1,
		Model: "summary-filter-fixture", LogSource: model.LogSourceProxy, StatusCode: http.StatusOK,
		Cost: 0.25, CostMultiplier: 1, CostMetadataVersion: 1, CostStatus: model.CostStatusEstimated,
	}); err != nil {
		t.Fatal(err)
	}
	c, w := newTestContext(t, newRequest(http.MethodGet, "/dashboard/summary?range=today&cost_status=not_charged", nil))
	server.HandleDashboardSummary(c)
	response := mustParseAPIResponse[struct {
		CostSummary *model.CostSummary `json:"cost_summary"`
		Requests    int                `json:"total_requests"`
	}](t, w.Body.Bytes()).Data
	if response.CostSummary == nil || response.CostSummary.SuccessfulRequests != 0 || response.Requests != 0 {
		t.Fatalf("legacy summary ignored its filter: summary=%+v requests=%d", response.CostSummary, response.Requests)
	}
}
