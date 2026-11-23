# CLAUDE.md

> PivotFlow（枢衡）：个人 AI API 站点管理与智能路由控制台。站点/账号/签到/公告构成控制面，PivotFlow 的渠道/Key/URL 选择、故障切换、协议转换与成本计量构成路由数据面。
> 本文件是 AI 操作手册——只记命令、硬约束、反直觉机制与入口;展开细节读对应代码。

## 命令

必须 `-tags sonic`;环境变量见 `.env`。

```bash
make build          # 构建(注入版本号+strip)
make dev            # 开发运行
bash .agents/skills/sync-cliproxy-core/scripts/verify.sh --tests # 协议快照审计+定向测试
bash .agents/skills/pivotflow-release/scripts/release.sh --self-test # PivotFlow 发布脚本自检（目录名为兼容保留）
go test -tags sonic ./internal/...
make race-fast      # 高价值 race 子集
make race           # 全量 race(可用 RACE_P/RACE_PARALLEL 调并行度)
make verify-web     # 前端验证(含 node:test)
golangci-lint run ./...   # 提交前必须零警告
```

## 代码规范(硬约束)

- 必须 `-tags sonic`;用 `any`,不用 `interface{}`
- YAGNI,拒绝过度工程;Fail-Fast:配置错误 `log.Fatal()` 退出
- Context:`defer cancel()` 无条件调用,用 `context.AfterFunc` 监听取消
- lint 启用 errcheck/govet/staticcheck/unused/revive/bodyclose(gosec 已禁)

## 架构与入口

```
internal/app/        HTTP+业务:proxy_* / admin_* / selector_* / *_cache / *_service
internal/protocol/   协议契约与注册;builtin/ 是 PivotFlow 适配层;cliproxy/ 是上游转换核心快照
internal/storage/    存储(factory/hybrid_store/sync_manager/migrate;sql/ sqlite/)
internal/cooldown/   冷却决策   internal/util/  classifier/cost_calculator/money/...
internal/site/       站点 Provider、凭证加密与 Webhook;控制面处理入口在 internal/app/admin_sites*.go
internal/{model,config,version,testutil}/   web/  管理后台前端(HTML+assets/{css,js,locales})
console/             PivotFlow React 控制台源码;npm run build 输出到 web/console
docs/                面向用户与维护者的唯一功能文档;截图只能使用 scripts/docs_screenshots.py 的合成数据
www/                 精简静态介绍页(`make www-setup` 同步品牌图和脱敏截图,别和 web/ 混淆)
```

| 任务 | 入口 |
|------|------|
| 代理主链路 | `proxy_handler.go:HandleProxyRequest` → `runProxyAttemptLoop` → `proxy_forward.go` → `proxy_stream.go` |
| Responses WebSocket | `proxy_responses_websocket.go:HandleResponsesWebsocket` → `executeResponsesWebsocketTurn` → `runProxyAttemptLoopWithFailureBoundary`;会话状态见 `responses_execution_session.go` |
| 渠道/Key/URL 选择 | `selector*.go`、`key_selector.go`、`smooth_weighted_rr.go`、`url_selector.go` |
| 错误分类/冷却 | `util/classifier.go`、`cooldown/manager.go` |
| 协议转换 | `protocol/registry.go` → `protocol/builtin/register.go` → `protocol/builtin/cliproxy_adapter.go`;核心实现/同步规则见 `protocol/cliproxy/{UPSTREAM.md,...}` |
| 定价/成本 | `util/cost_calculator.go` |
| 加 Admin API | `admin_types.go` 定类型 → `admin_<feature>.go` 实现 → `server.go:SetupRoutes` 注册 |
| 数据库 | Schema 启动自动 `migrate.go`;事务 `(*SQLStore).WithTransaction`;改后失效 `InvalidateChannelListCache`/`InvalidateAPIKeysCache` |

Responses WebSocket execution identity：同 Token 下以 `Session-Id` 标识顶层会话；存在 `Thread-Id` 时组合两者，使 Codex 主代理/子代理 transcript、Response ID、turn lock 隔离；无 `Thread-Id` 时回退原 `Session-Id` 契约，禁止改用请求体 `session_id`、`prompt_cache_key` 或每回合变化的 request/turn/window ID。默认限制：下游连接全局 64、单 Token 16；上游每 45 秒发送 Ping，连续 5 分钟未收到任何帧/Pong 判定失活。下游全部断开满 5 分钟后由每分钟清理器关闭上游物理连接（实际约 5–6 分钟），稳定逻辑会话与已提交 transcript 在 `responses_ws_session_ttl_minutes` 到期前（新安装/重置默认 15 分钟，小内存机器可设 10；升级不改已有值）不会因容量/预算压力被逐出。`upstream_connection_reuse_limit_seconds`（默认 0，不限制）还可限制上游连接的复用时间；达到时限的空闲连接立即关闭，在途 turn 完成后再关闭，下一轮按需重连并重放完整 transcript，因为 Response ID 只在原物理 WebSocket 上有效。达到 `responses_ws_max_sessions` 只拒绝新会话身份；已提交 payload 超过 `responses_ws_max_transcript_bytes`（默认 128 MiB）后，所有新回合在触达上游前以 `429/rate_limit_error/rate_limit` 拒绝，已准入回合仍可提交，有限最坏超量为 `max_sessions × max_body_bytes`。`/admin/runtime-metrics` 的 `transcript_bytes` 只统计有效 payload，不是 Go 堆占用，并提供 `ttl_expired`、`capacity_rejected`、`budget_rejected`、`previous_response_misses` 进程累计计数。下一轮会优先原渠道/Key/URL 并按需重连。

## 故障切换(`util/classifier.go` + `cooldown/detection.go` + `cooldown/decision.go`)

- **决策 API** (`cooldown/manager.go`):生产代码使用 `Decide(ctx, ErrorInput) (Decision, error)` + `ApplyEffect(ctx, Decision, channelID, keyIndex, statusCode) bool` 分离重试策略(`RetryStrategy`)与凭据惩罚(`Effect`)。`Decision` 包含:`Retry`(NextKey/NextURL/NextChannel/None/RefreshToken)、`Effect`(CoolKey/CoolModel/CoolChannel/ClearCooldowns/RecordFailure/None)、冷却时间戳(`Has*CooldownUntil` + `*CooldownUntil`)、原因(`CooldownReason`)、模型(`Model`)。`HandleError`/`HandleErrorWithKeyFallback` 与 `Action` 枚举已废弃(仍保留向后兼容),替换为 `DecideWithKeyFallback`/`ApplyEffectWithKeyFallback`
- Key 级(401/403)→ 冷却当前 Key,重试同渠道其他 Key;所有启用 Key 均冷却时自动升级渠道冷却
- 模型级(`model_cooldown`,上游 HTTP 400/499/5xx/520/524/429,597 服务类 SSE 错误,598/599 流故障,连接重置/HTTP2 流关闭/空响应/网络超时,404 模型不可用,410 明确模型退役)→ 写入 `(channel_id, 实际上游模型)` 冷却;直接切渠道,不再尝试同渠道其他 Key;**但 598/599 流超时故障在多 URL 场景下会先尝试同渠道其他 URL**（超时是 URL 相关的网络延迟问题）;所有配置模型均冷却时自动升级渠道冷却
- 渠道级(DNS/连接拒绝/网络或路由不可达)→ 切渠道
- 原生协议能力不支持(响应未提交的 HTTP 400、非模型 404/405,或结构化 500 明确返回 `convert_request_failed` + `not implemented`)→ 能力协商事件,不记失败日志、不冷却 Key/模型/渠道/URL;auto 模式可转换时同渠道/Key/URL 探测其他协议,不可转换时切 URL/渠道
- 客户端错误(406/413,404 非模型 `does not exist`)→ 直接返回,不重试
- 成本限额达到 → 跳过该渠道
- Key/模型/渠道共用指数退避策略:按错误类型取初始值(默认认证 5 min、服务端 2 min、超时/限流 1 min),随后翻倍并在 30 min 封顶;上游或自定义规则给出精确 reset 截止时间时优先使用
- **冷却探测规则**(`cooldown/detection.go`):渠道 `cooldown_detection_rules` 为空时继承系统设置 `global_cooldown_detection_rules`;按 rules 数组顺序(提交后重编号 0..N-1)匹配 status+正则,命名捕获组可解析出精确 reset 时间。网络故障故意不进这个匹配器(没有可信上游错误体);规则命中但不可执行时回退内置分类器,不猜冷却时长。`EvaluateCooldownDetectionRules` 无副作用,代理链路与 admin 规则测试端点共用
- **全冷却兜底**(`selector_cooldown.go`,`cooldown_fallback_enabled` 默认 true):所有渠道都冷却时不直接拒绝,而是挑「最早恢复」的渠道打 `CooldownFallback` 标记继续走正常流程,Key 也改选最早恢复的(`SelectCooldownFallbackKey`)。排查「明明全冷却了为什么还在发请求」先看这里;设 false 才直接拒绝
- **Key 回退**(`RetryOtherKeysOnFailure`):模型/渠道级故障写入 Key 冷却以让其他 Key 先试。`DecideWithKeyFallback` 产生 `{RetryNextKey, EffectCoolKey}` 并复制模型冷却时间戳(如有)到 Key 冷却时间戳;有明确渠道冷却时间戳时不回退(WebSocket 槽位耗尽等全局故障)
- Responses WebSocket 特例(仅首个语义输出前):非 WS→非 WS、原生 WS→非 WS/原生 WS 均在网关内部切换,其中 WS→非 WS 使用 execution session 的完整 transcript;非 WS 故障且下一候选为原生 WS 时返回 `status=502` 的 `server_error/upstream_unavailable` 并用 close code `1011` 断开,让 Codex 客户端完整 replay;已有语义输出后一律不切换或重放

## 自定义状态码(改相关代码前先读语义)

- **499** 客户端取消:不计失败、不冷却;上游直接返回 499:模型级冷却
- **596** 1308 配额超限 → Key 级冷却,不计健康度
- **597** SSE error(HTTP 200+错误体)→ `classifySSEError` 按 error.type 动态判级
- **598** 首字节超时 → 模型级冷却,但多 URL 场景下会先尝试其他 URL;**599** 流式中断 → 模型级冷却,但多 URL 场景下会先尝试其他 URL
- **`fwResult.StreamDiagMsg` 是 599 的判定开关,不只是日志字段**:非空即被 `forwardAttempt` 判为流不完整,置 599 并走模型级冷却。所以只有真实上游故障才允许写入,客户端断开必须先过 `isClientDisconnectError`(`buildStreamDiagnostics` 与 Codex 非流式收集器 `codex_wire.go` 各有一处),漏一处就会把 499 误升成 599。`markIncompleteStreamForwardResult` 不覆盖已经是 598 的状态码——两者冷却初值不同
- **429** 统计页/健康时间线计入 ErrorCount 与成功率,`rate_limited` 是 ErrorCount 子集;健康度排序(`GetChannelSuccessRates`/effective priority)排除 429,真实渠道级限流交给冷却过滤

## 关键机制(要点,细节读对应文件)

- **配置预览**：JSON/WebDAV 恢复和渠道页同步先生成服务端固定快照，再按 ID 确认；旧无预览恢复接口返回 428 `preview_required`。预览最多 16 个、保留内容编码预算 64 MiB、有效 10 分钟，终态再保留 10 分钟；DELETE 释放非运行记录。备份整份配置事务、同步按账号事务，写入前重查配置指纹；手动投影保留，上游完整发现后移除的 Key 只停用渠道。`commit_unknown` 禁止重试；主库已提交但副本/令牌权限重载失败显示明确警告。令牌热重载从主库读取并串行发布。见 `docs/config-import.md`。

- **客户端接入**：`ConnectPage` 复用令牌表单，完整下游令牌仅当前页面内存；URL 只传 ID。`clientConnection.ts` 生成 Codex/Claude/Gemini/OpenAI Python 模板，模型别名和秘密按各语法编码；Gemini 非本机必须 HTTPS。`gatewayClient.ts` 只向同源 `/v1`、`/v1beta` 请求，omit cookie/禁止 redirect，不使用管理员会话或自动重发。只有完整文本终态成功；停止/超时为未确认。`/v1/models?protocol=openai|codex|anthropic` 显式视图先校验允许列表，所有模型视图共用真实协议候选与令牌模型/协议/渠道范围；不能把模型可见当连接成功。见 `docs/client-connection.md`。

- **站点维护任务**：`site_task_queue.go` 合并活动操作并先获取账号租约后落任务；全局 4 执行/128 登记，排队 10min 与执行 2min 分开。`provider.RequestBudget` 由站点服务工厂共享，按 origin 单并发、250ms 间隔、4 全局 HTTP；body EOF/Close 后释放，取消不得早于 transport 退出释放。429/带 Retry-After 的503共享冷却，缺省30s，不重放限流签到。价格 singleflight 读取独立8s并登记 runAsync，停止时取消/等待。GET `/admin/site-tasks` 只显示当前进程活动任务；取消当天定时签到必须结束 attempt/run，避免下一 tick 重发。前端会话单例保留身份，unknown 不作为失败重试；取消只在服务器确认后显示终态。见 `docs/site-tasks.md`。
- **浏览器账号桥接**：`admin_browser_import.go` 接收原 JSON 文本，编码请求和解码文档各 2 MiB、64 条；严格 UTF-8／Unicode 转义／重复键／深度、来源与完整地址；unreserved 编码归一但 %2F 保持编码。`browser_import_verification.go` 只读 resolver/profile/models，90s、共享 site gate 和既有账号 2min 租约；不能调用会 refresh／reveal Key 的完整凭证准备路径。身份只取 profile 正整数 ID；固定预览内冻结凭证，事务重查主库指纹。修复用 `RepairSiteAccountCredential` 保留开关／历史／模型／渠道；同主凭证仍比较 Refresh Token 与有效期，辅助资料更新须显式目标与验证，按最终冻结资料重新判定。省略／null 保留旧刷新字段，非法类型拒绝；新账号停用且自动任务关闭。unknown 同来源禁替代、人工 `acknowledged=true` 才可移除，记录仅当前实例 10min。`#/browser-import` 的助手自包含、有限存储键、同 origin／顶层、手工 Cookie、零网络；渠道链接只预选。契约见 `docs/browser-import.md`。

- **运行健康**：管理员 `GET /admin/runtime-metrics` 由 `collectRuntimeMetrics(now)` 只复制真实操作的内存观察，禁止数据库探测或执行维护。`monitoring.OperationMonitor` 零值可用、并发安全、完成回调幂等；只有组合错误所有非空叶节点都取消才计取消，取消不能掩盖真实存储故障。`queued` 是通道等待，`buffered` 含线程持有的未完成批次；操作失败次数与最终记录损失分开。主库复制的后续成功不补发旧失败批次，`cache_may_be_stale` 不因无关同步成功清除，启动恢复各阶段的失败分别保留。未知存储能力显示 unknown，确认纯 SQL 才关闭副本相关状态。健康版本 1，状态与错误类别允许列表；前端 10 秒完成后轮询、8 秒超时、30 秒过期，失败保留原始诊断但将页面状态标为未知。见 `runtime_health.go`、`docs/runtime-health.md`。

- **请求历史**：`request_trace.go` 为 HTTP 调用和执行器中的每个 WS turn 生成 `pf_req_…`；HTTP 通过 `X-PivotFlow-Request-Id` 返回，活动请求与普通日志关联同一 ID。`forwardAttempt` 建立尝试组，context 携带 generation 防迟到 WS 回调污染后续尝试；真实传输、兼容重试和协议协商单独记录。`request_traces` 摘要与最多 128 个事件同一行原子写入，完整计数不截断；独立有界队列可丢弃，不能当财务流水。流式 HTTP 200 后的 597/599 仍是失败。Token 查询必须强制所有权并投影隐藏路由细节，见 `admin_request_traces.go` 与 `docs/request-history.md`。

- **请求缓存**：`request_cache*.go` 默认关闭，客户端需 `X-PivotFlow-Cache: use/refresh/bypass`；仅原生 OpenAI Chat／Anthropic Messages、非流式、temperature=0、单候选纯文本与递归字段白名单。按 Token ID/hash、协议、路径、原正文和语义头精确分帧 SHA-256；工具／会话／未知字段、协议转换／语义正文改写、OAuth／AnyRouter／自定义规则绕过。命中仍走认证／并发／可靠结算／权限／费用准入，只读形成候选，不推进 SWRR 或选择 Key；完整写出后复用幂等结算，新增 tokens/cost=0，不写伪造渠道日志。全局代际与活跃键填充代际防清空／配置／刷新旧结果回填；活跃键4096硬限、正文LRU/TTL和捕获预算独立有界。内部成功的 `invalidateChannelRelatedCache` 只刷新存储冷却／Key观察，不能触发语义配置失效。GET观察不清理过期；原因映射稀疏，前端只对缺省的合法原因计数补0，不接受未知代码／缺指标。历史 cache_status/cache_reason 固定值，SQL增量迁移及Token安全投影；见 `docs/request-cache.md`。

- **选择**:先冷却过滤(正确性优先),再二选一排序——`enable_health_score` 默认 **false** 走渠道平滑加权轮询(按有效 Key 数),开启才走健康度排序(`calculateEffectivePriority`:`P_eff = Priority - 失败惩罚 - TTFB惩罚`,两种惩罚各自按样本量打置信度折扣,TTFB 部分还要 `enable_ttfb_score` 单独开)。成本限额检查优先于冷却;模型冷却按每个渠道解析重定向/统一映射后的实际上游模型过滤;多 URL 探索优先→1/EWMA 加权随机,失败 URL 独立退避;`ChannelURL.Exact` 派生运行时 `#` 标记实现精确转发,持久化 URL 本身不含标记
- **路由候选是只读快照**(`cache.go:GetEnabledChannelsSnapshotByModel`):选择链路拿到的 `*model.Config` 归缓存所有,只有外层 slice 是请求私有的——可以过滤、排序、原地重排,但**禁止改 Config 字段**,要改先 `Clone()`(见 `selectAlphaSearchCandidates`);需要可变副本的路径继续用深拷贝的 `GetEnabledChannelsByModel`。`filterCooledChannels`/`selectByWeight`/`selectWithCooldownInPlace` 都原地压缩或重排入参,调用方只能用返回值,不能再按原长度复用入参
- **模型路由工作台**：`route_selection.go` 共用真实候选形成，`admin_model_routes.go` 提供 version 1、有界清单与诊断；候选与尝试分开，权重沿用启用 Key 减冷却，健康参考池在令牌渠道过滤前。诊断不得推进游标、清理运行状态、占槽、解密凭证或刷新价目。`#/routing` 的模型／协议／令牌与精确历史可恢复；历史模型限 191 字符，诊断限 512。令牌模型选择使用统一名称的启用清单，成功完整同步只清理已确认消失且无剩余渠道引用的别名，保留规范组名。见 `docs/model-routes.md`。
- **模型停用**(`ModelEntry.Disabled`):`disabled=true` 的模型对外完全不存在——`GetModels`/`modelIndex`/`channelModelCooldownKeys` 一律跳过。刷新模型列表的 `replace` 模式会按原名、归一化别名、重定向目标三种键把停用标记传播回新拉取的条目,避免刷新一次就把停用状态洗掉
- **渠道级限流**(`channel_rpm_limiter.go`+`channel_concurrency_limiter.go`):`rpm_limit`/`max_concurrency` 都是 0=无限。注意 `max_concurrency` 这个名字在系统设置(全局信号量)、Auth Token、渠道三处各有一份,互不相干,改代码前先认准层级
- **多协议处理**:每个渠道默认接受四种客户端协议,`protocol_transform_mode` 选择策略:`auto`(默认)、`upstream`(只直通客户端协议)、`local`(只本地转换)。实际上游能力只由 `ChannelURL.Protocols` 声明:非空声明是权威配置,不兼容 URL 无请求、无冷却地跳过。local 优先有声明的 URL 并保持声明顺序;仅当全部 URL 未声明时按 Anthropic → Codex → OpenAI → Gemini 请求。auto 先试客户端协议,再按 OpenAI → Anthropic → Codex → Gemini 自动探测并跳过已试协议;未提交响应的 HTTP 400、非模型 404/405、明确未实现 500、请求到达 API 前的 Cloudflare 403 拦截页或当前转换无法表示请求时才继续下一协议。成功协议按 URL+请求族缓存到进程重启或渠道配置变更;全部协议不支持时 10 分钟后重新探测
- **自定义请求规则**(`custom_rules.go`):`channels.custom_request_rules` JSON;header remove/override/append、body remove/override(点分路径);`validateCustomRequestRules` 强制认证头黑名单 + 禁 CRLF
- **Codex 上游 Header 契约**(`codex_credentials.go`+`codex_upstream_websocket.go`):不走通用反代透传；HTTP 只接收 Codex 客户端白名单，静态 Key 与 OAuth 都只用 `Authorization: Bearer`，固定官方 `User-Agent`/`Originator`，认证与身份头在自定义规则后重建。原生 WebSocket 额外接收 turn state/timing/`OpenAI-Beta`，握手前删除 HTTP 传输头并归一 `OpenAI-Beta`、`Session_id`、`Conversation_id`；渠道自定义 Header 规则仍可显式增加非认证 Header
- **系统设置生效方式**(`config_service.go`+`admin_settings.go`):`LoadDefaults` 启动读一次进内存,多数设置写库后 `go triggerRestart()`,2 秒后请求进程重启。注册表中 `live:` 设置例外:当前 `model_alias_groups` 与 `auto_refresh_interval_seconds` 保存后立即生效;三个写入口共用 `applyLiveSettings`,先 `RefreshSetting`,再重建派生状态(模型映射使用不可变快照)。新增热生效项必须同时注册消费者与回归测试,不要只改前缀
- **引导期配置只能是环境变量**:`ConfigService` 依赖已建好的 `storage.Store`,所以建库阶段消费的配置不可能迁进系统设置(要读设置得先开库,要开库得先知道设置)。`SQLITE_PATH`/`SQLITE_JOURNAL_MODE`(拼 DSN,`factory.go:buildSQLiteDSN`)、`PIVOTFLOW_MYSQL`/`PIVOTFLOW_POSTGRES`/`PIVOTFLOW_ENABLE_SQLITE_REPLICA`/`PIVOTFLOW_SQLITE_LOG_DAYS`(`factory.go:NewStore`)全部属于这一类,保持环境变量;运行期策略才进系统设置
- **全局限额与冷却时长**(`server.go:loadServerRuntimeConfig`):均为系统设置,启动读一次,改后重启生效。`max_concurrency`(全局并发信号量,注意与 Auth Token、渠道的同名字段是三个独立层级)、`max_body_bytes`/`max_image_body_bytes`(Images 路径独立上限,同时约束 Responses WS 帧与 transcript,注入见 `newRequestBodyLimits`)、`cooldown_{auth,server,timeout,rate_limit,min,max}_seconds`(`loadCooldownSettings` 读出 `util.CooldownSettings`,经 `Store.ConfigureCooldown` 注入;下限>上限时整对回退默认)。旧 `PIVOTFLOW_MAX_CONCURRENCY`/`PIVOTFLOW_MAX_BODY_BYTES`/`PIVOTFLOW_COOLDOWN_*` 已废弃,仍设置时启动打 WARN
- **上游超时**(`server.go:loadProtocolTimeouts`):`upstream_first_byte_timeout`(0=禁用,仅流式)、`stream_timeout`(0=禁用,流式总时长)、`non_stream_timeout`(120s),首字节与非流式超时可按实际上游协议 `{protocol}_*` 覆盖;写回前调 `disableResponseWriteTimeout` 防 `WriteTimeout` 截断响应体
- **上游连接最长复用时间**(`upstream_connection_age.go`+`codex_upstream_websocket.go`):`upstream_connection_reuse_limit_seconds`(默认 0=不限制)统一约束直连及渠道代理池中的 HTTP/1.1、HTTP/2、WebSocket 物理连接；达到时限后不再接收新请求，空闲连接立即关闭，在途请求/turn 完成后关闭，新请求自动建连。原生 WS 新物理连接必须用 execution session 完整 transcript 重放，不能携带旧 socket 的 `previous_response_id`；计划轮换不记失败、不触发冷却
- **Anthropic thinking**:项目生成的 Anthropic 请求用 `thinking.type=adaptive` + `output_config.effort`;anyrouter `/v1/messages` 兜底补 adaptive 并归一旧 `enabled`;anyrouter 额外注入 `anthropic-beta: context-1m`
- **定时检测**(`channel_check_scheduler.go`):全局 `channel_check_interval_hours`(0=禁用,启动读一次,改后重启生效)+ 渠道级开关

## 发布与更新

- 发布必须使用仓库 Skill：Codex 调 `$pivotflow-release`，Claude Code 调 `/pivotflow-release`；实现目录暂保留 `.agents/skills/pivotflow-release/` 以兼容现有工具入口
- 无参数默认 Beta；只有显式 `stable` 才发稳定版。Tag 只允许 `vX.Y.Z-beta.N` / `vX.Y.Z`
- `.github/workflows/release.yml` 是唯一发布入口：Tag 先跑 `internal/...`、Web 验证、构建、lint，再生成多平台 Release 和 GHCR 镜像；Beta=`prerelease=true` 且不改 GitHub latest，镜像发布精确版本 Tag+`beta`；稳定版更新 GitHub latest，镜像发布精确版本 Tag+`latest`
- 官方容器直接打包同一 Release 的 Linux 二进制；`PIVOTFLOW_CONTAINER=1` 时不启动版本检查或进程内更新，`auto_update_*` 设置只读；稳定版/测试版分别通过 `latest`/`beta` 镜像标签切换
- 非容器部署的单一更新管理器负责前端版本提示；当前 `StartUpdateManager` 明确传入 `ApplyUpdates=false`,后台只读检查发布信息。默认 `auto_update_channel=stable`，`preview` 同时考虑稳定版/测试版并按 SemVer 取最高版本；`auto_update_interval_hours=0` 关闭全部版本检查

## 协议转换核心(改前必读)

- 同步/审查转换核心必须使用仓库 Skill：Codex 调 `$sync-cliproxy-core`，Claude Code 调 `/sync-cliproxy-core`；唯一源码在 `.agents/skills/`，`.claude/skills/` 只放发现链接
- `protocol/registry.go` 是唯一契约/调度边界:同协议原样透传;跨协议只走 `builtin/register.go` 注册的 12 个有向转换对
- `builtin/cliproxy_adapter.go` 只处理 PivotFlow 边界(输入验证、JSON/SSE 规范化、流帧封装);`protocol/cliproxy/` 只放从 CLIProxyAPI 同步的纯转换核心
- 不要把上游 auth/config/routing/cache/plugin/network 代码搬进来,也不要改成运行时 Go module 依赖;来源 commit、许可证和同步步骤以 `protocol/cliproxy/UPSTREAM.md` 为准
- `RequestTranslationError` 是客户端语义错误:代理返回 HTTP 400,不切渠道、不冷却;不要把无法表示的请求伪装成上游故障
- Registry 边界测试定义 PivotFlow 线协议契约,上游同步测试守住转换行为;改协议后先跑命令区快照审计,再跑全量 `internal/...`

## 计费与限额

- **成功结算**：`main` 监听前调用 `EnableDurableBilling`，独立 `billing-pending.db` 先保存事件，再由主库 `token_settlements` 回执原子去重和累计；确认主库及必要副本后 ACK。重试不再累加内存费用；失败统计仍可丢弃。journal 写失败阻止新 HTTP/WS turn，以 `billing_unavailable` 返回 503，在途事件继续结算。默认与 `SQLITE_PATH` 同目录，可用引导变量 `PIVOTFLOW_BILLING_JOURNAL_PATH` 指定；journal 与 receipts 不是缓存，禁止清理。限制与备份见 `docs/reliability.md`。

- **渠道倍率** `cost_multiplier`(0=本地免费,<0 归 1):本地估算 × 渠道倍率 = `effective_cost`,写日志时快照到 `logs.cost_multiplier` 避免历史污染;`CostSource=site_pricing` 已包含站点价目与用户组倍率,不得再乘渠道倍率
- **费用依据**：新日志保存 `cost_metadata_version=1` 的状态、计费方式和安全数值明细；同次转发的日志/令牌结算复用来源、金额与倍率快照。版本 0 或未知版本为 `historical_unknown`，禁止按当前模型目录重建历史或站点明细。来源均为价格估算，`cost_summary.reconciliation` 只为 `not_reconciled`；统计/日志/趋势共享费用筛选，原始 `cost_detail_json` 禁止直接输出。状态与覆盖契约见 `docs/cost-semantics.md`。
- **Auth Token**:`cost_*_microusd`(微美元整数避浮点);仅 2xx 累加费用,失败只计次;费用在请求完成后结算,准入为软限额,并发在途请求可放大超额窗口;`PIVOTFLOW_API_TOKENS` 启动预置
- **Auth Token 访问控制**(`model/auth_token.go`、`auth_service.go`):`allowed_models` 模型白名单(空=无限制;校验经 Server 注入的 `modelAliasCandidates` 按统一映射组展开,写组内任一名字即放行整组,与列表折叠同口径);`allowed_channel_ids`+`channel_restriction_mode`(`allow` 白名单/`deny` 黑名单,空 mode 视为 allow,空列表始终无限制),`ChannelRestriction.Allows` 封装极性,选择链路走 `FilterAllowedChannels`;`max_concurrency` 令牌级并发上限(0=无限),`acquireTokenConcurrencySlot` 获取槽位
- **渠道每日限额** `daily_cost_limit`(美元,0=无限);`CostCache` 内存缓存按天重置
- **定价细节**(service_tier 倍率、GPT-5.4/Qwen-Plus 分层降档、Gemini 长上下文翻倍、缓存读折扣/写乘数):读 `cost_calculator.go`

## 存储

- 存储相关配置全是引导期环境变量,不进系统设置(原因见"关键机制"引导期配置条)
- 模式:纯 SQLite(默认)/ 纯 MySQL(`PIVOTFLOW_MYSQL`)/ 纯 PostgreSQL(`PIVOTFLOW_POSTGRES`)/ 混合(主库 DSN + `PIVOTFLOW_ENABLE_SQLITE_REPLICA=1`)
- 互斥:`PIVOTFLOW_MYSQL` 与 `PIVOTFLOW_POSTGRES` 同时设置 → `log.Fatal`
- PG DSN:URL(`postgres://user:pass@host:5432/db?sslmode=disable`)或 libpq 关键字串;驱动 `pgx/stdlib`
- 混合数据流:写主库(MySQL/PG)→同步 SQLite,读 SQLite,日志先 SQLite 后异步主库;`PIVOTFLOW_SQLITE_LOG_DAYS` 默认 7
- 模型冷却状态:`channel_model_cooldowns(channel_id, model, cooldown_until)`;写主库后同步 SQLite,启动自动建表/恢复,渠道删除时级联清理
- URL 禁用状态(`channel_url_states` 表)双写,重启 `URLSelector.LoadDisabled` 回填

## 前端(Playwright MCP)

截图必须 `type:"jpeg"`,优先 `browser_snapshot`(文本),避免 `fullPage:true`。
