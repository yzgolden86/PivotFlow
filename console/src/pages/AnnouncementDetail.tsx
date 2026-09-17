import { ExternalLink } from 'lucide-react'
import ReactMarkdown from 'react-markdown'
import rehypeRaw from 'rehype-raw'
import rehypeSanitize from 'rehype-sanitize'
import remarkBreaks from 'remark-breaks'
import remarkGfm from 'remark-gfm'

import type { Site, SiteAnnouncement } from '../types'
import { resolveAnnouncementContentURL, resolveAnnouncementSourceURL } from './announcementContent'
import { formatTime } from './shared'

// 这个组件刻意单独成文件：它带的 markdown 渲染栈（react-markdown + remark/rehype
// 全家桶）解压后约 340 KB，而只有「打开某条公告」的弹窗才用得到。
//
// 留在 AnnouncementsPage 里就会被并进那个路由 chunk，而控制台会预取**全部**路由
// chunk（App.tsx 的 pageLoaders，为的是首次导航不闪烁），于是每次会话都要下这
// 340 KB —— 哪怕从不打开公告页。拆出来之后它不在 pageLoaders 里，只按需下载。
// 改这里时别把它再 import 回 AnnouncementsPage，否则预取体积会悄悄涨回去。
export default function AnnouncementDetail({ item, site, close }: { item: SiteAnnouncement; site?: Site; close: () => void }) {
  const sourceURL = resolveAnnouncementSourceURL(item.source_url, site)
  return <div className="announcement-detail"><div className="announcement-detail-meta"><a className="entity-chip" href={`#/sites?focus_site_id=${item.site_id}`} onClick={close}>{site?.name || `站点 #${item.site_id}`}</a><time>{formatTime(item.upstream_updated_at || item.last_seen_at)}</time>{sourceURL && <a href={sourceURL} target="_blank" rel="noopener noreferrer">查看原文<ExternalLink size={12} /></a>}</div><div className="announcement-markdown"><ReactMarkdown remarkPlugins={[remarkGfm, remarkBreaks]} rehypePlugins={[rehypeRaw, rehypeSanitize]} components={{ a: ({ href, children }) => { const target = resolveAnnouncementContentURL(href, site); return target ? <a href={target} target="_blank" rel="noopener noreferrer">{children}</a> : <span>{children}</span> } }}>{item.content_markdown || '暂无正文'}</ReactMarkdown></div></div>
}
