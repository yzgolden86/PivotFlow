package provider

import (
	"context"
	"net/http"
	"testing"
)

// Compile-time guarantee that veloera-platform sites take the site-pricing
// path instead of being silently unpriced.
var _ PricingProvider = (*Veloera)(nil)

// Veloera delegates pricing to the New API family layout; verify the wiring
// end-to-end. Veloera is a New API fork, so /api/pricing keeps the same shape
// and auth (applyAuth sends the Veloera-User header variant too).
func TestVeloeraFetchPricingDelegatesToNewAPILayout(t *testing.T) {
	body := `{
	  "success": true,
	  "group_ratio": {"default": 1, "svip": 0.25},
	  "data": [
	    {"model_name":"glm-5.3","quota_type":0,"model_ratio":3,"completion_ratio":1,
	     "cache_ratio":0.1,"cache_creation_ratio":1.25,"enable_groups":["default","svip"]}
	  ]
	}`
	server := pricingTestServer(t, body, http.StatusOK)

	veloera := NewVeloera(ClientFactory{AllowPrivate: true})
	pricing, err := veloera.FetchPricing(context.Background(), pricingRequest(server.URL))
	if err != nil {
		t.Fatalf("FetchPricing: %v", err)
	}
	if len(pricing.Models) != 1 {
		t.Fatalf("len(Models)=%d, want 1", len(pricing.Models))
	}
	model := pricing.Models[0]
	almost(t, model.ModelRatio, 3, "ModelRatio")
	almost(t, model.CacheRatio, 0.1, "CacheRatio")
	input, output, cacheRead, cacheWrite := model.USDPerMillion(pricing.RatioFor("default"))
	almost(t, input, 6, "input USD/M")
	almost(t, output, 6, "output USD/M")
	almost(t, cacheRead, 0.6, "cache read USD/M")
	almost(t, cacheWrite, 7.5, "cache write USD/M")
	svipInput, _, _, _ := model.USDPerMillion(pricing.RatioFor("svip"))
	almost(t, svipInput, 1.5, "svip input USD/M")

	// Without a management session the family contract applies: refuse rather
	// than send a request that would fail confusingly.
	_, err = veloera.FetchPricing(context.Background(), AccountRequest{
		BaseURL:     server.URL,
		Credentials: Credentials{APIKey: "sk-only"},
	})
	if ErrorCode(err) != CodeUnsupported {
		t.Fatalf("code=%q, want %q", ErrorCode(err), CodeUnsupported)
	}
}
