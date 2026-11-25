import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import AnchoredPopover from './AnchoredPopover'

/** A compact result label with hover preview and a pinned click/touch panel. */
export default function OperationDetails({ label, ariaLabel, title, className = '', children }: {
  label: string
  ariaLabel: string
  title?: string
  className?: string
  children: ReactNode
}) {
  const [mode, setMode] = useState<'closed' | 'preview' | 'pinned'>('closed')
  const anchor = useRef<HTMLButtonElement>(null)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const id = useId()
  const clearTimer = () => { clearTimeout(timer.current) }
  const leave = () => {
    clearTimer()
    timer.current = setTimeout(() => setMode((current) => current === 'preview' ? 'closed' : current), 180)
  }
  useEffect(() => () => clearTimeout(timer.current), [])
  return <>
    <button ref={anchor} className={`text-button operation-details-trigger${className ? ` ${className}` : ''}`} type="button" title={title}
      aria-label={ariaLabel} aria-haspopup="dialog" aria-expanded={mode !== 'closed'} aria-controls={mode !== 'closed' ? id : undefined}
      onPointerEnter={(event) => { if (event.pointerType === 'mouse') { clearTimer(); setMode((current) => current === 'closed' ? 'preview' : current) } }}
      onPointerLeave={leave}
      onClick={() => { clearTimer(); setMode((current) => current === 'pinned' ? 'closed' : 'pinned') }}>
      {label}
    </button>
    <AnchoredPopover open={mode !== 'closed'} onOpenChange={(open) => { if (!open) setMode('closed') }} anchorRef={anchor}
      className="operation-details-popover" role="dialog" ariaLabel={ariaLabel} passive={mode === 'preview'} tabThrough>
      <div id={id} onPointerEnter={clearTimer} onPointerLeave={leave}>{children}</div>
    </AnchoredPopover>
  </>
}
