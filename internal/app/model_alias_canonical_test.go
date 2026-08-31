package app

import (
	"slices"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// aliasRegistryFor builds a registry the way the server does at startup.
func aliasRegistryFor(t *testing.T, groups ...model.ModelAliasGroup) *modelAliasRegistry {
	t.Helper()
	registry := &modelAliasRegistry{byName: make(map[string]model.ModelAliasGroup)}
	registry.groups = model.NormalizeModelAliasGroups(groups)
	for _, group := range registry.groups {
		if !group.Enabled {
			continue
		}
		registry.byName[lowerTrim(group.Canonical)] = group
		for _, alias := range group.Aliases {
			registry.byName[lowerTrim(alias)] = group
		}
	}
	return registry
}

func lowerTrim(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out = append(out, c)
	}
	return string(out)
}

// The user's exact question: a group maps DeepSeek-V4-Flash and two prefixed
// spellings onto deepseek-v4-flash. A channel whose upstream model is literally
// deepseek-v4-flash is NOT listed among the members. Does it still route?
//
// It must. namesFor always emits the canonical name first, so the candidate pool
// includes every channel carrying that exact name even though nobody listed it.
func TestCanonicalNameAlwaysParticipatesEvenWhenNotListedAsMember(t *testing.T) {
	registry := aliasRegistryFor(t, model.ModelAliasGroup{
		Canonical: "deepseek-v4-flash",
		Aliases: []string{
			"DeepSeek-V4-Flash",
			"deepseek-ai/deepseek-v4-flash",
			"deepseek/deepseek-v4-flash",
		},
		Enabled: true,
	})

	names := registry.namesFor("deepseek-v4-flash")
	if !slices.Contains(names, "deepseek-v4-flash") {
		t.Fatalf("canonical name missing from lookup set: %v", names)
	}
	if names[0] != "deepseek-v4-flash" {
		t.Errorf("names[0]=%q, want the canonical name first", names[0])
	}
	if len(names) != 4 {
		t.Errorf("len(names)=%d, want 4 (canonical + 3 members): %v", len(names), names)
	}

	// A channel that only declares the canonical spelling supports the request.
	plain := &model.Config{ModelEntries: []model.ModelEntry{{Model: "deepseek-v4-flash"}}}
	if !registry.supports(plain, "deepseek-v4-flash") {
		t.Error("channel declaring only the canonical name must support the request")
	}
	if got := registry.actualModelFor(plain, "deepseek-v4-flash"); got != "deepseek-v4-flash" {
		t.Errorf("actualModelFor=%q, want deepseek-v4-flash", got)
	}
}

// Requesting via any member spelling must reach the canonical-only channel too.
func TestMemberRequestReachesCanonicalOnlyChannel(t *testing.T) {
	registry := aliasRegistryFor(t, model.ModelAliasGroup{
		Canonical: "deepseek-v4-flash",
		Aliases:   []string{"DeepSeek-V4-Flash", "deepseek/deepseek-v4-flash"},
		Enabled:   true,
	})

	plain := &model.Config{ModelEntries: []model.ModelEntry{{Model: "deepseek-v4-flash"}}}
	for _, requested := range []string{"DeepSeek-V4-Flash", "deepseek/deepseek-v4-flash", "deepseek-v4-flash"} {
		if !registry.supports(plain, requested) {
			t.Errorf("request %q must reach the canonical-only channel", requested)
		}
		if got := registry.actualModelFor(plain, requested); got != "deepseek-v4-flash" {
			t.Errorf("request %q: actualModelFor=%q, want deepseek-v4-flash", requested, got)
		}
	}
}

// Listing the canonical name among the members as well must not duplicate it in
// the lookup set, or the candidate pool would be queried twice for one name.
func TestCanonicalListedAsMemberIsNotDuplicated(t *testing.T) {
	registry := aliasRegistryFor(t, model.ModelAliasGroup{
		Canonical: "deepseek-v4-flash",
		Aliases:   []string{"deepseek-v4-flash", "DeepSeek-V4-Flash"},
		Enabled:   true,
	})

	names := registry.namesFor("deepseek-v4-flash")
	seen := map[string]int{}
	for _, name := range names {
		seen[name]++
	}
	if seen["deepseek-v4-flash"] != 1 {
		t.Errorf("canonical appears %d times, want exactly 1: %v", seen["deepseek-v4-flash"], names)
	}
}

// A disabled group must fall back to plain exact matching, not silently widen.
func TestDisabledGroupLeavesRoutingUnchanged(t *testing.T) {
	registry := aliasRegistryFor(t, model.ModelAliasGroup{
		Canonical: "deepseek-v4-flash",
		Aliases:   []string{"DeepSeek-V4-Flash"},
		Enabled:   false,
	})

	names := registry.namesFor("deepseek-v4-flash")
	if len(names) != 1 || names[0] != "deepseek-v4-flash" {
		t.Errorf("names=%v, want just the requested name when the group is disabled", names)
	}
}
