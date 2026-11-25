import { test } from 'node:test'
import assert from 'node:assert/strict'
import { defaultThemeCustomization, readThemeCustomization } from './theme.ts'

function withSavedAppearance(appearance: unknown, check: () => void) {
  const previous = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const values = new Map([['pivotflow_appearance', JSON.stringify(appearance)]])
  Object.defineProperty(globalThis, 'localStorage', {
    configurable: true,
    value: { getItem: (key: string) => values.get(key) ?? null },
  })
  try { check() } finally {
    if (previous) Object.defineProperty(globalThis, 'localStorage', previous)
    else Reflect.deleteProperty(globalThis, 'localStorage')
  }
}

test('saved sand, ice, rose and ink palettes survive a new page session', () => {
  for (const preset of ['sand', 'ice', 'rose', 'ink']) {
    withSavedAppearance({ preset, preference: 'dark', radius: 'soft', surfaceOpacity: 78 }, () => {
      const appearance = readThemeCustomization()
      assert.equal(appearance.preset, preset)
      assert.equal(appearance.preference, 'dark')
      assert.equal(appearance.radius, 'soft')
      assert.equal(appearance.surfaceOpacity, 78)
    })
  }
})

test('saved SimHei, FangSong and Aptos fonts survive a new page session', () => {
  for (const font of ['simhei', 'fangsong', 'aptos']) {
    withSavedAppearance({ font, preset: 'anthropic', contentWidth: 'full' }, () => {
      const appearance = readThemeCustomization()
      assert.equal(appearance.font, font)
      assert.equal(appearance.preset, 'anthropic')
      assert.equal(appearance.contentWidth, 'full')
    })
  }
})

test('unknown preferences recover to the existing defaults and retain valid layout preferences', () => {
  withSavedAppearance({ font: 'removed-font', preset: 'removed-palette', sidebarWidth: 'wide', motion: 'reduced' }, () => {
    const appearance = readThemeCustomization()
    assert.equal(appearance.font, defaultThemeCustomization.font)
    assert.equal(appearance.preset, defaultThemeCustomization.preset)
    assert.equal(appearance.sidebarWidth, 'wide')
    assert.equal(appearance.motion, 'reduced')
  })
})
