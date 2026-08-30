package util

import (
	"strconv"
	"testing"
	"time"
)

// A 429 carrying Anthropic's unified reset header means the model is limited for
// the whole account. Trying sibling keys cannot succeed and only drives them all
// into cooldown, so key fallback must be blocked and the model cooled to the
// exact instant the upstream gave us.
func TestClassifyHTTPResponse429WithAnthropicResetPreventsKeyFallback(t *testing.T) {
	now := time.Now()
	reset := now.Add(90 * time.Second).Truncate(time.Second)
	headers := map[string][]string{
		"Anthropic-Ratelimit-Unified-Reset": {strconv.FormatInt(reset.Unix(), 10)},
	}

	got := classifyHTTPResponseWithMetaAt(429, headers, nil, now)

	if !got.PreventKeyFallback {
		t.Error("PreventKeyFallback=false, want true when the upstream gave a unified reset")
	}
	if !got.HasModelCooldownUntil {
		t.Fatal("HasModelCooldownUntil=false, want true")
	}
	if !got.ModelCooldownUntil.Equal(reset) {
		t.Errorf("ModelCooldownUntil=%v, want %v", got.ModelCooldownUntil, reset)
	}
	if got.ModelCooldownReason != "anthropic_unified_reset" {
		t.Errorf("ModelCooldownReason=%q, want %q", got.ModelCooldownReason, "anthropic_unified_reset")
	}
	if !got.ModelScoped {
		t.Error("ModelScoped=false, want true: 429 always cools only the requested model")
	}
}

// Header matching must be case-insensitive: Go canonicalizes incoming headers,
// but upstream casing varies and the map may be populated from other sources.
func TestClassifyHTTPResponse429ResetHeaderIsCaseInsensitive(t *testing.T) {
	now := time.Now()
	reset := now.Add(2 * time.Minute).Truncate(time.Second)

	for _, name := range []string{
		"anthropic-ratelimit-unified-reset",
		"ANTHROPIC-RATELIMIT-UNIFIED-RESET",
		"Anthropic-RateLimit-Unified-Reset",
	} {
		headers := map[string][]string{name: {strconv.FormatInt(reset.Unix(), 10)}}
		got := classifyHTTPResponseWithMetaAt(429, headers, nil, now)
		if !got.PreventKeyFallback {
			t.Errorf("header %q: PreventKeyFallback=false, want true", name)
		}
	}
}

// Without the header, behaviour must be unchanged: key fallback stays allowed.
func TestClassifyHTTPResponse429WithoutResetHeaderAllowsKeyFallback(t *testing.T) {
	now := time.Now()
	got := classifyHTTPResponseWithMetaAt(429, map[string][]string{}, nil, now)

	if got.PreventKeyFallback {
		t.Error("PreventKeyFallback=true, want false without a unified reset header")
	}
	if got.HasModelCooldownUntil {
		t.Error("HasModelCooldownUntil=true, want false: no precise instant was provided")
	}
	if !got.ModelScoped {
		t.Error("ModelScoped=false, want true")
	}
}

// Garbage or stale values must not be trusted; fall back to normal backoff.
func TestParseAnthropicRateLimitResetRejectsBadValues(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		value string
	}{
		{"not a number", "soon"},
		{"empty", ""},
		{"already past", strconv.FormatInt(now.Add(-time.Minute).Unix(), 10)},
		{"exactly now", strconv.FormatInt(now.Unix(), 10)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string][]string{"Anthropic-Ratelimit-Unified-Reset": {tc.value}}
			if _, ok := parseAnthropicRateLimitReset(headers, now); ok {
				t.Errorf("value %q was accepted, want rejected", tc.value)
			}
			got := classifyHTTPResponseWithMetaAt(429, headers, nil, now)
			if got.PreventKeyFallback {
				t.Errorf("value %q: PreventKeyFallback=true, want false", tc.value)
			}
		})
	}
}

// Whitespace around the value is tolerated.
func TestParseAnthropicRateLimitResetTrimsWhitespace(t *testing.T) {
	now := time.Now()
	reset := now.Add(time.Minute).Truncate(time.Second)
	headers := map[string][]string{
		"Anthropic-Ratelimit-Unified-Reset": {"  " + strconv.FormatInt(reset.Unix(), 10) + "  "},
	}
	until, ok := parseAnthropicRateLimitReset(headers, now)
	if !ok {
		t.Fatal("expected padded value to parse")
	}
	if !until.Equal(reset) {
		t.Errorf("until=%v, want %v", until, reset)
	}
}

// The reset header must only affect 429; other statuses keep their semantics.
func TestAnthropicResetHeaderOnlyAffects429(t *testing.T) {
	now := time.Now()
	headers := map[string][]string{
		"Anthropic-Ratelimit-Unified-Reset": {strconv.FormatInt(now.Add(time.Minute).Unix(), 10)},
	}
	for _, status := range []int{400, 401, 500, 503} {
		got := classifyHTTPResponseWithMetaAt(status, headers, nil, now)
		if got.PreventKeyFallback {
			t.Errorf("status %d: PreventKeyFallback=true, want false", status)
		}
	}
}
