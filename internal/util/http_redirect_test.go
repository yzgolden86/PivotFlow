package util

import (
	"errors"
	"net/http"
	"testing"
)

func redirectRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("NewRequest(%q): %v", target, err)
	}
	return request
}

func TestAuthenticatedRedirectPolicy(t *testing.T) {
	tests := []struct {
		name     string
		original string
		target   string
		wantErr  error
	}{
		{
			name:     "case-insensitive hostname and inferred https port",
			original: "https://API.Example.com/v1/start",
			target:   "https://api.example.com:443/v1/next",
		},
		{
			name:     "explicit and inferred http default ports",
			original: "http://example.com:80/start",
			target:   "http://EXAMPLE.COM/next",
		},
		{
			name:     "matching explicit non-default ports",
			original: "https://example.com:8443/start",
			target:   "https://example.com:8443/next",
		},
		{
			name:     "different hostname",
			original: "https://example.com/start",
			target:   "https://evil.example/next",
			wantErr:  errAuthenticatedRedirectCrossOrigin,
		},
		{
			name:     "same hostname different port",
			original: "http://example.com:8080/start",
			target:   "http://example.com:8081/next",
			wantErr:  errAuthenticatedRedirectCrossOrigin,
		},
		{
			name:     "scheme downgrade",
			original: "https://example.com/start",
			target:   "http://example.com/next",
			wantErr:  errAuthenticatedRedirectCrossOrigin,
		},
		{
			name:     "scheme upgrade is also cross-origin",
			original: "http://example.com/start",
			target:   "https://example.com/next",
			wantErr:  errAuthenticatedRedirectCrossOrigin,
		},
		{
			name:     "target URL userinfo",
			original: "https://example.com/start",
			target:   "https://user:password@example.com/next",
			wantErr:  errAuthenticatedRedirectURLUserInfo,
		},
		{
			name:     "original URL userinfo",
			original: "https://user:password@example.com/start",
			target:   "https://example.com/next",
			wantErr:  errAuthenticatedRedirectURLUserInfo,
		},
		{
			name:     "non-http scheme",
			original: "https://example.com/start",
			target:   "ftp://example.com/next",
			wantErr:  errAuthenticatedRedirectCrossOrigin,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			via := []*http.Request{redirectRequest(t, test.original)}
			err := AuthenticatedRedirectPolicy(redirectRequest(t, test.target), via)
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("AuthenticatedRedirectPolicy() error = %v", err)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("AuthenticatedRedirectPolicy() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestAuthenticatedRedirectPolicyAllowsTenHopsAndRejectsEleventh(t *testing.T) {
	const sameOrigin = "https://example.com/step"

	via := make([]*http.Request, maxAuthenticatedRedirectHops)
	for index := range via {
		via[index] = redirectRequest(t, sameOrigin)
	}
	if err := AuthenticatedRedirectPolicy(redirectRequest(t, sameOrigin), via); err != nil {
		t.Fatalf("redirect %d was rejected: %v", maxAuthenticatedRedirectHops, err)
	}

	via = append(via, redirectRequest(t, sameOrigin))
	err := AuthenticatedRedirectPolicy(redirectRequest(t, sameOrigin), via)
	if !errors.Is(err, errAuthenticatedRedirectTooManyHops) {
		t.Fatalf("redirect %d error = %v, want %v",
			maxAuthenticatedRedirectHops+1, err, errAuthenticatedRedirectTooManyHops)
	}
}
