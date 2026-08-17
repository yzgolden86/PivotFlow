package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"

	"github.com/gin-gonic/gin"
)

const (
	backupSchemaVersion       = "1.0"
	backupMaxImportBytes      = 32 << 20
	backupDefaultIntervalHour = 24
)

type backupSetting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type backupAccount struct {
	Account     *model.SiteAccount   `json:"account"`
	Credentials provider.Credentials `json:"credentials"`
}

type backupToken struct {
	PlainToken             string   `json:"token,omitempty"`
	Description            string   `json:"description"`
	ExpiresAt              *int64   `json:"expires_at,omitempty"`
	IsActive               bool     `json:"is_active"`
	AllowedModels          []string `json:"allowed_models,omitempty"`
	AllowedChannelIDs      []int64  `json:"allowed_channel_ids,omitempty"`
	ChannelRestrictionMode string   `json:"channel_restriction_mode,omitempty"`
	CostLimitUSD           float64  `json:"cost_limit_usd"`
	MaxConcurrency         int      `json:"max_concurrency"`
}

type backupChannel struct {
	Config          *model.Config  `json:"config"`
	APIKeys         []model.APIKey `json:"api_keys"`
	OAuthCredential string         `json:"oauth_credential,omitempty"`
}

type backupConnections struct {
	Sites    []*model.Site            `json:"sites"`
	Accounts []backupAccount          `json:"accounts"`
	Models   []model.SiteAccountModel `json:"models"`
	Channels []backupChannel          `json:"channels"`
	Tokens   []backupToken            `json:"tokens"`
}

type backupNotification struct {
	WebhookEnabled         bool    `json:"webhook_enabled"`
	WebhookURL             string  `json:"webhook_url,omitempty"`
	TelegramEnabled        bool    `json:"telegram_enabled"`
	TelegramBotToken       string  `json:"telegram_bot_token,omitempty"`
	TelegramChatID         string  `json:"telegram_chat_id,omitempty"`
	TelegramUseSystemProxy bool    `json:"telegram_use_system_proxy"`
	LowBalanceEnabled      bool    `json:"low_balance_enabled"`
	LowBalanceThreshold    float64 `json:"low_balance_threshold"`
	CheckinFailureEnabled  bool    `json:"checkin_failure_enabled"`
	CooldownMinutes        int     `json:"cooldown_minutes"`
}

type backupTarget struct {
	Enabled               bool   `json:"enabled"`
	FileURL               string `json:"file_url"`
	Username              string `json:"username"`
	Password              string `json:"password,omitempty"`
	ExportType            string `json:"export_type"`
	AutoSyncEnabled       bool   `json:"auto_sync_enabled"`
	AutoSyncIntervalHours int    `json:"auto_sync_interval_hours"`
}

type backupDocument struct {
	Version      string              `json:"version"`
	ExportedAt   int64               `json:"exported_at"`
	Type         string              `json:"type"`
	Connections  *backupConnections  `json:"connections,omitempty"`
	Settings     []backupSetting     `json:"settings,omitempty"`
	Notification *backupNotification `json:"notification,omitempty"`
	BackupTarget *backupTarget       `json:"backup_target,omitempty"`
	Warnings     []string            `json:"warnings,omitempty"`
}

type backupImportResult struct {
	Sites    int  `json:"sites"`
	Accounts int  `json:"accounts"`
	Channels int  `json:"channels"`
	Tokens   int  `json:"tokens"`
	Settings int  `json:"settings"`
	Restart  bool `json:"restart_required"`
}

type backupPasswordSecret struct {
	Value string `json:"value"`
}

type backupConfigView struct {
	*model.BackupConfig
	PasswordMasked string `json:"password_masked,omitempty"`
}

func normalizeBackupType(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "all":
		return "all", nil
	case "connections":
		return "connections", nil
	case "settings":
		return "settings", nil
	default:
		return "", errors.New("invalid_backup_type")
	}
}

func (s *Server) buildBackupDocument(ctx context.Context, requestedType string) (*backupDocument, error) {
	backupType, err := normalizeBackupType(requestedType)
	if err != nil {
		return nil, err
	}
	document := &backupDocument{Version: backupSchemaVersion, ExportedAt: time.Now().UnixMilli(), Type: backupType}
	if backupType == "all" || backupType == "connections" {
		connections, warnings, buildErr := s.buildBackupConnections(ctx)
		if buildErr != nil {
			return nil, buildErr
		}
		document.Connections = connections
		document.Warnings = append(document.Warnings, warnings...)
	}
	if backupType == "all" || backupType == "settings" {
		settings, listErr := s.store.ListAllSettings(ctx)
		if listErr != nil {
			return nil, listErr
		}
		for _, setting := range settings {
			if setting != nil {
				document.Settings = append(document.Settings, backupSetting{Key: setting.Key, Value: setting.Value})
			}
		}
		if notification, notificationErr := s.exportNotification(ctx); notificationErr == nil {
			document.Notification = notification
		} else {
			document.Warnings = append(document.Warnings, "通知凭证未能导出："+notificationErr.Error())
		}
		if target, targetErr := s.exportBackupTarget(ctx); targetErr == nil {
			document.BackupTarget = target
		} else if !strings.Contains(strings.ToLower(targetErr.Error()), "not found") {
			document.Warnings = append(document.Warnings, "WebDAV 配置未能导出："+targetErr.Error())
		}
	}
	return document, nil
}

func (s *Server) buildBackupConnections(ctx context.Context) (*backupConnections, []string, error) {
	if s.siteControl == nil || s.siteControl.cipher == nil {
		return nil, nil, errors.New("credential_locked")
	}
	sites, err := s.store.ListSites(ctx, model.SiteListFilter{})
	if err != nil {
		return nil, nil, err
	}
	accounts, err := s.store.ListSiteAccounts(ctx, 0, false)
	if err != nil {
		return nil, nil, err
	}
	result := &backupConnections{Sites: sites, Accounts: make([]backupAccount, 0, len(accounts))}
	for _, account := range accounts {
		if account == nil {
			continue
		}
		credentials, credentialErr := s.siteControl.credentials(account)
		if credentialErr != nil {
			return nil, nil, fmt.Errorf("decrypt account %d: %w", account.ID, credentialErr)
		}
		result.Accounts = append(result.Accounts, backupAccount{Account: account, Credentials: credentials})
		models, modelErr := s.store.ListSiteAccountModels(ctx, model.SiteModelFilter{SiteAccountID: account.ID, IncludeDisabled: true, Limit: 100000})
		if modelErr != nil {
			return nil, nil, modelErr
		}
		result.Models = append(result.Models, models...)
	}
	configs, err := s.store.ListConfigs(ctx)
	if err != nil {
		return nil, nil, err
	}
	allKeys, err := s.store.GetAllAPIKeys(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, config := range configs {
		if config == nil {
			continue
		}
		channel := backupChannel{Config: config, OAuthCredential: config.OAuthCredential}
		for _, key := range allKeys[config.ID] {
			if key != nil {
				channel.APIKeys = append(channel.APIKeys, *key)
			}
		}
		result.Channels = append(result.Channels, channel)
	}
	var warnings []string
	tokens, err := s.store.ListAuthTokens(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, token := range tokens {
		if token == nil {
			continue
		}
		plain := ""
		if strings.TrimSpace(token.TokenCiphertext) != "" {
			_ = s.siteControl.cipher.Open(token.TokenCiphertext, &plain)
		}
		if plain == "" {
			warnings = append(warnings, fmt.Sprintf("令牌 #%d 是历史不可恢复令牌，导入时会跳过", token.ID))
		}
		result.Tokens = append(result.Tokens, backupToken{PlainToken: plain, Description: token.Description, ExpiresAt: token.ExpiresAt, IsActive: token.IsActive, AllowedModels: token.AllowedModels, AllowedChannelIDs: token.AllowedChannelIDs, ChannelRestrictionMode: token.ChannelRestrictionMode, CostLimitUSD: token.CostLimitUSD(), MaxConcurrency: token.MaxConcurrency})
	}
	return result, warnings, nil
}

func (s *Server) exportNotification(ctx context.Context) (*backupNotification, error) {
	config, err := s.siteControl.loadWebhookConfig(ctx)
	if err != nil {
		return nil, err
	}
	result := &backupNotification{WebhookEnabled: config.Enabled, TelegramEnabled: config.TelegramEnabled, TelegramUseSystemProxy: config.TelegramUseSystemProxy, LowBalanceEnabled: config.LowBalanceEnabled, LowBalanceThreshold: config.LowBalanceThreshold, CheckinFailureEnabled: config.CheckinFailureEnabled, CooldownMinutes: config.CooldownMinutes}
	if config.URLConfigured {
		result.WebhookURL, err = s.siteControl.webhookEndpoint(config)
		if err != nil {
			return nil, err
		}
	}
	if config.TelegramConfigured {
		result.TelegramBotToken, result.TelegramChatID, err = s.siteControl.telegramCredentials(config)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Server) exportBackupTarget(ctx context.Context) (*backupTarget, error) {
	config, err := s.store.GetBackupConfig(ctx)
	if err != nil {
		return nil, err
	}
	result := &backupTarget{Enabled: config.Enabled, FileURL: config.FileURL, Username: config.Username, ExportType: config.ExportType, AutoSyncEnabled: config.AutoSyncEnabled, AutoSyncIntervalHours: config.AutoSyncIntervalHours}
	if config.PasswordConfigured && s.siteControl != nil && s.siteControl.cipher != nil {
		var secret backupPasswordSecret
		if err := s.siteControl.cipher.Open(config.PasswordCiphertext, &secret); err != nil {
			return nil, err
		}
		result.Password = secret.Value
	}
	return result, nil
}

func (s *Server) HandleBackupExport(c *gin.Context) {
	document, err := s.buildBackupDocument(c.Request.Context(), c.Query("type"))
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}
	RespondJSON(c, http.StatusOK, document)
}

func (s *Server) HandleBackupImport(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, backupMaxImportBytes)
	var request struct {
		Data json.RawMessage `json:"data"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || len(request.Data) == 0 {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid_backup_document")
		return
	}
	var document backupDocument
	if err := json.Unmarshal(request.Data, &document); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid_backup_document")
		return
	}
	result, err := s.importBackupDocument(c.Request.Context(), &document)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}
	RespondJSON(c, http.StatusOK, result)
	if result.Restart {
		go triggerRestart()
	}
}

func (s *Server) importBackupDocument(ctx context.Context, document *backupDocument) (backupImportResult, error) {
	var result backupImportResult
	if document == nil || document.Version != backupSchemaVersion {
		return result, errors.New("unsupported_backup_version")
	}
	backupType, err := normalizeBackupType(document.Type)
	if err != nil {
		return result, err
	}
	if (backupType == "all" || backupType == "connections") && document.Connections != nil {
		connectionResult, importErr := s.importBackupConnections(ctx, document.Connections)
		if importErr != nil {
			return result, importErr
		}
		result.Sites, result.Accounts, result.Channels, result.Tokens = connectionResult.Sites, connectionResult.Accounts, connectionResult.Channels, connectionResult.Tokens
	}
	if backupType == "all" || backupType == "settings" {
		updates := make(map[string]string)
		for _, item := range document.Settings {
			setting := s.configService.GetSetting(item.Key)
			if setting == nil || isContainerManagedUpdateSetting(item.Key) {
				continue
			}
			if err := validateSettingValue(item.Key, setting.ValueType, item.Value); err != nil {
				return result, fmt.Errorf("invalid setting %s: %w", item.Key, err)
			}
			updates[item.Key] = item.Value
		}
		if len(updates) > 0 {
			if err := s.configService.BatchUpdateSettings(ctx, updates); err != nil {
				return result, err
			}
			result.Settings = len(updates)
			result.Restart = true
		}
		if document.Notification != nil {
			if err := s.importNotification(ctx, document.Notification); err != nil {
				return result, err
			}
		}
		if document.BackupTarget != nil {
			if err := s.importBackupTarget(ctx, document.BackupTarget); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

func (s *Server) importBackupConnections(ctx context.Context, input *backupConnections) (backupImportResult, error) {
	var result backupImportResult
	if s.siteControl == nil || s.siteControl.cipher == nil {
		return result, errors.New("credential_locked")
	}
	existingSites, err := s.store.ListSites(ctx, model.SiteListFilter{})
	if err != nil {
		return result, err
	}
	siteIDs := make(map[int64]int64)
	for _, item := range input.Sites {
		if item == nil {
			continue
		}
		var existing *model.Site
		for _, candidate := range existingSites {
			if candidate != nil && (strings.EqualFold(strings.TrimRight(candidate.BaseURL, "/"), strings.TrimRight(item.BaseURL, "/")) || candidate.Name == item.Name) {
				existing = candidate
				break
			}
		}
		oldID := item.ID
		copySite := *item
		copySite.DeletedAt = 0
		if existing != nil {
			copySite.ID = existing.ID
			updated, updateErr := s.store.UpdateSite(ctx, existing.ID, &copySite)
			if updateErr != nil {
				return result, updateErr
			}
			siteIDs[oldID] = updated.ID
		} else {
			copySite.ID, copySite.CreatedAt, copySite.UpdatedAt = 0, 0, 0
			created, createErr := s.store.CreateSite(ctx, &copySite)
			if createErr != nil {
				return result, createErr
			}
			siteIDs[oldID] = created.ID
			existingSites = append(existingSites, created)
		}
		result.Sites++
	}
	existingAccounts, err := s.store.ListSiteAccounts(ctx, 0, false)
	if err != nil {
		return result, err
	}
	accountIDs := make(map[int64]int64)
	for _, item := range input.Accounts {
		if item.Account == nil {
			continue
		}
		newSiteID := siteIDs[item.Account.SiteID]
		if newSiteID == 0 {
			continue
		}
		sealed, sealErr := s.siteControl.cipher.Seal(item.Credentials)
		if sealErr != nil {
			return result, sealErr
		}
		oldID := item.Account.ID
		copyAccount := *item.Account
		copyAccount.SiteID, copyAccount.DeletedAt = newSiteID, 0
		copyAccount.CredentialCiphertext, copyAccount.CredentialKeyVersion = sealed, s.siteControl.cipher.Version()
		var existing *model.SiteAccount
		for _, candidate := range existingAccounts {
			if candidate != nil && candidate.SiteID == newSiteID && candidate.Label == copyAccount.Label {
				existing = candidate
				break
			}
		}
		if existing != nil {
			copyAccount.ID = existing.ID
			updated, updateErr := s.store.UpdateSiteAccount(ctx, existing.ID, &copyAccount)
			if updateErr != nil {
				return result, updateErr
			}
			if updateErr = s.store.UpdateSiteAccountCredential(ctx, existing.ID, copyAccount.CredentialType, sealed, s.siteControl.cipher.Version()); updateErr != nil {
				return result, updateErr
			}
			accountIDs[oldID] = updated.ID
		} else {
			copyAccount.ID, copyAccount.CreatedAt, copyAccount.UpdatedAt = 0, 0, 0
			created, createErr := s.store.CreateSiteAccount(ctx, &copyAccount)
			if createErr != nil {
				return result, createErr
			}
			accountIDs[oldID] = created.ID
			existingAccounts = append(existingAccounts, created)
		}
		result.Accounts++
	}
	modelsByAccount := make(map[int64][]model.SiteAccountModel)
	for _, item := range input.Models {
		if mapped := accountIDs[item.SiteAccountID]; mapped > 0 {
			item.SiteAccountID = mapped
			modelsByAccount[mapped] = append(modelsByAccount[mapped], item)
		}
	}
	for accountID, models := range modelsByAccount {
		if err := s.store.ReplaceSiteAccountModels(ctx, accountID, models); err != nil {
			return result, err
		}
	}
	channelIDs := make(map[int64]int64, len(input.Channels))
	if len(input.Channels) > 0 {
		existingChannels, listErr := s.store.ListConfigs(ctx)
		if listErr != nil {
			return result, listErr
		}
		existingByID := make(map[int64]*model.Config, len(existingChannels))
		existingByName := make(map[string]*model.Config, len(existingChannels))
		for _, channel := range existingChannels {
			if channel != nil {
				existingByID[channel.ID] = channel
				existingByName[channel.Name] = channel
			}
		}
		channels := make([]*model.ChannelWithKeys, 0, len(input.Channels))
		oldIDs := make([]int64, 0, len(input.Channels))
		for _, item := range input.Channels {
			if item.Config == nil {
				continue
			}
			config := item.Config.Clone()
			oldID := config.ID
			config.OAuthCredential = item.OAuthCredential
			if existing := existingByName[config.Name]; existing != nil {
				config.ID = existing.ID
			} else if existing := existingByID[config.ID]; existing != nil && existing.Name != config.Name {
				config.ID = 0
			}
			keys := append([]model.APIKey(nil), item.APIKeys...)
			channels = append(channels, &model.ChannelWithKeys{Config: config, APIKeys: keys})
			oldIDs = append(oldIDs, oldID)
		}
		created, updated, importErr := s.store.ImportChannelBatch(ctx, channels)
		if importErr != nil {
			return result, importErr
		}
		for index, channel := range channels {
			if channel != nil && channel.Config != nil {
				channelIDs[oldIDs[index]] = channel.Config.ID
			}
		}
		result.Channels = created + updated
		s.InvalidateChannelListCache()
		s.InvalidateAllAPIKeysCache()
	}
	existingTokens, err := s.store.ListAuthTokens(ctx)
	if err != nil {
		return result, err
	}
	tokenByHash := make(map[string]*model.AuthToken)
	for _, token := range existingTokens {
		if token != nil {
			tokenByHash[token.Token] = token
		}
	}
	for _, item := range input.Tokens {
		plain := strings.TrimSpace(item.PlainToken)
		if plain == "" {
			continue
		}
		hash := model.HashToken(plain)
		sealed, sealErr := s.siteControl.cipher.Seal(plain)
		if sealErr != nil {
			return result, sealErr
		}
		mode, modeErr := model.NormalizeChannelRestrictionMode(item.ChannelRestrictionMode)
		if modeErr != nil {
			return result, modeErr
		}
		allowedChannelIDs := remapBackupChannelIDs(item.AllowedChannelIDs, channelIDs)
		if existing := tokenByHash[hash]; existing != nil {
			existing.Description, existing.ExpiresAt, existing.IsActive = item.Description, item.ExpiresAt, item.IsActive
			existing.AllowedModels, existing.AllowedChannelIDs, existing.ChannelRestrictionMode = item.AllowedModels, allowedChannelIDs, mode
			existing.TokenCiphertext, existing.TokenHint, existing.MaxConcurrency = sealed, model.MaskToken(plain), item.MaxConcurrency
			existing.SetCostLimitUSD(item.CostLimitUSD)
			if err := s.store.UpdateAuthToken(ctx, existing); err != nil {
				return result, err
			}
		} else {
			token := &model.AuthToken{Token: hash, TokenCiphertext: sealed, TokenHint: model.MaskToken(plain), Description: item.Description, ExpiresAt: item.ExpiresAt, IsActive: item.IsActive, AllowedModels: item.AllowedModels, AllowedChannelIDs: allowedChannelIDs, ChannelRestrictionMode: mode, MaxConcurrency: item.MaxConcurrency}
			token.SetCostLimitUSD(item.CostLimitUSD)
			if err := s.store.CreateAuthToken(ctx, token); err != nil {
				return result, err
			}
			tokenByHash[hash] = token
		}
		result.Tokens++
	}
	if s.authService != nil {
		_ = s.authService.ReloadAuthTokens()
	}
	return result, nil
}

func remapBackupChannelIDs(ids []int64, mapping map[int64]int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if mapped := mapping[id]; mapped > 0 {
			result = append(result, mapped)
		}
	}
	return result
}

func (s *Server) importNotification(ctx context.Context, input *backupNotification) error {
	config, err := s.siteControl.loadWebhookConfig(ctx)
	if err != nil {
		return err
	}
	config.Enabled, config.TelegramEnabled = input.WebhookEnabled, input.TelegramEnabled
	config.TelegramUseSystemProxy = input.TelegramUseSystemProxy
	config.LowBalanceEnabled, config.LowBalanceThreshold = input.LowBalanceEnabled, input.LowBalanceThreshold
	config.CheckinFailureEnabled, config.CooldownMinutes = input.CheckinFailureEnabled, input.CooldownMinutes
	config.URLCiphertext, config.URLKeyVersion, config.URLConfigured = "", "", false
	config.TelegramBotCiphertext, config.TelegramBotKeyVersion = "", ""
	config.TelegramChatCiphertext, config.TelegramChatKeyVersion, config.TelegramConfigured = "", "", false
	if input.WebhookURL != "" {
		sealed, sealErr := s.siteControl.cipher.Seal(webhookSecret{URL: input.WebhookURL})
		if sealErr != nil {
			return sealErr
		}
		config.URLCiphertext, config.URLKeyVersion, config.URLConfigured = sealed, s.siteControl.cipher.Version(), true
	}
	if input.TelegramBotToken != "" && input.TelegramChatID != "" {
		bot, sealErr := s.siteControl.cipher.Seal(telegramSecret{Value: input.TelegramBotToken})
		if sealErr != nil {
			return sealErr
		}
		chat, sealErr := s.siteControl.cipher.Seal(telegramSecret{Value: input.TelegramChatID})
		if sealErr != nil {
			return sealErr
		}
		config.TelegramBotCiphertext, config.TelegramBotKeyVersion = bot, s.siteControl.cipher.Version()
		config.TelegramChatCiphertext, config.TelegramChatKeyVersion, config.TelegramConfigured = chat, s.siteControl.cipher.Version(), true
	}
	return s.store.UpsertWebhookConfig(ctx, config)
}

func (s *Server) importBackupTarget(ctx context.Context, input *backupTarget) error {
	config := &model.BackupConfig{ID: 1, Enabled: input.Enabled, FileURL: input.FileURL, Username: input.Username, ExportType: input.ExportType, AutoSyncEnabled: input.AutoSyncEnabled, AutoSyncIntervalHours: input.AutoSyncIntervalHours}
	if existing, err := s.store.GetBackupConfig(ctx); err == nil {
		config.CreatedAt = existing.CreatedAt
	}
	if input.Password != "" {
		sealed, err := s.siteControl.cipher.Seal(backupPasswordSecret{Value: input.Password})
		if err != nil {
			return err
		}
		config.PasswordCiphertext, config.PasswordKeyVersion = sealed, s.siteControl.cipher.Version()
	}
	return s.store.UpsertBackupConfig(ctx, config)
}

func defaultBackupConfig() *model.BackupConfig {
	return &model.BackupConfig{ID: 1, ExportType: "all", AutoSyncIntervalHours: backupDefaultIntervalHour}
}

func (s *Server) loadBackupConfig(ctx context.Context) (*model.BackupConfig, error) {
	config, err := s.store.GetBackupConfig(ctx)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "not found") {
		return defaultBackupConfig(), nil
	}
	return config, err
}

func (s *Server) backupConfigView(config *model.BackupConfig) backupConfigView {
	view := backupConfigView{BackupConfig: config}
	if config != nil && config.PasswordConfigured {
		view.PasswordMasked = "********"
	}
	return view
}

func validateWebDAVURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("invalid_webdav_url")
	}
	return nil
}

func (s *Server) HandleBackupWebDAV(c *gin.Context) {
	if c.Request.Method == http.MethodGet {
		config, err := s.loadBackupConfig(c.Request.Context())
		if err != nil {
			RespondError(c, http.StatusInternalServerError, err)
			return
		}
		RespondJSON(c, http.StatusOK, s.backupConfigView(config))
		return
	}
	var request struct {
		Enabled               bool    `json:"enabled"`
		FileURL               string  `json:"file_url"`
		Username              string  `json:"username"`
		Password              *string `json:"password"`
		ClearPassword         bool    `json:"clear_password"`
		ExportType            string  `json:"export_type"`
		AutoSyncEnabled       bool    `json:"auto_sync_enabled"`
		AutoSyncIntervalHours int     `json:"auto_sync_interval_hours"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid_request")
		return
	}
	exportType, err := normalizeBackupType(request.ExportType)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(request.FileURL) != "" {
		if err := validateWebDAVURL(request.FileURL); err != nil {
			RespondErrorMsg(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	if (request.Enabled || request.AutoSyncEnabled) && strings.TrimSpace(request.FileURL) == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "webdav_url_required")
		return
	}
	if request.AutoSyncIntervalHours < 1 || request.AutoSyncIntervalHours > 720 {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid_backup_interval")
		return
	}
	config, err := s.loadBackupConfig(c.Request.Context())
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	config.Enabled, config.FileURL, config.Username = request.Enabled, strings.TrimSpace(request.FileURL), strings.TrimSpace(request.Username)
	config.ExportType, config.AutoSyncEnabled, config.AutoSyncIntervalHours = exportType, request.AutoSyncEnabled, request.AutoSyncIntervalHours
	if request.ClearPassword {
		config.PasswordCiphertext, config.PasswordKeyVersion = "", ""
	}
	if request.Password != nil && strings.TrimSpace(*request.Password) != "" {
		if s.siteControl == nil || s.siteControl.cipher == nil {
			RespondErrorMsg(c, http.StatusLocked, "credential_locked")
			return
		}
		sealed, sealErr := s.siteControl.cipher.Seal(backupPasswordSecret{Value: *request.Password})
		if sealErr != nil {
			RespondError(c, http.StatusInternalServerError, sealErr)
			return
		}
		config.PasswordCiphertext, config.PasswordKeyVersion = sealed, s.siteControl.cipher.Version()
	}
	config.PasswordConfigured = strings.TrimSpace(config.PasswordCiphertext) != ""
	if err := s.store.UpsertBackupConfig(c.Request.Context(), config); err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	RespondJSON(c, http.StatusOK, s.backupConfigView(config))
}

func (s *Server) webDAVPassword(config *model.BackupConfig) (string, error) {
	if config == nil || strings.TrimSpace(config.PasswordCiphertext) == "" {
		return "", nil
	}
	if s.siteControl == nil || s.siteControl.cipher == nil {
		return "", errors.New("credential_locked")
	}
	var secret backupPasswordSecret
	if err := s.siteControl.cipher.Open(config.PasswordCiphertext, &secret); err != nil {
		return "", err
	}
	return secret.Value, nil
}

func (s *Server) exportToWebDAV(ctx context.Context, requestedType string) (*model.BackupConfig, error) {
	if !s.backupExportRunning.CompareAndSwap(false, true) {
		return nil, errors.New("backup_in_progress")
	}
	defer s.backupExportRunning.Store(false)
	config, err := s.loadBackupConfig(ctx)
	if err != nil {
		return nil, err
	}
	if !config.Enabled || validateWebDAVURL(config.FileURL) != nil {
		return config, errors.New("webdav_not_configured")
	}
	backupType := requestedType
	if strings.TrimSpace(backupType) == "" {
		backupType = config.ExportType
	}
	document, err := s.buildBackupDocument(ctx, backupType)
	if err != nil {
		return config, err
	}
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return config, err
	}
	password, err := s.webDAVPassword(config)
	if err != nil {
		return config, err
	}
	client, err := provider.ClientFactory{AllowPrivate: true}.New("")
	if err != nil {
		return config, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, config.FileURL, bytes.NewReader(body))
	if err != nil {
		return config, err
	}
	request.Header.Set("Content-Type", "application/json")
	if config.Username != "" || password != "" {
		request.SetBasicAuth(config.Username, password)
	}
	response, err := client.Do(request)
	if err != nil {
		return config, errors.New("webdav_request_failed")
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return config, fmt.Errorf("webdav_http_%d", response.StatusCode)
	}
	config.LastSyncAt, config.LastError = time.Now().UnixMilli(), ""
	_ = s.store.UpsertBackupConfig(context.Background(), config)
	return config, nil
}

func (s *Server) importFromWebDAV(ctx context.Context) (backupImportResult, *model.BackupConfig, error) {
	var result backupImportResult
	config, err := s.loadBackupConfig(ctx)
	if err != nil {
		return result, nil, err
	}
	if !config.Enabled || validateWebDAVURL(config.FileURL) != nil {
		return result, config, errors.New("webdav_not_configured")
	}
	password, err := s.webDAVPassword(config)
	if err != nil {
		return result, config, err
	}
	client, err := provider.ClientFactory{AllowPrivate: true}.New("")
	if err != nil {
		return result, config, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.FileURL, nil)
	if err != nil {
		return result, config, err
	}
	if config.Username != "" || password != "" {
		request.SetBasicAuth(config.Username, password)
	}
	response, err := client.Do(request)
	if err != nil {
		return result, config, errors.New("webdav_request_failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, config, fmt.Errorf("webdav_http_%d", response.StatusCode)
	}
	if response.ContentLength > backupMaxImportBytes {
		return result, config, errors.New("backup_too_large")
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, backupMaxImportBytes+1))
	if readErr != nil {
		return result, config, errors.New("webdav_request_failed")
	}
	if len(body) > backupMaxImportBytes {
		return result, config, errors.New("backup_too_large")
	}
	var document backupDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return result, config, errors.New("invalid_backup_document")
	}
	result, err = s.importBackupDocument(ctx, &document)
	if err != nil {
		return result, config, err
	}
	config.LastSyncAt, config.LastError = time.Now().UnixMilli(), ""
	_ = s.store.UpsertBackupConfig(context.Background(), config)
	return result, config, nil
}

func (s *Server) recordBackupError(config *model.BackupConfig, err error) {
	if config == nil || err == nil {
		return
	}
	config.LastError = sanitizeWebhookError(err)
	_ = s.store.UpsertBackupConfig(context.Background(), config)
}

func (s *Server) HandleBackupWebDAVExport(c *gin.Context) {
	var request struct {
		Type string `json:"type"`
	}
	_ = c.ShouldBindJSON(&request)
	config, err := s.exportToWebDAV(c.Request.Context(), request.Type)
	if err != nil {
		s.recordBackupError(config, err)
		RespondErrorMsg(c, http.StatusBadGateway, err.Error())
		return
	}
	RespondJSON(c, http.StatusOK, gin.H{"status": "success", "file_url": config.FileURL, "last_sync_at": config.LastSyncAt})
}

func (s *Server) HandleBackupWebDAVImport(c *gin.Context) {
	result, config, err := s.importFromWebDAV(c.Request.Context())
	if err != nil {
		s.recordBackupError(config, err)
		RespondErrorMsg(c, http.StatusBadGateway, err.Error())
		return
	}
	RespondJSON(c, http.StatusOK, result)
	if result.Restart {
		go triggerRestart()
	}
}

func (s *Server) startBackupScheduler() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(15 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-s.shutdownCh:
				return
			case <-ticker.C:
				config, err := s.loadBackupConfig(s.baseCtx)
				if err != nil || !config.Enabled || !config.AutoSyncEnabled {
					continue
				}
				interval := time.Duration(config.AutoSyncIntervalHours) * time.Hour
				if interval <= 0 {
					interval = backupDefaultIntervalHour * time.Hour
				}
				if config.LastSyncAt > 0 && time.Since(time.UnixMilli(config.LastSyncAt)) < interval {
					continue
				}
				if _, err := s.exportToWebDAV(s.baseCtx, config.ExportType); err != nil {
					s.recordBackupError(config, err)
				}
			}
		}
	}()
}
