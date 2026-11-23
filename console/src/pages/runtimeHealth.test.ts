import assert from 'node:assert/strict'
import test from 'node:test'
import type { RuntimeComponentId, RuntimeMetrics, RuntimeOperationStats, RuntimeQueueStats } from '../types.ts'
import {
  formatRuntimeTime,
  parseRuntimeMetrics,
  runtimeComponent,
  runtimeDiagnostics,
  runtimeDisplaySnapshot,
  runtimeDisplayState,
  runtimeFreshness,
  runtimeState,
} from './runtimeHealth.ts'

const now = 1_800_000_000_000

function operation(): RuntimeOperationStats {
  return {
    active: 0, attempts: 0, succeeded: 0, failed: 0, cancelled: 0, failing: false,
    consecutive_failures: 0, last_result: '', last_attempt_at: 0, last_finished_at: 0,
    last_success_at: 0, last_failure_at: 0, recovered_at: 0, last_error_code: '',
  }
}

function queue(): RuntimeQueueStats {
  return { enabled: true, queued: 0, capacity: 256, buffered: 0, dropped: 0, write_failed: 0, written: 0, last_dropped_at: 0, operation: operation() }
}

function metrics(): RuntimeMetrics {
  const ids: RuntimeComponentId[] = ['billing', 'request_history', 'logs', 'token_stats', 'log_replication', 'trace_replication', 'cache_sync', 'log_cleanup', 'debug_cleanup', 'startup_recovery', 'websocket']
  return {
    started_at: now - 60_000, observed_at: now,
    health: { version: 1, state: 'idle', components: ids.map((id) => ({ id, state: 'idle', reason: 'awaiting_work' })) },
    billing: {
      enabled: true, pending: 0, unpersisted: 0, blocked: false, closing: false,
      journal_failures: 0, store_failures: 0, invalid_cost_events: 0, settled: 0,
      deleted_token_events: 0, last_success_at: 0, oldest_pending_at: 0,
      journal: operation(), settlement: operation(),
    },
    request_traces: queue(), logs: queue(), token_failure_stats: queue(),
    cleanup: {
      logs: { enabled: true, mode: 'retention', retention_days: 3, retention_minutes: 0, interval_ms: 3_600_000, next_run_at: now + 3_600_000, deleted: 0, operation: operation() },
      debug_logs: { enabled: false, mode: 'disabled', retention_days: 0, retention_minutes: 0, interval_ms: 120_000, next_run_at: 0, deleted: 0, operation: operation() },
    },
    storage: {
      mode: 'sqlite', primary: 'sqlite', cache_may_be_stale: false, cache_sync: operation(), startup_recovery: null,
      log_replication: { enabled: false, queued: 0, capacity: 0, dropped: 0, failed: 0, last_dropped_at: 0, operation: operation() },
    },
    responses_websocket: {
      sessions: 0, active_attachments: 0, downstream_connections: 0, rejected_downstream_connections: 0,
      max_downstream_connections: 64, max_downstream_connections_per_token: 8,
      upstream_connections: 0, upstream_handshakes: 0, upstream_reuses: 0, upstream_heartbeat_failures: 0,
      upstream_queued_read_bytes: 0, oldest_upstream_connection_seconds: 0, reconnects: 0,
      transcript_bytes: 0, max_transcript_bytes: 134_217_728, max_sessions: 32,
      ttl_expired: 0, capacity_rejected: 0, budget_rejected: 0, previous_response_misses: 0,
    },
  }
}

function rawRequestCacheObservation() {
  return {
    version: 1, observed_at: now, process_started_at: now - 60_000,
    config: { enabled: false, ttl_seconds: 60, max_entries: 256, max_bytes: 33_554_432, max_entry_bytes: 1_048_576 },
    stats: {
      entries: 0, bytes: 0, expired_entries: 0, captures: 0, capture_bytes: 0,
      lookups: 0, hits: 0, misses: 0, refreshes: 0, bypasses: 2, stored: 0,
      expired: 0, evicted: 0, rejected: 0, clears: 0,
      invalidations: {}, reasons: { disabled: 2 }, last_invalidation_reason: '', last_invalidation_at: 0,
    },
  }
}

test('valid runtime observations preserve no-traffic and disabled facts', () => {
  const result = parseRuntimeMetrics(metrics())
  assert.equal(result.health.state, 'idle')
  assert.equal(result.billing.last_success_at, 0)
  assert.equal(result.cleanup.debug_logs.enabled, false)
  assert.equal(result.storage.startup_recovery, null)
  assert.equal(result.cleanup.logs.next_run_at, now + 3_600_000)
})

test('runtime diagnostics retain a validated optional request cache observation', () => {
  const result = parseRuntimeMetrics({ ...metrics(), request_cache: rawRequestCacheObservation() })
  assert.equal(result.request_cache?.stats.reasons.disabled, 2)
  assert.equal(result.request_cache?.stats.reasons.reused, 0)
  const diagnostic = runtimeDiagnostics(result, 'current', now)
  assert.equal(diagnostic.request_cache?.config.enabled, false)
  assert.equal(parseRuntimeMetrics(metrics()).request_cache, undefined)
  assert.equal(parseRuntimeMetrics({ ...metrics(), request_cache: null }).request_cache, null)
})

test('invalid request cache observations cannot enter runtime diagnostics', () => {
  const unknown = { ...rawRequestCacheObservation(), raw_response: 'private-response-body' }
  assert.throws(() => parseRuntimeMetrics({ ...metrics(), request_cache: unknown }), /请求缓存/)
  const malformed = rawRequestCacheObservation()
  malformed.stats.hits = Number.MAX_SAFE_INTEGER + 1
  assert.throws(() => parseRuntimeMetrics({ ...metrics(), request_cache: malformed }), /请求缓存/)
})

test('legacy metrics and incompatible health versions never become empty healthy data', () => {
  assert.throws(() => parseRuntimeMetrics({ billing: { pending: 0 }, request_traces: { queued: 0 } }), /运行健康|后端/)
  const future = metrics()
  future.health.version = 2
  assert.throws(() => parseRuntimeMetrics(future), /版本/)
})

test('missing and invalid observations are not replaced with zero', () => {
  const value = metrics()
  const partial = { ...value, billing: { ...value.billing, pending: 'not-a-count' } }
  assert.throws(() => parseRuntimeMetrics(partial), /不完整/)
  assert.throws(() => parseRuntimeMetrics({ ...value, logs: undefined }), /不完整/)
  assert.throws(() => parseRuntimeMetrics({ ...value, observed_at: 0 }), /不完整/)
})

test('out-of-range timestamps cannot crash the page or enter diagnostics', () => {
  const value = metrics()
  assert.throws(() => parseRuntimeMetrics({ ...value, observed_at: Number.MAX_SAFE_INTEGER }), /不完整/)
  value.logs.operation.last_success_at = Number.MAX_SAFE_INTEGER
  assert.throws(() => parseRuntimeMetrics(value), /不完整/)
  assert.equal(formatRuntimeTime(Number.MAX_SAFE_INTEGER), '时间不可用')
  assert.equal(formatRuntimeTime(Number.NaN), '时间不可用')
  assert.equal(formatRuntimeTime(0), '尚未观测到')
})

test('unknown component states and reasons make the overall presentation unknown', () => {
  const value = metrics()
  const components = value.health.components.map((item) => item.id === 'billing'
    ? { ...item, state: 'future-state', reason: 'future-reason' }
    : item)
  const result = parseRuntimeMetrics({ ...value, health: { ...value.health, state: 'healthy', components } })
  assert.equal(runtimeState('future-state'), 'unknown')
  assert.equal(runtimeComponent(result, 'billing').state, 'unknown')
  assert.equal(result.health.state, 'unknown')
})

test('missing or duplicated component observations cannot conceal a fault', () => {
  const value = metrics()
  assert.throws(() => parseRuntimeMetrics({ ...value, health: { ...value.health, components: value.health.components.slice(1) } }), /不完整/)
  assert.throws(() => parseRuntimeMetrics({ ...value, health: { ...value.health, components: [...value.health.components, value.health.components[0]] } }), /不完整/)
})

test('refresh failures and expired snapshots cannot report current health', () => {
  const value = metrics()
  value.health.state = 'healthy'
  assert.equal(runtimeFreshness(0, false, false, now), 'unavailable')
  assert.equal(runtimeFreshness(now - 1_000, false, false, now), 'current')
  assert.equal(runtimeFreshness(now - 1_000, false, true, now), 'paused')
  assert.equal(runtimeFreshness(now - 1_000, true, false, now), 'stale')
  assert.equal(runtimeFreshness(now - 30_001, false, false, now), 'stale')
  assert.equal(runtimeDisplayState(value, 'stale'), 'unknown')
  assert.equal(runtimeDisplayState(value, 'current'), 'healthy')
  value.health.components[0].state = 'healthy'
  const stale = runtimeDisplaySnapshot(value, 'stale')!
  assert.equal(stale.health.state, 'unknown')
  assert.ok(stale.health.components.every((item) => item.state === 'unknown'))
  assert.equal(value.health.components[0].state, 'healthy')
  assert.equal(runtimeDisplaySnapshot(value, 'current'), value)
})

test('diagnostics project only allowed observations and annotate old snapshots', () => {
  const value = metrics()
  value.billing.pending = 3
  const poisoned = {
    ...value, credential: 'private-credential', database_path: 'private-path',
    billing: { ...value.billing, token_hash: 'private-token', events: ['private-event'] },
    storage: { ...value.storage, primary: 'private-dsn', cache_sync: { ...value.storage.cache_sync, last_error_code: 'private-error-text' } },
  } as unknown as RuntimeMetrics
  const result = runtimeDiagnostics(poisoned, 'stale', now - 40_000)
  const text = JSON.stringify(result)
  assert.ok(!text.includes('private-'))
  assert.equal(result.snapshot_status, 'stale')
  assert.equal(result.received_at, now - 40_000)
  assert.equal(result.billing.pending, 3)
  assert.equal(result.storage.primary, 'unknown')
  assert.equal(result.storage.cache_sync.last_error_code, 'operation_failed')
})

test('diagnostics retain cumulative losses after successful recovery', () => {
  const value = metrics()
  value.request_traces.write_failed = 2
  value.request_traces.dropped = 4
  value.request_traces.operation.failed = 3
  value.request_traces.operation.succeeded = 1
  value.request_traces.operation.recovered_at = now - 1_000
  const result = runtimeDiagnostics(value, 'current', now)
  assert.equal(result.request_traces.write_failed, 2)
  assert.equal(result.request_traces.dropped, 4)
  assert.equal(result.request_traces.operation.failed, 3)
  assert.equal(result.request_traces.operation.recovered_at, now - 1_000)
})
