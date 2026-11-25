import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ArrowRight, Check, ChevronDown, Copy, Download, ExternalLink, KeyRound, PlugZap, Plus, RefreshCw, Square, Terminal } from 'lucide-react'
import { useLocation } from 'react-router-dom'
import { getAuthTokens, getChannels, getSiteInventory, getSiteModels, revealAuthToken } from '../api'
import type { AuthToken } from '../types'
import { fetchGatewayModels, gatewayFailureMessage, probeGateway, type GatewayProbeResult } from '../gatewayClient'
import { canApplyGatewayProbe, invalidateGatewayProbe, requestedTokenSelectionTarget, startGatewayProbe, type GatewayProbeSelection, type GatewayProbeSession } from './connectProbeSelection'
import HelpTip from '../components/HelpTip'
import { TokenForm } from './TokensPage'
import { formatMoney, LoadingState, PageHeader } from './shared'
import { requestDetailHref } from './requestHistory'
import { buildConnectionGuide, type ConnectConfigurationInput } from './connectPresentation'
import {
  buildClientArtifacts, clientProtocol, connectionClients, maskArtifact, normalizeClientGatewayRoot, tokenMetadata, tokenUnavailableReason,
  type ClientArtifact, type ClientKind, type ClientShell, type GatewayProtocol,
} from './clientConnection'
import '../connectPresentation.css'
import './connectWorkspace.css'

const EMPTY_CONFIGURATION: ConnectConfigurationInput = { sites: null, accounts: null, facts: null, channels: null }

export default function ConnectPage() {
  const location = useLocation()
  const requestedToken = Number(new URLSearchParams(location.search).get('token')) || 0
  const [tokens, setTokens] = useState<AuthToken[]>([])
  const [configuration, setConfiguration] = useState<ConnectConfigurationInput>(EMPTY_CONFIGURATION)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [tokenId, setTokenId] = useState(requestedToken)
  const [secret, setSecret] = useState('')
  const [reading, setReading] = useState(false)
  const [secretError, setSecretError] = useState('')
  const [creating, setCreating] = useState(false)
  const [client, setClient] = useState<ClientKind>('codex')
  const [shell, setShell] = useState<ClientShell>('powershell')
  const [gatewayRoot, setGatewayRoot] = useState(() => window.location.origin)
  const [model, setModel] = useState('')
  const [inventory, setInventory] = useState<{ tokenId: number; protocol: GatewayProtocol; models: string[] } | null>(null)
  const [modelsLoading, setModelsLoading] = useState(false)
  const [modelsError, setModelsError] = useState('')
  const [modelRevision, setModelRevision] = useState(0)
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<GatewayProbeResult | null>(null)
  const [now, setNow] = useState(Date.now())
  const mounted = useRef(false)
  const loadSequence = useRef(0)
  const revealSequence = useRef(0)
  const selectedId = useRef(requestedToken)
  const lastRequestedToken = useRef(requestedToken)
  const revealController = useRef<AbortController | null>(null)
  const testSession = useRef<GatewayProbeSession | null>(null)
  const selectionGeneration = useRef(0)
  const protocol = clientProtocol(client)
  const probeSelection = {
    tokenId,
    requestedToken,
    token: secret,
    protocol,
    model,
    generation: selectionGeneration.current,
  } satisfies GatewayProbeSelection
  const probeSelectionRef = useRef(probeSelection)
  probeSelectionRef.current = probeSelection
  const selectedToken = tokens.find((item) => item.id === tokenId)
  const unavailable = selectedToken ? tokenUnavailableReason({ ...selectedToken, token_recoverable: Boolean(secret) || selectedToken.token_recoverable }, now) : ''
  const models = inventory?.tokenId === tokenId && inventory.protocol === protocol && secret ? inventory.models : []

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false; revealController.current?.abort(); testSession.current?.controller.abort() }
  }, [])
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30_000)
    return () => window.clearInterval(timer)
  }, [])

  const invalidateTest = useCallback(() => {
    selectionGeneration.current++
    testSession.current = invalidateGatewayProbe(testSession.current)
    if (mounted.current) setTesting(false)
  }, [])
  const changeToken = useCallback((id: number) => {
    invalidateTest()
    revealSequence.current++; revealController.current?.abort()
    selectedId.current = id
    setTokenId(id); setSecret(''); setReading(false); setSecretError(''); setInventory(null); setModel(''); setResult(null)
  }, [invalidateTest])
  useEffect(() => {
    const target = requestedTokenSelectionTarget(lastRequestedToken.current, requestedToken, selectedId.current)
    lastRequestedToken.current = requestedToken
    if (target !== null) changeToken(target)
  }, [requestedToken, changeToken])

  const load = useCallback(async (signal?: AbortSignal) => {
    const sequence = ++loadSequence.current
    setLoading(true); setLoadError(''); setConfiguration(EMPTY_CONFIGURATION)
    const responses = await Promise.allSettled([
      getSiteInventory(signal, { force: true }),
      getChannels({ status: 'enabled', limit: 1000, offset: 0 }, signal, { force: true }),
      getSiteModels({ limit: 2000 }, signal),
      getAuthTokens('today', signal),
    ])
    if (!mounted.current || signal?.aborted || sequence !== loadSequence.current) return
    const [siteResponse, channelResponse, modelResponse, tokenResponse] = responses
    setConfiguration({
      sites: siteResponse.status === 'fulfilled' ? siteResponse.value.sites : null,
      accounts: siteResponse.status === 'fulfilled' ? siteResponse.value.accounts : null,
      facts: modelResponse.status === 'fulfilled' ? modelResponse.value.data : null,
      channels: channelResponse.status === 'fulfilled' ? channelResponse.value.data : null,
    })
    if (tokenResponse.status === 'fulfilled') {
      const rows = (tokenResponse.value.tokens || []).map(tokenMetadata)
      setTokens(rows)
      const selected = rows.find((item) => item.id === selectedId.current)
      if (!selected) {
        const missing = selectedId.current > 0
        changeToken(missing ? 0 : rows.find((item) => !tokenUnavailableReason(item))?.id || rows[0]?.id || 0)
        if (missing) setSecretError('指定令牌不存在或已被删除，请重新选择或创建令牌。')
      }
      else if (!selected.is_active || (selected.expires_at && Date.now() > selected.expires_at) || (selected.cost_limit_usd > 0 && selected.cost_used_usd >= selected.cost_limit_usd)) changeToken(selected.id)
    }
    if (responses.some((response) => response.status === 'rejected')) setLoadError('部分接入信息读取失败。请刷新后再确认来源、渠道与令牌状态。')
    setLoading(false)
  }, [changeToken])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  // A model list belongs to one exact token/protocol selection. An older list
  // can never enable a request while the new selection is still loading.
  useEffect(() => {
    const controller = new AbortController()
    setInventory(null); setModel(''); setModelsError(''); setResult(null)
    if (!secret || !tokenId) { setModelsLoading(false); return () => controller.abort() }
    setModelsLoading(true)
    void fetchGatewayModels(secret, protocol, controller.signal).then((names) => {
      if (controller.signal.aborted || !mounted.current) return
      setInventory({ tokenId, protocol, models: names }); setModel(names[0] || '')
    }).catch((error: unknown) => {
      if (!controller.signal.aborted && mounted.current) setModelsError(error instanceof Error && error.name === 'GatewayClientError' ? error.message : '可用模型读取失败，请刷新后重试')
    }).finally(() => { if (!controller.signal.aborted && mounted.current) setModelsLoading(false) })
    return () => controller.abort()
  }, [secret, tokenId, protocol, modelRevision])

  const readSecret = async () => {
    if (!selectedToken || unavailable || testing) return
    const selected = selectedToken.id
    const sequence = ++revealSequence.current
    const controller = new AbortController()
    revealController.current?.abort(); revealController.current = controller
    setReading(true); setSecretError(''); setSecret(''); setInventory(null); setResult(null)
    try {
      const value = await revealAuthToken(selected, controller.signal)
      if (sequence !== revealSequence.current || controller.signal.aborted || !mounted.current) return
      if (value.id !== selected || typeof value.token !== 'string' || !value.token) throw new Error('invalid reveal')
      setSecret(value.token)
    } catch {
      if (!controller.signal.aborted && mounted.current && sequence === revealSequence.current) setSecretError('完整令牌读取失败。请刷新令牌状态后重试，或创建新令牌。')
    } finally { if (mounted.current && sequence === revealSequence.current) setReading(false) }
  }

  const ready = Boolean(secret && selectedToken && !unavailable && models.includes(model) && !modelsLoading && !reading)
  const generated = useMemo(() => {
    try {
      const root = normalizeClientGatewayRoot(client, gatewayRoot)
      return { root, error: '', artifacts: ready ? buildClientArtifacts({ client, shell, gatewayRoot: root, model, token: secret }) : [] }
    } catch (error) { return { root: '', error: error instanceof Error ? error.message : '网关地址无效', artifacts: [] } }
  }, [gatewayRoot, ready, client, shell, model, secret])
  const differentAddress = Boolean(generated.root && generated.root !== window.location.origin)

  const test = async () => {
    if (!ready || testing || generated.error) return
    if (selectedToken && tokenUnavailableReason({ ...selectedToken, token_recoverable: true })) { setNow(Date.now()); return }
    const session = startGatewayProbe(probeSelectionRef.current)
    testSession.current = session
    setTesting(true); setResult(null)
    try {
      const response = await probeGateway(session.selection.token, session.selection.protocol, session.selection.model, session.controller.signal)
      if (mounted.current && canApplyGatewayProbe(session, probeSelectionRef.current, testSession.current)) setResult(response)
    } finally {
      if (mounted.current && testSession.current === session) {
        testSession.current = null
        setTesting(false)
      }
    }
  }

  const refresh = async () => {
    if (testing || reading) return
    await load()
    if (mounted.current) setModelRevision((revision) => revision + 1)
  }

  const permissionSummary = selectedToken
    ? [selectedToken.allowed_models?.length ? `模型 ${selectedToken.allowed_models.length} 个` : '模型不限', selectedToken.allowed_channel_ids?.length ? `渠道 ${selectedToken.allowed_channel_ids.length} 个` : '渠道不限', selectedToken.cost_limit_usd ? `额度 ${formatMoney(selectedToken.cost_used_usd)} / ${formatMoney(selectedToken.cost_limit_usd)}` : '额度不限'].join(' · ')
    : ''

  return <div className="workspace-page connect-page">
    <PageHeader icon={PlugZap} title="接入配置" tone="blue" actions={<><HelpTip label="接入配置" text="为客户端生成网关配置。选择令牌和模型后，可复制配置，并按需发送一次真实验证请求。" /><button className="icon-button icon-button--surface" type="button" onClick={() => void refresh()} disabled={loading || testing || reading} aria-label="刷新接入状态"><RefreshCw size={17} className={loading ? 'spin' : undefined} /></button></>} />
    {loadError && <div className="inline-error" role="alert">{loadError}</div>}
    <ConnectionGuide configuration={configuration} tokens={tokens.length} configured={ready && !generated.error} verified={result?.status === 'success'} loading={loading} />
    <div className="connect-layout">
      <div className="connect-workspace">
        <section className="connect-panel connect-selection" aria-labelledby="connect-selection-title">
          <header className="connect-panel-heading"><h2 id="connect-selection-title">选择令牌与模型</h2><HelpTip label="令牌与模型" text="读取或创建一个下游访问令牌，再选择此令牌可以访问的模型。完整令牌只保留在本页内存中。" /></header>
          <div className="connect-token-controls">
            <label className="connect-field">下游访问令牌<select value={tokenId} onChange={(event) => changeToken(Number(event.target.value))} disabled={testing || reading || loading} aria-label="下游访问令牌">
              <option value={0} disabled>{tokens.length ? '选择访问令牌' : '尚未创建访问令牌'}</option>
              {tokens.map((token) => <option key={token.id} value={token.id}>{token.description} · #{token.id}</option>)}
            </select></label>
            <button className={secret ? 'secondary-button' : 'primary-button'} type="button" onClick={() => void readSecret()} disabled={!selectedToken || Boolean(unavailable) || reading || testing || loading}>{reading ? <RefreshCw size={15} className="spin" /> : <KeyRound size={15} />}{reading ? '读取中' : secret ? '重新读取令牌' : '读取现有令牌'}</button>
            <button className="secondary-button" type="button" onClick={() => setCreating(true)} disabled={testing || reading || loading}><Plus size={15} />创建令牌</button>
          </div>
          {unavailable && <p className="connect-warning" role="status">{unavailable}</p>}
          {secretError && <p className="inline-error" role="alert">{secretError}</p>}
          {selectedToken && <details className="connect-permissions" aria-label="令牌权限与额度"><summary><span>令牌权限与额度</span><span>{permissionSummary}</span><ChevronDown size={14} aria-hidden="true" /></summary><div className="connect-permissions-body"><span>协议 {selectedToken.allowed_protocols?.length ? selectedToken.allowed_protocols.join(' / ') : '不限'}</span><span>模型 {selectedToken.allowed_models?.length ? `${selectedToken.allowed_models.length} 个` : '不限'}</span><span>渠道 {selectedToken.allowed_channel_ids?.length ? `${selectedToken.channel_restriction_mode === 'deny' ? '排除' : '只允许'} ${selectedToken.allowed_channel_ids.length} 个` : '不限'}</span><span>额度 {selectedToken.cost_limit_usd ? `${formatMoney(selectedToken.cost_used_usd)} / ${formatMoney(selectedToken.cost_limit_usd)}` : '不限'}</span><a href={`#/tokens?token=${selectedToken.id}`}>管理限制<ArrowRight size={12} /></a></div></details>}
          <div className="connect-choice-heading"><strong>客户端与协议</strong><HelpTip label="客户端协议" text="客户端类型决定后续模型列表与验证请求使用的协议。配置模板仍按所选客户端的官方语法生成，本页不会修改本机文件或安装客户端。" /></div>
          <div className="connect-client-options" role="group" aria-label="客户端">
            {connectionClients.map((entry) => <button key={entry.id} type="button" className={`connect-client${client === entry.id ? ' is-selected' : ''}`} aria-pressed={client === entry.id} disabled={testing} onClick={() => { invalidateTest(); setClient(entry.id); setResult(null) }}><strong>{entry.label}</strong><span>{entry.detail}</span></button>)}
          </div>
          <div className="connect-input-grid">
            <div className="connect-field"><div className="connect-field-label"><label htmlFor="connect-gateway-root">客户端访问地址</label><HelpTip label="客户端访问地址" text="填写客户端设备能访问的网关根地址，也可粘贴以 /v1 或 /v1beta 结尾的地址。浏览器验证始终只请求当前同源网关；外部地址需在客户端设备执行终端验证。" /></div><input id="connect-gateway-root" value={gatewayRoot} onChange={(event) => { setGatewayRoot(event.target.value); setResult(null) }} disabled={testing} placeholder="https://your-gateway.example" spellCheck={false} /></div>
            <label className="connect-field">终端环境<select value={shell} onChange={(event) => setShell(event.target.value as ClientShell)} disabled={testing}><option value="powershell">PowerShell 7+</option><option value="bash">Bash / zsh</option></select></label>
          </div>
          {generated.error && <p className="inline-error" role="alert">{generated.error}</p>}
          <div className="connect-model-controls"><div className="connect-field"><div className="connect-field-label"><label htmlFor="connect-model">此令牌可用的模型</label><HelpTip label="可用模型" text="列表来自当前同源网关，并应用所选令牌的模型、协议与渠道范围。模型可见只说明当前请求可选，不代表上游已通过联测；一次成功验证也只覆盖本次调用。" /></div><select id="connect-model" value={models.includes(model) ? model : ''} onChange={(event) => { invalidateTest(); setModel(event.target.value); setResult(null) }} disabled={!secret || testing || modelsLoading || !models.length}>
            <option value="" disabled>{modelsLoading ? '正在读取网关模型' : secret ? '此协议暂无可用模型' : '先读取令牌'}</option>
            {models.map((name) => <option value={name} key={name}>{name}</option>)}
          </select></div><button className="icon-button icon-button--surface" type="button" aria-label="刷新可用模型" onClick={() => setModelRevision((revision) => revision + 1)} disabled={!secret || testing || modelsLoading}><RefreshCw size={16} className={modelsLoading ? 'spin' : undefined} /></button></div>
          {modelsError && <p className="inline-error" role="alert">{modelsError}</p>}
          {secret && !modelsLoading && !modelsError && !models.length && <p className="connect-warning">当前令牌在此协议下没有可见模型。检查令牌限制、启用的渠道、模型与 URL 协议声明。<a href="#/channels?onboarding=1">检查渠道<ArrowRight size={12} /></a></p>}
          {models.length > 0 && <p className="connect-hint">已读取 {models.length} 个当前协议可见的模型。</p>}
        </section>
        <div className="connect-output">
          <ConfigurationPanel artifacts={generated.artifacts} secret={secret} shell={shell} client={client} />
          <section className="connect-panel connect-probe" aria-labelledby="connect-probe-title">
            <header className="connect-panel-heading"><h2 id="connect-probe-title">验证当前网关</h2><Terminal size={19} aria-hidden="true" /></header>
            <p className="connect-probe-address"><span>本次浏览器联测地址（仅当前同源网关）</span><code>{window.location.origin}{buildGatewayProbePath(protocol, model)}</code></p>
            {differentAddress && <p className="connect-warning">客户端地址与当前网关地址不同。浏览器联测只验证当前网关；请在客户端设备执行“终端验证”，确认填写的地址可达。</p>}
            <p className="connect-billing-warning"><span>验证请求按正常模型调用计费，页面不会自动重发。</span><HelpTip label="验证请求边界" text="只发送一次最多 256 输出 token 的文本模型请求。浏览器只验证当前同源网关，网关按现有路由规则处理故障切换。一次成功仅覆盖本次调用；工具调用、流式和长会话仍需在客户端使用中确认。" /></p>
            <div className="connect-probe-actions"><button className="primary-button" type="button" disabled={!ready || Boolean(generated.error) || testing} onClick={() => void test()}><PlugZap size={15} />{testing ? '正在等待响应' : '发送验证请求'}</button>{testing && <button className="secondary-button" type="button" onClick={() => testSession.current?.controller.abort()}><Square size={13} />停止等待</button>}<a className="text-button" href="#/logs?view=requests">请求历史<ArrowRight size={13} /></a></div>
            {result && <div className={`connect-result connect-result--${result.status}`} role="status" aria-live="polite"><strong>{result.status === 'success' ? '当前网关已返回有效响应' : result.status === 'unknown' ? '请求结果尚未确认' : result.status === 'cancelled' ? '请求未发送' : '验证未通过'}</strong>
              <p>{result.status === 'success' ? '所选令牌、模型与协议已通过本次真实代理调用。客户端工具、会话和流式能力仍需在客户端确认。' : result.status === 'cancelled' ? '在发出请求前已停止等待。' : gatewayFailureMessage(result.reason)}</p>
              {result.text && <code className="connect-response-text">{result.text}</code>}
              <div className="connect-result-meta"><span>{result.httpStatus ? `HTTP ${result.httpStatus} · ` : ''}{(result.elapsedMs / 1000).toFixed(2)} 秒</span>{result.requestId ? <a href={requestDetailHref(result.requestId)}>查看本次请求<ArrowRight size={13} /></a> : <span>未收到请求 ID，可先查看请求历史列表。</span>}</div>
            </div>}
          </section>
        </div>
      </div>
    </div>
    {creating && <TokenForm close={() => setCreating(false)} saved={(token, plain) => {
      if (!mounted.current) return
      setCreating(false)
      const metadata = tokenMetadata(token)
      setTokens((rows) => [metadata, ...rows]); changeToken(token.id)
      if (plain) setSecret(plain)
    }} />}
  </div>
}

function buildGatewayProbePath(protocol: GatewayProtocol, model: string): string {
  return protocol === 'codex' ? '/v1/responses' : protocol === 'anthropic' ? '/v1/messages' : protocol === 'gemini' ? `/v1beta/models/${model ? encodeURIComponent(model) : '{model}'}:generateContent` : '/v1/chat/completions'
}

function ConnectionGuide({ configuration, tokens, configured, verified, loading }: { configuration: ConnectConfigurationInput; tokens: number; configured: boolean; verified: boolean; loading: boolean }) {
  const guide = buildConnectionGuide({ configuration, tokens, configured, verified, loading })
  const configurationSteps = guide.steps.filter((step) => step.scope === 'configuration')
  return <details className="connect-guide" aria-label="接入步骤">
    <summary><span className="connect-guide-summary"><strong>{guide.summary}</strong></span><ChevronDown size={17} aria-hidden="true" /></summary>
    <div className="connect-guide-body">
      {loading ? <LoadingState label="读取接入状态" /> : <>
        <ol aria-label="配置条件">{configurationSteps.map((step, index) => <li key={step.id} className={step.state === 'ready' ? 'is-ready' : ''}><span className="connect-step-number" aria-hidden="true">{step.state === 'ready' ? <Check size={14} /> : step.state === 'unknown' ? '?' : index + 1}</span><div><header><h3>{step.label}</h3><span className="connect-step-status">{step.state === 'ready' ? '已配置' : step.state === 'unknown' ? '未确认' : '待配置'}</span></header><p>{step.detail}</p>{step.links.length > 0 && <div className="connect-step-links">{step.links.map(([label, href]) => <a href={href} key={href}>{label}<ArrowRight size={11} /></a>)}</div>}</div></li>)}</ol>
        <p className="connect-guide-note">前三项是配置前置条件，不代表已经完成真实调用测试。第四项只表示当前客户端配置模板可以生成。</p>
        <section className={`connect-guide-verification${guide.verification.ready ? ' is-ready' : ''}`} aria-labelledby="connect-guide-verification-title"><span className="connect-step-number" aria-hidden="true">{guide.verification.ready ? <Check size={14} /> : <Terminal size={14} />}</span><div><header><h3 id="connect-guide-verification-title">{guide.verification.label}</h3><span className="connect-step-status">{guide.verification.ready ? '本次已通过' : '单独执行'}</span></header><p>{guide.verification.detail}</p></div></section>
      </>}
    </div>
  </details>
}

function ConfigurationPanel({ artifacts, secret, shell, client }: { artifacts: ClientArtifact[]; secret: string; shell: ClientShell; client: ClientKind }) {
  const [kind, setKind] = useState<ClientArtifact['kind']>('config')
  const [visible, setVisible] = useState(false)
  const [notice, setNotice] = useState('')
  const artifact = artifacts.find((entry) => entry.kind === kind)
  useEffect(() => { setVisible(false); setNotice('') }, [secret, client, shell])

  const copy = async () => {
    if (!artifact) return
    try {
      if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(artifact.content)
      else {
        const textarea = document.createElement('textarea')
        try {
          textarea.value = artifact.content; textarea.setAttribute('readonly', 'true'); textarea.style.position = 'fixed'; textarea.style.opacity = '0'
          document.body.appendChild(textarea); textarea.select()
          if (!document.execCommand('copy')) throw new Error('clipboard unavailable')
        } finally { textarea.remove() }
      }
      setNotice(artifact.containsToken ? '已复制完整内容，其中包含下游令牌。' : '已复制内容；令牌请在环境变量页设置。')
    } catch { setNotice('复制失败，请检查剪贴板权限，或下载后手动复制。') }
  }
  const download = () => {
    if (!artifact) return
    const url = URL.createObjectURL(new Blob([artifact.content], { type: 'text/plain;charset=utf-8' }))
    const anchor = document.createElement('a')
    try { anchor.href = url; anchor.download = artifact.fileName; document.body.appendChild(anchor); anchor.click() }
    finally { anchor.remove(); window.setTimeout(() => URL.revokeObjectURL(url), 0) }
    setNotice(`已下载 ${artifact.fileName}${artifact.containsToken ? '，文件包含完整下游令牌。' : '。'}`)
  }
  return <section className="connect-panel connect-config" aria-labelledby="connect-config-title">
    <header className="connect-panel-heading"><h2 id="connect-config-title">复制并合并配置</h2><a className="connect-source" href={connectionClients.find((entry) => entry.id === client)?.source} target="_blank" rel="noreferrer">官方文档<ExternalLink size={12} /></a></header>
    <div className="connect-artifact-tabs" role="group" aria-label="配置内容">{(['config', 'environment', 'verify'] as const).map((value) => <button type="button" key={value} aria-pressed={kind === value} className={kind === value ? 'is-active' : ''} onClick={() => { setKind(value); setNotice('') }}>{value === 'config' ? '配置文件' : value === 'environment' ? '环境变量与启动' : '终端验证'}</button>)}</div>
    {artifact ? <><p className="connect-destination"><code>{artifact.destination}</code>{artifact.containsToken && <span>包含下游令牌</span>}</p><p className="connect-hint">{artifact.instructions}</p><pre className="connect-code" tabIndex={0} aria-label="配置预览"><code>{visible ? artifact.content : maskArtifact(artifact, secret, shell)}</code></pre><div className="connect-code-actions"><label className="checkbox-field"><input type="checkbox" checked={visible} onChange={(event) => setVisible(event.target.checked)} disabled={!artifact.containsToken} />显示完整令牌</label><div><button className="secondary-button" type="button" onClick={() => void copy()}><Copy size={14} />复制内容</button><button className="secondary-button" type="button" onClick={download}><Download size={14} />下载文件</button></div></div></> : <p className="connect-config-empty">先读取一个有效的下游令牌，再选择网关返回的模型，即可生成配置。</p>}
    {notice && <p className="connect-copy-notice" role="status" aria-live="polite">{notice}</p>}
  </section>
}
