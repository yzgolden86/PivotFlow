package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"path/filepath"
	"testing"

	"ccLoad/internal/model"
	"ccLoad/internal/site/credential"
	"ccLoad/internal/site/provider"
)

func TestSQLiteSnapshotPreservesEncryptedSiteCredentials(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	t.Setenv("FUSION_MASTER_KEY", base64.RawURLEncoding.EncodeToString(key))
	t.Setenv("FUSION_MASTER_KEY_VERSION", "snapshot-test")
	cipher, err := credential.NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := cipher.Seal(provider.Credentials{APIKey: "sk-snapshot-secret"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := CreateSQLiteStore(filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{Name: "snapshot", Platform: model.SitePlatformNewAPIFamily, BaseURL: "https://example.com", Enabled: true, Timezone: "Asia/Shanghai", TagsJSON: "[]", LastProbeStatus: "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: site.ID, Label: "main", CredentialType: model.CredentialTypeAPIKey, CredentialCiphertext: sealed, CredentialKeyVersion: cipher.Version(), Enabled: true, Status: model.SiteAccountStatusUnknown, BalanceCurrency: "CNY", LastRefreshStatus: "unknown", LastCheckinStatus: "unknown"}); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "backup", "ccload.db")
	if err := CreateSQLiteSnapshot(ctx, store, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := VerifySQLiteSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(snapshot)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var restoredCiphertext string
	if err := db.QueryRowContext(ctx, "SELECT credential_ciphertext FROM site_accounts WHERE site_id=?", site.ID).Scan(&restoredCiphertext); err != nil {
		t.Fatal(err)
	}
	if restoredCiphertext == "sk-snapshot-secret" {
		t.Fatal("snapshot contains a plaintext credential")
	}
	var restored provider.Credentials
	if err := cipher.Open(restoredCiphertext, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.APIKey != "sk-snapshot-secret" {
		t.Fatalf("restored API key = %q", restored.APIKey)
	}
}
