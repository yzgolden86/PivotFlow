package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/protocol"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	"github.com/yzgolden86/PivotFlow/internal/util"

	"github.com/gin-gonic/gin"
)

type deadlineRecorderResponseWriter struct {
	header         http.Header
	body           bytes.Buffer
	statusCode     int
	writeDeadline  time.Time
	deadlineCalled bool
}

func (w *deadlineRecorderResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *deadlineRecorderResponseWriter) Write(data []byte) (int, error) {
	return w.body.Write(data)
}

func (w *deadlineRecorderResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *deadlineRecorderResponseWriter) SetWriteDeadline(t time.Time) error {
	w.deadlineCalled = true
	w.writeDeadline = t
	return nil
}

func TestDisableResponseWriteTimeoutClearsDeadline(t *testing.T) {
	t.Parallel()

	w := &deadlineRecorderResponseWriter{}
	disableResponseWriteTimeout(w, "非流式")

	if !w.deadlineCalled {
		t.Fatal("SetWriteDeadline was not called")
	}
	if !w.writeDeadline.IsZero() {
		t.Fatalf("writeDeadline=%v, want zero time", w.writeDeadline)
	}
}

func TestServer_SetupRoutes_CORSPreflightBypassesAuth(t *testing.T) {
	srv := newInMemoryServer(t)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	srv.SetupRoutes(engine)

	req := httptest.NewRequest(http.MethodOptions, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want %d body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow-origin=%q, want empty", got)
	}
}

func TestServer_SetupRoutes_CORSHeadersOnAuthFailure(t *testing.T) {
	srv := newInMemoryServer(t)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	srv.SetupRoutes(engine)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Origin", "https://example.com")

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want %d body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow-origin=%q, want empty", got)
	}
}

func TestServer_SetupRoutes_V1BetaCORSPreflightBypassesAuth(t *testing.T) {
	srv := newInMemoryServer(t)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	srv.SetupRoutes(engine)

	req := httptest.NewRequest(http.MethodOptions, "/v1beta/models", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want %d body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow-origin=%q, want empty", got)
	}
}

func TestServer_SetupRoutes_V1BetaCORSHeadersOnAuthFailure(t *testing.T) {
	srv := newInMemoryServer(t)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	srv.SetupRoutes(engine)

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models", nil)
	req.Header.Set("Origin", "https://example.com")

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want %d body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow-origin=%q, want empty", got)
	}
}

func TestServer_SetupRoutes_SecurityHeadersAndProtectedSummaries(t *testing.T) {
	srv := newInMemoryServer(t)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	srv.SetupRoutes(engine)

	t.Run("global security headers", func(t *testing.T) {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))

		if got := w.Header().Get("Permissions-Policy"); got != "camera=(), microphone=(), geolocation=()" {
			t.Fatalf("Permissions-Policy=%q", got)
		}
		if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("X-Content-Type-Options=%q", got)
		}
		if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Fatalf("X-Frame-Options=%q", got)
		}
	})

	t.Run("admin responses are not cached", func(t *testing.T) {
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/channels", nil))

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d, want %d", w.Code, http.StatusUnauthorized)
		}
		if got := w.Header().Get("Cache-Control"); got != "no-store" {
			t.Fatalf("Cache-Control=%q, want no-store", got)
		}
		if got := w.Header().Get("Pragma"); got != "no-cache" {
			t.Fatalf("Pragma=%q, want no-cache", got)
		}
	})

	t.Run("login and dashboard responses are not cached", func(t *testing.T) {
		for _, testCase := range []struct {
			method string
			path   string
		}{
			{method: http.MethodPost, path: "/login"},
			{method: http.MethodGet, path: "/dashboard/summary"},
			{method: http.MethodPost, path: "/dashboard/v1/chat/completions"},
		} {
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(testCase.method, testCase.path, nil))
			if got := w.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("%s %s Cache-Control=%q, want no-store", testCase.method, testCase.path, got)
			}
			if got := w.Header().Get("Pragma"); got != "no-cache" {
				t.Errorf("%s %s Pragma=%q, want no-cache", testCase.method, testCase.path, got)
			}
		}
	})

	t.Run("usage summary requires a web session", func(t *testing.T) {
		publicResponse := httptest.NewRecorder()
		engine.ServeHTTP(publicResponse, httptest.NewRequest(http.MethodGet, "/public/summary", nil))
		if publicResponse.Code != http.StatusNotFound {
			t.Fatalf("GET /public/summary status=%d, want %d", publicResponse.Code, http.StatusNotFound)
		}

		dashboardResponse := httptest.NewRecorder()
		engine.ServeHTTP(dashboardResponse, httptest.NewRequest(http.MethodGet, "/dashboard/summary", nil))
		if dashboardResponse.Code != http.StatusUnauthorized {
			t.Fatalf("GET /dashboard/summary status=%d, want %d", dashboardResponse.Code, http.StatusUnauthorized)
		}
	})
}

func TestServer_GetWriteTimeout(t *testing.T) {
	t.Parallel()

	s := &Server{nonStreamTimeout: 10 * time.Second}
	if got := s.GetWriteTimeout(); got != 120*time.Second {
		t.Fatalf("GetWriteTimeout()=%v, want 120s", got)
	}

	s.nonStreamTimeout = 300 * time.Second
	if got := s.GetWriteTimeout(); got != 300*time.Second {
		t.Fatalf("GetWriteTimeout()=%v, want 300s", got)
	}

	s.streamTimeout = 600 * time.Second
	if got := s.GetWriteTimeout(); got != 600*time.Second {
		t.Fatalf("GetWriteTimeout()=%v, want 600s", got)
	}
}

func TestServer_GetWriteTimeout_IncludesProtocolNonStreamTimeout(t *testing.T) {
	t.Parallel()

	s := &Server{
		nonStreamTimeout: 10 * time.Second,
		protocolTimeouts: map[string]protocolTimeoutConfig{
			util.ProtocolOpenAI: {NonStreamTimeout: 300 * time.Second},
		},
	}

	if got := s.GetWriteTimeout(); got != 300*time.Second {
		t.Fatalf("GetWriteTimeout()=%v, want 300s", got)
	}
}

func TestServer_ResolveProtocolTimeouts(t *testing.T) {
	t.Parallel()

	s := &Server{
		firstByteTimeout: 90 * time.Second,
		nonStreamTimeout: 120 * time.Second,
		protocolTimeouts: map[string]protocolTimeoutConfig{
			util.ProtocolAnthropic: {
				FirstByteTimeout: 11 * time.Second,
				NonStreamTimeout: 12 * time.Second,
			},
			util.ProtocolOpenAI: {
				FirstByteTimeout: 21 * time.Second,
				NonStreamTimeout: 22 * time.Second,
			},
		},
	}

	localPlan := protocol.TransformPlan{
		ClientProtocol:   protocol.OpenAI,
		UpstreamProtocol: protocol.Anthropic,
	}
	localTimeouts := s.resolveProtocolTimeouts(localPlan)
	if localTimeouts.FirstByteTimeout != 11*time.Second || localTimeouts.NonStreamTimeout != 12*time.Second {
		t.Fatalf("local timeouts=%+v, want anthropic bucket", localTimeouts)
	}

	upstreamPlan := protocol.TransformPlan{
		ClientProtocol:   protocol.OpenAI,
		UpstreamProtocol: protocol.OpenAI,
	}
	upstreamTimeouts := s.resolveProtocolTimeouts(upstreamPlan)
	if upstreamTimeouts.FirstByteTimeout != 21*time.Second || upstreamTimeouts.NonStreamTimeout != 22*time.Second {
		t.Fatalf("upstream timeouts=%+v, want openai bucket", upstreamTimeouts)
	}
}

func TestServer_ResolveProtocolTimeouts_ZeroProtocolOverrideFallsBackToGlobal(t *testing.T) {
	t.Parallel()

	s := &Server{
		firstByteTimeout: 90 * time.Second,
		nonStreamTimeout: 120 * time.Second,
		protocolTimeouts: map[string]protocolTimeoutConfig{
			util.ProtocolCodex: {},
		},
	}
	plan := protocol.TransformPlan{UpstreamProtocol: protocol.Codex}

	timeouts := s.resolveProtocolTimeouts(plan)
	if timeouts.FirstByteTimeout != 90*time.Second || timeouts.NonStreamTimeout != 120*time.Second {
		t.Fatalf("timeouts=%+v, want global fallback", timeouts)
	}
}

func TestNewServer_ZeroNonStreamTimeoutDisablesTimeout(t *testing.T) {
	t.Parallel()

	store, err := storage.CreateSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("CreateSQLiteStore failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := store.UpdateSetting(ctx, "non_stream_timeout", "0"); err != nil {
		_ = store.Close()
		t.Fatalf("UpdateSetting failed: %v", err)
	}

	srv := NewServer(store)
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			t.Errorf("Server.Shutdown failed: %v", err)
		}
	})

	if srv.nonStreamTimeout != 0 {
		t.Fatalf("nonStreamTimeout=%v, want 0", srv.nonStreamTimeout)
	}
}

func TestNewServer_LoadsProtocolTimeoutOverrides(t *testing.T) {
	t.Parallel()

	store, err := storage.CreateSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("CreateSQLiteStore failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := store.UpdateSetting(ctx, "openai_first_byte_timeout", "9"); err != nil {
		_ = store.Close()
		t.Fatalf("UpdateSetting openai_first_byte_timeout failed: %v", err)
	}
	if err := store.UpdateSetting(ctx, "openai_non_stream_timeout", "33"); err != nil {
		_ = store.Close()
		t.Fatalf("UpdateSetting openai_non_stream_timeout failed: %v", err)
	}

	srv := NewServer(store)
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			t.Errorf("Server.Shutdown failed: %v", err)
		}
	})

	got := srv.protocolTimeouts[util.ProtocolOpenAI]
	if got.FirstByteTimeout != 9*time.Second || got.NonStreamTimeout != 33*time.Second {
		t.Fatalf("openai timeouts=%+v, want 9s/33s", got)
	}
}

func TestServer_GetConfig_FallbackToStore(t *testing.T) {
	_, store, cleanup := setupAdminTestServer(t)
	defer cleanup()

	cfg, err := store.CreateConfig(context.Background(), &model.Config{
		Name:         "ch",
		URLs:         model.ChannelURLs{{URL: "https://api.example.com"}},
		Priority:     1,
		ModelEntries: []model.ModelEntry{{Model: "m1"}},
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("CreateConfig failed: %v", err)
	}

	s := &Server{store: store}
	got, err := s.GetConfig(context.Background(), cfg.ID)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if got.ID != cfg.ID || got.Name != "ch" {
		t.Fatalf("unexpected config: %+v", got)
	}
}

func TestServer_HandleChannelKeys(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	server.store = store

	cfg, err := store.CreateConfig(context.Background(), &model.Config{
		Name:         "ch",
		URLs:         model.ChannelURLs{{URL: "https://api.example.com"}},
		Priority:     1,
		ModelEntries: []model.ModelEntry{{Model: "m1"}},
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("CreateConfig failed: %v", err)
	}
	if err := store.CreateAPIKeysBatch(context.Background(), []*model.APIKey{
		{ChannelID: cfg.ID, KeyIndex: 0, APIKey: "sk-1", KeyStrategy: model.KeyStrategySequential}, //nolint:gosec
	}); err != nil {
		t.Fatalf("CreateAPIKeysBatch failed: %v", err)
	}

	t.Run("invalid_id", func(t *testing.T) {
		c, w := newTestContext(t, newRequest(http.MethodGet, "/admin/channels/abc/keys", nil))
		c.Params = gin.Params{{Key: "id", Value: "abc"}}

		server.HandleChannelKeys(c)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want %d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
		}
	})

	t.Run("ok", func(t *testing.T) {
		c, w := newTestContext(t, newRequest(http.MethodGet, "/admin/channels/1/keys", nil))
		c.Params = gin.Params{{Key: "id", Value: "1"}}

		server.HandleChannelKeys(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
		}

		resp := mustParseAPIResponse[[]*model.APIKey](t, w.Body.Bytes())
		if !resp.Success {
			t.Fatalf("success=false, error=%q", resp.Error)
		}
		if resp.Data == nil || len(resp.Data) != 1 {
			t.Fatalf("keys=%v, want 1", len(resp.Data))
		}
	})
}
