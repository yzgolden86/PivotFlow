package app

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/protocol"
)

// ============================================================================
// Gemini API 特殊处理
// ============================================================================

func (s *Server) filterVisibleModelsForRequest(c *gin.Context, clientProtocol string, channels []*model.Config) []string {
	aliases := s.modelAliases.frozen()
	tokenHash, _ := c.Get("token_hash")
	tokenHashStr, _ := tokenHash.(string)
	if s.authService != nil && tokenHashStr != "" && !s.authService.IsProtocolAllowed(tokenHashStr, clientProtocol) {
		return nil
	}
	allowed := make(map[string]struct{})
	client := protocol.Protocol(clientProtocol)
	for _, cfg := range channels {
		if cfg == nil {
			continue
		}
		if s.authService != nil && tokenHashStr != "" {
			if restriction, restricted := s.authService.getChannelRestriction(tokenHashStr); restricted && !restriction.Allows(cfg.ID) {
				continue
			}
		}
		// Use the same declaration/transform-family rules as the real proxy,
		// without executing requests, cooling channels or mutating snapshots.
		compatible := false
		for _, entry := range cfg.URLs {
			candidates, _ := protocolCandidatesForURL(entry, cfg.GetProtocolTransformMode(), client, channelTestRequestFamily(client), localUpstreamProtocolOrder(cfg.URLs))
			if len(candidates) > 0 {
				compatible = true
				break
			}
		}
		if compatible {
			for _, name := range aliases.canonicalizeModelNames(cfg.GetModels()) {
				allowed[name] = struct{}{}
			}
		}
	}
	visible := make([]string, 0, len(allowed))
	for name := range allowed {
		visible = append(visible, name)
	}
	if s.authService != nil && tokenHashStr != "" {
		candidates := s.authService.modelAliasCandidates
		if candidates != nil {
			candidates = aliases.namesFor
		}
		return s.authService.filterAllowedModelsWithCandidates(tokenHashStr, visible, candidates)
	}
	return visible
}

// handleListGeminiModels 处理 GET /v1beta/models 请求，返回本地 Gemini 模型列表
// 从proxy.go提取，遵循SRP原则
func (s *Server) handleListGeminiModels(c *gin.Context) {
	ctx := c.Request.Context()

	channels, err := s.GetEnabledChannelsByModel(ctx, "*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load models"})
		return
	}
	models := s.filterVisibleModelsForRequest(c, "gemini", channels)
	sort.Strings(models)

	// 构造 Gemini API 响应格式
	type ModelInfo struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
	}

	modelList := make([]ModelInfo, 0, len(models))
	for _, model := range models {
		modelList = append(modelList, ModelInfo{
			Name:        "models/" + model,
			DisplayName: formatModelDisplayName(model),
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"models": modelList,
	})
}

// detectModelsClientProtocol 根据请求头判断 /v1/models 的客户端协议。
// anthropic-version 头存在 → anthropic 渠道；否则 → openai 渠道
func detectModelsClientProtocol(c *gin.Context) string {
	if c.GetHeader("anthropic-version") != "" {
		return "anthropic"
	}
	if strings.HasPrefix(strings.ToLower(c.GetHeader("User-Agent")), "claude-cli") {
		return "anthropic"
	}
	if strings.Contains(strings.ToLower(c.GetHeader("User-Agent")), "codex") {
		return "codex"
	}
	return "openai"
}

func requestedModelsClientProtocol(c *gin.Context) (string, bool) {
	values, specified := c.Request.URL.Query()["protocol"]
	if !specified {
		return detectModelsClientProtocol(c), true
	}
	if len(values) != 1 || strings.ContainsAny(values[0], "\r\n\x00") {
		return "", false
	}
	view := strings.ToLower(strings.TrimSpace(values[0]))
	switch view {
	case "openai", "codex", "anthropic":
		return view, true
	default:
		return "", false
	}
}

// handleListOpenAIModels 处理 GET /v1/models 请求，根据请求类型返回对应渠道的模型列表
func (s *Server) handleListOpenAIModels(c *gin.Context) {
	ctx := c.Request.Context()

	clientProtocol, validView := requestedModelsClientProtocol(c)
	if !validView {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "code": "invalid_protocol_view", "message": "protocol must be openai, codex or anthropic"}})
		return
	}
	channels, err := s.GetEnabledChannelsByModel(ctx, "*")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load models"})
		return
	}
	models := s.filterVisibleModelsForRequest(c, clientProtocol, channels)
	sort.Strings(models)

	if clientProtocol == "anthropic" {
		type ModelInfo struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
			Type        string `json:"type"`
			CreatedAt   string `json:"created_at"`
		}
		modelList := make([]ModelInfo, 0, len(models))
		for _, model := range models {
			modelList = append(modelList, ModelInfo{
				ID:          model,
				DisplayName: formatModelDisplayName(model),
				Type:        "model",
				CreatedAt:   time.Unix(0, 0).UTC().Format(time.RFC3339),
			})
		}

		resp := gin.H{
			"data":     modelList,
			"has_more": false,
		}
		if len(modelList) > 0 {
			resp["first_id"] = modelList[0].ID
			resp["last_id"] = modelList[len(modelList)-1].ID
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	// 构造 OpenAI API 响应格式
	type ModelInfo struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}

	modelList := make([]ModelInfo, 0, len(models))
	for _, model := range models {
		modelList = append(modelList, ModelInfo{
			ID:      model,
			Object:  "model",
			Created: 0,
			OwnedBy: "system",
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   modelList,
	})
}
