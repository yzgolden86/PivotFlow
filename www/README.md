# PivotFlow 静态介绍页

`www/` 是一个可独立部署的轻量产品介绍页。功能说明只有一个事实来源：仓库根 README 和 `docs/`，不要在静态站复制整套 API 文档。

## 预览

```bash
make www-setup
make www-run
```

默认地址为 `http://127.0.0.1:8888`。`make www-setup` 会复制当前 PivotFlow 品牌图、首页截图和路由截图。

## 文件

- `index.html`：产品入口。
- `assets/css/pivotflow-site.css`：静态页样式。
- `assets/images/`：由 `docs/assets/` 同步的脱敏图片。

旧 `install.html`、`config.html`、`usage.html` 和 `feedback.html` 只保留为兼容入口时，应重定向到根 README 或 `docs/`，不要继续维护旧 ccLoad 文案。
