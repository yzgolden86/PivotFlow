package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestModelListCompleteness(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		complete   bool
	}{
		{"plain empty", `{"data":[]}`, true},
		{"complete empty", `{"data":[],"total":0,"has_more":false,"page":1,"page_size":20,"total_pages":0,"next":null}`, true},
		{"complete nonempty", `{"data":[{"id":"model-a"}],"total":"1","page":1,"page_size":1,"total_pages":1}`, true},
		{"more models", `{"data":[],"total":1,"has_more":true}`, false},
		{"total without continuation", `{"data":[],"total":1}`, false},
		{"inconsistent nonempty total", `{"data":[{"id":"model-a"}],"total":0}`, false},
		{"partial nonempty", `{"data":[{"id":"model-a"}],"total":2}`, false},
		{"partial string collection", `{"data":["model-a"],"has_more":true}`, false},
		{"has more only", `{"data":[],"has_more":true}`, false},
		{"has next page", `{"data":[],"pagination":{"has_next_page":true}}`, false},
		{"invalid total", `{"data":[],"total":null}`, false},
		{"fractional total", `{"data":[],"total":0.5}`, false},
		{"invalid has more", `{"data":[],"has_more":null}`, false},
		{"next page", `{"data":[],"next_page":2}`, false},
		{"next URL", `{"data":[],"next":"/v1/models?page=2"}`, false},
		{"next page URL", `{"data":[],"next_page_url":"/v1/models?page=2"}`, false},
		{"next cursor", `{"data":[],"next_cursor":"opaque"}`, false},
		{"next token", `{"data":[],"next_page_token":"opaque"}`, false},
		{"later page", `{"data":[],"page":2}`, false},
		{"later current page", `{"data":[],"current_page":2}`, false},
		{"later zero based page", `{"data":[],"p":1}`, false},
		{"offset", `{"data":[],"offset":10}`, false},
		{"multiple pages", `{"data":[],"total_pages":2}`, false},
		{"zero size", `{"data":[],"page_size":0}`, false},
		{"overflow size", `{"data":[{"id":"a"},{"id":"b"}],"page_size":1}`, false},
		{"nested pagination", `{"data":[],"pagination":{"total":1,"has_more":true}}`, false},
		{"nested meta", `{"data":[],"meta":{"pagination":{"total":1}}}`, false},
		{"nested links", `{"data":[],"links":{"next":"/models?after=a"}}`, false},
		{"complete nested meta", `{"data":[],"meta":{"pagination":{"total":0,"has_more":false,"next_page":null}}}`, true},
	} {
		for _, kind := range []string{"newapi key", "openai key", "newapi session", "management fallback", "management scoped"} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				var managementCalls atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					if r.URL.Path == "/v1/models" {
						if kind == "management fallback" {
							http.NotFound(w, r)
							return
						}
						_, _ = w.Write([]byte(tc.body))
						return
					}
					if r.URL.Path == "/api/user/models" {
						managementCalls.Add(1)
						body := tc.body
						if kind == "newapi session" {
							body = `{"data":[]}` // Must not mask an incomplete API result.
						}
						_, _ = w.Write([]byte(strings.Replace(body, `{"data":`, `{"success":true,"data":`, 1)))
						return
					}
					http.NotFound(w, r)
				}))
				defer server.Close()
				adapter := NewNewAPI(ClientFactory{AllowPrivate: true})
				req := AccountRequest{BaseURL: server.URL, Credentials: Credentials{APIKey: "completeness-key"}}
				var models []ModelSnapshot
				var err error
				switch kind {
				case "openai key":
					models, err = NewOpenAICompatible(ClientFactory{AllowPrivate: true}).ListModels(context.Background(), req)
				case "newapi session", "management fallback":
					req.Credentials = Credentials{AccessToken: "completeness-session"}
					models, err = adapter.ListModels(context.Background(), req)
				case "management scoped":
					req.Credentials = Credentials{AccessToken: "completeness-session"}
					models, err = adapter.ListModelsForRoutingKey(context.Background(), req, RoutingKeySnapshot{Group: "scoped"})
				default:
					models, err = adapter.ListModels(context.Background(), req)
				}
				if tc.complete {
					if err != nil {
						t.Fatalf("valid complete response rejected: %v", err)
					}
				} else {
					if err == nil || ErrorCode(err) != CodeInvalidResponse || len(models) != 0 {
						t.Fatalf("partial discovery models=%v error=%v, want invalid_response without authoritative snapshot", models, err)
					}
					if kind == "newapi session" && managementCalls.Load() != 0 {
						t.Fatal("explicit incomplete API response was reinterpreted through management fallback")
					}
				}
			})
		}
	}
}

func TestModelListCompletenessManagementWrappedLists(t *testing.T) {
	for _, tc := range []struct {
		body string
		bad  bool
	}{
		{`{"success":true,"data":{"items":["model-a"],"total":2}}`, true},
		{`{"success":true,"data":{"models":["model-a"],"pagination":{"next_page":2}}}`, true},
		{`{"success":true,"data":{"models":["model-a"],"total":1,"page":1,"page_size":1}}`, false},
		{`{"success":true,"data":{"model-a":1000}}`, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			models, err := NewNewAPI(ClientFactory{AllowPrivate: true}).ListModelsForRoutingKey(context.Background(),
				AccountRequest{BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"}}, RoutingKeySnapshot{Group: "scoped"})
			if tc.bad && (err == nil || ErrorCode(err) != CodeInvalidResponse) || !tc.bad && (err != nil || len(models) != 1 || models[0].Model != "model-a") {
				t.Fatalf("models=%v error=%v, want incomplete=%t", models, err, tc.bad)
			}
		})
	}
}
