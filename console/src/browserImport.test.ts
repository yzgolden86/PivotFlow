import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  browserImportErrorDetails, browserImportHref, browserImportNextLinks,
  browserImportOperationCurrent, browserImportRouteState, createBrowserImportPreview,
  decodeBrowserImportFile, inspectBrowserImport, newBrowserImportOperation, readBrowserImportCredential,
  rewriteBrowserImportCredential, validateBrowserImportText,
} from './browserImport.ts'

const secret = '{{SECRET_3yfjmkoo}}'
const document = (count = 1) => JSON.stringify({
  format: 'pivotflow-browser-bridge', version: '1.0', captured_at: 1795392000000,
  entries: Array.from({ length: count }, (_, index) => ({
    source_origin: 'https://source.example', base_url: 'https://source.example/api',
    site_name: 'Source', platform: 'new-api-family', account_label: `demo-${index}`,
    credential_type: 'access_token', credential: { access_token: secret, user_id: 42 },
  })),
})

test('已知文件版本和条数先检查，原始文本仍交给服务器', () => {
  assert.deepEqual(validateBrowserImportText(document(64)), { format: 'pivotflow-browser-bridge', count: 64 })
  for (const version of ['2.0', '3.0', '4.0']) {
    assert.deepEqual(validateBrowserImportText(JSON.stringify({ version, accounts: { accounts: [{}] } })), { format: 'all-api-hub', count: 1 })
  }
  assert.throws(() => validateBrowserImportText(document(65)), /条/)
  assert.throws(() => validateBrowserImportText('{}'), /版本|格式/)
  assert.throws(() => validateBrowserImportText('{broken'), /JSON|格式/)
})

test('文件字节数和编码后的请求独立受 2 MiB 限制', () => {
  assert.throws(() => validateBrowserImportText('名'.repeat(700_000)), /大小|2 MiB/)
  const large = document().replace(secret, '\\'.repeat(1_100_000))
  assert.throws(() => validateBrowserImportText(large), /大小|2 MiB/)
})

test('文件原始 UTF-8 损坏时拒绝导入，正常中文和重复键保持原样', async () => {
  const raw = document().replace('"site_name":"Source"', '"site_name":"来源","site_name":"Corrected"')
  assert.equal(await decodeBrowserImportFile(new Blob([raw])), raw)
  const prefix = new TextEncoder().encode('{"credential":"')
  const suffix = new TextEncoder().encode('"}')
  const damaged = new Blob([prefix, Uint8Array.of(0xff), suffix])
  await assert.rejects(decodeBrowserImportFile(damaged), /UTF-8|格式/)
  await assert.rejects(decodeBrowserImportFile(new Blob([new Uint8Array(2 * 1024 * 1024 + 1)])), /2 MiB|大小/)
})

test('恢复链接只携带正整数目标和安全预览 ID', () => {
  assert.equal(browserImportHref({ accountId: 101, siteId: 1, previewId: 'bpv-restored' }), '#/browser-import?account_id=101&site_id=1&preview_id=bpv-restored')
  assert.equal(browserImportHref({ accountId: -1, siteId: NaN, previewId: '../token?secret=' + secret } as never), '#/browser-import')
  assert.deepEqual(browserImportRouteState('?account_id=101&site_id=1&preview_id=bpv-restored&credential=ignore'), { accountId: 101, siteId: 1, previewId: 'bpv-restored' })
  assert.deepEqual(browserImportRouteState('?account_id=1.5&site_id=9007199254740992&preview_id=%2f'), { accountId: 0, siteId: 0, previewId: '' })
  const untrusted = '?account_id=101&site_id=1&preview_id=bpv-restored&credential=' + secret
  assert.equal(browserImportHref({ ...browserImportRouteState(untrusted), helper: true }), '#/browser-import?account_id=101&site_id=1&preview_id=bpv-restored&helper=1')
})

test('所有错误仅显示允许的中文原因，待确认编号单独校验', () => {
  const privateText = 'upstream echoed ' + secret
  assert.ok(!browserImportErrorDetails(new Error(privateText)).label.includes(secret))
  assert.equal(browserImportErrorDetails(new Error('browser_required: ' + privateText)).code, 'browser_required')
  assert.ok(!browserImportErrorDetails(new Error('browser_required: ' + privateText)).label.includes(privateText))
  assert.equal(browserImportErrorDetails(new Error('browser_import_pending: bpv-pending')).pendingPreviewId, 'bpv-pending')
  assert.equal(browserImportErrorDetails(new Error('browser_import_pending: ../' + secret)).pendingPreviewId, '')
})

test('取消与新输入使旧异步结果失效', () => {
  const first = newBrowserImportOperation(1)
  assert.equal(browserImportOperationCurrent(first, 1, first), true)
  const replacement = newBrowserImportOperation(2)
  assert.equal(browserImportOperationCurrent(first, 2, replacement), false)
  first.controller.abort()
  assert.equal(browserImportOperationCurrent(first, 1, first), false)
})

test('轻量文件修正只重写已选账号的已知凭证字段', () => {
  const original = document(2)
  const repaired = rewriteBrowserImportCredential(original, 0, { primary: secret + '-repaired', userId: '43', refreshToken: '', expiresAt: '' })
  assert.equal(readBrowserImportCredential(repaired, 0).primary, secret + '-repaired')
  assert.equal(readBrowserImportCredential(repaired, 1).primary, secret)
  assert.equal(readBrowserImportCredential(original, 0).primary, secret)
  assert.equal(JSON.parse(repaired).entries[0].credential.user_id, 43)
  assert.throws(() => rewriteBrowserImportCredential(original, 5, { primary: '', userId: '', refreshToken: '', expiresAt: '' }), /条目/)
})

test('Hub Cookie 与 Sub2API 修正保留忽略部分且不会改另一个账号', () => {
  const original = JSON.stringify({ version: '4.0', preferences: { theme: 'dark' }, accounts: { accounts: [
    { site_type: 'anyrouter', authType: 'cookie', account_info: { id: 42, username: 'cookie-user' }, cookieAuth: { sessionCookie: secret } },
    { site_type: 'sub2api', authType: 'access_token', account_info: { id: 43, access_token: secret }, sub2apiAuth: { refreshToken: 'synthetic-refresh', tokenExpiresAt: 123 } },
  ] } })
  const repaired = rewriteBrowserImportCredential(original, 0, { primary: 'session=repaired', userId: '44', refreshToken: '', expiresAt: '' })
  const parsed = JSON.parse(repaired)
  assert.equal(parsed.accounts.accounts[0].cookieAuth.sessionCookie, 'session=repaired')
  assert.equal(parsed.accounts.accounts[1].account_info.access_token, secret)
  assert.deepEqual(parsed.preferences, { theme: 'dark' })
  const sub = rewriteBrowserImportCredential(repaired, 1, { primary: secret + '-new', userId: '43', refreshToken: 'synthetic-refresh-new', expiresAt: '1234567890123' })
  assert.equal(readBrowserImportCredential(sub, 1).refreshToken, 'synthetic-refresh-new')
  assert.equal(readBrowserImportCredential(sub, 1).expiresAt, '1234567890123')
})

test('成功入口依赖同一预览的已应用结果，不使用输入里的账号编号', () => {
  assert.deepEqual(browserImportNextLinks({ status: 'unknown' } as never), [])
  const links = browserImportNextLinks({ status: 'applied', result: { status: 'applied', browser_import: { site_id: 1, account_id: 101, action: 'create', verified: false } } } as never)
  assert.ok(links.some((link) => link.href === '#/accounts?focus_account_id=101'))
  assert.ok(links.some((link) => link.href === '#/channels?sync_account_id=101'))
  assert.ok(links.some((link) => link.href === '#/connect'))
})

test('检查请求保持原文件文本与重复键，仅在请求体带入凭证', async (t) => {
  const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: { getItem: (key: string) => key === 'pivotflow_token' ? 'synthetic-browser-import-admin' : null } })
  t.after(() => { if (originalStorage) Object.defineProperty(globalThis, 'localStorage', originalStorage); else Reflect.deleteProperty(globalThis, 'localStorage') })
  const raw = document().replace('"site_name":"Source"', '"site_name":"Source","site_name":"Corrected"')
  let sent = false
  t.mock.method(globalThis, 'fetch', async (input: string, init: RequestInit) => {
    sent = true
    assert.equal(input, '/admin/browser-import/inspect')
    assert.equal(init.method, 'POST')
    assert.equal(JSON.parse(String(init.body)).document, raw)
    assert.ok(!input.includes(secret))
    return new Response(JSON.stringify({ success: true, data: { version: 1, format: 'pivotflow-browser-bridge', captured_at: 0, ignored_sections: [], entries: [] } }))
  })
  await inspectBrowserImport(raw)
  assert.equal(sent, true)
})

test('预览请求要求来源确认和有效索引，不会隐式提升成验证', async (t) => {
  const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: { getItem: (key: string) => key === 'pivotflow_token' ? 'synthetic-browser-import-admin' : null } })
  t.after(() => { if (originalStorage) Object.defineProperty(globalThis, 'localStorage', originalStorage); else Reflect.deleteProperty(globalThis, 'localStorage') })
  let calls = 0
  t.mock.method(globalThis, 'fetch', async (input: string, init: RequestInit) => {
    calls++
    assert.equal(input, '/admin/browser-import/preview')
    assert.equal(JSON.parse(String(init.body)).mode, 'save_disabled')
    return new Response(JSON.stringify({ success: true, data: { id: 'bpv-1', kind: 'browser_import', status: 'ready', items: [], expires_at: Date.now() + 600_000, can_apply: true, browser_import: {
      format: 'pivotflow-browser-bridge', source_origin: 'https://source.example', base_url: 'https://source.example/api', site_name: 'Source', account_label: 'demo', credential_type: 'access_token', captured_at: 0, target_site_id: 0, target_account_id: 0, target_site_name: 'Source', target_account_label: 'demo', mode: 'save_disabled', verification: { status: 'unverified', scope: 'none', checked_at: 0, identity_known: false, model_count: 0, model_count_known: false, routing_key_available: false },
    } } }))
  })
  const request = { document: document(), entry_index: 0, target_account_id: 0, site_name: 'Source', account_label: 'demo', mode: 'save_disabled' as const, source_confirmed: false }
  await assert.rejects(createBrowserImportPreview(request), /核对|确认/)
  await createBrowserImportPreview({ ...request, source_confirmed: true })
  assert.equal(calls, 1)
})
