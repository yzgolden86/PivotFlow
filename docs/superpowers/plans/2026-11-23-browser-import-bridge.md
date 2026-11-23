# 浏览器辅助与轻量导入桥接实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. 用户已授权持续原生实施、本地验证、整合与清理，无需重复交接确认。

**Goal:** 用户把浏览器或 All API Hub 的账号资料带入现有站点／账号流程，能核对来源、目标、验证范围和实际差异，并安全恢复未确认结果。

**Architecture:** 新增服务端规范化检查与单项固定预览，复用 Cipher、Provider、同站点门控、权威配置事务和 config-preview 生命周期。独立页面助手只导出本地文件；React 工作页复用现有差异组件及账号／渠道入口，秘密仅保留在当前页面内存。

**Tech Stack:** Go 1.26.10、Gin、现有 SQLStore／HybridStore、React 18、TypeScript、Node 内置测试、Python Playwright；无新增依赖。

**Spec:** [浏览器桥接设计](../specs/2026-11-23-browser-import-bridge-design.md)

## Global Constraints

- 必须 `-tags sonic`，Go 构建／测试 `-buildvcs=false -mod=readonly`，禁用依赖下载。
- 主目录 HEAD `90aee95761d8fa21ab3f292b3b2930f8fa24be08`；完整当前源码基线 1,089 文件已保存并复制到 `.worktrees/browser-import-bridge`。不提交、推送、发布或部署。
- 外置归属目录 `E:/Dev/tools/api/.tmp-browser-import-bridge`；归档 `E:/Dev/tools/api/archive/browser-import-bridge-2026-11-23`。仅此阶段目录可整理。
- 不读取真实 env／数据库／秘密；9 个保护文件只比较 SHA-256，原运行程序、主依赖和技能目录链接保持原样。所有 `{{NAME_ab12cd34}}` 原样保留。
- 请求 2 MiB、文档最多 64 条、单项凭证 16 KiB、名字 191 Unicode 字符、URL 2048 字符；预览复用 16 记录／64 MiB／10 分钟。
- 来源与 API 地址同 origin，目标按完整基础地址匹配；禁止自动凭文件状态开启维护、同步渠道或标记签到成功。
- 截图 viewport JPEG，不使用真实上游、生产服务或可见浏览器窗口。
- 本阶段原生实施，外置 ledger、brief 和证据代替依赖 Bash／提交范围的 SDD 脚本；源码差异按完整当前基线而非单独 HEAD 生成。

## Review Focus

1. 非默认端口、大小写、前缀、转义路径、恶意 URL 与同 JSON 重复键，不能使检查／验证／应用指向不同身份；Task 1 测试。
2. 同站点同名不同账号、稳定用户 ID 不同、Cookie 与 access-token 混合、凭证轮换及无法解开旧凭证，不能误更新或重复创建；Task 2 测试。
3. 同账号维护、验证取消、迟到响应、提交未知或应用中切换，不能错误标绿、自动重试或用新预览规避核对；Tasks 2、3 测试。
4. 浏览器助手在错误 origin／iframe／存储异常／多个 token／平台切换下，不能读取或导出错误身份，不能产生网络请求；Task 3 实际产物测试。
5. 外部备份中的健康、余额、签到、设置、请求头、密码和模型不能被当作新事实；秘密不得进入恢复 URL、存储、错误或预览 DTO；Tasks 1–3 测试。

---

### Task 1: 服务端文档规范化和安全检查

**Files:**
- Create: `internal/app/browser_import_document.go`, `internal/app/browser_import_inspection.go`, `internal/app/admin_browser_import.go`, `internal/app/browser_import_document_test.go`。
- Modify: `internal/app/server.go`（管理路由注册）。

**Interfaces:**
- `decodeBrowserImportDocument(raw json.RawMessage) (*browserImportDocument, error)`：接收原始 JSON 对象或保留原文件文本的 JSON 字符串，按两个已知格式提取，返回标准化条目及安全阻断原因。
- `(*Server).inspectBrowserImport(ctx context.Context, document *browserImportDocument) (*browserImportInspection, error)`：权威主库只读匹配，输出不含 credential 的安全条目，标识重复、候选站点／账号和忽略部分。
- `(*Server).HandleBrowserImportInspect(c *gin.Context)` 注册 `POST /admin/browser-import/inspect`；请求 `{document: ...}`。
- 标准条目含 `SourceOrigin, BaseURL, SiteName, Platform, AccountLabel, CredentialType, Credential provider.Credentials`；检查结果只含安全字段、索引、可用性、候选 ID 和匹配原因。

- [x] RED：使用当前管理员测试路由请求轻量文件及 All API Hub 4.0，期待 HTTP 200、字面来源 `https://source.example`、账号 `demo` 和零站点写入；当前为 404。
- [x] RED：表驱动钉住 2.0／3.0／4.0 字段、Cookie／Sub2API 刷新字段、64／65 条、2 MiB、16 KiB、191／192 字符、错误版本、重复 JSON 键、非 HTTP(S)／userinfo／query／fragment／异源／不同前缀、缺凭证与不支持单项。
- [x] IMPLEMENT：严格解析和安全 normalize，不读未知备份部分；完整地址／平台匹配、精确主凭证及正 ID／标签候选，重复条目标识只给索引。源文件文本保持在请求 `document` 字符串中以便检查原始重复键。
- [x] VERIFY：最终 Task 1 定向测试 24 个 pass 事件；原有配置预览／凭证基线 46 项通过。UTF-8 损坏输入另有 RED→GREEN。
- [x] RECORD：外置 ledger 记录实际结果和偏离，源码不提交。

### Task 2: 有界验证、去重与事务固定预览

**Files:**
- Create: `internal/app/browser_import_plan.go`, `internal/app/browser_import_verification.go`, `internal/app/browser_import_plan_test.go`。
- Modify: `internal/app/admin_browser_import.go`, `internal/app/config_preview.go`（安全元数据与结果 DTO）、`internal/app/server.go`。
- Modify: `internal/site/provider/provider.go`, `newapi.go`, `veloera.go`, `sub2api.go`；Create: `internal/site/provider/account_identity_test.go`（真实响应身份回归）。
- Modify: `internal/storage/site_store.go`, `sql/site.go`, `hybrid_site.go`（只修复凭证和当前未知状态，保留历史／渠道；不改数据库结构）。

**Interfaces:**
- 请求 `browserImportPreviewRequest`：`Document json.RawMessage, EntryIndex int, TargetAccountID int64, SiteName string, AccountLabel string, Mode string, SourceConfirmed bool`。
- `(*Server).previewBrowserImport(ctx context.Context, request browserImportPreviewRequest) (*configPreview, error)`；注册 `POST /admin/browser-import/preview`。
- `configPreview.BrowserImport *browserImportPreviewContext` 与 `configPreviewResult.BrowserImport *browserImportApplyResult`：来源、目标、模式、验证范围／时间与最终站点／账号 ID；无凭证和私有指纹。
- `browserImportPlan` 保存安全差异、源／目标配置指纹和已经准备的冻结凭证；apply 只做确定的本地写入。
- 模式 `verify` 总 deadline 90 秒；同实例站点 gate 与全局执行槽共用，既有账号浏览器验证租约 2 分钟，执行后释放；模式 `save_disabled` 零上游调用。

- [x] RED：验证成功只生成预览、错误来源确认不发请求；新账号停用／自动任务关闭，重复应用只写一次；文件输入改动不能改变已有预览。
- [x] RED：WAF、expired、取消、超时不改既有账号／历史；既有 site gate 和验证租约冲突时不上游；验证得到的用户 ID 不同拒绝目标更新。
- [x] RED：相同主凭证重复、同名异身份、目标其他站点、停用父对象、刷新令牌保留／轮换、未知验证身份分别钉住。所有 credential 输出均掩码或省略。
- [x] RED：输入／请求头 UserID 与真实 profile ID 不同或 profile 缺 ID 时，验证身份只能取 profile；携带过期刷新凭证的导入不得调用 refresh 或 key reveal。
- [x] RED：真实 SQLite 触发器让账号写入失败，期待站点也回滚；相关配置变化 stale、提交未知只读恢复、并发重复 apply、共享预算／认证跨 origin 请求仍受限制。
- [x] IMPLEMENT：从权威主库读输入和目标，验证前／后重查配置；网络在事务外。冻结已经验证或只停用保存的凭证，在 `WithConfigurationTransaction` 中重读匹配和指纹，Cipher 密封后创建或精确更新账号，保留开关／自动任务／历史／绑定／渠道。
- [x] IMPLEMENT：复用 preview 终态／过期／取消观察；成功只刷新相关安全配置缓存和价格来源，既有账号状态 unknown，结果给 ID 和下一步。失败仅稳定码／安全位置。
- [x] VERIFY：定向 `TestBrowserImport`／配置预览 58 项及真实身份 21 个 pass 事件通过；完整内部 35 个有测试包／5,001 pass 事件、0 fail，包含站点同步与凭证回归。6 项平台／样本／DSN 跳过已核对，最终冻结源码与审查在 Task 4。
- [x] RECORD：ledger 记录功能契约、结果和部署边界；源码不提交。当前 Task 2 定向 58 项通过，真实响应身份 21 个 pass 事件；共享宽回归与最终审查在 Task 4 完成。

### Task 3: 浏览器助手与连续导入工作页

**Files:**
- Create: `console/src/browserImportTypes.ts`, `console/src/browserImport.ts`, `console/src/browserImport.test.ts`, `console/src/browserCapture.ts`, `console/src/browserCapture.test.ts`, `console/src/pages/BrowserImportPage.tsx`, `console/src/browserImport.css`。
- Modify: `console/src/App.tsx`, `console/src/configPreview.ts`, `console/src/configPreview.test.ts`, `console/src/pages/ConfigPreviewPanel.tsx`, `console/src/pages/shared.tsx`, `console/src/pages/SitesPage.tsx`, `console/src/pages/AccountsPageV2.tsx`, `console/src/pages/SiteTaskProgress.tsx`, `console/src/pages/ChannelsPage.tsx`, `console/src/pages/SiteSyncPreviewModal.tsx`, `scripts/ui_accessibility_smoke.py`。
- Create: `scripts/browser_import_smoke.py`（可由独立测试工作者编写，控制器运行浏览器／Vite）。

**Interfaces:**
- `inspectBrowserImport(document: string, signal?: AbortSignal): Promise<BrowserImportInspection>`，`createBrowserImportPreview(request: BrowserImportPreviewInput, signal?: AbortSignal): Promise<ConfigPreview>`；调用同源管理 API，原文件文本不进持久存储。
- `BrowserImportPreviewInput` 与 Task 2 请求一致；UI 必须把实际源条目和目标变化计入 generation，取消或迟到响应不得改变新选择。
- `buildBrowserCaptureScript(options: {expectedOrigin?: string, platform?: string}): string`，`buildBrowserCaptureBookmarklet(options): string`；自包含函数序列化，只有有限 DOM／localStorage 和下载操作，不能依赖模块闭包。
- 新路由 `#/browser-import`，初始 `account_id`／`site_id` 只传正整数 ID；恢复 `preview_id` 仅重读元数据。成功链接 `#/accounts?focus_account_id=ID`、`#/channels?sync_account_id=ID` 和 `#/connect`；同步页只是预选目标，用户点击才生成上游预览。

- [x] RED：Node 请求／URL／恢复状态测试，钉住秘密只在 request body、未知结果禁重发、ID 编码和错误允许列表；Node DOM 测试或实际浏览器测试运行助手产物，不以源文本存在断言代替行为。
- [x] RED：基线构建的 browser smoke `route-exists` 应失败；完整 smoke 夹具先固定预期行为。
- [x] IMPLEMENT：遵循已有主题与字体，来源／目标两栏、10 条分页、掩码编辑、来源确认、验证／停用保存、取消／错误恢复、已有差异面板和安全下一步。
- [x] IMPLEMENT：自包含页面助手，限定 origin／顶层、显式读取允许存储键、多候选选择、用户确认导出、Cookie 手动填写、下载后清空；来源不符、存储受限和无凭证提供清楚状态。
- [x] IMPLEMENT：站点／账号／凭证恢复入口；浏览器相关任务只链接对应账号，不把打开页面或关闭助手当作成功。渠道同步预选 ID 后仍走当前完整差异流程。
- [x] VERIFY：298 console／2 auth、双 TypeScript、Vite 通过；切换来源后掩码 RED→GREEN，修复后完整桥接实际浏览器 39/39，助手真实产物执行、竞态／恢复／5 视口／资源预算均通过。最新路由 gzip 11,301 字节，默认会话 40 请求／355,914 encoded 字节。
- [x] RECORD：证据写外置目录，5 张 JPEG 已检查；用户说明、文档入口与 API 契约已写入，12 个文档／156 条本地链接通过。最终审查和整合状态由 Task 4 更新。

### Task 4: 最终验证、独立审查、整合与归档

**Files:**
- Create: `docs/browser-import.md`, `docs/superpowers/plans/2026-11-23-browser-import-bridge-verification.md`。
- Modify: `README.md`, `README.zh-CN.md`, `CLAUDE.md`, `docs/README.md`, `docs/site-management.md`, `docs/config-import.md`, `docs/research/2026-11-19/README.md`, `docs/research/2026-11-19/recommendations.md`，本计划。
- Create: `internal/app/browser_import_credential_review_test.go`, `internal/app/browser_import_review_test.go`；独立审查的四项问题及 optional-field 类型／null 边界回归。
- Modify: `console/src/pages/TokensPage.tsx`, `scripts/ui_accessibility_smoke.py`；Main 复验发现的初始化／快速翻页竞态与确定性浏览器回归。

- [x] VERIFY：完整内部 Go 35 个有测试包／5,079 pass／6 skip／0 fail；仅测试 lint 微调后 136 项定向通过，后端源码哈希一致，lint 零问题、embed 成功。首次 Main 共享 44/45 暴露 TokenForm 初始化／快速翻页竞态，确定性 RED→GREEN 修复后，298 console／2 auth、双 TypeScript／Vite、39 桥接浏览器、46 可达性及 4 账号／渠道回归均通过。失败夹具与跳过原因保留，不用合成结果声称部署完成。
- [x] REVIEW：完整阶段按基线 ZIP 审查，1 Important／3 Minor 均复现 RED→GREEN；原审阅者有界复审确认四项关闭，无新增 Critical／Important，最新完整相关检查通过。首次取得真实 UserID 时要求显式目标的规则已写用户文档；原审查与 follow-up 分开保留。
- [x] INTEGRATE：初次与补充整合均先归档被替换原件，最终 `task4-final-2` 快照已进入主目录；1,115 份源码一致，阶段 81 个变更文件、25 个过时基线资产，1,034 份未涉及文件保持原样。主目录后端 136 项、共享界面 46/46、桥接 39/39、12 份文档／156 条本地链接通过；HEAD、9 个保护资源、原技能链接与主依赖一致。
- [x] CLOSE：8 个 ZIP 的 CRC／逐文件 SHA-256、额外补充原件及哈希记录全部通过，462 份执行证据归档；非递归解除借用依赖 junction 后，仅清理本阶段工作区、分支和外置缓存。实际移除 2,592,431,088 字节的逻辑文件大小，主目录源码及保护资源无变化，结果见归档 `cleanup.json`。
- [x] RECORD：使用说明、验证记录和两份研究进度已更新，失败、纠正夹具与通过证据均保留。已确认的 14 项改进主线完成 13 项；下一项 P2 可选请求缓存尚未开始，真实上游、数据库与部署边界写入验证记录。
