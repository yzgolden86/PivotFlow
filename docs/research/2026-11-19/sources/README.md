# 研究来源与证据说明

抓取和审阅日期：2026-11-19，Asia/Shanghai。这里保留公开源代码、发布 API、issue 与截图的研究副本，产品代码没有采用这些文件。

此目录的 `go.mod` 仅隔开研究快照与产品模块，避免根目录 `go test ./...` / lint 把来自不同项目和包的 `.go` 证据文件一起编译。不要在此运行依赖安装或构建；原始快照内容保持不变。

## 固定审阅点

| 项目 | 审阅提交 | 稳定发布 |
| --- | --- | --- |
| PivotFlow | `90aee95761d8fa21ab3f292b3b2930f8fa24be08` | v0.4.9 |
| ccLoad | `eebe29fe769332a6c1d822e4fd14a7b701348d24` | v5.0.1 为 `a6ccc5bb5ce11998578b597d5a370b46e3e16bd9`；审阅 HEAD 同 v5.0.3-beta.2 |
| Octopus 上游 | `8aa58b0340ba2de58ce74fbfacd32597c44842a2` | v0.13.13 |
| All API Hub | `5c4101d93143df6b3b112ccd819a29f3f4ada78c` | v4.10.1；main / Nightly 还包含其后的提交 |

各 API 文件代表抓取时的公开状态。主分支和滚动 Nightly 以后还会移动，研究行为的引用优先使用提交永久链接。

## 文件组织

- `ccload-repo/head/releases/tags/tree.json` 和 `ccload-issue-*.json`：仓库、分支、发布、文件树与近期问题原文。
- `ccload-src-*`：固定 ccLoad HEAD 的选定源文件与许可证；确切路径在 [ccLoad 报告](../ccload.md) 的链接中。
- `ccload-4.10.2-*`：开发时本地旧快照的图片；`ccload-current-*` 为当前仓库附带的公开图及查看副本。图内版本仍是 4.10.33-beta.1、10/06，不能用来证明 5.0.x UI。
- `octopus-release-timeline.md`、`octopus-feature-matrix.md`、`octopus-ui-evidence.md`、`octopus-repository-relationship.md`、`octopus-source-index.md`：逐项版本、来源、功能、UI 与 fork 证据索引。
- `octopus-src-*` 和 `octopus-desktop/mobile-*.png`：固定 HEAD 的八份关键源文件和五张公开截图；[octopus-captured-files.json](octopus-captured-files.json) 保存源路径、永久 URL、字节数和哈希。分组图的旧策略与当前源码有漂移。
- `github-all-api-hub-latest.json`、`github-hub-repo/head/releases/tags.json`：All API Hub 最新稳定发布、main 与 Nightly 的独立复核。
- `github-hub-README.md`、`github-hub-CHANGELOG.md`、`github-hub-package.json`、`github-hub-LICENSE`：固定 All API Hub HEAD 的公开原文；[github-hub-source-manifest.json](github-hub-source-manifest.json) 保存 URL。
- `github-hub-src-*`：九份关键源文件，覆盖按站点调度、Cookie 操作、验证工作区、导入预览、弹窗和 WebDAV；具体来源见 [github-hub-key-source-manifest.json](github-hub-key-source-manifest.json)。
- `github-hub-doc-*.png`：固定 HEAD 文档的四张账户、Popup、凭据库和 API 测试图；来源及哈希见 [github-hub-doc-images.json](github-hub-doc-images.json)。它们是文档演示图，API 测试图早于后续工作区调整。
- [github-hub-raw-capture-manifest.json](github-hub-raw-capture-manifest.json) 记录 23 份公开 API / 原文的 URL、字节数和 SHA-256；按原始 UTF-8 保存。加上关键源码、图片与 Octopus 清单，49 条哈希记录均复核一致，见 [研究资料复核](../../../../../archive/cleanup-2026-11-19/research-source-verification.json)。All API Hub 其余证据在 [独立报告](../all-api-hub.md) 中逐项链接。

原文 README / CHANGELOG 的上游相对链接未被改写，因此它们用于复核来源；本轮读者入口和可导航链接在上层报告中。

## 证据如何阅读

1. **代码事实**：固定提交中的具体字段、分支和处理流程。说明实现的语义与边界，不能自动证明现实负载下的表现。
2. **发布事实**：GitHub tag、SHA、prerelease、发布时间和变更说明。变更说明需结合源码判断，不能用 master 的功能替稳定版背书。
3. **截图事实**：只证明图中的布局和视觉。公开图可能滞后；图中余额、模型、性能和品牌不是评测基线。
4. **源码推断的 UI**：组件、状态机、CSS 和路由说明可见交互的实现意图；真实焦点、滚动、对比度与移动体验需要浏览器验收。
5. **公开 issue**：作为问题与修复线索。本轮未逐项复现，open / closed 仅是抓取时状态，不表示所有相似条件均解决。

All API Hub 的商店分发、Stable 与 Nightly 分别记录。Nightly 的 published_at 保留早期滚动发布的日期；最新构建应看 body、target commit、updated_at 与资产时间。

## 获取与验证限制

本地 smart-search 网络检查因代理端口解析失败，MCP 读取部分 GitHub 页面也出现 robots 403、匿名限流或超时。已获取材料先保留；后续以 `Invoke-WebRequest -NoProxy -TimeoutSec 15` 的任务内请求参数成功核对公开 GitHub API 和固定提交原文，没有修改系统代理或认证配置。失败的读取不作为功能证据。

本轮未运行三个参考项目的真实 OAuth/站点实例，也未做并发计费、长时负载或浏览器完整 E2E。报告中没有将公开截图、源码测试的存在或发布说明写成已经验证的性能结论。

## 来源与发行边界

ccLoad 根许可证为 MIT，具体第三方子树仍有自己的许可证和来源记录。Octopus 与 All API Hub 为 AGPL-3.0。文件保留上游许可证，截图仅作为研究材料。未来实际采用代码时，应记录具体提交、采用范围和许可证；学习交互思想可独立实现。
