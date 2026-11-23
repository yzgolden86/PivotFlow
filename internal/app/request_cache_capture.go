package app

import (
	"bytes"
	"io"
	"net/http"
	"sync"
)

// Capture forwards first and retains only successfully written bytes, subject
// to both per-entry and shared capture budgets. It never buffers a stream.
type requestCacheCapture struct {
	http.ResponseWriter
	mu     sync.Mutex
	cache  *requestCache
	ticket *requestCacheTicket
	body   []byte
	header http.Header
	status int
	reason string
	closed bool
}

func newRequestCacheCapture(writer http.ResponseWriter, cache *requestCache, ticket *requestCacheTicket) *requestCacheCapture {
	cache.mu.Lock()
	cache.metrics.Captures++
	cache.mu.Unlock()
	return &requestCacheCapture{ResponseWriter: writer, cache: cache, ticket: ticket}
}

func (w *requestCacheCapture) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *requestCacheCapture) WriteHeader(status int) {
	w.mu.Lock()
	if w.status == 0 && status >= 200 {
		w.status = status
		w.header = make(http.Header)
		budget := 0
		for _, key := range []string{"Content-Type", "Content-Encoding", "Cache-Control", "Pragma", "Vary"} {
			values := w.ResponseWriter.Header().Values(key)
			if len(values) > 16 {
				w.blockLocked("unsafe_response")
				break
			}
			for _, value := range values {
				budget += len(value)
				if budget > 4096 {
					w.blockLocked("unsafe_response")
					break
				}
				w.header.Add(key, value)
			}
			if w.reason != "" {
				break
			}
		}
		if len(w.ResponseWriter.Header().Values("Set-Cookie")) > 0 {
			w.header.Set("Set-Cookie", "") // marker only; never retain a Cookie value
		}
	}
	w.mu.Unlock()
	w.ResponseWriter.WriteHeader(status)
}

func (w *requestCacheCapture) Write(data []byte) (int, error) {
	w.mu.Lock()
	needsHeader := w.status == 0
	w.mu.Unlock()
	if needsHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		w.blockLocked("write_failed")
		return n, err
	}
	if w.closed || w.reason != "" || n == 0 {
		return n, err
	}
	if len(w.body)+n > w.cache.config.MaxEntryBytes {
		w.blockLocked("response_too_large")
		return n, err
	}
	w.cache.mu.Lock()
	current := w.cache.ticketCurrentLocked(w.ticket)
	withinBudget := w.cache.metrics.CaptureBytes+n <= w.cache.config.MaxBytes
	if current && withinBudget {
		w.cache.metrics.CaptureBytes += n
	}
	w.cache.mu.Unlock()
	if !current {
		w.blockLocked("invalidated")
	} else if !withinBudget {
		w.blockLocked("capture_budget")
	} else {
		w.body = append(w.body, data[:n]...)
	}
	return n, err
}

func (w *requestCacheCapture) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *requestCacheCapture) block(reason string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blockLocked(reason)
}

func (w *requestCacheCapture) blockLocked(reason string) {
	if w.reason == "" {
		w.reason = reason
	}
	if len(w.body) > 0 {
		w.cache.mu.Lock()
		w.cache.metrics.CaptureBytes -= len(w.body)
		w.cache.mu.Unlock()
		w.body = nil
	}
}

func (w *requestCacheCapture) snapshot() (int, http.Header, []byte, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status, w.header.Clone(), bytes.Clone(w.body), w.reason
}

func (w *requestCacheCapture) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	w.cache.mu.Lock()
	w.cache.metrics.Captures--
	w.cache.metrics.CaptureBytes -= len(w.body)
	w.cache.mu.Unlock()
	w.body = nil
}
