package storage

import (
	"context"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// The upstream token's group must survive a round trip: site price tables give
// a per-group multiplier, so losing the group means the cost cannot be computed
// with the site's own ratios.
func TestSiteProjectionPersistsPricingGroup(t *testing.T) {
	store, err := CreateSQLiteStore(t.TempDir() + "/pricing_group.db")
	if err != nil {
		t.Fatalf("CreateSQLiteStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{
		Name: "Group Site", Platform: model.SitePlatformNewAPIFamily,
		BaseURL: "https://group.example.com", Enabled: true, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("CreateSite: %v", err)
	}
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{
		SiteID: site.ID, Label: "main", CredentialType: model.CredentialTypeAPIKey,
		CredentialCiphertext: "sealed", Enabled: true, Status: model.SiteAccountStatusHealthy,
	})
	if err != nil {
		t.Fatalf("CreateSiteAccount: %v", err)
	}

	protocols := []string{"anthropic"}
	models := []string{"claude-opus-5"}
	apiKey := "sk-group"
	result, err := store.UpsertSiteProjection(ctx, model.SiteProjectionInput{
		SiteAccountID: account.ID, ProjectionKey: "key:vip", Name: "Group / vip",
		BaseURL: site.BaseURL, Protocols: protocols, Models: models, APIKey: apiKey,
		SourceHash:   model.SiteProjectionSourceHash(site.BaseURL, protocols, models, apiKey, true),
		PricingGroup: "vip", Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpsertSiteProjection: %v", err)
	}
	if result.Binding.PricingGroup != "vip" {
		t.Errorf("returned binding PricingGroup=%q, want vip", result.Binding.PricingGroup)
	}

	bindings, err := store.ListSiteChannelBindings(ctx)
	if err != nil {
		t.Fatalf("ListSiteChannelBindings: %v", err)
	}
	var found bool
	for _, binding := range bindings {
		if binding.ChannelID == result.Channel.ID {
			found = true
			if binding.PricingGroup != "vip" {
				t.Errorf("persisted PricingGroup=%q, want vip", binding.PricingGroup)
			}
		}
	}
	if !found {
		t.Fatal("binding for the projected channel was not listed")
	}

	// Re-projecting with a changed group must update, not keep the stale value:
	// moving a token to another group changes its real price.
	updated, err := store.UpsertSiteProjection(ctx, model.SiteProjectionInput{
		SiteAccountID: account.ID, ProjectionKey: "key:vip", Name: "Group / vip",
		BaseURL: site.BaseURL, Protocols: protocols, Models: models, APIKey: apiKey,
		SourceHash:   model.SiteProjectionSourceHash(site.BaseURL, protocols, models, apiKey, true),
		PricingGroup: "svip", Enabled: true, Force: true,
	})
	if err != nil {
		t.Fatalf("re-project: %v", err)
	}
	if updated.Binding.PricingGroup != "svip" {
		t.Errorf("PricingGroup after re-project=%q, want svip", updated.Binding.PricingGroup)
	}
}

// An empty group is legitimate (site exposes no groups) and must not error.
func TestSiteProjectionAcceptsEmptyPricingGroup(t *testing.T) {
	store, err := CreateSQLiteStore(t.TempDir() + "/pricing_group_empty.db")
	if err != nil {
		t.Fatalf("CreateSQLiteStore: %v", err)
	}
	defer func() { _ = store.Close() }()

	ctx := context.Background()
	site, err := store.CreateSite(ctx, &model.Site{
		Name: "Plain", Platform: model.SitePlatformNewAPIFamily,
		BaseURL: "https://plain.example.com", Enabled: true, Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("CreateSite: %v", err)
	}
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{
		SiteID: site.ID, Label: "main", CredentialType: model.CredentialTypeAPIKey,
		CredentialCiphertext: "sealed", Enabled: true, Status: model.SiteAccountStatusHealthy,
	})
	if err != nil {
		t.Fatalf("CreateSiteAccount: %v", err)
	}

	protocols := []string{"anthropic"}
	models := []string{"m"}
	apiKey := "sk-plain"
	result, err := store.UpsertSiteProjection(ctx, model.SiteProjectionInput{
		SiteAccountID: account.ID, ProjectionKey: "default", Name: "Plain / default",
		BaseURL: site.BaseURL, Protocols: protocols, Models: models, APIKey: apiKey,
		SourceHash: model.SiteProjectionSourceHash(site.BaseURL, protocols, models, apiKey, true),
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("UpsertSiteProjection: %v", err)
	}
	if result.Binding.PricingGroup != "" {
		t.Errorf("PricingGroup=%q, want empty", result.Binding.PricingGroup)
	}
}
