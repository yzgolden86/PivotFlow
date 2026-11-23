package app

import (
	"context"
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// Only safe observations are exported. The private hash is used for a sticky
// snapshot and is never serialized, logged, or decrypted.
type RouteDiagnosticToken struct {
	ID                int64                   `json:"id,omitempty"`
	Description       string                  `json:"description"`
	Scope             string                  `json:"scope"`
	Status            string                  `json:"status"`
	Admission         string                  `json:"admission"`
	PermissionSource  string                  `json:"permission_source"`
	ModelAllowed      *bool                   `json:"model_allowed"`
	ProtocolAllowed   *bool                   `json:"protocol_allowed"`
	ChannelRestricted bool                    `json:"channel_restricted"`
	CostUsedUSD       *float64                `json:"cost_used_usd"`
	CostLimitUSD      *float64                `json:"cost_limit_usd"`
	MaxConcurrency    *int                    `json:"max_concurrency"`
	ActiveConcurrency *int                    `json:"active_concurrency"`
	Reasons           []RouteDiagnosticReason `json:"reasons"`
}

type routeAdmissionObservation struct {
	view             RouteDiagnosticToken
	hash             string
	restriction      model.ChannelRestriction
	restrictionKnown bool
}

// snapshotRouteToken takes all runtime permissions and counters under one lock.
// It never revalidates by invoking auth middleware, acquiring a slot, expiring a
// token, or updating last_used_at.
func (s *AuthService) snapshotRouteToken(tokenID int64, name, client string, aliases *modelAliasRegistry, now time.Time) routeAdmissionObservation {
	result := routeAdmissionObservation{view: RouteDiagnosticToken{ID: tokenID, Scope: "token", Status: "not_loaded", Admission: "unknown", PermissionSource: "unknown", Reasons: []RouteDiagnosticReason{}}}
	if s == nil {
		return result
	}
	s.authTokensMux.RLock()
	defer s.authTokensMux.RUnlock()
	hash, known := s.authTokenHashes[tokenID]
	expires, active := s.authTokens[hash]
	if !known || !active {
		return result
	}
	result.hash = hash
	result.view.Status = "loaded"
	result.view.Admission = "allowed"
	result.view.PermissionSource = "runtime"
	result.restriction = s.authTokenChannels[hash]
	result.restrictionKnown = true
	result.view.ChannelRestricted = result.restriction.Restricted()
	modelAllowed := len(s.authTokenModels[hash]) == 0
	names := []string{name}
	if s.modelAliasCandidates != nil {
		names = aliases.namesFor(name)
	}
	for _, allowed := range s.authTokenModels[hash] {
		for _, candidate := range names {
			if strings.EqualFold(allowed, candidate) {
				modelAllowed = true
			}
		}
	}
	protocolAllowed := len(s.authTokenProtocols[hash]) == 0
	for _, allowed := range s.authTokenProtocols[hash] {
		if strings.ToLower(strings.TrimSpace(allowed)) == client {
			protocolAllowed = true
		}
	}
	result.view.ModelAllowed = &modelAllowed
	result.view.ProtocolAllowed = &protocolAllowed
	cost := s.authTokenCostLimits[hash]
	used, limit := float64(cost.usedMicroUSD)/1_000_000, float64(cost.limitMicroUSD)/1_000_000
	maxConn, activeConn := s.authTokenMaxConns[hash], s.authTokenActiveReqs[hash]
	result.view.CostUsedUSD = &used
	result.view.CostLimitUSD = &limit
	result.view.MaxConcurrency = &maxConn
	result.view.ActiveConcurrency = &activeConn
	add := func(code, message string) {
		result.view.Admission = "blocked"
		result.view.Reasons = append(result.view.Reasons, RouteDiagnosticReason{Code: code, Message: message, Blocking: true, Stage: "admission"})
	}
	if expires > 0 && now.UnixMilli() > expires {
		result.view.Status = "expired"
		add("token_expired", "所选令牌已过期，无法通过请求准入。")
	}
	if !modelAllowed {
		add("token_model_restriction", "所选令牌不允许当前请求模型及其统一映射组。")
	}
	if !protocolAllowed {
		add("token_protocol_restriction", "所选令牌不允许当前客户端协议。")
	}
	if cost.limitMicroUSD > 0 && cost.usedMicroUSD >= cost.limitMicroUSD {
		add("token_cost_limit", "所选令牌已达到费用软上限。")
	}
	if maxConn > 0 && activeConn >= maxConn {
		add("token_concurrency_limit", "所选令牌的当前并发已达到上限。")
	}
	return result
}

func (s *Server) observeRouteAdmission(ctx context.Context, id int64, name, client string, view *routeSelectionSnapshot) (routeAdmissionObservation, error) {
	if id == 0 {
		return routeAdmissionObservation{view: RouteDiagnosticToken{Scope: "global", Status: "global", Admission: "allowed", PermissionSource: "unscoped", Reasons: []RouteDiagnosticReason{}}, restrictionKnown: true}, nil
	}
	// This local metadata lookup does not recover an API token or decrypt its
	// ciphertext. A missing selected object must never become a global query.
	stored, err := s.store.GetAuthToken(ctx, id)
	if err != nil {
		return routeAdmissionObservation{}, err
	}
	if stored == nil {
		return routeAdmissionObservation{}, model.ErrAuthTokenNotFound
	}
	result := s.authService.snapshotRouteToken(id, name, client, view.aliases, view.now)
	result.view.Description = stored.Description
	if result.view.Status != "not_loaded" {
		return result, nil
	}
	result.view.Reasons = append(result.view.Reasons, RouteDiagnosticReason{Code: "token_runtime_unavailable", Message: "所选令牌尚未出现在当前进程的生效权限中；无法确认准入。", Stage: "admission"})
	if !stored.IsActive {
		result.view.Status = "inactive"
		result.view.Admission = "blocked"
		result.view.Reasons = append(result.view.Reasons, RouteDiagnosticReason{Code: "token_inactive", Message: "所选令牌已停用。", Blocking: true, Stage: "admission"})
	} else if stored.ExpiresAt != nil && *stored.ExpiresAt > 0 && view.now.UnixMilli() > *stored.ExpiresAt {
		result.view.Status = "expired"
		result.view.Admission = "blocked"
		result.view.Reasons = append(result.view.Reasons, RouteDiagnosticReason{Code: "token_expired", Message: "所选令牌已过期。", Blocking: true, Stage: "admission"})
	}
	// Compatibility diagnostics may describe configured channel restrictions,
	// but must mark them as stored rather than pretending they are runtime.
	result.restriction, err = stored.ChannelRestriction()
	if err != nil {
		return routeAdmissionObservation{}, err
	}
	result.view.PermissionSource = "stored_unverified"
	result.view.ChannelRestricted = result.restriction.Restricted()
	return result, nil
}
