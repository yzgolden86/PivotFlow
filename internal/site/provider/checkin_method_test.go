package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The check-in method is discovered from a public endpoint, so every New
// API-family adapter must expose it. A missing method would silently disable
// the discovery and fall back to blind POSTs.
var (
	_ CheckinMethodProvider = (*NewAPI)(nil)
	_ CheckinMethodProvider = (*Veloera)(nil)
	_ CheckinMethodProvider = (*AnyRouter)(nil)
)

func TestDiscoverCheckinReadsPublicStatusEndpoint(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   string
		key    string
	}{
		{
			name:   "turnstile blocks server-side check-in",
			status: `{"success":true,"data":{"system_name":"a","checkin_enabled":true,"turnstile_check":true,"turnstile_site_key":"0xKEY"}}`,
			want:   CheckinMethodTurnstile,
			key:    "0xKEY",
		},
		{
			name:   "enabled without turnstile is attemptable",
			status: `{"success":true,"data":{"system_name":"a","checkin_enabled":true,"turnstile_check":false}}`,
			want:   CheckinMethodAvailable,
		},
		{
			name:   "explicitly disabled",
			status: `{"success":true,"data":{"system_name":"a","checkin_enabled":false,"turnstile_check":false}}`,
			want:   CheckinMethodDisabled,
		},
		{
			// Older builds do not publish the switch. Claiming "disabled" would
			// skip a check-in that could have worked, so stay unknown.
			name:   "missing switch stays unknown",
			status: `{"success":true,"data":{"system_name":"a","turnstile_check":true}}`,
			want:   CheckinMethodUnknown,
		},
		{
			name:   "string encoded switches are accepted",
			status: `{"success":true,"data":{"checkin_enabled":"true","turnstile_check":"true"}}`,
			want:   CheckinMethodTurnstile,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/status" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.status))
			}))
			defer server.Close()

			adapter := NewNewAPI(ClientFactory{AllowPrivate: true})
			method, err := adapter.DiscoverCheckin(context.Background(), AccountRequest{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if method.Status != tt.want || method.TurnstileSiteKey != tt.key || method.Source != "api_status" {
				t.Fatalf("method=%+v, want status=%s key=%q", method, tt.want, tt.key)
			}
		})
	}
}

func TestDiscoverCheckinFailsWhenStatusEndpointIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer server.Close()

	adapter := NewNewAPI(ClientFactory{AllowPrivate: true})
	if _, err := adapter.DiscoverCheckin(context.Background(), AccountRequest{BaseURL: server.URL}); err == nil {
		t.Fatal("an unreachable status endpoint must surface an error, not a guessed method")
	}
}

func TestVeloeraAndAnyRouterDelegateCheckinDiscovery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"system_name":"veloera","checkin_enabled":true,"turnstile_check":true,"turnstile_site_key":"0xV"}}`))
	}))
	defer server.Close()

	factory := ClientFactory{AllowPrivate: true}
	for name, adapter := range map[string]CheckinMethodProvider{
		"veloera":   NewVeloera(factory),
		"anyrouter": NewAnyRouter(factory),
	} {
		t.Run(name, func(t *testing.T) {
			method, err := adapter.DiscoverCheckin(context.Background(), AccountRequest{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			if method.Status != CheckinMethodTurnstile || method.TurnstileSiteKey != "0xV" {
				t.Fatalf("method=%+v", method)
			}
		})
	}
}
