# P1 控制面任务合并与共享站点预算验证

2026-11-21，Asia/Shanghai。功能契约见 [站点维护任务](../../site-tasks.md)，设计见 [本阶段设计](../specs/2026-11-21-site-task-budget-design.md)，工作分解见 [实施计划](2026-11-21-site-task-budget.md)。

## 范围与基线

- 实施隔离目录为 `PivotFlow/.worktrees/site-task-budget`，临时分支为 `codex/site-task-budget`。
- 基线为主目录当前 968 个源码/文档/前端资源文件 SHA256，包含前序已授权但尚未提交的改动，不使用 HEAD 覆盖这些改动。
- HEAD 保持 `90aee95761d8fa21ab3f292b3b2930f8fa24be08`；密钥、数据库及 WAL/SHM、原有运行程序等九个文件单独保护，发现链接与共享依赖保持原样。
- 延用现有任务表、租约、站点门、Provider 工厂和管理接口，无新增数据库迁移、第三方依赖或生产配置。

## 实际检查

| 检查 | 结果与外置证据 |
| --- | --- |
| Provider HTTP 预算 | 同 origin 串行、250ms 间隔、全局 4 槽、等待可取消、body EOF/Close 释放、传输真实退出后释放、429/503 冷却、Retry-After 秒数/日期/非法值；`budget-green.log`、`cancel-transport-green.log` 与最终全量结果 |
| 任务登记与队列 | 合并活动任务、终态重新创建、128 上限、4 执行槽、取消排队/运行/定时任务、租约互斥、活动列表只含本进程登记任务、全站公告父子身份；`site-tests-final.log`、`queue-final-local.log` 与最终全量结果 |
| 价格读取与服务停止 | 并发读取共享、等待者独立取消、站点门、8 秒预算、runAsync 注册、stopTasks 取消与等待；`pricing-shutdown-red.log` → `pricing-shutdown-green.log` |
| 错误与签到回归 | 三种 Provider 保留共享限流错误、取消结束签到 attempt/run、限流不重复 POST；`provider-cooldown-code-red.log` → `provider-cooldown-code-green.log`、`checkin-cancel-red.log` → `site-tests-final.log` |
| 后端完整测试 | `go test -tags sonic -buildvcs=false -count=1 -p 4 -timeout 10m ./internal/...` 退出 0，35 个测试包通过、另 5 个包无测试，164.09 秒；`backend-full-final.log`、`backend-full-final-result.json` |
| 静态检查 | `golangci-lint run ./...`，0 issues，退出 0；`lint-final.log`、`lint-final-result.json` |
| 前端类型与测试 | `npm --prefix console run typecheck`、`npm --prefix console test` 均退出 0，196/196 测试通过；`frontend-typecheck-final.log`、`frontend-tests-final.log`、`frontend-final-result.json` |
| 前端生产构建 | `npm --prefix console run build` 退出 0，最终 Vite 构建 6.13 秒；`frontend-build-final.log` |
| 合成浏览器 | 36 项检查通过，未预期 API/外部请求/页面异常为 0；`browser-results.json`、`browser-qa-final.log` |
| 最终 Go 构建 | `go build -tags sonic -buildvcs=false -o <验证程序> .` 退出 0，2.25 秒；`go-build-final-verification.json` 保存产物哈希及大小，程序未执行、未替换生产程序 |

外置证据位于 `E:/Dev/tools/api/archive/site-task-budget-2026-11-21/`。首次红测试、失败日志及修复后结果均保留；早期全量通过不替代最终全量记录。

## 前端行为与浏览器范围

Node 测试覆盖重复提交合并、仅重试 failed/partial、排队取消、提交响应前取消、迟到响应保留 task ID、取消失败与拒绝、终态不会被迟到 GET 覆盖、未知结果重新观察、明确未准入错误和核实后移除。unknown 不进入失败重试，没有 ID 的 unknown 不会被“取消全部”伪造为已取消。

真实 Chromium 使用完整合成 API，未启动网关。覆盖账号批量余额刷新、失败目标精确重试、提交中取消、签到整批取消、公告按站点刷新与筛选、导航保留任务、定时任务恢复、公告迟到请求不能覆盖新筛选、已结束结果键盘入口、渠道同步取消及关闭窗口/失败重试后的数据刷新。390px 浅色、320px 深色、1360px 深色均检查页面与任务区无横向溢出、取消按钮键盘可达。已实际查看账号浅色、320px 深色及渠道同步截图。

## 审查与补充回归

独立前端审查及处理见外置 `frontend-review.md`，渠道页增量复审没有新的重要问题。审查发现的无 ID 未知项误取消、公告读取竞态和键盘入口均先复现再修复。明确排队拒绝与人工核实恢复也有单独回归。额外修复 JSX 数字零泄漏和取消后传输尚未退出就释放 HTTP 预算。

后端独立审查见外置 `backend-review.md`。最后复核另发现排队取消签到的 run 插入后没有结束时间，现补 UpdateCheckinRun；`queued-run-finish-red.log` → `queued-run-finish-green.log`，最终完整套件已包含此修复。补充回归修复：成功签到被后到取消覆盖、排队取消签到缺 daily 记录、无所有权的活动行误显示/误取消、错误 DTO 原文泄漏、目标删除击穿活动列表，`outcome-review-red.log` / `orphan-cancel-red.log` → `review-targeted-final.log`。服务停止和租约丢失保留各自原因，取消定时刷新保留真实刷新时间并延至下个间隔。

唯一价格等待方取消不再写15分钟负缓存；共享抓取自行提交缓存，generation 校验阻止价格源失效后的旧结果回写/返回。`pricing-review-red.log` → `pricing-review-green.log`，新增测试重复5次及相关回归通过。公告批次读取失败/未终结状态保留 unknown，全部子项取消则父项取消，终态读取限定3秒；`batch-review-red.log` → `batch-review-green.log`，新增4用例重复5次通过。前端收到服务器 unknown 保留身份与原因且禁止失败重试，`server-unknown-red.log` → 最终196项测试及浏览器未知状态检查。

主代理补查渠道同步的完成刷新：关闭窗口后渠道列表未更新、进度区重试后绑定快照未更新。两个浏览器用例均先失败，见 `channel-completion-red.log`、`channel-retry-completion-red.log`，现已通过最终浏览器套件。完成刷新由任务状态驱动并可取消；筛选/卸载继续受请求身份保护。

逐行状态朗读暂保留为后续读屏体验改进：当前有汇总 aria-live、可聚焦的逐项结果区，未声称使用真实读屏软件验收。

## 本地整合与清理

`collect-phase-files.ps1` 基于当前基线哈希生成本阶段清单，排除发现链接的检出表示差异；只允许删除过期 `web/console/assets/*.js|css`。`integrate-files.ps1` 整合前校验主目录，ZIP 备份被覆盖原文件并逐项 SHA256 验证；整合后再次核对增量、未触碰基线、九个保护文件与发现链接。先复制新资源，入口就位后删除旧资源。

主目录整合后的 Provider/任务/价格回归退出0，36项合成浏览器检查全部通过、未预期事件0。实际操作先复制77项源码/文档/资源、移除37项过期资源，再以已校验备份同步两项后端修正和验证文档；未触碰基线908项、保护文件9项，链接与HEAD保持原样。

最终操作证据见 `final-files.json`、`backup-verification.json`、`integration-execution.json`、`cleanup-verification.json` 和主目录 smoke/final-integrity 记录。仅清理本阶段工作区、临时分支、依赖 junction、验证程序及自有 Python 缓存；共享依赖、原程序、数据库和密钥保留。源码本地整合不包含提交、推送、发布或生产部署。

## 限制与下一项

HTTP 预算及任务登记为单进程内存状态；不是多实例统一限流服务，进程重启不自动重放历史任务。取消不能撤回已经发生的上游副作用，丢失响应仍需观察或人工核实。价格读取超时/限流可回退原本地估算。

本机无可用 C 编译器/CGO race 环境，未运行 race detector；未连接真实 MySQL/PostgreSQL、生产数据库、上游账号或执行真实签到。Go 测试使用测试存储/临时 SQLite，浏览器使用合成 API，不能替代真实客户端联测。

下一项按已批准路线推进新手接入引导和可验证的客户端配置，继续复用现有来源、渠道、下游令牌和请求历史。
