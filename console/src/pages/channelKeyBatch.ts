// 「批量粘贴 API Key」的解析。
//
// 放在纯 .ts 兄弟模块里是为了能直接跑 node --test —— 渠道编辑器的 Key 列表
// 是受控输入，它的粘贴语义（分隔符、去重、备注归属）容易写错又不容易肉眼发现，
// 值得有回归用例钉住，而 .tsx 没法被 node --test import。
//
// 支持的写法（每行一条，也允许一行多把）：
//   sk-aaa
//   sk-aaa | 主账号
//   sk-aaa, sk-bbb            ← 逗号 / 分号 / 中文逗号句号都行，共用同行备注
//   sk-aaa; sk-bbb | 备用
//   # 以 # 开头的整行是注释，会被忽略
//   "sk-aaa"                  ← 首尾成对的引号会被剥掉（从 JSON / 表格里复制常带）
//
// 去重与非法项**不报错**，只计数：批量粘贴时「有一半是已经存在的 Key」是最常见的
// 情况，一旦报错用户就得自己回头比对，反而更慢。调用方负责把计数展示出来。

export interface ChannelKeyBatchEntry {
  api_key: string
  note: string
}

export interface ChannelKeyBatchResult {
  /** 通过校验、且与已有行和本批次都不重复的条目，按粘贴顺序排列。 */
  entries: ChannelKeyBatchEntry[]
  /** 与已有行或本批次内重复而被跳过的数量。 */
  duplicate: number
  /** 含空白等明显不合法、被跳过的数量。分隔符切出来的空片段不计入。 */
  invalid: number
}

const separators = /[,;，；]/
const whitespace = /\s/

export function parseChannelKeyBatch(input: string, existingKeys: readonly string[] = []): ChannelKeyBatchResult {
  const seen = new Set<string>()
  for (const key of existingKeys) {
    const trimmed = key.trim()
    if (trimmed) seen.add(trimmed)
  }

  const entries: ChannelKeyBatchEntry[] = []
  let duplicate = 0
  let invalid = 0

  for (const rawLine of input.split(/\r?\n/)) {
    const line = rawLine.trim()
    if (!line || line.startsWith('#')) continue

    // 先切备注，再切多把 Key：这样 "sk-a, sk-b | 备用" 里的备注会同时落到两把 Key 上，
    // 而 "sk-a|sk-b" 不会被误当成两把 Key（| 在本语法里固定是备注分隔符）。
    const [keyPart, ...noteParts] = line.split('|')
    const note = noteParts.join('|').trim()

    for (const rawToken of keyPart.split(separators)) {
      const token = stripQuotes(rawToken.trim())
      if (!token) continue
      if (whitespace.test(token)) { invalid += 1; continue }
      if (seen.has(token)) { duplicate += 1; continue }
      seen.add(token)
      entries.push({ api_key: token, note })
    }
  }

  return { entries, duplicate, invalid }
}

/** 只剥成对的引号，避免把 Key 里合法出现的引号（极少）切坏。 */
function stripQuotes(value: string): string {
  const paired = /^(["'`])([\s\S]*)\1$/.exec(value)
  return paired ? paired[2].trim() : value
}
