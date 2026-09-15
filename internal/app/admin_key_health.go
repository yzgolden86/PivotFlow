package app

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/util"
)

type channelKeyHealthItem struct {
	ID              int64              `json:"id"`
	KeyIndex        int                `json:"key_index"`
	MaskedKey       string             `json:"masked_key"`
	Note            string             `json:"note"`
	Disabled        bool               `json:"disabled"`
	CooldownUntil   int64              `json:"cooldown_until"`
	Health          model.APIKeyHealth `json:"health"`
	AllowedModels   []string           `json:"allowed_models,omitempty"`
	ModelScopeEmpty bool               `json:"model_scope_empty,omitempty"`
	CostMultiplier  float64            `json:"cost_multiplier"`
}

// HandleChannelKeyHealth returns fresh observations without disclosing credentials.
func (s *Server) HandleChannelKeyHealth(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid channel id")
		return
	}
	if !s.requireMutableAPIKeys(c, id) {
		return
	}
	cfg, err := s.store.GetConfig(c.Request.Context(), id)
	if err != nil {
		RespondErrorMsg(c, http.StatusNotFound, "channel not found")
		return
	}
	keys, err := s.store.GetAPIKeys(c.Request.Context(), id)
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "读取 Key 状态失败")
		return
	}
	items := make([]channelKeyHealthItem, 0, len(keys))
	for _, key := range keys {
		items = append(items, channelKeyHealthItem{
			ID: key.ID, KeyIndex: key.KeyIndex, MaskedKey: util.MaskAPIKey(key.APIKey), Note: key.Note,
			Disabled: key.Disabled, CooldownUntil: key.CooldownUntil, Health: key.Health,
			AllowedModels: key.AllowedModels, ModelScopeEmpty: key.ModelScopeEmpty, CostMultiplier: key.CostMultiplier,
		})
	}
	c.Header("Cache-Control", "no-store")
	RespondJSON(c, http.StatusOK, gin.H{"channel_id": id, "channel_name": cfg.Name, "models": cfg.GetModels(), "keys": items})
}

func requireAPIKeyIdentity(c *gin.Context, expected *int64, actual int64) bool {
	if expected != nil && (*expected <= 0 || *expected != actual) {
		RespondErrorMsg(c, http.StatusConflict, "Key 列表已变化，请刷新后重试")
		return false
	}
	return true
}
