package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"

	"github.com/gin-gonic/gin"
)

func newSiteSyncPreviewFixture(t *testing.T, adapter provider.SiteAdapter) (*Server, *model.Site, *model.SiteAccount) {
	t.Helper()
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "site-preview-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	srv.siteControl.registry = provider.NewRegistry(adapter)
	srv.siteControl.requestBudget = provider.NewRequestBudget()

	sealed, err := cipher.Seal(provider.Credentials{AccessToken: "management-secret"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	site, err := srv.store.CreateSite(ctx, &model.Site{
		Name:     "Preview Site",
		Platform: model.SitePlatformNewAPIFamily,
		BaseURL:  "https://preview.example",
		Enabled:  true,
		Timezone: "Asia/Shanghai",
		TagsJSON: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err := srv.store.CreateSiteAccount(ctx, &model.SiteAccount{
		SiteID:               site.ID,
		Label:                "primary",
		CredentialType:       model.CredentialTypeAccessToken,
		CredentialCiphertext: sealed,
		CredentialKeyVersion: cipher.Version(),
		Enabled:              true,
		Status:               model.SiteAccountStatusHealthy,
		LastRefreshStatus:    "unknown",
		LastCheckinStatus:    "unknown",
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, site, account
}

func siteSyncPreviewAdapter(keys []provider.RoutingKeySnapshot, models map[string][]string) *multiKeyProjectionTestAdapter {
	return &multiKeyProjectionTestAdapter{
		keys:        keys,
		modelsByKey: models,
		errorsByKey: map[string]error{},
	}
}

func applySiteSyncPreviewForTest(t *testing.T, srv *Server, accountID int64) {
	t.Helper()
	previewID, err := srv.discoverSiteSyncPreview(context.Background(), accountID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.applyConfigPreview(context.Background(), previewID); err != nil {
		t.Fatal(err)
	}
}

func TestSiteSyncPreviewDiscoveryIsReadOnlyAndApplyCreatesProjection(t *testing.T) {
	const upstreamKey = "sk-upstream-a"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "alpha-id", Name: "Alpha", Group: "alpha", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"gpt-alpha"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	ctx := context.Background()

	previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	configs, err := srv.store.ListConfigs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 0 || len(bindings) != 0 {
		t.Fatalf("discovery wrote configuration: configs=%d bindings=%d", len(configs), len(bindings))
	}

	preview, err := srv.getConfigPreview(previewID)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Kind != "site_sync" || len(preview.Items) != 1 || preview.Items[0].Action != "create" {
		t.Fatalf("preview=%+v", preview)
	}
	if preview.Items[0].Message != "projection_create" {
		t.Fatalf("preview message=%q, want projection_create", preview.Items[0].Message)
	}
	if preview.Items[0].Key != "sync[1]" {
		t.Fatalf("preview item key=%q, want local sequence key", preview.Items[0].Key)
	}
	raw, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{upstreamKey, "management-secret", "alpha-id", "key:"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("preview exposed %q: %s", secret, raw)
		}
	}

	result, err := srv.applyConfigPreview(ctx, previewID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "applied" {
		t.Fatalf("apply status=%q", result.Status)
	}
	configs, err = srv.store.ListConfigs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err = srv.store.ListSiteChannelBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || len(bindings) != 1 || bindings[0].Status != "active" {
		t.Fatalf("configs=%+v bindings=%+v", configs, bindings)
	}
	if got := configs[0].GetModels(); len(got) != 1 || got[0] != "gpt-alpha" {
		t.Fatalf("models=%v", got)
	}
}

func TestSiteSyncPreviewPayloadSizeCountsCapturedSecretsAndModels(t *testing.T) {
	apiKey := strings.Repeat("k", 32<<10)
	modelName := strings.Repeat("m", 24<<10)
	credentialCiphertext := strings.Repeat("c", 16<<10)
	account := &model.SiteAccount{
		ID:                   1,
		SiteID:               2,
		Label:                "large",
		CredentialCiphertext: credentialCiphertext,
		CredentialKeyVersion: "v1",
		Enabled:              true,
	}
	site := &model.Site{ID: 2, Name: "Large", BaseURL: "https://large.example", Enabled: true}
	plan := siteSyncProjectionPlan{
		accountID:            1,
		fingerprint:          "fingerprint",
		activeProjectionKeys: []string{"key:large"},
		operations: []siteSyncProjectionOperation{{
			itemKey:       "sync[1]",
			account:       account,
			site:          site,
			channelID:     3,
			projectionKey: "key:large",
			name:          "Large",
			protocols:     []string{"openai"},
			models:        []string{modelName},
			apiKey:        apiKey,
			pricingGroup:  "large",
			force:         true,
			preserved: &siteSyncPreservedChannel{
				enabled:               true,
				priority:              12,
				protocolTransformMode: model.ProtocolTransformModeLocal,
				customRequestRules: &model.CustomRequestRules{
					Headers: []model.CustomHeaderRule{{Action: "override", Name: "X-Large", Value: strings.Repeat("r", 8<<10)}},
				},
			},
			previousKeys: []*model.APIKey{{APIKey: strings.Repeat("p", 8<<10), Note: "previous"}},
		}},
	}

	if got, wantMinimum := siteSyncPreviewPayloadSize(plan), len(apiKey)+len(modelName)+len(credentialCiphertext); got <= wantMinimum {
		t.Fatalf("payload size=%d, want greater than captured fields=%d", got, wantMinimum)
	}
}

func TestSiteSyncPreviewNormalizesLabelsWithoutChangingProjectedName(t *testing.T) {
	const upstreamKey = "sk-label-boundary"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "label-id", Name: "team", Group: "team", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"gpt-label"}},
	)
	srv, site, account := newSiteSyncPreviewFixture(t, adapter)
	ctx := context.Background()
	siteName := strings.Repeat("S", 80) + "\x00\t\n" + strings.Repeat("T", 80)
	site.Name = siteName
	if _, err := srv.store.UpdateSite(ctx, site.ID, site); err != nil {
		t.Fatal(err)
	}

	previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := srv.getConfigPreview(previewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 {
		t.Fatalf("items=%+v", preview.Items)
	}
	wantLabel := strings.Repeat("S", 80) + strings.Repeat("T", 40)
	if preview.Items[0].Label != wantLabel {
		t.Fatalf("preview label=%q, want normalized label %q", preview.Items[0].Label, wantLabel)
	}
	if strings.ContainsAny(preview.Items[0].Label, "\x00\t\n") {
		t.Fatalf("preview label retained control characters: %q", preview.Items[0].Label)
	}

	if _, err := srv.applyConfigPreview(ctx, previewID); err != nil {
		t.Fatal(err)
	}
	bindings, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	channel, err := srv.store.GetConfig(ctx, bindings[0].ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	wantName := siteName + " / primary / team"
	if channel.Name != wantName {
		t.Fatalf("projected channel name=%q, want unchanged planned name %q", channel.Name, wantName)
	}
}

func TestSiteSyncPreviewCompleteEmptySnapshotIsANoOp(t *testing.T) {
	adapter := siteSyncPreviewAdapter(nil, map[string][]string{})
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	ctx := context.Background()

	previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := srv.getConfigPreview(previewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 0 || !preview.CanApply {
		t.Fatalf("preview=%+v", preview)
	}
	result, err := srv.applyConfigPreview(ctx, previewID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "applied" {
		t.Fatalf("apply status=%q", result.Status)
	}
	configs, err := srv.store.ListConfigs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 0 || len(bindings) != 0 {
		t.Fatalf("empty preview wrote configs=%d bindings=%d", len(configs), len(bindings))
	}
}

func TestSiteSyncPreviewPreservesManualProjection(t *testing.T) {
	const upstreamKey = "sk-manual"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "manual-id", Name: "Manual", Group: "manual", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"gpt-old"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	applySiteSyncPreviewForTest(t, srv, account.ID)

	bindings, err := srv.store.ListSiteChannelBindings(context.Background())
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	channelID := bindings[0].ChannelID
	if err := srv.store.MarkSiteProjectionManual(context.Background(), channelID); err != nil {
		t.Fatal(err)
	}
	adapter.modelsByKey[upstreamKey] = []string{"gpt-new"}

	previewID, err := srv.discoverSiteSyncPreview(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := srv.getConfigPreview(previewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || preview.Items[0].Action != "preserve" {
		t.Fatalf("items=%+v", preview.Items)
	}
	if preview.Items[0].Message != "manual_ownership" {
		t.Fatalf("preserve message=%q, want manual_ownership", preview.Items[0].Message)
	}
	if _, err := srv.applyConfigPreview(context.Background(), previewID); err != nil {
		t.Fatal(err)
	}

	channel, err := srv.store.GetConfig(context.Background(), channelID)
	if err != nil {
		t.Fatal(err)
	}
	if got := channel.GetModels(); len(got) != 1 || got[0] != "gpt-old" {
		t.Fatalf("manual projection models=%v, want preserved gpt-old", got)
	}
}

func TestSiteSyncPreviewUpdatePreservesLocalProjectionFields(t *testing.T) {
	const upstreamKey = "sk-local-fields"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "local-id", Name: "Local", Group: "local", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"gpt-old"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	applySiteSyncPreviewForTest(t, srv, account.ID)

	ctx := context.Background()
	bindings, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	channelID := bindings[0].ChannelID
	channel, err := srv.store.GetConfig(ctx, channelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.Enabled = false
	channel.Priority = 42
	channel.ProtocolTransformMode = model.ProtocolTransformModeLocal
	channel.CustomRequestRules = &model.CustomRequestRules{
		Headers: []model.CustomHeaderRule{{Action: "override", Name: "X-Local", Value: "kept"}},
	}
	channel.CooldownDetectionRules = &model.CooldownDetectionRules{
		Rules: []model.CooldownDetectionRule{{
			RuleID: "local-rule", Enabled: true, Name: "Local rule", Priority: 3,
			StatusCodes: []int{429}, Scope: model.CooldownScopeKey, Mode: model.CooldownModeFixed,
			CooldownSeconds: 1,
		}},
	}
	if _, err := srv.store.UpdateConfig(ctx, channelID, channel); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpdateAPIKeysStrategy(ctx, channelID, model.KeyStrategyRoundRobin); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpdateAPIKeyNotes(ctx, channelID, map[int]string{0: "local note"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpdateAPIKeyMetadata(ctx, channelID, map[int]model.APIKey{
		0: {
			AllowedModels:     []string{"gpt-new"},
			CostMultiplier:    0.5,
			CostMultiplierSet: true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	cooldownUntil := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	if err := srv.store.SetKeyCooldown(ctx, channelID, 0, cooldownUntil); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetSiteProjectionOwnership(ctx, channelID, "projected"); err != nil {
		t.Fatal(err)
	}
	adapter.modelsByKey[upstreamKey] = []string{"gpt-new"}

	previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := srv.getConfigPreview(previewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || preview.Items[0].Action != "update" {
		t.Fatalf("items=%+v", preview.Items)
	}
	if preview.Items[0].Message != "projection_update" {
		t.Fatalf("update message=%q, want projection_update", preview.Items[0].Message)
	}
	for _, field := range []string{"enabled", "priority", "protocol_transform_mode", "custom_request_rules", "cooldown_detection_rules", "key_metadata"} {
		if !slices.Contains(preview.Items[0].PreservedFields, field) {
			t.Fatalf("preserved fields=%v, missing %s", preview.Items[0].PreservedFields, field)
		}
	}
	if _, err := srv.applyConfigPreview(ctx, previewID); err != nil {
		t.Fatal(err)
	}

	updated, err := srv.store.GetConfig(ctx, channelID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.Priority != 42 || updated.ProtocolTransformMode != model.ProtocolTransformModeLocal {
		t.Fatalf("local channel fields reset: enabled=%t priority=%d mode=%q", updated.Enabled, updated.Priority, updated.ProtocolTransformMode)
	}
	if updated.CustomRequestRules == nil || len(updated.CustomRequestRules.Headers) != 1 || updated.CustomRequestRules.Headers[0].Value != "kept" {
		t.Fatalf("custom request rules=%+v", updated.CustomRequestRules)
	}
	if updated.CooldownDetectionRules == nil || len(updated.CooldownDetectionRules.Rules) != 1 || updated.CooldownDetectionRules.Rules[0].RuleID != "local-rule" {
		t.Fatalf("cooldown rules=%+v", updated.CooldownDetectionRules)
	}
	if got := updated.GetModels(); len(got) != 1 || got[0] != "gpt-new" {
		t.Fatalf("models=%v", got)
	}
	keys, err := srv.store.GetAPIKeys(ctx, channelID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys=%+v err=%v", keys, err)
	}
	key := keys[0]
	if key.APIKey != upstreamKey || key.Note != "local note" || key.KeyStrategy != model.KeyStrategyRoundRobin ||
		!slices.Equal(key.AllowedModels, []string{"gpt-new"}) || key.CostMultiplier != 0.5 {
		t.Fatalf("key metadata reset: %+v", key)
	}
	if key.CooldownUntil < cooldownUntil.Unix() {
		t.Fatalf("key cooldown reset: got=%d want=%d", key.CooldownUntil, cooldownUntil.Unix())
	}
}

func TestSiteSyncPreviewRotatedKeyKeepsLocalMetadata(t *testing.T) {
	const (
		oldKey = "sk-rotation-old"
		newKey = "sk-rotation-new"
	)
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "rotation-id", Name: "Rotation", Group: "rotation", Key: oldKey, Enabled: true}},
		map[string][]string{oldKey: {"gpt-stable"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	applySiteSyncPreviewForTest(t, srv, account.ID)

	ctx := context.Background()
	bindings, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	channelID := bindings[0].ChannelID
	channel, err := srv.store.GetConfig(ctx, channelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.Enabled = false
	channel.Priority = 42
	channel.ProtocolTransformMode = model.ProtocolTransformModeLocal
	channel.CustomRequestRules = &model.CustomRequestRules{
		Headers: []model.CustomHeaderRule{{Action: "override", Name: "X-Rotation", Value: "kept"}},
	}
	channel.CooldownDetectionRules = &model.CooldownDetectionRules{
		Rules: []model.CooldownDetectionRule{{
			RuleID: "rotation-rule", Enabled: true, Name: "Rotation rule", Priority: 3,
			StatusCodes: []int{429}, Scope: model.CooldownScopeKey, Mode: model.CooldownModeFixed,
			CooldownSeconds: 2,
		}},
	}
	if _, err := srv.store.UpdateConfig(ctx, channelID, channel); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetSiteProjectionOwnership(ctx, channelID, "projected"); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpdateAPIKeysStrategy(ctx, channelID, model.KeyStrategyRoundRobin); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpdateAPIKeyNotes(ctx, channelID, map[int]string{0: "rotation note"}); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetAPIKeyDisabled(ctx, channelID, 0, true); err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpdateAPIKeyMetadata(ctx, channelID, map[int]model.APIKey{
		0: {
			AllowedModels:     []string{"gpt-stable"},
			CostMultiplier:    0.25,
			CostMultiplierSet: true,
		},
	}); err != nil {
		t.Fatal(err)
	}
	cooldownUntil := time.Now().Add(20 * time.Minute).Truncate(time.Second)
	if err := srv.store.SetKeyCooldown(ctx, channelID, 0, cooldownUntil); err != nil {
		t.Fatal(err)
	}
	queryStore, ok := srv.store.(*sqlstore.SQLStore)
	if !ok {
		t.Fatalf("store type=%T, want *sqlstore.SQLStore", srv.store)
	}
	health := model.APIKeyHealth{Status: "invalid", Reason: "local health", StatusCode: 401, CheckedAt: 123456}
	if _, err := queryStore.ExecContext(ctx, "UPDATE api_keys SET health_status=?, health_reason=?, health_status_code=?, health_checked_at=? WHERE channel_id=?", health.Status, health.Reason, health.StatusCode, health.CheckedAt, channelID); err != nil {
		t.Fatal(err)
	}
	bindingAfterLocalEdits, err := srv.store.GetSiteChannelBinding(ctx, account.ID, bindings[0].ProjectionKey)
	if err != nil {
		t.Fatal(err)
	}
	if bindingAfterLocalEdits == nil || bindingAfterLocalEdits.Ownership != "projected" {
		t.Fatalf("ownership after local edits=%+v, want projected", bindingAfterLocalEdits)
	}

	adapter.keys[0].Key = newKey
	adapter.modelsByKey[newKey] = []string{"gpt-stable"}
	previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := srv.getConfigPreview(previewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || preview.Items[0].Action != "update" || !slices.Contains(preview.Items[0].Fields, "api_key") {
		t.Fatalf("preview items=%+v", preview.Items)
	}
	channelRuntimeUntil := time.Now().Add(45 * time.Minute).Unix()
	if _, err := queryStore.ExecContext(ctx, "UPDATE channels SET cooldown_until=?, cooldown_duration_ms=? WHERE id=?", channelRuntimeUntil, int64(90000), channelID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.applyConfigPreview(ctx, previewID); err != nil {
		t.Fatal(err)
	}

	updated, err := srv.store.GetConfig(ctx, channelID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled || updated.Priority != 42 || updated.ProtocolTransformMode != model.ProtocolTransformModeLocal {
		t.Fatalf("local channel fields reset: enabled=%t priority=%d mode=%q", updated.Enabled, updated.Priority, updated.ProtocolTransformMode)
	}
	if updated.CustomRequestRules == nil || len(updated.CustomRequestRules.Headers) != 1 || updated.CustomRequestRules.Headers[0].Value != "kept" {
		t.Fatalf("custom request rules=%+v", updated.CustomRequestRules)
	}
	if updated.CooldownDetectionRules == nil || len(updated.CooldownDetectionRules.Rules) != 1 || updated.CooldownDetectionRules.Rules[0].RuleID != "rotation-rule" {
		t.Fatalf("cooldown rules=%+v", updated.CooldownDetectionRules)
	}
	if updated.CooldownUntil != channelRuntimeUntil || updated.CooldownDurationMs != 90000 {
		t.Fatalf("channel runtime cooldown reset: until=%d duration=%d", updated.CooldownUntil, updated.CooldownDurationMs)
	}
	keys, err := srv.store.GetAPIKeys(ctx, channelID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys=%+v err=%v", keys, err)
	}
	key := keys[0]
	if key.APIKey != newKey {
		t.Fatalf("rotated key was not applied: got=%q want=%q", key.APIKey, newKey)
	}
	if key.Note != "rotation note" || key.KeyStrategy != model.KeyStrategyRoundRobin || !key.Disabled ||
		!slices.Equal(key.AllowedModels, []string{"gpt-stable"}) || key.CostMultiplier != 0.25 || !key.CostMultiplierSet {
		t.Fatalf("rotated key metadata reset: %+v", key)
	}
	if key.CooldownUntil != 0 || key.CooldownDurationMs != 0 {
		t.Fatalf("rotated key retained old credential cooldown: until=%d duration=%d", key.CooldownUntil, key.CooldownDurationMs)
	}
	if key.Health != (model.APIKeyHealth{}) {
		t.Fatalf("rotated key retained old credential health: got=%+v", key.Health)
	}
}

func TestSiteSyncPreviewPreservesDisabledKeyAcrossUpdate(t *testing.T) {
	const upstreamKey = "sk-disabled"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "disabled-id", Name: "Disabled", Group: "disabled", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"gpt-old"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	applySiteSyncPreviewForTest(t, srv, account.ID)

	ctx := context.Background()
	bindings, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	channelID := bindings[0].ChannelID
	if err := srv.store.SetAPIKeyDisabled(ctx, channelID, 0, true); err != nil {
		t.Fatal(err)
	}
	adapter.modelsByKey[upstreamKey] = []string{"gpt-new"}

	previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := srv.getConfigPreview(previewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || preview.Items[0].Action != "update" {
		t.Fatalf("items=%+v", preview.Items)
	}
	if !slices.Contains(preview.Items[0].PreservedFields, "key_metadata") {
		t.Fatalf("preserved fields=%v, missing key_metadata", preview.Items[0].PreservedFields)
	}
	if _, err := srv.applyConfigPreview(ctx, previewID); err != nil {
		t.Fatal(err)
	}

	updated, err := srv.store.GetConfig(ctx, channelID)
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.GetModels(); len(got) != 1 || got[0] != "gpt-new" {
		t.Fatalf("models=%v", got)
	}
	keys, err := srv.store.GetAPIKeys(ctx, channelID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys=%+v err=%v", keys, err)
	}
	if !keys[0].Disabled {
		t.Fatal("disabled key was re-enabled by site sync")
	}
}

func TestSiteSyncPreviewProtectsMultiKeyBindings(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "projected conflict"
		if manual {
			name = "manual preserve"
		}
		t.Run(name, func(t *testing.T) {
			const upstreamKey = "sk-multi-binding"
			adapter := siteSyncPreviewAdapter(
				[]provider.RoutingKeySnapshot{{ID: "multi-binding-id", Name: "Multi Binding", Group: "multi", Key: upstreamKey, Enabled: true}},
				map[string][]string{upstreamKey: {"gpt-old"}},
			)
			srv, _, account := newSiteSyncPreviewFixture(t, adapter)
			applySiteSyncPreviewForTest(t, srv, account.ID)

			ctx := context.Background()
			bindings, err := srv.store.ListSiteChannelBindings(ctx)
			if err != nil || len(bindings) != 1 {
				t.Fatalf("bindings=%+v err=%v", bindings, err)
			}
			channelID := bindings[0].ChannelID
			if err := srv.store.CreateAPIKeysBatch(ctx, []*model.APIKey{{
				ChannelID:   channelID,
				KeyIndex:    1,
				APIKey:      "sk-local-second",
				Note:        "local second key",
				KeyStrategy: model.KeyStrategyRoundRobin,
				Disabled:    true,
			}}); err != nil {
				t.Fatal(err)
			}
			if manual {
				if err := srv.store.MarkSiteProjectionManual(ctx, channelID); err != nil {
					t.Fatal(err)
				}
			}
			adapter.modelsByKey[upstreamKey] = []string{"gpt-new"}

			previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := srv.getConfigPreview(previewID)
			if err != nil {
				t.Fatal(err)
			}
			if len(preview.Items) != 1 {
				t.Fatalf("items=%+v", preview.Items)
			}
			if manual {
				if preview.Items[0].Action != "preserve" || !preview.CanApply {
					t.Fatalf("manual preview=%+v", preview)
				}
				if _, err := srv.applyConfigPreview(ctx, previewID); err != nil {
					t.Fatal(err)
				}
			} else {
				if preview.Items[0].Action != "conflict" || preview.CanApply {
					t.Fatalf("projected preview=%+v", preview)
				}
				if _, err := srv.applyConfigPreview(ctx, previewID); err == nil {
					t.Fatal("multi-key projected conflict was applied")
				}
			}

			channel, err := srv.store.GetConfig(ctx, channelID)
			if err != nil {
				t.Fatal(err)
			}
			if got := channel.GetModels(); len(got) != 1 || got[0] != "gpt-old" {
				t.Fatalf("multi-key channel models=%v", got)
			}
			keys, err := srv.store.GetAPIKeys(ctx, channelID)
			if err != nil || len(keys) != 2 {
				t.Fatalf("keys=%+v err=%v", keys, err)
			}
			if keys[1].APIKey != "sk-local-second" || keys[1].Note != "local second key" || !keys[1].Disabled || keys[1].KeyStrategy != model.KeyStrategyRoundRobin {
				t.Fatalf("local second key metadata changed: %+v", keys[1])
			}
		})
	}
}

func TestSiteSyncPreviewUpdatesUpstreamNameAndPricingGroup(t *testing.T) {
	const upstreamKey = "sk-name-group"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "name-group-id", Name: "Original", Group: "team-old", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"gpt-stable"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	applySiteSyncPreviewForTest(t, srv, account.ID)

	ctx := context.Background()
	bindings, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	adapter.keys[0].Name = "Renamed"
	adapter.keys[0].Group = "team-new"

	previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := srv.getConfigPreview(previewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || preview.Items[0].Action != "update" {
		t.Fatalf("items=%+v", preview.Items)
	}
	for _, field := range []string{"name", "pricing_group"} {
		if !slices.Contains(preview.Items[0].Fields, field) {
			t.Fatalf("fields=%v, missing %s", preview.Items[0].Fields, field)
		}
	}
	if _, err := srv.applyConfigPreview(ctx, previewID); err != nil {
		t.Fatal(err)
	}

	channel, err := srv.store.GetConfig(ctx, bindings[0].ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Preview Site / primary / team-new / Renamed"; channel.Name != want {
		t.Fatalf("channel name=%q, want %q", channel.Name, want)
	}
	binding, err := srv.store.GetSiteChannelBinding(ctx, account.ID, bindings[0].ProjectionKey)
	if err != nil {
		t.Fatal(err)
	}
	if binding.PricingGroup != "team-new" {
		t.Fatalf("pricing group=%q, want team-new", binding.PricingGroup)
	}
}

func TestSiteSyncPreviewRejectsChangedLocalConfiguration(t *testing.T) {
	const upstreamKey = "sk-stale"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "stale-id", Name: "Stale", Group: "stale", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"gpt-old"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	applySiteSyncPreviewForTest(t, srv, account.ID)
	adapter.modelsByKey[upstreamKey] = []string{"gpt-new"}

	previewID, err := srv.discoverSiteSyncPreview(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := srv.store.ListSiteChannelBindings(context.Background())
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	channel, err := srv.store.GetConfig(context.Background(), bindings[0].ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	channel.Priority = 99
	if _, err := srv.store.UpdateConfig(context.Background(), channel.ID, channel); err != nil {
		t.Fatal(err)
	}

	if _, err := srv.applyConfigPreview(context.Background(), previewID); err == nil || !strings.Contains(err.Error(), "preview_stale") {
		t.Fatalf("apply error=%v, want preview_stale", err)
	}
	updated, err := srv.store.GetConfig(context.Background(), channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.GetModels(); len(got) != 1 || got[0] != "gpt-old" {
		t.Fatalf("stale preview changed models=%v", got)
	}
}

func TestSiteSyncPreviewRejectsSiteIdentityChangeAfterDiscovery(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		update func(*model.Site)
	}{
		{name: "name", update: func(site *model.Site) { site.Name = "Renamed Site" }},
		{name: "timezone", update: func(site *model.Site) { site.Timezone = "UTC" }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			const upstreamKey = "sk-site-stale"
			adapter := siteSyncPreviewAdapter(
				[]provider.RoutingKeySnapshot{{ID: "site-stale-id", Name: "Site Stale", Group: "stale", Key: upstreamKey, Enabled: true}},
				map[string][]string{upstreamKey: {"gpt-stale"}},
			)
			srv, site, account := newSiteSyncPreviewFixture(t, adapter)
			ctx := context.Background()
			previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
			if err != nil {
				t.Fatal(err)
			}

			testCase.update(site)
			if _, err := srv.store.UpdateSite(ctx, site.ID, site); err != nil {
				t.Fatal(err)
			}
			if _, err := srv.applyConfigPreview(ctx, previewID); err == nil || !strings.Contains(err.Error(), "preview_stale") {
				t.Fatalf("apply error=%v, want preview_stale", err)
			}
			configs, err := srv.store.ListConfigs(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(configs) != 0 {
				t.Fatalf("stale preview created configs=%+v", configs)
			}
		})
	}
}

func TestSiteSyncPreviewRejectsCredentialAndDeletionChangesAfterDiscovery(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		query string
		args  func(*model.Site, *model.SiteAccount) []any
	}{
		{
			name:  "credential ciphertext",
			query: "UPDATE site_accounts SET credential_ciphertext=? WHERE id=?",
			args: func(_ *model.Site, account *model.SiteAccount) []any {
				return []any{"changed-ciphertext", account.ID}
			},
		},
		{
			name:  "credential key version",
			query: "UPDATE site_accounts SET credential_key_version=? WHERE id=?",
			args: func(_ *model.Site, account *model.SiteAccount) []any {
				return []any{"changed-key-version", account.ID}
			},
		},
		{
			name:  "account deleted",
			query: "UPDATE site_accounts SET deleted_at=? WHERE id=?",
			args: func(_ *model.Site, account *model.SiteAccount) []any {
				return []any{int64(123456789), account.ID}
			},
		},
		{
			name:  "site deleted",
			query: "UPDATE sites SET deleted_at=? WHERE id=?",
			args: func(site *model.Site, _ *model.SiteAccount) []any {
				return []any{int64(123456789), site.ID}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			adapter := siteSyncPreviewAdapter(
				[]provider.RoutingKeySnapshot{{ID: "fingerprint-id", Name: "Fingerprint", Group: "fingerprint", Key: "sk-fingerprint", Enabled: true}},
				map[string][]string{"sk-fingerprint": {"gpt-fingerprint"}},
			)
			srv, site, account := newSiteSyncPreviewFixture(t, adapter)
			ctx := context.Background()
			previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
			if err != nil {
				t.Fatal(err)
			}
			queryStore, ok := srv.store.(*sqlstore.SQLStore)
			if !ok {
				t.Fatalf("store type=%T, want *sqlstore.SQLStore", srv.store)
			}
			if _, err := queryStore.ExecContext(ctx, testCase.query, testCase.args(site, account)...); err != nil {
				t.Fatal(err)
			}

			if _, err := srv.applyConfigPreview(ctx, previewID); err == nil || !strings.Contains(err.Error(), "preview_stale") {
				t.Fatalf("apply error=%v, want preview_stale", err)
			}
			configs, err := srv.store.ListConfigs(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(configs) != 0 {
				t.Fatalf("stale preview created configs=%+v", configs)
			}
		})
	}
}

func TestSiteSyncPreviewRejectsAccountExpiryBeforeUnboundApply(t *testing.T) {
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "expiry-id", Name: "Expiry", Group: "expiry", Key: "sk-expiry", Enabled: true}},
		map[string][]string{"sk-expiry": {"gpt-expiry"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	ctx := context.Background()
	previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	queryStore, ok := srv.store.(*sqlstore.SQLStore)
	if !ok {
		t.Fatalf("store type=%T, want *sqlstore.SQLStore", srv.store)
	}
	if _, err := queryStore.ExecContext(ctx, "UPDATE site_accounts SET status=? WHERE id=?", model.SiteAccountStatusExpired, account.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := srv.applyConfigPreview(ctx, previewID); err == nil || !strings.Contains(err.Error(), "preview_stale") {
		t.Fatalf("apply error=%v, want preview_stale", err)
	}
	configs, err := srv.store.ListConfigs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 0 {
		t.Fatalf("expired preview created configs=%+v", configs)
	}
}

func TestSiteSyncPreviewIgnoresVolatileRuntimeFieldsForStaleness(t *testing.T) {
	const upstreamKey = "sk-runtime-fields"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "runtime-id", Name: "Runtime", Group: "runtime", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"gpt-old"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	applySiteSyncPreviewForTest(t, srv, account.ID)
	adapter.modelsByKey[upstreamKey] = []string{"gpt-new"}

	ctx := context.Background()
	previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	queryStore, ok := srv.store.(*sqlstore.SQLStore)
	if !ok {
		t.Fatalf("store type=%T, want *sqlstore.SQLStore", srv.store)
	}
	runtimeAt := time.Now().Add(time.Hour).Unix()
	if _, err := queryStore.ExecContext(ctx, "UPDATE channels SET cooldown_until=?, cooldown_duration_ms=?, updated_at=? WHERE id=?", runtimeAt, int64(60000), runtimeAt*1000, bindings[0].ChannelID); err != nil {
		t.Fatal(err)
	}
	if _, err := queryStore.ExecContext(ctx, "UPDATE api_keys SET cooldown_until=?, cooldown_duration_ms=?, health_status=?, health_reason=?, health_status_code=?, health_checked_at=?, updated_at=? WHERE channel_id=?", runtimeAt, int64(60000), "invalid", "runtime health", 429, runtimeAt*1000, runtimeAt*1000, bindings[0].ChannelID); err != nil {
		t.Fatal(err)
	}

	if _, err := srv.applyConfigPreview(ctx, previewID); err != nil {
		t.Fatalf("apply after runtime-only changes: %v", err)
	}
	channel, err := srv.store.GetConfig(ctx, bindings[0].ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if got := channel.GetModels(); len(got) != 1 || got[0] != "gpt-new" {
		t.Fatalf("models=%v, want gpt-new", got)
	}
	if channel.CooldownUntil != runtimeAt || channel.CooldownDurationMs != 60000 {
		t.Fatalf("channel runtime cooldown reset: until=%d duration=%d", channel.CooldownUntil, channel.CooldownDurationMs)
	}
	keys, err := srv.store.GetAPIKeys(ctx, bindings[0].ChannelID)
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys=%+v err=%v", keys, err)
	}
	wantHealth := model.APIKeyHealth{Status: "invalid", Reason: "runtime health", StatusCode: 429, CheckedAt: runtimeAt * 1000}
	if keys[0].Health != wantHealth {
		t.Fatalf("key runtime health reset: got=%+v want=%+v", keys[0].Health, wantHealth)
	}
	if keys[0].CooldownUntil != runtimeAt || keys[0].CooldownDurationMs != 60000 {
		t.Fatalf("key runtime cooldown reset: until=%d duration=%d", keys[0].CooldownUntil, keys[0].CooldownDurationMs)
	}
}

type refreshingSiteSyncPreviewAdapter struct {
	*multiKeyProjectionTestAdapter
	refreshCalls int
	routingCalls int
}

func (a *refreshingSiteSyncPreviewAdapter) ListRoutingKeys(ctx context.Context, req provider.AccountRequest) ([]provider.RoutingKeySnapshot, error) {
	a.routingCalls++
	return a.multiKeyProjectionTestAdapter.ListRoutingKeys(ctx, req)
}

func (a *refreshingSiteSyncPreviewAdapter) RefreshCredentials(context.Context, provider.AccountRequest) (provider.Credentials, error) {
	a.refreshCalls++
	return provider.Credentials{
		AccessToken:  "site-refreshed-access",
		RefreshToken: "site-refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour).UnixMilli(),
	}, nil
}

func TestSiteSyncPreviewRetriesAfterCredentialRefresh(t *testing.T) {
	adapter := &refreshingSiteSyncPreviewAdapter{
		multiKeyProjectionTestAdapter: siteSyncPreviewAdapter(
			[]provider.RoutingKeySnapshot{{ID: "refresh-id", Name: "Refresh", Group: "refresh", Key: "sk-refresh", Enabled: true}},
			map[string][]string{"sk-refresh": {"gpt-refresh"}},
		),
	}
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	ctx := context.Background()
	account.Status = model.SiteAccountStatusExpired
	if _, err := srv.store.UpdateSiteAccount(ctx, account.ID, account); err != nil {
		t.Fatal(err)
	}
	expired, err := srv.siteControl.cipher.Seal(provider.Credentials{
		AccessToken:  "site-expired-access",
		RefreshToken: "site-refresh-token",
		ExpiresAt:    time.Now().Add(-time.Minute).UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.UpdateSiteAccountCredential(ctx, account.ID, account.CredentialType, expired, srv.siteControl.cipher.Version()); err != nil {
		t.Fatal(err)
	}

	previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := srv.getConfigPreview(previewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || preview.Items[0].Action != "create" {
		t.Fatalf("preview items=%+v", preview.Items)
	}
	if adapter.refreshCalls != 1 || adapter.routingCalls != 2 {
		t.Fatalf("refresh calls=%d routing calls=%d, want one refresh and one retry", adapter.refreshCalls, adapter.routingCalls)
	}
}

func TestSiteSyncPreviewRepeatedCompleteSnapshotIsUnchanged(t *testing.T) {
	const upstreamKey = "sk-idempotent"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "idempotent-id", Name: "Stable", Group: "stable", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"gpt-stable"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	applySiteSyncPreviewForTest(t, srv, account.ID)

	bindings, err := srv.store.ListSiteChannelBindings(context.Background())
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	before, err := srv.store.GetConfig(context.Background(), bindings[0].ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	previewID, err := srv.discoverSiteSyncPreview(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := srv.getConfigPreview(previewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || preview.Items[0].Action != "unchanged" {
		t.Fatalf("items=%+v", preview.Items)
	}
	queryStore, ok := srv.store.(*sqlstore.SQLStore)
	if !ok {
		t.Fatalf("store type=%T, want *sqlstore.SQLStore", srv.store)
	}
	if _, err := queryStore.ExecContext(context.Background(), "UPDATE site_channel_bindings SET updated_at=? WHERE id=?", int64(12345), bindings[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.applyConfigPreview(context.Background(), previewID); err != nil {
		t.Fatal(err)
	}
	after, err := srv.store.GetConfig(context.Background(), before.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !before.UpdatedAt.Equal(after.UpdatedAt.Time) {
		t.Fatalf("unchanged preview rewrote channel: before=%s after=%s", before.UpdatedAt.Time, after.UpdatedAt.Time)
	}
	bindingAfter, err := srv.store.GetSiteChannelBinding(context.Background(), account.ID, bindings[0].ProjectionKey)
	if err != nil {
		t.Fatal(err)
	}
	if bindingAfter.UpdatedAt != 12345 {
		t.Fatalf("unchanged preview rewrote binding updated_at=%d, want 12345", bindingAfter.UpdatedAt)
	}
}

func TestSiteSyncPreviewDeactivatesRemovedProjectedKeysAndKeepsManualKeys(t *testing.T) {
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{
			{ID: "keep-id", Name: "Keep", Group: "keep", Key: "sk-keep", Enabled: true},
			{ID: "remove-id", Name: "Remove", Group: "remove", Key: "sk-remove", Enabled: true},
			{ID: "manual-id", Name: "Manual", Group: "manual", Key: "sk-manual", Enabled: true},
		},
		map[string][]string{
			"sk-keep":   {"gpt-keep"},
			"sk-remove": {"gpt-remove"},
			"sk-manual": {"gpt-manual"},
		},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	applySiteSyncPreviewForTest(t, srv, account.ID)
	ctx := context.Background()

	bindings, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 3 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	channelIDByModel := map[string]int64{}
	for _, binding := range bindings {
		channel, err := srv.store.GetConfig(ctx, binding.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		for _, modelName := range channel.GetModels() {
			channelIDByModel[modelName] = channel.ID
		}
	}
	if err := srv.store.MarkSiteProjectionManual(ctx, channelIDByModel["gpt-manual"]); err != nil {
		t.Fatal(err)
	}
	adapter.keys = adapter.keys[:1]

	previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := srv.getConfigPreview(previewID)
	if err != nil {
		t.Fatal(err)
	}
	actions := make(map[string]int)
	for _, item := range preview.Items {
		actions[item.Action]++
	}
	if actions["unchanged"] != 1 || actions["deactivate"] != 1 || actions["preserve"] != 1 {
		t.Fatalf("preview actions=%v items=%+v", actions, preview.Items)
	}
	messagesByAction := map[string]string{}
	for _, item := range preview.Items {
		messagesByAction[item.Action] = item.Message
	}
	if messagesByAction["unchanged"] != "upstream_unchanged" ||
		messagesByAction["deactivate"] != "removed_deactivate_history" ||
		messagesByAction["preserve"] != "removed_manual_preserved" {
		t.Fatalf("preview messages=%v", messagesByAction)
	}
	if _, err := srv.applyConfigPreview(ctx, previewID); err != nil {
		t.Fatal(err)
	}

	removed, err := srv.store.GetConfig(ctx, channelIDByModel["gpt-remove"])
	if err != nil {
		t.Fatalf("removed projection was deleted: %v", err)
	}
	if removed.Enabled {
		t.Fatal("removed projection is still enabled")
	}
	manual, err := srv.store.GetConfig(ctx, channelIDByModel["gpt-manual"])
	if err != nil {
		t.Fatal(err)
	}
	if !manual.Enabled {
		t.Fatal("manually owned projection was deactivated")
	}
	bindings, err = srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 3 {
		t.Fatalf("bindings after apply=%+v err=%v", bindings, err)
	}
	for _, binding := range bindings {
		if binding.ChannelID == removed.ID && binding.Status != "inactive" {
			t.Fatalf("removed binding status=%q, want inactive", binding.Status)
		}
		if binding.ChannelID == manual.ID && binding.Ownership != "manual" {
			t.Fatalf("manual binding ownership=%q", binding.Ownership)
		}
	}

	repeatID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := srv.getConfigPreview(repeatID)
	if err != nil {
		t.Fatal(err)
	}
	var alreadyInactive bool
	for _, item := range repeat.Items {
		if item.Action == "unchanged" && item.Message == "removed_already_inactive" {
			alreadyInactive = true
		}
	}
	if !alreadyInactive {
		t.Fatalf("repeat preview missing removed_already_inactive: %+v", repeat.Items)
	}
}

func TestSiteSyncPreviewPreservesUnknownKeysAfterPartialDiscovery(t *testing.T) {
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{
			{ID: "ready-id", Name: "Ready", Group: "ready", Key: "sk-ready", Enabled: true},
			{ID: "unknown-id", Name: "Unknown", Group: "unknown", Key: "sk-unknown", Enabled: true},
		},
		map[string][]string{
			"sk-ready":   {"gpt-ready-old"},
			"sk-unknown": {"gpt-unknown"},
		},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	applySiteSyncPreviewForTest(t, srv, account.ID)
	ctx := context.Background()

	bindings, err := srv.store.ListSiteChannelBindings(ctx)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	channelIDByModel := map[string]int64{}
	for _, binding := range bindings {
		channel, err := srv.store.GetConfig(ctx, binding.ChannelID)
		if err != nil {
			t.Fatal(err)
		}
		for _, modelName := range channel.GetModels() {
			channelIDByModel[modelName] = channel.ID
		}
	}
	adapter.modelsByKey["sk-ready"] = []string{"gpt-ready-new"}
	adapter.errorsByKey["sk-unknown"] = &provider.Error{Code: provider.CodeTimeout, Message: "provider-secret-body"}

	previewID, err := srv.discoverSiteSyncPreview(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := srv.getConfigPreview(previewID)
	if err != nil {
		t.Fatal(err)
	}
	actions := make(map[string]int)
	for _, item := range preview.Items {
		actions[item.Action]++
		if strings.Contains(item.Message, "provider-secret-body") {
			t.Fatalf("preview exposed provider error: %+v", item)
		}
	}
	if actions["update"] != 1 || actions["preserve"] != 1 {
		t.Fatalf("preview actions=%v items=%+v", actions, preview.Items)
	}
	messagesByAction := map[string]string{}
	for _, item := range preview.Items {
		messagesByAction[item.Action] = item.Message
	}
	if messagesByAction["update"] != "projection_update" || messagesByAction["preserve"] != "discovery_incomplete" {
		t.Fatalf("preview messages=%v", messagesByAction)
	}
	if _, err := srv.applyConfigPreview(ctx, previewID); err != nil {
		t.Fatal(err)
	}

	ready, err := srv.store.GetConfig(ctx, channelIDByModel["gpt-ready-old"])
	if err != nil {
		t.Fatal(err)
	}
	if got := ready.GetModels(); len(got) != 1 || got[0] != "gpt-ready-new" {
		t.Fatalf("ready models=%v", got)
	}
	unknown, err := srv.store.GetConfig(ctx, channelIDByModel["gpt-unknown"])
	if err != nil {
		t.Fatal(err)
	}
	if !unknown.Enabled || !slices.Equal(unknown.GetModels(), []string{"gpt-unknown"}) {
		t.Fatalf("unknown key channel changed: enabled=%t models=%v", unknown.Enabled, unknown.GetModels())
	}
}

func TestHandleSiteSyncPreviewEnqueuesTaskAndPublishesPreviewReference(t *testing.T) {
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "handler-id", Name: "Handler", Group: "handler", Key: "sk-handler", Enabled: true}},
		map[string][]string{"sk-handler": {"gpt-handler"}},
	)
	srv, site, account := newSiteSyncPreviewFixture(t, adapter)
	router := gin.New()
	router.POST("/admin/site-accounts/:id/sync-preview", srv.HandleSiteSyncPreview)

	response := serveHTTP(t, router, newRequest(http.MethodPost, fmt.Sprintf("/admin/site-accounts/%d/sync-preview", account.ID), nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	payload := mustParseAPIResponse[struct {
		TaskID string `json:"task_id"`
		Merged bool   `json:"merged"`
	}](t, response.Body.Bytes())
	if payload.Data.TaskID == "" || payload.Data.Merged {
		t.Fatalf("submission=%+v", payload.Data)
	}

	var task *model.SiteTask
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, _ = srv.store.GetSiteTask(context.Background(), payload.Data.TaskID)
		if task != nil && (task.Status == model.SiteTaskStatusSuccess || task.Status == model.SiteTaskStatusFailed || task.Status == model.SiteTaskStatusPartial) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if task == nil || task.Status != model.SiteTaskStatusSuccess {
		t.Fatalf("task=%+v", task)
	}
	if task.Kind != "sync_preview" || task.SiteID != site.ID || task.SiteAccountID != account.ID {
		t.Fatalf("task metadata=%+v", task)
	}
	if !strings.HasPrefix(task.ResultRef, "config_preview:") {
		t.Fatalf("result_ref=%q", task.ResultRef)
	}
	previewID := strings.TrimPrefix(task.ResultRef, "config_preview:")
	preview, err := srv.getConfigPreview(previewID)
	if err != nil || preview.Kind != "site_sync" {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
}

func TestHandleSiteSyncPreviewRejectsDisabledOrDeletedConfiguration(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		setup func(*testing.T, *Server, *model.Site, *model.SiteAccount)
	}{
		{
			name: "account disabled",
			setup: func(t *testing.T, srv *Server, _ *model.Site, account *model.SiteAccount) {
				t.Helper()
				account.Enabled = false
				if _, err := srv.store.UpdateSiteAccount(context.Background(), account.ID, account); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "site disabled",
			setup: func(t *testing.T, srv *Server, site *model.Site, _ *model.SiteAccount) {
				t.Helper()
				site.Enabled = false
				if _, err := srv.store.UpdateSite(context.Background(), site.ID, site); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "account deleted",
			setup: func(t *testing.T, srv *Server, _ *model.Site, account *model.SiteAccount) {
				t.Helper()
				queryStore, ok := srv.store.(*sqlstore.SQLStore)
				if !ok {
					t.Fatalf("store type=%T, want *sqlstore.SQLStore", srv.store)
				}
				if _, err := queryStore.ExecContext(context.Background(), "UPDATE site_accounts SET deleted_at=? WHERE id=?", int64(123456789), account.ID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "site deleted",
			setup: func(t *testing.T, srv *Server, site *model.Site, _ *model.SiteAccount) {
				t.Helper()
				queryStore, ok := srv.store.(*sqlstore.SQLStore)
				if !ok {
					t.Fatalf("store type=%T, want *sqlstore.SQLStore", srv.store)
				}
				if _, err := queryStore.ExecContext(context.Background(), "UPDATE sites SET deleted_at=? WHERE id=?", int64(123456789), site.ID); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			adapter := siteSyncPreviewAdapter(
				[]provider.RoutingKeySnapshot{{ID: "invalid-handler-id", Name: "Invalid", Group: "invalid", Key: "sk-invalid-handler", Enabled: true}},
				map[string][]string{"sk-invalid-handler": {"gpt-invalid-handler"}},
			)
			srv, site, account := newSiteSyncPreviewFixture(t, adapter)
			testCase.setup(t, srv, site, account)
			router := gin.New()
			router.POST("/admin/site-accounts/:id/sync-preview", srv.HandleSiteSyncPreview)

			response := serveHTTP(t, router, newRequest(http.MethodPost, fmt.Sprintf("/admin/site-accounts/%d/sync-preview", account.ID), nil))
			if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "preview_stale") {
				t.Fatalf("status=%d body=%s, want HTTP 409 preview_stale", response.Code, response.Body.String())
			}
			if got := countRows(t, srv.store, "SELECT COUNT(*) FROM site_tasks"); got != 0 {
				t.Fatalf("site_tasks=%d, want no task submission", got)
			}
		})
	}
}

type blockingSiteSyncPreviewAdapter struct {
	*multiKeyProjectionTestAdapter
	started chan struct{}
	release chan struct{}
}

func (a *blockingSiteSyncPreviewAdapter) ListRoutingKeys(ctx context.Context, req provider.AccountRequest) ([]provider.RoutingKeySnapshot, error) {
	select {
	case <-a.started:
	default:
		close(a.started)
	}
	select {
	case <-a.release:
		return a.multiKeyProjectionTestAdapter.ListRoutingKeys(ctx, req)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestSiteSyncPreviewMergesDuplicateSubmission(t *testing.T) {
	adapter := &blockingSiteSyncPreviewAdapter{
		multiKeyProjectionTestAdapter: siteSyncPreviewAdapter(
			[]provider.RoutingKeySnapshot{{ID: "merge-id", Name: "Merge", Group: "merge", Key: "sk-merge", Enabled: true}},
			map[string][]string{"sk-merge": {"gpt-merge"}},
		),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)

	first, firstMerged, err := srv.submitSiteSyncPreviewTask(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	if firstMerged {
		t.Fatal("first submission was merged")
	}
	select {
	case <-adapter.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first submission did not start discovery")
	}
	second, secondMerged, err := srv.submitSiteSyncPreviewTask(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	if !secondMerged || second.id != first.id {
		t.Fatalf("second submission=%+v merged=%t, first=%+v", second, secondMerged, first)
	}
	close(adapter.release)
	select {
	case <-first.done:
	case <-time.After(3 * time.Second):
		t.Fatal("merged task did not finish")
	}
}

func TestSiteSyncPreviewConflictsOnChangeOutsideBindingHash(t *testing.T) {
	const upstreamKey = "sk-conflict"
	adapter := siteSyncPreviewAdapter(
		[]provider.RoutingKeySnapshot{{ID: "conflict-id", Name: "Conflict", Group: "conflict", Key: upstreamKey, Enabled: true}},
		map[string][]string{upstreamKey: {"gpt-old"}},
	)
	srv, _, account := newSiteSyncPreviewFixture(t, adapter)
	applySiteSyncPreviewForTest(t, srv, account.ID)
	bindings, err := srv.store.ListSiteChannelBindings(context.Background())
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	channelBefore, err := srv.store.GetConfig(context.Background(), bindings[0].ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an external writer that changed projected fields without taking
	// manual ownership, leaving the binding hash stale.
	queryStore, ok := srv.store.(*sqlstore.SQLStore)
	if !ok {
		t.Fatalf("store type=%T, want *sqlstore.SQLStore", srv.store)
	}
	if _, err := queryStore.ExecContext(context.Background(), "UPDATE channel_models SET model=? WHERE channel_id=? AND model=?", "gpt-local-change", channelBefore.ID, "gpt-old"); err != nil {
		t.Fatal(err)
	}
	channelBefore, err = srv.store.GetConfig(context.Background(), channelBefore.ID)
	if err != nil {
		t.Fatal(err)
	}
	keysBefore, err := srv.store.GetAPIKeys(context.Background(), bindings[0].ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if siteSyncBindingMatchesChannel(bindings[0], channelBefore, keysBefore) {
		t.Fatalf("local model change did not break binding match: binding=%+v channel=%+v keys=%+v", bindings[0], channelBefore, keysBefore)
	}
	adapter.modelsByKey[upstreamKey] = []string{"gpt-new"}

	previewID, err := srv.discoverSiteSyncPreview(context.Background(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := srv.getConfigPreview(previewID)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || preview.Items[0].Action != "conflict" || preview.CanApply {
		t.Fatalf("preview=%+v", preview)
	}
	if preview.Items[0].Message != "projection_external_change" {
		t.Fatalf("conflict message=%q, want projection_external_change", preview.Items[0].Message)
	}
	if _, err := srv.applyConfigPreview(context.Background(), previewID); err == nil {
		t.Fatal("conflicted preview was applied")
	}
	channel, err := srv.store.GetConfig(context.Background(), bindings[0].ChannelID)
	if err != nil {
		t.Fatal(err)
	}
	if got := channel.GetModels(); len(got) != 1 || got[0] != "gpt-local-change" {
		t.Fatalf("conflicted preview changed models=%v", got)
	}
}
