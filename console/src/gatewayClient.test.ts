import assert from 'node:assert/strict'
import test from 'node:test'
import { fetchGatewayModels, probeGateway } from './gatewayClient.ts'
import type { GatewayProtocol } from './pages/clientConnection.ts'

const secret = 'pf-synthetic-downstream-only'
const headers = { 'Content-Type': 'application/json', 'X-PivotFlow-Request-Id': 'pf_req_synthetic123' }
const jsonResponse = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers })
const successBodies: Record<GatewayProtocol, unknown> = {
  openai: { choices: [{ finish_reason: 'stop', message: { role: 'assistant', content: 'OK' } }] },
  codex: { status: 'completed', output: [{ type: 'message', role: 'assistant', content: [{ type: 'output_text', text: 'OK' }] }] },
  anthropic: { type: 'message', stop_reason: 'end_turn', content: [{ type: 'text', text: 'OK' }] },
  gemini: { candidates: [{ finishReason: 'STOP', content: { parts: [{ text: 'OK' }] } }] },
}

test('model discovery uses only relative public paths and the downstream credential', async (t) => {
  for (const protocol of ['openai', 'codex', 'anthropic', 'gemini'] as GatewayProtocol[]) {
    const mocked = t.mock.method(globalThis, 'fetch', async (url: string, init: RequestInit) => {
      assert.equal(url, protocol === 'gemini' ? '/v1beta/models' : `/v1/models?protocol=${protocol}`)
      assert.equal(init.credentials, 'omit')
      assert.equal(init.redirect, 'error')
      assert.equal(init.cache, 'no-store')
      assert.equal(init.method, 'GET')
      assert.ok(init.signal)
      const auth = new Headers(init.headers)
      assert.equal(auth.get(protocol === 'gemini' ? 'x-goog-api-key' : 'Authorization'), protocol === 'gemini' ? secret : `Bearer ${secret}`)
      assert.equal(auth.get('Cookie'), null)
      if (protocol === 'anthropic') assert.equal(auth.get('anthropic-version'), '2023-06-01')
      return jsonResponse(protocol === 'gemini' ? { models: [{ name: 'models/group/模型' }, { name: 'models/group/模型' }] } : { data: [{ id: 'group/模型' }, { id: 'group/模型' }] })
    })
    assert.deepEqual(await fetchGatewayModels(secret, protocol), ['group/模型'])
    assert.equal(mocked.mock.callCount(), 1)
    mocked.mock.restore()
  }
})

test('model discovery rejects missing or malformed rows instead of inventing an empty list', async (t) => {
  for (const body of [{ success: true, data: { models: [] } }, { data: [{ id: 'valid' }, { name: 'wrong-shape' }] }, { data: [{ id: '' }] }]) {
    const mocked = t.mock.method(globalThis, 'fetch', async () => jsonResponse(body))
    await assert.rejects(fetchGatewayModels(secret, 'codex'), /模型列表|响应/)
    mocked.mock.restore()
  }
})

test('four protocols require real terminal text and retain the public request ID', async (t) => {
  const paths: Record<GatewayProtocol, string> = { openai: '/v1/chat/completions', codex: '/v1/responses', anthropic: '/v1/messages', gemini: '/v1beta/models/group%2F%E6%A8%A1%E5%9E%8B:generateContent' }
  for (const protocol of Object.keys(paths) as GatewayProtocol[]) {
    const mocked = t.mock.method(globalThis, 'fetch', async (url: string, init: RequestInit) => {
      assert.equal(url, paths[protocol])
      assert.equal(init.method, 'POST')
      assert.equal(init.credentials, 'omit')
      assert.equal(init.redirect, 'error')
      const body = JSON.parse(String(init.body))
      if (protocol !== 'gemini') assert.equal(body.model, 'group/模型')
      return jsonResponse(successBodies[protocol])
    })
    const result = await probeGateway(secret, protocol, 'group/模型')
    assert.equal(result.status, 'success', protocol)
    assert.equal(result.text, 'OK')
    assert.equal(result.requestId, 'pf_req_synthetic123')
    assert.equal(result.httpStatus, 200)
    assert.equal(mocked.mock.callCount(), 1)
    mocked.mock.restore()
  }
})

test('HTTP 200 errors and incomplete text responses are not successful', async (t) => {
  const cases: [GatewayProtocol, unknown][] = [
    ['codex', { status: 'incomplete', output: [{ type: 'message', content: [{ type: 'output_text', text: 'partial' }] }] }],
    ['codex', { status: 'failed', error: { message: secret } }],
    ['codex', { status: 'completed', output: [] }],
    ['openai', { choices: [{ finish_reason: 'length', message: { content: 'partial' } }] }],
    ['openai', { choices: [{ finish_reason: null, message: { content: 'partial' } }] }],
    ['openai', { error: { type: 'server_error', message: secret } }],
    ['anthropic', { stop_reason: 'max_tokens', content: [{ type: 'text', text: 'partial' }] }],
    ['anthropic', { type: 'message', stop_reason: 'end_turn', content: [{ type: 'thinking', thinking: 'hidden' }] }],
    ['gemini', { candidates: [{ finishReason: 'MAX_TOKENS', content: { parts: [{ text: 'partial' }] } }] }],
    ['gemini', { promptFeedback: { blockReason: 'SAFETY' } }],
    ['gemini', { candidates: [{ finishReason: 'STOP', content: { parts: [{ functionCall: { name: 'anything' } }] } }] }],
  ]
  for (const [protocol, body] of cases) {
    const mocked = t.mock.method(globalThis, 'fetch', async () => jsonResponse(body))
    const result = await probeGateway(secret, protocol, 'test-model')
    assert.equal(result.status, 'failed', JSON.stringify(body))
    assert.ok(!JSON.stringify(result).includes(secret))
    assert.equal(result.requestId, 'pf_req_synthetic123')
    mocked.mock.restore()
  }
})

test('authentication, permission, quota and upstream failures use safe local explanations', async (t) => {
  const cases: [number, string][] = [[401, 'authentication'], [403, 'permission'], [429, 'rate_limit'], [402, 'quota'], [503, 'upstream']]
  for (const [status, reason] of cases) {
    const mocked = t.mock.method(globalThis, 'fetch', async () => jsonResponse({ error: { message: `leaked ${secret}`, type: 'invalid_request_error' } }, status))
    const result = await probeGateway(secret, 'openai', 'test-model')
    assert.equal(result.reason, reason)
    assert.equal(result.status, 'failed')
    assert.ok(!JSON.stringify(result).includes(secret))
    mocked.mock.restore()
  }
})

test('HTML, malformed JSON, oversized and suspicious IDs do not look like a successful model response', async (t) => {
  const responses = [
    new Response('<html>Sign in</html>', { headers: { 'Content-Type': 'text/html' } }),
    new Response('{broken', { headers }),
    new Response('x'.repeat(256 * 1024 + 1), { headers }),
  ]
  for (const response of responses) {
    const mocked = t.mock.method(globalThis, 'fetch', async () => response)
    assert.equal((await probeGateway(secret, 'openai', 'model')).status, 'failed')
    mocked.mock.restore()
  }
  t.mock.method(globalThis, 'fetch', async () => new Response(JSON.stringify(successBodies.openai), { headers: { ...headers, 'X-PivotFlow-Request-Id': 'https://evil.invalid/secret' } }))
  assert.equal((await probeGateway(secret, 'openai', 'model')).requestId, undefined)
})

test('invalid UTF-8 JSON is an invalid response rather than a network failure', async (t) => {
  const malformedUtf8 = new Uint8Array([0x7b, 0x22, 0x78, 0x22, 0x3a, 0x22, 0xc3, 0x28, 0x22, 0x7d])
  t.mock.method(globalThis, 'fetch', async () => new Response(malformedUtf8, { headers }))

  const probe = await probeGateway(secret, 'openai', 'model')
  assert.equal(probe.status, 'failed')
  assert.equal(probe.reason, 'invalid_response')

  await assert.rejects(fetchGatewayModels(secret, 'openai'), (error: unknown) => {
    return error instanceof Error && (error as Error & { reason?: string }).reason === 'invalid_response'
  })
})

test('a stopped or timed-out request remains unconfirmed and is never automatically replayed', async (t) => {
  for (const name of ['AbortError', 'TimeoutError', 'TypeError']) {
    const mocked = t.mock.method(globalThis, 'fetch', async () => { throw new DOMException('synthetic failure', name) })
    const result = await probeGateway(secret, 'codex', 'model')
    assert.equal(result.status, 'unknown')
    assert.equal(result.reason, name === 'AbortError' ? 'stopped' : name === 'TimeoutError' ? 'timeout' : 'network')
    assert.equal(mocked.mock.callCount(), 1)
    mocked.mock.restore()
  }
})

test('cancelling after response headers preserves the request ID for investigation', async (t) => {
  const controller = new AbortController()
  t.mock.method(globalThis, 'fetch', async () => {
    const body = new ReadableStream<Uint8Array>({ start(stream) { controller.abort(); stream.error(new DOMException('stopped', 'AbortError')) } })
    return new Response(body, { headers })
  })
  const result = await probeGateway(secret, 'codex', 'model', controller.signal)
  assert.equal(result.status, 'unknown')
  assert.equal(result.requestId, 'pf_req_synthetic123')
})

test('success preview redacts the exact downstream credential', async (t) => {
  t.mock.method(globalThis, 'fetch', async () => jsonResponse({ choices: [{ finish_reason: 'stop', message: { content: `OK ${secret}` } }] }))
  const result = await probeGateway(secret, 'openai', 'model')
  assert.equal(result.status, 'success')
  assert.ok(!result.text?.includes(secret))
})

test('invalid inputs and an already-cancelled probe do not reach fetch', async (t) => {
  const mocked = t.mock.method(globalThis, 'fetch', async () => jsonResponse(successBodies.openai))
  const controller = new AbortController(); controller.abort()
  assert.equal((await probeGateway(secret, 'openai', 'model', controller.signal)).status, 'cancelled')
  assert.equal((await probeGateway('bad\nheader', 'openai', 'model')).status, 'failed')
  assert.equal((await probeGateway(secret, 'openai', 'bad\nmodel')).status, 'failed')
  assert.equal(mocked.mock.callCount(), 0)
})
