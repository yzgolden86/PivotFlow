export type DashboardRange = 'today' | 'this_week' | 'this_month'

export type RuntimeHealthState = 'blocked' | 'error' | 'warning' | 'working' | 'healthy' | 'idle' | 'disabled' | 'unknown'
export type RuntimeComponentId = 'billing' | 'request_history' | 'logs' | 'token_stats' | 'log_replication' | 'trace_replication' | 'cache_sync' | 'log_cleanup' | 'debug_cleanup' | 'startup_recovery' | 'websocket'
export type RuntimeHealthReason = 'not_enabled' | 'awaiting_work' | 'processing' | 'write_failed' | 'queue_pressure' | 'recent_drop' | 'ready' | 'shutting_down' | 'billing_blocked' | 'billing_unpersisted' | 'settlement_failed' | 'journal_failed' | 'billing_backlog' | 'pending_settlement' | 'replication_failed' | 'cache_sync_failed' | 'cache_needs_reconcile' | 'cleanup_failed' | 'debug_disabled_cleared' | 'retention_disabled' | 'not_hybrid' | 'storage_unknown' | 'recovery_unobserved' | 'configuration_recovery_failed' | 'history_recovery_failed' | 'restoring' | 'no_sessions' | 'websocket_pressure' | 'replication_unobserved' | 'unknown'
export type RuntimeFreshness = 'current' | 'paused' | 'stale' | 'unavailable'

export interface RuntimeOperationStats {
  active: number
  attempts: number
  succeeded: number
  failed: number
  cancelled: number
  failing: boolean
  consecutive_failures: number
  last_result: '' | 'success' | 'error' | 'cancelled' | 'unknown'
  last_attempt_at: number
  last_finished_at: number
  last_success_at: number
  last_failure_at: number
  recovered_at: number
  last_error_code: '' | 'timeout' | 'operation_failed'
}

export interface RuntimeQueueStats {
  enabled: boolean
  queued: number
  capacity: number
  buffered: number
  dropped: number
  write_failed: number
  written: number
  last_dropped_at: number
  operation: RuntimeOperationStats
}

export interface RuntimeReplicationStats {
  enabled: boolean
  queued: number
  capacity: number
  dropped: number
  failed: number
  last_dropped_at: number
  operation: RuntimeOperationStats
}

export interface RuntimeCleanupStats {
  enabled: boolean
  mode: 'unknown' | 'retention' | 'clearing' | 'disabled'
  retention_days: number
  retention_minutes: number
  interval_ms: number
  next_run_at: number
  deleted: number
  operation: RuntimeOperationStats
}

export interface RuntimeStartupRecovery {
  enabled: boolean
  history_enabled: boolean
  log_days: number
  configurations: RuntimeOperationStats
  logs: RuntimeOperationStats
  request_traces: RuntimeOperationStats
}

export interface RuntimeStorageStats {
  mode: 'unknown' | 'sqlite' | 'mysql' | 'postgres' | 'hybrid'
  primary: 'unknown' | 'sqlite' | 'mysql' | 'postgres'
  cache_may_be_stale: boolean
  cache_sync: RuntimeOperationStats
  log_replication: RuntimeReplicationStats
  startup_recovery: RuntimeStartupRecovery | null
}

export interface RuntimeBillingStats {
  enabled: boolean
  pending: number
  unpersisted: number
  blocked: boolean
  closing: boolean
  journal_failures: number
  store_failures: number
  invalid_cost_events: number
  settled: number
  deleted_token_events: number
  last_success_at: number
  oldest_pending_at: number
  journal: RuntimeOperationStats
  settlement: RuntimeOperationStats
}

export interface RuntimeWebsocketStats {
  sessions: number
  active_attachments: number
  downstream_connections: number
  rejected_downstream_connections: number
  max_downstream_connections: number
  max_downstream_connections_per_token: number
  upstream_connections: number
  upstream_handshakes: number
  upstream_reuses: number
  upstream_heartbeat_failures: number
  upstream_queued_read_bytes: number
  oldest_upstream_connection_seconds: number
  reconnects: number
  transcript_bytes: number
  max_transcript_bytes: number
  max_sessions: number
  ttl_expired: number
  capacity_rejected: number
  budget_rejected: number
  previous_response_misses: number
}

export interface RuntimeHealthComponent {
  id: RuntimeComponentId
  state: RuntimeHealthState
  reason: RuntimeHealthReason
}

export type RequestCacheStatus = 'hit' | 'miss' | 'refresh' | 'bypass'
export type RequestCacheReason =
  | 'disabled'
  | 'not_opted_in'
  | 'client_bypass'
  | 'invalid_mode'
  | 'request_no_store'
  | 'request_no_cache'
  | 'request_query'
  | 'request_encoding'
  | 'streaming'
  | 'unsupported_endpoint'
  | 'nondeterministic'
  | 'tools_or_state'
  | 'unsupported_request'
  | 'no_identity'
  | 'route_unavailable'
  | 'channel_changed'
  | 'custom_rules'
  | 'protocol_transform'
  | 'empty'
  | 'expired'
  | 'client_refresh'
  | 'unsafe_response'
  | 'response_no_store'
  | 'response_too_large'
  | 'capture_budget'
  | 'invalidated'
  | 'upstream_error'
  | 'canceled'
  | 'write_failed'
  | 'reused'
  | 'stored'
export type RequestCacheInvalidationReason =
  | 'manual_clear'
  | 'routing_changed'
  | 'keys_changed'
  | 'aliases_changed'
  | 'permissions_changed'

export interface RequestCacheConfig {
  enabled: boolean
  ttl_seconds: number
  max_entries: number
  max_bytes: number
  max_entry_bytes: number
}

export interface RequestCacheStats {
  entries: number
  bytes: number
  expired_entries: number
  captures: number
  capture_bytes: number
  lookups: number
  hits: number
  misses: number
  refreshes: number
  bypasses: number
  stored: number
  expired: number
  evicted: number
  rejected: number
  clears: number
  invalidations: Record<RequestCacheInvalidationReason, number>
  reasons: Record<RequestCacheReason, number>
  last_invalidation_reason: RequestCacheInvalidationReason | ''
  last_invalidation_at: number
}

export interface RequestCacheObservation {
  version: 1
  observed_at: number
  process_started_at: number
  config: RequestCacheConfig
  stats: RequestCacheStats
}

export interface RuntimeMetrics {
  started_at: number
  observed_at: number
  health: { version: number; state: RuntimeHealthState; components: RuntimeHealthComponent[] }
  billing: RuntimeBillingStats
  request_traces: RuntimeQueueStats & { replication?: RuntimeReplicationStats }
  logs: RuntimeQueueStats
  token_failure_stats: RuntimeQueueStats
  cleanup: { logs: RuntimeCleanupStats; debug_logs: RuntimeCleanupStats }
  storage: RuntimeStorageStats
  responses_websocket: RuntimeWebsocketStats
  request_cache?: RequestCacheObservation | null
}

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

export type CostSource = 'site_pricing' | 'local_estimate'
export type CostStatus =
  | 'estimated'
  | 'zero_price'
  | 'local_free'
  | 'usage_missing'
  | 'unpriced_model'
  | 'partial_estimate'
  | 'invalid'
  | 'historical_unknown'
  | 'not_charged'
export type CostBasis = 'token' | 'per_call' | 'mixed' | 'unknown'

export interface StandardCostComponent {
  price_per_million: number
  quantity: number
  cost: number
}

export interface StandardCostBreakdown {
  input: StandardCostComponent
  output: StandardCostComponent
  cache_read: StandardCostComponent
  cache_write: StandardCostComponent
  total: number
  service_tier_multiplier?: number
}

/** Frozen, public-safe request cost details returned by the backend. */
export interface CostDetails {
  model_cost_usd: number
  tool_cost_usd: number
  currency: 'USD'
  breakdown?: StandardCostBreakdown
}

/**
 * Versioned aggregate over successful requests in the selected range.
 *
 * `unknown_requests` includes `partial_requests` and `historical_requests`.
 * `local_free_requests` is a subset of `zero_requests`. Reconciliation is
 * intentionally limited to `not_reconciled` in this data model.
 */
export interface CostSummary {
  version: 1
  currency: 'USD'
  reconciliation: 'not_reconciled'
  successful_requests: number
  estimated_requests: number
  zero_requests: number
  local_free_requests: number
  partial_requests: number
  unknown_requests: number
  historical_requests: number
  site_pricing_requests: number
  local_estimate_requests: number
  mixed_requests: number
  recorded_cost_usd: number
  estimated_cost_usd: number
  partial_cost_usd: number
  historical_cost_usd: number
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
  cost_summary?: CostSummary | null
}

export interface DashboardSummary {
  cost_summary?: CostSummary | null
  total_requests: number
  success_requests: number
  error_requests: number
  range: string
  duration_seconds: number
  rpm_stats?: RPMStats
  is_today: boolean
  by_client_protocol?: Record<string, unknown>
}

export interface APIResponse<T> {
  success: boolean
  data: T
  error?: string
  count?: number
  enabled_count?: number
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

export type ChannelHeaderRuleAction = 'remove' | 'override' | 'append'
export type ChannelBodyRuleAction = 'remove' | 'override'

export interface ChannelHeaderRule {
  action: ChannelHeaderRuleAction
  name: string
  value?: string
}

export interface ChannelBodyRule {
  action: ChannelBodyRuleAction
  path: string
  value?: unknown
}

export interface ChannelRequestRules {
  headers?: ChannelHeaderRule[]
  body?: ChannelBodyRule[]
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
  source?: string
  site_sync_ownership?: 'projected' | 'manual'
  site_account_id?: number
  projection_key?: string
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
  effective_key_count?: number
  key_health_issue_count?: number
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
  available_time_start?: string
  available_time_end?: string
  proxy_url?: string
  retry_other_keys_on_failure?: boolean
  custom_request_rules?: ChannelRequestRules
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
  health?: APIKeyHealth
  allowed_models?: string[]
  model_scope_empty?: boolean
  cost_multiplier?: number
}

export interface APIKeyHealth {
  status: string
  reason: string
  status_code: number
  checked_at: number
}

export interface ChannelKeyHealthItem {
  id: number
  key_index: number
  masked_key: string
  note: string
  disabled: boolean
  cooldown_until: number
  health: APIKeyHealth
  allowed_models?: string[]
  model_scope_empty?: boolean
  cost_multiplier?: number
}

export interface ChannelKeyHealthSnapshot {
	channel_id: number
	channel_name: string
	models: string[]
	keys: ChannelKeyHealthItem[]
}

export interface RouteDiagnosticReason {
	code: string
	message: string
	blocking: boolean
	stage: string
}

export interface RouteDiagnosticURL {
  index: number
  status: string
  declared: boolean
  protocols: string[]
  disabled: boolean
  cooling: boolean
  fallback: boolean
  retry_after?: string
}

export interface RouteDiagnosticCost {
  channel_multiplier: number
  key_multiplier_min: number | null
  key_multiplier_max: number | null
  key_multiplier_count: number
  daily_used_usd: number | null
  daily_limit_usd: number
  site_binding_known: boolean
  site_bound: boolean
  price_status: string
  price_cached_at?: string
}

export interface RouteDiagnosticToken {
  id?: number
  description: string
  scope: string
  status: string
  admission: string
  permission_source: string
  model_allowed: boolean | null
  protocol_allowed: boolean | null
  channel_restricted: boolean
  cost_used_usd: number | null
  cost_limit_usd: number | null
  max_concurrency: number | null
  active_concurrency: number | null
  reasons: RouteDiagnosticReason[]
}

export interface ChannelRouteDiagnostic {
	channel_id: number
	channel_name: string
	enabled: boolean
	base_priority: number
	effective_priority: number
	success_rate: number
	health_sample_count: number
	exact_model_match: boolean
	fuzzy_model_match: boolean
	model_eligible_key_count: number
	active_key_count: number
	enabled_key_count: number
	routing_weight: number
	attemptable: boolean
	attempt_status: string
	blocked_stage?: string
	credential_kind: string
	failure_penalty: number
	ttfb_penalty: number
	ttfb_sample_count: number
	average_ttfb_seconds: number
	url_count: number
	compatible_url_count: number
	urls: RouteDiagnosticURL[]
	possible_upstream_models: string[]
	possible_upstream_model_count: number
	cost: RouteDiagnosticCost
	available_time_start: string
	available_time_end: string
	channel_cooldown_until?: string
	model_cooldown_until?: string
	actual_known: boolean
	rpm_limit: number
	current_rpm: number
	max_concurrency: number
	active_concurrency: number
	candidate: boolean
	candidate_position?: number
	higher_priority_count: number
	same_priority_count: number
	estimated_traffic_share: number
	actual_requests: number
	actual_share: number
	reasons: RouteDiagnosticReason[]
}

export interface RouteDiagnosticSticky {
	channel_id: number
	channel_name: string
	remembered_at: string
	expires_at: string
	in_candidate_pool: boolean
	forward_status: string
}

export interface RouteDiagnosticResponse {
	model: string
	client_protocol: string
	token_id?: number
	route_strategy: string
	pool_mode: string
	health_score_enabled: boolean
	target: ChannelRouteDiagnostic
	candidates: ChannelRouteDiagnostic[]
	sticky?: RouteDiagnosticSticky
	actual_window: string
	actual_total_requests: number
	summary: string[]
	observed_at: string
	warnings: string[]
	token: RouteDiagnosticToken
}

export interface ModelRouteInventoryItem {
  model: string
  kind: 'model' | 'group' | 'wildcard'
  aliases: string[]
  alias_count: number
  configured_channels: number
  enabled_channels: number
}

export interface ModelRouteInventory {
  version: number
  items: ModelRouteInventoryItem[]
  total: number
  offset: number
  limit: number
}

export interface ModelRouteDiagnosticResponse {
  version: number
  observed_at: string
  model: string
  client_protocol: string
  canonical_model: string
  group_names: string[]
  group_name_count: number
  policy: {
    route_strategy: string
    pool_mode: string
    health_score_enabled: boolean
    ttfb_score_enabled: boolean
    health_window_minutes: number
    candidate_count: number
    attemptable_count: number
    attempt_unknown_count: number
    tier_count: number
    weight_reference: string
    actual_window: string
    actual_known: boolean
    actual_total_requests: number
    sticky?: RouteDiagnosticSticky
  }
  token: RouteDiagnosticToken
  channels: ChannelRouteDiagnostic[]
  total: number
  offset: number
  limit: number
  summary: string[]
  warnings: string[]
}

export interface ChannelEditorSnapshot {
  channel: Channel
  keys: ChannelAPIKey[]
  model_stats: { available: boolean; items: Array<{ model: string; success: number; error: number; total: number }> }
  url_stats: { available: boolean; items: unknown[] }
  features: { scheduled_check_enabled: boolean }
}

export interface OAuthUsageWindow {
  limit_name: string
  kind: string
  used_percent: number
  remaining_percent: number
  limit_window_seconds: number
  reset_at: number
}

export interface OAuthUsageSummary {
  provider: string
  plan_type?: string
  windows: OAuthUsageWindow[]
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
  api_keys: Array<{ api_key: string; note?: string; allowed_models?: string[]; model_scope_empty?: boolean; cost_multiplier?: number }>
  key_strategy?: string
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
  available_time_start: string
  available_time_end: string
  daily_cost_limit: number
  cost_multiplier: number
  proxy_url?: string
  retry_other_keys_on_failure: boolean
  custom_request_rules?: ChannelRequestRules
  cooldown_detection_rules?: unknown
}

export interface PaginatedResult<T> {
  data: T[]
  count: number
  enabled_count?: number
}

export interface ChannelFilters {
  search?: string
  status?: string
  source?: string
  sort?: string
  limit: number
  offset: number
}

export interface LogChannelOption {
  id: number
  name: string
}

export interface LogsBootstrap {
  channel_test_content: string
  auth_tokens: AuthToken[]
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
  effective_cost?: number
  auth_token_id?: number
  auth_token_description?: string
  cost_metadata_version?: number
  cost_status?: string
  cost_basis?: string
  cost_details?: CostDetails | null
  // site_pricing = 站点自身价目表估算；local_estimate = 厂商标价本地估算。
  // 空值或未知旧值保留为来源未知，不能猜成站点价或本地价。
  cost_source?: string
  request_id?: string
  attempt_number?: number
}

export interface CostEntryLike {
  status_code?: number | null
  effective_cost?: unknown
  cost?: unknown
  cost_metadata_version?: unknown
  cost_status?: unknown
  cost_basis?: unknown
  cost_source?: unknown
  cost_details?: unknown
}

export interface LogFilters {
  range: DashboardRange
  channel_name?: string
  auth_token_id?: string | number
  model?: string
  search?: string
  status_code?: string
  log_source?: string
  cost_source?: CostSource | ''
  cost_status?: CostStatus | ''
  cost_basis?: CostBasis | ''
  limit: number
  offset: number
}

export type RequestOutcome = 'success' | 'error' | 'canceled'
export type RequestAttemptOutcome = RequestOutcome | 'capability' | 'skipped'
export type RequestRetryReason = 'none' | 'next_key' | 'next_url' | 'next_channel' | 'refresh_token' | 'compatibility'
export type RequestCooldownScope = 'none' | 'key' | 'model' | 'channel'

export interface RequestAttempt {
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

export interface RequestTrace {
  request_id: string
  started_at: number
  auth_token_id: number
  model?: string
  actual_model?: string
  channel_id?: number
  channel_name?: string
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
  attempts?: RequestAttempt[]
  cache_status?: string
  cache_reason?: string
}

export interface RequestTraceStats {
  requests: number
  successes: number
  errors: number
  canceled: number
  upstream_attempts: number
  upstream_successes: number
}

export interface RequestTraceFilters {
  range: DashboardRange
  limit: number
  offset: number
  search?: string
  outcome?: RequestOutcome | ''
  auth_token_id?: string | number
  model?: string
  client_protocol?: string
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
  actual_cost_multiplier_min?: number
  actual_cost_multiplier_max?: number
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

export interface SiteBalanceHistoryPoint {
  day: string
  currency: string
  balance: number | null
  accounts: number
  updated_at: number
  daily_delta?: number | null
  known_accounts?: number
  comparison_status?: 'complete' | 'baseline' | 'missing_previous' | 'coverage_changed' | 'incomplete' | 'overflow'
  scheduled_at?: number
}

export interface StatsSnapshot {
  stats: StatsEntry[]
  duration_seconds: number
  rpm_stats: RPMStats
  is_today: boolean
  balance_history?: SiteBalanceHistoryPoint[]
  balance_schedule?: { enabled: boolean; time: string; timezone: string }
  cost_summary?: CostSummary | null
}

export interface StatsFilterOptions {
  channel_names: string[]
  models: string[]
}

export interface StatsFilters {
  range: DashboardRange
  channel_name?: string
  model?: string
  cost_source?: CostSource | ''
  cost_status?: CostStatus | ''
  cost_basis?: CostBasis | ''
}

export interface ChannelTestResult {
  success: boolean
  key_health?: APIKeyHealth
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
  // 仅出现在启停 PATCH 的响应里，用于提示连带影响了多少账号与渠道。
  cascade?: SiteCascadeResult
  timezone: string
  use_system_proxy: boolean
  proxy_url?: string
  external_checkin_url?: string
  tags_json: string
  last_probe_status: string
  last_error?: string
  // 站点公开状态端点报告的签到能力（unknown/available/disabled/turnstile）。
  // 站点级事实，探测一次后由所有账号共享；空表示尚未探测到。
  checkin_method?: string
  checkin_method_checked_at?: number
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
  latest_checkins?: Record<number, CheckinAttempt>
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

// 站点启停时连带停用/恢复的账号与渠道数量，仅在真的发生级联时返回。
export interface SiteCascadeResult {
  enabled: boolean
  accounts: number
  channels: number
}

export interface ModelAliasCandidate {
  model: string
  channel_count: number
  channel_names: string[]
  mapped_to?: string
  normalized_key: string
}

export interface ModelAliasSuggestion {
  canonical: string
  members: string[]
  reason: string
  extends_canonical?: string
}

export interface ModelAliasInventory {
  candidates: ModelAliasCandidate[]
  suggestions: ModelAliasSuggestion[]
  total_models: number
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

export interface SiteModelPrice {
  model: string
  quota_type: number
  per_call_price: number
  model_ratio: number
  completion_ratio: number
  cache_ratio: number
  cache_creation_ratio: number
  groups: string[]
  input_price: number
  output_price: number
  cache_read_price: number
  cache_write_price: number
}

export interface SitePricingSnapshot {
  site_id: number
  available: boolean
  models: SiteModelPrice[]
  group_ratio: Record<string, number>
}

export interface SiteTask {
  id: string
  kind: string
	site_id?: number
	site_account_id?: number
  status: 'queued' | 'running' | 'success' | 'partial' | 'failed' | 'cancelled' | 'unknown'
  progress?: { completed?: number; total?: number; items?: { site_id: number; task_id?: string; status: string; error?: string }[]; routes?: { name: string; status: 'updated' | 'unchanged' | 'failed' | 'deactivated'; error?: string }[] }
  budget?: { active: number; waiting: number; cooldown_until?: number; next_request_at?: number } | null
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
  pricing_group?: string
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
	telegram_enabled: boolean
	telegram_configured: boolean
	telegram_chat_masked?: string
	telegram_use_system_proxy: boolean
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
  allowed_protocols?: string[]
  channel_restriction_mode?: 'allow' | 'deny'
  max_concurrency: number
}

export interface AuthTokenList {
  tokens: AuthToken[]
  duration_seconds?: number
  rpm_stats?: RPMStats
  is_today: boolean
}

export interface SystemAccessToken {
  id: number
  token_hint: string
  token?: string
  description: string
  scopes: string[]
  created_at: number
  last_used_at?: number
  expires_at: number
  is_active: boolean
}

export interface SystemAccessTokenList {
  tokens: SystemAccessToken[]
  scopes: string[]
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
  runtime_effect?: string
  requires_restart: boolean
}

export interface SystemSettingMutationResult {
  message: string
  restart_required: boolean
  key?: string
  value?: string
}

export type BackupType = 'all' | 'connections' | 'settings'

export interface BackupDocument {
  version: string
  exported_at: number
  type: BackupType
  warnings?: string[]
  [key: string]: unknown
}

export interface BackupImportResult {
  sites: number
  accounts: number
  channels: number
  bindings?: number
  tokens: number
  settings: number
  restart_required: boolean
}

export interface BackupVerification {
  verified_at: number
  bytes: number
  sha256: string
  type: BackupType
  warning_count: number
}

export interface BackupExportResult {
  status: string
  file_url: string
  last_sync_at: number
  verification?: BackupVerification
  metadata_saved?: boolean
  warnings?: string[]
}

export interface BackupWebDAVConfig {
  id: number
  enabled: boolean
  file_url: string
  username: string
  password_configured: boolean
  password_masked?: string
  export_type: BackupType
  auto_sync_enabled: boolean
  auto_sync_interval_hours: number
  last_sync_at?: number
  last_verified_at?: number
  last_verified_bytes?: number
  last_verified_sha256?: string
  last_verified_type?: BackupType | ''
  last_verified_warning_count?: number
  warnings?: string[]
  last_error?: string
  created_at?: number
  updated_at?: number
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
  request_id?: string
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
