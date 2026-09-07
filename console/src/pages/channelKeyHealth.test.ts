import test from 'node:test'
import assert from 'node:assert/strict'
import { keyHealthLabel, keyNeedsAttention, matchesKeyFilter, mergeKeyHealth } from './channelKeyHealth.ts'
import type { ChannelKeyHealthItem, ChannelKeyHealthSnapshot } from '../types.ts'

const key = (id: number, status = '', at = 0): ChannelKeyHealthItem => ({ id, key_index: 0, masked_key: 'sk-.xyz', note: '', disabled: false, cooldown_until: 0, health: { status, checked_at: at, status_code: 0, reason: '' } })
const snapshot = (keys: ChannelKeyHealthItem[]): ChannelKeyHealthSnapshot => ({ channel_id: 1, channel_name: 'test', models: ['test-model'], keys })

test('health labels and filters do not equate cooldown or disabled with invalid credentials', () => {
  const unknown = { ...key(1), cooldown_until: 9999999999 }
  assert.equal(keyHealthLabel(unknown).label, '未检测')
  assert.equal(keyNeedsAttention(unknown), false)
  assert.equal(matchesKeyFilter(unknown, 'unknown'), true)
  assert.equal(keyHealthLabel(key(2, 'invalid', 100)).label, '认证失败')
  assert.equal(matchesKeyFilter(key(3, 'rate_limited', 100), 'attention'), true)
  assert.equal(matchesKeyFilter(key(4, 'healthy', 100), 'attention'), false)
  assert.equal(matchesKeyFilter({ ...key(5), disabled: true }, 'disabled'), true)
})

test('stale refresh preserves newer test results without crossing compacted key indices', () => {
  const previous = snapshot([key(1, 'invalid', 300), key(2, 'healthy', 400)])
  const next = snapshot([key(2, 'invalid', 200), key(3)])
  const merged = mergeKeyHealth(next, previous)
  assert.equal(merged.keys[0].health.status, 'healthy')
  assert.equal(merged.keys[1].health.checked_at, 0)
  assert.equal(next.keys[0].health.status, 'invalid')
  const fresh = mergeKeyHealth(snapshot([key(2, 'quota_exhausted', 500)]), previous)
  assert.equal(fresh.keys[0].health.status, 'quota_exhausted')
})
