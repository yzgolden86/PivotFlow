package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ccLoad/internal/model"
	"ccLoad/internal/site/credential"
	"ccLoad/internal/site/provider"

	"github.com/gin-gonic/gin"
)

type projectionTestAdapter struct{}

func (projectionTestAdapter) ID() string { return model.SitePlatformNewAPIFamily }
func (projectionTestAdapter) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Models: true}
}
func (projectionTestAdapter) Detect(context.Context, string) (provider.DetectionResult, error) {
	return provider.DetectionResult{Matched: true, ProviderID: model.SitePlatformNewAPIFamily}, nil
}
func (projectionTestAdapter) RefreshAccount(context.Context, provider.RefreshAccountRequest) (provider.AccountSnapshot, error) {
	return provider.AccountSnapshot{}, nil
}
func (projectionTestAdapter) ListModels(context.Context, provider.AccountRequest) ([]provider.ModelSnapshot, error) {
	return []provider.ModelSnapshot{{Model: "gpt-5", RouteType: "openai", Source: "test"}}, nil
}
func (projectionTestAdapter) Checkin(context.Context, provider.AccountRequest) (provider.CheckinResult, error) {
	return provider.CheckinResult{Status: provider.CheckinUnsupported}, nil
}
func (projectionTestAdapter) ListAnnouncements(context.Context, provider.AccountRequest) ([]provider.Announcement, error) {
	return nil, nil
}

type managementProjectionTestAdapter struct{ projectionTestAdapter }

func (managementProjectionTestAdapter) ListModels(_ context.Context, req provider.AccountRequest) ([]provider.ModelSnapshot, error) {
	if req.Credentials.AccessToken == "" {
		return nil, &provider.Error{Code: provider.CodeUnsupported, Message: "management credential is required"}
	}
	return []provider.ModelSnapshot{{Model: "gpt-5", RouteType: "openai", Source: "test"}}, nil
}

func (managementProjectionTestAdapter) ListRoutingKeys(context.Context, provider.AccountRequest) ([]provider.RoutingKeySnapshot, error) {
	return []provider.RoutingKeySnapshot{{Key: "sk-projected-management", Enabled: true}}, nil
}

type multiKeyProjectionTestAdapter struct {
	projectionTestAdapter
	keys []provider.RoutingKeySnapshot
}

func (a *multiKeyProjectionTestAdapter) ListRoutingKeys(context.Context, provider.AccountRequest) ([]provider.RoutingKeySnapshot, error) {
	return append([]provider.RoutingKeySnapshot(nil), a.keys...), nil
}

type cookieLoginAdapter struct{}

func (cookieLoginAdapter) ID() string { return model.SitePlatformNewAPIFamily }
func (cookieLoginAdapter) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{ServerCheckin: true, Balance: true, Models: true}
}
func (cookieLoginAdapter) Detect(context.Context, string) (provider.DetectionResult, error) {
	return provider.DetectionResult{Matched: true, ProviderID: model.SitePlatformNewAPIFamily}, nil
}
func (cookieLoginAdapter) Login(context.Context, provider.LoginRequest) (provider.Credentials, error) {
	return provider.Credentials{Cookie: "session=login-cookie", UserID: 42}, nil
}
func (cookieLoginAdapter) RefreshAccount(context.Context, provider.RefreshAccountRequest) (provider.AccountSnapshot, error) {
	return provider.AccountSnapshot{}, nil
}
func (cookieLoginAdapter) ListModels(context.Context, provider.AccountRequest) ([]provider.ModelSnapshot, error) {
	return nil, nil
}
func (cookieLoginAdapter) Checkin(context.Context, provider.AccountRequest) (provider.CheckinResult, error) {
	return provider.CheckinResult{Status: provider.CheckinSuccess}, nil
}
func (cookieLoginAdapter) ListAnnouncements(context.Context, provider.AccountRequest) ([]provider.Announcement, error) {
	return nil, nil
}

type anyRouterCredentialAdapter struct{ cookieLoginAdapter }

func (anyRouterCredentialAdapter) ID() string { return model.SitePlatformAnyRouter }
func (anyRouterCredentialAdapter) ResolveManagementCredentials(_ context.Context, req provider.AccountRequest) (provider.Credentials, error) {
	return req.Credentials, nil
}

type credentialVerifyAdapter struct{}

func (credentialVerifyAdapter) ID() string { return model.SitePlatformNewAPIFamily }
func (credentialVerifyAdapter) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{Balance: true, Models: true}
}
func (credentialVerifyAdapter) Detect(context.Context, string) (provider.DetectionResult, error) {
	return provider.DetectionResult{Matched: true, ProviderID: model.SitePlatformNewAPIFamily}, nil
}
func (credentialVerifyAdapter) RefreshAccount(_ context.Context, req provider.RefreshAccountRequest) (provider.AccountSnapshot, error) {
	if req.Credentials.AccessToken == "bad-token" {
		return provider.AccountSnapshot{}, &provider.Error{Code: provider.CodeExpired, StatusCode: http.StatusOK, Message: "invalid access token"}
	}
	balance := 12.5
	return provider.AccountSnapshot{Username: "verified-user", Balance: &balance, Currency: "CNY"}, nil
}
func (credentialVerifyAdapter) ListModels(context.Context, provider.AccountRequest) ([]provider.ModelSnapshot, error) {
	return []provider.ModelSnapshot{{Model: "gpt-5", RouteType: "openai", Source: "test"}}, nil
}
func (credentialVerifyAdapter) Checkin(context.Context, provider.AccountRequest) (provider.CheckinResult, error) {
	return provider.CheckinResult{Status: provider.CheckinUnsupported}, nil
}
func (credentialVerifyAdapter) ListAnnouncements(context.Context, provider.AccountRequest) ([]provider.Announcement, error) {
	return nil, nil
}

type rejectingCredentialAdapter struct{ credentialVerifyAdapter }

func (rejectingCredentialAdapter) ResolveManagementCredentials(context.Context, provider.AccountRequest) (provider.Credentials, error) {
	return provider.Credentials{}, &provider.Error{Code: provider.CodeExpired, StatusCode: http.StatusOK, Message: "Unauthorized, invalid access token"}
}

type balanceCheckinAdapter struct{ refreshCalls int }

func (a *balanceCheckinAdapter) ID() string { return model.SitePlatformNewAPIFamily }
func (a *balanceCheckinAdapter) Capabilities() provider.ProviderCapabilities {
	return provider.ProviderCapabilities{ServerCheckin: true, Balance: true, CredentialTypes: []string{model.CredentialTypeAPIKey}}
}
func (a *balanceCheckinAdapter) Detect(context.Context, string) (provider.DetectionResult, error) {
	return provider.DetectionResult{Matched: true, ProviderID: model.SitePlatformNewAPIFamily}, nil
}
func (a *balanceCheckinAdapter) RefreshAccount(context.Context, provider.RefreshAccountRequest) (provider.AccountSnapshot, error) {
	a.refreshCalls++
	balance := 10.0
	if a.refreshCalls > 1 {
		balance = 12.5
	}
	return provider.AccountSnapshot{Username: "balance-user", Balance: &balance, Currency: "CNY", Status: model.SiteAccountStatusHealthy}, nil
}
func (a *balanceCheckinAdapter) ListModels(context.Context, provider.AccountRequest) ([]provider.ModelSnapshot, error) {
	return nil, nil
}
func (a *balanceCheckinAdapter) Checkin(context.Context, provider.AccountRequest) (provider.CheckinResult, error) {
	return provider.CheckinResult{Status: provider.CheckinSuccess, Message: "签到成功"}, nil
}
func (a *balanceCheckinAdapter) ListAnnouncements(context.Context, provider.AccountRequest) ([]provider.Announcement, error) {
	return nil, nil
}

func TestCreateAnyRouterCookieAccountStoresEncryptedUserContext(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "provider-account-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	srv.siteControl.registry = provider.NewRegistry(anyRouterCredentialAdapter{})
	site, err := srv.store.CreateSite(context.Background(), &model.Site{Name: "AnyRouter", Platform: model.SitePlatformAnyRouter, BaseURL: "https://api.example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := srv.siteControl.createAccount(context.Background(), site.ID, accountCreateRequest{
		Label: "main", CredentialType: model.CredentialTypeCookie,
		Credential: provider.Credentials{Cookie: "session=secret", UserID: 42},
	})
	if err != nil || account.CredentialType != model.CredentialTypeCookie {
		t.Fatalf("account=%+v err=%v", account, err)
	}
	decrypted, err := srv.siteControl.credentials(account)
	if err != nil || decrypted.Cookie != "session=secret" || decrypted.UserID != 42 {
		t.Fatalf("decrypted=%+v err=%v", decrypted, err)
	}
}

func TestCreateSub2APILegacyCookieInputStoresValidatedAccessToken(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer sub2-jwt" {
			t.Fatalf("authorization=%q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/v1/auth/me":
			_, _ = w.Write([]byte(`{"code":"0","message":"ok","data":{"id":17,"username":"sub-user","balance":"8.5"}}`))
		case "/api/v1/keys":
			_, _ = w.Write([]byte(`{"code":"0","message":"ok","data":{"records":[{"apiKey":"sk-sub2","enabled":true}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "sub2-cookie-normalization-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	srv.siteControl.registry = provider.NewRegistry(provider.NewSub2API(provider.ClientFactory{AllowPrivate: true}))
	ctx := context.Background()
	site, err := srv.store.CreateSite(ctx, &model.Site{Name: "Sub2", Platform: model.SitePlatformSub2API, BaseURL: upstream.URL, Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := srv.siteControl.createAccount(ctx, site.ID, accountCreateRequest{
		Label: "main", CredentialType: model.CredentialTypeCookie,
		Credential: provider.Credentials{Cookie: "Bearer sub2-jwt"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.CredentialType != model.CredentialTypeAccessToken || account.AutoCheckin {
		t.Fatalf("account=%+v", account)
	}
	stored, err := srv.siteControl.credentials(account)
	if err != nil || stored.AccessToken != "sub2-jwt" || stored.Cookie != "" || stored.UserID != 17 || stored.APIKey != "sk-sub2" {
		t.Fatalf("credentials=%+v err=%v", stored, err)
	}
}

func TestCheckinPersistsBalanceIncrease(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "checkin-balance-test")
	if err != nil {
		t.Fatal(err)
	}
	adapter := &balanceCheckinAdapter{}
	srv.siteControl.cipher = cipher
	srv.siteControl.registry = provider.NewRegistry(adapter)
	ctx := context.Background()
	site, err := srv.store.CreateSite(ctx, &model.Site{Name: "Balance", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://balance.example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal(provider.Credentials{APIKey: "sk-balance"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := srv.store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "main", CredentialType: model.CredentialTypeAPIKey, CredentialCiphertext: sealed, CredentialKeyVersion: cipher.Version(), Enabled: true, Status: model.SiteAccountStatusHealthy, BalanceCurrency: "CNY", LastRefreshStatus: "unknown", LastCheckinStatus: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	task := &model.SiteTask{ID: "st_checkin_balance", Kind: "checkin", Status: model.SiteTaskStatusRunning, SiteID: site.ID, SiteAccountID: account.ID, ProgressJSON: "{}"}
	if err := srv.store.CreateSiteTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	srv.siteControl.checkinWithTrigger(ctx, task, account.ID, "manual", "manual:test")

	attempts, err := srv.store.ListCheckinAttempts(ctx, account.ID, 10)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts=%+v err=%v", attempts, err)
	}
	attempt := attempts[0]
	if attempt.Status != provider.CheckinSuccess || attempt.BalanceBefore == nil || *attempt.BalanceBefore != 10 || attempt.BalanceAfter == nil || *attempt.BalanceAfter != 12.5 || attempt.BalanceDelta == nil || *attempt.BalanceDelta != 2.5 || attempt.RewardText != "+2.50 CNY" {
		t.Fatalf("attempt=%+v", attempt)
	}
	updated, err := srv.store.GetSiteAccount(ctx, account.ID)
	if err != nil || updated.Balance == nil || *updated.Balance != 12.5 || updated.LastCheckinStatus != provider.CheckinSuccess {
		t.Fatalf("account=%+v err=%v", updated, err)
	}
	storedTask, err := srv.store.GetSiteTask(ctx, task.ID)
	if err != nil || storedTask.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("task=%+v err=%v", storedTask, err)
	}
}

func TestHandleSitesCreatesFirstAPIKeyAccountInSameRequest(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "site-create-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	request := newJSONRequest(t, http.MethodPost, "/admin/sites", map[string]any{
		"name": "Primary", "base_url": "https://primary.example.com", "platform": model.SitePlatformNewAPIFamily, "timezone": "Asia/Shanghai",
		"account": map[string]any{
			"label": "main", "credential_type": model.CredentialTypeAPIKey,
			"credential": map[string]any{"api_key": "sk-first-account"},
			"enabled":    true, "auto_checkin": true, "auto_refresh": true,
		},
	})
	c, recorder := newTestContext(t, request)
	srv.siteControl.handleSites(c)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	sites, err := srv.store.ListSites(context.Background(), model.SiteListFilter{})
	if err != nil || len(sites) != 1 {
		t.Fatalf("sites=%+v err=%v", sites, err)
	}
	accounts, err := srv.store.ListSiteAccounts(context.Background(), sites[0].ID, false)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%+v err=%v", accounts, err)
	}
	if accounts[0].Label != "main" || accounts[0].CredentialType != model.CredentialTypeAPIKey {
		t.Fatalf("unexpected account: %+v", accounts[0])
	}
	if accounts[0].AutoCheckin || accounts[0].AutoRefresh || strings.Contains(accounts[0].CredentialCiphertext, "sk-first-account") {
		t.Fatalf("API key account was not normalized and encrypted: %+v", accounts[0])
	}
}

func TestModelRefreshAutomaticallyCreatesProjectedChannel(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "projection-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	srv.siteControl.registry = provider.NewRegistry(projectionTestAdapter{})
	ctx := context.Background()
	site, err := srv.store.CreateSite(ctx, &model.Site{Name: "Primary", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://primary.example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := srv.siteControl.createAccount(ctx, site.ID, accountCreateRequest{Label: "main", CredentialType: model.CredentialTypeAPIKey, Credential: provider.Credentials{APIKey: "sk-projected"}})
	if err != nil {
		t.Fatal(err)
	}
	task := &model.SiteTask{ID: "st_projection", Kind: "model_refresh", Status: model.SiteTaskStatusRunning}
	srv.siteControl.refreshAccount(ctx, task, account.ID, true)
	if task.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("task=%+v", task)
	}
	bindings, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].SiteAccountID != account.ID || bindings[0].ChannelID == 0 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	channel, err := srv.store.GetConfig(ctx, bindings[0].ChannelID)
	if err != nil || channel.Name != "Primary / main" || len(channel.ModelEntries) != 1 || channel.ModelEntries[0].Model != "gpt-5" {
		t.Fatalf("channel=%+v err=%v", channel, err)
	}
	keys, err := srv.store.GetAPIKeys(ctx, channel.ID)
	if err != nil || len(keys) != 1 || keys[0].APIKey != "sk-projected" {
		t.Fatalf("keys=%+v err=%v", keys, err)
	}
	request := newJSONRequest(t, http.MethodGet, "/admin/site-channel-bindings", nil)
	c, recorder := newTestContext(t, request)
	srv.siteControl.handleSiteChannelBindings(c)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), fmt.Sprintf(`"site_account_id":%d`, account.ID)) || !strings.Contains(recorder.Body.String(), fmt.Sprintf(`"channel_id":%d`, channel.ID)) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestModelRefreshDoesNotRepeatManagementModelProbeWithRoutingKey(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "projection-management-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	srv.siteControl.registry = provider.NewRegistry(managementProjectionTestAdapter{})
	ctx := context.Background()
	site, err := srv.store.CreateSite(ctx, &model.Site{Name: "Management", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://management.example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := srv.siteControl.createAccount(ctx, site.ID, accountCreateRequest{Label: "main", CredentialType: model.CredentialTypeAccessToken, Credential: provider.Credentials{AccessToken: "system-token", UserID: 42}})
	if err != nil {
		t.Fatal(err)
	}
	task := &model.SiteTask{ID: "st_management_projection", Kind: "model_refresh", Status: model.SiteTaskStatusRunning}
	srv.siteControl.refreshAccount(ctx, task, account.ID, true)
	if task.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("task=%+v", task)
	}
	bindings, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 1 || bindings[0].ChannelID == 0 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
}

func TestModelRefreshProjectsEachRoutingKeyAndDeactivatesRemovedKeys(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "multi-key-projection-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	adapter := &multiKeyProjectionTestAdapter{keys: []provider.RoutingKeySnapshot{
		{ID: "alpha", Name: "Alpha key", Group: "team-a", Models: []string{"gpt-5"}, Key: "sk-alpha", Enabled: true},
		{ID: "beta", Name: "Beta key", Group: "team-b", Models: []string{"claude-3"}, Key: "sk-beta", Enabled: true},
	}}
	srv.siteControl.registry = provider.NewRegistry(adapter)
	ctx := context.Background()
	site, err := srv.store.CreateSite(ctx, &model.Site{Name: "Multi", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://multi.example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := srv.siteControl.createAccount(ctx, site.ID, accountCreateRequest{Label: "admin", CredentialType: model.CredentialTypeAccessToken, Credential: provider.Credentials{AccessToken: "system-token", UserID: 7}})
	if err != nil {
		t.Fatal(err)
	}
	refresh := func() *model.SiteTask {
		task := &model.SiteTask{ID: newSiteTaskID(), Kind: "model_refresh", Status: model.SiteTaskStatusRunning}
		srv.siteControl.refreshAccount(ctx, task, account.ID, true)
		return task
	}
	if task := refresh(); task.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("first refresh task=%+v", task)
	}
	bindings, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("bindings=%+v err=%v, want two key projections", bindings, err)
	}
	seenKeys := map[string]bool{}
	expectedModels := map[string]string{"sk-alpha": "gpt-5", "sk-beta": "claude-3"}
	for _, binding := range bindings {
		if binding.ChannelID == 0 || !strings.HasPrefix(binding.ProjectionKey, "key:") {
			t.Fatalf("unexpected binding=%+v", binding)
		}
		channel, getErr := srv.store.GetConfig(ctx, binding.ChannelID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		keys, keyErr := srv.store.GetAPIKeys(ctx, channel.ID)
		if keyErr != nil || len(keys) != 1 {
			t.Fatalf("channel=%+v keys=%+v err=%v", channel, keys, keyErr)
		}
		seenKeys[keys[0].APIKey] = true
		if len(channel.ModelEntries) != 1 || channel.ModelEntries[0].Model != expectedModels[keys[0].APIKey] {
			t.Fatalf("channel %q models=%+v", channel.Name, channel.ModelEntries)
		}
	}
	if !seenKeys["sk-alpha"] || !seenKeys["sk-beta"] {
		t.Fatalf("projected keys=%v", seenKeys)
	}
	if task := refresh(); task.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("second refresh task=%+v", task)
	}
	bindingsAgain, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindingsAgain) != 2 {
		t.Fatalf("second sync bindings=%+v err=%v", bindingsAgain, err)
	}

	adapter.keys = adapter.keys[:1]
	if task := refresh(); task.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("removal refresh task=%+v", task)
	}
	bindingsAfterRemoval, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindingsAfterRemoval) != 2 {
		t.Fatalf("after removal bindings=%+v err=%v", bindingsAfterRemoval, err)
	}
	for _, binding := range bindingsAfterRemoval {
		if binding.ProjectionKey != "key:beta" {
			continue
		}
		if binding.Status != "inactive" || binding.ChannelID == 0 {
			t.Fatalf("removed key binding=%+v", binding)
		}
		channel, getErr := srv.store.GetConfig(ctx, binding.ChannelID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if channel.Enabled {
			t.Fatalf("removed key channel remains enabled: %+v", channel)
		}
	}
}

func TestPasswordLoginWithCookieSessionCreatesCookieAccount(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "cookie-login-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	srv.siteControl.registry = provider.NewRegistry(cookieLoginAdapter{})
	ctx := context.Background()
	site, err := srv.store.CreateSite(ctx, &model.Site{Name: "Cookie Site", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://cookie.example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := srv.siteControl.createAccount(ctx, site.ID, accountCreateRequest{Label: "login", CredentialType: model.CredentialTypeUsernamePassword, Credential: provider.Credentials{Username: "u", Password: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	if account.CredentialType != model.CredentialTypeCookie {
		t.Fatalf("credential type=%q, want cookie", account.CredentialType)
	}
	credentials, err := srv.siteControl.credentials(account)
	if err != nil || credentials.Cookie != "session=login-cookie" || credentials.UserID != 42 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
}

func TestHandleSiteAccountPatchReplacesCredentialAndClearsExpiredState(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "credential-patch-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	srv.siteControl.registry = provider.NewRegistry(anyRouterCredentialAdapter{})
	ctx := context.Background()
	site, err := srv.store.CreateSite(ctx, &model.Site{Name: "AnyRouter Patch", Platform: model.SitePlatformAnyRouter, BaseURL: "https://patch.example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal(provider.Credentials{Cookie: "session=old", UserID: 1})
	if err != nil {
		t.Fatal(err)
	}
	account, err := srv.store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "main", CredentialType: model.CredentialTypeCookie, CredentialCiphertext: sealed, CredentialKeyVersion: cipher.Version(), Enabled: true, AutoCheckin: true, AutoRefresh: true, Status: model.SiteAccountStatusExpired, LastRefreshStatus: "failed", LastCheckinStatus: "failed", LastError: provider.CodeExpired, ConsecutiveFailures: 3})
	if err != nil {
		t.Fatal(err)
	}
	request := newJSONRequest(t, http.MethodPatch, "/admin/site-accounts/1", map[string]any{
		"credential_type": model.CredentialTypeAccessToken,
		"credential":      map[string]any{"access_token": "system-token-new", "user_id": 42},
	})
	c, recorder := newTestContext(t, request)
	c.Params = append(c.Params, gin.Param{Key: "id", Value: fmt.Sprint(account.ID)})
	srv.siteControl.handleSiteAccountByID(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	updated, err := srv.store.GetSiteAccount(ctx, account.ID)
	if err != nil || updated.Status != model.SiteAccountStatusUnknown || updated.LastError != "" || updated.ConsecutiveFailures != 0 || updated.CredentialType != model.CredentialTypeAccessToken {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
	credentials, err := srv.siteControl.credentials(updated)
	if err != nil || credentials.AccessToken != "system-token-new" || credentials.UserID != 42 {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
}

func TestHandleSiteAccountCredentialVerifyDoesNotPersistAndReturnsDetails(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "credential-verify-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	srv.siteControl.registry = provider.NewRegistry(credentialVerifyAdapter{})
	ctx := context.Background()
	site, err := srv.store.CreateSite(ctx, &model.Site{Name: "Verify Site", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://verify.example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := srv.siteControl.createAccount(ctx, site.ID, accountCreateRequest{Label: "main", CredentialType: model.CredentialTypeAPIKey, Credential: provider.Credentials{APIKey: "sk-original"}})
	if err != nil {
		t.Fatal(err)
	}
	originalCiphertext := account.CredentialCiphertext

	request := newJSONRequest(t, http.MethodPost, "/admin/site-accounts/1/credential/verify", map[string]any{
		"credential_type": model.CredentialTypeAccessToken,
		"credential":      map[string]any{"access_token": "new-system-token", "user_id": 42},
	})
	c, recorder := newTestContext(t, request)
	c.Params = append(c.Params, gin.Param{Key: "id", Value: fmt.Sprint(account.ID)})
	srv.siteControl.handleSiteAccountCredentialVerify(c)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"username":"verified-user"`) || !strings.Contains(recorder.Body.String(), `"balance":12.5`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	after, err := srv.store.GetSiteAccount(ctx, account.ID)
	if err != nil || after.CredentialType != model.CredentialTypeAPIKey || after.CredentialCiphertext != originalCiphertext {
		t.Fatalf("verification mutated account: %+v err=%v", after, err)
	}

	failing := newJSONRequest(t, http.MethodPost, "/admin/site-accounts/1/credential/verify", map[string]any{
		"credential_type": model.CredentialTypeAccessToken,
		"credential":      map[string]any{"access_token": "bad-token", "user_id": 42},
	})
	c, recorder = newTestContext(t, failing)
	c.Params = append(c.Params, gin.Param{Key: "id", Value: fmt.Sprint(account.ID)})
	srv.siteControl.handleSiteAccountCredentialVerify(c)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"error":"expired"`) || !strings.Contains(recorder.Body.String(), `"message":"invalid access token"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleSitesKeepsProviderErrorWhenFirstAccountFails(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "site-create-error-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	srv.siteControl.registry = provider.NewRegistry(rejectingCredentialAdapter{})
	request := newJSONRequest(t, http.MethodPost, "/admin/sites", map[string]any{
		"name": "Rejected Site", "base_url": "https://rejected.example.com", "platform": model.SitePlatformNewAPIFamily, "timezone": "Asia/Shanghai",
		"account": map[string]any{
			"label": "main", "credential_type": model.CredentialTypeAccessToken,
			"credential": map[string]any{"access_token": "bad-token", "user_id": 42},
		},
	})
	c, recorder := newTestContext(t, request)
	srv.siteControl.handleSites(c)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), `"error":"expired"`) || !strings.Contains(recorder.Body.String(), `"message":"Unauthorized, invalid access token"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	sites, err := srv.store.ListSites(context.Background(), model.SiteListFilter{})
	if err != nil || len(sites) != 0 {
		t.Fatalf("failed site creation was not rolled back: sites=%+v err=%v", sites, err)
	}
}
