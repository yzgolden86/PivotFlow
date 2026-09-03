# Retry/Effect Decoupling Design

> **Status**: P2 roadmap task — design document for decoupling retry logic from credential punishment effects
> 
> **Context**: Inspired by gpt-load v2 `health.Decision{Retry, Effect}` pattern; addresses PivotFlow's coupled classification in `util/classifier.go` + `cooldown.Action`

## Problem Statement

Current `cooldown.Action` enum conflates two orthogonal concerns:

```go
// internal/cooldown/manager.go:17-25
type Action int
const (
    ActionRetryKey     Action = iota // 重试当前渠道的其他Key
    ActionRetryModel                 // 当前模型在该渠道不可用，切换渠道
    ActionRetryChannel               // 切换到下一个渠道
    ActionReturnClient               // 直接返回给客户端
)
```

**Issues**:
1. **Mixed semantics**: `ActionRetryChannel` means both "switch channel" AND "cool the current channel"
2. **Special case proliferation**: Protocol capability negotiation, client errors, planned connection rotation all bypass normal cooldown via ad-hoc branches
3. **Hard to extend**: Adding new retry strategies (e.g., "retry same URL after delay") or new effects (e.g., "record failure without cooldown") requires new Action variants

**gpt-load v2 approach** (from memory `gpt-load-borrowable-features.md`):
```go
// Conceptual — actual structure in github.com/tbphp/gpt-load/internal/health
type Decision struct {
    Retry  RetryAction  // none / refresh_credential / next_candidate
    Effect EffectAction // none / cooldown_credential / record_credential_failure / skip_group
}
```

Benefits: Single attempt produces one effect; validation enforces orthogonality.

## Current Architecture

### Entry Points
1. **Proxy chain**: `proxy_handler.go:HandleProxyRequest` → `runProxyAttemptLoop` → `proxy_forward.go:forwardAttempt` → `proxy_error.go:handle*Error`
2. **Channel test**: `admin_testing.go:executeChannelTestWithCooldown`

### Decision Flow
```
Error occurs
  ↓
util/classifier.go:ClassifyHTTPResponseWithMeta / ClassifyError
  → HTTPResponseClassification (Level + cooldown metadata)
  ↓
cooldown/manager.go:classifyDecision
  → cooldownDecision (action + cooldown timestamps)
  ↓
cooldown/manager.go:HandleError / HandleErrorWithKeyFallback
  → Applies cooldowns + returns Action
  ↓
proxy_forward.go: switch on Action
  → RetryKey: try next key, same channel
  → RetryModel: skip channel (model unavailable)
  → RetryChannel: skip channel
  → ReturnClient: stop retry loop
```

### Special Cases (bypass normal flow)
- **Protocol capability missing** (`protocolCapabilityMissing=true`): Set at `proxy_forward.go:2047-2050`, checked at `proxy_handler.go:510`
- **Client errors** (40x): `classifier.go` returns `ErrorLevelClient` → `ActionReturnClient`, no cooldown applied
- **Planned connection rotation**: Upstream connection age limit reached → close connection without cooldown
- **RPM/concurrency limits**: Return special result markers (`rpm_limited`, `concurrency_limited`) to skip cooldown logic

## Proposed Design

### Core Types

```go
// internal/cooldown/decision.go (new file)
package cooldown

// RetryStrategy determines what to attempt next.
type RetryStrategy int

const (
	RetryNone          RetryStrategy = iota // Stop retry loop, return to client
	RetryNextKey                             // Try next key, same channel
	RetryNextURL                             // Try next URL, same channel/key
	RetryNextChannel                         // Switch to next channel
	RetryRefreshToken                        // Refresh OAuth token, retry same endpoint
)

// Effect determines how to punish/reward the failed/succeeded attempt.
type Effect int

const (
	EffectNone                Effect = iota // No side effect (e.g., client error, capability negotiation)
	EffectCoolKey                           // Cool current key
	EffectCoolModel                         // Cool model on current channel
	EffectCoolChannel                       // Cool entire channel
	EffectClearCooldowns                    // Clear key/model/channel cooldowns (success)
	EffectRecordFailure                     // Record failure without cooldown (e.g., transient network glitch)
)

// Decision separates retry logic from credential punishment.
type Decision struct {
	Retry RetryStrategy
	Effect Effect
	
	// Cooldown metadata (only when Effect requires it)
	KeyCooldownUntil        time.Time
	HasKeyCooldownUntil     bool
	ModelCooldownUntil      time.Time
	HasModelCooldownUntil   bool
	ChannelCooldownUntil    time.Time
	HasChannelCooldownUntil bool
	CooldownReason          string
	
	// Model scope (for model-level effects)
	Model string
	PreventKeyFallback bool
}

// Validate ensures the decision is internally consistent.
func (d Decision) Validate() error {
	// Enforce: one attempt produces at most one cooldown effect
	cooldownEffects := 0
	if d.Effect == EffectCoolKey { cooldownEffects++ }
	if d.Effect == EffectCoolModel { cooldownEffects++ }
	if d.Effect == EffectCoolChannel { cooldownEffects++ }
	if cooldownEffects > 1 {
		return errors.New("decision must produce at most one cooldown effect")
	}
	
	// RetryNone must pair with terminal effects or EffectNone
	if d.Retry == RetryNone && (d.Effect == EffectCoolKey || d.Effect == EffectCoolModel || d.Effect == EffectCoolChannel) {
		return errors.New("RetryNone should not cool resources that won't be retried")
	}
	
	return nil
}
```

### Migration Strategy

**Phase 1: Parallel implementation (backward compatible)**
1. Add `decision.go` with new types
2. Create `Manager.Decide(ctx, ErrorInput) (Decision, error)` alongside existing `HandleError`
3. Implement `Decision.ToLegacyAction() Action` bridge
4. Add validation tests

**Phase 2: Migrate call sites**
1. Update `proxy_error.go` to call `Decide()` → apply effect → map retry strategy
2. Update `admin_testing.go` to use `Decision`
3. Update `proxy_forward.go` retry loop to switch on `RetryStrategy`

**Phase 3: Remove legacy**
1. Delete `Action` enum and `HandleError` once all call sites migrated
2. Rename `Decide` → `HandleError` if desired

### Example Mappings

| Current Action | New Decision | Notes |
|----------------|--------------|-------|
| `ActionRetryKey` | `{RetryNextKey, EffectCoolKey}` | Cool key, try next |
| `ActionRetryModel` | `{RetryNextChannel, EffectCoolModel}` | Cool model, skip channel |
| `ActionRetryChannel` | `{RetryNextChannel, EffectCoolChannel}` | Cool channel, skip |
| `ActionReturnClient` (client error) | `{RetryNone, EffectNone}` | Stop, no punishment |
| Protocol capability missing | `{RetryNextChannel, EffectNone}` | Skip channel without cooldown |
| Connection age rotation | `{RetryNextChannel, EffectNone}` | Close connection, no punishment |
| Success | `{RetryNone, EffectClearCooldowns}` | Stop, clear cooldowns |

### Benefits

1. **Explicit special cases**: Protocol negotiation/rotation become `EffectNone`, no ad-hoc branches
2. **Extensibility**: Add `RetryNextURL` without new Effect; add `EffectRecordFailure` without new Retry
3. **Validation**: `Decision.Validate()` enforces invariants at construction
4. **Testability**: Test retry logic independently from cooldown logic

## Open Questions

1. **Effect timing**: Should `EffectCoolModel` block key fallback immediately, or let caller decide?
   - **Proposal**: Keep `PreventKeyFallback` field in Decision; caller honors it
   
2. **Multi-effect scenarios**: Should success clear *all* cooldowns or only relevant ones?
   - **Current**: `handleProxySuccess` clears key+channel+model unconditionally
   - **Proposal**: Keep `EffectClearCooldowns` as single effect, implement clearing in `Manager.ApplyEffect`

3. **Backward compatibility**: Should we support `Action` → `Decision` conversion during migration?
   - **Proposal**: Yes, implement `ActionToDecision(Action) Decision` for gradual migration

## Implementation Checklist

- [ ] Create `internal/cooldown/decision.go` with new types
- [ ] Implement `Manager.Decide(ctx, ErrorInput) (Decision, error)`
- [ ] Implement `Decision.Validate()` with comprehensive tests
- [ ] Implement `Decision.ToLegacyAction()` bridge
- [ ] Add unit tests for all current Action scenarios
- [ ] Migrate `proxy_error.go:handleNetworkError`
- [ ] Migrate `proxy_error.go:handleProxyErrorResponse`
- [ ] Migrate `proxy_error.go:handleProxySuccess`
- [ ] Migrate `admin_testing.go:executeChannelTestWithCooldown`
- [ ] Update `proxy_forward.go` retry loop to handle `RetryStrategy`
- [ ] Remove legacy `Action` enum and `HandleError`
- [ ] Update CLAUDE.md with new architecture

## References

- Memory: `gpt-load-borrowable-features.md` (gpt-load v2 comparison)
- Code: `internal/cooldown/manager.go` (current Decision impl)
- Code: `internal/util/classifier.go` (HTTP response classification)
- Code: `internal/app/proxy_error.go` (error handling entry points)
