package app

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"

	"github.com/gin-gonic/gin"
)

// HandleAdminDashboard returns the complete management-console overview in a
// single bounded response. Independent read queries run concurrently, while
// the existing stats cache absorbs repeated refreshes.
func (s *Server) HandleAdminDashboard(c *gin.Context) {
	params := ParsePaginationParams(c)
	if params.Range == "" {
		params.Range = "today"
	}
	startTime, endTime := params.GetTimeRange()
	filter := &model.LogFilter{LogSource: model.LogSourceProxy}
	ctx := c.Request.Context()

	var (
		stats       []model.StatsEntry
		clients     []model.ClientProtocolStats
		trend       []model.MetricPoint
		sites       []*model.Site
		accounts    []*model.SiteAccount
		bindings    []*model.SiteChannelBinding
		configs     []*model.Config
		unread      int
		statsErr    error
		clientsErr  error
		trendErr    error
		sitesErr    error
		accountsErr error
		bindingsErr error
		configsErr  error
		noticesErr  error
		wg          sync.WaitGroup
	)

	wg.Go(func() { stats, statsErr = s.statsCache.GetStatsLite(ctx, startTime, endTime, filter) })
	wg.Go(func() { clients, clientsErr = s.statsCache.GetClientProtocolStats(ctx, startTime, endTime, filter) })
	wg.Go(func() {
		trend, trendErr = s.store.AggregateRangeWithFilter(ctx, startTime, endTime, dashboardBucket(params.Range), filter)
		for i := range trend {
			trend[i].Channels = nil
		}
	})
	wg.Go(func() { sites, sitesErr = s.store.ListSites(ctx, model.SiteListFilter{}) })
	wg.Go(func() { accounts, accountsErr = s.store.ListSiteAccounts(ctx, 0, false) })
	wg.Go(func() { bindings, bindingsErr = s.store.ListSiteChannelBindings(ctx) })
	wg.Go(func() { configs, configsErr = s.store.ListConfigs(ctx) })
	wg.Go(func() {
		unreadOnly := true
		_, unread, noticesErr = s.store.ListSiteAnnouncements(ctx, model.SiteAnnouncementFilter{Unread: &unreadOnly, Limit: 1})
	})
	wg.Wait()

	for _, err := range []error{statsErr, clientsErr, trendErr, sitesErr, accountsErr, bindingsErr, configsErr, noticesErr} {
		if err != nil {
			RespondError(c, http.StatusInternalServerError, err)
			return
		}
	}

	snapshot := buildDashboardSnapshot(params.Range, startTime, endTime, stats, clients, trend, sites, accounts, bindings, configs)
	snapshot.UnreadNotices = unread
	RespondJSON(c, http.StatusOK, snapshot)
}

func dashboardBucket(rangeName string) time.Duration {
	switch rangeName {
	case "this_month", "last_month":
		return 24 * time.Hour
	case "this_week", "last_week":
		return 6 * time.Hour
	default:
		return time.Hour
	}
}

func buildDashboardSnapshot(
	rangeName string,
	startTime, endTime time.Time,
	stats []model.StatsEntry,
	clients []model.ClientProtocolStats,
	trend []model.MetricPoint,
	sites []*model.Site,
	accounts []*model.SiteAccount,
	bindings []*model.SiteChannelBinding,
	configs []*model.Config,
) model.DashboardSnapshot {
	snapshot := model.DashboardSnapshot{
		Range:        rangeName,
		StartsAt:     startTime.UnixMilli(),
		EndsAt:       endTime.UnixMilli(),
		GeneratedAt:  time.Now().UnixMilli(),
		Trend:        trend,
		Balances:     make([]model.DashboardBalance, 0),
		ModelUsage:   make([]model.DashboardUsage, 0),
		SiteUsage:    make([]model.DashboardSiteUsage, 0),
		ClientUsage:  make([]model.DashboardUsage, 0),
		SiteCount:    len(sites),
		AccountCount: len(accounts),
		ChannelCount: len(configs),
	}

	siteNames := make(map[int64]string, len(sites))
	sitePlatforms := make(map[int64]string, len(sites))
	for _, site := range sites {
		if site == nil {
			continue
		}
		siteNames[site.ID] = site.Name
		sitePlatforms[site.ID] = site.Platform
		if site.Enabled {
			snapshot.EnabledSites++
		}
	}
	for _, cfg := range configs {
		if cfg != nil && cfg.Enabled {
			snapshot.EnabledChannels++
		}
	}

	balanceMap := make(map[string]*model.DashboardBalance)
	accountSites := make(map[int64]int64, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		accountSites[account.ID] = account.SiteID
		if account.Enabled && account.Status == model.SiteAccountStatusHealthy {
			snapshot.HealthyAccounts++
		}
		if account.Balance == nil || !account.Enabled {
			continue
		}
		currency := strings.ToUpper(strings.TrimSpace(account.BalanceCurrency))
		if platform := sitePlatforms[account.SiteID]; platform == model.SitePlatformNewAPIFamily || platform == model.SitePlatformAnyRouter {
			currency = "USD"
		}
		if currency == "" {
			currency = "UNKNOWN"
		}
		balance := balanceMap[currency]
		if balance == nil {
			balance = &model.DashboardBalance{Currency: currency}
			balanceMap[currency] = balance
		}
		balance.Amount += *account.Balance
		balance.Accounts++
	}
	for _, balance := range balanceMap {
		snapshot.Balances = append(snapshot.Balances, *balance)
	}
	sort.Slice(snapshot.Balances, func(i, j int) bool { return snapshot.Balances[i].Currency < snapshot.Balances[j].Currency })

	channelSites := make(map[int64]int64, len(bindings))
	for _, binding := range bindings {
		if binding == nil || binding.ChannelID <= 0 {
			continue
		}
		if siteID := accountSites[binding.SiteAccountID]; siteID > 0 {
			channelSites[binding.ChannelID] = siteID
		}
	}

	modelUsage := make(map[string]*model.DashboardUsage)
	siteUsage := make(map[int64]*model.DashboardSiteUsage)
	for _, entry := range stats {
		inputTokens := int64Value(entry.TotalInputTokens)
		outputTokens := int64Value(entry.TotalOutputTokens)
		cacheRead := int64Value(entry.TotalCacheReadInputTokens)
		cacheCreate := int64Value(entry.TotalCacheCreationInputTokens)
		cost := float64Value(entry.TotalCost)
		effectiveCost := float64Value(entry.EffectiveCost)
		snapshot.Totals.Requests += entry.Total
		snapshot.Totals.Success += entry.Success
		snapshot.Totals.Errors += entry.Error
		snapshot.Totals.InputTokens += inputTokens
		snapshot.Totals.OutputTokens += outputTokens
		snapshot.Totals.CacheReadTokens += cacheRead
		snapshot.Totals.CacheCreationTokens += cacheCreate
		snapshot.Totals.Cost += cost
		snapshot.Totals.EffectiveCost += effectiveCost

		modelName := strings.TrimSpace(entry.Model)
		if modelName == "" {
			modelName = "未知模型"
		}
		usage := modelUsage[modelName]
		if usage == nil {
			usage = &model.DashboardUsage{Key: modelName, Label: modelName}
			modelUsage[modelName] = usage
		}
		addDashboardUsage(usage, entry.Total, entry.Success, entry.Error, inputTokens, outputTokens, effectiveCost)

		siteID := int64(0)
		if entry.ChannelID != nil {
			siteID = channelSites[int64(*entry.ChannelID)]
		}
		site := siteUsage[siteID]
		if site == nil {
			label := siteNames[siteID]
			if label == "" {
				label = "未绑定渠道"
			}
			site = &model.DashboardSiteUsage{DashboardUsage: model.DashboardUsage{Key: label, Label: label}, SiteID: siteID}
			siteUsage[siteID] = site
		}
		addDashboardUsage(&site.DashboardUsage, entry.Total, entry.Success, entry.Error, inputTokens, outputTokens, effectiveCost)
	}

	for _, usage := range modelUsage {
		snapshot.ModelUsage = append(snapshot.ModelUsage, *usage)
	}
	for _, usage := range siteUsage {
		snapshot.SiteUsage = append(snapshot.SiteUsage, *usage)
	}
	sortDashboardUsage(snapshot.ModelUsage)
	sort.Slice(snapshot.SiteUsage, func(i, j int) bool {
		return usageLess(snapshot.SiteUsage[i].DashboardUsage, snapshot.SiteUsage[j].DashboardUsage)
	})
	snapshot.ModelUsage = compactDashboardUsage(snapshot.ModelUsage, 12, "other-models", "其他模型")
	snapshot.SiteUsage = compactDashboardSiteUsage(snapshot.SiteUsage, 10)
	setUsageShares(snapshot.ModelUsage, snapshot.Totals)
	setSiteUsageShares(snapshot.SiteUsage, snapshot.Totals)

	for _, entry := range clients {
		usage := model.DashboardUsage{
			Key:           entry.ClientProtocol,
			Label:         dashboardClientLabel(entry.ClientProtocol),
			Requests:      entry.TotalRequests,
			Success:       entry.SuccessRequests,
			Errors:        entry.ErrorRequests,
			InputTokens:   entry.TotalInputTokens,
			OutputTokens:  entry.TotalOutputTokens,
			EffectiveCost: entry.EffectiveCost,
		}
		snapshot.ClientUsage = append(snapshot.ClientUsage, usage)
	}
	setUsageShares(snapshot.ClientUsage, snapshot.Totals)
	sortDashboardUsage(snapshot.ClientUsage)
	return snapshot
}

func addDashboardUsage(usage *model.DashboardUsage, requests, success, errors int, inputTokens, outputTokens int64, effectiveCost float64) {
	usage.Requests += requests
	usage.Success += success
	usage.Errors += errors
	usage.InputTokens += inputTokens
	usage.OutputTokens += outputTokens
	usage.EffectiveCost += effectiveCost
}

func setUsageShares(items []model.DashboardUsage, totals model.DashboardTotals) {
	for i := range items {
		items[i].Share = dashboardShare(items[i].EffectiveCost, items[i].Requests, totals)
	}
}

func setSiteUsageShares(items []model.DashboardSiteUsage, totals model.DashboardTotals) {
	for i := range items {
		items[i].Share = dashboardShare(items[i].EffectiveCost, items[i].Requests, totals)
	}
}

func dashboardShare(cost float64, requests int, totals model.DashboardTotals) float64 {
	if totals.EffectiveCost > 0 {
		return cost / totals.EffectiveCost
	}
	if totals.Requests > 0 {
		return float64(requests) / float64(totals.Requests)
	}
	return 0
}

func sortDashboardUsage(items []model.DashboardUsage) {
	sort.Slice(items, func(i, j int) bool { return usageLess(items[i], items[j]) })
}

func compactDashboardUsage(items []model.DashboardUsage, limit int, otherKey, otherLabel string) []model.DashboardUsage {
	if limit < 2 || len(items) <= limit {
		return items
	}
	result := append([]model.DashboardUsage(nil), items[:limit-1]...)
	other := model.DashboardUsage{Key: otherKey, Label: otherLabel}
	for _, item := range items[limit-1:] {
		addDashboardUsage(&other, item.Requests, item.Success, item.Errors, item.InputTokens, item.OutputTokens, item.EffectiveCost)
	}
	return append(result, other)
}

func compactDashboardSiteUsage(items []model.DashboardSiteUsage, limit int) []model.DashboardSiteUsage {
	if limit < 2 || len(items) <= limit {
		return items
	}
	result := append([]model.DashboardSiteUsage(nil), items[:limit-1]...)
	other := model.DashboardSiteUsage{
		DashboardUsage: model.DashboardUsage{Key: "other-sites", Label: "其他站点"},
		SiteID:         -1,
	}
	for _, item := range items[limit-1:] {
		addDashboardUsage(&other.DashboardUsage, item.Requests, item.Success, item.Errors, item.InputTokens, item.OutputTokens, item.EffectiveCost)
	}
	return append(result, other)
}

func usageLess(left, right model.DashboardUsage) bool {
	if left.EffectiveCost != right.EffectiveCost {
		return left.EffectiveCost > right.EffectiveCost
	}
	if left.Requests != right.Requests {
		return left.Requests > right.Requests
	}
	return left.Label < right.Label
}

func dashboardClientLabel(clientProtocol string) string {
	switch strings.ToLower(strings.TrimSpace(clientProtocol)) {
	case "anthropic":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "gemini":
		return "Gemini"
	case "openai":
		return "OpenAI"
	case "":
		return "其他工具"
	default:
		return clientProtocol
	}
}

func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func float64Value(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
