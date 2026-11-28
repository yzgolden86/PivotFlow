import type { SiteModelPrice, SitePricingSnapshot } from '../types.ts'
import type { ModelPricingOutcomes } from './modelPricingLoader.ts'

export type ModelPricingSummary = {
  kind: 'token' | 'call' | 'mixed'
  inputPrices: number[]
  outputPrices: number[]
  modelRatios: number[]
  completionRatios: number[]
  perCallPrices: number[]
  sourceCount: number
  sourceSiteIds: number[]
}

export type ModelPricingState = 'loading' | 'ready' | 'stale' | 'failed' | 'unavailable'

export function modelPricingFeedback(
  pricing: ModelPricingSummary | null,
  sources: ReadonlyArray<{ siteId: number }>,
  snapshots: Readonly<Record<number, SitePricingSnapshot>>,
  pendingSites: ReadonlySet<number>,
  outcomes: ModelPricingOutcomes = {},
): { pending: boolean; failed: boolean; stale: boolean; state: ModelPricingState; line: string; description: string } {
  const pending = sources.some((source) => pendingSites.has(source.siteId))
  const failed = sources.some((source) => outcomes[source.siteId] === 'failed')
  const retained = sources.filter((source) => snapshots[source.siteId]?.available
    && (outcomes[source.siteId] === 'failed' || outcomes[source.siteId] === 'unavailable'))
  const stale = retained.some((source) => pricing?.sourceSiteIds.includes(source.siteId))
  const state: ModelPricingState = stale ? 'stale' : pending ? 'loading' : failed ? 'failed' : pricing ? 'ready' : 'unavailable'
  const details: string[] = []
  if (stale) details.push(retained.some((source) => outcomes[source.siteId] === 'failed')
    ? '部分价目更新失败，暂用旧价目' : '部分上游价目暂不可用，暂用旧价目')
  else if (failed) details.push(pricing ? '部分上游价目读取失败，当前仅显示已取得的价目' : '上游价目读取失败')
  if (pricing) details.push(pricingSourceTitle(pricing), modelPricingLine(pricing))
  else if (!failed && !pending) details.push('当前模型暂无可用上游价目')
  if (pending) details.push(pricing ? '其余价目正在加载' : '上游价目加载中，模型清单与连通测试已可使用')
  const line = pricing
    ? `${stale ? '旧价 · ' : failed ? '部分失败 · ' : ''}${modelPricingLine(pricing)}`
    : pending ? '价目加载中' : failed ? '价目读取失败' : '暂无价目'
  return { pending, failed, stale, state, line, description: `${details.join('。')}。` }
}

// All states use the same three cells so asynchronous prices cannot add rows.
export function modelPricingFields(pricing: ModelPricingSummary | null, pending: boolean, state?: ModelPricingState): Array<{ label: string; value: string }> {
  if (!pricing) return [
    { label: '输入', value: pending ? '加载中' : state === 'failed' ? '读取失败' : '暂无价目' },
    { label: '输出', value: '—' },
    { label: '倍率', value: '—' },
  ]
  const suffix = state === 'stale' ? ' · 旧' : state === 'failed' ? ' · 缺' : ''
  if (pricing.kind === 'call') return [
    { label: `计费${suffix}`, value: '按次' },
    { label: '价格', value: formatPriceRange(pricing.perCallPrices) },
    { label: '来源', value: String(pricing.sourceCount) },
  ]
  return [
    { label: `输入${suffix}`, value: `${formatPriceRange(pricing.inputPrices)}/M` },
    { label: '输出', value: `${formatPriceRange(pricing.outputPrices)}/M` },
    pricing.kind === 'mixed'
      ? { label: '按次', value: formatPriceRange(pricing.perCallPrices) }
      : { label: '倍率', value: formatRatioPair(pricing.modelRatios, pricing.completionRatios) },
  ]
}

export function pricingSourceTitle(pricing: ModelPricingSummary): string {
  return `${pricing.sourceCount} 个上游价目来源，按渠道分组倍率换算`
}

export function modelPricingLine(pricing: ModelPricingSummary): string {
  const parts: string[] = []
  if (pricing.inputPrices.length) parts.push(`输入 ${formatPriceRange(pricing.inputPrices)}/M`)
  if (pricing.outputPrices.length) parts.push(`输出 ${formatPriceRange(pricing.outputPrices)}/M`)
  if (pricing.modelRatios.length && pricing.completionRatios.length) parts.push(`倍率 ${formatRatioPair(pricing.modelRatios, pricing.completionRatios)}`)
  if (pricing.perCallPrices.length) parts.push(`按次 ${formatPriceRange(pricing.perCallPrices)}`)
  return parts.join(' · ')
}

function formatPriceRange(values: number[]): string {
  const valid = values.filter((value) => Number.isFinite(value))
  if (!valid.length) return '—'
  const minimum = Math.min(...valid)
  const maximum = Math.max(...valid)
  return minimum === maximum ? formatPrice(minimum) : `${formatPrice(minimum)}–${formatPrice(maximum)}`
}

function formatPrice(value: number): string {
  if (!Number.isFinite(value)) return '—'
  if (value !== 0 && Math.abs(value) < .01) return `$${Number(value.toPrecision(6))}`
  const digits = value >= 1 || value === 0 ? 2 : 3
  return `$${value.toFixed(digits)}`
}

function formatRatioPair(modelRatios: number[], completionRatios: number[]): string {
  return `${formatRatioRange(modelRatios)}/${formatRatioRange(completionRatios)}`
}

function formatRatioRange(values: number[]): string {
  const valid = values.filter((value) => Number.isFinite(value))
  if (!valid.length) return '—'
  const minimum = Math.min(...valid)
  const maximum = Math.max(...valid)
  return minimum === maximum ? formatRatioValue(minimum) : `${formatRatioValue(minimum)}–${formatRatioValue(maximum)}`
}

function formatRatioValue(value: number): string {
  if (!Number.isFinite(value)) return '—'
  if (Number.isInteger(value)) return `${value}×`
  return `${value.toFixed(2).replace(/0+$/, '').replace(/\.$/, '')}×`
}

export function modelPricingSummary(
  model: string,
  candidates: ReadonlyArray<{ siteId: number; group: string }>,
  sitePricing: Record<number, SitePricingSnapshot>,
): ModelPricingSummary | null {
  const summary: ModelPricingSummary = {
    kind: 'token',
    inputPrices: [],
    outputPrices: [],
    modelRatios: [],
    completionRatios: [],
    perCallPrices: [],
    sourceCount: 0,
    sourceSiteIds: [],
  }
  const seen = new Set<string>()
  let hasTokenPrice = false
  let hasCallPrice = false
  for (const candidate of candidates) {
    const key = `${candidate.siteId}:${candidate.group}`
    if (seen.has(key)) continue
    const snapshot = sitePricing[candidate.siteId]
    const price = findSiteModelPrice(snapshot, model)
    if (!snapshot?.available || !price) continue
    if (price.groups.length && !price.groups.includes(candidate.group)) continue
    const groupRatio = snapshot.group_ratio?.[candidate.group] ?? snapshot.group_ratio?.default ?? 1
    if (!Number.isFinite(groupRatio) || groupRatio < 0) continue
    if (price.quota_type === 1) {
      const perCallPrice = price.per_call_price * groupRatio
      if (price.per_call_price_valid === false || !usablePrice(price.per_call_price) || !usablePrice(perCallPrice)) continue
      hasCallPrice = true
      summary.perCallPrices.push(perCallPrice)
    } else {
      const inputPrice = price.input_price * groupRatio
      const outputPrice = price.output_price * groupRatio
      if (![price.input_price, price.output_price, inputPrice, outputPrice].every(usablePrice)) continue
      hasTokenPrice = true
      summary.inputPrices.push(inputPrice)
      summary.outputPrices.push(outputPrice)
      summary.modelRatios.push(price.model_ratio)
      summary.completionRatios.push(price.completion_ratio)
    }
    seen.add(key)
    if (!summary.sourceSiteIds.includes(candidate.siteId)) summary.sourceSiteIds.push(candidate.siteId)
  }
  summary.kind = hasTokenPrice && hasCallPrice ? 'mixed' : hasCallPrice ? 'call' : 'token'
  summary.sourceCount = seen.size
  return summary.sourceCount ? summary : null
}

function usablePrice(value: number): boolean {
  return Number.isFinite(value) && value >= 0
}

function findSiteModelPrice(snapshot: SitePricingSnapshot | undefined, model: string): SiteModelPrice | undefined {
  if (!snapshot) return undefined
  const exact = snapshot.models.find((item) => item.model === model)
  if (exact) return exact
  const normalized = model.toLowerCase()
  return snapshot.models.find((item) => item.model.toLowerCase() === normalized)
}
