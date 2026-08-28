package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrSystemAccessTokenNotFound = errors.New("system access token not found")

const (
	SystemAccessScopeChannelsRead = "channels.read"
	SystemAccessScopeRoutesRead   = "routes.read"
	SystemAccessScopeLogsRead     = "logs.read"
	SystemAccessScopeMetricsRead  = "metrics.read"
)

var SystemAccessScopes = []string{
	SystemAccessScopeChannelsRead,
	SystemAccessScopeRoutesRead,
	SystemAccessScopeLogsRead,
	SystemAccessScopeMetricsRead,
}

// SystemAccessToken is a management-plane token used by diagnostic clients.
// Token stores only the SHA-256 digest; the plaintext is returned once at creation.
type SystemAccessToken struct {
	ID          int64    `json:"id"`
	Token       string   `json:"-"`
	TokenHint   string   `json:"token_hint"`
	Description string   `json:"description"`
	Scopes      []string `json:"scopes"`
	CreatedAt   int64    `json:"created_at"`
	LastUsedAt  *int64   `json:"last_used_at,omitempty"`
	ExpiresAt   int64    `json:"expires_at"`
	IsActive    bool     `json:"is_active"`
}

func (t *SystemAccessToken) IsValid(now time.Time) bool {
	return t != nil && t.IsActive && (t.ExpiresAt <= 0 || now.UnixMilli() <= t.ExpiresAt)
}

func (t *SystemAccessToken) HasScope(scope string) bool {
	if t == nil {
		return false
	}
	for _, item := range t.Scopes {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(scope)) {
			return true
		}
	}
	return false
}

func NormalizeSystemAccessScopes(scopes []string) ([]string, error) {
	allowed := make(map[string]struct{}, len(SystemAccessScopes))
	for _, scope := range SystemAccessScopes {
		allowed[scope] = struct{}{}
	}
	result := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, raw := range scopes {
		scope := strings.ToLower(strings.TrimSpace(raw))
		if _, ok := allowed[scope]; !ok {
			return nil, errors.New("unsupported system access token scope: " + raw)
		}
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		result = append(result, scope)
	}
	if len(result) == 0 {
		return nil, errors.New("at least one system access token scope is required")
	}
	return result, nil
}

func HashSystemAccessToken(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func MaskSystemAccessToken(value string) string {
	if len(value) <= 10 {
		return "********"
	}
	return value[:6] + "****" + value[len(value)-4:]
}

func (t SystemAccessToken) MarshalJSON() ([]byte, error) {
	type response struct {
		ID          int64    `json:"id"`
		TokenHint   string   `json:"token_hint"`
		Description string   `json:"description"`
		Scopes      []string `json:"scopes"`
		CreatedAt   int64    `json:"created_at"`
		LastUsedAt  *int64   `json:"last_used_at,omitempty"`
		ExpiresAt   int64    `json:"expires_at"`
		IsActive    bool     `json:"is_active"`
	}
	return json.Marshal(response{t.ID, t.TokenHint, t.Description, t.Scopes, t.CreatedAt, t.LastUsedAt, t.ExpiresAt, t.IsActive})
}
