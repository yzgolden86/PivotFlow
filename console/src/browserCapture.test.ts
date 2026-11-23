import assert from 'node:assert/strict'
import { test } from 'node:test'
import { runInNewContext } from 'node:vm'
import { buildBrowserCaptureBookmarklet, buildBrowserCaptureScript } from './browserCapture.ts'

function refusalContext(origin: string, framed = false) {
  const current = { location: { origin }, top: null as unknown, self: null as unknown }
  current.self = current
  current.top = framed ? {} : current
  let reads = 0
  return {
    context: { window: current, URL, localStorage: { getItem: () => { reads++; throw new Error('refusal must not read storage') } } },
    reads: () => reads,
  }
}

test('实际序列化脚本在 iframe 直接拒绝且不读取存储', () => {
  const fixture = refusalContext('https://source.example', true)
  const result = runInNewContext(buildBrowserCaptureScript({ expectedOrigin: 'https://source.example' }), fixture.context)
  assert.equal(result, 'frame_refused')
  assert.equal(fixture.reads(), 0)
})

test('实际脚本核对完整 origin 包括非默认端口', () => {
  const fixture = refusalContext('https://source.example:8443')
  const result = runInNewContext(buildBrowserCaptureScript({ expectedOrigin: 'https://source.example' }), fixture.context)
  assert.equal(result, 'origin_refused')
  assert.equal(fixture.reads(), 0)
})

test('书签脚本解码后仍是自包含可执行函数', () => {
  const bookmark = buildBrowserCaptureBookmarklet({ expectedOrigin: 'https://source.example', platform: 'new-api-family' })
  assert.ok(bookmark.startsWith('javascript:'))
  const fixture = refusalContext('https://other.example')
  assert.equal(runInNewContext(decodeURIComponent(bookmark.slice('javascript:'.length)), fixture.context), undefined)
  assert.equal(fixture.reads(), 0)
})

test('只写入允许的助手参数，网关或管理员凭证不进下载脚本', () => {
  const secret = '{{SECRET_ujwitbef}}'
  const source = buildBrowserCaptureScript({ expectedOrigin: 'https://source.example', platform: 'new-api-family', token: secret, gateway: 'https://gateway.example' } as never)
  assert.ok(!source.includes(secret))
  assert.ok(!source.includes('https://gateway.example'))
  assert.throws(() => buildBrowserCaptureScript({ expectedOrigin: 'https://user:password@source.example' }), /来源/)
  assert.throws(() => buildBrowserCaptureScript({ expectedOrigin: 'https://source.example/path' }), /来源/)
  assert.throws(() => buildBrowserCaptureScript({ expectedOrigin: 'javascript:alert(1)' }), /来源/)
  assert.throws(() => buildBrowserCaptureScript({ platform: 'unsupported-platform' }), /平台/)
})
