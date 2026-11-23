/** One keyboard owner for nested portals, dialogs and the mobile drawer. */
export interface FocusLayerOptions {
  modal: boolean
  /** Read-only hints own Escape, but do not take focus or change Tab order. */
  passive?: boolean
  close: () => void
  initialFocus?: () => HTMLElement | null | undefined
  returnFocus?: () => HTMLElement | null | undefined
  /** A sibling backdrop may dismiss the layer without exposing the page. */
  backdrop?: () => HTMLElement | null | undefined
}

interface FocusLayer extends FocusLayerOptions {
  element: HTMLElement
  opener: HTMLElement | null
  zIndex: string
}

const layers: FocusLayer[] = []
const liveRegions = new Set<HTMLElement>()
const isolated = new Map<HTMLElement, boolean>()
let savedOverflow: { overflow: string; x: string; y: string } | null = null

function available(element: HTMLElement | null | undefined): element is HTMLElement {
  return Boolean(element?.isConnected && !element.matches(':disabled') && !element.closest('[inert]')
    && element.getClientRects().length && getComputedStyle(element).visibility !== 'hidden')
}

function focus(element: HTMLElement | null | undefined, scroll = false) {
  if (available(element)) element.focus({ preventScroll: !scroll })
}

function tabbable(element: HTMLElement) {
  return [...element.querySelectorAll<HTMLElement>('a[href], button, input, select, textarea, summary, [tabindex], [contenteditable="true"]')]
    .filter((item) => item.tabIndex >= 0 && available(item))
}

function refreshIsolation() {
  for (const [element, inert] of isolated) element.inert = inert
  isolated.clear()
  const modalIndex = layers.map((layer) => layer.modal).lastIndexOf(true)
  if (modalIndex < 0) {
    if (savedOverflow) {
      document.body.style.overflow = savedOverflow.overflow
      document.body.style.overflowX = savedOverflow.x
      document.body.style.overflowY = savedOverflow.y
      savedOverflow = null
    }
    return
  }
  if (!savedOverflow) savedOverflow = {
    overflow: document.body.style.overflow, x: document.body.style.overflowX, y: document.body.style.overflowY,
  }
  document.body.style.overflow = 'hidden'
  const permittedLayers = layers.slice(modalIndex)
  const announcements = [...liveRegions].filter((element) => element.isConnected)
  const allowed = permittedLayers.flatMap((layer) => [layer.element, layer.backdrop?.()])
    .filter((element): element is HTMLElement => Boolean(element?.isConnected))
  allowed.push(...announcements)
  const isolateChildren = (parent: HTMLElement) => {
    for (const child of parent.children) {
      if (!(child instanceof HTMLElement) || allowed.includes(child)) continue
      if (allowed.some((element) => child.contains(element))) isolateChildren(child)
      else {
        isolated.set(child, child.inert)
        child.inert = true
      }
    }
  }
  isolateChildren(document.body)
  for (const region of announcements) {
    if (permittedLayers.some((layer) => layer.element.contains(region))) continue
    for (const control of region.querySelectorAll<HTMLElement>('a[href], button, input, select, textarea, summary, [tabindex], [contenteditable="true"]')) {
      if (!isolated.has(control)) isolated.set(control, control.inert)
      control.inert = true
    }
  }
}

/** Expose operation text during a modal, while its background controls stay inert. */
export function registerLiveRegion(element: HTMLElement): () => void {
  liveRegions.add(element)
  refreshIsolation()
  return () => { liveRegions.delete(element); refreshIsolation() }
}

function menuItems(element: HTMLElement) {
  return [...element.querySelectorAll<HTMLElement>('[role="menuitem"], [role="menuitemradio"], [role="menuitemcheckbox"]')]
    .filter(available)
}

function onKeyDown(event: KeyboardEvent) {
  let top = layers.at(-1)
  if (!top || event.isComposing) return
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopImmediatePropagation()
    top.close()
    return
  }
  if (top.passive) {
    if (event.key !== 'Tab') return
    top.close()
    top = [...layers].reverse().find((layer) => !layer.passive)
    if (!top) return
  }
  if (!top.modal) {
    const items = menuItems(top.element)
    if (items.length && ['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault()
      event.stopImmediatePropagation()
      const current = items.indexOf(document.activeElement as HTMLElement)
      const index = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1
        : event.key === 'ArrowUp' ? (current < 0 ? items.length - 1 : (current - 1 + items.length) % items.length)
          : (current + 1) % items.length
      focus(items[index], true)
    } else if (event.key === 'Tab') {
      event.preventDefault()
      event.stopImmediatePropagation()
      const opener = top.returnFocus?.() ?? top.opener
      top.close()
      requestAnimationFrame(() => {
        const modal = [...layers].reverse().find((layer) => layer.modal)
        const controls = tabbable(modal?.element ?? document.body)
        const current = controls.indexOf(opener as HTMLElement)
        const next = current < 0 ? 0 : current + (event.shiftKey ? -1 : 1)
        focus(controls[(next + controls.length) % controls.length] ?? modal?.element, true)
      })
    }
    return
  }
  if (event.key !== 'Tab') return
  const controls = tabbable(top.element)
  const first = controls[0], last = controls.at(-1)
  const active = document.activeElement
  if (!first || !last) {
    event.preventDefault()
    focus(top.element)
  } else if (!top.element.contains(active) || active === top.element) {
    event.preventDefault()
    focus(event.shiftKey ? last : first, true)
  } else if (event.shiftKey && active === first) {
    event.preventDefault()
    focus(last, true)
  } else if (!event.shiftKey && active === last) {
    event.preventDefault()
    focus(first, true)
  }
}

export function registerFocusLayer(element: HTMLElement, options: FocusLayerOptions): () => void {
  const layer: FocusLayer = { ...options, element,
    opener: document.activeElement instanceof HTMLElement ? document.activeElement : null,
    zIndex: element.style.zIndex,
  }
  if (!layers.length) document.addEventListener('keydown', onKeyDown, true)
  layers.push(layer)
  // A menu is a body sibling of its dialog. Keep it above the existing dialog
  // levels without changing the portal placement or the drawer's stacking root.
  if (!options.modal && !options.passive) element.style.zIndex = String(140 + layers.length)
  refreshIsolation()
  if (!options.passive) {
    const initial = options.initialFocus?.()
    if (available(initial)) focus(initial)
    else if (!options.modal) focus(menuItems(element)[0] ?? tabbable(element)[0] ?? element)
    else if (!element.contains(document.activeElement)) focus(element)
  }
  return () => {
    const index = layers.indexOf(layer)
    if (index < 0) return
    const wasTop = index === layers.length - 1
    layers.splice(index, 1)
    element.style.zIndex = layer.zIndex
    refreshIsolation()
    if (!layers.length) document.removeEventListener('keydown', onKeyDown, true)
    if (!wasTop || options.passive) return
    const active = document.activeElement
    // A pointer dismissal can already have focused another valid control.
    if (active instanceof HTMLElement && active !== document.body && !element.contains(active)
      && !layer.backdrop?.()?.contains(active) && available(active)) return
    const target = options.returnFocus?.() ?? layer.opener
    if (available(target)) focus(target)
    else focus(layers.at(-1)?.element ?? document.getElementById('main-content'))
  }
}
