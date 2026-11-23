// Package monitoring records bounded, process-local observations of background
// operations without retaining error text or performing health probes.
package monitoring

import (
	"context"
	"errors"
	"sync"
	"time"
)

// OperationStats describes observed work. Failed counts operation attempts,
// not lost records. RecoveredAt marks a later successful operation, not repair
// of data that an earlier operation failed to save.
type OperationStats struct {
	Active              int    `json:"active"`
	Attempts            uint64 `json:"attempts"`
	Succeeded           uint64 `json:"succeeded"`
	Failed              uint64 `json:"failed"`
	Cancelled           uint64 `json:"cancelled"`
	Failing             bool   `json:"failing"`
	ConsecutiveFailures uint64 `json:"consecutive_failures"`
	LastResult          string `json:"last_result"`
	LastAttemptAt       int64  `json:"last_attempt_at"`
	LastFinishedAt      int64  `json:"last_finished_at"`
	LastSuccessAt       int64  `json:"last_success_at"`
	LastFailureAt       int64  `json:"last_failure_at"`
	RecoveredAt         int64  `json:"recovered_at"`
	LastErrorCode       string `json:"last_error_code"`
}

// OperationMonitor is safe for concurrent use and needs no initialization.
type OperationMonitor struct {
	mu    sync.Mutex
	stats OperationStats
}

// Begin starts a real operation and returns its idempotent completion callback.
// Cancellation is recorded separately and does not clear an unresolved failure.
func (m *OperationMonitor) Begin() func(error) {
	m.mu.Lock()
	m.stats.Active++
	m.stats.Attempts++
	m.stats.LastAttemptAt = time.Now().UnixMilli()
	m.mu.Unlock()
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			m.finish(err)
		})
	}
}

func (m *OperationMonitor) finish(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UnixMilli()
	m.stats.Active--
	m.stats.LastFinishedAt = now
	switch {
	case err == nil:
		m.stats.Succeeded++
		m.stats.LastResult = "success"
		m.stats.LastSuccessAt = now
		if m.stats.Failing {
			m.stats.RecoveredAt = now
		}
		m.stats.Failing = false
		m.stats.ConsecutiveFailures = 0
	case IsCancellation(err):
		m.stats.Cancelled++
		m.stats.LastResult = "cancelled"
	default:
		m.stats.Failed++
		m.stats.LastResult = "error"
		m.stats.Failing = true
		m.stats.ConsecutiveFailures++
		m.stats.LastFailureAt = now
		m.stats.LastErrorCode = "operation_failed"
		if errors.Is(err, context.DeadlineExceeded) {
			m.stats.LastErrorCode = "timeout"
		}
	}
}

// IsCancellation reports cancellation only when every non-nil leaf of a
// wrapped or joined error is a cancellation. A real storage error must remain
// observable even when another part of the same operation was cancelled.
func IsCancellation(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		found := false
		for _, child := range joined.Unwrap() {
			if child == nil {
				continue
			}
			found = true
			if !IsCancellation(child) {
				return false
			}
		}
		return found
	}
	if child := errors.Unwrap(err); child != nil {
		return IsCancellation(child)
	}
	return errors.Is(err, context.Canceled)
}

// Snapshot copies the complete observation under one lock.
func (m *OperationMonitor) Snapshot() OperationStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stats
}
