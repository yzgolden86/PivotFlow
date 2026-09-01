import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

// 回归：受控输入所在的列表行，React key 里不能出现该行自己正在编辑的值。
//
// 症状是「只能粘贴，手打一个字符就中断」：每敲一个键 -> onChange 改状态 ->
// 该行的值变了 -> key 跟着变 -> React 认为是不同元素，卸载旧行挂载新行 ->
// 输入框连同焦点一起被销毁。粘贴之所以看着正常，是因为它只触发一次 onChange，
// 焦点丢失发生在输入已经结束之后。
//
// 没有 DOM 测试环境，所以这里在源码层面守：静态就能看出来的形状问题，
// 不值得为它引入一整套 jsdom + testing-library。
//
// 两处历史 bug：
//   SystemSettingsPageV2  key={`${index}-${group.canonical}`}  统一名称输入框
//   ChannelsPage          key={`${item.model}-${index}`}       渠道模型名输入框
const guarded: Array<{ file: string; rows: Array<{ marker: string; edited: string[] }> }> = [
  {
    file: 'SystemSettingsPageV2.tsx',
    rows: [{ marker: 'model-alias-row', edited: ['group.canonical', 'group.aliases'] }],
  },
  {
    file: 'ChannelsPage.tsx',
    rows: [{ marker: 'editable-model-row', edited: ['item.model', 'item.redirect_model'] }],
  },
]

// 从 open 处读出一段花括号平衡的 JSX 表达式（含首尾花括号）。
// 必须数深度：key={`${index}-${group.canonical}`} 里嵌了两层 ${}，
// 用 /\{[^}]*\}/ 只会截到第一个 } 前，正好把要查的那个字段漏掉。
function balancedBraces(source: string, open: number): string {
  let depth = 0
  for (let cursor = open; cursor < source.length; cursor += 1) {
    const char = source[cursor]
    if (char === '{') depth += 1
    else if (char === '}') {
      depth -= 1
      if (depth === 0) return source.slice(open, cursor + 1)
    }
  }
  assert.fail(`花括号未闭合，起点 ${open}`)
}

// 抓出 className 含 marker 的那个 JSX 标签上的 key={...}，允许两者顺序颠倒。
// 标签结尾同样按深度找 '>'：属性里的箭头函数（onClick={() => ...}）会有 '>'。
function keyExpressionFor(source: string, marker: string): string {
  const at = source.indexOf(marker)
  assert.ok(at >= 0, `找不到 ${marker}`)
  const start = source.lastIndexOf('<', at)
  assert.ok(start >= 0, `${marker} 前面没有标签起始符`)
  let depth = 0
  let end = -1
  for (let cursor = start; cursor < source.length; cursor += 1) {
    const char = source[cursor]
    if (char === '{') depth += 1
    else if (char === '}') depth -= 1
    else if (char === '>' && depth === 0) { end = cursor; break }
  }
  assert.ok(end > start, `${marker} 所在标签没有结束符`)
  const tag = source.slice(start, end + 1)
  const key = tag.indexOf('key=')
  assert.ok(key >= 0, `${marker} 所在标签没有 key：${tag.slice(0, 120)}`)
  const brace = tag.indexOf('{', key)
  assert.ok(brace >= 0, `${marker} 的 key 不是表达式`)
  return balancedBraces(tag, brace)
}

for (const { file, rows } of guarded) {
  for (const { marker, edited } of rows) {
    test(`${file} 的 ${marker} 不把编辑中的值拼进 key`, () => {
      const source = readFileSync(new URL(file, import.meta.url), 'utf8')
      const expression = keyExpressionFor(source, marker)
      for (const field of edited) {
        assert.ok(
          !expression.includes(field),
          `${marker} 的 key 含 ${field}：该值每次击键都变，会重建整行并丢焦点（key=${expression}）`,
        )
      }
    })
  }
}
