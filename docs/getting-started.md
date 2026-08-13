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
docker compose -f docker-compose.build.yml up -d --build
docker compose -f docker-compose.build.yml logs -f pivotflow
```

默认地址是 `http://127.0.0.1:8080/web/login.html`。PowerShell 对应命令是 `Copy-Item .env.docker.example .env`。该方式从当前源码构建，不要求仓库已经发布容器镜像。

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
