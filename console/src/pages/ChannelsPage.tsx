import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Copy, FileUp, FlaskConical, Layers3, Pencil, Plus, Power, RefreshCw, Route, Search, Settings2, Sparkles, Trash2 } from 'lucide-react'
import { createChannel, deleteChannel, deleteChannels, fetchChannelModelsPreview, fetchOAuthUsage, getAuthTokens, getChannelEditor, getChannelRouteDiagnostics, getChannels, getSiteChannelBindings, getSiteInventory, importOAuthCredentials, peekChannels, restoreChannelSiteSync, runAccountTask, setChannelsEnabled, updateChannel } from '../api'
import type { AuthToken, Channel, ChannelBodyRuleAction, ChannelEditorSnapshot, ChannelHeaderRuleAction, ChannelModel, ChannelMutation, ChannelRequestRules, ChannelRouteDiagnostic, ChannelURL, OAuthUsageSummary, RouteDiagnosticResponse, Site, SiteAccount, SiteChannelBinding } from '../types'
import { EmptyState, ErrorState, LoadingState, OperationNotice, PageHeader, Pagination } from './shared'
import { useLocation } from 'react-router-dom'
import { Modal, siteErrorMessage, StatusBadge } from './siteShared'
import ChannelKeysModal from './ChannelKeysModal'

const routeDiagnosticProtocols = [
  { value: 'openai', label: 'OpenAI' },
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'codex', label: 'Codex' },
  { value: 'gemini', label: 'Gemini' },
]

export default function ChannelsPage() {
  const location = useLocation()
  const query = useMemo(() => new URLSearchParams(location.search), [location.search])
  const querySearch = query.get('search') || ''
  const focusChannelID = Number(query.get('focus_channel_id') || 0)
  const initialResult = peekChannels({ search: querySearch, status: 'all', source: 'all', sort: 'priority', limit: 50, offset: 0 })
  const [channels, setChannels] = useState<Channel[]>(() => initialResult?.data || [])
  const [total, setTotal] = useState(() => initialResult?.count || 0)
  const [enabledTotal, setEnabledTotal] = useState(() => initialResult?.enabled_count ?? 0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const [searchDraft, setSearchDraft] = useState(querySearch)
  const [search, setSearch] = useState(querySearch)
  const [status, setStatus] = useState('all')
  const [source, setSource] = useState('all')
  const [sort, setSort] = useState('priority')
  const [loading, setLoading] = useState(!initialResult)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busyId, setBusyId] = useState<number | null>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState(false)
	const [editing, setEditing] = useState<number | 'new' | null>(null)
	const [keyHealthChannel, setKeyHealthChannel] = useState<number | null>(null)
	const [routeDiagnosticChannel, setRouteDiagnosticChannel] = useState<Channel | null>(null)
  const [sourceMenuOpen, setSourceMenuOpen] = useState(false)
  const [syncOpen, setSyncOpen] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const importInput = useRef<HTMLInputElement>(null)
  const focusedChannelRef = useRef(0)
  const loadedOnceRef = useRef(Boolean(initialResult))
  const loadSequence = useRef(0)

  // 手动刷新不带 AbortSignal，切换筛选时旧请求不会被取消，因此按序号丢弃过期响应。
  // 否则强制刷新（走网络）可能在筛选切换后落地，覆盖掉缓存命中的新筛选结果，
  // 出现「已启用」筛选下显示全部渠道的错配。
  const load = useCallback(async (signal?: AbortSignal, options: { silent?: boolean; force?: boolean } = {}) => {
    const sequence = ++loadSequence.current
    const stale = () => Boolean(signal?.aborted) || sequence !== loadSequence.current
    const filters = { search, status, source, sort, limit: pageSize, offset: (page - 1) * pageSize }
    const cached = peekChannels(filters)
    if (cached) {
      setChannels(cached.data)
      setTotal(cached.count)
      setEnabledTotal(cached.enabled_count ?? 0)
    }
    if (!options.silent) setLoading(!cached && !loadedOnceRef.current)
    setError('')
    try {
      const result = await getChannels(filters, signal, { force: options.force })
      if (stale()) return
      setChannels(result.data)
      setTotal(result.count)
      setEnabledTotal(result.enabled_count ?? 0)
      loadedOnceRef.current = true
      setSelected((current) => new Set([...current].filter((id) => result.data.some((channel) => channel.id === id))))
    } catch (reason) {
      if (!stale()) setError(reason instanceof Error ? reason.message : '渠道加载失败')
    } finally {
      // 复位 loading 不看序号：被更新的 silent load 顶掉时，那一次不会碰 loading，
      // 若这里也跳过复位，骨架屏就永久留在页面上。loading 只是 UI 标志，
      // 提前复位最坏是短暂显示旧数据，而漏复位会卡死。数据写入仍由序号守卫把关。
      if (!signal?.aborted && !options.silent) setLoading(false)
    }
  }, [page, pageSize, search, sort, source, status])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  // 分页在服务端，删完末页整页后 offset 会越界，请求返回 0 行，
  // 页面显示「没有匹配的渠道」而汇总仍写着渠道总数。回退到最后一个有效页。
  useEffect(() => {
    const pages = Math.max(1, Math.ceil(total / pageSize))
    if (page > pages) setPage(pages)
  }, [page, pageSize, total])

  useEffect(() => { setPage(1); setSearchDraft(querySearch); setSearch(querySearch) }, [querySearch])
  useEffect(() => {
    const nextSearch = searchDraft.trim()
    if (nextSearch === search) return
    const timer = window.setTimeout(() => {
      setPage(1)
      setSearch(nextSearch)
    }, 250)
    return () => window.clearTimeout(timer)
  }, [search, searchDraft])
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
    if (!window.confirm(`删除渠道"${channel.name}"？该渠道将立即退出路由。`)) return
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
      setNotice(`已复制渠道"${channel.name}"`)
      await load(undefined, { silent: true, force: true })
    } catch (reason) { setError(reason instanceof Error ? reason.message : '渠道复制失败') }
    finally { setBusyId(null) }
  }

  const restoreSiteSync = async (channel: Channel) => {
    if (!window.confirm(`恢复"${channel.name}"的自动同步？下一次同步会按上游模型覆盖当前手动修改。`)) return
    setBusyId(channel.id); setError(''); setNotice('')
    try {
      await restoreChannelSiteSync(channel.id)
      setNotice(`已恢复"${channel.name}"的自动同步`)
      await load(undefined, { silent: true, force: true })
    } catch (reason) { setError(reason instanceof Error ? reason.message : '恢复自动同步失败') }
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
      <PageHeader
        icon={Route}
        title="渠道分发"
        actions={<>
          <input ref={importInput} className="visually-hidden" type="file" accept="application/json,.json" multiple onChange={(event) => void importCredentials(event.target.files)} />
		  <div className="source-menu"><button className="secondary-button" type="button" aria-haspopup="menu" aria-expanded={sourceMenuOpen} onClick={() => setSourceMenuOpen((open) => !open)}><Layers3 size={16} />其他来源</button>{sourceMenuOpen && <div className="source-menu-popover" role="menu"><button type="button" role="menuitem" onClick={() => { setSourceMenuOpen(false); importInput.current?.click() }}><FileUp size={15} />导入 OAuth</button><button type="button" role="menuitem" onClick={() => { setSourceMenuOpen(false); setEditing('new') }}><Plus size={15} />手工渠道</button></div>}</div>
		  <button className="primary-button" type="button" onClick={() => setSyncOpen(true)}><RefreshCw size={16} />同步站点渠道</button>
          <button className="icon-button icon-button--surface" type="button" disabled={refreshing} title="刷新渠道" onClick={async () => { setRefreshing(true); try { await load(undefined, { silent: true, force: true }) } finally { setRefreshing(false) } }} aria-label="刷新渠道"><RefreshCw size={17} className={refreshing ? 'spin' : undefined} /></button>
        </>}
      />

      {notice && <OperationNotice onDismiss={() => setNotice('')}>{notice}</OperationNotice>}

      <section className="compact-summary channels-summary" aria-label="渠道摘要">
        <span><strong>{total}</strong>渠道总数</span><span><strong>{enabledTotal}</strong>全部启用</span><span><strong>{summary.enabled}</strong>本页启用</span><span><strong>{summary.cooldown}</strong>冷却中</span><span><strong>{summary.models}</strong>可用模型映射</span>
      </section>

      <div className="filter-bar">
        <label className="selection-toggle"><input type="checkbox" checked={allPageSelected} onChange={toggleAllPage} aria-label={`选择当前页全部渠道（${channels.length} 条）`} /><span>全选本页</span></label>
        <label className="search-field"><Search size={16} /><input value={searchDraft} onChange={(event) => setSearchDraft(event.target.value)} placeholder="搜索渠道名称" aria-label="搜索渠道名称" /></label>
        <select value={status} onChange={(event) => { setPage(1); setStatus(event.target.value) }} aria-label="渠道状态">
          <option value="all">全部状态</option><option value="enabled">已启用</option><option value="disabled">已停用</option><option value="cooldown">冷却中</option>
        </select>
        <select value={source} onChange={(event) => { setPage(1); setSource(event.target.value) }} aria-label="渠道来源">
          <option value="all">全部来源</option><option value="site_sync">站点同步</option><option value="manual">手工添加</option><option value="auth">AUTH 添加</option>
        </select>
        <select value={sort} onChange={(event) => { setPage(1); setSort(event.target.value) }} aria-label="渠道排序">
          <option value="priority">优先级</option><option value="newest">新建优先</option><option value="name">名称 A-Z</option><option value="enabled">启用优先</option><option value="models">模型数量</option>
        </select>
      </div>

      {selected.size > 0 && <div className="batch-toolbar" aria-label="渠道批量操作"><strong>已选择 {selected.size} 项</strong><div><button type="button" onClick={() => void runBatch('enable')} disabled={batchBusy}><Power size={14} />启用</button><button type="button" onClick={() => void runBatch('disable')} disabled={batchBusy}><Power size={14} />禁用</button><button className="danger-button" type="button" onClick={() => void runBatch('delete')} disabled={batchBusy}><Trash2 size={14} />删除</button></div></div>}

      {error && channels.length > 0 && <OperationNotice tone="error">{error}</OperationNotice>}
      {loading ? <LoadingState label="正在加载渠道" /> : error && channels.length === 0 ? <ErrorState message={error} retry={() => void load()} /> : channels.length === 0 ? <EmptyState label="没有符合条件的渠道" /> : (
        <div className="channel-list">
          {channels.map((channel) => <ChannelRow channel={channel} selected={selected.has(channel.id)} busy={busyId === channel.id || (batchBusy && selected.has(channel.id))} select={() => toggleSelected(channel.id)} toggle={() => void toggleChannel(channel)} copy={() => void copyChannel(channel)} edit={() => setEditing(channel.id)} remove={() => void removeChannel(channel)} manageKeys={() => setKeyHealthChannel(channel.id)} diagnose={() => setRouteDiagnosticChannel(channel)} restoreSync={() => void restoreSiteSync(channel)} key={channel.id} />)}
        </div>
      )}
      <Pagination page={page} pageSize={pageSize} total={total} onPage={setPage} pageSizes={[50, 100]} onPageSize={(size) => { setPage(1); setPageSize(size) }} />
      {editing && <ChannelEditor channelId={editing === 'new' ? undefined : editing} close={() => setEditing(null)} saved={() => { setEditing(null); void load(undefined, { silent: true, force: true }) }} />}
	      {syncOpen && <SiteChannelSyncModal close={() => setSyncOpen(false)} synced={() => void load(undefined, { silent: true, force: true })} />}
	      {keyHealthChannel !== null && <ChannelKeysModal channelId={keyHealthChannel} close={() => setKeyHealthChannel(null)} changed={() => void load(undefined, { silent: true, force: true })} />}
	      {routeDiagnosticChannel && <ChannelRouteDiagnosticsModal key={routeDiagnosticChannel.id} channel={routeDiagnosticChannel} close={() => setRouteDiagnosticChannel(null)} />}
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
    setSyncing(true); setError(''); setResults(new Map(targets.map((account) => [account.id, { status: 'queued' }]))); setProgress({ done: 0, total: targets.length })
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

  const resultValues = [...results.values()]
  const successCount = resultValues.filter((item) => item.status === 'success').length
  const partialCount = resultValues.filter((item) => item.status === 'partial').length
  const failedCount = resultValues.filter((item) => item.status === 'failed').length
  const completedCount = successCount + partialCount + failedCount
  const progressPercent = progress.total ? Math.round((progress.done / progress.total) * 100) : 0
  const hasFinished = results.size > 0 && completedCount === progress.total && !syncing

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
          {(syncing || hasFinished) && <div className="site-sync-progress" aria-live="polite"><div className="site-sync-progress-head"><strong>{syncing ? `同步进行中 · ${progress.done}/${progress.total}` : `同步完成 · 成功 ${successCount} · 部分成功 ${partialCount} · 失败 ${failedCount}`}</strong><span>{progressPercent}%</span></div><div className="site-sync-progress-track"><i style={{ width: `${progressPercent}%` }} /></div></div>}
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
          <footer><span>{syncing ? `正在同步 ${progress.done}/${progress.total}` : hasFinished ? `已完成 ${completedCount} 个账号，可查看上方逐项结果` : `已选择 ${selected.size} 个账号`}</span><div><button className="secondary-button" type="button" onClick={close} disabled={syncing}>关闭</button><button className="primary-button" type="button" onClick={() => void sync()} disabled={syncing || !selected.size}>{syncing && <RefreshCw className="spin" size={15} />}{syncing ? '同步中' : hasFinished ? '再次同步' : '开始同步'}</button></div></footer>
        </div>
      )}
    </Modal>
  )
}

function ChannelRouteDiagnosticsModal({ channel, close }: { channel: Channel; close: () => void }) {
  const models = useMemo(() => channel.models.filter((item) => !item.disabled).map((item) => item.model.trim()).filter(Boolean), [channel])
  const [model, setModel] = useState(models[0] || '')
  const [protocol, setProtocol] = useState('openai')
  const [tokenIdValue, setTokenIdValue] = useState('')
  const [tokens, setTokens] = useState<AuthToken[]>([])
  const [snapshot, setSnapshot] = useState<RouteDiagnosticResponse | null>(null)
  const [loading, setLoading] = useState(Boolean(models.length))
  const [error, setError] = useState('')
  const tokenId = Number(tokenIdValue) || 0

  const refresh = useCallback(async (signal?: AbortSignal) => {
    if (!model) {
      setSnapshot(null)
      setLoading(false)
      setError('')
      return
    }
    setLoading(true)
    setError('')
    try {
      const result = await getChannelRouteDiagnostics(channel.id, model, protocol, tokenId, signal)
      setSnapshot(result)
    } catch (reason) {
      if (!signal?.aborted) setError(reason instanceof Error ? reason.message : '路由诊断加载失败')
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [channel.id, model, protocol, tokenId])

  useEffect(() => {
    if (!model) return
    const controller = new AbortController()
    void refresh(controller.signal)
    return () => controller.abort()
  }, [model, protocol, refresh, tokenIdValue])

  useEffect(() => {
    const controller = new AbortController()
    getAuthTokens('today', controller.signal).then((result) => {
      if (!controller.signal.aborted) setTokens(result.tokens || [])
    }).catch((reason) => {
      if (!controller.signal.aborted) setError(reason instanceof Error ? `访问令牌加载失败：${reason.message}` : '访问令牌加载失败')
    })
    return () => controller.abort()
  }, [])

  const strategyLabel = snapshot?.route_strategy === 'sticky' ? '粘性轮询' : '均衡轮询'
  const target = snapshot?.target
  return (
    <Modal title={`${channel.name} · 路由诊断`} close={close} wide>
      <div className="route-diagnostic-panel">
        <div className="route-diagnostic-controls">
          <label><span>诊断模型</span><select value={model} onChange={(event) => setModel(event.target.value)}>{models.map((item) => <option value={item} key={item}>{item}</option>)}</select></label>
          <label><span>客户端协议</span><select value={protocol} onChange={(event) => setProtocol(event.target.value)}>{routeDiagnosticProtocols.map((item) => <option value={item.value} key={item.value}>{item.label}</option>)}</select></label>
          <label title="选择访问令牌后，诊断会套用该令牌的渠道限制、统计它的今日实际占比，并在粘性轮询下显示当前命中渠道"><span>访问令牌</span><select value={tokenIdValue} onChange={(event) => setTokenIdValue(event.target.value)}><option value="">全局视角</option>{tokens.map((token) => <option value={token.id} key={token.id}>{authTokenLabel(token)}</option>)}</select></label>
          <button className="secondary-button" type="button" onClick={() => void refresh()} disabled={loading || !model}><RefreshCw className={loading ? 'spin' : ''} size={15} />{loading ? '诊断中' : '重新诊断'}</button>
        </div>
        {snapshot?.route_strategy === 'sticky' && snapshot.sticky ? (
          <div className="route-diagnostic-sticky">
            <div><small>当前粘性命中</small><strong title={snapshot.sticky.channel_name}>{snapshot.sticky.channel_name || `#${snapshot.sticky.channel_id}`}</strong><span>记录于 {formatDiagnosticTime(snapshot.sticky.remembered_at)} · 有效至 {formatDiagnosticTime(snapshot.sticky.expires_at)}</span></div>
            <p>{snapshot.sticky.in_candidate_pool ? '该渠道在当前候选池中。粘性只会把同优先级层内的它提前，不会让低优先级渠道越过更高优先级渠道。' : '该渠道当前不在候选池中，实际请求会按候选池重新选择；这条历史记录不会强行接管本次路由。'}</p>
          </div>
        ) : snapshot?.route_strategy === 'sticky' && !tokenId ? <div className="route-diagnostic-note">当前是粘性轮询：同一「访问令牌 + 模型」会优先沿用上次成功渠道。请选择访问令牌查看当前命中；未选择时只能展示全局候选和日志占比。</div> : null}
        {loading ? <LoadingState label="正在计算候选池" /> : error ? <ErrorState message={error} retry={() => void refresh()} /> : !model ? <EmptyState label="该渠道没有启用模型" /> : snapshot ? (
          <>
            <div className="route-diagnostic-summary">
              <article><small>当前策略</small><strong>{strategyLabel}</strong><span>{snapshot.route_strategy === 'sticky' ? '按令牌和模型保持亲和' : '同优先级按权重轮转'}</span></article>
              <article><small>候选渠道</small><strong>{snapshot.candidates.length}</strong><span>{snapshot.pool_mode === 'exact' ? '精确模型匹配' : snapshot.pool_mode === 'fuzzy' ? '模糊模型匹配' : snapshot.pool_mode === 'cooldown_fallback' ? '冷却兜底' : '暂无候选'}</span></article>
              <article><small>今日实际请求</small><strong>{snapshot.actual_total_requests}</strong><span>{tokenId ? `令牌 #${tokenId} · ${snapshot.actual_window}` : `全部令牌 · ${snapshot.actual_window}`}</span></article>
              <article className={target?.candidate ? 'is-success' : 'is-warning'}><small>诊断结果</small><strong>{target?.candidate ? '已进入候选池' : '不在候选池'}</strong><span>{target?.candidate_position ? `第 ${target.candidate_position} 优先级层` : '未排序'}</span></article>
            </div>
            {snapshot.summary.length > 0 && <div className="route-diagnostic-callout"><strong>结论</strong><p>{snapshot.summary.join(' ')}</p></div>}
            {target && <RouteDiagnosticChannelCard diagnostic={target} actualTotal={snapshot.actual_total_requests} highlight />}
            <section className="route-diagnostic-candidates">
              <header><strong>候选池 · 优先级层</strong><span>列表只表示当前会进入的候选池与优先级层；层内顺序不代表下一次请求一定命中。理论份额由优先级层和可用 Key 计算，今日实际占比来自请求日志。</span></header>
              {snapshot.candidates.length ? <div className="route-diagnostic-list">{snapshot.candidates.map((item) => <RouteDiagnosticChannelCard diagnostic={item} actualTotal={snapshot.actual_total_requests} key={item.channel_id} />)}</div> : <EmptyState label="当前模型没有可用候选渠道" />}
            </section>
          </>
        ) : <EmptyState label="暂无路由诊断数据" />}
      </div>
    </Modal>
  )
}

function RouteDiagnosticChannelCard({ diagnostic, actualTotal, highlight = false }: { diagnostic: ChannelRouteDiagnostic; actualTotal: number; highlight?: boolean }) {
  return (
    <article className={`route-diagnostic-channel${highlight ? ' is-highlight' : ''}`}>
      <header>
        <div><strong title={diagnostic.channel_name}>{diagnostic.channel_name}</strong><small>#{diagnostic.channel_id}</small></div>
        <div className="route-diagnostic-shares">
          <span>{diagnostic.candidate ? `${(diagnostic.estimated_traffic_share * 100).toFixed(1)}% 理论份额` : '未进入候选池'}</span>
          <span className={actualTotal > 0 ? '' : 'is-muted'}>{actualTotal > 0 ? `${(diagnostic.actual_share * 100).toFixed(1)}% 今日实际` : '今日暂无请求'}</span>
        </div>
      </header>
      <dl>
        <div><dt>优先级层</dt><dd>{diagnostic.candidate && diagnostic.candidate_position ? `第 ${diagnostic.candidate_position} 层` : '—'}</dd></div>
        <div title="分子：未冷却且允许当前模型的 Key；分母：允许当前模型的启用 Key"><dt>可用 Key</dt><dd>{diagnostic.active_key_count}/{diagnostic.model_eligible_key_count}</dd></div>
        <div><dt>今日实际</dt><dd>{diagnostic.actual_requests}{actualTotal > 0 ? ` · ${(diagnostic.actual_share * 100).toFixed(1)}%` : ''}</dd></div>
        <div><dt>RPM</dt><dd>{diagnostic.rpm_limit ? `${diagnostic.current_rpm}/${diagnostic.rpm_limit}` : '不限'}</dd></div>
        <div><dt>并发</dt><dd>{diagnostic.max_concurrency ? `${diagnostic.active_concurrency}/${diagnostic.max_concurrency}` : '不限'}</dd></div>
      </dl>
      <ul>
        {diagnostic.reasons.length ? diagnostic.reasons.map((reason) => <li key={`${diagnostic.channel_id}-${reason.code}`} className={reason.blocking ? 'is-blocking' : ''}>{reason.message}</li>) : <li>暂无额外说明</li>}
      </ul>
    </article>
  )
}

function authTokenLabel(token: AuthToken) {
  return `${token.description || token.token_hint || '未命名令牌'} · #${token.id}`
}

function formatDiagnosticTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

function ChannelRow({ channel, selected, busy, select, toggle, copy, edit, remove, manageKeys, diagnose, restoreSync }: { channel: Channel; selected: boolean; busy: boolean; select: () => void; toggle: () => void; copy: () => void; edit: () => void; remove: () => void; manageKeys: () => void; diagnose: () => void; restoreSync: () => void }) {
  const cooling = isCooling(channel)
  const activeModels = channel.models.filter((model) => !model.disabled)
  const protocols = Array.from(new Set(channel.urls.flatMap((url) => url.protocols?.length ? url.protocols : ['auto'])))
  return (
    <article className={`channel-row${selected ? ' row-selected' : ''}`}>
      <div className="channel-identity">
        <input className="row-selector" type="checkbox" checked={selected} onChange={select} aria-label={`选择 ${channel.name}`} />
        <span className={`status-dot ${channel.enabled ? cooling ? 'status-dot--warning' : 'status-dot--success' : 'status-dot--muted'}`} />
        <div><div className="channel-name"><strong title={channel.name}>{channel.name}</strong><small>#{channel.id}</small>{channel.site_sync_ownership === 'manual' && <span className="site-sync-badge">手动接管</span>}</div>{channel.auth_type === 'api_key' ? <button type="button" className={`channel-key-health-link${channel.key_health_issue_count ? ' has-issues' : ''}`} onClick={manageKeys} aria-label={`管理 ${channel.name} 的 Key`} title="查看每个 Key 的状态、复检或清理">{channel.key_count} Keys · 健康管理{Boolean(channel.key_health_issue_count) && <em>{channel.key_health_issue_count} 需关注</em>}</button> : <span>{channel.auth_type}</span>}</div>
      </div>
      <div className="channel-endpoints">{channel.urls[0]?.url ? <a className="site-base-link" href={channel.urls[0].url} target="_blank" rel="noreferrer" title={`在新标签页打开 ${channel.urls[0].url}`}><strong>{channel.urls[0].url}</strong></a> : <strong>未配置 URL</strong>}<span title={channel.urls.map((item) => item.url).join('\n')}>{channel.urls.length} URL · {protocols.join(' / ')}</span></div>
      <div className="channel-routing"><span title="基础优先级越大越优先；相同优先级按有效 Key 数量平滑轮询">优先级 <strong>{channel.priority}</strong></span>{channel.effective_priority !== undefined && <span title="健康度排序使用的有效优先级；失败率或首字延迟可能使它低于基础优先级">有效 <strong>{channel.effective_priority.toFixed(1)}</strong></span>}<span title={channel.auth_type === 'api_key' ? `实际费用按最终选中的 Key 倍率计算；渠道默认倍率为 ${channel.cost_multiplier ?? 1}x` : '实际费用按渠道倍率计算'}>倍率 <strong>{channel.auth_type === 'api_key' ? '按 Key' : `${channel.cost_multiplier ?? 1}x`}</strong></span>{channel.success_rate !== undefined && <span title="统计窗口内的上游成功率">成功率 <strong>{Math.round(channel.success_rate * 100)}%</strong></span>}<small>{protocolMode(channel.protocol_transform_mode)}</small>{channel.available_time_start && channel.available_time_end && <small title="窗口外不会参与路由或定时巡检">时段 {channel.available_time_start}–{channel.available_time_end}</small>}</div>
      <div className="channel-models"><strong>{activeModels.length} 模型</strong><span title={activeModels.map((item) => item.model).join(', ')}>{activeModels.slice(0, 2).map((item) => item.model).join(' · ') || '未配置'}</span></div>
      <div className="channel-limits"><span>RPM {channel.rpm_limit || '不限'}</span><span>并发 {channel.max_concurrency || '不限'}</span>{channel.auth_type === 'api_key' && <span title="有效 Key 会排除禁用或处于冷却中的 Key">Key {channel.effective_key_count ?? channel.key_count}/{channel.key_count}</span>}{cooling && <small className="text-warning" title={cooldownSummary(channel)}>存在冷却</small>}</div>
      <div className="row-actions">
        <a className="icon-button icon-button--surface" href={`#/models?channel=${channel.id}&model=${encodeURIComponent(activeModels[0]?.model || '')}&view=probe`} aria-label={`测试 ${channel.name}`} title="模型测试"><FlaskConical size={16} /></a>
        <button className="icon-button icon-button--surface" type="button" onClick={diagnose} aria-label={`诊断 ${channel.name} 的路由`} title="路由诊断：查看候选池、策略和排除原因"><Route size={16} /></button>
        {channel.site_sync_ownership === 'manual' && <button className="icon-button icon-button--surface" type="button" onClick={restoreSync} disabled={busy} aria-label={`恢复 ${channel.name} 自动同步`} title="恢复自动同步"><RefreshCw size={16} /></button>}
        <button className={`icon-button icon-button--surface ${channel.enabled ? 'is-on' : ''}`} type="button" onClick={toggle} disabled={busy} aria-label={channel.enabled ? `停用 ${channel.name}` : `启用 ${channel.name}`} title={channel.enabled ? '停用渠道' : '启用渠道'}><Power className={busy ? 'spin' : ''} size={16} /></button>
        <button className="icon-button icon-button--surface" type="button" onClick={copy} disabled={busy} aria-label={`复制 ${channel.name}`} title="复制渠道"><Copy size={16} /></button>
        <button className="icon-button icon-button--surface" type="button" onClick={edit} aria-label={`编辑 ${channel.name}`} title="编辑"><Pencil size={16} /></button>
        <button className="icon-button icon-button--surface danger-button" type="button" onClick={remove} disabled={busy} aria-label={`删除 ${channel.name}`} title="删除"><Trash2 size={16} /></button>
      </div>
    </article>
  )
}

interface EditorForm {
  name: string; authType: string; urls: string; models: ChannelModel[]; keys: string; keyStrategy: string
  priority: number; rpmLimit: number; maxConcurrency: number; costMultiplier: number | ''; dailyCostLimit: number
  protocolMode: string; proxyURL: string; enabled: boolean; websockets: boolean; retryOtherKeys: boolean
  availableTimeStart: string; availableTimeEnd: string; requestRules: RequestRulesForm; modelDiscoveryProtocol: string
}

interface KeyDraft {
  api_key: string
  note: string
  allowed_models: string[]
  model_scope_empty: boolean
  cost_multiplier: number | ''
}

function blankKeyDraft(defaultMultiplier: number | ''): KeyDraft {
  return {
    api_key: '', note: '', allowed_models: [], model_scope_empty: false,
    cost_multiplier: typeof defaultMultiplier === 'number' && Number.isFinite(defaultMultiplier) && defaultMultiplier >= 0 ? defaultMultiplier : 1,
  }
}

function requiredMultiplier(value: number | '', label: string): number {
  if (value === '' || !Number.isFinite(value) || value < 0) throw new Error(`${label}必须填写大于等于 0 的数字`)
  return value
}

interface HeaderRuleForm {
  action: ChannelHeaderRuleAction
  name: string
  value: string
}

interface BodyRuleForm {
  action: ChannelBodyRuleAction
  path: string
  value: string
}

interface RequestRulesForm {
  headers: HeaderRuleForm[]
  body: BodyRuleForm[]
}

const maxCustomRuleEntries = 32

function emptyRequestRules(): RequestRulesForm {
  return { headers: [], body: [] }
}

const blankEditor: EditorForm = {
  name: '', authType: 'api_key', urls: '', models: [], keys: '', keyStrategy: 'sequential', priority: 0,
  rpmLimit: 0, maxConcurrency: 0, costMultiplier: 1, dailyCostLimit: 0, protocolMode: 'auto', proxyURL: '',
  enabled: true, websockets: false, retryOtherKeys: false, availableTimeStart: '', availableTimeEnd: '', requestRules: emptyRequestRules(),
  modelDiscoveryProtocol: 'auto',
}

function OAuthUsagePanel({ usage, loading, error, refresh }: { usage: OAuthUsageSummary | null; loading: boolean; error: string; refresh: () => void }) {
  return <section className="oauth-usage-panel">
    <header><div><strong>OAuth 额度</strong><span>{usage ? `${oauthProviderLabel(usage.provider)}${usage.plan_type ? ` · ${usage.plan_type}` : ''}` : '读取当前账号的上游额度窗口'}</span></div><button className="text-button" type="button" onClick={refresh} disabled={loading}>{loading ? <RefreshCw className="spin" size={14} /> : <RefreshCw size={14} />}{loading ? '读取中' : '刷新额度'}</button></header>
    {loading && !usage ? <div className="oauth-usage-empty">正在向上游读取额度...</div> : error ? <div className="oauth-usage-error"><span>{error}</span><button className="text-button" type="button" onClick={refresh}>重试</button></div> : usage?.windows.length ? <div className="oauth-usage-list">{usage.windows.map((window, index) => {
      const used = Math.min(100, Math.max(0, window.used_percent))
      const remaining = Math.min(100, Math.max(0, window.remaining_percent))
      const level = used >= 90 ? 'critical' : used >= 70 ? 'warning' : 'normal'
      return <div className={`oauth-usage-row oauth-usage-row--${level}`} key={`${window.limit_name}-${window.kind}-${index}`}><div className="oauth-usage-row-head"><strong>{window.limit_name || '额度窗口'}{window.kind ? ` · ${window.kind}` : ''}</strong><span><b>{Math.round(remaining)}%</b> 剩余</span></div><div className="oauth-usage-track" role="progressbar" aria-label={`${window.limit_name || '额度窗口'}已使用额度`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(used)}><i style={{ width: `${used}%` }} /></div><small><b>{Math.round(used)}% 已使用</b>{window.limit_window_seconds ? ` · 窗口 ${formatQuotaWindow(window.limit_window_seconds)}` : ' · 窗口时长未知'}{window.reset_at ? ` · 重置于 ${new Date(window.reset_at * 1000).toLocaleString()}` : ' · 重置时间未知'}</small></div>
    })}</div> : <div className="oauth-usage-empty">上游暂未返回可展示的额度窗口。</div>}
    {loading && usage && <div className="oauth-usage-refreshing"><RefreshCw className="spin" size={13} />正在刷新，当前数据仍可继续参考</div>}
  </section>
}

function oauthProviderLabel(provider: string): string {
  if (provider === 'codex' || provider === 'codex_oauth') return 'Codex OAuth'
  if (provider === 'antigravity' || provider === 'antigravity_oauth') return 'Antigravity OAuth'
  return provider || 'OAuth'
}

function formatQuotaWindow(seconds: number): string {
  if (seconds >= 24 * 60 * 60) return `${Math.round(seconds / (24 * 60 * 60))} 天`
  if (seconds >= 60 * 60) return `${Math.round(seconds / (60 * 60))} 小时`
  return `${Math.max(1, Math.round(seconds / 60))} 分钟`
}

type KeyScopeMode = 'all' | 'restricted' | 'paused'

function KeyRowsEditor({ rows, models, defaultMultiplier, onChange }: { rows: KeyDraft[]; models: ChannelModel[]; defaultMultiplier: number | ''; onChange: (rows: KeyDraft[]) => void }) {
  const availableModels = models.filter((item) => item.model.trim() && !item.disabled)
  const availableModelNames = new Set(availableModels.map((item) => item.model.trim().toLowerCase()))
  const update = (index: number, patch: Partial<KeyDraft>) => onChange(rows.map((row, rowIndex) => rowIndex === index ? { ...row, ...patch } : row))
  const remove = (index: number) => onChange(rows.filter((_, rowIndex) => rowIndex !== index))
  const add = () => onChange([...rows, blankKeyDraft(defaultMultiplier)])
  const setScopeMode = (index: number, row: KeyDraft, mode: KeyScopeMode) => {
    if (mode === 'all') { update(index, { allowed_models: [], model_scope_empty: false }); return }
    if (mode === 'paused') { update(index, { model_scope_empty: true }); return }
    const retained = row.allowed_models.filter((model) => availableModelNames.has(model.trim().toLowerCase()))
    update(index, { model_scope_empty: false, allowed_models: retained.length ? retained : availableModels[0] ? [availableModels[0].model] : [] })
  }
  const toggleScopeModel = (index: number, row: KeyDraft, model: string) => {
    const normalized = model.trim().toLowerCase()
    const selected = row.allowed_models.some((item) => item.trim().toLowerCase() === normalized)
    if (selected && row.allowed_models.length === 1) return
    update(index, { allowed_models: selected ? row.allowed_models.filter((item) => item.trim().toLowerCase() !== normalized) : [...row.allowed_models, model] })
  }

  return <section className="key-editor-section">
    <header className="key-editor-header"><div><strong>渠道 API Keys</strong><span>{rows.length ? `${rows.length} 把 Key · 可单独限制模型和成本` : '还没有添加 Key'}</span></div><button className="text-button" type="button" onClick={add}><Plus size={14} />添加 Key</button></header>
    <div className="key-editor-help">模型范围留空表示允许该渠道的全部模型；每把 Key 独立计费，新 Key 默认继承渠道默认倍率，0 表示免费。</div>
    {rows.length ? <div className="key-editor-list">{rows.map((row, index) => {
      const restricted = row.allowed_models.length > 0
      const paused = row.model_scope_empty
      const scopeMode: KeyScopeMode = paused ? 'paused' : restricted ? 'restricted' : 'all'
      const unavailableModels = row.allowed_models.filter((model) => model !== '*' && !availableModelNames.has(model.trim().toLowerCase()))
      return <article className="key-editor-row" key={index}>
        <div className="key-editor-row-head"><span className="key-editor-index">KEY {String(index + 1).padStart(2, '0')}</span><button className="icon-button icon-button--surface danger-button" type="button" onClick={() => remove(index)} aria-label={`删除第 ${index + 1} 个 Key`} title="删除 Key"><Trash2 size={15} /></button></div>
        <div className="key-editor-fields">
          <label>API Key<input required value={row.api_key} onChange={(event) => update(index, { api_key: event.target.value })} placeholder="sk-..." autoComplete="off" /></label>
          <label>备注<input value={row.note} onChange={(event) => update(index, { note: event.target.value })} placeholder="例如：主账号 / 备用" /></label>
          <label>Key 成本倍率<input required type="number" min="0" step="0.01" value={row.cost_multiplier} onChange={(event) => update(index, { cost_multiplier: event.target.value === '' ? '' : Number(event.target.value) })} aria-describedby={`key-multiplier-help-${index}`} /><small id={`key-multiplier-help-${index}`}>{row.cost_multiplier === 0 ? '免费，不计入有效费用' : '覆盖渠道默认倍率'}</small></label>
        </div>
        <div className="key-editor-scope">
          <div className="key-scope-mode" role="radiogroup" aria-label={`第 ${index + 1} 个 Key 的路由范围`}>
            <label><input type="radio" name={`key-scope-${index}`} checked={scopeMode === 'all'} onChange={() => setScopeMode(index, row, 'all')} /><span>全部模型</span></label>
            <label title={availableModels.length ? '只让这把 Key 服务选中的渠道模型' : '请先在下方勾选渠道模型'}><input type="radio" name={`key-scope-${index}`} checked={scopeMode === 'restricted'} disabled={!availableModels.length} onChange={() => setScopeMode(index, row, 'restricted')} /><span>指定模型</span></label>
            <label><input type="radio" name={`key-scope-${index}`} checked={scopeMode === 'paused'} onChange={() => setScopeMode(index, row, 'paused')} /><span>暂停路由</span></label>
          </div>
          {scopeMode === 'all' && <span className="key-scope-summary">可服务渠道内全部模型</span>}
          {scopeMode === 'paused' && <span className="key-scope-summary key-scope-summary--paused">不会参与任何请求{restricted ? ` · 恢复后仍限定 ${row.allowed_models.length} 个模型` : ''}</span>}
          {scopeMode === 'restricted' && <div className="key-model-scope" role="group" aria-label={`第 ${index + 1} 个 Key 的模型范围`}>{availableModels.map((item) => {
            const checked = row.allowed_models.some((model) => model.trim().toLowerCase() === item.model.trim().toLowerCase())
            return <label className={checked ? 'is-selected' : undefined} key={item.model}><input type="checkbox" checked={checked} disabled={checked && row.allowed_models.length === 1} onChange={() => toggleScopeModel(index, row, item.model)} /><span>{item.model}</span></label>
          })}</div>}
          {unavailableModels.length > 0 && <p className="key-scope-warning" role="alert">范围中有 {unavailableModels.length} 个模型未在下方选中：{unavailableModels.join('、')}。请重新勾选渠道模型，或将此 Key 改为“全部模型”。</p>}
        </div>
      </article>
    })}</div> : <button className="key-editor-empty" type="button" onClick={add}><Plus size={16} /><span><strong>添加第一把 Key</strong><small>每把 Key 都可以单独设置模型范围和倍率</small></span></button>}
  </section>
}

function ChannelEditor({ channelId, close, saved }: { channelId?: number; close: () => void; saved: () => void }) {
  const [snapshot, setSnapshot] = useState<ChannelEditorSnapshot | null>(null)
  const [form, setForm] = useState<EditorForm>(blankEditor)
  const [keyRows, setKeyRows] = useState<KeyDraft[]>([])
  const [loading, setLoading] = useState(Boolean(channelId))
  const [saving, setSaving] = useState(false)
  const [discovering, setDiscovering] = useState(false)
  const [oauthUsage, setOAuthUsage] = useState<OAuthUsageSummary | null>(null)
  const [oauthUsageLoading, setOAuthUsageLoading] = useState(false)
  const [oauthUsageError, setOAuthUsageError] = useState('')
  const [selectedModelIndexes, setSelectedModelIndexes] = useState<Set<number>>(new Set())
  const [error, setError] = useState('')

  useEffect(() => {
    if (!channelId) return
    const controller = new AbortController()
    void getChannelEditor(channelId, controller.signal).then((data) => {
      setSnapshot(data)
      setKeyRows(data.keys.map((item) => ({
        api_key: item.api_key,
        note: item.note || '',
        allowed_models: item.allowed_models || [],
        model_scope_empty: Boolean(item.model_scope_empty),
        cost_multiplier: item.cost_multiplier ?? 1,
      })))
      setSelectedModelIndexes(new Set(data.channel.models.map((_, index) => index)))
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
        availableTimeStart: data.channel.available_time_start || '',
        availableTimeEnd: data.channel.available_time_end || '',
        requestRules: requestRulesToForm(data.channel.custom_request_rules),
        modelDiscoveryProtocol: 'auto',
      })
      setOAuthUsage(null)
      setOAuthUsageError('')
      if (data.channel.auth_type !== 'api_key') {
        setOAuthUsageLoading(true)
        void fetchOAuthUsage(data.channel.id, controller.signal).then((usage) => {
          if (!controller.signal.aborted) setOAuthUsage(usage)
        }).catch((reason) => {
          if (!controller.signal.aborted) setOAuthUsageError(reason instanceof Error ? reason.message : '额度读取失败')
        }).finally(() => {
          if (!controller.signal.aborted) setOAuthUsageLoading(false)
        })
      } else {
        setOAuthUsageLoading(false)
      }
    }).catch((reason) => { if (!controller.signal.aborted) setError(reason instanceof Error ? reason.message : '渠道详情加载失败') }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [channelId])

  const refreshOAuthUsage = async () => {
    if (!channelId || form.authType === 'api_key') return
    setOAuthUsageLoading(true); setOAuthUsageError('')
    try { setOAuthUsage(await fetchOAuthUsage(channelId)) } catch (reason) { setOAuthUsageError(reason instanceof Error ? reason.message : '额度读取失败') }
    finally { setOAuthUsageLoading(false) }
  }

  useEffect(() => {
    setSelectedModelIndexes((current) => new Set([...current].filter((index) => index >= 0 && index < form.models.length)))
  }, [form.models.length])

  const submit = async (event: React.FormEvent) => {
    event.preventDefault(); setSaving(true); setError('')
    try {
      const models = normalizeEditorModels(form.models.filter((_, index) => selectedModelIndexes.has(index)))
      const selectedModelNames = new Set(models.map((item) => item.model.toLowerCase()))
      const scheduledCheckModel = snapshot?.channel.scheduled_check_model?.trim() || ''
      const customRequestRules = requestRulesToPayload(form.requestRules)
      const apiKeys = form.authType === 'api_key' ? keyRows.filter((item) => item.api_key.trim()).map((item, index) => ({
        api_key: item.api_key.trim(), note: item.note.trim(), allowed_models: item.allowed_models,
        model_scope_empty: item.model_scope_empty, cost_multiplier: requiredMultiplier(item.cost_multiplier, `第 ${index + 1} 把 Key 的成本倍率`),
      })) : []
      for (const [index, key] of apiKeys.entries()) {
        const unavailableModels = key.allowed_models.filter((model) => model !== '*' && !selectedModelNames.has(model.trim().toLowerCase()))
        if (unavailableModels.length) throw new Error(`第 ${index + 1} 把 Key 的模型范围包含未选中的渠道模型：${unavailableModels.join('、')}`)
      }
      const payload: ChannelMutation = {
        name: form.name.trim(), auth_type: form.authType,
        urls: parseURLs(form.urls), models, api_keys: apiKeys,
        ...(form.authType === 'api_key' ? { key_strategy: form.keyStrategy } : {}),
        priority: Number(form.priority) || 0, rpm_limit: Number(form.rpmLimit) || 0,
        max_concurrency: Number(form.maxConcurrency) || 0, enabled: form.enabled, websockets: form.websockets,
        protocol_transform_mode: form.protocolMode, scheduled_check_enabled: snapshot?.channel.scheduled_check_enabled || false,
        scheduled_check_model: selectedModelNames.has(scheduledCheckModel.toLowerCase()) ? scheduledCheckModel : '', daily_cost_limit: Number(form.dailyCostLimit) || 0,
        cost_multiplier: requiredMultiplier(form.costMultiplier, '渠道默认成本倍率'), proxy_url: form.proxyURL.trim(), retry_other_keys_on_failure: form.retryOtherKeys,
        available_time_start: form.availableTimeStart, available_time_end: form.availableTimeEnd,
        custom_request_rules: customRequestRules, cooldown_detection_rules: snapshot?.channel.cooldown_detection_rules,
      }
      if ((payload.available_time_start && !payload.available_time_end) || (!payload.available_time_start && payload.available_time_end)) throw new Error('可用时段需要同时填写开始和结束时间')
      if (!payload.urls.length) throw new Error('至少填写一个上游 URL')
      if (!payload.models.some((item) => item.model && !item.disabled)) throw new Error('至少勾选一个启用模型')
      if (form.authType === 'api_key' && !payload.api_keys.length) throw new Error('至少填写一个 API Key')
      if (channelId) await updateChannel(channelId, payload); else await createChannel(payload)
      saved()
    } catch (reason) { setError(reason instanceof Error ? reason.message : '渠道保存失败') }
    finally { setSaving(false) }
  }

  const discoverModels = async () => {
    const urls = parseURLs(form.urls)
    const keys = keyRows.map((item) => item.api_key.trim()).filter(Boolean)
    if (!urls.length || !keys.length) { setError('请先填写上游 URL 和 API Key'); return }
    setDiscovering(true); setError('')
    try {
      const protocol = form.modelDiscoveryProtocol === 'auto' ? undefined : form.modelDiscoveryProtocol
      const result = await fetchChannelModelsPreview({ urls, api_keys: keys, protocol })
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
        <label>渠道默认倍率<input required type="number" min="0" step="0.01" value={form.costMultiplier} onChange={(event) => setForm({ ...form, costMultiplier: event.target.value === '' ? '' : Number(event.target.value) })} /><small>{form.authType === 'api_key' ? '新 Key 的初始值，实际费用按 Key 计算' : form.costMultiplier === 0 ? '免费，不计入有效费用' : '用于该 OAuth 渠道的有效费用'}</small></label>
        <label>RPM 限制<input type="number" min="0" value={form.rpmLimit} onChange={(event) => setForm({ ...form, rpmLimit: Number(event.target.value) })} /></label>
        <label>最大并发<input type="number" min="0" value={form.maxConcurrency} onChange={(event) => setForm({ ...form, maxConcurrency: Number(event.target.value) })} /></label>
        <label>每日费用限额<input type="number" min="0" step="0.01" value={form.dailyCostLimit} onChange={(event) => setForm({ ...form, dailyCostLimit: Number(event.target.value) })} /></label>
        <label>可用开始<input type="time" value={form.availableTimeStart} onChange={(event) => setForm({ ...form, availableTimeStart: event.target.value })} /></label>
        <label>可用结束<input type="time" value={form.availableTimeEnd} onChange={(event) => setForm({ ...form, availableTimeEnd: event.target.value })} /></label>
        <label>渠道代理<input value={form.proxyURL} onChange={(event) => setForm({ ...form, proxyURL: event.target.value })} placeholder="留空使用环境代理" /></label>
      </div>
      {channelId && form.authType !== 'api_key' && <OAuthUsagePanel usage={oauthUsage} loading={oauthUsageLoading} error={oauthUsageError} refresh={() => void refreshOAuthUsage()} />}
      <div className="form-help">可用时段按服务器本地时间判断；留空表示全天，开始晚于结束时按跨午夜窗口处理。窗口外渠道不会参与路由或定时巡检。</div>
      <label className="textarea-field">上游 URL<textarea required rows={3} value={form.urls} onChange={(event) => setForm({ ...form, urls: event.target.value })} placeholder="每行一个；可写 URL | anthropic, openai" /></label>
      {form.authType === 'api_key' ? <KeyRowsEditor rows={keyRows} models={form.models.filter((_, index) => selectedModelIndexes.has(index))} defaultMultiplier={form.costMultiplier} onChange={setKeyRows} /> : <div className="form-help">OAuth 渠道不在这里手填密钥，请使用顶部"导入凭证"导入 Codex 或 Antigravity 凭证文件。</div>}
      {form.authType === 'api_key' && <div className="model-discovery-action">
        <div>
          <strong>自动获取模型</strong>
          <span>获取结果作为候选模型加入列表；勾选需要保留的模型后再保存渠道。</span>
        </div>
        <div className="model-discovery-controls">
          <label>发现协议<select value={form.modelDiscoveryProtocol} onChange={(event) => setForm({ ...form, modelDiscoveryProtocol: event.target.value })}><option value="auto">自动尝试</option><option value="openai">OpenAI</option><option value="anthropic">Anthropic</option><option value="codex">Codex</option><option value="gemini">Gemini</option></select></label>
          <button className="secondary-button" type="button" onClick={() => void discoverModels()} disabled={discovering || !form.urls.trim() || !keyRows.some((row) => row.api_key.trim())}>{discovering ? <RefreshCw className="spin" size={15} /> : <Sparkles size={15} />}{discovering ? '获取中' : '获取模型'}</button>
        </div>
      </div>}
      <EditableModelList models={form.models} selected={selectedModelIndexes} onSelectionChange={setSelectedModelIndexes} onChange={(models) => setForm((current) => ({ ...current, models }))} />
      <CustomRequestRulesEditor value={form.requestRules} onChange={(requestRules) => setForm((current) => ({ ...current, requestRules }))} />
      <div className="checkbox-grid"><label className="checkbox-field"><input type="checkbox" checked={form.enabled} onChange={(event) => setForm({ ...form, enabled: event.target.checked })} />启用渠道</label><label className="checkbox-field"><input type="checkbox" checked={form.websockets} onChange={(event) => setForm({ ...form, websockets: event.target.checked })} />WebSocket</label><label className="checkbox-field"><input type="checkbox" checked={form.retryOtherKeys} onChange={(event) => setForm({ ...form, retryOtherKeys: event.target.checked })} />失败时尝试其他 Key</label></div>
      <footer><button className="secondary-button" type="button" onClick={close}>取消</button><button className="primary-button" type="submit" disabled={saving}>{saving ? <RefreshCw className="spin" size={15} /> : null}{saving ? '保存中' : '保存渠道'}</button></footer>
    </form>}
  </Modal>
}

function CustomRequestRulesEditor({ value, onChange }: { value: RequestRulesForm; onChange: (value: RequestRulesForm) => void }) {
  const ruleCount = value.headers.length + value.body.length
  const updateHeader = (index: number, patch: Partial<HeaderRuleForm>) => onChange({ ...value, headers: value.headers.map((rule, itemIndex) => itemIndex === index ? { ...rule, ...patch } : rule) })
  const updateBody = (index: number, patch: Partial<BodyRuleForm>) => onChange({ ...value, body: value.body.map((rule, itemIndex) => itemIndex === index ? { ...rule, ...patch } : rule) })

  return <details className="advanced-request-settings">
    <summary><span className="advanced-request-summary"><Settings2 size={16} /><span><strong>高级请求设置</strong><small>请求头覆盖、参数覆盖与安全透传</small></span></span><span className="advanced-request-count">{ruleCount ? `${ruleCount} 条规则` : '未配置'}</span></summary>
    <div className="advanced-request-content">
      <p className="form-help">低频高级选项，默认不改变请求。普通请求头会按安全策略透传；协议转换选择"上游原生"可进行协议级透传。认证头由系统托管，不能通过这里覆盖。</p>
      <section className="advanced-rule-group">
        <header><div><strong>请求头规则</strong><small>按顺序删除、覆盖或追加上游请求头，最多 {maxCustomRuleEntries} 条</small></div><button className="text-button" type="button" disabled={value.headers.length >= maxCustomRuleEntries} title={value.headers.length >= maxCustomRuleEntries ? `最多添加 ${maxCustomRuleEntries} 条请求头规则` : '添加请求头规则'} onClick={() => onChange({ ...value, headers: [...value.headers, { action: 'override', name: '', value: '' }] })}><Plus size={14} />添加请求头</button></header>
        {value.headers.length === 0 ? <div className="advanced-rule-empty">未配置请求头规则</div> : <div className="advanced-rule-list">{value.headers.map((rule, index) => <div className="advanced-rule-row" key={`header-${index}`}>
          <select value={rule.action} onChange={(event) => updateHeader(index, { action: event.target.value as ChannelHeaderRuleAction })} aria-label={`第 ${index + 1} 条请求头动作`}><option value="override">覆盖</option><option value="append">追加</option><option value="remove">删除</option></select>
          <input value={rule.name} onChange={(event) => updateHeader(index, { name: event.target.value })} placeholder="请求头名称，例如 X-Api-Version" aria-label={`第 ${index + 1} 条请求头名称`} />
          <input value={rule.value} onChange={(event) => updateHeader(index, { value: event.target.value })} placeholder={rule.action === 'remove' ? '留空删除整个请求头；填写值可删除逗号分隔项' : '请求头值'} aria-label={`第 ${index + 1} 条请求头值`} />
          <button className="icon-button icon-button--surface danger-button" type="button" onClick={() => onChange({ ...value, headers: value.headers.filter((_, itemIndex) => itemIndex !== index) })} aria-label={`删除第 ${index + 1} 条请求头规则`} title="删除规则"><Trash2 size={15} /></button>
        </div>)}</div>}
      </section>
      <section className="advanced-rule-group">
        <header><div><strong>JSON 参数规则</strong><small>使用点号路径覆盖或删除请求体字段，最多 {maxCustomRuleEntries} 条</small></div><button className="text-button" type="button" disabled={value.body.length >= maxCustomRuleEntries} title={value.body.length >= maxCustomRuleEntries ? `最多添加 ${maxCustomRuleEntries} 条参数规则` : '添加 JSON 参数规则'} onClick={() => onChange({ ...value, body: [...value.body, { action: 'override', path: '', value: '' }] })}><Plus size={14} />添加参数</button></header>
        {value.body.length === 0 ? <div className="advanced-rule-empty">未配置参数规则</div> : <div className="advanced-rule-list">{value.body.map((rule, index) => <div className="advanced-rule-row advanced-rule-row--body" key={`body-${index}`}>
          <select value={rule.action} onChange={(event) => updateBody(index, { action: event.target.value as ChannelBodyRuleAction, ...(event.target.value === 'remove' ? { value: '' } : {}) })} aria-label={`第 ${index + 1} 条参数动作`}><option value="override">覆盖</option><option value="remove">删除</option></select>
          <input value={rule.path} onChange={(event) => updateBody(index, { path: event.target.value })} placeholder="参数路径，例如 thinking.budget_tokens" aria-label={`第 ${index + 1} 条参数路径`} />
          <textarea rows={1} value={rule.value} disabled={rule.action === 'remove'} onChange={(event) => updateBody(index, { value: event.target.value })} placeholder={'JSON 值，例如 "text"、8192 或 {"enabled":true}'} aria-label={`第 ${index + 1} 条参数值`} />
          <button className="icon-button icon-button--surface danger-button" type="button" onClick={() => onChange({ ...value, body: value.body.filter((_, itemIndex) => itemIndex !== index) })} aria-label={`删除第 ${index + 1} 条参数规则`} title="删除规则"><Trash2 size={15} /></button>
        </div>)}</div>}
      </section>
    </div>
  </details>
}

function requestRulesToForm(raw?: ChannelRequestRules): RequestRulesForm {
  if (!raw) return emptyRequestRules()
  return {
    headers: Array.isArray(raw.headers) ? raw.headers.map((rule) => ({ action: rule.action, name: rule.name || '', value: rule.value || '' })) : [],
    body: Array.isArray(raw.body) ? raw.body.map((rule) => ({ action: rule.action, path: rule.path || '', value: rule.action === 'remove' ? '' : jsonEditorValue(rule.value) })) : [],
  }
}

function jsonEditorValue(value: unknown): string {
  if (typeof value === 'undefined') return ''
  try { return JSON.stringify(value) } catch { return '' }
}

function requestRulesToPayload(form: RequestRulesForm): ChannelRequestRules | undefined {
  if (form.headers.length > maxCustomRuleEntries) throw new Error(`请求头规则最多 ${maxCustomRuleEntries} 条`)
  if (form.body.length > maxCustomRuleEntries) throw new Error(`参数规则最多 ${maxCustomRuleEntries} 条`)
  const headers = form.headers.map((rule, index) => {
    const name = rule.name.trim()
    if (!name) throw new Error(`请求头规则 ${index + 1} 的名称不能为空`)
    return { action: rule.action, name, ...(rule.value ? { value: rule.value } : {}) }
  })
  const body = form.body.map((rule, index) => {
    const path = rule.path.trim()
    if (!path) throw new Error(`参数规则 ${index + 1} 的路径不能为空`)
    if (rule.action === 'remove') return { action: rule.action, path }
    const raw = rule.value.trim()
    if (!raw) throw new Error(`参数规则 ${index + 1} 需要填写 JSON 值`)
    let parsed: unknown
    try { parsed = JSON.parse(raw) } catch { throw new Error(`参数规则 ${index + 1} 的值不是有效 JSON`) }
    return { action: rule.action, path, value: parsed }
  })
  if (!headers.length && !body.length) return undefined
  return { ...(headers.length ? { headers } : {}), ...(body.length ? { body } : {}) }
}

function EditableModelList({ models, selected, onSelectionChange, onChange }: { models: ChannelModel[]; selected: Set<number>; onSelectionChange: (selected: Set<number>) => void; onChange: (models: ChannelModel[]) => void }) {
  const [query, setQuery] = useState('')
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
    onSelectionChange(new Set([...selected].filter((itemIndex) => itemIndex !== index).map((itemIndex) => itemIndex > index ? itemIndex - 1 : itemIndex)))
  }
  const removeUnselected = () => {
    if (selected.size === models.length) return
    onChange(models.filter((_, index) => selected.has(index)))
    onSelectionChange(new Set(Array.from({ length: selected.size }, (_, index) => index)))
  }
  const add = () => {
    setQuery('')
    onChange([...models, { model: '', redirect_model: '' }])
    onSelectionChange(new Set(selected).add(models.length))
  }
  const allVisibleSelected = visibleIndexes.length > 0 && visibleIndexes.every((index) => selected.has(index))
  const toggleVisible = () => {
    const next = new Set(selected)
    if (allVisibleSelected) visibleIndexes.forEach((index) => next.delete(index))
    else visibleIndexes.forEach((index) => next.add(index))
    onSelectionChange(next)
  }
  const unselectedCount = models.length - selected.size
  return <section className="selection-panel discovered-model-panel">
    <header><div><strong>渠道模型</strong><span>{models.length ? `${models.length} 个候选，已勾选 ${selected.size} 个；保存时只提交勾选模型` : '尚未配置模型'}</span></div><div className="model-list-actions"><button className="text-button" type="button" onClick={toggleVisible} disabled={!visibleIndexes.length}>{allVisibleSelected ? '取消当前结果' : '全选搜索结果'}</button>{selected.size > 0 && <button className="text-button" type="button" onClick={() => onSelectionChange(new Set())}>清空选择</button>}{unselectedCount > 0 && <button className="text-button danger-text-button" type="button" onClick={removeUnselected}><Trash2 size={14} />移除未选 ({unselectedCount})</button>}<button className="text-button" type="button" onClick={add}><Plus size={14} />添加模型</button></div></header>
    {models.length ? <><div className="model-selection-toolbar"><label className="search-field selection-search"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索模型或映射名称" aria-label="搜索渠道模型" /><span>{visibleIndexes.length}/{models.length}</span></label><small>已跨搜索保留 {selected.size} 个勾选</small></div>{visibleIndexes.length ? <div className="editable-model-list">{visibleIndexes.map((index) => { const item = models[index]; return <div className="editable-model-row" key={index}><input type="checkbox" checked={selected.has(index)} onChange={() => { const next = new Set(selected); if (next.has(index)) next.delete(index); else next.add(index); onSelectionChange(next) }} aria-label={`保存模型 ${item.model || index + 1}`} /><input value={item.model} onChange={(event) => update(index, { model: event.target.value })} placeholder="对外模型名" aria-label={`第 ${index + 1} 个对外模型`} /><span aria-hidden="true">→</span><input value={item.redirect_model || ''} onChange={(event) => update(index, { redirect_model: event.target.value })} placeholder="映射到上游模型（可选）" aria-label={`第 ${index + 1} 个上游映射`} /><button className="icon-button icon-button--surface danger-button" type="button" onClick={() => remove(index)} aria-label={`删除模型 ${item.model || index + 1}`} title="删除模型"><Trash2 size={15} /></button></div> })}</div> : <span className="selection-empty">没有匹配"{query.trim()}"的模型，已勾选的其他模型仍会保留。</span>}</> : <span className="selection-empty">填写 URL 和 API Key 后点击"获取模型"，也可以直接添加模型。</span>}
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
    api_keys: snapshot.keys.map((item) => ({ api_key: item.api_key, note: item.note, allowed_models: item.allowed_models, model_scope_empty: item.model_scope_empty, cost_multiplier: item.cost_multiplier })),
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
    available_time_start: channel.available_time_start || '',
    available_time_end: channel.available_time_end || '',
    daily_cost_limit: channel.daily_cost_limit || 0,
    cost_multiplier: channel.cost_multiplier ?? 1,
    proxy_url: channel.proxy_url || '',
    retry_other_keys_on_failure: Boolean(channel.retry_other_keys_on_failure),
    custom_request_rules: channel.custom_request_rules,
    cooldown_detection_rules: channel.cooldown_detection_rules,
  }
}
