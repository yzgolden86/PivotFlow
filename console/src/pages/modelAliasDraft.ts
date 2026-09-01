import type { ModelAliasSuggestion } from '../types'

export type AliasDraft = { canonical: string; aliases: string; enabled: boolean }

// 统一名的身份口径必须与后端一致：NormalizeModelAliasGroups 按小写去重，
// 草稿里出现两个只差大小写的统一名会让 admin_settings 整批拒绝保存
// （报错只说 duplicate canonical names，不指出是哪一行）。
export function canonicalKey(name: string): string {
  return name.trim().toLowerCase()
}

export function parseAliasDraft(value: string): AliasDraft[] {
  try {
    const parsed = JSON.parse(value || '[]') as Array<{ canonical?: string; aliases?: string[]; enabled?: boolean }>
    if (!Array.isArray(parsed)) return []
    return parsed.map((item) => ({
      canonical: item.canonical || '',
      aliases: Array.isArray(item.aliases) ? item.aliases.join('\n') : '',
      enabled: item.enabled !== false,
    }))
  } catch {
    return []
  }
}

export function serializeAliasDraft(groups: AliasDraft[]): string {
  return JSON.stringify(groups.map((group) => ({
    canonical: group.canonical.trim(),
    aliases: group.aliases.split(/[\n,]/).map((item) => item.trim()).filter(Boolean),
    enabled: group.enabled,
  })), null, 2)
}

export function aliasMembers(group: AliasDraft): string[] {
  return group.aliases.split(/[\n,]/).map((item) => item.trim()).filter(Boolean)
}

export function withMembers(group: AliasDraft, members: string[]): AliasDraft {
  return { ...group, aliases: Array.from(new Set(members)).join('\n') }
}

// 批量套用建议。返回新的草稿数组，不改动入参。
//
// 两条硬约束，违反任意一条都会造成用户可见的错误：
//   1. 多条建议可以合法指向同一个已有组（一个组的别名可能归一化到不同键）。
//      按统一名去重会静默丢弃其余几条，横幅于是反复提示同一批名称。
//   2. 新建组的统一名不得与任何已有组大小写冲突，否则后端拒绝整批保存。
// 因此查找目标组和防重都走 canonicalKey，且「合并进已有组」优先于「新建组」。
export function adoptSuggestions(groups: AliasDraft[], selections: ModelAliasSuggestion[]): AliasDraft[] {
  const next = groups.map((group) => ({ ...group }))
  const createdKeys = new Set(next.map((group) => canonicalKey(group.canonical)))
  for (const suggestion of selections) {
    const targetName = suggestion.extends_canonical || suggestion.canonical
    const target = next.findIndex((group) => canonicalKey(group.canonical) === canonicalKey(targetName))
    if (target >= 0) {
      const merged = withMembers(next[target], [...aliasMembers(next[target]), ...suggestion.members])
      // 目标组可能是停用状态：后端不把停用组计入已认领，所以它的统一名会作为
      // 「新建」建议再次出现。静默并入却保持停用的话，采用后路由完全没变，
      // 而横幅又因为成员已被认领不再提示，用户无从察觉。采用即视为要它生效。
      next[target] = merged.enabled ? merged : { ...merged, enabled: true }
      continue
    }
    const key = canonicalKey(suggestion.canonical)
    if (!key || createdKeys.has(key)) continue
    createdKeys.add(key)
    const members = suggestion.members.filter((member) => member !== suggestion.canonical)
    next.push(withMembers({ canonical: suggestion.canonical, aliases: '', enabled: true }, members))
  }
  return next
}
