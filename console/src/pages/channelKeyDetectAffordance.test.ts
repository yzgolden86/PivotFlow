import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

// 「健康管理里能对单个 Key 检测」这条能力靠三个形状撑着。三条都容易在重构里被抹掉，
// 而抹掉之后界面**照样能用**，只是静默退化：
//
//   1) 每张 Key 卡片的按钮必须把**只含这一把**的数组交给 runChecks（`[key]`）。
//      改成 visible / targets 就退化成批量 —— 点哪把 Key 都会测全部。
//   2) 从未检测过的 Key 必须用 primary-button（醒目），已检测过的用 secondary-button。
//      全用 secondary 就回到「新加的 Key 看不出能点」——hao哥 这次报的就是这个观感。
//   3) 同一行里 primary 与 secondary 的紧凑尺寸必须一致：`.primary-button` 默认
//      min-height 36px、`.secondary-button` 34px，而 `.key-health-actions` 的紧凑
//      规则原本只写了 secondary，换成 primary 后按钮会高 6px、与「停用」对不齐。
//
// 没有 DOM 测试环境，所以在源码层面守：三条都是静态就能看出来的形状问题。

const modal = readFileSync(new URL('ChannelKeysModal.tsx', import.meta.url), 'utf8')
const css = readFileSync(new URL('../styles.css', import.meta.url), 'utf8')

test('每张 Key 卡片把「只有这一把」的数组交给检测', () => {
  assert.match(modal, /runChecks\(\[key\]\)/,
    '找不到 runChecks([key])：单把 Key 的检测入口没了，或改成了传整批（点哪把都会测全部）')
})

test('从未检测过的 Key 用 primary-button，检测过的用 secondary-button', () => {
  assert.match(modal, /className=\{key\.health\.checked_at \? 'secondary-button' : 'primary-button'\}/,
    '检测按钮不再按「有没有检测记录」区分主次 —— 新加的 Key 又看不出可以点')
})

test('单把 Key 的按钮叫「检测」，弹窗里不再出现「复检」', () => {
  assert.match(modal, /aria-label=\{`检测 Key #\$\{key\.key_index \+ 1\}`\}/,
    '单把 Key 的按钮 aria-label 变了 —— 它必须明确指向 Key #N')
  assert.ok(!modal.includes('复检'),
    '弹窗里又出现「复检」字样：hao哥 2026-10-03 明确要统一叫「检测」')
})

test('.key-health-actions 的紧凑尺寸同时覆盖 primary-button，且边框拉平', () => {
  const rule = /\.key-health-actions[^{}]*\{[^{}]*min-height:\s*30px[^{}]*\}/.exec(css)
  assert.ok(rule, '找不到 .key-health-actions 的紧凑尺寸规则（选择器改名了就同步改这里）')
  assert.match(rule[0], /\.key-health-actions\s+\.primary-button/,
    '紧凑尺寸只覆盖 secondary-button：primary 的「检测」按钮会比同一行的「停用」高 6px，基线对不齐')
  // primary 是 border:0、secondary 是 border:1px solid —— 只拉平 min-height/padding
  // 仍差 2px（实测 42 vs 44）。必须显式给 primary 补一条边框。
  assert.match(css, /\.key-health-actions\s+\.primary-button\s*\{[^{}]*border:\s*1px solid/,
    '没给 .key-health-actions 里的 primary-button 补边框：它会比同行的 secondary 矮 2px')
})

// 标签「检测」「停用」都只有两个字，但它曾经真的折成了两行（按钮 44px 高），
// 原因是本行的 4px 缺口被分摊到两个文字按钮上各缩 2px，而 CJK 可以在任意字间断行。
// 所以紧凑规则必须带 white-space: nowrap —— 两个字的标签永远不该重排。
test('紧凑按钮禁止换行（两个字的标签不许掉成两行）', () => {
  const rule = /\.key-health-actions[^{}]*\{[^{}]*min-height:\s*30px[^{}]*\}/.exec(css)
  assert.ok(rule, '找不到 .key-health-actions 的紧凑尺寸规则')
  assert.match(rule[0], /white-space:\s*nowrap/,
    '紧凑按钮没禁换行：本行只要出现几 px 的缺口，「检测」「停用」就会各掉成一个字一行、按钮变成 44px 高')
})

// .icon-button 基类是 flex: 0 0 34px。在行里主尺寸由 flex-basis 决定，
// 所以「只写 width: 30px」等于没改 —— 它还是 34px 宽，让本行凭空多出 4px 缺口，
// 反过来把两个文字按钮挤到折行（就是上面那条的成因）。宽度和 flex 必须一起写。
test('.key-health-actions 的图标按钮宽度与 flex 一起写', () => {
  const rules = css.match(/\.key-health-actions[^{}]*\.icon-button[^{}]*\{[^{}]*\}/g) || []
  assert.ok(rules.length >= 2, `应当有桌面与窄屏两条 .icon-button 规则，只找到 ${rules.length} 条`)
  for (const rule of rules) {
    assert.match(rule, /flex:\s*0 0 \d+px/,
      `这条规则只改了 width/height 没改 flex，图标在行里仍是基类的 34px：${rule}`)
  }
})
