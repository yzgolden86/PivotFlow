import { Activity, ArrowUpRight, CircleAlert, CircleCheck, Clock3, CircleHelp, PauseCircle, RefreshCw } from 'lucide-react'
import type { RuntimeHealthState } from '../types'
import { formatRuntimeNumber, runtimeComponentTitles, runtimeHeadlines, runtimeStateLabels } from './runtimeHealth'
import { runtimeAttentionItems, runtimeOverviewState, type RuntimeAttentionItem } from './runtimeHealthPresentation'
import { useRuntimeMetrics } from './useRuntimeMetrics'
import './runtimeHealthPresentation.css'

export function RuntimeBadge({ state }: { state: RuntimeHealthState }) {
  const Icon = state === 'healthy' ? CircleCheck : state === 'blocked' || state === 'error' || state === 'warning' ? CircleAlert : state === 'working' ? RefreshCw : state === 'disabled' ? PauseCircle : state === 'idle' ? Clock3 : CircleHelp
  return <span className={`runtime-badge runtime-badge--${state}`}><Icon size={13} aria-hidden="true" />{runtimeStateLabels[state]}</span>
}

export default function RuntimeHealthSummary() {
  const { snapshot, error, refreshing, state, freshness } = useRuntimeMetrics()
  const attention = snapshot ? runtimeAttentionItems(snapshot, freshness) : []
  const summaryState = runtimeOverviewState(attention, state)
  const headline = !snapshot && refreshing ? '正在读取运行指标' : runtimeHeadlines[summaryState]
  const detail = error
    || (freshness === 'stale' && !snapshot ? '尚未取得可用快照，当前状态未知' : freshness === 'stale' ? '上次快照已过期，当前状态未知' : attentionDetail(attention))
  return <a className={`runtime-summary runtime-summary--${summaryState}`} href="#/health" aria-label={`运行健康，${headline}，查看详情`}>
    <span className="runtime-summary-mark"><Activity size={19} aria-hidden="true" /></span>
    <div className="runtime-summary-copy">
      <div><strong>运行健康</strong><span aria-live="polite">{headline}</span></div>
      <p>{detail}</p>
    </div>
    {snapshot && freshness !== 'stale' && <span className="runtime-summary-counts">
      <span>待结算 <b>{formatRuntimeNumber(snapshot.billing.pending)}</b></span>
      <span>请求历史待写 <b>{formatRuntimeNumber(snapshot.request_traces.queued + snapshot.request_traces.buffered)}</b></span>
      {snapshot.billing.unpersisted > 0 && <span className="is-alert">仅在内存 <b>{formatRuntimeNumber(snapshot.billing.unpersisted)}</b></span>}
      {snapshot.billing.invalid_cost_events > 0 && <span className="is-alert">金额无效 <b>{formatRuntimeNumber(snapshot.billing.invalid_cost_events)}</b></span>}
    </span>}
    <span className="runtime-summary-tail"><RuntimeBadge state={summaryState} /><ArrowUpRight size={16} aria-hidden="true" /></span>
  </a>
}

function attentionDetail(items: RuntimeAttentionItem[]): string {
  if (items.length === 0) return '结算、写入、同步与清理的独立运行状态'
  const titles = items.slice(0, 3).map((item) => item.componentId ? runtimeComponentTitles[item.componentId] : '快照已过期')
  return `${titles.join(' · ')}${items.length > titles.length ? `，另有 ${items.length - titles.length} 项需核对` : ''}`
}
