import test from 'node:test'
import assert from 'node:assert/strict'
import { applyPageSelection, countSelected, withoutSelectedIDs } from './channelSelection.ts'

test('selecting a page preserves selections from earlier pages', () => {
  assert.deepEqual([...applyPageSelection(new Set([1]), [2, 3], true)], [1, 2, 3])
})

test('deselecting a page removes only that page and preserves other pages', () => {
  assert.deepEqual([...applyPageSelection(new Set([1, 2, 3]), [2, 3], false)], [1])
  assert.deepEqual([...applyPageSelection(new Set([1, 2, 4]), [2, 3], false)], [1, 4])
})

test('explicit deletion removes only the deleted IDs', () => {
  assert.deepEqual([...withoutSelectedIDs(new Set([1, 2, 3]), [2, 3])], [1])
})

test('selected count can describe the current page without shrinking the selection', () => {
  assert.equal(countSelected(new Set([1, 2, 3]), [2, 3, 4]), 2)
})
