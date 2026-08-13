package app

import (
	"net/http"
	"strings"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/testutil"

	"github.com/gin-gonic/gin"
)

// siteModelProbeRequest deliberately mirrors the small interactive subset of
// TestChannelRequest. A site probe is a transient control-plane operation: it
// must not create a channel, write request logs, or mutate cooldown state.
type siteModelProbeRequest struct {
	Model          string `json:"model"`
	Content        string `json:"content,omitempty"`
	Stream         bool   `json:"stream,omitempty"`
	ClientProtocol string `json:"client_protocol"`
}

func (r *siteModelProbeRequest) Validate() error {
	r.Model = strings.TrimSpace(r.Model)
	r.Content = strings.TrimSpace(r.Content)
	r.ClientProtocol = strings.ToLower(strings.TrimSpace(r.ClientProtocol))
	return (&testutil.TestChannelRequest{
		Model:          r.Model,
		Content:        r.Content,
		Stream:         r.Stream,
		ClientProtocol: r.ClientProtocol,
	}).Validate()
}

// HandleSiteAccountModelProbe executes a model request directly against a
// managed site account while reusing PivotFlow's protocol conversion and response
// parsing. The transient config is never persisted and the raw upstream body is
// intentionally omitted from the normalized response.
func (s *Server) HandleSiteAccountModelProbe(c *gin.Context) {
	accountID, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid_request")
		return
	}
	var req siteModelProbeRequest
	if err := BindAndValidate(c, &req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid model probe request")
		return
	}
	if s.siteControl == nil {
		RespondErrorMsg(c, http.StatusServiceUnavailable, "site_control_unavailable")
		return
	}

	account, err := s.store.GetSiteAccount(c.Request.Context(), accountID)
	if err != nil {
		RespondErrorMsg(c, http.StatusNotFound, "not_found")
		return
	}
	site, err := s.store.GetSite(c.Request.Context(), account.SiteID)
	if err != nil {
		RespondErrorMsg(c, http.StatusNotFound, "not_found")
		return
	}
	if !account.Enabled || !site.Enabled {
		RespondErrorMsg(c, http.StatusConflict, "site_account_disabled")
		return
	}

	models, err := s.store.ListSiteAccountModels(c.Request.Context(), model.SiteModelFilter{
		SiteAccountID: account.ID,
		Limit:         1000,
	})
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	var fact *model.SiteAccountModel
	for index := range models {
		if models[index].Model == req.Model && !models[index].Disabled {
			fact = &models[index]
			break
		}
	}
	if fact == nil {
		RespondErrorMsg(c, http.StatusBadRequest, "model_not_in_account_inventory")
		return
	}

	adapter, err := s.siteControl.adapter(site)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, provider.ErrorCode(err))
		return
	}
	credentials, err := s.siteControl.operationCredentials(c.Request.Context(), account, site, adapter)
	if err != nil {
		status := http.StatusBadRequest
		if err == credential.ErrCredentialLocked {
			status = http.StatusLocked
		}
		message := provider.ErrorCode(err)
		if status == http.StatusLocked {
			message = "credential_locked"
		}
		RespondErrorMsg(c, status, message)
		return
	}
	credentials, err = s.siteControl.ensureRoutingKey(c.Request.Context(), account, site, adapter, credentials)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, provider.ErrorCode(err))
		return
	}
	upstreamProtocol := siteModelUpstreamProtocol(fact.RouteType)
	transient := &model.Config{
		ID:                    -account.ID,
		Name:                  site.Name + " / " + account.Label,
		AuthType:              model.AuthTypeAPIKey,
		ProtocolTransformMode: model.ProtocolTransformModeLocal,
		URLs:                  model.ChannelURLs{{URL: site.BaseURL, Protocols: []string{upstreamProtocol}}},
		ModelEntries:          []model.ModelEntry{{Model: req.Model}},
		Enabled:               true,
		CostMultiplier:        1,
		ProxyURL:              site.ProxyURL,
	}
	testReq := &testutil.TestChannelRequest{
		Model:          req.Model,
		Content:        req.Content,
		Stream:         req.Stream,
		ClientProtocol: req.ClientProtocol,
	}
	result := s.testChannelAPI(c.Request.Context(), transient, strings.TrimSpace(credentials.APIKey), testReq)
	RespondJSON(c, http.StatusOK, normalizeSiteModelProbeResult(result, site, account, req.Model))
}

func siteModelUpstreamProtocol(routeType string) string {
	switch strings.ToLower(strings.TrimSpace(routeType)) {
	case "anthropic":
		return "anthropic"
	case "gemini":
		return "gemini"
	case "openai_response", "codex":
		return "codex"
	default:
		return "openai"
	}
}

func normalizeSiteModelProbeResult(result map[string]any, site *model.Site, account *model.SiteAccount, requestedModel string) gin.H {
	success, _ := result["success"].(bool)
	statusCode, _ := getResultInt(result["status_code"])
	status, reason := classifySiteModelProbe(success, statusCode, stringResult(result, "error"))
	duration, _ := getResultInt64(result["duration_ms"])
	firstByte, _ := getResultInt64(result["first_byte_duration_ms"])

	out := gin.H{
		"success":                success,
		"status":                 status,
		"reason":                 reason,
		"model":                  requestedModel,
		"actual_model":           fallbackString(stringResult(result, "actual_model"), requestedModel),
		"protocol":               stringResult(result, "upstream_protocol"),
		"client_protocol":        stringResult(result, "client_protocol"),
		"upstream_protocol":      stringResult(result, "upstream_protocol"),
		"latency_ms":             duration,
		"duration_ms":            duration,
		"first_byte_ms":          firstByte,
		"first_byte_duration_ms": firstByte,
		"base_url":               fallbackString(stringResult(result, "base_url"), site.BaseURL),
		"site_id":                site.ID,
		"site_account_id":        account.ID,
		"source_type":            "site_account",
	}
	if statusCode > 0 {
		out["status_code"] = statusCode
	}
	copyProbeNumber(result, out, "input_tokens")
	copyProbeNumber(result, out, "output_tokens")
	copyProbeNumber(result, out, "cost_usd")
	if text := strings.TrimSpace(stringResult(result, "response_text")); text != "" {
		if len(text) > 4000 {
			text = text[:4000]
		}
		out["response_text"] = text
	}
	return out
}

func classifySiteModelProbe(success bool, statusCode int, rawError string) (string, string) {
	if success {
		return "pass", ""
	}
	lower := strings.ToLower(rawError)
	switch {
	case statusCode == http.StatusNotFound || strings.Contains(lower, "not support") || strings.Contains(lower, "unsupported") || strings.Contains(lower, "模型不存在") || strings.Contains(lower, "模型不可用"):
		return "unsupported", "model_or_endpoint_unsupported"
	case statusCode == http.StatusTooManyRequests || statusCode == http.StatusServiceUnavailable:
		return "inconclusive", "upstream_temporarily_unavailable"
	case statusCode == 0 && (strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline")):
		return "inconclusive", "upstream_timeout"
	case statusCode >= 500 || statusCode == 0:
		return "inconclusive", "upstream_request_failed"
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return "fail", "credential_rejected"
	default:
		return "fail", "probe_failed"
	}
}

func copyProbeNumber(source map[string]any, target gin.H, key string) {
	if value, ok := source[key]; ok {
		target[key] = value
	}
}

func stringResult(result map[string]any, key string) string {
	value, _ := result[key].(string)
	return strings.TrimSpace(value)
}

func fallbackString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
