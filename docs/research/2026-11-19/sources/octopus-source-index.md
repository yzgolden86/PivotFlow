# Octopus 固定提交源码索引

所有永久源码链接都以 `8aa58b0340ba2de58ce74fbfacd32597c44842a2` 为基准，避免 `master` 继续移动后语义漂移。

基准 URL：`https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/`

## 身份、许可与入口

- README：`README.md`
- 中文 README：`README_zh.md`
- LICENSE：`LICENSE`，AGPL-3.0
- CLI 启动：`cmd/start.go`
- 密码命令：`cmd/passwd.go`
- 配置：`internal/conf/config.go`

## 数据模型与渠道

- 渠道、Key、模型、Grant：`internal/model/channel.go`
- 分组与 Relay 配置：`internal/model/group.go`
- API Key：`internal/model/apikey.go`
- 备份格式：`internal/model/backup.go`
- 统计结构：`internal/model/stats.go`
- 渠道操作：`internal/op/channel.go`
- API Key 校验与费用限额：`internal/op/apikey.go`
- 统计落库：`internal/op/stats.go`
- 备份导入导出：`internal/op/backup.go`

## Relay、协议和失败处理

- 主请求循环：`internal/relay/handler.go`
- 选成员、冷却、亲和：`internal/relay/route.go`
- 请求实时状态：`internal/relay/state.go`
- 协议请求/响应转换：`internal/relay/protocol.go`
- 上游 HTTP 与流读取：`internal/relay/upstream.go`
- 渠道执行：`internal/relay/channel.go`
- 请求日志：`internal/server/handlers/log.go`

## 认证、价格与存储

- JWT 生成校验：`internal/server/auth/auth.go`
- 管理面鉴权中间件：`internal/server/middleware/auth.go`
- 用户设置：`internal/server/handlers/user.go`
- 价格：`internal/price/price.go`、`internal/price/presets.go`
- 数据库初始化：`internal/db/db.go`
- 迁移：`internal/db/migrate/migrate.go`
- 数据库依赖：`go.mod`

## 部署与升级

- 更新处理：`internal/server/handlers/update.go`、`internal/update/core.go`
- Release workflow：`.github/workflows/release.yaml`
- 构建：`scripts/build.sh`
- Dockerfile：`scripts/dockerfile/Dockerfile`
- entrypoint：`scripts/dockerfile/entrypoint.sh`
- Compose：`docker-compose.yml`

## UI

- 应用外壳与导航：`web/src/components/app-shell.tsx`
- 全局状态：`web/src/stores/app.ts`
- 渠道表单：`web/src/components/modules/channel/Form.tsx`
- Key 编辑：`web/src/components/modules/channel/FormKeys.tsx`
- Grant 矩阵：`web/src/components/modules/channel/FormGrants.tsx`
- 分组成员编辑：`web/src/components/modules/group/Editor.tsx`
- 分组候选：`web/src/components/modules/group/ItemList.tsx`
- 实时成员状态：`web/src/components/modules/group/MemberStatus.tsx`
- 日志卡片：`web/src/components/modules/log/Item.tsx`
- 首页：`web/src/components/modules/home/index.tsx`
- 虚拟网格：`web/src/components/common/VirtualizedGrid.tsx`
- 主题：`web/src/provider/theme.tsx`
- 语言：`web/src/provider/locale.tsx`
- 样式：`web/src/globals.css`
- README 截图清单：`web/public/manifest.json`

## 二进制截图

固定提交中的 README 截图永久链接示例：

- https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/desktop-home.png
- https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/desktop-channel.png
- https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/desktop-group.png
- https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/desktop-log.png
- https://github.com/bestruirui/octopus/blob/8aa58b0340ba2de58ce74fbfacd32597c44842a2/web/public/screenshot/mobile-home.png