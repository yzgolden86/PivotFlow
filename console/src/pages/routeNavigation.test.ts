import assert from 'node:assert/strict'
import test from 'node:test'
import { modelRouteURL, parseModelRouteScope } from './modelRoutes.ts'
import { channelDiagnosisCloseURL, channelDiagnosisURL, channelDiagnosticState, channelEditorTarget, readChannelDiagnosis, updateModelRouteURL } from './routeNavigation.ts'

test('channel and model diagnosis links roundtrip exact request identity and frontend target context', () => {
  const scope = { model: ' vendor/模型🚦%_ +?&=# ', clientProtocol: 'codex', tokenId: '42' }
  const channelURL = channelDiagnosisURL(scope, '19')
  assert.equal(channelURL.split('?')[0], '/channels')
  assert.equal(new URLSearchParams(channelURL.split('?')[1]).get('diagnose_channel_id'), '19')
  const restored = readChannelDiagnosis(channelURL.slice(channelURL.indexOf('?')))
  assert.equal(restored.error, '')
  assert.equal(restored.channelId, '19')
  assert.equal(restored.scope.model, scope.model)
  assert.equal(restored.scope.clientProtocol, scope.clientProtocol)
  assert.equal(restored.scope.tokenId, scope.tokenId)
  const globalURL = modelRouteURL({ ...restored.scope, targetChannelId: restored.channelId })
  const globalScope = parseModelRouteScope(globalURL.slice(globalURL.indexOf('?')))
  assert.equal(globalScope.targetChannelId, '19')
  assert.equal(globalScope.model, scope.model)
  assert.equal(globalScope.tokenId, '42')
  assert.equal(channelDiagnosisURL(globalScope, '19'), channelURL)
  assert.equal(new URLSearchParams(globalURL.split('?')[1]).has('channel_id'), false)
  assert.equal(new URLSearchParams(channelURL.split('?')[1]).has('focus_channel_id'), false)
})

test('empty requested models remain empty instead of being replaced by a channel model', () => {
  const restored = readChannelDiagnosis('?diagnose_channel_id=7&model=&client_protocol=gemini&token_id=9')
  assert.equal(restored.error, '')
  assert.equal(restored.scope.model, '')
  assert.equal(restored.channelId, '7')
  assert.equal(channelDiagnosticState({ channelId: '7', model: '', loading: false }).status, 'empty')
  assert.equal(parseModelRouteScope(modelRouteURL({ ...restored.scope, targetChannelId: '7' }).split('?')[1]).model, '')
})

test('invalid and repeated IDs and scope fields block diagnosis rather than becoming global', () => {
  for (const id of ['', '0', '-1', '1.5', '0x10', '01', '1e2', '9007199254740992']) {
    assert.notEqual(readChannelDiagnosis(`?diagnose_channel_id=${id}&model=a`).error, '', id)
    assert.equal(channelDiagnosisURL({ model: 'a', clientProtocol: 'openai', tokenId: '' }, id), '', id)
    assert.notEqual(parseModelRouteScope(`?target_channel_id=${id}&model=a`).error, '', id)
  }
  for (const query of ['?diagnose_channel_id=1&diagnose_channel_id=2&model=a', '?diagnose_channel_id=1&model=a&model=b', '?diagnose_channel_id=1&model=a&token_id=', '?diagnose_channel_id=1&model=a&client_protocol=bad']) {
    assert.notEqual(readChannelDiagnosis(query).error, '', query)
  }
  assert.equal(readChannelDiagnosis('?search=a').requested, false)
  assert.equal(readChannelDiagnosis('?diagnose_channel_id=9007199254740991&model=a').channelId, '9007199254740991')
})

test('malformed escapes and UTF8 never repair the request model while encoded literal percent roundtrips', () => {
  for (const query of ['?model=%FF', '?model=%C0%AF', '?model=%E0%A4', '?model=%zz', '?model=%', '?model=a;b', '?%FF=a']) {
    assert.notEqual(parseModelRouteScope(query).error, '', query)
    assert.notEqual(readChannelDiagnosis(`${query}&diagnose_channel_id=1`).error, '', query)
  }
  for (const model of ['%FF', '%zz', 'literal% +中文', '�']) {
    const url = modelRouteURL({ model, clientProtocol: 'anthropic', tokenId: '' })
    const restored = parseModelRouteScope(url.slice(url.indexOf('?')))
    assert.equal(restored.error, '', model)
    assert.equal(restored.model, model)
  }
})

test('disabled and excluded targets stay observable; a stale snapshot never claims current eligibility', () => {
  assert.equal(channelDiagnosticState({ channelId: '4', model: 'alias', loading: false, target: { channel_id: 4, enabled: false }, stale: false }).status, 'disabled')
  assert.equal(channelDiagnosticState({ channelId: '4', model: 'alias', loading: false, target: { channel_id: 4, enabled: true }, stale: true, error: 'offline' }).status, 'stale')
  assert.equal(channelDiagnosticState({ channelId: '4', model: 'alias', loading: false, target: { channel_id: 4, enabled: true } }).status, 'ready')
})

test('unknown or mismatched target and token errors retain recoverable selected context', () => {
  const missing = channelDiagnosticState({ channelId: '999', model: 'alias%_', loading: false, error: 'routing_channel_not_found' })
  assert.equal(missing.status, 'not-found')
  assert.match(missing.message, /999/)
  assert.equal(channelDiagnosticState({ channelId: '4', model: 'alias', loading: false, target: { channel_id: 8, enabled: true } }).status, 'not-found')
  assert.equal(channelDiagnosticState({ channelId: '4', model: 'alias', loading: false, error: 'routing_token_not_found' }).status, 'error')
  assert.equal(readChannelDiagnosis('?diagnose_channel_id=999&model=alias%25_&token_id=21').scope.tokenId, '21')
})

test('malformed request queries require an explicit new model or reset before partial scope edits can read', () => {
  const query = '?model=%FF&client_protocol=openai&token_id=21'
  const scope = parseModelRouteScope(query)
  assert.equal(updateModelRouteURL(scope, { clientProtocol: 'codex' }, query), '')
  assert.equal(updateModelRouteURL(scope, { tokenId: '' }, query), '')
  const explicit = updateModelRouteURL(scope, { model: 'literal%FF' }, query)
  assert.equal(parseModelRouteScope(explicit.split('?')[1]).model, 'literal%FF')
  assert.equal(parseModelRouteScope(explicit.split('?')[1]).error, '')
  assert.equal(channelDiagnosisCloseURL('?diagnose_channel_id=7&model=%FF'), '/channels')
  assert.equal(channelDiagnosisCloseURL('?diagnose_channel_id=7&model=a%25b&token_id=21'), '/channels?model=a%25b&token_id=21')
})

test('read-only diagnosis takes precedence over a conflicting editor target', () => {
  assert.equal(channelEditorTarget('?focus_channel_id=1&diagnose_channel_id=1&model=a'), 0)
  assert.equal(channelEditorTarget('?focus_channel_id=1&diagnose_channel_id=bad&model=a'), 0)
  assert.equal(channelEditorTarget('?focus_channel_id=1&model=a'), 1)
  for (const query of ['?focus_channel_id=0x10', '?focus_channel_id=1&focus_channel_id=2', '?focus_channel_id=1&model=%FF']) assert.equal(channelEditorTarget(query), 0)
})

test('duplicated request identity stays blocked until every ambiguous field is explicitly replaced', () => {
  for (const [query, unrelated, replacement] of [
    ['?model=a&model=b&client_protocol=openai&token_id=21', { clientProtocol: 'codex' }, { model: 'chosen%_模型' }],
    ['?model=a&token_id=21&token_id=22&token_id=23', { model: 'new-model' }, { tokenId: '22' }],
    ['?model=a&client_protocol=openai&client_protocol=codex', { tokenId: '21' }, { clientProtocol: 'codex' }],
    ['?model=a&target_channel_id=4&target_channel_id=5', { model: 'new-model' }, { targetChannelId: '5' }],
    ['?model=a&target_channel_id=4&target_channel_id=4', { model: 'new-model' }, { targetChannelId: '4' }],
  ] as const) {
    const scope = parseModelRouteScope(query)
    assert.equal(updateModelRouteURL(scope, unrelated, query), '', query)
    const resolved = updateModelRouteURL(scope, replacement, query)
    assert.notEqual(resolved, '', query)
    const restored = parseModelRouteScope(resolved.split('?')[1])
    assert.equal(restored.error, '', query)
    for (const [key, value] of Object.entries(replacement)) assert.equal(restored[key as keyof typeof restored], value)
  }
  const both = '?model=a&model=b&token_id=21&token_id=22&token_id=23'
  const scope = parseModelRouteScope(both)
  assert.equal(updateModelRouteURL(scope, { model: 'selected' }, both), '')
  assert.equal(updateModelRouteURL(scope, { tokenId: '' }, both), '')
  const resolved = updateModelRouteURL(scope, { model: 'selected', tokenId: '' }, both)
  assert.equal(parseModelRouteScope(resolved.split('?')[1]).error, '')
  assert.equal(parseModelRouteScope(resolved.split('?')[1]).model, 'selected')
  assert.equal(new URLSearchParams(resolved.split('?')[1]).has('token_id'), false)
  const unknown = '?model=a&extra=1&extra=2'
  assert.equal(updateModelRouteURL(parseModelRouteScope(unknown), { model: 'selected' }, unknown), '')
})
