# 系统配置

## 启动环境变量

| 变量 | 用途 |
| --- | --- |
| `PIVOTFLOW_PASS` | 管理后台密码，必填 |
| `PORT` | HTTP 监听端口，默认 `8080` |
| `SQLITE_PATH` | SQLite 路径，默认 `./data/pivotflow.db` |
| `PIVOTFLOW_BILLING_JOURNAL_PATH` | 本实例待结算记录；默认与 `SQLITE_PATH` 同目录的 `billing-pending.db`，需持久保存 |
| `FUSION_MASTER_KEY` | 凭证加密主密钥 |
| `FUSION_MASTER_KEY_FILE` | 从文件读取凭证加密主密钥 |
| `PIVOTFLOW_API_TOKENS` | 启动时预置访问令牌，已有 token 不覆盖 |
| `PIVOTFLOW_ANTIGRAVITY_CLIENT_ID` | Antigravity OAuth 客户端 ID |
| `PIVOTFLOW_ANTIGRAVITY_CLIENT_SECRET` | Antigravity OAuth 客户端密钥 |
| `PIVOTFLOW_MYSQL` | 使用 MySQL 主存储 |
| `PIVOTFLOW_POSTGRES` | 使用 PostgreSQL 主存储 |
| `PIVOTFLOW_ENABLE_SQLITE_REPLICA` | 开启主库 + SQLite 副本 |
| `TRUSTED_PROXIES` | 可信代理 CIDR，`none` 表示不信任代理 |
| `PIVOTFLOW_ALLOW_INSECURE_TLS` | 临时禁用上游 TLS 校验，生产禁用 |

完整注释见 [.env.example](../.env.example) 和 [.env.docker.example](../.env.docker.example)。

## 系统设置页面

运行参数已迁移到管理控制台并持久化到数据库，包含请求体上限、并发、冷却、日志保留、Key 重试、上游超时、渠道选择策略、模型统一映射、默认测试内容、每日自动签到与公告刷新时间、通知和只读版本检查。保存需要重启的设置时，PivotFlow 会请求当前进程优雅重启。

大多数系统设置在启动时读入内存，保存后进程会在约两秒后请求优雅重启。“路由策略”属于这一类。模型统一映射和请求日志自动刷新间隔是例外：保存后立即生效，无需重启。控制台与管理 API 中每个设置的 `requires_restart` 字段表示实际生效方式。

“路由策略”里的渠道选择策略与模型统一映射见 [路由与分发](routing.md)；“系统访问”里的只读诊断令牌见 [安全与备份](security.md)。

“请求缓存”默认关闭，开关、TTL、条目数、总正文容量和单条容量都在重启后生效。客户端仍需显式选择 `X-PivotFlow-Cache: use`；面板单独展示当前进程策略、容量、命中／绕过与失效原因，可确认清空内存响应。支持范围、费用口径和示例见[可选请求缓存](request-cache.md)。

“通知设置”可分别启用通用 Webhook 与 Telegram；“导入导出”可生成完整配置、连接与路由或系统设置 JSON，也可配置 WebDAV 手动/定时备份。导入 JSON、从 WebDAV 恢复和“同步站点渠道”都会先显示差异预览，确认后应用同一份固定快照；旧的无预览导入接口返回 HTTP 428 与 `preview_required`。步骤和安全边界见 [配置导入与渠道同步](config-import.md)。

“只读版本检查”只显示上游发布信息，不会自动覆盖 PivotFlow 的融合程序。

## 存储选择

- 个人单机：SQLite。
- 多实例或已有数据库服务：MySQL 或 PostgreSQL。
- 需要主库持久化、节点本地缓存：混合存储。

不要在多个进程之间共享同一个 SQLite 文件，除非你清楚 WAL/锁和副本模式的边界。

所有存储模式都会使用本地待结算记录；远程主库部署也需要为它保留持久卷。它固定使用 WAL 和 `synchronous=FULL`，不受 `SQLITE_JOURNAL_MODE` 控制，应放在支持 SQLite WAL 的本地文件系统。`SQLITE_PATH` 使用 URI 或内存数据库时，必须显式指定待结算文件路径。恢复、503 故障与运行指标见 [可靠结算与流式恢复](reliability.md)。
