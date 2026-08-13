package provider

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// AnyRouter is a New API-family variant with a distinct check-in endpoint.
type AnyRouter struct{ family *NewAPI }

func NewAnyRouter(factory ClientFactory) *AnyRouter {
	return &AnyRouter{family: NewNewAPI(factory)}
}

func (p *AnyRouter) ID() string { return model.SitePlatformAnyRouter }

func (p *AnyRouter) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		ServerCheckin: true, BrowserAssisted: true, Balance: true, Models: true, Announcements: true,
		CredentialTypes: []string{model.CredentialTypeUsernamePassword, model.CredentialTypeCookie, model.CredentialTypeAccessToken, model.CredentialTypeAPIKey},
	}
}

func (p *AnyRouter) Detect(ctx context.Context, baseURL string) (DetectionResult, error) {
	parsed, _ := url.Parse(baseURL)
	if strings.Contains(strings.ToLower(parsed.Hostname()), "anyrouter") {
		return DetectionResult{Matched: true, ProviderID: p.ID(), SystemName: "AnyRouter", Capabilities: p.Capabilities()}, nil
	}
	var payload envelope
	if err := p.family.doJSON(ctx, AccountRequest{BaseURL: baseURL}, http.MethodGet, "/api/status", nil, &payload); err != nil {
		return DetectionResult{}, err
	}
	name, _ := stringValue(payload.Data, "system_name")
	version, _ := stringValue(payload.Data, "version")
	matched := strings.Contains(strings.ToLower(name+" "+version), "anyrouter")
	return DetectionResult{Matched: matched, ProviderID: p.ID(), SystemName: name, Capabilities: p.Capabilities()}, nil
}

func (p *AnyRouter) RefreshAccount(ctx context.Context, req RefreshAccountRequest) (AccountSnapshot, error) {
	return p.family.RefreshAccount(ctx, req)
}

func (p *AnyRouter) Login(ctx context.Context, req LoginRequest) (Credentials, error) {
	return p.family.Login(ctx, req)
}

func (p *AnyRouter) ResolveManagementCredentials(ctx context.Context, req AccountRequest) (Credentials, error) {
	return p.family.ResolveManagementCredentials(ctx, req)
}

func (p *AnyRouter) ListRoutingKeys(ctx context.Context, req AccountRequest) ([]RoutingKeySnapshot, error) {
	return p.family.ListRoutingKeys(ctx, req)
}

func (p *AnyRouter) ListModels(ctx context.Context, req AccountRequest) ([]ModelSnapshot, error) {
	return p.family.ListModels(ctx, req)
}

func (p *AnyRouter) Checkin(ctx context.Context, req AccountRequest) (CheckinResult, error) {
	if strings.TrimSpace(req.Credentials.AccessToken) != "" {
		bearerReq := req
		bearerReq.Credentials.Cookie = ""
		var payload envelope
		err := p.family.doJSONWithHeaders(ctx, bearerReq, http.MethodPost, "/api/user/checkin", nil, map[string]string{"X-Requested-With": "XMLHttpRequest"}, &payload)
		if err == nil {
			result, resultErr := anyRouterCheckinPayload(payload)
			if resultErr == nil || !anyRouterShouldFallbackToCookie(result.Message, resultErr) {
				return result, resultErr
			}
		} else if !anyRouterShouldFallbackToCookie("", err) {
			return checkinErrorResult(err)
		}
	}

	cookieReq := req
	cookieReq.Credentials.AccessToken = ""
	if strings.TrimSpace(cookieReq.Credentials.Cookie) == "" {
		cookieReq.Credentials.Cookie = anyRouterSessionCookie(req.Credentials.AccessToken)
	}
	if cookieReq.Credentials.Cookie == "" || cookieReq.Credentials.UserID <= 0 {
		return CheckinResult{Status: CheckinUnsupported}, &Error{Code: CodeUnsupported, Message: "AnyRouter check-in requires an access token, or a session cookie with user ID"}
	}
	var payload envelope
	err := p.family.doJSONWithHeaders(ctx, cookieReq, http.MethodPost, "/api/user/sign_in", map[string]any{}, map[string]string{"X-Requested-With": "XMLHttpRequest"}, &payload)
	if err != nil {
		return checkinErrorResult(err)
	}
	return anyRouterCheckinPayload(payload)
}

func anyRouterCheckinPayload(payload envelope) (CheckinResult, error) {
	message := strings.TrimSpace(payload.Message)
	if isAlreadyCheckedMessage(message) {
		return CheckinResult{Status: CheckinAlreadyChecked, Message: message}, nil
	}
	if !payload.Success {
		return CheckinResult{Status: CheckinFailed, Message: message}, responseError(payload, http.StatusOK)
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "success") || strings.Contains(message, "签到成功") {
		reward, _ := stringValue(payload.Data, "reward")
		return CheckinResult{Status: CheckinSuccess, RewardText: reward, Message: message}, nil
	}
	if message == "" {
		return CheckinResult{Status: CheckinSuccess}, nil
	}
	return CheckinResult{Status: CheckinFailed, Message: message}, &Error{Code: CodeRequestFailed, Message: "AnyRouter returned an unrecognized check-in result"}
}

func anyRouterShouldFallbackToCookie(message string, err error) bool {
	code := ErrorCode(err)
	if code == CodeUnsupported || code == CodeExpired || code == CodeUserIDRequired || code == CodeInvalidResponse {
		return true
	}
	text := strings.ToLower(strings.TrimSpace(message + " " + ErrorMessage(err)))
	return strings.Contains(text, "not login") || strings.Contains(text, "not logged") || strings.Contains(text, "unauthorized") || strings.Contains(text, "forbidden") || strings.Contains(text, "未登录") || strings.Contains(text, "未提供")
}

func anyRouterSessionCookie(accessToken string) string {
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(accessToken), "Bearer "))
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "=") {
		return raw
	}
	return "session=" + raw
}

func (p *AnyRouter) ListAnnouncements(ctx context.Context, req AccountRequest) ([]Announcement, error) {
	return p.family.ListAnnouncements(ctx, req)
}

func checkinErrorResult(err error) (CheckinResult, error) {
	status := CheckinFailed
	switch ErrorCode(err) {
	case CodeBrowserRequired:
		status = CheckinBrowserRequired
	case CodeUnsupported:
		status = CheckinUnsupported
	}
	return CheckinResult{Status: status}, err
}

func isAlreadyCheckedMessage(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(lower, "already") || strings.Contains(message, "已签到") || strings.Contains(message, "重复签到") || strings.Contains(message, "今日已")
}
