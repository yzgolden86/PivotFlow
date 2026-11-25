package app

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func TestModelRefreshRouteProgressAttributionAndTerminalPreservation(t *testing.T) {
	keys := []provider.RoutingKeySnapshot{
		{ID: "a", Name: "alpha", Key: "private-alpha-key", Enabled: true},
		{ID: "b", Name: "beta", Key: "private-beta-key", Enabled: true},
		{ID: "c", Name: "gamma", Key: "private-gamma-key", Enabled: true},
	}
	adapter := siteSyncPreviewAdapter(keys, map[string][]string{
		"private-alpha-key": {"alpha-model"}, "private-beta-key": {"beta-model"}, "private-gamma-key": {"gamma-model"},
	})
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	ctx := context.Background()
	initial := newSiteTask("model_refresh", account.SiteID, account.ID, 1)
	srv.siteControl.refreshAccount(ctx, initial, account.ID, true)
	if initial.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("initial refresh=%+v", initial)
	}
	names := map[string]string{}
	for _, key := range []string{"a", "b", "c"} {
		binding, err := srv.store.GetSiteChannelBinding(ctx, account.ID, "key:"+key)
		if err != nil {
			t.Fatal(err)
		}
		channel, err := srv.store.GetConfig(ctx, binding.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		names[key] = channel.Name
	}
	adapter.errorsByKey["private-alpha-key"] = &provider.Error{Code: provider.CodeExpired, Message: "private-alpha-key raw-alpha-body"}
	adapter.errorsByKey["private-beta-key"] = &provider.Error{Code: provider.CodeRateLimited, Message: "private-beta-key raw-beta-body"}
	adapter.modelsByKey["private-gamma-key"] = []string{"updated-gamma-model"}
	final := newSiteTask("model_refresh", account.SiteID, account.ID, 11)
	final.ProgressJSON = `{"completed":7,"total":11,"extra":"preserved"}`
	if err := srv.store.CreateSiteTask(ctx, final); err != nil {
		t.Fatal(err)
	}
	srv.siteControl.refreshAccount(ctx, final, account.ID, true)
	if final.Status != model.SiteTaskStatusPartial {
		t.Fatalf("final refresh=%+v", final)
	}
	stored, err := srv.store.GetSiteTask(ctx, final.ID)
	if err != nil {
		t.Fatal(err)
	}
	var progress struct {
		Completed int    `json:"completed"`
		Total     int    `json:"total"`
		Extra     string `json:"extra"`
		Routes    []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Error  string `json:"error"`
		} `json:"routes"`
	}
	if err := json.Unmarshal([]byte(stored.ProgressJSON), &progress); err != nil {
		t.Fatal(err)
	}
	if progress.Completed != 7 || progress.Total != 11 || progress.Extra != "preserved" || len(progress.Routes) != 3 {
		t.Fatalf("terminal progress lost counters or routes: %s", stored.ProgressJSON)
	}
	got := map[string][2]string{}
	for _, route := range progress.Routes {
		got[route.Name] = [2]string{route.Status, route.Error}
	}
	want := map[string][2]string{
		names["a"]: {"failed", provider.CodeRequestFailed},
		names["b"]: {"failed", provider.CodeRateLimited},
		names["c"]: {"updated", ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("route attribution=%v, want %v", got, want)
	}
	for _, forbidden := range []string{"private-alpha-key", "private-beta-key", "private-gamma-key", "raw-alpha-body", "raw-beta-body"} {
		if strings.Contains(stored.ProgressJSON, forbidden) {
			t.Fatalf("route progress exposed private discovery material: %s", stored.ProgressJSON)
		}
	}
	adapter.errorsByKey["private-gamma-key"] = &provider.Error{Code: provider.CodeInvalidResponse, Message: "raw-gamma-body"}
	allFailed := newSiteTask("model_refresh", account.SiteID, account.ID, 1)
	srv.siteControl.refreshAccount(ctx, allFailed, account.ID, true)
	if allFailed.Status != model.SiteTaskStatusFailed {
		t.Fatalf("all-failed status=%s", allFailed.Status)
	}
	if err := json.Unmarshal([]byte(allFailed.ProgressJSON), &progress); err != nil || len(progress.Routes) != 3 {
		t.Fatalf("all-failed discovery lost known channel outcomes: %s err=%v", allFailed.ProgressJSON, err)
	}
}

func TestModelRefreshRouteProgressEmptyAndRemovedChannels(t *testing.T) {
	for _, mode := range []string{"empty", "removed", "manual empty"} {
		t.Run(mode, func(t *testing.T) {
			adapter := siteSyncPreviewAdapter([]provider.RoutingKeySnapshot{{ID: "route", Key: "private-route-key", Enabled: true}},
				map[string][]string{"private-route-key": {"old-model"}})
			srv, _, account := newSiteSyncPreviewFixture(t, adapter)
			ctx := context.Background()
			initial := newSiteTask("model_refresh", account.SiteID, account.ID, 1)
			srv.siteControl.refreshAccount(ctx, initial, account.ID, true)
			binding, err := srv.store.GetSiteChannelBinding(ctx, account.ID, "key:route")
			if err != nil {
				t.Fatal(err)
			}
			channel, err := srv.store.GetConfig(ctx, binding.ChannelID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "manual empty" {
				if err := srv.store.MarkSiteProjectionManual(ctx, channel.ID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "removed" {
				adapter.keys = nil
			} else {
				adapter.modelsByKey["private-route-key"] = []string{}
			}
			final := newSiteTask("model_refresh", account.SiteID, account.ID, 1)
			srv.siteControl.refreshAccount(ctx, final, account.ID, true)
			var progress struct {
				Routes []map[string]string `json:"routes"`
			}
			if err := json.Unmarshal([]byte(final.ProgressJSON), &progress); err != nil {
				t.Fatal(err)
			}
			wantStatus := "deactivated"
			if mode == "manual empty" {
				wantStatus = "unchanged"
			}
			if len(progress.Routes) != 1 || progress.Routes[0]["name"] != channel.Name || progress.Routes[0]["status"] != wantStatus || progress.Routes[0]["error"] != "" {
				t.Fatalf("mode=%s progress=%s, want named %s row", mode, final.ProgressJSON, wantStatus)
			}
		})
	}
}

func TestModelRefreshRouteProgressUsesStoredChannelNames(t *testing.T) {
	keys := []provider.RoutingKeySnapshot{
		{ID: "a", Name: "shared", Group: "group", Key: "private-name-alpha", Enabled: true},
		{ID: "b", Name: "shared", Group: "group", Key: "private-name-beta", Enabled: true},
	}
	adapter := siteSyncPreviewAdapter(keys, map[string][]string{
		"private-name-alpha": {"alpha-model"}, "private-name-beta": {"beta-model"},
	})
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	ctx := context.Background()
	verifyNames := func(task *model.SiteTask, statuses map[string]string) {
		t.Helper()
		var progress struct {
			Routes []RouteSyncOutcome `json:"routes"`
		}
		if err := json.Unmarshal([]byte(task.ProgressJSON), &progress); err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, route := range progress.Routes {
			if _, duplicate := got[route.Name]; duplicate {
				t.Fatalf("different channels reported the same name: %s", task.ProgressJSON)
			}
			got[route.Name] = route.Status
		}
		if !reflect.DeepEqual(got, statuses) {
			t.Fatalf("progress=%v, want stored channel names %v", got, statuses)
		}
	}
	initial := newSiteTask("model_refresh", account.SiteID, account.ID, 1)
	srv.siteControl.refreshAccount(ctx, initial, account.ID, true)
	if initial.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("initial refresh=%+v", initial)
	}
	channels := map[string]*model.Config{}
	for _, key := range keys {
		binding, err := srv.store.GetSiteChannelBinding(ctx, account.ID, "key:"+key.ID)
		if err != nil {
			t.Fatal(err)
		}
		channel, err := srv.store.GetConfig(ctx, binding.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		channels[key.ID] = channel
	}
	verifyNames(initial, map[string]string{channels["a"].Name: "updated", channels["b"].Name: "updated"})
	manual := channels["b"].Clone()
	manual.Name = "管理员命名的渠道"
	if _, err := srv.store.UpdateConfig(ctx, manual.ID, manual); err != nil {
		t.Fatal(err)
	}
	final := newSiteTask("model_refresh", account.SiteID, account.ID, 1)
	srv.siteControl.refreshAccount(ctx, final, account.ID, true)
	verifyNames(final, map[string]string{channels["a"].Name: "updated", manual.Name: "unchanged"})
}

func TestModelRefreshRouteProgressDoesNotInventEmptyChannel(t *testing.T) {
	adapter := siteSyncPreviewAdapter([]provider.RoutingKeySnapshot{{ID: "empty", Key: "private-new-empty", Enabled: true}},
		map[string][]string{"private-new-empty": {}})
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	task := newSiteTask("model_refresh", account.SiteID, account.ID, 1)
	srv.siteControl.refreshAccount(context.Background(), task, account.ID, true)
	if task.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("empty new key refresh=%+v", task)
	}
	bindings, err := srv.store.ListSiteChannelBindings(context.Background())
	if err != nil || len(bindings) != 0 {
		t.Fatalf("new empty key created a channel binding: %v err=%v", bindings, err)
	}
	var progress struct {
		Routes []RouteSyncOutcome `json:"routes"`
	}
	if err := json.Unmarshal([]byte(task.ProgressJSON), &progress); err != nil {
		t.Fatal(err)
	}
	if len(progress.Routes) != 0 {
		t.Fatalf("no channel existed but refresh reports a channel result: %s", task.ProgressJSON)
	}
}
