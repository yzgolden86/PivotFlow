package cooldown

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/testutil"
	"github.com/yzgolden86/PivotFlow/internal/util"
)

func TestDecide_ClientError(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	// Context length exceeded errors are true client errors
	decision, err := mgr.Decide(context.Background(), ErrorInput{
		ChannelID:      1,
		KeyIndex:       0,
		StatusCode:     400,
		ErrorBody:      []byte(`{"error":{"type":"invalid_request_error","message":"Maximum context length exceeded"}}`),
		IsNetworkError: false,
	})

	if err != nil {
		t.Fatalf("Decide() unexpected error: %v", err)
	}

	if decision.Retry != RetryNone {
		t.Errorf("Retry = %v, want RetryNone", decision.Retry)
	}
	if decision.Effect != EffectNone {
		t.Errorf("Effect = %v, want EffectNone", decision.Effect)
	}
	if err := decision.Validate(); err != nil {
		t.Errorf("Decision.Validate() failed: %v", err)
	}
}

func TestDecide_KeyLevelError(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	decision, err := mgr.Decide(context.Background(), ErrorInput{
		ChannelID:      1,
		KeyIndex:       0,
		StatusCode:     401,
		ErrorBody:      []byte(`{"error":"unauthorized"}`),
		IsNetworkError: false,
	})

	if err != nil {
		t.Fatalf("Decide() unexpected error: %v", err)
	}

	if decision.Retry != RetryNextKey {
		t.Errorf("Retry = %v, want RetryNextKey", decision.Retry)
	}
	if decision.Effect != EffectCoolKey {
		t.Errorf("Effect = %v, want EffectCoolKey", decision.Effect)
	}
	if err := decision.Validate(); err != nil {
		t.Errorf("Decision.Validate() failed: %v", err)
	}
}

func TestDecide_ModelScopedError(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	decision, err := mgr.Decide(context.Background(), ErrorInput{
		ChannelID:      1,
		KeyIndex:       0,
		Model:          "gpt-4",
		StatusCode:     429,
		ErrorBody:      []byte(`{"error":{"code":"model_cooldown","message":"model temporarily unavailable","model":"gpt-4"}}`),
		IsNetworkError: false,
	})

	if err != nil {
		t.Fatalf("Decide() unexpected error: %v", err)
	}

	if decision.Retry != RetryNextChannel {
		t.Errorf("Retry = %v, want RetryNextChannel", decision.Retry)
	}
	if decision.Effect != EffectCoolModel {
		t.Errorf("Effect = %v, want EffectCoolModel", decision.Effect)
	}
	if decision.Model != "gpt-4" {
		t.Errorf("Model = %q, want \"gpt-4\"", decision.Model)
	}
	if err := decision.Validate(); err != nil {
		t.Errorf("Decision.Validate() failed: %v", err)
	}
}

func TestDecide_ChannelLevelError(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	decision, err := mgr.Decide(context.Background(), ErrorInput{
		ChannelID:      1,
		KeyIndex:       0,
		StatusCode:     503,
		ErrorBody:      []byte(`{"error":"service unavailable"}`),
		IsNetworkError: false,
	})

	if err != nil {
		t.Fatalf("Decide() unexpected error: %v", err)
	}

	if decision.Retry != RetryNextChannel {
		t.Errorf("Retry = %v, want RetryNextChannel", decision.Retry)
	}
	if decision.Effect != EffectCoolChannel {
		t.Errorf("Effect = %v, want EffectCoolChannel", decision.Effect)
	}
	if err := decision.Validate(); err != nil {
		t.Errorf("Decision.Validate() failed: %v", err)
	}
}

func TestDecide_NetworkError(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	decision, err := mgr.Decide(context.Background(), ErrorInput{
		ChannelID:      1,
		KeyIndex:       0,
		StatusCode:     502,
		IsNetworkError: true,
	})

	if err != nil {
		t.Fatalf("Decide() unexpected error: %v", err)
	}

	if decision.Retry != RetryNextChannel {
		t.Errorf("Retry = %v, want RetryNextChannel", decision.Retry)
	}
	if decision.Effect != EffectCoolChannel {
		t.Errorf("Effect = %v, want EffectCoolChannel", decision.Effect)
	}
	if err := decision.Validate(); err != nil {
		t.Errorf("Decision.Validate() failed: %v", err)
	}
}

func TestDecide_WithPreciseResetTime(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	resetTime := time.Now().Add(5 * time.Minute)
	headers := map[string][]string{
		"Anthropic-Ratelimit-Unified-Reset": {strconv.FormatInt(resetTime.Unix(), 10)},
	}

	decision, err := mgr.Decide(context.Background(), ErrorInput{
		ChannelID:      1,
		KeyIndex:       0,
		Model:          "claude-3-5-sonnet-20241022",
		StatusCode:     429,
		ErrorBody:      []byte(`{"error":{"type":"rate_limit_error","message":"rate limited"}}`),
		Headers:        headers,
		IsNetworkError: false,
	})

	if err != nil {
		t.Fatalf("Decide() unexpected error: %v", err)
	}

	if decision.Retry != RetryNextChannel {
		t.Errorf("Retry = %v, want RetryNextChannel", decision.Retry)
	}
	if decision.Effect != EffectCoolModel {
		t.Errorf("Effect = %v, want EffectCoolModel", decision.Effect)
	}
	if !decision.HasModelCooldownUntil {
		t.Error("HasModelCooldownUntil = false, want true")
	}
	if decision.PreventKeyFallback != true {
		t.Error("PreventKeyFallback = false, want true (unified reset header)")
	}
	if err := decision.Validate(); err != nil {
		t.Errorf("Decision.Validate() failed: %v", err)
	}
}

func TestDecide_ConfiguredRule(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	rules := &model.CooldownDetectionRules{
		Rules: []model.CooldownDetectionRule{
			{
				RuleID:          "test-rule-001",
				Enabled:         true,
				Name:            "Internal Server Error",
				Priority:        0,
				StatusCodes:     []int{500},
				MessagePattern:  "internal.*error",
				Scope:           model.CooldownScopeChannel,
				Mode:            model.CooldownModeFixed,
				CooldownSeconds: 60,
			},
		},
	}

	decision, err := mgr.Decide(context.Background(), ErrorInput{
		ChannelID:              1,
		KeyIndex:               0,
		StatusCode:             500,
		ErrorBody:              []byte(`{"error":"internal server error"}`),
		IsNetworkError:         false,
		CooldownDetectionRules: rules,
	})

	if err != nil {
		t.Fatalf("Decide() unexpected error: %v", err)
	}

	t.Logf("Decision: Retry=%v Effect=%v HasChannelCooldownUntil=%v CooldownReason=%q",
		decision.Retry, decision.Effect, decision.HasChannelCooldownUntil, decision.CooldownReason)

	if decision.Retry != RetryNextChannel {
		t.Errorf("Retry = %v, want RetryNextChannel", decision.Retry)
	}
	if decision.Effect != EffectCoolChannel {
		t.Errorf("Effect = %v, want EffectCoolChannel", decision.Effect)
	}
	if !decision.HasChannelCooldownUntil {
		t.Error("HasChannelCooldownUntil = false, want true (configured rule)")
	}
	if decision.CooldownReason != "configured_rule_0" {
		t.Errorf("CooldownReason = %q, want \"configured_rule_0\"", decision.CooldownReason)
	}
	if err := decision.Validate(); err != nil {
		t.Errorf("Decision.Validate() failed: %v", err)
	}
}

func TestDecide_ModelScopedNetworkError(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	decision, err := mgr.Decide(context.Background(), ErrorInput{
		ChannelID:      1,
		KeyIndex:       0,
		Model:          "gpt-4",
		StatusCode:     util.StatusStreamIncomplete,
		IsNetworkError: true,
		ModelScoped:    true,
	})

	if err != nil {
		t.Fatalf("Decide() unexpected error: %v", err)
	}

	if decision.Retry != RetryNextChannel {
		t.Errorf("Retry = %v, want RetryNextChannel", decision.Retry)
	}
	if decision.Effect != EffectCoolModel {
		t.Errorf("Effect = %v, want EffectCoolModel", decision.Effect)
	}
	if decision.Model != "gpt-4" {
		t.Errorf("Model = %q, want \"gpt-4\"", decision.Model)
	}
	if err := decision.Validate(); err != nil {
		t.Errorf("Decision.Validate() failed: %v", err)
	}
}

func TestDecide_RoundTripWithLegacyAction(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	tests := []struct {
		name       string
		input      ErrorInput
		wantAction Action
	}{
		{
			name: "401 → ActionRetryKey",
			input: ErrorInput{
				ChannelID:      1,
				KeyIndex:       0,
				StatusCode:     401,
				ErrorBody:      []byte(`{"error":"unauthorized"}`),
				IsNetworkError: false,
			},
			wantAction: ActionRetryKey,
		},
		{
			name: "503 → ActionRetryChannel",
			input: ErrorInput{
				ChannelID:      1,
				KeyIndex:       0,
				StatusCode:     503,
				ErrorBody:      []byte(`{"error":"service unavailable"}`),
				IsNetworkError: false,
			},
			wantAction: ActionRetryChannel,
		},
		{
			name: "context_length_exceeded → ActionReturnClient",
			input: ErrorInput{
				ChannelID:      1,
				KeyIndex:       0,
				StatusCode:     400,
				ErrorBody:      []byte(`{"error":{"type":"invalid_request_error","message":"Maximum context length exceeded"}}`),
				IsNetworkError: false,
			},
			wantAction: ActionReturnClient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := mgr.Decide(context.Background(), tt.input)
			if err != nil {
				t.Fatalf("Decide() error: %v", err)
			}

			legacyAction := decision.ToLegacyAction()
			if legacyAction != tt.wantAction {
				t.Errorf("ToLegacyAction() = %v, want %v", legacyAction, tt.wantAction)
			}

			// Also test that DecideAction returns the same result
			directAction := mgr.DecideAction(context.Background(), tt.input)
			if directAction != tt.wantAction {
				t.Errorf("DecideAction() = %v, want %v", directAction, tt.wantAction)
			}
		})
	}
}
