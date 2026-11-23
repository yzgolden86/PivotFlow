# P1 费用来源与数据语义验证记录

2026-11-22，Asia/Shanghai。按已批准路线在独立工作区实现，基线为主目录当前全部未提交的源码、文档和资源，而不是仅从 Git HEAD 重建。设计见 [费用语义设计](../specs/2026-11-22-cost-semantics-design.md)，步骤见 [实现计划](2026-11-22-cost-semantics.md)，用户契约见 [费用说明](../../cost-semantics.md)。

## 实现与验证范围

新请求保存版本、状态、计费方式和安全数值明细；同一转发结果冻结金额、来源与本地倍率，日志和令牌结算复用这份快照。站点金额不再乘本地倍率，本地金额保留原计算规则。历史记录没有被今天的价格目录重价，旧金额、累计和结算回执没有被本阶段迁移改写。

日志、计数、概览、数据面板、趋势和费用汇总共享三项费用筛选；下游令牌身份始终约束范围。成功用量与安全费用分开处理，无效费用不进入图表；缺用量、未定价、部分和历史记录也不会被显示成已知免费或完整上游消费。汇总始终为 USD、本地记录且未对账。

完整定向 RED/GREEN、执行结果、散列清单、截图和最终操作记录保存在项目外置的 `archive/cost-semantics-2026-11-22/`。收尾时详细 `.log`、`.jsonl` 和 `.diff` 在逐项验 SHA256 后进入 `cost-semantics-evidence.zip`，结果 JSON、脚本、审查记录和 JPEG 保留直接可读。

## 关键回归证据

| 行为 | 失败证据与修复后验证 |
| --- | --- |
| 旧行、失败、零价与缺用量 | `cost-historical-red.log` 显示按当前价目重建旧状态/明细；纯历史解释与显式 v1 快照回归通过，见 `task1-task3-cost-green.log` |
| 同次日志与结算一致 | `task1-snapshot-reuse-red.log` 中本地有效日志金额 0.15 与结算 0.4 分裂，站点 0.27 因兼容调用丢失来源变为 1.89；冻结并复用来源、金额、倍率后通过，见 `task1-task3-final-focused.log` |
| 原有有效公式兼容 | 按 Token、按次、分层、缓存拆分、合计缓存写入回退、service tier、模型加工具、站点/本地倍率矩阵通过；价目数值未改动 |
| 持久化、升级与复制 | 新四列单条/批量/debug 写入、SQLite reopen 与日志快照复制通过；已有回填 marker、令牌累计与两条结算回执逐字段不变，见 `task2-final-focused.log`、`task2-accounting-upgrade-final.log` |
| 无效费用与聚合溢出 | `task3-invalid-aggregate-red.log` 中汇总为 0，图表却仍有标准 0.25 / 有效 0.5；`task3-invalid-storage-red.log` 出现 Inf，另有溢出被伪装成 0。统一安全金额查询并明确报错后通过，同时保留成功用量，见 `pre-full-focused.log` |
| API、权限与筛选 | 缺失汇总、读取错误、费用条件与下游令牌范围 RED 后通过；非法条件无匹配，`cost_basis=unknown` 有合法语义，旧身份路径也不丢筛选 |
| 缓存范围与所有权 | `task3-cache-ownership-capacity-red.log` 显示返回值修改污染缓存且容量 1008 超出 1000；深拷贝及固定容量通过。毫秒窗口误共用键的 RED 也修复，见 `task3-cache-ownership-capacity-green.log`、`task1-task3-final-focused.log` |
| 近期固定窗口缓存 | 独立审查补出近一小时内的 30 秒分桶分支。临时 SQLite 的两个 2ms 差异区间应分别为 1 条/$0.25、2 条/$0.50，RED 却都为前者；精确起止毫秒及相同区间 TTL 复用回归通过，`review-recent-window-red.log`、`review-backend-regression-green-result.json` |
| 费用子集与大金额显示 | 其他未知扣除部分和历史两个互斥子集；非法子集汇总拒绝。后端允许的 $10,000,000,000 行与汇总显示一致，不再要求 JS 微美元整数精度。三项 UI RED 后 15/15 定向回归通过，`review-ui-regression-result.json` |
| 缺少响应结果的费用依据 | 内部 2xx 无结果路径不再写入未取得的价格证据；一次移除分支后仍遗留初始化值的失败也保留，随后窄范围清空元数据并通过，`review-nil-result-red.log`、`review-backend-first-fix.log`、`review-backend-regression-green-result.json` |
| 日志刷新并发 | 校正验收脚本的旧文案预期、模拟筛选与退出清理后，`browser-manual-refresh-red-results.json` 为 73 过、1 败、未预期事件 0：旧手动刷新提前结束新 Local 筛选加载，显示 20 条旧行。前台加载归属修正及自动刷新等待/恢复回归通过 |
| 数据面板切换范围 | `browser-stats-scope-red-results.json` 为 82 过、1 败、未预期事件 0：新筛选读取未完成时仍显示旧费用汇总、KPI 和趋势。开始读取时清空旧快照，并在写入响应前检查取消信号；最终 83/83 项通过 |

## 工作区完整检查

| 检查 | 实际结果 |
| --- | --- |
| Go 内部完整套件 | 独立审查修复后重跑 `go test -tags sonic -buildvcs=false -count=1 -p 4 -timeout 10m -json ./internal/...` 退出 0；35 个有测试的包通过、5 个无测试文件的包跳过；4792 条通过事件、6 条跳过事件，包含父测试和子测试；`backend-summary-worktree.json` |
| 前端类型与测试 | app/test TypeScript 项目通过；独立审查修复后 258/258 项测试通过，无失败、取消或跳过；`frontend-worktree-result.json` |
| Go 静态检查 | `golangci-lint run ./...` 退出 0；`lint-worktree-result.json` |
| 控制台与 Go embed 构建 | Vite 完成后才运行 Go root 构建，均退出 0；产物只作编译验证，没有执行，也没有替换主目录原程序；`frontend-worktree-result.json`、`go-build-worktree-verification.json` |
| 浏览器 | 最终 83/83 项检查通过、未预期事件 0，15 张合成数据 JPEG 视口截图；包括自动刷新、窄屏可读性、数据面板换范围及新读取后的错误回退回归，`browser-results.json` |
| 文档链接 | 最终文档与研究进度补齐后核验；结果写入 `doc-links-worktree.json` 和 `doc-links-main.json` |

## 独立审查与浏览器可读性

两席独立审查在不共享 builder 历史的上下文中进行，分别核读模型/计算/存储与 API/缓存/控制台，按本阶段逐文件清单比较主目录基线与工作区；没有使用包含旧阶段的 HEAD 差异冒充本阶段审查。近期缓存窗口、未知子集、金额显示及无响应结果依据问题经 RED/GREEN 和针对性复核后处置完毕，两席最终均允许本地整合，无遗留 Critical/Important 问题。原始意见、第一次未完整修复的反馈及最终结论分别保留在外置 final-api-ui-code-review*.md、final-backend-code-review*.md，汇总见 final-code-review.md。

PostgreSQL NaN 疑点经复核撤回，现有数值上界比较已覆盖；没有为此修改 SQL 或历史数据。mixed 与 partial_estimate 可并存的组成/完整性解释按已批准规则保留并补清说明。审查员未执行完整套件、真实数据库及账单核对；这些边界与主执行方实际运行的本机验证分别记录。

浏览器截图均为合成数据、可见视口 JPEG；320/390 浅色及 1380 深色覆盖日志、数据面板、概览和令牌。截图核对发现日志窄屏搜索框被桌面 flex basis 拉高且费用解释截断；新增实际量测在两种宽度都得到 `search_height=260`、20 条说明截断，`browser-mobile-layout-red-results.json` 为 76 过、4 败、未预期事件 0。局部修正 flex basis 和换行后 80 项检查全部通过；随后补齐数据面板范围与新读取回退检查，最终为 83/83。检查包含滚动后的日志记录视口，代表性桌面、移动及记录截图已实际打开核对。

数据面板清空旧快照后，要求每个回退场景等待新的 Stats/Dashboard 读取及加载结束，避免旧成功结果误通过。该检查暴露测试夹具把 $0.93 的有效汇总覆盖到 null/error 场景，原失败结果保存在 `browser-stats-summary-fixture-failure.json`；覆盖只应用于 valid 场景后，所有原有产品断言保留，最终检查通过。

独立审查还校正展开汇总的未知子集断言：合成夹具 6 条未知包含 1 条部分、2 条历史及 3 条其他未知。原脚本与 SHA256 保存为 `qa-cost-semantics-before-review-fix.py`、`browser-review-expectation-correction.json`，最终要求“其他未知 3 · 历史记录 2”。

## 本地整合与清理

本地整合复制 105 项源码、文档和资源，移除 41 项被新构建替代的 JS/CSS；945 项未触碰基线文件与九个保护文件逐项散列匹配。被覆盖或移除的原文件先写入 ZIP 并逐项验 SHA256；原有未提交工作、发现链接与 HEAD 保持原样。记录见外置 integration-execution.json 和 backup-verification.json。

主目录定向回归退出 0（66.77 秒），83 项合成浏览器检查全部通过、未预期事件 0；工作区最终浏览器 83 项也通过。主目录 10 份文档的 78 个本地链接通过，Go 构建退出 0（20.47 秒），验证程序未执行。记录见外置 smoke-main-result.json、browser-results-main.json、doc-links-main.json 与 go-build-main-verification.json。

本阶段工作区、临时分支、依赖 junction、验证程序及自有 Python 缓存已清理，共移除 84.14 MiB 临时文件；83 份详细日志和差异逐文件验散列后压缩归档，净释放 17.64 MiB，合计约 101.78 MiB。共享前端依赖、生产数据库、密钥和原有程序均保留。实际字节数见外置 cleanup-verification.json 和 compressed-evidence-verification.json；最后完整性见 final-integrity.json。

计划最后一项在这些操作完成后勾选，本页收尾数字同时补写。仅计划和本页在主目录检查后更新，源码与控制台资源没有变化。更新前文档与清单已备份，最终清单更新散列后再次核验完整性与文档链接，记录见 document-finalization.json。

## 验证边界

运行验证使用临时 SQLite、模拟上游及完全拦截 API 的静态浏览器页面。MySQL/PostgreSQL schema 与表达式做了代码/单元核读，未连接真实数据库；本机未验证 race detector。真实逐请求账单、实际客户端长会话与生产升级/部署不属于这份通过声明。

没有提交、推送、发布或部署，也没有运行生产程序、真实签到或修改九个保护文件。主目录原有未提交工作、共享前端依赖与发现链接需要在整合和清理后再次验散列；最终清单和完整性结果记录上述实际操作。
