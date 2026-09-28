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
  root.style.setProperty('--app-bg-image', backgroundImageValue(customization.backgroundImage))
  root.style.setProperty('--app-bg-dim', `${clampNumber(customization.backgroundDim, backgroundDimRange.min, backgroundDimRange.max, 0)}%`)
  root.style.setProperty('--app-bg-size', fit.size)
  root.style.setProperty('--app-bg-repeat', fit.repeat)
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
