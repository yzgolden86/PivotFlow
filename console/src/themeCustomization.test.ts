import { test } from 'node:test'
import assert from 'node:assert/strict'
import { backgroundImageValue, clampNumber, shouldSkipCustomCss } from './theme.ts'

/* ---- 壁纸地址 → CSS url() ---- */

test('空地址不产生任何背景层', () => {
  assert.equal(backgroundImageValue(''), 'none')
  assert.equal(backgroundImageValue('   '), 'none')
})

test('http / https 地址包成 url("...")', () => {
  assert.equal(backgroundImageValue('https://example.com/a.png'), 'url("https://example.com/a.png")')
  assert.equal(backgroundImageValue('http://example.com/a.png'), 'url("http://example.com/a.png")')
  // 前后空格要吃掉，否则 url() 里会带上一段无意义的空白。
  assert.equal(backgroundImageValue('  https://example.com/a.png  '), 'url("https://example.com/a.png")')
})

test('非 http(s) 协议一律拒绝，不给样式表留口子', () => {
  assert.equal(backgroundImageValue('javascript:alert(1)'), 'none')
  assert.equal(backgroundImageValue('data:image/svg+xml;base64,AAAA'), 'none')
  assert.equal(backgroundImageValue('file:///c:/secret.png'), 'none')
  assert.equal(backgroundImageValue('//example.com/a.png'), 'none')
})

test('地址里的引号被转义，无法提前闭合 url() 注入声明', () => {
  const hostile = 'https://a/x.png") , url("https://evil/y.png'
  const value = backgroundImageValue(hostile)
  // 整串仍然只是**一个** url()：首尾之外不能再出现未转义的引号。
  assert.ok(value.startsWith('url("'), value)
  assert.ok(value.endsWith('")'), value)
  const inner = value.slice(5, -2)
  const unescapedQuotes = inner.replace(/\\"/g, '').split('"').length - 1
  assert.equal(unescapedQuotes, 0, `仍有未转义的引号：${value}`)
  // 反斜杠本身也要转义，否则 `\` 会把后面的字符吃掉。
  assert.equal(backgroundImageValue('https://a/x\\y.png'), 'url("https://a/x\\\\y.png")')
})

/* ---- 自由 CSS 的逃生开关 ---- */

test('地址里带 plain=1 时跳过自由 CSS', () => {
  assert.equal(shouldSkipCustomCss('?plain=1', ''), true)
  assert.equal(shouldSkipCustomCss('?plain=true', ''), true)
  assert.equal(shouldSkipCustomCss('?a=1&plain=1', ''), true)
})

test('hash 里带 plain=1 同样跳过（设置页是 hash 路由）', () => {
  assert.equal(shouldSkipCustomCss('', '#/system?plain=1'), true)
  assert.equal(shouldSkipCustomCss('', '#/system?a=1&plain=1'), true)
})

test('没有 plain 或取值不是 1/true 时不跳过', () => {
  assert.equal(shouldSkipCustomCss('', ''), false)
  assert.equal(shouldSkipCustomCss('?plain=0', ''), false)
  assert.equal(shouldSkipCustomCss('?plain=', ''), false)
  assert.equal(shouldSkipCustomCss('', '#/system'), false)
  // 不能把 `plain=10` 或 `explain=1` 这种误判成开关。
  assert.equal(shouldSkipCustomCss('?plain=10', ''), false)
  assert.equal(shouldSkipCustomCss('?explain=1', ''), false)
  assert.equal(shouldSkipCustomCss('', '#/system?explain=1'), false)
})

/* ---- 数值夹取 ---- */

test('夹取把超范围的值收进区间，非数字回落到默认值', () => {
  assert.equal(clampNumber(5, 0, 10, 1), 5)
  assert.equal(clampNumber(-3, 0, 10, 1), 0)
  assert.equal(clampNumber(99, 0, 10, 1), 10)
  assert.equal(clampNumber('8', 0, 10, 1), 1)
  assert.equal(clampNumber(Number.NaN, 0, 10, 1), 1)
  assert.equal(clampNumber(Number.POSITIVE_INFINITY, 0, 10, 1), 1)
  assert.equal(clampNumber(undefined, 0, 10, 1), 1)
})
