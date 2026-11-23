package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

type backupRestoreDatabase struct {
	server *Server
	store  *sqlstore.SQLStore
	cipher *credential.Cipher
	path   string
}

// Starts no production server, background workers, catalog or scheduler. The
// real storage factory still migrates each owned on-disk SQLite database.
func newBackupRestoreDatabase(t *testing.T, path, material, keyVersion string) *backupRestoreDatabase {
	t.Helper()
	store, err := storage.CreateSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := credential.New([]byte(material), keyVersion)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	concrete := store.(*sqlstore.SQLStore)
	if err := concrete.ConfigureCredentialCipher(context.Background(), cipher); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	server := &Server{store: store, siteControl: &siteControlService{store: store, cipher: cipher}}
	server.authService = newTestAuthService(t)
	server.authService.store = store
	fixture := &backupRestoreDatabase{server: server, store: concrete, cipher: cipher, path: path}
	t.Cleanup(func() {
		fixture.server.stopConfigPreviews()
		fixture.server.authService.Close()
		_ = fixture.store.Close()
	})
	return fixture
}

func restoreAccountFixture(t *testing.T, db *backupRestoreDatabase, name, baseURL, platform, label, kind string, creds provider.Credentials) (*model.Site, *model.SiteAccount) {
	t.Helper()
	ctx := context.Background()
	site, err := db.store.CreateSite(ctx, &model.Site{Name: name, BaseURL: baseURL, Platform: platform, Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]", LastProbeStatus: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := db.cipher.Seal(creds)
	if err != nil {
		t.Fatal(err)
	}
	account, err := db.store.CreateSiteAccount(ctx, &model.SiteAccount{
		SiteID: site.ID, Label: label, CredentialType: kind, CredentialCiphertext: sealed,
		CredentialKeyVersion: db.cipher.Version(), Enabled: true, Status: model.SiteAccountStatusHealthy,
		BalanceCurrency: "USD", LastRefreshStatus: "unknown", LastCheckinStatus: "unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	return site, account
}

func restoreChannelFixture(t *testing.T, db *backupRestoreDatabase, name string, oauth bool) *model.Config {
	t.Helper()
	config := &model.Config{Name: name, Enabled: true, CostMultiplier: 1, URLs: model.ChannelURLs{{URL: "https://synthetic-routing.invalid/v1"}}, ModelEntries: []model.ModelEntry{{Model: "demo"}}}
	if oauth {
		config.AuthType = model.AuthTypeCodexOAuth
		config.OAuthCredential = `{"access_token":"synthetic-oauth-access","refresh_token":"synthetic-oauth-refresh"}`
	}
	saved, err := db.store.CreateConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if !oauth {
		if err := db.store.CreateAPIKeysBatch(context.Background(), []*model.APIKey{
			{ChannelID: saved.ID, KeyIndex: 0, APIKey: "synthetic-route-key-zero", CostMultiplier: 1, KeyStrategy: model.KeyStrategySequential},
			{ChannelID: saved.ID, KeyIndex: 1, APIKey: "synthetic-route-key-one", CostMultiplier: 0.5, CostMultiplierSet: true, AllowedModels: []string{"demo"}, KeyStrategy: model.KeyStrategySequential},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return saved
}

func restoreBindingFixture(t *testing.T, db *backupRestoreDatabase, accountID, channelID int64, projectionKey, ownership string) {
	t.Helper()
	_, err := db.store.ExecContext(context.Background(), `INSERT INTO site_channel_bindings(site_account_id,projection_key,channel_id,pricing_group,ownership,status,last_projected_hash,last_sync_status,last_sync_error,created_at,updated_at) VALUES(?,?,?,?,?,'active',?,'success','',100,100)`, accountID, projectionKey, channelID, "synthetic-group", ownership, "runtime-projection-hash-must-not-migrate")
	if err != nil {
		t.Fatal(err)
	}
}

func applyRestoreFixture(t *testing.T, target *backupRestoreDatabase, document *backupDocument) *configPreviewResult {
	t.Helper()
	preview, err := target.server.previewBackupDocument(context.Background(), document)
	if err != nil || preview == nil || !preview.CanApply {
		t.Fatalf("restoration preview rejected the synthetic fixture: err=%v", err)
	}
	result, err := target.server.applyConfigPreview(context.Background(), preview.ID)
	if err != nil || result == nil || result.Status != "applied" {
		t.Fatalf("restore did not commit its fixed preview: err=%v", err)
	}
	again, err := target.server.applyConfigPreview(context.Background(), preview.ID)
	if err != nil || again.Status != "applied" {
		t.Fatal("reapplication did not retain its terminal result")
	}
	return result
}

func TestBackupRestoreIndependentDatabasesReencryptAndRemapAfterReopen(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	source := newBackupRestoreDatabase(t, filepath.Join(directory, "source.db"), "0123456789abcdef0123456789abcdef", "restore-source")
	targetPath := filepath.Join(directory, "target.db")
	target := newBackupRestoreDatabase(t, targetPath, "fedcba9876543210fedcba9876543210", "restore-target")
	_, account := restoreAccountFixture(t, source, "Restore source", "https://synthetic-source.invalid", model.SitePlatformOpenAICompatible, "primary", model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-management-key"})
	_, cookieAccount := restoreAccountFixture(t, source, "Restore cookies", "https://synthetic-cookie.invalid", model.SitePlatformNewAPIFamily, "cookie", model.CredentialTypeCookie, provider.Credentials{Cookie: "session=synthetic-session-cookie", UserID: 4242})
	if _, err := source.store.ExecContext(ctx, "UPDATE site_accounts SET balance=42.5,last_checkin_at=1700000000000 WHERE id=?", account.ID); err != nil {
		t.Fatal(err)
	}
	apiChannel := restoreChannelFixture(t, source, "Restore API", false)
	oauthChannel := restoreChannelFixture(t, source, "Restore OAuth", true)
	restoreBindingFixture(t, source, account.ID, apiChannel.ID, "synthetic-routing-default", "projected")
	if err := source.store.RestoreSiteAccountModels(ctx, account.ID, []model.SiteAccountModel{{Model: "demo", RouteType: "chat", Source: "synthetic-discovery"}}); err != nil {
		t.Fatal(err)
	}
	if err := source.store.RestoreSiteAccountModels(ctx, cookieAccount.ID, []model.SiteAccountModel{{Model: "cookie-demo", RouteType: "chat", Source: "synthetic-discovery", Disabled: true, Stale: true}}); err != nil {
		t.Fatal(err)
	}
	future, past := time.Now().Add(24*time.Hour).UnixMilli(), time.Now().Add(-time.Hour).UnixMilli()
	for _, fixture := range []struct {
		plain, description, mode string
		channels                 []int64
		active                   bool
		expires                  *int64
	}{
		{"synthetic-restored-allow", "Restore allow", model.ChannelRestrictionModeAllow, []int64{apiChannel.ID}, true, &future},
		{"synthetic-restored-deny", "Restore deny", model.ChannelRestrictionModeDeny, []int64{oauthChannel.ID}, true, &future},
		{"synthetic-restored-inactive", "Restore inactive", model.ChannelRestrictionModeAllow, nil, false, nil},
		{"synthetic-restored-expired", "Restore expired", model.ChannelRestrictionModeAllow, nil, true, &past},
	} {
		sealed, err := source.cipher.Seal(fixture.plain)
		if err != nil {
			t.Fatal(err)
		}
		token := &model.AuthToken{Token: model.HashToken(fixture.plain), TokenCiphertext: sealed, Description: fixture.description, IsActive: fixture.active, ExpiresAt: fixture.expires, AllowedModels: []string{"demo"}, AllowedProtocols: []string{"openai"}, AllowedChannelIDs: fixture.channels, ChannelRestrictionMode: fixture.mode, MaxConcurrency: 2, CostLimitMicroUSD: 12_500_000, CostUsedMicroUSD: 3_250_000, SuccessCount: 7}
		if err := source.store.CreateAuthToken(ctx, token); err != nil {
			t.Fatal(err)
		}
		if _, err := source.store.ExecContext(ctx, "UPDATE auth_tokens SET cost_used_microusd=3250000,success_count=7 WHERE id=?", token.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := source.store.CreateAuthToken(ctx, &model.AuthToken{Token: model.HashToken("synthetic-unrecoverable-legacy"), Description: "Legacy unrecoverable", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if err := source.server.importNotification(ctx, &backupNotification{WebhookEnabled: true, WebhookURL: "https://synthetic-notification.invalid/hook", TelegramEnabled: true, TelegramBotToken: "synthetic-bot", TelegramChatID: "synthetic-chat", LowBalanceThreshold: 10, CooldownMinutes: 60}); err != nil {
		t.Fatal(err)
	}
	if err := source.server.importBackupTarget(ctx, &backupTarget{Enabled: true, FileURL: "https://synthetic-dav.invalid/backup.json", Username: "synthetic-dav-user", Password: "synthetic-portable-dav", ExportType: "all", AutoSyncIntervalHours: 24}); err != nil {
		t.Fatal(err)
	}

	// These IDs collide with archive-local IDs but identities do not.
	_, unrelatedAccount := restoreAccountFixture(t, target, "Unrelated target", "https://synthetic-unrelated.invalid", model.SitePlatformOpenAICompatible, "unrelated", model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-unrelated-management"})
	unrelatedChannel := restoreChannelFixture(t, target, "Unrelated channel", false)
	restoreBindingFixture(t, target, unrelatedAccount.ID, unrelatedChannel.ID, "unrelated-projection", "manual")
	if err := target.store.CreateAuthToken(ctx, &model.AuthToken{Token: model.HashToken("synthetic-unrelated-client"), Description: "Unrelated token", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	document, err := source.server.buildBackupDocument(ctx, "all")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var shape map[string]any
	_ = json.Unmarshal(encoded, &shape)
	connections := shape["connections"].(map[string]any)
	bindings, ok := connections["bindings"].([]any)
	if !ok || len(bindings) != 1 {
		t.Fatal("portable source backup omitted its restorable channel binding")
	}
	if strings.Contains(string(encoded), "runtime-projection-hash-must-not-migrate") || strings.Contains(string(encoded), "last_verified_at") || len(document.Warnings) != 1 {
		t.Fatal("portable backup includes runtime evidence or omits its unrecoverable-token warning")
	}
	applyRestoreFixture(t, target, document)
	target.server.stopConfigPreviews()
	target.server.authService.Close()
	if err := target.store.Close(); err != nil {
		t.Fatal(err)
	}
	target = newBackupRestoreDatabase(t, targetPath, "fedcba9876543210fedcba9876543210", "restore-target")
	sites, err := target.store.ListSites(ctx, model.SiteListFilter{})
	if err != nil || len(sites) != 3 {
		t.Fatal("source IDs overwrote unrelated site identities")
	}
	accounts, err := target.store.ListSiteAccounts(ctx, 0, false)
	if err != nil || len(accounts) != 3 {
		t.Fatal("restored account references or unrelated account were lost")
	}
	var restoredAccount *model.SiteAccount
	for _, restored := range accounts {
		creds, err := target.server.siteControl.credentials(restored)
		if err != nil {
			t.Fatal("reopened target cannot decrypt an account credential")
		}
		switch restored.Label {
		case "primary":
			restoredAccount = restored
			if restored.ID == account.ID || creds.APIKey != "synthetic-management-key" || restored.CredentialKeyVersion != "restore-target" || restored.Balance != nil || restored.LastCheckinAt != 0 {
				t.Fatal("account identity, credential, or config-only recovery boundary is wrong")
			}
			var wrong provider.Credentials
			if source.cipher.Open(restored.CredentialCiphertext, &wrong) == nil {
				t.Fatal("restored account is still encrypted with the source key")
			}
		case "cookie":
			if creds.Cookie != "session=synthetic-session-cookie" || creds.UserID != 4242 || restored.ID == cookieAccount.ID {
				t.Fatal("cookie and user identity did not survive cross-instance restoration")
			}
		case "unrelated":
			if creds.APIKey != "synthetic-unrelated-management" {
				t.Fatal("unrelated target credential was overwritten")
			}
		}
	}
	channels, err := target.store.ListConfigs(ctx)
	if err != nil || len(channels) != 3 {
		t.Fatal("channel ID collisions created or overwrote unexpected rows")
	}
	var restoredAPI, restoredOAuth *model.Config
	for _, channel := range channels {
		switch channel.Name {
		case "Restore API":
			restoredAPI = channel
		case "Restore OAuth":
			restoredOAuth = channel
		}
	}
	if restoredAPI == nil || restoredOAuth == nil || restoredAccount == nil || restoredAPI.ID == apiChannel.ID || restoredOAuth.ID == oauthChannel.ID || restoredOAuth.OAuthCredential != oauthChannel.OAuthCredential {
		t.Fatal("channel references or decrypted OAuth credential were not mapped")
	}
	keys, err := target.store.GetAPIKeys(ctx, restoredAPI.ID)
	if err != nil || len(keys) != 2 || keys[0].APIKey != "synthetic-route-key-zero" || keys[1].APIKey != "synthetic-route-key-one" || keys[1].CostMultiplier != 0.5 {
		t.Fatal("target channel keys did not survive encryption and reopening")
	}
	for _, query := range []string{"SELECT api_key FROM api_keys WHERE channel_id=? AND key_index=0", "SELECT oauth_credential FROM channels WHERE id=?"} {
		id := restoredAPI.ID
		if strings.Contains(query, "oauth_credential") {
			id = restoredOAuth.ID
		}
		var sealed, plain string
		if err := target.store.QueryRowContext(ctx, query, id).Scan(&sealed); err != nil || !credential.IsSealed(sealed) || target.cipher.Open(sealed, &plain) != nil || source.cipher.Open(sealed, &plain) == nil {
			t.Fatal("raw target channel credential is not protected by the target key")
		}
	}
	binding, err := target.store.GetSiteChannelBinding(ctx, restoredAccount.ID, "synthetic-routing-default")
	if err != nil || binding.ChannelID != restoredAPI.ID || binding.Ownership != "manual" || binding.Status != "active" || binding.PricingGroup != "synthetic-group" || binding.LastProjectedHash != "" {
		t.Fatal("channel binding was lost, mis-mapped, or claimed automatic synchronization")
	}
	if _, err := target.store.GetSiteChannelBinding(ctx, unrelatedAccount.ID, "unrelated-projection"); err != nil {
		t.Fatal("unlisted target binding was removed")
	}
	if err := target.server.authService.ReloadAuthTokens(); err != nil {
		t.Fatal(err)
	}
	tokens, err := target.store.ListAuthTokens(ctx)
	if err != nil || len(tokens) != 5 {
		t.Fatal("legacy unrecoverable token was guessed or an unrelated token was overwritten")
	}
	for _, token := range tokens {
		if !strings.HasPrefix(token.Description, "Restore ") {
			continue
		}
		var plain string
		if target.cipher.Open(token.TokenCiphertext, &plain) != nil || model.HashToken(plain) != token.Token || source.cipher.Open(token.TokenCiphertext, &plain) == nil || token.CostLimitMicroUSD != 12_500_000 || token.CostUsedMicroUSD != 0 || token.SuccessCount != 0 || token.MaxConcurrency != 2 || !slices.Equal(token.AllowedModels, []string{"demo"}) || !slices.Equal(token.AllowedProtocols, []string{"openai"}) {
			t.Fatal("token credentials, limits, or config-only accounting boundary changed")
		}
		if token.Description == "Restore allow" {
			if !slices.Equal(token.AllowedChannelIDs, []int64{restoredAPI.ID}) || token.ExpiresAt == nil || *token.ExpiresAt != future || !target.server.authService.IsChannelAllowed(token.Token, restoredAPI.ID) || target.server.authService.IsChannelAllowed(token.Token, unrelatedChannel.ID) || !target.server.authService.IsModelAllowed(token.Token, "demo") || target.server.authService.IsModelAllowed(token.Token, "other") || !target.server.authService.IsProtocolAllowed(token.Token, "openai") || target.server.authService.IsProtocolAllowed(token.Token, "anthropic") {
				t.Fatal("restored allow/model/protocol restrictions did not govern authorization")
			}
			release1, _, limit, ok1 := target.server.authService.acquireTokenConcurrencySlot(token.Token)
			release2, _, _, ok2 := target.server.authService.acquireTokenConcurrencySlot(token.Token)
			_, _, _, ok3 := target.server.authService.acquireTokenConcurrencySlot(token.Token)
			release1()
			release2()
			if !ok1 || !ok2 || ok3 || limit != 2 {
				t.Fatal("restored concurrency limit is not enforced")
			}
			target.server.authService.AddCostToCache(token.Token, 12_500_000)
			_, limitMicro, exceeded := target.server.authService.IsCostLimitExceeded(token.Token)
			if !exceeded || limitMicro != 12_500_000 {
				t.Fatal("restored cost limit is not enforced")
			}
		}
		if token.Description == "Restore deny" && (token.ChannelRestrictionMode != model.ChannelRestrictionModeDeny || !slices.Equal(token.AllowedChannelIDs, []int64{restoredOAuth.ID}) || target.server.authService.IsChannelAllowed(token.Token, restoredOAuth.ID) || !target.server.authService.IsChannelAllowed(token.Token, unrelatedChannel.ID)) {
			t.Fatal("deny list was widened or attached to the wrong target channel")
		}
		if token.Description == "Restore inactive" && target.server.authService.IsTokenActive(token.Token) || token.Description == "Restore expired" && target.server.authService.IsTokenActive(token.Token) {
			t.Fatal("inactive or expired token became usable")
		}
	}
	notification, err := target.server.exportNotification(ctx)
	if err != nil || notification.WebhookURL != "https://synthetic-notification.invalid/hook" || notification.TelegramBotToken != "synthetic-bot" || notification.TelegramChatID != "synthetic-chat" {
		t.Fatal("notification credentials could not be recovered under the target key")
	}
	backup, err := target.server.exportBackupTarget(ctx)
	if err != nil || backup.Password != "synthetic-portable-dav" {
		t.Fatal("WebDAV credential could not be recovered under the target key")
	}
}

func TestBackupRestoreExportReadsPrimaryInsteadOfStaleReplica(t *testing.T) {
	directory := t.TempDir()
	primary := newBackupRestoreDatabase(t, filepath.Join(directory, "primary.db"), "0123456789abcdef0123456789abcdef", "restore-primary")
	cache := newBackupRestoreDatabase(t, filepath.Join(directory, "cache.db"), "0123456789abcdef0123456789abcdef", "restore-primary")
	restoreChannelFixture(t, primary, "Authoritative channel", false)
	restoreChannelFixture(t, cache, "Stale replica channel", false)
	hybrid := storage.NewHybridStore(cache.store, primary.store)
	source := &Server{store: hybrid, siteControl: &siteControlService{store: hybrid, cipher: primary.cipher}}
	document, err := source.buildBackupDocument(context.Background(), "connections")
	if err != nil || len(document.Connections.Channels) != 1 || document.Connections.Channels[0].Config.Name != "Authoritative channel" || len(document.Connections.Channels[0].APIKeys) != 2 {
		t.Fatal("backup snapshot read stale replica channel/credential configuration")
	}
}

func TestBackupRestoreModelListsIncludeEmptyAndPreserveControlFlags(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled and stale", true: "empty list"}[empty], func(t *testing.T) {
			ctx := context.Background()
			directory := t.TempDir()
			source := newBackupRestoreDatabase(t, filepath.Join(directory, "source.db"), "0123456789abcdef0123456789abcdef", "source")
			target := newBackupRestoreDatabase(t, filepath.Join(directory, "target.db"), "fedcba9876543210fedcba9876543210", "target")
			_, sourceAccount := restoreAccountFixture(t, source, "Model site", "https://synthetic-models.invalid", model.SitePlatformOpenAICompatible, "models", model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-model-management"})
			_, targetAccount := restoreAccountFixture(t, target, "Model site", "https://synthetic-models.invalid", model.SitePlatformOpenAICompatible, "models", model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-model-management"})
			if !empty {
				if err := source.store.RestoreSiteAccountModels(ctx, sourceAccount.ID, []model.SiteAccountModel{{Model: "demo", RouteType: "chat", Source: "source", Disabled: true, Stale: true}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := target.store.RestoreSiteAccountModels(ctx, targetAccount.ID, []model.SiteAccountModel{{Model: "demo", RouteType: "chat", Source: "target"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := target.store.ExecContext(ctx, "UPDATE site_account_models SET last_seen_at=12345,created_at=2222 WHERE site_account_id=?", targetAccount.ID); err != nil {
				t.Fatal(err)
			}
			document, err := source.server.buildBackupDocument(ctx, "connections")
			if err != nil {
				t.Fatal(err)
			}
			applyRestoreFixture(t, target, document)
			models, err := target.store.ListSiteAccountModels(ctx, model.SiteModelFilter{SiteAccountID: targetAccount.ID, IncludeDisabled: true})
			if err != nil || empty && len(models) != 0 || !empty && (len(models) != 1 || !models[0].Disabled || !models[0].Stale || models[0].LastSeenAt != 12345 || models[0].CreatedAt != 2222) {
				t.Fatal("model restore did not converge to the exported controls/list while keeping target observations")
			}
			preview, err := target.server.previewBackupDocument(ctx, document)
			if err != nil || preview.Summary["update"] != 0 || preview.Summary["create"] != 0 {
				t.Fatal("second preview still reports model restore differences")
			}
		})
	}
}

func TestBackupRestoreRejectsDuplicateModelRegardlessOfRouteType(t *testing.T) {
	target := newBackupRestoreDatabase(t, filepath.Join(t.TempDir(), "target.db"), "fedcba9876543210fedcba9876543210", "target")
	document := previewDocument()
	document.Connections.Models = []model.SiteAccountModel{
		{SiteAccountID: 99, Model: "same-model", RouteType: "chat"},
		{SiteAccountID: 99, Model: "same-model", RouteType: "embedding"},
	}
	preview, err := target.server.previewBackupDocument(context.Background(), document)
	if err != nil || preview.CanApply || preview.Summary["conflict"] == 0 {
		t.Fatal("same model with different route types bypassed the database identity conflict")
	}
	if _, err := target.server.applyConfigPreview(context.Background(), preview.ID); err == nil {
		t.Fatal("invalid model backup was applied")
	}
	sites, _ := target.store.ListSites(context.Background(), model.SiteListFilter{})
	if len(sites) != 0 {
		t.Fatal("conflicted model restore wrote partial site configuration")
	}
}

func TestBackupRestoreReusesLegacyDeletedSiteNameAtomically(t *testing.T) {
	ctx := context.Background()
	target := newBackupRestoreDatabase(t, filepath.Join(t.TempDir(), "target.db"), "fedcba9876543210fedcba9876543210", "target")
	active, err := target.store.CreateSite(ctx, &model.Site{Name: "Old active name", BaseURL: "https://synthetic-legacy-active.invalid", Platform: model.SitePlatformOpenAICompatible, Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := target.store.CreateSite(ctx, &model.Site{Name: "Reusable source name", BaseURL: "https://synthetic-legacy-deleted.invalid", Platform: model.SitePlatformOpenAICompatible, Enabled: false, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.store.ExecContext(ctx, "UPDATE sites SET deleted_at=9000 WHERE id=?", deleted.ID); err != nil {
		t.Fatal(err)
	}
	document := &backupDocument{Version: "1.0", Type: "connections", Connections: &backupConnections{Sites: []*model.Site{{ID: 101, Name: "Reusable source name", BaseURL: active.BaseURL, Platform: model.SitePlatformOpenAICompatible, Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"}}}}
	applyRestoreFixture(t, target, document)
	recovered, err := target.store.GetSite(ctx, active.ID)
	if err != nil || recovered.Name != "Reusable source name" {
		t.Fatal("restored site could not reuse a historical deleted name")
	}
	var reservedName string
	if err := target.store.QueryRowContext(ctx, "SELECT name FROM sites WHERE id=? AND deleted_at=9000", deleted.ID).Scan(&reservedName); err != nil || reservedName == "Reusable source name" {
		t.Fatal("legacy name release deleted history or retained the conflicting name")
	}
}

func TestBackupRestoreInvalidBindingReferencesConflictWithoutWrites(t *testing.T) {
	for _, fixture := range []struct {
		name     string
		bindings []map[string]any
	}{
		{"unknown account", []map[string]any{{"site_account_id": 404, "channel_id": 73, "projection_key": "synthetic"}}},
		{"unknown channel", []map[string]any{{"site_account_id": 99, "channel_id": 404, "projection_key": "synthetic"}}},
		{"duplicate identity", []map[string]any{{"site_account_id": 99, "channel_id": 73, "projection_key": "same"}, {"site_account_id": 99, "channel_id": 73, "projection_key": "same"}}},
		{"ambiguous channel", []map[string]any{{"site_account_id": 99, "channel_id": 73, "projection_key": "first"}, {"site_account_id": 99, "channel_id": 73, "projection_key": "second"}}},
		{"blank key", []map[string]any{{"site_account_id": 99, "channel_id": 73, "projection_key": " "}}},
		{"long key", []map[string]any{{"site_account_id": 99, "channel_id": 73, "projection_key": strings.Repeat("x", 192)}}},
		{"invalid group", []map[string]any{{"site_account_id": 99, "channel_id": 73, "projection_key": "synthetic", "pricing_group": "group\nother"}}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			target := newBackupRestoreDatabase(t, filepath.Join(t.TempDir(), "target.db"), "fedcba9876543210fedcba9876543210", "target")
			encoded, _ := json.Marshal(previewDocument())
			var shape map[string]any
			_ = json.Unmarshal(encoded, &shape)
			shape["connections"].(map[string]any)["bindings"] = fixture.bindings
			encoded, _ = json.Marshal(shape)
			var document backupDocument
			if err := json.Unmarshal(encoded, &document); err != nil {
				t.Fatal(err)
			}
			preview, err := target.server.previewBackupDocument(context.Background(), &document)
			if err != nil || preview.CanApply || preview.Summary["conflict"] == 0 {
				t.Fatal("invalid or ambiguous binding was silently ignored")
			}
			if _, err := target.server.applyConfigPreview(context.Background(), preview.ID); err == nil {
				t.Fatal("binding conflict did not stop the complete configuration transaction")
			}
			sites, _ := target.store.ListSites(context.Background(), model.SiteListFilter{})
			if len(sites) != 0 {
				t.Fatal("invalid binding preview wrote partial configuration")
			}
		})
	}
}

func TestBackupRestoreLegacyDocumentKeepsDisabledBindingDisabled(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	source := newBackupRestoreDatabase(t, filepath.Join(directory, "source.db"), "0123456789abcdef0123456789abcdef", "source")
	target := newBackupRestoreDatabase(t, filepath.Join(directory, "target.db"), "fedcba9876543210fedcba9876543210", "target")
	_, sourceAccount := restoreAccountFixture(t, source, "Legacy binding", "https://synthetic-legacy-binding.invalid", model.SitePlatformOpenAICompatible, "bound", model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-bound-management"})
	_, targetAccount := restoreAccountFixture(t, target, "Legacy binding", "https://synthetic-legacy-binding.invalid", model.SitePlatformOpenAICompatible, "bound", model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-bound-management"})
	sourceChannel := restoreChannelFixture(t, source, "Legacy bound channel", false)
	targetChannel := restoreChannelFixture(t, target, "Legacy bound channel", false)
	restoreBindingFixture(t, source, sourceAccount.ID, sourceChannel.ID, "synthetic-legacy-projection", "projected")
	restoreBindingFixture(t, target, targetAccount.ID, targetChannel.ID, "synthetic-legacy-projection", "projected")
	sourceAccount.Enabled, sourceAccount.Status = false, model.SiteAccountStatusDisabled
	if _, err := source.store.UpdateSiteAccount(ctx, sourceAccount.ID, sourceAccount); err != nil {
		t.Fatal(err)
	}
	if _, err := source.store.UpdateChannelEnabled(ctx, sourceChannel.ID, false); err != nil {
		t.Fatal(err)
	}
	document, err := source.server.buildBackupDocument(ctx, "connections")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(document)
	var shape map[string]any
	_ = json.Unmarshal(encoded, &shape)
	delete(shape["connections"].(map[string]any), "bindings")
	encoded, _ = json.Marshal(shape)
	var legacy backupDocument
	_ = json.Unmarshal(encoded, &legacy)
	applyRestoreFixture(t, target, &legacy)
	binding, err := target.store.GetSiteChannelBinding(ctx, targetAccount.ID, "synthetic-legacy-projection")
	if err != nil || binding.Ownership != "manual" || binding.Status != "disabled" {
		t.Fatal("channel import reactivated a binding after its account was disabled")
	}
}

func TestBackupRestoreBindingUsesTargetAvailability(t *testing.T) {
	for _, fixture := range []struct {
		name, change, want string
	}{
		{"disabled account", "account", "disabled"},
		{"disabled site", "site", "disabled"},
		{"disabled channel", "channel", "disabled"},
		{"source expiry is an observation", "expiry", "active"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx := context.Background()
			directory := t.TempDir()
			source := newBackupRestoreDatabase(t, filepath.Join(directory, "source.db"), "0123456789abcdef0123456789abcdef", "source")
			target := newBackupRestoreDatabase(t, filepath.Join(directory, "target.db"), "fedcba9876543210fedcba9876543210", "target")
			site, account := restoreAccountFixture(t, source, "Binding availability", "https://synthetic-availability.invalid", model.SitePlatformOpenAICompatible, "bound", model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-binding-management"})
			channel := restoreChannelFixture(t, source, "Binding availability channel", false)
			restoreBindingFixture(t, source, account.ID, channel.ID, " opaque projection ", "projected")
			var err error
			switch fixture.change {
			case "account":
				_, err = source.store.ExecContext(ctx, "UPDATE site_accounts SET enabled=0 WHERE id=?", account.ID)
			case "site":
				_, err = source.store.ExecContext(ctx, "UPDATE sites SET enabled=0 WHERE id=?", site.ID)
			case "channel":
				_, err = source.store.UpdateChannelEnabled(ctx, channel.ID, false)
			case "expiry":
				_, err = source.store.ExecContext(ctx, "UPDATE site_accounts SET status='expired' WHERE id=?", account.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			document, err := source.server.buildBackupDocument(ctx, "connections")
			if err != nil {
				t.Fatal(err)
			}
			applyRestoreFixture(t, target, document)
			bindings, err := target.store.ListSiteChannelBindings(ctx)
			if err != nil || len(bindings) != 1 || bindings[0].Status != fixture.want || bindings[0].ProjectionKey != " opaque projection " || bindings[0].Ownership != "manual" {
				t.Fatal("binding restore lost its opaque identity or derived availability from source runtime history")
			}
			preview, err := target.server.previewBackupDocument(ctx, document)
			if err != nil || preview.Summary["update"] != 0 || preview.Summary["create"] != 0 {
				t.Fatal("repeated binding restore does not converge")
			}
		})
	}
}

func TestBackupRestoreBindingConflictPreservesTarget(t *testing.T) {
	ctx := context.Background()
	target := newBackupRestoreDatabase(t, filepath.Join(t.TempDir(), "target.db"), "fedcba9876543210fedcba9876543210", "target")
	_, account := restoreAccountFixture(t, target, "Preview Site", "https://preview.example/v1", model.SitePlatformOpenAICompatible, "other", model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-other-management"})
	channel := restoreChannelFixture(t, target, "Preview Channel", false)
	restoreBindingFixture(t, target, account.ID, channel.ID, "occupied", "manual")
	encoded, _ := json.Marshal(previewDocument())
	var shape map[string]any
	_ = json.Unmarshal(encoded, &shape)
	shape["connections"].(map[string]any)["bindings"] = []map[string]any{{"site_account_id": 99, "channel_id": 73, "projection_key": "imported"}}
	encoded, _ = json.Marshal(shape)
	var document backupDocument
	_ = json.Unmarshal(encoded, &document)
	preview, err := target.server.previewBackupDocument(ctx, &document)
	if err != nil || preview.CanApply || preview.Summary["conflict"] != 1 {
		t.Fatal("a channel already bound to another account did not produce a binding conflict")
	}
	if _, err := target.server.applyConfigPreview(ctx, preview.ID); err == nil {
		t.Fatal("conflicted binding restore was applied")
	}
	bindings, _ := target.store.ListSiteChannelBindings(ctx)
	accounts, _ := target.store.ListSiteAccounts(ctx, 0, false)
	if len(bindings) != 1 || bindings[0].ProjectionKey != "occupied" || bindings[0].SiteAccountID != account.ID || len(accounts) != 1 {
		t.Fatal("binding conflict partially changed the target account/channel association")
	}
}

func TestBackupRestoreScheduleChangePreservesNewerTargetVerification(t *testing.T) {
	ctx := context.Background()
	target := newBackupRestoreDatabase(t, filepath.Join(t.TempDir(), "target.db"), "fedcba9876543210fedcba9876543210", "target")
	if err := target.server.importBackupTarget(ctx, &backupTarget{Enabled: true, FileURL: "https://synthetic-observation.invalid/backup.json", Username: "synthetic-user", Password: "synthetic-observation-auth", ExportType: "all", AutoSyncIntervalHours: 24}); err != nil {
		t.Fatal(err)
	}
	config, _ := target.store.GetBackupConfig(ctx)
	config.LastSyncAt, config.LastVerifiedAt = 1700000000000, 1700000000000
	config.LastVerifiedBytes, config.LastVerifiedSHA256, config.LastVerifiedType = 2400, strings.Repeat("a", 64), "all"
	if err := target.store.UpsertBackupConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	input, _ := target.server.exportBackupTarget(ctx)
	input.AutoSyncIntervalHours = 48
	document := &backupDocument{Version: "1.0", Type: "settings", BackupTarget: input}
	preview, err := target.server.previewBackupDocument(ctx, document)
	if err != nil || !preview.CanApply {
		t.Fatal("schedule-only change did not produce a valid preview")
	}
	// A completed upload after preview is a runtime observation, not an edit.
	config.LastSyncAt, config.LastVerifiedAt = 1700000010000, 1700000010000
	config.LastVerifiedBytes, config.LastVerifiedSHA256, config.LastVerifiedWarningCount = 2600, strings.Repeat("b", 64), 1
	if err := target.store.UpsertBackupConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	result, err := target.server.applyConfigPreview(ctx, preview.ID)
	if err != nil || result.Status != "applied" {
		t.Fatal("new verification evidence incorrectly invalidated a configuration-only preview")
	}
	restored, err := target.store.GetBackupConfig(ctx)
	if err != nil || restored.AutoSyncIntervalHours != 48 || restored.LastVerifiedAt != 1700000010000 || restored.LastSyncAt != restored.LastVerifiedAt || restored.LastVerifiedSHA256 != strings.Repeat("b", 64) || restored.LastVerifiedBytes != 2600 || restored.LastVerifiedWarningCount != 1 || restored.PasswordCiphertext != config.PasswordCiphertext {
		t.Fatal("schedule import erased or replaced the destination's newer verification evidence/credential")
	}
}

func TestBackupRestoreDifferentDestinationClearsTargetVerification(t *testing.T) {
	ctx := context.Background()
	target := newBackupRestoreDatabase(t, filepath.Join(t.TempDir(), "target.db"), "fedcba9876543210fedcba9876543210", "target")
	config := &model.BackupConfig{Enabled: true, FileURL: "https://synthetic-old-observation.invalid/backup.json", ExportType: "all", AutoSyncIntervalHours: 24,
		LastSyncAt: 1700000000000, LastVerifiedAt: 1700000000000, LastVerifiedBytes: 2400, LastVerifiedSHA256: strings.Repeat("a", 64), LastVerifiedType: "all", LastError: "webdav_http_502"}
	if err := target.store.UpsertBackupConfig(ctx, config); err != nil {
		t.Fatal(err)
	}
	document := &backupDocument{Version: "1.0", Type: "settings", BackupTarget: &backupTarget{Enabled: true, FileURL: "https://synthetic-new-observation.invalid/backup.json", ExportType: "all", AutoSyncIntervalHours: 24}}
	applyRestoreFixture(t, target, document)
	restored, _ := target.store.GetBackupConfig(ctx)
	if restored.LastSyncAt != 0 || restored.LastVerifiedAt != 0 || restored.LastVerifiedSHA256 != "" || restored.LastVerifiedBytes != 0 || restored.LastError != "" {
		t.Fatal("import attributed the old target's upload/verification/error to a different WebDAV destination")
	}
}

func TestBackupRestoreOrphanBindingCannotOwnNewChannel(t *testing.T) {
	ctx := context.Background()
	target := newBackupRestoreDatabase(t, filepath.Join(t.TempDir(), "target.db"), "fedcba9876543210fedcba9876543210", "target")
	_, account := restoreAccountFixture(t, target, "Orphan binding", "https://synthetic-orphan.invalid", model.SitePlatformOpenAICompatible, "orphan", model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-orphan-management"})
	restoreBindingFixture(t, target, account.ID, 0, "orphan", "projected")
	defer func() {
		if recover() != nil {
			t.Fatal("a missing-channel binding was mistaken for ownership of a new channel")
		}
	}()
	preview, err := target.server.previewBackupDocument(ctx, previewDocument())
	if err != nil || !preview.CanApply {
		t.Fatal("an unrelated orphan link blocked a valid new-channel preview")
	}
	result, err := target.server.applyConfigPreview(ctx, preview.ID)
	if err != nil || result.Status != "applied" {
		t.Fatal("orphan link prevented the valid configuration import")
	}
	bindings, _ := target.store.ListSiteChannelBindings(ctx)
	if len(bindings) != 1 || bindings[0].ChannelID != 0 || bindings[0].Ownership != "projected" {
		t.Fatal("import guessed a channel for an unlisted orphan binding")
	}
}

func TestBackupRestoreLegacyNameReleaseRollsBackWithFailedUpdate(t *testing.T) {
	ctx := context.Background()
	target := newBackupRestoreDatabase(t, filepath.Join(t.TempDir(), "target.db"), "fedcba9876543210fedcba9876543210", "target")
	active, _ := target.store.CreateSite(ctx, &model.Site{Name: "Active before", BaseURL: "https://synthetic-rollback-active.invalid", Platform: model.SitePlatformOpenAICompatible, Enabled: true})
	deleted, _ := target.store.CreateSite(ctx, &model.Site{Name: "Historical reusable", BaseURL: "https://synthetic-rollback-deleted.invalid", Platform: model.SitePlatformOpenAICompatible})
	if _, err := target.store.ExecContext(ctx, "UPDATE sites SET deleted_at=9000 WHERE id=?", deleted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := target.store.ExecContext(ctx, `CREATE TRIGGER reject_live_site_update BEFORE UPDATE OF name ON sites WHEN OLD.deleted_at=0 BEGIN SELECT RAISE(ABORT,'synthetic-update-failure'); END`); err != nil {
		t.Fatal(err)
	}
	document := &backupDocument{Version: "1.0", Type: "connections", Connections: &backupConnections{Sites: []*model.Site{{ID: 101, Name: "Historical reusable", BaseURL: active.BaseURL, Enabled: true, Platform: model.SitePlatformOpenAICompatible}}}}
	preview, err := target.server.previewBackupDocument(ctx, document)
	if err != nil || !preview.CanApply {
		t.Fatal("valid name reuse did not produce a preview")
	}
	if _, err := target.server.applyConfigPreview(ctx, preview.ID); err == nil {
		t.Fatal("injected update failure was ignored")
	}
	before, _ := target.store.GetSite(ctx, active.ID)
	history, _ := target.store.GetSite(ctx, deleted.ID)
	if before.Name != "Active before" || history.Name != "Historical reusable" || history.DeletedAt != 9000 {
		t.Fatal("failed import committed a historical name release or a partial live update")
	}
}
