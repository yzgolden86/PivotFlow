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

`latest` 和 `beta` 是发布入口，不是可审计的版本证明。发布工作流会把正式 `vX.Y.Z` 指向 `latest`、把 `vX.Y.Z-beta.N` 指向 `beta`，但当前别名是否仍指向预期产物仍要在镜像仓库中核对。生产升级应记录完整版本标签和 manifest digest，并优先在部署清单中固定 digest。源码工作区有未提交改动时，不能把本地构建或分支标签当作公开发布。

如果无法访问已发布镜像，可以从当前源码构建：

```bash
docker compose -f docker-compose.build.yml up -d --build
docker compose -f docker-compose.build.yml ps
docker compose -f docker-compose.build.yml logs -f pivotflow
```

预构建镜像方式把当前目录的 `./data` 映射到 `/app/data`；源码构建方式使用 Compose 管理的 Docker 卷，逻辑名称为 `pivotflow_data`。两者是独立的数据存储，直接切换可能会看到一个空数据库。官方镜像以 UID/GID `1001:1001` 运行；查看解析后的 Compose 配置和实际挂载点后才能确认正在使用的数据目录。

升级、重建容器或迁移数据前，按照[备份与恢复](backup-recovery.md)备份主库、每个本地计费日志、结算回执和主密钥。不要执行 `docker compose down -v`，它会删除源码构建方式使用的命名卷。发布 Compose 与构建 Compose 之间也不能只换一个容器名就切换数据。

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

恢复与配置导入的本地合成验证使用测试临时数据库，不连接真实数据库、网页服务或上游：

```bash
go test -tags sonic -buildvcs=false -count=1 ./internal/app ./internal/storage ./internal/storage/sql -run 'TestBackupRestore|TestBackupVerificationMigration|TestSyncManagerRestoreTable|TestRestoreSiteAccountModels'
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

PivotFlow 的自动更新功能只检查 `stable` 或 `preview` 渠道，不会替换当前程序；Docker 自动检查已禁用。Docker 用户应选择完整版本标签或 digest，源码用户应替换已校验的自有二进制。更新成功、发布标签存在或容器别名可拉取，都不等于数据库迁移和主密钥兼容性已经验证。

生产升级前先停止写入并制作一致备份，在副本或隔离远端克隆上启动目标版本，再替换准确产物。启动程序没有 dry-run 或 migration-only 模式，后台任务会在 HTTP 监听前启动，因此不要用生产数据做“只检查迁移”的试探启动。完整步骤、验收和回退见[升级与回退](upgrade.md)。
