package app

import (
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"sync"
	"time"

	modelpkg "github.com/yzgolden86/PivotFlow/internal/model"
)

// SmoothWeightedRR 平滑加权轮询调度器
// 算法来源：Nginx upstream smooth weighted round-robin
type SmoothWeightedRR struct {
	mu     sync.Mutex
	states map[rrScope]*rrGroupState
}

type rrGroupKey [sha256.Size]byte

// rrScope 标识一个轮询组。
//
// universe 必须来自「该模型下全部候选渠道」这个稳定集合，而不是冷却过滤后的存活集合：
// 后者会随任意一个渠道进入/退出冷却而改变，导致本组换成一个全新的 state、
// currentWeights 全部归零。归零后 selectByWeight 只能走「取最大权重、同权重比 ID 小」
// 的冷启动兜底，于是在权重相同的等价渠道之间永远选中同一个（ID 最小的那个），
// 其余渠道被饿死。用稳定集合做键后，权重能跨冷却抖动累积，冷却中的渠道只是本轮
// 不加权、不被选中，恢复后凭已累积的权重优先补偿。
//
// tier 区分同一 universe 内的不同（有效）优先级层，避免高低优先级共用游标。
type rrScope struct {
	universe rrGroupKey
	tier     int64
}

// rrGroupState 单个优先级组的轮询状态
type rrGroupState struct {
	currentWeights map[int64]int // channelID -> currentWeight
	lastAccess     time.Time     // 最后访问时间，用于过期清理
}

// NewSmoothWeightedRR 创建平滑加权轮询调度器
func NewSmoothWeightedRR() *SmoothWeightedRR {
	rr := &SmoothWeightedRR{
		states: make(map[rrScope]*rrGroupState),
	}
	return rr
}

// selectByWeight 从渠道列表中选择下一个渠道（平滑加权轮询）。
// channels: 同优先级的渠道列表，必须是调用方私有的 slice——本函数原地重排它。
// weights: 与 channels 等长的权重（通常是有效Key数量）。
// 返回: 按轮询顺序排列的渠道列表（第一个是本次选中的）。
func (rr *SmoothWeightedRR) selectByWeight(
	channels []*modelpkg.Config,
	weights []int,
	scope rrScope,
) []*modelpkg.Config {
	n := len(channels)
	if n <= 1 || len(weights) != n {
		return channels
	}

	rr.mu.Lock()
	defer rr.mu.Unlock()

	// 获取或创建组状态
	state, exists := rr.states[scope]
	if !exists {
		state = &rrGroupState{
			currentWeights: make(map[int64]int),
		}
		rr.states[scope] = state
	}
	state.lastAccess = time.Now()

	// 增加当前权重并计算总权重。
	totalWeight := 0
	for i, ch := range channels {
		w := weights[i]
		totalWeight += w
		state.currentWeights[ch.ID] += w
	}
	if totalWeight == 0 {
		return channels
	}

	// Nginx 平滑加权轮询算法：
	// 1. 每个节点的 currentWeight += weight
	// 2. 选择 currentWeight 最大的节点
	// 3. 被选中节点的 currentWeight -= totalWeight

	// 找到 currentWeight 最大的节点
	maxWeight := state.currentWeights[channels[0].ID]
	selectedIdx := 0
	for i := 1; i < n; i++ {
		cw := state.currentWeights[channels[i].ID]                                            //nolint:gosec // G602: i < n = len(channels)
		if cw > maxWeight || (cw == maxWeight && channels[i].ID < channels[selectedIdx].ID) { //nolint:gosec // G602: 同上
			maxWeight = cw
			selectedIdx = i
		}
	}

	// 被选中节点减去总权重
	state.currentWeights[channels[selectedIdx].ID] -= totalWeight

	// 调用方传入请求私有 slice，原地把选中渠道移到首位并保持其余顺序。
	if selectedIdx > 0 {
		selected := channels[selectedIdx]
		copy(channels[1:selectedIdx+1], channels[:selectedIdx])
		channels[0] = selected
	}
	return channels
}

// selectWithCooldownInPlace 带冷却感知的平滑加权轮询，权重 = 有效Key数量（总Key - 冷却中Key）。
// 与 selectByWeight 一样原地重排 channels，调用方必须持有私有 slice。
func (rr *SmoothWeightedRR) selectWithCooldownInPlace(
	channels []*modelpkg.Config,
	keyCooldowns map[int64]map[int]time.Time,
	now time.Time,
	scope rrScope,
) []*modelpkg.Config {
	if len(channels) <= 1 {
		return channels
	}
	weights := make([]int, len(channels))
	for i, ch := range channels {
		weights[i] = calcEffectiveKeyCount(ch, keyCooldowns, now)
	}
	return rr.selectByWeight(channels, weights, scope)
}

// rrUniverseKey 对排序后的渠道 ID 做固定宽度摘要。
// 输入顺序不影响结果；固定宽度编码避免每个 ID 的十进制字符串分配。
//
// 传入的必须是稳定集合（冷却过滤之前的候选渠道），理由见 rrScope 注释。
func rrUniverseKey(channels []*modelpkg.Config) rrGroupKey {
	ids := make([]int64, 0, len(channels))
	for _, ch := range channels {
		if ch == nil {
			continue
		}
		ids = append(ids, ch.ID)
	}
	if len(ids) == 0 {
		return sha256.Sum256(nil)
	}
	slices.Sort(ids)

	hasher := sha256.New()
	var encoded [8]byte
	for _, id := range ids {
		binary.BigEndian.PutUint64(encoded[:], uint64(id))
		_, _ = hasher.Write(encoded[:])
	}
	var key rrGroupKey
	_ = hasher.Sum(key[:0])
	return key
}

// Cleanup 清理过期的轮询状态（可选，避免内存泄漏）
// 建议在后台定期调用
func (rr *SmoothWeightedRR) Cleanup(maxAge time.Duration) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	now := time.Now()
	for key, state := range rr.states {
		if now.Sub(state.lastAccess) > maxAge {
			delete(rr.states, key)
		}
	}
}

// ResetAll 清空所有轮询状态。
//
// 警告：不要把它挂到渠道缓存失效这类高频路径上。清零后每次选择都是冷启动，
// 会退化成「取最大权重、同权重比 ID 小」，在等价渠道之间永远选中同一个，
// 其余渠道被饿死（详见 InvalidateChannelListCache 里的说明）。
// 拓扑变化由 rrScope 分域自然处理，正常运行期不需要调用本函数。
func (rr *SmoothWeightedRR) ResetAll() {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.states = make(map[rrScope]*rrGroupState)
}

// calcEffectiveKeyCount 计算渠道的有效Key数量（排除冷却中的Key）
func calcEffectiveKeyCount(cfg *modelpkg.Config, keyCooldowns map[int64]map[int]time.Time, now time.Time) int {
	total := cfg.KeyCount
	if total <= 0 {
		return 1 // 最小为1
	}

	keyMap, ok := keyCooldowns[cfg.ID]
	if !ok || len(keyMap) == 0 {
		return total // 无冷却信息，使用全部Key数量
	}

	// 统计冷却中的Key数量
	cooledCount := 0
	for _, cooldownUntil := range keyMap {
		if cooldownUntil.After(now) {
			cooledCount++
		}
	}

	effective := total - cooledCount
	if effective <= 0 {
		return 1 // 最小为1
	}
	return effective
}
