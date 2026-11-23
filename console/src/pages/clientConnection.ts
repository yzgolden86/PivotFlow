import type { AuthToken } from '../types'

export type ClientKind = 'codex' | 'claude' | 'gemini' | 'openai-python'
export type ClientShell = 'powershell' | 'bash'
export type GatewayProtocol = 'codex' | 'anthropic' | 'gemini' | 'openai'

export const connectionClients: { id: ClientKind; label: string; detail: string; source: string }[] = [
  { id: 'codex', label: 'Codex', detail: 'Responses', source: 'https://developers.openai.com/codex/config-reference/' },
  { id: 'claude', label: 'Claude Code', detail: 'Messages', source: 'https://code.claude.com/docs/en/llm-gateway-connect' },
  { id: 'gemini', label: 'Gemini CLI', detail: 'GenerateContent', source: 'https://geminicli.com/docs/get-started/authentication/' },
  { id: 'openai-python', label: 'OpenAI SDK', detail: 'Python · Chat Completions', source: 'https://github.com/openai/openai-python' },
]

export interface ClientArtifact {
  kind: 'config' | 'environment' | 'verify'
  fileName: string
  label: string
  destination: string
  instructions: string
  content: string
  containsToken: boolean
}

export function clientProtocol(client: ClientKind): GatewayProtocol {
  return ({ codex: 'codex', claude: 'anthropic', gemini: 'gemini', 'openai-python': 'openai' } as const)[client]
}

export function normalizeGatewayRoot(input: string): string {
  if (/[\u0000-\u001f\u007f]/u.test(input)) throw new Error('网关地址不能含控制字符')
  let url: URL
  try { url = new URL(input.trim()) } catch { throw new Error('请输入完整的 HTTP(S) 网关地址') }
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || /[?#]/u.test(input)) {
    throw new Error('网关地址仅支持 HTTP(S)，不能含认证信息、查询参数或片段')
  }
  let path = url.pathname.replace(/\/+$/u, '')
  if (/\/(?:web|admin|dashboard)(?:\/|$)/u.test(path) || /\/(?:chat\/completions|responses|messages|models)(?:\/|$)/u.test(path)) {
    throw new Error('请填写网关根地址或 /v1、/v1beta 地址，不要填写控制台或具体接口')
  }
  path = path.replace(/\/v1(?:beta)?$/u, '')
  if (/\/v1(?:beta)?(?:\/|$)/u.test(path) || path.includes('//')) throw new Error('网关地址包含重复的 API 路径')
  return url.origin + path
}

export function normalizeClientGatewayRoot(client: ClientKind, input: string): string {
  const root = normalizeGatewayRoot(input)
  const url = new URL(root)
  if (client === 'gemini' && url.protocol !== 'https:' && !['localhost', '127.0.0.1', '[::1]'].includes(url.hostname)) {
    throw new Error('Gemini CLI 的非本机网关地址需要 HTTPS；本机可使用 localhost、127.0.0.1 或 [::1]')
  }
  return root
}

function checkedValue(value: string, label: string): string {
  if (!value.trim() || value.length > 2048 || /[\u0000-\u001f\u007f]/u.test(value)) throw new Error(`${label}为空、过长或含控制字符`)
  return value
}

export function buildGatewayProbeRequest(protocol: GatewayProtocol, model: string): { path: string; headers: Record<string, string>; body: Record<string, unknown> } {
  checkedValue(model, '模型名称')
  const prompt = '请只回复 OK。'
  switch (protocol) {
    case 'codex': return { path: '/v1/responses', headers: {}, body: { model, input: prompt, max_output_tokens: 256, stream: false } }
    case 'anthropic': return { path: '/v1/messages', headers: { 'anthropic-version': '2023-06-01' }, body: { model, max_tokens: 256, messages: [{ role: 'user', content: prompt }], stream: false } }
    case 'gemini': return { path: `/v1beta/models/${encodeURIComponent(model)}:generateContent`, headers: {}, body: { contents: [{ role: 'user', parts: [{ text: prompt }] }], generationConfig: { maxOutputTokens: 256 } } }
    case 'openai': return { path: '/v1/chat/completions', headers: {}, body: { model, messages: [{ role: 'user', content: prompt }], max_completion_tokens: 256, stream: false } }
  }
}

export function gatewayAuthHeaders(token: string, protocol: GatewayProtocol): Record<string, string> {
  checkedValue(token, '下游令牌')
  return protocol === 'gemini' ? { 'x-goog-api-key': token } : { Authorization: `Bearer ${token}` }
}

function shellLiteral(value: string, shell: ClientShell): string {
  return `'${value.replaceAll("'", shell === 'powershell' ? "''" : `'"'"'`)}'`
}

function environmentLines(values: Record<string, string>, shell: ClientShell): string {
  return Object.entries(values).map(([name, value]) => shell === 'powershell'
    ? `$env:${name} = ${shellLiteral(value, shell)}`
    : `export ${name}=${shellLiteral(value, shell)}`).join('\n')
}

function verificationCommand(root: string, protocol: GatewayProtocol, model: string, token: string, shell: ClientShell): string {
  const request = buildGatewayProbeRequest(protocol, model)
  const headers = { ...gatewayAuthHeaders(token, protocol), ...request.headers }
  const address = root + request.path
  const body = JSON.stringify(request.body)
  if (shell === 'powershell') {
    const entries = Object.entries(headers).map(([name, value]) => `  ${shellLiteral(name, shell)} = ${shellLiteral(value, shell)}`).join('\n')
    return `$ErrorActionPreference = 'Stop'\n$headers = @{\n${entries}\n}\n$payload = ${shellLiteral(body, shell)}\n$response = Invoke-WebRequest -UseBasicParsing -SkipHeaderValidation -ErrorAction Stop -Method Post -TimeoutSec 60 -Uri ${shellLiteral(address, shell)} -Headers $headers -ContentType 'application/json' -Body ([System.Text.Encoding]::UTF8.GetBytes($payload))\n$response.Headers['X-PivotFlow-Request-Id']\n$response.Content\n`
  }
  const entries = Object.entries({ ...headers, 'Content-Type': 'application/json' }).map(([name, value]) => `  --header ${shellLiteral(`${name}: ${value}`, shell)}`).join(' \\\n')
  return `curl --fail-with-body --show-error --include --max-time 60 --request POST ${shellLiteral(address, shell)} \\\n${entries} \\\n  --data-binary @- <<'PIVOTFLOW_REQUEST'\n${body}\nPIVOTFLOW_REQUEST\n`
}

export function buildClientArtifacts(input: { client: ClientKind; shell: ClientShell; gatewayRoot: string; model: string; token: string }): ClientArtifact[] {
  const { client, shell, model, token } = input
  const root = normalizeClientGatewayRoot(client, input.gatewayRoot)
  checkedValue(model, '模型名称'); checkedValue(token, '下游令牌')
  const extension = shell === 'powershell' ? 'ps1' : 'sh'
  let config: ClientArtifact
  let env: Record<string, string>
  let launch: string
  switch (client) {
    case 'codex':
      config = {
        kind: 'config', fileName: 'pivotflow-codex.toml', label: '配置文件', destination: '~/.codex/config.toml', containsToken: false,
        instructions: '将顶层 model 与 model_provider 合并到文件顶层，将 provider 表合并到对应位置。相同 provider 表只保留一份；不要直接覆盖已有配置。',
        content: `model = ${JSON.stringify(model)}\nmodel_provider = "pivotflow"\n\n[model_providers.pivotflow]\nname = "PivotFlow"\nbase_url = ${JSON.stringify(root + '/v1')}\nenv_key = "PIVOTFLOW_API_KEY"\nwire_api = "responses"\nrequires_openai_auth = false\n`,
      }
      env = { PIVOTFLOW_API_KEY: token }; launch = 'codex'; break
    case 'claude': {
      env = {
        ANTHROPIC_BASE_URL: root, ANTHROPIC_AUTH_TOKEN: token, ANTHROPIC_MODEL: model,
        ANTHROPIC_DEFAULT_OPUS_MODEL: model, ANTHROPIC_DEFAULT_FABLE_MODEL: model,
        ANTHROPIC_DEFAULT_SONNET_MODEL: model, ANTHROPIC_DEFAULT_HAIKU_MODEL: model, CLAUDE_CODE_SUBAGENT_MODEL: model,
      }
      config = {
        kind: 'config', fileName: 'pivotflow-claude.json', label: '配置文件', destination: '~/.claude/settings.json', containsToken: true,
        instructions: '将 env 字段合并到现有 settings.json。默认会话、模型别名与默认子代理都使用所选模型；项目或管理设置仍可能覆盖它们。',
        content: JSON.stringify({ env }, null, 2) + '\n',
      }
      launch = 'claude'; break
    }
    case 'gemini':
      config = {
        kind: 'config', fileName: 'pivotflow-gemini.json', label: '配置文件', destination: '~/.gemini/settings.json', containsToken: false,
        instructions: '合并 security.auth.selectedType 与 model.name，选择 Gemini API key 认证；在启动终端中设置下面的环境变量。',
        content: JSON.stringify({ security: { auth: { selectedType: 'gemini-api-key' } }, model: { name: model } }, null, 2) + '\n',
      }
      env = { GEMINI_API_KEY: token, GOOGLE_GEMINI_BASE_URL: root, GOOGLE_GENAI_API_VERSION: 'v1beta' }
      launch = `gemini --model ${shellLiteral(model, shell)}`; break
    case 'openai-python':
      config = {
        kind: 'config', fileName: 'pivotflow_openai.py', label: 'Python 示例', destination: 'pivotflow_openai.py', containsToken: false,
        instructions: '先安装 openai（python -m pip install openai），设置环境变量后运行此文件。客户端自动重试已关闭，便于首次验证。',
        content: `import os\nfrom openai import OpenAI\n\nclient = OpenAI(\n    api_key=os.environ["PIVOTFLOW_API_KEY"],\n    base_url=${JSON.stringify(root + '/v1')},\n    max_retries=0,\n    timeout=60.0,\n)\nresponse = client.chat.completions.create(\n    model=${JSON.stringify(model)},\n    messages=[{"role": "user", "content": "请只回复 OK。"}],\n)\nprint(response.choices[0].message.content)\n`,
      }
      env = { PIVOTFLOW_API_KEY: token }; launch = 'python pivotflow_openai.py'; break
  }
  return [config, {
    kind: 'environment', fileName: `pivotflow-${client}-env.${extension}`, label: '环境变量与启动', destination: '客户端启动终端', containsToken: true,
    instructions: '在同一终端设置这些变量并启动客户端。这里只生成内容，不修改你的本地文件或系统环境。',
    content: environmentLines(env, shell) + '\n' + launch + '\n',
  }, {
    kind: 'verify', fileName: `pivotflow-${client}-verify.${extension}`, label: '终端验证', destination: shell === 'powershell' ? 'PowerShell 7+' : 'Bash · cURL 7.76+', containsToken: true,
    instructions: '在客户端设备执行一次最小 HTTP 请求以验证填写的地址。它会按正常模型调用计费，输出响应和请求 ID。',
    content: verificationCommand(root, clientProtocol(client), model, token, shell),
  }]
}

export function maskArtifact(artifact: ClientArtifact, token: string, shell: ClientShell): string {
  if (!token) return artifact.content
  const forms = [token, JSON.stringify(token).slice(1, -1), shellLiteral(token, shell).slice(1, -1)]
  return [...new Set(forms)].sort((a, b) => b.length - a.length).reduce((content, encoded) => content.replaceAll(encoded, 'YOUR_PIVOTFLOW_KEY'), artifact.content)
}

export function tokenMetadata(token: AuthToken): AuthToken {
  return { ...token, token: token.token_hint || '' }
}

export function tokenUnavailableReason(token: Pick<AuthToken, 'is_active' | 'expires_at' | 'cost_limit_usd' | 'cost_used_usd' | 'token_recoverable'>, now = Date.now()): string {
  if (!token.is_active) return '令牌已停用，请在令牌管理中启用'
  if (token.expires_at && now > token.expires_at) return '令牌已过期，请创建新令牌'
  if (token.cost_limit_usd > 0 && token.cost_used_usd >= token.cost_limit_usd) return '令牌费用限额已用完，请调整限额或创建新令牌'
  if (!token.token_recoverable) return '此历史令牌的完整内容不可恢复，请创建新令牌'
  return ''
}
