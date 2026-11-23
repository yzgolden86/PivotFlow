import type {
  RequestAttempt,
  RequestAttemptOutcome,
  RequestCooldownScope,
  RequestOutcome,
  RequestRetryReason,
  RequestTrace,
  RequestTraceStats,
} from '../types'
import { repeatedScopeParameter, routeProtocols, validObjectId, validRouteText } from './modelRoutes.ts'
import {
  cacheReasonLabel,
  cacheStatusLabel,
  isRequestCacheReason,
  isRequestCacheStatus,
} from './requestCache.ts'
import type { RequestCacheStatus } from '../types'

export interface RequestHistoryScope {
  range: 'today' | 'this_week' | 'this_month'
  authTokenId: string
  model: string
  clientProtocol: string
  outcome: 'success' | 'error' | 'canceled' | ''
  search: string
  page: number
  error: string
}

export function readRequestHistoryScope(search: string): RequestHistoryScope {
  const params = new URLSearchParams(search)
  const range = params.get('range')
  const rawOutcome = params.get('outcome') || ''
  const page = Number(params.get('page') || 1)
  const scope: RequestHistoryScope = {
    range: range === 'this_week' || range === 'this_month' ? range : 'today',
    authTokenId: params.get('auth_token_id') || '', model: params.get('model') || '',
    clientProtocol: params.get('client_protocol') || '', outcome: rawOutcome === 'success' || rawOutcome === 'error' || rawOutcome === 'canceled' ? rawOutcome : '',
    search: params.get('search') || '', page: Number.isSafeInteger(page) && page > 0 ? page : 1, error: '',
  }
  if (repeatedScopeParameter(params)) scope.error = '历史范围包含重复参数，请清除筛选后重试。'
  else if (!validRouteText(scope.model, 191)) scope.error = '精确历史模型最多 191 字符，且不能包含控制字符。'
  else if (scope.clientProtocol && !routeProtocols.some((item) => item.value === scope.clientProtocol)) scope.error = '历史客户端协议无效，请重新选择。'
  else if (params.has('auth_token_id') && !validObjectId(scope.authTokenId)) scope.error = '历史令牌 ID 无效，请明确选择范围。'
  else if (!validRouteText(scope.search, 200)) scope.error = '历史搜索最多 200 字符，且不能包含控制字符。'
  else if (rawOutcome && !scope.outcome) scope.error = '最终结果筛选无效。'
  return scope
}

export function writeRequestHistoryScope(scope: RequestHistoryScope, search = ''): string {
  const params = new URLSearchParams(search)
  params.set('view', 'requests')
  for (const [key, value] of Object.entries({ range: scope.range, auth_token_id: scope.authTokenId, model: scope.model, client_protocol: scope.clientProtocol, outcome: scope.outcome, search: scope.search, page: scope.page > 1 ? String(scope.page) : '' })) {
    params.delete(key)
    if (value) params.set(key, value)
  }
  return `?${params}`
}

export interface RequestTimelineEntry {
  attempt: RequestAttempt
  offset_seconds: number
  end_offset_seconds: number
  gap_seconds: number | null
}

export interface RequestDiagnosticAttempt {
  number: number
  started_at: number
  duration: number
  first_byte_time: number
  channel_id?: number
  channel_name?: string
  key_index?: number
  url_index?: number
  endpoint?: string
  actual_model?: string
  upstream_protocol: string
  transport: string
  status_code: number
  outcome: RequestAttemptOutcome
  upstream: boolean
  response_committed: boolean
  reason: string
  retry: RequestRetryReason
  cooldown: RequestCooldownScope
  cooldown_until: number
}

export interface RequestDiagnostic {
  request_id: string
  started_at: number
  auth_token_id: number
  model?: string
  client_protocol: string
  transport: 'http' | 'websocket'
  is_streaming: boolean
  final_status: number
  outcome: RequestOutcome
  duration: number
  wait_duration: number
  attempt_count: number
  upstream_attempt_count: number
  upstream_success_count: number
  truncated: boolean
  attempts?: RequestDiagnosticAttempt[]
  cache_status?: string
  cache_reason?: string
}

export interface RequestCacheHistoryDiagnostic {
  status: RequestCacheStatus | 'unknown'
  label: string
  reasonLabel: string
  explanation: string
}

function finiteNonNegative(value: number): number {
  return Number.isFinite(value) && value > 0 ? value : 0
}

const REASON_LABELS: Record<string, string> = {
  success: '正常完成',
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

const RETRY_LABELS: Record<string, string> = {
  none: '不重试',
  next_key: '换 Key 重试',
  next_url: '换 URL 重试',
  next_channel: '换渠道重试',
  refresh_token: '刷新令牌后重试',
  compatibility: '兼容性重试',
}

const COOLDOWN_LABELS: Record<string, string> = {
  none: '未冷却',
  key: 'Key 冷却',
  model: '模型冷却',
  channel: '渠道冷却',
}

const INTERNAL_FINAL_STATUS_LABELS: Record<number, string> = {
  499: '客户端取消',
  597: '流错误',
  598: '首字超时',
  599: '流不完整',
}

export function reasonLabel(code?: string): string {
  if (!code) return '未记录原因'
  return REASON_LABELS[code] || `其他原因（${code}）`
}

export function retryLabel(code?: string): string {
  if (!code) return '未记录重试策略'
  return RETRY_LABELS[code] || `其他重试策略（${code}）`
}

export function cooldownLabel(code?: string): string {
  if (!code) return '未记录冷却策略'
  return COOLDOWN_LABELS[code] || `其他冷却策略（${code}）`
}

export function finalStatusLabel(status: number): string {
  const internal = INTERNAL_FINAL_STATUS_LABELS[status]
  if (internal) return `状态 ${internal}`
  return Number.isFinite(status) && status > 0 ? `状态 HTTP ${status}` : '状态未知'
}

export function finalStatusHint(status: number): string {
  const internal = INTERNAL_FINAL_STATUS_LABELS[status]
  if (internal) return `内部状态码 ${status}：${internal}；客户端可能已收到 HTTP 200 或 WebSocket 事件`
  return Number.isFinite(status) && status > 0 ? `客户端 HTTP 状态 ${status}` : '未记录最终状态'
}

export function attemptCommitmentLabel(attempt: Pick<RequestAttempt, 'outcome' | 'response_committed'>): string {
  if (!attempt.response_committed) return '响应未提交'
  return attempt.outcome === 'success' ? '响应已提交' : '响应已开始，未重放'
}

export function attemptChannelLabel(attempt: RequestAttempt): string {
  const name = attempt.channel_name?.trim()
  if (name) return name
  if (typeof attempt.channel_id === 'number' && attempt.channel_id > 0) return `渠道 #${attempt.channel_id}`
  return '渠道信息不可见'
}

export function attemptCredentialLabel(attempt: RequestAttempt): string {
  const labels: string[] = []
  if (typeof attempt.key_index === 'number') {
    labels.push(attempt.key_index < 0 ? 'OAuth 凭证' : `Key ${attempt.key_index}`)
  }
  if (typeof attempt.url_index === 'number') labels.push(`URL ${attempt.url_index}`)
  return labels.length ? labels.join(' · ') : '凭证与 URL 序号不可见'
}

export function attemptModelLabel(attempt: RequestAttempt): string {
  const actualModel = attempt.actual_model?.trim()
  if (actualModel) return actualModel
  return attempt.upstream === false ? '未选择上游模型' : '上游模型不可见'
}

export function isPendingRequestTraceError(reason: unknown): boolean {
  const message = reason instanceof Error ? reason.message : String(reason ?? '')
  return /(?:^|\D)404(?:\D|$)/.test(message) || /not[_ ]?found/i.test(message)
}

export function requestRate(numerator: number, denominator: number): number | null {
  if (!Number.isFinite(numerator) || !Number.isFinite(denominator) || denominator <= 0) return null
  return numerator / denominator
}

export function clientSuccessRate(stats: RequestTraceStats): number | null {
  return requestRate(stats.successes, stats.successes + stats.errors)
}

export function upstreamSuccessRate(stats: RequestTraceStats): number | null {
  return requestRate(stats.upstream_successes, stats.upstream_attempts)
}

export function formatRate(rate: number | null): string {
  if (rate === null) return '—'
  return `${(Math.round(rate * 1000) / 10).toFixed(1)}%`
}

export function buildRequestTimeline(trace: RequestTrace): RequestTimelineEntry[] {
  const attempts = [...(trace.attempts ?? [])].sort((left, right) => {
    if (left.number !== right.number) return left.number - right.number
    return left.started_at - right.started_at
  })
  let previousEnd = 0
  return attempts.map((attempt, index) => {
    const offset = Math.max(0, (finiteNonNegative(attempt.started_at) - finiteNonNegative(trace.started_at)) / 1000)
    const gap = index === 0 ? null : Math.max(0, offset - previousEnd)
    const end = offset + finiteNonNegative(attempt.duration)
    previousEnd = Math.max(previousEnd, end)
    return {
      attempt,
      offset_seconds: offset,
      end_offset_seconds: end,
      gap_seconds: gap,
    }
  })
}

export function requestDetailHref(requestId: string): string {
  return `#/logs?${new URLSearchParams({ view: 'requests', request: requestId }).toString()}`
}

export function compactRequestId(requestId: string): string {
  return requestId.length <= 20 ? requestId : `${requestId.slice(0, 12)}…${requestId.slice(-6)}`
}

export function requestCacheHistoryDiagnostic(
  trace: Pick<RequestTrace, 'cache_status' | 'cache_reason'> & { upstream_attempt_count?: number },
): RequestCacheHistoryDiagnostic | null {
  if (!trace.cache_status) return null
  const status = isRequestCacheStatus(trace.cache_status) ? trace.cache_status : 'unknown'
  const reasonLabel = trace.cache_reason
    ? cacheReasonLabel(trace.cache_reason)
    : '未记录缓存原因'
  let explanation: string
  if (status === 'hit') {
    if (trace.upstream_attempt_count === 0) {
      explanation = trace.cache_reason === 'write_failed' || trace.cache_reason === 'canceled'
        ? '找到缓存响应，但本次响应未完整写出，不记为成功请求。本次未调用上游，不新增上游用量或费用。'
        : '命中缓存，本次未调用上游，不新增上游用量或费用。空白尝试时间线是预期结果。'
    } else if (typeof trace.upstream_attempt_count === 'number' && Number.isSafeInteger(trace.upstream_attempt_count) && trace.upstream_attempt_count > 0) {
      explanation = `缓存状态标记为命中，但请求摘要记录了 ${trace.upstream_attempt_count} 次上游尝试；请核对记录，不据此推断费用。`
    } else {
      explanation = '缓存状态标记为命中，但未提供可验证的上游尝试数；当前无法确认上游调用或费用，请核对记录。'
    }
  } else if (status === 'miss') {
    explanation = '本次未命中缓存，按正常代理路径继续处理；用量与费用以上游实际调用为准。'
  } else if (status === 'refresh') {
    explanation = '客户端要求强制刷新；旧缓存不会在刷新失败时继续复用。'
  } else if (status === 'bypass') {
    explanation = '本次请求绕过请求缓存；用量与费用以上游实际调用为准。'
  } else {
    explanation = '缓存状态码无法识别，当前不能确认缓存是否参与，也无法据此推断上游调用或费用。'
  }
  return {
    status,
    label: cacheStatusLabel(trace.cache_status),
    reasonLabel,
    explanation,
  }
}

function diagnosticAttempt(attempt: RequestAttempt): RequestDiagnosticAttempt {
  return {
    number: attempt.number,
    started_at: attempt.started_at,
    duration: attempt.duration,
    first_byte_time: attempt.first_byte_time,
    channel_id: attempt.channel_id,
    channel_name: attempt.channel_name,
    key_index: attempt.key_index,
    url_index: attempt.url_index,
    endpoint: attempt.endpoint,
    actual_model: attempt.actual_model,
    upstream_protocol: attempt.upstream_protocol,
    transport: attempt.transport,
    status_code: attempt.status_code,
    outcome: attempt.outcome,
    upstream: attempt.upstream,
    response_committed: attempt.response_committed,
    reason: attempt.reason,
    retry: attempt.retry,
    cooldown: attempt.cooldown,
    cooldown_until: attempt.cooldown_until,
  }
}

export function buildRequestDiagnostic(trace: RequestTrace): RequestDiagnostic {
  const diagnostic: RequestDiagnostic = {
    request_id: trace.request_id,
    started_at: trace.started_at,
    auth_token_id: trace.auth_token_id,
    model: trace.model,
    client_protocol: trace.client_protocol,
    transport: trace.transport,
    is_streaming: trace.is_streaming,
    final_status: trace.final_status,
    outcome: trace.outcome,
    duration: trace.duration,
    wait_duration: trace.wait_duration,
    attempt_count: trace.attempt_count,
    upstream_attempt_count: trace.upstream_attempt_count,
    upstream_success_count: trace.upstream_success_count,
    truncated: trace.truncated,
  }
  if (trace.attempts) diagnostic.attempts = trace.attempts.map(diagnosticAttempt)
  if (isRequestCacheStatus(trace.cache_status)) diagnostic.cache_status = trace.cache_status
  if (isRequestCacheReason(trace.cache_reason)) diagnostic.cache_reason = trace.cache_reason
  return diagnostic
}
