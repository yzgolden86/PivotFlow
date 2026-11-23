//nolint:revive // HybridStore 方法实现 Store 接口，注释在接口定义处
package storage

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/monitoring"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
	"github.com/yzgolden86/PivotFlow/internal/util"
)

// HybridStore 混合存储（MySQL 主 + SQLite 本地缓存）
//
// 核心职责：
// - 读操作：从 SQLite 读取（本地缓存，低延迟）
// - 写操作：先写 MySQL（主存储），再同步更新 SQLite 缓存
// - 统计/日志查询：从 SQLite 查询
//
// 设计原则：
// - MySQL = 主存储（source of truth，持久化与恢复的唯一来源）
// - SQLite = 本地缓存（读加速，允许短暂不一致）
// - 写操作以 MySQL 为准：MySQL 成功即成功，SQLite 失败仅警告
//
// 日志特殊处理（高吞吐场景）：
// - 写入顺序：先写 SQLite（快），再异步同步到 MySQL（备份）
// - 这是性能妥协：日志写入频率高，同步延迟可接受
// - 代价：极端情况下 MySQL 可能丢失少量最新日志
// - 恢复时：SyncManager 从 MySQL 恢复历史日志到 SQLite
type HybridStore struct {
	sqlite *sqlstore.SQLStore // 本地缓存（读路径）
	mysql  *sqlstore.SQLStore // 主存储（写路径）

	// Token accounting mutations update both stores; serialize them so a
	// canonical snapshot cannot race a legacy incremental replica update.
	tokenAccountingMu sync.Mutex

	// configurationMu orders every in-process configuration write and the
	// postcommit snapshot/publication of WithConfigurationTransaction. When both
	// locks are needed, acquire configurationMu before tokenAccountingMu.
	configurationMu sync.Mutex

	// 异步同步队列（logs 与 request history）
	syncCh    chan *syncTask
	syncWg    sync.WaitGroup
	stopCh    chan struct{}
	closeOnce sync.Once
	closeMu   sync.RWMutex
	closing   bool

	// 降级可观测性计数器（参照 LogService.logDropCount 模式）
	// 静默降级在生产中难以察觉，计数器 + 采样告警让运维可见，不改一致性语义
	sqliteSyncFailCount         atomic.Uint64 // SQLite 缓存同步失败累计
	syncQueueDropCount          atomic.Uint64 // MySQL 同步队列满丢弃累计
	logReplicationLastDroppedAt atomic.Int64
	cacheSyncOperation          monitoring.OperationMonitor
	logReplicationOperation     monitoring.OperationMonitor
	traceReplicationOperation   monitoring.OperationMonitor
	startupRecoveryMu           sync.RWMutex
	startupRecovery             *model.StartupRecoveryStats

	// Request history has its own bounded replication budget. Trace batches
	// carry up to 32 summaries with 128 timeline events each, so letting them
	// fill the shared 10k log queue would retain far too much memory.
	traceReplicationQueued        atomic.Int64
	traceReplicationDropped       atomic.Uint64
	traceReplicationFailed        atomic.Uint64
	traceReplicationLastDroppedAt atomic.Int64
}

var _ Store = (*HybridStore)(nil)

// syncTask 同步任务
type syncTask struct {
	operation string
	data      any
}

// syncTaskLog 日志同步数据
type syncTaskLog struct {
	entry *model.LogEntry
}

// syncTaskLogBatch 批量日志同步数据
type syncTaskLogBatch struct {
	entries []*model.LogEntry
}

// syncTaskRequestTraceBatch contains immutable clones of request summaries for
// best-effort replication to the primary database.
type syncTaskRequestTraceBatch struct {
	traces []*model.RequestTrace
}

const (
	syncQueueSize                       = 10000 // 异步同步队列大小
	requestTraceReplicationCapacity     = 64
	requestTraceReplicationDropLogEvery = 10
)

// RequestTraceReplicationStats reports the bounded best-effort clone queue.
// Queued includes trace tasks currently executing or waiting in the shared
// sync queue. Dropped and Failed count trace batch tasks, not timeline events.
type RequestTraceReplicationStats = model.ReplicationRuntimeStats

// NewHybridStore 创建混合存储实例
func NewHybridStore(sqlite, mysql *sqlstore.SQLStore) *HybridStore {
	h := &HybridStore{
		sqlite: sqlite,
		mysql:  mysql,
		syncCh: make(chan *syncTask, syncQueueSize),
		stopCh: make(chan struct{}),
	}

	// 启动异步同步 worker
	h.syncWg.Add(1)
	go h.syncWorker()

	return h
}

// syncToSQLite 同步更新 SQLite 缓存
// SQLite 是本地库，启动时已验证可写，运行时通常不会失败
// 但磁盘空间不足等极端情况仍可能导致写入失败，记录日志以便排查
func (h *HybridStore) syncToSQLite(op string, fn func() error) {
	_ = h.syncSQLite(op, fn)
}

func (h *HybridStore) syncSQLite(op string, fn func() error) error {
	finish := h.cacheSyncOperation.Begin()
	err := fn()
	finish(err)
	if err != nil {
		count := h.sqliteSyncFailCount.Add(1)
		// 采样告警：首次必打印，之后每 10 次一次（避免磁盘满时日志洪泛）
		if count%10 == 1 {
			log.Printf("[WARN] SQLite 同步失败 (%s): %v (累计失败: %d)", op, err, count)
		}
	}
	return err
}

// cloneLogEntryForSync 克隆日志条目（异步队列需要）
func cloneLogEntryForSync(e *model.LogEntry) *model.LogEntry {
	if e == nil {
		return nil
	}
	clone := *e
	// 同步到 MySQL 时丢弃 DebugData：调试原始请求/响应体仅保留在 SQLite，
	// 避免膨胀 MySQL；但 logs 表主数据仍需正常同步
	clone.DebugData = nil
	return &clone
}

// cloneLogEntriesForSync 批量克隆日志条目
func cloneLogEntriesForSync(entries []*model.LogEntry) []*model.LogEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]*model.LogEntry, len(entries))
	for i, e := range entries {
		out[i] = cloneLogEntryForSync(e)
	}
	return out
}

func cloneRequestTraceForSync(trace *model.RequestTrace) (*model.RequestTrace, error) {
	return sqlstore.NormalizeRequestTraceForStorage(trace)
}

// ============================================================================
// 异步同步 Worker（logs 与 request history）
// ============================================================================

func (h *HybridStore) syncWorker() {
	defer h.syncWg.Done()

	for {
		select {
		case <-h.stopCh:
			h.drainSyncQueue()
			return
		case task := <-h.syncCh:
			h.executeSyncTask(task)
		}
	}
}

// drainSyncQueue 处理剩余的同步任务（优雅关闭）
func (h *HybridStore) drainSyncQueue() {
	queueLen := len(h.syncCh)
	timeoutSec := min(5+queueLen/100, 30)
	timeout := time.After(time.Duration(timeoutSec) * time.Second)

	processed := 0
	for {
		select {
		case task := <-h.syncCh:
			h.executeSyncTask(task)
			processed++
		case <-timeout:
			remaining := h.discardQueuedSyncTasks()
			if remaining > 0 {
				log.Printf("[WARN] MySQL 同步关闭超时（已处理 %d），丢弃 %d 个任务", processed, remaining)
			}
			return
		default:
			if processed > 0 {
				log.Printf("[INFO] MySQL 同步队列已清空，共处理 %d 个任务", processed)
			}
			return
		}
	}
}

func (h *HybridStore) discardQueuedSyncTasks() int {
	discarded := 0
	for {
		select {
		case task := <-h.syncCh:
			discarded++
			if task != nil && task.operation == "request_trace_batch" {
				h.releaseRequestTraceReplicationSlot()
				h.recordRequestTraceReplicationDrop("shutdown")
			} else if task != nil && (task.operation == "log" || task.operation == "log_batch") {
				h.recordLogReplicationDrop("shutdown")
			}
		default:
			return discarded
		}
	}
}

// executeSyncTask 执行单个同步任务
func (h *HybridStore) executeSyncTask(task *syncTask) {
	if task == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var err error
	isRequestTrace := task.operation == "request_trace_batch"
	var finish func(error)
	defer func() {
		if finish != nil {
			finish(err)
		}
		if !isRequestTrace {
			return
		}
		h.releaseRequestTraceReplicationSlot()
		if err != nil {
			h.traceReplicationFailed.Add(1)
		}
	}()

	switch task.operation {
	case "log":
		finish = h.logReplicationOperation.Begin()
		data := task.data.(*syncTaskLog)
		err = h.mysql.AddLog(ctx, data.entry)
	case "log_batch":
		finish = h.logReplicationOperation.Begin()
		data := task.data.(*syncTaskLogBatch)
		err = h.mysql.BatchAddLogs(ctx, data.entries)
	case "request_trace_batch":
		finish = h.traceReplicationOperation.Begin()
		data := task.data.(*syncTaskRequestTraceBatch)
		err = h.mysql.BatchAddRequestTraces(ctx, data.traces)
	default:
		return
	}

	if err != nil {
		log.Printf("[WARN] MySQL 同步失败: %v, operation=%s", err, task.operation)
	}
}

func (h *HybridStore) enqueueSyncTask(task *syncTask) bool {
	h.closeMu.RLock()
	defer h.closeMu.RUnlock()
	if h.closing {
		return false
	}
	select {
	case h.syncCh <- task:
		return true
	default:
		return false
	}
}

// enqueueLogSync 将日志同步任务加入队列（非阻塞，队列满则丢弃）
func (h *HybridStore) enqueueLogSync(task *syncTask) {
	if h.enqueueSyncTask(task) {
		return
	}
	h.recordLogReplicationDrop("queue_unavailable")
}

func (h *HybridStore) recordLogReplicationDrop(reason string) {
	count := h.syncQueueDropCount.Add(1)
	h.logReplicationLastDroppedAt.Store(time.Now().UnixMilli())
	// 队列满=MySQL 备份丢日志（数据丢失），ERROR 级；采样首次必打印
	if count%10 == 1 {
		log.Printf("[ERROR] 日志同步任务丢弃 (reason=%s, 累计丢弃: %d)", reason, count)
	}
}

func (h *HybridStore) tryReserveRequestTraceReplicationSlot() bool {
	for {
		queued := h.traceReplicationQueued.Load()
		if queued >= requestTraceReplicationCapacity {
			return false
		}
		if h.traceReplicationQueued.CompareAndSwap(queued, queued+1) {
			return true
		}
	}
}

func (h *HybridStore) releaseRequestTraceReplicationSlot() {
	if h.traceReplicationQueued.Add(-1) < 0 {
		log.Printf("[WARN] request trace replication slot count underflow")
	}
}

func (h *HybridStore) enqueueRequestTraceSync(task *syncTask) {
	if !h.tryReserveRequestTraceReplicationSlot() {
		h.recordRequestTraceReplicationDrop("capacity")
		return
	}
	if h.enqueueSyncTask(task) {
		return
	}
	h.releaseRequestTraceReplicationSlot()
	h.recordRequestTraceReplicationDrop("shared_queue")
}

func (h *HybridStore) recordRequestTraceReplicationDrop(reason string) {
	count := h.traceReplicationDropped.Add(1)
	h.traceReplicationLastDroppedAt.Store(time.Now().UnixMilli())
	if count%requestTraceReplicationDropLogEvery == 1 {
		log.Printf(
			"[WARN] 请求历史同步任务丢弃 (reason=%s, 累计丢弃: %d, capacity=%d)",
			reason,
			count,
			requestTraceReplicationCapacity,
		)
	}
}

// RequestTraceReplicationStats returns a snapshot of best-effort trace clone
// capacity and failures. Local request-history writes do not depend on it.
func (h *HybridStore) RequestTraceReplicationStats() RequestTraceReplicationStats {
	queued := h.traceReplicationQueued.Load()
	if queued < 0 {
		queued = 0
	}
	return RequestTraceReplicationStats{
		Enabled:       true,
		Queued:        int(queued),
		Capacity:      requestTraceReplicationCapacity,
		Dropped:       h.traceReplicationDropped.Load(),
		Failed:        h.traceReplicationFailed.Load(),
		LastDroppedAt: h.traceReplicationLastDroppedAt.Load(),
		Operation:     h.traceReplicationOperation.Snapshot(),
	}
}

// SetStartupRecoveryStats transfers the factory's real startup observations.
func (h *HybridStore) SetStartupRecoveryStats(stats model.StartupRecoveryStats) {
	h.startupRecoveryMu.Lock()
	defer h.startupRecoveryMu.Unlock()
	h.startupRecovery = &stats
}

// StorageRuntimeStats reads only in-memory observations, including when either
// database is unavailable. A later cache write cannot prove earlier repairs.
func (h *HybridStore) StorageRuntimeStats() model.StorageRuntimeStats {
	primary := "unknown"
	if h.mysql != nil {
		primary = h.mysql.DriverName()
	}
	operation := h.logReplicationOperation.Snapshot()
	stats := model.StorageRuntimeStats{
		Mode: "hybrid", Primary: primary,
		CacheSync: h.cacheSyncOperation.Snapshot(), CacheMayBeStale: h.sqliteSyncFailCount.Load() > 0,
		LogReplication: model.ReplicationRuntimeStats{
			Enabled: true, Queued: len(h.syncCh), Capacity: cap(h.syncCh),
			Dropped: h.syncQueueDropCount.Load(), Failed: operation.Failed,
			LastDroppedAt: h.logReplicationLastDroppedAt.Load(), Operation: operation,
		},
	}
	h.startupRecoveryMu.RLock()
	if h.startupRecovery != nil {
		copyStats := *h.startupRecovery
		stats.StartupRecovery = &copyStats
	}
	h.startupRecoveryMu.RUnlock()
	return stats
}

// ============================================================================
// Store 接口实现
// ============================================================================

// === Channel Management ===

func (h *HybridStore) ListConfigs(ctx context.Context) ([]*model.Config, error) {
	// 渠道管理数据属于控制面，主库是唯一事实来源。使用 SQLite 缓存
	// 会在缓存同步失败时把旧的 enabled 状态重新显示给管理员。
	return h.mysql.ListConfigs(ctx)
}

func (h *HybridStore) GetConfig(ctx context.Context, id int64) (*model.Config, error) {
	return h.mysql.GetConfig(ctx, id)
}

func (h *HybridStore) CreateConfig(ctx context.Context, c *model.Config) (*model.Config, error) {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	result, err := h.mysql.CreateConfig(ctx, c)
	if err != nil {
		return nil, err
	}

	h.syncToSQLite("CreateConfig", func() error {
		_, err := h.sqlite.CreateConfig(ctx, result)
		return err
	})

	return result, nil
}

func (h *HybridStore) UpdateConfig(ctx context.Context, id int64, upd *model.Config) (*model.Config, error) {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	result, err := h.mysql.UpdateConfig(ctx, id, upd)
	if err != nil {
		return nil, err
	}

	h.syncToSQLite("UpdateConfig", func() error {
		_, err := h.sqlite.UpdateConfig(ctx, id, result)
		return err
	})

	return result, nil
}

func (h *HybridStore) UpdateOAuthCredential(ctx context.Context, id int64, credential string) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.UpdateOAuthCredential(ctx, id, credential); err != nil {
		return err
	}
	h.syncToSQLite("UpdateOAuthCredential", func() error {
		return h.sqlite.UpdateOAuthCredential(ctx, id, credential)
	})
	return nil
}

func (h *HybridStore) UpdateChannelEnabled(ctx context.Context, id int64, enabled bool) (*model.Config, error) {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	result, err := h.mysql.UpdateChannelEnabled(ctx, id, enabled)
	if err != nil {
		return nil, err
	}

	h.syncToSQLite("UpdateChannelEnabled", func() error {
		_, err := h.sqlite.UpdateChannelEnabled(ctx, id, enabled)
		return err
	})

	return result, nil
}

// CascadeSiteSuspend applies the cascade on the primary, then mirrors it.
// Counts come from the primary; the replica is best-effort like every other
// write here.
func (h *HybridStore) CascadeSiteSuspend(ctx context.Context, siteID int64, enable bool) (int, int, error) {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	accounts, channels, err := h.mysql.CascadeSiteSuspend(ctx, siteID, enable)
	if err != nil {
		return 0, 0, err
	}

	h.syncToSQLite("CascadeSiteSuspend", func() error {
		_, _, err := h.sqlite.CascadeSiteSuspend(ctx, siteID, enable)
		return err
	})

	return accounts, channels, nil
}

func (h *HybridStore) ClearSiteAccountSuspendMark(ctx context.Context, accountID int64) error {
	if err := h.mysql.ClearSiteAccountSuspendMark(ctx, accountID); err != nil {
		return err
	}

	h.syncToSQLite("ClearSiteAccountSuspendMark", func() error {
		return h.sqlite.ClearSiteAccountSuspendMark(ctx, accountID)
	})

	return nil
}

func (h *HybridStore) BatchPatchConfigs(ctx context.Context, channelIDs []int64, patch model.BatchConfigPatch) (model.BatchConfigPatchResult, error) {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	result, err := h.mysql.BatchPatchConfigs(ctx, channelIDs, patch)
	if err != nil {
		return model.BatchConfigPatchResult{}, err
	}

	h.syncToSQLite("BatchPatchConfigs", func() error {
		_, err := h.sqlite.BatchPatchConfigs(ctx, channelIDs, patch)
		return err
	})

	return result, nil
}

func (h *HybridStore) DeleteConfig(ctx context.Context, id int64) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.DeleteConfig(ctx, id); err != nil {
		return err
	}

	h.syncToSQLite("DeleteConfig", func() error {
		return h.sqlite.DeleteConfig(ctx, id)
	})

	return nil
}

func (h *HybridStore) GetEnabledChannelsByModel(ctx context.Context, modelName string) ([]*model.Config, error) {
	return h.mysql.GetEnabledChannelsByModel(ctx, modelName)
}

func (h *HybridStore) BatchUpdatePriority(ctx context.Context, updates []struct {
	ID       int64
	Priority int
}) (int64, error) {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	affected, err := h.mysql.BatchUpdatePriority(ctx, updates)
	if err != nil {
		return 0, err
	}

	h.syncToSQLite("BatchUpdatePriority", func() error {
		_, err := h.sqlite.BatchUpdatePriority(ctx, updates)
		return err
	})

	return affected, nil
}

// === Channel URL Runtime State ===

func (h *HybridStore) LoadDisabledURLs(ctx context.Context) (map[int64][]string, error) {
	return h.sqlite.LoadDisabledURLs(ctx)
}

func (h *HybridStore) SetURLDisabled(ctx context.Context, channelID int64, url string, disabled bool) error {
	if err := h.mysql.SetURLDisabled(ctx, channelID, url, disabled); err != nil {
		return err
	}
	h.syncToSQLite("SetURLDisabled", func() error {
		return h.sqlite.SetURLDisabled(ctx, channelID, url, disabled)
	})
	return nil
}

func (h *HybridStore) SetAPIKeyDisabled(ctx context.Context, channelID int64, keyIndex int, disabled bool) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.SetAPIKeyDisabled(ctx, channelID, keyIndex, disabled); err != nil {
		return err
	}
	h.syncToSQLite("SetAPIKeyDisabled", func() error {
		return h.sqlite.SetAPIKeyDisabled(ctx, channelID, keyIndex, disabled)
	})
	return nil
}

func (h *HybridStore) CleanupOrphanedURLStates(ctx context.Context, channelID int64, keepURLs []string) error {
	// 先清理MySQL（主存储）
	if err := h.mysql.CleanupOrphanedURLStates(ctx, channelID, keepURLs); err != nil {
		return err
	}

	// 同步清理SQLite缓存（失败仅警告）
	h.syncToSQLite("CleanupOrphanedURLStates", func() error {
		return h.sqlite.CleanupOrphanedURLStates(ctx, channelID, keepURLs)
	})

	return nil
}

// === API Key Management ===

func (h *HybridStore) GetAPIKeys(ctx context.Context, channelID int64) ([]*model.APIKey, error) {
	return h.mysql.GetAPIKeys(ctx, channelID)
}

func (h *HybridStore) GetAPIKey(ctx context.Context, channelID int64, keyIndex int) (*model.APIKey, error) {
	return h.mysql.GetAPIKey(ctx, channelID, keyIndex)
}

func (h *HybridStore) GetAllAPIKeys(ctx context.Context) (map[int64][]*model.APIKey, error) {
	return h.mysql.GetAllAPIKeys(ctx)
}

func (h *HybridStore) CreateAPIKeysBatch(ctx context.Context, keys []*model.APIKey) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.CreateAPIKeysBatch(ctx, keys); err != nil {
		return err
	}

	h.syncToSQLite("CreateAPIKeysBatch", func() error {
		return h.sqlite.CreateAPIKeysBatch(ctx, keys)
	})

	return nil
}

func (h *HybridStore) UpdateAPIKeysStrategy(ctx context.Context, channelID int64, strategy string) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.UpdateAPIKeysStrategy(ctx, channelID, strategy); err != nil {
		return err
	}

	h.syncToSQLite("UpdateAPIKeysStrategy", func() error {
		return h.sqlite.UpdateAPIKeysStrategy(ctx, channelID, strategy)
	})

	return nil
}

func (h *HybridStore) UpdateAPIKeyNotes(ctx context.Context, channelID int64, notesByIndex map[int]string) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.UpdateAPIKeyNotes(ctx, channelID, notesByIndex); err != nil {
		return err
	}

	h.syncToSQLite("UpdateAPIKeyNotes", func() error {
		return h.sqlite.UpdateAPIKeyNotes(ctx, channelID, notesByIndex)
	})

	return nil
}

func (h *HybridStore) UpdateAPIKeyMetadata(ctx context.Context, channelID int64, metadataByIndex map[int]model.APIKey) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.UpdateAPIKeyMetadata(ctx, channelID, metadataByIndex); err != nil {
		return err
	}

	h.syncToSQLite("UpdateAPIKeyMetadata", func() error {
		return h.sqlite.UpdateAPIKeyMetadata(ctx, channelID, metadataByIndex)
	})

	return nil
}

func (h *HybridStore) DeleteAPIKey(ctx context.Context, channelID int64, keyIndex int) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.DeleteAPIKey(ctx, channelID, keyIndex); err != nil {
		return err
	}

	h.syncToSQLite("DeleteAPIKey", func() error {
		return h.sqlite.DeleteAPIKey(ctx, channelID, keyIndex)
	})

	return nil
}

func (h *HybridStore) CompactKeyIndices(ctx context.Context, channelID int64, removedIndex int) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.CompactKeyIndices(ctx, channelID, removedIndex); err != nil {
		return err
	}

	h.syncToSQLite("CompactKeyIndices", func() error {
		return h.sqlite.CompactKeyIndices(ctx, channelID, removedIndex)
	})

	return nil
}

func (h *HybridStore) DeleteAllAPIKeys(ctx context.Context, channelID int64) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.DeleteAllAPIKeys(ctx, channelID); err != nil {
		return err
	}

	h.syncToSQLite("DeleteAllAPIKeys", func() error {
		return h.sqlite.DeleteAllAPIKeys(ctx, channelID)
	})

	return nil
}

// === Cooldown Management ===

func (h *HybridStore) ConfigureCooldown(settings util.CooldownSettings) {
	h.mysql.ConfigureCooldown(settings)
	h.sqlite.ConfigureCooldown(settings)
}

func (h *HybridStore) GetAllChannelCooldowns(ctx context.Context) (map[int64]time.Time, error) {
	return h.sqlite.GetAllChannelCooldowns(ctx)
}

func (h *HybridStore) BumpChannelCooldown(ctx context.Context, channelID int64, now time.Time, statusCode int) (time.Duration, error) {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	duration, err := h.mysql.BumpChannelCooldown(ctx, channelID, now, statusCode)
	if err != nil {
		return 0, err
	}

	h.syncToSQLite("BumpChannelCooldown", func() error {
		_, err := h.sqlite.BumpChannelCooldown(ctx, channelID, now, statusCode)
		return err
	})

	return duration, nil
}

func (h *HybridStore) ResetChannelCooldown(ctx context.Context, channelID int64) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.ResetChannelCooldown(ctx, channelID); err != nil {
		return err
	}

	h.syncToSQLite("ResetChannelCooldown", func() error {
		return h.sqlite.ResetChannelCooldown(ctx, channelID)
	})

	return nil
}

func (h *HybridStore) ResetAllCooldowns(ctx context.Context, channelID int64) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.ResetAllCooldowns(ctx, channelID); err != nil {
		return err
	}

	h.syncToSQLite("ResetAllCooldowns", func() error {
		return h.sqlite.ResetAllCooldowns(ctx, channelID)
	})

	return nil
}

func (h *HybridStore) SetChannelCooldown(ctx context.Context, channelID int64, until time.Time) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.SetChannelCooldown(ctx, channelID, until); err != nil {
		return err
	}

	h.syncToSQLite("SetChannelCooldown", func() error {
		return h.sqlite.SetChannelCooldown(ctx, channelID, until)
	})

	return nil
}

func (h *HybridStore) GetAllKeyCooldowns(ctx context.Context) (map[int64]map[int]time.Time, error) {
	return h.sqlite.GetAllKeyCooldowns(ctx)
}

func (h *HybridStore) BumpKeyCooldown(ctx context.Context, channelID int64, keyIndex int, now time.Time, statusCode int) (time.Duration, error) {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	duration, err := h.mysql.BumpKeyCooldown(ctx, channelID, keyIndex, now, statusCode)
	if err != nil {
		return 0, err
	}

	h.syncToSQLite("BumpKeyCooldown", func() error {
		_, err := h.sqlite.BumpKeyCooldown(ctx, channelID, keyIndex, now, statusCode)
		return err
	})

	return duration, nil
}

func (h *HybridStore) ResetKeyCooldown(ctx context.Context, channelID int64, keyIndex int) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.ResetKeyCooldown(ctx, channelID, keyIndex); err != nil {
		return err
	}

	h.syncToSQLite("ResetKeyCooldown", func() error {
		return h.sqlite.ResetKeyCooldown(ctx, channelID, keyIndex)
	})

	return nil
}

func (h *HybridStore) SetKeyCooldown(ctx context.Context, channelID int64, keyIndex int, until time.Time) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.SetKeyCooldown(ctx, channelID, keyIndex, until); err != nil {
		return err
	}

	h.syncToSQLite("SetKeyCooldown", func() error {
		return h.sqlite.SetKeyCooldown(ctx, channelID, keyIndex, until)
	})

	return nil
}

func (h *HybridStore) GetAllModelCooldowns(ctx context.Context) (map[int64]map[string]time.Time, error) {
	return h.sqlite.GetAllModelCooldowns(ctx)
}

func (h *HybridStore) BumpModelCooldown(
	ctx context.Context,
	channelID int64,
	model string,
	now time.Time,
	statusCode int,
) (time.Duration, error) {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	duration, err := h.mysql.BumpModelCooldown(ctx, channelID, model, now, statusCode)
	if err != nil {
		return 0, err
	}

	h.syncToSQLite("BumpModelCooldown", func() error {
		_, err := h.sqlite.BumpModelCooldown(ctx, channelID, model, now, statusCode)
		return err
	})

	return duration, nil
}

func (h *HybridStore) SetModelCooldown(ctx context.Context, channelID int64, model string, until time.Time) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.SetModelCooldown(ctx, channelID, model, until); err != nil {
		return err
	}

	h.syncToSQLite("SetModelCooldown", func() error {
		return h.sqlite.SetModelCooldown(ctx, channelID, model, until)
	})

	return nil
}

func (h *HybridStore) ResetModelCooldown(ctx context.Context, channelID int64, model string) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.ResetModelCooldown(ctx, channelID, model); err != nil {
		return err
	}

	h.syncToSQLite("ResetModelCooldown", func() error {
		return h.sqlite.ResetModelCooldown(ctx, channelID, model)
	})

	return nil
}

// === Log Management ===
// 日志特殊处理：写 SQLite（快）+ 异步同步到 MySQL（备份）

func (h *HybridStore) AddLog(ctx context.Context, e *model.LogEntry) error {
	if err := h.sqlite.AddLog(ctx, e); err != nil {
		return err
	}

	// 启用 Debug 日志的条目只保留在 SQLite，不同步到 MySQL（避免上游原始请求/响应体膨胀 MySQL）
	// clone 时已剔除 DebugData，logs 主数据仍需同步到 MySQL

	h.enqueueLogSync(&syncTask{
		operation: "log",
		data:      &syncTaskLog{entry: cloneLogEntryForSync(e)},
	})

	return nil
}

func (h *HybridStore) BatchAddLogs(ctx context.Context, logs []*model.LogEntry) error {
	if err := h.sqlite.BatchAddLogs(ctx, logs); err != nil {
		return err
	}

	syncable := make([]*model.LogEntry, 0, len(logs))
	for _, entry := range logs {
		if entry == nil {
			continue
		}
		syncable = append(syncable, entry)
	}

	if len(syncable) > 0 {
		h.enqueueLogSync(&syncTask{
			operation: "log_batch",
			data:      &syncTaskLogBatch{entries: cloneLogEntriesForSync(syncable)},
		})
	}

	return nil
}

func (h *HybridStore) ListLogs(ctx context.Context, since time.Time, limit, offset int, filter *model.LogFilter) ([]*model.LogEntry, error) {
	return h.sqlite.ListLogs(ctx, since, limit, offset, filter)
}

func (h *HybridStore) ListLogsRange(ctx context.Context, since, until time.Time, limit, offset int, filter *model.LogFilter) ([]*model.LogEntry, error) {
	return h.sqlite.ListLogsRange(ctx, since, until, limit, offset, filter)
}

func (h *HybridStore) ListLogsRangeWithCount(ctx context.Context, since, until time.Time, limit, offset int, filter *model.LogFilter) ([]*model.LogEntry, int, error) {
	return h.sqlite.ListLogsRangeWithCount(ctx, since, until, limit, offset, filter)
}

func (h *HybridStore) CountLogs(ctx context.Context, since time.Time, filter *model.LogFilter) (int, error) {
	return h.sqlite.CountLogs(ctx, since, filter)
}

func (h *HybridStore) CountLogsRange(ctx context.Context, since, until time.Time, filter *model.LogFilter) (int, error) {
	return h.sqlite.CountLogsRange(ctx, since, until, filter)
}

func (h *HybridStore) GetCostSummary(ctx context.Context, since, until time.Time, filter *model.LogFilter) (*model.CostSummary, error) {
	return h.sqlite.GetCostSummary(ctx, since, until, filter)
}

func (h *HybridStore) GetTodayChannelURLStats(ctx context.Context, dayStart time.Time) ([]model.ChannelURLLogStat, error) {
	return h.sqlite.GetTodayChannelURLStats(ctx, dayStart)
}

func (h *HybridStore) CleanupLogsBefore(ctx context.Context, cutoff time.Time) error {
	localErr := h.sqlite.CleanupLogsBefore(ctx, cutoff)
	remoteErr := h.mysql.CleanupLogsBefore(ctx, cutoff)
	if localErr != nil {
		localErr = fmt.Errorf("cleanup local request history and logs: %w", localErr)
	}
	if remoteErr != nil {
		remoteErr = fmt.Errorf("cleanup primary request history and logs: %w", remoteErr)
	}
	return errors.Join(localErr, remoteErr)
}

// === Request History ===
// Request history uses the same local-first write and local-read policy as
// logs. The primary copy is a best-effort asynchronous replica.

func (h *HybridStore) BatchAddRequestTraces(ctx context.Context, traces []*model.RequestTrace) error {
	syncable := make([]*model.RequestTrace, 0, len(traces))
	for _, trace := range traces {
		if trace == nil {
			continue
		}
		clone, err := cloneRequestTraceForSync(trace)
		if err != nil {
			return err
		}
		syncable = append(syncable, clone)
	}
	if err := h.sqlite.BatchAddRequestTraces(ctx, syncable); err != nil {
		return err
	}
	if len(syncable) > 0 {
		h.enqueueRequestTraceSync(&syncTask{
			operation: "request_trace_batch",
			data:      &syncTaskRequestTraceBatch{traces: syncable},
		})
	}
	return nil
}

func (h *HybridStore) GetRequestTrace(ctx context.Context, id string, authTokenID *int64) (*model.RequestTrace, error) {
	return h.sqlite.GetRequestTrace(ctx, id, authTokenID)
}

func (h *HybridStore) ListRequestTraces(
	ctx context.Context,
	since, until time.Time,
	limit, offset int,
	filter *model.RequestTraceFilter,
) ([]*model.RequestTrace, int, error) {
	return h.sqlite.ListRequestTraces(ctx, since, until, limit, offset, filter)
}

func (h *HybridStore) GetRequestTraceStats(
	ctx context.Context,
	since, until time.Time,
	filter *model.RequestTraceFilter,
) (*model.RequestTraceStats, error) {
	return h.sqlite.GetRequestTraceStats(ctx, since, until, filter)
}

// === Metrics & Statistics ===

func (h *HybridStore) AggregateRangeWithFilter(ctx context.Context, since, until time.Time, bucket time.Duration, filter *model.LogFilter) ([]model.MetricPoint, error) {
	return h.sqlite.AggregateRangeWithFilter(ctx, since, until, bucket, filter)
}

func (h *HybridStore) GetDistinctModels(ctx context.Context, since, until time.Time, filter *model.LogFilter) ([]string, error) {
	return h.sqlite.GetDistinctModels(ctx, since, until, filter)
}

func (h *HybridStore) GetDistinctStatusCodes(ctx context.Context, since, until time.Time, filter *model.LogFilter) ([]int, error) {
	return h.sqlite.GetDistinctStatusCodes(ctx, since, until, filter)
}

func (h *HybridStore) GetDistinctChannels(ctx context.Context, since, until time.Time, filter *model.LogFilter) ([]model.ChannelNameID, error) {
	return h.sqlite.GetDistinctChannels(ctx, since, until, filter)
}

func (h *HybridStore) GetStats(ctx context.Context, startTime, endTime time.Time, filter *model.LogFilter, isToday bool) ([]model.StatsEntry, error) {
	return h.sqlite.GetStats(ctx, startTime, endTime, filter, isToday)
}

func (h *HybridStore) GetStatsLite(ctx context.Context, startTime, endTime time.Time, filter *model.LogFilter) ([]model.StatsEntry, error) {
	return h.sqlite.GetStatsLite(ctx, startTime, endTime, filter)
}

func (h *HybridStore) GetClientProtocolStats(ctx context.Context, startTime, endTime time.Time, filter *model.LogFilter) ([]model.ClientProtocolStats, error) {
	return h.sqlite.GetClientProtocolStats(ctx, startTime, endTime, filter)
}

func (h *HybridStore) GetRPMStats(ctx context.Context, startTime, endTime time.Time, filter *model.LogFilter, isToday bool) (*model.RPMStats, error) {
	return h.sqlite.GetRPMStats(ctx, startTime, endTime, filter, isToday)
}

func (h *HybridStore) GetChannelSuccessRates(ctx context.Context, since time.Time) (map[int64]model.ChannelHealthStats, error) {
	return h.sqlite.GetChannelSuccessRates(ctx, since)
}

func (h *HybridStore) GetHealthTimeline(ctx context.Context, params model.HealthTimelineParams) ([]model.HealthTimelineRow, error) {
	return h.sqlite.GetHealthTimeline(ctx, params)
}

func (h *HybridStore) GetTodayChannelCosts(ctx context.Context, todayStart time.Time) (map[int64]float64, error) {
	return h.sqlite.GetTodayChannelCosts(ctx, todayStart)
}

// === Auth Token Management ===

func (h *HybridStore) CreateAuthToken(ctx context.Context, token *model.AuthToken) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()
	h.tokenAccountingMu.Lock()
	defer h.tokenAccountingMu.Unlock()

	if err := h.mysql.CreateAuthToken(ctx, token); err != nil {
		return err
	}

	h.syncToSQLite("CreateAuthToken", func() error {
		return h.sqlite.CreateAuthToken(ctx, token)
	})

	return nil
}

// EnsureAuthToken creates a missing auth token in the primary store and mirrors it to SQLite.
func (h *HybridStore) EnsureAuthToken(ctx context.Context, token *model.AuthToken) (bool, error) {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()
	h.tokenAccountingMu.Lock()
	defer h.tokenAccountingMu.Unlock()

	created, err := h.mysql.EnsureAuthToken(ctx, token)
	if err != nil {
		return false, err
	}

	h.syncToSQLite("EnsureAuthToken", func() error {
		return h.sqlite.UpsertAuthTokenAllFields(ctx, token)
	})

	return created, nil
}

func (h *HybridStore) GetAuthToken(ctx context.Context, id int64) (*model.AuthToken, error) {
	return h.sqlite.GetAuthToken(ctx, id)
}

func (h *HybridStore) GetAuthTokenByValue(ctx context.Context, tokenHash string) (*model.AuthToken, error) {
	return h.sqlite.GetAuthTokenByValue(ctx, tokenHash)
}

func (h *HybridStore) ListAuthTokens(ctx context.Context) ([]*model.AuthToken, error) {
	return h.sqlite.ListAuthTokens(ctx)
}

func (h *HybridStore) ListActiveAuthTokens(ctx context.Context) ([]*model.AuthToken, error) {
	return h.sqlite.ListActiveAuthTokens(ctx)
}

func (h *HybridStore) UpdateAuthToken(ctx context.Context, token *model.AuthToken) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()
	h.tokenAccountingMu.Lock()
	defer h.tokenAccountingMu.Unlock()

	if err := h.mysql.UpdateAuthToken(ctx, token); err != nil {
		return err
	}

	h.syncToSQLite("UpdateAuthToken", func() error {
		return h.sqlite.UpdateAuthToken(ctx, token)
	})

	return nil
}

func (h *HybridStore) DeleteAuthToken(ctx context.Context, id int64) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()
	h.tokenAccountingMu.Lock()
	defer h.tokenAccountingMu.Unlock()

	if err := h.mysql.DeleteAuthToken(ctx, id); err != nil {
		return err
	}

	h.syncToSQLite("DeleteAuthToken", func() error {
		return h.sqlite.DeleteAuthToken(ctx, id)
	})

	return nil
}

func (h *HybridStore) UpdateTokenLastUsed(ctx context.Context, tokenHash string, now time.Time) error {
	h.tokenAccountingMu.Lock()
	defer h.tokenAccountingMu.Unlock()

	if err := h.mysql.UpdateTokenLastUsed(ctx, tokenHash, now); err != nil {
		return err
	}

	h.syncToSQLite("UpdateTokenLastUsed", func() error {
		return h.sqlite.UpdateTokenLastUsed(ctx, tokenHash, now)
	})

	return nil
}

func (h *HybridStore) UpdateTokenStats(ctx context.Context, tokenHash string, isSuccess bool, duration float64, isStreaming bool, firstByteTime float64, promptTokens int64, completionTokens int64, cacheReadTokens int64, cacheCreationTokens int64, costUSD float64, effectiveCostUSD float64) error {
	h.tokenAccountingMu.Lock()
	defer h.tokenAccountingMu.Unlock()

	if err := h.mysql.UpdateTokenStats(ctx, tokenHash, isSuccess, duration, isStreaming, firstByteTime, promptTokens, completionTokens, cacheReadTokens, cacheCreationTokens, costUSD, effectiveCostUSD); err != nil {
		return err
	}

	h.syncToSQLite("UpdateTokenStats", func() error {
		return h.sqlite.UpdateTokenStats(ctx, tokenHash, isSuccess, duration, isStreaming, firstByteTime, promptTokens, completionTokens, cacheReadTokens, cacheCreationTokens, costUSD, effectiveCostUSD)
	})

	return nil
}

func (h *HybridStore) ApplyTokenSettlement(ctx context.Context, event model.TokenSettlement) error {
	h.tokenAccountingMu.Lock()
	defer h.tokenAccountingMu.Unlock()

	if err := h.mysql.ApplyTokenSettlement(ctx, event); err != nil {
		return err
	}
	canonical, err := h.mysql.GetAuthTokenByValue(ctx, event.TokenHash)
	if err != nil {
		return fmt.Errorf("load primary token accounting after settlement: %w", err)
	}
	if err := h.syncSQLite("ApplyTokenSettlement", func() error {
		return h.sqlite.SyncTokenAccounting(ctx, canonical)
	}); err != nil {
		return fmt.Errorf("sync token settlement to sqlite replica: %w", err)
	}
	return nil
}

func (h *HybridStore) GetAuthTokenStatsInRange(ctx context.Context, startTime, endTime time.Time) (map[int64]*model.AuthTokenRangeStats, error) {
	return h.sqlite.GetAuthTokenStatsInRange(ctx, startTime, endTime)
}

func (h *HybridStore) FillAuthTokenRPMStats(ctx context.Context, stats map[int64]*model.AuthTokenRangeStats, startTime, endTime time.Time, isToday bool) error {
	return h.sqlite.FillAuthTokenRPMStats(ctx, stats, startTime, endTime, isToday)
}

// === System Access Token Management ===

func (h *HybridStore) CreateSystemAccessToken(ctx context.Context, token *model.SystemAccessToken) error {
	if err := h.mysql.CreateSystemAccessToken(ctx, token); err != nil {
		return err
	}
	h.syncToSQLite("CreateSystemAccessToken", func() error { return h.sqlite.CreateSystemAccessToken(ctx, token) })
	return nil
}

func (h *HybridStore) GetSystemAccessTokenByHash(ctx context.Context, tokenHash string) (*model.SystemAccessToken, error) {
	return h.sqlite.GetSystemAccessTokenByHash(ctx, tokenHash)
}

func (h *HybridStore) ListSystemAccessTokens(ctx context.Context) ([]*model.SystemAccessToken, error) {
	return h.sqlite.ListSystemAccessTokens(ctx)
}

func (h *HybridStore) UpdateSystemAccessToken(ctx context.Context, token *model.SystemAccessToken) error {
	if err := h.mysql.UpdateSystemAccessToken(ctx, token); err != nil {
		return err
	}
	h.syncToSQLite("UpdateSystemAccessToken", func() error { return h.sqlite.UpdateSystemAccessToken(ctx, token) })
	return nil
}

func (h *HybridStore) DeleteSystemAccessToken(ctx context.Context, id int64) error {
	if err := h.mysql.DeleteSystemAccessToken(ctx, id); err != nil {
		return err
	}
	h.syncToSQLite("DeleteSystemAccessToken", func() error { return h.sqlite.DeleteSystemAccessToken(ctx, id) })
	return nil
}

func (h *HybridStore) UpdateSystemAccessTokenLastUsed(ctx context.Context, tokenHash string, now time.Time) error {
	if err := h.mysql.UpdateSystemAccessTokenLastUsed(ctx, tokenHash, now); err != nil {
		return err
	}
	h.syncToSQLite("UpdateSystemAccessTokenLastUsed", func() error { return h.sqlite.UpdateSystemAccessTokenLastUsed(ctx, tokenHash, now) })
	return nil
}

// === System Settings ===

func (h *HybridStore) GetSetting(ctx context.Context, key string) (*model.SystemSetting, error) {
	return h.sqlite.GetSetting(ctx, key)
}

func (h *HybridStore) ListAllSettings(ctx context.Context) ([]*model.SystemSetting, error) {
	return h.sqlite.ListAllSettings(ctx)
}

func (h *HybridStore) UpdateSetting(ctx context.Context, key, value string) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.UpdateSetting(ctx, key, value); err != nil {
		return err
	}

	h.syncToSQLite("UpdateSetting", func() error {
		return h.sqlite.UpdateSetting(ctx, key, value)
	})

	return nil
}

func (h *HybridStore) CompareAndSwapSetting(ctx context.Context, key, expectedValue, newValue string) (bool, error) {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	updated, err := h.mysql.CompareAndSwapSetting(ctx, key, expectedValue, newValue)
	if err != nil || !updated {
		return updated, err
	}
	if err := h.syncSQLite("CompareAndSwapSetting", func() error {
		return h.sqlite.UpdateSetting(ctx, key, newValue)
	}); err != nil {
		return true, err
	}
	return true, nil
}

func (h *HybridStore) BatchUpdateSettings(ctx context.Context, updates map[string]string) error {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	if err := h.mysql.BatchUpdateSettings(ctx, updates); err != nil {
		return err
	}

	h.syncToSQLite("BatchUpdateSettings", func() error {
		return h.sqlite.BatchUpdateSettings(ctx, updates)
	})

	return nil
}

func (h *HybridStore) CreateWebSession(ctx context.Context, token string, session model.WebSession) error {
	return h.sqlite.CreateWebSession(ctx, token, session)
}

func (h *HybridStore) GetWebSession(ctx context.Context, token string) (model.WebSession, bool, error) {
	return h.sqlite.GetWebSession(ctx, token)
}

func (h *HybridStore) DeleteWebSession(ctx context.Context, token string) error {
	return h.sqlite.DeleteWebSession(ctx, token)
}

func (h *HybridStore) DeleteWebSessionsByAuthTokenID(ctx context.Context, authTokenID int64) error {
	return h.sqlite.DeleteWebSessionsByAuthTokenID(ctx, authTokenID)
}

func (h *HybridStore) CleanExpiredWebSessions(ctx context.Context) error {
	return h.sqlite.CleanExpiredWebSessions(ctx)
}

func (h *HybridStore) LoadWebSessions(ctx context.Context) (map[string]model.WebSession, error) {
	return h.sqlite.LoadWebSessions(ctx)
}

// === Model Fingerprint Management ===

func (h *HybridStore) ListModelFingerprints(ctx context.Context) ([]*model.ModelFingerprint, error) {
	return h.sqlite.ListModelFingerprints(ctx)
}

func (h *HybridStore) GetModelFingerprint(ctx context.Context, id int64) (*model.ModelFingerprint, error) {
	return h.sqlite.GetModelFingerprint(ctx, id)
}

func (h *HybridStore) ModelFingerprintNameExists(ctx context.Context, name string) (bool, error) {
	return h.mysql.ModelFingerprintNameExists(ctx, name)
}

func (h *HybridStore) CreateModelFingerprint(ctx context.Context, fp *model.ModelFingerprint) (*model.ModelFingerprint, error) {
	result, err := h.mysql.CreateModelFingerprint(ctx, fp)
	if err != nil {
		return nil, err
	}

	h.syncToSQLite("CreateModelFingerprint", func() error {
		_, err := h.sqlite.CreateModelFingerprint(ctx, result)
		return err
	})

	return result, nil
}

func (h *HybridStore) DeleteModelFingerprint(ctx context.Context, id int64) error {
	if err := h.mysql.DeleteModelFingerprint(ctx, id); err != nil {
		return err
	}

	h.syncToSQLite("DeleteModelFingerprint", func() error {
		return h.sqlite.DeleteModelFingerprint(ctx, id)
	})

	return nil
}

func (h *HybridStore) ClearFingerprintChannelID(ctx context.Context, channelID int64) error {
	if err := h.mysql.ClearFingerprintChannelID(ctx, channelID); err != nil {
		return err
	}

	h.syncToSQLite("ClearFingerprintChannelID", func() error {
		return h.sqlite.ClearFingerprintChannelID(ctx, channelID)
	})

	return nil
}

// === Fingerprint Test Results ===

func (h *HybridStore) CreateFingerprintTestResult(ctx context.Context, rec *model.FingerprintTestRecord) error {
	if err := h.mysql.CreateFingerprintTestResult(ctx, rec); err != nil {
		return err
	}

	h.syncToSQLite("CreateFingerprintTestResult", func() error {
		return h.sqlite.CreateFingerprintTestResult(ctx, rec)
	})

	return nil
}

func (h *HybridStore) ListFingerprintTestResults(ctx context.Context, limit int) ([]*model.FingerprintTestRecord, error) {
	return h.sqlite.ListFingerprintTestResults(ctx, limit)
}

func (h *HybridStore) DeleteFingerprintTestResult(ctx context.Context, id int64) error {
	if err := h.mysql.DeleteFingerprintTestResult(ctx, id); err != nil {
		return err
	}

	h.syncToSQLite("DeleteFingerprintTestResult", func() error {
		return h.sqlite.DeleteFingerprintTestResult(ctx, id)
	})

	return nil
}

// === Batch Operations ===

func (h *HybridStore) ImportChannelBatch(ctx context.Context, channels []*model.ChannelWithKeys) (created, updated int, err error) {
	h.configurationMu.Lock()
	defer h.configurationMu.Unlock()

	created, updated, err = h.mysql.ImportChannelBatch(ctx, channels)
	if err != nil {
		return 0, 0, err
	}

	h.syncToSQLite("ImportChannelBatch", func() error {
		_, _, err := h.sqlite.ImportChannelBatch(ctx, channels)
		return err
	})

	return created, updated, nil
}

// === Lifecycle ===

func (h *HybridStore) Ping(ctx context.Context) error {
	return h.sqlite.Ping(ctx)
}

// SyncQueueLen 返回当前同步队列中待处理的任务数量（用于监控）
func (h *HybridStore) SyncQueueLen() int {
	return len(h.syncCh)
}

// === Debug Log Management (SQLite only, no MySQL sync) ===

func (h *HybridStore) AddDebugLog(ctx context.Context, e *model.DebugLogEntry) error {
	return h.sqlite.AddDebugLog(ctx, e)
}

func (h *HybridStore) GetDebugLogByLogID(ctx context.Context, logID int64) (*model.DebugLogEntry, error) {
	return h.sqlite.GetDebugLogByLogID(ctx, logID)
}

func (h *HybridStore) CleanupDebugLogsBatch(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	return h.sqlite.CleanupDebugLogsBatch(ctx, cutoff, limit)
}

func (h *HybridStore) TruncateDebugLogs(ctx context.Context) error {
	return h.sqlite.TruncateDebugLogs(ctx)
}

func (h *HybridStore) Close() error {
	var err error
	h.closeOnce.Do(func() {
		h.closeMu.Lock()
		h.closing = true
		close(h.stopCh)
		h.closeMu.Unlock()
		h.syncWg.Wait()

		if closeErr := h.sqlite.Close(); closeErr != nil {
			err = closeErr
		}
		if closeErr := h.mysql.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	})
	return err
}
