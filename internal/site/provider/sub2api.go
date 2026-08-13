package provider

import (
	"context"
	"crypto/sha256"
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

type Sub2API struct {
	clients ClientFactory
	family  *NewAPI
}

func NewSub2API(factory ClientFactory) *Sub2API {
	return &Sub2API{clients: factory, family: NewNewAPI(factory)}
}

func (p *Sub2API) ID() string { return model.SitePlatformSub2API }
func (p *Sub2API) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		ServerCheckin: false, Balance: true, Models: true, Announcements: true,
		CredentialTypes: []string{model.CredentialTypeAccessToken, model.CredentialTypeAPIKey},
	}
}

type sub2Envelope struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func (p *Sub2API) Detect(ctx context.Context, baseURL string) (DetectionResult, error) {
	payload, contentType, err := p.probe(ctx, baseURL, "/api/v1/auth/me")
	if err != nil {
		return DetectionResult{}, err
	}
	matched := false
	if strings.Contains(strings.ToLower(contentType), "json") {
		switch code := payload.Code.(type) {
		case string:
			normalized := strings.ToUpper(strings.TrimSpace(code))
			matched = normalized == "UNAUTHORIZED" || normalized == "API_KEY_REQUIRED"
		case float64:
			matched = code == 0 && payload.Data != nil
		}
	}
	return DetectionResult{Matched: matched, ProviderID: p.ID(), SystemName: "Sub2API", Capabilities: p.Capabilities()}, nil
}

func (p *Sub2API) RefreshAccount(ctx context.Context, req RefreshAccountRequest) (AccountSnapshot, error) {
	if req.Credentials.AccessToken == "" {
		return AccountSnapshot{}, &Error{Code: CodeUnsupported, Message: "Sub2API balance refresh requires a JWT access token"}
	}
	data, err := p.requestData(ctx, req, http.MethodGet, "/api/v1/auth/me")
	if err != nil {
		return AccountSnapshot{}, err
	}
	userID, ok := numberValue(data, "id")
	if !ok || userID <= 0 {
		return AccountSnapshot{}, &Error{Code: CodeInvalidResponse, Message: "Sub2API identity is missing a valid user ID"}
	}
	username, _ := stringValue(data, "username")
	if username == "" {
		email, _ := stringValue(data, "email")
		username = strings.Split(email, "@")[0]
	}
	balanceValue, balanceOK := numberValue(data, "balance")
	var balance *float64
	if balanceOK {
		balance = &balanceValue
	}
	return AccountSnapshot{Username: username, Balance: balance, Currency: "USD", Status: model.SiteAccountStatusHealthy}, nil
}

func (p *Sub2API) ResolveManagementCredentials(ctx context.Context, req AccountRequest) (Credentials, error) {
	credentials := req.Credentials
	credentials.AccessToken = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(credentials.AccessToken), "Bearer "))
	if credentials.AccessToken == "" {
		return Credentials{}, &Error{Code: CodeUnsupported, Message: "Sub2API requires an Auth Token (JWT)"}
	}
	data, err := p.requestData(ctx, AccountRequest{BaseURL: req.BaseURL, ProxyURL: req.ProxyURL, Credentials: credentials}, http.MethodGet, "/api/v1/auth/me")
	if err != nil {
		return Credentials{}, err
	}
	if userID, ok := numberValue(data, "id"); ok && userID > 0 {
		credentials.UserID = int64(userID)
	}
	return credentials, nil
}

func (p *Sub2API) ListModels(ctx context.Context, req AccountRequest) ([]ModelSnapshot, error) {
	items, directErr := p.listModelsWithCredentials(ctx, req)
	if directErr == nil && len(items) > 0 {
		return items, nil
	}
	if req.Credentials.AccessToken == "" {
		return nil, directErr
	}
	apiKey, err := p.firstUsableAPIKey(ctx, req)
	if err != nil {
		if directErr != nil {
			return nil, directErr
		}
		return nil, err
	}
	fallback := req
	fallback.Credentials = Credentials{APIKey: apiKey}
	return p.listModelsWithCredentials(ctx, fallback)
}

func (p *Sub2API) Checkin(context.Context, AccountRequest) (CheckinResult, error) {
	return CheckinResult{Status: CheckinUnsupported, Message: "Sub2API does not support check-in"}, &Error{Code: CodeUnsupported, Message: "Sub2API does not support check-in"}
}

func (p *Sub2API) ListAnnouncements(ctx context.Context, req AccountRequest) ([]Announcement, error) {
	if req.Credentials.AccessToken == "" {
		return nil, &Error{Code: CodeUnsupported, Message: "Sub2API announcements require a JWT access token"}
	}
	data, err := p.requestData(ctx, req, http.MethodGet, "/api/v1/announcements?page=1&page_size=100")
	if err != nil {
		return nil, err
	}
	items := collectionItems(data)
	out := make([]Announcement, 0, len(items))
	for _, item := range items {
		id, idOK := numberValue(item, "id")
		title, _ := stringValue(item, "title")
		content, _ := stringValue(item, "content")
		if content == "" {
			content, _ = stringValue(item, "message")
		}
		if content == "" {
			content, _ = stringValue(item, "body")
		}
		if title == "" && content == "" {
			continue
		}
		if len(content) > 256<<10 {
			continue
		}
		if title == "" {
			title = "Sub2API announcement"
		}
		hashBytes := sha256.Sum256([]byte(title + "\n" + content))
		hash := hex.EncodeToString(hashBytes[:])
		sourceKey := "announcement:" + hash
		if idOK && id > 0 {
			sourceKey = "announcement:" + strconv.FormatInt(int64(id), 10)
		}
		upstreamAt := parseProviderTime(valueFromMap(item, "updated_at"))
		if upstreamAt == 0 {
			upstreamAt = parseProviderTime(valueFromMap(item, "created_at"))
		}
		out = append(out, Announcement{SourceKey: sourceKey, Title: title, ContentMarkdown: content, Level: "info", SourceURL: "/api/v1/announcements", UpstreamAt: upstreamAt, ContentHash: hash})
	}
	return out, nil
}

func (p *Sub2API) requestData(ctx context.Context, req AccountRequest, method, path string) (any, error) {
	var payload sub2Envelope
	if err := p.family.doJSON(ctx, req, method, path, nil, &payload); err != nil {
		return nil, err
	}
	if !sub2SuccessCode(payload.Code) {
		message := strings.TrimSpace(payload.Message)
		if message == "" {
			message = "Sub2API rejected the request"
		}
		code := CodeRequestFailed
		normalizedCode := strings.ToLower(strings.TrimSpace(fmt.Sprint(payload.Code)))
		lowerMessage := strings.ToLower(message)
		if strings.Contains(normalizedCode, "unauthorized") || strings.Contains(normalizedCode, "expired") || strings.Contains(lowerMessage, "unauthorized") || strings.Contains(lowerMessage, "expired") || strings.Contains(lowerMessage, "invalid token") {
			code = CodeExpired
		}
		return nil, &Error{Code: code, Message: message}
	}
	if payload.Data == nil {
		return nil, &Error{Code: CodeInvalidResponse, Message: "Sub2API response is missing data"}
	}
	return payload.Data, nil
}

func sub2SuccessCode(value any) bool {
	switch code := value.(type) {
	case float64:
		return code == 0
	case string:
		return strings.TrimSpace(code) == "0"
	default:
		return false
	}
}

func (p *Sub2API) listModelsWithCredentials(ctx context.Context, req AccountRequest) ([]ModelSnapshot, error) {
	var firstErr error
	for _, endpoint := range []string{"/v1/models", "/api/v1/models", "/v1beta/models", "/antigravity/v1/models", "/models"} {
		var payload any
		if err := p.family.doJSON(ctx, req, http.MethodGet, endpoint, nil, &payload); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		names := collectSub2ModelNames(payload)
		if len(names) == 0 {
			continue
		}
		out := make([]ModelSnapshot, 0, len(names))
		for _, name := range names {
			out = append(out, ModelSnapshot{Model: name, RouteType: "openai_chat", Source: "models_endpoint"})
		}
		return out, nil
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, &Error{Code: CodeUnsupported, Message: "Sub2API models endpoint returned no models"}
}

func collectSub2ModelNames(value any) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	var visit func(any)
	visit = func(current any) {
		switch item := current.(type) {
		case []any:
			for _, child := range item {
				visit(child)
			}
		case map[string]any:
			for _, key := range []string{"data", "items", "models", "list", "records", "rows"} {
				if child, ok := item[key]; ok {
					visit(child)
				}
			}
			for _, key := range []string{"id", "name", "model", "model_name", "modelName"} {
				if name, ok := stringValue(item, key); ok && name != "" {
					visit(name)
					break
				}
			}
		case string:
			name := strings.TrimPrefix(strings.TrimSpace(item), "models/")
			if name != "" {
				if _, exists := seen[name]; !exists {
					seen[name] = struct{}{}
					out = append(out, name)
				}
			}
		}
	}
	visit(value)
	return out
}

func (p *Sub2API) firstUsableAPIKey(ctx context.Context, req AccountRequest) (string, error) {
	items, err := p.ListRoutingKeys(ctx, req)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if item.Enabled && item.Key != "" {
			return item.Key, nil
		}
	}
	return "", &Error{Code: CodeUnsupported, Message: "Sub2API has no usable API key for model discovery"}
}

func (p *Sub2API) ListRoutingKeys(ctx context.Context, req AccountRequest) ([]RoutingKeySnapshot, error) {
	if req.Credentials.AccessToken == "" {
		return nil, &Error{Code: CodeUnsupported, Message: "Sub2API routing key discovery requires a JWT access token"}
	}
	for _, endpoint := range []string{"/api/v1/keys?page=1&page_size=100", "/api/v1/api-keys?page=1&page_size=100", "/api/v1/keys", "/api/v1/api-keys"} {
		data, err := p.requestData(ctx, req, http.MethodGet, endpoint)
		if err != nil {
			continue
		}
		items := collectionItems(data)
		out := make([]RoutingKeySnapshot, 0, len(items))
		for index, item := range items {
			key := firstSub2String(item, "key", "token", "api_key", "apiKey", "access_token", "accessToken")
			if key == "" {
				continue
			}
			name, _ := stringValue(item, "name")
			if name == "" {
				name = fmt.Sprintf("token-%d", index+1)
			}
			itemMap, _ := item.(map[string]any)
			id := strings.TrimSpace(fmt.Sprint(itemMap["id"]))
			if id == "<nil>" {
				id = ""
			}
			group := firstSub2String(item, "group", "group_name", "groupName")
			models := modelNames(itemMap["models"])
			if len(models) == 0 {
				models = modelNames(itemMap["allowed_models"])
			}
			out = append(out, RoutingKeySnapshot{ID: id, Name: name, Group: group, Models: models, Key: key, Enabled: sub2RoutingKeyEnabled(item)})
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	return nil, &Error{Code: CodeUnsupported, Message: "Sub2API has no usable API key for model discovery"}
}

func firstSub2String(value any, keys ...string) string {
	for _, key := range keys {
		if result, ok := stringValue(value, key); ok && result != "" {
			return result
		}
	}
	return ""
}

func sub2RoutingKeyEnabled(value any) bool {
	if item, ok := value.(map[string]any); ok {
		for _, key := range []string{"disabled", "enabled", "is_enabled", "active", "status"} {
			raw, exists := item[key]
			if !exists {
				continue
			}
			if key == "disabled" {
				return strings.EqualFold(strings.TrimSpace(fmt.Sprint(raw)), "false") || strings.TrimSpace(fmt.Sprint(raw)) == "0"
			}
			status := strings.TrimSpace(fmt.Sprint(raw))
			return !isDisabledProviderStatus(status)
		}
	}
	return true
}

func (p *Sub2API) probe(ctx context.Context, baseURL, path string) (sub2Envelope, string, error) {
	if err := ValidateBaseURL(baseURL, p.clients.AllowPrivate); err != nil {
		return sub2Envelope{}, "", err
	}
	client, err := p.clients.New("")
	if err != nil {
		return sub2Envelope{}, "", err
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return sub2Envelope{}, "", err
	}
	target, err := base.Parse(path)
	if err != nil || !strings.EqualFold(target.Hostname(), base.Hostname()) {
		return sub2Envelope{}, "", errors.New("provider target changed host")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return sub2Envelope{}, "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return sub2Envelope{}, "", &Error{Code: CodeRequestFailed, Message: "provider request failed"}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || int64(len(raw)) > maxResponseBytes {
		return sub2Envelope{}, "", &Error{Code: CodeInvalidResponse, Message: "invalid provider response"}
	}
	var payload sub2Envelope
	if err := json.Unmarshal(raw, &payload); err != nil {
		return sub2Envelope{}, resp.Header.Get("Content-Type"), &Error{Code: CodeInvalidResponse, Message: "provider returned invalid JSON"}
	}
	return payload, resp.Header.Get("Content-Type"), nil
}

func collectionItems(value any) []any {
	if items, ok := value.([]any); ok {
		return items
	}
	if m, ok := value.(map[string]any); ok {
		for _, key := range []string{"items", "list", "data", "records", "rows"} {
			if items, ok := m[key].([]any); ok {
				return items
			}
		}
	}
	return nil
}

func valueFromMap(value any, key string) any {
	if m, ok := value.(map[string]any); ok {
		return m[key]
	}
	return nil
}

func parseProviderTime(value any) int64 {
	switch v := value.(type) {
	case float64:
		if v > 1e12 {
			return int64(v)
		}
		return int64(v * 1000)
	case string:
		if numeric, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			if numeric > 1e12 {
				return numeric
			}
			return numeric * 1000
		}
		if parsed, err := time.Parse(time.RFC3339, v); err == nil {
			return parsed.UnixMilli()
		}
	}
	return 0
}

func isDisabledProviderStatus(status string) bool {
	normalized := strings.ToLower(strings.TrimSpace(status))
	return normalized == "inactive" || normalized == "disabled" || normalized == "false" || normalized == "0" || normalized == "quota_exhausted" || normalized == "expired"
}
