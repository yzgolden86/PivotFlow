import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ChevronLeft, ChevronRight, Eye, EyeOff, RefreshCw, X } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { listWindow, parsePageInput } from './listWindow'
import { registerLiveRegion } from '../components/focusLayers'

export function PageHeader({ icon: Icon, title, actions, className = '', tone = 'green' }: {
  icon: LucideIcon
  title: ReactNode
  actions?: ReactNode
  className?: string
  tone?: 'green' | 'blue' | 'amber' | 'coral' | 'graphite'
}) {
  return (
    <header className={`page-header page-header--elevated page-header--tone-${tone}${className ? ` ${className}` : ''}`}>
      <div className="page-header-copy">
        <span className="page-header-icon" aria-hidden="true"><Icon size={20} /></span>
        <div className="page-header-title">
          <h1>{title}</h1>
        </div>
      </div>
      {actions && <div className="header-controls">{actions}</div>}
    </header>
  )
}

export function OperationNotice({ children, persistent = false, onDismiss, tone = 'success', className = '' }: { children: ReactNode; persistent?: boolean; onDismiss?: () => void; tone?: 'success' | 'warning' | 'error'; className?: string }) {
  const [visible, setVisible] = useState(true)
  const onDismissRef = useRef(onDismiss)
  const announcement = useRef<HTMLSpanElement>(null)

  useLayoutEffect(() => {
    if (visible && announcement.current) return registerLiveRegion(announcement.current)
  }, [visible])

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

  // portal 到 body：`.operation-notice` 是 `position: fixed; top: 22px; right: 24px`，
  // 但它被渲染在页面根节点内部，而页面根节点正是 `.main-content > *`，带
  // `animation: pf-rise ... both`（styles.css 末尾）。动画在跑的那一帧里
  // `transform: translateY(8px)` 是非 none 值 —— 非 none 的 transform 会让元素成为
  // 后代 fixed 元素的包含块。当前 `pf-rise` 的 `to` 落在 `transform: none`，动画结束后
  // 包含块会释放，所以现状「看不出来坏」；但只要有人把收尾帧改成带 transform 的值，
  // 这条提示就会从「钉在视口右上角」变成「跟着页面滚」。与 `.search-overlay` 是同一类坑，
  // 这里顺手收口。
  return createPortal(
    <div className={`operation-notice operation-notice--${tone}${className ? ` ${className}` : ''}`}>
      <span ref={announcement} className="operation-notice__content" role={tone === 'error' ? 'alert' : 'status'} aria-live={tone === 'error' ? 'assertive' : 'polite'}>{children}</span>
      <button className="operation-notice__dismiss" type="button" onClick={dismiss} aria-label="关闭提示"><X size={15} /></button>
    </div>,
    document.body,
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
export function SecretInput({ value, change, placeholder, required = false, disabled = false, autoComplete = 'off' }: {
  value: string
  change: (value: string) => void
  placeholder?: string
  required?: boolean
  disabled?: boolean
  autoComplete?: string
}) {
  const [visible, setVisible] = useState(false)
  useEffect(() => { if (disabled) setVisible(false) }, [disabled])
  // 已保存的凭证从不回传浏览器（SiteAccount.CredentialCiphertext 标了 json:"-"），
  // 所以编辑时输入框初值是空的。此时显示切换按钮只会照出一个空框，
  // 让人以为功能坏了。只有在本次真的输入了内容时才提供切换。
  const hasValue = value.length > 0
  return <span className="secret-input">
    <input required={required} disabled={disabled} type={visible && hasValue ? 'text' : 'password'} autoComplete={autoComplete} value={value} onChange={(event) => change(event.target.value)} placeholder={placeholder} />
    {hasValue && <button type="button" disabled={disabled} onClick={() => setVisible((current) => !current)} aria-label={visible ? '隐藏内容' : '显示内容'} title={visible ? '隐藏内容' : '核对已输入的内容'}>
      {visible ? <EyeOff size={16} /> : <Eye size={16} />}
    </button>}
  </span>
}

export function Pagination({ page, pageSize, total, onPage, pageSizes, onPageSize, label = '分页' }: {
  page: number
  pageSize: number
  total: number
  onPage: (page: number) => void
  pageSizes?: number[]
  onPageSize?: (pageSize: number) => void
  label?: string
}) {
  const current = listWindow(total, page, pageSize)
  const [jump, setJump] = useState(String(current.page))
  const [invalid, setInvalid] = useState(false)
  const errorId = useId()
  useEffect(() => { setJump(String(current.page)); setInvalid(false) }, [current.page, current.pages])
  useEffect(() => { if (page !== current.page) onPage(current.page) }, [page, current.page, onPage])
  const go = () => {
    const target = parsePageInput(jump, current.pages)
    if (target === null) { setInvalid(true); return }
    setInvalid(false)
    onPage(target)
  }
  return (
    <nav className="pagination" aria-label={label}>
      <div className="pagination-meta"><span aria-live="polite">共 {current.total} 条 · {current.total ? current.start + 1 : 0}–{current.end} 条</span>{pageSizes && onPageSize && <label>每页<select value={pageSize} onChange={(event) => onPageSize(Number(event.target.value))}>{pageSizes.map((size) => <option value={size} key={size}>{size}</option>)}</select>条</label>}</div>
      <div className="pagination-controls">
        <button className="icon-button icon-button--surface" type="button" disabled={current.page <= 1} onClick={() => onPage(current.page - 1)} aria-label="上一页"><ChevronLeft size={16} /></button>
        <strong>{current.page} / {current.pages}</strong>
        <button className="icon-button icon-button--surface" type="button" disabled={current.page >= current.pages} onClick={() => onPage(current.page + 1)} aria-label="下一页"><ChevronRight size={16} /></button>
        {current.pages > 1 && <div className="pagination-jump"><label>跳至<input type="text" inputMode="numeric" value={jump} aria-label="跳转页码" aria-invalid={invalid || undefined} aria-describedby={invalid ? errorId : undefined} onChange={(event) => { setJump(event.target.value); setInvalid(false) }} onKeyDown={(event) => { if (event.key === 'Enter' && !event.nativeEvent.isComposing) { event.preventDefault(); event.stopPropagation(); go() } }} />页</label><button className="text-button" type="button" onClick={go}>跳转</button></div>}
      </div>
      {invalid && <p id={errorId} className="pagination-error" role="alert">请输入 1 到 {current.pages} 的整数页码。</p>}
    </nav>
  )
}

export function SelectionSummary({ selected, matching, clear }: { selected: number; matching: number; clear: () => void }) {
  return <div className="batch-selection-summary"><div><strong>已选择 {selected} 项</strong><span>当前筛选 {matching} 项{selected > matching ? ` · 筛选外 ${selected - matching} 项` : ''}</span></div><button className="text-button" type="button" onClick={clear}>清空选择</button></div>
}

// 数字/金额/百分比的实现在 ../format.ts（纯逻辑，可被 node --test 直接测）。
// 这里只做再导出，页面继续从 './shared' 拿，不用改 import 路径。
export { currencySymbol, formatMoney, formatMoneyIn, formatNumber, formatPercent, moneyDigits } from '../format'

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
