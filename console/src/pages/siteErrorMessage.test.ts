import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

// browser_required 的文案曾经是「该站点需要打开浏览器完成验证；完成上游页面操作后回到
// 这里重试，系统会识别今日已签到」。这两句都不成立：
//
//   · PivotFlow 跑在 VPS 上，操作员在自己电脑的浏览器里过完挑战，cookie 也到不了 VPS
//     发出的请求，所以「打开浏览器」是死胡同，照做只会浪费时间；
//   · 这个错误码同时用于**余额刷新**，那里根本没有「今日签到」这回事。
//
// 真实原因是上游（阿里云 WAF 一类）短时把人机校验页当成响应返回了。文案要说这件事，
// 而不是指挥操作员去做一件做不到的事。没有 DOM 测试环境，只能在源码层面守。

const shared = readFileSync(new URL('siteShared.tsx', import.meta.url), 'utf8')

function browserRequiredLabel(source: string): string {
  // 同一个文件里还有一张「签到状态」标签表，那里 browser_required 是 '需浏览器'。
  // 要守的是错误文案表，所以从 credential_locked 那一项起算。
  const match = /credential_locked:[^}]*browser_required:\s*'([^']*)'/.exec(source)
  assert.ok(match, 'siteShared.tsx 里找不到 browser_required 的错误文案（改名了就同步改这里）')
  return match[1]
}

test('browser_required 的文案不再让操作员去浏览器里完成验证', () => {
  const label = browserRequiredLabel(shared)
  assert.ok(!label.includes('打开浏览器'), `文案又让人去开浏览器了：${label}`)
  assert.ok(!label.includes('识别今日已签到'), `文案又把「今日签到」塞给余额刷新了：${label}`)
})

test('browser_required 的文案说明这是上游短时的人机校验', () => {
  const label = browserRequiredLabel(shared)
  assert.match(label, /人机校验|WAF/, `文案没说明触发源，操作员只能猜：${label}`)
  assert.match(label, /重试/, `文案没给出可执行的下一步：${label}`)
})
