import type {
  RequestCacheInvalidationReason,
  RequestCacheObservation,
  RequestCacheReason,
  RequestCacheStats,
  RequestCacheStatus,
} from '../types.ts'

export const REQUEST_CACHE_STATUSES = ['hit', 'miss', 'refresh', 'bypass'] as const
export const REQUEST_CACHE_REASONS = [
  'disabled',
  'not_opted_in',
  'client_bypass',
  'invalid_mode',
  'request_no_store',
  'request_no_cache',
  'request_query',
  'request_encoding',
  'streaming',
  'unsupported_endpoint',
  'nondeterministic',
  'tools_or_state',
  'unsupported_request',
  'no_identity',
  'route_unavailable',
  'channel_changed',
  'custom_rules',
  'protocol_transform',
  'empty',
  'expired',
  'client_refresh',
  'unsafe_response',
  'response_no_store',
  'response_too_large',
  'capture_budget',
  'invalidated',
  'upstream_error',
  'canceled',
  'write_failed',
  'reused',
  'stored',
] as const
export const REQUEST_CACHE_INVALIDATION_REASONS = [
  'manual_clear',
  'routing_changed',
  'keys_changed',
  'aliases_changed',
  'permissions_changed',
] as const

const STATUS_LABELS: Record<RequestCacheStatus, string> = {
  hit: '缓存命中',
  miss: '缓存未命中',
  refresh: '强制刷新',
  bypass: '已绕过缓存',
}

const REASON_LABELS: Record<RequestCacheReason, string> = {
  disabled: '请求缓存已关闭',
  not_opted_in: '客户端未选择使用缓存',
  client_bypass: '客户端要求绕过缓存',
  invalid_mode: '客户端缓存模式无效',
  request_no_store: '请求禁止读写缓存',
  request_no_cache: '请求要求刷新缓存',
  request_query: '请求带有查询参数',
  request_encoding: '请求体编码不受支持',
  streaming: '流式请求不复用',
  unsupported_endpoint: '接口不支持缓存',
  nondeterministic: '请求参数不是确定性的',
  tools_or_state: '请求包含工具或会话状态',
  unsupported_request: '请求字段不受支持',
  no_identity: '缺少可复用身份',
  route_unavailable: '当前没有可用路由',
  channel_changed: '缓存来源渠道已变化',
  custom_rules: '自定义规则可能改变请求',
  protocol_transform: '协议转换可能改变语义',
  empty: '尚无可复用条目',
  expired: '缓存条目已过期',
  client_refresh: '客户端要求刷新',
  unsafe_response: '响应包含不可复用内容',
  response_no_store: '上游禁止保存响应',
  response_too_large: '响应超过单条容量',
  capture_budget: '捕获超出容量预算',
  invalidated: '缓存已失效',
  upstream_error: '上游请求失败',
  canceled: '请求已取消',
  write_failed: '响应写出失败',
  reused: '已复用缓存响应',
  stored: '响应已写入缓存',
}

const INVALIDATION_LABELS: Record<RequestCacheInvalidationReason, string> = {
  manual_clear: '手动清空',
  routing_changed: '路由配置发生变化',
  keys_changed: '渠道密钥发生变化',
  aliases_changed: '模型映射发生变化',
  permissions_changed: '权限发生变化',
}

const CACHE_COUNTER_KEYS = [
  'entries',
  'bytes',
  'expired_entries',
  'captures',
  'capture_bytes',
  'lookups',
  'hits',
  'misses',
  'refreshes',
  'bypasses',
  'stored',
  'expired',
  'evicted',
  'rejected',
  'clears',
] as const

const MIB = 1_048_576
const MIN_TTL_SECONDS = 5
const MAX_TTL_SECONDS = 3_600
const MIN_ENTRIES = 1
const MAX_ENTRIES = 4_096
const MIN_BYTES = MIB
const MAX_BYTES = 128 * MIB
const MIN_ENTRY_BYTES = 4_096
const MAX_ENTRY_BYTES = MIB

function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : null
}

function assertExactKeys(value: Record<string, unknown>, keys: readonly string[], label: string): void {
  const actual = Object.keys(value)
  if (actual.length !== keys.length || keys.some((key) => !Object.prototype.hasOwnProperty.call(value, key))) {
    throw new Error(`请求缓存${label}字段不完整或包含未知字段`)
  }
}

function safeInteger(value: unknown, label: string, minimum = 0): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum) {
    throw new Error(`请求缓存${label}计数或时间戳无效`)
  }
  return value as number
}

function timestampValue(value: unknown, label: string, minimum = 0): number {
  const parsed = safeInteger(value, label, minimum)
  if (parsed > 8_640_000_000_000_000) throw new Error(`请求缓存${label}时间戳超出允许范围`)
  return parsed
}

function booleanValue(value: unknown): boolean {
  if (typeof value !== 'boolean') throw new Error('请求缓存配置字段无效')
  return value
}

function boundedInteger(value: unknown, minimum: number, maximum: number, label: string): number {
  const parsed = safeInteger(value, label, minimum)
  if (parsed > maximum) throw new Error(`请求缓存${label}超出允许范围`)
  return parsed
}

function fixedCounts<K extends string>(value: unknown, keys: readonly K[], label: string): Record<K, number> {
  const source = record(value)
  if (!source) throw new Error(`请求缓存${label}原因统计无效`)
  if (Object.keys(source).some((key) => !keys.includes(key as K))) {
    throw new Error(`请求缓存${label}原因统计包含未知原因`)
  }
  // Backend reason maps are sparse counters: an absent allowed code means no
  // recorded event, while a missing map or malformed present count is invalid.
  return Object.fromEntries(keys.map((key) => [key, Object.hasOwn(source, key)
    ? safeInteger(source[key], `${label}原因计数`) : 0])) as Record<K, number>
}

export function isRequestCacheStatus(value: unknown): value is RequestCacheStatus {
  return typeof value === 'string' && REQUEST_CACHE_STATUSES.includes(value as RequestCacheStatus)
}

export function isRequestCacheReason(value: unknown): value is RequestCacheReason {
  return typeof value === 'string' && REQUEST_CACHE_REASONS.includes(value as RequestCacheReason)
}

export function isRequestCacheInvalidationReason(value: unknown): value is RequestCacheInvalidationReason {
  return typeof value === 'string' && REQUEST_CACHE_INVALIDATION_REASONS.includes(value as RequestCacheInvalidationReason)
}

export function cacheStatusLabel(code?: string): string {
  return isRequestCacheStatus(code) ? STATUS_LABELS[code] : '未知缓存状态'
}

export function cacheReasonLabel(code?: string): string {
  return isRequestCacheReason(code) ? REASON_LABELS[code] : '未知缓存原因'
}

export function cacheInvalidationReasonLabel(code?: string): string {
  return isRequestCacheInvalidationReason(code) ? INVALIDATION_LABELS[code] : '未知失效原因'
}

export function parseRequestCacheObservation(value: unknown): RequestCacheObservation {
  const source = record(value)
  if (!source) throw new Error('请求缓存数据格式无效')
  assertExactKeys(source, ['version', 'observed_at', 'process_started_at', 'config', 'stats'], '数据')
  if (source.version !== 1) throw new Error('请求缓存数据版本不匹配，请同步前后端版本')

  const observedAt = timestampValue(source.observed_at, '观察时间', 1)
  const processStartedAt = timestampValue(source.process_started_at, '进程启动时间', 1)
  if (processStartedAt > observedAt) throw new Error('请求缓存进程时间范围无效')

  const configSource = record(source.config)
  if (!configSource) throw new Error('请求缓存配置数据无效')
  assertExactKeys(configSource, ['enabled', 'ttl_seconds', 'max_entries', 'max_bytes', 'max_entry_bytes'], '配置')
  const maxBytes = boundedInteger(configSource.max_bytes, MIN_BYTES, MAX_BYTES, '总容量')
  const maxEntryBytes = boundedInteger(configSource.max_entry_bytes, MIN_ENTRY_BYTES, MAX_ENTRY_BYTES, '单条响应容量')
  const maxEntries = boundedInteger(configSource.max_entries, MIN_ENTRIES, MAX_ENTRIES, '条目容量')
  if (maxEntryBytes > maxBytes) throw new Error('请求缓存单条响应容量不能超过总容量')

  const statsSource = record(source.stats)
  if (!statsSource) throw new Error('请求缓存统计无效')
  const statsKeys = [
    ...CACHE_COUNTER_KEYS,
    'invalidations',
    'reasons',
    'last_invalidation_reason',
    'last_invalidation_at',
  ]
  assertExactKeys(statsSource, statsKeys, '统计')
  const counters = Object.fromEntries(
    CACHE_COUNTER_KEYS.map((key) => [key, safeInteger(statsSource[key], '统计')]),
  ) as Record<(typeof CACHE_COUNTER_KEYS)[number], number>
  const lastInvalidationReason = statsSource.last_invalidation_reason
  if (typeof lastInvalidationReason !== 'string'
    || (lastInvalidationReason !== '' && !isRequestCacheInvalidationReason(lastInvalidationReason))) {
    throw new Error('请求缓存失效原因无效')
  }

  const stats: RequestCacheStats = {
    ...counters,
    invalidations: fixedCounts(statsSource.invalidations, REQUEST_CACHE_INVALIDATION_REASONS, '失效'),
    reasons: fixedCounts(statsSource.reasons, REQUEST_CACHE_REASONS, '缓存'),
    last_invalidation_reason: lastInvalidationReason,
    last_invalidation_at: timestampValue(statsSource.last_invalidation_at, '失效时间'),
  }
  if (stats.entries > maxEntries || stats.bytes > maxBytes || stats.capture_bytes > maxBytes) {
    throw new Error('请求缓存观察超过当前进程的容量上限')
  }
  if (stats.expired_entries > stats.entries) {
    throw new Error('请求缓存过期条目数超过驻留条目数')
  }
  if ((stats.last_invalidation_at === 0) !== (stats.last_invalidation_reason === '')) {
    throw new Error('请求缓存失效时间与原因不一致')
  }
  if (stats.last_invalidation_at > 0
    && (stats.last_invalidation_at < processStartedAt || stats.last_invalidation_at > observedAt)) {
    throw new Error('请求缓存失效时间不在当前进程观察范围内')
  }

  return {
    version: 1,
    observed_at: observedAt,
    process_started_at: processStartedAt,
    config: {
      enabled: booleanValue(configSource.enabled),
      ttl_seconds: boundedInteger(configSource.ttl_seconds, MIN_TTL_SECONDS, MAX_TTL_SECONDS, '有效期'),
      max_entries: maxEntries,
      max_bytes: maxBytes,
      max_entry_bytes: maxEntryBytes,
    },
    stats,
  }
}

export type RequestCacheCoordinatorPhase = 'idle' | 'loading' | 'ready' | 'refreshing' | 'clearing'

export interface RequestCacheCoordinatorState {
  phase: RequestCacheCoordinatorPhase
  observation: RequestCacheObservation | null
  error: string
  notice: string
  snapshotStale: boolean
}

interface RequestCacheCoordinatorOptions {
  read: (signal: AbortSignal) => Promise<RequestCacheObservation>
  clear: (signal: AbortSignal) => Promise<RequestCacheObservation>
  confirmClear: () => boolean
  timeoutMs?: number
  onChange?: (state: RequestCacheCoordinatorState) => void
}

export interface RequestCacheCoordinator {
  getState: () => RequestCacheCoordinatorState
  subscribe: (listener: (state: RequestCacheCoordinatorState) => void) => () => void
  start: () => void
  refresh: () => void
  clear: () => void
  dispose: () => void
}

interface ActiveOperation {
  controller: AbortController
  timer: ReturnType<typeof setTimeout> | null
}

export function createRequestCacheCoordinator(options: RequestCacheCoordinatorOptions): RequestCacheCoordinator {
  let state: RequestCacheCoordinatorState = {
    phase: 'idle',
    observation: null,
    error: '',
    notice: '',
    snapshotStale: false,
  }
  let active: ActiveOperation | null = null
  let generation = 0
  let disposed = false
  const listeners = new Set<(state: RequestCacheCoordinatorState) => void>()
  const timeoutMs = Math.max(1, options.timeoutMs ?? 8_000)

  const update = (patch: Partial<RequestCacheCoordinatorState>) => {
    if (disposed) return
    state = { ...state, ...patch }
    options.onChange?.(state)
    listeners.forEach((listener) => listener(state))
  }

  const finishActive = (operation: ActiveOperation) => {
    if (operation.timer) clearTimeout(operation.timer)
    if (active === operation) active = null
  }

  const failRead = (reason: unknown, hadSnapshot: boolean) => {
    update({
      phase: 'ready',
      error: hadSnapshot
        ? `刷新失败，显示的仍是上次成功读取的快照。${reason instanceof Error && reason.message ? ` ${reason.message}` : ''}`
        : `读取请求缓存状态失败，当前状态未知。${reason instanceof Error && reason.message ? ` ${reason.message}` : ''}`,
      notice: '',
      snapshotStale: hadSnapshot,
    })
  }

  const timeoutRead = (hadSnapshot: boolean) => {
    update({
      phase: 'ready',
      error: hadSnapshot
        ? '刷新请求缓存状态超时，显示的仍是上次成功读取的快照。'
        : '读取请求缓存状态超时，当前状态未知。',
      notice: '',
      snapshotStale: hadSnapshot,
    })
  }

  const runRead = () => {
    if (disposed || active) return
    const hadSnapshot = Boolean(state.observation)
    const controller = new AbortController()
    const operation: ActiveOperation = { controller, timer: null }
    const token = ++generation
    active = operation
    update({
      phase: hadSnapshot ? 'refreshing' : 'loading',
      error: '',
      notice: '',
    })
    operation.timer = setTimeout(() => {
      if (active !== operation || generation !== token) return
      generation += 1
      active = null
      controller.abort()
      timeoutRead(hadSnapshot)
    }, timeoutMs)
    Promise.resolve()
      .then(() => options.read(controller.signal))
      .then((observation) => {
        if (disposed || active !== operation || generation !== token) return
        finishActive(operation)
        update({ phase: 'ready', observation, error: '', snapshotStale: false })
      })
      .catch((reason) => {
        if (disposed || active !== operation || generation !== token || controller.signal.aborted) return
        finishActive(operation)
        failRead(reason, hadSnapshot)
      })
  }

  const clear = () => {
    if (disposed || state.phase === 'clearing' || !options.confirmClear()) return
    const token = ++generation
    if (active) {
      const replaced = active
      active = null
      replaced.controller.abort()
      if (replaced.timer) clearTimeout(replaced.timer)
    }
    const controller = new AbortController()
    const operation: ActiveOperation = { controller, timer: null }
    active = operation
    update({
      phase: 'clearing',
      error: '',
      notice: '',
      snapshotStale: Boolean(state.observation),
    })
    const timeoutClear = () => {
      if (disposed || active !== operation || generation !== token) return
      generation += 1
      active = null
      controller.abort()
      update({
        phase: 'ready',
        error: '清理请求超时，结果未知；请刷新确认。系统未自动重试。',
        notice: '',
        snapshotStale: Boolean(state.observation),
      })
    }
    operation.timer = setTimeout(timeoutClear, timeoutMs)
    Promise.resolve()
      .then(() => options.clear(controller.signal))
      .then((observation) => {
        if (disposed || active !== operation || generation !== token) return
        finishActive(operation)
        update({
          phase: 'ready',
          observation,
          error: '',
          notice: '请求缓存已清空；进程累计计数保留。',
          snapshotStale: false,
        })
      })
      .catch(() => {
        if (disposed || active !== operation || generation !== token || controller.signal.aborted) return
        finishActive(operation)
        update({
          phase: 'ready',
          error: '清理请求失败，结果未知；请刷新确认。系统未自动重试。',
          notice: '',
          snapshotStale: Boolean(state.observation),
        })
      })
  }

  return {
    getState: () => state,
    subscribe: (listener) => {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    start: runRead,
    refresh: runRead,
    clear,
    dispose: () => {
      if (disposed) return
      disposed = true
      generation += 1
      if (active) {
        active.controller.abort()
        if (active.timer) clearTimeout(active.timer)
        active = null
      }
      listeners.clear()
    },
  }
}
