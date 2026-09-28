import { test } from 'node:test'
import assert from 'node:assert/strict'
import { backgroundImageValue, clampNumber, shouldSkipCustomCss, sidebarAlphaValue, wallpaperActive, wallpaperUrl } from './theme.ts'

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

/* ---- 随机图源：给地址加会话级缓存绕过参数 ---- */

test('不开随机时地址原样返回，一个字符都不加', () => {
  // 普通图床的固定图片不该因为多一个参数就重新下载。
  assert.equal(wallpaperUrl('https://example.com/a.png', false), 'https://example.com/a.png')
  assert.equal(wallpaperUrl('  https://example.com/a.png  ', false), 'https://example.com/a.png')
  assert.equal(wallpaperUrl('https://example.com/a.png?w=1920', false), 'https://example.com/a.png?w=1920')
})

test('开随机时追加 pf_r 参数，已有 query 用 & 接', () => {
  const plain = wallpaperUrl('https://t.alcy.cc/fj', true)
  assert.match(plain, /^https:\/\/t\.alcy\.cc\/fj\?pf_r=[a-z0-9]+$/, plain)

  const withQuery = wallpaperUrl('https://example.com/a.png?w=1920', true)
  assert.match(withQuery, /^https:\/\/example\.com\/a\.png\?w=1920&pf_r=[a-z0-9]+$/, withQuery)
})

test('同一会话内 pf_r 必须稳定 —— 否则改个字体就换壁纸', () => {
  // applyThemeCustomization 会在用户每改一项外观设置时重跑；
  // 如果这里每次现算随机值，改字体/圆角都会顺带换掉壁纸。
  const first = wallpaperUrl('https://t.alcy.cc/fj', true)
  const second = wallpaperUrl('https://t.alcy.cc/fj', true)
  assert.equal(first, second)
  // 换一个地址也应该是同一个 nonce（nonce 是会话级的，不是每 URL 一份）。
  const other = wallpaperUrl('https://t.alcy.cc/other', true)
  assert.equal(first.split('pf_r=')[1], other.split('pf_r=')[1])
})

test('空地址不会拼出一个只有参数的怪 URL', () => {
  assert.equal(wallpaperUrl('', true), '')
  assert.equal(wallpaperUrl('   ', true), '')
})

test('拼上随机参数后，转义仍然有效 —— 不能因为多了一步就绕开 url() 注入防护', () => {
  const hostile = wallpaperUrl('https://a/x.png") , url("https://evil/y.png', true)
  const value = backgroundImageValue(hostile)
  assert.ok(value.startsWith('url("'), value)
  assert.ok(value.endsWith('")'), value)
  const inner = value.slice(5, -2)
  assert.equal(inner.replace(/\\"/g, '').split('"').length - 1, 0, `仍有未转义的引号：${value}`)
})

/* ---- 毛玻璃：侧栏不透明度 ---- */

test('侧栏比面板再透一档', () => {
  // 100% 实心时侧栏 85%，仍是「几乎不透明」，观感与改动前一致。
  assert.equal(sidebarAlphaValue(100), 85)
  assert.equal(sidebarAlphaValue(78), 66)
  assert.equal(sidebarAlphaValue(60), 62)
})

test('侧栏不透明度有 62% 下限 —— 再透下去导航文字就压不住壁纸了', () => {
  // 滑块的允许下限是 40，直接乘 0.85 会得到 34%，太透。
  assert.equal(sidebarAlphaValue(40), 62)
  assert.equal(sidebarAlphaValue(0), 62)
  assert.equal(sidebarAlphaValue(-100), 62)
  // 下限生效的整个区间里，值必须一致地钉在 62。
  for (const value of [0, 10, 20, 30, 40, 50, 60, 72]) {
    assert.equal(sidebarAlphaValue(value), 62, `surfaceAlpha=${value}`)
  }
})

test('侧栏不透明度随面板不透明度单调不减', () => {
  let previous = -1
  for (let value = 0; value <= 100; value += 5) {
    const current = sidebarAlphaValue(value)
    assert.ok(current >= previous, `surfaceAlpha=${value} 时 ${current} < ${previous}`)
    assert.ok(current >= 62 && current <= 100, `surfaceAlpha=${value} 时越界：${current}`)
    previous = current
  }
})


/* ---- 壁纸是否真的生效 ---- */

test('wallpaperActive 只看最终能不能产生背景层，不看字符串非空', () => {
  assert.equal(wallpaperActive('https://example.com/a.png'), true)
  assert.equal(wallpaperActive('  http://example.com/a.png  '), true)
  // 下面这些 backgroundImage 都非空，但 backgroundImageValue 会给出 'none'，
  // 界面上没有任何壁纸 —— 点阵该留着、文字也不该加描边。
  assert.equal(wallpaperActive(''), false)
  assert.equal(wallpaperActive('   '), false)
  assert.equal(wallpaperActive('ht'), false)
  assert.equal(wallpaperActive('/local/a.png'), false)
  assert.equal(wallpaperActive('javascript:alert(1)'), false)
  assert.equal(wallpaperActive('data:image/png;base64,AAAA'), false)
})

test('wallpaperActive 与 backgroundImageValue 永远同源', () => {
  // 两条判据必须一起变：不能出现「active=true 但没有背景层」或反过来的情况。
  for (const url of ['', '   ', 'ht', '/a.png', 'https://a/b.png', 'http://a/b.png?x=1', 'data:image/png,x']) {
    assert.equal(wallpaperActive(url), backgroundImageValue(url) !== 'none', `不一致：${url}`)
  }
})
