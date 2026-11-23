import { Activity, ArrowUpRight, CircleAlert, CircleCheck, Clock3, CircleHelp, PauseCircle, RefreshCw } from 'lucide-react'
import type { RuntimeHealthState } from '../types'
import { formatRuntimeNumber, runtimeComponentTitles, runtimeHeadlines, runtimeStateLabels } from './runtimeHealth'
import { useRuntimeMetrics } from './useRuntimeMetrics'

export function RuntimeBadge({ state }: { state: RuntimeHealthState }) {
  const Icon = state === 'healthy' ? CircleCheck : state === 'blocked' || state === 'error' || state === 'warning' ? CircleAlert : state === 'working' ? RefreshCw : state === 'disabled' ? PauseCircle : state === 'idle' ? Clock3 : CircleHelp
  return <span className={`runtime-badge runtime-badge--${state}`}><Icon size={13} aria-hidden="true" />{runtimeStateLabels[state]}</span>
}

export default function RuntimeHealthSummary() {
  const { snapshot, error, refreshing, state, freshness } = useRuntimeMetrics()
  const issues = snapshot?.health.components.filter((item) => ['blocked', 'error', 'warning', 'unknown'].includes(item.state)) ?? []
  const headline = !snapshot && refreshing ? '正在读取运行指标' : runtimeHeadlines[state]
  return <a className={`runtime-summary runtime-summary--${state}`} href="#/health" aria-label={`运行健康，${headline}，查看详情`}>
    <span className="runtime-summary-mark"><Activity size={19} aria-hidden="true" /></span>
    <div className="runtime-summary-copy">
      <div><strong>运行健康</strong><span aria-live="polite">{headline}</span></div>
      <p>{error || (freshness === 'stale' ? '上次快照已过期，当前状态未知' : issues.length ? issues.slice(0, 3).map((item) => runtimeComponentTitles[item.id]).join(' · ') : '结算、写入、同步与清理的独立运行状态')}</p>
    </div>
    {snapshot && freshness !== 'stale' && <span className="runtime-summary-counts"><span>待结算 <b>{formatRuntimeNumber(snapshot.billing.pending)}</b></span><span>请求历史待写 <b>{formatRuntimeNumber(snapshot.request_traces.queued + snapshot.request_traces.buffered)}</b></span></span>}
    <span className="runtime-summary-tail"><RuntimeBadge state={state} /><ArrowUpRight size={16} aria-hidden="true" /></span>
  </a>
}
