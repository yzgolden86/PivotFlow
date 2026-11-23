import { useCallback, useEffect, useRef, useState } from 'react'
import { Bug, Check, Copy, History, ListTree, RefreshCw, RotateCcw, ScrollText, Search, Waves } from 'lucide-react'
import { getActiveRequestDebug, getActiveRequests, getLogs, getLogsBootstrap, getSystemSettings } from '../api'
import type { ActiveRequest, AuthToken, CostBasis, CostSource, CostStatus, DashboardRange, LogEntry, LogsBootstrap } from '../types'
import { EmptyState, ErrorState, formatNumber, formatTime, LoadingState, OperationNotice, PageHeader, Pagination } from './shared'
import { useLocation } from 'react-router-dom'
import { Modal } from './siteShared'
import { modelRedirectTitle } from './modelRedirect'
import RequestHistoryView from './RequestHistoryView'
import { compactRequestId, requestDetailHref } from './requestHistory'
import { buildCostEntryView, COST_BASIS_FILTER_OPTIONS, COST_SOURCE_FILTER_OPTIONS, COST_STATUS_FILTER_OPTIONS } from '../costSemantics.ts'
import '../costSemantics.css'

const PAGE_SIZE = 20
const EMPTY_BOOTSTRAP: LogsBootstrap = { channel_test_content: '', auth_tokens: [], models: [], channels: [], status_codes: [] }
const AUTO_REFRESH_SETTING = 'auto_refresh_interval_seconds'

function readAutoRefreshSeconds(settings: { key: string; value: string }[]): number {
  const value = Number(settings.find((setting) => setting.key === AUTO_REFRESH_SETTING)?.value ?? 0)
  return Number.isFinite(value) && value > 0 ? Math.max(1, Math.round(value)) : 0
}

export default function LogsPage() {
  const location = useLocation()
  const viewParam = new URLSearchParams(location.search).get('view')
  const view = viewParam === 'history' ? 'history' : viewParam === 'active' ? 'active' : 'requests'
  const requestedRange = new URLSearchParams(location.search).get('range')
  const initialRange: DashboardRange = requestedRange === 'this_week' || requestedRange === 'this_month' ? requestedRange : 'today'
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [options, setOptions] = useState<LogsBootstrap>(EMPTY_BOOTSTRAP)
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [range, setRange] = useState<DashboardRange>(initialRange)
  const [channel, setChannel] = useState('')
  const [token, setToken] = useState('')
  const [model, setModel] = useState('')
  const [search, setSearch] = useState('')
  const [searchQuery, setSearchQuery] = useState('')
  const [status, setStatus] = useState('')
  const [source, setSource] = useState('proxy')
  const [costSource, setCostSource] = useState<CostSource | ''>('')
  const [costStatus, setCostStatus] = useState<CostStatus | ''>('')
  const [costBasis, setCostBasis] = useState<CostBasis | ''>('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [autoRefreshSeconds, setAutoRefreshSeconds] = useState(0)
  const [autoRefreshing, setAutoRefreshing] = useState(false)
  const [manualRefreshing, setManualRefreshing] = useState(false)
  const loadSequence = useRef(0)
  const foregroundLoadSequence = useRef(0)
  const backgroundRefreshInFlight = useRef(false)

  useEffect(() => { setRange(initialRange); setPage(1) }, [initialRange])

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setSearchQuery(search.trim())
      setPage(1)
    }, 300)
    return () => window.clearTimeout(timer)
  }, [search])

  const load = useCallback(async (signal?: AbortSignal, background = false) => {
    if (view !== 'history') return
    if (background && (backgroundRefreshInFlight.current || foregroundLoadSequence.current !== 0)) return
    const sequence = ++loadSequence.current
    if (background) {
      backgroundRefreshInFlight.current = true
      setAutoRefreshing(true)
    } else {
      foregroundLoadSequence.current = sequence
      setLoading(true)
      setError('')
    }
    try {
      const filters = {
        range,
        channel_name: channel,
        auth_token_id: token,
        model,
        search: searchQuery,
        status_code: status,
        log_source: source,
        cost_source: costSource,
        cost_status: costStatus,
        cost_basis: costBasis,
        limit: PAGE_SIZE,
        offset: (page - 1) * PAGE_SIZE,
      }
      const [result, bootstrap] = await Promise.all([getLogs(filters, signal), getLogsBootstrap(range, signal)])
      if (signal?.aborted || sequence !== loadSequence.current) return
      setLogs(result.data)
      setTotal(result.count)
      setOptions(bootstrap)
      setError('')
    } catch (reason) {
      if (!signal?.aborted && sequence === loadSequence.current && !background) setError(reason instanceof Error ? reason.message : '日志加载失败')
    } finally {
      if (background) {
        backgroundRefreshInFlight.current = false
        if (!signal?.aborted) setAutoRefreshing(false)
      } else if (sequence === foregroundLoadSequence.current) {
        // 只有当前前台读取可以结束加载；旧手动刷新不能复位新筛选。
        // 后台读取在前台加载完成前等待，避免抢占它的数据或加载归属。
        foregroundLoadSequence.current = 0
        if (!signal?.aborted) setLoading(false)
      }
    }
  }, [channel, costBasis, costSource, costStatus, model, page, range, searchQuery, source, status, token, view])

  useEffect(() => {
    if (view !== 'history') return
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load, view])

  useEffect(() => {
    if (view === 'requests') return
    const controller = new AbortController()
    void getSystemSettings(controller.signal).then((settings) => {
      if (!controller.signal.aborted) setAutoRefreshSeconds(readAutoRefreshSeconds(settings))
    }).catch(() => {
      if (!controller.signal.aborted) setAutoRefreshSeconds(0)
    })
    return () => controller.abort()
  }, [view])

  useEffect(() => {
    if (view !== 'history' || autoRefreshSeconds <= 0) return
    const refresh = () => {
      if (document.visibilityState === 'visible') void load(undefined, true)
    }
    const timer = window.setInterval(refresh, autoRefreshSeconds * 1000)
    return () => window.clearInterval(timer)
  }, [autoRefreshSeconds, load, view])

  return (
    <div className="workspace-page">
      <PageHeader
        icon={ScrollText}
        title="请求日志"
        tone="coral"
        actions={<><div className="page-tabs page-tabs--header" role="tablist"><a className={view === 'requests' ? 'is-active' : ''} href="#/logs?view=requests" role="tab" aria-selected={view === 'requests'}><ListTree size={14} />完整请求</a><a className={view === 'history' ? 'is-active' : ''} href="#/logs?view=history" role="tab" aria-selected={view === 'history'}><History size={14} />上游日志</a><a className={view === 'active' ? 'is-active' : ''} href="#/logs?view=active" role="tab" aria-selected={view === 'active'}><Waves size={14} />进行中</a></div>{view === 'history' && <button className="icon-button icon-button--surface" type="button" disabled={manualRefreshing} onClick={async () => { setManualRefreshing(true); try { await load() } finally { setManualRefreshing(false) } }} aria-label="刷新日志" title={autoRefreshSeconds > 0 ? `已启用自动刷新：每 ${autoRefreshSeconds} 秒` : '刷新日志'}><RefreshCw className={autoRefreshing || manualRefreshing ? 'spin' : undefined} size={17} /></button>}</>}
      />

      {view === 'requests' ? <RequestHistoryView /> : view === 'active' ? <ActiveRequestsView autoRefreshSeconds={autoRefreshSeconds} /> : <>
      <div className="filter-bar filter-bar--wide">
        <select value={range} onChange={(event) => { setPage(1); setRange(event.target.value as DashboardRange) }} aria-label="日志时间范围"><option value="today">今日</option><option value="this_week">本周</option><option value="this_month">本月</option></select>
        <select value={channel} onChange={(event) => { setPage(1); setChannel(event.target.value) }} aria-label="日志渠道"><option value="">全部渠道</option>{options.channels.map((item) => <option value={item.name} key={item.id}>{item.name}</option>)}</select>
        <select value={token} onChange={(event) => { setPage(1); setToken(event.target.value) }} aria-label="日志访问令牌"><option value="">全部令牌</option>{options.auth_tokens.map((item) => <option value={item.id} key={item.id}>{authTokenOptionLabel(item)}</option>)}</select>
        <select value={model} onChange={(event) => { setPage(1); setModel(event.target.value) }} aria-label="日志模型"><option value="">全部模型</option>{options.models.map((item) => <option value={item} key={item}>{item}</option>)}</select>
        <select value={status} onChange={(event) => { setPage(1); setStatus(event.target.value) }} aria-label="响应状态"><option value="">全部状态</option>{options.status_codes.map((item) => <option value={item} key={item}>{item}</option>)}</select>
        <select value={source} onChange={(event) => { setPage(1); setSource(event.target.value) }} aria-label="日志来源"><option value="proxy">网关请求</option><option value="manual_test">手动测试</option><option value="manual_chat">手动对话</option><option value="scheduled_check">定时巡检</option><option value="all">全部来源</option></select>
        <select value={costSource} onChange={(event) => { setPage(1); setCostSource(event.target.value as CostSource | '') }} aria-label="费用估算来源">{COST_SOURCE_FILTER_OPTIONS.map((item) => <option value={item.value} key={item.value || 'all'}>{item.label}</option>)}</select>
        <select value={costStatus} onChange={(event) => { setPage(1); setCostStatus(event.target.value as CostStatus | '') }} aria-label="费用状态">{COST_STATUS_FILTER_OPTIONS.map((item) => <option value={item.value} key={item.value || 'all'}>{item.label}</option>)}</select>
        <select value={costBasis} onChange={(event) => { setPage(1); setCostBasis(event.target.value as CostBasis | '') }} aria-label="费用计费方式">{COST_BASIS_FILTER_OPTIONS.map((item) => <option value={item.value} key={item.value || 'all'}>{item.label}</option>)}</select>
        <span className="filter-count log-filter-count">{total} 条记录</span>
        <label className="search-field log-search"><Search size={14} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索模型 / 错误 / URL" aria-label="搜索日志" /></label>
        {(channel || token || model || searchQuery || status || source !== 'proxy' || costSource || costStatus || costBasis || range !== 'today') && <button className="icon-button icon-button--surface" type="button" title="清除筛选" aria-label="清除日志筛选" onClick={() => { setChannel(''); setToken(''); setModel(''); setSearch(''); setSearchQuery(''); setStatus(''); setSource('proxy'); setCostSource(''); setCostStatus(''); setCostBasis(''); setRange('today'); setPage(1) }}><RotateCcw size={16} /></button>}
      </div>

      <p className="cost-log-note">费用为请求发生时冻结的本地记录；未知金额显示为“—”，尚未与上游账单对账。</p>

      {loading ? <LoadingState label="正在加载请求日志" /> : error ? <ErrorState message={error} retry={() => void load()} /> : logs.length === 0 ? <EmptyState label="当前筛选条件下暂无请求日志" /> : (
        <div className="records-panel log-records" role="table" aria-label="请求日志列表">
          <div className="record-head log-grid" role="row"><span>时间 / 来源</span><span>渠道</span><span>状态</span><span>模型与协议</span><span>时延</span><span>访问令牌</span><span>Token 用量</span><span>费用口径</span></div>
          {logs.map((entry) => <LogRow entry={entry} key={entry.id} />)}
        </div>
      )}
      <Pagination page={page} pageSize={PAGE_SIZE} total={total} onPage={setPage} />
      </>}
    </div>
  )
}

function ActiveRequestsView({ autoRefreshSeconds }: { autoRefreshSeconds: number }) {
  const [requests, setRequests] = useState<ActiveRequest[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [debug, setDebug] = useState<{ id: number; data: unknown } | null>(null)
  const [debugLoading, setDebugLoading] = useState<number | null>(null)

  useEffect(() => {
    let active = true
    const refresh = async () => {
      try { const data = await getActiveRequests(); if (active) { setRequests(data); setError('') } }
      catch (reason) { if (active) setError(reason instanceof Error ? reason.message : '活动请求加载失败') }
      finally { if (active) setLoading(false) }
    }
    void refresh()
    if (autoRefreshSeconds <= 0) return () => { active = false }
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') void refresh()
    }, autoRefreshSeconds * 1000)
    return () => { active = false; window.clearInterval(timer) }
  }, [autoRefreshSeconds])

  const openDebug = async (request: ActiveRequest) => {
    setDebugLoading(request.id); setError('')
    try { setDebug({ id: request.id, data: await getActiveRequestDebug(request.id) }) }
    catch (reason) { setError(reason instanceof Error ? reason.message : '调试快照加载失败') }
    finally { setDebugLoading(null) }
  }

  if (loading) return <LoadingState label="正在读取活动请求" />
  return <>
    {error && <OperationNotice tone="error">{error}</OperationNotice>}
    {!requests.length ? <EmptyState label="当前没有进行中的请求" /> : <div className="records-panel"><div className="record-head active-request-grid"><span>开始时间</span><span>渠道 / 模型</span><span>上游</span><span>传输状态</span><span>调试</span></div>{requests.map((request) => <article className="record-row active-request-grid" key={request.id}>
      <div><strong>{formatTime(request.start_time)}</strong><span>#{request.id} · {request.is_streaming ? '流式' : '非流式'}</span>{request.request_id && <a className="request-cross-link" href={requestDetailHref(request.request_id)} title={request.request_id}><ListTree size={12} />请求详情</a>}</div>
      <div><strong title={request.channel_name || undefined}>{request.channel_name || ((request.channel_id || 0) > 0 ? `渠道 #${request.channel_id}` : '尚未选定渠道')}</strong><span>{request.model}</span></div>
      <div><strong title={request.base_url}>{request.upstream_protocol || '自动'}</strong><span>{request.api_key_used || '—'}</span></div>
      <div><strong>{statusLabel(request.upstream_status)}</strong><span>{formatNumber(request.bytes_received)} bytes{request.client_first_byte_time ? ` · 首字 ${request.client_first_byte_time.toFixed(2)}s` : ''}</span></div>
      <div><button className="icon-button icon-button--surface" type="button" onClick={() => void openDebug(request)} disabled={!request.debug_log_available || debugLoading === request.id} aria-label={`查看请求 ${request.id} 调试快照`} title={request.debug_log_available ? '调试快照' : '未开启调试捕获'}><Bug className={debugLoading === request.id ? 'spin' : ''} size={15} /></button></div>
    </article>)}</div>}
    {debug && <Modal title={`请求 #${debug.id} 调试快照`} close={() => setDebug(null)} wide><pre className="debug-snapshot">{JSON.stringify(debug.data, null, 2)}</pre></Modal>}
  </>
}

function LogRow({ entry }: { entry: LogEntry }) {
  const [copied, setCopied] = useState(false)
  const success = entry.status_code >= 200 && entry.status_code < 300
  const statusText = `${entry.status_code || 'ERR'} · ${success ? '成功' : '错误'}`
  const cost = buildCostEntryView(entry)
  const hasChannel = entry.channel_id > 0
  const channelName = entry.channel_name || (hasChannel ? `渠道 #${entry.channel_id}` : '路由汇总')
  const usedTokenLabel = logAuthTokenLabel(entry)
  const requestHref = entry.request_id ? requestDetailHref(entry.request_id) : ''
  const copyStatus = async () => {
    const value = entry.message ? `${statusText}\n${entry.message}` : statusText
    const fallbackCopy = () => {
      const textarea = document.createElement('textarea')
      textarea.value = value
      textarea.setAttribute('readonly', 'true')
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      const copiedByFallback = document.execCommand('copy')
      textarea.remove()
      if (!copiedByFallback) throw new Error('clipboard unavailable')
    }
    try {
      if (navigator.clipboard?.writeText) {
        try { await navigator.clipboard.writeText(value) }
        catch { fallbackCopy() }
      } else fallbackCopy()
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      // The log row remains usable when the browser denies clipboard access.
    }
  }
  return (
    <article className="record-row log-grid">
      <div><strong>{formatTime(entry.time)}</strong><span>{sourceLabel(entry.log_source)}</span>{requestHref && <a className="request-cross-link" href={requestHref} title={entry.request_id} aria-label={`查看请求 ${entry.request_id} 详情`}><ListTree size={12} />{compactRequestId(entry.request_id || '')}{entry.attempt_number ? ` · 尝试 #${entry.attempt_number}` : ''}</a>}</div>
      <div className="log-channel-cell"><strong title={entry.channel_name || undefined}>{channelName}</strong><span>{hasChannel ? `#${entry.channel_id}` : '未命中可用渠道'}</span></div>
      <div className="log-status"><div className="log-status-line"><span className={`status-badge status-badge--${success ? 'success' : 'danger'}`}>{statusText}</span>{entry.message && <button className="log-copy-button" type="button" onClick={() => void copyStatus()} aria-label="复制状态错误" title="复制状态和错误详情">{copied ? <Check size={13} /> : <Copy size={13} />}</button>}</div>{entry.message && <span className="record-message" title={entry.message}>{entry.message}</span>}</div>
      <div className="log-model-cell"><strong title={modelRedirectTitle(entry.model, entry.actual_model)} className={entry.actual_model ? 'log-model-name' : undefined}>{entry.model}{entry.actual_model && entry.actual_model !== entry.model && <span className="log-model-arrow" aria-label={`实际上游模型 ${entry.actual_model}`}> → {entry.actual_model}</span>}</strong><span>{entry.client_protocol || '—'} → {entry.upstream_protocol || '—'}</span></div>
      <div><strong>{entry.duration ? `${entry.duration.toFixed(2)}s` : '—'}</strong><span>首字 {entry.first_byte_time ? `${entry.first_byte_time.toFixed(2)}s` : '—'}</span></div>
      <div><strong title={usedTokenLabel.title}>{usedTokenLabel.label}</strong><span>{entry.auth_token_id ? `#${entry.auth_token_id}` : '系统任务 / 手动测试'}</span></div>
      <div><strong>{formatNumber(entry.input_tokens)} / {formatNumber(entry.output_tokens)}</strong><span>输入 / 输出 token</span></div>
      <div><strong title={cost.title}>{cost.amountText}</strong><span className={`cost-entry-status ${cost.statusClass}`} title={cost.title}>{cost.semanticLabel}</span></div>
    </article>
  )
}

function authTokenOptionLabel(token: AuthToken): string {
  const name = token.description?.trim() || token.token_hint?.trim() || `令牌 #${token.id}`
  return `${name} · #${token.id}`
}

function logAuthTokenLabel(entry: LogEntry): { label: string; title: string } {
  if (!entry.auth_token_id) return { label: '系统 / 无令牌', title: '手动测试或系统任务未使用下游访问令牌' }
  const name = entry.auth_token_description?.trim() || `令牌 #${entry.auth_token_id}`
  return { label: name, title: `访问令牌 #${entry.auth_token_id}` }
}

function sourceLabel(source?: string): string {
  if (source === 'manual_test') return '手动测试'
  if (source === 'manual_chat') return '手动对话'
  if (source === 'scheduled_check') return '定时巡检'
  return '网关请求'
}

function statusLabel(status: string): string {
  if (status === 'receiving') return '接收响应'
  if (status === 'retrying') return '正在重试'
  return '请求上游'
}
