package model

import (
	"encoding/json"
	"testing"
)

func TestRequestTraceJSONContractUsesSnakeCase(t *testing.T) {
	trace := RequestTrace{
		RequestID:            "pf_req_0123456789abcdef0123456789abcdef",
		StartedAt:            1_700_000_000_123,
		AuthTokenID:          7,
		Model:                "gpt-5",
		ClientProtocol:       "openai",
		Transport:            "http",
		IsStreaming:          true,
		FinalStatus:          200,
		Outcome:              "success",
		Duration:             1.25,
		WaitDuration:         0.05,
		AttemptCount:         2,
		UpstreamAttemptCount: 1,
		UpstreamSuccessCount: 1,
		Truncated:            false,
		Attempts: []RequestAttempt{{
			Number:            1,
			StartedAt:         1_700_000_000_123,
			Duration:          1.25,
			FirstByteTime:     0.1,
			ChannelID:         42,
			ChannelName:       "primary",
			KeyIndex:          0,
			URLIndex:          1,
			Endpoint:          "https://api.example.com",
			ActualModel:       "gpt-5-upstream",
			UpstreamProtocol:  "openai",
			Transport:         "http",
			StatusCode:        200,
			Outcome:           "success",
			Upstream:          true,
			ResponseCommitted: true,
			Reason:            "",
			Retry:             "none",
			Cooldown:          "none",
			CooldownUntil:     0,
		}},
	}

	data, err := json.Marshal(trace)
	if err != nil {
		t.Fatalf("marshal request trace: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal request trace JSON: %v", err)
	}

	for _, key := range []string{
		"request_id", "started_at", "auth_token_id", "model", "client_protocol",
		"transport", "is_streaming", "final_status", "outcome", "duration",
		"wait_duration", "attempt_count", "upstream_attempt_count",
		"upstream_success_count", "truncated", "attempts",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("request trace JSON missing %q: %s", key, data)
		}
	}

	attempts, ok := got["attempts"].([]any)
	if !ok || len(attempts) != 1 {
		t.Fatalf("attempts JSON = %#v, want one object", got["attempts"])
	}
	attempt, ok := attempts[0].(map[string]any)
	if !ok {
		t.Fatalf("attempt JSON = %#v, want object", attempts[0])
	}
	for _, key := range []string{
		"number", "started_at", "duration", "first_byte_time", "channel_id",
		"channel_name", "key_index", "url_index", "endpoint", "actual_model",
		"upstream_protocol", "transport", "status_code", "outcome", "upstream",
		"response_committed", "reason", "retry", "cooldown", "cooldown_until",
	} {
		if _, ok := attempt[key]; !ok {
			t.Errorf("request attempt JSON missing %q: %s", key, data)
		}
	}
}

func TestRequestTraceStatsJSONUsesSnakeCase(t *testing.T) {
	data, err := json.Marshal(RequestTraceStats{
		Requests:          1,
		Successes:         2,
		Errors:            3,
		Canceled:          4,
		UpstreamAttempts:  5,
		UpstreamSuccesses: 6,
	})
	if err != nil {
		t.Fatalf("marshal request trace stats: %v", err)
	}
	var got map[string]int64
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal request trace stats: %v", err)
	}
	for _, key := range []string{
		"requests", "successes", "errors", "canceled", "upstream_attempts", "upstream_successes",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("request trace stats JSON missing %q: %s", key, data)
		}
	}
}
