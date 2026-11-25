import { useEffect, useState, useSyncExternalStore } from 'react'
import { getActiveSiteTasks } from '../api'
import type { TaskKind } from '../siteTaskRunner'
import { siteTaskRunner } from '../siteTasks'
import { siteErrorMessage } from './siteShared'
import { browserImportHref } from '../browserImport'
import { browserImportId } from '../browserImportTypes'
import { createSiteTaskDiscovery, siteTaskCompletionKey, siteTaskFeedback, siteTaskLabels, type SiteTaskScope } from './siteTaskObservation'
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
  const current = unknown[0] || failed.at(-1)
  const cancelFailure = active.find((item) => item.cancelError)
  if (!active.length && ((!current && !error) || (dismissed === signature && !unknown.length))) return null
  const act = async (operation: () => Promise<unknown>) => {
    setActionError('')
    try { await operation() } catch (reason) { setActionError(siteErrorMessage(reason)) }
  }
  const recoveryCode = (current?.message || '').split(':', 1)[0].trim()
  const canRecover = current && browserImportId(current.target.accountId) > 0 && ['browser_required', 'expired'].includes(recoveryCode)
  const tone = unknown.length || active.length || error || current?.status === 'partial' ? 'warning' : 'error'
  return <div className={`operation-notice operation-notice--${tone} maintenance-page-notice`}>
    <span className="operation-notice__content" role={tone === 'error' ? 'alert' : 'status'} aria-live={tone === 'error' ? 'assertive' : 'polite'}>
      <span className="maintenance-page-notice__text">
        {active.length > 0 && <span>{active.length} 项操作进行中，离开页面后仍会继续。</span>}
        {unknown.length > 0 && <span>{unknown.length} 项结果待确认；已阻止重复操作，请先核实上游结果。</span>}
        {failed.length > 0 && <span>{failed.length} 项操作失败或部分完成。</span>}
        {current && <span>{current.target.title} · {siteTaskLabels[current.target.kind]}：{current.message ? siteErrorMessage(current.message) : current.status === 'unknown' ? '结果待确认' : current.status === 'partial' ? '部分完成' : '操作失败'}</span>}
        {error && <span>活动任务读取失败：{siteErrorMessage(error)}；请先确认活动任务再发起操作。</span>}
        {actionError && <span>{actionError}</span>}
        {cancelFailure && <span>{cancelFailure.target.title} 取消未确认：{siteErrorMessage(cancelFailure.cancelError)}</span>}
      </span>
      <span className="maintenance-page-notice__actions">
        {active.length > 0 && <button className="secondary-button" type="button" disabled={active.every((item) => item.cancelling || item.cancelRequested)} onClick={() => void act(() => Promise.all(active.map((item) => siteTaskRunner.cancel(item.target.key))))}>取消进行中操作</button>}
        {(unknown.length > 0 || error) && <button className="secondary-button" type="button" disabled={discovering} onClick={() => void act(() => discovery.refresh())}>{discovering ? '读取中' : '重新读取活动任务'}</button>}
        {unknown[0]?.taskId && <button className="secondary-button" type="button" onClick={() => void act(() => siteTaskRunner.resume(unknown[0].target.key))}>重新观察原任务</button>}
        {unknown[0] && <button className="secondary-button" type="button" onClick={() => {
          if (window.confirm('请先核实上游操作结果。移除此记录后可重新发起，可能重复已经完成的操作。确认已核实并移除？')) siteTaskRunner.forgetUnknown(unknown[0].target.key)
        }}>核实后解除锁定</button>}
        {canRecover && <a className="secondary-button" href={browserImportHref({ accountId: current.target.accountId, siteId: current.target.siteId })}>浏览器修复凭证</a>}
      </span>
    </span>
    {!unknown.length && !active.length && <button className="secondary-button" type="button" onClick={() => setDismissed(signature)}>关闭提示</button>}
  </div>
}
