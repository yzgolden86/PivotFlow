# 接入配置与客户端验证

“接入配置”为 Codex、Claude Code、Gemini CLI 和 OpenAI Python SDK 生成网关配置，并提供一次真实调用验证。配置可复制或下载，由用户自行合并；页面不会自动安装客户端。模板字段按 2026-11-21 访问的官方文档核对。

## 从第一次登录开始

1. 在导航中打开“接入配置”。页面默认显示令牌、模型与配置操作；需要完整步骤时展开“接入清单”。配置条件只计入已启用来源、当前模型事实和具备模型、地址及凭证来源的渠道；读取失败显示“未确认”，这些条件不表示上游已测试成功。
2. 选择来源路径。使用站点账号时，进入“站点与账号”添加站点和账号，再验证凭证、刷新模型；已有 API Key 或 OAuth 时，在“渠道分发”添加渠道或导入 OAuth。已有手工渠道可以直接继续。
3. 在“模型测试”执行账号直测或渠道测试，确认上游基本可用。账号模型发现以后，在“渠道分发”使用现有站点渠道同步；手工配置渠道仍按原来的编辑流程管理。相关页面会显示返回接入引导的链接。
4. 回到接入页，创建一个客户端专用访问令牌，或选择现有令牌并点击“读取现有令牌”。创建表单沿用模型、渠道、额度、并发和到期限制。不可恢复的历史令牌需要重新创建；已停用、过期或用完额度的令牌不能继续生成可用配置。
5. 选择客户端、访问地址、终端环境和模型，合并配置，再点击“发送验证请求”。这是一次正常的模型调用。成功或失败后都可进入请求历史；收到请求 ID 时直接查看对应请求。

令牌页的每一行有“配置客户端”入口；创建成功弹窗也可进入配置页。入口只传递令牌 ID，完整令牌不会放进 URL 或浏览器 history。

## 模型与令牌权限

接入页使用选定的**下游访问令牌**读取当前网关模型列表，不用站点发现列表或管理员会话代替下游身份。模型列表会应用该令牌的模型、协议、渠道限制，并排除停用模型与不符合协议声明/转换策略的渠道。

OpenAI、Codex 和 Anthropic 使用 `/v1/models?protocol=openai|codex|anthropic`；Gemini 使用 `/v1beta/models`。不带查询参数的 `/v1/models` 保持原来的头部推断；显式查询优先于头部，未知、空或重复视图返回 400。

模型可见说明存在配置上的候选，不保证账号额度、Key 健康、网络、工具调用、流式或长会话已经验证。更新渠道或令牌限制后，点击“刷新接入状态”或“刷新可用模型”。无法恢复的历史令牌可在令牌页另行管理。

## 地址与四种模板

填写客户端设备能访问的网关根地址，例如 `https://gateway.example` 或 `https://gateway.example/pivotflow`。也可填写末尾带 `/v1` 或 `/v1beta` 的地址，生成器会移除这一层，再按客户端契约拼接。路径前缀会保留；认证信息、query、fragment、重复 API 路径、控制台地址和具体请求端点会被拒绝。

| 客户端 | 合并位置 | 地址与认证 | 模型与协议 |
| --- | --- | --- | --- |
| Codex | `~/.codex/config.toml` | `model_providers.pivotflow.base_url=<root>/v1`；`env_key=PIVOTFLOW_API_KEY` | `model_provider=pivotflow`、选定模型、`wire_api=responses`、`requires_openai_auth=false` |
| Claude Code | `~/.claude/settings.json` 的 `env` | `ANTHROPIC_BASE_URL=<root>`；`ANTHROPIC_AUTH_TOKEN=<下游令牌>` | `ANTHROPIC_MODEL`，默认 Opus/Fable/Sonnet/Haiku 与默认子代理均使用所选模型；Messages |
| Gemini CLI | `~/.gemini/settings.json`，加启动终端环境变量 | `GEMINI_API_KEY`、`GOOGLE_GEMINI_BASE_URL=<root>`、`GOOGLE_GENAI_API_VERSION=v1beta` | `security.auth.selectedType=gemini-api-key`、`model.name`；GenerateContent |
| OpenAI Python SDK | 下载的 `pivotflow_openai.py` | 显式读取 `PIVOTFLOW_API_KEY`；`OpenAI(base_url=<root>/v1)` | 选定模型、Chat Completions；示例关闭 SDK 自动重试，等待预算 60 秒 |

Gemini CLI 的官方文档要求非本机地址使用 HTTPS；HTTP 只接受 `localhost`、`127.0.0.1` 或 `[::1]`。生成器会先校验这个条件。其余客户端可以生成 HTTP 或 HTTPS 地址，地址是否可达需要实际验证。

Codex 的顶层 `model` 与 `model_provider` 必须放在 TOML 顶层，provider 表放在对应表位置；重复表不能直接追加。当前 Codex 文档说明 provider 配置是本机配置，项目级配置不能覆盖这些身份与地址字段，因此模板标注用户级路径。

Claude Code 的项目或管理设置、显式模型参数，以及子代理的独立配置仍可能覆盖默认值。把一个模型别名映射到这些默认模型，不意味着该模型支持 Claude 的全部代理能力。

Gemini 模板选择 API key 认证，避免已有 Google 登录选择导致自定义地址未被使用。这里不生成 Vertex AI 或 Google 登录配置。

## 复制和下载

三类内容分别是配置文件、环境变量与启动、终端验证。选择 PowerShell 或 Bash/zsh 后，所有脚本值按相应 shell 的字面量规则编码；JSON、TOML 和 Python 示例分别使用对应字符串语法。模型别名不会被改成小写、去掉供应商前缀或替换。

默认预览遮挡令牌；“复制内容”和“下载文件”取得完整内容。包含秘密的文件会明确标注“包含下游令牌”。Codex、Gemini 配置文件与 Python 示例本身不写入令牌，令牌放在环境变量内容中；Claude 的 `env` 配置包含令牌。

页面不修改客户端文件、用户配置、shell profile 或系统环境。合并前保留已有配置，不直接用下载文件覆盖包含其他 provider、MCP 或工具配置的文件。

完整令牌只在本页内存中使用；离开页面会停止等待并丢弃页面中的完整令牌。复制的剪贴板内容和用户下载的文件属于用户明确保存的副本，应按访问令牌保护。

## 浏览器联测和终端验证

浏览器只请求**当前同源网关**，认证使用下游令牌，禁止携带浏览器 cookie 或跟随重定向。页面不会把令牌发给填写的外部地址。

若“客户端访问地址”与当前浏览器地址不同，页面明确说明：联测成功只证明当前网关，外部地址仍需在客户端设备执行“终端验证”。这适用于反向代理前缀、外部域名、远程设备或不同端口，也防止把一个可用的本机网关误当作外部部署已验证。

验证请求按所选客户端使用原生入口：

| 协议 | 请求入口 | 认证 |
| --- | --- | --- |
| Codex | `POST /v1/responses` | `Authorization: Bearer` |
| Anthropic | `POST /v1/messages`，`anthropic-version: 2023-06-01` | `Authorization: Bearer` |
| Gemini | `POST /v1beta/models/{model}:generateContent` | `x-goog-api-key` |
| OpenAI | `POST /v1/chat/completions` | `Authorization: Bearer` |

提示只要求回复 OK，最多 256 输出 token。浏览器等待最多 60 秒，读取模型列表上限 2 MiB，联测响应上限 256 KiB；页面不会自动重发。代理内部仍按正常故障切换规则尝试上游，正常权限、费用结算、冷却、活动请求和请求历史都生效。

PowerShell 7+ 验证使用 `Invoke-WebRequest` 和 UTF-8 请求体，关闭额外的认证头语法推断，并在错误时停止，避免特殊令牌被改写或失败后继续输出；生成前仍禁止令牌中的控制字符。Bash 使用支持 `--fail-with-body` 的 cURL 7.76+，通过不展开的 heredoc 与标准输入发送 JSON，避免 Git Bash 下的 Windows cURL 将中文请求体转成系统编码。终端命令会输出响应以及可用的请求 ID。

## 结果如何理解

| 结果 | 含义与下一步 |
| --- | --- |
| 当前网关已返回有效响应 | 协议结构、完成状态和有效文本通过本次真实调用；再在客户端验证工具、流式与会话功能 |
| 认证失败 | 检查所选下游令牌是否停用、过期、删除，再重新读取；不会因此清理管理员登录 |
| 权限不足或模型不可用 | 查看令牌模型/协议/渠道范围、启用渠道和 URL 声明，刷新模型列表 |
| 限流或额度不足 | 查看令牌、渠道限额和本次实际尝试；等待或调整以后自行决定是否重发 |
| HTTP 200 但输出未完成、错误或为空 | 不算验证成功；256 token 预算可能不足，尤其是推理模型，查看请求记录并在客户端进一步验证 |
| 停止等待、超时或网络中断 | 结果尚未确认；请求可能已经执行并计费，先查看历史再决定是否重发 |
| HTML、无法解析或过大的响应 | 检查反向代理、登录拦截和响应完整性；不算验证成功 |

“停止等待”不是撤销已经执行的模型调用。认证在代理处理前失败时可能没有请求 ID；连接在响应头到达前中断时也无法获得 ID。请求历史是有界的观察记录，缺少记录同样不能证明没有执行。详见[请求身份与尝试历史](request-history.md)和[可靠结算与流式恢复](reliability.md)。

## 模板依据与验证范围

- [Codex 配置参考](https://developers.openai.com/codex/config-reference/)：provider 字段、环境认证、Responses 和本机配置层级。
- [Claude Code 网关连接](https://code.claude.com/docs/en/llm-gateway-connect)与[模型配置](https://code.claude.com/docs/en/model-config)：根地址、Bearer 认证、模型别名和默认子代理。
- [Gemini CLI 认证](https://geminicli.com/docs/get-started/authentication/)与[配置](https://geminicli.com/docs/get-started/configuration/)：API key 认证、base URL、API version 和 HTTPS 条件；[客户端实现](https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/core/contentGenerator.ts)用于交叉核对。
- [OpenAI Python 官方仓库](https://github.com/openai/openai-python)：显式 api_key/base_url、Chat Completions、重试与超时。

本阶段用合成数据验证真实 Go 代理链路和浏览器交互，并解析配置及验证脚本；没有使用生产账号运行 Codex、Claude Code 或 Gemini CLI，也不将一次文本请求描述为完整客户端端到端兼容认证。检查结果见[阶段验证记录](superpowers/plans/2026-11-21-client-onboarding-verification.md)。
