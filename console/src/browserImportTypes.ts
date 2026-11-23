export type BrowserImportFormat = 'pivotflow-browser-bridge' | 'all-api-hub'
export type BrowserImportMode = 'verify' | 'save_disabled'
export type BrowserImportCredentialType = 'api_key' | 'access_token' | 'cookie'

export interface BrowserImportCandidate {
  index: number
  source_origin: string
  base_url: string
  site_name: string
  platform: string
  account_label: string
  credential_type: BrowserImportCredentialType | ''
  refresh_configured: boolean
  eligible: boolean
  problem?: string
  duplicate_of?: number
  target_site_id: number
  candidates: { id: number; label: string; match: 'credential' | 'user_id' | 'label' | 'same_site' }[]
}

export interface BrowserImportInspection {
  version: 1
  format: BrowserImportFormat
  captured_at: number
  ignored_sections: string[]
  entries: BrowserImportCandidate[]
}

export interface BrowserImportVerification {
  status: 'verified' | 'unverified'
  scope: 'models' | 'account' | 'none'
  checked_at: number
  identity_known: boolean
  user_id?: number
  username?: string
  model_count: number
  model_count_known: boolean
  routing_key_available: boolean
}

export interface BrowserImportPreviewContext {
  format: BrowserImportFormat
  source_origin: string
  base_url: string
  site_name: string
  account_label: string
  credential_type: BrowserImportCredentialType
  captured_at: number
  target_site_id: number
  target_account_id: number
  target_site_name: string
  target_account_label: string
  mode: BrowserImportMode
  verification: BrowserImportVerification
}

export interface BrowserImportApplyResult {
  site_id: number
  account_id: number
  action: 'create' | 'update' | 'unchanged'
  verified: boolean
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

export function browserImportId(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 ? value : 0
}

function nonnegative(value: unknown): number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : 0
}

export function safeBrowserImportText(value: unknown, limit = 191): string {
  return typeof value === 'string' && Array.from(value).length <= limit && !/[\u0000-\u001f\u007f-\u009f]/u.test(value) ? value.trim() : ''
}

export function safeBrowserImportURL(value: unknown, originOnly = false): string {
  const raw = safeBrowserImportText(value, 2048)
  if (!raw || /[\\?#]/u.test(raw)) return ''
  try {
    const url = new URL(raw)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || /^https?:\/\/[^/]*@/iu.test(raw) || (originOnly && url.pathname !== '/')) return ''
    return originOnly ? url.origin : url.href.replace(/\/+$/u, '')
  } catch { return '' }
}

function importFormat(value: unknown): BrowserImportFormat | undefined {
  return value === 'pivotflow-browser-bridge' || value === 'all-api-hub' ? value : undefined
}

function credentialType(value: unknown): BrowserImportCredentialType | '' {
  return value === 'api_key' || value === 'access_token' || value === 'cookie' ? value : ''
}

// Whitelist the recovery DTO. Unexpected credential fields never survive into
// a component, browser history, or a persisted snapshot.
export function normalizeBrowserImportContext(value: unknown): BrowserImportPreviewContext | undefined {
  const raw = record(value)
  const format = importFormat(raw.format)
  const type = credentialType(raw.credential_type)
  const source = safeBrowserImportURL(raw.source_origin, true)
  const base = safeBrowserImportURL(raw.base_url)
  if (!format || !type || !source || !base || new URL(base).origin !== source || !['verify', 'save_disabled'].includes(String(raw.mode))) return undefined
  const verification = record(raw.verification)
  const scope = verification.scope === 'models' || verification.scope === 'account' ? verification.scope : 'none'
  const verified = verification.status === 'verified' && scope !== 'none'
  const userId = verified && verification.identity_known === true ? browserImportId(verification.user_id) : 0
  const username = verified ? safeBrowserImportText(verification.username) : ''
  return {
    format, source_origin: source, base_url: base,
    site_name: safeBrowserImportText(raw.site_name), account_label: safeBrowserImportText(raw.account_label), credential_type: type,
    captured_at: nonnegative(raw.captured_at), target_site_id: browserImportId(raw.target_site_id), target_account_id: browserImportId(raw.target_account_id),
    target_site_name: safeBrowserImportText(raw.target_site_name), target_account_label: safeBrowserImportText(raw.target_account_label), mode: raw.mode as BrowserImportMode,
    verification: {
      status: verified ? 'verified' : 'unverified', scope: verified ? scope : 'none', checked_at: verified ? nonnegative(verification.checked_at) : 0,
      identity_known: userId > 0, ...(userId ? { user_id: userId } : {}), ...(username ? { username } : {}),
      model_count: verified && verification.model_count_known === true ? nonnegative(verification.model_count) : 0,
      model_count_known: verified && verification.model_count_known === true,
      routing_key_available: verified && verification.routing_key_available === true,
    },
  }
}

export function normalizeBrowserImportResult(value: unknown): BrowserImportApplyResult | undefined {
  const raw = record(value)
  const siteId = browserImportId(raw.site_id), accountId = browserImportId(raw.account_id)
  if (!siteId || !accountId || !['create', 'update', 'unchanged'].includes(String(raw.action))) return undefined
  return { site_id: siteId, account_id: accountId, action: raw.action as BrowserImportApplyResult['action'], verified: raw.verified === true }
}

export function normalizeBrowserImportInspection(value: unknown): BrowserImportInspection {
  const raw = record(value), format = importFormat(raw.format)
  if (raw.version !== 1 || !format || !Array.isArray(raw.entries) || raw.entries.length > 64) throw new Error('invalid_response')
  return {
    version: 1, format, captured_at: nonnegative(raw.captured_at),
    ignored_sections: Array.isArray(raw.ignored_sections) ? raw.ignored_sections.filter((item): item is string => ['apiCredentialProfiles', 'channelConfigs', 'featureGuidance', 'preferences', 'tagStore'].includes(String(item))) : [],
    entries: raw.entries.map((value, position) => {
      const item = record(value)
      const source = safeBrowserImportURL(item.source_origin, true), base = safeBrowserImportURL(item.base_url)
      const type = credentialType(item.credential_type)
      const index = typeof item.index === 'number' && Number.isSafeInteger(item.index) && item.index >= 0 && item.index < 64 ? item.index : position
      const matches = ['credential', 'user_id', 'label', 'same_site'] as const
      const candidates = Array.isArray(item.candidates) ? item.candidates.map((candidate) => {
        const target = record(candidate)
        return { id: browserImportId(target.id), label: safeBrowserImportText(target.label), match: matches.includes(target.match as typeof matches[number]) ? target.match as typeof matches[number] : 'same_site' as const }
      }).filter((candidate) => candidate.id > 0) : []
      const duplicate = typeof item.duplicate_of === 'number' && Number.isSafeInteger(item.duplicate_of) && item.duplicate_of >= 0 && item.duplicate_of < 64 ? item.duplicate_of : undefined
      return {
        index, source_origin: source, base_url: base, site_name: safeBrowserImportText(item.site_name), platform: safeBrowserImportText(item.platform, 64), account_label: safeBrowserImportText(item.account_label), credential_type: type,
        refresh_configured: item.refresh_configured === true,
        eligible: item.eligible === true && Boolean(source && base && type) && new URL(base).origin === source && duplicate === undefined,
        ...(typeof item.problem === 'string' && /^[a-z_]{1,64}$/u.test(item.problem) ? { problem: item.problem } : {}),
        ...(duplicate !== undefined ? { duplicate_of: duplicate } : {}),
        target_site_id: browserImportId(item.target_site_id), candidates,
      }
    }),
  }
}
