import assert from 'node:assert/strict'
import { test } from 'node:test'
import { helpTipPosition } from './helpTipPosition.ts'

test('help tips stay inside mobile viewport at both horizontal edges', () => {
  for (const width of [320, 390, 768, 1440]) {
    const bubble = { width: Math.min(300, width - 24), height: 100 }
    for (const left of [0, 120, width - 24]) {
      const position = helpTipPosition({ left, top: 100, bottom: 126 }, bubble, { width, height: 844 })
      assert.ok(position.left >= 12)
      assert.ok(position.left + bubble.width <= width - 12)
      assert.equal(position.top, 134)
    }
  }
})

test('help tips open above a trigger near the bottom', () => {
  const position = helpTipPosition({ left: 200, top: 780, bottom: 806 }, { width: 300, height: 120 }, { width: 390, height: 844 })
  assert.equal(position.top, 652)
  assert.equal(position.left, 78)
})
