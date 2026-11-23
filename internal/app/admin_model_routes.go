package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/protocol"
)

const modelRoutePageSize = 50
const modelRouteMaxPageSize = 100

type ModelRouteInventoryItem struct {
	Model              string   `json:"model"`
	Kind               string   `json:"kind"`
	Aliases            []string `json:"aliases"`
	AliasCount         int      `json:"alias_count"`
	ConfiguredChannels int      `json:"configured_channels"`
	EnabledChannels    int      `json:"enabled_channels"`
}

type ModelRouteInventory struct {
	Version int                       `json:"version"`
	Items   []ModelRouteInventoryItem `json:"items"`
	Total   int                       `json:"total"`
	Offset  int                       `json:"offset"`
	Limit   int                       `json:"limit"`
}

type ModelRoutePolicy struct {
	RouteStrategy       string                 `json:"route_strategy"`
	PoolMode            string                 `json:"pool_mode"`
	HealthScoreEnabled  bool                   `json:"health_score_enabled"`
	TTFBScoreEnabled    bool                   `json:"ttfb_score_enabled"`
	HealthWindowMinutes int                    `json:"health_window_minutes"`
	CandidateCount      int                    `json:"candidate_count"`
	AttemptableCount    int                    `json:"attemptable_count"`
	AttemptUnknownCount int                    `json:"attempt_unknown_count"`
	TierCount           int                    `json:"tier_count"`
	WeightReference     string                 `json:"weight_reference"`
	ActualWindow        string                 `json:"actual_window"`
	ActualKnown         bool                   `json:"actual_known"`
	ActualTotalRequests int64                  `json:"actual_total_requests"`
	Sticky              *RouteDiagnosticSticky `json:"sticky,omitempty"`
}

type ModelRouteDiagnosticResponse struct {
	Version        int                      `json:"version"`
	ObservedAt     time.Time                `json:"observed_at"`
	Model          string                   `json:"model"`
	ClientProtocol string                   `json:"client_protocol"`
	CanonicalModel string                   `json:"canonical_model"`
	GroupNames     []string                 `json:"group_names"`
	GroupNameCount int                      `json:"group_name_count"`
	Policy         ModelRoutePolicy         `json:"policy"`
	Token          RouteDiagnosticToken     `json:"token"`
	Channels       []ChannelRouteDiagnostic `json:"channels"`
	Total          int                      `json:"total"`
	Offset         int                      `json:"offset"`
	Limit          int                      `json:"limit"`
	Summary        []string                 `json:"summary"`
	Warnings       []string                 `json:"warnings"`
}

func rejectRepeatedRouteQuery(c *gin.Context) error {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	// ParseQuery reports malformed escapes/semicolons instead of silently
	// dropping a selected scope and accidentally falling back to global.
	if err != nil {
		return errors.New("invalid query")
	}
	for _, values := range query {
		if len(values) != 1 {
			return errors.New("repeated query parameter")
		}
	}
	return err
}

func validateRouteText(value string, maxRunes int, field string) error {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s must be %d characters or fewer", field, maxRunes)
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return fmt.Errorf("invalid %s", field)
		}
	}
	return nil
}

func parseModelRoutePage(c *gin.Context) (limit, offset int, err error) {
	if err = rejectRepeatedRouteQuery(c); err != nil {
		return
	}
	limit = modelRoutePageSize
	if values, supplied := c.Request.URL.Query()["limit"]; supplied {
		limit, err = strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > modelRouteMaxPageSize {
			return 0, 0, errors.New("invalid limit")
		}
	}
	if values, supplied := c.Request.URL.Query()["offset"]; supplied {
		offset, err = strconv.Atoi(values[0])
		if err != nil || offset < 0 {
			return 0, 0, errors.New("invalid offset")
		}
	}
	return
}

func parseModelRouteScope(c *gin.Context) (name, client string, tokenID int64, err error) {
	if err = rejectRepeatedRouteQuery(c); err != nil {
		return
	}
	name = c.Query("model")
	if strings.TrimSpace(name) == "" {
		err = errors.New("model is required")
		return
	}
	if err = validateRouteText(name, 512, "model"); err != nil {
		return
	}
	client = string(protocol.OpenAI)
	if values, supplied := c.Request.URL.Query()["client_protocol"]; supplied {
		client = values[0]
		if !protocol.IsValid(protocol.Protocol(client)) {
			err = errors.New("invalid client_protocol")
			return
		}
	}
	if values, supplied := c.Request.URL.Query()["token_id"]; supplied {
		tokenID, err = strconv.ParseInt(values[0], 10, 64)
		if err != nil || tokenID <= 0 {
			err = errors.New("invalid token_id")
		}
	}
	return
}

func respondModelRouteError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, model.ErrAuthTokenNotFound):
		RespondErrorMsg(c, http.StatusNotFound, "routing_token_not_found")
	case errors.Is(err, errRouteDiagnosticChannelNotFound):
		RespondErrorMsg(c, http.StatusNotFound, "routing_channel_not_found")
	default:
		RespondErrorMsg(c, http.StatusInternalServerError, "routing_storage_unavailable")
	}
}

func (s *Server) HandleModelRouteInventory(c *gin.Context) {
	limit, offset, err := parseModelRoutePage(c)
	if err != nil {
		RespondErrorMsg(c, 400, err.Error())
		return
	}
	search := c.Query("search")
	if err = validateRouteText(search, 512, "search"); err != nil {
		RespondErrorMsg(c, 400, err.Error())
		return
	}
	enabledOnly := false
	if values, supplied := c.Request.URL.Query()["enabled_only"]; supplied {
		if values[0] != "true" && values[0] != "false" {
			RespondErrorMsg(c, 400, "invalid enabled_only")
			return
		}
		enabledOnly = values[0] == "true"
	}
	result, err := s.modelRouteInventory(c.Request.Context(), search, limit, offset, enabledOnly)
	if err != nil {
		respondModelRouteError(c, err)
		return
	}
	RespondJSON(c, 200, result)
}

// Inventory performs one local configuration read. It does not diagnose each
// model, resolve site pricing, or infer arbitrary names from wildcard channels.
func (s *Server) modelRouteInventory(ctx context.Context, search string, limit, offset int, enabledOnly bool) (ModelRouteInventory, error) {
	cfgs, err := s.store.ListConfigs(ctx)
	if err != nil {
		return ModelRouteInventory{}, err
	}
	aliases := s.modelAliases.frozen()
	type itemState struct {
		item                ModelRouteInventoryItem
		names               []string
		configured, enabled map[int64]struct{}
	}
	items := make(map[string]*itemState)
	ensure := func(name string) *itemState {
		canonical := aliases.canonicalModelFor(name)
		if item := items[canonical]; item != nil {
			return item
		}
		names := aliases.namesFor(canonical)
		kind := "model"
		if canonical == "*" {
			kind = "wildcard"
		} else if len(names) > 1 {
			kind = "group"
		}
		preview := []string{}
		if len(names) > 1 {
			preview = append(preview, names[1:min(len(names), 9)]...)
		}
		item := &itemState{item: ModelRouteInventoryItem{Model: canonical, Kind: kind, Aliases: preview, AliasCount: len(names) - 1}, names: append([]string{}, names...), configured: make(map[int64]struct{}), enabled: make(map[int64]struct{})}
		items[canonical] = item
		return item
	}
	if index := aliases.snapshot(); index != nil {
		for _, group := range index.groups {
			if group.Enabled {
				item := ensure(group.Canonical)
				item.item.Kind = "group"
			}
		}
	}
	for _, cfg := range cfgs {
		if cfg == nil {
			continue
		}
		for _, entry := range cfg.ModelEntries {
			if entry.Model == "" {
				continue
			}
			item := ensure(entry.Model)
			item.names = append(item.names, entry.Model)
			item.configured[cfg.ID] = struct{}{}
			if cfg.Enabled && !entry.Disabled {
				item.enabled[cfg.ID] = struct{}{}
			}
		}
	}
	needle := strings.ToLower(strings.TrimSpace(search))
	result := ModelRouteInventory{Version: 1, Items: []ModelRouteInventoryItem{}, Limit: limit, Offset: offset}
	for _, state := range items {
		// Token model selection uses current enabled, discovered names. A
		// wildcard is routing support, not a discovered model identity.
		if enabledOnly && (len(state.enabled) == 0 || state.item.Kind == "wildcard") {
			continue
		}
		match := needle == ""
		for _, name := range state.names {
			if strings.Contains(strings.ToLower(name), needle) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		state.item.ConfiguredChannels = len(state.configured)
		state.item.EnabledChannels = len(state.enabled)
		result.Items = append(result.Items, state.item)
	}
	sort.Slice(result.Items, func(i, j int) bool {
		if (result.Items[i].Kind == "wildcard") != (result.Items[j].Kind == "wildcard") {
			return result.Items[j].Kind == "wildcard"
		}
		return result.Items[i].Model < result.Items[j].Model
	})
	result.Total = len(result.Items)
	start := min(offset, result.Total)
	end := start + min(limit, result.Total-start)
	result.Items = result.Items[start:end]
	return result, nil
}

func (s *Server) HandleModelRouteDiagnostics(c *gin.Context) {
	limit, offset, err := parseModelRoutePage(c)
	if err != nil {
		RespondErrorMsg(c, 400, err.Error())
		return
	}
	name, client, tokenID, err := parseModelRouteScope(c)
	if err != nil {
		RespondErrorMsg(c, 400, err.Error())
		return
	}
	core, err := s.buildRouteDiagnostics(c.Request.Context(), 0, name, client, tokenID)
	if err != nil {
		respondModelRouteError(c, err)
		return
	}
	channels := core.Related
	total := len(channels)
	start := min(offset, total)
	end := start + min(limit, total-start)
	result := ModelRouteDiagnosticResponse{Version: 1, ObservedAt: core.ObservedAt, Model: name, ClientProtocol: client, CanonicalModel: core.CanonicalModel, GroupNames: core.GroupNames, GroupNameCount: core.GroupNameCount, Policy: core.Policy, Token: core.Token, Channels: channels[start:end], Total: total, Limit: limit, Offset: offset, Summary: core.ModelSummary, Warnings: core.Warnings}
	RespondJSON(c, 200, result)
}
