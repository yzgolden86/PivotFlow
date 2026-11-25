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

// An explicit successful model array with no entries is an authoritative
// empty list; a missing collection remains unsupported.
func TestListModelsAcceptsAuthoritativeEmptyArray(t *testing.T) {
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
	models, err := adapter.ListModels(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: Credentials{APIKey: "sk-only"}})
	if err != nil || len(models) != 0 {
		t.Fatalf("models=%+v err=%v, want authoritative empty list", models, err)
	}
}

func TestListModelsFallsBackToManagementEndpointForManagementCredential(t *testing.T) {
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
	if err != nil || len(models) != 2 || models[0].Model != "gpt-5" {
		t.Fatalf("models=%+v err=%v, want management fallback", models, err)
	}
}

func TestListModelsDoesNotTreatMissingOrMalformedCollectionsAsEmpty(t *testing.T) {
	for _, body := range []string{`{"success":true}`, `{"data":null}`, `{"data":{}}`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			_, err := NewNewAPI(ClientFactory{AllowPrivate: true}).ListModels(context.Background(), AccountRequest{
				BaseURL: server.URL, Credentials: Credentials{APIKey: "sk-only"},
			})
			if ErrorCode(err) != CodeUnsupported && ErrorCode(err) != CodeInvalidResponse {
				t.Fatalf("error=%v code=%q, want unsupported or invalid response", err, ErrorCode(err))
			}
		})
	}
}

func TestListModelsForRoutingKeyAcceptsExplicitSuccessfulEmptyGroup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/models" || r.URL.Query().Get("group") != "empty" {
			t.Fatalf("request=%s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
	}))
	defer server.Close()

	models, err := NewNewAPI(ClientFactory{AllowPrivate: true}).ListModelsForRoutingKey(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"},
	}, RoutingKeySnapshot{Group: "empty", Key: "sk-empty"})
	if err != nil || len(models) != 0 {
		t.Fatalf("models=%+v err=%v, want authoritative empty scoped group", models, err)
	}
}
