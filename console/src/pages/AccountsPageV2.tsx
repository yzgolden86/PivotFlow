import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { CheckCircle2, Copy, KeyRound, Pencil, Plus, Power, RefreshCw, Search, Trash2, WalletCards } from 'lucide-react'
import { useLocation } from 'react-router-dom'
import {
  createSiteAccount,
  deleteSiteAccount,
  getCheckinAttempts,
  getSiteInventory,
  runAccountTask as executeAccountTask,
  updateSiteAccount,
  verifySiteAccountCredential,
} from '../api'
import type { CheckinAttempt, Site, SiteAccount, SiteCredentialVerification } from '../types'
import { EmptyState, ErrorState, formatTime, LoadingState, Pagination } from './shared'
import { formatAccountBalance, Modal, siteErrorMessage, StatusBadge } from './siteShared'
import { credentialLabel, credentialOptions, normalizeCredentialType, platformSupportsCheckin, type CredentialType } from '../siteCredentials'

type CredentialForm = {
  credential_type: CredentialType
  credential: string
  username: string
  password: string
  user_id: number
  refresh_token: string
  expires_at: string
}

type CreateAccountForm = CredentialForm & {
  site_id: number
  label: string
  timezone: string
  enabled: boolean
  auto_checkin: boolean
  auto_refresh: boolean
}

type MetadataForm = {
  label: string
  timezone: string
  enabled: boolean
  auto_checkin: boolean
  auto_refresh: boolean
}

type AccountTaskKind = 'checkin' | 'refresh' | 'model_refresh' | 'update' | 'delete'

const emptyCredential = (type: CredentialType = 'username_password'): CredentialForm => ({
  credential_type: type,
  credential: '',
  username: '',
  password: '',
  user_id: 0,
  refresh_token: '',
  expires_at: '',
})

const emptyAccount = (siteId = 0): CreateAccountForm => ({
  ...emptyCredential(),
  site_id: siteId,
  label: '',
  timezone: '',
  enabled: true,
  auto_checkin: true,
  auto_refresh: true,
})

export default function AccountsPage() {
  const location = useLocation()
  const query = useMemo(() => new URLSearchParams(location.search), [location.search])
  const querySearch = query.get('search') || ''
  const querySite = Number(query.get('site_id') || 0)
  const focusAccountId = Number(query.get('focus_account_id') || 0)
  const createIntent = query.get('create') === '1'
  const credentialIntent = query.get('open_credential') === '1'

  const [sites, setSites] = useState<Site[]>([])
  const [accounts, setAccounts] = useState<SiteAccount[]>([])
  const [latestCheckins, setLatestCheckins] = useState<Record<number, CheckinAttempt>>({})
  const [search, setSearch] = useState(querySearch)
  const [siteFilter, setSiteFilter] = useState(querySite)
  const [status, setStatus] = useState('all')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState<Map<number, AccountTaskKind>>(new Map())
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState(false)
  const [openingCreate, setOpeningCreate] = useState(false)
  const [creating, setCreating] = useState(false)
  const [createForm, setCreateForm] = useState<CreateAccountForm>(emptyAccount())
  const [editing, setEditing] = useState<SiteAccount | null>(null)
  const [metadata, setMetadata] = useState<MetadataForm | null>(null)
  const [savingMetadata, setSavingMetadata] = useState(false)
  const [credentialAccount, setCredentialAccount] = useState<SiteAccount | null>(null)
  const [credentialForm, setCredentialForm] = useState<CredentialForm>(emptyCredential())
  const [credentialError, setCredentialError] = useState('')
  const [verification, setVerification] = useState<SiteCredentialVerification | null>(null)
  const [verifying, setVerifying] = useState(false)
  const [savingCredential, setSavingCredential] = useState(false)
  const handledIntent = useRef('')
  const rowRefs = useRef(new Map<number, HTMLElement>())

  const load = useCallback(async (signal?: AbortSignal, options: { silent?: boolean } = {}) => {
    if (!options.silent) setLoading(true)
    setError('')
    try {
      const data = await getSiteInventory(signal)
      setSites(data.sites)
      setAccounts(data.accounts)
      setSelected((current) => new Set([...current].filter((id) => data.accounts.some((account) => account.id === id))))
      const attempts = await Promise.all(data.accounts.map(async (account) => {
        try { return [account.id, (await getCheckinAttempts(account.id, signal))[0]] as const }
        catch { return [account.id, undefined] as const }
      }))
      setLatestCheckins(Object.fromEntries(attempts.filter((entry): entry is readonly [number, CheckinAttempt] => Boolean(entry[1]))))
    } catch (reason) {
      if (!signal?.aborted) setError(siteErrorMessage(reason))
    } finally {
      if (!signal?.aborted && !options.silent) setLoading(false)
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  useEffect(() => {
    setSearch(querySearch)
    setSiteFilter(querySite)
  }, [querySearch, querySite])

  const siteMap = useMemo(() => new Map(sites.map((site) => [site.id, site])), [sites])
  const visible = useMemo(() => accounts.filter((account) => (
    (!siteFilter || account.site_id === siteFilter)
    && (status === 'all' || account.status === status)
    && (!search.trim() || [account.label, siteMap.get(account.site_id)?.name || ''].some((value) => value.toLowerCase().includes(search.trim().toLowerCase())))
  )), [accounts, search, siteFilter, siteMap, status])
  const pagedVisible = useMemo(() => visible.slice((page - 1) * pageSize, page * pageSize), [page, pageSize, visible])

  useEffect(() => { setPage(1) }, [search, siteFilter, status])
  useEffect(() => {
    const pages = Math.max(1, Math.ceil(visible.length / pageSize))
    if (page > pages) setPage(pages)
  }, [page, pageSize, visible.length])

  useEffect(() => {
    if (!focusAccountId) return
    const index = visible.findIndex((account) => account.id === focusAccountId)
    if (index >= 0) setPage(Math.floor(index / pageSize) + 1)
  }, [focusAccountId, pageSize, visible])

  const openCreate = useCallback(async (preferredSiteId = 0) => {
    setOpeningCreate(true)
    setError('')
    try {
      // Always refresh here so a site created moments ago is immediately selectable.
      const data = await getSiteInventory()
      setSites(data.sites)
      setAccounts(data.accounts)
      const selectedSite = data.sites.some((site) => site.id === preferredSiteId)
        ? preferredSiteId
        : data.sites[0]?.id || 0
      const next = emptyAccount(selectedSite)
      const platform = data.sites.find((site) => site.id === selectedSite)?.platform || ''
      next.credential_type = normalizeCredentialType(platform, next.credential_type)
      next.auto_checkin = platformSupportsCheckin(platform)
      setCreateForm(next)
      setCreating(true)
    } catch (reason) {
      setError(siteErrorMessage(reason))
    } finally {
      setOpeningCreate(false)
    }
  }, [])

  const openMetadata = (account: SiteAccount) => {
    setError('')
    setEditing(account)
    setMetadata({
      label: account.label,
      timezone: account.timezone || '',
      enabled: account.enabled,
      auto_checkin: account.auto_checkin,
      auto_refresh: account.auto_refresh,
    })
  }

  const openCredential = useCallback((account: SiteAccount) => {
    setCredentialAccount(account)
    const platform = siteMap.get(account.site_id)?.platform || ''
    const next = emptyCredential(normalizeCredentialType(platform, account.credential_type as CredentialType))
    next.expires_at = account.credential_expires_at ? toLocalInput(account.credential_expires_at) : ''
    setCredentialForm(next)
    setCredentialError('')
    setVerification(null)
  }, [siteMap])

  useEffect(() => {
    if (loading) return
    const intentKey = location.search
    if (handledIntent.current === intentKey) return
    if (createIntent) {
      handledIntent.current = intentKey
      void openCreate(querySite)
      return
    }
    if (focusAccountId) {
      const account = accounts.find((item) => item.id === focusAccountId)
      if (account && credentialIntent) openCredential(account)
      else if (!account) setError(`未找到账号 #${focusAccountId}，请刷新后重试`)
      handledIntent.current = intentKey
    }
  }, [accounts, createIntent, credentialIntent, focusAccountId, loading, location.search, openCreate, openCredential, querySite])

  useEffect(() => {
    if (!focusAccountId || loading) return
    const row = rowRefs.current.get(focusAccountId)
    if (!row) return
    const timer = window.setTimeout(() => row.scrollIntoView({ behavior: 'smooth', block: 'center' }), 80)
    return () => window.clearTimeout(timer)
  }, [focusAccountId, loading, pagedVisible])

  const saveCreate = async (event: React.FormEvent) => {
    event.preventDefault()
    setOpeningCreate(true)
    setError('')
    try {
      const platform = sites.find((site) => site.id === createForm.site_id)?.platform || ''
      await createSiteAccount(createForm.site_id, {
        label: createForm.label,
        credential_type: createForm.credential_type,
        credential: credentialPayload(createForm),
        enabled: createForm.enabled,
        auto_checkin: createForm.credential_type === 'api_key' || !platformSupportsCheckin(platform) ? false : createForm.auto_checkin,
        auto_refresh: createForm.credential_type === 'api_key' ? false : createForm.auto_refresh,
        timezone: createForm.timezone,
      })
      setCreating(false)
      setNotice('账号已添加')
      await load()
    } catch (reason) {
      setError(siteErrorMessage(reason))
    } finally {
      setOpeningCreate(false)
    }
  }

  const saveMetadata = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!editing || !metadata) return
    setSavingMetadata(true)
    setError('')
    try {
      const apiKeyOnly = editing.credential_type === 'api_key'
      await updateSiteAccount(editing.id, {
        ...metadata,
        auto_checkin: apiKeyOnly ? false : metadata.auto_checkin,
        auto_refresh: apiKeyOnly ? false : metadata.auto_refresh,
      })
      setEditing(null)
      setMetadata(null)
      setNotice('账号设置已更新')
      await load()
    } catch (reason) {
      setError(siteErrorMessage(reason))
    } finally {
      setSavingMetadata(false)
    }
  }

  const changeCredential = (key: keyof CredentialForm, value: string | number | boolean) => {
    setCredentialForm((current) => ({ ...current, [key]: value }))
    setVerification(null)
    setCredentialError('')
  }

  const verifyCredential = async () => {
    if (!credentialAccount) return
    setVerifying(true)
    setCredentialError('')
    setVerification(null)
    try {
      const result = await verifySiteAccountCredential(credentialAccount.id, {
        credential_type: credentialForm.credential_type,
        credential: credentialPayload(credentialForm),
      })
      setVerification(result)
    } catch (reason) {
      setCredentialError(siteErrorMessage(reason))
    } finally {
      setVerifying(false)
    }
  }

  const saveCredential = async () => {
    if (!credentialAccount || !verification) return
    setSavingCredential(true)
    setCredentialError('')
    try {
      await updateSiteAccount(credentialAccount.id, {
        credential_type: credentialForm.credential_type,
        credential: credentialPayload(credentialForm),
      })
      setCredentialAccount(null)
      setVerification(null)
      setNotice('登录凭证已更新并验证')
      await load()
    } catch (reason) {
      setCredentialError(siteErrorMessage(reason))
    } finally {
      setSavingCredential(false)
    }
  }

  const task = async (account: SiteAccount, kind: 'checkin' | 'refresh' | 'model_refresh') => {
    setBusy((current) => new Map(current).set(account.id, kind))
    setError('')
    setNotice('')
    try {
      const result = await executeAccountTask(account.id, kind)
      if (!['success', 'partial'].includes(result.status)) throw new Error(result.error || result.status)
      const data = await getSiteInventory()
      setSites(data.sites)
      setAccounts(data.accounts)
      const updated = data.accounts.find((item) => item.id === account.id)
      const latestCheckin = kind === 'checkin' ? (await getCheckinAttempts(account.id))[0] : undefined
      if (latestCheckin) setLatestCheckins((current) => ({ ...current, [account.id]: latestCheckin }))
      const balanceDelta = latestCheckin?.balance_delta || 0
      setNotice(result.status === 'partial'
        ? `模型已刷新，渠道未同步：${siteErrorMessage(result.error)}`
        : kind === 'checkin' && balanceDelta > 0.000001
          ? `签到完成，余额增加 +${balanceDelta.toFixed(2)} ${latestCheckin?.balance_currency || updated?.balance_currency || account.balance_currency}`
          : kind === 'checkin' ? '签到任务已完成，余额已复核' : kind === 'refresh' ? '余额已刷新' : '模型和路由渠道已同步')
    } catch (reason) {
      setError(siteErrorMessage(reason))
    } finally {
      setBusy((current) => { const next = new Map(current); next.delete(account.id); return next })
    }
  }

  const remove = async (account: SiteAccount) => {
    if (!window.confirm(`删除账号“${account.label}”？对应投影渠道将被禁用。`)) return
    setBusy((current) => new Map(current).set(account.id, 'delete'))
    try {
      await deleteSiteAccount(account.id)
      setNotice('账号已删除')
      await load()
    } catch (reason) {
      setError(siteErrorMessage(reason))
    } finally {
      setBusy((current) => { const next = new Map(current); next.delete(account.id); return next })
    }
  }

  const toggleSelected = (id: number) => setSelected((current) => {
    const next = new Set(current)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })
  const allVisibleSelected = pagedVisible.length > 0 && pagedVisible.every((account) => selected.has(account.id))
  const toggleAllVisible = () => setSelected((current) => {
    const next = new Set(current)
    if (allVisibleSelected) pagedVisible.forEach((account) => next.delete(account.id)); else pagedVisible.forEach((account) => next.add(account.id))
    return next
  })

  const runBatch = async (action: 'refresh' | 'model_refresh' | 'enable' | 'disable' | 'delete') => {
    const selectedAccounts = accounts.filter((account) => selected.has(account.id))
    if (!selectedAccounts.length) return
    if (action === 'delete' && !window.confirm(`删除选中的 ${selectedAccounts.length} 个账号？对应投影渠道将被禁用。`)) return
    const targets = action === 'refresh' ? selectedAccounts.filter((account) => account.credential_type !== 'api_key') : selectedAccounts
    const skipped = selectedAccounts.length - targets.length
    setBatchBusy(true); setError(''); setNotice('')
    let failed = 0
    await runLimited(targets, async (account) => {
      setBusy((current) => new Map(current).set(account.id, action === 'enable' || action === 'disable' ? 'update' : action))
      try {
        if (action === 'refresh' || action === 'model_refresh') {
          const result = await executeAccountTask(account.id, action)
          if (!['success', 'partial'].includes(result.status)) throw new Error(result.error || result.status)
        } else if (action === 'delete') await deleteSiteAccount(account.id)
        else await updateSiteAccount(account.id, { enabled: action === 'enable' })
      } catch { failed++ }
      finally { setBusy((current) => { const next = new Map(current); next.delete(account.id); return next }) }
    })
    const succeeded = targets.length - failed
    setNotice(`批量操作完成：成功 ${succeeded}${skipped ? `，跳过 ${skipped} 个纯 API Key 账号` : ''}${failed ? `，失败 ${failed}` : ''}`)
    if (action === 'delete') setSelected(new Set())
    await load(undefined, { silent: true })
    setBatchBusy(false)
  }

  const copyAccount = async (account: SiteAccount) => {
    const site = siteMap.get(account.site_id)
    try { await navigator.clipboard.writeText([account.label, site?.name, site?.base_url].filter(Boolean).join('\n')); setNotice(`已复制账号“${account.label}”的信息`) }
    catch { setError('复制失败，请检查浏览器剪贴板权限') }
  }

  return <div className="workspace-page">
    <header className="page-header">
      <h1>账号管理</h1>
      <div className="header-controls">
        <button className="primary-button" type="button" onClick={() => void openCreate(siteFilter)} disabled={!sites.length || openingCreate}><Plus size={16} />添加账号</button>
        <button className="icon-button icon-button--surface" type="button" onClick={() => void load()} aria-label="刷新账号"><RefreshCw size={17} /></button>
      </div>
    </header>
    <section className="compact-summary"><span><strong>{accounts.length}</strong>账号总数</span><span><strong>{accounts.filter((item) => item.status === 'healthy').length}</strong>健康</span><span><strong>{accounts.filter((item) => item.auto_checkin).length}</strong>自动签到</span><span><strong>{accounts.filter((item) => item.credential_configured).length}</strong>凭证已配置</span></section>
    <div className="filter-bar filter-bar--wide"><label className="selection-toggle"><input type="checkbox" checked={allVisibleSelected} onChange={toggleAllVisible} aria-label="选择当前筛选下的全部账号" /><span>全选</span></label><label className="search-field"><Search size={16} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索账号或站点" aria-label="搜索账号" /></label><select value={siteFilter} onChange={(event) => setSiteFilter(Number(event.target.value))} aria-label="账号站点"><option value={0}>全部站点</option>{sites.map((site) => <option value={site.id} key={site.id}>{site.name}</option>)}</select><select value={status} onChange={(event) => setStatus(event.target.value)} aria-label="账号状态"><option value="all">全部状态</option><option value="healthy">正常</option><option value="error">异常</option><option value="expired">已过期</option><option value="unknown">未知</option></select></div>
    {selected.size > 0 && <div className="batch-toolbar" aria-label="账号批量操作"><strong>已选择 {selected.size} 项</strong><div><button type="button" onClick={() => void runBatch('refresh')} disabled={batchBusy}><WalletCards size={14} />刷新余额</button><button type="button" onClick={() => void runBatch('model_refresh')} disabled={batchBusy}><RefreshCw size={14} />同步路由</button><button type="button" onClick={() => void runBatch('enable')} disabled={batchBusy}><Power size={14} />启用</button><button type="button" onClick={() => void runBatch('disable')} disabled={batchBusy}><Power size={14} />禁用</button><button className="danger-button" type="button" onClick={() => void runBatch('delete')} disabled={batchBusy}><Trash2 size={14} />删除</button></div></div>}
    {notice && <div className="operation-notice">{notice}</div>}
    {error && accounts.length > 0 && <div className="inline-error">{error}</div>}
    {loading ? <LoadingState label="正在加载账号" /> : error && !accounts.length ? <ErrorState message={error} retry={() => void load()} /> : !visible.length ? accounts.length ? <EmptyState label="没有匹配的账号" /> : <div className="content-state content-state--empty"><strong>还没有账号</strong><button className="secondary-button" type="button" onClick={() => void openCreate()} disabled={!sites.length}><Plus size={15} />添加账号</button></div> : <div className="account-records records-panel"><div className="record-head account-grid"><span>账号 / 站点</span><span>状态</span><span>余额</span><span>最近签到</span><span>自动任务</span><span>操作</span></div>{pagedVisible.map((account) => <AccountRow key={account.id} account={account} site={siteMap.get(account.site_id)} latestCheckin={latestCheckins[account.id]} selected={selected.has(account.id)} busyKind={busy.get(account.id)} focused={account.id === focusAccountId} rowRef={(node) => { if (node) rowRefs.current.set(account.id, node); else rowRefs.current.delete(account.id) }} select={() => toggleSelected(account.id)} task={(kind) => void task(account, kind)} copy={() => void copyAccount(account)} edit={() => openMetadata(account)} credential={() => openCredential(account)} remove={() => void remove(account)} />)}</div>}
    <Pagination page={page} pageSize={pageSize} total={visible.length} onPage={setPage} pageSizes={[20, 50, 100]} onPageSize={(size) => { setPage(1); setPageSize(size) }} />

    {creating && <Modal title="添加账号" close={() => setCreating(false)}>{error && <div className="inline-error modal-error">{error}</div>}<CreateAccountFormView form={createForm} setForm={setCreateForm} sites={sites} saving={openingCreate} submit={saveCreate} /></Modal>}
    {editing && metadata && <Modal title="编辑账号" close={() => { setEditing(null); setMetadata(null) }}>{error && <div className="inline-error modal-error">{error}</div>}<MetadataFormView account={editing} site={siteMap.get(editing.site_id)} form={metadata} setForm={setMetadata} saving={savingMetadata} submit={saveMetadata} /></Modal>}
    {credentialAccount && <Modal title={`更新凭证 · ${credentialAccount.label}`} close={() => setCredentialAccount(null)}>{credentialError && <div className="inline-error modal-error">{credentialError}</div>}<CredentialUpdateView account={credentialAccount} site={siteMap.get(credentialAccount.site_id)} form={credentialForm} change={changeCredential} verification={verification} verifying={verifying} saving={savingCredential} verify={() => void verifyCredential()} save={() => void saveCredential()} /></Modal>}
  </div>
}

function AccountRow({ account, site, latestCheckin, selected, busyKind, focused, rowRef, select, task, copy, edit, credential, remove }: {
  account: SiteAccount
  site?: Site
  latestCheckin?: CheckinAttempt
  selected: boolean
  busyKind?: AccountTaskKind
  focused: boolean
  rowRef: (node: HTMLElement | null) => void
  select: () => void
  task: (kind: 'checkin' | 'refresh' | 'model_refresh') => void
  copy: () => void
  edit: () => void
  credential: () => void
  remove: () => void
}) {
  const apiKeyOnly = account.credential_type === 'api_key'
  const needsCredential = ['expired', 'error'].includes(account.status) || Boolean(account.last_error)
  const busy = Boolean(busyKind)
  return <article ref={rowRef} data-account-id={account.id} className={`record-row account-grid${selected ? ' row-selected' : ''}${focused ? ' row-focus-highlight' : ''}`}>
    <div className="account-identity"><input className="row-selector" type="checkbox" checked={selected} onChange={select} aria-label={`选择 ${account.label}`} /><div><a className="entity-link" href={`#/accounts?focus_account_id=${account.id}`}><strong>{account.label}</strong></a><a className="entity-chip" href={`#/sites?focus_site_id=${account.site_id}`}>{site?.name || `站点 #${account.site_id}`}</a><span>{credentialLabel(account.credential_type, site?.platform)}{account.credential_refresh_configured ? ` · 自动续期${account.credential_expires_at ? `至 ${formatTime(account.credential_expires_at)}` : ''}` : ''}</span></div></div>
    <div><StatusBadge status={account.enabled ? account.status : 'disabled'} />{needsCredential ? <button className="inline-entity-action" type="button" onClick={credential} title={account.last_error}>{account.last_error ? siteErrorMessage(account.last_error) : '需要更新凭证'}</button> : <span>{account.consecutive_failures} 次连续失败</span>}</div>
    <div><strong>{formatAccountBalance(account)}</strong>{latestCheckin?.balance_delta !== undefined && Math.abs(latestCheckin.balance_delta) > 0.000001 && <em className={latestCheckin.balance_delta > 0 ? 'balance-delta balance-delta--gain' : 'balance-delta balance-delta--loss'}>{latestCheckin.balance_delta > 0 ? '+' : ''}{latestCheckin.balance_delta.toFixed(2)} {latestCheckin.balance_currency || account.balance_currency}</em>}<span>{apiKeyOnly ? 'API Key 不读取余额' : account.balance_updated_at ? formatTime(account.balance_updated_at) : '尚未同步'}</span></div>
    <div><StatusBadge status={apiKeyOnly ? 'unsupported' : account.last_checkin_status} /><span>{apiKeyOnly ? '需要登录凭证' : account.last_checkin_at ? formatTime(account.last_checkin_at) : '尚无记录'}</span></div>
    <div><strong>{account.auto_checkin ? '签到' : '—'} / {account.auto_refresh ? '刷新' : '—'}</strong><span>{account.timezone || site?.timezone || '站点时区'}</span></div>
    <div className="account-actions"><button className="account-task-button" type="button" onClick={() => task('checkin')} disabled={busy || apiKeyOnly}>{busyKind === 'checkin' && <RefreshCw className="spin" size={14} />}签到</button><button className="account-task-button" type="button" onClick={() => task('refresh')} disabled={busy || apiKeyOnly}>{busyKind === 'refresh' ? <RefreshCw className="spin" size={14} /> : <WalletCards size={14} />}余额</button><button className="account-task-button account-task-button--route" type="button" onClick={() => task('model_refresh')} disabled={busy} title="刷新模型并更新对应路由渠道"><RefreshCw className={busyKind === 'model_refresh' ? 'spin' : ''} size={14} />同步路由</button><button type="button" onClick={credential} disabled={busy}><KeyRound size={14} />凭证</button><button className="icon-button icon-button--surface" type="button" onClick={copy} aria-label={`复制 ${account.label} 信息`} title="复制账号信息"><Copy size={15} /></button><button className="icon-button icon-button--surface" type="button" onClick={edit} disabled={busy} aria-label={`编辑 ${account.label}`}><Pencil size={15} /></button><button className="icon-button icon-button--surface danger-button" type="button" onClick={remove} disabled={busy} aria-label={`删除 ${account.label}`}><Trash2 size={15} /></button></div>
  </article>
}

function CreateAccountFormView({ form, setForm, sites, saving, submit }: { form: CreateAccountForm; setForm: React.Dispatch<React.SetStateAction<CreateAccountForm>>; sites: Site[]; saving: boolean; submit: (event: React.FormEvent) => void }) {
  const field = (key: keyof CreateAccountForm, value: string | number | boolean) => setForm((current) => ({ ...current, [key]: value }))
  const platform = sites.find((site) => site.id === form.site_id)?.platform || ''
  const supportsCheckin = platformSupportsCheckin(platform)
  const changeSite = (siteId: number) => {
    const nextPlatform = sites.find((site) => site.id === siteId)?.platform || ''
    setForm((current) => ({
      ...current,
      site_id: siteId,
      credential_type: normalizeCredentialType(nextPlatform, current.credential_type),
      user_id: nextPlatform === 'sub2api' ? 0 : current.user_id,
      auto_checkin: platformSupportsCheckin(nextPlatform) ? current.auto_checkin : false,
    }))
  }
  return <form className="console-form" onSubmit={submit}>
    <label><span>所属站点</span><select value={form.site_id} onChange={(event) => changeSite(Number(event.target.value))}>{sites.map((site) => <option value={site.id} key={site.id}>{site.name}</option>)}</select></label>
    <label><span>账号名称</span><input required maxLength={191} value={form.label} onChange={(event) => field('label', event.target.value)} placeholder="例如主账号" /></label>
    <CredentialFields form={form} field={field} platform={platform} />
    <label><span>账号时区（可选）</span><input value={form.timezone} onChange={(event) => field('timezone', event.target.value)} placeholder="留空继承站点时区" /></label>
    <div className="checkbox-grid"><label className="checkbox-field"><input type="checkbox" checked={form.enabled} onChange={(event) => field('enabled', event.target.checked)} /><span>启用账号</span></label><label className="checkbox-field"><input type="checkbox" checked={form.auto_checkin} onChange={(event) => field('auto_checkin', event.target.checked)} disabled={form.credential_type === 'api_key' || !supportsCheckin} /><span>{supportsCheckin ? '自动签到' : '该平台不支持签到'}</span></label><label className="checkbox-field"><input type="checkbox" checked={form.auto_refresh} onChange={(event) => field('auto_refresh', event.target.checked)} disabled={form.credential_type === 'api_key'} /><span>自动刷新余额</span></label></div>
    <footer><button className="primary-button" type="submit" disabled={saving || !form.site_id}>{saving ? <RefreshCw className="spin" size={15} /> : <Plus size={15} />}{saving ? '添加中' : '添加账号'}</button></footer>
  </form>
}

function MetadataFormView({ account, site, form, setForm, saving, submit }: { account: SiteAccount; site?: Site; form: MetadataForm; setForm: React.Dispatch<React.SetStateAction<MetadataForm | null>>; saving: boolean; submit: (event: React.FormEvent) => void }) {
  const field = (key: keyof MetadataForm, value: string | boolean) => setForm((current) => current ? { ...current, [key]: value } : current)
  const apiKeyOnly = account.credential_type === 'api_key'
  const supportsCheckin = platformSupportsCheckin(site?.platform || '')
  return <form className="console-form" onSubmit={submit}>
    <label><span>账号名称</span><input required maxLength={191} value={form.label} onChange={(event) => field('label', event.target.value)} /></label>
    <label><span>账号时区（可选）</span><input value={form.timezone} onChange={(event) => field('timezone', event.target.value)} placeholder="留空继承站点时区" /></label>
    <div className="checkbox-grid"><label className="checkbox-field"><input type="checkbox" checked={form.enabled} onChange={(event) => field('enabled', event.target.checked)} /><span>启用账号</span></label><label className="checkbox-field"><input type="checkbox" checked={form.auto_checkin} onChange={(event) => field('auto_checkin', event.target.checked)} disabled={apiKeyOnly || !supportsCheckin} /><span>{supportsCheckin ? '自动签到' : '该平台不支持签到'}</span></label><label className="checkbox-field"><input type="checkbox" checked={form.auto_refresh} onChange={(event) => field('auto_refresh', event.target.checked)} disabled={apiKeyOnly} /><span>自动刷新余额</span></label></div>
    <footer><button className="primary-button" type="submit" disabled={saving}>{saving && <RefreshCw className="spin" size={15} />}{saving ? '保存中' : '保存设置'}</button></footer>
  </form>
}

function CredentialUpdateView({ account, site, form, change, verification, verifying, saving, verify, save }: {
  account: SiteAccount
  site?: Site
  form: CredentialForm
  change: (key: keyof CredentialForm, value: string | number | boolean) => void
  verification: SiteCredentialVerification | null
  verifying: boolean
  saving: boolean
  verify: () => void
  save: () => void
}) {
  const ready = credentialComplete(form)
  return <div className="credential-update">
    <div className="credential-alert"><KeyRound size={17} /><div><strong>{account.label}</strong><span>{site?.name || `站点 #${account.site_id}`} · 当前为 {credentialLabel(account.credential_type, site?.platform)}</span>{account.last_error && <small>{siteErrorMessage(account.last_error)}</small>}</div></div>
    <div className="console-form credential-update-form"><CredentialFields form={form} field={change} platform={site?.platform || ''} />
      {verification && <div className="credential-verification"><div className="credential-verification-title"><CheckCircle2 size={16} /><strong>凭证验证通过</strong></div><dl><div><dt>用户</dt><dd>{verification.username || '已验证'}{verification.user_id ? ` · ID ${verification.user_id}` : ''}</dd></div><div><dt>余额</dt><dd>{verification.balance == null ? '未提供' : `${verification.balance.toFixed(2)} ${verification.currency || ''}`}</dd></div><div><dt>模型</dt><dd>{verification.model_count} 个</dd></div><div><dt>路由 Key</dt><dd>{verification.routing_key_available ? '已发现' : '未发现'}</dd></div></dl></div>}
      <footer className="credential-update-actions"><button className="secondary-button" type="button" onClick={verify} disabled={!ready || verifying || saving}>{verifying && <RefreshCw className="spin" size={15} />}{verifying ? '验证中' : '验证凭证'}</button><button className="primary-button" type="button" onClick={save} disabled={!verification || verifying || saving}>{saving && <RefreshCw className="spin" size={15} />}{saving ? '保存中' : '保存新凭证'}</button></footer>
    </div>
  </div>
}

function CredentialFields<T extends CredentialForm>({ form, field, platform }: { form: T; field: (key: keyof T, value: string | number | boolean) => void; platform: string }) {
  const options = credentialOptions(platform)
  return <section className="embedded-form-section credential-section"><label><span>凭证类型</span><select value={form.credential_type} onChange={(event) => field('credential_type', event.target.value)}>{options.map((option) => <option value={option} key={option}>{credentialLabel(option, platform)}</option>)}</select></label>{form.credential_type === 'username_password' ? <div className="form-grid"><label><span>用户名</span><input required value={form.username} onChange={(event) => field('username', event.target.value)} autoComplete="username" /></label><label><span>密码</span><input required type="password" value={form.password} onChange={(event) => field('password', event.target.value)} autoComplete="current-password" /></label></div> : <label><span>{credentialLabel(form.credential_type, platform)}</span><input required type="password" autoComplete="off" value={form.credential} onChange={(event) => field('credential', event.target.value)} placeholder={platform === 'sub2api' && form.credential_type === 'access_token' ? '填写浏览器存储中的 auth token' : '凭证将加密保存'} /></label>}{!['api_key', 'username_password'].includes(form.credential_type) && platform !== 'sub2api' && <label><span>上游用户 ID{form.credential_type === 'cookie' ? '' : '（自动识别失败时填写）'}</span><input required={form.credential_type === 'cookie'} type="number" min="1" value={form.user_id || ''} onChange={(event) => field('user_id', Number(event.target.value))} placeholder="用户个人中心显示的数字 ID" /></label>}{form.credential_type === 'access_token' && <><div className="form-grid"><label><span>Refresh Token（可选）</span><input type="password" autoComplete="off" value={form.refresh_token} onChange={(event) => field('refresh_token', event.target.value)} placeholder="用于访问令牌自动续期" /></label><label><span>访问令牌过期时间（可选）</span><input type="datetime-local" value={form.expires_at} onChange={(event) => field('expires_at', event.target.value)} /></label></div><div className="form-help">{platform === 'sub2api' ? 'Sub2API 会在 JWT 即将过期前自动刷新。Refresh Token 为空时仍可使用，但过期后需要重新登录。' : '如果上游提供 refresh token，可一并保存；JWT 的 exp 会自动识别，opaque token 建议填写过期时间。'}</div></>}{form.credential_type === 'api_key' && <div className="form-help">模型 API Key 只用于发现模型和路由，不支持余额、签到或公告。</div>}</section>
}

function credentialPayload(form: CredentialForm) {
  if (form.credential_type === 'username_password') return { username: form.username.trim(), password: form.password }
  if (form.credential_type === 'api_key') return { api_key: form.credential }
  if (form.credential_type === 'cookie') return { cookie: form.credential, user_id: form.user_id }
  return { access_token: form.credential, ...(form.user_id > 0 ? { user_id: form.user_id } : {}), ...(form.refresh_token.trim() ? { refresh_token: form.refresh_token.trim() } : {}), ...(form.expires_at ? { expires_at: new Date(form.expires_at).getTime() } : {}) }
}

function credentialComplete(form: CredentialForm): boolean {
  if (form.credential_type === 'username_password') return Boolean(form.username.trim() && form.password)
  if (!form.credential.trim()) return false
  return form.credential_type !== 'cookie' || form.user_id > 0
}

async function runLimited<T>(items: T[], worker: (item: T) => Promise<void>, concurrency = 3): Promise<void> {
  let cursor = 0
  const run = async () => { while (cursor < items.length) await worker(items[cursor++]) }
  await Promise.all(Array.from({ length: Math.min(concurrency, items.length) }, run))
}

function toLocalInput(value: number): string {
  const date = new Date(value - new Date(value).getTimezoneOffset() * 60_000)
  return date.toISOString().slice(0, 16)
}
