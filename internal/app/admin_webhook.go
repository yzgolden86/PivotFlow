package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"

	"github.com/gin-gonic/gin"
)

const (
	webhookEventLowBalance    = "low_balance"
	webhookEventCheckinFailed = "checkin_failed"
)

type webhookSecret struct {
	URL string `json:"url"`
}

type telegramSecret struct {
	Value string `json:"value"`
}

type webhookConfigView struct {
	*model.WebhookConfig
	URLMasked          string `json:"url_masked"`
	TelegramChatMasked string `json:"telegram_chat_masked,omitempty"`
}

type webhookUpdateRequest struct {
	Enabled                *bool    `json:"enabled"`
	URL                    *string  `json:"url"`
	TelegramEnabled        *bool    `json:"telegram_enabled"`
	TelegramBotToken       *string  `json:"telegram_bot_token"`
	TelegramChatID         *string  `json:"telegram_chat_id"`
	TelegramUseSystemProxy *bool    `json:"telegram_use_system_proxy"`
	TelegramClear          *bool    `json:"telegram_clear"`
	LowBalanceEnabled      *bool    `json:"low_balance_enabled"`
	LowBalanceThreshold    *float64 `json:"low_balance_threshold"`
	CheckinFailureEnabled  *bool    `json:"checkin_failure_enabled"`
	CooldownMinutes        *int     `json:"cooldown_minutes"`
}

type webhookPayload struct {
	SchemaVersion int            `json:"schema_version"`
	EventType     string         `json:"event_type"`
	EventKey      string         `json:"event_key"`
	OccurredAt    string         `json:"occurred_at"`
	Title         string         `json:"title"`
	Message       string         `json:"message"`
	Data          map[string]any `json:"data"`
}

func defaultWebhookConfig() *model.WebhookConfig {
	return &model.WebhookConfig{
		ID:                     1,
		LowBalanceEnabled:      true,
		LowBalanceThreshold:    10,
		CheckinFailureEnabled:  true,
		CooldownMinutes:        360,
		LastDeliveryStatus:     "never",
		TelegramUseSystemProxy: true,
	}
}

func (s *siteControlService) loadWebhookConfig(ctx context.Context) (*model.WebhookConfig, error) {
	config, err := s.store.GetWebhookConfig(ctx)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "not found") {
		return defaultWebhookConfig(), nil
	}
	return config, err
}

func (s *siteControlService) webhookEndpoint(config *model.WebhookConfig) (string, error) {
	if config == nil || strings.TrimSpace(config.URLCiphertext) == "" {
		return "", errors.New("webhook_not_configured")
	}
	if s.locked() {
		return "", credential.ErrCredentialLocked
	}
	var secret webhookSecret
	if err := s.cipher.Open(config.URLCiphertext, &secret); err != nil {
		return "", err
	}
	if err := provider.ValidateBaseURL(secret.URL, s.webhookSender.Clients.AllowPrivate); err != nil {
		return "", errors.New("invalid_webhook_url")
	}
	return secret.URL, nil
}

func maskedWebhookURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "configured"
	}
	masked := parsed.Scheme + "://" + parsed.Host
	if parsed.Path != "" && parsed.Path != "/" {
		masked += "/..."
	}
	return masked
}

func (s *siteControlService) webhookView(config *model.WebhookConfig) webhookConfigView {
	view := webhookConfigView{WebhookConfig: config}
	if endpoint, err := s.webhookEndpoint(config); err == nil {
		view.URLMasked = maskedWebhookURL(endpoint)
	}
	if _, chatID, err := s.telegramCredentials(config); err == nil {
		view.TelegramChatMasked = maskSecretTail(chatID)
	}
	return view
}

func maskSecretTail(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 4 {
		return "****"
	}
	return "****" + value[len(value)-4:]
}

func (s *siteControlService) telegramCredentials(config *model.WebhookConfig) (string, string, error) {
	if config == nil || strings.TrimSpace(config.TelegramBotCiphertext) == "" || strings.TrimSpace(config.TelegramChatCiphertext) == "" {
		return "", "", errors.New("telegram_not_configured")
	}
	if s.locked() {
		return "", "", credential.ErrCredentialLocked
	}
	var botToken, chatID telegramSecret
	if err := s.cipher.Open(config.TelegramBotCiphertext, &botToken); err != nil {
		return "", "", err
	}
	if err := s.cipher.Open(config.TelegramChatCiphertext, &chatID); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(botToken.Value) == "" || strings.TrimSpace(chatID.Value) == "" {
		return "", "", errors.New("telegram_not_configured")
	}
	return strings.TrimSpace(botToken.Value), strings.TrimSpace(chatID.Value), nil
}

func (s *siteControlService) handleWebhook(c *gin.Context) {
	switch c.Request.Method {
	case http.MethodGet:
		config, err := s.loadWebhookConfig(c.Request.Context())
		if err != nil {
			RespondError(c, http.StatusInternalServerError, err)
			return
		}
		RespondJSON(c, http.StatusOK, s.webhookView(config))
	case http.MethodPut:
		s.updateWebhook(c)
	default:
		RespondErrorMsg(c, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func (s *siteControlService) updateWebhook(c *gin.Context) {
	var request webhookUpdateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid_request")
		return
	}
	s.webhookMu.Lock()
	defer s.webhookMu.Unlock()
	config, err := s.loadWebhookConfig(c.Request.Context())
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	if request.Enabled != nil {
		config.Enabled = *request.Enabled
	}
	if request.TelegramEnabled != nil {
		config.TelegramEnabled = *request.TelegramEnabled
	}
	if request.TelegramUseSystemProxy != nil {
		config.TelegramUseSystemProxy = *request.TelegramUseSystemProxy
	}
	if request.TelegramClear != nil && *request.TelegramClear {
		config.TelegramEnabled = false
		config.TelegramBotCiphertext, config.TelegramBotKeyVersion = "", ""
		config.TelegramChatCiphertext, config.TelegramChatKeyVersion = "", ""
	}
	if request.TelegramBotToken != nil || request.TelegramChatID != nil {
		botToken := ""
		chatID := ""
		if request.TelegramBotToken != nil {
			botToken = strings.TrimSpace(*request.TelegramBotToken)
		}
		if request.TelegramChatID != nil {
			chatID = strings.TrimSpace(*request.TelegramChatID)
		}
		if botToken != "" || chatID != "" {
			if botToken == "" || chatID == "" || len(botToken) > 512 || len(chatID) > 128 {
				RespondErrorMsg(c, http.StatusBadRequest, "invalid_telegram_credentials")
				return
			}
			if s.locked() {
				RespondErrorMsg(c, http.StatusLocked, "credential_locked")
				return
			}
			sealedBot, sealErr := s.cipher.Seal(telegramSecret{Value: botToken})
			if sealErr != nil {
				RespondErrorMsg(c, http.StatusInternalServerError, "telegram_encrypt_failed")
				return
			}
			sealedChat, sealErr := s.cipher.Seal(telegramSecret{Value: chatID})
			if sealErr != nil {
				RespondErrorMsg(c, http.StatusInternalServerError, "telegram_encrypt_failed")
				return
			}
			config.TelegramBotCiphertext, config.TelegramBotKeyVersion = sealedBot, s.cipher.Version()
			config.TelegramChatCiphertext, config.TelegramChatKeyVersion = sealedChat, s.cipher.Version()
		}
	}
	if request.LowBalanceEnabled != nil {
		config.LowBalanceEnabled = *request.LowBalanceEnabled
	}
	if request.LowBalanceThreshold != nil {
		if *request.LowBalanceThreshold < 0 || *request.LowBalanceThreshold > 1e9 {
			RespondErrorMsg(c, http.StatusBadRequest, "invalid_low_balance_threshold")
			return
		}
		config.LowBalanceThreshold = *request.LowBalanceThreshold
	}
	if request.CheckinFailureEnabled != nil {
		config.CheckinFailureEnabled = *request.CheckinFailureEnabled
	}
	if request.CooldownMinutes != nil {
		if *request.CooldownMinutes < 5 || *request.CooldownMinutes > 10080 {
			RespondErrorMsg(c, http.StatusBadRequest, "invalid_cooldown_minutes")
			return
		}
		config.CooldownMinutes = *request.CooldownMinutes
	}
	if request.URL != nil {
		endpoint := strings.TrimSpace(*request.URL)
		if endpoint == "" {
			config.URLCiphertext, config.URLKeyVersion, config.URLConfigured, config.Enabled = "", "", false, false
		} else {
			if len(endpoint) > 2048 || provider.ValidateBaseURL(endpoint, false) != nil {
				RespondErrorMsg(c, http.StatusBadRequest, "invalid_webhook_url")
				return
			}
			if s.locked() {
				RespondErrorMsg(c, http.StatusLocked, "credential_locked")
				return
			}
			sealed, sealErr := s.cipher.Seal(webhookSecret{URL: endpoint})
			if sealErr != nil {
				RespondErrorMsg(c, http.StatusInternalServerError, "webhook_encrypt_failed")
				return
			}
			config.URLCiphertext, config.URLKeyVersion, config.URLConfigured = sealed, s.cipher.Version(), true
		}
	}
	if config.CooldownMinutes == 0 {
		config.CooldownMinutes = 360
	}
	if config.Enabled && strings.TrimSpace(config.URLCiphertext) == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "webhook_not_configured")
		return
	}
	config.TelegramConfigured = strings.TrimSpace(config.TelegramBotCiphertext) != "" && strings.TrimSpace(config.TelegramChatCiphertext) != ""
	if config.TelegramEnabled && !config.TelegramConfigured {
		RespondErrorMsg(c, http.StatusBadRequest, "telegram_not_configured")
		return
	}
	if err := s.store.UpsertWebhookConfig(c.Request.Context(), config); err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	RespondJSON(c, http.StatusOK, s.webhookView(config))
}

func (s *siteControlService) handleWebhookTest(c *gin.Context) {
	s.webhookMu.Lock()
	defer s.webhookMu.Unlock()
	config, err := s.loadWebhookConfig(c.Request.Context())
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	now := time.Now()
	payload := webhookPayload{
		SchemaVersion: 1,
		EventType:     "test",
		EventKey:      fmt.Sprintf("test:%d", now.UnixNano()),
		OccurredAt:    now.UTC().Format(time.RFC3339),
		Title:         "PivotFlow 通知测试",
		Message:       "通知通道配置正确，可以正常接收消息。",
		Data:          map[string]any{},
	}
	target := strings.ToLower(strings.TrimSpace(c.Query("target")))
	var attempts int
	var sendErr error
	if target == "telegram" {
		attempts, sendErr = s.sendTelegram(c.Request.Context(), config, payload)
	} else {
		endpoint, endpointErr := s.webhookEndpoint(config)
		if endpointErr != nil {
			RespondErrorMsg(c, http.StatusBadRequest, endpointErr.Error())
			return
		}
		attempts, sendErr = s.webhookSender.Send(c.Request.Context(), endpoint, payload)
	}
	config.LastDeliveryAt = time.Now().UnixMilli()
	config.LastDeliveryStatus, config.LastError = "success", ""
	if sendErr != nil {
		config.LastDeliveryStatus, config.LastError = "failed", sanitizeWebhookError(sendErr)
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_ = s.store.UpsertWebhookConfig(persistCtx, config)
	cancel()
	if sendErr != nil {
		RespondErrorMsg(c, http.StatusBadGateway, config.LastError)
		return
	}
	RespondJSON(c, http.StatusOK, gin.H{"status": "success", "attempts": attempts})
}

func (s *siteControlService) sendTelegram(ctx context.Context, config *model.WebhookConfig, payload webhookPayload) (int, error) {
	botToken, chatID, err := s.telegramCredentials(config)
	if err != nil {
		return 0, err
	}
	endpoint := "https://api.telegram.org/bot" + url.PathEscape(botToken) + "/sendMessage"
	proxyURL := ""
	if !config.TelegramUseSystemProxy {
		proxyURL = provider.DirectProxyURL
	}
	message := strings.TrimSpace(payload.Title)
	if detail := strings.TrimSpace(payload.Message); detail != "" {
		message += "\n" + detail
	}
	return s.webhookSender.SendWithProxy(ctx, endpoint, gin.H{"chat_id": chatID, "text": message, "disable_web_page_preview": true}, proxyURL)
}

func notificationConfigActive(config *model.WebhookConfig) bool {
	return config != nil && ((config.Enabled && config.URLConfigured) || (config.TelegramEnabled && config.TelegramConfigured))
}

func (s *siteControlService) dispatchWebhookEvent(config *model.WebhookConfig, eventKey, eventType string, accountID int64, payload webhookPayload) {
	s.webhookMu.Lock()
	defer s.webhookMu.Unlock()
	now := time.Now()
	state, err := s.store.GetWebhookEventState(s.baseCtx, eventKey)
	if err != nil {
		state = &model.WebhookEventState{EventKey: eventKey, EventType: eventType, SiteAccountID: accountID}
	}
	cooldown := time.Duration(config.CooldownMinutes) * time.Minute
	if state.LastAttemptAt > 0 && now.Sub(time.UnixMilli(state.LastAttemptAt)) < cooldown && state.Status != "resolved" {
		return
	}
	state.Status, state.LastAttemptAt, state.LastError = "sending", now.UnixMilli(), ""
	_ = s.store.UpsertWebhookEventState(s.baseCtx, state)
	attempts := 0
	var deliveryErrors []string
	delivered := 0
	notifyCtx, cancel := context.WithTimeout(s.baseCtx, 20*time.Second)
	defer cancel()
	if config.Enabled && config.URLConfigured {
		endpoint, endpointErr := s.webhookEndpoint(config)
		if endpointErr == nil {
			count, sendErr := s.webhookSender.Send(notifyCtx, endpoint, payload)
			attempts += count
			if sendErr == nil {
				delivered++
			} else {
				deliveryErrors = append(deliveryErrors, sanitizeWebhookError(sendErr))
			}
		} else {
			deliveryErrors = append(deliveryErrors, sanitizeWebhookError(endpointErr))
		}
	}
	if config.TelegramEnabled && config.TelegramConfigured {
		count, sendErr := s.sendTelegram(notifyCtx, config, payload)
		attempts += count
		if sendErr == nil {
			delivered++
		} else {
			deliveryErrors = append(deliveryErrors, sanitizeWebhookError(sendErr))
		}
	}
	var deliveryErr error
	if len(deliveryErrors) > 0 {
		deliveryErr = errors.New(strings.Join(deliveryErrors, "; "))
	}
	if delivered == 0 && deliveryErr == nil {
		deliveryErr = errors.New("notification_not_configured")
	}
	state.Attempts += attempts
	config.LastDeliveryAt = time.Now().UnixMilli()
	if deliveryErr == nil {
		state.Status, state.DeliveredAt, state.LastError = "delivered", config.LastDeliveryAt, ""
		config.LastDeliveryStatus, config.LastError = "success", ""
	} else {
		state.Status, state.LastError = "failed", sanitizeWebhookError(deliveryErr)
		config.LastDeliveryStatus, config.LastError = "failed", state.LastError
	}
	_ = s.store.UpsertWebhookEventState(s.baseCtx, state)
	_ = s.store.UpsertWebhookConfig(s.baseCtx, config)
}

func (s *siteControlService) evaluateLowBalance(account *model.SiteAccount, site *model.Site) {
	if account == nil || site == nil || account.Balance == nil {
		return
	}
	config, err := s.loadWebhookConfig(s.baseCtx)
	if err != nil || !notificationConfigActive(config) || !config.LowBalanceEnabled {
		return
	}
	eventKey := fmt.Sprintf("low_balance:%d", account.ID)
	if *account.Balance > config.LowBalanceThreshold {
		if state, stateErr := s.store.GetWebhookEventState(s.baseCtx, eventKey); stateErr == nil && state.Status != "resolved" {
			state.Status, state.LastError = "resolved", ""
			_ = s.store.UpsertWebhookEventState(s.baseCtx, state)
		}
		return
	}
	now := time.Now()
	payload := webhookPayload{
		SchemaVersion: 1,
		EventType:     webhookEventLowBalance,
		EventKey:      eventKey,
		OccurredAt:    now.UTC().Format(time.RFC3339),
		Title:         "Low balance alert",
		Message:       fmt.Sprintf("%s / %s balance is below the configured threshold.", site.Name, account.Label),
		Data: map[string]any{
			"site_id": site.ID, "site_name": site.Name, "site_account_id": account.ID,
			"account_label": account.Label, "balance": *account.Balance,
			"currency": account.BalanceCurrency, "threshold": config.LowBalanceThreshold,
		},
	}
	s.dispatchWebhookEvent(config, eventKey, webhookEventLowBalance, account.ID, payload)
}

func (s *siteControlService) notifyCheckinFailure(account *model.SiteAccount, site *model.Site, localDay, errorCode string) {
	if account == nil || site == nil {
		return
	}
	config, err := s.loadWebhookConfig(s.baseCtx)
	if err != nil || !notificationConfigActive(config) || !config.CheckinFailureEnabled {
		return
	}
	eventKey := fmt.Sprintf("checkin_failed:%d:%s", account.ID, localDay)
	now := time.Now()
	payload := webhookPayload{
		SchemaVersion: 1,
		EventType:     webhookEventCheckinFailed,
		EventKey:      eventKey,
		OccurredAt:    now.UTC().Format(time.RFC3339),
		Title:         "Check-in failed",
		Message:       fmt.Sprintf("%s / %s check-in failed.", site.Name, account.Label),
		Data: map[string]any{
			"site_id": site.ID, "site_name": site.Name, "site_account_id": account.ID,
			"account_label": account.Label, "local_day": localDay, "error_code": errorCode,
		},
	}
	s.dispatchWebhookEvent(config, eventKey, webhookEventCheckinFailed, account.ID, payload)
}

func sanitizeWebhookError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "webhook_delivery_failed"
	}
	if len(message) > 191 {
		message = message[:191]
	}
	return message
}
