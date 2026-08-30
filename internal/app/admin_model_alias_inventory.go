package app

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/yzgolden86/PivotFlow/internal/model"

	"github.com/gin-gonic/gin"
)

// maxAliasCandidateChannelNames caps how many channel names are echoed per
// model so a model carried by every channel does not bloat the payload.
const maxAliasCandidateChannelNames = 6

// ModelAliasInventory feeds the settings-page picker: every distinct model name
// currently configured on channels, plus auto-detected grouping suggestions.
type ModelAliasInventory struct {
	Candidates  []model.ModelAliasCandidate  `json:"candidates"`
	Suggestions []model.ModelAliasSuggestion `json:"suggestions"`
	TotalModels int                          `json:"total_models"`
}

// HandleModelAliasInventory returns the channel model inventory used by the
// model-alias picker. Read-only: it touches no rotation or cooldown state.
func (s *Server) HandleModelAliasInventory(c *gin.Context) {
	channels, err := s.store.ListConfigs(c.Request.Context())
	if err != nil {
		RespondError(c, http.StatusInternalServerError, err)
		return
	}

	// Group by the exact model string. Config.modelIndex is keyed exactly, so
	// "GLM-5.2" and "glm-5.2" are separate routing keys and both must be
	// listed — collapsing them would hide a name the user still needs to map.
	byModel := make(map[string][]string)
	for _, cfg := range channels {
		if cfg == nil || !cfg.Enabled {
			continue
		}
		for _, name := range cfg.GetModels() {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				continue
			}
			byModel[trimmed] = append(byModel[trimmed], cfg.Name)
		}
	}

	existing := s.currentAliasGroups()
	mappedTo := make(map[string]string)
	for _, group := range existing {
		if !group.Enabled {
			continue
		}
		mappedTo[strings.TrimSpace(group.Canonical)] = group.Canonical
		for _, alias := range group.Aliases {
			mappedTo[strings.TrimSpace(alias)] = group.Canonical
		}
	}

	candidates := make([]model.ModelAliasCandidate, 0, len(byModel))
	names := make([]string, 0, len(byModel))
	for modelName, owners := range byModel {
		names = append(names, modelName)
		channelNames := dedupeStrings(owners)
		sort.Strings(channelNames)
		total := len(channelNames)
		if len(channelNames) > maxAliasCandidateChannelNames {
			channelNames = channelNames[:maxAliasCandidateChannelNames]
		}
		candidates = append(candidates, model.ModelAliasCandidate{
			Model:         modelName,
			ChannelCount:  total,
			ChannelNames:  channelNames,
			MappedTo:      mappedTo[modelName],
			NormalizedKey: model.NormalizeModelNameForGrouping(modelName),
		})
	}
	// Widely available models first: they are the most useful to unify.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ChannelCount != candidates[j].ChannelCount {
			return candidates[i].ChannelCount > candidates[j].ChannelCount
		}
		return strings.ToLower(candidates[i].Model) < strings.ToLower(candidates[j].Model)
	})

	RespondJSON(c, http.StatusOK, ModelAliasInventory{
		Candidates:  candidates,
		Suggestions: model.BuildModelAliasSuggestions(names, existing),
		TotalModels: len(candidates),
	})
}

// currentAliasGroups reads the persisted alias groups from settings rather than
// the in-memory registry, so the picker reflects unsaved-restart edits too.
func (s *Server) currentAliasGroups() []model.ModelAliasGroup {
	if s.configService == nil {
		return nil
	}
	var groups []model.ModelAliasGroup
	raw := s.configService.GetString(modelAliasGroupsSettingKey, "[]")
	if err := json.Unmarshal([]byte(raw), &groups); err != nil {
		return nil
	}
	return model.NormalizeModelAliasGroups(groups)
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
