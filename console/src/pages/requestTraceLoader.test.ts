import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { RequestTrace } from '../types.ts'
import { createRequestTraceLoader } from './requestTraceLoader.ts'

const tick = () => new Promise<void>((resolve) => setImmediate(resolve))
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => { resolve = done })
  return { promise, resolve }
}
function observe<T>(promise: Promise<T>) {
  const result = { settled: false, value: undefined as T | undefined, error: undefined as unknown }
  void promise.then((value) => { result.value = value; result.settled = true },
    (error: unknown) => { result.error = error; result.settled = true })
  return result
}
function trace(requestId = 'pf_req_a'): RequestTrace {
  return {
    request_id: requestId, started_at: 1_000, auth_token_id: 4, model: 'public-alias',
    client_protocol: 'openai', transport: 'http', is_streaming: true,
    final_status: 200, outcome: 'success', duration: 4, wait_duration: 0.5,
    attempt_count: 128, upstream_attempt_count: 128, upstream_success_count: 1, truncated: false,
    attempts: Array.from({ length: 128 }, (_, index) => ({
      number: index + 1, started_at: 1_000 + index, duration: 0.01, first_byte_time: 0,
      channel_id: 7, channel_name: 'Actual channel', key_index: 0, url_index: 0,
      endpoint: 'https://example.test/v1', actual_model: 'vendor/actual-model',
      upstream_protocol: 'openai', transport: 'http', status_code: 200,
      outcome: 'success', upstream: true, response_committed: true, reason: '', retry: 'none', cooldown: 'none', cooldown_until: 0,
    })),
  }
}

test('successful detail is available synchronously on reopening with every timeline event and actual route intact', async () => {
  const complete = trace(), reads: string[] = []
  const loader = createRequestTraceLoader(async (id) => { reads.push(id); return complete })
  assert.equal(await loader.load('pf_req_a'), complete)
  assert.equal(loader.peek('pf_req_a'), complete)
  assert.equal(await loader.load('pf_req_a'), complete)
  assert.deepEqual(reads, ['pf_req_a'])
  assert.equal(loader.peek('pf_req_a')?.attempts?.length, 128)
  assert.equal(loader.peek('pf_req_a')?.attempts?.[127].actual_model, 'vendor/actual-model')
  assert.equal(loader.peek('pf_req_a')?.attempts?.[127].channel_name, 'Actual channel')
})

test('concurrent callers share one read; aborting one subscriber leaves the other running', async () => {
  const gate = deferred<RequestTrace>(), first = new AbortController(), signals: AbortSignal[] = []
  const loader = createRequestTraceLoader((_id, signal) => { signals.push(signal); return gate.promise })
  const a = observe(loader.load('a', first.signal)), b = observe(loader.load('a'))
  first.abort(); await tick()
  assert.equal(a.settled, true)
  assert.equal((a.error as Error).name, 'AbortError')
  assert.equal(signals.length, 1)
  assert.equal(signals[0].aborted, false)
  const complete = trace('a'); gate.resolve(complete); await tick()
  assert.equal(b.value, complete)
  assert.equal(loader.peek('a'), complete)
})

test('missing detail automatically recovers after three short backoffs without a manual retry', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let reads = 0
  const complete = trace()
  const loader = createRequestTraceLoader(async () => {
    if (++reads <= 3) throw new Error('request trace not found')
    return complete
  })
  t.after(() => loader.clear())
  const result = observe(loader.load('a')); await tick()
  assert.equal(reads, 1)
  t.mock.timers.tick(399); await tick(); assert.equal(reads, 1)
  t.mock.timers.tick(1); await tick(); assert.equal(reads, 2)
  t.mock.timers.tick(700); await tick(); assert.equal(reads, 3)
  t.mock.timers.tick(900); await tick()
  assert.equal(reads, 4)
  assert.equal(result.value, complete)
})

test('persistent not-found stops after three retries within three seconds and can be retried manually', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let reads = 0, available = false
  const loader = createRequestTraceLoader(async () => {
    reads++
    if (!available) throw new Error('not_found: request trace')
    return trace('a')
  })
  t.after(() => loader.clear())
  const result = observe(loader.load('a')); await tick()
  for (const delay of [400, 700, 900]) { t.mock.timers.tick(delay); await tick() }
  assert.equal(result.settled, true)
  assert.equal((result.error as Error).name, 'RequestTracePendingError')
  t.mock.timers.tick(10_000); await tick(); assert.equal(reads, 4)
  assert.equal(loader.peek('a'), undefined)
  available = true
  assert.equal((await loader.load('a')).request_id, 'a')
  assert.equal(reads, 5)
})

test('ordinary errors settle immediately without retry or caching, including malformed 404 responses', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  for (const message of ['HTTP 500', 'Failed to fetch', '登录状态已失效',
    '服务返回了 HTML 页面（HTTP 404，接口 /admin/request-traces/a）', 'HTTP 500: dependency not found']) {
    let reads = 0
    const failure = new Error(message)
    const loader = createRequestTraceLoader(async () => { reads++; throw failure })
    const result = observe(loader.load('a')); await tick()
    assert.equal(result.error, failure, message)
    assert.equal(result.settled, true, message)
    t.mock.timers.tick(8_000); await tick(); assert.equal(reads, 1, message)
    assert.equal(loader.peek('a'), undefined)
    observe(loader.load('a')); await tick(); assert.equal(reads, 2)
    loader.clear()
  }
})

test('closing or switching aborts the last subscriber promptly and excludes late detail from cache', async () => {
  const gates = [deferred<RequestTrace>(), deferred<RequestTrace>()], signals: AbortSignal[] = []
  const loader = createRequestTraceLoader((_id, signal) => {
    signals.push(signal); return gates[signals.length - 1].promise
  })
  const parent = new AbortController(), old = observe(loader.load('old', parent.signal))
  parent.abort(); await tick()
  assert.equal(old.settled, true)
  assert.equal((old.error as Error).name, 'AbortError')
  assert.equal(signals[0].aborted, true)
  const replacement = observe(loader.load('new'))
  gates[0].resolve(trace('old')); gates[1].resolve(trace('new')); await tick()
  assert.equal(loader.peek('old'), undefined)
  assert.equal(replacement.value?.request_id, 'new')
})

test('aborting during a not-found backoff cancels all further retries', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let reads = 0
  const loader = createRequestTraceLoader(async () => { reads++; throw new Error('HTTP 404') })
  t.after(() => loader.clear())
  const parent = new AbortController(), result = observe(loader.load('a', parent.signal))
  await tick(); parent.abort(); await tick()
  assert.equal((result.error as Error).name, 'AbortError')
  t.mock.timers.tick(10_000); await tick(); assert.equal(reads, 1)
})

test('the eight second deadline settles a hung transport and a late success cannot poison manual retry', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const gate = deferred<RequestTrace>(), signals: AbortSignal[] = []
  const loader = createRequestTraceLoader((_id, signal) => {
    signals.push(signal)
    return signals.length === 1 ? gate.promise : Promise.resolve(trace('a'))
  })
  t.after(() => loader.clear())
  const result = observe(loader.load('a'))
  t.mock.timers.tick(7_999); await tick(); assert.equal(result.settled, false)
  t.mock.timers.tick(1); await tick()
  assert.equal(result.settled, true)
  assert.equal((result.error as Error).name, 'TimeoutError')
  assert.equal(signals[0].aborted, true)
  assert.equal(loader.peek('a'), undefined)
  const replacement = await loader.load('a')
  gate.resolve(trace('obsolete')); await tick()
  assert.equal(loader.peek('a'), replacement)
})

test('the deadline includes time already spent retrying not-found', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let reads = 0
  const signals: AbortSignal[] = []
  const loader = createRequestTraceLoader(async (_id, signal) => {
    signals.push(signal)
    if (++reads === 1) throw new Error('request trace not found')
    return new Promise<RequestTrace>(() => {})
  })
  t.after(() => loader.clear())
  const result = observe(loader.load('a')); await tick()
  t.mock.timers.tick(400); await tick(); assert.equal(reads, 2)
  t.mock.timers.tick(7_600); await tick()
  assert.equal(result.settled, true)
  assert.equal((result.error as Error).name, 'TimeoutError')
  assert.equal(signals[1].aborted, true)
})

test('rapid replacement keeps at most two physical reads even when cancellation is ignored', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const gates = [deferred<RequestTrace>(), deferred<RequestTrace>()], reads: string[] = []
  const loader = createRequestTraceLoader((id) => {
    reads.push(id); return reads.length <= 2 ? gates[reads.length - 1].promise : Promise.resolve(trace(id))
  })
  t.after(() => loader.clear())
  for (let index = 0; index < 50; index++) {
    const parent = new AbortController()
    observe(loader.load(String(index), parent.signal)); parent.abort()
  }
  const latest = observe(loader.load('latest')); await tick()
  assert.deepEqual(reads, ['0', '1'])
  gates[0].resolve(trace('0')); await tick()
  assert.equal(latest.value?.request_id, 'latest')
  gates[1].resolve(trace('1')); await tick()
  assert.equal(loader.peek('0'), undefined)
  assert.equal(loader.peek('1'), undefined)
})

test('queued work also times out if both cancelled transports never settle', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let reads = 0
  const loader = createRequestTraceLoader(() => { reads++; return new Promise<RequestTrace>(() => {}) })
  t.after(() => loader.clear())
  const first = new AbortController(), second = new AbortController()
  observe(loader.load('a', first.signal)); first.abort()
  observe(loader.load('b', second.signal)); second.abort()
  const latest = observe(loader.load('c'))
  t.mock.timers.tick(8_000); await tick()
  assert.equal(reads, 2)
  assert.equal(latest.settled, true)
  assert.equal((latest.error as Error).name, 'TimeoutError')
})

test('the 32-detail LRU keeps recently reopened entries and evicts old ones', async () => {
  const reads: string[] = []
  const loader = createRequestTraceLoader(async (id) => { reads.push(id); return trace(id) })
  for (let index = 0; index < 32; index++) await loader.load(String(index))
  assert.ok(loader.peek('0'))
  await loader.load('32')
  assert.equal(loader.peek('1'), undefined)
  assert.ok(loader.peek('0'))
  await loader.load('0'); assert.equal(reads.length, 33)
  await loader.load('1'); assert.equal(reads.length, 34)
})

test('separate mounted owners never share cache; owner cleanup aborts work and clears saved detail', async () => {
  const gate = deferred<RequestTrace>(), signals: AbortSignal[] = []
  const first = createRequestTraceLoader(async (id, signal) => {
    signals.push(signal); return id === 'saved' ? trace(id) : gate.promise
  })
  const second = createRequestTraceLoader(async (id) => trace(id))
  await first.load('saved')
  assert.equal(second.peek('saved'), undefined)
  const pending = observe(first.load('pending')); first.clear(); await tick()
  assert.equal(first.peek('saved'), undefined)
  assert.equal(pending.settled, true)
  assert.equal((pending.error as Error).name, 'AbortError')
  assert.equal(signals[1].aborted, true)
  gate.resolve(trace('pending')); await tick(); assert.equal(first.peek('pending'), undefined)
  assert.equal((await first.load('saved')).request_id, 'saved')
})

test('an already aborted caller starts no work and cannot read a cached detail', async () => {
  let reads = 0
  const loader = createRequestTraceLoader(async (id) => { reads++; return trace(id) })
  await loader.load('saved')
  const parent = new AbortController(); parent.abort()
  const cached = observe(loader.load('saved', parent.signal)), fresh = observe(loader.load('fresh', parent.signal))
  await tick()
  assert.equal((cached.error as Error).name, 'AbortError')
  assert.equal((fresh.error as Error).name, 'AbortError')
  assert.equal(reads, 1)
})
