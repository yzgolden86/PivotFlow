# PivotFlow 文档

这套文档以当前代码和控制台为准，优先解释个人部署最常用的路径。

| 文档 | 解决的问题 |
| --- | --- |
| [快速开始](getting-started.md) | 第一次启动、登录和验证 |
| [客户端接入与配置生成](client-connection.md) | 五步接入、四类客户端模板、下游令牌权限与真实代理验证 |
| [核心概念](concepts.md) | 站点、账号、渠道、访问令牌的边界 |
| [站点与账号管理](site-management.md) | 凭证、余额、签到、公告和 Provider 能力 |
| [浏览器辅助与账号导入](browser-import.md) | 页面助手、All API Hub 账号、来源／目标核对、固定预览与结果恢复 |
| [站点维护任务与共享限速](site-tasks.md) | 重复任务合并、站点预算、共享冷却、取消和失败重试 |
| [路由与分发](routing.md) | PivotFlow 路由、Key/URL、优先级、冷却和同步 |
| [模型路由与统一模型清单](model-routes.md) | 按模型／统一组解释候选与尝试、令牌模型清单、失效成员清理和精确历史 |
| [可选请求缓存](request-cache.md) | 默认关闭与客户端选择、精确隔离、零新增费用、容量、失效和只读观察 |
| [Key 健康管理](key-health.md) | 多 Key 状态、复检、手动启停与清理 |
| [模型测试](model-testing.md) | 模型同步、账号直测和渠道测试 |
| [系统配置](configuration.md) | `.env` 和控制台系统设置 |
| [控制台键盘操作与窄屏阅读](console-accessibility.md) | 浮层焦点、搜索键盘、分页与选择范围、完整安全详情和资源预算 |
| [配置导入与渠道同步](config-import.md) | JSON/WebDAV 恢复和站点渠道同步的差异预览、固定快照确认与恢复边界 |
| [备份与恢复](backup-recovery.md) | WebDAV 回读证据、配置和数据库恢复、计费日志与回执配套、合成验收命令 |
| [升级与回退](upgrade.md) | 准确版本和镜像、存储与主密钥、隔离演练、升级验收和同一时点回退 |
| [部署](deployment.md) | Docker、源码构建、前端开发和升级 |
| [安全与备份](security.md) | 主密钥、凭证、数据库和恢复 |
| [可靠结算与流式恢复](reliability.md) | 待结算记录、故障恢复、认证跳转与流中断 |
| [请求身份与尝试历史](request-history.md) | 用请求 ID 串联故障切换、协议协商和最终结果 |
| [费用来源与数据语义](cost-semantics.md) | 估算来源、完整/部分/未知覆盖、Token 本地记账与上游对账边界 |
| [后台运行健康](runtime-health.md) | 结算、写入、复制、清理、启动恢复、资源压力和脱敏诊断 |
| [维护与 PivotFlow 同步](maintenance.md) | 上游更新的审计与同步边界 |
| [故障排查](troubleshooting.md) | 常见错误和定位顺序 |
| [2026-11-19 项目审阅与参考研究](research/2026-11-19/README.md) | 当前进展、文件清理、ccLoad / Octopus / All API Hub 最新功能、UI 与改进建议 |

## 文档图片

`assets/` 中的图片由 `scripts/docs_screenshots.py` 生成。脚本通过 Playwright 拦截管理 API 并注入合成数据，所有站点、余额、模型、URL 和账号均为虚构内容。

现有控制台图来自 2026-09-03 的 v0.2.0 演示，部分导航与当前版本有时间差；封面为概念图。当前页面与交互的审阅见 [2026-11-19 UI 报告](research/2026-11-19/pivotflow-ui-audit.md)。

## 范围说明

`internal/protocol/cliproxy/LICENSE` 和 `UPSTREAM.md` 是第三方来源与许可证记录，保留原文；上游模型注册 JSON 和测试模板是结构化数据，不作为产品文档改写。
