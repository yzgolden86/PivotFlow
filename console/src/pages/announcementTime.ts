import type { SiteAnnouncement } from '../types.ts'

// 公告的两个时间语义完全不同，混用会让「刚发布」和「刚抓到」看起来一样：
//
// - 获取时间（last_seen_at）：本地每次同步刷新，**必然存在**。
// - 发布时间（upstream_created_at）：上游公告自身的时间戳，**可能拿不到**——
//   New API 系的 `/api/notice` 只返回一段纯文本通知，响应里没有时间字段，
//   这类站点的 upstream_created_at 恒为 0。目前只有 Sub2API 的
//   `/api/v1/announcements` 会带 created_at / updated_at。
//
// 所以规则是：列表一律用获取时间；详情优先用发布时间，拿不到才退回获取时间，
// 并且要把「退回了」这件事告诉调用方，好让界面标注清楚，而不是悄悄换个含义。
export type AnnouncementTimeSource = 'published' | 'fetched'

export interface AnnouncementTime {
  at: number
  source: AnnouncementTimeSource
}

// 列表用：公告被我们抓到的时刻。刻意不看 upstream_*，
// 否则上游改过公告之后，列表里这条会「跳」到修改时间上去。
export function announcementFetchedTime(item: Pick<SiteAnnouncement, 'last_seen_at'>): number {
  return item.last_seen_at
}

// 详情用：公告自身的发布时间，拿不到则退回获取时间。
export function announcementDetailTime(
  item: Pick<SiteAnnouncement, 'upstream_created_at' | 'last_seen_at'>,
): AnnouncementTime {
  const published = item.upstream_created_at
  if (typeof published === 'number' && published > 0) return { at: published, source: 'published' }
  return { at: item.last_seen_at, source: 'fetched' }
}
