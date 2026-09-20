package app

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// 这条测的是「模型统一映射改完不生效」：设置保存后必须**立刻**改变 /v1/models
// 的输出，且不触发重启。
//
// 为什么值得单独测：BatchUpdateSettings 刻意只写库、不刷缓存（默认路径是保存后
// 重启进程），所以「保存成功」和「行为改变」是两件事。少调一次 applyLiveSettings，
// 接口照样返回 200 且 restart_required=false —— 只有输出列表还是旧的，界面上完全
// 看不出来。这就是当初那个缺陷的形状。
func TestModelAliasGroupsTakesEffectWithoutRestart(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()

	ctx := context.Background()
	// setupAdminTestServer 只搭了 store/statsCache/client，配置与注册表要按生产
	// 顺序自己接上：先加载配置，再由配置派生注册表。
	server.configService = NewConfigService(store)
	if err := server.configService.LoadDefaults(ctx); err != nil {
		t.Fatalf("LoadDefaults failed: %v", err)
	}
	server.modelAliases = loadModelAliasRegistry(server.configService)

	restartCh := make(chan struct{}, 4)
	previousRestart := RestartFunc
	RestartFunc = func() { restartCh <- struct{}{} }
	t.Cleanup(func() { RestartFunc = previousRestart })

	// 一个渠道同时带三种拼写 + 一个无关模型：折叠后应当只剩 canonical 与 gpt-4o。
	_, err := store.CreateConfig(ctx, &model.Config{
		Name:    "alias-live",
		URLs:    model.ChannelURLs{{URL: "https://example.com"}},
		Enabled: true,
		ModelEntries: []model.ModelEntry{
			{Model: "deepseek-v4-flash"},
			{Model: "DeepSeek-V4-Flash"},
			{Model: "deepseek/deepseek-v4-flash"},
			{Model: "gpt-4o"},
		},
	})
	if err != nil {
		t.Fatalf("CreateConfig failed: %v", err)
	}

	listModels := func(t *testing.T) []string {
		t.Helper()
		c, w := newTestContext(t, newRequest(http.MethodGet, "/v1/models", nil))
		server.handleListOpenAIModels(c)
		if w.Code != http.StatusOK {
			t.Fatalf("status=%d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		mustUnmarshalJSON(t, w.Body.Bytes(), &resp)
		out := make([]string, 0, len(resp.Data))
		for _, item := range resp.Data {
			out = append(out, item.ID)
		}
		sort.Strings(out)
		return out
	}

	if before := listModels(t); len(before) != 4 {
		t.Fatalf("映射生效前的模型列表=%v，期望 4 个未折叠的名字", before)
	}

	groups := `[{"canonical":"deepseek-v4-flash","aliases":["DeepSeek-V4-Flash","deepseek/deepseek-v4-flash"],"enabled":true}]`
	body, err := json.Marshal(map[string]string{modelAliasGroupsSettingKey: groups})
	if err != nil {
		t.Fatalf("marshal payload failed: %v", err)
	}
	c, w := newTestContext(t, newJSONRequestBytes(http.MethodPost, "/admin/settings/batch", body))
	server.AdminBatchUpdateSettings(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	resp := mustParseAPIResponse[map[string]any](t, w.Body.Bytes())
	if got := resp.Data["restart_required"]; got != false {
		t.Fatalf("restart_required=%v, want false：这个设置已经改成热生效", got)
	}
	select {
	case <-restartCh:
		t.Fatal("热生效的设置不该触发重启")
	case <-time.After(50 * time.Millisecond):
	}

	want := []string{"deepseek-v4-flash", "gpt-4o"}
	if after := listModels(t); !slices.Equal(after, want) {
		t.Fatalf("映射生效后的模型列表=%v，期望 %v（3 种拼写折叠成 1 个 canonical）", after, want)
	}

	// 再关掉映射，验证反向也即时生效（不是只能单向生效）。
	body, err = json.Marshal(map[string]string{modelAliasGroupsSettingKey: "[]"})
	if err != nil {
		t.Fatalf("marshal payload failed: %v", err)
	}
	c, w = newTestContext(t, newJSONRequestBytes(http.MethodPost, "/admin/settings/batch", body))
	server.AdminBatchUpdateSettings(c)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want %d body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if reverted := listModels(t); len(reverted) != 4 {
		t.Fatalf("清空映射后的模型列表=%v，期望恢复成 4 个名字", reverted)
	}
}

// 热生效必须是「换快照」而不是「就地改 map」：读者在代理热路径上并发跑，
// 就地改会和它们竞态。这条在读写并发下跑一轮 —— 若有人改回就地改 map，
// Go 运行时的 map 并发检测大概率会以
// `fatal error: concurrent map read and map write` 直接崩掉。
//
// 注意：**本机跑不了 -race**（默认 CGO_ENABLED=0，且没装 gcc，`-race` 会报
// "requires cgo"），所以这是概率性信号而不是证明。真正的保证来自
// modelAliasIndex 的不可变快照 + atomic.Pointer 设计本身。
func TestModelAliasRegistryReloadUnderConcurrentReads(t *testing.T) {
	server, store, cleanup := setupAdminTestServer(t)
	defer cleanup()

	ctx := context.Background()
	server.configService = NewConfigService(store)
	if err := server.configService.LoadDefaults(ctx); err != nil {
		t.Fatalf("LoadDefaults failed: %v", err)
	}
	server.modelAliases = loadModelAliasRegistry(server.configService)

	registry := server.modelAliases
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			registry.reload(server.configService)
		}
	}()
	for i := 0; i < 2000; i++ {
		_ = registry.namesFor("deepseek-v4-flash")
		_ = registry.canonicalizeModelNames([]string{"DeepSeek-V4-Flash", "gpt-4o"})
	}
	<-done
}
