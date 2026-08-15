export type DashboardRange = 'today' | 'this_week' | 'this_month'

export interface DashboardTotals {
  requests: number
  success: number
  errors: number
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_creation_tokens: number
  cost: number
  effective_cost: number
}

export interface DashboardBalance {
  currency: string
  amount: number
  accounts: number
}

export interface DashboardUsage {
  key: string
  label: string
  requests: number
  success: number
  errors: number
  input_tokens: number
  output_tokens: number
  effective_cost: number
  share: number
}

export interface DashboardSiteUsage extends DashboardUsage {
  site_id: number
}

export interface MetricPoint {
  ts: string
  success: number
  error: number
  effective_cost?: number
  input_tokens?: number
  output_tokens?: number
}

export interface DashboardSnapshot {
  range: DashboardRange
  starts_at: number
  ends_at: number
  generated_at: number
  totals: DashboardTotals
  balances: DashboardBalance[]
  model_usage: DashboardUsage[]
  site_usage: DashboardSiteUsage[]
  client_usage: DashboardUsage[]
  trend: MetricPoint[]
  unread_notices: number
  site_count: number
  enabled_sites: number
  account_count: number
  healthy_accounts: number
  channel_count: number
  enabled_channels: number
}

export interface APIResponse<T> {
  success: boolean
  data: T
  error?: string
  count?: number
}

export interface ChannelURL {
  url: string
  exact?: boolean
  protocols?: string[]
}

export interface ChannelModel {
  model: string
  redirect_model?: string
  disabled?: boolean
}

export interface ChannelCooldown {
  model?: string
  key_index?: number
  cooldown_until?: string
  cooldown_remaining_ms?: number
}

export interface Channel {
  id: number
  name: string
  auth_type: string
  protocol_transform_mode: string
  urls: ChannelURL[]
  priority: number
  rpm_limit: number
  max_concurrency: number
  enabled: boolean
  models: ChannelModel[]
  daily_cost_limit: number
  cost_multiplier: number
  key_count: number
  key_strategy?: string
  cooldown_until?: string
  cooldown_remaining_ms?: number
  key_cooldowns?: ChannelCooldown[]
  model_cooldowns?: ChannelCooldown[]
  effective_priority?: number
  success_rate?: number
  websockets?: boolean
  scheduled_check_enabled?: boolean
  scheduled_check_model?: string
  proxy_url?: string
  retry_other_keys_on_failure?: boolean
  custom_request_rules?: unknown
  cooldown_detection_rules?: unknown
}

export interface ChannelAPIKey {
  id?: number
  channel_id: number
  key_index: number
  api_key: string
  note?: string
  key_strategy?: string
  disabled?: boolean
}

export interface ChannelEditorSnapshot {
  channel: Channel
  keys: ChannelAPIKey[]
  model_stats: { available: boolean; items: Array<{ model: string; success: number; error: number; total: number }> }
  url_stats: { available: boolean; items: unknown[] }
  features: { scheduled_check_enabled: boolean }
}

export interface ChannelModelsPreview {
  models: ChannelModel[]
  protocol: string
  source: string
  debug?: unknown
}

export interface ChannelMutation {
  name: string
  auth_type: string
  api_keys: Array<{ api_key: string; note?: string }>
  key_strategy: string
  urls: ChannelURL[]
  priority: number
  rpm_limit: number
  max_concurrency: number
  models: ChannelModel[]
  enabled: boolean
  websockets: boolean
  protocol_transform_mode: string
  scheduled_check_enabled: boolean
  scheduled_check_model: string
  daily_cost_limit: number
  cost_multiplier: number
  proxy_url?: string
  retry_other_keys_on_failure: boolean
  custom_request_rules?: unknown
  cooldown_detection_rules?: unknown
}

export interface PaginatedResult<T> {
  data: T[]
  count: number
}

export interface ChannelFilters {
  search?: string
  status?: string
  limit: number
  offset: number
}

export interface LogChannelOption {
  id: number
  name: string
}

export interface LogsBootstrap {
  channel_test_content: string
  models: string[]
  channels: LogChannelOption[]
  status_codes: number[]
}

export interface LogEntry {
  id: number
  time: number
  model: string
  actual_model?: string
  log_source?: string
  channel_id: number
  channel_name?: string
  status_code: number
  message: string
  duration: number
  first_byte_time: number
  is_streaming: boolean
  client_protocol?: string
  upstream_protocol?: string
  input_tokens: number
  output_tokens: number
  cache_read_input_tokens: number
  cache_creation_input_tokens: number
  cost: number
  cost_multiplier: number
}

export interface LogFilters {
  range: DashboardRange
  channel_name?: string
  model?: string
  status_code?: string
  log_source?: string
  limit: number
  offset: number
}

export interface HealthPoint {
  ts: string
  rate: number
  success: number
  error: number
  rate_limited: number
}

export interface StatsEntry {
  channel_id?: number
  channel_name: string
  channel_priority?: number
  cost_multiplier?: number
  model: string
  success: number
  error: number
  total: number
  avg_first_byte_time_seconds?: number
  avg_duration_seconds?: number
  peak_rpm?: number
  avg_rpm?: number
  recent_rpm?: number
  total_input_tokens?: number
  total_output_tokens?: number
  total_cache_read_input_tokens?: number
  total_cache_creation_input_tokens?: number
  total_cost?: number
  effective_cost?: number
  health_timeline?: HealthPoint[]
}

export interface RPMStats {
  peak_rpm: number
  peak_qps: number
  avg_rpm: number
  avg_qps: number
  recent_rpm: number
  recent_qps: number
}

export interface StatsSnapshot {
  stats: StatsEntry[]
  duration_seconds: number
  rpm_stats: RPMStats
  is_today: boolean
}

export interface StatsFilterOptions {
  channel_names: string[]
  models: string[]
}

export interface StatsFilters {
  range: DashboardRange
  channel_name?: string
  model?: string
}

export interface ChannelTestResult {
  success: boolean
  status?: 'pass' | 'fail' | 'unsupported' | 'inconclusive' | 'skipped'
  reason?: string
  error?: string
  status_code?: number
  duration_ms?: number
  first_byte_duration_ms?: number
  input_tokens?: number
  output_tokens?: number
  cost_usd?: number
  response_text?: string
  raw_response?: string
  base_url?: string
  client_protocol?: string
  upstream_protocol?: string
  actual_model?: string
  tested_key_index?: number
  total_keys?: number
  source_type?: 'channel' | 'site_account'
  channel_id?: number
  site_id?: number
  site_account_id?: number
}

export interface Site {
  id: number
  name: string
  platform: string
  base_url: string
  enabled: boolean
  timezone: string
  use_system_proxy: boolean
  proxy_url?: string
  external_checkin_url?: string
  tags_json: string
  last_probe_status: string
  last_error?: string
  created_at: number
  updated_at: number
}

export interface SiteAccount {
  id: number
  site_id: number
  label: string
  credential_type: string
  credential_configured: boolean
  credential_refresh_configured: boolean
  credential_expires_at?: number
  enabled: boolean
  auto_checkin: boolean
  auto_refresh: boolean
  timezone?: string
  status: string
  balance?: number
  balance_currency: string
  balance_updated_at?: number
  last_refresh_at?: number
  last_refresh_status: string
  consecutive_failures: number
  last_checkin_at?: number
  last_checkin_status: string
  last_error?: string
  created_at: number
  updated_at: number
}

export interface SiteInventory {
  sites: Site[]
  accounts: SiteAccount[]
}

export interface SiteCredentialVerification {
  credential_type: string
  user_id?: number
  username?: string
  balance?: number
  currency?: string
  routing_key_available: boolean
  model_count: number
}

export interface SiteAccountModel {
  site_account_id: number
  model: string
  route_type: string
  source: string
  disabled: boolean
  stale: boolean
  last_seen_at: number
  created_at: number
  updated_at: number
}

export interface SiteTask {
  id: string
  kind: string
  status: 'queued' | 'running' | 'success' | 'partial' | 'failed' | 'cancelled'
  progress?: { completed?: number; total?: number }
  result_ref?: string
  error?: string
  created_at: number
  started_at?: number
  finished_at?: number
}

export interface CheckinAttempt {
  id: number
  run_id: number
  site_account_id: number
  provider_id: string
  local_day: string
  trigger_scope: string
  status: string
  reward_text?: string
  balance_before?: number
  balance_after?: number
  balance_delta?: number
  balance_currency?: string
  message?: string
  error_code?: string
  retry_after_at?: number
  started_at?: number
  finished_at?: number
  attempt_no: number
}

export interface SiteAnnouncement {
  id: number
  site_id: number
  source_key: string
  title: string
  content_markdown: string
  level: string
  source_url?: string
  upstream_created_at?: number
  upstream_updated_at?: number
  first_seen_at: number
  last_seen_at: number
  read_at?: number
}

export interface SiteProjectionResult {
  action: 'created' | 'updated' | 'unchanged' | 'conflict' | string
  binding?: { channel_id?: number; status: string; last_sync_status: string }
  channel?: Channel
}

export interface SiteChannelBinding {
  id: number
  site_account_id: number
  projection_key: string
  channel_id?: number
  ownership: string
  status: string
  last_sync_status: string
  last_sync_error?: string
  created_at: number
  updated_at: number
}

export interface WebhookConfig {
	id: number
	enabled: boolean
	url_configured: boolean
	url_masked?: string
	low_balance_enabled: boolean
	low_balance_threshold: number
	checkin_failure_enabled: boolean
	cooldown_minutes: number
	last_delivery_status: string
	last_delivery_at?: number
	last_error?: string
	created_at?: number
	updated_at?: number
}

export interface AuthToken {
  id: number
  token: string
  token_hint?: string
  token_recoverable?: boolean
  description: string
  created_at: string
  expires_at?: number
  last_used_at?: number
  is_active: boolean
  success_count: number
  failure_count: number
  prompt_tokens_total: number
  completion_tokens_total: number
  effective_cost_usd: number
  cost_used_usd: number
  cost_limit_usd: number
  peak_rpm?: number
  avg_rpm?: number
  recent_rpm?: number
  allowed_models?: string[]
  allowed_channel_ids?: number[]
  channel_restriction_mode?: 'allow' | 'deny'
  max_concurrency: number
}

export interface AuthTokenList {
  tokens: AuthToken[]
  duration_seconds?: number
  rpm_stats?: RPMStats
  is_today: boolean
}

export interface SystemSetting {
  key: string
  value: string
  value_type: 'int' | 'float' | 'bool' | 'duration' | 'string' | 'json'
  description: string
  default_value: string
  updated_at: number
  editable: boolean
  disabled_reason?: string
}

export interface VersionInfo {
  version: string
  has_update?: boolean
  latest_version?: string
  release_url?: string
  last_check?: string
  message?: string
  error?: string
}

export interface ActiveRequest {
  id: number
  model: string
  client_ip: string
  start_time: number
  is_streaming: boolean
  channel_id?: number
  channel_name?: string
  upstream_protocol?: string
  api_key_used?: string
  token_id?: number
  base_url?: string
  bytes_received?: number
  client_first_byte_time?: number
  cost_multiplier: number
  upstream_websocket?: boolean
  debug_log_available?: boolean
  thinking_effort?: string
  upstream_status: string
}

export interface FingerprintStats {
  mean: number
  median: number
  std_dev: number
  min: number
  max: number
  unique: number
  mode: number
}

export interface ModelFingerprint {
  id: number
  name: string
  channel_id?: number
  channel_name: string
  model: string
  actual_model?: string
  client_protocol: string
  sample_count: number
  stats: FingerprintStats
  prompt_version: string
  created_at: string
}

export interface FingerprintTestRecord {
  id: number
  channel_id?: number
  channel_name: string
  model: string
  sample_count: number
  best_score: number
  matches?: unknown[]
  created_at: string
}

export interface FingerprintJob {
  id: string
  type: string
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled'
  progress: { completed?: number; total?: number }
  result?: unknown
  error?: string
}
