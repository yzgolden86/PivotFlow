package app

import (
	"sort"
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/protocol"
)

// URL observations identify configuration positions, never origins, credentials,
// headers or proxy URLs. They cover the common text family of this client.
type RouteDiagnosticURL struct {
	Index      int        `json:"index"`
	Status     string     `json:"status"`
	Declared   bool       `json:"declared"`
	Protocols  []string   `json:"protocols"`
	Disabled   bool       `json:"disabled"`
	Cooling    bool       `json:"cooling"`
	Fallback   bool       `json:"fallback"`
	RetryAfter *time.Time `json:"retry_after,omitempty"`
}

type RouteDiagnosticCost struct {
	ChannelMultiplier  float64    `json:"channel_multiplier"`
	KeyMultiplierMin   *float64   `json:"key_multiplier_min"`
	KeyMultiplierMax   *float64   `json:"key_multiplier_max"`
	KeyMultiplierCount int        `json:"key_multiplier_count"`
	DailyUsedUSD       *float64   `json:"daily_used_usd"`
	DailyLimitUSD      float64    `json:"daily_limit_usd"`
	SiteBindingKnown   bool       `json:"site_binding_known"`
	SiteBound          bool       `json:"site_bound"`
	PriceStatus        string     `json:"price_status"`
	PriceCachedAt      *time.Time `json:"price_cached_at,omitempty"`
}

type routePriceObservation struct {
	status   string
	cachedAt time.Time
}

func (s *Server) routePricingSnapshot(now time.Time) map[int64]routePriceObservation {
	result := make(map[int64]routePriceObservation)
	if s.sitePricing == nil {
		return result
	}
	s.sitePricing.mu.RLock()
	defer s.sitePricing.mu.RUnlock()
	for id, entry := range s.sitePricing.entries {
		if entry == nil {
			continue
		}
		status := "cached"
		ttl := sitePricingTTL
		if entry.failed {
			status = "unavailable"
			ttl = sitePricingFailureTTL
		}
		if !entry.fetchedAt.Add(ttl).After(now) {
			status = "expired"
		}
		if status == "cached" && len(entry.pricing.Models) == 0 {
			status = "empty"
		}
		result[id] = routePriceObservation{status: status, cachedAt: entry.fetchedAt}
	}
	return result
}

func routeDiagnosticRelated(cfg *model.Config, names []string) bool {
	for _, entry := range cfg.ModelEntries {
		if entry.Model == "*" {
			return true
		}
		for _, name := range names {
			if strings.EqualFold(entry.Model, name) {
				return true
			}
		}
	}
	return false
}

func routeStickyForwardStatus(preferred *model.Config, view *routeSelectionSnapshot, s *Server) string {
	if view.poolMode == "cooldown_fallback" {
		return "cooldown_fallback"
	}
	ordered := s.diagnosticRouteOrder(view)
	if len(ordered) == 0 {
		return "not_candidate"
	}
	firstBucket := int64(ordered[0].Priority)
	if view.healthConfig.Enabled {
		firstBucket = effPriorityBucket(s.calculateEffectivePriority(ordered[0], routeHealthStats(view.healthStats, ordered[0].ID), view.healthConfig, s.routeSelectionMedian(view)))
	}
	matching, other := false, false
	for _, cfg := range ordered {
		bucket := int64(cfg.Priority)
		if view.healthConfig.Enabled {
			bucket = effPriorityBucket(s.calculateEffectivePriority(cfg, routeHealthStats(view.healthStats, cfg.ID), view.healthConfig, s.routeSelectionMedian(view)))
		}
		if bucket != firstBucket {
			break
		}
		if cfg.Priority == preferred.Priority {
			matching = true
		} else {
			other = true
		}
	}
	if !matching {
		return "base_priority_mismatch"
	}
	if other {
		return "conditional_on_balanced_first"
	}
	return "eligible"
}

func (s *Server) observeRouteAttempt(state *routeDiagnosticState, view *routeSelectionSnapshot, client string,
	capabilities map[protocolCapabilityKey]protocolCapabilityEntry, bindings map[int64]channelBinding, bindingsKnown bool, prices map[int64]routePriceObservation) {
	cfg := state.cfg
	d := &state.diagnostic
	d.AvailableTimeStart = cfg.AvailableTimeStart
	d.AvailableTimeEnd = cfg.AvailableTimeEnd
	if !state.channelCooldown.IsZero() {
		until := state.channelCooldown
		d.ChannelCooldownUntil = &until
	}
	if !state.modelCooldown.IsZero() {
		until := state.modelCooldown
		d.ModelCooldownUntil = &until
	}
	d.Cost = RouteDiagnosticCost{ChannelMultiplier: cfg.CostMultiplier, DailyLimitUSD: cfg.DailyCostLimit, SiteBindingKnown: bindingsKnown, PriceStatus: "not_bound"}
	if view.costs != nil {
		used := view.costs[cfg.ID]
		d.Cost.DailyUsedUSD = &used
	}
	if !bindingsKnown {
		d.Cost.PriceStatus = "unknown"
	}
	if binding, ok := bindings[cfg.ID]; ok {
		d.Cost.SiteBound = true
		d.Cost.PriceStatus = "not_cached"
		if price, found := prices[binding.siteID]; found {
			d.Cost.PriceStatus = price.status
			at := price.cachedAt
			d.Cost.PriceCachedAt = &at
		}
	}
	for _, key := range state.keys {
		if key == nil || key.Disabled || !key.AllowsModel(view.requestModel) {
			continue
		}
		value := key.CostMultiplier
		if value < 0 {
			value = cfg.CostMultiplier
		}
		if d.Cost.KeyMultiplierMin == nil || value < *d.Cost.KeyMultiplierMin {
			v := value
			d.Cost.KeyMultiplierMin = &v
		}
		if d.Cost.KeyMultiplierMax == nil || value > *d.Cost.KeyMultiplierMax {
			v := value
			d.Cost.KeyMultiplierMax = &v
		}
		d.Cost.KeyMultiplierCount++
	}
	d.URLCount = len(cfg.URLs)
	d.URLs = []RouteDiagnosticURL{}
	urlStats := map[urlKey]urlCooldownState{}
	disabled := map[urlKey]bool{}
	if s.urlSelector != nil {
		s.urlSelector.mu.RLock()
		for _, entry := range cfg.URLs {
			key := urlKey{channelID: cfg.ID, url: entry.RuntimeURL()}
			urlStats[key] = s.urlSelector.cooldowns[key]
			disabled[key] = s.urlSelector.disabled[key]
		}
		s.urlSelector.mu.RUnlock()
	}
	allDisabled := len(cfg.URLs) > 1
	for _, entry := range cfg.URLs {
		if !disabled[urlKey{channelID: cfg.ID, url: entry.RuntimeURL()}] {
			allDisabled = false
		}
	}
	possible := make(map[string]struct{})
	for index, entry := range cfg.URLs {
		candidates, declared := protocolCandidatesForURL(entry, cfg.GetProtocolTransformMode(), protocol.Protocol(client), channelTestRequestFamily(protocol.Protocol(client)), localUpstreamProtocolOrder(cfg.URLs))
		obs := RouteDiagnosticURL{Index: index, Declared: declared, Status: "declared", Protocols: []string{}}
		if !declared {
			obs.Status = "unobserved"
		}
		if cfg.GetProtocolTransformMode() == model.ProtocolTransformModeAuto && !declared {
			key := protocolCapabilityKey{channelID: cfg.ID, baseURL: entry.RuntimeURL(), clientProtocol: protocol.Protocol(client), requestFamily: channelTestRequestFamily(protocol.Protocol(client))}
			if cached, known := capabilities[key]; known {
				if cached.upstream == protocolUnsupported {
					candidates = nil
					obs.Status = "unsupported"
					at := cached.retryAfter
					obs.RetryAfter = &at
				} else {
					candidates = prioritizeProtocolCandidate(candidates, cached.upstream)
					obs.Status = "learned"
				}
			}
		}
		key := urlKey{channelID: cfg.ID, url: entry.RuntimeURL()}
		obs.Disabled = disabled[key]
		obs.Cooling = urlStats[key].until.After(view.now)
		// Forwarding retains cooling URLs for later attempts, and falls back to
		// all URLs if all are disabled. A single URL also bypasses disable sorting.
		obs.Fallback = obs.Disabled && (allDisabled || len(cfg.URLs) == 1)
		usable := !obs.Disabled || obs.Fallback
		for _, candidate := range candidates {
			obs.Protocols = append(obs.Protocols, string(candidate))
			if usable {
				possible[resolveFinalUpstreamModelWithAliases(cfg, view.requestModel, string(candidate), view.aliases)] = struct{}{}
			}
		}
		if len(candidates) == 0 && obs.Status != "unsupported" {
			obs.Status = "incompatible"
		}
		if usable && len(candidates) > 0 {
			d.CompatibleURLCount++
		}
		if index < 8 {
			d.URLs = append(d.URLs, obs)
		}
	}
	state.protocolMissing = d.CompatibleURLCount == 0
	d.PossibleUpstreamModels = []string{}
	for name := range possible {
		d.PossibleUpstreamModels = append(d.PossibleUpstreamModels, name)
	}
	sort.Strings(d.PossibleUpstreamModels)
	d.PossibleUpstreamModelCount = len(d.PossibleUpstreamModels)
	d.PossibleUpstreamModels = d.PossibleUpstreamModels[:min(len(d.PossibleUpstreamModels), 8)]
	state.credentialUnknown = cfg.UsesOAuth()
}

func finishRouteAttemptObservation(state *routeDiagnosticState, admission routeAdmissionObservation, view *routeSelectionSnapshot) {
	d := &state.diagnostic
	d.AttemptStatus = "ready"
	block := func(stage string) { d.AttemptStatus = "blocked"; d.BlockedStage = stage }
	if !d.Candidate {
		block("candidate")
	}
	if admission.view.Admission == "blocked" {
		block("admission")
	}
	if d.AttemptStatus != "blocked" && admission.view.Admission == "unknown" {
		d.AttemptStatus = "unknown"
		d.BlockedStage = "admission"
	}
	if d.Candidate && d.AttemptStatus != "blocked" && (d.ModelEligibleKeyCount == 0 || (d.ActiveKeyCount == 0 && view.poolMode != "cooldown_fallback") || state.protocolMissing || state.rpmExceeded || state.concurrencyFull) {
		block("attempt")
	}
	if d.AttemptStatus == "ready" && state.credentialUnknown {
		d.AttemptStatus = "unknown"
		d.BlockedStage = "attempt"
	}
	if d.AttemptStatus == "ready" && (len(view.warnings) > 0 && (cfgNeedsCostObservation(state.cfg, view) || hasRouteGateWarning(view.warnings))) {
		d.AttemptStatus = "unknown"
		d.BlockedStage = "candidate"
	}
	if d.AttemptStatus == "ready" && view.poolMode == "cooldown_fallback" {
		d.AttemptStatus = "probe"
	}
	d.Attemptable = d.AttemptStatus == "ready" || d.AttemptStatus == "probe"
	if admission.view.Admission != "allowed" {
		d.Reasons = append(d.Reasons, admission.view.Reasons...)
	}
	if state.credentialUnknown {
		d.Reasons = append(d.Reasons, RouteDiagnosticReason{Code: "oauth_credential_unverified", Message: "OAuth 凭证需要在实际尝试时解密并检查有效期；本页不解密或刷新凭证。", Stage: "attempt"})
	}
}

func cfgNeedsCostObservation(cfg *model.Config, view *routeSelectionSnapshot) bool {
	return cfg.DailyCostLimit > 0 && view.costs == nil
}
func hasRouteGateWarning(warnings []string) bool {
	for _, warning := range warnings {
		if warning == "channel_cooldown_unavailable" || warning == "key_cooldown_unavailable" || warning == "model_cooldown_unavailable" {
			return true
		}
	}
	return false
}
