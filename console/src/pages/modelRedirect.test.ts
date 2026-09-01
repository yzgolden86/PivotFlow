import { test } from 'node:test'
import assert from 'node:assert/strict'
import { classifyModelRedirect, modelRedirectTitle } from './modelRedirect.ts'

test('相同或缺失时不算重定向', () => {
  for (const [m, a] of [
    ['gpt-4o', 'gpt-4o'],
    ['gpt-4o', ''],
    ['gpt-4o', undefined],
    ['', 'gpt-4o'],
    [undefined, undefined],
  ] as Array<[string | undefined, string | undefined]>) {
    assert.equal(classifyModelRedirect(m, a), 'none', `${m} -> ${a}`)
    assert.equal(modelRedirectTitle(m, a), undefined)
  }
})

test('纯大小写差异算写法差异', () => {
  for (const [m, a] of [
    ['GPT-4O', 'gpt-4o'],
    ['DeepSeek-V4-Flash', 'deepseek-v4-flash'],
    ['GLM-5.2', 'glm-5.2'],
  ]) {
    assert.equal(classifyModelRedirect(m, a), 'cosmetic', `${m} -> ${a}`)
  }
})

test('站点/厂商前缀差异算写法差异', () => {
  for (const [m, a] of [
    ['773/deepseek-v4-flash', 'deepseek-v4-flash'],
    ['deepseek-v4-flash', '773/deepseek-v4-flash'],
    ['openai/gpt-4o', 'gpt-4o'],
    ['z.ai/glm-5.2', 'glm-5.2'],
    ['773/DeepSeek-V4-Flash', 'deepseek-v4-flash'],
  ]) {
    assert.equal(classifyModelRedirect(m, a), 'cosmetic', `${m} -> ${a}`)
  }
})

// 两侧都带前缀但厂商不同：是不同的路由端点，定价与可用性都可能不同。
test('不同厂商前缀不算写法差异', () => {
  for (const [m, a] of [
    ['773/deepseek-v4-flash', 'openai/deepseek-v4-flash'],
    ['openai/gpt-4o', 'azure/gpt-4o'],
    ['bedrock/claude-opus-4', 'vertex/claude-opus-4'],
  ]) {
    assert.equal(classifyModelRedirect(m, a), 'substantive', `${m} -> ${a}`)
  }
})

// 多段前缀剥一段后仍不相等，落在保守的一侧。
test('多段前缀判为实质重定向', () => {
  assert.equal(classifyModelRedirect('773/openai/gpt-4o', 'gpt-4o'), 'substantive')
})

// 回归核心：这 13 对曾被 isPrefixOrSuffixVariant 静默抑制。
// 每一对都是不同定价档的不同模型，抑制会让用户对不上账。
test('跨档位/跨快照差异必须判为实质重定向', () => {
  for (const [m, a] of [
    ['gpt-4o', 'gpt-4o-mini'],
    ['gpt-4o-mini', 'gpt-4o'],
    ['gpt-5', 'gpt-5-mini'],
    ['gpt-5', 'gpt-5-nano'],
    ['o1', 'o1-mini'],
    ['o3', 'o3-mini'],
    ['gemini-2.5-flash', 'gemini-2.5-flash-lite'],
    ['claude-sonnet-4', 'claude-sonnet-4-5'],
    ['claude-3-opus', 'claude-3'],
    ['gpt-4o', 'gpt-4o-2024-05-13'],
    ['qwen-plus', 'qwen-plus-latest'],
    ['gpt-4', 'gpt-4o'],
    ['claude-opus-4', 'claude-opus-4-1'],
  ]) {
    assert.equal(classifyModelRedirect(m, a), 'substantive', `${m} -> ${a} 必须告警`)
  }
})

test('明显跨模型仍判为实质重定向', () => {
  for (const [m, a] of [
    ['gpt-4o', 'claude-sonnet-4'],
    ['claude-3-5-sonnet', 'claude-3-5-haiku'],
    ['glm-4.6', 'glm-4.5'],
    ['gemini-2.5-pro', 'gemini-1.5-pro'],
    ['gpt-4o', 'chatgpt-4o-latest'],
  ]) {
    assert.equal(classifyModelRedirect(m, a), 'substantive', `${m} -> ${a}`)
  }
})

// 效果后缀在定价表里是独立条目或根本不存在，一律不得抑制。
test('效果/档位后缀不得判为写法差异', () => {
  for (const [m, a] of [
    ['gemini-3.6-flash-high', 'gemini-3.6-flash'],
    ['kimi-k2', 'kimi-k2-thinking'],
    ['qwen3-max', 'qwen3-max-thinking'],
    ['gpt-4-turbo', 'gpt-4o-turbo'],
  ]) {
    assert.equal(classifyModelRedirect(m, a), 'substantive', `${m} -> ${a}`)
  }
})

// 最重要的不变量：只要两者不同，真实模型永远出现在提示里。
test('真实模型在任何分级下都不被隐藏', () => {
  const cases: Array<[string, string]> = [
    ['773/deepseek-v4-flash', 'deepseek-v4-flash'],
    ['GPT-4O', 'gpt-4o'],
    ['gpt-4o', 'gpt-4o-mini'],
    ['gpt-4o', 'claude-sonnet-4'],
    ['gemini-3.6-flash-high', 'gemini-3.6-flash'],
  ]
  for (const [m, a] of cases) {
    const title = modelRedirectTitle(m, a)
    assert.ok(title, `${m} -> ${a} 必须有提示`)
    assert.ok(title.includes(a), `提示里必须含真实模型 ${a}，实际为 ${title}`)
    assert.ok(title.includes(m), `提示里必须含请求模型 ${m}，实际为 ${title}`)
  }
})

test('措辞按分级区分', () => {
  assert.match(modelRedirectTitle('773/deepseek-v4-flash', 'deepseek-v4-flash')!, /写法差异/)
  assert.match(modelRedirectTitle('gpt-4o', 'gpt-4o-mini')!, /实际重定向/)
  assert.doesNotMatch(modelRedirectTitle('gpt-4o', 'gpt-4o-mini')!, /写法差异/)
})

test('两侧空白被裁剪后再判定', () => {
  assert.equal(classifyModelRedirect('  gpt-4o  ', 'gpt-4o'), 'none')
  assert.equal(classifyModelRedirect(' 773/glm-5.2 ', ' glm-5.2 '), 'cosmetic')
})
