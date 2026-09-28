import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  base: '/web/console/',
  plugins: [react()],
  build: {
    outDir: '../web/console',
    emptyOutDir: true,
    sourcemap: false,
    target: 'es2022',
    cssCodeSplit: true,
    reportCompressedSize: true,
  },
  server: {
    port: 5174,
    // dev 下 vite 只负责「控制台自身」，其余同源请求全部要转发给后端。
    // localStorage 按**源**隔离：dev 跑在 127.0.0.1:5174，与后端 8080 不共享登录态，
    // 所以登录页也必须在 5174 这个源上打开、并且由 vite 转发它的接口。
    //
    // 这份清单是**穷举**出来的（`grep -rhoE "[`'\"]/[A-Za-z0-9_-]+" src/`）：
    //   直接 fetch    → /dashboard/session、/logout
    //   requestEnvelope → /admin/*
    //   location/资源  → /web/auth/、/web/brand-mark.svg、/web/favicon.svg、
    //                    /web/apple-touch-icon.png
    // 新增任何**新的**一级前缀（不是新的 /admin 子路径）都必须补到这里。
    // 漏掉一个的症状极具误导性，见下方 `/dashboard` 的注释。
    proxy: {
      '/admin': 'http://127.0.0.1:8080',
      '/login': 'http://127.0.0.1:8080',
      '/logout': 'http://127.0.0.1:8080',
      // 漏了这条 = 用户看到「密码不对，登不进去」：
      // main.tsx 的 bootstrap 会 fetch('/dashboard/session') 复核会话，请求没被转发
      // 就落到 vite 手里 → base 是 /web/console/，于是 404（text/plain）。
      // 控制台把「非 200」一律当会话无效 → 清掉刚存好的 token → replace 回
      // /web/auth/。密码其实是对的，登录接口也是 200，但页面立刻被弹回登录页，
      // 看起来就是密码错。排查时先看后端日志有没有收到 /dashboard/session。
      '/dashboard': 'http://127.0.0.1:8080',
      // 登录页与品牌资源：不代理的话 vite 会回落到 SPA 的 index.html，
      // 于是又启动一次、又发现没有 token，来回跳。
      '/web': {
        target: 'http://127.0.0.1:8080',
        // 但 `/web/console` 必须留在 vite 手里：它是 dev 服务器在内存里现编的，
        // 代理到后端会拿到 web/console 里那份**旧的构建产物**，改动就看不见了。
        bypass: (req) => (req.url?.startsWith('/web/console') ? req.url : undefined),
      },
    },
  },
})
