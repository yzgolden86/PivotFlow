package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"

	"github.com/gin-gonic/gin"
)

const (
	siteSyncMessageManualOwnership          = "manual_ownership"
	siteSyncMessageDiscoveryIncomplete      = "discovery_incomplete"
	siteSyncMessageProjectionCreate         = "projection_create"
	siteSyncMessageProjectionRecreate       = "projection_recreate"
	siteSyncMessageProjectionUpdate         = "projection_update"
	siteSyncMessageProjectionExternalChange = "projection_external_change"
	siteSyncMessageUpstreamUnchanged        = "upstream_unchanged"
	siteSyncMessageRemovedManualPreserved   = "removed_manual_preserved"
	siteSyncMessageRemovedAlreadyInactive   = "removed_already_inactive"
	siteSyncMessageRemovedDeactivateHistory = "removed_deactivate_history"
)

type siteSyncProjectionOperation struct {
	itemKey       string
	account       *model.SiteAccount
	site          *model.Site
	channelID     int64
	projectionKey string
	name          string
	protocols     []string
	models        []string
	apiKey        string
	pricingGroup  string
	force         bool
	preserved     *siteSyncPreservedChannel
	previousKeys  []*model.APIKey
}

type siteSyncPreservedChannel struct {
	enabled               bool
	priority              int
	protocolTransformMode string
	customRequestRules    *model.CustomRequestRules
	cooldownRules         *model.CooldownDetectionRules
}

type siteSyncProjectionPlan struct {
	accountID            int64
	account              *model.SiteAccount
	site                 *model.Site
	fingerprint          string
	activeProjectionKeys []string
	deactivateRemoved    bool
	deactivateAction     bool
	items                []configPreviewItem
	operations           []siteSyncProjectionOperation
}

var errSiteSyncCredentialsRefreshed = errors.New("site sync credentials refreshed")

func (s *Server) HandleSiteSyncPreview(c *gin.Context) {
	accountID, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid_request")
		return
	}
	account, err := s.siteControl.store.GetSiteAccount(c.Request.Context(), accountID)
	if err != nil {
		RespondErrorMsg(c, http.StatusNotFound, "not_found")
		return
	}
	site, err := s.siteControl.store.GetSite(c.Request.Context(), account.SiteID)
	if err != nil {
		RespondErrorMsg(c, http.StatusNotFound, "not_found")
		return
	}
	if siteSyncConfigurationStructurallyUnavailable(account, site) {
		RespondErrorMsg(c, http.StatusConflict, "preview_stale")
		return
	}
	run, merged, err := s.submitSiteSyncPreviewTask(c.Request.Context(), account)
	if err != nil {
		respondSiteTaskSubmissionError(c, err)
		return
	}
	RespondJSON(c, http.StatusAccepted, gin.H{"task_id": run.id, "merged": merged})
}

func (s *Server) submitSiteSyncPreviewTask(ctx context.Context, account *model.SiteAccount) (*siteTaskRun, bool, error) {
	spec := siteTaskSpec{
		key:           fmt.Sprintf("account:%d:sync_preview", account.ID),
		kind:          "sync_preview",
		siteID:        account.SiteID,
		accountID:     account.ID,
		total:         1,
		leaseKey:      fmt.Sprintf("site:%d:account:%d:refresh", account.SiteID, account.ID),
		leaseDuration: siteTaskLeaseDuration,
	}
	return s.siteControl.submitSiteTask(ctx, spec, func(taskCtx context.Context, task *model.SiteTask) {
		runCtx, finish, err := s.siteControl.acquireManualSiteTask(taskCtx, account.SiteID)
		if err != nil {
			s.siteControl.updateTask(taskCtx, task, model.SiteTaskStatusFailed, "", siteSyncTaskErrorCode(err))
			return
		}
		defer finish()
		s.siteControl.updateTask(runCtx, task, model.SiteTaskStatusRunning, "", "")
		if runCtx.Err() != nil {
			return
		}
		previewID, err := s.discoverSiteSyncPreview(runCtx, account.ID)
		if err != nil {
			s.siteControl.updateTask(runCtx, task, model.SiteTaskStatusFailed, "", siteSyncTaskErrorCode(err))
			return
		}
		s.siteControl.updateTask(runCtx, task, model.SiteTaskStatusSuccess, "config_preview:"+previewID, "")
	})
}

func (s *Server) discoverSiteSyncPreview(ctx context.Context, accountID int64) (string, error) {
	for attempt := 0; attempt < 2; attempt++ {
		previewID, err := s.discoverSiteSyncPreviewAttempt(ctx, accountID)
		if errors.Is(err, errSiteSyncCredentialsRefreshed) {
			continue
		}
		return previewID, err
	}
	return "", previewFailure("", "preview_stale")
}

func (s *Server) discoverSiteSyncPreviewAttempt(ctx context.Context, accountID int64) (string, error) {
	initial, err := captureSiteSyncConfiguration(ctx, s.store, accountID)
	if err != nil {
		return "", previewFailure("", safeSiteSyncErrorCode(err))
	}
	if siteSyncConfigurationStructurallyUnavailable(initial.account, initial.site) {
		return "", previewFailure("", "preview_stale")
	}
	account := initial.account
	site := initial.site
	adapter, err := s.siteControl.adapter(site)
	if err != nil {
		return "", previewFailure("", safeSiteSyncErrorCode(err))
	}
	creds, err := s.siteControl.operationCredentials(ctx, account, site, adapter)
	if err != nil {
		return "", previewFailure("", safeSiteSyncErrorCode(err))
	}
	keys, err := s.siteControl.routingSnapshots(ctx, account, site, adapter, creds)
	if err != nil {
		return "", previewFailure("", safeSiteSyncErrorCode(err))
	}
	modelsByProjection, _, modelErr := s.siteControl.routingModels(ctx, account, site, adapter, creds, keys)
	if modelErr != nil && len(modelsByProjection) == 0 {
		return "", previewFailure("", safeSiteSyncErrorCode(modelErr))
	}
	registered, err := captureSiteSyncConfiguration(ctx, s.store, account.ID)
	if err != nil {
		return "", previewFailure("", safeSiteSyncErrorCode(err))
	}
	if siteSyncConfigurationStructurallyUnavailable(registered.account, registered.site) {
		return "", previewFailure("", "preview_stale")
	}
	if initial.configurationFingerprint != registered.configurationFingerprint {
		return "", previewFailure("", "preview_stale")
	}
	if initial.fingerprint != registered.fingerprint {
		return "", errSiteSyncCredentialsRefreshed
	}
	bindings := registered.bindings
	bindingsByProjection := make(map[string]*model.SiteChannelBinding, len(bindings))
	for _, binding := range bindings {
		if binding != nil {
			bindingsByProjection[binding.ProjectionKey] = binding
		}
	}

	plan := siteSyncProjectionPlan{
		accountID:            account.ID,
		account:              account,
		site:                 site,
		fingerprint:          registered.fingerprint,
		activeProjectionKeys: make([]string, 0, len(keys)),
		deactivateRemoved:    modelErr == nil,
	}
	activeKeys := make(map[string]struct{}, len(keys))
	for index, item := range keys {
		projectionKey := stableProjectionKey(item, index)
		plan.activeProjectionKeys = append(plan.activeProjectionKeys, projectionKey)
		activeKeys[projectionKey] = struct{}{}
		binding := bindingsByProjection[projectionKey]
		if binding != nil && binding.Ownership == "manual" {
			plan.items = append(plan.items, configPreviewItem{
				Key:             fmt.Sprintf("sync[%d]", len(plan.items)+1),
				Entity:          "channel",
				Label:           siteProjectionChannelName(site, account, item),
				Action:          "preserve",
				PreservedFields: []string{"ownership"},
				Message:         siteSyncMessageManualOwnership,
			})
			continue
		}
		models, resolved := modelsByProjection[projectionKey]
		if !resolved || len(models) == 0 {
			label := siteProjectionChannelName(site, account, item)
			if binding != nil && binding.ChannelID > 0 {
				if channel := registered.channels[binding.ChannelID]; channel != nil && strings.TrimSpace(channel.Name) != "" {
					label = channel.Name
				}
			}
			plan.items = append(plan.items, configPreviewItem{
				Key:             fmt.Sprintf("sync[%d]", len(plan.items)+1),
				Entity:          "channel",
				Label:           label,
				Action:          "preserve",
				PreservedFields: []string{"upstream"},
				Message:         siteSyncMessageDiscoveryIncomplete,
			})
			continue
		}
		itemKey := fmt.Sprintf("sync[%d]", len(plan.items)+1)
		name := siteProjectionChannelName(site, account, item)
		operation := siteSyncProjectionOperation{
			itemKey:       itemKey,
			account:       account,
			site:          site,
			projectionKey: projectionKey,
			name:          name,
			protocols:     append([]string(nil), item.Protocols...),
			models:        append([]string(nil), models...),
			apiKey:        item.Key,
			pricingGroup:  strings.TrimSpace(item.Group),
		}
		action := "create"
		fields := []string{"url", "models", "api_key"}
		preservedFields := []string{}
		message := siteSyncMessageProjectionCreate
		if binding != nil && binding.ChannelID > 0 {
			channel := registered.channels[binding.ChannelID]
			if channel == nil {
				action = "create"
				message = siteSyncMessageProjectionRecreate
			} else {
				operation.channelID = channel.ID
				operation.preserved = preserveSiteSyncChannel(channel)
				operation.previousKeys = append([]*model.APIKey(nil), registered.apiKeys[channel.ID]...)
				foreignChange := !siteSyncBindingMatchesChannel(binding, channel, registered.apiKeys[channel.ID])
				remoteEqual := siteSyncChannelMatchesUpstream(channel, registered.apiKeys[channel.ID], site, item.Protocols, models, item.Key, name, item.Group, binding.PricingGroup)
				if foreignChange {
					action = "conflict"
					fields = siteSyncChangedFields(channel, registered.apiKeys[channel.ID], site, item.Protocols, models, item.Key, name, item.Group, binding.PricingGroup)
					preservedFields = []string{}
					message = siteSyncMessageProjectionExternalChange
				} else if remoteEqual {
					action = "unchanged"
					fields = []string{}
					message = siteSyncMessageUpstreamUnchanged
				} else {
					action = "update"
					operation.name = name
					operation.force = true
					fields = siteSyncChangedFields(channel, registered.apiKeys[channel.ID], site, item.Protocols, models, item.Key, name, item.Group, binding.PricingGroup)
					preservedFields = siteSyncPreservedFieldNames()
					message = siteSyncMessageProjectionUpdate
				}
			}
		}
		plan.items = append(plan.items, configPreviewItem{
			Key:             itemKey,
			Entity:          "channel",
			Label:           name,
			Action:          action,
			Fields:          fields,
			PreservedFields: preservedFields,
			Message:         message,
		})
		if action == "create" || action == "update" {
			plan.operations = append(plan.operations, operation)
		}
	}
	for _, binding := range bindings {
		if binding == nil || binding.ChannelID <= 0 {
			continue
		}
		if _, active := activeKeys[binding.ProjectionKey]; active {
			continue
		}
		channel := registered.channels[binding.ChannelID]
		label := "站点同步渠道"
		if channel != nil && strings.TrimSpace(channel.Name) != "" {
			label = channel.Name
		}
		itemKey := fmt.Sprintf("sync[%d]", len(plan.items)+1)
		if binding.Ownership == "manual" {
			plan.items = append(plan.items, configPreviewItem{
				Key:             itemKey,
				Entity:          "channel",
				Label:           label,
				Action:          "preserve",
				PreservedFields: []string{"ownership"},
				Message:         siteSyncMessageRemovedManualPreserved,
			})
			continue
		}
		if !plan.deactivateRemoved {
			plan.items = append(plan.items, configPreviewItem{
				Key:     itemKey,
				Entity:  "channel",
				Label:   label,
				Action:  "preserve",
				Message: siteSyncMessageDiscoveryIncomplete,
			})
			continue
		}
		if channel == nil || !channel.Enabled && binding.Status == "inactive" {
			plan.items = append(plan.items, configPreviewItem{
				Key:     itemKey,
				Entity:  "channel",
				Label:   label,
				Action:  "unchanged",
				Message: siteSyncMessageRemovedAlreadyInactive,
			})
			continue
		}
		plan.items = append(plan.items, configPreviewItem{
			Key:     itemKey,
			Entity:  "channel",
			Label:   label,
			Action:  "deactivate",
			Fields:  []string{"enabled"},
			Message: siteSyncMessageRemovedDeactivateHistory,
		})
		plan.deactivateAction = true
	}
	if len(plan.items) == 0 && modelErr != nil {
		return "", previewFailure("", safeSiteSyncErrorCode(modelErr))
	}
	for index := range plan.items {
		plan.items[index].Label = importLabel(plan.items[index].Label)
	}

	preview, err := s.newConfigPreview("site_sync", siteSyncPreviewPayloadSize(plan), plan.items, func(applyCtx context.Context) (*configPreviewResult, error) {
		return s.applySiteSyncProjectionPlan(applyCtx, plan)
	})
	if err != nil {
		return "", err
	}
	return preview.ID, nil
}

func (s *Server) applySiteSyncProjectionPlan(ctx context.Context, plan siteSyncProjectionPlan) (*configPreviewResult, error) {
	err := storage.WithConfigurationTransaction(ctx, s.store, func(txCtx context.Context, txStore storage.Store) error {
		current, err := captureSiteSyncConfiguration(txCtx, txStore, plan.accountID)
		if err != nil {
			return previewFailure("", safeSiteSyncErrorCode(err))
		}
		if siteSyncConfigurationUnavailable(current.account, current.site) {
			return previewFailure("", "preview_stale")
		}
		if current.fingerprint != plan.fingerprint {
			return previewFailure("", "preview_stale")
		}
		for _, operation := range plan.operations {
			result, err := txStore.UpsertSiteProjection(txCtx, model.SiteProjectionInput{
				SiteAccountID: operation.account.ID,
				ProjectionKey: operation.projectionKey,
				Name:          operation.name,
				BaseURL:       routingBaseURL(operation.site.BaseURL),
				Protocols:     projectedRoutingProtocols(operation.site, operation.protocols),
				Models:        operation.models,
				APIKey:        operation.apiKey,
				PricingGroup:  operation.pricingGroup,
				Enabled:       operation.account.Enabled,
				Force:         operation.force,
			})
			if err != nil {
				return previewFailure(operation.itemKey, safeSiteSyncErrorCode(err))
			}
			if result != nil && result.Action == "conflict" {
				return previewFailure(operation.itemKey, "preview_stale")
			}
			if operation.channelID > 0 && operation.preserved != nil {
				if err := restoreSiteSyncChannel(txCtx, txStore, operation, current.apiKeys[operation.channelID]); err != nil {
					return previewFailure(operation.itemKey, safeSiteSyncErrorCode(err))
				}
			}
		}
		if plan.deactivateAction {
			if err := txStore.DeactivateSiteProjectionsExcept(txCtx, plan.accountID, plan.activeProjectionKeys); err != nil {
				return previewFailure("", safeSiteSyncErrorCode(err))
			}
		}
		return nil
	})
	if err != nil {
		if storage.IsConfigurationCacheDegraded(err) {
			s.siteControl.projectionChanged()
			return &configPreviewResult{Items: plan.items, Warning: "cache_sync_degraded"}, nil
		}
		return nil, err
	}
	s.siteControl.projectionChanged()
	return &configPreviewResult{Items: plan.items}, nil
}

func siteProjectionChannelName(site *model.Site, account *model.SiteAccount, item provider.RoutingKeySnapshot) string {
	name := fmt.Sprintf("%s / %s", site.Name, account.Label)
	if group := strings.TrimSpace(item.Group); group != "" {
		name += " / " + group
	}
	itemName := strings.TrimSpace(item.Name)
	if itemName != "" && itemName != strings.TrimSpace(item.Group) && !strings.EqualFold(itemName, strings.TrimSpace(account.Label)) {
		name += " / " + itemName
	}
	return name
}

func safeSiteSyncErrorCode(err error) string {
	if err == nil {
		return provider.CodeRequestFailed
	}
	if errors.Is(err, credential.ErrCredentialLocked) {
		return "credential_locked"
	}
	var safe *configPreviewError
	if errors.As(err, &safe) {
		return safe.code
	}
	code := provider.ErrorCode(err)
	switch code {
	case provider.CodeUnsupported, provider.CodeBrowserRequired, provider.CodeExpired, provider.CodeUserIDRequired,
		provider.CodeTimeout, provider.CodeRateLimited, provider.CodeInvalidResponse, provider.CodeRequestFailed,
		provider.CodeRoutingKeyUnavailable:
		return code
	default:
		return provider.CodeRequestFailed
	}
}

func siteSyncTaskErrorCode(err error) string {
	code := safeSiteSyncErrorCode(err)
	switch code {
	case "preview_stale":
		return "conflict"
	case "credential_locked", "not_found", "invalid_request":
		return code
	default:
		return safeSiteTaskError(code)
	}
}

func siteSyncChannelsForBindings(ctx context.Context, store storage.Store, bindings []*model.SiteChannelBinding) (map[int64]*model.Config, error) {
	source := storage.ConfigurationSource(store)
	channels := make(map[int64]*model.Config, len(bindings))
	for _, binding := range bindings {
		if binding == nil || binding.ChannelID <= 0 {
			continue
		}
		channel, err := source.GetConfig(ctx, binding.ChannelID)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				continue
			}
			return nil, err
		}
		if channel == nil || channel.ID <= 0 {
			return nil, errors.New("configuration channel returned an empty snapshot")
		}
		channels[channel.ID] = channel
	}
	return channels, nil
}

func siteSyncAPIKeysForChannels(ctx context.Context, store storage.Store, channels map[int64]*model.Config) (map[int64][]*model.APIKey, error) {
	source := storage.ConfigurationSource(store)
	keys := make(map[int64][]*model.APIKey, len(channels))
	for channelID := range channels {
		items, err := source.GetAPIKeys(ctx, channelID)
		if err != nil {
			return nil, err
		}
		keys[channelID] = items
	}
	return keys, nil
}

func preserveSiteSyncChannel(channel *model.Config) *siteSyncPreservedChannel {
	return &siteSyncPreservedChannel{
		enabled:               channel.Enabled,
		priority:              channel.Priority,
		protocolTransformMode: channel.ProtocolTransformMode,
		customRequestRules:    channel.CustomRequestRules.Clone(),
		cooldownRules:         channel.CooldownDetectionRules.Clone(),
	}
}

func restoreSiteSyncChannel(ctx context.Context, store storage.Store, operation siteSyncProjectionOperation, currentKeys []*model.APIKey) error {
	channel, err := store.GetConfig(ctx, operation.channelID)
	if err != nil {
		return err
	}
	if channel == nil {
		return errors.New("configuration channel returned an empty snapshot")
	}
	channel.Enabled = operation.preserved.enabled
	channel.Priority = operation.preserved.priority
	channel.ProtocolTransformMode = operation.preserved.protocolTransformMode
	channel.CustomRequestRules = operation.preserved.customRequestRules.Clone()
	channel.CooldownDetectionRules = operation.preserved.cooldownRules.Clone()
	if _, err := store.UpdateConfig(ctx, channel.ID, channel); err != nil {
		return err
	}
	if len(currentKeys) == 1 {
		previous := currentKeys[0]
		if previous == nil {
			return errors.New("configuration key returned an empty snapshot")
		}
		preservedKey := *previous
		preservedKey.ID = 0
		preservedKey.ChannelID, preservedKey.KeyIndex = channel.ID, 0
		preservedKey.APIKey = strings.TrimSpace(operation.apiKey)
		if strings.TrimSpace(previous.APIKey) != preservedKey.APIKey {
			preservedKey.Health = model.APIKeyHealth{}
			preservedKey.CooldownUntil = 0
			preservedKey.CooldownDurationMs = 0
		}
		if err := store.DeleteAllAPIKeys(ctx, channel.ID); err != nil {
			return err
		}
		if err := store.CreateAPIKeysBatch(ctx, []*model.APIKey{&preservedKey}); err != nil {
			return err
		}
	}
	binding, err := store.GetSiteChannelBinding(ctx, operation.account.ID, operation.projectionKey)
	if err != nil {
		return err
	}
	if binding == nil {
		return errors.New("site projection binding returned an empty snapshot")
	}
	if binding.Ownership == "manual" {
		return store.SetSiteProjectionOwnership(ctx, channel.ID, "projected")
	}
	return nil
}

func siteSyncPreservedFieldNames() []string {
	return []string{"enabled", "priority", "protocol_transform_mode", "custom_request_rules", "cooldown_detection_rules", "key_metadata"}
}

func siteSyncConfigurationUnavailable(account *model.SiteAccount, site *model.Site) bool {
	return !siteSyncAccountRoutingEligible(account) || site == nil || !site.Enabled || site.DeletedAt != 0
}

func siteSyncConfigurationStructurallyUnavailable(account *model.SiteAccount, site *model.Site) bool {
	return account == nil || site == nil || !account.Enabled || account.DeletedAt != 0 || !site.Enabled || site.DeletedAt != 0
}

func siteSyncAccountRoutingEligible(account *model.SiteAccount) bool {
	return account != nil &&
		account.Enabled &&
		account.DeletedAt == 0 &&
		account.Status != model.SiteAccountStatusExpired &&
		account.Status != model.SiteAccountStatusDisabled
}

type siteSyncPreviewAccountPayload struct {
	*model.SiteAccount
	CredentialCiphertext string `json:"credential_ciphertext"`
	CredentialKeyVersion string `json:"credential_key_version"`
}

type siteSyncPreviewPreservedPayload struct {
	Enabled               bool
	Priority              int
	ProtocolTransformMode string
	CustomRequestRules    *model.CustomRequestRules
	CooldownRules         *model.CooldownDetectionRules
}

type siteSyncPreviewOperationPayload struct {
	ItemKey       string
	ChannelID     int64
	ProjectionKey string
	Name          string
	Protocols     []string
	Models        []string
	APIKey        string
	PricingGroup  string
	Force         bool
	Preserved     *siteSyncPreviewPreservedPayload
	PreviousKeys  []*model.APIKey
}

func siteSyncPreviewPayloadSize(plan siteSyncProjectionPlan) int {
	account := plan.account
	site := plan.site
	if account == nil || site == nil {
		for _, operation := range plan.operations {
			if account == nil {
				account = operation.account
			}
			if site == nil {
				site = operation.site
			}
		}
	}
	payload := struct {
		AccountID            int64
		Fingerprint          string
		ActiveProjectionKeys []string
		DeactivateRemoved    bool
		DeactivateAction     bool
		Account              *siteSyncPreviewAccountPayload
		Site                 *model.Site
		Operations           []siteSyncPreviewOperationPayload
	}{
		AccountID:            plan.accountID,
		Fingerprint:          plan.fingerprint,
		ActiveProjectionKeys: plan.activeProjectionKeys,
		DeactivateRemoved:    plan.deactivateRemoved,
		DeactivateAction:     plan.deactivateAction,
		Site:                 site,
	}
	if account != nil {
		payload.Account = &siteSyncPreviewAccountPayload{
			SiteAccount:          account,
			CredentialCiphertext: account.CredentialCiphertext,
			CredentialKeyVersion: account.CredentialKeyVersion,
		}
	}
	for _, operation := range plan.operations {
		item := siteSyncPreviewOperationPayload{
			ItemKey:       operation.itemKey,
			ChannelID:     operation.channelID,
			ProjectionKey: operation.projectionKey,
			Name:          operation.name,
			Protocols:     operation.protocols,
			Models:        operation.models,
			APIKey:        operation.apiKey,
			PricingGroup:  operation.pricingGroup,
			Force:         operation.force,
			PreviousKeys:  operation.previousKeys,
		}
		if operation.preserved != nil {
			item.Preserved = &siteSyncPreviewPreservedPayload{
				Enabled:               operation.preserved.enabled,
				Priority:              operation.preserved.priority,
				ProtocolTransformMode: operation.preserved.protocolTransformMode,
				CustomRequestRules:    operation.preserved.customRequestRules,
				CooldownRules:         operation.preserved.cooldownRules,
			}
		}
		payload.Operations = append(payload.Operations, item)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return -1
	}
	return len(raw)
}

func siteSyncChangedFields(channel *model.Config, keys []*model.APIKey, site *model.Site, protocols, models []string, apiKey, name, pricingGroup, currentPricingGroup string) []string {
	if channel == nil {
		return []string{"name", "url", "models", "api_key"}
	}
	fields := make([]string, 0, 5)
	if strings.TrimSpace(channel.Name) != strings.TrimSpace(name) {
		fields = append(fields, "name")
	}
	if len(channel.URLs) != 1 || channel.URLs[0].URL != routingBaseURL(site.BaseURL) ||
		!siteSyncStringsEqual(channel.URLs[0].Protocols, projectedRoutingProtocols(site, protocols), true) {
		fields = append(fields, "url")
	}
	if !siteSyncStringsEqual(channel.GetModels(), models, false) {
		fields = append(fields, "models")
	}
	if len(keys) != 1 || keys[0] == nil || keys[0].APIKey != strings.TrimSpace(apiKey) {
		fields = append(fields, "api_key")
	}
	if strings.TrimSpace(currentPricingGroup) != strings.TrimSpace(pricingGroup) {
		fields = append(fields, "pricing_group")
	}
	return fields
}

func siteSyncBindingMatchesChannel(binding *model.SiteChannelBinding, channel *model.Config, keys []*model.APIKey) bool {
	if binding == nil || channel == nil || len(channel.URLs) != 1 || len(keys) != 1 || keys[0] == nil {
		return false
	}
	models := channel.GetModels()
	for _, enabled := range []bool{true, false} {
		hash := model.SiteProjectionSourceHash(channel.URLs[0].URL, channel.URLs[0].Protocols, models, keys[0].APIKey, enabled)
		if binding.LastProjectedHash == hash {
			return true
		}
	}
	return false
}

func siteSyncChannelMatchesUpstream(channel *model.Config, keys []*model.APIKey, site *model.Site, protocols, models []string, apiKey, name, pricingGroup, currentPricingGroup string) bool {
	if channel == nil || site == nil || len(channel.URLs) != 1 || len(keys) != 1 || keys[0] == nil {
		return false
	}
	return strings.TrimSpace(channel.Name) == strings.TrimSpace(name) &&
		channel.URLs[0].URL == routingBaseURL(site.BaseURL) &&
		siteSyncStringsEqual(channel.URLs[0].Protocols, projectedRoutingProtocols(site, protocols), true) &&
		siteSyncStringsEqual(channel.GetModels(), models, false) &&
		keys[0].APIKey == strings.TrimSpace(apiKey) &&
		strings.TrimSpace(currentPricingGroup) == strings.TrimSpace(pricingGroup)
}

func siteSyncStringsEqual(left, right []string, fold bool) bool {
	if len(left) != len(right) {
		return false
	}
	normalize := func(values []string) []string {
		out := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if fold {
				value = strings.ToLower(value)
			}
			out = append(out, value)
		}
		sort.Strings(out)
		return out
	}
	left, right = normalize(left), normalize(right)
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func siteSyncBindingsForAccount(ctx context.Context, store storage.Store, accountID int64) ([]*model.SiteChannelBinding, error) {
	bindings, err := storage.ConfigurationSource(store).ListSiteChannelBindings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*model.SiteChannelBinding, 0, len(bindings))
	for _, binding := range bindings {
		if binding != nil && binding.SiteAccountID == accountID {
			out = append(out, binding)
		}
	}
	return out, nil
}

type siteSyncConfigurationSnapshot struct {
	account                  *model.SiteAccount
	site                     *model.Site
	bindings                 []*model.SiteChannelBinding
	channels                 map[int64]*model.Config
	apiKeys                  map[int64][]*model.APIKey
	fingerprint              string
	configurationFingerprint string
}

func captureSiteSyncConfiguration(ctx context.Context, store storage.Store, accountID int64) (*siteSyncConfigurationSnapshot, error) {
	source := storage.ConfigurationSource(store)
	account, err := source.GetSiteAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, errors.New("site account returned an empty snapshot")
	}
	site, err := source.GetSite(ctx, account.SiteID)
	if err != nil {
		return nil, err
	}
	if site == nil {
		return nil, errors.New("site returned an empty snapshot")
	}
	bindings, err := siteSyncBindingsForAccount(ctx, source, accountID)
	if err != nil {
		return nil, err
	}
	channels, err := siteSyncChannelsForBindings(ctx, source, bindings)
	if err != nil {
		return nil, err
	}
	apiKeys, err := siteSyncAPIKeysForChannels(ctx, source, channels)
	if err != nil {
		return nil, err
	}
	fingerprint, configurationFingerprint, err := siteSyncConfigurationFingerprint(account, site, bindings, channels, apiKeys)
	if err != nil {
		return nil, err
	}
	return &siteSyncConfigurationSnapshot{
		account:                  account,
		site:                     site,
		bindings:                 bindings,
		channels:                 channels,
		apiKeys:                  apiKeys,
		fingerprint:              fingerprint,
		configurationFingerprint: configurationFingerprint,
	}, nil
}

func siteSyncConfigurationFingerprint(
	account *model.SiteAccount,
	site *model.Site,
	bindings []*model.SiteChannelBinding,
	channels map[int64]*model.Config,
	apiKeys map[int64][]*model.APIKey,
) (string, string, error) {
	type bindingFingerprint struct {
		ID                int64
		SiteAccountID     int64
		ProjectionKey     string
		ChannelID         int64
		PricingGroup      string
		Ownership         string
		Status            string
		LastProjectedHash string
	}
	type channelFingerprint struct {
		ID     int64
		Fields map[string]any
	}
	type apiKeyFingerprint struct {
		ChannelID int64
		Fields    []map[string]any
	}
	accountFields := accountImportFields(account)
	accountFields["id"] = account.ID
	accountFields["deleted_at"] = account.DeletedAt
	accountFields["routing_eligible"] = siteSyncAccountRoutingEligible(account)
	siteFields := siteImportFields(site)
	siteFields["id"] = site.ID
	siteFields["deleted_at"] = site.DeletedAt

	bindingFields := make([]bindingFingerprint, 0, len(bindings))
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		bindingFields = append(bindingFields, bindingFingerprint{
			ID:                binding.ID,
			SiteAccountID:     binding.SiteAccountID,
			ProjectionKey:     binding.ProjectionKey,
			ChannelID:         binding.ChannelID,
			PricingGroup:      binding.PricingGroup,
			Ownership:         binding.Ownership,
			Status:            binding.Status,
			LastProjectedHash: binding.LastProjectedHash,
		})
	}
	sort.Slice(bindingFields, func(left, right int) bool {
		if bindingFields[left].ID != bindingFields[right].ID {
			return bindingFields[left].ID < bindingFields[right].ID
		}
		return bindingFields[left].ProjectionKey < bindingFields[right].ProjectionKey
	})

	channelIDs := make([]int64, 0, len(channels))
	for channelID, channel := range channels {
		if channel != nil {
			channelIDs = append(channelIDs, channelID)
		}
	}
	sort.Slice(channelIDs, func(left, right int) bool { return channelIDs[left] < channelIDs[right] })
	channelFields := make([]channelFingerprint, 0, len(channelIDs))
	keyFields := make([]apiKeyFingerprint, 0, len(channelIDs))
	for _, channelID := range channelIDs {
		channelFields = append(channelFields, channelFingerprint{ID: channelID, Fields: channelImportFields(channels[channelID])})
		keys := keyImportFields(apiKeys[channelID])
		sort.Slice(keys, func(left, right int) bool {
			leftRaw, _ := json.Marshal(keys[left])
			rightRaw, _ := json.Marshal(keys[right])
			return strings.Compare(string(leftRaw), string(rightRaw)) < 0
		})
		keyFields = append(keyFields, apiKeyFingerprint{ChannelID: channelID, Fields: keys})
	}

	configuration := struct {
		Account  map[string]any
		Site     map[string]any
		Bindings []bindingFingerprint
		Channels []channelFingerprint
		APIKeys  []apiKeyFingerprint
	}{
		Account:  accountFields,
		Site:     siteFields,
		Bindings: bindingFields,
		Channels: channelFields,
		APIKeys:  keyFields,
	}
	rawConfiguration, err := json.Marshal(configuration)
	if err != nil {
		return "", "", err
	}
	rawFull, err := json.Marshal(struct {
		Configuration        json.RawMessage
		CredentialCiphertext string
		CredentialKeyVersion string
	}{
		Configuration:        rawConfiguration,
		CredentialCiphertext: account.CredentialCiphertext,
		CredentialKeyVersion: account.CredentialKeyVersion,
	})
	if err != nil {
		return "", "", err
	}
	configurationSum := sha256.Sum256(rawConfiguration)
	fullSum := sha256.Sum256(rawFull)
	return hex.EncodeToString(fullSum[:]), hex.EncodeToString(configurationSum[:]), nil
}
