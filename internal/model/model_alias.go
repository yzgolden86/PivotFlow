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
		// Two groups may not claim the same canonical name; that comparison is
		// case-insensitive because one canonical entry point is one entry point
		// however it is typed.
		canonicalKey := strings.ToLower(group.Canonical)
		if _, exists := seenCanonical[canonicalKey]; exists {
			continue
		}
		seenCanonical[canonicalKey] = struct{}{}

		// Members are deduplicated by their EXACT spelling, not case-folded.
		// Config.modelIndex is keyed on the exact model string, so "DeepSeek-V4-Flash"
		// and "deepseek-v4-flash" are two distinct routing keys: dropping one as a
		// "duplicate" would exclude every channel that declares only that spelling.
		// The canonical name is pre-seeded so it is never repeated among the members.
		seen := map[string]struct{}{group.Canonical: {}}
		aliases := make([]string, 0, len(group.Aliases))
		for _, alias := range group.Aliases {
			alias = strings.TrimSpace(alias)
			if alias == "" {
				continue
			}
			if _, exists := seen[alias]; exists {
				continue
			}
			seen[alias] = struct{}{}
			aliases = append(aliases, alias)
		}
		group.Aliases = aliases
		out = append(out, group)
	}
	return out
}
