package app

import (
	"github.com/yzgolden86/PivotFlow/internal/config"
	"github.com/yzgolden86/PivotFlow/internal/monitoring"
)

const (
	debugCleanupModeRetention int32 = iota + 1
	debugCleanupModeClearing
	debugCleanupModeDisabled
)

type queueRuntimeStats struct {
	Enabled       bool                      `json:"enabled"`
	Queued        int                       `json:"queued"`
	Capacity      int                       `json:"capacity"`
	Buffered      int64                     `json:"buffered"`
	Dropped       uint64                    `json:"dropped"`
	WriteFailed   uint64                    `json:"write_failed"`
	Written       uint64                    `json:"written"`
	LastDroppedAt int64                     `json:"last_dropped_at"`
	Operation     monitoring.OperationStats `json:"operation"`
}

type cleanupTaskRuntimeStats struct {
	Enabled          bool                      `json:"enabled"`
	Mode             string                    `json:"mode"`
	RetentionDays    int                       `json:"retention_days"`
	RetentionMinutes int                       `json:"retention_minutes"`
	IntervalMS       int64                     `json:"interval_ms"`
	NextRunAt        int64                     `json:"next_run_at"`
	Deleted          uint64                    `json:"deleted"`
	Operation        monitoring.OperationStats `json:"operation"`
}

type cleanupRuntimeStats struct {
	Logs      cleanupTaskRuntimeStats `json:"logs"`
	DebugLogs cleanupTaskRuntimeStats `json:"debug_logs"`
}

type logServiceRuntimeStats struct {
	Logs    queueRuntimeStats
	Traces  queueRuntimeStats
	Cleanup cleanupRuntimeStats
}

func (s *LogService) runtimeStats() logServiceRuntimeStats {
	mode := "unknown"
	switch s.debugCleanupMode.Load() {
	case debugCleanupModeRetention:
		mode = "retention"
	case debugCleanupModeClearing:
		mode = "clearing"
	case debugCleanupModeDisabled:
		mode = "disabled"
	}
	return logServiceRuntimeStats{
		Logs: queueRuntimeStats{
			Enabled: s.logWorkers > 0, Queued: len(s.logChan), Capacity: cap(s.logChan),
			Buffered: max(0, s.logBuffered.Load()), Written: s.logWritten.Load(),
			Dropped: s.logDropCount.Load(), WriteFailed: s.logWriteFailures.Load(),
			LastDroppedAt: s.logLastDroppedAt.Load(), Operation: s.logOperation.Snapshot(),
		},
		Traces: queueRuntimeStats{
			Enabled: s.traceChan != nil, Queued: len(s.traceChan), Capacity: cap(s.traceChan),
			Buffered: max(0, s.traceBuffered.Load()), Written: s.traceWritten.Load(),
			Dropped: s.traceDropCount.Load(), WriteFailed: s.traceWriteFailures.Load(),
			LastDroppedAt: s.traceLastDroppedAt.Load(), Operation: s.traceOperation.Snapshot(),
		},
		Cleanup: cleanupRuntimeStats{
			Logs: cleanupTaskRuntimeStats{
				Enabled: s.retentionDays > 0, Mode: "retention", RetentionDays: s.retentionDays,
				IntervalMS: config.LogCleanupInterval.Milliseconds(), NextRunAt: s.logCleanupNextAt.Load(),
				Operation: s.logCleanupOperation.Snapshot(),
			},
			DebugLogs: cleanupTaskRuntimeStats{
				Enabled: mode != "disabled", Mode: mode, RetentionMinutes: int(s.debugRetentionMinutes.Load()),
				IntervalMS: config.DebugLogCleanupInterval.Milliseconds(), NextRunAt: s.debugCleanupNextAt.Load(),
				Deleted: s.debugCleanupDeleted.Load(), Operation: s.debugCleanupOperation.Snapshot(),
			},
		},
	}
}
