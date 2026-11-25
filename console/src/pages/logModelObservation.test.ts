import assert from 'node:assert/strict'
import test from 'node:test'
import * as observation from './modelRedirect.ts'

const describe = Reflect.get(observation, 'logModelObservation') as (entry: { model?: string; actual_model?: string; status_code: number }) => { label: string; model: string; title: string } | null

test('mapped log model remains visible without asserting upstream dispatch on a local failure', () => {
  assert.equal(typeof describe, 'function')
  const result = describe({ model: 'canonical', actual_model: 'vendor/variant', status_code: 0 })
  assert.ok(result)
  assert.equal(result.model, 'vendor/variant')
  assert.equal(result.label, '映射模型')
  assert.match(result.title, /canonical → vendor\/variant/)
  assert.match(result.title, /完整请求/)
  assert.doesNotMatch(result.label, /实际|已发/)
})

test('legacy and successful log rows use neutral mapping wording while preserving exact variants', () => {
  assert.equal(typeof describe, 'function')
  for (const status_code of [200, 499, 503]) {
    assert.equal(describe({ model: 'deepseek-v4.1-flash', actual_model: 'deepSeek-v4.1-flash-1017', status_code })?.label, '映射模型')
  }
  assert.equal(describe({ model: 'canonical', status_code: 200 }), null)
})
