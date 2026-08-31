package model

import (
	"slices"
	"testing"
)

// Members must be deduplicated by exact spelling. Config.modelIndex is keyed on
// the exact model string, so folding case here would delete a real routing key
// and silently exclude every channel that declares only that spelling.
func TestNormalizeModelAliasGroupsKeepsCaseVariants(t *testing.T) {
	got := NormalizeModelAliasGroups([]ModelAliasGroup{{
		Canonical: "deepseek-v4-flash",
		Aliases: []string{
			"DeepSeek-V4-Flash",
			"deepseek-ai/deepseek-v4-flash",
			"deepseek/deepseek-v4-flash",
		},
		Enabled: true,
	}})
	if len(got) != 1 {
		t.Fatalf("len(groups)=%d, want 1", len(got))
	}
	if !slices.Contains(got[0].Aliases, "DeepSeek-V4-Flash") {
		t.Errorf("capitalised member was dropped: %v", got[0].Aliases)
	}
	if len(got[0].Aliases) != 3 {
		t.Errorf("len(aliases)=%d, want 3: %v", len(got[0].Aliases), got[0].Aliases)
	}
}

// The canonical name is pre-seeded, so repeating it verbatim among the members
// is still collapsed — otherwise the candidate pool is queried twice for it.
func TestNormalizeModelAliasGroupsDropsExactCanonicalRepeat(t *testing.T) {
	got := NormalizeModelAliasGroups([]ModelAliasGroup{{
		Canonical: "glm-5.2",
		Aliases:   []string{"glm-5.2", "GLM-5.2"},
		Enabled:   true,
	}})
	if slices.Contains(got[0].Aliases, "glm-5.2") {
		t.Errorf("exact canonical repeat should be dropped: %v", got[0].Aliases)
	}
	if !slices.Contains(got[0].Aliases, "GLM-5.2") {
		t.Errorf("differently-cased member must survive: %v", got[0].Aliases)
	}
}

// Identical spellings are still collapsed.
func TestNormalizeModelAliasGroupsDropsExactDuplicates(t *testing.T) {
	got := NormalizeModelAliasGroups([]ModelAliasGroup{{
		Canonical: "m",
		Aliases:   []string{"a", "a", " a ", "b"},
		Enabled:   true,
	}})
	if len(got[0].Aliases) != 2 {
		t.Errorf("len(aliases)=%d, want 2 (a, b): %v", len(got[0].Aliases), got[0].Aliases)
	}
}

// Two groups must not claim the same canonical entry point, however it is typed:
// one canonical name is one entry point regardless of case.
func TestNormalizeModelAliasGroupsRejectsDuplicateCanonicalIgnoringCase(t *testing.T) {
	got := NormalizeModelAliasGroups([]ModelAliasGroup{
		{Canonical: "glm-5.2", Aliases: []string{"a"}, Enabled: true},
		{Canonical: "GLM-5.2", Aliases: []string{"b"}, Enabled: true},
	})
	if len(got) != 1 {
		t.Fatalf("len(groups)=%d, want 1 (second claims the same canonical)", len(got))
	}
	if got[0].Canonical != "glm-5.2" {
		t.Errorf("Canonical=%q, want the first one to win", got[0].Canonical)
	}
}

func TestNormalizeModelAliasGroupsDropsEmptyEntries(t *testing.T) {
	got := NormalizeModelAliasGroups([]ModelAliasGroup{
		{Canonical: "  ", Aliases: []string{"x"}, Enabled: true},
		{Canonical: "ok", Aliases: []string{"", "  ", "y"}, Enabled: true},
	})
	if len(got) != 1 || got[0].Canonical != "ok" {
		t.Fatalf("got=%+v, want only the ok group", got)
	}
	if len(got[0].Aliases) != 1 || got[0].Aliases[0] != "y" {
		t.Errorf("aliases=%v, want [y]", got[0].Aliases)
	}
}
