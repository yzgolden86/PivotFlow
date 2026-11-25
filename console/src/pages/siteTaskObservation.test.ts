import assert from 'node:assert/strict'
import { test } from 'node:test'
import { SiteTaskRunner, type TaskItem, type TaskSnapshot } from '../siteTaskRunner.ts'
import { createSiteTaskDiscovery, siteTaskCompletionKey, siteTaskFeedback, siteTaskBlocker } from './siteTaskObservation.ts'

const tick = () => new Promise<void>((resolve) => setImmediate(resolve))
const snapshot = (status: TaskSnapshot['status'] = 'running'): TaskSnapshot => ({ id: 'st_recovered', kind: 'checkin', site_id: 2, site_account_id: 7, status, created_at: 1 })
const target = { key: 'checkin:2:7', kind: 'checkin' as const, siteId: 2, accountId: 7, title: '测试账号', detail: '测试站点' }
const item = (status: TaskItem['status']): TaskItem => ({ target, taskId: 'st_recovered', status, cancelRequested: false, cancelling: false })
class Visibility extends EventTarget { hidden = false }

test('one discovery lifecycle recovers server identity even with two mounts and never resubmits it', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval'] })
  let starts = 0; let discoveries = 0
  const runner = new SiteTaskRunner({ start: async () => { starts++; return { task_id: 'duplicate' } }, read: async () => snapshot('success'), cancel: async () => ({ cancelled: true, status: 'cancelled' }) })
  const discovery = createSiteTaskDiscovery(async () => { discoveries++; return [snapshot()] }, runner, new Visibility())
  const first = discovery.mount(); const second = discovery.mount()
  t.mock.timers.tick(0); await tick()
  assert.equal(discoveries, 1)
  assert.equal(starts, 0)
  assert.equal(runner.snapshot()[0]?.taskId, 'st_recovered')
  assert.equal(runner.snapshot()[0]?.status, 'success')
  first(); t.mock.timers.tick(10_000); await tick()
  assert.equal(discoveries, 2)
  second(); t.mock.timers.tick(10_000); await tick()
  assert.equal(discoveries, 2)
})

test('StrictMode cleanup and remount discover once; hidden tabs wait for visibility', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval'] })
  const visibility = new Visibility(); visibility.hidden = true
  let reads = 0
  const runner = new SiteTaskRunner({ start: async () => { throw new Error('must not submit') }, read: async () => snapshot('success'), cancel: async () => ({ cancelled: true, status: 'cancelled' }) })
  const discovery = createSiteTaskDiscovery(async () => { reads++; return [snapshot()] }, runner, visibility)
  discovery.mount()(); const stop = discovery.mount()
  t.mock.timers.tick(10_000); await tick(); assert.equal(reads, 0)
  visibility.hidden = false; visibility.dispatchEvent(new Event('visibilitychange'))
  await tick(); assert.equal(reads, 1); assert.equal(runner.snapshot().length, 1)
  stop()
})

test('discovery does not overlap reads and ignores a late result after unmount', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval'] })
  let resolve!: (tasks: TaskSnapshot[]) => void; let signal!: AbortSignal; let reads = 0
  const runner = new SiteTaskRunner({ start: async () => { throw new Error('must not submit') }, read: async () => snapshot('success'), cancel: async () => ({ cancelled: true, status: 'cancelled' }) })
  const discovery = createSiteTaskDiscovery((incoming) => { signal = incoming; reads++; return new Promise((done) => { resolve = done }) }, runner, new Visibility())
  const stop = discovery.mount(); t.mock.timers.tick(0)
  void discovery.refresh(); void discovery.refresh(); assert.equal(reads, 1)
  stop(); assert.equal(signal.aborted, true)
  resolve([snapshot()]); await tick()
  assert.equal(runner.snapshot().length, 0)
  assert.equal(discovery.snapshot().discovering, false)
})

test('discovery errors remain visible until a successful read and unsupported kinds are ignored', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval'] })
  let offline = true
  const runner = new SiteTaskRunner({ start: async () => { throw new Error('must not submit') }, read: async () => snapshot('success'), cancel: async () => ({ cancelled: true, status: 'cancelled' }) })
  const discovery = createSiteTaskDiscovery(async () => { if (offline) throw new Error('offline'); return [{ ...snapshot(), kind: 'preview' }, snapshot()] }, runner, new Visibility())
  const stop = discovery.mount(); t.mock.timers.tick(0); await tick()
  assert.equal(discovery.snapshot().error, 'offline')
  offline = false; await discovery.refresh(); await tick()
  assert.equal(discovery.snapshot().error, '')
  assert.equal(runner.snapshot().length, 1)
  stop()
})

test('page feedback separates pending, failed and unknown outcomes without exposing unrelated task history', () => {
  const items = [item('queued'), { ...item('unknown'), target: { ...target, key: 'refresh:2:7', kind: 'refresh' as const } }, { ...item('failed'), target: { ...target, accountId: 9, key: 'checkin:2:9' } }, { ...item('failed'), target: { ...target, siteId: 3, key: 'checkin:3:7' } }, item('success')]
  const feedback = siteTaskFeedback(items, { kinds: ['checkin'], siteId: 2 })
  assert.equal(feedback.active.length, 1)
  assert.equal(feedback.failed.length, 1)
  assert.equal(feedback.unknown.length, 0)
  assert.equal(siteTaskFeedback(items, { kinds: ['refresh'] }).unknown.length, 1)
})

test('checkin feedback hides balance and routing tasks while retaining their duplicate-work protection', () => {
  const unrelated = [
    { ...item('running'), target: { ...target, kind: 'refresh' as const } },
    { ...item('unknown'), target: { ...target, kind: 'model_refresh' as const } },
    { ...item('failed'), target: { ...target, kind: 'refresh' as const } },
  ]
  assert.deepEqual(siteTaskFeedback(unrelated, { kinds: ['checkin'] }), { active: [], failed: [], unknown: [] })
  assert.equal(siteTaskBlocker(unrelated, 2, 7)?.status, 'running')
  assert.equal(siteTaskBlocker(unrelated.slice(1), 2, 7)?.status, 'unknown')
  assert.deepEqual(siteTaskFeedback([item('success')], { kinds: ['checkin'] }), { active: [], failed: [], unknown: [] })
})

test('a synchronous discovery failure releases the read slot for recovery', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval'] })
  let offline = true
  const runner = new SiteTaskRunner({ start: async () => { throw new Error('must not submit') }, read: async () => snapshot('success'), cancel: async () => ({ cancelled: true, status: 'cancelled' }) })
  const discovery = createSiteTaskDiscovery(() => { if (offline) throw new Error('synchronous transport error'); return Promise.resolve([snapshot()]) }, runner, new Visibility())
  const stop = discovery.mount(); t.mock.timers.tick(0); await tick()
  offline = false; await discovery.refresh(); await tick()
  assert.equal(runner.snapshot()[0]?.status, 'success')
  assert.equal(discovery.snapshot().error, '')
  stop()
})

test('timed out discovery reports an error and can read again without retaining a stale lock', async (t) => {
  t.mock.timers.enable({ apis: ['setTimeout', 'setInterval'] })
  let waiting = true
  const runner = new SiteTaskRunner({ start: async () => { throw new Error('must not submit') }, read: async () => snapshot('success'), cancel: async () => ({ cancelled: true, status: 'cancelled' }) })
  const discovery = createSiteTaskDiscovery((signal) => waiting ? new Promise((_, reject) => signal.addEventListener('abort', () => reject(signal.reason), { once: true })) : Promise.resolve([snapshot()]), runner, new Visibility())
  const stop = discovery.mount(); t.mock.timers.tick(0); t.mock.timers.tick(8_000); await tick()
  assert.notEqual(discovery.snapshot().error, '')
  assert.equal(discovery.snapshot().discovering, false)
  waiting = false; await discovery.refresh(); await tick()
  assert.equal(runner.snapshot()[0]?.status, 'success')
  stop()
})

test('unconfirmed identities block repeated account work while confirmed failures can be retried', () => {
  assert.equal(siteTaskBlocker([item('unknown')], 2, 7)?.status, 'unknown')
  assert.equal(siteTaskBlocker([item('running')], 2, 7)?.status, 'running')
  assert.equal(siteTaskBlocker([item('failed')], 2, 7), undefined)
  assert.equal(siteTaskBlocker([item('unknown')], 3, 7), undefined)
  assert.equal(siteTaskBlocker([item('unknown')], 2, 8), undefined)
  assert.equal(siteTaskBlocker([{ ...item('running'), target: { ...target, accountId: undefined } }], 2, 8)?.status, 'running')
  assert.equal(siteTaskBlocker([{ ...item('running'), target: { ...target, kind: 'announcement_refresh', accountId: undefined } }], 2, 8), undefined)
})

test('unknown completion refreshes originating pages without becoming a confirmed success', () => {
  assert.equal(siteTaskCompletionKey([item('running')]), '')
  assert.notEqual(siteTaskCompletionKey([item('unknown')]), '')
  assert.notEqual(siteTaskCompletionKey([item('unknown')]), siteTaskCompletionKey([item('success')]))
  assert.equal(siteTaskCompletionKey([item('unknown')], 'refresh'), '')
})
