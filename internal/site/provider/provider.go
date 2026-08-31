package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
}

type AccountRequest struct {
	BaseURL     string
	ProxyURL    string
	Credentials Credentials
}

type RefreshAccountRequest = AccountRequest

type AccountSnapshot struct {
	Username string
	Balance  *float64
	Currency string
	Status   string
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
	UpstreamAt      int64
	ContentHash     string
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
	// ModelRatio scales the input price. Output is additionally scaled by
	// CompletionRatio; cache reads and writes by their own ratios.
	ModelRatio         float64
	CompletionRatio    float64
	CacheRatio         float64
	CacheCreationRatio float64
	// Groups lists the site groups this model is available to. A price only
	// applies to a request whose token belongs to one of these groups.
	Groups []string
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
