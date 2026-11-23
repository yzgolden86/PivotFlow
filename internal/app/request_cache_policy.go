package app

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"hash"
	"math"
	"mime"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/protocol"
)

const requestCacheHeader = "X-PivotFlow-Cache"

// Cache eligibility is deliberately narrower than the proxy contract. A field
// added by an SDK remains forwardable but cannot silently acquire cache semantics.
func classifyRequestCacheRequest(req *http.Request, body []byte, client protocol.Protocol, streaming bool) (mode, reason string) {
	mode = strings.ToLower(strings.TrimSpace(req.Header.Get(requestCacheHeader)))
	switch mode {
	case "":
		return "bypass", "not_opted_in"
	case "bypass":
		return "bypass", "client_bypass"
	case "use", "refresh":
	default:
		return "bypass", "invalid_mode"
	}
	if len(req.Header.Values(requestCacheHeader)) != 1 {
		return "bypass", "invalid_mode"
	}
	if cacheHeaderDirective(req.Header, "Cache-Control", "no-store") {
		return "bypass", "request_no_store"
	}
	if streaming {
		return "bypass", "streaming"
	}
	supportedEndpoint := req.Method == http.MethodPost &&
		(client == protocol.OpenAI && req.URL.Path == "/v1/chat/completions" ||
			client == protocol.Anthropic && req.URL.Path == "/v1/messages")
	if !supportedEndpoint {
		return "bypass", "unsupported_endpoint"
	}
	if req.URL.RawQuery != "" {
		return "bypass", "request_query"
	}
	contentType, _, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || req.Header.Get("Content-Encoding") != "" {
		return "bypass", "request_encoding"
	}
	for name := range req.Header {
		name = strings.ToLower(strings.ReplaceAll(name, "_", "-"))
		if name == "cookie" || name == "cookie2" || strings.Contains(name, "session") || strings.Contains(name, "conversation") || strings.Contains(name, "thread") || strings.Contains(name, "idempotency") {
			return "bypass", "tools_or_state"
		}
	}
	root, ok := requestCacheJSONObject(body)
	if !ok {
		return "bypass", "unsupported_request"
	}
	for _, field := range []string{"tools", "tool_choice", "functions", "function_call", "parallel_tool_calls", "store", "previous_response_id", "conversation", "session_id", "prompt_cache_key", "thinking", "reasoning", "output_config", "background", "web_search_options", "audio", "modalities"} {
		if _, exists := root[field]; exists {
			return "bypass", "tools_or_state"
		}
	}
	if temperature, ok := cacheJSONNumber(root["temperature"]); !ok || temperature != 0 {
		return "bypass", "nondeterministic"
	}
	if n, exists := root["n"]; exists {
		if choices, ok := cacheJSONNumber(n); !ok || choices != 1 {
			return "bypass", "nondeterministic"
		}
	}
	if stream, exists := root["stream"]; exists && stream != false {
		return "bypass", "streaming"
	}
	allowed := []string{"model", "messages", "temperature", "stream", "max_tokens", "top_p", "stop"}
	if client == protocol.OpenAI {
		allowed = append(allowed, "max_completion_tokens", "n", "seed", "frequency_penalty", "presence_penalty", "response_format", "service_tier", "user")
	} else {
		allowed = []string{"model", "messages", "system", "temperature", "stream", "max_tokens", "top_p", "top_k", "stop_sequences"}
	}
	if !cacheOnlyKeys(root, allowed...) || !cacheNonemptyString(root["model"]) {
		return "bypass", "unsupported_request"
	}
	messages, ok := root["messages"].([]any)
	if !ok || len(messages) == 0 {
		return "bypass", "unsupported_request"
	}
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			return "bypass", "unsupported_request"
		}
		role, _ := message["role"].(string)
		roles := []string{"user", "assistant"}
		messageKeys := []string{"role", "content"}
		if client == protocol.OpenAI {
			roles = append(roles, "system", "developer")
			messageKeys = append(messageKeys, "name")
		}
		if !slices.Contains(roles, role) || !cacheOnlyKeys(message, messageKeys...) || !cacheTextContent(message["content"], false) {
			return "bypass", "tools_or_state"
		}
		if name, exists := message["name"]; exists && !cacheNonemptyString(name) {
			return "bypass", "unsupported_request"
		}
	}
	if system, exists := root["system"]; exists && !cacheTextContent(system, false) {
		return "bypass", "tools_or_state"
	}
	if !cacheRequestOptionsValid(root, client) {
		return "bypass", "unsupported_request"
	}
	if cacheHeaderDirective(req.Header, "Cache-Control", "no-cache") || cacheHeaderDirective(req.Header, "Pragma", "no-cache") {
		return "refresh", "request_no_cache"
	}
	if mode == "refresh" {
		return mode, "client_refresh"
	}
	return mode, ""
}

func cacheRequestOptionsValid(root map[string]any, client protocol.Protocol) bool {
	for _, key := range []string{"max_tokens", "max_completion_tokens", "top_k", "seed"} {
		if value, exists := root[key]; exists {
			number, ok := value.(json.Number)
			if !ok {
				return false
			}
			n, err := number.Int64()
			if err != nil || (key != "seed" && n <= 0) {
				return false
			}
		}
	}
	if client == protocol.Anthropic {
		if _, exists := root["max_tokens"]; !exists {
			return false
		}
	}
	for _, key := range []string{"top_p", "frequency_penalty", "presence_penalty"} {
		if value, exists := root[key]; exists {
			n, ok := cacheJSONNumber(value)
			if !ok || (key == "top_p" && (n < 0 || n > 1)) || (key != "top_p" && (n < -2 || n > 2)) {
				return false
			}
		}
	}
	for _, key := range []string{"stop", "stop_sequences"} {
		if value, exists := root[key]; exists && !cacheStringOrStrings(value) {
			return false
		}
	}
	if value, exists := root["user"]; exists {
		if _, ok := value.(string); !ok {
			return false
		}
	}
	if value, exists := root["service_tier"]; exists && !slices.Contains([]any{"auto", "default", "priority", "flex"}, value) {
		return false
	}
	if value, exists := root["response_format"]; exists {
		format, ok := value.(map[string]any)
		if !ok || !cacheOnlyKeys(format, "type") || !slices.Contains([]any{"text", "json_object"}, format["type"]) {
			return false
		}
	}
	return true
}

func requestCacheResponseReason(client protocol.Protocol, status int, header http.Header, body []byte) string {
	if status != http.StatusOK {
		return "upstream_error"
	}
	if cacheHeaderDirective(header, "Cache-Control", "no-store") || cacheHeaderDirective(header, "Cache-Control", "no-cache") ||
		cacheHeaderDirective(header, "Pragma", "no-cache") || cacheHeaderDirective(header, "Vary", "*") || len(header.Values("Set-Cookie")) > 0 {
		return "response_no_store"
	}
	contentType, _, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil || contentType != "application/json" || len(header.Get("Content-Type")) > 128 || header.Get("Content-Encoding") != "" {
		return "unsafe_response"
	}
	root, ok := requestCacheJSONObject(body)
	if !ok || cacheContainsToolMetadata(root) {
		return "unsafe_response"
	}
	if client == protocol.OpenAI {
		if !cacheOnlyKeys(root, "id", "object", "created", "model", "choices", "usage", "system_fingerprint", "service_tier") || root["object"] != "chat.completion" {
			return "unsafe_response"
		}
		choices, ok := root["choices"].([]any)
		if !ok || len(choices) != 1 {
			return "unsafe_response"
		}
		choice, ok := choices[0].(map[string]any)
		if !ok || !cacheOnlyKeys(choice, "index", "message", "finish_reason", "logprobs") || choice["finish_reason"] != "stop" {
			return "unsafe_response"
		}
		message, ok := choice["message"].(map[string]any)
		if !ok || !cacheOnlyKeys(message, "role", "content", "refusal") || message["role"] != "assistant" || message["refusal"] != nil || !cacheNonemptyString(message["content"]) {
			return "unsafe_response"
		}
		return ""
	}
	if client == protocol.Anthropic && cacheOnlyKeys(root, "id", "type", "role", "model", "content", "stop_reason", "stop_sequence", "usage") &&
		root["type"] == "message" && root["role"] == "assistant" && slices.Contains([]any{"end_turn", "stop_sequence"}, root["stop_reason"]) && cacheTextContent(root["content"], true) {
		return ""
	}
	return "unsafe_response"
}

func requestCacheJSONObject(body []byte) (map[string]any, bool) {
	// Reuse the audited UTF-8, surrogate, duplicate-key and depth-limited JSON
	// decoder. Its feature-specific error is discarded; cache diagnostics use
	// fixed codes and never include a body fragment.
	decoded, err := decodeBrowserImportJSON(body)
	root, ok := decoded.(map[string]any)
	return root, err == nil && ok
}

func cacheOnlyKeys(object map[string]any, allowed ...string) bool {
	for key := range object {
		if !slices.Contains(allowed, key) {
			return false
		}
	}
	return true
}

func cacheNonemptyString(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}

func cacheTextContent(value any, nonempty bool) bool {
	if text, ok := value.(string); ok {
		return !nonempty || strings.TrimSpace(text) != ""
	}
	blocks, ok := value.([]any)
	if !ok || len(blocks) == 0 {
		return false
	}
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok || !cacheOnlyKeys(block, "type", "text") || block["type"] != "text" {
			return false
		}
		text, ok := block["text"].(string)
		if !ok || (nonempty && strings.TrimSpace(text) == "") {
			return false
		}
	}
	return true
}

func cacheStringOrStrings(value any) bool {
	if _, ok := value.(string); ok {
		return true
	}
	values, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range values {
		if _, ok := item.(string); !ok {
			return false
		}
	}
	return true
}

func cacheJSONNumber(value any) (float64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := number.Float64()
	return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
}

func cacheContainsToolMetadata(value any) bool {
	switch object := value.(type) {
	case map[string]any:
		for key, child := range object {
			if strings.Contains(strings.ToLower(key), "tool") || strings.Contains(strings.ToLower(key), "function") || cacheContainsToolMetadata(child) {
				return true
			}
		}
	case []any:
		for _, child := range object {
			if cacheContainsToolMetadata(child) {
				return true
			}
		}
	}
	return false
}

func cacheHeaderDirective(header http.Header, name, directive string) bool {
	for _, value := range header.Values(name) {
		for _, item := range strings.Split(value, ",") {
			key, _, _ := strings.Cut(strings.TrimSpace(item), "=")
			if strings.EqualFold(key, directive) {
				return true
			}
		}
	}
	return false
}

func requestCacheKey(req *http.Request, body []byte, client protocol.Protocol, tokenHash string, tokenID int64) [32]byte {
	digest := sha256.New()
	for _, value := range []string{"pivotflow-response-cache-v1", tokenHash, strconv.FormatInt(tokenID, 10), string(client), req.Method, req.URL.EscapedPath()} {
		cacheHashFrame(digest, []byte(value))
	}
	cacheHashFrame(digest, body)
	names := make([]string, 0, len(req.Header))
	for name := range req.Header {
		if strings.EqualFold(name, requestCacheHeader) || strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "X-Api-Key") ||
			strings.EqualFold(name, "X-Goog-Api-Key") || strings.EqualFold(name, "Cache-Control") || strings.EqualFold(name, "Pragma") ||
			strings.EqualFold(name, "Content-Length") || shouldSkipHopByHopHeader(name, connectionHeaderTokens(req.Header)) {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		cacheHashFrame(digest, []byte(strings.ToLower(name)))
		for _, value := range req.Header[name] {
			cacheHashFrame(digest, []byte(value))
		}
		cacheHashFrame(digest, nil)
	}
	return [32]byte(digest.Sum(nil))
}

func cacheHashFrame(digest hash.Hash, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = digest.Write(size[:])
	_, _ = digest.Write(value)
}

func requestCacheChannelFingerprint(cfg *model.Config) ([32]byte, bool) {
	if cfg == nil {
		return [32]byte{}, false
	}
	// Keep configured routing/request fields, but exclude observations that
	// change after ordinary cooldown recovery or snapshot refresh. Clone first:
	// candidate Config pointers belong to the immutable shared channel cache.
	stable := cfg.Clone()
	stable.CooldownUntil, stable.CooldownDurationMs, stable.KeyCount = 0, 0, 0
	stable.CreatedAt, stable.UpdatedAt = model.JSONTime{}, model.JSONTime{}
	digest := sha256.New()
	if err := json.NewEncoder(digest).Encode(stable); err != nil {
		return [32]byte{}, false
	}
	return [32]byte(digest.Sum(nil)), true
}
