package model

import "strings"

// ModelAliasGroup exposes one stable model name for several upstream names.
// The canonical name is also considered a member of the group.
type ModelAliasGroup struct {
	Canonical string   `json:"canonical"`
	Aliases   []string `json:"aliases"`
	Enabled   bool     `json:"enabled"`
}

// NormalizeModelAliasGroups trims entries, removes duplicates and drops empty
// groups. It returns a new slice so callers can safely retain the result.
func NormalizeModelAliasGroups(groups []ModelAliasGroup) []ModelAliasGroup {
	out := make([]ModelAliasGroup, 0, len(groups))
	seenCanonical := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		group.Canonical = strings.TrimSpace(group.Canonical)
		if group.Canonical == "" {
			continue
		}
		canonicalKey := strings.ToLower(group.Canonical)
		if _, exists := seenCanonical[canonicalKey]; exists {
			continue
		}
		seenCanonical[canonicalKey] = struct{}{}
		seen := map[string]struct{}{canonicalKey: {}}
		aliases := make([]string, 0, len(group.Aliases))
		for _, alias := range group.Aliases {
			alias = strings.TrimSpace(alias)
			if alias == "" {
				continue
			}
			key := strings.ToLower(alias)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			aliases = append(aliases, alias)
		}
		group.Aliases = aliases
		out = append(out, group)
	}
	return out
}
