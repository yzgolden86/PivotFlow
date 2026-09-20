// 「自动签到」列那行小字的构成规则。
//
// 这一格原来把「时区 · 签到方式提示」直接拼在一起，两个问题：
//   1. 时区排在最前面。绝大多数站点就是 Asia/Shanghai，和浏览器同一时区，
//      这个字符串不携带任何信息，却把真正要看的状态（如「需人机验证」）挤到
//      列宽之外被省略号吃掉。
//   2. 列宽只有 130px，两个字符串一拼必然截断。
//
// 规则改成：**状态在前，时区只在它确实和本地不同时才出现**；两者无论显示与否
// 都会进 tooltip，所以信息不会丢，只是不再抢占版面。
//
// 放在 .ts 而不是页面里，是为了能被 node --test 直接跑到（测试运行器不做 JSX 转译）。

export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
}

// 与本地时区相同（或为空）时返回空串：显示它等于没显示，只会挤掉状态。
export function checkinTimezoneLabel(timezone: string | undefined, localTimezone: string): string {
  const zone = (timezone || '').trim()
  if (!zone) return ''
  const local = (localTimezone || '').trim()
  if (local && zone.toLowerCase() === local.toLowerCase()) return ''
  return zone
}

// 单元格里实际显示的那行小字。两段都为空时给「—」，与其它列的缺省写法一致。
export function checkinScheduleDetail(hint: string, timezone: string | undefined, localTimezone: string): string {
  const parts = [hint, checkinTimezoneLabel(timezone, localTimezone)].filter(Boolean)
  return parts.length ? parts.join(' · ') : '—'
}

// 悬浮提示：始终给全量信息，包含被省略的时区。
export function checkinScheduleTitle(hint: string, timezone: string | undefined, localTimezone: string): string {
  const zone = (timezone || '').trim()
  const parts: string[] = []
  if (hint) parts.push(hint)
  if (zone) {
    const local = (localTimezone || '').trim()
    parts.push(local && zone.toLowerCase() === local.toLowerCase() ? `${zone}（与本地时区相同）` : zone)
  }
  return parts.length ? parts.join(' · ') : '暂无签到状态信息'
}
