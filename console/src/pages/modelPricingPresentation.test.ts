import assert from 'node:assert/strict'
import { test } from 'node:test'
import { modelPricingFeedback, modelPricingFields, modelPricingLine, modelPricingSummary, type ModelPricingSummary } from './modelPricingPresentation.ts'
import type { SiteModelPrice, SitePricingSnapshot } from '../types.ts'

const token: ModelPricingSummary = {
  kind: 'token', sourceCount: 2, sourceSiteIds: [1, 2],
  inputPrices: [0, 1234.56], outputPrices: [0.0012, 9876.54],
  modelRatios: [0.1, 12.5], completionRatios: [1, 20], perCallPrices: [],
}

test('loading and unavailable prices reserve the same three labeled cells as ready prices', () => {
  assert.deepEqual(modelPricingFields(null, true), [
    { label: '输入', value: '加载中' },
    { label: '输出', value: '—' },
    { label: '倍率', value: '—' },
  ])
  assert.deepEqual(modelPricingFields(null, false), [
    { label: '输入', value: '暂无价目' },
    { label: '输出', value: '—' },
    { label: '倍率', value: '—' },
  ])
  assert.deepEqual(modelPricingFields(token, false), [
    { label: '输入', value: '$0.00–$1234.56/M' },
    { label: '输出', value: '$0.0012–$9876.54/M' },
    { label: '倍率', value: '0.1×–12.5×/1×–20×' },
  ])
})

test('partial or refreshing prices retain known values while other sources load', () => {
  assert.deepEqual(modelPricingFields(token, true), [
    { label: '输入', value: '$0.00–$1234.56/M' },
    { label: '输出', value: '$0.0012–$9876.54/M' },
    { label: '倍率', value: '0.1×–12.5×/1×–20×' },
  ])
})

test('call and mixed billing keep three cells and preserve full finite price ranges', () => {
  const call: ModelPricingSummary = { ...token, kind: 'call', sourceCount: 3,
    inputPrices: [], outputPrices: [], modelRatios: [], completionRatios: [], perCallPrices: [0.0025, 12.75, NaN] }
  assert.deepEqual(modelPricingFields(call, false), [
    { label: '计费', value: '按次' },
    { label: '价格', value: '$0.0025–$12.75' },
    { label: '来源', value: '3' },
  ])
  assert.equal(modelPricingLine(call), '按次 $0.0025–$12.75')
  const mixed: ModelPricingSummary = { ...token, kind: 'mixed', perCallPrices: [0.01, 99.5] }
  assert.deepEqual(modelPricingFields(mixed, false), [
    { label: '输入', value: '$0.00–$1234.56/M' },
    { label: '输出', value: '$0.0012–$9876.54/M' },
    { label: '按次', value: '$0.010–$99.50' },
  ])
  assert.equal(modelPricingLine(mixed), '输入 $0.00–$1234.56/M · 输出 $0.0012–$9876.54/M · 倍率 0.1×–12.5×/1×–20× · 按次 $0.010–$99.50')
})

test('small positive token and per-call prices remain nonzero in the cells and full tooltip text', () => {
  const small: ModelPricingSummary = { ...token, inputPrices: [0, 0.00001], outputPrices: [1e-9], perCallPrices: [0.00001], kind: 'mixed' }
  assert.deepEqual(modelPricingFields(small, false), [
    { label: '输入', value: '$0.00–$0.00001/M' },
    { label: '输出', value: '$1e-9/M' },
    { label: '按次', value: '$0.00001' },
  ])
  assert.equal(modelPricingLine(small), '输入 $0.00–$0.00001/M · 输出 $1e-9/M · 倍率 0.1×–12.5×/1×–20× · 按次 $0.00001')
  assert.equal(modelPricingFields({ ...small, kind: 'call', perCallPrices: [Number.MIN_VALUE] }, false)[1].value, '$5e-324')
})

test('failed source feedback stays scoped to displayed sites and distinguishes missing data from retained stale data', () => {
  const snapshots: Record<number, SitePricingSnapshot> = { 1: { site_id: 1, available: true, models: [], group_ratio: {} } }
  const feedback = modelPricingFeedback(token, [{ siteId: 1 }, { siteId: 2 }], snapshots, new Set([2]), { 1: 'failed' })
  assert.equal(feedback.pending, true)
  assert.equal(feedback.stale, true)
  assert.equal(feedback.state, 'stale')
  assert.match(feedback.line, /^旧价 · /)
  assert.match(feedback.description, /旧价目/)
  assert.match(feedback.description, /加载/)
  const fresh = modelPricingFeedback(token, [{ siteId: 2 }], snapshots, new Set(), { 1: 'failed', 2: 'ready' })
  assert.equal(fresh.failed, false)
  assert.equal(fresh.stale, false)
  assert.equal(fresh.state, 'ready')
  assert.doesNotMatch(fresh.description, /旧价目|失败/)
  const missing = modelPricingFeedback(null, [{ siteId: 3 }], snapshots, new Set(), { 3: 'failed' })
  assert.equal(missing.failed, true)
  assert.equal(missing.stale, false)
  assert.equal(missing.state, 'failed')
  assert.match(missing.description, /读取失败/)
})

test('retained unavailable prices stay stale during retries, and partial failures do not claim fresh prices are old', () => {
  const snapshots: Record<number, SitePricingSnapshot> = { 1: { site_id: 1, available: true, models: [], group_ratio: {} } }
  const stale = modelPricingFeedback(token, [{ siteId: 1 }], snapshots, new Set([1]), { 1: 'unavailable' })
  assert.equal(stale.state, 'stale')
  assert.equal(stale.pending, true)
  assert.match(stale.description, /暂不可用.*旧价目/)
  assert.match(stale.description, /加载/)
  assert.equal(modelPricingFields(token, true, stale.state)[0].label, '输入 · 旧')
  assert.equal(modelPricingFields(token, true, stale.state).length, 3)

  const partial = modelPricingFeedback(token, [{ siteId: 1 }, { siteId: 2 }], snapshots, new Set(), { 1: 'ready', 2: 'failed' })
  assert.equal(partial.state, 'failed')
  assert.equal(partial.stale, false)
  assert.match(partial.line, /^部分失败 · /)
  assert.doesNotMatch(partial.description, /旧价目/)
  assert.equal(modelPricingFields(token, false, partial.state)[0].label, '输入 · 缺')
  assert.equal(modelPricingFields(null, false, 'failed')[0].value, '读取失败')
  assert.equal(modelPricingFeedback(null, [{ siteId: 2 }], {}, new Set([2]), {}).state, 'loading')
  assert.equal(modelPricingFeedback(null, [{ siteId: 2 }], {}, new Set(), { 2: 'unavailable' }).state, 'unavailable')
})

const modelPrice = (overrides: Partial<SiteModelPrice> = {}): SiteModelPrice => ({
  model: 'synthetic-model', quota_type: 1, per_call_price: 0,
  model_ratio: 1, completion_ratio: 1, cache_ratio: 1, cache_creation_ratio: 1,
  groups: [], input_price: 1, output_price: 2, cache_read_price: 1, cache_write_price: 1,
  ...overrides,
})
const snapshot = (site_id: number, models: SiteModelPrice[]): SitePricingSnapshot => ({ site_id, available: true, models, group_ratio: {} })

test('explicitly unknown per-call prices contribute neither a free price nor a source; valid zero and legacy values remain usable', () => {
  const sources = [{ siteId: 1, group: 'default' }]
  assert.equal(modelPricingSummary('synthetic-model', sources, { 1: snapshot(1, [modelPrice({ per_call_price_valid: false })]) }), null)
  const knownFree = modelPricingSummary('synthetic-model', sources, { 1: snapshot(1, [modelPrice({ per_call_price_valid: true })]) })!
  assert.equal(knownFree.sourceCount, 1)
  assert.deepEqual(knownFree.perCallPrices, [0])
  assert.equal(modelPricingFields(knownFree, false)[1].value, '$0.00')
  const legacy = modelPricingSummary('synthetic-model', sources, { 1: snapshot(1, [modelPrice({ per_call_price: 0.25 })]) })!
  assert.deepEqual(legacy.perCallPrices, [0.25])
  const partial = modelPricingSummary('synthetic-model', [...sources, { siteId: 2, group: 'default' }], {
    1: snapshot(1, [modelPrice({ per_call_price_valid: false })]),
    2: snapshot(2, [modelPrice({ per_call_price_valid: true, per_call_price: 2.5 })]),
  })!
  assert.equal(partial.sourceCount, 1)
  assert.deepEqual(partial.perCallPrices, [2.5])
  assert.equal(modelPricingLine(partial), '按次 $2.50')
})

test('only finite nonnegative effective prices count as usable sources', () => {
  const sources = [{ siteId: 1, group: 'default' }]
  for (const invalid of [-1, NaN, Infinity]) {
    assert.equal(modelPricingSummary('synthetic-model', sources, { 1: snapshot(1, [modelPrice({ per_call_price: invalid })]) }), null)
    assert.equal(modelPricingSummary('synthetic-model', sources, { 1: snapshot(1, [modelPrice({ quota_type: 0, input_price: invalid })]) }), null)
    assert.equal(modelPricingSummary('synthetic-model', sources, { 1: { ...snapshot(1, [modelPrice()]), group_ratio: { default: invalid } } }), null)
  }
})

test('scoped summaries include a priced second account-only site when the representative site lacks that model price', () => {
  const sources = [{ siteId: 1, group: 'default' }, { siteId: 2, group: 'default' }]
  const snapshots = { 1: snapshot(1, []), 2: snapshot(2, [modelPrice({ per_call_price: 2.5 })]) }
  const all = modelPricingSummary('synthetic-model', sources, snapshots)!
  assert.deepEqual(all.perCallPrices, [2.5])
  assert.equal(all.sourceCount, 1)
  const feedback = modelPricingFeedback(all, sources, snapshots, new Set(), { 1: 'failed', 2: 'ready' })
  assert.equal(feedback.state, 'failed')
  assert.equal(feedback.stale, false)
  assert.doesNotMatch(feedback.line, /旧价/)
  assert.equal(modelPricingSummary('synthetic-model', sources.slice(0, 1), snapshots), null)
  assert.equal(modelPricingSummary('synthetic-model', sources.slice(1), snapshots)!.sourceCount, 1)
})
