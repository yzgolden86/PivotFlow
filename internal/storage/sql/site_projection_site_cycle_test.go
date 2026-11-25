package sql_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

func TestSiteProjectionSiteCyclePreservesUnavailableSupplyAndIntent(t *testing.T) {
	for _, discovery := range []string{"removed", "empty"} {
		for _, tc := range []struct {
			name             string
			initial, want    bool
			toggle           *bool
			toggleBeforeSync bool
		}{
			{name: "enabled", initial: true, want: true},
			{name: "deliberately disabled", initial: false, want: false},
			{name: "disable before sync", initial: true, toggle: boolPointer(false), toggleBeforeSync: true, want: false},
			{name: "enable before sync", initial: false, toggle: boolPointer(true), toggleBeforeSync: true, want: true},
			{name: "disable after sync", initial: true, toggle: boolPointer(false), want: false},
			{name: "enable after sync", initial: false, toggle: boolPointer(true), want: true},
		} {
			t.Run(discovery+"/"+tc.name, func(t *testing.T) {
				ctx := context.Background()
				store := newTestStore(t, "projection-site-cycle.db")
				site := restoreTestCreateSite(t, ctx, store, "projection-site-cycle")
				account := restoreTestCreateAccount(t, ctx, store, site.ID, "account")
				input := model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: "key:site-cycle",
					BaseURL: "https://upstream.example", APIKey: "site-cycle-key", Models: []string{"original-model"},
					Enabled: tc.initial, Force: true}
				first, err := store.UpsertSiteProjection(ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				deactivate := func() {
					t.Helper()
					if discovery == "removed" {
						if err := store.DeactivateSiteProjectionsExcept(ctx, account.ID, nil); err != nil {
							t.Fatal(err)
						}
					} else {
						empty := input
						empty.Models = nil
						if _, err := store.UpsertSiteProjection(ctx, empty); err != nil {
							t.Fatal(err)
						}
					}
				}
				for cycle := 0; cycle < 2; cycle++ {
					setProjectionSiteCycleEnabled(t, ctx, store, site.ID, false)
					if cycle == 0 && tc.toggle != nil && tc.toggleBeforeSync {
						if _, err := store.UpdateChannelEnabled(ctx, first.Channel.ID, *tc.toggle); err != nil {
							t.Fatal(err)
						}
					}
					deactivate()
					deactivate()
					if cycle == 0 && tc.toggle != nil && !tc.toggleBeforeSync {
						if _, err := store.UpdateChannelEnabled(ctx, first.Channel.ID, *tc.toggle); err != nil {
							t.Fatal(err)
						}
						deactivate()
					}
					// Account refresh finalization must not discard the saved intent.
					blocked, err := store.GetSiteAccount(ctx, account.ID)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := store.UpdateSiteAccount(ctx, account.ID, blocked); err != nil {
						t.Fatal(err)
					}
					assertProjectionChannelHidden(t, ctx, store, first.Channel.ID)
					channel, err := store.GetConfig(ctx, first.Channel.ID)
					if err != nil {
						t.Fatal(err)
					}
					if len(channel.ModelEntries) != 0 {
						t.Errorf("cycle %d: vanished routing models retained: %v", cycle, channel.GetModels())
					}
					// Returning supply while the site is off cannot consume recovery intent.
					input.Models, input.Enabled = []string{"returned-model"}, true
					if _, err := store.UpsertSiteProjection(ctx, input); err != nil {
						t.Fatal(err)
					}
					setProjectionSiteCycleEnabled(t, ctx, store, site.ID, true)
					if _, _, err := store.CascadeSiteSuspend(ctx, site.ID, true); err != nil {
						t.Fatal(err)
					}
					assertProjectionChannelHidden(t, ctx, store, first.Channel.ID)
					result, err := store.UpsertSiteProjection(ctx, input)
					if err != nil {
						t.Fatal(err)
					}
					if result.Channel.ID != first.Channel.ID || result.Channel.Enabled != tc.want ||
						result.Binding.Status != "active" || !reflect.DeepEqual(result.Channel.GetModels(), []string{"returned-model"}) {
						t.Fatalf("cycle %d: recovery lost enabled intent, want %t: channel=%+v binding=%+v", cycle, tc.want, result.Channel, result.Binding)
					}
				}
			})
		}
	}
}

func TestSiteProjectionSiteCycleDoesNotRestorePersistedInactiveCascadeMark(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t, "projection-inactive-cascade.db")
	ss := store.(*sqlstore.SQLStore)
	site := restoreTestCreateSite(t, ctx, store, "inactive-cascade")
	account := restoreTestCreateAccount(t, ctx, store, site.ID, "account")
	first, err := store.UpsertSiteProjection(ctx, model.SiteProjectionInput{SiteAccountID: account.ID,
		ProjectionKey: "key:inactive", BaseURL: "https://upstream.example", APIKey: "inactive-key",
		Models: []string{"old-model"}, Enabled: true, Force: true})
	if err != nil {
		t.Fatal(err)
	}
	setProjectionSiteCycleEnabled(t, ctx, store, site.ID, false)
	// Reproduce a persisted inactive binding left with a cascade mark by an older sync.
	if _, err := ss.ExecContext(ctx, "UPDATE site_channel_bindings SET status='inactive',last_sync_error='upstream_removed_restore_enabled' WHERE channel_id=?", first.Channel.ID); err != nil {
		t.Fatal(err)
	}
	setProjectionSiteCycleEnabled(t, ctx, store, site.ID, true)
	assertProjectionChannelHidden(t, ctx, store, first.Channel.ID)
}

func TestSiteProjectionPersistedIntentSurvivesBothSiteRestoreOrderings(t *testing.T) {
	for _, marker := range []string{
		"upstream_removed_keep_disabled", "upstream_removed_restore_enabled",
		"upstream_empty_keep_disabled", "upstream_empty_restore_enabled",
		"upstream key removed or disabled", "provider_timeout", "",
	} {
		for _, deactivateFirst := range []bool{false, true} {
			for _, explicitDisable := range []bool{false, true} {
				name := marker + "/site-enable-first"
				if deactivateFirst {
					name = marker + "/deactivate-first"
				}
				if explicitDisable {
					name += "/explicit-disable"
				}
				t.Run(name, func(t *testing.T) {
					ctx := context.Background()
					store, siteID, input, channelID := newPersistedProjectionIntent(t, marker)
					if explicitDisable {
						if _, err := store.UpdateChannelEnabled(ctx, channelID, false); err != nil {
							t.Fatal(err)
						}
					}
					if deactivateFirst {
						for i := 0; i < 2; i++ {
							if err := store.DeactivateSiteProjectionsExcept(ctx, input.SiteAccountID, nil); err != nil {
								t.Fatal(err)
							}
						}
					}
					setProjectionSiteCycleEnabled(t, ctx, store, siteID, true)
					if _, _, err := store.CascadeSiteSuspend(ctx, siteID, true); err != nil {
						t.Fatal(err)
					}
					assertProjectionChannelHidden(t, ctx, store, channelID)
					var suspended bool
					if err := store.(*sqlstore.SQLStore).QueryRowContext(ctx, "SELECT suspended_by_site FROM channels WHERE id=?", channelID).Scan(&suspended); err != nil {
						t.Fatal(err)
					}
					if suspended {
						t.Fatal("site restore retained the consumed cascade flag")
					}
					if !explicitDisable {
						wantMarker := "upstream_removed_restore_enabled"
						if marker == "upstream_empty_keep_disabled" || marker == "upstream_empty_restore_enabled" {
							wantMarker = "upstream_empty_restore_enabled"
						}
						assertSiteProjectionMarker(t, ctx, store, input.SiteAccountID, input.ProjectionKey, wantMarker)
					}
					input.Models = []string{"returned-model"}
					for i := 0; i < 2; i++ {
						result, err := store.UpsertSiteProjection(ctx, input)
						if err != nil {
							t.Fatal(err)
						}
						if result.Channel.ID != channelID || result.Channel.Enabled != !explicitDisable ||
							result.Binding.Status != "active" || !reflect.DeepEqual(result.Channel.GetModels(), []string{"returned-model"}) {
							t.Fatalf("persisted intent lost on supply return: enabled=%t want=%t binding=%+v", result.Channel.Enabled, !explicitDisable, result.Binding)
						}
					}
				})
			}
		}
	}
}

func TestSiteProjectionPersistedIntentTransferRollsBack(t *testing.T) {
	for _, failure := range []string{"binding update", "channel update", "account update"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			store, siteID, input, channelID := newPersistedProjectionIntent(t, "upstream_empty_keep_disabled")
			ss := store.(*sqlstore.SQLStore)
			beforeBinding, err := store.GetSiteChannelBinding(ctx, input.SiteAccountID, input.ProjectionKey)
			if err != nil {
				t.Fatal(err)
			}
			beforeAccount, err := store.GetSiteAccount(ctx, input.SiteAccountID)
			if err != nil {
				t.Fatal(err)
			}
			var trigger string
			switch failure {
			case "binding update":
				trigger = `CREATE TRIGGER reject_intent_transfer BEFORE UPDATE ON site_channel_bindings
					WHEN NEW.last_sync_error='upstream_empty_restore_enabled'
					BEGIN SELECT RAISE(ABORT,'injected intent transfer failure'); END`
			case "channel update":
				trigger = `CREATE TRIGGER reject_intent_channel BEFORE UPDATE ON channels
					WHEN NEW.suspended_by_site=0
					BEGIN SELECT RAISE(ABORT,'injected channel failure'); END`
			case "account update":
				trigger = `CREATE TRIGGER reject_intent_account BEFORE UPDATE ON site_accounts
					WHEN NEW.suspended_by_site=0
					BEGIN SELECT RAISE(ABORT,'injected account failure'); END`
			}
			if _, err := ss.ExecContext(ctx, trigger); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.CascadeSiteSuspend(ctx, siteID, true); err == nil {
				t.Fatal("site restore ignored injected write failure")
			}
			binding, err := store.GetSiteChannelBinding(ctx, input.SiteAccountID, input.ProjectionKey)
			if err != nil {
				t.Fatal(err)
			}
			account, err := store.GetSiteAccount(ctx, input.SiteAccountID)
			if err != nil {
				t.Fatal(err)
			}
			var enabled, suspended bool
			if err := ss.QueryRowContext(ctx, "SELECT enabled,suspended_by_site FROM channels WHERE id=?", channelID).Scan(&enabled, &suspended); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(binding, beforeBinding) || !reflect.DeepEqual(account, beforeAccount) || enabled || !suspended {
				t.Fatalf("failed site restore consumed intent or cascade state: binding=%+v enabled=%t suspended=%t", binding, enabled, suspended)
			}
		})
	}
}

func newPersistedProjectionIntent(t *testing.T, marker string) (storage.Store, int64, model.SiteProjectionInput, int64) {
	t.Helper()
	ctx := context.Background()
	store := newTestStore(t, "projection-persisted-intent.db")
	site := restoreTestCreateSite(t, ctx, store, "persisted-intent")
	account := restoreTestCreateAccount(t, ctx, store, site.ID, "account")
	input := model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: "key:persisted",
		BaseURL: "https://upstream.example", APIKey: "persisted-intent-key", Models: []string{"old-model"}, Enabled: true, Force: true}
	first, err := store.UpsertSiteProjection(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	setProjectionSiteCycleEnabled(t, ctx, store, site.ID, false)
	// Older refreshes could mark a suspended projection inactive without
	// transferring the cascade's enabled intent into last_sync_error.
	if _, err := store.(*sqlstore.SQLStore).ExecContext(ctx,
		"UPDATE site_channel_bindings SET status='inactive',last_sync_error=? WHERE channel_id=?", marker, first.Channel.ID); err != nil {
		t.Fatal(err)
	}
	return store, site.ID, input, first.Channel.ID
}

func TestSiteProjectionRemovedModelsAreAtomicAndHistorySafe(t *testing.T) {
	for _, failure := range []string{"none", "model delete", "binding update"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			store := newTestStore(t, "projection-removed-models.db")
			ss := store.(*sqlstore.SQLStore)
			site := restoreTestCreateSite(t, ctx, store, "removed-models")
			account := restoreTestCreateAccount(t, ctx, store, site.ID, "account")
			input := model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: "key:removed",
				BaseURL: "https://upstream.example", APIKey: "removed-key", Models: []string{"old-model"}, Enabled: true, Force: true}
			first, err := store.UpsertSiteProjection(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			cfg := first.Channel.Clone()
			cfg.Priority, cfg.ScheduledCheckEnabled, cfg.ScheduledCheckModel = 17, true, "user-scheduled-model"
			before, err := store.UpdateConfig(ctx, cfg.ID, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateAPIKeyNotes(ctx, cfg.ID, map[int]string{0: "retained note"}); err != nil {
				t.Fatal(err)
			}
			beforeKeys, err := store.GetAPIKeys(ctx, cfg.ID)
			if err != nil {
				t.Fatal(err)
			}
			beforeBinding, err := store.GetSiteChannelBinding(ctx, account.ID, input.ProjectionKey)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			if err := store.AddLog(ctx, &model.LogEntry{Time: model.JSONTime{Time: now}, Model: "old-model",
				ChannelID: cfg.ID, StatusCode: 200, Cost: 0.125, CostMultiplier: 1, InputTokens: 11, RequestID: "removed-history"}); err != nil {
				t.Fatal(err)
			}
			if err := store.BatchAddRequestTraces(ctx, []*model.RequestTrace{{RequestID: "removed-history",
				StartedAt: now.UnixMilli(), Model: "old-model", ActualModel: "old-model", ChannelID: cfg.ID,
				ChannelName: "historical channel", ClientProtocol: "openai", Transport: "http", FinalStatus: 200,
				Outcome: "success", UpstreamAttemptCount: 1, UpstreamSuccessCount: 1}}); err != nil {
				t.Fatal(err)
			}
			input.ProjectionKey = "key:manual"
			manual, err := store.UpsertSiteProjection(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkSiteProjectionManual(ctx, manual.Channel.ID); err != nil {
				t.Fatal(err)
			}
			input.ProjectionKey = "key:survivor"
			survivor, err := store.UpsertSiteProjection(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "model delete":
				_, err = ss.ExecContext(ctx, `CREATE TRIGGER reject_removed_models BEFORE DELETE ON channel_models
					BEGIN SELECT RAISE(ABORT,'injected model delete failure'); END`)
			case "binding update":
				_, err = ss.ExecContext(ctx, `CREATE TRIGGER reject_removed_binding BEFORE UPDATE ON site_channel_bindings
					WHEN NEW.status='inactive' BEGIN SELECT RAISE(ABORT,'injected binding failure'); END`)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = store.DeactivateSiteProjectionsExcept(ctx, account.ID, []string{"key:survivor"})
			if (err != nil) != (failure != "none") {
				t.Errorf("deactivation error=%v, injected failure=%s", err, failure)
			}
			if failure == "none" {
				if err := store.DeactivateSiteProjectionsExcept(ctx, account.ID, []string{"key:survivor"}); err != nil {
					t.Fatal(err)
				}
			}
			after, err := store.GetConfig(ctx, cfg.ID)
			if err != nil {
				t.Fatal(err)
			}
			binding, err := store.GetSiteChannelBinding(ctx, account.ID, "key:removed")
			if err != nil {
				t.Fatal(err)
			}
			if failure == "none" {
				if after.Enabled || len(after.ModelEntries) != 0 || binding.Status != "inactive" {
					t.Errorf("removed routing supply retained: channel=%+v binding=%+v", after, binding)
				}
			} else if after.Enabled != before.Enabled || !reflect.DeepEqual(after.ModelEntries, before.ModelEntries) || !reflect.DeepEqual(binding, beforeBinding) {
				t.Errorf("failed deactivation did not roll back routing state: channel=%+v binding=%+v", after, binding)
			}
			if after.Name != before.Name || after.Priority != 17 || !after.ScheduledCheckEnabled || after.ScheduledCheckModel != "user-scheduled-model" || !reflect.DeepEqual(after.URLs, before.URLs) {
				t.Errorf("deactivation changed local channel configuration: %+v", after)
			}
			keys, err := store.GetAPIKeys(ctx, cfg.ID)
			if err != nil || !reflect.DeepEqual(keys, beforeKeys) {
				t.Errorf("deactivation changed key metadata: err=%v", err)
			}
			logs, err := store.ListLogs(ctx, now.Add(-time.Minute), 10, 0, &model.LogFilter{ChannelID: &cfg.ID})
			if err != nil || len(logs) != 1 || logs[0].Cost != 0.125 || logs[0].InputTokens != 11 || logs[0].RequestID != "removed-history" {
				t.Errorf("deactivation changed history: logs=%+v err=%v", logs, err)
			}
			trace, err := store.GetRequestTrace(ctx, "removed-history", nil)
			if err != nil || trace.ChannelID != cfg.ID || trace.ChannelName != "historical channel" {
				t.Errorf("deactivation changed trace: trace=%+v err=%v", trace, err)
			}
			for _, id := range []int64{manual.Channel.ID, survivor.Channel.ID} {
				kept, err := store.GetConfig(ctx, id)
				if err != nil || !kept.Enabled || !reflect.DeepEqual(kept.GetModels(), []string{"old-model"}) {
					t.Errorf("manual or surviving projection changed: channel=%+v err=%v", kept, err)
				}
			}
		})
	}
}

func setProjectionSiteCycleEnabled(t *testing.T, ctx context.Context, store storage.Store, siteID int64, enabled bool) {
	t.Helper()
	site, err := store.GetSite(ctx, siteID)
	if err != nil {
		t.Fatal(err)
	}
	site.Enabled = enabled
	if _, err := store.UpdateSite(ctx, siteID, site); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CascadeSiteSuspend(ctx, siteID, enabled); err != nil {
		t.Fatal(err)
	}
}
