import type {
  RuntimeComponentId, RuntimeHealthComponent, RuntimeHealthReason, RuntimeHealthState,
  RuntimeMetrics, RuntimeOperationStats, RuntimeQueueStats, RuntimeReplicationStats,
  RuntimeCleanupStats, RuntimeStorageStats, RuntimeStartupRecovery, RuntimeFreshness,
} from '../types.ts'
import { parseRequestCacheObservation } from './requestCache.ts'

const componentIds: RuntimeComponentId[] = ['billing', 'request_history', 'logs', 'token_stats', 'log_replication', 'trace_replication', 'cache_sync', 'log_cleanup', 'debug_cleanup', 'startup_recovery', 'websocket']
const statePriority: Record<RuntimeHealthState, number> = { disabled: -1, idle: 0, healthy: 1, working: 2, unknown: 3, warning: 4, error: 5, blocked: 6 }
const reasons = new Set<RuntimeHealthReason>([
  'not_enabled', 'awaiting_work', 'processing', 'write_failed', 'queue_pressure', 'recent_drop', 'ready',
  'shutting_down', 'billing_blocked', 'billing_unpersisted', 'settlement_failed', 'journal_failed',
  'billing_backlog', 'pending_settlement', 'replication_failed', 'cache_sync_failed', 'cache_needs_reconcile',
  'cleanup_failed', 'debug_disabled_cleared', 'retention_disabled', 'not_hybrid', 'storage_unknown',
  'recovery_unobserved', 'configuration_recovery_failed', 'history_recovery_failed', 'restoring',
  'no_sessions', 'websocket_pressure', 'replication_unobserved', 'unknown',
])

const operationNumbers = ['active', 'attempts', 'succeeded', 'failed', 'cancelled', 'consecutive_failures', 'last_attempt_at', 'last_finished_at', 'last_success_at', 'last_failure_at', 'recovered_at'] as const
const queueNumbers = ['queued', 'capacity', 'buffered', 'dropped', 'write_failed', 'written', 'last_dropped_at'] as const
const replicationNumbers = ['queued', 'capacity', 'dropped', 'failed', 'last_dropped_at'] as const
const billingNumbers = ['pending', 'unpersisted', 'journal_failures', 'store_failures', 'invalid_cost_events', 'settled', 'deleted_token_events', 'last_success_at', 'oldest_pending_at'] as const
const cleanupNumbers = ['retention_days', 'retention_minutes', 'interval_ms', 'next_run_at', 'deleted'] as const
const websocketNumbers = ['sessions', 'active_attachments', 'downstream_connections', 'rejected_downstream_connections', 'max_downstream_connections', 'max_downstream_connections_per_token', 'upstream_connections', 'upstream_handshakes', 'upstream_reuses', 'upstream_heartbeat_failures', 'upstream_queued_read_bytes', 'oldest_upstream_connection_seconds', 'reconnects', 'transcript_bytes', 'max_transcript_bytes', 'max_sessions', 'ttl_expired', 'capacity_rejected', 'budget_rejected', 'previous_response_misses'] as const

function incomplete(): never {
  throw new Error('运行健康指标不完整，请重试并检查前后端版本')
}

function record(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return incomplete()
  return value as Record<string, unknown>
}

function count(value: unknown): number {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0) return incomplete()
  return value
}

function timestamp(value: unknown): number {
  const result = count(value)
  if (result > 8_640_000_000_000_000) return incomplete()
  return result
}

function numbers<K extends string>(source: Record<string, unknown>, keys: readonly K[]): Record<K, number> {
  return Object.fromEntries(keys.map((key) => [key, key.endsWith('_at') ? timestamp(source[key]) : count(source[key])])) as Record<K, number>
}

function flag(source: Record<string, unknown>, key: string): boolean {
  if (typeof source[key] !== 'boolean') return incomplete()
  return source[key] as boolean
}

export function runtimeState(value: unknown): RuntimeHealthState {
  return typeof value === 'string' && Object.hasOwn(statePriority, value) ? value as RuntimeHealthState : 'unknown'
}

function operation(value: unknown): RuntimeOperationStats {
  const source = record(value)
  if (typeof source.last_result !== 'string' || typeof source.last_error_code !== 'string') return incomplete()
  const lastResult = ['', 'success', 'error', 'cancelled', 'unknown'].includes(source.last_result)
    ? source.last_result as RuntimeOperationStats['last_result'] : 'unknown'
  const lastErrorCode = ['', 'timeout', 'operation_failed'].includes(source.last_error_code)
    ? source.last_error_code as RuntimeOperationStats['last_error_code'] : 'operation_failed'
  return { ...numbers(source, operationNumbers), failing: flag(source, 'failing'), last_result: lastResult, last_error_code: lastErrorCode }
}

function queue(value: unknown): RuntimeQueueStats {
  const source = record(value)
  return { ...numbers(source, queueNumbers), enabled: flag(source, 'enabled'), operation: operation(source.operation) }
}

function replication(value: unknown): RuntimeReplicationStats {
  const source = record(value)
  return { ...numbers(source, replicationNumbers), enabled: flag(source, 'enabled'), operation: operation(source.operation) }
}

function cleanup(value: unknown): RuntimeCleanupStats {
  const source = record(value)
  const mode = ['unknown', 'retention', 'clearing', 'disabled'].includes(String(source.mode))
    ? source.mode as RuntimeCleanupStats['mode'] : 'unknown'
  return { ...numbers(source, cleanupNumbers), enabled: flag(source, 'enabled'), mode, operation: operation(source.operation) }
}

function recovery(value: unknown): RuntimeStartupRecovery | null {
  if (value === null) return null
  const source = record(value)
  if (typeof source.log_days !== 'number' || !Number.isSafeInteger(source.log_days) || source.log_days < -1) return incomplete()
  return {
    enabled: flag(source, 'enabled'), history_enabled: flag(source, 'history_enabled'), log_days: source.log_days,
    configurations: operation(source.configurations), logs: operation(source.logs), request_traces: operation(source.request_traces),
  }
}

function storage(value: unknown): RuntimeStorageStats {
  const source = record(value)
  const mode = ['unknown', 'sqlite', 'mysql', 'postgres', 'hybrid'].includes(String(source.mode)) ? source.mode as RuntimeStorageStats['mode'] : 'unknown'
  const primary = ['unknown', 'sqlite', 'mysql', 'postgres'].includes(String(source.primary)) ? source.primary as RuntimeStorageStats['primary'] : 'unknown'
  return {
    mode, primary, cache_may_be_stale: flag(source, 'cache_may_be_stale'), cache_sync: operation(source.cache_sync),
    log_replication: replication(source.log_replication), startup_recovery: recovery(source.startup_recovery),
  }
}

/** Explicit projection is also the diagnostic allowlist. Missing values must
 * stay unavailable; neither the screen nor copied JSON fills them with zero. */
export function parseRuntimeMetrics(value: unknown): RuntimeMetrics {
  const source = record(value)
  if (!source.health) throw new Error('当前后端尚未提供新版运行健康数据，请更新后端后重试')
  const health = record(source.health)
  if (health.version !== 1) throw new Error('运行健康数据版本不匹配，请同步前后端版本')
  if (!Array.isArray(health.components)) return incomplete()
  const components = componentIds.map((id): RuntimeHealthComponent => {
    const matches = (health.components as unknown[]).filter((item) => item && typeof item === 'object' && (item as Record<string, unknown>).id === id)
    if (matches.length !== 1) return incomplete()
    const item = record(matches[0])
    const reason = reasons.has(item.reason as RuntimeHealthReason) ? item.reason as RuntimeHealthReason : 'unknown'
    return { id, reason, state: reason === 'unknown' ? 'unknown' : runtimeState(item.state) }
  })
  let state = runtimeState(health.state)
  for (const component of components) {
    if (statePriority[component.state] > statePriority[state]) state = component.state
  }
  const billing = record(source.billing)
  const cleaning = record(source.cleanup)
  const traces = record(source.request_traces)
  const observedAt = timestamp(source.observed_at)
  if (!observedAt) return incomplete()
  return {
    started_at: timestamp(source.started_at), observed_at: observedAt, health: { version: 1, state, components },
    billing: {
      ...numbers(billing, billingNumbers), enabled: flag(billing, 'enabled'), blocked: flag(billing, 'blocked'), closing: flag(billing, 'closing'),
      journal: operation(billing.journal), settlement: operation(billing.settlement),
    },
    request_traces: { ...queue(traces), ...(traces.replication == null ? {} : { replication: replication(traces.replication) }) },
    logs: queue(source.logs), token_failure_stats: queue(source.token_failure_stats),
    cleanup: { logs: cleanup(cleaning.logs), debug_logs: cleanup(cleaning.debug_logs) },
    storage: storage(source.storage), responses_websocket: numbers(record(source.responses_websocket), websocketNumbers),
    ...(source.request_cache === undefined ? {} : {
      request_cache: source.request_cache === null ? null : parseRequestCacheObservation(source.request_cache),
    }),
  }
}

export function runtimeComponent(snapshot: RuntimeMetrics, id: RuntimeComponentId): RuntimeHealthComponent {
  return snapshot.health.components.find((item) => item.id === id) ?? { id, state: 'unknown', reason: 'unknown' }
}

export function runtimeFreshness(receivedAt: number, failed: boolean, paused: boolean, now: number): RuntimeFreshness {
  if (!receivedAt) return 'unavailable'
  if (failed || now < receivedAt || now - receivedAt > 30_000) return 'stale'
  return paused ? 'paused' : 'current'
}

export function runtimeDisplayState(snapshot: RuntimeMetrics | null, freshness: RuntimeFreshness): RuntimeHealthState {
  return !snapshot || freshness === 'stale' || freshness === 'unavailable' ? 'unknown' : runtimeState(snapshot.health.state)
}

export function runtimeDisplaySnapshot(snapshot: RuntimeMetrics | null, freshness: RuntimeFreshness): RuntimeMetrics | null {
  if (!snapshot || (freshness !== 'stale' && freshness !== 'unavailable')) return snapshot
  return {
    ...snapshot,
    health: { ...snapshot.health, state: 'unknown', components: snapshot.health.components.map((item) => ({ ...item, state: 'unknown' })) },
  }
}

export function runtimeDiagnostics(snapshot: RuntimeMetrics, freshness: RuntimeFreshness, receivedAt: number) {
  return {
    kind: 'pivotflow_runtime_health', schema_version: 1, snapshot_status: freshness,
    received_at: timestamp(receivedAt), ...parseRuntimeMetrics(snapshot),
  }
}

export const runtimeStateLabels: Record<RuntimeHealthState, string> = {
  blocked: '暂停准入', error: '当前异常', warning: '需要关注', working: '正在处理',
  healthy: '正常', idle: '尚无操作', disabled: '未启用', unknown: '状态未知',
}

export const runtimeHeadlines: Record<RuntimeHealthState, string> = {
  blocked: '新的代理请求已暂停', error: '后台操作出现异常', warning: '有需要关注的运行状态',
  working: '后台任务正在处理', healthy: '暂无当前异常', idle: '等待后台工作产生记录',
  disabled: '当前功能未启用', unknown: '运行状态暂不可确认',
}

export const runtimeComponentTitles: Record<RuntimeComponentId, string> = {
  billing: '可靠结算', request_history: '请求历史写入', logs: '上游日志写入', token_stats: '失败请求统计',
  log_replication: '日志同步到主库', trace_replication: '请求历史同步到主库', cache_sync: 'SQLite 副本同步',
  log_cleanup: '日志与请求历史清理', debug_cleanup: '调试日志清理', startup_recovery: '启动恢复', websocket: 'WebSocket 资源',
}

export const runtimeReasonText: Record<RuntimeHealthReason, string> = {
  not_enabled: '当前配置未启用这项服务。', awaiting_work: '尚未观测到这项后台处理，首次操作后会显示实际结果。',
  processing: '后台正在处理已接收的记录。', write_failed: '最近写入失败，请检查服务端错误日志、磁盘空间与数据库连接。',
  queue_pressure: '等待队列已使用至少 80%，请观察处理是否跟得上写入。',
  recent_drop: '最近一分钟有任务或记录被丢弃，请检查队列压力；此前损失不会因后续成功自动补齐。',
  ready: '已观测到处理成功，当前没有这项操作的未恢复失败。', shutting_down: '进程正在关停，停止接收新的代理工作。',
  billing_blocked: '待结算文件不可用，新请求会返回 503；请优先恢复存储，并确认内存事件已保存。',
  billing_unpersisted: '仍有结算事件只在内存中，请先恢复存储，确认内存事件为 0 后再关闭进程。',
  settlement_failed: '主库结算最近失败，已保存的事件会继续重试；请检查主库及副本写入。',
  journal_failed: '待结算文件最近访问失败，请检查持久卷、磁盘空间与写入权限。',
  billing_backlog: '最老待确认结算已等待超过 30 秒，请观察积压是否持续增长。', pending_settlement: '已有持久结算事件等待确认，后台正在处理。',
  replication_failed: '主库复制最近失败，本地记录独立保存；失败批次不会自动补发。',
  cache_sync_failed: '副本同步最近失败，部分本地查询可能仍是旧值，请检查本地存储并核对副本。',
  cache_needs_reconcile: '后续同步已成功，但不能证明此前的数据差异已修复，副本仍需核对。',
  cleanup_failed: '最近清理未成功，后台会按原周期重试；请检查配置读取、数据库及磁盘状态。',
  debug_disabled_cleared: '调试日志已关闭，历史调试报文已清空。', retention_disabled: '自动清理已关闭，日志和请求历史会持续保留。',
  not_hybrid: '当前使用单一数据库，无需恢复 SQLite 副本。', storage_unknown: '后端未提供存储运行状态，暂不能确认这项后台操作。',
  recovery_unobserved: '尚未提供实际启动恢复结果，不能确认已恢复完成。', configuration_recovery_failed: '启动配置恢复失败，请检查部署与数据库。',
  history_recovery_failed: '启动配置已恢复，但部分历史恢复失败，本地历史可能不完整。', restoring: '启动恢复仍在处理，请以实际完成结果为准。',
  no_sessions: '当前没有 WebSocket 会话或连接。', websocket_pressure: '连接、会话或 transcript 已使用至少 80% 预算，新的连接或会话可能被拒绝。',
  replication_unobserved: '当前混合存储未提供这项同步状态。', unknown: '这项状态暂不可识别，请检查前后端版本或重新刷新。',
}

export function formatRuntimeTime(value: number): string {
  if (value === 0) return '尚未观测到'
  if (!Number.isSafeInteger(value) || value < 0 || value > 8_640_000_000_000_000) return '时间不可用'
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(new Date(value))
}

export function formatRuntimeNumber(value: number): string {
  return new Intl.NumberFormat('zh-CN').format(value)
}

export function formatRuntimeBytes(value: number): string {
  if (value < 1024) return `${value} B`
  const units = ['KiB', 'MiB', 'GiB']
  let size = value / 1024
  let index = 0
  while (size >= 1024 && index < units.length - 1) { size /= 1024; index += 1 }
  return `${size.toFixed(size >= 10 ? 1 : 2)} ${units[index]}`
}
