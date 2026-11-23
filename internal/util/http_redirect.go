package util

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
)

const maxAuthenticatedRedirectHops = 10

var (
	errAuthenticatedRedirectCrossOrigin = errors.New("authenticated redirect blocked: target is cross-origin")
	errAuthenticatedRedirectTooManyHops = errors.New("authenticated redirect blocked: more than 10 hops")
	errAuthenticatedRedirectURLUserInfo = errors.New("authenticated redirect blocked: URL contains userinfo")
	errAuthenticatedRedirectInvalidURL  = errors.New("authenticated redirect blocked: invalid URL")
	errAuthenticatedRedirectMissingVia  = errors.New("authenticated redirect blocked: redirect chain is empty")
)

// AuthenticatedRedirectPolicy permits redirects only within the origin of the
// original request. Origin equality includes the scheme, a case-insensitive
// hostname, and the effective port, so an omitted default port equals its
// explicit form. Ten redirect responses may be followed.
func AuthenticatedRedirectPolicy(request *http.Request, via []*http.Request) error {
	if request == nil || request.URL == nil {
		return errAuthenticatedRedirectInvalidURL
	}
	if len(via) == 0 || via[0] == nil || via[0].URL == nil {
		return errAuthenticatedRedirectMissingVia
	}
	for _, previous := range via {
		if previous != nil && previous.URL != nil && previous.URL.User != nil {
			return errAuthenticatedRedirectURLUserInfo
		}
	}
	if request.URL.User != nil {
		return errAuthenticatedRedirectURLUserInfo
	}
	if len(via) > maxAuthenticatedRedirectHops {
		return errAuthenticatedRedirectTooManyHops
	}
	if !sameAuthenticatedOrigin(via[0].URL, request.URL) {
		return errAuthenticatedRedirectCrossOrigin
	}
	return nil
}

func sameAuthenticatedOrigin(original, target *url.URL) bool {
	if original == nil || target == nil {
		return false
	}
	originalScheme := strings.ToLower(original.Scheme)
	targetScheme := strings.ToLower(target.Scheme)
	if !isHTTPTransportScheme(originalScheme) || originalScheme != targetScheme {
		return false
	}
	if original.Hostname() == "" || target.Hostname() == "" ||
		!strings.EqualFold(original.Hostname(), target.Hostname()) {
		return false
	}
	return effectiveHTTPPort(original) != "" &&
		effectiveHTTPPort(original) == effectiveHTTPPort(target)
}

func isHTTPTransportScheme(scheme string) bool {
	return scheme == "http" || scheme == "https"
}

func effectiveHTTPPort(target *url.URL) string {
	if port := target.Port(); port != "" {
		return port
	}
	switch strings.ToLower(target.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}
