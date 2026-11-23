# Reliability Foundation Implementation Plan

> **For agentic workers:** Use executing-plans for the local critical path; bounded storage, redirect and stream verification tasks have disjoint ownership. Steps use checkboxes to record evidence.

**Goal:** 可靠保存成功调用费用，限制认证请求跳转，并验证流式恢复契约。

**Architecture:** 独立 SQLite journal 保存待处理成功事件；主库按事件 ID 事务去重，后台重试。现有失败统计队列继续保留。认证客户端共用同源判断，现有协议和路由生命周期保持权威。

**Tech Stack:** Go 1.26.10 / Sonic / database/sql / modernc SQLite / Gin。

**Spec:** [设计](../specs/2026-11-19-reliability-foundation-design.md)。

## Global Constraints

- 必须 `-tags sonic`；不新增外部依赖，不访问真实上游。
- 当前研究与清理文档保留；新增实现先在隔离工作树验证，再整合回工作区。
- 默认全局 Go 缓存，避免项目内再次累积临时缓存。
- 数据库增量建表，不删除旧表/列；软限额语义不变。

## Review Focus

- 主库已提交但客户端收到错误时，同一事件重试不会重复计费。
- 副本恢复已经包含主库统计时，旧 journal 重放不能再次累加。
- 关停或唤醒队列满时，已保存事件仍能下次恢复。
- journal 故障时仅阻止新增代理工作，管理排障入口可用；在途 WS 每个 turn 同样受约束。
- 跨端口、协议降级及带 userinfo 的跳转不会携带认证数据离开源站。

### Task 1: SQL 结算去重

- [x] 新增 `model.TokenSettlement` 与 `Store.ApplyTokenSettlement(ctx,event) error`。
- [x] 用真实 SQLite 覆盖重复、并发重放、删除 Token、事务失败、事件时间和零价。
- [x] 新增 receipts 表与原子写入，覆盖三个 SQL 方言的生成；真实 MySQL/PostgreSQL 验证另记限制。
- [x] 混合存储同步权威统计，验证重放与副本故障后重试。

### Task 2: 持久待结算记录与应用接入

- [x] 先复现当前成功队列饱和丢失。
- [x] 新建 `internal/accounting/journal.go` 和测试：append/list/ack、重开、错误保留。
- [x] 新建 `internal/app/billing_service.go`，成功事件先保存、费用只累计一次、失败重试、启动恢复、有限关停。
- [x] 通过 `EnableDurableBilling` 在 main 启动监听前启用，增加 journal 路径说明和聚合指标。
- [x] 故障注入覆盖主库提交结果不明、journal 故障、恢复、关停、倍率和排队。

### Task 3: 认证重定向

- [x] 公共同源比较和跳转限制，首先运行失败回归。
- [x] 上游与 Provider 接入并保留已有 DNS/地址规则。
- [x] 同源/跨源/跨端口/降级/跳数/301/302/307/308 测试通过。

### Task 4: 流式恢复验证

- [x] 现有取消、半截流、WS/HTTP 切换、工具输出、线程与 response ID 测试映射。
- [x] 补充有实际缺口的行为测试；发现缺陷先 RED 再修复。

### Task 5: 集成与交付

- [x] 全部内部包测试、lint、构建（主工作区最终回归通过）。
- [x] 尝试相关 race，准确记录环境限制。
- [x] 独立代码审阅并处理重要问题。
- [x] 更新实施记录与用户文档，保留精简验证证据，整合工作树代码。

## 进度与决定

2026-11-19：开始实施；上轮完整测试的 33 个内部包已通过，产品源码基线 `90aee95`。按用户直接开始的授权继续执行，不重复请求设计批准。

2026-11-20：持久结算、主库事务回执、混合副本同步、认证重定向和流式有界检测均已实现，应用定向回归通过。额外复现并修复 journal 故障未尝试健康主库保存、上游负用量和异常金额使队列永久阻断的问题。负计数归零；异常金额保存零值占位及 `cost_invalid` 回执标记、聚合告警，需人工核对。独立审阅、lint 和构建完成，38 个文件已核对哈希并整合回主工作区。详见 [验证记录](2026-11-20-reliability-verification.md)。

验证限制：本机 `CGO_ENABLED=0` 且没有可用 GCC，`go test -race` 被工具链拒绝；Docker 引擎也未启动。此次没有启动真实 MySQL/PostgreSQL。不能把普通并发回归或 SQL 方言单测标成 race／远程数据库验证通过。
