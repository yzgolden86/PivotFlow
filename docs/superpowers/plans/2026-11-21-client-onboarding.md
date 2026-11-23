# 新手接入引导 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 将现有接入操作连接到准确的客户端配置与首个可追溯网关请求。

**Architecture:** 现有公共代理端点负责验证，前端独立模板生成模块负责编码，页面复用令牌表单和来源操作。公共模型列表增加受权限约束的显式协议视图。

**Tech Stack:** Go 1.26.10 + sonic、React 18、TypeScript、node:test、Playwright；不新增依赖。

**Spec:** [设计](../specs/2026-11-21-client-onboarding-design.md)

## Global Constraints

- 保留此前未提交实现；以当前文件哈希快照隔离实施；不提交、推送、部署或运行生产程序。
- 完整秘密只来自令牌创建/恢复，留在当前页面内存；不进入 URL、history 或 storage。
- 联测仅当前同源，credentials omit，redirect error；显式点击且不自动重发。
- 不新增依赖、数据库表、后台计费测试接口；来源维护使用现有操作。
- 保留现有主题、组件、占位符，合成数据验证 320/390px 与键盘。

## Review Focus

- 新选择之后迟到的令牌/模型/测试响应不能覆盖它，离开页后应停止等待。
- 下游认证失败不应清理管理员登录态；跨源地址或重定向不能获得令牌。
- HTTP 200 中的错误、incomplete、空文本、超限和非 JSON 不得声称首请求成功。
- Bash/PowerShell/JSON/TOML/Python 对引号、反斜杠、美元符号及模型别名精确编码。
- 资产不全、不可恢复令牌、协议/渠道限制和客户端地址不同均保留可理解的操作路径。

### Task 1: 协议模型视图与模板契约

**Files:** 修改 `internal/app/proxy_gemini.go`；新增 `internal/app/client_models_test.go`、`console/src/pages/clientConnection.ts`、`console/src/pages/clientConnection.test.ts`。

**Interfaces:** `normalizeGatewayRoot(input: string): string`；`buildClientArtifacts({client, shell, gatewayRoot, model, token}): ClientArtifact[]`；`clientProtocol(client): GatewayProtocol`。`/v1/models?protocol=<view>` 返回该协议、令牌权限内的清单。

- [x] 写入失败测试：显式 Codex/Anthropic 视图、无效查询 400、令牌限制仍生效；四种配置及地址归一/编码/秘密遮挡。
- [x] 运行 Go 定向测试及 node 定向测试，确认缺失行为导致失败。
- [x] 实现允许列表视图与纯模板函数；固定公共地址和官方字段。
- [x] 再跑定向测试及前端完整套件，Expected: 全部通过。

### Task 2: 真正代理联测与响应判定

**Files:** 新增 `console/src/gatewayClient.ts`、`console/src/gatewayClient.test.ts`、`internal/app/client_connection_test.go`。

**Interfaces:** `fetchGatewayModels(token, protocol, signal?): Promise<string[]>`；`probeGateway(token, protocol, model, signal?): Promise<GatewayProbeResult>`；只构建 `/v1`、`/v1beta` 相对路径。

- [x] 写入失败测试：四种结构与终态、失败分类、取消/超时、响应上限、同源选项、秘密不反射、模型视图与请求 ID。
- [x] 运行定向测试，Expected: 缺失模块或缺失行为 FAIL。
- [x] 实现单次请求、60 秒等待预算、2MiB 模型/256KiB 联测响应上限、严格成功判定与安全失败解释。
- [x] 合成 HTTP 上游经 Go 真实路由验证四种协议及认证/模型限制；Expected: 请求历史 ID 与实际路由结果一致。
- [x] 定向及前端完整套件，Expected: 全部通过。

### Task 3: 连续接入界面

**Files:** 新增 `console/src/pages/ConnectPage.tsx`；修改 `TokensPage.tsx`、`api.ts`、`types.ts`、`App.tsx`、`GlobalSearch.tsx`、`styles.css`、`vite.config.ts`。

**Interfaces:** 复用导出的 `TokenForm`；创建/恢复的明文只进入当前页 state；令牌页入口只传 `token` 数字 ID。

- [x] 准备合成浏览器验收脚本并确认页面入口/创建/配置/测试行为缺失。
- [x] 实现五步引导、协议模型、模板/环境/终端验证、遮挡/复制/下载与测试结果历史链接。
- [x] 增加序号/取消与选择守卫、阻止旧模型联测；令牌错误与管理员会话分离。
- [x] 联测期间禁用选择，支持停止等待与错误保留；空资产/不可恢复令牌有现有操作入口。
- [x] 测试、类型检查、构建，Expected: 全部通过；检查桌面/窄屏/暗色截图与键盘。

### Task 4: 验证、文档、集成和清理

**Files:** 新增 `docs/client-connection.md`、本阶段验证记录；更新 getting-started、docs 索引、CLAUDE、研究索引与建议状态。

- [x] 独立整阶段代码审查与最终问题修复；以当前源哈希对比确认审查范围。
- [x] 后端 `go test -tags sonic -buildvcs=false -count=1 -p 4 -timeout 10m ./internal/...`、lint、前端完整测试/类型/构建，Expected: 全部通过。
- [x] 合成浏览器验收覆盖 Review Focus；文档链接与配置文件实际解析，Expected: 全部通过。
- [x] 哈希校验备份并集成，核验未修改文件、受保护文件、发现链接与 HEAD 不变。
- [x] 主目录冒烟与构建验证；只删除本阶段工作区、临时程序和过期生成资产；保留压缩证据。

不创建 commit；用户现有未提交工作以哈希清单、备份和验证记录保留。
