// 兄弟模块**必须带 `.ts` 后缀**：`npm test` 是 `node --test "src/**/*.test.ts"`，
// 而 `themeCustomization.test.ts` 会 import 本文件 —— Node 的 ESM 解析器不补后缀，
// 少一个后缀整条 import 链就 ERR_MODULE_NOT_FOUND。这也是 `tsconfig.app.json` 里
// 打开 `allowImportingTsExtensions` 的原因（它只在 `noEmit` 下合法，本项目正是 noEmit）。
import { refreshBareTextTone, startBareTextToneWatch } from './wallpaperTone.ts'

export type ThemePreference = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'
// 预设清单是**唯一真源**：类型、校验、以及 index.html 里那段在 bundle 之前
// 跑的内联脚本都从它派生。内联脚本没法 import（它必须先于模块执行），
// 所以只能把字面量抄一份过去，由 themeBoot.test.ts 盯着两边不许漂移。
export const themePresetOptions = ['jade', 'ocean', 'coral', 'anthropic', 'violet', 'slate', 'forest', 'plum'] as const
export type ThemePreset = typeof themePresetOptions[number]
// 字体只列 Windows / macOS 预装的常用款：项目不加载 webfont，
// 写进未安装的字体只会静默回落，让选项之间看不出差别。
export const themeFontOptions = [
  { value: 'system', label: '系统默认', note: '跟随操作系统' },
  { value: 'yahei', label: '微软雅黑', note: 'Windows 常用黑体，字面大' },
  { value: 'pingfang', label: '苹方 / 思源黑', note: '字重均匀，偏现代' },
  { value: 'dengxian', label: '等线', note: '笔画清瘦，留白多' },
  { value: 'harmony', label: '鸿蒙黑体', note: '中性几何，比雅黑更瘦' },
  { value: 'notoserif', label: '思源宋体', note: '现代衬线，长文阅读舒适' },
  { value: 'songti', label: '宋体', note: '传统衬线，笔锋明显' },
  { value: 'kaiti', label: '楷体', note: '书写感，适合小字号少量文本' },
  { value: 'segoe', label: 'Segoe UI', note: '西文紧凑，数字清晰' },
  { value: 'consolas', label: 'Consolas', note: '等宽，数字逐列对齐' },
  { value: 'cascadia', label: 'Cascadia Mono', note: '等宽，字形比 Consolas 圆润' },
  { value: 'sarasa', label: '更纱等宽', note: '等宽且中文对齐，需自行安装' },
] as const

export type ThemeFont = typeof themeFontOptions[number]['value']
export const themeRadiusOptions = ['compact', 'balanced', 'soft'] as const
export type ThemeRadius = typeof themeRadiusOptions[number]

// 侧栏宽度只在桌面宽度生效：880px 以下侧栏变成覆盖层，那边 `--sidebar-width`
// 被强制为 0（styles.css 的 `@media (max-width: 880px)`），不能被这里盖掉。
export const themeSidebarWidthOptions = ['narrow', 'default', 'wide'] as const
export type ThemeSidebarWidth = typeof themeSidebarWidthOptions[number]
// `full` = 不设上限（`width: 100%`），`default` = 沿用原本的 1920px。
export const themeContentWidthOptions = ['narrow', 'default', 'full'] as const
export type ThemeContentWidth = typeof themeContentWidthOptions[number]
export const themeMotionOptions = ['full', 'reduced'] as const
export type ThemeMotion = typeof themeMotionOptions[number]

/** 壁纸的铺法。`cover` 铺满裁切、`contain` 完整显示、`tile` 平铺重复。 */
export const themeBackgroundFitOptions = ['cover', 'contain', 'tile'] as const
export type ThemeBackgroundFit = typeof themeBackgroundFitOptions[number]
/** 一个枚举驱动两个 CSS 属性，避免在样式表里把整串图层列表按铺法抄三遍。 */
const backgroundFitValues: Record<ThemeBackgroundFit, { size: string; repeat: string }> = {
  cover: { size: 'cover', repeat: 'no-repeat' },
  contain: { size: 'contain', repeat: 'no-repeat' },
  tile: { size: 'auto', repeat: 'repeat' },
}

/** 壁纸暗化的取值范围（百分比）。界面滑块与读盘时的夹取共用，避免两边写岔。 */
export const backgroundDimRange = { min: 0, max: 90 } as const

/**
 * 面板「表面」不透明度的取值范围（百分比）。
 *
 * 100 = 完全不透明，观感与引入毛玻璃之前逐像素一致（默认值）；
 * 调低后 `.sidebar` / 面板 / 卡片 / 表头会透出壁纸，配合 `surfaceBlur` 形成毛玻璃。
 * 下限取 40 而不是 0：再低文字就压不住壁纸了，与其给一个必然不可读的档位，不如不给。
 */
export const surfaceOpacityRange = { min: 40, max: 100 } as const
/** 面板毛玻璃的模糊半径（px）。14 是改造前写在样式表里的固定值，所以它是默认值。 */
export const surfaceBlurRange = { min: 0, max: 40 } as const
/** 界面「一键毛玻璃」按钮用的推荐值：能透出壁纸，文字仍清楚。 */
export const glassPresetValues = { surfaceOpacity: 78, surfaceBlur: 18 } as const

export interface ThemeCustomization {
  preference: ThemePreference
  preset: ThemePreset
  font: ThemeFont
  radius: ThemeRadius
  /** 壁纸图片地址。空串 = 不用壁纸。只接受 http(s)，见 `backgroundImageValue`。 */
  backgroundImage: string
  /** 用当前主题的底色把壁纸压暗多少（百分比），保证前景文字仍可读。 */
  backgroundDim: number
  backgroundFit: ThemeBackgroundFit
  /**
   * 壁纸地址是「随机图源」：每次打开页面追加一个随机参数，绕过浏览器缓存拿到新图。
   * 适合 `https://t.alcy.cc/fj` 这类每次请求都返回不同图片的接口。
   */
  backgroundRandom: boolean
  /** 面板 / 卡片 / 表头的「表面」不透明度（百分比）。100 = 实心。 */
  surfaceOpacity: number
  /** 面板毛玻璃的模糊半径（px）。0 = 不模糊。 */
  surfaceBlur: number
  sidebarWidth: ThemeSidebarWidth
  contentWidth: ThemeContentWidth
  motion: ThemeMotion
  /** 自由 CSS。只在当前浏览器生效，不同步服务端。 */
  customCss: string
  customCssEnabled: boolean
}

export const defaultThemeCustomization: ThemeCustomization = {
  preference: 'system',
  preset: 'jade',
  font: 'system',
  radius: 'balanced',
  backgroundImage: '',
  backgroundDim: 0,
  backgroundFit: 'cover',
  backgroundRandom: false,
  surfaceOpacity: 100,
  surfaceBlur: 14,
  sidebarWidth: 'default',
  contentWidth: 'default',
  motion: 'full',
  customCss: '',
  customCssEnabled: false,
}

const themeKey = 'pivotflow_theme'
const appearanceKey = 'pivotflow_appearance'
const legacyThemeKey = 'fusion_theme'
const customCssElementId = 'pivotflow-custom-css'

function isThemePreference(value: unknown): value is ThemePreference {
  return value === 'light' || value === 'dark' || value === 'system'
}

function isThemePreset(value: unknown): value is ThemePreset {
  return themePresetOptions.some((preset) => preset === value)
}

function isThemeFont(value: unknown): value is ThemeFont {
  return themeFontOptions.some((option) => option.value === value)
}

function isThemeRadius(value: unknown): value is ThemeRadius {
  return themeRadiusOptions.some((radius) => radius === value)
}

function isOneOf<T extends string>(options: readonly T[], value: unknown): value is T {
  return options.some((option) => option === value)
}

export function clampNumber(value: unknown, min: number, max: number, fallback: number): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) return fallback
  return Math.min(max, Math.max(min, value))
}

/**
 * 侧栏用的不透明度：比面板再透一档（×0.85），但不低于 62%。
 *
 * 侧栏在最外层，透一点才看得出壁纸；但导航文字必须读得清，所以要有下限。
 * 这段算术放 JS 而不是 CSS 的 `clamp()` / `calc()`：能被单元测试盯住，
 * 也不用赌浏览器对 `color-mix()` 里嵌 math 函数的支持。
 */
export function sidebarAlphaValue(surfaceAlpha: number): number {
  return Math.max(62, Math.round(surfaceAlpha * 0.85))
}

/**
 * 把用户填的图片地址转成可以安全写进 CSS 的 `url(...)` 值。
 *
 * **引号和反斜杠必须转义** —— 否则 `https://a/x.png") , url("https://evil/y.png`
 * 这种输入能提前闭合 `url("...")`，把任意声明注入进样式表。
 * 只放行 http(s)：`javascript:` / `data:` 在这里没有正当用途，不留这个口子。
 */
export function backgroundImageValue(url: string): string {
  const trimmed = url.trim()
  if (!trimmed || !/^https?:\/\//i.test(trimmed)) return 'none'
  return `url("${trimmed.replace(/[\\"]/g, '\\$&')}")`
}

/**
 * 壁纸是否**真的**生效。
 *
 * 空串和非 http(s) 的地址都会被 `backgroundImageValue` 变成 `none`，此时界面上
 * 没有任何壁纸 —— 所以「点阵要不要去掉」「文字要不要加对比度」这类判断必须看这个，
 * 不能直接看 `backgroundImage` 非空：用户在地址框里打了个半截的 `ht` 也会让它非空。
 */
export function wallpaperActive(url: string): boolean {
  return backgroundImageValue(url) !== 'none'
}

/**
 * 每次**页面加载**生成一次、之后整个会话内不变。
 *
 * 必须是模块级常量而不是每次调用现算：`applyThemeCustomization` 会在用户每改一项
 * 外观设置时重跑，如果每次现算，改一下字体就会换一张壁纸 —— 那是 bug 不是功能。
 * 想主动换图有 `rerollWallpaper()`。
 */
let wallpaperNonce = createWallpaperNonce()

function createWallpaperNonce(): string {
  return Math.random().toString(36).slice(2, 10)
}

/**
 * 给壁纸地址加上本次会话的随机参数，绕过浏览器对同一 URL 的缓存。
 *
 * 随机图源（如 `https://t.alcy.cc/fj`）每次请求都返回不同图片，但浏览器会按 URL 缓存，
 * 不加这个参数就永远是第一次那张。`random` 为 false 时原样返回，不加任何东西 ——
 * 普通图床的固定图片不该因为多一个参数就重新下载。
 */
export function wallpaperUrl(url: string, random: boolean): string {
  const trimmed = url.trim()
  if (!random || !trimmed) return trimmed
  return `${trimmed}${trimmed.includes('?') ? '&' : '?'}pf_r=${wallpaperNonce}`
}

/** 「换一张」：重新生成随机参数并立刻重应用，用于随机图源。 */
export function rerollWallpaper(): ThemeCustomization {
  wallpaperNonce = createWallpaperNonce()
  const current = readThemeCustomization()
  applyThemeCustomization(current)
  return current
}

/**
 * 自由 CSS 的逃生开关。
 *
 * 这段 CSS 是用户自己写的，写坏了可能让设置页都点不进去 —— 那样就再也没有
 * 入口把它关掉。所以必须有一条**不依赖界面**的退路：地址里带上 `plain=1`
 * 就跳过注入（`?plain=1` 放在 `#` 前面；也认 `#/system?plain=1` 这种写法）。
 */
export function shouldSkipCustomCss(search: string, hash: string): boolean {
  const fromSearch = new URLSearchParams(search).get('plain')
  if (fromSearch === '1' || fromSearch === 'true') return true
  return /(?:^|[?&])plain=(?:1|true)(?:&|$)/.test(hash)
}

function readStoredAppearance(): Partial<ThemeCustomization> {
  try {
    const stored = localStorage.getItem(appearanceKey)
    if (!stored) return {}
    const parsed: unknown = JSON.parse(stored)
    return parsed && typeof parsed === 'object' ? parsed as Partial<ThemeCustomization> : {}
  } catch {
    return {}
  }
}

export function readThemePreference(): ThemePreference {
  const stored = localStorage.getItem(themeKey)
  if (isThemePreference(stored)) return stored
  const appearance = readStoredAppearance()
  if (isThemePreference(appearance.preference)) return appearance.preference
  const legacy = localStorage.getItem(legacyThemeKey)
  return isThemePreference(legacy) ? legacy : defaultThemeCustomization.preference
}

export function readThemeCustomization(): ThemeCustomization {
  const stored = readStoredAppearance()
  return {
    preference: readThemePreference(),
    preset: isThemePreset(stored.preset) ? stored.preset : defaultThemeCustomization.preset,
    font: isThemeFont(stored.font) ? stored.font : defaultThemeCustomization.font,
    radius: isThemeRadius(stored.radius) ? stored.radius : defaultThemeCustomization.radius,
    backgroundImage: typeof stored.backgroundImage === 'string' ? stored.backgroundImage : defaultThemeCustomization.backgroundImage,
    backgroundDim: clampNumber(stored.backgroundDim, backgroundDimRange.min, backgroundDimRange.max, defaultThemeCustomization.backgroundDim),
    backgroundFit: isOneOf(themeBackgroundFitOptions, stored.backgroundFit) ? stored.backgroundFit : defaultThemeCustomization.backgroundFit,
    backgroundRandom: stored.backgroundRandom === true,
    surfaceOpacity: clampNumber(stored.surfaceOpacity, surfaceOpacityRange.min, surfaceOpacityRange.max, defaultThemeCustomization.surfaceOpacity),
    surfaceBlur: clampNumber(stored.surfaceBlur, surfaceBlurRange.min, surfaceBlurRange.max, defaultThemeCustomization.surfaceBlur),
    sidebarWidth: isOneOf(themeSidebarWidthOptions, stored.sidebarWidth) ? stored.sidebarWidth : defaultThemeCustomization.sidebarWidth,
    contentWidth: isOneOf(themeContentWidthOptions, stored.contentWidth) ? stored.contentWidth : defaultThemeCustomization.contentWidth,
    motion: isOneOf(themeMotionOptions, stored.motion) ? stored.motion : defaultThemeCustomization.motion,
    customCss: typeof stored.customCss === 'string' ? stored.customCss : defaultThemeCustomization.customCss,
    customCssEnabled: stored.customCssEnabled === true,
  }
}

export function resolveTheme(preference: ThemePreference): ResolvedTheme {
  if (preference !== 'system') return preference
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

/**
 * 把自由 CSS 写进 `<head>` 末尾的一个 `<style>`。
 *
 * 放在最后是为了让它能盖住打包进来的样式表（同权重时后者胜）。
 * 用 `textContent` 而不是 `innerHTML`：样式表内容是纯文本，
 * 没必要让它经过一遍 HTML 解析。
 */
function applyCustomCss(css: string, enabled: boolean): void {
  const existing = document.getElementById(customCssElementId)
  const active = enabled && css.trim().length > 0 && !shouldSkipCustomCss(location.search, location.hash)
  if (!active) {
    existing?.remove()
    return
  }
  const style = existing instanceof HTMLStyleElement ? existing : document.createElement('style')
  style.id = customCssElementId
  if (style.textContent !== css) style.textContent = css
  if (!style.isConnected) document.head.append(style)
}

export function applyThemeCustomization(customization: ThemeCustomization): ResolvedTheme {
  const resolved = resolveTheme(customization.preference)
  const root = document.documentElement
  root.dataset.theme = resolved
  root.dataset.themePreset = customization.preset
  root.dataset.themeFont = customization.font
  root.dataset.themeRadius = customization.radius
  root.dataset.sidebarWidth = customization.sidebarWidth
  root.dataset.contentWidth = customization.contentWidth
  root.dataset.motion = customization.motion
  // 壁纸与它的两个参数走 CSS 变量，由 `.app-shell` 的背景图层消费。
  // 没设壁纸时 `--app-bg-image` 是 `none`，那一层完全透明，观感与改动前一致。
  const fit = backgroundFitValues[customization.backgroundFit] ?? backgroundFitValues.cover
  // 原始地址（含随机参数）单独留一份：小字自适应要拿它去后端取图，
  // 而 `backgroundImageValue` 交出来的是包好的 `url("...")`，当不了接口参数。
  const wallpaperSource = wallpaperUrl(customization.backgroundImage, customization.backgroundRandom)
  const wallpaper = backgroundImageValue(wallpaperSource)
  const hasWallpaper = wallpaper !== 'none'
  root.style.setProperty('--app-bg-image', wallpaper)
  root.style.setProperty('--app-bg-dim', `${clampNumber(customization.backgroundDim, backgroundDimRange.min, backgroundDimRange.max, 0)}%`)
  root.style.setProperty('--app-bg-size', fit.size)
  root.style.setProperty('--app-bg-repeat', fit.repeat)
  // 点阵纹理整层去掉。它是给纯色底做「织理」的，压在照片上就只剩噪点 ——
  // 而且它排在壁纸**上面**（背景列表里靠前 = 更靠上），所以必须真的移除，
  // 调低浓度只是让它从「很明显」变成「有点明显」。
  // 用 `removeProperty` 而不是写回默认值：默认值本身依赖亮/暗主题（`--border-strong`
  // 在两套里亮度方向相反，见 styles.css），写死一个值就会在另一套主题下变错。
  if (hasWallpaper) root.style.setProperty('--app-bg-dots', 'none')
  else root.style.removeProperty('--app-bg-dots')
  // 壁纸下要把文字压得更实（styles.css 末尾 `data-wallpaper="on"` 那段）。
  root.dataset.wallpaper = hasWallpaper ? 'on' : 'off'
  // 页级裸文本（页脚 / 分页）底下是**壁纸本身**而不是面板底，所以字色要按那一块的
  // 实际明暗现算 —— 全站只有这三处没有面板兜着。这是唯一「不加框、不加描边」
  // 还能让文字在任意照片上可读的手段，理由与被否掉的五档见 wallpaperTone.ts。
  // 异步且失败即退回默认字色，不会因为取图失败把已经能看的页面弄坏。
  startBareTextToneWatch()
  void refreshBareTextTone(
    hasWallpaper
      ? {
          url: wallpaperSource,
          fit: customization.backgroundFit,
          dim: clampNumber(customization.backgroundDim, backgroundDimRange.min, backgroundDimRange.max, 0),
        }
      : null,
  )
  // 面板毛玻璃：`--surface-alpha` 被样式表里那几条 `:root[data-glass="on"]` 规则用来
  // 把 `--surface*` / `--sidebar` 重新算成带透明度的版本；`--glass-blur` 驱动面板的
  // `backdrop-filter`。默认 100% / 14px，此时 glass 是 off，样式表那条规则不匹配，
  // 四个令牌保持原样 —— 观感与引入毛玻璃之前逐像素一致。
  const surfaceAlpha = clampNumber(customization.surfaceOpacity, surfaceOpacityRange.min, surfaceOpacityRange.max, 100)
  root.style.setProperty('--surface-alpha', `${surfaceAlpha}%`)
  root.style.setProperty('--sidebar-alpha', `${sidebarAlphaValue(surfaceAlpha)}%`)
  root.style.setProperty('--glass-blur', `${clampNumber(customization.surfaceBlur, surfaceBlurRange.min, surfaceBlurRange.max, 14)}px`)
  root.dataset.glass = surfaceAlpha < 100 ? 'on' : 'off'
  applyCustomCss(customization.customCss, customization.customCssEnabled)
  localStorage.setItem(themeKey, customization.preference)
  localStorage.setItem(appearanceKey, JSON.stringify(customization))
  return resolved
}

export function applyTheme(preference: ThemePreference): ResolvedTheme {
  return applyThemeCustomization({ ...readThemeCustomization(), preference })
}

export function resetThemeCustomization(): ThemeCustomization {
  const defaults = { ...defaultThemeCustomization }
  applyThemeCustomization(defaults)
  return defaults
}
