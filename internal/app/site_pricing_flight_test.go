package app

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

// gatedSitePricingAdapter blocks each price read until the test releases it, so
// cancellation and invalidation can be injected while a shared flight is
// genuinely in progress.
type gatedSitePricingAdapter struct {
	checkinMethodTestAdapter
	calls   atomic.Int32
	started chan int
	release chan struct{}
}

func (a *gatedSitePricingAdapter) FetchPricing(ctx context.Context, _ provider.AccountRequest) (provider.SitePricing, error) {
	call := int(a.calls.Add(1))
	a.started <- call
	select {
	case <-ctx.Done():
		return provider.SitePricing{}, ctx.Err()
	case <-a.release:
		return provider.SitePricing{Models: []provider.ModelPrice{{Model: fmt.Sprintf("model-v%d", call)}}}, nil
	}
}

func newGatedPricingServer(t *testing.T, adapter *gatedSitePricingAdapter) (*Server, *model.Site, *sitePricingCache) {
	t.Helper()
	service, site, _ := newSiteRefreshTestService(t, adapter)
	var wg sync.WaitGroup
	service.wg = &wg
	cache := newSitePricingCache()
	cache.storeChannelBindings(map[int64]channelBinding{7001: {siteID: site.ID, group: "default"}}, time.Now())
	server := &Server{store: service.store, siteControl: service, sitePricing: cache}
	t.Cleanup(func() {
		service.stopTasks()
		wg.Wait()
	})
	return server, site, cache
}

// A caller that gives up must not turn the still-running shared read into a
// fifteen-minute negative cache entry. The flight owns the cache commit, and a
// cancellation is not evidence that the site has no pricing endpoint.
func TestSitePricingCancelledSoleWaiterDoesNotCacheFailure(t *testing.T) {
	adapter := &gatedSitePricingAdapter{started: make(chan int, 4), release: make(chan struct{}, 4)}
	server, site, cache := newGatedPricingServer(t, adapter)

	ctx, cancel := context.WithCancel(context.Background())
	resolved := make(chan bool, 1)
	go func() {
		_, ok := server.resolveChannelPricing(ctx, 7001)
		resolved <- ok
	}()

	select {
	case call := <-adapter.started:
		if call != 1 {
			t.Fatalf("first upstream call=%d, want 1", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pricing fetch did not start")
	}

	cancel()
	select {
	case ok := <-resolved:
		if ok {
			t.Fatal("cancelled waiter reported resolved pricing")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled waiter did not return")
	}

	adapter.release <- struct{}{}

	deadline := time.Now().Add(2 * time.Second)
	for {
		pricing, ok := cache.lookup(site.ID, time.Now())
		if ok && len(pricing.Models) == 1 && pricing.Models[0].Model == "model-v1" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("shared fetch did not commit its success after the waiter cancelled: resolved=%v models=%d", ok, len(pricing.Models))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Invalidating a site's pricing source while a read is in flight must retire
// that read. Its table can describe the old endpoint, so neither the waiter nor
// the cache may accept it; the waiter has to join a new generation instead.
func TestSitePricingInvalidationRejectsInFlightResult(t *testing.T) {
	adapter := &gatedSitePricingAdapter{started: make(chan int, 4), release: make(chan struct{}, 4)}
	server, site, cache := newGatedPricingServer(t, adapter)

	type result struct {
		pricing channelPricing
		ok      bool
	}
	resolved := make(chan result, 1)
	go func() {
		pricing, ok := server.resolveChannelPricing(context.Background(), 7001)
		resolved <- result{pricing: pricing, ok: ok}
	}()

	select {
	case call := <-adapter.started:
		if call != 1 {
			t.Fatalf("first upstream call=%d, want 1", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pricing fetch did not start")
	}

	cache.invalidateSite(site.ID)
	adapter.release <- struct{}{}

	select {
	case call := <-adapter.started:
		if call != 2 {
			t.Fatalf("replacement upstream call=%d, want 2", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("invalidation did not trigger a replacement fetch")
	}

	if pricing, ok := cache.lookup(site.ID, time.Now()); ok && len(pricing.Models) > 0 {
		t.Fatalf("in-flight result survived invalidation: %s", pricing.Models[0].Model)
	}

	adapter.release <- struct{}{}
	select {
	case got := <-resolved:
		if !got.ok || len(got.pricing.pricing.Models) != 1 || got.pricing.pricing.Models[0].Model != "model-v2" {
			t.Fatalf("resolved pricing=%+v ok=%v, want model-v2", got.pricing.pricing.Models, got.ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter did not resolve after the replacement fetch")
	}

	pricing, ok := cache.lookup(site.ID, time.Now())
	if !ok || len(pricing.Models) != 1 || pricing.Models[0].Model != "model-v2" {
		t.Fatalf("cached pricing=%+v ok=%v, want model-v2", pricing.Models, ok)
	}
}
