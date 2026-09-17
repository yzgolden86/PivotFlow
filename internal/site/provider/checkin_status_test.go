package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// New API answers *every* check-in failure with HTTP 200 and success:false: the
// Turnstile guard is a middleware that aborts before the handler, and the
// handler itself reports "already checked in" and "check-in disabled" the same
// way. The transport-error branch therefore never sees a browser challenge on a
// real site, so the payload branch has to reach the same verdict. These fixtures
// reproduce the upstream wording verbatim.
const (
	// middleware/turnstile-check.go
	turnstilePayload = `{"success":false,"message":"Turnstile token 为空"}`
	// model/checkin.go, HasCheckedInToday -> UserCheckin
	alreadyCheckedPayload = `{"success":false,"message":"今日已签到"}`
	// controller/checkin.go, DoCheckin success. Note the reward field is
	// quota_awarded, not reward.
	checkinSuccessPayload = `{"success":true,"message":"签到成功","data":{"quota_awarded":5,"checkin_date":"2026-09-17"}}`
)

func newCheckinPayloadServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

// A Turnstile challenge arrives as a payload, not as a transport error. Filing
// it as a plain failure is what made a site that merely needs one browser click
// look broken: last_checkin_status said "failed", browser_required_count stayed
// zero, and the failure webhook fired every single day.
func TestNewAPICheckinClassifiesBrowserChallengeFromPayload(t *testing.T) {
	server := newCheckinPayloadServer(t, turnstilePayload)

	result, err := NewNewAPI(ClientFactory{AllowPrivate: true}).Checkin(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"},
	})
	if result.Status != CheckinBrowserRequired {
		t.Fatalf("status=%q, want %q", result.Status, CheckinBrowserRequired)
	}
	if got := ErrorCode(err); got != CodeBrowserRequired {
		t.Fatalf("error code=%q, want %q", got, CodeBrowserRequired)
	}
	if result.Message != "Turnstile token 为空" {
		t.Fatalf("message=%q, want the upstream wording preserved", result.Message)
	}
}

func TestNewAPICheckinRecognizesAlreadyCheckedWording(t *testing.T) {
	server := newCheckinPayloadServer(t, alreadyCheckedPayload)

	result, err := NewNewAPI(ClientFactory{AllowPrivate: true}).Checkin(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"},
	})
	if err != nil || result.Status != CheckinAlreadyChecked {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestNewAPICheckinReportsSuccess(t *testing.T) {
	server := newCheckinPayloadServer(t, checkinSuccessPayload)

	result, err := NewNewAPI(ClientFactory{AllowPrivate: true}).Checkin(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"},
	})
	if err != nil || result.Status != CheckinSuccess {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestVeloeraCheckinClassifiesBrowserChallengeFromPayload(t *testing.T) {
	server := newCheckinPayloadServer(t, turnstilePayload)

	result, err := NewVeloera(ClientFactory{AllowPrivate: true}).Checkin(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"},
	})
	if result.Status != CheckinBrowserRequired {
		t.Fatalf("status=%q, want %q", result.Status, CheckinBrowserRequired)
	}
	if got := ErrorCode(err); got != CodeBrowserRequired {
		t.Fatalf("error code=%q, want %q", got, CodeBrowserRequired)
	}
}

func TestAnyRouterCheckinClassifiesBrowserChallengeFromPayload(t *testing.T) {
	server := newCheckinPayloadServer(t, turnstilePayload)

	result, err := NewAnyRouter(ClientFactory{AllowPrivate: true}).Checkin(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "session", UserID: 42},
	})
	if result.Status != CheckinBrowserRequired {
		t.Fatalf("status=%q, want %q", result.Status, CheckinBrowserRequired)
	}
	if got := ErrorCode(err); got != CodeBrowserRequired {
		t.Fatalf("error code=%q, want %q", got, CodeBrowserRequired)
	}
}

// The transport and payload paths must not disagree about the same condition.
func TestCheckinStatusFromCodeMatchesTransportPath(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{CodeBrowserRequired, CheckinBrowserRequired},
		{CodeUnsupported, CheckinUnsupported},
		{CodeExpired, CheckinFailed},
		{CodeRequestFailed, CheckinFailed},
		{CodeTimeout, CheckinFailed},
	}
	for _, tt := range cases {
		t.Run(tt.code, func(t *testing.T) {
			if got := checkinStatusFromCode(tt.code); got != tt.want {
				t.Fatalf("status=%q, want %q", got, tt.want)
			}
		})
	}
}

// The success body of current New API carries quota_awarded and no reward at
// all, so a reward-only read produced an empty label on every up-to-date site.
func TestNewAPICheckinReportsQuotaRewardWhenRewardIsAbsent(t *testing.T) {
	server := newCheckinPayloadServer(t, checkinSuccessPayload)

	result, err := NewNewAPI(ClientFactory{AllowPrivate: true}).Checkin(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"},
	})
	if err != nil || result.Status != CheckinSuccess {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.RewardText != "+5 额度" {
		t.Fatalf("reward=%q, want the quota count carried by quota_awarded", result.RewardText)
	}
}

func TestCheckinRewardTextFallsBackFromRewardToQuota(t *testing.T) {
	cases := []struct {
		name string
		data any
		want string
	}{
		{name: "a preformatted reward wins", data: map[string]any{"reward": "2.5", "quota_awarded": float64(500000)}, want: "2.5"},
		{name: "quota_awarded is the fallback", data: map[string]any{"quota_awarded": float64(500000)}, want: "+500000 额度"},
		{name: "a zero award is not a reward", data: map[string]any{"quota_awarded": float64(0)}, want: ""},
		{name: "neither field present", data: map[string]any{}, want: ""},
		{name: "data is not an object", data: "签到成功", want: ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkinRewardText(tt.data); got != tt.want {
				t.Fatalf("reward=%q, want %q", got, tt.want)
			}
		})
	}
}
