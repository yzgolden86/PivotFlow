package cooldown

import (
	"testing"
	"time"
)

func TestDecisionValidate(t *testing.T) {
	now := time.Now()
	future := now.Add(5 * time.Minute)

	tests := []struct {
		name    string
		decision Decision
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid: RetryNextKey with EffectCoolKey",
			decision: Decision{
				Retry:               RetryNextKey,
				Effect:              EffectCoolKey,
				KeyCooldownUntil:    future,
				HasKeyCooldownUntil: true,
			},
			wantErr: false,
		},
		{
			name: "valid: RetryNextChannel with EffectCoolModel",
			decision: Decision{
				Retry:                 RetryNextChannel,
				Effect:                EffectCoolModel,
				Model:                 "gpt-4",
				ModelCooldownUntil:    future,
				HasModelCooldownUntil: true,
			},
			wantErr: false,
		},
		{
			name: "valid: RetryNextChannel with EffectCoolChannel",
			decision: Decision{
				Retry:                   RetryNextChannel,
				Effect:                  EffectCoolChannel,
				ChannelCooldownUntil:    future,
				HasChannelCooldownUntil: true,
			},
			wantErr: false,
		},
		{
			name: "valid: RetryNone with EffectNone (client error)",
			decision: Decision{
				Retry:  RetryNone,
				Effect: EffectNone,
			},
			wantErr: false,
		},
		{
			name: "valid: RetryNextChannel with EffectNone (protocol negotiation)",
			decision: Decision{
				Retry:  RetryNextChannel,
				Effect: EffectNone,
			},
			wantErr: false,
		},
		{
			name: "valid: RetryNone with EffectClearCooldowns (success)",
			decision: Decision{
				Retry:  RetryNone,
				Effect: EffectClearCooldowns,
			},
			wantErr: false,
		},
		{
			name: "valid: RetryNextKey with EffectRecordFailure",
			decision: Decision{
				Retry:  RetryNextKey,
				Effect: EffectRecordFailure,
			},
			wantErr: false,
		},
		{
			name: "invalid: multiple cooldown effects",
			decision: Decision{
				Retry:                   RetryNextChannel,
				Effect:                  EffectCoolKey,
				KeyCooldownUntil:        future,
				HasKeyCooldownUntil:     true,
				ChannelCooldownUntil:    future,
				HasChannelCooldownUntil: true,
			},
			wantErr: true,
			errMsg:  "at most one cooldown effect",
		},
		{
			name: "invalid: RetryNone with cooldown effect",
			decision: Decision{
				Retry:               RetryNone,
				Effect:              EffectCoolKey,
				KeyCooldownUntil:    future,
				HasKeyCooldownUntil: true,
			},
			wantErr: true,
			errMsg:  "RetryNone should not cool resources",
		},
		{
			name: "valid: EffectCoolKey without explicit time (exponential backoff)",
			decision: Decision{
				Retry:               RetryNextKey,
				Effect:              EffectCoolKey,
				HasKeyCooldownUntil: false,
			},
			wantErr: false,
		},
		{
			name: "invalid: EffectCoolModel without Model",
			decision: Decision{
				Retry:                 RetryNextChannel,
				Effect:                EffectCoolModel,
				ModelCooldownUntil:    future,
				HasModelCooldownUntil: true,
			},
			wantErr: true,
			errMsg:  "EffectCoolModel requires non-empty Model",
		},
		{
			name: "valid: EffectCoolModel without explicit time (exponential backoff)",
			decision: Decision{
				Retry:                 RetryNextChannel,
				Effect:                EffectCoolModel,
				Model:                 "gpt-4",
				HasModelCooldownUntil: false,
			},
			wantErr: false,
		},
		{
			name: "valid: EffectCoolChannel without explicit time (exponential backoff)",
			decision: Decision{
				Retry:                   RetryNextChannel,
				Effect:                  EffectCoolChannel,
				HasChannelCooldownUntil: false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.decision.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate() expected error containing %q, got nil", tt.errMsg)
				}
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Fatalf("Validate() error = %q, want substring %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("Validate() unexpected error: %v", err)
				}
			}
		})
	}
}

func TestDecisionToLegacyAction(t *testing.T) {
	tests := []struct {
		name     string
		decision Decision
		want     Action
	}{
		{
			name: "EffectCoolKey → ActionRetryKey",
			decision: Decision{
				Retry:  RetryNextKey,
				Effect: EffectCoolKey,
			},
			want: ActionRetryKey,
		},
		{
			name: "EffectCoolModel → ActionRetryModel",
			decision: Decision{
				Retry:  RetryNextChannel,
				Effect: EffectCoolModel,
			},
			want: ActionRetryModel,
		},
		{
			name: "EffectCoolChannel → ActionRetryChannel",
			decision: Decision{
				Retry:  RetryNextChannel,
				Effect: EffectCoolChannel,
			},
			want: ActionRetryChannel,
		},
		{
			name: "EffectClearCooldowns → ActionReturnClient",
			decision: Decision{
				Retry:  RetryNone,
				Effect: EffectClearCooldowns,
			},
			want: ActionReturnClient,
		},
		{
			name: "EffectNone + RetryNextKey → ActionRetryKey",
			decision: Decision{
				Retry:  RetryNextKey,
				Effect: EffectNone,
			},
			want: ActionRetryKey,
		},
		{
			name: "EffectNone + RetryNextChannel → ActionRetryChannel",
			decision: Decision{
				Retry:  RetryNextChannel,
				Effect: EffectNone,
			},
			want: ActionRetryChannel,
		},
		{
			name: "EffectNone + RetryNextURL → ActionRetryChannel",
			decision: Decision{
				Retry:  RetryNextURL,
				Effect: EffectNone,
			},
			want: ActionRetryChannel,
		},
		{
			name: "EffectNone + RetryNone → ActionReturnClient",
			decision: Decision{
				Retry:  RetryNone,
				Effect: EffectNone,
			},
			want: ActionReturnClient,
		},
		{
			name: "EffectNone + RetryRefreshToken → ActionRetryKey",
			decision: Decision{
				Retry:  RetryRefreshToken,
				Effect: EffectNone,
			},
			want: ActionRetryKey,
		},
		{
			name: "EffectRecordFailure + RetryNextKey → ActionRetryKey",
			decision: Decision{
				Retry:  RetryNextKey,
				Effect: EffectRecordFailure,
			},
			want: ActionRetryKey,
		},
		{
			name: "EffectRecordFailure + RetryNextChannel → ActionRetryChannel",
			decision: Decision{
				Retry:  RetryNextChannel,
				Effect: EffectRecordFailure,
			},
			want: ActionRetryChannel,
		},
		{
			name: "EffectRecordFailure + RetryNone → ActionReturnClient",
			decision: Decision{
				Retry:  RetryNone,
				Effect: EffectRecordFailure,
			},
			want: ActionReturnClient,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.decision.ToLegacyAction()
			if got != tt.want {
				t.Errorf("ToLegacyAction() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestActionToDecision(t *testing.T) {
	tests := []struct {
		name   string
		action Action
		want   Decision
	}{
		{
			name:   "ActionRetryKey",
			action: ActionRetryKey,
			want: Decision{
				Retry:  RetryNextKey,
				Effect: EffectCoolKey,
			},
		},
		{
			name:   "ActionRetryModel",
			action: ActionRetryModel,
			want: Decision{
				Retry:  RetryNextChannel,
				Effect: EffectCoolModel,
			},
		},
		{
			name:   "ActionRetryChannel",
			action: ActionRetryChannel,
			want: Decision{
				Retry:  RetryNextChannel,
				Effect: EffectCoolChannel,
			},
		},
		{
			name:   "ActionReturnClient",
			action: ActionReturnClient,
			want: Decision{
				Retry:  RetryNone,
				Effect: EffectNone,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ActionToDecision(tt.action)
			if got.Retry != tt.want.Retry {
				t.Errorf("ActionToDecision().Retry = %v, want %v", got.Retry, tt.want.Retry)
			}
			if got.Effect != tt.want.Effect {
				t.Errorf("ActionToDecision().Effect = %v, want %v", got.Effect, tt.want.Effect)
			}
		})
	}
}

func TestDecisionRoundTrip(t *testing.T) {
	// Test that Action → Decision → Action preserves the action
	actions := []struct {
		action Action
		name   string
	}{
		{ActionRetryKey, "ActionRetryKey"},
		{ActionRetryModel, "ActionRetryModel"},
		{ActionRetryChannel, "ActionRetryChannel"},
		{ActionReturnClient, "ActionReturnClient"},
	}
	for _, tc := range actions {
		t.Run(tc.name, func(t *testing.T) {
			decision := ActionToDecision(tc.action)
			got := decision.ToLegacyAction()
			if got != tc.action {
				t.Errorf("round trip: Action(%v) → Decision → Action(%v)", tc.action, got)
			}
		})
	}
}

func TestRetryStrategyString(t *testing.T) {
	tests := []struct {
		strategy RetryStrategy
		want     string
	}{
		{RetryNone, "RetryNone"},
		{RetryNextKey, "RetryNextKey"},
		{RetryNextURL, "RetryNextURL"},
		{RetryNextChannel, "RetryNextChannel"},
		{RetryRefreshToken, "RetryRefreshToken"},
		{RetryStrategy(999), "RetryStrategy(unknown)"},
	}

	for _, tt := range tests {
		got := tt.strategy.String()
		if got != tt.want {
			t.Errorf("RetryStrategy(%d).String() = %q, want %q", tt.strategy, got, tt.want)
		}
	}
}

func TestEffectString(t *testing.T) {
	tests := []struct {
		effect Effect
		want   string
	}{
		{EffectNone, "EffectNone"},
		{EffectCoolKey, "EffectCoolKey"},
		{EffectCoolModel, "EffectCoolModel"},
		{EffectCoolChannel, "EffectCoolChannel"},
		{EffectClearCooldowns, "EffectClearCooldowns"},
		{EffectRecordFailure, "EffectRecordFailure"},
		{Effect(999), "Effect(unknown)"},
	}

	for _, tt := range tests {
		got := tt.effect.String()
		if got != tt.want {
			t.Errorf("Effect(%d).String() = %q, want %q", tt.effect, got, tt.want)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && stringContains(s, substr)))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
