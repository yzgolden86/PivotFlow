import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Activity, BarChart3, CircleDollarSign, Gauge, RefreshCw, WalletCards, Zap } from 'lucide-react'
import { getStats, getStatsFilterOptions } from '../api'
import HelpTip from '../components/HelpTip'
import type { DashboardRange, SiteBalanceHistoryPoint, StatsEntry, StatsFilterOptions, StatsSnapshot } from '../types'
import { EmptyState, ErrorState, formatMoney, formatNumber, LoadingState, PageHeader, successTone } from './shared'

export default function StatsPage() {
  const [snapshot, setSnapshot] = useState<StatsSnapshot | null>(null)
  const [options, setOptions] = useState<StatsFilterOptions>({ channel_names: [], models: [] })
  const [range, setRange] = useState<DashboardRange>('today')
  const [channel, setChannel] = useState('')
  const [model, setModel] = useState('')
  const loadSequence = useRef(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [refreshing, setRefreshing] = useState(false)

  // 手动刷新不带 AbortSignal，切换区间/筛选时旧请求不会被取消，
  // 因此按序号丢弃过期响应，避免旧区间的数字落在新筛选标签下。
  const load = useCallback(async (signal?: AbortSignal) => {
    const sequence = ++loadSequence.current
    setLoading(true); setError('')
    try {
      const [data, filterOptions] = await Promise.all([
        getStats({ range, channel_name: channel, model }, signal),
        getStatsFilterOptions(range, signal),
      ])
      if (sequence !== loadSequence.current) return
      setSnapshot(data)
      setOptions(filterOptions)
    }
    catch (reason) { if (!signal?.aborted && sequence === loadSequence.current) setError(reason instanceof Error ? reason.message : '统计加载失败') }
    finally { if (!signal?.aborted && sequence === loadSequence.current) setLoading(false) }
  }, [channel, model, range])

  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort() }, [load])
  const entries = snapshot?.stats || []
  const totals = useMemo(() => sumStats(entries), [entries])

  return (
    <div className="workspace-page">
      <PageHeader
        icon={BarChart3}
        title="用量统计"
        actions={<button className="icon-button icon-button--surface" type="button" disabled={refreshing} onClick={async () => { setRefreshing(true); try { await load() } finally { setRefreshing(false) } }} aria-label="刷新统计"><RefreshCw size={17} className={refreshing ? 'spin' : undefined} /></button>}
      />

      <div className="filter-bar">
        <select value={range} onChange={(event) => { setChannel(''); setModel(''); setRange(event.target.value as DashboardRange) }} aria-label="统计时间范围"><option value="today">今日</option><option value="this_week">本周</option><option value="this_month">本月</option></select>
        <select value={channel} onChange={(event) => setChannel(event.target.value)} aria-label="统计渠道"><option value="">全部渠道</option>{options.channel_names.map((item) => <option key={item}>{item}</option>)}</select>
        <select value={model} onChange={(event) => setModel(event.target.value)} aria-label="统计模型"><option value="">全部模型</option>{options.models.map((item) => <option key={item}>{item}</option>)}</select>
      </div>

      {snapshot && <section className="stat-kpis">
        <MiniKPI icon={Zap} label="请求总量" value={formatNumber(totals.total)} meta={`${formatNumber(totals.success)} 成功`} />
        <MiniKPI icon={Gauge} label="成功率" value={totals.total ? `${(totals.success / totals.total * 100).toFixed(1)}%` : '—'} meta={`${formatNumber(totals.error)} 失败`} />
        <MiniKPI icon={Activity} label="峰值 RPM" value={formatNumber(snapshot.rpm_stats?.peak_rpm, 1)} meta={`平均 ${formatNumber(snapshot.rpm_stats?.avg_rpm, 1)}`} />
        <MiniKPI icon={CircleDollarSign} label="有效费用" value={formatMoney(totals.cost)} meta={`${formatNumber(totals.tokens)} tokens`} />
      </section>}

      {snapshot?.balance_history?.length ? <BalanceHistoryPanel points={snapshot.balance_history} /> : snapshot ? <section className="balance-history-empty" aria-label="余额趋势等待数据">
        <span><WalletCards size={18} /></span>
        <div><strong>余额趋势等待数据</strong><span>成功刷新账号余额后会生成每日快照；从第二天开始可对比变化。</span></div>
      </section> : null}

      {loading ? <LoadingState label="正在计算用量统计" /> : error ? <ErrorState message={error} retry={() => void load()} /> : entries.length === 0 ? <EmptyState label="当前范围暂无统计数据" /> : (
        <div className="records-panel stats-records">
          <div className="record-head stats-grid"><span>渠道 / 模型</span><span>健康</span><span>成功 / 失败</span><span>首字 / 总耗时</span><span>RPM</span><span>Token</span><span>费用</span></div>
          {entries.map((entry, index) => <StatsRow entry={entry} key={`${entry.channel_id}-${entry.model}-${index}`} />)}
        </div>
      )}
    </div>
  )
}

function MiniKPI({ icon: Icon, label, value, meta }: { icon: typeof Zap; label: string; value: string; meta: string }) {
  return <article><span><Icon size={17} /></span><div><small>{label}</small><strong>{value}</strong><em>{meta}</em></div></article>
}

function BalanceHistoryPanel({ points }: { points: SiteBalanceHistoryPoint[] }) {
  const groups = new Map<string, SiteBalanceHistoryPoint[]>()
  for (const point of points) {
    const current = groups.get(point.currency) || []
    current.push(point)
    groups.set(point.currency, current)
  }

  return <section className="balance-history-panel" aria-label="余额历史">
    {Array.from(groups.entries()).map(([currency, currencyPoints]) => {
      const latest = currencyPoints[currencyPoints.length - 1]
      const baseline = currencyPoints[0]
      const delta = latest.balance - baseline.balance
      return <article key={currency}>
        <header>
          <div className="heading-with-hint">
            <h2>余额趋势 · {currency}</h2>
            <HelpTip label="余额趋势" text="按账号本地日期汇总，同日多次刷新保留最新值" />
          </div>
          <span>{currencyPoints.length} 天</span>
        </header>
        <div className="balance-history-summary">
          <span><small>最新余额</small><strong>{formatCurrency(latest.balance, currency)}</strong></span>
          <span><small>区间变化</small><em className={delta > 0 ? 'balance-history-delta gain' : delta < 0 ? 'balance-history-delta loss' : 'balance-history-delta'}>{formatSignedCurrency(delta, currency)}</em></span>
          <span><small>覆盖账号</small><strong>{formatNumber(latest.accounts)}</strong></span>
        </div>
        <div className="balance-history-list" role="table" aria-label={`${currency} 每日余额`}>
          <div role="row"><span role="columnheader">日期</span><span role="columnheader">余额汇总</span><span role="columnheader">账号</span></div>
          {[...currencyPoints].reverse().map((point) => <div role="row" key={`${point.day}:${point.currency}`}>
            <span role="cell">{formatDay(point.day)}</span>
            <span role="cell">{formatCurrency(point.balance, point.currency)}</span>
            <span role="cell">{formatNumber(point.accounts)}</span>
          </div>)}
        </div>
      </article>
    })}
  </section>
}

function formatCurrency(value: number, currency: string): string {
  try {
    return new Intl.NumberFormat('zh-CN', { style: 'currency', currency, maximumFractionDigits: 2 }).format(value)
  } catch {
    return `${currency} ${value.toFixed(2)}`
  }
}

function formatSignedCurrency(value: number, currency: string): string {
  const prefix = value > 0 ? '+' : ''
  return prefix + formatCurrency(value, currency)
}

function formatDay(day: string): string {
  const date = new Date(`${day}T00:00:00`)
  return Number.isNaN(date.getTime()) ? day : date.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' })
}

function StatsRow({ entry }: { entry: StatsEntry }) {
  const rate = entry.total ? entry.success / entry.total : 0
  const multiplierLabel = actualMultiplierLabel(entry)
  return <article className="record-row stats-grid">
    <div><strong>{entry.channel_name}</strong><span title={entry.model}>{entry.model}</span></div>
    <div className="health-cell"><HealthMeter rate={rate} hasData={entry.total > 0} /></div>
    <div><strong>{formatNumber(entry.success)} / {formatNumber(entry.error)}</strong><span>共 {formatNumber(entry.total)}</span></div>
    <div><strong>{entry.avg_first_byte_time_seconds ? `${entry.avg_first_byte_time_seconds.toFixed(2)}s` : '—'}</strong><span>{entry.avg_duration_seconds ? `${entry.avg_duration_seconds.toFixed(2)}s` : '—'}</span></div>
    <div><strong>{formatNumber(entry.peak_rpm, 1)}</strong><span>均值 {formatNumber(entry.avg_rpm, 1)}</span></div>
    <div><strong>{formatNumber(entry.total_input_tokens)} / {formatNumber(entry.total_output_tokens)}</strong><span>输入 / 输出</span></div>
    <div><strong>{formatMoney(entry.effective_cost ?? entry.total_cost)}</strong><span>{multiplierLabel}</span></div>
  </article>
}

function actualMultiplierLabel(entry: StatsEntry): string {
  const minimum = entry.actual_cost_multiplier_min
  const maximum = entry.actual_cost_multiplier_max
  if (minimum !== undefined && maximum !== undefined) {
    if (minimum === 0 && maximum === 0) return '免费'
    if (minimum === maximum) return minimum === 1 ? '标准倍率' : `${formatMultiplier(minimum)}x 实际倍率`
    return `${formatMultiplier(minimum)}x–${formatMultiplier(maximum)}x 实际倍率`
  }
  const configured = entry.cost_multiplier
  if (configured === 0) return '免费'
  if (configured !== undefined && configured !== 1) return `${formatMultiplier(configured)}x 倍率`
  return '标准倍率'
}

function formatMultiplier(value: number): string {
  return Number.isInteger(value) ? String(value) : value.toFixed(2).replace(/0+$/, '').replace(/\.$/, '')
}

// 分格显示成功率，细梳状：每格 2%，比粗格更精细，也比连续细条易读。
// 百分比放在右侧同一行，不再占用下方一行。
const healthSegments = 50

function HealthMeter({ rate, hasData }: { rate: number; hasData: boolean }) {
  const percent = rate * 100
  // 向上取整：非零成功率至少点亮一格，否则 3% 看起来和 0% 一样。
  const filled = hasData ? Math.min(healthSegments, Math.ceil(rate * healthSegments)) : 0
  const tone = successTone(rate)
  const label = hasData ? `${percent.toFixed(1)}%` : '—'
  return <span className={`health-meter health-meter--${tone}`} role="img" aria-label={hasData ? `成功率 ${label}` : '暂无数据'}>
    <span className="health-meter-cells">
      {Array.from({ length: healthSegments }, (_, index) => <i className={index < filled ? 'is-on' : undefined} key={index} />)}
    </span>
    <strong>{label}</strong>
  </span>
}

function sumStats(entries: StatsEntry[]) { return entries.reduce((sum, item) => ({ total: sum.total + item.total, success: sum.success + item.success, error: sum.error + item.error, tokens: sum.tokens + (item.total_input_tokens || 0) + (item.total_output_tokens || 0), cost: sum.cost + (item.effective_cost ?? item.total_cost ?? 0) }), { total: 0, success: 0, error: 0, tokens: 0, cost: 0 }) }
