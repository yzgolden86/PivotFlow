package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

type browserImportTestAdapter struct {
	platform string
	calls    atomic.Int64
	userID   int64
	failure  error
	onCall   func(context.Context, provider.AccountRequest) error
}

func (a *browserImportTestAdapter) ID() string { return a.platform }
func (a *browserImportTestAdapter) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Models: true, Balance: true, CredentialTypes: []string{model.CredentialTypeAPIKey, model.CredentialTypeAccessToken, model.CredentialTypeCookie}}
}
func (a *browserImportTestAdapter) Detect(context.Context, string) (provider.DetectionResult, error) {
	return provider.DetectionResult{Matched: true, ProviderID: a.platform}, nil
}
func (a *browserImportTestAdapter) call(ctx context.Context, request provider.AccountRequest) error {
	a.calls.Add(1)
	if a.onCall != nil {
		if err := a.onCall(ctx, request); err != nil {
			return err
		}
	}
	return a.failure
}
func (a *browserImportTestAdapter) ResolveManagementCredentials(ctx context.Context, request provider.AccountRequest) (provider.Credentials, error) {
	if err := a.call(ctx, request); err != nil {
		return provider.Credentials{}, err
	}
	credential := request.Credentials
	credential.UserID = a.userID
	return credential, nil
}
func (a *browserImportTestAdapter) RefreshAccount(ctx context.Context, request provider.AccountRequest) (provider.AccountSnapshot, error) {
	if err := a.call(ctx, request); err != nil {
		return provider.AccountSnapshot{}, err
	}
	balance := 17.25
	return provider.AccountSnapshot{Username: "verified-user", UserID: a.userID, Balance: &balance, Currency: "USD", Status: model.SiteAccountStatusHealthy}, nil
}
func (a *browserImportTestAdapter) ListModels(ctx context.Context, request provider.AccountRequest) ([]provider.ModelSnapshot, error) {
	if err := a.call(ctx, request); err != nil {
		return nil, err
	}
	return []provider.ModelSnapshot{{Model: "verified-model", RouteType: "openai", Source: "fixture"}}, nil
}
func (*browserImportTestAdapter) Checkin(context.Context, provider.AccountRequest) (provider.CheckinResult, error) {
	return provider.CheckinResult{}, errors.New("unexpected checkin")
}
func (*browserImportTestAdapter) ListAnnouncements(context.Context, provider.AccountRequest) ([]provider.Announcement, error) {
	return nil, errors.New("unexpected announcements")
}

func browserImportPlanFixture(t *testing.T, platform string) (*Server, *browserImportTestAdapter) {
	t.Helper()
	s := backupPreviewServer(t)
	adapter := &browserImportTestAdapter{platform: platform, userID: 42}
	s.siteControl.registry = provider.NewRegistry(adapter)
	return s, adapter
}

func browserImportPreviewInput(entry map[string]any, mode string, target int64) map[string]any {
	return map[string]any{"document": browserImportDocumentFixture(entry), "entry_index": 0, "target_account_id": target, "mode": mode, "source_confirmed": true}
}

func browserImportPreviewAPI(t *testing.T, s *Server, input map[string]any, status int) (*configPreview, map[string]any) {
	t.Helper()
	response := browserImportAPI(t, browserImportTestEngine(t, s), "/admin/browser-import/preview", input, status)
	if status != http.StatusOK {
		return nil, response
	}
	raw, _ := json.Marshal(response["data"])
	var preview configPreview
	if err := json.Unmarshal(raw, &preview); err != nil {
		t.Fatal(err)
	}
	return &preview, response["data"].(map[string]any)
}

func browserImportExistingAccount(t *testing.T, s *Server, platform, kind string, credential provider.Credentials) (*model.Site, *model.SiteAccount) {
	t.Helper()
	ctx := context.Background()
	site, err := s.store.CreateSite(ctx, &model.Site{Name: "Existing", BaseURL: "https://source.example", Platform: platform, Enabled: false, UseSystemProxy: false, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.siteControl.cipher.Seal(credential)
	if err != nil {
		t.Fatal(err)
	}
	balance := 7.5
	account, err := s.store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "existing-user", CredentialType: kind, CredentialCiphertext: sealed, CredentialKeyVersion: s.siteControl.cipher.Version(), Enabled: false, AutoRefresh: false, AutoCheckin: false, Timezone: "Asia/Shanghai", Status: model.SiteAccountStatusHealthy, Balance: &balance, BalanceCurrency: "USD", LastRefreshStatus: "success", LastCheckinStatus: "already_checked"})
	if err != nil {
		t.Fatal(err)
	}
	return site, account
}

func TestBrowserImportPreviewVerifiedCreateIsFrozenAndDisabled(t *testing.T) {
	s, adapter := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
	entry := browserImportEntryFixture()
	originalPrimary := entry["credential"].(map[string]any)["api_key"].(string)
	preview, data := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "verify", 0), http.StatusOK)
	if !preview.CanApply || preview.Kind != "browser_import" || adapter.calls.Load() != 1 {
		t.Fatal("verified import preview was not generated")
	}
	metadata := data["browser_import"].(map[string]any)
	verification := metadata["verification"].(map[string]any)
	if verification["scope"] != "models" || verification["status"] != "verified" || verification["model_count"] != float64(1) {
		t.Fatal("verification scope was not represented")
	}
	ctx := context.Background()
	sites, _ := s.store.ListSites(ctx, model.SiteListFilter{})
	if len(sites) != 0 {
		t.Fatal("verify created a site")
	}
	entry["credential"] = map[string]any{"api_key": "synthetic-changed-after-preview"}
	result, err := s.applyConfigPreview(ctx, preview.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.applyConfigPreview(ctx, preview.ID); err != nil {
		t.Fatal(err)
	}
	accounts, _ := s.store.ListSiteAccounts(ctx, 0, false)
	sites, _ = s.store.ListSites(ctx, model.SiteListFilter{})
	if len(accounts) != 1 || len(sites) != 1 || accounts[0].Enabled || accounts[0].AutoCheckin || accounts[0].AutoRefresh || !sites[0].Enabled || !sites[0].UseSystemProxy || accounts[0].Status != model.SiteAccountStatusUnknown {
		t.Fatal("new account defaults or application idempotency are incorrect")
	}
	var credential provider.Credentials
	if s.siteControl.cipher.Open(accounts[0].CredentialCiphertext, &credential) != nil || credential.APIKey != originalPrimary {
		t.Fatal("preview did not retain the original credential")
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), originalPrimary) || !strings.Contains(string(raw), `"account_id":`) || !strings.Contains(string(raw), `"browser_import":`) {
		t.Fatal("result omitted its target or exposed a secret")
	}
	if adapter.calls.Load() != 1 {
		t.Fatal("apply repeated upstream verification")
	}
}

func TestBrowserImportPreviewDisabledSaveMakesNoUpstreamRequest(t *testing.T) {
	s, adapter := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
	preview, data := browserImportPreviewAPI(t, s, browserImportPreviewInput(browserImportEntryFixture(), "save_disabled", 0), http.StatusOK)
	if !preview.CanApply || adapter.calls.Load() != 0 || data["browser_import"].(map[string]any)["verification"].(map[string]any)["status"] != "unverified" {
		t.Fatal("unverified save contacted upstream or claimed verification")
	}
	if _, err := s.applyConfigPreview(context.Background(), preview.ID); err != nil {
		t.Fatal(err)
	}
	accounts, _ := s.store.ListSiteAccounts(context.Background(), 0, false)
	if len(accounts) != 1 || accounts[0].Enabled || adapter.calls.Load() != 0 {
		t.Fatal("disabled import enabled or contacted upstream")
	}
}

func TestBrowserImportPreviewRejectsUnconfirmedSourceAndDifferentTarget(t *testing.T) {
	s, adapter := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
	input := browserImportPreviewInput(browserImportEntryFixture(), "verify", 0)
	input["source_confirmed"] = false
	browserImportPreviewAPI(t, s, input, http.StatusBadRequest)
	_, account := browserImportExistingAccount(t, s, model.SitePlatformOpenAICompatible, model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-old-target"})
	entry := browserImportEntryFixture()
	entry["source_origin"], entry["base_url"] = "https://other.example", "https://other.example"
	browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "verify", account.ID), http.StatusBadRequest)
	if adapter.calls.Load() != 0 {
		t.Fatal("unconfirmed or wrong target sent credentials")
	}
}

func TestBrowserImportPreviewSameCredentialIsUnchangedWithoutReactivation(t *testing.T) {
	s, adapter := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
	site, account := browserImportExistingAccount(t, s, model.SitePlatformOpenAICompatible, model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-browser-primary"})
	entry := browserImportEntryFixture()
	entry["credential"] = map[string]any{"api_key": "synthetic-browser-primary"}
	preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "save_disabled", 0), http.StatusOK)
	if !preview.CanApply || preview.Summary["create"] != 0 || preview.Summary["update"] != 0 || adapter.calls.Load() != 0 {
		t.Fatal("exact credential created or changed an account")
	}
	if _, err := s.applyConfigPreview(context.Background(), preview.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := s.store.GetSiteAccount(context.Background(), account.ID)
	parent, _ := s.store.GetSite(context.Background(), site.ID)
	if after.Enabled || parent.Enabled || after.Label != "existing-user" || after.Status != model.SiteAccountStatusHealthy {
		t.Fatal("duplicate import reactivated or rewrote existing state")
	}
}

func TestBrowserImportPreviewUnverifiedUpdateAndNameConflictDoNotCallProvider(t *testing.T) {
	s, adapter := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
	_, account := browserImportExistingAccount(t, s, model.SitePlatformOpenAICompatible, model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-old-target"})
	browserImportPreviewAPI(t, s, browserImportPreviewInput(browserImportEntryFixture(), "save_disabled", account.ID), http.StatusBadRequest)
	entry := browserImportEntryFixture()
	entry["source_origin"], entry["base_url"], entry["site_name"] = "https://other.example", "https://other.example", "Existing"
	preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "verify", 0), http.StatusOK)
	if preview.CanApply || preview.Summary["conflict"] == 0 || adapter.calls.Load() != 0 {
		t.Fatal("site name conflict was not resolved before upstream calls")
	}
}

func TestBrowserImportPreviewRepairKeepsCurrentHistoryAndRefreshCredential(t *testing.T) {
	s, adapter := browserImportPlanFixture(t, model.SitePlatformSub2API)
	site, account := browserImportExistingAccount(t, s, model.SitePlatformSub2API, model.CredentialTypeAccessToken, provider.Credentials{AccessToken: "synthetic-old-access", RefreshToken: "synthetic-retained-refresh", UserID: 42})
	entry := browserImportEntryFixture()
	entry["platform"], entry["credential_type"] = model.SitePlatformSub2API, model.CredentialTypeAccessToken
	entry["credential"] = map[string]any{"access_token": "synthetic-new-access", "user_id": 42}
	preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "verify", account.ID), http.StatusOK)
	current, _ := s.store.GetSiteAccount(context.Background(), account.ID)
	updatedBalance := 9.0
	current.Balance, current.LastCheckinStatus = &updatedBalance, "success"
	if _, err := s.store.UpdateSiteAccount(context.Background(), account.ID, current); err != nil {
		t.Fatal(err)
	}
	if _, err := s.applyConfigPreview(context.Background(), preview.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := s.store.GetSiteAccount(context.Background(), account.ID)
	parent, _ := s.store.GetSite(context.Background(), site.ID)
	var credential provider.Credentials
	if s.siteControl.cipher.Open(after.CredentialCiphertext, &credential) != nil || credential.AccessToken != "synthetic-new-access" || credential.RefreshToken != "synthetic-retained-refresh" || adapter.calls.Load() != 2 {
		t.Fatal("repair did not freeze prepared and retained credentials")
	}
	if after.Enabled || parent.Enabled || after.AutoCheckin || after.AutoRefresh || after.Balance == nil || *after.Balance != 9 || after.LastCheckinStatus != "success" || after.Status != model.SiteAccountStatusUnknown {
		t.Fatal("repair overwrote current observations or switches")
	}
}

func TestBrowserImportPreviewRejectsDifferentVerifiedIdentity(t *testing.T) {
	s, adapter := browserImportPlanFixture(t, model.SitePlatformNewAPIFamily)
	_, account := browserImportExistingAccount(t, s, model.SitePlatformNewAPIFamily, model.CredentialTypeAccessToken, provider.Credentials{AccessToken: "synthetic-old-access", UserID: 42})
	adapter.userID = 43
	entry := browserImportEntryFixture()
	entry["platform"], entry["credential_type"] = model.SitePlatformNewAPIFamily, model.CredentialTypeAccessToken
	entry["credential"] = map[string]any{"access_token": "synthetic-other-access", "user_id": 43}
	browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "verify", account.ID), http.StatusBadRequest)
	after, _ := s.store.GetSiteAccount(context.Background(), account.ID)
	if after.CredentialCiphertext != account.CredentialCiphertext || after.Status != account.Status {
		t.Fatal("different identity modified the selected account")
	}
}

func TestBrowserImportPreviewWAFAndExpiredPreserveExistingAccount(t *testing.T) {
	for _, code := range []string{provider.CodeBrowserRequired, provider.CodeExpired} {
		t.Run(code, func(t *testing.T) {
			s, adapter := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
			_, account := browserImportExistingAccount(t, s, model.SitePlatformOpenAICompatible, model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-old-target"})
			adapter.failure = &provider.Error{Code: code, Message: "synthetic-provider-echo-secret"}
			_, response := browserImportPreviewAPI(t, s, browserImportPreviewInput(browserImportEntryFixture(), "verify", account.ID), http.StatusBadRequest)
			raw, _ := json.Marshal(response)
			if strings.Contains(string(raw), "synthetic-provider-echo-secret") || !strings.Contains(string(raw), code) {
				t.Fatal("provider error was not safely classified")
			}
			after, _ := s.store.GetSiteAccount(context.Background(), account.ID)
			if after.Status != account.Status || after.LastError != account.LastError || after.CredentialCiphertext != account.CredentialCiphertext {
				t.Fatal("browser verification failure rewrote account state")
			}
		})
	}
}

func TestBrowserImportPreviewCancelledVerificationKeepsNoPreview(t *testing.T) {
	s, adapter := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
	started := make(chan struct{})
	adapter.onCall = func(ctx context.Context, _ provider.AccountRequest) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	g := browserImportTestEngine(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	raw, _ := json.Marshal(browserImportPreviewInput(browserImportEntryFixture(), "verify", 0))
	r := httptest.NewRequest(http.MethodPost, "/admin/browser-import/preview", bytes.NewReader(raw)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer browser-import-admin")
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { g.ServeHTTP(w, r); close(done) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("verification did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("verification did not stop")
	}
	if len(s.configPreviews.records) != 0 || w.Code != http.StatusBadRequest {
		t.Fatal("cancelled verification created a preview")
	}
}

func TestBrowserImportPreviewRollbackAndStale(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "stale"}[stale], func(t *testing.T) {
			s, _ := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
			preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(browserImportEntryFixture(), "save_disabled", 0), http.StatusOK)
			if stale {
				_, err := s.store.CreateSite(context.Background(), &model.Site{Name: "Changed", BaseURL: "https://source.example", Platform: model.SitePlatformOpenAICompatible, Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				store := s.store.(*sqlstore.SQLStore)
				if _, err := store.ExecContext(context.Background(), "CREATE TRIGGER reject_browser_account BEFORE INSERT ON site_accounts BEGIN SELECT RAISE(ABORT, 'synthetic-private-database-detail'); END"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.applyConfigPreview(context.Background(), preview.ID); err == nil || strings.Contains(err.Error(), "synthetic-private-database-detail") {
				t.Fatal("failed application was successful or returned SQL details")
			}
			accounts, _ := s.store.ListSiteAccounts(context.Background(), 0, false)
			sites, _ := s.store.ListSites(context.Background(), model.SiteListFilter{})
			if len(accounts) != 0 || !stale && len(sites) != 0 {
				t.Fatal("failed application left account or orphan site")
			}
			view, _ := s.getConfigPreview(preview.ID)
			want := "failed"
			if stale {
				want = "stale"
			}
			if view.Status != want || view.CanApply {
				t.Fatal("failed application did not keep its terminal state")
			}
		})
	}
}

func TestBrowserImportPreviewCommitUnknownDoesNotRepeatRealWrites(t *testing.T) {
	s, _ := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
	preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(browserImportEntryFixture(), "save_disabled", 0), http.StatusOK)
	apply := s.configPreviews.records[preview.ID].apply
	s.configPreviews.records[preview.ID].apply = func(ctx context.Context) (*configPreviewResult, error) {
		if _, err := apply(ctx); err != nil {
			return nil, err
		}
		return nil, storage.ErrConfigurationCommitUnknown
	}
	for range 2 {
		if _, err := s.applyConfigPreview(context.Background(), preview.ID); err == nil {
			t.Fatal("unknown commit was treated as confirmed")
		}
	}
	accounts, _ := s.store.ListSiteAccounts(context.Background(), 0, false)
	view, _ := s.getConfigPreview(preview.ID)
	if len(accounts) != 1 || view.Status != "unknown" || view.CanApply {
		t.Fatal("unknown commit was retried or lost")
	}
}

type browserImportReadOnlyAdapter struct {
	*browserImportTestAdapter
	refreshCalls atomic.Int64
	keyCalls     atomic.Int64
}

func (a *browserImportReadOnlyAdapter) RefreshCredentials(context.Context, provider.AccountRequest) (provider.Credentials, error) {
	a.refreshCalls.Add(1)
	return provider.Credentials{}, errors.New("unexpected token rotation")
}
func (a *browserImportReadOnlyAdapter) ListRoutingKeys(context.Context, provider.AccountRequest) ([]provider.RoutingKeySnapshot, error) {
	a.keyCalls.Add(1)
	return nil, errors.New("unexpected key reveal")
}

func TestBrowserImportPreviewVerificationOmitsRefreshAndKeyDiscovery(t *testing.T) {
	s, adapter := browserImportPlanFixture(t, model.SitePlatformSub2API)
	readOnly := &browserImportReadOnlyAdapter{browserImportTestAdapter: adapter}
	s.siteControl.registry = provider.NewRegistry(readOnly)
	adapter.onCall = func(_ context.Context, request provider.AccountRequest) error {
		if request.Credentials.RefreshToken != "" || request.Credentials.APIKey != "" || request.Credentials.Password != "" {
			return errors.New("verification received non-read-only credential fields")
		}
		return nil
	}
	entry := browserImportEntryFixture()
	entry["platform"], entry["credential_type"] = model.SitePlatformSub2API, model.CredentialTypeAccessToken
	entry["credential"] = map[string]any{"access_token": "synthetic-read-only-access", "refresh_token": "synthetic-read-only-refresh", "expires_at": time.Now().Add(-time.Minute).UnixMilli()}
	preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "verify", 0), http.StatusOK)
	if _, err := s.applyConfigPreview(context.Background(), preview.ID); err != nil {
		t.Fatal(err)
	}
	accounts, _ := s.store.ListSiteAccounts(context.Background(), 0, false)
	var credential provider.Credentials
	if len(accounts) != 1 || s.siteControl.cipher.Open(accounts[0].CredentialCiphertext, &credential) != nil || credential.RefreshToken != "synthetic-read-only-refresh" || readOnly.refreshCalls.Load() != 0 || readOnly.keyCalls.Load() != 0 {
		t.Fatal("read-only verification rotated tokens, revealed keys, or lost the retained refresh credential")
	}
}

func TestBrowserImportPreviewIdentityEvidenceDoesNotTrustInputHint(t *testing.T) {
	for _, actualID := range []int64{0, 42} {
		t.Run(map[int64]string{0: "unknown", 42: "actual"}[actualID], func(t *testing.T) {
			s, adapter := browserImportPlanFixture(t, model.SitePlatformNewAPIFamily)
			adapter.userID = actualID
			entry := browserImportEntryFixture()
			entry["platform"], entry["credential_type"] = model.SitePlatformNewAPIFamily, model.CredentialTypeAccessToken
			entry["credential"] = map[string]any{"access_token": "synthetic-hinted-access", "user_id": 111}
			preview, data := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "verify", 0), http.StatusOK)
			verification := data["browser_import"].(map[string]any)["verification"].(map[string]any)
			if verification["identity_known"] != (actualID > 0) || actualID == 0 && verification["user_id"] != nil || actualID > 0 && verification["user_id"] != float64(actualID) {
				t.Fatal("input hint was presented as verified identity")
			}
			if !preview.CanApply {
				t.Fatal("new verified account has no apply path")
			}
		})
	}
}

func TestBrowserImportPreviewVerifiedExistingIdentityRequiresExplicitTarget(t *testing.T) {
	s, _ := browserImportPlanFixture(t, model.SitePlatformNewAPIFamily)
	browserImportExistingAccount(t, s, model.SitePlatformNewAPIFamily, model.CredentialTypeAccessToken, provider.Credentials{AccessToken: "synthetic-old-profile", UserID: 42})
	entry := browserImportEntryFixture()
	entry["platform"], entry["credential_type"] = model.SitePlatformNewAPIFamily, model.CredentialTypeAccessToken
	entry["credential"] = map[string]any{"access_token": "synthetic-new-profile"}
	preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "verify", 0), http.StatusOK)
	if preview.CanApply || preview.Summary["conflict"] == 0 {
		t.Fatal("verified identity silently created a duplicate or selected an account")
	}
	accounts, _ := s.store.ListSiteAccounts(context.Background(), 0, false)
	if len(accounts) != 1 {
		t.Fatal("verification created a duplicate account")
	}
}

func browserImportContextResponse(t *testing.T, s *Server, ctx context.Context, input map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	engine := browserImportTestEngine(t, s)
	raw, _ := json.Marshal(input)
	r := httptest.NewRequest(http.MethodPost, "/admin/browser-import/preview", bytes.NewReader(raw)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer browser-import-admin")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, r)
	return w
}

func TestBrowserImportPreviewRespectsSiteGateAndAccountLease(t *testing.T) {
	for _, held := range []string{"gate", "lease"} {
		t.Run(held, func(t *testing.T) {
			s, adapter := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
			site, account := browserImportExistingAccount(t, s, model.SitePlatformOpenAICompatible, model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-gate-old"})
			if held == "gate" {
				release, ok := s.siteControl.acquireSiteGate(context.Background(), site.ID)
				if !ok {
					t.Fatal("fixture could not hold gate")
				}
				defer release()
			} else {
				key := fmt.Sprintf("site:%d:account:%d:browser_import", site.ID, account.ID)
				ok, err := s.store.AcquireSiteTaskLease(context.Background(), key, "fixture-owner", time.Now().UnixMilli(), time.Now().Add(2*time.Minute).UnixMilli())
				if err != nil || !ok {
					t.Fatal("fixture could not hold lease")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
			defer cancel()
			response := browserImportContextResponse(t, s, ctx, browserImportPreviewInput(browserImportEntryFixture(), "verify", account.ID))
			if response.Code != http.StatusBadRequest || adapter.calls.Load() != 0 || len(s.configPreviews.records) != 0 {
				t.Fatal("blocked verification sent credentials or generated an applicable preview")
			}
		})
	}
}

func TestBrowserImportPreviewUnknownBlocksReplacementAndRequiresAcknowledgement(t *testing.T) {
	s, adapter := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
	input := browserImportPreviewInput(browserImportEntryFixture(), "save_disabled", 0)
	first, _ := browserImportPreviewAPI(t, s, input, http.StatusOK)
	second, _ := browserImportPreviewAPI(t, s, input, http.StatusOK)
	apply := s.configPreviews.records[first.ID].apply
	s.configPreviews.records[first.ID].apply = func(ctx context.Context) (*configPreviewResult, error) {
		if _, err := apply(ctx); err != nil {
			return nil, err
		}
		return nil, storage.ErrConfigurationCommitUnknown
	}
	if _, err := s.applyConfigPreview(context.Background(), first.ID); err == nil {
		t.Fatal("fixture commit was not unknown")
	}
	input["mode"] = "verify"
	browserImportPreviewAPI(t, s, input, http.StatusBadRequest)
	if _, err := s.applyConfigPreview(context.Background(), second.ID); err == nil || !strings.Contains(err.Error(), "browser_import_pending") {
		t.Fatal("ready preview bypassed the unknown outcome")
	}
	engine := browserImportTestEngine(t, s)
	for _, acknowledgement := range []bool{false, true} {
		path := "/admin/config-previews/" + first.ID
		if acknowledgement {
			path += "?acknowledged=true"
		}
		r := httptest.NewRequest(http.MethodDelete, path, nil)
		r.Header.Set("Authorization", "Bearer browser-import-admin")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		want := http.StatusConflict
		if acknowledgement {
			want = http.StatusOK
		}
		if w.Code != want {
			t.Fatal("unknown outcome could be silently discarded or could not be acknowledged")
		}
	}
	if adapter.calls.Load() != 0 {
		t.Fatal("replacement validation contacted upstream before observing the unknown outcome")
	}
}

func TestBrowserImportPreviewRepairPreservesModelsBindingsAndChannels(t *testing.T) {
	s, _ := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
	_, account := browserImportExistingAccount(t, s, model.SitePlatformOpenAICompatible, model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-projection-old"})
	ctx := context.Background()
	if err := s.store.ReplaceSiteAccountModels(ctx, account.ID, []model.SiteAccountModel{{Model: "historical-model", RouteType: "openai", Source: "prior-refresh", LastSeenAt: 123}}); err != nil {
		t.Fatal(err)
	}
	projection, err := s.store.UpsertSiteProjection(ctx, model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: "existing-key", Name: "Existing channel", BaseURL: "https://source.example", Protocols: []string{"openai"}, Models: []string{"historical-model"}, APIKey: "synthetic-projection-key", Enabled: false})
	if err != nil || projection == nil || projection.Channel == nil {
		t.Fatal("could not prepare an existing projection")
	}
	// Legacy/manual channel state can differ from the stopped account. Import
	// must not invoke the generic account-toggle cascade or rewrite that state.
	store := s.store.(*sqlstore.SQLStore)
	if _, err = store.ExecContext(ctx, "UPDATE channels SET enabled=1 WHERE id=?", projection.Channel.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ExecContext(ctx, "UPDATE site_channel_bindings SET status='active', ownership='manual' WHERE site_account_id=?", account.ID); err != nil {
		t.Fatal(err)
	}
	bindingsBefore, _ := s.store.ListSiteChannelBindings(ctx)
	modelsBefore, _ := s.store.ListSiteAccountModels(ctx, model.SiteModelFilter{SiteAccountID: account.ID})
	preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(browserImportEntryFixture(), "verify", account.ID), http.StatusOK)
	if _, err = s.applyConfigPreview(ctx, preview.ID); err != nil {
		t.Fatal(err)
	}
	channel, _ := s.store.GetConfig(ctx, projection.Channel.ID)
	bindingsAfter, _ := s.store.ListSiteChannelBindings(ctx)
	modelsAfter, _ := s.store.ListSiteAccountModels(ctx, model.SiteModelFilter{SiteAccountID: account.ID})
	before, _ := json.Marshal(struct{ Bindings, Models any }{bindingsBefore, modelsBefore})
	after, _ := json.Marshal(struct{ Bindings, Models any }{bindingsAfter, modelsAfter})
	if !channel.Enabled || !bytes.Equal(before, after) {
		t.Fatal("credential repair cascaded into channels, bindings, or historical models")
	}
}
