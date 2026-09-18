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
	// model/user.go, User.CheckIn: the already-checked branch. The wording
	// splits 已 and 签到 across 已经, so it slipped past a "已签到" match.
	veloeraAlreadyCheckedPayload = `{"success":false,"message":"你今天已经签到过了"}`
	// controller/user.go, CheckIn success. Veloera names the reward quota,
	// where current New API says quota_awarded.
	veloeraCheckinSuccessPayload = `{"success":true,"message":"签到成功","data":{"quota":500000}}`
	// controller/user.go, CheckInStatus: can_check_in is true while the day's
	// check-in is still outstanding.
	veloeraNotCheckedInPayload  = `{"success":true,"message":"","data":{"can_check_in":true}}`
	veloeraAlreadyCheckedStatus = `{"success":true,"message":"","data":{"can_check_in":false}}`
)

func newCheckinPayloadServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return newStatusPayloadServer(t, http.MethodPost, "", body)
}

// newStatusPayloadServer answers one JSON body for a single method and path;
// every other request gets a 404, so a call to the wrong route fails the test
// instead of quietly returning an empty payload.
func newStatusPayloadServer(t *testing.T, method, path, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method || (path != "" && r.URL.Path != path) {
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
		{name: "veloera names it quota", data: map[string]any{"quota": float64(500000)}, want: "+500000 额度"},
		{name: "quota_awarded outranks quota", data: map[string]any{"quota_awarded": float64(7), "quota": float64(500000)}, want: "+7 额度"},
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

// Veloera splits the two characters of 已签到 across 已经, so a matcher built
// around "已签到" read "你今天已经签到过了" as an unrecognized failure: the day
// was filed as failed, the failure webhook fired, and the retry budget
// kept hammering a site that could not answer differently until tomorrow.
func TestVeloeraCheckinRecognizesAlreadyCheckedWording(t *testing.T) {
	server := newCheckinPayloadServer(t, veloeraAlreadyCheckedPayload)

	result, err := NewVeloera(ClientFactory{AllowPrivate: true}).Checkin(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"},
	})
	if err != nil || result.Status != CheckinAlreadyChecked {
		t.Fatalf("result=%+v err=%v, want the already-checked outcome", result, err)
	}
}

func TestVeloeraCheckinReportsQuotaReward(t *testing.T) {
	server := newCheckinPayloadServer(t, veloeraCheckinSuccessPayload)

	result, err := NewVeloera(ClientFactory{AllowPrivate: true}).Checkin(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"},
	})
	if err != nil || result.Status != CheckinSuccess {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if result.RewardText != "+500000 额度" {
		t.Fatalf("reward=%q, want the quota count carried by data.quota", result.RewardText)
	}
}

// The Veloera status route is /api/user/check_in_status, and its flag is
// inverted: can_check_in is true while the check-in is still outstanding.
// Delegating to the New API family method would query /api/user/checkin, which
// does not exist here - the server below answers 404 for every other path so a
// regression to the delegated call fails loudly instead of reporting "not
// checked in".
func TestVeloeraCheckedInTodayReadsItsOwnStatusRoute(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{name: "a pending check-in means it is still open", body: veloeraNotCheckedInPayload, want: false},
		{name: "a completed check-in inverts the flag", body: veloeraAlreadyCheckedStatus, want: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			server := newStatusPayloadServer(t, http.MethodGet, "/api/user/check_in_status", tt.body)

			checked, err := NewVeloera(ClientFactory{AllowPrivate: true}).CheckedInToday(context.Background(), AccountRequest{
				BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"},
			})
			if err != nil {
				t.Fatalf("err=%v", err)
			}
			if checked != tt.want {
				t.Fatalf("checkedInToday=%v, want %v", checked, tt.want)
			}
		})
	}
}

// A status body without the flag must not be read as "not checked in": silence
// there would let a completed day slip back into the retry budget.
func TestVeloeraCheckedInTodayRejectsPayloadWithoutFlag(t *testing.T) {
	server := newStatusPayloadServer(t, http.MethodGet, "/api/user/check_in_status", `{"success":true,"message":"","data":{}}`)

	_, err := NewVeloera(ClientFactory{AllowPrivate: true}).CheckedInToday(context.Background(), AccountRequest{
		BaseURL: server.URL, Credentials: Credentials{AccessToken: "session"},
	})
	if err == nil || ErrorCode(err) != CodeInvalidResponse {
		t.Fatalf("err=%v, want %q", err, CodeInvalidResponse)
	}
}

// newRawPayloadServer serves one body with an explicit status and content type,
// for cases where the response shape itself is the subject.
func newRawPayloadServer(t *testing.T, status int, contentType, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}
