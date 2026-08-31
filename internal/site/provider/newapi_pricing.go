package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// defaultPricingGroup is the group name New API uses when a token has none.
const defaultPricingGroup = "default"

// newAPIRatioToUSDPerMillion converts a New API ratio unit to USD per million
// tokens. The site's quota_per_unit is 500000 and one ratio unit is $0.002 per
// 1K tokens, so one ratio unit is $2 per million.
const newAPIRatioToUSDPerMillion = 2.0

// pricingResponse mirrors GET /api/pricing. The models array arrives either at
// the top level or wrapped in `data`; group_ratio always sits at the top level.
type pricingResponse struct {
	Success    bool               `json:"success"`
	Message    string             `json:"message"`
	Data       []pricingModelRow  `json:"data"`
	GroupRatio map[string]float64 `json:"group_ratio"`
}

type pricingModelRow struct {
	ModelName       string   `json:"model_name"`
	QuotaType       int      `json:"quota_type"`
	ModelRatio      *float64 `json:"model_ratio"`
	CompletionRatio *float64 `json:"completion_ratio"`
	// Sites differ on the casing and naming of the cache ratios; accept the
	// variants metapi has observed in the wild rather than assuming one form.
	CacheRatio            *float64        `json:"cache_ratio"`
	CacheRatioAlt         *float64        `json:"cacheRatio"`
	CacheCreationRatio    *float64        `json:"cache_creation_ratio"`
	CacheCreationRatioAlt *float64        `json:"cacheCreationRatio"`
	CreateCacheRatio      *float64        `json:"create_cache_ratio"`
	ModelPrice            json.RawMessage `json:"model_price"`
	EnableGroups          []string        `json:"enable_groups"`
}

// FetchPricing reads the site's own price table.
//
// Requires a management session: /api/pricing is an authenticated endpoint on
// most deployments. A plain model-call API key is not enough.
func (p *NewAPI) FetchPricing(ctx context.Context, req AccountRequest) (SitePricing, error) {
	if req.Credentials.AccessToken == "" && req.Credentials.Cookie == "" {
		return SitePricing{}, &Error{Code: CodeUnsupported, Message: "pricing lookup requires a management session"}
	}

	var payload pricingResponse
	if err := p.doJSON(ctx, req, http.MethodGet, "/api/pricing", nil, &payload); err != nil {
		return SitePricing{}, err
	}
	// Some forks omit `success` on this endpoint but still return rows; only
	// treat it as an error when it is explicitly false and there is no data.
	if !payload.Success && len(payload.Data) == 0 {
		return SitePricing{}, &Error{Code: CodeUnsupported, Message: "site does not expose a pricing table"}
	}

	pricing := SitePricing{
		Models:     make([]ModelPrice, 0, len(payload.Data)),
		GroupRatio: normalizeGroupRatio(payload.GroupRatio),
	}
	for _, row := range payload.Data {
		name := strings.TrimSpace(row.ModelName)
		if name == "" {
			continue
		}
		pricing.Models = append(pricing.Models, ModelPrice{
			Model:              name,
			QuotaType:          row.QuotaType,
			PerCallPrice:       parsePerCallPrice(row.ModelPrice),
			ModelRatio:         positiveRatio(row.ModelRatio, 1),
			CompletionRatio:    positiveRatio(row.CompletionRatio, 1),
			CacheRatio:         positiveRatio(firstRatio(row.CacheRatio, row.CacheRatioAlt), 1),
			CacheCreationRatio: positiveRatio(firstRatio(row.CacheCreationRatio, row.CacheCreationRatioAlt, row.CreateCacheRatio), 1),
			Groups:             normalizeGroups(row.EnableGroups),
		})
	}
	if len(pricing.Models) == 0 {
		return SitePricing{}, &Error{Code: CodeUnsupported, Message: "pricing table contained no usable models"}
	}
	return pricing, nil
}

// parsePerCallPrice reads model_price, which is either a bare number or an
// object with input/output. Only the input side is charged per call; that is
// what the upstream deducts.
func parsePerCallPrice(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	var scalar float64
	if err := json.Unmarshal(raw, &scalar); err == nil {
		return scalar
	}
	var object struct {
		Input  float64 `json:"input"`
		Output float64 `json:"output"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		return object.Input
	}
	return 0
}

func firstRatio(values ...*float64) *float64 {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// positiveRatio rejects zero and negative ratios: a zero would silently make a
// paid model look free, which is worse than falling back to the neutral 1.
func positiveRatio(value *float64, fallback float64) float64 {
	if value == nil || *value <= 0 {
		return fallback
	}
	return *value
}

func normalizeGroups(groups []string) []string {
	out := make([]string, 0, len(groups))
	for _, group := range groups {
		if trimmed := strings.TrimSpace(group); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return []string{defaultPricingGroup}
	}
	return out
}

// normalizeGroupRatio drops non-positive ratios and guarantees a default entry,
// so a lookup for an unknown group cannot return zero and zero out the cost.
func normalizeGroupRatio(raw map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(raw)+1)
	for group, ratio := range raw {
		if trimmed := strings.TrimSpace(group); trimmed != "" && ratio > 0 {
			out[trimmed] = ratio
		}
	}
	if _, ok := out[defaultPricingGroup]; !ok {
		out[defaultPricingGroup] = 1
	}
	return out
}

// USDPerMillion returns input, output, cache-read and cache-write prices in USD
// per million tokens for one group. For per-call models all four are zero and
// the caller must use PerCallUSD instead.
func (m ModelPrice) USDPerMillion(groupRatio float64) (input, output, cacheRead, cacheWrite float64) {
	if m.QuotaType == 1 {
		return 0, 0, 0, 0
	}
	base := m.ModelRatio * newAPIRatioToUSDPerMillion * groupRatio
	return base, base * m.CompletionRatio, base * m.CacheRatio, base * m.CacheCreationRatio
}

// PerCallUSD returns the fixed charge for a per-call model, or 0 if this model
// is billed per token.
func (m ModelPrice) PerCallUSD(groupRatio float64) float64 {
	if m.QuotaType != 1 {
		return 0
	}
	return m.PerCallPrice * groupRatio
}

// RatioFor returns the multiplier for a group, falling back to default.
func (s SitePricing) RatioFor(group string) float64 {
	group = strings.TrimSpace(group)
	if group != "" {
		if ratio, ok := s.GroupRatio[group]; ok && ratio > 0 {
			return ratio
		}
	}
	if ratio, ok := s.GroupRatio[defaultPricingGroup]; ok && ratio > 0 {
		return ratio
	}
	return 1
}
