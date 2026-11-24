import assert from 'node:assert/strict'
import test from 'node:test'
import type { RuntimeComponentId, RuntimeMetrics, RuntimeOperationStats, RuntimeQueueStats } from '../types.ts'
import { groupRuntimeComponents, runtimeAttentionItems, runtimeOverviewState } from './runtimeHealthPresentation.ts'

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
    health: { version: 1, state: 'healthy', components: ids.map((id) => ({ id, state: 'healthy', reason: 'ready' })) },
    billing: {
      enabled: true, pending: 0, unpersisted: 0, blocked: false, closing: false,
      journal_failures: 0, store_failures: 0, invalid_cost_events: 0, settled: 4,
      deleted_token_events: 0, last_success_at: now - 1_000, oldest_pending_at: 0,
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

test('component grouping keeps core records visible and folds optional or maintenance details', () => {
  const value = metrics()
  value.health.components = value.health.components.map((item) => (
    item.id === 'debug_cleanup' || item.id === 'log_replication'
      ? { ...item, state: 'disabled', reason: 'not_enabled' }
      : item
  ))

  const groups = groupRuntimeComponents(value)
  assert.deepEqual(groups.primary, ['billing', 'request_history'])
  assert.deepEqual(groups.maintenance, ['log_cleanup', 'startup_recovery'])
  assert.deepEqual(groups.optional, ['log_replication', 'debug_cleanup'])
  assert.deepEqual(groups.routine, ['logs', 'token_stats', 'trace_replication', 'cache_sync', 'websocket'])
})

test('current healthy state without retained losses needs no attention block', () => {
  assert.deepEqual(runtimeAttentionItems(metrics(), 'current'), [])
})

test('retained losses remain actionable when every component reports healthy', () => {
  const value = metrics()
  value.billing.unpersisted = 2
  value.billing.invalid_cost_events = 1
  value.request_traces.write_failed = 3
  value.logs.dropped = 4
  value.storage.log_replication.failed = 2
  value.request_traces.replication = { enabled: true, queued: 0, capacity: 64, dropped: 1, failed: 0, last_dropped_at: now, operation: operation() }
  value.storage.cache_may_be_stale = true

  assert.deepEqual(
    runtimeAttentionItems(value, 'current').map((item) => ({
      component: item.componentId,
      kinds: item.kinds,
      severity: item.severity,
    })),
    [
      { component: 'billing', kinds: ['unpersisted', 'invalid_cost'], severity: 'critical' },
      { component: 'request_history', kinds: ['write_loss'], severity: 'warning' },
      { component: 'logs', kinds: ['write_loss'], severity: 'warning' },
      { component: 'log_replication', kinds: ['replication_loss'], severity: 'warning' },
      { component: 'trace_replication', kinds: ['replication_loss'], severity: 'warning' },
      { component: 'cache_sync', kinds: ['replica_difference'], severity: 'warning' },
    ],
  )
})

test('abnormal component states are surfaced once per component', () => {
  const value = metrics()
  value.health.components = value.health.components.map((item) => (
    item.id === 'logs' ? { ...item, state: 'warning', reason: 'write_failed' } : item
  ))
  value.logs.write_failed = 1

  assert.deepEqual(
    runtimeAttentionItems(value, 'current').map((item) => ({
      component: item.componentId,
      kinds: item.kinds,
      severity: item.severity,
    })),
    [{ component: 'logs', kinds: ['component_warning', 'write_loss'], severity: 'warning' }],
  )
})

test('recovered settlement attempts remain history instead of becoming a current issue', () => {
  const value = metrics()
  value.billing.journal_failures = 3
  value.billing.store_failures = 2

  assert.deepEqual(runtimeAttentionItems(value, 'current'), [])
})

test('an unknown current component remains visible without a stale snapshot', () => {
  const value = metrics()
  value.health.components = value.health.components.map((item) => (
    item.id === 'token_stats' ? { ...item, state: 'unknown', reason: 'unknown' } : item
  ))

  assert.deepEqual(
    runtimeAttentionItems(value, 'current').map((item) => ({
      component: item.componentId,
      kinds: item.kinds,
      severity: item.severity,
    })),
    [{ component: 'token_stats', kinds: ['component_unknown'], severity: 'unknown' }],
  )
})

test('a stale snapshot is ordered first and avoids repeating every component as unknown', () => {
  const value = metrics()
  value.health.components = value.health.components.map((item) => ({ ...item, state: 'unknown', reason: 'unknown' }))
  value.billing.unpersisted = 1

  const attention = runtimeAttentionItems(value, 'stale')
  assert.equal(attention[0].id, 'snapshot:stale')
  assert.equal(attention[0].severity, 'unknown')
  assert.ok(attention.every((item) => !item.kinds.includes('component_unknown')))
  assert.deepEqual(attention[1].kinds, ['unpersisted'])
})

test('retained write loss keeps both overviews in warning after processing recovers', () => {
  const value = metrics()
  value.logs.write_failed = 5
  value.logs.dropped = 4
  assert.equal(runtimeOverviewState(runtimeAttentionItems(value, 'current'), 'healthy'), 'warning')
})

test('an expired snapshot keeps the overview unknown while preserving its last unpersisted counts', () => {
  const value = metrics()
  value.billing.unpersisted = 2
  const attention = runtimeAttentionItems(value, 'stale')
  assert.equal(runtimeOverviewState(attention, 'unknown'), 'unknown')
  assert.ok(attention.some((item) => item.kinds.includes('unpersisted')))
})

test('a currently blocked admission remains distinct from other critical errors', () => {
  const value = metrics()
  value.health.components[0] = { id: 'billing', state: 'blocked', reason: 'billing_unpersisted' }
  value.billing.unpersisted = 2
  assert.equal(runtimeOverviewState(runtimeAttentionItems(value, 'current'), 'blocked'), 'blocked')
})
