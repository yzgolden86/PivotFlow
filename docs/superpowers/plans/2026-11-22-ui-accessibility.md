# 操作可达与可读性 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. 用户已授权连续本地执行、隔离验证与整合；无需重复交接确认。

**Goal:** 键盘可完成现有维护路径，窄屏可查看完整安全字段，大列表和跨页选择可预测。

**Architecture:** 共享焦点浮层栈管理 Modal、搜索、菜单及移动导航；详情复用只读字段；维护清单与权限选择采用有界分页。

**Tech Stack:** React 18、TypeScript、现有 Vite/Node test 与 Python Playwright，不新增依赖。

**Spec:** [操作可达与可读性设计](../specs/2026-11-22-ui-accessibility-design.md)

## Global Constraints

- 320px、390px、深浅主题、长文本与短视口；截图仅可视视口 JPEG。
- 保留 preview/apply、未知结果、真实取消、Key 检测最多 20 个与令牌秘密边界。
- 当前全部未提交源文件为基线；9 个保护文件只哈希，共享技能目录链接保持原样。
- 不提交/推送/发布/部署，不新增依赖，不运行生产程序/数据/上游/WebDAV。
- 既有 400,000 字节会话 gzip、150,000 字节路由 gzip、450,000 字节路由解压预算；记录旧 45 请求上限的起点状态。
- 重型检查串行；Vite 不与 Go embed/lint 重叠。

## Review Focus

- 嵌套搜索/菜单关闭、上层被程序关闭和触发元素被移除：只处理最上层按键并保留有效焦点与滚动锁。
- IME Enter、关闭后立即重开、部分读取失败和旧请求迟到：不误跳转、不覆盖新候选，并保留成功资源。
- 删除末页、过滤/翻页选择与外层表单中的跳页 Enter：页码有效、提交目标不变且不会意外保存。
- 长 URL/名称、窄屏、短视口和暗色：信息可完整读取，关闭和焦点不被裁剪。
- 大模型/Key/令牌清单、权限范围和秘密详情：DOM 有界，选择不丢，分页不改变副作用目标，不读取完整秘密。

### Task 1: 统一浮层焦点

**Files:** Create `console/src/components/focusLayers.ts`, `console/src/components/useFocusLayer.ts`; Modify `console/src/pages/siteShared.tsx`, `console/src/pages/ChannelKeysModal.tsx`, `console/src/App.tsx`, `console/src/components/AnchoredPopover.tsx`, `console/src/components/SourceMenu.tsx`, `console/src/components/HelpTip.tsx`, `console/src/styles.css`; Test `scripts/ui_accessibility_smoke.py`。

**Interfaces:** `useFocusLayer(ref, { open, modal, close, initialFocus?, returnFocus? })` 注册/撤销最上层交互，返回可用于 Portal 的层标记。菜单仍由调用方受控；定位接口不变。

- [x] RED：实际页面验证弹窗滚动锁、嵌套搜索/移动导航 Escape、隐藏/禁用控件 Tab、焦点返回及菜单方向键，记录失败原因。
- [x] GREEN：实现共享栈与顶层隔离；Modal 标题用 `useId` 关联；移除 Key/移动导航重复 trap，菜单用上下键/Home/End 并恢复按钮焦点。
- [x] VERIFY：`python scripts/ui_accessibility_smoke.py --group focus --output <本阶段目录>` 和 `npm test` / `npm run typecheck` 通过；将结果记入本阶段 ledger，不创建提交。

### Task 2: 全局搜索与资源恢复

**Files:** Modify `console/src/components/GlobalSearch.tsx`; Create `console/src/components/searchNavigation.ts`, `console/src/components/searchNavigation.test.ts`（若纯逻辑拆分有实际价值）；Test `scripts/ui_accessibility_smoke.py`。

**Interfaces:** 复用 Task 1 的 `useFocusLayer`；搜索最多 14 个候选，已成功资源独立保留。

- [x] RED：ArrowDown/ArrowUp/Enter、IME、关闭查询、部分失败/重试、立即重开与过期响应，断言路由、候选和请求次数。
- [x] GREEN：combobox/listbox、候选可见状态与滚动；所有关闭走同一路径，异步请求按代际和 source 更新，失败反馈和细化搜索提示。
- [x] VERIFY：search 浏览器组、完整 Node 测试和两份 TS 检查通过，记录结果。

### Task 3: 分页与明确选择范围

**Files:** Modify `console/src/pages/shared.tsx`, `SitesPage.tsx`, `AccountsPageV2.tsx`, `TokensPage.tsx`, `ChannelKeysModal.tsx`; Create `console/src/pages/listWindow.ts`, `listWindow.test.ts`, `siteAccountIndex.ts`, `siteAccountIndex.test.ts`; Test `scripts/ui_accessibility_smoke.py`。

**Interfaces:** `listWindow(total, page, pageSize)` 返回有效页码、页数、起止位置；`buildSiteAccountIndex(accounts)` 返回站点分组及健康数。分页支持有名称的直接跳页并防止外层表单提交。

- [x] RED：151 个令牌/2,000 个模型/120 个渠道/120 个 Key 的渲染界限；翻页/搜索保留选择、删除末页、非法跳页与筛选外选择提示，验证无意外保存/检测。
- [x] GREEN：主令牌 50/100、权限和 Key 50；本页全选准确命名、筛选外计数/清空；健康索引复用；保留全筛选 Key 检测目标最多 20 个。
- [x] VERIFY：lists 浏览器组和完整 Node/TS 通过；记录实际 DOM 数与可重复性能样本。

### Task 4: 窄屏完整信息与可见焦点

**Files:** Create `console/src/pages/ResourceDetails.tsx`, `console/src/accessibility.css`; Modify `SitesPage.tsx`, `ChannelsPage.tsx`, `TokensPage.tsx`, `SystemSettingsPageV2.tsx`, `console/src/main.tsx`（样式入口）；Test `scripts/ui_accessibility_smoke.py`。

**Interfaces:** `ResourceDetails({ title, facts, close })` 展示安全的 ReactNode 名值对；不触发网络写入或秘密恢复。Task 1 Modal 与 Task 3 分页/选择继续复用。

- [x] RED：320/390 深浅主题长字段的详情入口/完整值、弹窗横向溢出/标题关闭、分页与系统分类键盘行为、无完整令牌请求。
- [x] GREEN：安全字段详情及文本入口、局部断点/折行/焦点样式，分类标题/面板关联与方向键；保留默认视觉及 reduced-motion。
- [x] VERIFY：details 浏览器组、六个视口/主题及桌面等效 200% 探针通过；可视 JPEG 自查，Node/TS 通过。

### Task 5: 独立审查、整合和关闭

**Files:** Create `docs/console-accessibility.md`, `docs/superpowers/plans/2026-11-22-ui-accessibility-verification.md`; Modify `docs/README.md`, `docs/research/2026-11-19/README.md`, `recommendations.md`; Evidence 保存在工作区外本阶段归档。

- [x] VERIFY：完整前端/类型检查、构建、预算、合成浏览器、Go tests/lint/embed 构建顺序执行并读取结果；文档本地链接核对。
- [x] REVIEW：冻结 manifest/diff/source 与哈希；一位新上下文审查者独立检查，重要发现以 RED→GREEN 回归解决，原审查快照不改写。
- [x] INTEGRATE：主项目重新核对基线/保护/链接，先归档本阶段原文件，只复制阶段差异并移除过时生成资源；主项目验证及未改文件哈希检查。
- [x] CLOSE：ZIP CRC/内容哈希核对，非递归解除依赖 junction，核对分支 HEAD/路径/所有权后移除工作区和本阶段临时产物；记录真实测试数与空间回收，更新路线图状态。
