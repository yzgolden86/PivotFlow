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

反向代理只需要转发控制台和 `/v1/*`，并正确设置 `X-Forwarded-*`。使用 `TRUSTED_PROXIES` 限制可信代理网段；直接暴露公网时设置为 `none`。

## 更新原则

PivotFlow 的自动更新功能固定为只读检查。Docker 用户通过镜像 tag 升级，源码用户先查看变更并运行定向测试，再替换二进制。不要把旧 PivotFlow 自动更新脚本直接用于融合程序。
