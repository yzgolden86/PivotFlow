package model

import (
	"regexp"
	"sort"
	"strings"
)

// ModelAliasCandidate is one distinct model name discovered on channels,
// annotated with how widely it is used and whether it is already mapped.
type ModelAliasCandidate struct {
	Model         string   `json:"model"`
	ChannelCount  int      `json:"channel_count"`
	ChannelNames  []string `json:"channel_names"`
	MappedTo      string   `json:"mapped_to,omitempty"`
	NormalizedKey string   `json:"normalized_key"`
}

// ModelAliasSuggestion proposes one alias group derived from name similarity.
// Members are real model names present on channels.
//
// When ExtendsCanonical is non-empty the suggestion is not a new group but a set
// of names that belong in that already-configured group. Creating a second group
// for them would leave two canonical names competing for the same model.
type ModelAliasSuggestion struct {
	Canonical        string   `json:"canonical"`
	Members          []string `json:"members"`
	Reason           string   `json:"reason"`
	ExtendsCanonical string   `json:"extends_canonical,omitempty"`
}

// vendorPrefixPattern matches a leading "vendor/" segment such as "z.ai/" or
// "openai/" so that "z.ai/glm-5.2" groups with "glm-5.2".
var vendorPrefixPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]*/`)

// dateSuffixPattern matches trailing dated build stamps such as "-20250219"
// or "-2025-02-19", which upstreams append to otherwise identical models.
// An optional short build number ("-20250219-1", "-20250219v2") is tolerated.
var dateSuffixPattern = regexp.MustCompile(`[-_](20\d{2})[-_]?(\d{2})[-_]?(\d{2})(?:[-_]?v?\d{1,3})?$`)

// latestSuffixPattern strips the conventional "newest snapshot" marker, which
// relays append without implying a distinct model. "preview" is NOT stripped:
// preview builds are usually a separate, billable model.
var latestSuffixPattern = regexp.MustCompile(`[-_]?latest$`)

// separatorPattern matches characters upstreams use inconsistently between the
// same model's variants (dots, underscores, spaces, dashes, colons).
var separatorPattern = regexp.MustCompile(`[._\-\s:]+`)

// NormalizeModelNameForGrouping reduces a model name to a comparison key so
// that casing, vendor prefixes, separators and dated suffixes do not split one
// logical model into several. It deliberately preserves meaningful trailing
// words (for example "-sol" or "-thinking") because those denote real variants.
func NormalizeModelNameForGrouping(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return ""
	}
	// Strip at most one vendor prefix; nested paths are rare and meaningful.
	key = vendorPrefixPattern.ReplaceAllString(key, "")
	key = dateSuffixPattern.ReplaceAllString(key, "")
	key = latestSuffixPattern.ReplaceAllString(key, "")
	key = separatorPattern.ReplaceAllString(key, "")
	return key
}

// BuildModelAliasSuggestions groups the supplied model names by their
// normalized key and returns one suggestion per key that has more than one
// distinct spelling. Names already covered by an existing group are skipped so
// suggestions never duplicate configured mappings.
//
// candidates must be distinct model names. existing may be nil.
func BuildModelAliasSuggestions(candidates []string, existing []ModelAliasGroup) []ModelAliasSuggestion {
	// Claims are exact: a group listing only "glm-5.2" leaves "GLM-5.2" free to
	// be suggested, because that spelling is still an unrouted key.
	// ownerByKey maps a normalized key to the canonical name of the group that
	// already owns it, so partially-covered buckets extend that group instead of
	// competing with it.
	claimed := make(map[string]struct{})
	ownerByKey := make(map[string]string)
	for _, group := range existing {
		if !group.Enabled {
			continue
		}
		canonical := strings.TrimSpace(group.Canonical)
		claimed[canonical] = struct{}{}
		if key := NormalizeModelNameForGrouping(canonical); key != "" {
			ownerByKey[key] = canonical
		}
		for _, alias := range group.Aliases {
			alias = strings.TrimSpace(alias)
			claimed[alias] = struct{}{}
			if key := NormalizeModelNameForGrouping(alias); key != "" {
				ownerByKey[key] = canonical
			}
		}
	}

	buckets := make(map[string][]string)
	for _, name := range candidates {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		if _, taken := claimed[trimmed]; taken {
			continue
		}
		key := NormalizeModelNameForGrouping(trimmed)
		if key == "" {
			continue
		}
		buckets[key] = append(buckets[key], trimmed)
	}

	suggestions := make([]ModelAliasSuggestion, 0, len(buckets))
	for key, members := range buckets {
		members = dedupeExact(members)
		sort.Strings(members)
		// An existing group already owns this model: even a single leftover name
		// is worth surfacing, because it is currently routed separately.
		if owner, owned := ownerByKey[key]; owned {
			suggestions = append(suggestions, ModelAliasSuggestion{
				Canonical:        owner,
				Members:          members,
				Reason:           "与已配置映射同名，建议补进该映射",
				ExtendsCanonical: owner,
			})
			continue
		}
		if len(members) < 2 {
			continue
		}
		suggestions = append(suggestions, ModelAliasSuggestion{
			Canonical: preferredCanonical(members),
			Members:   members,
			Reason:    "名称仅大小写、分隔符、厂商前缀或日期后缀不同",
		})
	}
	// Most-members first, then alphabetical, so the UI order is stable.
	sort.Slice(suggestions, func(i, j int) bool {
		if len(suggestions[i].Members) != len(suggestions[j].Members) {
			return len(suggestions[i].Members) > len(suggestions[j].Members)
		}
		return suggestions[i].Canonical < suggestions[j].Canonical
	})
	return suggestions
}

// preferredCanonical picks the tidiest spelling as the unified name: no vendor
// prefix first, then all-lowercase, then shortest, then alphabetical.
func preferredCanonical(members []string) string {
	best := members[0]
	for _, candidate := range members[1:] {
		if canonicalLess(candidate, best) {
			best = candidate
		}
	}
	return best
}

func canonicalLess(a, b string) bool {
	aPrefixed := vendorPrefixPattern.MatchString(strings.ToLower(a))
	bPrefixed := vendorPrefixPattern.MatchString(strings.ToLower(b))
	if aPrefixed != bPrefixed {
		return !aPrefixed
	}
	aLower := a == strings.ToLower(a)
	bLower := b == strings.ToLower(b)
	if aLower != bLower {
		return aLower
	}
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// dedupeExact removes repeated spellings but keeps case variants apart:
// Config.modelIndex is keyed on the exact model string, so "GLM-5.2" and
// "glm-5.2" are distinct routing keys and both belong in the alias group.
func dedupeExact(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
