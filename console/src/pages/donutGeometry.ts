// 环形图切片几何。
//
// 不要退回成「一个整圆 + stroke-dasharray 切段」的写法：Chromium 在闭合圆的接缝处
// 会把 dash 末端裁成一条斜切口，十二点方向会露出一块白色缺口（渠道用量/模型用量
// 饼图上肉眼可见）。显式圆弧路径让相邻切片共用同一个端点，接缝严丝合缝。

const TAU = Math.PI * 2

function slicePoint(share: number, radius: number, center: number): [number, number] {
  const angle = share * TAU
  return [center + radius * Math.cos(angle), center + radius * Math.sin(angle)]
}

function round(value: number): string {
  return value.toFixed(3)
}

// 角度以三点钟方向为 0、顺时针增长；调用方统一用 rotate(-90deg) 把起点转到十二点。
export function donutSlicePath(startShare: number, sweepShare: number, radius: number, center = 50): string {
  const sweep = Math.min(1, Math.max(0, sweepShare))
  const [x1, y1] = slicePoint(startShare, radius, center)
  if (sweep <= 0) return `M ${round(x1)} ${round(y1)}`
  if (sweep >= 1 - 1e-9) {
    // 整圆无法用一条弧从起点回到起点，拆成两段半圆。
    const [mx, my] = slicePoint(startShare + 0.5, radius, center)
    return `M ${round(x1)} ${round(y1)} A ${radius} ${radius} 0 1 1 ${round(mx)} ${round(my)} A ${radius} ${radius} 0 1 1 ${round(x1)} ${round(y1)}`
  }
  const [x2, y2] = slicePoint(startShare + sweep, radius, center)
  return `M ${round(x1)} ${round(y1)} A ${radius} ${radius} 0 ${sweep > 0.5 ? 1 : 0} 1 ${round(x2)} ${round(y2)}`
}

// 把占比序列累加成一圈切片，端点用累加值算出，保证前一段终点与后一段起点字符级一致。
export function donutSlices<T>(items: T[], share: (item: T) => number): Array<{ item: T; start: number; share: number }> {
  let consumed = 0
  return items.map((item) => {
    const raw = share(item)
    // 占比缺失时按 0 处理，否则 NaN 会渗进 path 的 d 属性，整段弧直接不渲染。
    const value = Number.isFinite(raw) ? Math.max(0, raw) : 0
    const slice = { item, start: consumed, share: value }
    consumed += value
    return slice
  })
}
