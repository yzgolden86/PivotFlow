package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/yzgolden86/PivotFlow/internal/model"

	"github.com/gin-gonic/gin"
)

// RouteDiagnosticReason explains one routing gate in user-facing language.
// Blocking reasons may apply at admission, candidate or attempt stage.
type RouteDiagnosticReason struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
	Stage    string `json:"stage"`
}

// ChannelRouteDiagnostic is a read-only snapshot of a channel's current route
// eligibility. It deliberately does not advance the smooth round-robin state.
type ChannelRouteDiagnostic struct {
	ChannelID                  int64                   `json:"channel_id"`
	ChannelName                string                  `json:"channel_name"`
	Enabled                    bool                    `json:"enabled"`
	BasePriority               int                     `json:"base_priority"`
	EffectivePriority          float64                 `json:"effective_priority"`
	SuccessRate                float64                 `json:"success_rate"`
	HealthSampleCount          int64                   `json:"health_sample_count"`
	ExactModelMatch            bool                    `json:"exact_model_match"`
	FuzzyModelMatch            bool                    `json:"fuzzy_model_match"`
	ModelEligibleKeyCount      int                     `json:"model_eligible_key_count"`
	ActiveKeyCount             int                     `json:"active_key_count"`
	EnabledKeyCount            int                     `json:"enabled_key_count"`
	RoutingWeight              int                     `json:"routing_weight"`
	Attemptable                bool                    `json:"attemptable"`
	AttemptStatus              string                  `json:"attempt_status"`
	BlockedStage               string                  `json:"blocked_stage,omitempty"`
	CredentialKind             string                  `json:"credential_kind"`
	FailurePenalty             float64                 `json:"failure_penalty"`
	TTFBPenalty                float64                 `json:"ttfb_penalty"`
	TTFBSampleCount            int64                   `json:"ttfb_sample_count"`
	AverageTTFBSeconds         float64                 `json:"average_ttfb_seconds"`
	URLCount                   int                     `json:"url_count"`
	CompatibleURLCount         int                     `json:"compatible_url_count"`
	URLs                       []RouteDiagnosticURL    `json:"urls"`
	PossibleUpstreamModels     []string                `json:"possible_upstream_models"`
	PossibleUpstreamModelCount int                     `json:"possible_upstream_model_count"`
	Cost                       RouteDiagnosticCost     `json:"cost"`
	AvailableTimeStart         string                  `json:"available_time_start"`
	AvailableTimeEnd           string                  `json:"available_time_end"`
	ChannelCooldownUntil       *time.Time              `json:"channel_cooldown_until,omitempty"`
	ModelCooldownUntil         *time.Time              `json:"model_cooldown_until,omitempty"`
	ActualKnown                bool                    `json:"actual_known"`
	RPMLimit                   int                     `json:"rpm_limit"`
	CurrentRPM                 int                     `json:"current_rpm"`
	MaxConcurrency             int                     `json:"max_concurrency"`
	ActiveConcurrency          int                     `json:"active_concurrency"`
	Candidate                  bool                    `json:"candidate"`
	CandidatePosition          int                     `json:"candidate_position,omitempty"`
	HigherPriorityCount        int                     `json:"higher_priority_count"`
	SamePriorityCount          int                     `json:"same_priority_count"`
	EstimatedTrafficShare      float64                 `json:"estimated_traffic_share"`
	ActualRequests             int64                   `json:"actual_requests"`
	ActualShare                float64                 `json:"actual_share"`
	Reasons                    []RouteDiagnosticReason `json:"reasons"`
}

// RouteDiagnosticSticky is a read-only snapshot of the current sticky-route
// preference. It reports what the router remembers, not a prediction.
type RouteDiagnosticSticky struct {
	ChannelID       int64     `json:"channel_id"`
	ChannelName     string    `json:"channel_name"`
	RememberedAt    time.Time `json:"remembered_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	InCandidatePool bool      `json:"in_candidate_pool"`
	ForwardStatus   string    `json:"forward_status"`
}

type RouteDiagnosticResponse struct {
	Model               string                   `json:"model"`
	ClientProtocol      string                   `json:"client_protocol"`
	TokenID             int64                    `json:"token_id,omitempty"`
	RouteStrategy       string                   `json:"route_strategy"`
	PoolMode            string                   `json:"pool_mode"`
	HealthScoreEnabled  bool                     `json:"health_score_enabled"`
	Target              ChannelRouteDiagnostic   `json:"target"`
	Candidates          []ChannelRouteDiagnostic `json:"candidates"`
	Sticky              *RouteDiagnosticSticky   `json:"sticky,omitempty"`
	ActualWindow        string                   `json:"actual_window"`
	ActualTotalRequests int64                    `json:"actual_total_requests"`
	Summary             []string                 `json:"summary"`
	ObservedAt          time.Time                `json:"observed_at"`
	Warnings            []string                 `json:"warnings"`
	Related             []ChannelRouteDiagnostic `json:"-"`
	Token               RouteDiagnosticToken     `json:"token"`
	Policy              ModelRoutePolicy         `json:"-"`
	CanonicalModel      string                   `json:"-"`
	GroupNames          []string                 `json:"-"`
	GroupNameCount      int                      `json:"-"`
	ModelSummary        []string                 `json:"-"`
}

type routeDiagnosticState struct {
	cfg                *model.Config
	diagnostic         ChannelRouteDiagnostic
	channelCooldown    time.Time
	modelCooldown      time.Time
	costExceeded       bool
	tokenRejected      bool
	rpmExceeded        bool
	concurrencyFull    bool
	outsideTime        bool
	keys               []*model.APIKey
	protocolMissing    bool
	credentialUnknown  bool
	explicitGroupMatch bool
}

// HandleChannelRouteDiagnostics explains why a channel is or is not currently
// eligible for a model. The endpoint is intentionally read-only: opening it
// must never consume a round-robin turn or send an upstream request.
// GET /admin/channels/:id/route-diagnostics?model=...&client_protocol=openai&token_id=...
func (s *Server) HandleChannelRouteDiagnostics(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid channel id")
		return
	}
	modelName, clientProtocol, tokenID, err := parseModelRouteScope(c)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}

	result, err := s.buildChannelRouteDiagnostics(c.Request.Context(), id, modelName, clientProtocol, tokenID)
	if err != nil {
		respondModelRouteError(c, err)
		return
	}
	RespondJSON(c, http.StatusOK, result)
}

var errRouteDiagnosticChannelNotFound = errors.New("channel not found")

func (s *Server) buildChannelRouteDiagnostics(
	ctx context.Context,
	targetID int64,
	modelName string,
	clientProtocol string,
	tokenID int64,
) (RouteDiagnosticResponse, error) {
	return s.buildRouteDiagnostics(ctx, targetID, modelName, clientProtocol, tokenID)
}

// buildRouteDiagnostics is the common model evaluation used by the workspace,
// channel projection and system read API. targetID zero requests the model view.
func (s *Server) buildRouteDiagnostics(ctx context.Context, targetID int64, modelName, clientProtocol string, tokenID int64) (RouteDiagnosticResponse, error) {
	cfgs, err := s.store.ListConfigs(ctx)
	if err != nil {
		return RouteDiagnosticResponse{}, err
	}

	allKeys, err := s.store.GetAllAPIKeys(ctx)
	if err != nil {
		return RouteDiagnosticResponse{}, err
	}
	if allKeys == nil {
		allKeys = make(map[int64][]*model.APIKey)
	}
	if cache := s.getChannelCache(); cache != nil {
		for id, keys := range cache.ReadOnlyAPIKeySnapshot() {
			allKeys[id] = keys
		}
	}
	view, err := s.collectModelRouteSelection(ctx, modelName, clientProtocol)
	if err != nil {
		return RouteDiagnosticResponse{}, err
	}
	now := view.now
	runtimeConfigs := make(map[int64]*model.Config, len(view.sourceChannels))
	for _, cfg := range view.sourceChannels {
		runtimeConfigs[cfg.ID] = cfg
	}
	for _, cfg := range view.channels {
		runtimeConfigs[cfg.ID] = cfg
	}

	admission, err := s.observeRouteAdmission(ctx, tokenID, modelName, clientProtocol, view)
	if err != nil {
		return RouteDiagnosticResponse{}, err
	}
	tokenHasRestriction := admission.restriction.Restricted()
	capabilities := s.protocolCapabilities.snapshotAt(now)
	bindings, bindingErr := s.loadChannelBindings(ctx)
	if bindingErr != nil {
		view.warnings = append(view.warnings, "pricing_binding_unavailable")
	}
	prices := s.routePricingSnapshot(now)
	if s.costCache == nil {
		view.warnings = append(view.warnings, "cost_observation_unavailable")
	}

	states := make([]*routeDiagnosticState, 0, len(cfgs))
	statesByID := make(map[int64]*routeDiagnosticState, len(cfgs))
	var target *routeDiagnosticState
	for _, cfg := range cfgs {
		if cfg == nil {
			continue
		}
		if runtime := runtimeConfigs[cfg.ID]; runtime != nil {
			cfg = runtime
		}
		state := s.newRouteDiagnosticState(cfg, allKeys[cfg.ID], view.cooldowns, view.costs, modelName, clientProtocol, now, view.aliases)
		if tokenHasRestriction && !admission.restriction.Allows(cfg.ID) {
			state.tokenRejected = true
		}
		states = append(states, state)
		statesByID[cfg.ID] = state
		if cfg.ID == targetID {
			target = state
		}
	}
	if targetID > 0 && target == nil {
		return RouteDiagnosticResponse{}, errRouteDiagnosticChannelNotFound
	}

	pool := make([]*routeDiagnosticState, 0, len(view.channels))
	for _, cfg := range s.diagnosticRouteOrder(view) {
		if state := statesByID[cfg.ID]; state != nil {
			pool = append(pool, state)
		}
	}
	healthEnabled := view.healthConfig.Enabled
	s.applyRouteDiagnosticHealth(states, view)
	referencePool := append([]*routeDiagnosticState{}, pool...)

	// The proxy applies the downstream token restriction after model/cooldown
	// selection. Mirror that behavior so this report explains the real request.
	if tokenHasRestriction {
		pool = filterRouteDiagnosticStates(pool, func(state *routeDiagnosticState) bool { return !state.tokenRejected })
	}

	markRouteDiagnosticCandidates(pool, referencePool, healthEnabled)

	for _, state := range states {
		s.observeRouteAttempt(state, view, clientProtocol, capabilities, bindings, bindingErr == nil, prices)
		state.diagnostic.Reasons = s.routeDiagnosticReasons(state, view.poolMode, view.poolMode == "exact")
		finishRouteAttemptObservation(state, admission, view)
	}

	actualWindow := "今日（本机时区 00:00 起）"
	var actualCounts map[int64]int64
	var actualTotal int64
	actualKnown := utf8.RuneCountInString(modelName) <= 191
	if actualKnown {
		actualCounts, actualTotal, err = s.routeDiagnosticActualCounts(ctx, now, modelName, clientProtocol, tokenID)
		if err != nil {
			actualKnown = false
			view.warnings = append(view.warnings, "history_unavailable")
		}
	} else {
		view.warnings = append(view.warnings, "history_model_identity_too_long")
	}
	for _, state := range states {
		state.diagnostic.ActualKnown = actualKnown
		count := actualCounts[state.cfg.ID]
		state.diagnostic.ActualRequests = count
		if actualTotal > 0 {
			state.diagnostic.ActualShare = float64(count) / float64(actualTotal)
		}
	}

	candidates := make([]ChannelRouteDiagnostic, 0, len(pool))
	for _, state := range pool {
		candidates = append(candidates, state.diagnostic)
	}

	response := RouteDiagnosticResponse{
		Model:               modelName,
		ClientProtocol:      clientProtocol,
		TokenID:             tokenID,
		RouteStrategy:       normalizeRouteStrategy(s.routeStrategyMode),
		PoolMode:            view.poolMode,
		HealthScoreEnabled:  healthEnabled,
		Candidates:          candidates,
		ActualWindow:        actualWindow,
		ActualTotalRequests: actualTotal,
		ObservedAt:          view.now,
		Warnings:            append([]string{}, view.warnings...),
		Token:               admission.view,
		CanonicalModel:      view.aliases.canonicalModelFor(modelName),
		Related:             []ChannelRouteDiagnostic{},
	}
	if target != nil {
		response.Target = target.diagnostic
	}
	if response.RouteStrategy == RouteStrategySticky && admission.hash != "" {
		response.Sticky = s.routeDiagnosticSticky(admission.hash, modelName, now, cfgs, pool, view)
	}
	names := view.aliases.namesFor(modelName)
	response.GroupNameCount = len(names)
	response.GroupNames = append([]string{}, names[:min(len(names), 50)]...)
	for _, state := range states {
		if routeDiagnosticRelated(state.cfg, names) {
			response.Related = append(response.Related, state.diagnostic)
		}
	}
	sort.SliceStable(response.Related, func(i, j int) bool {
		left, right := response.Related[i], response.Related[j]
		if left.Candidate != right.Candidate {
			return left.Candidate
		}
		if left.CandidatePosition != right.CandidatePosition {
			return left.CandidatePosition < right.CandidatePosition
		}
		return left.ChannelID < right.ChannelID
	})
	response.Policy = ModelRoutePolicy{RouteStrategy: response.RouteStrategy, PoolMode: view.poolMode, HealthScoreEnabled: healthEnabled, TTFBScoreEnabled: healthEnabled && view.healthConfig.EnableTTFBScore, HealthWindowMinutes: view.healthConfig.WindowMinutes, CandidateCount: len(pool), WeightReference: "pre_token_same_tier_enabled_keys_minus_cooldown", ActualKnown: actualKnown, ActualWindow: actualWindow, ActualTotalRequests: actualTotal, Sticky: response.Sticky}
	for _, state := range pool {
		response.Policy.TierCount = max(response.Policy.TierCount, state.diagnostic.CandidatePosition)
		if state.diagnostic.Attemptable {
			response.Policy.AttemptableCount++
		}
		if state.diagnostic.AttemptStatus == "unknown" {
			response.Policy.AttemptUnknownCount++
		}
	}
	response.ModelSummary = []string{"优先级与健康决定候选层；同层按当前轮询权重均衡，粘性可沿用最近成功的渠道。", "进入候选池后，还需通过令牌准入、Key 范围、URL 协议、RPM 与并发检查；此快照不预测下一次命中。", "权重参考以令牌筛选前的同层候选为分母；低层只在允许失败转移时被尝试，费用倍率不用于自动选择最便宜渠道。"}
	if !response.Target.Candidate {
		response.Target.CandidatePosition = 0
		response.Target.HigherPriorityCount = 0
		response.Target.SamePriorityCount = 0
		response.Target.EstimatedTrafficShare = 0
	}
	response.Summary = routeDiagnosticSummary(response)
	return response, nil
}

// routeDiagnosticActualCounts reads persisted proxy logs. Unlike the
// theoretical share, these numbers are historical facts from request logs.
func (s *Server) routeDiagnosticActualCounts(ctx context.Context, now time.Time, modelName, clientProtocol string, tokenID int64) (map[int64]int64, int64, error) {
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	filter := &model.LogFilter{
		Model:          modelName,
		LogSource:      model.LogSourceProxy,
		ClientProtocol: clientProtocol,
	}
	if tokenID > 0 {
		filter.AuthTokenID = &tokenID
	}
	stats, err := s.store.GetStatsLite(ctx, dayStart, now, filter)
	if err != nil {
		return nil, 0, err
	}
	counts := make(map[int64]int64, len(stats))
	var total int64
	for _, stat := range stats {
		if stat.ChannelID == nil || *stat.ChannelID <= 0 {
			continue
		}
		channelID := int64(*stat.ChannelID)
		counts[channelID] += int64(stat.Total)
		total += int64(stat.Total)
	}
	return counts, total, nil
}

func (s *Server) routeDiagnosticSticky(hash, modelName string, now time.Time, cfgs []*model.Config, pool []*routeDiagnosticState, view *routeSelectionSnapshot) *RouteDiagnosticSticky {
	if hash == "" || s.stickyRouter == nil {
		return nil
	}
	entry, ok := s.stickyRouter.snapshot(stickyScopeKey(hash), modelName, now)
	if !ok {
		return nil
	}
	result := &RouteDiagnosticSticky{
		ChannelID:     entry.ChannelID,
		RememberedAt:  entry.RememberedAt,
		ExpiresAt:     entry.RememberedAt.Add(stickyEntryTTL),
		ForwardStatus: "not_candidate",
	}
	for _, state := range pool {
		if state != nil && state.cfg.ID == entry.ChannelID {
			result.InCandidatePool = true
			break
		}
	}
	for _, cfg := range cfgs {
		if cfg != nil && cfg.ID == entry.ChannelID {
			result.ChannelName = cfg.Name
			if result.InCandidatePool {
				result.ForwardStatus = routeStickyForwardStatus(cfg, view, s)
			}
			break
		}
	}
	return result
}

func (s *Server) newRouteDiagnosticState(
	cfg *model.Config,
	keys []*model.APIKey,
	cooldowns channelCooldownSnapshot,
	costs map[int64]float64,
	modelName string,
	clientProtocol string,
	now time.Time,
	aliases *modelAliasRegistry,
) *routeDiagnosticState {
	exact := routeDiagnosticExactModelMatch(cfg, modelName)
	fuzzy := !exact && (cfg.SupportsModel(modelName) || aliases.supports(cfg, modelName))
	state := &routeDiagnosticState{
		cfg:  cfg,
		keys: keys,
		diagnostic: ChannelRouteDiagnostic{
			ChannelID:         cfg.ID,
			ChannelName:       cfg.Name,
			Enabled:           cfg.Enabled,
			BasePriority:      cfg.Priority,
			EffectivePriority: float64(cfg.Priority),
			SuccessRate:       1,
			ExactModelMatch:   exact,
			FuzzyModelMatch:   fuzzy,
			RPMLimit:          cfg.RPMLimit,
			MaxConcurrency:    cfg.MaxConcurrency,
			RoutingWeight:     calcEffectiveKeyCount(cfg, cooldowns.keys, now),
			Reasons:           make([]RouteDiagnosticReason, 0),
		},
	}
	state.outsideTime = !cfg.IsAvailableAt(now)
	for _, entry := range cfg.ModelEntries {
		if entry.Disabled || entry.Model == "*" {
			continue
		}
		for _, name := range aliases.namesFor(modelName) {
			if entry.Model == name {
				state.explicitGroupMatch = true
			}
		}
	}
	if s.channelRPMLimiter != nil {
		state.diagnostic.CurrentRPM = s.channelRPMLimiter.snapshot(cfg.ID)
		state.rpmExceeded = cfg.RPMLimit > 0 && state.diagnostic.CurrentRPM >= cfg.RPMLimit
	}
	if s.channelConcurrencyLimiter != nil {
		state.diagnostic.ActiveConcurrency = s.channelConcurrencyLimiter.snapshot(cfg.ID)
		state.concurrencyFull = cfg.MaxConcurrency > 0 && state.diagnostic.ActiveConcurrency >= cfg.MaxConcurrency
	}
	if until := cooldowns.channels[cfg.ID]; until.After(now) {
		state.channelCooldown = until
	}
	if until, ok := s.modelCooldownUntilAt(cfg, modelName, clientProtocol, cooldowns.models, now, aliases); ok && until.After(now) {
		state.modelCooldown = until
	}
	if cfg.DailyCostLimit > 0 && costs[cfg.ID] >= cfg.DailyCostLimit {
		state.costExceeded = true
	}

	for _, key := range keys {
		if key == nil || key.Disabled {
			continue
		}
		state.diagnostic.EnabledKeyCount++
		if !key.AllowsModel(modelName) {
			continue
		}
		state.diagnostic.ModelEligibleKeyCount++
		if until := cooldowns.keys[cfg.ID][key.KeyIndex]; !until.After(now) {
			state.diagnostic.ActiveKeyCount++
		}
	}
	if cfg.UsesOAuth() {
		state.diagnostic.EnabledKeyCount = 1
		state.diagnostic.ModelEligibleKeyCount = 1
		state.diagnostic.ActiveKeyCount = 1
	}
	state.diagnostic.CredentialKind = "api_key"
	if cfg.UsesOAuth() {
		state.diagnostic.CredentialKind = "oauth"
	}
	return state
}

func routeDiagnosticExactModelMatch(cfg *model.Config, requested string) bool {
	if cfg == nil {
		return false
	}
	for _, entry := range cfg.ModelEntries {
		if !entry.Disabled && entry.Model == requested {
			return true
		}
	}
	return false
}

func filterRouteDiagnosticStates(states []*routeDiagnosticState, keep func(*routeDiagnosticState) bool) []*routeDiagnosticState {
	result := make([]*routeDiagnosticState, 0, len(states))
	for _, state := range states {
		if state != nil && keep(state) {
			result = append(result, state)
		}
	}
	return result
}

func (s *Server) cooldownFallbackEnabled() bool {
	if s.configService == nil {
		return true
	}
	return s.configService.GetBool("cooldown_fallback_enabled", true)
}

func (s *Server) applyRouteDiagnosticHealth(states []*routeDiagnosticState, view *routeSelectionSnapshot) {
	medianTTFB := s.routeSelectionMedian(view)
	for _, state := range states {
		stats := routeHealthStats(view.healthStats, state.cfg.ID)
		state.diagnostic.SuccessRate = stats.SuccessRate
		state.diagnostic.HealthSampleCount = stats.SampleCount
		state.diagnostic.TTFBSampleCount = stats.FirstByteSampleCount
		state.diagnostic.AverageTTFBSeconds = stats.AvgFirstByteSeconds
		if !view.healthConfig.Enabled {
			continue
		}
		state.diagnostic.EffectivePriority = s.calculateEffectivePriority(
			state.cfg, stats, view.healthConfig, medianTTFB,
		)
		withoutTTFB := view.healthConfig
		withoutTTFB.EnableTTFBScore = false
		failureScore := s.calculateEffectivePriority(state.cfg, stats, withoutTTFB, medianTTFB)
		state.diagnostic.FailurePenalty = float64(state.cfg.Priority) - failureScore
		state.diagnostic.TTFBPenalty = failureScore - state.diagnostic.EffectivePriority
		state.diagnostic.TTFBSampleCount = stats.FirstByteSampleCount
		state.diagnostic.AverageTTFBSeconds = stats.AvgFirstByteSeconds
	}
}

func markRouteDiagnosticCandidates(pool, referencePool []*routeDiagnosticState, healthEnabled bool) {
	// The selector only moves the SWRR winner to the front of each priority
	// bucket. Remaining peers keep stable order, so their list index is not a
	// meaningful probability rank. Report the priority bucket instead.
	priorityPositions := make(map[int64]int, len(pool))
	nextPosition := 1
	for _, state := range pool {
		bucket := routeDiagnosticPriorityBucket(state, healthEnabled)
		if _, exists := priorityPositions[bucket]; !exists {
			priorityPositions[bucket] = nextPosition
			nextPosition++
		}
	}
	for _, state := range pool {
		state.diagnostic.Candidate = true
		state.diagnostic.CandidatePosition = priorityPositions[routeDiagnosticPriorityBucket(state, healthEnabled)]
		groupWeight := 0
		for _, peer := range pool {
			if routeDiagnosticSamePriorityGroup(state, peer, healthEnabled) {
				state.diagnostic.SamePriorityCount++
				continue
			}
			if routeDiagnosticPriorityAbove(peer, state, healthEnabled) {
				state.diagnostic.HigherPriorityCount++
			}
		}
		for _, peer := range referencePool {
			if routeDiagnosticSamePriorityGroup(state, peer, healthEnabled) {
				groupWeight += peer.diagnostic.RoutingWeight
			}
		}
		if groupWeight > 0 {
			state.diagnostic.EstimatedTrafficShare = float64(state.diagnostic.RoutingWeight) / float64(groupWeight)
		}
	}
}

func routeDiagnosticPriorityBucket(state *routeDiagnosticState, healthEnabled bool) int64 {
	if healthEnabled {
		return effPriorityBucket(state.diagnostic.EffectivePriority)
	}
	return int64(state.cfg.Priority)
}

func routeDiagnosticSamePriorityGroup(left, right *routeDiagnosticState, healthEnabled bool) bool {
	if healthEnabled {
		return effPriorityBucket(left.diagnostic.EffectivePriority) == effPriorityBucket(right.diagnostic.EffectivePriority)
	}
	return left.cfg.Priority == right.cfg.Priority
}

func routeDiagnosticPriorityAbove(left, right *routeDiagnosticState, healthEnabled bool) bool {
	if healthEnabled {
		return effPriorityBucket(left.diagnostic.EffectivePriority) > effPriorityBucket(right.diagnostic.EffectivePriority)
	}
	return left.cfg.Priority > right.cfg.Priority
}

func (s *Server) routeDiagnosticReasons(state *routeDiagnosticState, poolMode string, exactPoolAvailable bool) []RouteDiagnosticReason {
	reasons := make([]RouteDiagnosticReason, 0, 6)
	add := func(code, message string, blocking bool) {
		stage := "candidate"
		switch code {
		case "no_enabled_key", "key_model_scope", "all_keys_cooling", "rpm_limit", "concurrency_limit", "no_compatible_protocol":
			stage = "attempt"
		case "higher_priority_candidates", "smooth_weighted_round_robin", "first_candidate":
			stage = "ordering"
		}
		if poolMode == "cooldown_fallback" && state.diagnostic.Candidate && (code == "channel_cooldown" || code == "model_cooldown" || code == "all_keys_cooling") {
			blocking = false
		}
		reasons = append(reasons, RouteDiagnosticReason{Code: code, Message: message, Blocking: blocking, Stage: stage})
	}
	if !state.cfg.Enabled {
		add("disabled", "渠道已停用。", true)
	}
	if state.outsideTime {
		add("available_time", "当前在渠道可用时段之外。", true)
	}
	if !state.diagnostic.ExactModelMatch && !state.diagnostic.FuzzyModelMatch {
		add("model_mismatch", "渠道没有匹配当前请求模型。", true)
	} else if !state.diagnostic.Candidate && !state.explicitGroupMatch && state.cfg.SupportsModel("*") && exactPoolAvailable {
		add("exact_pool_preferred", "已有可用的精确或统一映射渠道，通配渠道尚未进入兜底池。", true)
	}
	if state.costExceeded {
		add("daily_cost_limit", fmt.Sprintf("今日费用已达到渠道上限 $%.2f。", state.cfg.DailyCostLimit), true)
	}
	if !state.channelCooldown.IsZero() {
		add("channel_cooldown", "渠道冷却至 "+state.channelCooldown.Format("15:04:05")+"。", true)
	}
	if !state.modelCooldown.IsZero() {
		add("model_cooldown", "当前模型在此渠道冷却至 "+state.modelCooldown.Format("15:04:05")+"。", true)
	}
	if !state.cfg.UsesOAuth() && state.diagnostic.EnabledKeyCount == 0 {
		add("no_enabled_key", "渠道没有启用的上游 Key。", true)
	} else if !state.cfg.UsesOAuth() && state.diagnostic.ModelEligibleKeyCount == 0 {
		add("key_model_scope", "启用的上游 Key 都不允许当前请求模型。", true)
	} else if !state.cfg.UsesOAuth() && state.diagnostic.ActiveKeyCount == 0 {
		add("all_keys_cooling", "所有允许当前模型的 Key 当前都在冷却。", true)
	}
	if state.tokenRejected {
		add("token_channel_restriction", "所选下游令牌不允许使用此渠道。", true)
	}
	if state.rpmExceeded {
		add("rpm_limit", fmt.Sprintf("最近一分钟请求数 %d 已达到 RPM 上限 %d；实际请求会暂时跳过此渠道。", state.diagnostic.CurrentRPM, state.cfg.RPMLimit), true)
	}
	if state.concurrencyFull {
		add("concurrency_limit", fmt.Sprintf("当前并发 %d 已达到渠道上限 %d；实际请求会暂时跳过此渠道。", state.diagnostic.ActiveConcurrency, state.cfg.MaxConcurrency), true)
	}
	if state.protocolMissing {
		add("no_compatible_protocol", "当前请求族没有可协商的协议 URL，实际尝试会跳过；自动协议的不支持观察会到期重试。", true)
	}
	if state.diagnostic.Candidate {
		if state.diagnostic.HigherPriorityCount > 0 {
			add("higher_priority_candidates", fmt.Sprintf("前面还有 %d 个更高%s渠道；它们成功后不会继续尝试本渠道。", state.diagnostic.HigherPriorityCount, map[bool]string{true: "有效优先级", false: "优先级"}[s.healthCache != nil && s.healthCache.Config().Enabled]), false)
		} else if state.diagnostic.SamePriorityCount > 1 {
			add("smooth_weighted_round_robin", fmt.Sprintf("已进入最高优先级组；同组 %d 个渠道按启用 Key 减冷却数的权重平滑轮询，当前同层权重参考为 %.1f%%。", state.diagnostic.SamePriorityCount, state.diagnostic.EstimatedTrafficShare*100), false)
		} else {
			add("first_candidate", "当前是此模型的首选候选渠道。", false)
		}
	}
	if poolMode == "cooldown_fallback" && state.diagnostic.Candidate {
		add("cooldown_fallback", "所有匹配渠道都在冷却，本次仅作为最早恢复的兜底渠道。", false)
	}
	return reasons
}

func routeDiagnosticSummary(result RouteDiagnosticResponse) []string {
	target := result.Target
	if target.Candidate && target.AttemptStatus == "blocked" {
		for _, reason := range target.Reasons {
			if reason.Blocking {
				return []string{"当前已进入候选池，但准入或实际尝试阶段会被阻断。", reason.Message}
			}
		}
	}
	if target.Candidate && target.AttemptStatus == "unknown" {
		return []string{"当前已进入候选池，但缺少生效权限或凭证观察，无法确认此刻可尝试。"}
	}
	for _, reason := range target.Reasons {
		if target.Candidate && (reason.Code == "rpm_limit" || reason.Code == "concurrency_limit") {
			return []string{"当前已进入模型候选池，但会在实际尝试时被临时跳过。", reason.Message}
		}
	}
	if !target.Candidate {
		for _, reason := range target.Reasons {
			if reason.Blocking {
				return []string{"当前不会进入该模型的路由候选池。", reason.Message}
			}
		}
		return []string{"当前不会进入该模型的路由候选池。"}
	}
	if target.HigherPriorityCount > 0 {
		return []string{
			fmt.Sprintf("当前已进入候选池，但前面有 %d 个更高优先级渠道。", target.HigherPriorityCount),
			"只有前面的渠道失败并允许转移时，才会尝试此渠道。",
		}
	}
	if target.SamePriorityCount > 1 {
		return []string{
			fmt.Sprintf("当前位于最高优先级轮询组，同层权重参考为 %.1f%%；权重按启用 Key 减冷却数计算，与当前模型可尝试 Key 数分开。", target.EstimatedTrafficShare*100),
			"实际单次请求在首个渠道成功后就会结束，不会把同一请求发送给所有同级渠道。",
		}
	}
	return []string{"当前是该模型的首选候选渠道。"}
}
