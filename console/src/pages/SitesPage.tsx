import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Copy, ExternalLink, Globe2, Network, Pencil, Plus, Power, Radar, RefreshCw, Search, Trash2, UserPlus } from 'lucide-react'
import { createSite, deleteSite, getSiteInventory, peekSiteInventory, probeSite, updateSite } from '../api'
import type { Site, SiteAccount } from '../types'
import { EmptyState, ErrorState, LoadingState, OperationNotice, Pagination } from './shared'
import { Modal, StatusBadge, siteErrorMessage } from './siteShared'
import { useLocation } from 'react-router-dom'
import { credentialLabel, credentialOptions, normalizeCredentialType, platformSupportsCheckin, type CredentialType } from '../siteCredentials'

type SiteForm = {
  name: string; base_url: string; platform: string; timezone: string; use_system_proxy: boolean; proxy_url: string; external_checkin_url: string; enabled: boolean
  addAccount: boolean; accountLabel: string; credentialType: CredentialType; credential: string; username: string; password: string; userId: number; refreshToken: string; expiresAt: string
  autoCheckin: boolean; autoRefresh: boolean
}
const emptyForm: SiteForm = {
  name: '', base_url: '', platform: 'unknown', timezone: 'Asia/Shanghai', use_system_proxy: true, proxy_url: '', external_checkin_url: '', enabled: true,
  addAccount: true, accountLabel: '主账号', credentialType: 'username_password', credential: '', username: '', password: '', userId: 0, refreshToken: '', expiresAt: '', autoCheckin: true, autoRefresh: true,
}

const PLATFORM_OPTIONS = [
  ['unknown', '自动识别'], ['new-api-family', 'New API 家族'], ['one-api', 'One API'], ['one-hub', 'OneHub'], ['done-hub', 'DoneHub'],
  ['veloera', 'Veloera'], ['anyrouter', 'AnyRouter'], ['sub2api', 'Sub2API'], ['voapi', 'VoAPI'], ['axon-hub', 'AxonHub'],
  ['openai-compatible', 'OpenAI Compatible'],
] as const

export default function SitesPage() {
  const initialInventory = peekSiteInventory()
  const location = useLocation()
  const query = useMemo(() => new URLSearchParams(location.search), [location.search])
  const querySearch = query.get('search') || ''
  const focusSiteId = Number(query.get('focus_site_id') || 0)
  const [sites, setSites] = useState<Site[]>(() => initialInventory?.sites || [])
  const [accounts, setAccounts] = useState<SiteAccount[]>(() => initialInventory?.accounts || [])
  const [search, setSearch] = useState(querySearch)
  const [sort, setSort] = useState('newest')
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const [loading, setLoading] = useState(!initialInventory)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busyId, setBusyId] = useState<number | null>(null)
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [batchBusy, setBatchBusy] = useState(false)
  const [editing, setEditing] = useState<Site | null | undefined>(undefined)
  const [form, setForm] = useState<SiteForm>(emptyForm)
  const [saving, setSaving] = useState(false)
  const rowRefs = useRef(new Map<number, HTMLElement>())

  const load = useCallback(async (signal?: AbortSignal, options: { silent?: boolean; force?: boolean } = {}) => {
    if (!options.silent && !peekSiteInventory()) setLoading(true)
    setError('')
    try {
      const data = await getSiteInventory(signal, { force: options.force })
      setSites(data.sites)
      setAccounts(data.accounts)
      setSelected((current) => new Set([...current].filter((id) => data.sites.some((site) => site.id === id))))
    }
    catch (reason) { if (!signal?.aborted) setError(siteErrorMessage(reason)) }
    finally { if (!signal?.aborted && !options.silent) setLoading(false) }
  }, [])

  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort() }, [load])
  useEffect(() => setSearch(querySearch), [querySearch])
  const visible = useMemo(() => {
    const filtered = sites.filter((site) => !search.trim() || [site.name, site.base_url, site.platform].some((value) => value.toLowerCase().includes(search.trim().toLowerCase())))
    return [...filtered].sort((left, right) => {
      if (sort === 'name') return left.name.localeCompare(right.name, 'zh-CN')
      if (sort === 'enabled') return Number(right.enabled) - Number(left.enabled) || left.name.localeCompare(right.name, 'zh-CN')
      if (sort === 'health') {
        const leftHealthy = accounts.filter((account) => account.site_id === left.id && account.status === 'healthy').length
        const rightHealthy = accounts.filter((account) => account.site_id === right.id && account.status === 'healthy').length
        return rightHealthy - leftHealthy || left.name.localeCompare(right.name, 'zh-CN')
      }
      return right.id - left.id
    })
  }, [accounts, search, sites, sort])
  const pagedVisible = useMemo(() => visible.slice((page - 1) * pageSize, page * pageSize), [page, pageSize, visible])
  const healthy = accounts.filter((account) => account.status === 'healthy').length

  useEffect(() => { setPage(1) }, [search])
  useEffect(() => {
    const pages = Math.max(1, Math.ceil(visible.length / pageSize))
    if (page > pages) setPage(pages)
  }, [page, pageSize, visible.length])

  useEffect(() => {
    if (!focusSiteId) return
    const index = visible.findIndex((site) => site.id === focusSiteId)
    if (index >= 0) setPage(Math.floor(index / pageSize) + 1)
  }, [focusSiteId, pageSize, visible])

  useEffect(() => {
    if (!focusSiteId || loading) return
    const row = rowRefs.current.get(focusSiteId)
    if (!row) {
      setError(`未找到站点 #${focusSiteId}，请刷新后重试`)
      return
    }
    const timer = window.setTimeout(() => row.scrollIntoView({ behavior: 'smooth', block: 'center' }), 80)
    return () => window.clearTimeout(timer)
  }, [focusSiteId, loading, pagedVisible])

  const openForm = (site?: Site) => {
    setError('')
    setEditing(site || null)
    setForm(site ? {
      ...emptyForm, name: site.name, base_url: site.base_url, platform: site.platform, timezone: site.timezone,
      use_system_proxy: site.use_system_proxy, proxy_url: site.proxy_url || '', external_checkin_url: site.external_checkin_url || '', enabled: site.enabled, addAccount: false,
    } : { ...emptyForm })
  }

  const save = async (event: React.FormEvent) => {
    event.preventDefault(); setSaving(true); setError('')
    try {
      if (editing) {
        await updateSite(editing.id, {
          name: form.name, base_url: form.base_url, platform: form.platform, timezone: form.timezone,
          use_system_proxy: form.use_system_proxy, proxy_url: form.proxy_url, external_checkin_url: form.external_checkin_url, enabled: form.enabled,
        })
      } else {
		const credential = form.credentialType === 'username_password'
			? { username: form.username.trim(), password: form.password }
			: form.credentialType === 'api_key'
				? { api_key: form.credential }
				: form.credentialType === 'cookie'
					? { cookie: form.credential, user_id: form.userId }
				: { access_token: form.credential, ...(form.userId > 0 ? { user_id: form.userId } : {}), ...(form.platform === 'sub2api' && form.refreshToken.trim() ? { refresh_token: form.refreshToken.trim() } : {}), ...(form.platform === 'sub2api' && /^\d+$/.test(form.expiresAt.trim()) ? { expires_at: Number(form.expiresAt.trim()) } : {}) }
		await createSite({
			name: form.name, base_url: form.base_url, platform: form.platform, timezone: form.timezone,
			use_system_proxy: form.use_system_proxy, proxy_url: form.proxy_url, external_checkin_url: form.external_checkin_url, tags: [],
			...(form.addAccount ? { account: {
				label: form.accountLabel.trim() || '主账号', credential_type: form.credentialType, credential,
				enabled: true, auto_checkin: form.credentialType === 'api_key' || !platformSupportsCheckin(form.platform) ? false : form.autoCheckin,
				auto_refresh: form.credentialType === 'api_key' ? false : form.autoRefresh, timezone: form.timezone,
			} } : {}),
		})
      }
		setEditing(undefined); setNotice(editing ? '站点已更新' : form.addAccount ? '站点和主账号已添加' : '站点已添加'); await load(undefined, { silent: true, force: true })
    } catch (reason) {
      setError(siteErrorMessage(reason))
    }
    finally { setSaving(false) }
  }

  const execute = async (site: Site, action: 'probe' | 'toggle' | 'delete') => {
    if (action === 'delete' && !window.confirm(`永久删除站点“${site.name}”、全部账号及同步生成的渠道？手工渠道不受影响，此操作无法撤销。`)) return
    setBusyId(site.id); setError(''); setNotice('')
    try {
      if (action === 'probe') { const result = await probeSite(site.id); setNotice(result.matched ? '站点探测成功' : '未识别到受支持的平台') }
      if (action === 'toggle') { await updateSite(site.id, { enabled: !site.enabled }); setNotice(site.enabled ? '站点已停用' : '站点已启用') }
      if (action === 'delete') { await deleteSite(site.id); setNotice('站点已删除') }
      await load(undefined, { silent: true, force: true })
    } catch (reason) { setError(siteErrorMessage(reason)) }
    finally { setBusyId(null) }
  }

  const toggleSelected = (id: number) => setSelected((current) => {
    const next = new Set(current)
    if (next.has(id)) next.delete(id); else next.add(id)
    return next
  })
  const allVisibleSelected = pagedVisible.length > 0 && pagedVisible.every((site) => selected.has(site.id))
  const toggleAllVisible = () => setSelected((current) => {
    const next = new Set(current)
    if (allVisibleSelected) pagedVisible.forEach((site) => next.delete(site.id)); else pagedVisible.forEach((site) => next.add(site.id))
    return next
  })

  const runBatch = async (action: 'proxy_on' | 'proxy_off' | 'enable' | 'disable' | 'delete') => {
    const targets = sites.filter((site) => selected.has(site.id))
    if (!targets.length) return
    if (action === 'delete' && !window.confirm(`永久删除选中的 ${targets.length} 个站点、全部账号及同步生成的渠道？手工渠道不受影响，此操作无法撤销。`)) return
    setBatchBusy(true); setError(''); setNotice('')
    let failed = 0
    await runLimited(targets, async (site) => {
      try {
        if (action === 'delete') await deleteSite(site.id)
        else if (action === 'proxy_on' || action === 'proxy_off') await updateSite(site.id, { use_system_proxy: action === 'proxy_on' })
        else await updateSite(site.id, { enabled: action === 'enable' })
      } catch { failed++ }
    })
    setNotice(failed ? `批量操作完成，${targets.length - failed} 个成功，${failed} 个失败` : `已处理 ${targets.length} 个站点`)
    if (action === 'delete') setSelected(new Set())
    await load(undefined, { silent: true, force: true })
    setBatchBusy(false)
  }

  const copySite = async (site: Site) => {
    try { await navigator.clipboard.writeText(site.base_url); setNotice(`已复制 ${site.name} 的站点地址`) }
    catch { setError('复制失败，请检查浏览器剪贴板权限') }
  }

  return <div className="workspace-page">
    <header className="page-header">
	  <h1>站点管理</h1>
      <div className="header-controls"><button className="primary-button" type="button" onClick={() => openForm()}><Plus size={16} />添加站点</button><button className="icon-button icon-button--surface" type="button" onClick={() => void load(undefined, { silent: true, force: true })} aria-label="刷新站点"><RefreshCw size={17} /></button></div>
    </header>
    <section className="compact-summary"><span><strong>{sites.length}</strong>站点总数</span><span><strong>{sites.filter((site) => site.enabled).length}</strong>已启用</span><span><strong>{accounts.length}</strong>账号总数</span><span><strong>{healthy}</strong>健康账号</span></section>
    <div className="filter-bar"><label className="selection-toggle"><input type="checkbox" checked={allVisibleSelected} onChange={toggleAllVisible} aria-label="选择当前筛选下的全部站点" /><span>全选</span></label><label className="search-field"><Search size={16} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索站点、地址或平台" aria-label="搜索站点" /></label><select value={sort} onChange={(event) => { setSort(event.target.value); setPage(1) }} aria-label="站点排序"><option value="newest">新建优先</option><option value="name">名称 A-Z</option><option value="enabled">启用优先</option><option value="health">健康账号数</option></select><span className="filter-count"><Globe2 size={14} />{visible.length} 个站点</span></div>
    {selected.size > 0 && <div className="batch-toolbar" aria-label="站点批量操作"><strong>已选择 {selected.size} 项</strong><div><button type="button" onClick={() => void runBatch('proxy_on')} disabled={batchBusy}><Network size={14} />开启系统代理</button><button type="button" onClick={() => void runBatch('proxy_off')} disabled={batchBusy}><Network size={14} />关闭系统代理</button><button type="button" onClick={() => void runBatch('enable')} disabled={batchBusy}><Power size={14} />启用</button><button type="button" onClick={() => void runBatch('disable')} disabled={batchBusy}><Power size={14} />禁用</button><button className="danger-button" type="button" onClick={() => void runBatch('delete')} disabled={batchBusy}><Trash2 size={14} />删除</button></div></div>}
    {notice && <OperationNotice onDismiss={() => setNotice('')}>{notice}</OperationNotice>}{error && sites.length > 0 && <OperationNotice tone="error">{error}</OperationNotice>}
    {loading ? <LoadingState label="正在加载站点资产" /> : error && !sites.length ? <ErrorState message={error} retry={() => void load()} /> : !visible.length ? <EmptyState label={sites.length ? '没有匹配的站点' : '尚未添加站点'} /> : <div className="site-list">{pagedVisible.map((site) => <SiteRow key={site.id} site={site} accounts={accounts.filter((account) => account.site_id === site.id)} selected={selected.has(site.id)} busy={busyId === site.id || (batchBusy && selected.has(site.id))} focused={site.id === focusSiteId} rowRef={(node) => { if (node) rowRefs.current.set(site.id, node); else rowRefs.current.delete(site.id) }} select={() => toggleSelected(site.id)} copy={() => void copySite(site)} edit={() => openForm(site)} execute={(action) => void execute(site, action)} />)}</div>}
    <Pagination page={page} pageSize={pageSize} total={visible.length} onPage={setPage} pageSizes={[50, 100]} onPageSize={(size) => { setPage(1); setPageSize(size) }} />
    {editing !== undefined && <Modal title={editing ? '编辑站点' : '添加站点'} close={() => setEditing(undefined)}>{error && <div className="inline-error modal-error">{error}</div>}<SiteFormView form={form} setForm={setForm} saving={saving} submit={save} editing={Boolean(editing)} /></Modal>}
  </div>
}

function SiteRow({ site, accounts, selected, busy, focused, rowRef, select, copy, edit, execute }: { site: Site; accounts: SiteAccount[]; selected: boolean; busy: boolean; focused: boolean; rowRef: (node: HTMLElement | null) => void; select: () => void; copy: () => void; edit: () => void; execute: (action: 'probe' | 'toggle' | 'delete') => void }) {
  const healthy = accounts.filter((account) => account.status === 'healthy').length
  return <article ref={rowRef} data-site-id={site.id} className={`site-row${selected ? ' row-selected' : ''}${focused ? ' row-focus-highlight' : ''}`}>
    <div className="site-identity"><input className="row-selector" type="checkbox" checked={selected} onChange={select} aria-label={`选择 ${site.name}`} /><span className={`status-dot ${site.enabled ? 'status-dot--success' : 'status-dot--muted'}`} /><div><a className="entity-link" href={`#/sites?focus_site_id=${site.id}`}><strong>{site.name}</strong></a><span>#{site.id} · {site.timezone || 'Asia/Shanghai'}</span></div></div>
    <div className="site-address"><a className="site-base-link" href={site.base_url} target="_blank" rel="noreferrer" title={`在新标签页打开 ${site.base_url}`}><strong>{site.base_url}</strong></a><span>{site.platform || 'unknown'} · {site.proxy_url ? '自定义代理' : site.use_system_proxy ? '系统代理' : '直连'}</span></div>
    <div className="site-account-summary"><strong>{healthy}/{accounts.length}</strong><div className="site-account-links">{accounts.length ? accounts.slice(0, 2).map((account) => <a className="entity-chip" key={account.id} href={`#/accounts?focus_account_id=${account.id}${['expired', 'error'].includes(account.status) ? '&open_credential=1' : ''}`}>{account.label}</a>) : <span>暂无账号</span>}</div></div>
    <div className="site-probe"><StatusBadge status={site.last_probe_status} /><span title={site.last_error}>{site.last_error || '最近探测状态'}</span></div>
    <div className="row-actions">
      {site.external_checkin_url && <a className="icon-button icon-button--surface" href={site.external_checkin_url} target="_blank" rel="noreferrer" aria-label={`打开 ${site.name} 签到页`} title="外部签到页"><ExternalLink size={16} /></a>}
      <button className="icon-button icon-button--surface" type="button" onClick={() => execute('probe')} disabled={busy} aria-label={`探测 ${site.name}`} title="探测站点"><Radar className={busy ? 'spin' : ''} size={16} /></button>
      <button className={`icon-button icon-button--surface ${site.enabled ? 'is-on' : ''}`} type="button" onClick={() => execute('toggle')} disabled={busy} aria-label={site.enabled ? `停用 ${site.name}` : `启用 ${site.name}`}><Power size={16} /></button>
      <a className="icon-button icon-button--surface" href={`#/accounts?site_id=${site.id}&create=1`} aria-label={`为 ${site.name} 添加账号`} title="添加账号"><UserPlus size={16} /></a>
      <button className="icon-button icon-button--surface" type="button" onClick={copy} aria-label={`复制 ${site.name} 地址`} title="复制站点地址"><Copy size={16} /></button>
      <button className="icon-button icon-button--surface" type="button" onClick={edit} aria-label={`编辑 ${site.name}`} title="编辑站点"><Pencil size={16} /></button>
      <button className="icon-button icon-button--surface danger-button" type="button" onClick={() => execute('delete')} disabled={busy} aria-label={`删除 ${site.name}`}><Trash2 size={16} /></button>
    </div>
  </article>
}

function SiteFormView({ form, setForm, saving, submit, editing }: { form: SiteForm; setForm: React.Dispatch<React.SetStateAction<SiteForm>>; saving: boolean; submit: (event: React.FormEvent) => void; editing: boolean }) {
  const field = (key: keyof SiteForm, value: string | boolean | number) => setForm((current) => ({ ...current, [key]: value }))
  const options = credentialOptions(form.platform)
  const supportsCheckin = platformSupportsCheckin(form.platform)
  const changePlatform = (platform: string) => setForm((current) => ({
    ...current,
    platform,
    credentialType: normalizeCredentialType(platform, current.credentialType),
    autoCheckin: platformSupportsCheckin(platform) ? current.autoCheckin : false,
    userId: platform === 'sub2api' ? 0 : current.userId,
  }))
  return <form className="console-form" onSubmit={submit}>
    <div className="form-grid"><label><span>站点名称</span><input required maxLength={191} value={form.name} onChange={(event) => field('name', event.target.value)} placeholder="例如：我的主力站" /></label><label><span>主站地址</span><input required type="url" value={form.base_url} onChange={(event) => field('base_url', event.target.value)} placeholder="https://example.com" /></label></div>
		<div className="form-grid"><label><span>平台类型</span><select value={form.platform} onChange={(event) => changePlatform(event.target.value)}>{PLATFORM_OPTIONS.map(([value, label]) => <option value={value} key={value}>{label}</option>)}</select></label><label><span>站点时区</span><input value={form.timezone} onChange={(event) => field('timezone', event.target.value)} placeholder="Asia/Shanghai" /></label></div>
	<div className="form-help">建议使用账号密码或系统访问令牌：系统会读取余额、签到、公告，并自动发现模型调用 Key。只填模型 API Key 时仅支持模型与路由。</div>
    <label className="checkbox-field"><input type="checkbox" checked={form.use_system_proxy} onChange={(event) => field('use_system_proxy', event.target.checked)} /><span>使用系统代理</span></label>
    <label><span>代理地址（可选）</span><input value={form.proxy_url} onChange={(event) => field('proxy_url', event.target.value)} placeholder="http://127.0.0.1:7890" /></label>
    <label><span>外部签到地址（可选）</span><input type="url" value={form.external_checkin_url} onChange={(event) => field('external_checkin_url', event.target.value)} placeholder="需要浏览器时打开此地址" /></label>
	{!editing && <section className="embedded-form-section">
      <label className="checkbox-field"><input type="checkbox" checked={form.addAccount} onChange={(event) => field('addAccount', event.target.checked)} /><span>同时添加首个账号</span></label>
      {form.addAccount && <>
        <div className="form-grid"><label><span>账号名称</span><input value={form.accountLabel} onChange={(event) => field('accountLabel', event.target.value)} placeholder="主账号" /></label><label><span>添加方式</span><select value={form.credentialType} onChange={(event) => field('credentialType', event.target.value)}>{options.map((option) => <option value={option} key={option}>{credentialLabel(option, form.platform)}</option>)}</select></label></div>
        {form.credentialType === 'username_password' ? <div className="form-grid"><label><span>用户名</span><input required value={form.username} onChange={(event) => field('username', event.target.value)} autoComplete="username" /></label><label><span>密码</span><input required type="password" value={form.password} onChange={(event) => field('password', event.target.value)} autoComplete="current-password" /></label></div> : <label><span>{credentialLabel(form.credentialType, form.platform)}</span><input required type="password" autoComplete="off" value={form.credential} onChange={(event) => field('credential', event.target.value)} placeholder={form.platform === 'sub2api' && form.credentialType === 'access_token' ? '填写浏览器存储中的 auth token' : '凭证会加密保存'} /></label>}
        {!['api_key', 'username_password'].includes(form.credentialType) && form.platform !== 'sub2api' && <label><span>上游用户 ID{form.credentialType === 'cookie' ? '' : '（自动识别失败时填写）'}</span><input required={form.credentialType === 'cookie'} type="number" min="1" value={form.userId || ''} onChange={(event) => field('userId', Number(event.target.value))} placeholder="用户个人中心显示的数字 ID" /></label>}
        {form.platform === 'sub2api' && form.credentialType === 'access_token' && <><div className="form-grid"><label><span>Refresh Token（可选）</span><input type="password" autoComplete="off" value={form.refreshToken} onChange={(event) => field('refreshToken', event.target.value)} placeholder="用于访问令牌自动续期" /></label><label><span>访问令牌过期时间（可选）</span><input type="text" inputMode="numeric" pattern="[0-9]*" value={form.expiresAt} onChange={(event) => field('expiresAt', event.target.value.replace(/[^0-9]/g, ''))} placeholder="例如 1786982260314" /></label></div><div className="form-help">填写 Auth Token；如同时提供 Refresh Token，系统会在访问令牌即将过期前自动续期。</div></>}
        {form.credentialType === 'api_key' ? <div className="form-help">模型 API Key 会用于模型发现和自动创建渠道，不会执行余额刷新、签到或公告同步。</div> : <div className="checkbox-grid"><label className="checkbox-field"><input type="checkbox" checked={form.autoCheckin} onChange={(event) => field('autoCheckin', event.target.checked)} disabled={!supportsCheckin} /><span>{supportsCheckin ? '自动签到' : '该平台不支持签到'}</span></label><label className="checkbox-field"><input type="checkbox" checked={form.autoRefresh} onChange={(event) => field('autoRefresh', event.target.checked)} /><span>自动刷新余额</span></label></div>}
      </>}
    </section>}
    {editing && <label className="checkbox-field"><input type="checkbox" checked={form.enabled} onChange={(event) => field('enabled', event.target.checked)} /><span>启用站点</span></label>}
    <footer><button className="primary-button" type="submit" disabled={saving}>{saving ? <RefreshCw className="spin" size={15} /> : null}{saving ? '保存中' : '保存站点'}</button></footer>
  </form>
}

async function runLimited<T>(items: T[], worker: (item: T) => Promise<void>, concurrency = 3): Promise<void> {
  let cursor = 0
  const run = async () => { while (cursor < items.length) await worker(items[cursor++]) }
  await Promise.all(Array.from({ length: Math.min(concurrency, items.length) }, run))
}
