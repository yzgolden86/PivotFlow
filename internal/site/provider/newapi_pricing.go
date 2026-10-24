package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
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
	BillingExpr     string   `json:"billing_expr"`
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
		billingExpr := strings.TrimSpace(row.BillingExpr)
		billingTerms := parseBillingTerms(billingExpr)
		pricing.Models = append(pricing.Models, ModelPrice{
			Model:                  name,
			QuotaType:              row.QuotaType,
			PerCallPrice:           parsePerCallPrice(row.ModelPrice),
			ModelRatio:             positiveRatio(row.ModelRatio, 1),
			CompletionRatio:        positiveRatio(row.CompletionRatio, 1),
			CacheRatio:             positiveRatio(firstRatio(row.CacheRatio, row.CacheRatioAlt), 1),
			CacheCreationRatio:     positiveRatio(firstRatio(row.CacheCreationRatio, row.CacheCreationRatioAlt, row.CreateCacheRatio), 1),
			BillingTerms:           billingTerms,
			BillingExprUnsupported: billingExpr != "" && billingTerms == nil,
			Groups:                 normalizeGroups(row.EnableGroups),
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

// USDPerMillionAll returns every token price bucket in USD per million tokens
// for one group. Cache writes are kept separate because billing expressions
// can price the 5-minute and 1-hour TTLs differently. For per-call models all
// values are zero and the caller must use PerCallUSD instead.
func (m ModelPrice) USDPerMillionAll(groupRatio float64) (input, output, cacheRead, cacheWrite5m, cacheWrite1h float64) {
	if m.QuotaType == 1 {
		return 0, 0, 0, 0, 0
	}
	if terms := m.BillingTerms; terms != nil {
		input = terms.Input * groupRatio
		output = terms.Output * groupRatio

		// New API treats cache variables as opt-in: an omitted bucket stays in p.
		// The relay parser has already removed those tokens from p, so price
		// the bucket at p here to preserve the upstream total.
		cacheRead = input
		if terms.CacheReadSet {
			cacheRead = terms.CacheRead * groupRatio
		}
		cacheWrite5m = input
		if terms.CacheWrite5mSet {
			cacheWrite5m = terms.CacheWrite5m * groupRatio
		}
		// Claude's 1h cache write defaults to 6/3.75 of the short write price,
		// which itself defaults to p when cc was not referenced.
		cacheWrite1h = cacheWrite5m * 6 / 3.75
		if terms.CacheWrite1hSet {
			cacheWrite1h = terms.CacheWrite1h * groupRatio
		}
		return input, output, cacheRead, cacheWrite5m, cacheWrite1h
	}
	base := m.ModelRatio * newAPIRatioToUSDPerMillion * groupRatio
	cacheWrite := base * m.CacheCreationRatio
	return base, base * m.CompletionRatio, base * m.CacheRatio, cacheWrite, cacheWrite
}

// USDPerMillion returns input, output, cache-read and cache-write prices in USD
// per million tokens for one group. It is retained for display callers that
// have a single cache-write column; the 5-minute bucket is the representative
// value. Cost calculation must use USDPerMillionAll.
func (m ModelPrice) USDPerMillion(groupRatio float64) (input, output, cacheRead, cacheWrite float64) {
	input, output, cacheRead, cacheWrite, _ = m.USDPerMillionAll(groupRatio)
	return input, output, cacheRead, cacheWrite
}

// PerCallUSD returns the fixed charge for a per-call model, or 0 if this model
// is billed per token.
func (m ModelPrice) PerCallUSD(groupRatio float64) float64 {
	if m.QuotaType != 1 {
		return 0
	}
	return m.PerCallPrice * groupRatio
}

// DetermineBillingModel returns the pricing row that actually covers the
// request model. New API forks commonly prefix only displayed model IDs with a
// channel number, while the routing request uses the bare model name (or the
// reverse). A basename match is safe only when it is unique; ambiguity must
// fall back to local estimation rather than choosing another channel's tariff.
func (s SitePricing) DetermineBillingModel(modelName string) (string, bool) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return "", false
	}
	find := func(candidate string) (ModelPrice, bool) {
		for _, price := range s.Models {
			if price.Model == candidate {
				return price, true
			}
		}
		for _, price := range s.Models {
			if strings.EqualFold(price.Model, candidate) {
				return price, true
			}
		}
		return ModelPrice{}, false
	}

	if price, ok := find(modelName); ok {
		return price.Model, true
	}

	baseName := modelName
	if index := strings.LastIndex(baseName, "/"); index >= 0 {
		baseName = strings.TrimSpace(baseName[index+1:])
	}
	if baseName == "" {
		return "", false
	}

	var matched ModelPrice
	matches := 0
	for _, price := range s.Models {
		priceBase := price.Model
		if index := strings.LastIndex(priceBase, "/"); index >= 0 {
			priceBase = strings.TrimSpace(priceBase[index+1:])
		}
		if !strings.EqualFold(priceBase, baseName) {
			continue
		}
		matches++
		matched = price
	}
	if matches == 1 {
		return matched.Model, true
	}
	return "", false
}

// parseBillingTerms reads the base arm of a New API billing expression.
// The grammar is deliberately small: a tier("base", ...) arm built from
// +, -, * and parentheses over numeric coefficients and the token variables
// p, c, cr, cc and cc1h. Anything else returns nil so the legacy ratios remain
// authoritative instead of guessing.
func parseBillingTerms(expression string) *BillingTerms {
	body, ok := billingTierBody(expression, "base")
	if !ok {
		return nil
	}
	parser := billingExprParser{source: body}
	value, ok := parser.parseSum()
	parser.skipSpace()
	if !ok || parser.index != len(parser.source) || value.constant != 0 {
		return nil
	}
	return &value.terms
}

// billingTierBody finds the last arm with the requested tier name. New API
// expressions commonly put small-request tiers before the catch-all base arm,
// so taking the last match is deliberate.
func billingTierBody(expression, tierName string) (string, bool) {
	var body string
	matched := false
	for searchFrom := 0; searchFrom < len(expression); {
		relative := strings.Index(expression[searchFrom:], "tier")
		if relative < 0 {
			break
		}
		index := searchFrom + relative
		searchFrom = index + len("tier")
		if index > 0 && isBillingIdentifierByte(expression[index-1]) {
			continue
		}
		cursor := searchFrom
		for cursor < len(expression) && isBillingSpace(expression[cursor]) {
			cursor++
		}
		if cursor >= len(expression) || expression[cursor] != '(' {
			continue
		}
		open := cursor
		end := matchingClosingParen(expression, open)
		if end < 0 {
			break
		}
		searchFrom = end + 1
		cursor++
		for cursor < len(expression) && isBillingSpace(expression[cursor]) {
			cursor++
		}
		name, afterName, ok := billingStringLiteral(expression, cursor)
		if !ok {
			continue
		}
		cursor = afterName
		for cursor < end && isBillingSpace(expression[cursor]) {
			cursor++
		}
		if cursor < end && expression[cursor] == ',' && strings.TrimSpace(name) == tierName {
			body = expression[cursor+1 : end]
			matched = true
		}
	}
	return body, matched
}

func billingStringLiteral(source string, start int) (string, int, bool) {
	if start >= len(source) || source[start] != '"' {
		return "", start, false
	}
	var value strings.Builder
	escaped := false
	for index := start + 1; index < len(source); index++ {
		ch := source[index]
		if escaped {
			value.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			return value.String(), index + 1, true
		}
		value.WriteByte(ch)
	}
	return "", start, false
}

func isBillingSpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n'
}

type billingExprParser struct {
	source string
	index  int
}

// billingLinear is the linear form accepted here: all token variables are
// degree one and constants must reduce to zero for a per-token price.
type billingLinear struct {
	terms    BillingTerms
	constant float64
}

func (p *billingExprParser) skipSpace() {
	for p.index < len(p.source) && isBillingSpace(p.source[p.index]) {
		p.index++
	}
}

func (p *billingExprParser) parseSum() (billingLinear, bool) {
	left, ok := p.parseProduct()
	if !ok {
		return billingLinear{}, false
	}
	for {
		p.skipSpace()
		if p.index >= len(p.source) || (p.source[p.index] != '+' && p.source[p.index] != '-') {
			return left, true
		}
		operator := p.source[p.index]
		p.index++
		right, ok := p.parseProduct()
		if !ok {
			return billingLinear{}, false
		}
		if operator == '+' {
			left = addBillingLinear(left, right)
		} else {
			left = subtractBillingLinear(left, right)
		}
	}
}

func (p *billingExprParser) parseProduct() (billingLinear, bool) {
	left, ok := p.parseUnary()
	if !ok {
		return billingLinear{}, false
	}
	for {
		p.skipSpace()
		if p.index >= len(p.source) || p.source[p.index] != '*' {
			return left, true
		}
		p.index++
		right, ok := p.parseUnary()
		if !ok {
			return billingLinear{}, false
		}
		left, ok = multiplyBillingLinear(left, right)
		if !ok {
			return billingLinear{}, false
		}
	}
}

func (p *billingExprParser) parseUnary() (billingLinear, bool) {
	p.skipSpace()
	negative := false
	if p.index < len(p.source) && (p.source[p.index] == '+' || p.source[p.index] == '-') {
		negative = p.source[p.index] == '-'
		p.index++
	}
	value, ok := p.parseAtom()
	if !ok {
		return billingLinear{}, false
	}
	if negative {
		value = scaleBillingLinear(value, -1)
	}
	return value, true
}

func (p *billingExprParser) parseAtom() (billingLinear, bool) {
	p.skipSpace()
	if p.index >= len(p.source) {
		return billingLinear{}, false
	}
	if p.source[p.index] == '(' {
		p.index++
		value, ok := p.parseSum()
		if !ok {
			return billingLinear{}, false
		}
		p.skipSpace()
		if p.index >= len(p.source) || p.source[p.index] != ')' {
			return billingLinear{}, false
		}
		p.index++
		return value, true
	}
	if variable, ok := p.parseVariable(); ok {
		value := billingLinear{}
		if !setBillingTerm(&value.terms, variable, 1) {
			return billingLinear{}, false
		}
		return value, true
	}
	return p.parseNumber()
}

func (p *billingExprParser) parseVariable() (string, bool) {
	for _, candidate := range []string{"cc1h", "cc", "cr", "p", "c"} {
		if !strings.HasPrefix(p.source[p.index:], candidate) {
			continue
		}
		end := p.index + len(candidate)
		if end < len(p.source) && isBillingIdentifierByte(p.source[end]) {
			continue
		}
		p.index = end
		return candidate, true
	}
	return "", false
}

func (p *billingExprParser) parseNumber() (billingLinear, bool) {
	start := p.index
	digits := 0
	dot := false
	for p.index < len(p.source) {
		ch := p.source[p.index]
		if ch >= '0' && ch <= '9' {
			digits++
			p.index++
			continue
		}
		if ch == '.' && !dot {
			dot = true
			p.index++
			continue
		}
		break
	}
	if digits == 0 {
		return billingLinear{}, false
	}
	if p.index < len(p.source) && (p.source[p.index] == 'e' || p.source[p.index] == 'E') {
		p.index++
		if p.index < len(p.source) && (p.source[p.index] == '+' || p.source[p.index] == '-') {
			p.index++
		}
		exponentStart := p.index
		for p.index < len(p.source) && p.source[p.index] >= '0' && p.source[p.index] <= '9' {
			p.index++
		}
		if p.index == exponentStart {
			return billingLinear{}, false
		}
	}
	value, err := strconv.ParseFloat(p.source[start:p.index], 64)
	if err != nil {
		return billingLinear{}, false
	}
	return billingLinear{constant: value}, true
}

func isBillingIdentifierByte(ch byte) bool {
	return ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}

func setBillingTerm(terms *BillingTerms, variable string, value float64) bool {
	if terms == nil {
		return false
	}
	switch variable {
	case "p":
		terms.Input = value
	case "c":
		terms.Output = value
	case "cr":
		terms.CacheRead = value
		terms.CacheReadSet = true
	case "cc":
		terms.CacheWrite5m = value
		terms.CacheWrite5mSet = true
	case "cc1h":
		terms.CacheWrite1h = value
		terms.CacheWrite1hSet = true
	default:
		return false
	}
	return true
}

func addBillingTerms(left, right BillingTerms) BillingTerms {
	return BillingTerms{
		Input:           left.Input + right.Input,
		Output:          left.Output + right.Output,
		CacheRead:       left.CacheRead + right.CacheRead,
		CacheWrite5m:    left.CacheWrite5m + right.CacheWrite5m,
		CacheWrite1h:    left.CacheWrite1h + right.CacheWrite1h,
		CacheReadSet:    left.CacheReadSet || right.CacheReadSet,
		CacheWrite5mSet: left.CacheWrite5mSet || right.CacheWrite5mSet,
		CacheWrite1hSet: left.CacheWrite1hSet || right.CacheWrite1hSet,
	}
}

func subtractBillingTerms(left, right BillingTerms) BillingTerms {
	return BillingTerms{
		Input:           left.Input - right.Input,
		Output:          left.Output - right.Output,
		CacheRead:       left.CacheRead - right.CacheRead,
		CacheWrite5m:    left.CacheWrite5m - right.CacheWrite5m,
		CacheWrite1h:    left.CacheWrite1h - right.CacheWrite1h,
		CacheReadSet:    left.CacheReadSet || right.CacheReadSet,
		CacheWrite5mSet: left.CacheWrite5mSet || right.CacheWrite5mSet,
		CacheWrite1hSet: left.CacheWrite1hSet || right.CacheWrite1hSet,
	}
}

func addBillingLinear(left, right billingLinear) billingLinear {
	return billingLinear{
		terms:    addBillingTerms(left.terms, right.terms),
		constant: left.constant + right.constant,
	}
}

func subtractBillingLinear(left, right billingLinear) billingLinear {
	return billingLinear{
		terms:    subtractBillingTerms(left.terms, right.terms),
		constant: left.constant - right.constant,
	}
}

func scaleBillingLinear(value billingLinear, scalar float64) billingLinear {
	return billingLinear{
		terms: BillingTerms{
			Input:           value.terms.Input * scalar,
			Output:          value.terms.Output * scalar,
			CacheRead:       value.terms.CacheRead * scalar,
			CacheWrite5m:    value.terms.CacheWrite5m * scalar,
			CacheWrite1h:    value.terms.CacheWrite1h * scalar,
			CacheReadSet:    value.terms.CacheReadSet,
			CacheWrite5mSet: value.terms.CacheWrite5mSet,
			CacheWrite1hSet: value.terms.CacheWrite1hSet,
		},
		constant: value.constant * scalar,
	}
}

func multiplyBillingLinear(left, right billingLinear) (billingLinear, bool) {
	leftHasTerms := left.terms != (BillingTerms{})
	rightHasTerms := right.terms != (BillingTerms{})
	if leftHasTerms && rightHasTerms {
		return billingLinear{}, false
	}
	if leftHasTerms {
		return scaleBillingLinear(left, right.constant), true
	}
	if rightHasTerms {
		return scaleBillingLinear(right, left.constant), true
	}
	return billingLinear{constant: left.constant * right.constant}, true
}

func matchingClosingParen(value string, open int) int {
	depth := 0
	inString := false
	escaped := false
	for index := open; index < len(value); index++ {
		ch := value[index]
		if inString {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
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
