import { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Activity, FlaskConical, Gauge, Globe2, Route, Search, Settings, Users, X } from 'lucide-react'
import { getChannels, getSiteInventory, getSiteModels } from '../api'
import type { Channel, Site, SiteAccount, SiteAccountModel } from '../types'
import useFocusLayer from './useFocusLayer'

interface SearchEntry {
  id: string
  label: string
  detail: string
  href: string
  kind: 'page' | 'site' | 'account' | 'channel' | 'model' | 'advanced'
  keywords?: string
}

type ResourceSource = 'inventory' | 'channels' | 'models'
const resourceLabels: Record<ResourceSource, string> = { inventory: '站点和账号', channels: '渠道', models: '模型' }

const staticEntries: SearchEntry[] = [
  { id: 'page-dashboard', label: '系统概览', detail: '余额、消耗、模型与客户端分布', href: '#/', kind: 'page', keywords: 'dashboard 首页 概览' },
  { id: 'page-sites', label: '站点管理', detail: '站点地址、平台与关联账号', href: '#/sites', kind: 'page' },
  { id: 'page-accounts', label: '账号管理', detail: '凭证、余额、签到与路由同步', href: '#/accounts', kind: 'page' },
  { id: 'page-checkins', label: '签到中心', detail: '批量签到与结果历史', href: '#/checkins', kind: 'page' },
  { id: 'page-announcements', label: '公告中心', detail: '统一查看站点公告', href: '#/announcements', kind: 'page' },
  { id: 'page-channels', label: '渠道分发', detail: '路由候选、协议与冷却', href: '#/channels', kind: 'page' },
  { id: 'page-routing', label: '模型路由', detail: '统一名称、候选渠道与排除原因', href: '#/routing', kind: 'page', keywords: '模型映射 统一 别名 诊断' },
  { id: 'page-connect', label: '接入配置', detail: '生成客户端配置，并测试一次网关调用', href: '#/connect', kind: 'page', keywords: '客户端接入 新手 onboarding Codex Claude Gemini SDK 配置' },
  { id: 'page-logs', label: '请求日志', detail: '路由结果、响应时延与费用', href: '#/logs', kind: 'page' },
  { id: 'page-stats', label: '数据面板', detail: '趋势、渠道与模型消耗总览', href: '#/stats', kind: 'page' },
  { id: 'page-health', label: '运行健康', detail: '结算、写入、同步、清理与恢复状态', href: '#/health', kind: 'page', keywords: 'health 队列 积压 故障 恢复' },
  { id: 'page-models', label: '模型测试', detail: '站点模型清单、直测与渠道测试', href: '#/models', kind: 'page' },
	{ id: 'page-settings', label: '系统设置', detail: '运行参数与通知', href: '#/system', kind: 'page' },
  { id: 'advanced-oauth', label: 'OAuth 凭证导入', detail: '在渠道页直接导入 Codex 与 Antigravity 凭证', href: '#/channels', kind: 'advanced', keywords: 'codex antigravity 用量' },
  { id: 'advanced-active', label: '活动请求与调试日志', detail: '实时请求与调试快照', href: '#/logs?view=active', kind: 'advanced' },
  { id: 'advanced-settings', label: '冷却规则与系统设置', detail: '路由、冷却、日志与运行参数', href: '#/system', kind: 'advanced' },
  { id: 'advanced-token', label: '令牌管理', detail: '访问令牌、限额与用量', href: '#/tokens', kind: 'advanced' },
  { id: 'advanced-account-import', label: '导入账号', detail: 'All API Hub 账号文件或浏览器页面助手', href: '#/browser-import', kind: 'advanced', keywords: '浏览器导入 插件账号' },
  { id: 'advanced-backup', label: '配置备份与恢复', detail: 'PivotFlow 配置文件与 WebDAV', href: '#/system?section=backup', kind: 'advanced', keywords: '导入导出 迁移 备份' },

]

export default function GlobalSearch() {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(false)
  const [resourceErrors, setResourceErrors] = useState<Partial<Record<ResourceSource, string>>>({})
  const [resourceLimits, setResourceLimits] = useState<Partial<Record<ResourceSource, { loaded: number; total: number }>>>({})
  const [retry, setRetry] = useState(0)
  const [activeId, setActiveId] = useState('')
  const [inventory, setInventory] = useState<{ sites: Site[]; accounts: SiteAccount[]; channels: Channel[]; models: SiteAccountModel[] }>({ sites: [], accounts: [], channels: [], models: [] })
  const loaded = useRef(new Set<ResourceSource>())
  const sequence = useRef(0)
  const dialog = useRef<HTMLElement>(null)
  const input = useRef<HTMLInputElement>(null)
  const listId = useId()
  const resultId = (entry: SearchEntry) => `${listId}-${encodeURIComponent(entry.id)}`
  const close = useCallback(() => {
    sequence.current++
    loaded.current.clear()
    setOpen(false)
    setQuery('')
    setActiveId('')
  }, [])
  useFocusLayer(dialog, { open, modal: true, close, initialFocus: () => input.current })

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setOpen(true)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [])

  useEffect(() => {
    if (!open) return
    const generation = ++sequence.current
    const controller = new AbortController()
    const stale = () => controller.signal.aborted || generation !== sequence.current
    const sources = (['inventory', 'channels', 'models'] as const).filter((source) => !loaded.current.has(source))
    setLoading(sources.length > 0)
    setResourceErrors((current) => Object.fromEntries(Object.entries(current).filter(([source]) => !sources.includes(source as ResourceSource))))
    const success = (source: ResourceSource) => {
      loaded.current.add(source)
      setResourceErrors((current) => { const next = { ...current }; delete next[source]; return next })
    }
    const jobs = sources.map(async (source) => {
      try {
        if (source === 'inventory') {
          const response = await getSiteInventory(controller.signal)
          if (stale()) return
          setInventory((current) => ({ ...current, sites: response.sites, accounts: response.accounts }))
        } else if (source === 'channels') {
          const response = await getChannels({ limit: 1000, offset: 0 }, controller.signal)
          if (stale()) return
          setInventory((current) => ({ ...current, channels: response.data }))
          setResourceLimits((current) => ({ ...current, channels: { loaded: response.data.length, total: response.count } }))
        } else {
          const response = await getSiteModels({}, controller.signal)
          if (stale()) return
          setInventory((current) => ({ ...current, models: response.data }))
          setResourceLimits((current) => ({ ...current, models: { loaded: response.data.length, total: response.count } }))
        }
        success(source)
      } catch {
        if (!stale()) setResourceErrors((current) => ({ ...current, [source]: `${resourceLabels[source]}资源读取失败` }))
      }
    })
    void Promise.allSettled(jobs).then(() => { if (!stale()) setLoading(false) })
    return () => { controller.abort(); if (sequence.current === generation) sequence.current++ }
  }, [open, retry])

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
        return { id: `model-${fact.site_account_id}-${fact.model}`, label: fact.model, detail: `模型 · ${site?.name || '站点'} / ${account?.label || `账号 #${fact.site_account_id}`}`, href: `#/models?account=${fact.site_account_id}&model=${encodeURIComponent(fact.model)}&view=probe`, kind: 'model', keywords: `${fact.route_type} ${fact.source}` }
      }),
    ]
  }, [inventory])

  const matches = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    const candidates = normalized ? entries.filter((entry) => `${entry.label} ${entry.detail} ${entry.keywords || ''}`.toLowerCase().includes(normalized)) : entries.filter((entry) => entry.kind === 'page')
    return candidates
  }, [entries, query])
  const visible = matches.slice(0, 14)
  const active = visible.find((entry) => entry.id === activeId) ?? visible[0]

  useEffect(() => {
    if (open && active) document.getElementById(`${listId}-${encodeURIComponent(active.id)}`)?.scrollIntoView({ block: 'nearest' })
  }, [open, active?.id, listId])

  const select = () => {
    close()
    requestAnimationFrame(() => document.getElementById('main-content')?.focus({ preventScroll: true }))
  }

  return <>
    <button className="global-search-trigger" type="button" onClick={() => setOpen(true)} aria-label="全局搜索" title="全局搜索 · Ctrl/Cmd+K" aria-haspopup="dialog" aria-expanded={open}><Search size={16} /><span>全局搜索</span></button>
    {/* 必须 portal 到 body，不能留在 `.sidebar` 里。`<GlobalSearch />` 挂在 App.tsx 的
        `<aside className="sidebar">` 内部，而 `.sidebar` 有 `backdrop-filter: blur(24px)`
        —— 任何 `backdrop-filter` 不为 none 的元素都会成为后代 `position: fixed` 的包含块。
        于是 `.search-overlay { position: fixed; inset: 0 }` 被关进 216px 宽的侧栏里，
        `.search-dialog { width: min(100%, 650px) }` 实际只算到约 176px，搜索框被压成
        一条竖条。同类的坑本项目已踩过三次（页头 overflow 裁下拉、侧栏 overflow 裁主题
        菜单、页面入场动画关住弹窗），所以规则统一为：**祖先里有 overflow / transform /
        filter / backdrop-filter / animation 的浮层，一律 portal 到 body**。 */}
    {open && createPortal(
      <div className="search-overlay" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) close() }}>
        <section ref={dialog} className="search-dialog" role="dialog" aria-modal="true" aria-label="全局搜索" tabIndex={-1}>
          <header><Search size={18} /><input ref={input} value={query} onChange={(event) => { setQuery(event.target.value); setActiveId('') }} placeholder="搜索页面、站点、账号、渠道或模型" aria-label="全局搜索内容" role="combobox" aria-autocomplete="list" aria-expanded="true" aria-controls={listId} aria-activedescendant={active ? resultId(active) : undefined}
            onKeyDown={(event) => {
              if (event.nativeEvent.isComposing || event.keyCode === 229) return
              if ((event.key === 'ArrowDown' || event.key === 'ArrowUp') && visible.length) {
                event.preventDefault()
                const index = active ? visible.indexOf(active) : -1
                setActiveId(visible[(index + (event.key === 'ArrowDown' ? 1 : -1) + visible.length) % visible.length].id)
              } else if (event.key === 'Enter' && active) {
                event.preventDefault()
                document.getElementById(resultId(active))?.click()
              }
            }} /><button className="icon-button icon-button--surface" type="button" onClick={close} aria-label="关闭全局搜索"><X size={17} /></button></header>
          {Object.keys(resourceErrors).length > 0 && <div className="search-resource-error" role="status"><span>{Object.values(resourceErrors).join('；')}。已读取的资源和页面仍可搜索。</span><button className="text-button" type="button" onClick={() => setRetry((value) => value + 1)} disabled={loading}>重试资源</button></div>}
          {Object.entries(resourceLimits).filter(([, limit]) => limit.loaded < limit.total).map(([source, limit]) => <p className="search-resource-limit" key={source}>{resourceLabels[source as ResourceSource]}清单已加载 {limit.loaded} / {limit.total} 条，可在<a href={source === 'models' ? '#/models' : '#/channels'} onClick={select}>对应页面</a>查看其余资源。</p>)}
          <div className="search-results">
            <div className="search-results-label"><span>{query.trim() ? '搜索结果' : '控制台页面'}</span>{loading && <span><Activity className="spin" size={12} />正在同步资源</span>}</div>
            <div id={listId} role="listbox" aria-label="搜索候选" aria-busy={loading}>
              {!visible.length ? <div className="search-empty">没有找到匹配内容</div> : visible.map((entry) => <a id={resultId(entry)} href={entry.href} className={`search-result${active?.id === entry.id ? ' search-result--active' : ''}`} role="option" aria-selected={active?.id === entry.id} tabIndex={-1} onPointerMove={() => setActiveId(entry.id)} onClick={select} key={entry.id}><span className={`search-result-icon search-result-icon--${entry.kind}`}>{entryIcon(entry.kind)}</span><span><strong>{entry.label}</strong><small>{entry.detail}</small></span></a>)}
            </div>
          </div>
          <footer className="search-result-summary" aria-live="polite"><span>{matches.length > 14 ? `找到 ${matches.length} 项，显示前 14 项；请细化搜索词。` : `${matches.length} 项候选`}</span><span>↑ ↓ 选择 · Enter 进入 · Esc 关闭</span></footer>
        </section>
      </div>,
      document.body,
    )}
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
