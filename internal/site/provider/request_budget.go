package provider

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const siteRequestInterval = 250 * time.Millisecond

// RequestBudget belongs to one control service, shared by every provider client
// it creates. It is intentionally separate from gateway inference rate limits.
type RequestBudget struct {
	mu      sync.Mutex
	origins map[string]*originBudget
	global  chan struct{}
}

type originBudget struct {
	gate          chan struct{}
	active        int
	waiting       int
	next          time.Time
	cooldownUntil time.Time
}

// BudgetSnapshot contains no host, URL, credential, or upstream response text.
type BudgetSnapshot struct {
	Active        int   `json:"active"`
	Waiting       int   `json:"waiting"`
	NextRequestAt int64 `json:"next_request_at,omitempty"`
	CooldownUntil int64 `json:"cooldown_until,omitempty"`
}

func NewRequestBudget() *RequestBudget {
	return &RequestBudget{origins: make(map[string]*originBudget), global: make(chan struct{}, 4)}
}

func budgetOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + host
}

func (b *RequestBudget) Snapshot(baseURL string) BudgetSnapshot {
	if b == nil {
		return BudgetSnapshot{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.origins[budgetOrigin(baseURL)]
	if state == nil {
		return BudgetSnapshot{}
	}
	result := BudgetSnapshot{Active: state.active, Waiting: state.waiting}
	if state.next.After(time.Now()) {
		result.NextRequestAt = state.next.UnixMilli()
	}
	if state.cooldownUntil.After(time.Now()) {
		result.CooldownUntil = state.cooldownUntil.UnixMilli()
	}
	return result
}

func (b *RequestBudget) acquire(ctx context.Context, origin string) (*originBudget, func(), error) {
	b.mu.Lock()
	state := b.origins[origin]
	if state == nil {
		state = &originBudget{gate: make(chan struct{}, 1)}
		b.origins[origin] = state
	}
	state.waiting++
	b.mu.Unlock()
	defer func() { b.mu.Lock(); state.waiting--; b.mu.Unlock() }()
	select {
	case state.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	releaseOrigin := func() { <-state.gate }
	b.mu.Lock()
	next, until := state.next, state.cooldownUntil
	b.mu.Unlock()
	if until.After(time.Now()) {
		releaseOrigin()
		return nil, nil, &Error{Code: CodeRateLimited, StatusCode: http.StatusTooManyRequests, RetryAfter: until, Message: "site request budget is cooling down"}
	}
	if wait := time.Until(next); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			releaseOrigin()
			return nil, nil, ctx.Err()
		}
	}
	select {
	case b.global <- struct{}{}:
	case <-ctx.Done():
		releaseOrigin()
		return nil, nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-b.global
		releaseOrigin()
		return nil, nil, err
	}
	b.mu.Lock()
	state.next = time.Now().Add(siteRequestInterval)
	state.active++
	b.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			b.mu.Lock()
			state.active--
			b.mu.Unlock()
			<-b.global
			releaseOrigin()
		})
	}
	return state, release, nil
}

func budgetRetryAfter(raw string, now time.Time) time.Time {
	// Check multiplication before converting seconds to Duration. An overflowed
	// value must not turn a large limit into a time in the past.
	seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err == nil && seconds > 0 && seconds <= int64((1<<63-1)/time.Second) {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if deadline, err := http.ParseTime(strings.TrimSpace(raw)); err == nil && deadline.After(now) {
		return deadline
	}
	return now.Add(30 * time.Second)
}

func (b *RequestBudget) wrap(base http.RoundTripper) http.RoundTripper {
	if b == nil {
		return base
	}
	return &budgetTransport{budget: b, base: base}
}

type budgetTransport struct {
	budget *RequestBudget
	base   http.RoundTripper
}

func (t *budgetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	state, release, err := t.budget.acquire(req.Context(), budgetOrigin(req.URL.String()))
	if err != nil {
		return nil, err
	}
	var bodyMu sync.Mutex
	var responseBody *budgetBody
	stop := context.AfterFunc(req.Context(), func() {
		bodyMu.Lock()
		body := responseBody
		bodyMu.Unlock()
		if body != nil {
			_ = body.Close()
		}
	})
	finish := func() { stop(); release() }
	response, err := t.base.RoundTrip(req)
	if err != nil {
		finish()
		return response, err
	}
	if response.StatusCode == http.StatusTooManyRequests || (response.StatusCode == http.StatusServiceUnavailable && response.Header.Get("Retry-After") != "") {
		until := budgetRetryAfter(response.Header.Get("Retry-After"), time.Now())
		t.budget.mu.Lock()
		if until.After(state.cooldownUntil) {
			state.cooldownUntil = until
		}
		t.budget.mu.Unlock()
	}
	if response.Body == nil {
		response.Body = http.NoBody
	}
	body := &budgetBody{ReadCloser: response.Body, finish: finish}
	response.Body = body
	bodyMu.Lock()
	responseBody = body
	bodyMu.Unlock()
	if req.Context().Err() != nil {
		_ = body.Close()
	}
	return response, nil
}

type budgetBody struct {
	io.ReadCloser
	finish    func()
	closeOnce sync.Once
	closeErr  error
}

func (b *budgetBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.finish()
	}
	return n, err
}

func (b *budgetBody) Close() error {
	b.closeOnce.Do(func() { b.closeErr = b.ReadCloser.Close(); b.finish() })
	return b.closeErr
}
