package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestAnyRouterCheckinContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/sign_in" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Requested-With") != "XMLHttpRequest" || r.Header.Get("New-API-User") != "42" || r.Header.Get("Cookie") != "session=ok" {
			t.Fatalf("unexpected AnyRouter headers: %#v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"message":"签到成功"}`))
	}))
	defer server.Close()

	adapter := NewAnyRouter(ClientFactory{AllowPrivate: true})
	result, err := adapter.Checkin(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: Credentials{Cookie: "session=ok", UserID: 42}})
	if err != nil || result.Status != CheckinSuccess {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestAnyRouterCheckinUsesBearerThenFallsBackToLegacyCookie(t *testing.T) {
	t.Run("bearer", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/user/checkin" || r.Method != http.MethodPost {
				http.NotFound(w, r)
				return
			}
			if r.Header.Get("Authorization") != "Bearer access-token" || r.Header.Get("Cookie") != "" {
				t.Fatalf("unexpected bearer headers: %#v", r.Header)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"success":true,"message":"checkin success","data":{"reward":"2.5"}}`))
		}))
		defer server.Close()

		result, err := NewAnyRouter(ClientFactory{AllowPrivate: true}).Checkin(context.Background(), AccountRequest{
			BaseURL: server.URL, Credentials: Credentials{AccessToken: "access-token", UserID: 42},
		})
		if err != nil || result.Status != CheckinSuccess || result.RewardText != "2.5" {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})

	t.Run("legacy_cookie_fallback", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/user/checkin":
				http.NotFound(w, r)
			case "/api/user/sign_in":
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "session=legacy-token" || r.Header.Get("New-API-User") != "42" {
					t.Fatalf("unexpected fallback headers: %#v", r.Header)
				}
				_, _ = w.Write([]byte(`{"success":true,"message":"签到成功"}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer server.Close()

		result, err := NewAnyRouter(ClientFactory{AllowPrivate: true}).Checkin(context.Background(), AccountRequest{
			BaseURL: server.URL, Credentials: Credentials{AccessToken: "legacy-token", UserID: 42},
		})
		if err != nil || result.Status != CheckinSuccess {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
}

func TestVeloeraUsesDedicatedCheckinAndBalanceContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Veloera-User") != "7" || r.Header.Get("User-id") != "7" {
			t.Fatalf("missing Veloera compatibility headers: %#v", r.Header)
		}
		switch r.URL.Path {
		case "/api/user/self":
			_, _ = w.Write([]byte(`{"success":true,"data":{"username":"demo","quota":9000000,"used_quota":2000000}}`))
		case "/api/user/check_in":
			_, _ = w.Write([]byte(`{"success":true,"message":"ok","data":{"reward":"1"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewVeloera(ClientFactory{AllowPrivate: true})
	req := AccountRequest{BaseURL: server.URL, Credentials: Credentials{AccessToken: "jwt", UserID: 7}}
	snapshot, err := adapter.RefreshAccount(context.Background(), req)
	if err != nil || snapshot.Balance == nil || *snapshot.Balance != 7 || snapshot.Currency != "USD" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	result, err := adapter.Checkin(context.Background(), req)
	if err != nil || result.Status != CheckinSuccess || result.RewardText != "1" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestSub2APIJWTBalanceModelFallbackAndAnnouncements(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		auth := r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/v1/auth/me":
			if auth == "" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"UNAUTHORIZED","message":"Authorization header is required"}`))
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"id":9,"username":"sub-user","balance":"12.5"}}`))
		case "/api/v1/keys":
			_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"items":[{"id":3,"key":"sk-user","status":"active"}]}}`))
		case "/v1/models":
			if auth != "Bearer sk-user" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"API_KEY_REQUIRED","message":"api key is required"}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"},{"id":"models/claude-sonnet"}]}`))
		case "/api/v1/announcements":
			_, _ = w.Write([]byte(`{"code":0,"message":"ok","data":{"items":[{"id":11,"title":"Maintenance","content":"Tonight","updated_at":"2026-08-10T02:00:00Z"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewSub2API(ClientFactory{AllowPrivate: true})
	detected, err := adapter.Detect(context.Background(), server.URL)
	if err != nil || !detected.Matched || detected.ProviderID != model.SitePlatformSub2API {
		t.Fatalf("detected=%+v err=%v", detected, err)
	}
	req := AccountRequest{BaseURL: server.URL, Credentials: Credentials{AccessToken: "jwt"}}
	snapshot, err := adapter.RefreshAccount(context.Background(), req)
	if err != nil || snapshot.Balance == nil || *snapshot.Balance != 12.5 || snapshot.Currency != "USD" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	models, err := adapter.ListModels(context.Background(), req)
	if err != nil || len(models) != 2 || models[1].Model != "claude-sonnet" {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	announcements, err := adapter.ListAnnouncements(context.Background(), req)
	if err != nil || len(announcements) != 1 || announcements[0].SourceKey != "announcement:11" {
		t.Fatalf("announcements=%+v err=%v", announcements, err)
	}
	checkin, err := adapter.Checkin(context.Background(), req)
	if ErrorCode(err) != CodeUnsupported || checkin.Status != CheckinUnsupported {
		t.Fatalf("checkin=%+v err=%v", checkin, err)
	}
}

func TestSub2APIParsesStringSuccessCodeAndCollectionVariants(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/keys":
			if r.Header.Get("Authorization") != "Bearer jwt-token" {
				t.Fatalf("unexpected key authorization: %q", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"code":"0","message":"ok","data":{"records":[{"apiKey":"sk-record","is_enabled":true}]}}`))
		case "/v1/models":
			if r.Header.Get("Authorization") != "Bearer sk-record" {
				t.Fatalf("unexpected model authorization: %q", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"data":{"rows":[{"modelName":"gpt-record"}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewSub2API(ClientFactory{AllowPrivate: true})
	keys, err := adapter.ListRoutingKeys(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: Credentials{AccessToken: "jwt-token"}})
	if err != nil || len(keys) != 1 || keys[0].Key != "sk-record" || !keys[0].Enabled {
		t.Fatalf("keys=%+v err=%v", keys, err)
	}
	models, err := adapter.ListModels(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: Credentials{APIKey: keys[0].Key}})
	if err != nil || len(models) != 1 || models[0].Model != "gpt-record" {
		t.Fatalf("models=%+v err=%v", models, err)
	}
}

func TestSub2APIRoutingKeyGroupObjectDoesNotSerializeMap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/keys" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"items":[{"id":8,"key":"Bearer sk-group","name":"token-8","group":{"id":8,"name":"付费兜底分组","platform":"anthropic","models":{"claude-sonnet":true,"gpt-5":true}},"status":"active"}]}}`))
	}))
	defer server.Close()

	keys, err := NewSub2API(ClientFactory{AllowPrivate: true}).ListRoutingKeys(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: Credentials{AccessToken: "jwt"}})
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys=%+v err=%v", keys, err)
	}
	if keys[0].Key != "sk-group" || keys[0].Group != "付费兜底分组" {
		t.Fatalf("unexpected normalized key=%+v", keys[0])
	}
	if strings.Contains(keys[0].Group, "map[") || len(keys[0].Models) != 2 || !slices.Equal(keys[0].Protocols, []string{"anthropic"}) {
		t.Fatalf("group object was not parsed safely: %+v", keys[0])
	}
}

func TestSub2APIRefreshCredentialsRotatesManagedTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/refresh" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["refresh_token"] != "refresh-old" {
			t.Fatalf("refresh body=%v err=%v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"access-new","refresh_token":"refresh-new","expires_in":3600}}`))
	}))
	defer server.Close()

	before := time.Now().UnixMilli()
	refreshed, err := NewSub2API(ClientFactory{AllowPrivate: true}).RefreshCredentials(context.Background(), AccountRequest{
		BaseURL:     server.URL,
		Credentials: Credentials{AccessToken: "access-old", RefreshToken: "refresh-old"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "access-new" || refreshed.RefreshToken != "refresh-new" {
		t.Fatalf("refreshed=%+v", refreshed)
	}
	if refreshed.ExpiresAt < before+3_500_000 || refreshed.ExpiresAt > before+3_700_000 {
		t.Fatalf("expires_at=%d before=%d", refreshed.ExpiresAt, before)
	}
}
