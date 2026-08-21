import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Activity, Copy, FileUp, FlaskConical, Layers3, Pencil, Plus, Power, RefreshCw, Search, Sparkles, Trash2 } from 'lucide-react'
import { createChannel, deleteChannel, deleteChannels, fetchChannelModelsPreview, getAuthTokens, getChannelEditor, getChannelRouteDiagnostics, getChannels, getSiteChannelBindings, getSiteInventory, importOAuthCredentials, peekChannels, runAccountTask, setChannelsEnabled, updateChannel } from '../api'
import type { AuthToken, Channel, ChannelEditorSnapshot, ChannelModel, ChannelMutation, ChannelRouteDiagnostic, ChannelURL, RouteDiagnosticResponse, Site, SiteAccount, SiteChannelBinding } from '../types'
import { EmptyState, ErrorState, LoadingState, OperationNotice, Pagination } from './shared'
import { useLocation } from 'react-router-dom'
import { Modal, siteErrorMessage, StatusBadge } from './siteShared'

export default function ChannelsPage() {
  const location = useLocation()
  const query = useMemo(() => new URLSearchParams(location.search), [location.search])
  const querySearch = query.get('search') || ''
  const focusChannelID = Number(query.get('focus_channel_id') || 0)
  const initialResult = peekChannels({ search: querySearch, status: 'all', sort: 'priority', limit: 50, offset: 0 })
  const [channels, setChannels] = useState<Channel[]>(() => initialResult?.data || [])
  const [total, setTotal] = useState(() => initialResult?.count || 0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const [searchDraft, setSearchDraft] = useState(querySearch)
  const [search, setSearch] = useState(querySearch)
  const [status, setStatus] = useState('all')
  const [sort, setSort] = useState('priority')
  const [loading, setLoading] = useState(!initialResult)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busyId, setBusyId] = useState<number | null>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState(false)
  const [editing, setEditing] = useState<number | 'new' | null>(null)
  const [sourceMenuOpen, setSourceMenuOpen] = useState(false)
  const [syncOpen, setSyncOpen] = useState(false)
  const [diagnosing, setDiagnosing] = useState<Channel | null>(null)
  const importInput = useRef<HTMLInputElement>(null)
  const focusedChannelRef = useRef(0)

  const load = useCallback(async (signal?: AbortSignal, options: { silent?: boolean; force?: boolean } = {}) => {
    const filters = { search, status, sort, limit: pageSize, offset: (page - 1) * pageSize }
    const cached = peekChannels(filters)
    if (cached) {
      setChannels(cached.data)
      setTotal(cached.count)
    }
    if (!options.silent) setLoading(!cached)
    setError('')
    try {
      const result = await getChannels(filters, signal, { force: options.force })
      setChannels(result.data)
      setTotal(result.count)
      setSelected((current) => new Set([...current].filter((id) => result.data.some((channel) => channel.id === id))))
    } catch (reason) {
      if (!signal?.aborted) setError(reason instanceof Error ? reason.message : '渠道加载失败')
    } finally {
      if (!signal?.aborted && !options.silent) setLoading(false)
    }
  }, [page, pageSize, search, sort, status])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  useEffect(() => { setPage(1); setSearchDraft(querySearch); setSearch(querySearch) }, [querySearch])
  useEffect(() => {
    if (!focusChannelID || loading || focusedChannelRef.current === focusChannelID) return
    focusedChannelRef.current = focusChannelID
    setEditing(focusChannelID)
  }, [channels, focusChannelID, loading])
  useEffect(() => {
    if (editing) setSourceMenuOpen(false)
  }, [editing])

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
    try { await deleteChannel(channel.id); await load(undefined, { silent: true, force: true }) }
    catch (reason) { setError(reason instanceof Error ? reason.message : '渠道删除失败') }
    finally { setBusyId(null) }
  }

  const toggleSelected = (id: number) => setSelected((current) => {
    const next = new Set(current)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })
  const allPageSelected = channels.length > 0 && channels.every((channel) => selected.has(channel.id))
  const toggleAllPage = () => setSelected(allPageSelected ? new Set() : new Set(channels.map((channel) => channel.id)))

  const runBatch = async (action: 'enable' | 'disable' | 'delete') => {
    const ids = [...selected]
    if (!ids.length) return
    if (action === 'delete' && !window.confirm(`删除选中的 ${ids.length} 个渠道？这些渠道将立即退出路由。`)) return
    setBatchBusy(true); setError(''); setNotice('')
    try {
      if (action === 'delete') await deleteChannels(ids)
      else await setChannelsEnabled(ids, action === 'enable')
      setNotice(action === 'delete' ? `已删除 ${ids.length} 个渠道` : `已${action === 'enable' ? '启用' : '禁用'} ${ids.length} 个渠道`)
      setSelected(new Set())
      await load(undefined, { silent: true, force: true })
    } catch (reason) { setError(reason instanceof Error ? reason.message : '批量操作失败') }
    finally { setBatchBusy(false) }
  }

  const copyChannel = async (channel: Channel) => {
    setBusyId(channel.id); setError(''); setNotice('')
    try {
      const snapshot = await getChannelEditor(channel.id)
      const payload = snapshotToMutation(snapshot, `${channel.name} 副本`)
      await createChannel(payload)
      setNotice(`已复制渠道“${channel.name}”`)
      await load(undefined, { silent: true, force: true })
    } catch (reason) { setError(reason instanceof Error ? reason.message : '渠道复制失败') }
    finally { setBusyId(null) }
  }

  const importCredentials = async (files: FileList | null) => {
    if (!files?.length) return
    setError(''); setNotice('')
    try {
      const result = await importOAuthCredentials(Array.from(files))
      setNotice(`凭证导入完成：新建 ${result.created || 0}，跳过 ${result.skipped || 0}，失败 ${result.failed || 0}`)
      await load(undefined, { silent: true, force: true })
    } catch (reason) { setError(reason instanceof Error ? reason.message : '凭证导入失败') }
    finally { if (importInput.current) importInput.current.value = '' }
  }

  return (
    <div className="workspace-page">
      <header className="page-header">
        <h1>渠道分发</h1>
        <div className="header-controls">
          <input ref={importInput} className="visually-hidden" type="file" accept="application/json,.json" multiple onChange={(event) => void importCredentials(event.target.files)} />
		  <div className="source-menu"><button className="secondary-button" type="button" aria-haspopup="menu" aria-expanded={sourceMenuOpen} onClick={() => setSourceMenuOpen((open) => !open)}><Layers3 size={16} />其他来源</button>{sourceMenuOpen && <div className="source-menu-popover" role="menu"><button type="button" role="menuitem" onClick={() => { setSourceMenuOpen(false); importInput.current?.click() }}><FileUp size={15} />导入 OAuth</button><button type="button" role="menuitem" onClick={() => { setSourceMenuOpen(false); setEditing('new') }}><Plus size={15} />手工渠道</button></div>}</div>
		  <button className="primary-button" type="button" onClick={() => setSyncOpen(true)}><RefreshCw size={16} />同步站点渠道</button>
          <button className="icon-button icon-button--surface" type="button" onClick={() => void load(undefined, { silent: true, force: true })} aria-label="刷新渠道" title="刷新渠道"><RefreshCw size={17} /></button>
        </div>
      </header>

      {notice && <OperationNotice onDismiss={() => setNotice('')}>{notice}</OperationNotice>}

      <section className="compact-summary" aria-label="当前页渠道摘要">
        <span><strong>{total}</strong>渠道总数</span><span><strong>{summary.enabled}</strong>当前页启用</span><span><strong>{summary.cooldown}</strong>冷却中</span><span><strong>{summary.models}</strong>可用模型映射</span>
      </section>

      <form className="filter-bar" onSubmit={(event) => { event.preventDefault(); setPage(1); setSearch(searchDraft.trim()) }}>
        <label className="selection-toggle"><input type="checkbox" checked={allPageSelected} onChange={toggleAllPage} aria-label="选择当前页全部渠道" /><span>全选</span></label>
        <label className="search-field"><Search size={16} /><input value={searchDraft} onChange={(event) => setSearchDraft(event.target.value)} placeholder="搜索渠道名称" aria-label="搜索渠道名称" /></label>
        <select value={status} onChange={(event) => { setPage(1); setStatus(event.target.value) }} aria-label="渠道状态">
          <option value="all">全部状态</option><option value="enabled">已启用</option><option value="disabled">已停用</option><option value="cooldown">冷却中</option>
        </select>
        <select value={sort} onChange={(event) => { setPage(1); setSort(event.target.value) }} aria-label="渠道排序">
          <option value="priority">优先级</option><option value="newest">新建优先</option><option value="name">名称 A-Z</option><option value="enabled">启用优先</option><option value="models">模型数量</option>
        </select>
        <button className="primary-button" type="submit"><Search size={15} />筛选</button>
      </form>

      {selected.size > 0 && <div className="batch-toolbar" aria-label="渠道批量操作"><strong>已选择 {selected.size} 项</strong><div><button type="button" onClick={() => void runBatch('enable')} disabled={batchBusy}><Power size={14} />启用</button><button type="button" onClick={() => void runBatch('disable')} disabled={batchBusy}><Power size={14} />禁用</button><button className="danger-button" type="button" onClick={() => void runBatch('delete')} disabled={batchBusy}><Trash2 size={14} />删除</button></div></div>}

      {error && channels.length > 0 && <OperationNotice tone="error">{error}</OperationNotice>}
      {loading ? <LoadingState label="正在加载渠道" /> : error && channels.length === 0 ? <ErrorState message={error} retry={() => void load()} /> : channels.length === 0 ? <EmptyState label="没有符合条件的渠道" /> : (
        <div className="channel-list">
          {channels.map((channel) => <ChannelRow channel={channel} selected={selected.has(channel.id)} busy={busyId === channel.id || (batchBusy && selected.has(channel.id))} select={() => toggleSelected(channel.id)} toggle={() => void toggleChannel(channel)} copy={() => void copyChannel(channel)} diagnose={() => setDiagnosing(channel)} edit={() => setEditing(channel.id)} remove={() => void removeChannel(channel)} key={channel.id} />)}
        </div>
      )}
      <Pagination page={page} pageSize={pageSize} total={total} onPage={setPage} pageSizes={[50, 100]} onPageSize={(size) => { setPage(1); setPageSize(size) }} />
      {editing && <ChannelEditor channelId={editing === 'new' ? undefined : editing} close={() => setEditing(null)} saved={() => { setEditing(null); void load(undefined, { silent: true, force: true }) }} />}
      {diagnosing && <RouteDiagnosticModal channel={diagnosing} close={() => setDiagnosing(null)} />}
      {syncOpen && <SiteChannelSyncModal close={() => setSyncOpen(false)} synced={() => void load(undefined, { silent: true, force: true })} />}
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
  const [accountSearch, setAccountSearch] = useState('')

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
  const filteredAccounts = useMemo(() => {
    const needle = accountSearch.trim().toLowerCase()
    if (!needle) return accounts
    return accounts.filter((account) => {
      const site = siteMap.get(account.site_id)
      return [account.label, account.credential_type, site?.name, site?.base_url]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(needle))
    })
  }, [accountSearch, accounts, siteMap])
  const toggle = (id: number) => setSelected((current) => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next })
  const selectAll = () => setSelected((current) => {
    const available = filteredAccounts.filter((account) => account.enabled).map((account) => account.id)
    if (available.length > 0 && available.every((id) => current.has(id))) {
      const next = new Set(current)
      available.forEach((id) => next.delete(id))
      return next
    }
    return new Set([...current, ...available])
  })

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
            <div><button className="text-button" type="button" onClick={selectAll}>{filteredAccounts.some((account) => account.enabled) && filteredAccounts.filter((account) => account.enabled).every((account) => selected.has(account.id)) ? '取消全选' : '全选搜索结果'}</button><button className="text-button" type="button" onClick={() => setSelected(new Set())}>清空</button></div>
          </div>
          <label className="search-field site-sync-search"><Search size={15} /><input value={accountSearch} onChange={(event) => setAccountSearch(event.target.value)} placeholder="搜索账号、站点名称或网址" aria-label="搜索站点账号" /><span>{filteredAccounts.length}/{accounts.length}</span></label>
          {!accounts.length ? <EmptyState label="还没有站点账号" /> : (
            <div className="site-sync-list">
              {!filteredAccounts.length ? <EmptyState label="没有匹配的站点账号" /> : filteredAccounts.map((account) => {
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

function ChannelRow({ channel, selected, busy, select, toggle, copy, diagnose, edit, remove }: { channel: Channel; selected: boolean; busy: boolean; select: () => void; toggle: () => void; copy: () => void; diagnose: () => void; edit: () => void; remove: () => void }) {
  const cooling = isCooling(channel)
  const activeModels = channel.models.filter((model) => !model.disabled)
  const protocols = Array.from(new Set(channel.urls.flatMap((url) => url.protocols?.length ? url.protocols : ['auto'])))
  return (
    <article className={`channel-row${selected ? ' row-selected' : ''}`}>
      <div className="channel-identity">
        <input className="row-selector" type="checkbox" checked={selected} onChange={select} aria-label={`选择 ${channel.name}`} />
        <span className={`status-dot ${channel.enabled ? cooling ? 'status-dot--warning' : 'status-dot--success' : 'status-dot--muted'}`} />
        <div><div className="channel-name"><strong title={channel.name}>{channel.name}</strong><small>#{channel.id}</small></div><span>{channel.auth_type === 'api_key' ? `${channel.key_count} Keys · ${channel.key_strategy || 'sequential'}` : channel.auth_type}</span></div>
      </div>
      <div className="channel-endpoints">{channel.urls[0]?.url ? <a className="site-base-link" href={channel.urls[0].url} target="_blank" rel="noreferrer" title={`在新标签页打开 ${channel.urls[0].url}`}><strong>{channel.urls[0].url}</strong></a> : <strong>未配置 URL</strong>}<span title={channel.urls.map((item) => item.url).join('\n')}>{channel.urls.length} URL · {protocols.join(' / ')}</span></div>
      <div className="channel-routing"><span title="基础优先级越大越优先；相同优先级按有效 Key 数量平滑轮询">优先级 <strong>{channel.priority}</strong></span>{channel.effective_priority !== undefined && <span title="健康度排序使用的有效优先级；失败率或首字延迟可能使它低于基础优先级">有效 <strong>{channel.effective_priority.toFixed(1)}</strong></span>}<span>倍率 <strong>{channel.cost_multiplier || 1}x</strong></span>{channel.success_rate !== undefined && <span title="统计窗口内的上游成功率">成功率 <strong>{Math.round(channel.success_rate * 100)}%</strong></span>}<small>{protocolMode(channel.protocol_transform_mode)}</small></div>
      <div className="channel-models"><strong>{activeModels.length} 模型</strong><span title={activeModels.map((item) => item.model).join(', ')}>{activeModels.slice(0, 2).map((item) => item.model).join(' · ') || '未配置'}</span></div>
      <div className="channel-limits"><span>RPM {channel.rpm_limit || '不限'}</span><span>并发 {channel.max_concurrency || '不限'}</span>{channel.auth_type === 'api_key' && <span title="有效 Key 会排除禁用或处于冷却中的 Key">Key {channel.effective_key_count ?? channel.key_count}/{channel.key_count}</span>}{cooling && <small className="text-warning" title={cooldownSummary(channel)}>存在冷却</small>}</div>
      <div className="row-actions">
        <button className="icon-button icon-button--surface" type="button" onClick={diagnose} aria-label={`诊断 ${channel.name} 路由`} title="路由诊断"><Activity size={16} /></button>
        <a className="icon-button icon-button--surface" href={`#/models?channel=${channel.id}&model=${encodeURIComponent(activeModels[0]?.model || '')}&view=probe`} aria-label={`测试 ${channel.name}`} title="模型测试"><FlaskConical size={16} /></a>
        <button className={`icon-button icon-button--surface ${channel.enabled ? 'is-on' : ''}`} type="button" onClick={toggle} disabled={busy} aria-label={channel.enabled ? `停用 ${channel.name}` : `启用 ${channel.name}`} title={channel.enabled ? '停用渠道' : '启用渠道'}><Power className={busy ? 'spin' : ''} size={16} /></button>
        <button className="icon-button icon-button--surface" type="button" onClick={copy} disabled={busy} aria-label={`复制 ${channel.name}`} title="复制渠道"><Copy size={16} /></button>
        <button className="icon-button icon-button--surface" type="button" onClick={edit} aria-label={`编辑 ${channel.name}`} title="编辑"><Pencil size={16} /></button>
        <button className="icon-button icon-button--surface danger-button" type="button" onClick={remove} disabled={busy} aria-label={`删除 ${channel.name}`} title="删除"><Trash2 size={16} /></button>
      </div>
    </article>
  )
}

function RouteDiagnosticModal({ channel, close }: { channel: Channel; close: () => void }) {
  const models = useMemo(() => channel.models.filter((item) => !item.disabled).map((item) => item.model), [channel.models])
  const [model, setModel] = useState(models[0] || '')
  const [clientProtocol, setClientProtocol] = useState('openai')
  const [tokens, setTokens] = useState<AuthToken[]>([])
  const [tokenID, setTokenID] = useState(0)
  const [result, setResult] = useState<RouteDiagnosticResponse | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    void getAuthTokens('today', controller.signal).then((data) => setTokens(data.tokens.filter((token) => token.is_active))).catch(() => setTokens([]))
    return () => controller.abort()
  }, [])

  const run = useCallback(async () => {
    if (!model.trim()) { setError('请选择或输入要诊断的模型'); return }
    setLoading(true); setError('')
    try {
      setResult(await getChannelRouteDiagnostics(channel.id, { model: model.trim(), client_protocol: clientProtocol, token_id: tokenID || undefined }))
    } catch (reason) { setError(reason instanceof Error ? reason.message : '路由诊断失败') }
    finally { setLoading(false) }
  }, [channel.id, clientProtocol, model, tokenID])

  useEffect(() => { if (model) void run() }, []) // eslint-disable-line react-hooks/exhaustive-deps

  return <Modal title={`路由诊断 · ${channel.name}`} close={close} wide>
    <div className="route-diagnostic">
      <div className="route-diagnostic-controls">
        <label>请求模型<input list={`diagnostic-models-${channel.id}`} value={model} onChange={(event) => setModel(event.target.value)} placeholder="输入模型名称" /><datalist id={`diagnostic-models-${channel.id}`}>{models.map((name) => <option value={name} key={name} />)}</datalist></label>
        <label>客户端协议<select value={clientProtocol} onChange={(event) => setClientProtocol(event.target.value)}><option value="openai">OpenAI</option><option value="anthropic">Anthropic</option><option value="codex">Codex</option><option value="gemini">Gemini</option></select></label>
        <label>下游令牌<select value={tokenID} onChange={(event) => setTokenID(Number(event.target.value))}><option value={0}>不检查令牌限制</option>{tokens.map((token) => <option value={token.id} key={token.id}>{token.description || `令牌 #${token.id}`}</option>)}</select></label>
        <button className="primary-button" type="button" onClick={() => void run()} disabled={loading || !model.trim()}>{loading ? <RefreshCw className="spin" size={15} /> : <Activity size={15} />}{loading ? '分析中' : '分析路由'}</button>
      </div>
      {error && <div className="modal-error inline-error">{error}</div>}
      {loading && !result ? <LoadingState label="正在计算当前路由候选" /> : result && <RouteDiagnosticResult result={result} />}
    </div>
  </Modal>
}

function RouteDiagnosticResult({ result }: { result: RouteDiagnosticResponse }) {
  const target = result.target
  const poolLabel = result.pool_mode === 'exact' ? '精确模型池' : result.pool_mode === 'fuzzy' ? '模糊匹配池' : result.pool_mode === 'cooldown_fallback' ? '全冷却兜底' : '无候选'
  return <>
    <section className={`route-diagnostic-verdict ${target.candidate ? 'is-eligible' : 'is-blocked'}`}>
      <div><span>{target.candidate ? '已进入候选池' : '当前不会被选中'}</span><strong>{poolLabel}</strong></div>
      <div className="route-diagnostic-summary">{result.summary.map((line) => <p key={line}>{line}</p>)}</div>
    </section>
    <section className="route-diagnostic-metrics">
      <Metric label="基础优先级" value={String(target.base_priority)} />
      <Metric label="有效优先级" value={result.health_score_enabled ? target.effective_priority.toFixed(1) : '未启用'} />
      <Metric label="有效 Key" value={`${target.active_key_count}/${target.enabled_key_count}`} />
      <Metric label="优先级层级" value={target.candidate_position ? `第 ${target.candidate_position} 层` : '—'} />
      <Metric label="同级理论份额" value={target.candidate && target.same_priority_count > 1 ? `${(target.estimated_traffic_share * 100).toFixed(1)}%` : '—'} />
      <Metric label="近期成功率" value={result.health_score_enabled ? `${Math.round(target.success_rate * 100)}% · ${target.health_sample_count} 样本` : '未参与排序'} />
    </section>
    <section className="route-diagnostic-reasons"><header><strong>判断依据</strong><span>红色项会阻止或临时跳过该渠道</span></header><div>{target.reasons.map((reason) => <div className={reason.blocking ? 'is-blocking' : 'is-info'} key={reason.code}><span />{reason.message}</div>)}</div></section>
    <section className="route-candidate-list"><header><strong>当前候选池</strong><span>{result.candidates.length} 个渠道；同层顺序仅供展示，实际首选由平滑加权轮询决定</span></header>{result.candidates.length ? result.candidates.map((candidate) => <RouteCandidateRow candidate={candidate} targetID={target.channel_id} healthEnabled={result.health_score_enabled} key={candidate.channel_id} />) : <span className="selection-empty">当前模型没有可用候选渠道。</span>}</section>
  </>
}

function Metric({ label, value }: { label: string; value: string }) { return <div><span>{label}</span><strong>{value}</strong></div> }

function RouteCandidateRow({ candidate, targetID, healthEnabled }: { candidate: ChannelRouteDiagnostic; targetID: number; healthEnabled: boolean }) {
  return <div className={`route-candidate-row${candidate.channel_id === targetID ? ' is-target' : ''}`}>
    <span className="route-rank" title="优先级层级">P{candidate.candidate_position}</span>
    <div><strong title={candidate.channel_name}>{candidate.channel_name}</strong><span>#{candidate.channel_id}{candidate.channel_id === targetID ? ' · 当前渠道' : ''}</span></div>
    <span>P{candidate.base_priority}{healthEnabled ? ` / 有效 ${candidate.effective_priority.toFixed(1)}` : ''}</span>
    <span>Key {candidate.active_key_count}/{candidate.enabled_key_count}</span>
    <span>{candidate.same_priority_count > 1 ? `同级约 ${(candidate.estimated_traffic_share * 100).toFixed(1)}%` : '独占当前级'}</span>
  </div>
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
  const [loading, setLoading] = useState(Boolean(channelId))
  const [saving, setSaving] = useState(false)
  const [discovering, setDiscovering] = useState(false)
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

  const submit = async (event: React.FormEvent) => {
    event.preventDefault(); setSaving(true); setError('')
    try {
      const models = normalizeEditorModels(form.models)
      const payload: ChannelMutation = {
        name: form.name.trim(), auth_type: form.authType,
        urls: parseURLs(form.urls), models, api_keys: form.authType === 'api_key' ? parseKeys(form.keys) : [],
        key_strategy: form.keyStrategy, priority: Number(form.priority) || 0, rpm_limit: Number(form.rpmLimit) || 0,
        max_concurrency: Number(form.maxConcurrency) || 0, enabled: form.enabled, websockets: form.websockets,
        protocol_transform_mode: form.protocolMode, scheduled_check_enabled: snapshot?.channel.scheduled_check_enabled || false,
        scheduled_check_model: snapshot?.channel.scheduled_check_model || '', daily_cost_limit: Number(form.dailyCostLimit) || 0,
        cost_multiplier: Number(form.costMultiplier), proxy_url: form.proxyURL.trim(), retry_other_keys_on_failure: form.retryOtherKeys,
        custom_request_rules: snapshot?.channel.custom_request_rules, cooldown_detection_rules: snapshot?.channel.cooldown_detection_rules,
      }
      if (!payload.urls.length) throw new Error('至少填写一个上游 URL')
      if (!payload.models.some((item) => item.model && !item.disabled)) throw new Error('至少保留一个启用模型')
      if (form.authType === 'api_key' && !payload.api_keys.length) throw new Error('至少填写一个 API Key')
      if (channelId) await updateChannel(channelId, payload); else await createChannel(payload)
      saved()
    } catch (reason) { setError(reason instanceof Error ? reason.message : '渠道保存失败') }
    finally { setSaving(false) }
  }

  const discoverModels = async () => {
    const urls = parseURLs(form.urls)
    const keys = parseKeys(form.keys).map((item) => item.api_key)
    if (!urls.length || !keys.length) { setError('请先填写上游 URL 和 API Key'); return }
    setDiscovering(true); setError('')
    try {
      const result = await fetchChannelModelsPreview({ urls, api_keys: keys })
      const models = result.models.filter((item) => item.model.trim())
      if (!models.length) throw new Error('上游没有返回可用模型')
      setForm((current) => ({ ...current, models: mergeDiscoveredModels(current.models, models) }))
    } catch (reason) { setError(reason instanceof Error ? reason.message : '模型获取失败') }
    finally { setDiscovering(false) }
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
      {form.authType === 'api_key' ? <label className="textarea-field">API Keys<textarea required rows={4} value={form.keys} onChange={(event) => setForm({ ...form, keys: event.target.value })} placeholder="每行一个；可写 key | 备注" /></label> : <div className="form-help">OAuth 渠道不在这里手填密钥，请使用顶部“导入凭证”导入 Codex 或 Antigravity 凭证文件。</div>}
      {form.authType === 'api_key' && <div className="model-discovery-action"><div><strong>自动获取模型</strong><span>使用上方 URL 和 Key 请求模型列表，获取结果会直接用于渠道。</span></div><button className="secondary-button" type="button" onClick={() => void discoverModels()} disabled={discovering || !form.urls.trim() || !form.keys.trim()}>{discovering ? <RefreshCw className="spin" size={15} /> : <Sparkles size={15} />}{discovering ? '获取中' : '获取模型'}</button></div>}
      <EditableModelList models={form.models} onChange={(models) => setForm((current) => ({ ...current, models }))} />
      <div className="checkbox-grid"><label className="checkbox-field"><input type="checkbox" checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} />启用渠道</label><label className="checkbox-field"><input type="checkbox" checked={form.websockets} onChange={(event) => setForm({ ...form, websockets: event.target.checked })} />WebSocket</label><label className="checkbox-field"><input type="checkbox" checked={form.retryOtherKeys} onChange={(event) => setForm({ ...form, retryOtherKeys: event.target.checked })} />失败时尝试其他 Key</label></div>
      <footer><button className="secondary-button" type="button" onClick={close}>取消</button><button className="primary-button" type="submit" disabled={saving}>{saving ? <RefreshCw className="spin" size={15} /> : null}{saving ? '保存中' : '保存渠道'}</button></footer>
    </form>}
  </Modal>
}

function EditableModelList({ models, onChange }: { models: ChannelModel[]; onChange: (models: ChannelModel[]) => void }) {
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [query, setQuery] = useState('')
  useEffect(() => {
    setSelected((current) => new Set([...current].filter((index) => index >= 0 && index < models.length)))
  }, [models.length])
  const visibleIndexes = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return models.reduce<number[]>((indexes, item, index) => {
      if (!needle || item.model.toLowerCase().includes(needle) || (item.redirect_model || '').toLowerCase().includes(needle)) indexes.push(index)
      return indexes
    }, [])
  }, [models, query])
  const update = (index: number, patch: Partial<ChannelModel>) => onChange(models.map((item, itemIndex) => itemIndex === index ? { ...item, ...patch } : item))
  const remove = (index: number) => {
    onChange(models.filter((_, itemIndex) => itemIndex !== index))
    setSelected((current) => new Set([...current].filter((itemIndex) => itemIndex !== index).map((itemIndex) => itemIndex > index ? itemIndex - 1 : itemIndex)))
  }
  const removeSelected = () => {
    if (!selected.size) return
    onChange(models.filter((_, index) => !selected.has(index)))
    setSelected(new Set())
  }
  const add = () => { setQuery(''); onChange([...models, { model: '', redirect_model: '' }]) }
  const allVisibleSelected = visibleIndexes.length > 0 && visibleIndexes.every((index) => selected.has(index))
  const toggleVisible = () => setSelected((current) => {
    const next = new Set(current)
    if (allVisibleSelected) visibleIndexes.forEach((index) => next.delete(index))
    else visibleIndexes.forEach((index) => next.add(index))
    return next
  })
  return <section className="selection-panel discovered-model-panel">
    <header><div><strong>渠道模型</strong><span>{models.length ? `${models.length} 个模型，可删减或配置映射${selected.size ? `，累计已选 ${selected.size} 个` : ''}` : '尚未配置模型'}</span></div><div className="model-list-actions"><button className="text-button" type="button" onClick={toggleVisible} disabled={!visibleIndexes.length}>{allVisibleSelected ? '取消当前结果' : '全选搜索结果'}</button>{selected.size > 0 && <button className="text-button" type="button" onClick={() => setSelected(new Set())}>清空选择</button>}{selected.size > 0 && <button className="text-button danger-text-button" type="button" onClick={removeSelected}><Trash2 size={14} />删除所选 ({selected.size})</button>}<button className="text-button" type="button" onClick={add}><Plus size={14} />添加模型</button></div></header>
    {models.length ? <><div className="model-selection-toolbar"><label className="search-field selection-search"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索模型或映射名称" aria-label="搜索渠道模型" /><span>{visibleIndexes.length}/{models.length}</span></label>{selected.size > 0 && <small>已跨搜索保留 {selected.size} 个选择</small>}</div>{visibleIndexes.length ? <div className="editable-model-list">{visibleIndexes.map((index) => { const item = models[index]; return <div className="editable-model-row" key={`${item.model}-${index}`}><input type="checkbox" checked={selected.has(index)} onChange={() => setSelected((current) => { const next = new Set(current); if (next.has(index)) next.delete(index); else next.add(index); return next })} aria-label={`选择模型 ${item.model || index + 1}`} /><input value={item.model} onChange={(event) => update(index, { model: event.target.value })} placeholder="对外模型名" aria-label={`第 ${index + 1} 个对外模型`} /><span aria-hidden="true">→</span><input value={item.redirect_model || ''} onChange={(event) => update(index, { redirect_model: event.target.value })} placeholder="映射到上游模型（可选）" aria-label={`第 ${index + 1} 个上游映射`} /><button className="icon-button icon-button--surface danger-button" type="button" onClick={() => remove(index)} aria-label={`删除模型 ${item.model || index + 1}`} title="删除模型"><Trash2 size={15} /></button></div> })}</div> : <span className="selection-empty">没有匹配“{query.trim()}”的模型，已勾选的其他模型仍会保留。</span>}</> : <span className="selection-empty">填写 URL 和 API Key 后点击“获取模型”，也可以直接添加模型。</span>}
  </section>
}

function mergeDiscoveredModels(existing: ChannelModel[], discovered: ChannelModel[]): ChannelModel[] {
  const result = [...existing]
  const seen = new Set(existing.map((item) => item.model.trim().toLowerCase()).filter(Boolean))
  for (const item of discovered) {
    const model = item.model.trim()
    if (!model || seen.has(model.toLowerCase())) continue
    seen.add(model.toLowerCase())
    result.push({ model, redirect_model: item.redirect_model || '' })
  }
  return result
}

function normalizeEditorModels(models: ChannelModel[]): ChannelModel[] {
  const seen = new Set<string>()
  return models.map((item) => ({
    model: item.model.trim(),
    redirect_model: item.redirect_model?.trim() || '',
    disabled: Boolean(item.disabled),
  })).filter((item) => {
    const key = item.model.toLowerCase()
    if (!key || seen.has(key)) return false
    seen.add(key)
    return true
  })
}

function parseURLs(value: string): ChannelURL[] { return splitRows(value).map((row) => { const [url, protocolText] = row.split('|').map((item) => item.trim()); return { url, ...(protocolText ? { protocols: protocolText.split(',').map((item) => item.trim()).filter(Boolean) } : {}) } }).filter((item) => item.url) }
function parseKeys(value: string): ChannelMutation['api_keys'] { return splitRows(value).map((row) => { const [api_key, note = ''] = row.split('|').map((item) => item.trim()); return { api_key, note } }).filter((item) => item.api_key) }
function splitRows(value: string): string[] { return value.split('\n').map((item) => item.trim()).filter(Boolean) }

function isCooling(channel: Channel): boolean {
  return Boolean(channel.cooldown_remaining_ms || channel.key_cooldowns?.some((item) => item.cooldown_remaining_ms) || channel.model_cooldowns?.some((item) => item.cooldown_remaining_ms))
}

function cooldownSummary(channel: Channel): string {
  const keyCount = channel.key_cooldowns?.filter((item) => Boolean(item.cooldown_remaining_ms)).length || 0
  const modelCount = channel.model_cooldowns?.filter((item) => Boolean(item.cooldown_remaining_ms)).length || 0
  const parts = [channel.cooldown_remaining_ms ? '渠道冷却' : '', keyCount ? `${keyCount} 个 Key 冷却` : '', modelCount ? `${modelCount} 个模型冷却` : ''].filter(Boolean)
  return parts.length ? parts.join('；') : '存在冷却状态'
}

function protocolMode(mode: string): string {
  if (mode === 'local') return '本地协议转换'
  if (mode === 'upstream') return '上游原生协议'
  return '自动协议协商'
}

function snapshotToMutation(snapshot: ChannelEditorSnapshot, name: string): ChannelMutation {
  const channel = snapshot.channel
  return {
    name,
    auth_type: channel.auth_type,
    api_keys: snapshot.keys.map((item) => ({ api_key: item.api_key, note: item.note })),
    key_strategy: channel.key_strategy || snapshot.keys[0]?.key_strategy || 'sequential',
    urls: channel.urls,
    priority: channel.priority || 0,
    rpm_limit: channel.rpm_limit || 0,
    max_concurrency: channel.max_concurrency || 0,
    models: channel.models,
    enabled: channel.enabled,
    websockets: Boolean(channel.websockets),
    protocol_transform_mode: channel.protocol_transform_mode || 'auto',
    scheduled_check_enabled: Boolean(channel.scheduled_check_enabled),
    scheduled_check_model: channel.scheduled_check_model || '',
    daily_cost_limit: channel.daily_cost_limit || 0,
    cost_multiplier: channel.cost_multiplier ?? 1,
    proxy_url: channel.proxy_url || '',
    retry_other_keys_on_failure: Boolean(channel.retry_other_keys_on_failure),
    custom_request_rules: channel.custom_request_rules,
    cooldown_detection_rules: channel.cooldown_detection_rules,
  }
}
