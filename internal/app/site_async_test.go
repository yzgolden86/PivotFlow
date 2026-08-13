package app

import (
	"context"
	"sync"
	"testing"
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
