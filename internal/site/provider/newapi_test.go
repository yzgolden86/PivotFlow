package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewAPIRefreshModelsCheckinAndNotice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"username":"demo","quota":1000000}}`))
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4.1"},{"id":"claude-sonnet"}]}`))
		case "/api/user/checkin":
			_, _ = w.Write([]byte(`{"success":true,"message":"ok","data":{"reward":12}}`))
		case "/api/notice":
			_, _ = w.Write([]byte(`{"success":true,"data":"maintenance"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewNewAPI(ClientFactory{AllowPrivate: true})
	req := AccountRequest{BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"}}
	snapshot, err := adapter.RefreshAccount(context.Background(), req)
	if err != nil || snapshot.Balance == nil || *snapshot.Balance != 2 || snapshot.Currency != "USD" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	models, err := adapter.ListModels(context.Background(), req)
	if err != nil || len(models) != 2 || models[0].Model != "gpt-4.1" {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	checkin, err := adapter.Checkin(context.Background(), req)
	if err != nil || checkin.Status != CheckinSuccess || checkin.RewardText != "12" {
		t.Fatalf("checkin=%+v err=%v", checkin, err)
	}
	notices, err := adapter.ListAnnouncements(context.Background(), req)
	if err != nil || len(notices) != 1 || notices[0].ContentMarkdown != "maintenance" {
		t.Fatalf("notices=%+v err=%v", notices, err)
	}
}

func TestNewAPIChallengeAndRateLimitClassification(t *testing.T) {
	t.Run("challenge", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html>Cloudflare Turnstile</html>`))
		}))
		defer server.Close()
		adapter := NewNewAPI(ClientFactory{AllowPrivate: true})
		_, err := adapter.RefreshAccount(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: Credentials{AccessToken: "x"}})
		if ErrorCode(err) != CodeBrowserRequired {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("rate_limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer server.Close()
		adapter := NewNewAPI(ClientFactory{AllowPrivate: true})
		_, err := adapter.RefreshAccount(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: Credentials{AccessToken: "x"}})
		if ErrorCode(err) != CodeRateLimited {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestNewAPILoginAcceptsSessionCookieAndDiscoversRoutingKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/user/login":
			if r.Method != http.MethodPost || r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				t.Fatalf("unexpected login request: %s %#v", r.Method, r.Header)
			}
			w.Header().Add("Set-Cookie", "session=management-session; Path=/; HttpOnly")
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":42}}`))
		case "/api/token/":
			if r.Header.Get("Cookie") != "session=management-session" || r.Header.Get("New-API-User") != "42" {
				t.Fatalf("session was not forwarded: cookie=%q user=%q", r.Header.Get("Cookie"), r.Header.Get("New-API-User"))
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"name":"primary","key":"sk-route","status":1}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewNewAPI(ClientFactory{AllowPrivate: true})
	credentials, err := adapter.Login(context.Background(), LoginRequest{BaseURL: server.URL, Username: "demo", Password: "secret"})
	if err != nil {
		t.Fatalf("login failed: %v", err)
	}
	if credentials.Cookie != "session=management-session" || credentials.UserID != 42 || credentials.Password != "" {
		t.Fatalf("unexpected login credentials: %+v", credentials)
	}
	keys, err := adapter.ListRoutingKeys(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: credentials})
	if err != nil || len(keys) != 1 || keys[0].Key != "sk-route" {
		t.Fatalf("keys=%+v err=%v", keys, err)
	}
}

func TestNewAPIResolveManagementCredentialsDiscoversRequiredUserID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/user/self" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("New-API-User") != "7" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"New-API-User is required"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":7,"username":"demo"}}`))
	}))
	defer server.Close()

	adapter := NewNewAPI(ClientFactory{AllowPrivate: true})
	credentials, err := adapter.ResolveManagementCredentials(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: Credentials{AccessToken: "system-token"}})
	if err != nil || credentials.UserID != 7 || credentials.AccessToken != "system-token" {
		t.Fatalf("credentials=%+v err=%v", credentials, err)
	}
}

func TestNewAPIHTTP200CredentialErrorsKeepUsefulClassification(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{name: "invalid token", message: "Unauthorized, invalid access token", want: CodeExpired},
		{name: "user ID has priority", message: "New-API-User is required for this access token", want: CodeUserIDRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":false,"message":"` + tt.message + `"}`))
			}))
			defer server.Close()

			adapter := NewNewAPI(ClientFactory{AllowPrivate: true})
			_, err := adapter.RefreshAccount(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: Credentials{AccessToken: "bad", UserID: 42}})
			if ErrorCode(err) != tt.want {
				t.Fatalf("error=%v code=%q want=%q", err, ErrorCode(err), tt.want)
			}
			if ErrorMessage(err) != tt.message {
				t.Fatalf("message=%q want=%q", ErrorMessage(err), tt.message)
			}
		})
	}
}

func TestNewAPIApplyAuthSendsCompatibleUserIDHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.com/api/user/self", nil)
	applyAuth(req, Credentials{AccessToken: "system-token", UserID: 73})
	for _, name := range []string{"New-API-User", "Veloera-User", "voapi-user", "User-id", "X-User-Id", "Rix-Api-User", "neo-api-user"} {
		if got := req.Header.Get(name); got != "73" {
			t.Fatalf("header %s=%q want=73", name, got)
		}
	}
}
