package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestProxyHandler_ProtocolRestriction(t *testing.T) {
	srv := newInMemoryServer(t)
	store := srv.store
	authService := srv.authService

	ctx := context.Background()

	// 创建一个限制协议的 Token：只允许 anthropic
	plainToken := "test-restricted-token"
	token := &model.AuthToken{
		Token:            model.HashToken(plainToken),
		Description:      "Protocol restricted token",
		IsActive:         true,
		AllowedProtocols: []string{"anthropic"},
	}
	if err := store.CreateAuthToken(ctx, token); err != nil {
		t.Fatalf("failed to create auth token: %v", err)
	}

	if err := authService.ReloadAuthTokens(); err != nil {
		t.Fatalf("failed to reload after token creation: %v", err)
	}

	tests := []struct {
		name           string
		path           string
		requestBody    map[string]any
		expectedStatus int
		expectError    string
	}{
		{
			name: "anthropic protocol allowed",
			path: "/v1/messages",
			requestBody: map[string]any{
				"model":      "claude-3-5-sonnet-20241022",
				"max_tokens": 1024,
				"messages": []map[string]string{
					{"role": "user", "content": "Hello"},
				},
			},
			expectedStatus: http.StatusServiceUnavailable, // 没有配置渠道，所以503
		},
		{
			name: "openai protocol blocked",
			path: "/v1/chat/completions",
			requestBody: map[string]any{
				"model": "gpt-4",
				"messages": []map[string]string{
					{"role": "user", "content": "Hello"},
				},
			},
			expectedStatus: http.StatusForbidden,
			expectError:    "protocol 'openai' is not allowed for this token",
		},
		{
			name: "gemini protocol blocked",
			path: "/v1beta/models/gemini-1.5-pro:generateContent",
			requestBody: map[string]any{
				"contents": []map[string]any{
					{"role": "user", "parts": []map[string]string{{"text": "Hello"}}},
				},
			},
			expectedStatus: http.StatusForbidden,
			expectError:    "protocol 'gemini' is not allowed for this token",
		},
		{
			name: "codex protocol blocked",
			path: "/v1/responses",
			requestBody: map[string]any{
				"model": "gpt-4o",
				"messages": []map[string]string{
					{"role": "user", "content": "Hello"},
				},
			},
			expectedStatus: http.StatusForbidden,
			expectError:    "protocol 'codex' is not allowed for this token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.requestBody)
			if err != nil {
				t.Fatalf("failed to marshal request body: %v", err)
			}

			req := newRequest(http.MethodPost, tt.path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-restricted-token")

			c, w := newTestContext(t, req)

			// 模拟中间件注入协议元数据
			c.Set("PivotFlow.clientProtocol", detectClientProtocolFromPath(tt.path))
			c.Set("PivotFlow.clientPath", tt.path)

			// 手动注入认证上下文（模拟 RequireAPIAuth 中间件）
			tokenHash := model.HashToken(plainToken)
			c.Set("token_hash", tokenHash)
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
				errorMsg, ok := resp["error"].(string)
				if !ok || errorMsg != tt.expectError {
					t.Errorf("error message = %q, want %q", errorMsg, tt.expectError)
				}
			}
		})
	}
}

func TestProxyHandler_NoProtocolRestriction(t *testing.T) {
	srv := newInMemoryServer(t)
	store := srv.store
	authService := srv.authService

	ctx := context.Background()

	// 创建一个不限制协议的 Token
	plainToken := "test-unrestricted-token"
	token := &model.AuthToken{
		Token:            model.HashToken(plainToken), // 存储哈希值
		Description:      "Unrestricted token",
		IsActive:         true,
		AllowedProtocols: []string{}, // 空列表=无限制
	}
	if err := store.CreateAuthToken(ctx, token); err != nil {
		t.Fatalf("failed to create auth token: %v", err)
	}
	if err := authService.ReloadAuthTokens(); err != nil {
		t.Fatalf("failed to reload after token creation: %v", err)
	}

	tests := []struct {
		name string
		path string
		body map[string]any
	}{
		{
			name: "anthropic allowed",
			path: "/v1/messages",
			body: map[string]any{
				"model":      "claude-3-5-sonnet-20241022",
				"max_tokens": 1024,
				"messages":   []map[string]string{{"role": "user", "content": "Hello"}},
			},
		},
		{
			name: "openai allowed",
			path: "/v1/chat/completions",
			body: map[string]any{
				"model":    "gpt-4",
				"messages": []map[string]string{{"role": "user", "content": "Hello"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			req := newRequest(http.MethodPost, tt.path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-unrestricted-token")

			c, w := newTestContext(t, req)

			// 模拟中间件注入协议元数据
			c.Set("PivotFlow.clientProtocol", detectClientProtocolFromPath(tt.path))
			c.Set("PivotFlow.clientPath", tt.path)

			tokenHash := model.HashToken(plainToken)
			c.Set("token_hash", tokenHash)
			c.Set("token_id", token.ID)

			srv.HandleProxyRequest(c)

			// 无限制的 Token 不应该返回 403 协议错误
			if w.Code == http.StatusForbidden {
				var resp map[string]any
				json.Unmarshal(w.Body.Bytes(), &resp)
				if errMsg, ok := resp["error"].(string); ok && bytes.Contains([]byte(errMsg), []byte("protocol")) {
					t.Errorf("unrestricted token should not block any protocol, got: %s", errMsg)
				}
			}
		})
	}
}

