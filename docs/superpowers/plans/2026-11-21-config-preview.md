# 配置导入与渠道同步差异预览 Implementation Plan

> **For agentic workers:** Use executing-plans for the main implementation and dispatching-parallel-agents for disjoint bounded storage/UI tasks. No commits in this session.

**Goal:** 在配置写入前提供可信差异并确保应用所确认的快照。
**Architecture:** 有界内存预览、共享差异 DTO、配置事务、沿用站点任务队列。
**Tech Stack:** Go / Gin / database/sql；React / TypeScript，无新增依赖。
**Spec:** [设计](../specs/2026-11-21-config-preview-design.md)

## Global Constraints

- 仅在 `.worktrees/config-preview` 写源代码；从当前主工作区 1004 个文件快照开始。
- 九个受保护文件仅校验散列；所有占位符原样保留；无提交、推送、部署。
- 差异响应不能包含秘密、原始 SQL/provider 错误、凭证散列。
- 不改变现有视觉系统和站点预算、租约、取消机制。
- 前端构建结束后再执行 Go embed 构建。

## Review Focus

- 并发编辑使预览失效；同一预览重复提交不能重复执行。
- 备份源 ID 与目标 ID 冲突不能指向错误对象或扩大令牌权限。
- 手动渠道与已停用渠道不被同步无意覆盖/启用。
- 发现不完整时不能删除或停用未知 Key；恢复中途错误须回滚。
- 文件选择、账号选择、弹窗关闭、网络丢失后的迟到响应不能错误报告成功。

## Task 1: 配置事务

Files: `internal/storage/configuration_transaction.go`, `internal/storage/sql/configuration_transaction.go`, SQL query/transaction wrappers and focused tests.

Interface: `storage.WithConfigurationTransaction(ctx, store, func(context.Context, storage.Store) error) error`; `storage.ConfigurationSource(store) storage.Store`. Main writes use returned authoritative transaction view. No external network or cache effects inside callback.

- [x] 写回滚、嵌套现有方法、主库读取及混合缓存测试并观察失败。
- [x] 实现事务视图和提交后混合配置复制；测试通过。

## Task 2: 备份预览与应用

Files: `internal/app/config_preview.go`, `backup_preview.go`, tests, `admin_backup.go`, routes.

Interface: POST `/admin/backup/preview` `{data}`; POST `/admin/backup/webdav/preview` `{}`; POST `/admin/config-previews/:id/apply` `{}`; GET/DELETE `/admin/config-previews/:id`. API uses existing RespondJSON envelope; legacy blind import endpoints return 428 `preview_required`.

Preview: `{id,kind,expires_at,can_apply,items:[{key,entity,label,action,fields:[],preserved_fields:[],message}],summary:{create,update,unchanged,preserve,conflict,deactivate},status,error?,result?}`. `kind=backup|site_sync`; status `ready|applying|applied|failed|stale|unknown`; apply returns `{preview_id,status,items,backup?:BackupImportResult,warning?}`. `commit_unknown` blocks retry; `cache_sync_degraded` means committed.

- [x] 覆盖只读、冲突、陈旧、回滚、脱敏、协议限制、重复应用。
- [x] 实现有界缓存与同一规划器；本地及 WebDAV 入口接入。

## Task 3: 渠道同步预览

Files: `internal/app/site_projection_preview.go`, tests, task DTO/routes.

Interface: POST `/admin/site-accounts/:id/sync-preview` `{}` returns `{task_id,merged}`; task `kind=sync_preview`, successful `result_ref=config_preview:<id>`; use existing GET task and cancel APIs, then GET preview. Apply uses shared preview endpoint.

- [x] 覆盖新建、更新、手动保留、移除停用、部分失败、陈旧、幂等。
- [x] 实现只发现不投影的任务与事务应用；修正旧同步路径不覆盖手动所有权。

## Task 4: 控制台

Files: `console/src/configPreview.ts`, tests, `pages/ConfigPreviewPanel.tsx`, `pages/SiteSyncPreviewModal.tsx`, backup panel, channel modal wiring, scoped CSS.

Interface consumes Task 2/3 DTO. API wrapper in new module so existing shared api.ts needs no conflict.

- [x] 状态机/筛选/错误翻译 tests RED→GREEN；交互按选择序号丢弃过期响应。
- [x] 备份与同步预览确认、逐项结果、未知结果复查、过期刷新。
- [x] 合成浏览器验证桌面/移动/暗色/键盘和秘密不可见。

## Task 5: 完成验证与整合

- [x] 新鲜上下文复核及重要问题回归。
- [x] Go internal suite、lint、前端 typecheck/tests/build、Go build、文档链接。
- [x] 更新用户文档、路线进度及验证记录。
- [x] SHA256 校验备份后整合主目录；主目录冒烟；清理本轮 worktree/二进制并压缩证据；再次校验保护文件。
