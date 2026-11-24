import { formatMoney } from './format.ts'
import type {
  CostBasis,
  CostDetails,
  CostEntryLike,
  CostSource,
  CostStatus,
  CostSummary,
  StandardCostBreakdown,
  StandardCostComponent,
} from './types.ts'

const COST_STATUSES = new Set<CostStatus>([
  'estimated',
  'zero_price',
  'local_free',
  'usage_missing',
  'unpriced_model',
  'partial_estimate',
  'invalid',
  'historical_unknown',
  'not_charged',
])

const COST_BASES = new Set<CostBasis>(['token', 'per_call', 'mixed', 'unknown'])
const COST_SOURCES = new Set<CostSource>(['site_pricing', 'local_estimate'])
const MAX_BACKEND_COST_USD = (2 ** 63) / 1_000_000

export const COST_STATUS_LABELS: Record<CostStatus, string> = {
  estimated: '完整估算',
  zero_price: '价目计算结果为零',
  local_free: '本地记账免费（上游费用未确认）',
  usage_missing: '未获取计费用量',
  unpriced_model: '当时无可用模型价格',
  partial_estimate: '部分估算',
  invalid: '费用数据无效',
  historical_unknown: '历史记录，依据不完整',
  not_charged: '未按成功请求记账',
}

export const COST_SOURCE_LABELS: Record<CostSource, string> = {
  site_pricing: '站点价目表估算',
  local_estimate: '本地公开价估算',
}

export const COST_BASIS_LABELS: Record<CostBasis, string> = {
  token: '按 Token',
  per_call: '按次',
  mixed: '多项费用组合',
  unknown: '计费方式未知',
}

export const COST_SOURCE_FILTER_OPTIONS: ReadonlyArray<{ value: CostSource | ''; label: string }> = [
  { value: '', label: '全部估算来源' },
  { value: 'site_pricing', label: COST_SOURCE_LABELS.site_pricing },
  { value: 'local_estimate', label: COST_SOURCE_LABELS.local_estimate },
]

export const COST_STATUS_FILTER_OPTIONS: ReadonlyArray<{ value: CostStatus | ''; label: string }> = [
  { value: '', label: '全部费用状态' },
  { value: 'estimated', label: COST_STATUS_LABELS.estimated },
  { value: 'zero_price', label: COST_STATUS_LABELS.zero_price },
  { value: 'local_free', label: COST_STATUS_LABELS.local_free },
  { value: 'usage_missing', label: COST_STATUS_LABELS.usage_missing },
  { value: 'unpriced_model', label: COST_STATUS_LABELS.unpriced_model },
  { value: 'partial_estimate', label: COST_STATUS_LABELS.partial_estimate },
  { value: 'invalid', label: COST_STATUS_LABELS.invalid },
  { value: 'historical_unknown', label: COST_STATUS_LABELS.historical_unknown },
  { value: 'not_charged', label: COST_STATUS_LABELS.not_charged },
]

export const COST_BASIS_FILTER_OPTIONS: ReadonlyArray<{ value: CostBasis | ''; label: string }> = [
  { value: '', label: '全部计费方式' },
  { value: 'token', label: COST_BASIS_LABELS.token },
  { value: 'per_call', label: COST_BASIS_LABELS.per_call },
  { value: 'mixed', label: COST_BASIS_LABELS.mixed },
  { value: 'unknown', label: COST_BASIS_LABELS.unknown },
]

export interface CostEntryView {
  status: CostStatus
  amount: number | null
  amountText: string
  statusLabel: string
  sourceLabel: string
  basisLabel: string
  semanticLabel: string
  detailLabel: string
  title: string
  knownZero: boolean
  incomplete: boolean
  statusClass: string
}

export type CostSummaryStatus = 'ready' | 'missing' | 'unknown_version' | 'invalid'

export interface CostSummaryView {
  status: CostSummaryStatus
  coverageState: 'complete' | 'incomplete' | 'empty' | 'missing'
  summary: CostSummary | null
  amountText: string
  statusLabel: string
  coverageText: string
  recordedBreakdown: string
  knownRequests: number
  unknownOrPartialRequests: number
  completeCoverage: boolean
  reconciliation: 'not_reconciled'
  detailRows: Array<{ label: string; value: string }>
}

export function costStatusLabel(value: unknown): string {
  return isCostStatus(value) ? COST_STATUS_LABELS[value] : '费用状态未知'
}

export function costSourceLabel(value: unknown): string {
  return isCostSource(value) ? COST_SOURCE_LABELS[value] : '来源未记录'
}

export function costBasisLabel(value: unknown): string {
  return isCostBasis(value) ? COST_BASIS_LABELS[value] : COST_BASIS_LABELS.unknown
}

export function parseCostDetails(value: unknown): CostDetails | null {
  if (!isRecord(value)) return null
  const modelCost = finiteCostAmount(value.model_cost_usd)
  const toolCost = finiteCostAmount(value.tool_cost_usd)
  if (modelCost == null || toolCost == null || value.currency !== 'USD') return null

  const details: CostDetails = {
    model_cost_usd: modelCost,
    tool_cost_usd: toolCost,
    currency: 'USD',
  }
  const breakdown = parseCostBreakdown(value.breakdown)
  if (breakdown) details.breakdown = breakdown
  return details
}

export function buildCostEntryView(entry: CostEntryLike, digits?: number): CostEntryView {
  const statusCode = finiteInteger(entry.status_code)
  const reportedStatus = isCostStatus(entry.cost_status) ? entry.cost_status : null
  const version = finiteInteger(entry.cost_metadata_version)
  const rawAmount = finiteCostAmount(entry.effective_cost)

  let status: CostStatus
  if (statusCode != null && (statusCode < 200 || statusCode >= 300)) {
    status = 'not_charged'
  } else if (version !== 1 || !reportedStatus || reportedStatus === 'not_charged') {
    status = 'historical_unknown'
  } else if (reportedStatus) {
    status = reportedStatus
  } else {
    status = 'invalid'
  }

  let amount: number | null = rawAmount
  if (['not_charged', 'usage_missing', 'unpriced_model', 'invalid'].includes(status)) {
    amount = null
  } else if (status === 'historical_unknown' || status === 'partial_estimate') {
    amount = rawAmount != null && rawAmount > 0 ? rawAmount : null
  } else if (rawAmount == null) {
    status = 'invalid'
    amount = null
  } else if ((status === 'zero_price' || status === 'local_free') && rawAmount !== 0) {
    status = 'invalid'
    amount = null
  }

  const details = ['invalid', 'historical_unknown', 'not_charged'].includes(status)
    ? null
    : parseCostDetails(entry.cost_details)
  const source = isCostSource(entry.cost_source) ? entry.cost_source : null
  const sourceLabel = combinedSourceLabel(source, details?.tool_cost_usd || 0)
  const basisLabel = version === 1 ? costBasisLabel(entry.cost_basis) : COST_BASIS_LABELS.unknown
  const statusLabel = COST_STATUS_LABELS[status]
  const incomplete = ['usage_missing', 'unpriced_model', 'partial_estimate', 'historical_unknown', 'invalid'].includes(status)
  const semanticLabel = [statusLabel, sourceLabel, basisLabel].join(' · ')
  const detailLabel = describeCostDetails(details, digits)

  return {
    status,
    amount,
    amountText: amount == null ? '—' : formatMoney(amount, digits),
    statusLabel,
    sourceLabel,
    basisLabel,
    semanticLabel,
    detailLabel,
    title: [semanticLabel, detailLabel].filter(Boolean).join('\n'),
    knownZero: amount === 0 && (status === 'zero_price' || status === 'local_free'),
    incomplete,
    statusClass: `cost-entry-status--${status}`,
  }
}

export function analyzeCostSummary(value: unknown, digits?: number): CostSummaryView {
  if (value == null) {
    return unavailableSummary('missing', '费用依据未提供')
  }
  if (!isRecord(value)) {
    return unavailableSummary('invalid', '费用汇总数据无效，费用依据未提供')
  }
  if (value.version !== 1) {
    return unavailableSummary('unknown_version', '费用汇总版本不支持，费用依据未提供')
  }

  const summary = parseCostSummaryV1(value)
  if (!summary) {
    return unavailableSummary('invalid', '费用汇总数据无效，费用依据未提供')
  }

  const knownRequests = summary.estimated_requests + summary.zero_requests
  const unknownOrPartialRequests = summary.unknown_requests
  const completeCoverage = summary.successful_requests > 0 && unknownOrPartialRequests === 0 && knownRequests === summary.successful_requests
  const coverageText = summary.successful_requests === 0
    ? '暂无成功请求'
    : completeCoverage
      ? `完整估算 ${knownRequests}/${summary.successful_requests}`
      : `完整估算 ${knownRequests}/${summary.successful_requests} · ${unknownOrPartialRequests} 条待核对`
  const recordedBreakdown = [
    `完整估算 ${formatMoney(summary.estimated_cost_usd, digits)}`,
    `部分 ${formatMoney(summary.partial_cost_usd, digits)}`,
    `历史 ${formatMoney(summary.historical_cost_usd, digits)}`,
  ].join(' · ')

  return {
    status: 'ready',
    coverageState: summary.successful_requests === 0 ? 'empty' : completeCoverage ? 'complete' : 'incomplete',
    summary,
    amountText: formatMoney(summary.recorded_cost_usd, digits),
    statusLabel: '费用汇总已读取',
    coverageText,
    recordedBreakdown,
    knownRequests,
    unknownOrPartialRequests,
    completeCoverage,
    reconciliation: 'not_reconciled',
    detailRows: [
      { label: '估算来源', value: `站点价目表 ${summary.site_pricing_requests} 次 · 本地公开价 ${summary.local_estimate_requests} 次 · 混合计费 ${summary.mixed_requests} 次` },
      { label: '请求状态', value: `完整估算 ${summary.estimated_requests} · 零价 ${summary.zero_requests}（其中本地免费 ${summary.local_free_requests}）· 部分 ${summary.partial_requests}` },
      { label: '未知记录', value: `其他未知 ${summary.unknown_requests - summary.partial_requests - summary.historical_requests} · 历史记录 ${summary.historical_requests}` },
    ],
  }
}

function unavailableSummary(status: Exclude<CostSummaryStatus, 'ready'>, statusLabel: string): CostSummaryView {
  return {
    status,
    coverageState: 'missing',
    summary: null,
    amountText: '—',
    statusLabel,
    coverageText: '无法判断完整估算覆盖',
    recordedBreakdown: '',
    knownRequests: 0,
    unknownOrPartialRequests: 0,
    completeCoverage: false,
    reconciliation: 'not_reconciled',
    detailRows: [],
  }
}

function parseCostSummaryV1(value: Record<string, unknown>): CostSummary | null {
  if (value.currency !== 'USD' || value.reconciliation !== 'not_reconciled') return null

  const integerKeys = [
    'successful_requests',
    'estimated_requests',
    'zero_requests',
    'local_free_requests',
    'partial_requests',
    'unknown_requests',
    'historical_requests',
    'site_pricing_requests',
    'local_estimate_requests',
    'mixed_requests',
  ] as const
  const moneyKeys = [
    'recorded_cost_usd',
    'estimated_cost_usd',
    'partial_cost_usd',
    'historical_cost_usd',
  ] as const

  const integers = {} as Record<(typeof integerKeys)[number], number>
  for (const key of integerKeys) {
    const parsed = finiteNonNegativeInteger(value[key])
    if (parsed == null) return null
    integers[key] = parsed
  }
  const money = {} as Record<(typeof moneyKeys)[number], number>
  for (const key of moneyKeys) {
    const parsed = finiteNonNegative(value[key])
    if (parsed == null) return null
    money[key] = parsed
  }

  if (integers.local_free_requests > integers.zero_requests) return null
  if (integers.partial_requests + integers.historical_requests > integers.unknown_requests) return null
  if (
    integers.estimated_requests +
      integers.zero_requests +
      integers.unknown_requests !==
    integers.successful_requests
  ) return null

  return {
    version: 1,
    currency: 'USD',
    reconciliation: 'not_reconciled',
    ...integers,
    ...money,
  }
}

function combinedSourceLabel(source: CostSource | null, toolCost: number): string {
  const modelLabel = source ? COST_SOURCE_LABELS[source] : '模型费用来源未记录'
  return toolCost > 0 ? `${modelLabel} + 本地工具估算` : modelLabel
}

function describeCostDetails(details: CostDetails | null, digits?: number): string {
  if (!details) return ''
  const parts = [
    `模型费用 ${formatMoney(details.model_cost_usd, digits)}`,
    `工具费用 ${formatMoney(details.tool_cost_usd, digits)}（本地估算）`,
    '币种 USD',
  ]
  if (details.breakdown) {
    const breakdown = details.breakdown
    parts.push(`Token 明细：输入 ${formatMoney(breakdown.input.cost, digits)} / 输出 ${formatMoney(breakdown.output.cost, digits)} / 缓存读取 ${formatMoney(breakdown.cache_read.cost, digits)} / 缓存写入 ${formatMoney(breakdown.cache_write.cost, digits)}`)
  }
  return parts.join(' · ')
}

function parseCostBreakdown(value: unknown): StandardCostBreakdown | null {
  if (!isRecord(value)) return null
  const input = parseCostComponent(value.input)
  const output = parseCostComponent(value.output)
  const cacheRead = parseCostComponent(value.cache_read)
  const cacheWrite = parseCostComponent(value.cache_write)
  const total = finiteNonNegative(value.total)
  if (!input || !output || !cacheRead || !cacheWrite || total == null) return null

  const result: StandardCostBreakdown = {
    input,
    output,
    cache_read: cacheRead,
    cache_write: cacheWrite,
    total,
  }
  if (value.service_tier_multiplier !== undefined) {
    const multiplier = finitePositive(value.service_tier_multiplier)
    if (multiplier == null) return null
    result.service_tier_multiplier = multiplier
  }
  return result
}

function parseCostComponent(value: unknown): StandardCostComponent | null {
  if (!isRecord(value)) return null
  const price = finiteNonNegative(value.price_per_million)
  const quantity = finiteNonNegativeInteger(value.quantity)
  const cost = finiteNonNegative(value.cost)
  if (price == null || quantity == null || cost == null) return null
  return {
    price_per_million: price,
    quantity,
    cost,
  }
}

function isCostStatus(value: unknown): value is CostStatus {
  return typeof value === 'string' && COST_STATUSES.has(value as CostStatus)
}

function isCostSource(value: unknown): value is CostSource {
  return typeof value === 'string' && COST_SOURCES.has(value as CostSource)
}

function isCostBasis(value: unknown): value is CostBasis {
  return typeof value === 'string' && COST_BASES.has(value as CostBasis)
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function finiteNonNegative(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null
}

function finiteCostAmount(value: unknown): number | null {
  const amount = finiteNonNegative(value)
  if (amount == null || amount >= MAX_BACKEND_COST_USD) return null
  return amount
}

function finitePositive(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : null
}

function finiteInteger(value: unknown): number | null {
  return typeof value === 'number' && Number.isSafeInteger(value) ? value : null
}

function finiteNonNegativeInteger(value: unknown): number | null {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 ? value : null
}
