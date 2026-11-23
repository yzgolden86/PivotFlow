import assert from 'node:assert/strict'
import test from 'node:test'
import { buildClientArtifacts, clientProtocol, maskArtifact, normalizeGatewayRoot, tokenMetadata, tokenUnavailableReason, type ClientKind } from './clientConnection.ts'
import type { AuthToken } from '../types'

const token = "pf-synthetic-'\"$`\\-only"
const model = "group/模型-'\"$`\\"
const input = { shell: 'powershell' as const, gatewayRoot: 'https://gateway.example/prefix/v1/', model, token }

test('gateway root normalization preserves prefix and removes one API suffix', () => {
  for (const suffix of ['', '/', '/v1/', '/v1beta']) {
    assert.equal(normalizeGatewayRoot(`https://gateway.example/prefix${suffix}`), 'https://gateway.example/prefix')
  }
  assert.equal(normalizeGatewayRoot(' http://127.0.0.1:8080/v1 '), 'http://127.0.0.1:8080')
  for (const address of ['ftp://gateway.example', 'https://key@gateway.example', 'https://gateway.example?key=secret', 'https://gateway.example/#x', 'https://gateway.example/v1/chat/completions', 'https://gateway.example/web/console/', 'https://gateway.example/\nsecret', 'https://gateway.example/v1/v1']) {
    assert.throws(() => normalizeGatewayRoot(address), { name: 'Error' }, address)
  }
})

test('four clients use the correct native protocol', () => {
  assert.deepEqual((['codex', 'claude', 'gemini', 'openai-python'] as ClientKind[]).map(clientProtocol), ['codex', 'anthropic', 'gemini', 'openai'])
})

test('Codex uses Responses with an environment key and root/v1', () => {
  const artifacts = buildClientArtifacts({ ...input, client: 'codex' })
  const config = artifacts.find((file) => file.kind === 'config')!
  assert.match(config.content, /base_url = "https:\/\/gateway.example\/prefix\/v1"/)
  assert.match(config.content, /wire_api = "responses"/)
  assert.match(config.content, /env_key = "PIVOTFLOW_API_KEY"/)
  assert.match(config.content, /requires_openai_auth = false/)
  assert.ok(config.content.includes(`model = ${JSON.stringify(model)}`))
  assert.ok(!config.content.includes(token))
  assert.ok(artifacts.some((file) => file.kind === 'environment' && file.content.includes(token.replaceAll("'", "''"))))
})

test('Claude config pins session, alias and helper models to the permitted alias', () => {
  const config = buildClientArtifacts({ ...input, client: 'claude' }).find((file) => file.kind === 'config')!
  const env = JSON.parse(config.content).env
  assert.equal(env.ANTHROPIC_BASE_URL, 'https://gateway.example/prefix')
  assert.equal(env.ANTHROPIC_AUTH_TOKEN, token)
  for (const key of ['ANTHROPIC_MODEL', 'ANTHROPIC_DEFAULT_OPUS_MODEL', 'ANTHROPIC_DEFAULT_FABLE_MODEL', 'ANTHROPIC_DEFAULT_SONNET_MODEL', 'ANTHROPIC_DEFAULT_HAIKU_MODEL', 'CLAUDE_CODE_SUBAGENT_MODEL']) assert.equal(env[key], model)
})

test('Gemini selects API key authentication and keeps v1beta out of base URL', () => {
  const files = buildClientArtifacts({ ...input, client: 'gemini' })
  const config = JSON.parse(files.find((file) => file.kind === 'config')!.content)
  assert.equal(config.security.auth.selectedType, 'gemini-api-key')
  assert.equal(config.model.name, model)
  const env = files.find((file) => file.kind === 'environment')!.content
  assert.ok(env.includes("$env:GOOGLE_GEMINI_BASE_URL = 'https://gateway.example/prefix'"))
  assert.ok(env.includes("$env:GOOGLE_GENAI_API_VERSION = 'v1beta'"))
  assert.ok(env.includes('$env:GEMINI_API_KEY'))
})

test('Gemini respects its documented HTTPS or loopback base URL contract', () => {
  assert.throws(() => buildClientArtifacts({ ...input, client: 'gemini', gatewayRoot: 'http://gateway.example' }), /Gemini.*HTTPS/)
  for (const root of ['http://localhost:8080', 'http://127.0.0.1:8080', 'http://[::1]:8080', 'https://gateway.example']) {
    assert.ok(buildClientArtifacts({ ...input, client: 'gemini', gatewayRoot: root }).length)
  }
  assert.ok(buildClientArtifacts({ ...input, client: 'codex', gatewayRoot: 'http://gateway.example' }).length)
})

test('OpenAI Python reads an explicit downstream environment key', () => {
  const config = buildClientArtifacts({ ...input, client: 'openai-python' }).find((file) => file.kind === 'config')!
  assert.match(config.content, /api_key=os.environ\["PIVOTFLOW_API_KEY"\]/)
  assert.match(config.content, /base_url="https:\/\/gateway.example\/prefix\/v1"/)
  assert.match(config.content, /client.chat.completions.create/)
  assert.ok(config.content.includes(`model=${JSON.stringify(model)}`))
  assert.ok(!config.content.includes(token))
})

test('PowerShell and Bash quote literals without evaluating client values', () => {
  const pwsh = buildClientArtifacts({ ...input, client: 'codex' })
  const bash = buildClientArtifacts({ ...input, client: 'codex', shell: 'bash' })
  assert.ok(pwsh.find((file) => file.kind === 'environment')!.content.includes(`'${token.replaceAll("'", "''")}'`))
  assert.ok(bash.find((file) => file.kind === 'environment')!.content.includes(`'${token.replaceAll("'", `'"'"'`)}'`))
  assert.match(pwsh.find((file) => file.kind === 'verify')!.content, /Invoke-WebRequest/)
  assert.match(bash.find((file) => file.kind === 'verify')!.content, /curl /)
  for (const file of [...pwsh, ...bash]) assert.ok(!file.content.includes('Authorization: Bearer $'))
})

test('each terminal verification request uses the selected protocol and a header token', () => {
  const paths = { codex: '/v1/responses', claude: '/v1/messages', gemini: '/v1beta/models/', 'openai-python': '/v1/chat/completions' }
  for (const client of Object.keys(paths) as ClientKind[]) {
    const command = buildClientArtifacts({ ...input, client }).find((file) => file.kind === 'verify')!.content
    assert.ok(command.includes(paths[client]))
    assert.ok(!command.includes('?key='))
    assert.ok(!command.includes('/admin/'))
    assert.ok(!command.includes('/v1/v1'))
  }
})

test('masked previews hide JSON and shell encodings of the same token', () => {
  for (const client of ['codex', 'claude', 'gemini', 'openai-python'] as ClientKind[]) {
    for (const shell of ['bash', 'powershell'] as const) {
      for (const file of buildClientArtifacts({ ...input, client, shell })) {
        const masked = maskArtifact(file, token, shell)
        assert.ok(!masked.includes(token))
        assert.ok(!masked.includes(JSON.stringify(token).slice(1, -1)))
        assert.ok(!masked.includes(token.replaceAll("'", "''")))
      }
    }
  }
})

test('invalid model and header tokens fail before generating configuration', () => {
  for (const bad of ['', 'x\ny', 'x\ry', '\u0000x']) {
    assert.throws(() => buildClientArtifacts({ ...input, client: 'codex', model: bad }))
    assert.throws(() => buildClientArtifacts({ ...input, client: 'codex', token: bad }))
  }
})

test('literal application placeholders survive JSON configuration and script generation', () => {
  const value = '{{NAME_ab12cd34}}'
  const files = buildClientArtifacts({ ...input, model: value, token: value, client: 'claude' })
  assert.equal(JSON.parse(files[0].content).env.ANTHROPIC_AUTH_TOKEN, value)
  assert.ok(files.find((file) => file.kind === 'verify')!.content.includes(value))
})

test('token status distinguishes expiry, inactivity, quota and unrecoverable secrets', () => {
  const now = 1_000
  const active = { is_active: true, token_recoverable: true, cost_limit_usd: 0, cost_used_usd: 0 }
  assert.equal(tokenUnavailableReason(active, now), '')
  assert.match(tokenUnavailableReason({ ...active, is_active: false }, now), /停用/)
  assert.match(tokenUnavailableReason({ ...active, expires_at: 500 }, now), /过期/)
  assert.match(tokenUnavailableReason({ ...active, cost_limit_usd: 5, cost_used_usd: 5 }, now), /限额/)
  assert.match(tokenUnavailableReason({ ...active, token_recoverable: false }, now), /恢复/)
})

test('created token metadata never retains the one-time plaintext', () => {
  const raw = { id: 1, token, token_hint: 'pf-****only' } as AuthToken
  const metadata = tokenMetadata(raw)
  assert.equal(metadata.token, 'pf-****only')
  assert.equal(raw.token, token)
  assert.ok(!JSON.stringify(metadata).includes(token))
})
