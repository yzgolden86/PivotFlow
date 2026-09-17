import test from 'node:test'
import assert from 'node:assert/strict'
import { needsBrowserCheckin, siteCheckinMethodHint } from './siteCheckinMethod.ts'
import type { Site, SiteAccount } from '../types.ts'

const site = (checkin_method?: string): Pick<Site, 'checkin_method'> => ({ checkin_method })
const account = (last_checkin_status: string): Pick<SiteAccount, 'last_checkin_status'> => ({ last_checkin_status })

test('已探测到的签到能力各给一句提示', () => {
  assert.equal(siteCheckinMethodHint(site('turnstile')), '需人机验证')
  assert.equal(siteCheckinMethodHint(site('disabled')), '站点已关闭签到')
  assert.equal(siteCheckinMethodHint(site('unavailable')), '站点没有签到接口')
})

// 能自动签到的站点不该在界面上多说一句话，尚未探测到也一样留空。
test('能自动签到或尚未探测时不占版面', () => {
  for (const method of ['available', 'unknown', undefined, '']) {
    assert.equal(siteCheckinMethodHint(site(method)), '', `checkin_method=${method}`)
  }
  assert.equal(siteCheckinMethodHint(undefined), '')
})

// 核心回归：签到端点 404 那次的落库状态正是 unsupported。若这里不排除 unavailable，
// New API 系站点会由 BROWSER_CHECKIN_PATHS 兜底拼出 /console/personal，操作列就长出
// 一枚指向不存在页面的「打开签到页」，用户点进去只能扑空。
test('站点没有签到接口时不给浏览器入口', () => {
  assert.equal(needsBrowserCheckin(account('unsupported'), site('unavailable')), false)
  assert.equal(needsBrowserCheckin(account('failed'), site('unavailable')), false)
  // 对照：同样的落库状态，能力未知时仍要给入口——这条回落路径不能被上面的收严误伤。
  assert.equal(needsBrowserCheckin(account('unsupported'), site(undefined)), true)
})

test('站点自己关了签到也不给入口', () => {
  assert.equal(needsBrowserCheckin(account('failed'), site('disabled')), false)
  assert.equal(needsBrowserCheckin(account('unsupported'), site('disabled')), false)
})

// 反向保护：turnstile 是唯一「服务端必然过不去、只能人工」的档，
// 收严 unavailable 时不能把它一起吞掉，否则这一档就再没有入口了。
test('turnstile 站点必须给入口，且与最近一次结果无关', () => {
  for (const status of ['browser_required', 'failed', 'unsupported', 'success', 'already_checked', '']) {
    assert.equal(needsBrowserCheckin(account(status), site('turnstile')), true, `status=${status}`)
  }
})

test('能力未知时回落到最近一次签到结果', () => {
  for (const status of ['browser_required', 'failed', 'unsupported']) {
    assert.equal(needsBrowserCheckin(account(status), site('unknown')), true, `status=${status}`)
  }
  for (const status of ['success', 'already_checked', 'running', 'queued', '']) {
    assert.equal(needsBrowserCheckin(account(status), site('unknown')), false, `status=${status}`)
  }
})
