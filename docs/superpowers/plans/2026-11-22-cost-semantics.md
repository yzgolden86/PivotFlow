# P1 费用来源与数据语义 Implementation Plan

> **For agentic workers:** Main uses executing-plans; disjoint SQL and frontend implementation uses dispatching-parallel-agents. User has authorized continuation and local integration. No commits, pushes or deployment.

**Goal:** 保存可信费用解释，并在日志、概览和统计使用同一口径。
**Architecture:** 版本化请求费用快照、纯模型状态解释、同一日志筛选 SQL 汇总、共享前端解释组件。
**Tech Stack:** Go / Gin / database/sql；React / TypeScript，沿现有依赖。
**Spec:** [设计](../specs/2026-11-22-cost-semantics-design.md)

## Global Constraints

- 源代码仅在 `.worktrees/cost-semantics` 修改，以当前主目录完整散列快照为基线。
- 九个保护文件仅验散列；保留所有历史未提交改动、发现链接与共享依赖；占位符原样保留。
- 不改既有有效费用数值算法、财务回执、路由策略或转换核心；不连真实上游/生产数据库。
- `site_pricing` 仍不乘渠道倍率；新增状态是价格表估算，reconciliation 只为 not_reconciled。
- Vite 稳定后再 Go embed build；验证程序不执行；截图合成/JPEG/非 fullPage。

## Review Focus

- 正标准金额与零倍率、零用量与按次费用不能混淆；不放弃未知模型成功用量记录。
- 旧价格与站点模型不能用当前本地目录重算明细或假造历史零价证据。
- 同次成功的日志和令牌结算必须采用同一费用/来源，工具费与站点估算明确分开。
- SQL summary、日志列表、统计趋势、缓存键和 API-token 所有权采用相同筛选。
- 迟到响应、缺少后端 summary、null/未知版本/非有限金额不能显示完整覆盖或实际扣费。

## Task 1: 模型与请求费用快照（main）

Files: `internal/model/log.go`, `internal/model/cost_semantics.go`, tests; `internal/util/cost_calculator.go` and focused tests; `internal/app/cost_semantics.go`, `proxy_util.go`, `proxy_error.go`, `site_pricing.go`, `dashboard_scope.go` and focused tests.

Interfaces: `model.LogCostStatus`, `model.LogCostBasis`; `LogEntry.CostMetadataVersion/CostStatus/CostBasis/CostDetailJSON`; `model.CostSummary` exact JSON fields from spec. Shared request snapshot is transient and releases with fwResult; frozen details only safe numbers.

- [x] Write status and cost regressions, observe RED for zero multiplier, old row, per-call, unpriced/tool partial, negative/nonfinite and site/local multiplier.
- [x] Implement pure classification, frozen amount/detail and production per-result reuse without changing valid accounting amounts; GREEN.
- [x] Project safe metadata/detail for admin and API-token logs; prove historical/price-change and source-specific detail behavior; GREEN.

## Task 2: 存储、迁移、过滤与汇总（storage worker）

Files: `internal/storage/**` only; new migration/SQL cost semantics tests. Model files are owned by main.

Interfaces: Store method `GetCostSummary(ctx context.Context,since,until time.Time,filter *model.LogFilter)(*model.CostSummary,error)`; model fields from Task 1; LogFilter.CostSource/CostStatus/CostBasis. Every log writer/reader/copy path preserves four new columns.

- [x] SQLite migration/reopen and copy RED; version 0 preserved, values never re-priced; implement and GREEN.
- [x] Parameterized source/status/basis filtering using same normalized status as model, including legacy/invalid/failed. Prove list count/stats/trend/summary scoping and unknown filter fail-closed; GREEN.
- [x] SQL-only aggregate summary, pure/mixed storage wrapper and compile assertion; test totals, known zero, partial/historical/unknown coverage and invalid amounts; GREEN.

## Task 3: API 聚合与缓存（main）

Files: `internal/app/handlers.go`, `stats_cache.go`, `admin_dashboard.go`, `admin_stats.go`, related tests; `internal/model/dashboard.go`.

Interfaces: `DashboardSnapshot.CostSummary *model.CostSummary`; `/admin/stats` cost_summary; same forced token identity and BuildLogFilter; cache adds cost fields/search/request_id.

- [x] RED scoped API and cache tests; wire query/error propagation without hiding failures; GREEN.
- [x] Ensure no mutation of cache-shared structures and no unbounded log fetch; summary no external IO.

## Task 4: 控制台（frontend worker）

Files: `console/src/costSemantics.ts`, tests, scoped CSS, `pages/CostSummaryPanel.tsx`, `types.ts`, `pages/LogsPage.tsx`, `DashboardPage.tsx`, `StatsPage.tsx`, `TokensPage.tsx`; API query typing if needed.

Interfaces: cost metadata and summary exactly as spec; filters passed through existing API; safe fallback when metadata missing. No new dependencies or Vite build before coordinated freeze.

- [x] Shared formatter/state RED→GREEN covers known zero, unknown, historical, partial, invalid/nullable data and no false reconciliation.
- [x] Wire same source/status/basis filters, reset page and ignore stale responses; show recorded amount, coverage and not-reconciled text.
- [x] Typecheck/tests, then coordinated build and synthetic browser interaction at 320/390/desktop/light/dark/keyboard; GREEN.

## Task 5: 文档、复核、整合与清理

Files: `docs/cost-semantics.md`, docs index and affected usage pages; main owns spec/plan/research status/verification and evidence scripts.

- [x] Document source/basis/status, history limitations, amount/soft-limit/reconciliation, filters and migration without live claims.
- [x] Fresh independent review, confirmed important fixes with RED→GREEN, full Go internal/lint/frontend/build/docs checks.
- [x] Hash manifest and verified ZIP backup, local main integration and focused/browser/build checks; clean only owned worktree/branch/junction/binary, compress logs and final integrity; update actual checklist/results last.
