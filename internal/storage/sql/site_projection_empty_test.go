package sql_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

func TestSiteProjectionEmptyModelsAtomicAndRecoverable(t *testing.T) {
	for _, state := range []string{"enabled", "disabled", "manual", "write failure"} {
		t.Run(state, func(t *testing.T) {
			store := newTestStore(t, "empty-projection.db")
			ss := store.(*sqlstore.SQLStore)
			ctx := context.Background()
			site := restoreTestCreateSite(t, ctx, store, "empty-projection")
			account := restoreTestCreateAccount(t, ctx, store, site.ID, "account")
			input := model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: "key:empty", BaseURL: "https://upstream.example",
				Protocols: []string{"openai"}, Models: []string{"old-model"}, APIKey: "sk-empty-projection", Enabled: true, Force: true}
			first, err := store.UpsertSiteProjection(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			id := first.Channel.ID
			if state == "disabled" {
				if _, err := store.UpdateChannelEnabled(ctx, id, false); err != nil {
					t.Fatal(err)
				}
			}
			if state == "manual" {
				if err := store.MarkSiteProjectionManual(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.UpdateAPIKeyNotes(ctx, id, map[int]string{0: "retain me"}); err != nil {
				t.Fatal(err)
			}
			beforeKeys, err := store.GetAPIKeys(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			beforeBinding, err := store.GetSiteChannelBinding(ctx, account.ID, input.ProjectionKey)
			if err != nil {
				t.Fatal(err)
			}
			if state == "write failure" {
				if _, err := ss.ExecContext(ctx, `CREATE TRIGGER reject_empty_binding BEFORE UPDATE ON site_channel_bindings
					WHEN NEW.status='inactive' BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			input.Models = nil
			empty, err := store.UpsertSiteProjection(ctx, input)
			if state == "write failure" && err == nil || state != "write failure" && err != nil {
				t.Fatalf("empty result=%+v err=%v", empty, err)
			}
			channel, err := store.GetConfig(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			binding, err := store.GetSiteChannelBinding(ctx, account.ID, input.ProjectionKey)
			if err != nil {
				t.Fatal(err)
			}
			keys, err := store.GetAPIKeys(ctx, id)
			if err != nil || !reflect.DeepEqual(keys, beforeKeys) {
				t.Fatalf("key metadata changed: %+v err=%v", keys, err)
			}
			if state == "manual" || state == "write failure" {
				if len(channel.ModelEntries) != 1 || !channel.Enabled || binding.Ownership != beforeBinding.Ownership || binding.Status != beforeBinding.Status {
					t.Fatalf("preserved/rolled back channel=%+v binding=%+v", channel, binding)
				}
				return
			}
			if channel.Enabled || len(channel.ModelEntries) != 0 || binding.Status != "inactive" || binding.Ownership != "projected" {
				t.Fatalf("empty channel=%+v binding=%+v", channel, binding)
			}
			input.Models = []string{"new-model"}
			recovered, err := store.UpsertSiteProjection(ctx, input)
			if err != nil || recovered.Channel.ID != id || recovered.Channel.Enabled != (state == "enabled") || recovered.Binding.Status != "active" {
				t.Fatalf("recovery=%+v err=%v, prior state=%s", recovered, err, state)
			}
		})
	}
}

func TestSiteProjectionRejectsEmptyCreation(t *testing.T) {
	store := newTestStore(t, "empty-create.db")
	ctx := context.Background()
	site := restoreTestCreateSite(t, ctx, store, "empty-create")
	account := restoreTestCreateAccount(t, ctx, store, site.ID, "account")
	_, err := store.UpsertSiteProjection(ctx, model.SiteProjectionInput{SiteAccountID: account.ID,
		ProjectionKey: "key:unknown", BaseURL: "https://upstream.example", APIKey: "sk-unknown", Enabled: true})
	if err == nil {
		t.Fatal("empty creation succeeded")
	}
	channels, err := store.ListConfigs(ctx)
	if err != nil || len(channels) != 0 {
		t.Fatalf("empty creation mutated topology=%+v err=%v", channels, err)
	}
}
