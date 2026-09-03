# P2 Task 1: Retry/Effect Decoupling - Implementation Summary

**Date**: 2026-09-03
**Status**: ✅ Phase 1 Complete (Parallel Implementation)

## What Was Done

### 1. Design Document
Created `docs/retry-effect-decoupling.md` documenting:
- Problem statement: Current `cooldown.Action` conflates retry logic with credential punishment
- Proposed `Decision{Retry, Effect}` structure inspired by gpt-load v2
- Migration strategy (3 phases)
- Example mappings from legacy Action to new Decision
- Implementation checklist

### 2. Core Implementation
Created `internal/cooldown/decision.go` with:

**New Types**:
```go
type RetryStrategy int  // RetryNone, RetryNextKey, RetryNextURL, RetryNextChannel, RetryRefreshToken
type Effect int         // EffectNone, EffectCoolKey, EffectCoolModel, EffectCoolChannel, EffectClearCooldowns, EffectRecordFailure

type Decision struct {
    Retry  RetryStrategy
    Effect Effect
    // Cooldown metadata fields
    // Model scope fields
}
```

**Key Methods**:
- `Decision.Validate()`: Enforces invariants (one cooldown effect per attempt, RetryNone doesn't cool resources)
- `Decision.ToLegacyAction()`: Bridge for gradual migration to legacy `Action` enum
- `ActionToDecision()`: Converts legacy `Action` to `Decision` (lossy conversion)
- `String()` methods for debugging

### 3. Comprehensive Test Coverage
Created `internal/cooldown/decision_test.go` with:
- 13 validation test cases (valid + invalid scenarios)
- 12 ToLegacyAction conversion test cases
- 4 ActionToDecision conversion test cases
- Round-trip conversion tests
- String representation tests

**All tests pass** ✅:
```
=== RUN   TestDecisionValidate
--- PASS: TestDecisionValidate (0.00s)
=== RUN   TestDecisionToLegacyAction
--- PASS: TestDecisionToLegacyAction (0.00s)
=== RUN   TestDecisionRoundTrip
--- PASS: TestDecisionRoundTrip (0.00s)
PASS
ok  	github.com/yzgolden86/PivotFlow/internal/cooldown	7.154s
```

Full cooldown package tests pass with no regressions.

## Benefits Achieved

1. **Explicit special cases**: Protocol negotiation, connection rotation → `{RetryNextChannel, EffectNone}` (no ad-hoc branches)
2. **Extensibility**: Add `RetryNextURL` without new Effect; add `EffectRecordFailure` without new Retry
3. **Validation**: `Decision.Validate()` enforces invariants at construction time
4. **Testability**: Retry logic testable independently from cooldown logic
5. **Backward compatibility**: Legacy `Action` still works via bridge functions

## Next Steps (Phase 2: Migration)

Remaining work from implementation checklist:

- [ ] Implement `Manager.Decide(ctx, ErrorInput) (Decision, error)` alongside existing `HandleError`
- [ ] Migrate `proxy_error.go:handleNetworkError`
- [ ] Migrate `proxy_error.go:handleProxyErrorResponse`
- [ ] Migrate `proxy_error.go:handleProxySuccess`
- [ ] Migrate `admin_testing.go:executeChannelTestWithCooldown`
- [ ] Update `proxy_forward.go` retry loop to handle `RetryStrategy`

**Phase 3** (after Phase 2 complete):
- [ ] Remove legacy `Action` enum and `HandleError`
- [ ] Update CLAUDE.md with new architecture

## Design Rationale

### Why HasXxxCooldownUntil for Multiple Cooldown Detection?

Initial implementation checked `Effect` enum:
```go
if d.Effect == EffectCoolKey { cooldownEffects++ }
```

**Problem**: Test case "multiple cooldown effects" set `Effect=EffectCoolKey` but also set `HasChannelCooldownUntil=true`. The validation should fail (multiple cooldown metadata present), but checking only `Effect` missed it.

**Fix**: Check metadata flags instead:
```go
if d.HasKeyCooldownUntil { cooldownEffects++ }
if d.HasModelCooldownUntil { cooldownEffects++ }
if d.HasChannelCooldownUntil { cooldownEffects++ }
```

This enforces the real invariant: "one attempt produces metadata for at most one cooldown scope."

## Files Changed

- `docs/retry-effect-decoupling.md` (new)
- `internal/cooldown/decision.go` (new)
- `internal/cooldown/decision_test.go` (new)

## Verification

✅ All Decision tests pass
✅ All cooldown package tests pass (no regressions)
✅ Full codebase compiles (`go build -tags sonic -buildvcs=false ./...`)

## References

- Memory: `gpt-load-borrowable-features.md` (gpt-load v2 comparison)
- Design doc: `docs/retry-effect-decoupling.md`
- Code: `internal/cooldown/manager.go` (existing Action-based implementation)
