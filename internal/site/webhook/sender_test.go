package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/site/provider"
)

func TestSenderRetriesTransientFailuresAndPostsJSON(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected request: %s %#v", r.Method, r.Header)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["event_type"] != "test" {
			t.Fatalf("payload=%#v err=%v", payload, err)
		}
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sender := Sender{Clients: provider.ClientFactory{AllowPrivate: true}, Timeout: time.Second}
	attempts, err := sender.Send(context.Background(), server.URL, map[string]any{"event_type": "test"})
	if err != nil || attempts != 3 || calls.Load() != 3 {
		t.Fatalf("attempts=%d calls=%d err=%v", attempts, calls.Load(), err)
	}
}

func TestSenderDoesNotRetryPermanentFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	sender := Sender{Clients: provider.ClientFactory{AllowPrivate: true}, Timeout: time.Second}
	attempts, err := sender.Send(context.Background(), server.URL, map[string]any{"event_type": "test"})
	if err == nil || attempts != 1 || calls.Load() != 1 {
		t.Fatalf("attempts=%d calls=%d err=%v", attempts, calls.Load(), err)
	}
}
