# P1 费用来源与数据语义设计

2026-11-22，Asia/Shanghai。按用户已批准的路线和明确“继续下一阶段”执行；日常实现取舍由代理完成，继承此前不提交、不推送、不部署和保留生产数据的工作方式。

## 目标与现状

让同一笔请求费用在日志、概览、数据面板和令牌页面有一致且可追溯的解释。当前 `cost_source` 已保存 site_pricing/local_estimate，且站点价包含组倍率，不再乘本地渠道倍率。它们都是价格表估算，源码与页面“实扣/上游计算”的文案不能代表已对账。

当前 `logCostStatus` 用今天的模型目录解释历史金额，正金额优先于零倍率判断；站点记录也可能展示按今天的本地价格重算的明细。统计把缺失数值当零汇总，无法看出多少成功请求未能完整估算。

## 方案与范围

选择持久化请求发生时的费用解释快照，加一条复用日志筛选的聚合查询。只改页面文案会继续丢失历史依据；引入完整上游账单对账需要账单身份、币种和权限接口，本阶段没有对应数据。

覆盖普通代理和已有测试/巡检日志生成、SQL 持久化及复制、管理员和下游令牌的安全日志投影、概览/数据面板汇总、来源/状态/计费方式筛选与令牌记账文案。已有财务回执和用量累计不得重新计算或迁移；不更改成功结算幂等、限额软约束、路由策略、价格目录数值或转换核心。余额观察不用于推断单请求扣费，不新增外部调用、依赖或手工账单确认入口。

## 请求费用契约

`model.LogEntry` 新增四项：`CostMetadataVersion int`（JSON `cost_metadata_version`，新行 1、旧行 0）、`CostStatus string`（`cost_status`）、`CostBasis string`（`cost_basis`）、`CostDetailJSON string`（仅持久化，不直接输出）。SQL 列为 `cost_metadata_version INTEGER NOT NULL DEFAULT 0`、`cost_status VARCHAR(32) NOT NULL DEFAULT ''`、`cost_basis VARCHAR(16) NOT NULL DEFAULT ''`、`cost_detail_json TEXT NOT NULL DEFAULT ''`。迁移只加列，不补算旧金额或假造旧证据。

来源继续使用 `site_pricing`、`local_estimate`，空/无效旧来源不猜成站点。本地倍率 0 仅令本地有效记账免费；站点估算继续不乘本地倍率。站点基础价加本地工具费保留现有金额规则，但计费方式为 mixed，并在明细明确工具是本地估算。

状态允许列表：

| 代码 | 含义 |
| --- | --- |
| estimated | 具有价格依据和必要用量或按次依据，金额是完整估算 |
| zero_price | 请求发生时可用的价目与用量计算结果为零；不是未知或已对账 |
| local_free | 本地渠道倍率明确为零，有效记账为零；不表示上游免费 |
| usage_missing | 按 Token 价格存在，但没有可计费的用量；零金额不能解释为免费 |
| unpriced_model | 当时没有可用模型价格，不能估算完整金额 |
| partial_estimate | 工具等部分金额已知，模型金额仍缺少价格或用量；显示部分金额 |
| invalid | 数量或金额无效、负数或非有限值；不返回非有限 JSON 金额 |
| historical_unknown | 旧行或未知元数据版本，金额保留但历史依据不完整；不按当前目录重解释 |
| not_charged | 非 2xx 结果，网关未按成功费用记账；不推断上游是否收费 |

计费方式 `token|per_call|mixed|unknown`：按次模型无需 Token 数即可估算；有模型与工具等组合费用时 mixed。mixed 描述组成而非完整性：模型金额未知、仅工具费已知时仍为 mixed，配合 partial_estimate 只展示已知部分。新增计算快照保持价目查找、tier/cache/service_tier 和 fixed-per-request 算法数值兼容。缺少 split cache 写入量时继续采用既有合计→5m 规则。缺少用量的判定使用当前 parser 可用的计费计数，不把全零用量解释成收费 Token 证据。

成功请求写入时按以下顺序冻结状态：数量、金额、倍率非有限或金额超出可结算范围先为 invalid；本地来源且倍率 0 为 local_free；模型估算不完整且工具费为正为 partial_estimate；没有模型价格为 unpriced_model；有 Token 价格但没有计费用量为 usage_missing；完整估算金额为 0 为 zero_price；其余完整金额为 estimated。站点来源永不因本地倍率 0 判为 local_free。按次依据不要求 Token 用量；旧内部 SiteCost 兼容调用仅有正金额时保留该金额依据、计费方式为 unknown，不能把无用量的零金额判为零价。local_free 表示本地有效记账可知为 0，覆盖说明仍不是上游费用覆盖。

混合费用先按既有公式得到 `Cost = model_cost_usd + tool_cost_usd`；站点来源的 `effective_cost = Cost`，本地及历史未知来源的 `effective_cost = Cost × 请求时倍率`（负倍率沿用 1、零倍率为 0）。站点模型费包含站点组倍率，工具明细仍单列本地估算；不通过拆明细改变有效金额。快照同时冻结本地倍率，日志与结算不能在两个消费环节之间重新取倍率或替换来源。

核心模型提供 `model.LogCostStatus(*LogEntry) string` 和 `model.LogCostBasis(*LogEntry) string`，纯函数不依赖当前价格目录；非 2xx 优先 not_charged，版本 1 的合法持久化状态优先，其他成功行 historical_unknown。日志读取与 SQL 状态表达式必须同义。

`cost_detail_json` 保存已计算的 `{model_cost_usd,tool_cost_usd,currency:"USD",breakdown?:StandardCostBreakdown}`，只包含安全数值。本地 Token 明细按当时计算值冻结；有可靠站点 Token 单价时冻结站点明细；不能解释明细时留空，不能用当前本地公开价冒充站点或历史明细。公开投影增加 `effective_cost` 和安全 `cost_details`，兼容 `cost_breakdown` 仅展示已冻结且适用的 Token 明细。不返回原始 JSON、账户信息、价格表、凭据、数据库错误或隐藏渠道路由。

这里的单次明细允许管理员与拥有该日志的下游令牌看到计费数量、该请求采用的单价与金额。完整站点价目表、组/账户映射与其他请求的价格不属于该 DTO；原始 JSON 的未知属性会在类型化读取中丢弃。

普通代理对同一次成功转发复用费用解析快照，使日志与令牌结算采用同一已解析金额/来源；快照随转发结果释放，不跨请求缓存。保留 `SiteCost *float64` 的旧内部兼容签名，生产解析同时保存站点计费方式与可用明细。未知/部分金额不因此放弃既有成功用量累计或重复收取。

## 汇总与筛选契约

新增 `model.CostSummary` 和 `Store.GetCostSummary(ctx, since, until, filter *model.LogFilter) (*model.CostSummary,error)`。沿用相同日志时间单位、channel_id>0、成功 2xx 范围及筛选解析；下游令牌身份强制覆盖 AuthTokenID，不因费用筛选扩大权限。该查询从 SQL 聚合，不加载完整日志，不连上游。

汇总 JSON 字段：`version:1`、`currency:"USD"`、`reconciliation:"not_reconciled"`、`successful_requests`、`estimated_requests`、`zero_requests`、`local_free_requests`、`partial_requests`、`unknown_requests`、`historical_requests`、`site_pricing_requests`、`local_estimate_requests`、`mixed_requests`、`recorded_cost_usd`、`estimated_cost_usd`、`partial_cost_usd`、`historical_cost_usd`。计数均 int64，金额 float64；empty 范围返回完整零计数结构。

主状态计数可拆为 estimated + zero + partial + other unknown；unknown 包含 partial 和 historical，不重复计入覆盖率。local_free 是 zero 的子集；historical 是 unknown 的子集。金额按既有有效费用规则：recorded 包括安全的历史与部分金额，estimated 只包含完整 estimated/zero/local_free，partial 与 historical 单列；不把 unknown 的缺失项纳入总额并称“完整实际消费”。非法金额不给 JSON 产生 NaN/Inf。reconciliation 永远 not_reconciled；当前没有逐请求已确认账单数据，因此不能展示“已确认 0 美元”假结论。

精确恒等式为 `successful = estimated + zero + unknown`、`unknown = partial + historical + other_unknown`；other_unknown 为剩余 usage_missing/unpriced_model/invalid，不另设 JSON 字段。partial 与 historical 按状态互斥，local_free 包含在 zero 中。来源计数只匹配显式 site_pricing/local_estimate，历史空/无效来源都不匹配这两个筛选；mixed 按 basis 独立计数，可与站点/本地来源同时计入，三项不能相加当作成功总数。successful 按成功日志行计数，不按 request_id 去重；客户端最终请求数仍由既有请求历史接口统计。筛选 not_charged 后成功范围为空，返回零结构和“暂无成功请求”，不能显示覆盖完整。

`DashboardSnapshot.CostSummary *CostSummary`（JSON `cost_summary`）与 `/admin/stats` 响应使用该查询；复用 StatsCache TTL/容量并把所有筛选（包括 search、request_id 和新费用筛选）纳入缓存键。失败时返回明确读取失败，不把汇总失败伪装成无未知请求。

`LogFilter` 和控制台 LogFilters/StatsFilters 新增 `CostSource`、`CostStatus`、`CostBasis`；API 参数 `cost_source`、`cost_status`、`cost_basis`。SQL 所有日志/计数/统计/趋势使用同一参数化表达式，历史状态由版本推导。未知或不支持值过滤为无匹配，不去掉限制；用户字符串不能拼入 SQL。费用滤镜不影响原有来源/状态/时间/模型/渠道/令牌条件。

`cost_basis=unknown` 是明确允许的值，用来查询缺少计费方式依据的行；仅允许列表以外的字符串无匹配。`/dashboard/summary` 也提供同一 cost_summary，沿用强制令牌身份与全部筛选。既有旧版本的首次有效费用回填迁移保持原机制；本阶段新增四列不会重跑已有标记的回填，也不改写累计或财务回执。

## 控制台

沿用现有视觉系统，不新增重复统计页。共享 `costSemantics.ts`（纯函数与测试）和 `CostSummaryPanel.tsx`（局部样式）供 Logs/Dashboard/Stats/Tokens 使用。

日志示例：`$0.0012 · 本地公开价估算 · 按 Token`、`— · 未获取计费用量`、`$0.0040 · 部分估算`、`$0.0000 · 本地记账免费（上游费用未确认）`、`$0.0020 · 历史记录，依据不完整`。历史零、usage_missing/unpriced/invalid 的未知值展示 —；已知零价保留 0。失败显示未按成功请求记账。来源与状态均是文字，不能只用颜色区分。

汇总示例：`已记录费用 $…`，旁边 `完整估算 N/M · K 条未知/部分 · 尚未与上游账单对账`，可展开显示站点/本地估算、零价与未知原因。筛选用相同参数传给日志、统计与概览，切换筛选重置页码，迟到响应不能落在新筛选标签下。工具卡/趋势/分布金额说明是已记录费用，不称实际扣费；令牌累计是本地记账/软限额。

未知版本/旧后端缺少 summary 时页面显示“费用依据未提供”，不猜成覆盖完整。最小宽度 320px、390px 浅色、桌面深色、键盘筛选/展开都验收。所有截图合成数据、JPEG、可见视口；不启动生产程序。

## 验收与收尾

TDD 覆盖：零倍率且正标准金额、价格为零与缺用量、未定价仅工具费、按次无 Token、缓存拆分、非法金额、历史不随当前价格变更、site 与 local 相同费用不重复乘倍率、同请求日志/结算一致、混合存储复制与重新打开、三种 SQL dialect 的加列、过滤/缓存与令牌范围一致、summary 与记录和筛选一致。

完整 Go internal suite、lint、前端 typecheck/tests/build，随后 Go embed build；Vite 与 Go embed 构建不并发。独立复核本阶段与当前主目录基线，重要问题修复后复验。SHA256 与 ZIP 备份核验后本地整合，主目录定向回归、合成浏览器、文档链接与构建通过，清理本轮 worktree/分支/junction/验证程序并压缩日志，保护文件最后核验。真实 MySQL/PostgreSQL、race detector、真实账单与生产部署不作为本机通过声明。
