package app

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

const modelAliasGroupsSettingKey = "model_alias_groups"

type modelAliasRegistry struct {
	groups []model.ModelAliasGroup
	byName map[string]model.ModelAliasGroup
}

func loadModelAliasRegistry(cs *ConfigService) *modelAliasRegistry {
	registry := &modelAliasRegistry{byName: make(map[string]model.ModelAliasGroup)}
	if cs == nil {
		return registry
	}
	var groups []model.ModelAliasGroup
	if err := json.Unmarshal([]byte(cs.GetString(modelAliasGroupsSettingKey, "[]")), &groups); err != nil {
		log.Printf("[WARN] 无效的 %s，已禁用全局模型映射: %v", modelAliasGroupsSettingKey, err)
		return registry
	}
	registry.groups = model.NormalizeModelAliasGroups(groups)
	for _, group := range registry.groups {
		if !group.Enabled {
			continue
		}
		registry.byName[strings.ToLower(group.Canonical)] = group
		for _, alias := range group.Aliases {
			registry.byName[strings.ToLower(alias)] = group
		}
	}
	return registry
}

func (r *modelAliasRegistry) namesFor(name string) []string {
	if r == nil {
		return []string{name}
	}
	group, ok := r.byName[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return []string{name}
	}
	names := make([]string, 0, len(group.Aliases)+1)
	names = append(names, group.Canonical)
	names = append(names, group.Aliases...)
	return names
}

func (r *modelAliasRegistry) actualModelFor(cfg *model.Config, requested string) string {
	if cfg == nil {
		return requested
	}
	for _, candidate := range r.namesFor(requested) {
		for _, actual := range cfg.GetModels() {
			if strings.EqualFold(candidate, actual) {
				return actual
			}
		}
	}
	return requested
}

func (r *modelAliasRegistry) supports(cfg *model.Config, requested string) bool {
	if cfg == nil {
		return false
	}
	for _, candidate := range r.namesFor(requested) {
		if cfg.SupportsModel(candidate) {
			return true
		}
	}
	return false
}

// canonicalModelFor 返回 name 所属映射组的 canonical 名称；不在任何组内时原样返回。
func (r *modelAliasRegistry) canonicalModelFor(name string) string {
	if r == nil {
		return name
	}
	if group, ok := r.byName[strings.ToLower(strings.TrimSpace(name))]; ok {
		return group.Canonical
	}
	return name
}

// canonicalizeModelNames 把模型列表折叠到统一名称：组内任何成员都收敛成
// canonical 并去重。路由层本就把 canonical 与别名视为可互换，别名只是不再
// 出现在列表里，手动输入仍然可用，能力无损失。
func (r *modelAliasRegistry) canonicalizeModelNames(names []string) []string {
	if r == nil || len(r.byName) == 0 || len(names) == 0 {
		return names
	}
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		canonical := r.canonicalModelFor(name)
		if _, duplicate := seen[canonical]; duplicate {
			continue
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	return out
}
