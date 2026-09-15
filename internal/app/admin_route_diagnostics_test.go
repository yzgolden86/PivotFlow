package app

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func createDiagnosticChannel(t *testing.T, server *Server, name string, priority, keyCount int) *model.Config {
	t.Helper()
	ctx := context.Background()
	cfg, err := server.store.CreateConfig(ctx, &model.Config{
		Name: name, URLs: model.ChannelURLs{{URL: "https://" + name + ".example"}}, Priority: priority,
		Enabled: true, ModelEntries: []model.ModelEntry{{Model: "gpt-route"}},
	})
	if err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	keys := make([]*model.APIKey, 0, keyCount)
	for index := range keyCount {
		keys = append(keys, &model.APIKey{ChannelID: cfg.ID, KeyIndex: index, APIKey: name + "-key"})
	}
	if err := server.store.CreateAPIKeysBatch(ctx, keys); err != nil {
		t.Fatalf("create channel keys %s: %v", name, err)
	}
	return cfg
}

func TestBuildChannelRouteDiagnosticsExplainsPriorityAndWeightedShare(t *testing.T) {
	server, _, cleanup := setupAdminTestServer(t)
	defer cleanup()

	target := createDiagnosticChannel(t, server, "target", 1, 1)
	_ = createDiagnosticChannel(t, server, "peer", 1, 3)
	_ = createDiagnosticChannel(t, server, "higher", 2, 1)

	result, err := server.buildChannelRouteDiagnostics(context.Background(), target.ID, "gpt-route", "openai", 0)
	if err != nil {
		t.Fatalf("build diagnostics: %v", err)
	}
	if result.RouteStrategy != RouteStrategyBalanced {
		t.Fatalf("route strategy=%q, want %q", result.RouteStrategy, RouteStrategyBalanced)
	}
	if !result.Target.Candidate {
		t.Fatalf("target should be candidate: %+v", result.Target.Reasons)
	}
	if result.Target.HigherPriorityCount != 1 {
		t.Fatalf("higher priority count=%d, want 1", result.Target.HigherPriorityCount)
	}
	if result.Target.SamePriorityCount != 2 {
		t.Fatalf("same priority count=%d, want 2", result.Target.SamePriorityCount)
	}
	if math.Abs(result.Target.EstimatedTrafficShare-0.25) > 0.0001 {
		t.Fatalf("estimated share=%v, want 0.25", result.Target.EstimatedTrafficShare)
	}
	if result.Target.CandidatePosition != 2 {
		t.Fatalf("target priority tier=%d, want 2", result.Target.CandidatePosition)
	}
	if len(result.Candidates) != 3 || result.Candidates[0].ChannelName != "higher" {
		t.Fatalf("candidate order=%+v, want higher first", result.Candidates)
	}
	for _, candidate := range result.Candidates {
		if candidate.ChannelName == "peer" && candidate.CandidatePosition != result.Target.CandidatePosition {
			t.Fatalf("same-priority peer tier=%d, target tier=%d", candidate.CandidatePosition, result.Target.CandidatePosition)
		}
	}
}

func TestBuildChannelRouteDiagnosticsExplainsTokenRestriction(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	target := createDiagnosticChannel(t, server, "restricted", 1, 1)
	allowed := createDiagnosticChannel(t, server, "allowed", 1, 1)
	token := &model.AuthToken{
		Token: "route-diagnostic-token-hash", Description: "restricted token", IsActive: true,
		AllowedChannelIDs: []int64{allowed.ID}, ChannelRestrictionMode: model.ChannelRestrictionModeAllow,
	}
	if err := store.CreateAuthToken(context.Background(), token); err != nil {
		t.Fatalf("create token: %v", err)
	}

	result, err := server.buildChannelRouteDiagnostics(context.Background(), target.ID, "gpt-route", "openai", token.ID)
	if err != nil {
		t.Fatalf("build diagnostics: %v", err)
	}
	if result.Target.Candidate {
		t.Fatal("restricted target must not remain a candidate")
	}
	found := false
	for _, reason := range result.Target.Reasons {
		if reason.Code == "token_channel_restriction" && reason.Blocking {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing token restriction reason: %+v", result.Target.Reasons)
	}
}

func TestBuildChannelRouteDiagnosticsCountsKeysByModelScope(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	ctx := context.Background()

	target := createDiagnosticChannel(t, server, "scoped", 1, 4)
	metadata := map[int]model.APIKey{
		0: {AllowedModels: []string{"other-model"}},
		1: {AllowedModels: []string{"gpt-route"}},
		2: {ModelScopeEmpty: true},
		3: {AllowedModels: []string{"*"}},
	}
	if err := store.UpdateAPIKeyMetadata(ctx, target.ID, metadata); err != nil {
		t.Fatalf("update key metadata: %v", err)
	}
	if err := store.SetKeyCooldown(ctx, target.ID, 1, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("set key cooldown: %v", err)
	}

	result, err := server.buildChannelRouteDiagnostics(ctx, target.ID, "gpt-route", "openai", 0)
	if err != nil {
		t.Fatalf("build diagnostics: %v", err)
	}
	if result.Target.EnabledKeyCount != 4 {
		t.Fatalf("enabled key count=%d, want 4", result.Target.EnabledKeyCount)
	}
	if result.Target.ModelEligibleKeyCount != 2 {
		t.Fatalf("model eligible key count=%d, want 2", result.Target.ModelEligibleKeyCount)
	}
	if result.Target.ActiveKeyCount != 1 {
		t.Fatalf("active key count=%d, want 1", result.Target.ActiveKeyCount)
	}
}

func TestBuildChannelRouteDiagnosticsCalculatesActualShareFromLogs(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	ctx := context.Background()

	target := createDiagnosticChannel(t, server, "actual-target", 1, 1)
	peer := createDiagnosticChannel(t, server, "actual-peer", 1, 1)
	token := &model.AuthToken{Token: "actual-share-token-hash", Description: "actual share token", IsActive: true}
	if err := store.CreateAuthToken(ctx, token); err != nil {
		t.Fatalf("create token: %v", err)
	}
	now := time.Now()
	logs := []*model.LogEntry{
		{Time: model.JSONTime{Time: now}, Model: "gpt-route", LogSource: model.LogSourceProxy, ChannelID: target.ID, StatusCode: 200, AuthTokenID: token.ID},
		{Time: model.JSONTime{Time: now}, Model: "gpt-route", LogSource: model.LogSourceProxy, ChannelID: target.ID, StatusCode: 200, AuthTokenID: token.ID},
		{Time: model.JSONTime{Time: now}, Model: "gpt-route", LogSource: model.LogSourceProxy, ChannelID: target.ID, StatusCode: 500, AuthTokenID: token.ID},
		{Time: model.JSONTime{Time: now}, Model: "gpt-route", LogSource: model.LogSourceProxy, ChannelID: peer.ID, StatusCode: 200, AuthTokenID: token.ID},
		{Time: model.JSONTime{Time: now}, Model: "gpt-route", LogSource: model.LogSourceProxy, ChannelID: target.ID, StatusCode: 200},
	}
	for index, entry := range logs {
		if err := store.AddLog(ctx, entry); err != nil {
			t.Fatalf("add log %d: %v", index, err)
		}
	}

	result, err := server.buildChannelRouteDiagnostics(ctx, target.ID, "gpt-route", "openai", token.ID)
	if err != nil {
		t.Fatalf("build diagnostics: %v", err)
	}
	if result.ActualTotalRequests != 4 {
		t.Fatalf("actual total requests=%d, want 4", result.ActualTotalRequests)
	}
	if result.Target.ActualRequests != 3 || result.Target.ActualShare != 0.75 {
		t.Fatalf("target actual=%d/%v, want 3/0.75", result.Target.ActualRequests, result.Target.ActualShare)
	}
	var peerActual int64
	var peerShare float64
	for _, candidate := range result.Candidates {
		if candidate.ChannelID == peer.ID {
			peerActual = candidate.ActualRequests
			peerShare = candidate.ActualShare
		}
	}
	if peerActual != 1 || peerShare != 0.25 {
		t.Fatalf("peer actual=%d/%v, want 1/0.25", peerActual, peerShare)
	}
}

func TestBuildChannelRouteDiagnosticsReportsStickyChannel(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	ctx := context.Background()

	target := createDiagnosticChannel(t, server, "sticky-target", 1, 1)
	token := &model.AuthToken{Token: "sticky-token-hash", Description: "sticky token", IsActive: true}
	if err := store.CreateAuthToken(ctx, token); err != nil {
		t.Fatalf("create token: %v", err)
	}
	server.routeStrategyMode = RouteStrategySticky
	server.stickyRouter = newStickyRouter()
	server.stickyRouter.remember(stickyScopeKey(token.Token), "gpt-route", target.ID)

	result, err := server.buildChannelRouteDiagnostics(ctx, target.ID, "gpt-route", "openai", token.ID)
	if err != nil {
		t.Fatalf("build diagnostics: %v", err)
	}
	if result.Sticky == nil {
		t.Fatal("sticky snapshot is missing")
	}
	if result.Sticky.ChannelID != target.ID || result.Sticky.ChannelName != target.Name || !result.Sticky.InCandidatePool {
		t.Fatalf("sticky snapshot=%+v, want target %q in candidate pool", result.Sticky, target.Name)
	}
	if result.Sticky.RememberedAt.IsZero() || !result.Sticky.ExpiresAt.After(result.Sticky.RememberedAt) {
		t.Fatalf("sticky timestamps are invalid: %+v", result.Sticky)
	}
}
