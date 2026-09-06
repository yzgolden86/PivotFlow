package app

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestSiteControlAsyncTaskCancellationAndStop(t *testing.T) {
	baseCtx, baseCancel := context.WithCancel(context.Background())
	defer baseCancel()
	var wg sync.WaitGroup
	service := &siteControlService{
		baseCtx: baseCtx,
		wg:      &wg,
		tasks:   make(map[string]context.CancelFunc),
	}
	started := make(chan struct{})
	finished := make(chan struct{})
	if !service.runAsync("st_async", func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(finished)
	}) {
		t.Fatal("runAsync rejected an active service")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("async task did not start")
	}
	service.cancelTask("st_async")
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("task cancellation did not reach the worker context")
	}
	service.stopTasks()
	if service.runAsync("st_late", func(context.Context) {}) {
		t.Fatal("stopped service accepted a new task")
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("site task remained outside the server wait group")
	}
}

func TestManualSiteTaskQueuePreservesExecutionBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var wg sync.WaitGroup
		service := &siteControlService{baseCtx: context.Background(), wg: &wg, tasks: make(map[string]context.CancelFunc)}
		release, ok := service.acquireSiteGate(context.Background(), 1)
		if !ok {
			t.Fatal("scheduled task did not acquire gate")
		}
		started := make(chan time.Duration, 1)
		service.runAsync("queued", func(ctx context.Context) {
			runCtx, finish, err := service.acquireManualSiteTask(ctx, 1)
			if err != nil {
				t.Errorf("queued task failed: %v", err)
				return
			}
			defer finish()
			deadline, _ := runCtx.Deadline()
			started <- time.Until(deadline)
			<-runCtx.Done()
			if runCtx.Err() != context.DeadlineExceeded {
				t.Errorf("execution ended with %v, want deadline", runCtx.Err())
			}
		})
		time.Sleep(130 * time.Second)
		release()
		wg.Wait()
		select {
		case remaining := <-started:
			if remaining != siteTaskRunTimeout {
				t.Fatalf("execution budget=%v, want %v", remaining, siteTaskRunTimeout)
			}
		default:
			t.Fatal("manual task never reached execution")
		}
		release, ok = service.acquireSiteGate(context.Background(), 1)
		if !ok {
			t.Fatal("timed-out task retained gate")
		}
		release()
	})
}

func TestManualSiteTaskQueueCancellationAndTimeout(t *testing.T) {
	for _, mode := range []string{"cancel", "stop", "queue_timeout"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var wg sync.WaitGroup
				service := &siteControlService{baseCtx: context.Background(), wg: &wg, tasks: make(map[string]context.CancelFunc)}
				release, _ := service.acquireSiteGate(context.Background(), 1)
				defer release()
				result := make(chan error, 1)
				service.runAsync("queued", func(ctx context.Context) {
					_, finish, err := service.acquireManualSiteTask(ctx, 1)
					if finish != nil {
						finish()
					}
					result <- err
				})
				synctest.Wait()
				want := context.Canceled
				switch mode {
				case "cancel":
					service.cancelTask("queued")
				case "stop":
					service.stopTasks()
				default:
					want = context.DeadlineExceeded
					time.Sleep(siteTaskQueueTimeout + time.Second)
				}
				wg.Wait()
				if got := <-result; got != want {
					t.Fatalf("queue ended with %v, want %v", got, want)
				}
			})
		})
	}
}
