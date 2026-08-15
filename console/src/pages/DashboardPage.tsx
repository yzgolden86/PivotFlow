import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  ArrowDownRight,
  ArrowUpRight,
  BadgeDollarSign,
  CircleDollarSign,
  Cpu,
  Database,
  RefreshCw,
  Route,
  Server,
  ShieldCheck,
  WalletCards,
  Zap,
} from 'lucide-react'
import { getDashboard } from '../api'
import type {
  DashboardBalance,
  DashboardRange,
  DashboardSnapshot,
  DashboardUsage,
  MetricPoint,
} from '../types'

const rangeOptions: Array<{ value: DashboardRange; label: string }> = [
  { value: 'today', label: '今日' },
  { value: 'this_week', label: '本周' },
  { value: 'this_month', label: '本月' },
]

const toolTone: Record<string, string> = {
  anthropic: 'coral',
  codex: 'graphite',
  gemini: 'blue',
  openai: 'green',
}

export default function DashboardPage() {
  const [range, setRange] = useState<DashboardRange>('today')
  const [snapshot, setSnapshot] = useState<DashboardSnapshot | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async (selectedRange: DashboardRange, refresh = false, signal?: AbortSignal) => {
    if (refresh) setRefreshing(true)
    else setLoading(true)
    setError('')
    try {
      const data = await getDashboard(selectedRange, signal)
      setSnapshot(data)
    } catch (reason) {
      if (!signal?.aborted) {
        setError(reason instanceof Error ? reason.message : '概览加载失败')
      }
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void load(range, false, controller.signal)
    return () => controller.abort()
  }, [load, range])

  if (loading && !snapshot) return <DashboardSkeleton />
  if (error && !snapshot) return <DashboardError message={error} retry={() => void load(range)} />
  if (!snapshot) return null

  const successRate = snapshot.totals.requests
    ? (snapshot.totals.success / snapshot.totals.requests) * 100
    : 0
  const totalTokens = snapshot.totals.input_tokens + snapshot.totals.output_tokens

  return (
    <div className="dashboard-page">
      <header className="page-header dashboard-header">
        <h1>概览</h1>
        <div className="header-controls">
          <div className="range-control" role="radiogroup" aria-label="统计时间范围">
            {rangeOptions.map((option) => (
              <button
                className={range === option.value ? 'is-active' : ''}
                type="button"
                role="radio"
                aria-checked={range === option.value}
                key={option.value}
                onClick={() => setRange(option.value)}
              >
                {option.label}
              </button>
            ))}
          </div>
          <button
            className="icon-button icon-button--surface"
            type="button"
            onClick={() => void load(range, true)}
            disabled={refreshing}
            aria-label="刷新概览"
            title="刷新概览"
          >
            <RefreshCw className={refreshing ? 'spin' : ''} size={17} />
          </button>
        </div>
      </header>

      {error && <div className="inline-error">{error}</div>}

      <section className="kpi-grid" aria-label="关键指标">
        <MetricCard
          icon={WalletCards}
          tone="amber"
          label="站点余额"
          value={<BalanceValue balances={snapshot.balances} />}
          meta={`${snapshot.healthy_accounts}/${snapshot.account_count} 个健康账号`}
        />
        <MetricCard
          icon={CircleDollarSign}
          tone="green"
          label="消耗额度"
          value={formatMoney(snapshot.totals.effective_cost)}
          meta={`标准成本 ${formatMoney(snapshot.totals.cost)}`}
        />
        <MetricCard
          icon={Zap}
          tone="blue"
          label="请求与 Token"
          value={formatCompact(snapshot.totals.requests)}
          meta={`${formatCompact(totalTokens)} tokens`}
        />
        <MetricCard
          icon={ShieldCheck}
          tone={successRate >= 95 || snapshot.totals.requests === 0 ? 'green' : 'coral'}
          label="路由成功率"
          value={snapshot.totals.requests ? `${successRate.toFixed(1)}%` : '—'}
          meta={`${formatCompact(snapshot.totals.errors)} 次失败`}
        />
      </section>

      <section className="resource-strip" aria-label="资源状态">
        <ResourceStatus
          icon={Server}
          label="站点"
          value={`${snapshot.enabled_sites}/${snapshot.site_count}`}
          suffix="启用"
        />
        <ResourceStatus
          icon={Route}
          label="路由渠道"
          value={`${snapshot.enabled_channels}/${snapshot.channel_count}`}
          suffix="启用"
        />
        <ResourceStatus
          icon={Database}
          label="站点账号"
          value={`${snapshot.healthy_accounts}/${snapshot.account_count}`}
          suffix="健康"
        />
        <ResourceStatus
          icon={BadgeDollarSign}
          label="缓存命中"
          value={formatCompact(snapshot.totals.cache_read_tokens)}
          suffix="tokens"
        />
      </section>

      <section className="dashboard-grid dashboard-grid--primary">
        <div className="data-panel usage-panel">
          <PanelHeader title="模型消耗分配" caption="按实际费用排序" icon={Cpu} />
          <DistributionPanel items={snapshot.model_usage.slice(0, 7)} emptyLabel="暂无模型消耗" />
        </div>
        <div className="data-panel trend-panel">
          <PanelHeader title="消耗走势" caption={rangeLabel(range)} icon={ArrowUpRight} />
          <TrendBars points={snapshot.trend} />
        </div>
      </section>

      <section className="dashboard-grid dashboard-grid--secondary">
        <div className="data-panel site-panel">
          <PanelHeader title="站点消耗分配" caption="投影渠道归属" icon={Server} />
          <BarRanking items={snapshot.site_usage.slice(0, 6)} emptyLabel="暂无站点消耗" />
        </div>
        <div className="tool-section">
          <div className="section-heading">
            <div>
              <h2>工具消耗</h2>
              <p>按客户端入口协议归类</p>
            </div>
          </div>
          <div className="tool-grid">
            {normalizedTools(snapshot.client_usage).map((tool) => (
              <ToolCard key={tool.key} usage={tool} />
            ))}
          </div>
        </div>
      </section>

      <footer className="dashboard-footer">
        <span className="footer-health">
          <span /> 数据范围 {formatDateTime(snapshot.starts_at)} 至 {formatDateTime(snapshot.ends_at)}
        </span>
        <span>更新于 {formatDateTime(snapshot.generated_at)}</span>
      </footer>
    </div>
  )
}

function MetricCard({
  icon: Icon,
  tone,
  label,
  value,
  meta,
}: {
  icon: typeof WalletCards
  tone: string
  label: string
  value: React.ReactNode
  meta: string
}) {
  return (
    <article className={`metric-card metric-card--${tone}`}>
      <div className="metric-card-top">
        <span className="metric-label">{label}</span>
        <span className="metric-icon"><Icon size={18} /></span>
      </div>
      <div className="metric-value">{value}</div>
      <div className="metric-meta">{meta}</div>
    </article>
  )
}

function BalanceValue({ balances }: { balances: DashboardBalance[] }) {
  if (!balances.length) return <>—</>
  return (
    <span
      className={`balance-value${balances.length > 1 ? ' balance-value--multiple' : ''}`}
      aria-label={balances.map((balance) => `${currencyName(balance.currency)} ${formatNumber(balance.amount, 2)}`).join('，')}
    >
      {balances.map((balance) => (
        <span key={balance.currency} title={`${currencyName(balance.currency)}（${balance.currency}）`}>
          <b>{currencySymbol(balance.currency)}{formatNumber(balance.amount, 2)}</b>
          <small>{currencyName(balance.currency)}</small>
        </span>
      ))}
    </span>
  )
}

function ResourceStatus({
  icon: Icon,
  label,
  value,
  suffix,
}: {
  icon: typeof Server
  label: string
  value: string
  suffix: string
}) {
  return (
    <div className="resource-status">
      <Icon size={17} />
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{suffix}</small>
    </div>
  )
}

function PanelHeader({
  title,
  caption,
  icon: Icon,
}: {
  title: string
  caption: string
  icon: typeof Cpu
}) {
  return (
    <div className="panel-header">
      <div className="panel-title">
        <span className="panel-title-icon"><Icon size={16} /></span>
        <h2>{title}</h2>
      </div>
      <span>{caption}</span>
    </div>
  )
}

function UsageList({ items, emptyLabel }: { items: DashboardUsage[]; emptyLabel: string }) {
  if (!items.length) return <EmptyData label={emptyLabel} />
  return (
    <div className="usage-list">
      {items.map((item, index) => (
        <div className="usage-row" key={item.key}>
          <div className="usage-rank">{String(index + 1).padStart(2, '0')}</div>
          <div className="usage-main">
            <div className="usage-label-row">
              <strong title={item.label}>{item.label}</strong>
              <span>{formatMoney(item.effective_cost)}</span>
            </div>
            <div className="usage-progress" aria-hidden="true">
              <span style={{ width: `${Math.max(item.share * 100, item.requests ? 2 : 0)}%` }} />
            </div>
            <div className="usage-meta">
              <span>{formatCompact(item.requests)} 请求</span>
              <span>{formatCompact(item.input_tokens + item.output_tokens)} tokens</span>
              <span>{formatPercent(item.share)}</span>
            </div>
          </div>
        </div>
      ))}
    </div>
  )
}

function DistributionPanel({ items, emptyLabel }: { items: DashboardUsage[]; emptyLabel: string }) {
  if (!items.length) return <EmptyData label={emptyLabel} />
  const colors = ['var(--green)', 'var(--blue)', 'var(--amber)', 'var(--coral)', '#6f7d77', '#9aa59f', '#c2cbc6']
  let offset = 0
  const segments = items.map((item, index) => {
    const start = offset
    offset += Math.max(0, item.share * 100)
    return `${colors[index]} ${start}% ${offset}%`
  })
  if (offset < 100) segments.push(`var(--surface-strong) ${offset}% 100%`)
  return <div className="distribution-layout">
    <div className="donut-chart" role="img" aria-label="模型消耗占比" style={{ background: `conic-gradient(${segments.join(', ')})` }}>
      <div><strong>{formatMoney(items.reduce((sum, item) => sum + item.effective_cost, 0))}</strong><span>模型消耗</span></div>
    </div>
    <div className="distribution-legend">{items.slice(0, 5).map((item, index) => <div key={item.key}><i style={{ background: colors[index] }} /><strong title={item.label}>{item.label}</strong><span>{formatPercent(item.share)}</span></div>)}</div>
  </div>
}

function BarRanking({ items, emptyLabel }: { items: DashboardUsage[]; emptyLabel: string }) {
  if (!items.length) return <EmptyData label={emptyLabel} />
  const maximum = Math.max(...items.map((item) => item.effective_cost), 1)
  return <div className="bar-ranking">{items.map((item) => <div className="bar-ranking-row" key={item.key}><div><strong title={item.label}>{item.label}</strong><span>{formatMoney(item.effective_cost)}</span></div><div className="bar-ranking-track"><i style={{ width: `${Math.max((item.effective_cost / maximum) * 100, item.requests ? 3 : 0)}%` }} /></div><small>{formatCompact(item.requests)} 请求</small></div>)}</div>
}

function TrendBars({ points }: { points: MetricPoint[] }) {
  const visible = points.slice(-32)
  const hasCost = visible.some((point) => Number(point.effective_cost || 0) > 0)
  const values = visible.map((point) => hasCost
    ? Number(point.effective_cost || 0)
    : Number(point.success || 0) + Number(point.error || 0))
  const maximum = Math.max(...values, 1)

  if (!visible.length) return <EmptyData label="暂无趋势数据" />
  return (
    <div className="trend-chart" role="img" aria-label={hasCost ? '费用消耗走势' : '请求量走势'}>
      <div className="trend-axis">
        <span>{hasCost ? formatMoney(maximum) : formatCompact(maximum)}</span>
        <span>{hasCost ? formatMoney(maximum / 2) : formatCompact(maximum / 2)}</span>
        <span>0</span>
      </div>
      <div className="trend-bars">
        {visible.map((point, index) => {
          const value = values[index]
          const height = Math.max((value / maximum) * 100, value > 0 ? 4 : 1)
          const total = Number(point.success || 0) + Number(point.error || 0)
          return (
            <span
              className={point.error > point.success && total > 0 ? 'trend-bar trend-bar--warning' : 'trend-bar'}
              key={`${point.ts}-${index}`}
              style={{ height: `${height}%` }}
              title={`${formatPointTime(point.ts)} · ${hasCost ? formatMoney(value) : `${formatCompact(value)} 请求`}`}
            />
          )
        })}
      </div>
      <div className="trend-caption">
        <span>{formatPointTime(visible[0]?.ts)}</span>
        <span>{hasCost ? '有效费用' : '请求数量'}</span>
        <span>{formatPointTime(visible.at(-1)?.ts)}</span>
      </div>
    </div>
  )
}

function ToolCard({ usage }: { usage: DashboardUsage }) {
  const successRate = usage.requests ? (usage.success / usage.requests) * 100 : 0
  const tone = toolTone[usage.key] || 'amber'
  return (
    <article className={`tool-card tool-card--${tone}`}>
      <div className="tool-card-head">
        <span className="tool-monogram">{toolMonogram(usage.label)}</span>
        <span className="tool-share">{formatPercent(usage.share)}</span>
      </div>
      <h3>{usage.label}</h3>
      <div className="tool-cost">{formatMoney(usage.effective_cost)}</div>
      <div className="tool-meta">
        <span>{formatCompact(usage.requests)} 请求</span>
        <span>{usage.requests ? `${successRate.toFixed(0)}% 成功` : '暂无调用'}</span>
      </div>
    </article>
  )
}

function normalizedTools(items: DashboardUsage[]): DashboardUsage[] {
  const expected = [
    { key: 'anthropic', label: 'Claude Code' },
    { key: 'codex', label: 'Codex' },
    { key: 'gemini', label: 'Gemini' },
    { key: 'openai', label: 'OpenAI' },
  ]
  const lookup = new Map(items.map((item) => [item.key, item]))
  return expected.map((entry) => lookup.get(entry.key) || {
    ...entry,
    requests: 0,
    success: 0,
    errors: 0,
    input_tokens: 0,
    output_tokens: 0,
    effective_cost: 0,
    share: 0,
  })
}

function EmptyData({ label }: { label: string }) {
  return (
    <div className="empty-data">
      <ArrowDownRight size={18} />
      <span>{label}</span>
    </div>
  )
}

function DashboardSkeleton() {
  return (
    <div className="dashboard-page dashboard-skeleton" aria-label="正在加载概览">
      <div className="skeleton skeleton--title" />
      <div className="kpi-grid">
        {[0, 1, 2, 3].map((item) => <div className="skeleton skeleton--metric" key={item} />)}
      </div>
      <div className="skeleton skeleton--strip" />
      <div className="dashboard-grid dashboard-grid--primary">
        <div className="skeleton skeleton--panel" />
        <div className="skeleton skeleton--panel" />
      </div>
    </div>
  )
}

function DashboardError({ message, retry }: { message: string; retry: () => void }) {
  return (
    <div className="dashboard-error" role="alert">
      <div className="dashboard-error-mark">!</div>
      <h1>概览暂时不可用</h1>
      <p>{message}</p>
      <button type="button" className="primary-button" onClick={retry}>
        <RefreshCw size={16} /> 重试
      </button>
    </div>
  )
}

function formatCompact(value: number): string {
  return new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 }).format(value || 0)
}

function formatNumber(value: number, digits = 0): string {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: digits, minimumFractionDigits: digits }).format(value || 0)
}

function formatMoney(value: number): string {
  return new Intl.NumberFormat('zh-CN', {
    style: 'currency',
    currency: 'USD',
    maximumFractionDigits: value >= 100 ? 0 : value >= 1 ? 2 : 4,
  }).format(value || 0)
}

function formatPercent(value: number): string {
  return `${Math.max(0, value * 100).toFixed(value > 0 && value < 0.01 ? 1 : 0)}%`
}

function formatDateTime(value: number): string {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(value))
}

function formatPointTime(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  return new Intl.DateTimeFormat('zh-CN', {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(date)
}

function currencySymbol(currency: string): string {
  if (currency === 'CNY' || currency === 'RMB') return '¥'
  if (currency === 'EUR') return '€'
  if (currency === 'GBP') return '£'
  if (currency === 'JPY') return '¥'
  if (currency === 'USD' || currency === 'USDT') return '$'
  return `${currency} `
}

function currencyName(currency: string): string {
  if (currency === 'CNY' || currency === 'RMB') return '人民币'
  if (currency === 'USD') return '美元'
  if (currency === 'USDT') return 'USDT'
  if (currency === 'EUR') return '欧元'
  if (currency === 'GBP') return '英镑'
  if (currency === 'JPY') return '日元'
  if (currency === 'UNKNOWN') return '未标明币种'
  return currency
}

function rangeLabel(range: DashboardRange): string {
  if (range === 'this_week') return '本周分时'
  if (range === 'this_month') return '本月逐日'
  return '今日逐时'
}

function toolMonogram(label: string): string {
  if (label === 'Claude Code') return 'C'
  if (label === 'Gemini') return 'G'
  if (label === 'OpenAI') return 'O'
  return 'X'
}
