package provider

import (
	"context"
	"errors"
	"fmt"
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
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	AccessToken string `json:"access_token,omitempty"`
	APIKey      string `json:"api_key,omitempty"`
	Cookie      string `json:"cookie,omitempty"`
	UserID      int64  `json:"user_id,omitempty"`
}

func (c Credentials) Token() string {
	if c.AccessToken != "" {
		return c.AccessToken
	}
	return c.APIKey
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
	ID      string
	Name    string
	Group   string
	Models  []string
	Key     string
	Enabled bool
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

// ManagementCredentialResolver validates a management credential and fills
// provider-specific identity fields such as New-API-User when discoverable.
type ManagementCredentialResolver interface {
	ResolveManagementCredentials(ctx context.Context, req AccountRequest) (Credentials, error)
}

// RoutingKeyProvider exposes model-call API keys owned by a management
// session. The control plane uses this to build ccLoad channels without asking
// the user to paste the same key twice.
type RoutingKeyProvider interface {
	ListRoutingKeys(ctx context.Context, req AccountRequest) ([]RoutingKeySnapshot, error)
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

// Detect tries adapters in registration order. Dedicated providers should be
// registered before the broad New API-family fallback.
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
