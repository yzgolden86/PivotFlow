export type TaskKind = 'refresh' | 'model_refresh' | 'checkin' | 'announcement_refresh'
export type TaskStatus = 'queued' | 'running' | 'success' | 'partial' | 'failed' | 'cancelled' | 'unknown'
export type TaskTarget = { key: string; kind: TaskKind; accountId?: number; siteId: number; title: string; detail: string }
export type TaskSnapshot = {
  id: string; kind: string; status: TaskStatus; created_at: number; error?: string
  site_id?: number; site_account_id?: number
  budget?: { active: number; waiting: number; cooldown_until?: number; next_request_at?: number } | null
  progress?: { completed?: number; total?: number; items?: { site_id: number; task_id?: string; status: string; error?: string }[] }
}
export type TaskItem = {
  target: TaskTarget; taskId?: string; status: TaskStatus | 'submitting' | 'unknown'
  message?: string; cancelError?: string; cancelRequested: boolean; cancelling: boolean; merged?: boolean; task?: TaskSnapshot
}
export type TaskAPI = {
  start: (target: TaskTarget) => Promise<{ task_id: string; merged?: boolean }>
  read: (id: string, signal: AbortSignal) => Promise<TaskSnapshot>
  cancel: (id: string) => Promise<{ cancelled: boolean; status: TaskStatus }>
  delay?: () => Promise<void>
}
type Job = { item: TaskItem; done: Promise<TaskItem>; resolve: (value: TaskItem) => void; settled: boolean; processing: boolean; cancellation?: Promise<void> }
const terminal = (status: string) => ['success', 'partial', 'failed', 'cancelled'].includes(status)
const message = (reason: unknown) => reason instanceof Error ? reason.message : String(reason)
export class TaskSubmissionRejected extends Error {}

// One instance belongs to the browser session. Navigation does not cancel the
// server task or lose its ID. A page reload recovers active IDs through the API.
export class SiteTaskRunner {
  private api: TaskAPI
  private jobs = new Map<string, Job>()
  private queue: Job[] = []
  private active = 0
  private listeners = new Set<() => void>()
  private view: TaskItem[] = []

  constructor(api: TaskAPI) { this.api = api }
  snapshot = (): TaskItem[] => this.view
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener) } }
  private emit() { this.view = [...this.jobs.values()].map((job) => ({ ...job.item })); this.listeners.forEach((listener) => listener()) }
  private create(item: TaskItem): Job {
    let resolve!: (value: TaskItem) => void
    const done = new Promise<TaskItem>((r) => { resolve = r })
    return { item, done, resolve, settled: false, processing: false }
  }
  private enqueue(job: Job) {
    this.jobs.set(job.item.target.key, job)
    this.queue.push(job)
    // Retain at most 200 settled results; all pending and unknown identities
    // remain available, even for larger batches.
    let settled = [...this.jobs.values()].filter((entry) => entry.settled && terminal(entry.item.status)).length
    for (const [key, entry] of this.jobs) { if (settled <= 200) break; if (entry.settled && terminal(entry.item.status)) { this.jobs.delete(key); settled-- } }
    this.emit(); this.pump()
  }
  private pump() {
    while (this.active < 4 && this.queue.length) {
      const job = this.queue.shift()!
      if (job.item.status === 'cancelled' && !job.item.taskId) { this.finish(job); continue }
      this.active++; job.processing = true
      void this.execute(job).finally(() => { this.active--; this.finish(job); this.pump() })
    }
  }
  private finish(job: Job) { job.processing = false; job.settled = true; this.emit(); job.resolve({ ...job.item }) }

  run(targets: TaskTarget[]): Promise<TaskItem[]> {
    return Promise.all(targets.map((target) => {
      const previous = this.jobs.get(target.key)
      // An interrupted observation is not evidence that work never happened.
      if (previous && (!previous.settled || previous.item.status === 'unknown')) return previous.done
      const job = this.create({ target, status: 'queued', cancelRequested: false, cancelling: false })
      this.enqueue(job); return job.done
    }))
  }
  retryFailed(): Promise<TaskItem[]> {
    return this.run(this.view.filter((item) => ['failed', 'partial'].includes(item.status) && item.target.siteId > 0).map((item) => item.target))
  }
  resume(key: string): Promise<TaskItem | undefined> {
    const previous = this.jobs.get(key)
    if (!previous || !previous.item.taskId || previous.item.status !== 'unknown') return Promise.resolve(undefined)
    const job = this.create({ ...previous.item, status: 'queued', message: undefined, cancelRequested: false, cancelling: false })
    this.enqueue(job); return job.done
  }
  observe(target: TaskTarget, snapshot: TaskSnapshot): Promise<TaskItem> {
    const sameID = [...this.jobs.values()].find((job) => job.item.taskId === snapshot.id)
    if (sameID) {
      if (sameID.item.status === 'unknown') return this.resume(sameID.item.target.key).then((item) => item || sameID.item)
      return sameID.done
    }
    const previous = this.jobs.get(target.key)
    if (previous && !previous.settled) {
      if (!previous.item.taskId) { previous.item.taskId = snapshot.id; previous.item.task = snapshot; this.emit(); return previous.done }
      target = { ...target, key: `${target.key}:${snapshot.id}` }
    }
    const job = this.create({ target, taskId: snapshot.id, task: snapshot, status: snapshot.status, cancelRequested: false, cancelling: false })
    this.enqueue(job); return job.done
  }
  async cancel(key: string): Promise<void> {
    const job = this.jobs.get(key)
    if (!job || terminal(job.item.status)) return
    if (job.item.status === 'unknown' && !job.item.taskId) return
    job.item.cancelRequested = true
    if (!job.item.taskId) {
      if (!job.processing) job.item.status = 'cancelled'
      this.emit(); return
    }
    if (job.cancellation) return job.cancellation
    job.item.cancelling = true; job.item.cancelError = undefined; this.emit()
    job.cancellation = (async () => {
      try {
        const result = await this.api.cancel(job.item.taskId!)
        if (result.cancelled) job.item.status = 'cancelled'
        else if (terminal(result.status)) job.item.status = result.status
        else job.item.cancelError = '取消尚未确认，请重新观察或再次取消'
      } catch (reason) { job.item.cancelError = message(reason) }
      finally { job.item.cancelling = false; job.item.cancelRequested = false; job.cancellation = undefined; this.emit() }
    })()
    return job.cancellation
  }
  cancelAll(): Promise<void[]> { return Promise.all(this.view.filter((item) => !terminal(item.status)).map((item) => this.cancel(item.target.key))) }
  clearFinished() { for (const [key, job] of this.jobs) if (job.settled && terminal(job.item.status)) this.jobs.delete(key); this.emit() }
  forgetUnknown(key: string) { const job = this.jobs.get(key); if (job?.settled && job.item.status === 'unknown') { this.jobs.delete(key); this.emit() } }

  private async execute(job: Job): Promise<void> {
    try {
      if (!job.item.taskId) {
        job.item.status = 'submitting'; this.emit()
        const submitted = await this.api.start(job.item.target)
        if (!submitted.task_id) throw new Error('任务提交响应缺少 ID，结果未知')
        job.item.taskId = submitted.task_id; job.item.merged = submitted.merged; job.item.status = 'queued'; this.emit()
      }
      if (job.item.cancelRequested) await this.cancel(job.item.target.key)
      const deadline = Date.now() + 13 * 60_000
      while (!terminal(job.item.status)) {
        if (Date.now() >= deadline) throw new Error('任务观察已超时，请重新观察原任务')
        const snapshot = await this.api.read(job.item.taskId!, AbortSignal.timeout(8_000))
        // A completed cancel response is authoritative even if an older GET
        // response arrived later. A cancellation refusal keeps the real result.
        if (terminal(job.item.status)) return
        if (snapshot.status === 'unknown') { job.item.task = snapshot; job.item.status = 'unknown'; job.item.message = snapshot.error || 'observation_interrupted'; this.emit(); return }
        if (!['queued', 'running', 'success', 'partial', 'failed', 'cancelled'].includes(snapshot.status)) throw new Error('任务状态无法识别')
        job.item.task = snapshot; job.item.status = snapshot.status; job.item.message = snapshot.error; this.emit()
        if (!terminal(snapshot.status)) await (this.api.delay?.() || new Promise<void>((resolve) => setTimeout(resolve, 1_000)))
      }
    } catch (reason) {
      if (!terminal(job.item.status)) { job.item.status = !job.item.taskId && reason instanceof TaskSubmissionRejected ? 'failed' : 'unknown'; job.item.message = message(reason); this.emit() }
    }
  }
}
