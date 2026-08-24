import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Boxes,
  CheckCircle2,
  Clock3,
  FlaskConical,
  GitCompareArrows,
  Play,
  RefreshCw,
  Search,
  Server,
  XCircle,
} from 'lucide-react'
import { getChannels, getSiteInventory, getSiteModels, testChannel, testSiteAccountModel } from '../api'
import type { Channel, ChannelTestResult, Site, SiteAccount, SiteAccountModel } from '../types'
import { EmptyState, ErrorState, formatMoney, formatNumber, formatTime, LoadingState, OperationNotice } from './shared'
import { useLocation } from 'react-router-dom'

type ModelsView = 'catalog' | 'probe'
type ProbeTarget = 'site_account' | 'channel'
type ProbeResult = ChannelTestResult & { source_label: string; requested_model: string }

const protocols = [
  { value: 'anthropic', label: 'Anthropic' },
  { value: 'openai', label: 'OpenAI' },
  { value: 'codex', label: 'Codex' },
  { value: 'gemini', label: 'Gemini' },
]

export default function ModelTestPage() {
  const location = useLocation()
  const params = useMemo(() => new URLSearchParams(location.search), [location.search])
  const [view, setView] = useState<ModelsView>(() => params.has('channel') || params.has('account') || params.get('view') === 'probe' ? 'probe' : 'catalog')
  const [target, setTarget] = useState<ProbeTarget>(() => params.has('account') ? 'site_account' : 'channel')
  const [channels, setChannels] = useState<Channel[]>([])
  const [sites, setSites] = useState<Site[]>([])
  const [accounts, setAccounts] = useState<SiteAccount[]>([])
  const [siteModels, setSiteModels] = useState<SiteAccountModel[]>([])
  const [channelId, setChannelId] = useState(0)
  const [accountId, setAccountId] = useState(0)
  const [model, setModel] = useState('')
  const [protocol, setProtocol] = useState('openai')
  const [content, setContent] = useState('请用一句话说明当前连接正常。')
  const [stream, setStream] = useState(true)
  const [results, setResults] = useState<Partial<Record<ProbeTarget, ProbeResult>>>({})
  const [loading, setLoading] = useState(true)
  const [testing, setTesting] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true)
    setError('')
    try {
      const [channelResponse, inventory, modelResponse] = await Promise.all([
        getChannels({ limit: 1000, offset: 0 }, signal),
        getSiteInventory(signal),
        getSiteModels({}, signal),
      ])
      const availableChannels = channelResponse.data
      const enabledAccounts = inventory.accounts.filter((item) => item.enabled)
      setChannels(availableChannels)
      setSites(inventory.sites)
      setAccounts(enabledAccounts)
      setSiteModels(modelResponse.data)

      const requestedChannel = Number(params.get('channel'))
      const requestedAccount = Number(params.get('account'))
      const requestedModel = (params.get('model') || '').trim()
      const selectedChannel = availableChannels.find((item) => item.id === requestedChannel) || (!requestedChannel ? availableChannels[0] : undefined)
      const selectedAccount = enabledAccounts.find((item) => item.id === requestedAccount) || enabledAccounts.find((item) => modelResponse.data.some((fact) => fact.site_account_id === item.id && !fact.disabled))
      setChannelId(selectedChannel?.id || (requestedChannel > 0 ? requestedChannel : 0))
      setAccountId(selectedAccount?.id || 0)
      if (params.has('account')) {
        setView('probe')
        setTarget('site_account')
        const selectedModels = modelResponse.data.filter((item) => item.site_account_id === selectedAccount?.id && !item.disabled).map((item) => item.model)
        setModel(requestedModel && selectedModels.includes(requestedModel) ? requestedModel : firstSiteModel(modelResponse.data, selectedAccount?.id || 0))
      } else {
        if (params.has('channel')) {
          setView('probe')
          setTarget('channel')
        }
        const selectedModels = selectedChannel?.models.filter((item) => !item.disabled).map((item) => item.model) || []
        setModel(requestedModel && selectedModels.includes(requestedModel) ? requestedModel : selectedChannel ? firstChannelModel(selectedChannel) : '')
      }
    } catch (reason) {
      if (!signal?.aborted) setError(reason instanceof Error ? reason.message : '模型数据加载失败')
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [params])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  const siteMap = useMemo(() => new Map(sites.map((site) => [site.id, site])), [sites])
  const accountMap = useMemo(() => new Map(accounts.map((account) => [account.id, account])), [accounts])
  const channel = useMemo(() => channels.find((item) => item.id === channelId) || null, [channelId, channels])
  const account = useMemo(() => accounts.find((item) => item.id === accountId) || null, [accountId, accounts])
  const accountModels = useMemo(() => siteModels.filter((item) => item.site_account_id === accountId && !item.disabled), [accountId, siteModels])
  const channelModels = useMemo(() => channel?.models.filter((item) => !item.disabled) || [], [channel])
  const availableAccounts = useMemo(() => accounts.filter((item) => siteModels.some((fact) => fact.site_account_id === item.id && !fact.disabled)), [accounts, siteModels])
  const ready = target === 'channel' ? Boolean(channel && channelModels.length) : Boolean(account && accountModels.length)

  const changeTarget = (next: ProbeTarget) => {
    setTarget(next)
    const options = next === 'channel' ? channelModels.map((item) => item.model) : accountModels.map((item) => item.model)
    if (!options.includes(model)) {
      setModel(options[0] || '')
      setResults({})
    }
  }

  const changeChannel = (nextId: number) => {
    const next = channels.find((item) => item.id === nextId)
    setChannelId(nextId)
    setModel(next ? firstChannelModel(next) : '')
    setResults({})
  }

  const changeAccount = (nextId: number) => {
    setAccountId(nextId)
    setModel(firstSiteModel(siteModels, nextId))
    setResults({})
  }

  const changeModel = (nextModel: string) => {
    setModel(nextModel)
    setResults({})
  }

  const openProbe = (fact: SiteAccountModel) => {
    setView('probe')
    setTarget('site_account')
    setAccountId(fact.site_account_id)
    setModel(fact.model)
    setProtocol(protocolForRoute(fact.route_type))
    setResults({})
  }

  const run = async () => {
    if (!model.trim() || !ready) return
    setTesting(true)
    setError('')
    try {
      const payload = { model: model.trim(), content: content.trim() || 'hi', stream, client_protocol: protocol }
      const raw = target === 'channel'
        ? await testChannel(channelId, payload)
        : await testSiteAccountModel(accountId, payload)
      const sourceLabel = target === 'channel'
        ? channel?.name || (channelId > 0 ? `渠道 #${channelId}` : '尚未选定渠道')
        : `${siteMap.get(account?.site_id || 0)?.name || '站点'} / ${account?.label || `账号 #${accountId}`}`
      const normalized: ProbeResult = {
        ...raw,
        status: raw.status || (raw.success ? 'pass' : statusFromChannelResult(raw)),
        reason: raw.reason || raw.error || (raw.success ? '' : 'probe_failed'),
        source_type: target,
        channel_id: target === 'channel' ? channelId : undefined,
        site_account_id: target === 'site_account' ? accountId : undefined,
        source_label: sourceLabel,
        requested_model: model.trim(),
      }
      setResults((current) => ({ ...current, [target]: normalized }))
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '模型测试请求失败')
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="workspace-page model-test-page">
      <header className="page-header">
        <h1>模型测试</h1>
		<div className="header-controls"><button className="icon-button icon-button--surface" type="button" onClick={() => void load()} aria-label="刷新模型数据" title="刷新模型数据"><RefreshCw size={17} /></button></div>
      </header>

      <div className="view-tabs" role="tablist" aria-label="模型视图">
        <button type="button" role="tab" aria-selected={view === 'catalog'} className={view === 'catalog' ? 'is-active' : ''} onClick={() => setView('catalog')}><Boxes size={15} />模型清单</button>
        <button type="button" role="tab" aria-selected={view === 'probe'} className={view === 'probe' ? 'is-active' : ''} onClick={() => setView('probe')}><FlaskConical size={15} />连通测试</button>
      </div>

      {loading ? <LoadingState label="正在汇总站点与渠道模型" /> : error && !channels.length && !accounts.length ? <ErrorState message={error} retry={() => void load()} /> : view === 'catalog' ? (
        <ModelCatalog models={siteModels} sites={sites} accounts={accounts} channels={channels} siteMap={siteMap} accountMap={accountMap} probe={openProbe} />
      ) : (
        <div className="test-layout">
          <section className="test-composer">
            <div className="section-heading"><div><h2>测试请求</h2><p>站点直测不创建渠道，也不改变冷却状态</p></div><span className="composer-channel-state"><i className={ready ? 'is-ready' : ''} />{ready ? '测试目标已就绪' : '没有可测试模型'}</span></div>
            <div className="probe-target-control" role="group" aria-label="测试目标">
              <button type="button" className={target === 'site_account' ? 'is-active' : ''} onClick={() => changeTarget('site_account')}><Server size={15} />站点账号直测</button>
              <button type="button" className={target === 'channel' ? 'is-active' : ''} onClick={() => changeTarget('channel')}><GitCompareArrows size={15} />路由渠道</button>
            </div>
            <div className="form-grid">
              {target === 'channel' ? (
                <label><span>渠道</span><select value={channelId} onChange={(event) => changeChannel(Number(event.target.value))}>{channels.map((item) => <option value={item.id} key={item.id}>{item.name} · P{item.priority}{item.enabled ? '' : ' · 已停用'}</option>)}</select></label>
              ) : (
                <label><span>站点账号</span><select value={accountId} onChange={(event) => changeAccount(Number(event.target.value))}>{availableAccounts.map((item) => <option value={item.id} key={item.id}>{siteMap.get(item.site_id)?.name || `站点 #${item.site_id}`} · {item.label}</option>)}</select></label>
              )}
              <label><span>模型</span><select value={model} onChange={(event) => changeModel(event.target.value)}>{(target === 'channel' ? channelModels : accountModels).map((item) => <option value={item.model} key={item.model}>{'redirect_model' in item && item.redirect_model && item.redirect_model !== item.model ? `${item.model} → ${item.redirect_model}` : item.model}</option>)}</select></label>
              <label><span>客户端协议</span><select value={protocol} onChange={(event) => setProtocol(event.target.value)}>{protocols.map((item) => <option value={item.value} key={item.value}>{item.label}</option>)}</select></label>
              <label className="toggle-field"><span>流式响应</span><button className={`switch ${stream ? 'switch--on' : ''}`} type="button" role="switch" aria-checked={stream} onClick={() => setStream((value) => !value)}><i /></button></label>
            </div>
            <label className="textarea-field"><span>测试内容</span><textarea rows={6} value={content} onChange={(event) => setContent(event.target.value)} /></label>
            {ready && <RoutePreview target={target} channel={channel} account={account} site={siteMap.get(account?.site_id || 0)} protocol={protocol} />}
            {error && <OperationNotice tone="error">{error}</OperationNotice>}
            <button className="primary-button test-submit" type="button" disabled={!ready || !model || testing} onClick={() => void run()}>{testing ? <RefreshCw className="spin" size={17} /> : <Play size={17} />}{testing ? '正在测试' : '开始测试'}</button>
          </section>
          <section className="test-result-panel">
            <div className="section-heading"><div><h2>结果对照</h2><p>同一展示契约保留最近一次站点与渠道结果</p></div><GitCompareArrows size={18} /></div>
            {!results.site_account && !results.channel ? <div className="test-result-empty"><Clock3 size={28} /><strong>等待测试</strong><span>分别运行站点直测和渠道测试后即可对照</span></div> : <div className="probe-results">{results.site_account && <TestResult result={results.site_account} />}{results.channel && <TestResult result={results.channel} />}</div>}
          </section>
        </div>
      )}
    </div>
  )
}

function ModelCatalog({ models, sites, accounts, channels, siteMap, accountMap, probe }: {
  models: SiteAccountModel[]
  sites: Site[]
  accounts: SiteAccount[]
  channels: Channel[]
  siteMap: Map<number, Site>
  accountMap: Map<number, SiteAccount>
  probe: (model: SiteAccountModel) => void
}) {
  const [search, setSearch] = useState('')
  const [siteId, setSiteId] = useState(0)
  // The catalog is a current snapshot by default. Historical stale facts stay
  // available through the explicit “过期” filter when a refresh was partial.
  const [status, setStatus] = useState<'all' | 'available' | 'stale' | 'disabled'>('available')
  const visible = useMemo(() => models.filter((fact) => {
    const account = accountMap.get(fact.site_account_id)
    if (!account || (siteId && account.site_id !== siteId)) return false
    if (status === 'available' && (fact.disabled || fact.stale)) return false
    if (status === 'stale' && (fact.disabled || !fact.stale)) return false
    if (status === 'disabled' && !fact.disabled) return false
    const site = siteMap.get(account.site_id)
    const query = search.trim().toLowerCase()
    return !query || [fact.model, fact.route_type, site?.name || '', account.label].some((value) => value.toLowerCase().includes(query))
  }), [accountMap, models, search, siteId, siteMap, status])
  const currentModels = models.filter((item) => !item.disabled && !item.stale)
  const unique = new Set(currentModels.map((item) => item.model)).size
  const projected = new Set(currentModels.filter((fact) => channels.some((channel) => channel.models.some((item) => !item.disabled && item.model === fact.model))).map((item) => item.model)).size

  return <>
    <section className="compact-summary" aria-label="模型清单摘要"><span><strong>{unique}</strong>站点模型</span><span><strong>{models.length}</strong>账号模型事实</span><span><strong>{projected}</strong>已进入渠道</span><span><strong>{models.filter((item) => item.stale).length}</strong>待刷新</span></section>
    <div className="filter-bar model-catalog-filter"><label className="search-field"><Search size={16} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索模型、站点或账号" aria-label="搜索站点模型" /></label><select value={siteId} onChange={(event) => setSiteId(Number(event.target.value))} aria-label="模型站点"><option value={0}>全部站点</option>{sites.map((site) => <option value={site.id} key={site.id}>{site.name}</option>)}</select><div className="model-status-filter" role="group" aria-label="模型状态筛选">{([['all', '全部'], ['available', '可用'], ['stale', '过期'], ['disabled', '停用']] as const).map(([value, label]) => <button type="button" className={status === value ? 'is-active' : ''} onClick={() => setStatus(value)} key={value}>{label}</button>)}</div><span className="filter-count">{visible.length} 条结果</span></div>
    {!visible.length ? <EmptyState label={models.length ? '没有符合条件的模型' : '尚未刷新任何站点模型'} /> : <div className="records-panel model-records"><div className="record-head model-grid"><span>模型</span><span>站点 / 账号</span><span>路由协议</span><span>来源</span><span>最近发现</span><span>状态 / 操作</span></div>{visible.map((fact) => {
      const account = accountMap.get(fact.site_account_id)
      const site = siteMap.get(account?.site_id || 0)
      const channelCount = channels.filter((channel) => channel.models.some((item) => !item.disabled && item.model === fact.model)).length
      return <article className="record-row model-grid" key={`${fact.site_account_id}:${fact.model}`}><div><strong title={fact.model}>{fact.model}</strong><span>{channelCount ? `${channelCount} 个渠道可路由` : '尚未投影到渠道'}</span></div><div>{account?.site_id ? <a className="entity-link model-entity-link model-entity-link--site" href={`#/sites?focus_site_id=${account.site_id}`}><strong>{site?.name || `站点 #${account.site_id}`}</strong></a> : <strong>{site?.name || '未知站点'}</strong>}{fact.site_account_id ? <a className="entity-link model-entity-link model-entity-link--account" href={`#/accounts?focus_account_id=${fact.site_account_id}`}><span>{account?.label || `账号 #${fact.site_account_id}`}</span></a> : <span>未知账号</span>}</div><div><strong>{routeLabel(fact.route_type)}</strong><span>{fact.route_type}</span></div><div><strong>{sourceLabel(fact.source)}</strong><span>{fact.source}</span></div><div><strong>{fact.last_seen_at ? formatTime(fact.last_seen_at) : '—'}</strong><span>{fact.stale ? '需要重新刷新' : '当前快照'}</span></div><div className="model-row-action"><span className={`status-badge status-badge--${fact.disabled ? 'muted' : fact.stale ? 'warning' : 'success'}`}>{fact.disabled ? '停用' : fact.stale ? '过期' : '可用'}</span><button className="icon-button icon-button--surface" type="button" disabled={fact.disabled} onClick={() => probe(fact)} aria-label={`直测 ${fact.model}`} title="站点账号直测"><Play size={15} /></button></div></article>
    })}</div>}
  </>
}

function RoutePreview({ target, channel, account, site, protocol }: { target: ProbeTarget; channel: Channel | null; account: SiteAccount | null; site?: Site; protocol: string }) {
  return <div className="route-preview"><span>{target === 'channel' ? '路由测试' : '站点直连'}</span><strong>{target === 'channel' ? channel?.name : `${site?.name || '站点'} / ${account?.label || '账号'}`}</strong><i>→</i><strong>{protocolLabel(protocol)}</strong><i>→</i><code>{target === 'channel' ? channel?.urls[0]?.url || '未配置 URL' : site?.base_url || '未配置站点地址'}</code></div>
}

function TestResult({ result }: { result: ProbeResult }) {
  const tone = probeTone(result.status, result.success)
  const response = result.response_text || result.error || reasonLabel(result.reason) || '上游未返回可展示的正文'
  return <article className={`test-result test-result--${tone}`}>
    <div className="probe-result-source"><span>{result.source_type === 'site_account' ? '站点直测' : '渠道测试'}</span><strong title={result.source_label}>{result.source_label}</strong></div>
    <div className="test-result-status">{result.success ? <CheckCircle2 size={23} /> : <XCircle size={23} />}<div><strong>{statusLabel(result.status, result.success)}</strong><span>{result.status_code ? `HTTP ${result.status_code}` : reasonLabel(result.reason) || '未获得 HTTP 状态'}</span></div></div>
    <div className="test-metrics"><span><small>总耗时</small><strong>{result.duration_ms != null ? `${result.duration_ms} ms` : '—'}</strong></span><span><small>首字节</small><strong>{result.first_byte_duration_ms != null ? `${result.first_byte_duration_ms} ms` : '—'}</strong></span><span><small>Token</small><strong>{formatNumber((result.input_tokens || 0) + (result.output_tokens || 0))}</strong></span><span><small>费用</small><strong>{formatMoney(result.cost_usd)}</strong></span></div>
    <dl className="test-details"><div><dt>请求模型</dt><dd>{result.requested_model}</dd></div><div><dt>实际模型</dt><dd>{result.actual_model || result.requested_model}</dd></div><div><dt>协议链路</dt><dd>{result.client_protocol || '—'} → {result.upstream_protocol || '—'}</dd></div><div><dt>上游 URL</dt><dd title={result.base_url}>{result.base_url || '—'}</dd></div></dl>
    <div className="test-response"><span>响应摘要</span><pre>{response.slice(0, 4000)}</pre></div>
  </article>
}

function firstChannelModel(channel: Channel): string { return channel.models.find((item) => !item.disabled)?.model || '' }
function firstSiteModel(models: SiteAccountModel[], accountId: number): string { return models.find((item) => item.site_account_id === accountId && !item.disabled)?.model || '' }
function protocolLabel(value: string): string { return protocols.find((item) => item.value === value)?.label || value }
function protocolForRoute(route: string): string { if (route === 'anthropic') return 'anthropic'; if (route === 'gemini') return 'gemini'; if (route === 'openai_response') return 'codex'; return 'openai' }
function routeLabel(route: string): string { if (route === 'anthropic') return 'Anthropic'; if (route === 'gemini') return 'Gemini'; if (route === 'openai_response') return 'OpenAI Responses'; if (route === 'openai_chat') return 'OpenAI Chat'; return '自动识别' }
function sourceLabel(source: string): string { if (source === 'models_endpoint') return '站点模型接口'; if (source === 'probe') return '运行探测'; if (source === 'manual') return '手工维护'; return '站点快照' }
function statusFromChannelResult(result: ChannelTestResult): ChannelTestResult['status'] { const reason = (result.error || '').toLowerCase(); if (result.status_code === 404 || reason.includes('not support') || reason.includes('unsupported') || reason.includes('不在此渠道') || reason.includes('模型不存在')) return 'unsupported'; if (!result.status_code || result.status_code === 429 || result.status_code >= 500) return 'inconclusive'; return 'fail' }
function probeTone(status: ChannelTestResult['status'], success: boolean): string { if (success || status === 'pass') return 'success'; if (status === 'inconclusive' || status === 'unsupported') return 'warning'; return 'danger' }
function statusLabel(status: ChannelTestResult['status'], success: boolean): string { if (success || status === 'pass') return '测试通过'; if (status === 'unsupported') return '当前不支持'; if (status === 'inconclusive') return '暂无法判定'; if (status === 'skipped') return '已跳过'; return '测试失败' }
function reasonLabel(reason?: string): string { const labels: Record<string, string> = { model_or_endpoint_unsupported: '模型或接口不受支持', upstream_temporarily_unavailable: '上游暂时不可用', upstream_timeout: '上游响应超时', upstream_request_failed: '上游请求失败', credential_rejected: '站点凭证被拒绝', probe_failed: '探测失败' }; return reason ? labels[reason] || reason : '' }
