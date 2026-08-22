# 部署

## Docker Compose

默认使用已发布的 GHCR 镜像：

```bash
cp .env.docker.example .env
docker compose pull
docker compose up -d
docker compose ps
docker compose logs -f pivotflow
```

该方式使用 `ghcr.io/yzgolden86/pivotflow:latest`。如果 GHCR 要求认证，先执行 `docker login ghcr.io`：用户名填写 GitHub 用户名，密码使用具备 `read:packages` 权限的个人访问令牌；GitHub 账号密码不能直接使用。该账号还必须具有镜像读取权限。

如果无法访问已发布镜像，可以从当前源码构建：

```bash
docker compose -f docker-compose.build.yml up -d --build
docker compose -f docker-compose.build.yml ps
docker compose -f docker-compose.build.yml logs -f pivotflow
```

预构建镜像方式把当前目录的 `./data` 映射到 `/app/data`；源码构建方式使用 Compose 管理的 Docker 卷，逻辑名称为 `pivotflow_data`。两者是独立的数据存储，直接切换可能会看到一个空数据库。升级或切换前按照[安全与备份](security.md)备份并迁移数据库和凭证主密钥。

## 源码开发

```bash
go test -tags sonic ./internal/...
cd console
npm ci
npm run dev
```

生产前端由 `npm run build` 生成并嵌入 Go 二进制；`make console-check` 会执行 TypeScript 检查和构建。

## 常用验证

```bash
make verify-web
go test ./internal/antigravityauth -count=1
go test -tags sonic ./internal/protocol/cliproxy/...
```

## 端口和反向代理

仓库 Compose 保留通用的 `8080:8080` 映射，不替用户假设网络结构。对于“VPS 本机运行 PivotFlow，Caddy 作为唯一公网入口”的部署，必须满足下面二选一的隔离条件：

1. 在本机部署副本中把映射改成 `127.0.0.1:8080:8080`；或者
2. 保留 `8080:8080`，但云安全组和主机防火墙都只向公网开放 `80/tcp`、`443/tcp`，明确拒绝 `8080/tcp`。

第二种方式依赖防火墙，不能因为已经配置 Caddy 就省略检查。可从另一台机器验证 `http://VPS_IP:8080/health` 无法连接，同时域名 HTTPS 正常访问。

推荐的 Caddy 配置如下。确认域名 HTTPS 已稳定工作后再启用 HSTS；未确认前先删除对应一行。

```caddyfile
pivot.example.com {
    encode zstd gzip

    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options "nosniff"
        X-Frame-Options "DENY"
        Referrer-Policy "strict-origin-when-cross-origin"
        Permissions-Policy "camera=(), microphone=(), geolocation=()"
        -Server
    }

    reverse_proxy 127.0.0.1:8080
}
```

Caddy 与 PivotFlow 在同一 VPS 时，反向代理只转发整个站点即可，不要只转发 `/v1/*`，否则登录、控制台和管理接口会出现 HTML/JSON 路由错误。应用只信任 `TRUSTED_PROXIES` 中来源提供的 `X-Forwarded-*`；不要把该值设成 `0.0.0.0/0`。调用 API 时优先使用 `Authorization`、`X-API-Key` 或 `x-goog-api-key`，避免 `?key=` 进入 Caddy 访问日志。

官方镜像以 UID/GID `1001:1001` 的非 root 用户运行。首次部署或从旧 Compose 升级时，先确保绑定目录可写：

```bash
mkdir -p data
sudo chown -R 1001:1001 data
chmod 750 data
```

不要在 Compose 中重新添加 `user: root` 来绕过权限错误；应修正宿主机目录所有权。源码构建方式使用 Docker 卷，通常无需手工调整。

## 更新原则

PivotFlow 的自动更新功能固定为只读检查。Docker 用户通过镜像 tag 升级，源码用户先查看变更并运行定向测试，再替换二进制。不要把旧 PivotFlow 自动更新脚本直接用于融合程序。
