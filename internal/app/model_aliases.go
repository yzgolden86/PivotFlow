package app

import (
	"encoding/json"
	"log"
	"strings"
	"sync/atomic"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

const modelAliasGroupsSettingKey = "model_alias_groups"

// modelAliasIndex is one immutable snapshot of the alias groups.
//
// Why a snapshot instead of mutating the maps in place: the registry is read on
// the proxy hot path (selector / proxy_util / proxy_gemini / auth_service) from
// many goroutines, and this setting can now be changed at any time. Rebuilding
// the maps in place would race with those readers; an immutable snapshot swapped
// atomically keeps readers lock-free and lets a request see either the whole old
// mapping or the whole new one, never a half-built one.
type modelAliasIndex struct {
	groups []model.ModelAliasGroup
	byName map[string]model.ModelAliasGroup
}

// modelAliasKey normalizes a model name for group lookup. Only case and
// surrounding whitespace are folded — see NormalizeModelAliasGroups for why the
// spelling is otherwise preserved.
func modelAliasKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func newModelAliasIndex(groups []model.ModelAliasGroup) *modelAliasIndex {
	index := &modelAliasIndex{byName: make(map[string]model.ModelAliasGroup)}
	index.groups = model.NormalizeModelAliasGroups(groups)
	for _, group := range index.groups {
		if !group.Enabled {
			continue
		}
		index.byName[modelAliasKey(group.Canonical)] = group
		for _, alias := range group.Aliases {
			index.byName[modelAliasKey(alias)] = group
		}
	}
	return index
}

type modelAliasRegistry struct {
	index atomic.Pointer[modelAliasIndex]
}

func newModelAliasRegistry(groups []model.ModelAliasGroup) *modelAliasRegistry {
	registry := &modelAliasRegistry{}
	registry.index.Store(newModelAliasIndex(groups))
	return registry
}

func loadModelAliasRegistry(cs *ConfigService) *modelAliasRegistry {
	return newModelAliasRegistry(modelAliasGroupsFromSetting(cs))
}

// modelAliasGroupsFromSetting decodes the persisted setting. A malformed value
// disables the mapping rather than failing startup.
func modelAliasGroupsFromSetting(cs *ConfigService) []model.ModelAliasGroup {
	if cs == nil {
		return nil
	}
	var groups []model.ModelAliasGroup
	if err := json.Unmarshal([]byte(cs.GetString(modelAliasGroupsSettingKey, "[]")), &groups); err != nil {
		log.Printf("[WARN] 无效的 %s，已禁用全局模型映射: %v", modelAliasGroupsSettingKey, err)
		return nil
	}
	return groups
}

// reload swaps in a snapshot built from the current setting. It is what makes
// the mapping take effect without a restart (see Server.applyLiveSettings).
//
// Safe to call while requests are in flight.
func (r *modelAliasRegistry) reload(cs *ConfigService) {
	if r == nil {
		return
	}
	r.index.Store(newModelAliasIndex(modelAliasGroupsFromSetting(cs)))
}

// snapshot returns the current index, or nil for a nil registry. A nil result
// means "no mapping configured" and every method falls back to plain matching.
func (r *modelAliasRegistry) snapshot() *modelAliasIndex {
	if r == nil {
		return nil
	}
	return r.index.Load()
}

func (r *modelAliasRegistry) namesFor(name string) []string {
	index := r.snapshot()
	if index == nil {
		return []string{name}
	}
	group, ok := index.byName[modelAliasKey(name)]
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
	index := r.snapshot()
	if index == nil {
		return name
	}
	if group, ok := index.byName[modelAliasKey(name)]; ok {
		return group.Canonical
	}
	return name
}

// canonicalizeModelNames 把模型列表折叠到统一名称：组内任何成员都收敛成
// canonical 并去重。路由层本就把 canonical 与别名视为可互换，别名只是不再
// 出现在列表里，手动输入仍然可用，能力无损失。
func (r *modelAliasRegistry) canonicalizeModelNames(names []string) []string {
	index := r.snapshot()
	if index == nil || len(index.byName) == 0 || len(names) == 0 {
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
