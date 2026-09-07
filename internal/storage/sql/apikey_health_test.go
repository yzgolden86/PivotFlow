package sql_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func TestAPIKeyHealthDebugLogsSurviveReopen(t *testing.T) {
	for _, batch := range []bool{false, true} {
		name := "single"
		if batch {
			name = "batch"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "health-reopen.db")
			store, err := storage.CreateSQLiteStore(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			ctx := t.Context()
			id := createTestChannel(t, ctx, store, "health-reopen")
			createTestAPIKey(t, ctx, store, id, 0)
			at := time.UnixMilli(5000)
			entry := &model.LogEntry{
				ChannelID: id, APIKeyUsed: "sk-test-key", StatusCode: 401,
				Model: "gpt-4", Time: model.JSONTime{Time: at},
				DebugData: &model.DebugLogEntry{CreatedAt: at.Unix(), ReqBody: []byte(`{}`), RespStatus: 401},
			}
			if batch {
				err = store.BatchAddLogs(ctx, []*model.LogEntry{entry})
			} else {
				err = store.AddLog(ctx, entry)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := storage.CreateSQLiteStore(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = reopened.Close() })
			key, err := reopened.GetAPIKey(ctx, id, 0)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := model.ObserveAPIKeyHealth(entry)
			if key.Health != want || key.Disabled {
				t.Fatalf("reopened key health=%+v disabled=%v, want health=%+v and enabled", key.Health, key.Disabled, want)
			}
		})
	}
}

func TestAPIKeyHealthLogPersistenceAndIdentity(t *testing.T) {
	store := newTestStore(t, "key-health.db")
	ctx := context.Background()
	id := createTestChannel(t, ctx, store, "health")
	keys := []*model.APIKey{{ChannelID: id, KeyIndex: 0, APIKey: "key-a"}, {ChannelID: id, KeyIndex: 1, APIKey: "key-b"}, {ChannelID: id, KeyIndex: 2, APIKey: "key-c"}}
	if err := store.CreateAPIKeysBatch(ctx, keys); err != nil {
		t.Fatal(err)
	}
	entry := func(value string, code int, at int64) *model.LogEntry {
		return &model.LogEntry{ChannelID: id, APIKeyUsed: value, StatusCode: code, Model: "gpt-4", Time: model.JSONTime{Time: time.UnixMilli(at)}}
	}
	assertHealth := func(index int, status string, at int64) {
		t.Helper()
		key, err := store.GetAPIKey(ctx, id, index)
		if err != nil {
			t.Fatal(err)
		}
		if key.Health.Status != status || key.Health.CheckedAt != at {
			t.Fatalf("key %d health = %+v", index, key.Health)
		}
	}
	assertHealth(0, "", 0)
	if err := store.BatchAddLogs(ctx, []*model.LogEntry{entry("key-a", 401, 3000), entry("key-a", 200, 2000), entry("key-b", 402, 4000)}); err != nil {
		t.Fatal(err)
	}
	assertHealth(0, "invalid", 3000)
	assertHealth(1, "quota_exhausted", 4000)
	assertHealth(2, "", 0)
	if err := store.AddLog(ctx, entry("key-a", 200, 1000)); err != nil {
		t.Fatal(err)
	}
	assertHealth(0, "invalid", 3000)
	if err := store.DeleteAPIKey(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.CompactKeyIndices(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	if err := store.AddLog(ctx, entry("key-a", 401, 8000)); err != nil {
		t.Fatal(err)
	}
	assertHealth(0, "quota_exhausted", 4000)
	if err := store.AddLog(ctx, entry("key-b", 200, 9000)); err != nil {
		t.Fatal(err)
	}
	assertHealth(0, "healthy", 9000)
	if err := store.ResetKeyCooldown(ctx, id, 0); err != nil {
		t.Fatal(err)
	}
	assertHealth(0, "healthy", 9000)
	all, err := store.GetAllAPIKeys(ctx)
	if err != nil || all[id][0].Health.Status != "healthy" {
		t.Fatalf("all-key read lost health: %v", err)
	}
	rebuilt, err := store.GetAPIKeys(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAllAPIKeys(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAPIKeysBatch(ctx, rebuilt); err != nil {
		t.Fatal(err)
	}
	assertHealth(0, "healthy", 9000)
}
