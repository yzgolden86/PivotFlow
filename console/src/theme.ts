export type ThemePreference = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'
export type ThemePreset = 'jade' | 'ocean' | 'coral' | 'anthropic'
export type ThemeFont = 'modern' | 'system' | 'serif'
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
  font: 'modern',
  radius: 'balanced',
}

const themeKey = 'pivotflow_theme'
const appearanceKey = 'pivotflow_appearance'
const legacyThemeKey = 'fusion_theme'

function isThemePreference(value: unknown): value is ThemePreference {
  return value === 'light' || value === 'dark' || value === 'system'
}

function isThemePreset(value: unknown): value is ThemePreset {
  return value === 'jade' || value === 'ocean' || value === 'coral' || value === 'anthropic'
}

function isThemeFont(value: unknown): value is ThemeFont {
  return value === 'modern' || value === 'system' || value === 'serif'
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
