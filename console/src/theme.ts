export type ThemePreference = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'
export type ThemePreset = 'jade' | 'ocean' | 'coral' | 'anthropic' | 'violet' | 'slate' | 'forest' | 'plum'
// 字体只列 Windows / macOS 预装的常用款：项目不加载 webfont，
// 写进未安装的字体只会静默回落，让选项之间看不出差别。
export const themeFontOptions = [
  { value: 'system', label: '系统默认', note: '跟随操作系统' },
  { value: 'yahei', label: '微软雅黑', note: 'Windows 常用黑体' },
  { value: 'pingfang', label: '苹方 / 思源黑', note: '字重均匀，偏现代' },
  { value: 'dengxian', label: '等线', note: '笔画细，偏清瘦' },
  { value: 'songti', label: '宋体', note: '衬线，阅读感强' },
  { value: 'kaiti', label: '楷体', note: '书写感，适合小字号少量文本' },
  { value: 'inter', label: '西文优先', note: '拉丁与数字紧凑清晰' },
  { value: 'mono', label: '等宽', note: '数字逐列对齐，适合读日志' },
] as const

export type ThemeFont = typeof themeFontOptions[number]['value']
export type ThemeRadius = 'compact' | 'balanced' | 'soft'

export interface ThemeCustomization {
  preference: ThemePreference
  preset: ThemePreset
  font: ThemeFont
  radius: ThemeRadius
}

export const defaultThemeCustomization: ThemeCustomization = {
  preference: 'system',
  preset: 'jade',
  font: 'system',
  radius: 'balanced',
}

const themeKey = 'pivotflow_theme'
const appearanceKey = 'pivotflow_appearance'
const legacyThemeKey = 'fusion_theme'

function isThemePreference(value: unknown): value is ThemePreference {
  return value === 'light' || value === 'dark' || value === 'system'
}

function isThemePreset(value: unknown): value is ThemePreset {
  return value === 'jade' || value === 'ocean' || value === 'coral' || value === 'anthropic' ||
    value === 'violet' || value === 'slate' || value === 'forest' || value === 'plum'
}

function isThemeFont(value: unknown): value is ThemeFont {
  return themeFontOptions.some((option) => option.value === value)
}

function isThemeRadius(value: unknown): value is ThemeRadius {
  return value === 'compact' || value === 'balanced' || value === 'soft'
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
  }
}

export function resolveTheme(preference: ThemePreference): ResolvedTheme {
  if (preference !== 'system') return preference
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export function applyThemeCustomization(customization: ThemeCustomization): ResolvedTheme {
  const resolved = resolveTheme(customization.preference)
  const root = document.documentElement
  root.dataset.theme = resolved
  root.dataset.themePreset = customization.preset
  root.dataset.themeFont = customization.font
  root.dataset.themeRadius = customization.radius
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
