import { useCallback, useEffect, useMemo, useState } from 'react'
import { Check, Copy, KeyRound, Pencil, Play, Plus, Power, RefreshCw, Search, Trash2 } from 'lucide-react'
import { createAuthToken, deleteAuthToken, getAuthTokens, getChannels, getSiteModels, revealAuthToken, updateAuthToken } from '../api'
import type { AuthToken, Channel, DashboardRange } from '../types'
import { EmptyState, ErrorState, formatMoney, formatNumber, LoadingState, OperationNotice } from './shared'
import { Modal } from './siteShared'

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

export default function TokensPage() {
  const [tokens, setTokens] = useState<AuthToken[]>([])
  const [range, setRange] = useState<DashboardRange>('today')
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [editing, setEditing] = useState<AuthToken | 'new' | null>(null)
  const [createdToken, setCreatedToken] = useState('')
  const [copiedId, setCopiedId] = useState<number | 'created' | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true); setError('')
    try { setTokens((await getAuthTokens(range, signal)).tokens || []) }
    catch (reason) { if (!signal?.aborted) setError(reason instanceof Error ? reason.message : '令牌加载失败') }
    finally { if (!signal?.aborted) setLoading(false) }
  }, [range])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  const visible = useMemo(() => {
    const value = query.trim().toLowerCase()
    const filtered = value ? tokens.filter((token) => token.description.toLowerCase().includes(value) || String(token.id).includes(value)) : tokens
    return [...filtered].sort((left, right) => createdTimestamp(right) - createdTimestamp(left))
  }, [query, tokens])

  const totals = useMemo(() => ({
    active: tokens.filter((token) => token.is_active).length,
    requests: tokens.reduce((sum, token) => sum + token.success_count + token.failure_count, 0),
    cost: tokens.reduce((sum, token) => sum + token.effective_cost_usd, 0),
  }), [tokens])

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
        document.body.appendChild(textarea)
        textarea.select()
        if (!document.execCommand('copy')) throw new Error('clipboard unavailable')
        textarea.remove()
      }
    } catch {
      throw new Error('复制失败，请检查浏览器剪贴板权限')
    }
    if (id === undefined) return
    setCopiedId(id)
    window.setTimeout(() => setCopiedId((current) => current === id ? null : current), 1800)
  }

  return <div className="workspace-page tokens-page">
    <header className="page-header">
      <h1>令牌管理</h1>
      <div className="header-controls">
        <button className="primary-button" type="button" onClick={() => setEditing('new')}><Plus size={16} />创建令牌</button>
        <button className="icon-button icon-button--surface" type="button" onClick={() => void load()} aria-label="刷新令牌"><RefreshCw size={17} /></button>
      </div>
    </header>

    <section className="compact-summary" aria-label="令牌摘要">
      <span><strong>{tokens.length}</strong>令牌总数</span><span><strong>{totals.active}</strong>已启用</span>
      <span><strong>{formatNumber(totals.requests)}</strong>请求</span><span><strong>{formatMoney(totals.cost)}</strong>消耗</span>
    </section>

    <div className="filter-bar">
      <label className="search-field"><Search size={16} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索名称或 ID" /></label>
      <div className="range-control range-control--compact" role="radiogroup" aria-label="令牌统计范围">
        {([['today', '今日'], ['this_week', '本周'], ['this_month', '本月']] as const).map(([value, label]) => <button className={range === value ? 'is-active' : ''} type="button" role="radio" aria-checked={range === value} onClick={() => setRange(value)} key={value}>{label}</button>)}
      </div>
      <span className="filter-count">{visible.length} 条</span>
    </div>

    {error && tokens.length > 0 && <OperationNotice tone="error">{error}</OperationNotice>}
    {loading ? <LoadingState label="正在加载令牌" /> : error && !tokens.length ? <ErrorState message={error} retry={() => void load()} /> : !visible.length ? <EmptyState label="没有符合条件的令牌" /> : <div className="token-list">
      {visible.map((token) => <article className="token-row" key={token.id}>
        <span className={`token-icon${token.is_active ? '' : ' token-icon--off'}`}><KeyRound size={17} /></span>
        <div className="token-identity"><strong>{token.description}</strong><span>令牌 #{token.id}</span></div>
        <div className="token-secret-cell"><code>{token.token_hint || token.token || '****'}</code><button className="icon-button icon-button--surface" type="button" onClick={() => void reveal(token)} disabled={busyId === token.id || !token.token_recoverable} aria-label={`复制 ${token.description}`} title={token.token_recoverable ? '复制令牌' : '历史令牌不可恢复'}>{copiedId === token.id ? <Check size={15} /> : <Copy size={15} />}</button></div>
        <div><strong>{formatNumber(token.success_count + token.failure_count)}</strong><span>{token.failure_count} 次失败</span></div>
        <div><strong>{formatMoney(token.effective_cost_usd)}</strong><span>{formatNumber((token.prompt_tokens_total || 0) + (token.completion_tokens_total || 0))} tokens</span></div>
        <div><strong>{token.max_concurrency || '不限'}</strong><span>{token.cost_limit_usd ? `限额 ${formatMoney(token.cost_limit_usd)}` : '无费用限额'}</span></div>
        <div><strong>{formatDate(createdTimestamp(token))}</strong><span>创建时间</span></div>
        <div><strong>{token.last_used_at ? formatDate(token.last_used_at) : '未使用'}</strong><span>{token.expires_at ? `到期 ${formatDate(token.expires_at)}` : '最后使用时间'}</span></div>
        <div className="row-actions">
          <button className={`icon-button icon-button--surface${token.is_active ? ' is-on' : ''}`} type="button" onClick={() => void toggle(token)} disabled={busyId === token.id} aria-label={token.is_active ? `停用 ${token.description}` : `启用 ${token.description}`} title={token.is_active ? '停用' : '启用'}>{token.is_active ? <Power size={16} /> : <Play size={16} />}</button>
          <button className="icon-button icon-button--surface" type="button" onClick={() => setEditing(token)} aria-label={`编辑 ${token.description}`} title="编辑"><Pencil size={16} /></button>
          <button className="icon-button icon-button--surface danger-button" type="button" onClick={() => void remove(token)} disabled={busyId === token.id} aria-label={`删除 ${token.description}`} title="删除"><Trash2 size={16} /></button>
        </div>
      </article>)}
    </div>}

    {editing && <TokenForm token={editing === 'new' ? undefined : editing} close={() => setEditing(null)} saved={(token, plain) => {
      setEditing(null); if (plain) setCreatedToken(plain)
      setTokens((items) => editing === 'new' ? [token, ...items] : items.map((item) => item.id === token.id ? { ...item, ...token } : item))
    }} />}
    {createdToken && <Modal title="令牌已创建" close={() => setCreatedToken('')}><div className="secret-reveal"><p>请立即保存完整令牌。关闭后，列表将不再显示密钥内容。</p><code>{createdToken}</code><button className="primary-button" type="button" onClick={() => void copyValue(createdToken, 'created')}>{copiedId === 'created' ? <Check size={15} /> : <Copy size={15} />}{copiedId === 'created' ? '已复制' : '复制令牌'}</button></div></Modal>}
  </div>
}

function TokenForm({ token, close, saved }: { token?: AuthToken; close: () => void; saved: (token: AuthToken, plain?: string) => void }) {
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
  const [models, setModels] = useState<string[]>([])
  const [channels, setChannels] = useState<Channel[]>([])
  const [modelSearch, setModelSearch] = useState('')
  const [channelSearch, setChannelSearch] = useState('')
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    void Promise.all([getSiteModels({ limit: 2000 }, controller.signal), getChannels({ limit: 1000, offset: 0 }, controller.signal)])
      .then(([modelResult, channelResult]) => {
        const discovered = modelResult.data.map((item) => item.model)
        const channelModels = channelResult.data.flatMap((item) => item.models.filter((model) => !model.disabled).map((model) => model.model))
        setModels(Array.from(new Set([...discovered, ...channelModels, ...form.allowedModels])).sort((a, b) => a.localeCompare(b)))
        setChannels(channelResult.data)
      })
      .catch((reason) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : '模型清单加载失败') })
    return () => controller.abort()
  }, [])

  const visibleModels = models.filter((model) => !modelSearch.trim() || model.toLowerCase().includes(modelSearch.trim().toLowerCase()))
  const visibleChannels = channels.filter((channel) => !channelSearch.trim() || channel.name.toLowerCase().includes(channelSearch.trim().toLowerCase()) || String(channel.id).includes(channelSearch.trim()))
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
      <section className="selection-panel"><header><div><strong>允许的模型</strong><span>{form.allowedModels.length ? `已选 ${form.allowedModels.length} 个` : '未选择表示不限制'}</span></div><div className="selection-actions"><button type="button" className="text-button" onClick={() => setForm((current) => ({ ...current, allowedModels: models }))}>全选</button><button type="button" className="text-button" onClick={() => setForm((current) => ({ ...current, allowedModels: [] }))}>清空</button></div></header><label className="search-field selection-search"><Search size={14} /><input value={modelSearch} onChange={(event) => setModelSearch(event.target.value)} placeholder="搜索模型" /></label><div className="selection-list">{visibleModels.length ? visibleModels.map((model) => <label className={`selection-option${form.allowedModels.includes(model) ? ' is-selected' : ''}`} key={model}><input type="checkbox" checked={form.allowedModels.includes(model)} onChange={() => toggleModel(model)} /><code>{model}</code></label>) : <span className="selection-empty">暂无已发现模型，先刷新站点账号模型。</span>}</div></section>
      <section className="selection-panel"><header><div><strong>渠道范围</strong><span>{form.allowedChannels.length ? `已选 ${form.allowedChannels.length} 个` : '未选择表示不限制'}</span></div><div className="selection-actions"><button type="button" className="text-button" onClick={() => setForm((current) => ({ ...current, allowedChannels: channels.map((channel) => channel.id) }))}>全选</button><button type="button" className="text-button" onClick={() => setForm((current) => ({ ...current, allowedChannels: [] }))}>清空</button></div></header><label className="search-field selection-search"><Search size={14} /><input value={channelSearch} onChange={(event) => setChannelSearch(event.target.value)} placeholder="搜索渠道" /></label><div className="selection-list">{visibleChannels.length ? visibleChannels.map((channel) => <label className={`selection-option${form.allowedChannels.includes(channel.id) ? ' is-selected' : ''}`} key={channel.id}><input type="checkbox" checked={form.allowedChannels.includes(channel.id)} onChange={() => toggleChannel(channel.id)} /><span>{channel.name}<small>#{channel.id} · {channel.models.filter((model) => !model.disabled).length} 个模型</small></span></label>) : <span className="selection-empty">暂无渠道。</span>}</div></section>
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
