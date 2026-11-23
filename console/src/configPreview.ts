import { apiMutation, apiRequest } from './api.ts'
import type { BackupDocument, BackupImportResult } from './types.ts'
import { normalizeBrowserImportContext, normalizeBrowserImportResult, type BrowserImportApplyResult, type BrowserImportPreviewContext } from './browserImportTypes.ts'

export type ConfigPreviewKind = 'backup' | 'site_sync' | 'browser_import'
export type ConfigPreviewStatus = 'ready' | 'applying' | 'applied' | 'failed' | 'stale' | 'unknown'
export type ConfigPreviewResultStatus = Exclude<ConfigPreviewStatus, 'ready'>
export type ConfigPreviewAction = 'create' | 'update' | 'unchanged' | 'preserve' | 'conflict' | 'deactivate'

export interface ConfigPreviewItem {
  key: string
  entity: string
  label: string
  action: ConfigPreviewAction
  fields: string[]
  preserved_fields: string[]
  message: string
}

export interface ConfigPreviewSummary {
  create: number
  update: number
  unchanged: number
  preserve: number
  conflict: number
  deactivate: number
}

export interface ConfigPreviewApplyResult {
  preview_id: string
  status: ConfigPreviewResultStatus
  items: ConfigPreviewItem[]
  backup?: BackupImportResult
  browser_import?: BrowserImportApplyResult
  warning?: string
}

export interface ConfigPreview {
  id: string
  kind: ConfigPreviewKind
  expires_at: number | string
  can_apply: boolean
  items: ConfigPreviewItem[]
  summary: ConfigPreviewSummary
  status: ConfigPreviewStatus
  error?: string
  result?: ConfigPreviewApplyResult
  browser_import?: BrowserImportPreviewContext
}

export interface SiteSyncPreviewTask {
  task_id: string
  merged?: boolean
}

export const MAX_CONFIG_PREVIEW_ACCOUNTS = 16
export const CONFIG_PREVIEW_READ_TIMEOUT_MS = 8_000

export const previewActions: ConfigPreviewAction[] = ['create', 'update', 'unchanged', 'preserve', 'conflict', 'deactivate']

export const previewActionLabels: Record<ConfigPreviewAction, string> = {
  create: '新建',
  update: '更新',
  unchanged: '无变化',
  preserve: '保留',
  conflict: '冲突',
  deactivate: '停用',
}

export const previewStatusLabels: Record<ConfigPreviewStatus, string> = {
  ready: '待确认',
  applying: '正在应用',
  applied: '已应用',
  failed: '失败',
  stale: '已失效',
  unknown: '结果待确认',
}

export function previewActionLabel(action: ConfigPreviewAction): string {
  return previewActionLabels[action] || '其他动作'
}

export function configPreviewStatusLabel(status: ConfigPreviewStatus): string {
  return previewStatusLabels[status] || '未知状态'
}

export const configPreviewFieldLabels: Record<string, string> = {
  id: '标识',
  omitted_excludes: '导入排除的凭证',
  name: '名称',
  label: '账号标签',
  base_url: '站点地址',
  platform: '站点平台',
  timezone: '时区',
  tags_json: '站点标签',
  enabled: '启用状态',
  use_system_proxy: '系统代理',
  proxy_url: '代理地址',
  external_checkin_url: '外部签到地址',
  site_id: '所属站点',
  site_account_id: '所属账号',
  channel_id: '关联渠道',
  projection_key: '绑定标识',
  status: '绑定可用状态',
  credential: '凭证类型',
  credential_type: '凭证类型',
  credentials: '凭证',
  auto_checkin: '自动签到',
  auto_refresh: '自动刷新',
  auth_type: '认证类型',
  urls: '上游地址',
  url: '上游地址',
  api_key: '上游 API Key',
  upstream: '上游一致性',
  pricing_group: '定价分组',
  key_metadata: 'Key 元数据',
  ownership: '管理归属',
  priority: '优先级',
  rpm_limit: 'RPM 限制',
  max_concurrency: '最大并发',
  websockets: 'WebSocket',
  protocol_transform_mode: '协议转换模式',
  scheduled_check_enabled: '定时检测',
  scheduled_check_model: '定时检测模型',
  available_time_start: '可用开始时间',
  available_time_end: '可用结束时间',
  daily_cost_limit: '每日费用限额',
  cost_multiplier: '费用倍率',
  retry_other_keys_on_failure: 'Key 失败重试',
  custom_request_rules: '自定义请求规则',
  cooldown_detection_rules: '冷却检测规则',
  oauth_credential: 'OAuth 凭证',
  api_keys: 'API Key',
  key_strategy: 'Key 使用策略',
  model_entries: '渠道模型',
  models: '模型清单',
  is_active: '启用状态',
  expires_at: '过期时间',
  allowed_models: '允许模型',
  allowed_channel_ids: '允许渠道',
  channel_restriction_mode: '渠道限制模式',
  cost_limit_usd: '费用限额',
  allowed_protocols: '允许协议',
  description: '说明',
  value: '设置值（已隐藏）',
  webhook_enabled: 'Webhook 启用状态',
  webhook_url: 'Webhook 地址',
  telegram_enabled: 'Telegram 启用状态',
  telegram_bot_token: 'Telegram 机器人令牌',
  telegram_chat_id: 'Telegram 会话 ID',
  telegram_use_system_proxy: 'Telegram 系统代理',
  low_balance_enabled: '低余额通知',
  low_balance_threshold: '低余额阈值',
  checkin_failure_enabled: '签到失败通知',
  cooldown_minutes: '防重复通知间隔',
  file_url: '备份文件地址',
  username: '用户名',
  password: '密码',
  export_type: '备份范围',
  auto_sync_enabled: '自动备份',
  auto_sync_interval_hours: '自动备份间隔',
  runtime_state: '运行时状态',
  historical_statistics: '历史统计',
  request_history: '请求历史',
  statistics: '运行统计',
  balance_observations: '余额观测记录',
  checkin_history: '签到历史',
  usage_statistics: '用量统计',
  existing_token: '已有令牌',
  used_cost: '令牌已用额度',
  last_used_at: '最后使用时间',
  legacy_protocol_restrictions: '旧版协议限制',
  current_setting: '当前设置值',
  notification_history: '通知历史',
  backup_sync_history: '备份同步历史',
  binding: '渠道绑定',
  channels: '已有渠道',
}

export const configPreviewEntityLabels: Record<string, string> = {
  site: '站点',
  account: '账号',
  models: '模型',
  channel: '渠道',
  binding: '渠道绑定',
  token: '令牌',
  setting: '设置',
  notification: '通知设置',
  backup_target: '备份目标',
}

export const configPreviewMessageLabels: Record<string, string> = {
  invalid_entry: '条目格式无效',
  invalid_site: '站点配置无效',
  duplicate_identity: '身份重复',
  ambiguous_identity: '记录匹配不唯一',
  site_name_conflict: '站点名称冲突',
  missing_reference: '引用的对象不存在',
  unsupported_credential_type: '不支持的凭证类型',
  credential_required: '缺少登录凭证',
  credential_locked: '凭证无法读取，请检查加密密钥',
  invalid_model_reference: '模型引用无效',
  invalid_channel: '渠道配置无效',
  invalid_binding_reference: '绑定引用的账号或渠道不存在',
  invalid_binding: '绑定标识或定价分组无效',
  binding_channel_conflict: '该渠道已关联其他绑定，请先核对账号归属和定价来源',
  binding_manual_restore: '按目标账号与渠道重映射，并恢复为手动接管；不会自动启动同步',
  auth_type_conflict: '认证类型冲突',
  unrecoverable_token: '历史令牌不可恢复，已保留现有记录',
  invalid_token: '令牌配置无效',
  missing_channel_reference: '引用的渠道不存在',
  managed_or_unknown_setting: '设置由系统管理或不存在，已保留当前值',
  invalid_setting: '设置值无效',
  invalid_notification: '通知配置无效',
  invalid_backup_target: '备份目标配置无效',
  account_disables_channel: '账号停用或失效，将停用关联渠道',
  preview_capacity: '预览容量已满，请稍后重试',
  snapshot_failed: '无法读取当前配置快照',
  apply_failed: '配置应用失败',
  commit_unknown: '提交结果待确认，请人工核对当前配置',
  manual_ownership: '手动管理的渠道保持不变',
  discovery_incomplete: '模型发现未完成，已保留现有渠道且不会推断删除',
  projection_create: '创建站点同步渠道',
  projection_recreate: '重新创建缺失的站点同步渠道',
  projection_update: '更新站点同步渠道',
  projection_external_change: '渠道在绑定后发生外部变化，请重新核对',
  upstream_unchanged: '渠道与上游一致',
  removed_manual_preserved: '上游已移除该 Key；手动管理的渠道保持不变',
  removed_already_inactive: '上游 Key 已移除，渠道已经停用',
  removed_deactivate_history: '上游 Key 已移除；停用渠道并保留历史记录',
  browser_import_create_disabled: '新账号以停用状态保存，自动维护关闭；请在账号页核对后启用',
  browser_import_update: '更新所选账号凭证，当前状态待重新观测；保留历史与渠道配置',
  browser_import_unchanged: '主凭证与既有账号一致，保留原标签、开关和历史',
  target_required: '已有身份或账号标签匹配，请明确选择目标后重新核对',
  platform_conflict: '文件平台与现有站点不兼容',
}

export const configPreviewWarningLabels: Record<string, string> = {
  cache_sync_degraded: '配置已提交，但缓存副本同步失败，请核对副本状态',
  token_reload_failed: '配置已提交，但访问令牌权限重载失败；当前权限可能尚未生效，请核对后重新加载或重启服务',
  cache_sync_degraded_token_reload_failed: '配置已提交，但缓存副本同步和访问令牌权限重载均失败；当前权限可能尚未生效，请核对后重新加载或重启服务',
}

export function configPreviewFieldLabel(field: string): string {
  return configPreviewFieldLabels[field.trim().toLocaleLowerCase()] || '其他字段'
}

export function configPreviewEntityLabel(entity: string): string {
  return configPreviewEntityLabels[entity.trim().toLocaleLowerCase()] || '其他对象'
}

export function configPreviewMessageLabel(message: string): string {
  const normalized = message.trim().toLocaleLowerCase()
  if (!normalized) return ''
  const code = normalized.split(':', 1)[0].trim()
  return configPreviewMessageLabels[code] || '需要核对此条目'
}

export function configPreviewWarningLabel(warning: string): string {
  if (!warning.trim()) return ''
  return configPreviewWarningLabels[warning.trim().toLocaleLowerCase()] || '配置已提交，但存在需要核对的缓存状态'
}

export function previewExpiresAt(value: number | string | null | undefined): number | undefined {
  if (typeof value === 'number') {
    if (!Number.isFinite(value) || value <= 0) return undefined
    return value < 10_000_000_000 ? value * 1_000 : value
  }
  if (typeof value === 'string' && value.trim()) {
    const numeric = Number(value)
    if (Number.isFinite(numeric)) return previewExpiresAt(numeric)
    const parsed = Date.parse(value)
    return Number.isFinite(parsed) && parsed > 0 ? parsed : undefined
  }
  return undefined
}

export function toggleSitePreviewAccount(
  selected: ReadonlySet<number>,
  accountId: number,
  limit = MAX_CONFIG_PREVIEW_ACCOUNTS,
): { selected: Set<number>; limited: boolean } {
  const next = new Set(selected)
  if (next.has(accountId)) {
    next.delete(accountId)
    return { selected: next, limited: false }
  }
  if (next.size >= Math.max(1, limit)) return { selected: next, limited: true }
  next.add(accountId)
  return { selected: next, limited: false }
}

export function addSitePreviewAccounts(
  selected: ReadonlySet<number>,
  available: Iterable<number>,
  limit = MAX_CONFIG_PREVIEW_ACCOUNTS,
): { selected: Set<number>; limited: boolean } {
  const next = new Set(selected)
  let limited = false
  for (const accountId of available) {
    if (next.has(accountId)) continue
    if (next.size >= Math.max(1, limit)) {
      limited = true
      break
    }
    next.add(accountId)
  }
  return { selected: next, limited }
}

function normalizePreviewItems(items: ConfigPreviewItem[] | null | undefined): ConfigPreviewItem[] {
  if (!Array.isArray(items)) return []
  return items
    .filter((item): item is ConfigPreviewItem => Boolean(item && typeof item === 'object'))
    .map((item) => {
      const raw = item as unknown as {
        key?: unknown
        entity?: unknown
        label?: unknown
        action?: unknown
        fields?: unknown
        preserved_fields?: unknown
        message?: unknown
      }
      const action = typeof raw.action === 'string' && previewActions.includes(raw.action as ConfigPreviewAction)
        ? raw.action as ConfigPreviewAction
        : 'conflict'
      return {
        key: typeof raw.key === 'string' ? raw.key : '',
        entity: typeof raw.entity === 'string' ? raw.entity : '',
        label: typeof raw.label === 'string' ? raw.label : '',
        action,
        fields: Array.isArray(raw.fields) ? raw.fields.filter((field): field is string => typeof field === 'string') : [],
        preserved_fields: Array.isArray(raw.preserved_fields) ? raw.preserved_fields.filter((field): field is string => typeof field === 'string') : [],
        message: typeof raw.message === 'string' ? raw.message : '',
      }
    })
}

export function summarizePreviewItems(items: ConfigPreviewItem[]): ConfigPreviewSummary {
  const summary: ConfigPreviewSummary = { create: 0, update: 0, unchanged: 0, preserve: 0, conflict: 0, deactivate: 0 }
  for (const item of items) {
    if (item.action in summary) summary[item.action] += 1
  }
  return summary
}

export function filterPreviewItems(
  items: ConfigPreviewItem[],
  search: string,
  action: ConfigPreviewAction | 'all' = 'all',
): ConfigPreviewItem[] {
  const needle = search.trim().toLocaleLowerCase()
  return items.filter((item) => {
    if (action !== 'all' && item.action !== action) return false
    if (!needle) return true
    return [
      item.entity,
      configPreviewEntityLabel(item.entity),
      item.label,
      previewActionLabel(item.action),
      item.message,
      configPreviewMessageLabel(item.message),
      ...item.fields,
      ...item.fields.map(configPreviewFieldLabel),
      ...item.preserved_fields,
      ...item.preserved_fields.map(configPreviewFieldLabel),
    ].some((value) => value.toLocaleLowerCase().includes(needle))
  })
}

export function canApplyPreview(preview: ConfigPreview, now = Date.now()): boolean {
  const expiry = previewExpiresAt(preview.expires_at)
  return preview.status === 'ready'
    && preview.can_apply
    && !preview.error
    && expiry !== undefined
    && expiry > now
    && preview.summary.conflict === 0
}

export function normalizeConfigPreviewApplyResult(result: ConfigPreviewApplyResult): ConfigPreviewApplyResult {
  const raw = result as unknown as {
    preview_id?: unknown
    status?: unknown
    items?: ConfigPreviewItem[] | null
    backup?: BackupImportResult
    browser_import?: unknown
    warning?: unknown
  }
  const statuses: ConfigPreviewResultStatus[] = ['applying', 'applied', 'failed', 'stale', 'unknown']
  const status: ConfigPreviewApplyResult['status'] = typeof raw.status === 'string' && statuses.includes(raw.status as ConfigPreviewResultStatus)
    ? raw.status as ConfigPreviewResultStatus
    : 'failed'
  return {
    preview_id: typeof raw.preview_id === 'string' ? raw.preview_id : '',
    status,
    items: normalizePreviewItems(raw.items),
    ...(raw.backup ? { backup: raw.backup } : {}),
    ...(normalizeBrowserImportResult(raw.browser_import) ? { browser_import: normalizeBrowserImportResult(raw.browser_import) } : {}),
    ...(typeof raw.warning === 'string' && raw.warning.trim() ? { warning: raw.warning.trim() } : {}),
  }
}

export function normalizeConfigPreview(preview: ConfigPreview): ConfigPreview {
  const raw = preview as unknown as {
    id?: unknown
    kind?: unknown
    expires_at?: unknown
    can_apply?: unknown
    items?: ConfigPreviewItem[] | null
    status?: unknown
    error?: unknown
    result?: ConfigPreviewApplyResult | null
    browser_import?: unknown
  }
  const items = normalizePreviewItems(raw.items)
  const statuses: ConfigPreviewStatus[] = ['ready', 'applying', 'applied', 'failed', 'stale', 'unknown']
  const status = typeof raw.status === 'string' && statuses.includes(raw.status as ConfigPreviewStatus)
    ? raw.status as ConfigPreviewStatus
    : 'failed'
  const browserImport = raw.kind === 'browser_import' ? normalizeBrowserImportContext(raw.browser_import) : undefined
  return {
    id: typeof raw.id === 'string' ? raw.id : '',
    kind: raw.kind === 'site_sync' ? 'site_sync' : raw.kind === 'browser_import' ? 'browser_import' : 'backup',
    expires_at: typeof raw.expires_at === 'number' || typeof raw.expires_at === 'string' ? raw.expires_at : 0,
    can_apply: raw.can_apply === true && (raw.kind !== 'browser_import' || Boolean(browserImport)),
    items,
    summary: summarizePreviewItems(items),
    status,
    ...(typeof raw.error === 'string' && raw.error.trim() ? { error: safeConfigPreviewError(raw.error) } : {}),
    ...(raw.result ? { result: normalizeConfigPreviewApplyResult(raw.result) } : {}),
    ...(browserImport ? { browser_import: browserImport } : {}),
  }
}

export function mergePreviewApplyResult(preview: ConfigPreview, result: ConfigPreviewApplyResult): ConfigPreview {
  const normalized = normalizeConfigPreviewApplyResult(result)
  return {
    ...preview,
    status: normalized.status,
    can_apply: false,
    error: undefined,
    items: normalized.items,
    summary: summarizePreviewItems(normalized.items),
    result: normalized,
  }
}

export function markPreviewObservationUnknown(preview: ConfigPreview): ConfigPreview {
  return {
    ...preview,
    status: 'unknown',
    can_apply: false,
    error: undefined,
  }
}

export function syncPreviewReference(value: string | null | undefined): string {
  const prefix = 'config_preview:'
  const reference = (value || '').trim()
  return reference.startsWith(prefix) ? reference.slice(prefix.length).trim() : ''
}

export async function mapWithConcurrency<T, R>(
  values: T[],
  worker: (value: T, index: number) => Promise<R>,
  concurrency = 3,
): Promise<R[]> {
  if (!values.length) return []
  const limit = Math.max(1, Math.min(values.length, Math.floor(concurrency) || 1))
  const results = new Array<R>(values.length)
  let cursor = 0
  const run = async () => {
    while (cursor < values.length) {
      const index = cursor
      cursor += 1
      results[index] = await worker(values[index], index)
    }
  }
  await Promise.all(Array.from({ length: limit }, run))
  return results
}

export function configPreviewReadSignal(signal?: AbortSignal, timeoutMs = CONFIG_PREVIEW_READ_TIMEOUT_MS): AbortSignal {
  const timeout = AbortSignal.timeout(timeoutMs)
  return signal ? AbortSignal.any([signal, timeout]) : timeout
}

const configPreviewErrorLabels: Record<string, string> = {
  ...configPreviewMessageLabels,
  invalid_preview: '无法生成配置预览',
  invalid_backup_document: '备份文件内容无效',
  unsupported_backup_version: '备份文件版本不受支持',
  invalid_backup_type: '备份范围无效',
  webdav_not_configured: '尚未启用或配置 WebDAV，请先保存连接设置',
  credential_locked: 'WebDAV 凭证无法读取，请检查服务器加密密钥配置',
  webdav_request_failed: '无法连接 WebDAV，请检查地址、网络和凭据',
  backup_too_large: '备份文件超过大小限制，请缩小备份范围后重试',
  snapshot_failed: '无法读取当前配置快照，请稍后重试',
  webdav_http_401: 'WebDAV 认证失败，请检查用户名和密码',
  webdav_http_403: 'WebDAV 拒绝访问，请检查账号权限',
  webdav_http_404: 'WebDAV 地址或备份文件不存在，请检查路径',
  webdav_http_405: 'WebDAV 服务器不允许该请求，请检查地址是否指向文件或目录',
  webdav_http_409: 'WebDAV 路径冲突，请检查同名文件或目录',
  webdav_http_423: 'WebDAV 文件已锁定，请稍后重试或解除锁定',
  webdav_http_500: 'WebDAV 服务器内部错误，请稍后重试',
  webdav_http_502: 'WebDAV 网关错误，请检查服务地址或稍后重试',
  webdav_http_504: 'WebDAV 网关超时，请检查服务地址或稍后重试',
  webdav_http_507: 'WebDAV 存储空间不足，请清理空间后重试',
  request_failed: '配置预览请求失败，请重试',
    preview_expired: '预览已过期，请重新生成后再应用',
    config_preview_expired: '预览已过期，请重新生成后再应用',
    preview_not_found: '预览已过期或不存在，请重新生成',
    config_preview_not_found: '预览已过期或不存在，请重新生成',
    not_found: '预览已过期或不存在，请重新生成',
    preview_stale: '配置已发生变化，旧预览不能应用，请重新生成预览',
    config_preview_stale: '配置已发生变化，旧预览不能应用，请重新生成预览',
    stale_preview: '配置已发生变化，旧预览不能应用，请重新生成预览',
    configuration_changed: '配置已发生变化，旧预览不能应用，请重新生成预览',
    preview_conflict: '预览包含冲突或无效条目，请修正来源配置后重新预览',
    conflict: '预览包含冲突或无效条目，请修正来源配置后重新预览',
    preview_not_ready: '预览尚未就绪或已经处理，请重新读取状态',
    preview_apply_in_progress: '该预览正在应用，请稍后重新读取结果',
    apply_in_progress: '该预览正在应用，请稍后重新读取结果',
    observation_interrupted: '操作结果待确认，请重新读取预览状态后再决定是否操作',
    task_unknown: '无法确认同步结果，请保留任务编号并重新检查任务状态',
    site_task_queue_full: '维护任务队列已满，请稍后重新生成预览',
    server_shutting_down: '服务正在停止，请恢复连接后重新生成预览',
    cancelled: '预览生成已取消',
    expired: '登录会话或上游凭证已失效，请更新凭证后重试',
    unsupported: '当前站点不支持生成同步预览',
    models_required: '请先同步账号模型后再生成渠道预览',
    routing_api_key_unavailable: '没有读取到可用的模型 API Key，请先在上游站点创建 Key',
    invalid_request: '预览请求格式无效，请检查所选配置后重试',
    webdav_html_response: 'WebDAV 返回了网页而不是备份文件，请检查服务地址',
    browser_import_pending: '同来源有应用中或结果待确认的预览，请先核对原结果',
    browser_import_acknowledgement_required: '请先核对实际账号配置，再确认移除待核对记录',
}

export function safeConfigPreviewLocation(value: string): string {
  const location = value.trim()
  if (/^(?:sites|accounts|models|channels|bindings|tokens|settings|sync)\[(?:0|[1-9]\d{0,7})\]$/.test(location)) return location
  if (/^(?:models\/account|channel):(?:0|[1-9]\d{0,7})$/.test(location)) return location
  if (/^(?:notification|backup_target|connections|target)$/.test(location)) return location
  return ''
}

export interface ConfigPreviewErrorDetails {
  code: string
  label: string
  location?: string
}

export function configPreviewErrorDetails(reason: unknown): ConfigPreviewErrorDetails {
  const raw = reason instanceof Error ? reason.message.trim() : String(reason || '').trim()
  const separator = raw.indexOf(': ')
  const head = (separator >= 0 ? raw.slice(0, separator) : raw).trim().toLocaleLowerCase()
  const tail = separator >= 0 ? raw.slice(separator + 2).trim() : ''
  let code = ''
  let location = ''
  if (configPreviewErrorLabels[head]) {
    code = head
    location = safeConfigPreviewLocation(tail)
  } else if (configPreviewErrorLabels[tail.toLocaleLowerCase()]) {
    code = tail.toLocaleLowerCase()
    location = safeConfigPreviewLocation(head)
  }
  return {
    code,
    label: configPreviewErrorLabels[code] || '配置预览失败，请重新生成预览后重试',
    ...(location ? { location } : {}),
  }
}

function safeConfigPreviewError(raw: string): string {
  const details = configPreviewErrorDetails(raw)
  if (!details.code) return 'apply_failed'
  return details.location ? `${details.code}: ${details.location}` : details.code
}

export function configPreviewErrorMessage(reason: unknown): string {
  const details = configPreviewErrorDetails(reason)
  return details.location ? `${details.label}（位置：${details.location}）` : details.label
}

export function previewBackup(data: BackupDocument, signal?: AbortSignal): Promise<ConfigPreview> {
  return apiMutation<ConfigPreview>('/admin/backup/preview', { data }, 'POST', signal).then(normalizeConfigPreview)
}

export function previewBackupWebDAV(signal?: AbortSignal): Promise<ConfigPreview> {
  return apiMutation<ConfigPreview>('/admin/backup/webdav/preview', {}, 'POST', signal).then(normalizeConfigPreview)
}

export function getConfigPreview(id: string, signal?: AbortSignal): Promise<ConfigPreview> {
  return apiRequest<ConfigPreview>(
    `/admin/config-previews/${encodeURIComponent(id)}`,
    configPreviewReadSignal(signal),
  ).then(normalizeConfigPreview)
}

export function applyConfigPreview(id: string, signal?: AbortSignal): Promise<ConfigPreviewApplyResult> {
  return apiMutation<ConfigPreviewApplyResult>(`/admin/config-previews/${encodeURIComponent(id)}/apply`, {}, 'POST', signal)
    .then(normalizeConfigPreviewApplyResult)
}

export function discardConfigPreview(id: string, signal?: AbortSignal, acknowledged = false): Promise<{ deleted: boolean }> {
  return apiMutation<{ deleted: boolean }>(`/admin/config-previews/${encodeURIComponent(id)}${acknowledged ? '?acknowledged=true' : ''}`, undefined, 'DELETE', signal)
}

export function startSiteSyncPreview(accountId: number, signal?: AbortSignal): Promise<SiteSyncPreviewTask> {
  return apiMutation<SiteSyncPreviewTask>(`/admin/site-accounts/${accountId}/sync-preview`, {}, 'POST', signal)
}
