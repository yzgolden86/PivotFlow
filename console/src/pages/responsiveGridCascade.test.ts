import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

// 回归：媒体查询不增加优先级。顶层规则只要排在媒体查询之后，同权重靠后就赢，
// 窄屏收列会被整段吃掉。
//
// 历史 bug：.token-row 的九列模板写在顶层且排在 1180px/620px 两个媒体查询后面，
// 于是窄屏下模板仍是九列，而 :nth-child 该隐藏的单元格照样隐藏——列数和单元格数
// 对不上，表头和数据整列错位。
//
// 现在表头和行共用 .token-table-wrap 上的 --token-columns / --token-min，
// 媒体查询改变量即可。这个测试守两件事：
//   1) 收列后的模板必须由媒体查询里的变量覆盖，不能再出现「顶层列模板排在
//      媒体查询之后」的形状；
//   2) 表头和数据行必须引用同一个变量，不能各写一份字面模板。
const css = readFileSync(new URL('../styles.css', import.meta.url), 'utf8')

type Rule = { selector: string; body: string; index: number; inMedia: boolean }

// 扫描全部规则，记录位置和它外层有没有条件 at-rule。
// 用栈记录每层是不是 at-rule：单个 mediaDepth 变量在嵌套 @media/@supports 下会
// 错位——嵌套块闭合时把标记一起清掉，后面的媒体查询规则就被当成顶层规则了。
function parseRules(source: string): Rule[] {
  // 先去注释，避免注释里的花括号和分号干扰深度统计。
  const clean = source.replace(/\/\*[\s\S]*?\*\//g, (comment) => comment.replace(/[^\n]/g, ' '))
  const rules: Rule[] = []
  const stack: boolean[] = []
  let start = 0
  for (let cursor = 0; cursor < clean.length; cursor += 1) {
    const char = clean[cursor]
    if (char === '{') {
      const prelude = clean.slice(start, cursor).trim()
      const isAtRule = prelude.startsWith('@')
      if (!isAtRule) {
        const close = clean.indexOf('}', cursor)
        rules.push({
          selector: prelude,
          body: clean.slice(cursor + 1, close === -1 ? clean.length : close),
          index: cursor,
          inMedia: stack.some(Boolean),
        })
      }
      stack.push(isAtRule)
      start = cursor + 1
    } else if (char === '}') {
      stack.pop()
      start = cursor + 1
    } else if (char === ';' && stack.length === 0) {
      start = cursor + 1
    }
  }
  return rules
}

const rules = parseRules(css)

// 令牌表格：表头和数据行都必须走变量，且顶层不能在媒体查询之后重写列模板。
test('令牌表格的列模板只有一份来源', () => {
  for (const selector of ['.token-table-head', '.token-row']) {
    const owning = rules.filter((rule) => !rule.inMedia
      && rule.selector.split(',').some((part) => part.trim() === selector)
      && /grid-template-columns\s*:/.test(rule.body))
    assert.equal(owning.length, 1, `${selector} 的顶层列模板应当只有一处，实际 ${owning.length} 处`)
    assert.match(
      owning[0].body,
      /grid-template-columns\s*:\s*var\(--token-columns\)/,
      `${selector} 必须引用 --token-columns，不能各写一份字面模板`,
    )
  }
})

// 收列既可以直接写 grid-template-columns，也可以改自定义属性（本表格就是改
// --token-columns）。两种写法都算「列声明」，否则改成变量后这个守卫会失效。
const columnDeclaration = /(?:^|[;\s])(?:grid-template-columns|--[\w-]*columns)\s*:/

// 通用形状守卫：任何在媒体查询里被收列的选择器，其顶层列声明都必须排在
// 那个媒体查询之前，否则同权重靠后，收列不生效。
test('顶层列声明不得排在覆盖它的媒体查询之后', () => {
  const offenders: string[] = []
  for (const rule of rules) {
    if (rule.inMedia || !columnDeclaration.test(rule.body)) continue
    for (const raw of rule.selector.split(',')) {
      const selector = raw.trim()
      if (!selector.startsWith('.')) continue
      const overriding = rules.find((other) => other.inMedia
        && other.index < rule.index
        && columnDeclaration.test(other.body)
        && other.selector.split(',').some((part) => part.trim() === selector))
      if (overriding) offenders.push(`${selector}：顶层声明在偏移 ${rule.index}，被它盖掉的媒体查询在偏移 ${overriding.index}`)
    }
  }
  assert.deepEqual(offenders, [], `以下选择器的窄屏收列会被靠后的顶层规则盖掉：\n${offenders.join('\n')}`)
})
