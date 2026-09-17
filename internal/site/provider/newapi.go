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
	// Detection already paid for /api/status, which is also where the check-in
	// capability is published, so hand it back instead of making callers ask again.
	method := checkinMethodFromStatus(payload)
	return DetectionResult{Matched: matched, ProviderID: p.ID(), SystemName: name, Capabilities: p.Capabilities(), CheckinMethod: &method}, nil
}

// checkinMethodFromStatus reads the check-in capability out of an /api/status
// payload. Both Detect and DiscoverCheckin parse the same document, so the
// interpretation lives in one place.
func checkinMethodFromStatus(payload envelope) CheckinMethod {
	enabled, hasEnabled := boolValue(payload.Data, "checkin_enabled")
	turnstile, _ := boolValue(payload.Data, "turnstile_check")
	siteKey, _ := stringValue(payload.Data, "turnstile_site_key")
	method := CheckinMethod{Status: CheckinMethodUnknown, TurnstileSiteKey: siteKey, Source: "api_status"}
	switch {
	case !hasEnabled:
		// Older builds do not publish the flag. Attempting the check-in is the
		// historical behaviour and stays correct; claiming "disabled" would not.
	case !enabled:
		method.Status = CheckinMethodDisabled
	case turnstile:
		method.Status = CheckinMethodTurnstile
	default:
		method.Status = CheckinMethodAvailable
	}
	return method
}

// DiscoverCheckin reads the public status endpoint to learn whether this site
// offers server-side check-in and whether an interactive Turnstile challenge
// stands in the way. The endpoint needs no credentials, so this can run before
// an account is even configured.
func (p *NewAPI) DiscoverCheckin(ctx context.Context, req AccountRequest) (CheckinMethod, error) {
	var payload envelope
	if err := p.doJSON(ctx, AccountRequest{BaseURL: req.BaseURL, ProxyURL: req.ProxyURL}, http.MethodGet, "/api/status", nil, &payload); err != nil {
		return CheckinMethod{}, err
	}
	return checkinMethodFromStatus(payload), nil
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
	if resolved.RefreshToken == "" {
		resolved.RefreshToken = original.RefreshToken
	}
	if resolved.ExpiresAt == 0 {
		resolved.ExpiresAt = original.ExpiresAt
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
	maskedIDs := make([]int, 0)
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		key, _ := stringValue(item, "key")
		id, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(item["id"])))
		if isMaskedRoutingKey(key) && err == nil && id > 0 {
			maskedIDs = append(maskedIDs, id)
		}
	}
	resolvedKeys := p.resolveRoutingKeysBatch(ctx, req, maskedIDs)
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
		if isMaskedRoutingKey(key) {
			if resolved := strings.TrimSpace(resolvedKeys[id]); resolved != "" && !isMaskedRoutingKey(resolved) {
				key = resolved
			} else {
				resolved, err := p.resolveRoutingKey(ctx, req, id)
				if err != nil {
					return nil, &Error{Code: ErrorCode(err), StatusCode: ErrorStatusCode(err), Message: fmt.Sprintf("unable to recover routing key %s: %s", name, ErrorMessage(err))}
				}
				key = resolved
			}
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

func isMaskedRoutingKey(value string) bool {
	return strings.Contains(value, "*") || strings.Contains(value, "\u2022")
}

func (p *NewAPI) resolveRoutingKeysBatch(ctx context.Context, req AccountRequest, ids []int) map[string]string {
	if len(ids) == 0 {
		return nil
	}
	var payload envelope
	if err := p.doJSON(ctx, req, http.MethodPost, "/api/token/batch/keys", map[string]any{"ids": ids}, &payload); err != nil || !payload.Success {
		return nil
	}
	data, ok := payload.Data.(map[string]any)
	if !ok {
		return nil
	}
	keys, ok := data["keys"].(map[string]any)
	if !ok {
		return nil
	}
	resolved := make(map[string]string, len(keys))
	for id, raw := range keys {
		key := strings.TrimSpace(fmt.Sprint(raw))
		if key != "" && key != "<nil>" && !isMaskedRoutingKey(key) {
			resolved[id] = key
		}
	}
	return resolved
}

func (p *NewAPI) resolveRoutingKey(ctx context.Context, req AccountRequest, id string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", &Error{Code: CodeRoutingKeyUnavailable, Message: "masked routing key has no token ID"}
	}
	path := "/api/token/" + url.PathEscape(id) + "/key"
	var lastErr error
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		var payload envelope
		if err := p.doJSON(ctx, req, method, path, nil, &payload); err != nil {
			lastErr = err
			if method == http.MethodPost && (ErrorStatusCode(err) == http.StatusMethodNotAllowed || ErrorCode(err) == CodeUnsupported) {
				continue
			}
			return "", err
		}
		if !payload.Success {
			lastErr = responseError(payload, http.StatusOK)
			continue
		}
		key := ""
		switch data := payload.Data.(type) {
		case string:
			key = strings.TrimSpace(data)
		case map[string]any:
			key, _ = stringValue(data, "key")
		}
		if key == "" || isMaskedRoutingKey(key) {
			lastErr = &Error{Code: CodeInvalidResponse, Message: "routing key reveal returned no usable key"}
			continue
		}
		return key, nil
	}
	if lastErr == nil {
		lastErr = &Error{Code: CodeRoutingKeyUnavailable, Message: "routing key reveal is unavailable"}
	}
	return "", lastErr
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
	return AccountSnapshot{Username: username, Balance: balance, Currency: "USD", Status: model.SiteAccountStatusHealthy}, nil
}

func (p *NewAPI) ListModels(ctx context.Context, req AccountRequest) ([]ModelSnapshot, error) {
	var openAI struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	endpointErr := p.doJSON(ctx, req, http.MethodGet, "/v1/models", nil, &openAI)
	if endpointErr == nil && len(openAI.Data) > 0 {
		out := make([]ModelSnapshot, 0, len(openAI.Data))
		for _, m := range openAI.Data {
			if strings.TrimSpace(m.ID) != "" {
				out = append(out, ModelSnapshot{Model: m.ID, RouteType: "openai_chat", Source: "models_endpoint"})
			}
		}
		return out, nil
	}
	// Without a session credential there is no management endpoint to fall back
	// on, so this call is the only source of truth. Preserve the endpoint's own
	// error rather than flattening every outcome into "unsupported": a rejected
	// or expired routing key reported as an unsupported site sends the operator
	// looking at the wrong thing entirely.
	if req.Credentials.AccessToken == "" && req.Credentials.Cookie == "" {
		if endpointErr != nil {
			return nil, endpointErr
		}
		return nil, &Error{Code: CodeUnsupported, Message: "model endpoint unavailable for this credential"}
	}
	return p.listManagementModels(ctx, req, "/api/user/models", "models_endpoint")
}

func (p *NewAPI) ListModelsForRoutingKey(ctx context.Context, req AccountRequest, key RoutingKeySnapshot) ([]ModelSnapshot, error) {
	path := "/api/user/models"
	if group := strings.TrimSpace(key.Group); group != "" {
		path += "?group=" + url.QueryEscape(group)
	}
	return p.listManagementModels(ctx, req, path, "routing_group_models")
}

func (p *NewAPI) listManagementModels(ctx context.Context, req AccountRequest, path, source string) ([]ModelSnapshot, error) {
	var payload envelope
	if err := p.doJSON(ctx, req, http.MethodGet, path, nil, &payload); err != nil {
		return nil, err
	}
	models := modelNames(payload.Data)
	// A few New API forks omit the success flag while still returning a valid
	// data array. Preserve that compatibility, but keep upstream error messages
	// when the response contains no usable model list.
	if !payload.Success && len(models) == 0 && strings.TrimSpace(payload.Message) != "" {
		return nil, responseError(payload, http.StatusOK)
	}
	if len(models) == 0 {
		return nil, &Error{Code: CodeUnsupported, Message: "models endpoint returned no supported model list"}
	}
	out := make([]ModelSnapshot, 0, len(models))
	for _, name := range models {
		out = append(out, ModelSnapshot{Model: name, RouteType: "openai_chat", Source: source})
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
		return checkinErrorResult(err)
	}
	message := payload.Message
	if payload.Success {
		return CheckinResult{Status: CheckinSuccess, RewardText: checkinRewardText(payload.Data), Message: message}, nil
	}
	if isAlreadyCheckedMessage(message) {
		return CheckinResult{Status: CheckinAlreadyChecked, Message: message}, nil
	}
	failure := responseError(payload, http.StatusOK)
	return CheckinResult{Status: checkinStatusFromCode(ErrorCode(failure)), Message: message}, failure
}

func (p *NewAPI) CheckedInToday(ctx context.Context, req AccountRequest) (bool, error) {
	if req.Credentials.AccessToken == "" && req.Credentials.Cookie == "" {
		return false, &Error{Code: CodeUnsupported, Message: "check-in status requires a session token"}
	}
	month := time.Now().Format("2006-01")
	var payload envelope
	if err := p.doJSON(ctx, req, http.MethodGet, "/api/user/checkin?month="+url.QueryEscape(month), nil, &payload); err != nil {
		return false, err
	}
	if !payload.Success {
		return false, responseError(payload, http.StatusOK)
	}
	return envelopeCheckedInToday(payload), nil
}

func envelopeCheckedInToday(payload envelope) bool {
	data, ok := payload.Data.(map[string]any)
	if !ok {
		return false
	}
	stats, ok := data["stats"].(map[string]any)
	if !ok {
		return false
	}
	checked, _ := stats["checked_in_today"].(bool)
	return checked
}

// checkinRewardText reads the reward out of a successful check-in payload.
//
// Forks disagree on the field. AnyRouter and older New API builds publish
// data.reward as an already formatted string; current New API publishes
// data.quota_awarded as an integer count of quota units and no reward at all;
// Veloera publishes that same count as data.quota. Reading only reward
// therefore produced an empty label on every up-to-date site, which is not
// fatal — the caller prefers the balance delta when the follow-up refresh
// succeeds — but it silently degraded the fallback. The quota count is labelled
// rather than dressed up as currency: the quota-to-currency rate is a site
// setting this response does not carry.
//
// quota names the account balance on /api/user/self, but this helper only reads
// check-in responses, where the field carries the reward.
func checkinRewardText(data any) string {
	if reward, ok := stringValue(data, "reward"); ok {
		return reward
	}
	quota, ok := numberValue(data, "quota_awarded")
	if !ok {
		quota, ok = numberValue(data, "quota")
	}
	if !ok || quota <= 0 {
		return ""
	}
	return fmt.Sprintf("+%d 额度", int64(quota))
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
	if err := decodeProviderJSON(raw, out); err != nil {
		return nil, &Error{Code: CodeInvalidResponse, Message: describeUnexpectedPayload(resp, raw)}
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

// looksLikeChallenge reports whether a response is a browser/WAF interstitial
// rather than the JSON a management endpoint is supposed to return.
//
// A marker list alone is not enough. Aliyun WAF answers with a page that has no
// <html> tag at all - only a doctype and a pair of <meta name="aliyun_waf_*">
// elements - so the body used to slip past detection and die in the JSON
// decoder as "invalid JSON", which is what operators reported as an
// intermittent, unexplained sync failure.
//
// Order matters. A body that parses as JSON is the endpoint's answer, whatever
// words it carries: an upstream error is allowed to say "Turnstile token 为空"
// without being mistaken for the interstitial that phrase also appears in.
// Only what is left is matched against the challenge markers, and failing that
// against the HTML shape - a doctype, an <html> tag, or a bare text/html
// content type, which is how the Aliyun page gives itself away.
func looksLikeChallenge(contentType string, raw []byte) bool {
	trimmed := bytes.TrimSpace(raw)
	if json.Valid(trimmed) {
		return false
	}
	text := strings.ToLower(string(trimmed))
	if strings.Contains(text, "turnstile") || strings.Contains(text, "cf-chl-") ||
		strings.Contains(text, "cloudflare") || strings.Contains(text, "acw_sc__v2") ||
		strings.Contains(text, "aliyun_waf") {
		return true
	}
	return strings.Contains(text, "<!doctype html") || strings.Contains(text, "<html") ||
		strings.Contains(strings.ToLower(contentType), "text/html")
}

// describeUnexpectedPayload names what actually came back. "provider returned
// invalid JSON" on its own sent operators looking for a parsing bug when the
// real cause was, say, a proxy's HTML error page or a truncated body; the small
// verbatim excerpt makes that visible from the console and the logs alone.
func describeUnexpectedPayload(resp *http.Response, raw []byte) string {
	kind := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if kind == "" {
		kind = "no content type"
	}
	body := strings.Join(strings.Fields(string(raw)), " ")
	if len(body) > 64 {
		body = body[:64] + "..."
	}
	if body == "" {
		return fmt.Sprintf("provider returned an empty body (HTTP %d, %s)", resp.StatusCode, kind)
	}
	return fmt.Sprintf("provider returned invalid JSON (HTTP %d, %s, body=%q)", resp.StatusCode, kind, body)
}

func decodeProviderJSON(raw []byte, out any) error {
	raw = bytes.TrimSpace(bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf}))
	if err := json.Unmarshal(raw, out); err == nil {
		return nil
	}
	// A few reverse proxies prepend an anti-XSSI marker. Strip it only when a
	// complete JSON object/array remains; never attempt to parse arbitrary HTML.
	if bytes.HasPrefix(raw, []byte(")]}'")) {
		if index := bytes.IndexAny(raw, "{["); index >= 0 {
			trimmed := bytes.TrimSpace(raw[index:])
			if err := json.Unmarshal(trimmed, out); err == nil {
				return nil
			}
		}
	}
	return errors.New("invalid provider JSON")
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

// boolValue reads a JSON boolean flag. The decoder yields bool for real JSON
// booleans, but a few forks serialize these switches as strings or numbers, so
// accept those shapes too. The second result reports whether the key existed.
func boolValue(value any, key string) (bool, bool) {
	m, ok := value.(map[string]any)
	if !ok {
		return false, false
	}
	v, ok := m[key]
	if !ok {
		return false, false
	}
	switch b := v.(type) {
	case bool:
		return b, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(b))
		return parsed, err == nil
	case float64:
		return b != 0, true
	default:
		return false, false
	}
}
func modelNames(value any) []string {
	seen := map[string]struct{}{}
	out := []string{}
	var add func(string)
	add = func(raw string) {
		raw = strings.TrimSpace(strings.TrimPrefix(raw, "models/"))
		if raw == "" {
			return
		}
		// New API and several compatible panels serialize the allowed model list
		// as a comma/newline/semicolon separated string instead of an array.
		parts := strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r'
		})
		if len(parts) > 1 {
			for _, part := range parts {
				add(part)
			}
			return
		}
		if _, ok := seen[raw]; ok {
			return
		}
		seen[raw] = struct{}{}
		out = append(out, raw)
	}
	var visit func(any)
	visit = func(current any) {
		switch data := current.(type) {
		case string:
			add(data)
		case []string:
			for _, item := range data {
				add(item)
			}
		case []any:
			for _, item := range data {
				visit(item)
			}
		case map[string]any:
			// Prefer nested model fields when a provider wraps the list in an
			// object. If none are present, model-limit maps use their keys as
			// model names (for example {"gpt-4": 1000}).
			matchedField := false
			for _, key := range []string{"models", "allowed_models", "model_limits", "model_list", "modelList", "data", "items", "list"} {
				if child, ok := data[key]; ok {
					visit(child)
					matchedField = true
				}
			}
			for _, key := range []string{"id", "model", "model_name", "modelName"} {
				if child, ok := data[key]; ok {
					if name, ok := child.(string); ok {
						add(name)
						matchedField = true
					}
				}
			}
			if matchedField {
				return
			}
			for key, child := range data {
				if key == "models" || key == "allowed_models" || key == "model_limits" || key == "model_list" || key == "modelList" || key == "data" || key == "items" || key == "list" || key == "id" || key == "model" || key == "model_name" || key == "modelName" || key == "name" || key == "key" || key == "group" || key == "group_name" || key == "status" {
					continue
				}
				switch child.(type) {
				case string, float64, json.Number, int, int64, bool:
					add(key)
				}
			}
		}
	}
	visit(value)
	return out
}
