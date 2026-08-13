# 系统配置

## 启动环境变量

| 变量 | 用途 |
| --- | --- |
| `CCLOAD_PASS` | 管理后台密码，必填 |
| `PORT` | HTTP 监听端口，默认 `8080` |
| `SQLITE_PATH` | SQLite 路径，默认 `./data/ccload.db` |
| `FUSION_MASTER_KEY` | 凭证加密主密钥 |
| `FUSION_MASTER_KEY_FILE` | 从文件读取凭证加密主密钥 |
| `CCLOAD_API_TOKENS` | 启动时预置下游密钥，已有 token 不覆盖 |
| `CCLOAD_ANTIGRAVITY_CLIENT_ID` | Antigravity OAuth 客户端 ID |
| `CCLOAD_ANTIGRAVITY_CLIENT_SECRET` | Antigravity OAuth 客户端密钥 |
| `CCLOAD_MYSQL` | 使用 MySQL 主存储 |
| `CCLOAD_POSTGRES` | 使用 PostgreSQL 主存储 |
| `CCLOAD_ENABLE_SQLITE_REPLICA` | 开启主库 + SQLite 副本 |
| `TRUSTED_PROXIES` | 可信代理 CIDR，`none` 表示不信任代理 |
| `CCLOAD_ALLOW_INSECURE_TLS` | 临时禁用上游 TLS 校验，生产禁用 |

完整注释见 [.env.example](../.env.example) 和 [.env.docker.example](../.env.docker.example)。

## 系统设置页面

运行参数已迁移到管理控制台并持久化到数据库，包含请求体上限、并发、冷却、日志保留、Key 重试、上游超时、默认测试内容、通知和只读版本检查。保存需要重启的设置时，PivotFlow 会请求当前进程优雅重启。

“只读版本检查”只显示上游发布信息，不会自动覆盖 PivotFlow 的融合程序。

## 存储选择

- 个人单机：SQLite。
- 多实例或已有数据库服务：MySQL 或 PostgreSQL。
- 需要主库持久化、节点本地缓存：混合存储。

不要在多个进程之间共享同一个 SQLite 文件，除非你清楚 WAL/锁和副本模式的边界。
