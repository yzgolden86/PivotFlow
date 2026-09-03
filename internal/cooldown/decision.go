// Package cooldown provides retry/effect decision types for decoupled error handling.
package cooldown

import (
	"errors"
	"time"
)

// RetryStrategy determines what to attempt next after an error.
type RetryStrategy int

const (
	// RetryNone stops the retry loop and returns the result to the client.
	RetryNone RetryStrategy = iota
	// RetryNextKey tries the next available key on the same channel.
	RetryNextKey
	// RetryNextURL tries the next URL on the same channel with the same key.
	RetryNextURL
	// RetryNextChannel switches to the next available channel.
	RetryNextChannel
	// RetryRefreshToken refreshes OAuth credentials and retries the same endpoint.
	RetryRefreshToken
)

// Effect determines how to reward or punish an attempt.
type Effect int

const (
	// EffectNone performs no side effects (e.g., client errors, protocol capability negotiation).
	EffectNone Effect = iota
	// EffectCoolKey cools the current key, preventing its use until cooldown expires.
	EffectCoolKey
	// EffectCoolModel cools the model on the current channel.
	EffectCoolModel
	// EffectCoolChannel cools the entire channel.
	EffectCoolChannel
	// EffectClearCooldowns clears key, model, and channel cooldowns (success case).
	EffectClearCooldowns
	// EffectClearKeyCooldown clears only the key cooldown (granular success case).
	EffectClearKeyCooldown
	// EffectClearModelCooldown clears only the model cooldown (granular success case).
	EffectClearModelCooldown
	// EffectClearChannelCooldown clears only the channel cooldown (granular success case).
	EffectClearChannelCooldown
	// EffectRecordFailure records the failure without applying cooldown.
	EffectRecordFailure
)

// Decision separates retry logic from credential punishment.
// This decoupling eliminates special-case branches for protocol negotiation,
// client errors, and planned connection rotation.
type Decision struct {
	// Retry determines what to attempt next.
	Retry RetryStrategy
	// Effect determines how to punish or reward the attempt.
	Effect Effect

	// Cooldown metadata (only meaningful when Effect requires it)
	KeyCooldownUntil        time.Time
	HasKeyCooldownUntil     bool
	ModelCooldownUntil      time.Time
	HasModelCooldownUntil   bool
	ChannelCooldownUntil    time.Time
	HasChannelCooldownUntil bool
	CooldownReason          string

	// Model scope metadata (for model-level effects)
	Model              string
	PreventKeyFallback bool // When true, trying other keys cannot succeed
}

// Validate ensures the decision is internally consistent.
// Returns an error if the decision violates invariants.
func (d Decision) Validate() error {
	// Enforce: one attempt produces at most one cooldown effect
	// Check HasXxxCooldownUntil flags, not Effect enum (Effect only declares intent)
	cooldownEffects := 0
	if d.HasKeyCooldownUntil {
		cooldownEffects++
	}
	if d.HasModelCooldownUntil {
		cooldownEffects++
	}
	if d.HasChannelCooldownUntil {
		cooldownEffects++
	}
	if cooldownEffects > 1 {
		return errors.New("decision must produce at most one cooldown effect")
	}

	// RetryNone should not cool resources that won't be retried
	if d.Retry == RetryNone && (d.Effect == EffectCoolKey || d.Effect == EffectCoolModel || d.Effect == EffectCoolChannel) {
		return errors.New("RetryNone should not cool resources that won't be retried")
	}

	// Effect-specific validations
	// Note: HasXxxCooldownUntil can be false for exponential backoff scenarios
	// Only validate when explicit cooldown time is present
	if d.HasKeyCooldownUntil && d.KeyCooldownUntil.IsZero() {
		return errors.New("HasKeyCooldownUntil=true but KeyCooldownUntil is zero")
	}
	if d.Effect == EffectCoolModel {
		if d.Model == "" {
			return errors.New("EffectCoolModel requires non-empty Model")
		}
		if d.HasModelCooldownUntil && d.ModelCooldownUntil.IsZero() {
			return errors.New("HasModelCooldownUntil=true but ModelCooldownUntil is zero")
		}
	}
	if d.HasChannelCooldownUntil && d.ChannelCooldownUntil.IsZero() {
		return errors.New("HasChannelCooldownUntil=true but ChannelCooldownUntil is zero")
	}

	return nil
}

// ToLegacyAction converts Decision to the legacy Action enum for backward compatibility.
// This bridge enables gradual migration from Action-based to Decision-based code.
func (d Decision) ToLegacyAction() Action {
	// Priority: Effect takes precedence for determining Action
	// (Action conflates retry strategy and effect)
	switch d.Effect {
	case EffectCoolKey:
		return ActionRetryKey
	case EffectCoolModel:
		return ActionRetryModel
	case EffectCoolChannel:
		return ActionRetryChannel
	case EffectClearCooldowns:
		return ActionReturnClient // Success case stops retry loop
	case EffectNone:
		// For EffectNone, use Retry strategy to determine Action
		switch d.Retry {
		case RetryNextKey:
			return ActionRetryKey
		case RetryNextChannel, RetryNextURL:
			return ActionRetryChannel
		case RetryNone:
			return ActionReturnClient
		case RetryRefreshToken:
			return ActionRetryKey // Refresh and retry same channel
		default:
			return ActionReturnClient
		}
	case EffectRecordFailure:
		// Record failure without cooldown, but still retry if strategy says so
		switch d.Retry {
		case RetryNextKey:
			return ActionRetryKey
		case RetryNextChannel, RetryNextURL:
			return ActionRetryChannel
		default:
			return ActionReturnClient
		}
	default:
		return ActionReturnClient
	}
}

// ActionToDecision converts legacy Action to Decision for gradual migration.
// This is a lossy conversion because Action conflates retry and effect.
func ActionToDecision(a Action) Decision {
	switch a {
	case ActionRetryKey:
		return Decision{
			Retry:  RetryNextKey,
			Effect: EffectCoolKey,
		}
	case ActionRetryModel:
		return Decision{
			Retry:  RetryNextChannel,
			Effect: EffectCoolModel,
		}
	case ActionRetryChannel:
		return Decision{
			Retry:  RetryNextChannel,
			Effect: EffectCoolChannel,
		}
	case ActionReturnClient:
		return Decision{
			Retry:  RetryNone,
			Effect: EffectNone,
		}
	default:
		return Decision{
			Retry:  RetryNone,
			Effect: EffectNone,
		}
	}
}

// String returns a human-readable representation of RetryStrategy.
func (r RetryStrategy) String() string {
	switch r {
	case RetryNone:
		return "RetryNone"
	case RetryNextKey:
		return "RetryNextKey"
	case RetryNextURL:
		return "RetryNextURL"
	case RetryNextChannel:
		return "RetryNextChannel"
	case RetryRefreshToken:
		return "RetryRefreshToken"
	default:
		return "RetryStrategy(unknown)"
	}
}

// String returns a human-readable representation of Effect.
func (e Effect) String() string {
	switch e {
	case EffectNone:
		return "EffectNone"
	case EffectCoolKey:
		return "EffectCoolKey"
	case EffectCoolModel:
		return "EffectCoolModel"
	case EffectCoolChannel:
		return "EffectCoolChannel"
	case EffectClearCooldowns:
		return "EffectClearCooldowns"
	case EffectClearKeyCooldown:
		return "EffectClearKeyCooldown"
	case EffectClearModelCooldown:
		return "EffectClearModelCooldown"
	case EffectClearChannelCooldown:
		return "EffectClearChannelCooldown"
	case EffectRecordFailure:
		return "EffectRecordFailure"
	default:
		return "Effect(unknown)"
	}
}
