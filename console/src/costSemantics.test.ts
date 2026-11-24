import assert from 'node:assert/strict'
import test from 'node:test'
import type { CostEntryLike, CostSummary } from './types.ts'
import {
  analyzeCostSummary,
  buildCostEntryView,
  costBasisLabel,
  costSourceLabel,
  costStatusLabel,
  parseCostDetails,
} from './costSemantics.ts'

function entry(overrides: Partial<CostEntryLike> = {}): CostEntryLike {
  return {
    status_code: 200,
    cost_metadata_version: 1,
    cost_status: 'estimated',
    cost_basis: 'token',
    cost_source: 'local_estimate',
    effective_cost: 0.0012,
    cost_details: {
      model_cost_usd: 0.0012,
      tool_cost_usd: 0,
      currency: 'USD',
    },
    ...overrides,
  }
}

function summary(overrides: Partial<CostSummary> = {}): CostSummary {
  return {
    version: 1,
    currency: 'USD',
    reconciliation: 'not_reconciled',
    successful_requests: 10,
    estimated_requests: 6,
    zero_requests: 2,
    local_free_requests: 1,
    partial_requests: 1,
    unknown_requests: 2,
    historical_requests: 1,
    site_pricing_requests: 6,
    local_estimate_requests: 4,
    mixed_requests: 2,
    recorded_cost_usd: 0.021,
    estimated_cost_usd: 0.014,
    partial_cost_usd: 0.004,
    historical_cost_usd: 0.003,
    ...overrides,
  }
}

test('known zero stays zero while an unknown zero is shown as an em dash', () => {
  const knownZero = buildCostEntryView(entry({ cost_status: 'zero_price', effective_cost: 0 }))
  assert.equal(knownZero.amountText, '$0.00')
  assert.equal(knownZero.knownZero, true)
  assert.equal(knownZero.incomplete, false)
  assert.match(knownZero.semanticLabel, /价目计算结果为零/)

  for (const status of ['usage_missing', 'unpriced_model', 'partial_estimate', 'invalid']) {
    const unknownZero = buildCostEntryView(entry({ cost_status: status, effective_cost: 0 }))
    assert.equal(unknownZero.amountText, '—', `${status} must never look like a known $0.00`)
    assert.equal(unknownZero.knownZero, false)
    assert.equal(unknownZero.incomplete, true)
  }
})

test('historical positive amounts remain visible but carry an incomplete-basis warning', () => {
  const view = buildCostEntryView(entry({
    cost_metadata_version: 0,
    cost_status: 'estimated',
    cost_source: 'site_pricing',
    effective_cost: 0.002,
  }))
  assert.equal(view.status, 'historical_unknown')
  assert.equal(view.amountText, '$0.0020')
  assert.equal(view.incomplete, true)
  assert.match(view.statusLabel, /历史记录/)
  assert.match(view.semanticLabel, /依据不完整/)
  assert.doesNotMatch(view.semanticLabel, /实扣|上游计算|已对账/)
  assert.equal(view.detailLabel, '', 'historical rows must not expose frozen details')
  assert.equal(view.basisLabel, '计费方式未知')

  const historicalZero = buildCostEntryView(entry({
    cost_metadata_version: 0,
    effective_cost: 0,
  }))
  assert.equal(historicalZero.amountText, '—')
  assert.equal(historicalZero.incomplete, true)

  const legacyFreeStatus = buildCostEntryView(entry({
    cost_metadata_version: 0,
    cost_status: 'free_model',
    effective_cost: 0.001,
  }))
  assert.equal(legacyFreeStatus.status, 'historical_unknown')
  assert.equal(legacyFreeStatus.amountText, '$0.0010')
  assert.doesNotMatch(legacyFreeStatus.semanticLabel, /free_model/)
})

test('partial site pricing plus a local tool is described as a combination', () => {
  const view = buildCostEntryView(entry({
    cost_status: 'partial_estimate',
    cost_source: 'site_pricing',
    cost_basis: 'mixed',
    effective_cost: 0.005,
    cost_details: {
      model_cost_usd: 0.004,
      tool_cost_usd: 0.001,
      currency: 'USD',
    },
  }))
  assert.equal(view.amountText, '$0.0050')
  assert.match(view.statusLabel, /部分估算/)
  assert.equal(view.sourceLabel, '站点价目表估算 + 本地工具估算')
  assert.equal(view.basisLabel, '多项费用组合')
  assert.match(view.detailLabel, /模型费用/)
  assert.match(view.detailLabel, /工具费用/)
  assert.doesNotMatch(view.semanticLabel, /实扣|上游计算|已对账/)
})

test('failed responses are not charged as successful requests even if a cost exists', () => {
  const view = buildCostEntryView(entry({
    status_code: 500,
    cost_status: 'estimated',
    effective_cost: 1.25,
  }))
  assert.equal(view.status, 'not_charged')
  assert.equal(view.amountText, '—')
  assert.match(view.semanticLabel, /未按成功请求记账/)
  assert.equal(view.detailLabel, '')
})

test('invalid and non-finite amounts fall back without echoing backend codes', () => {
  for (const value of [Number.NaN, Number.POSITIVE_INFINITY, Number.MAX_VALUE, -1]) {
    const view = buildCostEntryView(entry({ effective_cost: value }))
    assert.equal(view.amountText, '—')
    assert.equal(view.status, 'invalid')
    assert.match(view.semanticLabel, /费用数据无效/)
    assert.equal(view.detailLabel, '')
  }
  const invalidStatus = buildCostEntryView(entry({ cost_status: 'invalid', effective_cost: 0.002 }))
  assert.equal(invalidStatus.status, 'invalid')
  assert.equal(invalidStatus.amountText, '—')
  assert.equal(invalidStatus.detailLabel, '')
  const unknownCode = buildCostEntryView(entry({ cost_status: 'backend_private_code' }))
  assert.equal(unknownCode.status, 'historical_unknown')
  assert.equal(unknownCode.amountText, '$0.0012')
  assert.doesNotMatch(unknownCode.semanticLabel, /backend_private_code/)
})

test('backend-supported large amounts render consistently in rows and summaries', () => {
  const largeAmount = 10_000_000_000
  const row = buildCostEntryView(entry({ effective_cost: largeAmount }), 2)
  const aggregate = analyzeCostSummary(summary({
    recorded_cost_usd: largeAmount,
    estimated_cost_usd: largeAmount,
    partial_cost_usd: 0,
    historical_cost_usd: 0,
  }), 2)

  assert.equal(row.status, 'estimated')
  assert.equal(row.amount, largeAmount)
  assert.equal(row.amountText, '$10,000,000,000.00')
  assert.equal(aggregate.status, 'ready')
  assert.equal(aggregate.amountText, row.amountText)
})

test('version-one not_charged on a successful row is treated as historical evidence', () => {
  const view = buildCostEntryView(entry({
    cost_status: 'not_charged',
    effective_cost: 0.0015,
  }))
  assert.equal(view.status, 'historical_unknown')
  assert.equal(view.amountText, '$0.0015')
  assert.match(view.semanticLabel, /历史记录，依据不完整/)
})

test('source and basis labels preserve provenance without claiming reconciliation', () => {
  assert.equal(costStatusLabel('estimated'), '完整估算')
  assert.equal(costStatusLabel('local_free'), '本地记账免费（上游费用未确认）')
  assert.equal(costStatusLabel('not_charged'), '未按成功请求记账')
  assert.equal(costSourceLabel('site_pricing'), '站点价目表估算')
  assert.equal(costSourceLabel('local_estimate'), '本地公开价估算')
  assert.equal(costSourceLabel(''), '来源未记录')
  assert.equal(costBasisLabel('token'), '按 Token')
  assert.equal(costBasisLabel('per_call'), '按次')
  assert.equal(costBasisLabel('mixed'), '多项费用组合')
  assert.equal(costBasisLabel('unknown'), '计费方式未知')
  assert.doesNotMatch(costSourceLabel('site_pricing'), /实扣|上游计算|已对账/)
})

test('cost details are reconstructed from safe numbers only', () => {
  const details = parseCostDetails({
    model_cost_usd: 0.004,
    tool_cost_usd: 0.001,
    currency: 'USD',
    secret: 'must not escape',
    breakdown: {
      input: { price_per_million: 3, quantity: 1000, cost: 0.003 },
      output: { price_per_million: 15, quantity: 66, cost: 0.001 },
      cache_read: { price_per_million: 0.3, quantity: 0, cost: 0 },
      cache_write: { price_per_million: 3.75, quantity: 0, cost: 0 },
      total: 0.004,
      service_tier_multiplier: 1,
      raw_provider_payload: { authorization: 'not-safe' },
    },
  })
  assert.deepEqual(details, {
    model_cost_usd: 0.004,
    tool_cost_usd: 0.001,
    currency: 'USD',
    breakdown: {
      input: { price_per_million: 3, quantity: 1000, cost: 0.003 },
      output: { price_per_million: 15, quantity: 66, cost: 0.001 },
      cache_read: { price_per_million: 0.3, quantity: 0, cost: 0 },
      cache_write: { price_per_million: 3.75, quantity: 0, cost: 0 },
      total: 0.004,
      service_tier_multiplier: 1,
    },
  })
  assert.equal(parseCostDetails({ model_cost_usd: 0.004, tool_cost_usd: -1, currency: 'USD' }), null)
  assert.equal(parseCostDetails({ model_cost_usd: Number.MAX_VALUE, tool_cost_usd: 0, currency: 'USD' }), null)
})

test('summary exposes recorded cost and explicit incomplete coverage without double counting', () => {
  const view = analyzeCostSummary(summary(), 4)
  assert.equal(view.status, 'ready')
  assert.equal(view.amountText, '$0.0210')
  assert.equal(view.knownRequests, 8)
  assert.equal(view.unknownOrPartialRequests, 2)
  assert.equal(view.completeCoverage, false)
  assert.match(view.coverageText, /完整估算 8\/10/)
  assert.match(view.coverageText, /2 条待核对/)
  assert.equal(view.reconciliation, 'not_reconciled')
  assert.match(view.recordedBreakdown, /完整估算 \$0\.0140/)
  assert.match(view.recordedBreakdown, /部分 \$0\.0040/)
  assert.match(view.recordedBreakdown, /历史 \$0\.0030/)
  assert.equal(view.reconciliation, 'not_reconciled')
})

test('summary excludes both partial and historical rows from other unknown', () => {
  const view = analyzeCostSummary(summary())
  const unknownRow = view.detailRows.find((row) => row.label === '未知记录')

  assert.equal(view.status, 'ready')
  assert.match(unknownRow?.value ?? '', /其他未知 0/)
  assert.match(unknownRow?.value ?? '', /历史记录 1/)
})

test('summary rejects overlapping partial and historical unknown subsets', () => {
  const view = analyzeCostSummary(summary({
    successful_requests: 10,
    estimated_requests: 6,
    zero_requests: 3,
    unknown_requests: 1,
    partial_requests: 1,
    historical_requests: 1,
  }))

  assert.equal(view.status, 'invalid')
  assert.equal(view.detailRows.length, 0)
  assert.equal(view.completeCoverage, false)
})

test('a fully covered summary can say complete, but still never says reconciled', () => {
  const view = analyzeCostSummary(summary({
    successful_requests: 4,
    estimated_requests: 3,
    zero_requests: 1,
    local_free_requests: 0,
    partial_requests: 0,
    unknown_requests: 0,
    historical_requests: 0,
    recorded_cost_usd: 0.012,
    estimated_cost_usd: 0.012,
    partial_cost_usd: 0,
    historical_cost_usd: 0,
  }), 2)
  assert.equal(view.completeCoverage, true)
  assert.match(view.coverageText, /完整估算 4\/4/)
  assert.doesNotMatch(view.coverageText, /已与上游账单对账/)
  assert.equal(view.reconciliation, 'not_reconciled')
})

test('missing, unknown-version, and inconsistent summaries never imply complete coverage', () => {
  const missing = analyzeCostSummary(null)
  assert.equal(missing.status, 'missing')
  assert.equal(missing.completeCoverage, false)
  assert.match(missing.statusLabel, /费用依据未提供/)
  assert.equal(missing.reconciliation, 'not_reconciled')

  const unknownVersion = analyzeCostSummary({ ...summary(), version: 2 })
  assert.equal(unknownVersion.status, 'unknown_version')
  assert.equal(unknownVersion.completeCoverage, false)
  assert.match(unknownVersion.statusLabel, /费用依据未提供/)

  const inconsistent = analyzeCostSummary(summary({
    successful_requests: 10,
    estimated_requests: 10,
    zero_requests: 1,
  }))
  assert.equal(inconsistent.status, 'invalid')
  assert.equal(inconsistent.completeCoverage, false)
  assert.match(inconsistent.statusLabel, /费用依据未提供/)
})

test('a valid empty summary says there are no successful requests without claiming coverage', () => {
  const view = analyzeCostSummary(summary({
    successful_requests: 0,
    estimated_requests: 0,
    zero_requests: 0,
    local_free_requests: 0,
    partial_requests: 0,
    unknown_requests: 0,
    historical_requests: 0,
    site_pricing_requests: 0,
    local_estimate_requests: 0,
    mixed_requests: 0,
    recorded_cost_usd: 0,
    estimated_cost_usd: 0,
    partial_cost_usd: 0,
    historical_cost_usd: 0,
  }))
  assert.equal(view.status, 'ready')
  assert.equal(view.completeCoverage, false)
  assert.match(view.coverageText, /暂无成功请求/)
  assert.equal(view.reconciliation, 'not_reconciled')
})

test('coverage presentation distinguishes incomplete evidence, complete evidence, no requests and missing data', () => {
  assert.equal(analyzeCostSummary(summary()).coverageState, 'incomplete')
  assert.equal(analyzeCostSummary(summary({ successful_requests: 2, estimated_requests: 1, zero_requests: 1,
    partial_requests: 0, unknown_requests: 0, historical_requests: 0 })).coverageState, 'complete')
  assert.equal(analyzeCostSummary(summary({ successful_requests: 0, estimated_requests: 0, zero_requests: 0,
    local_free_requests: 0, partial_requests: 0, unknown_requests: 0, historical_requests: 0 })).coverageState, 'empty')
  assert.equal(analyzeCostSummary(null).coverageState, 'missing')
  assert.equal(analyzeCostSummary({ ...summary(), version: 2 }).coverageState, 'missing')
  assert.equal(analyzeCostSummary(summary({ successful_requests: 1 })).coverageState, 'missing')
})
