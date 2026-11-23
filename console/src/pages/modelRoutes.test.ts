import assert from 'node:assert/strict'
import test from 'node:test'
import {
  attemptStateLabel, healthObservationLabel, keyMultiplierLabel, modelRouteURL,
  parseModelRouteScope, requestHistoryLink, routeWarningLabel, stickyForwardLabel,
  tokenOptions, weightReferenceLabel,
} from './modelRoutes.ts'
import { readRequestHistoryScope, writeRequestHistoryScope } from './requestHistory.ts'

test('model scope round trips exact names, protocol and token without expanding aliases', () => {
  const model = 'vendor/model%_ /中文?&'
  const href = modelRouteURL({ model, clientProtocol: 'codex', tokenId: '42' })
  assert.equal(href.startsWith('/routing?'), true)
  assert.deepEqual(parseModelRouteScope(href.slice(href.indexOf('?'))), { model, clientProtocol: 'codex', tokenId: '42', error: '' })
})

test('invalid or repeated selected scope never silently becomes global', () => {
  for (const query of ['?model=a&model=b', '?model=a&token_id=bad', '?model=a&token_id=0', '?model=a&token_id=', '?model=a&client_protocol=other', '?model=a%0Ab']) {
    assert.notEqual(parseModelRouteScope(query).error, '', query)
  }
  assert.notEqual(parseModelRouteScope(`?model=${'模'.repeat(513)}`).error, '')
  assert.equal(parseModelRouteScope(`?model=${'🚦'.repeat(512)}`).error, '')
})

test('history links preserve original model/protocol/token and disclose 191-character limit', () => {
  const scope = { model: 'model%_ /中文?&', clientProtocol: 'anthropic', tokenId: '8' }
  const exact = requestHistoryLink(scope)
  const params = new URLSearchParams(exact.href.split('?')[1])
  assert.equal(exact.exact, true)
  assert.equal(params.get('model'), scope.model)
  assert.equal(params.get('client_protocol'), 'anthropic')
  assert.equal(params.get('auth_token_id'), '8')
  assert.equal(params.get('view'), 'requests')
  assert.equal(requestHistoryLink({ ...scope, model: '🚦'.repeat(191) }).exact, true)
  const long = requestHistoryLink({ ...scope, model: '🚦'.repeat(192) })
  assert.equal(long.exact, false)
  assert.match(long.explanation, /191/)
  assert.equal(new URLSearchParams(long.href.split('?')[1]).has('model'), false)
  assert.equal(new URLSearchParams(long.href.split('?')[1]).get('auth_token_id'), '8')
})

test('weight labels report conditional references and never claim hit probabilities', () => {
  assert.equal(weightReferenceLabel({ candidate: true, estimated_traffic_share: .25, higher_priority_count: 2 }), '条件权重参考 25.0%')
  assert.equal(weightReferenceLabel({ candidate: true, estimated_traffic_share: .75, higher_priority_count: 0 }), '同层权重参考 75.0%')
  assert.equal(weightReferenceLabel({ candidate: false, estimated_traffic_share: 0, higher_priority_count: 0 }), '未进入候选池')
})

test('unknown health, attempts and Key fee range retain their distinct meanings', () => {
  assert.equal(healthObservationLabel({ health_sample_count: 0, success_rate: 1 }), '无健康样本')
  assert.equal(healthObservationLabel({ health_sample_count: 20, success_rate: .9 }), '90.0% · 20 次样本')
  assert.equal(attemptStateLabel('probe'), '提前试探')
  assert.equal(attemptStateLabel('unknown'), '状态待确认')
  assert.equal(attemptStateLabel('blocked'), '尝试被阻断')
  assert.equal(keyMultiplierLabel({ key_multiplier_count: 2, key_multiplier_min: 0, key_multiplier_max: 1.5 }), '0–1.5×')
  assert.equal(keyMultiplierLabel({ key_multiplier_count: 0, key_multiplier_min: null, key_multiplier_max: null }), '无适用 Key 倍率')
  assert.match(stickyForwardLabel('conditional_on_balanced_first'), /基础优先级/)
  assert.match(routeWarningLabel('history_unavailable'), /历史/)
})

test('token search covers the whole list but renders only a bounded preview', () => {
  const tokens = Array.from({ length: 151 }, (_, index) => ({ id: index + 1, description: `令牌 ${index + 1}` }))
  assert.equal(tokenOptions(tokens, '', '151').length, 51)
  assert.equal(tokenOptions(tokens, '151', '151').length, 1)
  assert.equal(tokenOptions(tokens, '151', '151')[0].id, 151)
  assert.equal(tokenOptions(tokens, '不存在', '151')[0].id, 151)
})

test('request history restores every filter after detail navigation or browser back', () => {
  const scope = { range: 'this_week' as const, authTokenId: '7', model: 'm%_ /中文', clientProtocol: 'codex', outcome: 'error' as const, search: 'pf_req_quoted', page: 3, error: '' }
  const query = writeRequestHistoryScope(scope, '?view=requests&request=pf_req_a')
  assert.deepEqual(readRequestHistoryScope(query), scope)
  assert.equal(new URLSearchParams(query).get('request'), 'pf_req_a')
  for (const bad of ['?model=x&model=y', `?model=${'模'.repeat(192)}`, '?client_protocol=other', '?auth_token_id=bad']) {
    assert.notEqual(readRequestHistoryScope(bad).error, '', bad)
  }
})
