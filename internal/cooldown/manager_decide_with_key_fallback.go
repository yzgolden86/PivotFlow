package cooldown

import (
	"context"
	"log"
)

// DecideWithKeyFallback applies optional independent-key relay fallback.
// Ordinary model- and channel-level failures are recorded against the selected
// Key so another Key can be tried first. Errors with explicit channel cooldown
// (e.g., WebSocket connection-slot exhaustion) retain their channel-wide semantics.
//
// Returns the Decision and a boolean indicating if key fallback was applied.
// When fallback applies, the returned Decision contains {RetryNextKey, EffectCoolKey}
// with cooldown metadata copied from the model/channel classification.
func (m *Manager) DecideWithKeyFallback(ctx context.Context, in ErrorInput) (Decision, bool, error) {
	legacy := m.classifyDecision(in)
	if canFallbackToOtherKey(in, legacy) {
		return m.keyFallbackDecision(legacy), true, nil
	}
	return m.legacyToDecision(legacy), false, nil
}

// keyFallbackDecision transforms a model/channel classification into a key cooldown
// so another Key is tried first. Copies the model/channel cooldown timestamp when
// present; otherwise ApplyEffect will use exponential backoff.
func (m *Manager) keyFallbackDecision(cd cooldownDecision) Decision {
	d := Decision{
		Retry:              RetryNextKey,
		Effect:             EffectCoolKey,
		Model:              cd.model,
		PreventKeyFallback: cd.preventKeyFallback,
	}

	// Copy model/channel cooldown time to key if present
	if cd.hasModelCooldownUntil {
		d.KeyCooldownUntil = cd.modelCooldownUntil
		d.HasKeyCooldownUntil = true
	} else if cd.hasChannelCooldownUntil {
		d.KeyCooldownUntil = cd.channelCooldownUntil
		d.HasKeyCooldownUntil = true
	}
	// When neither is present, ApplyEffect will use exponential backoff

	// Preserve the original scope for logging context
	d.ModelCooldownUntil = cd.modelCooldownUntil
	d.HasModelCooldownUntil = cd.hasModelCooldownUntil
	d.ChannelCooldownUntil = cd.channelCooldownUntil
	d.HasChannelCooldownUntil = cd.hasChannelCooldownUntil

	// Reason priority matches handleKeyFallback: prefer explicit timestamp source
	if cd.hasModelCooldownUntil || cd.modelScoped {
		d.CooldownReason = "key_fallback_model"
	} else if cd.hasChannelCooldownUntil {
		d.CooldownReason = "key_fallback_channel"
	} else {
		d.CooldownReason = "key_fallback"
	}

	return d
}

// ApplyEffectWithKeyFallback combines DecideWithKeyFallback and ApplyEffect,
// matching the legacy HandleErrorWithKeyFallback's single-call contract.
func (m *Manager) ApplyEffectWithKeyFallback(ctx context.Context, in ErrorInput) (Decision, bool, error) {
	decision, fallbackApplied, err := m.DecideWithKeyFallback(ctx, in)
	if err != nil {
		return decision, fallbackApplied, err
	}

	exhausted := m.ApplyEffect(ctx, decision, in.ChannelID, in.KeyIndex, in.StatusCode)
	if exhausted {
		// Resource exhaustion promotion: switch from RetryNextKey to RetryNextChannel
		decision.Retry = RetryNextChannel
		log.Printf("[COOLDOWN] 资源耗尽后将 RetryNextKey 升级为 RetryNextChannel (channel=%d)", in.ChannelID)
	}

	return decision, fallbackApplied, nil
}
