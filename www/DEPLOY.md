# 静态站部署

先同步资源：

```bash
make www-setup
```

然后把 `www/` 作为普通静态目录部署到 Nginx、Caddy、GitHub Pages 或任意对象存储。站点不需要后端、数据库和环境变量。

Nginx 最小示例：

```nginx
server {
    listen 80;
    server_name example.com;
    root /var/www/pivotflow;
    index index.html;
    location / { try_files $uri $uri/ /index.html; }
}
```

介绍页不应包含真实控制台地址、站点 URL、账号、余额、Cookie 或 Key。更新产品界面后运行 `python scripts/docs_screenshots.py` 重新生成脱敏截图，再执行 `make www-setup`。
