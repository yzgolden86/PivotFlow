package model

import (
	"strings"
	"testing"
)

func TestNormalizeModelNameForGrouping(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"lowercases", "GLM-5.2", "glm52"},
		{"strips vendor prefix", "z.ai/glm-5.2", "glm52"},
		{"strips separators", "glm_5_2", "glm52"},
		{"strips dated suffix", "claude-3-5-sonnet-20241022", "claude35sonnet"},
		{"strips dashed date", "claude-3-5-sonnet-2024-10-22", "claude35sonnet"},
		{"strips date with build number", "deepseek-v4-flash-20250219-1", "deepseekv4flash"},
		{"strips date with v build", "glm-5.2-20250219-v2", "glm52"},
		{"strips latest suffix", "minimax-m3-latest", "minimaxm3"},
		{"keeps preview suffix", "gpt-5.6-preview", "gpt56preview"},
		{"keeps variant suffix", "gpt-5.6-sol", "gpt56sol"},
		{"keeps thinking suffix", "glm-5.2-thinking", "glm52thinking"},
		{"empty stays empty", "   ", ""},
		{"strips only one vendor segment", "openai/gpt-4o", "gpt4o"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeModelNameForGrouping(tc.in); got != tc.want {
				t.Fatalf("NormalizeModelNameForGrouping(%q)=%q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Variants that carry real meaning must never collapse into one suggestion.
func TestNormalizeModelNameForGroupingKeepsDistinctModelsApart(t *testing.T) {
	pairs := [][2]string{
		{"gpt-5.6", "gpt-5.6-sol"},
		{"claude-opus-5", "claude-sonnet-5"},
		{"glm-5.2", "glm-5.2-air"},
		{"deepseek-v3", "deepseek-v3.1"},
		{"gemini-2.5-pro", "gemini-2.5-flash"},
	}
	for _, pair := range pairs {
		left := NormalizeModelNameForGrouping(pair[0])
		right := NormalizeModelNameForGrouping(pair[1])
		if left == right {
			t.Fatalf("%q and %q both normalized to %q; distinct models must not merge", pair[0], pair[1], left)
		}
	}
}

func TestBuildModelAliasSuggestions(t *testing.T) {
	names := []string{
		"GLM-5.2",
		"glm-5.2",
		"z.ai/glm-5.2",
		"gpt-5.6-sol",
		"claude-opus-5",
	}
	suggestions := BuildModelAliasSuggestions(names, nil)
	if len(suggestions) != 1 {
		t.Fatalf("len(suggestions)=%d, want 1: %+v", len(suggestions), suggestions)
	}
	got := suggestions[0]
	if got.Canonical != "glm-5.2" {
		t.Fatalf("Canonical=%q, want %q (tidiest spelling)", got.Canonical, "glm-5.2")
	}
	if len(got.Members) != 3 {
		t.Fatalf("Members=%v, want 3 entries", got.Members)
	}
	if got.Reason == "" {
		t.Fatal("Reason must explain why the group was suggested")
	}
}

// Models with only one spelling are not worth unifying.
func TestBuildModelAliasSuggestionsSkipsSingletons(t *testing.T) {
	suggestions := BuildModelAliasSuggestions([]string{"gpt-5.6-sol", "claude-opus-5"}, nil)
	if len(suggestions) != 0 {
		t.Fatalf("len(suggestions)=%d, want 0: %+v", len(suggestions), suggestions)
	}
}

// Anything already mapped must not be suggested again.
func TestBuildModelAliasSuggestionsSkipsExistingGroups(t *testing.T) {
	names := []string{"GLM-5.2", "glm-5.2", "z.ai/glm-5.2", "Qwen-3", "qwen-3"}
	existing := []ModelAliasGroup{{
		Canonical: "glm-5.2",
		Aliases:   []string{"GLM-5.2", "z.ai/glm-5.2"},
		Enabled:   true,
	}}
	suggestions := BuildModelAliasSuggestions(names, existing)
	if len(suggestions) != 1 {
		t.Fatalf("len(suggestions)=%d, want 1 (only the qwen group): %+v", len(suggestions), suggestions)
	}
	if !strings.EqualFold(suggestions[0].Canonical, "qwen-3") {
		t.Fatalf("Canonical=%q, want qwen-3", suggestions[0].Canonical)
	}
}

// A disabled group leaves its members available for suggestion again.
func TestBuildModelAliasSuggestionsIgnoresDisabledGroups(t *testing.T) {
	names := []string{"GLM-5.2", "glm-5.2"}
	existing := []ModelAliasGroup{{
		Canonical: "glm-5.2",
		Aliases:   []string{"GLM-5.2"},
		Enabled:   false,
	}}
	if got := BuildModelAliasSuggestions(names, existing); len(got) != 1 {
		t.Fatalf("len(suggestions)=%d, want 1: %+v", len(got), got)
	}
}

func TestPreferredCanonicalPrefersCleanSpelling(t *testing.T) {
	cases := []struct {
		name    string
		members []string
		want    string
	}{
		{"prefers unprefixed", []string{"z.ai/glm-5.2", "glm-5.2"}, "glm-5.2"},
		{"prefers lowercase", []string{"GLM-5.2", "glm-5.2"}, "glm-5.2"},
		{"prefers shortest", []string{"glm-5.2-latest", "glm-5.2"}, "glm-5.2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := preferredCanonical(tc.members); got != tc.want {
				t.Fatalf("preferredCanonical(%v)=%q, want %q", tc.members, got, tc.want)
			}
		})
	}
}
