import { useCallback, useEffect, useMemo, useState } from 'react'
import { CalendarCheck2, ExternalLink, History, Info, Play, RefreshCw, Users } from 'lucide-react'
import { getCheckinAttemptsBatch, getSiteInventory, peekCheckinAttempts, peekSiteInventory } from '../api'
import type { CheckinAttempt, Site, SiteAccount } from '../types'
import { currencySymbol, EmptyState, ErrorState, formatMoneyIn, formatNumber, formatTime, LoadingState, moneyDigits, OperationNotice, PageHeader } from './shared'
import { siteErrorMessage, StatusBadge } from './siteShared'
import { checkinActionAvailability, needsCredentialRecovery, siteCheckinMethodHint } from './siteCheckinMethod'
import { browserTimezone, checkinScheduleDetail, checkinScheduleTitle } from './siteCheckinSchedule'
import { executeSiteAccountTask, siteTaskRunner, siteTaskTarget } from '../siteTasks'
import { SiteTaskNotice, useSiteTaskCompletion, useSiteTasks } from './SiteTaskProgress'
import { checkinBlockerRecovery, siteTaskBlocker } from './siteTaskObservation'
import type { TaskItem } from '../siteTaskRunner'
import { batchNoticeTone, countBatchOutcomes, type NoticeTone } from './batchOutcome'
import './checkinActionLayout.css'
import OperationDetails from '../components/OperationDetails'

type CheckinView = 'accounts' | 'history'

// 浏览器时区只取一次：会话中不会变化，而列表里每一行都要拿它做比较。
const LOCAL_TIMEZONE = browserTimezone()


export default function CheckinsPage() {
  const initialInventory = peekSiteInventory()
  const initialAttempts = peekCheckinAttempts(100)
  const [sites, setSites] = useState<Site[]>(() => initialInventory?.sites || []); const [accounts, setAccounts] = useState<SiteAccount[]>(() => initialInventory?.accounts || []); const [attempts, setAttempts] = useState<CheckinAttempt[]>(() => initialAttempts || [])
  const [view, setView] = useState<CheckinView>('accounts'); const [siteFilter, setSiteFilter] = useState(0); const [status, setStatus] = useState('all'); const [loading, setLoading] = useState(!initialInventory); const [error, setError] = useState(''); const [notice, setNotice] = useState(''); const [noticeTone, setNoticeTone] = useState<NoticeTone>('success')
  const [busy, setBusy] = useState<Set<number>>(new Set()); const [bulk, setBulk] = useState<{ done: number; total: number; success: number } | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const maintenanceTasks = useSiteTasks()
  const taskCompletion = useSiteTaskCompletion()
  const maintenanceBusy = useMemo(() => new Set(accounts.filter((account) => {
    const blocker = siteTaskBlocker(maintenanceTasks, account.site_id, account.id)
    return blocker && blocker.status !== 'unknown'
  }).map((account) => account.id)), [accounts, maintenanceTasks])
  const unconfirmed = useMemo(() => new Set(accounts.filter((account) => siteTaskBlocker(maintenanceTasks, account.site_id, account.id)?.status === 'unknown').map((account) => account.id)), [accounts, maintenanceTasks])

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
  useEffect(() => { if (!taskCompletion) return; const timer = setTimeout(() => void load(undefined, { silent: true, force: true }), 500); return () => clearTimeout(timer) }, [taskCompletion, load])
  const siteMap = useMemo(() => new Map(sites.map((site) => [site.id, site])), [sites]); const accountMap = useMemo(() => new Map(accounts.map((account) => [account.id, account])), [accounts])
  const visibleAccounts = useMemo(() => accounts.filter((account) => !siteFilter || account.site_id === siteFilter), [accounts, siteFilter])
  const visibleAttempts = useMemo(() => attempts.filter((attempt) => { const account = accountMap.get(attempt.site_account_id); return (!siteFilter || account?.site_id === siteFilter) && (status === 'all' || attempt.status === status) }), [accountMap, attempts, siteFilter, status])
  const eligible = useMemo(() => accounts.filter((account) => checkinActionAvailability(account, siteMap.get(account.site_id)).canRunServer && (!siteFilter || account.site_id === siteFilter)), [accounts, siteFilter, siteMap])
  const targets = eligible.filter((account) => !busy.has(account.id) && !siteTaskBlocker(maintenanceTasks, account.site_id, account.id))

  // 签到失败原因要能直接指引下一步动作：不支持签到和需要浏览器验证是两种完全不同的
  // 处理方式，前者只能去上游手动签，后者点开签到页完成验证就能回来重试。
  const checkinFailure = (reason: unknown, account: SiteAccount) => {
    const raw = reason instanceof Error ? reason.message : String(reason || '')
    const separator = raw.indexOf(': ')
    const code = separator > 0 ? raw.slice(0, separator) : raw
    const browserURL = checkinActionAvailability(account, siteMap.get(account.site_id)).manualURL
    if (code === 'unsupported') return browserURL ? '该站点不支持自动签到，可点“打开签到页”手动完成' : '该站点不支持自动签到'
    if (code === 'browser_required') return browserURL ? '该站点需要浏览器验证：点“打开签到页”完成上游操作后回来重试，系统会识别今日已签到' : siteErrorMessage(reason)
    if (code === 'already_checked') return '该账号今日已签到'
    return siteErrorMessage(reason)
  }
  // 「打开签到页」与服务端签到分别由 checkinActionAvailability 判断。
  const runOne = async (account: SiteAccount) => {
    if (siteTaskBlocker(siteTaskRunner.snapshot(), account.site_id, account.id)) return false
    setBusy((current) => new Set(current).add(account.id)); setError(''); setNotice('')
    try {
      const result = await executeSiteAccountTask(account, 'checkin', siteMap.get(account.site_id)?.name || `站点 #${account.site_id}`)
      // Keep the compact row/page summary and the original top-right feedback.
      // Unknown results keep their task identity and never become retryable.
      if (result.status === 'cancelled') {
        setNotice(`${account.label} 签到已取消`)
        setNoticeTone('warning')
      } else if (result.status === 'success') {
        setNotice(`${account.label} 签到完成`)
        setNoticeTone('success')
      } else if (result.status === 'failed' || result.status === 'partial') {
        setNotice(`${account.label}：${checkinFailure(result.error || result.status, account)}`)
        setNoticeTone(result.status === 'partial' ? 'warning' : 'error')
      } else if (result.status === 'unknown') {
        setNotice(`${account.label}：签到结果待确认，请先核实上游结果；已阻止重复操作`)
        setNoticeTone('warning')
      }
      await load(undefined, { silent: true, force: true })
      return result.status === 'success'
    } catch (reason) { setError(`${account.label}：${checkinFailure(reason, account)}`); return false }
    finally { setBusy((current) => { const next = new Set(current); next.delete(account.id); return next }) }
  }
  const runBulk = async () => {
    const admitted = targets.filter((account) => !siteTaskBlocker(siteTaskRunner.snapshot(), account.site_id, account.id))
    if (bulk || !admitted.length) { setError('没有可执行签到的账号，请先确认进行中或待确认的操作'); return }
    setBulk({ done: 0, total: admitted.length, success: 0 }); setError(''); setNotice('')
    try {
      const results = await siteTaskRunner.run(admitted.map((account) => siteTaskTarget('checkin', account.site_id, account.id, account.label, siteMap.get(account.site_id)?.name || `站点 #${account.site_id}`)))
      const counts = countBatchOutcomes(results.map((item) => item.status))
      setNotice(`签到结果：成功 ${counts.success}，部分完成 ${counts.partial}，失败 ${counts.failed}，取消 ${counts.cancelled}，待确认 ${counts.unknown}${eligible.length > admitted.length ? `，跳过 ${eligible.length - admitted.length} 个进行中或待确认账号` : ''}`)
      setNoticeTone(batchNoticeTone(counts))
      await load(undefined, { silent: true, force: true })
    } catch (reason) {
      setError(`批量签到未完成：${siteErrorMessage(reason)}；请刷新后核实各账号结果`)
    } finally { setBulk(null) }
  }
  const successCount = attempts.filter((item) => item.status === 'success').length; const alreadyCount = attempts.filter((item) => item.status === 'already_checked').length; const attentionCount = accounts.filter((item) => ['expired', 'error'].includes(item.status) || ['failed', 'browser_required'].includes(item.last_checkin_status)).length

  return <div className="workspace-page checkin-workspace">
    <PageHeader
      icon={CalendarCheck2}
      title="签到中心"
      tone="blue"
      actions={<><button className="primary-button" type="button" disabled={Boolean(bulk) || !targets.length} onClick={() => void runBulk()}>{bulk ? <RefreshCw className="spin" size={16} /> : <Play size={16} />}{bulk ? '签到进行中' : '全部签到'}</button><button className="icon-button icon-button--surface" type="button" disabled={refreshing} onClick={async () => { setRefreshing(true); try { await load(undefined, { silent: true, force: true }) } finally { setRefreshing(false) } }} aria-label="刷新签到数据"><RefreshCw size={17} className={refreshing ? 'spin' : undefined} /></button></>}
    />
    <SiteTaskNotice kinds={['checkin']} siteId={siteFilter} />
    <section className="compact-summary"><span><strong>{targets.length}</strong>可签到账号</span><span><strong>{successCount}</strong>成功记录</span><span><strong>{alreadyCount}</strong>已签到记录</span><span><strong>{attentionCount}</strong>需要处理</span></section>
    <div className="filter-bar checkin-toolbar"><div className="segmented-control" aria-label="签到视图"><button className={view === 'accounts' ? 'is-active' : ''} type="button" onClick={() => setView('accounts')}><Users size={14} />账号</button><button className={view === 'history' ? 'is-active' : ''} type="button" onClick={() => setView('history')}><History size={14} />记录</button></div><select value={siteFilter} onChange={(event) => setSiteFilter(Number(event.target.value))} aria-label="签到站点"><option value={0}>全部站点</option>{sites.map((site) => <option value={site.id} key={site.id}>{site.name}</option>)}</select>{view === 'history' && <select value={status} onChange={(event) => setStatus(event.target.value)} aria-label="签到状态"><option value="all">全部结果</option><option value="success">成功</option><option value="already_checked">已签到</option><option value="browser_required">需浏览器</option><option value="unsupported">不支持</option><option value="failed">失败</option></select>}<span className="filter-count"><CalendarCheck2 size={14} />{view === 'accounts' ? `${visibleAccounts.length} 个账号` : `${visibleAttempts.length} 条记录`}</span></div>
    {notice && <OperationNotice tone={noticeTone} persistent={noticeTone !== 'success'} onDismiss={() => setNotice('')}>{notice}</OperationNotice>}{error && accounts.length > 0 && <OperationNotice tone="error">{error}</OperationNotice>}
    {loading ? <LoadingState label="正在加载签到数据" /> : error && !accounts.length ? <ErrorState message={error} retry={() => void load(undefined, { force: true })} /> : !accounts.length ? <EmptyState label="请先添加站点账号" /> : view === 'accounts' ? <AccountCheckinList accounts={visibleAccounts} siteMap={siteMap} busy={new Set([...busy, ...maintenanceBusy])} unconfirmed={unconfirmed} tasks={maintenanceTasks} run={(account) => void runOne(account)} /> : !visibleAttempts.length ? <EmptyState label="当前筛选条件下暂无签到记录" /> : <div className="records-panel checkin-records"><div className="record-head checkin-grid"><span>账号 / 站点</span><span>结果</span><span>日期 / 触发</span><span>状态说明</span><span>完成时间</span><span>操作</span></div>{visibleAttempts.map((attempt) => { const account = accountMap.get(attempt.site_account_id); const site = account ? siteMap.get(account.site_id) : undefined; return <CheckinRow key={attempt.id} tasks={maintenanceTasks} attempt={attempt} account={account} site={site} busy={Boolean(account && (busy.has(account.id) || maintenanceBusy.has(account.id)))} unconfirmed={Boolean(account && unconfirmed.has(account.id))} rerun={() => account && void runOne(account)} /> })}</div>}
  </div>
}

function AccountCheckinList({ accounts, siteMap, busy, unconfirmed, tasks, run }: { accounts: SiteAccount[]; siteMap: Map<number, Site>; busy: Set<number>; unconfirmed: Set<number>; tasks: TaskItem[]; run: (account: SiteAccount) => void }) {
  if (!accounts.length) return <EmptyState label="当前站点没有账号" />
  return <div className="records-panel checkin-account-records">
    <div className="record-head checkin-account-grid"><span>账号 / 站点</span><span>账号状态</span><span>最近签到</span><span>自动签到</span><span>操作</span></div>
    {accounts.map((account) => {
      const site = siteMap.get(account.site_id)
      const apiKeyOnly = account.credential_type === 'api_key'
      const accountReason = account.last_error ? siteErrorMessage(account.last_error) : account.status === 'expired' ? '登录凭证已过期' : account.status === 'error' ? '请核对账号凭证' : account.credential_configured ? '凭证已配置' : '尚未配置凭证'
      return <article className="record-row checkin-account-grid" key={account.id}>
        <div><a className="entity-link" href={`#/accounts?focus_account_id=${account.id}`}><strong>{account.label}</strong></a><a className="entity-chip" href={`#/sites?focus_site_id=${account.site_id}`}>{site?.name || `站点 #${account.site_id}`}</a></div>
        <div><StatusBadge status={account.enabled ? account.status : 'disabled'} />{account.last_error || ['expired', 'error'].includes(account.status)
          ? <OperationDetails className="account-error-message" label={accountReason} ariaLabel={`查看 ${account.label} 状态说明`} title={accountReason}><p className="operation-details-summary">{accountReason}</p></OperationDetails>
          : <span className="account-error-message" title={accountReason}>{accountReason}</span>}</div>
        <div className="checkin-recent"><StatusBadge status={apiKeyOnly ? 'unsupported' : account.last_checkin_status} /><span>{apiKeyOnly ? '模型 API Key 不支持签到' : account.last_checkin_at ? formatTime(account.last_checkin_at) : '尚未签到'}</span></div>
        <div className="checkin-schedule"><strong>{apiKeyOnly ? '不支持' : account.auto_checkin ? '已开启' : '未开启'}</strong><span title={checkinScheduleTitle(siteCheckinMethodHint(site), account.timezone || site?.timezone, LOCAL_TIMEZONE)}>{checkinScheduleDetail(siteCheckinMethodHint(site), account.timezone || site?.timezone, LOCAL_TIMEZONE)}</span></div>
        <CheckinActions account={account} site={site} busy={busy.has(account.id)} unconfirmed={unconfirmed.has(account.id)} tasks={tasks} run={() => run(account)} />
      </article>
    })}
  </div>
}

function CheckinRow({ attempt, account, site, busy, unconfirmed, tasks, rerun }: { attempt: CheckinAttempt; account?: SiteAccount; site?: Site; busy: boolean; unconfirmed: boolean; tasks: TaskItem[]; rerun: () => void }) {
  const delta = attempt.balance_delta; const currency = attempt.balance_currency || account?.balance_currency; const digits = moneyDigits([delta, attempt.balance_before, attempt.balance_after])
  // 奖励是增量，永远为正，所以不走 formatMoneyIn 的「< ¥0.01」分支 —— 那会拼出
  // 「+< ¥0.0001」这种读不通的东西。余额那两个才用 formatMoneyIn。
  const reward = delta != null && delta > 0.000001 ? `+${currencySymbol(currency) || '$'}${formatNumber(delta, digits)}` : attempt.reward_text || attempt.message || attempt.error_code || '—'
  const balanceDetail = attempt.balance_before != null && attempt.balance_after != null ? `${formatMoneyIn(attempt.balance_before, currency, digits)} → ${formatMoneyIn(attempt.balance_after, currency, digits)}` : attempt.error_code || '无余额变化数据'
  return <article className="record-row checkin-grid"><div><a className="entity-link" href={`#/accounts?focus_account_id=${attempt.site_account_id}`}><strong>{account?.label || `账号 #${attempt.site_account_id}`}</strong></a>{account ? <a className="entity-chip" href={`#/sites?focus_site_id=${account.site_id}`}>{site?.name || '未知站点'}</a> : <span>未知站点</span>}<span>{attempt.provider_id}</span></div><div><StatusBadge status={attempt.status} /><span>第 {attempt.attempt_no} 次尝试</span></div><div><strong>{attempt.local_day || '—'}</strong><span>{attempt.trigger_scope || 'manual'}</span></div><div><strong className={delta != null && delta > 0.000001 ? 'balance-delta' : ''} title={reward}>{reward}</strong><span>{balanceDetail}</span></div><div><strong>{attempt.finished_at ? formatTime(attempt.finished_at) : '进行中'}</strong><span>{attempt.started_at ? `开始 ${formatTime(attempt.started_at)}` : '—'}</span></div><CheckinActions account={account} site={site} attempt={attempt} busy={busy} unconfirmed={unconfirmed} tasks={tasks} run={rerun} /></article>
}

function CheckinActions({ account, site, attempt, busy, unconfirmed, tasks, run }: { account?: SiteAccount; site?: Site; attempt?: CheckinAttempt; busy: boolean; unconfirmed: boolean; tasks: TaskItem[]; run: () => void }) {
  const availability = account ? checkinActionAvailability(attempt ? { ...account, last_checkin_status: attempt.status } : account, site) : null
  const blocker = account ? siteTaskBlocker(tasks, account.site_id, account.id) : undefined
  // An older history failure must not force a repaired account back into recovery.
  const credentialRejected = Boolean(account && needsCredentialRecovery(account))
  // A historical attempt can remain expired after the account has been fixed.
  // Only current account state/error may open the credential editor; otherwise
  // an old row would make a healthy account look broken again.
  const needsCredential = credentialRejected
  const browserRequired = site?.checkin_method === 'turnstile' || (attempt?.status || account?.last_checkin_status) === 'browser_required'
  const recoveryInPrimary = credentialRejected && availability?.canRunServer && !busy && !unconfirmed && !blocker
  const reason = availability?.reason || '账号已不存在，无法重试'
  const reasonLabel = reason === '模型 API Key 不支持服务端签到' ? 'API Key 不支持签到' : reason === '该平台不支持服务端签到' ? '平台不支持签到' : reason
  const lockedReason = unconfirmed || blocker?.status === 'unknown' ? '原操作结果待确认，请先重新观察或核实上游结果' : busy || blocker ? '账号有维护操作进行中，请等待完成后重试' : ''
  const blockerRecovery = account ? checkinBlockerRecovery(tasks, account.site_id, account.id) : null
  return <div className="checkin-actions checkin-action-layout">
    {availability?.manualURL
      ? <a className="secondary-button checkin-action-open" href={availability.manualURL} target="_blank" rel="noreferrer" title="在上游站点手动完成签到或验证">打开签到页<ExternalLink size={12} /></a>
      : <span className="checkin-action-open checkin-action-empty" aria-hidden="true" />}
    {!availability?.canRunServer
      ? <span className="checkin-unsupported checkin-action-primary" title={reason}><Info size={13} /><span>{reasonLabel}</span></span>
      : recoveryInPrimary && account
        ? <CheckinRecoveryLink tasks={tasks} account={account} credential primary />
        : <button className={`${attempt ? 'secondary-button' : 'primary-button'} checkin-action-primary`} type="button" onClick={run} disabled={busy || unconfirmed || Boolean(blocker)} title={lockedReason || (browserRequired ? '完成上游浏览器验证后可重试，系统会识别今日已签到' : undefined)}>{busy ? <RefreshCw className="spin" size={13} /> : <Play size={13} />}{attempt ? '重试' : '立即签到'}</button>}
    {lockedReason && (needsCredential || !blockerRecovery)
      ? <span className="checkin-action-help" title={lockedReason}><Info size={13} /><span>{unconfirmed || blocker?.status === 'unknown' ? '结果待确认，请先核实' : '维护操作进行中'}</span></span>
      : browserRequired && availability?.canRunServer && !recoveryInPrimary
        ? <span className="checkin-action-help"><Info size={13} /><span>{availability.manualURL ? '需浏览器验证后重试' : '需在上游完成浏览器验证后重试'}</span></span>
        : null}
    {account && !recoveryInPrimary && (needsCredential || blockerRecovery) && <CheckinRecoveryLink tasks={tasks} account={account} credential={needsCredential} />}
  </div>
}

function CheckinRecoveryLink({ tasks, account, credential = false, primary = false }: { tasks: TaskItem[]; account: SiteAccount; credential?: boolean; primary?: boolean }) {
  const recovery = checkinBlockerRecovery(tasks, account.site_id, account.id)
  if (credential) return <a className={`checkin-blocker-recovery${primary ? ' secondary-button checkin-action-primary' : ''}`} href={`#/accounts?focus_account_id=${account.id}&open_credential=1`} title={account.last_error ? siteErrorMessage(account.last_error) : account.status === 'expired' ? '登录凭证已过期，请在账号管理中更新凭证' : '请在账号管理中核对并更新登录凭证'}><Info size={13} /><span>更新凭证</span></a>
  return recovery ? <a className="checkin-blocker-recovery" href={recovery.href} title={recovery.text}><Info size={13} /><span>{recovery.text}</span></a> : null
}
