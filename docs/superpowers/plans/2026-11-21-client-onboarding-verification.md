# P1 新手接入引导与客户端配置验证

2026-11-21，Asia/Shanghai。功能契约见 [客户端接入](../../client-connection.md)，设计见 [本阶段设计](../specs/2026-11-21-client-onboarding-design.md)，工作分解见 [实施计划](2026-11-21-client-onboarding.md)。

## 范围与基线

- 基线为主目录当前 985 个源码、文档和前端资源文件 SHA256，包含此前已批准但尚未提交的实现。隔离工作区为 `PivotFlow/.worktrees/client-onboarding`，分支为 `codex/client-onboarding`。
- 主目录 HEAD 保持 `90aee95761d8fa21ab3f292b3b2930f8fa24be08`。九个密钥、数据库及 WAL/SHM、模型目录和运行程序文件单独核验；发现链接和共享前端依赖保留。
- 复用现有站点、账号、渠道、令牌表单、模型测试、公共代理、费用结算和请求历史；不新增依赖、数据库表或专用计费联测接口。

## 检查与证据

外置证据位于 `E:/Dev/tools/api/archive/client-onboarding-2026-11-21/`。首次红测试、失败日志与修复结果均保留；初次完整检查不代替修复后的最终回归。收尾时详细 `.log` 和官方原文压缩到 `client-onboarding-evidence.zip`，逐文件 SHA256 验证后移除重复原件；结果 JSON、脚本、清单、审查摘要和截图保留可直接读取。

| 检查 | 已取得结果 |
| --- | --- |
| 前端基线 | 196/196 测试、类型检查通过；`frontend-baseline.log`、`frontend-baseline-result.json` |
| 模板与公共请求客户端 | 初次完整测试 220/220、类型检查与构建通过；`frontend-final.log`、`typecheck-final.log`、`build-final.log` |
| 真实 Go 代理契约 | 四种原生协议通过公共代理入口、单次合成上游调用、令牌/模型/协议拒绝、请求身份与历史关联；`proxy-contracts.log` |
| 初次后端完整测试 | 35 个有测试包通过、另 5 个包无测试，222.04 秒；`backend-full.log`、`backend-full-result.json` |
| 初次静态检查 | 0 issues，退出 0；`lint.log`、`lint-result.json` |
| 初次合成浏览器 | 38/38 检查通过，未预期 API、外部请求和页面异常均为 0；`browser-before-review-results.json`、`browser-final.log` |
| 配置与终端脚本实际检查 | 24 个合成产物、85/85 项检查；8 次成功的 mock HTTP 请求及 2 次预期 403 非零退出；`fixture-verification.json`、`fixture-transport-green.log` |
| 最终后端完整测试 | `go test -tags sonic -buildvcs=false -count=1 -p 4 -timeout 10m ./internal/...` 退出 0；35 个有测试包通过、另 5 个包无测试，218.32 秒；`backend-full-final.log`、`backend-full-final-result.json` |
| 最终前端测试、类型与构建 | 224/224 测试通过，typecheck/build 均退出 0，Vite 构建 6.33 秒；`frontend-tests-reviewed.log`、`frontend-typecheck-reviewed.log`、`frontend-build-reviewed.log`、`frontend-reviewed-result.json` |
| 最终合成浏览器 | 43/43 检查通过，未预期事件 0；`browser-reviewed.log`、`browser-reviewed-results.json` |
| 模板证据与最终源码一致性 | 重新生成的 24 个产物与此前解析/执行验证的清单完全一致；`fixture-current-source-result.json`，复用未改变的 85 项脚本验证结果 |
| 最终静态检查 | `golangci-lint run ./...`，0 issues、退出 0；`lint-reviewed.log`、`lint-reviewed-result.json` |
| 最终 Go 构建 | 稳定的前端资源构建后 `go build -tags sonic -buildvcs=false -p 4 -o <验证程序> .` 退出 0，3.57 秒；`go-build-final-verification.json` 保存产物哈希及大小；程序未执行 |

## 配置与响应边界

模板按当日访问的官方 Codex、Claude Code、Gemini CLI 和 OpenAI Python 文档核对；原文保存在外置 `official-*.md`。生成 JSON/TOML/Python/PowerShell/Bash 后实际解析，并把中文、引号、美元符号、反引号、反斜杠和模型供应商前缀经合成 HTTP 服务核对。Node 断言覆盖地址前缀、重复 API 路径、认证信息、query/fragment、控制字符、Gemini 外部 HTTPS 要求、秘密遮挡和环境变量读取。

实际执行发现两个脚本问题，均先失败后修复：PowerShell 的 Bearer 语法推断拒绝特殊令牌，Git Bash 下 Windows cURL 将中文 argv 转为系统编码。现采用 **PowerShell 7+**、UTF-8 字节请求体、`SkipHeaderValidation` 和错误停止；Bash 使用不展开的 heredoc/标准输入及 cURL 7.76+。证据见 `fixture-transport-red.log`、`fixture-transport-red.json` 和绿色检查记录。

浏览器公共请求只使用同源相对路径、下游认证、`credentials: omit` 与 `redirect: error`，不进入管理员会话请求处理。模型列表有 2 MiB/20,000 行上限，联测等待 60 秒、响应上限 256 KiB。请求最多 256 输出 token，页面不自动重发。成功必须具有对应协议完整终态与非空有效文本；HTTP 200 的 error/incomplete、空文本、HTML、过大或损坏内容不算成功。停止、超时和断线保留可能已执行的含义。

## 独立审查与修复

审查以本阶段当前源文件对比主目录基线，未将此前未提交改动纳入本阶段差异。审查指出并核实以下问题：

1. Gemini 路径模型在 `/` 或 `:` 处截断，导致完整令牌白名单和路由键不正确。`gemini-model-identity-red.log` 复现完整模型返回 403。代理现用转义路径提取最后动作分隔符之前的模型、仅解码一次，并对转发/重定向重新进行路径编码。Gemini 的 URL 模型优先于可选 `body.model`，不能用允许的 body 前缀绕过 URL 模型限制。回归覆盖 `/`、`:`、中文、字面 `%2F`、`?`、`#`、权限拒绝、上游路径与持久历史；已有 `/models/{model}/completions` 行为另行保留。
2. 模型列表重复读取渠道，第二次存储失败被显示成 200 空清单。`models-snapshot-red.log` 复现；现一次读取同一渠道快照完成权限和协议过滤，存储失败返回 500，合法权限空集合仍返回空列表。
3. 浏览器历史改变 `?token=` 时旧联测结果可能写入新选择。现绑定启动时的令牌、完整秘密、协议、模型与 generation；切换或移除 URL 令牌参数均中止旧请求、清空选择秘密和结果并解除等待。用户主动停止仍能显示 unknown；初次无参数仍能选中首个可用令牌。`history-token-switch-red.log` → 最终 43 项浏览器检查，验证前进/后退、迟到成功和移除参数。
4. 创建令牌响应遗漏费用限额和协议元数据。现复用模型的公开序列化，单独附带一次性明文；`token-metadata-review-red.log` → 目标、创建/更新/恢复及 AuthToken 相关绿色回归，验证费用、协议和渠道范围与存储一致，hash/ciphertext 不进入响应。
5. 已收到的无效 UTF-8 内容原被分类为网络中断。现分类为 invalid_response；`ui-review-fixes-red.log` → 单元回归和最终 224 项完整测试。

独立审查摘要见外置 `independent-review.md`。所有修复已进入最终后端/前端完整套件与浏览器回归。实际查看了最终桌面、390px 深色配置和 320px 浅色结果 JPEG；没有横向溢出，配置与验证操作可达。键盘焦点循环和相关页面的返回接入链接包含在浏览器检查中；未使用真实读屏软件验收。

单次渠道快照取代旧模型清单方法后，lint 检出两个闲置辅助函数，现已移除并重跑完整后端和静态检查；`lint-unused-snapshot-red.log` 保留失败记录。初次 Go build 与前端构建并发替换嵌入资源而失败，后续在静态资源稳定后顺序构建通过；`go-build.log` 保存原失败，`go-build-final.log` 保存最终成功。

## 本地整合与清理

使用当前源 SHA256 增量清单集成；被覆盖原文件先备份至 ZIP，并逐文件核验。新静态资源和入口写入后，才移除旧生成资源；不删除源码、用户数据、历史配置或共享依赖。主目录冒烟后再删除本阶段工作区、依赖 junction、临时分支和未执行的验证程序。

整合前 985 个基线文件及九个保护文件全部匹配，见 `pre-integration-main-integrity.json`。最终记录分别为 `final-files.json`、`backup-verification.json`、`integration-execution.json`、主目录 smoke/browser 结果、`cleanup-verification.json`、`final-integrity.json` 和证据压缩记录。它们保存实际操作数、哈希、临时文件空间和 QA 端口关闭状态。

## 明确限制与下一项

真实 Go 代理测试使用合成上游和测试存储/临时 SQLite；Chromium 使用完整合成 API。本阶段没有使用生产账号启动 Codex、Claude Code、Gemini CLI 或 OpenAI SDK，也不把一次文本调用与脚本解析描述为工具、流式和长会话的完整兼容认证。实际部署和已保存客户端配置由用户另行应用。

本机无 C 编译器/CGO race 环境，未运行 race detector；未连接真实 MySQL/PostgreSQL、生产数据库或执行真实签到。本阶段不包含提交、推送、发布或替换生产程序。

下一项按已批准路线推进 **P1 配置导入与渠道同步差异预览**，让写入前的新增、更新、冲突和保留字段可见，继续复用现有备份与渠道投影。


## 实际本地收尾结果

本地整合复制 75 项源码、文档与资源，移除 38 项过期生成资源；929 项未触碰基线文件和九个保护文件全部匹配，发现链接及 HEAD 保持原样。主目录代理回归、43 项合成浏览器检查和 64 个文档链接均通过；主目录 Go build 退出 0，耗时 8.48 秒，验证程序未执行，记录见外置 main-proxy-smoke-result.json、browser-main-result.json 和 go-build-main-verification.json。

本阶段工作区、临时分支、依赖 junction 和验证程序已清理，共移除 82.78 MiB 临时文件；共享依赖、数据库、密钥和原有程序保留。日志压缩后的净空间另见 compressed-evidence-verification.json。实施计划的本地整合与清理两项已在操作完成后勾选；仅这两项勾选和本段收尾记录在主目录验证后补写，源文件哈希清单同步更新，细节见 checklist-finalization.json。
