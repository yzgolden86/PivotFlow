package app

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

type requestTraceWriterStore struct {
	storage.Store

	mu             sync.Mutex
	calls          int
	failureThrough int
	batches        [][]*model.RequestTrace
	callCh         chan int
}

func newRequestTraceWriterStore(failureThrough int) *requestTraceWriterStore {
	return &requestTraceWriterStore{
		failureThrough: failureThrough,
		callCh:         make(chan int, 16),
	}
}

func (s *requestTraceWriterStore) BatchAddRequestTraces(_ context.Context, traces []*model.RequestTrace) error {
	cloned := make([]*model.RequestTrace, 0, len(traces))
	for _, trace := range traces {
		cloned = append(cloned, cloneRequestTraceWorkerValue(trace))
	}

	s.mu.Lock()
	s.calls++
	call := s.calls
	s.batches = append(s.batches, cloned)
	fail := call <= s.failureThrough
	s.mu.Unlock()

	s.callCh <- call
	if fail {
		return context.DeadlineExceeded
	}
	return nil
}

func (s *requestTraceWriterStore) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *requestTraceWriterStore) batchSizes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	sizes := make([]int, 0, len(s.batches))
	for _, batch := range s.batches {
		sizes = append(sizes, len(batch))
	}
	return sizes
}

func newRequestTraceWorkerTestService(
	store storage.Store,
	bufferSize int,
) (*LogService, chan struct{}, *atomic.Bool, *sync.WaitGroup) {
	shutdownCh := make(chan struct{})
	isShuttingDown := &atomic.Bool{}
	wg := &sync.WaitGroup{}
	return &LogService{
		store:          store,
		traceChan:      make(chan *model.RequestTrace, bufferSize),
		shutdownCh:     shutdownCh,
		isShuttingDown: isShuttingDown,
		wg:             wg,
	}, shutdownCh, isShuttingDown, wg
}

func newRequestTraceWorkerTestTrace(requestID string) *model.RequestTrace {
	return &model.RequestTrace{
		RequestID:   requestID,
		StartedAt:   time.Now().UnixMilli(),
		AuthTokenID: 7,
		Model:       "writer-model",
		Outcome:     "success",
		Attempts: []model.RequestAttempt{{
			Number:      1,
			ChannelID:   11,
			Endpoint:    "https://writer.example.com",
			ActualModel: "writer-upstream-model",
			Outcome:     "success",
		}},
	}
}

func waitForRequestTraceWriterCall(t testing.TB, calls <-chan int, want int) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for {
		select {
		case call := <-calls:
			if call == want {
				return
			}
			if call > want {
				t.Fatalf("writer reached call %d before expected call %d", call, want)
			}
		case <-timer.C:
			t.Fatalf("writer did not reach call %d", want)
		}
	}
}

func TestRequestTraceWorkerSaturationDropsAndOwnsSnapshot(t *testing.T) {
	svc, _, _, _ := newRequestTraceWorkerTestService(nil, 1)
	trace := newRequestTraceWorkerTestTrace("pf_req_snapshot")

	svc.AddRequestTraceAsync(trace)
	trace.Model = "mutated-model"
	trace.Attempts[0].Endpoint = "https://mutated.example.com"
	trace.Attempts[0].ActualModel = "mutated-upstream-model"
	svc.AddRequestTraceAsync(newRequestTraceWorkerTestTrace("pf_req_dropped"))

	if got := svc.traceDropCount.Load(); got != 1 {
		t.Fatalf("drop count=%d, want 1", got)
	}
	if got := len(svc.traceChan); got != 1 {
		t.Fatalf("queued traces=%d, want 1", got)
	}

	queued := <-svc.traceChan
	if queued.Model != "writer-model" {
		t.Fatalf("queued model=%q, caller mutation reached writer", queued.Model)
	}
	if queued.Attempts[0].Endpoint != "https://writer.example.com" ||
		queued.Attempts[0].ActualModel != "writer-upstream-model" {
		t.Fatalf("queued attempt changed with caller mutation: %+v", queued.Attempts[0])
	}
}

func TestRequestTraceWorkerRecoversAfterTransientFailures(t *testing.T) {
	store := newRequestTraceWriterStore(2)
	svc, shutdownCh, _, wg := newRequestTraceWorkerTestService(store, 8)
	svc.startRequestTraceWorker()

	svc.AddRequestTraceAsync(newRequestTraceWorkerTestTrace("pf_req_retry"))
	waitForRequestTraceWriterCall(t, store.callCh, 3)
	close(shutdownCh)
	wg.Wait()

	if got := store.callCount(); got != 3 {
		t.Fatalf("write attempts=%d, want 3", got)
	}
	if got := svc.traceWritten.Load(); got != 1 {
		t.Fatalf("written traces=%d, want 1", got)
	}
	if got := svc.traceWriteFailures.Load(); got != 0 {
		t.Fatalf("failed traces=%d, want 0 after recovery", got)
	}
}

func TestRequestTraceWorkerCountsPermanentlyFailedTraces(t *testing.T) {
	store := newRequestTraceWriterStore(100)
	svc, shutdownCh, _, wg := newRequestTraceWorkerTestService(store, 8)
	svc.startRequestTraceWorker()

	svc.AddRequestTraceAsync(newRequestTraceWorkerTestTrace("pf_req_fail_1"))
	svc.AddRequestTraceAsync(newRequestTraceWorkerTestTrace("pf_req_fail_2"))
	waitForRequestTraceWriterCall(t, store.callCh, 3)
	close(shutdownCh)
	wg.Wait()

	if got := store.callCount(); got != 3 {
		t.Fatalf("write attempts=%d, want 3", got)
	}
	if got := svc.traceWriteFailures.Load(); got != 2 {
		t.Fatalf("failed traces=%d, want 2 (not 3 attempts)", got)
	}
	if got := svc.traceWritten.Load(); got != 0 {
		t.Fatalf("written traces=%d, want 0", got)
	}
	if sizes := store.batchSizes(); len(sizes) != 3 || sizes[0] != 2 || sizes[1] != 2 || sizes[2] != 2 {
		t.Fatalf("batch sizes=%v, want three batches of 2", sizes)
	}
}

func TestRequestTraceWorkerShutdownDrainMakesOneAttempt(t *testing.T) {
	store := newRequestTraceWriterStore(100)
	svc, shutdownCh, isShuttingDown, wg := newRequestTraceWorkerTestService(store, 8)
	svc.AddRequestTraceAsync(newRequestTraceWorkerTestTrace("pf_req_drain_1"))
	svc.AddRequestTraceAsync(newRequestTraceWorkerTestTrace("pf_req_drain_2"))

	isShuttingDown.Store(true)
	close(shutdownCh)
	svc.startRequestTraceWorker()
	wg.Wait()

	if got := store.callCount(); got != 1 {
		t.Fatalf("shutdown write attempts=%d, want exactly 1", got)
	}
	if sizes := store.batchSizes(); len(sizes) != 1 || sizes[0] != 2 {
		t.Fatalf("shutdown batches=%v, want one drained batch of 2", sizes)
	}
	if got := svc.traceWriteFailures.Load(); got != 2 {
		t.Fatalf("failed traces=%d, want 2", got)
	}
	if got := svc.traceWritten.Load(); got != 0 {
		t.Fatalf("written traces=%d, want 0", got)
	}
}
