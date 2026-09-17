// 数字与金额格式化。
//
// 抽成独立的 .ts 而不是留在 shared.tsx 里，是因为 `node --test` 不做 JSX 转译、
// import 不了 .tsx —— 精度规则需要被测，就必须住在这里（同 modelRedirect.ts、
// channelKeyHealth.ts 的惯例）。shared.tsx 只是把这里的实现再导出给页面用。

/** 千分位整数，digits 为小数位。 */
export function formatNumber(value: number | undefined, digits = 0): string {
  return new Intl.NumberFormat('zh-CN', {
    maximumFractionDigits: digits,
    minimumFractionDigits: digits,
  }).format(value || 0)
}

/**
 * 一组数字**共用**的小数位。
 *
 * 精度必须按组定，不能按单个值定。按单个值定会出现：工具卡里 `$7.80` 旁边站着
 * `$0.8906`，趋势轴上一格 `$1.84` 下一格 `$0.9219`。它们来自同一份数据、本该
 * 一样准，读者却得先分辨「哪个更精确」—— 那不是信息，是噪音。而且 4 位小数会
 * 让 89 美分看起来像 89.06 美分，凭空多出两位有效数字。
 *
 * 规则：整组都不到 1 分钱时才上 4 位（否则全是 `$0.00`，等于没显示）。
 */
export function moneyDigits(values: (number | undefined)[]): number {
  const maximum = Math.max(0, ...values.map((value) => Math.abs(value || 0)))
  return maximum > 0 && maximum < 0.01 ? 4 : 2
}

/**
 * 金额。
 *
 * 省略 digits 时用「2 位，除非整个值本身不到 1 分钱」。**别再退回「>= 1 才两位、
 * 否则四位」那种按值分档的写法** —— 那正是同一列里混出 `$4.23` 和 `$0.8906` 的原因。
 * 新代码在成组展示时请显式传 `moneyDigits(整组)`，可以额外覆盖「一组里既有 5 分
 * 又有 0.4 分」这种边缘情况。
 */
export function formatMoney(value: number | undefined, digits?: number): string {
  const amount = value || 0
  const places = digits ?? (Math.abs(amount) > 0 && Math.abs(amount) < 0.01 ? 4 : 2)
  const floor = 10 ** -places
  if (amount > 0 && amount < floor) return `< $${floor.toFixed(places)}`
  return `$${formatNumber(amount, places)}`
}

/** 百分比。小于 1% 时保留一位，否则取整 —— 避免「0%」把非零的量抹掉。 */
export function formatPercent(value: number): string {
  const percent = Math.max(0, value * 100)
  return `${percent.toFixed(percent > 0 && percent < 1 ? 1 : 0)}%`
}
