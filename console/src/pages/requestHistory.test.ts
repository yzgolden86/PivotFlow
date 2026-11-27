import assert from 'node:assert/strict'
import test from 'node:test'
import type { RequestTrace, RequestTraceStats } from '../types.ts'
import {
  attemptCommitmentLabel,
  attemptChannelLabel,
  attemptCredentialLabel,
  attemptModelLabel,
  buildRequestDiagnostic,
  buildRequestTimeline,
  clientSuccessRate,
  cooldownLabel,
  compactRequestId,
  finalStatusHint,
  finalStatusLabel,
  formatRate,
  isPendingRequestTraceError,
  reasonLabel,
  requestCacheHistoryDiagnostic,
  requestDetailHref,
  readRequestHistoryScope,
  requestAttemptSummary,
  retryLabel,
  upstreamSuccessRate,
  writeRequestHistoryScope,
} from './requestHistory.ts'

const stats: RequestTraceStats = {
  requests: 9,
  successes: 5,
  errors: 3,
  canceled: 1,
  upstream_attempts: 8,
  upstream_successes: 6,
}

test('keeps canceled requests out of the client success denominator', () => {
  assert.equal(clientSuccessRate(stats), 0.625)
  assert.equal(upstreamSuccessRate(stats), 0.75)
  assert.equal(formatRate(clientSuccessRate(stats)), '62.5%')
  assert.equal(formatRate(clientSuccessRate({ ...stats, successes: 0, errors: 0 })), '—')
})

test('summarizes upstream failures that were recovered by a later success', () => {
  const summary = requestAttemptSummary({
    attempt_count: 3,
    upstream_attempt_count: 2,
    upstream_success_count: 1,
    outcome: 'success',
    attempt_summaries: [
      { number: 1, started_at: 1_000, status_code: 401, outcome: 'error', upstream: true },
      { number: 2, started_at: 1_250, status_code: 200, outcome: 'success', upstream: true },
    ],
  })

  assert.equal(summary.upstream_failures, 1)
  assert.equal(summary.has_failure, true)
  assert.equal(summary.label, '1 次失败 → 最终成功')
  assert.match(summary.title, /上游 2 次尝试/)
  assert.deepEqual(summary.events, [
    { number: 1, started_at: 1_000, status_code: 401, outcome: 'error', upstream: true },
    { number: 2, started_at: 1_250, status_code: 200, outcome: 'success', upstream: true },
  ])
})

test('keeps a final error visible when no upstream attempt succeeded', () => {
  const summary = requestAttemptSummary({
    attempt_count: 4,
    upstream_attempt_count: 3,
    upstream_success_count: 0,
    outcome: 'error',
  })

  assert.equal(summary.upstream_failures, 3)
  assert.equal(summary.has_failure, true)
  assert.equal(summary.label, '3 次失败 · 最终失败')
})

test('preserves the upstream-failure filter in the request-history URL', () => {
  const scope = readRequestHistoryScope('?view=requests&range=this_week&upstream_failures=1')
  assert.equal(scope.upstreamFailuresOnly, true)
  assert.match(writeRequestHistoryScope(scope), /upstream_failures=1/)
  assert.equal(readRequestHistoryScope('?upstream_failures=invalid').error, '上游失败筛选无效。')
})

test('orders attempts by execution number even when timestamps invert, without mutating the trace', () => {
  const trace: RequestTrace = {
    request_id: 'pf_req_test',
    started_at: 1_000,
    auth_token_id: 4,
    model: 'example-model',
    client_protocol: 'openai',
    transport: 'http',
    is_streaming: true,
    final_status: 200,
    outcome: 'success',
    duration: 4,
    wait_duration: 0.5,
    attempt_count: 3,
    upstream_attempt_count: 2,
    upstream_success_count: 1,
    truncated: false,
    attempts: [
      { number: 3, started_at: 2_000, duration: 1 },
      { number: 1, started_at: 3_000, duration: 0.25 },
      { number: 2, started_at: 2_000, duration: 0.25 },
    ].map((attempt) => ({
      ...attempt,
      first_byte_time: 0,
      channel_id: 1,
      channel_name: 'Channel',
      key_index: 0,
      url_index: 0,
      endpoint: 'https://example.test',
      actual_model: 'example-model',
      upstream_protocol: 'openai',
      transport: 'http',
      status_code: 200,
      outcome: 'success',
      upstream: true,
      response_committed: true,
      reason: 'success',
      retry: 'none',
      cooldown: 'none',
      cooldown_until: 0,
    })),
  }

  const timeline = buildRequestTimeline(trace)
  assert.deepEqual(timeline.map((entry) => entry.attempt.number), [1, 2, 3])
  assert.deepEqual(timeline.map((entry) => entry.offset_seconds), [2, 1, 1])
  assert.deepEqual(timeline.map((entry) => entry.gap_seconds), [null, 0, 0])
  assert.deepEqual(trace.attempts?.map((attempt) => attempt.number), [3, 1, 2])
})

test('sanitized diagnostics contain only the documented request and attempt fields', () => {
  const trace = {
    request_id: 'pf_req_diagnostic',
    started_at: 1_000,
    auth_token_id: 2,
    model: 'model',
    client_protocol: 'openai',
    transport: 'http',
    is_streaming: false,
    final_status: 200,
    outcome: 'success',
    duration: 1,
    wait_duration: 0,
    attempt_count: 1,
    upstream_attempt_count: 1,
    upstream_success_count: 1,
    truncated: false,
    raw_secret: 'must-not-copy',
    attempts: [{
      number: 1,
      started_at: 1_000,
      duration: 1,
      first_byte_time: 0.1,
      channel_id: 3,
      channel_name: 'Channel',
      key_index: 0,
      url_index: 0,
      endpoint: 'https://example.test',
      actual_model: 'model',
      upstream_protocol: 'openai',
      transport: 'http',
      status_code: 200,
      outcome: 'success',
      upstream: true,
      response_committed: true,
      reason: 'success',
      retry: 'none',
      cooldown: 'none',
      cooldown_until: 0,
      authorization: 'must-not-copy',
    }],
  } as unknown as RequestTrace

  const diagnostic = buildRequestDiagnostic(trace) as unknown as Record<string, unknown>
  assert.equal('raw_secret' in diagnostic, false)
  const attempt = (diagnostic.attempts as Record<string, unknown>[])[0]
  assert.equal('authorization' in attempt, false)
  assert.equal(diagnostic.request_id, 'pf_req_diagnostic')
  assert.equal(attempt.endpoint, 'https://example.test')
})

test('builds a production-shaped request detail deep link', () => {
  const requestId = 'pf_req_0123456789abcdef0123456789abcdef'
  assert.equal(
    requestDetailHref(requestId),
    `#/logs?view=requests&request=${requestId}`,
  )
})

test('compacts long request IDs for dense log rows', () => {
  assert.equal(compactRequestId('pf_req_0123456789abcdef0123456789abcdef'), 'pf_req_01234…abcdef')
  assert.equal(compactRequestId('pf_req_short'), 'pf_req_short')
})

test('maps fixed reason, retry, and cooldown codes to Chinese labels', () => {
  const reasons = {
    upstream_auth: '上游认证失败',
    rate_limited: '上游限流',
    quota_exceeded: '上游配额用尽',
    stream_error: '流式响应错误',
    first_byte_timeout: '首字节超时',
    stream_incomplete: '流式响应不完整',
    upstream_error: '上游错误',
    local_error: '本地处理错误',
    client_canceled: '客户端已取消',
    protocol_unsupported: '上游协议不支持',
    channel_rpm_limit: '渠道请求频率限制',
    channel_concurrency_limit: '渠道并发已满',
    websocket_handshake_fallback: 'WebSocket 握手回退',
    stream_options_unsupported: '流式选项不支持',
    websocket_reconnect_fallback: 'WebSocket 重连回退',
    request_compatibility: '请求兼容性重试',
    all_keys_unavailable: '所有 Key 不可用',
    channel_unavailable: '渠道不可用',
    no_compatible_protocol: '无兼容协议',
    websocket_reconnect: 'WebSocket 重连',
  }
  for (const [code, label] of Object.entries(reasons)) {
    assert.equal(reasonLabel(code), label)
    assert.equal(reasonLabel(code).includes(code), false)
  }
  assert.equal(reasonLabel(''), '未记录原因')
  assert.equal(retryLabel('next_channel'), '换渠道重试')
  assert.equal(cooldownLabel('model'), '模型冷却')
})

test('labels internal final statuses without presenting them as client HTTP codes', () => {
  assert.equal(finalStatusLabel(200), '状态 HTTP 200')
  assert.equal(finalStatusLabel(499), '状态 客户端取消')
  assert.equal(finalStatusLabel(597), '状态 流错误')
  assert.equal(finalStatusLabel(598), '状态 首字超时')
  assert.equal(finalStatusLabel(599), '状态 流不完整')
  assert.equal(finalStatusLabel(599).includes('599'), false)
  assert.match(finalStatusHint(599), /内部状态码 599/)
  assert.match(finalStatusHint(599), /HTTP 200 或 WebSocket/)
  assert.equal(finalStatusHint(200), '客户端 HTTP 状态 200')
  assert.equal(finalStatusHint(0), '未记录最终状态')
})

test('explains committed non-success attempts as started but not replayed', () => {
  assert.equal(attemptCommitmentLabel({ outcome: 'success', response_committed: true }), '响应已提交')
  assert.equal(attemptCommitmentLabel({ outcome: 'error', response_committed: true }), '响应已开始，未重放')
  assert.equal(attemptCommitmentLabel({ outcome: 'canceled', response_committed: true }), '响应已开始，未重放')
  assert.equal(attemptCommitmentLabel({ outcome: 'error', response_committed: false }), '响应未提交')
})

test('renders token-scoped attempts without internal routing fields', () => {
  const attempt = {
    number: 1,
    started_at: 1_000,
    duration: 1,
    first_byte_time: 0.1,
    upstream_protocol: 'openai',
    transport: 'http',
    status_code: 200,
    outcome: 'success',
    upstream: true,
    response_committed: true,
    reason: 'success',
    retry: 'none',
    cooldown: 'none',
    cooldown_until: 0,
  } as const
  assert.equal(attemptChannelLabel(attempt), '渠道信息不可见')
  assert.equal(attemptCredentialLabel(attempt), '凭证与 URL 序号不可见')
  assert.equal(attemptModelLabel(attempt), '上游模型不可见')
})

test('treats a missing saved trace as still pending, not a permanent failure', () => {
  assert.equal(isPendingRequestTraceError(new Error('not_found: request trace')), true)
  assert.equal(isPendingRequestTraceError(new Error('HTTP 404')), true)
  assert.equal(isPendingRequestTraceError(new Error('HTTP 500')), false)
})

test('explains a cache hit without inventing upstream work or prompt-cache usage', () => {
  const diagnostic = requestCacheHistoryDiagnostic({
    cache_status: 'hit',
    cache_reason: 'reused',
    upstream_attempt_count: 0,
  })

  assert.equal(diagnostic?.label, '缓存命中')
  assert.equal(diagnostic?.reasonLabel, '已复用缓存响应')
  assert.match(diagnostic?.explanation || '', /未调用上游/)
  assert.match(diagnostic?.explanation || '', /不新增上游用量或费用/)
  assert.doesNotMatch(diagnostic?.explanation || '', /prompt|token 用量/i)
})

test('distinguishes an interrupted cache response from a successful delivery', () => {
  for (const reason of ['write_failed', 'canceled']) {
    const diagnostic = requestCacheHistoryDiagnostic({
      cache_status: 'hit', cache_reason: reason, upstream_attempt_count: 0,
    })
    assert.match(diagnostic?.explanation || '', /未完整写出|未完成/)
    assert.match(diagnostic?.explanation || '', /不记.*成功|未记.*成功/)
    assert.match(diagnostic?.explanation || '', /未调用上游/)
  }
  const incomplete = requestCacheHistoryDiagnostic({ cache_status: 'hit', cache_reason: 'reused' })
  assert.doesNotMatch(incomplete?.explanation || '', /undefined/)
  assert.match(incomplete?.explanation || '', /无法确认|未提供/)
})

test('keeps empty and unknown cache metadata safe in old and future traces', () => {
  assert.equal(requestCacheHistoryDiagnostic({}), null)
  const unknown = requestCacheHistoryDiagnostic({ cache_status: 'future_raw_code', cache_reason: 'future_raw_reason' })
  assert.equal(unknown?.label, '未知缓存状态')
  assert.equal(unknown?.reasonLabel, '未知缓存原因')
  assert.doesNotMatch(`${unknown?.label}${unknown?.reasonLabel}${unknown?.explanation}`, /future_raw/)
})

test('copies only recognized cache metadata into the sanitized diagnostic JSON', () => {
  const trace = {
    request_id: 'pf_req_cache',
    started_at: 1_000,
    auth_token_id: 2,
    client_protocol: 'openai',
    transport: 'http',
    is_streaming: false,
    final_status: 200,
    outcome: 'success',
    duration: 1,
    wait_duration: 0,
    attempt_count: 0,
    upstream_attempt_count: 0,
    upstream_success_count: 0,
    truncated: false,
    cache_status: 'hit',
    cache_reason: 'reused',
  } as RequestTrace
  const unknown = { ...trace, cache_status: 'raw_status', cache_reason: 'raw_reason' }

  const diagnostic = buildRequestDiagnostic(trace)
  assert.equal(diagnostic.cache_status, 'hit')
  assert.equal(diagnostic.cache_reason, 'reused')
  const safeUnknown = buildRequestDiagnostic(unknown)
  assert.equal(safeUnknown.cache_status, undefined)
  assert.equal(safeUnknown.cache_reason, undefined)
})
