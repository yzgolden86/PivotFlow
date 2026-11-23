# Octopus 最新功能、可靠性与 UI 深度研究报告

- 研究日期：2026-11-19（Asia/Shanghai）
- 上游仓库：[bestruirui/octopus](https://github.com/bestruirui/octopus)
- 固定审查点：[`master@8aa58b0`](https://github.com/bestruirui/octopus/commit/8aa58b0340ba2de58ce74fbfacd32597c44842a2)，即 [v0.13.13](https://github.com/bestruirui/octopus/releases/tag/v0.13.13)
- 对比项目：PivotFlow，参考其 [README](../../../README.md) 与 [CLAUDE.md](../../../CLAUDE.md)
- 研究范围：版本与发布、渠道/Key/模型/分组/路由、协议与流式、认证、费用与配额、存储、备份、部署升级、公开 issue、UI，以及对 PivotFlow 的可借鉴设计
- 完整证据附表：[发布时间线](sources/octopus-release-timeline.md)、[功能矩阵](sources/octopus-feature-matrix.md)、[UI 证据](sources/octopus-ui-evidence.md)、[仓库关系](sources/octopus-repository-relationship.md)、[源码索引](sources/octopus-source-index.md)

## 执行摘要

1. Octopus 当前默认分支是 `master`，不是 `main`。截至核查时，`master` HEAD 与最新 stable tag `v0.13.13` 都指向 `8aa58b0340ba2de58ce74fbfacd32597c44842a2`；最新 release 发布于 2026-10-23T01:03:51Z，标记为正式版，未发现公开 prerelease。v0.13.13 只做 Go 1.26.10、AxonHub 依赖与版本号升级，以关闭安全相关 issue #426，并不是新增路由能力的功能版。

2. 当前核心数据模型不是简单的“渠道下有模型”，而是 `Channel → ChannelModel × ChannelKey → ChannelGrant（模型 × Key × 协议位） → Group`。授权矩阵和按 Key 独立探测是 Octopus 最有借鉴价值的产品设计。

3. 当前路由只有两种：`manual` 和 `failover`。没有轮询、随机、加权、低成本优先、健康评分、嵌套分组、Gemini、Images 或 WebSocket。仓库中固定提交的分组截图仍显示“轮询/随机/加权分配”，与当前后端枚举不一致，是素材陈旧问题，不能把这个截图当作功能证据。

4. 可靠性的强项是“首个语义输出前才允许故障转移”、对若干流式终止事件的严格校验、单一恢复探测和故障切换后的成员亲和。明显缺口是 manual 失败后持续重试、所有 failover 成员都在冷却时只等待、冷却/亲和状态仅保存在进程内，以及缺少 PivotFlow 已有的细粒度错误分类和全冷却最早恢复兜底。因此它更像可运维的个人单实例网关，而非经证据证明的多副本高可用网关。

5. UI 的核心不是装饰，而是把复杂对象落到可操作的界面：五步渠道表单、模型 × Key × 协议三态矩阵、渠道 → 模型 → Key 三级候选、拖拽优先级、实时冷却/亲和倒计时、逐轮请求证据、首字/速度/缓存/费用同卡展示。视觉系统统一、移动 Dock 连贯，但公开截图存在实现漂移，且没有充分的交互、对比度、reduced-motion 或屏幕阅读器实测证据。

6. PivotFlow 已在多 URL、Key 选择、细粒度错误分类、指数退避、全冷却兜底、平滑加权轮询、健康评分、协议转换、WebSocket execution session、成本/并发配额、SQL/WebDAV 备份等方向更强。最值得借鉴的是 Octopus 的能力矩阵、操作反馈和请求证据组织，而不是复制其较弱的调度状态机或 AGPL 代码。

## 一、版本、分支与发布事实

### 1.1 当前状态

| 项目 | 核实结果 | 证据 |
|---|---|---|
| 仓库身份 | `bestruirui/octopus`，公开、非 fork、未归档 | [Repo API](https://api.github.com/repos/bestruirui/octopus) |
| 默认分支 | `master` | [Repo API](https://api.github.com/repos/bestruirui/octopus) |
| `master` HEAD | `8aa58b0340ba2de58ce74fbfacd32597c44842a2` | [HEAD API](https://api.github.com/repos/bestruirui/octopus/commits/master) |
| HEAD 提交时间 | 2026-10-23T00:49:11Z | [固定提交](https://github.com/bestruirui/octopus/commit/8aa58b0340ba2de58ce74fbfacd32597c44842a2) |
| 最新 stable | `v0.13.13` | [Latest Release API](https://api.github.com/repos/bestruirui/octopus/releases/latest) |
| 发布时间 | 2026-10-23T01:03:51Z | [v0.13.13](https://github.com/bestruirui/octopus/releases/tag/v0.13.13) |
| 最新 prerelease | 未发现公开 prerelease | [Releases API](https://api.github.com/repos/bestruirui/octopus/releases?per_page=100) |
| tag 与分支关系 | `v0.13.13` 的 target 为 `master`，SHA 等同 HEAD | [Release API](https://api.github.com/repos/bestruirui/octopus/releases/latest) |
| 许可证 | AGPL-3.0 | [Repo API](https://api.github.com/repos/bestruirui/octopus)、[LICENSE](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/LICENSE) |
| 快照社区数据 | 约 2,749 stars、448 forks、`open_issues_count=17` | [Repo API](https://api.github.com/repos/bestruirui/octopus)，数字会变化，且计数可能包含 PR |

`main` 与 `master` 必须分开：请求 `/commits/main` 不能代表主线，`branches/main` 返回 404。报告中的所有源码永久链接均固定到 `8aa58b0`，不使用会移动的分支 URL 论证行为。

### 1.2 v0.13.13 是什么，不是什么

v0.13.13 的提交摘要为 8 行变化（新增 5、删除 3），涉及 `go.mod`、`go.sum` 和版本文件；Go 从 1.26.4 升到 1.26.10，AxonHub 依赖从 2026-10-14 伪版本更新到 2026-10-22 伪版本。提交说明是 `CVE-2026-111943`，关闭 issue #426。

- [提交与 patch 证据](https://api.github.com/repos/bestruirui/octopus/commits/8aa58b0340ba2de58ce74fbfacd32597c44842a2)
- [Go 官方问题 #82345](https://github.com/golang/go/issues/82345)

CVE-2026-111943 位于 Go `crypto/x509` 的信任判定面。可靠结论是“Octopus 升级运行时和相关依赖来消除依赖面风险”；不能说 CVE 是 Octopus 自研代码产生，也不能把安全升级写成新功能发布。

### 1.3 近约 90 天的 stable 时间表

2026-08-21 至 2026-11-19 的核查窗口中，公开 release 为 16 个 stable、0 个 prerelease：

| Tag | SHA | 发布时间（UTC） | 主要变化 |
|---|---|---|---|
| v0.12.0 | 6eb8b5251553f8ecfe8dcda0cb87decc60a8b1aa | 2026-08-24 07:32:28 | 自定义 Key、模型统计、渠道模型拆表；流式异常与 Windows 更新修复 |
| v0.12.1 | e7a145513a1b66c81c7c60ff27cbfdc3f35f4a7c | 2026-08-25 14:35:36 | 渠道统计改版、分享 |
| v0.13.0 | f07b8dc026a01c0fa08bbbdbc4662d9bb6a92703 | 2026-09-01 10:02:54 | 删除 Gemini、单渠道多 Key、数据库迁移修复、升级前备份提醒 |
| v0.13.1 | dd21e66b8d841729bddb49a45a7bb59fb8c497d1 | 2026-09-02 04:19:42 | gzip、全新数据库迁移修复 |
| v0.13.2 | 27aa40dc0f3b2902bce3e96ccdba019d17041606 | 2026-09-02 10:38:34 | 前端优化 |
| v0.13.3 | 7a720e9ef4fe69e24765d66d9cad76a17fb183a0 | 2026-09-10 01:38:48 | 日志显示来源 API Key、实时状态 |
| v0.13.4 | 6dce286bda7f078b2fa8831bc59783a012261d3b | 2026-09-10 02:54:57 | `client_header`、渠道禁用修复、日志 UI |
| v0.13.5 | 6f330f5ac004bd602aabf50074b759416c6e25d0 | 2026-09-19 09:20:07 | 全局模型过滤、token speed、缓存率、手动取消 |
| v0.13.6 | 4a54b59e2f489c92070cb1cec5ed12cd0bb72e22 | 2026-09-20 01:22:15 | 关闭 #398，修复流式 `context canceled` 误报 |
| v0.13.7 | 0e1c3fc6844d15c5e36266811719ebe706cb84bb | 2026-09-23 08:17:50 | 厂商预设修复 |
| v0.13.8 | 9357a322ff1979bd2bf825ce8e2322e7144a8e1e | 2026-09-23 12:58:19 | 渠道创建错误弹窗、思考等级、日志布局 |
| v0.13.9 | a94a74049e7264ec49304e3d13540fffd6cd2949 | 2026-09-30 03:12:09 | 更多模板、日志显示渠道 Key |
| v0.13.10 | d1a26bdd8770a05ee03e73d867c2cf5af9fa2951 | 2026-10-07 08:50:33 | 反代子路径、分组置顶/置底、`passwd`、迁移修复 |
| v0.13.11 | fd8b3b86c7236f8951b3542eaf734f44a3d9ad3c | 2026-10-13 07:25:47 | 更严格流式响应检查 |
| v0.13.12 | 3d1ff135ec56f13417ffdb8b344bde56a1531202 | 2026-10-14 03:33:12 | UA 请求头透传 |
| v0.13.13 | 8aa58b0340ba2de58ce74fbfacd32597c44842a2 | 2026-10-23 01:03:51 | Go/依赖安全升级，关闭 #426 |

每条 tag 的永久页面可写为 `https://github.com/bestruirui/octopus/releases/tag/<tag>`；逐项证据见 [发布时间线](sources/octopus-release-timeline.md)。

## 二、仓库关系与旧参考包边界

### 2.1 `bestruirui/octopus` 与 `Hureru/octopus`

`Hureru/octopus` 是 fork：

- [Hureru Repo API](https://api.github.com/repos/Hureru/octopus) 明确 `fork=true`，parent/source 指向 `bestruirui/octopus`。
- Hureru 默认分支是 `dev`，`pushed_at=2026-11-18T19:15:15Z`；上游默认分支是 `master`。
- Hureru README 自述 “Forked from bestruirui/octopus”，并称其开发线大量领先。
- 没有证据显示原仓库发生所有权转移。PivotFlow README 致谢的 [Hureru/octopus](https://github.com/Hureru/octopus) 应保留为 fork 指向，研究上游最新主线时则必须使用 bestruirui。

### 2.2 本地 `octopus-dev.zip`

只查看 zip manifest 与少数特定文件，没有解压整个目录：

- 文件大小：7,044,564 bytes
- SHA-256：`3185A44FFD37BFE3597CFE623BDDD2DD7C5D350FCC3DF698B1085EE2AD45CE7E`
- ZIP 修改时间：2026-08-09；多数内部文件时间：2026-07-02
- `internal/conf/version.go`：`Version=dev`、`Commit=unknown`、`Author=hureru`、`Repo=https://github.com/Hureru/octopus`
- `go.mod`：Go 1.25.0

因此，旧包是 Hureru fork 的 2026-07 开发快照，不能用作 bestruirui 的 2026-08 版本，也不能据其中轮询、加权、WebSocket、Images、Gemini、站点投影等能力反推上游已实现。旧包与上游主线的功能差异应单列为“fork 对照”，不进入上游版本时间表。

## 三、当前功能结构与边界

### 3.1 从渠道到分组的对象模型

固定源码可从 [channel.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/channel.go) 和 [group.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/group.go) 读出：

```text
Channel
  ├─ URL、代理、Header、参数覆盖、过滤、协议路径
  ├─ ChannelModel[]            上游模型名
  └─ ChannelKey[]              命名、启停、独立统计
            │
            └─ ChannelGrant   ChannelModel × ChannelKey × 协议位掩码
                              ├─ OpenAI Chat Completions
                              ├─ OpenAI Responses
                              └─ Anthropic Messages
                                       │
Group（客户端模型名）                    │
  └─ GroupItem[] ──────────────────────┘
       Priority、manual 当前项或 failover 顺序
```

Grant 是最小路由单位，这比“渠道模型启用/禁用”更细。它允许同渠道的不同 Key 对同一模型拥有不同协议能力，也让 UI 能用矩阵批量授权。v0.13.0 的“单渠道多 Key”是形成这一模型的关键节点。

### 3.2 分组模式

`GroupMode` 只有：

```go
const (
    GroupModeManual   GroupMode = "manual"
    GroupModeFailover GroupMode = "failover"
)
```

请求绑定也是 `oneof=manual failover`。当前不存在：

- 轮询、随机、加权分配
- 成本优先、延迟优先、成功率健康评分
- 嵌套分组
- 多实例共享的调度状态
- Redis/数据库中的跨请求 affinity 或 cooldown

固定提交内附带的 `desktop-group.png` 仍显示旧四种模式，因此评估时必须坚持“源码高于展示素材”。产品事实是：**配置 UI 源码与旧截图存在漂移风险；路由事实以后端 `GroupMode` 为准。**

### 3.3 默认 Relay 参数

[group.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/group.go) 的 `DefaultGroupRelayConfig` 给出：

| 参数 | 默认值 | 含义 |
|---|---:|---|
| `member_max_attempts` | 2 | 单成员包含首次请求的总尝试数；主要作用于 failover |
| `member_retry_interval_seconds` | 3 | 相似尝试之间等待 |
| `member_non_stream_response_timeout_seconds` | 120 | 非流式完整响应超时 |
| `member_stream_first_event_timeout_seconds` | 30 | 流式首个有效事件超时 |
| `member_cooldown_seconds` | 60 | 成员在本请求耗尽尝试后被跳过的秒数 |
| `member_affinity_seconds` | 300 | 故障切换成功后的保持时间，0 表示不保持 |

后端 Normalize 会用上述默认值补齐零值；若 UI 初始值看似不同，应以服务端 Normalize 后的实际行为为准。

### 3.4 功能矩阵

| 能力 | 当前状态 | 关键结论 |
|---|---|---|
| 渠道 URL/代理/Header/参数覆盖 | 已实现 | 能表达常见自建反代与供应商兼容参数 |
| 单渠道多 Key | 已实现 | Key 独立启用、探测、统计 |
| 模型映射 | 已实现 | Group 名作为客户端模型，上游模型由 Grant/ChannelModel 指定 |
| 模型 × Key × 协议授权 | 已实现 | 三态矩阵，粒度细致 |
| manual | 已实现 | 固定人工成员；失败不冷却、不自动切换 |
| failover | 已实现 | Priority 顺序、失败冷却、单探针、成功亲和 |
| 轮询/随机/加权/低成本 | 未实现 | 价格只用于计费，不参与选路 |
| 健康评分 | 未实现 | 只有冷却与失败阈值；无成功率和 TTFB 综合分 |
| Gemini | 未实现 | v0.13.0 已删除；README 旧行不可信 |
| OpenAI Chat / Responses / Anthropic | 已实现 | 当前三项协议位 |
| Images / WebSocket | 未实现 | Codex 配置明确 `supports_websockets=false` |
| 流式故障转移 | 部分/有边界 | 首个语义输出前可切换，提交后不可回滚 |
| 细粒度错误分类 | 部分 | 有基础重试和流检查，未达到 PivotFlow 分类器深度 |
| 模型/Key/地址/API Key 统计 | 已实现 | 总、日、小时和多对象层级 |
| 价格、cache 费用 | 已实现 | 自定义价优先，cache write/read 可计价 |
| 下游费用/模型/过期配额 | 部分 | 请求前检查，异步统计有单请求超额窗口 |
| SQLite/MySQL/PostgreSQL | 已实现 | 启动迁移 |
| JSON dump/restore | 部分 | dump v5；导入为 do-nothing/upsert 合并，不是无损恢复 |
| WebDAV 定时备份 | 未实现 | 不要与 PivotFlow 的已有能力混淆 |
| Docker/CLI | 已实现 | `octopus start`、卷 `/app/data` |
| 自动更新 | 部分/有供应链风险 | 下载 latest zip 后替换并重启；未见 release 签名绑定 |
| 日志长期审计 | 部分 | 实时详情主要在内存，已结束请求最多 50 个；统计持久化 |

详细逐项源码见 [功能矩阵证据](sources/octopus-feature-matrix.md)。

### 3.5 协议与流式

固定源码中的协议位只有 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages。v0.13.0 删除 Gemini 后，README 若仍出现 Gemini 是文档滞后。当前 Responses 配置明确不支持 WebSocket。

请求循环对首个可提交响应与“已向客户端提交”区分：

1. 上游返回 synthetic start 时，先把启动事件暂存。
2. 首事件之前发生上游错误，可以按配置重试当前成员或切换下一个成员。
3. 首个含实际语义输出的事件被编码并写给下游时，调用 `markCommitted`。
4. 一旦提交，后续上游失败记录为流中断，不会假装“还能无缝 failover”；否则客户端已经看到不一致输出。
5. OpenAI Chat 流会自动补 `stream_options.include_usage=true`，以便得到 usage。
6. 严格校验 Responses `failed/incomplete/cancelled/error`、Anthropic `error` 和 Chat SSE error，避免把错误对象当成功完成。

证据：

- [handler.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/handler.go)
- [protocol.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/protocol.go)
- [upstream.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/upstream.go)
- [state.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/state.go)

### 3.6 认证、密钥与存储

- 管理面使用 Cookie JWT，HS256。源码中 secret 从用户名与密码派生；用户改密会使旧 token 不再有效。
- 管理 Cookie 源码设置 `Secure=false, HttpOnly=false`，`SameSite` 也不足以在源码层证明强隔离。生产部署应使用 TLS 反代、禁止不受信脚本、限制管理面暴露范围，并关注 CSRF/XSS。
- 上游 Key 与下游 API Key 在数据库模型中按字符串字段保存；固定源码中未找到 Octopus 自带的字段级加密封装。备份导出凭证后必须视为高敏数据。
- 下游认证中间件接受 `x-api-key` 或 `Authorization: Bearer`。
- API Key 有启用、过期、最大费用、允许模型/分组等限制。
- SQLite/MySQL/PostgreSQL 均存在，迁移记录可防止重复执行。默认 SQLite 数据在 `data/data.db`，配置在 `data/config.json`。

关键源码：

- [管理 JWT](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/server/auth/auth.go)
- [鉴权中间件](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/server/middleware/auth.go)
- [API Key 模型](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/apikey.go)
- [迁移器](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/db/migrate/migrate.go)

### 3.7 费用、配额、缓存与会话

价格来自 models.dev，过滤到可计费的自研模型系列与 text output；用户自定义价格优先。费用按 input、output、cache read、cache write 计算。cache 费用并入 input cost，UI 另展示 cache token 数和缓存率。价格不参与低成本路由。

统计层级包括总量、日、小时、渠道、渠道模型、渠道 Key、下游 API Key。默认约每 10 分钟批量落库，正常退出 flush。若进程被强杀，最近窗口的统计可能丢失。

API Key 的最大费用在请求开始前检查，统计异步更新；这意味着并发或同一时刻多个请求可能超过阈值“一个统计窗口”，不能把它当成严格事务式钱包。

这里的“缓存”主要是上游 usage 中的 prompt cache token；没有找到 Octopus 维护跨请求会话、粘住 prompt cache 节点或 WebSocket execution session 的实现。路由中的 affinity 只表示故障转移后暂时使用当前 GroupItem。

### 3.8 数据备份与同步

JSON dump version 5 包含渠道、Key、模型、Grant、分组、价格、API Key、设置与统计。导入按实体混合使用 do-nothing 与 upsert：已有对象可能更新，新对象可能创建，导入不是先清库再恢复，也不是数据库级快照。

这带来两个运维后果：

- 同名对象、旧 ID、已删除对象和分组引用需要预检，否则“恢复”可能变成合并。
- 备份中包含凭证，传输与保管都应按密钥材料处理。

固定主线没有发现 WebDAV 定时备份。PivotFlow 的 WebDAV 自动同步是其自有能力，不能因为两者都叫“AI 网关”就把 Octopus 当作该功能的来源。

### 3.9 部署与升级

- CLI：`octopus start`，默认监听 `0.0.0.0:8080`。
- 默认账户：`admin/admin`。必须首日改密；可用 UI 或 `octopus passwd`，CLI 改密后需要重启。
- Docker：镜像 `bestrui/octopus`，挂载 `/app/data`；entrypoint 默认 `PUID=0`、`PGID=0`。硬编码 root 不利于最小权限部署。
- 数据升级前应同时备份数据库与数据卷；v0.13.0 的 release 曾专门提醒数据库迁移风险。
- 应用内自动更新下载 GitHub latest stable zip，解压并替换当前可执行文件，然后重启。固定源码未显示下载物与 release asset digest、项目签名或透明日志强绑定，不能把它当作已具备完整供应链验证。
- Release workflow 在 `main.go` push 时触发，创建并推送 tag 后再构建发布；发布 job 没有可见的测试阶段。`prerelease:false` 硬编码，Docker 同时推送 GHCR、Docker Hub 与 `latest`。

证据：

- [start.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/cmd/start.go)
- [passwd.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/cmd/passwd.go)
- [Docker entrypoint](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/scripts/dockerfile/entrypoint.sh)
- [updater](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/update/core.go)
- [release workflow](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/.github/workflows/release.yaml)

## 四、针对可靠 AI 网关的深度分析

### 4.1 请求选择状态机

固定源码中的 failover 过程可概括为：

```text
客户端模型名
  → 按 Group 名称查找分组
  → API Key 权限/费用/过期检查
  → pickGroupItem：
       manual：只返回 ActiveItemID
       failover：按 Priority 找一个未冷却项
                 冷却到期项只允许一个 probe
                 亲和期内固定当前项
  → 读取 GroupItem 引用 Grant
  → 加载 ChannelModel、Channel、ChannelKey
  → 执行协议请求/转换
  → 成功 recordRouteSuccess
  → 一轮失败累积，达到阈值 recordRouteFailure 并冷却
  → 首个语义输出前继续下一轮/下一成员
  → 已提交后流故障不重放
```

选路实现在 [route.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/route.go)，请求循环在 [handler.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/handler.go)。

### 4.2 做得好的部分

**首个语义输出前提交。** 流式代理最难的是“何时允许 failover”。Octopus 在 synthetic start 时还保留切换空间，在首个实际内容写给客户端后标记 committed。这个边界比简单“HTTP 头已发就禁止切换”更接近 AI 流语义，也比“每个 SSE 事件都可重试”安全。

**单恢复探测。** 冷却到期不等于健康。Octopus 只让一个请求探测已到期成员；成功清冷却，失败重新冷却。它避免恢复瞬间所有流量冲击尚未恢复的供应商。

**切换后的亲和。** failover 成功后默认保持 300 秒，避免下一个请求立刻回到更高 Priority 但不稳定的渠道。它提供短期稳定性，且 manual 不使用该机制。

**实际证据可观察。** 日志与实时状态显示来源 API Key、实际渠道 Key、实际上游模型、协议、逐轮错误、首字、费用、cache 与手动取消入口。这对排障比只记录最终 200 更有价值。

**协议终止事件校验。** 把 Responses 的 failed/incomplete/cancelled/error、Anthropic error、Chat SSE error 纳入失败语义，避免“网关返回 200、客户端却拿到错误对象”的质量幻觉。

### 4.3 明确缺口与风险

**manual 会持续重试。** manual 模式失败不冷却、`recordRouteFailure` 直接返回 false；主循环按 3 秒间隔继续，直到客户端取消或某次成功。没有最大失败请求数。开放 issue [#374](https://github.com/bestruirui/octopus/issues/374) 正好涉及重试次数限制。对成本型上游，这可能放大失败请求与上游账单。

**全冷却时只等待。** `pickGroupItem` 找不到未冷却成员时返回零值；handler 等待 `member_retry_interval_seconds` 后再次选路。它不会像 PivotFlow 那样选择“最早恢复”渠道继续尝试。开放 issue [#405](https://github.com/bestruirui/octopus/issues/405) 报告带 failover 的负载均衡/单渠道冷却卡住问题，需把 issue 视为公开故障报告而非经本报告复现的结论。

**缺少细粒度失败分类。** 处理循环对多数未提交错误走“重试/切换”，但没有证据显示其对 401/403、429、模型 404、DNS、连接拒绝、SSE 中断、首字节超时等分别采用不同冷却层级与退避策略。统一 60 秒冷却和固定 3 秒间隔未必匹配不同供应商的恢复曲线。

**状态仅进程内。** cooldown、probe、current item 和 affinity 在 `routes` map 中。重启立即丢失；多副本部署时各进程有独立状态，无法形成一致冷却。固定源码没有 Redis、数据库锁或分布式选主证据。

**没有严格预算事务。** API Key 最大费用在请求前检查，统计批量异步落库。高并发时的预算判断存在时间窗口，不能当作硬钱包；需要预留额或原子 reservation。

**请求详情不是完整审计。** 已结束请求最多保留 50 个，长周期取证依赖统计，且没有证据表明每个请求的完整请求/响应会持久保存。对合规审计、事后追责和故障复现不足。

**“拦截所有上游错误”是宣传性概括。** 提交之后、manual 持续失败、全冷却等待、异步统计等边界都说明网关不可能保证“所有错误均在响应前拦截”。更准确的承诺应是“在可提交边界前按配置重试；提交后只报告流中断”。

### 4.4 综合可靠性判断

对个人、单实例、希望人工控制或简单故障转移的部署，Octopus 的结构足够清楚，关键流式边界有认真处理；其 UI 与本地单机运维体验是主要优势。

对要求多租户公平、严格预算、故障不放大、跨实例一致、全冷却自动降级、细粒度上游差异处理、长审计与供应链可验证的生产 AI 网关，固定版本证据不足。不能仅凭 README、release 数量或 stars 将其视为企业级 HA 网关。

## 五、UI 学习

### 5.1 已人工看到的画面

本次实际查看了固定提交中的：

- [desktop-home.png](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/desktop-home.png)
- [desktop-channel.png](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/desktop-channel.png)
- [desktop-group.png](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/desktop-group.png)
- [desktop-log.png](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/desktop-log.png)
- [mobile-home.png](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/mobile-home.png)

视觉系统：暖灰背景、低饱和绿色光斑、白色大圆角卡、绿色图标、深绿暗色主题、5 级绿色数据系列。标题、工具动作和内容的层级稳定；图表与指标卡使用相同语义颜色，不靠强烈饱和度制造焦点。

导航：桌面左侧垂直悬浮 Dock，移动端底部 Dock，图标与页面一一对应；选中项用暖色底，状态与主色区分。这种设计把六个主模块一直留在视线内，同时让内容占满剩余宽度。

Dashboard：请求量、成本、token 等指标成组，热力图与时间序列连用，供应商/模型双榜支持从“整体趋势”下钻到“谁贡献”。分享功能可隐藏渠道名，说明考虑过数据外发隐私。

渠道：卡片先显示状态、请求数和成本，让用户先做“渠道是否健康、是否值得继续”的判断，再进入编辑。搜索与新增固定于标题区，主操作明显。

分组：成员显示序号、拖拽手柄、上游模型、供应商与状态；顺序即优先级，交互意图直接。问题在于截图仍有已被源码移除的模式。

日志：宽卡片让来源、模型、时间、首字、耗时、token、费用位于一条视觉扫描线；左侧状态与供应商图标帮助快速定位。很适合每天查看请求状况，但小屏必须改为分段/折叠，不能硬压到单行。

移动首页：没有把桌面 Dock 简单塞进顶栏，而是把同一图标系统转成底部悬浮 Dock；卡片仍维持大圆角与足够内边距。视觉统一度较高。

### 5.2 由 UI 源码推断、未做浏览器实测

- 渠道编辑器分 5 步：预设、连接、Key、授权、高级配置。
- Grant 授权是三态批量矩阵，可展开/折叠、清空；Key 有独立开关和探测。
- 分组候选采用渠道 → 模型 → Key 三级选择，并显示协议标签；成员可拖拽、置顶/置底、键盘移动、删除确认。
- 首页指标可切换请求/成本/token，支持 today/7d/30d、双榜、隐藏渠道和 PNG 分享。
- 卡片网格使用 VirtualizedGrid，三档响应列数；页面悬停/触摸可预加载。
- 主题有 light/dark/system；语言是英文、简体中文、繁体中文三套资源。
- CSS 与组件包含 focus-visible、ARIA、键盘激活和删除确认等可访问性投入。

未证实：完整 WCAG 等级、reduced-motion 覆盖、色盲对比、触屏拖拽可用性、屏幕阅读器流程、低端手机动画帧率、所有页面的真实 320px/375px 布局。

### 5.3 UI 最重要的启示

Octopus 把“配置复杂度”拆到不同粒度：

- 渠道编辑负责连接与能力发现。
- Grant 矩阵负责模型、Key、协议的细粒度能力。
- 分组编辑器负责优先级和运营状态。
- 日志负责证明某次请求实际命中了什么。

这比把所有开关堆进一个“渠道高级设置”更容易定位错误。PivotFlow 可以借鉴这种信息架构和视觉节奏；具体组件代码、截图素材和品牌资产受 AGPL 与版权边界约束，不应直接复制。
## 六、公开 issue、修复边界与不能外推的路线图

以下是 2026-11-19 的公开状态快照。Issue 是用户/维护者报告，不等于本报告已复现；关闭 issue 也不自动证明相关代码路径全部无缺陷。

| Issue | 快照状态 | 与本研究的关系 |
|---|---|---|
| [#371](https://github.com/bestruirui/octopus/issues/371) | Open | 请求分组负载均衡；当前 `GroupMode` 没有负载均衡，issue 不能写成已实现 |
| [#374](https://github.com/bestruirui/octopus/issues/374) | Open | 失败请求次数限制；对应 manual 持续重试的明确产品缺口 |
| [#405](https://github.com/bestruirui/octopus/issues/405) | Open | 带 failover 的负载均衡/单渠道冷却卡住；提示全冷却和策略组合需要回归测试 |
| [#409](https://github.com/bestruirui/octopus/issues/409) | Open | 渠道参数覆盖思考等级后，日志仍显示客户端原值；关系“实际生效值证据” |
| [#417](https://github.com/bestruirui/octopus/issues/417) | Open | 希望忠实保留上游响应额外信息；关系网关是否过度归一化 |
| [#420](https://github.com/bestruirui/octopus/issues/420) | Open | 嵌套分组；当前对象模型没有嵌套 Group |
| [#432](https://github.com/bestruirui/octopus/issues/432) | Open | 更多视频格式支持；说明视频不是当前已确认能力 |
| [#398](https://github.com/bestruirui/octopus/issues/398) | Closed | 与流式 `context canceled` 误报相关，v0.13.6 明确写出关闭 |
| [#426](https://github.com/bestruirui/octopus/issues/426) | Closed | v0.13.13 安全依赖升级关闭 |

研究边界：

- roadmap、issue 和 README 计划不能作为“已实现”证据。
- v0.13.6 修复 #398 证明该具体问题被处理；不等于所有 `context canceled` 分类都消失。
- v0.13.13 关闭 #426 证明运行/依赖被升级；不等于 Octopus 通过了完整安全审计。
- issue #405 所指的“负载均衡”可能来自用户期望、旧版本或 fork，当前主线源码仍只有 manual/failover。
- GitHub `open_issues_count` 可能包含 PR，不应当把它当成纯 issue 数做质量结论。

## 七、许可证、素材与直接复用边界

### 7.1 软件许可证

Octopus 固定提交和 GitHub API 均显示 AGPL-3.0。AGPL 对通过网络向用户提供修改版软件的网络交互有源代码提供要求。PivotFlow 自身使用 MIT 许可证；把 Octopus 的 Go/TypeScript 代码、实质性移植甚至紧密翻译复制进 PivotFlow，会引入许可证兼容与披露义务讨论，需要由项目所有者做明确许可决策和法律审阅。

本报告不提供法律意见。工程上应把 Octopus 当作：

- 可观察的产品行为和交互案例；
- 可从公开描述独立实现的设计思想来源；
- 需要署名、版本和许可证边界记录的外部研究材料。

不应把 AGPL 组件作为“只是参考一下”直接复制进 MIT 产品，更不应删除版权头。

### 7.2 截图、logo 与视觉资产

README 中的 UI PNG、Octopus logo、供应商图标及示例文案属于仓库/第三方素材。PivotFlow 可以学习层级、响应式、工作流和指标组织，但不应把 PNG、SVG、品牌图标、品牌名称、逐像素视觉稿直接搬入产品。

### 7.3 数据与安全边界

公开截图含虚构/或已处理的数据；不能把截图里的供应商名、金额、token 与性能数字当作基准数据。备份与运行时数据库含凭证，研究过程没有读取或复制真实部署数据。

## 八、PivotFlow 可借鉴的具体做法

以下建议以 PivotFlow 当前 README/CLAUDE 的真实能力为参照。PivotFlow 已有多 Key/多 URL、优先级、协议转换、三级冷却、分类 failover、全冷却最早恢复、规则化探测、平滑加权轮询、可选健康评分、WebSocket execution session、费用/并发/每日限额、加密凭证、SQL 与 WebDAV 备份，因此不会把这些误列为缺口。

### 8.1 增加“模型 × Key × 协议”三态能力矩阵

- **问题**：当协议能力散落在渠道默认值、URL 声明、Key 健康与模型清单里，排障时很难回答“这个模型能否用这个 Key、通过这个协议打到这个上游”。
- **Octopus 实现**：Grant 是模型 × Key × 协议位掩码的最小路由单位；前端矩阵支持三态批量、展开/折叠与清空。[channel.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/channel.go)、[FormGrants.tsx](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/src/components/modules/channel/FormGrants.tsx)
- **PivotFlow 已有/部分/缺口**：已有 `ChannelURL.Protocols` 权威声明和多协议自动探测；Key 健康也存在。公开 CLAUDE 未描述一个面向操作者的模型 × Key × 协议三态矩阵，属于产品化展示缺口。
- **建议方式**：保留现有后端协议和选路，不改数据面；增加只读能力快照与批量编辑适配层，矩阵每个格子解释来源（静态声明、探测成功、冷却中、最近失败）。
- **风险/成本**：中高。需要处理配置漂移、批量误写、超大矩阵虚拟化和权限控制；先做可观测视图，后做批量写入。
- **结论**：最值得借鉴，优先级高。

### 8.2 把 Key 探测升级为能力发现，并保留人工覆盖

- **问题**：只有“Key 是否活着”还不够，运营商需要知道该 Key 对哪些模型、哪些协议、哪些参数等级有效。
- **Octopus 实现**：Key 可独立探测，Grant 矩阵把探测/选择结果落到模型与协议组合。[FormKeys.tsx](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/src/components/modules/channel/FormKeys.tsx)
- **PivotFlow 已有/部分/缺口**：已有渠道定时检测、Key 失败冷却和协议能力探测；缺口主要是把探测结果汇总成可审阅的模型/协议能力差量。
- **建议方式**：探测只提供候选，默认不自动删除人工声明；展示“人工声明 vs 探测结果 vs 最近实际成功”，并用明确按钮接受差量。
- **风险/成本**：中。探测会增加延迟和少量成本，可能误判临时 429/权益变化；必须限并发、区分认证失败与能力不支持、允许回滚。
- **结论**：优先中高。

### 8.3 分组候选采用“渠道 → 模型 → Key”三级选择

- **问题**：成员列表若只显示最终模型名，运营商无法快速知道同一模型来自哪个渠道、哪个 Key，以及协议能力。
- **Octopus 实现**：候选按三级组织，显示协议标签，并能按分组名自动匹配候选。[ItemList.tsx](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/src/components/modules/group/ItemList.tsx)
- **PivotFlow 已有/部分/缺口**：已有渠道、模型映射、Key 与多 URL 数据，但 README/CLAUDE 未显示同等三级候选交互。
- **建议方式**：新增成员选择抽屉，先展示自动建议，再按三级树筛选；来源标签显示 Key 掩码、协议与冷却状态；允许一键添加但必须在保存前预览最终优先级。
- **风险/成本**：中。自动匹配在模型别名或统一映射下容易误选；建议结果必须可解释并默认仅作候选。
- **结论**：高价值、低数据面风险。

### 8.4 将成员优先级做成可拖拽、可键盘、可撤销的编辑体验

- **问题**：优先级是故障转移的核心配置，纯数字输入容易误排且缺少当前运行态反馈。
- **Octopus 实现**：成员可拖拽、置顶/置底、键盘移动、删除二次确认，并显示预热/冷却/亲和倒计时。[Editor.tsx](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/src/components/modules/group/Editor.tsx)、[MemberStatus.tsx](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/src/components/modules/group/MemberStatus.tsx)
- **PivotFlow 已有/部分/缺口**：CLAUDE 显示 Priority、权重、冷却与健康状态都在后端；缺口是是否已有同等键盘可操作且带实时倒计时的编辑体验，公开文档不足以确认。
- **建议方式**：拖拽只改变草稿，保存时一次提交顺序；提供移动按钮与快捷键；离开未保存页面时提示；运行态用只读徽标呈现。
- **风险/成本**：中。错序会影响故障切换与成本；需要前端撤销、并发版本检查和后端最终校验。
- **结论**：体验收益高。

### 8.5 对“冷却恢复”使用单飞行探测与成功防抖

- **问题**：冷却到期瞬间若所有请求一起打向未恢复上游，会形成恢复风暴；一次偶发成功也不应立刻放入全量流量。
- **Octopus 实现**：同一分组只能有一个 ProbeItemID；成功清冷却并开始亲和，失败重新冷却。[route.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/route.go)
- **PivotFlow 已有/部分/缺口**：已有冷却过滤和最早恢复兜底，但公开 CLAUDE 未明确“每个同一资源只放行一个恢复请求”的单飞行语义；属于应验证的潜在缺口。
- **建议方式**：以 `(channel, key, model, url, protocol)` 为键加单飞行 probe；成功的正常请求本身可作为探测，尽量不额外合成付费请求；指标记录 probe wins/losses。
- **风险/成本**：中。需要处理进程重启与多副本锁；单实例可用 mutex，多副本需要共享 lease。
- **结论**：可靠性改进优先级高。

### 8.6 在 failover 成功后短期保持当前成员，并把亲和状态显示出来

- **问题**：备用成员刚恢复服务时，下个请求立刻被更高 Priority 的旧成员抢回，会造成抖动。
- **Octopus 实现**：默认故障切换成功后 300 秒保持当前成员，旧成员失败立即结束亲和。[group.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/group.go)
- **PivotFlow 已有/部分/缺口**：已有 `stickyRouter` 和 `route_strategy=sticky`，但 Octopus 的“只由 failover 触发、有截止时间、UI 倒计时”是清晰的运营界面模式。
- **建议方式**：保留 PivotFlow sticky 路由；在系统设置与管理 UI 明确展示当前 sticky 原因、剩余时间、解除入口，并纳入 runtime metrics。不要在轮询模式中混用这一语义。
- **风险/成本**：低到中。主要风险是亲和掩盖恢复或违背成本/健康目标，需要按策略启用并记录原因。
- **结论**：逻辑已有，重点借鉴可观察性。

### 8.7 把首个语义提交边界做成请求时间线

- **问题**：客户端只看到超时或断流时，运营者很难判断是在上游首事件前失败、已输出部分后断流，还是客户端主动取消。
- **Octopus 实现**：synthetic start 暂存，首个含语义输出事件后才 `markCommitted`；日志与状态记录轮次和手动取消。[handler.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/handler.go)、[state.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/state.go)
- **PivotFlow 已有/部分/缺口**：PivotFlow 的流式与 WS 已有“首个语义输出后不切换”原则，后端更强；缺口在把该边界作为清晰 UI 时间线展示。
- **建议方式**：请求详情画 attempts 时间线：请求到达、首个上游头、首个暂存事件、首次下游写、错误分类、冷却效果、结束原因；不要记录敏感正文。
- **风险/成本**：中。日志量与隐私是主要风险，需按采样、脱敏和保留期控制。
- **结论**：借用呈现，不重写已有数据面。

### 8.8 建立逐轮证据账本，显示“实际生效”而非客户端原值

- **问题**：参数覆盖、模型重定向和协议转换后，若日志只显示客户端请求值，会误导排障。
- **Octopus 实现**：日志卡片显示实际渠道 Key、实际模型、协议、逐轮错误、首字、输入/输出/cache/费用；但开放 #409 仍说明参数覆盖后的思考等级显示存在未闭环问题。[日志 UI](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/src/components/modules/log/Item.tsx)、[#409](https://github.com/bestruirui/octopus/issues/409)
- **PivotFlow 已有/部分/缺口**：CLAUDE 已要求实际上游模型进入冷却键，且成本/等级有后端语义；缺口是逐字段“请求值 → 映射/覆盖值 → 上游值”的可视化证据链。
- **建议方式**：每轮 attempt 记录结构化摘要：channel/key/url/model/protocol/转换策略、被覆盖字段、状态、首字、流终止、费用增量；正文和凭证永不入日志。
- **风险/成本**：中。需统一 attempt schema、控制高基数和大日志；字段透传要避免泄密。
- **结论**：最高业务价值之一。

### 8.9 用虚拟化卡片网格承载渠道/模型库存

- **问题**：渠道与模型可能达到数百项，普通卡片网格一次性挂载会产生滚动卡顿和内存压力。
- **Octopus 实现**：VirtualizedGrid 与 1/2/3 列响应布局，并在页面悬停/触摸预加载。[VirtualizedGrid.tsx](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/src/components/common/VirtualizedGrid.tsx)、[app-shell.tsx](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/src/components/app-shell.tsx)
- **PivotFlow 已有/部分/缺口**：后端具备大规模渠道/模型库存能力；公开文档未证明前端已有同等虚拟化和页面预加载策略。
- **建议方式**：只对列表级页面启用；保留搜索、筛选、批量选择与可访问焦点；用真实 500/2000 项数据做内存与滚动基准，不要为了动画牺牲输入响应。
- **风险/成本**：中。拖拽、表格选择、屏幕阅读器与虚拟化组合复杂；先做基准和可观测性能预算。
- **结论**：适用于 PivotFlow 的运营控制台。

### 8.10 保持桌面侧 Dock 与移动底部 Dock 的同一信息架构

- **问题**：桌面侧栏在移动端若直接折叠成汉堡菜单，核心页面切换会多一层。
- **Octopus 实现**：桌面和移动都使用同图标集，分别放入左侧与底部悬浮 Dock；首页/渠道/分组/模型/日志/设置稳定存在。[app-shell.tsx](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/src/components/app-shell.tsx)
- **PivotFlow 已有/部分/缺口**：控制台功能更多，是否已有同等移动导航未在本次审查中证实；站点/账号等新增模块需要防止底栏过载。
- **建议方式**：将底部 Dock 限制为 4–5 个高频目的地，其余进入“更多”；桌面默认侧栏保持全部模块。避免图标无标签导致认知差异。
- **风险/成本**：低到中。主要风险是模块数量与可访问性，不是后端。
- **结论**：可小步试点。

### 8.11 Dashboard 使用“指标 + 时间范围 + 双榜 + 隐私分享”的四层信息结构

- **问题**：单一请求量图表无法同时回答消耗、费用、谁贡献和变化窗口。
- **Octopus 实现**：请求/成本/token 指标切换，today/7d/30d，渠道与模型双榜，隐藏渠道名后导出 PNG。[首页](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/src/components/modules/home/index.tsx)、[排名](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/src/components/modules/home/rank.tsx)
- **PivotFlow 已有/部分/缺口**：README/CLAUDE 已有请求、成本、健康时间线、限流错误等数据；公开文档未显示同等一键隐私分享。
- **建议方式**：用统一直方图/时间序列查询服务；分页排名；分享前脱敏站点、账号、Key 与金额阈值，并显示预览。
- **风险/成本**：中。隐私与聚合一致性最重要；不要在前端临时重算不同口径。
- **结论**：适合 PivotFlow 的产品化增强。

### 8.12 备份导入前增加差异预检，并把“合并”与“恢复”分成两种动作

- **问题**：Octopus 的 dump/import 混合 upsert，用户很难预期“导入”究竟覆盖什么；凭证又一起导出。
- **Octopus 实现**：dump v5 含配置、统计与凭证，但导入不是清空重建。[backup.go](https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/op/backup.go)
- **PivotFlow 已有/部分/缺口**：已有 JSON 导入导出、WebDAV 定时备份、加密凭证与迁移文档；公开 CLAUDE 未证明已有完整的导入差异预览。应以 Octopus 为反例补强。
- **建议方式**：上传后先显示将新增/更新/跳过/冲突、渠道/Key 引用变化、凭证覆盖、数据库 schema 兼容性；明确“合并导入”和“清空恢复”两个危险等级不同的按钮。
- **风险/成本**：高。需要稳定 identity 规则和事务回滚；绝不能让预检和实际写入使用不同逻辑。
- **结论**：重要且应优先做安全设计。

## 九、PivotFlow 应明确保留的差异

1. **保留细粒度错误分类。** Octopus 的基础重试不应替换 PivotFlow 对 Key/模型/渠道/URL 的逐层决策。
2. **保留全冷却最早恢复兜底。** Octopus 返回零值并等待，造成可用性下降；PivotFlow 的兜底更符合刚性网关。
3. **保留持久化模型冷却。** Octopus 的路由状态重启即丢失；PivotFlow 已有 `channel_model_cooldowns`，不应为了简化而退回到内存态。
4. **保留平滑加权、健康评分与成本限额。** Octopus 当前缺少这些现成能力，PivotFlow 不应因借鉴 UI 而弱化选路。
5. **保留 WebSocket execution session。** Octopus 当前明确不支持 Responses WebSocket，PivotFlow 的会话身份、transcript、连接复用和清理都是独立且更复杂的能力。
6. **保留加密凭证和 WebDAV。** Octopus 的字符串 Key 存储、含凭证 JSON 与无 WebDAV 不应被当成参考实现。
7. **保留经典稳定的安全头与构建验证。** Octopus 管理 Cookie `HttpOnly=false`、源码默认 admin/admin 与 release job 无测试，均不能成为 PivotFlow 的模板。

## 十、单项目视角的建议排序

以下排序只表达 Octopus 研究中呈现的风险和收益。PivotFlow 的最终改进顺序已结合自身代码缺口与另外两个项目重新整合，见 [总体建议](recommendations.md)；不能把对 Octopus 的默认账户、Cookie 等问题直接认定为 PivotFlow 同样存在。

| 优先级 | 改动 | 原因 | 建议阶段 |
|---|---|---|---|
| P0 | 备份导入差异预检与合并/恢复分离 | 直接关系数据丢失、凭证覆盖与可恢复性 | 下一轮安全/运维设计 |
| P0 | 管理面安全头、Cookie、默认账户和更新产物校验审计 | 外部实现证据显示这类边界容易被低估 | 安全基线 |
| P1 | 模型 × Key × 协议能力视图 | 最能降低运营商的理解和排障成本 | 只读视图先行 |
| P1 | 逐 attempt 证据账本与首语义提交时间线 | PivotFlow 数据面已有基础，主要做观测产品化 | 先落 schema，再落 UI |
| P1 | 单飞行恢复探测的代码审计/补强 | 防止恢复风暴，可量化 | 加 metrics 和并发测试 |
| P2 | 三级候选、成员编辑、Dashboard 隐私分享 | 高交互价值但依赖前置信息架构 | 前端体验迭代 |
| P2 | 虚拟化网格和移动 Dock | 规模与移动体验优化 | 基准测试后实施 |
| P3 | 直接复制 Octopus 代码或截图 | 与 AGPL、品牌和视觉原创边界冲突 | 不建议 |

## 十一、来源、证据等级与局限

### 11.1 证据等级

本报告优先使用以下顺序：

1. `8aa58b0` 固定提交中的源码。
2. `v0.13.13` 与历史 tag 的 GitHub release，以及 GitHub API 的精确 SHA/时间。
3. 仓库固定提交中的真实 UI 源码和 README 截图。
4. 公开 issue，仅作为外部报告与边界提示。
5. README 文案与旧 pack，只用于对照，不能压过源码。

主要固定源码入口见 [源码索引](sources/octopus-source-index.md)。

### 11.2 本次实际做过的核查

- 用 GitHub API 读取 repo、HEAD commit、latest release 与 releases 列表。
- 用只读 blob-filtered 浅克隆在固定 HEAD 查模型、路由、流式、认证、价格、统计、迁移、更新与 UI 源码。
- 读取固定提交中的 5 张真实截图并人工评估；其余 UI 结论均明确标为“源码推断”。
- 读取本地旧 zip 的 manifest 和指定身份/依赖文件，没有整包解压。
- 对照 PivotFlow README 与 CLAUDE 中的已有路由、冷却、WebSocket、预算、存储、备份与加密事实。

### 11.3 未证实事项

- 没有启动 Octopus 服务，没有做真实上游压测、SSE 断流注入、并发余额测试或多实例测试。
- 没有验证所有 GitLab/GitHub CDN、Docker Hub、GHCR 资产在 2026-11-19 的实时可用性。
- 没有做渗透测试、TLS 配置实测、JWT 伪造测试、XSS/CSRF 验证或供应链复现构建。
- 没有证明所有上游供应商的“Key 探测”语义一致；不同供应商可能有不同模型列表与协议能力。
- 没有确认截图中的虚构数据完全无敏感信息；报告未复用其中的数据作为性能基线。
- 没有把开放 issue 的复现步骤全部重跑；#374、#405、#409、#417、#420、#432 仍应按“公开报告”阅读。
- 仓库 stars/forks 是 2026-11-19 API 快照，不是稳定事实；`open_issues_count` 可能包含 PR。
- 未发现公开 prerelease 不等于未来不会出现 prerelease；结论只覆盖本次 API 返回。
- “未找到”表示固定提交和本次检索范围内未找到，不等于数学上保证所有历史分支都没有该功能。

### 11.4 旧参考 ZIP 的处置

原 ZIP 已逐字节复制到工作区 `archive/reference-snapshots/octopus-dev.zip`，校验 SHA-256 与 CRC 后删除根目录重复副本。身份、大小与 Go 版本记录保留。固定 HEAD 的 8 份关键源码和 5 张截图另存到本报告 sources，逐项哈希见 [octopus-captured-files.json](sources/octopus-captured-files.json)；本轮只读浅克隆已清除。完整清理事实见 [清理报告](workspace-cleanup.md)。

## 十二、最终结论

Octopus 在 2026-10 的最新 stable 是 `v0.13.13`，源代码固定在 `master@8aa58b0`。它已经形成清晰的“渠道能力 → Grant 授权 → Group 顺序路由”模型，UI 把授权、优先级和请求证据做得比许多只提供 YAML 的网关更容易操作。最近 90 天的演进也集中在多 Key、统计、日志、流式健壮性和安全依赖升级。

但它当前的主线不是负载均衡平台：没有轮询、随机、权重、低成本优先、健康评分、嵌套组、Gemini、Images 或 WebSocket；manual 重试和全冷却等待还存在直接可靠性风险，路由状态也不跨重启/实例共享。把这些限制与 README、旧截图、Hureru dev fork 或 roadmap 混在一起，会得到错误的“功能已经很多”的印象。

对 PivotFlow 最有价值的借鉴顺序应是：先补安全与备份恢复边界，再把 Octopus 的能力矩阵、三级候选、逐轮证据、恢复探针可视化和移动端的清晰层级映射到 PivotFlow 已有数据面；保留 PivotFlow 已有的分类冷却、全冷却兜底、健康/权重/预算与 WebSocket 能力，不把 AGPL 代码或素材直接复制进来。