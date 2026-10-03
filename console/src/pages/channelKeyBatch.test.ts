import test from 'node:test'
import assert from 'node:assert/strict'
import { parseChannelKeyBatch } from './channelKeyBatch.ts'

// 批量粘贴 Key 的解析回归。这些语义都很容易在重构时被改坏，而改坏之后
// 界面只会「少出几把 Key」，不会报错 —— 所以钉在这里。

test('每行一个 Key，空行与注释行被忽略', () => {
  const result = parseChannelKeyBatch('\nsk-aaa\n\n# 这是注释\nsk-bbb\n  \n')
  assert.deepEqual(result, { entries: [{ api_key: 'sk-aaa', note: '' }, { api_key: 'sk-bbb', note: '' }], duplicate: 0, invalid: 0 })
})

test('用 | 在 Key 后面写备注', () => {
  const result = parseChannelKeyBatch('sk-aaa | 主账号\nsk-bbb|备用')
  assert.deepEqual(result.entries, [{ api_key: 'sk-aaa', note: '主账号' }, { api_key: 'sk-bbb', note: '备用' }])
})

test('一行多把 Key：逗号、分号、中文标点都算分隔符，并共用同行备注', () => {
  const result = parseChannelKeyBatch('sk-aaa, sk-bbb；sk-ccc | 备用')
  assert.deepEqual(result.entries, [
    { api_key: 'sk-aaa', note: '备用' },
    { api_key: 'sk-bbb', note: '备用' },
    { api_key: 'sk-ccc', note: '备用' },
  ])
})

test('| 固定是备注分隔符，不会把一行拆成两把 Key', () => {
  const result = parseChannelKeyBatch('sk-aaa|sk-bbb')
  assert.deepEqual(result.entries, [{ api_key: 'sk-aaa', note: 'sk-bbb' }])
})

test('与已有行重复的 Key 被跳过并计数，不报错', () => {
  const result = parseChannelKeyBatch('sk-aaa\nsk-new', ['sk-aaa', 'sk-old'])
  assert.deepEqual(result.entries, [{ api_key: 'sk-new', note: '' }])
  assert.equal(result.duplicate, 1)
  assert.equal(result.invalid, 0)
})

test('同一批粘贴里的重复只保留第一把', () => {
  const result = parseChannelKeyBatch('sk-aaa\nsk-aaa\nsk-aaa')
  assert.deepEqual(result.entries, [{ api_key: 'sk-aaa', note: '' }])
  assert.equal(result.duplicate, 2)
})

test('已有行的空白不影响去重判断', () => {
  const result = parseChannelKeyBatch('sk-aaa', ['  sk-aaa  '])
  assert.deepEqual(result.entries, [])
  assert.equal(result.duplicate, 1)
})

test('含空白的片段视为无效并计数', () => {
  const result = parseChannelKeyBatch('sk-aaa sk-bbb\nsk-ccc')
  assert.deepEqual(result.entries, [{ api_key: 'sk-ccc', note: '' }])
  assert.equal(result.invalid, 1)
})

test('首尾成对的引号被剥掉（从 JSON / 表格复制常带）', () => {
  const result = parseChannelKeyBatch('"sk-aaa"\n\'sk-bbb\'')
  assert.deepEqual(result.entries, [{ api_key: 'sk-aaa', note: '' }, { api_key: 'sk-bbb', note: '' }])
})

test('只有分隔符或只有备注时不会产出空条目', () => {
  const result = parseChannelKeyBatch(',,,\n| 只有备注\n;')
  assert.deepEqual(result, { entries: [], duplicate: 0, invalid: 0 })
})

test('CRLF 换行同样能解析', () => {
  const result = parseChannelKeyBatch('sk-aaa\r\nsk-bbb | 备用\r\n')
  assert.deepEqual(result.entries, [{ api_key: 'sk-aaa', note: '' }, { api_key: 'sk-bbb', note: '备用' }])
})
