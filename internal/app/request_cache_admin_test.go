package app

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

func requestCacheWebSession(server *Server, name string, role model.WebRole, tokenID int64) string {
	value := "synthetic-cache-web-session-" + name
	hash := model.HashToken(value)
	server.authService.tokensMux.Lock()
	server.authService.validTokens[hash] = model.WebSession{TokenHash: hash, Role: role, AuthTokenID: tokenID, ExpiresAt: time.Now().Add(time.Hour)}
	server.authService.tokensMux.Unlock()
	return value
}

func requestCacheAdminRequest(t *testing.T, env *proxyTestEnv, method, path, session string, body any) *httptest.ResponseRecorder {
	t.Helper()
	request := newJSONRequest(t, method, path, body)
	request.Header.Set("Authorization", "Bearer "+session)
	response := httptest.NewRecorder()
	env.engine.ServeHTTP(response, request)
	return response
}

func TestRequestCacheAdminObservationIsReadOnlyAndClearSupersedesFills(t *testing.T) {
	env := setupRequestCacheEnv(t, "http://test-unused.invalid", true)
	createRequestCacheToken(t, env, "test-api-key", &model.AuthToken{Description: "owner"})
	cache := env.server.requestCache
	channel, err := env.store.GetConfig(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	old := cache.openTicket(sha256.Sum256([]byte("expired fixture")), "use", "")
	defer cache.closeTicket(old)
	if !cache.put(old, channel, "application/json", []byte("bounded body"), time.Now().Add(-time.Hour)) {
		t.Fatal("expired fixture could not be retained")
	}
	session := requestCacheWebSession(env.server, "admin", model.WebRoleAdmin, 0)
	for range 2 {
		response := requestCacheAdminRequest(t, env, "GET", "/admin/request-cache", session, nil)
		if response.Code != 200 {
			t.Fatalf("admin observation=%d %s", response.Code, response.Body.String())
		}
		var observation requestCacheSnapshot
		mustUnmarshalAPIResponseData(t, response.Body.Bytes(), &observation)
		if observation.Version != 1 || observation.Stats.Entries != 1 || observation.Stats.ExpiredEntries != 1 || observation.Stats.Expired != 0 || observation.Stats.Lookups != 0 {
			t.Fatalf("read endpoint cleaned/probed/changed state: %+v", observation)
		}
	}
	cleared := requestCacheAdminRequest(t, env, "DELETE", "/admin/request-cache", session, nil)
	if cleared.Code != 200 {
		t.Fatalf("admin clear=%d %s", cleared.Code, cleared.Body.String())
	}
	var observation requestCacheSnapshot
	mustUnmarshalAPIResponseData(t, cleared.Body.Bytes(), &observation)
	if observation.Stats.Entries != 0 || observation.Stats.Bytes != 0 || observation.Stats.Clears != 1 || observation.Stats.LastInvalidationReason != "manual_clear" || cache.put(old, channel, "application/json", []byte("late body"), time.Now()) {
		t.Fatalf("clear did not supersede retained/inflight content: %+v", observation)
	}
	token, err := env.store.GetAuthTokenByValue(context.Background(), model.HashToken("test-api-key"))
	if err != nil {
		t.Fatal(err)
	}
	tokenSession := requestCacheWebSession(env.server, "token", model.WebRoleAPIToken, token.ID)
	for _, method := range []string{"GET", "DELETE"} {
		if response := requestCacheAdminRequest(t, env, method, "/admin/request-cache", tokenSession, nil); response.Code != http.StatusForbidden {
			t.Fatalf("API-token web session obtained global cache access: %s %d %s", method, response.Code, response.Body.String())
		}
	}
	if cache.stats(time.Now()).Clears != 1 {
		t.Fatal("forbidden request cleared the cache")
	}
}

func TestRequestCacheSettingsRejectInvalidSingleBatchAndResetWrites(t *testing.T) {
	env := setupRequestCacheEnv(t, "http://test-unused.invalid", false)
	session := requestCacheWebSession(env.server, "settings", model.WebRoleAdmin, 0)
	response := requestCacheAdminRequest(t, env, "PUT", "/admin/settings/request_cache_ttl_seconds", session, map[string]any{"value": "4"})
	if response.Code != 400 {
		t.Fatalf("invalid TTL accepted: %d %s", response.Code, response.Body.String())
	}
	response = requestCacheAdminRequest(t, env, "POST", "/admin/settings/batch", session, map[string]string{"request_cache_ttl_seconds": "5", "request_cache_max_entry_bytes": "2097152"})
	if response.Code != 400 {
		t.Fatalf("invalid cache batch accepted: %d %s", response.Code, response.Body.String())
	}
	setting, err := env.store.GetSetting(context.Background(), "request_cache_ttl_seconds")
	if err != nil || setting.Value != "60" {
		t.Fatalf("invalid batch partially wrote valid companion setting: %+v %v", setting, err)
	}
	store := env.store.(*sqlstore.SQLStore)
	if _, err := store.ExecContext(context.Background(), `UPDATE system_settings SET default_value='0' WHERE key='request_cache_ttl_seconds'`); err != nil {
		t.Fatal(err)
	}
	if err := env.server.configService.RefreshSetting(context.Background(), "request_cache_ttl_seconds"); err != nil {
		t.Fatal(err)
	}
	response = requestCacheAdminRequest(t, env, "POST", "/admin/settings/request_cache_ttl_seconds/reset", session, nil)
	if response.Code != 400 {
		t.Fatalf("invalid persisted reset default accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestRequestCacheStartupRejectsInvalidPersistedPolicy(t *testing.T) {
	for key, value := range map[string]string{"request_cache_enabled": "invalid", "request_cache_ttl_seconds": "9999", "request_cache_max_entries": "0", "request_cache_max_bytes": "536870912", "request_cache_max_entry_bytes": "2097152"} {
		t.Run(key, func(t *testing.T) {
			store, err := storage.CreateSQLiteStore(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.UpdateSetting(context.Background(), key, value); err != nil {
				t.Fatal(err)
			}
			config := NewConfigService(store)
			if err := config.LoadDefaults(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := loadRequestCacheConfig(config); err == nil {
				t.Fatal("invalid persisted cache policy silently enabled or fell back")
			}
		})
	}
}
