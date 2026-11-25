// 请求模型与日志所保存映射模型的差异分级。
//
// 只有「可证明不改变模型身份」的差异才算写法差异：
//   1. 纯大小写不同         GPT-4O                vs gpt-4o
//   2. 仅多一段站点/厂商前缀  773/deepseek-v4-flash vs deepseek-v4-flash
// 其余一律实质重定向。
//
// 判据故意保守，因为定价表里这些后缀都是独立定价条目，判成写法差异会掩盖计费差：
//   gpt-4o $2.50/$10.00      vs gpt-4o-mini $0.15/$0.60          16.7 倍
//   gpt-5   $1.25/$10.00     vs gpt-5-nano  $0.05/$0.40          25 倍
//   gemini-2.5-flash $0.30   vs -flash-lite $0.10                3 倍
//   gpt-4o-2024-05-13 → gpt-4o-legacy $5.00（日期快照换档，不是写法差异）
// 而 ResolveBillingModel 只要实际模型有定价就按实际模型计费，日志页模型列却显示
// 请求模型 —— 所以隐藏这个差异等于让用户对不上账。
//
// 关键约束：无论哪一级，只要两者不同就必须把真实模型显示出来。
// 分级只影响提示措辞，不影响数据是否可见。旧日志的 actual_model 可能只
// 记录了模型选择；完整请求另提供实际发送的传输事实，不能仅凭名称推断发送。

export type ModelRedirectKind = 'none' | 'cosmetic' | 'substantive'

// 站点/厂商前缀形如 "773/"、"openai/"、"z.ai/"，只剥最前面一段。
const vendorPrefixPattern = /^[^/]+\//

function stripVendorPrefix(name: string): string {
  return name.replace(vendorPrefixPattern, '')
}

function hasVendorPrefix(name: string): boolean {
  return vendorPrefixPattern.test(name)
}

export function classifyModelRedirect(model?: string, actualModel?: string): ModelRedirectKind {
  const request = (model || '').trim()
  const actual = (actualModel || '').trim()
  if (!request || !actual || request === actual) return 'none'
  const a = request.toLowerCase()
  const b = actual.toLowerCase()
  if (a === b) return 'cosmetic'
  // 只认「单侧多一段前缀」。两侧都带前缀且厂商不同（openai/gpt-4o vs
  // azure/gpt-4o）是不同的路由端点，定价与可用性都可能不同，不算同一模型。
  if (hasVendorPrefix(a) !== hasVendorPrefix(b) && stripVendorPrefix(a) === stripVendorPrefix(b)) return 'cosmetic'
  return 'substantive'
}

// 悬浮提示：两者不同就一定给出真实模型，措辞按分级区分。
export function modelRedirectTitle(model?: string, actualModel?: string): string | undefined {
  const kind = classifyModelRedirect(model, actualModel)
  if (kind === 'none') return undefined
  const pair = `${(model || '').trim()} → ${(actualModel || '').trim()}`
  return kind === 'cosmetic' ? `${pair}（写法差异，同一模型）` : `实际重定向：${pair}`
}

// Older attempt logs retain the selected mapping even when constructing the
// request failed locally. Without a saved transmission flag, use neutral
// wording; the complete-request timeline records which attempts reached upstream.
export function logModelObservation(entry: { model?: string; actual_model?: string }): { label: string; model: string; title: string } | null {
  if (!entry.actual_model) return null
  return {
    label: '映射模型',
    model: entry.actual_model,
    title: `${entry.model || '未记录请求模型'} → ${entry.actual_model}。此处是日志保存的模型映射；是否请求上游以完整请求的传输记录为准。`,
  }
}
