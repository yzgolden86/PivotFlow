import { useEffect, useState, useSyncExternalStore } from 'react'
import { RefreshCw, Square, X } from 'lucide-react'
import { getActiveSiteTasks } from '../api'
import type { TaskKind } from '../siteTaskRunner'
import { siteTaskRunner, siteTaskTarget } from '../siteTasks'
import { siteErrorMessage, StatusBadge } from './siteShared'
import { browserImportHref } from '../browserImport'
import { browserImportId } from '../browserImportTypes'

const labels: Record<TaskKind, string> = { refresh: '刷新余额', model_refresh: '同步模型与路由', checkin: '签到', announcement_refresh: '刷新公告' }
export function useSiteTasks() { return useSyncExternalStore(siteTaskRunner.subscribe, siteTaskRunner.snapshot) }
export function useSiteTaskCompletion(kind?: TaskKind) {
  return useSiteTasks().filter((item) => (!kind || item.target.kind === kind) && ['success', 'partial', 'failed', 'cancelled'].includes(item.status)).map((item) => `${item.taskId || item.target.key}:${item.status}`).join('|')
}

export default function SiteTaskProgress() {
  const items = useSiteTasks()
  const [discoveryError, setDiscoveryError] = useState('')
  const [discovering, setDiscovering] = useState(false)
  const discover = async (signal?: AbortSignal) => {
    setDiscovering(true)
    try {
      const tasks = await getActiveSiteTasks(signal || AbortSignal.timeout(8_000))
      if (signal?.aborted) return
      setDiscoveryError('')
      for (const task of tasks) {
        if (!(task.kind in labels)) continue
        const target = siteTaskTarget(task.kind as TaskKind, task.site_id || 0, task.site_account_id || undefined,
          task.site_account_id ? `账号 #${task.site_account_id}` : task.site_id ? `站点 #${task.site_id}` : '全部站点', labels[task.kind as TaskKind])
        void siteTaskRunner.observe(target, task)
      }
    } catch (reason) { if (!signal?.aborted) setDiscoveryError(siteErrorMessage(reason)) }
    finally { if (!signal?.aborted) setDiscovering(false) }
  }
  useEffect(() => {
    let controller: AbortController | null = null
    const refresh = () => { if (document.hidden) return; controller?.abort(); controller = new AbortController(); const current = controller; const timeout = setTimeout(() => current.abort(), 8_000); void discover(current.signal).finally(() => clearTimeout(timeout)) }
    refresh()
    const timer = setInterval(refresh, 10_000)
    document.addEventListener('visibilitychange', refresh)
    return () => { clearInterval(timer); controller?.abort(); document.removeEventListener('visibilitychange', refresh) }
  }, [])

  const active = items.filter((item) => ['queued', 'submitting', 'running'].includes(item.status)).length
  const failed = items.filter((item) => ['failed', 'partial'].includes(item.status) && item.target.siteId > 0).length
  const unknown = items.filter((item) => item.status === 'unknown').length
  if (!items.length && !discoveryError) return null
  return <section className="site-task-progress" aria-label="站点维护任务">
    <header>
      <div><strong>站点维护任务</strong><span aria-live="polite">{active ? `${active} 项进行中` : '本次任务已结束'}{unknown ? ` · ${unknown} 项结果待确认` : ''}</span></div>
      <div className="site-task-actions">
        {active > 0 && <button type="button" className="secondary-button" onClick={() => void siteTaskRunner.cancelAll()}><Square size={13} />取消全部</button>}
        {failed > 0 && <button type="button" className="secondary-button" onClick={() => void siteTaskRunner.retryFailed()} disabled={active > 0}><RefreshCw size={13} />仅重试失败（{failed}）</button>}
        <button type="button" className="secondary-button" onClick={() => void discover()} disabled={discovering}><RefreshCw size={13} className={discovering ? 'spin' : undefined} />发现活动任务</button>
        {items.some((item) => ['success', 'partial', 'failed', 'cancelled'].includes(item.status)) && <button type="button" className="icon-button icon-button--surface" onClick={() => siteTaskRunner.clearFinished()} aria-label="清除已结束结果"><X size={15} /></button>}
      </div>
    </header>
    <p className="site-task-hint">同站点排队，共享限速。离开页面后服务器任务继续；取消不会撤回已完成的上游操作。部分成功也可单独重试。</p>
    {discoveryError && <p className="inline-error">活动任务读取失败：{discoveryError}</p>}
    <div className="site-task-results" tabIndex={0} role="region" aria-label="逐项任务结果">
      {items.map((item) => {
        const cooldownUntil = item.task?.budget?.cooldown_until || 0
        const recoveryCode = (item.message || item.task?.error || '').split(':', 1)[0].trim()
        const canRecover = ['failed', 'partial'].includes(item.status) && browserImportId(item.target.accountId) > 0 && ['browser_required', 'expired'].includes(recoveryCode)
        return <article key={item.target.key} className="site-task-row">
        <div className="site-task-identity"><strong>{item.target.title}</strong><span>{labels[item.target.kind]} · {item.target.detail}</span>{item.taskId && <code title={item.taskId}>{item.taskId}</code>}</div>
        <div>{item.status === 'unknown' ? <span className="site-task-unknown">结果待确认</span> : <StatusBadge status={item.status === 'submitting' ? 'pending' : item.status} />}{item.merged && <small>已合并</small>}</div>
        <div className="site-task-message"><span>{item.cancelError ? `取消未确认：${item.cancelError}` : item.message ? siteErrorMessage(item.message) : item.status === 'queued' ? '等待同站点任务或执行名额' : item.status === 'submitting' ? '正在提交任务' : item.status === 'running' ? '上游处理中' : ''}</span>{cooldownUntil > Date.now() && <span>站点共享冷却至 {new Date(cooldownUntil).toLocaleTimeString()}</span>}{item.status === 'unknown' && <span>{item.taskId ? '请重新观察原任务，避免重复操作' : '提交结果未知，请发现活动任务；重新发起前先核实上游结果'}</span>}</div>
        <div className="site-task-actions">{canRecover && <a className="secondary-button" href={browserImportHref({ accountId: item.target.accountId, siteId: item.target.siteId })}>浏览器修复凭证</a>}{item.status === 'unknown' && item.taskId && <button type="button" className="secondary-button" onClick={() => void siteTaskRunner.resume(item.target.key)}>重新观察</button>}{item.status === 'unknown' && <button type="button" className="secondary-button" title="仅移除本地结果，不会取消服务器任务" onClick={() => { if (window.confirm('请先核实上游操作结果。移除此记录后可重新发起，可能重复已经完成的操作。确认已核实并移除？')) siteTaskRunner.forgetUnknown(item.target.key) }}>核实后移除</button>}{['queued', 'submitting', 'running', 'unknown'].includes(item.status) && <button type="button" className="secondary-button" disabled={item.cancelling || item.cancelRequested || (item.status === 'unknown' && !item.taskId)} onClick={() => void siteTaskRunner.cancel(item.target.key)}>{item.cancelling || item.cancelRequested ? '取消中' : '取消'}</button>}</div>
      </article>})}
    </div>
  </section>
}
