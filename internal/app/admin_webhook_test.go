package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	sitewebhook "github.com/yzgolden86/PivotFlow/internal/site/webhook"
)

func TestWebhookConfigResponseMasksEncryptedEndpoint(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "webhook-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	sealed, err := cipher.Seal(webhookSecret{URL: "https://hooks.example.com/private/secret-token?key=hidden"})
	if err != nil {
		t.Fatal(err)
	}
	config := &model.WebhookConfig{ID: 1, Enabled: true, URLCiphertext: sealed, URLKeyVersion: cipher.Version(), LowBalanceEnabled: true, LowBalanceThreshold: 10, CheckinFailureEnabled: true, CooldownMinutes: 360, LastDeliveryStatus: "never"}
	if err := srv.store.UpsertWebhookConfig(srv.baseCtx, config); err != nil {
		t.Fatal(err)
	}

	c, recorder := newTestContext(t, newJSONRequest(t, http.MethodGet, "/admin/webhook", nil))
	srv.siteControl.handleWebhook(c)
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || !strings.Contains(body, "https://hooks.example.com/...") {
		t.Fatalf("status=%d body=%s", recorder.Code, body)
	}
	for _, secret := range []string{"secret-token", "hidden", sealed, "url_ciphertext"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response leaked %q: %s", secret, body)
		}
	}
}

func TestLowBalanceWebhookCooldownAndRecovery(t *testing.T) {
	var calls atomic.Int32
	var eventType string
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload webhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		eventType = payload.EventType
		w.WriteHeader(http.StatusNoContent)
	}))
	defer endpoint.Close()

	srv := newInMemoryServer(t)
	cipher, _ := credential.New([]byte("0123456789abcdef0123456789abcdef"), "webhook-test")
	srv.siteControl.cipher = cipher
	srv.siteControl.webhookSender = sitewebhook.Sender{Clients: provider.ClientFactory{AllowPrivate: true}, Timeout: time.Second}
	sealed, _ := cipher.Seal(webhookSecret{URL: endpoint.URL})
	config := &model.WebhookConfig{ID: 1, Enabled: true, URLCiphertext: sealed, URLKeyVersion: cipher.Version(), LowBalanceEnabled: true, LowBalanceThreshold: 10, CheckinFailureEnabled: true, CooldownMinutes: 60, LastDeliveryStatus: "never"}
	if err := srv.store.UpsertWebhookConfig(srv.baseCtx, config); err != nil {
		t.Fatal(err)
	}
	balance := 4.5
	account := &model.SiteAccount{ID: 21, SiteID: 8, Label: "main", Balance: &balance, BalanceCurrency: "USD"}
	site := &model.Site{ID: 8, Name: "Provider A"}

	srv.siteControl.evaluateLowBalance(account, site)
	srv.siteControl.evaluateLowBalance(account, site)
	if calls.Load() != 1 || eventType != webhookEventLowBalance {
		t.Fatalf("calls=%d event=%q", calls.Load(), eventType)
	}
	state, err := srv.store.GetWebhookEventState(srv.baseCtx, "low_balance:21")
	if err != nil || state.Status != "delivered" || state.Attempts != 1 {
		t.Fatalf("state=%+v err=%v", state, err)
	}

	balance = 20
	srv.siteControl.evaluateLowBalance(account, site)
	state, _ = srv.store.GetWebhookEventState(srv.baseCtx, "low_balance:21")
	if state.Status != "resolved" {
		t.Fatalf("state after recovery=%+v", state)
	}
	balance = 3
	srv.siteControl.evaluateLowBalance(account, site)
	if calls.Load() != 2 {
		t.Fatalf("recovered balance did not re-arm alert: calls=%d", calls.Load())
	}
}

func TestWebhookUpdateRejectsPrivateEndpoint(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, _ := credential.New([]byte("0123456789abcdef0123456789abcdef"), "webhook-test")
	srv.siteControl.cipher = cipher
	c, recorder := newTestContext(t, newJSONRequest(t, http.MethodPut, "/admin/webhook", map[string]any{"url": "http://127.0.0.1/hook"}))
	srv.siteControl.handleWebhook(c)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "invalid_webhook_url") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestTelegramConfigIsEncryptedMaskedAndClearable(t *testing.T) {
	srv := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "telegram-test")
	if err != nil {
		t.Fatal(err)
	}
	srv.siteControl.cipher = cipher
	botToken := "123456:AA-super-secret-bot-token"
	chatID := "-1001234567890"
	c, recorder := newTestContext(t, newJSONRequest(t, http.MethodPut, "/admin/webhook", map[string]any{
		"telegram_enabled":          true,
		"telegram_bot_token":        botToken,
		"telegram_chat_id":          chatID,
		"telegram_use_system_proxy": true,
	}))
	srv.siteControl.handleWebhook(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	for _, secret := range []string{botToken, chatID, "telegram_bot_ciphertext", "telegram_chat_ciphertext"} {
		if strings.Contains(recorder.Body.String(), secret) {
			t.Fatalf("response leaked %q: %s", secret, recorder.Body.String())
		}
	}
	if !strings.Contains(recorder.Body.String(), "****7890") {
		t.Fatalf("response does not contain masked chat ID: %s", recorder.Body.String())
	}
	stored, err := srv.store.GetWebhookConfig(srv.baseCtx)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TelegramBotCiphertext == "" || stored.TelegramChatCiphertext == "" || strings.Contains(stored.TelegramBotCiphertext, botToken) {
		t.Fatalf("telegram credentials were not encrypted: %+v", stored)
	}

	c, recorder = newTestContext(t, newJSONRequest(t, http.MethodPut, "/admin/webhook", map[string]any{"telegram_clear": true}))
	srv.siteControl.handleWebhook(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("clear status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	stored, err = srv.store.GetWebhookConfig(srv.baseCtx)
	if err != nil {
		t.Fatal(err)
	}
	if stored.TelegramEnabled || stored.TelegramBotCiphertext != "" || stored.TelegramChatCiphertext != "" {
		t.Fatalf("telegram credentials were not cleared: %+v", stored)
	}
}
