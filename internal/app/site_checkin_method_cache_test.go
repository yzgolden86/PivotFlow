package app

import (
	"context"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func TestCachedCheckinMethod(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	siteAt := func(method string, age time.Duration) *model.Site {
		return &model.Site{ID: 1, CheckinMethod: method, CheckinMethodCheckedAt: now.Add(-age).UnixMilli()}
	}
	cases := []struct {
		name string
		site *model.Site
		want bool
	}{
		{name: "nil site is never cached", site: nil, want: false},
		{name: "never discovered is not cached", site: &model.Site{ID: 1}, want: false},
		{
			name: "unknown is not a usable answer",
			site: siteAt(provider.CheckinMethodUnknown, time.Minute),
			want: false,
		},
		{
			name: "a fresh answer is cached",
			site: siteAt(provider.CheckinMethodTurnstile, time.Minute),
			want: true,
		},
		{
			// Not a discovery result — it is only ever written after a real
			// attempt came back 404. It has to be cached like any other answer,
			// because the skip that saves the request depends on this hit: a
			// whitelist of "expected" statuses that omitted it would quietly
			// turn the whole feature back off.
			name: "a route that turned out not to exist is cached",
			site: siteAt(provider.CheckinMethodUnavailable, time.Minute),
			want: true,
		},
		{
			name: "a missing timestamp cannot be trusted",
			site: &model.Site{ID: 1, CheckinMethod: provider.CheckinMethodAvailable},
			want: false,
		},
		{
			name: "an answer at the TTL boundary expires",
			site: siteAt(provider.CheckinMethodAvailable, siteCheckinMethodTTL),
			want: false,
		},
		{
			name: "an answer just inside the TTL is still cached",
			site: siteAt(provider.CheckinMethodAvailable, siteCheckinMethodTTL-time.Second),
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			method, ok := cachedCheckinMethod(tc.site, now)
			if ok != tc.want {
				t.Fatalf("cachedCheckinMethod(%+v)=%v, want %v", tc.site, ok, tc.want)
			}
			if ok && method.Status != tc.site.CheckinMethod {
				t.Fatalf("cached status=%q, want %q", method.Status, tc.site.CheckinMethod)
			}
		})
	}
}

// The capability belongs to the site, so the first check-in must discover and
// persist it, and every later check-in — including one on a different account —
// must read it back instead of asking the upstream again.
func TestCheckinMethodIsDiscoveredOncePerSite(t *testing.T) {
	adapter := &checkinMethodTestAdapter{method: provider.CheckinMethod{Status: provider.CheckinMethodAvailable, Source: "api_status"}}
	service, site, account := newSiteRefreshTestService(t, adapter)
	ctx := context.Background()

	runCheckinTask(t, service, site, account)
	if got := adapter.discoveries.Load(); got != 1 {
		t.Fatalf("discovery probes=%d after the first check-in, want 1", got)
	}
	stored, err := service.store.GetSite(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.CheckinMethod != provider.CheckinMethodAvailable || stored.CheckinMethodCheckedAt <= 0 {
		t.Fatalf("site check-in method=%q checkedAt=%d, want %q persisted with a timestamp", stored.CheckinMethod, stored.CheckinMethodCheckedAt, provider.CheckinMethodAvailable)
	}

	// A second check-in for the same site must reuse the stored answer.
	runCheckinTask(t, service, site, account)
	if got := adapter.discoveries.Load(); got != 1 {
		t.Fatalf("discovery probes=%d after a second check-in, want the cached answer to be reused", got)
	}
	if got := adapter.checkins.Load(); got != 2 {
		t.Fatalf("check-in POSTs=%d, want 2: caching the method must not skip the check-in", got)
	}
}

// A stale answer must be re-discovered rather than trusted forever: an operator
// can switch check-in on, or turn Turnstile on, at any time.
func TestCheckinMethodIsRediscoveredAfterTTL(t *testing.T) {
	adapter := &checkinMethodTestAdapter{method: provider.CheckinMethod{Status: provider.CheckinMethodTurnstile, Source: "api_status"}}
	service, site, account := newSiteRefreshTestService(t, adapter)
	ctx := context.Background()

	runCheckinTask(t, service, site, account)
	if got := adapter.discoveries.Load(); got != 1 {
		t.Fatalf("discovery probes=%d after the first check-in, want 1", got)
	}

	// Age the stored observation past the TTL.
	stale := time.Now().Add(-siteCheckinMethodTTL - time.Minute).UnixMilli()
	if err := service.store.UpdateSiteCheckinMethod(ctx, site.ID, provider.CheckinMethodTurnstile, stale); err != nil {
		t.Fatal(err)
	}
	runCheckinTask(t, service, site, account)
	if got := adapter.discoveries.Load(); got != 2 {
		t.Fatalf("discovery probes=%d after the TTL elapsed, want a fresh probe", got)
	}
}
