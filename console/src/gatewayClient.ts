import { buildGatewayProbeRequest, gatewayAuthHeaders, type GatewayProtocol } from './pages/clientConnection.ts'

export type GatewayFailureReason = 'authentication' | 'permission' | 'quota' | 'rate_limit' | 'model_unavailable' | 'upstream' | 'invalid_response' | 'empty_output' | 'incomplete' | 'blocked' | 'response_too_large' | 'invalid_input' | 'network' | 'timeout' | 'stopped'

export interface GatewayProbeResult {
  status: 'success' | 'failed' | 'unknown' | 'cancelled'
  reason?: GatewayFailureReason
  text?: string
  requestId?: string
  httpStatus?: number
  elapsedMs: number
}

class GatewayClientError extends Error {
  reason: GatewayFailureReason
  constructor(reason: GatewayFailureReason, message?: string) {
    super(message || gatewayFailureMessage(reason))
    this.name = 'GatewayClientError'
    this.reason = reason
  }
}

export function gatewayFailureMessage(reason?: GatewayFailureReason): string {
  switch (reason) {
    case 'authentication': return '下游令牌认证失败。检查令牌是否已停用、过期或被删除，再重新读取。'
    case 'permission': return '令牌没有此模型、协议或渠道的访问权限。请检查令牌限制与渠道配置。'
    case 'quota': return '令牌或渠道费用额度已用完。请查看限额与已使用费用。'
    case 'rate_limit': return '网关或上游正在限流。请查看本次请求记录，等待后再决定是否重新测试。'
    case 'model_unavailable': return '模型或接口不可用。请刷新所选令牌的模型列表，并检查渠道声明。'
    case 'upstream': return '网关未获得成功的上游响应。请打开本次请求记录查看各次尝试。'
    case 'incomplete': return '模型输出未完成，或 256 token 验证预算已耗尽。请查看记录，并在客户端中进一步验证。'
    case 'empty_output': return '响应中没有可验证的文本，不能确认接入成功。请查看记录及模型能力。'
    case 'blocked': return '模型阻止了本次验证输出。请查看记录或在客户端使用其他提示验证。'
    case 'response_too_large': return '验证响应超过读取上限，不能确认其完整结果。请查看请求记录。'
    case 'invalid_input': return '令牌或模型名称为空、过长或含无效字符。请重新读取并选择。'
    case 'network': return '连接中断，未确认模型结果。请求可能已经执行；请先查看请求记录再决定是否重发。'
    case 'timeout': return '60 秒等待预算已结束，未确认模型结果。请求可能已经执行；请先查看请求记录。'
    case 'stopped': return '已停止等待，未确认模型结果。请求可能已经执行；请先查看请求记录再决定是否重发。'
    default: return '网关返回的响应格式无法验证。若收到 HTML，请检查反向代理路由与登录拦截。'
  }
}

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}

function budgetSignal(signal?: AbortSignal): AbortSignal {
  const timeout = AbortSignal.timeout(60_000)
  return signal ? AbortSignal.any([signal, timeout]) : timeout
}

function safeRequestId(response: Response): string | undefined {
  const id = response.headers.get('X-PivotFlow-Request-Id') || ''
  return /^pf_req_[a-zA-Z0-9_-]{1,100}$/u.test(id) ? id : undefined
}

async function readLimitedJSON(response: Response, limit: number, signal: AbortSignal): Promise<unknown> {
  const contentType = response.headers.get('Content-Type') || ''
  if (!/\bapplication\/(?:[\w.-]+\+)?json\b/iu.test(contentType)) {
    await response.body?.cancel().catch(() => undefined)
    throw new GatewayClientError('invalid_response')
  }
  if (Number(response.headers.get('Content-Length')) > limit) {
    await response.body?.cancel().catch(() => undefined)
    throw new GatewayClientError('response_too_large')
  }
  if (!response.body) throw new GatewayClientError('invalid_response')
  const reader = response.body.getReader()
  const decoder = new TextDecoder('utf-8', { fatal: true })
  let size = 0
  let text = ''
  const stop = () => { void reader.cancel().catch(() => undefined) }
  const decode = (value?: Uint8Array, options?: TextDecodeOptions): string => {
    try {
      return value ? decoder.decode(value, options) : decoder.decode()
    } catch {
      throw new GatewayClientError('invalid_response')
    }
  }
  signal.addEventListener('abort', stop, { once: true })
  try {
    while (true) {
      signal.throwIfAborted()
      const { value, done } = await reader.read()
      signal.throwIfAborted()
      if (done) break
      size += value.byteLength
      if (size > limit) throw new GatewayClientError('response_too_large')
      text += decode(value, { stream: true })
    }
    text += decode()
    try { return JSON.parse(text) as unknown } catch { throw new GatewayClientError('invalid_response') }
  } catch (error) {
    await reader.cancel().catch(() => undefined)
    throw error
  } finally {
    signal.removeEventListener('abort', stop)
    reader.releaseLock()
  }
}

function failureReason(status: number, body?: unknown): GatewayFailureReason {
  const error = record(record(body)?.error)
  const code = typeof error?.code === 'string' ? error.code : typeof error?.type === 'string' ? error.type : ''
  if (['cost_limit_exceeded', 'insufficient_quota', 'quota_exceeded'].includes(code) || status === 402) return 'quota'
  if (status === 401) return 'authentication'
  if (status === 403) return 'permission'
  if (status === 429) return 'rate_limit'
  if (status === 404 || code === 'model_not_found') return 'model_unavailable'
  if (status >= 500 || ['server_error', 'upstream_unavailable'].includes(code)) return 'upstream'
  return 'invalid_response'
}

export async function fetchGatewayModels(token: string, protocol: GatewayProtocol, signal?: AbortSignal): Promise<string[]> {
  const deadline = budgetSignal(signal)
  const path = protocol === 'gemini' ? '/v1beta/models' : `/v1/models?protocol=${protocol}`
  let response: Response
  try {
    response = await fetch(path, {
      method: 'GET', credentials: 'omit', redirect: 'error', cache: 'no-store', signal: deadline,
      headers: { ...gatewayAuthHeaders(token, protocol), ...(protocol === 'anthropic' ? { 'anthropic-version': '2023-06-01' } : {}) },
    })
  } catch (error) {
    if (signal?.aborted) throw error
    throw new GatewayClientError(deadline.aborted ? 'timeout' : 'network')
  }
  let body: unknown
  try { body = await readLimitedJSON(response, 2 * 1024 * 1024, deadline) } catch (error) {
    if (!response.ok) throw new GatewayClientError(failureReason(response.status))
    throw error
  }
  if (!response.ok || record(body)?.error != null) throw new GatewayClientError(failureReason(response.status, body))
  const rows = record(body)?.[protocol === 'gemini' ? 'models' : 'data']
  if (!Array.isArray(rows) || rows.length > 20_000) throw new GatewayClientError('invalid_response', '模型列表响应格式不完整，请检查代理接口')
  const names: string[] = []
  for (const row of rows) {
    const value = record(row)?.[protocol === 'gemini' ? 'name' : 'id']
    if (typeof value !== 'string' || !value.trim() || value.length > 2048 || /[\u0000-\u001f\u007f]/u.test(value) || (protocol === 'gemini' && !value.startsWith('models/'))) {
      throw new GatewayClientError('invalid_response', '模型列表含无效条目，请检查代理接口')
    }
    const name = protocol === 'gemini' ? value.slice('models/'.length) : value
    if (!name.trim()) throw new GatewayClientError('invalid_response', '模型列表含空名称')
    names.push(name)
  }
  return [...new Set(names)].sort((left, right) => left.localeCompare(right))
}

function terminalText(body: unknown, protocol: GatewayProtocol): string {
  const data = record(body)
  if (!data) throw new GatewayClientError('invalid_response')
  if (data.error != null) throw new GatewayClientError(failureReason(200, body))
  let text = ''
  switch (protocol) {
    case 'codex': {
      if (data.status !== 'completed') throw new GatewayClientError(data.status === 'failed' ? 'upstream' : 'incomplete')
      if (!Array.isArray(data.output)) throw new GatewayClientError('invalid_response')
      for (const value of data.output) {
        const item = record(value)
        if (item?.type !== 'message' || (item.role !== undefined && item.role !== 'assistant') || !Array.isArray(item.content)) continue
        for (const value of item.content) {
          const part = record(value)
          if (part?.type === 'output_text' && typeof part.text === 'string') text += part.text
        }
      }
      break
    }
    case 'openai': {
      const choice = record(Array.isArray(data.choices) ? data.choices[0] : null)
      if (!choice) throw new GatewayClientError('invalid_response')
      if (choice.finish_reason === 'content_filter') throw new GatewayClientError('blocked')
      if (choice.finish_reason !== 'stop') throw new GatewayClientError('incomplete')
      const content = record(choice.message)?.content
      if (typeof content === 'string') text = content
      break
    }
    case 'anthropic':
      if (data.stop_reason !== 'end_turn' && data.stop_reason !== 'stop_sequence') throw new GatewayClientError('incomplete')
      if (!Array.isArray(data.content)) throw new GatewayClientError('invalid_response')
      text = data.content.map((value) => record(value)).filter((part) => part?.type === 'text' && typeof part.text === 'string').map((part) => part!.text).join('')
      break
    case 'gemini': {
      if (record(data.promptFeedback)?.blockReason) throw new GatewayClientError('blocked')
      const candidate = record(Array.isArray(data.candidates) ? data.candidates[0] : null)
      if (!candidate) throw new GatewayClientError('invalid_response')
      if (['SAFETY', 'RECITATION', 'BLOCKLIST', 'PROHIBITED_CONTENT'].includes(String(candidate.finishReason))) throw new GatewayClientError('blocked')
      if (candidate.finishReason !== 'STOP') throw new GatewayClientError('incomplete')
      const parts = record(candidate.content)?.parts
      if (!Array.isArray(parts)) throw new GatewayClientError('invalid_response')
      text = parts.map((value) => record(value)).filter((part) => !part?.thought && typeof part?.text === 'string').map((part) => part!.text).join('')
      break
    }
  }
  if (!text.trim()) throw new GatewayClientError('empty_output')
  return text
}

export async function probeGateway(token: string, protocol: GatewayProtocol, model: string, signal?: AbortSignal): Promise<GatewayProbeResult> {
  const started = performance.now()
  const elapsed = () => Math.max(0, Math.round(performance.now() - started))
  if (signal?.aborted) return { status: 'cancelled', reason: 'stopped', elapsedMs: elapsed() }
  let request: ReturnType<typeof buildGatewayProbeRequest>
  let headers: Record<string, string>
  try {
    request = buildGatewayProbeRequest(protocol, model)
    headers = { ...gatewayAuthHeaders(token, protocol), ...request.headers, 'Content-Type': 'application/json' }
  } catch {
    return { status: 'failed', reason: 'invalid_input', elapsedMs: elapsed() }
  }
  const deadline = budgetSignal(signal)
  let httpStatus: number | undefined
  let requestId: string | undefined
  try {
    const response = await fetch(request.path, { method: 'POST', credentials: 'omit', redirect: 'error', cache: 'no-store', signal: deadline, headers, body: JSON.stringify(request.body) })
    httpStatus = response.status
    requestId = safeRequestId(response)
    let body: unknown
    try { body = await readLimitedJSON(response, 256 * 1024, deadline) } catch (error) {
      if (!response.ok && error instanceof GatewayClientError) throw new GatewayClientError(failureReason(response.status))
      throw error
    }
    if (!response.ok) throw new GatewayClientError(failureReason(response.status, body))
    const text = terminalText(body, protocol).replaceAll(token, '[已隐藏令牌]').replace(/[\u0000-\u001f\u007f]/gu, ' ').slice(0, 400)
    return { status: 'success', text, requestId, httpStatus, elapsedMs: elapsed() }
  } catch (error) {
    if (error instanceof GatewayClientError) return { status: 'failed', reason: error.reason, requestId, httpStatus, elapsedMs: elapsed() }
    const name = error instanceof Error ? error.name : ''
    const reason = name === 'TimeoutError' || (deadline.aborted && !signal?.aborted) ? 'timeout' : name === 'AbortError' || signal?.aborted ? 'stopped' : 'network'
    return { status: 'unknown', reason, requestId, httpStatus, elapsedMs: elapsed() }
  }
}
