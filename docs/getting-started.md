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

默认地址是 `http://127.0.0.1:8080/web/auth/`。PowerShell 对应命令是 `Copy-Item .env.docker.example .env`。该方式使用 `ghcr.io/yzgolden86/pivotflow:latest`。如果 GHCR 要求认证，请执行 `docker login ghcr.io`，使用 GitHub 用户名和具备 `read:packages` 权限的个人访问令牌；GitHub 账号密码不能直接使用，账号也必须具有镜像读取权限。

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
5. 在“令牌管理”创建访问令牌，用客户端发一条最小请求。

## 最小请求

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer YOUR_PIVOTFLOW_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5.4","messages":[{"role":"user","content":"ping"}]}'
```

不要把上游访问令牌放在这个请求中；代理只使用 PivotFlow 发放的访问令牌。

## 验证服务

```bash
curl http://127.0.0.1:8080/health
```

`/health` 只表示进程和 HTTP 服务可用，不代表每个上游账号或渠道健康。

## 之后可以调的

跑通之后这几项按需了解，默认值对个人部署已经够用：

- **渠道选择策略**（系统设置 → 路由策略）：默认均衡轮询，同优先级内摊平流量；也可切成粘性轮询，固定用上次成功的渠道直到失败。见 [路由与分发](routing.md)。
- **模型统一映射**（同一分组）：把 `GLM-5.2`、`glm-5.2`、`z.ai/glm-5.2` 这类不同写法归到一个入口，面板会自动检测可合并项，也可从渠道模型清单里挑。
- **首字超时**（系统设置 → 请求超时）：某个渠道接受连接却长时间不返回时，调低对应协议的首字超时能让它更早失败切换，而不是耗到总超时。
- **系统访问令牌**（系统设置 → 系统访问）：给排障脚本用的只读诊断令牌，不能调用模型。见 [安全与备份](security.md)。

这些设置改完都需要重启进程才生效，保存时控制台会请求优雅重启。
