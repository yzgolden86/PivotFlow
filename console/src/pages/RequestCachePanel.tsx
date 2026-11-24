import { useEffect, useRef, useState } from 'react'
import {
  Activity,
  Clock3,
  Database,
  Gauge,
  RefreshCw,
  ShieldCheck,
  TimerReset,
  Trash2,
} from 'lucide-react'
import { clearRequestCache, getRequestCacheObservation } from '../api'
import type { RequestCacheObservation } from '../types'
import {
  cacheInvalidationReasonLabel,
  cacheReasonLabel,
  createRequestCacheCoordinator,
} from './requestCache'
import HelpTip from '../components/HelpTip'
import type {
  RequestCacheCoordinator,
  RequestCacheCoordinatorState,
} from './requestCache'

const INITIAL_STATE: RequestCacheCoordinatorState = {
  phase: 'idle',
  observation: null,
  error: '',
  notice: '',
  snapshotStale: false,
}

export default function RequestCachePanel({ unsavedCount }: { unsavedCount: number }) {
  const [state, setState] = useState<RequestCacheCoordinatorState>(INITIAL_STATE)
  const coordinatorRef = useRef<RequestCacheCoordinator | null>(null)
  const observationTitleRef = useRef<HTMLHeadingElement | null>(null)

  useEffect(() => {
    const coordinator = createRequestCacheCoordinator({
      read: getRequestCacheObservation,
      clear: clearRequestCache,
      confirmClear: () => window.confirm('清空当前进程中的全部请求缓存条目？进程累计计数会保留，此操作不会自动重试。'),
    })
    coordinatorRef.current = coordinator
    const unsubscribe = coordinator.subscribe(setState)
    coordinator.start()
    return () => {
      unsubscribe()
      coordinator.dispose()
      coordinatorRef.current = null
    }
  }, [])

  const busy = state.phase === 'loading' || state.phase === 'refreshing' || state.phase === 'clearing'

  return <section className="request-cache-panel" aria-labelledby="request-cache-observation-title">
    <header className="request-cache-panel-head">
      <div>
        <span className="request-cache-eyebrow"><i aria-hidden="true" />当前进程观察</span>
        <div className="request-cache-heading">
          <h3 id="request-cache-observation-title" ref={observationTitleRef} tabIndex={-1}>请求缓存运行状态</h3>
          <HelpTip label="请求缓存运行状态" text="下方只显示后端进程当前生效的配置与本次进程累计观察，不会主动探测数据库或上游。读取、刷新和清空都不会应用上方尚未保存的设置。" />
        </div>
      </div>
      <div className="request-cache-actions">
        <button className="secondary-button" type="button" disabled={busy} onClick={() => coordinatorRef.current?.refresh()}>
          <RefreshCw className={state.phase === 'refreshing' || state.phase === 'loading' ? 'spin' : undefined} size={16} />
          {state.phase === 'loading' ? '读取中' : state.phase === 'refreshing' ? '刷新中' : '刷新观察'}
        </button>
        <button className="secondary-button request-cache-clear" type="button" disabled={busy || !state.observation} onClick={() => coordinatorRef.current?.clear()}>
          <Trash2 size={16} />
          {state.phase === 'clearing' ? '正在清空' : '清空缓存'}
        </button>
      </div>
    </header>

    {unsavedCount > 0 && <div className="request-cache-distinction">
      <Database size={18} aria-hidden="true" />
      <div>
        <strong>上方设置与下方快照属于两个时刻</strong>
        <span>上方有 {unsavedCount} 项未保存修改。保存并重启后，下方才会显示新的当前配置。</span>
      </div>
    </div>}

    <div className="request-cache-contract">
      <ShieldCheck size={18} aria-hidden="true" />
      <div>
        <p><strong>默认关闭，客户端必须显式选择。</strong>发送 <code>X-PivotFlow-Cache: use</code> 才会复用；<code>refresh</code> 强制刷新，<code>bypass</code> 跳过。</p>
        <details className="settings-disclosure">
          <summary>可复用范围与不缓存条件</summary>
          <p>仅复用非流式、<code>temperature=0</code>、单候选且只含文本的 OpenAI Chat 与 Anthropic Messages 成功响应。工具、媒体、会话、Responses、WebSocket、协议转换和语义请求改写会绕过；命中成功时不新增上游用量或费用。</p>
        </details>
      </div>
    </div>

    {state.error && <div className="request-cache-message request-cache-message--error" role="alert">
      <span>{state.error}</span>
      <button className="text-button" type="button" disabled={busy} onClick={() => {
        observationTitleRef.current?.focus()
        coordinatorRef.current?.refresh()
      }}>{state.observation ? '刷新确认' : '重新读取'}</button>
    </div>}
    {state.notice && <div className="request-cache-message request-cache-message--success" role="status">{state.notice}</div>}
    {state.snapshotStale && state.observation && <div className="request-cache-message request-cache-message--stale" role="status">
      <Clock3 size={15} />
      <span>下方仍是上次成功读取的快照，不能视为当前时刻；清空失败或超时时尤其需要刷新确认。</span>
    </div>}

    {!state.observation && state.phase === 'loading'
      ? <div className="request-cache-loading" role="status"><RefreshCw className="spin" size={18} /><span>正在读取当前进程的缓存状态</span></div>
      : state.observation
        ? <RequestCacheObservationView observation={state.observation} stale={state.snapshotStale} />
        : <div className="request-cache-loading request-cache-loading--unknown" role="status"><Activity size={18} /><span>当前状态未知。读取成功后才会展示缓存条目、容量和原因统计。</span></div>}
  </section>
}

function RequestCacheObservationView({ observation, stale }: { observation: RequestCacheObservation; stale: boolean }) {
  const { config, stats } = observation
  const reasonEntries = Object.entries(stats.reasons).filter(([, count]) => count > 0)
  const invalidationEntries = Object.entries(stats.invalidations).filter(([, count]) => count > 0)

  return <div className={`request-cache-readout${stale ? ' is-stale' : ''}`}>
    <div className="request-cache-status-line">
      <span className={`request-cache-state ${config.enabled ? 'is-enabled' : 'is-disabled'}`}>
        <i aria-hidden="true" />{stale
          ? config.enabled ? '上次观察时已启用' : '上次观察时已关闭'
          : config.enabled ? '当前已启用' : '当前默认关闭'}
      </span>
      <span><Clock3 size={14} />观察于 {formatCacheTime(observation.observed_at)}</span>
      <span>进程启动于 {formatCacheTime(observation.process_started_at)}</span>
      {stale && <strong>快照已过期</strong>}
    </div>

    <section className="request-cache-config" aria-labelledby="request-cache-current-config">
      <div className="request-cache-section-title">
        <div><Database size={17} /><h4 id="request-cache-current-config">{stale ? '上次观察配置' : '当前生效配置'}</h4></div>
        <span>来自后端进程快照</span>
      </div>
      <dl>
        <div><dt>有效期</dt><dd>{formatNumber(config.ttl_seconds)} <small>秒</small></dd></div>
        <div><dt>条目上限</dt><dd>{formatNumber(config.max_entries)} <small>条</small></dd></div>
        <div><dt>正文总容量</dt><dd>{formatBytes(config.max_bytes)}</dd></div>
        <div><dt>单条响应上限</dt><dd>{formatBytes(config.max_entry_bytes)}</dd></div>
        <div><dt>客户端选择</dt><dd>{config.enabled ? '需显式 use / refresh' : '全部请求绕过'}</dd></div>
      </dl>
    </section>

    <div className="request-cache-capacity">
      <CapacityMeter label="驻留条目" value={stats.entries} capacity={config.max_entries} format={formatNumber} detail={`其中过期但仍驻留 ${formatNumber(stats.expired_entries)} 条`} />
      <CapacityMeter label="响应正文" value={stats.bytes} capacity={config.max_bytes} format={formatBytes} detail="按响应正文 payload 计数，不代表 Go 堆内存" />
      <CapacityMeter label="捕获正文" value={stats.capture_bytes} capacity={config.max_bytes} format={formatBytes} detail={`当前 ${formatNumber(stats.captures)} 个在途捕获，与已存条目的总容量分别受控`} />
    </div>

    <div className="request-cache-metrics">
      <CacheMetric label="查找" value={stats.lookups} detail="实际复用查找；刷新与绕过单独计数" icon={Activity} />
      <CacheMetric label="命中" value={stats.hits} detail="只复用完成写出的成功响应" icon={ShieldCheck} tone="hit" />
      <CacheMetric label="未命中" value={stats.misses} detail="未找到可复用条目" icon={Database} />
      <CacheMetric label="强制刷新" value={stats.refreshes} detail="旧条目从刷新开始即移除" icon={RefreshCw} />
      <CacheMetric label="绕过" value={stats.bypasses} detail="关闭、未选择或不支持时不读不写" icon={Gauge} />
      <CacheMetric label="写入" value={stats.stored} detail="成功保存的安全响应" icon={Database} />
    </div>

    <div className="request-cache-ledger">
      <section>
        <div className="request-cache-section-title"><div><TimerReset size={17} /><h4>条目流转</h4></div><span>本次进程累计</span></div>
        <dl>
          <div><dt>过期</dt><dd>{formatNumber(stats.expired)}</dd></div>
          <div><dt>LRU 淘汰</dt><dd>{formatNumber(stats.evicted)}</dd></div>
          <div><dt>拒绝存储</dt><dd>{formatNumber(stats.rejected)}</dd></div>
          <div><dt>手动清空</dt><dd>{formatNumber(stats.clears)}</dd></div>
        </dl>
      </section>
      <section>
        <div className="request-cache-section-title"><div><Clock3 size={17} /><h4>最近失效</h4></div><span>清空后累计值保留</span></div>
        {stats.last_invalidation_at > 0 ? <div className="request-cache-last-invalidation">
          <strong>{cacheInvalidationReasonLabel(stats.last_invalidation_reason)}</strong>
          <span>{formatCacheTime(stats.last_invalidation_at)}</span>
        </div> : <p className="request-cache-empty-copy">本进程尚未发生缓存失效。</p>}
      </section>
    </div>

    <div className="request-cache-breakdown-grid">
      <ReasonList title="失效分布" entries={invalidationEntries} label={cacheInvalidationReasonLabel} empty="本进程尚未记录失效。" />
      <ReasonList title="缓存原因分布" entries={reasonEntries} label={cacheReasonLabel} empty="本进程尚未记录缓存决策原因。" />
    </div>

    <details className="settings-disclosure">
      <summary>统计与计量口径</summary>
      <footer className="request-cache-footnote">
      <span>统计计数器只表示当前进程；重启后清零。</span>
      <span>条目数包含仍驻留的过期项；正文与捕获容量分开计量。</span>
      <span>管理读取不会触发清理、探测数据库或执行维护。</span>
      </footer>
    </details>
  </div>
}

function CapacityMeter({ label, value, capacity, format, detail }: {
  label: string
  value: number
  capacity: number
  format: (value: number) => string
  detail: string
}) {
  const percent = capacity > 0 ? Math.min(100, value / capacity * 100) : 0
  return <article>
    <div><span>{label}<HelpTip label={`${label}计量`} text={detail} /></span><strong>{format(value)} <small>/ {format(capacity)}</small></strong></div>
    <progress aria-label={label} value={Math.min(value, capacity)} max={capacity} />
  </article>
}

function CacheMetric({ label, value, detail, icon: Icon, tone = 'default' }: {
  label: string
  value: number
  detail: string
  icon: typeof Activity
  tone?: 'default' | 'hit'
}) {
  return <article className={`request-cache-metric request-cache-metric--${tone}`}>
    <span aria-hidden="true"><Icon size={15} /></span>
    <div><small>{label}<HelpTip label={`缓存${label}`} text={detail} /></small><strong>{formatNumber(value)}</strong></div>
  </article>
}

function ReasonList({ title, entries, label, empty }: {
  title: string
  entries: Array<[string, number]>
  label: (code?: string) => string
  empty: string
}) {
  return <section className="request-cache-reasons">
    <div className="request-cache-section-title"><div><Gauge size={17} /><h4>{title}</h4></div></div>
    {entries.length ? <dl>{entries.map(([code, count]) => <div key={code}><dt>{label(code)}</dt><dd>{formatNumber(count)}</dd></div>)}</dl> : <p className="request-cache-empty-copy">{empty}</p>}
  </section>
}

function formatCacheTime(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '未记录'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(new Date(value))
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat('zh-CN').format(value)
}

function formatBytes(value: number): string {
  if (value < 1_024) return `${formatNumber(value)} B`
  if (value < 1_048_576) return `${formatDecimal(value / 1_024)} KiB`
  return `${formatDecimal(value / 1_048_576)} MiB`
}

function formatDecimal(value: number): string {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: value >= 10 ? 1 : 2 }).format(value)
}
