import assert from 'node:assert/strict'
import { test } from 'node:test'
import { SiteTaskRunner, TaskSubmissionRejected, type TaskTarget, type TaskAPI, type TaskSnapshot } from './siteTaskRunner.ts'

const target = (id: number): TaskTarget => ({ key: `refresh:${id}`, kind: 'refresh', accountId: id, siteId: 1, title: `账号 ${id}`, detail: '合成站点' })
const task = (id: string, status: TaskSnapshot['status']): TaskSnapshot => ({ id, kind: 'refresh', status, created_at: 1 })
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>((r) => { resolve = r }); return { promise, resolve } }
const tick = () => new Promise<void>((resolve) => setImmediate(resolve))

test('重复提交共享任务，完成后仅重试确认失败和部分成功项', async () => {
  const gate = deferred<void>(); const submitted: number[] = []; let retry = false
  const api: TaskAPI = {
    start: async (item) => { submitted.push(item.accountId!); await gate.promise; return { task_id: `st_${item.accountId}` } },
    read: async (id) => task(id, retry || id === 'st_1' ? 'success' : id === 'st_2' ? 'failed' : 'partial'),
    cancel: async () => ({ cancelled: true, status: 'cancelled' }),
  }
  const runner = new SiteTaskRunner(api)
  const first = runner.run([target(1), target(2), target(3)])
  const duplicate = runner.run([target(1)])
  gate.resolve(); await first; await duplicate
  assert.deepEqual(submitted.sort(), [1, 2, 3])
  retry = true; await runner.retryFailed()
  assert.deepEqual(submitted, [1, 2, 3, 2, 3])
  assert.ok(runner.snapshot().every((item) => item.status === 'success'))
})

test('提交响应之前取消，拿到真实 task ID 后发送服务器取消', async () => {
  const submission = deferred<{ task_id: string }>(); const cancelled: string[] = []
  const runner = new SiteTaskRunner({ start: () => submission.promise, read: async (id) => task(id, 'running'), cancel: async (id) => { cancelled.push(id); return { cancelled: true, status: 'cancelled' } } })
  const work = runner.run([target(1)])
  void runner.cancel(target(1).key)
  submission.resolve({ task_id: 'st_real' }); await work
  assert.deepEqual(cancelled, ['st_real'])
  assert.equal(runner.snapshot()[0].taskId, 'st_real')
  assert.equal(runner.snapshot()[0].status, 'cancelled')
})

test('整批取消不提交本地排队项，服务器取消失败不伪装已取消', async () => {
  const gate = deferred<void>(); const started: number[] = []
  const runner = new SiteTaskRunner({ start: async (item) => { started.push(item.accountId!); await gate.promise; return { task_id: `st_${item.accountId}` } }, read: async (id) => task(id, 'success'), cancel: async () => { throw new Error('cancel transport failed') } })
  const work = runner.run([1, 2, 3, 4, 5].map(target))
  void runner.cancelAll(); gate.resolve(); await work
  assert.equal(started.length, 4)
  assert.equal(runner.snapshot().find((item) => item.target.accountId === 5)?.status, 'cancelled')
  assert.equal(runner.snapshot()[0].status, 'success')
  assert.match(runner.snapshot()[0].cancelError || '', /cancel transport failed/)
  await runner.retryFailed(); assert.equal(started.length, 4)
})

test('轮询中断保留任务身份，重新观察不创建新任务', async () => {
  let submissions = 0; let failed = true
  const runner = new SiteTaskRunner({ start: async () => { submissions++; return { task_id: 'st_known' } }, read: async (id) => { if (failed) throw new Error('offline'); return task(id, 'success') }, cancel: async () => ({ cancelled: true, status: 'cancelled' }) })
  await runner.run([target(1)])
  assert.equal(runner.snapshot()[0].status, 'unknown')
  assert.equal(runner.snapshot()[0].taskId, 'st_known')
  await runner.retryFailed(); assert.equal(submissions, 1)
  failed = false; await runner.resume(target(1).key)
  assert.equal(submissions, 1); assert.equal(runner.snapshot()[0].status, 'success')
})

test('提交结果未知时不自动重发，活动任务发现可恢复同一身份', async () => {
  let submissions = 0
  const runner = new SiteTaskRunner({ start: async () => { submissions++; throw new Error('response lost') }, read: async (id) => task(id, 'success'), cancel: async () => ({ cancelled: true, status: 'cancelled' }) })
  await runner.run([target(1)]); await runner.retryFailed(); await runner.run([target(1)])
  assert.equal(submissions, 1); assert.equal(runner.snapshot()[0].status, 'unknown')
  await runner.observe(target(1), task('st_recovered', 'running'))
  assert.equal(runner.snapshot().length, 1); assert.equal(runner.snapshot()[0].taskId, 'st_recovered'); assert.equal(runner.snapshot()[0].status, 'success')
})

test('活动任务恢复与未知状态取消使用服务器身份，取消已结束任务保留实际结果', async () => {
  const read = deferred<TaskSnapshot>(); const ids: string[] = []
  const runner = new SiteTaskRunner({ start: async () => { throw new Error('must not submit') }, read: () => read.promise, cancel: async (id) => { ids.push(id); return { cancelled: false, status: 'success' } } })
  const work = runner.observe(target(1), task('st_scheduled', 'running'))
  await tick(); await runner.cancel(target(1).key)
  read.resolve(task('st_scheduled', 'success')); await work
  assert.deepEqual(ids, ['st_scheduled']); assert.equal(runner.snapshot()[0].status, 'success')
})

test('取消 API 已确认终态，迟到的轮询不能恢复为运行中', async () => {
  const read = deferred<TaskSnapshot>()
  let reads = 0
  const runner = new SiteTaskRunner({ start: async () => ({ task_id: 'st_late' }), read: async (id) => ++reads === 1 ? read.promise : task(id, 'success'), cancel: async () => ({ cancelled: false, status: 'success' }) })
  const work = runner.run([target(1)])
  await tick(); await runner.cancel(target(1).key)
  read.resolve(task('st_late', 'running'))
  await tick()
  assert.equal(runner.snapshot()[0].status, 'success')
  if (runner.snapshot()[0].status === 'success') await work
})

test('明确拒绝的排队请求可以重试，未知提交只能由用户核实后移除', async () => {
  let calls = 0; let lost = false
  const runner = new SiteTaskRunner({ start: async () => { calls++; if (lost) throw new Error('submission response lost'); if (calls === 1) throw new TaskSubmissionRejected('site_task_queue_full'); return { task_id: 'st_admitted' } }, read: async (id) => task(id, 'success'), cancel: async () => ({ cancelled: true, status: 'cancelled' }) })
  await runner.run([target(1)]); assert.equal(runner.snapshot()[0].status, 'failed')
  await runner.retryFailed(); assert.equal(runner.snapshot()[0].status, 'success'); assert.equal(calls, 2)
  lost = true; await runner.run([target(2)]); await runner.run([target(2)]); assert.equal(calls, 3)
  runner.forgetUnknown(target(2).key); lost = false; await runner.run([target(2)])
  assert.equal(calls, 4); assert.equal(runner.snapshot()[1].status, 'success')
})

test('取消全部不能把没有服务器 ID 的未知提交伪造成已取消', async () => {
  const submission = deferred<{ task_id: string }>()
  const runner = new SiteTaskRunner({ start: async (item) => { if (item.accountId === 1) throw new Error('response lost'); return submission.promise }, read: async (id) => task(id, 'running'), cancel: async () => ({ cancelled: true, status: 'cancelled' }) })
  await runner.run([target(1)])
  const active = runner.run([target(2)])
  await runner.cancelAll(); submission.resolve({ task_id: 'st_second' }); await active
  assert.equal(runner.snapshot()[0].status, 'unknown')
  assert.equal(runner.snapshot()[0].cancelRequested, false)
  assert.equal(runner.snapshot()[1].status, 'cancelled')
})

test('服务器报告未知结果时保留原因与身份，禁止失败重试', async () => {
  let starts = 0
  const runner = new SiteTaskRunner({ start: async () => { starts++; return { task_id: 'st_orphan' } }, read: async () => ({ id: 'st_orphan', kind: 'refresh', status: 'unknown', error: 'observation_interrupted', created_at: 1 } as unknown as TaskSnapshot), cancel: async () => ({ cancelled: false, status: 'running' }) })
  await runner.run([target(1)])
  assert.equal(runner.snapshot()[0].status, 'unknown')
  assert.equal(runner.snapshot()[0].taskId, 'st_orphan')
  assert.equal(runner.snapshot()[0].message, 'observation_interrupted')
  await runner.retryFailed()
  assert.equal(starts, 1)
})
