package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/antigravityauth"
	"github.com/yzgolden86/PivotFlow/internal/codexauth"
)

const upstreamRedirectRequestBody = `{"prompt":"private request body"}`

type upstreamRedirectObservation struct {
	method        string
	authorization string
	apiKey        string
	googleKey     string
	body          string
}

func newSharedUpstreamRedirectTestClient(t *testing.T) *http.Client {
	t.Helper()
	client := newUpstreamHTTPClient(&http.Transport{Proxy: nil}, 0)
	t.Cleanup(func() { closeUpstreamHTTPClient(client) })
	return client
}

func newUpstreamRedirectRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, target, strings.NewReader(upstreamRedirectRequestBody))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	request.Header.Set("Authorization", "Bearer direct-secret")
	request.Header.Set("X-Api-Key", "nonstandard-api-key")
	request.Header.Set("X-Goog-Api-Key", "google-api-key")
	request.Header.Set("OpenAI-Beta", "responses=experimental")
	return request
}

func closeUpstreamRedirectResponse(response *http.Response) {
	if response == nil || response.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
}

func TestSharedUpstreamClientFollowsSameOriginRedirects(t *testing.T) {
	tests := []struct {
		status     int
		wantMethod string
		wantBody   string
	}{
		{status: http.StatusMovedPermanently, wantMethod: http.MethodGet},
		{status: http.StatusFound, wantMethod: http.MethodGet},
		{status: http.StatusTemporaryRedirect, wantMethod: http.MethodPost, wantBody: upstreamRedirectRequestBody},
		{status: http.StatusPermanentRedirect, wantMethod: http.MethodPost, wantBody: upstreamRedirectRequestBody},
	}

	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			var observation upstreamRedirectObservation
			var observationMu sync.Mutex
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/redirect" {
					http.Redirect(w, request, server.URL+"/target", test.status)
					return
				}
				body, err := io.ReadAll(request.Body)
				if err != nil {
					t.Errorf("read target body: %v", err)
				}
				observationMu.Lock()
				observation = upstreamRedirectObservation{
					method:        request.Method,
					authorization: request.Header.Get("Authorization"),
					apiKey:        request.Header.Get("X-Api-Key"),
					googleKey:     request.Header.Get("X-Goog-Api-Key"),
					body:          string(body),
				}
				observationMu.Unlock()
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(server.Close)

			client := newSharedUpstreamRedirectTestClient(t)
			response, err := client.Do(newUpstreamRedirectRequest(t, server.URL+"/redirect"))
			closeUpstreamRedirectResponse(response)
			if err != nil {
				t.Fatalf("same-origin redirect failed: %v", err)
			}

			observationMu.Lock()
			got := observation
			observationMu.Unlock()
			if got.method != test.wantMethod {
				t.Errorf("redirected method = %q, want %q", got.method, test.wantMethod)
			}
			if got.authorization != "Bearer direct-secret" ||
				got.apiKey != "nonstandard-api-key" ||
				got.googleKey != "google-api-key" {
				t.Errorf("redirected auth headers = %+v", got)
			}
			if got.body != test.wantBody {
				t.Errorf("redirected body = %q, want %q", got.body, test.wantBody)
			}
		})
	}
}

func TestSharedUpstreamClientRejectsForeignRedirectBeforeSendingSecrets(t *testing.T) {
	tests := []int{
		http.StatusMovedPermanently,
		http.StatusFound,
		http.StatusTemporaryRedirect,
		http.StatusPermanentRedirect,
	}

	for _, status := range tests {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var foreignHits atomic.Int32
			var mu sync.Mutex
			var observation upstreamRedirectObservation

			foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				foreignHits.Add(1)
				body, _ := io.ReadAll(request.Body)
				mu.Lock()
				observation = upstreamRedirectObservation{
					method:        request.Method,
					authorization: request.Header.Get("Authorization"),
					apiKey:        request.Header.Get("X-Api-Key"),
					googleKey:     request.Header.Get("X-Goog-Api-Key"),
					body:          string(body),
				}
				mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(foreign.Close)

			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				http.Redirect(w, request, foreign.URL+"/steal", status)
			}))
			t.Cleanup(origin.Close)

			client := newSharedUpstreamRedirectTestClient(t)
			response, err := client.Do(newUpstreamRedirectRequest(t, origin.URL+"/redirect"))
			closeUpstreamRedirectResponse(response)
			if err == nil {
				t.Fatal("foreign redirect succeeded, want redirect-policy error")
			}
			if hits := foreignHits.Load(); hits != 0 {
				mu.Lock()
				got := observation
				mu.Unlock()
				t.Fatalf("foreign target received %d request(s): %+v", hits, got)
			}
		})
	}
}

func TestSharedUpstreamClientAllowsTenRedirectHops(t *testing.T) {
	var hits atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		hits.Add(1)
		http.Redirect(w, request, server.URL+"/again", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)

	client := newSharedUpstreamRedirectTestClient(t)
	response, err := client.Do(newUpstreamRedirectRequest(t, server.URL+"/start"))
	closeUpstreamRedirectResponse(response)
	if err == nil {
		t.Fatal("unbounded same-origin redirect loop succeeded")
	}
	if got := hits.Load(); got != 11 {
		t.Fatalf("server received %d requests, want 11 (initial request plus ten followed redirects)", got)
	}
}

func TestOAuthTokenServicesUseSharedUpstreamRedirectPolicy(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(*http.Client, string) error
	}{
		{
			name: "Codex",
			invoke: func(client *http.Client, tokenURL string) error {
				service := codexauth.NewService(client)
				service.TokenURL = tokenURL
				_, err := service.Refresh(context.Background(), "codex-refresh-token")
				return err
			},
		},
		{
			name: "Antigravity",
			invoke: func(client *http.Client, tokenURL string) error {
				service := antigravityauth.NewService(client)
				service.TokenURL = tokenURL
				service.ClientID = "client-id"
				service.ClientSecret = "client-secret"
				_, err := service.Refresh(context.Background(), "antigravity-refresh-token")
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var foreignHits atomic.Int32
			var foreignMu sync.Mutex
			var foreignBody string
			foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				foreignHits.Add(1)
				body, _ := io.ReadAll(request.Body)
				foreignMu.Lock()
				foreignBody = string(body)
				foreignMu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{}`)
			}))
			t.Cleanup(foreign.Close)

			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				http.Redirect(w, request, foreign.URL+"/token", http.StatusTemporaryRedirect)
			}))
			t.Cleanup(origin.Close)

			client := newSharedUpstreamRedirectTestClient(t)
			err := test.invoke(client, origin.URL+"/token")
			if err == nil {
				t.Fatal("OAuth token exchange error = nil, want redirect-policy error")
			}
			if hits := foreignHits.Load(); hits != 0 {
				foreignMu.Lock()
				defer foreignMu.Unlock()
				t.Fatalf("foreign token endpoint received %d request(s), body=%q", hits, foreignBody)
			}
		})
	}
}
