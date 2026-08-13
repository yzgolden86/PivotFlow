package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"

	"github.com/gin-gonic/gin"
)

func TestHandleSiteAccountModelProbeUsesTransientConfigWithoutLogsOrCooldowns(t *testing.T) {
	var gotModelAuthorization string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/token/":
			if r.Header.Get("Authorization") != "Bearer management-token" || r.Header.Get("New-API-User") != "9" {
				t.Fatalf("unexpected management headers: %#v", r.Header)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"name":"probe-key","key":"sk-site-secret","status":1}]}}`))
			return
		case "/v1/chat/completions":
			gotModelAuthorization = r.Header.Get("Authorization")
		default:
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl-site","model":"gpt-site","choices":[{"message":{"role":"assistant","content":"site probe ok"}}],"usage":{"prompt_tokens":4,"completion_tokens":3}}`))
	}))
	defer upstream.Close()

	srv := newInMemoryServer(t)
	srv.client = upstream.Client()
	srv.siteControl.registry = provider.NewRegistry(provider.NewNewAPI(provider.ClientFactory{AllowPrivate: true}))
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "site-probe-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	sealed, err := cipher.Seal(provider.Credentials{AccessToken: "management-token", UserID: 9})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	site, err := srv.store.CreateSite(ctx, &model.Site{
		Name: "Site probe", Platform: model.SitePlatformNewAPIFamily, BaseURL: upstream.URL,
		Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]", LastProbeStatus: "success",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := srv.store.CreateSiteAccount(ctx, &model.SiteAccount{
		SiteID: site.ID, Label: "primary", CredentialType: model.CredentialTypeAPIKey,
		CredentialCiphertext: sealed, CredentialKeyVersion: cipher.Version(), Enabled: true,
		Status: model.SiteAccountStatusHealthy, LastRefreshStatus: "success", LastCheckinStatus: "unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.ReplaceSiteAccountModels(ctx, account.ID, []model.SiteAccountModel{{
		SiteAccountID: account.ID, Model: "gpt-site", RouteType: "openai_chat", Source: "models_endpoint", LastSeenAt: time.Now().UnixMilli(),
	}}); err != nil {
		t.Fatal(err)
	}

	request := newJSONRequest(t, http.MethodPost, "/admin/site-accounts/1/model-probe", map[string]any{
		"model": "gpt-site", "content": "ping", "stream": false, "client_protocol": "openai",
	})
	c, recorder := newTestContext(t, request)
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	srv.HandleSiteAccountModelProbe(c)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := mustParseAPIResponse[map[string]any](t, recorder.Body.Bytes())
	if response.Data["status"] != "pass" || response.Data["source_type"] != "site_account" {
		t.Fatalf("unexpected probe result: %+v", response.Data)
	}
	if response.Data["response_text"] != "site probe ok" {
		t.Fatalf("response_text=%v", response.Data["response_text"])
	}
	if gotModelAuthorization != "Bearer sk-site-secret" {
		t.Fatalf("authorization=%q", gotModelAuthorization)
	}
	if strings.Contains(recorder.Body.String(), "sk-site-secret") || strings.Contains(recorder.Body.String(), "raw_response") {
		t.Fatalf("probe response leaked a credential or raw response: %s", recorder.Body.String())
	}
	configs, err := srv.store.ListConfigs(ctx)
	if err != nil || len(configs) != 0 {
		t.Fatalf("transient probe persisted channels: configs=%d err=%v", len(configs), err)
	}
	logs, err := srv.store.CountLogs(ctx, time.Unix(0, 0), nil)
	if err != nil || logs != 0 {
		t.Fatalf("transient probe wrote logs: count=%d err=%v", logs, err)
	}
	cooldowns, err := srv.store.GetAllChannelCooldowns(ctx)
	if err != nil || len(cooldowns) != 0 {
		t.Fatalf("transient probe changed cooldowns: %+v err=%v", cooldowns, err)
	}
	updated, err := srv.store.GetSiteAccount(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := srv.siteControl.credentials(updated)
	if err != nil || persisted.APIKey != "sk-site-secret" || persisted.AccessToken != "management-token" {
		t.Fatalf("persisted credentials=%+v err=%v", persisted, err)
	}
}

func TestHandleSiteAccountModelProbeRejectsModelOutsideInventory(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "site-probe-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	sealed, _ := cipher.Seal(provider.Credentials{APIKey: "sk-site-secret"})
	site, _ := srv.store.CreateSite(context.Background(), &model.Site{Name: "Site", BaseURL: "https://example.invalid", Platform: model.SitePlatformNewAPIFamily, Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	account, _ := srv.store.CreateSiteAccount(context.Background(), &model.SiteAccount{SiteID: site.ID, Label: "primary", CredentialType: model.CredentialTypeAPIKey, CredentialCiphertext: sealed, Enabled: true, Status: model.SiteAccountStatusHealthy})

	c, recorder := newTestContext(t, newJSONRequest(t, http.MethodPost, "/admin/site-accounts/1/model-probe", map[string]any{
		"model": "not-discovered", "client_protocol": "openai",
	}))
	c.Params = gin.Params{{Key: "id", Value: "1"}}
	srv.HandleSiteAccountModelProbe(c)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "model_not_in_account_inventory") {
		t.Fatalf("status=%d body=%s account=%d", recorder.Code, recorder.Body.String(), account.ID)
	}
}
