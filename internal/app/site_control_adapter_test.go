package app

import (
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func TestSiteControlAdapterMapsNewAPIFamilyAliases(t *testing.T) {
	service := &siteControlService{
		registry: provider.NewRegistry(provider.NewNewAPI(provider.ClientFactory{})),
	}

	for _, platform := range []string{"new-api", "one-api", "one-hub", "done-hub", "voapi", "axon-hub", "axonhub"} {
		t.Run(platform, func(t *testing.T) {
			adapter, err := service.adapter(&model.Site{Platform: platform})
			if err != nil {
				t.Fatalf("adapter(%q): %v", platform, err)
			}
			if adapter.ID() != model.SitePlatformNewAPIFamily {
				t.Fatalf("adapter(%q).ID() = %q, want %q", platform, adapter.ID(), model.SitePlatformNewAPIFamily)
			}
		})
	}
}

func TestSiteControlAdapterMapsOpenAICompatibleAliases(t *testing.T) {
	service := &siteControlService{
		registry: provider.NewRegistry(provider.NewOpenAICompatible(provider.ClientFactory{})),
	}
	for _, platform := range []string{"openai-compatible", "openai", "openai-compatible-api", "openai_compatible"} {
		t.Run(platform, func(t *testing.T) {
			adapter, err := service.adapter(&model.Site{Platform: platform})
			if err != nil {
				t.Fatalf("adapter(%q): %v", platform, err)
			}
			if adapter.ID() != model.SitePlatformOpenAICompatible {
				t.Fatalf("adapter(%q).ID() = %q, want %q", platform, adapter.ID(), model.SitePlatformOpenAICompatible)
			}
		})
	}
}

func TestSiteProxyURLPriority(t *testing.T) {
	tests := []struct {
		name string
		site *model.Site
		want string
	}{
		{name: "nil site uses environment proxy", site: nil, want: ""},
		{name: "system proxy enabled", site: &model.Site{UseSystemProxy: true}, want: ""},
		{name: "system proxy disabled", site: &model.Site{UseSystemProxy: false}, want: provider.DirectProxyURL},
		{name: "explicit proxy wins", site: &model.Site{UseSystemProxy: false, ProxyURL: " http://127.0.0.1:7890 "}, want: "http://127.0.0.1:7890"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := siteProxyURL(tt.site); got != tt.want {
				t.Fatalf("siteProxyURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
