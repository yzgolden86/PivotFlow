package app

import (
	"sync"
	"time"

	modelpkg "github.com/yzgolden86/PivotFlow/internal/model"
)

// 路由策略取值。均衡策略是历史行为，也是默认值。
const (
	// RouteStrategyBalanced 同优先级内按权重平滑轮询，每个请求换一个渠道。
	RouteStrategyBalanced = "balanced"
	// RouteStrategySticky 沿用上次成功的渠道，直到它失败才换下一个。
	RouteStrategySticky = "sticky"
)

const routeStrategySettingKey = "route_strategy"

// stickyEntryTTL 限制粘性记录的有效期。超时后回到正常轮询，
// 避免长期空闲的模型把流量永久钉在一个可能早已变慢的渠道上。
const stickyEntryTTL = 30 * time.Minute

// stickyRouter 记录每个模型最近一次成功的渠道。
//
// 只保存「上次成功」这一个事实，不做任何计数或健康评估：粘性策略的语义就是
// 成功即继续、失败即切换，评分工作交给健康度排序（另一个独立开关）。
type stickyRouter struct {
	mu      sync.RWMutex
	entries map[string]stickyEntry // key: 模型名
}

type stickyEntry struct {
	channelID int64
	at        time.Time
}

func newStickyRouter() *stickyRouter {
	return &stickyRouter{entries: make(map[string]stickyEntry)}
}

// remember 记录某模型最近一次成功的渠道。
func (r *stickyRouter) remember(modelName string, channelID int64) {
	if r == nil || modelName == "" || channelID <= 0 {
		return
	}
	r.mu.Lock()
	r.entries[modelName] = stickyEntry{channelID: channelID, at: time.Now()}
	r.mu.Unlock()
}

// forget 清除某模型的粘性，使下一个请求回到正常轮询顺序。
// 渠道失败时调用：粘性的语义是「成功才继续用」。
func (r *stickyRouter) forget(modelName string) {
	if r == nil || modelName == "" {
		return
	}
	r.mu.Lock()
	delete(r.entries, modelName)
	r.mu.Unlock()
}

// forgetChannel 清除指向某渠道的全部粘性记录，渠道被删除或禁用时调用。
func (r *stickyRouter) forgetChannel(channelID int64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	for modelName, entry := range r.entries {
		if entry.channelID == channelID {
			delete(r.entries, modelName)
		}
	}
	r.mu.Unlock()
}

// preferred 返回该模型应优先使用的渠道 ID；0 表示没有可用的粘性记录。
func (r *stickyRouter) preferred(modelName string, now time.Time) int64 {
	if r == nil || modelName == "" {
		return 0
	}
	r.mu.RLock()
	entry, ok := r.entries[modelName]
	r.mu.RUnlock()
	if !ok {
		return 0
	}
	if now.Sub(entry.at) > stickyEntryTTL {
		r.forget(modelName)
		return 0
	}
	return entry.channelID
}

// cleanup 清理过期记录，避免长期运行后 map 无界增长。
func (r *stickyRouter) cleanup(maxAge time.Duration) {
	if r == nil || maxAge <= 0 {
		return
	}
	cutoff := time.Now().Add(-maxAge)
	r.mu.Lock()
	for modelName, entry := range r.entries {
		if entry.at.Before(cutoff) {
			delete(r.entries, modelName)
		}
	}
	r.mu.Unlock()
}

// normalizeRouteStrategy 把设置值收敛到已知策略，未知值回落到均衡轮询。
func normalizeRouteStrategy(value string) string {
	if value == RouteStrategySticky {
		return RouteStrategySticky
	}
	return RouteStrategyBalanced
}

// routeStrategy 返回本次进程生效的路由策略。
//
// 取启动时加载的快照而不是每次查配置：ConfigService 的缓存在设置更新后并不刷新
// （见 ConfigService.UpdateSetting 的注释），实时读取只会拿到启动时的旧值，
// 反而给人「改了却没生效」的错觉。该项按需重启生效，与 model_alias_groups 一致。
func (s *Server) routeStrategy() string {
	if s == nil {
		return RouteStrategyBalanced
	}
	return normalizeRouteStrategy(s.routeStrategyMode)
}

// applyStickyPreference 把上次成功的渠道提到候选列表首位。
//
// 优先级仍然优先：只有当粘性渠道与当前首位处于同一优先级层时才前移。
// 否则高优先级渠道会被一个低优先级的粘性渠道挤掉，违背优先级语义。
//
// channels 必须已按优先级排好序（调用方保证），且是调用方私有的 slice。
func applyStickyPreference(channels []*modelpkg.Config, preferredID int64) []*modelpkg.Config {
	if len(channels) <= 1 || preferredID <= 0 {
		return channels
	}
	if channels[0] == nil || channels[0].ID == preferredID {
		return channels
	}
	topPriority := channels[0].Priority
	for index, cfg := range channels {
		if cfg == nil || cfg.ID != preferredID {
			continue
		}
		// 跨优先级层不前移：优先级是硬约束。
		if cfg.Priority != topPriority {
			return channels
		}
		copy(channels[1:index+1], channels[:index])
		channels[0] = cfg
		return channels
	}
	return channels
}
