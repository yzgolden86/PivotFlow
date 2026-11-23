# 请求身份与尝试历史验证记录

实施日期：2026-11-20 至 2026-11-21，Asia/Shanghai。Git 基线为 `90aee95761d8fa21ab3f292b3b2930f8fa24be08`，另承接主工作区的 P0 和研究阶段未提交改动；922 个源码/文档文件的散列用作本阶段整合基线。Go 1.26.10，标签 `sonic`。测试使用临时 SQLite、合成 HTTP/WebSocket 上游和合成浏览器数据。

## 行为证据

| 行为 | 验证入口 | 结果 |
| --- | --- | --- |
| HTTP 请求身份与日志关联 | `internal/app/request_trace_test.go` | 网关生成 `pf_req_` + 32 位十六进制身份；响应、普通日志及活动请求关联一致，同名外部 Header 不覆盖 |
| 渠道/Key 故障切换与协议协商 | `request_trace_test.go` | 同一请求包含真实执行顺序、实际模型和尝试序号；401 切 Key、渠道失败切换、协议协商和 `stream_options` 兼容重试均可见 |
| 等待、本地拒绝、取消和部分输出 | `request_trace_test.go`、`stream_reliability_test.go` | 全局槽等待与上游耗时分开；本地跳过不伪装为已发上游；499 取消独立计数，已输出的半截流按 599 失败且不重放 |
| 有界时间线与迟到回调 | `request_trace_test.go` | 128 个事件后明确截断，完整计数继续累计；旧 WebSocket reader 的回调不能污染新尝试，已关闭 reader 不再重连 |
| Responses 每回合与恢复边界 | `request_trace_websocket_test.go` | 每回合独立 ID；prewarm、597、599、499、native reconnect 和握手 HTTP fallback 记录准确；成功后晚关闭不改成取消 |
| 关闭中的读取器与在途重连 | `request_trace_websocket_lifecycle_test.go` | 修改前通过网络 barrier 复现关闭后第二次握手/请求；修改后 Close 取消读取器并等其退出，已取消的 replay 不新增尝试，连续 5 次通过 |
| 失败的 2xx 语义结果 | `TestResponsesWebsocketRequestTraceFailed2xxIsNotSuccess` | 修改前 200/201/204 的失败结果被记录为 success；修改后全部为 502/error，连续 5 次通过 |
| 只读 API 与 Token 所有权 | `admin_request_traces_test.go` | 分页、筛选和 ID 校验；数据库查询强制绑定 Token，外部 ID 与不存在的 ID 均为 404，Token 投影隐藏路由细节 |
| 独立异步写入与降级 | `request_trace_service_test.go` | 256 容量、32 条批量、定时 flush、有限重试/关停 drain、快照隔离和写入/丢弃/失败计数 |
| 幂等存储、查询、迁移和保留期 | `internal/storage/request_history*_test.go`、`internal/storage/sql/request_trace_test.go` | 摘要与尝试原子写入；重复 ID 不重复统计；时间/Token/请求 ID 筛选及旧 SQLite 表增量迁移通过 |
| 混合同步与恢复 | `hybrid_request_history_test.go`、`hybrid_store_test.go` | local-only 记录在恢复后保留；远端记录合并幂等；复制预算独立于普通日志，两侧清理即使单侧失败仍分别执行 |
| 三方言字符串与 JSON 边界 | `request_trace_test.go`、`hybrid_request_history_test.go` | 标签限制为 191 rune 并去 NUL；非法字段拒绝；本地与异步远端使用同一规范化快照，长时间线在入队前截断 |
| 控制台数据表达 | `console/src/pages/requestHistory.test.ts` | 时间线按尝试序号、两种统计口径、Token 字段缺省、内部语义状态、诊断 JSON 和请求 ID 跳转 |
| 浏览器布局与操作 | `scripts/docs_screenshots.py --request-history-qa` | 桌面/320–390px、深浅主题、筛选、ID deep link、复制诊断、首次 404 重试、Escape、焦点约束和关闭后焦点返回 |

## 检查结果

- `go test -tags sonic -count=1 -p 4 -timeout 10m ./internal/...`：首轮通过后，独立审查追加两处 WS 边界修复；最终完整重跑退出码 0，34 个有测试包通过、5 个包无测试；app 222.380 秒，storage 117.440 秒，总耗时 230.054 秒。首轮结果也留在归档中。
- 最终 `golangci-lint run -v --concurrency 2 --timeout 10m ./...`：退出码 0，`0 issues`，176.153 秒，覆盖审查修复。初次检查将第三方研究快照当作应用包而失败；研究快照目录增加独立 `go.mod` 隔离构建，未改参考源码。
- 最终 `go build -buildvcs=false -tags sonic -o .tmp-request-history-final/pivotflow-verify.exe .`：退出码 0，5.359 秒，包含最终 WS 修复和前端资源。验证程序不启动或替换正式 exe，记录散列后清理。
- `npm run test`：最终独立重跑 177/177 通过，0 失败。最终独立 `npm run typecheck` 与 `npm run build` 分别在 40.821 秒、56.705 秒内通过；38 个前端资源与浏览器验收时的构建逐文件散列一致。
- 最终合成浏览器 QA 于 22:48:20 完成：49/49 通过，0 非预期浏览器事件，1 个预期首次 404，8 张 JPEG。搜索字段为请求 ID / 模型，所有 fixture ID 均符合真实格式，599 不冒称线路上的 HTTP 状态。

精简证据保存在工作区 `archive/request-history-2026-11-20/`，包含测试、lint、构建、浏览器结果和截图；整合清单与保护文件校验也放在该目录。用户功能说明见 [请求身份与尝试历史](../../request-history.md)，设计见 [设计契约](../specs/2026-11-20-request-history-design.md)。

## 整合与清理

- 95 个新增/修改文件已整合到 `PivotFlow/` 主目录，其中新增 56 个、修改 39 个；32 个过时前端 hash 资源已替换清理。127 项文件操作逐一验证散列或删除状态，原有 P0 与研究阶段未提交修改完整承接。
- 851 个未涉及的基线文件以及 9 个数据、密钥、配置与正式程序保护文件的散列保持一致，原 `.claude/skills/sync-cliproxy-core` 发现链接保留。
- 覆盖前保存 `pre-integration-backup.zip`，71 个原文件逐项散列验证通过，压缩后 659,175 字节。该包仅保存本阶段覆盖/替代的源码、文档与前端资源，不包含生产数据库或主密钥。
- 主目录定向回归覆盖请求历史、WS、只读 API、写入队列、混合存储及 P0 计费/恢复：app、storage、storage/sql、model 四包全部通过，总耗时 32.478 秒。app 13.147 秒、storage 12.437 秒。
- 临时工作树、`feat/request-history` 分支与验证程序已清理，移除 950 个临时文件、85,892,526 字节（约 81.9 MiB）。主目录 Node 依赖与已有 `internal/app/data/fusion-master.key` 保留。最终记录见归档中的 `cleanup-final.json` 和 `final-verification.json`。
- 工作保持未提交状态，未推送、发布或替换正式 exe。最终进度文档仅在整合与清理完成后更新，不改变已验证的产品代码。

## 审查与实现决策

- 启动恢复由覆盖本地窗口改为合并：请求历史先本地保存，远端同步不承担财务 journal 的持久化保证，不能以远端缺记录为理由删除本地证据。
- 每次尝试的 context 带 generation，迟到回调只能更新所属尝试；WebSocket 响应体拥有独立的可取消 context，Close 等待读取器和 turn 释放后返回。重连在记录尝试前及写入前后检查取消，写入取消回调绑定不变的连接，避免捕获循环中变化的 conn。兼容重试后的首字耗时从当前尝试开始计算。
- HTTP 和 WS 的最终状态表达网关语义结果；已输出 HTTP 200 的流仍可最终失败。成功 WS 回合结束后发生的客户端关闭不回写为取消。
- 最终重试方向由真实后续渠道/Key/URL 推断，最终事件不显示尚未发生的重试；截断后的最终结果以上方摘要为准。
- 只整合本阶段 delta，保留主工作区现有未提交修改。`.claude/skills/sync-cliproxy-core` 的原发现链接与工作树普通文件表示不同，明确排除整合；旧前端 hash 资源以新构建替换并留有压缩回退副本。
- 存储独立审查无阻塞项。代理独立审查发现的关闭/重连时序和失败 2xx 摘要均已用先失败再通过的回归验证，包含修复的全量测试通过。保留一个后续项：定时日志清理当前丢弃 `CleanupLogsBefore` 的返回错误，持续远端清理失败缺少操作员可见信号；两端清理与下轮重试仍会执行。下一阶段健康展示需要纳入此状态。

## 覆盖边界

- 请求历史是可降级观测，队列满、最终写入失败或进程在刷盘前退出时可能丢记录；两种成功率只统计已保存的摘要。
- 鉴权中间件拒绝、WS 帧归一化/回合锁之前的拒绝不在本阶段范围。全局等待不是所有外层等待的总和；同源 HTTP 重定向不逐跳记录。
- MySQL/PostgreSQL 已实现 schema 与 SQL 分支，但未连接真实服务验证；混合回归使用两个真实 SQLite 库注入故障。真实 Codex/Claude 等客户端与生产负载尚未联测。
- Windows 本机 `CGO_ENABLED=0` 且没有可用 C 编译器，未运行 race；普通并发测试不能替代 race。
- 未读取生产报文、使用真实上游凭证、启动正式服务或签到；未提交、推送或发布。健康 UI 和接入工作流仍按后续阶段推进。
