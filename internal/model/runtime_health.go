package model

import "github.com/yzgolden86/PivotFlow/internal/monitoring"

// ReplicationRuntimeStats records best-effort replication. Log Queued is the
// shared channel's waiting tasks; request-history Queued includes active tasks
// in its separate budget. Dropped and Failed count tasks, not records.
type ReplicationRuntimeStats struct {
	Enabled       bool                      `json:"enabled"`
	Queued        int                       `json:"queued"`
	Capacity      int                       `json:"capacity"`
	Dropped       uint64                    `json:"dropped"`
	Failed        uint64                    `json:"failed"`
	LastDroppedAt int64                     `json:"last_dropped_at"`
	Operation     monitoring.OperationStats `json:"operation"`
}

// StartupRecoveryStats preserves optional history recovery failures even when
// configuration recovery succeeded and the application was allowed to start.
type StartupRecoveryStats struct {
	Enabled        bool                      `json:"enabled"`
	HistoryEnabled bool                      `json:"history_enabled"`
	LogDays        int                       `json:"log_days"`
	Configurations monitoring.OperationStats `json:"configurations"`
	Logs           monitoring.OperationStats `json:"logs"`
	RequestTraces  monitoring.OperationStats `json:"request_traces"`
}

// StorageRuntimeStats is a memory-only optional capability of storage backends.
// It deliberately contains neither database addresses nor raw error messages.
type StorageRuntimeStats struct {
	Mode            string                    `json:"mode"`
	Primary         string                    `json:"primary"`
	CacheSync       monitoring.OperationStats `json:"cache_sync"`
	CacheMayBeStale bool                      `json:"cache_may_be_stale"`
	LogReplication  ReplicationRuntimeStats   `json:"log_replication"`
	StartupRecovery *StartupRecoveryStats     `json:"startup_recovery"`
}
