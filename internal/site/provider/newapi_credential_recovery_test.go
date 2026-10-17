package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// newAPILoginPage is what a New API deployment serves when the session cookie it
// was handed no longer maps to a user: HTML, HTTP 200, and no WAF markers at
// all. It is indistinguishable from a challenge page to the detector, which is
// why a stored cookie that had quietly expired used to be reported as "browser
// verification is required" and never recovered - every request carried the dead
// cookie and no variant ever presented the access token on its own.
const newAPILoginPage = `<!doctype html><html><head><title>登录</title></head><body><form action="/login"></form></body></html>`

// /api/user/self authorises and answers in the same round trip, so a credential
// that already works must cost one request. The verification call that used to
// run first repeated this exact request; on a WAF-fronted site every extra
// request is another chance to be challenged, and the challenge is sticky once
// it fires.
func TestNewAPIRefreshAccountCostsOneRequestWhenTheCredentialIsGood(t *testing.T) {
	var requests int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":5,"username":"demo","quota":1000000}}`))
	}))
	t.Cleanup(server.Close)

	snapshot, err := NewNewAPI(ClientFactory{AllowPrivate: true}).RefreshAccount(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "token", UserID: 5},
	})
	if err != nil || snapshot.Balance == nil || *snapshot.Balance != 2 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if got := atomic.LoadInt64(&requests); got != 1 {
		t.Fatalf("requests=%d, want 1", got)
	}
}

// Without a stored cookie no credential variant can change the answer to a WAF
// challenge, so the challenge must cost exactly one request. Retrying it is what
// kept the block alive: the site counts the retries as more traffic.
func TestNewAPIRefreshAccountDoesNotRetryAChallenge(t *testing.T) {
	var requests int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&requests, 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(aliyunWAFPage))
	}))
	t.Cleanup(server.Close)

	_, err := NewNewAPI(ClientFactory{AllowPrivate: true}).RefreshAccount(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "token", UserID: 5},
	})
	if got := ErrorCode(err); got != CodeBrowserRequired {
		t.Fatalf("error code=%q, want %q (err=%v)", got, CodeBrowserRequired, err)
	}
	if got := atomic.LoadInt64(&requests); got != 1 {
		t.Fatalf("requests=%d, want 1", got)
	}
}

func TestNewAPIManagementCredentialVariantsKeepTheTokenOnlyForm(t *testing.T) {
	variants := managementCredentialVariants(Credentials{AccessToken: "token", Cookie: "session=stale", UserID: 5})
	if len(variants) != 2 {
		t.Fatalf("variants=%+v, want the stored form plus a token-only form", variants)
	}
	if variants[0].Cookie != "session=stale" || variants[0].AccessToken != "token" {
		t.Fatalf("first variant=%+v, want the stored credential tried first", variants[0])
	}
	if variants[1].Cookie != "" || variants[1].AccessToken != "token" || variants[1].UserID != 5 {
		t.Fatalf("token-only variant=%+v", variants[1])
	}
}

// The recovery path end to end: a dead cookie is dropped, the token is used, and
// the credentials handed back do not restore the cookie that had just failed.
func TestNewAPIExpiredCookieFallsBackToTheAccessToken(t *testing.T) {
	var requests int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requests, 1)
		if r.Header.Get("Cookie") != "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(newAPILoginPage))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":5,"username":"demo","quota":1000000}}`))
	}))
	t.Cleanup(server.Close)

	adapter := NewNewAPI(ClientFactory{AllowPrivate: true})
	stored := Credentials{AccessToken: "token", Cookie: "session=stale", UserID: 5}

	credentials, err := adapter.ResolveManagementCredentials(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: stored})
	if err != nil {
		t.Fatalf("resolve err=%v", err)
	}
	// mergeResolvedCredentials restores a field the verify call did not echo
	// back, which would put the dead cookie straight back into the result.
	if credentials.Cookie != "" {
		t.Fatalf("credentials=%+v, want the rejected cookie dropped", credentials)
	}
	if credentials.AccessToken != "token" {
		t.Fatalf("credentials=%+v, want the access token preserved", credentials)
	}

	atomic.StoreInt64(&requests, 0)
	snapshot, err := adapter.RefreshAccount(context.Background(), AccountRequest{BaseURL: server.URL, Credentials: stored})
	if err != nil || snapshot.Balance == nil || *snapshot.Balance != 2 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	// 1 direct + 2 variants + 1 retry. The point is that it is bounded: a
	// fallback that keeps trying variants would multiply the load on the very
	// site that is already refusing us.
	if got := atomic.LoadInt64(&requests); got > 4 {
		t.Fatalf("requests=%d, want at most 4", got)
	}
}
