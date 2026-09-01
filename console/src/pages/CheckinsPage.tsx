import { useCallback, useEffect, useMemo, useState } from 'react'
import { CalendarCheck2, ExternalLink, History, Play, RefreshCw, Users } from 'lucide-react'
import { getCheckinAttemptsBatch, getSiteInventory, peekCheckinAttempts, peekSiteInventory, runAccountTask as executeAccountTask } from '../api'
import type { CheckinAttempt, Site, SiteAccount } from '../types'
import { EmptyState, ErrorState, formatTime, LoadingState, OperationNotice } from './shared'
import { siteErrorMessage, StatusBadge } from './siteShared'

type CheckinView = 'accounts' | 'history'

export default function CheckinsPage() {
  const initialInventory = peekSiteInventory()
  const initialAttempts = peekCheckinAttempts(100)
  const [sites, setSites] = useState<Site[]>(() => initialInventory?.sites || []); const [accounts, setAccounts] = useState<SiteAccount[]>(() => initialInventory?.accounts || []); const [attempts, setAttempts] = useState<CheckinAttempt[]>(() => initialAttempts || [])
  const [view, setView] = useState<CheckinView>('accounts'); const [siteFilter, setSiteFilter] = useState(0); const [status, setStatus] = useState('all'); const [loading, setLoading] = useState(!initialInventory); const [error, setError] = useState(''); const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState<Set<number>>(new Set()); const [bulk, setBulk] = useState<{ done: number; total: number; success: number } | null>(null)
  const [refreshing, setRefreshing] = useState(false)

  const load = useCallback(async (signal?: AbortSignal, options: { silent?: boolean; force?: boolean } = {}) => {
    if (!options.silent && !peekSiteInventory()) setLoading(true)
    setError('')
    try {
      const [inventory, histories] = await Promise.all([
        getSiteInventory(signal, { force: options.force }),
        getCheckinAttemptsBatch(100, signal, { force: options.force }),
      ])
      setSites(inventory.sites)
      setAccounts(inventory.accounts)
      setAttempts([...histories].sort((a, b) => (b.finished_at || b.started_at || 0) - (a.finished_at || a.started_at || 0)))
    } catch (reason) {
      if (!signal?.aborted) setError(siteErrorMessage(reason))
    } finally {
      if (!signal?.aborted && !options.silent) setLoading(false)
    }
  }, [])
  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort() }, [load])
  const siteMap = useMemo(() => new Map(sites.map((site) => [site.id, site])), [sites]); const accountMap = useMemo(() => new Map(accounts.map((account) => [account.id, account])), [accounts])
  const visibleAccounts = useMemo(() => accounts.filter((account) => !siteFilter || account.site_id === siteFilter), [accounts, siteFilter])
  const visibleAttempts = useMemo(() => attempts.filter((attempt) => { const account = accountMap.get(attempt.site_account_id); return (!siteFilter || account?.site_id === siteFilter) && (status === 'all' || attempt.status === status) }), [accountMap, attempts, siteFilter, status])
  const targets = useMemo(() => accounts.filter((account) => account.enabled && account.credential_type !== 'api_key'), [accounts])

  const runOne = async (account: SiteAccount, quiet = false) => { setBusy((current) => new Set(current).add(account.id)); setError(''); try { const result = await executeAccountTask(account.id, 'checkin'); if (!['success', 'partial'].includes(result.status)) throw new Error(result.error || result.status); if (!quiet) { setNotice(`${account.label} 签到完成`); await load(undefined, { silent: true, force: true }) } return true } catch (reason) { if (!quiet) setError(siteErrorMessage(reason)); return false } finally { setBusy((current) => { const next = new Set(current); next.delete(account.id); return next }) } }
  const runBulk = async () => { if (!targets.length) { setError('没有可执行签到的账号'); return } setBulk({ done: 0, total: targets.length, success: 0 }); setError(''); setNotice(''); let cursor = 0; let done = 0; let success = 0; const worker = async () => { while (cursor < targets.length) { const account = targets[cursor++]; if (await runOne(account, true)) success++; done++; setBulk({ done, total: targets.length, success }) } }; await Promise.all(Array.from({ length: Math.min(4, targets.length) }, worker)); setNotice(`签到完成：${success}/${done} 个账号成功`); setBulk(null); await load(undefined, { silent: true, force: true }) }
  const successCount = attempts.filter((item) => item.status === 'success').length; const alreadyCount = attempts.filter((item) => item.status === 'already_checked').length; const attentionCount = accounts.filter((item) => ['expired', 'error'].includes(item.status) || ['failed', 'browser_required'].includes(item.last_checkin_status)).length

  return <div className="workspace-page">
    <header className="page-header"><h1>签到中心</h1><div className="header-controls"><button className="primary-button" type="button" disabled={Boolean(bulk) || !targets.length} onClick={() => void runBulk()}>{bulk ? <RefreshCw className="spin" size={16} /> : <Play size={16} />}{bulk ? `${bulk.done}/${bulk.total}` : '全部签到'}</button><button className="icon-button icon-button--surface" type="button" disabled={refreshing} onClick={async () => { setRefreshing(true); try { await load(undefined, { silent: true, force: true }) } finally { setRefreshing(false) } }} aria-label="刷新签到数据"><RefreshCw size={17} className={refreshing ? 'spin' : undefined} /></button></div></header>
    <section className="compact-summary"><span><strong>{targets.length}</strong>可签到账号</span><span><strong>{successCount}</strong>成功记录</span><span><strong>{alreadyCount}</strong>已签到记录</span><span><strong>{attentionCount}</strong>需要处理</span></section>
    <div className="filter-bar checkin-toolbar"><div className="segmented-control" aria-label="签到视图"><button className={view === 'accounts' ? 'is-active' : ''} type="button" onClick={() => setView('accounts')}><Users size={14} />账号</button><button className={view === 'history' ? 'is-active' : ''} type="button" onClick={() => setView('history')}><History size={14} />记录</button></div><select value={siteFilter} onChange={(event) => setSiteFilter(Number(event.target.value))} aria-label="签到站点"><option value={0}>全部站点</option>{sites.map((site) => <option value={site.id} key={site.id}>{site.name}</option>)}</select>{view === 'history' && <select value={status} onChange={(event) => setStatus(event.target.value)} aria-label="签到状态"><option value="all">全部结果</option><option value="success">成功</option><option value="already_checked">已签到</option><option value="browser_required">需浏览器</option><option value="unsupported">不支持</option><option value="failed">失败</option></select>}<span className="filter-count"><CalendarCheck2 size={14} />{view === 'accounts' ? `${visibleAccounts.length} 个账号` : `${visibleAttempts.length} 条记录`}</span></div>
    {notice && <OperationNotice onDismiss={() => setNotice('')}>{notice}</OperationNotice>}{error && accounts.length > 0 && <OperationNotice tone="error">{error}</OperationNotice>}
    {loading ? <LoadingState label="正在加载签到数据" /> : !accounts.length ? <EmptyState label="请先添加站点账号" /> : view === 'accounts' ? <AccountCheckinList accounts={visibleAccounts} siteMap={siteMap} busy={busy} run={(account) => void runOne(account)} /> : !visibleAttempts.length ? <EmptyState label="当前筛选条件下暂无签到记录" /> : <div className="records-panel checkin-records"><div className="record-head checkin-grid"><span>账号 / 站点</span><span>结果</span><span>日期 / 触发</span><span>奖励或说明</span><span>完成时间</span><span>操作</span></div>{visibleAttempts.map((attempt) => { const account = accountMap.get(attempt.site_account_id); const site = account ? siteMap.get(account.site_id) : undefined; return <CheckinRow key={attempt.id} attempt={attempt} account={account} site={site} busy={Boolean(account && busy.has(account.id))} rerun={() => account && void runOne(account)} /> })}</div>}
  </div>
}

function AccountCheckinList({ accounts, siteMap, busy, run }: { accounts: SiteAccount[]; siteMap: Map<number, Site>; busy: Set<number>; run: (account: SiteAccount) => void }) {
  if (!accounts.length) return <EmptyState label="当前站点没有账号" />
  return <div className="records-panel checkin-account-records"><div className="record-head checkin-account-grid"><span>账号 / 站点</span><span>账号状态</span><span>最近签到</span><span>自动签到</span><span>操作</span></div>{accounts.map((account) => { const site = siteMap.get(account.site_id); const apiKeyOnly = account.credential_type === 'api_key'; const disabled = !account.enabled; const needsCredential = ['expired', 'error'].includes(account.status) || Boolean(account.last_error); return <article className="record-row checkin-account-grid" key={account.id}><div><a className="entity-link" href={`#/accounts?focus_account_id=${account.id}`}><strong>{account.label}</strong></a><a className="entity-chip" href={`#/sites?focus_site_id=${account.site_id}`}>{site?.name || `站点 #${account.site_id}`}</a></div><div><StatusBadge status={account.enabled ? account.status : 'disabled'} />{needsCredential ? <a className="inline-entity-action" href={`#/accounts?focus_account_id=${account.id}&open_credential=1`} title={account.last_error}>{account.last_error ? siteErrorMessage(account.last_error) : '更新凭证'}</a> : <span className="account-error-message">凭证已配置</span>}</div><div><StatusBadge status={apiKeyOnly ? 'unsupported' : account.last_checkin_status} /><span>{apiKeyOnly ? '模型 API Key 不支持签到' : account.last_checkin_at ? formatTime(account.last_checkin_at) : '尚未签到'}</span></div><div><strong>{apiKeyOnly ? '不支持' : account.auto_checkin ? '已开启' : '未开启'}</strong><span>{account.timezone || site?.timezone || '站点时区'}</span></div><div className="checkin-actions">{account.last_checkin_status === 'browser_required' && site?.external_checkin_url && <a className="secondary-button" href={site.external_checkin_url} target="_blank" rel="noreferrer">打开站点<ExternalLink size={12} /></a>}<button className="primary-button" type="button" onClick={() => run(account)} disabled={apiKeyOnly || disabled || busy.has(account.id)}>{busy.has(account.id) ? <RefreshCw className="spin" size={13} /> : <Play size={13} />}{apiKeyOnly ? '不支持签到' : disabled ? '账号已停用' : '立即签到'}</button></div></article> })}</div>
}

function CheckinRow({ attempt, account, site, busy, rerun }: { attempt: CheckinAttempt; account?: SiteAccount; site?: Site; busy: boolean; rerun: () => void }) {
  const delta = attempt.balance_delta
  const reward = delta != null && delta > 0.000001 ? `+${delta.toFixed(2)} ${attempt.balance_currency || account?.balance_currency || ''}` : attempt.reward_text || attempt.message || attempt.error_code || '—'
  const balanceDetail = attempt.balance_before != null && attempt.balance_after != null ? `${attempt.balance_before.toFixed(2)} → ${attempt.balance_after.toFixed(2)}` : attempt.error_code || '无余额变化数据'
  return <article className="record-row checkin-grid"><div><a className="entity-link" href={`#/accounts?focus_account_id=${attempt.site_account_id}${account && ['expired', 'error'].includes(account.status) ? '&open_credential=1' : ''}`}><strong>{account?.label || `账号 #${attempt.site_account_id}`}</strong></a>{account ? <a className="entity-chip" href={`#/sites?focus_site_id=${account.site_id}`}>{site?.name || '未知站点'}</a> : <span>未知站点</span>}<span>{attempt.provider_id}</span></div><div><StatusBadge status={attempt.status} /><span>第 {attempt.attempt_no} 次尝试</span></div><div><strong>{attempt.local_day || '—'}</strong><span>{attempt.trigger_scope || 'manual'}</span></div><div><strong className={delta != null && delta > 0.000001 ? 'balance-delta' : ''} title={reward}>{reward}</strong><span>{balanceDetail}</span></div><div><strong>{attempt.finished_at ? formatTime(attempt.finished_at) : '进行中'}</strong><span>{attempt.started_at ? `开始 ${formatTime(attempt.started_at)}` : '—'}</span></div><div className="checkin-actions">{attempt.status === 'browser_required' && site?.external_checkin_url && <a className="secondary-button" href={site.external_checkin_url} target="_blank" rel="noreferrer">打开站点<ExternalLink size={12} /></a>}<button className="secondary-button" type="button" onClick={rerun} disabled={!account || busy}>{busy ? <RefreshCw className="spin" size={13} /> : null}重试</button></div></article>
}
