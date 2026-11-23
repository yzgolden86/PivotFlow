package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func browserImportTestEngine(t *testing.T, s *Server) *gin.Engine {
	t.Helper()
	if s.authService == nil {
		s.authService = newTestAuthService(t)
	}
	injectAdminToken(s.authService, "browser-import-admin", time.Now().Add(time.Hour))
	g := gin.New()
	s.SetupRoutes(g)
	return g
}

func browserImportAPI(t *testing.T, g *gin.Engine, path string, value any, status int) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer browser-import-admin")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("POST %s: status=%d, want %d", path, w.Code, status)
	}
	var envelope map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope
}

func browserImportEntryFixture() map[string]any {
	return map[string]any{
		"source_origin": "https://source.example", "base_url": "https://SOURCE.example:443/",
		"site_name": "Source", "platform": "openai-compatible", "account_label": "demo",
		"credential_type": "api_key", "credential": map[string]any{"api_key": "synthetic-browser-primary"},
	}
}

func browserImportDocumentFixture(entries ...map[string]any) map[string]any {
	return map[string]any{"format": "pivotflow-browser-bridge", "version": "1.0", "captured_at": 1795392000000, "entries": entries}
}

func browserInspectionEntries(t *testing.T, response map[string]any) []any {
	t.Helper()
	data, ok := response["data"].(map[string]any)
	if !ok {
		t.Fatal("missing inspection data")
	}
	entries, ok := data["entries"].([]any)
	if !ok {
		t.Fatal("missing inspection entries")
	}
	return entries
}

func TestBrowserImportInspectCanonicalAndReadOnly(t *testing.T) {
	s := backupPreviewServer(t)
	g := browserImportTestEngine(t, s)
	entry := browserImportEntryFixture()
	primary := entry["credential"].(map[string]any)["api_key"].(string)
	response := browserImportAPI(t, g, "/admin/browser-import/inspect", map[string]any{"document": browserImportDocumentFixture(entry)}, http.StatusOK)
	entries := browserInspectionEntries(t, response)
	if len(entries) != 1 {
		t.Fatalf("entries=%d", len(entries))
	}
	item := entries[0].(map[string]any)
	if item["eligible"] != true || item["base_url"] != "https://source.example" || item["account_label"] != "demo" || item["source_origin"] != "https://source.example" {
		t.Fatal("canonical source or eligibility is incorrect")
	}
	sites, err := s.store.ListSites(context.Background(), model.SiteListFilter{})
	if err != nil || len(sites) != 0 {
		t.Fatal("inspection wrote a site")
	}
	raw, _ := json.Marshal(response)
	if strings.Contains(string(raw), primary) {
		t.Fatal("inspection returned a credential")
	}
}

func TestBrowserImportInspectAllAPIHubAccountSubset(t *testing.T) {
	for _, version := range []string{"2.0", "3.0", "4.0"} {
		t.Run(version, func(t *testing.T) {
			s := backupPreviewServer(t)
			g := browserImportTestEngine(t, s)
			document := map[string]any{
				"version": version, "timestamp": 1795392000000,
				"accounts": map[string]any{"accounts": []any{
					map[string]any{"site_name": "Source", "site_url": "https://source.example", "site_type": "new-api", "authType": "access_token", "account_info": map[string]any{"id": "42", "username": "demo", "access_token": "synthetic-hub-access", "quota": 999}, "health": map[string]any{"status": "healthy"}},
					map[string]any{"site_name": "Cookie", "site_url": "https://cookie.example", "site_type": "anyrouter", "authType": "cookie", "account_info": map[string]any{"id": "43", "username": "cookie-user", "access_token": "synthetic-unused-access"}, "cookieAuth": map[string]any{"sessionCookie": "session=synthetic-hub-cookie"}},
					map[string]any{"site_name": "Sub", "site_url": "https://sub.example", "site_type": "sub2api", "authType": "access_token", "account_info": map[string]any{"id": "44", "username": "sub-user", "access_token": "synthetic-hub-jwt"}, "sub2apiAuth": map[string]any{"refreshToken": "synthetic-hub-refresh", "tokenExpiresAt": 1795392000100}},
					map[string]any{"site_name": "Unsupported", "site_url": "https://native.example", "site_type": "kimi", "authType": "none", "account_info": map[string]any{"username": "native", "access_token": "synthetic-native-secret"}},
				}},
				"preferences": map[string]any{"webdav": "synthetic-hub-setting-secret"}, "channelConfigs": map[string]any{"key": "synthetic-channel-secret"},
			}
			response := browserImportAPI(t, g, "/admin/browser-import/inspect", map[string]any{"document": document}, http.StatusOK)
			entries := browserInspectionEntries(t, response)
			if len(entries) != 4 || entries[0].(map[string]any)["platform"] != "new-api-family" || entries[1].(map[string]any)["credential_type"] != "cookie" || entries[2].(map[string]any)["refresh_configured"] != true || entries[3].(map[string]any)["eligible"] != false {
				t.Fatal("external backup subset was interpreted incorrectly")
			}
			raw, _ := json.Marshal(response)
			secrets := []string{"synthetic-hub-access", "session=synthetic-hub-cookie", "synthetic-hub-jwt", "synthetic-hub-refresh", "synthetic-hub-setting-secret", "synthetic-channel-secret", "synthetic-native-secret"}
			for _, value := range document["accounts"].(map[string]any)["accounts"].([]any) {
				row := value.(map[string]any)
				info, _ := row["account_info"].(map[string]any)
				cookie, _ := row["cookieAuth"].(map[string]any)
				refresh, _ := row["sub2apiAuth"].(map[string]any)
				secrets = append(secrets, stringField(info, "access_token"), stringField(cookie, "sessionCookie"), stringField(refresh, "refreshToken"))
			}
			for _, secret := range secrets {
				if secret != "" && strings.Contains(string(raw), secret) {
					t.Fatal("external backup secret returned")
				}
			}
			data := response["data"].(map[string]any)
			if len(data["ignored_sections"].([]any)) != 2 {
				t.Fatal("ignored sections were not explained")
			}
		})
	}
}

func TestBrowserImportInspectRejectsUnsafeSingleEntries(t *testing.T) {
	cases := []struct {
		name, field string
		value       any
	}{
		{"cross-origin", "source_origin", "https://different.example"},
		{"origin-path", "source_origin", "https://source.example/panel"},
		{"userinfo", "base_url", "https://user:secret@source.example"},
		{"query", "base_url", "https://source.example?token=synthetic-query"},
		{"fragment", "base_url", "https://source.example#synthetic-fragment"},
		{"scheme", "base_url", "file:///source.example"},
		{"private", "base_url", "http://127.0.0.1"},
		{"encoded-control", "base_url", "https://source.example/%0a"},
		{"dot-prefix", "base_url", "https://source.example/prefix/../api"},
		{"escaped-dot-prefix", "base_url", "https://source.example/prefix/%2e%2E/api"},
		{"escaped-dot-segment", "base_url", "https://source.example/%2e/api"},
		{"empty-key", "credential", map[string]any{"api_key": ""}},
		{"oversized-key", "credential", map[string]any{"api_key": strings.Repeat("k", 16*1024+1)}},
		{"password", "credential", map[string]any{"api_key": "synthetic-primary", "password": "synthetic-password"}},
		{"unknown-platform", "platform", "not-registered"},
		{"long-name", "account_label", strings.Repeat("名", 192)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := backupPreviewServer(t)
			entry := browserImportEntryFixture()
			entry[tc.field] = tc.value
			response := browserImportAPI(t, browserImportTestEngine(t, s), "/admin/browser-import/inspect", map[string]any{"document": browserImportDocumentFixture(entry, browserImportEntryFixture())}, http.StatusOK)
			entries := browserInspectionEntries(t, response)
			if entries[0].(map[string]any)["eligible"] != false || entries[1].(map[string]any)["eligible"] != true {
				t.Fatal("invalid entry was selected or prevented the valid entry")
			}
		})
	}
}

func TestBrowserImportInspectDocumentBoundsAndDuplicateJSON(t *testing.T) {
	s := backupPreviewServer(t)
	g := browserImportTestEngine(t, s)
	for _, count := range []int{64, 65} {
		entries := make([]map[string]any, count)
		for i := range entries {
			entries[i] = browserImportEntryFixture()
			entries[i]["account_label"] = fmt.Sprintf("account-%d", i)
			entries[i]["credential"] = map[string]any{"api_key": fmt.Sprintf("synthetic-key-%d", i)}
		}
		want := http.StatusOK
		if count == 65 {
			want = http.StatusBadRequest
		}
		browserImportAPI(t, g, "/admin/browser-import/inspect", map[string]any{"document": browserImportDocumentFixture(entries...)}, want)
	}
	for _, doc := range []string{
		`{"format":"pivotflow-browser-bridge","version":"9.0","entries":[]}`,
		`{"format":"pivotflow-browser-bridge","version":"1.0","version":"9.0","entries":[]}`,
		`{"format":"pivotflow-browser-bridge","version":"1.0","entries":[]}`,
		`{"version":"5.0","accounts":{"accounts":[]}}`,
	} {
		browserImportAPI(t, g, "/admin/browser-import/inspect", map[string]any{"document": doc}, http.StatusBadRequest)
	}
	oversized := strings.Repeat("x", 2*1024*1024+1)
	browserImportAPI(t, g, "/admin/browser-import/inspect", map[string]any{"document": oversized}, http.StatusRequestEntityTooLarge)
}

func TestBrowserImportInspectFindsExactCredentialAndPreservesAPIPrefix(t *testing.T) {
	s := backupPreviewServer(t)
	ctx := context.Background()
	site, err := s.store.CreateSite(ctx, &model.Site{Name: "Existing", BaseURL: "https://source.example/prefix", Platform: model.SitePlatformOpenAICompatible, Enabled: false, Timezone: "Asia/Shanghai", TagsJSON: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.siteControl.cipher.Seal(provider.Credentials{APIKey: "synthetic-browser-primary"})
	if err != nil {
		t.Fatal(err)
	}
	account, err := s.store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "existing-user", CredentialType: model.CredentialTypeAPIKey, CredentialCiphertext: sealed, CredentialKeyVersion: s.siteControl.cipher.Version(), Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	entry := browserImportEntryFixture()
	entry["base_url"] = "https://source.example/prefix/"
	response := browserImportAPI(t, browserImportTestEngine(t, s), "/admin/browser-import/inspect", map[string]any{"document": browserImportDocumentFixture(entry, entry)}, http.StatusOK)
	items := browserInspectionEntries(t, response)
	first := items[0].(map[string]any)
	if first["target_site_id"] != float64(site.ID) || first["base_url"] != "https://source.example/prefix" {
		t.Fatal("full API prefix did not match")
	}
	candidates := first["candidates"].([]any)
	if len(candidates) != 1 || candidates[0].(map[string]any)["id"] != float64(account.ID) || candidates[0].(map[string]any)["match"] != "credential" {
		t.Fatal("exact credential was not identified")
	}
	if items[1].(map[string]any)["eligible"] != false || items[1].(map[string]any)["duplicate_of"] != float64(0) {
		t.Fatal("duplicate source was not identified")
	}
	other := browserImportEntryFixture()
	response = browserImportAPI(t, browserImportTestEngine(t, s), "/admin/browser-import/inspect", map[string]any{"document": browserImportDocumentFixture(other)}, http.StatusOK)
	if browserInspectionEntries(t, response)[0].(map[string]any)["target_site_id"] != float64(0) {
		t.Fatal("same origin incorrectly merged two API prefixes")
	}
}

func TestBrowserImportInspectPreservesEscapedPathAndAcceptsExactBounds(t *testing.T) {
	s := backupPreviewServer(t)
	entry := browserImportEntryFixture()
	entry["source_origin"] = "https://SOURCE.example:8443"
	entry["base_url"] = "https://SOURCE.example:8443/prefix%2Ftenant/"
	entry["account_label"] = strings.Repeat("名", 191)
	entry["credential"] = map[string]any{"api_key": strings.Repeat("k", 16*1024)}
	response := browserImportAPI(t, browserImportTestEngine(t, s), "/admin/browser-import/inspect", map[string]any{"document": browserImportDocumentFixture(entry)}, http.StatusOK)
	item := browserInspectionEntries(t, response)[0].(map[string]any)
	if item["eligible"] != true || item["source_origin"] != "https://source.example:8443" || item["base_url"] != "https://source.example:8443/prefix%2Ftenant" {
		t.Fatal("escaped API prefix, port or exact input bounds changed")
	}
}

func TestBrowserImportInspectRejectsInvalidUTF8Credential(t *testing.T) {
	s := backupPreviewServer(t)
	g := browserImportTestEngine(t, s)
	raw := []byte(`{"document":{"format":"pivotflow-browser-bridge","version":"1.0","entries":[{"source_origin":"https://source.example","base_url":"https://source.example","site_name":"Source","platform":"openai-compatible","account_label":"demo","credential_type":"api_key","credential":{"api_key":"synthetic-`)
	raw = append(raw, 0xff)
	raw = append(raw, []byte(`"}}]}}`)...)
	r := httptest.NewRequest(http.MethodPost, "/admin/browser-import/inspect", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer browser-import-admin")
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid UTF-8 credential silently replaced: status=%d", w.Code)
	}
}
