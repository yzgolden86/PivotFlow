import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

// 小字可读性回归。两条不变量，都是靠「生效值」判断，不是文本里出现过就算：
//   1) 除 .appearance-preview 的缩略主题预览外，任何选择器的生效 font-size 不得小于 11px；
//   2) 小字用的文字色 / 底色组合在 8 套预设 × 明暗两种模式下都要过 WCAG AA 4.5:1。
//
// 第 2 条踩过两个坑，改这个文件前先读懂：
//   - 强调色徽章的底色是同族 --*-soft，不是 --surface；按 surface 算会漏掉真实失败。
//   - :root[data-theme-preset="X"] 和 :root[data-theme="dark"] 同为 (0,2,0)，浅色预设块
//     在文件里更靠后，会盖掉 dark 块。所以 dark 的回落值必须写在
//     :root[data-theme="dark"][data-theme-preset="X"] (0,3,0) 里。这里按同样的优先级
//     顺序解析，能抓住回落被浅色值污染的情况。
const css = readFileSync(new URL('../styles.css', import.meta.url), 'utf8')

// 去注释，避免注释里的花括号和分号干扰深度统计。
const clean = css.replace(/\/\*[\s\S]*?\*\//g, (comment) => comment.replace(/[^\n]/g, ' '))

// 按层叠顺序扫描：后出现的同名选择器覆盖先出现的。栈记录每层是不是 at-rule，
// 媒体查询里的声明同样参与覆盖（媒体查询不加权重，但位置靠后仍然生效）。
function effectiveFontSizes(source: string): Map<string, number> {
  const sizes = new Map<string, number>()
  const stack: boolean[] = []
  let start = 0
  for (let cursor = 0; cursor < source.length; cursor += 1) {
    const char = source[cursor]
    if (char === '{') {
      const prelude = source.slice(start, cursor).trim()
      const isAtRule = prelude.startsWith('@')
      if (!isAtRule) {
        const close = source.indexOf('}', cursor)
        const body = source.slice(cursor + 1, close === -1 ? source.length : close)
        const declared = /(?:^|[;\s])font-size\s*:\s*([\d.]+)px/.exec(body)
        if (declared) {
          for (const part of prelude.split(',')) sizes.set(part.trim(), Number(declared[1]))
        }
      }
      stack.push(isAtRule)
      start = cursor + 1
    } else if (char === '}') {
      stack.pop()
      start = cursor + 1
    } else if (char === ';' && stack.length === 0) {
      start = cursor + 1
    }
  }
  return sizes
}

test('小字不得小于 11px（缩略主题预览除外）', () => {
  const offenders = [...effectiveFontSizes(clean)]
    .filter(([selector, size]) => size < 11 && !selector.includes('appearance-preview'))
    .map(([selector, size]) => `${selector} 生效 ${size}px`)
  assert.deepEqual(offenders, [], `以下选择器的生效字号小于 11px：\n${offenders.join('\n')}`)
})

// ---- 对比度 ----

const rootBlocks = new Map<string, Map<string, string>>()
for (const [, selector, body] of clean.matchAll(/(:root[^{]*)\{([^}]*)\}/g)) {
  const key = selector.trim()
  const tokens = rootBlocks.get(key) ?? new Map<string, string>()
  for (const [, name, value] of body.matchAll(/(--[\w-]+):\s*([^;]+);/g)) tokens.set(name, value.trim())
  rootBlocks.set(key, tokens)
}

const presets = ['default', 'anthropic', 'ocean', 'coral', 'violet', 'slate', 'forest', 'plum']

// 小字实际用到的「文字色 on 底色」组合。强调色既配同族 -soft 徽章底，也配 surface。
const pairs: Array<[string, string]> = [
  ['--text', '--bg'], ['--text', '--surface'],
  ['--text-secondary', '--surface'], ['--text-secondary', '--bg'],
  ['--text-muted', '--surface'], ['--text-muted', '--surface-muted'],
  ['--text-muted', '--surface-strong'], ['--text-muted', '--bg'],
  ['--sidebar-text', '--sidebar'], ['--sidebar-muted', '--sidebar'],
  ['--green-text', '--green-soft'], ['--amber-text', '--amber-soft'],
  ['--coral-text', '--coral-soft'], ['--blue-text', '--blue-soft'],
  ['--green-text', '--surface'], ['--amber-text', '--surface'],
  ['--coral-text', '--surface'], ['--blue-text', '--surface'],
]

function resolveTheme(preset: string, dark: boolean): Map<string, string> {
  const tokens = new Map(rootBlocks.get(':root'))
  const apply = (key: string) => {
    for (const [name, value] of rootBlocks.get(key) ?? []) tokens.set(name, value)
  }
  if (preset !== 'default') apply(`:root[data-theme-preset="${preset}"]`)
  if (dark) {
    apply(':root[data-theme="dark"]')
    if (preset !== 'default') apply(`:root[data-theme="dark"][data-theme-preset="${preset}"]`)
  }
  return tokens
}

// var() 可以指向另一个 token（深色回落就是 --green-text: var(--green)），要一路跟到十六进制。
function resolve(tokens: Map<string, string>, name: string, seen = new Set<string>()): string | null {
  if (seen.has(name)) return null
  seen.add(name)
  const value = tokens.get(name)
  if (!value) return null
  const indirect = /^var\((--[\w-]+)\)$/.exec(value)
  if (indirect) return resolve(tokens, indirect[1], seen)
  return value.startsWith('#') ? value : null
}

function luminance(hex: string): number {
  const raw = hex.replace('#', '')
  const channels = [0, 2, 4].map((offset) => {
    const part = Number.parseInt(raw.slice(offset, offset + 2), 16) / 255
    return part <= 0.03928 ? part / 12.92 : ((part + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2]
}

function contrast(foreground: string, background: string): number {
  const [high, low] = [luminance(foreground), luminance(background)].sort((a, b) => b - a)
  return (high + 0.05) / (low + 0.05)
}

test('小字配色在全部主题下过 WCAG AA 4.5:1', () => {
  const offenders: string[] = []
  let checked = 0
  for (const preset of presets) {
    for (const dark of [false, true]) {
      const tokens = resolveTheme(preset, dark)
      for (const [foreground, background] of pairs) {
        const fg = resolve(tokens, foreground)
        const bg = resolve(tokens, background)
        // 解析不出来说明 token 改名或删了，必须报错——否则这个测试会静默少测。
        assert.ok(fg, `${preset}-${dark ? 'dark' : 'light'} 解析不出 ${foreground}`)
        assert.ok(bg, `${preset}-${dark ? 'dark' : 'light'} 解析不出 ${background}`)
        checked += 1
        const ratio = contrast(fg, bg)
        if (ratio < 4.5) {
          offenders.push(`${preset}-${dark ? 'dark' : 'light'} ${foreground} on ${background}：${fg} / ${bg} 只有 ${ratio.toFixed(2)}`)
        }
      }
    }
  }
  assert.equal(checked, presets.length * 2 * pairs.length)
  assert.deepEqual(offenders, [], `以下小字配色未过 AA 4.5:1：\n${offenders.join('\n')}`)
})
