# 部署

## Docker Compose

从当前源码构建是最稳定的首选方式：

```bash
cp .env.docker.example .env
docker compose -f docker-compose.build.yml up -d --build
docker compose -f docker-compose.build.yml ps
docker compose -f docker-compose.build.yml logs -f pivotflow
```

仓库发布 GHCR 镜像后，也可以使用：

```bash
docker compose pull
docker compose up -d
```

数据卷映射到 `/app/data`。升级前备份数据库和凭证主密钥。

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

PivotFlow 的自动更新功能固定为只读检查。Docker 用户通过镜像 tag 升级，源码用户先查看变更并运行定向测试，再替换二进制。不要把旧 ccLoad 自动更新脚本直接用于融合程序。
