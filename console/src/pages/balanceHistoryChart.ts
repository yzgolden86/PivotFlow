import type { SiteBalanceHistoryPoint } from '../types.ts'

// Optional until older API responses and the shared types receive daily sampling.
// Missing fields provide no authority to infer a change from balances or counts.
export type BalanceHistoryInput = Omit<SiteBalanceHistoryPoint, 'balance' | 'daily_delta' | 'known_accounts' | 'comparison_status' | 'scheduled_at'> & {
  balance: number | null
  daily_delta?: number | null
  known_accounts?: number
  comparison_status?: string
  scheduled_at?: number
}

export interface BalanceChartPoint {
  day: string
  balance: number | null
  accounts: number | null
  knownAccounts: number | null
  comparisonStatus: string
  scheduledAt: number | null
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

export function balanceScheduleCaption(value: unknown): string {
  if (typeof value !== 'object' || value == null || Array.isArray(value)) return '采样时间未提供'
  const schedule = value as { enabled?: unknown; time?: unknown; timezone?: unknown }
  if (schedule.enabled === false) return '自动采样已关闭'
  if (schedule.enabled !== true || typeof schedule.time !== 'string' || !/^(?:[01]\d|2[0-3]):[0-5]\d$/.test(schedule.time) || typeof schedule.timezone !== 'string' || !schedule.timezone.trim()) return '采样时间未提供'
  return `每日 ${schedule.time} · ${schedule.timezone === 'Asia/Shanghai' ? '北京时间' : schedule.timezone}`
}

export function dailyComparisonLabel(point: BalanceChartPoint): string {
  if (point.comparisonStatus === 'complete') {
    return point.dailyDelta == null ? '日差额无效' : point.dailyDelta > 0 ? '净增加' : point.dailyDelta < 0 ? '净减少' : '无净变化'
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

/** Only the server can establish exact consecutive-day cohort comparability. */
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
    else if (observedAt === previousAt && (point.daily_delta !== previous.daily_delta || point.comparison_status !== previous.comparison_status || point.balance !== previous.balance || point.accounts !== previous.accounts || point.known_accounts !== previous.known_accounts || point.scheduled_at !== previous.scheduled_at)) {
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
      const dailyDelta = point.comparison_status === 'complete' ? finite(point.daily_delta) : null
      return { day: point.day, balance: finite(point.balance), accounts: accountCount(point.accounts),
        knownAccounts: accountCount(point.known_accounts), comparisonStatus: point.comparison_status ?? 'unavailable', scheduledAt: finite(point.scheduled_at),
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
