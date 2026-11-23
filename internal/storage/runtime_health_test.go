package storage

import (
	"context"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestRuntimeHealthStorageReplicationRecovery(t *testing.T) {
	local := createTestSQLiteStore(t)
	primary := createTestSQLiteStore(t)
	h := newUnstartedHybridStore(local, primary, 4)
	t.Cleanup(func() { _ = h.Close() })
	ctx := context.Background()
	if _, err := primary.ExecContext(ctx, "ALTER TABLE logs RENAME TO unavailable_logs"); err != nil {
		t.Fatal(err)
	}
	h.enqueueLogSync(&syncTask{operation: "log", data: &syncTaskLog{entry: &model.LogEntry{Model: "first"}}})
	h.executeSyncTask(<-h.syncCh)
	failed := h.StorageRuntimeStats().LogReplication
	if !failed.Enabled || !failed.Operation.Failing || failed.Failed != 1 || failed.Operation.LastFailureAt == 0 {
		t.Fatalf("failed log replication not visible: %+v", failed)
	}
	if _, err := primary.ExecContext(ctx, "ALTER TABLE unavailable_logs RENAME TO logs"); err != nil {
		t.Fatal(err)
	}
	h.enqueueLogSync(&syncTask{operation: "log", data: &syncTaskLog{entry: &model.LogEntry{Model: "later"}}})
	h.executeSyncTask(<-h.syncCh)
	recovered := h.StorageRuntimeStats().LogReplication
	if recovered.Operation.Failing || recovered.Failed != 1 || recovered.Operation.Succeeded != 1 || recovered.Operation.RecoveredAt == 0 {
		t.Fatalf("later replication success did not preserve failure history: %+v", recovered)
	}

	if _, err := primary.ExecContext(ctx, "ALTER TABLE request_traces RENAME TO unavailable_traces"); err != nil {
		t.Fatal(err)
	}
	first := testRequestTrace("pf_req_sync_failed", "first", "success", time.Now(), 0)
	if err := h.BatchAddRequestTraces(ctx, []*model.RequestTrace{first}); err != nil {
		t.Fatal(err)
	}
	h.executeSyncTask(<-h.syncCh)
	traceFailed := h.RequestTraceReplicationStats()
	if traceFailed.Queued != 0 || traceFailed.Failed != 1 || !traceFailed.Operation.Failing {
		t.Fatalf("failed trace replication not visible: %+v", traceFailed)
	}
	if _, err := primary.ExecContext(ctx, "ALTER TABLE unavailable_traces RENAME TO request_traces"); err != nil {
		t.Fatal(err)
	}
	later := testRequestTrace("pf_req_sync_later", "later", "success", time.Now(), 0)
	if err := h.BatchAddRequestTraces(ctx, []*model.RequestTrace{later}); err != nil {
		t.Fatal(err)
	}
	h.executeSyncTask(<-h.syncCh)
	traceRecovered := h.RequestTraceReplicationStats()
	if traceRecovered.Failed != 1 || traceRecovered.Operation.Failing || traceRecovered.Operation.RecoveredAt == 0 {
		t.Fatalf("trace transport recovery misreported: %+v", traceRecovered)
	}
	if got, err := primary.GetRequestTrace(ctx, first.RequestID, nil); err != nil || got != nil {
		t.Fatalf("best-effort failure must not be silently treated as backfilled: got=%+v err=%v", got, err)
	}
	if got, err := local.GetRequestTrace(ctx, first.RequestID, nil); err != nil || got == nil {
		t.Fatalf("remote outage affected the successful local write: got=%+v err=%v", got, err)
	}
}

func TestRuntimeHealthStorageDropsIncludeShutdownDiscard(t *testing.T) {
	h := &HybridStore{syncCh: make(chan *syncTask, 2), stopCh: make(chan struct{})}
	for range 3 {
		h.enqueueLogSync(&syncTask{operation: "log"})
	}
	before := h.StorageRuntimeStats().LogReplication
	if before.Queued != 2 || before.Capacity != 2 || before.Dropped != 1 || before.LastDroppedAt == 0 {
		t.Fatalf("queue overflow not visible: %+v", before)
	}
	if discarded := h.discardQueuedSyncTasks(); discarded != 2 {
		t.Fatalf("discarded=%d, want 2", discarded)
	}
	if got := h.StorageRuntimeStats().LogReplication; got.Queued != 0 || got.Dropped != 3 || got.Failed != 0 {
		t.Fatalf("shutdown discard was omitted or counted as database failure: %+v", got)
	}
}

func TestRuntimeHealthStorageSettlementReplicaNeedsReconcile(t *testing.T) {
	local := createTestSQLiteStore(t)
	primary := createTestSQLiteStore(t)
	h := newUnstartedHybridStore(local, primary, 2)
	t.Cleanup(func() { _ = h.Close() })
	ctx := context.Background()
	hash := model.HashToken("runtime-health-settlement")
	if err := h.CreateAuthToken(ctx, &model.AuthToken{Token: hash, IsActive: true, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := local.ExecContext(ctx, `CREATE TRIGGER reject_health_settlement BEFORE UPDATE ON auth_tokens BEGIN SELECT RAISE(ABORT, 'replica unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	event := model.TokenSettlement{ID: "runtime-settlement", TokenHash: hash, OccurredAt: time.Now().UnixMilli(), PromptTokens: 3}
	if err := h.ApplyTokenSettlement(ctx, event); err == nil {
		t.Fatal("failed replica accounting was acknowledged")
	}
	stats := h.StorageRuntimeStats()
	if !stats.CacheSync.Failing || !stats.CacheMayBeStale || stats.CacheSync.Failed != 1 {
		t.Fatalf("settlement's direct replica path was not monitored: %+v", stats)
	}
	if err := h.UpdateSetting(ctx, "debug_log_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	stats = h.StorageRuntimeStats()
	if stats.CacheSync.Failing || stats.CacheSync.RecoveredAt == 0 || !stats.CacheMayBeStale {
		t.Fatalf("an unrelated success must not claim that missing accounting was repaired: %+v", stats)
	}
	if _, err := local.ExecContext(ctx, "DROP TRIGGER reject_health_settlement"); err != nil {
		t.Fatal(err)
	}
	if err := h.ApplyTokenSettlement(ctx, event); err != nil {
		t.Fatal(err)
	}
	if got, err := local.GetAuthTokenByValue(ctx, hash); err != nil || got.SuccessCount != 1 {
		t.Fatalf("settlement replay did not repair this event idempotently: got=%+v err=%v", got, err)
	}
}

func TestRuntimeHealthStorageStartupPartialRecoveryRemainsVisible(t *testing.T) {
	local := createTestSQLiteStore(t)
	primary := createTestSQLiteStore(t)
	t.Cleanup(func() { _ = local.Close(); _ = primary.Close() })
	ctx := context.Background()
	for _, table := range []string{"logs", "request_traces"} {
		if _, err := primary.ExecContext(ctx, "ALTER TABLE "+table+" RENAME TO unavailable_"+table); err != nil {
			t.Fatal(err)
		}
	}
	manager := NewSyncManager(primary, local)
	if err := manager.RestoreOnStartup(ctx, 7); err != nil {
		t.Fatalf("optional history failure changed startup admission: %v", err)
	}
	got := manager.StartupRecoveryStats()
	if !got.Enabled || !got.HistoryEnabled || got.LogDays != 7 || got.Configurations.Succeeded != 1 || !got.Logs.Failing || !got.RequestTraces.Failing {
		t.Fatalf("overall startup success hid partial history failure: %+v", got)
	}
	h := newUnstartedHybridStore(local, primary, 2)
	h.SetStartupRecoveryStats(got)
	if exposed := h.StorageRuntimeStats().StartupRecovery; exposed == nil || exposed.Logs.Failed != 1 || exposed.RequestTraces.Failed != 1 {
		t.Fatalf("startup observations were not transferred to runtime: %+v", exposed)
	}
	for _, table := range []string{"logs", "request_traces"} {
		if _, err := primary.ExecContext(ctx, "ALTER TABLE unavailable_"+table+" RENAME TO "+table); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.RestoreOnStartup(ctx, 7); err != nil {
		t.Fatal(err)
	}
	got = manager.StartupRecoveryStats()
	if got.Logs.Failing || got.RequestTraces.Failing || got.Logs.Failed != 1 || got.Logs.RecoveredAt == 0 || got.RequestTraces.RecoveredAt == 0 {
		t.Fatalf("later startup recovery erased history or remained failed: %+v", got)
	}
}

func TestRuntimeHealthStorageDisabledHistoryAndMemoryOnlySnapshot(t *testing.T) {
	local := createTestSQLiteStore(t)
	primary := createTestSQLiteStore(t)
	manager := NewSyncManager(primary, local)
	if err := manager.RestoreOnStartup(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	stats := manager.StartupRecoveryStats()
	if !stats.Enabled || stats.HistoryEnabled || stats.Logs.Attempts != 0 || stats.RequestTraces.Attempts != 0 {
		t.Fatalf("disabled history was falsely reported as recovered: %+v", stats)
	}
	if err := primary.Close(); err != nil {
		t.Fatal(err)
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	if got := local.StorageRuntimeStats(); got.Mode != "sqlite" || got.LogReplication.Enabled || got.StartupRecovery != nil {
		t.Fatalf("pure, closed SQL snapshot required a database query or reported hybrid work: %+v", got)
	}
	h := newUnstartedHybridStore(local, primary, 2)
	h.SetStartupRecoveryStats(stats)
	if got := h.StorageRuntimeStats(); got.Mode != "hybrid" || got.StartupRecovery.Configurations.Succeeded != 1 {
		t.Fatalf("runtime snapshot could not be read while databases are closed: %+v", got)
	}
}
