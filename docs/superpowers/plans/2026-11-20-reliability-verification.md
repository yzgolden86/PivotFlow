# 可靠性第一阶段验证记录

基线：`90aee95761d8fa21ab3f292b3b2930f8fa24be08`。本阶段按 2026-11-19 批准的顺序实施，验证使用临时 SQLite、合成上游和本地 HTTP/WebSocket 服务；未运行正式数据库，也未替换已部署程序。Go 1.26.10，构建标签 `sonic`。

## 行为证据

| 风险 | 验证入口 | 结果 |
| --- | --- | --- |
| 成功统计队列饱和丢账 | `token_stats_saturation_test.go` | 修改前复现 count/cost/input 为 0；修改后保存一次 |
| 主库已提交但确认丢失 | `billing_service_test.go:TestBillingRetriesAmbiguousCommitWithoutDoubleCharging` | 重试后次数、费用和用量保持一次 |
| 主库中断、进程重开 | `TestBillingPendingSurvivesReopenAndReloadsLimit` | 待结算文件重开恢复，费用限额重新加载 |
| journal 故障与恢复 | `TestBillingJournalFailureBlocksNewWorkUntilRecovered`、`TestBillingJournalFailureStillSettlesAvailablePrimary` | 暂停新工作、保留事件；健康主库可兜底保存；真实写探测恢复准入 |
| 已建立 WebSocket 在下一轮绕过故障门槛 | `TestBillingAdmissionCoversHTTPAndExistingWebsocketTurns` | HTTP 与既有 WS 新回合均返回 503 `billing_unavailable` |
| 关停期限与在途结算 | `TestBillingShutdownWaitsForAdmittedCompletion`、`TestBillingShutdownLeavesDurablePendingForNextStart` | 停止等待后在途完成仍可保存；重开可见未确认事件 |
| journal ACK 结果不明 | `TestBillingRecoversAmbiguousJournalAcknowledgement` | 写探测后恢复，不重复结算 |
| 免费与站点实扣价格 | `TestBillingSuccessfulProxyPersistsUsageAndFreeKeyPrice`、`TestBillingUpdatesLimitBeforeWorkerAndNeverAgainOnRetry` | 完整代理链路保留免费 Key；站点价不重复乘倍率；重试不再次扣内存额度 |
| 非法负用量拖垮新 journal | `TestBillingNegativeUpstreamUsageDoesNotPoisonJournal` | 修改前复现停止准入；修改后负计数归零、有效计数保留，价格继续使用原兜底 |
| NaN/Inf/负价/溢出金额永久阻断 | `TestBillingInvalidAmountsPersistUsageWithoutBlocking`、`TestBillingInvalidAmountDoesNotHoldOtherEventsDuringJournalFailure` | 修改前复现无法 drain；修改后 usage 保存、金额置零且 `cost_invalid` 持久标记；重试不重复指标；混合正常事件不被拖住 |
| 真实 HTTP 返回异常用量后继续调用 | `TestBillingProxyInvalidUsageStillAcceptsNextRequest` | 负计数和金额越界后下一请求仍正常完成与计费 |
| 主库重复 ID / 异常金额 | `internal/storage/sql/token_settlement_test.go` | 相同内容幂等、不同内容冲突；溢出拒绝；事务失败不留半笔统计 |
| 混合存储副本滞后与写入竞争 | `hybrid_token_settlement_test.go` | 主库成功副本失败后可重试；成功/失败/最近使用更新交错时计数一致 |
| journal 损坏 / 取消 / 权限 | `internal/accounting/journal_test.go` | 损坏记录保留并报错；取消传播；现存父目录权限不改动 |
| 认证跳转 | `http_redirect_test.go`、`upstream_redirect_test.go`、Provider `transport_test.go` | 同源可用；跨源、端口、降级、userinfo 拒绝；10 跳上限；301/302/307/308 语义验证 |
| 客户端取消 | `stream_reliability_test.go:TestReliabilityClientCancellationDoesNotRetryOrCool` | 499，不重试、不冷却 |
| Responses 语义输出后半截 SSE | `TestReliabilityResponsesTruncatedSSEIsFailureButTerminalSSEIsSuccess` | 修改前日志为 200；修改后记 599，已写出的流不重放；正常终态成功 |
| 原生 WS 切回 HTTP | `TestReliabilityNativeWebsocketToHTTPReplayUsesFullTranscript` | 重放 user/assistant/user 顺序，移除旧 socket 的 response ID |
| 超大 SSE 与后置 type | `TestResponsesSSETerminalTracker*`、`TestStreamCopySSE*` | 修改前复现缓冲超过 1 MiB 和正常终态误报；修改后缓存有界、完整透传，巨大 JSON 后部的 type 仍可识别 |

原有回归同时继续覆盖 `TestResponsesWebsocketThreadIsolationPreservesParentContinuation`、`TestResponsesWebsocketRejectsStalePreviousResponse`、`TestResponsesWebsocketReconcilesCompletedToolCallBeforeReplay`、`TestNativeCodexWebsocketReadFailureReconnectsWithReplay` 和 `TestResponsesWebsocketRetryableErrorReplaysTranscriptToNativeFallback` 等边界。

## 集成验证

- 首轮 `go test -tags sonic -count=1 -timeout 10m ./internal/...`：退出码 0，34 个有测试包通过、5 个包无测试；其中 app 387.443 秒、storage 194.490 秒。当时并行静态分析增加了运行时间。
- 后续负用量及计费定向回归：退出码 0，app 3.518 秒。
- SQL/hybrid 新增结算和并发回归在专项阶段连续运行 5 次通过。
- journal 与存储 `go vet -tags sonic`：通过。
- 最终计费、异常金额与流式定向回归：退出码 0，app 21.542 秒。
- `golangci-lint run -v --concurrency 2 --timeout 10m ./...`：退出码 0，`0 issues`。首次完整检查发现 journal `rows.Close` 未显式处理，已修复；最终检查针对与主工作区哈希一致的代码。
- 主工作区 `go build -buildvcs=false -tags sonic`：退出码 0。此验证构建跳过 Git VCS 元数据探测，避免隔离工作树的 safe.directory 限制；没有运行构建产物。
- 独立审阅：异常负用量与异常金额的全局毒化问题已闭合；最终未发现 critical/important 阻塞问题。金额无法确认时的零值占位、持久标记、告警与人工核对边界已写入用户文档。
- 主工作区最终 `go test -tags sonic -count=1 -p 4 -timeout 10m ./internal/...`：退出码 0，34 个有测试包全部通过、5 个包无测试；app 381.640 秒、storage 175.739 秒。包括最终 `cost_invalid` 回执与有界流式扫描代码。
- 38 个文件已整合到主工作区并逐文件比较 SHA-256，原有研究文档与未提交编辑保留。临时工作树已清理，正式数据库、主密钥、配置和既有 exe 等 8 个保护文件哈希保持一致。
- 精简证据归档在工作区 `archive/reliability-2026-11-20/`，包含首轮与最终测试、lint、整合清单、保护文件校验和清理记录。构建产物在记录哈希后删除，不作为已部署程序交付。

## 实际限制

- Windows 本机没有可用 C 编译器，`CGO_ENABLED=0`；执行 race 命令报 `-race requires cgo`。Docker 引擎未启动，未用容器替代。普通并发测试不等价于 race。
- MySQL/PostgreSQL 已生成对应 schema 和 SQL 分支，但没有连接真实远程数据库验证。混合测试使用两个真实 SQLite 库注入失败与交错。
- POSIX 权限断言在 Windows 跳过，文件权限应在 Linux CI 补验。
- 强制杀进程前尚未触发完成回调的用量，以及所有持久化手段同时失败时的易失事件，不在本次恢复保证内。
- 费用仍为软限额；回执需要长期保留；每次成功写 journal 带来一次持久化开销，未做生产负载基准。
- 本阶段未改前端 UI。健康展示、请求历史与接入引导继续按研究路线实施。
