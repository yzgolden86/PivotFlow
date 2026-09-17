import type { Site } from '../types.ts'

// 只依赖 base_url，所以收窄成 Pick —— 调用方不必为了解析一个链接去凑一个完整 Site，
// 测试里也就不用构造 id/name/platform 那一堆无关字段。
type SiteBase = Pick<Site, 'base_url'>

// 公告里的链接要按站点 base_url 解析：上游给的常是 `/notice/1` 这类站内路径，
// 直接塞进 <a href> 会指到控制台自己的地址上去。
//
// 两个函数的差别只在**允许的协议**：
// - 「查看原文」是跳转入口，只放行 http(s)。
// - 正文里的链接放行 mailto（作者邮箱之类），但同样不放行 javascript:。
export function resolveAnnouncementSourceURL(sourceURL: string | undefined, site?: SiteBase): string | null {
  const raw = sourceURL?.trim()
  if (!raw) return null
  try {
    const base = site?.base_url?.trim()
    // 纯 `/api/...` 路径不拼给站点：它指向的是接口而不是公告页，拼出来只会 404。
    if (base && /^\/api\//i.test(raw)) return base.replace(/\/+$/, '')
    const url = base ? new URL(raw, `${base.replace(/\/+$/, '')}/`) : new URL(raw)
    return ['http:', 'https:'].includes(url.protocol) ? url.toString() : null
  } catch {
    return null
  }
}

export function resolveAnnouncementContentURL(sourceURL: string | undefined, site?: SiteBase): string | null {
  const raw = sourceURL?.trim()
  if (!raw) return null
  try {
    const base = site?.base_url?.trim()
    const url = base ? new URL(raw, `${base.replace(/\/+$/, '')}/`) : new URL(raw)
    return ['http:', 'https:', 'mailto:'].includes(url.protocol) ? url.toString() : null
  } catch {
    return null
  }
}
