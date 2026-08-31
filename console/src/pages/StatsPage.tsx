import { useCallback, useEffect, useMemo, useState } from 'react'
import { Activity, CircleDollarSign, Gauge, RefreshCw, Zap } from 'lucide-react'
import { getStats, getStatsFilterOptions } from '../api'
import type { DashboardRange, StatsEntry, StatsFilterOptions, StatsSnapshot } from '../types'
import { EmptyState, ErrorState, formatMoney, formatNumber, LoadingState, successTone } from './shared'

export default function StatsPage() {
  const [snapshot, setSnapshot] = useState<StatsSnapshot | null>(null)
  const [options, setOptions] = useState<StatsFilterOptions>({ channel_names: [], models: [] })
  const [range, setRange] = useState<DashboardRange>('today')
  const [channel, setChannel] = useState('')
  const [model, setModel] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true); setError('')
    try {
      const [data, filterOptions] = await Promise.all([
        getStats({ range, channel_name: channel, model }, signal),
        getStatsFilterOptions(range, signal),
      ])
      setSnapshot(data)
      setOptions(filterOptions)
    }
    catch (reason) { if (!signal?.aborted) setError(reason instanceof Error ? reason.message : '统计加载失败') }
    finally { if (!signal?.aborted) setLoading(false) }
  }, [channel, model, range])

  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort() }, [load])
  const entries = snapshot?.stats || []
  const totals = useMemo(() => sumStats(entries), [entries])

  return (
    <div className="workspace-page">
      <header className="page-header">
        <h1>用量统计</h1>
        <div className="header-controls"><button className="icon-button icon-button--surface" type="button" onClick={() => void load()} aria-label="刷新统计"><RefreshCw size={17} /></button></div>
      </header>

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

function StatsRow({ entry }: { entry: StatsEntry }) {
  const rate = entry.total ? entry.success / entry.total : 0
  return <article className="record-row stats-grid">
    <div><strong>{entry.channel_name}</strong><span title={entry.model}>{entry.model}</span></div>
    <div className="health-cell"><HealthMeter rate={rate} hasData={entry.total > 0} /></div>
    <div><strong>{formatNumber(entry.success)} / {formatNumber(entry.error)}</strong><span>共 {formatNumber(entry.total)}</span></div>
    <div><strong>{entry.avg_first_byte_time_seconds ? `${entry.avg_first_byte_time_seconds.toFixed(2)}s` : '—'}</strong><span>{entry.avg_duration_seconds ? `${entry.avg_duration_seconds.toFixed(2)}s` : '—'}</span></div>
    <div><strong>{formatNumber(entry.peak_rpm, 1)}</strong><span>均值 {formatNumber(entry.avg_rpm, 1)}</span></div>
    <div><strong>{formatNumber(entry.total_input_tokens)} / {formatNumber(entry.total_output_tokens)}</strong><span>输入 / 输出</span></div>
    <div><strong>{formatMoney(entry.effective_cost ?? entry.total_cost)}</strong>{entry.cost_multiplier && entry.cost_multiplier !== 1 ? <span>{entry.cost_multiplier}x 倍率</span> : <span>标准倍率</span>}</div>
  </article>
}

// 分格显示成功率，像电池电量：一眼能数出格数，比连续细条更易读。
// 百分比放在右侧同一行，不再占用下方一行。
const healthSegments = 10

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
