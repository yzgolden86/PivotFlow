# P1 后台运行健康验证记录

2026-11-21，Asia/Shanghai。本阶段实现管理员首页健康摘要与 `#/health` 详情，覆盖可靠结算、请求历史、上游日志、失败统计、主库复制、SQLite 副本、清理、启动恢复与 WebSocket 资源。

使用说明见 [后台运行健康](../../runtime-health.md)，设计见 [本阶段设计](../specs/2026-11-21-runtime-health-design.md)，任务清单见 [实施计划](2026-11-21-runtime-health.md)。

## 范围与基线

- 实施工作区：`PivotFlow/.worktrees/runtime-health`，临时分支 `codex/runtime-health`。
- 基线是主目录当前 946 个文件的 SHA256，包含此前尚未提交的 P0/P1 与研究工作；不以 Git HEAD 覆盖这些改动。
- 主目录 HEAD 为 `90aee95761d8fa21ab3f292b3b2930f8fa24be08`。
- 主目录密钥、数据库及 WAL/SHM、现有 `pivotflow.exe` 等九个文件作为保护基线；发现符号链接与主目录依赖目录保留。
- 没有新数据库表、系统配置或前端依赖，也没有新的管理写接口。

## 实际检查

| 检查 | 结果与证据 |
| --- | --- |
| 真实操作监控 | 失败、恢复、纯取消、混合取消错误、幂等结束及 100 个并发完成测试通过；`operation-green.log`、`review-green.log` |
| 服务与结算回归 | journal/结算监控、最老积压、批次待写、最终未保存、普通及调试清理、失败统计回归通过；`services-green.log`、`review-green.log` |
| 存储与恢复回归 | 真实临时 SQLite 的失败/恢复、复制、丢弃、关闭 drain、副本差异与可选启动历史恢复通过；`storage-green-final.log` |
| 管理接口 | 管理员鉴权、Token 拒绝、原字段保留、不查询数据库、未知存储能力及错误允许列表通过；`api-green.log`、`review-green.log` |
| 后端全量 | `go test -tags sonic -buildvcs=false -count=1 -p 4 -timeout 10m ./internal/...` 退出 0，35 个有测试的内部包通过，耗时 196.27 秒；`backend-full-final.log`、`backend-full-final-verification.json` |
| 静态检查 | `golangci-lint run ./...`，0 issues，退出 0，耗时 85.02 秒；`lint-final.log` 与对应 verification JSON |
| 前端类型与全量测试 | 应用及测试类型检查退出 0，186/186 测试通过；`console-typecheck-final.log`、`console-tests-final.log` |
| 控制台构建 | `npm run build` 退出 0，Vite 最终构建 2.96 秒；`console-build-final.log` |
| 合成浏览器 | 66 项检查全部通过，未预期事件 0；`browser-qa-final.log`、`qa-result.json` |
| 最终 Go 构建 | `go build -tags sonic -buildvcs=false -o <验证程序> .` 退出 0，耗时 11.06 秒；`go-build-final-verification.json` 记录构建产物 SHA256 和大小 |

证据位于工作区外的 `E:/Dev/tools/api/archive/runtime-health-2026-11-21/`。表中的 JSON 指运行记录，完整文件名以目录中的 `*-verification.json` 为准。验证程序仅用于确认构建，未执行，也未替换现有运行程序。

## 浏览器覆盖

正常、暂停准入、仅内存结算、最老积压、通道与工作线程批次、最终记录损失、80% 队列压力、复制失败与后续成功、粘性副本差异、可选历史恢复失败和关闭、清理失败与恢复、无流量、纯 SQL 存储和 WebSocket 预算均有断言。

旧后端、404、健康版本不匹配、无效计数、不可表示时间、未知状态与缺少存储指标能力保持不可用或未知。诊断复制检查显式允许列表、累计损失、过期标记和剪贴板兼容回退；测试剪贴板为隔离的浏览器模拟值，不写用户的系统剪贴板。

刷新生命周期覆盖完成后 10 秒轮询、不重叠、8 秒超时、隐藏暂停、恢复可见、暂停取消、手动刷新、迟到响应不能覆盖新快照及卸载清理。隐藏 60 秒后返回且响应被挂起时，页面在响应前立即变为未知。

首页在业务加载和失败时仍可读取健康摘要，切换统计时间范围不会重建或重复轮询。1440px、390px 和 320px、深浅主题、键盘详情展开与可见焦点、跳过导航、全局搜索、手机抽屉与减少动效均有检查；横向溢出为 0。

截图为合成数据，保留桌面、窄屏、深色、暂停准入、历史损失、过期快照和业务概览失败等页面。已实际查看桌面全页、390px 首屏和过期快照截图。

## 独立审查处理

Volta 独立只读审查后端和前端增量，发现并修正三项可复现问题：

1. 组合错误中的取消掩盖真实存储故障，且关停时可能不输出清理失败日志。现在只有所有非空叶节点均取消才计取消。
2. 缺少可选存储指标时，复制和缓存同步误报为未启用。现在未知能力保持未知。
3. 长时间隐藏后返回，旧快照在新响应前可能短暂显示当前正常。现在恢复可见和开始读取时立即更新时间。

红测试分别见 `review-red.log`、`visibility-review-red.log/json`，修正后的定向及全量结果见上述记录。额外补齐不可表示时间的防御性处理，修正小字体至项目要求的至少 11px，并核对完整请求与上游日志的独立入口。

## 整合与清理规则

`collect-phase-files.ps1` 按当前文件 SHA256 生成增量清单，仅允许删除过期的 `web/console/assets/*.js|css`。`integrate-files.ps1` 在整合前后校验所有增量和未触碰基线、九个保护文件与发现链接；覆盖前创建 ZIP 并逐条验证原始文件 SHA256。新版资源先复制，入口就位后才移除旧资源。

实际整合与清理记录分别保存在 `integration-execution.json`、`backup-verification.json`、`cleanup-verification.json` 及最终完整性检查中。清理仅处理本阶段临时工作区、分支、依赖 junction、验证程序和自有可再生文件。源码与控制台资源的本地整合不包含提交、推送、发布或生产部署。

## 限制与下一项

没有可用 C 编译器/CGO race 环境，未执行 race detector；未连接真实 MySQL/PostgreSQL、上游账号或生产数据库。SQLite 与管理员路由使用真实测试实现，浏览器 API 使用完整合成数据；这些检查不能代替生产网络与客户端联测。

当前运行指标只展示真实操作观察，不能证明过去的副本差异或历史损失已补齐。下一项按原路线推进控制面任务合并与按站点限速，再处理接入引导和可验证的客户端配置。
