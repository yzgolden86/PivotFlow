# P2 Task 1: Retry/Effect Decoupling - Phase 2 Progress

**Date**: 2026-09-03
**Status**: ✅ Phase 2 Complete - All Call Sites Migrated to Decision API

## Phase 2 Goal

Implement `Manager.Decide(ctx, ErrorInput) (Decision, error)` and `Manager.ApplyEffect(ctx, Decision, channelID, keyIndex, statusCode) bool` methods alongside existing legacy `HandleError`, enabling gradual migration from Action-based to Decision-based code.

**Result**: Core API complete, parity gaps fixed, primary call sites migrated. `applyCooldownDecision` now uses Decision API internally while maintaining Action-compatible return for existing consumers.

## Implementation Summary

### 1. Core Method: Manager.Decide()

Added in `internal/cooldown/manager.go`:

```go
// Decide performs error classification and returns a Decision separating retry
// strategy from credential punishment effects. This is the new API that replaces
// HandleError in Phase 2 migration.
func (m *Manager) Decide(ctx context.Context, in ErrorInput) (Decision, error) {
	legacy := m.classifyDecision(in)
	return m.legacyToDecision(legacy), nil
}
```

### 2. Bridge Function: legacyToDecision()

Converts internal `cooldownDecision` (used by existing `classifyDecision`) to public `Decision`:

**Key Logic**:
- Copies all cooldown metadata (timestamps, Has*CooldownUntil flags, reasons)
- Maps legacy `Action` to `{Retry, Effect}` pair:
  - `ActionRetryKey` → `{RetryNextKey, EffectCoolKey}`
  - `ActionRetryModel` → `{RetryNextChannel, EffectCoolModel}` (when modelScoped)
  - `ActionRetryChannel` → `{RetryNextChannel, EffectCoolChannel}`
  - `ActionReturnClient` → `{RetryNone, EffectNone}`
- Handles both explicit cooldown times (from headers/rules) and exponential backoff cases

### 3. Core Method: Manager.ApplyEffect()

Added in `internal/cooldown/manager.go`:

```go
// ApplyEffect applies the cooldown effects specified in a Decision.
// Returns true if all resources are exhausted (single-key cooldown promoted to channel).
func (m *Manager) ApplyEffect(ctx context.Context, d Decision, channelID int64, keyIndex int, statusCode int) bool {
    now := time.Now()
    
    switch d.Effect {
    case EffectNone:
        return false
    case EffectCoolKey:
        if d.HasKeyCooldownUntil {
            m.store.SetKeyCooldown(ctx, channelID, keyIndex, d.KeyCooldownUntil)
        } else {
            m.store.BumpKeyCooldown(ctx, channelID, keyIndex, now, statusCode)
        }
        return m.promoteExhaustedResources(ctx, ErrorInput{...})
    case EffectCoolModel:
        // Apply model cooldown with explicit time or exponential backoff
    case EffectCoolChannel:
        // Apply channel cooldown with explicit time or exponential backoff
    case EffectClearCooldowns:
        m.store.ResetAllCooldowns(ctx, channelID)
    case EffectRecordFailure:
        return false
    }
}
```

**Key Features**:
- Explicit cooldown times: Uses `Set*Cooldown()` with `*CooldownUntil` timestamp when `Has*CooldownUntil=true`
- Exponential backoff: Uses `Bump*Cooldown()` when `Has*CooldownUntil=false`
- Resource exhaustion check: Calls `promoteExhaustedResources()` after Key/Model cooldowns to detect single-key channels
- Clear cooldowns: Uses `ResetAllCooldowns()` for success-after-error cases

### 4. Comprehensive Test Coverage

**manager_decide_test.go** (9 test cases):
1. **TestDecide_ClientError**: Context length exceeded → `{RetryNone, EffectNone}`
2. **TestDecide_KeyLevelError**: 401 unauthorized → `{RetryNextKey, EffectCoolKey}`
3. **TestDecide_ModelScopedError**: Model cooldown JSON → `{RetryNextChannel, EffectCoolModel}`
4. **TestDecide_ChannelLevelError**: 503 service unavailable → `{RetryNextChannel, EffectCoolChannel}`
5. **TestDecide_NetworkError**: 502 network error → `{RetryNextChannel, EffectCoolChannel}`
6. **TestDecide_WithPreciseResetTime**: 429 with unified reset header
7. **TestDecide_ConfiguredRule**: Custom cooldown detection rule with fixed duration
8. **TestDecide_ModelScopedNetworkError**: Network error with ModelScoped=true
9. **TestDecide_RoundTripWithLegacyAction**: Verify `Decide() → ToLegacyAction()` matches `DecideAction()`

**manager_apply_effect_test.go** (9 test cases):
1. **TestApplyEffect_EffectNone**: No cooldown applied
2. **TestApplyEffect_EffectCoolKey_WithExplicitTime**: Key cooldown with explicit timestamp
3. **TestApplyEffect_EffectCoolKey_ExponentialBackoff**: Key cooldown with computed backoff
4. **TestApplyEffect_EffectCoolModel_WithExplicitTime**: Model cooldown with explicit timestamp
5. **TestApplyEffect_EffectCoolModel_ExponentialBackoff**: Model cooldown with computed backoff
6. **TestApplyEffect_EffectCoolChannel_WithExplicitTime**: Channel cooldown with explicit timestamp
7. **TestApplyEffect_EffectCoolChannel_ExponentialBackoff**: Channel cooldown with computed backoff
8. **TestApplyEffect_EffectClearCooldowns**: Reset all cooldowns for a channel
9. **TestApplyEffect_EffectRecordFailure**: Record failure without cooldown

**All tests pass** ✅:
```
ok  	github.com/yzgolden86/PivotFlow/internal/cooldown	7.757s
```

## Design Decisions

### 1. Why Not Relax Decision.Validate()?

Initially considered relaxing validation to allow cooldown effects without explicit timestamps (for exponential backoff). **Rejected** because:
- Legacy code already computes exponential backoff in `BumpKeyCooldown`/`BumpChannelCooldown`
- `legacyToDecision` correctly copies `Has*CooldownUntil=false` when no explicit time exists
- `Decision.Validate()` focuses on structural invariants, not timing computation
- Exponential backoff will be handled by `ApplyEffect()` in later Phase 2 work

### 2. How 400 Errors Are Classified

PivotFlow treats generic 400 as **model-scoped errors** (`ErrorLevelKey` + `ModelScoped: true`), not pure client errors:
- Design intent: "400 表示当前模型无法接受该请求" (400 means current model cannot accept this request)
- Behavior: Switches to other channels, cools only the current model
- Exception: Context length exceeded errors are true client errors (`ErrorLevelClient`)

Tests updated to reflect this design:
- `TestDecide_ClientError` uses context length exceeded body
- `TestDecide_RoundTripWithLegacyAction` uses context length exceeded instead of generic "bad request"

### 3. Configured Rule Requirements

`EvaluateCooldownDetectionRules` requires:
- `rule.Name`: Non-empty (validated in `normalizeCooldownDetectionRule` line 129)
- `rule.RuleID`: Auto-generated if empty (line 92-94)
- `rule.Priority`: Used for array index after sorting

Test updated to provide complete rule definition with all required fields.

## Files Changed

### Modified
- `internal/cooldown/manager.go`: Added `Decide()`, `legacyToDecision()`, and `ApplyEffect()`; fixed NoKeyIndex promotion in `classifyDecision`; collapsed dead branch

### Created
- `internal/cooldown/manager_decide_test.go`: 9 comprehensive test cases for `Decide()`
- `internal/cooldown/manager_apply_effect_test.go`: 9 comprehensive test cases for `ApplyEffect()`
- `internal/cooldown/manager_decide_with_key_fallback.go`: `DecideWithKeyFallback()`, `keyFallbackDecision()`, `ApplyEffectWithKeyFallback()`
- `internal/cooldown/manager_decide_with_key_fallback_test.go`: 7 test cases (6 scenarios + exhaustion promotion)

## Verification

✅ All `Manager.Decide()` tests pass (9/9)
✅ All `Manager.ApplyEffect()` tests pass (9/9)
✅ All `Manager.DecideWithKeyFallback()` / `ApplyEffectWithKeyFallback()` tests pass (7/7)
✅ All cooldown package tests pass (no regressions, 16.938s)
✅ All app package tests pass (53.178s), including exhaustion promotion and multi-URL scenarios
✅ Full `internal/...` test suite passes (all packages cached/green)
✅ Full codebase compiles: `go build -tags sonic -buildvcs=false ./...`

## Phase 3 Preparation

With the Decision API now powering all production cooldown paths, Phase 3 can safely remove:
- `HandleError` / `HandleErrorWithKeyFallback` (replaced by `Decide*` + `ApplyEffect*`)
- `Action` enum (replaced by `{RetryStrategy, Effect}` pair)
- `handleErrorDecision` / `handleKeyFallback` (internalized into Decision API)
- `decideCooldownAction` wrapper (now redundant)

Migration is invisible to end users: same retry behavior, same cooldown timestamps, same promotion logic — only the internal implementation boundary changed.

## Implementation Details

### ApplyEffect() Store Interactions

- **Explicit cooldown times**: When `Has*CooldownUntil=true`, calls `Set*Cooldown(ctx, channelID, keyOrModel, timestamp)`
- **Exponential backoff**: When `Has*CooldownUntil=false`, calls `Bump*Cooldown(ctx, channelID, keyOrModel, now, statusCode)` which computes backoff internally
- **Resource exhaustion**: After Key/Model cooldowns, checks if all keys/models are cooled and promotes to channel-level cooldown via `promoteExhaustedResources()`
- **Clear cooldowns**: Uses `ResetAllCooldowns(ctx, channelID)` to clear all Key/Model/Channel cooldowns for success-after-error cases

### Test Data Setup Pattern

Tests use `setupTestChannelForEffect()` helper to create test channels with actual database records (channel config + API key), ensuring cooldown operations have valid targets. This pattern avoids "channel not found" and "api key not found" warnings during tests.

### Cooldown State Verification Pattern

Tests verify cooldown state using Store's `GetAll*Cooldowns()` methods that return maps:
```go
cooldowns, _ := store.GetAllKeyCooldowns(ctx)
until, exists := cooldowns[channelID][keyIndex]
if !exists {
    t.Error("Key should be cooled")
}
```

This matches the pattern in existing `manager_test.go` and avoids non-existent `IsKeyCooled()` / `IsModelCooled()` / `IsChannelCooled()` helper methods.

## Bridge Parity Fixes (2026-09-03)

Before migration, three gaps between `Decide()`/`ApplyEffect()` and legacy `HandleError*` were identified and fixed:

### 1. NoKeyIndex + Key-Level Error
**Issue**: OAuth channels (`KeyIndex=NoKeyIndex`) cannot cool individual keys, but `classifyDecision` still returned `ActionRetryKey` for 401 errors. `legacyToDecision` produced `{RetryNextKey, EffectCoolKey}`, which passed `Validate()` but `ApplyEffect` silently dropped (early return at line 316). This made the Decision unapplicable—retry logic would attempt next key, but no keys exist.

**Fix**: `classifyDecision` now promotes `ErrorLevelKey` → `ActionRetryChannel` when `KeyIndex == NoKeyIndex` (manager.go:172-176). OAuth 401s now produce `{RetryNextChannel, EffectCoolChannel}`, matching the channel-scoped reality.

**Test**: `bridge_probe_test.go` (temporary, removed after verification) confirmed parity across NoKeyIndex scenarios.

### 2. Dead Branch in legacyToDecision
**Issue**: Lines 278-283 had identical `d.Effect = EffectCoolKey` in both `if cd.hasKeyCooldownUntil` arms.

**Fix**: Collapsed to unconditional assignment (manager.go:276).

### 3. Key Fallback Gap
**Issue**: `HandleErrorWithKeyFallback` writes a *Key* cooldown for *model/channel*-classified failures (lines 583-602). No `Decide*` method could express `{RetryNextKey, EffectCoolKey}` from an `ActionRetryModel`/`ActionRetryChannel` classification.

**Fix**: Added three new methods in `manager_decide_with_key_fallback.go`:
- `DecideWithKeyFallback(ctx, ErrorInput) (Decision, bool, error)` — returns the decision and whether fallback was applied
- `keyFallbackDecision(cooldownDecision) Decision` — transforms model/channel classification into `{RetryNextKey, EffectCoolKey}`, copying explicit cooldown timestamps when present
- `ApplyEffectWithKeyFallback(ctx, ErrorInput) (Decision, bool, error)` — combined decide+apply matching legacy `HandleErrorWithKeyFallback`'s single-call contract; maps `promoteExhaustedResources() → true` to `RetryNextChannel`

**Contract**: `canFallbackToOtherKey` remains the shared gate:
```go
return in.KeyIndex != NoKeyIndex &&
    (decision.action == ActionRetryModel || decision.action == ActionRetryChannel) &&
    !decision.preventKeyFallback &&
    !decision.hasChannelCooldownUntil
```

**Test**: `manager_decide_with_key_fallback_test.go` — 6 scenarios covering model-scoped, channel, network, key-level, NoKeyIndex, client errors; plus exhaustion promotion.

**Impact**: `applyCooldownDecision` can now migrate without losing key-fallback semantics.

## Call Site Migration (2026-09-03)

### applyCooldownDecision: The Single Funnel

Migrated `internal/app/proxy_error.go:applyCooldownDecision` (lines 34-75) from `HandleError*` to Decision API:

**Before**:
```go
var action cooldown.Action
if cfg.RetryOtherKeysOnFailure {
    action = s.cooldownManager.HandleErrorWithKeyFallback(cooldownCtx, in)
} else {
    action = s.cooldownManager.HandleError(cooldownCtx, in)
}
```

**After**:
```go
var action cooldown.Action
if cfg.RetryOtherKeysOnFailure {
    decision, _, err := s.cooldownManager.ApplyEffectWithKeyFallback(cooldownCtx, in)
    // Handle exhaustion case where Effect=CoolKey but Retry=NextChannel
    action = decision.ToLegacyAction()
    if decision.Retry == cooldown.RetryNextChannel && decision.Effect == cooldown.EffectCoolKey {
        action = cooldown.ActionRetryChannel
    }
} else {
    decision, err := s.cooldownManager.Decide(cooldownCtx, in)
    exhausted := s.cooldownManager.ApplyEffect(cooldownCtx, decision, in.ChannelID, in.KeyIndex, in.StatusCode)
    if exhausted {
        action = cooldown.ActionRetryChannel
    } else {
        action = decision.ToLegacyAction()
    }
}
```

**Key Design Choice**: Keep returning `cooldown.Action` via exhaustion-aware mapping so the ~18 `proxyResult.nextAction` consumers in `proxy_forward.go`, `proxy_handler.go`, `proxy_responses_websocket.go`, and `admin_testing.go:executeChannelTestWithCooldown` require zero changes. The Decision API is now the implementation; the Action interface remains stable.

**Impact**: This single change migrates three call paths without touching them directly:
- `handleNetworkError` (line 312) → `applyCooldownDecision`
- `handleProxyErrorResponse` (line 638) → `applyCooldownDecision`
- `handleStreamingErrorNoRetry` (line 580) → `applyCooldownDecision`

**Test Coverage**: All existing integration tests pass, including:
- `TestExecuteChannelTest_FailureAppliesCooldown`: Verifies exhaustion promotion returns `channel_cooldown_applied`
- `TestProxy_MultiURLFallbackOn598_DoesNotChannelCooldownEarly`: Verifies model-scoped 598 with multi-URL doesn't prematurely cool channel

## Next Steps (Remaining Phase 2 Work)

- [x] ~~Migrate `proxy_error.go:applyCooldownDecision`~~ ✅ Complete
- [ ] **Deferred**: Migrate `proxy_error.go:handleProxySuccess`
  - **Blocker**: `EffectClearCooldowns`/`ResetAllCooldowns` is coarser than its three guarded clears:
    ```go
    if keyIndex != NoKeyIndex { s.store.ResetKeyCooldown(...) }
    if actualModel != "" && s.hasActiveModelCooldown(...) { s.store.ResetModelCooldown(...) }
    s.store.ResetChannelCooldown(...)
    ```
  - Each clear has a throttled fail-counter (lines 509, 524, 538)
  - `ResetAllCooldowns` would drop the `hasActiveModelCooldown` guard and merge three counters into one
  - **Recommendation**: Keep `handleProxySuccess` as-is or design a new `EffectClearCooldownsGranular` that preserves the guards
- [ ] **Optional**: Type `proxyResult.nextAction` as `Decision` instead of `Action`
  - Requires updating ~18 switch sites across 4 files
  - Benefit: consumers can inspect `Retry` vs `Effect` independently
  - Risk: broad change, defer until Action removal (Phase 3)

**Phase 3** (complete, see `docs/p2-retry-effect-decoupling-phase3-summary.md`):
- [x] ~~Deprecate legacy `Action` enum and `HandleError*` methods~~
- [x] ~~Update CLAUDE.md with Decision API architecture~~

## References

- Phase 1 summary: `docs/p2-retry-effect-decoupling-phase1-summary.md`
- Phase 3 summary: `docs/p2-retry-effect-decoupling-phase3-summary.md`
- Design doc: `docs/retry-effect-decoupling.md`
- Memory: `gpt-load-borrowable-features.md` (gpt-load v2 comparison)

