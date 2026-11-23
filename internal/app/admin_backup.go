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
	"strconv"
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"

	"github.com/gin-gonic/gin"
)

const (
	backupSchemaVersion       = "1.0"
	backupMaxImportBytes      = 32 << 20
	backupDefaultIntervalHour = 24
	backupDefaultFileName     = "pivotflow-backup.json"
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
	AllowedProtocols       []string `json:"allowed_protocols"`
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

// Portable references only; synchronization ownership and observations belong
// to the destination and are never copied from the source instance.
type backupBinding struct {
	SiteAccountID int64  `json:"site_account_id"`
	ChannelID     int64  `json:"channel_id"`
	ProjectionKey string `json:"projection_key"`
	PricingGroup  string `json:"pricing_group,omitempty"`
}

type backupConnections struct {
	Sites    []*model.Site            `json:"sites"`
	Accounts []backupAccount          `json:"accounts"`
	Models   []model.SiteAccountModel `json:"models"`
	Channels []backupChannel          `json:"channels"`
	Bindings []backupBinding          `json:"bindings,omitempty"`
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
	Bindings int  `json:"bindings"`
	Tokens   int  `json:"tokens"`
	Settings int  `json:"settings"`
	Restart  bool `json:"restart_required"`
}

type backupPasswordSecret struct {
	Value string `json:"value"`
}

type backupConfigView struct {
	*model.BackupConfig
	PasswordMasked string   `json:"password_masked,omitempty"`
	Warnings       []string `json:"warnings,omitempty"`
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
	var document *backupDocument
	// Capture every reference and credential from the same primary snapshot.
	// Encoding and all WebDAV network operations happen after this completes.
	err = storage.WithConfigurationTransaction(ctx, storage.ConfigurationSource(s.store), func(txCtx context.Context, store storage.Store) error {
		var buildErr error
		document, buildErr = s.backupWriter(store).buildBackupDocumentSnapshot(txCtx, backupType)
		return buildErr
	})
	return document, err
}

func (s *Server) buildBackupDocumentSnapshot(ctx context.Context, backupType string) (*backupDocument, error) {
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
	accountIDs := make(map[int64]bool, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		credentials, credentialErr := s.siteControl.credentials(account)
		if credentialErr != nil {
			return nil, nil, fmt.Errorf("decrypt account %d: %w", account.ID, credentialErr)
		}
		result.Accounts = append(result.Accounts, backupAccount{Account: account, Credentials: credentials})
		accountIDs[account.ID] = true
		models, modelErr := s.store.ListSiteAccountModels(ctx, model.SiteModelFilter{SiteAccountID: account.ID, IncludeDisabled: true})
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
	channelIDs := make(map[int64]bool, len(configs))
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
		channelIDs[config.ID] = true
	}
	var warnings []string
	bindings, err := s.store.ListSiteChannelBindings(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, binding := range bindings {
		if binding == nil {
			continue
		}
		if !accountIDs[binding.SiteAccountID] || !channelIDs[binding.ChannelID] {
			warnings = append(warnings, fmt.Sprintf("绑定 #%d 的账号或渠道已不存在，未包含在连接备份中", binding.ID))
			continue
		}
		result.Bindings = append(result.Bindings, backupBinding{SiteAccountID: binding.SiteAccountID, ChannelID: binding.ChannelID, ProjectionKey: binding.ProjectionKey, PricingGroup: binding.PricingGroup})
	}
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
		result.Tokens = append(result.Tokens, backupToken{PlainToken: plain, Description: token.Description, ExpiresAt: token.ExpiresAt, IsActive: token.IsActive, AllowedModels: token.AllowedModels, AllowedProtocols: append([]string{}, token.AllowedProtocols...), AllowedChannelIDs: token.AllowedChannelIDs, ChannelRestrictionMode: token.ChannelRestrictionMode, CostLimitUSD: token.CostLimitUSD(), MaxConcurrency: token.MaxConcurrency})
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
	RespondErrorMsg(c, http.StatusPreconditionRequired, "preview_required")
}

func (s *Server) importBackupDocument(ctx context.Context, document *backupDocument) (backupImportResult, error) {
	preview, err := s.previewBackupDocument(ctx, document)
	if err != nil {
		return backupImportResult{}, err
	}
	result, err := s.applyConfigPreview(ctx, preview.ID)
	if err != nil {
		return backupImportResult{}, err
	}
	return *result.Backup, nil
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
	config := defaultBackupConfig()
	existing, err := s.store.GetBackupConfig(ctx)
	if err != nil && err.Error() != "not found" {
		return err
	}
	if existing != nil {
		*config = *existing
	}
	before := *config
	config.Enabled, config.FileURL, config.Username = input.Enabled, input.FileURL, input.Username
	config.ExportType, config.AutoSyncEnabled, config.AutoSyncIntervalHours = input.ExportType, input.AutoSyncEnabled, input.AutoSyncIntervalHours
	if input.Password == "" {
		config.PasswordCiphertext, config.PasswordKeyVersion = "", ""
	} else {
		if err := s.replaceBackupPassword(config, input.Password); err != nil {
			return err
		}
	}
	if !sameBackupDestination(&before, config) {
		clearBackupObservations(config)
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
	if config != nil {
		if warning := s.pendingBackupObservationWarning(config); warning != "" {
			view.Warnings = []string{warning}
		}
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

// webDAVTargetURL accepts either a concrete JSON file URL or a WebDAV
// collection URL. Most clients expose the latter (for example /dav or
// /remote.php/dav/files/user), so appending the deterministic backup name is
// compatible with providers that reject PUT to a collection itself.
func webDAVTargetURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".json") {
		return raw
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + backupDefaultFileName
	parsed.RawPath = ""
	return parsed.String()
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
	var config *model.BackupConfig
	err = storage.WithConfigurationTransaction(c.Request.Context(), s.store, func(ctx context.Context, store storage.Store) error {
		loaded, loadErr := store.GetBackupConfig(ctx)
		if loadErr != nil && loadErr.Error() != "not found" {
			return loadErr
		}
		if loaded == nil {
			loaded = defaultBackupConfig()
		}
		config = loaded
		before := *config
		config.Enabled, config.FileURL, config.Username = request.Enabled, strings.TrimSpace(request.FileURL), strings.TrimSpace(request.Username)
		config.ExportType, config.AutoSyncEnabled, config.AutoSyncIntervalHours = exportType, request.AutoSyncEnabled, request.AutoSyncIntervalHours
		if request.ClearPassword {
			config.PasswordCiphertext, config.PasswordKeyVersion = "", ""
		}
		if request.Password != nil && strings.TrimSpace(*request.Password) != "" {
			if err := s.replaceBackupPassword(config, *request.Password); err != nil {
				return err
			}
		}
		if !sameBackupDestination(&before, config) {
			clearBackupObservations(config)
		}
		config.PasswordConfigured = strings.TrimSpace(config.PasswordCiphertext) != ""
		return store.UpsertBackupConfig(ctx, config)
	})
	if err != nil {
		if err.Error() == "credential_locked" {
			RespondErrorMsg(c, http.StatusLocked, "credential_locked")
		} else if storage.IsConfigurationCacheDegraded(err) {
			s.discardChangedBackupObservation(config)
			view := s.backupConfigView(config)
			view.Warnings = append(view.Warnings, "backup_status_cache_degraded")
			RespondJSON(c, http.StatusOK, view)
		} else if storage.IsConfigurationCommitUnknown(err) {
			RespondErrorMsg(c, http.StatusInternalServerError, "backup_settings_result_unknown")
		} else {
			RespondErrorMsg(c, http.StatusInternalServerError, "backup_settings_save_failed")
		}
		return
	}
	s.discardChangedBackupObservation(config)
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

func (s *Server) replaceBackupPassword(config *model.BackupConfig, value string) error {
	if s.siteControl == nil || s.siteControl.cipher == nil {
		return errors.New("credential_locked")
	}
	// A readable identical secret keeps the destination identity. An unreadable
	// old envelope can be repaired by an explicit replacement under this key.
	if oldValue, err := s.webDAVPassword(config); err == nil && oldValue == value {
		return nil
	}
	sealed, err := s.siteControl.cipher.Seal(backupPasswordSecret{Value: value})
	if err != nil {
		return err
	}
	config.PasswordCiphertext, config.PasswordKeyVersion = sealed, s.siteControl.cipher.Version()
	return nil
}

func webDAVHTTPError(statusCode int, operation string) error {
	detail := "WebDAV 服务返回了非成功状态"
	switch statusCode {
	case http.StatusUnauthorized:
		detail = "WebDAV 认证失败，请检查用户名、应用密码和 WebDAV 地址"
	case http.StatusForbidden:
		detail = "WebDAV 拒绝访问，请检查账号是否有目标目录的读写权限"
	case http.StatusNotFound:
		if operation == "download" {
			detail = "WebDAV 中没有找到备份文件，请先上传备份并检查目标目录"
		} else {
			detail = "WebDAV 目标目录不存在，请先在存储服务中创建该目录"
		}
	case http.StatusMethodNotAllowed:
		detail = "WebDAV 不允许当前文件操作，请确认填写的是 WebDAV 服务地址而不是网页登录地址"
	case http.StatusConflict:
		detail = "WebDAV 目标父目录不存在，请先在存储服务中创建目录"
	case http.StatusLocked:
		detail = "WebDAV 目标文件已被锁定"
	case http.StatusInsufficientStorage:
		detail = "WebDAV 存储空间不足"
	case http.StatusBadGateway:
		detail = "WebDAV 或其反向代理返回了 502，请检查 WebDAV 服务端状态、路径和网关转发配置"
	case http.StatusGatewayTimeout:
		detail = "WebDAV 网关响应超时，请检查服务端状态和网络连通性"
	default:
		if statusCode >= 500 {
			detail = "WebDAV 服务暂时不可用，请稍后重试"
		}
	}
	return fmt.Errorf("webdav_http_%d: %s", statusCode, detail)
}

func webDAVHTMLResponse(response *http.Response) bool {
	if response == nil {
		return false
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	return strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml+xml")
}

func (s *Server) exportToWebDAV(ctx context.Context, requestedType string) (*backupExportResult, error) {
	if !s.backupExportRunning.CompareAndSwap(false, true) {
		return nil, errors.New("backup_in_progress")
	}
	defer s.backupExportRunning.Store(false)
	config, err := s.loadBackupConfig(ctx)
	if err != nil {
		return nil, err
	}
	result := &backupExportResult{config: config, Warnings: []string{}}
	if config == nil || !config.Enabled || validateWebDAVURL(config.FileURL) != nil {
		return result, errors.New("webdav_not_configured")
	}
	backupType := requestedType
	if strings.TrimSpace(backupType) == "" {
		backupType = config.ExportType
	}
	document, err := s.buildBackupDocument(ctx, backupType)
	if err != nil {
		return result, err
	}
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return result, err
	}
	if len(body) > backupMaxImportBytes {
		return result, errors.New("backup_too_large")
	}
	password, err := s.webDAVPassword(config)
	if err != nil {
		return result, err
	}
	client, err := provider.ClientFactory{AllowPrivate: true}.New("")
	if err != nil {
		return result, err
	}
	defer client.CloseIdleConnections()
	targetURL := webDAVTargetURL(config.FileURL)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	request.Header.Set("Content-Type", "application/json")
	if config.Username != "" || password != "" {
		request.SetBasicAuth(config.Username, password)
	}
	response, err := client.Do(request)
	if err != nil {
		return result, errors.New("webdav_request_failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		return result, webDAVHTTPError(response.StatusCode, "upload")
	}
	// A successful WebDAV PUT may legitimately return an HTML status page
	// (some providers do this for 201/204 responses). The HTTP status is the
	// accepted upload result; the following GET must match the file itself.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	if err := verifyWebDAVUpload(ctx, client, targetURL, config.Username, password, body); err != nil {
		return result, err
	}
	proof := newBackupVerification(body, document)
	result.Status, result.FileURL, result.LastSyncAt, result.Verification = "success", targetURL, proof.VerifiedAt, proof
	stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var warning string
	result.MetadataSaved, warning = s.completeBackupVerification(stateCtx, config, proof)
	if warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	if proof.WarningCount > 0 {
		result.Warnings = append(result.Warnings, "backup_has_warnings")
	}
	return result, nil
}

func (s *Server) downloadWebDAVBackup(ctx context.Context) (*backupDocument, *model.BackupConfig, error) {
	var result *backupDocument
	config, err := s.loadBackupConfig(ctx)
	if err != nil {
		return result, nil, previewFailure("", "snapshot_failed")
	}
	if config == nil || !config.Enabled || validateWebDAVURL(config.FileURL) != nil {
		return result, config, errors.New("webdav_not_configured")
	}
	password, err := s.webDAVPassword(config)
	if err != nil {
		return result, config, previewFailure("", "credential_locked")
	}
	client, err := provider.ClientFactory{AllowPrivate: true}.New("")
	if err != nil {
		return result, config, previewFailure("", "webdav_request_failed")
	}
	defer client.CloseIdleConnections()
	targetURL := webDAVTargetURL(config.FileURL)
	body, readErr := readWebDAVBody(ctx, client, targetURL, config.Username, password)
	if readErr != nil {
		return result, config, readErr
	}
	var document backupDocument
	if err := json.Unmarshal(body, &document); err != nil {
		return result, config, errors.New("invalid_backup_document")
	}
	return &document, config, nil
}

func (s *Server) importFromWebDAV(ctx context.Context) (backupImportResult, *model.BackupConfig, error) {
	document, config, err := s.downloadWebDAVBackup(ctx)
	if err != nil {
		return backupImportResult{}, config, err
	}
	result, err := s.importBackupDocument(ctx, document)
	return result, config, err
}

func (s *Server) HandleBackupWebDAVPreview(c *gin.Context) {
	document, _, err := s.downloadWebDAVBackup(c.Request.Context())
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, safeBackupWebDAVPreviewError(err))
		return
	}
	preview, err := s.previewBackupDocument(c.Request.Context(), document)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}
	RespondJSON(c, http.StatusOK, preview)
}

func safeBackupWebDAVPreviewError(err error) string {
	var safe *configPreviewError
	if errors.As(err, &safe) {
		return safe.code
	}
	code := strings.SplitN(err.Error(), ":", 2)[0]
	switch code {
	case "webdav_not_configured", "webdav_request_failed", "webdav_html_response", "backup_too_large", "invalid_backup_document", "credential_locked":
		return code
	}
	if strings.HasPrefix(code, "webdav_http_") {
		status, parseErr := strconv.Atoi(strings.TrimPrefix(code, "webdav_http_"))
		if parseErr == nil && status >= 100 && status <= 599 {
			return code
		}
	}
	return "webdav_request_failed"
}

func (s *Server) recordBackupError(config *model.BackupConfig, err error) {
	if config == nil || err == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.backupVerificationState.mu.Lock()
	defer s.backupVerificationState.mu.Unlock()
	_ = storage.WithConfigurationTransaction(ctx, s.store, func(txCtx context.Context, store storage.Store) error {
		current, loadErr := store.GetBackupConfig(txCtx)
		if loadErr != nil {
			return loadErr
		}
		if backupIdentity(current) != backupIdentity(config) {
			return nil
		}
		current.LastError = safeBackupExportError(err).Error()
		return store.UpsertBackupConfig(txCtx, current)
	})
}

func (s *Server) HandleBackupWebDAVExport(c *gin.Context) {
	var request struct {
		Type string `json:"type"`
	}
	_ = c.ShouldBindJSON(&request)
	result, err := s.exportToWebDAV(c.Request.Context(), request.Type)
	if err != nil {
		if result != nil {
			s.recordBackupError(result.config, err)
		}
		// Keep admin operation failures as JSON so reverse proxies cannot hide
		// the actual WebDAV diagnosis behind an HTML error page.
		RespondErrorMsg(c, http.StatusOK, safeBackupExportError(err).Error())
		return
	}
	RespondJSON(c, http.StatusOK, result)
}

func (s *Server) HandleBackupWebDAVImport(c *gin.Context) {
	RespondErrorMsg(c, http.StatusPreconditionRequired, "preview_required")
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
				if err != nil || config == nil {
					continue
				}
				if !s.backupSyncDue(s.baseCtx, config, time.Now()) {
					continue
				}
				if result, err := s.exportToWebDAV(s.baseCtx, config.ExportType); err != nil && result != nil {
					s.recordBackupError(result.config, err)
				}
			}
		}
	}()
}
