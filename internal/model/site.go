package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// Site control-plane stable identifiers and status values.
const (
	SitePlatformNewAPIFamily     = "new-api-family"
	SitePlatformAnyRouter        = "anyrouter"
	SitePlatformVeloera          = "veloera"
	SitePlatformSub2API          = "sub2api"
	SitePlatformOpenAICompatible = "openai-compatible"
	SitePlatformUnknown          = "unknown"

	CredentialTypeUsernamePassword = "username_password"
	CredentialTypeAccessToken      = "access_token"
	CredentialTypeAPIKey           = "api_key"
	CredentialTypeCookie           = "cookie"

	SiteAccountStatusUnknown  = "unknown"
	SiteAccountStatusHealthy  = "healthy"
	SiteAccountStatusDegraded = "degraded"
	SiteAccountStatusExpired  = "expired"
	SiteAccountStatusDisabled = "disabled"
	SiteAccountStatusError    = "error"

	SiteTaskStatusQueued    = "queued"
	SiteTaskStatusRunning   = "running"
	SiteTaskStatusSuccess   = "success"
	SiteTaskStatusPartial   = "partial"
	SiteTaskStatusFailed    = "failed"
	SiteTaskStatusCancelled = "cancelled"
)

// Site is a normalized upstream service managed by the control plane.
type Site struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Platform           string `json:"platform"`
	BaseURL            string `json:"base_url"`
	Enabled            bool   `json:"enabled"`
	Timezone           string `json:"timezone"`
	UseSystemProxy     bool   `json:"use_system_proxy"`
	ProxyURL           string `json:"proxy_url,omitempty"`
	ExternalCheckinURL string `json:"external_checkin_url,omitempty"`
	TagsJSON           string `json:"tags_json"`
	LastProbeStatus    string `json:"last_probe_status"`
	LastError          string `json:"last_error,omitempty"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
	DeletedAt          int64  `json:"deleted_at,omitempty"`
}

// SiteAccount contains account state. CredentialCiphertext is deliberately
// excluded from JSON and may only be decrypted immediately before a provider call.
type SiteAccount struct {
	ID                          int64    `json:"id"`
	SiteID                      int64    `json:"site_id"`
	Label                       string   `json:"label"`
	CredentialType              string   `json:"credential_type"`
	CredentialCiphertext        string   `json:"-"`
	CredentialKeyVersion        string   `json:"-"`
	CredentialConfigured        bool     `json:"credential_configured"`
	CredentialRefreshConfigured bool     `json:"credential_refresh_configured"`
	CredentialExpiresAt         int64    `json:"credential_expires_at,omitempty"`
	Enabled                     bool     `json:"enabled"`
	AutoCheckin                 bool     `json:"auto_checkin"`
	AutoRefresh                 bool     `json:"auto_refresh"`
	Timezone                    string   `json:"timezone,omitempty"`
	Status                      string   `json:"status"`
	Balance                     *float64 `json:"balance,omitempty"`
	BalanceCurrency             string   `json:"balance_currency"`
	BalanceUpdatedAt            int64    `json:"balance_updated_at,omitempty"`
	LastRefreshAt               int64    `json:"last_refresh_at,omitempty"`
	LastRefreshStatus           string   `json:"last_refresh_status"`
	ConsecutiveFailures         int      `json:"consecutive_failures"`
	LastCheckinAt               int64    `json:"last_checkin_at,omitempty"`
	LastCheckinStatus           string   `json:"last_checkin_status"`
	LastError                   string   `json:"last_error,omitempty"`
	CreatedAt                   int64    `json:"created_at"`
	UpdatedAt                   int64    `json:"updated_at"`
	DeletedAt                   int64    `json:"deleted_at,omitempty"`
}

// SiteAccountModel is the last known model fact for one account.
type SiteAccountModel struct {
	SiteAccountID int64  `json:"site_account_id"`
	Model         string `json:"model"`
	RouteType     string `json:"route_type"`
	Source        string `json:"source"`
	Disabled      bool   `json:"disabled"`
	Stale         bool   `json:"stale"`
	LastSeenAt    int64  `json:"last_seen_at"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

// SiteAnnouncement is a sanitized, deduplicated upstream announcement.
type SiteAnnouncement struct {
	ID                int64  `json:"id"`
	SiteID            int64  `json:"site_id"`
	SourceKey         string `json:"source_key"`
	Title             string `json:"title"`
	ContentMarkdown   string `json:"content_markdown"`
	Level             string `json:"level"`
	SourceURL         string `json:"source_url,omitempty"`
	UpstreamCreatedAt int64  `json:"upstream_created_at,omitempty"`
	UpstreamUpdatedAt int64  `json:"upstream_updated_at,omitempty"`
	FirstSeenAt       int64  `json:"first_seen_at"`
	LastSeenAt        int64  `json:"last_seen_at"`
	ReadAt            int64  `json:"read_at,omitempty"`
	ContentHash       string `json:"content_hash"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
}

// CheckinRun aggregates one manual, scheduled, or retry batch.
type CheckinRun struct {
	ID                   int64  `json:"id"`
	Trigger              string `json:"trigger"`
	LocalDay             string `json:"local_day"`
	Timezone             string `json:"timezone"`
	Status               string `json:"status"`
	Total                int    `json:"total"`
	SuccessCount         int    `json:"success_count"`
	AlreadyCount         int    `json:"already_count"`
	BrowserRequiredCount int    `json:"browser_required_count"`
	UnsupportedCount     int    `json:"unsupported_count"`
	FailedCount          int    `json:"failed_count"`
	StartedAt            int64  `json:"started_at,omitempty"`
	FinishedAt           int64  `json:"finished_at,omitempty"`
	LastError            string `json:"last_error,omitempty"`
}

// CheckinAttempt stores the account-level result of a run.
type CheckinAttempt struct {
	ID              int64    `json:"id"`
	RunID           int64    `json:"run_id"`
	SiteAccountID   int64    `json:"site_account_id"`
	ProviderID      string   `json:"provider_id"`
	LocalDay        string   `json:"local_day"`
	TriggerScope    string   `json:"trigger_scope"`
	Status          string   `json:"status"`
	RewardText      string   `json:"reward_text,omitempty"`
	BalanceBefore   *float64 `json:"balance_before,omitempty"`
	BalanceAfter    *float64 `json:"balance_after,omitempty"`
	BalanceDelta    *float64 `json:"balance_delta,omitempty"`
	BalanceCurrency string   `json:"balance_currency,omitempty"`
	Message         string   `json:"message,omitempty"`
	ErrorCode       string   `json:"error_code,omitempty"`
	RetryAfterAt    int64    `json:"retry_after_at,omitempty"`
	StartedAt       int64    `json:"started_at,omitempty"`
	FinishedAt      int64    `json:"finished_at,omitempty"`
	AttemptNo       int      `json:"attempt_no"`
}

// SiteChannelBinding links account facts to an existing PivotFlow channel.
type SiteChannelBinding struct {
	ID                int64  `json:"id"`
	SiteAccountID     int64  `json:"site_account_id"`
	ProjectionKey     string `json:"projection_key"`
	ChannelID         int64  `json:"channel_id,omitempty"`
	Ownership         string `json:"ownership"`
	Status            string `json:"status"`
	LastProjectedHash string `json:"last_projected_hash,omitempty"`
	LastSyncStatus    string `json:"last_sync_status"`
	LastSyncError     string `json:"last_sync_error,omitempty"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
}

// SiteTask is the durable status returned by asynchronous admin operations.
type SiteTask struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	SiteID        int64  `json:"site_id,omitempty"`
	SiteAccountID int64  `json:"site_account_id,omitempty"`
	ProgressJSON  string `json:"progress_json"`
	ResultRef     string `json:"result_ref,omitempty"`
	Error         string `json:"error,omitempty"`
	CreatedAt     int64  `json:"created_at"`
	StartedAt     int64  `json:"started_at,omitempty"`
	FinishedAt    int64  `json:"finished_at,omitempty"`
	CancelledAt   int64  `json:"cancelled_at,omitempty"`
}

// SiteTaskLease prevents scheduler/manual task re-entry.
type SiteTaskLease struct {
	TaskKey    string `json:"task_key"`
	OwnerID    string `json:"owner_id"`
	LeaseUntil int64  `json:"lease_until"`
	UpdatedAt  int64  `json:"updated_at"`
}

// WebhookConfig is the single notification endpoint used by a personal
// deployment. The encrypted URL is never serialized by admin APIs.
type WebhookConfig struct {
	ID                     int64   `json:"id"`
	Enabled                bool    `json:"enabled"`
	URLCiphertext          string  `json:"-"`
	URLKeyVersion          string  `json:"-"`
	URLConfigured          bool    `json:"url_configured"`
	TelegramEnabled        bool    `json:"telegram_enabled"`
	TelegramBotCiphertext  string  `json:"-"`
	TelegramBotKeyVersion  string  `json:"-"`
	TelegramChatCiphertext string  `json:"-"`
	TelegramChatKeyVersion string  `json:"-"`
	TelegramConfigured     bool    `json:"telegram_configured"`
	TelegramUseSystemProxy bool    `json:"telegram_use_system_proxy"`
	LowBalanceEnabled      bool    `json:"low_balance_enabled"`
	LowBalanceThreshold    float64 `json:"low_balance_threshold"`
	CheckinFailureEnabled  bool    `json:"checkin_failure_enabled"`
	CooldownMinutes        int     `json:"cooldown_minutes"`
	LastDeliveryStatus     string  `json:"last_delivery_status"`
	LastDeliveryAt         int64   `json:"last_delivery_at,omitempty"`
	LastError              string  `json:"last_error,omitempty"`
	CreatedAt              int64   `json:"created_at"`
	UpdatedAt              int64   `json:"updated_at"`
}

// WebhookEventState provides cooldown deduplication without storing payloads.
type WebhookEventState struct {
	EventKey      string `json:"event_key"`
	EventType     string `json:"event_type"`
	SiteAccountID int64  `json:"site_account_id,omitempty"`
	Status        string `json:"status"`
	Attempts      int    `json:"attempts"`
	LastAttemptAt int64  `json:"last_attempt_at,omitempty"`
	DeliveredAt   int64  `json:"delivered_at,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	UpdatedAt     int64  `json:"updated_at"`
}

// SiteListFilter controls site listing.
type SiteListFilter struct {
	IncludeDeleted bool
}

// SiteAnnouncementFilter controls announcement listing.
type SiteAnnouncementFilter struct {
	SiteID int64
	Unread *bool
	Limit  int
	Offset int
}

// SiteModelFilter controls account-model listing.
type SiteModelFilter struct {
	SiteID          int64
	SiteAccountID   int64
	IncludeDisabled bool
	Limit           int
	Offset          int
}

// SiteProjectionInput is the source-of-truth payload for atomic channel projection.
type SiteProjectionInput struct {
	SiteAccountID int64
	ProjectionKey string
	Name          string
	BaseURL       string
	Protocols     []string
	Models        []string
	APIKey        string
	SourceHash    string
	// Enabled is used when creating a projection. For an existing projected
	// channel, its persisted enabled flag is a local routing decision and is
	// preserved during synchronization.
	Enabled       bool
	Force         bool
}

// SiteProjectionResult reports the idempotent projection outcome.
type SiteProjectionResult struct {
	Binding *SiteChannelBinding `json:"binding"`
	Channel *Config             `json:"channel"`
	Action  string              `json:"action"`
}

// SiteProjectionSourceHash fingerprints only fields owned by site projection.
// The API key is hashed before the canonical payload is encoded, so the
// returned fingerprint never embeds credential material.
func SiteProjectionSourceHash(baseURL string, protocols, models []string, apiKey string, enabled bool) string {
	canonical := func(values []string, lower bool) []string {
		seen := make(map[string]struct{}, len(values))
		out := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if lower {
				value = strings.ToLower(value)
			}
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			out = append(out, value)
		}
		sort.Strings(out)
		return out
	}
	keyHash := sha256.Sum256([]byte(strings.TrimSpace(apiKey)))
	payload, _ := json.Marshal(struct {
		BaseURL   string   `json:"base_url"`
		Protocols []string `json:"protocols"`
		Models    []string `json:"models"`
		KeyHash   string   `json:"key_hash"`
		Enabled   bool     `json:"enabled"`
	}{
		BaseURL:   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Protocols: canonical(protocols, true),
		Models:    canonical(models, false),
		KeyHash:   hex.EncodeToString(keyHash[:]),
		Enabled:   enabled,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
