import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { ChevronLeft, ChevronRight, Eye, EyeOff, RefreshCw, X } from 'lucide-react'

export function OperationNotice({ children, persistent = false, onDismiss, tone = 'success' }: { children: ReactNode; persistent?: boolean; onDismiss?: () => void; tone?: 'success' | 'warning' | 'error' }) {
  const [visible, setVisible] = useState(true)
  const onDismissRef = useRef(onDismiss)

  useEffect(() => { onDismissRef.current = onDismiss }, [onDismiss])

  const dismiss = useCallback(() => {
    setVisible(false)
    onDismissRef.current?.()
  }, [])

  useEffect(() => {
    setVisible(true)
    if (persistent) return
    const timer = window.setTimeout(dismiss, 4500)
    return () => window.clearTimeout(timer)
  }, [dismiss, persistent])

  if (!visible) return null

  return (
    <div className={`operation-notice operation-notice--${tone}`} role={tone === 'error' ? 'alert' : 'status'} aria-live={tone === 'error' ? 'assertive' : 'polite'}>
      <span className="operation-notice__content">{children}</span>
      <button className="operation-notice__dismiss" type="button" onClick={dismiss} aria-label="关闭提示"><X size={15} /></button>
    </div>
  )
}

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

// SecretInput 用统一的显隐切换替代裸 password 输入框。
// 浏览器自绘的显示按钮已在样式中屏蔽，避免和这里的眼睛图标重叠。
export function SecretInput({ value, change, placeholder, required = false, autoComplete = 'off' }: {
  value: string
  change: (value: string) => void
  placeholder?: string
  required?: boolean
  autoComplete?: string
}) {
  const [visible, setVisible] = useState(false)
  // 已保存的凭证从不回传浏览器（SiteAccount.CredentialCiphertext 标了 json:"-"），
  // 所以编辑时输入框初值是空的。此时显示切换按钮只会照出一个空框，
  // 让人以为功能坏了。只有在本次真的输入了内容时才提供切换。
  const hasValue = value.length > 0
  return <span className="secret-input">
    <input required={required} type={visible && hasValue ? 'text' : 'password'} autoComplete={autoComplete} value={value} onChange={(event) => change(event.target.value)} placeholder={placeholder} />
    {hasValue && <button type="button" onClick={() => setVisible((current) => !current)} aria-label={visible ? '隐藏内容' : '显示内容'} title={visible ? '隐藏内容' : '核对已输入的内容'}>
      {visible ? <EyeOff size={16} /> : <Eye size={16} />}
    </button>}
  </span>
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
  const amount = value || 0
  if (amount > 0 && amount < 0.0001) return '< $0.0001'
  const digits = amount >= 1 ? 2 : 4
  return `$${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: digits, minimumFractionDigits: digits }).format(amount)}`
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
