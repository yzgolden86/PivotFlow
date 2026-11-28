import type { SiteBalanceHistoryPoint } from '../types.ts'

// Optional for older API responses that predate scheduled balance sampling.
// Missing fields provide no authority to infer a change from balances or counts.
export type BalanceHistoryInput = Omit<SiteBalanceHistoryPoint, 'balance' | 'daily_delta' | 'known_accounts' | 'comparison_status' | 'scheduled_at' | 'change_basis'> & {
  balance: number | null
  daily_delta?: number | null
  known_accounts?: number
  comparison_status?: string
  scheduled_at?: number
  change_basis?: string
}

export interface BalanceChartPoint {
  day: string
  balance: number | null
  accounts: number | null
  knownAccounts: number | null
  comparisonStatus: string
  scheduledAt: number | null
  updatedAt: number | null
  changeBasis: 'first_sample' | 'previous_day' | null
  x: number
  y: number | null
  dailyDelta: number | null
}

export interface BalanceHistoryChart {
  currency: string
  points: BalanceChartPoint[]
  latest: BalanceChartPoint
  paths: string[]
  startDay: string
  endDay: string
  zeroY: number
  ticks: { value: number; y: number }[]
  missingDays: number
  unknownPoints: number
  coverageChanges: number
  baselinePoints: number
  hasData: boolean
  isolatedPoints: BalanceChartPoint[]
}

export interface BalanceHistoryOverlay {
  series: Array<Omit<BalanceHistoryChart, 'latest'> & { latest: BalanceChartPoint | null }>
  days: { day: string; x: number }[]
  startDay: string
  endDay: string
  zeroY: number
}

export type BalanceHistoryDays = 7 | 14
export const BALANCE_SAMPLE_INTERVALS = [15, 30, 60, 120, 360, 720, 1440] as const

export function balanceHistoryOverlay(source: BalanceHistoryInput[], days: BalanceHistoryDays = 14, asOfDay?: string): BalanceHistoryOverlay {
  const groups = dailyGroups(source)
  const dates = Array.from(groups.values()).flatMap((days) => Array.from(days.keys())).sort()
  const anchoredEnd = asOfDay ? dayNumber(asOfDay) : null
  if (!dates.length && anchoredEnd == null) return { series: [], days: [], startDay: '', endDay: '', zeroY: 150 }
  const end = anchoredEnd ?? dayNumber(dates[dates.length - 1])!
  // The requested calendar remains complete even when sampling is sparse.
  // Legacy callers without a server anchor retain their observed date domain.
  const start = anchoredEnd == null ? Math.max(dayNumber(dates[0])!, end - (days - 1)) : end - (days - 1)
  const calendar = Array.from({ length: end - start + 1 }, (_, index) => ({
    day: new Date((start + index) * DAY).toISOString().slice(0, 10), x: dateX(start + index, start, end),
  }))
  const series = Array.from(groups, ([currency, records]) => buildSeries(currency, Array.from(records.values()), start, end))
    .sort((a, b) => a.currency.localeCompare(b.currency))
  return { series, days: calendar, startDay: calendar[0].day, endDay: calendar[calendar.length - 1].day, zeroY: 150 }
}

export function selectBalanceCurrencies(currencies: string[], requested: string[] = []): string[] {
  const priority = [...requested, 'CNY', 'USD', ...currencies]
  return [...new Set(priority.filter((currency) => currencies.includes(currency)))].slice(0, 2)
}

const DAY = 86_400_000

function dayNumber(day: string): number | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) return null
  const timestamp = Date.parse(`${day}T00:00:00Z`)
  return Number.isFinite(timestamp) && new Date(timestamp).toISOString().slice(0, 10) === day ? timestamp / DAY : null
}

function finite(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

export function formatBalanceAmount(value: number | null, currency: string, digits = 2, signed = false): string {
  if (value == null || !Number.isFinite(value)) return '未知'
  const format = (amount: number): string => {
    if (!currency) return `${amount.toFixed(digits)}（币种未知）`
    try {
      return new Intl.NumberFormat('zh-CN', { style: 'currency', currency, minimumFractionDigits: digits, maximumFractionDigits: digits }).format(amount)
    } catch { return `${currency} ${amount.toFixed(digits)}` }
  }
  const floor = 10 ** -digits
  if (value !== 0 && Math.abs(value) < floor) return value < 0 ? `> ${format(-floor)}` : `< ${format(floor)}`
  return (signed && value > 0 ? '+' : '') + format(value === 0 ? 0 : value)
}

function accountCount(value: unknown): number | null {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : null
}

export function formatDailyDelta(value: number | null, currency: string): string {
  if (value == null || !Number.isFinite(value)) return '未知'
  const [mantissa, exponentText] = Math.abs(value).toString().split('e')
  const decimals = Math.max(0, (mantissa.split('.')[1]?.length ?? 0) - Number(exponentText ?? 0))
  if (decimals > 20) return `${value > 0 ? '+' : ''}${value} ${currency || '（币种未知）'}`
  return formatBalanceAmount(value, currency, Math.max(2, decimals), true)
}

export function formatBalanceSampleTime(value: unknown): string {
  const timestamp = finite(value)
  if (timestamp == null || timestamp <= 0) return '实际采样：未知'
  // updated_at is the actual collection timestamp in Unix milliseconds.
  // Never substitute the scheduled slot or infer a time from the chart day.
  const date = new Date(timestamp)
  if (!Number.isFinite(date.getTime())) return '实际采样：未知'
  const parts = new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
  }).formatToParts(date)
  const part = (type: string) => parts.find((item) => item.type === type)?.value ?? ''
  return `实际采样：${part('year')}-${part('month')}-${part('day')} ${part('hour')}:${part('minute')}:${part('second')}（北京时间）`
}

export function balanceScheduleCaption(value: unknown): string {
  if (typeof value !== 'object' || value == null || Array.isArray(value)) return '采样时间未提供'
  const schedule = value as { enabled?: unknown; time?: unknown; timezone?: unknown; interval_minutes?: unknown }
  if (schedule.enabled === false) return '自动采样已关闭'
  if (schedule.enabled !== true || typeof schedule.time !== 'string' || !/^(?:[01]\d|2[0-3]):[0-5]\d$/.test(schedule.time) || typeof schedule.timezone !== 'string' || !schedule.timezone.trim()) return '采样时间未提供'
  const zone = schedule.timezone === 'Asia/Shanghai' ? '北京时间' : schedule.timezone
  if (!Object.hasOwn(schedule, 'interval_minutes')) return `每日 ${schedule.time} · ${zone}`
  const interval = schedule.interval_minutes
  if (typeof interval !== 'number' || !BALANCE_SAMPLE_INTERVALS.some((allowed) => allowed === interval)) return '采样时间未提供'
  if (interval === 1440) return `每日 ${schedule.time} · ${zone}`
  const cadence = interval === 60 ? '每小时' : interval < 60 ? `每 ${interval} 分钟` : `每 ${interval / 60} 小时`
  return `${cadence} · 基准 ${schedule.time} · ${zone}`
}

export function balanceHistoryNotice(charts: BalanceHistoryOverlay['series'], schedule: unknown): {
  state: 'ready' | 'empty' | 'disabled' | 'baseline' | 'incomplete' | 'coverage_changed' | 'missing_previous' | 'unavailable'
  title: string
  description: string
} {
  if (charts.some((chart) => chart.hasData)) return { state: 'ready', title: '', description: '' }
  const caption = balanceScheduleCaption(schedule)
  const hasInterval = typeof schedule === 'object' && schedule != null && Object.hasOwn(schedule, 'interval_minutes')
  const interval = hasInterval ? (schedule as { interval_minutes: unknown }).interval_minutes : undefined
  // Missing metadata predates interval sampling; explicit 1440 still uses the
  // legacy daily sampling window rather than the new intraday sample store.
  const daily = caption !== '采样时间未提供' && (!hasInterval || interval === 1440)
  if (caption === '自动采样已关闭') return { state: 'disabled', title: daily ? '每日余额采样已关闭' : '余额自动采样已关闭',
    description: daily ? '可在系统设置的自动任务中开启。已有余额不代表已保存固定时间的每日采样。'
      : '可在系统设置的自动任务中开启。已有实时余额不代表已保存定时采样；关闭后保留已有历史。' }
  const subdaily = caption !== '采样时间未提供' && !daily
  const latest = charts.flatMap((chart) => chart.latest ? [chart.latest] : [])
  const states = new Set(latest.map((point) => point.comparisonStatus))
  const timing = daily ? `${caption}。这里记录固定采样时点之间的净变化，当天实时余额变化会在下一次成功采样后参与比较。`
    : subdaily ? `${caption}。每天保留截至最新成功采样的净变化，当天实时余额变化会在下一次成功采样后参与比较。`
    : `${caption}。仅展示已确认可比较采样的净变化，缺失记录不会计作零。`
  if (!latest.length) return { state: 'empty', title: '等待首次余额采样',
    description: `${timing} 当前未返回该日期范围的采样记录；请核对采样开关、服务运行时间、站点余额读取能力及服务端历史读取告警。` }
  if (states.has('incomplete')) return { state: 'incomplete', title: '余额采样尚未完整',
    description: `${timing} ${daily ? '本日或前日有账号未成功采样' : '比较所需的采样中有账号未成功读取'}；请检查账号凭证、上游余额查询及服务端采样任务。缺失记录不会计作零。` }
  if (states.has('coverage_changed')) return { state: 'coverage_changed', title: '账号范围已变化，等待连续采样',
    description: `${timing} ${daily ? '连续两天的账号集合不同，不能直接计算日差额' : '比较所需的账号集合不同，不能直接计算净变化'}；相同账号集合连续采样成功后恢复比较。` }
  if (states.has('missing_previous')) return { state: 'missing_previous', title: '已记录余额，等待连续采样',
    description: subdaily ? `${timing} 缺少前一天的可比较采样，不能将隔日余额直接作为单日变化；当天取得可比较的多次采样后可展示日内净变化。`
      : `${timing} 缺少前一天的采样，不能将隔日余额直接作为单日变化。` }
  if (states.size === 1 && states.has('baseline')) return { state: 'baseline', title: '首日余额已记录',
    description: `${timing} 最新基线日期为 ${latest.map((point) => point.day).sort().at(-1)}；${daily ? '还需要下一天相同账号集合的完整记录才能生成第一笔日差额。'
      : subdaily ? '等待下一次相同账号集合的完整采样，当天再次采样成功后即可展示自当日首次采样以来的净变化。'
      : '等待相同账号集合的完整可比较采样；采样间隔尚未确认。'}` }
  return { state: 'unavailable', title: '暂时没有可比较的余额记录',
    description: `${timing} 当前记录不足以计算有效日差额，未将其显示为零。` }
}

export function dailyComparisonLabel(point: BalanceChartPoint): string {
  if (point.comparisonStatus === 'complete' || point.comparisonStatus === 'partial') {
    if (point.dailyDelta == null) return '日差额无效'
    const direction = point.dailyDelta > 0 ? '净增加' : point.dailyDelta < 0 ? '净减少' : '无净变化'
    const basis = point.changeBasis === 'first_sample' ? '自当日首次采样' : point.changeBasis === 'previous_day' ? '较前日最后采样'
      : point.comparisonStatus === 'partial' ? '日内部分时段' : ''
    return basis ? `${direction}（${basis}）` : direction
  }
  const labels: Record<string, string> = {
    baseline: '首日基线，暂无日差额', missing_previous: '缺少前日采样', coverage_changed: '账号范围变化，无法比较',
    incomplete: '采样不完整，差额未知', overflow: '差额超出数值范围',
  }
  return Object.hasOwn(labels, point.comparisonStatus) ? labels[point.comparisonStatus] : '日差额依据未提供'
}

/** Monotone cubics: each interval stays within its two measured values.
 * Coordinates are already normalized, so slopes cannot overflow with huge money.
 * Longer horizontal handles round the shoulders at extrema without moving samples.
 */
function smoothDailyPath(points: BalanceChartPoint[]): string {
  const handleRatio = .42
  const slopes = points.slice(1).map((point, index) => (point.y! - points[index].y!) / (point.x - points[index].x))
  const tangents = points.map((_, index) => {
    if (index === 0) return slopes[0]
    if (index === points.length - 1) return slopes[slopes.length - 1]
    const before = slopes[index - 1]
    const after = slopes[index]
    return before * after <= 0 ? 0 : 2 / (1 / before + 1 / after)
  })
  // Scale the Fritsch–Carlson bound with the longer handles. Both the date and
  // value stay monotone within each interval, and adjacent tangents still agree.
  slopes.forEach((slope, index) => {
    if (slope === 0) { tangents[index] = 0; tangents[index + 1] = 0; return }
    const radius = Math.hypot(tangents[index] / slope, tangents[index + 1] / slope)
    const limit = 1 / handleRatio
    if (radius > limit) {
      tangents[index] *= limit / radius
      tangents[index + 1] *= limit / radius
    }
  })
  const coordinate = (value: number) => value.toFixed(3)
  return points.slice(1).reduce((path, point, index) => {
    const previous = points[index]
    const handle = (point.x - previous.x) * handleRatio
    const low = Math.min(previous.y!, point.y!)
    const high = Math.max(previous.y!, point.y!)
    const clamp = (value: number) => Math.max(low, Math.min(high, value))
    return `${path} C ${coordinate(previous.x + handle)} ${coordinate(clamp(previous.y! + handle * tangents[index]))}, ${coordinate(point.x - handle)} ${coordinate(clamp(point.y! - handle * tangents[index + 1]))}, ${coordinate(point.x)} ${coordinate(point.y!)}`
  }, `M ${coordinate(points[0].x)} ${coordinate(points[0].y!)}`)
}

/** Only the server can establish comparability for a daily or partial interval. */
function dailyGroups(source: BalanceHistoryInput[]): Map<string, Map<string, BalanceHistoryInput>> {
  const groups = new Map<string, Map<string, BalanceHistoryInput>>()
  for (const point of source) {
    if (dayNumber(point.day) == null) continue
    const currency = typeof point.currency === 'string' ? point.currency.trim() : ''
    const days = groups.get(currency) ?? new Map<string, BalanceHistoryInput>()
    const previous = days.get(point.day)
    const observedAt = finite(point.updated_at) ?? 0
    const previousAt = finite(previous?.updated_at) ?? 0
    if (!previous || observedAt > previousAt) days.set(point.day, { ...point })
    else if (observedAt === previousAt && (point.daily_delta !== previous.daily_delta || point.comparison_status !== previous.comparison_status || point.balance !== previous.balance || point.accounts !== previous.accounts || point.known_accounts !== previous.known_accounts || point.scheduled_at !== previous.scheduled_at || point.change_basis !== previous.change_basis)) {
      days.set(point.day, { ...previous, daily_delta: null, comparison_status: 'incomplete' })
    }
    groups.set(currency, days)
  }
  return groups
}

function dateX(day: number, start: number, end: number): number {
  return start === end ? 500 : 80 + (day - start) / (end - start) * 840
}

export function balanceHistoryCharts(source: BalanceHistoryInput[]): BalanceHistoryChart[] {
  return Array.from(dailyGroups(source), ([currency, days]) => {
    const sorted = Array.from(days.values()).sort((a, b) => a.day.localeCompare(b.day))
    const end = dayNumber(sorted[sorted.length - 1].day)!
    const start = Math.max(dayNumber(sorted[0].day)!, end - 13)
    const chart = buildSeries(currency, sorted, start, end)
    return { ...chart, latest: chart.latest! }
  }).sort((a, b) => a.currency.localeCompare(b.currency))
}

function buildSeries(currency: string, source: BalanceHistoryInput[], start: number, end: number): BalanceHistoryOverlay['series'][number] {
    const visible = source.filter((point) => dayNumber(point.day)! >= start && dayNumber(point.day)! <= end)
      .sort((a, b) => a.day.localeCompare(b.day))
    const points: BalanceChartPoint[] = visible.map((point) => {
      const dailyDelta = point.comparison_status === 'complete' || point.comparison_status === 'partial' ? finite(point.daily_delta) : null
      return { day: point.day, balance: finite(point.balance), accounts: accountCount(point.accounts),
        knownAccounts: accountCount(point.known_accounts), comparisonStatus: point.comparison_status ?? 'unavailable', scheduledAt: finite(point.scheduled_at),
        updatedAt: finite(point.updated_at), changeBasis: point.change_basis === 'first_sample' || point.change_basis === 'previous_day' ? point.change_basis : null,
        x: dateX(dayNumber(point.day)!, start, end),
        y: null, dailyDelta }
    })
    const values = points.flatMap((point) => point.dailyDelta == null ? [] : [point.dailyDelta])
    // Divide before scaling, never subtract +/-MAX_VALUE to form the domain.
    const scale = Math.max(...values.map(Math.abs), 0) || 1
    const y = (value: number) => 150 - value / scale * 116
    const paths: string[] = []
    const isolatedPoints: BalanceChartPoint[] = []
    let segment: BalanceChartPoint[] = []
    const missingDays = end - start + 1 - points.length
    const flush = () => {
      if (segment.length > 1) paths.push(smoothDailyPath(segment))
      else if (segment.length === 1) isolatedPoints.push(segment[0])
      segment = []
    }
    points.forEach((point, index) => {
      if (point.dailyDelta != null) point.y = y(point.dailyDelta)
      const previous = points[index - 1]
      if (previous) {
        const gap = dayNumber(point.day)! - dayNumber(previous.day)!
        if (gap !== 1) flush()
      }
      if (point.y == null) flush()
      else segment.push(point)
    })
    flush()
    return {
      currency, points, latest: points[points.length - 1] ?? null, paths, isolatedPoints,
      startDay: new Date(start * DAY).toISOString().slice(0, 10), endDay: new Date(end * DAY).toISOString().slice(0, 10),
      zeroY: y(0),
      ticks: values.length ? values.every((value) => value === 0) ? [{ value: 0, y: 150 }] : [1, .5, 0, -.5, -1].map((fraction) => ({ value: fraction * scale, y: 150 - fraction * 116 })) : [],
      missingDays, coverageChanges: points.filter((point) => point.comparisonStatus === 'coverage_changed').length,
      baselinePoints: points.filter((point) => point.comparisonStatus === 'baseline').length,
      unknownPoints: points.filter((point) => point.dailyDelta == null && point.comparisonStatus !== 'baseline').length,
      hasData: values.length > 0,
    }
}
