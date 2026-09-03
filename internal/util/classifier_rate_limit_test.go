package util

import (
	"testing"
	"time"
)

func TestParseRateLimitResetHeaders(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		headers map[string][]string
		want    time.Time
		wantOk  bool
	}{
		{
			name: "anthropic-ratelimit-requests-reset",
			headers: map[string][]string{
				"Anthropic-Ratelimit-Requests-Reset": {"1788436800"}, // 2026-09-03 12:00:00 UTC
			},
			want:   now.Add(MaxUpstreamResetDuration), // Clamped to 1 hour (2 hours > max)
			wantOk: true,
		},
		{
			name: "anthropic-ratelimit-tokens-reset",
			headers: map[string][]string{
				"Anthropic-Ratelimit-Tokens-Reset": {"1788440400"}, // 2026-09-03 13:00:00 UTC
			},
			want:   now.Add(MaxUpstreamResetDuration), // Clamped to 1 hour (3 hours > max)
			wantOk: true,
		},
		{
			name: "x-ratelimit-reset-requests",
			headers: map[string][]string{
				"X-Ratelimit-Reset-Requests": {"1788444000"}, // 2026-09-03 14:00:00 UTC
			},
			want:   now.Add(MaxUpstreamResetDuration), // Clamped to 1 hour (4 hours > max)
			wantOk: true,
		},
		{
			name: "x-ratelimit-reset",
			headers: map[string][]string{
				"X-Ratelimit-Reset": {"1788447600"}, // 2026-09-03 15:00:00 UTC
			},
			want:   now.Add(MaxUpstreamResetDuration), // Clamped to 1 hour (5 hours > max)
			wantOk: true,
		},
		{
			name: "priority: requests-reset over tokens-reset",
			headers: map[string][]string{
				"Anthropic-Ratelimit-Requests-Reset": {"1788436800"}, // 12:00
				"Anthropic-Ratelimit-Tokens-Reset":   {"1788440400"}, // 13:00
			},
			want:   now.Add(MaxUpstreamResetDuration), // Should use requests-reset, clamped
			wantOk: true,
		},
		{
			name: "case insensitive matching",
			headers: map[string][]string{
				"anthropic-RateLimit-Requests-RESET": {"1788436800"},
			},
			want:   now.Add(MaxUpstreamResetDuration), // Clamped
			wantOk: true,
		},
		{
			name: "short reset time not clamped",
			headers: map[string][]string{
				"X-Ratelimit-Reset": {"1788431400"}, // 2026-09-03 10:30:00 UTC (30 minutes)
			},
			want:   time.Unix(1788431400, 0), // Not clamped
			wantOk: true,
		},
		{
			name: "past timestamp ignored",
			headers: map[string][]string{
				"X-Ratelimit-Reset": {"1788429600"}, // 2026-09-03 10:00:00 UTC (exactly now)
			},
			want:   time.Time{},
			wantOk: false,
		},
		{
			name: "invalid format ignored",
			headers: map[string][]string{
				"X-Ratelimit-Reset": {"not-a-number"},
			},
			want:   time.Time{},
			wantOk: false,
		},
		{
			name:    "no rate-limit headers",
			headers: map[string][]string{},
			want:    time.Time{},
			wantOk:  false,
		},
		{
			name: "very long reset clamped to MaxUpstreamResetDuration",
			headers: map[string][]string{
				"X-Ratelimit-Reset": {"1788516000"}, // 2026-09-04 10:00:00 UTC (24 hours later)
			},
			want:   now.Add(MaxUpstreamResetDuration), // Clamped to 1 hour
			wantOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseRateLimitResetHeaders(tt.headers, now)
			if ok != tt.wantOk {
				t.Errorf("parseRateLimitResetHeaders() ok = %v, want %v", ok, tt.wantOk)
				t.Logf("headers: %+v", tt.headers)
				t.Logf("got time: %v, want time: %v", got, tt.want)
				return
			}
			if !got.Equal(tt.want) {
				t.Errorf("parseRateLimitResetHeaders() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClassifyHTTPResponseWithMeta_RateLimitResetHeaders(t *testing.T) {
	now := time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		statusCode int
		headers    map[string][]string
		body       []byte
		want       HTTPResponseClassification
	}{
		{
			name:       "429 with anthropic-ratelimit-requests-reset",
			statusCode: 429,
			headers: map[string][]string{
				"Anthropic-Ratelimit-Requests-Reset": {"1788436800"},
			},
			body: []byte(`{"error":{"type":"rate_limit_error","message":"Rate limited"}}`),
			want: HTTPResponseClassification{
				Level:                 ErrorLevelKey,
				ModelScoped:           true,
				PreventKeyFallback:    true,
				ModelCooldownUntil:    now.Add(MaxUpstreamResetDuration), // Clamped
				HasModelCooldownUntil: true,
				ModelCooldownReason:   "rate_limit_reset_header",
			},
		},
		{
			name:       "429 with x-ratelimit-reset",
			statusCode: 429,
			headers: map[string][]string{
				"X-Ratelimit-Reset": {"1788436800"},
			},
			body: []byte(`{"error":"rate limited"}`),
			want: HTTPResponseClassification{
				Level:                 ErrorLevelKey,
				ModelScoped:           true,
				PreventKeyFallback:    true,
				ModelCooldownUntil:    now.Add(MaxUpstreamResetDuration), // Clamped
				HasModelCooldownUntil: true,
				ModelCooldownReason:   "rate_limit_reset_header",
			},
		},
		{
			name:       "429 unified header takes precedence",
			statusCode: 429,
			headers: map[string][]string{
				"Anthropic-Ratelimit-Unified-Reset":  {"1788440400"}, // 13:00
				"Anthropic-Ratelimit-Requests-Reset": {"1788436800"}, // 12:00
			},
			body: []byte(`{"error":"rate limited"}`),
			want: HTTPResponseClassification{
				Level:                 ErrorLevelKey,
				ModelScoped:           true,
				PreventKeyFallback:    true,
				ModelCooldownUntil:    now.Add(MaxUpstreamResetDuration), // Uses unified reset, clamped
				HasModelCooldownUntil: true,
				ModelCooldownReason:   "anthropic_unified_reset",
			},
		},
		{
			name:       "429 without reset headers uses exponential backoff",
			statusCode: 429,
			headers:    map[string][]string{},
			body:       []byte(`{"error":"rate limited"}`),
			want: HTTPResponseClassification{
				Level:                 ErrorLevelKey,
				ModelScoped:           true,
				PreventKeyFallback:    false,
				HasModelCooldownUntil: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyHTTPResponseWithMetaAt(tt.statusCode, tt.headers, tt.body, now)
			if got.Level != tt.want.Level {
				t.Errorf("Level = %v, want %v", got.Level, tt.want.Level)
			}
			if got.ModelScoped != tt.want.ModelScoped {
				t.Errorf("ModelScoped = %v, want %v", got.ModelScoped, tt.want.ModelScoped)
			}
			if got.PreventKeyFallback != tt.want.PreventKeyFallback {
				t.Errorf("PreventKeyFallback = %v, want %v", got.PreventKeyFallback, tt.want.PreventKeyFallback)
			}
			if got.HasModelCooldownUntil != tt.want.HasModelCooldownUntil {
				t.Errorf("HasModelCooldownUntil = %v, want %v", got.HasModelCooldownUntil, tt.want.HasModelCooldownUntil)
			}
			if tt.want.HasModelCooldownUntil && !got.ModelCooldownUntil.Equal(tt.want.ModelCooldownUntil) {
				t.Errorf("ModelCooldownUntil = %v, want %v", got.ModelCooldownUntil, tt.want.ModelCooldownUntil)
			}
			if got.ModelCooldownReason != tt.want.ModelCooldownReason {
				t.Errorf("ModelCooldownReason = %q, want %q", got.ModelCooldownReason, tt.want.ModelCooldownReason)
			}
		})
	}
}
