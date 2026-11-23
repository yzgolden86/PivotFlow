# Restore Verification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking. 用户已授权连续推进与本地整合；不创建提交。

**Goal:** 为配置恢复提供独立 SQLite 回归证据，为 WebDAV 上传提供有界回读校验及可区分的状态，并给出升级与回退操作说明。

**Architecture:** 复用现有配置预览与事务导入。上传固定 JSON 后只读回查相同目标，以字节数和 SHA-256 判断一致；观察与设置使用配置事务保存。页面显示持久校验证据，文档分别解释可迁移配置与运行数据库恢复。

**Tech Stack:** Go 1.26.10 / sonic、现有 SQLite/SQL Store、React/TypeScript/Vite、Node test、Python Playwright。

**Spec:** [恢复验证设计](../specs/2026-11-22-restore-verification-design.md)

## Global Constraints

- 来源为当前主目录完整未提交状态，1050 文件及九个保护文件均有散列基线；隔离工作区 `.worktrees/restore-verification`。
- 无新依赖；JSON `1.0`、上限 32 MiB；回读最多 5 次/1 秒间隔/10 秒整体截止时间，只重试 GET。
- 所有验证使用自有临时数据库、合成密钥与模拟上游；不运行生产服务，不提交、推送、发布或部署。
- Vite 构建与 Go root/embed 构建或 lint `./...` 不并行；保留双花括号占位符。

## Review Focus

1. 上传 2xx 而远端文件缺失/旧内容/HTML/过大时，不产生成功校验证据；归 Task 2 模拟 WebDAV 回归。
2. 上传期间保存新目标，或旧状态已更新再保存设置，旧结果不能覆盖新配置或丢失新观察；归 Task 2 事务与竞争回归。
3. 目标 ID 与源 ID 相撞、allow/deny 及空列表恢复时，实际认证/模型/渠道限制不会扩大；归 Task 1 磁盘恢复与权限回归。
4. 旧数据库增列、主库已提交而副本失败、校验后状态持久化失败，结果能正确区分；归 Task 2 迁移与状态失败回归。
5. 旧服务器/旧记录或畸形校验证据不能被页面标为已验证；警告与长错误在窄屏可读；归 Task 3 单测和浏览器回归。

---

### Task 1: 独立数据库恢复矩阵

**Files:** Create `internal/app/backup_restore_test.go`；必要时最小修改 `backup_preview.go`、`admin_backup.go`，修复由失败测试证实的问题。

**Bounded sidecar:** `internal/storage/sync_manager.go` 与新建 `sync_manager_restore_test.go` 由独立 worker 负责，证明并修复主库空表恢复不清理旧副本的问题；不与应用恢复或 WebDAV 文件重叠。

**Model storage sidecar:** `internal/storage/sql/site.go`、新建 `site_restore_models_test.go`、`store.go` 与 `hybrid_site.go` 由第二个 worker 负责，提供 `RestoreSiteAccountModels(context.Context,int64,[]model.SiteAccountModel) error`；应用预览由主执行者负责，日常发现接口语义保持。

**Interfaces:** 使用 `buildBackupDocument(context.Context,string)`、`previewBackupDocument(context.Context,*backupDocument)` 与既有按预览 ID 应用流程，真实磁盘 `storage.CreateSQLiteStore(path)`；不添加生产演练 API。

- [x] 写不同磁盘库/密钥、预置目标 ID 冲突、引用/凭证/绑定和全部令牌限制的恢复测试；关闭再打开后重新验证实际存储和权限。
- [x] 运行 `go test -tags sonic -buildvcs=false -count=1 ./internal/app -run 'TestBackupRestore'`，记录当前行为；缺陷测试必须先失败，再最小修复。已存在行为的验收测试允许初次通过，不能虚称 RED。
- [x] 验证历史令牌警告、已有统计保留、无效引用回滚与重复应用；相关恢复/预览测试通过后登记 Task 1。
- [x] 主库成功读取为空时清空副本；查询/写入故障保留旧副本，用合成主库/副本回归证明，独立记录 RED→GREEN。
- [x] 新备份显式携带绑定引用并按目标 ID 恢复为手动所有权；旧文件仍保留原契约。按导出账号替换空模型清单，重复同模型引用冲突，停用/陈旧字段精确恢复而日常发现保留人工停用。

### Task 2: WebDAV 回读与持久观察

**Files:** Create `internal/app/backup_verification.go`、`backup_verification_test.go`、`internal/storage/backup_verification_migration_test.go`；Modify `admin_backup.go`、`admin_backup_test.go`、`server.go`、`internal/model/backup.go`、`internal/storage/sql/backup.go`、`internal/storage/schema/tables.go`、`migrate.go`、`migrate_columns.go`。

**Interfaces:** `exportToWebDAV(ctx,type) (*backupExportResult,error)`，结果保留既有 `status/file_url/last_sync_at` 并添加 `verification`、`metadata_saved` 和安全警告；`verifyWebDAVUpload(ctx,client,targetURL,username,password,body)` 只读验证；状态和设置均复用 `storage.WithConfigurationTransaction`。

- [x] 添加失败测试：2xx+旧文件、HTML、认证失败、429/425 后可见、取消、超限响应/导出、竞争设置、状态写入失败；断言 PUT 仅一次、成功证据只属于正确目标，失败保留上次观察。
- [x] 运行 WebDAV 新测试并确认缺失行为造成的失败；实现有界校验、安全错误、响应与事务记录，更新原有 HTTP 回归预期。
- [x] 添加并运行旧库增列/重开幂等、字段保存、目标变更清证据测试；旧上传时间保留而校验字段为空。
- [x] 验证校验成功但主库观察保存失败时返回独立警告，调度仅重试状态并按内存校验时间计算间隔；配置改变清除该观察。添加旧程序再次上传导致较早证据失效的前端回归。
- [x] 运行 `go test -tags sonic -buildvcs=false -count=1 ./internal/app ./internal/storage ./internal/storage/sql -run 'Test.*Backup|Test.*WebDAV|Test.*Restore'`，记录 Task 2。

### Task 3: 校验反馈与恢复/升级文档

**Files:** Create `console/src/backupVerification.ts`、`backupVerification.test.ts`、`docs/backup-recovery.md`、`docs/upgrade.md`；Modify `BackupSettingsPanel.tsx`、`types.ts`、`api.ts`、`styles.css`、`docs/security.md`、`deployment.md`、`config-import.md`、`docs/README.md`、项目 README 与研究状态入口。

**Interfaces:** 前端校验证据解析器只接受完整时间/32 MiB 内大小/64 位摘要/有效范围与警告计数；旧或错误响应不得标为已校验。备份反馈使用现有 OperationNotice、HelpTip 与卡片样式。

- [x] 写前端失败测试：成功状态无证据、旧时间、缺失/错误字段、校验后状态保存警告，先观察失败再实现解析与反馈。
- [x] 改页面范围文案、持久观察/警告和长文本换行；完善文档，给出可直接运行的合成恢复命令与部署环境验收，不对 prose 添加实现镜像测试。
- [x] 运行前端完整测试、双 TS、Vite 构建；合成浏览器核对正常、历史、缺失、失败、警告、设置变化、320/390px 和深浅主题，保存可见视口 JPEG。

### Task 4: 审查、整合与资源清理

**Files:** Create 阶段验证文档；更新研究进度。证据、整合和清理脚本放 `archive/restore-verification-2026-11-22/`。

**Interfaces:** 基线散列清单、阶段差异清单、已覆盖文件 ZIP 与校验清单、最终完整性记录；整合仅采用本阶段差异。

- [x] 完成新鲜上下文代码审查，处理已证实的重要问题，以回归证明；完整后端、前端、lint、Go embed 构建和文档链接通过。
- [x] 整合前确认主目录基线、九个保护文件与发现 symlink；覆盖/移除文件先 ZIP 并逐项验 SHA-256，本地整合后验所有差异和未触碰基线。
- [x] 主目录定向复验与合成浏览器复验；清理自有工作区、临时分支、junction、验证二进制和缓存，压缩完整证据并记录实际释放空间。
- [x] 完成计划勾选、验证文档与 `phase-completion.json`，最终散列确认；中文交付实际功能、证据、边界和下一项路线。
