package sql_test

import (
	"context"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func TestSiteProjectionRemovalMarkerTracksExplicitToggleAndManualTakeover(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, "site-projection-markers.db")
	site := restoreTestCreateSite(t, ctx, store, "projection-markers")
	account := restoreTestCreateAccount(t, ctx, store, site.ID, "account")
	input := model.SiteProjectionInput{
		SiteAccountID: account.ID,
		ProjectionKey: "key:marker",
		BaseURL:       "https://upstream.example",
		Protocols:     []string{"openai"},
		Models:        []string{"old-model"},
		APIKey:        "marker-key",
		Enabled:       true,
		Force:         true,
	}
	projected, err := store.UpsertSiteProjection(ctx, input)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.DeactivateSiteProjectionsExcept(ctx, account.ID, nil); err != nil {
		t.Fatal(err)
	}
	assertSiteProjectionMarker(t, ctx, store, account.ID, "key:marker", "upstream_removed_restore_enabled")

	if _, err := store.UpdateChannelEnabled(ctx, projected.Channel.ID, false); err != nil {
		t.Fatal(err)
	}
	assertSiteProjectionMarker(t, ctx, store, account.ID, "key:marker", "upstream_removed_keep_disabled")
	if err := store.DeactivateSiteProjectionsExcept(ctx, account.ID, nil); err != nil {
		t.Fatal(err)
	}
	assertSiteProjectionMarker(t, ctx, store, account.ID, "key:marker", "upstream_removed_keep_disabled")

	if _, err := store.UpdateChannelEnabled(ctx, projected.Channel.ID, true); err != nil {
		t.Fatal(err)
	}
	assertSiteProjectionMarker(t, ctx, store, account.ID, "key:marker", "upstream_removed_restore_enabled")
	if err := store.DeactivateSiteProjectionsExcept(ctx, account.ID, nil); err != nil {
		t.Fatal(err)
	}
	assertSiteProjectionMarker(t, ctx, store, account.ID, "key:marker", "upstream_removed_restore_enabled")

	input.Models = []string{"recovered-model"}
	recovered, err := store.UpsertSiteProjection(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.Channel.Enabled || recovered.Binding.Status != "active" {
		t.Fatalf("explicit enable was not restored: channel=%+v binding=%+v", recovered.Channel, recovered.Binding)
	}

	manualInput := input
	manualInput.ProjectionKey = "key:manual"
	manualInput.Models = []string{"manual-model"}
	manual, err := store.UpsertSiteProjection(ctx, manualInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateChannelEnabled(ctx, manual.Channel.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := store.DeactivateSiteProjectionsExcept(ctx, account.ID, []string{"key:marker"}); err != nil {
		t.Fatal(err)
	}
	manualConfig, err := store.GetConfig(ctx, manual.Channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(manualConfig.ModelEntries) != 0 {
		t.Fatalf("removed upstream key retained routing models: %+v", manualConfig.ModelEntries)
	}
	manualConfig.Name = "manually owned channel"
	manualConfig.ModelEntries = []model.ModelEntry{{Model: "manual-model"}}
	if _, err := store.UpdateConfig(ctx, manual.Channel.ID, manualConfig); err != nil {
		t.Fatal(err)
	}
	manualBinding, err := store.GetSiteChannelBinding(ctx, account.ID, "key:manual")
	if err != nil {
		t.Fatal(err)
	}
	if manualBinding.Ownership != "manual" {
		t.Fatalf("generic config update did not take ownership: %+v", manualBinding)
	}
	manualInput.Models = []string{"must-not-overwrite"}
	manualResult, err := store.UpsertSiteProjection(ctx, manualInput)
	if err != nil || manualResult.Action != "conflict" || manualResult.Binding.Ownership != "manual" {
		t.Fatalf("automatic rehydration did not respect manual binding: result=%+v err=%v", manualResult, err)
	}
	manualAfter, err := store.GetConfig(ctx, manual.Channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(manualAfter.ModelEntries) != 1 || manualAfter.ModelEntries[0].Model != "manual-model" || manualAfter.Enabled {
		t.Fatalf("manual channel changed during automatic rehydration: %+v", manualAfter)
	}
}

func assertSiteProjectionMarker(t *testing.T, ctx context.Context, store storage.Store, accountID int64, projectionKey, want string) {
	t.Helper()
	binding, err := store.GetSiteChannelBinding(ctx, accountID, projectionKey)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Status != "inactive" || binding.LastSyncError != want {
		t.Fatalf("binding marker=%+v, want inactive/%q", binding, want)
	}
}

func TestSiteProjectionInactiveRecoveryHonorsLatestEnabledIntent(t *testing.T) {
	for _, tc := range []struct {
		name          string
		initial       bool
		explicit      *bool
		genericToggle bool
		wantEnabled   bool
	}{
		{"removed enabled to empty", true, nil, false, true},
		{"removed disabled to empty", false, nil, false, false},
		{"explicit disable", true, boolPointer(false), false, false},
		{"explicit enable", false, boolPointer(true), false, true},
		{"generic explicit enable", false, boolPointer(true), true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := newTestStore(t, "projection-recovery.db")
			site := restoreTestCreateSite(t, ctx, store, "recovery")
			account := restoreTestCreateAccount(t, ctx, store, site.ID, "account")
			input := model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: "key:recovery",
				BaseURL: "https://upstream.example", APIKey: "recovery-key", Models: []string{"old"}, Enabled: tc.initial, Force: true}
			first, err := store.UpsertSiteProjection(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.DeactivateSiteProjectionsExcept(ctx, account.ID, nil); err != nil {
				t.Fatal(err)
			}
			if tc.explicit != nil {
				if tc.genericToggle {
					cfg, err := store.GetConfig(ctx, first.Channel.ID)
					if err != nil {
						t.Fatal(err)
					}
					cfg.Enabled = *tc.explicit
					_, err = store.UpdateConfig(ctx, cfg.ID, cfg)
					if err != nil {
						t.Fatal(err)
					}
				} else if _, err := store.UpdateChannelEnabled(ctx, first.Channel.ID, *tc.explicit); err != nil {
					t.Fatal(err)
				}
			}
			// A metadata-only update must keep the upstream restoration intent.
			cfg, err := store.GetConfig(ctx, first.Channel.ID)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Priority = 17
			if _, err := store.UpdateConfig(ctx, cfg.ID, cfg); err != nil {
				t.Fatal(err)
			}
			if err := store.DeactivateSiteProjectionsExcept(ctx, account.ID, nil); err != nil {
				t.Fatal(err)
			}
			input.Models = nil
			if _, err := store.UpsertSiteProjection(ctx, input); err != nil {
				t.Fatal(err)
			}
			if _, err := store.UpsertSiteProjection(ctx, input); err != nil {
				t.Fatal(err)
			}
			input.Models, input.Enabled = []string{"recovered"}, true
			recovered, err := store.UpsertSiteProjection(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if recovered.Channel.Enabled != tc.wantEnabled || recovered.Channel.Priority != 17 || recovered.Binding.Ownership != "projected" || recovered.Binding.Status != "active" {
				t.Fatalf("recovery=%+v binding=%+v, want enabled=%t with projected ownership and retained priority", recovered.Channel, recovered.Binding, tc.wantEnabled)
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }
