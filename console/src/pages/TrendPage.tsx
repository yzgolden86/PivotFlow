import { useCallback, useEffect, useMemo, useState } from 'react'
import { Activity, CircleDollarSign, Clock3, RefreshCw, TrendingUp, Zap } from 'lucide-react'
import { getDashboard } from '../api'
import type { DashboardRange, DashboardSnapshot, MetricPoint } from '../types'
import { ErrorState, formatMoney, formatNumber, LoadingState, OperationNotice } from './shared'

type TrendMetric = 'requests' | 'tokens' | 'cost'

export default function TrendPage() {
  const [range, setRange] = useState<DashboardRange>('today')
  const [metric, setMetric] = useState<TrendMetric>('requests')
  const [snapshot, setSnapshot] = useState<DashboardSnapshot | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true); setError('')
    try { setSnapshot(await getDashboard(range, signal)) }
    catch (reason) { if (!signal?.aborted) setError(reason instanceof Error ? reason.message : '趋势加载失败') }
    finally { if (!signal?.aborted) setLoading(false) }
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
    <header className="page-header">
      <h1>消费趋势</h1>
      <div className="header-controls">
        <div className="range-control" role="radiogroup" aria-label="趋势时间范围">
          {([['today', '今日'], ['this_week', '本周'], ['this_month', '本月']] as const).map(([value, label]) => <button className={range === value ? 'is-active' : ''} type="button" role="radio" aria-checked={range === value} onClick={() => setRange(value)} key={value}>{label}</button>)}
        </div>
        <button className="icon-button icon-button--surface" type="button" disabled={refreshing} onClick={async () => { setRefreshing(true); try { await load() } finally { setRefreshing(false) } }} aria-label="刷新趋势"><RefreshCw size={17} className={refreshing ? 'spin' : undefined} /></button>
      </div>
    </header>
    {error && <OperationNotice tone="error">{error}</OperationNotice>}

    <section className="stat-kpis">
      <article><span><Activity size={18} /></span><div><small>请求</small><strong>{formatNumber(snapshot.totals.requests)}</strong><small>{formatNumber(snapshot.totals.errors)} 次失败</small></div></article>
      <article><span><Zap size={18} /></span><div><small>Token</small><strong>{formatNumber(totalTokens)}</strong><small>{formatNumber(snapshot.totals.cache_read_tokens)} 缓存命中</small></div></article>
      <article><span><CircleDollarSign size={18} /></span><div><small>实际消耗</small><strong>{formatMoney(snapshot.totals.effective_cost)}</strong><small>标准 {formatMoney(snapshot.totals.cost)}</small></div></article>
      <article><span><Clock3 size={18} /></span><div><small>采样间隔</small><strong>{formatInterval(averageDuration)}</strong><small>{snapshot.trend.length} 个时间点</small></div></article>
    </section>

    <section className="trend-workbench">
      <header>
        <div><TrendingUp size={18} /><h2>{metricLabel(metric)}</h2></div>
        <div className="trend-metric-tabs" role="tablist" aria-label="趋势指标">
          {(['requests', 'tokens', 'cost'] as TrendMetric[]).map((value) => <button className={metric === value ? 'is-active' : ''} type="button" role="tab" aria-selected={metric === value} onClick={() => setMetric(value)} key={value}>{metricLabel(value)}</button>)}
        </div>
      </header>
      <TrendChart points={snapshot.trend} metric={metric} />
    </section>

    <section className="trend-breakdown">
      <div className="data-panel"><h2>模型消耗</h2><Breakdown items={snapshot.model_usage.slice(0, 8)} /></div>
      <div className="data-panel"><h2>站点消耗</h2><Breakdown items={snapshot.site_usage.slice(0, 8)} /></div>
    </section>
  </div>
}

function TrendChart({ points, metric }: { points: MetricPoint[]; metric: TrendMetric }) {
  const [hovered, setHovered] = useState<number | null>(null)
  const values = useMemo(() => points.map((point) => pointValue(point, metric)), [metric, points])
  const max = Math.max(...values, 1)
  const line = values.map((value, index) => {
    const x = values.length <= 1 ? 40 : 40 + (index / (values.length - 1)) * 920
    const y = 245 - (value / max) * 205
    return `${x},${y}`
  }).join(' ')
  if (!points.length) return <div className="content-state content-state--empty">当前范围暂无趋势数据</div>
  const hoveredIndex = hovered ?? 0
  const hoveredPoint = hovered == null ? null : points[hoveredIndex]
  const hoveredValue = hoveredPoint == null ? 0 : values[hoveredIndex] || 0
  const hoveredX = values.length <= 1 ? 8 : Math.min(92, Math.max(8, 4 + (hoveredIndex / (values.length - 1)) * 92))
  const hoveredY = 245 - (hoveredValue / max) * 205
  return <div className="trend-workbench-chart">
    <svg viewBox="0 0 1000 280" role="img" aria-label={`${metricLabel(metric)}趋势图`} preserveAspectRatio="none">
      {[40, 91, 142, 193, 245].map((y) => <line x1="40" x2="960" y1={y} y2={y} className="trend-grid-line" key={y} />)}
      <polyline points={line} className="trend-main-line" />
      {values.map((value, index) => {
        const x = values.length <= 1 ? 40 : 40 + (index / (values.length - 1)) * 920
        const y = 245 - (value / max) * 205
        return <circle cx={x} cy={y} r={hovered === index ? 6 : 3.5} className="trend-main-point" key={`${points[index].ts}-${index}`} onMouseEnter={() => setHovered(index)} onMouseLeave={() => setHovered(null)} onFocus={() => setHovered(index)} onBlur={() => setHovered(null)} tabIndex={0} role="button" aria-label={`${formatPointTime(points[index].ts)} ${metricLabel(metric)} ${formatMetric(value, metric)}`}><title>{`${formatPointTime(points[index].ts)} · ${metricLabel(metric)} ${formatMetric(value, metric)}${metric === 'requests' ? ' 次' : ''}`}</title></circle>
      })}
    </svg>
    {hoveredPoint && <div className="trend-point-tooltip" style={{ left: `${hoveredX}%`, top: `${(hoveredY / 280) * 100}%` }}>
      <strong>{formatPointTime(hoveredPoint.ts)}</strong><span>{metricLabel(metric)} <b>{formatMetric(hoveredValue, metric)}{metric === 'requests' ? ' 次' : ''}</b></span>
    </div>}
    <div className="trend-axis"><span>{formatPointTime(points[0].ts)}</span><strong>峰值 {formatMetric(max, metric)}</strong><span>{formatPointTime(points[points.length - 1].ts)}</span></div>
  </div>
}

function Breakdown({ items }: { items: Array<{ key: string; label: string; requests: number; effective_cost: number; share: number }> }) {
  if (!items.length) return <div className="mini-empty">暂无消耗</div>
  return <div className="trend-breakdown-list">{items.map((item) => <div key={item.key}><span><strong>{item.label}</strong><small>{formatNumber(item.requests)} 请求 · {formatMoney(item.effective_cost)}</small></span><i><b style={{ width: `${Math.max(2, item.share * 100)}%` }} /></i><em>{(item.share * 100).toFixed(1)}%</em></div>)}</div>
}

function pointValue(point: MetricPoint, metric: TrendMetric): number {
  if (metric === 'tokens') return (point.input_tokens || 0) + (point.output_tokens || 0)
  if (metric === 'cost') return point.effective_cost || 0
  return point.success + point.error
}
function metricLabel(metric: TrendMetric): string { return metric === 'tokens' ? 'Token' : metric === 'cost' ? '费用' : '请求量' }
function formatMetric(value: number, metric: TrendMetric): string { return metric === 'cost' ? formatMoney(value) : formatNumber(value) }
function formatPointTime(value: string): string { const date = new Date(value); return Number.isNaN(date.getTime()) ? value : new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(date) }
function averageTrendInterval(points: MetricPoint[]): number {
  if (points.length < 2) return 0
  const timestamps = points.map((point) => new Date(point.ts).getTime()).filter((value) => Number.isFinite(value))
  if (timestamps.length < 2) return 0
  const gaps = timestamps.slice(1).map((value, index) => value - timestamps[index]).filter((value) => value > 0)
  return gaps.length ? gaps.reduce((sum, value) => sum + value, 0) / gaps.length / 1000 : 0
}
function formatInterval(seconds: number): string { if (!seconds) return '—'; if (seconds >= 3600) return `${Math.round(seconds / 3600)} 小时`; if (seconds >= 60) return `${Math.round(seconds / 60)} 分钟`; return `${Math.round(seconds)} 秒` }
