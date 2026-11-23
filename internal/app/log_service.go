package app

import (
	"context"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/config"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/monitoring"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

const (
	debugLogCleanupBatchSize  = 200
	debugLogCleanupBatchYield = 100 * time.Millisecond
)

// LogService 日志管理服务
//
// 职责：处理所有日志相关的业务逻辑
// - 异步日志记录（批量写入）
// - 日志 Worker 管理
// - 日志清理（定时任务）
// - 优雅关闭
//
// 遵循 SRP 原则：仅负责日志管理，不涉及代理、认证、管理 API
type LogService struct {
	store storage.Store

	// 日志队列和 Worker
	logChan            chan *model.LogEntry
	logWorkers         int
	logDropCount       atomic.Uint64
	logLastDroppedAt   atomic.Int64
	logBuffered        atomic.Int64
	logWritten         atomic.Uint64
	logWriteFailures   atomic.Uint64
	logOperation       monitoring.OperationMonitor
	traceChan          chan *model.RequestTrace
	traceDropCount     atomic.Uint64
	traceLastDroppedAt atomic.Int64
	traceBuffered      atomic.Int64
	traceWriteFailures atomic.Uint64
	traceWritten       atomic.Uint64
	traceOperation     monitoring.OperationMonitor

	logCleanupOperation   monitoring.OperationMonitor
	debugCleanupOperation monitoring.OperationMonitor
	logCleanupNextAt      atomic.Int64
	debugCleanupNextAt    atomic.Int64
	debugCleanupMode      atomic.Int32
	debugRetentionMinutes atomic.Int64
	debugCleanupDeleted   atomic.Uint64

	// 日志保留天数（启动时确定，修改后重启生效）
	retentionDays int

	// 优雅关闭
	shutdownCh     chan struct{}
	isShuttingDown *atomic.Bool
	wg             *sync.WaitGroup
}

// NewLogService 创建日志服务实例
func NewLogService(
	store storage.Store,
	logBufferSize int,
	logWorkers int,
	retentionDays int, // 启动时确定，修改后重启生效
	shutdownCh chan struct{},
	isShuttingDown *atomic.Bool,
	wg *sync.WaitGroup,
) *LogService {
	return &LogService{
		store:          store,
		logChan:        make(chan *model.LogEntry, logBufferSize),
		traceChan:      make(chan *model.RequestTrace, 256),
		logWorkers:     logWorkers,
		retentionDays:  retentionDays,
		shutdownCh:     shutdownCh,
		isShuttingDown: isShuttingDown,
		wg:             wg,
	}
}

// ============================================================================
// Worker 管理
// ============================================================================

// StartWorkers 启动日志 Worker
func (s *LogService) StartWorkers() {
	s.startRequestTraceWorker()
	for i := 0; i < s.logWorkers; i++ {
		s.wg.Add(1)
		go s.logWorker()
	}
}

// logWorker 日志 Worker（后台协程）
func (s *LogService) logWorker() {
	defer s.wg.Done()

	batch := make([]*model.LogEntry, 0, config.LogBatchSize)
	ticker := time.NewTicker(config.LogBatchTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-s.shutdownCh:
			// shutdown时尽量flush掉已排队的日志，避免“退出即丢日志”
			for {
				select {
				case entry, ok := <-s.logChan:
					if !ok {
						s.flushIfNeeded(batch)
						return
					}
					batch = append(batch, entry)
					s.logBuffered.Add(1)
					if len(batch) >= config.LogBatchSize {
						s.flushBufferedLogs(batch)
						batch = batch[:0]
					}
				default:
					s.flushIfNeeded(batch)
					return
				}
			}

		case entry, ok := <-s.logChan:
			if !ok {
				// logChan已关闭，flush剩余日志并退出
				s.flushIfNeeded(batch)
				return
			}

			batch = append(batch, entry)
			s.logBuffered.Add(1)
			if len(batch) >= config.LogBatchSize {
				s.flushBufferedLogs(batch)
				batch = batch[:0]
				ticker.Reset(config.LogBatchTimeout)
			}

		case <-ticker.C:
			// 移除嵌套select，简化定时flush逻辑
			// 设计原则：
			// - ticker触发时直接flush当前batch
			// - 如果logChan关闭，下次循环会在entry <- logChan中捕获
			// - shutdown信号在select中优先级最高，保证快速响应
			s.flushIfNeeded(batch)
			batch = batch[:0]
		}
	}
}

// flushLogs 批量写入日志
func (s *LogService) flushLogs(logs []*model.LogEntry) {
	if len(logs) == 0 {
		return
	}

	timeout := time.Duration(config.LogFlushTimeoutMs) * time.Millisecond
	maxRetries := config.LogFlushMaxRetries
	if s.isShutdownInProgress() {
		// 关停阶段不做重试，避免单批刷盘耗时放大拖垮优雅关闭预算。
		maxRetries = 1
	}

	var lastErr error
	attempts := 0
retryLoop:
	for attempt := 1; attempt <= maxRetries; attempt++ {
		attempts = attempt
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		finish := s.logOperation.Begin()
		err := s.store.BatchAddLogs(ctx, logs)
		cancel()
		finish(err)
		if err == nil {
			s.logWritten.Add(uint64(len(logs)))
			if attempt > 1 {
				log.Printf("[WARN] 日志批量写入重试成功 (attempt=%d/%d, batch_size=%d)", attempt, maxRetries, len(logs))
			}
			return
		}

		lastErr = err
		if attempt < maxRetries {
			// 运行中可能刚进入关停流程，此时停止重试，避免拖慢 drain。
			if s.isShutdownInProgress() {
				break
			}

			log.Printf("[WARN] 日志批量写入失败，准备重试 (attempt=%d/%d, batch_size=%d): %v", attempt, maxRetries, len(logs), err)
			backoff := time.Duration(attempt) * config.LogFlushRetryBackoff
			timer := time.NewTimer(backoff)
			select {
			case <-timer.C:
			case <-s.shutdownCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				break retryLoop
			}
		}
	}

	s.logWriteFailures.Add(uint64(len(logs)))
	log.Printf("[ERROR] 日志批量写入最终失败 (attempts=%d, batch_size=%d): %v", attempts, len(logs), lastErr)
}

func (s *LogService) isShutdownInProgress() bool {
	return s.isShuttingDown != nil && s.isShuttingDown.Load()
}

// flushIfNeeded 辅助函数：当batch非空时执行flush
func (s *LogService) flushIfNeeded(batch []*model.LogEntry) {
	if len(batch) > 0 {
		s.flushBufferedLogs(batch)
	}
}

func (s *LogService) flushBufferedLogs(batch []*model.LogEntry) {
	s.flushLogs(batch)
	s.logBuffered.Add(-int64(len(batch)))
}

// ============================================================================
// 日志记录方法
// ============================================================================

// AddLogAsync 异步添加日志
func (s *LogService) AddLogAsync(entry *model.LogEntry) {
	if entry == nil {
		return
	}
	// shutdown时不再写入日志
	if s.isShutdownInProgress() {
		return
	}

	select {
	case s.logChan <- entry:
		// 成功放入队列
	default:
		// 队列满，丢弃日志（计数用于监控）
		count := s.logDropCount.Add(1)
		s.logLastDroppedAt.Store(time.Now().UnixMilli())
		// [FIX] 降低采样频率，每10次丢弃打印一次（原来是100次）
		// 设计原则：及早暴露问题，避免用户在黑暗中调试
		if count%10 == 1 {
			log.Printf("[ERROR] 日志队列已满，日志被丢弃 (累计丢弃: %d) - 考虑增大 LOG_BUFFER_SIZE 或 LOG_WORKERS", count)
		}
	}
}

// ============================================================================
// 日志清理
// ============================================================================

// StartCleanupLoop 分别启动普通日志和调试日志清理后台协程。
// 普通日志每小时检查一次，调试日志启动后立即检查并按独立周期运行。
// 支持优雅关闭
func (s *LogService) StartCleanupLoop() {
	s.wg.Add(2)
	go s.cleanupOldLogsLoop()
	go s.cleanupDebugLogsLoop()
}

// cleanupOldLogsLoop 日志清理后台协程（私有方法）
func (s *LogService) cleanupOldLogsLoop() {
	defer s.wg.Done()
	ctx, cancel := s.cleanupLoopContext()
	defer cancel()

	logTicker := time.NewTicker(config.LogCleanupInterval)
	defer logTicker.Stop()
	if s.retentionDays > 0 {
		s.logCleanupNextAt.Store(time.Now().Add(config.LogCleanupInterval).UnixMilli())
	}
	defer s.logCleanupNextAt.Store(0)

	for {
		select {
		case scheduledAt := <-logTicker.C:
			if s.retentionDays > 0 {
				s.logCleanupNextAt.Store(scheduledAt.Add(config.LogCleanupInterval).UnixMilli())
				runCtx, runCancel := context.WithTimeout(ctx, 30*time.Second)
				_ = s.cleanupOldLogs(runCtx) // The operation records and logs its result.
				runCancel()
			}

		case <-ctx.Done():
			return
		}
	}
}

// cleanupOldLogs records the actual retention operation, including failures.
func (s *LogService) cleanupOldLogs(ctx context.Context) error {
	if s.retentionDays <= 0 {
		return nil
	}
	finish := s.logCleanupOperation.Begin()
	err := s.store.CleanupLogsBefore(ctx, time.Now().AddDate(0, 0, -s.retentionDays))
	finish(err)
	if err != nil && !monitoring.IsCancellation(err) {
		log.Printf("[WARN] 日志及请求历史清理失败，下个周期重试: %v", err)
	}
	return err
}

func (s *LogService) cleanupLoopContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-s.shutdownCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

// cleanupDebugLogsLoop 使用独立协程清理调试日志，避免阻塞普通日志维护任务。
func (s *LogService) cleanupDebugLogsLoop() {
	defer s.wg.Done()

	ctx, cancel := s.cleanupLoopContext()
	defer cancel()
	defer s.debugCleanupNextAt.Store(0)

	debugCleanupDone := s.cleanupDebugLogs(ctx)
	for !debugCleanupDone && ctx.Err() == nil {
		s.debugCleanupNextAt.Store(time.Now().Add(config.DebugLogCleanupInterval).UnixMilli())
		timer := time.NewTimer(config.DebugLogCleanupInterval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}

		debugCleanupDone = s.cleanupDebugLogs(ctx)
	}
}

// cleanupDebugLogs 执行一轮调试日志清理。返回 true 表示 Debug 已关闭且表已清空。
func (s *LogService) cleanupDebugLogs(ctx context.Context) bool {
	finish := s.debugCleanupOperation.Begin()
	var cleanupErr error
	defer func() { finish(cleanupErr) }()
	setting, err := s.store.GetSetting(ctx, "debug_log_enabled")
	if err != nil {
		cleanupErr = err
		if !monitoring.IsCancellation(err) {
			log.Printf("[WARN] 读取 Debug 日志开关失败: %v", err)
		}
		return false
	}
	if setting == nil || setting.Value != "true" {
		s.debugCleanupMode.Store(debugCleanupModeClearing)
		if err := s.store.TruncateDebugLogs(ctx); err != nil {
			cleanupErr = err
			if !monitoring.IsCancellation(err) {
				log.Printf("[WARN] 清空调试日志失败: %v", err)
			}
			return false
		}
		s.debugCleanupMode.Store(debugCleanupModeDisabled)
		log.Printf("[INFO] 调试日志未启用，已清空历史调试日志")
		return true
	}
	s.debugCleanupMode.Store(debugCleanupModeRetention)

	retentionMinutes := 5
	if setting, err := s.store.GetSetting(ctx, "debug_log_retention_minutes"); err != nil {
		cleanupErr = err
		if !monitoring.IsCancellation(err) {
			log.Printf("[WARN] 读取 Debug 日志保留时间失败: %v", err)
		}
		return false
	} else if setting != nil {
		if value, err := strconv.Atoi(setting.Value); err == nil && value > 0 {
			retentionMinutes = value
		}
	}
	s.debugRetentionMinutes.Store(int64(retentionMinutes))
	cutoff := time.Now().Add(-time.Duration(retentionMinutes) * time.Minute)

	for {
		deleted, err := s.store.CleanupDebugLogsBatch(ctx, cutoff, debugLogCleanupBatchSize)
		if err != nil {
			cleanupErr = err
			if !monitoring.IsCancellation(err) {
				log.Printf("[WARN] 清理调试日志失败，本轮停止 (batch_size=%d): %v", debugLogCleanupBatchSize, err)
			}
			return false
		}
		if deleted > 0 {
			s.debugCleanupDeleted.Add(uint64(deleted))
		}
		if deleted < int64(debugLogCleanupBatchSize) {
			return false
		}

		select {
		case <-time.After(debugLogCleanupBatchYield):
		case <-ctx.Done():
			cleanupErr = ctx.Err()
			return false
		}
	}
}
