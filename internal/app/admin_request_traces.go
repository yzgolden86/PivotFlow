package app

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/protocol"

	"github.com/gin-gonic/gin"
)

const requestTraceSearchMaxRunes = 200

var requestTraceIDPattern = regexp.MustCompile(`^pf_req_[0-9a-f]{32}$`)

type requestTraceSummaryView struct {
	RequestID            string                           `json:"request_id"`
	StartedAt            int64                            `json:"started_at"`
	AuthTokenID          int64                            `json:"auth_token_id"`
	Model                string                           `json:"model"`
	ActualModel          string                           `json:"actual_model,omitempty"`
	ChannelID            int64                            `json:"channel_id,omitempty"`
	ChannelName          string                           `json:"channel_name,omitempty"`
	ClientProtocol       string                           `json:"client_protocol"`
	Transport            string                           `json:"transport"`
	IsStreaming          bool                             `json:"is_streaming"`
	FinalStatus          int                              `json:"final_status"`
	Outcome              string                           `json:"outcome"`
	Duration             float64                          `json:"duration"`
	WaitDuration         float64                          `json:"wait_duration"`
	AttemptCount         int                              `json:"attempt_count"`
	UpstreamAttemptCount int                              `json:"upstream_attempt_count"`
	UpstreamSuccessCount int                              `json:"upstream_success_count"`
	Truncated            bool                             `json:"truncated"`
	AttemptSummaries     []requestTraceAttemptSummaryView `json:"attempt_summaries,omitempty"`
	CacheStatus          string                           `json:"cache_status,omitempty"`
	CacheReason          string                           `json:"cache_reason,omitempty"`
}

type requestTraceAttemptSummaryView struct {
	Number     int    `json:"number"`
	StartedAt  int64  `json:"started_at"`
	StatusCode int    `json:"status_code"`
	Outcome    string `json:"outcome"`
	Upstream   bool   `json:"upstream"`
}

type tokenRequestTraceAttemptView struct {
	Number            int     `json:"number"`
	StartedAt         int64   `json:"started_at"`
	Duration          float64 `json:"duration"`
	FirstByteTime     float64 `json:"first_byte_time"`
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

type tokenRequestTraceDetailView struct {
	requestTraceSummaryView
	Attempts []tokenRequestTraceAttemptView `json:"attempts"`
}

// HandleRequestTraces returns request summaries without attempt detail.
func (s *Server) HandleRequestTraces(c *gin.Context) {
	params, filter, err := parseRequestTraceRequest(c)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}
	since, until := params.GetTimeRange()
	traces, total, err := s.store.ListRequestTraces(
		c.Request.Context(),
		since,
		until,
		params.Limit,
		params.Offset,
		filter,
	)
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "读取请求历史失败")
		return
	}
	summaries := make([]requestTraceSummaryView, 0, len(traces))
	includeRouting := requestTraceIdentityScope(c) == nil
	for _, trace := range traces {
		if trace == nil {
			continue
		}
		summaries = append(summaries, projectRequestTraceSummary(trace, includeRouting))
	}
	RespondJSONWithCount(c, http.StatusOK, summaries, total)
}

// HandleRequestTraceStats returns summary counters using the same filters as
// the request list.
func (s *Server) HandleRequestTraceStats(c *gin.Context) {
	params, filter, err := parseRequestTraceRequest(c)
	if err != nil {
		RespondErrorMsg(c, http.StatusBadRequest, err.Error())
		return
	}
	since, until := params.GetTimeRange()
	stats, err := s.store.GetRequestTraceStats(c.Request.Context(), since, until, filter)
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "读取请求统计失败")
		return
	}
	if stats == nil {
		stats = &model.RequestTraceStats{}
	}
	RespondJSON(c, http.StatusOK, stats)
}

// HandleRequestTrace returns one request timeline. API-token sessions are
// ownership-scoped and receive an explicit projection without routing details.
func (s *Server) HandleRequestTrace(c *gin.Context) {
	requestID := strings.TrimSpace(c.Param("request_id"))
	if !requestTraceIDPattern.MatchString(requestID) {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid request_id")
		return
	}

	authTokenID := requestTraceIdentityScope(c)
	trace, err := s.store.GetRequestTrace(c.Request.Context(), requestID, authTokenID)
	if err != nil {
		RespondErrorMsg(c, http.StatusInternalServerError, "读取请求详情失败")
		return
	}
	if trace == nil || (authTokenID != nil && trace.AuthTokenID != *authTokenID) {
		RespondErrorMsg(c, http.StatusNotFound, "request trace not found")
		return
	}

	if authTokenID != nil {
		RespondJSON(c, http.StatusOK, projectTokenRequestTrace(trace))
		return
	}
	RespondJSON(c, http.StatusOK, trace)
}

func parseRequestTraceRequest(c *gin.Context) (*PaginationParams, *model.RequestTraceFilter, error) {
	if err := rejectRepeatedRouteQuery(c); err != nil {
		return nil, nil, err
	}
	params := ParsePaginationParams(c)
	filter := &model.RequestTraceFilter{}
	if name := c.Query("model"); name != "" {
		if err := validateRouteText(name, 191, "model"); err != nil {
			return nil, nil, err
		}
		filter.Model = name
	}
	if client := c.Query("client_protocol"); client != "" {
		if !protocol.IsValid(protocol.Protocol(client)) {
			return nil, nil, errors.New("invalid client_protocol")
		}
		filter.ClientProtocol = client
	}

	if search := strings.TrimSpace(c.Query("search")); search != "" {
		if utf8.RuneCountInString(search) > requestTraceSearchMaxRunes {
			return nil, nil, errors.New("search must be 200 characters or fewer")
		}
		filter.Search = search
	}

	switch outcome := strings.ToLower(strings.TrimSpace(c.Query("outcome"))); outcome {
	case "":
	case "success", "error", "canceled":
		filter.Outcome = outcome
	default:
		return nil, nil, errors.New("invalid outcome")
	}

	if raw := strings.TrimSpace(c.Query("auth_token_id")); raw != "" {
		if authTokenID, err := strconv.ParseInt(raw, 10, 64); err == nil && authTokenID > 0 {
			filter.AuthTokenID = &authTokenID
		}
	}

	if scopedAuthTokenID := requestTraceIdentityScope(c); scopedAuthTokenID != nil {
		filter.AuthTokenID = scopedAuthTokenID
	}
	switch raw := strings.TrimSpace(c.Query("upstream_failures")); raw {
	case "", "0":
	case "1":
		filter.UpstreamFailuresOnly = true
	default:
		return nil, nil, errors.New("invalid upstream_failures")
	}
	return params, filter, nil
}

func requestTraceIdentityScope(c *gin.Context) *int64 {
	identity, ok := WebIdentityFromContext(c)
	if !ok || identity.Role != model.WebRoleAPIToken {
		return nil
	}
	authTokenID := identity.AuthTokenID
	if authTokenID <= 0 {
		authTokenID = 1<<63 - 1
	}
	return &authTokenID
}

func projectRequestTraceSummary(trace *model.RequestTrace, includeRouting bool) requestTraceSummaryView {
	if trace == nil {
		return requestTraceSummaryView{}
	}
	summary := requestTraceSummaryView{
		RequestID:            trace.RequestID,
		StartedAt:            trace.StartedAt,
		AuthTokenID:          trace.AuthTokenID,
		Model:                trace.Model,
		ClientProtocol:       trace.ClientProtocol,
		Transport:            trace.Transport,
		IsStreaming:          trace.IsStreaming,
		FinalStatus:          trace.FinalStatus,
		Outcome:              trace.Outcome,
		Duration:             trace.Duration,
		WaitDuration:         trace.WaitDuration,
		AttemptCount:         trace.AttemptCount,
		UpstreamAttemptCount: trace.UpstreamAttemptCount,
		UpstreamSuccessCount: trace.UpstreamSuccessCount,
		Truncated:            trace.Truncated,
		CacheStatus:          trace.CacheStatus,
		CacheReason:          trace.CacheReason,
	}
	for _, attempt := range trace.AttemptSummaries {
		summary.AttemptSummaries = append(summary.AttemptSummaries, requestTraceAttemptSummaryView{
			Number: attempt.Number, StartedAt: attempt.StartedAt, StatusCode: attempt.StatusCode,
			Outcome: attempt.Outcome, Upstream: attempt.Upstream,
		})
	}
	if len(summary.AttemptSummaries) == 0 {
		for _, attempt := range trace.Attempts {
			summary.AttemptSummaries = append(summary.AttemptSummaries, requestTraceAttemptSummaryView{
				Number: attempt.Number, StartedAt: attempt.StartedAt, StatusCode: attempt.StatusCode,
				Outcome: attempt.Outcome, Upstream: attempt.Upstream,
			})
		}
	}
	if includeRouting {
		summary.ActualModel = model.LastRequestActualModel(trace)
		summary.ChannelID, summary.ChannelName = model.LastRequestChannel(trace)
	}
	return summary
}

func projectTokenRequestTrace(trace *model.RequestTrace) tokenRequestTraceDetailView {
	summary := projectRequestTraceSummary(trace, false)
	attempts := make([]tokenRequestTraceAttemptView, 0, len(trace.Attempts))
	for _, attempt := range trace.Attempts {
		attempts = append(attempts, tokenRequestTraceAttemptView{
			Number:            attempt.Number,
			StartedAt:         attempt.StartedAt,
			Duration:          attempt.Duration,
			FirstByteTime:     attempt.FirstByteTime,
			UpstreamProtocol:  attempt.UpstreamProtocol,
			Transport:         attempt.Transport,
			StatusCode:        attempt.StatusCode,
			Outcome:           attempt.Outcome,
			Upstream:          attempt.Upstream,
			ResponseCommitted: attempt.ResponseCommitted,
			Reason:            attempt.Reason,
			Retry:             attempt.Retry,
			Cooldown:          attempt.Cooldown,
			CooldownUntil:     attempt.CooldownUntil,
		})
	}
	return tokenRequestTraceDetailView{
		requestTraceSummaryView: summary,
		Attempts:                attempts,
	}
}
