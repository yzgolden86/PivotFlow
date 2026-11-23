package app

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

// StatsCache 统计结果缓存层
//
// 核心职责：
// - 缓存统计查询结果，减少重复聚合计算
// - 智能 TTL：越近的数据 TTL 越短
// - filter 哈希：支持复杂过滤器的缓存键生成
// - 定期清理：后台 goroutine 清理过期条目，防止内存泄漏
// - 容量限制：最多 1000 个条目，超过时强制清理
//
// 设计原则：
// - KISS：简单的 sync.Map，避免过度工程
// - 透明降级：缓存失效不影响业务
type StatsCache struct {
	store      storage.Store
	cache      sync.Map     // key: cacheKey, value: *cachedStats
	writeMu    sync.Mutex   // Serializes entry accounting and capacity checks.
	entryCount atomic.Int64 // 当前缓存条目数（原子计数，避免锁）
	stopCh     chan struct{}
	stopWg     sync.WaitGroup
}

const maxCacheEntries = 1000 // 最大缓存条目数

// cachedStats 缓存的统计数据
type cachedStats struct {
	data   any       // Statistics, RPM or the scoped cost summary.
	expiry time.Time // 过期时间
}

// NewStatsCache 创建统计缓存实例
func NewStatsCache(store storage.Store) *StatsCache {
	sc := &StatsCache{
		store:  store,
		stopCh: make(chan struct{}),
	}

	// 启动后台清理 goroutine
	sc.stopWg.Add(1)
	go sc.cleanupWorker()

	return sc
}

// cleanupWorker 后台清理过期缓存条目
func (sc *StatsCache) cleanupWorker() {
	defer sc.stopWg.Done()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-sc.stopCh:
			return
		case <-ticker.C:
			sc.cleanupExpired()
		}
	}
}

// cleanupExpired 清理所有过期条目
func (sc *StatsCache) cleanupExpired() {
	sc.writeMu.Lock()
	defer sc.writeMu.Unlock()
	sc.cleanupExpiredLocked()
}

func (sc *StatsCache) cleanupExpiredLocked() {
	now := time.Now()
	sc.cache.Range(func(key, value any) bool {
		cs := value.(*cachedStats)
		if now.After(cs.expiry) {
			if _, ok := sc.cache.LoadAndDelete(key); ok {
				sc.entryCount.Add(-1)
			}
		}
		return true
	})
}

// storeCache keeps a fixed budget across all statistics, including cost filters.
// When the budget is full, a read still returns its SQL result without caching.
func (sc *StatsCache) storeCache(key string, value *cachedStats) {
	sc.writeMu.Lock()
	defer sc.writeMu.Unlock()
	if _, loaded := sc.cache.Load(key); loaded {
		sc.cache.Store(key, value)
		return
	}
	if sc.entryCount.Load() >= maxCacheEntries {
		sc.cleanupExpiredLocked()
		if sc.entryCount.Load() >= maxCacheEntries {
			return
		}
	}
	sc.cache.Store(key, value)
	sc.entryCount.Add(1)
}

// Close 关闭缓存（停止清理 goroutine）
func (sc *StatsCache) Close() {
	close(sc.stopCh)
	sc.stopWg.Wait()
}

// GetStats 获取统计数据（带缓存）
func (sc *StatsCache) GetStats(ctx context.Context, startTime, endTime time.Time, filter *model.LogFilter, isToday bool) ([]model.StatsEntry, error) {
	key := buildCacheKey("stats", startTime, endTime, filter)

	// 尝试缓存
	if cached, ok := sc.cache.Load(key); ok {
		cs := cached.(*cachedStats)
		if time.Now().Before(cs.expiry) {
			return cloneStatsEntries(cs.data.([]model.StatsEntry)), nil
		}
	}

	// 缓存未命中，查询数据库
	result, err := sc.store.GetStats(ctx, startTime, endTime, filter, isToday)
	if err != nil {
		return nil, err
	}

	// 写入缓存
	ttl := calculateTTL(endTime)
	cachedResult := cloneStatsEntries(result)
	sc.storeCache(key, &cachedStats{
		data:   cachedResult,
		expiry: time.Now().Add(ttl),
	})

	return cloneStatsEntries(cachedResult), nil
}

// GetStatsLite 获取轻量统计数据（带缓存）
func (sc *StatsCache) GetStatsLite(ctx context.Context, startTime, endTime time.Time, filter *model.LogFilter) ([]model.StatsEntry, error) {
	key := buildCacheKey("stats_lite", startTime, endTime, filter)

	// 尝试缓存
	if cached, ok := sc.cache.Load(key); ok {
		cs := cached.(*cachedStats)
		if time.Now().Before(cs.expiry) {
			return cloneStatsEntries(cs.data.([]model.StatsEntry)), nil
		}
	}

	// 缓存未命中，查询数据库
	result, err := sc.store.GetStatsLite(ctx, startTime, endTime, filter)
	if err != nil {
		return nil, err
	}

	// 写入缓存
	ttl := calculateTTL(endTime)
	cachedResult := cloneStatsEntries(result)
	sc.storeCache(key, &cachedStats{
		data:   cachedResult,
		expiry: time.Now().Add(ttl),
	})

	return cloneStatsEntries(cachedResult), nil
}

// GetClientProtocolStats 获取按客户端入口协议聚合的首页统计（带缓存）。
func (sc *StatsCache) GetClientProtocolStats(ctx context.Context, startTime, endTime time.Time, filter *model.LogFilter) ([]model.ClientProtocolStats, error) {
	key := buildCacheKey("client_protocol_stats", startTime, endTime, filter)

	if cached, ok := sc.cache.Load(key); ok {
		cs := cached.(*cachedStats)
		if time.Now().Before(cs.expiry) {
			return slices.Clone(cs.data.([]model.ClientProtocolStats)), nil
		}
	}

	result, err := sc.store.GetClientProtocolStats(ctx, startTime, endTime, filter)
	if err != nil {
		return nil, err
	}

	sc.storeCache(key, &cachedStats{
		data:   slices.Clone(result),
		expiry: time.Now().Add(calculateTTL(endTime)),
	})
	return result, nil
}

// GetCostSummary caches one SQL aggregate under the complete request scope.
// Copies isolate the cached value from handler and caller mutations.
func (sc *StatsCache) GetCostSummary(ctx context.Context, startTime, endTime time.Time, filter *model.LogFilter) (*model.CostSummary, error) {
	key := buildCacheKey("cost_summary", startTime, endTime, filter)
	if cached, ok := sc.cache.Load(key); ok {
		cs := cached.(*cachedStats)
		if time.Now().Before(cs.expiry) {
			result := *cs.data.(*model.CostSummary)
			return &result, nil
		}
	}
	result, err := sc.store.GetCostSummary(ctx, startTime, endTime, filter)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("cost summary unavailable")
	}
	cached := *result
	sc.storeCache(key, &cachedStats{data: &cached, expiry: time.Now().Add(calculateTTL(endTime))})
	response := cached
	return &response, nil
}

// GetRPMStats 获取 RPM 统计（带缓存）
func (sc *StatsCache) GetRPMStats(ctx context.Context, startTime, endTime time.Time, filter *model.LogFilter, isToday bool) (*model.RPMStats, error) {
	key := buildCacheKey("rpm", startTime, endTime, filter)

	// 尝试缓存
	if cached, ok := sc.cache.Load(key); ok {
		cs := cached.(*cachedStats)
		if time.Now().Before(cs.expiry) {
			return cloneStatsScalar(cs.data.(*model.RPMStats)), nil
		}
	}

	// 缓存未命中，查询数据库
	result, err := sc.store.GetRPMStats(ctx, startTime, endTime, filter, isToday)
	if err != nil {
		return nil, err
	}

	// 写入缓存
	ttl := calculateTTL(endTime)
	sc.storeCache(key, &cachedStats{
		data:   cloneStatsScalar(result),
		expiry: time.Now().Add(ttl),
	})

	return result, nil
}

func cloneStatsScalar[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneStatsEntries(entries []model.StatsEntry) []model.StatsEntry {
	result := slices.Clone(entries)
	for i := range result {
		entry := &result[i]
		entry.ChannelID = cloneStatsScalar(entry.ChannelID)
		entry.ChannelPriority = cloneStatsScalar(entry.ChannelPriority)
		entry.CostMultiplier = cloneStatsScalar(entry.CostMultiplier)
		entry.ActualCostMultiplierMin = cloneStatsScalar(entry.ActualCostMultiplierMin)
		entry.ActualCostMultiplierMax = cloneStatsScalar(entry.ActualCostMultiplierMax)
		entry.AvgFirstByteTimeSeconds = cloneStatsScalar(entry.AvgFirstByteTimeSeconds)
		entry.AvgDurationSeconds = cloneStatsScalar(entry.AvgDurationSeconds)
		entry.LastSuccessAt = cloneStatsScalar(entry.LastSuccessAt)
		entry.LastSuccessID = cloneStatsScalar(entry.LastSuccessID)
		entry.LastRequestAt = cloneStatsScalar(entry.LastRequestAt)
		entry.LastRequestID = cloneStatsScalar(entry.LastRequestID)
		entry.LastRequestStatus = cloneStatsScalar(entry.LastRequestStatus)
		entry.PeakRPM = cloneStatsScalar(entry.PeakRPM)
		entry.AvgRPM = cloneStatsScalar(entry.AvgRPM)
		entry.RecentRPM = cloneStatsScalar(entry.RecentRPM)
		entry.TotalInputTokens = cloneStatsScalar(entry.TotalInputTokens)
		entry.TotalOutputTokens = cloneStatsScalar(entry.TotalOutputTokens)
		entry.TotalCacheReadInputTokens = cloneStatsScalar(entry.TotalCacheReadInputTokens)
		entry.TotalCacheCreationInputTokens = cloneStatsScalar(entry.TotalCacheCreationInputTokens)
		entry.TotalCost = cloneStatsScalar(entry.TotalCost)
		entry.EffectiveCost = cloneStatsScalar(entry.EffectiveCost)
		entry.HealthTimeline = slices.Clone(entry.HealthTimeline)
	}
	return result
}

// buildCacheKey 生成缓存键
func buildCacheKey(typ string, startTime, endTime time.Time, filter *model.LogFilter) string {
	// Cache the exact millisecond SQL window. TTL controls freshness; it must
	// not merge distinct fixed ranges merely because both end near time.Now().
	filterHash := hashFilter(filter)
	return fmt.Sprintf("%s:%d:%d:%s", typ, startTime.UnixMilli(), endTime.UnixMilli(), filterHash)
}

// hashFilter 对 filter 进行哈希
func hashFilter(filter *model.LogFilter) string {
	if filter == nil {
		return "nil"
	}

	// 构建 filter 的字符串表示
	var parts []string
	if filter.ChannelID != nil {
		parts = append(parts, fmt.Sprintf("ch:%d", *filter.ChannelID))
	}
	if filter.UpstreamProtocol != "" {
		parts = append(parts, fmt.Sprintf("upstream_protocol:%s", filter.UpstreamProtocol))
	}
	if filter.ChannelName != "" {
		parts = append(parts, fmt.Sprintf("name_exact:%s", filter.ChannelName))
	}
	if filter.Model != "" {
		parts = append(parts, fmt.Sprintf("model:%s", filter.Model))
	}
	if filter.ChannelNameLike != "" {
		parts = append(parts, fmt.Sprintf("name:%s", filter.ChannelNameLike))
	}
	if filter.ModelLike != "" {
		parts = append(parts, fmt.Sprintf("model_like:%s", filter.ModelLike))
	}
	if filter.AuthTokenID != nil {
		parts = append(parts, fmt.Sprintf("auth:%d", *filter.AuthTokenID))
	}
	if filter.StatusCode != nil {
		parts = append(parts, fmt.Sprintf("status:%d", *filter.StatusCode))
	}
	if filter.StatusCodeMin != nil {
		parts = append(parts, fmt.Sprintf("status_min:%d", *filter.StatusCodeMin))
	}
	if filter.LogSource != "" {
		parts = append(parts, fmt.Sprintf("source:%s", filter.LogSource))
	}
	if filter.Search != "" {
		parts = append(parts, fmt.Sprintf("search:%s", filter.Search))
	}
	if filter.RequestID != "" {
		parts = append(parts, fmt.Sprintf("request_id:%s", filter.RequestID))
	}
	if filter.CostSource != "" {
		parts = append(parts, fmt.Sprintf("cost_source:%s", filter.CostSource))
	}
	if filter.CostStatus != "" {
		parts = append(parts, fmt.Sprintf("cost_status:%s", filter.CostStatus))
	}
	if filter.CostBasis != "" {
		parts = append(parts, fmt.Sprintf("cost_basis:%s", filter.CostBasis))
	}

	// 排序确保顺序一致性
	sort.Strings(parts)

	// 计算 SHA256 哈希
	h := sha256.New()
	for _, part := range parts {
		// Length framing preserves field boundaries even when a user-supplied
		// model or search string contains another field's textual prefix.
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(part))))
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))[:16] // 取前16字符即可
}

// calculateTTL 根据时间范围计算 TTL
//
// TTL 策略：越近的数据 TTL 越短
//   - 最近 1 小时：30 秒
//   - 今天：5 分钟
//   - 最近 7 天：30 分钟
//   - 历史数据：2 小时
func calculateTTL(endTime time.Time) time.Duration {
	now := time.Now()

	// 最近 1 小时
	if endTime.After(now.Add(-1 * time.Hour)) {
		return 30 * time.Second
	}

	// 今天
	if endTime.After(now.Add(-24 * time.Hour)) {
		return 5 * time.Minute
	}

	// 最近 7 天
	if endTime.After(now.Add(-7 * 24 * time.Hour)) {
		return 30 * time.Minute
	}

	// 历史数据
	return 2 * time.Hour
}
