package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/version"

	"github.com/gin-gonic/gin"
)

// 配置验证常量
const (
	LogRetentionDaysMin      = 1
	LogRetentionDaysMax      = 365
	LogRetentionDaysDisabled = -1 // 永久保留
)

type adminSystemSetting struct {
	*model.SystemSetting
	Editable        bool   `json:"editable"`
	DisabledReason  string `json:"disabled_reason,omitempty"`
	RuntimeEffect   string `json:"runtime_effect"`
	RequiresRestart bool   `json:"requires_restart"`
}

const containerImageManagedDisabledReason = "container_image_managed"
const runtimeConsumerMissingDisabledReason = "runtime_consumer_missing"

// systemSettingActivation is the single registry for settings exposed by the
// admin UI. Keeping the consumer and activation mode together prevents a new
// setting from silently becoming a persistence-only shell.
type systemSettingActivation struct {
	effect          string
	requiresRestart bool
}

var systemSettingRuntimeEffects = map[string]string{
	"log_retention_days":                      "请求日志清理任务",
	"max_key_retries":                         "渠道内 Key 重试循环",
	"max_concurrency":                         "代理请求全局并发控制",
	"max_body_bytes":                          "普通请求体读取上限",
	"max_image_body_bytes":                    "图片请求体读取上限",
	"cooldown_auth_seconds":                   "认证错误冷却策略",
	"cooldown_server_seconds":                 "上游服务错误冷却策略",
	"cooldown_timeout_seconds":                "上游超时冷却策略",
	"cooldown_rate_limit_seconds":             "上游限流冷却策略",
	"cooldown_max_seconds":                    "指数退避冷却上限",
	"cooldown_min_seconds":                    "指数退避冷却下限",
	"global_cooldown_detection_rules":         "全局上游错误分类器",
	"antigravity_sensitive_words":             "Antigravity 请求兼容处理",
	"upstream_first_byte_timeout":             "全局流式首字超时",
	"upstream_connection_reuse_limit_seconds": "上游连接池复用时限",
	"stream_timeout":                          "流式请求总超时",
	"non_stream_timeout":                      "非流式请求总超时",
	"route_strategy":                          "渠道选择策略",
	"model_fuzzy_match":                       "模型候选选择器",
	"model_alias_groups":                      "全局模型统一映射",
	"channel_test_content":                    "手动测试与定时巡检",
	"channel_check_interval_hours":            "渠道定时巡检调度器",
	"site_daily_checkin_time":                 "站点每日签到调度器",
	"site_daily_announcement_time":            "站点每日公告刷新调度器",
	"model_catalog_sync_interval_hours":       "模型价格目录同步器",
	"auto_update_interval_hours":              "版本检查调度器",
	"auto_update_channel":                     "版本发布通道选择",
	"enable_health_score":                     "渠道健康度排序器",
	"success_rate_penalty_weight":             "健康度失败率惩罚",
	"health_score_window_minutes":             "健康度统计时间窗",
	"health_score_update_interval":            "健康度缓存刷新任务",
	"health_min_confident_sample":             "健康度样本置信度",
	"enable_ttfb_score":                       "首字延迟排序开关",
	"ttfb_penalty_weight":                     "首字延迟惩罚",
	"ttfb_max_slow_ratio":                     "首字慢速惩罚上限",
	"ttfb_min_confident_sample":               "首字样本置信度",
	"cooldown_fallback_enabled":               "全渠道冷却兜底选择器",
	"debug_log_enabled":                       "上游原始报文捕获",
	"debug_log_retention_minutes":             "原始报文清理任务",
	"auto_refresh_interval_seconds":           "live:请求日志页面自动刷新",
	"responses_ws_max_sessions":               "Responses 执行会话上限",
	"responses_ws_session_ttl_minutes":        "Responses 会话过期清理",
	"responses_ws_max_transcript_bytes":       "Responses 会话内容容量",
	"responses_ws_max_connections":            "Responses 全局长连接上限",
	"responses_ws_max_connections_per_token":  "Responses 单令牌长连接上限",
	"anthropic_first_byte_timeout":            "Anthropic 流式首字超时",
	"anthropic_non_stream_timeout":            "Anthropic 非流式请求超时",
	"codex_first_byte_timeout":                "Codex 流式首字超时",
	"codex_non_stream_timeout":                "Codex 非流式请求超时",
	"openai_first_byte_timeout":               "OpenAI 流式首字超时",
	"openai_non_stream_timeout":               "OpenAI 非流式请求超时",
	"gemini_first_byte_timeout":               "Gemini 流式首字超时",
	"gemini_non_stream_timeout":               "Gemini 非流式请求超时",
}

func systemSettingActivationFor(key string) (systemSettingActivation, bool) {
	effect, ok := systemSettingRuntimeEffects[key]
	if !ok {
		return systemSettingActivation{}, false
	}
	const livePrefix = "live:"
	requiresRestart := true
	if len(effect) >= len(livePrefix) && effect[:len(livePrefix)] == livePrefix {
		effect = effect[len(livePrefix):]
		requiresRestart = false
	}
	return systemSettingActivation{effect: effect, requiresRestart: requiresRestart}, true
}

func systemSettingRequiresRestart(key string) bool {
	activation, ok := systemSettingActivationFor(key)
	return !ok || activation.requiresRestart
}

// systemSettingRuntimeEffect is the contract between the settings registry and
// its real consumer. A setting without an effect is treated as unfinished and
// must not be editable in the console. This prevents persistence-only shells.
func systemSettingRuntimeEffect(key string) string {
	activation, ok := systemSettingActivationFor(key)
	if !ok {
		return ""
	}
	return activation.effect
}

func isContainerManagedUpdateSetting(key string) bool {
	if !runningInContainer() {
		return false
	}
	return key == autoUpdateIntervalSettingKey || key == autoUpdateChannelSettingKey
}

func systemSettingForAdmin(setting *model.SystemSetting) adminSystemSetting {
	view := adminSystemSetting{
		SystemSetting:   setting,
		Editable:        true,
		RuntimeEffect:   systemSettingRuntimeEffect(setting.Key),
		RequiresRestart: systemSettingRequiresRestart(setting.Key),
	}
	if view.RuntimeEffect == "" {
		view.Editable = false
		view.DisabledReason = runtimeConsumerMissingDisabledReason
	}
	if isContainerManagedUpdateSetting(setting.Key) {
		view.Editable = false
		view.DisabledReason = containerImageManagedDisabledReason
	}
	return view
}

func rejectContainerManagedUpdateSetting(c *gin.Context, key string) bool {
	if !isContainerManagedUpdateSetting(key) {
		return false
	}
	RespondErrorMsg(c, http.StatusConflict, "container image updates are managed by image tags; use latest for stable or beta for preview")
	return true
}

func rejectSettingWithoutRuntimeConsumer(c *gin.Context, key string) bool {
	if systemSettingRuntimeEffect(key) != "" {
		return false
	}
	RespondErrorMsg(c, http.StatusConflict, "setting has no registered runtime consumer")
	return true
}

// AdminListSettings 获取所有配置项
// GET /admin/settings
func (s *Server) AdminListSettings(c *gin.Context) {
	settings, err := s.configService.ListAllSettings(c.Request.Context())
	if err != nil {
		log.Printf("[ERROR] AdminListSettings 失败: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	if settings == nil {
		settings = make([]*model.SystemSetting, 0)
	}
	views := make([]adminSystemSetting, 0, len(settings))
	for _, setting := range settings {
		views = append(views, systemSettingForAdmin(setting))
	}
	RespondJSON(c, http.StatusOK, views)
}

// AdminGetSetting 获取单个配置项
// GET /admin/settings/:key
func (s *Server) AdminGetSetting(c *gin.Context) {
	key := c.Param("key")
	if key == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "missing setting key")
		return
	}

	// 管理接口必须返回持久化后的最新值，不能复用等待重启的运行时缓存。
	setting, err := s.configService.GetSettingFresh(c.Request.Context(), key)
	if errors.Is(err, model.ErrSettingNotFound) {
		RespondErrorMsg(c, http.StatusNotFound, fmt.Sprintf("setting not found: %s", key))
		return
	}
	if err != nil {
		log.Printf("[ERROR] AdminGetSetting key=%s 失败: %v", key, err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	c.Header("Cache-Control", "no-store")
	RespondJSON(c, http.StatusOK, systemSettingForAdmin(setting))
}

// AdminUpdateSetting 更新配置项
// PUT /admin/settings/:key
func (s *Server) AdminUpdateSetting(c *gin.Context) {
	key := c.Param("key")
	if key == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "missing setting key")
		return
	}
	if rejectContainerManagedUpdateSetting(c, key) {
		return
	}
	var req SettingUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	// 验证值的合法性
	setting := s.configService.GetSetting(key)
	if setting == nil {
		RespondErrorMsg(c, http.StatusNotFound, fmt.Sprintf("setting not found: %s", key))
		return
	}
	if rejectSettingWithoutRuntimeConsumer(c, key) {
		return
	}

	if err := validateSettingValue(key, setting.ValueType, req.Value); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("invalid value for type %s: %v", setting.ValueType, err))
		return
	}

	// 更新配置
	if err := s.configService.UpdateSetting(c.Request.Context(), key, req.Value); err != nil {
		log.Printf("[ERROR] AdminUpdateSetting key=%s 失败: %v", key, err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// log.Printf("[INFO] Setting updated: %s = %s (restart required)", key, req.Value)

	// 返回成功响应，告知需要重启
	restartRequired := systemSettingRequiresRestart(key)
	RespondJSON(c, http.StatusOK, gin.H{
		"message":          settingUpdateMessage(restartRequired),
		"key":              key,
		"value":            req.Value,
		"restart_required": restartRequired,
	})

	if restartRequired {
		// 异步触发重启
		go triggerRestart()
	}
}

// AdminResetSetting 重置配置为默认值
// POST /admin/settings/:key/reset
func (s *Server) AdminResetSetting(c *gin.Context) {
	key := c.Param("key")
	if key == "" {
		RespondErrorMsg(c, http.StatusBadRequest, "missing setting key")
		return
	}
	if rejectContainerManagedUpdateSetting(c, key) {
		return
	}
	// 获取默认值
	setting := s.configService.GetSetting(key)
	if setting == nil {
		RespondErrorMsg(c, http.StatusNotFound, fmt.Sprintf("setting not found: %s", key))
		return
	}
	if rejectSettingWithoutRuntimeConsumer(c, key) {
		return
	}

	// 重置为默认值
	if err := s.configService.UpdateSetting(c.Request.Context(), key, setting.DefaultValue); err != nil {
		log.Printf("[ERROR] AdminResetSetting key=%s 失败: %v", key, err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// log.Printf("[INFO] Setting reset to default: %s = %s (restart required)", key, setting.DefaultValue)

	restartRequired := systemSettingRequiresRestart(key)
	RespondJSON(c, http.StatusOK, gin.H{
		"message":          settingResetMessage(restartRequired),
		"key":              key,
		"value":            setting.DefaultValue,
		"restart_required": restartRequired,
	})

	if restartRequired {
		// 异步触发重启
		go triggerRestart()
	}
}

// AdminBatchUpdateSettings 批量更新配置(事务保护)
// POST /admin/settings/batch
func (s *Server) AdminBatchUpdateSettings(c *gin.Context) {
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		return
	}

	if len(req) == 0 {
		RespondErrorMsg(c, http.StatusBadRequest, "no settings to update")
		return
	}

	// 验证所有配置
	for key, value := range req {
		if rejectContainerManagedUpdateSetting(c, key) {
			return
		}
		setting := s.configService.GetSetting(key)
		if setting == nil {
			RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("unknown setting: %s", key))
			return
		}
		if rejectSettingWithoutRuntimeConsumer(c, key) {
			return
		}

		if err := validateSettingValue(key, setting.ValueType, value); err != nil {
			RespondErrorMsg(c, http.StatusBadRequest, fmt.Sprintf("invalid value for %s: %v", key, err))
			return
		}
	}

	// 批量更新(事务保护)
	if err := s.configService.BatchUpdateSettings(c.Request.Context(), req); err != nil {
		log.Printf("[ERROR] AdminBatchUpdateSettings 失败: %v", err)
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	restartRequired := false
	for key := range req {
		if systemSettingRequiresRestart(key) {
			restartRequired = true
			break
		}
	}
	log.Printf("[INFO] 已批量更新 %d 项配置（restart_required=%t）", len(req), restartRequired)

	RespondJSON(c, http.StatusOK, gin.H{
		"message":          settingBatchUpdateMessage(len(req), restartRequired),
		"restart_required": restartRequired,
	})

	if restartRequired {
		// 异步触发重启
		go triggerRestart()
	}
}

func settingUpdateMessage(restartRequired bool) string {
	if restartRequired {
		return "配置已保存，服务正在自动重启"
	}
	return "配置已保存，立即生效"
}

func settingResetMessage(restartRequired bool) string {
	if restartRequired {
		return "配置已重置为默认值，服务正在自动重启"
	}
	return "配置已重置为默认值，立即生效"
}

func settingBatchUpdateMessage(count int, restartRequired bool) string {
	if restartRequired {
		return fmt.Sprintf("已保存 %d 项配置，服务正在自动重启", count)
	}
	return fmt.Sprintf("已保存 %d 项配置，立即生效", count)
}

// validateSettingValue 验证配置值的合法性
func validateSettingValue(key, valueType, value string) error {
	if key == globalCooldownDetectionRulesSettingKey {
		_, err := parseGlobalCooldownDetectionRules(value)
		return err
	}

	switch valueType {
	case "int":
		intVal, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not a valid integer")
		}
		// 按配置项定义具体约束
		switch key {
		case "max_key_retries":
			if intVal < 1 {
				return fmt.Errorf("max_key_retries must be >= 1")
			}
		case "log_retention_days":
			if intVal != LogRetentionDaysDisabled && (intVal < LogRetentionDaysMin || intVal > LogRetentionDaysMax) {
				return fmt.Errorf("log_retention_days must be %d (永久) or %d-%d", LogRetentionDaysDisabled, LogRetentionDaysMin, LogRetentionDaysMax)
			}
		case "auto_update_interval_hours":
			if intVal != 0 && intVal < 1 {
				return fmt.Errorf("auto_update_interval_hours must be 0 or >= 1")
			}
		case "responses_ws_max_sessions",
			"responses_ws_max_connections",
			"responses_ws_max_connections_per_token":
			if intVal < 0 {
				return fmt.Errorf("%s must be >= 0", key)
			}
		case "responses_ws_session_ttl_minutes":
			if intVal < 1 {
				return fmt.Errorf("responses_ws_session_ttl_minutes must be >= 1")
			}
		case "responses_ws_max_transcript_bytes":
			if intVal < 1 {
				return fmt.Errorf("responses_ws_max_transcript_bytes must be >= 1")
			}
		case "max_concurrency",
			"max_body_bytes",
			"max_image_body_bytes",
			"cooldown_auth_seconds",
			"cooldown_server_seconds",
			"cooldown_timeout_seconds",
			"cooldown_rate_limit_seconds",
			"cooldown_max_seconds",
			"cooldown_min_seconds":
			if intVal < 1 {
				return fmt.Errorf("%s must be >= 1", key)
			}
		default:
			if intVal < -1 {
				return fmt.Errorf("value must be >= -1")
			}
		}

	case "float":
		floatVal, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("not a valid number")
		}
		if math.IsNaN(floatVal) || math.IsInf(floatVal, 0) {
			return fmt.Errorf("must be a finite number")
		}
		switch key {
		case "channel_check_interval_hours", "model_catalog_sync_interval_hours":
			if floatVal < 0 {
				return fmt.Errorf("%s must be >= 0", key)
			}
		}

	case "bool":
		if value != "true" && value != "false" && value != "1" && value != "0" {
			return fmt.Errorf("must be true/false or 1/0")
		}

	case "duration":
		intVal, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("duration must be an integer (seconds)")
		}
		if intVal < 0 {
			return fmt.Errorf("duration must be >= 0 (0 = disabled)")
		}

	case "string":
		switch key {
		case "site_daily_checkin_time", "site_daily_announcement_time":
			if _, err := time.Parse("15:04", value); err != nil {
				return fmt.Errorf("%s must use HH:MM", key)
			}
		case "auto_update_channel":
			_, err := version.ParseReleaseChannel(value)
			return err
		case routeStrategySettingKey:
			if value != RouteStrategyBalanced && value != RouteStrategySticky {
				return fmt.Errorf("route_strategy must be %q or %q", RouteStrategyBalanced, RouteStrategySticky)
			}
		}

	case "json":
		if !json.Valid([]byte(value)) {
			return fmt.Errorf("not valid JSON")
		}
		if key == "model_alias_groups" {
			var groups []model.ModelAliasGroup
			if err := json.Unmarshal([]byte(value), &groups); err != nil {
				return fmt.Errorf("model_alias_groups must be an array")
			}
			if len(model.NormalizeModelAliasGroups(groups)) != len(groups) {
				return fmt.Errorf("model_alias_groups contains empty or duplicate canonical names")
			}
		}

	default:
		return fmt.Errorf("unknown value type: %s", valueType)
	}

	return nil
}

// RestartFunc 重启函数（由 main 包注入，避免循环依赖）
var RestartFunc func()

// triggerRestart 触发程序重启
// 依赖优雅关闭语义：触发 SIGTERM 后，HTTP 服务器应完成当前请求再退出。
func triggerRestart() {
	log.Print("[INFO] 配置变更触发重启...")

	if RestartFunc == nil {
		log.Printf("[ERROR] RestartFunc 为空，重启已跳过")
		return
	}
	RestartFunc()
}
