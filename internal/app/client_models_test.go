package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func TestClientModelsExplicitView(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	for _, name := range []string{"openai", "codex", "anthropic", "gemini"} {
		_, err := store.CreateConfig(context.Background(), &model.Config{
			Name: name, Enabled: true, ProtocolTransformMode: model.ProtocolTransformModeUpstream,
			URLs:         model.ChannelURLs{{URL: "https://synthetic.invalid", Protocols: []string{name}}},
			ModelEntries: []model.ModelEntry{{Model: name + "-only"}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, view := range []string{"openai", "codex", "anthropic"} {
		t.Run(view, func(t *testing.T) {
			req := newRequest(http.MethodGet, "/v1/models?protocol="+view, nil)
			req.Header.Set("User-Agent", "Mozilla/5.0")
			req.Header.Set("anthropic-version", "2023-06-01") // explicit query wins
			c, w := newTestContext(t, req)
			server.handleListOpenAIModels(c)
			var resp struct {
				Object string `json:"object"`
				Data   []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || len(resp.Data) != 1 || resp.Data[0].ID != view+"-only" {
				t.Fatalf("view %s: status=%d models=%+v", view, w.Code, resp.Data)
			}
			if (resp.Object == "list") != (view != "anthropic") {
				t.Fatalf("wrong response shape for %s: %+v", view, resp)
			}
		})
	}
	for _, query := range []string{"unknown", "gemini", "openai&protocol=codex", "%0acodex"} {
		t.Run("invalid_"+query, func(t *testing.T) {
			c, w := newTestContext(t, newRequest(http.MethodGet, "/v1/models?protocol="+query, nil))
			server.handleListOpenAIModels(c)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, expected 400 for invalid view", w.Code)
			}
		})
	}
}

type clientModelsReadStore struct {
	storage.Store
	reads  int
	failAt int
}

func (s *clientModelsReadStore) GetEnabledChannelsByModel(ctx context.Context, name string) ([]*model.Config, error) {
	s.reads++
	if s.reads == s.failAt {
		return nil, errors.New("synthetic model snapshot unavailable")
	}
	return s.Store.GetEnabledChannelsByModel(ctx, name)
}

func TestClientModelsSingleSnapshotAndStorageFailure(t *testing.T) {
	for _, path := range []string{"/v1/models?protocol=openai", "/v1beta/models"} {
		for _, failAt := range []int{1, 2} {
			t.Run(path+"/failAt"+string(rune('0'+failAt)), func(t *testing.T) {
				server, store, cleanup := setupAdminTestServer(t)
				defer cleanup()
				_, err := store.CreateConfig(context.Background(), &model.Config{
					Name: "consistent-snapshot", Enabled: true,
					URLs:         model.ChannelURLs{{URL: "https://synthetic.invalid", Protocols: []string{"openai", "gemini"}}},
					ModelEntries: []model.ModelEntry{{Model: "snapshot-model"}},
				})
				if err != nil {
					t.Fatal(err)
				}
				wrapped := &clientModelsReadStore{Store: store, failAt: failAt}
				server.store = wrapped
				c, w := newTestContext(t, newRequest(http.MethodGet, path, nil))
				if path == "/v1beta/models" {
					server.handleListGeminiModels(c)
				} else {
					server.handleListOpenAIModels(c)
				}
				if wrapped.reads != 1 {
					t.Errorf("model list read %d channel snapshots, want one", wrapped.reads)
				}
				if failAt == 1 {
					if w.Code != http.StatusInternalServerError {
						t.Fatalf("storage failure became status=%d body=%s", w.Code, w.Body.String())
					}
				} else if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "snapshot-model") {
					t.Fatalf("consistent snapshot lost: status=%d body=%s", w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestClientModelsPermissionsAndTransform(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()
	server.authService = newTestAuthService(t)
	server.authService.authTokenProtocols = make(map[string][]string)
	hash := model.HashToken("synthetic-downstream")
	server.authService.authTokenProtocols[hash] = []string{"codex"}
	server.authService.authTokenModels[hash] = []string{"Allowed-Alias"}
	allowed, err := store.CreateConfig(context.Background(), &model.Config{
		Name: "auto-transform", Enabled: true,
		URLs:         model.ChannelURLs{{URL: "https://synthetic.invalid", Protocols: []string{"openai"}}},
		ModelEntries: []model.ModelEntry{{Model: "Allowed-Alias"}, {Model: "disallowed"}, {Model: "disabled", Disabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateConfig(context.Background(), &model.Config{
		Name: "denied-channel", Enabled: true,
		URLs:         model.ChannelURLs{{URL: "https://synthetic.invalid"}},
		ModelEntries: []model.ModelEntry{{Model: "denied-only"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.authService.authTokenChannels[hash] = mustChannelRestriction(t, "allow", allowed.ID)
	for _, view := range []string{"codex", "openai", "anthropic"} {
		c, w := newTestContext(t, newRequest(http.MethodGet, "/v1/models?protocol="+view, nil))
		c.Set("token_hash", hash)
		server.handleListOpenAIModels(c)
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(resp.Data))
		for _, item := range resp.Data {
			ids = append(ids, item.ID)
		}
		want := []string{}
		if view == "codex" {
			want = []string{"Allowed-Alias"}
		}
		if !slices.Equal(ids, want) {
			t.Fatalf("%s view models=%v want=%v", view, ids, want)
		}
	}
	c, w := newTestContext(t, newRequest(http.MethodGet, "/v1beta/models", nil))
	c.Set("token_hash", hash)
	server.handleListGeminiModels(c)
	var gemini struct {
		Models []any `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &gemini); err != nil || len(gemini.Models) != 0 {
		t.Fatalf("Gemini protocol restriction bypassed: %s", w.Body.String())
	}
}
