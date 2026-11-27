import assert from 'node:assert/strict'
import test from 'node:test'
import type { StatsEntry } from '../types.ts'
import { statsDistribution } from './statsDistribution.ts'

function entry(index: number, cost: number | undefined = 10): StatsEntry {
  return { channel_name: `channel-${index}`, model: `model-${index}`, total: 10, success: 10, error: 0, effective_cost: cost }
}

test('channel and model views retain every cost in top four plus other, sharing one denominator', () => {
  const source = Array.from({ length: 9 }, (_, index) => entry(index))
  const before = structuredClone(source)
  for (const dimension of ['channel', 'model'] as const) {
    const view = statsDistribution(source, dimension, 'cost')
    assert.equal(view.total, 90)
    assert.equal(view.items.length, 5)
    assert.deepEqual(view.items.map((item) => item.value), [10, 10, 10, 10, 50])
    assert.equal(view.items.at(-1)?.isOther, true)
    assert.ok(Math.abs(view.items.reduce((sum, item) => sum + item.share, 0) - 1) < 1e-12)
    assert.equal(view.items.reduce((sum, item) => sum + item.value, 0), view.total)
  }
  assert.deepEqual(source, before)
})

test('unknown cost rows are counted without being depicted as known zero; legacy cost still counts', () => {
  const unknown = { ...entry(2), effective_cost: undefined }
  const invalid = { ...entry(3), effective_cost: NaN, total_cost: 100 }
  const view = statsDistribution([entry(1, 0), unknown, invalid, { ...entry(4), effective_cost: undefined, total_cost: 3 }], 'model', 'cost')
  assert.equal(view.total, 3)
  assert.equal(view.unknownEntries, 2)
  assert.deepEqual(view.items.map((item) => item.value), [3, 0])
  assert.deepEqual(view.items.map((item) => item.share), [1, 0])
})

test('a real label named other remains distinct from the generated remainder and zero-cost tails are retained', () => {
  const source = Array.from({ length: 7 }, (_, index) => entry(index, 0))
  source[0].model = '其他'
  const view = statsDistribution(source, 'model', 'cost')
  assert.equal(view.items.length, 5)
  assert.equal(new Set(view.items.map((item) => item.key)).size, 5)
  assert.equal(view.items[0].isOther, false)
  assert.equal(view.items[4].isOther, true)
  assert.ok(view.items.every((item) => item.share === 0))
})

test('grouping repeated names sums raw values before rounding and request distributions stay complete', () => {
  const source = [entry(1, 0.000001), entry(2, 0.000002), { ...entry(3, 0.000003), model: 'model-1' }]
  const view = statsDistribution(source, 'model', 'cost')
  assert.equal(view.total, source.reduce((sum, item) => sum + item.effective_cost!, 0))
  assert.equal(view.items.find((item) => item.label === 'model-1')?.value, 0.000004)
  assert.equal(statsDistribution(source, 'channel', 'requests').total, 30)
  assert.deepEqual(statsDistribution([], 'model', 'cost'), { total: 0, items: [], unknownEntries: 0 })
})

test('scrollable list retains every named group while the pie keeps its grouped remainder', () => {
  const source = Array.from({ length: 18 }, (_, index) => entry(index, index + 1))
  const list = statsDistribution(source, 'channel', 'cost', false)
  const pie = statsDistribution(source, 'channel', 'cost')
  assert.equal(list.items.length, 18)
  assert.equal(list.items.some((item) => item.isOther), false)
  assert.equal(list.items[0].label, 'channel-17')
  assert.equal(list.items.reduce((sum, item) => sum + item.value, 0), pie.total)
  assert.ok(Math.abs(list.items.reduce((sum, item) => sum + item.share, 0) - 1) < 1e-12)
})
