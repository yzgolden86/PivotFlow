package app

import (
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/monitoring"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

const (
	runtimeRecentDropWindow  = time.Minute
	runtimeBillingBacklogAge = 30 * time.Second
)

type requestTraceRuntimeStats struct {
	queueRuntimeStats
	Replication *storage.RequestTraceReplicationStats `json:"replication,omitempty"`
}

type runtimeHealthComponent struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type runtimeHealthSnapshot struct {
	Version    int                      `json:"version"`
	State      string                   `json:"state"`
	Components []runtimeHealthComponent `json:"components"`
}

type runtimeMetricsSnapshot struct {
	StartedAt          int64                               `json:"started_at"`
	ObservedAt         int64                               `json:"observed_at"`
	ResponsesWebsocket responsesExecutionSessionStoreStats `json:"responses_websocket"`
	Billing            billingRuntimeStats                 `json:"billing"`
	TokenFailureStats  queueRuntimeStats                   `json:"token_failure_stats"`
	RequestTraces      requestTraceRuntimeStats            `json:"request_traces"`
	Logs               queueRuntimeStats                   `json:"logs"`
	Cleanup            cleanupRuntimeStats                 `json:"cleanup"`
	Storage            model.StorageRuntimeStats           `json:"storage"`
	Health             runtimeHealthSnapshot               `json:"health"`
	RequestCache       *requestCacheSnapshot               `json:"request_cache,omitempty"`
}

// collectRuntimeMetrics performs no database probes. All observations are
// copied from the real work paths; cumulative loss is separate from health.
func (s *Server) collectRuntimeMetrics(now time.Time) runtimeMetricsSnapshot {
	snapshot := runtimeMetricsSnapshot{StartedAt: s.runtimeStartedAt, ObservedAt: now.UnixMilli(),
		Storage: model.StorageRuntimeStats{Mode: "unknown", Primary: "unknown"},
	}
	if s.responsesExecutionSessions != nil {
		snapshot.ResponsesWebsocket = s.responsesExecutionSessions.stats()
	}
	if s.responsesWebsocketConnections != nil {
		connections := s.responsesWebsocketConnections.stats()
		snapshot.ResponsesWebsocket.DownstreamConnections = connections.Active
		snapshot.ResponsesWebsocket.RejectedDownstreamConnections = connections.Rejected
		snapshot.ResponsesWebsocket.MaxDownstreamConnections = connections.Max
		snapshot.ResponsesWebsocket.MaxDownstreamConnectionsPerToken = connections.MaxPerSubject
	}
	if s.billing != nil {
		snapshot.Billing = s.billing.stats()
	}
	if s.requestCache != nil {
		cache := s.requestCacheSnapshot(now)
		snapshot.RequestCache = &cache
	}
	if s.logService != nil {
		logs := s.logService.runtimeStats()
		snapshot.Logs = logs.Logs
		snapshot.RequestTraces.queueRuntimeStats = logs.Traces
		snapshot.Cleanup = logs.Cleanup
	}
	operation := s.tokenStatsOperation.Snapshot()
	snapshot.TokenFailureStats = queueRuntimeStats{
		Enabled: s.tokenStatsCh != nil, Queued: len(s.tokenStatsCh), Capacity: cap(s.tokenStatsCh),
		Buffered: int64(operation.Active), Dropped: uint64(max(0, s.tokenStatsDropCount.Load())),
		Written: operation.Succeeded, WriteFailed: operation.Failed,
		LastDroppedAt: s.tokenStatsLastDroppedAt.Load(), Operation: operation,
	}
	if source, ok := s.store.(interface {
		StorageRuntimeStats() model.StorageRuntimeStats
	}); ok {
		snapshot.Storage = normalizeStorageRuntimeStats(source.StorageRuntimeStats())
	}
	if source, ok := s.store.(interface {
		RequestTraceReplicationStats() storage.RequestTraceReplicationStats
	}); ok {
		replication := source.RequestTraceReplicationStats()
		replication.Operation = safeOperationStats(replication.Operation)
		snapshot.RequestTraces.Replication = &replication
	}
	components := []runtimeHealthComponent{
		billingComponentState(snapshot.Billing, now),
		queueComponentState("request_history", snapshot.RequestTraces.queueRuntimeStats, now),
		queueComponentState("logs", snapshot.Logs, now),
		queueComponentState("token_stats", snapshot.TokenFailureStats, now),
		replicationComponentState("log_replication", snapshot.Storage.LogReplication, now),
		cacheComponentState(snapshot.Storage),
		cleanupComponentState("log_cleanup", snapshot.Cleanup.Logs),
		cleanupComponentState("debug_cleanup", snapshot.Cleanup.DebugLogs),
		startupRecoveryComponentState(snapshot.Storage),
		websocketComponentState(s.responsesExecutionSessions != nil || s.responsesWebsocketConnections != nil, snapshot.ResponsesWebsocket),
	}
	if snapshot.Storage.Mode == "unknown" {
		for i := range components {
			if components[i].ID == "log_replication" || components[i].ID == "cache_sync" {
				components[i].State, components[i].Reason = "unknown", "storage_unknown"
			}
		}
	}
	traceReplication := runtimeHealthComponent{ID: "trace_replication", State: "disabled", Reason: "not_enabled"}
	if snapshot.RequestTraces.Replication != nil {
		traceReplication = replicationComponentState("trace_replication", *snapshot.RequestTraces.Replication, now)
	} else if snapshot.Storage.Mode == "hybrid" {
		traceReplication.State, traceReplication.Reason = "unknown", "replication_unobserved"
	} else if snapshot.Storage.Mode == "unknown" {
		traceReplication.State, traceReplication.Reason = "unknown", "storage_unknown"
	}
	components = append(components, traceReplication)
	snapshot.Health = runtimeHealthSnapshot{Version: 1, State: overallRuntimeHealth(components), Components: components}
	return snapshot
}

func queueComponentState(id string, stats queueRuntimeStats, now time.Time) runtimeHealthComponent {
	component := runtimeHealthComponent{ID: id, State: "idle", Reason: "awaiting_work"}
	switch {
	case !stats.Enabled:
		component.State, component.Reason = "disabled", "not_enabled"
	case stats.Operation.Failing:
		component.State, component.Reason = "error", "write_failed"
	case runtimeCapacityPressure(int64(stats.Queued), int64(stats.Capacity)):
		component.State, component.Reason = "warning", "queue_pressure"
	case recentRuntimeDrop(stats.LastDroppedAt, now):
		component.State, component.Reason = "warning", "recent_drop"
	case stats.Queued > 0 || stats.Buffered > 0 || stats.Operation.Active > 0:
		component.State, component.Reason = "working", "processing"
	case stats.Operation.Succeeded > 0:
		component.State, component.Reason = "healthy", "ready"
	}
	return component
}

func billingComponentState(stats billingRuntimeStats, now time.Time) runtimeHealthComponent {
	component := runtimeHealthComponent{ID: "billing", State: "idle", Reason: "awaiting_work"}
	switch {
	case !stats.Enabled:
		component.State, component.Reason = "disabled", "not_enabled"
	case stats.Closing:
		component.State, component.Reason = "blocked", "shutting_down"
	case stats.Blocked:
		component.State, component.Reason = "blocked", "billing_blocked"
	case stats.Unpersisted > 0:
		component.State, component.Reason = "error", "billing_unpersisted"
	case stats.Settlement.Failing:
		component.State, component.Reason = "error", "settlement_failed"
	case stats.Journal.Failing:
		component.State, component.Reason = "error", "journal_failed"
	case stats.Pending > 0 && stats.OldestPendingAt > 0 && now.Sub(time.UnixMilli(stats.OldestPendingAt)) > runtimeBillingBacklogAge:
		component.State, component.Reason = "warning", "billing_backlog"
	case stats.Pending > 0 || stats.Settlement.Active > 0:
		component.State, component.Reason = "working", "pending_settlement"
	case stats.Settlement.Succeeded > 0:
		component.State, component.Reason = "healthy", "ready"
	}
	return component
}

func replicationComponentState(id string, stats model.ReplicationRuntimeStats, now time.Time) runtimeHealthComponent {
	component := queueComponentState(id, queueRuntimeStats{
		Enabled: stats.Enabled, Queued: stats.Queued, Capacity: stats.Capacity,
		LastDroppedAt: stats.LastDroppedAt, Operation: stats.Operation,
	}, now)
	if stats.Enabled && stats.Operation.Failing {
		component.State, component.Reason = "warning", "replication_failed"
	}
	return component
}

func cacheComponentState(stats model.StorageRuntimeStats) runtimeHealthComponent {
	component := queueComponentState("cache_sync", queueRuntimeStats{Enabled: stats.Mode == "hybrid", Operation: stats.CacheSync}, time.Time{})
	if stats.Mode == "hybrid" {
		if stats.CacheSync.Failing {
			component.State, component.Reason = "warning", "cache_sync_failed"
		} else if stats.CacheMayBeStale {
			component.State, component.Reason = "warning", "cache_needs_reconcile"
		}
	}
	return component
}

func cleanupComponentState(id string, stats cleanupTaskRuntimeStats) runtimeHealthComponent {
	component := queueComponentState(id, queueRuntimeStats{Enabled: stats.Enabled, Operation: stats.Operation}, time.Time{})
	if stats.Enabled && stats.Operation.Failing {
		component.State, component.Reason = "warning", "cleanup_failed"
	} else if id == "debug_cleanup" && !stats.Enabled && stats.Mode == "disabled" {
		component.Reason = "debug_disabled_cleared"
	} else if id == "log_cleanup" && !stats.Enabled {
		component.Reason = "retention_disabled"
	}
	return component
}

func startupRecoveryComponentState(stats model.StorageRuntimeStats) runtimeHealthComponent {
	component := runtimeHealthComponent{ID: "startup_recovery", State: "disabled", Reason: "not_hybrid"}
	if stats.Mode == "unknown" || stats.Mode == "" {
		component.State, component.Reason = "unknown", "storage_unknown"
		return component
	}
	if stats.Mode != "hybrid" {
		return component
	}
	recovery := stats.StartupRecovery
	switch {
	case recovery == nil || !recovery.Enabled:
		component.State, component.Reason = "unknown", "recovery_unobserved"
	case recovery.Configurations.Failing:
		component.State, component.Reason = "error", "configuration_recovery_failed"
	case recovery.HistoryEnabled && (recovery.Logs.Failing || recovery.RequestTraces.Failing):
		component.State, component.Reason = "warning", "history_recovery_failed"
	case recovery.Configurations.Active > 0 || recovery.Logs.Active > 0 || recovery.RequestTraces.Active > 0:
		component.State, component.Reason = "working", "restoring"
	case recovery.Configurations.Succeeded == 0:
		component.State, component.Reason = "unknown", "recovery_unobserved"
	default:
		component.State, component.Reason = "healthy", "ready"
	}
	return component
}

func websocketComponentState(enabled bool, stats responsesExecutionSessionStoreStats) runtimeHealthComponent {
	component := runtimeHealthComponent{ID: "websocket", State: "idle", Reason: "no_sessions"}
	switch {
	case !enabled:
		component.State, component.Reason = "disabled", "not_enabled"
	case runtimeCapacityPressure(int64(stats.DownstreamConnections), int64(stats.MaxDownstreamConnections)) ||
		runtimeCapacityPressure(int64(stats.Sessions), int64(stats.MaxSessions)) || runtimeCapacityPressure(stats.TranscriptBytes, stats.MaxTranscriptBytes):
		component.State, component.Reason = "warning", "websocket_pressure"
	case stats.DownstreamConnections > 0 || stats.Sessions > 0:
		component.State, component.Reason = "healthy", "ready"
	}
	return component
}

func overallRuntimeHealth(components []runtimeHealthComponent) string {
	priority := map[string]int{"disabled": -1, "idle": 0, "healthy": 1, "working": 2, "unknown": 3, "warning": 4, "error": 5, "blocked": 6}
	state := "idle"
	for _, component := range components {
		if priority[component.State] > priority[state] {
			state = component.State
		}
	}
	return state
}

func runtimeCapacityPressure(queued, capacity int64) bool {
	return capacity > 0 && float64(queued)/float64(capacity) >= 0.8
}

func recentRuntimeDrop(lastDroppedAt int64, now time.Time) bool {
	if lastDroppedAt <= 0 {
		return false
	}
	age := now.Sub(time.UnixMilli(lastDroppedAt))
	return age >= 0 && age <= runtimeRecentDropWindow
}

func normalizeStorageRuntimeStats(stats model.StorageRuntimeStats) model.StorageRuntimeStats {
	stats.Mode = safeStorageMode(stats.Mode, true)
	stats.Primary = safeStorageMode(stats.Primary, false)
	stats.CacheSync = safeOperationStats(stats.CacheSync)
	stats.LogReplication.Operation = safeOperationStats(stats.LogReplication.Operation)
	if stats.StartupRecovery != nil {
		copyRecovery := *stats.StartupRecovery
		copyRecovery.Configurations = safeOperationStats(copyRecovery.Configurations)
		copyRecovery.Logs = safeOperationStats(copyRecovery.Logs)
		copyRecovery.RequestTraces = safeOperationStats(copyRecovery.RequestTraces)
		stats.StartupRecovery = &copyRecovery
	}
	return stats
}

func safeStorageMode(mode string, allowHybrid bool) string {
	switch mode {
	case "sqlite", "mysql", "postgres":
		return mode
	case "hybrid":
		if allowHybrid {
			return mode
		}
	}
	return "unknown"
}

func safeOperationStats(stats monitoring.OperationStats) monitoring.OperationStats {
	switch stats.LastErrorCode {
	case "", "timeout", "operation_failed":
	default:
		stats.LastErrorCode = "operation_failed"
	}
	switch stats.LastResult {
	case "", "success", "error", "cancelled":
	default:
		stats.LastResult = "unknown"
	}
	return stats
}
