package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
)

// These are the user's complete upstream spellings, including exact case
// variants. The public list must expose one canonical name for the whole group.
func deepSeekV41ModelNames() []string {
	return []string{
		"DeepSeek-V4.1-Flash",
		"deepseek-ai/DeepSeek-V4.1-Flash",
		"other/DeepSeek-V4.1-Flash",
		"deepseek-ai/deepseek-v4.1-flash",
		"deepseek/deepseek-v4.1-flash",
		"opencode/deepseek-v4.1-flash",
		"deepseek-v4.1-flash",
		"deepseek-v4.1-flash-1017",
		"channel1/DeepSeek-V4.1-Flash-1017",
		"deepseek-v4.1-flash-thinking",
		"deepseek-ai/DeepSeek-V4.1-Flash-1017",
		"deepseek-v4.1-flash-1017-特价",
		"deepseek-v4.1-flash-free",
		"deepseek-v4.1-flash-特价",
		"deepSeek-v4.1-flash-1017",
		"deepseek-v4.1flash-1017",
		"deepseek/deepseek-v4.1-flash-1017",
		"inkstone/deepseek-v4.1-flash-1017",
		"nvidia/deepseek-v4.1-flash",
		"opencode/deepseek-v4.1-flash-free",
	}
}

func TestNewTokenListsOnlyCanonicalDeepSeekModels(t *testing.T) {
	s := newInMemoryServer(t)
	cipher, err := credential.New([]byte("0123456789abcdef0123456789abcdef"), "unified-model-list-test")
	if err != nil {
		t.Fatal(err)
	}
	s.siteControl.cipher = cipher
	ctx := context.Background()
	entries := []model.ModelEntry{{Model: "gpt-unrelated"}}
	for _, name := range deepSeekV41ModelNames() {
		entries = append(entries, model.ModelEntry{Model: name})
	}
	_, err = s.store.CreateConfig(ctx, &model.Config{
		Name: "complete-user-group", Enabled: true,
		URLs:         model.ChannelURLs{{URL: "https://synthetic.invalid", Protocols: []string{"openai", "codex", "anthropic", "gemini"}}},
		ModelEntries: entries,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.InvalidateChannelListCache()
	groups, err := json.Marshal([]model.ModelAliasGroup{{Canonical: "deepseek-v4.1-flash", Aliases: deepSeekV41ModelNames(), Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	c, w := newTestContext(t, newJSONRequest(t, http.MethodPost, "/admin/settings/batch", map[string]string{modelAliasGroupsSettingKey: string(groups)}))
	s.AdminBatchUpdateSettings(c)
	if w.Code != http.StatusOK {
		t.Fatalf("mapping save status=%d", w.Code)
	}
	g := modelRoutesTestEngine(t, s)
	for _, whitelist := range [][]string{nil, {"inkstone/deepseek-v4.1-flash-1017"}, {"deepseek-v4.1-flash"}, {"gpt-unrelated"}} {
		t.Run(strings.Join(whitelist, ","), func(t *testing.T) {
			c, w := newTestContext(t, newJSONRequest(t, http.MethodPost, "/admin/auth-tokens", map[string]any{
				"description": "unified model regression", "allowed_models": whitelist,
			}))
			s.HandleCreateAuthToken(c)
			if w.Code != http.StatusOK {
				t.Fatalf("new token status=%d", w.Code)
			}
			var created struct {
				Token string `json:"token"`
			}
			mustUnmarshalAPIResponseData(t, w.Body.Bytes(), &created)
			for _, path := range []string{"/v1/models", "/v1/models?protocol=codex", "/v1/models?protocol=anthropic", "/v1beta/models"} {
				r := httptest.NewRequest(http.MethodGet, path, nil)
				r.Header.Set("Authorization", "Bearer "+created.Token)
				w := httptest.NewRecorder()
				g.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("%s status=%d", path, w.Code)
				}
				var response struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
					Models []struct {
						Name string `json:"name"`
					} `json:"models"`
				}
				mustUnmarshalJSON(t, w.Body.Bytes(), &response)
				ids := []string{}
				for _, item := range response.Data {
					ids = append(ids, item.ID)
				}
				for _, item := range response.Models {
					ids = append(ids, strings.TrimPrefix(item.Name, "models/"))
				}
				want := []string{"deepseek-v4.1-flash", "gpt-unrelated"}
				if len(whitelist) > 0 {
					want = []string{"deepseek-v4.1-flash"}
					if whitelist[0] == "gpt-unrelated" {
						want = []string{"gpt-unrelated"}
					}
				}
				if !slices.Equal(ids, want) {
					t.Fatalf("%s public model IDs=%v, want=%v", path, ids, want)
				}
			}
		})
	}
}

func TestTokenModelInventoryContainsOnlyEnabledCanonicalNames(t *testing.T) {
	s, _, cleanup := setupAdminTestServer(t)
	defer cleanup()
	c := createDiagnosticChannel(t, s, "token-inventory", 1, 1)
	changeDiagnosticConfig(t, s, c.ID, func(cfg *model.Config) {
		cfg.ModelEntries = []model.ModelEntry{{Model: "vendor/deepseek"}, {Model: "disabled-model", Disabled: true}, {Model: "*"}}
	})
	s.modelAliases = newModelAliasRegistry([]model.ModelAliasGroup{
		{Canonical: "deepseek-v4.1-flash", Aliases: []string{"vendor/deepseek"}, Enabled: true},
		{Canonical: "empty-group", Aliases: []string{"missing-upstream"}, Enabled: true},
	})
	g := modelRoutesTestEngine(t, s)
	var response modelInventoryTestView
	readModelRouteAPI(t, g, "/admin/routing/models?enabled_only=true", 200, &response)
	if response.Total != 1 || len(response.Items) != 1 || response.Items[0].Model != "deepseek-v4.1-flash" {
		t.Fatalf("token model inventory contains stale/disabled/raw names: %+v", response)
	}
	for _, value := range []string{"yes", "", "true&enabled_only=false"} {
		readModelRouteAPI(t, g, "/admin/routing/models?enabled_only="+value, 400, nil)
	}
}
