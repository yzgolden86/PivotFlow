package model

// ValidRequestCacheStatus reports whether status belongs to the durable
// request-cache status vocabulary.
func ValidRequestCacheStatus(status string) bool {
	switch status {
	case "", "hit", "miss", "refresh", "bypass":
		return true
	default:
		return false
	}
}

// ValidRequestCacheReason reports whether reason belongs to the durable
// request-cache reason vocabulary.
func ValidRequestCacheReason(reason string) bool {
	switch reason {
	case "",
		"disabled",
		"not_opted_in",
		"client_bypass",
		"invalid_mode",
		"request_no_store",
		"request_no_cache",
		"request_query",
		"request_encoding",
		"streaming",
		"unsupported_endpoint",
		"nondeterministic",
		"tools_or_state",
		"unsupported_request",
		"no_identity",
		"route_unavailable",
		"channel_changed",
		"custom_rules",
		"protocol_transform",
		"empty",
		"expired",
		"client_refresh",
		"unsafe_response",
		"response_no_store",
		"response_too_large",
		"capture_budget",
		"invalidated",
		"upstream_error",
		"canceled",
		"write_failed",
		"reused",
		"stored":
		return true
	default:
		return false
	}
}
