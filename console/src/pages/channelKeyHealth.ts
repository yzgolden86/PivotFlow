import type { ChannelKeyHealthItem, ChannelKeyHealthSnapshot } from '../types.ts'

export const healthLabels: Record<string, { label: string; tone: string }> = {
  healthy: { label: '正常', tone: 'success' },
  invalid: { label: '认证失败', tone: 'danger' },
  quota_exhausted: { label: '额度不足', tone: 'danger' },
  access_denied: { label: '访问受限', tone: 'warning' },
  rate_limited: { label: '请求限流', tone: 'warning' },
  upstream_error: { label: '请求异常', tone: 'warning' },
}

export function keyHealthLabel(key: ChannelKeyHealthItem) {
  return (key.health.checked_at && healthLabels[key.health.status]) || { label: '未检测', tone: 'muted' }
}

export function keyNeedsAttention(key: ChannelKeyHealthItem): boolean {
  return key.health.checked_at > 0 && key.health.status !== 'healthy'
}

export function matchesKeyFilter(key: ChannelKeyHealthItem, filter: string): boolean {
  if (filter === 'attention') return keyNeedsAttention(key)
  if (filter === 'unknown') return !key.health.checked_at
  if (filter === 'disabled') return key.disabled
  return true
}

// Hybrid storage may briefly return an older observation after a manual test.
// Preserve newer evidence only for the same persistent key ID, never its index.
export function mergeKeyHealth(next: ChannelKeyHealthSnapshot, previous: ChannelKeyHealthSnapshot | null): ChannelKeyHealthSnapshot {
  if (!previous || previous.channel_id !== next.channel_id) return next
  const byId = new Map(previous.keys.map((key) => [key.id, key.health]))
  return { ...next, keys: next.keys.map((key) => {
    const newer = byId.get(key.id)
    return newer && newer.checked_at > key.health.checked_at ? { ...key, health: newer } : key
  }) }
}
