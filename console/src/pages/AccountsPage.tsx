import { useCallback, useEffect, useMemo, useState } from 'react'
import { KeyRound, Pencil, Plus, RefreshCw, Search, Trash2, WalletCards } from 'lucide-react'
import { createSiteAccount, deleteSiteAccount, getSiteInventory, runAccountTask as executeAccountTask, updateSiteAccount } from '../api'
import type { Site, SiteAccount } from '../types'
import { EmptyState, ErrorState, formatTime, LoadingState, OperationNotice } from './shared'
import { formatAccountBalance, Modal, siteErrorMessage, StatusBadge } from './siteShared'
import { useLocation } from 'react-router-dom'

type AccountForm = { site_id: number; label: string; credential_type: string; credential: string; username: string; password: string; user_id: number; timezone: string; enabled: boolean; auto_checkin: boolean; auto_refresh: boolean; replace_credential: boolean }
const emptyAccount = (siteId = 0): AccountForm => ({ site_id: siteId, label: '', credential_type: 'username_password', credential: '', username: '', password: '', user_id: 0, timezone: '', enabled: true, auto_checkin: true, auto_refresh: true, replace_credential: false })

export default function AccountsPage() {
  const location = useLocation(); const query = useMemo(() => new URLSearchParams(location.search), [location.search]); const querySearch = query.get('search') || ''; const querySite = Number(query.get('site_id') || 0)
  const [sites, setSites] = useState<Site[]>([]); const [accounts, setAccounts] = useState<SiteAccount[]>([])
  const [search, setSearch] = useState(querySearch); const [siteFilter, setSiteFilter] = useState(querySite); const [status, setStatus] = useState('all')
  const [loading, setLoading] = useState(true); const [error, setError] = useState(''); const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState<Set<number>>(new Set()); const [editing, setEditing] = useState<SiteAccount | null | undefined>(undefined)
  const [form, setForm] = useState<AccountForm>(emptyAccount()); const [saving, setSaving] = useState(false)

  const load = useCallback(async (signal?: AbortSignal) => { setLoading(true); setError(''); try { const data = await getSiteInventory(signal); setSites(data.sites); setAccounts(data.accounts) } catch (reason) { if (!signal?.aborted) setError(siteErrorMessage(reason)) } finally { if (!signal?.aborted) setLoading(false) } }, [])
  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort() }, [load])
  useEffect(() => { setSearch(querySearch); setSiteFilter(querySite) }, [querySearch, querySite])
  const siteMap = useMemo(() => new Map(sites.map((site) => [site.id, site])), [sites])
  const visible = useMemo(() => accounts.filter((account) => (!siteFilter || account.site_id === siteFilter) && (status === 'all' || account.status === status) && (!search.trim() || [account.label, siteMap.get(account.site_id)?.name || ''].some((value) => value.toLowerCase().includes(search.trim().toLowerCase())))), [accounts, search, siteFilter, siteMap, status])

	const openForm = (account?: SiteAccount) => { setError(''); setEditing(account || null); setForm(account ? { site_id: account.site_id, label: account.label, credential_type: account.credential_type, credential: '', username: '', password: '', user_id: 0, timezone: account.timezone || '', enabled: account.enabled, auto_checkin: account.auto_checkin, auto_refresh: account.auto_refresh, replace_credential: false } : emptyAccount(siteFilter || sites[0]?.id || 0)) }
	const save = async (event: React.FormEvent) => {
		event.preventDefault(); setSaving(true); setError('')
		try {
			const credential = accountCredentialPayload(form)
			if (editing) {
				await updateSiteAccount(editing.id, {
					label: form.label, timezone: form.timezone, enabled: form.enabled,
					auto_checkin: form.credential_type === 'api_key' ? false : form.auto_checkin,
					auto_refresh: form.credential_type === 'api_key' ? false : form.auto_refresh,
					...(form.replace_credential ? { credential_type: form.credential_type, credential } : {}),
				})
			} else {
				await createSiteAccount(form.site_id, { label: form.label, credential_type: form.credential_type, credential, enabled: form.enabled, auto_checkin: form.credential_type === 'api_key' ? false : form.auto_checkin, auto_refresh: form.credential_type === 'api_key' ? false : form.auto_refresh, timezone: form.timezone })
			}
			setEditing(undefined); setNotice(editing ? '账号已更新' : '账号已添加'); await load()
		} catch (reason) { setError(siteErrorMessage(reason)) }
		finally { setSaving(false) }
	}

  const task = async (account: SiteAccount, kind: 'checkin' | 'refresh' | 'model_refresh') => { setBusy((current) => new Set(current).add(account.id)); setError(''); setNotice(''); try { const result = await executeAccountTask(account.id, kind); if (!['success', 'partial'].includes(result.status)) throw new Error(result.error || result.status); setNotice(result.status === 'partial' ? `模型已刷新，渠道未同步：${siteErrorMessage(result.error)}` : kind === 'checkin' ? '签到任务已完成' : kind === 'refresh' ? '余额已刷新' : '模型和路由渠道已同步'); await load() } catch (reason) { setError(siteErrorMessage(reason)) } finally { setBusy((current) => { const next = new Set(current); next.delete(account.id); return next }) } }
  const remove = async (account: SiteAccount) => { if (!window.confirm(`永久删除账号“${account.label}”及其同步生成的渠道？手工渠道不受影响，此操作无法撤销。`)) return; setBusy((current) => new Set(current).add(account.id)); try { await deleteSiteAccount(account.id); setNotice('账号及同步渠道已删除'); await load() } catch (reason) { setError(siteErrorMessage(reason)) } finally { setBusy((current) => { const next = new Set(current); next.delete(account.id); return next }) } }

  return <div className="workspace-page">
	<header className="page-header"><h1>{siteFilter ? `${siteMap.get(siteFilter)?.name || '站点'} · 账号` : '站点账号'}</h1><div className="header-controls"><a className="secondary-button" href="#/sites">返回站点</a><button className="primary-button" type="button" onClick={() => openForm()} disabled={!sites.length}><Plus size={16} />添加账号</button><button className="icon-button icon-button--surface" type="button" onClick={() => void load()} aria-label="刷新账号"><RefreshCw size={17} /></button></div></header>
    <section className="compact-summary"><span><strong>{accounts.length}</strong>账号总数</span><span><strong>{accounts.filter((item) => item.status === 'healthy').length}</strong>健康</span><span><strong>{accounts.filter((item) => item.auto_checkin).length}</strong>自动签到</span><span><strong>{accounts.filter((item) => item.credential_configured).length}</strong>凭证已配置</span></section>
    <div className="filter-bar filter-bar--wide"><label className="search-field"><Search size={16} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索账号或站点" aria-label="搜索账号" /></label><select value={siteFilter} onChange={(event) => setSiteFilter(Number(event.target.value))} aria-label="账号站点"><option value={0}>全部站点</option>{sites.map((site) => <option value={site.id} key={site.id}>{site.name}</option>)}</select><select value={status} onChange={(event) => setStatus(event.target.value)} aria-label="账号状态"><option value="all">全部状态</option><option value="healthy">正常</option><option value="error">异常</option><option value="expired">已过期</option><option value="unknown">未知</option></select></div>
    {notice && <OperationNotice onDismiss={() => setNotice('')}>{notice}</OperationNotice>}{error && accounts.length > 0 && <OperationNotice tone="error">{error}</OperationNotice>}
    {loading ? <LoadingState label="正在加载站点账号" /> : error && !accounts.length ? <ErrorState message={error} retry={() => void load()} /> : !visible.length ? accounts.length ? <EmptyState label="没有匹配的账号" /> : <div className="content-state content-state--empty"><strong>还没有账号</strong><span>登录站点账号后才能读取余额、签到和同步公告；只有 API Key 时仅用于模型与路由。</span><button className="secondary-button" type="button" onClick={() => openForm()} disabled={!sites.length}><Plus size={15} />添加账号</button></div> : <div className="account-records records-panel"><div className="record-head account-grid"><span>账号 / 站点</span><span>状态</span><span>余额</span><span>最近签到</span><span>自动任务</span><span>操作</span></div>{visible.map((account) => <AccountRow key={account.id} account={account} site={siteMap.get(account.site_id)} busy={busy.has(account.id)} task={(kind) => void task(account, kind)} edit={() => openForm(account)} remove={() => void remove(account)} />)}</div>}
    {editing !== undefined && <Modal title={editing ? '编辑账号' : '添加账号'} close={() => setEditing(undefined)}>{error && <div className="inline-error modal-error">{error}</div>}<AccountFormView form={form} setForm={setForm} sites={sites} editing={Boolean(editing)} saving={saving} submit={save} /></Modal>}
  </div>
}

function AccountRow({ account, site, busy, task, edit, remove }: { account: SiteAccount; site?: Site; busy: boolean; task: (kind: 'checkin' | 'refresh' | 'model_refresh') => void; edit: () => void; remove: () => void }) {
	const apiKeyOnly = account.credential_type === 'api_key'
	return <article className="record-row account-grid"><div><strong>{account.label}</strong><span>{site?.name || `站点 #${account.site_id}`} · {credentialLabel(account.credential_type)}</span></div><div><StatusBadge status={account.enabled ? account.status : 'disabled'} /><span title={account.last_error}>{account.last_error ? siteErrorMessage(account.last_error) : `${account.consecutive_failures} 次连续失败`}</span></div><div><strong>{formatAccountBalance(account)}</strong><span>{apiKeyOnly ? 'API Key 不读取余额' : account.balance_updated_at ? formatTime(account.balance_updated_at) : '尚未同步'}</span></div><div><StatusBadge status={apiKeyOnly ? 'unsupported' : account.last_checkin_status} /><span>{apiKeyOnly ? '需要登录凭证' : account.last_checkin_at ? formatTime(account.last_checkin_at) : '尚无记录'}</span></div><div><strong>{account.auto_checkin ? '签到' : '—'} / {account.auto_refresh ? '刷新' : '—'}</strong><span>{account.timezone || site?.timezone || '站点时区'}</span></div><div className="account-actions"><button type="button" onClick={() => task('checkin')} disabled={busy || apiKeyOnly}>签到</button><button type="button" onClick={() => task('refresh')} disabled={busy || apiKeyOnly}><WalletCards size={14} />余额</button><button type="button" onClick={() => task('model_refresh')} disabled={busy}><RefreshCw className={busy ? 'spin' : ''} size={14} />同步</button><button className="icon-button icon-button--surface" type="button" onClick={edit} aria-label={`编辑 ${account.label}`}><Pencil size={15} /></button><button className="icon-button icon-button--surface danger-button" type="button" onClick={remove} disabled={busy} aria-label={`删除 ${account.label}`}><Trash2 size={15} /></button></div></article>
}

function AccountFormView({ form, setForm, sites, editing, saving, submit }: { form: AccountForm; setForm: React.Dispatch<React.SetStateAction<AccountForm>>; sites: Site[]; editing: boolean; saving: boolean; submit: (event: React.FormEvent) => void }) {
  const field = (key: keyof AccountForm, value: string | number | boolean) => setForm((current) => ({ ...current, [key]: value }))
	const showCredential = !editing || form.replace_credential
	return <form className="console-form" onSubmit={submit}><label><span>所属站点</span><select value={form.site_id} onChange={(event) => field('site_id', Number(event.target.value))} disabled={editing}>{sites.map((site) => <option value={site.id} key={site.id}>{site.name}</option>)}</select></label><label><span>账号名称</span><input required maxLength={191} value={form.label} onChange={(event) => field('label', event.target.value)} placeholder="例如主账号" /></label>{editing && <label className="checkbox-field credential-replace-toggle"><input type="checkbox" checked={form.replace_credential} onChange={(event) => field('replace_credential', event.target.checked)} /><span>更换登录凭证</span></label>}{showCredential && <CredentialFields form={form} field={field} />}<label><span>账号时区（可选）</span><input value={form.timezone} onChange={(event) => field('timezone', event.target.value)} placeholder="留空继承站点时区" /></label><div className="checkbox-grid"><label className="checkbox-field"><input type="checkbox" checked={form.enabled} onChange={(event) => field('enabled', event.target.checked)} /><span>启用账号</span></label><label className="checkbox-field"><input type="checkbox" checked={form.auto_checkin} onChange={(event) => field('auto_checkin', event.target.checked)} disabled={form.credential_type === 'api_key'} /><span>自动签到</span></label><label className="checkbox-field"><input type="checkbox" checked={form.auto_refresh} onChange={(event) => field('auto_refresh', event.target.checked)} disabled={form.credential_type === 'api_key'} /><span>自动刷新余额</span></label></div><footer><button className="primary-button" type="submit" disabled={saving || !form.site_id}>{saving ? <RefreshCw className="spin" size={15} /> : <KeyRound size={15} />}{saving ? '保存中' : '保存账号'}</button></footer></form>
}

function CredentialFields({ form, field }: { form: AccountForm; field: (key: keyof AccountForm, value: string | number | boolean) => void }) {
	return <section className="embedded-form-section credential-section"><label><span>凭证类型</span><select value={form.credential_type} onChange={(event) => field('credential_type', event.target.value)}><option value="username_password">账号密码登录</option><option value="access_token">系统访问令牌</option><option value="cookie">Session Cookie</option><option value="api_key">模型 API Key</option></select></label>{form.credential_type === 'username_password' ? <div className="form-grid"><label><span>用户名</span><input required value={form.username} onChange={(event) => field('username', event.target.value)} autoComplete="username" /></label><label><span>密码</span><input required type="password" value={form.password} onChange={(event) => field('password', event.target.value)} autoComplete="current-password" /></label></div> : <label><span>{form.credential_type === 'cookie' ? 'Session Cookie' : form.credential_type === 'api_key' ? '模型 API Key' : '系统访问令牌'}</span><input required type="password" autoComplete="off" value={form.credential} onChange={(event) => field('credential', event.target.value)} placeholder="凭证将加密保存" /></label>}{!['api_key', 'username_password'].includes(form.credential_type) && <label><span>上游用户 ID{form.credential_type === 'cookie' ? '' : '（自动识别失败时填写）'}</span><input required={form.credential_type === 'cookie'} type="number" min="1" value={form.user_id || ''} onChange={(event) => field('user_id', Number(event.target.value))} placeholder="用户个人中心显示的数字 ID" /></label>}{form.credential_type === 'access_token' && <div className="form-help">填写 New API 用户个人中心的系统访问令牌，不是以 sk- 开头的模型调用 Key。</div>}{form.credential_type === 'api_key' && <div className="form-help">模型 API Key 只用于发现模型和路由，不支持余额、签到或公告等账号管理功能。</div>}</section>
}

function accountCredentialPayload(form: AccountForm) {
	if (form.credential_type === 'username_password') return { username: form.username.trim(), password: form.password }
	if (form.credential_type === 'api_key') return { api_key: form.credential }
	if (form.credential_type === 'cookie') return { cookie: form.credential, user_id: form.user_id }
	return { access_token: form.credential, ...(form.user_id > 0 ? { user_id: form.user_id } : {}) }
}

function credentialLabel(type: string): string {
	if (type === 'api_key') return 'API Key'
	if (type === 'cookie') return 'Session Cookie'
	return '站点登录会话'
}
