package model_test

import (
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestObserveAPIKeyHealth(t *testing.T) {
	for _, tc := range []struct {
		name            string
		code            int
		message, status string
	}{
		{"success", 200, "", "healthy"},
		{"auth", 401, "sk-secret-value", "invalid"},
		{"expired SSE", 597, "api_key_expired", "invalid"},
		{"forbidden", 403, "Cloudflare access denied", "access_denied"},
		{"explicit invalid", 403, "invalid_api_key", "invalid"},
		{"payment", 402, "", "quota_exhausted"},
		{"quota", 429, "insufficient_quota", "quota_exhausted"},
		{"balance", 403, "余额不足", "quota_exhausted"},
		{"1308", 596, "", "quota_exhausted"},
		{"rate", 429, "requests per minute quota_exceeded", "rate_limited"},
		{"model", 404, "model does not exist", "upstream_error"},
		{"network", 502, "dial tcp failed", "upstream_error"},
		{"timeout", 598, "", "upstream_error"},
		{"stream", 599, "", "upstream_error"},
		{"cancel", 499, "", ""},
		{"local", 429, "rpm_limited", ""},
		{"skip", 0, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &model.LogEntry{ChannelID: 1, APIKeyUsed: "sk-secret-value", StatusCode: tc.code, Message: tc.message,
				Time: model.JSONTime{Time: time.UnixMilli(1000)}, Duration: 2}
			h, ok := model.ObserveAPIKeyHealth(e)
			if ok != (tc.status != "") || h.Status != tc.status {
				t.Fatalf("got %+v, %v; want %s", h, ok, tc.status)
			}
			if ok && (h.CheckedAt != 3000 || strings.Contains(h.Reason, e.APIKeyUsed)) {
				t.Fatalf("unsafe or incorrect health: %+v", h)
			}
			e.LogSource = model.LogSourceManualTest
			h, ok = model.ObserveAPIKeyHealth(e)
			if ok && h.CheckedAt != 1000 {
				t.Fatal("detection completion time was counted twice")
			}
			e.SkipKeyHealth = true
			if _, ok := model.ObserveAPIKeyHealth(e); ok {
				t.Fatal("local outcome changed health")
			}
		})
	}
}
