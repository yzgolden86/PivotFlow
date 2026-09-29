/**
 * `wallpaperTone.ts` 里**不碰 DOM** 的那一半：颜色解析、亮度、铺图几何、取样。
 *
 * 拆出来的理由与项目里其它 `.ts` 兄弟模块一致：`node --test` 进不了 DOM，
 * 而这一半恰恰是最容易算错、错了还看不出来的部分 ——
 * 铺图几何算错 → 取样位置偏 → 字色判反 → 深底配深字，比不改还糟。
 * 所以几何必须有守卫，而守卫要求它是纯函数。
 */

/**
 * 相对亮度低于此值就认为「底色是深的」，该处小字改用浅色。
 *
 * 0.16 的来历：亮色档小字令牌 `#1a231f` 的相对亮度是 0.0152。要在深底上达到
 * 小字 AA（4.5:1）需要 `(L + 0.05) / (0.0152 + 0.05) >= 4.5`，即 `L >= 0.243`。
 * 但 0.243 已经是相当亮的底（约 `#8a8a8a`），拿它当阈值会把大量本来看得清的
 * 中间调也判成深底。取 0.16（约 `#6b6b6b`）让分界落在真正「看不清」的那一侧。
 */
export const DARK_BACKDROP_LUMINANCE = 0.16

/** 裸文本的两种字色档：`on-dark` = 底是深的、要用浅字；`on-light` 反之。 */
export type BareTextTone = 'on-dark' | 'on-light'

/** 判据本身单独拎出来，这样「深底配浅字」这个方向能被测到。 */
export function toneForLuminance(luminance: number): BareTextTone {
  return luminance < DARK_BACKDROP_LUMINANCE ? 'on-dark' : 'on-light'
}

/** 采样画布的缩放比。1/4 足够定位明暗，`getImageData` 快一个数量级。 */
export const SAMPLE_SCALE = 0.25

/** 一像素块。坐标系是**画布像素**（已经乘过 SAMPLE_SCALE）。 */
export interface DrawRect {
  x: number
  y: number
  width: number
  height: number
}

/** 视口坐标下的矩形（和 `getBoundingClientRect()` 同一套）。 */
export interface Box {
  left: number
  top: number
  right: number
  bottom: number
}

/** 把 `#rrggbb` / `rgb(r, g, b)` 解析成三元组；认不出来返回 null。 */
export function parseColor(value: string): [number, number, number] | null {
  const text = value.trim()
  const hex = /^#([0-9a-f]{6})$/i.exec(text)
  if (hex) {
    const n = parseInt(hex[1], 16)
    return [(n >> 16) & 255, (n >> 8) & 255, n & 255]
  }
  const rgb = /^rgba?\(\s*(\d+)[,\s]+(\d+)[,\s]+(\d+)/i.exec(text)
  if (rgb) return [Number(rgb[1]), Number(rgb[2]), Number(rgb[3])]
  return null
}

/** WCAG 相对亮度。对比度必须用它，不能用「感知亮度」。 */
export function relativeLuminance(r: number, g: number, b: number): number {
  const channel = (raw: number) => {
    const c = raw / 255
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b)
}

/**
 * 算出「要往画布上画哪几块」，用来复刻 CSS 的背景铺法。
 *
 * 必须与 `styles.css` 里 `.app-shell` 那条背景规则一致：
 * `background-position: center`、`background-attachment: fixed`，
 * `background-size` / `background-repeat` 由 `theme.ts` 的 `backgroundFitValues` 决定。
 *
 * ⚠️ `imageWidth` / `imageHeight` 要传**画布单位**（自然尺寸 × SAMPLE_SCALE），
 * 不是图片的自然尺寸 —— `tile` 的 `auto` 是「原图像素」，缩放要由调用方先乘好。
 */
export function wallpaperDrawRects(
  fit: 'cover' | 'contain' | 'tile',
  imageWidth: number,
  imageHeight: number,
  width: number,
  height: number,
): DrawRect[] {
  if (imageWidth <= 0 || imageHeight <= 0 || width <= 0 || height <= 0) return []

  if (fit === 'tile') {
    // 1×1 之类的退化图会铺出几十万块，先把上限卡住：超过就当「铺不满」，
    // 宁可不做自适应（返回空 → 调用方清掉标记），也不要卡死主线程。
    const cols = Math.ceil(width / imageWidth) + 1
    const rows = Math.ceil(height / imageHeight) + 1
    if (cols * rows > 4000) return []
    // `background-position: center` 对 repeat 同样生效：整张图先居中再向两侧铺。
    const startX = ((width - imageWidth) / 2) % imageWidth - imageWidth
    const startY = ((height - imageHeight) / 2) % imageHeight - imageHeight
    const rects: DrawRect[] = []
    for (let y = startY; y < height; y += imageHeight) {
      for (let x = startX; x < width; x += imageWidth) {
        rects.push({ x, y, width: imageWidth, height: imageHeight })
      }
    }
    return rects
  }

  const scale =
    fit === 'contain'
      ? Math.min(width / imageWidth, height / imageHeight)
      : Math.max(width / imageWidth, height / imageHeight)
  const drawWidth = imageWidth * scale
  const drawHeight = imageHeight * scale
  return [{
    x: (width - drawWidth) / 2,
    y: (height - drawHeight) / 2,
    width: drawWidth,
    height: drawHeight,
  }]
}

/**
 * 把画布像素朝某个颜色混过去，用来还原壁纸上面那层「压暗」。
 *
 * `--app-bg-dim` 的实现在 styles.css 里是一条
 * `linear-gradient(color-mix(in srgb, var(--bg) var(--dim), transparent))`，
 * 盖在壁纸上等价于每个通道按 dim 比例朝 `--bg` 混。不还原这一步，
 * 用户把「压暗」拉高时我们会误判成深底、把字改成浅色。
 *
 * 就地修改 `data`（调用方刚 `getImageData` 出来的副本，没有别名风险）。
 */
export function blendToward(
  data: Uint8ClampedArray,
  color: [number, number, number],
  alpha: number,
): void {
  const a = Math.min(1, Math.max(0, alpha))
  if (a <= 0) return
  for (let i = 0; i < data.length; i += 4) {
    data[i] = data[i] * (1 - a) + color[0] * a
    data[i + 1] = data[i + 1] * (1 - a) + color[1] * a
    data[i + 2] = data[i + 2] * (1 - a) + color[2] * a
  }
}

/**
 * 求某个视口矩形底下那一片的平均相对亮度。
 *
 * 坐标要乘 SAMPLE_SCALE 换到画布空间，并夹到画布边界内。
 * 完全落在画布外（或夹完为空）返回 null，调用方据此清掉标记。
 */
export function meanLuminance(
  data: Uint8ClampedArray,
  dataWidth: number,
  dataHeight: number,
  box: Box,
): number | null {
  const x0 = Math.max(0, Math.floor(box.left * SAMPLE_SCALE))
  const y0 = Math.max(0, Math.floor(box.top * SAMPLE_SCALE))
  const x1 = Math.min(dataWidth, Math.ceil(box.right * SAMPLE_SCALE))
  const y1 = Math.min(dataHeight, Math.ceil(box.bottom * SAMPLE_SCALE))
  if (x1 <= x0 || y1 <= y0) return null

  let sum = 0
  let count = 0
  for (let y = y0; y < y1; y += 1) {
    for (let x = x0; x < x1; x += 1) {
      const i = (y * dataWidth + x) * 4
      sum += relativeLuminance(data[i], data[i + 1], data[i + 2])
      count += 1
    }
  }
  return count > 0 ? sum / count : null
}

/**
 * 元素在折叠线以下时，它被看到的那一刻页面已经滚到底了。
 *
 * 壁纸是 `background-attachment: fixed`，不随滚动移动，所以那一刻它底下就是
 * 「视口下沿」那一块 —— 按贴住下沿取样，而不是用它当前（屏幕外）的坐标。
 */
export function sampleBoxInViewport(box: Box, viewportHeight: number): Box {
  const height = box.bottom - box.top
  if (height <= 0) return box
  const maxTop = Math.max(0, viewportHeight - height)
  const top = Math.min(Math.max(0, box.top), maxTop)
  return { left: box.left, top, right: box.right, bottom: top + height }
}
