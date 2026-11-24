import { backgroundImageValue } from './theme.ts'

export type ByteSettingKey = 'request_cache_max_bytes' | 'request_cache_max_entry_bytes'
export type ByteSettingUnit = 'KiB' | 'MiB'

type ByteSettingSpec = {
  unit: ByteSettingUnit
  factor: number
  minBytes: number
  maxBytes: number
}

const KIB = 1_024
const MIB = 1_048_576

const BYTE_SETTINGS: Record<ByteSettingKey, ByteSettingSpec> = {
  request_cache_max_bytes: {
    unit: 'MiB',
    factor: MIB,
    minBytes: MIB,
    maxBytes: 128 * MIB,
  },
  request_cache_max_entry_bytes: {
    unit: 'KiB',
    factor: KIB,
    minBytes: 4 * KIB,
    maxBytes: MIB,
  },
}

export type ByteSettingParseResult =
  | { ok: true; bytes: number }
  | { ok: false; error: string }

export type WallpaperValidationResult =
  | { ok: true; value: string; action: 'apply' | 'clear' }
  | { ok: false; error: string }

export function isByteSettingKey(key: string): key is ByteSettingKey {
  return Object.hasOwn(BYTE_SETTINGS, key)
}

export function byteSettingUnit(key: ByteSettingKey): ByteSettingUnit {
  return BYTE_SETTINGS[key].unit
}

export function formatByteSettingValue(key: ByteSettingKey, value: string): string {
  const bytes = Number(value)
  if (!Number.isSafeInteger(bytes) || bytes < 0) return value

  const spec = BYTE_SETTINGS[key]
  const factor = BigInt(spec.factor)
  let remainder = BigInt(bytes)
  const whole = remainder / factor
  remainder %= factor
  if (remainder === 0n) return whole.toString()

  let fraction = ''
  while (remainder > 0n && fraction.length < 20) {
    remainder *= 10n
    fraction += (remainder / factor).toString()
    remainder %= factor
  }
  return `${whole}.${fraction.replace(/0+$/, '')}`
}

export function parseByteSettingValue(key: ByteSettingKey, input: string): ByteSettingParseResult {
  const spec = BYTE_SETTINGS[key]
  const trimmed = input.trim()
  if (!trimmed) return { ok: false, error: `请输入容量（${spec.unit}）。` }
  if (trimmed.length > 64) return { ok: false, error: '容量数值过长。' }

  const match = /^(?:(\d+)(?:\.(\d*))?|\.(\d+))$/.exec(trimmed)
  if (!match) return { ok: false, error: `请输入有效的 ${spec.unit} 容量数值（仅支持普通十进制）。` }

  const whole = match[1] || '0'
  const fraction = match[2] ?? match[3] ?? ''
  const numerator = BigInt(whole + fraction) * BigInt(spec.factor)
  const denominator = 10n ** BigInt(fraction.length)
  if (numerator % denominator !== 0n) {
    return { ok: false, error: '该数值换算后不是整字节，请减少小数位；系统不会自动舍入。' }
  }

  const bytes = numerator / denominator
  if (bytes < BigInt(spec.minBytes) || bytes > BigInt(spec.maxBytes)) {
    return {
      ok: false,
      error: `容量需在 ${formatByteSettingValue(key, String(spec.minBytes))} ${spec.unit} 到 ${formatByteSettingValue(key, String(spec.maxBytes))} ${spec.unit} 之间。`,
    }
  }
  if (bytes > BigInt(Number.MAX_SAFE_INTEGER)) {
    return { ok: false, error: '容量超出安全整数范围。' }
  }
  return { ok: true, bytes: Number(bytes) }
}

export function validateWallpaperInput(input: string): WallpaperValidationResult {
  const value = input.trim()
  if (!value) return { ok: true, value: '', action: 'clear' }
  if (backgroundImageValue(value) === 'none') {
    return { ok: false, error: '图片地址必须以 http:// 或 https:// 开头。' }
  }
  if (/[\u0000-\u001f\u007f]/.test(value)) {
    return { ok: false, error: '图片地址不能包含换行或其他控制字符。' }
  }
  try {
    const parsed = new URL(value)
    if (!/^https?:$/.test(parsed.protocol) || !parsed.hostname) {
      return { ok: false, error: '图片地址必须包含有效主机名。' }
    }
  } catch {
    return { ok: false, error: '图片地址格式无效，请填写有效的 http:// 或 https:// 地址。' }
  }
  return { ok: true, value, action: 'apply' }
}
