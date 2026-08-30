package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func createAliasTestChannel(t *testing.T, server *Server, name string, enabled bool, models ...string) {
	t.Helper()
	entries := make([]model.ModelEntry, 0, len(models))
	for _, name := range models {
		entries = append(entries, model.ModelEntry{Model: name})
	}
	if _, err := server.store.CreateConfig(context.Background(), &model.Config{
		Name:         name,
		URLs:         model.ChannelURLs{{URL: "https://api.example.com"}},
		Priority:     10,
		ModelEntries: entries,
		Enabled:      enabled,
	}); err != nil {
		t.Fatalf("创建测试渠道 %s 失败: %v", name, err)
	}
}

func fetchAliasInventory(t *testing.T, server *Server) ModelAliasInventory {
	t.Helper()
	c, w := newTestContext(t, newRequest(http.MethodGet, "/admin/model-alias-inventory", nil))
	server.HandleModelAliasInventory(c)
	if w.Code != http.StatusOK {
		t.Fatalf("期望状态码 200，实际 %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Success bool                `json:"success"`
		Data    ModelAliasInventory `json:"data"`
	}
	mustUnmarshalJSON(t, w.Body.Bytes(), &resp)
	if !resp.Success {
		t.Fatalf("期望 success=true: %s", w.Body.String())
	}
	return resp.Data
}

func findCandidate(inventory ModelAliasInventory, name string) *model.ModelAliasCandidate {
	for i := range inventory.Candidates {
		if inventory.Candidates[i].Model == name {
			return &inventory.Candidates[i]
		}
	}
	return nil
}

func TestHandleModelAliasInventoryCountsChannelsPerModel(t *testing.T) {
	server, _, cleanup := setupAdminTestServer(t)
	defer cleanup()

	createAliasTestChannel(t, server, "relay-a", true, "glm-5.2", "gpt-5.6-sol")
	createAliasTestChannel(t, server, "relay-b", true, "glm-5.2")
	createAliasTestChannel(t, server, "relay-c", true, "z.ai/glm-5.2")

	inventory := fetchAliasInventory(t, server)

	shared := findCandidate(inventory, "glm-5.2")
	if shared == nil {
		t.Fatalf("清单中缺少 glm-5.2：%+v", inventory.Candidates)
	}
	if shared.ChannelCount != 2 {
		t.Errorf("glm-5.2 ChannelCount=%d，期望 2", shared.ChannelCount)
	}
	if len(shared.ChannelNames) != 2 {
		t.Errorf("glm-5.2 ChannelNames=%v，期望 2 个", shared.ChannelNames)
	}
	// Widest availability first, so the most useful model to unify leads.
	if inventory.Candidates[0].Model != "glm-5.2" {
		t.Errorf("首项=%q，期望覆盖渠道最多的 glm-5.2", inventory.Candidates[0].Model)
	}
	if inventory.TotalModels != 3 {
		t.Errorf("TotalModels=%d，期望 3", inventory.TotalModels)
	}
}

// Disabled channels must not contribute models: mapping to them cannot route.
func TestHandleModelAliasInventorySkipsDisabledChannels(t *testing.T) {
	server, _, cleanup := setupAdminTestServer(t)
	defer cleanup()

	createAliasTestChannel(t, server, "enabled-relay", true, "glm-5.2")
	createAliasTestChannel(t, server, "disabled-relay", false, "retired-model")

	inventory := fetchAliasInventory(t, server)

	if findCandidate(inventory, "retired-model") != nil {
		t.Errorf("停用渠道的模型不应出现：%+v", inventory.Candidates)
	}
	if findCandidate(inventory, "glm-5.2") == nil {
		t.Errorf("启用渠道的模型缺失：%+v", inventory.Candidates)
	}
}

// Case variants are distinct routing keys, so both must stay listed and be
// offered as one merge suggestion.
func TestHandleModelAliasInventoryKeepsCaseVariantsAndSuggestsMerge(t *testing.T) {
	server, _, cleanup := setupAdminTestServer(t)
	defer cleanup()

	createAliasTestChannel(t, server, "relay-upper", true, "GLM-5.2")
	createAliasTestChannel(t, server, "relay-lower", true, "glm-5.2")

	inventory := fetchAliasInventory(t, server)

	if findCandidate(inventory, "GLM-5.2") == nil || findCandidate(inventory, "glm-5.2") == nil {
		t.Fatalf("大小写变体都应列出：%+v", inventory.Candidates)
	}
	if len(inventory.Suggestions) != 1 {
		t.Fatalf("期望 1 组建议，实际 %d：%+v", len(inventory.Suggestions), inventory.Suggestions)
	}
	if got := len(inventory.Suggestions[0].Members); got != 2 {
		t.Errorf("建议成员数=%d，期望 2：%+v", got, inventory.Suggestions[0])
	}
}

func TestHandleModelAliasInventoryEmptyWithoutChannels(t *testing.T) {
	server, _, cleanup := setupAdminTestServer(t)
	defer cleanup()

	inventory := fetchAliasInventory(t, server)
	if inventory.TotalModels != 0 {
		t.Errorf("TotalModels=%d，期望 0", inventory.TotalModels)
	}
	// Must serialize as [] rather than null so the console can map over it.
	if inventory.Candidates == nil {
		t.Error("Candidates 不应为 nil，前端会直接遍历")
	}
	if inventory.Suggestions == nil {
		t.Error("Suggestions 不应为 nil，前端会直接遍历")
	}
}
