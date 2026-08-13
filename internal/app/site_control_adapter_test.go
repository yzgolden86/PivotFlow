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
