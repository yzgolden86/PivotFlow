package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func TestRequestCacheTraceSetterStoresOnlyFixedMetadata(t *testing.T) {
	var nilTrace *requestTrace
	nilTrace.setCache("hit", "reused")

	trace := &requestTrace{}
	trace.setCache("hit", "reused")
	trace.mu.Lock()
	status, reason := trace.entry.CacheStatus, trace.entry.CacheReason
	trace.mu.Unlock()
	if status != "hit" || reason != "reused" {
		t.Fatalf("cache metadata=(%q,%q), want hit/reused", status, reason)
	}

	trace.setCache("client_secret_status", "reused")
	trace.setCache("hit", "client_secret_reason")
	trace.mu.Lock()
	status, reason = trace.entry.CacheStatus, trace.entry.CacheReason
	trace.mu.Unlock()
	if status != "hit" || reason != "reused" {
		t.Fatalf("invalid metadata replaced safe values: (%q,%q)", status, reason)
	}
}

func TestRequestCacheTraceSummaryAndTokenProjectionCarryOnlyCacheMetadata(t *testing.T) {
	trace := sensitiveRequestTraceForAPI()
	trace.CacheStatus = "hit"
	trace.CacheReason = "reused"

	summary := projectRequestTraceSummary(trace)
	tokenDetail := projectTokenRequestTrace(trace)
	if summary.CacheStatus != "hit" || summary.CacheReason != "reused" {
		t.Fatalf("admin summary cache metadata=(%q,%q)", summary.CacheStatus, summary.CacheReason)
	}
	if tokenDetail.CacheStatus != "hit" || tokenDetail.CacheReason != "reused" {
		t.Fatalf("token summary cache metadata=(%q,%q)", tokenDetail.CacheStatus, tokenDetail.CacheReason)
	}

	encoded, err := json.Marshal(tokenDetail)
	if err != nil {
		t.Fatalf("marshal token detail: %v", err)
	}
	body := string(encoded)
	for _, forbidden := range []string{
		"private-channel-name",
		"private-origin.example.com",
		"private-upstream-model",
		"request_body",
		"cache_key",
		"source_request_id",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("token projection exposed %q: %s", forbidden, body)
		}
	}
}

func TestRequestCacheTraceSnapshotPreservesZeroAttemptHit(t *testing.T) {
	trace := &requestTrace{
		entry: model.RequestTrace{
			CacheStatus: "hit",
			CacheReason: "reused",
			Attempts:    []model.RequestAttempt{},
		},
	}
	snapshot := trace.snapshot(200, false)
	if snapshot.Outcome != "success" || snapshot.CacheStatus != "hit" || snapshot.CacheReason != "reused" {
		t.Fatalf("snapshot cache summary=%+v", snapshot)
	}
	if snapshot.AttemptCount != 0 || snapshot.UpstreamAttemptCount != 0 || len(snapshot.Attempts) != 0 {
		t.Fatalf("snapshot invented cache-hit activity: %+v", snapshot)
	}
}
