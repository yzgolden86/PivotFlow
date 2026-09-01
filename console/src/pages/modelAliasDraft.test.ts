import { test } from 'node:test'
import assert from 'node:assert/strict'
import type { ModelAliasSuggestion } from '../types.ts'
import {
  adoptSuggestions,
  aliasMembers,
  canonicalKey,
  parseAliasDraft,
  serializeAliasDraft,
  withMembers,
  type AliasDraft,
} from './modelAliasDraft.ts'

function draft(canonical: string, aliases: string[] = [], enabled = true): AliasDraft {
  return { canonical, aliases: aliases.join('\n'), enabled }
}

function suggest(canonical: string, members: string[], extend?: string): ModelAliasSuggestion {
  return { canonical, members, reason: '', extends_canonical: extend }
}

test('canonicalKey 大小写与空白不敏感', () => {
  assert.equal(canonicalKey('  GPT-5  '), 'gpt-5')
  assert.equal(canonicalKey('gpt-5'), canonicalKey('GPT-5'))
})

// 回归核心一：多条建议指向同一已有组时必须全部合并，不能只取第一条。
test('同一目标的多条建议全部合并', () => {
  const groups = [draft('gpt-5', ['gpt-5-turbo'])]
  const next = adoptSuggestions(groups, [
    suggest('gpt-5', ['GPT-5'], 'gpt-5'),
    suggest('gpt-5', ['GPT-5-Turbo'], 'gpt-5'),
    suggest('gpt-5', ['GPT-5-Chat'], 'gpt-5'),
  ])
  assert.equal(next.length, 1, '不应新建组')
  assert.deepEqual(aliasMembers(next[0]), ['gpt-5-turbo', 'GPT-5', 'GPT-5-Turbo', 'GPT-5-Chat'])
})

// 回归核心二：草稿里改了统一名的大小写后，仍要认出同一个组，不能新建冲突组。
test('目标组按大小写不敏感匹配', () => {
  const groups = [draft('GLM-5.2', ['glm-5.2'])]
  const next = adoptSuggestions(groups, [suggest('glm-5.2', ['z.ai/glm-5.2'], 'glm-5.2')])
  assert.equal(next.length, 1, '大小写不同不应视为另一个组')
  assert.equal(next[0].canonical, 'GLM-5.2', '保留用户当前写法')
  assert.ok(aliasMembers(next[0]).includes('z.ai/glm-5.2'))
})

test('结果中不出现大小写冲突的统一名', () => {
  const groups = [draft('glm-5.2', ['GLM-5.2'])]
  const next = adoptSuggestions(groups, [
    suggest('GLM-5.2', ['z.ai/glm-5.2'], 'GLM-5.2'),
    suggest('Glm-5.2', ['openai/glm-5.2'], 'Glm-5.2'),
  ])
  const keys = next.map((group) => canonicalKey(group.canonical))
  assert.equal(new Set(keys).size, keys.length, `统一名冲突会让后端拒绝整批保存: ${keys}`)
})

test('新建组的统一名与已有组冲突时合并而非新建', () => {
  const groups = [draft('gpt-5', ['gpt-5-turbo'])]
  const next = adoptSuggestions(groups, [suggest('GPT-5', ['GPT-5', 'GPT-5-Preview'])])
  assert.equal(next.length, 1)
  assert.ok(aliasMembers(next[0]).includes('GPT-5-Preview'))
})

test('多条新建建议共用统一名时合并成一个组且不丢成员', () => {
  const next = adoptSuggestions([], [
    suggest('qwen-3', ['qwen-3', 'Qwen-3']),
    suggest('Qwen-3', ['QWEN-3']),
  ])
  assert.equal(next.length, 1)
  // 只断言长度会空过：旧实现靠「丢弃第二条」同样得到长度 1。
  assert.ok(aliasMembers(next[0]).includes('QWEN-3'), `第二条的成员被丢了: ${aliasMembers(next[0])}`)
})

test('不同目标彼此独立', () => {
  const groups = [draft('gpt-5', ['gpt-5-turbo']), draft('glm-5.2', ['GLM-5.2'])]
  const next = adoptSuggestions(groups, [
    suggest('gpt-5', ['GPT-5'], 'gpt-5'),
    suggest('glm-5.2', ['z.ai/glm-5.2'], 'glm-5.2'),
  ])
  assert.equal(next.length, 2)
  assert.ok(aliasMembers(next[0]).includes('GPT-5'))
  assert.ok(aliasMembers(next[1]).includes('z.ai/glm-5.2'))
})

test('新建组的成员里不含统一名自身', () => {
  const next = adoptSuggestions([], [suggest('qwen-3', ['qwen-3', 'Qwen-3', 'QWEN-3'])])
  assert.equal(next.length, 1)
  assert.deepEqual(aliasMembers(next[0]), ['Qwen-3', 'QWEN-3'])
})

test('合并时成员去重', () => {
  const groups = [draft('gpt-5', ['GPT-5'])]
  const next = adoptSuggestions(groups, [suggest('gpt-5', ['GPT-5', 'GPT-5-Turbo'], 'gpt-5')])
  assert.deepEqual(aliasMembers(next[0]), ['GPT-5', 'GPT-5-Turbo'])
})

// 后端不把停用组计入已认领，它的统一名会作为「新建」建议再次出现。
// 并入后必须启用，否则采用了却毫无效果，且横幅不再提示。
test('并入已停用的组时把它启用', () => {
  const groups = [draft('glm-5.2', [], false)]
  const next = adoptSuggestions(groups, [suggest('glm-5.2', ['glm-5.2', 'GLM-5.2', 'z.ai/glm-5.2'])])
  assert.equal(next.length, 1)
  assert.equal(next[0].enabled, true, '采用建议后目标组仍是停用的，路由不会变')
  assert.ok(aliasMembers(next[0]).includes('GLM-5.2'))
  assert.ok(aliasMembers(next[0]).includes('z.ai/glm-5.2'))
})

test('并入已启用的组不改变启用状态', () => {
  const groups = [draft('gpt-5', ['gpt-5-turbo'])]
  const next = adoptSuggestions(groups, [suggest('gpt-5', ['GPT-5'], 'gpt-5')])
  assert.equal(next[0].enabled, true)
})

test('不修改入参', () => {
  const groups = [draft('gpt-5', ['gpt-5-turbo'])]
  const snapshot = JSON.stringify(groups)
  adoptSuggestions(groups, [suggest('gpt-5', ['GPT-5'], 'gpt-5')])
  assert.equal(JSON.stringify(groups), snapshot)
})

test('空统一名的建议被忽略', () => {
  const next = adoptSuggestions([], [suggest('   ', ['whatever'])])
  assert.equal(next.length, 0)
})

test('草稿序列化往返保持内容', () => {
  const groups = [draft('gpt-5', ['GPT-5', 'gpt-5-turbo']), draft('glm-5.2', ['GLM-5.2'], false)]
  const round = parseAliasDraft(serializeAliasDraft(groups))
  assert.equal(round.length, 2)
  assert.equal(round[0].canonical, 'gpt-5')
  assert.deepEqual(aliasMembers(round[0]), ['GPT-5', 'gpt-5-turbo'])
  assert.equal(round[1].enabled, false)
})

test('非法 JSON 安全降级为空数组', () => {
  assert.deepEqual(parseAliasDraft('not json'), [])
  assert.deepEqual(parseAliasDraft(''), [])
  assert.deepEqual(parseAliasDraft('{"not":"array"}'), [])
})

test('withMembers 去重且保序', () => {
  const group = withMembers(draft('gpt-5'), ['b', 'a', 'b', 'c'])
  assert.deepEqual(aliasMembers(group), ['b', 'a', 'c'])
})
