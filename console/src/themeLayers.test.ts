import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

// 回归：自定义壁纸 + 玻璃表面的几条契约。都是「文本里看着对、但换个位置就不对」
// 的形状问题，所以静态守 —— 这个仓库没有 DOM 测试环境，不为它引入 jsdom
// （同样的取舍见 pages/editableRowKeys.test.ts 的注释）。
//
// 守五件事：
//   1) 点阵纹理必须在壁纸生效时被**整层**去掉（它排在壁纸**上面**）；
//   2) 全项目所有能透出背景的表面共用 `--glass-blur` 一个半径；
//   3) `.main-content > *` 的入场动画不能带 fill-mode（backdrop root 陷阱）；
//   4) 壁纸下**只改「小字」那两个令牌**（`--text-secondary` / `--text-muted`），
//      `--text`（含 27px 大标题）与 `--sidebar-*` 不许动；
//   5) 壁纸下**没有轮廓**：`--halo` / `--wallpaper-halo` / 壁纸档 `text-shadow`
//      一律不得存在（描边做不到只给小字，三档全被否，详见测试里的理由）。
const css = readFileSync(new URL('./styles.css', import.meta.url), 'utf8')
const theme = readFileSync(new URL('./theme.ts', import.meta.url), 'utf8')
// 去注释：注释里会引用 `blur(24px)`、`radial-gradient(circle, ...)` 这类片段，
// 不剔掉就会把「注释里提到过」误判成「真的写了」。
const clean = css.replace(/\/\*[\s\S]*?\*\//g, (comment) => comment.replace(/[^\n]/g, ' '))

function occurrences(source: string, pattern: RegExp): number {
  return (source.match(pattern) ?? []).length
}

/**
 * 找出某条声明归属的选择器。
 *
 * 只对**声明体内部**的调用者有意义（声明体里不会出现 `{`/`}`），做法是往前找最近的
 * `{` 与再往前的那个 `}`/`{`，两者之间就是选择器。`@media` 包裹时会带上外层选择器
 * 的残留片段，但这里只用来跟固定字符串比对，够用。
 */
function ownerSelector(source: string, declarationIndex: number): string {
  const open = source.lastIndexOf('{', declarationIndex)
  assert.ok(open >= 0, '声明前面找不到 `{`')
  const before = Math.max(source.lastIndexOf('}', open), source.lastIndexOf('{', open - 1))
  return source.slice(before + 1, open).replace(/\s+/g, ' ').trim()
}

/* ---- 1. 点阵纹理：壁纸生效时必须整层去掉 ---- */

test('点阵只有两处定义（亮色 + 暗色），都走 --app-bg-dots 令牌', () => {
  assert.equal(
    occurrences(clean, /--app-bg-dots:\s*radial-gradient\(circle,/g),
    2,
    '--app-bg-dots 应当在 :root 与 :root[data-theme="dark"] 各定义一次',
  )
  // 点阵的**形状**签名是「1px 圆点 + 1px 透明」+ 28px 平铺。按签名数，不按
  // `radial-gradient(circle,` 数 —— 后者还会命中装饰性的环境光晕
  // （`.page-header::before` 那类 `radial-gradient(circle, color-mix(...), transparent 68%)`），
  // 用它当判据会把正常的光晕算成「多出来的点阵」。
  assert.equal(
    occurrences(clean, /1px,\s*transparent 1px\)/g),
    2,
    '1px 点阵只能出现在 --app-bg-dots 的两处定义中，不能直接写进 .app-shell 的图层列表',
  )
  assert.equal(occurrences(clean, /background-size:[^;]*28px 28px/g), 2, '两个主题档的 .app-shell 都要保留 28px 平铺')
})

test('两个主题档的 .app-shell 都通过变量引用点阵', () => {
  // 一处亮色、一处暗色。少一处 = 另一套主题下点阵不受控。
  assert.equal(occurrences(clean, /var\(--app-bg-dots\)/g), 2, '.app-shell 的亮/暗两套图层都要用 var(--app-bg-dots)')
})

test('壁纸生效时 theme.ts 把点阵置 none，失效时交还给样式表', () => {
  assert.match(
    theme,
    /root\.style\.setProperty\('--app-bg-dots', 'none'\)/,
    '壁纸生效时必须把 --app-bg-dots 内联覆盖成 none',
  )
  // 必须是 removeProperty 而不是写回某个具体值：默认值依赖亮/暗主题
  // （--border-strong 在两套里亮度方向相反），写死一个值会在另一套主题下变错。
  assert.match(
    theme,
    /root\.style\.removeProperty\('--app-bg-dots'\)/,
    '没有壁纸时要 removeProperty，不能写回字面量',
  )
})

test('壁纸是否生效走 wallpaperActive，不能直接看 backgroundImage 非空', () => {
  // 用户在地址框里打了个半截的 `ht` 也会让 backgroundImage 非空，但那不产生任何
  // 背景层。判据必须是 backgroundImageValue(...) !== 'none'。
  assert.match(theme, /const hasWallpaper = wallpaper !== 'none'/)
  assert.match(theme, /root\.dataset\.wallpaper = hasWallpaper \? 'on' : 'off'/)
})

/* ---- 2. 模糊半径：一个旋钮管全部玻璃表面 ---- */

test('所有 backdrop-filter 只能是「2px 遮罩」或「--glass-blur 玻璃面」两种', () => {
  // 这条是本文件的重点。历史 bug：`.sidebar` 硬编码 blur(24px)、
  // `.page-header--elevated` 硬编码 blur(16px)、`.resource-strip` /
  // `.compact-summary` 硬编码 blur(14px) —— 侧栏是最大的玻璃面却不跟滑块，
  // 用户拖「模糊」时得出的结论是「这个设置没效果」。
  // 用「取值集合」而不是「逐条枚举选择器」来守：以后新增玻璃表面时，
  // 只要写了 var(--glass-blur) 就自动合规，写了字面量就会被这条抓住。
  const declarations = [...clean.matchAll(/backdrop-filter:\s*([^;]+);/g)].map((match) => match[1].trim())
  assert.ok(declarations.length >= 8, `backdrop-filter 声明太少（${declarations.length} 条），正则可能失效了`)
  assert.deepEqual(
    [...new Set(declarations)].sort(),
    ['blur(2px)', 'blur(var(--glass-blur, 14px))'],
    '出现第三种 backdrop-filter 取值：要么是忘了接 --glass-blur，要么是新增了不该跟滑块的层',
  )
})

test('两个遮罩层的 2px 是有意保留的，注释里说明了理由', () => {
  // 防止有人为了让上面那条断言「更干净」而把遮罩也接到 --glass-blur 上：
  // 遮罩不是玻璃面，是压暗层，跟着滑块变会让弹窗背景糊成一片。
  assert.equal(occurrences(clean, /backdrop-filter:\s*blur\(2px\);/g), 2, '遮罩层应当正好两条（.search-overlay / .modal-layer）')
})

/* ---- 3. 入场动画不能留 fill-mode ---- */

test('.main-content > * 的入场动画不带 fill-mode', () => {
  const matched = /\.main-content > \* \{\s*animation:\s*([^;]+);/.exec(clean)
  assert.ok(matched, '找不到 .main-content > * 的 animation 声明')
  // pf-rise 的 to 帧是 opacity:1 / transform:none，与自然状态逐字相同，且没有 delay
  // （backwards 只在有延迟时才有意义）—— 所以 `both` 在视觉上完全多余。
  // 但它会让页面根节点一直挂着一个生效中的 opacity/transform 动画，于是整页成为
  // backdrop root，里面的 backdrop-filter 采不到壁纸，毛玻璃就只剩「不透明度生效、
  // 模糊没效果」。
  assert.ok(!/\bboth\b/.test(matched[1]), `.main-content > * 的动画带了 fill-mode：${matched[1]}`)
})

test('.kpi-grid > * 必须保留 both（靠 delay 错峰，不能跟着一起删）', () => {
  const matched = /\.kpi-grid > \* \{\s*animation:\s*([^;]+);/.exec(clean)
  assert.ok(matched, '找不到 .kpi-grid > * 的 animation 声明')
  // 它有 animation-delay 错峰，必须有 backwards，否则延迟期间会先闪一下实体卡片。
  assert.match(matched[1], /\bboth\b/, `.kpi-grid > * 的动画丢了 fill-mode：${matched[1]}`)
})

/* ---- 4. 壁纸下的可读性 ---- */

test('壁纸下只改「小字」这两个令牌：--text 与 --sidebar-* 一律不许动', () => {
  // 范围是按**实测字号**定的，2026-09-29 由用户拍板：
  //   --text-secondary   9~13px（中位 11px）  95 处  ← 改
  //   --text-muted       9~13px（中位 11px） 186 处  ← 改
  //   --text            11~27px（中位 13px） 142 处  ← 不改（含大标题、大数字）
  //   --sidebar-text     继承                  1 处  ← 不改
  //   --sidebar-muted   10~11px                5 处  ← 不改
  // 前两个字号分布完全一样，是最灰最吃力的一档；`--text` 混着 27px 的大标题和大数字，
  // 一起加深会让整页「变重」。
  const light = /:root\[data-wallpaper="on"\]\s*\{([^}]*)\}/.exec(clean)
  assert.ok(light, '缺少亮色档的文字色覆盖')
  const dark = /:root\[data-theme="dark"\]\[data-wallpaper="on"\]\s*\{([^}]*)\}/.exec(clean)
  assert.ok(dark, '缺少暗色档的文字色覆盖')

  for (const [name, body] of [['亮色', light[1]], ['暗色', dark[1]]] as const) {
    assert.match(body, /--text-secondary:/, `${name}档应当覆盖 --text-secondary`)
    assert.match(body, /--text-muted:/, `${name}档应当覆盖 --text-muted`)
    // `--text\s*:` 不会误伤 `--text-secondary:`（后者在 `--text` 后接的是 `-` 不是 `:`）。
    assert.ok(!/--text\s*:/.test(body), `${name}档不该覆盖 --text（含 27px 大标题、大数字）`)
    assert.ok(!/--sidebar-text\s*:/.test(body), `${name}档不该覆盖 --sidebar-text`)
    assert.ok(!/--sidebar-muted\s*:/.test(body), `${name}档不该覆盖 --sidebar-muted`)
    // 规则体里**只允许**这两个令牌 —— 描边（`text-shadow`）与垫底（`background`）
    // 都被否掉了，多声明一个往往就是有人想把被否的方案加回来。
    const declared = [...body.matchAll(/(--[\w-]+)\s*:/g)].map((match) => match[1])
    assert.deepEqual(
      declared,
      ['--text-secondary', '--text-muted'],
      `${name}档的壁纸规则里只允许这两个令牌，实际：${declared.join(', ')}`,
    )
  }

  // 暗色块只能有一份：写两遍时后者胜、前者是死代码，而且「改一处漏一处」最容易发生。
  assert.equal(
    occurrences(clean, /:root\[data-theme="dark"\]\[data-wallpaper="on"\]\s*\{/g),
    1,
    '暗色壁纸块出现了多次',
  )
})

test('壁纸下不做轮廓：--halo / --wallpaper-halo 与壁纸档 text-shadow 一律不得存在', () => {
  // 描边这条路试过**三档全被否**（2026-09-28 一整天）：软阴影 / 硬边+辉光+88% /
  // 硬边+零辉光+55%。最后一档技术上最舒服，但用户最终要求「保留小字、其他改回去」，
  // 而**轮廓做不到只给小字**：`text-shadow` 只能挂在**选择器**上、不能挂在颜色令牌上，
  // 只给小字就得维护一份会随新增页面不断漏的选择器清单（漏了还没有守卫能发现）。
  // 挂 `body` 靠继承全站兜底又必然改到大字，与「只改小字」直接冲突 —— 两头都走不通。
  // 颜色令牌是按定义精确落到它自己的 281 个消费点上，不用维护清单，这才是能活的方案。
  // ↑ 这段理由在 styles.css 的注释里有一份，改这里之前先看那份别把结论改反。
  assert.ok(!/--wallpaper-halo/.test(clean), '--wallpaper-halo 已整条撤掉，不该再出现')
  assert.ok(!/--halo\b/.test(clean), '--halo 已整条撤掉，不该再出现')
  assert.ok(
    !/:root\[[^\]]*data-wallpaper="on"[^\]]*\]\s+body/.test(clean),
    '壁纸档不应再有 body 规则（轮廓已撤，body 上没有别的事要做）',
  )
  assert.ok(
    !/:root\[[^\]]*data-wallpaper="on"[^\]]*\]\s+\.metric-value/.test(clean),
    '壁纸档不应再有 .metric-value 规则（轮廓已撤，无需并回）',
  )
})

test('全项目唯一的 text-shadow 是 .metric-value 那条原有发光', () => {
  // 撤掉轮廓之后，`text-shadow` 应当只剩这条**做本功能之前就存在**的装饰性发光。
  // 任何新增的 `text-shadow` 都会掉进这里 —— 报错信息里带着它的归属选择器。
  // 改这条之前先 `grep -n text-shadow console/src/styles.css` 重新穷举一遍。
  const declarations = [...clean.matchAll(/text-shadow:\s*([^;]+);/g)].map((match) => {
    assert.ok(match.index !== undefined, 'matchAll 的结果一定带 index')
    return { owner: ownerSelector(clean, match.index), value: match[1].trim() }
  })
  assert.equal(
    declarations.length,
    1,
    `全项目应当只剩 .metric-value 那一条原有发光，实际 ${declarations.length} 条：` +
      declarations.map((item) => `${item.owner} { ${item.value} }`).join(' | '),
  )
  assert.equal(declarations[0].owner, '.metric-value')
  assert.match(
    declarations[0].value,
    /^0 0 20px color-mix\(in srgb, var\(--metric-tone, var\(--green\)\) 12%, transparent\)$/,
    '剩下的必须是那条已知的 20px 装饰性发光，不是别的什么',
  )
  assert.ok(
    !declarations[0].value.includes('wallpaper'),
    `.metric-value 的发光不该再引用任何壁纸轮廓：${declarations[0].value}`,
  )
})

test('壁纸下不得给元素加垫底（「底部一长条」已被否）', () => {
  // `.dashboard-footer` / `.pagination` 是 `justify-content: space-between` 的
  // **全宽 flex 行**，加底色必然拉成一条横贯页面的带子。实测结论：「好丑」。
  const rules = [...clean.matchAll(/:root((?:\[[^\]]*\])+)\s*\{([^}]*)\}/g)]
    .filter((match) => match[1].includes('data-wallpaper="on"'))
  assert.equal(rules.length, 2, `壁纸规则应当正好 2 条（亮/暗），实际 ${rules.length} 条`)
  for (const rule of rules) {
    assert.ok(
      !/background:/.test(rule[2]),
      `壁纸规则里不该出现 background（垫底已被否）：${rule[0].replace(/\s+/g, ' ').slice(0, 120)}`,
    )
  }
  // 选择器要整串吃属性再筛，不能写 `:root\[[^\]]*data-wallpaper…` ——
  // 那个 `[^\]]*` 跨不过暗色那条中间的 `][`，暗色规则会静默漏掉，
  // 守卫只覆盖一半而测试照样绿。
  assert.ok(
    !/:root\[[^\]]*data-wallpaper="on"[^\]]*\]\s+\.pagination/.test(clean),
    '分页条不该有壁纸专属规则',
  )
  assert.ok(
    !/:root\[[^\]]*data-wallpaper="on"[^\]]*\]\s+\.dashboard-footer/.test(clean),
    '页脚不该有壁纸专属规则',
  )
})
