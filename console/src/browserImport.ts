import { apiMutation } from './api.ts'
import { normalizeConfigPreview, type ConfigPreview } from './configPreview.ts'
import {
  browserImportId, normalizeBrowserImportInspection,
  type BrowserImportFormat, type BrowserImportMode,
} from './browserImportTypes.ts'
export type * from './browserImportTypes.ts'

export const BROWSER_IMPORT_MAX_BYTES = 2 * 1024 * 1024
export const BROWSER_IMPORT_PAGE_SIZE = 10
export const BROWSER_IMPORT_VERIFY_TIMEOUT_MS = 90_000
const encoder = new TextEncoder()

const errorLabels: Record<string, string> = {
  browser_import_invalid_document: '文件 JSON 或账号格式无效，请核对原文件。',
  browser_import_invalid_utf8: '文件不是有效的 UTF-8 文本，请重新导出原文件。',
  browser_import_unsupported_version: '文件格式或版本不受支持，请使用轻量文件或 All API Hub 2.0／3.0／4.0 明文账号备份。',
  browser_import_too_large: '文件或编码后的请求超过 2 MiB 大小限制。',
  browser_import_entry_limit: '每份文件需要包含 1–64 条账号资料。',
  source_confirmation_required: '请先核对并确认来源与目标账号。',
  target_site_mismatch: '指定账号与文件的完整站点地址不符，请核对目标。',
  target_required: '存在同一身份或标签的账号，请明确选择目标后重新核对。',
  identity_conflict: '验证得到的用户身份与目标账号不同，请重新选择目标。',
  ambiguous_identity: '目标匹配不唯一，请先整理重复站点或账号。',
  ambiguous_source_identity: '文件中同一用户存在不同凭证，请确认保留哪份资料。',
  duplicate_identity: '这是前面条目的重复资料，请选择原条目。',
  platform_conflict: '文件平台与现有站点平台不兼容，请核对平台。',
  site_name_conflict: '站点名称已被其他地址使用，请修改新站点名称。',
  unverified_update_forbidden: '停用保存只支持新账号。更新既有凭证请先验证。',
  browser_import_pending: '同一来源还有应用中或结果待确认的预览，请先读取并核对原结果。',
  browser_import_acknowledgement_required: '请先在账号页核对实际结果，再确认移除待核对预览。',
  browser_required: '上游需要浏览器验证。请打开同来源站点完成人工操作，取得资料后再重新验证。',
  expired: '上游凭证已失效，请从已登录的浏览器重新取得资料。',
  user_id_required: '需要填写上游个人中心中的正整数用户 ID。',
  invalid_name: '名称需要为 1–191 个字符，且不能包含控制字符。',
  invalid_credential: '凭证内容无效或超过 16 KiB，请核对输入。',
  credential_required: '缺少所选类型的凭证，请核对原文件或手动修正。',
  unsupported_credential_type: '该站点不支持这种凭证类型。',
  unsupported_platform: '此条目的平台尚无适配器，请使用现有站点或账号表单。',
  invalid_source_origin: '来源需要是合法的 HTTP(S) origin。',
  invalid_base_url: 'API 基础地址无效，请核对协议、主机和完整前缀。',
  source_target_mismatch: '来源与 API 基础地址需要属于同一个 origin。',
  ambiguous_credential: '条目同时带有多种主凭证，请只保留实际使用的一种。',
  unsupported_field: '轻量条目包含不支持的字段，请重新导出。',
  unsupported_credential_field: '轻量凭证中包含不支持的字段，请只保留认证资料。',
  credential_locked: '服务器凭证无法读取，请检查加密密钥配置。',
  provider_timeout: '上游验证超时，请稍后显式重试。',
  provider_rate_limited: '上游暂时限流，请等待后重新验证。',
  invalid_response: '未收到有效的检查或验证结果，请重新核对。',
  unsupported: '当前平台不支持所选验证，请核对接入方式。',
  preview_stale: '配置已发生变化，请重新核对并生成预览。',
  preview_expired: '预览已过期，请重新导入文件后生成预览。',
  preview_capacity: '预览容量已满，请移除不需要的预览或等待过期。',
  preview_apply_in_progress: '原预览正在应用，请重新读取同一预览的结果。',
  commit_unknown: '数据库提交结果待确认，请在账号页核对实际配置并保留原预览编号。',
  conflict: '账号验证正由其他操作占用，请稍后重新验证。',
  server_shutting_down: '服务正在停止，请恢复连接后再操作。',
  cancelled: '验证已取消。当前输入保留，可重新核对后继续。',
  invalid_request: '请求参数或所选条目无效，请重新核对。',
  request_failed: '检查或验证请求失败，请恢复连接后显式重试。',
}

function previewId(value: unknown): string {
  return typeof value === 'string' && /^[a-zA-Z0-9_-]{1,80}$/u.test(value) ? value : ''
}

export function browserImportErrorDetails(reason: unknown): { code: string; label: string; pendingPreviewId: string } {
  const raw = reason instanceof Error ? reason.message : typeof reason === 'string' ? reason : ''
  const [head, tail = ''] = raw.trim().split(': ', 2)
  const code = errorLabels[head] ? head : 'request_failed'
  return { code, label: errorLabels[code], pendingPreviewId: code === 'browser_import_pending' ? previewId(tail) : '' }
}

class BrowserImportError extends Error {
  code: string
  pendingPreviewId: string
  constructor(reason: unknown) {
    const details = browserImportErrorDetails(reason)
    super(details.label)
    this.name = 'BrowserImportError'
    this.code = details.code
    this.pendingPreviewId = details.pendingPreviewId
  }
}

function fail(code: string): never { throw new BrowserImportError(code) }

export async function decodeBrowserImportFile(file: Blob): Promise<string> {
  if (file.size > BROWSER_IMPORT_MAX_BYTES) fail('browser_import_too_large')
  const bytes = await file.arrayBuffer()
  try { return new TextDecoder('utf-8', { fatal: true }).decode(bytes) }
  catch { fail('browser_import_invalid_utf8') }
}

function sourceRows(parsed: Record<string, unknown>): { format: BrowserImportFormat; rows: Record<string, unknown>[] } {
  if (parsed.format === 'pivotflow-browser-bridge' && parsed.version === '1.0' && Array.isArray(parsed.entries)) return { format: 'pivotflow-browser-bridge', rows: parsed.entries as Record<string, unknown>[] }
  if (parsed.format !== undefined || !['2.0', '3.0', '4.0'].includes(String(parsed.version))) fail('browser_import_unsupported_version')
  const accounts = parsed.accounts
  const rows = Array.isArray(accounts) ? accounts : accounts && typeof accounts === 'object' ? (accounts as Record<string, unknown>).accounts : null
  if (!Array.isArray(rows)) fail('browser_import_invalid_document')
  return { format: 'all-api-hub', rows: rows as Record<string, unknown>[] }
}

export function validateBrowserImportText(text: string): { format: BrowserImportFormat; count: number } {
  if (encoder.encode(text).length > BROWSER_IMPORT_MAX_BYTES || encoder.encode(JSON.stringify({ document: text })).length > BROWSER_IMPORT_MAX_BYTES) fail('browser_import_too_large')
  let parsed: Record<string, unknown>
  try { parsed = JSON.parse(text) } catch { fail('browser_import_invalid_document') }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) fail('browser_import_invalid_document')
  const { format, rows } = sourceRows(parsed)
  if (!rows.length || rows.length > 64) fail('browser_import_entry_limit')
  return { format, count: rows.length }
}

export interface BrowserImportPreviewInput {
  document: string
  entry_index: number
  target_account_id: number
  site_name: string
  account_label: string
  mode: BrowserImportMode
  source_confirmed: boolean
}

export async function inspectBrowserImport(document: string, signal?: AbortSignal) {
  validateBrowserImportText(document)
  try {
    const response = await apiMutation<unknown>('/admin/browser-import/inspect', { document }, 'POST', signal)
    return normalizeBrowserImportInspection(response)
  } catch (error) {
    if (signal?.aborted) throw error
    throw new BrowserImportError(error)
  }
}

export async function createBrowserImportPreview(input: BrowserImportPreviewInput, signal?: AbortSignal): Promise<ConfigPreview> {
  const { count } = validateBrowserImportText(input.document)
  if (!input.source_confirmed) fail('source_confirmation_required')
  if (!Number.isSafeInteger(input.entry_index) || input.entry_index < 0 || input.entry_index >= count || !Number.isSafeInteger(input.target_account_id) || input.target_account_id < 0 || !['verify', 'save_disabled'].includes(input.mode)) fail('invalid_request')
  // Unknown caller fields, including accidental credential overrides, are not
  // copied into the wire contract. All edits belong to the original document.
  const body = { document: input.document, entry_index: input.entry_index, target_account_id: input.target_account_id, site_name: input.site_name, account_label: input.account_label, mode: input.mode, source_confirmed: input.source_confirmed }
  if (encoder.encode(JSON.stringify(body)).length > BROWSER_IMPORT_MAX_BYTES) fail('browser_import_too_large')
  try {
    const value = normalizeConfigPreview(await apiMutation<ConfigPreview>('/admin/browser-import/preview', body, 'POST', signal))
    if (value.kind !== 'browser_import' || !value.browser_import) fail('invalid_response')
    return value
  } catch (error) {
    if (signal?.aborted || error instanceof BrowserImportError) throw error
    throw new BrowserImportError(error)
  }
}

function queryId(value: string | null): number {
  return value && /^\d+$/u.test(value) ? browserImportId(Number(value)) : 0
}

export function browserImportRouteState(search: string) {
  const query = new URLSearchParams(search)
  return { accountId: queryId(query.get('account_id')), siteId: queryId(query.get('site_id')), previewId: previewId(query.get('preview_id')) }
}

export function browserImportHref(input: { accountId?: number; siteId?: number; previewId?: string; helper?: boolean } = {}): string {
  const query = new URLSearchParams()
  const accountId = browserImportId(input.accountId), siteId = browserImportId(input.siteId), id = previewId(input.previewId)
  if (accountId) query.set('account_id', String(accountId))
  if (siteId) query.set('site_id', String(siteId))
  if (id) query.set('preview_id', id)
  if (input.helper === true) query.set('helper', '1')
  return '#/browser-import' + (query.size ? '?' + query : '')
}

export function browserImportNextLinks(preview: ConfigPreview): { label: string; href: string }[] {
  const result = preview.status === 'applied' && preview.result?.status === 'applied' ? preview.result.browser_import : undefined
  if (!result || !browserImportId(result.account_id) || !browserImportId(result.site_id)) return []
  return [{ label: '查看账号', href: `#/accounts?focus_account_id=${result.account_id}` }, { label: '核对渠道同步', href: `#/channels?sync_account_id=${result.account_id}` }, { label: '客户端接入', href: '#/connect' }]
}

export interface BrowserImportOperation { generation: number; controller: AbortController }
export function newBrowserImportOperation(generation: number): BrowserImportOperation { return { generation, controller: new AbortController() } }
export function browserImportOperationCurrent(operation: BrowserImportOperation, generation: number, active: BrowserImportOperation | null): boolean {
  return operation === active && operation.generation === generation && !operation.controller.signal.aborted
}

export interface BrowserImportCredentialDraft { primary: string; userId: string; refreshToken: string; expiresAt: string }

function sourceRow(text: string, index: number) {
  validateBrowserImportText(text)
  const parsed = JSON.parse(text) as Record<string, unknown>
  const source = sourceRows(parsed)
  const row = source.rows[index]
  if (!row || typeof row !== 'object' || Array.isArray(row)) throw new Error('来源条目不存在或无效。')
  return { parsed, row, format: source.format }
}

function fields(value: unknown): Record<string, unknown> { return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {} }
function textField(value: unknown): string { return typeof value === 'string' ? value : '' }
function numericField(value: unknown): string { return (typeof value === 'number' || typeof value === 'string') && /^\d+$/u.test(String(value)) ? String(value) : '' }

export function readBrowserImportCredential(text: string, index: number): BrowserImportCredentialDraft {
  const { row, format } = sourceRow(text, index)
  if (format === 'pivotflow-browser-bridge') {
    const credential = fields(row.credential)
    return { primary: textField(credential[String(row.credential_type)]), userId: numericField(credential.user_id), refreshToken: textField(credential.refresh_token), expiresAt: numericField(credential.expires_at) }
  }
  const info = fields(row.account_info), cookie = fields(row.cookieAuth), refresh = row.site_type === 'sub2api' ? fields(row.sub2apiAuth) : {}
  return { primary: row.authType === 'cookie' ? textField(cookie.sessionCookie) : textField(info.access_token), userId: numericField(info.id), refreshToken: textField(refresh.refreshToken), expiresAt: numericField(refresh.tokenExpiresAt) }
}

function optionalInteger(value: string, label: string): number | undefined {
  if (!value.trim()) return undefined
  const parsed = Number(value)
  if (!/^\d+$/u.test(value.trim()) || !Number.isSafeInteger(parsed) || parsed <= 0) throw new Error(`${label}需要填写正整数。`)
  return parsed
}

export function rewriteBrowserImportCredential(text: string, index: number, draft: BrowserImportCredentialDraft): string {
  const { parsed, row, format } = sourceRow(text, index)
  for (const secret of [draft.primary, draft.refreshToken]) {
    if (encoder.encode(secret).length > 16 * 1024 || /[\u0000-\u001f\u007f-\u009f]/u.test(secret)) fail('invalid_credential')
  }
  const userId = optionalInteger(draft.userId, '用户 ID'), expiresAt = optionalInteger(draft.expiresAt, '过期时间')
  if (format === 'pivotflow-browser-bridge') {
    if (!['api_key', 'access_token', 'cookie'].includes(String(row.credential_type))) fail('unsupported_credential_type')
    row.credential = { [String(row.credential_type)]: draft.primary.trim(), ...(userId ? { user_id: userId } : {}), ...(draft.refreshToken.trim() ? { refresh_token: draft.refreshToken.trim() } : {}), ...(expiresAt ? { expires_at: expiresAt } : {}) }
  } else {
    const info = { ...fields(row.account_info) }
    if (userId) info.id = userId
    else delete info.id
    if (row.authType === 'cookie') row.cookieAuth = { ...fields(row.cookieAuth), sessionCookie: draft.primary.trim() }
    else if (row.authType === 'access_token') info.access_token = draft.primary.trim()
    else fail('unsupported_credential_type')
    row.account_info = info
    if (row.site_type === 'sub2api') {
      const refresh = { ...fields(row.sub2apiAuth) }
      if (draft.refreshToken.trim()) refresh.refreshToken = draft.refreshToken.trim()
      else delete refresh.refreshToken
      if (expiresAt) refresh.tokenExpiresAt = expiresAt
      else delete refresh.tokenExpiresAt
      row.sub2apiAuth = refresh
    }
  }
  const repaired = JSON.stringify(parsed)
  validateBrowserImportText(repaired)
  return repaired
}

export function browserImportFailureState(error: unknown) {
  return error instanceof BrowserImportError ? { code: error.code, label: error.message, pendingPreviewId: error.pendingPreviewId } : browserImportErrorDetails(error)
}
