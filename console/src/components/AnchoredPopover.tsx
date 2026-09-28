import { useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import { popoverPosition } from './popoverPosition'

/**
 * 挂在锚点上的浮层（下拉菜单 / 弹出面板）。
 *
 * **一律 portal 到 `document.body`，不要留在页面里。** 原因有两层，本项目两个都踩过：
 * 1. 祖先的 `overflow: hidden` 会直接把它裁掉 —— 页头 `.page-header--elevated`
 *    （要裁左侧竖条和右上角光晕）裁掉了「其他来源」菜单，侧栏 `.sidebar`
 *    （要裁收起态的导航内容）裁掉了主题菜单。
 * 2. 祖先的 `transform` / `filter` / `animation` 会形成层叠上下文，把 `position: fixed`
 *    关进页面内部，于是浮层的 `z-index` 反而比不过侧栏（`.modal-layer` 踩过）。
 *
 * 判断依据：「这个浮层的祖先里有没有 overflow / transform / filter / animation」，
 * 有就必须 portal。
 *
 * 开合状态由调用方持有（受控），因为调用方通常还需要在别的时机收起它
 * （例如渠道页打开编辑器时要收起来源菜单）。
 */
export default function AnchoredPopover({
  open,
  onOpenChange,
  anchorRef,
  placement = 'below',
  flyOut = false,
  className,
  role,
  ariaLabel,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** 定位基准元素。它同时也是「点击内部不关闭」的判断范围，所以触发按钮要在里面。 */
  anchorRef: RefObject<HTMLElement | null>
  placement?: 'below' | 'above'
  /** 见 `popoverPosition` 的 `flyOut`。 */
  flyOut?: boolean
  className?: string
  role?: string
  ariaLabel?: string
  children: ReactNode
}) {
  const [position, setPosition] = useState<{ left: number; top: number } | null>(null)
  const menu = useRef<HTMLDivElement>(null)

  useLayoutEffect(() => {
    if (!open) return
    const place = () => {
      const anchor = anchorRef.current
      const element = menu.current
      if (!anchor || !element) return
      setPosition(popoverPosition(
        anchor.getBoundingClientRect(),
        element.getBoundingClientRect(),
        { width: document.documentElement.clientWidth, height: window.innerHeight },
        { placement, flyOut },
      ))
    }
    // 点锚点和点浮层之外的地方都关掉；用捕获阶段，免得被内层 stopPropagation 拦掉。
    const dismiss = (event: PointerEvent) => {
      if (!(event.target instanceof Node)) return
      if (anchorRef.current?.contains(event.target)) return
      if (menu.current?.contains(event.target)) return
      onOpenChange(false)
    }
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onOpenChange(false)
    }
    place()
    // 页面滚动 / 窗口缩放 / 锚点或浮层自身尺寸变化都要重新定位，
    // 否则浮层会停在旧坐标上（它已经不在锚点的定位上下文里了）。
    const observer = new ResizeObserver(place)
    if (anchorRef.current) observer.observe(anchorRef.current)
    if (menu.current) observer.observe(menu.current)
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
  }, [open, onOpenChange, anchorRef, placement, flyOut])

  if (!open) return null

  return createPortal(
    <div
      ref={menu}
      className={className}
      role={role}
      aria-label={ariaLabel}
      // 首帧还不知道尺寸，先隐藏但保留布局，量完再显示，避免闪一下错位。
      style={{ ...position, visibility: position ? 'visible' : 'hidden' }}
    >
      {children}
    </div>,
    document.body,
  )
}
