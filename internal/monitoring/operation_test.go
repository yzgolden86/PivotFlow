package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Losing the active failure on Begin, resetting the cumulative count on
// success, or retaining an error's private text would break this contract.
func TestOperationMonitorFailureRecovery(t *testing.T) {
	var monitor OperationMonitor
	initial := monitor.Snapshot()
	if initial.Attempts != 0 || initial.Active != 0 || initial.LastSuccessAt != 0 || initial.Failing {
		t.Fatalf("unobserved operation reported activity: %+v", initial)
	}
	finish := monitor.Begin()
	if running := monitor.Snapshot(); running.Active != 1 || running.Attempts != 1 || running.LastAttemptAt == 0 {
		t.Fatalf("operation not visible while running: %+v", running)
	}
	finish(errors.New("dsn=postgres://user:test-secret@private-host secret-path"))
	failed := monitor.Snapshot()
	if !failed.Failing || failed.Failed != 1 || failed.ConsecutiveFailures != 1 || failed.LastFailureAt == 0 || failed.Active != 0 {
		t.Fatalf("failure not observable: %+v", failed)
	}
	if failed.LastErrorCode != "operation_failed" || failed.LastResult != "error" || failed.RecoveredAt != 0 {
		t.Fatalf("unexpected failure result: %+v", failed)
	}
	data, err := json.Marshal(failed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-") || strings.Contains(string(data), "postgres://") {
		t.Fatalf("operation snapshot leaked error text: %s", data)
	}
	finish = monitor.Begin()
	if !monitor.Snapshot().Failing {
		t.Fatal("starting a retry cleared the failure before it succeeded")
	}
	finish(nil)
	finish(errors.New("duplicate callback"))
	recovered := monitor.Snapshot()
	if recovered.Failing || recovered.Failed != 1 || recovered.Succeeded != 1 || recovered.ConsecutiveFailures != 0 || recovered.RecoveredAt == 0 {
		t.Fatalf("recovery must preserve historical failure without double completion: %+v", recovered)
	}
	if recovered.Attempts != 2 || recovered.Active != 0 || recovered.LastSuccessAt == 0 || recovered.LastResult != "success" {
		t.Fatalf("incorrect recovered counters: %+v", recovered)
	}
	monitor.Begin()(nil)
	if got := monitor.Snapshot().RecoveredAt; got != recovered.RecoveredAt {
		t.Fatalf("ordinary success rewrote the recovery transition: %d != %d", got, recovered.RecoveredAt)
	}
}

func TestOperationMonitorCancellationPreservesFailure(t *testing.T) {
	var monitor OperationMonitor
	monitor.Begin()(context.DeadlineExceeded)
	monitor.Begin()(context.Canceled)
	got := monitor.Snapshot()
	if !got.Failing || got.Failed != 1 || got.Cancelled != 1 || got.ConsecutiveFailures != 1 || got.LastErrorCode != "timeout" || got.RecoveredAt != 0 {
		t.Fatalf("cancellation masked an unresolved timeout: %+v", got)
	}
	var cancelledOnly OperationMonitor
	cancelledOnly.Begin()(context.Canceled)
	if got := cancelledOnly.Snapshot(); got.Failing || got.Failed != 0 || got.Cancelled != 1 || got.LastSuccessAt != 0 {
		t.Fatalf("shutdown cancellation reported a storage failure or success: %+v", got)
	}
}

func TestOperationMonitorConcurrentCompletion(t *testing.T) {
	var monitor OperationMonitor
	var wg sync.WaitGroup
	for range 100 {
		finish := monitor.Begin()
		wg.Add(1)
		go func() {
			defer wg.Done()
			finish(nil)
			finish(nil)
		}()
	}
	wg.Wait()
	got := monitor.Snapshot()
	if got.Active != 0 || got.Attempts != 100 || got.Succeeded != 100 || got.Failed != 0 {
		t.Fatalf("concurrent completion lost or duplicated operations: %+v", got)
	}
}

func TestOperationMonitorJoinedCancellationDoesNotHideFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		cancelled bool
		code      string
	}{
		{"wrapped cancellation", fmt.Errorf("shutdown: %w", context.Canceled), true, ""},
		{"all joined cancellations", errors.Join(context.Canceled, fmt.Errorf("replica: %w", context.Canceled)), true, ""},
		{"cancellation with disk error", errors.Join(context.Canceled, errors.New("disk full")), false, "operation_failed"},
		{"wrapped joined error", fmt.Errorf("cleanup: %w", errors.Join(errors.New("SQL failure"), context.Canceled)), false, "operation_failed"},
		{"nested multiple error", errors.Join(errors.Join(context.Canceled, errors.New("permission denied")), context.Canceled), false, "operation_failed"},
		{"cancellation with deadline", errors.Join(context.Canceled, context.DeadlineExceeded), false, "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var monitor OperationMonitor
			monitor.Begin()(tc.err)
			got := monitor.Snapshot()
			if tc.cancelled {
				if got.Cancelled != 1 || got.Failed != 0 || got.Failing {
					t.Fatalf("pure cancellation reported as a fault: %+v", got)
				}
			} else if got.Cancelled != 0 || got.Failed != 1 || !got.Failing || got.LastErrorCode != tc.code {
				t.Fatalf("cancellation concealed a real operation failure: %+v", got)
			}
		})
	}
}
