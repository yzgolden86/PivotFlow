import assert from 'node:assert/strict'
import test from 'node:test'
import type { RequestAttempt, RequestTrace } from '../types.ts'
import * as history from './requestHistory.ts'

type ModelObservation = { actualModel: string; label: string; title: string }
const observe = Reflect.get(history, 'requestModelObservation') as (trace: RequestTrace) => ModelObservation
const base: RequestTrace = {
  request_id: 'pf_req_synthetic', started_at: 1000, auth_token_id: 7,
  model: 'deepseek-v4.1-flash', client_protocol: 'openai', transport: 'http',
  is_streaming: false, final_status: 200, outcome: 'success', duration: 1,
  wait_duration: 0, attempt_count: 3, upstream_attempt_count: 2,
  upstream_success_count: 1, truncated: false,
}
const attempt = (number: number, actual: string, upstream = true): RequestAttempt => ({
  number, actual_model: actual, upstream, started_at: 1000 - number, duration: 1,
  first_byte_time: 0, upstream_protocol: 'openai', transport: 'http', status_code: 200,
  outcome: upstream ? 'success' : 'skipped', response_committed: upstream,
  reason: '', retry: 'none', cooldown: 'none', cooldown_until: 0,
})

test('uses the recorded upstream scalar even when the retained timeline is truncated', () => {
  assert.equal(typeof observe, 'function')
  const result = observe({ ...base, actual_model: 'channel1/DeepSeek-V4.1-Flash-1017', truncated: true } as RequestTrace)
  assert.equal(result.actualModel, 'channel1/DeepSeek-V4.1-Flash-1017')
  assert.equal(result.label, '实际上游')
  assert.match(result.title, /channel1\/DeepSeek-V4\.1-Flash-1017/)
})

test('selects the last transmitted legacy attempt by execution number, without mutating it', () => {
  assert.equal(typeof observe, 'function')
  const attempts = [attempt(3, 'never-sent', false), attempt(1, 'vendor/first'), attempt(2, 'vendor/final')]
  const before = JSON.stringify(attempts)
  assert.equal(observe({ ...base, attempts }).actualModel, 'vendor/final')
  assert.equal(JSON.stringify(attempts), before)
})

test('labels failed routes as the last upstream instead of implying a successful destination', () => {
  assert.equal(typeof observe, 'function')
  const result = observe({ ...base, outcome: 'error', final_status: 503, attempts: [attempt(1, 'vendor/failed')] })
  assert.equal(result.actualModel, 'vendor/failed')
  assert.equal(result.label, '最后上游')
})

test('does not invent a final model from a truncated legacy prefix or a missing final observation', () => {
  assert.equal(typeof observe, 'function')
  assert.equal(observe({ ...base, truncated: true, attempts: [attempt(1, 'vendor/partial-prefix')] }).actualModel, '')
  assert.equal(observe({ ...base, attempts: [attempt(1, 'vendor/earlier'), attempt(2, '')] }).actualModel, '')
  assert.equal(observe(base).label, '上游模型未提供')
})

test('distinguishes an unsent request and a cache hit without copying the canonical name', () => {
  assert.equal(typeof observe, 'function')
  const noUpstream = { ...base, attempt_count: 0, upstream_attempt_count: 0, upstream_success_count: 0 }
  assert.deepEqual(observe(noUpstream), { actualModel: '', label: '未请求上游', title: '本次请求没有已记录的上游传输。' })
  const hit = observe({ ...noUpstream, cache_status: 'hit' })
  assert.equal(hit.actualModel, '')
  assert.equal(hit.label, '缓存命中 · 未请求上游')
})
