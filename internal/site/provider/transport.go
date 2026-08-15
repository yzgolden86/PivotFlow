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
	proxyFunc := http.ProxyFromEnvironment
	if strings.EqualFold(strings.TrimSpace(proxyURL), DirectProxyURL) {
		proxyFunc = nil
		proxyURL = ""
	}
	transport := &http.Transport{Proxy: proxyFunc, ForceAttemptHTTP2: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
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
