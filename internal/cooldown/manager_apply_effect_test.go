package cooldown

import (
	"context"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
	"github.com/yzgolden86/PivotFlow/internal/testutil"
)

// setupTestChannelForEffect creates a test channel with one API key for ApplyEffect tests
func setupTestChannelForEffect(t *testing.T, store storage.Store, name string) (int64, int) {
	t.Helper()
	ctx := context.Background()

	cfg := &model.Config{
		Name:     name,
		URLs:     model.ChannelURLs{{URL: "https://api.example.com"}},
		Priority: 10,
		ModelEntries: []model.ModelEntry{
			{Model: "gpt-4", RedirectModel: ""},
		},
		Enabled: true,
	}

	created, err := store.CreateConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("Failed to create test channel: %v", err)
	}

	key := &model.APIKey{
		ChannelID:   created.ID,
		KeyIndex:    0,
		APIKey:      "sk-test-key-0",
		KeyStrategy: model.KeyStrategySequential,
	}
	if err := store.CreateAPIKeysBatch(ctx, []*model.APIKey{key}); err != nil {
		t.Fatalf("Failed to create API key: %v", err)
	}

	return created.ID, 0
}

func TestApplyEffect_EffectNone(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	channelID, keyIndex := setupTestChannelForEffect(t, store, "test-effect-none")

	decision := Decision{
		Retry:  RetryNone,
		Effect: EffectNone,
	}

	exhausted := mgr.ApplyEffect(context.Background(), decision, channelID, keyIndex, 400)
	if exhausted {
		t.Error("EffectNone should not exhaust resources")
	}

	// Verify no cooldowns were applied
	cooldowns, _ := store.GetAllKeyCooldowns(context.Background())
	if _, exists := cooldowns[channelID][keyIndex]; exists {
		t.Error("Key should not be cooled after EffectNone")
	}
}

func TestApplyEffect_EffectCoolKey_WithExplicitTime(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	channelID, keyIndex := setupTestChannelForEffect(t, store, "test-cool-key-explicit")

	cooldownUntil := time.Now().Add(5 * time.Minute)
	decision := Decision{
		Retry:               RetryNextKey,
		Effect:              EffectCoolKey,
		HasKeyCooldownUntil: true,
		KeyCooldownUntil:    cooldownUntil,
		CooldownReason:      "test_explicit_key_cooldown",
	}

	mgr.ApplyEffect(context.Background(), decision, channelID, keyIndex, 401)

	// Verify key is cooled
	cooldowns, _ := store.GetAllKeyCooldowns(context.Background())
	until, exists := cooldowns[channelID][keyIndex]
	if !exists {
		t.Error("Key should be cooled after EffectCoolKey with explicit time")
	}
	if exists && until.Before(time.Now()) {
		t.Error("Key cooldown should be in the future")
	}
}

func TestApplyEffect_EffectCoolKey_ExponentialBackoff(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	channelID, keyIndex := setupTestChannelForEffect(t, store, "test-cool-key-backoff")

	decision := Decision{
		Retry:               RetryNextKey,
		Effect:              EffectCoolKey,
		HasKeyCooldownUntil: false, // Exponential backoff
	}

	mgr.ApplyEffect(context.Background(), decision, channelID, keyIndex, 401)

	// Verify key is cooled (exponential backoff should have been applied)
	cooldowns, _ := store.GetAllKeyCooldowns(context.Background())
	until, exists := cooldowns[channelID][keyIndex]
	if !exists {
		t.Error("Key should be cooled after EffectCoolKey with exponential backoff")
	}
	if exists && until.Before(time.Now()) {
		t.Error("Key cooldown should be in the future")
	}
}

func TestApplyEffect_EffectCoolModel_WithExplicitTime(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	channelID, keyIndex := setupTestChannelForEffect(t, store, "test-cool-model-explicit")

	cooldownUntil := time.Now().Add(10 * time.Minute)
	decision := Decision{
		Retry:                 RetryNextChannel,
		Effect:                EffectCoolModel,
		Model:                 "gpt-4",
		HasModelCooldownUntil: true,
		ModelCooldownUntil:    cooldownUntil,
	}

	mgr.ApplyEffect(context.Background(), decision, channelID, keyIndex, 429)

	// Verify model is cooled
	cooldowns, _ := store.GetAllModelCooldowns(context.Background())
	until, exists := cooldowns[channelID]["gpt-4"]
	if !exists {
		t.Error("Model should be cooled after EffectCoolModel with explicit time")
	}
	if exists && until.Before(time.Now()) {
		t.Error("Model cooldown should be in the future")
	}
}

func TestApplyEffect_EffectCoolModel_ExponentialBackoff(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	channelID, keyIndex := setupTestChannelForEffect(t, store, "test-cool-model-backoff")

	decision := Decision{
		Retry:                 RetryNextChannel,
		Effect:                EffectCoolModel,
		Model:                 "gpt-4",
		HasModelCooldownUntil: false, // Exponential backoff
	}

	mgr.ApplyEffect(context.Background(), decision, channelID, keyIndex, 500)

	// Verify model is cooled
	cooldowns, _ := store.GetAllModelCooldowns(context.Background())
	until, exists := cooldowns[channelID]["gpt-4"]
	if !exists {
		t.Error("Model should be cooled after EffectCoolModel with exponential backoff")
	}
	if exists && until.Before(time.Now()) {
		t.Error("Model cooldown should be in the future")
	}
}

func TestApplyEffect_EffectCoolChannel_WithExplicitTime(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	channelID, keyIndex := setupTestChannelForEffect(t, store, "test-cool-channel-explicit")

	cooldownUntil := time.Now().Add(15 * time.Minute)
	decision := Decision{
		Retry:                   RetryNextChannel,
		Effect:                  EffectCoolChannel,
		HasChannelCooldownUntil: true,
		ChannelCooldownUntil:    cooldownUntil,
		CooldownReason:          "test_channel_cooldown",
	}

	mgr.ApplyEffect(context.Background(), decision, channelID, keyIndex, 503)

	// Verify channel is cooled
	cooldowns, _ := store.GetAllChannelCooldowns(context.Background())
	until, exists := cooldowns[channelID]
	if !exists {
		t.Error("Channel should be cooled after EffectCoolChannel with explicit time")
	}
	if exists && until.Before(time.Now()) {
		t.Error("Channel cooldown should be in the future")
	}
}

func TestApplyEffect_EffectCoolChannel_ExponentialBackoff(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	channelID, keyIndex := setupTestChannelForEffect(t, store, "test-cool-channel-backoff")

	decision := Decision{
		Retry:                   RetryNextChannel,
		Effect:                  EffectCoolChannel,
		HasChannelCooldownUntil: false, // Exponential backoff
	}

	mgr.ApplyEffect(context.Background(), decision, channelID, keyIndex, 502)

	// Verify channel is cooled
	cooldowns, _ := store.GetAllChannelCooldowns(context.Background())
	until, exists := cooldowns[channelID]
	if !exists {
		t.Error("Channel should be cooled after EffectCoolChannel with exponential backoff")
	}
	if exists && until.Before(time.Now()) {
		t.Error("Channel cooldown should be in the future")
	}
}

func TestApplyEffect_EffectClearCooldowns(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	channelID, _ := setupTestChannelForEffect(t, store, "test-clear-cooldowns")
	ctx := context.Background()

	// First apply some cooldowns
	cooldownUntil := time.Now().Add(10 * time.Minute)
	_ = store.SetModelCooldown(ctx, channelID, "gpt-4", cooldownUntil)
	_ = store.SetChannelCooldown(ctx, channelID, cooldownUntil)

	// Verify cooldowns exist
	modelCooldowns, _ := store.GetAllModelCooldowns(ctx)
	channelCooldowns, _ := store.GetAllChannelCooldowns(ctx)
	_, modelExists := modelCooldowns[channelID]["gpt-4"]
	_, channelExists := channelCooldowns[channelID]
	if !modelExists || !channelExists {
		t.Fatal("Setup failed: cooldowns should exist before clearing")
	}

	// Apply EffectClearCooldowns
	decision := Decision{
		Retry:  RetryNone,
		Effect: EffectClearCooldowns,
	}
	mgr.ApplyEffect(ctx, decision, channelID, 0, 200)

	// Verify cooldowns are cleared
	modelCooldowns, _ = store.GetAllModelCooldowns(ctx)
	channelCooldowns, _ = store.GetAllChannelCooldowns(ctx)
	_, modelExists = modelCooldowns[channelID]["gpt-4"]
	_, channelExists = channelCooldowns[channelID]
	if modelExists {
		t.Error("Model cooldown should be cleared after EffectClearCooldowns")
	}
	if channelExists {
		t.Error("Channel cooldown should be cleared after EffectClearCooldowns")
	}
}

func TestApplyEffect_EffectRecordFailure(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	channelID, keyIndex := setupTestChannelForEffect(t, store, "test-record-failure")

	decision := Decision{
		Retry:  RetryNextKey,
		Effect: EffectRecordFailure,
	}

	exhausted := mgr.ApplyEffect(context.Background(), decision, channelID, keyIndex, 500)
	if exhausted {
		t.Error("EffectRecordFailure should not exhaust resources")
	}

	// Verify no cooldowns were applied
	cooldowns, _ := store.GetAllKeyCooldowns(context.Background())
	if _, exists := cooldowns[channelID][keyIndex]; exists {
		t.Error("Key should not be cooled after EffectRecordFailure")
	}
}

func TestApplyEffect_EffectClearKeyCooldown(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	channelID, keyIndex := setupTestChannelForEffect(t, store, "test-clear-key")
	ctx := context.Background()

	// First apply key cooldown
	cooldownUntil := time.Now().Add(10 * time.Minute)
	_ = store.SetKeyCooldown(ctx, channelID, keyIndex, cooldownUntil)

	// Verify cooldown exists
	keyCooldowns, _ := store.GetAllKeyCooldowns(ctx)
	_, keyExists := keyCooldowns[channelID][keyIndex]
	if !keyExists {
		t.Fatal("Setup failed: key cooldown should exist before clearing")
	}

	// Apply EffectClearKeyCooldown
	decision := Decision{
		Retry:  RetryNone,
		Effect: EffectClearKeyCooldown,
	}
	mgr.ApplyEffect(ctx, decision, channelID, keyIndex, 200)

	// Verify key cooldown is cleared
	keyCooldowns, _ = store.GetAllKeyCooldowns(ctx)
	_, keyExists = keyCooldowns[channelID][keyIndex]
	if keyExists {
		t.Error("Key cooldown should be cleared after EffectClearKeyCooldown")
	}
}

func TestApplyEffect_EffectClearModelCooldown(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	channelID, _ := setupTestChannelForEffect(t, store, "test-clear-model")
	ctx := context.Background()

	// First apply model cooldown
	cooldownUntil := time.Now().Add(10 * time.Minute)
	_ = store.SetModelCooldown(ctx, channelID, "gpt-4", cooldownUntil)

	// Verify cooldown exists
	modelCooldowns, _ := store.GetAllModelCooldowns(ctx)
	_, modelExists := modelCooldowns[channelID]["gpt-4"]
	if !modelExists {
		t.Fatal("Setup failed: model cooldown should exist before clearing")
	}

	// Apply EffectClearModelCooldown
	decision := Decision{
		Retry:  RetryNone,
		Effect: EffectClearModelCooldown,
		Model:  "gpt-4",
	}
	mgr.ApplyEffect(ctx, decision, channelID, 0, 200)

	// Verify model cooldown is cleared
	modelCooldowns, _ = store.GetAllModelCooldowns(ctx)
	_, modelExists = modelCooldowns[channelID]["gpt-4"]
	if modelExists {
		t.Error("Model cooldown should be cleared after EffectClearModelCooldown")
	}
}

func TestApplyEffect_EffectClearChannelCooldown(t *testing.T) {
	store, cleanup := testutil.SetupTestStore(t)
	defer cleanup()
	mgr := NewManager(store, nil)

	channelID, _ := setupTestChannelForEffect(t, store, "test-clear-channel")
	ctx := context.Background()

	// First apply channel cooldown
	cooldownUntil := time.Now().Add(10 * time.Minute)
	_ = store.SetChannelCooldown(ctx, channelID, cooldownUntil)

	// Verify cooldown exists
	channelCooldowns, _ := store.GetAllChannelCooldowns(ctx)
	_, channelExists := channelCooldowns[channelID]
	if !channelExists {
		t.Fatal("Setup failed: channel cooldown should exist before clearing")
	}

	// Apply EffectClearChannelCooldown
	decision := Decision{
		Retry:  RetryNone,
		Effect: EffectClearChannelCooldown,
	}
	mgr.ApplyEffect(ctx, decision, channelID, 0, 200)

	// Verify channel cooldown is cleared
	channelCooldowns, _ = store.GetAllChannelCooldowns(ctx)
	_, channelExists = channelCooldowns[channelID]
	if channelExists {
		t.Error("Channel cooldown should be cleared after EffectClearChannelCooldown")
	}
}
