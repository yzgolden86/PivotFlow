package app

import (
	"context"
	"net/url"
	"strings"

	modelpkg "github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/protocol"
	"github.com/yzgolden86/PivotFlow/internal/util"
)

func normalizeOptionalProtocol(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return util.NormalizeProtocol(value)
}

// selectCandidatesByClientProtocol 返回所有启用渠道；clientProtocol 仅用于计算客户端协议对应的模型冷却键。
func (s *Server) selectCandidatesByClientProtocol(ctx context.Context, clientProtocol, stickyScope string) ([]*modelpkg.Config, error) {
	channels, err := s.getEnabledChannelsSnapshotByModel(ctx, "*")
	if err != nil {
		return nil, err
	}
	return s.filterCooldownChannels(ctx, channels, "*", clientProtocol, stickyScope)
}

func (s *Server) selectCandidatesByClientProtocolAnonymous(ctx context.Context, clientProtocol string) ([]*modelpkg.Config, error) {
	return s.selectCandidatesByClientProtocol(ctx, clientProtocol, stickyScopeKey(""))
}

// alphaSearchUpstreamURLs removes exact URLs for other Codex endpoints and
// endpoints that recently proved they do not implement alpha/search.
// Normal base URLs remain eligible because the request path is appended later.
func (s *Server) alphaSearchUpstreamURLs(cfg *modelpkg.Config) []string {
	urls := cfg.GetURLs()
	compatible := make([]string, 0, len(urls))
	for _, rawURL := range urls {
		key := protocolCapabilityKey{
			channelID: cfg.ID, baseURL: rawURL,
			clientProtocol: protocol.Codex, requestFamily: protocol.RequestFamilyAlphaSearch,
		}
		if cached, known := s.protocolCapabilities.get(key); known && cached == protocolUnsupported {
			continue
		}
		if !modelpkg.HasExactUpstreamURLMarker(rawURL) {
			compatible = append(compatible, rawURL)
			continue
		}

		parsed, err := url.Parse(modelpkg.StripExactUpstreamURLMarker(rawURL))
		if err == nil && protocol.DetectRequestFamily(parsed.Path) == protocol.RequestFamilyAlphaSearch {
			compatible = append(compatible, rawURL)
		}
	}
	return compatible
}

func (s *Server) selectAlphaSearchCandidates(ctx context.Context, modelName, stickyScope string) ([]*modelpkg.Config, error) {
	routeModel := modelName
	if routeModel == "" {
		routeModel = "*"
	}
	channels, err := s.getEnabledChannelsSnapshotByModel(ctx, routeModel)
	if err != nil {
		return nil, err
	}

	compatible := make([]*modelpkg.Config, 0, len(channels))
	for _, cfg := range channels {
		if cfg == nil {
			continue
		}

		urls := s.alphaSearchUpstreamURLs(cfg)
		if len(urls) == 0 {
			continue
		}
		if len(urls) != len(cfg.GetURLs()) {
			configuredURLs := cfg.URLs
			cfg = cfg.Clone()
			allowed := make(map[string]struct{}, len(urls))
			for _, runtimeURL := range urls {
				allowed[runtimeURL] = struct{}{}
			}
			cfg.URLs = cfg.URLs[:0]
			for _, entry := range configuredURLs {
				if _, ok := allowed[entry.RuntimeURL()]; ok {
					cfg.URLs = append(cfg.URLs, entry)
				}
			}
		}
		compatible = append(compatible, cfg)
	}

	return s.filterCooldownChannels(ctx, compatible, routeModel, string(protocol.Codex), stickyScope)
}

// selectCandidatesByModelAndClientProtocol 按模型选择候选渠道；clientProtocol 仅表示客户端协议，不过滤上游主协议。
func (s *Server) selectCandidatesByModelAndClientProtocol(ctx context.Context, model, clientProtocol, stickyScope string) ([]*modelpkg.Config, error) {
	view, err := s.collectModelRouteSelection(ctx, model, normalizeOptionalProtocol(clientProtocol))
	if err != nil {
		return nil, err
	}
	return s.orderRouteSelection(view, model, stickyScope), nil
}

func (s *Server) selectCandidatesByModelAndClientProtocolAnonymous(ctx context.Context, model, clientProtocol string) ([]*modelpkg.Config, error) {
	return s.selectCandidatesByModelAndClientProtocol(ctx, model, clientProtocol, stickyScopeKey(""))
}

func (s *Server) getEnabledChannelsSnapshotByUnifiedModelWithAliases(ctx context.Context, modelName string, aliases *modelAliasRegistry) ([]*modelpkg.Config, error) {
	if modelName == "*" || aliases == nil {
		return s.getEnabledChannelsSnapshotByModel(ctx, modelName)
	}
	names := aliases.namesFor(modelName)
	if len(names) <= 1 {
		return s.getEnabledChannelsSnapshotByModel(ctx, modelName)
	}
	seen := make(map[int64]struct{})
	merged := make([]*modelpkg.Config, 0)
	for _, name := range names {
		channels, err := s.getEnabledChannelsSnapshotByModel(ctx, name)
		if err != nil {
			return nil, err
		}
		for _, cfg := range channels {
			if cfg == nil {
				continue
			}
			if _, exists := seen[cfg.ID]; exists {
				continue
			}
			seen[cfg.ID] = struct{}{}
			merged = append(merged, cfg)
		}
	}
	return merged, nil
}
