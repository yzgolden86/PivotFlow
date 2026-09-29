package app

import (
	"strings"
	"testing"
)

// 守的是「壁纸取图接口不许被当成 SSRF 跳板」。
// 这个接口会替**管理员填的地址**发起出站请求，所以私网判断是它的核心安全属性，
// 必须有守卫钉住 —— 以后有人为了「让内网壁纸也能用」把它放开，这里会红。
//
// 全部用 IP 字面量，不写域名：`net.LookupIP` 对字面量直接返回、不查 DNS，
// 这样测试不依赖网络。
func TestRejectPrivateWallpaperHost(t *testing.T) {
	blocked := []struct {
		host string
		why  string
	}{
		{"127.0.0.1", "环回"},
		{"127.1.2.3", "环回网段"},
		{"::1", "IPv6 环回"},
		{"0.0.0.0", "未指定地址"},
		{"10.0.0.1", "私网 A"},
		{"172.16.5.4", "私网 B"},
		{"192.168.1.1", "私网 C"},
		{"169.254.169.254", "链路本地（云元数据服务，最典型的 SSRF 目标）"},
		{"::ffff:127.0.0.1", "IPv4-mapped IPv6 环回 —— 不 Unmap 就会漏"},
		{"fe80::1", "IPv6 链路本地"},
	}
	for _, tc := range blocked {
		if err := rejectPrivateWallpaperHost(tc.host); err == nil {
			t.Errorf("rejectPrivateWallpaperHost(%q) = nil，但它应当被拒绝（%s）", tc.host, tc.why)
		}
	}

	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111"}
	for _, host := range allowed {
		if err := rejectPrivateWallpaperHost(host); err != nil {
			t.Errorf("rejectPrivateWallpaperHost(%q) = %v，公网地址不该被拒", host, err)
		}
	}

	if err := rejectPrivateWallpaperHost(""); err == nil {
		t.Error("空主机名应当被拒绝")
	}
}

// 错误信息要能让人看懂「为什么被拒」，否则管理员只会看到「取壁纸失败」。
func TestRejectPrivateWallpaperHostMessage(t *testing.T) {
	err := rejectPrivateWallpaperHost("192.168.1.1")
	if err == nil {
		t.Fatal("应当被拒绝")
	}
	if !strings.Contains(err.Error(), "内网") {
		t.Errorf("拒绝原因应当点明是内网，实际：%v", err)
	}
}
