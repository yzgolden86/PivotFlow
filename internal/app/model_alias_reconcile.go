package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

var errModelAliasReconcileUnavailable = errors.New("model alias reconciliation is unavailable")
var errModelAliasReplicaRefresh = errors.New("model alias cleanup committed but the configuration replica refresh failed")
var errModelAliasReconcileContended = errors.New("model alias cleanup needs retry after concurrent setting changes")

// persistChannelModelReplace keeps the old topology available when cleanup has
// a known failure. Both topology and aliases commit together; only afterward
// may the live registry be refreshed from authoritative settings.
func (s *Server) persistChannelModelReplace(ctx context.Context, before, after *model.Config) error {
	if s == nil || before == nil || after == nil {
		return errModelAliasReconcileUnavailable
	}
	var aliasesChanged bool
	err := storage.WithConfigurationTransaction(ctx, s.store, func(txCtx context.Context, txStore storage.Store) error {
		current, err := txStore.GetConfig(txCtx, after.ID)
		if err != nil {
			return err
		}
		if !modelEntriesEqual(current.ModelEntries, before.ModelEntries) {
			return errModelAliasReconcileContended
		}
		// Preserve unrelated fields that may have been edited while model
		// discovery ran; only this refresh's model/check fields are replaced.
		current.ModelEntries = append([]model.ModelEntry(nil), after.ModelEntries...)
		if current.ScheduledCheckModel != before.ScheduledCheckModel && after.ScheduledCheckModel != before.ScheduledCheckModel {
			return errModelAliasReconcileContended
		}
		if current.ScheduledCheckModel == before.ScheduledCheckModel {
			current.ScheduledCheckModel = after.ScheduledCheckModel
		}
		if _, err := txStore.UpdateConfig(txCtx, after.ID, current); err != nil {
			return err
		}
		if s.configService == nil || s.modelAliases == nil {
			return nil
		}
		aliasesChanged, err = reconcileModelAliasMembers(txCtx, txStore, NewConfigService(txStore),
			removedEnabledModelNames(before, current), nil)
		return err
	})
	committed := err == nil || storage.IsConfigurationCacheDegraded(err)
	unknown := storage.IsConfigurationCommitUnknown(err)
	if aliasesChanged && (committed || unknown) {
		reloadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if reloadErr := s.reloadModelAliasesFromPersistedSetting(reloadCtx); reloadErr != nil {
			if unknown {
				return errors.Join(err, reloadErr)
			}
			return &storage.ConfigurationCacheDegradedError{Err: errors.Join(err, reloadErr)}
		}
	}
	return err
}

// reloadModelAliasesFromPersistedSetting refreshes both the configuration cache
// and the immutable runtime snapshot after a successful conditional update.
func (s *Server) reloadModelAliasesFromPersistedSetting(ctx context.Context) error {
	if s == nil || s.configService == nil || s.modelAliases == nil {
		return errModelAliasReconcileUnavailable
	}
	source := storage.ConfigurationSource(s.store)
	if source == nil {
		return errModelAliasReconcileUnavailable
	}
	setting, err := source.GetSetting(ctx, modelAliasGroupsSettingKey)
	if err != nil {
		return fmt.Errorf("load authoritative model alias groups: %w", err)
	}
	s.configService.applySettingSnapshot(modelAliasGroupsSettingKey, setting)
	s.modelAliases.reload(s.configService)
	s.invalidateRequestCache("aliases_changed")
	return nil
}

func (s *siteControlService) reconcileModelAliasesAfterSiteRefresh(
	ctx context.Context,
	before, after []model.SiteAccountModel,
) error {
	if s == nil || s.configService == nil || s.onModelAliasesChanged == nil {
		return nil
	}
	_, err := reconcileModelAliasMembers(
		ctx,
		s.store,
		s.configService,
		removedSiteModelNames(before, after),
		s.onModelAliasesChanged,
	)
	return err
}

func reconcileModelAliasMembers(
	ctx context.Context,
	store storage.Store,
	configService *ConfigService,
	removedCandidates []string,
	reload func(context.Context) error,
) (bool, error) {
	if store == nil || configService == nil || len(removedCandidates) == 0 {
		return false, nil
	}
	source := storage.ConfigurationSource(store)
	if source == nil {
		return false, errModelAliasReconcileUnavailable
	}
	candidates := exactModelNameSet(removedCandidates)
	if len(candidates) == 0 {
		return false, nil
	}

	for attempt := 0; attempt < 3; attempt++ {
		setting, err := source.GetSetting(ctx, modelAliasGroupsSettingKey)
		if err != nil {
			return false, fmt.Errorf("load model alias groups: %w", err)
		}
		if setting == nil {
			return false, nil
		}
		var groups []model.ModelAliasGroup
		if err := json.Unmarshal([]byte(setting.Value), &groups); err != nil {
			return false, fmt.Errorf("decode model alias groups: %w", err)
		}
		groups = model.NormalizeModelAliasGroups(groups)
		if len(groups) == 0 {
			return false, nil
		}

		channels, err := source.ListConfigs(ctx)
		if err != nil {
			return false, fmt.Errorf("list configured channels: %w", err)
		}
		configured := configuredModelNameSet(channels)
		removable := make(map[string]struct{})
		for candidate := range candidates {
			if _, present := configured[candidate]; !present {
				removable[candidate] = struct{}{}
			}
		}
		if len(removable) == 0 {
			return false, nil
		}
		tokens, err := source.ListActiveAuthTokens(ctx)
		if err != nil {
			return false, fmt.Errorf("list active token whitelists: %w", err)
		}
		tokenModels := activeTokenModelNameSet(tokens)
		changed := false
		for groupIndex := range groups {
			kept := make([]string, 0, len(groups[groupIndex].Aliases))
			for _, alias := range groups[groupIndex].Aliases {
				_, removed := removable[alias]
				_, tokenReferenced := tokenModels[modelAliasKey(alias)]
				if removed && !tokenReferenced {
					changed = true
					continue
				}
				kept = append(kept, alias)
			}
			groups[groupIndex].Aliases = kept
		}
		if !changed {
			return false, nil
		}

		nextValue, err := json.Marshal(groups)
		if err != nil {
			return false, fmt.Errorf("encode model alias groups: %w", err)
		}
		updated, err := configService.CompareAndSwapSetting(
			ctx,
			modelAliasGroupsSettingKey,
			setting.Value,
			string(nextValue),
		)
		if err != nil {
			if updated {
				// Hybrid stores report a primary commit followed by a replica
				// synchronization error this way. Reload from the authoritative
				// setting so the current process does not keep the stale mapping.
				if reload != nil {
					if reloadErr := reload(ctx); reloadErr != nil {
						log.Printf("[WARN] model alias cleanup committed to authoritative settings but live alias reload failed")
					}
				}
				log.Printf("[WARN] model alias cleanup committed to authoritative settings but the configuration replica refresh failed")
				return true, errModelAliasReplicaRefresh
			}
			return false, err
		}
		if !updated {
			// An administrator or another reconciler won. Re-read both the
			// setting and channel topology, then apply only removals that are
			// still supported by the latest persisted state.
			continue
		}
		if reload != nil {
			if err := reload(ctx); err != nil {
				return true, fmt.Errorf("reload model aliases: %w", err)
			}
		}
		return true, nil
	}
	return false, errModelAliasReconcileContended
}

func removedSiteModelNames(before, after []model.SiteAccountModel) []string {
	afterNames := make(map[string]struct{}, len(after))
	for _, fact := range after {
		if fact.Model != "" {
			afterNames[fact.Model] = struct{}{}
		}
	}
	removed := make([]string, 0)
	seen := make(map[string]struct{}, len(before))
	for _, fact := range before {
		name := fact.Model
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		if _, retained := afterNames[name]; retained {
			continue
		}
		removed = append(removed, name)
	}
	return removed
}

func removedEnabledModelNames(before, after *model.Config) []string {
	afterNames := make(map[string]struct{})
	if after != nil {
		for _, entry := range after.ModelEntries {
			if entry.Model != "" {
				afterNames[entry.Model] = struct{}{}
			}
		}
	}
	removed := make([]string, 0)
	seen := make(map[string]struct{})
	if before != nil {
		for _, entry := range before.ModelEntries {
			if entry.Disabled || entry.Model == "" {
				continue
			}
			if _, duplicate := seen[entry.Model]; duplicate {
				continue
			}
			seen[entry.Model] = struct{}{}
			if _, retained := afterNames[entry.Model]; retained {
				continue
			}
			removed = append(removed, entry.Model)
		}
	}
	return removed
}

func configuredModelNameSet(channels []*model.Config) map[string]struct{} {
	configured := make(map[string]struct{})
	for _, cfg := range channels {
		if cfg == nil {
			continue
		}
		for _, entry := range cfg.ModelEntries {
			if entry.Model != "" {
				configured[entry.Model] = struct{}{}
			}
			if entry.RedirectModel != "" {
				configured[entry.RedirectModel] = struct{}{}
			}
		}
		if cfg.ScheduledCheckModel != "" {
			configured[cfg.ScheduledCheckModel] = struct{}{}
		}
	}
	return configured
}

func exactModelNameSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name != "" {
			set[name] = struct{}{}
		}
	}
	return set
}

func activeTokenModelNameSet(tokens []*model.AuthToken) map[string]struct{} {
	referenced := make(map[string]struct{})
	for _, token := range tokens {
		if token == nil {
			continue
		}
		for _, name := range token.AllowedModels {
			if key := modelAliasKey(name); key != "" {
				referenced[key] = struct{}{}
			}
		}
	}
	return referenced
}
