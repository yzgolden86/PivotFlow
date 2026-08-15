package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// OpenAICompatible is the final, API-key-only fallback for upstreams that
// expose the standard OpenAI models API but do not expose a supported
// management plane. It must be registered after all management providers.
type OpenAICompatible struct{ clients ClientFactory }

func NewOpenAICompatible(factory ClientFactory) *OpenAICompatible {
	return &OpenAICompatible{clients: factory}
}

func (p *OpenAICompatible) ID() string { return model.SitePlatformOpenAICompatible }

func (p *OpenAICompatible) Capabilities() ProviderCapabilities {
	return ProviderCapabilities{
		Models:          true,
		CredentialTypes: []string{model.CredentialTypeAPIKey},
	}
}

func (p *OpenAICompatible) Detect(ctx context.Context, baseURL string) (DetectionResult, error) {
	status, contentType, raw, err := p.requestModels(ctx, AccountRequest{BaseURL: baseURL})
	if err != nil {
		return DetectionResult{}, err
	}
	matched := false
	if status >= 200 && status < 300 {
		_, matched = openAIModelNames(raw)
	} else if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests {
		matched = openAIEndpointError(contentType, raw)
	}
	return DetectionResult{Matched: matched, ProviderID: p.ID(), SystemName: "OpenAI Compatible", Capabilities: p.Capabilities()}, nil
}

func (p *OpenAICompatible) RefreshAccount(context.Context, RefreshAccountRequest) (AccountSnapshot, error) {
	return AccountSnapshot{}, &Error{Code: CodeUnsupported, Message: "OpenAI-compatible API keys do not expose account balance"}
}

func (p *OpenAICompatible) ListModels(ctx context.Context, req AccountRequest) ([]ModelSnapshot, error) {
	if strings.TrimSpace(req.Credentials.APIKey) == "" {
		return nil, &Error{Code: CodeExpired, Message: "an API key is required for model discovery"}
	}
	status, contentType, raw, err := p.requestModels(ctx, req)
	if err != nil {
		return nil, err
	}
	if looksLikeChallenge(contentType, raw) {
		return nil, &Error{Code: CodeBrowserRequired, StatusCode: status, Message: "browser verification is required"}
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, &Error{Code: CodeExpired, StatusCode: status, Message: "provider API key was rejected"}
	}
	if status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
		return nil, &Error{Code: CodeRateLimited, StatusCode: status, Message: "provider is rate limited"}
	}
	if status == http.StatusNotFound {
		return nil, &Error{Code: CodeUnsupported, StatusCode: status, Message: "OpenAI-compatible /v1/models endpoint is unavailable"}
	}
	if status < 200 || status >= 300 {
		return nil, &Error{Code: CodeRequestFailed, StatusCode: status, Message: "provider returned an unexpected response"}
	}
	names, shapeOK := openAIModelNames(raw)
	if !shapeOK {
		return nil, &Error{Code: CodeInvalidResponse, StatusCode: status, Message: "models endpoint did not return an OpenAI-compatible response"}
	}
	if len(names) == 0 {
		return nil, &Error{Code: CodeUnsupported, StatusCode: status, Message: "models endpoint returned no models"}
	}
	out := make([]ModelSnapshot, 0, len(names))
	for _, name := range names {
		out = append(out, ModelSnapshot{Model: name, RouteType: "openai_chat", Source: "models_endpoint"})
	}
	return out, nil
}

func (p *OpenAICompatible) Checkin(context.Context, AccountRequest) (CheckinResult, error) {
	return CheckinResult{Status: CheckinUnsupported}, &Error{Code: CodeUnsupported, Message: "OpenAI-compatible upstreams do not expose check-in"}
}

func (p *OpenAICompatible) ListAnnouncements(context.Context, AccountRequest) ([]Announcement, error) {
	return nil, &Error{Code: CodeUnsupported, Message: "OpenAI-compatible upstreams do not expose announcements"}
}

func (p *OpenAICompatible) requestModels(ctx context.Context, req AccountRequest) (int, string, []byte, error) {
	if err := ValidateBaseURL(req.BaseURL, p.clients.AllowPrivate); err != nil {
		return 0, "", nil, err
	}
	target, err := openAIModelsEndpoint(req.BaseURL)
	if err != nil {
		return 0, "", nil, err
	}
	client, err := p.clients.New(req.ProxyURL)
	if err != nil {
		return 0, "", nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, "", nil, err
	}
	httpReq.Header.Set("Accept", "application/json")
	if key := strings.TrimSpace(req.Credentials.APIKey); key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+strings.TrimPrefix(key, "Bearer "))
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return 0, "", nil, &Error{Code: CodeTimeout, Message: "provider request timed out"}
		}
		return 0, "", nil, &Error{Code: CodeRequestFailed, Message: "provider request failed"}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return 0, "", nil, &Error{Code: CodeRequestFailed, Message: "read provider response"}
	}
	if int64(len(raw)) > maxResponseBytes {
		return 0, "", nil, &Error{Code: CodeInvalidResponse, Message: "provider response exceeds size limit"}
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), raw, nil
}

func openAIModelsEndpoint(rawBaseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil {
		return "", err
	}
	cleanPath := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(cleanPath, "/v1/models"):
		parsed.Path = cleanPath
	case strings.HasSuffix(cleanPath, "/v1"):
		parsed.Path = cleanPath + "/models"
	default:
		parsed.Path = path.Join(cleanPath, "/v1/models")
	}
	parsed.RawPath = ""
	parsed.RawQuery = ""
	return parsed.String(), nil
}

func openAIModelNames(raw []byte) ([]string, bool) {
	var payload any
	if json.Unmarshal(raw, &payload) != nil {
		return nil, false
	}
	root, ok := payload.(map[string]any)
	if !ok {
		return nil, false
	}
	var collection any
	for _, key := range []string{"data", "models", "items"} {
		if value, exists := root[key]; exists {
			collection = value
			break
		}
	}
	if collection == nil {
		return nil, false
	}
	items, ok := collection.([]any)
	if !ok {
		return nil, false
	}
	seen := make(map[string]struct{}, len(items))
	names := make([]string, 0, len(items))
	for _, item := range items {
		name := ""
		switch value := item.(type) {
		case string:
			name = value
		case map[string]any:
			for _, key := range []string{"id", "model", "name"} {
				if candidate, exists := value[key].(string); exists && strings.TrimSpace(candidate) != "" {
					name = candidate
					break
				}
			}
		}
		name = strings.TrimPrefix(strings.TrimSpace(name), "models/")
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names, true
}

func openAIEndpointError(contentType string, raw []byte) bool {
	if !strings.Contains(strings.ToLower(contentType), "json") {
		return false
	}
	text := strings.ToLower(string(raw))
	for _, marker := range []string{"api key", "api_key", "bearer", "authorization", "unauthorized", "authentication", "invalid_api_key"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}
