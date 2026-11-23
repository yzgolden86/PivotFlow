package model

// RequestTrace is the durable summary of one gateway request. Attempts is
// stored as JSON in the same row so summary counters and the detail timeline
// cannot be committed independently.
type RequestTrace struct {
	RequestID            string           `json:"request_id"`
	StartedAt            int64            `json:"started_at"`
	AuthTokenID          int64            `json:"auth_token_id"`
	Model                string           `json:"model"`
	ClientProtocol       string           `json:"client_protocol"`
	Transport            string           `json:"transport"`
	IsStreaming          bool             `json:"is_streaming"`
	FinalStatus          int              `json:"final_status"`
	Outcome              string           `json:"outcome"`
	Duration             float64          `json:"duration"`
	WaitDuration         float64          `json:"wait_duration"`
	AttemptCount         int              `json:"attempt_count"`
	UpstreamAttemptCount int              `json:"upstream_attempt_count"`
	UpstreamSuccessCount int              `json:"upstream_success_count"`
	Truncated            bool             `json:"truncated"`
	CacheStatus          string           `json:"cache_status,omitempty"`
	CacheReason          string           `json:"cache_reason,omitempty"`
	Attempts             []RequestAttempt `json:"attempts"`
}

// RequestAttempt is one timeline event. Endpoint contains only the configured
// origin and Reason is a fixed diagnostic code rather than raw upstream text.
type RequestAttempt struct {
	Number            int     `json:"number"`
	StartedAt         int64   `json:"started_at"`
	Duration          float64 `json:"duration"`
	FirstByteTime     float64 `json:"first_byte_time"`
	ChannelID         int64   `json:"channel_id"`
	ChannelName       string  `json:"channel_name"`
	KeyIndex          int     `json:"key_index"`
	URLIndex          int     `json:"url_index"`
	Endpoint          string  `json:"endpoint"`
	ActualModel       string  `json:"actual_model"`
	UpstreamProtocol  string  `json:"upstream_protocol"`
	Transport         string  `json:"transport"`
	StatusCode        int     `json:"status_code"`
	Outcome           string  `json:"outcome"`
	Upstream          bool    `json:"upstream"`
	ResponseCommitted bool    `json:"response_committed"`
	Reason            string  `json:"reason"`
	Retry             string  `json:"retry"`
	Cooldown          string  `json:"cooldown"`
	CooldownUntil     int64   `json:"cooldown_until"`
}

// RequestTraceFilter narrows request summaries. AuthTokenID is also supplied
// separately to list/stat methods so token-owned reads can be enforced.
type RequestTraceFilter struct {
	AuthTokenID    *int64 `json:"auth_token_id,omitempty"`
	Search         string `json:"search,omitempty"`
	Outcome        string `json:"outcome,omitempty"`
	Model          string `json:"model,omitempty"`
	ClientProtocol string `json:"client_protocol,omitempty"`
}

// RequestTraceStats contains summary-meter counters, never probe logs.
type RequestTraceStats struct {
	Requests          int64 `json:"requests"`
	Successes         int64 `json:"successes"`
	Errors            int64 `json:"errors"`
	Canceled          int64 `json:"canceled"`
	UpstreamAttempts  int64 `json:"upstream_attempts"`
	UpstreamSuccesses int64 `json:"upstream_successes"`
}
