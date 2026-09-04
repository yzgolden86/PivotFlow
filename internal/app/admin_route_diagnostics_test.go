package app

import (
	"context"
	"math"
	"testing"

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
