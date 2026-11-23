import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  canApplyPreview,
  configPreviewEntityLabel,
  configPreviewFieldLabel,
  configPreviewErrorMessage,
  configPreviewMessageLabel,
  configPreviewReadSignal,
  configPreviewStatusLabel,
  configPreviewWarningLabel,
  MAX_CONFIG_PREVIEW_ACCOUNTS,
  discardConfigPreview,
  filterPreviewItems,
  mapWithConcurrency,
  markPreviewObservationUnknown,
  mergePreviewApplyResult,
  normalizeConfigPreview,
  normalizeConfigPreviewApplyResult,
  previewActionLabel,
  previewExpiresAt,
  safeConfigPreviewLocation,
  summarizePreviewItems,
  syncPreviewReference,
  toggleSitePreviewAccount,
  addSitePreviewAccounts,
  type ConfigPreview,
  type ConfigPreviewItem,
} from './configPreview.ts'

const items: ConfigPreviewItem[] = [
  { key: 'site:1', entity: 'site', label: '站点 A', action: 'create', fields: ['name'], preserved_fields: [], message: '' },
  { key: 'account:2', entity: 'account', label: '账号 B', action: 'update', fields: ['credential', 'label'], preserved_fields: ['timezone'], message: '凭证来自备份' },
  { key: 'channel:3', entity: 'channel', label: '渠道 C', action: 'unchanged', fields: [], preserved_fields: ['priority'], message: '' },
  { key: 'channel:4', entity: 'channel', label: '手工渠道', action: 'preserve', fields: [], preserved_fields: ['models'], message: '保留手动所有权' },
  { key: 'token:5', entity: 'token', label: '令牌冲突', action: 'conflict', fields: ['quota'], preserved_fields: [], message: '身份重复' },
  { key: 'channel:6', entity: 'channel', label: '旧渠道', action: 'deactivate', fields: ['enabled'], preserved_fields: [], message: '' },
]

const preview = (overrides: Partial<ConfigPreview> = {}): ConfigPreview => {
  const previewItems = items.filter((item) => item.action !== 'conflict')
  return {
    id: 'cp_1',
    kind: 'backup',
    expires_at: 2_000_000_000_000,
    can_apply: true,
    items: previewItems,
    summary: summarizePreviewItems(previewItems),
    status: 'ready',
    ...overrides,
  }
}

test('状态与动作使用稳定中文标签', () => {
  assert.equal(configPreviewStatusLabel('ready'), '待确认')
  assert.equal(configPreviewStatusLabel('applying'), '正在应用')
  assert.equal(configPreviewStatusLabel('applied'), '已应用')
  assert.equal(configPreviewStatusLabel('failed'), '失败')
  assert.equal(configPreviewStatusLabel('stale'), '已失效')
  assert.equal(configPreviewStatusLabel('unknown'), '结果待确认')
  assert.equal(previewActionLabel('preserve'), '保留')
})

test('绑定预览能按安全引用位置解释冲突和人工接管', () => {
  assert.equal(configPreviewEntityLabel('binding'), '渠道绑定')
  assert.notEqual(configPreviewFieldLabel('site_account_id'), '其他字段')
  assert.notEqual(configPreviewFieldLabel('projection_key'), '其他字段')
  assert.equal(safeConfigPreviewLocation('bindings[2]'), 'bindings[2]')
  assert.equal(safeConfigPreviewLocation('bindings[2]: synthetic-private-response'), '')
  for (const code of ['invalid_binding_reference', 'invalid_binding', 'binding_channel_conflict', 'binding_manual_restore']) {
    assert.notEqual(configPreviewMessageLabel(code), '需要核对此条目')
    assert.ok(!configPreviewErrorMessage(new Error(`${code}: bindings[2]`)).includes(code))
  }
})

test('只有待确认、允许应用、未过期且无冲突的预览可以应用', () => {
  assert.equal(canApplyPreview(preview(), 1_000_000_000_000), true)
  assert.equal(canApplyPreview(preview({ status: 'applying' }), 1_000_000_000_000), false)
  assert.equal(canApplyPreview(preview({ status: 'unknown' }), 1_000_000_000_000), false)
  assert.equal(canApplyPreview(preview({ can_apply: false }), 1_000_000_000_000), false)
  assert.equal(canApplyPreview(preview({ status: 'stale' }), 1_000_000_000_000), false)
  assert.equal(canApplyPreview(preview({ error: 'apply_failed: channels[2]' }), 1_000_000_000_000), false)
  assert.equal(canApplyPreview(preview({ expires_at: 999_000_000_000 }), 1_000_000_000_000), false)
  assert.equal(canApplyPreview(preview({ items: [items[4]], summary: summarizePreviewItems([items[4]]) }), 1_000_000_000_000), false)
})

test('过期时间兼容毫秒、秒与 ISO 时间', () => {
  assert.equal(previewExpiresAt(2_000_000_000_000), 2_000_000_000_000)
  assert.equal(previewExpiresAt(2_000_000_000), 2_000_000_000_000)
  assert.equal(previewExpiresAt('2033-05-18T03:33:20Z'), 2_000_000_000_000)
  assert.equal(previewExpiresAt(0), undefined)
})

test('摘要按动作计数且筛选同时匹配字段与说明', () => {
  assert.deepEqual(summarizePreviewItems(items), {
    create: 1,
    update: 1,
    unchanged: 1,
    preserve: 1,
    conflict: 1,
    deactivate: 1,
  })
  assert.deepEqual(filterPreviewItems(items, 'priority', 'unchanged').map((item) => item.key), ['channel:3'])
  assert.deepEqual(filterPreviewItems(items, '手动', 'preserve').map((item) => item.key), ['channel:4'])
  assert.deepEqual(filterPreviewItems(items, '凭证类型', 'update').map((item) => item.key), ['account:2'])
  assert.deepEqual(filterPreviewItems(items, '身份重复', 'conflict').map((item) => item.key), ['token:5'])
  assert.deepEqual(filterPreviewItems(items, '', 'conflict').map((item) => item.key), ['token:5'])
})

test('API 边界把 null 数组统一规范为空数组', () => {
  const normalized = normalizeConfigPreview({
    ...preview({ status: 'applied' }),
    items: null,
    error: 'provider-secret raw backend text',
  } as unknown as ConfigPreview)
  assert.deepEqual(normalized.items, [])
  assert.equal(normalized.error, 'apply_failed')

  const normalizedItems = normalizeConfigPreview({
    ...preview({ status: 'applied' }),
    items: [{
      ...items[0],
      fields: null,
      preserved_fields: null,
    }],
    result: {
      preview_id: 'cp_1',
      status: 'applied',
      items: null,
      warning: 'cache_sync_degraded',
    },
  } as unknown as ConfigPreview)
  assert.deepEqual(normalizedItems.items[0].fields, [])
  assert.deepEqual(normalizedItems.items[0].preserved_fields, [])
  assert.deepEqual(normalizedItems.result?.items, [])
  assert.equal(normalizedItems.result?.warning, 'cache_sync_degraded')

  const normalizedResult = normalizeConfigPreviewApplyResult({
    preview_id: 'cp_2',
    status: 'applied',
    items: [{ ...items[1], fields: null, preserved_fields: null }],
  } as never)
  assert.deepEqual(normalizedResult.items[0].fields, [])
  assert.deepEqual(normalizedResult.items[0].preserved_fields, [])
})

test('浏览器预览保留安全上下文和结果，丢弃意外的凭证字段', () => {
  const input = { ...preview(), kind: 'browser_import', browser_import: {
    format: 'pivotflow-browser-bridge', source_origin: 'https://source.example', base_url: 'https://source.example/api', site_name: 'Source', account_label: 'demo', credential_type: 'api_key', captured_at: 0, target_site_id: 1, target_account_id: 2, target_site_name: 'Source', target_account_label: 'demo', mode: 'verify', credential: 'synthetic-private-preview-value',
    verification: { status: 'verified', scope: 'models', checked_at: 123, identity_known: false, user_id: 42, model_count: 3, model_count_known: true, routing_key_available: true },
  }, result: { preview_id: 'cp_1', status: 'applied', items: [], browser_import: { site_id: 1, account_id: 2, action: 'create', verified: true, credential: 'synthetic-private-preview-value' } } }
  const normalized = normalizeConfigPreview(input as unknown as ConfigPreview)
  assert.equal(normalized.kind, 'browser_import')
  assert.ok(!JSON.stringify(normalized).includes('synthetic-private-preview-value'))
  assert.equal((normalized as typeof input).browser_import.verification.user_id, undefined)
  assert.equal((normalized as typeof input).result.browser_import.account_id, 2)
})

test('字段、消息、实体与告警使用中文标签，未知代码不原样显示', () => {
  assert.equal(configPreviewFieldLabel('base_url'), '站点地址')
  assert.equal(configPreviewFieldLabel('credential_type'), '凭证类型')
  assert.equal(configPreviewFieldLabel('credentials'), '凭证')
  assert.equal(configPreviewFieldLabel('oauth_credential'), 'OAuth 凭证')
  assert.equal(configPreviewFieldLabel('api_keys'), 'API Key')
  assert.equal(configPreviewFieldLabel('value'), '设置值（已隐藏）')
  assert.equal(configPreviewFieldLabel('id'), '标识')
  assert.equal(configPreviewFieldLabel('omitted_excludes'), '导入排除的凭证')
  assert.equal(configPreviewFieldLabel('ownership'), '管理归属')
  assert.equal(configPreviewFieldLabel('url'), '上游地址')
  assert.equal(configPreviewFieldLabel('api_key'), '上游 API Key')
  assert.equal(configPreviewFieldLabel('upstream'), '上游一致性')
  assert.equal(configPreviewFieldLabel('pricing_group'), '定价分组')
  for (const [field, label] of [
    ['runtime_state', '运行时状态'],
    ['historical_statistics', '历史统计'],
    ['request_history', '请求历史'],
    ['statistics', '运行统计'],
    ['balance_observations', '余额观测记录'],
    ['checkin_history', '签到历史'],
    ['usage_statistics', '用量统计'],
    ['existing_token', '已有令牌'],
    ['used_cost', '令牌已用额度'],
    ['last_used_at', '最后使用时间'],
    ['legacy_protocol_restrictions', '旧版协议限制'],
    ['current_setting', '当前设置值'],
    ['notification_history', '通知历史'],
    ['backup_sync_history', '备份同步历史'],
  ]) {
    assert.equal(configPreviewFieldLabel(field), label)
  }
  assert.equal(configPreviewEntityLabel('channel'), '渠道')
  assert.equal(configPreviewMessageLabel('credential_required'), '缺少登录凭证')
  assert.equal(
    configPreviewMessageLabel('account_disables_channel'),
    '账号停用或失效，将停用关联渠道',
  )
  assert.equal(configPreviewMessageLabel('unknown_backend_code'), '需要核对此条目')
  assert.equal(configPreviewWarningLabel('cache_sync_degraded'), '配置已提交，但缓存副本同步失败，请核对副本状态')
  assert.equal(
    configPreviewWarningLabel('token_reload_failed'),
    '配置已提交，但访问令牌权限重载失败；当前权限可能尚未生效，请核对后重新加载或重启服务',
  )
  assert.equal(
    configPreviewWarningLabel('cache_sync_degraded_token_reload_failed'),
    '配置已提交，但缓存副本同步和访问令牌权限重载均失败；当前权限可能尚未生效，请核对后重新加载或重启服务',
  )
  assert.equal(configPreviewWarningLabel('raw_backend_warning'), '配置已提交，但存在需要核对的缓存状态')
})

test('站点同步稳定消息代码全部使用中文标签', () => {
  for (const [code, label] of [
    ['manual_ownership', '手动管理的渠道保持不变'],
    ['discovery_incomplete', '模型发现未完成，已保留现有渠道且不会推断删除'],
    ['projection_create', '创建站点同步渠道'],
    ['projection_recreate', '重新创建缺失的站点同步渠道'],
    ['projection_update', '更新站点同步渠道'],
    ['projection_external_change', '渠道在绑定后发生外部变化，请重新核对'],
    ['upstream_unchanged', '渠道与上游一致'],
    ['removed_manual_preserved', '上游已移除该 Key；手动管理的渠道保持不变'],
    ['removed_already_inactive', '上游 Key 已移除，渠道已经停用'],
    ['removed_deactivate_history', '上游 Key 已移除；停用渠道并保留历史记录'],
  ]) {
    assert.equal(configPreviewMessageLabel(code), label)
  }
})

test('安全错误显示白名单位置并隐藏任意后端文本', () => {
  assert.equal(
    configPreviewErrorMessage(new Error('apply_failed: channels[2]')),
    '配置应用失败（位置：channels[2]）',
  )
  assert.equal(
    configPreviewErrorMessage(new Error('invalid_site: sites[12]')),
    '站点配置无效（位置：sites[12]）',
  )
  const unsafe = configPreviewErrorMessage(new Error('invalid_channel: <script>provider-secret</script>'))
  assert.equal(unsafe, '渠道配置无效')
  assert.doesNotMatch(unsafe, /script|provider-secret/)
  assert.equal(safeConfigPreviewLocation('sync[2]'), 'sync[2]')
  assert.equal(safeConfigPreviewLocation('accounts[12]'), 'accounts[12]')
  assert.equal(safeConfigPreviewLocation('channel:42'), 'channel:42')
  assert.equal(safeConfigPreviewLocation('models/account:7'), 'models/account:7')
  assert.equal(safeConfigPreviewLocation('sync[123456789]'), '')
  assert.equal(safeConfigPreviewLocation('sync[raw secret]'), '')
  assert.equal(
    configPreviewErrorMessage(new Error('apply_failed: sync[2]')),
    '配置应用失败（位置：sync[2]）',
  )
})

test('不确定请求后收到安全的 ready GET 状态即可恢复应用', () => {
  const pending = markPreviewObservationUnknown(preview())
  assert.equal(pending.status, 'unknown')
  assert.equal(pending.can_apply, false)
  assert.deepEqual(pending.items, preview().items)
  assert.equal(canApplyPreview(pending, 1_000_000_000_000), false)

  const observed = preview({ status: 'ready', can_apply: true, error: '' })
  assert.equal(canApplyPreview(observed, 1_000_000_000_000), true)
})

test('同步任务只接受 config_preview 引用', () => {
  assert.equal(syncPreviewReference('config_preview:cp_123'), 'cp_123')
  assert.equal(syncPreviewReference(' config_preview:abc:def '), 'abc:def')
  assert.equal(syncPreviewReference('model_refresh:1'), '')
  assert.equal(syncPreviewReference(undefined), '')
})

test('应用结果合并后保留原预览元数据并更新状态与条目', () => {
  const original = preview()
  const nextItems = [{ ...items[0], action: 'unchanged' as const, fields: [] }]
  const merged = mergePreviewApplyResult(original, { preview_id: original.id, status: 'applied', items: nextItems })
  assert.equal(merged.id, original.id)
  assert.equal(merged.kind, original.kind)
  assert.equal(merged.expires_at, original.expires_at)
  assert.equal(merged.status, 'applied')
  assert.equal(merged.can_apply, false)
  assert.deepEqual(merged.items, nextItems)
})

test('并发映射最多运行指定数量的任务并保持结果顺序', async () => {
  let active = 0
  let maxActive = 0
  const results = await mapWithConcurrency([1, 2, 3, 4, 5, 6], async (value) => {
    active += 1
    maxActive = Math.max(maxActive, active)
    await new Promise((resolve) => setTimeout(resolve, 5))
    active -= 1
    return value * 2
  }, 3)
  assert.equal(maxActive, 3)
  assert.deepEqual(results, [2, 4, 6, 8, 10, 12])
})

test('预览错误翻译为可操作提示且不泄露未知原始错误', () => {
  assert.match(configPreviewErrorMessage(new Error('preview_expired')), /过期/)
  assert.match(configPreviewErrorMessage(new Error('config_preview_stale: fingerprint changed')), /重新生成预览/)
  assert.match(configPreviewErrorMessage(new Error('preview_conflict')), /冲突/)
  assert.match(configPreviewErrorMessage(new Error('observation_interrupted')), /待确认/)
  assert.equal(configPreviewErrorMessage(new Error('provider token super-secret failed')), '配置预览失败，请重新生成预览后重试')
})

test('WebDAV 稳定错误码使用可操作中文提示', () => {
  for (const [code, label] of [
    ['webdav_not_configured', '尚未启用或配置 WebDAV，请先保存连接设置'],
    ['credential_locked', 'WebDAV 凭证无法读取，请检查服务器加密密钥配置'],
    ['webdav_request_failed', '无法连接 WebDAV，请检查地址、网络和凭据'],
    ['backup_too_large', '备份文件超过大小限制，请缩小备份范围后重试'],
    ['invalid_backup_document', '备份文件内容无效'],
    ['snapshot_failed', '无法读取当前配置快照，请稍后重试'],
    ['webdav_http_401', 'WebDAV 认证失败，请检查用户名和密码'],
    ['webdav_http_403', 'WebDAV 拒绝访问，请检查账号权限'],
    ['webdav_http_404', 'WebDAV 地址或备份文件不存在，请检查路径'],
    ['webdav_http_405', 'WebDAV 服务器不允许该请求，请检查地址是否指向文件或目录'],
    ['webdav_http_409', 'WebDAV 路径冲突，请检查同名文件或目录'],
    ['webdav_http_423', 'WebDAV 文件已锁定，请稍后重试或解除锁定'],
    ['webdav_http_500', 'WebDAV 服务器内部错误，请稍后重试'],
    ['webdav_http_502', 'WebDAV 网关错误，请检查服务地址或稍后重试'],
    ['webdav_http_504', 'WebDAV 网关超时，请检查服务地址或稍后重试'],
    ['webdav_http_507', 'WebDAV 存储空间不足，请清理空间后重试'],
  ]) {
    assert.equal(configPreviewErrorMessage(new Error(code)), label)
  }
})

test('COMMIT 结果不明显示为终态并提示人工复查', () => {
  const unknown = normalizeConfigPreview(preview({
    status: 'unknown',
    can_apply: false,
    error: 'commit_unknown',
  } as never))
  assert.equal(unknown.status, 'unknown')
  assert.equal(canApplyPreview(unknown, 1_000_000_000_000), false)
  assert.equal(configPreviewMessageLabel('commit_unknown'), '提交结果待确认，请人工核对当前配置')
  assert.equal(
    configPreviewErrorMessage(new Error('commit_unknown')),
    '提交结果待确认，请人工核对当前配置',
  )
})

test('已知失败只说明失败，不断言配置已经回滚', () => {
  const message = configPreviewErrorMessage(new Error('apply_failed: settings[2]'))
  assert.equal(message, '配置应用失败（位置：settings[2]）')
  assert.doesNotMatch(message, /回滚|未写入|已写入/)
})

test('释放配置预览使用 DELETE 和编码后的预览标识', async () => {
  const originalFetch = globalThis.fetch
  const originalLocalStorage = (globalThis as unknown as { localStorage?: unknown }).localStorage
  let request: { path: string; init?: RequestInit } | undefined
  ;(globalThis as unknown as {
    localStorage: { getItem: (key: string) => string | null }
  }).localStorage = {
    getItem: (key: string) => key.includes('token') ? 'test-token' : '0',
  }
  globalThis.fetch = async (input, init) => {
    request = { path: String(input), init }
    return new Response(JSON.stringify({ success: true, data: { deleted: true } }), {
      status: 200,
      headers: { 'content-type': 'application/json' },
    })
  }
  try {
    await discardConfigPreview('cp/a b')
  } finally {
    globalThis.fetch = originalFetch
    ;(globalThis as unknown as { localStorage?: unknown }).localStorage = originalLocalStorage
  }
  assert.equal(request?.path, '/admin/config-previews/cp%2Fa%20b')
  assert.equal(request?.init?.method, 'DELETE')
  assert.equal(request?.init?.body, undefined)
})

test('配置预览读取信号同时响应调用方取消和超时', async () => {
  const caller = new AbortController()
  const combined = configPreviewReadSignal(caller.signal, 10_000)
  caller.abort()
  assert.equal(combined.aborted, true)

  const timed = configPreviewReadSignal(undefined, 5)
  await new Promise((resolve) => setTimeout(resolve, 25))
  assert.equal(timed.aborted, true)
})

test('站点账号多选最多保留 16 个', () => {
  assert.equal(MAX_CONFIG_PREVIEW_ACCOUNTS, 16)
  const full = new Set(Array.from({ length: MAX_CONFIG_PREVIEW_ACCOUNTS }, (_, index) => index + 1))
  assert.deepEqual(toggleSitePreviewAccount(full, 17), { selected: full, limited: true })
  assert.deepEqual([...toggleSitePreviewAccount(full, 3).selected], [
    1, 2, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
  ])

  const added = addSitePreviewAccounts(new Set([1, 2]), [2, 3, 4, 5], 4)
  assert.deepEqual([...added.selected], [1, 2, 3, 4])
  assert.equal(added.limited, true)
})
