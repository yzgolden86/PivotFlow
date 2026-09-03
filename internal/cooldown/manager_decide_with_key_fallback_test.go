package cooldown

import (
	"context"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/testutil"
)

// TestDecideWithKeyFallback verifies that DecideWithKeyFallback produces
// {RetryNextKey, EffectCoolKey} for model/channel failures when key fallback
// applies, preserving cooldown timestamps when present.
func TestDecideWithKeyFallback(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	cases := []struct {
		name           string
		in             ErrorInput
		wantFallback   bool
		wantRetry      RetryStrategy
		wantEffect     Effect
		wantHasKeyTime bool
	}{
		{
			name: "model_scoped_with_explicit_time",
			in: ErrorInput{
				ChannelID:  1,
				KeyIndex:   0,
				Model:      "gpt-4",
				StatusCode: 429,
				ErrorBody:  []byte(`{"error":{"code":"model_overloaded"}}`),
			},
			wantFallback:   true,
			wantRetry:      RetryNextKey,
			wantEffect:     EffectCoolKey,
			wantHasKeyTime: false, // No explicit time in this example
		},
		{
			name: "channel_failure_503",
			in: ErrorInput{
				ChannelID:  1,
				KeyIndex:   0,
				Model:      "gpt-4",
				StatusCode: 503,
				ErrorBody:  []byte(`{"error":{"message":"service unavailable"}}`),
			},
			wantFallback:   true,
			wantRetry:      RetryNextKey,
			wantEffect:     EffectCoolKey,
			wantHasKeyTime: false,
		},
		{
			name: "network_error",
			in: ErrorInput{
				ChannelID:      1,
				KeyIndex:       0,
				Model:          "gpt-4",
				StatusCode:     0,
				IsNetworkError: true,
			},
			wantFallback:   true,
			wantRetry:      RetryNextKey,
			wantEffect:     EffectCoolKey,
			wantHasKeyTime: false,
		},
		{
			name: "key_level_401_no_fallback",
			in: ErrorInput{
				ChannelID:  1,
				KeyIndex:   0,
				Model:      "gpt-4",
				StatusCode: 401,
				ErrorBody:  []byte(`{"error":{"message":"invalid api key"}}`),
			},
			wantFallback: false,
			wantRetry:    RetryNextKey,
			wantEffect:   EffectCoolKey,
		},
		{
			name: "NoKeyIndex_no_fallback",
			in: ErrorInput{
				ChannelID:  1,
				KeyIndex:   NoKeyIndex,
				Model:      "gpt-4",
				StatusCode: 503,
			},
			wantFallback: false,
			wantRetry:    RetryNextChannel,
			wantEffect:   EffectCoolModel, // 503 + Model promotes to model-scoped
		},
		{
			name: "client_error_400_no_fallback",
			in: ErrorInput{
				ChannelID:  1,
				KeyIndex:   0,
				Model:      "gpt-4",
				StatusCode: 400,
				ErrorBody:  []byte(`{"error":{"code":"context_length_exceeded"}}`),
			},
			wantFallback: false,
			wantRetry:    RetryNone,
			wantEffect:   EffectNone,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, fallback, err := mgr.DecideWithKeyFallback(context.Background(), tc.in)
			if err != nil {
				t.Fatalf("DecideWithKeyFallback: %v", err)
			}

			if fallback != tc.wantFallback {
				t.Errorf("fallback=%v, want %v", fallback, tc.wantFallback)
			}
			if d.Retry != tc.wantRetry {
				t.Errorf("Retry=%v, want %v", d.Retry, tc.wantRetry)
			}
			if d.Effect != tc.wantEffect {
				t.Errorf("Effect=%v, want %v", d.Effect, tc.wantEffect)
			}

			if tc.wantHasKeyTime && !d.HasKeyCooldownUntil {
				t.Error("Expected explicit key cooldown time but HasKeyCooldownUntil=false")
			}

			if verr := d.Validate(); verr != nil {
				t.Errorf("Decision.Validate() failed: %v", verr)
			}

			// Round-trip check: fallback decisions must convert back to ActionRetryKey
			if fallback && d.ToLegacyAction() != ActionRetryKey {
				t.Errorf("Key fallback decision converted to %v, want ActionRetryKey", d.ToLegacyAction())
			}
		})
	}
}

// TestApplyEffectWithKeyFallback verifies the combined decide+apply path,
// including resource exhaustion promotion from RetryNextKey to RetryNextChannel.
func TestApplyEffectWithKeyFallback(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	// Create a single-key channel for exhaustion testing
	ctx := context.Background()
	channelID, keyIndex := setupTestChannelForEffect(t, store, "single-key-fallback")

	in := ErrorInput{
		ChannelID:  channelID,
		KeyIndex:   keyIndex,
		Model:      "gpt-4",
		StatusCode: 503,
		ErrorBody:  []byte(`{"error":{"message":"service unavailable"}}`),
	}

	decision, fallback, err := mgr.ApplyEffectWithKeyFallback(ctx, in)
	if err != nil {
		t.Fatalf("ApplyEffectWithKeyFallback: %v", err)
	}

	if !fallback {
		t.Error("Expected key fallback to apply for 503 with valid KeyIndex")
	}

	// Single-key channel should trigger exhaustion promotion
	if decision.Retry != RetryNextChannel {
		t.Errorf("After single-key exhaustion, Retry=%v, want RetryNextChannel", decision.Retry)
	}

	// Verify the key was cooled
	cooldowns, _ := store.GetAllKeyCooldowns(ctx)
	until, exists := cooldowns[channelID][keyIndex]
	if !exists {
		t.Error("Key should be cooled after key fallback")
	}
	if exists && until.Before(time.Now()) {
		t.Error("Key cooldown should be in the future")
	}
}
