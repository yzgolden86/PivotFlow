package app

import (
	"context"
	"net/http"
	"testing"
)

func TestAdminLogsBootstrapReturnsConfiguredChannelClickAction(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	server.configService = NewConfigService(store)
	if err := server.configService.LoadDefaults(context.Background()); err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if err := store.UpdateSetting(context.Background(), "log_channel_click_action", "navigate"); err != nil {
		t.Fatalf("update click action: %v", err)
	}

	c, w := newTestContext(t, newRequest(http.MethodGet, "/admin/logs/bootstrap?range=today", nil))
	server.HandleLogsBootstrap(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	response := mustParseAPIResponse[LogsBootstrapResponse](t, w.Body.Bytes())
	if response.Data.LogChannelClickAction != "navigate" {
		t.Fatalf("click action=%q, want navigate", response.Data.LogChannelClickAction)
	}
}
