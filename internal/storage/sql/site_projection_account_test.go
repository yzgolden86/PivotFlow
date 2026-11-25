package sql_test

import (
	"context"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func TestSiteProjectionAccountCyclePreservesLifecycleIntent(t *testing.T) {
	for _, discovery := range []string{"empty", "removed"} {
		for _, block := range []string{"off", "disabled", "expired"} {
			for _, intent := range []string{"restore", "keep disabled", "explicit disable", "explicit enable", "manual"} {
				t.Run(discovery+"/"+block+"/"+intent, func(t *testing.T) {
					ctx := context.Background()
					store, account, input, channelID := newAccountLifecycleProjection(t, discovery, intent)
					blocked := *account
					setProjectionAccountBlocked(&blocked, block)
					for i := 0; i < 2; i++ {
						if _, err := store.UpdateSiteAccount(ctx, account.ID, &blocked); err != nil {
							t.Fatal(err)
						}
					}
					assertProjectionChannelHidden(t, ctx, store, channelID)
					if intent == "explicit disable" || intent == "explicit enable" {
						if _, err := store.UpdateChannelEnabled(ctx, channelID, intent == "explicit enable"); err != nil {
							t.Fatal(err)
						}
						// The account gate must disable routing without erasing the
						// intent just recorded by an explicit channel switch.
						if _, err := store.UpdateSiteAccount(ctx, account.ID, &blocked); err != nil {
							t.Fatal(err)
						}
						assertProjectionChannelHidden(t, ctx, store, channelID)
					}
					if _, err := store.UpdateSiteAccount(ctx, account.ID, account); err != nil {
						t.Fatal(err)
					}
					// Re-enabling the account alone must not activate vanished supply.
					assertProjectionChannelHidden(t, ctx, store, channelID)
					input.Models = []string{"returned-model"}
					result, err := store.UpsertSiteProjection(ctx, input)
					if err != nil {
						t.Fatal(err)
					}
					wantEnabled := intent == "restore" || intent == "explicit enable"
					if result.Channel.ID != channelID || result.Channel.Enabled != wantEnabled {
						t.Fatalf("recovery enabled=%t, want %t after %s account cycle; action=%s binding=%+v", result.Channel.Enabled, wantEnabled, block, result.Action, result.Binding)
					}
					if intent == "manual" {
						if result.Binding.Ownership != "manual" || len(result.Channel.ModelEntries) != 1 || result.Channel.ModelEntries[0].Model != "original-model" {
							t.Fatalf("manual channel overwritten: channel=%+v binding=%+v", result.Channel, result.Binding)
						}
					} else if result.Binding.Status != "active" || result.Binding.Ownership != "projected" || len(result.Channel.ModelEntries) != 1 || result.Channel.ModelEntries[0].Model != "returned-model" {
						t.Fatalf("returned supply was not projected: channel=%+v binding=%+v", result.Channel, result.Binding)
					}
				})
			}
		}
	}
}

func TestSiteProjectionReturnedSupplyWaitsForHealthyAccount(t *testing.T) {
	for _, discovery := range []string{"empty", "removed"} {
		for _, block := range []string{"off", "disabled", "expired"} {
			t.Run(discovery+"/"+block, func(t *testing.T) {
				ctx := context.Background()
				store, account, input, channelID := newAccountLifecycleProjection(t, discovery, "restore")
				blocked := *account
				setProjectionAccountBlocked(&blocked, block)
				if _, err := store.UpdateSiteAccount(ctx, account.ID, &blocked); err != nil {
					t.Fatal(err)
				}
				input.Models = []string{"returned-model"}
				for i := 0; i < 2; i++ {
					if _, err := store.UpsertSiteProjection(ctx, input); err != nil {
						t.Fatal(err)
					}
					assertProjectionChannelHidden(t, ctx, store, channelID)
				}
				if _, err := store.UpdateSiteAccount(ctx, account.ID, account); err != nil {
					t.Fatal(err)
				}
				result, err := store.UpsertSiteProjection(ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				if !result.Channel.Enabled || result.Binding.Status != "active" || result.Channel.ID != channelID || len(result.Channel.ModelEntries) != 1 || result.Channel.ModelEntries[0].Model != "returned-model" {
					t.Fatalf("healthy recovery lost restoration intent: channel=%+v binding=%+v", result.Channel, result.Binding)
				}
			})
		}
	}
}

func newAccountLifecycleProjection(t *testing.T, discovery, intent string) (storage.Store, *model.SiteAccount, model.SiteProjectionInput, int64) {
	t.Helper()
	ctx := context.Background()
	store := newTestStore(t, "projection-account-cycle.db")
	site := restoreTestCreateSite(t, ctx, store, "projection-account-cycle")
	account := restoreTestCreateAccount(t, ctx, store, site.ID, "account")
	input := model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: "key:cycle", Name: "account cycle channel",
		BaseURL: "https://upstream.example", APIKey: "account-cycle-key", Models: []string{"original-model"},
		Enabled: intent != "keep disabled" && intent != "explicit enable", Force: true}
	first, err := store.UpsertSiteProjection(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if intent == "manual" {
		if err := store.MarkSiteProjectionManual(ctx, first.Channel.ID); err != nil {
			t.Fatal(err)
		}
	}
	if discovery == "empty" {
		empty := input
		empty.Models = nil
		if _, err := store.UpsertSiteProjection(ctx, empty); err != nil {
			t.Fatal(err)
		}
	} else if err := store.DeactivateSiteProjectionsExcept(ctx, account.ID, nil); err != nil {
		t.Fatal(err)
	}
	input.Enabled = true
	return store, account, input, first.Channel.ID
}

func setProjectionAccountBlocked(account *model.SiteAccount, block string) {
	switch block {
	case "off":
		account.Enabled = false
	case "expired":
		account.Status = model.SiteAccountStatusExpired
	default:
		account.Status = model.SiteAccountStatusDisabled
	}
}

func assertProjectionChannelHidden(t *testing.T, ctx context.Context, store storage.Store, channelID int64) {
	t.Helper()
	channel, err := store.GetConfig(ctx, channelID)
	if err != nil {
		t.Fatal(err)
	}
	if channel.Enabled {
		t.Fatalf("blocked or unavailable supply remained enabled: channel=%d", channelID)
	}
	channels, err := store.GetEnabledChannelsByModel(ctx, "*")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range channels {
		if item.ID == channelID {
			t.Fatalf("blocked or unavailable supply is routable: channel=%d", channelID)
		}
	}
}
