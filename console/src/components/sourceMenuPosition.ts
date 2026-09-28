export interface SourceMenuAnchor {
  left: number
  right: number
  top: number
  bottom: number
}

export interface SourceMenuSize {
  width: number
  height: number
}

export interface SourceMenuViewport {
  width: number
  height: number
}

/**
 * 「其他来源」下拉菜单的定位。
 *
 * 抽成纯函数是因为组件本身跑不了单测（`node --test` 不做 JSX 转译），而这里的
 * 边界条件恰恰是最容易写错的地方：菜单默认**右对齐**到触发按钮（跟原来的
 * `right: 0` 一致），并且要保证在窄屏 / 靠近视口边缘时不会跑到屏幕外面去。
 *
 * 返回视口坐标，供 `position: fixed` 使用。
 */
export function sourceMenuPosition(
  anchor: SourceMenuAnchor,
  menu: SourceMenuSize,
  viewport: SourceMenuViewport,
): { left: number; top: number } {
  const margin = 12
  // 与按钮之间的间距，跟原来 CSS 里的 `calc(100% + 7px)` 保持一致。
  const gap = 7

  const left = Math.max(margin, Math.min(anchor.right - menu.width, viewport.width - menu.width - margin))

  const below = anchor.bottom + gap
  const above = anchor.top - menu.height - gap
  // 下方放得下就向下弹（默认）；放不下才翻到上方。页头在最上面，所以实际几乎总是向下。
  const top = below + menu.height <= viewport.height - margin ? below : Math.max(margin, above)

  return { left, top }
}
