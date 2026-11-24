import assert from 'node:assert/strict'
import test from 'node:test'
import type { DashboardUsage } from '../types.ts'
import { modelCostDistribution } from './costDistribution.ts'

function usage(n: number, cost = 10, requests = 10): DashboardUsage {
  return { key: `model-${n}`, label: `model-${n}`, requests, success: requests, errors: 0,
    input_tokens: requests * 100, output_tokens: requests * 20, effective_cost: cost, share: 1 / 9 }
}

test('nine equal-cost models retain the full $90 total and merge hidden rows into one legend item', () => {
  const source = Array.from({ length: 9 }, (_, i) => usage(i + 1))
  const view = modelCostDistribution(source)
  assert.equal(view.total, 90)
  assert.equal(view.items.length, 5)
  assert.deepEqual(view.items.slice(0, 4).map((item) => item.key), ['model-1', 'model-2', 'model-3', 'model-4'])
  const other = view.items[4]
  assert.equal(other.label, '其他模型')
  assert.equal(other.effective_cost, 50)
  assert.equal(other.requests, 50)
  assert.equal(other.input_tokens, 5000)
  assert.equal(other.output_tokens, 1000)
  assert.ok(Math.abs(view.items.reduce((sum, item) => sum + item.share, 0) - 1) < 1e-12)
  assert.equal(source.length, 9)
  assert.equal(source[0].share, 1 / 9)
})

test('an existing backend other-models bucket is folded once without losing its cost', () => {
  const source = Array.from({ length: 6 }, (_, i) => usage(i + 1))
  source.push({ ...usage(7, 40, 80), key: 'other-models', label: '其他模型' })
  const view = modelCostDistribution(source)
  assert.equal(view.total, 100)
  assert.equal(view.items.filter((item) => item.key === 'other-models').length, 1)
  assert.equal(view.items.at(-1)?.effective_cost, 60)
  assert.equal(view.items.at(-1)?.requests, 100)
})

test('a zero-cost view uses request proportions and an empty view remains empty', () => {
  const view = modelCostDistribution([usage(1, 0, 1), usage(2, 0, 3)])
  assert.equal(view.total, 0)
  assert.deepEqual(view.items.map((item) => item.share), [0.25, 0.75])
  assert.deepEqual(modelCostDistribution([]), { total: 0, items: [] })
  assert.deepEqual(modelCostDistribution([usage(1, 0, 0)]).items.map((item) => item.share), [0])
})

test('small recorded amounts and exact limit retain every cost rather than rounding before aggregation', () => {
  const source = Array.from({ length: 5 }, (_, i) => usage(i + 1, 0.000001))
  const view = modelCostDistribution(source)
  assert.equal(view.items.length, 5)
  assert.equal(view.total, source.reduce((sum, item) => sum + item.effective_cost, 0))
  assert.equal(view.items[0].effective_cost, 0.000001)
})
