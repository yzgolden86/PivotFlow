import { test } from 'node:test'
import assert from 'node:assert/strict'

import {
  blendToward,
  meanLuminance,
  parseColor,
  relativeLuminance,
  SAMPLE_SCALE,
  sampleBoxInViewport,
  toneForLuminance,
  wallpaperDrawRects,
} from './wallpaperToneMath.ts'

// 守的是「小字按壁纸局部明暗自适应」里最容易算错、错了还看不出来的那一半：
// 铺图几何与取样位置。几何偏一点 → 取样取到隔壁 → 深底配深字，比不改还糟。
// 判据本身（toneForLuminance 的方向）也在这里钉死。

/* ---- 1. 颜色解析与亮度 ---- */

test('parseColor 认 #rrggbb 与 rgb()/rgba()，其它一律返回 null', () => {
  assert.deepEqual(parseColor('#1a231f'), [0x1a, 0x23, 0x1f])
  assert.deepEqual(parseColor('  #FFFFFF  '), [255, 255, 255])
  assert.deepEqual(parseColor('rgb(12, 34, 56)'), [12, 34, 56])
  assert.deepEqual(parseColor('rgba(12,34,56,0.5)'), [12, 34, 56])
  // 认不出来必须返回 null（调用方据此跳过压暗还原），不能瞎猜一个值。
  assert.equal(parseColor('var(--bg)'), null)
  assert.equal(parseColor('#abc'), null)
  assert.equal(parseColor(''), null)
})

test('relativeLuminance 的锚点：黑 0、白 1、中灰约 0.18', () => {
  assert.equal(relativeLuminance(0, 0, 0), 0)
  assert.equal(relativeLuminance(255, 255, 255), 1)
  // 0.2159 是 #808080 的标准值；#767676 略低。用区间而不是等值，
  // 免得以后有人「优化」公式时被浮点误差绊住。
  assert.ok(Math.abs(relativeLuminance(128, 128, 128) - 0.2159) < 0.001)
  assert.ok(relativeLuminance(26, 35, 31) < 0.02, '壁纸档的近黑令牌应当非常暗')
})

/* ---- 2. 铺图几何：必须和 CSS 的 cover / contain / tile 一致 ---- */

test('cover 要盖满画布且居中（不留边）', () => {
  const rects = wallpaperDrawRects('cover', 200, 100, 100, 100)
  assert.equal(rects.length, 1)
  const rect = rects[0]
  assert.ok(rect)
  // 200x100 的图盖 100x100 的画布：按较大比例放大到 200x100，左右各溢出 50。
  assert.equal(rect.width, 200)
  assert.equal(rect.height, 100)
  assert.equal(rect.x, -50)
  assert.equal(rect.y, 0)
  assert.ok(rect.x <= 0 && rect.x + rect.width >= 100, 'cover 必须左右都盖满')
})

test('contain 要整张装进去且居中（允许留边）', () => {
  const rects = wallpaperDrawRects('contain', 200, 100, 100, 100)
  assert.equal(rects.length, 1)
  const rect = rects[0]
  assert.ok(rect)
  assert.equal(rect.width, 100)
  assert.equal(rect.height, 50)
  assert.equal(rect.x, 0)
  assert.equal(rect.y, 25, 'contain 上下留边，垂直居中')
})

test('tile 用原图尺寸铺满，并且按 center 偏移（不是从 0,0 开始）', () => {
  const rects = wallpaperDrawRects('tile', 30, 30, 100, 100)
  assert.ok(rects.length > 0)
  for (const rect of rects) {
    assert.equal(rect.width, 30)
    assert.equal(rect.height, 30)
  }
  // 必须真的盖满：左边界要 ≤ 0，右边界要 ≥ 100。
  const minX = Math.min(...rects.map((r) => r.x))
  const maxX = Math.max(...rects.map((r) => r.x + r.width))
  const minY = Math.min(...rects.map((r) => r.y))
  const maxY = Math.max(...rects.map((r) => r.y + r.height))
  assert.ok(minX <= 0 && maxX >= 100, `tile 没铺满横向：${minX}..${maxX}`)
  assert.ok(minY <= 0 && maxY >= 100, `tile 没铺满纵向：${minY}..${maxY}`)
  // `background-position: center` 对 repeat 也生效 —— 从 0,0 开始铺是错的。
  assert.ok(minX < 0, 'tile 应当有负偏移（居中的结果），从 0,0 开始说明漏了 center')
})

test('退化输入返回空数组，而不是算出天文数字的块数', () => {
  // 1x1 的图铺 4000x4000 画布 = 1600 万块。必须被卡住。
  assert.deepEqual(wallpaperDrawRects('tile', 1, 1, 4000, 4000), [])
  assert.deepEqual(wallpaperDrawRects('cover', 0, 100, 100, 100), [])
  assert.deepEqual(wallpaperDrawRects('contain', 100, 100, 0, 100), [])
})

/* ---- 3. 压暗还原与取样 ---- */

test('blendToward 的端点：alpha=0 原样、alpha=1 全变该色', () => {
  const untouched = new Uint8ClampedArray([10, 20, 30, 255])
  blendToward(untouched, [200, 200, 200], 0)
  assert.deepEqual([...untouched], [10, 20, 30, 255])

  const washed = new Uint8ClampedArray([0, 0, 0, 255])
  blendToward(washed, [200, 200, 200], 1)
  assert.deepEqual([...washed], [200, 200, 200, 255])

  // alpha 夹在 0~1，越界不该算出负值或超过 255。
  const clamped = new Uint8ClampedArray([0, 0, 0, 255])
  blendToward(clamped, [200, 200, 200], 5)
  assert.deepEqual([...clamped], [200, 200, 200, 255])
})

/** 造一张「上半纯白、下半纯黑」的画布缓冲，复刻 hao哥 那张壁纸的形状。 */
function brightTopDarkBottom(width: number, height: number): Uint8ClampedArray {
  const data = new Uint8ClampedArray(width * height * 4)
  for (let y = 0; y < height; y += 1) {
    const value = y < height / 2 ? 255 : 0
    for (let x = 0; x < width; x += 1) {
      const i = (y * width + x) * 4
      data[i] = value
      data[i + 1] = value
      data[i + 2] = value
      data[i + 3] = 255
    }
  }
  return data
}

test('meanLuminance 取的是矩形内的平均，越界夹住而不是抛错', () => {
  const width = 100
  const height = 100
  const data = brightTopDarkBottom(width, height)

  const top = meanLuminance(data, width, height, { left: 0, top: 0, right: 100, bottom: 100 })
  assert.equal(top, 1)
  const bottom = meanLuminance(data, width, height, { left: 0, top: 300, right: 100, bottom: 400 })
  assert.equal(bottom, 0)

  // 完全在画布外 → null（调用方据此清掉标记，而不是当成「亮底」）。
  assert.equal(meanLuminance(data, width, height, { left: 900, top: 900, right: 999, bottom: 999 }), null)
})

test('元素在折叠线以下时按「贴住视口下沿」取样', () => {
  // 壁纸是 fixed 的，滚到底那一刻元素底下就是视口下沿那一块。
  const box = { left: 0, top: 1124, right: 100, bottom: 1154 }
  const moved = sampleBoxInViewport(box, 900)
  assert.equal(moved.top, 870, '1154 高的元素贴到 900 的视口下沿 → top = 900 - 30')
  assert.equal(moved.bottom, 900)
  assert.equal(moved.left, 0, '横向不该动')

  // 已经在视口内的不该被挪。
  const inside = { left: 0, top: 100, right: 100, bottom: 130 }
  assert.deepEqual(sampleBoxInViewport(inside, 900), inside)
})

/* ---- 4. 判据方向：这条错了整个功能就是反的 ---- */

test('深底必须配浅字：toneForLuminance 的方向不能反', () => {
  assert.equal(toneForLuminance(0), 'on-dark', '纯黑底 → 用浅字')
  assert.equal(toneForLuminance(0.001), 'on-dark')
  assert.equal(toneForLuminance(1), 'on-light', '纯白底 → 用深字')
  assert.equal(toneForLuminance(0.9), 'on-light')
})

test('端到端：上亮下黑的壁纸，页脚判深底、页头判浅底', () => {
  // 画布 100x100、SAMPLE_SCALE=0.25 → 对应 400x400 的视口。
  const width = 100
  const height = 100
  const data = brightTopDarkBottom(width, height)
  const viewport = { width: width / SAMPLE_SCALE, height: height / SAMPLE_SCALE }

  const headerBox = { left: 0, top: 20, right: viewport.width, bottom: 60 }
  const footerBox = sampleBoxInViewport(
    { left: 0, top: viewport.height - 30, right: viewport.width, bottom: viewport.height },
    viewport.height,
  )

  const headerTone = toneForLuminance(meanLuminance(data, width, height, headerBox) ?? 1)
  const footerTone = toneForLuminance(meanLuminance(data, width, height, footerBox) ?? 1)
  assert.equal(headerTone, 'on-light', '上半部是亮底 → 深字')
  assert.equal(footerTone, 'on-dark', '下半部是纯黑底 → 浅字（这正是要修的那一格）')
})
