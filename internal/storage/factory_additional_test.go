package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
)

func TestIsDirWritable(t *testing.T) {
	dir := t.TempDir()
	if !isDirWritable(dir) {
		t.Fatalf("expected dir writable: %s", dir)
	}

	filePath := filepath.Join(dir, "f")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	if isDirWritable(filePath) {
		t.Fatalf("expected file path not writable as dir: %s", filePath)
	}

	if isDirWritable(filepath.Join(dir, "no_such_dir")) {
		t.Fatal("expected non-existent dir not writable")
	}
}

func TestResolveSQLitePath_DefaultAndFallback(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}

	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	// 默认：data 目录可创建/可写
	got := resolveSQLitePath()
	if got != filepath.Join("data", "pivotflow.db") {
		t.Fatalf("resolveSQLitePath()=%q, want %q", got, filepath.Join("data", "pivotflow.db"))
	}

	// fallback：用同名文件阻止 data 目录创建
	if err := os.RemoveAll("data"); err != nil {
		t.Fatalf("RemoveAll(data) failed: %v", err)
	}
	if err := os.WriteFile("data", []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("write data file failed: %v", err)
	}

	got2 := resolveSQLitePath()
	if !strings.Contains(got2, filepath.Join(os.TempDir(), "pivotflow")) {
		t.Fatalf("expected fallback path under temp dir, got %q", got2)
	}
}

func TestGetLogSyncDays(t *testing.T) {
	t.Setenv("PIVOTFLOW_SQLITE_LOG_DAYS", "")
	if got := getLogSyncDays(); got != 7 {
		t.Fatalf("default getLogSyncDays=%d, want 7", got)
	}

	t.Setenv("PIVOTFLOW_SQLITE_LOG_DAYS", "0")
	if got := getLogSyncDays(); got != 0 {
		t.Fatalf("getLogSyncDays=%d, want 0", got)
	}

	t.Setenv("PIVOTFLOW_SQLITE_LOG_DAYS", "-1")
	if got := getLogSyncDays(); got != -1 {
		t.Fatalf("getLogSyncDays=%d, want -1", got)
	}

	t.Setenv("PIVOTFLOW_SQLITE_LOG_DAYS", "-2")
	if got := getLogSyncDays(); got != 7 {
		t.Fatalf("invalid getLogSyncDays=%d, want 7", got)
	}

	t.Setenv("PIVOTFLOW_SQLITE_LOG_DAYS", "not-an-int")
	if got := getLogSyncDays(); got != 7 {
		t.Fatalf("invalid getLogSyncDays=%d, want 7", got)
	}
}

func TestNewStore_SQLiteMode_UsesTempCWDDefaultPath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	t.Setenv("PIVOTFLOW_MYSQL", "")
	t.Setenv("SQLITE_PATH", "")

	s, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}

func TestValidateJournalMode(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "WAL"},
		{"WAL", "WAL"},
		{"wal", "WAL"},
		{"DELETE", "DELETE"},
		{"delete", "DELETE"},
		{"TRUNCATE", "TRUNCATE"},
		{"PERSIST", "PERSIST"},
		{"MEMORY", "MEMORY"},
		{"OFF", "OFF"},
	}

	for _, tc := range tests {
		t.Run("mode_"+tc.input, func(t *testing.T) {
			result := validateJournalMode(tc.input)
			if result != tc.expected {
				t.Errorf("validateJournalMode(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestBuildSQLiteDSN(t *testing.T) {
	dsn := buildSQLiteDSN("/tmp/test.db")
	if !strings.Contains(dsn, "/tmp/test.db") {
		t.Errorf("DSN should contain db path, got %q", dsn)
	}
	if !strings.Contains(dsn, "journal_mode") {
		t.Errorf("DSN should contain journal_mode pragma, got %q", dsn)
	}
	if !strings.Contains(dsn, "busy_timeout") {
		t.Errorf("DSN should contain busy_timeout pragma, got %q", dsn)
	}
}

func TestNewStore_WithExplicitSQLitePath(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "explicit.db")

	t.Setenv("PIVOTFLOW_MYSQL", "")
	t.Setenv("SQLITE_PATH", dbPath)

	s, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer func() { _ = s.Close() }()

	// 验证文件存在
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatalf("database file not created at %s", dbPath)
	}

	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}

func TestNewStoreMigratesAndEncryptsChannelSecrets(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "encrypted-channel-secrets.db")
	ctx := context.Background()

	legacy, err := CreateSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create legacy store: %v", err)
	}
	apiChannel, err := legacy.CreateConfig(ctx, &model.Config{
		Name: "legacy-api-key", URLs: model.ChannelURLs{{URL: "https://api.example.test"}},
		Enabled: true, ModelEntries: []model.ModelEntry{{Model: "gpt-test"}},
	})
	if err != nil {
		t.Fatalf("create legacy API channel: %v", err)
	}
	if err := legacy.CreateAPIKeysBatch(ctx, []*model.APIKey{{
		ChannelID: apiChannel.ID, KeyIndex: 0, APIKey: "sk-legacy-plaintext",
	}}); err != nil {
		t.Fatalf("create legacy API key: %v", err)
	}
	oauthPayload := `{"type":"codex","access_token":"at-legacy-plaintext","refresh_token":"rt-legacy-plaintext"}`
	oauthChannel, err := legacy.CreateConfig(ctx, &model.Config{
		Name: "legacy-oauth", AuthType: model.AuthTypeCodexOAuth, OAuthCredential: oauthPayload,
		URLs:    model.ChannelURLs{{URL: "https://chatgpt.com/backend-api/codex", Protocols: []string{"codex"}}},
		Enabled: true, ModelEntries: []model.ModelEntry{{Model: "*"}},
	})
	if err != nil {
		t.Fatalf("create legacy OAuth channel: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy store: %v", err)
	}

	rightKey := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	t.Setenv("PIVOTFLOW_MYSQL", "")
	t.Setenv("PIVOTFLOW_POSTGRES", "")
	t.Setenv("PIVOTFLOW_ENABLE_SQLITE_REPLICA", "")
	t.Setenv("SQLITE_PATH", dbPath)
	t.Setenv("FUSION_MASTER_KEY", rightKey)
	t.Setenv("FUSION_MASTER_KEY_FILE", "")
	t.Setenv("FUSION_MASTER_KEY_VERSION", "v1")

	store, err := NewStore()
	if err != nil {
		t.Fatalf("migrate channel secrets: %v", err)
	}
	keys, err := store.GetAPIKeys(ctx, apiChannel.ID)
	if err != nil || len(keys) != 1 || keys[0].APIKey != "sk-legacy-plaintext" {
		t.Fatalf("decrypted API keys = %#v, err=%v", keys, err)
	}
	oauthConfig, err := store.GetConfig(ctx, oauthChannel.ID)
	if err != nil || oauthConfig.OAuthCredential != oauthPayload {
		t.Fatalf("decrypted OAuth credential = %q, err=%v", oauthConfig.OAuthCredential, err)
	}
	newChannel, err := store.CreateConfig(ctx, &model.Config{
		Name: "new-encrypted-api-key", URLs: model.ChannelURLs{{URL: "https://new.example.test"}},
		Enabled: true, ModelEntries: []model.ModelEntry{{Model: "gpt-new"}},
	})
	if err != nil {
		t.Fatalf("create channel after encryption enabled: %v", err)
	}
	if err := store.CreateAPIKeysBatch(ctx, []*model.APIKey{{
		ChannelID: newChannel.ID, KeyIndex: 0, APIKey: "sk-new-plaintext",
	}}); err != nil {
		t.Fatalf("create key after encryption enabled: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close encrypted store: %v", err)
	}

	rawDB, err := sql.Open("sqlite", buildSQLiteDSN(dbPath))
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	var rawAPIKey, rawNewAPIKey, rawOAuth string
	if err := rawDB.QueryRowContext(ctx, "SELECT api_key FROM api_keys WHERE channel_id=?", apiChannel.ID).Scan(&rawAPIKey); err != nil {
		t.Fatalf("read raw API key: %v", err)
	}
	if err := rawDB.QueryRowContext(ctx, "SELECT oauth_credential FROM channels WHERE id=?", oauthChannel.ID).Scan(&rawOAuth); err != nil {
		t.Fatalf("read raw OAuth credential: %v", err)
	}
	if err := rawDB.QueryRowContext(ctx, "SELECT api_key FROM api_keys WHERE channel_id=?", newChannel.ID).Scan(&rawNewAPIKey); err != nil {
		t.Fatalf("read newly stored raw API key: %v", err)
	}
	if !credential.IsSealed(rawAPIKey) || strings.Contains(rawAPIKey, "sk-legacy-plaintext") {
		t.Fatalf("API key was not encrypted at rest: %q", rawAPIKey)
	}
	if !credential.IsSealed(rawOAuth) || strings.Contains(rawOAuth, "at-legacy-plaintext") || strings.Contains(rawOAuth, "rt-legacy-plaintext") {
		t.Fatalf("OAuth credential was not encrypted at rest: %q", rawOAuth)
	}
	if !credential.IsSealed(rawNewAPIKey) || strings.Contains(rawNewAPIKey, "sk-new-plaintext") {
		t.Fatalf("new API key was not encrypted at rest: %q", rawNewAPIKey)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw database: %v", err)
	}

	wrongKey := base64.RawURLEncoding.EncodeToString([]byte("fedcba9876543210fedcba9876543210"))
	t.Setenv("FUSION_MASTER_KEY", wrongKey)
	if wrongStore, err := NewStore(); err == nil {
		_ = wrongStore.Close()
		t.Fatal("NewStore accepted a master key that cannot decrypt existing channel secrets")
	}

	t.Setenv("FUSION_MASTER_KEY", rightKey)
	reopened, err := NewStore()
	if err != nil {
		t.Fatalf("reopen after rejected key: %v", err)
	}
	defer func() { _ = reopened.Close() }()
	if got, err := reopened.GetAPIKey(ctx, apiChannel.ID, 0); err != nil || got.APIKey != "sk-legacy-plaintext" {
		t.Fatalf("credential changed after rejected key: %#v, err=%v", got, err)
	}
}

func TestCreateSQLiteStore(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")

	store, err := CreateSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("CreateSQLiteStore failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}

func TestCreateSQLiteStore_CreatesParentDir(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "nested", "deep", "test.db")

	store, err := CreateSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("CreateSQLiteStore failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	// 验证父目录被创建
	if _, err := os.Stat(filepath.Dir(dbPath)); os.IsNotExist(err) {
		t.Fatalf("parent directory not created")
	}
}
