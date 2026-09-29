package app

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// wallpaperProxyMaxBytes 壁纸取回的体积上限。
//
// 壁纸动辄几 MB，给 16 MiB 足够覆盖 4K 照片；再大就说明对方不是壁纸，
// 直接截断，免得把一个巨大的响应体拖进内存。
const wallpaperProxyMaxBytes = 16 << 20

// wallpaperProxyTimeout 单次取图的超时。壁纸是几 MB 的图片，比普通 API 慢，
// 但也不能无限等 —— 超时后前端只是退回「不做自适应」，不影响任何已有功能。
const wallpaperProxyTimeout = 20 * time.Second

// HandleWallpaperBytes 把用户在外观设置里填的壁纸原样取回来，交给浏览器。
//
// ── 为什么需要这个接口 ────────────────────────────────────────────────
// 「小字按壁纸明暗自适应」必须**读到壁纸的像素**。浏览器端只有一条路：
// 把图画进 canvas 再 `getImageData()`。而这一步要求图片带
// `Access-Control-Allow-Origin`，否则 canvas 被污染、读像素直接抛异常。
//
// 2026-09-29 实测常见图床**并不可靠**：
//
//	images.unsplash.com   → Access-Control-Allow-Origin: *   可以
//	api.dujin.org（必应）  → 不发该头                            不行
//
// 也就是说纯前端的方案对相当一部分用户是**静默失效**的 —— 而这正是最难查的那种
// bug。所以改成后端取回来、**同源**发给浏览器：同源就没有污染问题。
//
// ── 为什么后端不解码 ──────────────────────────────────────────────────
// 现代壁纸常见 webp / avif，Go 标准库解不了，而**浏览器一定能解**（它能显示
// 就一定能画进 canvas）。所以后端只做搬运，几何与亮度全部交给前端 canvas。
// 这条决定了这个接口的形态：返回**原始字节**，不是「算好的亮度」。
//
// ── 安全 ──────────────────────────────────────────────────────────────
// 挂在 admin 鉴权组下；只允许 http(s)；解析后拒绝环回 / 私网 / 链路本地地址；
// 只接受 `image/*`；有超时与体积上限。DNS 解析与实际拨号之间仍有 TOCTOU 窗口
// （解析后再解析一次），对「管理员自己填的地址」这个威胁模型可以接受，
// 这里如实记下，不假装它是完整防护。
func (s *Server) HandleWallpaperBytes(c *gin.Context) {
	raw := strings.TrimSpace(c.Query("url"))
	if raw == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "缺少 url 参数")
		return
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "壁纸地址无法解析")
		return
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		RespondErrorMsg(c, http.StatusBadRequest, "壁纸地址必须是 http(s)")
		return
	}
	if parsed.Host == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "壁纸地址缺少主机名")
		return
	}
	if err := rejectPrivateWallpaperHost(parsed.Hostname()); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), wallpaperProxyTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		RespondError(c, http.StatusBadGateway, err)
		return
	}
	// 有些图床对没有 UA 的请求直接 403。
	req.Header.Set("User-Agent", "PivotFlow/wallpaper-probe")

	resp, err := wallpaperHTTPClient.Do(req)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadGateway, "取壁纸失败: "+err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		RespondErrorMsg(c, http.StatusBadGateway, fmt.Sprintf("取壁纸失败: 上游返回 %d", resp.StatusCode))
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	if !strings.HasPrefix(contentType, "image/") {
		RespondErrorMsg(c, http.StatusBadGateway, "上游返回的不是图片: "+contentType)
		return
	}

	// 多读 1 字节用来判断是否被截断 —— 截断的图片浏览器解不出来，
	// 与其给它半张图，不如明确报错，让前端退回「不做自适应」。
	body, err := io.ReadAll(io.LimitReader(resp.Body, wallpaperProxyMaxBytes+1))
	if err != nil {
		RespondError(c, http.StatusBadGateway, err)
		return
	}
	if len(body) > wallpaperProxyMaxBytes {
		RespondErrorMsg(c, http.StatusBadGateway, "壁纸超过 16 MiB，已放弃")
		return
	}
	if len(body) == 0 {
		RespondErrorMsg(c, http.StatusBadGateway, "上游返回了空响应")
		return
	}

	// 同一次页面会话里会同时被 canvas 采样和（可能）重试读到，让它可缓存。
	c.Header("Cache-Control", "private, max-age=600")
	c.Data(http.StatusOK, contentType, body)
}

// wallpaperHTTPClient 专用客户端。
//
// 用 `ProxyFromEnvironment` 的克隆版：这样本地开发时 `HTTPS_PROXY` 仍然生效
// （这台机器直连外网要靠 Clash），而生产上不设该变量就是直连。
func newWallpaperHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = wallpaperProxyTimeout
	return &http.Client{
		Transport: transport,
		Timeout:   wallpaperProxyTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("重定向次数过多")
			}
			// 每一跳都要重新过一遍私网判断：图床 302 到 169.254.169.254
			// 是这类接口最典型的被利用方式。
			return rejectPrivateWallpaperHost(req.URL.Hostname())
		},
	}
}

var wallpaperHTTPClient = newWallpaperHTTPClient()

// rejectPrivateWallpaperHost 拒掉指向本机 / 内网的壁纸地址。
//
// 与渠道出站那套（`internal/site/provider/transport.go` 的 `isPrivateAddress`）
// 判断同一批网段，但**故意不复用**：那套是给「用户配置的站点」用的，
// 语义是「允许通过代理绕开」，这里要的是「一律拒绝」。混用会把两边的策略搅在一起。
func rejectPrivateWallpaperHost(host string) error {
	if host == "" {
		return fmt.Errorf("壁纸地址缺少主机名")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("壁纸域名解析失败: %s", host)
	}
	if len(ips) == 0 {
		return fmt.Errorf("壁纸域名解析不到地址: %s", host)
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}
		// IPv4-mapped IPv6（::ffff:127.0.0.1）要先还原成 IPv4，否则下面几条判据都漏。
		addr = addr.Unmap()
		if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() ||
			addr.IsLinkLocalMulticast() || addr.IsUnspecified() || addr.IsMulticast() {
			return fmt.Errorf("壁纸地址指向内网，已拒绝: %s", host)
		}
	}
	return nil
}
