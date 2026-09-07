package model

import (
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/util"
)

// ObserveAPIKeyHealth classifies evidence, not a permanent judgment about a credential.
// Reasons are fixed text: upstream errors can echo credentials or user content.
func ObserveAPIKeyHealth(e *LogEntry) (APIKeyHealth, bool) {
	if e == nil || e.SkipKeyHealth || e.ChannelID <= 0 || e.APIKeyUsed == "" || e.StatusCode == 0 || e.StatusCode == util.StatusClientClosedRequest {
		return APIKeyHealth{}, false
	}
	message := strings.ToLower(e.Message)
	if strings.Contains(message, "rpm_limited") || strings.Contains(message, "concurrency_limited") || strings.Contains(message, "context canceled") {
		return APIKeyHealth{}, false
	}
	h := APIKeyHealth{StatusCode: e.StatusCode}
	switch {
	case e.StatusCode >= 200 && e.StatusCode < 300:
		h.Status, h.Reason = "healthy", "最近一次请求成功；不代表所有模型可用或余额充足"
	case e.StatusCode == util.StatusQuotaExceeded || e.StatusCode == 402 || containsHealthSignal(message,
		"insufficient_quota", "insufficient_balance", "insufficient balance", "credit balance is too low", "balance exhausted", "余额不足", "额度不足", "额度已用尽", "配额已耗尽"):
		h.Status, h.Reason = "quota_exhausted", "上游提示余额或配额不足，请到服务商确认额度及恢复时间"
	case e.StatusCode == 401 || containsHealthSignal(message,
		"invalid_api_key", "api_key_expired", "key has expired", "invalid api key", "incorrect api key", "api key is invalid", "api key revoked", "密钥已失效", "令牌已过期", "无效的令牌"):
		h.Status, h.Reason = "invalid", "认证失败，Key 可能无效、过期或已撤销，请核对后复检"
	case e.StatusCode == 403:
		h.Status, h.Reason = "access_denied", "访问被拒绝，可能是模型权限、IP 限制或站点防护，不能据此认定 Key 失效"
	case e.StatusCode == 429 || containsHealthSignal(message, "rate_limit_error", "rate_limit_exceeded"):
		h.Status, h.Reason = "rate_limited", "上游暂时限流，请稍后复检；不代表 Key 已失效"
	default:
		h.Status, h.Reason = "upstream_error", "请求未成功，可能是网络、模型或上游服务问题；不代表 Key 已失效"
	}
	observedAt := e.Time.Time
	if observedAt.IsZero() {
		observedAt = time.Now()
	} else if source := NormalizeStoredLogSource(e.LogSource); source == LogSourceProxy || source == LogSourceManualChat {
		// Proxy logs use request start time; detection logs already use completion time.
		if e.Duration > 0 {
			observedAt = observedAt.Add(time.Duration(e.Duration * float64(time.Second)))
		}
	}
	h.CheckedAt = observedAt.UnixMilli()
	return h, true
}

func containsHealthSignal(message string, signals ...string) bool {
	for _, signal := range signals {
		if strings.Contains(message, signal) {
			return true
		}
	}
	return false
}
