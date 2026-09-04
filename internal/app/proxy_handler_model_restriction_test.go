package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// 白名单写了别名时，请求 canonical（或组内另一别名）必须同样放行：
// 统一映射组在白名单层面全有或全无，与路由层「canonical 与别名可互换」
// 同口径。未配置渠道时通过白名单的请求止步于 503，被拒的返回 403。
func TestProxyHandler_ModelRestrictionAliasExpansion(t *testing.T) {
	srv := newInMemoryServerWithSettings(t, map[string]string{
		"model_alias_groups": `[
			{"canonical":"glm-5.3","aliases":["z-ai/glm-5.3","GLM-5.3-1M"],"enabled":true}
		]`,
	})

	plainToken := "alias-whitelist-token"
	token := &model.AuthToken{
		Token:         model.HashToken(plainToken),
		Description:   "Alias whitelist token",
		IsActive:      true,
		AllowedModels: []string{"z-ai/glm-5.3"}, // 白名单只写了别名
	}
	if err := srv.store.CreateAuthToken(context.Background(), token); err != nil {
		t.Fatalf("failed to create auth token: %v", err)
	}
	if err := srv.authService.ReloadAuthTokens(); err != nil {
		t.Fatalf("failed to reload auth tokens: %v", err)
	}

	tests := []struct {
		name           string
		requestModel   string
		expectedStatus int
		expectError    string
	}{
		{
			name:           "canonical request passes alias whitelist",
			requestModel:   "glm-5.3",
			expectedStatus: http.StatusServiceUnavailable, // 白名单通过，无渠道可用
		},
		{
			name:           "other alias in group passes via expansion",
			requestModel:   "GLM-5.3-1M",
			expectedStatus: http.StatusServiceUnavailable,
		},
		{
			name:           "unrelated model rejected",
			requestModel:   "gpt-4o",
			expectedStatus: http.StatusForbidden,
			expectError:    "model 'gpt-4o' is not allowed for this token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"model":    tt.requestModel,
				"messages": []map[string]string{{"role": "user", "content": "Hello"}},
			})
			if err != nil {
				t.Fatalf("failed to marshal request body: %v", err)
			}

			req := newRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+plainToken)

			c, w := newTestContext(t, req)
			c.Set("PivotFlow.clientProtocol", detectClientProtocolFromPath("/v1/chat/completions"))
			c.Set("PivotFlow.clientPath", "/v1/chat/completions")
			c.Set("token_hash", model.HashToken(plainToken))
			c.Set("token_id", token.ID)

			srv.HandleProxyRequest(c)

			if w.Code != tt.expectedStatus {
				t.Errorf("status code = %d, want %d; body: %s", w.Code, tt.expectedStatus, w.Body.String())
			}
			if tt.expectError != "" {
				var resp map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to unmarshal response: %v", err)
				}
				if errorMsg, _ := resp["error"].(string); errorMsg != tt.expectError {
					t.Errorf("error message = %q, want %q", errorMsg, tt.expectError)
				}
			}
		})
	}
}
