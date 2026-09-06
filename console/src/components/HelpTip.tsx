import { useId, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { CircleHelp } from 'lucide-react'
import { helpTipPosition } from './helpTipPosition'

export default function HelpTip({ label, text, code }: { label: string; text: string; code?: string }) {
  const [mode, setMode] = useState<'closed' | 'preview' | 'pinned'>('closed')
  const [position, setPosition] = useState<{ left: number; top: number } | null>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const bubble = useRef<HTMLSpanElement>(null)
  const id = useId()
  const open = mode !== 'closed'

  useLayoutEffect(() => {
    if (!open) {
      setPosition(null)
      return
    }
    const place = () => {
      if (!trigger.current || !bubble.current) return
      setPosition(helpTipPosition(trigger.current.getBoundingClientRect(), bubble.current.getBoundingClientRect(), {
        width: document.documentElement.clientWidth, height: window.innerHeight,
      }))
    }
    const dismiss = (event: PointerEvent) => {
      if (event.target instanceof Node && !trigger.current?.contains(event.target) && !bubble.current?.contains(event.target)) setMode('closed')
    }
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setMode('closed')
    }
    place()
    const observer = new ResizeObserver(place)
    if (trigger.current) observer.observe(trigger.current)
    if (bubble.current) observer.observe(bubble.current)
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, true)
    document.addEventListener('pointerdown', dismiss, true)
    document.addEventListener('keydown', escape)
    return () => {
      observer.disconnect()
      window.removeEventListener('resize', place)
      window.removeEventListener('scroll', place, true)
      document.removeEventListener('pointerdown', dismiss, true)
      document.removeEventListener('keydown', escape)
    }
  }, [open])

  return <span className="help-tip">
    <button ref={trigger} className="help-tip-trigger" type="button" aria-label={`${label} 的说明`} aria-expanded={open} aria-describedby={open ? id : undefined}
      onPointerEnter={(event) => { if (event.pointerType === 'mouse') setMode((current) => current === 'closed' ? 'preview' : current) }}
      onPointerLeave={() => setMode((current) => current === 'preview' ? 'closed' : current)}
      onFocus={(event) => { if (event.currentTarget.matches(':focus-visible')) setMode('preview') }}
      onBlur={() => setMode('closed')}
      onClick={(event) => { event.stopPropagation(); setMode((current) => current === 'pinned' ? 'closed' : 'pinned') }}>
      <CircleHelp size={15} aria-hidden="true" />
    </button>
    {open && createPortal(<span ref={bubble} id={id} className="help-tip-bubble" role="tooltip" style={{ ...position, visibility: position ? 'visible' : 'hidden' }}>
      {text}{code && <code>{code}</code>}
    </span>, document.body)}
  </span>
}
