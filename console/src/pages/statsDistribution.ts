import type { StatsEntry } from '../types.ts'

export type DistributionMode = 'cost' | 'requests'
export interface DistributionItem { key: string; label: string; value: number; share: number; isOther: boolean }

export function statsDistribution(entries: StatsEntry[], dimension: 'channel' | 'model', mode: DistributionMode): { total: number; items: DistributionItem[]; unknownEntries: number } {
  const groups = new Map<string, number>()
  let unknownEntries = 0
  let total = 0
  for (const entry of entries) {
    const value = mode === 'cost' ? entry.effective_cost ?? entry.total_cost : entry.total
    if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) { unknownEntries += 1; continue }
    const label = dimension === 'channel' ? entry.channel_name : entry.model
    groups.set(label, (groups.get(label) ?? 0) + value)
    total += value
  }
  const sorted = Array.from(groups, ([label, value]) => ({ key: `named:${label}`, label, value, share: 0, isOther: false }))
    .sort((a, b) => b.value - a.value)
  let items = sorted
  if (sorted.length > 5) {
    items = [...sorted.slice(0, 4), { key: 'remainder', label: '其他', value: sorted.slice(4).reduce((sum, item) => sum + item.value, 0), share: 0, isOther: true }]
  }
  return { total, unknownEntries, items: items.map((item) => ({ ...item, share: total > 0 ? item.value / total : 0 })) }
}
