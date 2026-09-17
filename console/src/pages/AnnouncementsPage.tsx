import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from 'react'
import { Bell, CheckCheck, RefreshCw } from 'lucide-react'
import { getAnnouncements, getSites, markAllAnnouncementsRead, markAnnouncementRead, refreshAnnouncements, waitForSiteTask } from '../api'
import type { Site, SiteAnnouncement } from '../types'
import { EmptyState, ErrorState, formatTime, LoadingState, OperationNotice, PageHeader, Pagination } from './shared'
import { Modal, siteErrorMessage } from './siteShared'

const PAGE_SIZE = 30

// 详情弹窗单独成 chunk：它带的 markdown 栈解压后约 340 KB，只有真正打开某条公告
// 时才用得到。这个 import() **不在** App.tsx 的 pageLoaders 里，所以不会被 600ms
// 的全站预取带上 —— 每次会话因此少下约 111 KB（占原先预取总量的 31%）。
const loadAnnouncementDetail = () => import('./AnnouncementDetail')
const AnnouncementDetail = lazy(loadAnnouncementDetail)

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
    <PageHeader
      icon={Bell}
      title="公告中心"
      tone="blue"
      actions={<><button className="secondary-button" type="button" onClick={() => void readAll()} disabled={!unreadCount}><CheckCheck size={15} />全部已读</button><button className="primary-button" type="button" onClick={() => void refresh()} disabled={refreshing}>{refreshing ? <RefreshCw className="spin" size={15} /> : <RefreshCw size={15} />}{refreshing ? '刷新中' : '刷新公告'}</button></>}
    />
    <section className="compact-summary"><span><strong>{total}</strong>当前公告</span><span><strong>{unreadCount}</strong>未读</span><span><strong>{sites.filter((site) => site.enabled).length}</strong>启用站点</span><span><strong>{items.filter((item) => item.level === 'important' || item.level === 'warning').length}</strong>重要提醒</span></section>
    <div className="filter-bar"><select value={siteFilter} onChange={(event) => { setPage(1); setSiteFilter(Number(event.target.value)) }} aria-label="公告站点"><option value={0}>全部站点</option>{sites.map((site) => <option value={site.id} key={site.id}>{site.name}</option>)}</select><label className="checkbox-field filter-checkbox"><input type="checkbox" checked={unread} onChange={(event) => { setPage(1); setUnread(event.target.checked) }} /><span>只看未读</span></label><span className="filter-count"><Bell size={14} />{total} 条公告</span></div>
    {notice && <OperationNotice tone={noticeTone} onDismiss={() => setNotice('')}>{notice}</OperationNotice>}{error && items.length > 0 && <OperationNotice tone="error">{error}</OperationNotice>}
    {loading ? <LoadingState label="正在加载站点公告" /> : error && !items.length ? <ErrorState message={error} retry={() => void load()} /> : !items.length ? <EmptyState label={unread ? '没有未读公告' : '暂无站点公告'} /> : <div className="announcement-list">{items.map((item) => <AnnouncementRow key={item.id} item={item} siteName={siteMap.get(item.site_id)?.name || `站点 #${item.site_id}`} open={() => void open(item)} warm={() => void loadAnnouncementDetail()} />)}</div>}
    <Pagination page={page} pageSize={PAGE_SIZE} total={total} onPage={setPage} />
    {selected && <Modal title={selected.title || '公告'} close={() => setSelected(null)} wide><Suspense fallback={<LoadingState label="正在加载公告正文" />}><AnnouncementDetail item={selected} site={siteMap.get(selected.site_id)} close={() => setSelected(null)} /></Suspense></Modal>}
  </div>
}

// warm 在悬停/聚焦时先把详情 chunk 拉下来，把「首次打开要等一下」的代价基本抹掉。
function AnnouncementRow({ item, siteName, open, warm }: { item: SiteAnnouncement; siteName: string; open: () => void; warm: () => void }) {
  const preview = (item.content_markdown || '').replace(/[#>*_`\[\]]/g, '').trim()
  return <article className={`announcement-row${item.read_at ? '' : ' announcement-row--unread'}`}><span className={`announcement-level announcement-level--${item.level || 'info'}`} /><div><button className="announcement-open" type="button" onClick={open} onMouseEnter={warm} onFocus={warm}><strong>{item.title || '无标题公告'}</strong><p>{preview || '暂无正文摘要'}</p></button><footer><a className="entity-chip" href={`#/sites?focus_site_id=${item.site_id}`}>{siteName}</a><time>{formatTime(item.upstream_updated_at || item.last_seen_at)}</time>{!item.read_at && <em>未读</em>}</footer></div></article>
}
