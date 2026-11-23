package app

import (
	"context"
	"log"
	"slices"
	"sort"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// routeSelectionSnapshot owns the request-local slices and immutable observations
// used to form a candidate pool. Only orderRouteSelection advances the balancer.
type routeSelectionSnapshot struct {
	now            time.Time
	aliases        *modelAliasRegistry
	cooldowns      channelCooldownSnapshot
	costs          map[int64]float64
	healthConfig   model.HealthScoreConfig
	healthStats    map[int64]model.ChannelHealthStats
	warnings       []string
	channels       []*model.Config
	sourceChannels []*model.Config
	universe       rrGroupKey
	poolMode       string
	requestModel   string
}

func (s *Server) loadRouteSelectionSnapshot(ctx context.Context, requestModel string) *routeSelectionSnapshot {
	view := &routeSelectionSnapshot{now: time.Now(), poolMode: "none", aliases: s.modelAliases.frozen(), requestModel: requestModel}
	var err error
	view.cooldowns.channels, err = s.getAllChannelCooldowns(ctx)
	if err != nil {
		view.cooldowns.channels = nil
		view.warnings = append(view.warnings, "channel_cooldown_unavailable")
		log.Print("[WARN] 路由渠道冷却观察不可用，沿用既有降级策略")
	}
	view.cooldowns.keys, err = s.getAllKeyCooldowns(ctx)
	if err != nil {
		view.cooldowns.keys = nil
		view.warnings = append(view.warnings, "key_cooldown_unavailable")
		log.Print("[WARN] 路由 Key 冷却观察不可用，沿用既有降级策略")
	}
	if requestModel != "" && requestModel != "*" {
		view.cooldowns.models, err = s.getAllModelCooldowns(ctx)
		if err != nil {
			view.cooldowns.models = nil
			view.warnings = append(view.warnings, "model_cooldown_unavailable")
			log.Print("[WARN] 路由模型冷却观察不可用，沿用既有降级策略")
		}
	}
	if s.costCache != nil {
		view.costs = s.costCache.snapshotAt(view.now)
	}
	if s.healthCache != nil {
		view.healthConfig = s.healthCache.Config()
		if stats := s.healthCache.healthStats.Load(); stats != nil {
			view.healthStats = *stats
		}
	}
	return view
}

func routeHealthStats(stats map[int64]model.ChannelHealthStats, channelID int64) model.ChannelHealthStats {
	if value, exists := stats[channelID]; exists {
		return value
	}
	return model.ChannelHealthStats{SuccessRate: 1}
}

func filterRouteCostLimits(channels []*model.Config, costs map[int64]float64) []*model.Config {
	if costs == nil {
		return channels
	}
	return filterConfigs(channels, func(cfg *model.Config) bool {
		return cfg.DailyCostLimit <= 0 || costs[cfg.ID] < cfg.DailyCostLimit
	})
}

// routeCandidatePool is shared by the proxy and diagnostics. Key model scopes,
// URL protocol capabilities and RPM/concurrency reservations belong to the later
// attempt stage and must not be silently removed from this pool.
func (s *Server) routeCandidatePool(view *routeSelectionSnapshot, channels []*model.Config, requestModel, requestProtocol string, allowFallback bool) ([]*model.Config, bool) {
	eligible := filterAvailableChannelsAt(channels, view.now)
	eligible = filterRouteCostLimits(eligible, view.costs)
	if len(eligible) == 0 {
		return nil, false
	}
	filtered := s.filterCooledChannelsAt(slices.Clone(eligible), requestModel, requestProtocol,
		view.cooldowns.channels, view.cooldowns.keys, view.cooldowns.models, view.now, view.aliases)
	if len(filtered) > 0 {
		return filtered, false
	}
	if !allowFallback || !s.cooldownFallbackEnabled() {
		return nil, false
	}
	best, _ := s.pickBestChannelFromSnapshot(eligible, requestModel, requestProtocol, view)
	if best == nil {
		return nil, false
	}
	return []*model.Config{cooldownFallbackCandidate(best)}, true
}

func (s *Server) collectModelRouteSelection(ctx context.Context, requestModel, requestProtocol string) (*routeSelectionSnapshot, error) {
	view := s.loadRouteSelectionSnapshot(ctx, requestModel)
	channels, err := s.getEnabledChannelsSnapshotByUnifiedModelWithAliases(ctx, requestModel, view.aliases)
	if err != nil {
		return nil, err
	}
	view.sourceChannels = channels
	view.universe = rrUniverseKey(channels)
	view.channels, _ = s.routeCandidatePool(view, channels, requestModel, requestProtocol, false)
	if len(view.channels) > 0 {
		view.poolMode = "exact"
		return view, nil
	}

	var fallback []*model.Config
	if requestModel != "*" {
		source, loadErr := s.getEnabledChannelsSnapshotByModel(ctx, "*")
		if loadErr != nil {
			return nil, loadErr
		}
		if len(source) == 0 {
			source, loadErr = s.store.ListConfigs(ctx)
			if loadErr != nil {
				return nil, loadErr
			}
		}
		fallback = filterConfigs(source, func(cfg *model.Config) bool {
			return cfg != nil && cfg.Enabled && configSupportsModel(cfg, requestModel, view.aliases)
		})
	}
	view.sourceChannels = fallback
	view.universe = rrUniverseKey(fallback)
	var probe bool
	view.channels, probe = s.routeCandidatePool(view, fallback, requestModel, requestProtocol, true)
	if probe {
		view.poolMode = "cooldown_fallback"
	} else if len(view.channels) > 0 {
		// Preserve the legacy API value; this is exact/explicit-alias/wildcard
		// fallback, never substring or model-family matching.
		view.poolMode = "fuzzy"
	}
	return view, nil
}

func (s *Server) orderRouteSelection(view *routeSelectionSnapshot, requestModel, stickyScope string) []*model.Config {
	channels := slices.Clone(view.channels)
	if len(channels) == 0 || view.poolMode == "cooldown_fallback" {
		return channels
	}
	if view.healthConfig.Enabled {
		channels = s.sortChannelsByHealthFromSnapshot(channels, view.cooldowns.keys, view.now, view.universe,
			view.healthConfig, view.healthStats)
	} else {
		channels = s.balanceSamePriorityChannels(channels, view.cooldowns.keys, view.now, view.universe)
	}
	if requestModel != "" && requestModel != "*" && s.routeStrategy() == RouteStrategySticky {
		channels = applyStickyPreference(channels, s.stickyRouter.preferred(stickyScope, requestModel, view.now))
	}
	return channels
}

func (s *Server) routeSelectionMedian(view *routeSelectionSnapshot) float64 {
	samples := make([]float64, 0, len(view.channels))
	for _, cfg := range view.channels {
		stats := routeHealthStats(view.healthStats, cfg.ID)
		if stats.FirstByteSampleCount > 0 && stats.AvgFirstByteSeconds > 0 {
			samples = append(samples, stats.AvgFirstByteSeconds)
		}
	}
	return medianFloat64(samples)
}

// diagnosticRouteOrder reports stable score order without consuming a SWRR turn.
func (s *Server) diagnosticRouteOrder(view *routeSelectionSnapshot) []*model.Config {
	channels := slices.Clone(view.channels)
	median := s.routeSelectionMedian(view)
	sort.SliceStable(channels, func(i, j int) bool {
		left, right := float64(channels[i].Priority), float64(channels[j].Priority)
		if view.healthConfig.Enabled {
			left = s.calculateEffectivePriority(channels[i], routeHealthStats(view.healthStats, channels[i].ID), view.healthConfig, median)
			right = s.calculateEffectivePriority(channels[j], routeHealthStats(view.healthStats, channels[j].ID), view.healthConfig, median)
		}
		if left != right {
			return left > right
		}
		return channels[i].ID < channels[j].ID
	})
	return channels
}
