import { useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { sourceMenuPosition } from './sourceMenuPosition'

export interface SourceMenuItem {
  label: string
  icon: ReactNode
  onSelect: () => void
}

/**
 * 页头里的下拉菜单（「其他来源」）。
 *
 * **必须挂到 `document.body`，不能留在页面容器里。** 页头是
 * `.page-header--elevated`，它带 `overflow: hidden` —— 那是为了把左侧 4px 竖条
 * （`::before`）和右上角那团径向光晕（`::after`，定位在 `top:-64px; right:-42px`）
 * 裁进圆角里。代价是：任何往下弹的绝对定位子元素都会被同一条边切掉。
 * 原来的 `.source-menu-popover` 正是这样被裁成一条缝的（只剩菜单顶部几个像素，
 * 下面直接被页头底边截断）。挪到 body 上就没有这个裁切上下文了。
 *
 * 位置改用视口坐标（`position: fixed`）+ `sourceMenuPosition` 计算，所以
 * 不需要再依赖触发按钮的 `position: relative` 祖先。
 *
 * 开合状态由调用方持有（受控），这样页面在「渠道编辑器被打开」这类场景下
 * 仍然能主动把菜单收起来 —— 那是本次改动前就有的行为，别弄丢。
 */
export default function SourceMenu({ open, onOpenChange, label, icon, items }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  label: string
  icon: ReactNode
  items: SourceMenuItem[]
}) {
  const [position, setPosition] = useState<{ left: number; top: number } | null>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const menu = useRef<HTMLDivElement>(null)

  useLayoutEffect(() => {
    if (!open) {
      setPosition(null)
      return
    }
    const place = () => {
      if (!trigger.current || !menu.current) return
      setPosition(sourceMenuPosition(
        trigger.current.getBoundingClientRect(),
        menu.current.getBoundingClientRect(),
        { width: document.documentElement.clientWidth, height: window.innerHeight },
      ))
    }
    // 点菜单和点按钮之外的地方都关掉；用捕获阶段，免得被内层 stopPropagation 拦掉。
    const dismiss = (event: PointerEvent) => {
      if (event.target instanceof Node && !trigger.current?.contains(event.target) && !menu.current?.contains(event.target)) onOpenChange(false)
    }
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onOpenChange(false)
    }
    place()
    // 页面滚动 / 窗口缩放 / 按钮或菜单自身尺寸变化都要重新定位，
    // 否则菜单会停在旧坐标上（它已经不在页头的定位上下文里了）。
    const observer = new ResizeObserver(place)
    if (trigger.current) observer.observe(trigger.current)
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
  }, [open, onOpenChange])

  return <div className="source-menu">
    <button ref={trigger} className="secondary-button" type="button" aria-haspopup="menu" aria-expanded={open}
      onClick={() => onOpenChange(!open)}>{icon}{label}</button>
    {open && createPortal(
      <div ref={menu} className="source-menu-popover" role="menu"
        style={{ ...position, visibility: position ? 'visible' : 'hidden' }}>
        {items.map((item) => <button key={item.label} type="button" role="menuitem"
          onClick={() => { onOpenChange(false); item.onSelect() }}>{item.icon}{item.label}</button>)}
      </div>,
      document.body,
    )}
  </div>
}
