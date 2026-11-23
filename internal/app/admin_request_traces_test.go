package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"

	"github.com/gin-gonic/gin"
)

type requestTraceAPIStore struct {
	storage.Store

	mu                sync.Mutex
	listFilter        *model.RequestTraceFilter
	statsFilter       *model.RequestTraceFilter
	detailAuthTokenID *int64
	traces            []*model.RequestTrace
	stats             *model.RequestTraceStats
	detail            *model.RequestTrace
}

func (s *requestTraceAPIStore) ListRequestTraces(
	_ context.Context,
	_, _ time.Time,
	_, _ int,
	filter *model.RequestTraceFilter,
) ([]*model.RequestTrace, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listFilter = cloneRequestTraceAPIFilter(filter)
	return append([]*model.RequestTrace(nil), s.traces...), len(s.traces), nil
}

func (s *requestTraceAPIStore) GetRequestTraceStats(
	_ context.Context,
	_, _ time.Time,
	filter *model.RequestTraceFilter,
) (*model.RequestTraceStats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statsFilter = cloneRequestTraceAPIFilter(filter)
	if s.stats == nil {
		return &model.RequestTraceStats{}, nil
	}
	stats := *s.stats
	return &stats, nil
}

func (s *requestTraceAPIStore) GetRequestTrace(
	_ context.Context,
	_ string,
	authTokenID *int64,
) (*model.RequestTrace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if authTokenID == nil {
		s.detailAuthTokenID = nil
	} else {
		value := *authTokenID
		s.detailAuthTokenID = &value
	}
	return s.detail, nil
}

func cloneRequestTraceAPIFilter(filter *model.RequestTraceFilter) *model.RequestTraceFilter {
	if filter == nil {
		return nil
	}
	clone := *filter
	if filter.AuthTokenID != nil {
		value := *filter.AuthTokenID
		clone.AuthTokenID = &value
	}
	return &clone
}

func setRequestTraceAPITokenIdentity(c *gin.Context, tokenID int64) {
	c.Set(webIdentityContextKey, WebIdentity{
		Role:        model.WebRoleAPIToken,
		AuthTokenID: tokenID,
	})
}

func sensitiveRequestTraceForAPI() *model.RequestTrace {
	return &model.RequestTrace{
		RequestID:            "pf_req_0123456789abcdef0123456789abcdef",
		StartedAt:            time.Now().Add(-time.Minute).UnixMilli(),
		AuthTokenID:          42,
		Model:                "requested-model",
		ClientProtocol:       "openai",
		Transport:            "http",
		IsStreaming:          true,
		FinalStatus:          http.StatusOK,
		Outcome:              "success",
		Duration:             1.5,
		WaitDuration:         0.1,
		AttemptCount:         1,
		UpstreamAttemptCount: 1,
		UpstreamSuccessCount: 1,
		Attempts: []model.RequestAttempt{{
			Number:            1,
			StartedAt:         time.Now().UnixMilli(),
			Duration:          0.5,
			FirstByteTime:     0.1,
			ChannelID:         987,
			ChannelName:       "private-channel-name",
			KeyIndex:          3,
			URLIndex:          4,
			Endpoint:          "https://private-origin.example.com",
			ActualModel:       "private-upstream-model",
			UpstreamProtocol:  "openai",
			Transport:         "http",
			StatusCode:        http.StatusOK,
			Outcome:           "success",
			Upstream:          true,
			ResponseCommitted: true,
			Reason:            "completed",
			Retry:             "none",
			Cooldown:          "none",
		}},
	}
}

func TestRequestTraceAPIListAndStatsForceTokenScope(t *testing.T) {
	store := &requestTraceAPIStore{
		traces: []*model.RequestTrace{sensitiveRequestTraceForAPI()},
		stats:  &model.RequestTraceStats{Requests: 1, Successes: 1, UpstreamAttempts: 1, UpstreamSuccesses: 1},
	}
	server := &Server{store: store}

	listContext, listRecorder := newTestContext(t, newRequest(
		http.MethodGet,
		"/admin/request-traces?auth_token_id={{SECRET_kx4x3h17}}&search=needle&outcome=success",
		nil,
	))
	setRequestTraceAPITokenIdentity(listContext, 42)
	server.HandleRequestTraces(listContext)

	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	listResponse := mustParseAPIResponse[[]map[string]any](t, listRecorder.Body.Bytes())
	if listResponse.Count != 1 || len(listResponse.Data) != 1 {
		t.Fatalf("unexpected list response: %+v", listResponse)
	}
	if _, found := listResponse.Data[0]["attempts"]; found {
		t.Fatalf("list response unexpectedly included attempt detail: %+v", listResponse.Data[0])
	}
	for _, secret := range []string{
		"private-channel-name",
		"private-origin.example.com",
		"private-upstream-model",
	} {
		if strings.Contains(listRecorder.Body.String(), secret) {
			t.Fatalf("list response leaked %q: %s", secret, listRecorder.Body.String())
		}
	}

	store.mu.Lock()
	listFilter := cloneRequestTraceAPIFilter(store.listFilter)
	store.mu.Unlock()
	if listFilter == nil || listFilter.AuthTokenID == nil || *listFilter.AuthTokenID != 42 {
		t.Fatalf("list filter=%+v, want forced token ID 42", listFilter)
	}
	if listFilter.Search != "needle" || listFilter.Outcome != "success" {
		t.Fatalf("list filter=%+v, want search/outcome preserved", listFilter)
	}

	statsContext, statsRecorder := newTestContext(t, newRequest(
		http.MethodGet,
		"/admin/request-traces/stats?auth_token_id={{SECRET_gvnyo6nk}}&search=needle&outcome=success",
		nil,
	))
	setRequestTraceAPITokenIdentity(statsContext, 42)
	server.HandleRequestTraceStats(statsContext)

	if statsRecorder.Code != http.StatusOK {
		t.Fatalf("stats status=%d body=%s", statsRecorder.Code, statsRecorder.Body.String())
	}
	statsResponse := mustParseAPIResponse[model.RequestTraceStats](t, statsRecorder.Body.Bytes())
	if statsResponse.Data.Requests != 1 {
		t.Fatalf("unexpected stats response: %+v", statsResponse)
	}

	store.mu.Lock()
	statsFilter := cloneRequestTraceAPIFilter(store.statsFilter)
	store.mu.Unlock()
	if statsFilter == nil || statsFilter.AuthTokenID == nil || *statsFilter.AuthTokenID != 42 {
		t.Fatalf("stats filter=%+v, want forced token ID 42", statsFilter)
	}
	if statsFilter.Search != "needle" || statsFilter.Outcome != "success" {
		t.Fatalf("stats filter=%+v, want search/outcome preserved", statsFilter)
	}
}

func TestRequestTraceAPIDetailRedactsTokenInternalFields(t *testing.T) {
	trace := sensitiveRequestTraceForAPI()
	store := &requestTraceAPIStore{detail: trace}
	server := &Server{store: store}
	context, recorder := newTestContext(t, newRequest(
		http.MethodGet,
		"/admin/request-traces/"+trace.RequestID,
		nil,
	))
	context.Params = gin.Params{{Key: "request_id", Value: trace.RequestID}}
	setRequestTraceAPITokenIdentity(context, 42)

	server.HandleRequestTrace(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	response := mustParseAPIResponse[map[string]any](t, recorder.Body.Bytes())
	attempts, ok := response.Data["attempts"].([]any)
	if !ok || len(attempts) != 1 {
		t.Fatalf("attempts=%v, want one projected attempt", response.Data["attempts"])
	}
	attempt, ok := attempts[0].(map[string]any)
	if !ok {
		t.Fatalf("attempt=%T, want object", attempts[0])
	}
	for _, forbidden := range []string{
		"channel_id",
		"channel_name",
		"endpoint",
		"key_index",
		"url_index",
		"actual_model",
	} {
		if _, found := attempt[forbidden]; found {
			t.Fatalf("token detail leaked %q: %s", forbidden, recorder.Body.String())
		}
	}
	for _, secret := range []string{
		"private-channel-name",
		"private-origin.example.com",
		"private-upstream-model",
	} {
		if strings.Contains(recorder.Body.String(), secret) {
			t.Fatalf("token detail leaked %q: %s", secret, recorder.Body.String())
		}
	}
	if attempt["status_code"] != float64(http.StatusOK) || attempt["upstream_protocol"] != "openai" {
		t.Fatalf("safe attempt projection omitted allowed diagnostics: %+v", attempt)
	}

	store.mu.Lock()
	detailAuthTokenID := store.detailAuthTokenID
	store.mu.Unlock()
	if detailAuthTokenID == nil || *detailAuthTokenID != 42 {
		t.Fatalf("detail auth token scope=%v, want 42", detailAuthTokenID)
	}
}

func TestRequestTraceAPIAdminDetailKeepsSanitizedTimeline(t *testing.T) {
	trace := sensitiveRequestTraceForAPI()
	store := &requestTraceAPIStore{detail: trace}
	server := &Server{store: store}
	context, recorder := newTestContext(t, newRequest(
		http.MethodGet,
		"/admin/request-traces/"+trace.RequestID,
		nil,
	))
	context.Params = gin.Params{{Key: "request_id", Value: trace.RequestID}}
	context.Set(webIdentityContextKey, WebIdentity{Role: model.WebRoleAdmin})

	server.HandleRequestTrace(context)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Success bool                       `json:"success"`
		Data    map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !response.Success || len(response.Data["attempts"]) == 0 {
		t.Fatalf("admin detail omitted sanitized timeline: %s", recorder.Body.String())
	}
}

func TestRequestTraceAPIRejectsInvalidFiltersAndRequestIDs(t *testing.T) {
	server := &Server{store: &requestTraceAPIStore{}}
	listCases := []struct {
		name   string
		target string
	}{
		{name: "outcome", target: "/admin/request-traces?outcome=maybe"},
		{name: "long search", target: "/admin/request-traces?search=" + strings.Repeat("x", 201)},
	}
	for _, testCase := range listCases {
		t.Run(testCase.name, func(t *testing.T) {
			context, recorder := newTestContext(t, newRequest(http.MethodGet, testCase.target, nil))
			server.HandleRequestTraces(context)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	for _, requestID := range []string{
		"pf_req_short",
		"pf_req_0123456789ABCDEF0123456789ABCDEF",
		"pf_req_0123456789abcdef0123456789abcdeg",
	} {
		t.Run(requestID, func(t *testing.T) {
			context, recorder := newTestContext(t, newRequest(
				http.MethodGet,
				"/admin/request-traces/"+requestID,
				nil,
			))
			context.Params = gin.Params{{Key: "request_id", Value: requestID}}
			server.HandleRequestTrace(context)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestRequestTraceAPIDetailReturnsNotFoundForForeignOrMissingTrace(t *testing.T) {
	store := &requestTraceAPIStore{}
	server := &Server{store: store}
	requestID := "pf_req_0123456789abcdef0123456789abcdef"
	context, recorder := newTestContext(t, newRequest(
		http.MethodGet,
		"/admin/request-traces/"+requestID,
		nil,
	))
	context.Params = gin.Params{{Key: "request_id", Value: requestID}}
	setRequestTraceAPITokenIdentity(context, 42)

	server.HandleRequestTrace(context)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
	store.mu.Lock()
	detailAuthTokenID := store.detailAuthTokenID
	store.mu.Unlock()
	if detailAuthTokenID == nil || *detailAuthTokenID != 42 {
		t.Fatalf("detail was not ownership-scoped: %v", detailAuthTokenID)
	}
}
