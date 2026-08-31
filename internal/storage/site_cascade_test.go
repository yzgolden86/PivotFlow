package storage

import (
	"context"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// cascadeFixture builds one site with two accounts, each projecting one channel.
func cascadeFixture(t *testing.T) (Store, int64, []int64, []int64) {
	t.Helper()
	store, err := CreateSQLiteStore(t.TempDir() + "/cascade.db")
	if err != nil {
		t.Fatalf("CreateSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{
		Name: "Cascade Site", Platform: model.SitePlatformNewAPIFamily,
		BaseURL: "https://cascade.example.com", Enabled: true, Timezone: "Asia/Shanghai",
	})
	if err != nil {
		t.Fatalf("CreateSite: %v", err)
	}

	accountIDs := make([]int64, 0, 2)
	channelIDs := make([]int64, 0, 2)
	for i := range 2 {
		account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{
			SiteID: site.ID, Label: string(rune('a' + i)),
			CredentialType: model.CredentialTypeAPIKey, CredentialCiphertext: "sealed",
			Enabled: true, Status: model.SiteAccountStatusHealthy,
		})
		if err != nil {
			t.Fatalf("CreateSiteAccount: %v", err)
		}
		accountIDs = append(accountIDs, account.ID)

		protocols := []string{"anthropic"}
		models := []string{"claude-opus-5"}
		apiKey := "sk-cascade"
		projection, err := store.UpsertSiteProjection(ctx, model.SiteProjectionInput{
			SiteAccountID: account.ID, ProjectionKey: "default",
			Name: "Cascade / " + string(rune('a'+i)), BaseURL: site.BaseURL,
			Protocols: protocols, Models: models, APIKey: apiKey,
			SourceHash: model.SiteProjectionSourceHash(site.BaseURL, protocols, models, apiKey, true),
			Enabled:    true,
		})
		if err != nil {
			t.Fatalf("UpsertSiteProjection: %v", err)
		}
		channelIDs = append(channelIDs, projection.Channel.ID)
	}
	return store, site.ID, accountIDs, channelIDs
}

func channelEnabled(t *testing.T, store Store, id int64) bool {
	t.Helper()
	cfg, err := store.GetConfig(context.Background(), id)
	if err != nil {
		t.Fatalf("GetConfig(%d): %v", id, err)
	}
	return cfg.Enabled
}

func accountEnabled(t *testing.T, store Store, id int64) bool {
	t.Helper()
	account, err := store.GetSiteAccount(context.Background(), id)
	if err != nil {
		t.Fatalf("GetSiteAccount(%d): %v", id, err)
	}
	return account.Enabled
}

// Disabling a site must stop its accounts and its projected channels; leaving
// channels enabled would keep routing traffic to a site the user turned off.
func TestCascadeSiteSuspendDisablesAccountsAndChannels(t *testing.T) {
	store, siteID, accountIDs, channelIDs := cascadeFixture(t)
	ctx := context.Background()

	accounts, channels, err := store.CascadeSiteSuspend(ctx, siteID, false)
	if err != nil {
		t.Fatalf("CascadeSiteSuspend(disable): %v", err)
	}
	if accounts != 2 || channels != 2 {
		t.Fatalf("accounts=%d channels=%d, want 2 and 2", accounts, channels)
	}
	for _, id := range accountIDs {
		if accountEnabled(t, store, id) {
			t.Errorf("account %d still enabled after site disable", id)
		}
	}
	for _, id := range channelIDs {
		if channelEnabled(t, store, id) {
			t.Errorf("channel %d still enabled after site disable", id)
		}
	}
}

// Re-enabling restores exactly what the cascade stopped.
func TestCascadeSiteSuspendRestoresCascadedRows(t *testing.T) {
	store, siteID, accountIDs, channelIDs := cascadeFixture(t)
	ctx := context.Background()

	if _, _, err := store.CascadeSiteSuspend(ctx, siteID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	accounts, channels, err := store.CascadeSiteSuspend(ctx, siteID, true)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if accounts != 2 || channels != 2 {
		t.Fatalf("restored accounts=%d channels=%d, want 2 and 2", accounts, channels)
	}
	for _, id := range accountIDs {
		if !accountEnabled(t, store, id) {
			t.Errorf("account %d not restored", id)
		}
	}
	for _, id := range channelIDs {
		if !channelEnabled(t, store, id) {
			t.Errorf("channel %d not restored", id)
		}
	}
}

// The B semantics the user chose: a row the user stopped by hand before the
// cascade must stay stopped when the site comes back.
func TestCascadeSiteSuspendKeepsManualStopsStopped(t *testing.T) {
	store, siteID, accountIDs, channelIDs := cascadeFixture(t)
	ctx := context.Background()

	// User manually stops the second account and the second channel.
	manualAccount, err := store.GetSiteAccount(ctx, accountIDs[1])
	if err != nil {
		t.Fatalf("GetSiteAccount: %v", err)
	}
	manualAccount.Enabled = false
	if _, err := store.UpdateSiteAccount(ctx, manualAccount.ID, manualAccount); err != nil {
		t.Fatalf("UpdateSiteAccount: %v", err)
	}
	if _, err := store.UpdateChannelEnabled(ctx, channelIDs[1], false); err != nil {
		t.Fatalf("UpdateChannelEnabled: %v", err)
	}

	// Disable the site: only the still-enabled first pair should be marked.
	accounts, channels, err := store.CascadeSiteSuspend(ctx, siteID, false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if accounts != 1 || channels != 1 {
		t.Fatalf("cascade touched accounts=%d channels=%d, want 1 and 1 (already-off rows must be skipped)", accounts, channels)
	}

	// Re-enable: the manually stopped pair must remain off.
	if _, _, err := store.CascadeSiteSuspend(ctx, siteID, true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !accountEnabled(t, store, accountIDs[0]) {
		t.Error("cascaded account should be restored")
	}
	if !channelEnabled(t, store, channelIDs[0]) {
		t.Error("cascaded channel should be restored")
	}
	if accountEnabled(t, store, accountIDs[1]) {
		t.Error("manually stopped account must stay stopped after site re-enable")
	}
	if channelEnabled(t, store, channelIDs[1]) {
		t.Error("manually stopped channel must stay stopped after site re-enable")
	}
}

// A manual toggle during the suspension clears the mark, so the user's newer
// intent wins over the pending cascade restore.
func TestManualToggleDuringSuspensionWinsOverRestore(t *testing.T) {
	store, siteID, accountIDs, channelIDs := cascadeFixture(t)
	ctx := context.Background()

	if _, _, err := store.CascadeSiteSuspend(ctx, siteID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	// While the site is off the user turns one channel back on by hand.
	if _, err := store.UpdateChannelEnabled(ctx, channelIDs[0], true); err != nil {
		t.Fatalf("UpdateChannelEnabled: %v", err)
	}
	// And explicitly clears the mark on one account, mirroring the PATCH path.
	if err := store.ClearSiteAccountSuspendMark(ctx, accountIDs[0]); err != nil {
		t.Fatalf("ClearSiteAccountSuspendMark: %v", err)
	}

	// Re-enabling must not double-count rows whose mark is gone.
	accounts, channels, err := store.CascadeSiteSuspend(ctx, siteID, true)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if channels != 1 {
		t.Errorf("restored channels=%d, want 1 (the hand-enabled one lost its mark)", channels)
	}
	if accounts != 1 {
		t.Errorf("restored accounts=%d, want 1 (the cleared one is no longer cascade-owned)", accounts)
	}
	// The hand-enabled channel stays on either way.
	if !channelEnabled(t, store, channelIDs[0]) {
		t.Error("hand-enabled channel must stay enabled")
	}
}

// Repeating a disable is harmless and reports no further changes.
func TestCascadeSiteSuspendIsIdempotent(t *testing.T) {
	store, siteID, _, _ := cascadeFixture(t)
	ctx := context.Background()

	if _, _, err := store.CascadeSiteSuspend(ctx, siteID, false); err != nil {
		t.Fatalf("first disable: %v", err)
	}
	accounts, channels, err := store.CascadeSiteSuspend(ctx, siteID, false)
	if err != nil {
		t.Fatalf("second disable: %v", err)
	}
	if accounts != 0 || channels != 0 {
		t.Errorf("second disable changed accounts=%d channels=%d, want 0 and 0", accounts, channels)
	}
}

// A site with nothing under it must not error.
func TestCascadeSiteSuspendEmptySite(t *testing.T) {
	store, err := CreateSQLiteStore(t.TempDir() + "/empty.db")
	if err != nil {
		t.Fatalf("CreateSQLiteStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{
		Name: "Empty", Platform: model.SitePlatformNewAPIFamily,
		BaseURL: "https://empty.example.com", Enabled: true, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("CreateSite: %v", err)
	}

	accounts, channels, err := store.CascadeSiteSuspend(ctx, site.ID, false)
	if err != nil {
		t.Fatalf("CascadeSiteSuspend on empty site: %v", err)
	}
	if accounts != 0 || channels != 0 {
		t.Errorf("accounts=%d channels=%d, want 0 and 0", accounts, channels)
	}
}

// Channels belonging to a different site must never be touched.
func TestCascadeSiteSuspendDoesNotLeakAcrossSites(t *testing.T) {
	store, siteID, _, channelIDs := cascadeFixture(t)
	ctx := context.Background()

	other, err := store.CreateSite(ctx, &model.Site{
		Name: "Other Site", Platform: model.SitePlatformNewAPIFamily,
		BaseURL: "https://other.example.com", Enabled: true, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("CreateSite: %v", err)
	}
	otherAccount, err := store.CreateSiteAccount(ctx, &model.SiteAccount{
		SiteID: other.ID, Label: "other", CredentialType: model.CredentialTypeAPIKey,
		CredentialCiphertext: "sealed", Enabled: true, Status: model.SiteAccountStatusHealthy,
	})
	if err != nil {
		t.Fatalf("CreateSiteAccount: %v", err)
	}
	protocols := []string{"anthropic"}
	models := []string{"claude-opus-5"}
	apiKey := "sk-other"
	otherProjection, err := store.UpsertSiteProjection(ctx, model.SiteProjectionInput{
		SiteAccountID: otherAccount.ID, ProjectionKey: "default", Name: "Other / default",
		BaseURL: other.BaseURL, Protocols: protocols, Models: models, APIKey: apiKey,
		SourceHash: model.SiteProjectionSourceHash(other.BaseURL, protocols, models, apiKey, true),
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("UpsertSiteProjection: %v", err)
	}

	if _, _, err := store.CascadeSiteSuspend(ctx, siteID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}

	if !accountEnabled(t, store, otherAccount.ID) {
		t.Error("other site's account must not be disabled")
	}
	if !channelEnabled(t, store, otherProjection.Channel.ID) {
		t.Error("other site's channel must not be disabled")
	}
	if channelEnabled(t, store, channelIDs[0]) {
		t.Error("target site's channel should be disabled")
	}
}
