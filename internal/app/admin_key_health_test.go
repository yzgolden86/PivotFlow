package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestChannelKeyHealthSnapshotAndIdentity(t *testing.T) {
	srv, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	ctx := context.Background()
	cfg, err := store.CreateConfig(ctx, &model.Config{Name: "key-health", Enabled: true,
		URLs:         model.ChannelURLs{{URL: "https://example.invalid"}},
		ModelEntries: []model.ModelEntry{{Model: "model-1"}, {Model: "hidden", Disabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKeysBatch(ctx, []*model.APIKey{
		{ChannelID: cfg.ID, KeyIndex: 0, APIKey: "sk-health-secret-a", Note: "primary"},
		{ChannelID: cfg.ID, KeyIndex: 1, APIKey: "sk-health-secret-b"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.AddLog(ctx, &model.LogEntry{ChannelID: cfg.ID, APIKeyUsed: "sk-health-secret-b", StatusCode: 401,
		Model: "model-1", Time: model.JSONTime{Time: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	channelID := fmt.Sprint(cfg.ID)
	c, w := newTestContext(t, newRequest(http.MethodGet, "/admin/channels/"+channelID+"/key-health", nil))
	c.Params = gin.Params{{Key: "id", Value: channelID}}
	srv.HandleChannelKeyHealth(c)
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("snapshot status/headers: %d %v", w.Code, w.Header())
	}
	if strings.Contains(w.Body.String(), "sk-health-secret") {
		t.Fatal("snapshot exposed a credential")
	}
	resp := mustParseAPIResponse[struct {
		Models []string               `json:"models"`
		Keys   []channelKeyHealthItem `json:"keys"`
	}](t, w.Body.Bytes())
	if len(resp.Data.Models) != 1 || len(resp.Data.Keys) != 2 || resp.Data.Keys[0].Note != "primary" ||
		resp.Data.Keys[0].Health.CheckedAt != 0 || resp.Data.Keys[1].Health.Status != "invalid" {
		t.Fatalf("snapshot: %+v", resp.Data)
	}
	staleID := resp.Data.Keys[0].ID
	if err := store.DeleteAPIKey(ctx, cfg.ID, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.CompactKeyIndices(ctx, cfg.ID, 0); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"disable", "enable", "delete", "test"} {
		t.Run(operation, func(t *testing.T) {
			payload := map[string]any{"key_index": 0, "key_id": staleID, "model": "model-1", "client_protocol": "openai"}
			c, w := newTestContext(t, newJSONRequest(t, http.MethodPost, "/admin/channels/"+channelID+"/key-"+operation, payload))
			c.Params = gin.Params{{Key: "id", Value: channelID}, {Key: "keyIndex", Value: "0"}}
			switch operation {
			case "disable":
				srv.HandleAPIKeyDisable(c)
			case "enable":
				srv.HandleAPIKeyEnable(c)
			case "test":
				srv.HandleChannelTest(c)
			case "delete":
				c.Request = newRequest(http.MethodDelete, fmt.Sprintf("/admin/channels/%s/keys/0?key_id=%d", channelID, staleID), nil)
				srv.HandleDeleteAPIKey(c)
			}
			if w.Code != http.StatusConflict {
				t.Fatalf("stale %s status=%d, body=%s", operation, w.Code, w.Body.String())
			}
			keys, err := store.GetAPIKeys(ctx, cfg.ID)
			if err != nil || len(keys) != 1 || keys[0].Disabled || keys[0].Health.Status != "invalid" {
				t.Fatalf("remaining key changed: %+v err=%v", keys, err)
			}
		})
	}
}

func TestChannelKeyHealthProbePersistsSelectedKeyAndRecovery(t *testing.T) {
	var calls atomic.Int32
	upstream := newTestHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-health-selected" {
			t.Error("probe used the wrong key")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			// Deliberately misleading HTTP status: business error is still a failure.
			_, _ = w.Write([]byte(`{"error":{"type":"insufficient_quota","message":"insufficient_quota"}}`))
		} else {
			_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
		}
	}))
	defer upstream.Close()
	srv := newInMemoryServer(t)
	srv.client = upstream.Client()
	ctx := context.Background()
	cfg, err := srv.store.CreateConfig(ctx, &model.Config{Name: "health-probe", Enabled: true,
		URLs: model.ChannelURLs{{URL: upstream.URL, Protocols: []string{"openai"}}}, ModelEntries: []model.ModelEntry{{Model: "model-1"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.CreateAPIKeysBatch(ctx, []*model.APIKey{
		{ChannelID: cfg.ID, KeyIndex: 0, APIKey: "sk-health-unused"},
		{ChannelID: cfg.ID, KeyIndex: 1, APIKey: "sk-health-selected", Disabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	keys, err := srv.store.GetAPIKeys(ctx, cfg.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"quota_exhausted", "healthy"} {
		c, w := newTestContext(t, newJSONRequest(t, http.MethodPost, fmt.Sprintf("/admin/channels/%d/test", cfg.ID), map[string]any{
			"model": "model-1", "client_protocol": "openai", "stream": false, "key_index": 1, "key_id": keys[1].ID, "max_tokens": 64,
		}))
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(cfg.ID)}}
		srv.HandleChannelTest(c)
		if w.Code != http.StatusOK {
			t.Fatalf("probe: %d %s", w.Code, w.Body.String())
		}
		resp := mustParseAPIResponse[struct {
			Health model.APIKeyHealth `json:"key_health"`
		}](t, w.Body.Bytes())
		if resp.Data.Health.Status != want {
			t.Fatalf("probe health=%+v want=%s", resp.Data.Health, want)
		}
		stored, err := srv.store.GetAPIKeys(ctx, cfg.ID)
		if err != nil || stored[0].Health.CheckedAt != 0 || stored[1].Health.Status != want || !stored[1].Disabled {
			t.Fatalf("persisted health/disabled: %+v err=%v", stored, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("unexpected upstream calls: %d", calls.Load())
	}
}

func TestDetectionKeyHealthSkipsLocalLimitsAndSoftSuccess(t *testing.T) {
	cfg := &model.Config{ID: 1}
	for _, flag := range []string{"rpm_limited", "concurrency_limited"} {
		entry := detectionLogFromResult(cfg, model.LogSourceManualTest, "m", "m", "key", "", "", map[string]any{
			"success": false, "status_code": 429, flag: true,
		})
		if _, ok := model.ObserveAPIKeyHealth(entry); ok {
			t.Fatalf("local %s overwrote key health", flag)
		}
	}
	entry := detectionLogFromResult(cfg, model.LogSourceManualTest, "m", "m", "key", "", "", map[string]any{
		"success": false, "status_code": 200, "message": "stale success message", "error": "response body unreadable",
	})
	health, ok := model.ObserveAPIKeyHealth(entry)
	if !ok || health.Status != "upstream_error" || entry.Message != "response body unreadable" {
		t.Fatalf("soft failure: %+v", health)
	}
}
