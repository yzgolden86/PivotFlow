# 2026-11-19 有界事实审阅

审阅仅覆盖 `pivotflow-current-state.md` 与 `recommendations.md` 中可沿链接核验的计费、请求关联、运行指标、费用口径、设置生效和建议边界。未联网，未运行测试，未读取真实 `data/`、密钥、完整 `HANDOFF.md` 或其他研究报告。版本、HEAD、前端验证和本机 exe 事实沿用主代理已核实结果。

## 实质更正

### 1. R2 把“进程内请求 ID 不贯穿尝试”和“缺少持久尝试时间线”混在一起

- 报告位置：`docs/research/2026-11-19/pivotflow-current-state.md:131`；`docs/research/2026-11-19/recommendations.md:61`。
- 源码证据：`internal/app/proxy_forward.go:2709-2727` 在每个 URL 尝试开始时调用 `BeginAttempt(reqCtx.activeReqID, ...)`；`internal/app/active_requests.go:95-105` 只在 ID 为 0 时分配新 ID，后续尝试复用同一 ID 并标为 `retrying`。所以运行期 ID 实际可跨同一请求的多次尝试。
- 但该 ID 只活到请求结束：`internal/app/active_requests.go:108-123` 用每次尝试覆盖当前字段，`internal/app/active_requests.go:135-140` 的重试也重置当前快照，`internal/app/active_requests.go:184-188` 在完成时删除记录；`internal/app/active_requests.go:233-259` 的公开列表只有当前值。`internal/model/log.go:65-87` 的 `LogEntry` 又没有运行期请求 ID 或尝试序号。
- 应改文字：将 R2 的结论改为“日志有行 ID；活动请求有可在进程内跨多次尝试复用的运行期 ID，但重试会覆盖当前尝试快照、请求完成即删除，日志也未保存该 ID，且没有尝试序号、尝试历史和最终结果的持久关联。缺的是可持久查询的请求身份与时间线。”
- 应改建议：将 `recommendations.md:61` 首句改为“当前活动请求虽有进程内 ID 并在多个尝试间复用，但它只保留当前快照、完成即删除，日志也未保存；行 ID 和该运行期 ID 仍不能形成可查询的完整请求身份。”后续“稳定 ID、尝试序号”建议保留，并明确先复用现有 `BeginAttempt` 生命周期，避免重复造一套并行状态。

## 其余核验结论

- R1 成立：`internal/app/proxy_error.go:511-524` 在成功统计入队等待 100ms 后仍可能因二次非阻塞发送失败而丢弃；`internal/app/proxy_error.go:429-434` 则先更新内存费用缓存，再写数据库。
- R3 成立：`internal/app/admin_active_requests.go:19-32` 的 `/admin/runtime-metrics` 只返回 `responses_websocket`；日志丢弃（`internal/app/log_service.go:207-220`）、计费统计丢弃（`internal/app/proxy_error.go:519-531`）和混合存储同步失败/丢弃（`internal/storage/hybrid_store.go:43-46`、`89-95`、`189-199`）仅有内部计数。
- R4、R6 成立：`internal/app/proxy_handler.go:531-537` 明示费用在完成后记账、准入为检查后执行；`internal/app/site_pricing.go:435-450` 与 `453-481` 使用站点声明价目及返回用量计算，不能据此宣称已经向上游账单对账。
- 设置与倍率口径成立：`internal/app/admin_settings.go:74,94` 只列 `model_alias_groups` 与 `auto_refresh_interval_seconds` 为 `live:`，`191-209` 执行热刷新；`internal/app/auto_update.go:31-52` 将 `applyUpdates` 固定为 `false`；`internal/app/dashboard_scope.go:177-197` 对站点价目不再乘倍率，倍率为 0 时结果为零，负倍率回退为 1。
- 现有能力与边界没有误判：`recommendations.md:21,23,25,28` 明确把多 Key、粘性、WebSocket 和备份列为已有能力；`recommendations.md:38,84` 保持“已输出不重放”和浏览器/服务器隔离，未见破坏这些边界的建议。

局限：这是源码事实审阅，不替代后端测试、真实客户端回归或上游账单验证；未检查未在报告链接中出现的实现路径。
