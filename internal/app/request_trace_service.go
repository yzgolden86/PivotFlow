package app

import (
	"context"
	"log"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/config"
	"github.com/yzgolden86/PivotFlow/internal/model"
)

const (
	requestTraceBatchSize     = 32
	requestTraceFlushInterval = 200 * time.Millisecond
	requestTraceFlushTimeout  = 3 * time.Second
	requestTraceMaxAttempts   = 3
	requestTraceDropLogEveryN = 100
)

// AddRequestTraceAsync snapshots a finished trace and enqueues it without
// blocking proxy completion. Queue overflow increments traceDropCount.
func (s *LogService) AddRequestTraceAsync(entry *model.RequestTrace) {
	if entry == nil {
		return
	}
	if s.isShutdownInProgress() {
		s.traceDropCount.Add(1)
		s.traceLastDroppedAt.Store(time.Now().UnixMilli())
		return
	}

	snapshot := cloneRequestTraceWorkerValue(entry)
	select {
	case s.traceChan <- snapshot:
	default:
		count := s.traceDropCount.Add(1)
		s.traceLastDroppedAt.Store(time.Now().UnixMilli())
		if count%requestTraceDropLogEveryN == 1 {
			log.Printf("[WARN] 请求历史队列已满，记录被丢弃 (累计丢弃: %d)", count)
		}
	}
}

// startRequestTraceWorker starts the single bounded request-history writer.
func (s *LogService) startRequestTraceWorker() {
	s.wg.Add(1)
	go s.requestTraceWorker()
}

func (s *LogService) requestTraceWorker() {
	defer s.wg.Done()

	batch := make([]*model.RequestTrace, 0, requestTraceBatchSize)
	ticker := time.NewTicker(requestTraceFlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.shutdownCh:
			s.drainRequestTraceQueue(batch)
			return

		case trace, ok := <-s.traceChan:
			if !ok {
				s.flushIfRequestTracesNeed(trace, batch)
				return
			}
			if trace != nil {
				batch = append(batch, trace)
				s.traceBuffered.Add(1)
			}
			if len(batch) >= requestTraceBatchSize {
				s.flushBufferedRequestTraces(batch)
				clear(batch)
				batch = batch[:0]
				ticker.Reset(requestTraceFlushInterval)
			}

		case <-ticker.C:
			if len(batch) > 0 {
				s.flushBufferedRequestTraces(batch)
				clear(batch)
				batch = batch[:0]
			}
		}
	}
}

func (s *LogService) drainRequestTraceQueue(batch []*model.RequestTrace) {
	for {
		select {
		case trace, ok := <-s.traceChan:
			if !ok {
				if len(batch) > 0 {
					s.flushBufferedRequestTraces(batch)
				}
				return
			}
			if trace != nil {
				batch = append(batch, trace)
				s.traceBuffered.Add(1)
			}
			if len(batch) >= requestTraceBatchSize {
				s.flushBufferedRequestTraces(batch)
				clear(batch)
				batch = batch[:0]
			}
		default:
			if len(batch) > 0 {
				s.flushBufferedRequestTraces(batch)
			}
			return
		}
	}
}

func (s *LogService) flushIfRequestTracesNeed(trace *model.RequestTrace, batch []*model.RequestTrace) {
	if trace != nil {
		batch = append(batch, trace)
		s.traceBuffered.Add(1)
	}
	if len(batch) > 0 {
		s.flushBufferedRequestTraces(batch)
	}
}

func (s *LogService) flushBufferedRequestTraces(batch []*model.RequestTrace) {
	s.flushRequestTraces(batch)
	s.traceBuffered.Add(-int64(len(batch)))
}

// flushRequestTraces writes one bounded batch. A trace is counted as written
// after a successful write, or as failed once after all attempts are exhausted.
func (s *LogService) flushRequestTraces(traces []*model.RequestTrace) {
	batch := make([]*model.RequestTrace, 0, len(traces))
	for _, trace := range traces {
		if trace != nil {
			batch = append(batch, cloneRequestTraceWorkerValue(trace))
		}
	}
	if len(batch) == 0 {
		return
	}

	attemptLimit := requestTraceMaxAttempts
	if s.isShutdownInProgress() {
		attemptLimit = 1
	}

	var lastAttempt int
	for attempt := 1; attempt <= attemptLimit; attempt++ {
		lastAttempt = attempt
		ctx, cancel := context.WithTimeout(context.Background(), requestTraceFlushTimeout)
		finish := s.traceOperation.Begin()
		err := s.store.BatchAddRequestTraces(ctx, batch)
		cancel()
		finish(err)
		if err == nil {
			s.traceWritten.Add(uint64(len(batch)))
			return
		}
		if attempt >= attemptLimit || s.isShutdownInProgress() {
			break
		}

		log.Printf(
			"[WARN] 请求历史批量写入失败，准备重试 (attempt=%d/%d, batch_size=%d)",
			attempt,
			attemptLimit,
			len(batch),
		)
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
			lastAttempt = attempt
			goto failed
		}
	}

failed:
	s.traceWriteFailures.Add(uint64(len(batch)))
	log.Printf(
		"[ERROR] 请求历史批量写入最终失败 (attempts=%d, batch_size=%d)",
		lastAttempt,
		len(batch),
	)
}

func cloneRequestTraceWorkerValue(trace *model.RequestTrace) *model.RequestTrace {
	if trace == nil {
		return nil
	}
	clone := *trace
	clone.Attempts = append([]model.RequestAttempt(nil), trace.Attempts...)
	return &clone
}
