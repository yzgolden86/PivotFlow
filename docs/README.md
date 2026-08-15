# PivotFlow 文档

这套文档以当前代码和控制台为准，优先解释个人部署最常用的路径。

| 文档 | 解决的问题 |
| --- | --- |
| [快速开始](getting-started.md) | 第一次启动、登录和验证 |
| [核心概念](concepts.md) | 站点、账号、渠道、访问令牌的边界 |
| [站点与账号管理](site-management.md) | 凭证、余额、签到、公告和 Provider 能力 |
| [路由与分发](routing.md) | PivotFlow 路由、Key/URL、优先级、冷却和同步 |
| [模型测试](model-testing.md) | 模型同步、账号直测和渠道测试 |
| [系统配置](configuration.md) | `.env` 和控制台系统设置 |
| [部署](deployment.md) | Docker、源码构建、前端开发和升级 |
| [安全与备份](security.md) | 主密钥、凭证、数据库和恢复 |
| [维护与 PivotFlow 同步](maintenance.md) | 上游更新的审计与同步边界 |
| [故障排查](troubleshooting.md) | 常见错误和定位顺序 |

## 文档图片

`assets/` 中的图片由 `scripts/docs_screenshots.py` 生成。脚本通过 Playwright 拦截管理 API 并注入合成数据，所有站点、余额、模型、URL 和账号均为虚构内容。

## 范围说明

`internal/protocol/cliproxy/LICENSE` 和 `UPSTREAM.md` 是第三方来源与许可证记录，保留原文；上游模型注册 JSON 和测试模板是结构化数据，不作为产品文档改写。
