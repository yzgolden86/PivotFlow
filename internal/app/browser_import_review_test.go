package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

func TestBrowserImportPreviewReusesDeletedSiteNameWithoutResurrection(t *testing.T) {
	for _, legacyName := range []bool{false, true} {
		t.Run(map[bool]string{false: "current-physical-delete", true: "legacy-name-retained"}[legacyName], func(t *testing.T) {
			s, adapter := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
			site, _ := browserImportExistingAccount(t, s, model.SitePlatformOpenAICompatible, model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-deleted-site-key"})
			ctx := context.Background()
			if legacyName {
				// Current DeleteSite physically deletes. Older databases can still
				// contain a soft-deleted row whose unique name was never released.
				if _, err := s.store.(*sqlstore.SQLStore).ExecContext(ctx, "UPDATE sites SET deleted_at=1 WHERE id=?", site.ID); err != nil {
					t.Fatal(err)
				}
			} else if err := s.store.DeleteSite(ctx, site.ID); err != nil {
				t.Fatal(err)
			}
			entry := browserImportEntryFixture()
			entry["source_origin"], entry["base_url"], entry["site_name"] = "https://replacement.example", "https://replacement.example/api", site.Name
			preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "verify", 0), http.StatusOK)
			if !preview.CanApply || preview.Summary["conflict"] != 0 || adapter.calls.Load() != 1 {
				t.Fatal("a tombstoned site name blocked the legitimate new-site import")
			}
			if _, err := s.applyConfigPreview(ctx, preview.ID); err != nil {
				t.Fatal(err)
			}
			sites, _ := s.store.ListSites(ctx, model.SiteListFilter{IncludeDeleted: true})
			live, deleted := 0, 0
			for _, current := range sites {
				if current.ID == site.ID && current.DeletedAt > 0 {
					deleted++
				} else if current.ID != site.ID && current.DeletedAt == 0 && current.Name == site.Name && current.BaseURL == "https://replacement.example/api" {
					live++
				}
			}
			wantDeleted := 0
			if legacyName {
				wantDeleted = 1
			}
			if live != 1 || deleted != wantDeleted || adapter.calls.Load() != 1 {
				t.Fatal("name reuse resurrected the old site or repeated verification")
			}
		})
	}
}

func TestBrowserImportPreviewDeletedSiteNameRaceStillInvalidatesPreview(t *testing.T) {
	s, _ := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
	site, _ := browserImportExistingAccount(t, s, model.SitePlatformOpenAICompatible, model.CredentialTypeAPIKey, provider.Credentials{APIKey: "synthetic-deleted-site-key"})
	ctx := context.Background()
	store := s.store.(*sqlstore.SQLStore)
	if _, err := store.ExecContext(ctx, "UPDATE sites SET deleted_at=1 WHERE id=?", site.ID); err != nil {
		t.Fatal(err)
	}
	entry := browserImportEntryFixture()
	entry["source_origin"], entry["base_url"], entry["site_name"] = "https://replacement.example", "https://replacement.example", site.Name
	preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "save_disabled", 0), http.StatusOK)
	if !preview.CanApply {
		t.Fatal("deleted-name race fixture could not create its initial preview")
	}
	if _, err := store.ExecContext(ctx, "UPDATE sites SET deleted_at=0 WHERE id=?", site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.applyConfigPreview(ctx, preview.ID); err == nil || !strings.Contains(err.Error(), "preview_stale") {
		t.Fatal("restored name conflict did not invalidate the frozen preview")
	}
	sites, _ := s.store.ListSites(ctx, model.SiteListFilter{})
	if len(sites) != 1 || sites[0].ID != site.ID {
		t.Fatal("stale preview created a conflicting site")
	}
}

func TestBrowserImportJSONUnicodeEscapesRejectUnpairedSurrogates(t *testing.T) {
	cases := []struct {
		name, encoded, want string
		valid               bool
	}{
		{name: "unpaired-high", encoded: `"synthetic-\ud800"`},
		{name: "unpaired-low", encoded: `"synthetic-\udc00"`},
		{name: "high-before-text", encoded: `"synthetic-\ud800x"`},
		{name: "two-high", encoded: `"synthetic-\ud800\ud800"`},
		{name: "high-before-unrelated-escape", encoded: `"synthetic-\ud800\u0061"`},
		{name: "valid-pair", encoded: `"synthetic-\ud83d\ude80"`, want: "synthetic-🚀", valid: true},
		{name: "literal-replacement", encoded: `"synthetic-�"`, want: "synthetic-�", valid: true},
		{name: "escaped-replacement", encoded: `"synthetic-\ufffd"`, want: "synthetic-�", valid: true},
		{name: "literal-backslash-u", encoded: `"synthetic-\\ud800"`, want: `synthetic-\ud800`, valid: true},
	}
	for _, path := range []string{"/admin/browser-import/inspect", "/admin/browser-import/preview"} {
		for _, textDocument := range []bool{false, true} {
			for _, tc := range cases {
				t.Run(path+map[bool]string{false: "/object/", true: "/text/"}[textDocument]+tc.name, func(t *testing.T) {
					s, adapter := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
					entry := browserImportEntryFixture()
					entry["credential"] = map[string]any{"api_key": "synthetic-unicode-marker"}
					raw, _ := json.Marshal(browserImportDocumentFixture(entry))
					raw = bytes.Replace(raw, []byte(`"synthetic-unicode-marker"`), []byte(tc.encoded), 1)
					var document any = json.RawMessage(raw)
					if textDocument {
						document = string(raw)
					}
					input := browserImportPreviewInput(entry, "save_disabled", 0)
					input["document"] = document
					status := http.StatusBadRequest
					if tc.valid {
						status = http.StatusOK
					}
					response := browserImportAPI(t, browserImportTestEngine(t, s), path, input, status)
					if tc.valid {
						decoded, err := decodeBrowserImportDocument(raw)
						if err != nil || decoded.Entries[0].Credential.APIKey != tc.want {
							t.Fatal("well-formed Unicode or a literal escape changed during decoding")
						}
						if path == "/admin/browser-import/inspect" && browserInspectionEntries(t, response)[0].(map[string]any)["eligible"] != true {
							t.Fatal("well-formed Unicode credential became ineligible")
						}
					} else {
						encoded, _ := json.Marshal(response)
						if !bytes.Contains(encoded, []byte("browser_import_invalid_document")) {
							t.Fatal("malformed Unicode did not receive the document rejection")
						}
					}
					if adapter.calls.Load() != 0 {
						t.Fatal("document inspection or disabled preview made an upstream request")
					}
				})
			}
		}
	}
}

func TestBrowserImportInspectCanonicalEscapesMatchFullSiteIdentity(t *testing.T) {
	cases := []struct {
		name, existing, imported, normalized string
		match                                bool
	}{
		{"unreserved-api", "/api", "/%61%70%69/", "/api", true},
		{"unreserved-range", "/api/~A-_.", "/api/%7e%41%2D%5F%2E", "/api/~A-_.", true},
		{"reserved-slash-preserved", "/prefix%2Ftenant", "/%70refix%2ftenant/", "/prefix%2Ftenant", true},
		{"reserved-slash-is-distinct", "/prefix/tenant", "/prefix%2Ftenant", "/prefix%2Ftenant", false},
		{"full-prefix-is-distinct", "/api", "/%61pi/v1", "/api/v1", false},
		{"path-case-is-distinct", "/API", "/%61pi", "/api", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, adapter := browserImportPlanFixture(t, model.SitePlatformOpenAICompatible)
			entry := browserImportEntryFixture()
			primary := entry["credential"].(map[string]any)["api_key"].(string)
			site, account := browserImportExistingAccount(t, s, model.SitePlatformOpenAICompatible, model.CredentialTypeAPIKey, provider.Credentials{APIKey: primary})
			ctx := context.Background()
			site.BaseURL = "https://source.example" + tc.existing
			if _, err := s.store.UpdateSite(ctx, site.ID, site); err != nil {
				t.Fatal(err)
			}
			entry["base_url"] = "https://source.example" + tc.imported
			canonical := browserImportEntryFixture()
			canonical["base_url"] = "https://source.example" + tc.normalized
			response := browserImportAPI(t, browserImportTestEngine(t, s), "/admin/browser-import/inspect", map[string]any{"document": browserImportDocumentFixture(entry, canonical)}, http.StatusOK)
			items := browserInspectionEntries(t, response)
			first := items[0].(map[string]any)
			wantID := float64(0)
			if tc.match {
				wantID = float64(site.ID)
			}
			if first["eligible"] != true || first["base_url"] != "https://source.example"+tc.normalized || first["target_site_id"] != wantID {
				t.Fatal("equivalent escape or distinct full API prefix changed site identity")
			}
			if tc.match {
				candidates := first["candidates"].([]any)
				if len(candidates) != 1 || candidates[0].(map[string]any)["id"] != float64(account.ID) || candidates[0].(map[string]any)["match"] != "credential" {
					t.Fatal("canonical URL did not match the existing account credential")
				}
			}
			if items[1].(map[string]any)["eligible"] != false || items[1].(map[string]any)["duplicate_of"] != float64(0) {
				t.Fatal("equivalent source spellings were not deduplicated")
			}
			preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "save_disabled", 0), http.StatusOK)
			if !preview.CanApply || tc.match && preview.Summary["unchanged"] != 1 || !tc.match && preview.Summary["unchanged"] != 0 || adapter.calls.Load() != 0 {
				t.Fatal("canonical URL preview duplicated or merged a distinct account")
			}
		})
	}
}
