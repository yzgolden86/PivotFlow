import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Activity,
  ArrowUpRight,
  Check,
  Clock3,
  Copy,
  Database,
  ListTree,
  RefreshCw,
  RotateCcw,
  Search,
  ShieldCheck,
} from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { getAuthTokens, getRequestTrace, getRequestTraces, getRequestTraceStats } from '../api'
import useReadSnapshot from '../components/useReadSnapshot'
import type {
  AuthToken,
  RequestAttempt,
  RequestAttemptOutcome,
  RequestOutcome,
  RequestTrace,
  RequestTraceStats,
} from '../types'
import { ErrorState, formatNumber, formatTime, LoadingState, Pagination } from './shared'
import { Modal } from './siteShared'
import {
  attemptChannelLabel,
  attemptCommitmentLabel,
  attemptCredentialLabel,
  attemptModelLabel,
  buildRequestDiagnostic,
  buildRequestTimeline,
  clientSuccessRate,
  cooldownLabel,
  finalStatusHint,
  finalStatusLabel,
  formatRate,
  isPendingRequestTraceError,
  reasonLabel,
  readRequestHistoryScope,
  requestCacheHistoryDiagnostic,
  retryLabel,
  upstreamSuccessRate,
  writeRequestHistoryScope,
} from './requestHistory'
import type { RequestHistoryScope } from './requestHistory'
import { routeProtocols, tokenOptions } from './modelRoutes'

const PAGE_SIZE = 20
const EMPTY_STATS: RequestTraceStats = {
  requests: 0,
  successes: 0,
  errors: 0,
  canceled: 0,
  upstream_attempts: 0,
  upstream_successes: 0,
}

export default function RequestHistoryView() {
  const location = useLocation()
  const navigate = useNavigate()
  const params = new URLSearchParams(location.search)
  const requestId = params.get('request')?.trim() || ''
  const scope = useMemo(() => readRequestHistoryScope(location.search), [location.search])
  const { range, authTokenId: token, outcome, model, clientProtocol, search: searchQuery, page } = scope
  const [search, setSearch] = useState(searchQuery)
  const [modelDraft, setModelDraft] = useState(model)
  const [tokenSearch, setTokenSearch] = useState('')
  const setFilters = useCallback((patch: Partial<RequestHistoryScope>, replace = false) => {
    const next = new URLSearchParams(location.search)
    next.delete('request')
    navigate(`/logs${writeRequestHistoryScope({ ...scope, page: 1, ...patch, error: '' }, next.toString())}`, { replace })
  }, [location.search, navigate, scope])
  useEffect(() => { setSearch(searchQuery) }, [searchQuery])
  useEffect(() => { setModelDraft(model) }, [model])

  useEffect(() => {
    if (search === searchQuery) return
    const timer = window.setTimeout(() => {
      setFilters({ search: search.trim() }, true)
    }, 300)
    return () => window.clearTimeout(timer)
  }, [search, searchQuery, setFilters])
  const readTokens = useCallback((signal: AbortSignal) => getAuthTokens(range, signal), [range])
  const tokenSnapshot = useReadSnapshot(`history-tokens:${range}`, readTokens)
  const tokens = tokenOptions(tokenSnapshot.data?.tokens || [], tokenSearch, token)

  const readHistory = useCallback(async (signal: AbortSignal) => {
    const filters = {
      range, search: searchQuery, outcome, auth_token_id: token,
      model, client_protocol: clientProtocol,
      limit: PAGE_SIZE, offset: (page - 1) * PAGE_SIZE,
    }
    const [result, nextStats] = await Promise.all([getRequestTraces(filters, signal), getRequestTraceStats(filters, signal)])
    return { result, stats: nextStats }
  }, [clientProtocol, model, outcome, page, range, searchQuery, token])
  const history = useReadSnapshot(writeRequestHistoryScope(scope), readHistory, !scope.error)
  const requests = history.data?.result.data || []
  const stats = history.data?.stats || EMPTY_STATS
  const total = history.data?.result.count || 0
  const loading = history.loading && !history.data
  const refreshing = history.loading && Boolean(history.data)
  const error = history.error

  const openRequest = (id: string) => {
    const next = new URLSearchParams(params)
    next.set('view', 'requests')
    next.set('request', id)
    navigate(`/logs?${next.toString()}`)
  }

  const closeRequest = () => {
    const next = new URLSearchParams(params)
    next.set('view', 'requests')
    next.delete('request')
    navigate(`/logs?${next.toString()}`, { replace: true })
  }

  const clearFilters = () => {
    navigate('/logs?view=requests&range=today')
  }

  const hasFilters = Boolean(token || model || clientProtocol || outcome || searchQuery || range !== 'today' || scope.error)

  return <>
    <div className="request-history-note">
      <span aria-hidden="true"><Database size={16} /></span>
      <div>
        <strong>已保存的网关请求</strong>
        <p>仅展示请求历史功能上线后成功写入的摘要。旧日志不会补录，定时巡检探测不计入。</p>
      </div>
    </div>

    <div className="filter-bar filter-bar--wide request-filter-bar">
      <select value={range} onChange={(event) => setFilters({ range: event.target.value as RequestHistoryScope['range'] })} aria-label="完整请求时间范围">
        <option value="today">今日</option>
        <option value="this_week">本周</option>
        <option value="this_month">本月</option>
      </select>
      <label className="search-field request-token-search"><Search size={14} /><input value={tokenSearch} onChange={(event) => setTokenSearch(event.target.value)} placeholder="查找令牌" aria-label="搜索历史令牌" /></label>
      <select value={token} onChange={(event) => setFilters({ authTokenId: event.target.value })} aria-label="完整请求访问令牌">
        <option value="">全部令牌</option>
        {token && !tokens.some((item) => String(item.id) === token) && <option value={token}>所选令牌未载入 · #{token}</option>}
        {tokens.map((item) => <option value={item.id} key={item.id}>{authTokenOptionLabel(item)}</option>)}
      </select>
      <select value={clientProtocol} onChange={(event) => setFilters({ clientProtocol: event.target.value })} aria-label="历史客户端协议">
        <option value="">全部协议</option>
        {clientProtocol && !routeProtocols.some((item) => item.value === clientProtocol) && <option value={clientProtocol}>无效协议</option>}
        {routeProtocols.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
      </select>
      <select value={outcome} onChange={(event) => setFilters({ outcome: event.target.value as RequestOutcome | '' })} aria-label="最终结果">
        <option value="">全部结果</option>
        <option value="success">成功</option>
        <option value="error">失败</option>
        <option value="canceled">已取消</option>
      </select>
      <span className="filter-count request-filter-count">{total} 个请求</span>
      <label className="search-field request-search">
        <Search size={14} />
        <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索请求 ID / 模型" aria-label="搜索完整请求" />
      </label>
      <form className="request-model-filter" onSubmit={(event) => { event.preventDefault(); setFilters({ model: modelDraft }) }}>
        <input value={modelDraft} onChange={(event) => setModelDraft(event.target.value)} placeholder="完整请求模型（精确匹配）" aria-label="精确请求模型" spellCheck={false} />
        <button type="submit" className="secondary-button">筛选模型</button>
      </form>
      {hasFilters && <button className="icon-button icon-button--surface" type="button" title="清除筛选" aria-label="清除完整请求筛选" onClick={clearFilters}><RotateCcw size={16} /></button>}
      <button className="icon-button icon-button--surface" type="button" disabled={refreshing || Boolean(scope.error)} onClick={history.refresh} aria-label="刷新完整请求" title="刷新完整请求"><RefreshCw className={refreshing ? 'spin' : undefined} size={16} /></button>
    </div>

    {tokenSnapshot.error && <p className="inline-error" role="alert">令牌清单读取失败，当前历史范围保持。<button className="text-button" type="button" onClick={tokenSnapshot.refresh}>重试令牌清单</button></p>}
    {scope.error ? <div className="inline-error" role="alert">{scope.error}<button className="secondary-button" type="button" onClick={clearFilters}>清除无效筛选</button></div> : loading ? <LoadingState label="正在加载完整请求" /> : error ? <ErrorState message={error} retry={history.refresh} /> : <>
      <RequestStatsStrip stats={stats} />
      {requests.length === 0 ? <div className="content-state content-state--empty request-empty">当前筛选条件下暂无已保存请求</div> : (
        <div className="records-panel request-ledger" aria-label="完整请求列表">
          <div className="request-ledger-head" aria-hidden="true">
            <span>最终结果 / 开始时间</span>
            <span>模型与协议</span>
            <span>传输与尝试</span>
            <span>耗时</span>
            <span />
          </div>
          {requests.map((request) => <button
            className={`request-ledger-row request-ledger-row--${request.outcome}`}
            type="button"
            key={request.request_id}
            onClick={() => openRequest(request.request_id)}
            aria-label={`查看请求 ${request.request_id} 详情`}
          >
            <span className="request-ledger-cell request-ledger-result">
              <span><RequestOutcomeBadge outcome={request.outcome} /> <RequestCacheBadge trace={request} /> <strong>{formatTime(request.started_at)}</strong></span>
              <code title={request.request_id}>{request.request_id}</code>
            </span>
            <span className="request-ledger-cell">
              <strong title={request.model}>{request.model || '未记录模型'}</strong>
              <span title={finalStatusHint(request.final_status)}>{request.client_protocol || '—'} → {finalStatusLabel(request.final_status)}</span>
            </span>
            <span className="request-ledger-cell">
              <strong>{transportLabel(request.transport)}{request.is_streaming ? ' · 流式' : ''}</strong>
              <span>{request.attempt_count} 次尝试 · 上游 {request.upstream_attempt_count} 次{request.truncated ? ' · 明细截断' : ''}</span>
            </span>
            <span className="request-ledger-cell request-ledger-timing">
              <strong>{formatSeconds(request.duration)}</strong>
              <span>等待 {formatSeconds(request.wait_duration)}</span>
            </span>
            <span className="request-ledger-open" aria-hidden="true"><ArrowUpRight size={16} /></span>
          </button>)}
        </div>
      )}
      <Pagination page={page} pageSize={PAGE_SIZE} total={total} onPage={(page) => setFilters({ page })} />
    </>}

    {requestId && <RequestDetailModal requestId={requestId} close={closeRequest} />}
  </>
}

function RequestStatsStrip({ stats }: { stats: RequestTraceStats }) {
  return <div className="request-stats-strip" aria-label="完整请求汇总">
    <RequestStat label="已保存请求" value={formatNumber(stats.requests)} detail="当前筛选范围" icon={Database} />
    <RequestStat label="最终成功" value={formatNumber(stats.successes)} detail="客户端结果" icon={ShieldCheck} />
    <RequestStat label="最终失败" value={formatNumber(stats.errors)} detail="客户端结果" icon={Activity} tone="danger" />
    <RequestStat label="已取消" value={formatNumber(stats.canceled)} detail="独立计数，不进入成功率" icon={Clock3} tone="warning" />
    <RequestStat label="客户端成功率" value={formatRate(clientSuccessRate(stats))} detail="成功 ÷（成功 + 失败），取消除外" icon={ListTree} />
    <RequestStat label="上游成功率" value={formatRate(upstreamSuccessRate(stats))} detail={`${formatNumber(stats.upstream_successes)} / ${formatNumber(stats.upstream_attempts)} 次上游成功`} icon={ArrowUpRight} />
  </div>
}

function RequestStat({ label, value, detail, icon: Icon, tone = 'default' }: {
  label: string
  value: string
  detail: string
  icon: typeof Database
  tone?: 'default' | 'warning' | 'danger'
}) {
  return <article className={`request-stat-card request-stat-card--${tone}`}>
    <span aria-hidden="true"><Icon size={15} /></span>
    <div><small>{label}</small><strong>{value}</strong><p>{detail}</p></div>
  </article>
}

function RequestDetailModal({ requestId, close }: { requestId: string; close: () => void }) {
  const [trace, setTrace] = useState<RequestTrace | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const [retryKey, setRetryKey] = useState(0)
  const [copied, setCopied] = useState(false)
  const cacheDiagnostic = trace ? requestCacheHistoryDiagnostic(trace) : null

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    setPending(false)
    void getRequestTrace(requestId, controller.signal)
      .then((result) => { if (!controller.signal.aborted) setTrace(result) })
      .catch((reason) => {
        if (!controller.signal.aborted) {
          setTrace(null)
          const awaitingTrace = isPendingRequestTraceError(reason)
          setPending(awaitingTrace)
          setError(awaitingTrace ? '' : reason instanceof Error ? reason.message : '请求详情加载失败')
        }
      })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [requestId, retryKey])

  useEffect(() => setCopied(false), [requestId])

  const copyDiagnostic = async () => {
    if (!trace) return
    const text = JSON.stringify(buildRequestDiagnostic(trace), null, 2)
    const fallbackCopy = () => {
      const textarea = document.createElement('textarea')
      textarea.value = text
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
        try { await navigator.clipboard.writeText(text) }
        catch { fallbackCopy() }
      } else fallbackCopy()
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    } catch {
      setCopied(false)
    }
  }

  return <Modal title={`完整请求 ${requestId}`} close={close} wide>
    <div className="request-detail">
      {loading ? <div className="request-detail-state"><LoadingState label="正在加载请求时间线" /></div> : pending ? (
        <div className="request-detail-state request-detail-pending" role="status">
          <Clock3 size={22} />
          <strong>请求记录尚未就绪</strong>
          <span>请求仍在执行或记录尚未保存，请稍后重试</span>
          <button className="secondary-button" type="button" onClick={() => setRetryKey((value) => value + 1)}><RefreshCw size={15} />重新检查</button>
        </div>
      ) : error ? (
        <div className="request-detail-state"><ErrorState message={error} retry={() => setRetryKey((value) => value + 1)} /></div>
      ) : trace ? (
        <>
          <header className="request-detail-hero">
            <div>
              <span className={`request-outcome-mark request-outcome-mark--${trace.outcome}`}><RequestOutcomeBadge outcome={trace.outcome} /></span>
              <strong>{trace.model || '未记录模型'}</strong>
              <code>{trace.request_id}</code>
            </div>
            <button className="secondary-button request-copy-diagnostic" type="button" onClick={() => void copyDiagnostic()}>
              {copied ? <Check size={15} /> : <Copy size={15} />}{copied ? '已复制' : '复制诊断 JSON'}
            </button>
          </header>

          <div className="request-detail-facts">
            <RequestFact label="开始时间" value={formatTime(trace.started_at)} />
            <RequestFact label="最终状态" value={finalStatusLabel(trace.final_status)} title={finalStatusHint(trace.final_status)} />
            <RequestFact label="客户端协议" value={trace.client_protocol || '—'} />
            <RequestFact label="传输" value={`${transportLabel(trace.transport)}${trace.is_streaming ? ' · 流式' : ' · 非流式'}`} />
            <RequestFact label="总耗时" value={formatSeconds(trace.duration)} />
            <RequestFact label="全局等待" value={formatSeconds(trace.wait_duration)} />
            {cacheDiagnostic && <RequestFact label="请求缓存" value={cacheDiagnostic.label} title={cacheDiagnostic.reasonLabel} />}
          </div>

          {trace.outcome === 'canceled' && <div className="request-detail-alert request-detail-alert--warning"><Clock3 size={16} /><div><strong>客户端已取消</strong><span>取消独立计数，不进入客户端成功率分母。</span></div></div>}
          {cacheDiagnostic && <div className={`request-cache-diagnostic request-cache-diagnostic--${cacheDiagnostic.status}`} role="note">
            <span aria-hidden="true"><Database size={17} /></span>
            <div><strong>{cacheDiagnostic.label} · {cacheDiagnostic.reasonLabel}</strong><p>{cacheDiagnostic.explanation}</p></div>
          </div>}
          {trace.truncated && <div className="request-detail-alert"><ListTree size={16} /><div><strong>时间线已截断</strong><span>仅保存前 128 条事件；下方计数仍反映全部真实尝试。</span></div></div>}

          <section className="request-timeline-section">
            <header>
              <div><ListTree size={17} /><h3>尝试时间线</h3></div>
              <span>{trace.attempt_count} 次尝试 · {trace.upstream_attempt_count} 次已发上游 · {trace.upstream_success_count} 次上游成功</span>
            </header>
            {trace.attempts?.length ? <RequestTimeline trace={trace} /> : <div className="request-timeline-empty"><Activity size={18} /><div><strong>{cacheDiagnostic?.status === 'hit' ? '缓存命中，未执行上游尝试' : '没有可用的时间线事件'}</strong><span>{cacheDiagnostic?.status === 'hit' ? '命中请求复用已保存响应，空白上游时间线是预期结果。' : '不根据空白明细推断成功；最终结果以上方保存的请求摘要为准。'}</span></div></div>}
          </section>
        </>
      ) : null}
    </div>
  </Modal>
}

function RequestTimeline({ trace }: { trace: RequestTrace }) {
  const timeline = buildRequestTimeline(trace)
  return <ol className="request-timeline">
    {timeline.map((entry, index) => <RequestTimelineEvent
      attempt={entry.attempt}
      offsetSeconds={entry.offset_seconds}
      gapSeconds={entry.gap_seconds}
      first={index === 0}
      key={`${entry.attempt.number}-${entry.attempt.started_at}`}
    />)}
  </ol>
}

function RequestTimelineEvent({ attempt, offsetSeconds, gapSeconds, first }: {
  attempt: RequestAttempt
  offsetSeconds: number
  gapSeconds: number | null
  first: boolean
}) {
  return <li className={`request-timeline-event request-timeline-event--${attempt.outcome}`}>
    <span className="request-timeline-marker" aria-hidden="true">{attempt.number}</span>
    <article>
      <header>
        <div>
          <strong>尝试 #{attempt.number}</strong>
          <span>{first ? `请求开始后 ${formatSeconds(offsetSeconds)}` : `距上次结束 ${formatSeconds(gapSeconds ?? 0)}`}</span>
        </div>
        <AttemptOutcomeBadge outcome={attempt.outcome} />
      </header>
      <div className="request-timeline-route">
        <span><strong>{attemptChannelLabel(attempt)}</strong><small>{attemptCredentialLabel(attempt)}</small></span>
        <span><strong>{attemptModelLabel(attempt)}</strong><small>{attempt.upstream_protocol || '—'} · {transportLabel(attempt.transport)}</small></span>
        <span><strong>{attempt.status_code ? finalStatusLabel(attempt.status_code) : '无 HTTP 状态'}</strong><small>{attempt.upstream ? '已发往上游' : '未发往上游'} · {attemptCommitmentLabel(attempt)}</small></span>
        <span><strong>{formatSeconds(attempt.duration)}</strong><small>首字 {formatSeconds(attempt.first_byte_time)}</small></span>
      </div>
      <div className="request-timeline-signals">
        {attempt.endpoint && <code title={attempt.endpoint}>{attempt.endpoint}</code>}
        {attempt.reason && <span title={`固定原因码：${attempt.reason}`}>原因 <code>{reasonLabel(attempt.reason)}</code></span>}
        {attempt.retry && attempt.retry !== 'none' && <span title={`重试策略码：${attempt.retry}`}>重试 <code>{retryLabel(attempt.retry)}</code></span>}
        {attempt.cooldown && attempt.cooldown !== 'none' && <span title={`冷却策略码：${attempt.cooldown}`}>冷却 <code>{cooldownLabel(attempt.cooldown)}</code>{attempt.cooldown_until ? ` 至 ${formatTime(attempt.cooldown_until)}` : ''}</span>}
      </div>
    </article>
  </li>
}

function RequestFact({ label, value, title }: { label: string; value: string; title?: string }) {
  return <span title={title}><small>{label}</small><strong>{value}</strong></span>
}

function RequestOutcomeBadge({ outcome }: { outcome: RequestOutcome }) {
  return <span className={`status-badge ${outcome === 'success' ? 'status-badge--success' : outcome === 'canceled' ? 'status-badge--warning' : 'status-badge--danger'}`}>{outcomeLabel(outcome)}</span>
}

function RequestCacheBadge({ trace }: { trace: RequestTrace }) {
  const diagnostic = requestCacheHistoryDiagnostic(trace)
  if (!diagnostic) return null
  return <span className={`request-cache-badge request-cache-badge--${diagnostic.status}`} title={diagnostic.reasonLabel}>{diagnostic.label}</span>
}

function AttemptOutcomeBadge({ outcome }: { outcome: RequestAttemptOutcome }) {
  const tone = outcome === 'success' ? 'success' : outcome === 'canceled' || outcome === 'capability' || outcome === 'skipped' ? 'warning' : 'danger'
  return <span className={`status-badge status-badge--${tone}`}>{attemptOutcomeLabel(outcome)}</span>
}

function outcomeLabel(outcome: RequestOutcome): string {
  if (outcome === 'success') return '成功'
  if (outcome === 'canceled') return '已取消'
  return '失败'
}

function attemptOutcomeLabel(outcome: RequestAttemptOutcome): string {
  if (outcome === 'success') return '成功'
  if (outcome === 'canceled') return '已取消'
  if (outcome === 'capability') return '能力探测'
  if (outcome === 'skipped') return '已跳过'
  return '失败'
}

function transportLabel(transport: string): string {
  return transport === 'websocket' ? 'WebSocket' : transport === 'http' ? 'HTTP' : transport || '—'
}

function authTokenOptionLabel(token: AuthToken): string {
  const name = token.description?.trim() || token.token_hint?.trim() || `令牌 #${token.id}`
  return `${name} · #${token.id}`
}

function formatSeconds(value: number): string {
  if (!Number.isFinite(value) || value < 0) return '—'
  return `${value.toFixed(value >= 10 ? 1 : 2)}s`
}
