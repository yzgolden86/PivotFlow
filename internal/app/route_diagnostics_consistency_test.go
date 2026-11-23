package app

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func changeDiagnosticConfig(t *testing.T, server *Server, id int64, change func(*model.Config)) {
	t.Helper()
	cfg, err := server.store.GetConfig(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	change(cfg)
	if _, err := server.store.UpdateConfig(context.Background(), id, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestRouteDiagnosticsUnifiedPoolIncludesDirectAndAlias(t *testing.T) {
	server, _, cleanup := setupAdminTestServer(t)
	defer cleanup()
	server.channelBalancer = NewSmoothWeightedRR()
	direct := createDiagnosticChannel(t, server, "direct", 10, 1)
	alias := createDiagnosticChannel(t, server, "alias", 10, 1)
	wildcard := createDiagnosticChannel(t, server, "wildcard", 10, 1)
	changeDiagnosticConfig(t, server, alias.ID, func(cfg *model.Config) {
		cfg.ModelEntries = []model.ModelEntry{{Model: "vendor/gpt-route"}}
	})
	changeDiagnosticConfig(t, server, wildcard.ID, func(cfg *model.Config) {
		cfg.ModelEntries = []model.ModelEntry{{Model: "*"}}
	})
	server.modelAliases = newModelAliasRegistry([]model.ModelAliasGroup{
		{Canonical: "gpt-route", Aliases: []string{"vendor/gpt-route"}, Enabled: true},
	})

	actual, err := server.selectCandidatesByModelAndClientProtocolAnonymous(context.Background(), "gpt-route", "openai")
	if err != nil || len(actual) != 2 {
		t.Fatalf("actual selector must include the two explicit group channels: %v / %v", actual, err)
	}
	result, err := server.buildChannelRouteDiagnostics(context.Background(), direct.ID, "gpt-route", "openai", 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		ids = append(ids, candidate.ChannelID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []int64{direct.ID, alias.ID}) {
		t.Fatalf("diagnostic pool=%v, want direct and alias only, wildcard %d must wait for fallback", ids, wildcard.ID)
	}
}

func TestRouteDiagnosticsWeightMatchesSelector(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	ctx := context.Background()
	target := createDiagnosticChannel(t, server, "weight-target", 10, 4)
	createDiagnosticChannel(t, server, "weight-peer", 10, 1)
	if err := store.UpdateAPIKeyMetadata(ctx, target.ID, map[int]model.APIKey{
		0: {AllowedModels: []string{"other-model"}},
		1: {AllowedModels: []string{"gpt-route"}},
		2: {ModelScopeEmpty: true},
		3: {AllowedModels: []string{"*"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetKeyCooldown(ctx, target.ID, 1, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	result, err := server.buildChannelRouteDiagnostics(ctx, target.ID, "gpt-route", "openai", 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Target.ActiveKeyCount != 1 || result.Target.ModelEligibleKeyCount != 2 {
		t.Fatalf("request-name scope counts must remain distinct: %+v", result.Target)
	}
	if math.Abs(result.Target.EstimatedTrafficShare-0.75) > 1e-9 {
		t.Fatalf("weight reference=%v, want 3/(3+1)=0.75; scope has 1 available key", result.Target.EstimatedTrafficShare)
	}
}

func TestRouteDiagnosticsDistinguishesCandidateFromNoKeyAttempt(t *testing.T) {
	server, _, cleanup := setupAdminTestServer(t)
	defer cleanup()
	server.channelBalancer = NewSmoothWeightedRR()
	target := createDiagnosticChannel(t, server, "no-key", 10, 0)
	actual, err := server.selectCandidatesByModelAndClientProtocolAnonymous(context.Background(), "gpt-route", "openai")
	if err != nil || len(actual) != 1 || actual[0].ID != target.ID {
		t.Fatalf("the real candidate stage retains the no-key channel: %v / %v", actual, err)
	}
	result, err := server.buildChannelRouteDiagnostics(context.Background(), target.ID, "gpt-route", "openai", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Target.Candidate {
		t.Fatalf("no-key is an attempt-stage exclusion, not a candidate-stage exclusion: %+v", result.Target)
	}
	if result.PoolMode != "exact" {
		t.Fatalf("no-key candidate was falsely labelled a cooldown probe: pool mode=%q", result.PoolMode)
	}
}

func TestRouteDiagnosticsExcludesOutsideAvailableTime(t *testing.T) {
	server, _, cleanup := setupAdminTestServer(t)
	defer cleanup()
	server.channelBalancer = NewSmoothWeightedRR()
	target := createDiagnosticChannel(t, server, "outside-window", 10, 1)
	now := time.Now()
	changeDiagnosticConfig(t, server, target.ID, func(cfg *model.Config) {
		cfg.AvailableTimeStart = now.Add(time.Hour).Format("15:04")
		cfg.AvailableTimeEnd = now.Add(2 * time.Hour).Format("15:04")
	})
	actual, err := server.selectCandidatesByModelAndClientProtocolAnonymous(context.Background(), "gpt-route", "openai")
	if err != nil || len(actual) != 0 {
		t.Fatalf("actual selector must exclude the time window: %v / %v", actual, err)
	}
	result, err := server.buildChannelRouteDiagnostics(context.Background(), target.ID, "gpt-route", "openai", 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Target.Candidate {
		t.Fatal("diagnostic included a channel outside its configured time window")
	}
}

func TestRouteDiagnosticsHealthUsesPoolBeforeTokenRestriction(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	ctx := context.Background()
	a := createDiagnosticChannel(t, server, "median-a", 100, 1)
	b := createDiagnosticChannel(t, server, "median-b", 100, 1)
	c := createDiagnosticChannel(t, server, "median-c", 100, 1)
	server.healthCache = &HealthCache{config: model.HealthScoreConfig{
		Enabled: true, EnableTTFBScore: true, TTFBPenaltyWeight: 20,
		TTFBMaxSlowRatio: 2, TTFBMinConfidentSample: 10,
	}}
	stats := map[int64]model.ChannelHealthStats{
		a.ID: {SuccessRate: 1, SampleCount: 100, AvgFirstByteSeconds: 5, FirstByteSampleCount: 20},
		b.ID: {SuccessRate: 1, SampleCount: 100, AvgFirstByteSeconds: 1, FirstByteSampleCount: 20},
		c.ID: {SuccessRate: 1, SampleCount: 100, AvgFirstByteSeconds: 20, FirstByteSampleCount: 20},
	}
	server.healthCache.healthStats.Store(&stats)
	token := &model.AuthToken{Token: "synthetic-median-token", IsActive: true, AllowedChannelIDs: []int64{a.ID, c.ID}}
	if err := store.CreateAuthToken(ctx, token); err != nil {
		t.Fatal(err)
	}
	result, err := server.buildChannelRouteDiagnostics(ctx, c.ID, "gpt-route", "openai", token.ID)
	if err != nil {
		t.Fatal(err)
	}
	// The real selector's median is 5, before the token removes b. C is 4x slower,
	// so the slow ratio is capped at 2 and its 100 priority receives a 40 penalty.
	if result.Target.EffectivePriority != 60 {
		t.Fatalf("effective priority=%v, want 60 from the full pre-token pool", result.Target.EffectivePriority)
	}
}

func TestRouteDiagnosticsReadOnlyDoesNotAdvanceRealRoundRobin(t *testing.T) {
	server, _, cleanup := setupAdminTestServer(t)
	defer cleanup()
	server.channelBalancer = NewSmoothWeightedRR()
	a := createDiagnosticChannel(t, server, "readonly-a", 10, 1)
	b := createDiagnosticChannel(t, server, "readonly-b", 10, 1)
	for _, want := range []int64{a.ID, b.ID, a.ID, b.ID} {
		for range 3 {
			if _, err := server.buildChannelRouteDiagnostics(context.Background(), a.ID, "gpt-route", "openai", 0); err != nil {
				t.Fatal(err)
			}
		}
		actual, err := server.selectCandidatesByModelAndClientProtocolAnonymous(context.Background(), "gpt-route", "openai")
		if err != nil || len(actual) != 2 || actual[0].ID != want {
			t.Fatalf("diagnostic consumed a turn: selection=%v err=%v want first=%d", actual, err, want)
		}
	}
}
