import { ChevronDown, ReceiptText } from 'lucide-react'
import type { CostSummary } from '../types'
import { analyzeCostSummary } from '../costSemantics.ts'
import { moneyDigits } from '../format.ts'
import '../costSemantics.css'

export default function CostSummaryPanel({
  summary,
  title = '已记录费用',
  compact = false,
  className = '',
}: {
  summary: unknown
  title?: string
  compact?: boolean
  className?: string
}) {
  const values = summaryMoneyValues(summary)
  const view = analyzeCostSummary(summary, moneyDigits(values))
  const successful = view.summary?.successful_requests || 0
  const coverage = successful > 0 ? Math.min(100, (view.knownRequests / successful) * 100) : 0
  const readinessLabel = view.status !== 'ready'
    ? '依据缺失'
    : successful === 0
      ? '暂无成功请求'
      : view.completeCoverage
        ? '覆盖完整'
        : '覆盖不完整'

  return (
    <section
      className={`cost-summary-panel${compact ? ' cost-summary-panel--compact' : ''}${className ? ` ${className}` : ''}`}
      aria-label="已记录费用与估算覆盖"
    >
      <header className="cost-summary-panel__head">
        <div className="cost-summary-panel__identity">
          <span className="cost-summary-panel__icon" aria-hidden="true"><ReceiptText size={17} /></span>
          <div>
            <span>{title}</span>
            <strong>{view.amountText}</strong>
            <small>本地记录金额 · 尚未与上游账单对账</small>
          </div>
        </div>
        <span className={`cost-summary-panel__state cost-summary-panel__state--${view.status === 'ready' ? 'ready' : 'missing'}`}>
          {readinessLabel}
        </span>
      </header>

      <p className="cost-summary-panel__coverage">{view.coverageText}</p>
      <div
        className="cost-summary-panel__meter"
        role="img"
        aria-label={view.status === 'ready' ? `完整估算覆盖 ${view.knownRequests}/${successful}` : '完整估算覆盖无法判断'}
      >
        <i style={{ width: `${coverage}%` }} />
      </div>

      {view.status === 'ready' ? (
        <details className="cost-summary-panel__details">
          <summary><span>查看费用依据</span><ChevronDown size={15} aria-hidden="true" /></summary>
          <div className="cost-summary-panel__breakdown">
            <strong>{view.recordedBreakdown}</strong>
            {view.detailRows.map((row) => (
              <p key={row.label}><span>{row.label}</span><b>{row.value}</b></p>
            ))}
          </div>
        </details>
      ) : (
        <p className="cost-summary-panel__unavailable">{view.statusLabel}。历史已记录金额仍按原样保留；覆盖与来源无法确认。</p>
      )}
    </section>
  )
}

function summaryMoneyValues(value: unknown): number[] {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return []
  const summary = value as Partial<CostSummary>
  return [
    summary.recorded_cost_usd,
    summary.estimated_cost_usd,
    summary.partial_cost_usd,
    summary.historical_cost_usd,
  ].filter((item): item is number => typeof item === 'number' && Number.isFinite(item))
}
