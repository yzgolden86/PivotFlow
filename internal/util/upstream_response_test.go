package util

import (
	"strings"
	"testing"
)

// 真实故障样本的结构：403 拦截页，无 <h1>，可读原因在 <title>，
// <style> 里塞了 url(data:font/woff2;base64,...) 这种数万字符不含空白的 token。
func blockPageHTML(base64Len int) string {
	return `<html lang="zh"><head> <title>访问已被拦截</title>` +
		`<style>@font-face{font-family:x;src:url(data:font/woff2;base64,` +
		strings.Repeat("A", base64Len) + `)}</style></head>` +
		`<body><p>你的请求被安全策略拦截</p></body></html>`
}

func TestSummarizeUpstreamResponseBody(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		want        string
	}{
		{"取 h1 优先于 title", "text/html", `<html><head><title>标题</title></head><body><h1>被拦截</h1></body></html>`, "被拦截"},
		{"无 h1 回落 title", "text/html; charset=utf-8", `<html><head><title>访问已被拦截</title></head><body><p>细节</p></body></html>`, "访问已被拦截"},
		{"无 h1 无 title 取正文", "text/html", `<html><body><p>Bad&nbsp;gateway</p></body></html>`, "Bad gateway"},
		{"空响应体带 Content-Type", "text/html", "", "上游返回空响应体: text/html"},
		{"空响应体无 Content-Type", "", "   ", "上游返回空响应体"},
		{"纯文本原样折叠空白", "text/plain", "upstream\n\n  error", "upstream error"},
		{"JSON 也会被折叠（仅展示用途）", "application/json", "{\n  \"error\": \"bad key\"\n}", `{ "error": "bad key" }`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SummarizeUpstreamResponseBody(tt.contentType, []byte(tt.body)); got != tt.want {
				t.Fatalf("SummarizeUpstreamResponseBody(%q, %q) = %q, want %q", tt.contentType, tt.body, got, tt.want)
			}
		})
	}
}

// 这个用例就是用户报的 bug：100 KB 拦截页把「添加渠道」弹窗抻得极宽。
func TestSummarizeUpstreamResponseBodyDropsBase64FontPayload(t *testing.T) {
	body := blockPageHTML(42284)
	got := SummarizeUpstreamResponseBody("text/html", []byte(body))

	if got != "访问已被拦截" {
		t.Fatalf("摘要应为 title 文本，实际 = %q", got)
	}
	if strings.Contains(got, "AAAA") {
		t.Fatal("摘要里不能出现 base64 载荷")
	}
}

// <style>/<script> 的内容必须整段丢掉，不是只删标签。只删标签的话
// base64 会变成正文，摘要就成了一串无信息量的 A。
func TestStripHTMLTagsDropsRawTextElementContent(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"style 内容整段丢弃", `<style>.a{content:"AAAA"}</style><p>hi</p>`, "hi"},
		{"script 内容整段丢弃", `<script>var s="AAAA";</script><p>hi</p>`, "hi"},
		{"未闭合 style 吃到结尾", `<p>hi</p><style>.a{content:"AAAA"}`, "hi"},
		{"多个 raw 元素都丢弃", `<style>AAAA</style><p>hi</p><script>BBBB</script><p>there</p>`, "hi there"},
		{"同前缀标签名不误伤", `<styled-box>keep</styled-box>`, "keep"},
		{"同前缀标签名后面的真 style 仍要丢", `<styled-box>keep</styled-box><style>AAAA</style>`, "keep"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeUnexpectedResponseText(stripHTMLTags(tt.body))
			if got != tt.want {
				t.Fatalf("stripHTMLTags(%q) 归一后 = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestLooksLikeHTMLResponse(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		want        bool
	}{
		{"Content-Type 判定（空 body）", "text/html; charset=utf-8", "", true},
		{"xhtml 也算", "application/xhtml+xml", "", true},
		{"JSON 不算", "application/json", `{"error":"x"}`, false},
		{"无 Content-Type 靠 body 特征", "", "<!DOCTYPE html><html></html>", true},
		{"Content-Type 不可解析时看 body", "not a media type", "<html>", true},
		{"纯文本不算", "text/plain", "upstream error", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooksLikeHTMLResponse(tt.contentType, tt.body); got != tt.want {
				t.Fatalf("LooksLikeHTMLResponse(%q, %q) = %v, want %v", tt.contentType, tt.body, got, tt.want)
			}
		})
	}
}

func TestNormalizeUnexpectedResponseTextCapsAt200Runes(t *testing.T) {
	got := normalizeUnexpectedResponseText(strings.Repeat("汉", 500))
	if want := strings.Repeat("汉", 200) + "..."; got != want {
		t.Fatalf("超长文本应截断到 200 个字符加省略号，实际长度 = %d", len([]rune(got)))
	}
	if got := normalizeUnexpectedResponseText("  \n\t "); got != "" {
		t.Fatalf("全空白应返回空串，实际 = %q", got)
	}
}

// SanitizeUpstreamErrorBody 与 Summarize 的关键区别：非 HTML 响应体原样保留。
// admin_models.go 会从 error 文本里把 body 抠回来做错误分级，改写 JSON 会让
// 1308、结构化配额、模型退役这些判定退化成只看状态码。
func TestSanitizeUpstreamErrorBodyPreservesJSON(t *testing.T) {
	body := "{\n  \"error\": {\n    \"code\": 1308,\n    \"message\": \"quota exceeded\"\n  }\n}"
	got := SanitizeUpstreamErrorBody("application/json", []byte(body))
	if got != body {
		t.Fatalf("JSON 错误体必须原样保留，得到 %q", got)
	}
}

func TestSanitizeUpstreamErrorBodySummarizesHTML(t *testing.T) {
	got := SanitizeUpstreamErrorBody("text/html", []byte(blockPageHTML(42284)))
	if got != "访问已被拦截" {
		t.Fatalf("HTML 拦截页应压成一句话，实际 = %q", got)
	}
}

func TestSanitizeUpstreamErrorBodyCapsLength(t *testing.T) {
	got := SanitizeUpstreamErrorBody("application/json", []byte(strings.Repeat("A", 100000)))
	if runes := len([]rune(got)); runes != 2003 {
		t.Fatalf("非 HTML 长响应应截到 2000 字符加省略号，实际 %d 个字符", runes)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatal("截断后应带省略号")
	}
}

func TestSanitizeUpstreamErrorBodyEmpty(t *testing.T) {
	if got := SanitizeUpstreamErrorBody("application/json", nil); got != "上游返回空响应体: application/json" {
		t.Fatalf("空响应体提示不对：%q", got)
	}
}
