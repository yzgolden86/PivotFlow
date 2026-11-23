package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

func browserImportAuxiliaryEntry() (map[string]any, provider.Credentials) {
	entry := browserImportEntryFixture()
	primary := entry["credential"].(map[string]any)["api_key"].(string)
	old := provider.Credentials{AccessToken: primary, RefreshToken: "synthetic-browser-review-refresh-old", ExpiresAt: 1800000000000, UserID: 42}
	entry["platform"], entry["credential_type"] = model.SitePlatformSub2API, model.CredentialTypeAccessToken
	entry["credential"] = map[string]any{"access_token": old.AccessToken, "refresh_token": old.RefreshToken, "expires_at": old.ExpiresAt, "user_id": old.UserID}
	return entry, old
}

func TestBrowserImportPreviewAuxiliaryCredentialRepairPreservesState(t *testing.T) {
	for _, field := range []string{"refresh_token", "expires_at", "clear_refresh_token"} {
		t.Run(field, func(t *testing.T) {
			s, adapter := browserImportPlanFixture(t, model.SitePlatformSub2API)
			entry, old := browserImportAuxiliaryEntry()
			want := old
			credential := entry["credential"].(map[string]any)
			switch field {
			case "refresh_token":
				want.RefreshToken = "synthetic-browser-review-refresh-new"
				credential["refresh_token"] = want.RefreshToken
			case "expires_at":
				want.ExpiresAt += 60000
				credential["expires_at"] = want.ExpiresAt
				delete(credential, "refresh_token") // Omitted material must be retained.
			case "clear_refresh_token":
				want.RefreshToken = ""
				credential["refresh_token"] = ""
			}
			site, account := browserImportExistingAccount(t, s, model.SitePlatformSub2API, model.CredentialTypeAccessToken, old)
			ctx := context.Background()
			if err := s.store.ReplaceSiteAccountModels(ctx, account.ID, []model.SiteAccountModel{{Model: "retained-review-model", Source: "prior-refresh", RouteType: "openai", LastSeenAt: 123}}); err != nil {
				t.Fatal(err)
			}
			projection, err := s.store.UpsertSiteProjection(ctx, model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: "review-key", Name: "Review channel", BaseURL: site.BaseURL, Protocols: []string{"openai"}, Models: []string{"retained-review-model"}, APIKey: "synthetic-browser-review-channel-key", Enabled: false})
			if err != nil || projection == nil || projection.Channel == nil {
				t.Fatal("could not seed an existing projection")
			}
			store := s.store.(*sqlstore.SQLStore)
			if _, err = store.ExecContext(ctx, "UPDATE channels SET enabled=1 WHERE id=?", projection.Channel.ID); err != nil {
				t.Fatal(err)
			}
			modelsBefore, _ := s.store.ListSiteAccountModels(ctx, model.SiteModelFilter{SiteAccountID: account.ID})
			preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "verify", account.ID), http.StatusOK)
			if !preview.CanApply || preview.Summary["update"] != 1 || adapter.calls.Load() != 2 {
				t.Fatal("a material auxiliary credential repair was not a verified update")
			}
			// A later source edit must not replace the verified frozen materials.
			credential["refresh_token"], credential["expires_at"] = "synthetic-browser-review-late-edit", int64(1900000000000)
			current, _ := s.store.GetSiteAccount(ctx, account.ID)
			balance := 9.0
			current.Balance, current.LastCheckinStatus = &balance, "success"
			if _, err = s.store.UpdateSiteAccount(ctx, account.ID, current); err != nil {
				t.Fatal(err)
			}
			// The ordinary toggle path cascades; restore the manually seeded state.
			if _, err = store.ExecContext(ctx, "UPDATE channels SET enabled=1 WHERE id=?", projection.Channel.ID); err != nil {
				t.Fatal(err)
			}
			bindingsBefore, _ := s.store.ListSiteChannelBindings(ctx)
			if _, err = s.applyConfigPreview(ctx, preview.ID); err != nil {
				t.Fatal(err)
			}
			after, _ := s.store.GetSiteAccount(ctx, account.ID)
			parent, _ := s.store.GetSite(ctx, site.ID)
			var saved provider.Credentials
			if s.siteControl.cipher.Open(after.CredentialCiphertext, &saved) != nil || saved.AccessToken != want.AccessToken || saved.RefreshToken != want.RefreshToken || saved.ExpiresAt != want.ExpiresAt || saved.UserID != want.UserID {
				t.Fatal("apply discarded or replaced a verified auxiliary credential repair")
			}
			if after.Enabled || parent.Enabled || after.AutoRefresh || after.AutoCheckin || after.Label != account.Label || after.Balance == nil || *after.Balance != 9 || after.LastCheckinStatus != "success" || after.Status != model.SiteAccountStatusUnknown || adapter.calls.Load() != 2 {
				t.Fatal("auxiliary repair changed switches, observations, identity or repeated verification")
			}
			channel, _ := s.store.GetConfig(ctx, projection.Channel.ID)
			bindingsAfter, _ := s.store.ListSiteChannelBindings(ctx)
			modelsAfter, _ := s.store.ListSiteAccountModels(ctx, model.SiteModelFilter{SiteAccountID: account.ID})
			before, _ := json.Marshal(struct{ Bindings, Models any }{bindingsBefore, modelsBefore})
			now, _ := json.Marshal(struct{ Bindings, Models any }{bindingsAfter, modelsAfter})
			if !channel.Enabled || !bytes.Equal(before, now) {
				t.Fatal("auxiliary repair changed a channel, binding or model observation")
			}
		})
	}
}

func TestBrowserImportPreviewAuxiliaryChangesRequireVerifiedExplicitTarget(t *testing.T) {
	for _, field := range []string{"refresh_token", "expires_at"} {
		for _, mode := range []string{"verify", "save_disabled"} {
			for _, explicit := range []bool{false, true} {
				if explicit && mode == "verify" {
					continue // The successful repair and state assertions are above.
				}
				t.Run(field+"/"+mode+map[bool]string{false: "/implicit", true: "/explicit"}[explicit], func(t *testing.T) {
					s, adapter := browserImportPlanFixture(t, model.SitePlatformSub2API)
					entry, old := browserImportAuxiliaryEntry()
					_, account := browserImportExistingAccount(t, s, model.SitePlatformSub2API, model.CredentialTypeAccessToken, old)
					credential := entry["credential"].(map[string]any)
					if field == "refresh_token" {
						credential[field] = "synthetic-browser-review-refresh-new"
					} else {
						credential[field] = old.ExpiresAt + 60000
					}
					target, status := int64(0), http.StatusOK
					if explicit {
						target, status = account.ID, http.StatusBadRequest
					}
					preview, response := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, mode, target), status)
					if explicit {
						encoded, _ := json.Marshal(response)
						if !bytes.Contains(encoded, []byte("unverified_update_forbidden")) {
							t.Fatal("unverified auxiliary update did not receive the precise rejection")
						}
					} else if preview.CanApply || preview.Summary["conflict"] != 1 || len(preview.Items) != 1 || preview.Items[0].Message != "target_required" {
						t.Fatal("an auxiliary repair silently selected an existing account")
					}
					after, _ := s.store.GetSiteAccount(context.Background(), account.ID)
					if adapter.calls.Load() != 0 || after.CredentialCiphertext != account.CredentialCiphertext {
						t.Fatal("unselected or unverified repair contacted upstream or wrote credentials")
					}
				})
			}
		}
	}
}

func TestBrowserImportPreviewSameEffectiveCredentialRetainsOmittedMaterial(t *testing.T) {
	for _, jwt := range []bool{false, true} {
		t.Run(map[bool]string{false: "opaque", true: "JWT-effective-expiry"}[jwt], func(t *testing.T) {
			s, adapter := browserImportPlanFixture(t, model.SitePlatformSub2API)
			entry, old := browserImportAuxiliaryEntry()
			credential := entry["credential"].(map[string]any)
			if jwt {
				claims := base64.RawURLEncoding.EncodeToString([]byte(`{"exp":1800000000}`))
				old.AccessToken, old.ExpiresAt = "e30."+claims+".signature", 0
				credential["access_token"], credential["expires_at"] = old.AccessToken, old.EffectiveExpiresAt()
			} else {
				delete(credential, "expires_at")
			}
			delete(credential, "refresh_token")
			credential["user_id"] = 999 // A hint alone must not create a material update.
			_, account := browserImportExistingAccount(t, s, model.SitePlatformSub2API, model.CredentialTypeAccessToken, old)
			preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "verify", 0), http.StatusOK)
			if !preview.CanApply || preview.Summary["update"] != 0 || preview.Summary["unchanged"] != 1 {
				t.Fatal("equivalent material or an input hint created an unnecessary repair")
			}
			if _, err := s.applyConfigPreview(context.Background(), preview.ID); err != nil {
				t.Fatal(err)
			}
			after, _ := s.store.GetSiteAccount(context.Background(), account.ID)
			if after.CredentialCiphertext != account.CredentialCiphertext || after.Status != account.Status || adapter.calls.Load() != 2 {
				t.Fatal("unchanged import rewrote omitted material or account state")
			}
		})
	}
}

type browserImportExpiryResolver struct {
	*browserImportTestAdapter
	expiry int64
}

func (a *browserImportExpiryResolver) ResolveManagementCredentials(ctx context.Context, request provider.AccountRequest) (provider.Credentials, error) {
	credential, err := a.browserImportTestAdapter.ResolveManagementCredentials(ctx, request)
	credential.ExpiresAt = a.expiry
	return credential, err
}

func TestBrowserImportPreviewFinalResolvedCredentialControlsAction(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "implicit", true: "explicit"}[explicit], func(t *testing.T) {
			s, adapter := browserImportPlanFixture(t, model.SitePlatformSub2API)
			entry, old := browserImportAuxiliaryEntry()
			_, account := browserImportExistingAccount(t, s, model.SitePlatformSub2API, model.CredentialTypeAccessToken, old)
			resolver := &browserImportExpiryResolver{browserImportTestAdapter: adapter, expiry: old.ExpiresAt + 60000}
			s.siteControl.registry = provider.NewRegistry(resolver)
			target := int64(0)
			if explicit {
				target = account.ID
			}
			preview, _ := browserImportPreviewAPI(t, s, browserImportPreviewInput(entry, "verify", target), http.StatusOK)
			if !explicit {
				if preview.CanApply || preview.Summary["conflict"] != 1 || preview.Items[0].Message != "target_required" {
					t.Fatal("resolved material silently changed an implicitly selected account")
				}
				return
			}
			if !preview.CanApply || preview.Summary["update"] != 1 {
				t.Fatal("resolved expiry correction was not a material update")
			}
			if _, err := s.applyConfigPreview(context.Background(), preview.ID); err != nil {
				t.Fatal(err)
			}
			after, _ := s.store.GetSiteAccount(context.Background(), account.ID)
			var saved provider.Credentials
			if s.siteControl.cipher.Open(after.CredentialCiphertext, &saved) != nil || saved.ExpiresAt != resolver.expiry || saved.RefreshToken != old.RefreshToken {
				t.Fatal("the final resolved material was not frozen and persisted")
			}
		})
	}
}

func TestBrowserImportPreviewHubAuxiliaryCredentialRepair(t *testing.T) {
	for _, version := range []string{"2.0", "3.0", "4.0"} {
		t.Run(version, func(t *testing.T) {
			s, _ := browserImportPlanFixture(t, model.SitePlatformSub2API)
			entry, old := browserImportAuxiliaryEntry()
			_, account := browserImportExistingAccount(t, s, model.SitePlatformSub2API, model.CredentialTypeAccessToken, old)
			newRefresh := "synthetic-browser-review-hub-refresh"
			input := browserImportPreviewInput(entry, "verify", account.ID)
			input["document"] = map[string]any{"version": version, "accounts": map[string]any{"accounts": []any{map[string]any{
				"site_name": "Source", "site_url": "https://source.example", "site_type": "sub2api", "authType": "access_token",
				"account_info": map[string]any{"username": "demo", "id": old.UserID, "access_token": old.AccessToken},
				"sub2apiAuth":  map[string]any{"refreshToken": newRefresh, "tokenExpiresAt": old.ExpiresAt + 60000},
			}}}}
			preview, _ := browserImportPreviewAPI(t, s, input, http.StatusOK)
			if !preview.CanApply || preview.Summary["update"] != 1 {
				t.Fatal("Hub auxiliary credential change was reported unchanged")
			}
			if _, err := s.applyConfigPreview(context.Background(), preview.ID); err != nil {
				t.Fatal(err)
			}
			after, _ := s.store.GetSiteAccount(context.Background(), account.ID)
			var saved provider.Credentials
			if s.siteControl.cipher.Open(after.CredentialCiphertext, &saved) != nil || saved.RefreshToken != newRefresh || saved.ExpiresAt != old.ExpiresAt+60000 {
				t.Fatal("Hub auxiliary credential repair was discarded")
			}
		})
	}
}

func TestBrowserImportPreviewOptionalCredentialFieldsMustBeMaterialValues(t *testing.T) {
	for _, hub := range []bool{false, true} {
		for _, kind := range []string{"null-is-omitted", "invalid-refresh-type", "invalid-expiry-type"} {
			t.Run(map[bool]string{false: "bridge/", true: "Hub/"}[hub]+kind, func(t *testing.T) {
				s, adapter := browserImportPlanFixture(t, model.SitePlatformSub2API)
				entry, old := browserImportAuxiliaryEntry()
				_, account := browserImportExistingAccount(t, s, model.SitePlatformSub2API, model.CredentialTypeAccessToken, old)
				credential := entry["credential"].(map[string]any)
				credential["refresh_token"], credential["expires_at"] = nil, nil
				switch kind {
				case "invalid-refresh-type":
					credential["refresh_token"] = 12
				case "invalid-expiry-type":
					credential["expires_at"] = "unknown"
				}
				input := browserImportPreviewInput(entry, "verify", account.ID)
				if hub {
					input["document"] = map[string]any{"version": "4.0", "accounts": []any{map[string]any{
						"site_name": "Source", "site_url": "https://source.example", "site_type": "sub2api", "authType": "access_token",
						"account_info": map[string]any{"username": "demo", "id": old.UserID, "access_token": old.AccessToken},
						"sub2apiAuth":  map[string]any{"refreshToken": credential["refresh_token"], "tokenExpiresAt": credential["expires_at"]},
					}}}
				}
				status := http.StatusBadRequest
				if kind == "null-is-omitted" {
					status = http.StatusOK
				}
				preview, _ := browserImportPreviewAPI(t, s, input, status)
				if kind == "null-is-omitted" {
					if !preview.CanApply || preview.Summary["unchanged"] != 1 {
						t.Fatal("null optional fields cleared existing refresh or expiry material")
					}
					if _, err := s.applyConfigPreview(context.Background(), preview.ID); err != nil {
						t.Fatal(err)
					}
				} else if adapter.calls.Load() != 0 {
					t.Fatal("malformed optional credential fields reached the provider")
				}
				after, _ := s.store.GetSiteAccount(context.Background(), account.ID)
				if after.CredentialCiphertext != account.CredentialCiphertext {
					t.Fatal("omitted or malformed optional fields changed existing credential material")
				}
			})
		}
	}
}
