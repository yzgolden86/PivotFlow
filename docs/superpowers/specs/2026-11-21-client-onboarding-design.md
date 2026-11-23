# 新手接入引导与可验证的客户端配置

用户已授权按研究路线连续实施。本设计沿用现有 React 控制台与站点/账号/渠道/令牌概念；2026-11-21 核对当前源码和官方客户端契约后确定。

## 目标与边界

在“客户端接入”页串联来源、发现、渠道、下游令牌与首个真实代理请求。来源维护仍使用现有站点/账号表单、OAuth 导入、模型测试和渠道同步，不静默覆盖手工渠道。直接管理渠道的用户可跳过站点账号路径。

提供 Codex、Claude Code、Gemini CLI、OpenAI Python SDK 四种模板，支持 PowerShell 7+/Bash 环境变量、PowerShell HTTP 验证与对应协议的 Bash cURL 验证命令。PowerShell 禁止原生头语法重解释并停止失败；Bash 通过不展开 heredoc/标准输入保持 UTF-8。用户复制或下载后自行合并配置；本阶段不写入用户客户端配置文件。

不新增依赖、数据库表、后台计费测试接口或自动模型调用。保留此前未提交的实现，基于当前文件 SHA256 快照隔离实施并核验集成。不得运行生产程序或使用生产账号、凭证、数据库。

## 交互与状态

- 在导航、全局搜索和令牌页提供入口；接入页显示五步操作与真实资产状态。提示只表述已观测事实，资产数量不等同于连接成功。
- 接入页复用现有令牌创建表单。选择已有令牌时，用户点击读取后才恢复完整下游令牌；不可恢复、已停用、过期或已达费用限额的令牌显示原因。
- 完整令牌只在当前页面内存中使用；URL 只携带令牌 ID，配置预览默认遮挡令牌。复制和下载使用完整内容并明确显示其包含令牌。离开页面中止等待，不保存秘密到 storage/history。
- 通过所选令牌获取当前网关的模型清单，按所选客户端协议和服务端模型/渠道/协议权限过滤。禁止用站点发现列表代替实际可访问模型列表。
- 模板地址可填写 HTTP(S) 根地址，自动移除末尾 `/v1` 或 `/v1beta`，保留反向代理路径前缀；拒绝认证信息、query、fragment、控制字符和 API 端点地址。地址、模型、令牌分别按 JSON、TOML、Python、Bash、PowerShell 的语法编码。
- 浏览器只向当前同源网关 `/v1`、`/v1beta` 发起请求，使用下游令牌、`credentials: omit`、`redirect: error`。若客户端地址不同，结果明确只证明当前网关，提供在客户端设备执行的终端命令。
- 联测需显式点击，走正常权限、路由、故障切换、结算与请求历史。页面不自动重发；正常代理内部重试仍生效。取消按钮标为“停止等待”，超时/网络中断不声称模型未执行。
- 只对结构正确、终态成功且有可验证文本的响应显示成功；HTTP 200 错误、空输出、incomplete、截断、过大响应、非 JSON 均不得显示成功。失败说明来自本地分类，不反射含凭证的上游错误正文。
- 保存安全的 `X-PivotFlow-Request-Id`，成功和失败都可进入同一请求历史；认证前失败或网络未收到 ID 时可进入历史列表，说明观察限制。
- 禁用联测期间的选择变化；模型读取、令牌恢复与资产刷新采用取消/序号防止迟到响应污染新选择。

## 客户端契约

- Codex：`~/.codex/config.toml` 自定义 `model_providers.pivotflow`，`base_url=<root>/v1`、`env_key=PIVOTFLOW_API_KEY`、`wire_api=responses`、`requires_openai_auth=false`，模型为网关原始别名。
- Claude Code：`~/.claude/settings.json` 的 `env` 设置 `ANTHROPIC_BASE_URL=<root>`、`ANTHROPIC_AUTH_TOKEN` 与选定模型；默认 Opus/Fable/Sonnet/Haiku 与默认子代理模型均使用所选别名。网关不会由此承诺模型具有 Claude 专有工具、thinking 或完整代理能力。
- Gemini CLI：`~/.gemini/settings.json` 选择 `gemini-api-key` 认证及模型；环境设置 `GEMINI_API_KEY`、`GOOGLE_GEMINI_BASE_URL=<root>`、`GOOGLE_GENAI_API_VERSION=v1beta`。按官方文档限制非本机地址为 HTTPS，本机仅 localhost/127.0.0.1/[::1] 可使用 HTTP。不生成 Vertex/Google 登录路径。
- OpenAI Python：显式 `OpenAI(api_key=os.environ['PIVOTFLOW_API_KEY'], base_url=<root>/v1)`，走 Chat Completions；不混用管理员登录令牌。

官方依据和访问日期写入用户文档；抓取原文保存在独立验证目录。客户端本体未运行时不声称端到端 CLI 已验证。

## 后端最小调整

现有 `/v1/models` 根据 User-Agent/anthropic-version 推断协议。浏览器无法设置 Codex User-Agent，因此增加允许列表内的显式 `protocol=openai|anthropic|codex` 查询参数，优先于头部推断；未知参数返回 400。原请求不带参数时契约不变，所有视图仍执行令牌模型、协议、渠道限制。Gemini 继续使用 `/v1beta/models`。

## 验收

1. 从空资产页可连续进入现有添加、发现/直测、同步和令牌创建；已有手工渠道可直接接入。
2. 模板的地址、JSON/TOML/Python/脚本编码与官方契约可复核；只有下游令牌进入配置。
3. 四种协议使用合成上游经过真实 Go 代理成功调用；权限拒绝、失败与请求 ID 可验证。
4. 单元测试覆盖错误终态、秘密处理、地址归一、权限与迟到响应；完整后端测试、lint、前端测试/类型/构建通过。
5. 合成浏览器验证空安装、创建/恢复令牌、配置复制/下载、同源请求、联测成功/失败/停止等待、键盘、320/390px 和深浅主题。
