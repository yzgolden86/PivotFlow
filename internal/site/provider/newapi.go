package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

type NewAPI struct{ clients ClientFactory }

func NewNewAPI(factory ClientFactory) *NewAPI { return &NewAPI{clients: factory} }
func (p *NewAPI) ID() string                  { return model.SitePlatformNewAPIFamily }
func (p *NewAPI) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{ServerCheckin: true, Balance: true, Models: true, Announcements: true, CredentialTypes: []string{model.CredentialTypeUsernamePassword, model.CredentialTypeAccessToken, model.CredentialTypeCookie, model.CredentialTypeAPIKey}}
}

func (p *NewAPI) Detect(ctx context.Context, baseURL string) (DetectionResult, error) {
	var payload envelope
	if err := p.doJSON(ctx, AccountRequest{BaseURL: baseURL}, http.MethodGet, "/api/status", nil, &payload); err != nil {
		return DetectionResult{}, err
	}
	name, _ := stringValue(payload.Data, "system_name")
	matched := payload.Success && name != ""
	return DetectionResult{Matched: matched, ProviderID: p.ID(), SystemName: name, Capabilities: p.Capabilities()}, nil
}

func (p *NewAPI) Login(ctx context.Context, req LoginRequest) (Credentials, error) {
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" {
		return Credentials{}, &Error{Code: CodeRequestFailed, Message: "username and password are required"}
	}
	var payload envelope
	responseHeaders, err := p.doJSONWithResponseHeaders(ctx, AccountRequest{BaseURL: req.BaseURL, ProxyURL: req.ProxyURL}, http.MethodPost, "/api/user/login", map[string]any{"username": username, "password": req.Password}, map[string]string{"X-Requested-With": "XMLHttpRequest"}, &payload)
	if err != nil {
		return Credentials{}, err
	}
	if !payload.Success {
		return Credentials{}, responseError(payload, http.StatusOK)
	}
	token := loginToken(payload)
	cookie := responseCookieHeader(responseHeaders)
	if token == "" && !hasUsableSessionCookie(cookie) {
		return Credentials{}, &Error{Code: CodeInvalidResponse, Message: "login response did not contain an access token"}
	}
	credentials := Credentials{Username: username, AccessToken: token, Cookie: cookie}
	if id, ok := numberValue(payload.Data, "id"); ok && id > 0 {
		credentials.UserID = int64(id)
	}
	return credentials, nil
}

func loginToken(payload envelope) string {
	if token, ok := payload.Data.(string); ok {
		return strings.TrimSpace(token)
	}
	for _, key := range []string{"token", "access_token", "accessToken"} {
		if token, ok := stringValue(payload.Data, key); ok && token != "" {
			return strings.TrimSpace(token)
		}
	}
	for _, token := range []string{payload.Token, payload.AccessToken} {
		if strings.TrimSpace(token) != "" {
			return strings.TrimSpace(token)
		}
	}
	return ""
}

func responseCookieHeader(headers http.Header) string {
	response := &http.Response{Header: headers}
	cookies := response.Cookies()
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name != "" {
			parts = append(parts, cookie.Name+"="+cookie.Value)
		}
	}
	return strings.Join(parts, "; ")
}

func hasUsableSessionCookie(cookieHeader string) bool {
	ignored := map[string]struct{}{"acw_tc": {}, "acw_sc__v2": {}, "cdn_sec_tc": {}}
	for _, part := range strings.Split(cookieHeader, ";") {
		name, _, ok := strings.Cut(strings.TrimSpace(part), "=")
		name = strings.ToLower(strings.TrimSpace(name))
		if !ok || name == "" {
			continue
		}
		if _, skip := ignored[name]; skip {
			continue
		}
		if name == "session" || name == "token" || name == "auth_token" || name == "access_token" || name == "jwt" || name == "jwt_token" || strings.Contains(name, "session") || strings.Contains(name, "token") || strings.Contains(name, "auth") {
			return true
		}
	}
	return false
}

func (p *NewAPI) ResolveManagementCredentials(ctx context.Context, req AccountRequest) (Credentials, error) {
	credentials := req.Credentials
	if strings.TrimSpace(credentials.AccessToken) == "" && strings.TrimSpace(credentials.Cookie) == "" {
		return Credentials{}, &Error{Code: CodeUnsupported, Message: "management credential is required"}
	}
	verify := func(candidate Credentials) (Credentials, error) {
		var payload envelope
		check := req
		check.Credentials = candidate
		if err := p.doJSON(ctx, check, http.MethodGet, "/api/user/self", nil, &payload); err != nil {
			return Credentials{}, err
		}
		if !payload.Success {
			return Credentials{}, responseError(payload, http.StatusOK)
		}
		if id, ok := numberValue(payload.Data, "id"); ok && id > 0 {
			candidate.UserID = int64(id)
		}
		return candidate, nil
	}
	variants := managementCredentialVariants(credentials)
	var firstErr error
	needsUserID := false
	for _, candidate := range variants {
		resolved, err := verify(candidate)
		if err == nil {
			return mergeResolvedCredentials(credentials, resolved), nil
		}
		if firstErr == nil {
			firstErr = err
		}
		if ErrorCode(err) == CodeUserIDRequired {
			needsUserID = true
		}
		if code := ErrorCode(err); code == CodeBrowserRequired || code == CodeRateLimited || code == CodeTimeout {
			return Credentials{}, err
		}
	}
	if credentials.UserID > 0 {
		return Credentials{}, firstErr
	}
	if !needsUserID {
		return Credentials{}, firstErr
	}

	ids := make([]int64, 0, len(managementUserIDProbeCandidates())+1)
	if id := jwtUserID(credentials.AccessToken); id > 0 {
		ids = append(ids, id)
	}
	for _, id := range managementUserIDProbeCandidates() {
		duplicate := false
		for _, existing := range ids {
			if existing == id {
				duplicate = true
				break
			}
		}
		if !duplicate {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		for _, variant := range variants {
			candidate := variant
			candidate.UserID = id
			if resolved, err := verify(candidate); err == nil {
				return mergeResolvedCredentials(credentials, resolved), nil
			}
		}
	}
	return Credentials{}, &Error{Code: CodeUserIDRequired, Message: "unable to determine the upstream user ID; enter the user ID shown in the New API user profile"}
}

func managementCredentialVariants(credentials Credentials) []Credentials {
	variants := []Credentials{credentials}
	if strings.TrimSpace(credentials.Cookie) != "" || strings.TrimSpace(credentials.AccessToken) == "" {
		return variants
	}
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(credentials.AccessToken), "Bearer "))
	if raw == "" {
		return variants
	}
	cookies := make([]string, 0, 3)
	if strings.Contains(raw, "=") {
		cookies = append(cookies, raw)
	}
	cookies = append(cookies, "session="+raw, "token="+raw)
	seen := map[string]struct{}{}
	for _, cookie := range cookies {
		if _, exists := seen[cookie]; exists {
			continue
		}
		seen[cookie] = struct{}{}
		candidate := credentials
		candidate.Cookie = cookie
		variants = append(variants, candidate)
	}
	return variants
}

func mergeResolvedCredentials(original, resolved Credentials) Credentials {
	if resolved.AccessToken == "" {
		resolved.AccessToken = original.AccessToken
	}
	if resolved.APIKey == "" {
		resolved.APIKey = original.APIKey
	}
	if resolved.Cookie == "" {
		resolved.Cookie = original.Cookie
	}
	if resolved.Username == "" {
		resolved.Username = original.Username
	}
	return resolved
}

func managementUserIDProbeCandidates() []int64 {
	// New API system access tokens are frequently opaque and some deployments
	// reject /api/user/self until New-API-User is present. These common IDs match
	// the conservative discovery set used by MetaAPI; a successful response is
	// still required before an ID is accepted or persisted.
	return []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 15, 20, 50, 100, 8899, 11494}
}

func jwtUserID(token string) int64 {
	parts := strings.Split(strings.TrimSpace(strings.TrimPrefix(token, "Bearer ")), ".")
	if len(parts) != 3 {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims map[string]any
	if json.Unmarshal(raw, &claims) != nil {
		return 0
	}
	for _, key := range []string{"id", "user_id", "userId", "sub"} {
		switch value := claims[key].(type) {
		case float64:
			if value > 0 {
				return int64(value)
			}
		case string:
			if id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && id > 0 {
				return id
			}
		}
	}
	return 0
}

func (p *NewAPI) ListRoutingKeys(ctx context.Context, req AccountRequest) ([]RoutingKeySnapshot, error) {
	if req.Credentials.AccessToken == "" && req.Credentials.Cookie == "" {
		return nil, &Error{Code: CodeUnsupported, Message: "routing key discovery requires a management session"}
	}
	var payload envelope
	if err := p.doJSON(ctx, req, http.MethodGet, "/api/token/?p=0&size=100", nil, &payload); err != nil {
		return nil, err
	}
	if !payload.Success {
		return nil, responseError(payload, http.StatusOK)
	}
	items := collectionItems(payload.Data)
	if len(items) == 0 {
		if direct, ok := payload.Data.([]any); ok {
			items = direct
		}
	}
	out := make([]RoutingKeySnapshot, 0, len(items))
	for index, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		key, _ := stringValue(item, "key")
		if key == "" {
			continue
		}
		name, _ := stringValue(item, "name")
		if name == "" {
			name = fmt.Sprintf("token-%d", index+1)
		}
		id := strings.TrimSpace(fmt.Sprint(item["id"]))
		if id == "<nil>" {
			id = ""
		}
		group, _ := stringValue(item, "group")
		if group == "" {
			group, _ = stringValue(item, "group_name")
		}
		models := modelNames(item["models"])
		if len(models) == 0 {
			models = modelNames(item["model_limits"])
		}
		enabled := true
		if status, ok := numberValue(item, "status"); ok {
			enabled = int(status) == 1
		} else if status, ok := stringValue(item, "status"); ok {
			enabled = !isDisabledProviderStatus(status)
		}
		out = append(out, RoutingKeySnapshot{ID: id, Name: name, Group: group, Models: models, Key: key, Enabled: enabled})
	}
	return out, nil
}

func (p *NewAPI) RefreshAccount(ctx context.Context, req RefreshAccountRequest) (AccountSnapshot, error) {
	if req.Credentials.AccessToken == "" && req.Credentials.Cookie == "" {
		return AccountSnapshot{}, &Error{Code: CodeUnsupported, Message: "balance refresh requires a session token"}
	}
	credentials, err := p.ResolveManagementCredentials(ctx, req)
	if err != nil {
		return AccountSnapshot{}, err
	}
	req.Credentials = credentials
	var payload envelope
	if err := p.doJSON(ctx, req, http.MethodGet, "/api/user/self", nil, &payload); err != nil {
		return AccountSnapshot{}, err
	}
	if !payload.Success {
		return AccountSnapshot{}, responseError(payload, http.StatusOK)
	}
	username, _ := stringValue(payload.Data, "username")
	if username == "" {
		username, _ = stringValue(payload.Data, "display_name")
	}
	quota, ok := numberValue(payload.Data, "quota")
	var balance *float64
	if ok {
		value := quota / 500000
		balance = &value
	}
	return AccountSnapshot{Username: username, Balance: balance, Currency: "CNY", Status: model.SiteAccountStatusHealthy}, nil
}

func (p *NewAPI) ListModels(ctx context.Context, req AccountRequest) ([]ModelSnapshot, error) {
	var openAI struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := p.doJSON(ctx, req, http.MethodGet, "/v1/models", nil, &openAI); err == nil && len(openAI.Data) > 0 {
		out := make([]ModelSnapshot, 0, len(openAI.Data))
		for _, m := range openAI.Data {
			if strings.TrimSpace(m.ID) != "" {
				out = append(out, ModelSnapshot{Model: m.ID, RouteType: "openai_chat", Source: "models_endpoint"})
			}
		}
		return out, nil
	}
	if req.Credentials.AccessToken == "" && req.Credentials.Cookie == "" {
		return nil, &Error{Code: CodeUnsupported, Message: "model endpoint unavailable for this credential"}
	}
	var payload envelope
	if err := p.doJSON(ctx, req, http.MethodGet, "/api/user/models", nil, &payload); err != nil {
		return nil, err
	}
	models := modelNames(payload.Data)
	if len(models) == 0 {
		return nil, &Error{Code: CodeUnsupported, Message: "models endpoint returned no supported model list"}
	}
	out := make([]ModelSnapshot, 0, len(models))
	for _, name := range models {
		out = append(out, ModelSnapshot{Model: name, RouteType: "openai_chat", Source: "models_endpoint"})
	}
	return out, nil
}

func (p *NewAPI) Checkin(ctx context.Context, req AccountRequest) (CheckinResult, error) {
	if req.Credentials.AccessToken == "" && req.Credentials.Cookie == "" {
		return CheckinResult{Status: CheckinUnsupported}, &Error{Code: CodeUnsupported, Message: "check-in requires a session token"}
	}
	var payload envelope
	err := p.doJSON(ctx, req, http.MethodPost, "/api/user/checkin", map[string]any{}, &payload)
	if err != nil {
		code := ErrorCode(err)
		status := CheckinFailed
		if code == CodeBrowserRequired {
			status = CheckinBrowserRequired
		}
		if code == CodeUnsupported {
			status = CheckinUnsupported
		}
		return CheckinResult{Status: status}, err
	}
	message := payload.Message
	if payload.Success {
		reward, _ := stringValue(payload.Data, "reward")
		return CheckinResult{Status: CheckinSuccess, RewardText: reward, Message: message}, nil
	}
	lower := strings.ToLower(message)
	if strings.Contains(lower, "already") || strings.Contains(message, "已签到") || strings.Contains(message, "重复签到") {
		return CheckinResult{Status: CheckinAlreadyChecked, Message: message}, nil
	}
	return CheckinResult{Status: CheckinFailed, Message: message}, responseError(payload, http.StatusOK)
}

func (p *NewAPI) ListAnnouncements(ctx context.Context, req AccountRequest) ([]Announcement, error) {
	var payload envelope
	if err := p.doJSON(ctx, req, http.MethodGet, "/api/notice", nil, &payload); err != nil {
		return nil, err
	}
	content, ok := payload.Data.(string)
	content = strings.TrimSpace(content)
	if !ok || content == "" {
		return []Announcement{}, nil
	}
	if len(content) > 256<<10 {
		return nil, &Error{Code: CodeInvalidResponse, Message: "announcement exceeds size limit"}
	}
	h := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(h[:])
	return []Announcement{{SourceKey: "notice:" + hash, Title: "Site notice", ContentMarkdown: content, Level: "info", SourceURL: "/api/notice", ContentHash: hash}}, nil
}

type envelope struct {
	Success     bool   `json:"success"`
	Data        any    `json:"data"`
	Message     string `json:"message"`
	Error       any    `json:"error"`
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}

func (p *NewAPI) doJSON(ctx context.Context, req AccountRequest, method, path string, body any, out any) error {
	return p.doJSONWithHeaders(ctx, req, method, path, body, nil, out)
}

func (p *NewAPI) doJSONWithHeaders(ctx context.Context, req AccountRequest, method, path string, body any, headers map[string]string, out any) error {
	_, err := p.doJSONWithResponseHeaders(ctx, req, method, path, body, headers, out)
	return err
}

func (p *NewAPI) doJSONWithResponseHeaders(ctx context.Context, req AccountRequest, method, path string, body any, headers map[string]string, out any) (http.Header, error) {
	if err := ValidateBaseURL(req.BaseURL, p.clients.AllowPrivate); err != nil {
		return nil, err
	}
	client, err := p.clients.New(req.ProxyURL)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(strings.TrimRight(req.BaseURL, "/"))
	if err != nil {
		return nil, err
	}
	target, err := base.Parse(path)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(target.Hostname(), base.Hostname()) {
		return nil, errors.New("provider target changed host")
	}
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/json")
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	applyAuth(httpReq, req.Credentials)
	for key, value := range headers {
		httpReq.Header.Set(key, value)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return nil, &Error{Code: CodeTimeout, Message: "provider request timed out"}
		}
		return nil, &Error{Code: CodeRequestFailed, Message: "provider request failed"}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, &Error{Code: CodeRequestFailed, Message: "read provider response"}
	}
	if int64(len(raw)) > maxResponseBytes {
		return nil, &Error{Code: CodeInvalidResponse, Message: "provider response exceeds size limit"}
	}
	if looksLikeChallenge(resp.Header.Get("Content-Type"), raw) {
		return nil, &Error{Code: CodeBrowserRequired, StatusCode: resp.StatusCode, Message: "browser verification is required"}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		message := responseMessage(raw)
		if requiresUserID(message) {
			return nil, &Error{Code: CodeUserIDRequired, StatusCode: resp.StatusCode, Message: message}
		}
		return nil, &Error{Code: CodeExpired, StatusCode: resp.StatusCode, Message: "provider credential was rejected"}
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, &Error{Code: CodeUnsupported, StatusCode: resp.StatusCode, Message: "provider endpoint is not available"}
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		return nil, &Error{Code: CodeRateLimited, StatusCode: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")), Message: "provider is rate limited"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &Error{Code: CodeRequestFailed, StatusCode: resp.StatusCode, Message: "provider returned HTTP " + strconv.Itoa(resp.StatusCode)}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, &Error{Code: CodeInvalidResponse, Message: "provider returned invalid JSON"}
	}
	return resp.Header.Clone(), nil
}

func applyAuth(req *http.Request, c Credentials) {
	token := strings.TrimSpace(c.Token())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(token, "Bearer "))
	}
	if c.Cookie != "" {
		req.Header.Set("Cookie", c.Cookie)
	}
	if c.UserID > 0 {
		value := strconv.FormatInt(c.UserID, 10)
		for _, header := range []string{"New-API-User", "Veloera-User", "voapi-user", "User-id", "X-User-Id", "Rix-Api-User", "neo-api-user"} {
			req.Header.Set(header, value)
		}
	}
}
func looksLikeChallenge(contentType string, raw []byte) bool {
	text := strings.ToLower(string(raw))
	return strings.Contains(strings.ToLower(contentType), "text/html") && (strings.Contains(text, "turnstile") || strings.Contains(text, "cf-chl-") || strings.Contains(text, "cloudflare") || strings.Contains(text, "acw_sc__v2"))
}
func parseRetryAfter(raw string) time.Time {
	seconds, err := strconv.Atoi(strings.TrimSpace(raw))
	if err == nil && seconds > 0 {
		return time.Now().Add(time.Duration(seconds) * time.Second)
	}
	if parsed, err := http.ParseTime(raw); err == nil {
		return parsed
	}
	return time.Time{}
}
func responseError(payload envelope, status int) error {
	message := strings.TrimSpace(payload.Message)
	if message == "" {
		message = "provider rejected the request"
	}
	lower := strings.ToLower(message)
	code := CodeRequestFailed
	if requiresUserID(message) {
		code = CodeUserIDRequired
	}
	if strings.Contains(lower, "mismatch") || strings.Contains(lower, "user not found") || strings.Contains(lower, "invalid user id") || strings.Contains(message, "用户不存在") || strings.Contains(message, "用户 ID 不匹配") || strings.Contains(message, "用户ID不匹配") {
		code = CodeUserIDRequired
	}
	if code != CodeUserIDRequired && (strings.Contains(lower, "invalid token") || strings.Contains(lower, "access token") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "forbidden") || strings.Contains(lower, "expired") || strings.Contains(message, "无权") || strings.Contains(message, "未登录") || strings.Contains(message, "已过期") || strings.Contains(message, "令牌无效")) {
		code = CodeExpired
	}
	if strings.Contains(lower, "turnstile") || strings.Contains(lower, "captcha") || strings.Contains(message, "验证") {
		code = CodeBrowserRequired
	}
	return &Error{Code: code, StatusCode: status, Message: message}
}

func responseMessage(raw []byte) string {
	var payload envelope
	if json.Unmarshal(raw, &payload) == nil {
		if message := strings.TrimSpace(payload.Message); message != "" {
			return message
		}
	}
	return "provider credential was rejected"
}

func requiresUserID(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	return strings.Contains(lower, "new-api-user") || strings.Contains(lower, "veloera-user") || strings.Contains(lower, "user id") || strings.Contains(lower, "user_id") || strings.Contains(message, "用户 ID") || strings.Contains(message, "用户ID")
}
func numberValue(value any, key string) (float64, bool) {
	m, ok := value.(map[string]any)
	if !ok {
		return 0, false
	}
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		x, e := n.Float64()
		return x, e == nil
	case string:
		x, e := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return x, e == nil
	default:
		return 0, false
	}
}
func stringValue(value any, key string) (string, bool) {
	m, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	switch s := v.(type) {
	case string:
		return strings.TrimSpace(s), true
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64), true
	default:
		return fmt.Sprint(s), true
	}
}
func modelNames(value any) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	switch data := value.(type) {
	case []any:
		for _, item := range data {
			switch v := item.(type) {
			case string:
				add(v)
			case map[string]any:
				if id, ok := v["id"].(string); ok {
					add(id)
				}
			}
		}
	case map[string]any:
		for key := range data {
			add(key)
		}
	}
	return out
}
