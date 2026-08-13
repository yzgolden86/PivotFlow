import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { FileUp, FlaskConical, MoreHorizontal, Pencil, Plus, Power, RefreshCw, Search, Trash2 } from 'lucide-react'
import { createChannel, deleteChannel, getChannelEditor, getChannels, getSiteChannelBindings, getSiteInventory, getSiteModels, importOAuthCredentials, runAccountTask, setChannelsEnabled, updateChannel } from '../api'
import type { Channel, ChannelEditorSnapshot, ChannelModel, ChannelMutation, ChannelURL, Site, SiteAccount, SiteChannelBinding } from '../types'
import { EmptyState, ErrorState, LoadingState, Pagination } from './shared'
import { useLocation } from 'react-router-dom'
import { Modal, siteErrorMessage, StatusBadge } from './siteShared'

const PAGE_SIZE = 12

export default function ChannelsPage() {
  const location = useLocation()
  const querySearch = useMemo(() => new URLSearchParams(location.search).get('search') || '', [location.search])
  const [channels, setChannels] = useState<Channel[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [searchDraft, setSearchDraft] = useState(querySearch)
  const [search, setSearch] = useState(querySearch)
  const [status, setStatus] = useState('all')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busyId, setBusyId] = useState<number | null>(null)
  const [editing, setEditing] = useState<number | 'new' | null>(null)
  const [syncOpen, setSyncOpen] = useState(false)
  const importInput = useRef<HTMLInputElement>(null)

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true)
    setError('')
    try {
      const result = await getChannels({ search, status, limit: PAGE_SIZE, offset: (page - 1) * PAGE_SIZE }, signal)
      setChannels(result.data)
      setTotal(result.count)
    } catch (reason) {
      if (!signal?.aborted) setError(reason instanceof Error ? reason.message : '渠道加载失败')
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [page, search, status])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  useEffect(() => { setPage(1); setSearchDraft(querySearch); setSearch(querySearch) }, [querySearch])

  const summary = useMemo(() => ({
    enabled: channels.filter((item) => item.enabled).length,
    cooldown: channels.filter(isCooling).length,
    models: channels.reduce((sum, item) => sum + item.models.filter((model) => !model.disabled).length, 0),
  }), [channels])

  const toggleChannel = async (channel: Channel) => {
    setBusyId(channel.id)
    setError('')
    try {
      await setChannelsEnabled([channel.id], !channel.enabled)
      setChannels((items) => items.map((item) => item.id === channel.id ? { ...item, enabled: !item.enabled } : item))
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '渠道状态更新失败')
    } finally {
      setBusyId(null)
    }
  }

  const removeChannel = async (channel: Channel) => {
    if (!window.confirm(`删除渠道“${channel.name}”？该渠道将立即退出路由。`)) return
    setBusyId(channel.id); setError('')
    try { await deleteChannel(channel.id); await load() }
    catch (reason) { setError(reason instanceof Error ? reason.message : '渠道删除失败') }
    finally { setBusyId(null) }
  }

  const importCredentials = async (files: FileList | null) => {
    if (!files?.length) return
    setError(''); setNotice('')
    try {
      const result = await importOAuthCredentials(Array.from(files))
      setNotice(`凭证导入完成：新建 ${result.created || 0}，跳过 ${result.skipped || 0}，失败 ${result.failed || 0}`)
      await load()
    } catch (reason) { setError(reason instanceof Error ? reason.message : '凭证导入失败') }
    finally { if (importInput.current) importInput.current.value = '' }
  }

  return (
    <div className="workspace-page">
      <header className="page-header">
        <h1>渠道与分发</h1>
        <div className="header-controls">
          <input ref={importInput} className="visually-hidden" type="file" accept="application/json,.json" multiple onChange={(event) => void importCredentials(event.target.files)} />
		  <details className="source-menu"><summary className="secondary-button"><MoreHorizontal size={16} />其他来源</summary><div className="source-menu-popover"><button type="button" onClick={() => importInput.current?.click()}><FileUp size={15} />导入 OAuth</button><button type="button" onClick={() => setEditing('new')}><Plus size={15} />手工渠道</button></div></details>
		  <button className="primary-button" type="button" onClick={() => setSyncOpen(true)}><RefreshCw size={16} />同步站点渠道</button>
          <button className="icon-button icon-button--surface" type="button" onClick={() => void load()} aria-label="刷新渠道" title="刷新渠道"><RefreshCw size={17} /></button>
        </div>
      </header>

      {notice && <div className="operation-notice">{notice}</div>}

      <section className="compact-summary" aria-label="当前页渠道摘要">
        <span><strong>{total}</strong>渠道总数</span><span><strong>{summary.enabled}</strong>当前页启用</span><span><strong>{summary.cooldown}</strong>冷却中</span><span><strong>{summary.models}</strong>可用模型映射</span>
      </section>

      <form className="filter-bar" onSubmit={(event) => { event.preventDefault(); setPage(1); setSearch(searchDraft.trim()) }}>
        <label className="search-field"><Search size={16} /><input value={searchDraft} onChange={(event) => setSearchDraft(event.target.value)} placeholder="搜索渠道名称" aria-label="搜索渠道名称" /></label>
        <select value={status} onChange={(event) => { setPage(1); setStatus(event.target.value) }} aria-label="渠道状态">
          <option value="all">全部状态</option><option value="enabled">已启用</option><option value="disabled">已停用</option><option value="cooldown">冷却中</option>
        </select>
        <button className="primary-button" type="submit"><Search size={15} />筛选</button>
      </form>

      {error && channels.length > 0 && <div className="inline-error">{error}</div>}
      {loading ? <LoadingState label="正在加载渠道" /> : error && channels.length === 0 ? <ErrorState message={error} retry={() => void load()} /> : channels.length === 0 ? <EmptyState label="没有符合条件的渠道" /> : (
        <div className="channel-list">
          {channels.map((channel) => <ChannelRow channel={channel} busy={busyId === channel.id} toggle={() => void toggleChannel(channel)} edit={() => setEditing(channel.id)} remove={() => void removeChannel(channel)} key={channel.id} />)}
        </div>
      )}
      <Pagination page={page} pageSize={PAGE_SIZE} total={total} onPage={setPage} />
      {editing && <ChannelEditor channelId={editing === 'new' ? undefined : editing} close={() => setEditing(null)} saved={() => { setEditing(null); void load() }} />}
      {syncOpen && <SiteChannelSyncModal close={() => setSyncOpen(false)} synced={() => void load()} />}
    </div>
  )
}

function SiteChannelSyncModal({ close, synced }: { close: () => void; synced: () => void }) {
  const [sites, setSites] = useState<Site[]>([])
  const [accounts, setAccounts] = useState<SiteAccount[]>([])
  const [bindings, setBindings] = useState<SiteChannelBinding[]>([])
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [results, setResults] = useState<Map<number, { status: string; message?: string }>>(new Map())
  const [loading, setLoading] = useState(true)
  const [syncing, setSyncing] = useState(false)
  const [progress, setProgress] = useState({ done: 0, total: 0 })
  const [error, setError] = useState('')

  const loadData = useCallback(async (signal?: AbortSignal) => {
    setLoading(true); setError('')
    try {
      const [inventory, currentBindings] = await Promise.all([getSiteInventory(signal), getSiteChannelBindings(signal)])
      setSites(inventory.sites); setAccounts(inventory.accounts); setBindings(currentBindings)
    } catch (reason) {
      if (!signal?.aborted) setError(siteErrorMessage(reason))
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [])

  useEffect(() => { const controller = new AbortController(); void loadData(controller.signal); return () => controller.abort() }, [loadData])

  const siteMap = useMemo(() => new Map(sites.map((site) => [site.id, site])), [sites])
  const bindingMap = useMemo(() => {
    const next = new Map<number, SiteChannelBinding[]>()
    for (const binding of bindings) {
      const items = next.get(binding.site_account_id) || []
      items.push(binding)
      next.set(binding.site_account_id, items)
    }
    return next
  }, [bindings])
  const toggle = (id: number) => setSelected((current) => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next })
  const selectAll = () => setSelected(new Set(accounts.filter((account) => account.enabled).map((account) => account.id)))

  const sync = async () => {
    const targets = accounts.filter((account) => selected.has(account.id))
    if (!targets.length) { setError('请至少选择一个站点账号'); return }
    setSyncing(true); setError(''); setResults(new Map()); setProgress({ done: 0, total: targets.length })
    let cursor = 0; let done = 0
    const worker = async () => {
      while (cursor < targets.length) {
        const account = targets[cursor++]
        setResults((current) => new Map(current).set(account.id, { status: 'running' }))
        try {
          const task = await runAccountTask(account.id, 'model_refresh')
          if (!['success', 'partial'].includes(task.status)) throw new Error(task.error || task.status)
          setResults((current) => new Map(current).set(account.id, { status: task.status, message: task.error ? siteErrorMessage(task.error) : undefined }))
        } catch (reason) {
          setResults((current) => new Map(current).set(account.id, { status: 'failed', message: siteErrorMessage(reason) }))
        }
        done++; setProgress({ done, total: targets.length })
      }
    }
    await Promise.all(Array.from({ length: Math.min(3, targets.length) }, worker))
    await loadData(); synced(); setSyncing(false)
  }

  return (
    <Modal title="同步站点渠道" close={close} wide>
      {error && <div className="inline-error modal-error">{error}</div>}
      {loading ? <LoadingState label="正在加载站点账号" /> : (
        <div className="site-sync-dialog">
          <div className="site-sync-toolbar">
            <span>选择账号后，系统会刷新模型，并用模型 API Key 创建或更新对应路由渠道。</span>
            <div><button className="text-button" type="button" onClick={selectAll}>全选可用账号</button><button className="text-button" type="button" onClick={() => setSelected(new Set())}>清空</button></div>
          </div>
          {!accounts.length ? <EmptyState label="还没有站点账号" /> : (
            <div className="site-sync-list">
              {accounts.map((account) => {
                const site = siteMap.get(account.site_id)
                const accountBindings = bindingMap.get(account.id) || []
                const activeBindings = accountBindings.filter((item) => item.status === 'active')
                const binding = activeBindings.find((item) => item.projection_key === 'default') || activeBindings[0] || accountBindings[0]
                const result = results.get(account.id)
                const failedBinding = activeBindings.find((item) => item.last_sync_error)
                const channelCount = activeBindings.filter((item) => item.channel_id).length
                const inactiveCount = accountBindings.length - activeBindings.length
                const bindingMessage = failedBinding?.last_sync_error
                  ? siteErrorMessage(failedBinding.last_sync_error)
                  : channelCount
                    ? `${channelCount} 个渠道 · ${activeBindings.length} 把 Key${inactiveCount ? ` · 已停用 ${inactiveCount} 个旧渠道` : ''}`
                    : '尚未创建渠道'
                const bindingStatus = result?.status || failedBinding?.last_sync_status || (binding ? binding.status : 'unknown')
                return <div className={`site-sync-row${selected.has(account.id) ? ' is-selected' : ''}`} key={account.id}><input type="checkbox" checked={selected.has(account.id)} disabled={!account.enabled || syncing} onChange={() => toggle(account.id)} aria-label={`选择 ${account.label}`} /><div><a className="entity-link" href={`#/accounts?focus_account_id=${account.id}${['expired', 'error'].includes(account.status) ? '&open_credential=1' : ''}`} onClick={close}><strong>{account.label}</strong></a><span><a className="entity-chip" href={`#/sites?focus_site_id=${account.site_id}`} onClick={close}>{site?.name || `站点 #${account.site_id}`}</a> · {account.credential_type === 'api_key' ? '模型 API Key' : '站点登录'}</span></div><div><StatusBadge status={bindingStatus} /><span>{result?.message || bindingMessage}</span></div></div>
              })}
            </div>
          )}
          <footer><span>{syncing ? `正在同步 ${progress.done}/${progress.total}` : `已选择 ${selected.size} 个账号`}</span><div><button className="secondary-button" type="button" onClick={close} disabled={syncing}>关闭</button><button className="primary-button" type="button" onClick={() => void sync()} disabled={syncing || !selected.size}>{syncing && <RefreshCw className="spin" size={15} />}{syncing ? '同步中' : '开始同步'}</button></div></footer>
        </div>
      )}
    </Modal>
  )
}

function ChannelRow({ channel, busy, toggle, edit, remove }: { channel: Channel; busy: boolean; toggle: () => void; edit: () => void; remove: () => void }) {
  const cooling = isCooling(channel)
  const activeModels = channel.models.filter((model) => !model.disabled)
  const protocols = Array.from(new Set(channel.urls.flatMap((url) => url.protocols?.length ? url.protocols : ['auto'])))
  return (
    <article className="channel-row">
      <div className="channel-identity">
        <span className={`status-dot ${channel.enabled ? cooling ? 'status-dot--warning' : 'status-dot--success' : 'status-dot--muted'}`} />
        <div><div className="channel-name"><strong>{channel.name}</strong><small>#{channel.id}</small></div><span>{channel.auth_type === 'api_key' ? `${channel.key_count} Keys · ${channel.key_strategy || 'sequential'}` : channel.auth_type}</span></div>
      </div>
      <div className="channel-endpoints"><strong title={channel.urls.map((item) => item.url).join('\n')}>{channel.urls[0]?.url || '未配置 URL'}</strong><span>{channel.urls.length} URL · {protocols.join(' / ')}</span></div>
      <div className="channel-routing"><span>优先级 <strong>{channel.priority}</strong></span><span>倍率 <strong>{channel.cost_multiplier || 1}x</strong></span><small>{protocolMode(channel.protocol_transform_mode)}</small></div>
      <div className="channel-models"><strong>{activeModels.length} 模型</strong><span title={activeModels.map((item) => item.model).join(', ')}>{activeModels.slice(0, 2).map((item) => item.model).join(' · ') || '未配置'}</span></div>
      <div className="channel-limits"><span>RPM {channel.rpm_limit || '不限'}</span><span>并发 {channel.max_concurrency || '不限'}</span>{cooling && <small className="text-warning">存在冷却</small>}</div>
      <div className="row-actions">
        <a className="icon-button icon-button--surface" href={`#/models?channel=${channel.id}&view=probe`} aria-label={`测试 ${channel.name}`} title="模型测试"><FlaskConical size={16} /></a>
        <button className={`icon-button icon-button--surface ${channel.enabled ? 'is-on' : ''}`} type="button" onClick={toggle} disabled={busy} aria-label={channel.enabled ? `停用 ${channel.name}` : `启用 ${channel.name}`} title={channel.enabled ? '停用渠道' : '启用渠道'}><Power className={busy ? 'spin' : ''} size={16} /></button>
        <button className="icon-button icon-button--surface" type="button" onClick={edit} aria-label={`编辑 ${channel.name}`} title="编辑"><Pencil size={16} /></button>
        <button className="icon-button icon-button--surface danger-button" type="button" onClick={remove} disabled={busy} aria-label={`删除 ${channel.name}`} title="删除"><Trash2 size={16} /></button>
      </div>
    </article>
  )
}

interface EditorForm {
  name: string; authType: string; urls: string; models: ChannelModel[]; keys: string; keyStrategy: string
  priority: number; rpmLimit: number; maxConcurrency: number; costMultiplier: number; dailyCostLimit: number
  protocolMode: string; proxyURL: string; enabled: boolean; websockets: boolean; retryOtherKeys: boolean
}

const blankEditor: EditorForm = {
  name: '', authType: 'api_key', urls: '', models: [], keys: '', keyStrategy: 'sequential', priority: 0,
  rpmLimit: 0, maxConcurrency: 0, costMultiplier: 1, dailyCostLimit: 0, protocolMode: 'auto', proxyURL: '',
  enabled: true, websockets: false, retryOtherKeys: false,
}

function ChannelEditor({ channelId, close, saved }: { channelId?: number; close: () => void; saved: () => void }) {
  const [snapshot, setSnapshot] = useState<ChannelEditorSnapshot | null>(null)
  const [form, setForm] = useState<EditorForm>(blankEditor)
  const [availableModels, setAvailableModels] = useState<string[]>([])
  const [modelSearch, setModelSearch] = useState('')
  const [loading, setLoading] = useState(Boolean(channelId))
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!channelId) return
    const controller = new AbortController()
    void getChannelEditor(channelId, controller.signal).then((data) => {
      setSnapshot(data)
      setForm({
        name: data.channel.name,
        authType: data.channel.auth_type || 'api_key',
        urls: data.channel.urls.map((item) => `${item.url}${item.protocols?.length ? ` | ${item.protocols.join(', ')}` : ''}`).join('\n'),
        models: data.channel.models,
        keys: data.keys.map((item) => `${item.api_key}${item.note ? ` | ${item.note}` : ''}`).join('\n'),
        keyStrategy: data.channel.key_strategy || data.keys[0]?.key_strategy || 'sequential',
        priority: data.channel.priority || 0,
        rpmLimit: data.channel.rpm_limit || 0,
        maxConcurrency: data.channel.max_concurrency || 0,
        costMultiplier: data.channel.cost_multiplier ?? 1,
        dailyCostLimit: data.channel.daily_cost_limit || 0,
        protocolMode: data.channel.protocol_transform_mode || 'auto',
        proxyURL: data.channel.proxy_url || '',
        enabled: data.channel.enabled,
        websockets: Boolean(data.channel.websockets),
        retryOtherKeys: Boolean(data.channel.retry_other_keys_on_failure),
      })
    }).catch((reason) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : '渠道详情加载失败') }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [channelId])

  useEffect(() => {
    const controller = new AbortController()
    void getSiteModels({ limit: 2000 }, controller.signal).then((result) => {
      setAvailableModels((current) => Array.from(new Set([...current, ...result.data.map((item) => item.model)])).sort((a, b) => a.localeCompare(b)))
    }).catch(() => undefined)
    return () => controller.abort()
  }, [])

  useEffect(() => {
    if (snapshot) setAvailableModels((current) => Array.from(new Set([...current, ...snapshot.channel.models.map((item) => item.model)])).sort((a, b) => a.localeCompare(b)))
  }, [snapshot])

  const submit = async (event: React.FormEvent) => {
    event.preventDefault(); setSaving(true); setError('')
    try {
      const payload: ChannelMutation = {
        name: form.name.trim(), auth_type: form.authType,
        urls: parseURLs(form.urls), models: form.models, api_keys: form.authType === 'api_key' ? parseKeys(form.keys) : [],
        key_strategy: form.keyStrategy, priority: Number(form.priority) || 0, rpm_limit: Number(form.rpmLimit) || 0,
        max_concurrency: Number(form.maxConcurrency) || 0, enabled: form.enabled, websockets: form.websockets,
        protocol_transform_mode: form.protocolMode, scheduled_check_enabled: snapshot?.channel.scheduled_check_enabled || false,
        scheduled_check_model: snapshot?.channel.scheduled_check_model || '', daily_cost_limit: Number(form.dailyCostLimit) || 0,
        cost_multiplier: Number(form.costMultiplier), proxy_url: form.proxyURL.trim(), retry_other_keys_on_failure: form.retryOtherKeys,
        custom_request_rules: snapshot?.channel.custom_request_rules, cooldown_detection_rules: snapshot?.channel.cooldown_detection_rules,
      }
      if (!payload.urls.length) throw new Error('至少填写一个上游 URL')
      if (!payload.models.filter((item) => item.model.trim()).length) throw new Error('至少选择一个模型')
      if (form.authType === 'api_key' && !payload.api_keys.length) throw new Error('至少填写一个 API Key')
      if (channelId) await updateChannel(channelId, payload); else await createChannel(payload)
      saved()
    } catch (reason) { setError(reason instanceof Error ? reason.message : '渠道保存失败') }
    finally { setSaving(false) }
  }

  return <Modal title={channelId ? '编辑渠道' : '添加渠道'} close={close} wide>
    {loading ? <LoadingState label="正在加载渠道配置" /> : <form className="console-form channel-editor-form" onSubmit={submit}>
      {error && <div className="modal-error inline-error">{error}</div>}
      <div className="form-grid">
        <label>渠道名称<input required value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /></label>
        <label>认证类型<select value={form.authType} onChange={(event) => setForm({ ...form, authType: event.target.value })} disabled={Boolean(channelId && snapshot?.channel.auth_type !== 'api_key')}><option value="api_key">API Key</option><option value="codex_oauth">Codex OAuth</option><option value="antigravity_oauth">Antigravity OAuth</option></select></label>
        <label>协议转换<select value={form.protocolMode} onChange={(event) => setForm({ ...form, protocolMode: event.target.value })}><option value="auto">自动协商</option><option value="local">本地转换</option><option value="upstream">上游原生</option></select></label>
        <label>Key 分配<select value={form.keyStrategy} onChange={(event) => setForm({ ...form, keyStrategy: event.target.value })} disabled={form.authType !== 'api_key'}><option value="sequential">顺序</option><option value="round_robin">轮询</option></select></label>
        <label>优先级<input type="number" value={form.priority} onChange={(event) => setForm({ ...form, priority: Number(event.target.value) })} /></label>
        <label>成本倍率<input type="number" min="0" step="0.01" value={form.costMultiplier} onChange={(event) => setForm({ ...form, costMultiplier: Number(event.target.value) })} /></label>
        <label>RPM 限制<input type="number" min="0" value={form.rpmLimit} onChange={(event) => setForm({ ...form, rpmLimit: Number(event.target.value) })} /></label>
        <label>最大并发<input type="number" min="0" value={form.maxConcurrency} onChange={(event) => setForm({ ...form, maxConcurrency: Number(event.target.value) })} /></label>
        <label>每日费用限额<input type="number" min="0" step="0.01" value={form.dailyCostLimit} onChange={(event) => setForm({ ...form, dailyCostLimit: Number(event.target.value) })} /></label>
        <label>渠道代理<input value={form.proxyURL} onChange={(event) => setForm({ ...form, proxyURL: event.target.value })} placeholder="留空使用环境代理" /></label>
      </div>
	  <div className="form-help">正常情况请从站点添加账号并点击“同步”，系统会自动读取 URL、API Key 和模型并生成渠道。这里仅用于无法纳入站点管理的特殊上游或多 URL 高级配置。</div>
      <label className="textarea-field">上游 URL<textarea required rows={3} value={form.urls} onChange={(event) => setForm({ ...form, urls: event.target.value })} placeholder="每行一个；可写 URL | anthropic, openai" /></label>
      <ModelMappingEditor models={form.models} availableModels={availableModels} search={modelSearch} setSearch={setModelSearch} setModels={(models) => setForm((current) => ({ ...current, models }))} />
      {form.authType === 'api_key' ? <label className="textarea-field">API Keys<textarea required rows={4} value={form.keys} onChange={(event) => setForm({ ...form, keys: event.target.value })} placeholder="每行一个；可写 key | 备注" /></label> : <div className="form-help">OAuth 渠道不在这里手填密钥，请使用顶部“导入凭证”导入 Codex 或 Antigravity 凭证文件。</div>}
      <div className="checkbox-grid"><label className="checkbox-field"><input type="checkbox" checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} />启用渠道</label><label className="checkbox-field"><input type="checkbox" checked={form.websockets} onChange={(event) => setForm({ ...form, websockets: event.target.checked })} />WebSocket</label><label className="checkbox-field"><input type="checkbox" checked={form.retryOtherKeys} onChange={(event) => setForm({ ...form, retryOtherKeys: event.target.checked })} />失败时尝试其他 Key</label></div>
      <footer><button className="secondary-button" type="button" onClick={close}>取消</button><button className="primary-button" type="submit" disabled={saving}>{saving ? <RefreshCw className="spin" size={15} /> : null}{saving ? '保存中' : '保存渠道'}</button></footer>
    </form>}
  </Modal>
}

function ModelMappingEditor({ models, availableModels, search, setSearch, setModels }: { models: ChannelModel[]; availableModels: string[]; search: string; setSearch: (value: string) => void; setModels: (models: ChannelModel[]) => void }) {
  const visible = availableModels.filter((model) => !search.trim() || model.toLowerCase().includes(search.trim().toLowerCase()))
  const selected = new Set(models.map((item) => item.model))
  const toggle = (model: string) => setModels(selected.has(model) ? models.filter((item) => item.model !== model) : [...models, { model, redirect_model: '' }])
  const updateRedirect = (model: string, redirect_model: string) => setModels(models.map((item) => item.model === model ? { ...item, redirect_model } : item))
  const addCustom = () => { const value = window.prompt('输入自定义模型名称'); if (value?.trim() && !selected.has(value.trim())) setModels([...models, { model: value.trim(), redirect_model: '' }]) }
  return <section className="selection-panel model-mapping-panel"><header><div><strong>模型映射</strong><span>{models.length ? `已选 ${models.length} 个，可设置重定向模型` : '从站点已发现模型中勾选'}</span></div><div className="selection-actions"><button type="button" className="text-button" onClick={() => setModels(availableModels.map((model) => ({ model, redirect_model: '' })))}>全选</button><button type="button" className="text-button" onClick={() => setModels([])}>清空</button><button type="button" className="text-button" onClick={addCustom}>自定义</button></div></header><label className="search-field selection-search"><Search size={14} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索已发现模型" /></label><div className="selection-list model-selection-list">{visible.length ? visible.map((model) => <label className={`selection-option${selected.has(model) ? ' is-selected' : ''}`} key={model}><input type="checkbox" checked={selected.has(model)} onChange={() => toggle(model)} /><code>{model}</code></label>) : <span className="selection-empty">暂无站点模型；可以点击“自定义”添加。</span>}</div>{models.length > 0 && <div className="mapping-list">{models.map((item) => <div className="mapping-row" key={item.model}><code>{item.model}</code><span>→</span><input value={item.redirect_model || ''} onChange={(event) => updateRedirect(item.model, event.target.value)} placeholder="留空使用原模型" aria-label={`${item.model} 重定向模型`} /></div>)}</div>}</section>
}

function parseURLs(value: string): ChannelURL[] { return splitRows(value).map((row) => { const [url, protocolText] = row.split('|').map((item) => item.trim()); return { url, ...(protocolText ? { protocols: protocolText.split(',').map((item) => item.trim()).filter(Boolean) } : {}) } }).filter((item) => item.url) }
function parseKeys(value: string): ChannelMutation['api_keys'] { return splitRows(value).map((row) => { const [api_key, note = ''] = row.split('|').map((item) => item.trim()); return { api_key, note } }).filter((item) => item.api_key) }
function splitRows(value: string): string[] { return value.split('\n').map((item) => item.trim()).filter(Boolean) }

function isCooling(channel: Channel): boolean {
  return Boolean(channel.cooldown_remaining_ms || channel.key_cooldowns?.some((item) => item.cooldown_remaining_ms) || channel.model_cooldowns?.some((item) => item.cooldown_remaining_ms))
}

function protocolMode(mode: string): string {
  if (mode === 'local') return '本地协议转换'
  if (mode === 'upstream') return '上游原生协议'
  return '自动协议协商'
}
