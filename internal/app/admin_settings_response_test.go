package app

import (
	"database/sql"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func TestAdminAPI_ListSettings_ResponseShape(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()

	server.configService = NewConfigService(store)

	c, w := newTestContext(t, newRequest(http.MethodGet, "/admin/settings", nil))

	server.AdminListSettings(c)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	resp := mustParseAPIResponse[[]*model.SystemSetting](t, w.Body.Bytes())
	if !resp.Success {
		t.Fatalf("success=false, error=%q", resp.Error)
	}
	if resp.Data == nil {
		t.Fatalf("data is null, want []")
	}
}

func TestUpgradedSettingsDoNotAdvertiseRemovedFuzzyMatching(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	store, err := storage.CreateSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, seedErr := db.Exec(`INSERT INTO system_settings (key, value, value_type, description, default_value, updated_at) VALUES ('model_fuzzy_match', 'true', 'bool', 'Legacy fuzzy matching', 'false', unixepoch())`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if seedErr != nil {
		t.Fatal(seedErr)
	}
	// A second reopen verifies the removal is idempotent as well as upgrade-safe.
	for attempt := 0; attempt < 2; attempt++ {
		store, err := storage.CreateSQLiteStore(path)
		if err != nil {
			t.Fatal(err)
		}
		server := &Server{configService: NewConfigService(store)}
		c, response := newTestContext(t, newRequest(http.MethodGet, "/admin/settings", nil))
		server.AdminListSettings(c)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK {
			t.Fatalf("settings status=%d body=%s", response.Code, response.Body.String())
		}
		settings := mustParseAPIResponse[[]*model.SystemSetting](t, response.Body.Bytes())
		for _, setting := range settings.Data {
			if setting.Key == "model_fuzzy_match" {
				t.Fatal("upgraded settings still expose the removed fuzzy matching control")
			}
		}
	}
}
