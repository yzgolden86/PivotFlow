import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  Boxes,
  CalendarClock,
  CheckCircle2,
  Clock3,
  FlaskConical,
  GitCompareArrows,
  LayoutGrid,
  Play,
  RefreshCw,
  Route,
  Rows3,
  Search,
  Server,
  ShieldCheck,
  XCircle,
} from 'lucide-react'
import { getChannels, getSiteChannelBindings, getSiteInventory, getSiteModels, getSitePricing, testChannel, testSiteAccountModel } from '../api'
import HelpTip from '../components/HelpTip'
import type { Channel, ChannelTestResult, Site, SiteAccount, SiteAccountModel, SiteChannelBinding, SiteModelPrice, SitePricingSnapshot } from '../types'
import { EmptyState, ErrorState, formatMoney, formatNumber, formatTime, LoadingState, OperationNotice, PageHeader } from './shared'
import { useLocation } from 'react-router-dom'

type ModelsView = 'catalog' | 'probe'
type ProbeTarget = 'site_account' | 'channel'
type ProbeResult = ChannelTestResult & { source_label: string; requested_model: string }
type CatalogModel = {
  model: string
  fact?: SiteAccountModel
  channels: Channel[]
}

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
  const [channelBindings, setChannelBindings] = useState<SiteChannelBinding[]>([])
  const [sitePricing, setSitePricing] = useState<Record<number, SitePricingSnapshot>>({})
  const [channelId, setChannelId] = useState(0)
  const [accountId, setAccountId] = useState(0)
  const [model, setModel] = useState('')
  const [protocol, setProtocol] = useState('openai')
  const [content, setContent] = useState('请用一句话说明当前连接正常。')
  const [stream, setStream] = useState(true)
  const [results, setResults] = useState<Partial<Record<ProbeTarget, ProbeResult>>>({})
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [testing, setTesting] = useState(false)
  const [error, setError] = useState('')
  // load 里要读当前选择做失效校验，但不能把它们放进 useCallback 依赖，
  // 否则每次改选择都会重建 load 并触发挂载 effect 重新拉数。
  const selectionRef = useRef<{ channelId: number; accountId: number; target: ProbeTarget }>({ channelId: 0, accountId: 0, target: 'channel' })
  const pricingSequence = useRef(0)

  const loadPricing = useCallback(async (siteIds: number[], signal?: AbortSignal, refresh = false) => {
    const sequence = ++pricingSequence.current
    const uniqueIds = Array.from(new Set(siteIds.filter((id) => id > 0)))
    if (!uniqueIds.length) return
    const results = await Promise.all(uniqueIds.map(async (siteId) => {
      try { return await getSitePricing({ site_id: siteId, refresh }, signal) }
      catch { return null }
    }))
    if (signal?.aborted || sequence !== pricingSequence.current) return
    setSitePricing((current) => {
      const next = { ...current }
      uniqueIds.forEach((siteId, index) => {
        const pricing = results[index]
        if (pricing && !Array.isArray(pricing) && Array.isArray(pricing.models)) {
          next[siteId] = { ...pricing, group_ratio: pricing.group_ratio || {} }
        }
      })
      return next
    })
  }, [])

  // keepSelection：手动刷新只重新取数，不能重放 URL 参数，否则用户当前选好的
  // 渠道/账号/模型会被打回默认值。force：绕过 getChannels(30s) 与
  // getSiteInventory(45s) 的 TTL 缓存，否则「刷新」可能拿回陈旧数据。
  const load = useCallback(async (signal?: AbortSignal, options: { force?: boolean; keepSelection?: boolean } = {}) => {
    setLoading(true)
    setError('')
    try {
      const [channelResponse, inventory, modelResponse, bindingResponse] = await Promise.all([
        getChannels({ limit: 1000, offset: 0 }, signal, { force: options.force }),
        getSiteInventory(signal, { force: options.force }),
        getSiteModels({}, signal),
        getSiteChannelBindings(signal),
      ])
      const availableChannels = channelResponse.data
      const enabledAccounts = inventory.accounts.filter((item) => item.enabled)
      setChannels(availableChannels)
      setSites(inventory.sites)
      setAccounts(enabledAccounts)
      setSiteModels(modelResponse.data)
      setChannelBindings(bindingResponse)

      if (options.keepSelection) {
        // 保留的选择可能已在上游被删除或停用。失效时按 changeChannel /
        // changeAccount 的既有约定退回首个可用项，否则界面会停在一个
        // 既选不动也测不了的空状态。
        // 只处理当前页签对应的那一项：另一项失效与用户正在做的事无关，
        // 动它会把用户已选好的模型无故清掉。
        const kept = selectionRef.current
        const keptChannel = availableChannels.find((item) => item.id === kept.channelId)
        const nextChannel = kept.channelId > 0 && !keptChannel ? availableChannels[0] : keptChannel
        if (kept.channelId > 0 && !keptChannel) setChannelId(nextChannel?.id || 0)
        const accountGone = kept.accountId > 0 && !enabledAccounts.some((item) => item.id === kept.accountId)
        const nextAccountId = accountGone ? 0 : kept.accountId
        if (accountGone) setAccountId(0)
        // 模型同样要校验：它可能被上游从清单里停用或删除。原来的参数重放路径
        // 用 selectedModels.includes(...) 兜底，keepSelection 不能把这层丢掉，
        // 否则下拉显示空白但「开始测试」仍可点，会把已停用的模型发给后端。
        const models = kept.target === 'channel'
          ? nextChannel?.models.filter((item) => !item.disabled).map((item) => item.model) || []
          : modelResponse.data.filter((item) => item.site_account_id === nextAccountId && !item.disabled).map((item) => item.model)
        setModel((current) => models.includes(current) ? current : (models[0] || ''))
      } else {
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
      }
      const siteByAccount = new Map(enabledAccounts.map((account) => [account.id, account.site_id]))
      const pricingSiteIds = [
        ...bindingResponse.map((binding) => siteByAccount.get(binding.site_account_id)),
        ...modelResponse.data.map((fact) => siteByAccount.get(fact.site_account_id)),
      ].filter((siteId): siteId is number => Boolean(siteId))
      void loadPricing(pricingSiteIds, signal, options.force)
    } catch (reason) {
      if (!signal?.aborted) setError(reason instanceof Error ? reason.message : '模型数据加载失败')
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [loadPricing, params])

  useEffect(() => { selectionRef.current = { channelId, accountId, target } }, [channelId, accountId, target])

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

  const openProbe = (item: CatalogModel) => {
    setView('probe')
    if (item.fact) {
      setTarget('site_account')
      setAccountId(item.fact.site_account_id)
      setModel(item.model)
      setProtocol(protocolForRoute(item.fact.route_type))
    } else {
      const channel = item.channels[0]
      setTarget('channel')
      setChannelId(channel?.id || 0)
      setModel(item.model)
      setProtocol('openai')
    }
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
      <PageHeader
        icon={FlaskConical}
        title="模型测试"
        tone="graphite"
        actions={<button className="icon-button icon-button--surface" type="button" disabled={refreshing} onClick={async () => { setRefreshing(true); try { await load(undefined, { force: true, keepSelection: true }) } finally { setRefreshing(false) } }} aria-label="刷新模型数据" title="刷新模型数据"><RefreshCw size={17} className={refreshing ? 'spin' : undefined} /></button>}
      />

      <div className="view-tabs" role="tablist" aria-label="模型视图">
        <button type="button" role="tab" aria-selected={view === 'catalog'} className={view === 'catalog' ? 'is-active' : ''} onClick={() => setView('catalog')}><Boxes size={15} />模型清单</button>
        <button type="button" role="tab" aria-selected={view === 'probe'} className={view === 'probe' ? 'is-active' : ''} onClick={() => setView('probe')}><FlaskConical size={15} />连通测试</button>
      </div>

      {loading ? <LoadingState label="正在汇总站点与渠道模型" /> : error && !channels.length && !accounts.length ? <ErrorState message={error} retry={() => void load()} /> : view === 'catalog' ? (
        <ModelCatalog models={siteModels} sites={sites} accounts={accounts} channels={channels} channelBindings={channelBindings} sitePricing={sitePricing} siteMap={siteMap} accountMap={accountMap} probe={openProbe} />
      ) : (
        <div className="test-layout">
          <section className="test-composer">
            <div className="section-heading"><div><div className="heading-with-hint"><h2>测试请求</h2><HelpTip label="测试请求" text="站点直测不创建渠道，也不改变冷却状态" /></div></div><span className="composer-channel-state"><i className={ready ? 'is-ready' : ''} />{ready ? '测试目标已就绪' : '没有可测试模型'}</span></div>
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
            <div className="section-heading"><div><h2>结果对照</h2></div><GitCompareArrows size={18} /></div>
            {!results.site_account && !results.channel ? <div className="test-result-empty"><Clock3 size={28} /><strong>等待测试</strong><span>分别运行站点直测和渠道测试后即可对照</span></div> : <div className="probe-results">{results.site_account && <TestResult result={results.site_account} />}{results.channel && <TestResult result={results.channel} />}</div>}
          </section>
        </div>
      )}
    </div>
  )
}

function ModelCatalog({ models: siteModels, sites, accounts, channels, channelBindings, sitePricing, siteMap, accountMap, probe }: {
  models: SiteAccountModel[]
  sites: Site[]
  accounts: SiteAccount[]
  channels: Channel[]
  channelBindings: SiteChannelBinding[]
  sitePricing: Record<number, SitePricingSnapshot>
  siteMap: Map<number, Site>
  accountMap: Map<number, SiteAccount>
  probe: (model: CatalogModel) => void
}) {
  const [search, setSearch] = useState('')
  const [siteId, setSiteId] = useState(0)
  const [vendorKey, setVendorKey] = useState('all')
  // The catalog is a current snapshot by default. Historical stale facts stay
  // available through the explicit “过期” filter when a refresh was partial.
  const [status, setStatus] = useState<'all' | 'available' | 'stale' | 'disabled'>('available')
  const [layout, setLayout] = useState<'cards' | 'table'>('cards')
  const catalogModels = useMemo(() => {
    const byModel = new Map<string, CatalogModel>()
    for (const fact of siteModels) {
      byModel.set(fact.model, { model: fact.model, fact, channels: [] })
    }
    for (const channel of channels) {
      if (!channel.enabled) continue
      for (const entry of channel.models) {
        if (entry.disabled) continue
        const current = byModel.get(entry.model) || { model: entry.model, channels: [] }
        current.channels.push(channel)
        byModel.set(entry.model, current)
      }
    }
    return Array.from(byModel.values()).sort((a, b) => a.model.localeCompare(b.model))
  }, [channels, siteModels])
  const baseVisible = useMemo(() => catalogModels.filter((item) => {
    const account = item.fact ? accountMap.get(item.fact.site_account_id) : undefined
    const channelMatchesSite = siteId === 0 || item.channels.some((channel) => {
      const channelAccount = channel.site_account_id ? accountMap.get(channel.site_account_id) : undefined
      return channelAccount?.site_id === siteId
    })
    if (!channelMatchesSite) return false
    if (account && siteId && account.site_id !== siteId) return false
    if (!account && !item.channels.length) return false
    if (status === 'available' && item.fact && !item.channels.length && (item.fact.disabled || item.fact.stale)) return false
    if (status === 'stale' && (!item.fact?.stale || item.fact.disabled)) return false
    if (status === 'disabled' && !item.fact?.disabled) return false
    const site = account ? siteMap.get(account.site_id) : undefined
    const query = search.trim().toLowerCase()
    return !query || [item.model, item.fact?.route_type || '', site?.name || '', account?.label || '', ...item.channels.map((channel) => channel.name)].some((value) => value.toLowerCase().includes(query))
  }), [accountMap, catalogModels, search, siteId, siteMap, status])
  const vendorOptions = useMemo(() => {
    const counts = new Map<string, number>()
    for (const item of baseVisible) {
      const vendor = modelVendor(item.model)
      counts.set(vendor.key, (counts.get(vendor.key) || 0) + 1)
    }
    return Array.from(counts.entries()).map(([key, count]) => ({
      vendor: modelVendors.find((item) => item.key === key) || { key, matches: [], name: key === 'other' ? '其他' : key, mark: 'OT' },
      count,
    })).sort((a, b) => modelVendorOrder(a.vendor.key) - modelVendorOrder(b.vendor.key))
  }, [baseVisible])
  const visible = useMemo(() => baseVisible.filter((item) => {
    if (vendorKey !== 'all' && modelVendor(item.model).key !== vendorKey) return false
    return true
  }), [baseVisible, vendorKey])
  const currentModels = siteModels.filter((item) => !item.disabled && !item.stale)
  const unique = new Set(catalogModels.map((item) => item.model)).size
  const projected = new Set(catalogModels.filter((item) => item.channels.length > 0).map((item) => item.model)).size
  const channelOnly = catalogModels.filter((item) => !item.fact && item.channels.length > 0).length

  return <>
    <section className="compact-summary" aria-label="模型清单摘要"><span><strong>{unique}</strong>可测模型</span><span><strong>{siteModels.length}</strong>账号模型事实</span><span><strong>{projected}</strong>已进入渠道</span><span><strong>{channelOnly}</strong>渠道专属</span></section>
    <div className="filter-bar model-catalog-filter">
      <label className="search-field"><Search size={16} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜索模型、站点、账号或渠道" aria-label="搜索模型清单" /></label>
      <select value={siteId} onChange={(event) => setSiteId(Number(event.target.value))} aria-label="模型站点"><option value={0}>全部来源</option>{sites.map((site) => <option value={site.id} key={site.id}>{site.name}</option>)}</select>
      <div className="model-status-filter" role="group" aria-label="模型状态筛选">{([['all', '全部'], ['available', '可用'], ['stale', '过期'], ['disabled', '停用']] as const).map(([value, label]) => <button type="button" className={status === value ? 'is-active' : ''} onClick={() => setStatus(value)} key={value}>{label}</button>)}</div>
      <div className="model-layout-toggle" role="group" aria-label="模型展示方式">
        <button type="button" className={layout === 'cards' ? 'is-active' : ''} onClick={() => setLayout('cards')} aria-pressed={layout === 'cards'} title="卡片视图"><LayoutGrid size={15} /></button>
        <button type="button" className={layout === 'table' ? 'is-active' : ''} onClick={() => setLayout('table')} aria-pressed={layout === 'table'} title="列表视图"><Rows3 size={15} /></button>
      </div>
      <span className="filter-count">{visible.length} 条结果</span>
    </div>
    <div className="model-vendor-tabs" role="tablist" aria-label="模型供应商">
      <button type="button" role="tab" aria-selected={vendorKey === 'all'} className={`model-vendor-tab${vendorKey === 'all' ? ' is-active' : ''}`} onClick={() => setVendorKey('all')}>
        <span className="model-vendor-tab-mark model-vendor-tab-mark--all" aria-hidden="true"><LayoutGrid size={14} /></span>
        <span>全部</span><b>{baseVisible.length}</b>
      </button>
      {vendorOptions.map(({ vendor, count }) => <button type="button" role="tab" aria-selected={vendorKey === vendor.key} className={`model-vendor-tab${vendorKey === vendor.key ? ' is-active' : ''}`} onClick={() => setVendorKey(vendor.key)} key={vendor.key}>
        <span className={`model-vendor-tab-mark model-vendor--${vendor.key}`} aria-hidden="true"><strong>{vendor.mark}</strong></span>
        <span>{vendor.name}</span><b>{count}</b>
      </button>)}
    </div>
    {!visible.length ? <EmptyState label={catalogModels.length ? '没有符合条件的模型' : '暂无站点模型或启用渠道模型'} /> : layout === 'cards' ? <div className="model-card-grid">{visible.map((item) => <ModelCard key={`${item.fact ? `${item.fact.site_account_id}:` : 'channel:'}${item.model}`} item={item} siteMap={siteMap} accountMap={accountMap} channelBindings={channelBindings} sitePricing={sitePricing} probe={probe} />)}</div> : <div className="records-panel model-records"><div className="record-head model-grid"><span>模型</span><span>站点 / 账号</span><span>路由协议</span><span>来源</span><span>最近发现</span><span>状态 / 操作</span></div>{visible.map((item) => {
      const fact = item.fact
      const account = fact ? accountMap.get(fact.site_account_id) : undefined
      const site = siteMap.get(account?.site_id || 0)
      const channelLabel = item.channels.length > 2 ? `${item.channels[0].name}、${item.channels[1].name} 等 ${item.channels.length} 个渠道` : item.channels.map((channel) => channel.name).join('、') || '尚未投影到渠道'
      const pricing = modelPricingSummary(item, sitePricing, channelBindings, accountMap)
      return <article className="record-row model-grid" key={`${fact ? `${fact.site_account_id}:` : 'channel:'}${item.model}`}><div><strong title={item.model}>{item.model}</strong><span>{item.channels.length ? `${item.channels.length} 个渠道可路由` : '尚未投影到渠道'}</span>{pricing ? <em className="model-price-line" title={pricingSourceTitle(pricing)}>{modelPricingLine(pricing)}</em> : null}</div><div>{fact ? <>{account?.site_id ? <a className="entity-link model-entity-link model-entity-link--site" href={`#/sites?focus_site_id=${account.site_id}`}><strong>{site?.name || `站点 #${account.site_id}`}</strong></a> : <strong>{site?.name || '未知站点'}</strong>}{fact.site_account_id ? <a className="entity-link model-entity-link model-entity-link--account" href={`#/accounts?focus_account_id=${fact.site_account_id}`}><span>{account?.label || `账号 #${fact.site_account_id}`}</span></a> : <span>未知账号</span>}</> : <strong title={channelLabel}>{channelLabel}</strong>}</div><div><strong>{fact ? routeLabel(fact.route_type) : '自动识别'}</strong><span>{fact ? fact.route_type : '按渠道协议转换'}</span></div><div><strong>{fact ? sourceLabel(fact.source) : '渠道配置'}</strong><span>{fact ? fact.source : 'channel_models'}</span></div><div><strong>{fact?.last_seen_at ? formatTime(fact.last_seen_at) : '—'}</strong><span>{fact ? (fact.stale ? '需要重新刷新' : '当前快照') : '渠道当前配置'}</span></div><div className="model-row-action"><span className={`status-badge status-badge--${fact?.disabled ? 'muted' : fact?.stale && !item.channels.length ? 'warning' : 'success'}`}>{fact?.disabled && !item.channels.length ? '停用' : fact?.stale && !item.channels.length ? '过期' : '可用'}</span><button className="icon-button icon-button--surface" type="button" disabled={Boolean(fact?.disabled) && item.channels.length === 0} onClick={() => probe(item)} aria-label={fact ? `直测 ${item.model}` : `渠道测试 ${item.model}`} title={fact ? '站点账号直测' : '渠道路由测试'}><Play size={15} /></button></div></article>
    })}</div>}
  </>
}

function ModelCard({ item, siteMap, accountMap, channelBindings, sitePricing, probe }: {
  item: CatalogModel
  siteMap: Map<number, Site>
  accountMap: Map<number, SiteAccount>
  channelBindings: SiteChannelBinding[]
  sitePricing: Record<number, SitePricingSnapshot>
  probe: (model: CatalogModel) => void
}) {
  const fact = item.fact
  const account = fact ? accountMap.get(fact.site_account_id) : undefined
  const site = siteMap.get(account?.site_id || 0)
  const vendor = modelVendor(item.model)
  const disabled = Boolean(fact?.disabled) && item.channels.length === 0
  const stale = Boolean(fact?.stale) && item.channels.length === 0
  const tone = disabled ? 'muted' : stale ? 'warning' : 'success'
  const statusLabel = disabled ? '停用' : stale ? '过期' : '可用'
  const channelLabel = item.channels.length > 2 ? `${item.channels[0].name}、${item.channels[1].name} 等 ${item.channels.length} 个渠道` : item.channels.map((channel) => channel.name).join('、') || '尚未投影到渠道'
  const pricing = modelPricingSummary(item, sitePricing, channelBindings, accountMap)
  return <article className={`model-card model-card--${tone} model-vendor--${vendor.key}`} key={`${fact ? `${fact.site_account_id}:` : 'channel:'}${item.model}`}>
    <header>
      <span className="model-vendor" aria-hidden="true"><strong>{vendor.mark}</strong><small>{vendor.name}</small></span>
      <div><strong title={item.model}>{displayModelName(item.model, vendor)}</strong><code title={item.model}>{item.model}</code></div>
      <button type="button" disabled={disabled} onClick={() => probe(item)} aria-label={fact ? `直测 ${item.model}` : `渠道测试 ${item.model}`} title={fact ? '站点账号直测' : '渠道路由测试'}><Play size={14} /><span>测试</span></button>
    </header>
    <div className="model-card-badges">
      <span className={`status-badge status-badge--${tone}`}><ShieldCheck size={12} />{statusLabel}</span>
      <span className="model-meta-badge"><Route size={12} />{fact ? routeLabel(fact.route_type) : '自动识别'}</span>
      <span className="model-meta-badge">{fact ? sourceLabel(fact.source) : '渠道配置'}</span>
    </div>
    <dl className="model-card-meta">
      <div><dt><Server size={12} />来源</dt><dd>{fact ? <>{account?.site_id ? <a className="entity-link model-entity-link model-entity-link--site" href={`#/sites?focus_site_id=${account.site_id}`}>{site?.name || `站点 #${account.site_id}`}</a> : <>{site?.name || '未知站点'}</>}{fact.site_account_id ? <a className="entity-link model-entity-link model-entity-link--account" href={`#/accounts?focus_account_id=${fact.site_account_id}`}>{account?.label || `账号 #${fact.site_account_id}`}</a> : <span>未知账号</span>}</> : <span title={channelLabel}>{channelLabel}</span>}</dd></div>
      <div><dt><CalendarClock size={12} />发现</dt><dd>{fact?.last_seen_at ? formatTime(fact.last_seen_at) : '渠道当前配置'}</dd></div>
    </dl>
    {pricing ? <div className="model-pricing-strip" title={pricingSourceTitle(pricing)}>
      {pricing.kind === 'call' ? <>
        <span><small>计费</small><strong>按次</strong></span>
        <span><small>价格</small><strong>{formatPriceRange(pricing.perCallPrices)}</strong></span>
        <span><small>来源</small><strong>{pricing.sourceCount}</strong></span>
      </> : pricing.kind === 'mixed' ? <>
        <span><small>输入</small><strong>{formatPriceRange(pricing.inputPrices)}/M</strong></span>
        <span><small>输出</small><strong>{formatPriceRange(pricing.outputPrices)}/M</strong></span>
        <span><small>按次</small><strong>{formatPriceRange(pricing.perCallPrices)}</strong></span>
      </> : <>
        <span><small>输入</small><strong>{formatPriceRange(pricing.inputPrices)}/M</strong></span>
        <span><small>输出</small><strong>{formatPriceRange(pricing.outputPrices)}/M</strong></span>
        <span><small>倍率</small><strong>{formatRatioPair(pricing.modelRatios, pricing.completionRatios)}</strong></span>
      </>}
    </div> : null}
    <footer>
      {item.channels.slice(0, 3).map((channel) => <span key={channel.id}>{channel.name}</span>)}
      {item.channels.length > 3 ? <span>+{item.channels.length - 3}</span> : null}
      {!item.channels.length ? <span>尚未投影到渠道</span> : null}
    </footer>
  </article>
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

type ModelVendor = { key: string; matches: string[]; name: string; mark: string }
const modelVendors: ModelVendor[] = [
  { key: 'openai', matches: ['openai', 'gpt', 'codex', 'o1', 'o3', 'o4'], name: 'OpenAI', mark: 'OA' },
  { key: 'anthropic', matches: ['anthropic', 'claude'], name: 'Claude', mark: 'CL' },
  { key: 'google', matches: ['google', 'gemini'], name: 'Gemini', mark: 'GM' },
  { key: 'zhipu', matches: ['zhipu', 'glm'], name: '智谱 GLM', mark: 'ZP' },
  { key: 'deepseek', matches: ['deepseek'], name: 'DeepSeek', mark: 'DS' },
  { key: 'qwen', matches: ['qwen'], name: 'Qwen', mark: 'QW' },
  { key: 'kimi', matches: ['kimi', 'moonshot'], name: 'Kimi', mark: 'KM' },
  { key: 'xai', matches: ['xai', 'grok'], name: 'xAI', mark: 'XA' },
  { key: 'meta', matches: ['meta', 'llama'], name: 'Llama', mark: 'MA' },
  { key: 'mistral', matches: ['mistral'], name: 'Mistral', mark: 'MI' },
]

type ModelPricingSummary = {
  kind: 'token' | 'call' | 'mixed'
  inputPrices: number[]
  outputPrices: number[]
  modelRatios: number[]
  completionRatios: number[]
  perCallPrices: number[]
  sourceCount: number
}

function modelPricingSummary(
  item: CatalogModel,
  sitePricing: Record<number, SitePricingSnapshot>,
  channelBindings: SiteChannelBinding[],
  accountMap: Map<number, SiteAccount>,
): ModelPricingSummary | null {
  const summary: ModelPricingSummary = {
    kind: 'token',
    inputPrices: [],
    outputPrices: [],
    modelRatios: [],
    completionRatios: [],
    perCallPrices: [],
    sourceCount: 0,
  }
  const bindingByChannel = new Map(channelBindings.filter((binding) => binding.channel_id).map((binding) => [binding.channel_id as number, binding]))
  const candidates: Array<{ siteId: number; group: string }> = []
  for (const channel of item.channels) {
    const binding = bindingByChannel.get(channel.id)
    const account = accountMap.get(channel.site_account_id || binding?.site_account_id || 0)
    if (account?.site_id) candidates.push({ siteId: account.site_id, group: binding?.pricing_group || 'default' })
  }
  if (item.fact) {
    const account = accountMap.get(item.fact.site_account_id)
    if (account?.site_id) candidates.push({ siteId: account.site_id, group: 'default' })
  }

  const seen = new Set<string>()
  let hasTokenPrice = false
  let hasCallPrice = false
  for (const candidate of candidates) {
    const key = `${candidate.siteId}:${candidate.group}`
    if (seen.has(key)) continue
    const snapshot = sitePricing[candidate.siteId]
    const price = findSiteModelPrice(snapshot, item.model)
    if (!snapshot?.available || !price) continue
    if (price.groups.length && !price.groups.includes(candidate.group)) continue
    seen.add(key)
    const groupRatio = snapshot.group_ratio?.[candidate.group] ?? snapshot.group_ratio?.default ?? 1
    if (price.quota_type === 1) {
      hasCallPrice = true
      summary.perCallPrices.push(price.per_call_price * groupRatio)
    } else {
      hasTokenPrice = true
      summary.inputPrices.push(price.input_price * groupRatio)
      summary.outputPrices.push(price.output_price * groupRatio)
      summary.modelRatios.push(price.model_ratio)
      summary.completionRatios.push(price.completion_ratio)
    }
  }
  summary.kind = hasTokenPrice && hasCallPrice ? 'mixed' : hasCallPrice ? 'call' : 'token'
  summary.sourceCount = seen.size
  return summary.sourceCount ? summary : null
}

function findSiteModelPrice(snapshot: SitePricingSnapshot | undefined, model: string): SiteModelPrice | undefined {
  if (!snapshot) return undefined
  const exact = snapshot.models.find((item) => item.model === model)
  if (exact) return exact
  const normalized = model.toLowerCase()
  return snapshot.models.find((item) => item.model.toLowerCase() === normalized)
}

function pricingSourceTitle(pricing: ModelPricingSummary): string {
  return `${pricing.sourceCount} 个上游价目来源，按渠道分组倍率换算`
}

function modelPricingLine(pricing: ModelPricingSummary): string {
  const parts: string[] = []
  if (pricing.inputPrices.length) parts.push(`输入 ${formatPriceRange(pricing.inputPrices)}/M`)
  if (pricing.outputPrices.length) parts.push(`输出 ${formatPriceRange(pricing.outputPrices)}/M`)
  if (pricing.modelRatios.length && pricing.completionRatios.length) parts.push(`倍率 ${formatRatioPair(pricing.modelRatios, pricing.completionRatios)}`)
  if (pricing.perCallPrices.length) parts.push(`按次 ${formatPriceRange(pricing.perCallPrices)}`)
  return parts.join(' · ')
}

function formatPriceRange(values: number[]): string {
  const valid = values.filter((value) => Number.isFinite(value))
  if (!valid.length) return '—'
  const minimum = Math.min(...valid)
  const maximum = Math.max(...valid)
  return minimum === maximum ? formatPrice(minimum) : `${formatPrice(minimum)}–${formatPrice(maximum)}`
}

function formatPrice(value: number): string {
  if (!Number.isFinite(value)) return '—'
  const digits = value >= 1 || value === 0 ? 2 : value >= .01 ? 3 : 4
  return `$${value.toFixed(digits)}`
}

function formatRatioPair(modelRatios: number[], completionRatios: number[]): string {
  const model = formatRatioRange(modelRatios)
  const completion = formatRatioRange(completionRatios)
  return `${model}/${completion}`
}

function formatRatioRange(values: number[]): string {
  const valid = values.filter((value) => Number.isFinite(value))
  if (!valid.length) return '—'
  const minimum = Math.min(...valid)
  const maximum = Math.max(...valid)
  return minimum === maximum ? formatRatioValue(minimum) : `${formatRatioValue(minimum)}–${formatRatioValue(maximum)}`
}

function formatRatioValue(value: number): string {
  if (!Number.isFinite(value)) return '—'
  if (Number.isInteger(value)) return `${value}×`
  return `${value.toFixed(2).replace(/0+$/, '').replace(/\.$/, '')}×`
}

function modelVendor(value: string): ModelVendor {
  const lower = value.toLowerCase()
  const matched = modelVendors.find((vendor) => vendor.matches.some((token) => modelTokenMatches(lower, token)))
  return matched || { key: 'other', matches: [], name: '其他', mark: 'OT' }
}

function modelTokenMatches(value: string, token: string): boolean {
  return new RegExp(`(?:^|[^a-z0-9])${token}(?:$|[^a-z0-9])`).test(value)
}

function modelVendorOrder(key: string): number {
  const index = modelVendors.findIndex((vendor) => vendor.key === key)
  return index === -1 ? modelVendors.length : index
}

function displayModelName(value: string, vendor: ModelVendor): string {
  if (!vendor.matches.length) return value
  const lower = value.toLowerCase()
  const matched = vendor.matches
    .map((token) => ({ token, index: lower.indexOf(token) }))
    .filter((item) => item.index >= 0 && modelTokenMatches(lower, item.token))
    .sort((a, b) => b.index - a.index)[0]?.token || vendor.matches[0]
  const start = lower.indexOf(matched)
  if (start < 0) return value
  const suffix = value.slice(start + matched.length).replace(/^[-_/.]+/, '').replace(/[_-]+/g, ' ').trim()
  if (!suffix) return modelFamilyLabel(matched)
  const detail = suffix.replace(/\b[a-z]/g, (char) => char.toUpperCase())
  return `${modelFamilyLabel(matched)} ${detail}`
}

function modelFamilyLabel(token: string): string {
  const labels: Record<string, string> = {
    openai: 'OpenAI', gpt: 'GPT', codex: 'Codex', o1: 'O1', o3: 'O3', o4: 'O4',
    anthropic: 'Claude', claude: 'Claude', google: 'Gemini', gemini: 'Gemini',
    zhipu: '智谱 GLM', glm: 'GLM', deepseek: 'DeepSeek', qwen: 'Qwen',
    kimi: 'Kimi', moonshot: 'Kimi', xai: 'xAI', grok: 'Grok',
    meta: 'Llama', llama: 'Llama', mistral: 'Mistral',
  }
  return labels[token] || token.replace(/\b[a-z]/g, (char) => char.toUpperCase())
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
