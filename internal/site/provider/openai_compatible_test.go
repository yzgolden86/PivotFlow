package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAICompatibleDetectsJSONAuthenticationResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Invalid API key","type":"invalid_request_error"}}`))
	}))
	defer server.Close()

	result, err := NewOpenAICompatible(ClientFactory{AllowPrivate: true}).Detect(context.Background(), server.URL)
	if err != nil || !result.Matched || result.ProviderID != "openai-compatible" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestOpenAICompatibleListModelsUsesKeyAndNormalizesNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/gateway/v1/models" || r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatalf("request=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"},{"model":"models/claude-sonnet"},{"name":"gpt-5"},"gemini-2.5"]}`))
	}))
	defer server.Close()

	models, err := NewOpenAICompatible(ClientFactory{AllowPrivate: true}).ListModels(context.Background(), AccountRequest{
		BaseURL:     server.URL + "/gateway",
		Credentials: Credentials{APIKey: "sk-test"},
	})
	if err != nil || len(models) != 3 || models[0].Model != "gpt-5" || models[1].Model != "claude-sonnet" || models[2].Model != "gemini-2.5" {
		t.Fatalf("models=%+v err=%v", models, err)
	}
}

func TestOpenAICompatibleRejectsHTMLChallengeDuringDetection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`<html>verification required</html>`))
	}))
	defer server.Close()

	result, err := NewOpenAICompatible(ClientFactory{AllowPrivate: true}).Detect(context.Background(), server.URL)
	if err != nil || result.Matched {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestOpenAICompatibleMapsRejectedKeyToExpired(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer server.Close()

	_, err := NewOpenAICompatible(ClientFactory{AllowPrivate: true}).ListModels(context.Background(), AccountRequest{
		BaseURL:     server.URL,
		Credentials: Credentials{APIKey: "sk-bad"},
	})
	if ErrorCode(err) != CodeExpired {
		t.Fatalf("err=%v code=%q", err, ErrorCode(err))
	}
}

func TestRegistryPrefersManagementProviderOverOpenAICompatibleFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/status":
			_, _ = w.Write([]byte(`{"success":true,"data":{"system_name":"Managed upstream"}}`))
		case "/v1/models":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"api key required"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	registry := NewRegistry(
		NewNewAPI(ClientFactory{AllowPrivate: true}),
		NewOpenAICompatible(ClientFactory{AllowPrivate: true}),
	)
	result, err := registry.Detect(context.Background(), server.URL)
	if err != nil || !result.Matched || result.ProviderID != "new-api-family" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
