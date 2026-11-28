package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	CodeUnsupported           = "unsupported"
	CodeBrowserRequired       = "browser_required"
	CodeExpired               = "expired"
	CodeUserIDRequired        = "user_id_required"
	CodeTimeout               = "provider_timeout"
	CodeRateLimited           = "provider_rate_limited"
	CodeInvalidResponse       = "invalid_response"
	CodeRequestFailed         = "request_failed"
	CodeRoutingKeyUnavailable = "routing_api_key_unavailable"
	CheckinSuccess            = "success"
	CheckinAlreadyChecked     = "already_checked"
	CheckinBrowserRequired    = "browser_required"
	CheckinUnsupported        = "unsupported"
	CheckinFailed             = "failed"
)

type Error struct {
	Code       string
	StatusCode int
	RetryAfter time.Time
	Message    string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Code + ": " + e.Message
	}
	return e.Code
}

func ErrorCode(err error) string {
	var providerErr *Error
	if errors.As(err, &providerErr) {
		return providerErr.Code
	}
	return CodeRequestFailed
}

func ErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	var providerErr *Error
	if errors.As(err, &providerErr) {
		if message := strings.TrimSpace(providerErr.Message); message != "" {
			return message
		}
	}
	return strings.TrimSpace(err.Error())
}

func ErrorStatusCode(err error) int {
	var providerErr *Error
	if errors.As(err, &providerErr) {
		return providerErr.StatusCode
	}
	return 0
}

type Credentials struct {
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	Cookie       string `json:"cookie,omitempty"`
	UserID       int64  `json:"user_id,omitempty"`
}

func (c Credentials) Token() string {
	if c.AccessToken != "" {
		return c.AccessToken
	}
	return c.APIKey
}

// EffectiveExpiresAt returns an explicitly stored expiry, or the exp claim
// from a JWT when the upstream did not provide a separate expires_in value.
// Opaque access tokens simply return zero and are refreshed only when the
// provider exposes an explicit expiry.
func (c Credentials) EffectiveExpiresAt() int64 {
	if c.ExpiresAt > 0 {
		return c.ExpiresAt
	}
	parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(c.AccessToken, "Bearer ")), ".")
	if len(parts) != 3 {
		return 0
	}
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var payload map[string]any
	if json.Unmarshal(claims, &payload) != nil {
		return 0
	}
	switch value := payload["exp"].(type) {
	case float64:
		if value > 0 {
			return int64(value * 1000)
		}
	case json.Number:
		if seconds, err := strconv.ParseInt(string(value), 10, 64); err == nil && seconds > 0 {
			return seconds * 1000
		}
	case string:
		if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds > 0 {
			return seconds * 1000
		}
	}
	return 0
}

func (c Credentials) RefreshDue(now time.Time, lead time.Duration) bool {
	if strings.TrimSpace(c.RefreshToken) == "" {
		return false
	}
	expiresAt := c.EffectiveExpiresAt()
	return expiresAt > 0 && expiresAt-now.Add(lead).UnixMilli() <= 0
}

type ProviderCapabilities struct {
	ServerCheckin   bool     `json:"server_checkin"`
	BrowserAssisted bool     `json:"browser_assisted"`
	Balance         bool     `json:"balance"`
	Models          bool     `json:"models"`
	Announcements   bool     `json:"announcements"`
	CredentialTypes []string `json:"credential_types"`
}

type DetectionResult struct {
	Matched      bool                 `json:"matched"`
	ProviderID   string               `json:"provider_id"`
	SystemName   string               `json:"system_name,omitempty"`
	Capabilities ProviderCapabilities `json:"capabilities"`
	// CheckinMethod is what the site published about its check-in capability
	// while it was being detected. Providers that read it from the same
	// endpoint they already use for detection fill this in, so callers learn
	// the capability without a second round trip. Nil means "not reported
	// here", which is not the same as reporting CheckinMethodUnknown.
	CheckinMethod *CheckinMethod `json:"checkin_method,omitempty"`
}

// Checkin method status values reported by DiscoverCheckin.
const (
	// CheckinMethodUnknown means the status endpoint did not publish the flag,
	// so the check-in must still be attempted the historical way.
	CheckinMethodUnknown = "unknown"
	// CheckinMethodAvailable means server-side check-in can be attempted.
	CheckinMethodAvailable = "available"
	// CheckinMethodDisabled means the site has check-in switched off; posting
	// the check-in endpoint would only waste a request.
	CheckinMethodDisabled = "disabled"
	// CheckinMethodTurnstile means an interactive Cloudflare Turnstile
	// challenge is required. A headless request cannot satisfy it, so the
	// server-side check-in is impossible until a human completes it.
	CheckinMethodTurnstile = "turnstile"
	// CheckinMethodUnavailable means the site does not serve a check-in
	// endpoint at all: the attempt came back 404. It differs from
	// CheckinMethodDisabled in where the fact comes from — disabled is what the
	// site says about itself on its public status endpoint, unavailable is what
	// the site did when actually asked. The conclusion for the scheduler is the
	// same either way: posting again cannot succeed, so do not spend the
	// request.
	CheckinMethodUnavailable = "unavailable"
)

// CheckinMethod describes how (or whether) a site can be checked in on the
// server side. Most New API-family sites publish this on their public
// /api/status endpoint, so the scheduler can discover it before spending a
// request that the upstream is guaranteed to reject.
type CheckinMethod struct {
	// Status is one of the CheckinMethod* constants.
	Status string `json:"status"`
	// TurnstileSiteKey is the public site key, kept for diagnostics only.
	TurnstileSiteKey string `json:"turnstile_site_key,omitempty"`
	// Source records where the method was discovered, e.g. "api_status".
	Source string `json:"source,omitempty"`
}

// CheckinMethodProvider is implemented by providers that can discover the
// site's check-in method instead of guessing it from a failed check-in.
type CheckinMethodProvider interface {
	DiscoverCheckin(ctx context.Context, req AccountRequest) (CheckinMethod, error)
}

type AccountRequest struct {
	BaseURL     string
	ProxyURL    string
	Credentials Credentials
}

type RefreshAccountRequest = AccountRequest

type AccountSnapshot struct {
	Username string
	// UserID comes only from the current profile response. Request credentials
	// and compatibility headers are hints, not evidence of the accepted user.
	UserID   int64
	Balance  *float64
	Currency string
	Status   string
}

func profileUserID(data any) int64 {
	row, ok := data.(map[string]any)
	if !ok {
		return 0
	}
	const maxExactID = 1<<53 - 1
	var id int64
	switch value := row["id"].(type) {
	case float64:
		if value <= 0 || value > maxExactID || math.Trunc(value) != value {
			return 0
		}
		id = int64(value)
	case string:
		id, _ = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	case json.Number:
		id, _ = strconv.ParseInt(string(value), 10, 64)
	}
	if id <= 0 || id > maxExactID {
		return 0
	}
	return id
}

type ModelSnapshot struct {
	Model     string
	RouteType string
	Source    string
}

type LoginRequest struct {
	BaseURL  string
	ProxyURL string
	Username string
	Password string
}

type RoutingKeySnapshot struct {
	ID        string
	Name      string
	Group     string
	Protocols []string
	Models    []string
	Key       string
	Enabled   bool
}

type CheckinResult struct {
	Status     string
	RewardText string
	Message    string
}

type Announcement struct {
	SourceKey       string
	Title           string
	ContentMarkdown string
	Level           string
	SourceURL       string
	// UpstreamCreatedAt / UpstreamUpdatedAt 是上游公告自身的发布时间与最后修改时间。
	// 控制台的详情页要显示「发布时间」，所以这两个值必须分开传：
	// 合成一个字段后就没法区分「刚发布」和「刚被改过」。
	// 上游不给时间时保持 0 —— New API 系的 /api/notice 只返回一段纯文本通知，
	// 响应里根本没有时间字段，这类站点的发布时间是拿不到的。
	UpstreamCreatedAt int64
	UpstreamUpdatedAt int64
	ContentHash       string
}

type SiteAdapter interface {
	ID() string
	Capabilities() ProviderCapabilities
	Detect(ctx context.Context, baseURL string) (DetectionResult, error)
	RefreshAccount(ctx context.Context, req RefreshAccountRequest) (AccountSnapshot, error)
	ListModels(ctx context.Context, req AccountRequest) ([]ModelSnapshot, error)
	Checkin(ctx context.Context, req AccountRequest) (CheckinResult, error)
	ListAnnouncements(ctx context.Context, req AccountRequest) ([]Announcement, error)
}

// AccountAuthenticator is implemented by providers that can exchange a user
// login for a management/session token.
type AccountAuthenticator interface {
	Login(ctx context.Context, req LoginRequest) (Credentials, error)
}

// CredentialRefresher is implemented by providers whose management session
// can be renewed without asking the user to paste a new access token.
type CredentialRefresher interface {
	RefreshCredentials(ctx context.Context, req AccountRequest) (Credentials, error)
}

// ManagementCredentialResolver validates a management credential and fills
// provider-specific identity fields such as New-API-User when discoverable.
type ManagementCredentialResolver interface {
	ResolveManagementCredentials(ctx context.Context, req AccountRequest) (Credentials, error)
}

// RoutingKeyProvider exposes model-call API keys owned by a management
// session. The control plane uses this to build PivotFlow channels without asking
// the user to paste the same key twice.
type RoutingKeyProvider interface {
	ListRoutingKeys(ctx context.Context, req AccountRequest) ([]RoutingKeySnapshot, error)
}

// RoutingModelProvider resolves the model list for one routing key through a
// provider's management API. It is used when the key-level OpenAI endpoint is
// unavailable or returns an unscoped model list for every key.
type RoutingModelProvider interface {
	ListModelsForRoutingKey(ctx context.Context, req AccountRequest, key RoutingKeySnapshot) ([]ModelSnapshot, error)
}

// CheckinStatusProvider reports whether the credential has already checked in
// today. It lets a browser-challenged check-in endpoint be reconciled after
// the user completes the action in the site's own web UI.
type CheckinStatusProvider interface {
	CheckedInToday(ctx context.Context, req AccountRequest) (bool, error)
}

// ModelPrice is one model's billing terms as the upstream site itself declares
// them. Relay sites apply their own ratios, so a cost computed from vendor list
// prices diverges from what the site actually deducts; these are the numbers
// that make the estimate match.
//
// Ratios are relative units, not money. New API's convention is
// quota_per_unit = 500000 and one ratio unit = $0.002 per 1K tokens, which is
// why converting a ratio to USD per million tokens multiplies by 2.
type ModelPrice struct {
	Model string
	// QuotaType 0 bills per token via the ratios below; 1 bills a fixed amount
	// per call and the ratios do not apply.
	QuotaType int
	// PerCallPrice is the fixed charge for QuotaType 1, before the group ratio.
	PerCallPrice float64
	// PerCallPriceValid distinguishes an explicit nonnegative price (including
	// zero) from a missing or malformed upstream value. Nil preserves legacy
	// producers that do not report validity; it is unset for per-token rows.
	PerCallPriceValid *bool
	// ModelRatio scales the input price. Output is additionally scaled by
	// CompletionRatio; cache reads and writes by their own ratios.
	ModelRatio         float64
	CompletionRatio    float64
	CacheRatio         float64
	CacheCreationRatio float64
	// BillingTerms holds the per-million-token coefficients from a site's
	// billing expression when it publishes one. Nil means the legacy ratio
	// fields above are authoritative.
	BillingTerms *BillingTerms
	// BillingExprUnsupported is true when the site published a billing
	// expression that this client cannot evaluate. In that case the display
	// model_ratio must not be trusted as the charge; callers fall back to local
	// estimation instead of recording a confidently wrong site-priced amount.
	BillingExprUnsupported bool
	// Groups lists the site groups this model is available to. A price only
	// applies to a request whose token belongs to one of these groups.
	Groups []string
}

// BillingTerms is a site-declared linear token expression in USD per million
// tokens: input*p + output*c + cacheRead*cr + cacheWrite5m*cc +
// cacheWrite1h*cc1h. Some New API forks publish a model_ratio for display that
// is unrelated to the expression they actually charge.
type BillingTerms struct {
	Input        float64
	Output       float64
	CacheRead    float64
	CacheWrite5m float64
	CacheWrite1h float64
	// The Set flags distinguish an omitted variable from an explicit zero
	// coefficient. New API folds an omitted cache variable back into prompt
	// pricing, while "cc * 0" intentionally makes that bucket free.
	CacheReadSet    bool
	CacheWrite5mSet bool
	CacheWrite1hSet bool
}

// SitePricing is a whole site's price table plus its per-group multipliers.
// GroupRatio always carries a "default" entry so lookups cannot miss.
type SitePricing struct {
	Models     []ModelPrice
	GroupRatio map[string]float64
}

// PricingProvider is implemented by providers whose management API exposes the
// site's own price table. Used to replace vendor list prices with the site's
// real ratios when computing request cost.
type PricingProvider interface {
	FetchPricing(ctx context.Context, req AccountRequest) (SitePricing, error)
}

type Registry struct {
	adapters map[string]SiteAdapter
	order    []SiteAdapter
}

func NewRegistry(adapters ...SiteAdapter) *Registry {
	r := &Registry{adapters: make(map[string]SiteAdapter, len(adapters)), order: make([]SiteAdapter, 0, len(adapters))}
	for _, adapter := range adapters {
		if adapter != nil {
			r.adapters[adapter.ID()] = adapter
			r.order = append(r.order, adapter)
		}
	}
	return r
}

// Detect tries adapters in registration order. Management providers must be
// registered before the API-only OpenAI-compatible fallback.
func (r *Registry) Detect(ctx context.Context, baseURL string) (DetectionResult, error) {
	if r == nil {
		return DetectionResult{}, fmt.Errorf("provider registry is nil")
	}
	var lastErr error
	for _, adapter := range r.order {
		result, err := adapter.Detect(ctx, baseURL)
		if err != nil {
			lastErr = err
			continue
		}
		if result.Matched {
			return result, nil
		}
	}
	if lastErr != nil {
		return DetectionResult{}, lastErr
	}
	return DetectionResult{ProviderID: "unknown"}, nil
}

func (r *Registry) Get(id string) (SiteAdapter, error) {
	if r == nil {
		return nil, fmt.Errorf("provider registry is nil")
	}
	adapter, ok := r.adapters[id]
	if !ok {
		return nil, &Error{Code: CodeUnsupported, Message: "site provider is not registered"}
	}
	return adapter, nil
}
