import { useRef, type ReactNode } from 'react'
import AnchoredPopover from './AnchoredPopover'

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
 * 下面直接被页头底边截断）。portal 到 body 就没有这个裁切上下文了 ——
 * 具体机制与判断依据见 `AnchoredPopover`。
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
  const trigger = useRef<HTMLButtonElement>(null)

  return <div className="source-menu">
    <button ref={trigger} className="secondary-button" type="button" aria-haspopup="menu" aria-expanded={open}
      onClick={() => onOpenChange(!open)}>{icon}{label}</button>
    <AnchoredPopover open={open} onOpenChange={onOpenChange} anchorRef={trigger}
      className="source-menu-popover" role="menu">
      {items.map((item) => <button key={item.label} type="button" role="menuitem"
        onClick={() => { onOpenChange(false); item.onSelect() }}>{item.icon}{item.label}</button>)}
    </AnchoredPopover>
  </div>
}
