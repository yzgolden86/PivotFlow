# 维护与 ccLoad 同步

## 三个独立来源

1. PivotFlow 站点控制面、Provider 适配器、存储和控制台。
2. ccLoad 路由选择器、冷却、Key/URL 调度和代理链路。
3. `internal/protocol/cliproxy` 的纯协议转换快照。

它们不能用同一种“直接覆盖目录”方式升级。

## 跟踪 ccLoad 上游

建议每次上游发布都记录：版本/提交、变更范围、是否触及 selector、cooldown、proxy、protocol 或数据结构、对应测试和回滚点。只同步与核心路由相关的改动；上游 UI、配置、认证和自动更新逻辑不应直接覆盖 PivotFlow。

## 同步前检查

```bash
git fetch --tags upstream
git diff <last-reviewed>..<candidate> -- internal/app internal/cooldown internal/protocol
go test -tags sonic ./internal/protocol/...
go test -tags sonic ./internal/app ./internal/storage/sql
```

CLIProxyAPI 转换快照必须按 `internal/protocol/cliproxy/UPSTREAM.md` 的固定提交流程同步，生产源文件和同步测试必须来自同一个提交。

## 不做自动覆盖的原因

PivotFlow 在上游代码之外增加了站点控制面、凭证加密、投影绑定、公告、签到、Webhook 和中文控制台。自动把上游目录覆盖回来，容易重新引入旧品牌、旧路由入口或破坏数据边界。因此版本检查只读，合并必须人工审核和回归。

## 发布入口

当前仓库使用 `main`、`yzgolden86/PivotFlow` 和 `ghcr.io/yzgolden86/pivotflow`。发布脚本的目录名 `.agents/skills/ccload-release` 为兼容保留，Skill 名称和实际发布目标均为 PivotFlow；任何重新出现的 `master` 或 `caidaoli/ccLoad` 发布目标都应视为配置回退。
