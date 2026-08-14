# 快速开始

## 准备环境

- Docker Compose，或 Go `1.26`。
- 使用源码前端时准备 Node.js `24`。
- 为数据目录准备持久化磁盘。

## Docker

```bash
cp .env.docker.example .env
```

编辑 `.env`，至少设置一个强密码：

```dotenv
PIVOTFLOW_PASS=replace-with-a-long-random-password
TZ=Asia/Shanghai
```

启动：

```bash
docker compose pull
docker compose up -d
docker compose logs -f pivotflow
```

默认地址是 `http://127.0.0.1:8080/web/login.html`。PowerShell 对应命令是 `Copy-Item .env.docker.example .env`。该方式使用 `ghcr.io/yzgolden86/pivotflow:latest`。如果 GHCR 要求认证，请执行 `docker login ghcr.io`，使用 GitHub 用户名和具备 `read:packages` 权限的个人访问令牌；GitHub 账号密码不能直接使用，账号也必须具有镜像读取权限。

如果无法访问已发布镜像，从当前源码构建：

```bash
docker compose -f docker-compose.build.yml up -d --build
docker compose -f docker-compose.build.yml logs -f pivotflow
```

预构建镜像使用 `./data` 保存数据；源码构建使用 Compose 管理的 Docker 卷，逻辑名称为 `pivotflow_data`。两者是独立的数据存储，直接切换可能会看到一个空数据库。切换前请按照[安全与备份](security.md)迁移数据库和凭证主密钥。

## 源码构建

```bash
cp .env.example .env
go build -tags sonic -trimpath -o pivotflow .
./pivotflow
```

Windows：

```powershell
Copy-Item .env.example .env
go build -tags sonic -trimpath -o pivotflow.exe .
.\pivotflow.exe
```

用 `PORT=8089`（PowerShell 为 `$env:PORT='8089'`）更换监听端口。

## 首次登录后

1. 打开系统设置，确认数据库路径和主密钥状态。
2. 添加一个测试站点和账号，点击验证凭证。
3. 刷新余额并同步模型。
4. 执行一次账号直测，再同步站点渠道。
5. 创建下游密钥，用客户端发一条最小请求。

## 最小请求

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer YOUR_PIVOTFLOW_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.4","messages":[{"role":"user","content":"ping"}]}'
```

不要把上游访问令牌放在这个请求中；代理只使用 PivotFlow 下游密钥。

## 验证服务

```bash
curl http://127.0.0.1:8080/health
```

`/health` 只表示进程和 HTTP 服务可用，不代表每个上游账号或渠道健康。
