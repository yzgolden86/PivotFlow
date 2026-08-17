package storage

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestSiteControlSQLiteCRUDAndProjection(t *testing.T) {
	store, err := CreateSQLiteStore(t.TempDir() + "/site.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{Name: "demo", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://example.com", Enabled: true, Timezone: "Asia/Shanghai", UseSystemProxy: true, TagsJSON: "[]", LastProbeStatus: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if !site.UseSystemProxy {
		t.Fatal("site system proxy preference was not persisted")
	}
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "main", CredentialType: model.CredentialTypeAPIKey, CredentialCiphertext: "fc1.test", CredentialKeyVersion: "v1", Enabled: true, AutoCheckin: true, AutoRefresh: true, Status: "unknown", BalanceCurrency: "CNY", LastRefreshStatus: "unknown", LastCheckinStatus: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSiteAccountModels(ctx, account.ID, []model.SiteAccountModel{{Model: "gpt-4.1", RouteType: "openai_chat", Source: "models_endpoint"}}); err != nil {
		t.Fatal(err)
	}
	firstInput := model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: "default", Name: "site/demo", BaseURL: site.BaseURL, Protocols: []string{"openai"}, Models: []string{"gpt-4.1"}, APIKey: "sk-demo", Enabled: true}
	result, err := store.UpsertSiteProjection(ctx, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	if result.Channel == nil || result.Channel.ID == 0 || result.Action != "created" {
		t.Fatalf("result=%+v", result)
	}
	unchanged, err := store.UpsertSiteProjection(ctx, firstInput)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Channel.ID != result.Channel.ID || unchanged.Action != "unchanged" {
		t.Fatalf("unchanged=%+v", unchanged)
	}

	secondInput := model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: "default", Name: "ignored-on-update", BaseURL: site.BaseURL, Protocols: []string{"openai"}, Models: []string{"gpt-4.1", "gpt-4.2"}, APIKey: "sk-demo-2", Enabled: true}
	result2, err := store.UpsertSiteProjection(ctx, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if result2.Channel.ID != result.Channel.ID || result2.Action != "updated" {
		t.Fatalf("second=%+v", result2)
	}
	if result2.Channel.Name != "site/demo" {
		t.Fatalf("manual channel name was overwritten: %q", result2.Channel.Name)
	}

	execStore, ok := store.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	})
	if !ok {
		t.Fatal("SQLite store does not expose ExecContext")
	}
	if _, err := execStore.ExecContext(ctx, "UPDATE api_keys SET api_key=? WHERE channel_id=? AND key_index=0", "sk-manual", result.Channel.ID); err != nil {
		t.Fatal(err)
	}
	conflict, err := store.UpsertSiteProjection(ctx, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if conflict.Action != "conflict" || conflict.Binding.LastSyncStatus != "conflict" {
		t.Fatalf("conflict=%+v", conflict)
	}
	manualKey, err := store.GetAPIKey(ctx, result.Channel.ID, 0)
	if err != nil || manualKey.APIKey != "sk-manual" {
		t.Fatalf("manual key was replaced during conflict: key=%+v err=%v", manualKey, err)
	}

	secondInput.Force = true
	forced, err := store.UpsertSiteProjection(ctx, secondInput)
	if err != nil {
		t.Fatal(err)
	}
	if forced.Action != "updated" {
		t.Fatalf("forced=%+v", forced)
	}
	restoredKey, err := store.GetAPIKey(ctx, result.Channel.ID, 0)
	if err != nil || restoredKey.APIKey != "sk-demo-2" {
		t.Fatalf("forced key=%+v err=%v", restoredKey, err)
	}

	if err := store.DeleteSiteAccount(ctx, account.ID); err != nil {
		t.Fatal(err)
	}
	disabledChannel, err := store.GetConfig(ctx, result.Channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if disabledChannel.Enabled {
		t.Fatal("projected channel remained enabled after account deletion")
	}
}

func TestSiteProjectionUsesStableNamesForDuplicateUpstreamKeys(t *testing.T) {
	store, err := CreateSQLiteStore(t.TempDir() + "/site-duplicate-projection.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{Name: "duplicate", Platform: model.SitePlatformSub2API, BaseURL: "https://example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "main", CredentialType: model.CredentialTypeAccessToken, CredentialCiphertext: "fc1.test", CredentialKeyVersion: "v1", Enabled: true, Status: "healthy", BalanceCurrency: "USD", LastRefreshStatus: "unknown", LastCheckinStatus: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	base := model.SiteProjectionInput{SiteAccountID: account.ID, Name: "付费分组", BaseURL: site.BaseURL, Protocols: []string{"openai"}, Models: []string{"gpt-5"}, Enabled: true}
	first := base
	first.ProjectionKey, first.APIKey = "key:8", "sk-eight"
	second := base
	second.ProjectionKey, second.APIKey = "key:9", "sk-nine"
	createdFirst, err := store.UpsertSiteProjection(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	createdSecond, err := store.UpsertSiteProjection(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if createdFirst.Channel.Name != "付费分组" || createdSecond.Channel.Name == createdFirst.Channel.Name {
		t.Fatalf("duplicate projection names were not separated: first=%q second=%q", createdFirst.Channel.Name, createdSecond.Channel.Name)
	}
	if createdSecond.Channel.ID == createdFirst.Channel.ID {
		t.Fatalf("duplicate projection reused channel: first=%d second=%d", createdFirst.Channel.ID, createdSecond.Channel.ID)
	}
	again, err := store.UpsertSiteProjection(ctx, second)
	if err != nil || again.Channel.Name != createdSecond.Channel.Name || again.Action != "unchanged" {
		t.Fatalf("second projection was not idempotent: result=%+v err=%v", again, err)
	}
}

func TestPruneSiteProjectionsDeletesOnlyRemovedProjectedChannels(t *testing.T) {
	store, err := CreateSQLiteStore(t.TempDir() + "/site-prune-projection.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{Name: "prune", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "main", CredentialType: model.CredentialTypeAccessToken, CredentialCiphertext: "fc1.test", CredentialKeyVersion: "v1", Enabled: true, Status: "healthy", BalanceCurrency: "USD", LastRefreshStatus: "unknown", LastCheckinStatus: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	keep, err := store.UpsertSiteProjection(ctx, model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: "key:keep", Name: "keep", BaseURL: site.BaseURL, Protocols: []string{"openai"}, Models: []string{"gpt-5"}, APIKey: "sk-keep", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	remove, err := store.UpsertSiteProjection(ctx, model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: "key:remove", Name: "remove", BaseURL: site.BaseURL, Protocols: []string{"openai"}, Models: []string{"gpt-4"}, APIKey: "sk-remove", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	manual, err := store.CreateConfig(ctx, &model.Config{Name: "manual", URLs: model.ChannelURLs{{URL: site.BaseURL}}, ModelEntries: []model.ModelEntry{{Model: "manual-model"}}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	execStore := store.(interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	})
	now := time.Now().UnixMilli()
	if _, err := execStore.ExecContext(ctx, "INSERT INTO site_channel_bindings(site_account_id,projection_key,channel_id,ownership,status,last_projected_hash,last_sync_status,last_sync_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)", account.ID, "manual", manual.ID, "manual", "active", "", "success", "", now, now); err != nil {
		t.Fatal(err)
	}
	if err := store.PruneSiteProjectionsExcept(ctx, account.ID, []string{"key:keep"}); err != nil {
		t.Fatal(err)
	}
	bindings, err := store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	if _, err := store.GetConfig(ctx, keep.Channel.ID); err != nil {
		t.Fatalf("kept projection was deleted: %v", err)
	}
	if removed, err := store.GetConfig(ctx, remove.Channel.ID); err == nil {
		t.Fatalf("removed projection channel still exists: %+v", removed)
	}
	if _, err := store.GetConfig(ctx, manual.ID); err != nil {
		t.Fatalf("manual channel was deleted: %v", err)
	}
}

func TestDeletedSiteNameCanBeReused(t *testing.T) {
	store, err := CreateSQLiteStore(t.TempDir() + "/site-recreate.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	first, err := store.CreateSite(ctx, &model.Site{Name: "reusable", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://old.example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSite(ctx, &model.Site{Name: "reusable", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://duplicate.example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"}); err == nil || err.Error() != "site_name_exists" {
		t.Fatalf("active duplicate error=%v", err)
	}
	if err := store.DeleteSite(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateSite(ctx, &model.Site{Name: "reusable", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://new.example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == first.ID || second.Name != "reusable" {
		t.Fatalf("recreated site=%+v first=%+v", second, first)
	}
}

func TestUpdateSitePersistsProbeResult(t *testing.T) {
	store, err := CreateSQLiteStore(t.TempDir() + "/site-probe.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{Name: "probe", Platform: model.SitePlatformUnknown, BaseURL: "https://example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]", LastProbeStatus: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	site.Platform = model.SitePlatformNewAPIFamily
	site.LastProbeStatus = "success"
	site.LastError = ""
	updated, err := store.UpdateSite(ctx, site.ID, site)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastProbeStatus != "success" || updated.Platform != model.SitePlatformNewAPIFamily || updated.LastError != "" {
		t.Fatalf("probe result was not persisted: %+v", updated)
	}
	site.LastProbeStatus = "failed"
	site.LastError = "request_failed"
	updated, err = store.UpdateSite(ctx, site.ID, site)
	if err != nil {
		t.Fatal(err)
	}
	if updated.LastProbeStatus != "failed" || updated.LastError != "request_failed" {
		t.Fatalf("probe failure was not persisted: %+v", updated)
	}
}

func TestSiteTaskCancellationIsTerminal(t *testing.T) {
	store, err := CreateSQLiteStore(t.TempDir() + "/tasks.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	task := &model.SiteTask{ID: "st_cancel", Kind: "refresh", Status: model.SiteTaskStatusQueued, ProgressJSON: `{}`, CreatedAt: time.Now().UnixMilli()}
	if err := store.CreateSiteTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	task.Status = model.SiteTaskStatusRunning
	updated, err := store.UpdateSiteTask(ctx, task)
	if err != nil || !updated {
		t.Fatalf("start task = (%v, %v)", updated, err)
	}
	cancelled, err := store.CancelSiteTask(ctx, task.ID, time.Now().UnixMilli())
	if err != nil || !cancelled {
		t.Fatalf("cancel task = (%v, %v)", cancelled, err)
	}
	task.Status = model.SiteTaskStatusSuccess
	task.FinishedAt = time.Now().UnixMilli()
	updated, err = store.UpdateSiteTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("cancelled task accepted a later success transition")
	}
	stored, err := store.GetSiteTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.SiteTaskStatusCancelled {
		t.Fatalf("status=%q, want cancelled", stored.Status)
	}
}

func TestWebhookConfigAndEventStateRoundTrip(t *testing.T) {
	store, err := CreateSQLiteStore(t.TempDir() + "/webhook.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	config := &model.WebhookConfig{
		ID: 1, Enabled: true, URLCiphertext: "fc1.v1.encrypted", URLKeyVersion: "v1",
		LowBalanceEnabled: true, LowBalanceThreshold: 8.5, CheckinFailureEnabled: true,
		CooldownMinutes: 120, LastDeliveryStatus: "never",
	}
	if err := store.UpsertWebhookConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetWebhookConfig(ctx)
	if err != nil || !loaded.Enabled || !loaded.URLConfigured || loaded.LowBalanceThreshold != 8.5 || loaded.CooldownMinutes != 120 {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	state := &model.WebhookEventState{EventKey: "low_balance:1", EventType: "low_balance", SiteAccountID: 1, Status: "delivered", Attempts: 2, LastAttemptAt: 1000, DeliveredAt: 1000}
	if err := store.UpsertWebhookEventState(ctx, state); err != nil {
		t.Fatal(err)
	}
	state.Status, state.Attempts, state.LastAttemptAt = "resolved", 3, 2000
	if err := store.UpsertWebhookEventState(ctx, state); err != nil {
		t.Fatal(err)
	}
	loadedState, err := store.GetWebhookEventState(ctx, state.EventKey)
	if err != nil || loadedState.Status != "resolved" || loadedState.Attempts != 3 || loadedState.LastAttemptAt != 2000 {
		t.Fatalf("state=%+v err=%v", loadedState, err)
	}
}

func TestSiteTaskLeaseExcludesConcurrentOwner(t *testing.T) {
	store, err := CreateSQLiteStore(t.TempDir() + "/leases.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	const now int64 = 1_000
	acquired, err := store.AcquireSiteTaskLease(ctx, "account:1:refresh", "owner-1", now, now+90_000)
	if err != nil || !acquired {
		t.Fatalf("first acquire = (%v, %v)", acquired, err)
	}
	acquired, err = store.AcquireSiteTaskLease(ctx, "account:1:refresh", "owner-2", now+1, now+90_001)
	if err != nil {
		t.Fatal(err)
	}
	if acquired {
		t.Fatal("second owner acquired an active lease")
	}
	renewed, err := store.RenewSiteTaskLease(ctx, "account:1:refresh", "owner-2", now+120_000, now+2)
	if err != nil {
		t.Fatal(err)
	}
	if renewed {
		t.Fatal("non-owner renewed the lease")
	}
	acquired, err = store.AcquireSiteTaskLease(ctx, "account:1:refresh", "owner-2", now+90_001, now+180_001)
	if err != nil || !acquired {
		t.Fatalf("expired lease takeover = (%v, %v)", acquired, err)
	}
}
