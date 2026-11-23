package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

func newRestoreSyncStores(t *testing.T) (*sqlstore.SQLStore, *sqlstore.SQLStore) {
	t.Helper()

	create := func(name string) *sqlstore.SQLStore {
		store, err := CreateSQLiteStore(filepath.Join(t.TempDir(), name+".db"))
		if err != nil {
			t.Fatalf("create %s store: %v", name, err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store.(*sqlstore.SQLStore)
	}

	return create("primary"), create("replica")
}

func createRestoreTestChannel(t *testing.T, ctx context.Context, store *sqlstore.SQLStore, name string) *model.Config {
	t.Helper()

	cfg, err := store.CreateConfig(ctx, &model.Config{
		Name:     name,
		URLs:     model.ChannelURLs{{URL: "https://restore.example.com"}},
		Priority: 1,
		Enabled:  true,
	})
	if err != nil {
		t.Fatalf("create channel %q: %v", name, err)
	}
	return cfg
}

func requireRestoreTestTableCount(t *testing.T, ctx context.Context, store *sqlstore.SQLStore, table string, want int) {
	t.Helper()

	var got int
	query := "SELECT COUNT(*) FROM " + table //nolint:gosec // fixed table names from test callers
	if err := store.QueryRowContext(ctx, query).Scan(&got); err != nil {
		t.Fatalf("count %s rows: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s row count=%d, want %d", table, got, want)
	}
}

func TestSyncManagerRestoreTable_EmptyPrimaryClearsReplica(t *testing.T) {
	ctx := context.Background()

	t.Run("channels", func(t *testing.T) {
		primary, replica := newRestoreSyncStores(t)
		stale := createRestoreTestChannel(t, ctx, replica, "stale-channel")

		if err := NewSyncManager(primary, replica).restoreTable(ctx, "channels"); err != nil {
			t.Fatalf("restore empty channels: %v", err)
		}

		requireRestoreTestTableCount(t, ctx, replica, "channels", 0)
		if _, err := replica.GetConfig(ctx, stale.ID); err == nil {
			t.Fatalf("stale channel %d survived an authoritative empty restore", stale.ID)
		}
	})

	t.Run("auth_tokens", func(t *testing.T) {
		primary, replica := newRestoreSyncStores(t)
		stale := &model.AuthToken{
			Token:       "stale-auth-token",
			Description: "stale replica token",
			IsActive:    true,
		}
		if err := replica.CreateAuthToken(ctx, stale); err != nil {
			t.Fatalf("create stale auth token: %v", err)
		}

		if err := NewSyncManager(primary, replica).restoreTable(ctx, "auth_tokens"); err != nil {
			t.Fatalf("restore empty auth_tokens: %v", err)
		}

		requireRestoreTestTableCount(t, ctx, replica, "auth_tokens", 0)
		if _, err := replica.GetAuthToken(ctx, stale.ID); err == nil {
			t.Fatalf("stale auth token %d survived an authoritative empty restore", stale.ID)
		}
	})
}

func TestSyncManagerRestoreTable_PrimaryReadFailurePreservesReplica(t *testing.T) {
	ctx := context.Background()
	primary, replica := newRestoreSyncStores(t)
	stale := createRestoreTestChannel(t, ctx, replica, "stale-channel")
	createRestoreTestChannel(t, ctx, primary, "primary-channel")

	if _, err := primary.ExecContext(ctx, "ALTER TABLE channels RENAME TO channels_backing"); err != nil {
		t.Fatalf("rename primary channels table: %v", err)
	}
	if _, err := primary.ExecContext(ctx, `
		CREATE VIEW channels AS
		SELECT backing.*, json_extract('not-json', '$') AS restore_error
		FROM channels_backing AS backing
	`); err != nil {
		t.Fatalf("create failing primary view: %v", err)
	}

	err := NewSyncManager(primary, replica).restoreTable(ctx, "channels")
	if err == nil {
		t.Fatal("restore succeeded despite a primary read failure")
	}
	if !strings.Contains(err.Error(), "MySQL 查询失败") {
		t.Fatalf("restore error=%q, want primary query failure", err)
	}

	got, getErr := replica.GetConfig(ctx, stale.ID)
	if getErr != nil {
		t.Fatalf("read preserved replica channel: %v", getErr)
	}
	if got.Name != stale.Name {
		t.Fatalf("preserved channel name=%q, want %q", got.Name, stale.Name)
	}
	requireRestoreTestTableCount(t, ctx, replica, "channels", 1)
}

func TestSyncManagerRestoreTable_DestinationWriteFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	primary, replica := newRestoreSyncStores(t)
	stale := createRestoreTestChannel(t, ctx, replica, "stale-channel")
	createRestoreTestChannel(t, ctx, primary, "primary-channel")

	if _, err := replica.ExecContext(ctx, `
		CREATE TRIGGER reject_primary_channel_insert
		BEFORE INSERT ON channels
		WHEN NEW.name = 'primary-channel'
		BEGIN
			SELECT RAISE(ABORT, 'destination write rejected');
		END
	`); err != nil {
		t.Fatalf("create destination insert trigger: %v", err)
	}

	if err := NewSyncManager(primary, replica).restoreTable(ctx, "channels"); err == nil {
		t.Fatal("restore succeeded despite a destination write failure")
	}

	got, err := replica.GetConfig(ctx, stale.ID)
	if err != nil {
		t.Fatalf("read rolled-back replica channel: %v", err)
	}
	if got.Name != stale.Name {
		t.Fatalf("rolled-back channel name=%q, want %q", got.Name, stale.Name)
	}
	requireRestoreTestTableCount(t, ctx, replica, "channels", 1)
}

func TestSyncManagerRestoreTable_DestinationDeleteFailurePreservesReplica(t *testing.T) {
	ctx := context.Background()
	primary, replica := newRestoreSyncStores(t)
	stale := createRestoreTestChannel(t, ctx, replica, "stale-channel")
	createRestoreTestChannel(t, ctx, primary, "primary-channel")

	if _, err := replica.ExecContext(ctx, `
		CREATE TRIGGER reject_channel_delete
		BEFORE DELETE ON channels
		BEGIN
			SELECT RAISE(ABORT, 'destination delete rejected');
		END
	`); err != nil {
		t.Fatalf("create destination delete trigger: %v", err)
	}

	if err := NewSyncManager(primary, replica).restoreTable(ctx, "channels"); err == nil {
		t.Fatal("restore succeeded despite a destination delete failure")
	}

	got, err := replica.GetConfig(ctx, stale.ID)
	if err != nil {
		t.Fatalf("read replica channel after rejected delete: %v", err)
	}
	if got.Name != stale.Name {
		t.Fatalf("channel name after rejected delete=%q, want %q", got.Name, stale.Name)
	}
	requireRestoreTestTableCount(t, ctx, replica, "channels", 1)
}

func TestSyncManagerRestoreTable_MissingPrimaryTablePreservesReplica(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	primary, replica := newRestoreSyncStores(t)
	stale := createRestoreTestChannel(t, ctx, replica, "stale-channel")

	if _, err := primary.ExecContext(ctx, "ALTER TABLE channels RENAME TO channels_missing"); err != nil {
		t.Fatalf("make primary channels table unavailable: %v", err)
	}

	if err := NewSyncManager(primary, replica).restoreTable(ctx, "channels"); err == nil {
		t.Fatal("restore succeeded despite a missing primary table")
	}

	got, err := replica.GetConfig(ctx, stale.ID)
	if err != nil {
		t.Fatalf("read replica channel after primary schema failure: %v", err)
	}
	if got.Name != stale.Name {
		t.Fatalf("channel name after primary schema failure=%q, want %q", got.Name, stale.Name)
	}
	requireRestoreTestTableCount(t, ctx, replica, "channels", 1)
}
