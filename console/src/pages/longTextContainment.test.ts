import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

// 回归：上游错误原文直接渲染进这些容器，可能是 URL、base64 或几万字符不含空白的
// 整段 token（真实故障是一个 100 KB 的 403 拦截页，内嵌 42 KB 的 base64 字体）。
//
// .console-modal 是 overflow:auto 的 flex 项，自动最小尺寸为 0：不换行的长 token
// 不会撑破弹窗宽度声明，而是让弹窗横向滚动、内容被裁到可视区之外，视觉上就是
// 「添加渠道弹窗被拉得极宽」。两个声明缺一不可——
//   overflow-wrap: anywhere 允许在任意位置断行；
//   min-width: 0 让 flex/grid 项真的可以收缩到比内容窄。
//
// 后端也做了截断（util.SanitizeUpstreamErrorBody），但前端这层是兜底：任何未来
// 新增的长字符串都不该能破坏布局。
const css = readFileSync(new URL('../styles.css', import.meta.url), 'utf8')
const clean = css.replace(/\/\*[\s\S]*?\*\//g, (comment) => comment.replace(/[^\n]/g, ' '))

// 承载上游/服务端错误原文的容器。新增同类容器时补进这个清单。
const errorBanners = ['.inline-error', '.model-alias-inline-error', '.account-route-sync-message']

function declarations(selector: string): string {
  let body = ''
  for (const [, prelude, block] of clean.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (prelude.split(',').some((part) => part.trim() === selector)) body += `;${block}`
  }
  return body
}

test('错误提示容器必须能折断超长 token', () => {
  const offenders: string[] = []
  for (const selector of errorBanners) {
    const body = declarations(selector)
    // 选择器改名会让这个测试静默失效，必须报错。
    assert.notEqual(body, '', `styles.css 里找不到 ${selector}，选择器改名了就同步改这里`)
    if (!/(?:^|[;\s])overflow-wrap\s*:\s*(?:anywhere|break-word)/.test(body)) {
      offenders.push(`${selector} 缺 overflow-wrap: anywhere`)
    }
    if (!/(?:^|[;\s])min-width\s*:\s*0/.test(body)) {
      offenders.push(`${selector} 缺 min-width: 0`)
    }
  }
  assert.deepEqual(offenders, [], `以下错误提示容器会被超长 token 撑破布局：\n${offenders.join('\n')}`)
})
