import { cancelSiteTask, getSiteTask, invalidateSiteTaskData, refreshAnnouncements, startAccountTask } from './api'
import { SiteTaskRunner, TaskSubmissionRejected, type TaskKind, type TaskTarget } from './siteTaskRunner'

export const siteTaskRunner = new SiteTaskRunner({
  start: async (target) => {
    try { return await (target.kind === 'announcement_refresh'
      ? refreshAnnouncements(target.siteId, AbortSignal.timeout(15_000))
      : startAccountTask(target.accountId!, target.kind, AbortSignal.timeout(15_000))) }
    catch (reason) {
      // These are explicit pre-admission server responses. A timeout, lost
      // response, lease conflict or opaque error remains an unknown outcome.
      if (reason instanceof Error && ['site_task_queue_full', 'server_shutting_down', 'not_found', 'invalid_request'].includes(reason.message)) throw new TaskSubmissionRejected(reason.message)
      throw reason
    }
  },
  read: async (id, signal) => {
    const task = await getSiteTask(id, signal)
    if (['success', 'partial', 'failed', 'cancelled'].includes(task.status)) invalidateSiteTaskData(task.kind)
    return task
  },
  cancel: async (id) => { const result = await cancelSiteTask(id); invalidateSiteTaskData('checkin'); invalidateSiteTaskData('model_refresh'); return result },
})

export function siteTaskTarget(kind: TaskKind, siteId: number, accountId: number | undefined, title: string, detail: string): TaskTarget {
  return { key: `${kind}:${siteId}:${accountId || 0}`, kind, siteId, accountId, title, detail }
}

export async function executeSiteAccountTask(account: { id: number; site_id: number; label: string }, kind: Exclude<TaskKind, 'announcement_refresh'>, siteName: string) {
  const [result] = await siteTaskRunner.run([siteTaskTarget(kind, account.site_id, account.id, account.label, siteName)])
  return { status: result.status, error: result.message || (result.status === 'unknown' ? '任务结果待确认，请在进度区重新观察' : result.status === 'cancelled' ? '任务已取消' : '') }
}
