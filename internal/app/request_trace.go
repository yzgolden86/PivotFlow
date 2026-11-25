package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/cooldown"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/util"
)

const requestIDHeader = "X-PivotFlow-Request-Id"
const maxRequestTraceAttempts = 128

type requestTraceContextKey struct{}

type requestTraceAttemptScope struct {
	trace      *requestTrace
	generation uint64
}

// requestTrace collects metadata only. The mutex also covers reconnects in the
// native websocket reader. Finished snapshots are immutable once queued.
type requestTrace struct {
	mu           sync.Mutex
	entry        model.RequestTrace
	started      time.Time
	attemptStart time.Time
	current      *model.RequestAttempt
	generation   uint64
}

func newRequestTrace(start time.Time, tokenID int64, transport string) *requestTrace {
	var id [16]byte
	// crypto/rand.Read cannot fail on supported Go platforms (fatal on failure).
	_, _ = rand.Read(id[:])
	return &requestTrace{started: start, entry: model.RequestTrace{
		RequestID: "pf_req_" + hex.EncodeToString(id[:]), StartedAt: start.UnixMilli(),
		AuthTokenID: tokenID, Transport: transport, Attempts: make([]model.RequestAttempt, 0),
	}}
}

func (t *requestTrace) id() string {
	if t == nil {
		return ""
	}
	return t.entry.RequestID // immutable
}

func traceText(value string) string {
	// Names are labels, not raw request/response bodies. Bound even malformed input.
	runes := []rune(strings.ReplaceAll(value, "\x00", ""))
	return string(runes[:min(len(runes), 191)])
}

func traceEndpoint(raw string) string {
	parsed, err := url.Parse(strings.TrimSuffix(raw, "#"))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	return traceText(parsed.Scheme + "://" + parsed.Host)
}

func (t *requestTrace) setRequest(modelName, protocolName string, streaming bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.entry.Model = traceText(modelName)
	t.entry.ClientProtocol = protocolName
	t.entry.IsStreaming = streaming
	t.mu.Unlock()
}

func (t *requestTrace) setWait(d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.entry.WaitDuration = d.Seconds()
	t.mu.Unlock()
}

func (t *requestTrace) setCache(status, reason string) {
	if t == nil || !model.ValidRequestCacheStatus(status) || !model.ValidRequestCacheReason(reason) {
		return
	}
	t.mu.Lock()
	t.entry.CacheStatus = status
	t.entry.CacheReason = reason
	t.mu.Unlock()
}

func (t *requestTrace) begin(cfg *model.Config, keyIndex int, baseURL, actualModel, upstreamProtocol string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	urlIndex := -1
	for index, candidate := range cfg.GetURLs() {
		if candidate == baseURL {
			urlIndex = index
			break
		}
	}
	if !t.entry.Truncated && len(t.entry.Attempts) > 0 {
		previous := &t.entry.Attempts[len(t.entry.Attempts)-1]
		switch {
		case previous.ChannelID != cfg.ID:
			previous.Retry = "next_channel"
		case previous.KeyIndex != keyIndex:
			previous.Retry = "next_key"
		case previous.URLIndex != urlIndex:
			previous.Retry = "next_url"
		case previous.Retry != "refresh_token":
			previous.Retry = "compatibility"
		}
	}
	t.attemptStart = time.Now()
	t.generation++
	t.current = &model.RequestAttempt{
		Number: t.entry.AttemptCount + 1, StartedAt: t.attemptStart.UnixMilli(),
		ChannelID: cfg.ID, ChannelName: traceText(cfg.Name), KeyIndex: keyIndex, URLIndex: urlIndex,
		Endpoint: traceEndpoint(baseURL), ActualModel: traceText(actualModel),
		UpstreamProtocol: upstreamProtocol, Transport: "http", Retry: "none", Cooldown: "none",
	}
}

func (t *requestTrace) number() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current != nil {
		return t.current.Number
	}
	return t.entry.AttemptCount
}

func (t *requestTrace) attemptContext(ctx context.Context) context.Context {
	if t == nil {
		return ctx
	}
	t.mu.Lock()
	scope := requestTraceAttemptScope{trace: t, generation: t.generation}
	t.mu.Unlock()
	return context.WithValue(ctx, requestTraceContextKey{}, scope)
}

func markTraceUpstream(ctx context.Context, transport string) {
	scope, ok := ctx.Value(requestTraceContextKey{}).(requestTraceAttemptScope)
	if !ok {
		return
	}
	trace := scope.trace
	trace.mu.Lock()
	if trace.current != nil && trace.generation == scope.generation {
		trace.current.Upstream = true
		trace.current.Transport = transport
	}
	trace.mu.Unlock()
}

func markTraceFirstByte(ctx context.Context) {
	scope, ok := ctx.Value(requestTraceContextKey{}).(requestTraceAttemptScope)
	if !ok {
		return
	}
	trace := scope.trace
	trace.mu.Lock()
	if trace.current != nil && trace.generation == scope.generation && trace.current.FirstByteTime == 0 {
		trace.current.FirstByteTime = time.Since(trace.attemptStart).Seconds()
	}
	trace.mu.Unlock()
}

// retry records a real compatibility/transport retry which does not enter the
// ordinary health logs. No error bodies, credentials or headers are retained.
func (t *requestTrace) retry(status int, outcome, reason string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.retryLocked(status, outcome, reason)
}

func (t *requestTrace) retryLocked(status int, outcome, reason string) {
	if t.current == nil {
		return
	}
	next := *t.current
	t.current.StatusCode = status
	t.current.Outcome = outcome
	t.current.Reason = reason
	t.current.Retry = "compatibility"
	t.appendLocked()
	t.attemptStart = time.Now()
	next.Number = t.entry.AttemptCount + 1
	next.StartedAt = t.attemptStart.UnixMilli()
	next.Upstream = false
	next.Duration, next.FirstByteTime = 0, 0
	next.StatusCode, next.Outcome, next.Reason = 0, "", ""
	next.Retry, next.Cooldown, next.CooldownUntil = "none", "none", 0
	next.ResponseCommitted = false
	t.current = &next
}

func retryTraceFromContext(ctx context.Context, status int, outcome, reason string) {
	scope, ok := ctx.Value(requestTraceContextKey{}).(requestTraceAttemptScope)
	if !ok {
		return
	}
	trace := scope.trace
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if trace.generation == scope.generation {
		trace.retryLocked(status, outcome, reason)
	}
}

func (t *requestTrace) appendLocked() {
	a := t.current
	if a == nil {
		return
	}
	a.Duration = time.Since(t.attemptStart).Seconds()
	t.entry.AttemptCount++
	if a.Upstream {
		t.entry.ActualModel = a.ActualModel
		t.entry.ChannelID, t.entry.ChannelName = a.ChannelID, a.ChannelName
		t.entry.UpstreamAttemptCount++
		if a.Outcome == "success" {
			t.entry.UpstreamSuccessCount++
		}
	}
	if len(t.entry.Attempts) < maxRequestTraceAttempts {
		t.entry.Attempts = append(t.entry.Attempts, *a)
	} else {
		t.entry.Truncated = true
	}
	t.current = nil
}

func traceDecision(a *model.RequestAttempt, decision cooldown.Decision) {
	a.Retry = map[cooldown.RetryStrategy]string{
		cooldown.RetryNone: "none", cooldown.RetryNextKey: "next_key", cooldown.RetryNextURL: "next_url",
		cooldown.RetryNextChannel: "next_channel", cooldown.RetryRefreshToken: "refresh_token",
	}[decision.Retry]
	switch decision.Effect {
	case cooldown.EffectCoolKey:
		a.Cooldown = "key"
		if decision.HasKeyCooldownUntil {
			a.CooldownUntil = decision.KeyCooldownUntil.UnixMilli()
		}
	case cooldown.EffectCoolModel:
		a.Cooldown = "model"
		if decision.HasModelCooldownUntil {
			a.CooldownUntil = decision.ModelCooldownUntil.UnixMilli()
		}
	case cooldown.EffectCoolChannel:
		a.Cooldown = "channel"
		if decision.HasChannelCooldownUntil {
			a.CooldownUntil = decision.ChannelCooldownUntil.UnixMilli()
		}
	}
}

func traceStatusReason(status int) string {
	switch status {
	case 401, 403:
		return "upstream_auth"
	case 429:
		return "rate_limited"
	case 596:
		return "quota_exceeded"
	case 597:
		return "stream_error"
	case 598:
		return "first_byte_timeout"
	case 599:
		return "stream_incomplete"
	default:
		return "upstream_error"
	}
}

func (t *requestTrace) finishAttempt(result *proxyResult, decision cooldown.Decision, res *fwResult, err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	a := t.current
	if a == nil {
		return
	}
	a.Outcome, a.Reason = "error", "local_error"
	if res != nil {
		a.ResponseCommitted = res.ResponseCommitted
		a.StatusCode = res.Status
	}
	if result != nil {
		a.StatusCode = result.status
		switch {
		case result.isClientCanceled:
			a.Outcome, a.Reason = "canceled", "client_canceled"
		case result.protocolCapabilityMissing:
			a.Outcome, a.Reason = "capability", "protocol_unsupported"
		case result.succeeded && result.status >= 200 && result.status < 300:
			a.Outcome, a.Reason = "success", ""
		default:
			a.Reason = traceStatusReason(result.status)
		}
	}
	if errors.Is(err, ErrChannelRPMExceeded) {
		a.Outcome, a.Reason, a.StatusCode = "skipped", "channel_rpm_limit", 429
	} else if errors.Is(err, ErrChannelConcurrencyExceeded) {
		a.Outcome, a.Reason, a.StatusCode = "skipped", "channel_concurrency_limit", 429
	}
	if result != nil && result.deferredCooldown != nil {
		// A decision which has only been considered must not appear as applied.
		decision.Effect = cooldown.EffectNone
	}
	traceDecision(a, decision)
	t.appendLocked()
}

func (t *requestTrace) skipped(cfg *model.Config, keyIndex int, baseURL, reason string, status int) {
	if t == nil {
		return
	}
	t.begin(cfg, keyIndex, baseURL, "", "")
	t.mu.Lock()
	t.current.StatusCode, t.current.Outcome, t.current.Reason = status, "skipped", reason
	t.current.Retry = "next_channel"
	t.appendLocked()
	t.mu.Unlock()
}

// amendDecision captures URL/OAuth orchestration decisions made after the
// forward helper returns, without inventing an additional upstream attempt.
func (t *requestTrace) amendDecision(retry string, decision *cooldown.Decision) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entry.Truncated || len(t.entry.Attempts) == 0 {
		return
	}
	a := &t.entry.Attempts[len(t.entry.Attempts)-1]
	if decision != nil {
		traceDecision(a, *decision)
	}
	if retry != "" {
		a.Retry = retry
	}
}

func (t *requestTrace) snapshot(status int, canceled bool) *model.RequestTrace {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.entry
	entry.Duration = time.Since(t.started).Seconds()
	entry.FinalStatus = status
	entry.Outcome = "error"
	if canceled {
		entry.Outcome, entry.FinalStatus = "canceled", util.StatusClientClosedRequest
	} else if status >= http.StatusOK && status < http.StatusMultipleChoices {
		entry.Outcome = "success"
	}
	entry.Attempts = append([]model.RequestAttempt{}, entry.Attempts...)
	if !entry.Truncated && len(entry.Attempts) > 0 {
		entry.Attempts[len(entry.Attempts)-1].Retry = "none"
	}
	return &entry
}

func (s *Server) persistRequestTrace(trace *requestTrace, status int, canceled bool) {
	if trace != nil && s.logService != nil {
		s.logService.AddRequestTraceAsync(trace.snapshot(status, canceled))
	}
}
