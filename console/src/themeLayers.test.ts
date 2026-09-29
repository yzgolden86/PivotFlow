import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

// 回归：自定义壁纸 + 玻璃表面的几条契约。都是「文本里看着对、但换个位置就不对」
// 的形状问题，所以静态守 —— 这个仓库没有 DOM 测试环境，不为它引入 jsdom
// （同样的取舍见 pages/editableRowKeys.test.ts 的注释）。
//
// 守七件事：
//   1) 点阵纹理必须在壁纸生效时被**整层**去掉（它排在壁纸**上面**）；
//   2) 全项目所有能透出背景的表面共用 `--glass-blur` 一个半径；
//   3) 入场动画不能把页面变成 backdrop root —— 三条：`pf-rise` 关键帧不得含
//      `opacity`、`.main-content > *` 不得带 fill-mode、`.kpi-grid > *` 只能用
//      `backwards`（触发条件只有 opacity，实测见该组注释）；
//   4) 壁纸下**只改「小字」那两个令牌**（`--text-secondary` / `--text-muted`），
//      `--text`（含 27px 大标题）与 `--sidebar-*` 不许动；
//   5) 壁纸下**没有任何看得见的底 / 边**：`--halo` / `--wallpaper-halo` / 壁纸档
//      `text-shadow` / 页脚分页的 `background` 一律不得存在 —— 五档「给文字加东西」
//      全被否（最后一档的原话是「加了一个背景框，看起来好丑啊」）。
//      裸文本改成按壁纸局部明暗**换字色**（`data-bare-text-tone`，由
//      wallpaperTone.ts 采样后打在元素上）；
//   6) 每个「自己就是一层表面」的元素都在毛玻璃名单里 —— 第 2 组只守取值集合，
//      守不住「漏写」，而 2026-09-29 那个 bug 正是漏写（详见测试里的实测数字）；
//   7) 壁纸小字的对比度不能又被改浅（有下限断言）。
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

/* ---- 3. 入场动画不能把页面变成 backdrop root ---- */
//
// 触发条件**只有 opacity**：2026-09-29 用 `.tmp-dev-ui/glassroot.html`（棋盘格底，
// 「高频能量」越大 = 越没糊）逐项实测过 ——
//     plain / transform:translateY(0) / will-change:transform / filter:none /
//     isolation:isolate / contain:paint                        → 4.0~5.3  正常
//     opacity: 0.999                                            → 24.3     失效
//     `animation … both`（**已结束**）                            → 24.2     失效
//     `animation …`（只动 opacity，跑动中）                        → 33.9     失效
//     `animation … backwards`（已结束）                           → 4.3      正常
// 只要元素是 backdrop root，里面所有 `backdrop-filter` 就只能采到本层内画的东西
// （几乎为空）→ 壁纸不被模糊，毛玻璃退化成「清晰壁纸的取景窗」。
// 两种触发路径都要守：动画**跑动期间**（临时）、fill-mode 为 both/forwards 时（**永久**）。

test('入场关键帧不得含 opacity（否则动画期间全站毛玻璃失效）', () => {
  // pf-rise 只用在玻璃面板自己（.kpi-grid > * = .metric-card）或它的祖先（页面根节点）上，
  // 所以一个 opacity 都不能有。这条正是 hao哥 2026-09-29 报的
  // 「刚打开是一个样、立马又自己变一个样」：520ms 的入场里面板透出**清晰**壁纸，
  // 动画一结束模糊才「啪」地出现。
  const rise = /@keyframes pf-rise\s*\{([\s\S]*?)\n\}/.exec(clean)
  assert.ok(rise, '找不到 @keyframes pf-rise')
  assert.ok(
    !/opacity\s*:/.test(rise[1]),
    `pf-rise 里出现了 opacity —— 动画期间全站毛玻璃会失效：\n${rise[1].trim()}`,
  )
})

test('.main-content > * 的入场动画不带 fill-mode', () => {
  const matched = /\.main-content > \* \{\s*animation:\s*([^;]+);/.exec(clean)
  assert.ok(matched, '找不到 .main-content > * 的 animation 声明')
  // 页面根节点带 fill-mode 会让整页**永久**是 backdrop root，里面的 backdrop-filter
  // 采不到壁纸，毛玻璃就只剩「不透明度生效、模糊没效果」。
  // 这里没有 animation-delay，to 帧又与自然状态逐字相同，fill-mode 本来就多余。
  assert.ok(
    !/\b(both|forwards)\b/.test(matched[1]),
    `.main-content > * 的动画带了 fill-mode：${matched[1]}`,
  )
})

test('.kpi-grid > * 的错峰只能用 backwards，不能用 both/forwards', () => {
  const matched = /\.kpi-grid > \* \{\s*animation:\s*([^;]+);/.exec(clean)
  assert.ok(matched, '找不到 .kpi-grid > * 的 animation 声明')
  // 这四条就是 .metric-card，**自己就带 backdrop-filter**。`both` 的 forwards 会让动画的
  // to 帧一直生效 → 元素永久是 backdrop root → 模糊一辈子不生效
  // （实测高频 10.1，与纯壁纸 12.7 同档 = 完全没糊）。
  // 必须有 backwards：靠 animation-delay 错峰，没有它延迟期间会先闪一下实体卡片。
  assert.match(
    matched[1],
    /\bbackwards\b/,
    `.kpi-grid > * 缺 backwards —— 延迟期间会闪一下实体卡片：${matched[1]}`,
  )
  assert.ok(
    !/\b(both|forwards)\b/.test(matched[1]),
    `.kpi-grid > * 用了 both/forwards —— 会让这四张卡的模糊永久失效：${matched[1]}`,
  )
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

test('壁纸下不给裸文本加任何「看得见的底」，只靠 data-bare-text-tone 换字色', () => {
  // 五档「给文字加东西」全被否，最后一条的原话是「加了一个背景框，看起来好丑啊」。
  // 现在只剩「只改文字颜色」：wallpaperTone.ts 采样壁纸后给元素打
  // data-bare-text-tone，样式表按它切深/浅两套小字令牌。
  // 这条守卫做两件事：① 不许再冒出给裸文本加底的规则；② 两套字色必须真的存在。

  // 1) 令牌规则（亮/暗）里仍然只允许颜色。
  const tokenRules = [...clean.matchAll(/:root((?:\[[^\]]*\])+)\s*\{([^}]*)\}/g)]
    .filter((match) => match[1].includes('data-wallpaper="on"'))
  assert.equal(tokenRules.length, 2, `壁纸令牌规则应当正好 2 条（亮/暗），实际 ${tokenRules.length} 条`)
  for (const rule of tokenRules) {
    assert.ok(
      !/background:/.test(rule[2]),
      `令牌规则里不该出现 background：${rule[0].replace(/\s+/g, ' ').slice(0, 120)}`,
    )
  }

  // 2) 壁纸档下任何**碰到页脚/分页**的规则，都不许带垫底类声明。
  //    按「选择器里出现 dashboard-footer 或 pagination」整条扫，比盯死某几个
  //    选择器更难绕过去 —— 换个写法（> span / :last-child / 加个新类）照样会被抓到。
  const offenders: string[] = []
  for (const rule of clean.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (!/data-wallpaper="on"/.test(rule[1])) continue
    if (!/dashboard-footer|pagination/.test(rule[1])) continue
    if (/background:|backdrop-filter:|border-radius:/.test(rule[2])) {
      offenders.push(`${rule[1].trim()} { ${rule[2].trim()} }`)
    }
  }
  assert.deepEqual(offenders, [], `页脚/分页又出现了垫底类声明（五档全被否，别再试）：\n${offenders.join('\n')}`)

  // 3) 两套自适应字色必须在，且方向不能反：深底那套必须比浅底那套**更亮**。
  const onDark = /\[data-bare-text-tone="on-dark"\]\s*\{([^}]*)\}/.exec(clean)
  const onLight = /\[data-bare-text-tone="on-light"\]\s*\{([^}]*)\}/.exec(clean)
  assert.ok(onDark, '缺少 data-bare-text-tone="on-dark" 那套（深底要用浅字）')
  assert.ok(onLight, '缺少 data-bare-text-tone="on-light" 那套（浅底要用深字）')
  const tokenLuminance = (body: string): number => {
    const hex = /--text-muted:\s*(#[0-9a-f]{6})/i.exec(body)
    assert.ok(hex, `这套缺 --text-muted：${body.trim()}`)
    return relativeLuminance(hex[1])
  }
  assert.ok(
    tokenLuminance(onDark[1]) > tokenLuminance(onLight[1]),
    'on-dark 那套（给深底用的）必须比 on-light 那套更亮，否则方向反了',
  )
})

/* ---- 6. 半透明面必须都被毛玻璃兜住 ---- */

/**
 * 收集所有「带 `backdrop-filter: blur(var(--glass-blur…))`」的规则里的类名。
 *
 * 上面第 2 组只守了**取值集合**（不许出现第三种半径），守不住**覆盖范围**：
 * 一个元素只要压根不写 `backdrop-filter`，那条测试永远是绿的。
 * 而「漏写」正是 2026-09-29 那个 bug 的形状 —— 名单是手写的，每次「想起来一个补一个」。
 */
function glassBlurredClasses(source: string): Set<string> {
  const found = new Set<string>()
  for (const rule of source.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (!/backdrop-filter:\s*blur\(var\(--glass-blur/.test(rule[2])) continue
    for (const cls of rule[1].matchAll(/\.([a-zA-Z][\w-]*)/g)) found.add(cls[1])
  }
  return found
}

test('每个「自己就是一层表面」的元素都在毛玻璃名单里', () => {
  // 漏一个的后果：那块变成「清晰壁纸的取景窗」，壁纸原样满对比透出来。
  // 实测「可见清晰壁纸量」：正常玻璃面 1.7~8.9，漏掉的 16~68（差 6~30 倍），
  // 且对「模糊」滑块零响应。用户的原话是「这几个页面的透明度怎么调都不对」。
  const blurred = glassBlurredClasses(clean)
  const required = [
    // 视觉语言 v2 那一批（styles.css 5816 行）
    'metric-card', 'records-panel', 'model-card', 'stat-kpis', 'tool-card',
    'trend-workbench', 'usage-panel', 'trend-panel', 'site-panel',
    // 后面陆续补的
    'data-panel', 'compact-summary', 'resource-strip', 'sidebar', 'page-header--elevated',
    // 2026-09-29 补漏：筛选条 / 列表行 / 空状态 / 概览页工具区
    'filter-bar', 'content-state', 'tool-section',
    'site-row', 'channel-row', 'token-row', 'announcement-row',
  ]
  const missing = required.filter((name) => !blurred.has(name))
  assert.deepEqual(missing, [], `这些半透明面没有毛玻璃，会变成「清晰壁纸的取景窗」：${missing.join(', ')}`)
})

test('紧凑档的主题弹层必须收窄到能装进 184px 侧栏', () => {
  // 紧凑档侧栏 184px、锚点 `.sidebar-actions` 右边界 172；菜单 168px 时
  // `left = 172 - 168 = 4 < 12`，会触发 flyOut 挂到侧栏右边外面（实测溢出 163px）。
  const narrow = /:root\[data-sidebar-width="narrow"\]\s*\.theme-picker\s*\{([^}]*)\}/.exec(clean)
  assert.ok(narrow, '缺少紧凑档的 .theme-picker 宽度覆盖 —— 弹层会跑到侧栏外')
  const width = /width:\s*(\d+)px/.exec(narrow[1])
  assert.ok(width, `紧凑档的覆盖必须给出 px 宽度：${narrow[1].trim()}`)
  // 172（锚点右边界） - width >= 12（popoverPosition 的 margin）才不会被判为「放不下」
  assert.ok(
    Number(width[1]) <= 160,
    `紧凑档弹层宽 ${width[1]}px 仍然会让 left 落到 12px 边距以内，触发外挂`,
  )
})

/* ---- 7. 壁纸小字的对比度不能又被改浅 ---- */

function relativeLuminance(hex: string): number {
  const value = hex.replace('#', '')
  const [r, g, b] = [0, 2, 4]
    .map((i) => parseInt(value.slice(i, i + 2), 16) / 255)
    .map((c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4))
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function contrastRatio(a: string, b: string): number {
  const [hi, lo] = [relativeLuminance(a), relativeLuminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

test('壁纸下的小字必须真的比默认色更清楚（对比度下限）', () => {
  // 用户 2026-09-29 给的壁纸实测是中亮度（p10=75 / p50=118 / p90=162），
  // #767676 就是那个中位数亮度，拿它当探针底。
  // 这条守的是「方向」：以后有人为了好看把值改浅回去，这里会红。
  const PROBE = '#767676'
  const baseLight = /--text-muted:\s*(#[0-9a-f]{6})/i.exec(clean)
  assert.ok(baseLight, '找不到默认的 --text-muted')
  const blocks = [
    { name: '亮色', re: /:root\[data-wallpaper="on"\]\s*\{([^}]*)\}/ },
    { name: '暗色', re: /:root\[data-theme="dark"\]\[data-wallpaper="on"\]\s*\{([^}]*)\}/ },
  ] as const
  for (const { name, re } of blocks) {
    const block = re.exec(clean)
    assert.ok(block, `缺少${name}档的壁纸文字覆盖`)
    const value = /--text-muted:\s*(#[0-9a-f]{6})/i.exec(block[1])
    assert.ok(value, `${name}档缺少 --text-muted`)
    const got = contrastRatio(value[1], PROBE)
    const base = contrastRatio(baseLight[1], PROBE)
    assert.ok(
      got >= 3.0,
      `${name}档 --text-muted (${value[1]}) 在中亮度壁纸上的对比度只有 ${got.toFixed(2)}:1，低于 3.0:1`,
    )
    assert.ok(
      got >= base * 2,
      `${name}档 --text-muted 的对比度 ${got.toFixed(2)}:1 没有比默认色 (${base.toFixed(2)}:1) 好一倍`,
    )
  }
})
