package app

import (
	modelpkg "github.com/yzgolden86/PivotFlow/internal/model"
)

// configSupportsModel 检查渠道是否支持指定模型
func (s *Server) configSupportsModel(cfg *modelpkg.Config, model string) bool {
	if model == "*" {
		return true
	}
	return cfg.SupportsModel(model) || (s.modelAliases != nil && s.modelAliases.supports(cfg, model))
}

// configSupportsModelWithFuzzyMatch 检查渠道是否支持指定模型。
//
// 历史上带「模糊匹配」回退（子串包含 + 版本排序），但子串语义过于贪婪
// (glm-5.3 会命中 glm-5.3-flash) 并绕过统一映射，已移除。保留函数名仅供
// 候选筛选与路由诊断调用方沿用，语义退化为精确匹配 + 统一映射，行为
// 与 configSupportsModel 一致。
func (s *Server) configSupportsModelWithFuzzyMatch(cfg *modelpkg.Config, model string) bool {
	return s.configSupportsModel(cfg, model)
}
