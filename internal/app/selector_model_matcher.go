package app

import (
	modelpkg "github.com/yzgolden86/PivotFlow/internal/model"
)

// configSupportsModel uses the same immutable alias snapshot as candidate
// collection. Historical fuzzy fallback still means explicit/wildcard matching.
func configSupportsModel(cfg *modelpkg.Config, model string, aliases *modelAliasRegistry) bool {
	if cfg == nil {
		return false
	}
	if model == "*" {
		return true
	}
	return cfg.SupportsModel(model) || aliases.supports(cfg, model)
}
