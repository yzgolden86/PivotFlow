package sql_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

func TestRestoreSiteAccountModelsExactStateSurvivesReopen(t *testing.T) {
	store := newTestStore(t, "site-restore-models.db")
	ss := store.(*sqlstore.SQLStore)
	ctx := context.Background()
	site := restoreTestCreateSite(t, ctx, store, "restore-models")
	account := restoreTestCreateAccount(t, ctx, store, site.ID, "restore-models-account")

	if err := store.ReplaceSiteAccountModels(ctx, account.ID, []model.SiteAccountModel{{
		Model:     "retained",
		RouteType: "target_route",
		Source:    "target_source",
	}}); err != nil {
		t.Fatalf("seed retained model: %v", err)
	}
	const targetLastSeenAt int64 = 321
	const targetCreatedAt int64 = 123
	if _, err := ss.ExecContext(ctx, `UPDATE site_account_models
		SET route_type=?, source=?, disabled=?, stale=?, last_seen_at=?, created_at=?, updated_at=?
		WHERE site_account_id=? AND model=?`,
		"target_route", "target_source", false, true, targetLastSeenAt, targetCreatedAt, 456,
		account.ID, "retained",
	); err != nil {
		t.Fatalf("seed target model state: %v", err)
	}

	beforeRestore := time.Now().UnixMilli()
	if err := store.RestoreSiteAccountModels(ctx, account.ID, []model.SiteAccountModel{
		{
			SiteAccountID: 999,
			Model:         "retained",
			RouteType:     "restore_route",
			Source:        "restore_source",
			Disabled:      true,
			Stale:         false,
			LastSeenAt:    9001,
			CreatedAt:     8001,
			UpdatedAt:     7001,
		},
		{
			SiteAccountID: 999,
			Model:         "fresh",
			RouteType:     "restore_fresh_route",
			Source:        "restore_fresh_source",
			Disabled:      true,
			Stale:         true,
			LastSeenAt:    6001,
			CreatedAt:     5001,
			UpdatedAt:     4001,
		},
	}); err != nil {
		t.Fatalf("restore models: %v", err)
	}

	restored := restoreTestListModels(t, ctx, store, account.ID)
	if len(restored) != 2 {
		t.Fatalf("restored model count=%d, want 2: %+v", len(restored), restored)
	}
	retained := restoreTestFindModel(t, restored, "retained")
	if retained.RouteType != "restore_route" || retained.Source != "restore_source" ||
		!retained.Disabled || retained.Stale {
		t.Fatalf("restored flags/metadata do not exactly match payload: %+v", retained)
	}
	if retained.LastSeenAt != targetLastSeenAt || retained.CreatedAt != targetCreatedAt {
		t.Fatalf("retained observations were not preserved: last_seen_at=%d created_at=%d, want %d/%d",
			retained.LastSeenAt, retained.CreatedAt, targetLastSeenAt, targetCreatedAt)
	}
	if retained.UpdatedAt < beforeRestore {
		t.Fatalf("retained model was not marked updated by restore: %+v", retained)
	}
	fresh := restoreTestFindModel(t, restored, "fresh")
	if fresh.RouteType != "restore_fresh_route" || fresh.Source != "restore_fresh_source" ||
		!fresh.Disabled || !fresh.Stale {
		t.Fatalf("fresh model flags/metadata do not exactly match payload: %+v", fresh)
	}
	if fresh.LastSeenAt != 0 {
		t.Fatalf("fresh model last_seen_at=%d, want 0", fresh.LastSeenAt)
	}
	if fresh.CreatedAt < beforeRestore || fresh.UpdatedAt < beforeRestore {
		t.Fatalf("fresh model should receive new creation/update times: %+v", fresh)
	}

	dbPath := restoreTestSQLitePath(t, ctx, ss)
	if err := store.Close(); err != nil {
		t.Fatalf("close store before reopen: %v", err)
	}
	reopened, err := storage.CreateSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reloaded := restoreTestListModels(t, ctx, reopened, account.ID)
	if !reflect.DeepEqual(reloaded, restored) {
		t.Fatalf("restored models changed after reopen:\n got %+v\nwant %+v", reloaded, restored)
	}
}

func TestRestoreSiteAccountModelsEmptyClearsOnlySelectedAccount(t *testing.T) {
	store := newTestStore(t, "site-restore-empty.db")
	ctx := context.Background()
	site := restoreTestCreateSite(t, ctx, store, "restore-empty")
	selected := restoreTestCreateAccount(t, ctx, store, site.ID, "selected")
	other := restoreTestCreateAccount(t, ctx, store, site.ID, "other")

	if err := store.ReplaceSiteAccountModels(ctx, selected.ID, []model.SiteAccountModel{{
		Model: "selected-model", RouteType: "openai_chat", Source: "models_endpoint",
	}}); err != nil {
		t.Fatalf("seed selected account: %v", err)
	}
	if err := store.ReplaceSiteAccountModels(ctx, other.ID, []model.SiteAccountModel{{
		Model: "other-model", RouteType: "openai_chat", Source: "models_endpoint",
	}}); err != nil {
		t.Fatalf("seed other account: %v", err)
	}

	if err := store.RestoreSiteAccountModels(ctx, selected.ID, nil); err != nil {
		t.Fatalf("restore empty selected list: %v", err)
	}
	if selectedModels := restoreTestListModels(t, ctx, store, selected.ID); len(selectedModels) != 0 {
		t.Fatalf("selected account should have no models, got %+v", selectedModels)
	}
	otherModels := restoreTestListModels(t, ctx, store, other.ID)
	if len(otherModels) != 1 || otherModels[0].Model != "other-model" {
		t.Fatalf("empty restore changed another account: %+v", otherModels)
	}
}

func TestRestoreSiteAccountModelsRollsBackOnDuplicateAndWriteFailure(t *testing.T) {
	t.Run("duplicate input key", func(t *testing.T) {
		store := newTestStore(t, "site-restore-duplicate.db")
		ctx := context.Background()
		site := restoreTestCreateSite(t, ctx, store, "restore-duplicate")
		account := restoreTestCreateAccount(t, ctx, store, site.ID, "duplicate")
		restoreTestSeedOriginalModel(t, ctx, store, account.ID)

		err := store.RestoreSiteAccountModels(ctx, account.ID, []model.SiteAccountModel{
			{Model: "first", RouteType: "openai_chat", Source: "models_endpoint"},
			{Model: "first", RouteType: "openai_chat", Source: "models_endpoint"},
		})
		if err == nil {
			t.Fatal("duplicate restore keys should fail")
		}
		restoreTestAssertOriginalModelOnly(t, ctx, store, account.ID)
	})

	t.Run("injected insert failure", func(t *testing.T) {
		store := newTestStore(t, "site-restore-write-failure.db")
		ss := store.(*sqlstore.SQLStore)
		ctx := context.Background()
		site := restoreTestCreateSite(t, ctx, store, "restore-write-failure")
		account := restoreTestCreateAccount(t, ctx, store, site.ID, "write-failure")
		restoreTestSeedOriginalModel(t, ctx, store, account.ID)
		if _, err := ss.ExecContext(ctx, `CREATE TRIGGER reject_restore_model
			BEFORE INSERT ON site_account_models
			WHEN NEW.model = 'reject'
			BEGIN
				SELECT RAISE(ABORT, 'injected restore write failure');
			END`); err != nil {
			t.Fatalf("install injected write failure: %v", err)
		}

		err := store.RestoreSiteAccountModels(ctx, account.ID, []model.SiteAccountModel{
			{Model: "first", RouteType: "openai_chat", Source: "models_endpoint"},
			{Model: "reject", RouteType: "openai_chat", Source: "models_endpoint"},
		})
		if err == nil {
			t.Fatal("injected write failure should fail restore")
		}
		restoreTestAssertOriginalModelOnly(t, ctx, store, account.ID)
	})
}

func TestReplaceSiteAccountModelsPreservesManualDisabledAndClearsStale(t *testing.T) {
	store := newTestStore(t, "site-replace-manual-disabled.db")
	ss := store.(*sqlstore.SQLStore)
	ctx := context.Background()
	site := restoreTestCreateSite(t, ctx, store, "replace-manual-disabled")
	account := restoreTestCreateAccount(t, ctx, store, site.ID, "manual-disabled")

	if err := store.ReplaceSiteAccountModels(ctx, account.ID, []model.SiteAccountModel{
		{Model: "manual", RouteType: "openai_chat", Source: "models_endpoint"},
		{Model: "removed", RouteType: "openai_chat", Source: "models_endpoint"},
	}); err != nil {
		t.Fatalf("seed daily models: %v", err)
	}
	if _, err := ss.ExecContext(ctx, "UPDATE site_account_models SET disabled=?, stale=? WHERE site_account_id=? AND model=?",
		true, true, account.ID, "manual"); err != nil {
		t.Fatalf("manually disable model: %v", err)
	}

	if err := store.ReplaceSiteAccountModels(ctx, account.ID, []model.SiteAccountModel{{
		Model: "manual", RouteType: "openai_chat", Source: "models_endpoint",
	}}); err != nil {
		t.Fatalf("daily replace: %v", err)
	}
	models := restoreTestListModels(t, ctx, store, account.ID)
	if len(models) != 1 || models[0].Model != "manual" {
		t.Fatalf("daily replace should prune stale model: %+v", models)
	}
	if !models[0].Disabled {
		t.Fatalf("daily replace must preserve manual disabled state: %+v", models[0])
	}
	if models[0].Stale {
		t.Fatalf("daily replace must refresh retained stale bit: %+v", models[0])
	}
}

func restoreTestCreateSite(t *testing.T, ctx context.Context, store storage.Store, name string) *model.Site {
	t.Helper()
	site, err := store.CreateSite(ctx, &model.Site{
		Name:     name,
		Platform: model.SitePlatformNewAPIFamily,
		BaseURL:  "https://" + name + ".example.com",
		Enabled:  true,
		Timezone: "Asia/Shanghai",
		TagsJSON: "[]",
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	return site
}

func restoreTestCreateAccount(t *testing.T, ctx context.Context, store storage.Store, siteID int64, label string) *model.SiteAccount {
	t.Helper()
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{
		SiteID:          siteID,
		Label:           label,
		CredentialType:  model.CredentialTypeAccessToken,
		Enabled:         true,
		Status:          model.SiteAccountStatusHealthy,
		BalanceCurrency: "USD",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return account
}

func restoreTestListModels(t *testing.T, ctx context.Context, store storage.Store, accountID int64) []model.SiteAccountModel {
	t.Helper()
	models, err := store.ListSiteAccountModels(ctx, model.SiteModelFilter{
		SiteAccountID:   accountID,
		IncludeDisabled: true,
	})
	if err != nil {
		t.Fatalf("list account models: %v", err)
	}
	return models
}

func restoreTestFindModel(t *testing.T, models []model.SiteAccountModel, name string) model.SiteAccountModel {
	t.Helper()
	for _, item := range models {
		if item.Model == name {
			return item
		}
	}
	t.Fatalf("model %q not found in %+v", name, models)
	return model.SiteAccountModel{}
}

func restoreTestSQLitePath(t *testing.T, ctx context.Context, store *sqlstore.SQLStore) string {
	t.Helper()
	rows, err := store.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		t.Fatalf("read sqlite database path: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var sequence int
		var name, path string
		if err := rows.Scan(&sequence, &name, &path); err != nil {
			t.Fatalf("scan sqlite database path: %v", err)
		}
		if name == "main" && path != "" {
			return path
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite database path: %v", err)
	}
	t.Fatal("sqlite main database has no file path")
	return ""
}

func restoreTestSeedOriginalModel(t *testing.T, ctx context.Context, store storage.Store, accountID int64) {
	t.Helper()
	if err := store.ReplaceSiteAccountModels(ctx, accountID, []model.SiteAccountModel{{
		Model: "original", RouteType: "target_route", Source: "target_source",
	}}); err != nil {
		t.Fatalf("seed original model: %v", err)
	}
}

func restoreTestAssertOriginalModelOnly(t *testing.T, ctx context.Context, store storage.Store, accountID int64) {
	t.Helper()
	models := restoreTestListModels(t, ctx, store, accountID)
	if len(models) != 1 || models[0].Model != "original" ||
		models[0].RouteType != "target_route" || models[0].Source != "target_source" {
		t.Fatalf("failed restore must leave original list intact: %+v", models)
	}
}
