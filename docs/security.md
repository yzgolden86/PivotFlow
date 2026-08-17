# 安全与备份

## 凭证主密钥

站点账号凭证、Webhook URL、Telegram 凭证和 WebDAV 密码使用主密钥加密。个人 SQLite 未显式提供密钥时，服务会在 SQLite 路径同目录生成权限受限的 `fusion-master.key`。

主密钥丢失时，数据库中的密文不能恢复。恢复数据库时必须同时恢复同一主密钥和 `FUSION_MASTER_KEY_VERSION`（如有设置）。

## 建议的备份集合

```text
data/pivotflow.db
data/fusion-master.key
.env                # 放在受控的密码库，不要提交 Git
```

备份前停止写入或使用 SQLite 快照机制。不要把备份文件上传到公开仓库或截图工具。

控制台“导入导出”生成的是可迁移 JSON。完整备份和连接备份会包含解密后的账号凭证、上游 Key、OAuth 凭证和下游令牌；系统设置备份可能包含通知与 WebDAV 凭证。因此备份文件本身必须按明文密钥材料保护，只保存到可信设备或受控 WebDAV 空间。导入到另一实例时，敏感字段会使用目标实例的主密钥重新加密。

WebDAV 自动备份仅支持明确配置的 `http://` 或 `https://` 文件地址，并允许访问个人 NAS 等内网目标。请使用独立的低权限 WebDAV 账号、HTTPS 和专用目录，不要指向公共分享链接。

## OAuth 和 API 密钥

- Antigravity OAuth 客户端信息只从 `PIVOTFLOW_ANTIGRAVITY_CLIENT_ID/SECRET` 读取。
- Codex/Antigravity 凭证导入后仅保存加密后的必要字段。
- 访问令牌支持重新显示/复制，但管理 API 不会默认明文返回。
- 生产日志、文档截图和问题报告必须脱敏 URL、余额、Cookie、Key 和用户 ID。

## 网络安全

默认拒绝不可信的上游目标和跨主机跳转；仅在受控内网临时启用私有地址访问。不要使用 `PIVOTFLOW_ALLOW_INSECURE_TLS=1` 绕过证书错误来“修复”生产连接。

## 事件响应

怀疑密钥泄露时：撤销访问令牌，轮换上游 Key，更新管理密码和主密钥，检查请求日志，再恢复服务。主密钥轮换需要按迁移代码和备份策略执行，不要直接删除数据库旁的 key 文件。
