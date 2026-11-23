# 控制面任务合并与站点请求预算 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox syntax for tracking.

**Goal:** 合并重复维护操作，限制共享上游请求，并提供可见、可取消、可仅重试失败的维护流程。
**Architecture:** Provider 的共享 origin 预算与现有站点门分工；持久任务继续使用现有表/租约，增加进程内合并登记。前端共享执行器复用各页进度。
**Tech Stack:** Go / Gin / existing SQLStore; React / TypeScript / node:test / Python Playwright。无新增依赖。
**Spec:** `docs/superpowers/specs/2026-11-21-site-task-budget-design.md`

## Global Constraints

- 4 个后台执行/真实 HTTP 槽、128 个合并登记、每 origin 单 HTTP 并发和 250ms 间隔；429 默认 30 秒冷却。
- 现有 10 分钟排队与 2 分钟运行预算分开；价格共享读取最多 8 秒。
- 保留正文/样式、凭证、数据面、现有租约/缓存；不重写协议核心、不改生产数据。
- `-tags sonic -buildvcs=false`；无提交/推送/发布，按当前文件哈希整合并清理。

## Review Focus

1. HTTP body 读完、关闭、失败和取消都释放资源；等待不阻塞其他 origin。
2. 合并登记的创建/结束/取消/关停无竞争，取消后不偷偷启动第二次上游请求。
3. 价格共享调用不由首个等待者取消，不在持锁时等待，遵守同站点操作边界。
4. 定时租约续期包含排队时间，无租约不创建历史；签到副作用不因重试 UI 重放成功项。
5. 前端提交响应与取消交错、观察中断、页面卸载和整批取消不丢服务器 task ID，不把未知当失败。

### Task 1: 共享 Provider 请求预算

**Files:** create `internal/site/provider/request_budget.go`, `request_budget_test.go`; modify `transport.go`, `site_control.go`.
**Interfaces:** `NewRequestBudget() *RequestBudget`; `(*RequestBudget).Snapshot(baseURL string) BudgetSnapshot`; transport 使用相同实例。
- [x] RED: 虚拟时间测试同 origin 串行/间隔、不同 origin 继续、body Close/EOF 释放、等待取消、429 delta/date/default 冷却和无自动重放；运行 `go test -tags sonic -buildvcs=false ./internal/site/provider -run RequestBudget`，预期缺失实现失败。
- [x] GREEN: 最小预算与响应 body 包装，构造站点工厂时共享；运行 Provider 完整测试，预期 PASS。

### Task 2: 任务合并、可取消调度与价格共享

**Files:** create `internal/app/site_task_queue.go`, `site_task_queue_test.go`; modify `admin_sites.go`, `site_scheduler.go`, `site_control.go`, `site_pricing.go`.
**Interfaces:** 合并提交返回固定任务 ID/完成信号/merged；队列准入持有日/账号租约，注册 runAsync，结束释放合并登记。价格 `fetchSitePricing` 通过共享 singleflight 调用原读取实现。
- [x] RED: 路由级并发重复提交仅一行一执行，终态重建；取消排队/运行/定时工作；共享全局 4、停止和 128 上限；价格并发读取合并/等待者独立取消；运行定向测试并确认缺失行为。
- [x] GREEN: 扩展既有入口，保留不写租约冲突历史测试；任务查询提供 safe budget/progress；运行所有站点及价格测试，预期 PASS。

### Task 3: 逐项任务进度、取消与失败重试

**Files:** create `console/src/siteTaskRunner.ts`, `.test.ts`, `pages/SiteTaskProgress.tsx`; modify `api.ts`, `types.ts`, AccountsPageV2/CheckinsPage/AnnouncementsPage/ChannelsPage，必要的局部样式。
**Interfaces:** 控制器接收 start/read/cancel API，逐项结果含 task ID、状态、错误和观察状态；执行最多 4 worker；cancel 与 start 交错仍取消已提交 task；retryFailed 只取已确认 failed/partial。
- [x] RED: node:test 覆盖仅失败重试、取消未提交/提交中/已提交、服务器取消失败、观察超时/中断、重复开始与 dispose；预期缺失实现失败。
- [x] GREEN: 共享执行器/进度区接入各页，保留既有编辑/删除等行为；类型检查、所有前端测试及构建 PASS。
- [x] 合成浏览器验证三个入口、取消/重试目标、320–390px、深浅主题和键盘，保存 JPEG 并目视检查。

### Task 4: 审查、完整验证与整合清理

- [x] 独立审查本阶段差异；重要问题先写失败测试，再修复并回归。
- [x] `go test -tags sonic -buildvcs=false -count=1 -p 4 -timeout 10m ./internal/...`，lint、前端 typecheck/test/build、最终 Go build；每项读取实际结果。
- [x] 文档记录真实行为/限制、研究进度与验证证据；哈希比对/备份/整合本阶段差异。
- [x] 主目录 smoke check、九个保护哈希与无关文件验证；清理本阶段独有资源。保留外置证据，无 Git commit/push。

审查后补充：已确认的成功/部分成功不被后到取消覆盖；取消接口等待真实执行结果（最多3秒），无所有权的历史活动行显示 unknown。排队中取消的每日签到也持久记取消；价格 flight 自行提交缓存并以 generation 防止失效覆盖；公告批次区分未知、确定失败和取消；任务 DTO 仅输出允许列表错误码。完整证据见 [验证记录](2026-11-21-site-task-budget-verification.md)。清单最终完成状态在整合与清理之后更新，外置 progress ledger 保留过程。
