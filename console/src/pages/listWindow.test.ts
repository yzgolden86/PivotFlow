import assert from 'node:assert/strict'
import { test } from 'node:test'
import { listWindow, parsePageInput } from './listWindow.ts'

test('empty lists keep a valid page and an empty slice', () => {
  assert.deepEqual(listWindow(0, 9, 50), { page: 1, pages: 1, pageSize: 50, total: 0, start: 0, end: 0 })
})

test('partial last pages keep the exact visible range', () => {
  assert.deepEqual(listWindow(151, 4, 50), { page: 4, pages: 4, pageSize: 50, total: 151, start: 150, end: 151 })
})

test('a removed last page immediately falls back to the new last page', () => {
  assert.deepEqual(listWindow(150, 4, 50), { page: 3, pages: 3, pageSize: 50, total: 150, start: 100, end: 150 })
})

test('invalid or fractional API counts and pagination state cannot produce invalid slices', () => {
  assert.deepEqual(listWindow(Number.NaN, -4, 0), { page: 1, pages: 1, pageSize: 50, total: 0, start: 0, end: 0 })
  assert.deepEqual(listWindow(101.8, 2.9, 50.2), { page: 2, pages: 3, pageSize: 50, total: 101, start: 50, end: 100 })
})

test('changing page capacity preserves a valid bounded window', () => {
  assert.deepEqual(listWindow(151, 4, 100), { page: 2, pages: 2, pageSize: 100, total: 151, start: 100, end: 151 })
})

test('direct page input accepts only in-range integer pages', () => {
  for (const [input, expected] of [['1', 1], [' 04 ', 4], ['0', null], ['5', null], ['', null], ['1.5', null], ['-1', null], ['1e2', null], ['Infinity', null], ['9007199254740993', null]] as const) {
    assert.equal(parsePageInput(input, 4), expected, input)
  }
})
