# All API Hub 最新功能、可靠性与 UI 深度研究

审阅日期：2026-11-19，Asia/Shanghai。目标：[qixing-jk/all-api-hub](https://github.com/qixing-jk/all-api-hub)。固定点：`main@5c4101d93143df6b3b112ccd819a29f3f4ada78c`。研究浏览器资产管理、网关管理与验证工作流，并与 PivotFlow 当前实现对照。本轮未运行真实站点或扩展 E2E。

## 1. 结论与最新版本

All API Hub 的强项是把“找到来源 → 识别账号 → 管理凭据 → 比较价格 → 验证接口 → 配置客户端/网关 → 自动维护”变成浏览器内的连续任务。最近重点已从增加站点数量，转向认证恢复、签到后置确认、共享请求预算、原生额度语义、WebDAV 可恢复提交和验证工作区。

| 状态 | 核实值 | 来源 |
| --- | --- | --- |
| 默认分支 | main | [仓库 API](https://api.github.com/repos/qixing-jk/all-api-hub) |
| 当前 main | `5c4101d93143df6b3b112ccd819a29f3f4ada78c`，11-18T08:04:46Z | [固定提交](https://github.com/qixing-jk/all-api-hub/commit/5c4101d93143df6b3b112ccd819a29f3f4ada78c) |
| 最新 stable | v4.10.1，11-12T05:36:32Z | [Release](https://github.com/qixing-jk/all-api-hub/releases/tag/v4.10.1)、[Latest API](https://api.github.com/repos/qixing-jk/all-api-hub/releases/latest) |
| stable SHA | `d5d1d40d66eae71f2654e5905d15cc95f9b5ae49` | [标签原文](sources/github-hub-tags.json) |
| Nightly | 滚动 prerelease，指向 main；最近构建 11-18 08:07 UTC | [Nightly](https://github.com/qixing-jk/all-api-hub/releases/tag/nightly) |
| Nightly 包版本 | 4.10.1，但包含 stable 后 15 个提交 | [发布原文](sources/github-hub-releases.json) |
| Firefox AMO | 4.10.1，reviewed 11-12T05:47:07Z | [AMO](https://addons.mozilla.org/api/v5/addons/addon/all-api-hub/)、[保存原文](sources/github-hub-amo.json) |
| Chrome / Edge 商店 | 安装入口确认，本轮未可靠核实各自当前版本 | [Chrome](https://chromewebstore.google.com/detail/lapnciffpekdengooeolaienkeoilfeo)、[Edge](https://microsoftedge.microsoft.com/addons/detail/pcokpjaffghgipcgjhapgdpeddlhblaa) |
| 根许可证 | AGPL-3.0 | [LICENSE](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/LICENSE) |

Nightly 的 published_at 仍为 2026-03-15，表示滚动条目的早期发布时间。判断最新构建看 target commit、body、updated_at 和资产时间。main 的后续修复也不能算作商店 stable 已具备。

GitHub Stable 的 CRX 可按 updates.xml 更新，解压 ZIP 不会自动更新；Firefox 普通用户使用 AMO；Safari 没有通用商店版，需要 Xcode bundle。安装与更新说明应对应渠道。

## 2. 从本地 3.61 至 4.10 的演进

时间来自 GitHub Release 的 UTC published_at，changelog 自然日有时相差一天。重点窗口为 2026-09-08 至 11-19；本地 ZIP 修改时间不能作为发布证据。

| Release | UTC 发布时间 | 主要变化 |
| --- | --- | --- |
| [v3.61.0](https://github.com/qixing-jk/all-api-hub/releases/tag/v3.61.0) | 09-08 21:49:31 | APIyi、Sub2API 迁移、流/非流验证、分组 Key 补齐、签到分类、New API 令牌恢复 |
| [v3.62.0](https://github.com/qixing-jk/all-api-hub/releases/tag/v3.62.0) | 09-13 17:21:51 | 排序/重复检查、New API 高级渠道/多 Key 迁移、AgentRouter 登录签到、CLIProxyAPI、分层价格与 Secret Gist |
| [v4.0.0](https://github.com/qixing-jk/all-api-hub/releases/tag/v4.0.0) | 09-20 14:42:07 | 密度/字体/字号/主题、签到重发现、按账号组过滤模型、更长历史、存储竞态修正 |
| [v4.1.0](https://github.com/qixing-jk/all-api-hub/releases/tag/v4.1.0) | 09-25 17:25:48 | 当前 tab 账号优先、类型冲突解释、RightCode、AI Toolbox、公告轮询、验证结果过期 |
| [v4.2.0](https://github.com/qixing-jk/all-api-hub/releases/tag/v4.2.0) | 09-30 07:00:13 | 批量地址、adapter-owned OAuth、更多签到、RIX_API、Sub2API split-origin/页面自动化 |
| [v4.3.0](https://github.com/qixing-jk/all-api-hub/releases/tag/v4.3.0) | 10-04 21:57:22 | 最近标签页、书签恢复、请求头覆盖、权威奖励、发现诊断、Kimi/gpt-load/OmniRoute |
| [v4.4.0](https://github.com/qixing-jk/all-api-hub/releases/tag/v4.4.0) | 10-10 16:13:01 | Hiyo/Cubence/老张/Magpie、多 Key、模型同步排除、WebDAV 回读验证/无 MOVE 兼容、Cookie 隔离 |
| [v4.5.0](https://github.com/qixing-jk/all-api-hub/releases/tag/v4.5.0) | 10-16 12:03:22 | FoxCode/Dahl/Krill/肖恩、Turnstile、更多签到、Key 修复/导入、站点 badge、空态引导 |
| [v4.6.0](https://github.com/qixing-jk/all-api-hub/releases/tag/v4.6.0) | 10-20 18:18:33 | AgentRouter 登录、RelayFor/EMRank、渠道对应更新、原生额度与货币余额分离 |
| [v4.7.0](https://github.com/qixing-jk/all-api-hub/releases/tag/v4.7.0) | 10-26 17:27:28 | Vercel AI Gateway/Daya/PM-API/Requesty、新账号交互、挑战识别、自定义签到、多模态验证 |
| [v4.8.0](https://github.com/qixing-jk/all-api-hub/releases/tag/v4.8.0) | 11-01 20:40:02 | Novita、缺失 Token 恢复、URL/UUID 误识别修正 |
| [v4.9.0](https://github.com/qixing-jk/all-api-hub/releases/tag/v4.9.0) | 11-05 13:39:19 | AI/ML API/NanoGPT/Bai API、账号组独立报价、站点预算/429 冷却、模型选择器就地恢复 |
| [v4.10.0](https://github.com/qixing-jk/all-api-hub/releases/tag/v4.10.0) | 11-11 17:10:14 | Cookie、AnyRouter/Sub2API 登录恢复、余额历史、CC Switch、API/CLI 工作区、上下文/NIAH/Q&A、WAF、平铺 Key |
| [v4.10.1](https://github.com/qixing-jk/all-api-hub/releases/tag/v4.10.1) | 11-12 05:36:32 | Firefox 脚本满足 AMO 文件限制；主要功能来自前版 |

来源：[Release JSON](sources/github-hub-releases.json)、[CHANGELOG](sources/github-hub-CHANGELOG.md)。本地名为 all-api-hub-main 的目录其实 package.json=3.55.0，不能代表今天 main；旧材料已逐文件验证后归档，见 [快照说明](../../../../archive/reference-snapshots/README.md)。

### stable 后的 Nightly

| 变更 | 价值 |
| --- | --- |
| #1894 通知授权 | Firefox 已授权仍不能正确通知的问题 |
| #1895 浏览器对齐和会话清理 | Chrome Stable E2E、New API 临时会话生命周期 |
| #1888 账号/日期确认 | 不能把另一账号或前一天成功当成本次结果 |
| #1886 / #1883 方法与报告 | 端点元数据、来源本地化、精简反馈 |
| #1884 焦点 | 验证内容切换与兼容检查后的焦点稳定 |
| #1881 新领取流程 | Xingya 与 New API session fallback |
| #1878 加载/恢复 | 降低初始开销，恢复对话框 |
| #1876 工作区 | 流式结果、响应布局和操作连续性 |
| #1870 版本提示 | 发布后 notes 未就绪时重试提醒 |

具体 commit 链接保存在 Nightly 原文，不能将这些变更全部写成 AMO 4.10.1 已发布。

## 3. 功能与职责矩阵

All API Hub 读写站点管理接口、创建网关渠道、发测试请求。真正推理流量路由由目标网关负责；PivotFlow 的四协议转换、Key/URL 调度、WS 会话、冷却和费用准入仍属服务端数据面。

| 领域 | 当前能力 | 重要边界 |
| --- | --- | --- |
| 站点/账号 | New API/One API 系、Sub2API、Veloera、DoneHub、大量原生平台；标签、启停、搜索、排序、重复检查 | 账号识别/余额/Key/模型/签到需按能力分别判断 |
| 独立凭据库 | 无账号也可保存 Base URL+Key、来源、标签、请求头、验证/导出、原生额度 | 管理凭据与调用凭据有不同角色 |
| 浏览器发现 | 当前 tab、域名/title/header、被动身份、content-session extractor | tabs/cookies/content script 是浏览器能力 |
| 登录恢复 | Cookie、Token/OAuth、可续期凭据、临时页面、恢复/挑战结果保留 | 有效 Key 不代表浏览器 proof 满足 |
| 签到/兑换 | 方法发现、来源、分类、当天范围、手动路径/报告 | 挑战和页面改版会使能力降级 |
| 资产历史 | 余额、原生额度、消费/收入、日快照、用量/延迟聚合 | 本地聚合不是完整原始审计日志 |
| 模型/价格 | 原生目录、组倍率、Token/按次、多模态、条件价格、组报价 | 缺值保持缺失，不伪装零价 |
| 验证 | API/CLI、模型、流/非流、工具/结构化输出、多模态、上下文容量 | 只说明所测端点/模型/配置；压力测试产生费用 |
| 网关管理 | New API、Sub2API、CLIProxyAPI、OmniRoute、Magpie、gpt-load、AxonHub、Claude Code Hub、Octopus 等 | 目标接口不完全同构 |
| 客户端导出 | CC Switch、Cherry Studio、CCR、Kilo/Roo、Kelivo、AI Toolbox、Cursor++ 等 | deeplink/配置包可能含凭据 |
| 公告/通知 | 聚合公告，浏览器/Telegram/Webhook/飞书/企微/钉钉/ntfy | 通知失败与原任务结果分开 |
| 同步 | JSON、WebDAV、Secret Gist、领域选择、合并/上传/下载、加密 | 同步不等于完整云端历史 |
| UI | Popup/Side Panel、options、分面、搜索、主题/密度/字号、移动、任务恢复 | 源码/文档图不等于本轮 E2E |

来源：[固定 README](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/README.md)、[保存原文](sources/github-hub-README.md)、[固定树](sources/github-hub-tree.json)。

## 4. 账号发现、凭据与恢复的实现经验

### 分开平台识别、登录身份与操作能力

Onboarding registry 注册域名、title、兼容 header、被动 browser identity、content-session extractor 与隐私策略。被动识别不应执行会刷新凭据的 extractor，隐私判断按实际请求 URL，而非盲信 site hint。

结果可以是“检测到平台，但没有当前登录身份”“有身份，但不能提取调用 Key”“管理 Token 正常，但没有模型调用权限”。PivotFlow 已有 Provider 契约、管理凭据与调用 Key 分离，适合将这些状态显示清楚。

来源：[registry.ts](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/src/services/accountSiteOnboarding/registry.ts)。

### Cookie 与多账号的作用域

Cookie 请求隔离、AnyRouter/Sub2API 登录恢复、refresh token 保留和恢复结果跨验证窗口的修复，集中解决身份范围：一个账号的 Cookie 或页面结果不能污染同站点另一账号；WAF 也不能被共享请求层改写成凭据无效。

[Cookie operation session](sources/github-hub-src-src-services-apiTransport-cookieOperationSession.ts) 是按操作维护临时浏览器凭据的证据。服务端可以学习状态/作用域，无法自然读取用户浏览器会话。

### 筛选和批量操作

账号列表按站点类型、刷新健康、签到和启用状态筛选，选项带数量；支持当前 tab 优先、标签、复合搜索、虚拟化、拖拽和批量选择。工具栏区分当前可见、全部选中与隐藏项。

来源：[筛选](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/src/features/AccountManagement/components/AccountList/filtering/AccountFilterBar.tsx)、[批量工具栏](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/src/features/AccountManagement/components/AccountList/bulk/AccountBulkToolbar.tsx)。PivotFlow 已有同步进度与逐项反馈；增量是范围明确、跨页恢复及只重试失败。

## 5. 签到、日期与人机验证

签到需要方法发现、来源元数据、能力重发现、分类结果、权威奖励、账号身份、当天边界、WAF/captcha 和人工入口。Nightly #1888 尤其有价值：确认结果必须属于当前账号及其本地日期。另一账号、前一天或恢复窗口中的旧成功，都不能把本次任务标绿。

存储写失败后重复执行也是曾修复的问题。任务幂等记录和上游后置证据同样重要，应显示等待、执行、已签到、确认成功、部分结果、需重新认证、需浏览器、暂时失败和不支持，并给出下一步。

PivotFlow 已有能力位、历史、分类重试和浏览器入口。重点是账号/日期范围与恢复状态一致；不能把自动签到重新列成待开发功能。浏览器可以打开站点完成挑战；服务端使用稳定管理 API 或交给用户。增加重试不能解决 Turnstile，WAF HTML 也不等于账号失效。

证据：[CHANGELOG](sources/github-hub-CHANGELOG.md)、[Nightly](sources/github-hub-releases.json)、[#1896](https://github.com/qixing-jk/all-api-hub/issues/1896)。

## 6. 费用、原生额度、历史与验证

### 费用与额度语义

原生 5 小时/7 天额度、每日请求、货币余额、累计消费应独立。4.6 修正 allowance 与余额混用，4.9 按账号组独立报价，4.10 修正 Kimi 单位。缺失 meter 保持 absent，没有消费数据不能显示确认零消费。

模型价格包括 direct USD、New API ratio、组倍率、Token/按次、多模态/条件计价和 quota plan。比较需保留币种、单位、来源、采样时间和不适用项。PivotFlow 已对齐倍率、缓存和 billing_expr，应先统一价目估算说明，再开放成本策略。

来源：[modelPricing.ts](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/src/services/models/utils/modelPricing.ts)、[凭据契约](sources/github-hub-src-src-features-ApiCredentialProfiles-contracts.ts)。

### 有界历史同步

从支持站点的消费日志接口分页读取，限制页数/条目，使用 cursor 与隐私 fingerprint 去重，保存日/小时/模型/Token 聚合和延迟桶，保留未知覆盖、不支持和失败来源，Token secret 不进入历史。

来源：[sync.ts](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/src/services/history/usageHistory/sync.ts)、[历史说明](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/docs/docs/usage-analytics.md)。PivotFlow 已有代理日志与数据面板；可学的是覆盖时间/来源和上游账单差异，避免重造统计页。

### API/CLI 工作区

4.10 统一验证、可续跑上下文容量、NIAH 计划和 Q&A，识别 HTTP/SSE 上下文溢出。Nightly 流式展示、响应布局与焦点进一步稳定。[useVerificationWorkspace](sources/github-hub-src-src-features-Verification-api-useVerificationWorkspace.ts) 维护选择、设置区、详情 portal 与焦点，布局在 [CSS](sources/github-hub-src-src-features-Verification-api-verificationWorkspace.css)。

PivotFlow 已有直测和渠道测试。优先通过下游令牌走真实代理验证协议、路由、模型和首个输出，并链接同一请求。大上下文/质量测试作为用户显式发起的可选项目，注明成本和覆盖；单次 NIAH 不能证明模型身份。

## 7. 站点共享预算的关键细节

[siteRequestLimiter.ts](sources/github-hub-src-src-services-apiTransport-siteRequestLimiter.ts) 以规范化 origin 建进程内状态，含 token bucket、并发 slot、前/后台队列、pause/reset 和 backoff。类型冲突时收紧预算，不重新加满 token。

- 前台等待时保留 token/slot，自动扫描不会完全占满。
- 连续五个前台后给等待的后台一次机会，避免另一种饥饿。
- lease 区分调用者 result 与底层 completion。即使调用者先取消/超时，底层结束前仍占 slot，避免实际请求超过并发预算。
- 429 优先 Retry-After；无可信值时约 1 秒起步、指数到 30 秒、25% jitter；闲置状态有 TTL。
- 类型默认预算是扩展自己的保护值，不是上游承诺的配额。

来源：[调度契约](sources/github-hub-src-src-services-apiTransport-requestScheduling.ts)、[站点预算](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/src/services/accountSiteDefinitions/requestLimits.ts)。

PivotFlow 适合让余额、模型、公告、签到、价目表和同站点多账号共享控制面预算，配合重复合并、singleflight、错峰及取消。它与数据面 RPM/Key 冷却职责不同。

## 8. WebDAV、Gist 与可恢复同步

### 领域选择与删除

可选账号、书签、偏好、凭据 profiles 和标签；合并/只上传/只下载；WebDAV 与 Secret Gist 同时只运行一个服务。删除使用 tombstone，避免旧备份复活；冲突为 whole-account LWW，而非字段级 CRDT。旧客户端可能覆盖整个账号的新字段，多版本同步需明确升级/协调流程。

来源：[选择同步](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/src/services/webdav/sync/webdavSelectiveSync.ts)、[文档](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/docs/docs/webdav-sync.md)。

### 加密与提交

加密采用 PBKDF2-SHA-256、默认 250,000 次、随机 16-byte salt、AES-256-GCM 与 12-byte IV，envelope 标明格式/KDF/cipher。格式来源：[webdavBackupEncryption.ts](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/src/services/webdav/backup/webdavBackupEncryption.ts)。

[提交源码](sources/github-hub-src-src-services-webdav-backup-webdavBackupTransport.ts) 的核心过程：同目录临时上传 → 回读验证 → MOVE 到正式路径 → 再验证。只有 MOVE 明确 405/501 时使用直接 PUT。模糊写入错误先回读判断已提交/未提交/未知，不能直接再写或删除已有成功备份。409/500 等兼容覆盖也在读后判断，未知状态保留恢复副本。

这比“收到 2xx 就算成功”可靠。限制也明确：删除后覆盖有短窗口，扩展不维护完整云端历史，Gist 版本检查不一定原子。PivotFlow 已有 JSON/WebDAV；优先补写后验证、恢复演练和导入差异。tombstone/LWW 是多设备同步语义，不能未经设计套给单机备份。

## 9. 通知与调度

通知覆盖浏览器、Telegram、飞书、企微、钉钉、ntfy、Webhook；签到、WebDAV、模型同步、用量/余额历史、公告可各自开关。通道失败隔离，通知失败不把成功任务改成失败；测试通知则显示错误，点击可到相关设置。

来源：[taskNotificationService.ts](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/src/services/notifications/taskNotificationService.ts)。

浏览器 alarms 受关闭、休眠和移动后台冻结影响，多设备也可能重复。PivotFlow 的服务端定时器有独立生命周期；可以学任务反馈与幂等，不能将扩展 alarms 当全天在线调度证据。

## 10. UI：实际图片与源码证据

### 快速面板与完整工作区

Popup/Side Panel 的账号、书签、API 凭据视图面向快速查看和跳转；options 面向录入、筛选、批量、历史和配置。桌面展开/折叠侧栏，移动抽屉，内容居中/全宽，主题/字号/密度，Ctrl/Cmd+K、lazy load、预加载和目标定位体现统一信息架构。

来源：[popup App](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/src/entrypoints/popup/App.tsx)、[viewRegistry](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/src/entrypoints/popup/viewRegistry.tsx)、[options](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/src/entrypoints/options/App.tsx)、[主题](https://github.com/qixing-jk/all-api-hub/blob/5c4101d93143df6b3b112ccd819a29f3f4ada78c/src/constants/theme.ts)。

### 本轮人工看过的固定 HEAD 文档图

| 图 | 可直接观察 | 学习点和边界 |
| --- | --- | --- |
| [账号管理](sources/github-hub-doc-account-manage.png) | 左侧平台/账号，行中链接/Key/编辑，右侧金额；顶部多个筛选和批量入口 | 轻行布局利于扫读，金额对齐；窄屏不能仅靠缩字体。图中账号/金额是演示，不是实测 |
| [Popup](sources/github-hub-doc-account-manage-pop-up.png) | 今日消费/余额切换、更新时间、大新增按钮、Key/模型快捷入口、单账号行 | 快速面板只承担少量任务，不复制完整仪表盘 |
| [API 凭据库](sources/github-hub-doc-api-credential-profile-list.png) | 按 Base URL 左侧分组，右侧凭据/协议/最近验证；5h/7d 进度、reset、未提供、更新时间、快捷操作 | 原生额度与货币分开，覆盖“2/6 有数据”，未知值不写 0；图内日期 11-04，不能当今天运行结果 |
| [API 测试](sources/github-hub-doc-web-ai-api-check-dialog.png) | 提取原文本、URL/Key、API 类型、模型、流模式、客户端模拟；独立模型/文本/工具/结构化测试；保存与测试分开 | 一次测试通过不代表完全兼容；提示说清凭据存储范围。此图不是后续新工作区的运行证明 |

图片来自固定 HEAD 的 docs，而不是本轮运行扩展生成。明显的 example.test、fixture 和 e2e Key 表明它们是演示数据。源 URL/哈希见 [图片清单](sources/github-hub-doc-images.json)。旧 3.61 图只作为历史视觉参照；AMO API 的六张官方 preview 也可能滞后，没有用于推断某项当前行为。

### 状态、焦点与响应布局

刷新健康、签到、启用、数据覆盖、凭据验证和原生 quota 是不同状态；就地加载与恢复使用户不必反复开关页面。4.x 的重点还包括 Tooltip 主题、shadow-root、reduced-motion、大目录减少重复计算、平铺 Key 和选中结果定位。

[Modal 源码](sources/github-hub-src-src-components-ui-Dialog-Modal.tsx) 基于 Radix，维护嵌套/替换模态的 focus session、恢复目标和 workflow。它体现了“页面内容换了，焦点也要跟任务保持有效”的具体实现；不等于本轮验证了所有辅助技术。

与 PivotFlow 对照：[UI 审阅](pivotflow-ui-audit.md) 已确认移动侧栏有 trap/恢复，但通用 Modal 与全局搜索不一致。优先统一现有组件的行为，不需要新增整套主题。

## 11. 可借鉴内容与采用边界

总体优先级在 [recommendations](recommendations.md)；下表不是同时开工的功能包。

| 用户问题 | All API Hub 的经验 | PivotFlow 已有程度与增量 | 顺序 / 验收 |
| --- | --- | --- | --- |
| 刚接入不知道下一步 | 发现、验证、保存、Key、导出形成工作流 | 已有站点/账号/渠道/令牌/测试；补跨页上下文与首个请求 | P1，源到客户端成功有连续引导 |
| 账号多难筛选 | 分面带数量、当前可见与隐藏选中项 | 搜索/筛选/批量已有；统一范围，保留/覆盖预览 | P1，翻页/筛选不会误操作全库 |
| 识别失败含义模糊 | 平台/身份/提取/能力分层 | Provider/凭据/Key 已分离；显示 detected/expected、auth layer、能力和下一动作 | P1，不把缺浏览器上下文说成凭据失效 |
| 登录/挑战状态丢失 | Cookie 验证、renewable token、验证后恢复 | 已有 WAF/Cookie 修正与人工入口；补稳定生命周期 | P1，旧结果不覆盖新失败；成功属于当前账号 |
| 签到误报或重复 | 方法来源、后置确认、账号/日期、写失败幂等 | 自动签到与历史已有；验证来源/日期/恢复边界 | P1，不重做签到，确认真实结果 |
| 批量任务打爆上游 | origin 预算、429、foreground reserve、completion lease | 数据面限额/调度已有；补控制面共享预算 | P1，同站点多账号和多任务不会绕过并发 |
| 费用无法比较 | 账号组独立报价、单位与来源、缺值保留 | 站点/组/缓存/按次已有；显示估算/来源/时刻和不适用项 | P1，别把“最低价”当唯一选路依据 |
| 只看余额不知质量 | 有界历史聚合、延迟和覆盖 | 代理日志/数据面板已有；补时间与来源覆盖、最终/attempt 区分 | P1，避免重复统计口径 |
| 测试成功客户端仍失败 | API/CLI 工作区、流错误、可恢复结果 | 直测/渠道测试已有；走下游令牌真实代理联测 | P1，失败关联请求；上下文压测可选且提示成本 |
| 备份成功却不可恢复 | 临时写、回读、提交确认与恢复副本 | JSON/WebDAV 已有；写后校验、差异预览、独立恢复 | P1，收到 2xx 不等于可恢复；多设备合并另设计 |
| 用户只想快速处理异常 | Popup 与 options 分工、就地恢复 | 已有首页与工作页；首页提供需处理项和下一步 | UI P1，不新增重复总览页 |
| 凭据散在网页/客户端 | 独立库、来源、目标描述、导出预览 | 渠道直接 Key/账号投影已有；可先增加来源与配置生成 | P2，先复用现有凭据，避免两套存储 |
| 想复用浏览器登录 | 当前 tab / content script / session extractor | 服务端无法读取浏览器状态；受限 companion 导入可探索 | P2，用户发起、一次授权、范围预览、去重 |
| 通知无上下文 | 任务×通道、失败隔离、跳相关设置 | Webhook/Telegram 已有；任务结果与通知投递状态分开 | P1/2，通知失败不改原任务结果 |

[批量导入预览](sources/github-hub-src-src-services-managedSites-batchImport-tokenBatchImportPreview.ts) 是具体可参考的设计：先构造目标/重复/修复计划，再执行。PivotFlow 的账号到渠道投影已经幂等，适合在这一真实计划上提供差异，不能另用一套前端推算。

## 12. 公开问题说明了哪些持续风险

### Sub2API panel proof：浏览器边界

[#1898](https://github.com/qixing-jk/all-api-hub/issues/1898) 在 11-19 创建、抓取时 open。报告的新版站点要求 X-Panel-Proof：由该 origin IndexedDB 的不可导出 P-256 私钥签 ES256 JWT，关联方法、URL、body 和 access token。仅有 Bearer 仍 401；站点页面带 proof 可成功。来源原文保存在 [issue JSON](sources/github-hub-issue-1898.json)。

这是用户报告，本轮没有复现或验证其所有密码学细节，也没有证明 main 已解决。它说明认证可能要求浏览器持有的 origin 能力，不能把“缺 proof”误归成“Key 无效”。服务端使用正式 API、部署允许的认证途径或明确 browser_required；若要 companion，需独立设计授权、来源和失败恢复。

### 识别与签到发现仍会漂移

[#1897](https://github.com/qixing-jk/all-api-hub/issues/1897) 报告 chatanywhere.tech 已登录仍 invalid_response；[#1896](https://github.com/qixing-jk/all-api-hub/issues/1896) 报告 discovery=unsupported、selection=none、no_registered_candidates、scan=partial。两者抓取时 open，未复现。

它们说明域名、登录态、账号解析和签到方法是独立能力，扫描覆盖不足应显示 partial，而非断言站点不可能支持。PivotFlow 适合维护版本化 capability、诊断与人工入口；不要用不受限自动扫描替代。

其他真实修复线索包括 Cookie 污染、storage 读改写竞态、签到写失败后重复、WAF 被共享层丢失、登录结果被验证覆盖、旧测试结果长期保留、并发账号/日期归属和 New API 会话残留。可靠性提升集中在身份、日期、请求和提交的正确边界。

## 13. 权限、来源与复用

AMO 当前 4.10.1 兼容 Firefox >=104、Android >=120；必需权限包括 tabs/storage/unlimitedStorage/alarms/contextMenus/scripting/all_urls，可选 cookies/webRequest/notifications/clipboard/bookmarks 等。范围与多站点产品有关，但不能以权限列表推导所有站点均可操作。AMO 的数据类别为空，源码有 Product Analytics 结构；本轮没有审计实际上传字段和用户开关，不能据元数据宣称完全没有遥测。

根 LICENSE 为 AGPL-3.0，AMO 当前元数据却写 MIT。该冲突已在原文中核实，不能用 AMO 的标签覆盖仓库许可证。[README](sources/github-hub-README.md) 还有商业授权和历史 MIT 来源说明。工程复用应以具体代码来源、范围和许可证核对；本文只提出独立实现的行为和架构经验。

## 14. 证据文件与未实测事项

公开 API、tag、main、Release、AMO、issue 与固定源码以原始 HTTP 文件保存，见 [raw capture manifest](sources/github-hub-raw-capture-manifest.json)。核心九份源码及 SHA-256 在 [key source manifest](sources/github-hub-key-source-manifest.json)，四张文档图在 [image manifest](sources/github-hub-doc-images.json)。其他固定 source URL 已在正文标明。

未启动扩展、未使用真实账户、未跑三个浏览器商店版、实际 WebDAV/多设备同步、长时负载或压力验证；Chrome/Edge 版本未可靠核实；公开 issue 未逐项复现；AMO/MIT 冲突原因未知；Product Analytics 载荷未审计；文档图不能证明当前工作区实际运行。所有性能、可靠性和费用收益均是待验收的建议，没有虚构改进百分比。

主线与历史资料、全部参考项目的对照见 [研究入口](README.md) 和 [跨项目矩阵](feature-comparison.md)。
