package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/protocol"

	"github.com/gin-gonic/gin"
)

// RouteDiagnosticReason explains one routing gate in user-facing language.
// Blocking reasons keep the channel out of the final candidate pool.
type RouteDiagnosticReason struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
}

// ChannelRouteDiagnostic is a read-only snapshot of a channel's current route
// eligibility. It deliberately does not advance the smooth round-robin state.
type ChannelRouteDiagnostic struct {
	ChannelID             int64                   `json:"channel_id"`
	ChannelName           string                  `json:"channel_name"`
	Enabled               bool                    `json:"enabled"`
	BasePriority          int                     `json:"base_priority"`
	EffectivePriority     float64                 `json:"effective_priority"`
	SuccessRate           float64                 `json:"success_rate"`
	HealthSampleCount     int64                   `json:"health_sample_count"`
	ExactModelMatch       bool                    `json:"exact_model_match"`
	FuzzyModelMatch       bool                    `json:"fuzzy_model_match"`
	ActiveKeyCount        int                     `json:"active_key_count"`
	EnabledKeyCount       int                     `json:"enabled_key_count"`
	RPMLimit              int                     `json:"rpm_limit"`
	CurrentRPM            int                     `json:"current_rpm"`
	MaxConcurrency        int                     `json:"max_concurrency"`
	ActiveConcurrency     int                     `json:"active_concurrency"`
	Candidate             bool                    `json:"candidate"`
	CandidatePosition     int                     `json:"candidate_position,omitempty"`
	HigherPriorityCount   int                     `json:"higher_priority_count"`
	SamePriorityCount     int                     `json:"same_priority_count"`
	EstimatedTrafficShare float64                 `json:"estimated_traffic_share"`
	Reasons               []RouteDiagnosticReason `json:"reasons"`
}

type RouteDiagnosticResponse struct {
	Model              string                   `json:"model"`
	ClientProtocol     string                   `json:"client_protocol"`
	TokenID            int64                    `json:"token_id,omitempty"`
	PoolMode           string                   `json:"pool_mode"`
	HealthScoreEnabled bool                     `json:"health_score_enabled"`
	Target             ChannelRouteDiagnostic   `json:"target"`
	Candidates         []ChannelRouteDiagnostic `json:"candidates"`
	Summary            []string                 `json:"summary"`
}

type routeDiagnosticState struct {
	cfg             *model.Config
	diagnostic      ChannelRouteDiagnostic
	channelCooldown time.Time
	modelCooldown   time.Time
	costExceeded    bool
	tokenRejected   bool
	rpmExceeded     bool
	concurrencyFull bool
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
	modelName := strings.TrimSpace(c.Query("model"))
	if modelName == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "model is required")
		return
	}
	clientProtocol := normalizeOptionalProtocol(c.DefaultQuery("client_protocol", string(protocol.OpenAI)))
	if !protocol.IsValid(protocol.Protocol(clientProtocol)) {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid client_protocol")
		return
	}

	var tokenID int64
	if raw := strings.TrimSpace(c.Query("token_id")); raw != "" {
		tokenID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || tokenID <= 0 {
			RespondErrorMsg(c, http.StatusBadRequest, "invalid token_id")
			return
		}
	}

	result, err := s.buildChannelRouteDiagnostics(c.Request.Context(), id, modelName, clientProtocol, tokenID)
	if err != nil {
		if errors.Is(err, errRouteDiagnosticChannelNotFound) || errors.Is(err, model.ErrAuthTokenNotFound) {
			RespondError(c, http.StatusNotFound, err)
			return
		}
		RespondError(c, http.StatusInternalServerError, err)
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
	cfgs, err := s.store.ListConfigs(ctx)
	if err != nil {
		return RouteDiagnosticResponse{}, err
	}

	allKeys, err := s.store.GetAllAPIKeys(ctx)
	if err != nil {
		return RouteDiagnosticResponse{}, err
	}
	cooldowns := s.loadChannelCooldownSnapshot(ctx)
	now := time.Now()
	costs := map[int64]float64{}
	if s.costCache != nil {
		costs = s.costCache.GetAll()
	}

	var tokenRestriction model.ChannelRestriction
	var tokenHasRestriction bool
	if tokenID > 0 {
		token, tokenErr := s.store.GetAuthToken(ctx, tokenID)
		if tokenErr != nil {
			return RouteDiagnosticResponse{}, tokenErr
		}
		tokenRestriction, tokenErr = token.ChannelRestriction()
		if tokenErr != nil {
			return RouteDiagnosticResponse{}, tokenErr
		}
		tokenHasRestriction = tokenRestriction.Restricted()
	}

	states := make([]*routeDiagnosticState, 0, len(cfgs))
	var target *routeDiagnosticState
	for _, cfg := range cfgs {
		if cfg == nil {
			continue
		}
		state := s.newRouteDiagnosticState(cfg, allKeys[cfg.ID], cooldowns, costs, modelName, clientProtocol, now)
		if tokenHasRestriction && !tokenRestriction.Allows(cfg.ID) {
			state.tokenRejected = true
		}
		states = append(states, state)
		if cfg.ID == targetID {
			target = state
		}
	}
	if target == nil {
		return RouteDiagnosticResponse{}, errRouteDiagnosticChannelNotFound
	}

	exactPool := filterRouteDiagnosticStates(states, func(state *routeDiagnosticState) bool {
		return state.cfg.Enabled && state.diagnostic.ExactModelMatch && !state.costExceeded
	})
	exactAvailable := filterRouteDiagnosticStates(exactPool, routeDiagnosticAvailableBeforeToken)

	poolMode := "none"
	pool := exactAvailable
	if len(pool) > 0 {
		poolMode = "exact"
	} else {
		fuzzyPool := filterRouteDiagnosticStates(states, func(state *routeDiagnosticState) bool {
			return state.cfg.Enabled && (state.diagnostic.ExactModelMatch || state.diagnostic.FuzzyModelMatch) && !state.costExceeded
		})
		pool = filterRouteDiagnosticStates(fuzzyPool, routeDiagnosticAvailableBeforeToken)
		if len(pool) > 0 {
			poolMode = "fuzzy"
		} else if len(fuzzyPool) > 0 && s.cooldownFallbackEnabled() {
			configs := make([]*model.Config, 0, len(fuzzyPool))
			for _, state := range fuzzyPool {
				configs = append(configs, state.cfg)
			}
			best, _ := s.pickBestChannelWhenAllCooled(
				configs, modelName, clientProtocol, cooldowns.channels, cooldowns.keys, cooldowns.models, now,
			)
			if best != nil {
				poolMode = "cooldown_fallback"
				pool = filterRouteDiagnosticStates(fuzzyPool, func(state *routeDiagnosticState) bool { return state.cfg.ID == best.ID })
			}
		}
	}

	// The proxy applies the downstream token restriction after model/cooldown
	// selection. Mirror that behavior so this report explains the real request.
	if tokenHasRestriction {
		pool = filterRouteDiagnosticStates(pool, func(state *routeDiagnosticState) bool { return !state.tokenRejected })
	}

	healthEnabled := s.healthCache != nil && s.healthCache.Config().Enabled
	s.applyRouteDiagnosticHealth(states, pool, healthEnabled)
	sortRouteDiagnosticPool(pool, healthEnabled)
	markRouteDiagnosticCandidates(pool, healthEnabled)

	for _, state := range states {
		state.diagnostic.Reasons = s.routeDiagnosticReasons(state, poolMode, len(exactAvailable) > 0)
	}

	candidates := make([]ChannelRouteDiagnostic, 0, len(pool))
	for _, state := range pool {
		candidates = append(candidates, state.diagnostic)
	}

	response := RouteDiagnosticResponse{
		Model:              modelName,
		ClientProtocol:     clientProtocol,
		TokenID:            tokenID,
		PoolMode:           poolMode,
		HealthScoreEnabled: healthEnabled,
		Target:             target.diagnostic,
		Candidates:         candidates,
	}
	if !response.Target.Candidate {
		response.Target.CandidatePosition = 0
		response.Target.HigherPriorityCount = 0
		response.Target.SamePriorityCount = 0
		response.Target.EstimatedTrafficShare = 0
	}
	response.Summary = routeDiagnosticSummary(response)
	return response, nil
}

func (s *Server) newRouteDiagnosticState(
	cfg *model.Config,
	keys []*model.APIKey,
	cooldowns channelCooldownSnapshot,
	costs map[int64]float64,
	modelName string,
	clientProtocol string,
	now time.Time,
) *routeDiagnosticState {
	exact := routeDiagnosticExactModelMatch(cfg, modelName)
	fuzzy := !exact && s.configSupportsModelWithFuzzyMatch(cfg, modelName)
	state := &routeDiagnosticState{
		cfg: cfg,
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
			Reasons:           make([]RouteDiagnosticReason, 0),
		},
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
	if until, ok := s.modelCooldownUntil(cfg, modelName, clientProtocol, cooldowns.models); ok && until.After(now) {
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
		if until := cooldowns.keys[cfg.ID][key.KeyIndex]; !until.After(now) {
			state.diagnostic.ActiveKeyCount++
		}
	}
	if cfg.UsesOAuth() {
		state.diagnostic.EnabledKeyCount = 1
		state.diagnostic.ActiveKeyCount = 1
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

func routeDiagnosticAvailableBeforeToken(state *routeDiagnosticState) bool {
	if state == nil || state.costExceeded || !state.channelCooldown.IsZero() || !state.modelCooldown.IsZero() {
		return false
	}
	return state.cfg.UsesOAuth() || state.diagnostic.ActiveKeyCount > 0
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

func (s *Server) applyRouteDiagnosticHealth(states, pool []*routeDiagnosticState, enabled bool) {
	if !enabled {
		return
	}
	statsByID := make(map[int64]model.ChannelHealthStats, len(states))
	for _, state := range states {
		stats := s.healthCache.GetHealthStats(state.cfg.ID)
		statsByID[state.cfg.ID] = stats
		state.diagnostic.SuccessRate = stats.SuccessRate
		state.diagnostic.HealthSampleCount = stats.SampleCount
	}
	samples := make([]float64, 0, len(pool))
	for _, state := range pool {
		stats := statsByID[state.cfg.ID]
		if stats.FirstByteSampleCount > 0 && stats.AvgFirstByteSeconds > 0 {
			samples = append(samples, stats.AvgFirstByteSeconds)
		}
	}
	medianTTFB := medianFloat64(samples)
	for _, state := range states {
		state.diagnostic.EffectivePriority = s.calculateEffectivePriority(
			state.cfg, statsByID[state.cfg.ID], s.healthCache.Config(), medianTTFB,
		)
	}
}

func sortRouteDiagnosticPool(pool []*routeDiagnosticState, healthEnabled bool) {
	sort.SliceStable(pool, func(i, j int) bool {
		if healthEnabled {
			left := effPriorityBucket(pool[i].diagnostic.EffectivePriority)
			right := effPriorityBucket(pool[j].diagnostic.EffectivePriority)
			if left != right {
				return left > right
			}
		} else if pool[i].cfg.Priority != pool[j].cfg.Priority {
			return pool[i].cfg.Priority > pool[j].cfg.Priority
		}
		return pool[i].cfg.ID < pool[j].cfg.ID
	})
}

func markRouteDiagnosticCandidates(pool []*routeDiagnosticState, healthEnabled bool) {
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
				groupWeight += max(1, peer.diagnostic.ActiveKeyCount)
				continue
			}
			if routeDiagnosticPriorityAbove(peer, state, healthEnabled) {
				state.diagnostic.HigherPriorityCount++
			}
		}
		if groupWeight > 0 {
			state.diagnostic.EstimatedTrafficShare = float64(max(1, state.diagnostic.ActiveKeyCount)) / float64(groupWeight)
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
		reasons = append(reasons, RouteDiagnosticReason{Code: code, Message: message, Blocking: blocking})
	}
	if !state.cfg.Enabled {
		add("disabled", "渠道已停用。", true)
	}
	if !state.diagnostic.ExactModelMatch && !state.diagnostic.FuzzyModelMatch {
		add("model_mismatch", "渠道没有匹配当前请求模型。", true)
	} else if !state.diagnostic.ExactModelMatch && exactPoolAvailable {
		add("exact_pool_preferred", "已有可用的精确模型渠道，本次不会进入模糊匹配兜底池。", true)
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
	} else if !state.cfg.UsesOAuth() && state.diagnostic.ActiveKeyCount == 0 {
		add("all_keys_cooling", "所有启用 Key 当前都在冷却。", true)
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
	if state.diagnostic.Candidate {
		if state.diagnostic.HigherPriorityCount > 0 {
			add("higher_priority_candidates", fmt.Sprintf("前面还有 %d 个更高%s渠道；它们成功后不会继续尝试本渠道。", state.diagnostic.HigherPriorityCount, map[bool]string{true: "有效优先级", false: "优先级"}[s.healthCache != nil && s.healthCache.Config().Enabled]), false)
		} else if state.diagnostic.SamePriorityCount > 1 {
			add("smooth_weighted_round_robin", fmt.Sprintf("已进入最高优先级组；同组 %d 个渠道按有效 Key 数平滑轮询，当前理论份额约 %.1f%%。", state.diagnostic.SamePriorityCount, state.diagnostic.EstimatedTrafficShare*100), false)
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
			fmt.Sprintf("当前位于最高优先级轮询组，理论流量份额约 %.1f%%。", target.EstimatedTrafficShare*100),
			"实际单次请求在首个渠道成功后就会结束，不会把同一请求发送给所有同级渠道。",
		}
	}
	return []string{"当前是该模型的首选候选渠道。"}
}
