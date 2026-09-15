import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Activity, BarChart3, CircleDollarSign, Clock3, LineChart, RefreshCw, TrendingUp, Zap } from 'lucide-react'
import { getDashboard } from '../api'
import type { DashboardRange, DashboardSnapshot, MetricPoint } from '../types'
import { ErrorState, formatMoney, formatNumber, LoadingState, OperationNotice, PageHeader } from './shared'

type TrendMetric = 'requests' | 'tokens' | 'cost'
type TrendChartMode = 'curve' | 'bars'

export default function TrendPage() {
  const [range, setRange] = useState<DashboardRange>('today')
  const [chartMode, setChartMode] = useState<TrendChartMode>('curve')
  const loadSequence = useRef(0)
  const [snapshot, setSnapshot] = useState<DashboardSnapshot | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')

  // 手动刷新不带 AbortSignal，切换区间时旧请求不会被取消，按序号丢弃过期响应，
  // 否则「本月」的数字可能落在「今日」标签下。
  const load = useCallback(async (signal?: AbortSignal) => {
    const sequence = ++loadSequence.current
    const stale = () => Boolean(signal?.aborted) || sequence !== loadSequence.current
    setLoading(true); setError('')
    try {
      const result = await getDashboard(range, signal)
      if (stale()) return
      setSnapshot(result)
    }
    catch (reason) { if (!stale()) setError(reason instanceof Error ? reason.message : '趋势加载失败') }
    finally { if (!stale()) setLoading(false) }
  }, [range])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  if (loading && !snapshot) return <div className="workspace-page"><LoadingState label="正在加载消费趋势" /></div>
  if (error && !snapshot) return <div className="workspace-page"><ErrorState message={error} retry={() => void load()} /></div>
  if (!snapshot) return null

  const totalTokens = snapshot.totals.input_tokens + snapshot.totals.output_tokens
  const averageDuration = averageTrendInterval(snapshot.trend)
  return <div className="workspace-page trend-page">
    <PageHeader
      icon={TrendingUp}
      title="消费趋势"
      tone="coral"
      actions={<>
        <div className="range-control" role="radiogroup" aria-label="趋势时间范围">
          {([['today', '今日'], ['this_week', '本周'], ['this_month', '本月']] as const).map(([value, label]) => <button className={range === value ? 'is-active' : ''} type="button" role="radio" aria-checked={range === value} onClick={() => setRange(value)} key={value}>{label}</button>)}
        </div>
        <button className="icon-button icon-button--surface" type="button" disabled={refreshing} onClick={async () => { setRefreshing(true); try { await load() } finally { setRefreshing(false) } }} aria-label="刷新趋势"><RefreshCw size={17} className={refreshing ? 'spin' : undefined} /></button>
      </>}
    />
    {error && <OperationNotice tone="error">{error}</OperationNotice>}

    <section className="stat-kpis">
      <article><span><Activity size={18} /></span><div><small>请求</small><strong>{formatNumber(snapshot.totals.requests)}</strong><small>{formatNumber(snapshot.totals.errors)} 次失败</small></div></article>
      <article><span><Zap size={18} /></span><div><small>Token</small><strong>{formatNumber(totalTokens)}</strong><small>{formatNumber(snapshot.totals.cache_read_tokens)} 缓存命中</small></div></article>
      <article><span><CircleDollarSign size={18} /></span><div><small>实际消耗</small><strong>{formatMoney(snapshot.totals.effective_cost)}</strong><small>标准 {formatMoney(snapshot.totals.cost)}</small></div></article>
      <article><span><Clock3 size={18} /></span><div><small>采样间隔</small><strong>{formatInterval(averageDuration)}</strong><small>{snapshot.trend.length} 个时间点</small></div></article>
    </section>

    <section className="trend-workbench">
      <header>
        <div><TrendingUp size={18} /><h2>用量总览</h2></div>
        <div className="trend-chart-toggle" role="group" aria-label="趋势图形式">
          <button type="button" className={chartMode === 'curve' ? 'is-active' : ''} onClick={() => setChartMode('curve')} aria-pressed={chartMode === 'curve'} title="曲线视图"><LineChart size={15} /><span>曲线图</span></button>
          <button type="button" className={chartMode === 'bars' ? 'is-active' : ''} onClick={() => setChartMode('bars')} aria-pressed={chartMode === 'bars'} title="柱状视图"><BarChart3 size={15} /><span>柱状图</span></button>
        </div>
      </header>
      <CombinedTrendChart points={snapshot.trend} mode={chartMode} />
    </section>

    <section className="trend-breakdown">
      <div className="data-panel"><h2>模型消耗</h2><Breakdown items={snapshot.model_usage.slice(0, 8)} /></div>
      <div className="data-panel"><h2>站点消耗</h2><Breakdown items={snapshot.site_usage.slice(0, 8)} /></div>
    </section>
  </div>
}

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
  // 曲线视图先做一次轻度平滑：真实数据常带锯齿，直接连线会显得生硬。
  // 悬浮提示仍展示原始数值，柱状视图也不受影响。
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

function Breakdown({ items }: { items: Array<{ key: string; label: string; requests: number; effective_cost: number; share: number }> }) {
  if (!items.length) return <div className="mini-empty">暂无消耗</div>
  return <div className="trend-breakdown-list">{items.map((item, index) => <div key={item.key}>
    <span className="trend-rank">{index + 1}</span>
    <div><strong title={item.label}>{item.label}</strong><small>{formatNumber(item.requests)} 请求 · {formatMoney(item.effective_cost)}</small><i><b style={{ width: `${Math.max(3, item.share * 100)}%` }} /></i></div>
    <em>{(item.share * 100).toFixed(1)}%</em>
  </div>)}</div>
}

function metricLabel(metric: TrendMetric): string { return metric === 'tokens' ? 'Token' : metric === 'cost' ? '费用' : '请求量' }
function formatMetric(value: number, metric: TrendMetric): string { return metric === 'cost' ? formatMoney(value) : formatNumber(value) }
function barHeight(value: number, max: number): number { return value <= 0 ? 1 : Math.max(4, value / max * 100) }
function curveX(index: number, count: number): number { return count <= 1 ? 500 : 30 + (index / (count - 1)) * 940 }
function curveY(value: number, max: number): number { return 284 - Math.min(1, Math.max(0, value / max)) * 238 }
function smoothCurvePath(values: number[], max: number): string {
  if (!values.length) return ''
  const points = values.map((value, index) => ({ x: curveX(index, values.length), y: curveY(value, max) }))
  if (points.length === 1) return `M ${points[0].x.toFixed(2)} ${points[0].y.toFixed(2)} L ${(points[0].x + 1).toFixed(2)} ${points[0].y.toFixed(2)}`
  // 标准 Catmull-Rom 转三次贝塞尔：控制点取相邻两点连线的 1/6，
  // 曲线在每个节点上圆滑转向，不会出现折角；平滑后的数据本身
  // 起伏有限，因此只在绘图区边界做夹取，保留完整弧度。
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
// 五点二项平滑：保留走势的同时抹平单点锯齿，端点按自身值补齐避免塌陷。
function smoothSeries(values: number[]): number[] {
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
