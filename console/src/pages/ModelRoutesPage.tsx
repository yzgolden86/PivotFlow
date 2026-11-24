import { useCallback, useEffect, useId, useMemo, useState } from 'react'
import { Activity, ArrowRight, ChevronDown, ChevronUp, Layers3, RefreshCw, Route, Search, Settings2 } from 'lucide-react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { getAuthTokens, getModelRouteDiagnostics, getModelRouteInventory } from '../api'
import HelpTip from '../components/HelpTip'
import useReadSnapshot from '../components/useReadSnapshot'
import type { ChannelRouteDiagnostic, ModelRouteDiagnosticResponse } from '../types'
import {
  attemptStateLabel, boundedRouteText, healthObservationLabel, keyMultiplierLabel, modelInventoryStatus, modelRouteURL,
  parseModelRouteScope, priceObservationLabel, requestHistoryLink, routeProtocols,
  routeWarningLabel, stickyForwardLabel, tokenOptions, validRouteText, weightReferenceLabel,
} from './modelRoutes'
import type { ModelRouteScope } from './modelRoutes'
import { EmptyState, ErrorState, formatMoney, LoadingState, PageHeader, Pagination } from './shared'
import '../modelRoutes.css'

const INVENTORY_SIZE = 50
const CHANNEL_SIZE = 20

export default function ModelRoutesPage() {
  const location = useLocation()
  const navigate = useNavigate()
  const scope = useMemo(() => parseModelRouteScope(location.search), [location.search])
  const scopeKey = modelRouteURL(scope)
  const [modelDraft, setModelDraft] = useState(scope.model)
  const [searchDraft, setSearchDraft] = useState('')
  const [search, setSearch] = useState('')
  const [inventoryPage, setInventoryPage] = useState(1)
  const [inventoryExpanded, setInventoryExpanded] = useState(() => !scope.model.trim())
  const [channelPaging, setChannelPaging] = useState({ key: '', page: 1 })
  const [tokenSearch, setTokenSearch] = useState('')
  const [inputError, setInputError] = useState('')
  const searchErrorId = useId()
  const modelErrorId = useId()
  const tokenSearchErrorId = useId()
  const searchInvalid = !validRouteText(searchDraft, 512)
  const modelTextError = inputError || (modelDraft && !validRouteText(modelDraft, 512) ? '请求模型最多 512 个字符，且不能包含控制字符。' : '')
  const tokenSearchInvalid = !validRouteText(tokenSearch, 512)
  const channelPage = channelPaging.key === scopeKey ? channelPaging.page : 1

  useEffect(() => { setModelDraft(scope.model); setInputError('') }, [scope.model])
  useEffect(() => { if (!scope.model.trim()) setInventoryExpanded(true) }, [scope.model])
  useEffect(() => {
    const timer = window.setTimeout(() => { setSearch(searchDraft.trim()); setInventoryPage(1) }, 250)
    return () => window.clearTimeout(timer)
  }, [searchDraft])

  const readInventory = useCallback((signal: AbortSignal) => getModelRouteInventory({ search, limit: INVENTORY_SIZE, offset: (inventoryPage - 1) * INVENTORY_SIZE }, signal), [inventoryPage, search])
  const inventory = useReadSnapshot(`inventory:${search}:${inventoryPage}`, readInventory, validRouteText(search, 512))
  const readTokens = useCallback((signal: AbortSignal) => getAuthTokens('today', signal), [])
  const tokens = useReadSnapshot('routing-tokens', readTokens)
  const readDiagnostics = useCallback((signal: AbortSignal) => getModelRouteDiagnostics({ model: scope.model, client_protocol: scope.clientProtocol, token_id: scope.tokenId, limit: CHANNEL_SIZE, offset: (channelPage - 1) * CHANNEL_SIZE }, signal), [channelPage, scope.clientProtocol, scope.model, scope.tokenId])
  const diagnostics = useReadSnapshot(`${scopeKey}:${channelPage}`, readDiagnostics, Boolean(scope.model.trim()) && !scope.error)
  const availableTokens = tokenOptions(tokens.data?.tokens || [], tokenSearch, scope.tokenId)
  const tokenMissing = Boolean(scope.tokenId && !availableTokens.some((token) => String(token.id) === scope.tokenId))
  const chooseScope = (patch: Partial<ModelRouteScope>) => {
    setInputError('')
    navigate(modelRouteURL({ ...scope, ...patch, error: '' }))
  }
  const chooseInventoryModel = (model: string) => {
    setInventoryExpanded(false)
    chooseScope({ model })
  }
  const submitModel = (event: React.FormEvent) => {
    event.preventDefault()
    if (!modelDraft.trim() || !validRouteText(modelDraft, 512)) {
      setInputError('请输入 1–512 个字符的请求模型，且不能包含控制字符。')
      return
    }
    setInventoryExpanded(false)
    chooseScope({ model: modelDraft })
  }
  const clearInventorySearch = () => {
    setSearchDraft('')
    setSearch('')
    setInventoryPage(1)
  }
  const refresh = () => { diagnostics.refresh(); inventory.refresh(); tokens.refresh() }
  const channelStart = diagnostics.data?.total ? Math.min((channelPage - 1) * CHANNEL_SIZE + 1, diagnostics.data.total) : 0
  const channelEnd = diagnostics.data?.total ? Math.min(channelPage * CHANNEL_SIZE, diagnostics.data.total) : 0

  return <div className="model-routes-page">
    <PageHeader icon={Route} title="模型路由" actions={<>
      <button className="icon-button icon-button--surface" type="button" onClick={refresh} aria-label="刷新路由快照" title="刷新当前模型与路由观察"><RefreshCw size={17} className={diagnostics.loading ? 'spin' : ''} /></button>
      <Link className="secondary-button" to="/system?section=runtime&group=routing"><Settings2 size={15} />路由设置</Link>
    </>} />
    <div className="model-routes-layout">
      <aside className={`model-route-inventory${inventoryExpanded ? ' is-expanded' : ' is-collapsed'}`} role="region" aria-label="模型清单">
        <header><span><Layers3 size={17} /><strong>对外模型</strong><HelpTip label="对外模型清单" text="统一组使用稳定的对外名称；启用渠道按当前渠道配置覆盖计算。清单反映配置覆盖，不代表某次请求一定成功。" /></span><small>{inventory.data?.total ?? '—'} 项</small></header>
        <div className="model-route-mobile-choice">
          <div><span>当前模型</span><strong>{scope.model || '尚未选择'}</strong></div>
          <button className="secondary-button" type="button" aria-expanded={inventoryExpanded} aria-controls="model-route-inventory-browser" onClick={() => setInventoryExpanded((current) => !current)}>
            {inventoryExpanded ? '收起清单' : scope.model ? '更换模型' : '选择模型'}{inventoryExpanded ? <ChevronUp size={15} /> : <ChevronDown size={15} />}
          </button>
        </div>
        <div id="model-route-inventory-browser" className="model-route-inventory-browser">
          <label className="search-field"><Search size={15} /><input value={searchDraft} onChange={(event) => setSearchDraft(boundedRouteText(event.target.value, 512))} placeholder="搜索模型或完整别名" aria-label="搜索模型或别名" aria-invalid={searchInvalid || undefined} aria-describedby={searchInvalid ? searchErrorId : undefined} /></label>
          {searchInvalid && <p id={searchErrorId} className="model-route-field-error" role="alert">模型搜索最多 512 个字符，且不能包含控制字符。</p>}
          <div className="model-route-inventory-results">
            {searchInvalid ? null : inventory.loading && !inventory.data ? <LoadingState label="正在读取模型清单" /> : inventory.error && !inventory.data ? <ErrorState message={inventory.error} retry={inventory.refresh} /> : <>
              {inventory.stale && <p className="model-route-alert" role="alert">模型清单已过时：{inventory.error}<button className="text-button" type="button" onClick={inventory.refresh}>重试</button></p>}
              {inventory.data && !inventory.data.items.length
                ? <div className="model-route-empty-state">
                  <EmptyState label={search ? `没有匹配“${search}”的模型配置` : '尚无可用模型配置'} />
                  <div className="model-route-empty-action">{search
                    ? <button className="secondary-button" type="button" onClick={clearInventorySearch}>清除搜索</button>
                    : <Link className="secondary-button" to="/channels">前往渠道配置模型</Link>}
                  </div>
                </div>
                : inventory.data && <div className="model-route-models">
                  {inventory.data.items.map((item) => <button type="button" className={`model-route-model${scope.model === item.model ? ' is-selected' : ''}${item.enabled_channels === 0 ? ' is-empty' : ''}`} aria-label={`查看 ${item.model} 的路由`} aria-pressed={scope.model === item.model} onClick={() => chooseInventoryModel(item.model)} key={item.model}>
                    <strong>{item.model}</strong><span>{modelInventoryStatus(item)}</span>
                  </button>)}
                </div>}
            </>}
          </div>
          {!searchInvalid && inventory.data && <Pagination label="模型清单分页" page={inventoryPage} pageSize={INVENTORY_SIZE} total={inventory.data.total} onPage={setInventoryPage} />}
        </div>
      </aside>

      <section className="model-route-workspace" aria-label="模型诊断">
        <div className="model-route-scope">
          <form onSubmit={submitModel} className="model-route-request-form">
            <label><span>请求模型</span><input value={modelDraft} onChange={(event) => { setModelDraft(boundedRouteText(event.target.value, 512)); if (inputError) setInputError('') }} placeholder="从左侧选择，或输入完整请求名" aria-label="请求模型" aria-required="true" aria-invalid={modelTextError ? true : undefined} aria-describedby={modelTextError ? modelErrorId : undefined} spellCheck={false} onBlur={() => { if (modelDraft && !validRouteText(modelDraft, 512)) setInputError('请输入 1–512 个字符的请求模型，且不能包含控制字符。') }} />{modelTextError && <small id={modelErrorId} className="model-route-field-error" role="alert">{modelTextError}</small>}</label>
            <button className="primary-button" type="submit">查看路由<ArrowRight size={15} /></button>
          </form>
          <div className="model-route-scope-options">
            <label><span>客户端协议</span><select value={scope.clientProtocol} aria-label="客户端协议" onChange={(event) => chooseScope({ clientProtocol: event.target.value })}>
              {!routeProtocols.some((item) => item.value === scope.clientProtocol) && <option value={scope.clientProtocol}>无效协议</option>}
              {routeProtocols.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
            </select></label>
            <label className="model-route-token-search"><span>查找令牌</span><input aria-label="搜索路由令牌" value={tokenSearch} onChange={(event) => setTokenSearch(boundedRouteText(event.target.value, 512))} placeholder="名称或 ID" aria-invalid={tokenSearchInvalid || undefined} aria-describedby={tokenSearchInvalid ? tokenSearchErrorId : undefined} />{tokenSearchInvalid && <small id={tokenSearchErrorId} className="model-route-field-error" role="alert">令牌搜索不能包含控制字符。</small>}</label>
            <label><span>观察范围</span><select value={scope.tokenId} aria-label="路由令牌范围" onChange={(event) => chooseScope({ tokenId: event.target.value })}>
              <option value="">全局管理员观察</option>
              {tokenMissing && <option value={scope.tokenId}>所选令牌未载入 · #{scope.tokenId}</option>}
              {availableTokens.map((item) => <option key={item.id} value={String(item.id)}>#{item.id} · {item.description || '未命名令牌'}</option>)}
            </select></label>
          </div>
          {tokens.error && <p className="model-route-alert" role="alert">令牌清单读取失败，当前范围保持。<button className="text-button" type="button" onClick={tokens.refresh}>重试令牌清单</button></p>}
          {tokens.data && availableTokens.length < tokens.data.tokens.length && <p className="model-route-hint">令牌预览最多 50 项，可检索全部 {tokens.data.tokens.length} 项；当前选择始终保留。</p>}
        </div>
        {scope.error && <p className="model-route-alert" role="alert">{scope.error}</p>}
        {!scope.error && (!scope.model.trim() ? <div className="model-route-welcome"><Route size={30} /><h2>从一个请求模型开始</h2><p>从左侧清单或上方输入框选择模型，查看候选渠道、排除原因与已保存的上游尝试。</p></div> : <>
          {diagnostics.loading && !diagnostics.data ? <LoadingState label="正在读取路由快照" /> : diagnostics.error && !diagnostics.data ? <ErrorState message={diagnostics.error} retry={diagnostics.refresh} /> : diagnostics.data && <>
            {diagnostics.stale && <p className="model-route-alert" role="alert">当前快照已过时：{diagnostics.error}<button className="text-button" type="button" onClick={diagnostics.refresh}>重试当前范围</button></p>}
            <ModelRouteSnapshot snapshot={diagnostics.data} scope={scope} chooseModel={(model) => chooseScope({ model })} />
            <section className="model-route-channels" aria-label="相关渠道">
              <header><h2>相关渠道 <small>{diagnostics.data.total}</small></h2><span>{diagnostics.data.total ? `渠道 ${channelStart}–${channelEnd} / ${diagnostics.data.total}` : '共 0 个渠道'}</span></header>
              {diagnostics.data.channels.length ? <div className="model-route-channel-list">{diagnostics.data.channels.map((channel) => <ModelRouteChannelCard channel={channel} key={channel.channel_id} />)}</div> : <EmptyState label="此请求模型没有相关渠道配置" />}
              <Pagination label="相关渠道分页" page={channelPage} pageSize={CHANNEL_SIZE} total={diagnostics.data.total} onPage={(page) => setChannelPaging({ key: scopeKey, page })} />
            </section>
          </>}
        </>)}
      </section>
    </div>
  </div>
}

function ModelRouteSnapshot({ snapshot, scope, chooseModel }: { snapshot: ModelRouteDiagnosticResponse; scope: ModelRouteScope; chooseModel: (name: string) => void }) {
  const history = requestHistoryLink(scope)
  const { policy, token } = snapshot
  return <div className="model-route-snapshot">
    <header className="model-route-snapshot-header"><div><span className="model-route-eyebrow">当前请求</span><h2>{snapshot.model}</h2>{snapshot.canonical_model !== snapshot.model && <p>统一名称 <code>{snapshot.canonical_model}</code></p>}</div><div className="model-route-snapshot-meta"><HelpTip label="路由快照计算" text="权重是同层长期均衡参考；低层仅在故障转移时参与。健康样本来自整个渠道。快照不保证下一次命中，实际 Key、URL、RPM 和并发在请求尝试时再次检查。" /><time dateTime={snapshot.observed_at}>快照 {new Date(snapshot.observed_at).toLocaleTimeString('zh-CN', { hour12: false })}</time></div></header>
    <div className="model-route-pipeline" aria-label="路由求值顺序">
      <div><span>01 · 准入</span><strong>{token.scope === 'global' ? '全局观察' : token.admission === 'allowed' ? '令牌允许' : token.admission === 'blocked' ? '令牌被阻断' : '令牌待确认'}</strong></div>
      <div><span>02 · 候选池</span><strong>{policy.candidate_count} 个候选</strong><small>{policy.pool_mode === 'exact' ? '精确与统一组优先' : policy.pool_mode === 'fuzzy' ? '显式映射与通配兜底' : policy.pool_mode === 'cooldown_fallback' ? '全冷却提前试探' : '没有当前候选'}</small></div>
      <div><span>03 · 排序</span><strong>{policy.route_strategy === 'sticky' ? '粘性轮询' : '均衡轮询'}</strong><small>{policy.health_score_enabled ? '健康评分已启用' : '按基础优先级'}{policy.ttfb_score_enabled ? ' · 首字评分' : ''}</small></div>
      <div><span>04 · 尝试</span><strong>{policy.attemptable_count} 个当前可尝试</strong><small>{policy.attempt_unknown_count} 个状态待确认</small></div>
    </div>
    {!!token.reasons.length && <div className={`model-route-admission${token.admission === 'blocked' ? ' is-blocked' : token.admission === 'unknown' ? ' is-unknown' : ''}`}><strong>令牌准入 · {token.description || `#${token.id}`}</strong><ul>{token.reasons.map((reason, index) => <li key={`${reason.code}:${index}`}>{reason.message}</li>)}</ul></div>}
    {!!snapshot.warnings.length && <ul className="model-route-warnings">{snapshot.warnings.map((code) => <li key={code}>{routeWarningLabel(code)}</li>)}</ul>}
    {snapshot.summary.length > 0 && <ul className="model-route-summary">{snapshot.summary.map((text, index) => <li key={index}>{text}</li>)}</ul>}
    {policy.sticky && <p className="model-route-sticky"><strong>最近成功渠道 · {policy.sticky.channel_name}</strong><span>{stickyForwardLabel(policy.sticky.forward_status)}</span></p>}
    <details className="model-route-group"><summary>当前组名称 · {snapshot.group_name_count} 项{snapshot.group_name_count > snapshot.group_names.length ? `（预览 ${snapshot.group_names.length} 项）` : ''}</summary><div>{snapshot.group_names.map((name) => <button type="button" className="model-route-alias" onClick={() => chooseModel(name)} key={name}>{name}</button>)}</div><p>选择别名会按该原始请求名诊断和查询历史。</p></details>
    <div className="model-route-links"><Link to={history.href}>{history.exact ? '查看请求历史' : '普通请求历史'}<ArrowRight size={14} /></Link><Link to="/system?section=runtime&group=health"><Activity size={14} />健康设置</Link></div>
    <p className="model-route-hint">{history.explanation}</p>
  </div>
}

const stages: Record<string, string> = { admission: '准入', candidate: '候选', attempt: '尝试' }
const urlStates: Record<string, string> = { compatible: '协议兼容', unsupported: '协议不支持', disabled: '已停用', unknown: '能力待确认', cooling: '冷却中' }

function ModelRouteChannelCard({ channel }: { channel: ChannelRouteDiagnostic }) {
  const c = channel
  return <article className={`model-route-channel${c.candidate ? ' is-candidate' : ' is-excluded'}`} data-channel-id={c.channel_id} data-candidate={String(c.candidate)}>
    <header><div><span className="model-route-tier">{c.candidate ? `第 ${c.candidate_position || 1} 层` : '未入候选池'}</span><h3>{c.channel_name}</h3><small>#{c.channel_id}</small></div><span className={`model-route-state model-route-state--${c.attempt_status}`}>{attemptStateLabel(c.attempt_status)}</span></header>
    <div className="model-route-channel-metrics">
      <span>优先级 <strong>{c.base_priority} → {c.effective_priority.toFixed(1)}</strong></span>
      <span>轮询权重 <strong>{c.routing_weight}</strong><small>{weightReferenceLabel(c)}</small></span>
      <span>模型范围内 Key <strong>{c.active_key_count} / {c.model_eligible_key_count}</strong><small>可用 / 启用；渠道启用 {c.enabled_key_count}</small></span>
      <span>协议 URL <strong>{c.compatible_url_count} / {c.url_count}</strong></span>
    </div>
    {c.reasons.length > 0 && <ul className="model-route-reasons">{c.reasons.map((reason, index) => <li className={reason.blocking ? 'is-blocking' : ''} key={`${reason.code}:${index}`}><span>{stages[reason.stage] || '观察'}{reason.blocking ? '阻断' : '说明'}</span>{reason.message}</li>)}</ul>}
    <details className="model-route-observations"><summary>健康、容量、上游身份与费用依据</summary>
      <dl>
        <div><dt>健康样本</dt><dd>{healthObservationLabel(c)}；失败惩罚 {c.failure_penalty.toFixed(1)}</dd></div>
        <div><dt>首字观察</dt><dd>{c.ttfb_sample_count ? `${c.average_ttfb_seconds.toFixed(2)} 秒 · ${c.ttfb_sample_count} 次样本` : '无首字样本'}；惩罚 {c.ttfb_penalty.toFixed(1)}</dd></div>
        <div><dt>请求容量</dt><dd>RPM {c.current_rpm}{c.rpm_limit ? ` / ${c.rpm_limit}` : ' · 不限'}；并发 {c.active_concurrency}{c.max_concurrency ? ` / ${c.max_concurrency}` : ' · 不限'}</dd></div>
        <div><dt>可用时段</dt><dd>{c.available_time_start && c.available_time_end ? `${c.available_time_start}–${c.available_time_end}` : '全天'}</dd></div>
        <div><dt>日额度</dt><dd>{c.cost.daily_used_usd === null ? '用量未知' : formatMoney(c.cost.daily_used_usd)}{c.cost.daily_limit_usd ? ` / ${formatMoney(c.cost.daily_limit_usd)}` : ' · 不限'}</dd></div>
        <div><dt>本地倍率</dt><dd>渠道 {c.cost.channel_multiplier}×；适用 Key {keyMultiplierLabel(c.cost)}</dd></div>
        <div><dt>站点价目</dt><dd>{priceObservationLabel(c.cost.price_status)}</dd></div>
        <div><dt>已保存上游尝试</dt><dd>{c.actual_known ? `${c.actual_requests} 次 · ${(c.actual_share * 100).toFixed(1)}%` : '历史观察不可用'}；本机今日、精确请求名与协议</dd></div>
      </dl>
      <p className="model-route-hint">倍率属于本地费用配置；价目缓存不证明当前模型有适用价格，最终费用需要对账。</p>
      <div className="model-route-upstream"><strong>可能的上游模型 · {c.possible_upstream_model_count}</strong>{c.possible_upstream_models.map((name) => <code key={name}>{name}</code>)}{c.possible_upstream_model_count > c.possible_upstream_models.length && <small>仅展示 {c.possible_upstream_models.length} 项</small>}</div>
      <ul className="model-route-urls">{c.urls.map((entry) => <li key={entry.index}>URL {entry.index} · {urlStates[entry.status] || entry.status}{entry.protocols.length ? ` · ${entry.protocols.join(' / ')}` : ''}{entry.fallback ? ' · 保留重试兜底' : ''}{entry.cooling ? ' · 冷却中' : ''}</li>)}</ul>
      {c.url_count > c.urls.length && <p className="model-route-hint">URL 仅预览 {c.urls.length} / {c.url_count}；兼容计数包含完整集合。</p>}
    </details>
    <footer><Link to={`/channels?focus_channel_id=${c.channel_id}`}>编辑渠道<ArrowRight size={13} /></Link>{c.blocked_stage && <span>阻断阶段：{stages[c.blocked_stage] || c.blocked_stage}</span>}</footer>
  </article>
}
