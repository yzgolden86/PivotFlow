import { useCallback, useEffect, useState } from 'react'
import { Bug, History, RefreshCw, Search, Waves } from 'lucide-react'
import { getActiveRequestDebug, getActiveRequests, getLogs, getLogsBootstrap } from '../api'
import type { ActiveRequest, DashboardRange, LogEntry, LogsBootstrap } from '../types'
import { EmptyState, ErrorState, formatMoney, formatNumber, formatTime, LoadingState, Pagination } from './shared'
import { useLocation } from 'react-router-dom'
import { Modal } from './siteShared'

const PAGE_SIZE = 20
const EMPTY_BOOTSTRAP: LogsBootstrap = { channel_test_content: '', models: [], channels: [], status_codes: [] }

export default function LogsPage() {
  const location = useLocation()
  const view = new URLSearchParams(location.search).get('view') === 'active' ? 'active' : 'history'
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [options, setOptions] = useState<LogsBootstrap>(EMPTY_BOOTSTRAP)
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [range, setRange] = useState<DashboardRange>('today')
  const [channel, setChannel] = useState('')
  const [model, setModel] = useState('')
  const [status, setStatus] = useState('')
  const [source, setSource] = useState('proxy')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true)
    setError('')
    try {
      const filters = { range, channel_name: channel, model, status_code: status, log_source: source, limit: PAGE_SIZE, offset: (page - 1) * PAGE_SIZE }
      const [result, bootstrap] = await Promise.all([getLogs(filters, signal), getLogsBootstrap(range, signal)])
      setLogs(result.data)
      setTotal(result.count)
      setOptions(bootstrap)
    } catch (reason) {
      if (!signal?.aborted) setError(reason instanceof Error ? reason.message : '日志加载失败')
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [channel, model, page, range, source, status])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  return (
    <div className="workspace-page">
      <header className="page-header">
        <h1>请求日志</h1>
        <div className="header-controls"><div className="page-tabs page-tabs--header" role="tablist"><a className={view === 'history' ? 'is-active' : ''} href="#/logs" role="tab" aria-selected={view === 'history'}><History size={14} />历史请求</a><a className={view === 'active' ? 'is-active' : ''} href="#/logs?view=active" role="tab" aria-selected={view === 'active'}><Waves size={14} />进行中</a></div>{view === 'history' && <button className="icon-button icon-button--surface" type="button" onClick={() => void load()} aria-label="刷新日志"><RefreshCw size={17} /></button>}</div>
      </header>

      {view === 'active' ? <ActiveRequestsView /> : <>
      <div className="filter-bar filter-bar--wide">
        <select value={range} onChange={(event) => { setPage(1); setRange(event.target.value as DashboardRange) }} aria-label="日志时间范围"><option value="today">今日</option><option value="this_week">本周</option><option value="this_month">本月</option></select>
        <select value={channel} onChange={(event) => { setPage(1); setChannel(event.target.value) }} aria-label="日志渠道"><option value="">全部渠道</option>{options.channels.map((item) => <option value={item.name} key={item.id}>{item.name}</option>)}</select>
        <select value={model} onChange={(event) => { setPage(1); setModel(event.target.value) }} aria-label="日志模型"><option value="">全部模型</option>{options.models.map((item) => <option value={item} key={item}>{item}</option>)}</select>
        <select value={status} onChange={(event) => { setPage(1); setStatus(event.target.value) }} aria-label="响应状态"><option value="">全部状态</option>{options.status_codes.map((item) => <option value={item} key={item}>{item}</option>)}</select>
        <select value={source} onChange={(event) => { setPage(1); setSource(event.target.value) }} aria-label="日志来源"><option value="proxy">网关请求</option><option value="manual_test">手动测试</option><option value="manual_chat">手动对话</option><option value="scheduled_check">定时巡检</option><option value="all">全部来源</option></select>
        <span className="filter-count"><Search size={14} />{total} 条记录</span>
      </div>

      {loading ? <LoadingState label="正在加载请求日志" /> : error ? <ErrorState message={error} retry={() => void load()} /> : logs.length === 0 ? <EmptyState label="当前筛选条件下暂无请求日志" /> : (
        <div className="records-panel log-records" role="table" aria-label="请求日志列表">
          <div className="record-head log-grid" role="row"><span>时间 / 来源</span><span>渠道</span><span>模型与协议</span><span>状态</span><span>时延</span><span>Token</span><span>费用</span></div>
          {logs.map((entry) => <LogRow entry={entry} key={entry.id} />)}
        </div>
      )}
      <Pagination page={page} pageSize={PAGE_SIZE} total={total} onPage={setPage} />
      </>}
    </div>
  )
}

function ActiveRequestsView() {
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
    const timer = window.setInterval(() => void refresh(), 2000)
    return () => { active = false; window.clearInterval(timer) }
  }, [])

  const openDebug = async (request: ActiveRequest) => {
    setDebugLoading(request.id); setError('')
    try { setDebug({ id: request.id, data: await getActiveRequestDebug(request.id) }) }
    catch (reason) { setError(reason instanceof Error ? reason.message : '调试快照加载失败') }
    finally { setDebugLoading(null) }
  }

  if (loading) return <LoadingState label="正在读取活动请求" />
  return <>
    {error && <div className="inline-error">{error}</div>}
    {!requests.length ? <EmptyState label="当前没有进行中的请求" /> : <div className="records-panel"><div className="record-head active-request-grid"><span>开始时间</span><span>渠道 / 模型</span><span>上游</span><span>传输状态</span><span>调试</span></div>{requests.map((request) => <article className="record-row active-request-grid" key={request.id}>
      <div><strong>{formatTime(request.start_time)}</strong><span>#{request.id} · {request.is_streaming ? '流式' : '非流式'}</span></div>
      <div><strong>{request.channel_name || `渠道 #${request.channel_id}`}</strong><span>{request.model}</span></div>
      <div><strong title={request.base_url}>{request.upstream_protocol || '自动'}</strong><span>{request.api_key_used || '—'}</span></div>
      <div><strong>{statusLabel(request.upstream_status)}</strong><span>{formatNumber(request.bytes_received)} bytes{request.client_first_byte_time ? ` · 首字 ${request.client_first_byte_time.toFixed(2)}s` : ''}</span></div>
      <div><button className="icon-button icon-button--surface" type="button" onClick={() => void openDebug(request)} disabled={!request.debug_log_available || debugLoading === request.id} aria-label={`查看请求 ${request.id} 调试快照`} title={request.debug_log_available ? '调试快照' : '未开启调试捕获'}><Bug className={debugLoading === request.id ? 'spin' : ''} size={15} /></button></div>
    </article>)}</div>}
    {debug && <Modal title={`请求 #${debug.id} 调试快照`} close={() => setDebug(null)} wide><pre className="debug-snapshot">{JSON.stringify(debug.data, null, 2)}</pre></Modal>}
  </>
}

function LogRow({ entry }: { entry: LogEntry }) {
  const success = entry.status_code >= 200 && entry.status_code < 300
  const effectiveCost = entry.cost * (entry.cost_multiplier > 0 ? entry.cost_multiplier : 1)
  return (
    <article className="record-row log-grid">
      <div><strong>{formatTime(entry.time)}</strong><span>{sourceLabel(entry.log_source)}</span></div>
      <div><strong>{entry.channel_name || `渠道 #${entry.channel_id}`}</strong><span>#{entry.channel_id}</span></div>
      <div><strong title={entry.actual_model && entry.actual_model !== entry.model ? `${entry.model} → ${entry.actual_model}` : entry.model}>{entry.model}</strong><span>{entry.client_protocol || '—'} → {entry.upstream_protocol || '—'}</span></div>
      <div><span className={`status-badge status-badge--${success ? 'success' : 'danger'}`}>{entry.status_code || 'ERR'}</span>{entry.message && <span className="record-message" title={entry.message}>{entry.message}</span>}</div>
      <div><strong>{entry.duration ? `${entry.duration.toFixed(2)}s` : '—'}</strong><span>首字 {entry.first_byte_time ? `${entry.first_byte_time.toFixed(2)}s` : '—'}</span></div>
      <div><strong>{formatNumber(entry.input_tokens)} / {formatNumber(entry.output_tokens)}</strong><span>输入 / 输出</span></div>
      <div><strong>{formatMoney(effectiveCost)}</strong><span>{entry.is_streaming ? '流式' : '非流式'}</span></div>
    </article>
  )
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
