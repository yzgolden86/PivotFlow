package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

func TestConfigurationTransactionHybridRollsBackWithoutCacheEffects(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	abort := errors.New("abort hybrid configuration transaction")
	var channelID int64
	err := WithConfigurationTransaction(ctx, hybrid, func(ctx context.Context, store Store) error {
		created, err := store.CreateConfig(ctx, &model.Config{
			Name:         "hybrid-rollback",
			URLs:         model.ChannelURLs{{URL: "https://hybrid-rollback.example.com"}},
			Enabled:      true,
			ModelEntries: []model.ModelEntry{{Model: "gpt-rollback"}},
		})
		if err != nil {
			return err
		}
		channelID = created.ID

		if _, err := cache.GetConfig(ctx, channelID); err == nil {
			t.Fatal("replica changed before the primary transaction committed")
		}

		if err := WithConfigurationTransaction(ctx, store, func(ctx context.Context, nested Store) error {
			_, err := nested.CreateConfig(ctx, &model.Config{
				Name:    "hybrid-nested-rollback",
				URLs:    model.ChannelURLs{{URL: "https://hybrid-nested.example.com"}},
				Enabled: true,
			})
			return err
		}); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("error=%v, want %v", err, abort)
	}
	if channelID == 0 {
		t.Fatal("test did not allocate a channel id")
	}
	if _, err := primary.GetConfig(ctx, channelID); err == nil {
		t.Fatal("primary kept a rolled-back channel")
	}
	if _, err := cache.GetConfig(ctx, channelID); err == nil {
		t.Fatal("replica kept a rolled-back channel")
	}
	configs, err := primary.ListConfigs(ctx)
	if err != nil {
		t.Fatalf("list primary configs: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("primary has unexpected configs after rollback: %+v", configs)
	}
}

func TestConfigurationTransactionHybridCommitsAndMirrorsConfiguration(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	var channelID, tokenID int64
	err := WithConfigurationTransaction(ctx, hybrid, func(ctx context.Context, store Store) error {
		created, err := store.CreateConfig(ctx, &model.Config{
			Name:         "hybrid-commit",
			URLs:         model.ChannelURLs{{URL: "https://hybrid-commit.example.com"}},
			Enabled:      true,
			ModelEntries: []model.ModelEntry{{Model: "gpt-hybrid-commit"}},
		})
		if err != nil {
			return err
		}
		channelID = created.ID
		if err := store.CreateAPIKeysBatch(ctx, []*model.APIKey{{
			ChannelID:   channelID,
			KeyIndex:    0,
			APIKey:      "sk-hybrid-commit",
			KeyStrategy: model.KeyStrategySequential,
		}}); err != nil {
			return err
		}
		token := &model.AuthToken{
			Token:       "config-transaction-token",
			Description: "mirrored token",
			IsActive:    true,
		}
		if err := store.CreateAuthToken(ctx, token); err != nil {
			return err
		}
		tokenID = token.ID
		if err := store.BatchUpdateSettings(ctx, map[string]string{"log_retention_days": "31"}); err != nil {
			return err
		}
		if _, err := cache.GetConfig(ctx, channelID); err == nil {
			t.Fatal("replica changed before commit")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("configuration transaction: %v", err)
	}

	config, err := cache.GetConfig(ctx, channelID)
	if err != nil {
		t.Fatalf("replica config missing after commit: %v", err)
	}
	if config.Name != "hybrid-commit" {
		t.Fatalf("replica config name=%q", config.Name)
	}
	keys, err := cache.GetAPIKeys(ctx, channelID)
	if err != nil {
		t.Fatalf("replica api keys: %v", err)
	}
	if len(keys) != 1 || keys[0].APIKey != "sk-hybrid-commit" {
		t.Fatalf("replica api keys=%+v", keys)
	}
	token, err := cache.GetAuthToken(ctx, tokenID)
	if err != nil {
		t.Fatalf("replica token missing after commit: %v", err)
	}
	if token.Description != "mirrored token" {
		t.Fatalf("replica token description=%q", token.Description)
	}
	setting, err := cache.GetSetting(ctx, "log_retention_days")
	if err != nil {
		t.Fatalf("replica setting missing after commit: %v", err)
	}
	if setting.Value != "31" {
		t.Fatalf("replica setting value=%q, want 31", setting.Value)
	}
}

func TestConfigurationTransactionHybridReportsCacheDegradedAfterCommit(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	if err := cache.Close(); err != nil {
		t.Fatalf("close replica: %v", err)
	}

	var channelID int64
	err := WithConfigurationTransaction(ctx, hybrid, func(ctx context.Context, store Store) error {
		created, err := store.CreateConfig(ctx, &model.Config{
			Name:    "committed-with-degraded-cache",
			URLs:    model.ChannelURLs{{URL: "https://degraded.example.com"}},
			Enabled: true,
		})
		channelID = created.ID
		return err
	})
	if err == nil {
		t.Fatal("expected cache degradation error")
	}
	if !IsConfigurationCacheDegraded(err) {
		t.Fatalf("error=%v, want committed cache degraded", err)
	}
	if got := hybrid.sqliteSyncFailCount.Load(); got == 0 {
		t.Fatal("configuration mirror failure did not increment the SQLite sync failure counter")
	}
	if _, err := primary.GetConfig(ctx, channelID); err != nil {
		t.Fatalf("primary commit was rolled back: %v", err)
	}
}

func TestConfigurationSourceReturnsAuthoritativeStore(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })

	if got := ConfigurationSource(hybrid); got != Store(primary) {
		t.Fatalf("hybrid configuration source=%T, want primary %T", got, primary)
	}
	if got := ConfigurationSource(primary); got != Store(primary) {
		t.Fatalf("sql configuration source=%T, want same store", got)
	}
}

func TestConfigurationCommitUnknownHelper(t *testing.T) {
	if !IsConfigurationCommitUnknown(sqlstore.ErrTransactionCommitUnknown) {
		t.Fatal("commit-unknown sentinel is not recognized")
	}
	if IsConfigurationCommitUnknown(errors.New("commit transaction: closed")) {
		t.Fatal("plain commit error was classified as commit-unknown")
	}
}

func TestConfigurationMirrorCreatesMissingChannelWithFullConfig(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	config, err := primary.CreateConfig(ctx, &model.Config{
		Name:                    "full-config-mirror",
		URLs:                    model.ChannelURLs{{URL: "https://full-config.example.com"}},
		Priority:                7,
		RPMLimit:                120,
		MaxConcurrency:          5,
		Enabled:                 true,
		Websockets:              true,
		ProtocolTransformMode:   model.ProtocolTransformModeUpstream,
		ScheduledCheckEnabled:   true,
		ScheduledCheckModel:     "gpt-check",
		DailyCostLimit:          12.5,
		CostMultiplier:          1.75,
		ProxyURL:                "http://proxy.example.com:8080",
		AvailableTimeStart:      "08:00",
		AvailableTimeEnd:        "22:30",
		RetryOtherKeysOnFailure: true,
		CustomRequestRules: &model.CustomRequestRules{Headers: []model.CustomHeaderRule{{
			Action: model.RuleActionOverride,
			Name:   "X-Mirror",
			Value:  "yes",
		}}},
		ModelEntries: []model.ModelEntry{{Model: "gpt-mirror"}},
	})
	if err != nil {
		t.Fatalf("create primary config: %v", err)
	}
	if err := primary.CreateAPIKeysBatch(ctx, []*model.APIKey{{
		ChannelID:   config.ID,
		KeyIndex:    0,
		APIKey:      "primary-full-config-key",
		KeyStrategy: model.KeyStrategySequential,
	}}); err != nil {
		t.Fatalf("create primary key: %v", err)
	}

	changes := newConfigurationChangeSet()
	changes.channel(config.ID)
	if err := hybrid.mirrorConfigurationChanges(ctx, changes); err != nil {
		t.Fatalf("mirror configuration: %v", err)
	}

	got, err := cache.GetConfig(ctx, config.ID)
	if err != nil {
		t.Fatalf("read replica config: %v", err)
	}
	if !got.Websockets || got.DailyCostLimit != 12.5 || got.CostMultiplier != 1.75 {
		t.Fatalf("replica lost scalar config: %+v", got)
	}
	if got.ProxyURL != "http://proxy.example.com:8080" ||
		got.AvailableTimeStart != "08:00" ||
		got.AvailableTimeEnd != "22:30" ||
		!got.RetryOtherKeysOnFailure ||
		got.ProtocolTransformMode != model.ProtocolTransformModeUpstream {
		t.Fatalf("replica lost runtime config fields: %+v", got)
	}
	if got.CustomRequestRules == nil || len(got.CustomRequestRules.Headers) != 1 ||
		got.CustomRequestRules.Headers[0].Name != "X-Mirror" {
		t.Fatalf("replica lost custom request rules: %+v", got.CustomRequestRules)
	}
	if len(got.ModelEntries) != 1 || got.ModelEntries[0].Model != "gpt-mirror" {
		t.Fatalf("replica lost model entries: %+v", got.ModelEntries)
	}
	if got.KeyCount != 1 {
		t.Fatalf("replica key count=%d, want 1", got.KeyCount)
	}
	keys, err := cache.GetAPIKeys(ctx, config.ID)
	if err != nil {
		t.Fatalf("read replica keys: %v", err)
	}
	if len(keys) != 1 || keys[0].APIKey != "primary-full-config-key" {
		t.Fatalf("replica keys=%+v", keys)
	}
}

func TestConfigurationMirrorChannelSnapshotRollsBackAsOneUnit(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	primaryConfig, err := primary.CreateConfig(ctx, &model.Config{
		Name:         "primary-new",
		URLs:         model.ChannelURLs{{URL: "https://primary-new.example.com"}},
		Enabled:      true,
		ModelEntries: []model.ModelEntry{{Model: "gpt-new"}},
	})
	if err != nil {
		t.Fatalf("create primary config: %v", err)
	}
	if err := primary.CreateAPIKeysBatch(ctx, []*model.APIKey{{
		ChannelID:   primaryConfig.ID,
		KeyIndex:    0,
		APIKey:      "primary-new-key",
		KeyStrategy: model.KeyStrategySequential,
	}}); err != nil {
		t.Fatalf("create primary key: %v", err)
	}

	cacheConfig := &model.Config{
		ID:           primaryConfig.ID,
		Name:         "cache-old",
		URLs:         model.ChannelURLs{{URL: "https://cache-old.example.com"}},
		Enabled:      true,
		ModelEntries: []model.ModelEntry{{Model: "gpt-old"}},
	}
	if _, err := cache.CreateConfig(ctx, cacheConfig); err != nil {
		t.Fatalf("create cache config: %v", err)
	}
	if err := cache.CreateAPIKeysBatch(ctx, []*model.APIKey{{
		ChannelID:   cacheConfig.ID,
		KeyIndex:    0,
		APIKey:      "cache-old-key",
		KeyStrategy: model.KeyStrategySequential,
	}}); err != nil {
		t.Fatalf("create cache key: %v", err)
	}
	if _, err := cache.ExecContext(ctx, `
		CREATE TRIGGER block_mirror_api_key_insert
		BEFORE INSERT ON api_keys
		BEGIN
			SELECT RAISE(ABORT, 'blocked mirror insert');
		END
	`); err != nil {
		t.Fatalf("create cache trigger: %v", err)
	}

	changes := newConfigurationChangeSet()
	changes.channel(primaryConfig.ID)
	if err := hybrid.mirrorConfigurationChanges(ctx, changes); err == nil {
		t.Fatal("mirror succeeded despite the cache insert trigger")
	}

	got, err := cache.GetConfig(ctx, cacheConfig.ID)
	if err != nil {
		t.Fatalf("read cache config after rollback: %v", err)
	}
	if got.Name != "cache-old" {
		t.Fatalf("cache config committed partially: %+v", got)
	}
	keys, err := cache.GetAPIKeys(ctx, cacheConfig.ID)
	if err != nil {
		t.Fatalf("read cache keys after rollback: %v", err)
	}
	if len(keys) != 1 || keys[0].APIKey != "cache-old-key" {
		t.Fatalf("cache keys were exposed empty or replaced after rollback: %+v", keys)
	}
}

func TestConfigurationMirrorUpdatesOAuthCredentialInSameSnapshot(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	primaryConfig, err := primary.CreateConfig(ctx, &model.Config{
		Name:            "oauth-same-type",
		AuthType:        model.AuthTypeCodexOAuth,
		OAuthCredential: "primary-new-credential",
		URLs:            model.ChannelURLs{{URL: "https://oauth.example.com"}},
		Enabled:         true,
		ModelEntries:    []model.ModelEntry{{Model: "gpt-oauth"}},
	})
	if err != nil {
		t.Fatalf("create primary oauth config: %v", err)
	}
	if _, err := cache.CreateConfig(ctx, &model.Config{
		ID:              primaryConfig.ID,
		Name:            "oauth-same-type",
		AuthType:        model.AuthTypeCodexOAuth,
		OAuthCredential: "cache-old-credential",
		URLs:            model.ChannelURLs{{URL: "https://oauth.example.com"}},
		Enabled:         true,
		ModelEntries:    []model.ModelEntry{{Model: "gpt-oauth"}},
	}); err != nil {
		t.Fatalf("create cache oauth config: %v", err)
	}

	changes := newConfigurationChangeSet()
	changes.channel(primaryConfig.ID)
	if err := hybrid.mirrorConfigurationChanges(ctx, changes); err != nil {
		t.Fatalf("mirror oauth credential: %v", err)
	}
	got, err := cache.GetConfig(ctx, primaryConfig.ID)
	if err != nil {
		t.Fatalf("read mirrored oauth config: %v", err)
	}
	if got.OAuthCredential != "primary-new-credential" {
		t.Fatalf("replica kept stale oauth credential: %q", got.OAuthCredential)
	}
}

func TestConfigurationMirrorRebuildsOnPersistedAuthTypeMismatch(t *testing.T) {
	tests := []struct {
		name      string
		primary   *model.Config
		cache     *model.Config
		wantAuth  string
		wantOAuth string
	}{
		{
			name: "cache api key to primary oauth",
			primary: &model.Config{
				Name:            "primary-oauth-type",
				AuthType:        model.AuthTypeCodexOAuth,
				OAuthCredential: "oauth-from-primary",
				URLs:            model.ChannelURLs{{URL: "https://primary-oauth.example.com"}},
				Enabled:         true,
			},
			cache: &model.Config{
				Name:     "cache-api-type",
				AuthType: model.AuthTypeAPIKey,
				URLs:     model.ChannelURLs{{URL: "https://cache-api.example.com"}},
				Enabled:  true,
			},
			wantAuth:  model.AuthTypeCodexOAuth,
			wantOAuth: "oauth-from-primary",
		},
		{
			name: "cache oauth to primary api key",
			primary: &model.Config{
				Name:         "primary-api-type",
				AuthType:     model.AuthTypeAPIKey,
				URLs:         model.ChannelURLs{{URL: "https://primary-api.example.com"}},
				Enabled:      true,
				ModelEntries: []model.ModelEntry{{Model: "gpt-api"}},
			},
			cache: &model.Config{
				Name:            "cache-oauth-type",
				AuthType:        model.AuthTypeCodexOAuth,
				OAuthCredential: "stale-oauth-credential",
				URLs:            model.ChannelURLs{{URL: "https://cache-oauth.example.com"}},
				Enabled:         true,
			},
			wantAuth:  model.AuthTypeAPIKey,
			wantOAuth: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary := createTestSQLiteStore(t)
			cache := createTestSQLiteStore(t)
			hybrid := NewHybridStore(cache, primary)
			t.Cleanup(func() { _ = hybrid.Close() })
			ctx := context.Background()

			primaryConfig, err := primary.CreateConfig(ctx, tt.primary)
			if err != nil {
				t.Fatalf("create primary config: %v", err)
			}
			tt.cache.ID = primaryConfig.ID
			if _, err := cache.CreateConfig(ctx, tt.cache); err != nil {
				t.Fatalf("create cache config: %v", err)
			}
			if tt.primary.AuthType == model.AuthTypeAPIKey {
				if err := primary.CreateAPIKeysBatch(ctx, []*model.APIKey{{
					ChannelID:   primaryConfig.ID,
					KeyIndex:    0,
					APIKey:      "primary-api-type-key",
					KeyStrategy: model.KeyStrategySequential,
				}}); err != nil {
					t.Fatalf("create primary key: %v", err)
				}
			}

			changes := newConfigurationChangeSet()
			changes.channel(primaryConfig.ID)
			if err := hybrid.mirrorConfigurationChanges(ctx, changes); err != nil {
				t.Fatalf("mirror auth type: %v", err)
			}
			got, err := cache.GetConfig(ctx, primaryConfig.ID)
			if err != nil {
				t.Fatalf("read mirrored config: %v", err)
			}
			if got.GetAuthType() != tt.wantAuth {
				t.Fatalf("auth type=%q, want %q", got.GetAuthType(), tt.wantAuth)
			}
			if got.OAuthCredential != tt.wantOAuth {
				t.Fatalf("oauth credential=%q, want %q", got.OAuthCredential, tt.wantOAuth)
			}
			if tt.wantAuth == model.AuthTypeAPIKey {
				keys, err := cache.GetAPIKeys(ctx, primaryConfig.ID)
				if err != nil {
					t.Fatalf("read mirrored api keys: %v", err)
				}
				if len(keys) != 1 || keys[0].APIKey != "primary-api-type-key" {
					t.Fatalf("mirrored api keys=%+v", keys)
				}
			}
		})
	}
}

func TestConfigurationTransactionNestedCallKeepsMirrorRecording(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	var outerID, nestedID int64
	err := WithConfigurationTransaction(ctx, hybrid, func(ctx context.Context, store Store) error {
		outer, err := store.CreateConfig(ctx, &model.Config{
			Name:    "outer-config",
			URLs:    model.ChannelURLs{{URL: "https://outer.example.com"}},
			Enabled: true,
		})
		if err != nil {
			return err
		}
		outerID = outer.ID
		return WithConfigurationTransaction(ctx, store, func(ctx context.Context, nested Store) error {
			created, err := nested.CreateConfig(ctx, &model.Config{
				Name:    "nested-config",
				URLs:    model.ChannelURLs{{URL: "https://nested.example.com"}},
				Enabled: true,
			})
			if err == nil {
				nestedID = created.ID
			}
			return err
		})
	})
	if err != nil {
		t.Fatalf("nested configuration transaction: %v", err)
	}
	for name, id := range map[string]int64{"outer": outerID, "nested": nestedID} {
		if _, err := cache.GetConfig(ctx, id); err != nil {
			t.Fatalf("%s channel was not mirrored: %v", name, err)
		}
	}
}

func TestConfigurationTransactionMirrorsSiteAccountChannelDisablement(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	site, err := primary.CreateSite(ctx, &model.Site{
		Name:     "disable-account-site",
		Platform: model.SitePlatformNewAPIFamily,
		BaseURL:  "https://disable-account.example.com",
		Enabled:  true,
		Timezone: "Asia/Shanghai",
		TagsJSON: "[]",
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	account, err := primary.CreateSiteAccount(ctx, &model.SiteAccount{
		SiteID:               site.ID,
		Label:                "bounded-account",
		CredentialType:       model.CredentialTypeAPIKey,
		CredentialCiphertext: "import-test-key-zero",
		CredentialKeyVersion: "v1",
		Enabled:              true,
		Status:               "healthy",
		BalanceCurrency:      "USD",
		LastRefreshStatus:    "unknown",
		LastCheckinStatus:    "unknown",
	})
	if err != nil {
		t.Fatalf("create site account: %v", err)
	}

	var channelIDs []int64
	inputs := []model.SiteProjectionInput{
		{
			SiteAccountID: account.ID,
			ProjectionKey: "bounded:a",
			Name:          "bounded-a",
			BaseURL:       site.BaseURL,
			Protocols:     []string{"openai"},
			Models:        []string{"gpt-bounded-a"},
			APIKey:        "bounded-key-a",
			Enabled:       true,
		},
		{
			SiteAccountID: account.ID,
			ProjectionKey: "bounded:b",
			Name:          "bounded-b",
			BaseURL:       site.BaseURL,
			Protocols:     []string{"openai"},
			Models:        []string{"gpt-bounded-b"},
			APIKey:        "bounded-key-b",
			Enabled:       true,
		},
	}
	err = WithConfigurationTransaction(ctx, hybrid, func(ctx context.Context, store Store) error {
		for _, input := range inputs {
			result, err := store.UpsertSiteProjection(ctx, input)
			if err != nil {
				return err
			}
			channelIDs = append(channelIDs, result.Channel.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("create projected channels: %v", err)
	}
	for _, channelID := range channelIDs {
		config, err := cache.GetConfig(ctx, channelID)
		if err != nil {
			t.Fatalf("read initial cached channel %d: %v", channelID, err)
		}
		if !config.Enabled {
			t.Fatalf("initial cached channel %d is disabled", channelID)
		}
	}

	disabledAccount := *account
	disabledAccount.Enabled = false
	disabledAccount.Status = model.SiteAccountStatusDisabled
	err = WithConfigurationTransaction(ctx, hybrid, func(ctx context.Context, store Store) error {
		_, err := store.UpdateSiteAccount(ctx, account.ID, &disabledAccount)
		return err
	})
	if err != nil {
		t.Fatalf("disable site account: %v", err)
	}

	for _, channelID := range channelIDs {
		config, err := cache.GetConfig(ctx, channelID)
		if err != nil {
			t.Fatalf("read cached channel %d after account disable: %v", channelID, err)
		}
		if config.Enabled {
			t.Fatalf("cached channel %d stayed enabled after linked account was disabled", channelID)
		}
	}
}

func TestConfigurationTransactionExpiredAccountCredentialKeepsChannelDisabled(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	site, err := primary.CreateSite(ctx, &model.Site{
		Name:     "expired-account-site",
		Platform: model.SitePlatformNewAPIFamily,
		BaseURL:  "https://expired-account.example.com",
		Enabled:  true,
		Timezone: "Asia/Shanghai",
		TagsJSON: "[]",
	})
	if err != nil {
		t.Fatalf("create site: %v", err)
	}
	account, err := primary.CreateSiteAccount(ctx, &model.SiteAccount{
		SiteID:               site.ID,
		Label:                "expired-account",
		CredentialType:       model.CredentialTypeAPIKey,
		CredentialCiphertext: "import-test-key-zero",
		CredentialKeyVersion: "v1",
		Enabled:              true,
		Status:               model.SiteAccountStatusHealthy,
		BalanceCurrency:      "USD",
		LastRefreshStatus:    "unknown",
		LastCheckinStatus:    "unknown",
	})
	if err != nil {
		t.Fatalf("create site account: %v", err)
	}

	var channelID int64
	err = WithConfigurationTransaction(ctx, hybrid, func(ctx context.Context, store Store) error {
		result, err := store.UpsertSiteProjection(ctx, model.SiteProjectionInput{
			SiteAccountID: account.ID,
			ProjectionKey: "expired:key",
			Name:          "expired-account-channel",
			BaseURL:       site.BaseURL,
			Protocols:     []string{"openai"},
			Models:        []string{"gpt-expired"},
			APIKey:        "expired-account-key",
			Enabled:       true,
		})
		if err != nil {
			return err
		}
		channelID = result.Channel.ID
		return nil
	})
	if err != nil {
		t.Fatalf("create projected channel: %v", err)
	}

	expired := *account
	expired.Enabled = true
	expired.Status = model.SiteAccountStatusExpired
	err = WithConfigurationTransaction(ctx, hybrid, func(ctx context.Context, store Store) error {
		if _, err := store.UpdateSiteAccount(ctx, account.ID, &expired); err != nil {
			return err
		}
		return store.UpdateSiteAccountCredential(
			ctx,
			account.ID,
			expired.CredentialType,
			"fc1.test",
			"v2",
		)
	})
	if err != nil {
		t.Fatalf("expire site account and replace credential: %v", err)
	}

	accountAfter, err := hybrid.GetSiteAccount(ctx, account.ID)
	if err != nil {
		t.Fatalf("read site account after credential replacement: %v", err)
	}
	if accountAfter.Status != model.SiteAccountStatusExpired {
		t.Fatalf("account status=%q, want expired", accountAfter.Status)
	}
	if accountAfter.CredentialKeyVersion != "v2" {
		t.Fatalf("credential version=%q, want v2", accountAfter.CredentialKeyVersion)
	}
	channelAfter, err := cache.GetConfig(ctx, channelID)
	if err != nil {
		t.Fatalf("read cached projected channel: %v", err)
	}
	if channelAfter.Enabled {
		t.Fatal("credential replacement re-enabled a channel owned by the expired account")
	}
}

func TestConfigurationTransactionAcquiresSharedConfigurationMutex(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	hybrid.configurationMu.Lock()
	locked := true
	defer func() {
		if locked {
			hybrid.configurationMu.Unlock()
		}
	}()

	started := make(chan struct{})
	transactionDone := make(chan error, 1)
	go func() {
		close(started)
		transactionDone <- WithConfigurationTransaction(ctx, hybrid, func(ctx context.Context, store Store) error {
			_, err := store.CreateConfig(ctx, &model.Config{
				Name:    "transaction-lock-channel",
				URLs:    model.ChannelURLs{{URL: "https://transaction-lock.example.com"}},
				Enabled: true,
			})
			return err
		})
	}()

	<-started
	select {
	case err := <-transactionDone:
		t.Fatalf("configuration transaction ignored the shared configuration mutex: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	hybrid.configurationMu.Unlock()
	locked = false
	if err := <-transactionDone; err != nil {
		t.Fatalf("configuration transaction: %v", err)
	}

	configs, err := cache.ListConfigs(ctx)
	if err != nil {
		t.Fatalf("list cached channels: %v", err)
	}
	if len(configs) != 1 || configs[0].Name != "transaction-lock-channel" {
		t.Fatalf("configuration transaction did not publish under the shared mutex: %+v", configs)
	}
}

func TestOrdinaryConfigurationWritesWaitForSharedConfigurationMutex(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	channel, err := hybrid.CreateConfig(ctx, &model.Config{
		Name:    "initial-channel",
		URLs:    model.ChannelURLs{{URL: "https://channel-order.example.com"}},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	token := &model.AuthToken{
		Token:       "configuration-order-token",
		Description: "initial-token",
		IsActive:    true,
	}
	if err := hybrid.CreateAuthToken(ctx, token); err != nil {
		t.Fatalf("create auth token: %v", err)
	}

	hybrid.configurationMu.Lock()
	locked := true
	defer func() {
		if locked {
			hybrid.configurationMu.Unlock()
		}
	}()

	channelStarted := make(chan struct{})
	channelDone := make(chan error, 1)
	go func() {
		close(channelStarted)
		current, err := hybrid.GetConfig(ctx, channel.ID)
		if err == nil {
			current.Name = "ordinary-state"
			_, err = hybrid.UpdateConfig(ctx, channel.ID, current)
		}
		channelDone <- err
	}()

	<-channelStarted
	select {
	case err := <-channelDone:
		t.Fatalf("ordinary channel update ignored the shared configuration mutex: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	tokenStarted := make(chan struct{})
	revoked := make(chan error, 1)
	go func() {
		close(tokenStarted)
		revoked <- hybrid.DeleteAuthToken(ctx, token.ID)
	}()

	<-tokenStarted
	select {
	case err := <-revoked:
		t.Fatalf("ordinary token revocation ignored the shared configuration mutex: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	hybrid.configurationMu.Unlock()
	locked = false
	if err := <-channelDone; err != nil {
		t.Fatalf("ordinary channel update: %v", err)
	}
	if err := <-revoked; err != nil {
		t.Fatalf("ordinary token revocation: %v", err)
	}

	primaryConfig, err := primary.GetConfig(ctx, channel.ID)
	if err != nil {
		t.Fatalf("read primary channel: %v", err)
	}
	cachedConfig, err := cache.GetConfig(ctx, channel.ID)
	if err != nil {
		t.Fatalf("read cached channel: %v", err)
	}
	if primaryConfig.Name != "ordinary-state" || cachedConfig.Name != "ordinary-state" {
		t.Fatalf("final primary/cache names=%q/%q, want ordinary-state", primaryConfig.Name, cachedConfig.Name)
	}
	if _, err := cache.GetAuthToken(ctx, token.ID); err == nil {
		t.Fatal("revoked token remained in the cache")
	}
	if _, err := primary.GetAuthToken(ctx, token.ID); err == nil {
		t.Fatal("revoked token remained in the primary store")
	}
}

func TestAuthTokenConfigurationWritesAcquireConfigurationMutexBeforeAccounting(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	token := &model.AuthToken{
		Token:       "lock-order-token",
		Description: "lock-order",
		IsActive:    true,
	}
	if err := hybrid.CreateAuthToken(ctx, token); err != nil {
		t.Fatalf("create auth token: %v", err)
	}

	hybrid.tokenAccountingMu.Lock()
	accountingLocked := true
	defer func() {
		if accountingLocked {
			hybrid.tokenAccountingMu.Unlock()
		}
	}()

	started := make(chan struct{})
	deleted := make(chan error, 1)
	go func() {
		close(started)
		deleted <- hybrid.DeleteAuthToken(ctx, token.ID)
	}()

	<-started
	deadline := time.Now().Add(2 * time.Second)
	for hybrid.configurationMu.TryLock() {
		hybrid.configurationMu.Unlock()
		if time.Now().After(deadline) {
			t.Fatal("token configuration write did not acquire configurationMu before tokenAccountingMu")
		}
		time.Sleep(time.Millisecond)
	}

	hybrid.tokenAccountingMu.Unlock()
	accountingLocked = false
	if err := <-deleted; err != nil {
		t.Fatalf("delete auth token: %v", err)
	}
	if _, err := cache.GetAuthToken(ctx, token.ID); err == nil {
		t.Fatal("deleted token remained in the cache")
	}
}

func TestConfigurationMirrorReportsPrimaryNotificationConfigErrors(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	if err := primary.Close(); err != nil {
		t.Fatalf("close primary: %v", err)
	}
	changes := newConfigurationChangeSet()
	changes.webhook = true
	changes.backup = true
	err := hybrid.mirrorConfigurationChanges(ctx, changes)
	if err == nil {
		t.Fatal("mirror hid primary webhook/backup read errors")
	}
	if !strings.Contains(err.Error(), "webhook config") || !strings.Contains(err.Error(), "backup config") {
		t.Fatalf("mirror error did not identify both primary reads: %v", err)
	}
}

func TestConfigurationRuntimeSettersWaitForSharedConfigurationMutex(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, *HybridStore, int64) error
	}{
		{
			name: "bump channel cooldown",
			run: func(ctx context.Context, hybrid *HybridStore, channelID int64) error {
				_, err := hybrid.BumpChannelCooldown(ctx, channelID, time.Now(), 500)
				return err
			},
		},
		{
			name: "reset channel cooldown",
			run: func(ctx context.Context, hybrid *HybridStore, channelID int64) error {
				return hybrid.ResetChannelCooldown(ctx, channelID)
			},
		},
		{
			name: "set channel cooldown",
			run: func(ctx context.Context, hybrid *HybridStore, channelID int64) error {
				return hybrid.SetChannelCooldown(ctx, channelID, time.Now().Add(time.Minute))
			},
		},
		{
			name: "bump key cooldown",
			run: func(ctx context.Context, hybrid *HybridStore, channelID int64) error {
				_, err := hybrid.BumpKeyCooldown(ctx, channelID, 0, time.Now(), 429)
				return err
			},
		},
		{
			name: "reset key cooldown",
			run: func(ctx context.Context, hybrid *HybridStore, channelID int64) error {
				return hybrid.ResetKeyCooldown(ctx, channelID, 0)
			},
		},
		{
			name: "set key cooldown",
			run: func(ctx context.Context, hybrid *HybridStore, channelID int64) error {
				return hybrid.SetKeyCooldown(ctx, channelID, 0, time.Now().Add(time.Minute))
			},
		},
		{
			name: "reset all cooldowns",
			run: func(ctx context.Context, hybrid *HybridStore, channelID int64) error {
				return hybrid.ResetAllCooldowns(ctx, channelID)
			},
		},
		{
			name: "bump model cooldown",
			run: func(ctx context.Context, hybrid *HybridStore, channelID int64) error {
				_, err := hybrid.BumpModelCooldown(ctx, channelID, "gpt-runtime", time.Now(), 502)
				return err
			},
		},
		{
			name: "set model cooldown",
			run: func(ctx context.Context, hybrid *HybridStore, channelID int64) error {
				return hybrid.SetModelCooldown(ctx, channelID, "gpt-runtime", time.Now().Add(time.Minute))
			},
		},
		{
			name: "reset model cooldown",
			run: func(ctx context.Context, hybrid *HybridStore, channelID int64) error {
				return hybrid.ResetModelCooldown(ctx, channelID, "gpt-runtime")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary := createTestSQLiteStore(t)
			cache := createTestSQLiteStore(t)
			hybrid := NewHybridStore(cache, primary)
			t.Cleanup(func() { _ = hybrid.Close() })
			ctx := context.Background()

			channel, err := hybrid.CreateConfig(ctx, &model.Config{
				Name:         "runtime-setter-order",
				URLs:         model.ChannelURLs{{URL: "https://runtime-setter.example.com"}},
				Enabled:      true,
				ModelEntries: []model.ModelEntry{{Model: "gpt-runtime"}},
			})
			if err != nil {
				t.Fatalf("create channel: %v", err)
			}
			if err := hybrid.CreateAPIKeysBatch(ctx, []*model.APIKey{{
				ChannelID:   channel.ID,
				KeyIndex:    0,
				APIKey:      "runtime-setter-key",
				KeyStrategy: model.KeyStrategySequential,
			}}); err != nil {
				t.Fatalf("create key: %v", err)
			}

			hybrid.configurationMu.Lock()
			locked := true
			defer func() {
				if locked {
					hybrid.configurationMu.Unlock()
				}
			}()

			started := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				close(started)
				done <- tt.run(ctx, hybrid, channel.ID)
			}()

			<-started
			select {
			case err := <-done:
				t.Fatalf("runtime setter ignored the shared configuration mutex: %v", err)
			case <-time.After(100 * time.Millisecond):
			}

			hybrid.configurationMu.Unlock()
			locked = false
			if err := <-done; err != nil {
				t.Fatalf("runtime setter: %v", err)
			}
		})
	}
}

func TestConfigurationMirrorPreservesNewerCachedAPIKeyHealth(t *testing.T) {
	primary := createTestSQLiteStore(t)
	cache := createTestSQLiteStore(t)
	hybrid := NewHybridStore(cache, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()

	primaryConfig, err := primary.CreateConfig(ctx, &model.Config{
		Name:    "health-mirror-order",
		URLs:    model.ChannelURLs{{URL: "https://health-mirror.example.com"}},
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("create primary config: %v", err)
	}
	if err := primary.CreateAPIKeysBatch(ctx, []*model.APIKey{{
		ChannelID:   primaryConfig.ID,
		KeyIndex:    0,
		APIKey:      "health-mirror-key",
		KeyStrategy: model.KeyStrategySequential,
		Health: model.APIKeyHealth{
			Status:     "invalid",
			Reason:     "stale primary observation",
			StatusCode: 401,
			CheckedAt:  3000,
		},
	}}); err != nil {
		t.Fatalf("create primary key: %v", err)
	}
	if _, err := cache.CreateConfig(ctx, &model.Config{
		ID:      primaryConfig.ID,
		Name:    primaryConfig.Name,
		URLs:    primaryConfig.URLs,
		Enabled: primaryConfig.Enabled,
	}); err != nil {
		t.Fatalf("create cached config: %v", err)
	}
	if err := cache.CreateAPIKeysBatch(ctx, []*model.APIKey{{
		ChannelID:   primaryConfig.ID,
		KeyIndex:    0,
		APIKey:      "health-mirror-key",
		KeyStrategy: model.KeyStrategySequential,
	}}); err != nil {
		t.Fatalf("create cached key: %v", err)
	}

	entry := &model.LogEntry{
		ChannelID:  primaryConfig.ID,
		APIKeyUsed: "health-mirror-key",
		StatusCode: 200,
		Model:      "gpt-health",
		Time:       model.JSONTime{Time: time.UnixMilli(9000)},
	}
	if err := cache.AddLog(ctx, entry); err != nil {
		t.Fatalf("record newer cached health: %v", err)
	}
	wantHealth, ok := model.ObserveAPIKeyHealth(entry)
	if !ok {
		t.Fatal("test log did not produce a health observation")
	}

	changes := newConfigurationChangeSet()
	changes.channel(primaryConfig.ID)
	if err := hybrid.mirrorConfigurationChanges(ctx, changes); err != nil {
		t.Fatalf("mirror configuration: %v", err)
	}

	key, err := cache.GetAPIKey(ctx, primaryConfig.ID, 0)
	if err != nil {
		t.Fatalf("read mirrored key: %v", err)
	}
	if key.Health != wantHealth {
		t.Fatalf("mirrored health=%+v, want newer cached health %+v", key.Health, wantHealth)
	}
}

func TestSiteMutationsThatChangeChannelsWaitForSharedConfigurationMutex(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(context.Context, Store, *HybridStore) (func(context.Context) error, error)
	}{
		{
			name: "update site account",
			prepare: func(ctx context.Context, primary Store, hybrid *HybridStore) (func(context.Context) error, error) {
				_, account, err := createSiteAndAccountForGateTest(ctx, primary, "update-account")
				if err != nil {
					return nil, err
				}
				updated := *account
				updated.Label = "updated-account"
				updated.Status = model.SiteAccountStatusExpired
				return func(ctx context.Context) error {
					_, err := hybrid.UpdateSiteAccount(ctx, account.ID, &updated)
					return err
				}, nil
			},
		},
		{
			name: "delete site",
			prepare: func(ctx context.Context, primary Store, hybrid *HybridStore) (func(context.Context) error, error) {
				site, _, err := createSiteAndAccountForGateTest(ctx, primary, "delete-site")
				if err != nil {
					return nil, err
				}
				return func(ctx context.Context) error {
					return hybrid.DeleteSite(ctx, site.ID)
				}, nil
			},
		},
		{
			name: "delete site account",
			prepare: func(ctx context.Context, primary Store, hybrid *HybridStore) (func(context.Context) error, error) {
				_, account, err := createSiteAndAccountForGateTest(ctx, primary, "delete-account")
				if err != nil {
					return nil, err
				}
				return func(ctx context.Context) error {
					return hybrid.DeleteSiteAccount(ctx, account.ID)
				}, nil
			},
		},
		{
			name: "upsert site projection",
			prepare: func(ctx context.Context, primary Store, hybrid *HybridStore) (func(context.Context) error, error) {
				site, account, err := createSiteAndAccountForGateTest(ctx, primary, "upsert-projection")
				if err != nil {
					return nil, err
				}
				input := model.SiteProjectionInput{
					SiteAccountID: account.ID,
					ProjectionKey: "gate:upsert",
					Name:          "gate-upsert",
					BaseURL:       site.BaseURL,
					Protocols:     []string{"openai"},
					Models:        []string{"gpt-gate"},
					APIKey:        "gate-upsert-key",
					Enabled:       true,
				}
				return func(ctx context.Context) error {
					_, err := hybrid.UpsertSiteProjection(ctx, input)
					return err
				}, nil
			},
		},
		{
			name: "deactivate site projections",
			prepare: func(ctx context.Context, primary Store, hybrid *HybridStore) (func(context.Context) error, error) {
				site, account, err := createSiteAndAccountForGateTest(ctx, primary, "deactivate-projection")
				if err != nil {
					return nil, err
				}
				if _, err := primary.UpsertSiteProjection(ctx, model.SiteProjectionInput{
					SiteAccountID: account.ID,
					ProjectionKey: "gate:deactivate",
					Name:          "gate-deactivate",
					BaseURL:       site.BaseURL,
					Protocols:     []string{"openai"},
					Models:        []string{"gpt-gate"},
					APIKey:        "gate-deactivate-key",
					Enabled:       true,
				}); err != nil {
					return nil, err
				}
				return func(ctx context.Context) error {
					return hybrid.DeactivateSiteProjectionsExcept(ctx, account.ID, nil)
				}, nil
			},
		},
		{
			name: "prune site projections",
			prepare: func(ctx context.Context, primary Store, hybrid *HybridStore) (func(context.Context) error, error) {
				site, account, err := createSiteAndAccountForGateTest(ctx, primary, "prune-projection")
				if err != nil {
					return nil, err
				}
				if _, err := primary.UpsertSiteProjection(ctx, model.SiteProjectionInput{
					SiteAccountID: account.ID,
					ProjectionKey: "gate:prune",
					Name:          "gate-prune",
					BaseURL:       site.BaseURL,
					Protocols:     []string{"openai"},
					Models:        []string{"gpt-gate"},
					APIKey:        "gate-prune-key",
					Enabled:       true,
				}); err != nil {
					return nil, err
				}
				return func(ctx context.Context) error {
					return hybrid.PruneSiteProjectionsExcept(ctx, account.ID, nil)
				}, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary := createTestSQLiteStore(t)
			cache := createTestSQLiteStore(t)
			hybrid := NewHybridStore(cache, primary)
			t.Cleanup(func() { _ = hybrid.Close() })
			ctx := context.Background()

			run, err := tt.prepare(ctx, primary, hybrid)
			if err != nil {
				t.Fatalf("prepare site mutation: %v", err)
			}

			hybrid.configurationMu.Lock()
			locked := true
			defer func() {
				if locked {
					hybrid.configurationMu.Unlock()
				}
			}()

			started := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				close(started)
				done <- run(ctx)
			}()

			<-started
			select {
			case err := <-done:
				t.Fatalf("site mutation ignored the shared configuration mutex: %v", err)
			case <-time.After(100 * time.Millisecond):
			}

			hybrid.configurationMu.Unlock()
			locked = false
			if err := <-done; err != nil {
				t.Fatalf("site mutation: %v", err)
			}
		})
	}
}

func createSiteAndAccountForGateTest(
	ctx context.Context,
	store Store,
	suffix string,
) (*model.Site, *model.SiteAccount, error) {
	site, err := store.CreateSite(ctx, &model.Site{
		Name:     "gate-" + suffix,
		Platform: model.SitePlatformNewAPIFamily,
		BaseURL:  "https://gate-" + suffix + ".example.com",
		Enabled:  true,
		Timezone: "Asia/Shanghai",
		TagsJSON: "[]",
	})
	if err != nil {
		return nil, nil, err
	}
	account, err := store.CreateSiteAccount(ctx, &model.SiteAccount{
		SiteID:               site.ID,
		Label:                "gate-account-" + suffix,
		CredentialType:       model.CredentialTypeAPIKey,
		CredentialCiphertext: "import-test-key-zero",
		CredentialKeyVersion: "v1",
		Enabled:              true,
		Status:               model.SiteAccountStatusHealthy,
		BalanceCurrency:      "USD",
		LastRefreshStatus:    "unknown",
		LastCheckinStatus:    "unknown",
	})
	if err != nil {
		return nil, nil, err
	}
	return site, account, nil
}

func TestConfigurationProjectionRecorderPropagatesBindingErrors(t *testing.T) {
	listErr := errors.New("binding enumeration failed")
	tests := []struct {
		name  string
		run   func(*configurationTransactionStore) error
		calls func(*bindingErrorStore) int
	}{
		{
			name: "deactivate",
			run: func(store *configurationTransactionStore) error {
				return store.DeactivateSiteProjectionsExcept(context.Background(), 1, nil)
			},
			calls: func(store *bindingErrorStore) int { return store.deactivateCalls },
		},
		{
			name: "prune",
			run: func(store *configurationTransactionStore) error {
				return store.PruneSiteProjectionsExcept(context.Background(), 1, nil)
			},
			calls: func(store *bindingErrorStore) int { return store.pruneCalls },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary := &bindingErrorStore{err: listErr}
			store := &configurationTransactionStore{
				Store:   primary,
				changes: newConfigurationChangeSet(),
			}
			err := tt.run(store)
			if !errors.Is(err, listErr) {
				t.Fatalf("error=%v, want binding enumeration failure", err)
			}
			if calls := tt.calls(primary); calls != 0 {
				t.Fatalf("primary mutation ran %d times after binding enumeration failed", calls)
			}
		})
	}
}

type bindingErrorStore struct {
	Store
	err             error
	deactivateCalls int
	pruneCalls      int
}

func (s *bindingErrorStore) ListSiteChannelBindings(context.Context) ([]*model.SiteChannelBinding, error) {
	return nil, s.err
}

func (s *bindingErrorStore) DeactivateSiteProjectionsExcept(context.Context, int64, []string) error {
	s.deactivateCalls++
	return nil
}

func (s *bindingErrorStore) PruneSiteProjectionsExcept(context.Context, int64, []string) error {
	s.pruneCalls++
	return nil
}
