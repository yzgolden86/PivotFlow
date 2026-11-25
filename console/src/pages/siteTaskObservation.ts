import type { SiteTaskRunner, TaskItem, TaskKind, TaskSnapshot } from '../siteTaskRunner.ts'

export const siteTaskLabels: Record<TaskKind, string> = { refresh: '刷新余额', model_refresh: '同步模型与路由', checkin: '签到', announcement_refresh: '刷新公告' }
const pending = (item: TaskItem) => ['queued', 'submitting', 'running'].includes(item.status)
export type SiteTaskScope = { kinds?: readonly TaskKind[]; siteId?: number; accountIds?: readonly number[] }

export function siteTaskFeedback(items: TaskItem[], scope: SiteTaskScope = {}) {
  const relevant = items.filter((item) => (!scope.kinds || scope.kinds.includes(item.target.kind))
    && (!scope.siteId || !item.target.siteId || item.target.siteId === scope.siteId)
    && (!scope.accountIds || !item.target.accountId || scope.accountIds.includes(item.target.accountId)))
  return {
    active: relevant.filter(pending),
    unknown: relevant.filter((item) => item.status === 'unknown'),
    failed: relevant.filter((item) => ['failed', 'partial'].includes(item.status)),
  }
}

export function siteTaskBlocker(items: TaskItem[], siteId: number, accountId: number): TaskItem | undefined {
  return items.find((item) => item.target.kind !== 'announcement_refresh' && (pending(item) || item.status === 'unknown')
    && (!item.target.siteId || item.target.siteId === siteId)
    && (!item.target.accountId || item.target.accountId === accountId))
}

export function accountRouteTask(items: TaskItem[], accountId: number): TaskItem | undefined {
  const matches = items.filter((item) => item.target.kind === 'model_refresh' && item.target.accountId === accountId)
  const active = matches.filter(pending)
  if (active.length) return active.at(-1)
  return matches.reduce<TaskItem | undefined>((latest, item) =>
    !latest || (item.task?.created_at || 0) >= (latest.task?.created_at || 0) ? item : latest, undefined)
}

export function siteTaskRecovery(item: TaskItem): { text: string; href: string } | null {
  const code = (item.message || '').split(':', 1)[0].trim()
  const accountId = item.target.accountId
  if (['expired', 'credential_required', 'user_id_required'].includes(code) && accountId && Number.isSafeInteger(accountId) && accountId > 0) {
    return { text: '更新凭证', href: `#/accounts?focus_account_id=${accountId}&open_credential=1` }
  }
  if (code === 'browser_required' && Number.isSafeInteger(item.target.siteId) && item.target.siteId > 0) {
    return { text: '查看上游站点', href: `#/sites?focus_site_id=${item.target.siteId}` }
  }
  return null
}

export function routeTaskSummary(item: TaskItem): { name: string; status: string; error?: string }[] {
  return item.task?.progress?.routes || []
}

// An operation from another page is relevant here only when it locks this
// account. Link back to its own recovery controls instead of mounting its panel.
export function checkinBlockerRecovery(items: TaskItem[], siteId: number, accountId: number): { text: string; href: string } | null {
  const blocker = siteTaskBlocker(items, siteId, accountId)
  if (!blocker || blocker.target.kind === 'checkin') return null
  return {
    text: `${siteTaskLabels[blocker.target.kind]}${blocker.status === 'unknown' ? '结果待确认 · 去处理' : '进行中 · 查看'}`,
    href: blocker.target.kind === 'model_refresh' ? '#/channels' : `#/accounts?focus_account_id=${accountId}`,
  }
}

export function siteTaskCompletionKey(items: TaskItem[], kind?: TaskKind): string {
  return items.filter((item) => (!kind || item.target.kind === kind) && ['success', 'partial', 'failed', 'cancelled', 'unknown'].includes(item.status))
    .map((item) => `${item.taskId || item.target.key}:${item.status}`).join('|')
}

// App owns this lifecycle after the admin session has been authenticated.
// Shared leases also guard StrictMode and accidental duplicate observer mounts.
export function createSiteTaskDiscovery(
  read: (signal: AbortSignal) => Promise<TaskSnapshot[]>,
  runner: Pick<SiteTaskRunner, 'observe'>,
  visibility: Pick<Document, 'hidden' | 'addEventListener' | 'removeEventListener'> = document,
) {
  let state = { discovering: false, error: '' }
  const listeners = new Set<() => void>()
  let mounts = 0
  let timer: ReturnType<typeof setInterval> | undefined
  let initial: ReturnType<typeof setTimeout> | undefined
  let controller: AbortController | undefined
  let inFlight: Promise<void> | undefined
  const update = (next: typeof state) => { state = next; listeners.forEach((listener) => listener()) }
  const refresh = (): Promise<void> => {
    if (!mounts || visibility.hidden) return Promise.resolve()
    if (inFlight) return inFlight
    const current = new AbortController()
    controller = current
    update({ ...state, discovering: true })
    const timeout = setTimeout(() => current.abort(new Error('活动任务读取超时，请重新读取确认')), 8_000)
    const work = (async () => {
      try {
        const tasks = await read(current.signal)
        if (current.signal.aborted || controller !== current || !mounts) return
        update({ discovering: false, error: '' })
        for (const task of tasks) {
          if (!Object.hasOwn(siteTaskLabels, task.kind)) continue
          const kind = task.kind as TaskKind
          const siteId = task.site_id || 0; const accountId = task.site_account_id || undefined
          void runner.observe({ key: `${kind}:${siteId}:${accountId || 0}`, kind, siteId, accountId,
            title: accountId ? `账号 #${accountId}` : siteId ? `站点 #${siteId}` : '全部站点', detail: siteTaskLabels[kind] }, task)
        }
      } catch (reason) {
        if (controller === current && mounts) {
          const error = current.signal.aborted ? current.signal.reason : reason
          update({ discovering: false, error: error instanceof Error ? error.message : String(error) })
        }
      } finally {
        clearTimeout(timeout)
        if (controller === current) { controller = undefined; update({ ...state, discovering: false }) }
      }
    })()
    const tracked = work.finally(() => { if (inFlight === tracked) inFlight = undefined })
    inFlight = tracked
    return tracked
  }
  const onVisibility = () => { void refresh() }
  return {
    snapshot: () => state,
    subscribe: (listener: () => void) => { listeners.add(listener); return () => { listeners.delete(listener) } },
    refresh,
    mount: () => {
      if (++mounts === 1) {
        // Defer the first read so StrictMode's immediate cleanup does no I/O.
        initial = setTimeout(onVisibility, 0)
        timer = setInterval(onVisibility, 10_000)
        visibility.addEventListener('visibilitychange', onVisibility)
      }
      let released = false
      return () => {
        if (released) return
        released = true
        if (--mounts) return
        clearTimeout(initial); clearInterval(timer)
        visibility.removeEventListener('visibilitychange', onVisibility)
        controller?.abort(); controller = undefined; inFlight = undefined
        update({ ...state, discovering: false })
      }
    },
  }
}
