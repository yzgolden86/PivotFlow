import assert from 'node:assert/strict'
import test from 'node:test'
import type { RequestAttempt, RequestTrace } from '../types.ts'
import * as history from './requestHistory.ts'

type ChannelObservation = { channelId: number; channelName: string; label: string; title: string }
const observer = Reflect.get(history, 'requestChannelObservation') as (trace: RequestTrace) => ChannelObservation
const observe = (trace: RequestTrace) => {
  assert.equal(typeof observer, 'function', 'Request history must expose the actual channel observation')
  return observer(trace)
}
const base: RequestTrace = {
  request_id: 'pf_req_channel_fixture', started_at: 1000, auth_token_id: 7,
  model: 'deepseek-v4.1-flash', client_protocol: 'openai', transport: 'http',
  is_streaming: false, final_status: 200, outcome: 'success', duration: 1,
  wait_duration: 0, attempt_count: 3, upstream_attempt_count: 2,
  upstream_success_count: 1, truncated: false,
}
const attempt = (number: number, id: number, name: string, upstream = true): RequestAttempt => ({
  number, channel_id: id, channel_name: name, upstream, started_at: 1000 - number,
  duration: 1, first_byte_time: 0, upstream_protocol: 'openai', transport: 'http',
  status_code: upstream ? 502 : 429, outcome: upstream ? 'error' : 'skipped',
  response_committed: false, reason: '', retry: 'none', cooldown: 'none', cooldown_until: 0,
})

test('shows the recorded channel after the retained timeline reaches its limit', () => {
  const result = observe({ ...base, channel_id: 140, channel_name: '最终渠道', truncated: true,
    attempts: [attempt(1, 1, '早期渠道')] } as RequestTrace)
  assert.equal(result.channelId, 140)
  assert.equal(result.channelName, '最终渠道')
  assert.equal(result.label, '最终渠道')
  assert.match(result.title, /最后一次.*实际|最后一次.*上游/)
})

test('restores the last executed legacy channel and ignores a later skipped candidate', () => {
  const attempts = [attempt(3, 3, '未发送候选', false), attempt(1, 1, '首次渠道'), attempt(2, 2, '实际回退渠道')]
  const before = JSON.stringify(attempts)
  const result = observe({ ...base, outcome: 'error', final_status: 503, attempts })
  assert.equal(result.channelId, 2)
  assert.equal(result.label, '实际回退渠道')
  assert.equal(JSON.stringify(attempts), before)
})

test('uses an ID fallback without borrowing a name from an earlier channel', () => {
  const result = observe({ ...base, channel_id: 42, channel_name: '', attempts: [attempt(1, 7, '另一渠道')] } as RequestTrace)
  assert.equal(result.channelId, 42)
  assert.equal(result.channelName, '')
  assert.equal(result.label, '渠道 #42')
})

test('keeps the name when a saved channel has no available numeric identity', () => {
  const result = observe({ ...base, channel_name: '已删除的历史渠道' } as RequestTrace)
  assert.equal(result.channelId, 0)
  assert.equal(result.label, '已删除的历史渠道')
})

test('does not use an earlier channel when the last legacy transport has no routing metadata', () => {
  const result = observe({ ...base, attempts: [attempt(1, 1, '早期渠道'), attempt(2, 0, '')] })
  assert.equal(result.channelId, 0)
  assert.equal(result.channelName, '')
  assert.equal(result.label, '渠道未记录')
})

test('does not guess a final channel from a truncated legacy prefix or redacted summary', () => {
  assert.equal(observe({ ...base, truncated: true, attempts: [attempt(1, 1, '早期渠道')] }).label, '渠道未记录')
  assert.equal(observe(base).label, '渠道未记录')
})

test('distinguishes cache hits and unsent requests without retaining stale channel metadata', () => {
  const unsent = { ...base, upstream_attempt_count: 0, channel_id: 42, channel_name: '候选渠道',
    attempts: [attempt(1, 42, '候选渠道', false)] } as RequestTrace
  assert.equal(observe(unsent).label, '未请求上游')
  assert.equal(observe(unsent).channelId, 0)
  assert.equal(observe(unsent).channelName, '')
  const cached = observe({ ...unsent, cache_status: 'hit' })
  assert.equal(cached.label, '缓存命中 · 未请求上游')
  assert.equal(cached.channelId, 0)
})

test('includes the observed channel in diagnostics without copying arbitrary fields', () => {
  const diagnostic = history.buildRequestDiagnostic({ ...base, channel_id: 140, channel_name: '最终渠道',
    truncated: true, arbitrary_field: 'do-not-copy' } as RequestTrace) as unknown as Record<string, unknown>
  assert.equal(diagnostic.channel_id, 140)
  assert.equal(diagnostic.channel_name, '最终渠道')
  assert.equal('arbitrary_field' in diagnostic, false)
  const redacted = history.buildRequestDiagnostic(base) as unknown as Record<string, unknown>
  assert.equal('channel_id' in redacted, false)
  assert.equal('channel_name' in redacted, false)
})
