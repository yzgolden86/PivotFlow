package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func TestHandleSitePricingPreservesPerCallPriceValidity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/pricing" {
			t.Errorf("unexpected pricing request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true,"data":[
			{"model_name":"paid","quota_type":1,"model_price":0.07},
			{"model_name":"free","quota_type":1,"model_price":0},
			{"model_name":"object-free","quota_type":1,"model_price":{"input":0}},
			{"model_name":"missing","quota_type":1},
			{"model_name":"malformed","quota_type":1,"model_price":{"output":0.5}},
			{"model_name":"token","quota_type":0,"model_ratio":1}
		]}`)
	}))
	t.Cleanup(upstream.Close)
	adapter := provider.NewNewAPI(provider.ClientFactory{AllowPrivate: true})
	pricing, err := adapter.FetchPricing(context.Background(), provider.AccountRequest{
		BaseURL: upstream.URL, Credentials: provider.Credentials{AccessToken: "synthetic-management-token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pricing.Models = append(pricing.Models, provider.ModelPrice{Model: "legacy", QuotaType: 1})
	server := &Server{sitePricing: newSitePricingCache()}
	server.sitePricing.store(7, pricing, false, time.Now())
	request := newJSONRequest(t, http.MethodGet, "/admin/site-pricing?site_id=7", nil)
	c, recorder := newTestContext(t, request)
	server.handleSitePricing(c)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Models []struct {
				Model             string  `json:"model"`
				PerCallPrice      float64 `json:"per_call_price"`
				PerCallPriceValid *bool   `json:"per_call_price_valid"`
			} `json:"models"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	payload := response.Data
	if len(payload.Models) != 7 {
		t.Fatalf("models=%d, want 7", len(payload.Models))
	}
	for _, row := range payload.Models {
		if row.Model == "token" || row.Model == "legacy" {
			if row.PerCallPriceValid != nil {
				t.Errorf("%s row must omit per_call_price_valid", row.Model)
			}
			continue
		}
		valid := row.Model == "paid" || row.Model == "free" || row.Model == "object-free"
		if row.PerCallPriceValid == nil || *row.PerCallPriceValid != valid {
			t.Errorf("%s: per_call_price_valid=%v, want explicit %v", row.Model, row.PerCallPriceValid, valid)
		}
		want := 0.0
		if row.Model == "paid" {
			want = 0.07
		}
		if row.PerCallPrice != want {
			t.Errorf("%s: per_call_price=%v, want %v", row.Model, row.PerCallPrice, want)
		}
	}
	for _, price := range pricing.Models {
		switch price.Model {
		case "missing", "malformed":
			if _, ok := computeSiteCost(tokenPricing("default", price), price.Model, &fwResult{}); ok {
				t.Errorf("%s: unknown per-call price was accepted as a site-priced charge", price.Model)
			}
		case "free", "object-free", "legacy":
			if cost, ok := computeSiteCost(tokenPricing("default", price), price.Model, &fwResult{}); !ok || cost != 0 {
				t.Errorf("%s: known or legacy zero changed: cost=%v known=%v", price.Model, cost, ok)
			}
		}
	}
}
