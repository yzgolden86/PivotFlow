import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Activity, BarChart3, ChevronDown, CircleDollarSign, Gauge, LineChart, List, PieChart, RefreshCw, WalletCards, Zap } from 'lucide-react'
import { getDashboard, getStats, getStatsFilterOptions, getUsageTrend } from '../api'
import HelpTip from '../components/HelpTip'
import useReadSnapshot from '../components/useReadSnapshot'
import type { CostBasis, CostSource, CostStatus, DashboardRange, DashboardSnapshot, MetricPoint, StatsEntry, StatsFilterOptions, StatsSnapshot } from '../types'
import { donutSlicePath, donutSlices } from './donutGeometry'
import { EmptyState, ErrorState, formatMoney, formatNumber, LoadingState, PageHeader, successTone } from './shared'
import { analyzeCostSummary, COST_BASIS_FILTER_OPTIONS, COST_SOURCE_FILTER_OPTIONS, COST_STATUS_FILTER_OPTIONS } from '../costSemantics.ts'
import { moneyDigits } from '../format.ts'
import { balanceHistoryNotice, balanceHistoryOverlay, balanceScheduleCaption, dailyComparisonLabel, formatBalanceAmount, formatBalanceSampleTime, formatDailyDelta, selectBalanceCurrencies } from './balanceHistoryChart.ts'
import type { BalanceChartPoint, BalanceHistoryDays, BalanceHistoryInput } from './balanceHistoryChart.ts'
import { statsDistribution } from './statsDistribution.ts'
import type { DistributionItem, DistributionMode } from './statsDistribution.ts'
import './statsPresentation.css'

export default function StatsPage() {
  const [snapshot, setSnapshot] = useState<StatsSnapshot | null>(null)
  const [dashboard, setDashboard] = useState<DashboardSnapshot | null>(null)
  const [options, setOptions] = useState<StatsFilterOptions>({ channel_names: [], models: [] })
  const [range, setRange] = useState<DashboardRange>('today')
  const [channel, setChannel] = useState('')
  const [model, setModel] = useState('')
  const [costSource, setCostSource] = useState<CostSource | ''>('')
  const [costStatus, setCostStatus] = useState<CostStatus | ''>('')
  const [costBasis, setCostBasis] = useState<CostBasis | ''>('')
  const [chartMode, setChartMode] = useState<TrendChartMode>('curve')
  const [trendRange, setTrendRange] = useState<DashboardRange>('this_month')
  const [detailsOpen, setDetailsOpen] = useState(false)
  const [balanceDays, setBalanceDays] = useState<BalanceHistoryDays>(14)
  const loadSequence = useRef(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [refreshing, setRefreshing] = useState(false)

  // 手动刷新不带 AbortSignal，切换区间/筛选时旧请求不会被取消，
  // 因此按序号丢弃过期响应，避免旧区间的数字落在新筛选标签下。
  const load = useCallback(async (signal?: AbortSignal) => {
    const sequence = ++loadSequence.current
    setLoading(true); setError('')
    setSnapshot(null)
    setDashboard(null)
    try {
      const [data, filterOptions, dashboardData] = await Promise.all([
        getStats({ range, channel_name: channel, model, cost_source: costSource, cost_status: costStatus, cost_basis: costBasis }, signal),
        getStatsFilterOptions(range, signal),
        getDashboard({ range, channel_name: channel, model, cost_source: costSource, cost_status: costStatus, cost_basis: costBasis }, signal),
      ])
      if (signal?.aborted || sequence !== loadSequence.current) return
      setSnapshot(data)
      setOptions(filterOptions)
      setDashboard(dashboardData)
    }
    catch (reason) { if (!signal?.aborted && sequence === loadSequence.current) setError(reason instanceof Error ? reason.message : '数据面板加载失败') }
    finally { if (!signal?.aborted && sequence === loadSequence.current) setLoading(false) }
  }, [channel, costBasis, costSource, costStatus, model, range])

  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort() }, [load])
  const trendFilters = useMemo(() => ({ range: trendRange, channel_name: channel, model, cost_source: costSource, cost_status: costStatus, cost_basis: costBasis }), [channel, costBasis, costSource, costStatus, model, trendRange])
  const readTrend = useCallback((signal: AbortSignal) => getUsageTrend(trendFilters, signal), [trendFilters])
  const trend = useReadSnapshot(JSON.stringify(trendFilters), readTrend)
  const entries = snapshot?.stats || []
  const totals = useMemo(() => sumStats(entries), [entries])
  const totalTokens = (dashboard?.totals.input_tokens || 0) + (dashboard?.totals.output_tokens || 0)
  const costSummary = snapshot?.cost_summary ?? dashboard?.cost_summary ?? null
  const costMoney = [
    costSummary?.recorded_cost_usd,
    costSummary?.estimated_cost_usd,
    costSummary?.partial_cost_usd,
    costSummary?.historical_cost_usd,
  ]
  const costView = analyzeCostSummary(costSummary, moneyDigits(costMoney))
  const recordedDistribution = useMemo(() => statsDistribution(entries, 'model', 'cost'), [entries])
  const costAmount = costView.status === 'ready' ? costView.amountText
    : recordedDistribution.items.length ? formatMoney(recordedDistribution.total) : '—'
  const balanceSchedule = snapshot?.balance_schedule
  const balanceAsOf = snapshot?.balance_as_of_day || formatShanghaiDay(new Date())

  return (
    <div className="workspace-page data-dashboard-page">
      <PageHeader
        icon={BarChart3}
        title="数据面板"
        tone="coral"
        actions={<>
          <div className="range-control" role="radiogroup" aria-label="数据面板时间范围">
            {([['today', '今日'], ['this_week', '本周'], ['this_month', '本月']] as const).map(([value, label]) => <button className={range === value ? 'is-active' : ''} type="button" role="radio" aria-checked={range === value} onClick={() => { setChannel(''); setModel(''); setCostSource(''); setCostStatus(''); setCostBasis(''); setRange(value) }} key={value}>{label}</button>)}
          </div>
          <button className="icon-button icon-button--surface" type="button" disabled={refreshing} onClick={async () => { setRefreshing(true); trend.refresh(); try { await load() } finally { setRefreshing(false) } }} aria-label="刷新数据面板"><RefreshCw size={17} className={refreshing ? 'spin' : undefined} /></button>
        </>}
      />

      <div className="filter-bar">
        <select value={channel} onChange={(event) => setChannel(event.target.value)} aria-label="数据面板渠道"><option value="">全部渠道</option>{options.channel_names.map((item) => <option key={item}>{item}</option>)}</select>
        <select value={model} onChange={(event) => setModel(event.target.value)} aria-label="数据面板模型"><option value="">全部模型</option>{options.models.map((item) => <option key={item}>{item}</option>)}</select>
        <select value={costSource} onChange={(event) => setCostSource(event.target.value as CostSource | '')} aria-label="数据面板费用估算来源">{COST_SOURCE_FILTER_OPTIONS.map((item) => <option value={item.value} key={item.value || 'all'}>{item.label}</option>)}</select>
        <select value={costStatus} onChange={(event) => setCostStatus(event.target.value as CostStatus | '')} aria-label="数据面板费用状态">{COST_STATUS_FILTER_OPTIONS.map((item) => <option value={item.value} key={item.value || 'all'}>{item.label}</option>)}</select>
        <select value={costBasis} onChange={(event) => setCostBasis(event.target.value as CostBasis | '')} aria-label="数据面板费用计费方式">{COST_BASIS_FILTER_OPTIONS.map((item) => <option value={item.value} key={item.value || 'all'}>{item.label}</option>)}</select>
        {(channel || model || costSource || costStatus || costBasis) && <button className="icon-button icon-button--surface" type="button" onClick={() => { setChannel(''); setModel(''); setCostSource(''); setCostStatus(''); setCostBasis('') }} aria-label="清除数据面板筛选" title="清除筛选"><RefreshCw size={15} /></button>}
      </div>

      {snapshot && dashboard && <section className="stat-kpis">
        <MiniKPI icon={Zap} label="请求总量" value={formatNumber(totals.total)} meta={`${formatNumber(totals.success)} 成功`} />
        <MiniKPI icon={Gauge} label="成功率" value={totals.total ? `${(totals.success / totals.total * 100).toFixed(1)}%` : '—'} meta={`${formatNumber(totals.error)} 失败`} />
        <MiniKPI icon={Activity} label="Token 总量" value={formatNumber(totalTokens)} meta={`${formatNumber(dashboard.totals.cache_read_tokens)} 缓存命中`} />
        <article className={`stats-cost-kpi stats-cost-kpi--${costView.coverageState}`}>
          <span><CircleDollarSign size={17} /></span>
          <div>
            <small className="stats-kpi-label">已记录费用 · USD<HelpTip label="已记录费用" text={`本地记录金额，尚未与上游账单对账。${costView.coverageText}；未记录的费用不计为零。来源与金额可在下方渠道、模型明细中查看。`} /></small><strong>{costAmount}</strong>
          </div>
        </article>
      </section>}

      <div className="stats-charts-grid">
      <section className="trend-workbench data-dashboard-trend" aria-label="用量走势">
        <header>
          <div><LineChart size={18} /><h2>用量走势</h2><HelpTip label="用量走势" text="本周与本月按日汇总，今日按 3 小时汇总；时间范围独立于上方统计，沿用当前渠道、模型和费用筛选。曲线保留实际采样值，各指标使用独立刻度。" /></div>
          <div className="stats-trend-controls">
            <select value={trendRange} onChange={(event) => setTrendRange(event.target.value as DashboardRange)} aria-label="用量走势时间范围"><option value="today">今日</option><option value="this_week">本周</option><option value="this_month">本月</option></select>
            <div className="trend-chart-toggle" role="group" aria-label="趋势图形式">
              <button type="button" className={chartMode === 'curve' ? 'is-active' : ''} onClick={() => setChartMode('curve')} aria-pressed={chartMode === 'curve'} aria-label="曲线视图" title="曲线视图"><LineChart size={15} /></button>
              <button type="button" className={chartMode === 'bars' ? 'is-active' : ''} onClick={() => setChartMode('bars')} aria-pressed={chartMode === 'bars'} aria-label="柱状视图" title="柱状视图"><BarChart3 size={15} /></button>
            </div>
          </div>
        </header>
        {trend.error ? <ErrorState message={trend.error} retry={trend.refresh} /> : trend.loading && !trend.data ? <LoadingState label="正在读取用量走势" /> : <CombinedTrendChart points={trend.data || []} mode={chartMode} />}
      </section>
      {snapshot ? <BalanceHistoryPanel points={snapshot.balance_history || []} schedule={balanceSchedule} days={balanceDays} asOfDay={balanceAsOf} onDaysChange={setBalanceDays} /> : <section className="balance-change-panel"><LoadingState label="正在读取余额变化" /></section>}
      </div>

      {!loading && !error && entries.length > 0 ? <StatsDistribution entries={entries} /> : null}

      {loading ? <LoadingState label="正在计算数据面板" /> : error ? <ErrorState message={error} retry={() => void load()} /> : entries.length === 0 ? <EmptyState label="当前范围暂无统计数据" /> : (
        <section className={`stats-records-wrap${detailsOpen ? ' is-open' : ''}`}>
          <button className="stats-records-toggle" type="button" aria-expanded={detailsOpen} onClick={() => setDetailsOpen((open) => !open)}>
            <span><List size={16} /><strong>渠道 / 模型明细</strong><em>{entries.length} 项</em></span>
            <span>成功率、延迟、RPM 与已记录费用<ChevronDown size={16} /></span>
          </button>
          {detailsOpen && <div className="records-panel stats-records">
            <div className="record-head stats-grid"><span>渠道 / 模型</span><span>健康</span><span>成功 / 失败</span><span>首字 / 总耗时</span><span>RPM</span><span>Token</span><span>已记录费用</span></div>
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
  const hasCostData = entries.some((entry) => entry.effective_cost != null || entry.total_cost != null)
  const mode: DistributionMode = hasCostData ? 'cost' : 'requests'
  const channelView = statsDistribution(entries, 'channel', mode, false)
  const modelView = statsDistribution(entries, 'model', mode, false)
  return <section className="stats-distribution" aria-label="已记录费用与请求分布">
    <DistributionPanel icon={BarChart3} title={mode === 'cost' ? '渠道已记录费用' : '渠道请求量'} total={channelView.total} items={channelView.items} unknownEntries={channelView.unknownEntries} mode={mode} />
    <DistributionPanel icon={Activity} title={mode === 'cost' ? '模型已记录费用' : '模型请求量'} total={modelView.total} items={modelView.items} unknownEntries={modelView.unknownEntries} mode={mode} />
  </section>
}

function DistributionPanel({ icon: Icon, title, total, items, unknownEntries, mode }: { icon: typeof BarChart3; title: string; total: number; items: DistributionItem[]; unknownEntries: number; mode: DistributionMode }) {
  // 默认饼图：先回答「谁占大头」；条形列表保留给需要精确排序对比的场景。
  const [view, setView] = useState<'pie' | 'list'>('pie')
  return <article>
    <header>
      <Icon size={17} />
      <h2>{title}</h2>
      <HelpTip label={`${title}口径`} text={`${mode === 'cost' ? '仅含已记录金额，尚未与上游账单对账。' : '包含全部请求。'}${unknownEntries > 0 ? ` ${unknownEntries} 项明细的${mode === 'cost' ? '费用' : '数量'}暂不可用。` : ''}图表与列表都保留全部项目，可滚动查看。`} />
      <div className="stats-distribution-toggle" role="group" aria-label={`${title}展示方式`}>
        <button type="button" className={view === 'pie' ? 'is-active' : ''} onClick={() => setView('pie')} aria-pressed={view === 'pie'} aria-label={`${title}占比饼图`} title="占比饼图"><PieChart size={14} /></button>
        <button type="button" className={view === 'list' ? 'is-active' : ''} onClick={() => setView('list')} aria-pressed={view === 'list'} aria-label={`${title}条形列表`} title="条形列表"><List size={14} /></button>
      </div>
    </header>
    {items.length ? view === 'pie' ? <DistributionPie total={total} items={items} mode={mode} /> : <DistributionList items={items} mode={mode} /> : <p className="stats-distribution-note">当前明细没有可用金额</p>}
  </article>
}

function DistributionPie({ total, items, mode }: { total: number; items: DistributionItem[]; mode: DistributionMode }) {
  const [hovered, setHovered] = useState<number | null>(null)
  const digits = moneyDigits(items.map((item) => item.value))
  const radius = 33
  const slices = donutSlices(items, (item) => item.share).map((slice, index) => ({
    ...slice.item,
    index,
    start: slice.start,
    share: slice.share,
    color: slice.item.isOther ? 'var(--text-muted)' : PIE_COLORS[index % PIE_COLORS.length],
  }))
  const active = hovered == null ? null : slices[hovered]
  return <div className="stats-distribution-pie">
    <div className="stats-distribution-donut">
      <svg viewBox="0 0 100 100" role="img" aria-label="占比环形图">
        {slices.map((slice) => <path
          className={`stats-distribution-slice${hovered === slice.index ? ' is-active' : ''}`}
          key={slice.key}
          d={donutSlicePath(slice.start, slice.share, radius)}
          stroke={slice.color}
          onMouseEnter={() => setHovered(slice.index)}
          onMouseLeave={() => setHovered(null)}
        />)}
      </svg>
      <div className="stats-distribution-center">
        {active ? <>
          <strong title={active.label}>{active.label}</strong>
          <b>{formatDistributionValue(active.value, mode, digits)}</b>
          <em>{(active.share * 100).toFixed(1)}%</em>
        </> : <>
          <strong>合计</strong>
          <b>{formatDistributionValue(total, mode, digits)}</b>
          <em>{items.length} 项</em>
        </>}
      </div>
    </div>
    <ul className="stats-distribution-legend">
      {slices.map((slice) => <li
        className={hovered === slice.index ? 'is-active' : ''}
        key={slice.key}
        onMouseEnter={() => setHovered(slice.index)}
        onMouseLeave={() => setHovered(null)}
      >
        <button type="button" onFocus={() => setHovered(slice.index)} onBlur={() => setHovered(null)} onClick={() => setHovered(slice.index)} aria-label={`${slice.label} ${formatDistributionValue(slice.value, mode, digits)}，占比 ${(slice.share * 100).toFixed(1)}%`}>
          <i style={{ background: slice.color }} aria-hidden="true" />
          <div className="stats-distribution-legend-value"><span title={slice.label}>{slice.label}</span><b>{formatDistributionValue(slice.value, mode, digits)}</b></div>
          <em>{(slice.share * 100).toFixed(1)}%</em>
        </button>
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
      <span><i className="combined-trend-dot combined-trend-dot--cost" />已记录费用 <b>峰值 {formatMetric(maxima.cost, 'cost')}</b></span>
    </div>
    <div className={`combined-trend-plot${mode === 'curve' ? ' combined-trend-plot--curve' : ''}`} role="img" aria-label="请求量、Token 与已记录费用组合趋势图">
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
            aria-label={`${formatPointTime(entry.point.ts)}，请求量 ${formatMetric(entry.requests, 'requests')} 次，Token ${formatMetric(entry.tokens, 'tokens')}，已记录费用 ${formatMetric(entry.cost, 'cost')}`}
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
          aria-label={`${formatPointTime(entry.point.ts)}，请求量 ${formatMetric(entry.requests, 'requests')} 次，Token ${formatMetric(entry.tokens, 'tokens')}，已记录费用 ${formatMetric(entry.cost, 'cost')}`}
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
        <span><i className="combined-trend-dot combined-trend-dot--cost" />已记录费用<b>{formatMetric(hoveredEntry.cost, 'cost')}</b></span>
      </div>}
    </div>
    <div className="combined-trend-axis"><span>{formatPointTime(points[0].ts)}</span><span>{formatPointTime(points[points.length - 1].ts)}</span></div>
  </div>
}

function formatDistributionValue(value: number, mode: DistributionMode, digits?: number): string {
  return mode === 'cost' ? formatMoney(value, digits) : formatNumber(value)
}

function DistributionList({ items, mode }: { items: DistributionItem[]; mode: DistributionMode }) {
  const digits = moneyDigits(items.map((item) => item.value))
  return <div className="stats-distribution-list">
    {items.map((item, index) => <div key={item.key}>
      <span className="stats-distribution-rank">{index + 1}</span>
      <div>
        <strong title={item.label}>{item.label}</strong>
        <i><b style={{ width: `${item.share * 100}%` }} /></i>
      </div>
      <span>{formatDistributionValue(item.value, mode, digits)}</span>
    </div>)}
  </div>
}

function BalanceHistoryPanel({ points, schedule, days, asOfDay, onDaysChange }: { points: BalanceHistoryInput[]; schedule: unknown; days: BalanceHistoryDays; asOfDay?: string; onDaysChange: (value: BalanceHistoryDays) => void }) {
  const overlay = useMemo(() => balanceHistoryOverlay(points, days, asOfDay), [points, days, asOfDay])
  const [requestedCurrencies, setRequestedCurrencies] = useState<string[]>([])
  const [activeDay, setActiveDay] = useState<string | null>(null)
  const pinnedDay = useRef<string | null>(null)
  const currencies = overlay.series.map((chart) => chart.currency)
  const selected = selectBalanceCurrencies(currencies, requestedCurrencies)
  const charts = selected.map((currency) => overlay.series.find((chart) => chart.currency === currency)!)
  const notice = balanceHistoryNotice(charts, schedule)
  const day = overlay.days.find((item) => item.day === activeDay) ?? null
  const label = (currency: string) => currency || '币种未知'
  const digits = (chart: typeof charts[number]) => moneyDigits(chart.points.map((item) => item.dailyDelta ?? undefined))
  const displayDelta = (value: number | null, currency: string) => value == null ? '—' : formatDailyDelta(value, currency)
  const describe = (item: BalanceChartPoint, currency: string) => `${item.day}，${label(currency)} 每日净变化 ${displayDelta(item.dailyDelta, currency)}，${dailyComparisonLabel(item)}`
  const selectDay = (value: string) => {
    pinnedDay.current = value
    setActiveDay(value)
  }
  const changeDays = (value: BalanceHistoryDays) => {
    if (value === days) return
    const visible = new Set(balanceHistoryOverlay(points, value, asOfDay).days.map((item) => item.day))
    if (!visible.has(pinnedDay.current ?? '')) pinnedDay.current = null
    setActiveDay((current) => current && visible.has(current) ? current : pinnedDay.current)
    onDaysChange(value)
  }
  const changeCurrency = (index: number, value: string) => {
    const next = selected.slice()
    const otherIndex = index === 0 ? 1 : 0
    if (next[otherIndex] === value) next[otherIndex] = next[index]
    next[index] = value
    setRequestedCurrencies(next)
  }
  return <section className="balance-change-panel balance-history-lines" data-balance-start-day={overlay.startDay} data-balance-end-day={overlay.endDay} aria-label="余额变化">
    <header>
      <div><WalletCards size={17} /><h2>余额变化</h2><HelpTip label="余额变化" text={`按${days}日固定日历展示，每日一点，截至当天最新成功采样；${balanceScheduleCaption(schedule)}。各币种共用日期与中心零线，左右轴分别使用原币种独立刻度，不做汇率转换或相加。优先比较前日最后采样与当日最新采样；缺少前日可比较记录时可展示自当日首次采样以来的部分时段净变化，具体依据见悬停读数。只采用服务端确认可比较的同一账号集合记录，曲线只连接有效日期，不补零。`}/></div>
      <div className="balance-history-header-controls">
        <div className="balance-history-range" role="group" aria-label="余额变化区间">
          {([7, 14] as const).map((value) => <button type="button" className={days === value ? 'is-active' : ''} aria-pressed={days === value} onClick={() => changeDays(value)} key={value}>{value}日</button>)}
        </div>
      </div>
    </header>
    {charts.length ? <>
      <div className="balance-history-series-controls">
        {charts.map((chart, index) => <div className={`balance-history-currency-summary balance-series-${index}`} data-currency={chart.currency} key={index}>
          <label><i className="balance-history-line-key" aria-hidden="true" /><span>{index === 0 ? '左轴' : '右轴'}</span><select aria-label={index === 0 ? '左轴币种' : '右轴币种'} value={chart.currency} onChange={(event) => changeCurrency(index, event.target.value)}>{currencies.map((currency) => <option value={currency} key={currency}>{label(currency)}</option>)}</select></label>
          <span>{chart.latest ? `${formatDay(chart.latest.day)}${chart.latest.dailyDelta == null ? '' : ` · ${formatDailyDelta(chart.latest.dailyDelta, chart.currency)}`}` : '当前日期范围无记录'}</span>
        </div>)}
      </div>
      <div className="balance-history-overlay-layout">
        {charts.map((chart, index) => <div className={`balance-history-currency-axis balance-series-${index}`} data-currency={chart.currency} data-side={index === 0 ? 'left' : 'right'} key={chart.currency}>
          {chart.ticks.map((tick, tickIndex) => <span className="balance-history-tick" data-value={tick.value} data-y={tick.y} style={{ top: `${tick.y / 3}%` }} title={`${label(chart.currency)} ${formatSignedCurrency(tick.value, chart.currency, digits(chart))}`} key={tickIndex}>{formatSignedCurrency(tick.value, chart.currency, digits(chart))}</span>)}
        </div>)}
      <div className="balance-history-line-plot">
          <svg className="balance-history-overlay" viewBox="0 0 1000 300" preserveAspectRatio="none" role="img" aria-label="各币种每日净变化平滑曲线图">
            <desc>{charts.flatMap((chart) => chart.points.map((item) => describe(item, chart.currency))).join('；')}。左右轴各用原币种刻度，不换算，不相加。缺口不连线。</desc>
            {[34, 92, 208, 266].map((y) => <line className="balance-history-grid-line" x1="80" x2="920" y1={y} y2={y} key={y} />)}
            <line className={`balance-history-zero${notice.state !== 'ready' ? ' balance-history-zero--empty' : ''}`} x1="80" x2="920" y1={overlay.zeroY} y2={overlay.zeroY} />
            {charts.map((chart, seriesIndex) => <g className={`balance-series-${seriesIndex}`} key={chart.currency}>
              {chart.paths.map((path, pathIndex) => <path className="balance-history-line" data-currency={chart.currency} data-style={seriesIndex === 0 ? 'solid' : 'dashed'} d={path} key={pathIndex} />)}
              {chart.isolatedPoints.map((point) => <line className="balance-history-isolated" data-currency={chart.currency} x1={Math.max(80, point.x - 8)} x2={Math.min(920, point.x + 8)} y1={point.y ?? overlay.zeroY} y2={point.y ?? overlay.zeroY} key={`${point.day}-${point.x}`} />)}
            </g>)}
            {day && <line className="balance-history-crosshair" data-day={day.day} x1={day.x} x2={day.x} y1="24" y2="276" />}
          </svg>
          {overlay.days.map((item) => <button
            type="button" className="balance-history-hit" data-day={item.day} data-x={item.x}
            style={{ left: `${item.x / 10}%`, width: overlay.days.length > 1 ? `${84 / (overlay.days.length - 1)}%` : '28px' }}
            key={item.day} aria-label={`${item.day} 每日净变化`}
            onPointerEnter={(event) => { if (event.pointerType === 'mouse') setActiveDay(item.day) }}
            onPointerLeave={(event) => { if (event.pointerType === 'mouse') setActiveDay(pinnedDay.current) }}
            onPointerUp={(event) => { if (event.isPrimary && event.pointerType !== 'mouse') selectDay(item.day) }}
            onFocus={() => setActiveDay(item.day)}
            onBlur={() => setActiveDay(pinnedDay.current)}
            onClick={() => selectDay(item.day)}
            onKeyDown={(event) => {
              if (event.key === 'Escape') {
                event.stopPropagation()
                pinnedDay.current = null
                setActiveDay(null)
              }
            }}
          />)}
          {notice.state !== 'ready' && <div className="balance-history-no-values" data-state={notice.state} role="status"><span>{notice.title}</span><HelpTip label="余额记录状态" text={notice.description} /></div>}
          {day && <div className="balance-history-readout" aria-live="polite" role="status" style={{ left: `${Math.min(75, Math.max(25, day.x / 10))}%`, gridTemplateColumns: charts.length === 1 ? 'minmax(0, 1fr)' : undefined }}>
            <strong>{formatDay(day.day)}</strong>{charts.map((chart, index) => {
              const item = chart.points.find((point) => point.day === day.day)
              return <span className={`balance-history-readout-value balance-series-${index}`} data-currency={chart.currency} data-status={item?.comparisonStatus ?? 'missing'} data-change-basis={item?.changeBasis ?? undefined} data-day={day.day} data-value={item?.dailyDelta ?? ''} key={chart.currency}><b>{label(chart.currency)} {displayDelta(item?.dailyDelta ?? null, chart.currency)}</b>{item && <span>{dailyComparisonLabel(item)}</span>}<span className="balance-history-sample-time">{formatBalanceSampleTime(item?.updatedAt ?? null)}</span></span>
            })}
          </div>}
        </div>
        <div className="balance-history-axis"><span>{formatDay(overlay.startDay)}</span><span>{formatDay(overlay.endDay)}</span></div>
      </div>
    </> : <div className="balance-history-empty" aria-label="每日余额变化等待数据">
      <span><WalletCards size={18} /></span>
      <div><strong>{notice.title}<HelpTip label="余额记录状态" text={notice.description} /></strong><span>{overlay.startDay ? `${formatDay(overlay.startDay)} – ${formatDay(overlay.endDay)}` : ''}</span></div>
    </div>}
  </section>
}

function formatSignedCurrency(value: number | null, currency: string, digits = 2): string {
  return formatBalanceAmount(value, currency, digits, true)
}

function formatDay(day: string): string {
  const date = new Date(`${day}T00:00:00`)
  return Number.isNaN(date.getTime()) ? day : date.toLocaleDateString('zh-CN', { month: '2-digit', day: '2-digit' })
}

function formatShanghaiDay(value: Date): string {
  const parts = new Intl.DateTimeFormat('en-US', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(value)
  const get = (type: string) => parts.find((part) => part.type === type)?.value ?? ''
  return `${get('year')}-${get('month')}-${get('day')}`
}

function StatsRow({ entry }: { entry: StatsEntry }) {
  const rate = entry.total ? entry.success / entry.total : 0
  const multiplierLabel = actualMultiplierLabel(entry)
  const recordedCost = entry.effective_cost ?? entry.total_cost
  return <article className="record-row stats-grid">
    <div><strong>{entry.channel_name}</strong><span title={entry.model}>{entry.model}</span></div>
    <div className="health-cell"><HealthMeter rate={rate} hasData={entry.total > 0} /></div>
    <div><strong>{formatNumber(entry.success)} / {formatNumber(entry.error)}</strong><span>共 {formatNumber(entry.total)}</span></div>
    <div><strong>{entry.avg_first_byte_time_seconds ? `${entry.avg_first_byte_time_seconds.toFixed(2)}s` : '—'}</strong><span>{entry.avg_duration_seconds ? `${entry.avg_duration_seconds.toFixed(2)}s` : '—'}</span></div>
    <div><strong>{formatNumber(entry.peak_rpm, 1)}</strong><span>均值 {formatNumber(entry.avg_rpm, 1)}</span></div>
    <div><strong>{formatNumber(entry.total_input_tokens)} / {formatNumber(entry.total_output_tokens)}</strong><span>输入 / 输出</span></div>
    <div><strong title="本地已记录费用，未与上游账单对账">{recordedCost == null ? '—' : formatMoney(recordedCost)}</strong><span>{multiplierLabel}</span></div>
  </article>
}

function actualMultiplierLabel(entry: StatsEntry): string {
  const minimum = entry.actual_cost_multiplier_min
  const maximum = entry.actual_cost_multiplier_max
  if (minimum !== undefined && maximum !== undefined) {
    if (minimum === 0 && maximum === 0) return '本地倍率为零'
    if (minimum === maximum) return minimum === 1 ? '标准倍率' : `${formatMultiplier(minimum)}x 记录倍率`
    return `${formatMultiplier(minimum)}x–${formatMultiplier(maximum)}x 记录倍率`
  }
  const configured = entry.cost_multiplier
  if (configured === 0) return '本地倍率为零'
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
// One pass softens corners while retaining day-level peaks and troughs for a
// longer-range chart; repeated passes visibly flattened real activity.
const CURVE_SMOOTH_PASSES = 1
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
