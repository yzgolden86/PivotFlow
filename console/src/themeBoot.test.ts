import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import {
  defaultThemeCustomization,
  themeContentWidthOptions,
  themeFontOptions,
  themeMotionOptions,
  themePresetOptions,
  themeRadiusOptions,
  themeSidebarWidthOptions,
} from './theme.ts'

// index.html 里那段内联脚本必须先于 bundle 执行 —— 它要在 React 挂载前就把
// data-theme / data-theme-preset 写进 <html>，否则会先闪一下默认主题。所以它
// 没法 import theme.ts，只能把三份清单抄成字面量。抄漏的后果很隐蔽：
// 被拒的值会被替换成默认值，随后 bundle 挂载再改回用户真正选的那个，
// 表现为「每次刷新先闪一下默认主题」——功能断言全绿，肉眼也不一定注意得到。
//
// 历史遗留就是证据：预设清单长期停在 ['jade','ocean','coral']（theme.ts 早已是 8 套），
// 字体清单停在 ['modern','system','serif']（这两个值连选项里都不存在了）。
// 这里逐项比对，把漂移钉死。
const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8')

function assignment(datasetKey: string): string {
  const matched = new RegExp(`root\\.dataset\\.${datasetKey}\\s*=\\s*([^\\n]+)`).exec(html)
  assert.ok(matched, `index.html 的内联脚本里找不到 root.dataset.${datasetKey} 的赋值`)
  return matched[1]
}

/** 取出 .includes([...]) 里的取值清单。 */
function bootList(datasetKey: string): string[] {
  const matched = /\[([^\]]*)\]/.exec(assignment(datasetKey))
  assert.ok(matched, `root.dataset.${datasetKey} 的赋值里没有取值清单`)
  return matched[1]
    .split(',')
    .map((part) => part.trim().replace(/^['"]|['"]$/g, ''))
    .filter((part) => part.length > 0)
}

/** 取出三元表达式 else 分支上的回落默认值。 */
function bootFallback(datasetKey: string): string {
  const matched = /:\s*['"]([^'"]+)['"]\s*$/.exec(assignment(datasetKey).trim())
  assert.ok(matched, `root.dataset.${datasetKey} 的赋值里没有回落默认值`)
  return matched[1]
}

test('内联脚本的主题清单与 theme.ts 逐项一致', () => {
  assert.deepEqual(bootList('themePreset'), [...themePresetOptions])
  assert.deepEqual(bootList('themeFont'), themeFontOptions.map((option) => option.value))
  assert.deepEqual(bootList('themeRadius'), [...themeRadiusOptions])
})

test('内联脚本的布局与动效清单与 theme.ts 逐项一致', () => {
  // 后三项只是布局 / 动效，但闪一下同样是肉眼可见的位移（侧栏宽度、内容宽度、
  // 动画起手），所以跟配色字体一样必须钉住。
  assert.deepEqual(bootList('sidebarWidth'), [...themeSidebarWidthOptions])
  assert.deepEqual(bootList('contentWidth'), [...themeContentWidthOptions])
  assert.deepEqual(bootList('motion'), [...themeMotionOptions])
})

test('内联脚本的回落默认值与 theme.ts 的默认外观一致', () => {
  // 清单没抄漏、但默认值抄错，同样会闪 —— 这两件事要分别盯。
  assert.equal(bootFallback('themePreset'), defaultThemeCustomization.preset)
  assert.equal(bootFallback('themeFont'), defaultThemeCustomization.font)
  assert.equal(bootFallback('themeRadius'), defaultThemeCustomization.radius)
  assert.equal(bootFallback('sidebarWidth'), defaultThemeCustomization.sidebarWidth)
  assert.equal(bootFallback('contentWidth'), defaultThemeCustomization.contentWidth)
  assert.equal(bootFallback('motion'), defaultThemeCustomization.motion)
})
