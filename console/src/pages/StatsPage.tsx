import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Activity, BarChart3, CircleDollarSign, Gauge, RefreshCw, WalletCards, Zap } from 'lucide-react'
import { getStats, getStatsFilterOptions } from '../api'
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
      tone="coral"
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

      {!loading && !error && entries.length > 0 ? <StatsDistribution entries={entries} /> : null}

      {snapshot?.balance_history?.length ? <BalanceHistoryPanel points={snapshot.balance_history} /> : snapshot ? <section className="balance-history-empty" aria-label="余额变化等待数据">
        <span><WalletCards size={18} /></span>
        <div><strong>余额变化等待数据</strong><span>成功刷新账号余额后，从第二天开始可对比每日变化。</span></div>
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

function StatsDistribution({ entries }: { entries: StatsEntry[] }) {
  const costTotal = entries.reduce((sum, entry) => sum + (entry.effective_cost ?? entry.total_cost ?? 0), 0)
  const mode = costTotal > 0 ? 'cost' : 'requests'
  const channelItems = buildDistribution(entries, (entry) => entry.channel_name, mode)
  const modelItems = buildDistribution(entries, (entry) => entry.model, mode)
  return <section className="stats-distribution" aria-label="用量分布">
    <article>
      <header><BarChart3 size={17} /><h2>渠道用量</h2></header>
      <DistributionList items={channelItems} mode={mode} />
    </article>
    <article>
      <header><Activity size={17} /><h2>模型用量</h2></header>
      <DistributionList items={modelItems} mode={mode} />
    </article>
  </section>
}

function buildDistribution(entries: StatsEntry[], key: (entry: StatsEntry) => string, mode: 'cost' | 'requests') {
  const totals = new Map<string, number>()
  for (const entry of entries) {
    const value = mode === 'cost' ? (entry.effective_cost ?? entry.total_cost ?? 0) : entry.total
    totals.set(key(entry), (totals.get(key(entry)) || 0) + value)
  }
  const sorted = Array.from(totals.entries()).sort((a, b) => b[1] - a[1])
  const visible = sorted.slice(0, 6)
  const otherTotal = sorted.slice(6).reduce((sum, [, value]) => sum + value, 0)
  if (otherTotal > 0) visible.push(['其他', otherTotal])
  const total = sorted.reduce((sum, [, value]) => sum + value, 0) || 1
  return visible.map(([label, value]) => ({ label, value, share: value / total, isOther: label === '其他' }))
}

function DistributionList({ items, mode }: { items: Array<{ label: string; value: number; share: number; isOther: boolean }>; mode: 'cost' | 'requests' }) {
  return <div className="stats-distribution-list">
    {items.map((item, index) => <div key={item.label}>
      <span className="stats-distribution-rank">{index + 1}</span>
      <div>
        <strong title={item.label}>{item.label}</strong>
        <i><b style={{ width: `${Math.max(3, item.share * 100)}%` }} /></i>
      </div>
      <span>{mode === 'cost' ? formatMoney(item.value) : formatNumber(item.value)}</span>
    </div>)}
  </div>
}

function BalanceHistoryPanel({ points }: { points: SiteBalanceHistoryPoint[] }) {
  const groups = new Map<string, SiteBalanceHistoryPoint[]>()
  for (const point of points) {
    const current = groups.get(point.currency) || []
    current.push(point)
    groups.set(point.currency, current)
  }

  return <section className="balance-change-panel" aria-label="余额变化">
    <header>
      <div><WalletCards size={17} /><h2>余额变化</h2></div>
      <span>最近 14 天</span>
    </header>
    {Array.from(groups.entries()).map(([currency, currencyPoints]) => (
      <BalanceChangeRow currency={currency} points={currencyPoints} key={currency} />
    ))}
  </section>
}

function BalanceChangeRow({ currency, points }: { currency: string; points: SiteBalanceHistoryPoint[] }) {
  const [hovered, setHovered] = useState<number | null>(null)
  const latest = points[points.length - 1]
  const baseline = points[0]
  const delta = latest.balance - baseline.balance
  const deltas = points.slice(1).map((point, index) => ({
    day: point.day,
    balance: point.balance,
    value: point.balance - points[index].balance,
  }))
  const recentDeltas = deltas.slice(-14)
  const maxChange = Math.max(...recentDeltas.map((item) => Math.abs(item.value)), 1)
  const hoveredItem = hovered == null ? null : recentDeltas[hovered]
  const tooltipLeft = Math.min(90, Math.max(10, ((hovered ?? 0) + .5) / Math.max(recentDeltas.length, 1) * 100))
  return <article className="balance-change-row">
    <div className="balance-change-identity">
      <strong>{currency}</strong>
      <span>{formatCurrency(latest.balance, currency)}</span>
    </div>
    <div className="balance-change-bars" role="img" aria-label={`${currency} 每日余额变化`}>
      {recentDeltas.length ? recentDeltas.map((item, index) => <span
        className={`balance-change-cell${hovered === index ? ' is-active' : ''}`}
        key={`${currency}:${item.day}`}
        tabIndex={0}
        aria-label={`${formatDay(item.day)} 变化 ${formatSignedCurrency(item.value, currency)}，余额 ${formatCurrency(item.balance, currency)}`}
        onMouseEnter={() => setHovered(index)}
        onMouseLeave={() => setHovered(null)}
        onFocus={() => setHovered(index)}
        onBlur={() => setHovered(null)}
      >
        <i className={`balance-change-bar ${item.value > 0 ? 'gain' : item.value < 0 ? 'loss' : 'flat'}`} style={{ height: `${item.value === 0 ? 2 : Math.max(7, Math.abs(item.value) / maxChange * 50)}%` }} />
      </span>) : <span className="balance-change-wait">等待次日数据</span>}
      {hoveredItem && <div className="balance-change-tooltip" style={{ left: `${tooltipLeft}%` }}>
        <strong>{formatDay(hoveredItem.day)}</strong>
        <span>变化 <b className={hoveredItem.value > 0 ? 'gain' : hoveredItem.value < 0 ? 'loss' : undefined}>{formatSignedCurrency(hoveredItem.value, currency)}</b></span>
        <span>余额 <b>{formatCurrency(hoveredItem.balance, currency)}</b></span>
      </div>}
    </div>
    <div className="balance-change-meta">
      <strong className={delta > 0 ? 'gain' : delta < 0 ? 'loss' : undefined}>{formatSignedCurrency(delta, currency)}</strong>
      <span>{points.length} 天 · {formatNumber(latest.accounts)} 账号</span>
    </div>
  </article>
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
