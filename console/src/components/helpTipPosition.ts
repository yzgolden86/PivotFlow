export function helpTipPosition(
  anchor: { left: number; top: number; bottom: number },
  bubble: { width: number; height: number },
  viewport: { width: number; height: number },
) {
  const margin = 12
  const gap = 8
  const left = Math.max(margin, Math.min(anchor.left - 8, viewport.width - bubble.width - margin))
  const below = anchor.bottom + gap
  const above = anchor.top - bubble.height - gap
  const top = below + bubble.height <= viewport.height - margin ? below : Math.max(margin, above)
  return { left, top }
}
