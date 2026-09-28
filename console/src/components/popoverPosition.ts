export interface PopoverAnchor {
  left: number
  right: number
  top: number
  bottom: number
}

export interface PopoverSize {
  width: number
  height: number
}

export interface PopoverViewport {
  width: number
  height: number
}

export interface PopoverOptions {
  /** 往哪边弹。默认 `below`（向下）。 */
  placement?: 'below' | 'above'
  /**
   * 右对齐会把浮层推出视口左边缘时，是否改成「贴着锚点右侧外挂」。
   *
   * 典型场景是窄锚点：收起态侧栏只有 72px 宽，而主题菜单有 168px，
   * 右对齐必然算出负的 left。默认 `false` 保留旧的「夹到左边距」行为。
   */
  flyOut?: boolean
}

/**
 * 浮层（下拉菜单 / 弹出面板）的定位。
 *
 * 抽成纯函数是因为组件本身跑不了单测（`node --test` 不做 JSX 转译），而这里的
 * 边界条件恰恰是最容易写错的地方：浮层默认**右对齐**到锚点（跟原来 CSS 的
 * `right: 0` 一致），并且要保证在窄屏 / 靠近视口边缘时不会跑到屏幕外面去。
 *
 * 返回视口坐标，供 `position: fixed` 使用 —— 调用方一律把浮层 portal 到
 * `document.body`，否则祖先的 `overflow: hidden` 会把它裁掉（页头、侧栏都带这属性）。
 */
export function popoverPosition(
  anchor: PopoverAnchor,
  menu: PopoverSize,
  viewport: PopoverViewport,
  options: PopoverOptions = {},
): { left: number; top: number } {
  const margin = 12
  // 与锚点之间的间距，跟原来 CSS 里的 `calc(100% + 7px)` 保持一致。
  const gap = 7
  const { placement = 'below', flyOut = false } = options

  let left = anchor.right - menu.width
  if (left < margin && flyOut) {
    // 右对齐放不下才考虑外挂，且只在右侧真的放得下时才用；
    // 否则退回下面的夹取，至少保证不越界。
    const rightOfAnchor = anchor.right + gap
    if (rightOfAnchor + menu.width <= viewport.width - margin) left = rightOfAnchor
  }
  left = Math.max(margin, Math.min(left, viewport.width - menu.width - margin))

  const below = anchor.bottom + gap
  const above = anchor.top - menu.height - gap
  // `below` 分支保持原样：下方放得下就向下弹，放不下才翻到上方。
  // `above` 分支是给贴底的浮层用的（侧栏底部的主题菜单）：上方放得下就向上弹，
  // 放不下才翻到下方，并夹进视口。
  const top = placement === 'above'
    ? (above >= margin ? above : Math.max(margin, Math.min(below, viewport.height - menu.height - margin)))
    : (below + menu.height <= viewport.height - margin ? below : Math.max(margin, above))

  return { left, top }
}
