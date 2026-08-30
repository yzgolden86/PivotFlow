package model

import "testing"

// A bucket that an existing group already partly covers must extend that group
// rather than propose a rival canonical name for the same model.
func TestBuildModelAliasSuggestionsExtendsPartiallyCoveredGroup(t *testing.T) {
	names := []string{"glm-5.2", "GLM-5.2", "z.ai/glm-5.2"}
	existing := []ModelAliasGroup{{
		Canonical: "glm-5.2",
		Aliases:   []string{"GLM-5.2"},
		Enabled:   true,
	}}

	suggestions := BuildModelAliasSuggestions(names, existing)
	if len(suggestions) != 1 {
		t.Fatalf("len(suggestions)=%d, want 1: %+v", len(suggestions), suggestions)
	}
	got := suggestions[0]
	if got.ExtendsCanonical != "glm-5.2" {
		t.Fatalf("ExtendsCanonical=%q, want %q", got.ExtendsCanonical, "glm-5.2")
	}
	if got.Canonical != "glm-5.2" {
		t.Fatalf("Canonical=%q, want the existing group's name", got.Canonical)
	}
	if len(got.Members) != 1 || got.Members[0] != "z.ai/glm-5.2" {
		t.Fatalf("Members=%v, want only the unmapped spelling", got.Members)
	}
}

// A fully covered group produces no suggestion at all.
func TestBuildModelAliasSuggestionsSilentWhenFullyCovered(t *testing.T) {
	names := []string{"glm-5.2", "GLM-5.2"}
	existing := []ModelAliasGroup{{
		Canonical: "glm-5.2",
		Aliases:   []string{"GLM-5.2"},
		Enabled:   true,
	}}
	if got := BuildModelAliasSuggestions(names, existing); len(got) != 0 {
		t.Fatalf("len(suggestions)=%d, want 0: %+v", len(got), got)
	}
}

// A brand-new group and an extension can be proposed in the same response.
func TestBuildModelAliasSuggestionsMixesNewAndExtension(t *testing.T) {
	names := []string{"z.ai/glm-5.2", "Qwen-3", "qwen-3"}
	existing := []ModelAliasGroup{{
		Canonical: "glm-5.2",
		Aliases:   []string{"GLM-5.2"},
		Enabled:   true,
	}}

	suggestions := BuildModelAliasSuggestions(names, existing)
	if len(suggestions) != 2 {
		t.Fatalf("len(suggestions)=%d, want 2: %+v", len(suggestions), suggestions)
	}

	var extension, fresh int
	for _, suggestion := range suggestions {
		if suggestion.ExtendsCanonical != "" {
			extension++
			continue
		}
		fresh++
	}
	if extension != 1 || fresh != 1 {
		t.Fatalf("extension=%d fresh=%d, want 1 and 1: %+v", extension, fresh, suggestions)
	}
}
