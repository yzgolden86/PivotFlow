package cooldown

import (
	"strconv"
	"testing"
	"time"
)

// A 429 that carries Anthropic's unified reset applies to the model across the
// whole account. Falling back to sibling keys cannot succeed and only drags every
// key into cooldown, which then evicts the channel from the candidate pool.
func TestCanFallbackToOtherKeyBlockedByUnifiedReset(t *testing.T) {
	reset := time.Now().Add(90 * time.Second)
	in := ErrorInput{
		ChannelID:  7,
		Model:      "claude-opus-5",
		KeyIndex:   0,
		StatusCode: 429,
		Headers: map[string][]string{
			"Anthropic-Ratelimit-Unified-Reset": {strconv.FormatInt(reset.Unix(), 10)},
		},
	}

	manager := &Manager{}
	decision := manager.classifyDecision(in)

	if !decision.preventKeyFallback {
		t.Fatal("preventKeyFallback=false, want true for a unified-reset 429")
	}
	if canFallbackToOtherKey(in, decision) {
		t.Error("canFallbackToOtherKey=true, want false: burning sibling keys cannot help")
	}
	if !decision.hasModelCooldownUntil {
		t.Error("hasModelCooldownUntil=false, want true")
	}
}

// Without the header the previous behaviour must be preserved: a 429 is model
// scoped and sibling keys are still worth trying.
func TestCanFallbackToOtherKeyAllowedWithoutUnifiedReset(t *testing.T) {
	in := ErrorInput{
		ChannelID:  7,
		Model:      "claude-opus-5",
		KeyIndex:   0,
		StatusCode: 429,
		Headers:    map[string][]string{},
	}

	manager := &Manager{}
	decision := manager.classifyDecision(in)

	if decision.preventKeyFallback {
		t.Error("preventKeyFallback=true, want false without a unified reset")
	}
	if !canFallbackToOtherKey(in, decision) {
		t.Error("canFallbackToOtherKey=false, want true: sibling keys may still work")
	}
}

// OAuth channels have no per-key identity, so the gate stays closed regardless.
func TestCanFallbackToOtherKeyStillFalseForOAuthChannels(t *testing.T) {
	in := ErrorInput{
		ChannelID:  7,
		Model:      "claude-opus-5",
		KeyIndex:   NoKeyIndex,
		StatusCode: 429,
		Headers:    map[string][]string{},
	}

	manager := &Manager{}
	if canFallbackToOtherKey(in, manager.classifyDecision(in)) {
		t.Error("canFallbackToOtherKey=true, want false when there is no key index")
	}
}
