# Octopus 功能矩阵与源码证据

基准：`bestruirui/octopus@8aa58b0340ba2de58ce74fbfacd32597c44842a2`（v0.13.13）。

状态含义：
- **已实现**：后端模型、处理路径或 UI 源码可直接证明。
- **部分**：存在能力，但缺关键闭环或受明确边界限制。
- **未实现**：当前固定提交源码未找到；不能因 README 或旧截图写成功。

| 主题 | 状态 | 当前实现与边界 | 固定源码 |
|---|---|---|---|
| 渠道配置 | 已实现 | Channel 记录上游 URL、协议入口/路径、代理、Header、参数覆盖和过滤；渠道有启用状态。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/channel.go |
| 多 Key | 已实现 | v0.13.0 起一个 Channel 下多个 ChannelKey；Key 可命名、独立启用、独立探测并独立统计。 | https://github.com/bestruirui/octopus/releases/tag/v0.13.0 ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/channel.go |
| 模型映射 | 已实现 | ChannelModel 保存上游模型名；Group 用对外模型名接收客户端请求；一个 Grant 连接模型、Key 和协议能力。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/channel.go |
| 路由最小单位 | 已实现 | ChannelGrant 是“ChannelModel × ChannelKey × 协议位”的组合，GroupItem 引用 Grant。公开名称/Key 能力能精细到单品。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/channel.go ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/group.go |
| 协议 | 已实现（三协议） | 当前源码只有 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages 三种协议位。Gemini 已在 v0.13.0 删除。 | https://github.com/bestruirui/octopus/releases/tag/v0.13.0 ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/channel.go |
| Gemini | 未实现 | 当前主线后端无 Gemini 协议位；README 若有旧行属于文档滞后，以源码和 v0.13.0 release 为准。 | https://github.com/bestruirui/octopus/releases/tag/v0.13.0 |
| Images | 未实现 | 当前协议枚举中没有 Images Relay。不能从旧 Hureru 包或旧 README 推断上游已有。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/channel.go |
| WebSocket | 未实现 | Responses 配置明确 `supports_websockets=false`；当前 relay 是 HTTP 请求和 SSE 流。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/protocol.go |
| 分组模式 | 仅两种 | `GroupMode` 只有 `manual` 和 `failover`，请求绑定 `oneof=manual failover`。没有轮询、随机、加权、成本优先、健康评分或嵌套分组。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/group.go |
| 优先级 | 已实现（顺序） | GroupItem.Priority 决定界面顺序与 failover 切换顺序；提交成员数组的顺序即优先级顺序。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/group.go |
| manual | 已实现（固定成员） | 人工选择 ActiveItemID，只走该成员；manual 失败不进入冷却，也不自动切换，主循环按重试间隔继续等待/重试直到客户端取消。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/handler.go ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/route.go |
| failover | 已实现（有边界） | 按 Priority 选首个未冷却成员；单个成员达失败阈值后冷却并切换。所有成员都在冷却时 `pickGroupItem` 返回零值，请求等待后重试，不选择最早恢复成员。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/route.go ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/handler.go |
| 健康调度 | 部分 | 有失败阈值、cooling deadline、单恢复探测和成功防抖，但没有公开的成功率/延迟评分模型。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/route.go |
| 恢复探测 | 已实现（进程内） | 冷却到期后只放行一个 probe；同一分组同时只保留一个 ProbeItemID；成功清冷却，失败重新冷却。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/route.go |
| 亲和 | 已实现（进程内） | failover 切换到备用并首次成功后，RelayConfig.MemberAffinitySeconds 内继续使用当前成员，避免立刻被高优先级旧成员抢回。manual 不使用。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/route.go |
| 冷却持久化 | 未实现 | RouteState 的 current item、cooldown、probe、affinity 存在进程内 `routes` map；重启后恢复默认。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/route.go |
| 低成本路由 | 未实现 | 价格用于统计与费用计算；选路仅按 Priority、冷却、亲和，不按价格或成本大小排序。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/route.go ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/price/price.go |
| 权重/轮询/随机 | 未实现 | 当前 GroupMode 枚举及其 Gin binding 足够证明。固定提交的旧 screenshot 显示四按钮，是素材与实现不同步，不是路由证据。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/desktop-group.png ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/group.go |
| 流式 failover 边界 | 已实现 | synthetic start/首个含语义输出事件前，可对该轮上游失败做成员切换；首帧对下游提交后不再回滚或换成员。请求状态有 committed 标志。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/handler.go ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/state.go |
| 流式终止校验 | 已实现（部分协议事件） | 严格检查 Responses 的 failed/incomplete/cancelled/error，Anthropic error，Chat SSE error；synthetic 启动事件先缓存。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/upstream.go |
| 通用错误分类 | 部分 | 处理循环对首个未提交上游错误和成员失败做重试/切换，但没有 PivotFlow 所记录的按 401/403、模型 404、SSE 断流、DNS 等逐类策略和退避。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/handler.go |
| 使用量统计 | 已实现 | 记录总、日、小时、渠道、渠道模型、渠道 Key、下游 API Key；批量落库，正常退出会 flush。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/op/stats.go |
| 价格/费用 | 已实现 | 价格自 models.dev 载入，按模型系列与 text output 过滤；自定义价格优先。费用按 input/output/cache read/cache write 计算，cache 金额并入 input cost，UI 另显 cache tokens/rate。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/price/price.go ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/op/stats.go |
| API Key 配额 | 部分 | 下游 Key 有启用、过期、最大费用、模型/分组白名单。费用在请求前检查，但统计异步，可能多放行一个超限请求。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/apikey.go ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/op/apikey.go |
| 下游认证 | 已实现 | x-api-key 或 Bearer 均可标识下游 Key。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/server/middleware/auth.go |
| 管理认证 | 部分/高风险 | Cookie JWT，HS256，secret 由用户名+密码派生；源码设置 `Secure=false, HttpOnly=false`。部署应 TLS 反代并防止脚本注入。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/server/auth/auth.go ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/server/handlers/user.go |
| 上游密钥存储 | 部分/高风险 | 模型中以字符串字段保存，未找到字段级加密封装。备份会导出凭证，需按高敏文件处理。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/channel.go ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/backup.go |
| 会话/缓存 | 部分 | 有 failover 成员亲和与用量 cache token 统计；没有发现跨请求 prompt cache 保持、WebSocket execution session 或会话 sticky routing。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/route.go |
| 请求日志 | 已实现（内存态） | 实时状态最多保留当前请求，已结束请求最多 50 个；大量请求统计进数据库，但请求详情不是长期审计数据库。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/relay/state.go |
| SQL 存储 | 已实现 | SQLite、MySQL、PostgreSQL 三套数据库；启动迁移并记录迁移版本。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/go.mod ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/db/migrate/migrate.go |
| JSON 备份 | 已实现（合并语义） | dump version 5 导出渠道、Key、模型、Grant、分组、价格、API Key、设置、统计；导入由 do-nothing/upsert 混合，不是“清空后无损覆盖”。凭证也在备份中。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/op/backup.go ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/model/backup.go |
| WebDAV 定时同步 | 未实现 | 当前主线源码和 README 未找到 WebDAV 后台同步；PivotFlow 自己的 WebDAV 是既有能力，不能反写为 Octopus 已有。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/README.md |
| 部署 | 已实现 | `octopus start`；默认 `0.0.0.0:8080`；配置 `data/config.json`、SQLite `data/data.db`。Docker 挂载 `/app/data`，入口默认 PUID/PGID=0。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/cmd/start.go ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/scripts/dockerfile/entrypoint.sh |
| 首次账户 | 已实现但危险默认 | 默认 `admin/admin`，可通过 UI 或 `octopus passwd` 修改；CLI 修改后要求重启。生产部署必须首日改密。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/cmd/passwd.go ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/README.md |
| 自动更新 | 部分/供应链风险 | 从 GitHub latest stable 下载 zip，解压、替换当前可执行文件并重启。更新器源码未显示下载物与 release SHA/签名绑定校验；GitHub 资产 API 已有 digest，但更新流程是否利用需另行验证。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/update/core.go ；https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/internal/server/handlers/update.go |
| 发布流水线 | 已实现（强副作用） | workflow 在 `main.go` push 时触发，创建并推送 tag，再构建发布；没有在该发布 job 中看到测试步骤。`prerelease:false` 硬编码。 | https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/.github/workflows/release.yaml |

## 不应从 README 外推的内容

- “Load Balancing”、轮询、随机、加权、低成本优先等词若残留在 README、manifest 或公开图片中，不等于当前 `GroupMode` 有这些模式。
- README 的 Gemini、Images、WebSocket、站点管理等旧能力可能来自历史或 fork 版本；固定提交源码是功能边界的最高证据。
- `v0.13.13` 是安全依赖升级，不应包装成新路由功能发布。