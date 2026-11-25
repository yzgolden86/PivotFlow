import { modelRouteURL, parseModelRouteScope, repeatedScopeParameter, validObjectId, validRouteQuery, type ModelRouteScope } from './modelRoutes.ts'

export function channelDiagnosisURL(scope: ModelRouteScope, channelId: string): string {
  const { targetChannelId: _target, ...request } = scope
  const query = modelRouteURL(request).split('?')[1]
  if (!validObjectId(channelId) || scope.error || parseModelRouteScope(query).error) return ''
  const params = new URLSearchParams(query)
  params.set('diagnose_channel_id', channelId)
  return `/channels?${params}`
}

export function readChannelDiagnosis(search: string) {
  const params = new URLSearchParams(search)
  const scope = parseModelRouteScope(search)
  const requested = params.has('diagnose_channel_id')
  const channelId = params.get('diagnose_channel_id') || ''
  const error = scope.error || (requested && !validObjectId(channelId) ? '诊断渠道 ID 无效，请重新选择渠道。' : '')
  return { requested, channelId, scope, error }
}

export function updateModelRouteURL(scope: ModelRouteScope, patch: Partial<ModelRouteScope>, search: string): string {
  if (!validRouteQuery(search) && !Object.prototype.hasOwnProperty.call(patch, 'model')) return ''
  const params = new URLSearchParams(search)
  const scopeFields = new Map<string, keyof ModelRouteScope>([
    ['model', 'model'], ['client_protocol', 'clientProtocol'],
    ['token_id', 'tokenId'], ['target_channel_id', 'targetChannelId'],
  ])
  // Serializing a parsed scope drops duplicates. Only an explicit replacement
  // of every ambiguous identity may resolve them; unrelated edits must block.
  for (const key of params.keys()) {
    if (params.getAll(key).length <= 1) continue
    const field = scopeFields.get(key)
    if (!field || !Object.prototype.hasOwnProperty.call(patch, field)) return ''
  }
  return modelRouteURL({ ...scope, ...patch, error: '' })
}

export function channelDiagnosisCloseURL(search: string): string {
  if (!validRouteQuery(search)) return '/channels'
  const params = new URLSearchParams(search)
  if (params.has('diagnose_channel_id')) params.delete('focus_channel_id')
  params.delete('diagnose_channel_id')
  return `/channels?${params}`
}

export function channelEditorTarget(search: string): number {
  const params = new URLSearchParams(search)
  const id = params.get('focus_channel_id') || ''
  return !params.has('diagnose_channel_id') && validRouteQuery(search) && !repeatedScopeParameter(params) && validObjectId(id) ? Number(id) : 0
}

export function channelDiagnosticState(input: { channelId: string; model: string; loading: boolean; stale?: boolean; error?: string; target?: { channel_id: number; enabled: boolean } }) {
  if (!input.model.trim()) return { status: 'empty', message: '请输入请求模型后查看渠道诊断。' }
  if (input.target && String(input.target.channel_id) !== input.channelId) return { status: 'not-found', message: `渠道 #${input.channelId} 的诊断身份不匹配，请重新读取当前渠道。` }
  if (input.stale && input.target) return { status: 'stale', message: `渠道快照已过时：${input.error || '请重新读取当前范围。'}` }
  if (input.loading) return { status: 'loading', message: '' }
  if (input.error?.includes('routing_channel_not_found')) {
    return { status: 'not-found', message: `渠道 #${input.channelId} 不存在或已删除，请返回渠道列表重新选择。` }
  }
  if (input.error) return { status: 'error', message: input.error.includes('routing_token_not_found') ? '所选令牌不存在或已删除，请重新选择观察范围。' : input.error }
  if (input.target?.enabled === false) return { status: 'disabled', message: '渠道已停用；仍可查看当前排除原因。' }
  return { status: 'ready', message: '' }
}
