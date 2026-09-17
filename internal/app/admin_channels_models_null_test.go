package app

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// A channel with no models must serialize `models` as [] and never null.
//
// Regression point: with ModelEntries nil, encoding/json writes null, while the
// console declares `models` as a non-optional array. The channels page then
// throws inside a useMemo on `item.models.filter(...)`, and because React has no
// error boundary by default it unmounts the whole tree -- so every route after
// that one goes blank too, not just the channels page.
//
// This only surfaced after seeding demo data: an empty database never produces
// the "channel exists but has no models" row that the smoke gate runs against.
func TestChannelsListEmitsEmptyModelsArray(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()

	if _, err := store.CreateConfig(context.Background(), &model.Config{
		Name:     "no-models",
		URLs:     model.ChannelURLs{{URL: "https://api.example.com"}},
		Priority: 1,
		Enabled:  true,
	}); err != nil {
		t.Fatalf("CreateConfig failed: %v", err)
	}

	c, w := newTestContext(t, newRequest(http.MethodGet, "/admin/channels", nil))
	server.HandleChannels(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}

	var payload struct {
		Data []struct {
			Name   string          `json:"name"`
			Models json.RawMessage `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal failed: %v body=%s", err, w.Body.String())
	}
	if len(payload.Data) == 0 {
		t.Fatalf("expected at least one channel, body=%s", w.Body.String())
	}

	// Assert on the raw literal: decoding into []model.ModelEntry would make
	// null and [] both report length 0, which is exactly the regression.
	for _, channel := range payload.Data {
		if got := string(channel.Models); got != "[]" {
			t.Errorf("channel %q models=%s, want []", channel.Name, got)
		}
	}
}
