import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { SiteAnnouncement } from '../types.ts'
import { announcementDetailTime, announcementFetchedTime } from './announcementTime.ts'

type AnnouncementTimes = Pick<SiteAnnouncement, 'upstream_created_at' | 'last_seen_at'>
const item = (times: AnnouncementTimes): AnnouncementTimes => times

test('列表用获取时间，且不受上游修改时间影响', () => {
  // last_seen_at 是本地同步时刻；upstream_updated_at 不该出现在列表里，
  // 否则上游改一次公告，列表这条就跳到修改时间上去了。
  assert.equal(announcementFetchedTime({ last_seen_at: 1700000000000 }), 1700000000000)
})

test('详情拿到发布时间时用发布时间', () => {
  const result = announcementDetailTime(item({ upstream_created_at: 1699999999000, last_seen_at: 1700000000000 }))
  assert.deepEqual(result, { at: 1699999999000, source: 'published' })
})

test('上游不给时间（New API 系恒为 0）时详情退回获取时间并标注来源', () => {
  const result = announcementDetailTime(item({ upstream_created_at: 0, last_seen_at: 1700000000000 }))
  assert.deepEqual(result, { at: 1700000000000, source: 'fetched' })
})

test('上游时间字段缺失（undefined）同样退回获取时间', () => {
  // json 里 upstream_created_at 带 omitempty，0 会被整个字段省掉。
  const result = announcementDetailTime(item({ last_seen_at: 1700000000000 }))
  assert.deepEqual(result, { at: 1700000000000, source: 'fetched' })
})

test('上游给负数（脏数据）时不当作发布时间', () => {
  const result = announcementDetailTime(item({ upstream_created_at: -1, last_seen_at: 1700000000000 }))
  assert.deepEqual(result, { at: 1700000000000, source: 'fetched' })
})
