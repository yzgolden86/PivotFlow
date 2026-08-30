package app

import (
	"testing"
	"time"

	modelpkg "github.com/yzgolden86/PivotFlow/internal/model"
)

func TestStickyRouterRemembersAndForgets(t *testing.T) {
	router := newStickyRouter()
	now := time.Now()

	if got := router.preferred("glm-5.2", now); got != 0 {
		t.Fatalf("preferred=%d, want 0 before anything succeeded", got)
	}

	router.remember("glm-5.2", 44)
	if got := router.preferred("glm-5.2", now); got != 44 {
		t.Fatalf("preferred=%d, want 44", got)
	}

	// 其他模型互不影响：粘性按模型独立记录。
	if got := router.preferred("claude-opus-5", now); got != 0 {
		t.Fatalf("preferred(other model)=%d, want 0", got)
	}

	router.forget("glm-5.2")
	if got := router.preferred("glm-5.2", now); got != 0 {
		t.Fatalf("preferred=%d, want 0 after forget", got)
	}
}

// 渠道被删除后，指向它的全部粘性记录都必须失效，否则会一直优先一个不存在的渠道。
func TestStickyRouterForgetChannelClearsEveryModel(t *testing.T) {
	router := newStickyRouter()
	now := time.Now()

	router.remember("glm-5.2", 44)
	router.remember("claude-opus-5", 44)
	router.remember("gpt-5.6-sol", 47)

	router.forgetChannel(44)

	if got := router.preferred("glm-5.2", now); got != 0 {
		t.Errorf("glm-5.2 preferred=%d, want 0", got)
	}
	if got := router.preferred("claude-opus-5", now); got != 0 {
		t.Errorf("claude-opus-5 preferred=%d, want 0", got)
	}
	if got := router.preferred("gpt-5.6-sol", now); got != 47 {
		t.Errorf("gpt-5.6-sol preferred=%d, want 47 (untouched)", got)
	}
}

// 超过 TTL 的记录不再生效，避免长期空闲的模型被永久钉在一个可能已变慢的渠道。
func TestStickyRouterEntryExpires(t *testing.T) {
	router := newStickyRouter()
	router.remember("glm-5.2", 44)

	stale := time.Now().Add(stickyEntryTTL + time.Minute)
	if got := router.preferred("glm-5.2", stale); got != 0 {
		t.Fatalf("preferred=%d, want 0 after TTL", got)
	}
	// 过期读取应顺手清掉记录。
	router.mu.RLock()
	_, exists := router.entries["glm-5.2"]
	router.mu.RUnlock()
	if exists {
		t.Error("expired entry should be dropped on read")
	}
}

func TestStickyRouterCleanupDropsOldEntries(t *testing.T) {
	router := newStickyRouter()
	router.remember("old-model", 44)
	router.remember("fresh-model", 47)

	// 手工把一条记录改旧。
	router.mu.Lock()
	router.entries["old-model"] = stickyEntry{channelID: 44, at: time.Now().Add(-2 * time.Hour)}
	router.mu.Unlock()

	router.cleanup(time.Hour)

	router.mu.RLock()
	defer router.mu.RUnlock()
	if _, exists := router.entries["old-model"]; exists {
		t.Error("old entry should be reclaimed")
	}
	if _, exists := router.entries["fresh-model"]; !exists {
		t.Error("fresh entry must survive cleanup")
	}
}

// nil 接收者必须安全：策略关闭时这些方法仍会被调用。
func TestStickyRouterNilSafe(t *testing.T) {
	var router *stickyRouter
	router.remember("glm-5.2", 44)
	router.forget("glm-5.2")
	router.forgetChannel(44)
	router.cleanup(time.Hour)
	if got := router.preferred("glm-5.2", time.Now()); got != 0 {
		t.Fatalf("preferred=%d, want 0 on nil router", got)
	}
}

func stickyChannels(specs ...[2]int64) []*modelpkg.Config {
	channels := make([]*modelpkg.Config, 0, len(specs))
	for _, spec := range specs {
		channels = append(channels, &modelpkg.Config{
			ID:       spec[0],
			Priority: int(spec[1]),
			KeyCount: 1,
			Enabled:  true,
		})
	}
	return channels
}

func TestApplyStickyPreferenceMovesChannelToFront(t *testing.T) {
	channels := stickyChannels([2]int64{41, 0}, [2]int64{44, 0}, [2]int64{45, 0})

	got := applyStickyPreference(channels, 45)

	if got[0].ID != 45 {
		t.Fatalf("first=%d, want 45", got[0].ID)
	}
	// 其余顺序保持原样，作为本次请求内的回退顺序。
	if got[1].ID != 41 || got[2].ID != 44 {
		t.Errorf("order=%d,%d,%d; want 45,41,44", got[0].ID, got[1].ID, got[2].ID)
	}
}

// 优先级是硬约束：低优先级的粘性渠道不得挤掉高优先级渠道。
func TestApplyStickyPreferenceRespectsPriority(t *testing.T) {
	// 优先级 10 在前，0 在后（调用方已按优先级降序排好）。
	channels := stickyChannels([2]int64{41, 10}, [2]int64{44, 10}, [2]int64{45, 0})

	got := applyStickyPreference(channels, 45)

	if got[0].ID != 41 {
		t.Fatalf("first=%d, want 41: a lower-priority sticky channel must not jump the queue", got[0].ID)
	}
}

// 同优先级层内可以前移。
func TestApplyStickyPreferenceMovesWithinSameTier(t *testing.T) {
	channels := stickyChannels([2]int64{41, 10}, [2]int64{44, 10}, [2]int64{45, 0})

	got := applyStickyPreference(channels, 44)

	if got[0].ID != 44 {
		t.Fatalf("first=%d, want 44 (same tier as head)", got[0].ID)
	}
	if got[2].ID != 45 {
		t.Errorf("low-priority channel should stay last, got %d", got[2].ID)
	}
}

func TestApplyStickyPreferenceNoOps(t *testing.T) {
	cases := []struct {
		name        string
		channels    []*modelpkg.Config
		preferredID int64
	}{
		{"empty", nil, 44},
		{"single", stickyChannels([2]int64{41, 0}), 41},
		{"no preference", stickyChannels([2]int64{41, 0}, [2]int64{44, 0}), 0},
		{"already first", stickyChannels([2]int64{41, 0}, [2]int64{44, 0}), 41},
		{"not a candidate", stickyChannels([2]int64{41, 0}, [2]int64{44, 0}), 999},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := make([]int64, 0, len(tc.channels))
			for _, cfg := range tc.channels {
				before = append(before, cfg.ID)
			}
			got := applyStickyPreference(tc.channels, tc.preferredID)
			if len(got) != len(tc.channels) {
				t.Fatalf("length changed: %d -> %d", len(tc.channels), len(got))
			}
			for i, cfg := range got {
				if cfg.ID != before[i] {
					t.Fatalf("order changed at %d: %v -> %d", i, before, cfg.ID)
				}
			}
		})
	}
}

func TestNormalizeRouteStrategy(t *testing.T) {
	cases := map[string]string{
		RouteStrategySticky:   RouteStrategySticky,
		RouteStrategyBalanced: RouteStrategyBalanced,
		"":                    RouteStrategyBalanced,
		"nonsense":            RouteStrategyBalanced,
		"STICKY":              RouteStrategyBalanced, // 大小写不容错，避免和数据库里的规范值混淆
	}
	for input, want := range cases {
		if got := normalizeRouteStrategy(input); got != want {
			t.Errorf("normalizeRouteStrategy(%q)=%q, want %q", input, got, want)
		}
	}
}

// 默认必须是均衡轮询：这是历史行为，升级不应改变任何人的路由方式。
func TestServerRouteStrategyDefaultsToBalanced(t *testing.T) {
	var server *Server
	if got := server.routeStrategy(); got != RouteStrategyBalanced {
		t.Errorf("nil server routeStrategy=%q, want %q", got, RouteStrategyBalanced)
	}
	if got := (&Server{}).routeStrategy(); got != RouteStrategyBalanced {
		t.Errorf("zero server routeStrategy=%q, want %q", got, RouteStrategyBalanced)
	}
	if got := (&Server{routeStrategyMode: RouteStrategySticky}).routeStrategy(); got != RouteStrategySticky {
		t.Errorf("sticky server routeStrategy=%q, want sticky", got)
	}
}
