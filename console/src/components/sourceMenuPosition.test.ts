import assert from 'node:assert/strict'
import { test } from 'node:test'
import { sourceMenuPosition } from './sourceMenuPosition.ts'

const viewport = { width: 1440, height: 900 }

test('默认右对齐到触发按钮，并向下弹 7px', () => {
  const anchor = { left: 1000, right: 1096, top: 20, bottom: 54 }
  const menu = { width: 156, height: 78 }
  const position = sourceMenuPosition(anchor, menu, viewport)
  assert.equal(position.left, 1096 - 156)
  assert.equal(position.top, 54 + 7)
})

test('菜单贴着视口右边缘时不越界，留 12px 边距', () => {
  const anchor = { left: 1400, right: 1440, top: 20, bottom: 54 }
  const menu = { width: 156, height: 78 }
  const position = sourceMenuPosition(anchor, menu, viewport)
  assert.equal(position.left, 1440 - 156 - 12)
  assert.ok(position.left + menu.width <= viewport.width - 12)
})

test('窄屏放不下时贴左边距，而不是把菜单推到屏幕外', () => {
  const anchor = { left: 0, right: 40, top: 20, bottom: 54 }
  const menu = { width: 156, height: 78 }
  const position = sourceMenuPosition(anchor, menu, { width: 320, height: 640 })
  assert.equal(position.left, 12)
})

test('下方放不下时翻到按钮上方', () => {
  const anchor = { left: 1000, right: 1096, top: 820, bottom: 854 }
  const menu = { width: 156, height: 120 }
  const position = sourceMenuPosition(anchor, menu, { width: 1440, height: 900 })
  assert.equal(position.top, 820 - 120 - 7)
})

test('上下都放不下时至少不越过顶部边距', () => {
  const anchor = { left: 1000, right: 1096, top: 4, bottom: 38 }
  const menu = { width: 156, height: 500 }
  const position = sourceMenuPosition(anchor, menu, { width: 1440, height: 420 })
  assert.equal(position.top, 12)
})

test('任意视口宽度下左右都不越界', () => {
  const menu = { width: 156, height: 78 }
  for (const width of [320, 390, 768, 1024, 1440]) {
    for (const right of [40, Math.round(width / 2), width]) {
      const anchor = { left: Math.max(0, right - 96), right, top: 20, bottom: 54 }
      const position = sourceMenuPosition(anchor, menu, { width, height: 800 })
      assert.ok(position.left >= 12, `width=${width} right=${right} left=${position.left}`)
      assert.ok(position.left + menu.width <= width - 12, `width=${width} right=${right}`)
    }
  }
})
