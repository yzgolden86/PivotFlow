package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Aliyun WAF answers a blocked client with HTTP 200 and an HTML interstitial.
// The page carries no <html> tag - only a doctype and a pair of
// <meta name="aliyun_waf_*"> elements - which is why a marker list built around
// "<html" let the body reach the JSON decoder, where it surfaced to the
// operator as an intermittent "invalid JSON" instead of a browser challenge.
// Shape taken from a live response; the nonces are placeholders.
const aliyunWAFPage = `<!doctype html>
<meta charset="UTF-8">
<meta name="aliyun_waf_aa" content="0xbootstrapnonce">
<meta name="aliyun_waf_bb" content="0xpayloadnonce">
<title></title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<script>window.__waf_verify=function(){document.cookie="acw_sc__v3=0xvalue;path=/";location.reload()}</script>`

func TestLooksLikeChallenge(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		want        bool
	}{
		{name: "aliyun waf page without an html tag", contentType: "text/html; charset=utf-8", body: aliyunWAFPage, want: true},
		{name: "cloudflare interstitial", contentType: "text/html", body: `<html><title>Just a moment...</title>cf-chl-`, want: true},
		{name: "plain html page", contentType: "text/html", body: `<html><body>gateway error</body></html>`, want: true},
		{name: "doctype only", contentType: "text/html", body: `<!doctype html><p>blocked</p>`, want: true},
		{name: "empty body typed as html", contentType: "text/html", body: "", want: true},
		{name: "untyped html body", contentType: "", body: aliyunWAFPage, want: true},
		// A proxy may mislabel a perfectly good answer; valid JSON is never a
		// challenge, whatever the content type says.
		{name: "json mislabelled as html", contentType: "text/html", body: `{"success":true,"data":{"id":42}}`, want: false},
		{name: "ordinary json", contentType: "application/json", body: `{"success":true}`, want: false},
		{name: "json with a leading bom", contentType: "application/json", body: "\ufeff" + `{"success":true}`, want: false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeChallenge(tt.contentType, []byte(tt.body)); got != tt.want {
				t.Fatalf("looksLikeChallenge(%q, %.40q) = %v, want %v", tt.contentType, tt.body, got, tt.want)
			}
		})
	}
}

// The live symptom, end to end through the adapter: a WAF page must be reported
// as a browser challenge, not as unparseable data.
func TestWAFInterstitialIsReportedAsBrowserChallenge(t *testing.T) {
	server := newRawPayloadServer(t, http.StatusOK, "text/html; charset=utf-8", aliyunWAFPage)

	_, err := NewNewAPI(ClientFactory{AllowPrivate: true}).CheckedInToday(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"},
	})
	if got := ErrorCode(err); got != CodeBrowserRequired {
		t.Fatalf("error code=%q, want %q (err=%v)", got, CodeBrowserRequired, err)
	}
}

// Anything that is neither JSON nor a recognizable interstitial must still say
// what arrived: the bare "invalid JSON" wording hid the content type, the
// status and the first bytes of the body from whoever had to diagnose it.
func TestUnexpectedPayloadIsDescribed(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		body        string
		want        []string
	}{
		// The excerpt is quoted with %q, so embedded quotes arrive escaped.
		{name: "truncated json", contentType: "application/json", body: `{"success":`, want: []string{"invalid JSON", "HTTP 200", "application/json", `body="{\"success\":`}},
		{name: "plain text answer", contentType: "text/plain", body: "Service Unavailable", want: []string{"invalid JSON", "text/plain", "Service Unavailable"}},
		{name: "empty body", contentType: "application/json", body: "", want: []string{"empty body", "HTTP 200"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server := newRawPayloadServer(t, http.StatusOK, tt.contentType, tt.body)
			_, err := NewNewAPI(ClientFactory{AllowPrivate: true}).CheckedInToday(context.Background(), AccountRequest{
				BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"},
			})
			if got := ErrorCode(err); got != CodeInvalidResponse {
				t.Fatalf("error code=%q, want %q", got, CodeInvalidResponse)
			}
			message := ErrorMessage(err)
			for _, want := range tt.want {
				if !strings.Contains(message, want) {
					t.Fatalf("message=%q, want it to contain %q", message, want)
				}
			}
		})
	}
}
