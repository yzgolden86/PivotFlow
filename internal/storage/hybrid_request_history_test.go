package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	sqlstore "github.com/yzgolden86/PivotFlow/internal/storage/sql"
)

func TestHybridRequestHistoryWritesLocalFirstAndClonesRemote(t *testing.T) {
	primary := createTestSQLiteStore(t)
	replica := createTestSQLiteStore(t)
	defer func() { _ = primary.Close() }()
	defer func() { _ = replica.Close() }()
	hybrid := NewHybridStore(replica, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	remoteOnly := testRequestTrace("pf_req_remote_only", "remote-only", "success", now, 0)
	if err := primary.BatchAddRequestTraces(ctx, []*model.RequestTrace{remoteOnly}); err != nil {
		t.Fatalf("seed primary-only trace: %v", err)
	}
	requireRequestTraceMissing(t, hybrid, ctx, remoteOnly.RequestID, nil)

	trace := testRequestTrace("pf_req_hybrid", "hybrid", "success", now, 5,
		model.RequestAttempt{Number: 1, Outcome: "success", Upstream: true, StatusCode: 200},
	)
	if err := hybrid.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatalf("hybrid BatchAddRequestTraces: %v", err)
	}
	if localTrace, err := hybrid.GetRequestTrace(ctx, trace.RequestID, nil); err != nil || localTrace == nil {
		t.Fatalf("local-first read failed: trace=%+v err=%v", localTrace, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if cloned, err := primary.GetRequestTrace(ctx, trace.RequestID, nil); err == nil && cloned != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for request trace clone in primary")
		}
		time.Sleep(10 * time.Millisecond)
	}

	trace.Model = "mutated-after-enqueue"
	cloned, err := primary.GetRequestTrace(ctx, trace.RequestID, nil)
	if err != nil {
		t.Fatalf("load cloned trace: %v", err)
	}
	if cloned.Model != "hybrid" {
		t.Fatalf("async clone retained mutable input: model=%q", cloned.Model)
	}
}

func TestHybridRequestTraceReplicationQueuesBoundedSanitizedPayload(t *testing.T) {
	local := createTestSQLiteStore(t)
	remote := createTestSQLiteStore(t)
	hybrid := newUnstartedHybridStore(local, remote, 1)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	attempts := make([]model.RequestAttempt, 300)
	for i := range attempts {
		attempts[i] = model.RequestAttempt{
			Number:      i + 1,
			ChannelName: "c\x00" + strings.Repeat("c", 255),
			ActualModel: "a\x00" + strings.Repeat("a", 255),
			Outcome:     "skipped",
		}
	}
	trace := testRequestTrace(
		"pf_req_bounded_sync",
		"m\x00"+strings.Repeat("m", 255),
		"success",
		now,
		0,
		attempts...,
	)
	if err := hybrid.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatalf("BatchAddRequestTraces oversized direct input: %v", err)
	}

	var task *syncTask
	select {
	case task = <-hybrid.syncCh:
	default:
		t.Fatal("request trace replication task was not queued")
	}
	batch, ok := task.data.(*syncTaskRequestTraceBatch)
	if !ok || len(batch.traces) != 1 {
		t.Fatalf("queued request trace payload=%#v", task.data)
	}
	queued := batch.traces[0]
	assertBoundedTracePayload(t, queued, "queued")

	localTrace, err := local.GetRequestTrace(ctx, trace.RequestID, nil)
	if err != nil {
		t.Fatalf("read normalized local trace: %v", err)
	}
	assertBoundedTracePayload(t, localTrace, "local")

	hybrid.executeSyncTask(task)
	remoteTrace, err := remote.GetRequestTrace(ctx, trace.RequestID, nil)
	if err != nil {
		t.Fatalf("read normalized remote trace: %v", err)
	}
	assertBoundedTracePayload(t, remoteTrace, "remote")
}

func assertBoundedTracePayload(t testing.TB, trace *model.RequestTrace, source string) {
	t.Helper()
	if trace == nil {
		t.Fatalf("%s trace is nil", source)
	}
	if len(trace.Attempts) != 128 || !trace.Truncated {
		t.Fatalf("%s attempts=%d truncated=%v, want 128/true", source, len(trace.Attempts), trace.Truncated)
	}
	if trace.Model != strings.Repeat("m", 191) || strings.ContainsRune(trace.Model, '\x00') {
		t.Fatalf("%s model was not normalized: %q", source, trace.Model)
	}
	attempt := trace.Attempts[0]
	if attempt.ChannelName != strings.Repeat("c", 191) || strings.ContainsRune(attempt.ChannelName, '\x00') {
		t.Fatalf("%s channel name was not normalized: %q", source, attempt.ChannelName)
	}
	if attempt.ActualModel != strings.Repeat("a", 191) || strings.ContainsRune(attempt.ActualModel, '\x00') {
		t.Fatalf("%s actual model was not normalized: %q", source, attempt.ActualModel)
	}
}

func TestHybridRequestHistoryCleanupAppliesToPrimaryAndReplica(t *testing.T) {
	primary := createTestSQLiteStore(t)
	replica := createTestSQLiteStore(t)
	defer func() { _ = primary.Close() }()
	defer func() { _ = replica.Close() }()
	hybrid := NewHybridStore(replica, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	cutoff := now.Add(-time.Hour)

	oldTrace := testRequestTrace("pf_req_old_both", "old", "success", cutoff.Add(-time.Minute), 0)
	newTrace := testRequestTrace("pf_req_new_both", "new", "success", cutoff.Add(time.Minute), 0)
	if err := hybrid.BatchAddRequestTraces(ctx, []*model.RequestTrace{oldTrace, newTrace}); err != nil {
		t.Fatalf("seed hybrid traces: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		oldCloned, oldErr := primary.GetRequestTrace(ctx, oldTrace.RequestID, nil)
		newCloned, newErr := primary.GetRequestTrace(ctx, newTrace.RequestID, nil)
		if oldErr == nil && newErr == nil && oldCloned != nil && newCloned != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for request traces to clone")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := hybrid.CleanupLogsBefore(ctx, cutoff); err != nil {
		t.Fatalf("hybrid CleanupLogsBefore: %v", err)
	}
	for name, store := range map[string]*sqlstore.SQLStore{"primary": primary, "replica": replica} {
		t.Run(name+" old trace missing", func(t *testing.T) {
			requireRequestTraceMissing(t, store, ctx, oldTrace.RequestID, nil)
		})
		if trace, err := store.GetRequestTrace(ctx, newTrace.RequestID, nil); err != nil || trace == nil {
			t.Fatalf("%s removed new trace during cleanup: trace=%+v err=%v", name, trace, err)
		}
	}
}

func TestSyncManagerRestoresRequestTraceWindow(t *testing.T) {
	primary := createTestSQLiteStore(t)
	replica := createTestSQLiteStore(t)
	defer func() { _ = primary.Close() }()
	defer func() { _ = replica.Close() }()
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	primaryTrace := testRequestTrace("pf_req_restore", "restored", "success", now.Add(-time.Minute), 3,
		model.RequestAttempt{Number: 1, Outcome: "success", StatusCode: 200, Upstream: true},
	)
	if err := primary.BatchAddRequestTraces(ctx, []*model.RequestTrace{primaryTrace}); err != nil {
		t.Fatalf("seed primary trace: %v", err)
	}
	localOnlyTrace := testRequestTrace("pf_req_local_only", "local-only", "error", now.Add(-time.Minute), 0)
	if err := replica.BatchAddRequestTraces(ctx, []*model.RequestTrace{
		localOnlyTrace,
	}); err != nil {
		t.Fatalf("seed local-only replica trace: %v", err)
	}

	if err := NewSyncManager(primary, replica).restoreRequestTracesSnapshot(ctx, 7); err != nil {
		t.Fatalf("restoreRequestTracesSnapshot: %v", err)
	}
	got, err := replica.GetRequestTrace(ctx, primaryTrace.RequestID, nil)
	if err != nil {
		t.Fatalf("restored trace missing: %v", err)
	}
	if got == nil {
		t.Fatal("restore did not import primary trace")
	}
	if len(got.Attempts) != 1 {
		t.Fatalf("restored attempts=%d, want 1", len(got.Attempts))
	}
	localTrace, err := replica.GetRequestTrace(ctx, localOnlyTrace.RequestID, nil)
	if err != nil {
		t.Fatalf("read local-only trace after restore: %v", err)
	}
	if localTrace == nil || localTrace.Model != "local-only" {
		t.Fatalf("restore deleted or changed local-only trace: %+v", localTrace)
	}
	if err := NewSyncManager(primary, replica).restoreRequestTracesSnapshot(ctx, 7); err != nil {
		t.Fatalf("repeat restoreRequestTracesSnapshot: %v", err)
	}
	traces, total, err := replica.ListRequestTraces(ctx, now.Add(-time.Hour), now.Add(time.Hour), 10, 0, nil)
	if err != nil {
		t.Fatalf("list merged request traces: %v", err)
	}
	if total != 2 || len(traces) != 2 {
		t.Fatalf("merged trace total=%d traces=%d, want 2 immutable rows", total, len(traces))
	}
}

func newUnstartedHybridStore(local, remote *sqlstore.SQLStore, queueSize int) *HybridStore {
	return &HybridStore{
		sqlite: local,
		mysql:  remote,
		syncCh: make(chan *syncTask, queueSize),
		stopCh: make(chan struct{}),
	}
}

func TestHybridRequestTraceReplicationReservesBoundedSlots(t *testing.T) {
	local := createTestSQLiteStore(t)
	remote := createTestSQLiteStore(t)
	hybrid := newUnstartedHybridStore(local, remote, requestTraceReplicationCapacity+64)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	for i := 0; i < requestTraceReplicationCapacity; i++ {
		trace := testRequestTrace(fmt.Sprintf("pf_req_capacity_%03d", i), "capacity", "success", now, 0)
		if err := hybrid.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
			t.Fatalf("write trace %d: %v", i, err)
		}
	}
	stats := hybrid.RequestTraceReplicationStats()
	if stats.Queued != requestTraceReplicationCapacity || stats.Capacity != requestTraceReplicationCapacity || stats.Dropped != 0 {
		t.Fatalf("saturated stats=%+v, want queued/capacity=%d dropped=0", stats, requestTraceReplicationCapacity)
	}

	extra := testRequestTrace("pf_req_capacity_extra", "capacity", "success", now, 0)
	if err := hybrid.BatchAddRequestTraces(ctx, []*model.RequestTrace{extra}); err != nil {
		t.Fatalf("local write with saturated replication slots: %v", err)
	}
	if got, err := hybrid.GetRequestTrace(ctx, extra.RequestID, nil); err != nil || got == nil {
		t.Fatalf("local trace missing after replication drop: trace=%+v err=%v", got, err)
	}
	stats = hybrid.RequestTraceReplicationStats()
	if stats.Queued != requestTraceReplicationCapacity || stats.Dropped != 1 {
		t.Fatalf("overflow stats=%+v, want queued=%d dropped=1", stats, requestTraceReplicationCapacity)
	}
	if got, err := remote.GetRequestTrace(ctx, extra.RequestID, nil); err != nil || got != nil {
		t.Fatalf("dropped trace reached remote: trace=%+v err=%v", got, err)
	}

	for len(hybrid.syncCh) > 0 {
		hybrid.executeSyncTask(<-hybrid.syncCh)
	}
	stats = hybrid.RequestTraceReplicationStats()
	if stats.Queued != 0 || stats.Failed != 0 {
		t.Fatalf("post-replication stats=%+v, want queued=0 failed=0", stats)
	}
}

func TestHybridRequestTraceReplicationRejectsFullSharedQueue(t *testing.T) {
	local := createTestSQLiteStore(t)
	remote := createTestSQLiteStore(t)
	hybrid := newUnstartedHybridStore(local, remote, 0)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	trace := testRequestTrace("pf_req_full_queue", "queue-full", "success", now, 0)
	if err := hybrid.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatalf("local write with full shared queue: %v", err)
	}
	if got, err := hybrid.GetRequestTrace(ctx, trace.RequestID, nil); err != nil || got == nil {
		t.Fatalf("local trace missing after queue rejection: trace=%+v err=%v", got, err)
	}
	stats := hybrid.RequestTraceReplicationStats()
	if stats.Queued != 0 || stats.Dropped != 1 {
		t.Fatalf("queue rejection stats=%+v, want queued=0 dropped=1", stats)
	}
}

func TestHybridRequestTraceReplicationReleasesDiscardedTasks(t *testing.T) {
	local := createTestSQLiteStore(t)
	remote := createTestSQLiteStore(t)
	hybrid := newUnstartedHybridStore(local, remote, requestTraceReplicationCapacity)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	trace := testRequestTrace("pf_req_shutdown_discard", "shutdown", "success", now, 0)
	if err := hybrid.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatalf("queue trace before shutdown discard: %v", err)
	}
	if stats := hybrid.RequestTraceReplicationStats(); stats.Queued != 1 {
		t.Fatalf("queued before discard=%+v, want 1", stats)
	}
	if discarded := hybrid.discardQueuedSyncTasks(); discarded != 1 {
		t.Fatalf("discarded tasks=%d, want 1", discarded)
	}
	stats := hybrid.RequestTraceReplicationStats()
	if stats.Queued != 0 || stats.Dropped != 1 || stats.Failed != 0 {
		t.Fatalf("discard release stats=%+v, want queued=0 dropped=1 failed=0", stats)
	}
}

func TestHybridRequestTraceReplicationReportsRemoteFailure(t *testing.T) {
	local := createTestSQLiteStore(t)
	remote := createTestSQLiteStore(t)
	hybrid := NewHybridStore(local, remote)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)

	if err := remote.Close(); err != nil {
		t.Fatalf("close remote: %v", err)
	}
	trace := testRequestTrace("pf_req_remote_failure", "remote-failure", "success", now, 0)
	if err := hybrid.BatchAddRequestTraces(ctx, []*model.RequestTrace{trace}); err != nil {
		t.Fatalf("local write must succeed when remote is unavailable: %v", err)
	}
	if got, err := hybrid.GetRequestTrace(ctx, trace.RequestID, nil); err != nil || got == nil {
		t.Fatalf("local trace missing after remote failure: trace=%+v err=%v", got, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		stats := hybrid.RequestTraceReplicationStats()
		if stats.Failed > 0 {
			if stats.Queued != 0 || stats.Capacity != requestTraceReplicationCapacity {
				t.Fatalf("failed replication stats=%+v, want queued=0 capacity=%d", stats, requestTraceReplicationCapacity)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for request trace replication failure counter")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHybridCleanupKeepsLocalRetentionWhenPrimaryFails(t *testing.T) {
	local := createTestSQLiteStore(t)
	primary := createTestSQLiteStore(t)
	hybrid := NewHybridStore(local, primary)
	t.Cleanup(func() { _ = hybrid.Close() })
	ctx := context.Background()
	now := time.Now().Truncate(time.Millisecond)
	cutoff := now.Add(-time.Hour)
	oldTrace := testRequestTrace("pf_req_local_retention", "old-local", "success", cutoff.Add(-time.Minute), 0)

	if err := local.AddLog(ctx, &model.LogEntry{Time: model.JSONTime{Time: cutoff.Add(-time.Minute)}, Model: "old-local", StatusCode: 200}); err != nil {
		t.Fatalf("seed local log: %v", err)
	}
	if err := local.BatchAddRequestTraces(ctx, []*model.RequestTrace{oldTrace}); err != nil {
		t.Fatalf("seed local trace: %v", err)
	}
	if err := primary.Close(); err != nil {
		t.Fatalf("close primary: %v", err)
	}

	if err := hybrid.CleanupLogsBefore(ctx, cutoff); err == nil {
		t.Fatal("CleanupLogsBefore hid primary cleanup failure")
	}
	requireRequestTraceMissing(t, local, ctx, oldTrace.RequestID, nil)
	logs, err := local.ListLogsRange(ctx, now.Add(-24*time.Hour), now, 10, 0, nil)
	if err != nil {
		t.Fatalf("list local logs after cleanup: %v", err)
	}
	if len(logs) != 0 {
		t.Fatalf("local retention did not run after primary failure: %d logs remain", len(logs))
	}
}
