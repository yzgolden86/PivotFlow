import type { Site, SiteAccount } from '../types.ts'

// 站点签到能力的展示与判定。放在 .ts 而不是页面里，是为了能被 node --test 直接跑到
// （测试运行器不做 JSX 转译，import 不了 .tsx）。

// 站点已探测到的签到能力（turnstile / disabled / unavailable / …），只在值得注意时给一句
// 中文提示。能自动签到的站点返回空串，不占版面；尚未探测到也一样留空。
export function siteCheckinMethodHint(site?: Pick<Site, 'checkin_method'>): string {
  if (site?.checkin_method === 'turnstile') return '需人机验证'
  if (site?.checkin_method === 'disabled') return '站点已关闭签到'
  // 真实尝试拿到 404 后记下的：站点对外宣称可签到，实际并不提供这个路由，服务端无解。
  if (site?.checkin_method === 'unavailable') return '站点没有签到接口'
  return ''
}

// 「打开签到页」只在真的需要人工介入时才出现。优先用站点已探测到的签到能力判断：
// turnstile 是确定绕不过去的拦路石，直接给入口；disabled 表示站点自己关了签到，
// unavailable 表示它压根没有这个路由——这两者给了也没用。能力未知（还没探测过，或站点
// 不发布这个字段）时才退回看最近一次签到结果。
// 给所有支持签到的站点都铺这个链接，会把操作列挤满，也让它失去「这里出问题了」的提示意义。
//
// 参数只取用到的那两个字段：既够用，也让调用方（和控制台测试）不必造整个 Site/SiteAccount。
export function needsBrowserCheckin(
  account: Pick<SiteAccount, 'last_checkin_status'>,
  site?: Pick<Site, 'checkin_method'>,
): boolean {
  if (site?.checkin_method === 'turnstile') return true
  if (site?.checkin_method === 'disabled' || site?.checkin_method === 'unavailable') return false
  return ['browser_required', 'failed', 'unsupported'].includes(account.last_checkin_status)
}
