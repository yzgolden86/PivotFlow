package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A rejected routing key must not be reported as "the site cannot do this".
// elysiver.h-e.top returns 401 Invalid token from /v1/models for a bad key; the
// old code swallowed that and answered CodeUnsupported, which sent the operator
// looking at the site instead of at the key.
func TestListModelsPreservesRejectedKeyInsteadOfReportingUnsupported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"","message":"Invalid token","type":"new_api_error"}}`))
	}))
	defer server.Close()

	adapter := NewNewAPI(ClientFactory{AllowPrivate: true})
	_, err := adapter.ListModels(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: Credentials{APIKey: "sk-shared"}})
	if ErrorCode(err) != CodeExpired {
		t.Fatalf("error=%v code=%q, want %q: a rejected key is a credential problem", err, ErrorCode(err), CodeExpired)
	}
}

// The legitimate "cannot enumerate" case must survive the fix: the endpoint
// answered successfully but listed nothing, and there is no session credential
// to fall back on.
func TestListModelsStillReportsUnsupportedWhenEndpointListsNothing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()

	adapter := NewNewAPI(ClientFactory{AllowPrivate: true})
	_, err := adapter.ListModels(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: Credentials{APIKey: "sk-only"}})
	if ErrorCode(err) != CodeUnsupported {
		t.Fatalf("error=%v code=%q, want %q", err, ErrorCode(err), CodeUnsupported)
	}
}

// With a session credential the management endpoint is still the fallback when
// the key endpoint answers but lists nothing.
func TestListModelsFallsBackToManagementEndpointWhenKeyEndpointIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "/api/user/models":
			_, _ = w.Write([]byte(`{"success":true,"data":["gpt-5","claude-sonnet"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewNewAPI(ClientFactory{AllowPrivate: true})
	models, err := adapter.ListModels(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Model != "gpt-5" || models[0].Source != "models_endpoint" {
		t.Fatalf("models=%+v", models)
	}
}
