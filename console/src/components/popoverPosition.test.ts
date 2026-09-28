import assert from 'node:assert/strict'
import { test } from 'node:test'
import { popoverPosition } from './popoverPosition.ts'

const viewport = { width: 1440, height: 900 }

test('默认右对齐到锚点，并向下弹 7px', () => {
  const anchor = { left: 1000, right: 1096, top: 20, bottom: 54 }
  const menu = { width: 156, height: 78 }
  const position = popoverPosition(anchor, menu, viewport)
  assert.equal(position.left, 1096 - 156)
  assert.equal(position.top, 54 + 7)
})

test('浮层贴着视口右边缘时不越界，留 12px 边距', () => {
  const anchor = { left: 1400, right: 1440, top: 20, bottom: 54 }
  const menu = { width: 156, height: 78 }
  const position = popoverPosition(anchor, menu, viewport)
  assert.equal(position.left, 1440 - 156 - 12)
  assert.ok(position.left + menu.width <= viewport.width - 12)
})

test('窄屏放不下且未开外挂时贴左边距，而不是把浮层推到屏幕外', () => {
  const anchor = { left: 0, right: 40, top: 20, bottom: 54 }
  const menu = { width: 156, height: 78 }
  const position = popoverPosition(anchor, menu, { width: 320, height: 640 })
  assert.equal(position.left, 12)
})

test('下方放不下时翻到锚点上方', () => {
  const anchor = { left: 1000, right: 1096, top: 820, bottom: 854 }
  const menu = { width: 156, height: 120 }
  const position = popoverPosition(anchor, menu, { width: 1440, height: 900 })
  assert.equal(position.top, 820 - 120 - 7)
})

test('上下都放不下时至少不越过顶部边距', () => {
  const anchor = { left: 1000, right: 1096, top: 4, bottom: 38 }
  const menu = { width: 156, height: 500 }
  const position = popoverPosition(anchor, menu, { width: 1440, height: 420 })
  assert.equal(position.top, 12)
})

test('任意视口宽度下左右都不越界', () => {
  const menu = { width: 156, height: 78 }
  for (const width of [320, 390, 768, 1024, 1440]) {
    for (const right of [40, Math.round(width / 2), width]) {
      const anchor = { left: Math.max(0, right - 96), right, top: 20, bottom: 54 }
      const position = popoverPosition(anchor, menu, { width, height: 800 })
      assert.ok(position.left >= 12, `width=${width} right=${right} left=${position.left}`)
      assert.ok(position.left + menu.width <= width - 12, `width=${width} right=${right}`)
    }
  }
})

/* ---- 主题菜单：贴底向上弹 + 窄锚点外挂 ---- */

test('贴底浮层向上弹：展开态侧栏右对齐到操作条内边缘', () => {
  // 展开态侧栏 216px：footer padding 11 + actions padding 4 → 内边缘在 201。
  const anchor = { left: 15, right: 201, top: 700, bottom: 737 }
  const menu = { width: 168, height: 122 }
  const position = popoverPosition(anchor, menu, viewport, { placement: 'above', flyOut: true })
  assert.equal(position.left, 201 - 168)
  assert.equal(position.top, 700 - 122 - 7)
  assert.ok(position.left >= 12)
})

test('收起态侧栏只有 72px 宽，右对齐会出界 → 改为贴着锚点右侧外挂', () => {
  // 收起态 actions 内边缘在 57，右对齐会算出 -111。
  const anchor = { left: 15, right: 57, top: 600, bottom: 634 }
  const menu = { width: 168, height: 122 }
  const position = popoverPosition(anchor, menu, viewport, { placement: 'above', flyOut: true })
  assert.equal(position.left, 57 + 7)
  assert.ok(position.left + menu.width <= viewport.width - 12)
})

test('未开外挂时同一组数字仍夹到左边距（证明 flyOut 是承重的）', () => {
  const anchor = { left: 15, right: 57, top: 600, bottom: 634 }
  const menu = { width: 168, height: 122 }
  const position = popoverPosition(anchor, menu, viewport, { placement: 'above' })
  assert.equal(position.left, 12)
})

test('上方放不下时贴底浮层翻到下方', () => {
  const anchor = { left: 15, right: 201, top: 40, bottom: 77 }
  const menu = { width: 168, height: 122 }
  const position = popoverPosition(anchor, menu, viewport, { placement: 'above' })
  assert.equal(position.top, 77 + 7)
})

test('侧栏浮层在 320/390/768/1024/1440 下都不越界', () => {
  const menu = { width: 168, height: 122 }
  for (const width of [320, 390, 768, 1024, 1440]) {
    // 展开态与收起态两种锚点宽度都扫一遍。
    for (const right of [57, 201]) {
      const anchor = { left: 15, right, top: 600, bottom: 634 }
      const position = popoverPosition(anchor, menu, { width, height: 800 }, { placement: 'above', flyOut: true })
      assert.ok(position.left >= 12, `width=${width} right=${right} left=${position.left}`)
      assert.ok(position.left + menu.width <= width - 12, `width=${width} right=${right} left=${position.left}`)
    }
  }
})
