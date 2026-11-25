import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { SitePricingSnapshot } from '../types.ts'
import { createModelPricingLoader } from './modelPricingLoader.ts'

const tick = () => new Promise<void>((resolve) => setImmediate(resolve))
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}
const snapshot = (siteId: number): SitePricingSnapshot => ({ site_id: siteId, available: true, models: [], group_ratio: {} })

test('121 required sites use at most three active reads and eventually publish every site', async () => {
  const gate = deferred<void>()
  const reads: number[] = [], published: number[] = []
  let active = 0, peak = 0
  const loader = createModelPricingLoader(async (siteId) => {
    reads.push(siteId)
    peak = Math.max(peak, ++active)
    await gate.promise
    active--
    return snapshot(siteId)
  })
  const done = loader.load(Array.from({ length: 121 }, (_, index) => index + 1), (siteId) => published.push(siteId))
  await tick()
  const initialReads = reads.length
  gate.resolve()
  await done
  assert.equal(initialReads, 3)
  assert.equal(peak, 3)
  assert.equal(active, 0)
  assert.deepEqual(reads, Array.from({ length: 121 }, (_, index) => index + 1))
  assert.deepEqual(published.slice().sort((a, b) => a - b), Array.from({ length: 121 }, (_, index) => index + 1))
})

test('only distinct positive safe integer site IDs are read, in their first-seen order', async () => {
  const reads: number[] = []
  const loader = createModelPricingLoader(async (siteId) => { reads.push(siteId); return snapshot(siteId) })
  await loader.load([2, 1, 2, 0, -1, 1.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1, 1, 3], () => {})
  assert.deepEqual(reads, [2, 1, 3])
})

test('a fast snapshot publishes and releases its slot while other prices remain pending', async () => {
  const gates = new Map<number, ReturnType<typeof deferred<SitePricingSnapshot>>>()
  const reads: number[] = [], published: number[] = []
  const loader = createModelPricingLoader((siteId) => {
    reads.push(siteId)
    const gate = deferred<SitePricingSnapshot>(); gates.set(siteId, gate)
    return gate.promise
  })
  let completed = false
  const done = loader.load([1, 2, 3, 4], (siteId) => published.push(siteId)).then(() => { completed = true })
  await tick()
  gates.get(2)!.resolve(snapshot(2))
  await tick()
  const early = { published: [...published], reads: [...reads], completed }
  for (const [siteId, gate] of gates) gate.resolve(snapshot(siteId))
  await done
  assert.deepEqual(early.published, [2])
  assert.deepEqual(early.reads, [1, 2, 3, 4])
  assert.equal(early.completed, false)
})

test('synchronous and asynchronous read failures and malformed snapshots do not block remaining sites', async () => {
  const reads: number[] = [], published: SitePricingSnapshot[] = []
  const complete: SitePricingSnapshot = {
    site_id: 6, available: true, group_ratio: { default: 1, vip: 0.5 },
    models: [{ model: 'synthetic-model', groups: ['default', 'vip'], quota_type: 1,
      model_ratio: 2, completion_ratio: 3, per_call_price: 0.25,
      cache_ratio: 0.1, cache_creation_ratio: 0.2,
      input_price: 0, output_price: 9, cache_read_price: 0.1, cache_write_price: 0.2 }],
  }
  const loader = createModelPricingLoader((siteId) => {
    reads.push(siteId)
    if (siteId === 1) throw new Error('synchronous management read failure')
    if (siteId === 2) return Promise.reject(new Error('management read unavailable'))
    if (siteId === 3) return Promise.resolve({ ...snapshot(3), models: null } as unknown as SitePricingSnapshot)
    if (siteId === 4) return Promise.resolve({ ...snapshot(4), available: false })
    if (siteId === 5) return Promise.resolve({ ...snapshot(5), group_ratio: undefined } as unknown as SitePricingSnapshot)
    return Promise.resolve(complete)
  })
  await loader.load([1, 2, 3, 4, 5, 6], (_, value) => published.push(value))
  assert.deepEqual(reads, [1, 2, 3, 4, 5, 6])
  assert.deepEqual(published.map((value) => value.site_id).sort(), [4, 5, 6])
  assert.equal(published.find((value) => value.site_id === 4)!.available, false)
  assert.deepEqual(published.find((value) => value.site_id === 5)!.group_ratio, {})
  assert.deepEqual(published.find((value) => value.site_id === 6), complete)
})

test('parent cancellation aborts active reads, settles promptly, skips queued sites and ignores late success', async () => {
  const gate = deferred<void>(), parent = new AbortController()
  const reads: number[] = [], signals: AbortSignal[] = [], published: number[] = []
  const loader = createModelPricingLoader(async (siteId, _, signal) => {
    reads.push(siteId); signals.push(signal)
    await gate.promise // Deliberately ignore cancellation to exercise stale-result protection.
    return snapshot(siteId)
  })
  let completed = false
  const done = loader.load([1, 2, 3, 4, 5], (siteId) => published.push(siteId), { signal: parent.signal }).then(() => { completed = true })
  await tick(); parent.abort(); await tick()
  const settledOnAbort = completed, aborted = signals.every((signal) => signal.aborted)
  gate.resolve(); await done; await tick()
  assert.equal(settledOnAbort, true)
  assert.equal(aborted, true)
  assert.deepEqual(reads, [1, 2, 3])
  assert.deepEqual(published, [])
})

test('a replacement load cancels old work and shares the three read slots until old reads settle', async () => {
  const oldGate = deferred<void>(), newGate = deferred<void>()
  const reads: number[] = [], signals: AbortSignal[] = [], published: number[] = []
  let active = 0, peak = 0, oldCompleted = false
  const loader = createModelPricingLoader(async (siteId, _, signal) => {
    reads.push(siteId); signals.push(signal)
    peak = Math.max(peak, ++active)
    await (siteId < 9 ? oldGate.promise : newGate.promise)
    active--
    return snapshot(siteId)
  })
  const old = loader.load([1, 2, 3, 4, 5, 6], (siteId) => published.push(siteId)).then(() => { oldCompleted = true })
  await tick()
  const next = loader.load([9, 10, 11, 12], (siteId) => published.push(siteId))
  await tick()
  const beforeRelease = { reads: [...reads], oldCompleted, aborted: signals.slice(0, 3).every((signal) => signal.aborted) }
  oldGate.resolve(); await tick(); newGate.resolve()
  await Promise.all([old, next]); await tick()
  assert.deepEqual(beforeRelease.reads, [1, 2, 3])
  assert.equal(beforeRelease.oldCompleted, true)
  assert.equal(beforeRelease.aborted, true)
  assert.equal(peak, 3)
  assert.deepEqual(reads, [1, 2, 3, 9, 10, 11, 12])
  assert.deepEqual(published, [9, 10, 11, 12])
})

test('owner disposal and an empty replacement both cancel active and queued pricing work', async () => {
  for (const cancel of ['abort', 'empty'] as const) {
    const gate = deferred<void>(), reads: number[] = [], signals: AbortSignal[] = [], published: number[] = []
    const loader = createModelPricingLoader(async (siteId, _, signal) => {
      reads.push(siteId); signals.push(signal); await gate.promise; return snapshot(siteId)
    })
    const done = loader.load([1, 2, 3, 4], (siteId) => published.push(siteId))
    await tick()
    if (cancel === 'abort') loader.abort()
    else await loader.load([], () => {})
    const aborted = signals.every((signal) => signal.aborted)
    gate.resolve(); await done; await tick()
    assert.equal(aborted, true, cancel)
    assert.deepEqual(reads, [1, 2, 3], cancel)
    assert.deepEqual(published, [], cancel)
  }
})

test('an already-aborted parent starts no reads and an old parent cannot cancel a replacement', async () => {
  const reads: Array<{ siteId: number; refresh: boolean; signal: AbortSignal }> = []
  const gate = deferred<void>(), oldParent = new AbortController()
  const loader = createModelPricingLoader(async (siteId, refresh, signal) => {
    reads.push({ siteId, refresh, signal })
    if (siteId !== 1) await gate.promise
    return snapshot(siteId)
  })
  const cancelled = new AbortController(); cancelled.abort()
  await loader.load([1], () => {}, { signal: cancelled.signal })
  assert.equal(reads.length, 0)
  const old = loader.load([2], () => {}, { signal: oldParent.signal })
  const next = loader.load([3], () => {}, { refresh: true })
  oldParent.abort(); await tick()
  const nextSignal = reads.find((read) => read.siteId === 3)?.signal
  gate.resolve(); await Promise.all([old, next])
  assert.equal(nextSignal?.aborted, false)
  assert.deepEqual(reads.map(({ siteId, refresh }) => ({ siteId, refresh })), [
    { siteId: 2, refresh: false }, { siteId: 3, refresh: true },
  ])
})
