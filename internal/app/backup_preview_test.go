package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

func backupPreviewServer(t *testing.T) *Server {
	t.Helper()
	s := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "preview-test")
	if err != nil {
		t.Fatal(err)
	}
	s.siteControl.cipher = cipher
	return s
}

func previewDocument() *backupDocument {
	return &backupDocument{Version: "1.0", Type: "connections", Connections: &backupConnections{
		Sites:    []*model.Site{{ID: 42, Name: "Preview Site", BaseURL: "https://preview.example/v1", Platform: model.SitePlatformOpenAICompatible, Enabled: true}},
		Accounts: []backupAccount{{Account: &model.SiteAccount{ID: 99, SiteID: 42, Label: "primary", CredentialType: model.CredentialTypeAPIKey, Enabled: true}, Credentials: provider.Credentials{APIKey: "synthetic-preview-upstream"}}},
		Channels: []backupChannel{{Config: &model.Config{ID: 73, Name: "Preview Channel", URLs: model.ChannelURLs{{URL: "https://preview.example"}}, Enabled: true, CostMultiplier: 1, ModelEntries: []model.ModelEntry{{Model: "demo"}}}, APIKeys: []model.APIKey{{KeyIndex: 0, APIKey: "synthetic-channel-key", CostMultiplier: 1}}}},
		Tokens:   []backupToken{{PlainToken: "synthetic-client-token", Description: "Preview token", AllowedChannelIDs: []int64{73}, IsActive: true}},
	}}
}

func TestBackupPreviewReadOnlyAndStableReapplication(t *testing.T) {
	s := backupPreviewServer(t)
	ctx := context.Background()
	doc := previewDocument()
	preview, err := s.previewBackupDocument(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.CanApply || preview.Summary["create"] != 4 {
		t.Fatalf("preview=%+v", preview)
	}
	channels, _ := s.store.ListConfigs(ctx)
	sites, _ := s.store.ListSites(ctx, model.SiteListFilter{})
	if len(channels)+len(sites) != 0 {
		t.Fatal("preview wrote configurations")
	}
	raw, _ := json.Marshal(preview)
	for _, secret := range []string{doc.Connections.Accounts[0].Credentials.APIKey, doc.Connections.Channels[0].APIKeys[0].APIKey, doc.Connections.Tokens[0].PlainToken} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("preview exposed a credential")
		}
	}
	result, err := s.applyConfigPreview(ctx, preview.ID)
	if err != nil || result.Backup.Channels != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err = s.applyConfigPreview(ctx, preview.ID); err != nil {
		t.Fatal(err)
	}
	preview, err = s.previewBackupDocument(ctx, doc)
	if err != nil || preview.Summary["create"] != 0 || preview.Summary["update"] != 0 {
		t.Fatalf("repeat=%+v err=%v", preview, err)
	}
	tokens, _ := s.store.ListAuthTokens(ctx)
	channels, _ = s.store.ListConfigs(ctx)
	if len(tokens) != 1 || len(channels) != 1 || len(tokens[0].AllowedChannelIDs) != 1 || tokens[0].AllowedChannelIDs[0] != channels[0].ID {
		t.Fatal("references were not remapped")
	}
}

func TestBackupPreviewUsesCurrentKeyObservationsInsteadOfBackupHealth(t *testing.T) {
	for _, rotated := range []bool{false, true} {
		t.Run(map[bool]string{false: "same_credential", true: "rotated_credential"}[rotated], func(t *testing.T) {
			s := backupPreviewServer(t)
			ctx := context.Background()
			doc := previewDocument()
			initial, err := s.previewBackupDocument(ctx, doc)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.applyConfigPreview(ctx, initial.ID); err != nil {
				t.Fatal(err)
			}
			channels, err := s.store.ListConfigs(ctx)
			if err != nil || len(channels) != 1 {
				t.Fatalf("channel count=%d err=%v", len(channels), err)
			}
			doc.Connections.Channels[0].Config.Priority = 17
			doc.Connections.Channels[0].APIKeys[0].Health = model.APIKeyHealth{Status: "invalid", Reason: "obsolete backup observation", StatusCode: 401, CheckedAt: 10}
			if rotated {
				doc.Connections.Channels[0].APIKeys[0].APIKey = "synthetic-replaced-key"
			}
			preview, err := s.previewBackupDocument(ctx, doc)
			if err != nil || !preview.CanApply {
				t.Fatalf("preview unavailable err=%v", err)
			}
			queryStore, ok := s.store.(*sqlstore.SQLStore)
			if !ok {
				t.Fatal("test requires SQL store")
			}
			cooldownUntil := time.Now().Add(time.Hour).Unix()
			observedAt := time.Now().UnixMilli()
			if _, err = queryStore.ExecContext(ctx, "UPDATE api_keys SET cooldown_until=?, cooldown_duration_ms=?, health_status=?, health_reason=?, health_status_code=?, health_checked_at=? WHERE channel_id=?", cooldownUntil, 60000, "rate_limited", "latest runtime observation", 429, observedAt, channels[0].ID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.applyConfigPreview(ctx, preview.ID); err != nil {
				t.Fatal(err)
			}
			keys, err := s.store.GetAPIKeys(ctx, channels[0].ID)
			if err != nil || len(keys) != 1 {
				t.Fatalf("key count=%d err=%v", len(keys), err)
			}
			key := keys[0]
			if rotated {
				if key.CooldownUntil != 0 || key.CooldownDurationMs != 0 || key.Health != (model.APIKeyHealth{}) {
					t.Fatal("new credential inherited old credential cooldown or backup health")
				}
			} else if key.CooldownUntil != cooldownUntil || key.CooldownDurationMs != 60000 || key.Health.StatusCode != 429 || key.Health.CheckedAt != observedAt || key.Health.Reason != "latest runtime observation" {
				t.Fatal("configuration import overwrote newer runtime cooldown or key health")
			}
		})
	}
}

type unavailableBackupPreviewStore struct{ storage.Store }

func (unavailableBackupPreviewStore) GetBackupConfig(context.Context) (*model.BackupConfig, error) {
	return nil, errors.New("synthetic-private-sql-credential")
}

func TestBackupWebDAVPreviewDoesNotExposeStorageFailure(t *testing.T) {
	s := backupPreviewServer(t)
	original := s.store
	s.store = unavailableBackupPreviewStore{Store: original}
	defer func() { s.store = original }()
	router := gin.New()
	router.POST("/admin/backup/webdav/preview", s.HandleBackupWebDAVPreview)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/backup/webdav/preview", strings.NewReader("{}")))
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), "private") || !strings.Contains(response.Body.String(), "snapshot_failed") {
		t.Fatalf("WebDAV preview exposed a storage failure: status=%d", response.Code)
	}
}

func TestBackupPreviewRejectsConflictsBeforeAnyWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*backupDocument)
	}{
		{"missing channel", func(d *backupDocument) { d.Connections.Tokens[0].AllowedChannelIDs = []int64{777} }},
		{"duplicate source id", func(d *backupDocument) {
			d.Connections.Sites = append(d.Connections.Sites, &model.Site{ID: 42, Name: "other", BaseURL: "https://other.example"})
		}},
		{"missing account parent", func(d *backupDocument) { d.Connections.Accounts[0].Account.SiteID = 777 }},
		{"invalid channel", func(d *backupDocument) { d.Connections.Channels[0].Config.URLs = nil }},
		{"negative token limit", func(d *backupDocument) { d.Connections.Tokens[0].MaxConcurrency = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := backupPreviewServer(t)
			doc := previewDocument()
			tc.change(doc)
			p, err := s.previewBackupDocument(context.Background(), doc)
			if err != nil {
				t.Fatal(err)
			}
			if p.CanApply || p.Summary["conflict"] == 0 {
				t.Fatalf("invalid preview=%+v", p)
			}
			if _, err = s.applyConfigPreview(context.Background(), p.ID); err == nil {
				t.Fatal("applied conflicts")
			}
			sites, _ := s.store.ListSites(context.Background(), model.SiteListFilter{})
			if len(sites) != 0 {
				t.Fatal("partial import")
			}
		})
	}
}

func TestBackupPreviewRejectsConcurrentEdit(t *testing.T) {
	s := backupPreviewServer(t)
	ctx := context.Background()
	p, err := s.previewBackupDocument(ctx, previewDocument())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.store.CreateSite(ctx, &model.Site{Name: "concurrent", BaseURL: "https://concurrent.example"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.applyConfigPreview(ctx, p.ID)
	if err == nil || !strings.Contains(err.Error(), "preview_stale") {
		t.Fatalf("stale err=%v", err)
	}
	channels, _ := s.store.ListConfigs(ctx)
	if len(channels) != 0 {
		t.Fatal("stale wrote")
	}
}

func TestBackupProtocolRestrictionsSurviveRoundTrip(t *testing.T) {
	s := backupPreviewServer(t)
	ctx := context.Background()
	sealed, _ := s.siteControl.cipher.Seal("synthetic-protocol-token")
	token := &model.AuthToken{Token: model.HashToken("synthetic-protocol-token"), TokenCiphertext: sealed, Description: "protocol", IsActive: true, AllowedProtocols: []string{"openai"}}
	if err := s.store.CreateAuthToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	doc, err := s.buildBackupDocument(ctx, "connections")
	if err != nil {
		t.Fatal(err)
	}
	target := backupPreviewServer(t)
	if _, err = target.importBackupDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	tokens, err := target.store.ListAuthTokens(ctx)
	if err != nil || len(tokens) != 1 || tokens[0].IsProtocolAllowed("anthropic") {
		t.Fatal("restored token gained protocols")
	}
}

func TestBackupPreviewDatabaseFailureRollsBackEveryEntry(t *testing.T) {
	s := backupPreviewServer(t)
	ctx := context.Background()
	db := s.store.(*sqlstore.SQLStore)
	_, err := db.ExecContext(ctx, `CREATE TRIGGER reject_preview_token BEFORE INSERT ON auth_tokens BEGIN SELECT RAISE(ABORT, 'synthetic-hidden-storage-error'); END`)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.previewBackupDocument(ctx, previewDocument())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.applyConfigPreview(ctx, p.ID)
	if err == nil || err.Error() != "apply_failed: tokens[0]" {
		t.Fatalf("unsafe failure=%v", err)
	}
	channels, _ := s.store.ListConfigs(ctx)
	sites, _ := s.store.ListSites(ctx, model.SiteListFilter{})
	accounts, _ := s.store.ListSiteAccounts(ctx, 0, false)
	if len(channels)+len(sites)+len(accounts) != 0 {
		t.Fatal("backup partially committed")
	}
}

func TestBackupPreviewFreezesWebDAVDocument(t *testing.T) {
	s := backupPreviewServer(t)
	ctx := context.Background()
	doc := previewDocument()
	var gets atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	}))
	defer remote.Close()
	if err := s.store.UpsertBackupConfig(ctx, &model.BackupConfig{Enabled: true, FileURL: remote.URL + "/backup.json", ExportType: "connections", AutoSyncIntervalHours: 24}); err != nil {
		t.Fatal(err)
	}
	c, recorder := newTestContext(t, newJSONRequest(t, http.MethodPost, "/admin/backup/webdav/preview", map[string]any{}))
	s.HandleBackupWebDAVPreview(c)
	if recorder.Code != 200 {
		t.Fatalf("preview %d", recorder.Code)
	}
	var p configPreview
	mustUnmarshalAPIResponseData(t, recorder.Body.Bytes(), &p)
	doc.Connections.Channels[0].Config.Name = "changed upstream after preview"
	if _, err := s.applyConfigPreview(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	channels, _ := s.store.ListConfigs(ctx)
	if len(channels) != 1 || channels[0].Name != "Preview Channel" || gets.Load() != 1 {
		t.Fatal("apply downloaded different content")
	}
}

func TestBackupPreviewLegacyProtocolPreservationAndExplicitUnrestrict(t *testing.T) {
	s := backupPreviewServer(t)
	ctx := context.Background()
	doc := previewDocument()
	if _, err := s.importBackupDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	tokens, _ := s.store.ListAuthTokens(ctx)
	tokens[0].AllowedProtocols = []string{"openai"}
	if err := s.store.UpdateAuthToken(ctx, tokens[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.importBackupDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	tokens, _ = s.store.ListAuthTokens(ctx)
	if tokens[0].IsProtocolAllowed("gemini") {
		t.Fatal("old backup cleared missing protocol restriction")
	}
	doc.Connections.Tokens[0].AllowedProtocols = []string{}
	if _, err := s.importBackupDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	tokens, _ = s.store.ListAuthTokens(ctx)
	if !tokens[0].IsProtocolAllowed("gemini") {
		t.Fatal("explicit unrestricted value not applied")
	}
}

func TestBackupPreviewInvalidRulesAndTokenModelsConflict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*backupDocument)
	}{
		{"bad rule", func(d *backupDocument) {
			d.Connections.Channels[0].Config.CustomRequestRules = &model.CustomRequestRules{Headers: []model.CustomHeaderRule{{Action: "unknown", Name: "test"}}}
		}},
		{"bad key strategy", func(d *backupDocument) { d.Connections.Channels[0].APIKeys[0].KeyStrategy = "unknown" }},
		{"bad auth type", func(d *backupDocument) { d.Connections.Channels[0].Config.AuthType = "unknown" }},
		{"bad protocol mode", func(d *backupDocument) { d.Connections.Channels[0].Config.ProtocolTransformMode = "unknown" }},
		{"case duplicate model", func(d *backupDocument) {
			d.Connections.Channels[0].Config.ModelEntries = append(d.Connections.Channels[0].Config.ModelEntries, model.ModelEntry{Model: "DEMO"})
		}},
		{"blank restricted model", func(d *backupDocument) { d.Connections.Tokens[0].AllowedModels = []string{" "} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := backupPreviewServer(t)
			doc := previewDocument()
			tc.change(doc)
			p, err := s.previewBackupDocument(context.Background(), doc)
			if err != nil {
				t.Fatal(err)
			}
			if p.CanApply {
				t.Fatalf("accepted %s", tc.name)
			}
		})
	}
}

func TestBackupPreviewAccountsReportLinkedChannelDeactivation(t *testing.T) {
	s := backupPreviewServer(t)
	ctx := context.Background()
	doc := previewDocument()
	if _, err := s.importBackupDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	accounts, _ := s.store.ListSiteAccounts(ctx, 0, false)
	projection, err := s.store.UpsertSiteProjection(ctx, model.SiteProjectionInput{SiteAccountID: accounts[0].ID, ProjectionKey: "auto", Name: "Linked channel", BaseURL: "https://preview.example", APIKey: "synthetic-linked", Models: []string{"demo"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	doc.Connections.Accounts[0].Account.Enabled = false
	p, err := s.previewBackupDocument(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	if p.Summary["deactivate"] != 1 {
		t.Fatalf("hidden side effect: %+v", p)
	}
	if _, err = s.applyConfigPreview(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	channel, err := s.store.GetConfig(ctx, projection.Channel.ID)
	if err != nil || channel.Enabled {
		t.Fatal("linked channel should be disabled")
	}
}

func TestBackupPreviewDeclaresProjectionOwnershipChange(t *testing.T) {
	s := backupPreviewServer(t)
	ctx := context.Background()
	doc := previewDocument()
	if _, err := s.importBackupDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	accounts, _ := s.store.ListSiteAccounts(ctx, 0, false)
	_, err := s.store.UpsertSiteProjection(ctx, model.SiteProjectionInput{SiteAccountID: accounts[0].ID, ProjectionKey: "auto", Name: "Projected", BaseURL: "https://preview.example", APIKey: "synthetic-linked", Models: []string{"demo"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := s.buildBackupDocument(ctx, "connections")
	if err != nil {
		t.Fatal(err)
	}
	for i := range exported.Connections.Channels {
		if exported.Connections.Channels[i].Config.Name == "Projected" {
			exported.Connections.Channels[i].Config.Priority = 25
		}
	}
	p, err := s.previewBackupDocument(ctx, exported)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range p.Items {
		if item.Label == "Projected" && item.Action == "update" {
			found = slices.Contains(item.Fields, "ownership")
		}
	}
	if !found {
		t.Fatal("ownership transfer not shown")
	}
}

func TestBackupPreviewInvalidatesWhenAccountEligibilityChanges(t *testing.T) {
	s := backupPreviewServer(t)
	ctx := context.Background()
	doc := previewDocument()
	if _, err := s.importBackupDocument(ctx, doc); err != nil {
		t.Fatal(err)
	}
	accounts, err := s.store.ListSiteAccounts(ctx, 0, false)
	if err != nil || len(accounts) != 1 {
		t.Fatal("fixture account unavailable")
	}
	projection, err := s.store.UpsertSiteProjection(ctx, model.SiteProjectionInput{SiteAccountID: accounts[0].ID, ProjectionKey: "eligibility", Name: "Eligibility channel", BaseURL: "https://preview.example", APIKey: "synthetic-eligibility", Models: []string{"demo"}, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	doc.Connections.Accounts[0].Account.AutoRefresh = !accounts[0].AutoRefresh
	preview, err := s.previewBackupDocument(ctx, doc)
	if err != nil || preview.Summary["deactivate"] != 0 {
		t.Fatal("unexpected initial deactivation")
	}
	queryStore := s.store.(*sqlstore.SQLStore)
	if _, err = queryStore.ExecContext(ctx, "UPDATE site_accounts SET status=? WHERE id=?", model.SiteAccountStatusExpired, accounts[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.applyConfigPreview(ctx, preview.ID); err == nil || !strings.Contains(err.Error(), "preview_stale") {
		t.Fatal("application changed account eligibility without invalidating preview")
	}
	channel, err := s.store.GetConfig(ctx, projection.Channel.ID)
	if err != nil || !channel.Enabled {
		t.Fatal("application made an unpreviewed channel deactivation")
	}
}

func TestBackupPreviewClearsOptionalFieldsAndListsTheirRemoval(t *testing.T) {
	for _, field := range []string{"proxy_url", "custom_request_rules", "websockets", "available_time_start", "site_proxy_url"} {
		for _, additional := range []bool{false, true} {
			t.Run(field+map[bool]string{false: "/only_clear", true: "/clear_and_update"}[additional], func(t *testing.T) {
				s := backupPreviewServer(t)
				ctx := context.Background()
				doc := previewDocument()
				config := doc.Connections.Channels[0].Config
				switch field {
				case "proxy_url":
					config.ProxyURL = "http://synthetic-proxy.example:8080"
				case "custom_request_rules":
					config.CustomRequestRules = &model.CustomRequestRules{Headers: []model.CustomHeaderRule{{Action: model.RuleActionOverride, Name: "X-Synthetic", Value: "old"}}}
				case "websockets":
					config.Websockets = true
				case "available_time_start":
					config.AvailableTimeStart, config.AvailableTimeEnd = "08:00", "22:00"
				case "site_proxy_url":
					doc.Connections.Sites[0].ProxyURL = "http://synthetic-proxy.example:8080"
				}
				if _, err := s.importBackupDocument(ctx, doc); err != nil {
					t.Fatal(err)
				}
				switch field {
				case "proxy_url":
					config.ProxyURL = ""
				case "custom_request_rules":
					config.CustomRequestRules = nil
				case "websockets":
					config.Websockets = false
				case "available_time_start":
					config.AvailableTimeStart, config.AvailableTimeEnd = "", ""
				case "site_proxy_url":
					doc.Connections.Sites[0].ProxyURL = ""
				}
				if additional {
					if field == "site_proxy_url" {
						doc.Connections.Sites[0].Enabled = false
					} else {
						config.Priority = 23
					}
				}
				preview, err := s.previewBackupDocument(ctx, doc)
				if err != nil || !preview.CanApply {
					t.Fatal("preview unavailable")
				}
				key, expectedField := "channels[0]", field
				if field == "site_proxy_url" {
					key, expectedField = "sites[0]", "proxy_url"
				}
				found := false
				for _, item := range preview.Items {
					if item.Key == key {
						found = item.Action == "update" && slices.Contains(item.Fields, expectedField)
					}
				}
				if !found {
					t.Fatal("cleared field is missing from the preview difference")
				}
				if _, err = s.applyConfigPreview(ctx, preview.ID); err != nil {
					t.Fatal(err)
				}
				channels, _ := s.store.ListConfigs(ctx)
				sites, _ := s.store.ListSites(ctx, model.SiteListFilter{})
				if len(channels) != 1 || len(sites) != 1 {
					t.Fatal("unexpected imported identities")
				}
				got := channels[0]
				if got.ProxyURL != "" || got.CustomRequestRules != nil || got.Websockets || got.AvailableTimeStart != "" || got.AvailableTimeEnd != "" || sites[0].ProxyURL != "" {
					t.Fatal("cleared optional field remained configured")
				}
			})
		}
	}
}

type unavailableTokenReloadStore struct{ storage.Store }

func (unavailableTokenReloadStore) ListActiveAuthTokens(context.Context) ([]*model.AuthToken, error) {
	return nil, errors.New("synthetic-private-token-cache-read")
}

func TestBackupPreviewReportsCommittedButTokenReloadFailed(t *testing.T) {
	s := backupPreviewServer(t)
	ctx := context.Background()
	original := s.authService.store
	s.authService.store = unavailableTokenReloadStore{Store: original}
	defer func() { s.authService.store = original }()
	preview, err := s.previewBackupDocument(ctx, previewDocument())
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.applyConfigPreview(ctx, preview.ID)
	if err != nil || result == nil || result.Status != "applied" || result.Warning != "token_reload_failed" {
		t.Fatal("committed import silently ignored token authorization reload failure")
	}
	tokens, err := s.store.ListAuthTokens(ctx)
	if err != nil || len(tokens) != 1 {
		t.Fatal("committed token missing after runtime reload warning")
	}
}

func TestBackupPreviewTokenReloadReadsPrimaryAfterCacheDegradation(t *testing.T) {
	primary := backupPreviewServer(t)
	cache := backupPreviewServer(t)
	ctx := context.Background()
	hash := model.HashToken("synthetic-revoked-primary-token")
	if err := primary.store.CreateAuthToken(ctx, &model.AuthToken{Token: hash, Description: "revoked", IsActive: false}); err != nil {
		t.Fatal(err)
	}
	if err := cache.store.CreateAuthToken(ctx, &model.AuthToken{Token: hash, Description: "stale", IsActive: true}); err != nil {
		t.Fatal(err)
	}
	hybrid := storage.NewHybridStore(cache.store.(*sqlstore.SQLStore), primary.store.(*sqlstore.SQLStore))
	t.Cleanup(func() { _ = hybrid.Close() })
	original := primary.authService.store
	primary.authService.store = hybrid
	defer func() { primary.authService.store = original }()
	if err := primary.authService.ReloadAuthTokens(); err != nil {
		t.Fatal(err)
	}
	if primary.authService.IsTokenActive(hash) {
		t.Fatal("authoritative revoked token became active from stale SQLite replica")
	}
}
