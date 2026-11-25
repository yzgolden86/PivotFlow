import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ArrowRight, Check, Copy, KeyRound, Pencil, Play, PlugZap, Plus, Power, RefreshCw, Search, Trash2 } from 'lucide-react'
import { useLocation } from 'react-router-dom'
import { createAuthToken, deleteAuthToken, getAuthTokens, getChannels, getModelRouteInventory, revealAuthToken, updateAuthToken } from '../api'
import useReadSnapshot from '../components/useReadSnapshot'
import HelpTip from '../components/HelpTip'
import type { AuthToken, Channel, DashboardRange } from '../types'
import { EmptyState, ErrorState, formatMoney, formatNumber, LoadingState, OperationNotice, PageHeader, Pagination } from './shared'
import { Modal } from './siteShared'
import { tokenMetadata } from './clientConnection'
import './tokensPresentation.css'
import { listWindow } from './listWindow'
import ResourceDetails, { DetailList, detailDate } from './ResourceDetails'

interface TokenFormValue {
  description: string
  expires: string
  allowedModels: string[]
  allowedChannels: number[]
  restrictionMode: 'allow' | 'deny'
  costLimit: number
  maxConcurrency: number
  active: boolean
}

const emptyForm: TokenFormValue = {
  description: '', expires: '', allowedModels: [], allowedChannels: [], restrictionMode: 'allow',
  costLimit: 0, maxConcurrency: 0, active: true,
}

const rangeNames = { today: '今日', this_week: '本周', this_month: '本月' }

export default function TokensPage() {
  const location = useLocation()
  const requestedTokenId = Number(new URLSearchParams(location.search).get('token')) || 0
  const openedToken = useRef(0)
  const [tokens, setTokens] = useState<AuthToken[]>([])
  const [range, setRange] = useState<DashboardRange>('today')
  const loadSequence = useRef(0)
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<AuthToken | 'new' | null>(null)
  const [details, setDetails] = useState<AuthToken | null>(null)
  const [createdToken, setCreatedToken] = useState<{ value: string; id: number } | null>(null)
  const [copiedId, setCopiedId] = useState<number | 'created' | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [refreshing, setRefreshing] = useState(false)

  // 手动刷新不带 AbortSignal，切换区间时旧请求不会被取消，按序号丢弃过期响应。
  const load = useCallback(async (signal?: AbortSignal) => {
    const sequence = ++loadSequence.current
    const stale = () => Boolean(signal?.aborted) || sequence !== loadSequence.current
    setLoading(true); setError('')
    try {
      const data = await getAuthTokens(range, signal)
      if (stale()) return
      setTokens(data.tokens || [])
    }
    catch (reason) { if (!stale()) setError(reason instanceof Error ? reason.message : '令牌加载失败') }
    finally { if (!stale()) setLoading(false) }
  }, [range])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  useEffect(() => {
    if (!requestedTokenId || loading || openedToken.current === requestedTokenId) return
    const selected = tokens.find((token) => token.id === requestedTokenId)
    if (selected) { openedToken.current = requestedTokenId; setEditing(selected) }
  }, [requestedTokenId, loading, tokens])

  const visible = useMemo(() => {
    const value = query.trim().toLowerCase()
    const filtered = value ? tokens.filter((token) => token.description.toLowerCase().includes(value) || String(token.id).includes(value)) : tokens
    return [...filtered].sort((left, right) => createdTimestamp(right) - createdTimestamp(left))
  }, [query, tokens])
  const tokenWindow = listWindow(visible.length, page, pageSize)
  const pagedTokens = visible.slice(tokenWindow.start, tokenWindow.end)

  const totals = useMemo(() => ({
    active: tokens.filter((token) => token.is_active).length,
    requests: tokens.reduce((sum, token) => sum + token.success_count + token.failure_count, 0),
    quotaUsed: tokens.reduce((sum, token) => sum + token.cost_used_usd, 0),
  }), [tokens])
  const rangeName = rangeNames[range as keyof typeof rangeNames] || '所选时间'

  const toggle = async (token: AuthToken) => {
    setBusyId(token.id); setError('')
    try {
      await updateAuthToken(token.id, { is_active: !token.is_active })
      setTokens((items) => items.map((item) => item.id === token.id ? { ...item, is_active: !item.is_active } : item))
    } catch (reason) { setError(reason instanceof Error ? reason.message : '状态更新失败') }
    finally { setBusyId(null) }
  }

  const remove = async (token: AuthToken) => {
    if (!window.confirm(`删除令牌“${token.description}”？该操作立即生效。`)) return
    setBusyId(token.id); setError('')
    try { await deleteAuthToken(token.id); setTokens((items) => items.filter((item) => item.id !== token.id)) }
    catch (reason) { setError(reason instanceof Error ? reason.message : '删除失败') }
    finally { setBusyId(null) }
  }

  const reveal = async (token: AuthToken) => {
    setBusyId(token.id); setError('')
    try {
      const result = await revealAuthToken(token.id)
      await copyValue(result.token, token.id)
    } catch (reason) { setError(reason instanceof Error ? reason.message : '令牌恢复失败') }
    finally { setBusyId(null) }
  }

  const copyValue = async (value: string, id?: number | 'created') => {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(value)
      } else {
        const textarea = document.createElement('textarea')
        textarea.value = value
        textarea.setAttribute('readonly', 'true')
        textarea.style.position = 'fixed'
        textarea.style.opacity = '0'
        try {
          document.body.appendChild(textarea)
          textarea.select()
          if (!document.execCommand('copy')) throw new Error('clipboard unavailable')
        } finally { textarea.remove() }
      }
    } catch {
      throw new Error('复制失败，请检查浏览器剪贴板权限')
    }
    if (id === undefined) return
    setCopiedId(id)
    window.setTimeout(() => setCopiedId((current) => current === id ? null : current), 1800)
  }

  return <div className="workspace-page tokens-page">
    <PageHeader
      icon={KeyRound}
      title="令牌管理"
      tone="amber"
      actions={<>
        <button className="primary-button" type="button" onClick={() => setEditing('new')}><Plus size={16} />创建令牌</button>
        <button className="icon-button icon-button--surface" type="button" disabled={refreshing} onClick={async () => { setRefreshing(true); try { await load() } finally { setRefreshing(false) } }} aria-label="刷新令牌"><RefreshCw size={17} className={refreshing ? 'spin' : undefined} /></button>
      </>}
    />

    <div className="filter-bar">
      <label className="search-field"><Search size={16} /><input value={query} onChange={(event) => { setQuery(event.target.value); setPage(1) }} placeholder="搜索名称或 ID" aria-label="搜索令牌" /></label>
      <div className="range-control range-control--compact" role="radiogroup" aria-label="令牌统计范围">
        {([['today', '今日'], ['this_week', '本周'], ['this_month', '本月']] as const).map(([value, label]) => <button className={range === value ? 'is-active' : ''} type="button" role="radio" aria-checked={range === value} onClick={() => setRange(value)} key={value}>{label}</button>)}
      </div>
      <span className="filter-count">{visible.length} 条</span>
    </div>

    <section className="compact-summary" aria-label="令牌摘要">
      <span><strong>{tokens.length}</strong>令牌总数</span><span><strong>{totals.active}</strong>已启用</span>
      <span><strong>{loading ? '—' : formatNumber(totals.requests)}</strong>{rangeName}请求记录</span>
      <span><strong>{formatMoney(totals.quotaUsed)}</strong>累计额度用量<HelpTip label="令牌用量" text="累计额度用量来自持久结算回执，用于费用软限额。请求统计和列表中的日志费用按所选时间查询，可能因历史或缺失日志与额度累计不同。所有金额均为本地口径，未与上游账单对账。" /></span>
    </section>

    {error && tokens.length > 0 && <OperationNotice tone="error">{error}</OperationNotice>}
    {loading ? <LoadingState label="正在加载令牌" /> : error && !tokens.length ? <ErrorState message={error} retry={() => void load()} /> : !visible.length ? <EmptyState label={query.trim() ? '没有符合搜索条件的令牌' : '创建一个令牌，开始配置客户端接入'} /> : <div className="token-table-wrap" role="region" aria-label="令牌列表" tabIndex={0}><div className="token-table-head"><span aria-hidden="true" /><span>名称</span><span>令牌</span><span className="token-number-cell">{rangeName}请求</span><span className="token-number-cell">{rangeName}日志费用</span><span>限制</span><span>创建时间</span><span>最后使用</span><span>操作</span></div><div className="token-list">
      {pagedTokens.map((token) => <article className="token-row" key={token.id}>
        <span className={`token-icon${token.is_active ? '' : ' token-icon--off'}`}><KeyRound size={17} /></span>
        <div className="token-identity"><strong title={token.description}>{token.description}</strong><span>令牌 #{token.id} · {token.is_active ? '已启用' : '已停用'}</span><span className="token-mobile-stats">{rangeName} {formatNumber(token.success_count + token.failure_count)} 次请求 · 日志费用 {formatMoney(token.effective_cost_usd)}</span><button className="resource-details-trigger" type="button" onClick={() => setDetails(token)} aria-label={`查看 ${token.description} 详情`}>详情</button></div>
        <div className="token-secret-cell"><code>{token.token_hint || token.token || '****'}</code><button className="icon-button icon-button--surface" type="button" onClick={() => void reveal(token)} disabled={busyId === token.id || !token.token_recoverable} aria-label={`复制 ${token.description}`} title={token.token_recoverable ? '复制令牌' : '历史令牌不可恢复'}>{copiedId === token.id ? <Check size={15} /> : <Copy size={15} />}</button></div>
        <div className="token-number-cell"><strong>{formatNumber(token.success_count + token.failure_count)}</strong><span>{token.failure_count} 次失败</span></div>
        <div className="token-number-cell"><strong title={`${rangeName}归属此令牌的代理日志费用，尚未与上游账单对账`}>{formatMoney(token.effective_cost_usd)}</strong><span>{formatNumber((token.prompt_tokens_total || 0) + (token.completion_tokens_total || 0))} tokens</span></div>
        <div><strong>并发 {token.max_concurrency || '不限'}</strong><span title={`累计额度用量 ${formatMoney(token.cost_used_usd)}`}>{token.cost_limit_usd ? `${formatMoney(token.cost_used_usd)} / ${formatMoney(token.cost_limit_usd)}` : '费用不限'}</span></div>
        <div><strong>{formatDate(createdTimestamp(token))}</strong></div>
        <div><strong>{token.last_used_at ? formatDate(token.last_used_at) : '未使用'}</strong>{token.expires_at != null && token.expires_at > 0 && <span>到期 {formatDate(token.expires_at)}</span>}</div>
        <div className="row-actions">
          <a className="icon-button icon-button--surface" href={`#/connect?token=${token.id}`} aria-label={`配置客户端 ${token.description}`} title="配置客户端"><PlugZap size={16} /></a>
          <button className={`icon-button icon-button--surface${token.is_active ? ' is-on' : ''}`} type="button" onClick={() => void toggle(token)} disabled={busyId === token.id} aria-label={token.is_active ? `停用 ${token.description}` : `启用 ${token.description}`} title={token.is_active ? '停用' : '启用'}>{token.is_active ? <Power size={16} /> : <Play size={16} />}</button>
          <button className="icon-button icon-button--surface" type="button" onClick={() => setEditing(token)} aria-label={`编辑 ${token.description}`} title="编辑"><Pencil size={16} /></button>
          <button className="icon-button icon-button--surface danger-button" type="button" onClick={() => void remove(token)} disabled={busyId === token.id} aria-label={`删除 ${token.description}`} title="删除"><Trash2 size={16} /></button>
        </div>
      </article>)}
    </div></div>}
    <Pagination label="令牌分页" page={page} pageSize={pageSize} total={visible.length} onPage={setPage} pageSizes={[50, 100]} onPageSize={(size) => { setPage(1); setPageSize(size) }} />

    {editing && <TokenForm token={editing === 'new' ? undefined : editing} close={() => setEditing(null)} saved={(token, plain) => {
      setEditing(null); if (plain) setCreatedToken({ value: plain, id: token.id })
      const metadata = tokenMetadata(token)
      setTokens((items) => editing === 'new' ? [metadata, ...items] : items.map((item) => item.id === token.id ? { ...item, ...metadata } : item))
    }} />}
    {createdToken && <Modal title="令牌已创建" close={() => setCreatedToken(null)}><div className="secret-reveal"><p>请立即保存完整令牌。关闭后，列表将不再显示密钥内容。</p><code>{createdToken.value}</code><button className="primary-button" type="button" onClick={() => void copyValue(createdToken.value, 'created').catch((reason: unknown) => setError(reason instanceof Error ? reason.message : '复制失败'))}>{copiedId === 'created' ? <Check size={15} /> : <Copy size={15} />}{copiedId === 'created' ? '已复制' : '复制令牌'}</button><a className="secondary-button" href={`#/connect?token=${createdToken.id}`}>生成客户端配置<ArrowRight size={14} /></a></div></Modal>}
    {details && <ResourceDetails title="令牌详情" close={() => setDetails(null)} facts={[
      { label: '名称', value: details.description },
      { label: '令牌 ID', value: `#${details.id}` },
      { label: '令牌提示', value: <code>{details.token_hint || '未提供提示'}</code> },
      { label: '状态', value: details.is_active ? '已启用' : '已停用' },
      { label: '允许的模型', value: <DetailList empty="不限制模型" values={(details.allowed_models ?? []).map((model) => <code>{model}</code>)} /> },
      { label: '允许的协议', value: <DetailList empty="不限制协议" values={details.allowed_protocols ?? []} /> },
      { label: '渠道限制模式', value: details.channel_restriction_mode === 'deny' ? '排除列出的渠道' : '只允许列出的渠道' },
      { label: '渠道范围', value: <DetailList empty="未选择渠道，不限制范围" values={(details.allowed_channel_ids ?? []).map((id) => `渠道 #${id}`)} /> },
      { label: '最大并发', value: details.max_concurrency || '不限' },
      { label: '费用软限额', value: details.cost_limit_usd ? `${details.cost_limit_usd.toFixed(2)} USD` : '未设置' },
      { label: `${rangeName}日志费用`, value: `${formatMoney(details.effective_cost_usd)} · 尚未与上游账单对账` },
      { label: '限额使用金额', value: `${formatMoney(details.cost_used_usd)} · 本地费用回执口径` },
      { label: '请求统计', value: `${formatNumber(details.success_count + details.failure_count)} 次 · 成功 ${formatNumber(details.success_count)} · 失败 ${formatNumber(details.failure_count)}` },
      { label: 'Token 统计', value: `输入 ${formatNumber(details.prompt_tokens_total)} · 输出 ${formatNumber(details.completion_tokens_total)}` },
      { label: '创建时间', value: detailDate(details.created_at) },
      { label: '最后使用', value: details.last_used_at ? detailDate(details.last_used_at) : '未使用' },
      { label: '到期时间', value: details.expires_at ? detailDate(details.expires_at) : '未设置到期时间' },
    ]} />}
  </div>
}

export function TokenForm({ token, close, saved }: { token?: AuthToken; close: () => void; saved: (token: AuthToken, plain?: string) => void }) {
  const [form, setForm] = useState<TokenFormValue>(() => token ? {
    description: token.description,
    expires: token.expires_at ? toLocalInput(token.expires_at) : '',
    allowedModels: token.allowed_models || [],
    allowedChannels: token.allowed_channel_ids || [],
    restrictionMode: token.channel_restriction_mode || 'allow',
    costLimit: token.cost_limit_usd || 0,
    maxConcurrency: token.max_concurrency || 0,
    active: token.is_active,
  } : emptyForm)
  const [channels, setChannels] = useState<Channel[]>([])
  const [modelSearch, setModelSearch] = useState('')
  const [modelQuery, setModelQuery] = useState('')
  const [channelSearch, setChannelSearch] = useState('')
  const [modelPage, setModelPage] = useState(1)
  const [channelPage, setChannelPage] = useState(1)
  const [coverage, setCoverage] = useState({ channelLoaded: 0, channelTotal: 0 })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    const query = modelSearch.trim()
    if (query === modelQuery) return
    const timer = window.setTimeout(() => { setModelQuery(query); setModelPage(1) }, 250)
    return () => window.clearTimeout(timer)
  }, [modelSearch, modelQuery])
  const readModels = useCallback((signal: AbortSignal) => getModelRouteInventory({ search: modelQuery, limit: 50, offset: (modelPage - 1) * 50, enabled_only: true }, signal), [modelPage, modelQuery])
  const modelInventory = useReadSnapshot(`token-models:${modelQuery}:${modelPage}`, readModels)
  const models = modelInventory.data?.items.map((item) => item.model) || []

  useEffect(() => {
    const controller = new AbortController()
    void getChannels({ limit: 1000, offset: 0 }, controller.signal)
      .then((channelResult) => {
        if (controller.signal.aborted) return
        setChannels(channelResult.data)
        setCoverage({ channelLoaded: channelResult.data.length, channelTotal: channelResult.count })
      })
      .catch((reason) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : '模型清单加载失败') })
    return () => controller.abort()
  }, [])

  const visibleChannels = channels.filter((channel) => !channelSearch.trim() || channel.name.toLowerCase().includes(channelSearch.trim().toLowerCase()) || String(channel.id).includes(channelSearch.trim()))
  const channelWindow = listWindow(visibleChannels.length, channelPage, 50)
  const selectedModels = new Set(form.allowedModels)
  const selectedChannels = new Set(form.allowedChannels)
  const toggleModel = (model: string) => setForm((current) => ({ ...current, allowedModels: current.allowedModels.includes(model) ? current.allowedModels.filter((item) => item !== model) : [...current.allowedModels, model] }))
  const toggleChannel = (channelId: number) => setForm((current) => ({ ...current, allowedChannels: current.allowedChannels.includes(channelId) ? current.allowedChannels.filter((item) => item !== channelId) : [...current.allowedChannels, channelId] }))

  const submit = async (event: React.FormEvent) => {
    event.preventDefault(); setSaving(true); setError('')
    const payload = {
      description: form.description.trim(),
      expires_at: form.expires ? new Date(form.expires).getTime() : null,
      is_active: form.active,
      allowed_models: form.allowedModels,
      allowed_channel_ids: form.allowedChannels,
      channel_restriction_mode: form.restrictionMode,
      cost_limit_usd: Number(form.costLimit) || 0,
      max_concurrency: Number(form.maxConcurrency) || 0,
    }
    try {
      const result = token ? await updateAuthToken(token.id, payload) : await createAuthToken(payload)
      saved({ ...token, ...result } as AuthToken, token ? undefined : result.token)
    } catch (reason) { setError(reason instanceof Error ? reason.message : '保存失败') }
    finally { setSaving(false) }
  }

  return <Modal title={token ? '编辑令牌' : '创建令牌'} close={close} wide>
    <form className="console-form" onSubmit={submit}>
      {error && <div className="modal-error inline-error">{error}</div>}
      <div className="form-grid">
        <label>名称<input required value={form.description} onChange={(event) => setForm({ ...form, description: event.target.value })} placeholder="例如：家中 Codex" /></label>
        <label>到期时间<input type="datetime-local" value={form.expires} onChange={(event) => setForm({ ...form, expires: event.target.value })} /></label>
        <label>费用限额（USD）<input type="number" min="0" step="0.01" value={form.costLimit} onChange={(event) => setForm({ ...form, costLimit: Number(event.target.value) })} /></label>
        <label>最大并发<input type="number" min="0" value={form.maxConcurrency} onChange={(event) => setForm({ ...form, maxConcurrency: Number(event.target.value) })} /></label>
        <label>渠道限制模式<select value={form.restrictionMode} onChange={(event) => setForm({ ...form, restrictionMode: event.target.value as 'allow' | 'deny' })}><option value="allow">只允许选中渠道</option><option value="deny">排除选中渠道</option></select></label>
      </div>
      <section className="selection-panel"><header><div><strong>允许的模型</strong><span>{form.allowedModels.length ? `已选 ${form.allowedModels.length} 个` : '未选择表示不限制'}</span></div><div className="selection-actions"><button type="button" className="text-button" disabled={!models.length || modelInventory.loading} onClick={() => setForm((current) => ({ ...current, allowedModels: [...new Set([...current.allowedModels, ...models])] }))}>全选本页模型</button><button type="button" className="text-button" onClick={() => setForm((current) => ({ ...current, allowedModels: [] }))}>清空</button></div></header>
        <p className="selection-coverage">按统一名称展示当前启用渠道的模型；可搜索全部模型与别名。原有权限选择保持，全选仅添加本页。</p>
        <label className="search-field selection-search"><Search size={14} /><input value={modelSearch} onChange={(event) => setModelSearch(event.target.value)} placeholder="搜索模型或别名" aria-label="搜索可选模型" /></label>
        {modelInventory.loading && !modelInventory.data ? <LoadingState label="正在读取统一模型清单" /> : modelInventory.error ? <ErrorState message={modelInventory.error} retry={modelInventory.refresh} /> : <div className="selection-list">{models.length ? models.map((model) => <label className={`selection-option${selectedModels.has(model) ? ' is-selected' : ''}`} key={model}><input type="checkbox" checked={selectedModels.has(model)} onChange={() => toggleModel(model)} /><code>{model}</code></label>) : <span className="selection-empty">没有匹配的启用模型。已选择的模型仍会保留。</span>}</div>}
        {modelInventory.data && <Pagination label="模型选择分页" page={modelPage} pageSize={50} total={modelInventory.data.total} onPage={setModelPage} />}
        {form.allowedModels.some((name) => !models.includes(name)) && <details className="selection-retained"><summary>本页之外的原有选择 · {form.allowedModels.filter((name) => !models.includes(name)).length} 项</summary><div className="selection-list">{form.allowedModels.filter((name) => !models.includes(name)).slice(0, 50).map((name) => <label className="selection-option is-selected" key={name}><input type="checkbox" checked onChange={() => toggleModel(name)} /><code>{name}</code></label>)}</div><p className="selection-coverage">预览最多 50 项，取消后可继续查看余下选择；旧别名权限不会自动改写。</p></details>}
      </section>
      <section className="selection-panel"><header><div><strong>渠道范围</strong><span>{form.allowedChannels.length ? `已选 ${form.allowedChannels.length} 个` : '未选择表示不限制'}</span></div><div className="selection-actions"><button type="button" className="text-button" onClick={() => setForm((current) => ({ ...current, allowedChannels: [...new Set([...current.allowedChannels, ...channels.map((channel) => channel.id)])] }))}>全选已加载渠道</button><button type="button" className="text-button" onClick={() => setForm((current) => ({ ...current, allowedChannels: [] }))}>清空</button></div></header>
        {coverage.channelLoaded < coverage.channelTotal && <p className="selection-coverage">渠道清单已加载 {coverage.channelLoaded} / {coverage.channelTotal} 条；全选只添加已加载渠道，原有选择保持。</p>}
        <label className="search-field selection-search"><Search size={14} /><input value={channelSearch} onChange={(event) => { setChannelSearch(event.target.value); setChannelPage(1) }} placeholder="搜索渠道" aria-label="搜索可选渠道" /></label><div className="selection-list">{visibleChannels.length ? visibleChannels.slice(channelWindow.start, channelWindow.end).map((channel) => <label className={`selection-option${selectedChannels.has(channel.id) ? ' is-selected' : ''}`} key={channel.id}><input type="checkbox" checked={selectedChannels.has(channel.id)} onChange={() => toggleChannel(channel.id)} /><span>{channel.name}<small>#{channel.id} · {channel.models.filter((model) => !model.disabled).length} 个模型</small></span></label>) : <span className="selection-empty">{channels.length ? '没有匹配的渠道。已选择的渠道仍会保留。' : '暂无渠道。'}</span>}</div>
        <Pagination label="渠道选择分页" page={channelPage} pageSize={50} total={visibleChannels.length} onPage={setChannelPage} />
      </section>
      <label className="checkbox-field"><input type="checkbox" checked={form.active} onChange={(event) => setForm({ ...form, active: event.target.checked })} />立即启用</label>
      <footer><button className="secondary-button" type="button" onClick={close}>取消</button><button className="primary-button" type="submit" disabled={saving}>{saving ? <RefreshCw className="spin" size={15} /> : null}{saving ? '保存中' : '保存'}</button></footer>
    </form>
  </Modal>
}

function formatDate(value: number): string { return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value)) }
function createdTimestamp(token: AuthToken): number {
  const value = Date.parse(token.created_at)
  return Number.isFinite(value) ? value : token.id
}
function toLocalInput(value: number): string {
  const date = new Date(value - new Date(value).getTimezoneOffset() * 60_000)
  return date.toISOString().slice(0, 16)
}
