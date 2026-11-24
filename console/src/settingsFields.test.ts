import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  byteSettingUnit,
  formatByteSettingValue,
  parseByteSettingValue,
  validateWallpaperInput,
} from './settingsFields.ts'

test('缓存字节默认值以明确单位展示', () => {
  assert.equal(byteSettingUnit('request_cache_max_bytes'), 'MiB')
  assert.equal(byteSettingUnit('request_cache_max_entry_bytes'), 'KiB')
  assert.equal(formatByteSettingValue('request_cache_max_bytes', '33554432'), '32')
  assert.equal(formatByteSettingValue('request_cache_max_entry_bytes', '1048576'), '1024')
})

test('缓存容量输入精确换算为现有字节 API 设置', () => {
  const total = parseByteSettingValue('request_cache_max_bytes', '1.5')
  const entry = parseByteSettingValue('request_cache_max_entry_bytes', '512')
  assert.deepEqual(total, { ok: true, bytes: 1_572_864 })
  assert.deepEqual(entry, { ok: true, bytes: 524_288 })
})

test('非整 MiB 的既有字节值往返不静默取整', () => {
  const formatted = formatByteSettingValue('request_cache_max_bytes', '1500000')
  assert.equal(formatted, '1.430511474609375')
  assert.deepEqual(parseByteSettingValue('request_cache_max_bytes', formatted), { ok: true, bytes: 1_500_000 })
})

test('缓存容量输入执行后端边界校验', () => {
  const below = parseByteSettingValue('request_cache_max_bytes', '0.5')
  const above = parseByteSettingValue('request_cache_max_bytes', '129')
  const smallEntry = parseByteSettingValue('request_cache_max_entry_bytes', '3.5')
  assert.equal(below.ok, false)
  assert.equal(above.ok, false)
  assert.equal(smallEntry.ok, false)
  assert.match(below.ok ? '' : below.error, /1 MiB/)
  assert.match(above.ok ? '' : above.error, /128 MiB/)
})

test('缓存容量拒绝会产生非整字节的静默舍入', () => {
  const result = parseByteSettingValue('request_cache_max_bytes', '1.0000001')
  assert.equal(result.ok, false)
  assert.match(result.ok ? '' : result.error, /整字节/)
})

test('壁纸输入只接受与当前主题归一化一致的 HTTP(S) 地址', () => {
  assert.deepEqual(validateWallpaperInput('  https://example.com/a.jpg  '), {
    ok: true,
    value: 'https://example.com/a.jpg',
    action: 'apply',
  })
  assert.deepEqual(validateWallpaperInput('http://example.com/a.jpg'), {
    ok: true,
    value: 'http://example.com/a.jpg',
    action: 'apply',
  })
})

test('壁纸清空与非法协议和地址分开处理', () => {
  assert.deepEqual(validateWallpaperInput('   '), { ok: true, value: '', action: 'clear' })
  for (const value of ['javascript:alert(1)', 'data:image/png;base64,AAAA', 'file:///tmp/a.png', '//example.com/a.png', '/local/a.png', 'https://']) {
    const result = validateWallpaperInput(value)
    assert.equal(result.ok, false, value)
    assert.match(result.ok ? '' : result.error, /http:\/\/ 或 https:\/\/|有效主机名/, value)
  }
})
