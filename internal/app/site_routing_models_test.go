package app

import (
	"context"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

// An unsupported key endpoint still needs management fallback. Unlike a valid
// empty array, the missing endpoint provides no authoritative model snapshot.
type routingModelsEmptyKeyAdapter struct {
	projectionTestAdapter
}

func (routingModelsEmptyKeyAdapter) ListModels(_ context.Context, req provider.AccountRequest) ([]provider.ModelSnapshot, error) {
	if req.Credentials.APIKey != "" {
		return nil, &provider.Error{Code: provider.CodeUnsupported, Message: "model endpoint unavailable"}
	}
	return nil, &provider.Error{Code: provider.CodeRateLimited, Message: "management credential was rate limited"}
}

func TestRoutingModelsReportsTheFallbackCauseWhenNothingElseExplainsIt(t *testing.T) {
	adapter := routingModelsEmptyKeyAdapter{}
	service, site, account := newSiteRefreshTestService(t, adapter)

	_, _, err := service.routingModels(context.Background(), account, site, adapter,
		provider.Credentials{AccessToken: "session-token"},
		[]provider.RoutingKeySnapshot{{ID: "k1", Name: "default", Key: "sk-single", Enabled: true}})
	if err == nil {
		t.Fatal("routingModels reported no error for a key whose models could not be discovered")
	}
	if code := provider.ErrorCode(err); code != provider.CodeRateLimited {
		t.Fatalf("error code=%q, want management fallback rate limit", code)
	}
	if !strings.Contains(err.Error(), "management credential was rate limited") {
		t.Fatalf("error=%q, want the upstream cause preserved", err.Error())
	}
}

// routingModelsKeyRejectedAdapter rejects every model request, which is the
// shape of a routing key whose credential is no longer accepted upstream.
type routingModelsKeyRejectedAdapter struct {
	projectionTestAdapter
}

func (routingModelsKeyRejectedAdapter) ListModels(context.Context, provider.AccountRequest) ([]provider.ModelSnapshot, error) {
	return nil, &provider.Error{Code: provider.CodeExpired, Message: "Invalid token"}
}

// A rejected routing key must reach the operator as a request failure carrying
// the upstream message. Reporting it as "expired" would send them to the
// management session, and reporting "unsupported" would tell them the site has
// no model endpoint at all — both are wrong and both were observed in the wild.
func TestRoutingModelsKeepsTheUpstreamCauseForARejectedKey(t *testing.T) {
	adapter := routingModelsKeyRejectedAdapter{}
	service, site, account := newSiteRefreshTestService(t, adapter)

	_, _, err := service.routingModels(context.Background(), account, site, adapter,
		provider.Credentials{AccessToken: "session-token"},
		[]provider.RoutingKeySnapshot{{ID: "k1", Name: "opus-shared", Key: "sk-rejected", Enabled: true}})
	if err == nil {
		t.Fatal("routingModels reported no error for a rejected routing key")
	}
	if code := provider.ErrorCode(err); code != provider.CodeRequestFailed {
		t.Fatalf("error code=%q, want %q", code, provider.CodeRequestFailed)
	}
	if !strings.Contains(err.Error(), "opus-shared") || !strings.Contains(err.Error(), "Invalid token") {
		t.Fatalf("error=%q, want both the routing key and the upstream message", err.Error())
	}
}
