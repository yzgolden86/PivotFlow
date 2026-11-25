import { useEffect, useState, useSyncExternalStore } from 'react'
import { getActiveSiteTasks } from '../api'
import OperationDetails from '../components/OperationDetails'
import type { TaskKind } from '../siteTaskRunner'
import { siteTaskRunner } from '../siteTasks'
import { siteErrorMessage } from './siteShared'
import { createSiteTaskDiscovery, routeTaskSummary, siteTaskCompletionKey, siteTaskFeedback, siteTaskLabels, siteTaskRecovery, type SiteTaskScope } from './siteTaskObservation'
import './maintenanceCheckins.css'

const discovery = createSiteTaskDiscovery(getActiveSiteTasks, siteTaskRunner)
export function useSiteTasks() { return useSyncExternalStore(siteTaskRunner.subscribe, siteTaskRunner.snapshot) }
export function useSiteTaskCompletion(kind?: TaskKind) { return siteTaskCompletionKey(useSiteTasks(), kind) }

// Mounted once in the authenticated App shell. This component has no UI.
export default function SiteTaskObserver() {
  useEffect(discovery.mount, [])
  return null
}

// Unfinished work belongs to its originating page's operation notices, with
// recovery for unknown identities. Finished success history is never rendered.
export function SiteTaskNotice(scope: SiteTaskScope) {
  const items = useSiteTasks()
  const { discovering, error } = useSyncExternalStore(discovery.subscribe, discovery.snapshot)
  const { active, unknown, failed } = siteTaskFeedback(items, scope)
  const signature = [...unknown, ...failed].map((item) => `${item.target.key}:${item.taskId}:${item.status}:${item.message}`).join('|') + error
  const [dismissed, setDismissed] = useState<string | null>(null)
  const [actionError, setActionError] = useState('')
  const problems = [...unknown, ...failed]
  const current = unknown[0] || failed.at(-1)
  const cancelFailure = active.find((item) => item.cancelError)
  if (!active.length && ((!current && !error) || (dismissed === signature && !unknown.length))) return null
  const act = async (operation: () => Promise<unknown>) => {
    setActionError('')
    try { await operation() } catch (reason) { setActionError(siteErrorMessage(reason)) }
  }
  const tone = unknown.length || active.length || error || current?.status === 'partial' ? 'warning' : 'error'
  const summary = [
    active.length ? `${active.length} 项操作进行中，离开页面后仍会继续。` : '',
    unknown.length ? `${unknown.length} 项结果待确认；已阻止重复操作，请先核实上游结果。` : '',
    failed.length ? `${failed.length} 项操作失败或部分完成。` : '',
    error ? '活动任务读取失败，请先核实活动任务。' : '',
  ].filter(Boolean).join(' ')
  const detail = actionError || (error ? siteErrorMessage(error) : cancelFailure ? `${cancelFailure.target.title} 取消未确认：${siteErrorMessage(cancelFailure.cancelError)}` : current ? `${current.target.title} · ${siteTaskLabels[current.target.kind]}${current.message ? `：${siteErrorMessage(current.message)}` : ''}` : '')
  return <div className={`operation-notice operation-notice--${tone} maintenance-page-notice`}>
    <span className="operation-notice__content" role={tone === 'error' ? 'alert' : 'status'} aria-live={tone === 'error' ? 'assertive' : 'polite'}>
      <span className="maintenance-page-notice__text" title={[summary, detail].filter(Boolean).join('\n')}>
        <span>{summary}</span>{detail && <span>{detail}</span>}
      </span>
    </span>
    <OperationDetails label="查看结果" ariaLabel="查看逐项操作结果">
      <p className="operation-details-summary">{summary}</p>
        {problems.length > 0 && <ul className="maintenance-page-notice__results" aria-label="逐项操作结果">{problems.map((item) => {
          const recovery = siteTaskRecovery(item)
          const failedRoutes = routeTaskSummary(item).filter((route) => route.status === 'failed')
          return <li key={`${item.target.key}:${item.taskId || ''}`} data-status={item.status}>
            <span><strong>{item.target.title}</strong>{item.target.detail && item.target.detail !== siteTaskLabels[item.target.kind] && <span> · {item.target.detail}</span>} · {siteTaskLabels[item.target.kind]}：{item.status === 'unknown' ? '结果待确认' : item.status === 'partial' ? '部分完成' : '失败'}{item.message && <> · {siteErrorMessage(item.message)}</>}{failedRoutes.length > 0 && <span className="maintenance-page-notice__route-errors"> · 渠道问题：{failedRoutes.map((route) => `${route.name}${route.error ? `（${siteErrorMessage(route.error)}）` : ''}`).join('、')}</span>}</span>
            {recovery && <a className="text-button" href={recovery.href}>{recovery.text}</a>}
          </li>
        })}</ul>}
        {error && <span>活动任务读取失败：{siteErrorMessage(error)}；请先确认活动任务再发起操作。</span>}
        {actionError && <span>{actionError}</span>}
        {cancelFailure && <span>{cancelFailure.target.title} 取消未确认：{siteErrorMessage(cancelFailure.cancelError)}</span>}
      <div className="maintenance-page-notice__actions">
        {active.length > 0 && <button className="secondary-button" type="button" disabled={active.every((item) => item.cancelling || item.cancelRequested)} onClick={() => void act(() => Promise.all(active.map((item) => siteTaskRunner.cancel(item.target.key))))}>取消进行中操作</button>}
        {(unknown.length > 0 || error) && <button className="secondary-button" type="button" disabled={discovering} onClick={() => void act(() => discovery.refresh())}>{discovering ? '读取中' : '重新读取活动任务'}</button>}
        {unknown[0]?.taskId && <button className="secondary-button" type="button" onClick={() => void act(() => siteTaskRunner.resume(unknown[0].target.key))}>重新观察原任务</button>}
        {unknown[0] && <button className="secondary-button" type="button" onClick={() => {
          if (window.confirm('请先核实上游操作结果。移除此记录后可重新发起，可能重复已经完成的操作。确认已核实并移除？')) siteTaskRunner.forgetUnknown(unknown[0].target.key)
        }}>核实后解除锁定</button>}
      </div>
    </OperationDetails>
    {!unknown.length && !active.length && <button className="secondary-button" type="button" onClick={() => setDismissed(signature)}>关闭提示</button>}
  </div>
}
