package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

// ErrConfigurationCommitUnknown aliases the SQL transaction sentinel and is
// surfaced only when WithSerializableTransaction cannot determine commit outcome.
var ErrConfigurationCommitUnknown = sqlstore.ErrTransactionCommitUnknown

// IsConfigurationCommitUnknown reports whether a configuration transaction
// failed at commit with an unknown outcome. Such a transaction must not be
// retried blindly because the primary may already contain its changes.
func IsConfigurationCommitUnknown(err error) bool {
	return errors.Is(err, ErrConfigurationCommitUnknown)
}

// ErrConfigurationCacheDegraded marks a configuration transaction that has
// committed in the authoritative store while refreshing the hybrid SQLite
// replica failed. Callers must treat this as committed with a stale cache.
var ErrConfigurationCacheDegraded = errors.New("configuration committed but cache mirror failed")

// ConfigurationCacheDegradedError distinguishes a post-commit cache failure
// from a transaction rollback. The committed configuration is authoritative.
type ConfigurationCacheDegradedError struct {
	Err error
}

func (e *ConfigurationCacheDegradedError) Error() string {
	if e == nil || e.Err == nil {
		return ErrConfigurationCacheDegraded.Error()
	}
	return ErrConfigurationCacheDegraded.Error() + ": " + e.Err.Error()
}

func (e *ConfigurationCacheDegradedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *ConfigurationCacheDegradedError) Is(target error) bool {
	return target == ErrConfigurationCacheDegraded
}

// IsConfigurationCacheDegraded reports whether err is the typed post-commit
// cache failure returned by WithConfigurationTransaction.
func IsConfigurationCacheDegraded(err error) bool {
	return errors.Is(err, ErrConfigurationCacheDegraded)
}

type configurationSourceProvider interface {
	configurationSource() Store
}

// ConfigurationSource returns the store that owns authoritative configuration
// rows. Hybrid stores use the primary database; other SQL stores are read and
// written directly.
func ConfigurationSource(store Store) Store {
	if store == nil {
		return nil
	}
	if provider, ok := store.(configurationSourceProvider); ok {
		return provider.configurationSource()
	}
	if hybrid, ok := store.(*HybridStore); ok {
		return hybrid.mysql
	}
	return store
}

// WithConfigurationTransaction runs fn inside one serializable transaction on
// the authoritative configuration store. Every read and nested WithTransaction
// call made through the supplied store shares that transaction. Hybrid replicas
// are touched only after commit, and only for configuration rows the callback
// changed.
//
// A callback error rolls the primary transaction back. A replica failure after
// commit returns *ConfigurationCacheDegradedError; it never reports a rollback.
func WithConfigurationTransaction(
	ctx context.Context,
	store Store,
	fn func(context.Context, Store) error,
) error {
	if store == nil {
		return errors.New("configuration transaction requires a store")
	}
	if fn == nil {
		return errors.New("configuration transaction callback is required")
	}
	if active, ok := store.(*configurationTransactionStore); ok {
		return fn(ctx, active)
	}

	source := ConfigurationSource(store)
	primary, ok := source.(*sqlstore.SQLStore)
	if !ok {
		return fmt.Errorf("configuration transaction requires a SQL store, got %T", source)
	}

	hybrid, isHybrid := store.(*HybridStore)
	if isHybrid {
		hybrid.configurationMu.Lock()
		defer hybrid.configurationMu.Unlock()
	}
	var changes *configurationChangeSet
	err := primary.WithSerializableTransaction(ctx, func(txCtx context.Context, tx *sqlstore.SQLStore) error {
		if !isHybrid {
			return fn(txCtx, tx)
		}
		changes = newConfigurationChangeSet()
		return fn(txCtx, &configurationTransactionStore{Store: tx, changes: changes})
	})
	if err != nil {
		return err
	}
	if !isHybrid || changes == nil {
		return nil
	}

	mirrorCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	mirrorErr := hybrid.syncSQLite("ConfigurationTransaction", func() error {
		return hybrid.mirrorConfigurationChanges(mirrorCtx, changes)
	})
	if mirrorErr != nil {
		return &ConfigurationCacheDegradedError{Err: mirrorErr}
	}
	return nil
}

// configurationTransactionStore delegates every operation to the primary
// transaction view while recording configuration rows for post-commit mirroring.
type configurationTransactionStore struct {
	Store
	changes *configurationChangeSet
}

func (s *configurationTransactionStore) configurationSource() Store {
	return s.Store
}

func (s *configurationTransactionStore) recordChannel(id int64) {
	if id > 0 {
		s.changes.channel(id)
	}
}

func (s *configurationTransactionStore) CreateConfig(ctx context.Context, c *model.Config) (*model.Config, error) {
	result, err := s.Store.CreateConfig(ctx, c)
	if err == nil && result != nil {
		s.recordChannel(result.ID)
	}
	return result, err
}

func (s *configurationTransactionStore) UpdateConfig(ctx context.Context, id int64, upd *model.Config) (*model.Config, error) {
	result, err := s.Store.UpdateConfig(ctx, id, upd)
	if err == nil {
		s.recordChannel(id)
	}
	return result, err
}

func (s *configurationTransactionStore) UpdateOAuthCredential(ctx context.Context, id int64, credential string) error {
	if err := s.Store.UpdateOAuthCredential(ctx, id, credential); err != nil {
		return err
	}
	s.recordChannel(id)
	return nil
}

func (s *configurationTransactionStore) UpdateChannelEnabled(ctx context.Context, id int64, enabled bool) (*model.Config, error) {
	result, err := s.Store.UpdateChannelEnabled(ctx, id, enabled)
	if err == nil {
		s.recordChannel(id)
	}
	return result, err
}

func (s *configurationTransactionStore) DeleteConfig(ctx context.Context, id int64) error {
	if err := s.Store.DeleteConfig(ctx, id); err != nil {
		return err
	}
	s.recordChannel(id)
	return nil
}

func (s *configurationTransactionStore) BatchPatchConfigs(
	ctx context.Context,
	channelIDs []int64,
	patch model.BatchConfigPatch,
) (model.BatchConfigPatchResult, error) {
	result, err := s.Store.BatchPatchConfigs(ctx, channelIDs, patch)
	if err != nil {
		return result, err
	}
	for _, id := range channelIDs {
		s.recordChannel(id)
	}
	return result, nil
}

func (s *configurationTransactionStore) BatchUpdatePriority(
	ctx context.Context,
	updates []struct {
		ID       int64
		Priority int
	},
) (int64, error) {
	affected, err := s.Store.BatchUpdatePriority(ctx, updates)
	if err != nil {
		return affected, err
	}
	for _, update := range updates {
		s.recordChannel(update.ID)
	}
	return affected, nil
}

func (s *configurationTransactionStore) ImportChannelBatch(
	ctx context.Context,
	channels []*model.ChannelWithKeys,
) (created, updated int, err error) {
	created, updated, err = s.Store.ImportChannelBatch(ctx, channels)
	if err != nil {
		return created, updated, err
	}
	for _, channel := range channels {
		if channel != nil && channel.Config != nil {
			s.recordChannel(channel.Config.ID)
		}
	}
	return created, updated, nil
}

func (s *configurationTransactionStore) CreateAPIKeysBatch(ctx context.Context, keys []*model.APIKey) error {
	if err := s.Store.CreateAPIKeysBatch(ctx, keys); err != nil {
		return err
	}
	for _, key := range keys {
		if key != nil {
			s.recordChannel(key.ChannelID)
		}
	}
	return nil
}

func (s *configurationTransactionStore) DeleteAllAPIKeys(ctx context.Context, channelID int64) error {
	if err := s.Store.DeleteAllAPIKeys(ctx, channelID); err != nil {
		return err
	}
	s.recordChannel(channelID)
	return nil
}

func (s *configurationTransactionStore) CreateAuthToken(ctx context.Context, token *model.AuthToken) error {
	if err := s.Store.CreateAuthToken(ctx, token); err != nil {
		return err
	}
	if token != nil {
		s.changes.authToken(token.ID)
	}
	return nil
}

func (s *configurationTransactionStore) EnsureAuthToken(ctx context.Context, token *model.AuthToken) (bool, error) {
	created, err := s.Store.EnsureAuthToken(ctx, token)
	if err != nil {
		return created, err
	}
	if token != nil {
		s.changes.authToken(token.ID)
	}
	return created, nil
}

func (s *configurationTransactionStore) UpdateAuthToken(ctx context.Context, token *model.AuthToken) error {
	if err := s.Store.UpdateAuthToken(ctx, token); err != nil {
		return err
	}
	if token != nil {
		s.changes.authToken(token.ID)
	}
	return nil
}

func (s *configurationTransactionStore) DeleteAuthToken(ctx context.Context, id int64) error {
	if err := s.Store.DeleteAuthToken(ctx, id); err != nil {
		return err
	}
	s.changes.authToken(id)
	return nil
}

func (s *configurationTransactionStore) UpdateSetting(ctx context.Context, key, value string) error {
	if err := s.Store.UpdateSetting(ctx, key, value); err != nil {
		return err
	}
	s.changes.setting(key)
	return nil
}

// CompareAndSwapSetting participates in this authoritative transaction and
// marks a successful conditional update for the post-commit hybrid mirror.
func (s *configurationTransactionStore) CompareAndSwapSetting(ctx context.Context, key, expectedValue, newValue string) (bool, error) {
	cas, ok := s.Store.(interface {
		CompareAndSwapSetting(context.Context, string, string, string) (bool, error)
	})
	if !ok {
		return false, errors.New("conditional setting update is unavailable in this transaction")
	}
	updated, err := cas.CompareAndSwapSetting(ctx, key, expectedValue, newValue)
	if updated {
		s.changes.setting(key)
	}
	return updated, err
}

func (s *configurationTransactionStore) BatchUpdateSettings(ctx context.Context, updates map[string]string) error {
	if err := s.Store.BatchUpdateSettings(ctx, updates); err != nil {
		return err
	}
	for key := range updates {
		s.changes.setting(key)
	}
	return nil
}

func (s *configurationTransactionStore) UpsertBackupConfig(ctx context.Context, config *model.BackupConfig) error {
	if err := s.Store.UpsertBackupConfig(ctx, config); err != nil {
		return err
	}
	s.changes.backup = true
	return nil
}

func (s *configurationTransactionStore) UpsertWebhookConfig(ctx context.Context, config *model.WebhookConfig) error {
	if err := s.Store.UpsertWebhookConfig(ctx, config); err != nil {
		return err
	}
	s.changes.webhook = true
	return nil
}

func (s *configurationTransactionStore) UpdateSiteAccount(
	ctx context.Context,
	id int64,
	account *model.SiteAccount,
) (*model.SiteAccount, error) {
	channelIDs, err := s.boundChannelIDs(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list bound channels for site account %d: %w", id, err)
	}
	result, err := s.Store.UpdateSiteAccount(ctx, id, account)
	if err != nil {
		return nil, err
	}
	for _, channelID := range channelIDs {
		s.recordChannel(channelID)
	}
	return result, nil
}

func (s *configurationTransactionStore) UpsertSiteProjection(
	ctx context.Context,
	input model.SiteProjectionInput,
) (*model.SiteProjectionResult, error) {
	result, err := s.Store.UpsertSiteProjection(ctx, input)
	if err == nil && result != nil && result.Channel != nil {
		s.recordChannel(result.Channel.ID)
	}
	return result, err
}

func (s *configurationTransactionStore) DeactivateSiteProjectionsExcept(
	ctx context.Context,
	siteAccountID int64,
	activeProjectionKeys []string,
) error {
	channelIDs, err := s.projectedChannelIDs(ctx, siteAccountID)
	if err != nil {
		return fmt.Errorf("list projected channels for site account %d: %w", siteAccountID, err)
	}
	if err := s.Store.DeactivateSiteProjectionsExcept(ctx, siteAccountID, activeProjectionKeys); err != nil {
		return err
	}
	for _, id := range channelIDs {
		s.recordChannel(id)
	}
	return nil
}

func (s *configurationTransactionStore) PruneSiteProjectionsExcept(
	ctx context.Context,
	siteAccountID int64,
	activeProjectionKeys []string,
) error {
	channelIDs, err := s.projectedChannelIDs(ctx, siteAccountID)
	if err != nil {
		return fmt.Errorf("list projected channels for site account %d: %w", siteAccountID, err)
	}
	if err := s.Store.PruneSiteProjectionsExcept(ctx, siteAccountID, activeProjectionKeys); err != nil {
		return err
	}
	for _, id := range channelIDs {
		s.recordChannel(id)
	}
	return nil
}

func (s *configurationTransactionStore) projectedChannelIDs(ctx context.Context, siteAccountID int64) ([]int64, error) {
	bindings, err := s.ListSiteChannelBindings(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil || binding.SiteAccountID != siteAccountID || binding.ChannelID <= 0 {
			continue
		}
		if binding.Ownership == "projected" {
			ids = append(ids, binding.ChannelID)
		}
	}
	return ids, nil
}

func (s *configurationTransactionStore) boundChannelIDs(ctx context.Context, siteAccountID int64) ([]int64, error) {
	bindings, err := s.ListSiteChannelBindings(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil || binding.SiteAccountID != siteAccountID || binding.ChannelID <= 0 {
			continue
		}
		ids = append(ids, binding.ChannelID)
	}
	return ids, nil
}

type configurationChangeSet struct {
	mu           sync.Mutex
	channelIDs   map[int64]struct{}
	authTokenIDs map[int64]struct{}
	settingKeys  map[string]struct{}
	webhook      bool
	backup       bool
}

func newConfigurationChangeSet() *configurationChangeSet {
	return &configurationChangeSet{
		channelIDs:   make(map[int64]struct{}),
		authTokenIDs: make(map[int64]struct{}),
		settingKeys:  make(map[string]struct{}),
	}
}

func (c *configurationChangeSet) channel(id int64) {
	if c == nil || id <= 0 {
		return
	}
	c.mu.Lock()
	c.channelIDs[id] = struct{}{}
	c.mu.Unlock()
}

func (c *configurationChangeSet) authToken(id int64) {
	if c == nil || id <= 0 {
		return
	}
	c.mu.Lock()
	c.authTokenIDs[id] = struct{}{}
	c.mu.Unlock()
}

func (c *configurationChangeSet) setting(key string) {
	key = strings.TrimSpace(key)
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	c.settingKeys[key] = struct{}{}
	c.mu.Unlock()
}

func (c *configurationChangeSet) snapshot() (channels, tokens []int64, settings []string, webhook, backup bool) {
	if c == nil {
		return nil, nil, nil, false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	channels = make([]int64, 0, len(c.channelIDs))
	for id := range c.channelIDs {
		channels = append(channels, id)
	}
	tokens = make([]int64, 0, len(c.authTokenIDs))
	for id := range c.authTokenIDs {
		tokens = append(tokens, id)
	}
	settings = make([]string, 0, len(c.settingKeys))
	for key := range c.settingKeys {
		settings = append(settings, key)
	}
	return channels, tokens, settings, c.webhook, c.backup
}

func (h *HybridStore) mirrorConfigurationChanges(ctx context.Context, changes *configurationChangeSet) error {
	channels, tokens, settings, webhook, backup := changes.snapshot()
	var errs []error
	if err := h.mirrorConfigurationChannels(ctx, channels); err != nil {
		errs = append(errs, fmt.Errorf("mirror channels: %w", err))
	}
	h.tokenAccountingMu.Lock()
	tokenErr := h.mirrorConfigurationAuthTokens(ctx, tokens)
	h.tokenAccountingMu.Unlock()
	if tokenErr != nil {
		errs = append(errs, fmt.Errorf("mirror auth tokens: %w", tokenErr))
	}
	if err := h.mirrorConfigurationSettings(ctx, settings); err != nil {
		errs = append(errs, fmt.Errorf("mirror settings: %w", err))
	}
	if webhook {
		config, err := h.mysql.GetWebhookConfig(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("read primary webhook config: %w", err))
		} else if err := h.sqlite.UpsertWebhookConfig(ctx, config); err != nil {
			errs = append(errs, fmt.Errorf("mirror webhook config: %w", err))
		}
	}
	if backup {
		config, err := h.mysql.GetBackupConfig(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("read primary backup config: %w", err))
		} else if err := h.sqlite.UpsertBackupConfig(ctx, config); err != nil {
			errs = append(errs, fmt.Errorf("mirror backup config: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (h *HybridStore) mirrorConfigurationChannels(ctx context.Context, channelIDs []int64) error {
	type channelSnapshot struct {
		channelID int64
		config    *model.Config
		keys      []*model.APIKey
		missing   bool
	}

	snapshots := make([]channelSnapshot, 0, len(channelIDs))
	var snapshotErrs []error
	err := h.mysql.WithSerializableTransaction(ctx, func(ctx context.Context, primary *sqlstore.SQLStore) error {
		for _, channelID := range channelIDs {
			config, err := primary.GetConfig(ctx, channelID)
			if err != nil {
				if isConfigurationNotFound(err) {
					snapshots = append(snapshots, channelSnapshot{channelID: channelID, missing: true})
					continue
				}
				snapshotErrs = append(snapshotErrs, fmt.Errorf("read channel %d: %w", channelID, err))
				continue
			}

			snapshot := config.Clone()
			snapshot.ID = channelID
			snapshot.KeyCount = 0
			var keys []*model.APIKey
			if snapshot.GetAuthType() == model.AuthTypeAPIKey {
				keys, err = primary.GetAPIKeys(ctx, channelID)
				if err != nil {
					snapshotErrs = append(snapshotErrs, fmt.Errorf("read primary api keys for channel %d: %w", channelID, err))
					continue
				}
			}
			snapshots = append(snapshots, channelSnapshot{
				channelID: channelID,
				config:    snapshot,
				keys:      keys,
			})
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("snapshot primary channels: %w", err)
	}
	var errs []error
	errs = append(errs, snapshotErrs...)
	for _, snapshot := range snapshots {
		channelID := snapshot.channelID
		if snapshot.missing {
			deleteErr := h.sqlite.WithSerializableTransaction(ctx, func(ctx context.Context, cache *sqlstore.SQLStore) error {
				return cache.DeleteConfig(ctx, channelID)
			})
			if deleteErr != nil && !isConfigurationNotFound(deleteErr) {
				errs = append(errs, fmt.Errorf("delete channel %d: %w", channelID, deleteErr))
			}
			continue
		}

		config := snapshot.config
		keys := snapshot.keys
		authType := config.GetAuthType()

		applyErr := h.sqlite.WithSerializableTransaction(ctx, func(ctx context.Context, cache *sqlstore.SQLStore) error {
			cached, cacheErr := cache.GetConfig(ctx, channelID)
			if cacheErr != nil && !isConfigurationNotFound(cacheErr) {
				return fmt.Errorf("read cached channel %d: %w", channelID, cacheErr)
			}
			replace := cacheErr != nil || cached.GetAuthType() != authType
			if replace && cached != nil && cached.GetAuthType() == model.AuthTypeAPIKey && authType != model.AuthTypeAPIKey {
				// The key API intentionally rejects mutations after auth_type
				// changes. Clear stale keys while the cached row is still API-key
				// backed; the enclosing transaction still hides the empty state.
				if err := cache.DeleteAllAPIKeys(ctx, channelID); err != nil {
					return fmt.Errorf("clear cached api keys before replacing channel %d: %w", channelID, err)
				}
			}

			switch {
			case replace:
				if _, err := cache.CreateConfig(ctx, config); err != nil {
					return fmt.Errorf("replace cached channel %d after auth type change: %w", channelID, err)
				}
			default:
				if _, err := cache.UpdateConfig(ctx, channelID, config); err != nil {
					return fmt.Errorf("update cached channel %d: %w", channelID, err)
				}
				if authType != model.AuthTypeAPIKey {
					if err := cache.UpdateOAuthCredential(ctx, channelID, config.OAuthCredential); err != nil {
						return fmt.Errorf("update cached OAuth credential for channel %d: %w", channelID, err)
					}
				}
			}

			if authType == model.AuthTypeAPIKey {
				cachedKeys, err := cache.GetAPIKeys(ctx, channelID)
				if err != nil {
					return fmt.Errorf("read cached api keys for channel %d: %w", channelID, err)
				}
				keys = preserveNewerAPIKeyHealth(keys, cachedKeys)
				if err := cache.DeleteAllAPIKeys(ctx, channelID); err != nil {
					return fmt.Errorf("clear cached api keys for channel %d: %w", channelID, err)
				}
				if len(keys) > 0 {
					if err := cache.CreateAPIKeysBatch(ctx, keys); err != nil {
						return fmt.Errorf("mirror api keys for channel %d: %w", channelID, err)
					}
				}
			}
			return nil
		})
		if applyErr != nil {
			errs = append(errs, applyErr)
		}
	}
	return errors.Join(errs...)
}

// preserveNewerAPIKeyHealth keeps log-derived observations that reached the
// local replica after the primary snapshot was taken. Key IDs and indices may
// differ across the two stores, while the credential value is the identity used
// by health observation itself.
func preserveNewerAPIKeyHealth(primaryKeys, cachedKeys []*model.APIKey) []*model.APIKey {
	latestCached := make(map[string]model.APIKeyHealth, len(cachedKeys))
	for _, key := range cachedKeys {
		if key == nil || key.APIKey == "" {
			continue
		}
		current, ok := latestCached[key.APIKey]
		if !ok || key.Health.CheckedAt > current.CheckedAt {
			latestCached[key.APIKey] = key.Health
		}
	}
	for _, key := range primaryKeys {
		if key == nil {
			continue
		}
		cachedHealth, ok := latestCached[key.APIKey]
		if ok && cachedHealth.CheckedAt > key.Health.CheckedAt {
			key.Health = cachedHealth
		}
	}
	return primaryKeys
}

func (h *HybridStore) mirrorConfigurationAuthTokens(ctx context.Context, tokenIDs []int64) error {
	type tokenSnapshot struct {
		tokenID int64
		token   *model.AuthToken
		missing bool
	}

	snapshots := make([]tokenSnapshot, 0, len(tokenIDs))
	var errs []error
	for _, tokenID := range tokenIDs {
		token, err := h.mysql.GetAuthToken(ctx, tokenID)
		if err != nil {
			if errors.Is(err, model.ErrAuthTokenNotFound) || isConfigurationNotFound(err) {
				snapshots = append(snapshots, tokenSnapshot{tokenID: tokenID, missing: true})
				continue
			}
			errs = append(errs, fmt.Errorf("read token %d: %w", tokenID, err))
			continue
		}
		snapshots = append(snapshots, tokenSnapshot{tokenID: tokenID, token: token})
	}
	for _, snapshot := range snapshots {
		if snapshot.missing {
			if deleteErr := h.sqlite.DeleteAuthToken(ctx, snapshot.tokenID); deleteErr != nil && !isConfigurationNotFound(deleteErr) {
				errs = append(errs, fmt.Errorf("delete token %d: %w", snapshot.tokenID, deleteErr))
			}
			continue
		}
		if err := h.sqlite.UpsertAuthTokenAllFields(ctx, snapshot.token); err != nil {
			errs = append(errs, fmt.Errorf("upsert token %d: %w", snapshot.tokenID, err))
		}
	}
	return errors.Join(errs...)
}

func (h *HybridStore) mirrorConfigurationSettings(ctx context.Context, keys []string) error {
	var errs []error
	for _, key := range keys {
		setting, err := h.mysql.GetSetting(ctx, key)
		if err != nil {
			if errors.Is(err, model.ErrSettingNotFound) || isConfigurationNotFound(err) {
				if deleteErr := h.sqlite.DeleteSetting(ctx, key); deleteErr != nil && !isConfigurationNotFound(deleteErr) {
					errs = append(errs, fmt.Errorf("delete setting %s: %w", key, deleteErr))
				}
				continue
			}
			errs = append(errs, fmt.Errorf("read setting %s: %w", key, err))
			continue
		}
		if err := h.sqlite.UpsertSettingRecord(ctx, setting); err != nil {
			errs = append(errs, fmt.Errorf("upsert setting %s: %w", key, err))
		}
	}
	return errors.Join(errs...)
}

func isConfigurationNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}
