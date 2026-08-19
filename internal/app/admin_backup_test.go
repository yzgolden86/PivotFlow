package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func TestBackupRoundTripReencryptsAccountAndTokenCredentials(t *testing.T) {
	source := newInMemoryServer(t)
	sourceCipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "backup-source")
	if err != nil {
		t.Fatal(err)
	}
	source.siteControl.cipher = sourceCipher
	ctx := context.Background()
	site, err := source.store.CreateSite(ctx, &model.Site{Name: "Source", Platform: model.SitePlatformOpenAICompatible, BaseURL: "https://source.example", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]", LastProbeStatus: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	accountSecret := "sk-upstream-backup-secret"
	accountCiphertext, err := sourceCipher.Seal(provider.Credentials{APIKey: accountSecret})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "primary", CredentialType: model.CredentialTypeAPIKey, CredentialCiphertext: accountCiphertext, CredentialKeyVersion: sourceCipher.Version(), Enabled: true, Status: model.SiteAccountStatusHealthy, BalanceCurrency: "USD", LastRefreshStatus: "unknown", LastCheckinStatus: "unknown"}); err != nil {
		t.Fatal(err)
	}
	tokenSecret := "sk-pivotflow-backup-token"
	tokenCiphertext, err := sourceCipher.Seal(tokenSecret)
	if err != nil {
		t.Fatal(err)
	}
	token := &model.AuthToken{Token: model.HashToken(tokenSecret), TokenCiphertext: tokenCiphertext, TokenHint: model.MaskToken(tokenSecret), Description: "backup token", IsActive: true, ChannelRestrictionMode: model.ChannelRestrictionModeAllow, MaxConcurrency: 2}
	if err = source.store.CreateAuthToken(ctx, token); err != nil {
		t.Fatal(err)
	}

	document, err := source.buildBackupDocument(ctx, "connections")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), accountSecret) || !strings.Contains(string(raw), tokenSecret) {
		t.Fatalf("portable backup is missing decrypted credentials: %s", raw)
	}
	for _, forbidden := range []string{accountCiphertext, tokenCiphertext, "credential_ciphertext", "token_ciphertext"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("backup leaked source ciphertext field %q", forbidden)
		}
	}

	target := newInMemoryServer(t)
	targetCipher, err := credential.New([]byte("fedcba9876543210fedcba9876543210"), "backup-target")
	if err != nil {
		t.Fatal(err)
	}
	target.siteControl.cipher = targetCipher
	result, err := target.importBackupDocument(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if result.Sites != 1 || result.Accounts != 1 || result.Tokens != 1 {
		t.Fatalf("unexpected import result: %+v", result)
	}
	accounts, err := target.store.ListSiteAccounts(ctx, 0, false)
	if err != nil || len(accounts) != 1 {
		t.Fatalf("accounts=%d err=%v", len(accounts), err)
	}
	credentials, err := target.siteControl.credentials(accounts[0])
	if err != nil || credentials.APIKey != accountSecret {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
	if accounts[0].CredentialCiphertext == accountCiphertext {
		t.Fatal("account credential was not re-encrypted with the target key")
	}
	tokens, err := target.store.ListAuthTokens(ctx)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("tokens=%d err=%v", len(tokens), err)
	}
	var recovered string
	if err = targetCipher.Open(tokens[0].TokenCiphertext, &recovered); err != nil || recovered != tokenSecret {
		t.Fatalf("recovered token=%q err=%v", recovered, err)
	}
}

func TestWebDAVBackupUsesBasicAuthAndMasksStoredPassword(t *testing.T) {
	var uploaded []byte
	var methods []string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "dav-user" || password != "dav-password" {
			t.Errorf("unexpected basic auth: ok=%v username=%q password=%q", ok, username, password)
		}
		methods = append(methods, r.Method)
		switch r.Method {
		case http.MethodPut:
			uploaded, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(uploaded)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer remote.Close()

	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "webdav-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	sealed, err := cipher.Seal(backupPasswordSecret{Value: "dav-password"})
	if err != nil {
		t.Fatal(err)
	}
	config := &model.BackupConfig{ID: 1, Enabled: true, FileURL: remote.URL + "/backup.json", Username: "dav-user", PasswordCiphertext: sealed, PasswordKeyVersion: cipher.Version(), ExportType: "settings", AutoSyncIntervalHours: 24}
	if err = srv.store.UpsertBackupConfig(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	if _, err = srv.exportToWebDAV(context.Background(), "settings"); err != nil {
		t.Fatal(err)
	}
	if len(uploaded) == 0 {
		t.Fatal("WebDAV PUT did not upload a backup")
	}
	if _, _, err = srv.importFromWebDAV(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || methods[0] != http.MethodPut || methods[1] != http.MethodGet {
		t.Fatalf("methods=%v, want [PUT GET]", methods)
	}

	c, recorder := newTestContext(t, newRequest(http.MethodGet, "/admin/backup/webdav", nil))
	srv.HandleBackupWebDAV(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, forbidden := range []string{"dav-password", sealed, "password_ciphertext", "password_key_version"} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("WebDAV response leaked %q: %s", forbidden, recorder.Body.String())
		}
	}
	if !strings.Contains(recorder.Body.String(), "password_configured") || !strings.Contains(recorder.Body.String(), "********") {
		t.Fatalf("WebDAV response is missing masked password state: %s", recorder.Body.String())
	}
}

func TestWebDAVHTTPErrorProvidesSafeActionableDetails(t *testing.T) {
	tests := []struct {
		status    int
		operation string
		contains  string
	}{
		{http.StatusUnauthorized, "upload", "认证失败"},
		{http.StatusNotFound, "upload", "父目录不存在"},
		{http.StatusNotFound, "download", "没有找到备份文件"},
		{http.StatusMethodNotAllowed, "upload", "完整文件地址"},
		{http.StatusInsufficientStorage, "upload", "存储空间不足"},
	}
	for _, test := range tests {
		err := webDAVHTTPError(test.status, test.operation)
		if !strings.Contains(err.Error(), fmt.Sprintf("webdav_http_%d", test.status)) || !strings.Contains(err.Error(), test.contains) {
			t.Fatalf("status=%d operation=%s error=%q", test.status, test.operation, err)
		}
		if strings.Contains(err.Error(), "Authorization") || strings.Contains(err.Error(), "password") {
			t.Fatalf("error contains sensitive implementation details: %q", err)
		}
	}
}

func TestWebDAVHTMLResponseOnlyMatchesWebPages(t *testing.T) {
	for _, contentType := range []string{"text/html; charset=utf-8", "application/xhtml+xml"} {
		response := &http.Response{Header: make(http.Header)}
		response.Header.Set("Content-Type", contentType)
		if !webDAVHTMLResponse(response) {
			t.Fatalf("content type %q was not detected as HTML", contentType)
		}
	}
	response := &http.Response{Header: make(http.Header)}
	response.Header.Set("Content-Type", "application/json")
	if webDAVHTMLResponse(response) {
		t.Fatal("JSON response was detected as HTML")
	}
}

func TestRemapBackupChannelIDsDropsUnknownChannels(t *testing.T) {
	got := remapBackupChannelIDs([]int64{2, 7, 9}, map[int64]int64{2: 20, 9: 90})
	if len(got) != 2 || got[0] != 20 || got[1] != 90 {
		t.Fatalf("remapped IDs=%v, want [20 90]", got)
	}
}
