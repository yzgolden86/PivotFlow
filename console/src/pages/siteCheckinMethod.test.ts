import test from 'node:test'
import assert from 'node:assert/strict'
import { checkinActionAvailability, needsBrowserCheckin, needsCredentialRecovery, siteCheckinMethodHint } from './siteCheckinMethod.ts'
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

test('凭证恢复只看账号当前状态，不被历史签到失败牵连', () => {
  assert.equal(needsCredentialRecovery({ status: 'healthy', credential_configured: true, last_error: '' }), false)
  assert.equal(needsCredentialRecovery({ status: 'healthy', credential_configured: true, last_error: 'expired' }), true)
  assert.equal(needsCredentialRecovery({ status: 'expired', credential_configured: true, last_error: '' }), true)
  assert.equal(needsCredentialRecovery({ status: 'healthy', credential_configured: false, last_error: '' }), true)
})

test('普通刷新失败保留签到重试，只在明确凭证故障时要求更新', () => {
  for (const last_error of ['provider_timeout', 'provider_rate_limited', 'request_failed', 'invalid_response', 'browser_required', '']) {
    assert.equal(needsCredentialRecovery({ status: 'error', credential_configured: true, last_error }), false, last_error)
  }
  for (const last_error of ['expired', 'credential_required', 'user_id_required: missing upstream identity']) {
    assert.equal(needsCredentialRecovery({ status: 'error', credential_configured: true, last_error }), true, last_error)
  }
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

test('explicit browser check-in stays available when the platform has no server API', () => {
  const actions = checkinActionAvailability(
    { enabled: true, credential_type: 'api_key', last_checkin_status: 'unknown' },
    {
      enabled: true,
      platform: 'openaicompatible',
      base_url: 'https://manual.example',
      external_checkin_url: 'https://manual.example/checkin',
    },
  )
  assert.equal(actions.manualURL, 'https://manual.example/checkin')
  assert.equal(actions.canRunServer, false)
})

test('server check-in remains independent from an explicit manual URL', () => {
  const actions = checkinActionAvailability(
    { enabled: true, credential_type: 'username_password', last_checkin_status: 'unknown' },
    {
      enabled: true,
      platform: 'new-api-family',
      base_url: 'https://api.example',
      external_checkin_url: 'https://api.example/checkin',
    },
  )
  assert.equal(actions.manualURL, 'https://api.example/checkin')
  assert.equal(actions.canRunServer, true)
})

test('an explicit manual URL is independent of a disabled or missing server check-in route', () => {
  for (const checkin_method of ['disabled', 'unavailable']) {
    const actions = checkinActionAvailability(
      { enabled: true, credential_type: 'access_token', last_checkin_status: 'failed' },
      {
        enabled: true,
        platform: 'new-api-family',
        base_url: 'https://api.example',
        external_checkin_url: 'https://api.example/checkin',
        checkin_method,
      },
    )
    assert.equal(actions.manualURL, 'https://api.example/checkin')
    assert.equal(actions.canRunServer, false)
  }
})

test('a disabled or missing server check-in route does not generate a fallback browser URL', () => {
  for (const checkin_method of ['disabled', 'unavailable']) {
    const actions = checkinActionAvailability(
      { enabled: true, credential_type: 'access_token', last_checkin_status: 'failed' },
      { enabled: true, platform: 'new-api-family', base_url: 'https://api.example', checkin_method },
    )
    assert.equal(actions.manualURL, '')
    assert.equal(actions.canRunServer, false)
  }
})

test('a disabled site cannot run a server check-in', () => {
  const actions = checkinActionAvailability(
    { enabled: true, credential_type: 'access_token', last_checkin_status: 'unknown' },
    {
      enabled: false,
      platform: 'new-api-family',
      base_url: 'https://api.example',
    },
  )
  assert.equal(actions.canRunServer, false)
  assert.match(actions.reason, /站点已停用/)
})
