import { test } from 'node:test'
import assert from 'node:assert/strict'
import { currencySymbol, formatMoney, formatMoneyIn, formatNumber, formatPercent, moneyDigits } from './format.ts'

// 金额精度回归。
//
// 这条守卫针对的是**一类**缺陷，不是某个数字：`formatMoney` 原来按单个值定精度
// （`>= 1` 两位，否则四位），于是同一组数字里会混出两种写法 —— 工具卡上
// `$7.80` 旁边站着 `$0.8906`，趋势轴上一格 `$1.84` 下一格 `$0.9219`。
// 它们来自同一份数据、本该一样准，读者却得先分辨「哪个更精确」；
// 而且 4 位小数把 89 美分写成 89.06 美分，凭空多出两位有效数字。
//
// 所以下面最关键的一条是「同组精度必须一致」——它对**任意**一组数字都成立，
// 将来往卡片里加新数字时，只要忘了传组精度就会红。

/** 数出 `$1.234` 里小数位的个数；`<$0.01` 这类也算。 */
function decimalsOf(formatted: string): number {
  const matched = /\.(\d+)/.exec(formatted)
  return matched ? matched[1].length : 0
}

test('moneyDigits：整组都不到 1 分钱才上 4 位，否则 2 位', () => {
  assert.equal(moneyDigits([12.5, 3.25]), 2)
  assert.equal(moneyDigits([0.89, 0.71]), 2)
  assert.equal(moneyDigits([0.008, 0.003]), 4, '全组都是亚分位时必须加精度，否则全是 $0.00')
  assert.equal(moneyDigits([0, 0]), 2, '全零按 2 位，不该因为「最大值 < 0.01」跳成 4 位')
  assert.equal(moneyDigits([]), 2)
  assert.equal(moneyDigits([undefined, 5]), 2, 'undefined 当 0 处理')
})

test('同组金额的小数位必须一致（这条是真正的回归点）', () => {
  // 就是截图里那四张工具卡的真实取值。
  const group = [7.8, 0.8906, 0.7054, 0.7791]
  const digits = moneyDigits(group)
  const formatted = group.map((value) => formatMoney(value, digits))
  const widths = new Set(formatted.map(decimalsOf))
  assert.equal(widths.size, 1, `同组出现了 ${widths.size} 种精度：${formatted.join(' / ')}`)
  assert.deepEqual(formatted, ['$7.80', '$0.89', '$0.71', '$0.78'])
})

test('趋势轴上下两格的小数位必须一致', () => {
  // 截图里 y 轴是 $1.84 和 $0.9219 —— 同一根轴上两种刻度写法。
  const maximum = 1.8438
  const digits = moneyDigits([maximum])
  const labels = [maximum, maximum / 2].map((value) => formatMoney(value, digits))
  assert.equal(new Set(labels.map(decimalsOf)).size, 1, `轴标签精度不一致：${labels.join(' / ')}`)
  assert.deepEqual(labels, ['$1.84', '$0.92'])
})

test('整组都是亚分位时才保留 4 位', () => {
  const digits = moneyDigits([0.0081, 0.0034])
  assert.equal(digits, 4)
  assert.deepEqual([0.0081, 0.0034].map((v) => formatMoney(v, digits)), ['$0.0081', '$0.0034'])
})

test('省略 digits 时也不按单个值分档 —— 这是跨页面那个缺陷的根因', () => {
  // 旧规则「>= 1 两位、否则四位」会让同一列里出现 $4.23 和 $0.8906。
  // 现在默认就是 2 位，只有值本身不到 1 分钱才上 4 位。
  assert.equal(formatMoney(12.3456), '$12.35')
  assert.equal(formatMoney(0.8906), '$0.89')
  assert.equal(formatMoney(0.0693), '$0.07')
  assert.equal(formatMoney(0.004), '$0.0040', '不到 1 分钱，2 位会显示成 $0.00，所以要 4 位')

  // 整列混排：全部 2 位，不再出现两种写法。
  const column = [4.23, 3.05, 0.8906, 0.6361, 0.6278, 0.5157, 0.1513, 0.0693]
  const formatted = column.map((value) => formatMoney(value))
  assert.equal(new Set(formatted.map(decimalsOf)).size, 1, `同列精度不一致：${formatted.join(' / ')}`)
})

test('低于该精度能表示的下限时，不谎报 $0.00', () => {
  assert.equal(formatMoney(0.004, 2), '< $0.01')
  assert.equal(formatMoney(0.00004, 4), '< $0.0001')
  assert.equal(formatMoney(0, 2), '$0.00', '真的零要显示成零，不是 < $0.01')
})

test('负数与 undefined 不炸', () => {
  assert.equal(formatMoney(undefined, 2), '$0.00')
  // 负号在货币符号**后面**（`$-3.50`）。这是改造前就有的行为，本次不动它 ——
  // 改成正字号的 `-$3.50` 会影响另外 5 个已在用这个函数的页面，
  // 而负金额（退款/返还）在界面上极少出现。真要改就单独提一次。
  assert.equal(formatMoney(-3.5, 2), '$-3.50')
  assert.equal(formatNumber(undefined), '0')
})

test('formatNumber 按位数补零，用于对齐的表格数字', () => {
  assert.equal(formatNumber(1234.567, 2), '1,234.57')
  assert.equal(formatNumber(1234.567, 0), '1,235')
  assert.equal(formatNumber(0), '0')
})

test('formatPercent 只在不到 1% 时保留一位', () => {
  assert.equal(formatPercent(0.386), '39%')
  assert.equal(formatPercent(0.004), '0.4%', '不到 1% 不能被取整成 0%')
  assert.equal(formatPercent(0), '0%')
  assert.equal(formatPercent(-1), '0%', '负占比按 0 处理')
})

test('币种符号：美元 $、人民币 ¥，大小写与空白都容忍', () => {
  assert.equal(currencySymbol('USD'), '$')
  assert.equal(currencySymbol('usd'), '$')
  assert.equal(currencySymbol(' USD '), '$')
  assert.equal(currencySymbol('CNY'), '¥')
  assert.equal(currencySymbol('RMB'), '¥')
})

test('认不出的币种原样返回，不冒充成美元', () => {
  // 认不出就退回代码本身，至少不会把金额说成错误的币种。
  assert.equal(currencySymbol('EUR'), 'EUR')
  assert.equal(currencySymbol(''), '')
  assert.equal(currencySymbol(undefined), '')
})

test('formatMoneyIn 与 formatMoney 的精度规则完全一致，只换符号', () => {
  // formatMoney 就是 formatMoneyIn(值, USD)，这条守住「没有第二套精度规则」。
  for (const value of [12.3456, 0.8906, 0.0693, 0.004, 0, -3.5, undefined]) {
    assert.equal(formatMoney(value), formatMoneyIn(value, 'USD'), `值 ${value} 两条路径结果不一致`)
  }
})

test('人民币金额带 ¥，小于一分钱时也给 ¥ 而不是 $', () => {
  assert.equal(formatMoneyIn(7.8, 'CNY'), '¥7.80')
  assert.equal(formatMoneyIn(0.004, 'CNY', 2), '< ¥0.01')
  assert.equal(formatMoneyIn(1234.567, 'CNY', 2), '¥1,234.57')
})

test('币种缺失时退回 $（历史行为，绝大多数站点按美元计价）', () => {
  assert.equal(formatMoneyIn(7.8, undefined), '$7.80')
  assert.equal(formatMoneyIn(7.8, ''), '$7.80')
})
