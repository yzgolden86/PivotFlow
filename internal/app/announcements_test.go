package app

import "testing"

func TestResolveAnnouncementSourceURL(t *testing.T) {
	tests := []struct {
		name   string
		base   string
		source string
		want   string
	}{
		{name: "API endpoint opens upstream site", base: "https://upstream.example.com/", source: "/api/notice", want: "https://upstream.example.com"},
		{name: "public announcement page", base: "https://upstream.example.com/", source: "/announcements/42", want: "https://upstream.example.com/announcements/42"},
		{name: "absolute https", base: "https://upstream.example.com", source: "https://docs.example.com/notice", want: "https://docs.example.com/notice"},
		{name: "reject unsafe scheme", base: "https://upstream.example.com", source: "javascript:alert(1)", want: ""},
		{name: "reject invalid base", base: "not a url", source: "/api/notice", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveAnnouncementSourceURL(tt.base, tt.source); got != tt.want {
				t.Fatalf("resolveAnnouncementSourceURL(%q, %q) = %q, want %q", tt.base, tt.source, got, tt.want)
			}
		})
	}
}
