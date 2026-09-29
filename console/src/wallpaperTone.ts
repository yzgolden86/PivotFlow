/**
 * 小字按壁纸的明暗自适应 —— 只改文字颜色，不往文字上叠任何东西。
 *
 * ── 要解决什么 ────────────────────────────────────────────────────────
 * 玻璃面板里的小字都有面板底可依（hao哥 那张壁纸上实测 3.98~4.26:1，能看），
 * **只有页脚和分页是裸文本**，直接压在壁纸上。那张壁纸底部是纯黑 `rgb(0,0,0)`，
 * 而亮色档的小字令牌是近黑 `#1a231f` → 实测 **1.03:1**，肉眼消失。
 *
 * ── 为什么是「改颜色」而不是「加东西」────────────────────────────────
 * 2026-09-29 之前试过并被否掉五档：软阴影 / 硬边+辉光 / 硬边零辉光 /
 * 整行垫底 / 贴合垫片。全部是「给文字加东西」，hao哥 看到实图后的原话是
 * **「加了一个背景框，看起来好丑啊」**。所以只剩**只改文字颜色**这一条路。
 *
 * 但同一屏里上半部要深字、下半部要浅字，任何单一令牌都无解
 * （实测：把「压暗」拉到 60% 才能把页脚顶到 5.3:1，那等于把壁纸整体洗掉）。
 * 于是必须知道**每一处文字底下到底是深还是浅** —— 也就是必须读到壁纸像素。
 *
 * ── 像素从哪来 ────────────────────────────────────────────────────────
 * 浏览器读像素只有 canvas 一条路，而它要求图片带 `Access-Control-Allow-Origin`。
 * 实测常见图床并不可靠（`api.dujin.org` 不发、`images.unsplash.com` 发），
 * 纯前端方案会对一部分用户**静默失效**。所以走 `/admin/appearance/wallpaper-bytes`
 * 让后端取回来 —— 同源，没有污染问题。详见 `internal/app/admin_wallpaper.go`。
 *
 * ── 几何怎么对齐 ──────────────────────────────────────────────────────
 * 壁纸层是 `background-attachment: fixed`，钉在**视口**上（见 styles.css 的
 * `.app-shell`）。所以只要把图画进一张「视口大小」的画布、用和 CSS 同一套
 * cover/contain/tile + center 规则铺开，画布坐标就等于视口坐标，
 * 元素的 `getBoundingClientRect()` 可以直接拿来取样，不用换算。
 * 铺图几何与取样在 `wallpaperToneMath.ts` 里，那半边是纯函数、有守卫。
 *
 * ── 失败时怎么办 ──────────────────────────────────────────────────────
 * 取不到图 / 解不开 / 采样不出来 → **什么都不设**，退回改动前的行为。
 * 这条很重要：这个模块只能让情况变好，绝不能因为网络问题把已经能看的页面弄坏。
 */

import {
  blendToward,
  meanLuminance,
  parseColor,
  SAMPLE_SCALE,
  sampleBoxInViewport,
  toneForLuminance,
  wallpaperDrawRects,
} from './wallpaperToneMath.ts'

/** 挂在元素上的属性名。样式表末尾按它切深/浅两套小字令牌。 */
const TONE_ATTR = 'data-bare-text-tone'

/** 重新采样的防抖间隔（ms）。 */
const RESAMPLE_DEBOUNCE_MS = 350

export interface WallpaperToneInput {
  /** 已解析好的壁纸地址（含随机参数）。空串 = 不用壁纸。 */
  url: string
  fit: 'cover' | 'contain' | 'tile'
  /** 壁纸暗化百分比，0~90。暗化层是 `--bg` 的半透明覆盖，会整体提亮深底。 */
  dim: number
}

interface Backdrop {
  data: Uint8ClampedArray
  width: number
  height: number
}

let cachedBitmap: ImageBitmap | null = null
let cachedBitmapKey = ''
let lastInput: WallpaperToneInput | null = null
let running = false
let rerunQueued = false

/** 页面级「裸文本」元素 —— 全站只有这三处没有面板底。 */
function bareTextTargets(): HTMLElement[] {
  const found: HTMLElement[] = []
  const collect = (selector: string) => {
    for (const el of document.querySelectorAll(selector)) {
      if (el instanceof HTMLElement) found.push(el)
    }
  }
  collect('.dashboard-footer')
  collect('.pagination-meta > span')
  collect('.pagination > div:last-child > strong')
  return found
}

export function clearBareTextTone(): void {
  for (const el of bareTextTargets()) el.removeAttribute(TONE_ATTR)
}

async function loadBitmap(url: string): Promise<ImageBitmap | null> {
  if (cachedBitmap && cachedBitmapKey === url) return cachedBitmap
  const token = localStorage.getItem('pivotflow_token') ?? ''
  try {
    const response = await fetch(
      `/admin/appearance/wallpaper-bytes?url=${encodeURIComponent(url)}`,
      { headers: token ? { Authorization: `Bearer ${token}` } : undefined },
    )
    if (!response.ok) return null
    const blob = await response.blob()
    const next = await createImageBitmap(blob)
    cachedBitmap?.close()
    cachedBitmap = next
    cachedBitmapKey = url
    return next
  } catch {
    // 网络失败 / 解不开（比如上游返回了半张图）—— 交给调用方退回原行为。
    return null
  }
}

/** 按 CSS 的铺法把壁纸画进一张「视口大小 / SAMPLE_SCALE」的画布。 */
function paintBackdrop(
  image: ImageBitmap,
  fit: WallpaperToneInput['fit'],
  dim: number,
): Backdrop | null {
  const width = Math.max(1, Math.round(window.innerWidth * SAMPLE_SCALE))
  const height = Math.max(1, Math.round(window.innerHeight * SAMPLE_SCALE))
  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const ctx = canvas.getContext('2d', { willReadFrequently: true })
  if (!ctx) return null

  // 先铺黑：contain 会留边，留边处 CSS 露的是 `.app-shell` 的底色，
  // 这里用黑当「最坏情况」，宁可把留边判成深底用浅字。
  ctx.fillStyle = '#000000'
  ctx.fillRect(0, 0, width, height)

  const rects = wallpaperDrawRects(
    fit,
    image.width * SAMPLE_SCALE,
    image.height * SAMPLE_SCALE,
    width,
    height,
  )
  if (rects.length === 0) return null
  for (const rect of rects) {
    ctx.drawImage(image, rect.x, rect.y, rect.width, rect.height)
  }

  let data: Uint8ClampedArray
  try {
    data = ctx.getImageData(0, 0, width, height).data
  } catch {
    // 同源（走后端取回）不该抛；留这条是为了「万一」也不要炸掉整页。
    return null
  }

  if (dim > 0) {
    const bg = parseColor(getComputedStyle(document.documentElement).getPropertyValue('--bg'))
    if (bg) blendToward(data, bg, dim / 100)
  }

  return { data, width, height }
}

function applyTone(image: ImageBitmap, input: WallpaperToneInput): void {
  const backdrop = paintBackdrop(image, input.fit, input.dim)
  if (!backdrop) {
    clearBareTextTone()
    return
  }
  for (const el of bareTextTargets()) {
    const rect = el.getBoundingClientRect()
    if (rect.width <= 0 || rect.height <= 0) {
      el.removeAttribute(TONE_ATTR)
      continue
    }
    const box = sampleBoxInViewport(
      { left: rect.left, top: rect.top, right: rect.right, bottom: rect.bottom },
      window.innerHeight,
    )
    const luminance = meanLuminance(backdrop.data, backdrop.width, backdrop.height, box)
    if (luminance === null) {
      el.removeAttribute(TONE_ATTR)
      continue
    }
    el.setAttribute(TONE_ATTR, toneForLuminance(luminance))
  }
}

/**
 * 重新采样并给页级裸文本打上明暗标记。
 *
 * 任何时候都可以调：没有壁纸 / 取不到图 / 采样失败都会**清掉标记**（退回默认字色），
 * 不会留下上一次的半截状态。
 */
export async function refreshBareTextTone(input: WallpaperToneInput | null): Promise<void> {
  lastInput = input
  if (!input || !input.url) {
    clearBareTextTone()
    return
  }
  if (running) {
    rerunQueued = true
    return
  }
  running = true
  try {
    const image = await loadBitmap(input.url)
    if (!image) {
      clearBareTextTone()
      return
    }
    applyTone(image, input)
  } finally {
    running = false
    if (rerunQueued) {
      rerunQueued = false
      void refreshBareTextTone(lastInput)
    }
  }
}

let observer: MutationObserver | null = null
let debounceTimer: number | undefined

function scheduleResample(): void {
  window.clearTimeout(debounceTimer)
  debounceTimer = window.setTimeout(() => {
    void refreshBareTextTone(lastInput)
  }, RESAMPLE_DEBOUNCE_MS)
}

/**
 * 开始监听会改变「文字底下是什么」的事件。
 *
 * 用 `childList` 观察整棵树而不是只听 `hashchange`：页脚/分页是数据到位后才渲染出来的，
 * 路由变化之后还要等一次接口返回，只听路由会漏掉那一拍。
 * 只观察 `childList`（不看 attributes），因为我们自己写的就是 attribute，
 * 看 attributes 会自激。
 */
export function startBareTextToneWatch(): void {
  if (observer || typeof MutationObserver === 'undefined') return
  observer = new MutationObserver(scheduleResample)
  observer.observe(document.body, { childList: true, subtree: true })
  window.addEventListener('resize', scheduleResample)
}
