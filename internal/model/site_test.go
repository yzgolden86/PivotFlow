package model

import "testing"

func TestSiteProjectionSourceHashCanonicalAndCredentialSensitive(t *testing.T) {
	first := SiteProjectionSourceHash("https://example.com/", []string{"OpenAI", "openai"}, []string{"gpt-4.2", "gpt-4.1"}, "sk-one", true)
	second := SiteProjectionSourceHash("https://example.com", []string{"openai"}, []string{"gpt-4.1", "gpt-4.2"}, "sk-one", true)
	if first != second {
		t.Fatalf("canonical hashes differ: %s != %s", first, second)
	}
	if first == SiteProjectionSourceHash("https://example.com", []string{"openai"}, []string{"gpt-4.1", "gpt-4.2"}, "sk-two", true) {
		t.Fatal("credential change did not change projection hash")
	}
	if first == SiteProjectionSourceHash("https://example.com", []string{"openai"}, []string{"gpt-4.1", "gpt-4.2"}, "sk-one", false) {
		t.Fatal("enabled state did not change projection hash")
	}
}
