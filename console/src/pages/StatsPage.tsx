import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Activity, BarChart3, ChevronDown, CircleDollarSign, Clock3, Gauge, LineChart, List, PieChart, RefreshCw, WalletCards, Zap } from 'lucide-react'
import { getDashboard, getStats, getStatsFilterOptions } from '../api'
import type { DashboardRange, DashboardSnapshot, MetricPoint, SiteBalanceHistoryPoint, StatsEntry, StatsFilterOptions, StatsSnapshot } from '../types'
import { donutSlicePath, donutSlices } from './donutGeometry'
import { EmptyState, ErrorState, formatMoney, formatNumber, LoadingState, PageHeader, successTone } from './shared'

export default function StatsPage() {
  const [snapshot, setSnapshot] = useState<StatsSnapshot | null>(null)
  const [dashboard, setDashboard] = useState<DashboardSnapshot | null>(null)
  const [options, setOptions] = useState<StatsFilterOptions>({ channel_names: [], models: [] })
  const [range, setRange] = useState<DashboardRange>('today')
  const [channel, setChannel] = useState('')
  const [model, setModel] = useState('')
  const [chartMode, setChartMode] = useState<TrendChartMode>('curve')
  const [detailsOpen, setDetailsOpen] = useState(false)
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
      const [data, filterOptions, dashboardData] = await Promise.all([
        getStats({ range, channel_name: channel, model }, signal),
        getStatsFilterOptions(range, signal),
        getDashboard({ range, channel_name: channel, model }, signal),
      ])
      if (sequence !== loadSequence.current) return
      setSnapshot(data)
      setOptions(filterOptions)
      setDashboard(dashboardData)
    }
    catch (reason) { if (!signal?.aborted && sequence === loadSequence.current) setError(reason instanceof Error ? reason.message : '数据面板加载失败') }
    finally { if (!signal?.aborted && sequence === loadSequence.current) setLoading(false) }
  }, [channel, model, range])

  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort() }, [load])
  const entries = snapshot?.stats || []
  const totals = useMemo(() => sumStats(entries), [entries])
  const totalTokens = (dashboard?.totals.input_tokens || 0) + (dashboard?.totals.output_tokens || 0)

  return (
    <div className="workspace-page data-dashboard-page">
      <PageHeader
        icon={BarChart3}
        title="数据面板"
        tone="coral"
        actions={<>
          <div className="range-control" role="radiogroup" aria-label="数据面板时间范围">
            {([['today', '今日'], ['this_week', '本周'], ['this_month', '本月']] as const).map(([value, label]) => <button className={range === value ? 'is-active' : ''} type="button" role="radio" aria-checked={range === value} onClick={() => { setChannel(''); setModel(''); setRange(value) }} key={value}>{label}</button>)}
          </div>
          <button className="icon-button icon-button--surface" type="button" disabled={refreshing} onClick={async () => { setRefreshing(true); try { await load() } finally { setRefreshing(false) } }} aria-label="刷新数据面板"><RefreshCw size={17} className={refreshing ? 'spin' : undefined} /></button>
        </>}
      />

      <div className="filter-bar">
        <select value={channel} onChange={(event) => setChannel(event.target.value)} aria-label="数据面板渠道"><option value="">全部渠道</option>{options.channel_names.map((item) => <option key={item}>{item}</option>)}</select>
        <select value={model} onChange={(event) => setModel(event.target.value)} aria-label="数据面板模型"><option value="">全部模型</option>{options.models.map((item) => <option key={item}>{item}</option>)}</select>
        {(channel || model) && <button className="icon-button icon-button--surface" type="button" onClick={() => { setChannel(''); setModel('') }} aria-label="清除数据面板筛选" title="清除筛选"><RefreshCw size={15} /></button>}
      </div>

      {snapshot && dashboard && <section className="stat-kpis">
        <MiniKPI icon={Zap} label="请求总量" value={formatNumber(totals.total)} meta={`${formatNumber(totals.success)} 成功`} />
        <MiniKPI icon={Gauge} label="成功率" value={totals.total ? `${(totals.success / totals.total * 100).toFixed(1)}%` : '—'} meta={`${formatNumber(totals.error)} 失败`} />
        <MiniKPI icon={Activity} label="Token 总量" value={formatNumber(totalTokens)} meta={`${formatNumber(dashboard.totals.cache_read_tokens)} 缓存命中`} />
        <MiniKPI icon={CircleDollarSign} label="有效费用" value={formatMoney(totals.cost)} meta={`标准成本 ${formatMoney(totals.standardCost)}`} />
      </section>}

      {dashboard ? <section className="trend-workbench data-dashboard-trend">
        <header>
          <div><LineChart size={18} /><h2>用量走势</h2></div>
          <div className="trend-chart-toggle" role="group" aria-label="趋势图形式">
            <button type="button" className={chartMode === 'curve' ? 'is-active' : ''} onClick={() => setChartMode('curve')} aria-pressed={chartMode === 'curve'} title="曲线视图"><LineChart size={15} /><span>曲线图</span></button>
            <button type="button" className={chartMode === 'bars' ? 'is-active' : ''} onClick={() => setChartMode('bars')} aria-pressed={chartMode === 'bars'} title="柱状视图"><BarChart3 size={15} /><span>柱状图</span></button>
          </div>
        </header>
        <CombinedTrendChart points={dashboard.trend} mode={chartMode} />
        <div className="data-dashboard-sampling"><Clock3 size={13} /><span>采样间隔 {formatInterval(averageTrendInterval(dashboard.trend))} · {dashboard.trend.length} 个时间点</span></div>
      </section> : null}

      {!loading && !error && entries.length > 0 ? <StatsDistribution entries={entries} /> : null}

      {snapshot?.balance_history?.length ? <BalanceHistoryPanel points={snapshot.balance_history} /> : snapshot ? <section className="balance-history-empty" aria-label="余额变化等待数据">
        <span><WalletCards size={18} /></span>
        <div><strong>余额变化等待数据</strong><span>成功刷新账号余额后，从第二天开始可对比每日变化。</span></div>
      </section> : null}

      {loading ? <LoadingState label="正在计算数据面板" /> : error ? <ErrorState message={error} retry={() => void load()} /> : entries.length === 0 ? <EmptyState label="当前范围暂无统计数据" /> : (
        <section className={`stats-records-wrap${detailsOpen ? ' is-open' : ''}`}>
          <button className="stats-records-toggle" type="button" aria-expanded={detailsOpen} onClick={() => setDetailsOpen((open) => !open)}>
            <span><List size={16} /><strong>渠道 / 模型明细</strong><em>{entries.length} 项</em></span>
            <span>成功率、延迟、RPM 与费用<ChevronDown size={16} /></span>
          </button>
          {detailsOpen && <div className="records-panel stats-records">
            <div className="record-head stats-grid"><span>渠道 / 模型</span><span>健康</span><span>成功 / 失败</span><span>首字 / 总耗时</span><span>RPM</span><span>Token</span><span>费用</span></div>
            {entries.map((entry, index) => <StatsRow entry={entry} key={`${entry.channel_id}-${entry.model}-${index}`} />)}
          </div>}
        </section>
      )}
    </div>
  )
}
function MiniKPI({ icon: Icon, label, value, meta }: { icon: typeof Zap; label: string; value: string; meta: string }) {
  return <article><span><Icon size={17} /></span><div><small>{label}</small><strong>{value}</strong><em>{meta}</em></div></article>
}

type DistributionMode = 'cost' | 'requests'
type DistributionItem = { label: string; value: number; share: number; isOther: boolean }

// 环形图配色：只取主题里的强调色与两处混色，8 套主题明暗切换都能自动跟随；
// 最后一档固定给「其他」，用中性色表示不参与对比的尾部长尾。
const PIE_COLORS = [
  'var(--green)',
  'var(--blue)',
  'var(--amber)',
  'var(--coral)',
  'color-mix(in srgb, var(--green) 46%, var(--blue))',
  'color-mix(in srgb, var(--coral) 52%, var(--amber))',
  'var(--text-muted)',
]

function StatsDistribution({ entries }: { entries: StatsEntry[] }) {
  const costTotal = entries.reduce((sum, entry) => sum + (entry.effective_cost ?? entry.total_cost ?? 0), 0)
  const mode: DistributionMode = costTotal > 0 ? 'cost' : 'requests'
  const channelItems = buildDistribution(entries, (entry) => entry.channel_name, mode)
  const modelItems = buildDistribution(entries, (entry) => entry.model, mode)
  return <section className="stats-distribution" aria-label="用量分布">
    <DistributionPanel icon={BarChart3} title="渠道用量" items={channelItems} mode={mode} />
    <DistributionPanel icon={Activity} title="模型用量" items={modelItems} mode={mode} />
  </section>
}

function DistributionPanel({ icon: Icon, title, items, mode }: { icon: typeof BarChart3; title: string; items: DistributionItem[]; mode: DistributionMode }) {
  // 默认饼图：先回答「谁占大头」；条形列表保留给需要精确排序对比的场景。
  const [view, setView] = useState<'pie' | 'list'>('pie')
  return <article>
    <header>
      <Icon size={17} />
      <h2>{title}</h2>
      <div className="stats-distribution-toggle" role="group" aria-label={`${title}展示方式`}>
        <button type="button" className={view === 'pie' ? 'is-active' : ''} onClick={() => setView('pie')} aria-pressed={view === 'pie'} title="占比饼图"><PieChart size={14} /></button>
        <button type="button" className={view === 'list' ? 'is-active' : ''} onClick={() => setView('list')} aria-pressed={view === 'list'} title="条形列表"><List size={14} /></button>
      </div>
    </header>
    {view === 'pie' ? <DistributionPie items={items} mode={mode} /> : <DistributionList items={items} mode={mode} />}
  </article>
}

function DistributionPie({ items, mode }: { items: DistributionItem[]; mode: DistributionMode }) {
  const [hovered, setHovered] = useState<number | null>(null)
  const total = items.reduce((sum, item) => sum + item.value, 0)
  const radius = 33
  const slices = donutSlices(items, (item) => item.share).map((slice, index) => ({
    ...slice.item,
    index,
    start: slice.start,
    share: slice.share,
    color: PIE_COLORS[index % PIE_COLORS.length],
  }))
  const active = hovered == null ? null : slices[hovered]
  return <div className="stats-distribution-pie">
    <div className="stats-distribution-donut">
      <svg viewBox="0 0 100 100" role="img" aria-label="占比环形图">
        {slices.map((slice) => <path
          className={`stats-distribution-slice${hovered === slice.index ? ' is-active' : ''}`}
          key={slice.label}
          d={donutSlicePath(slice.start, slice.share, radius)}
          stroke={slice.color}
          onMouseEnter={() => setHovered(slice.index)}
          onMouseLeave={() => setHovered(null)}
        />)}
      </svg>
      <div className="stats-distribution-center">
        {active ? <>
          <strong title={active.label}>{active.label}</strong>
          <b>{formatDistributionValue(active.value, mode)}</b>
          <em>{(active.share * 100).toFixed(1)}%</em>
        </> : <>
          <strong>合计</strong>
          <b>{formatDistributionValue(total, mode)}</b>
          <em>{items.length} 项</em>
        </>}
      </div>
    </div>
    <ul className="stats-distribution-legend">
      {slices.map((slice) => <li
        className={hovered === slice.index ? 'is-active' : ''}
        key={slice.label}
        onMouseEnter={() => setHovered(slice.index)}
        onMouseLeave={() => setHovered(null)}
      >
        <i style={{ background: slice.color }} aria-hidden="true" />
        <div><span title={slice.label}>{slice.label}</span><b>{formatDistributionValue(slice.value, mode)}</b></div>
        <em>{(slice.share * 100).toFixed(1)}%</em>
      </li>)}
    </ul>
  </div>
}

type TrendChartMode = 'curve' | 'bars'
type TrendMetric = 'requests' | 'tokens' | 'cost'

function CombinedTrendChart({ points, mode }: { points: MetricPoint[]; mode: TrendChartMode }) {
  const [hovered, setHovered] = useState<number | null>(null)
  const series = useMemo(() => points.map((point) => ({
    point,
    requests: (point.success || 0) + (point.error || 0),
    tokens: (point.input_tokens || 0) + (point.output_tokens || 0),
    cost: point.effective_cost || 0,
  })), [points])
  const maxima = useMemo(() => ({
    requests: Math.max(...series.map((entry) => entry.requests), 1),
    tokens: Math.max(...series.map((entry) => entry.tokens), 1),
    cost: Math.max(...series.map((entry) => entry.cost), 1),
  }), [series])
  // 曲线视图只做轻度平滑，悬浮提示和柱状图仍使用原始值，不改变事实。
  const smoothed = useMemo(() => ({
    requests: smoothSeries(series.map((entry) => entry.requests)),
    tokens: smoothSeries(series.map((entry) => entry.tokens)),
    cost: smoothSeries(series.map((entry) => entry.cost)),
  }), [series])
  const curvePaths = useMemo(() => ({
    requests: smoothCurvePath(smoothed.requests, maxima.requests),
    tokens: smoothCurvePath(smoothed.tokens, maxima.tokens),
    cost: smoothCurvePath(smoothed.cost, maxima.cost),
  }), [maxima, smoothed])
  if (!points.length) return <div className="content-state content-state--empty">当前范围暂无趋势数据</div>
  const hoveredEntry = hovered == null ? null : series[hovered]
  const tooltipLeft = Math.min(88, Math.max(12, ((hovered ?? 0) + .5) / series.length * 100))
  return <div className="combined-trend-chart">
    <div className="combined-trend-legend" aria-label="趋势指标图例">
      <span><i className="combined-trend-dot combined-trend-dot--requests" />请求量 <b>峰值 {formatMetric(maxima.requests, 'requests')}</b></span>
      <span><i className="combined-trend-dot combined-trend-dot--tokens" />Token <b>峰值 {formatMetric(maxima.tokens, 'tokens')}</b></span>
      <span><i className="combined-trend-dot combined-trend-dot--cost" />费用 <b>峰值 {formatMetric(maxima.cost, 'cost')}</b></span>
    </div>
    <div className={`combined-trend-plot${mode === 'curve' ? ' combined-trend-plot--curve' : ''}`} role="img" aria-label="请求量、Token 与费用组合趋势图">
      <div className="combined-trend-grid" aria-hidden="true">
        {[100, 75, 50, 25, 0].map((level) => <span key={level} style={{ bottom: `${level}%` }} />)}
      </div>
      {mode === 'curve' ? <>
        <svg className="combined-trend-svg" viewBox="0 0 1000 320" preserveAspectRatio="none" aria-hidden="true">
          <path className="combined-trend-curve combined-trend-curve--requests" d={curvePaths.requests} />
          <path className="combined-trend-curve combined-trend-curve--tokens" d={curvePaths.tokens} />
          <path className="combined-trend-curve combined-trend-curve--cost" d={curvePaths.cost} />
          {hovered != null && ([
            ['requests', smoothed.requests[hovered], maxima.requests],
            ['tokens', smoothed.tokens[hovered], maxima.tokens],
            ['cost', smoothed.cost[hovered], maxima.cost],
          ] as const).map(([metric, value, max]) => <circle
            key={metric}
            className={`combined-trend-point combined-trend-point--${metric}`}
            cx={curveX(hovered, series.length)}
            cy={curveY(value, max)}
            r={5}
          />)}
        </svg>
        <div className="combined-trend-hover-columns">
          {series.map((entry, index) => <div
            className={`combined-trend-hover-column${hovered === index ? ' is-active' : ''}`}
            key={`${entry.point.ts}-${index}`}
            tabIndex={0}
            aria-label={`${formatPointTime(entry.point.ts)}，请求量 ${formatMetric(entry.requests, 'requests')} 次，Token ${formatMetric(entry.tokens, 'tokens')}，费用 ${formatMetric(entry.cost, 'cost')}`}
            onMouseEnter={() => setHovered(index)}
            onMouseLeave={() => setHovered(null)}
            onFocus={() => setHovered(index)}
            onBlur={() => setHovered(null)}
          />)}
        </div>
      </> : <div className="combined-trend-buckets">
        {series.map((entry, index) => <div
          className={`combined-trend-bucket${hovered === index ? ' is-active' : ''}`}
          key={`${entry.point.ts}-${index}`}
          tabIndex={0}
          aria-label={`${formatPointTime(entry.point.ts)}，请求量 ${formatMetric(entry.requests, 'requests')} 次，Token ${formatMetric(entry.tokens, 'tokens')}，费用 ${formatMetric(entry.cost, 'cost')}`}
          onMouseEnter={() => setHovered(index)}
          onMouseLeave={() => setHovered(null)}
          onFocus={() => setHovered(index)}
          onBlur={() => setHovered(null)}
        >
          <span className="combined-trend-bar combined-trend-bar--requests" style={{ height: `${barHeight(entry.requests, maxima.requests)}%` }} />
          <span className="combined-trend-bar combined-trend-bar--tokens" style={{ height: `${barHeight(entry.tokens, maxima.tokens)}%` }} />
          <span className="combined-trend-bar combined-trend-bar--cost" style={{ height: `${barHeight(entry.cost, maxima.cost)}%` }} />
        </div>)}
      </div>}
      {hoveredEntry && <div className="combined-trend-tooltip" style={{ left: `${tooltipLeft}%` }}>
        <strong>{formatPointTime(hoveredEntry.point.ts)}</strong>
        <span><i className="combined-trend-dot combined-trend-dot--requests" />请求量<b>{formatMetric(hoveredEntry.requests, 'requests')} 次</b></span>
        <span><i className="combined-trend-dot combined-trend-dot--tokens" />Token<b>{formatMetric(hoveredEntry.tokens, 'tokens')}</b></span>
        <span><i className="combined-trend-dot combined-trend-dot--cost" />费用<b>{formatMetric(hoveredEntry.cost, 'cost')}</b></span>
      </div>}
    </div>
    <div className="combined-trend-axis"><span>{formatPointTime(points[0].ts)}</span><span>{formatPointTime(points[points.length - 1].ts)}</span></div>
  </div>
}

function formatDistributionValue(value: number, mode: DistributionMode): string {
  return mode === 'cost' ? formatMoney(value) : formatNumber(value)
}

function buildDistribution(entries: StatsEntry[], key: (entry: StatsEntry) => string, mode: DistributionMode): DistributionItem[] {
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

function DistributionList({ items, mode }: { items: DistributionItem[]; mode: DistributionMode }) {
  return <div className="stats-distribution-list">
    {items.map((item, index) => <div key={item.label}>
      <span className="stats-distribution-rank">{index + 1}</span>
      <div>
        <strong title={item.label}>{item.label}</strong>
        <i><b style={{ width: `${Math.max(3, item.share * 100)}%` }} /></i>
      </div>
      <span>{formatDistributionValue(item.value, mode)}</span>
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

function sumStats(entries: StatsEntry[]) {
  return entries.reduce((sum, item) => ({
    total: sum.total + item.total,
    success: sum.success + item.success,
    error: sum.error + item.error,
    tokens: sum.tokens + (item.total_input_tokens || 0) + (item.total_output_tokens || 0),
    cost: sum.cost + (item.effective_cost ?? item.total_cost ?? 0),
    standardCost: sum.standardCost + (item.total_cost || 0),
  }), { total: 0, success: 0, error: 0, tokens: 0, cost: 0, standardCost: 0 })
}


function formatMetric(value: number, metric: TrendMetric): string { return metric === 'cost' ? formatMoney(value) : formatNumber(value) }
function barHeight(value: number, max: number): number { return value <= 0 ? 1 : Math.max(4, value / max * 100) }
function curveX(index: number, count: number): number { return count <= 1 ? 500 : 30 + (index / (count - 1)) * 940 }
function curveY(value: number, max: number): number { return 284 - Math.min(1, Math.max(0, value / max)) * 238 }
function smoothCurvePath(values: number[], max: number): string {
  if (!values.length) return ''
  const points = values.map((value, index) => ({ x: curveX(index, values.length), y: curveY(value, max) }))
  if (points.length === 1) return `M ${points[0].x.toFixed(2)} ${points[0].y.toFixed(2)} L ${(points[0].x + 1).toFixed(2)} ${points[0].y.toFixed(2)}`
  const tension = 1 / 6
  return points.slice(1).reduce((path, point, index) => {
    const previous = points[index]
    const before = points[index - 1] ?? previous
    const after = points[index + 2] ?? point
    const controlX1 = previous.x + (point.x - before.x) * tension
    const controlY1 = clampPlotY(previous.y + (point.y - before.y) * tension)
    const controlX2 = point.x - (after.x - previous.x) * tension
    const controlY2 = clampPlotY(point.y - (after.y - previous.y) * tension)
    return `${path} C ${controlX1.toFixed(2)} ${controlY1.toFixed(2)}, ${controlX2.toFixed(2)} ${controlY2.toFixed(2)}, ${point.x.toFixed(2)} ${point.y.toFixed(2)}`
  }, `M ${points[0].x.toFixed(2)} ${points[0].y.toFixed(2)}`)
}
function clampPlotY(value: number): number { return Math.min(306, Math.max(8, value)) }
const CURVE_SMOOTH_PASSES = 3
function smoothSeries(values: number[], passes = CURVE_SMOOTH_PASSES): number[] {
  let result = values.slice()
  for (let pass = 0; pass < passes; pass += 1) result = smoothOnce(result)
  return result
}
function smoothOnce(values: number[]): number[] {
  if (values.length < 4) return values.slice()
  const kernel = [1, 2, 3, 2, 1]
  return values.map((value, index) => {
    let sum = 0
    let weight = 0
    for (let offset = 0; offset < kernel.length; offset += 1) {
      const source = index + offset - 2
      sum += (source >= 0 && source < values.length ? values[source] : value) * kernel[offset]
      weight += kernel[offset]
    }
    return sum / weight
  })
}
function formatPointTime(value: string): string { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(date) }
function averageTrendInterval(points: MetricPoint[]): number {
  if (points.length < 2) return 0
  const timestamps = points.map((point) => new Date(point.ts).getTime()).filter((value) => Number.isFinite(value))
  if (timestamps.length < 2) return 0
  const gaps = timestamps.slice(1).map((value, index) => value - timestamps[index]).filter((value) => value > 0)
  return gaps.length ? gaps.reduce((sum, value) => sum + value, 0) / gaps.length / 1000 : 0
}
function formatInterval(seconds: number): string { if (!seconds) return '—'; if (seconds >= 3600) return `${Math.round(seconds / 3600)} 小时`; if (seconds >= 60) return `${Math.round(seconds / 60)} 分钟`; return `${Math.round(seconds)} 秒` }
