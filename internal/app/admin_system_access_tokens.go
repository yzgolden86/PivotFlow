package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
)

// RequireSystemAccessToken authenticates only Authorization: Bearer <token>.
// It deliberately does not accept cookies, query parameters, or model API-key
// headers so management credentials cannot be confused with proxy credentials.
func (s *Server) RequireSystemAccessToken(requiredScope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := strings.TrimSpace(c.GetHeader("Authorization"))
		if !strings.HasPrefix(auth, "Bearer ") {
			RespondErrorMsg(c, http.StatusUnauthorized, "system access token required")
			c.Abort()
			return
		}
		plain := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		if plain == "" || strings.ContainsAny(plain, "\r\n") {
			RespondErrorMsg(c, http.StatusUnauthorized, "system access token required")
			c.Abort()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		token, err := s.store.GetSystemAccessTokenByHash(ctx, model.HashSystemAccessToken(plain))
		if err != nil || token == nil || !token.IsValid(time.Now()) {
			RespondErrorMsg(c, http.StatusUnauthorized, "invalid or expired system access token")
			c.Abort()
			return
		}
		if requiredScope != "" && !token.HasScope(requiredScope) {
			RespondErrorMsg(c, http.StatusForbidden, "system access token scope is insufficient")
			c.Abort()
			return
		}
		c.Set("pivotflow_system_access_token", token)
		// Diagnostic traffic is low-volume. A bounded, best-effort write avoids
		// spawning an unbounded goroutine for every authenticated request.
		_ = s.store.UpdateSystemAccessTokenLastUsed(ctx, model.HashSystemAccessToken(plain), time.Now())
		c.Next()
	}
}

func generateSystemAccessToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "pf_sys_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func systemAccessTokenResponse(token *model.SystemAccessToken, plain string) gin.H {
	response := gin.H{
		"id": token.ID, "token_hint": token.TokenHint, "description": token.Description,
		"scopes": token.Scopes, "created_at": token.CreatedAt, "last_used_at": token.LastUsedAt,
		"expires_at": token.ExpiresAt, "is_active": token.IsActive,
	}
	if plain != "" {
		response["token"] = plain
	}
	return response
}

// HandleListSystemAccessTokens lists management tokens without secrets.
func (s *Server) HandleListSystemAccessTokens(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	tokens, err := s.store.ListSystemAccessTokens(ctx)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	RespondJSON(c, http.StatusOK, gin.H{"tokens": tokens, "scopes": model.SystemAccessScopes})
}

func (s *Server) HandleCreateSystemAccessToken(c *gin.Context) {
	var req struct {
		Description string   `json:"description" binding:"required"`
		Scopes      []string `json:"scopes"`
		ExpiresAt   int64    `json:"expires_at"`
		IsActive    *bool    `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Description) == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "description is required")
		return
	}
	scopes := req.Scopes
	if len(scopes) == 0 {
		scopes = append([]string(nil), model.SystemAccessScopes...)
	}
	normalized, err := model.NormalizeSystemAccessScopes(scopes)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}
	plain, err := generateSystemAccessToken()
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "failed to generate system access token")
		return
	}
	active := true
	if req.IsActive != nil {
		active = *req.IsActive
	}
	token := &model.SystemAccessToken{Token: model.HashSystemAccessToken(plain), TokenHint: model.MaskSystemAccessToken(plain), Description: strings.TrimSpace(req.Description), Scopes: normalized, CreatedAt: time.Now().UnixMilli(), ExpiresAt: req.ExpiresAt, IsActive: active}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := s.store.CreateSystemAccessToken(ctx, token); err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	RespondJSON(c, http.StatusOK, systemAccessTokenResponse(token, plain))
}

func (s *Server) HandleUpdateSystemAccessToken(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid system access token id")
		return
	}
	var req struct {
		Description *string   `json:"description"`
		Scopes      *[]string `json:"scopes"`
		ExpiresAt   *int64    `json:"expires_at"`
		IsActive    *bool     `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	tokens, listErr := s.store.ListSystemAccessTokens(ctx)
	if listErr != nil {
		RespondError(c, http.StatusInternalServerError, listErr)
		return
	}
	var token *model.SystemAccessToken
	for _, item := range tokens {
		if item.ID == id {
			token = item
			break
		}
	}
	if token == nil {
		RespondErrorMsg(c, http.StatusNotFound, "system access token not found")
		return
	}
	if req.Description != nil {
		token.Description = strings.TrimSpace(*req.Description)
		if token.Description == "" {
			RespondErrorMsg(c, http.StatusBadRequest, "description is required")
			return
		}
	}
	if req.Scopes != nil {
		normalized, normalizeErr := model.NormalizeSystemAccessScopes(*req.Scopes)
		if normalizeErr != nil {
			RespondErrorMsg(c, http.StatusBadRequest, normalizeErr.Error())
			return
		}
		token.Scopes = normalized
	}
	if req.ExpiresAt != nil {
		token.ExpiresAt = *req.ExpiresAt
	}
	if req.IsActive != nil {
		token.IsActive = *req.IsActive
	}
	if err := s.store.UpdateSystemAccessToken(ctx, token); err != nil {
		if errors.Is(err, model.ErrSystemAccessTokenNotFound) {
			RespondErrorMsg(c, http.StatusNotFound, "system access token not found")
			return
		}
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	RespondJSON(c, http.StatusOK, systemAccessTokenResponse(token, ""))
}

func (s *Server) HandleDeleteSystemAccessToken(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid system access token id")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := s.store.DeleteSystemAccessToken(ctx, id); err != nil {
		if errors.Is(err, model.ErrSystemAccessTokenNotFound) {
			RespondErrorMsg(c, http.StatusNotFound, "system access token not found")
			return
		}
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	RespondJSON(c, http.StatusOK, gin.H{"id": id})
}

// HandleSystemAPIChannels exposes a deliberately small, credential-free view
// for diagnostic clients authenticated with a system access token.
func (s *Server) HandleSystemAPIChannels(c *gin.Context) {
	configs, err := s.store.ListConfigs(c.Request.Context())
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	type channelView struct {
		ID       int64    `json:"id"`
		Name     string   `json:"name"`
		Enabled  bool     `json:"enabled"`
		Priority int      `json:"priority"`
		Protocol string   `json:"protocol_transform_mode"`
		Models   []string `json:"models"`
		URLHosts []string `json:"url_hosts,omitempty"`
	}
	views := make([]channelView, 0, len(configs))
	for _, cfg := range configs {
		if cfg == nil {
			continue
		}
		models := make([]string, 0, len(cfg.ModelEntries))
		for _, entry := range cfg.ModelEntries {
			if !entry.Disabled {
				models = append(models, entry.Model)
			}
		}
		hosts := make([]string, 0, len(cfg.URLs))
		for _, entry := range cfg.URLs {
			if parsed, parseErr := url.Parse(entry.URL); parseErr == nil && parsed.Host != "" {
				hosts = append(hosts, parsed.Scheme+"://"+parsed.Host)
			}
		}
		views = append(views, channelView{ID: cfg.ID, Name: cfg.Name, Enabled: cfg.Enabled, Priority: cfg.Priority, Protocol: cfg.GetProtocolTransformMode(), Models: models, URLHosts: hosts})
	}
	RespondJSON(c, http.StatusOK, gin.H{"channels": views})
}

func (s *Server) HandleSystemAPIRouteDiagnostics(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid channel id")
		return
	}
	modelName := strings.TrimSpace(c.Query("model"))
	if modelName == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "model is required")
		return
	}
	clientProtocol := normalizeOptionalProtocol(c.DefaultQuery("client_protocol", "openai"))
	result, err := s.buildChannelRouteDiagnostics(c.Request.Context(), id, modelName, clientProtocol, 0)
	if err != nil {
		RespondError(c, http.StatusBadGateway, err)
		return
	}
	RespondJSON(c, http.StatusOK, result)
}

var systemDiagnosticBearerPattern = regexp.MustCompile(`(?i)\b(?:authorization\s*:\s*)?bearer\s+[A-Za-z0-9._~+/=-]+`)

func redactSystemDiagnosticMessage(message string) string {
	message = tokenLogURLPattern.ReplaceAllString(message, "[redacted-url]")
	message = systemDiagnosticBearerPattern.ReplaceAllString(message, "[redacted-secret]")
	message = tokenLogSecretPattern.ReplaceAllString(message, "[redacted-secret]")
	runes := []rune(message)
	if len(runes) > 512 {
		return string(runes[:512]) + "…"
	}
	return message
}

func (s *Server) HandleSystemAPILogs(c *gin.Context) {
	params := ParsePaginationParams(c)
	since, until := params.GetTimeRange()
	filter := BuildLogFilter(c)
	minimumErrorStatus := http.StatusBadRequest
	filter.StatusCodeMin = &minimumErrorStatus
	logs, total, err := s.store.ListLogsRangeWithCount(c.Request.Context(), since, until, params.Limit, params.Offset, &filter)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	type logView struct {
		ID               int64          `json:"id"`
		Time             model.JSONTime `json:"time"`
		ChannelID        int64          `json:"channel_id"`
		ChannelName      string         `json:"channel_name"`
		Model            string         `json:"model"`
		StatusCode       int            `json:"status_code"`
		Message          string         `json:"message"`
		Duration         float64        `json:"duration"`
		ClientProtocol   string         `json:"client_protocol,omitempty"`
		UpstreamProtocol string         `json:"upstream_protocol,omitempty"`
	}
	views := make([]logView, 0, len(logs))
	for _, entry := range logs {
		if entry == nil || entry.StatusCode < 400 {
			continue
		}
		views = append(views, logView{ID: entry.ID, Time: entry.Time, ChannelID: entry.ChannelID, ChannelName: entry.ChannelName, Model: entry.Model, StatusCode: entry.StatusCode, Message: redactSystemDiagnosticMessage(entry.Message), Duration: entry.Duration, ClientProtocol: entry.ClientProtocol, UpstreamProtocol: entry.UpstreamProtocol})
	}
	RespondJSONWithCount(c, http.StatusOK, views, total)
}

func (s *Server) HandleSystemAPIMetrics(c *gin.Context) {
	params := ParsePaginationParams(c)
	bucketMin, _ := strconv.Atoi(c.DefaultQuery("bucket_min", "5"))
	if bucketMin <= 0 {
		bucketMin = 5
	}
	filter := BuildLogFilter(c)
	filter.LogSource = model.LogSourceProxy
	since, until := params.GetTimeRange()
	points, err := s.store.AggregateRangeWithFilter(c.Request.Context(), since, until, time.Duration(bucketMin)*time.Minute, &filter)
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}
	RespondJSON(c, http.StatusOK, points)
}
