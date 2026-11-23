import assert from 'node:assert/strict'
import test from 'node:test'
import type { RequestCacheObservation } from '../types.ts'
import {
  cacheInvalidationReasonLabel,
  cacheReasonLabel,
  cacheStatusLabel,
  createRequestCacheCoordinator,
  parseRequestCacheObservation,
} from './requestCache.ts'

const invalidationReasons = {
  manual_clear: 1,
  routing_changed: 2,
  keys_changed: 3,
  aliases_changed: 4,
  permissions_changed: 5,
}

const cacheReasons = {
  disabled: 1,
  not_opted_in: 2,
  client_bypass: 3,
  invalid_mode: 4,
  request_no_store: 5,
  request_no_cache: 6,
  request_query: 7,
  request_encoding: 8,
  streaming: 9,
  unsupported_endpoint: 10,
  nondeterministic: 11,
  tools_or_state: 12,
  unsupported_request: 13,
  no_identity: 14,
  route_unavailable: 15,
  channel_changed: 16,
  custom_rules: 17,
  protocol_transform: 18,
  empty: 19,
  expired: 20,
  client_refresh: 21,
  unsafe_response: 22,
  response_no_store: 23,
  response_too_large: 24,
  capture_budget: 25,
  invalidated: 26,
  upstream_error: 27,
  canceled: 28,
  write_failed: 29,
  reused: 30,
  stored: 31,
}

function observation(): RequestCacheObservation {
  return {
    version: 1,
    observed_at: 2_000,
    process_started_at: 1_000,
    config: {
      enabled: false,
      ttl_seconds: 60,
      max_entries: 256,
      max_bytes: 33_554_432,
      max_entry_bytes: 1_048_576,
    },
    stats: {
      entries: 2,
      bytes: 512,
      expired_entries: 1,
      captures: 8,
      capture_bytes: 2_048,
      lookups: 9,
      hits: 3,
      misses: 2,
      refreshes: 1,
      bypasses: 4,
      stored: 2,
      expired: 1,
      evicted: 0,
      rejected: 1,
      clears: 2,
      invalidations: { ...invalidationReasons },
      reasons: { ...cacheReasons },
      last_invalidation_reason: 'manual_clear',
      last_invalidation_at: 1_900,
    },
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((nextResolve, nextReject) => {
    resolve = nextResolve
    reject = nextReject
  })
  return { promise, resolve, reject }
}

const tick = () => new Promise<void>((resolve) => setImmediate(resolve))

test('parses the complete version-one request cache observation', () => {
  const parsed = parseRequestCacheObservation(observation())

  assert.deepEqual(parsed, observation())
  assert.equal(parsed.stats.entries, 2)
  assert.equal(parsed.stats.reasons.stored, 31)
  assert.equal(parsed.stats.invalidations.permissions_changed, 5)
})

test('accepts the sparse reason maps emitted by a fresh or lightly used backend', () => {
  const sparse = {
    ...observation(),
    stats: { ...observation().stats, reasons: { disabled: 2 }, invalidations: {} },
  }
  const parsed = parseRequestCacheObservation(sparse)
  assert.equal(parsed.stats.reasons.disabled, 2)
  assert.equal(parsed.stats.reasons.reused, 0)
  assert.equal(parsed.stats.invalidations.manual_clear, 0)
  assert.throws(() => parseRequestCacheObservation({
    ...sparse, stats: { ...sparse.stats, reasons: { disabled: null } },
  }), /计数/)
})

test('rejects timestamps that cannot be rendered as dates', () => {
  const oversized = Number.MAX_SAFE_INTEGER
  assert.throws(() => parseRequestCacheObservation({ ...observation(), observed_at: oversized }), /时间/)
  assert.throws(() => parseRequestCacheObservation({
    ...observation(), stats: { ...observation().stats, last_invalidation_at: oversized },
  }), /时间/)
})

test('rejects impossible resident and in-flight capacity observations', () => {
  for (const patch of [
    { entries: 257 }, { bytes: 33_554_433 }, { capture_bytes: 33_554_433 }, { expired_entries: 3 },
  ]) {
    assert.throws(() => parseRequestCacheObservation({
      ...observation(), stats: { ...observation().stats, ...patch },
    }), /容量|驻留|过期/)
  }
})

test('requires invalidation reason and time to describe the observed process', () => {
  for (const patch of [
    { last_invalidation_at: 0 }, { last_invalidation_reason: '' },
    { last_invalidation_at: 999 }, { last_invalidation_at: 2_001 },
  ]) {
    assert.throws(() => parseRequestCacheObservation({
      ...observation(), stats: { ...observation().stats, ...patch },
    }), /失效.*时间|时间.*范围|原因/)
  }
})

test('rejects future versions and malformed capacity bounds before display', () => {
  const future = { ...observation(), version: 2 }
  const maxEntry = observation()
  maxEntry.config.max_entry_bytes = 2_097_152
  const extraTopLevel = { ...observation(), raw_reason: 'must-not-leak' }

  assert.throws(() => parseRequestCacheObservation(future), /版本/)
  assert.throws(() => parseRequestCacheObservation(maxEntry), /单条.*容量/)
  assert.throws(() => parseRequestCacheObservation(extraTopLevel), /字段/)
})

test('rejects unsafe counters and unknown fixed reason codes', () => {
  const unsafe = observation()
  unsafe.stats.hits = Number.MAX_SAFE_INTEGER + 1
  const unknownReason = observation()
  const reasons = unknownReason.stats.reasons as Record<string, number>
  reasons.upstream_secret = 1

  assert.throws(() => parseRequestCacheObservation(unsafe), /计数/)
  assert.throws(() => parseRequestCacheObservation(unknownReason), /原因/)
})

test('maps fixed cache codes to Chinese without exposing unknown raw values', () => {
  assert.equal(cacheStatusLabel('hit'), '缓存命中')
  assert.equal(cacheStatusLabel('bypass'), '已绕过缓存')
  assert.equal(cacheReasonLabel('request_no_cache'), '请求要求刷新缓存')
  assert.equal(cacheInvalidationReasonLabel('permissions_changed'), '权限发生变化')
  assert.equal(cacheStatusLabel('future_status'), '未知缓存状态')
  assert.equal(cacheReasonLabel('raw_server_code'), '未知缓存原因')
  assert.doesNotMatch(cacheReasonLabel('raw_server_code'), /raw_server_code/)
})

test('deduplicates an in-flight read and keeps a refresh error from replacing the known snapshot', async () => {
  const first = deferred<RequestCacheObservation>()
  const second = deferred<RequestCacheObservation>()
  let reads = 0
  const coordinator = createRequestCacheCoordinator({
    read: () => {
      reads += 1
      return reads === 1 ? first.promise : second.promise
    },
    clear: async () => observation(),
    confirmClear: () => true,
  })

  coordinator.start()
  coordinator.start()
  await tick()
  assert.equal(reads, 1)
  first.resolve(observation())
  await tick()
  assert.equal(coordinator.getState().observation?.stats.entries, 2)

  coordinator.refresh()
  coordinator.refresh()
  await tick()
  assert.equal(reads, 2)
  second.reject(new Error('network down'))
  await tick()

  assert.equal(coordinator.getState().observation?.stats.entries, 2)
  assert.equal(coordinator.getState().snapshotStale, true)
  assert.match(coordinator.getState().error, /刷新失败/)
})

test('ignores the old read after clear succeeds', async () => {
  const staleRead = deferred<RequestCacheObservation>()
  const cleared = observation()
  cleared.stats.entries = 0
  cleared.stats.bytes = 0
  const coordinator = createRequestCacheCoordinator({
    read: () => staleRead.promise,
    clear: async () => cleared,
    confirmClear: () => true,
  })

  coordinator.start()
  coordinator.clear()
  await tick()
  assert.equal(coordinator.getState().observation, cleared)

  staleRead.resolve(observation())
  await tick()
  assert.equal(coordinator.getState().observation, cleared)
  assert.equal(coordinator.getState().snapshotStale, false)
})

test('ignores a late read after disposal', async () => {
  const lateRead = deferred<RequestCacheObservation>()
  const coordinator = createRequestCacheCoordinator({
    read: () => lateRead.promise,
    clear: async () => observation(),
    confirmClear: () => true,
  })

  coordinator.start()
  coordinator.dispose()
  lateRead.resolve(observation())
  await tick()

  assert.equal(coordinator.getState().observation, null)
})

test('read timeouts abort, ignore late replies, retain an old snapshot, and allow recovery', async () => {
  const first = deferred<RequestCacheObservation>()
  const lateRefresh = deferred<RequestCacheObservation>()
  const signals: AbortSignal[] = []
  const coordinator = createRequestCacheCoordinator({
    read: (signal) => {
      signals.push(signal)
      if (signals.length === 1) return first.promise
      if (signals.length === 3) return lateRefresh.promise
      return Promise.resolve(observation())
    },
    clear: async () => observation(), confirmClear: () => true, timeoutMs: 20,
  })
  coordinator.start()
  await new Promise((resolve) => setTimeout(resolve, 35))
  assert.equal(signals[0].aborted, true)
  assert.equal(coordinator.getState().phase, 'ready')
  assert.equal(coordinator.getState().observation, null)
  assert.match(coordinator.getState().error, /读取.*超时/)
  first.resolve(observation())
  await tick()
  assert.equal(coordinator.getState().observation, null)

  coordinator.refresh()
  await tick()
  const retained = coordinator.getState().observation
  assert.ok(retained)
  coordinator.refresh()
  await new Promise((resolve) => setTimeout(resolve, 35))
  assert.equal(signals[2].aborted, true)
  assert.equal(coordinator.getState().observation, retained)
  assert.equal(coordinator.getState().snapshotStale, true)
  assert.match(coordinator.getState().error, /刷新.*超时/)
  lateRefresh.resolve({ ...observation(), config: { ...observation().config, ttl_seconds: 75 } })
  await tick()
  assert.equal(coordinator.getState().observation, retained)
  coordinator.refresh()
  await tick()
  assert.equal(coordinator.getState().snapshotStale, false)
  assert.equal(coordinator.getState().error, '')
  coordinator.dispose()
})

test('reports a clear timeout as unknown and leaves confirmation with the user', async () => {
  let confirms = 0
  const coordinator = createRequestCacheCoordinator({
    read: async () => observation(),
    clear: () => new Promise<RequestCacheObservation>(() => undefined),
    confirmClear: () => {
      confirms += 1
      return false
    },
    timeoutMs: 20,
  })

  coordinator.start()
  await tick()
  coordinator.clear()
  assert.equal(confirms, 1)
  assert.equal(coordinator.getState().phase, 'ready')

  const accepted = createRequestCacheCoordinator({
    read: async () => observation(),
    clear: () => new Promise<RequestCacheObservation>(() => undefined),
    confirmClear: () => true,
    timeoutMs: 20,
  })
  accepted.start()
  await tick()
  accepted.clear()
  await new Promise((resolve) => setTimeout(resolve, 35))

  assert.equal(accepted.getState().snapshotStale, true)
  assert.match(accepted.getState().error, /结果未知/)
})
