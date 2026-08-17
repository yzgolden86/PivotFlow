import { useCallback, useEffect, useMemo, useState } from 'react'
import { Bell, CheckCheck, ExternalLink, RefreshCw } from 'lucide-react'
import ReactMarkdown from 'react-markdown'
import rehypeRaw from 'rehype-raw'
import rehypeSanitize from 'rehype-sanitize'
import remarkBreaks from 'remark-breaks'
import remarkGfm from 'remark-gfm'
import { getAnnouncements, getSites, markAllAnnouncementsRead, markAnnouncementRead, refreshAnnouncements, waitForSiteTask } from '../api'
import type { Site, SiteAnnouncement } from '../types'
import { EmptyState, ErrorState, formatTime, LoadingState, OperationNotice, Pagination } from './shared'
import { Modal, siteErrorMessage } from './siteShared'

const PAGE_SIZE = 30

export default function AnnouncementsPage() {
  const [sites, setSites] = useState<Site[]>([]); const [items, setItems] = useState<SiteAnnouncement[]>([]); const [total, setTotal] = useState(0); const [unreadCount, setUnreadCount] = useState(0)
  const [siteFilter, setSiteFilter] = useState(0); const [unread, setUnread] = useState(false); const [page, setPage] = useState(1); const [loading, setLoading] = useState(true); const [error, setError] = useState(''); const [notice, setNotice] = useState(''); const [noticeTone, setNoticeTone] = useState<'success' | 'warning'>('success'); const [refreshing, setRefreshing] = useState(false); const [selected, setSelected] = useState<SiteAnnouncement | null>(null)
  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true)
    setError('')
    try {
      const [siteResult, announcementResult, unreadResult] = await Promise.allSettled([
        getSites(signal),
        getAnnouncements({ site_id: siteFilter, unread, limit: PAGE_SIZE, offset: (page - 1) * PAGE_SIZE }, signal),
        getAnnouncements({ site_id: siteFilter, unread: true, limit: 1, offset: 0 }, signal),
      ])
      if (announcementResult.status === 'rejected') throw announcementResult.reason
      if (siteResult.status === 'fulfilled') setSites(siteResult.value)
      setItems(announcementResult.value.data)
      setTotal(announcementResult.value.count)
      if (unreadResult.status === 'fulfilled') setUnreadCount(unreadResult.value.count)
    } catch (reason) {
      if (!signal?.aborted) setError(siteErrorMessage(reason))
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [page, siteFilter, unread])
  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort() }, [load])
  const siteMap = useMemo(() => new Map(sites.map((site) => [site.id, site])), [sites])
  const refresh = async () => { setRefreshing(true); setError(''); setNotice(''); setNoticeTone('success'); try { const queued = await refreshAnnouncements(siteFilter); const task = await waitForSiteTask(queued.task_id); if (!['success', 'partial'].includes(task.status)) throw new Error(task.error || task.status); setNotice(task.status === 'partial' ? `公告已刷新，但部分站点失败：${task.error || '请稍后重试'}` : siteFilter ? '站点公告已刷新' : '全部站点公告已刷新'); setNoticeTone(task.status === 'partial' ? 'warning' : 'success'); await load() } catch (reason) { setError(siteErrorMessage(reason)) } finally { setRefreshing(false) } }
  const readAll = async () => { try { await markAllAnnouncementsRead(siteFilter); setNoticeTone('success'); setNotice('公告已全部标记为已读'); await load() } catch (reason) { setError(siteErrorMessage(reason)) } }
  const open = async (item: SiteAnnouncement) => { setSelected(item); if (!item.read_at) { try { await markAnnouncementRead(item.id); setItems((current) => current.map((entry) => entry.id === item.id ? { ...entry, read_at: Date.now() } : entry)); setUnreadCount((value) => Math.max(0, value - 1)) } catch { /* 阅读不因回写失败而中断 */ } } }

  return <div className="workspace-page">
    <header className="page-header"><h1>公告中心</h1><div className="header-controls"><button className="secondary-button" type="button" onClick={() => void readAll()} disabled={!unreadCount}><CheckCheck size={15} />全部已读</button><button className="primary-button" type="button" onClick={() => void refresh()} disabled={refreshing}>{refreshing ? <RefreshCw className="spin" size={15} /> : <RefreshCw size={15} />}{refreshing ? '刷新中' : '刷新公告'}</button></div></header>
    <section className="compact-summary"><span><strong>{total}</strong>当前公告</span><span><strong>{unreadCount}</strong>未读</span><span><strong>{sites.filter((site) => site.enabled).length}</strong>启用站点</span><span><strong>{items.filter((item) => item.level === 'important' || item.level === 'warning').length}</strong>重要提醒</span></section>
    <div className="filter-bar"><select value={siteFilter} onChange={(event) => { setPage(1); setSiteFilter(Number(event.target.value)) }} aria-label="公告站点"><option value={0}>全部站点</option>{sites.map((site) => <option value={site.id} key={site.id}>{site.name}</option>)}</select><label className="checkbox-field filter-checkbox"><input type="checkbox" checked={unread} onChange={(event) => { setPage(1); setUnread(event.target.checked) }} /><span>只看未读</span></label><span className="filter-count"><Bell size={14} />{total} 条公告</span></div>
    {notice && <OperationNotice tone={noticeTone} onDismiss={() => setNotice('')}>{notice}</OperationNotice>}{error && items.length > 0 && <OperationNotice tone="error">{error}</OperationNotice>}
    {loading ? <LoadingState label="正在加载站点公告" /> : error && !items.length ? <ErrorState message={error} retry={() => void load()} /> : !items.length ? <EmptyState label={unread ? '没有未读公告' : '暂无站点公告'} /> : <div className="announcement-list">{items.map((item) => <AnnouncementRow key={item.id} item={item} siteName={siteMap.get(item.site_id)?.name || `站点 #${item.site_id}`} open={() => void open(item)} />)}</div>}
    <Pagination page={page} pageSize={PAGE_SIZE} total={total} onPage={setPage} />
    {selected && <Modal title={selected.title || '公告'} close={() => setSelected(null)} wide><AnnouncementDetail item={selected} site={siteMap.get(selected.site_id)} close={() => setSelected(null)} /></Modal>}
  </div>
}

function AnnouncementDetail({ item, site, close }: { item: SiteAnnouncement; site?: Site; close: () => void }) {
  const sourceURL = resolveAnnouncementSourceURL(item.source_url, site)
  return <div className="announcement-detail"><div className="announcement-detail-meta"><a className="entity-chip" href={`#/sites?focus_site_id=${item.site_id}`} onClick={close}>{site?.name || `站点 #${item.site_id}`}</a><time>{formatTime(item.upstream_updated_at || item.last_seen_at)}</time>{sourceURL && <a href={sourceURL} target="_blank" rel="noopener noreferrer">查看原文<ExternalLink size={12} /></a>}</div><div className="announcement-markdown"><ReactMarkdown remarkPlugins={[remarkGfm, remarkBreaks]} rehypePlugins={[rehypeRaw, rehypeSanitize]} components={{ a: ({ href, children }) => { const target = resolveAnnouncementContentURL(href, site); return target ? <a href={target} target="_blank" rel="noopener noreferrer">{children}</a> : <span>{children}</span> } }}>{item.content_markdown || '暂无正文'}</ReactMarkdown></div></div>
}

function resolveAnnouncementSourceURL(sourceURL: string | undefined, site?: Site): string | null {
  const raw = sourceURL?.trim()
  if (!raw) return null
  try {
    const base = site?.base_url?.trim()
    if (base && /^\/api\//i.test(raw)) return base.replace(/\/+$/, '')
    const url = base ? new URL(raw, `${base.replace(/\/+$/, '')}/`) : new URL(raw)
    return ['http:', 'https:'].includes(url.protocol) ? url.toString() : null
  } catch {
    return null
  }
}

function resolveAnnouncementContentURL(sourceURL: string | undefined, site?: Site): string | null {
  const raw = sourceURL?.trim()
  if (!raw) return null
  try {
    const base = site?.base_url?.trim()
    const url = base ? new URL(raw, `${base.replace(/\/+$/, '')}/`) : new URL(raw)
    return ['http:', 'https:', 'mailto:'].includes(url.protocol) ? url.toString() : null
  } catch {
    return null
  }
}

function AnnouncementRow({ item, siteName, open }: { item: SiteAnnouncement; siteName: string; open: () => void }) {
  const preview = (item.content_markdown || '').replace(/[#>*_`\[\]]/g, '').trim()
  return <article className={`announcement-row${item.read_at ? '' : ' announcement-row--unread'}`}><span className={`announcement-level announcement-level--${item.level || 'info'}`} /><div><button className="announcement-open" type="button" onClick={open}><strong>{item.title || '无标题公告'}</strong><p>{preview || '暂无正文摘要'}</p></button><footer><a className="entity-chip" href={`#/sites?focus_site_id=${item.site_id}`}>{siteName}</a><time>{formatTime(item.upstream_updated_at || item.last_seen_at)}</time>{!item.read_at && <em>未读</em>}</footer></div></article>
}
