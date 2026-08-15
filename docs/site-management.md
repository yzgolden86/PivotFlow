# 站点与账号管理

## 添加站点

填写名称、基础 URL、平台类型和时区。平台可以手动选择，也可以保存后点击探测。若勾选“添加首个账号”，系统在同一请求中创建站点和账号，避免先创建站点再依赖前端缓存重新检索。

凭证类型按 Provider 能力显示：

- New API 系、AnyRouter、Veloera：用户名密码、访问令牌、Cookie、API Key。
- Sub2API：访问令牌、Refresh Token、API Key。访问令牌为 JWT 时会自动识别 `exp`；同时保存 Refresh Token 后，系统会在到期前自动续期并保存轮换后的令牌。
- OpenAI Compatible：仅 API Key，用于 `/v1/models` 模型发现和路由同步。

自动探测会先尝试 Sub2API、Veloera、AnyRouter、New API 系等管理型 Provider，均不匹配时才使用 OpenAI Compatible。平台探测只识别接口特征，不会凭空生成站点令牌。你仍需提供自己有权使用的凭证。

## 凭证和验证

添加或修改账号后，使用“验证凭证”检查用户信息、余额、模型数量和路由 Key 可用性。验证是一次性请求，不会替换数据库中的凭证；“保存凭证”才会加密写入。

如果提示 `credential_locked`，检查 `FUSION_MASTER_KEY`、`FUSION_MASTER_KEY_FILE` 和数据目录权限。如果提示 `expired`，重新填写上游访问令牌或会话 Cookie，并同时填写 Provider 要求的用户 ID。JWT 的 `exp` 会自动识别；opaque token 没有可解析的 `exp` 时，可在账号凭证表单中手动填写过期时间。Refresh Token 只在对应 Provider 实现续期接口时生效。

AnyRouter 的会话 Cookie 需要用户 ID；Veloera 的 Cookie/令牌可能需要兼容用户头。Sub2API 的访问令牌通常是 JWT，`session cookie` 不是把 JWT 随便填进 Cookie 字段。

## 余额、签到和变化

“刷新余额”调用 Provider 的账户信息接口并写入当前余额。“签到”先保存签到前余额，再执行 Provider 签到，最后刷新余额；结果会记录奖励文本、签到状态、余额前后值和余额变化。

支持服务端签到的 Provider：New API 系、AnyRouter、Veloera。Sub2API 当前返回 `unsupported`，可以继续使用余额、模型和公告功能。OpenAI Compatible 不提供管理接口，因此不支持余额、签到或公告。需要浏览器挑战的站点会返回 `browser_required`，不会伪造成功。

## 公告

公告中心按站点聚合、去重和记录已读状态。点击刷新会创建后台任务；任务完成后刷新列表。公告源 URL 是上游提供的参考地址，不等于 PivotFlow 的代理地址。

## Webhook

系统设置中配置一个加密保存的 Webhook URL，可选择低余额和签到失败事件，并设置去重冷却时间。先使用“测试 Webhook”验证网络，再启用真实通知。Webhook 发送失败会保留最后错误，不会阻塞余额或签到任务。
