package provider

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxResponseBytes int64 = 2 << 20

const DirectProxyURL = "direct://"

type ClientFactory struct {
	AllowPrivate bool
	Resolver     *net.Resolver
}

func (f ClientFactory) New(proxyURL string) (*http.Client, error) {
	resolver := f.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	hops := &proxyHopTracker{}
	proxyFunc := http.ProxyFromEnvironment
	if strings.EqualFold(strings.TrimSpace(proxyURL), DirectProxyURL) {
		proxyFunc = nil
		proxyURL = ""
	}
	if proxyFunc != nil {
		// The selector runs per request and may pick a different proxy for
		// different targets, so recording here is the only way the dialer learns
		// about an env-configured proxy it never saw at construction time.
		proxyFunc = hops.wrap(proxyFunc)
	}
	transport := &http.Transport{Proxy: proxyFunc, ForceAttemptHTTP2: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		// A proxy hop is dialled as configured rather than filtered. The
		// private-address rule below exists to stop a site URL from reaching
		// internal services, but when a proxy is configured the transport dials
		// the proxy, not the site — so the rule rejected every localhost proxy
		// (Clash / v2ray on 127.0.0.1, which is exactly what the per-site proxy
		// field is for) with "provider host resolves only to private or unsafe
		// addresses", while protecting nothing: the proxy is what reaches the
		// target. The site URL itself is still checked by ValidateBaseURL.
		if hops.isProxyHop(host) {
			return dialer.DialContext(ctx, network, address)
		}
		ips, err := resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolve provider host: %w", err)
		}
		for _, ip := range ips {
			if !f.AllowPrivate && isPrivateAddress(ip) {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		return nil, errors.New("provider host resolves only to private or unsafe addresses")
	}}
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return nil, errors.New("invalid site proxy url")
		}
		transport.Proxy = http.ProxyURL(parsed)
		hops.remember(parsed.Hostname())
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return errors.New("too many provider redirects")
		}
		if len(via) > 0 && !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
			return errors.New("provider redirect changed host")
		}
		if err := ValidateBaseURL(req.URL.String(), f.AllowPrivate); err != nil {
			return err
		}
		return nil
	}
	return client, nil
}

func ValidateBaseURL(raw string, allowPrivate bool) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return errors.New("invalid site url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("site url must use http or https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("site url contains forbidden components")
	}
	hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if !allowPrivate && (hostname == "localhost" || strings.HasSuffix(hostname, ".localhost")) {
		return errors.New("site url points to a private or unsafe address")
	}
	if ip, err := netip.ParseAddr(parsed.Hostname()); err == nil && !allowPrivate && isPrivateAddress(ip) {
		return errors.New("site url points to a private or unsafe address")
	}
	return nil
}

func isPrivateAddress(ip netip.Addr) bool {
	if !ip.IsValid() {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() || ip.String() == "169.254.169.254"
}

// proxyHopTracker remembers which host names the transport may dial as a proxy
// hop, so DialContext can tell "connecting to the proxy" apart from "connecting
// to the site" — they are the same callback but need opposite handling.
type proxyHopTracker struct{ hosts sync.Map }

func (t *proxyHopTracker) remember(hostname string) {
	if hostname = strings.ToLower(strings.TrimSpace(hostname)); hostname != "" {
		t.hosts.Store(hostname, struct{}{})
	}
}

func (t *proxyHopTracker) isProxyHop(hostname string) bool {
	_, ok := t.hosts.Load(strings.ToLower(hostname))
	return ok
}

func (t *proxyHopTracker) wrap(selectProxy func(*http.Request) (*url.URL, error)) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		selected, err := selectProxy(req)
		if selected != nil {
			t.remember(selected.Hostname())
		}
		return selected, err
	}
}
