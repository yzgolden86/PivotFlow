import assert from 'node:assert/strict'
import test from 'node:test'
import type { TaskItem } from '../siteTaskRunner.ts'
import * as observation from './siteTaskObservation.ts'

const recovery = Reflect.get(observation, 'checkinBlockerRecovery') as (items: TaskItem[], siteId: number, accountId: number) => { text: string; href: string } | null
const item = (kind: TaskItem['target']['kind'], status: TaskItem['status'], accountId = 11): TaskItem => ({
  target: { key: `${kind}:1:${accountId}`, kind, siteId: 1, accountId, title: 'synthetic', detail: '' },
  taskId: 'synthetic-task', status, cancelRequested: false, cancelling: false,
})

test('unknown balance work blocking checkin exposes an account recovery link', () => {
  assert.equal(typeof recovery, 'function')
  const result = recovery([item('refresh', 'unknown')], 1, 11)
  assert.deepEqual(result, { text: '刷新余额结果待确认 · 去处理', href: '#/accounts?focus_account_id=11' })
})

test('unknown model work blocking checkin exposes routing recovery rather than unrelated task history', () => {
  assert.equal(typeof recovery, 'function')
  const result = recovery([item('model_refresh', 'unknown')], 1, 11)
  assert.deepEqual(result, { text: '同步模型与路由结果待确认 · 去处理', href: '#/channels' })
  assert.equal(recovery([item('model_refresh', 'unknown', 12)], 1, 11), null)
  assert.equal(recovery([item('model_refresh', 'failed')], 1, 11), null)
  assert.equal(recovery([item('checkin', 'unknown')], 1, 11), null)
})

test('active work explains a disabled account action and links to its originating page', () => {
  assert.equal(typeof recovery, 'function')
  assert.deepEqual(recovery([item('refresh', 'running')], 1, 11), { text: '刷新余额进行中 · 查看', href: '#/accounts?focus_account_id=11' })
})
