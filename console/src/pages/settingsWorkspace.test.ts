import assert from 'node:assert/strict'
import test from 'node:test'
import { editByteSetting, groupSettingsForWorkspace, settingGroup } from './settingsWorkspace.ts'

test('fixed daily balance controls appear with automatic tasks', () => {
  const keys = ['site_daily_balance_enabled', 'site_daily_balance_time', 'site_daily_checkin_time', 'site_daily_announcement_time']
  for (const key of keys) assert.equal(settingGroup(key), 'automation')
  const sections = groupSettingsForWorkspace(keys.map((key) => ({ key })))
  assert.equal(sections.length, 1)
  assert.equal(sections[0].title, '自动任务')
  assert.equal(sections[0].settings.length, 4)
})

test('timeout navigation separates defaults, protocols and connection policy without losing unfamiliar settings', () => {
  const settings = [
    { key: 'codex_first_byte_timeout' }, { key: 'stream_timeout' },
    { key: 'openai_non_stream_timeout' }, { key: 'upstream_first_byte_timeout' },
    { key: 'codex_non_stream_timeout' }, { key: 'upstream_connection_reuse_limit_seconds' },
    { key: 'future_protocol_timeout' },
  ]
  const sections = groupSettingsForWorkspace(settings)
  const sectionFor = (key: string) => sections.find((section) => section.settings.some((setting) => setting.key === key))
  assert.equal(sectionFor('codex_first_byte_timeout'), sectionFor('codex_non_stream_timeout'))
  assert.notEqual(sectionFor('codex_first_byte_timeout'), sectionFor('openai_non_stream_timeout'))
  assert.equal(sectionFor('stream_timeout'), sectionFor('upstream_first_byte_timeout'))
  assert.notEqual(sectionFor('stream_timeout'), sectionFor('upstream_connection_reuse_limit_seconds'))
  assert.ok(sectionFor('future_protocol_timeout'))
  assert.deepEqual(sections.flatMap((section) => section.settings).sort((a, b) => a.key.localeCompare(b.key)), [...settings].sort((a, b) => a.key.localeCompare(b.key)))
  assert.equal(new Set(sections.map((section) => section.id)).size, sections.length)
})

test('search and modified views retain every category including cooldown and websocket timeout settings', () => {
  assert.equal(settingGroup('cooldown_timeout_seconds'), 'cooldown')
  assert.equal(settingGroup('responses_ws_idle_timeout_seconds'), 'websocket')
  const settings = [{ key: 'cooldown_timeout_seconds' }, { key: 'responses_ws_max_sessions' }, { key: 'request_cache_ttl_seconds' }, { key: 'route_strategy' }]
  const sections = groupSettingsForWorkspace(settings)
  assert.equal(sections.length, 4)
  assert.deepEqual(sections.flatMap((section) => section.settings), settings)
  assert.deepEqual(groupSettingsForWorkspace([]), [])
})

test('unfamiliar protocol timeout settings remain visible without assuming default inheritance', () => {
  const sections = groupSettingsForWorkspace([{ key: 'codex_first_byte_timeout' }, { key: 'codex_connect_timeout' }])
  const unfamiliar = sections.find((section) => section.settings.some((setting) => setting.key === 'codex_connect_timeout'))
  assert.ok(unfamiliar)
  assert.equal(unfamiliar.caption, undefined)
  assert.equal(unfamiliar.settings.length, 1)
})

test('invalid byte input retains the raw draft and exposes unsaved state without sending invalid bytes', () => {
  const edit = editByteSetting('request_cache_max_bytes', 'oops', '33554432')
  assert.equal(edit.draft, 'oops')
  assert.equal(edit.dirty, true)
  assert.equal(edit.value, null)
  assert.ok(edit.error)
})

test('repairing a byte draft clears errors and compares exact bytes with the saved value', () => {
  assert.deepEqual(editByteSetting('request_cache_max_bytes', '32.0', '33554432'), { draft: '32.0', value: '33554432', error: '', dirty: false })
  assert.deepEqual(editByteSetting('request_cache_max_entry_bytes', '512', '1048576'), { draft: '512', value: '524288', error: '', dirty: true })
  const fractional = editByteSetting('request_cache_max_bytes', '1.430511474609375', '1500000')
  assert.equal(fractional.value, '1500000')
  assert.equal(fractional.dirty, false)
  const outOfRange = editByteSetting('request_cache_max_entry_bytes', '0', '1048576')
  assert.equal(outOfRange.value, null)
  assert.equal(outOfRange.dirty, true)
})
