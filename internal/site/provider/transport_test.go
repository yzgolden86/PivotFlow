package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/version"
)

// proxyStub is a minimal forward proxy: it answers every request itself instead
// of forwarding, which is enough to prove the client reached the proxy at all.
func proxyStub(t *testing.T, hits *int32) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"system_name":"via-proxy"}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func getThrough(t *testing.T, client *http.Client, target string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512))
	return string(body), err
}

// A proxy on a loopback address is the normal local setup (Clash / v2ray on
// 127.0.0.1) and exactly what a site's proxy field is for. The SSRF filter used
// to be applied to the proxy hop as well, so every such site failed with
// "provider host resolves only to private or unsafe addresses" — a message that
// named the site while inspecting the proxy.
func TestExplicitLoopbackProxyIsReachable(t *testing.T) {
	var hits int32
	proxy := proxyStub(t, &hits)

	client, err := ClientFactory{}.New(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Plain http so the stub needs no CONNECT handling.
	body, err := getThrough(t, client, "http://site.invalid/api/status")
	if err != nil {
		t.Fatalf("request through a loopback proxy failed: %v", err)
	}
	if hits != 1 {
		t.Fatalf("proxy hits=%d, want 1", hits)
	}
	if !strings.Contains(body, "via-proxy") {
		t.Fatalf("body=%q, want the proxy's answer", body)
	}
}

// The guard the filter exists for must survive: with no proxy, a site URL that
// resolves to a private address is still refused.
func TestDirectPrivateTargetIsStillRefused(t *testing.T) {
	var hits int32
	target := proxyStub(t, &hits)

	client, err := ClientFactory{}.New(DirectProxyURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := getThrough(t, client, target.URL); err == nil {
		t.Fatal("direct request to a loopback address succeeded, want the SSRF guard to refuse it")
	} else if !strings.Contains(err.Error(), "private or unsafe") {
		t.Fatalf("err=%v, want the private-address refusal", err)
	}
	if hits != 0 {
		t.Fatalf("target was reached %d times, want 0", hits)
	}
}

// AllowPrivate is the explicit opt-in that already existed for operators who
// really do point a site at a local address.
func TestAllowPrivateStillReachesPrivateTarget(t *testing.T) {
	var hits int32
	target := proxyStub(t, &hits)

	client, err := ClientFactory{AllowPrivate: true}.New(DirectProxyURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := getThrough(t, client, target.URL); err != nil {
		t.Fatalf("AllowPrivate request failed: %v", err)
	}
	if hits != 1 {
		t.Fatalf("target hits=%d, want 1", hits)
	}
}

// The tracker is what keeps the two cases apart; check it directly as well, so a
// future refactor of New cannot silently drop the distinction.
func TestProxyHopTrackerDistinguishesProxyFromSite(t *testing.T) {
	tracker := &proxyHopTracker{}
	tracker.remember("127.0.0.1")

	if !tracker.isProxyHop("127.0.0.1") {
		t.Fatal("a recorded proxy host was not recognised")
	}
	if tracker.isProxyHop("cun.ai") {
		t.Fatal("an unrecorded host was treated as a proxy hop")
	}
	// Host names are case-insensitive in DNS and must be treated so here too.
	tracker.remember("Proxy.Example")
	if !tracker.isProxyHop("proxy.example") {
		t.Fatal("proxy host matching is case-sensitive")
	}
}

// wrap must record whatever the selector picks, since env-configured proxies are
// only known at request time.
func TestProxyHopTrackerWrapRecordsSelectedProxy(t *testing.T) {
	tracker := &proxyHopTracker{}
	selectProxy := tracker.wrap(func(*http.Request) (*url.URL, error) {
		return url.Parse("http://127.0.0.1:7890")
	})

	req, _ := http.NewRequest(http.MethodGet, "http://site.invalid/", nil)
	selected, err := selectProxy(req)
	if err != nil || selected == nil {
		t.Fatalf("selected=%v err=%v", selected, err)
	}
	if !tracker.isProxyHop("127.0.0.1") {
		t.Fatal("the selected proxy's host was not recorded")
	}

	// A selector that declines (NO_PROXY, or no proxy configured) records nothing.
	empty := &proxyHopTracker{}
	decline := empty.wrap(func(*http.Request) (*url.URL, error) { return nil, nil })
	if selected, err := decline(req); selected != nil || err != nil {
		t.Fatalf("selected=%v err=%v, want nil", selected, err)
	}
	if empty.isProxyHop("127.0.0.1") {
		t.Fatal("nothing should be recorded when no proxy is selected")
	}
}

// Provider requests go out as PivotFlow instead of the bare Go default: a live
// cun.ai request without any User-Agent at all is answered with Cloudflare's
// 403 block page, which the caller then reports as a rejected credential, and
// "Go-http-client/1.1" is the automation marker the rest of the codebase
// already avoids. An explicit header must survive untouched.
func TestProviderClientSendsUserAgent(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()

	client, err := ClientFactory{AllowPrivate: true}.New(DirectProxyURL)
	if err != nil {
		t.Fatal(err)
	}
	do := func(request *http.Request) {
		t.Helper()
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	do(request)
	do(request)
	custom, err := http.NewRequest(http.MethodGet, server.URL+"/api/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	custom.Header.Set("User-Agent", "SiteProbe/9.9")
	do(custom)

	if len(seen) != 3 {
		t.Fatalf("saw %d requests, want 3", len(seen))
	}
	if seen[0] != version.OutboundUserAgent() || seen[1] != version.OutboundUserAgent() {
		t.Fatalf("default User-Agent=%q/%q, want %q", seen[0], seen[1], version.OutboundUserAgent())
	}
	if seen[2] != "SiteProbe/9.9" {
		t.Fatalf("explicit User-Agent was overwritten: %q", seen[2])
	}
	if request.Header.Get("User-Agent") != "" {
		t.Fatalf("the caller's request object was mutated: %q", request.Header.Get("User-Agent"))
	}
}
