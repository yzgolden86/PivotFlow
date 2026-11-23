# 以模型／组组织路由策略 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. 用户已授权持续原生实现、本地验证、整合与清理，无需重复交接确认。

**Goal:** 从模型／统一组查看生效策略、准确候选层、实际尝试限制、费用依据和真实请求历史。

**Architecture:** 真实选择器与只读诊断共用候选形成；新管理员接口提供有界模型清单与模型诊断，旧渠道接口投影同一结果；React 工作台复用已有设置、渠道编辑和历史入口。

**Tech Stack:** Go 1.26.10／Gin／现有 SQL 与缓存、React 18／TypeScript／Vite／Node test、Python Playwright；不新增依赖。

**Spec:** [模型组路由设计](../specs/2026-11-23-model-group-routing-design.md)

## Global Constraints

- 完整当前 1,061 个源文件为基线，HEAD `90aee95761d8fa21ab3f292b3b2930f8fa24be08` 不变；9 个保护文件只哈希，主技能链接与依赖保持原样。
- 不提交／推送／发布／部署，不新增依赖，不运行生产程序、数据、真实上游或 WebDAV。
- 保留全局 balanced/sticky、健康评分、冷却兜底、Key 当前请求名范围与令牌过滤顺序；不新增组策略或持久配置。
- 诊断不推进渠道／Key／URL 游标，不消耗限额，不改变冷却、粘性或站点价目；失败用安全错误／警告。
- 模型／相关渠道默认 50、最大 100；别名预览 8／50；请求模型 512 字符，精确历史 191 字符；拒绝控制字符与重复参数。
- 320／390px、720×450 短窗口、深浅主题；可视视口 JPEG，DOM 有界；400,000 会话 encoded 字节、45 请求、单路由 150,000 gzip／450,000 decoded 字节。
- Go `-tags sonic`；Vite、浏览器、Go tests／lint／embed 等重型检查串行。

## Review Focus

- 统一组热更新、直接模型／别名／通配同时存在：单次诊断一致，候选并集及精确池优先准确。
- 令牌拒绝、Key 原请求名范围、已学协议不支持、RPM／并发已满：候选与可尝试状态分开，不能把被拒请求解释成成功。
- 健康跨基础层、受限令牌与粘性、无样本和冷却兜底：参考池及实际排序顺序保持，份额不伪装成命中概率。
- 读取失败、长名称、查询重复、分页末页／完整别名搜索：不输出秘密或无依据的零值，不按页面重算分母。
- 刷新后切换模型／协议／令牌、浏览器后退／重开／详情跳转：迟到响应不覆盖当前范围，不自动扩大令牌范围，历史模型／协议／令牌筛选同步恢复。

### Task 1: 共用只读候选与旧诊断一致性

**Files:** Modify `internal/app/selector.go`, `selector_cooldown.go`, `selector_balancer.go`, `admin_route_diagnostics.go`; Create `internal/app/route_selection.go`, `route_selection_test.go`（按责任需要）；Test `internal/app/admin_route_diagnostics_test.go`。

**Interfaces:** 共用内部 `routeSelectionSnapshot` 记录时间、冷却／费用／健康观察、候选来源及候选集合。真实选择入口继续返回 `[]*model.Config`；诊断消费者取得未推进 SWRR 的层级视图。旧 `buildChannelRouteDiagnostics(ctx, targetID, modelName, clientProtocol, tokenID)` 签名保留。

- [x] RED：`TestRouteDiagnosticsUnifiedPoolIncludesDirectAndAlias` 用直接／别名／通配渠道断言组并集及通配未入池；`TestRouteDiagnosticsWeightMatchesSelector` 用 4 启用 Key、其中 2 不允许模型／1 冷却断言实际权重 3，与范围内可用数分开；时段、无 Key 及真实候选状态用字面预期核对。
- [x] VERIFY RED：运行定向 Go，确认失败为旧诊断偏差；结果写入外置 ledger。
- [x] GREEN：提取现有候选形成步骤，诊断复用同一模型查询／过滤／兜底路径；权重直接复用 `calcEffectiveKeyCount`。健康中位基于令牌过滤前候选；候选仍保留尝试阶段跳过项。
- [x] VERIFY：定向组含 `TestRouteDiagnosticsReadOnlyDoesNotAdvanceRealRoundRobin`；插入重复诊断后真实合成代理序列保持手工预期。46 项通过；完整应用包测试在 Task 2 完成后统一运行；不创建提交。

### Task 2: 模型清单、诊断 API 与历史精确筛选

**Files:** Create `internal/app/admin_model_routes.go`, `admin_model_routes_test.go`; Modify `admin_route_diagnostics.go`, `server.go`, `protocol_capability.go`（只读快照）、`auth_service.go`（安全权限观察）；Modify `internal/model/request_trace.go`, `internal/storage/sql/request_trace.go`, `internal/app/admin_request_traces.go`；Test `internal/storage/sql/request_trace_test.go`, `internal/app/admin_request_traces_test.go`。

**Interfaces:** `GET /admin/routing/models` → `ModelRouteInventory`（version/items/total/offset/limit）；`GET /admin/routing/diagnostics` → `ModelRouteDiagnosticResponse`（version/observed_at/model/canonical_model/group_names/group_name_count/policy/token/channels/total/offset/limit/summary/warnings）。渠道条目复用并扩展 `ChannelRouteDiagnostic`；`RequestTraceFilter` 新增 `Model string`, `ClientProtocol string`，列表和统计同一 SQL builder。

- [x] RED：API 请求字面断言 105 模型分页、完整别名搜索、空组与停用模型、相关集合、无匹配结果；拒绝重复参数／无效协议／超长控制字符；404 令牌与安全 500；不发任何真实上游调用。
- [x] RED：实际生效的令牌模型／协议／allow/deny／过期／费用／并发准入；scope missing 不自动退回全局。协议按 URL 声明、auto 成功／不支持快照和请求族观察，Key 名称仍检查原始请求名。
- [x] RED：健康跨基础层、粘性不能前移／可前移、条件均衡份额、全冷却兜底；诊断与合成实际尝试及持久历史的模型、协议和失败转移一致。
- [x] GREEN：实现有界接口、完整集合求值后分页、明确各阶段原因／警告、安全费用配置／缓存观察；旧渠道／系统访问读口同一结果投影。
- [x] RED→GREEN：`TestRequestTraceExactModelAndProtocolFilters` 写入同前缀模型、另一协议／令牌及 SQL 特殊字符，列表／计数／汇总精确相等；管理员与令牌所有权仍强制；191 字符边界不截断后匹配。
- [x] VERIFY：完整应用 2,323 项通过；存储 520 通过、1 跳过；完整内部 Go 统一放 Task 4，记录真实数量。

### Task 3: 工作台、页面连接与浏览器任务

**Files:** Create `console/src/pages/ModelRoutesPage.tsx`, `modelRoutes.ts`, `modelRoutes.test.ts`, `console/src/modelRoutes.css`, `scripts/model_routes_smoke.py`; Modify `console/src/App.tsx`, `types.ts`, `api.ts`, `pages/ChannelsPage.tsx`, `SystemSettingsPageV2.tsx`, `RequestHistoryView.tsx`, `requestHistory.ts`（共享 URL 过滤）、`scripts/ui_accessibility_smoke.py`（纳入新路由预算）。

**Interfaces:** `getModelRouteInventory({ search, limit, offset }, signal)`、`getModelRouteDiagnostics({ model, client_protocol, token_id?, limit, offset }, signal)`；`modelRoutes.ts` 只负责可测试的显示／链接和范围逻辑，不复制路由计算。`/routing` 读取 URL 的 model/protocol/token_id；历史 URL model/client_protocol/auth_token_id 精确恢复。

- [x] RED：纯函数测试长模型、分层份额／无样本、费用范围与无价目、191 历史边界及 URL 语法编码。合成实际 React 页面断言新路由、范围选择、完整理由、精确历史入口、设置分组／渠道定位和无 POST／reveal／测试调用。
- [x] GREEN：清单与诊断独立加载／分页、范围变更即清空不适用快照、generation+AbortController+有界超时；错误留选项，令牌失败／缺失不扩大到全局；状态、原因、实际权重和费用可见。
- [x] GREEN：请求历史模型／协议过滤与 URL 后退同步；系统设置链接定位 routing/health；渠道诊断共用扩展条目的正确标签并链接模型工作台。
- [x] VERIFY：完整 Node（282 控制台、2 登录页）、两套 TS、Vite 和 25 个新增浏览器场景通过；迟到刷新／切换、API 失败重试、真正的 12 秒超时、列表缩小后的页码恢复、512 字符模型与 105 渠道分页、320／390／720 短窗口、深浅主题和键盘焦点；可视 JPEG 已查看。令牌模型页码误把加载中视为 0 条的回归已修复并验证跨页精确选择。资源 36 请求／335,008 encoded 字节，新路由 gzip 5,508／decoded 15,748 字节。

### Task 4: 全量验证、独立审查、整合与清理

**Files:** Create `docs/model-routes.md`, `docs/superpowers/plans/2026-11-23-model-group-routing-verification.md`; Modify `docs/routing.md`, `docs/request-history.md`, `docs/README.md`, `docs/research/2026-11-19/README.md`, `recommendations.md`, `CLAUDE.md`（入口及文档说明）。

- [x] VERIFY：完整内部 Go（35 包、4,931 项通过、6 跳过）、lint（0 issues）、Go embed、完整前端（282 + 2）及预算／25 新增浏览器顺序运行，读取所有结果；10 份文档、86 个本地链接通过。旧权限翻页问题另有 3 场景 GREEN；无未解决的重要错误。
- [x] REVIEW：冻结完整基线对比、源码及证据，新上下文 Lagrange 完成只读独立审查；精确历史排序规则与 CAS 耗尽两项经 RED→GREEN 修复，原审查者仅对这两项闭环核对，均关闭。完整源码及两份报告将归档。
- [x] INTEGRATE：主目录完整基线、9 保护文件及技能链接核对通过；原件归档后整合 102 个阶段文件、移除 25 个旧生成资产。1,089 文件匹配最终快照，987 个未涉及文件保持原样；主目录后端关键回归 13 项、完整模型路由浏览器 25 场景、精确权限翻页 1 场景及 10 文档／86 链接通过。
- [x] CLOSE：243 个证据文件压缩归档，源码／原件／证据 ZIP 的 CRC 与逐项 SHA-256 通过；非递归解除依赖 junction，归属路径／分支／HEAD 核对后清理自有工作区、临时分支、缓存和验证程序。移除逻辑字节 2,366,544,361，主库／密钥／原运行程序／技能链接／依赖保持原样；验证记录和下一阶段路线图已更新。
