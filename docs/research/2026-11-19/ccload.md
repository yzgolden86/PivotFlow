# ccLoad 最新状态、可靠性与 UI 调研（截至 2026-11-19）

> 范围：`caidaoli/ccLoad` 官方仓库、GitHub Release/Tag/Issue 原文、当日 `master` 源码、仓库当前公开截图，以及本地旧参考快照 ccLoad 4.10.2。本文把“实际运行可见”“官方发布声明”“源码可证”“源码推断”和“未能实测”分开写。PivotFlow 状态依据仓库根目录 `README.md` 与 `CLAUDE.md`，只作能力映射，不替主代理排期。

## 1. 结论先行

- 默认分支是 `master`。截至本轮 GitHub API 抓取时，HEAD 为 `eebe29fe769332a6c1d822e4fd14a7b701348d24`，提交时间是 **2026-11-19 09:00:56 UTC**，标题为 `fix(oauth): stop stale passive Anthropic windows from reappearing after refresh`。来源：[仓库 API](https://api.github.com/repos/caidaoli/ccLoad)、[HEAD commit](https://github.com/caidaoli/ccLoad/commit/eebe29fe769332a6c1d822e4fd14a7b701348d24)。
- GitHub 的 latest stable 是 **v5.0.1**，发布于 **2026-11-18 01:13:50 UTC**，对应 commit `a6ccc5bb5ce11998578b597d5a370b46e3e16bd9`。来源：[v5.0.1 release](https://github.com/caidaoli/ccLoad/releases/tag/v5.0.1)。
- 最新 prerelease 是 **v5.0.3-beta.2**，发布于 **2026-11-19 09:05:04 UTC**，target 写的是 `master`，标签实际指向 HEAD `eebe29fe`。来源：[v5.0.3-beta.2 release](https://github.com/caidaoli/ccLoad/releases/tag/v5.0.3-beta.2)。
- 因此“latest release”不是 `master` 的泛称：stable 以 `v5.0.1` 的固定 SHA 为准；`master` 已在其后继续前进，包含 v5.0.2/v5.0.3 的预发布修复。若讲“当前代码”，应说 `master@eebe29fe`；若讲“生产稳定版”，应说 `v5.0.1@a6ccc5b`。
- 从本地 4.10.2 到 HEAD 的主要变化不是单纯增加渠道数量，而是继续围绕长连接、协议转换、带副作用重试、按作用域冷却、缓存粘性、费用周期、OAuth 配额、批量运维和安全边界加固。当前实现值得重点借鉴的是：**HTTP 跨域重定向拒绝、按会话成功绑定的缓存粘性、完整 transport generation 轮换、物理 WS 连接额度与逻辑 turn 分离、费用限额的明确超额窗口**。
- 仓库当前公开的 dashboard/logs 图片内容实际显示 `v4.10.33-beta.1-7-g67c6d964`、日期 `10/06`；它们是“当前仓库里可取得的官方截图”，但不是 5.0.x 运行界面证据。5.0.x 的导航、抽屉、批量栏和调试面板只能依据 HEAD 的 HTML/JS/CSS 描述，本文均标为“源码推断”。

## 2. 版本时间表与 90 天窗口

本地快照是 4.10.2；本轮重点比较 **2026-09-05 到 2026-11-19 约 75 天** 的变化，跨过 v5.0 发布。时间表按阶段压缩，完整发布记录另保存在 sources 中。

| 时间（UTC） | 版本 | 类型 / SHA | 本阶段值得注意的变化 |
|---|---|---|---|
| 2026-09-05 11:19:55 | [v4.10.2](https://github.com/caidaoli/ccLoad/releases/tag/v4.10.2) | stable，`353beb4a970a021ca5184327a4c6ecc5fecd8a05` | 旧参考基线；发布说明核心是导出渠道时只导出选中的模型。 |
| 2026-10-01 - 10-06 | v4.10.30-beta.3 至 v4.10.34-beta.1 | prerelease | 协议核心同步、缓存 TTL 统计、Antigravity/Anthropic 修正；渠道列表与编辑器抽屉重构、总览页和服务健康条完善。公开截图即此阶段。 |
| 2026-10-07 12:26:59 | [v4.11.0](https://github.com/caidaoli/ccLoad/releases/tag/v4.11.0) | stable，`95119e12e2adf1ceba1e6a6e71ea4d3ff21fe297` | 定时检测改为按渠道间隔/起始时间；OAuth 检测使用真实凭证；保留 OAuth 刷新时手工模型；OAuth 配额进度与累计费用持久化。 |
| 2026-10-08 00:22:20 | [v4.11.1](https://github.com/caidaoli/ccLoad/releases/tag/v4.11.1) | stable，`2edcf7d2fb2b436c04d64c93bdea796f24022ad3` | Haiku 5.5 定价和模型发现修正。 |
| 2026-10-08 04:03:06 | [v4.12.0](https://github.com/caidaoli/ccLoad/releases/tag/v4.12.0) | stable，`6ad637bdce06d00dad1a03258152227884960a03` | OAuth 上游额度透传开关、token 页面重设、图像测试结果持久化、成本倍率快照。 |
| 2026-10-18 12:47:59 | [v4.13.0](https://github.com/caidaoli/ccLoad/releases/tag/v4.13.0) | stable，`e45ec7180c8b856bda5035ae88c9d772761fa4d0` | Responses WS 支持 interrupt；同渠道重试；大范围统计优化；健康时间线锚定当前时刻。 |
| 2026-10-19 08:39:13 | [v4.13.1](https://github.com/caidaoli/ccLoad/releases/tag/v4.13.1) | stable，`5ad888f508cd2d845dafc80af0a1fa6b38589870` | 429 按分钟退避；转换流干净 EOF 判失败；缓存命中视觉分档调整。 |
| 2026-11-18 00:36:04 | [v5.0.0](https://github.com/caidaoli/ccLoad/releases/tag/v5.0.0) | stable，`e2a3e0e0c92ffa947110a0f9717499751bd050c2` | 大版本：Qoder/Accio、OAuth 与签到、隐私脱敏、动态 reasoning、Responses 上下文压缩、流截断、Vertex 多区域、登录/安全与协议核心同步等。 |
| 2026-11-18 01:13:50 | [v5.0.1](https://github.com/caidaoli/ccLoad/releases/tag/v5.0.1) | latest stable，`a6ccc5bb5ce11998578b597d5a370b46e3e16bd9` | 仅在前版基础上修正 Codex OAuth 渠道测试的服务端客户端身份；当前 latest stable 定格在此 SHA。 |
| 2026-11-18 01:58 - 15:50 | v5.0.2-beta.1 至 beta.3 | 预发布 | models.dev reasoning 目录回退；隐私遮罩补洞；Codex/Claude 客户端基线更新；修复 hybrid bootstrap 丢失 Codex refresh history、禁用项仍出现在 token 选择器。 |
| 2026-11-19 03:11:21 | [v5.0.3-beta.1](https://github.com/caidaoli/ccLoad/releases/tag/v5.0.3-beta.1) | prerelease，`889f4eeaf9cbc1557d3b07d5bcf1f62449c9b768` | Gemini Neon 模型；跨协议保留 tool schema。 |
| 2026-11-19 09:05:04 | [v5.0.3-beta.2](https://github.com/caidaoli/ccLoad/releases/tag/v5.0.3-beta.2) | 最新 prerelease，`eebe29fe769332a6c1d822e4fd14a7b701348d24` | 修复 Anthropic 配额刷新后旧的 passive quota windows 重新出现；对应当前 HEAD。 |

发布 API 原文保存在 `sources/ccload-releases.json`，标签与 SHA 保存在 `sources/ccload-tags.json`。注意：API 的 release target 字段都写 `master`，但标签对象本身已经 dereference 到固定 SHA；判断可复现版本必须使用 SHA，不能只看 target 名称。

## 3. 功能矩阵：4.10.2、stable 与 HEAD 的差别

“已支持”表示旧快照或当前源码可以直接证明；“增强/部分”表示机制已存在但近期修过关键边界；“未见”只表示本轮未取得证据，不等同于永久不存在。

| 能力 | 4.10.2 旧快照 | v5.0.1 stable | 当前 `master@eebe29fe` | 证据 |
|---|---|---|---|---|
| 多渠道、多 Key、多 URL、优先级/轮询 | 已支持 | 已支持 | 增加更多按 Key 模型范围、Key 优先级的批量编辑 | [README](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L231)、[channels.html](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/channels.html) |
| Anthropic / OpenAI Chat / Responses / Gemini 四协议转换 | 已支持 | 已支持 | Responses 修复、跨协议 tool schema、图片、JSON schema、reasoning 持续增强 | [README](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L244)、[CLAUDE 旧快照说明](https://github.com/caidaoli/ccLoad/blob/353beb4a970a021ca5184327a4c6ecc5fecd8a05/CLAUDE.md) |
| Codex Responses WebSocket | 已支持 | 增强 | interrupt、上下文压缩、流截断、物理连接限额与会话恢复继续加固 | [WS limiter](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/responses_websocket_connection_limiter.go)、[responses_retry.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/responses_retry.go) |
| retry / failover | 首字节前切换 | 增强 | HTTP 200 内 SSE error、流截断、错误作用域、跨协议和 WS 边界继续细分 | [manager.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/cooldown/manager.go) |
| Key/模型/渠道/URL 冷却 | 已支持 | 已支持 | 更完整 reset metadata、全冷却兜底、冷却规则测试与作用域收窄 | [selector_cooldown.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/selector_cooldown.go) |
| prompt cache / 会话粘性 | 本轮未把其列为 4.10.2 主能力 | 已支持 | Claude Session/Agent 与 Codex Thread 粘性；guardian 继承父线程；成功后才绑定，TTL 1 小时 | [session_affinity.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/session_affinity.go) |
| 全局/渠道/Token 并发、RPM、每日费用 | 部分 | 已支持 | Token 增加总/日/月费用与 RPM；费用限额仍有明确 check-then-act 超额窗口 | [proxy_handler.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/proxy_handler.go)、[tokens.html](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/tokens.html) |
| OAuth 账号与额外提供商 | Codex/Anthropic/Antigravity/Cursor 等 | 已支持 | Qoder、Accio、CodeBuddy、xAI、Loomy、Z.ai、Vertex 等；批量凭证导入与失效清理更完整 | [README 渠道说明](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L116) |
| OAuth 配额窗口与标准成本 | 部分 | 已支持 | 周/月窗口、reset boundary、ledger、refresh 后旧窗口清理 | [v5.0.3-beta.2](https://github.com/caidaoli/ccLoad/releases/tag/v5.0.3-beta.2)、[README](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L258) |
| 隐私脱敏/还原 | 旧快照未作为主能力 | 已支持 | 请求向上游遮罩、回复还原；调试界面显示占位符统计；按调用 token 隔离映射 | [README](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L262)、[logs.js](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/assets/js/logs.js) |
| 动态 reasoning | 未见 | 已支持 | 决策渠道/模型、按模型目录校准、客户端显式 effort 优先 | [v5.0.2-beta.1](https://github.com/caidaoli/ccLoad/releases/tag/v5.0.2-beta.1)、[README](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L251) |
| API token 登录只读控制台 | 未见 | 已支持 | 默认关闭；可隐藏渠道，只看自身用量 | [README](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L261) |
| 批量渠道操作 | 部分 | 已支持 | 批量启停、刷新额度、优先级/倍率/RPM/并发/日限额/协议；凭证清理/刷新有进度与失败详情 | [channels.html 批量栏](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/channels.html#L892) |
| CSV / 凭证导入导出 | 已支持 | 已支持 | CSV 含完整 key/OAuth credential；导入先校验 OAuth，再批量事务写入；有进度和逐行错误 | [admin_csv.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/admin_csv.go) |
| 请求活动、终止、原始/翻译后 debug | 部分 | 已支持 | 活动请求可中断；显示首包/耗时/cache/cost；debug 可看原文、转换后请求/响应和隐私占位符 | [logs.html](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/logs.html)、[logs.js](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/assets/js/logs.js) |
| 稳定/预览更新 | 有版本更新基础 | stable/preview | 二进制发布 SHA-256；容器按 tag；覆盖前验证，随后替换和重启 | [auto_update.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/auto_update.go)、[release.yml](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/.github/workflows/release.yml) |
| 主库 + SQLite hybrid | 已有基础 | 已支持 | 启动快照恢复配置、quota ledger 和 Codex refresh journal；日志按尾部增量恢复 | [sync_manager.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/storage/sync_manager.go) |
| 认证安全 | API key/token、admin 登录 | 增强 | Token 哈希落库、bcrypt、登录限速、IPv6 `/64` 合并、24h Web 会话、跨域上游 redirect 拒绝 | [auth_service.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/auth_service.go) |

## 4. 路线、可靠性、缓存与限流的实现阅读

### 4.1 路由和首字节失败边界

ccLoad 的可靠性核心是：候选按优先级、同层轮询/健康选择，失败后按 Key、模型、渠道或 URL 的精确作用域惩罚，并在客户端响应尚未提交时切到下一候选。上游返回的 HTTP 200 不能证明安全；SSE error、结构化的成功响应错误、流干净 EOF、连接重置、超时和空的响应都可能进入失败路径。当前 README 将此列为软错误检测和首字节前 failover：[README](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L233)。

这份实现值得借鉴的地方是“不可重放的边界”写得比较明确。`responses_retry.go` 只修复一组可以确定的 Responses 输入问题，再重试一次；对模糊 400 不直接重放带副作用的 POST。响应提交后，错误会停止透明换渠道，避免客户端收到两段拼接上下文。源码：[responses_retry.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/responses_retry.go)。

### 4.2 冷却作用域

冷却状态可以落在 Key、渠道 + 模型、渠道和客户端请求分类上。5xx 默认收窄到模型；DNS/拒绝连接等基础设施错误更容易是渠道级；连接重置、HTTP/2 流中断、首字节超时等多按模型或 URL 处理。实现提供误差退避、上游精确 reset 时间、全冷却时选“最早恢复”的兜底路径：[cooldown/manager.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/cooldown/manager.go)、[detection.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/cooldown/detection.go)、[selector_cooldown.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/selector_cooldown.go)。

这里最值得学习的不是“有 cooldown”本身，而是：**惩罚范围要和恢复条件一致**。一个模型 429 不应杀死整个渠道；DNS 失败也不应只惩罚一个模型。读失败时普通渠道可降级为跳过冷却，而付费/关键 Antigravity 路径更偏 fail-closed，说明不同风险等级的可用性取舍被显式建模。

### 4.3 缓存粘性和会话身份

`session_affinity.go` 以调用 token 为隔离边界：Anthropic 使用 Session/显式 Agent；Codex 要求 `Session-Id`，优先 `Thread-Id`；不使用请求体里的 `prompt_cache_key` 或消息哈希猜身份。绑定在成功后才写入，TTL 为 1 小时，和 Anthropic 常见 1h prompt cache 对齐；更高优先级渠道恢复后允许迁移。guardian 子代理可只读继承父线程绑定，成功后只写自身键：[session_affinity.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/session_affinity.go)。

它的设计避免了两个常见错误：失败尝试不应该占住会话；粘性也不应压过主备优先级。代价是绑定只存在进程内，重启后丢缓存亲和。对多副本部署，若需要跨实例一致，还需共享状态和冲突策略，ccLoad 当前文件本身没有为此提供证据。

### 4.4 WebSocket：逻辑会话与物理连接分开

`responses_websocket_connection_limiter.go` 对物理上游 WebSocket 同时限制全局和单 Token 的 socket 数；它不把 socket 数与每轮 turn 并发混为一谈。HTTP 转换、原生 WS 和多次 turn 可以共享或切换物理通道，但会话 transcript/response identity 另有生命周期：[responses_websocket_connection_limiter.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/responses_websocket_connection_limiter.go)、[README](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L246)。

该拆分的收益是容量可控，同时不因下一轮复用同一个 socket 而错误拒绝逻辑 turn。陷阱是两套额度都满时错误语义不同：物理 socket 满适合渠道/容量级冷却，turn 满更适合客户端限流；日志和指标必须能区分，否则排障会把容量问题误判为上游故障。

### 4.5 传输连接轮换与凭据重定向安全

`upstream_connection_age.go` 不是只关闭空闲连接，而是轮换完整的 HTTP transport generation。到期 generation 不再接收新请求，已有 HTTP/1.1 或 HTTP/2 流 drain 结束后再关闭剩余空闲连接。若只调用 `CloseIdleConnections`，正在复用的 HTTP/2 stream 仍能保持原 generation 存活，限龄目标会失效：[upstream_connection_age.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/upstream_connection_age.go)。

同一文件还拒绝跨 origin redirect。原因写得很具体：Go `net/http` 在 redirect 时只自动移除 `Authorization` 和 cookie，但 `x-api-key`、Gemini key 与渠道自定义 secret header 可能继续被重建到新 host。这个防线对任何多上游代理都是高价值实现：[refuseCrossOriginRedirect](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/upstream_connection_age.go#L68)。

### 4.6 准入、并发、限额和费用

`HandleProxyRequest` 在读取正文前后分别处理全局并发、Token RPM、请求体和解压后大小、共享内存预算；这和仅限制“上游响应并发”不同，能阻止大请求在排队时先耗尽内存：[proxy_handler.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/proxy_handler.go#L452)。

费用是请求前检查、响应完成后记账，源码明确承认 check-then-act：有并发上限时最多滞后约该上限个请求，无并发上限时并发 N 个请求都可能同时越过预检。它不是永久绕过，但高并发套餐如果要用“硬预算”，必须再设计预估预留/退款或独立预算额度。原文：[proxy_handler.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/proxy_handler.go#L908)。

## 5. 渠道、OAuth、协议与费用

### 5.1 渠道和 Key

当前 UI 同时支持 API Key 渠道、多个 URL、多个 Key、每 Key 优先级、每 Key 模型范围、倍率、通道顺序和协议声明。批量栏可对选中渠道启停、刷新额度，并批量应用优先级、Key 倍率、RPM、并发、日限额和协议。实现入口：[channels.html](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/channels.html)、[channels-render.js](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/assets/js/channels-render.js)。

批量导入不是只在前端解析：CSV 后端要求必需列，逐行解析模型、重定向、Key 范围和价格；OAuth 渠道会先做刷新/身份/额度探针，再调用 `ImportChannelBatch`。这样做避免把无效 OAuth credential 提交进事务，但外部探针本身不能与数据库事务原子化，网络超时后的“是否已生效”仍需保守处理：[admin_csv.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/admin_csv.go)。

### 5.2 OAuth 账号

当前 README 列出 Codex/ChatGPT、Anthropic、Antigravity、xAI、CodeBuddy、Accio、Qoder、Loomy、Z.ai、Cursor，另支持 Vertex 服务账号：[README 渠道节](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L116)。这已经超出“聚合 API key”的范畴，涉及订阅账号、浏览器授权、refresh token、配额窗口、签到和客户端身份模拟。PivotFlow 若引入类似渠道，需要单独评估提供商条款、账号风险和客户端指纹维护成本。

v5.0.3-beta.2 修复 stale passive Anthropic windows，说明配额窗口不是一次快照即可：provider 可能同时返回主动/被动窗口，刷新后必须按窗口身份替换而不是简单 append。该修复在 HEAD：[commit eebe29fe](https://github.com/caidaoli/ccLoad/commit/eebe29fe769332a6c1d822e4fd14a7b701348d24)。

### 5.3 协议覆盖

四种客户端协议都可由“所有声明该模型的上游”承载；上游 URL 可显式声明协议，也可探测并缓存。当前新增 Gemini Neon 和跨协议 tool schema 保留：[v5.0.3-beta.1](https://github.com/caidaoli/ccLoad/releases/tag/v5.0.3-beta.1)。v5.0.0 还处理了 Responses context compaction、截断流和 Chat 干净 EOF：[v5.0.0](https://github.com/caidaoli/ccLoad/releases/tag/v5.0.0)。协议转换不是简单字段映射，尤其需要维护 tool definition、thinking/reasoning history、response item identity、cache breakpoint 和 finish reason。

### 5.4 费用和 OAuth 配额

- Token 可设总、日、月费用，RPM 和并发；UI 同时展示“上限”和“已消耗”边界：[tokens.html](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/tokens.html#L113)。
- 渠道支持每日费用、RPM、并发和可用时间窗；这些限制会使渠道退出选路，但不一定写入 cooldown：[README](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L240)。
- OAuth quota cost 以 credential 的周/月窗口维护 ledger，README 称与上游 quota window 对齐：[README](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L260)。这项能力对订阅账号有价值，但没有实际账号时应仅视为源码和发布说明支持。
- 费用字段区分标准成本和渠道倍率后的有效成本；历史日志需快照倍率，否则未来修改倍率会污染旧账。[v4.12.0](https://github.com/caidaoli/ccLoad/releases/tag/v4.12.0)。

## 6. 导入导出、存储、更新与认证

**CSV 是含 secret 的迁移包。** CSV header 包含 `api_key`、`oauth_credential`、每 Key 模型范围/优先级/倍率等；README 也明确写“上游 API keys 和账号凭证存储在数据库，并包含在 CSV 导出中”。所以导出文件应按凭据备份处理：[admin_csv.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/admin_csv.go#L107)、[README 安全节](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L298)。本文没有证实跨所有数据库驱动存在/不存在字段级信封加密；不要把“MIT + admin-only endpoint”当成数据库被窃取后的保护。

**存储：** hybrid 模式以 MySQL/PostgreSQL 为主库、SQLite 为读模型。启动恢复把配置、quota cost ledger 和 Codex refresh journal 放进恢复事务；Codex refresh history 只补缺失项，不覆盖 SQLite 已有记录。这解决的是 2026-11-18 issue #198，而不是一般备份工具：[sync_manager.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/storage/sync_manager.go#L38)、[issue #198](https://github.com/caidaoli/ccLoad/issues/198)。

**更新：** binary 支持 stable/preview 渠道，下载后校验 SHA-256，再替换并重启；容器则由 image tag 控制：[auto_update.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/auto_update.go)、[release workflow](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/.github/workflows/release.yml#L157)。SHA-256 能发现传输损坏/资产被替换，但 checksum 与二进制来自同一发布通道；本文未找到独立代码签名，因此不能把校验和宣传为完整供应链签名。

**认证：** API token 只保存 hash；admin 密码使用 bcrypt；失败登录有限速，IPv6 按 `/64` 聚合；API token 登录控制台默认关闭，可隐藏渠道；浏览器只保存随机 Web session token，服务端可撤销。源码：[admin_auth_tokens.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/admin_auth_tokens.go#L166)、[auth_service.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/auth_service.go#L602)。

一个必须注意的公开兼容面是 Gemini 的 `?key=`：当前同时接受 `Authorization`、`X-API-Key` 和 query `key`。代理/反向代理访问日志若记录完整 URL，会泄露下游 token。兼容需求可以保留，但 PivotFlow 应在 access log、trace、错误上明确 URL 清洗，并在 UI 中提示优先使用 header：[auth_service.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/auth_service.go#L629)。

## 7. UI：真实截图与源码推断分开

### 7.1 实际截图可见

仓库 HEAD 当前仍有这两个图片：[dashboard.webp](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/images/ccload-dashboard.webp)、[logs.webp](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/images/ccload-logs.webp)。本轮保存到 `sources/ccload-current-dashboard.webp` 和 `sources/ccload-current-logs.webp`。图片右上角明确显示 `v4.10.33-beta.1-7-g67c6d964`，表内日期为 `10/06`，所以视觉证据属于 10 月版本，不能冒充 5.0.x。

截图能直接证实：

- 顶部导航为 **概览、渠道管理、API 令牌、调用统计、请求趋势、日志、模型测试、设置**，右侧有版本、GitHub、语言/主题和退出。
- 总览以四个 KPI 卡（请求数、成功率、总费用、输出 tokens）开头；下方按请求协议、渠道分表，再放“服务健康监测”状态条，颜色区分无请求/异常/波动/健康。
- 日志首屏是横向密集表：时间、渠道 Key、渠道、模型、状态码、首字/耗时、Tok/s、输入、输出、缓存读/建/命中、成本、信息，顶部有范围、协议、渠道、模型、来源、状态和令牌筛选。
- 缓存分列和数字右对齐/等宽有利于比较；429 原文直接铺在“信息”列，导致多条记录行高膨胀。更好的做法是固定短摘要（如 `429 rate_limit · Try again later`）加大宽度，完整 JSON 通过详情抽屉查看。
- 旧 4.10.2 快照的浅蓝紫渐变、圆角卡片、粗表头更偏视觉展示；10 月 dashboard 已转为更克制的浅色数据面板。两套截图显示了不同的视觉风格；当前源码继续演进后，没有 5.0.x 运行截图可以证明最终视觉和交互表现。

### 7.2 5.0.x UI 源码推断

以下不是实际 5.0.x 截图体验，只由 HEAD HTML/JS/CSS 推断：

- 渠道管理采用“页面筛选 + 浮动批量栏 + 右侧分区编辑器”。编辑器分区包括基础、URL、Key、模型、限额、规则、冷却探测、定时任务、管理账号和凭证，右侧长表单按区导航，适合字段多但想保留单渠道心智模型的场景。[channels.html](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/channels.html#L1147)
- 列表列拆为渠道/模型、健康、额度、优先级和操作；顶部可筛状态、认证类型、模型、名称并排序。窄屏改为卡片和单独排序控件：[channels.html](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/channels.html#L761)。
- 批量操作不是分散的逐行菜单，而是选中后出现浮动工作栏，并区分启停、刷新额度、高级批量值。OAuth 凭证清理/刷新显示进度、失败明细，关闭窗口不终止后台任务：[channels.html](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/channels.html#L892)。
- 日志页活动请求与历史请求共用筛选语义，活动行可点“中断”；中断后记录 `start_time`，上游换轮后按钮重新可用，防止重复点击。顶层 UI 只有一个轮询源，页面订阅 active payload，减少多组件重复拉取：[ui.js](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/assets/js/ui.js#L391)、[logs.js](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/assets/js/logs.js#L981)。
- Debug 面板分原始请求、转换后请求、原始响应、转换后响应；可换行、复制、合并流响应。隐私遮罩有分类统计，点击占位符可在原文中定位：[logs.html](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/logs.html#L215)、[logs.js](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/assets/js/logs.js#L2589)。
- 统计页提供表格/图表视图与渠道-模型树；表格含 RPM 峰/均/近、缓存读/建/命中、费用，列可排序，图表按渠道/模型切调用数、费用和 token 用量：[stats.html](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/stats.html)。
- API Token 编辑采用侧栏基础信息 + 限额矩阵 + 渠道/模型双页签。创建后给出 Claude Code/Codex 可复制的配置片段，避免用户手工拼 base URL/header：[tokens.html](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/tokens.html#L190)。

## 8. 最值得 PivotFlow 学习的 12 项

### 1) 同优先级内的缓存粘性，而不是全局粘死

**问题：** 纯轮询会让同一 Claude/Codex 会话切换账号，prompt cache 按组织和账号隔离，后续长上下文可能反复写缓存。\
**ccLoad 实现：** token + Session/Agent 或 Thread 作为键；只在成功尝试后绑定；同优先级档才置顶；1h 过期；guardian 可只读继承父线程。\
**落地思路：** PivotFlow 可在已有 Responses execution identity 之外，为 Anthropic Messages 和 Codex HTTP 增加进程内 affinity store；键包含下游 token、协议和显式 session/thread；失败或绑定渠道冷却立即换绑。\
**收益：** 降低 cache miss、首字延迟和写缓存费用，且保留高优先级恢复能力。\
**代价/陷阱：** 不能按消息内容猜会话；需要防止内存无界增长和跨租户串话；多副本要另做共享状态。\
**证据：** [session_affinity.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/session_affinity.go)。\
**PivotFlow 映射：** **部分已有，新增点是会话粒度**。[selector_sticky.go](../../../internal/app/selector_sticky.go) 已按 Token + 模型保存最近成功渠道，30 分钟 TTL，且只在同优先级层前移；Responses WS 另有严格 execution identity 与 transcript。可借鉴显式 Anthropic Session/Agent、Codex Thread 的独立亲和，避免同一 Token 的多个会话共用 Token + 模型绑定。应扩展或协调现有 sticky 生命周期，而非另建重复、互相冲突的选路器。

### 2) 跨 origin redirect 一律拒绝

**问题：** Go redirect 会重写新请求；只自动删 `Authorization`/cookie，非标准认证头和渠道自定义 secret 可能被带往另一主机。\
**ccLoad 实现：** `CheckRedirect` 比较 scheme/host/port，跨 origin 返回错误并进入渠道 failover，不把重定向透明传回客户端。\
**落地思路：** PivotFlow 的所有上游 HTTP client、OAuth refresh client、模型发现和渠道测试统一使用同一个拒绝函数；同 origin 最多 10 跳。\
**收益：** 防止恶意/错误 DNS、渠道 URL 或上游 30x 泄露 API key。\
**代价/陷阱：** 某些合法 CDN/登录 flow 会跨域；OAuth 浏览器跳转与 API 请求需分开，不能把浏览器授权 redirect 规则误套到 agent 代理。\
**证据：** [refuseCrossOriginRedirect](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/upstream_connection_age.go#L68)。\
**PivotFlow 映射：** **实现缺口已核对**。[newUpstreamHTTPClient](../../../internal/app/upstream_connection_age.go) 没有设置 CheckRedirect；[站点 Provider](../../../internal/site/provider/transport.go) 已限制跳数、主机名及地址，但主机名检查不等于 scheme + host + port 的完整同源检查。需要按代理、站点管理与 OAuth 流分别对齐认证转发规则；不把已有的站点防护描述成完全没有。该判断来自源码，尚未进行故障注入。

### 3) 连接寿命按 transport generation 轮换

**问题：** HTTP/2 多路复用下“关空闲连接”可能不关在途 stream，连接复用上限形同虚设。\
**ccLoad 实现：** 定时退休整个 generation，旧 generation 停收新请求，active 归零后关完整池。\
**落地思路：** PivotFlow 的 [upstream_connection_age.go](../../../internal/app/upstream_connection_age.go) 已实现完整 generation 轮换、停止新请求和在途响应排空。保留该实现；对照上游新增修复与现有测试，检查各类 client 的覆盖，避免重复开发。\
**收益：** DNS/证书/上游 routing 变化能稳定生效，同时不打断在途长请求。\
**代价/陷阱：** 轮换会丢失 keep-alive/HTTP2 预热，增加短期 TLS handshake；不能在 active 时强切。\
**证据：** [upstream_connection_age.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/upstream_connection_age.go)。\
**PivotFlow 映射：** **已有**，`CLAUDE.md` 描述同类实现；应把 ccLoad 的完整 generation 测试场景与代理池覆盖纳入回归，而非另造机制。

### 4) 物理 WebSocket 额度与逻辑 turn 额度拆开

**问题：** 一个 Codex 物理 WS 可承载多 turn；只按请求 tok/semaphore 限流会低估 socket、只按 socket 限制又会误杀并发 turn。\
**ccLoad 实现：** 分别计数物理上游连接与逻辑执行，做全局和 token 双层限额。\
**落地思路：** PivotFlow 保持“下游连接/逻辑会话/上游物理连接/turn lock”四层指标，日志显示拒绝原因；容量拒绝与上游故障不要写同一种 cooldown。\
**收益：** 连接池和 turn 并发都能独立调优，排障清楚。\
**代价/陷阱：** cleanup 必须同时释放连接状态和逻辑状态；断线重放时不能携带旧 socket 的 `previous_response_id`。\
**证据：** [responses_websocket_connection_limiter.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/responses_websocket_connection_limiter.go)。\
**PivotFlow 映射：** **部分已有**。已有下游/Token 连接限制、稳定逻辑会话和 reuse age；建议核验是否也把每轮 turn 与物理 socket 做成独立指标。

### 5) 冷却恢复时间优先使用结构化上游信号

**问题：** 所有 429/5xx 用固定退避会过早重试或过度冷却；所有 reset 字段都当同一窗口又会把被动 quota 与主 quota 混淆。\
**ccLoad 实现：** 规则/内置分类器区分 key/model/channel/client；支持 Retry-After、Google RetryInfo、provider business code、精确 reset；全冷却时挑最早恢复。近期修复 stale passive Anthropic windows。\
**落地思路：** PivotFlow 在现有 `cooldown/detection.go` 和 provider classifier 上增加结构化 extractor 的单元矩阵：status + headers + JSON shape + window identity；只接受可信 reset，无法解释时退回经验退避。\
**收益：** 减少无效重试和长时间误冷却，提高多账号利用率。\
**代价/陷阱：** reset 时间可能跨时区/rolling window；错误匹配会产生“永不冷却”；需要最大/最小边界和观测。\
**证据：** [cooldown/detection.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/cooldown/detection.go)、[v5.0.3-beta.2](https://github.com/caidaoli/ccLoad/releases/tag/v5.0.3-beta.2)。\
**PivotFlow 映射：** **已有且有部分结构化能力**。现有指数退避、命名捕获 reset、全冷却兜底已经很强；缺口应按 provider 实测字段补齐，而不是重构整套决策 API。

### 6) Token 采用总/日/月 + RPM + 并发四维限制

**问题：** 只有总量/日费用不能表达“每天小预算、每月大预算、短时防刷”。\
**ccLoad 实现：** token 同时存总/日/月 USD、RPM、MaxConcurrency，保留微美元整数以降低浮点误差；UI 展示已用值。\
**落地思路：** PivotFlow 若当前只有部分维度，可复用 `AuthToken` 和 CostCache，按完成时间归入日/月窗口；读取与写入使用原子状态；明确 `0=无限`。\
**收益：** 一人/多 agent/多人共享可给出可预测上限。\
**代价/陷阱：** 金额只有完成后才知道，预检天然滞后；RPM 的 rolling/fixed window 需统一；时区必须写死并可观测。严格硬预算需预授权+退款，复杂度显著上升。\
**证据：** [tokens.html](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/tokens.html#L113)、[admin_auth_tokens.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/admin_auth_tokens.go#L166)、[proxy_handler.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/proxy_handler.go#L908)。\
**PivotFlow 映射：** **部分已有，差距已核对**。[AuthToken](../../../internal/model/auth_token.go) 当前有总费用限额、最大并发和 RPM 统计；没有 Token 日/月费用限额字段及 Token RPM 准入字段。渠道另有日费用和 RPM 限制。只有共享 Token 确实需要周期预算时，再扩展这两个维度，并保持统计与准入语义分开。

### 7) 费用型 token 默认要求正并发上限，至少提示超额窗口

**问题：** 若没有并发上限，N 个请求可在同一时刻全部通过预检，预算的“硬限制”会明显超支。\
**ccLoad 实现：** 源码直接写明最多滞后“并发上限”个；费用型 token 当前有并发要求/校验，UI 也把费用和并发放同一限额区。\
**落地思路：** PivotFlow 可以把“费用限额且 max_concurrency=0”列为 warning 或强制设置保守上限；日志/API 错误返回 `cost_limit_exceeded` 和 `x-should-retry:false`。\
**收益：** 预算语义对用户可解释，减少共享 token 的账单争议。\
**代价/陷阱：** 低并发会延长排队；若另做预估预留，必须处理取消、失败、供应商不返回 usage 的退款。\
**证据：** [proxy_handler.go 注释](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/proxy_handler.go#L908)。\
**PivotFlow 映射：** **建议校准文档和 UI**。当前费用准入与完成后结算存在并发放大的超额窗口；“允许超额一个请求”只适用于无并发的理解，不能当作普遍上界。先提示软限额及并发关系，再按需求设计周期额度。

### 8) API token 只读控制台

**问题：** 团队共享时，管理员不想给出完整控制台，但使用者需要查自己的调用、成本和限额。\
**ccLoad 实现：** 管理员开关启用后，token 可登录 Web；只能看自身数据，默认隐藏渠道，可另开 channel visibility；使用独立会话限速。\
**落地思路：** PivotFlow 复用 downstream token 身份创建一个受限会话角色，服务端字段投影，不靠前端隐藏；渠道名、上游 URL、其他 token 默认不可见。\
**收益：** 降低共享账号和泄露 admin 密码的需求，支持自助排障。\
**代价/陷阱：** 必须做 endpoint-by-endpoint 授权，防止 `/admin/*` 一处漏判；web session 撤销、只读和 token revoke 需要联动。\
**证据：** [README](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L261)、[auth_service.go](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/internal/app/auth_service.go#L769)。\
**PivotFlow 映射：** **建议补足**。README 只描述 proxy token 管理，未显示 token 作为只读 Web 身份。

### 9) 订阅账号的 quota-window cost ledger

**问题：** 订阅账号的“钱”不能只按日普通成本累计；不同 provider 有周/月窗口、reserve credit 和滚动 reset。\
**ccLoad 实现：** 维护 credential + window key + 标准成本 ledger，refresh 时更新 active windows，并把标准成本与渠道倍率分开。\
**落地思路：** 只对 PivotFlow 实际支持的 OAuth provider 建 provider-specific snapshot；窗口用稳定 ID，reset 后关闭旧窗口；刷新失败保留最后已知状态并标 stale。\
**收益：** 能按 provider 额度窗口做选路、预算和解释，不把 API 日费用模型硬套订阅。\
**代价/陷阱：** provider 未公开字段会频繁漂移；旧窗口清理错误会造成双算；需要 golden fixtures 和人工额度核对。\
**证据：** [README](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L260)、[v5.0.3-beta.2](https://github.com/caidaoli/ccLoad/releases/tag/v5.0.3-beta.2)。\
**PivotFlow 映射：** **建议补足（按 provider 分阶段）**。PivotFlow 已有 Codex/Antigravity OAuth，但 README 未把 OAuth quota windows 列为一等成本模型。

### 10) Token 创建后的客户端配置片段

**问题：** 用户拿到 token 后仍需查文档正确拼 `/v1`、auth mode、Codex provider TOML 和 WS 支持，容易在接入阶段失败。\
**ccLoad 实现：** token 成功弹窗提供 Claude Code 和 Codex 两个 tab，分别展示可复制 JSON/TOML/shell 片段，并提示 app-server daemon 的旧配置/旧 token 缓存。\
**落地思路：** PivotFlow 用当前公开 base URL 和 token 动态生成只读片段；不把 token 写日志；提供“复制全部”和 provider-specific 注意事项。\
**收益：** 首次成功率和支持成本显著改善。\
**代价/陷阱：** 配置格式随客户端版本变化；片段要带版本/时间戳和文档链接，不要复制过时路径；`/v1` 对 Claude/Codex 的差异需准确。\
**证据：** [tokens.html](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/tokens.html#L190)、[README Codex 配置](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/README.md#L174)。\
**PivotFlow 映射：** **建议补足或已有但未在 README 突出**；这是低成本、高用户价值项。

### 11) 批量运维采用后台任务、进度和可恢复失败

**问题：** 几百个渠道时，同步刷新/清理/导入的逐项失败比整体成功更常见；关窗就中止会让运维不可用。\
**ccLoad 实现：** 批量栏固定选中集；OAuth 凭证导入/清理、CSV 导入和 Key 批量测试都有计数/进度/失败明细；关闭对话框不终止后台任务。\
**落地思路：** PivotFlow 将批量任务记录为 job（pending/running/succeeded/failed），允许关页后轮询；前端只读任务状态，后端幂等、限并发、每个对象有独立错误。\
**收益：** 大规模操作可观察、可重试，不阻塞 UI。\
**代价/陷阱：** 后台任务要定义取消、重试和重复提交语义；OAuth 外部探针不能回滚，需防止“界面显示失败、上游已刷新”；批量应用限额必须有覆盖/保留语义。\
**证据：** [channels.html 批量栏](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/channels.html#L892)、[OAuth 进度](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/channels.html#L661)。\
**PivotFlow 映射：** **部分已有**。PivotFlow 已有批量同步/导入和活动请求，需判断任务是否真正后台持久化、是否能跨页面恢复；不要仅复制进度条。

### 12) 日志固定摘要 + 详情钻取，做规模友好的可观测性

**问题：** 实测旧截图把完整 429 JSON 塞进表格，单行可占多行；300-400 模型/约 50 渠道的 issue #178 也提示筛选和编辑规模压力。密集表若同时承担诊断详情，会损害扫读。\
**ccLoad 实现：** 表格展示 status、首字/耗时、cache、cost 的短字段；详情通过 debug modal 看原始/转换后请求与响应；活动请求单独可中断；筛选状态可从 URL/localStorage 恢复；统计有表/图切换。\
**落地思路：** PivotFlow 日志表固定行高，错误列只显示结构化摘要+代码；详情抽屉显示 sanitized raw、翻译前后差异、route attempt 列表、cooldown/reset、费用分解和复制按钮；筛选/排序尽量服务端完成，长表分页/虚拟化。\
**收益：** 行高稳定、横向比较快，同时保留深度排障。\
**代价/陷阱：** raw debug 含敏感数据，必须 header/body 双层脱敏、短时保留、RBAC；localStorage/URL 可能带 secret，绝不能把 token 或完整请求写入。\
**证据：** [实际 logs 截图](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/images/ccload-logs.webp)、[logs.js](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/web/assets/js/logs.js#L1900)、[issue #178](https://github.com/caidaoli/ccLoad/issues/178)。\
**PivotFlow 映射：** **部分已有**。PivotFlow 已有请求日志、活动请求、模型测试；需确认是否已有原文/转换后双响应对照、筛选持久化和长错误摘要。

## 9. 近期问题、修复信号与风险

| Issue | 状态 | 说明 |
|---|---|---|
| [#199](https://github.com/caidaoli/ccLoad/issues/199) | 本轮抓取时 open | OAuth 模型发现遗漏账号实际可用模型，包括 Codex `gpt-6.2-sol`/`gpt-6.2-astra`、Anthropic Fable 5.2。说明登录/refresh 成功不等于完整模型目录；最新 beta 也不保证已解决。 |
| [#198](https://github.com/caidaoli/ccLoad/issues/198) | closed | hybrid bootstrap 丢 Codex refresh history，随后由 `8982f82` 修复，并以 v5.0.2-beta.3 发布。 |
| [#197](https://github.com/caidaoli/ccLoad/issues/197) | closed | Token 渠道/模型选择器显示禁用项，由 `2b52c79` 修复。 |
| [#196](https://github.com/caidaoli/ccLoad/issues/196) | closed | vendor-prefixed/目录外模型的动态 thinking 不生效，v5.0.2-beta.1 修复。 |
| [#192](https://github.com/caidaoli/ccLoad/issues/192) | closed | 模型发现默认 UA 被 Cloudflare 403，`086dcd88` 改善并在 UI 识别 challenge。 |
| [#178](https://github.com/caidaoli/ccLoad/issues/178) | 规模风险案例 | 300–400 模型、约 50 渠道时筛选和编辑卡顿。即使后续已有性能提交，仍值得作为 PivotFlow 的前端容量测试场景。 |

其他风险：

- **许可证覆盖范围：** 主仓库为 [MIT](https://github.com/caidaoli/ccLoad/blob/eebe29fe769332a6c1d822e4fd14a7b701348d24/LICENSE)，复制/修改/商用需保留 copyright 和 permission notice，软件按原样提供、无担保。协议转换核心、Cursor SDK Bridge、第三方 auth 目录另有许可证/来源约束；复用具体子树时必须逐目录核验，不能只以根 LICENSE 代替。
- **供应商条款不是 MIT 能解决的问题。** 订阅 OAuth、客户端身份、签到和受限客户端协议适配必须独立评估账号和数据风险。
- **凭据文件面很广。** OAuth credential、API keys、management login 和 CSV 都属于高敏感数据；仅在 admin endpoint 后并不意味着可放入普通 Git、聊天或长期 debug。
- **登录/发布供应链。** 登录有 bcrypt、限速和 IPv6 聚合；binary 更新有 SHA-256。当前材料没有显示独立代码签名，checksum 不应被当成供应链签名。`master` 在调研时推进很快，近期 commit 显示 unsigned/unverified；若部署 beta，应按 SHA 固定并在生产前跑核心 smoke/回归。
- **评测缺口：** 本轮没有真实 OAuth 账号、没有触达 live demo、没有持续负载，也没有对 ccLoad 运行完整测试套件。性能/账务/多副本行为只能从设计、单测存在性和源码边界判断。

## 10. 证据文件与可复核性

本目录 `sources/` 中保存的文件均为本轮只读获取后留存：

- `ccload-releases.json`、`ccload-tags.json`、`ccload-repo.json`、`ccload-head.json`：Release/Tag/SHA/默认分支。
- `ccload-tree.json`：HEAD 文件树，用于定位源码与截图。
- `ccload-issue-192.json`、`ccload-issue-196.json`、`ccload-issue-197.json`、`ccload-issue-198.json`：近期已修复 issue 原文；#199/#178 的后续 API 抓取遭遇 GitHub 匿名限流，报告中链接仍指向公开页面。
- `ccload-src-*.go|html|js|yml|md|LICENSE`：报告中引用的 HEAD 源码快照。
- `ccload-current-dashboard.webp`、`ccload-current-logs.webp`：HEAD 仓库所附官方图片；图片内证明是 `v4.10.33-beta.1-7-g67c6d964`、10/06。
- `ccload-current-*-preview.png`：仅为查看便利生成的预览，不增加版本证据。
- `ccload-4.10.2-dashboard.jpeg`、`ccload-4.10.2-logs.jpg`：本地 4.10.2 快照的旧截图副本。

## 11. 研究缺口

1. **没有 5.0.x 实际运行截图。** HEAD 变更过导航/抽屉/批量栏/Token 页，但仓库内图片仍是 10 月版；所有 5.0.x 交互仅为源码推断。
2. **没有真实 provider 账号。** OAuth refresh、quota reset、订阅成本准确性、模型发现遗漏和多账号 failover 未做 live 验证。
3. **没有全量 commit diff。** 本地 4.10.2 目录不是 Git worktree；报告用固定 release notes、tag SHA 和旧/新文件对照，不能声称枚举了每个中间 commit。
4. **没有性能基准。** #178 说明规模风险，但本轮未创建 300–400 模型数据集，未测首屏、筛选、编辑、批量更新的 p50/p95。
5. **没有实际攻击/供应链审计。** 报告只描述已读防线；未进行 credential-at-rest、CSRF、CSP、SSRF、依赖 SBOM 或二进制签名的完整审计。
6. **没有跨实例一致性验证。** session affinity 与 quota/limit cache 的跨副本语义未在源码中完整证实。
7. **没有执行 ccLoad 测试。** 本轮以只读研究和源码证据为主，避免下载/构建大项目或改动产品代码。
