# P1 运行健康面板 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking. 用户已授权继续本阶段，连续执行；在最终验证期间进行独立代码审查。

**Goal:** 管理员能够从首页与运行健康页识别当前后台故障、积压、恢复与历史损失。

**Architecture:** 给真实后台操作增加并发安全的状态记录，扩展现有只读运行指标并集中派生状态。前端消费受控状态，独立刷新、保留失效快照并显式投影诊断。

**Tech Stack:** Go/Gin、SQLite/MySQL/PostgreSQL 存储抽象、React 18/TypeScript/Vite、node:test、Playwright。

**Spec:** [2026-11-21-runtime-health-design.md](../specs/2026-11-21-runtime-health-design.md)

## Global Constraints

- `-tags sonic`、`-buildvcs=false`；无新数据库表、环境配置或前端依赖。
- 保留原有指标字段语义、队列预算、保留策略、计费和恢复契约。
- 不返回错误原文、路径、DSN、凭据、Token 哈希和事件 ID；指标读取不访问数据库。
- 队列 80% 提示压力；60 秒内丢弃提示近期损失；结算最老积压超过 30 秒提示等待。
- 前端轮询 10 秒，超时 8 秒，快照超过 30 秒过期；隐藏页面暂停，恢复可见时更新。
- 所有修改在 `.worktrees/runtime-health`；以 946 个当前源码文件的 SHA256 基线为准，不以 HEAD 覆盖用户的未提交工作。
- 保护主目录九个生产/配置文件、依赖目录与发现链接；不创建主分支提交，不推送、发布或部署。

## Review Focus

- 并发操作和重复结束回调：计数不能重复或出现负活动数；已取消操作不能抹去未恢复失败。Task 1 并发及取消测试。
- 通道已空但批次未写完：`buffered` 与活动操作仍可见。Task 2 阻塞写入测试。
- 混合副本无关操作成功：旧差异仍需核对；可选启动恢复失败仍显示。Task 3 真 SQLite 故障测试。
- 无流量、关闭功能、历史失败和未知字段：不能冒充当前健康或永久判定故障。Task 4 状态表测试与 Task 5 缺接口检查。
- 刷新失败、页面隐藏、取消与额外秘密字段：保留失效快照，不重叠请求、不泄漏诊断。Task 5 浏览器故障及脱敏测试。

---

### Task 1: 真实操作监控与共享 DTO

**Files:**
- Create: `internal/monitoring/operation.go`、`operation_test.go`
- Create: `internal/model/runtime_health.go`

**Interfaces:**
- Produces: 零值可用 `monitoring.OperationMonitor.Begin() func(error)`、`Snapshot() monitoring.OperationStats`。
- Produces: `model.StorageRuntimeStats`、`model.ReplicationRuntimeStats`、`model.StartupRecoveryStats`，供存储和 API 使用。

- [x] 先写 `TestOperationMonitorFailureRecovery`、`TestOperationMonitorCancellationPreservesFailure`、`TestOperationMonitorConcurrentCompletion`，断言累计失败保留、成功清除 `failing`、记录恢复时间、结束只计一次、100 个并发任务活动数最终 0。
- [x] 运行 `go test -tags sonic -buildvcs=false ./internal/monitoring`，Expected: 因缺实现失败。
- [x] 实现以 mutex 保护完整快照，使用 `errors.Is` 判断超时、递归确认组合错误全部取消，不存错误文本。
- [x] 同命令验证，Expected: PASS。

### Task 2: 结算、日志写入与清理结果

**Files:**
- Modify: `internal/app/billing_service.go`、`log_service.go`、`request_trace_service.go`、`proxy_error.go`、`server.go`
- Create: `internal/app/runtime_health_services_test.go`

**Interfaces:**
- Consumes: Task 1 OperationMonitor。
- Produces: journal/结算/日志/请求历史/失败统计的操作快照、最后丢弃时间、批次等待数、普通及调试清理结果、计划时间；`cleanupOldLogs(ctx context.Context) error` 为循环真实调用入口。

- [x] 先写失败测试：普通清理失败可见且随后成功恢复；未启用清理不执行；调试读取/删除失败及取消可见；日志最终未保存数；请求历史正在阻塞写入时通道为空仍有 `buffered`；结算暂时失败后成功不重置累计计数。
- [x] 运行相应 `go test ... ./internal/app -run 'TestRuntimeHealthServices'`，Expected: 缺少状态或清理入口而失败。
- [x] 包装实际存储调用，记录每次操作结果和最终记录损失；在接收/完成批次时维护 buffered；普通清理停止忽略错误并采样日志。保持原有重试和清理频率；关闭取消单独记录。
- [x] 同命令与已有 `Test(StartCleanupLoop|RequestTraceWorker|FlushLogs|BillingRetries|TokenStats)` 定向回归，Expected: PASS。

### Task 3: 混合同步与启动恢复观测

**Files:**
- Modify: `internal/storage/hybrid_store.go`、`sync_manager.go`、`factory.go`
- Create: `internal/storage/runtime_health_test.go`、`internal/storage/sql/runtime_metrics.go`

**Interfaces:**
- Consumes: Task 1 DTO 和 OperationMonitor。
- Produces: SQLStore/HybridStore `StorageRuntimeStats() model.StorageRuntimeStats`；HybridStore `SetStartupRecoveryStats(model.StartupRecoveryStats)`；SyncManager `StartupRecoveryStats() model.StartupRecoveryStats`。
- Extends: 现有 `RequestTraceReplicationStats` 保留 queued/capacity/dropped/failed，添加启用、操作状态和最后丢弃时间。

- [x] 先写失败测试：日志/请求历史复制失败后后续成功；队列丢弃及关闭丢弃；副本失败后无关成功仍需核对；配置恢复成功但日志/历史恢复失败分别保留；`logDays=0` 明确未恢复历史；指标读取关闭数据库后仍可返回。
- [x] 运行 `go test ... ./internal/storage ./internal/storage/sql -run 'TestRuntimeHealthStorage'`，Expected: 缺少能力/状态而失败。
- [x] 为真实同步操作记录结果，在工厂传递启动恢复快照；不改变恢复算法或成功结算的错误返回。
- [x] 运行新增测试及已有混合同步、请求历史和结算回归，Expected: PASS。

### Task 4: 管理员运行指标和确定性状态

**Files:**
- Modify: `internal/app/admin_active_requests.go`
- Create: `internal/app/runtime_health.go`、`runtime_health_test.go`

**Interfaces:**
- Consumes: Tasks 2/3 的进程状态。
- Produces: 原有运行指标扩展字段及 `health.version=1`、受控组件状态/原因；`collectRuntimeMetrics(now time.Time)` 为同一快照的收集入口。

- [x] 状态表测试覆盖 blocked、error、warning、working、healthy、idle、disabled、unknown；累计失败但已成功不永久报错；过期丢弃不报近期损失；副本风险不清除；WebSocket 累计拒绝不当作当前故障。
- [x] API 回归测试原有字段及管理员认证，使用会 panic 的 Store 验证读指标不访问存储；合成错误带秘密也不进入响应。
- [x] 先跑观察失败，再实现中央状态规则和返回；无新管理写操作。定向回归 Expected: PASS。

### Task 5: 首页健康摘要与详情页

**Files:**
- Create: `console/src/pages/HealthPage.tsx`、`runtimeHealth.ts`、`runtimeHealth.test.ts`、`useRuntimeMetrics.ts`、`RuntimeHealthSummary.tsx`
- Modify: `console/src/api.ts`、`types.ts`、`App.tsx`、`components/GlobalSearch.tsx`、`pages/DashboardPage.tsx`、`styles.css`

**Interfaces:**
- Consumes: Task 4 API；`getRuntimeMetrics(signal?: AbortSignal)`。
- Produces: `#/health` 和首页摘要；純函数处理快照有效性、状态文案和白名单诊断；hook 负责单次请求与轮询生命周期。

- [x] 先写纯逻辑测试：缺 health/版本不匹配不可用；过期或失败的上次快照不能当当前健康；未知状态保持未知；诊断排除额外秘密与不受控字符串。
- [x] 运行 `node --test src/pages/runtimeHealth.test.ts`，Expected: 缺实现失败；实现后 Expected: PASS。
- [x] 实现详情分区、清理提示、资源压力与说明，加入导航/搜索；首页业务概览失败时健康摘要仍可读。
- [x] 轮询不重叠，支持隐藏/返回、暂停、8 秒超时和卸载取消；错误提示保留在页面。复制过期诊断带快照状态。
- [x] `npm run typecheck`、`npm test`、`npm run build`，Expected: PASS。
- [x] 合成浏览器检查正常、阻断、同步/清理失败、恢复、无流量、关闭、旧接口、刷新失败/超时、暂停、隐藏/返回、移动端、深浅主题和键盘；Expected: 所有断言通过且无未预期事件。

### Task 6: 独立审查、验证、整合与清理

**Files:**
- Create: `docs/runtime-health.md`、`docs/superpowers/plans/2026-11-21-runtime-health-verification.md`
- Modify: `docs/README.md`、`docs/reliability.md`、`docs/request-history.md`、研究索引与路线、`CLAUDE.md`
- Evidence: `../archive/runtime-health-2026-11-21/`

- [x] 在实现基本完成后委托独立审查，仅审查本阶段相对当前主源码的差异；同时完成验证文档和浏览器 QA。重要发现按可复现测试修正。
- [x] `go test -tags sonic -buildvcs=false -count=1 -p 4 -timeout 10m ./internal/...`，Expected: 全部测试包通过；`golangci-lint run ./...` Expected: 0 issues；最终 Go build Expected: exit 0。
- [x] 完成前端类型检查、全量测试、构建与最终浏览器检查，读取真实输出并记录限制。没有真实数据库服务/race 时如实说明。
- [x] 根据 SHA256 清单只整合新增/修改文件及新版生成资源；覆盖前备份、校验当前主目录无并发改动、核对未触碰基线及保护文件。
- [x] 删除本阶段拥有的依赖链接、工作区、临时分支、验证程序及可再生脚本/大日志；保留紧凑验证证据、截图及备份。
- [x] 更新本计划勾选与验证记录；最终交付具体功能、真实检查结果和下一路线项。

## 执行记录

记录在本阶段证据目录的 `progress.md`；没有主分支提交，审查/整合使用当前文件哈希差异。已有的 P0/P1 证据和工作不重跑或重建。
