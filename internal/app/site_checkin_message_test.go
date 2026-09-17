package app

import (
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

// A Turnstile site can only be closed out by a human, so the message tells the
// operator what to do. The second half of that sentence - "then the system will
// re-check" - is only true for an adapter that can report check-in status.
// Promising it elsewhere leaves the operator waiting on a reconciliation that
// is never scheduled: Veloera sat behind exactly that gap, and AnyRouter cannot
// close it at all because its upstream publishes no status endpoint.
func TestTurnstileMessagePromisesRecheckOnlyWhenAdapterCanReport(t *testing.T) {
	factory := provider.ClientFactory{AllowPrivate: true}
	cases := []struct {
		name        string
		adapter     provider.SiteAdapter
		wantRecheck bool
		description string
	}{
		{
			name:        "veloera answers its own status route",
			adapter:     provider.NewVeloera(factory),
			wantRecheck: true,
			description: "a status endpoint exists, so the re-check really runs",
		},
		{
			name:        "anyrouter has no status endpoint to call",
			adapter:     provider.NewAnyRouter(factory),
			wantRecheck: false,
			description: "the promise would never be kept",
		},
		{
			name:        "an adapter without the status interface",
			adapter:     struct{ provider.SiteAdapter }{provider.NewVeloera(factory)},
			wantRecheck: false,
			description: "only the capability, not the concrete type, decides",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			message := turnstileCheckinMessage(tt.adapter)
			if !strings.Contains(message, "请在浏览器完成签到") {
				t.Fatalf("message=%q, want the browser instruction for every adapter", message)
			}
			if got := strings.Contains(message, "由系统复核"); got != tt.wantRecheck {
				t.Fatalf("message=%q promises re-check=%v, want %v: %s", message, got, tt.wantRecheck, tt.description)
			}
		})
	}
}
