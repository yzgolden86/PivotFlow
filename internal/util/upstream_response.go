package util

import (
	"html"
	"mime"
	"strings"
)

// 上游返回非 API 响应时（WAF/CDN 拦截页、反代错误页、登录跳转页）的摘要逻辑。
//
// 这类响应体可达上百 KB，并且内嵌 `url(data:font/woff2;base64,...)` 这种长达数万
// 字符、中间没有空白的 token。把它原样拼进 error 会一路传到控制台弹窗，撑爆布局
// （前端那侧的兜底见 styles.css 里 .inline-error 的 overflow-wrap 注释）。所以任何
// 会把上游响应体写进错误信息的路径都必须先过这里。
//
// 摘要优先取 HTML 的 <h1>，再取 <title>——拦截页的可读原因通常就在这两处，
// 取到的往往比截断后的 body 更有价值（例如「访问已被拦截」）。

// SummarizeUpstreamResponseBody 把上游的非预期响应体压成一句可安全展示的摘要。
// 只用于纯展示的场合（渠道测试的 error 字段，原始响应另有 raw_response 可看）。
// 会被再次解析的错误串用 SanitizeUpstreamErrorBody。
func SummarizeUpstreamResponseBody(contentType string, bodyBytes []byte) string {
	body := strings.TrimSpace(string(bodyBytes))
	if body == "" {
		return emptyBodyMessage(contentType)
	}

	if LooksLikeHTMLResponse(contentType, body) {
		if heading := extractHTMLTagText(body, "h1"); heading != "" {
			return heading
		}
		if title := extractHTMLTagText(body, "title"); title != "" {
			return title
		}
	}

	if snippet := normalizeUnexpectedResponseText(stripHTMLTags(body)); snippet != "" {
		return snippet
	}
	if ct := strings.TrimSpace(contentType); ct != "" {
		return "上游返回了非预期响应: " + ct
	}
	return "上游返回了非预期响应"
}

// SanitizeUpstreamErrorBody 用于要拼进 error、之后还会被重新解析的响应体。
//
// 和 SummarizeUpstreamResponseBody 的区别在于对非 HTML 的处理：模型抓取失败后，
// admin_models.go 的 shouldTryNextKeyOnFetchModelsError / shouldCooldownURLOnFetchModelsError
// 会从 error 文本里把 body 抠回来交给 ClassifyHTTPResponseWithMeta，而 1308、结构化配额、
// 模型退役这些判定都要读 JSON 字段。所以非 HTML 的响应体必须原样保留（只做长度上限），
// 不能压缩空白也不能删标签，否则分级会退化成只看状态码。
//
// HTML 一定不是可分类的 API 错误体（分级只会落到状态码），所以拦截页照旧压成一句话——
// 那正是 100 KB 内嵌 base64 字体的来源。
func SanitizeUpstreamErrorBody(contentType string, bodyBytes []byte) string {
	body := strings.TrimSpace(string(bodyBytes))
	if body == "" {
		return emptyBodyMessage(contentType)
	}
	if LooksLikeHTMLResponse(contentType, body) {
		return SummarizeUpstreamResponseBody(contentType, bodyBytes)
	}

	// 够装下真实的上游 JSON 错误体，又不至于让弹窗被几十 KB 撑满。
	return truncateRunes(body, 2000)
}

func emptyBodyMessage(contentType string) string {
	if ct := strings.TrimSpace(contentType); ct != "" {
		return "上游返回空响应体: " + ct
	}
	return "上游返回空响应体"
}

// LooksLikeHTMLResponse 判断响应是网页而不是 API 数据。
// body 传空串表示只按 Content-Type 判断（流式路径拿不到完整 body）。
func LooksLikeHTMLResponse(contentType, body string) bool {
	if ct := strings.TrimSpace(contentType); ct != "" {
		if mediaType, _, err := mime.ParseMediaType(ct); err == nil {
			switch strings.ToLower(mediaType) {
			case "text/html", "application/xhtml+xml":
				return true
			}
		}
	}

	bodyLower := strings.ToLower(body)
	return strings.Contains(bodyLower, "<!doctype html") ||
		strings.Contains(bodyLower, "<html") ||
		strings.Contains(bodyLower, "<body") ||
		strings.Contains(bodyLower, "<title")
}

func extractHTMLTagText(body, tag string) string {
	tagLower := strings.ToLower(tag)
	bodyLower := strings.ToLower(body)
	openIdx := strings.Index(bodyLower, "<"+tagLower)
	if openIdx < 0 {
		return ""
	}

	contentStart := strings.Index(bodyLower[openIdx:], ">")
	if contentStart < 0 {
		return ""
	}
	contentStart += openIdx + 1

	closeIdx := strings.Index(bodyLower[contentStart:], "</"+tagLower+">")
	if closeIdx < 0 {
		return ""
	}

	return normalizeUnexpectedResponseText(stripHTMLTags(body[contentStart : contentStart+closeIdx]))
}

// stripHTMLTags 去掉标签，只留文字。
// <script>/<style> 的**内容**必须整段丢掉：拦截页的 base64 字体就藏在 <style> 里，
// 只删标签的话摘要会变成一串毫无信息量的 base64。
func stripHTMLTags(body string) string {
	var builder strings.Builder
	builder.Grow(len(body))

	rest := body
	for {
		start, end, ok := findRawTextElement(rest)
		if !ok {
			break
		}
		builder.WriteString(rest[:start])
		builder.WriteByte(' ')
		rest = rest[end:]
	}
	builder.WriteString(rest)

	var out strings.Builder
	out.Grow(builder.Len())
	inTag := false
	for _, r := range builder.String() {
		switch r {
		case '<':
			inTag = true
		case '>':
			if inTag {
				inTag = false
				out.WriteByte(' ')
			}
		default:
			if !inTag {
				out.WriteRune(r)
			}
		}
	}

	return html.UnescapeString(out.String())
}

// findRawTextElement 定位第一个 <script>/<style> 元素的起止偏移（含首尾标签）。
// 没有闭合标签时吃掉到结尾——残缺的拦截页也不能把 base64 漏进摘要。
func findRawTextElement(body string) (start, end int, ok bool) {
	lower := strings.ToLower(body)
	start, name := -1, ""
	for _, candidate := range []string{"script", "style"} {
		if idx, found := indexRawTextTag(lower, candidate); found && (start < 0 || idx < start) {
			start, name = idx, candidate
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	closeIdx := strings.Index(lower[start:], "</"+name+">")
	if closeIdx < 0 {
		return start, len(body), true
	}
	return start, start + closeIdx + len("</"+name+">"), true
}

// indexRawTextTag 找 `<name` 开标签的位置，跳过 <styled-box> 这类同前缀标签名。
// 必须继续往后扫而不是遇到同前缀就放弃：`<styled-box></styled-box><style>…`
// 里真正的 <style> 在后面，放弃就等于把 base64 放进摘要。
func indexRawTextTag(lower, name string) (int, bool) {
	needle := "<" + name
	for offset := 0; ; {
		idx := strings.Index(lower[offset:], needle)
		if idx < 0 {
			return 0, false
		}
		idx += offset
		next := idx + len(needle)
		if next >= len(lower) {
			return idx, true
		}
		switch lower[next] {
		case '>', ' ', '\t', '\n', '\r', '/':
			return idx, true
		}
		offset = next
	}
}

func normalizeUnexpectedResponseText(text string) string {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if text == "" {
		return ""
	}
	return truncateRunes(text, 200)
}

func truncateRunes(text string, maxRunes int) string {
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes]) + "..."
}
