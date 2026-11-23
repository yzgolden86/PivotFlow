package app

import (
	"context"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func createManualSiteProjectionForTest(t *testing.T, srv *Server, account *model.SiteAccount) (int64, string) {
	t.Helper()
	const upstreamKey = "sk-manual-ownership"
	creds, err := srv.siteControl.credentials(account)
	if err != nil {
		t.Fatal(err)
	}
	creds.APIKey = upstreamKey
	site, err := srv.store.GetSite(context.Background(), account.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := srv.siteControl.projectAccountWithModelsForProtocols(
		context.Background(),
		account,
		site,
		creds,
		"key:manual-ownership",
		"Manual ownership",
		nil,
		[]string{"gpt-old"},
		false,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.MarkSiteProjectionManual(context.Background(), result.Channel.ID); err != nil {
		t.Fatal(err)
	}
	return result.Channel.ID, upstreamKey
}

func TestSiteControlRefreshDoesNotOverrideManualProjection(t *testing.T) {
	const upstreamKey = "sk-manual-ownership"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "manual-ownership", Name: "Manual ownership", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"gpt-new"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	channelID, _ := createManualSiteProjectionForTest(t, srv, account)

	task := newSiteTask("model_refresh", account.SiteID, account.ID, 1)
	srv.siteControl.refreshAccount(context.Background(), task, account.ID, true)
	channel, err := srv.store.GetConfig(context.Background(), channelID)
	if err != nil {
		t.Fatal(err)
	}
	if got := channel.GetModels(); len(got) != 1 || got[0] != "gpt-old" {
		t.Fatalf("manual refresh overwrote models=%v", got)
	}
	binding, err := srv.store.GetSiteChannelBinding(context.Background(), account.ID, "key:manual-ownership")
	if err != nil {
		t.Fatal(err)
	}
	if binding.Ownership != "manual" {
		t.Fatalf("binding ownership=%q, want manual", binding.Ownership)
	}
}

func TestExplicitProjectionForceOverridesManualProjection(t *testing.T) {
	const upstreamKey = "sk-manual-ownership"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "manual-ownership", Name: "Manual ownership", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"gpt-new"}},
	)
	srv, site, account := newSiteSyncPreviewFixture(t, adapter)
	channelID, _ := createManualSiteProjectionForTest(t, srv, account)
	creds, err := srv.siteControl.credentials(account)
	if err != nil {
		t.Fatal(err)
	}
	creds.APIKey = upstreamKey

	if _, err := srv.siteControl.projectAccountWithModelsForProtocols(
		context.Background(),
		account,
		site,
		creds,
		"key:manual-ownership",
		"Manual ownership",
		nil,
		[]string{"gpt-new"},
		true,
		"",
	); err != nil {
		t.Fatal(err)
	}
	channel, err := srv.store.GetConfig(context.Background(), channelID)
	if err != nil {
		t.Fatal(err)
	}
	if got := channel.GetModels(); len(got) != 1 || got[0] != "gpt-new" {
		t.Fatalf("explicit force models=%v", got)
	}
	binding, err := srv.store.GetSiteChannelBinding(context.Background(), account.ID, "key:manual-ownership")
	if err != nil {
		t.Fatal(err)
	}
	if binding.Ownership != "manual" {
		t.Fatalf("force changed binding ownership=%q", binding.Ownership)
	}
}
