package app

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
)

func TestHandleCreateAuthTokenReturnsCompletePublicMetadataAndPlaintext(t *testing.T) {
	server := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "auth-token-create-metadata-test")
	if err != nil {
		t.Fatal(err)
	}
	server.siteControl.cipher = cipher

	expiresAt := int64(1893456000000)
	request := newJSONRequest(t, http.MethodPost, "/admin/auth-tokens", map[string]any{
		"description":              "client onboarding",
		"expires_at":               expiresAt,
		"is_active":                false,
		"allowed_models":           []string{"gpt-5", "claude-sonnet"},
		"allowed_channel_ids":      []int64{3, 5},
		"channel_restriction_mode": model.ChannelRestrictionModeDeny,
		"cost_limit_usd":           1.25,
		"max_concurrency":          4,
		"allowed_protocols":        []string{"openai", "anthropic"},
	})
	createContext, recorder := newTestContext(t, request)
	server.HandleCreateAuthToken(createContext)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d", recorder.Code, http.StatusOK)
	}

	response := mustParseAPIResponse[json.RawMessage](t, recorder.Body.Bytes())
	if !response.Success {
		t.Fatalf("success=false, error=%q", response.Error)
	}

	var fields map[string]json.RawMessage
	mustUnmarshalJSON(t, response.Data, &fields)
	for _, key := range []string{
		"id",
		"token",
		"token_hint",
		"token_recoverable",
		"description",
		"created_at",
		"expires_at",
		"is_active",
		"success_count",
		"failure_count",
		"stream_avg_ttfb",
		"non_stream_avg_rt",
		"stream_count",
		"non_stream_count",
		"prompt_tokens_total",
		"completion_tokens_total",
		"cache_read_tokens_total",
		"cache_creation_tokens_total",
		"total_cost_usd",
		"effective_cost_usd",
		"cost_used_usd",
		"cost_limit_usd",
		"allowed_models",
		"allowed_channel_ids",
		"channel_restriction_mode",
		"max_concurrency",
		"allowed_protocols",
	} {
		if _, ok := fields[key]; !ok {
			t.Errorf("response is missing public metadata field %q", key)
		}
	}

	var created struct {
		ID                     int64    `json:"id"`
		Token                  string   `json:"token"`
		TokenHint              string   `json:"token_hint"`
		TokenRecoverable       bool     `json:"token_recoverable"`
		Description            string   `json:"description"`
		CreatedAt              string   `json:"created_at"`
		ExpiresAt              *int64   `json:"expires_at"`
		IsActive               bool     `json:"is_active"`
		CostUsedUSD            float64  `json:"cost_used_usd"`
		CostLimitUSD           float64  `json:"cost_limit_usd"`
		AllowedModels          []string `json:"allowed_models"`
		AllowedChannelIDs      []int64  `json:"allowed_channel_ids"`
		ChannelRestrictionMode string   `json:"channel_restriction_mode"`
		MaxConcurrency         int      `json:"max_concurrency"`
		AllowedProtocols       []string `json:"allowed_protocols"`
	}
	mustUnmarshalJSON(t, response.Data, &created)

	if created.ID == 0 || created.Description != "client onboarding" {
		t.Fatalf("unexpected created token identity: id=%d description=%q", created.ID, created.Description)
	}
	if created.Token == "" || !strings.HasPrefix(created.Token, "sk-pf-") {
		t.Fatal("create response must include the one-time plaintext token")
	}
	if created.TokenHint == "" || !created.TokenRecoverable {
		t.Fatalf("token_hint=%q token_recoverable=%v, want non-empty hint and recoverable=true", created.TokenHint, created.TokenRecoverable)
	}
	if created.TokenHint != model.MaskToken(created.Token) {
		t.Fatalf("token_hint=%q does not match the one-time plaintext token", created.TokenHint)
	}
	if created.CreatedAt == "" {
		t.Fatal("created_at is empty")
	}
	if created.ExpiresAt == nil || *created.ExpiresAt != expiresAt {
		t.Fatalf("expires_at=%v, want %d", created.ExpiresAt, expiresAt)
	}
	if created.IsActive {
		t.Fatal("is_active=true, want false")
	}
	if created.CostUsedUSD != 0 {
		t.Fatalf("cost_used_usd=%v, want 0", created.CostUsedUSD)
	}
	if created.CostLimitUSD != 1.25 {
		t.Fatalf("cost_limit_usd=%v, want 1.25", created.CostLimitUSD)
	}
	if !slices.Equal(created.AllowedModels, []string{"gpt-5", "claude-sonnet"}) {
		t.Fatalf("allowed_models=%v, want [gpt-5 claude-sonnet]", created.AllowedModels)
	}
	if !slices.Equal(created.AllowedChannelIDs, []int64{3, 5}) {
		t.Fatalf("allowed_channel_ids=%v, want [3 5]", created.AllowedChannelIDs)
	}
	if created.ChannelRestrictionMode != model.ChannelRestrictionModeDeny {
		t.Fatalf("channel_restriction_mode=%q, want deny", created.ChannelRestrictionMode)
	}
	if created.MaxConcurrency != 4 {
		t.Fatalf("max_concurrency=%d, want 4", created.MaxConcurrency)
	}
	if !slices.Equal(created.AllowedProtocols, []string{"openai", "anthropic"}) {
		t.Fatalf("allowed_protocols=%v, want [openai anthropic]", created.AllowedProtocols)
	}

	stored, err := server.store.GetAuthToken(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Token != model.HashToken(created.Token) || stored.TokenCiphertext == "" {
		t.Fatal("stored token is not hash-only with encrypted plaintext")
	}
	if !slices.Equal(stored.AllowedProtocols, []string{"openai", "anthropic"}) || stored.CostLimitUSD() != 1.25 {
		t.Fatal("creation limits were not persisted")
	}

	body := recorder.Body.Bytes()
	if strings.Contains(string(body), stored.Token) {
		t.Fatal("create response exposed the stored token hash")
	}
	if strings.Contains(string(body), stored.TokenCiphertext) {
		t.Fatal("create response exposed token ciphertext")
	}
	for _, forbiddenKey := range []string{"token_hash", "token_ciphertext"} {
		if _, ok := fields[forbiddenKey]; ok {
			t.Fatalf("create response exposed forbidden field %q", forbiddenKey)
		}
	}
}
