import type {
  APIResponse,
  Channel,
  ChannelEditorSnapshot,
  ChannelFilters,
  ChannelMutation,
  ChannelModelsPreview,
  ChannelTestResult,
  DashboardRange,
  DashboardSnapshot,
  LogEntry,
  LogFilters,
  LogsBootstrap,
  PaginatedResult,
  StatsFilters,
  StatsFilterOptions,
  StatsSnapshot,
  CheckinAttempt,
  Site,
  SiteAccount,
  SiteCredentialVerification,
  SiteAnnouncement,
  SiteInventory,
  SiteAccountModel,
  SiteProjectionResult,
  SiteTask,
	WebhookConfig,
  ActiveRequest,
  AuthToken,
  AuthTokenList,
  FingerprintJob,
  FingerprintTestRecord,
  ModelFingerprint,
  SystemSetting,
  BackupDocument,
  BackupImportResult,
  BackupType,
  BackupWebDAVConfig,
  VersionInfo,
} from './types'

const TOKEN_KEY = 'pivotflow_token'
const EXPIRY_KEY = 'pivotflow_token_expiry'

function loginURL(): string {
  const redirect = `${window.location.pathname}${window.location.search}${window.location.hash}`
  return `/web/auth/?redirect=${encodeURIComponent(redirect)}`
}

function clearSession(): void {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(EXPIRY_KEY)
  localStorage.removeItem('pivotflow_web_role')
}

async function requestEnvelope<T>(path: string, init: RequestInit = {}): Promise<APIResponse<T>> {
  const token = localStorage.getItem(TOKEN_KEY)
  const expiry = Number(localStorage.getItem(EXPIRY_KEY) || 0)
  if (!token || (expiry > 0 && Date.now() > expiry)) {
    clearSession()
    window.location.replace(loginURL())
    throw new Error('登录状态已失效')
  }

  const response = await fetch(path, {
    ...init,
    cache: init.cache ?? 'no-store',
    headers: {
      Authorization: `Bearer ${token}`,
      ...init.headers,
    },
  })
  if (response.status === 401) {
    clearSession()
    window.location.replace(loginURL())
    throw new Error('登录状态已失效')
  }

  const payload = (await response.json()) as APIResponse<T>
  if (!response.ok || !payload.success) {
	const details = payload.data as unknown as { message?: string } | null
	const code = payload.error || `请求失败 (${response.status})`
	const message = typeof details?.message === 'string' ? details.message.trim() : ''
	throw new Error(message && message !== code ? `${code}: ${message}` : code)
  }
  return payload
}

export async function apiRequest<T>(path: string, signal?: AbortSignal): Promise<T> {
  const payload = await requestEnvelope<T>(path, { signal })
  return payload.data
}

export async function apiMutation<T>(path: string, body?: unknown, method = 'POST'): Promise<T> {
  const payload = await requestEnvelope<T>(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  return payload.data
}

export function getDashboard(range: DashboardRange, signal?: AbortSignal): Promise<DashboardSnapshot> {
  return apiRequest<DashboardSnapshot>(`/admin/dashboard?range=${range}`, signal)
}

export function checkForUpdates(): Promise<VersionInfo> {
  return apiMutation<VersionInfo>('/admin/version/check')
}


export async function getChannels(filters: ChannelFilters, signal?: AbortSignal): Promise<PaginatedResult<Channel>> {
  const params = new URLSearchParams({
    limit: String(filters.limit),
    offset: String(filters.offset),
  })
  if (filters.search) params.set('search', filters.search)
  if (filters.status && filters.status !== 'all') params.set('status', filters.status)
  if (filters.sort) params.set('sort', filters.sort)
  const payload = await requestEnvelope<Channel[]>(`/admin/channels?${params}`, { signal })
  return { data: payload.data, count: payload.count ?? payload.data.length }
}

export function setChannelsEnabled(channelIds: number[], enabled: boolean): Promise<unknown> {
  return apiMutation('/admin/channels/batch-enabled', { channel_ids: channelIds, enabled })
}

export function deleteChannels(channelIds: number[]): Promise<unknown> {
  return apiMutation('/admin/channels/batch-delete', { channel_ids: channelIds })
}

export function fetchChannelModelsPreview(payload: {
  urls: import('./types').ChannelURL[]
  api_keys: string[]
  protocol?: string
}): Promise<ChannelModelsPreview> {
  return apiMutation<ChannelModelsPreview>('/admin/channels/models/fetch', payload)
}

export function getChannelEditor(channelId: number, signal?: AbortSignal): Promise<ChannelEditorSnapshot> {
  return apiRequest<ChannelEditorSnapshot>(`/admin/channels/${channelId}/editor`, signal)
}

export function createChannel(payload: ChannelMutation): Promise<Channel> {
  return apiMutation<Channel>('/admin/channels', payload)
}

export function updateChannel(channelId: number, payload: ChannelMutation): Promise<Channel> {
  return apiMutation<Channel>(`/admin/channels/${channelId}`, payload, 'PUT')
}

export function deleteChannel(channelId: number): Promise<{ id: number }> {
  return apiMutation(`/admin/channels/${channelId}`, undefined, 'DELETE')
}

export async function importOAuthCredentials(files: File[]): Promise<{ created: number; skipped: number; failed: number }> {
  const form = new FormData()
  files.forEach((file) => form.append('files', file))
  const payload = await requestEnvelope<{ created: number; skipped: number; failed: number }>('/admin/oauth/credentials/import', {
    method: 'POST',
    body: form,
  })
  return payload.data
}

export function getLogsBootstrap(range: DashboardRange, signal?: AbortSignal): Promise<LogsBootstrap> {
  return apiRequest<LogsBootstrap>(`/admin/logs/bootstrap?range=${range}`, signal)
}

export async function getLogs(filters: LogFilters, signal?: AbortSignal): Promise<PaginatedResult<LogEntry>> {
  const params = new URLSearchParams({
    range: filters.range,
    limit: String(filters.limit),
    offset: String(filters.offset),
  })
  if (filters.channel_name) params.set('channel_name', filters.channel_name)
  if (filters.model) params.set('model', filters.model)
  if (filters.status_code) params.set('status_code', filters.status_code)
  if (filters.log_source) params.set('log_source', filters.log_source)
  const payload = await requestEnvelope<LogEntry[]>(`/admin/logs?${params}`, { signal })
  return { data: payload.data, count: payload.count ?? payload.data.length }
}

export function getStats(filters: StatsFilters, signal?: AbortSignal): Promise<StatsSnapshot> {
  const params = new URLSearchParams({ range: filters.range })
  if (filters.channel_name) params.set('channel_name', filters.channel_name)
  if (filters.model) params.set('model', filters.model)
  return apiRequest<StatsSnapshot>(`/admin/stats?${params}`, signal)
}

export function getStatsFilterOptions(range: DashboardRange, signal?: AbortSignal): Promise<StatsFilterOptions> {
  return apiRequest<StatsFilterOptions>(`/admin/stats/filter-options?range=${range}`, signal)
}

export function testChannel(
  channelId: number,
  payload: { model: string; content: string; stream: boolean; client_protocol: string },
): Promise<ChannelTestResult> {
  return apiMutation<ChannelTestResult>(`/admin/channels/${channelId}/test`, payload)
}

export function getSites(signal?: AbortSignal): Promise<Site[]> {
  return apiRequest<Site[]>('/admin/sites', signal)
}

export function createSite(payload: {
  name: string
  base_url: string
  platform: string
  timezone: string
  use_system_proxy?: boolean
  proxy_url?: string
  external_checkin_url?: string
  tags?: string[]
	account?: {
		label: string
		credential_type: string
		credential: { api_key?: string; access_token?: string; refresh_token?: string; expires_at?: number; cookie?: string; user_id?: number; username?: string; password?: string }
		enabled: boolean
		auto_checkin: boolean
		auto_refresh: boolean
		timezone?: string
	}
}): Promise<Site> {
  return apiMutation<Site>('/admin/sites', payload)
}

export function updateSite(siteId: number, payload: Partial<Pick<Site,
  'name' | 'base_url' | 'platform' | 'timezone' | 'use_system_proxy' | 'proxy_url' | 'external_checkin_url' | 'enabled'
>>): Promise<Site> {
  return apiMutation<Site>(`/admin/sites/${siteId}`, payload, 'PATCH')
}

export function deleteSite(siteId: number): Promise<{ id: number; deleted: boolean }> {
  return apiMutation(`/admin/sites/${siteId}`, undefined, 'DELETE')
}

export function probeSite(siteId: number): Promise<{ matched: boolean; provider_id: string; system_name?: string }> {
  return apiMutation(`/admin/sites/${siteId}/probe`)
}

export function getSiteAccounts(siteId: number, signal?: AbortSignal): Promise<SiteAccount[]> {
  return apiRequest<SiteAccount[]>(`/admin/sites/${siteId}/accounts`, signal)
}

export async function getSiteInventory(signal?: AbortSignal): Promise<SiteInventory> {
  const sites = await getSites(signal)
  const groups = await Promise.all(sites.map((site) => getSiteAccounts(site.id, signal)))
  return { sites, accounts: groups.flat() }
}

export function createSiteAccount(siteId: number, payload: {
  label: string
  credential_type: string
	credential: { api_key?: string; access_token?: string; refresh_token?: string; expires_at?: number; cookie?: string; user_id?: number; username?: string; password?: string }
  enabled: boolean
  auto_checkin: boolean
  auto_refresh: boolean
  timezone?: string
}): Promise<SiteAccount> {
  return apiMutation<SiteAccount>(`/admin/sites/${siteId}/accounts`, payload)
}

export function updateSiteAccount(accountId: number, payload: Partial<Pick<SiteAccount,
  'label' | 'enabled' | 'auto_checkin' | 'auto_refresh' | 'timezone' | 'credential_type'
>> & { credential?: { api_key?: string; access_token?: string; refresh_token?: string; expires_at?: number; cookie?: string; user_id?: number; username?: string; password?: string } }): Promise<SiteAccount> {
  return apiMutation<SiteAccount>(`/admin/site-accounts/${accountId}`, payload, 'PATCH')
}

export function verifySiteAccountCredential(accountId: number, payload: {
  credential_type: string
  credential: { api_key?: string; access_token?: string; refresh_token?: string; expires_at?: number; cookie?: string; user_id?: number; username?: string; password?: string }
}): Promise<SiteCredentialVerification> {
  return apiMutation<SiteCredentialVerification>(`/admin/site-accounts/${accountId}/credential/verify`, payload)
}

export function getSiteChannelBindings(signal?: AbortSignal): Promise<import('./types').SiteChannelBinding[]> {
  return apiRequest<import('./types').SiteChannelBinding[]>('/admin/site-channel-bindings', signal)
}

export function deleteSiteAccount(accountId: number): Promise<{ id: number; deleted: boolean }> {
  return apiMutation(`/admin/site-accounts/${accountId}`, undefined, 'DELETE')
}

export function startAccountTask(accountId: number, kind: 'checkin' | 'refresh' | 'model_refresh'): Promise<{ task_id: string }> {
  const suffix = kind === 'model_refresh' ? 'models/refresh' : kind
  return apiMutation(`/admin/site-accounts/${accountId}/${suffix}`)
}

export function getSiteTask(taskId: string, signal?: AbortSignal): Promise<SiteTask> {
  return apiRequest<SiteTask>(`/admin/site-tasks/${encodeURIComponent(taskId)}`, signal)
}

export async function getSiteModels(filters: {
  site_id?: number
  account_id?: number
  include_disabled?: boolean
  limit?: number
  offset?: number
} = {}, signal?: AbortSignal): Promise<PaginatedResult<SiteAccountModel>> {
  const params = new URLSearchParams({
    limit: String(filters.limit ?? 1000),
    offset: String(filters.offset ?? 0),
  })
  if (filters.site_id) params.set('site_id', String(filters.site_id))
  if (filters.account_id) params.set('account_id', String(filters.account_id))
  if (filters.include_disabled) params.set('include_disabled', 'true')
  const payload = await requestEnvelope<SiteAccountModel[]>(`/admin/site-models?${params}`, { signal })
  return { data: payload.data, count: payload.count ?? payload.data.length }
}

export function testSiteAccountModel(
  accountId: number,
  payload: { model: string; content: string; stream: boolean; client_protocol: string },
): Promise<ChannelTestResult> {
  return apiMutation<ChannelTestResult>(`/admin/site-accounts/${accountId}/model-probe`, payload)
}

export async function waitForSiteTask(taskId: string, signal?: AbortSignal, timeoutMs = 120_000): Promise<SiteTask> {
  const startedAt = Date.now()
  while (Date.now() - startedAt < timeoutMs) {
    const task = await getSiteTask(taskId, signal)
    if (['success', 'partial', 'failed', 'cancelled'].includes(task.status)) return task
    await abortableDelay(500, signal)
  }
  throw new Error('任务等待超时')
}

export async function runAccountTask(accountId: number, kind: 'checkin' | 'refresh' | 'model_refresh', signal?: AbortSignal): Promise<SiteTask> {
  const queued = await startAccountTask(accountId, kind)
  return waitForSiteTask(queued.task_id, signal)
}

export async function getCheckinAttempts(accountId: number, signal?: AbortSignal): Promise<CheckinAttempt[]> {
  return apiRequest<CheckinAttempt[]>(`/admin/site-accounts/${accountId}/checkin-runs?limit=100`, signal)
}

export function projectSiteAccount(accountId: number, payload: {
  projection_key: string
  name?: string
  api_key?: string
  force: boolean
}): Promise<SiteProjectionResult> {
  return apiMutation<SiteProjectionResult>(`/admin/site-accounts/${accountId}/project`, payload)
}

export async function getAnnouncements(filters: {
  site_id?: number
  unread?: boolean
  limit: number
  offset: number
}, signal?: AbortSignal): Promise<PaginatedResult<SiteAnnouncement>> {
  const params = new URLSearchParams({ limit: String(filters.limit), offset: String(filters.offset) })
  if (filters.site_id) params.set('site_id', String(filters.site_id))
  if (filters.unread) params.set('unread', 'true')
  let lastError: unknown
  for (let attempt = 0; attempt < 2; attempt += 1) {
    try {
      const payload = await requestEnvelope<SiteAnnouncement[]>(`/admin/announcements?${params}`, { signal })
      return { data: payload.data, count: payload.count ?? payload.data.length }
    } catch (reason) {
      lastError = reason
      if (signal?.aborted || (reason instanceof Error && /登录状态已失效/.test(reason.message))) throw reason
      if (attempt === 0) await abortableDelay(250, signal)
    }
  }
  throw lastError instanceof Error ? lastError : new Error(String(lastError || '公告请求失败'))
}

export function refreshAnnouncements(siteId = 0): Promise<{ task_id: string }> {
  return apiMutation('/admin/announcements/refresh', { site_id: siteId })
}

export function markAnnouncementRead(id: number): Promise<{ id: number; read: boolean }> {
  return apiMutation(`/admin/announcements/${id}/read`)
}

export function markAllAnnouncementsRead(siteId = 0): Promise<{ read: boolean }> {
  return apiMutation('/admin/announcements/read-all', { site_id: siteId })
}

export function getWebhookConfig(signal?: AbortSignal): Promise<WebhookConfig> {
	return apiRequest<WebhookConfig>('/admin/webhook', signal)
}

export function updateWebhookConfig(payload: {
	enabled: boolean
	url?: string
	telegram_enabled: boolean
	telegram_bot_token?: string
	telegram_chat_id?: string
	telegram_use_system_proxy: boolean
	telegram_clear?: boolean
	low_balance_enabled: boolean
	low_balance_threshold: number
	checkin_failure_enabled: boolean
	cooldown_minutes: number
}): Promise<WebhookConfig> {
	return apiMutation<WebhookConfig>('/admin/webhook', payload, 'PUT')
}

export function testWebhook(target: 'webhook' | 'telegram' = 'webhook'): Promise<{ status: string; attempts: number }> {
	return apiMutation(`/admin/webhook/test?target=${target}`)
}

export function getAuthTokens(range: DashboardRange, signal?: AbortSignal): Promise<AuthTokenList> {
  return apiRequest<AuthTokenList>(`/admin/auth-tokens?range=${range}`, signal)
}

export function createAuthToken(payload: {
  description: string
  expires_at: number | null
  is_active: boolean
  allowed_models: string[]
  allowed_channel_ids: number[]
  channel_restriction_mode: 'allow' | 'deny'
  cost_limit_usd: number
  max_concurrency: number
}): Promise<AuthToken> {
  return apiMutation<AuthToken>('/admin/auth-tokens', payload)
}

export function updateAuthToken(tokenId: number, payload: Partial<{
  description: string
  expires_at: number | null
  is_active: boolean
  allowed_models: string[]
  allowed_channel_ids: number[]
  channel_restriction_mode: 'allow' | 'deny'
  cost_limit_usd: number
  max_concurrency: number
}>): Promise<AuthToken> {
  return apiMutation<AuthToken>(`/admin/auth-tokens/${tokenId}`, payload, 'PUT')
}

export function deleteAuthToken(tokenId: number): Promise<{ id: number }> {
  return apiMutation(`/admin/auth-tokens/${tokenId}`, undefined, 'DELETE')
}

export function revealAuthToken(tokenId: number): Promise<{ id: number; token: string; token_hint: string }> {
  return apiRequest(`/admin/auth-tokens/${tokenId}/reveal`)
}

export function getSystemSettings(signal?: AbortSignal): Promise<SystemSetting[]> {
  return apiRequest<SystemSetting[]>('/admin/settings', signal)
}

export function updateSystemSettings(values: Record<string, string>): Promise<{ message: string }> {
  return apiMutation('/admin/settings/batch', values)
}

export function resetSystemSetting(key: string): Promise<{ key: string; value: string }> {
  return apiMutation(`/admin/settings/${encodeURIComponent(key)}/reset`)
}

export function exportBackup(type: BackupType): Promise<BackupDocument> {
  return apiRequest<BackupDocument>(`/admin/backup/export?type=${encodeURIComponent(type)}`)
}

export function importBackup(data: BackupDocument): Promise<BackupImportResult> {
  return apiMutation<BackupImportResult>('/admin/backup/import', { data })
}

export function getBackupWebDAV(signal?: AbortSignal): Promise<BackupWebDAVConfig> {
  return apiRequest<BackupWebDAVConfig>('/admin/backup/webdav', signal)
}

export function updateBackupWebDAV(payload: {
  enabled: boolean
  file_url: string
  username: string
  password?: string
  clear_password?: boolean
  export_type: BackupType
  auto_sync_enabled: boolean
  auto_sync_interval_hours: number
}): Promise<BackupWebDAVConfig> {
  return apiMutation<BackupWebDAVConfig>('/admin/backup/webdav', payload, 'PUT')
}

export function uploadBackupToWebDAV(type: BackupType): Promise<{ status: string; file_url: string; last_sync_at: number }> {
  return apiMutation('/admin/backup/webdav/export', { type })
}

export function restoreBackupFromWebDAV(): Promise<BackupImportResult> {
  return apiMutation('/admin/backup/webdav/import')
}

export async function getActiveRequests(signal?: AbortSignal): Promise<ActiveRequest[]> {
  const payload = await requestEnvelope<ActiveRequest[]>('/admin/active-requests', { signal })
  return payload.data
}

export function getActiveRequestDebug(requestId: number, signal?: AbortSignal): Promise<unknown> {
  return apiRequest(`/admin/active-requests/${requestId}/debug-log`, signal)
}

export function getFingerprints(signal?: AbortSignal): Promise<ModelFingerprint[]> {
  return apiRequest<ModelFingerprint[]>('/admin/fingerprints', signal)
}

export function getFingerprintResults(signal?: AbortSignal): Promise<FingerprintTestRecord[]> {
  return apiRequest<FingerprintTestRecord[]>('/admin/fingerprints/test-results', signal)
}

export function startFingerprintCalibration(payload: {
  name: string; channel_id: number; model: string; client_protocol: string
  iterations: number; concurrency: number; key_index: number; stream: boolean
}): Promise<{ job_id: string }> {
  return apiMutation('/admin/fingerprints/calibrate', payload)
}

export function startFingerprintTest(payload: {
  channel_id: number; model: string; client_protocol: string; fingerprint_id?: number
  iterations: number; concurrency: number; key_index: number; stream: boolean
}): Promise<{ job_id: string }> {
  return apiMutation('/admin/fingerprints/test', payload)
}

export function getFingerprintJob(jobId: string, signal?: AbortSignal): Promise<FingerprintJob> {
  return apiRequest(`/admin/fingerprints/jobs/${encodeURIComponent(jobId)}`, signal)
}

export async function waitForFingerprintJob(jobId: string, signal?: AbortSignal, timeoutMs = 180_000): Promise<FingerprintJob> {
  const startedAt = Date.now()
  while (Date.now() - startedAt < timeoutMs) {
    const job = await getFingerprintJob(jobId, signal)
    if (['succeeded', 'failed', 'cancelled'].includes(job.status)) return job
    await abortableDelay(600, signal)
  }
  throw new Error('指纹任务等待超时')
}

export function deleteFingerprint(id: number): Promise<{ deleted: boolean }> {
  return apiMutation(`/admin/fingerprints/${id}`, undefined, 'DELETE')
}

function abortableDelay(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) { reject(new DOMException('Aborted', 'AbortError')); return }
    const onAbort = () => { window.clearTimeout(timer); reject(new DOMException('Aborted', 'AbortError')) }
    const timer = window.setTimeout(() => { signal?.removeEventListener('abort', onAbort); resolve() }, ms)
    signal?.addEventListener('abort', onAbort, { once: true })
  })
}
