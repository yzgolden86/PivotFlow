package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestSystemAccessTokenCreateReturnsPlaintextOnce(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()

	c, recorder := newTestContext(t, newJSONRequest(t, http.MethodPost, "/admin/system-access-tokens", map[string]any{
		"description": "diagnostic bot",
		"scopes":      []string{model.SystemAccessScopeChannelsRead},
	}))
	server.HandleCreateSystemAccessToken(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var created struct {
		Success bool `json:"success"`
		Data    struct {
			ID    int64  `json:"id"`
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !created.Success || created.Data.ID <= 0 || !strings.HasPrefix(created.Data.Token, "pf_sys_") {
		t.Fatalf("unexpected create response: %+v", created)
	}
	stored, err := store.GetSystemAccessTokenByHash(context.Background(), model.HashSystemAccessToken(created.Data.Token))
	if err != nil {
		t.Fatalf("read stored token: %v", err)
	}
	if stored.Token == created.Data.Token || len(stored.Token) != 64 {
		t.Fatalf("stored token must be a SHA-256 digest, got %q", stored.Token)
	}

	c, recorder = newTestContext(t, httptest.NewRequest(http.MethodGet, "/admin/system-access-tokens", nil))
	server.HandleListSystemAccessTokens(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), created.Data.Token) || strings.Contains(recorder.Body.String(), stored.Token) {
		t.Fatalf("list response leaked token material: %s", recorder.Body.String())
	}
}

func TestSystemAPILogsOnlyReturnsErrorsAndRedactsSecrets(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	now := time.Now()
	if err := store.AddLog(context.Background(), &model.LogEntry{Time: model.JSONTime{Time: now.Add(-time.Second)}, StatusCode: http.StatusOK, Message: "success", LogSource: model.LogSourceProxy}); err != nil {
		t.Fatalf("add success log: %v", err)
	}
	if err := store.AddLog(context.Background(), &model.LogEntry{Time: model.JSONTime{Time: now}, StatusCode: http.StatusBadGateway, Message: "POST https://secret.example/v1 Authorization: Bearer exposed-secret", LogSource: model.LogSourceProxy}); err != nil {
		t.Fatalf("add error log: %v", err)
	}
	c, recorder := newTestContext(t, httptest.NewRequest(http.MethodGet, "/system-api/logs?range=today&limit=1", nil))
	server.HandleSystemAPILogs(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("logs status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Success bool `json:"success"`
		Count   int  `json:"count"`
		Data    []struct {
			StatusCode int    `json:"status_code"`
			Message    string `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode logs response: %v", err)
	}
	if !response.Success || response.Count != 1 || len(response.Data) != 1 || response.Data[0].StatusCode != http.StatusBadGateway {
		t.Fatalf("unexpected error logs response: %+v", response)
	}
	if strings.Contains(response.Data[0].Message, "secret.example") || strings.Contains(response.Data[0].Message, "exposed-secret") {
		t.Fatalf("diagnostic log leaked sensitive content: %q", response.Data[0].Message)
	}
}

func runSystemAccessMiddleware(t *testing.T, server *Server, scope string, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	engine := gin.New()
	engine.Any("/test", server.RequireSystemAccessToken(scope), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.ServeHTTP(w, req)
	return w
}

func TestSystemAccessTokenMiddlewareIsBearerOnlyAndScopeBound(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	plain := "pf_sys_middleware_secret"
	token := &model.SystemAccessToken{Token: model.HashSystemAccessToken(plain), TokenHint: model.MaskSystemAccessToken(plain), Description: "reader", Scopes: []string{model.SystemAccessScopeChannelsRead}, CreatedAt: time.Now().UnixMilli(), IsActive: true}
	if err := store.CreateSystemAccessToken(context.Background(), token); err != nil {
		t.Fatalf("create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	if w := runSystemAccessMiddleware(t, server, model.SystemAccessScopeChannelsRead, req); w.Code != http.StatusNoContent {
		t.Fatalf("valid status=%d body=%s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-API-Key", plain)
	if w := runSystemAccessMiddleware(t, server, model.SystemAccessScopeChannelsRead, req); w.Code != http.StatusUnauthorized {
		t.Fatalf("x-api-key status=%d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/test?key="+plain, nil)
	if w := runSystemAccessMiddleware(t, server, model.SystemAccessScopeChannelsRead, req); w.Code != http.StatusUnauthorized {
		t.Fatalf("query key status=%d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	if w := runSystemAccessMiddleware(t, server, model.SystemAccessScopeLogsRead, req); w.Code != http.StatusForbidden {
		t.Fatalf("scope status=%d", w.Code)
	}
}

func TestSystemAccessTokenMiddlewareRejectsExpiredAndInactive(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	plain := "pf_sys_expired"
	token := &model.SystemAccessToken{Token: model.HashSystemAccessToken(plain), TokenHint: model.MaskSystemAccessToken(plain), Description: "expired", Scopes: []string{model.SystemAccessScopeChannelsRead}, CreatedAt: time.Now().UnixMilli(), ExpiresAt: time.Now().Add(-time.Minute).UnixMilli(), IsActive: true}
	if err := store.CreateSystemAccessToken(context.Background(), token); err != nil {
		t.Fatalf("create token: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	if w := runSystemAccessMiddleware(t, server, model.SystemAccessScopeChannelsRead, req); w.Code != http.StatusUnauthorized {
		t.Fatalf("expired status=%d", w.Code)
	}
}
