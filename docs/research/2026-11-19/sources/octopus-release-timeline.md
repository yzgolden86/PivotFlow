# Octopus 发布与版本证据（2026-11-19 核查）

核查对象是 `bestruirui/octopus`。所有 GitHub API 时间均按 UTC 保留；报告日期 2026-11-19 是 Asia/Shanghai 的工作日期。

## 仓库与 HEAD

- GitHub API 原文：https://api.github.com/repos/bestruirui/octopus
- `fork=false`，`archived=false`，默认分支 `default_branch=master`。
- License API 字段为 `AGPL-3.0`。
- HEAD API：https://api.github.com/repos/bestruirui/octopus/commits/master
- `master` HEAD：`8aa58b0340ba2de58ce74fbfacd32597c44842a2`
- 提交作者/提交时间：`2026-10-23T00:49:11Z`
- 提交信息：`feat: :rotating_light: CVE-2026-111943`，正文 `close #426`。
- 该提交为未签名提交，API verification 为 `reason=unsigned`；API 统计为 8 行变化（新增 5、删除 3）。
- 永久提交页：https://github.com/bestruirui/octopus/commit/8aa58b0340ba2de58ce74fbfacd32597c44842a2

`main` 与 `master` 分开：仓库 API 明确默认分支为 `master`。对 `branches/main` 的访问得到 404，因此不能以 `/commits/main` 代表 Octopus 主线。

## 最新 release

- Latest API：https://api.github.com/repos/bestruirui/octopus/releases/latest
- `tag_name=v0.13.13`
- `target_commitish=master`
- `draft=false`，`prerelease=false`
- `created_at=2026-10-23T00:49:11Z`
- `published_at=2026-10-23T01:03:51Z`
- tag 指向 `8aa58b0340ba2de58ce74fbfacd32597c44842a2`，与 master HEAD 相同。
- 永久 release：https://github.com/bestruirui/octopus/releases/tag/v0.13.13

回查 release 列表未发现预发布项；API 返回的 release 均标记 `prerelease=false`。因此“最新 prerelease”的结论是：截至本次核查未发现公开 prerelease，而不是存在某个隐藏版本。

## 约 90 天内的 stable 时间表

窗口近似为 2026-08-21 至 2026-11-19。按发布时间列出 16 个 stable：

| Tag | SHA | published_at（UTC） | 要点 |
|---|---|---|---|
| v0.12.0 | 6eb8b5251553f8ecfe8dcda0cb87decc60a8b1aa | 2026-08-24T07:32:28Z | 自定义 Key、模型统计、渠道模型拆表；流式 `context canceled` 与 Windows 更新修复 |
| v0.12.1 | e7a145513a1b66c81c7c60ff27cbfdc3f35f4a7c | 2026-08-25T14:35:36Z | 渠道统计改版、分享 |
| v0.13.0 | f07b8dc026a01c0fa08bbbdbc4662d9bb6a92703 | 2026-09-01T10:02:54Z | 删除 Gemini 渠道；Single Channel Multiple Keys；MySQL 迁移修复；升级前备份提醒 |
| v0.13.1 | dd21e66b8d841729bddb49a45a7bb59fb8c497d1 | 2026-09-02T04:19:42Z | gzip；全新数据库迁移修复 |
| v0.13.2 | 27aa40dc0f3b2902bce3e96ccdba019d17041606 | 2026-09-02T10:38:34Z | 前端优化 |
| v0.13.3 | 7a720e9ef4fe69e24765d66d9cad76a17fb183a0 | 2026-09-10T01:38:48Z | 日志显示来源 API Key；实时状态优化 |
| v0.13.4 | 6dce286bda7f078b2fa8831bc59783a012261d3b | 2026-09-10T02:54:57Z | `client_header`、渠道禁用修复、日志 UI |
| v0.13.5 | 6f330f5ac004bd602aabf50074b759416c6e25d0 | 2026-09-19T09:20:07Z | 全局模型过滤、token speed、首字/速度/缓存率、手动取消 |
| v0.13.6 | 4a54b59e2f489c92070cb1cec5ed12cd0bb72e22 | 2026-09-20T01:22:15Z | 关闭 #398，修复流式 `context canceled` 误报 |
| v0.13.7 | 0e1c3fc6844d15c5e36266811719ebe706cb84bb | 2026-09-23T08:17:50Z | 厂商预设修复 |
| v0.13.8 | 9357a322ff1979bd2bf825ce8e2322e7144a8e1e | 2026-09-23T12:58:19Z | 创建渠道错误弹窗、思考等级、日志布局 |
| v0.13.9 | a94a74049e7264ec49304e3d13540fffd6cd2949 | 2026-09-30T03:12:09Z | 更多模板；日志显示渠道 Key |
| v0.13.10 | d1a26bdd8770a05ee03e73d867c2cf5af9fa2951 | 2026-10-07T08:50:33Z | 反代子路径、分组置顶/置底、`passwd`、迁移修复 |
| v0.13.11 | fd8b3b86c7236f8951b3542eaf734f44a3d9ad3c | 2026-10-13T07:25:47Z | 更严格的流式响应检查 |
| v0.13.12 | 3d1ff135ec56f13417ffdb8b344bde56a1531202 | 2026-10-14T03:33:12Z | 请求头 UA 透传 |
| v0.13.13 | 8aa58b0340ba2de58ce74fbfacd32597c44842a2 | 2026-10-23T01:03:51Z | 安全升级、Go 依赖升级、版本号；关闭 #426 |

## v0.13.13 的安全边界

该 tag 的仓库差异只有 `go.mod`、`go.sum` 和版本文件等，属于供应链安全升级版，没有新的路由功能。Go 版本由 1.26.4 提到 1.26.10，AxonHub 依赖更新时间也改变。可由提交 API 的 `files[].patch` 和固定提交链接核验：

- https://api.github.com/repos/bestruirui/octopus/commits/8aa58b0340ba2de58ce74fbfacd32597c44842a2
- https://github.com/bestruirui/octopus/commit/8aa58b0340ba2de58ce74fbfacd32597c44842a2
- https://github.com/golang/go/issues/82345

CVE-2026-111943 是 Go `crypto/x509` 的信任判定缓解问题；公开证据支持“Octopus 升级 Go 以处置其依赖面风险”，不支持把该 CVE 说成 Octopus 自己发明并产生的漏洞。

## 本地旧参考包

`E:\Dev\tools\api\octopus-dev.zip` 不是上述主线的 2026-08 release：

- 大小：7,044,564 bytes
- SHA-256：`3185A44FFD37BFE3597CFE623BDDD2DD7C5D350FCC3DF698B1085EE2AD45CE7E`
- ZIP 修改时间：2026-08-09；内部文件时间多为 2026-07-02
- `internal/conf/version.go`：`Version="dev"`、`Commit="unknown"`、`Author="hureru"`、`Repo="https://github.com/Hureru/octopus"`
- `go.mod` 声明 Go 1.25.0

结论：它只能作为 `Hureru/octopus` 的 `dev` 快照参考，不能当成 `bestruirui/octopus` 在 2026-08 的版本，也不能据此证明上游存在轮询、加权、WebSocket、Images 等功能。