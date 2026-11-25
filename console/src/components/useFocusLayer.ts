import { useLayoutEffect, useRef, type RefObject } from 'react'
import { registerFocusLayer, type FocusLayerOptions } from './focusLayers'

export default function useFocusLayer(ref: RefObject<HTMLElement | null>, options: FocusLayerOptions & { open?: boolean }) {
  const latest = useRef(options)
  latest.current = options
  const open = options.open ?? true
  const modal = options.modal
  const passive = options.passive
  const tabThrough = options.tabThrough
  useLayoutEffect(() => {
    if (!open || !ref.current) return
    return registerFocusLayer(ref.current, {
      modal,
      passive,
      tabThrough,
      close: () => latest.current.close(),
      initialFocus: () => latest.current.initialFocus?.(),
      returnFocus: () => latest.current.returnFocus?.(),
      backdrop: () => latest.current.backdrop?.(),
    })
  }, [ref, open, modal, passive, tabThrough])
}
