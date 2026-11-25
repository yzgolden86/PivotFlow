export interface ModelRouteScope { model: string; clientProtocol: string; tokenId: string; targetChannelId?: string; error?: string }

export const routeProtocols = [
  { value: 'openai', label: 'OpenAI' }, { value: 'anthropic', label: 'Anthropic' },
  { value: 'codex', label: 'Codex' }, { value: 'gemini', label: 'Gemini' },
]

export function validRouteText(value: string, maximum: number): boolean {
  return Array.from(value).length <= maximum && !/[\p{Cc}\p{Cs}]/u.test(value)
}

export function boundedRouteText(value: string, maximum: number): string {
  return Array.from(value).slice(0, maximum).join('')
}

export function validObjectId(value: string): boolean {
  return /^[1-9]\d*$/.test(value) && Number.isSafeInteger(Number(value))
}

export function repeatedScopeParameter(params: URLSearchParams): boolean {
  return [...params.keys()].some((key) => params.getAll(key).length > 1)
}

export function validRouteQuery(search: string): boolean {
  // URLSearchParams repairs malformed percent escapes/UTF8. Validate the raw
  // query first so an exact request name cannot silently change on navigation.
  if (search.includes(';')) return false
  try {
    for (const part of search.replace(/^\?/, '').split('&')) {
      decodeURIComponent(part.replace(/\+/g, ' '))
    }
    return true
  } catch { return false }
}

export function parseModelRouteScope(search: string): ModelRouteScope {
  const params = new URLSearchParams(search)
  const scope: ModelRouteScope = { model: params.get('model') || '', clientProtocol: params.get('client_protocol') ?? 'openai', tokenId: params.get('token_id') || '', error: '' }
  if (params.has('target_channel_id')) scope.targetChannelId = params.get('target_channel_id') || ''
  if (!validRouteQuery(search)) scope.error = '路由参数编码无效，请重新选择请求模型。'
  else if (repeatedScopeParameter(params)) scope.error = '路由范围包含重复参数，请重新选择。'
  else if (!validRouteText(scope.model, 512)) scope.error = '请求模型最多 512 字符，且不能包含控制字符。'
  else if (!routeProtocols.some((item) => item.value === scope.clientProtocol)) scope.error = '客户端协议无效，请重新选择。'
  else if (params.has('token_id') && !validObjectId(scope.tokenId)) scope.error = '所选令牌 ID 无效，请明确选择令牌或全局范围。'
  else if (params.has('target_channel_id') && !validObjectId(scope.targetChannelId || '')) scope.error = '目标渠道 ID 无效，请重新选择渠道。'
  return scope
}

export function modelRouteURL(scope: ModelRouteScope): string {
  const params = new URLSearchParams({ client_protocol: scope.clientProtocol })
  if (scope.model) params.set('model', scope.model)
  if (scope.tokenId) params.set('token_id', scope.tokenId)
  if (scope.targetChannelId !== undefined) params.set('target_channel_id', scope.targetChannelId)
  return `/routing?${params}`
}

export function requestHistoryLink(scope: ModelRouteScope): { href: string; exact: boolean; explanation: string } {
  const params = new URLSearchParams({ view: 'requests', range: 'today', client_protocol: scope.clientProtocol })
  if (scope.tokenId) params.set('auth_token_id', scope.tokenId)
  const exact = Boolean(scope.model) && validRouteText(scope.model, 191)
  if (exact) params.set('model', scope.model)
  return {
    href: scope.error ? '' : `/logs?${params}`,
    exact,
    explanation: exact ? '按原始请求名、客户端协议和当前令牌范围筛选；当前别名关系不会改写过去的身份。' : '请求历史保存的模型身份最多 191 字符。此名称无法精确查询，可查看当前协议与令牌范围的普通历史。',
  }
}

export function weightReferenceLabel(value: { candidate: boolean; estimated_traffic_share: number; higher_priority_count: number }): string {
  if (!value.candidate) return '未进入候选池'
  const percent = Number.isFinite(value.estimated_traffic_share) ? `${(value.estimated_traffic_share * 100).toFixed(1)}%` : '未知'
  return `${value.higher_priority_count > 0 ? '条件' : '同层'}权重参考 ${percent}`
}

export function modelInventoryStatus(value: { kind: 'model' | 'group' | 'wildcard'; alias_count: number; enabled_channels: number }): string {
  const channels = `${value.enabled_channels} 个启用渠道`
  if (value.kind === 'group') return `统一组 · ${value.alias_count} 个别名 · ${channels}`
  if (value.kind === 'wildcard') return `通配 · ${channels}`
  return channels
}

export function healthObservationLabel(value: { health_sample_count: number; success_rate: number }): string {
  if (!value.health_sample_count) return '无健康样本'
  const rate = Math.min(1, Math.max(0, value.success_rate))
  return `${(rate * 100).toFixed(1)}% · ${value.health_sample_count} 次样本`
}

export function attemptStateLabel(status: string): string {
  return ({ ready: '当前可尝试', blocked: '尝试被阻断', unknown: '状态待确认', probe: '提前试探' } as Record<string, string>)[status] || '状态待确认'
}

export function keyMultiplierLabel(value: { key_multiplier_count: number; key_multiplier_min: number | null; key_multiplier_max: number | null }): string {
  if (!value.key_multiplier_count || value.key_multiplier_min === null || value.key_multiplier_max === null) return '无适用 Key 倍率'
  return value.key_multiplier_min === value.key_multiplier_max ? `${value.key_multiplier_min}×` : `${value.key_multiplier_min}–${value.key_multiplier_max}×`
}

export function priceObservationLabel(status: string): string {
  return ({ not_bound: '未绑定站点', not_cached: '站点已绑定，价目未缓存', cached: '已有站点价目缓存', empty: '缓存价目没有模型价格', expired: '价目观察已过期', unavailable: '价目读取曾失败', unknown: '站点绑定观察不可用' } as Record<string, string>)[status] || '价目状态未知'
}

export function routeWarningLabel(code: string): string {
  return ({
    channel_cooldown_unavailable: '渠道冷却观察不可用，候选采用既有降级行为。',
    key_cooldown_unavailable: 'Key 冷却观察不可用，无法确认当前可尝试状态。',
    model_cooldown_unavailable: '模型冷却观察不可用，无法确认当前可尝试状态。',
    cost_observation_unavailable: '当前没有渠道日费用观察；有日上限的渠道状态待确认。',
    pricing_binding_unavailable: '站点绑定读取失败，费用依据显示为未知。',
    history_unavailable: '历史尝试读取失败；当前候选诊断仍可查看。',
    history_model_identity_too_long: '模型名超过历史的 191 字符存储边界，无法精确统计历史尝试。',
  } as Record<string, string>)[code] || `部分观察不可用（${code}）`
}

export function stickyForwardLabel(code: string): string {
  return ({ eligible: '符合基础优先级条件，可以沿用最近成功渠道。', base_priority_mismatch: '基础优先级与当前首层不同，粘性不能前移。', conditional_on_balanced_first: '首层包含不同基础优先级，能否前移取决于轮询后首位的基础优先级。', not_candidate: '最近成功渠道已不在当前候选池。', cooldown_fallback: '本次是冷却兜底，按最早恢复渠道提前试探。' } as Record<string, string>)[code] || '当前范围没有可用的粘性观察。'
}

export function tokenOptions<T extends { id: number; description?: string }>(tokens: T[], search: string, selected: string): T[] {
  const needle = search.trim().toLowerCase()
  const result = tokens.filter((token) => `${token.id} ${token.description || ''}`.toLowerCase().includes(needle)).slice(0, 50)
  const current = tokens.find((token) => String(token.id) === selected)
  if (current && !result.some((token) => token.id === current.id)) result.push(current)
  return result
}
