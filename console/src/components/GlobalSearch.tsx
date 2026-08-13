import { useEffect, useMemo, useRef, useState } from 'react'
import { Activity, FlaskConical, Gauge, Globe2, Route, Search, Settings, Users, X } from 'lucide-react'
import { getChannels, getSiteInventory, getSiteModels } from '../api'
import type { Channel, Site, SiteAccount, SiteAccountModel } from '../types'

interface SearchEntry {
  id: string
  label: string
  detail: string
  href: string
  kind: 'page' | 'site' | 'account' | 'channel' | 'model' | 'advanced'
  keywords?: string
}

const staticEntries: SearchEntry[] = [
  { id: 'page-dashboard', label: '概览', detail: '余额、消耗、模型与客户端分布', href: '#/', kind: 'page', keywords: 'dashboard 首页' },
  { id: 'page-sites', label: '站点管理', detail: '站点地址、平台与关联账号', href: '#/sites', kind: 'page' },
  { id: 'page-accounts', label: '账号管理', detail: '凭证、余额、签到与路由同步', href: '#/accounts', kind: 'page' },
  { id: 'page-checkins', label: '签到中心', detail: '批量签到与结果历史', href: '#/checkins', kind: 'page' },
  { id: 'page-announcements', label: '公告中心', detail: '统一查看站点公告', href: '#/announcements', kind: 'page' },
  { id: 'page-channels', label: '渠道与分发', detail: 'CCLoad 路由候选、协议与冷却', href: '#/channels', kind: 'page' },
  { id: 'page-logs', label: '请求日志', detail: '路由结果、响应时延与费用', href: '#/logs', kind: 'page' },
  { id: 'page-stats', label: '用量统计', detail: '按渠道与模型核对消耗', href: '#/stats', kind: 'page' },
  { id: 'page-models', label: '模型与测试', detail: '站点模型清单、直测与渠道测试', href: '#/models', kind: 'page' },
	{ id: 'page-settings', label: '系统设置', detail: '运行参数与通知', href: '#/system', kind: 'page' },
  { id: 'advanced-oauth', label: 'OAuth 凭证导入', detail: '在渠道页直接导入 Codex 与 Antigravity 凭证', href: '#/channels', kind: 'advanced', keywords: 'codex antigravity 用量' },
  { id: 'advanced-active', label: '活动请求与调试日志', detail: '实时请求与调试快照', href: '#/logs?view=active', kind: 'advanced' },
  { id: 'advanced-settings', label: '冷却规则与系统设置', detail: '路由、冷却、日志与运行参数', href: '#/system', kind: 'advanced' },
  { id: 'advanced-token', label: '下游 API 密钥', detail: '访问令牌、限额与用量', href: '#/tokens', kind: 'advanced' },
  { id: 'advanced-trend', label: '消费趋势', detail: '请求、Token 与费用走势', href: '#/trend', kind: 'advanced' },
]

export default function GlobalSearch() {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(false)
  const [inventory, setInventory] = useState<{ sites: Site[]; accounts: SiteAccount[]; channels: Channel[]; models: SiteAccountModel[] }>({ sites: [], accounts: [], channels: [], models: [] })
  const loaded = useRef(false)

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setOpen(true)
      }
      if (event.key === 'Escape') setOpen(false)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  useEffect(() => {
    if (!open || loaded.current) return
    loaded.current = true
    setLoading(true)
    const controller = new AbortController()
    void Promise.all([
      getSiteInventory(controller.signal),
      getChannels({ limit: 1000, offset: 0 }, controller.signal),
      getSiteModels({}, controller.signal),
    ]).then(([siteInventory, channelResponse, modelResponse]) => {
      setInventory({ sites: siteInventory.sites, accounts: siteInventory.accounts, channels: channelResponse.data, models: modelResponse.data })
    }).catch(() => {
      loaded.current = false
    }).finally(() => setLoading(false))
    return () => controller.abort()
  }, [open])

  const entries = useMemo(() => {
    const siteMap = new Map(inventory.sites.map((site) => [site.id, site]))
    const accountMap = new Map(inventory.accounts.map((account) => [account.id, account]))
    return [
      ...staticEntries,
      ...inventory.sites.map((site): SearchEntry => ({ id: `site-${site.id}`, label: site.name, detail: `站点 · ${site.base_url}`, href: `#/sites?focus_site_id=${site.id}`, kind: 'site', keywords: `${site.platform} ${site.base_url}` })),
	  ...inventory.accounts.map((account): SearchEntry => ({ id: `account-${account.id}`, label: account.label, detail: `账号 · ${siteMap.get(account.site_id)?.name || `站点 #${account.site_id}`}`, href: `#/accounts?focus_account_id=${account.id}${['expired', 'error'].includes(account.status) ? '&open_credential=1' : ''}`, kind: 'account', keywords: account.status })),
      ...inventory.channels.map((channel): SearchEntry => ({ id: `channel-${channel.id}`, label: channel.name, detail: `渠道 · P${channel.priority} · ${channel.models.filter((item) => !item.disabled).length} 模型`, href: `#/channels?search=${encodeURIComponent(channel.name)}`, kind: 'channel', keywords: `${channel.id} ${channel.urls.map((item) => item.url).join(' ')}` })),
      ...inventory.models.map((fact): SearchEntry => {
        const account = accountMap.get(fact.site_account_id)
        const site = siteMap.get(account?.site_id || 0)
        return { id: `model-${fact.site_account_id}-${fact.model}`, label: fact.model, detail: `模型 · ${site?.name || '站点'} / ${account?.label || `账号 #${fact.site_account_id}`}`, href: `#/models?account=${fact.site_account_id}&view=probe`, kind: 'model', keywords: `${fact.route_type} ${fact.source}` }
      }),
    ]
  }, [inventory])

  const visible = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    const candidates = normalized ? entries.filter((entry) => `${entry.label} ${entry.detail} ${entry.keywords || ''}`.toLowerCase().includes(normalized)) : entries.filter((entry) => entry.kind === 'page')
    return candidates.slice(0, 14)
  }, [entries, query])

  const close = () => { setOpen(false); setQuery('') }

  return <>
    <button className="global-search-trigger" type="button" onClick={() => setOpen(true)} aria-label="全局搜索" title="全局搜索"><Search size={16} /><span>全局搜索</span></button>
    {open && <div className="search-overlay" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) close() }}>
      <section className="search-dialog" role="dialog" aria-modal="true" aria-label="全局搜索">
        <header><Search size={18} /><input autoFocus value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索页面、站点、账号、渠道或模型" aria-label="全局搜索内容" /><button className="icon-button icon-button--surface" type="button" onClick={close} aria-label="关闭全局搜索"><X size={17} /></button></header>
        <div className="search-results">
          <div className="search-results-label"><span>{query.trim() ? '搜索结果' : '控制台页面'}</span>{loading && <span><Activity className="spin" size={12} />正在同步资源</span>}</div>
          {!visible.length ? <div className="search-empty">没有找到匹配内容</div> : visible.map((entry) => <a href={entry.href} className="search-result" onClick={close} key={entry.id}><span className={`search-result-icon search-result-icon--${entry.kind}`}>{entryIcon(entry.kind)}</span><span><strong>{entry.label}</strong><small>{entry.detail}</small></span></a>)}
        </div>
      </section>
    </div>}
  </>
}

function entryIcon(kind: SearchEntry['kind']) {
  if (kind === 'site') return <Globe2 size={15} />
  if (kind === 'account') return <Users size={15} />
  if (kind === 'channel') return <Route size={15} />
  if (kind === 'model') return <FlaskConical size={15} />
  if (kind === 'advanced') return <Settings size={15} />
  return <Gauge size={15} />
}
