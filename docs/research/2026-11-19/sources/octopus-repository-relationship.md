# Octopus 仓库关系、fork 与旧包证据

## 结论

`Hureru/octopus` 是独立 fork，不是 `bestruirui/octopus` 的转移仓库。公开 API 同时保留 parent/source 关系，且 Hureru 的默认分支和最近推送时间都与上游不同。

## 可核验事实

- 上游：https://api.github.com/repos/bestruirui/octopus
  - `full_name=bestruirui/octopus`
  - `fork=false`
  - 默认分支 `master`
  - AGPL-3.0
  - master HEAD `8aa58b0340ba2de58ce74fbfacd32597c44842a2`
- Fork：https://api.github.com/repos/Hureru/octopus
  - `full_name=Hureru/octopus`
  - `fork=true`
  - 默认分支 `dev`
  - `pushed_at=2026-11-18T19:15:15Z`
  - parent/source 指向 `bestruirui/octopus`
  - AGPL-3.0
- Fork 页面：https://github.com/Hureru/octopus
- 主项目 README 的致谢链接指向的是 Hureru fork：https://github.com/Hureru/octopus
- Hureru README 明确说明 “Forked from bestruirui/octopus”，并描述 dev 分支继续演进。

没有证据支持“上游被转移给 Hureru”或“Hureru 接管了原 repo”。更准确的说法是：上游作者 bestruirui 的 `master` 与 Hureru 的 `dev` 并行发展，后者以 fork 身份持续提交。

## 旧 zip 身份

只读取 zip 清单和少数特定文件，没有解压整个包：

- 路径：`E:\Dev\tools\api\octopus-dev.zip`
- bytes：7,044,564
- SHA-256：`3185A44FFD37BFE3597CFE623BDDD2DD7C5D350FCC3DF698B1085EE2AD45CE7E`
- ZIP 修改时间：2026-08-09
- 大多数内部文件时间：2026-07-02
- `internal/conf/version.go` 自报 Author `hureru`、Repo `https://github.com/Hureru/octopus`、Version `dev`、Commit `unknown`

因此旧包只能说明 fork 的开发快照范围，不能代表上游 2026-08 功能，也不能用来构造“上下游迁移时间表”。

## 旧包与上游主线的方向差异

旧包中可读到轮询、随机、failover、加权、会话保持、WebSocket、Images、Gemini、站点投影等开发分支能力。当前上游 `master` 的公开源码则是三种协议位、`manual/failover` 分组、三套 SQL 数据库、无 WebDAV 定时备份。两套设计不能混用。

## 交付 archive 建议

保留原 zip 作为历史核对物是安全且可复现的做法；当前已经记录大小、哈希、时间和关键文件身份，因此从研究可复现性看不必立即删除。若工作区要求清理，可删除原 zip，因为证据身份已归档；在归档完成前不应删除。