import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Activity, ArrowUpRight, Check, CircleAlert, CircleHelp, Copy, Database, RefreshCw, ShieldCheck, WalletCards } from 'lucide-react'
import type { RuntimeHealthComponent, RuntimeMetrics, RuntimeOperationStats, RuntimeQueueStats, RuntimeReplicationStats } from '../types'
import { ErrorState, LoadingState, PageHeader } from './shared'
import { RuntimeBadge } from './RuntimeHealthSummary'
import {
  formatRuntimeBytes, formatRuntimeNumber, formatRuntimeTime, runtimeComponent, runtimeComponentTitles,
  runtimeDiagnostics, runtimeDisplaySnapshot, runtimeHeadlines, runtimeReasonText,
} from './runtimeHealth'
import { useRuntimeMetrics } from './useRuntimeMetrics'

export default function HealthPage() {
  const [autoRefresh, setAutoRefresh] = useState(true)
  const { snapshot: lastSnapshot, error, refreshing, receivedAt, freshness, state, refresh } = useRuntimeMetrics(autoRefresh)
  const snapshot = runtimeDisplaySnapshot(lastSnapshot, freshness)
  const OverviewIcon = state === 'healthy' ? ShieldCheck : state === 'unknown' ? CircleHelp : ['blocked', 'error', 'warning'].includes(state) ? CircleAlert : Activity
  const [copyMessage, setCopyMessage] = useState('')
  const mounted = useRef(false)
  const copyTimer = useRef<number>()
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false; window.clearTimeout(copyTimer.current) }
  }, [])

  const copyDiagnostic = async () => {
    if (!lastSnapshot) return
    const fallbackCopy = (text: string) => {
      const focused = document.activeElement as HTMLElement | null
      const textarea = document.createElement('textarea')
      textarea.value = text
      textarea.readOnly = true
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      try {
        textarea.select()
        if (!document.execCommand('copy')) throw new Error('copy unavailable')
      } finally { textarea.remove(); focused?.focus() }
    }
    try {
      const text = JSON.stringify(runtimeDiagnostics(lastSnapshot, freshness, receivedAt), null, 2)
      if (navigator.clipboard?.writeText) {
        try { await navigator.clipboard.writeText(text) }
        catch { fallbackCopy(text) }
      } else fallbackCopy(text)
      if (mounted.current) setCopyMessage('脱敏诊断已复制')
    } catch {
      if (mounted.current) setCopyMessage('复制失败，请检查浏览器剪贴板权限')
    }
    if (mounted.current) {
      window.clearTimeout(copyTimer.current)
      copyTimer.current = window.setTimeout(() => setCopyMessage(''), 4000)
    }
  }

  return <div className="management-page health-page" data-snapshot-status={freshness}>
    <PageHeader icon={Activity} title="运行健康" actions={<div className="health-controls">
      <label className="health-auto-refresh"><input type="checkbox" checked={autoRefresh} onChange={(event) => setAutoRefresh(event.target.checked)} /><span>每 10 秒自动刷新</span></label>
      <button className="secondary-button" type="button" onClick={refresh} disabled={refreshing}><RefreshCw size={15} className={refreshing ? 'spin' : ''} />刷新</button>
      <button className="secondary-button" type="button" onClick={() => void copyDiagnostic()} disabled={!snapshot}>{copyMessage === '脱敏诊断已复制' ? <Check size={15} /> : <Copy size={15} />}复制脱敏诊断</button>
    </div>} />
    <span className="health-copy-status" role="status">{copyMessage}</span>

    {!snapshot ? (error ? <ErrorState message={error} retry={refresh} /> : <LoadingState label={autoRefresh ? '正在读取后台运行状态' : '尚未获取快照，请手动刷新'} />) : <>
      <section className={`health-overview health-overview--${state}`} aria-label="当前运行状态">
        <span className="health-overview-icon"><OverviewIcon size={25} aria-hidden="true" /></span>
        <div className="health-overview-copy">
          <div><RuntimeBadge state={state} /><h2 aria-live="polite">{freshness === 'paused' ? '上次快照：' : ''}{runtimeHeadlines[state]}</h2></div>
          <p>{freshness === 'stale' ? '当前快照已过期。以下计数和处理结果来自上次读取，重新读取成功后才能确认当前状态。' : freshness === 'paused' ? '自动刷新已暂停，以下是最近一次成功读取的快照。' : '这里只展示后台处理与资源状态；请求成功率请查看请求日志。'}</p>
        </div>
        <div className="health-overview-time"><span>快照时间</span><strong>{formatRuntimeTime(snapshot.observed_at)}</strong><small>{storageLabel(snapshot)}</small></div>
      </section>
      {error && <div className="health-refresh-error" role="alert"><strong>刷新失败，当前状态未知</strong><span>{error}</span><button type="button" className="secondary-button" onClick={refresh} disabled={refreshing}>重新读取</button></div>}

      <HealthSection title="费用与请求记录" caption="先确认费用有保障，排障记录能保存" icon={<WalletCards size={17} />}>
        <div className="health-grid">
          <BillingCard snapshot={snapshot} />
          <QueueCard snapshot={snapshot} id="request_history" stats={snapshot.request_traces} caption="完整请求与尝试时间线" />
        </div>
      </HealthSection>

      <HealthSection title="写入与同步" caption="本地写入与主库复制分别观察" icon={<Database size={17} />}>
        <div className="health-grid">
          <QueueCard snapshot={snapshot} id="logs" stats={snapshot.logs} caption="上游逐次尝试日志" />
          <QueueCard snapshot={snapshot} id="token_stats" stats={snapshot.token_failure_stats} caption="可降级的失败请求统计" />
          <ReplicationCard snapshot={snapshot} id="log_replication" stats={snapshot.storage.log_replication} />
          <ReplicationCard snapshot={snapshot} id="trace_replication" stats={snapshot.request_traces.replication} />
          <CacheCard snapshot={snapshot} />
        </div>
      </HealthSection>

      <HealthSection title="维护与恢复" caption="保留策略、最近清理与实际启动结果" icon={<RefreshCw size={17} />}>
        <div className="health-grid">
          <CleanupCard snapshot={snapshot} id="log_cleanup" />
          <CleanupCard snapshot={snapshot} id="debug_cleanup" />
          <StartupCard snapshot={snapshot} />
        </div>
      </HealthSection>

      <WebsocketCard snapshot={snapshot} />
      <footer className="health-footer"><span>进程启动于 {formatRuntimeTime(snapshot.started_at)}</span><span>累计计数以本次进程为单位，重启后清零；持久结算积压会重新读取。</span></footer>
    </>}
  </div>
}

function HealthSection({ title, caption, icon, children }: { title: string; caption: string; icon: ReactNode; children: ReactNode }) {
  return <section className="health-section"><div className="health-section-heading"><h2>{icon}{title}</h2><span>{caption}</span></div>{children}</section>
}

function HealthCard({ component, caption, children, wide = false }: { component: RuntimeHealthComponent; caption: string; children: ReactNode; wide?: boolean }) {
  return <article id={`runtime-${component.id}`} className={`health-card health-card--${component.state}${wide ? ' health-card--wide' : ''}`} aria-label={runtimeComponentTitles[component.id]}>
    <header><div><h3>{runtimeComponentTitles[component.id]}</h3><span>{caption}</span></div><RuntimeBadge state={component.state} /></header>
    <p className="health-card-explanation">{runtimeReasonText[component.reason]}</p>
    {children}
  </article>
}

function Metrics({ children }: { children: ReactNode }) {
  return <dl className="health-metrics">{children}</dl>
}

function Metric({ label, value, unit, alert = false }: { label: string; value: ReactNode; unit?: string; alert?: boolean }) {
  return <div className={alert ? 'health-metric health-metric--alert' : 'health-metric'}><dt>{label}</dt><dd>{typeof value === 'number' ? formatRuntimeNumber(value) : value}{unit && <small>{unit}</small>}</dd></div>
}

function QueueMeter({ value, capacity, label }: { value: number; capacity: number; label: string }) {
  return <div className="health-queue-meter"><div><span>{label}</span><strong>{formatRuntimeNumber(value)} / {formatRuntimeNumber(capacity)}</strong></div>{capacity > 0 && <progress aria-label={label} value={Math.min(value, capacity)} max={capacity} />}</div>
}

function OperationDetails({ stats, title = '最近处理与累计记录' }: { stats: RuntimeOperationStats; title?: string }) {
  const result = stats.last_result === 'success' ? '成功' : stats.last_result === 'error' ? '失败' : stats.last_result === 'cancelled' ? '已取消' : stats.last_result === 'unknown' ? '未知' : '尚未运行'
  return <details className="health-operation-details"><summary>{title}<span>最近结果：{result}</span></summary><dl>
    <div><dt>最近尝试</dt><dd>{formatRuntimeTime(stats.last_attempt_at)}</dd></div>
    <div><dt>最近成功</dt><dd>{formatRuntimeTime(stats.last_success_at)}</dd></div>
    <div><dt>最近失败</dt><dd>{formatRuntimeTime(stats.last_failure_at)}</dd></div>
    <div><dt>失败后再次成功</dt><dd>{formatRuntimeTime(stats.recovered_at)}</dd></div>
    <div><dt>成功 / 失败次数</dt><dd>{formatRuntimeNumber(stats.succeeded)} / {formatRuntimeNumber(stats.failed)}</dd></div>
    <div><dt>连续失败 / 取消次数</dt><dd>{formatRuntimeNumber(stats.consecutive_failures)} / {formatRuntimeNumber(stats.cancelled)}</dd></div>
    {stats.last_error_code && <div><dt>最近失败类别</dt><dd>{stats.last_error_code === 'timeout' ? '操作超时' : '操作失败，请查看服务端日志'}</dd></div>}
  </dl></details>
}

function BillingCard({ snapshot }: { snapshot: RuntimeMetrics }) {
  const billing = snapshot.billing
  const oldest = billing.pending === 0 ? '无积压' : billing.oldest_pending_at ? `${formatRuntimeNumber(Math.max(0, Math.floor((snapshot.observed_at - billing.oldest_pending_at) / 1000)))} 秒` : '未提供时间'
  return <HealthCard component={runtimeComponent(snapshot, 'billing')} caption="成功费用先保存，再幂等结算">
    {billing.enabled && <>
      <Metrics><Metric label="待确认" value={billing.pending} unit="条" /><Metric label="仅在内存" value={billing.unpersisted} unit="条" alert={billing.unpersisted > 0} /><Metric label="已结算确认" value={billing.settled} unit="次" /><Metric label="最老等待" value={oldest} /></Metrics>
      <p className="health-last-success">最近结算确认 <strong>{formatRuntimeTime(billing.last_success_at)}</strong></p>
      {billing.invalid_cost_events > 0 && <p className="health-loss-note">本进程有 {formatRuntimeNumber(billing.invalid_cost_events)} 笔金额无效事件，需核对上游账单；这部分金额可能被低估。</p>}
      <div className="health-counter-line"><span>待结算文件累计失败 <b>{formatRuntimeNumber(billing.journal_failures)}</b></span><span>主库累计失败 <b>{formatRuntimeNumber(billing.store_failures)}</b></span><span>已删除 Token 事件 <b>{formatRuntimeNumber(billing.deleted_token_events)}</b></span></div>
      <OperationDetails stats={billing.journal} title="待结算文件访问" /><OperationDetails stats={billing.settlement} title="主库结算记录" />
    </>}
    <a className="health-card-link" href="#/tokens">查看令牌用量<ArrowUpRight size={14} /></a>
  </HealthCard>
}

function QueueCard({ snapshot, id, stats, caption }: { snapshot: RuntimeMetrics; id: 'request_history' | 'logs' | 'token_stats'; stats: RuntimeQueueStats; caption: string }) {
  return <HealthCard component={runtimeComponent(snapshot, id)} caption={caption}>
    {stats.enabled && <>
      <Metrics><Metric label="通道等待" value={stats.queued} unit="条" /><Metric label="批次待处理" value={stats.buffered} unit="条" /><Metric label="成功处理" value={stats.written} unit="条" /><Metric label="最终未保存" value={stats.write_failed} unit="条" alert={stats.write_failed > 0} /></Metrics>
      <QueueMeter label="等待通道使用量" value={stats.queued} capacity={stats.capacity} />
      <p className="health-card-hint">批次待处理包含已取出、正在凑批或写入的记录。</p>
      {stats.dropped > 0 && <p className="health-loss-note">本进程累计丢弃 {formatRuntimeNumber(stats.dropped)} 条记录。后续成功不代表此前缺失已补齐。</p>}
      {stats.write_failed > 0 && <p className="health-loss-note">最终未保存的记录已停止该批写入重试，需结合服务端日志核对。</p>}
      {id === 'token_stats' && <p className="health-card-hint">失败统计允许在饱和时降级；成功费用使用独立持久结算。</p>}
      <p className="health-last-success">最近写入成功 <strong>{formatRuntimeTime(stats.operation.last_success_at)}</strong></p>
      <OperationDetails stats={stats.operation} />
    </>}
    <a className="health-card-link" href={id === 'request_history' ? '#/logs?view=requests' : id === 'logs' ? '#/logs?view=history' : '#/tokens'}>{id === 'token_stats' ? '查看令牌统计' : id === 'logs' ? '查看上游日志' : '查看完整请求'}<ArrowUpRight size={14} /></a>
  </HealthCard>
}

function ReplicationCard({ snapshot, id, stats }: { snapshot: RuntimeMetrics; id: 'log_replication' | 'trace_replication'; stats?: RuntimeReplicationStats }) {
  const trace = id === 'trace_replication'
  return <HealthCard component={runtimeComponent(snapshot, id)} caption={trace ? '独立预算 · 最多 64 个批次' : '共享同步等待队列'}>
    {stats?.enabled && <>
      <Metrics><Metric label={trace ? '等待与执行' : '共享队列等待'} value={stats.queued} unit="任务" /><Metric label="正在执行" value={stats.operation.active} unit="任务" /><Metric label="累计失败" value={stats.failed} unit="任务" alert={stats.failed > 0} /><Metric label="累计丢弃" value={stats.dropped} unit="任务" alert={stats.dropped > 0} /></Metrics>
      <QueueMeter value={stats.queued} capacity={stats.capacity} label={trace ? '同步预算（包含执行中）' : '共享等待通道使用量'} />
      <p className="health-card-hint">{trace ? '计数单位是请求历史批次，单批最多 32 条请求。' : '共享队列包含日志与请求历史任务；失败、丢弃和正在执行的计数仅指日志任务。'}</p>
      {(stats.failed > 0 || stats.dropped > 0) && <p className="health-loss-note">复制失败或丢弃的任务不会自动补发。后续成功只说明处理恢复，请核对主库历史是否完整。</p>}
      <p className="health-last-success">最近同步成功 <strong>{formatRuntimeTime(stats.operation.last_success_at)}</strong></p>
      <OperationDetails stats={stats.operation} />
    </>}
    {!stats && runtimeComponent(snapshot, id).state === 'unknown' && <p className="health-card-hint">未提供计数，暂不能确认同步情况。</p>}
  </HealthCard>
}

function CacheCard({ snapshot }: { snapshot: RuntimeMetrics }) {
  const stats = snapshot.storage.cache_sync
  return <HealthCard component={runtimeComponent(snapshot, 'cache_sync')} caption="主库写入后的本地副本" wide>
    {snapshot.storage.mode === 'hybrid' && <>
      <Metrics><Metric label="正在同步" value={stats.active} unit="次" /><Metric label="累计成功" value={stats.succeeded} unit="次" /><Metric label="累计失败" value={stats.failed} unit="次" /><Metric label="副本核对" value={snapshot.storage.cache_may_be_stale ? '需要核对' : '未观测到差异'} alert={snapshot.storage.cache_may_be_stale} /></Metrics>
      {snapshot.storage.cache_may_be_stale && <p className="health-loss-note">先检查本地磁盘、权限和数据库错误。修复后按部署文档恢复并核对副本；其他操作成功无法验证此前所有数据差异。</p>}
      <p className="health-last-success">最近同步成功 <strong>{formatRuntimeTime(stats.last_success_at)}</strong></p>
      <OperationDetails stats={stats} />
    </>}
  </HealthCard>
}

function CleanupCard({ snapshot, id }: { snapshot: RuntimeMetrics; id: 'log_cleanup' | 'debug_cleanup' }) {
  const debug = id === 'debug_cleanup'
  const stats = debug ? snapshot.cleanup.debug_logs : snapshot.cleanup.logs
  const retention = debug ? stats.mode === 'disabled' ? '已关闭并清空' : stats.mode === 'clearing' ? '关闭，待清空' : stats.retention_minutes > 0 ? `${stats.retention_minutes} 分钟` : '尚未读取' : stats.enabled ? `${stats.retention_days} 天` : '自动清理关闭'
  return <HealthCard component={runtimeComponent(snapshot, id)} caption={debug ? '启动检查 · 分批清理调试报文' : '普通日志和完整请求共用保留期限'}>
    <Metrics><Metric label="保留策略" value={retention} /><Metric label="清理周期" value={`${formatRuntimeNumber(stats.interval_ms / 60_000)} 分钟`} /><Metric label="下一次计划" value={stats.next_run_at ? formatRuntimeTime(stats.next_run_at) : stats.enabled ? '尚未安排' : '不再调度'} />{debug && <Metric label="按批次已删除" value={stats.deleted} unit="条" />}</Metrics>
    <p className="health-last-success">最近清理成功 <strong>{formatRuntimeTime(stats.operation.last_success_at)}</strong></p>
    <OperationDetails stats={stats.operation} />
    <a className="health-card-link" href="#/system">查看保留设置<ArrowUpRight size={14} /></a>
  </HealthCard>
}

function StartupCard({ snapshot }: { snapshot: RuntimeMetrics }) {
  const recovery = snapshot.storage.startup_recovery
  const observed = runtimeComponent(snapshot, 'startup_recovery').state !== 'unknown'
  const history = !recovery?.history_enabled ? '历史恢复未启用' : recovery.log_days === -1 ? '恢复全部历史' : `恢复最近 ${recovery.log_days} 天历史`
  return <HealthCard component={runtimeComponent(snapshot, 'startup_recovery')} caption="实际启动结果 · 主库到 SQLite 副本" wide>
    {recovery && <>
      <p className="health-card-hint">{history}。请求历史恢复保留本地独有记录。</p>
      <div className="health-recovery-phases">
        <RecoveryPhase label="配置副本" stats={recovery.configurations} enabled={recovery.enabled} observed={observed} />
        <RecoveryPhase label="上游日志历史" stats={recovery.logs} enabled={recovery.history_enabled} observed={observed} />
        <RecoveryPhase label="完整请求历史" stats={recovery.request_traces} enabled={recovery.history_enabled} observed={observed} />
      </div>
    </>}
  </HealthCard>
}

function RecoveryPhase({ label, stats, enabled, observed }: { label: string; stats: RuntimeOperationStats; enabled: boolean; observed: boolean }) {
  const state = !observed ? 'unknown' : !enabled ? 'disabled' : stats.failing ? 'warning' : stats.active > 0 ? 'working' : stats.succeeded > 0 ? 'healthy' : 'unknown'
  return <div className="health-recovery-phase"><div><strong>{label}</strong><RuntimeBadge state={state} /></div><span>{enabled ? `最近成功 ${formatRuntimeTime(stats.last_success_at)}` : '未执行这项恢复'}</span><OperationDetails stats={stats} title="恢复处理记录" /></div>
}

function WebsocketCard({ snapshot }: { snapshot: RuntimeMetrics }) {
  const stats = snapshot.responses_websocket
  const component = runtimeComponent(snapshot, 'websocket')
  return <HealthSection title="WebSocket 资源" caption="按当前占用判断压力，历史拒绝独立计数" icon={<Activity size={17} />}><HealthCard component={component} caption="下游连接、执行会话与上游物理连接" wide>
    {component.state !== 'disabled' && <>
      <Metrics><Metric label="下游连接" value={`${stats.downstream_connections} / ${stats.max_downstream_connections || '未提供上限'}`} /><Metric label="执行会话" value={`${stats.sessions} / ${stats.max_sessions || '未提供上限'}`} /><Metric label="上游连接" value={stats.upstream_connections} unit="个" /><Metric label="transcript 占用" value={formatRuntimeBytes(stats.transcript_bytes)} /></Metrics>
      <div className="health-resource-gauges"><QueueMeter label="下游连接预算" value={stats.downstream_connections} capacity={stats.max_downstream_connections} /><QueueMeter label="transcript 字节预算" value={stats.transcript_bytes} capacity={stats.max_transcript_bytes} /></div>
      <div className="health-counter-line"><span>连接拒绝 <b>{formatRuntimeNumber(stats.rejected_downstream_connections)}</b></span><span>会话容量拒绝 <b>{formatRuntimeNumber(stats.capacity_rejected)}</b></span><span>transcript 拒绝 <b>{formatRuntimeNumber(stats.budget_rejected)}</b></span><span>心跳失败 <b>{formatRuntimeNumber(stats.upstream_heartbeat_failures)}</b></span><span>重连 <b>{formatRuntimeNumber(stats.reconnects)}</b></span><span>上游待读 <b>{formatRuntimeBytes(stats.upstream_queued_read_bytes)}</b></span></div>
      <p className="health-card-hint">累计重连、心跳失败和拒绝不会单独把当前状态判为故障；会话在进程重启后不保留。</p>
    </>}
    <a className="health-card-link" href="#/logs?view=active">查看进行中的请求<ArrowUpRight size={14} /></a>
  </HealthCard></HealthSection>
}

function storageLabel(snapshot: RuntimeMetrics): string {
  const names = { sqlite: 'SQLite', mysql: 'MySQL', postgres: 'PostgreSQL', unknown: '未提供存储类别', hybrid: '混合存储' }
  return snapshot.storage.mode === 'hybrid' ? `${names[snapshot.storage.primary]} 主库 + SQLite 副本` : names[snapshot.storage.mode]
}
