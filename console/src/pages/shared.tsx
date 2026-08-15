import { ChevronLeft, ChevronRight, RefreshCw } from 'lucide-react'

export function LoadingState({ label = '正在加载数据' }: { label?: string }) {
  return <div className="content-state" role="status"><RefreshCw className="spin" size={18} />{label}</div>
}

export function ErrorState({ message, retry }: { message: string; retry: () => void }) {
  return (
    <div className="content-state content-state--error" role="alert">
      <strong>数据加载失败</strong><span>{message}</span>
      <button className="secondary-button" type="button" onClick={retry}><RefreshCw size={15} />重试</button>
    </div>
  )
}

export function EmptyState({ label }: { label: string }) {
  return <div className="content-state content-state--empty">{label}</div>
}

export function Pagination({ page, pageSize, total, onPage, pageSizes, onPageSize }: {
  page: number
  pageSize: number
  total: number
  onPage: (page: number) => void
  pageSizes?: number[]
  onPageSize?: (pageSize: number) => void
}) {
  const pages = Math.max(1, Math.ceil(total / pageSize))
  return (
    <div className="pagination">
      <div className="pagination-meta"><span>共 {total} 条</span>{pageSizes && onPageSize && <label>每页<select value={pageSize} onChange={(event) => onPageSize(Number(event.target.value))}>{pageSizes.map((size) => <option value={size} key={size}>{size}</option>)}</select>条</label>}</div>
      <div>
        <button className="icon-button icon-button--surface" type="button" disabled={page <= 1} onClick={() => onPage(page - 1)} aria-label="上一页"><ChevronLeft size={16} /></button>
        <strong>{page} / {pages}</strong>
        <button className="icon-button icon-button--surface" type="button" disabled={page >= pages} onClick={() => onPage(page + 1)} aria-label="下一页"><ChevronRight size={16} /></button>
      </div>
    </div>
  )
}

export function formatNumber(value: number | undefined, digits = 0): string {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: digits }).format(value || 0)
}

export function formatMoney(value: number | undefined): string {
  return new Intl.NumberFormat('zh-CN', {
    style: 'currency', currency: 'USD', maximumFractionDigits: (value || 0) >= 1 ? 2 : 4,
  }).format(value || 0)
}

export function formatTime(timestamp: number): string {
  const value = timestamp > 10_000_000_000 ? timestamp : timestamp * 1000
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
  }).format(new Date(value))
}

export function successTone(rate: number): string {
  if (rate >= 0.95) return 'success'
  if (rate >= 0.8) return 'warning'
  return 'danger'
}
