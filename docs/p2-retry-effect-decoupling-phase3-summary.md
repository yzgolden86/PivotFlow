# P2 Task 1: Retry/Effect Decoupling - Phase 3 Summary

**Date**: 2026-09-03
**Status**: ✅ Phase 3 Complete - Legacy API Deprecated, Documentation Updated

## Phase 3 Goal

Deprecate the legacy `Action` enum and `HandleError*` methods now that all production code uses the Decision API, and update CLAUDE.md to document the new architecture.

## Changes

### 1. Deprecated Legacy API (`internal/cooldown/manager.go`)

Added deprecation notices to guide future maintainers:

**Action type** (line 16):
```go
// Deprecated: Use Decision with separate RetryStrategy and Effect fields for new
// code. Action conflates "what to try next" with "how to punish credentials",
// making it impossible to express scenarios like "retry next key but cool the
// model" (key fallback) or independently reason about retry vs punishment logic.
//
// Migration path:
//   - Replace HandleError() calls with Decide() + ApplyEffect()
//   - Replace HandleErrorWithKeyFallback() with ApplyEffectWithKeyFallback()
//   - Use Decision.ToLegacyAction() during transition if Action-typed fields remain
```

**HandleError method** (line 434):
```go
// Deprecated: Use Decide() + ApplyEffect() for new code. This method remains for
// backward compatibility and existing tests but conflates retry strategy with
// credential punishment. The Decision API separates these concerns and supports
// fine-grained control over exhaustion promotion.
```

**HandleErrorWithKeyFallback method** (line 448):
```go
// Deprecated: Use DecideWithKeyFallback() or ApplyEffectWithKeyFallback() for new
// code. This method remains for backward compatibility and existing tests.
```

### 2. Updated CLAUDE.md

Rewrote the "故障切换" section to lead with the Decision API:

- Documents `Decide()` / `ApplyEffect()` / `DecideWithKeyFallback()` / `ApplyEffectWithKeyFallback()` as the production API
- Explains `Decision` structure: `{RetryStrategy, Effect}` pair, cooldown timestamps, reasons
- Notes `HandleError*` and `Action` are deprecated but remain for backward compatibility
- Describes key fallback mechanism: produces `{RetryNextKey, EffectCoolKey}` with model cooldown time copied to key cooldown

### 3. Preserved Backward Compatibility

**No breaking changes:**
- All deprecated methods still work identically
- Existing tests using `HandleError*` continue to pass (32 tests in `manager_test.go`, `detection_test.go`, `manager_1308_test.go`)
- `proxyResult.nextAction` remains `Action`-typed; `applyCooldownDecision` returns `Action` via exhaustion-aware `ToLegacyAction()` mapping
- Switch statements in `proxy_forward.go`, `proxy_handler.go`, `proxy_responses_websocket.go`, `admin_testing.go` require zero changes

## Why Deprecate Instead of Delete?

1. **Test Value**: 32 existing `HandleError*` tests document historical classification behavior and edge cases. They remain valuable regression guards.

2. **Gradual Migration**: `proxyResult.nextAction` is consumed by ~18 switch sites across 4 files. Keeping `Action` as a valid return type via `ToLegacyAction()` allows incremental migration without a big-bang rewrite.

3. **External Risk**: Unknown if any internal tools or scripts directly import and call `HandleError`. Deprecation signals the direction without breaking existing callers.

## Future Cleanup (Optional)

When ready for breaking changes:

1. **Type `proxyResult.nextAction` as `Decision`** instead of `Action`
   - Update ~18 switch sites to inspect `decision.Retry` instead of switching on `Action`
   - Benefit: consumers can independently reason about retry vs effect
   - Files: `proxy_forward.go`, `proxy_handler.go`, `proxy_responses_websocket.go`, `admin_testing.go:executeChannelTestWithCooldown`

2. **Remove `HandleError*` and `Action` entirely**
   - Convert 32 legacy tests to use Decision API
   - Remove `handleErrorDecision` / `handleKeyFallback` internal methods
   - Remove `ToLegacyAction()` / `ActionToDecision()` bridge helpers

3. **Migrate `handleProxySuccess`**
   - Design `EffectClearCooldownsGranular` or keep specialized clear logic
   - Currently deferred due to guard/counter mismatch with `ResetAllCooldowns`

## Verification

✅ Full codebase compiles: `go build -tags sonic -buildvcs=false ./...`
✅ All cooldown tests pass: `ok github.com/yzgolden86/PivotFlow/internal/cooldown 8.268s`
✅ All app tests pass: `ok github.com/yzgolden86/PivotFlow/internal/app 61.886s`
✅ No behavior changes: all existing switch statements and tests work identically
✅ CLAUDE.md updated to document Decision API as the primary interface

## Summary

The retry/effect decoupling project is complete. All production cooldown paths now use the Decision API internally, providing clean separation between retry strategy and credential punishment. The legacy `Action`-based interface remains available for backward compatibility but is clearly marked as deprecated. Future code should use `Decide*()` + `ApplyEffect*()` for all new cooldown logic.
