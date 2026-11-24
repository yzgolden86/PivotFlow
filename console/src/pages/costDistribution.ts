import type { DashboardUsage } from '../types.ts'

/** The total, slices and legend all use the same complete set of model costs. */
export function modelCostDistribution(source: DashboardUsage[]): { total: number; items: DashboardUsage[] } {
  const total = source.reduce((sum, item) => sum + item.effective_cost, 0)
  const requests = source.reduce((sum, item) => sum + item.requests, 0)
  let items = source.map((item) => ({ ...item }))
  if (items.length > 5) {
    const named = items.filter((item) => item.key !== 'other-models')
    const visible = named.slice(0, 4)
    const remainder = [...named.slice(4), ...items.filter((item) => item.key === 'other-models')]
    const other: DashboardUsage = {
      key: 'other-models', label: '其他模型', requests: 0, success: 0, errors: 0,
      input_tokens: 0, output_tokens: 0, effective_cost: 0, share: 0,
    }
    for (const item of remainder) {
      other.requests += item.requests
      other.success += item.success
      other.errors += item.errors
      other.input_tokens += item.input_tokens
      other.output_tokens += item.output_tokens
      other.effective_cost += item.effective_cost
    }
    items = [...visible, other]
  }
  return {
    total,
    items: items.map((item) => ({ ...item, share: total > 0 ? item.effective_cost / total : requests > 0 ? item.requests / requests : 0 })),
  }
}
