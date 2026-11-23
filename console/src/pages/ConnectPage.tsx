import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ArrowRight, Check, Copy, Download, ExternalLink, KeyRound, PlugZap, Plus, RefreshCw, Square, Terminal } from 'lucide-react'
import { useLocation } from 'react-router-dom'
import { getAuthTokens, getChannels, getSiteInventory, getSiteModels, revealAuthToken } from '../api'
import type { AuthToken } from '../types'
import { fetchGatewayModels, gatewayFailureMessage, probeGateway, type GatewayProbeResult } from '../gatewayClient'
import { canApplyGatewayProbe, invalidateGatewayProbe, requestedTokenSelectionTarget, startGatewayProbe, type GatewayProbeSelection, type GatewayProbeSession } from './connectProbeSelection'
import { TokenForm } from './TokensPage'
import { formatMoney, LoadingState, PageHeader } from './shared'
import { requestDetailHref } from './requestHistory'
import {
  buildClientArtifacts, clientProtocol, connectionClients, maskArtifact, normalizeClientGatewayRoot, tokenMetadata, tokenUnavailableReason,
  type ClientArtifact, type ClientKind, type ClientShell, type GatewayProtocol,
} from './clientConnection'

const EMPTY_ASSETS = { sites: 0, accounts: 0, channels: 0, discovered: false }

export default function ConnectPage() {
  const location = useLocation()
  const requestedToken = Number(new URLSearchParams(location.search).get('token')) || 0
  const [tokens, setTokens] = useState<AuthToken[]>([])
  const [assets, setAssets] = useState(EMPTY_ASSETS)
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
    setLoading(true); setLoadError('')
    const responses = await Promise.allSettled([
      getSiteInventory(signal, { force: true }),
      getChannels({ status: 'enabled', limit: 1000, offset: 0 }, signal, { force: true }),
      getSiteModels({ limit: 2000 }, signal),
      getAuthTokens('today', signal),
    ])
    if (!mounted.current || signal?.aborted || sequence !== loadSequence.current) return
    const [siteResponse, channelResponse, modelResponse, tokenResponse] = responses
    setAssets({
      sites: siteResponse.status === 'fulfilled' ? siteResponse.value.sites.length : 0,
      accounts: siteResponse.status === 'fulfilled' ? siteResponse.value.accounts.length : 0,
      channels: channelResponse.status === 'fulfilled' ? channelResponse.value.count : 0,
      discovered: (modelResponse.status === 'fulfilled' && modelResponse.value.data.some((item) => !item.disabled)) || (channelResponse.status === 'fulfilled' && channelResponse.value.data.some((channel) => channel.models.some((item) => !item.disabled))),
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

  return <div className="workspace-page connect-page">
    <PageHeader icon={PlugZap} title="客户端接入" tone="blue" actions={<button className="icon-button icon-button--surface" type="button" onClick={() => void refresh()} disabled={loading || testing || reading} aria-label="刷新接入状态"><RefreshCw size={17} className={loading ? 'spin' : undefined} /></button>} />
    <p className="connect-intro">从上游来源到首个网关请求，在这里完成接入，并为客户端生成配置。</p>
    {loadError && <div className="inline-error" role="alert">{loadError}</div>}
    <div className="connect-layout">
      <ConnectionGuide assets={assets} tokens={tokens.length} configured={ready && !generated.error} verified={result?.status === 'success'} loading={loading} />
      <div className="connect-workspace">
        <section className="connect-panel" aria-labelledby="connect-selection-title">
          <header className="connect-panel-heading"><div><span className="connect-eyebrow">配置客户端</span><h2 id="connect-selection-title">选择令牌与模型</h2></div><KeyRound size={19} aria-hidden="true" /></header>
          <div className="connect-token-controls">
            <label className="connect-field">下游访问令牌<select value={tokenId} onChange={(event) => changeToken(Number(event.target.value))} disabled={testing || reading || loading} aria-label="下游访问令牌">
              <option value={0} disabled>{tokens.length ? '选择访问令牌' : '尚未创建访问令牌'}</option>
              {tokens.map((token) => <option key={token.id} value={token.id}>{token.description} · #{token.id}</option>)}
            </select></label>
            <button className="secondary-button" type="button" onClick={() => void readSecret()} disabled={!selectedToken || Boolean(unavailable) || reading || testing || loading}>{reading ? <RefreshCw size={15} className="spin" /> : <KeyRound size={15} />}{reading ? '读取中' : secret ? '重新读取令牌' : '读取令牌'}</button>
            <button className="secondary-button" type="button" onClick={() => setCreating(true)} disabled={testing || reading || loading}><Plus size={15} />创建令牌</button>
          </div>
          {selectedToken && <div className="connect-permissions" aria-label="令牌权限与额度"><span>模型 {selectedToken.allowed_models?.length ? `${selectedToken.allowed_models.length} 个` : '不限'}</span><span>渠道 {selectedToken.allowed_channel_ids?.length ? `${selectedToken.channel_restriction_mode === 'deny' ? '排除' : '只允许'} ${selectedToken.allowed_channel_ids.length} 个` : '不限'}</span><span>协议 {selectedToken.allowed_protocols?.length ? selectedToken.allowed_protocols.join(' / ') : '不限'}</span><span>额度 {selectedToken.cost_limit_usd ? `${formatMoney(selectedToken.cost_used_usd)} / ${formatMoney(selectedToken.cost_limit_usd)}` : '不限'}</span><a href={`#/tokens?token=${selectedToken.id}`}>管理限制<ArrowRight size={12} /></a></div>}
          {unavailable && <p className="connect-warning" role="status">{unavailable}</p>}
          {secretError && <p className="inline-error" role="alert">{secretError}</p>}
          {secret && !unavailable && <p className="connect-hint"><Check size={13} aria-hidden="true" />已读取下游令牌，仅用于本页配置与验证。</p>}
          <div className="connect-client-options" role="group" aria-label="客户端">
            {connectionClients.map((entry) => <button key={entry.id} type="button" className={`connect-client${client === entry.id ? ' is-selected' : ''}`} aria-pressed={client === entry.id} disabled={testing} onClick={() => { invalidateTest(); setClient(entry.id); setResult(null) }}><strong>{entry.label}</strong><span>{entry.detail}</span></button>)}
          </div>
          <div className="connect-input-grid">
            <label className="connect-field">客户端访问地址<input value={gatewayRoot} onChange={(event) => { setGatewayRoot(event.target.value); setResult(null) }} disabled={testing} placeholder="https://your-gateway.example" spellCheck={false} aria-describedby="connect-address-hint" /></label>
            <label className="connect-field">终端环境<select value={shell} onChange={(event) => setShell(event.target.value as ClientShell)} disabled={testing}><option value="powershell">PowerShell 7+</option><option value="bash">Bash / zsh</option></select></label>
          </div>
          <p id="connect-address-hint" className="connect-hint">填写客户端能访问的网关根地址；也可粘贴以 /v1 或 /v1beta 结尾的地址。</p>
          {generated.error && <p className="inline-error" role="alert">{generated.error}</p>}
          <div className="connect-model-controls"><label className="connect-field">此令牌可用的模型<select value={models.includes(model) ? model : ''} onChange={(event) => { invalidateTest(); setModel(event.target.value); setResult(null) }} disabled={!secret || testing || modelsLoading || !models.length}>
            <option value="" disabled>{modelsLoading ? '正在读取网关模型' : secret ? '此协议暂无可用模型' : '先读取令牌'}</option>
            {models.map((name) => <option value={name} key={name}>{name}</option>)}
          </select></label><button className="icon-button icon-button--surface" type="button" aria-label="刷新可用模型" onClick={() => setModelRevision((revision) => revision + 1)} disabled={!secret || testing || modelsLoading}><RefreshCw size={16} className={modelsLoading ? 'spin' : undefined} /></button></div>
          {modelsError && <p className="inline-error" role="alert">{modelsError}</p>}
          {secret && !modelsLoading && !modelsError && !models.length && <p className="connect-warning">当前令牌在此协议下没有可见模型。检查令牌限制、启用的渠道、模型与 URL 协议声明。<a href="#/channels?onboarding=1">检查渠道<ArrowRight size={12} /></a></p>}
          {models.length > 0 && <p className="connect-hint">{models.length} 个模型来自当前网关，已应用所选令牌的模型、协议和渠道范围。模型可见不代表上游已通过联测。</p>}
        </section>
        <ConfigurationPanel artifacts={generated.artifacts} secret={secret} shell={shell} client={client} />
        <section className="connect-panel" aria-labelledby="connect-probe-title">
          <header className="connect-panel-heading"><div><span className="connect-eyebrow">首个请求</span><h2 id="connect-probe-title">验证当前网关</h2></div><Terminal size={19} aria-hidden="true" /></header>
          <p className="connect-probe-address"><span>本次浏览器联测地址</span><code>{window.location.origin}{buildGatewayProbePath(protocol, model)}</code></p>
          {differentAddress && <p className="connect-warning">客户端地址与当前网关地址不同。浏览器联测只验证当前网关；请在客户端设备执行“终端验证”，确认填写的地址可达。</p>}
          <p className="connect-hint">显式发送一次最多 256 输出 token 的模型请求，按正常调用计费。页面不会自动重发；网关按现有路由规则处理故障切换。</p>
          <div className="connect-probe-actions"><button className="primary-button" type="button" disabled={!ready || Boolean(generated.error) || testing} onClick={() => void test()}><PlugZap size={15} />{testing ? '正在等待响应' : '发送验证请求'}</button>{testing && <button className="secondary-button" type="button" onClick={() => testSession.current?.controller.abort()}><Square size={13} />停止等待</button>}<a className="text-button" href="#/logs?view=history">请求历史<ArrowRight size={13} /></a></div>
          {result && <div className={`connect-result connect-result--${result.status}`} role="status" aria-live="polite"><strong>{result.status === 'success' ? '当前网关已返回有效响应' : result.status === 'unknown' ? '请求结果尚未确认' : result.status === 'cancelled' ? '请求未发送' : '验证未通过'}</strong>
            <p>{result.status === 'success' ? '所选令牌、模型与协议已通过本次真实代理调用。客户端工具、会话和流式能力仍需在客户端确认。' : result.status === 'cancelled' ? '在发出请求前已停止等待。' : gatewayFailureMessage(result.reason)}</p>
            {result.text && <code className="connect-response-text">{result.text}</code>}
            <div className="connect-result-meta"><span>{result.httpStatus ? `HTTP ${result.httpStatus} · ` : ''}{(result.elapsedMs / 1000).toFixed(2)} 秒</span>{result.requestId ? <a href={requestDetailHref(result.requestId)}>查看本次请求<ArrowRight size={13} /></a> : <span>未收到请求 ID，可先查看请求历史列表。</span>}</div>
          </div>}
        </section>
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

function ConnectionGuide({ assets, tokens, configured, verified, loading }: { assets: typeof EMPTY_ASSETS; tokens: number; configured: boolean; verified: boolean; loading: boolean }) {
  const steps = [
    { title: '添加来源', done: assets.accounts > 0 || assets.channels > 0, detail: assets.accounts ? `${assets.sites} 个站点 · ${assets.accounts} 个账号` : assets.channels ? '已有渠道，可直接配置客户端' : '站点账号、API Key 或 OAuth', links: [['站点与账号', '#/sites?onboarding=1'], ['API Key / OAuth', '#/channels?onboarding=1']] },
    { title: '发现并验证模型', done: assets.discovered, detail: assets.discovered ? '模型已配置，连接仍需测试' : '刷新账号模型，或设置渠道模型', links: [['刷新账号模型', '#/accounts?onboarding=1'], ['模型测试', '#/models?onboarding=1']] },
    { title: '准备路由渠道', done: assets.channels > 0, detail: assets.channels ? `${assets.channels} 个已启用渠道` : '同步站点渠道，或手动添加', links: [['检查与同步渠道', '#/channels?onboarding=1']] },
    { title: '生成客户端配置', done: configured, detail: configured ? '模板已就绪，请合并到客户端' : tokens ? '选择下游令牌、客户端与模型' : '创建客户端专用下游令牌', links: [] },
    { title: '验证首个请求', done: verified, detail: verified ? '当前网关已返回有效响应' : '走真实代理入口，保留请求记录', links: [] },
  ]
  const completed = steps.filter((step) => step.done).length
  return <aside className="connect-guide" aria-label="接入步骤"><header><span className="connect-eyebrow">接入路径</span><strong>{loading ? '正在读取状态' : `${completed} / 5 步已具备条件`}</strong></header>{loading ? <LoadingState label="读取接入状态" /> : <ol>{steps.map((step, index) => <li key={step.title} className={step.done ? 'is-ready' : ''}><span className="connect-step-number" aria-hidden="true">{step.done ? <Check size={14} /> : index + 1}</span><div><h3>{step.title}</h3><p>{step.detail}</p>{step.links.length > 0 && <div className="connect-step-links">{step.links.map(([label, href]) => <a href={href} key={href}>{label}<ArrowRight size={11} /></a>)}</div>}</div></li>)}</ol>}<p className="connect-guide-note">站点资产与渠道条件来自当前配置；只有实际成功的联测才完成最后一步。</p></aside>
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
    <header className="connect-panel-heading"><div><span className="connect-eyebrow">客户端配置</span><h2 id="connect-config-title">复制并合并配置</h2></div><a className="connect-source" href={connectionClients.find((entry) => entry.id === client)?.source} target="_blank" rel="noreferrer">官方文档<ExternalLink size={12} /></a></header>
    <div className="connect-artifact-tabs" role="group" aria-label="配置内容">{(['config', 'environment', 'verify'] as const).map((value) => <button type="button" key={value} aria-pressed={kind === value} className={kind === value ? 'is-active' : ''} onClick={() => { setKind(value); setNotice('') }}>{value === 'config' ? '配置文件' : value === 'environment' ? '环境变量与启动' : '终端验证'}</button>)}</div>
    {artifact ? <><p className="connect-destination"><code>{artifact.destination}</code>{artifact.containsToken && <span>包含下游令牌</span>}</p><p className="connect-hint">{artifact.instructions}</p><pre className="connect-code" tabIndex={0} aria-label="配置预览"><code>{visible ? artifact.content : maskArtifact(artifact, secret, shell)}</code></pre><div className="connect-code-actions"><label className="checkbox-field"><input type="checkbox" checked={visible} onChange={(event) => setVisible(event.target.checked)} disabled={!artifact.containsToken} />显示完整令牌</label><div><button className="secondary-button" type="button" onClick={() => void copy()}><Copy size={14} />复制内容</button><button className="secondary-button" type="button" onClick={download}><Download size={14} />下载文件</button></div></div></> : <p className="connect-config-empty">先读取一个有效的下游令牌，再选择网关返回的模型，即可生成配置。</p>}
    {notice && <p className="connect-copy-notice" role="status" aria-live="polite">{notice}</p>}
  </section>
}
