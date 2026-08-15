![PivotFlow 封面](docs/assets/pivotflow-cover.png)

# PivotFlow（枢衡）

[English](README.md) · [完整文档](docs/README.md)

PivotFlow 是面向个人使用的 AI API 站点管理与智能路由控制台。

它把多家上游站点的账号、余额、签到、公告和模型清单集中到一个界面，同时保留已验证的路由核心：多 Key、多 URL、优先级、协议转换、冷却和故障切换。PivotFlow 将站点控制面与路由基础整合在同一个应用中。

![PivotFlow 首页概览](docs/assets/dashboard.png)

## 当前能力

### 站点控制面

- 站点探测与平台识别。
- 账号凭证加密保存。
- 余额刷新，签到后显示余额变化。
- 手动或自动签到，并保留账号级结果历史。
- 同步账号模型，使用站点账号直接测试模型。
- 聚合站点公告并记录已读状态。
- 低余额、签到失败等事件的通用 Webhook。
- 将站点账号幂等投影为路由渠道。

### PivotFlow 路由数据面

- OpenAI Chat Completions、OpenAI Responses、Anthropic Messages、Gemini 和 Codex 入口。
- 按模型选择渠道，按优先级、Key 策略和 URL 状态调度。
- Key 级、模型级和渠道级冷却。
- 对认证失败、限流、上游故障、超时和流中断分类并故障切换。
- 本地协议转换、上游原生协议和自动协议协商。
- RPM、并发、成本倍率、日成本上限、WebSocket 和请求日志。

![PivotFlow 渠道与分发](docs/assets/routing.png)

### 运维与观测

- 首页余额、消耗、模型分配、站点分配和客户端入口统计。
- 请求日志、用量统计、消费趋势和活动请求。
- 下游 API 密钥，可限制模型、渠道、成本和并发，并可再次复制已保存密钥。
- Codex OAuth、Antigravity OAuth 及凭证文件导入。
- SQLite、MySQL、PostgreSQL 和主库加 SQLite 副本的混合存储。
- 主题、路由、冷却、日志、通知和只读上游版本检查。

## 四个概念

| 概念 | 保存什么 | 主要用途 |
| --- | --- | --- |
| 站点 | 上游地址、平台、时区和代理 | 标识一个上游服务 |
| 账号 | 站点下的一组登录凭证及状态 | 余额、签到、模型和公告 |
| 渠道 | URL、Key、模型和路由策略 | 承接下游请求并执行 PivotFlow 路由 |
| 下游密钥 | PivotFlow 发放的令牌及限制 | 让 Claude Code、Codex、Gemini 或 OpenAI 客户端调用 PivotFlow |

账号和渠道不是两套必须重复维护的配置。先在站点下添加账号，再在“渠道与分发”执行“同步站点渠道”。PivotFlow 会发现路由 Key 和模型，创建或更新带来源绑定的渠道。只有无法纳入站点管理的特殊上游才使用手工渠道。

多 Key、多分组和同步覆盖规则见 [核心概念](docs/concepts.md) 与 [路由与分发](docs/routing.md)。

## Provider 支持范围

| Provider | 余额 | 模型 | 公告 | 服务端签到 | 说明 |
| --- | :---: | :---: | :---: | :---: | --- |
| New API / One API 系 | ✓ | ✓ | ✓ | ✓ | 具体二开版本以探测结果为准 |
| AnyRouter | ✓ | ✓ | ✓ | ✓ | 支持访问令牌或带用户 ID 的会话 Cookie；必要时可浏览器辅助 |
| Veloera | ✓ | ✓ | ✓ | ✓ | 使用 Veloera 专用用户信息和签到契约 |
| Sub2API | ✓ | ✓ | ✓ | — | 当前服务端签到明确不支持 |
| OpenAI Compatible | — | ✓ | — | — | 仅使用 API Key 发现模型并同步路由的兜底 Provider |
| OneHub / DoneHub / VoAPI / AxonHub | 兼容 | 兼容 | 兼容 | 取决于实现 | 仅在部署保持 New API / One API 契约时兼容 |

“支持”表示 PivotFlow 中存在对应适配器和契约测试，不代表任意第三方实例都开放相同端点或权限。

## 快速启动

### Docker Compose

```powershell
Copy-Item .env.docker.example .env
# 编辑 .env，至少设置 PIVOTFLOW_PASS
docker compose pull
docker compose up -d
```

Linux/macOS 使用 `cp .env.docker.example .env`。该方式直接使用已发布的 `ghcr.io/yzgolden86/pivotflow:latest` 镜像。如果 GHCR 要求登录，请执行 `docker login ghcr.io`，用户名填写 GitHub 用户名，密码使用具备 `read:packages` 权限的个人访问令牌；GitHub 账号密码不能直接用于登录。该账号还必须具有镜像读取权限。打开 `http://127.0.0.1:8080/web/auth/`，使用 `PIVOTFLOW_PASS` 登录。

如果账号无法访问已发布镜像，也可以从当前源码构建：

```powershell
docker compose -f docker-compose.build.yml up -d --build
```

使用已发布镜像的 Compose 文件会把数据挂载到当前目录的 `./data`；源码构建文件使用 Compose 管理的 Docker 卷，逻辑名称为 `pivotflow_data`。两者是独立的数据存储，直接切换可能会看到一个空数据库。迁移数据库和凭证主密钥前请阅读[安全与备份](docs/security.md)。

### 源码构建

```powershell
Copy-Item .env.example .env
go build -tags sonic -o pivotflow.exe .
.\pivotflow.exe
```

默认端口是 `8080`，可通过 `PORT` 修改。当前构建和 CI 使用 Go `1.26` 与 Node.js `24`。

## 第一次配置

1. 在“系统设置”检查存储、请求上限、冷却和日志。
2. 在“站点管理”添加上游，并可在同一表单创建首个账号。
3. 在“账号管理”验证凭证、刷新余额并同步模型。
4. 在“签到中心”执行一次手动签到。
5. 在“渠道与分发”同步站点渠道，检查 URL、模型、Key 数和优先级。
6. 在“下游密钥”创建令牌并填入客户端。
7. 在“模型与测试”执行一次账号直测或渠道测试，再开始真实流量。

## 下游入口

统一代理入口是当前服务的 `/v1/*`：

```text
OpenAI Chat Completions  http://127.0.0.1:8080/v1/chat/completions
OpenAI Responses         http://127.0.0.1:8080/v1/responses
Anthropic Messages       http://127.0.0.1:8080/v1/messages
Gemini                   http://127.0.0.1:8080/v1beta/models/{model}:generateContent
Codex                    http://127.0.0.1:8080/v1/responses
```

客户端只使用 PivotFlow 下游密钥。上游凭证留在站点账号或渠道中，不应写入客户端配置。

## 安全要点

- 设置强管理密码，不要提交 `.env`、数据库、主密钥、Cookie、OAuth 凭证或 API Key。
- 站点凭证和 Webhook URL 会加密保存。个人 SQLite 未显式配置密钥时，会在数据库旁生成 `fusion-master.key`；备份时数据库和该文件必须成对保存。
- `FUSION_MASTER_KEY` 与 `FUSION_MASTER_KEY_FILE` 二选一，生产密钥应放在仓库之外。
- `PIVOTFLOW_ALLOW_INSECURE_TLS=1` 只用于临时排障。
- 管理 API 使用登录会话；代理 API 使用下游密钥。

迁移或恢复数据前请阅读 [安全与备份](docs/security.md)。

## 文档

- [文档索引](docs/README.md)
- [快速开始](docs/getting-started.md)
- [核心概念](docs/concepts.md)
- [站点与账号管理](docs/site-management.md)
- [路由与分发](docs/routing.md)
- [模型测试](docs/model-testing.md)
- [系统配置](docs/configuration.md)
- [部署](docs/deployment.md)
- [安全与备份](docs/security.md)
- [维护与 PivotFlow 同步](docs/maintenance.md)
- [故障排查](docs/troubleshooting.md)

## 架构与来源

PivotFlow 维护站点控制面、管理 API、存储和控制台。PivotFlow 的选择器、冷却、Key/URL 调度和代理链路仍是路由基础。`internal/protocol/cliproxy` 是固定提交的纯协议转换快照，来源见 [UPSTREAM.md](internal/protocol/cliproxy/UPSTREAM.md)。

上游发布不会自动覆盖 PivotFlow，而是按 [维护说明](docs/maintenance.md) 人工审计、移植和验证。

## 验证

```bash
go test -tags sonic ./internal/...
make verify-web
go test ./internal/antigravityauth -count=1
```

前端开发：

```bash
cd console
npm ci
npm run dev
```

## 许可证

MIT，见 [LICENSE](LICENSE)。第三方代码的来源和保留声明与对应代码放在一起。PivotFlow 只应用于你有权访问的上游服务和账号。

## 致谢

PivotFlow 的实现离不开开源社区已有的积累与启发，特别感谢：

- [ccLoad](https://github.com/caidaoli/ccLoad)：为 PivotFlow 提供并保留了路由选择、调度、冷却、故障切换与协议转换基础。
- [Metapi](https://github.com/cita-777/metapi)：站点聚合、账号管理流程与首页看板设计的重要参考。
- [All API Hub](https://github.com/qixing-jk/all-api-hub)：多站点资产管理、Provider 兼容和交互设计的重要参考。
- [Octopus](https://github.com/Hureru/octopus)：个人网关、渠道管理和简洁控制台设计的重要参考。
- [Sub2API](https://github.com/Wei-Shaw/sub2api) 与 [New API](https://github.com/QuantumNous/new-api)：上游接口契约、生态约定和管理流程的重要参考。

感谢以上项目的维护者与贡献者为社区提供优秀的开源成果。此处的参考与致谢不替代各项目自身的许可证及版权声明。

**学 AI，上 [LINUX.DO](https://linux.do)。**
