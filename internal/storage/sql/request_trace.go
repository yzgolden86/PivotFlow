package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

const (
	maxStoredRequestAttempts      = 128
	defaultTraceListLimit         = 50
	maxRequestTraceIDLength       = 64
	maxRequestTraceLabelLength    = 191
	maxRequestTraceEndpointLength = 512
	maxRequestTraceCodeLength     = 64
	maxRequestTraceCounter        = 1_000_000
)

const requestTraceInsertColumns = `INSERT INTO request_traces(
	request_id, started_at, auth_token_id, model, client_protocol, transport,
	is_streaming, final_status, outcome, duration, wait_duration, attempt_count,
	upstream_attempt_count, upstream_success_count, truncated, cache_status,
	cache_reason, actual_model, channel_id, channel_name, attempts_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const requestTraceSummaryColumns = `
	request_id, started_at, auth_token_id, model, client_protocol, transport,
	is_streaming, final_status, outcome, duration, wait_duration, attempt_count,
	upstream_attempt_count, upstream_success_count, truncated, cache_status,
	cache_reason, actual_model, channel_id, channel_name`

var validRequestTraceRetryCodes = map[string]struct{}{
	"none":          {},
	"next_key":      {},
	"next_url":      {},
	"next_channel":  {},
	"refresh_token": {},
	"compatibility": {},
}

var validRequestTraceCooldownCodes = map[string]struct{}{
	"none":    {},
	"key":     {},
	"model":   {},
	"channel": {},
}

type requestTraceInsertRow struct {
	trace        *model.RequestTrace
	attemptsJSON string
}

func prepareRequestTraceRow(trace *model.RequestTrace) (requestTraceInsertRow, error) {
	clone, err := NormalizeRequestTraceForStorage(trace)
	if err != nil {
		return requestTraceInsertRow{}, err
	}
	attemptsJSON, err := json.Marshal(clone.Attempts)
	if err != nil {
		return requestTraceInsertRow{}, fmt.Errorf("marshal request trace %s attempts: %w", clone.RequestID, err)
	}
	return requestTraceInsertRow{trace: clone, attemptsJSON: string(attemptsJSON)}, nil
}

// NormalizeRequestTraceForStorage returns an immutable clone with schema-sized
// labels, a bounded attempt timeline, and only storage-safe string values.
func NormalizeRequestTraceForStorage(trace *model.RequestTrace) (*model.RequestTrace, error) {
	if trace == nil {
		return nil, fmt.Errorf("request trace is nil")
	}
	requestID := strings.TrimSpace(trace.RequestID)
	if requestID == "" {
		return nil, fmt.Errorf("request trace request_id is required")
	}
	if strings.ContainsRune(requestID, '\x00') {
		return nil, fmt.Errorf("request trace request_id contains NUL")
	}
	if len([]rune(requestID)) > maxRequestTraceIDLength {
		return nil, fmt.Errorf(
			"request trace request_id length %d exceeds %d",
			len([]rune(requestID)),
			maxRequestTraceIDLength,
		)
	}

	clone := *trace
	clone.RequestID = requestID
	clone.Model = truncateRequestTraceText(clone.Model, maxRequestTraceLabelLength)
	clone.ActualModel = truncateRequestTraceText(model.LastRequestActualModel(trace), maxRequestTraceLabelLength)
	clone.ChannelID, clone.ChannelName = model.LastRequestChannel(trace)
	clone.ChannelName = truncateRequestTraceText(clone.ChannelName, maxRequestTraceLabelLength)
	if err := validateRequestTraceID(clone.ChannelID, "channel_id"); err != nil {
		return nil, err
	}
	clientProtocol, err := normalizeRequestTraceProtocol(clone.ClientProtocol, "client_protocol")
	if err != nil {
		return nil, err
	}
	clone.ClientProtocol = clientProtocol
	transport, err := normalizeRequestTraceTransport(clone.Transport, false, "transport")
	if err != nil {
		return nil, err
	}
	clone.Transport = transport
	if err := validateRequestTraceSummaryOutcome(clone.Outcome); err != nil {
		return nil, err
	}
	if err := validateRequestCacheMetadata(clone.CacheStatus, clone.CacheReason); err != nil {
		return nil, err
	}
	if err := validateRequestTraceID(clone.AuthTokenID, "auth_token_id"); err != nil {
		return nil, err
	}
	if clone.StartedAt < 0 {
		return nil, fmt.Errorf("request trace started_at must be non-negative")
	}
	if err := validateRequestTraceDuration(clone.Duration, "duration"); err != nil {
		return nil, err
	}
	if err := validateRequestTraceDuration(clone.WaitDuration, "wait_duration"); err != nil {
		return nil, err
	}
	for name, value := range map[string]int{
		"attempt_count":          clone.AttemptCount,
		"upstream_attempt_count": clone.UpstreamAttemptCount,
		"upstream_success_count": clone.UpstreamSuccessCount,
	} {
		if err := validateRequestTraceCount(value, name); err != nil {
			return nil, err
		}
	}

	storedAttempts := trace.Attempts
	if len(storedAttempts) > maxStoredRequestAttempts {
		storedAttempts = storedAttempts[:maxStoredRequestAttempts]
		clone.Truncated = true
	}
	clone.Attempts = append([]model.RequestAttempt(nil), storedAttempts...)
	for i := range clone.Attempts {
		attempt := &clone.Attempts[i]
		if attempt.Number <= 0 {
			return nil, fmt.Errorf("request trace attempt %d number must be positive", i)
		}
		if attempt.Number > maxRequestTraceCounter {
			return nil, fmt.Errorf(
				"request trace attempts[%d].number %d exceeds %d",
				i,
				attempt.Number,
				maxRequestTraceCounter,
			)
		}
		if attempt.StartedAt < 0 {
			return nil, fmt.Errorf("request trace attempt %d started_at must be non-negative", i)
		}
		if err := validateRequestTraceDuration(attempt.Duration, fmt.Sprintf("attempts[%d].duration", i)); err != nil {
			return nil, err
		}
		if err := validateRequestTraceDuration(attempt.FirstByteTime, fmt.Sprintf("attempts[%d].first_byte_time", i)); err != nil {
			return nil, err
		}
		if attempt.ChannelID < 0 {
			return nil, fmt.Errorf("request trace attempts[%d].channel_id must be non-negative", i)
		}
		if attempt.KeyIndex < -1 {
			return nil, fmt.Errorf("request trace attempts[%d].key_index must be at least -1", i)
		}
		if attempt.URLIndex < -1 {
			return nil, fmt.Errorf("request trace attempts[%d].url_index must be at least -1", i)
		}
		attempt.ChannelName = truncateRequestTraceText(attempt.ChannelName, maxRequestTraceLabelLength)
		attempt.ActualModel = truncateRequestTraceText(attempt.ActualModel, maxRequestTraceLabelLength)
		attempt.Endpoint = requestTraceOrigin(attempt.Endpoint)
		upstreamProtocol, err := normalizeRequestTraceProtocol(attempt.UpstreamProtocol, fmt.Sprintf("attempts[%d].upstream_protocol", i))
		if err != nil {
			return nil, err
		}
		attempt.UpstreamProtocol = upstreamProtocol
		attemptTransport, err := normalizeRequestTraceTransport(attempt.Transport, true, fmt.Sprintf("attempts[%d].transport", i))
		if err != nil {
			return nil, err
		}
		attempt.Transport = attemptTransport
		if err := validateRequestTraceAttemptOutcome(attempt.Outcome, i); err != nil {
			return nil, err
		}
		attempt.Retry, err = normalizeRequestTraceCode(
			attempt.Retry,
			"none",
			fmt.Sprintf("attempts[%d].retry", i),
			validRequestTraceRetryCodes,
		)
		if err != nil {
			return nil, err
		}
		attempt.Cooldown, err = normalizeRequestTraceCode(
			attempt.Cooldown,
			"none",
			fmt.Sprintf("attempts[%d].cooldown", i),
			validRequestTraceCooldownCodes,
		)
		if err != nil {
			return nil, err
		}
		if attempt.CooldownUntil < 0 {
			return nil, fmt.Errorf("request trace attempts[%d].cooldown_until must be non-negative", i)
		}
		if err := validateRequestTraceReason(attempt.Reason, i); err != nil {
			return nil, err
		}
	}
	if clone.Attempts == nil {
		clone.Attempts = []model.RequestAttempt{}
	}
	return &clone, nil
}

func truncateRequestTraceText(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if len(value) <= maxRunes && !strings.ContainsRune(value, '\x00') {
		return value
	}
	runes := make([]rune, 0, maxRunes)
	for _, r := range value {
		if r == '\x00' {
			continue
		}
		runes = append(runes, r)
		if len(runes) == maxRunes {
			break
		}
	}
	return string(runes)
}

func normalizeRequestTraceProtocol(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	switch value {
	case "", "anthropic", "codex", "openai", "gemini":
		return value, nil
	default:
		return "", fmt.Errorf("request trace %s %q is invalid", field, value)
	}
}

func normalizeRequestTraceTransport(value string, allowEmpty bool, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" && allowEmpty {
		return "http", nil
	}
	switch value {
	case "http", "websocket":
		return value, nil
	default:
		return "", fmt.Errorf("request trace %s %q is invalid", field, value)
	}
}

func validateRequestTraceSummaryOutcome(value string) error {
	switch value {
	case "success", "error", "canceled":
		return nil
	default:
		return fmt.Errorf("request trace outcome %q is invalid", value)
	}
}

func validateRequestTraceAttemptOutcome(value string, attemptIndex int) error {
	switch value {
	case "success", "error", "canceled", "capability", "skipped":
		return nil
	default:
		return fmt.Errorf("request trace attempts[%d].outcome %q is invalid", attemptIndex, value)
	}
}

func normalizeRequestTraceCode(value, defaultValue, field string, allowed map[string]struct{}) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultValue, nil
	}
	if _, ok := allowed[value]; !ok {
		return "", fmt.Errorf("request trace %s %q is invalid", field, value)
	}
	return value, nil
}

func validateRequestTraceReason(value string, attemptIndex int) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len([]rune(value)) > maxRequestTraceCodeLength || !isFixedTraceCode(value) {
		return fmt.Errorf("request trace attempts[%d].reason is not a fixed code", attemptIndex)
	}
	return nil
}

func isFixedTraceCode(value string) bool {
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func validateRequestTraceDuration(value float64, field string) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("request trace %s must be finite", field)
	}
	if value < 0 {
		return fmt.Errorf("request trace %s must be non-negative", field)
	}
	return nil
}

func validateRequestTraceCount(value int, field string) error {
	if value < 0 || value > maxRequestTraceCounter {
		return fmt.Errorf("request trace %s %d is outside [0,%d]", field, value, maxRequestTraceCounter)
	}
	return nil
}

func validateRequestTraceID(value int64, field string) error {
	if value < 0 {
		return fmt.Errorf("request trace %s must be non-negative", field)
	}
	return nil
}

func requestTraceOrigin(raw string) string {
	raw = truncateRequestTraceText(strings.TrimSpace(raw), maxRequestTraceEndpointLength*4)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return ""
	}
	return truncateRequestTraceText(parsed.Scheme+"://"+parsed.Host, maxRequestTraceEndpointLength)
}

func (s *SQLStore) requestTraceInsertSQL() string {
	switch {
	case s.IsMySQL():
		return requestTraceInsertColumns + " ON DUPLICATE KEY UPDATE request_id=request_id"
	case s.IsPostgres():
		return requestTraceInsertColumns + " ON CONFLICT (request_id) DO NOTHING"
	default:
		return requestTraceInsertColumns + " ON CONFLICT(request_id) DO NOTHING"
	}
}

func requestTraceInsertArgs(trace *model.RequestTrace, attemptsJSON string) []any {
	return []any{
		trace.RequestID,
		trace.StartedAt,
		trace.AuthTokenID,
		trace.Model,
		trace.ClientProtocol,
		trace.Transport,
		boolAsInt(trace.IsStreaming),
		trace.FinalStatus,
		trace.Outcome,
		trace.Duration,
		trace.WaitDuration,
		trace.AttemptCount,
		trace.UpstreamAttemptCount,
		trace.UpstreamSuccessCount,
		boolAsInt(trace.Truncated),
		trace.CacheStatus,
		trace.CacheReason,
		trace.ActualModel,
		trace.ChannelID,
		trace.ChannelName,
		attemptsJSON,
	}
}

func boolAsInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// BatchAddRequestTraces inserts immutable finished summaries. Duplicate request
// IDs are ignored, so retries cannot overwrite a previously committed trace.
func (s *SQLStore) BatchAddRequestTraces(ctx context.Context, traces []*model.RequestTrace) error {
	if len(traces) == 0 {
		return nil
	}
	rows := make([]requestTraceInsertRow, 0, len(traces))
	for _, trace := range traces {
		if trace == nil {
			continue
		}
		row, err := prepareRequestTraceRow(trace)
		if err != nil {
			return err
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil
	}

	return s.withTx(ctx, func(tx *sql.Tx) error {
		stmt, err := s.prepareTx(ctx, tx, s.requestTraceInsertSQL())
		if err != nil {
			return fmt.Errorf("prepare request trace insert: %w", err)
		}
		defer func() { _ = stmt.Close() }()

		for _, row := range rows {
			if _, err := stmt.ExecContext(ctx, requestTraceInsertArgs(row.trace, row.attemptsJSON)...); err != nil {
				return fmt.Errorf("insert request trace %s: %w", row.trace.RequestID, err)
			}
		}
		return nil
	})
}

func scanRequestTraceSummary(scanner interface {
	Scan(...any) error
}, legacyAttempts *string) (*model.RequestTrace, error) {
	var trace model.RequestTrace
	var isStreaming, truncated int
	values := []any{
		&trace.RequestID,
		&trace.StartedAt,
		&trace.AuthTokenID,
		&trace.Model,
		&trace.ClientProtocol,
		&trace.Transport,
		&isStreaming,
		&trace.FinalStatus,
		&trace.Outcome,
		&trace.Duration,
		&trace.WaitDuration,
		&trace.AttemptCount,
		&trace.UpstreamAttemptCount,
		&trace.UpstreamSuccessCount,
		&truncated,
		&trace.CacheStatus,
		&trace.CacheReason,
		&trace.ActualModel,
		&trace.ChannelID,
		&trace.ChannelName,
	}
	if legacyAttempts != nil {
		values = append(values, legacyAttempts)
	}
	if err := scanner.Scan(values...); err != nil {
		return nil, err
	}
	trace.IsStreaming = isStreaming != 0
	trace.Truncated = truncated != 0
	if err := validateRequestCacheMetadata(trace.CacheStatus, trace.CacheReason); err != nil {
		return nil, err
	}
	return &trace, nil
}

// GetRequestTrace returns one immutable summary with its attempt timeline.
// Supplying authTokenID enforces token ownership in SQL. Missing and
// out-of-scope rows return (nil, nil), matching debug-log reads.
func (s *SQLStore) GetRequestTrace(ctx context.Context, id string, authTokenID *int64) (*model.RequestTrace, error) {
	qb := NewQueryBuilder(`SELECT `+requestTraceSummaryColumns+`, attempts_json FROM request_traces`).
		Where("request_id = ?", id)
	if authTokenID != nil {
		qb.Where("auth_token_id = ?", *authTokenID)
	}
	query, args := qb.Build()

	var attemptsJSON string
	trace := &model.RequestTrace{}
	row := s.QueryRowContext(ctx, query, args...)
	var isStreaming, truncated int
	if err := row.Scan(
		&trace.RequestID,
		&trace.StartedAt,
		&trace.AuthTokenID,
		&trace.Model,
		&trace.ClientProtocol,
		&trace.Transport,
		&isStreaming,
		&trace.FinalStatus,
		&trace.Outcome,
		&trace.Duration,
		&trace.WaitDuration,
		&trace.AttemptCount,
		&trace.UpstreamAttemptCount,
		&trace.UpstreamSuccessCount,
		&truncated,
		&trace.CacheStatus,
		&trace.CacheReason,
		&trace.ActualModel,
		&trace.ChannelID,
		&trace.ChannelName,
		&attemptsJSON,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	trace.IsStreaming = isStreaming != 0
	trace.Truncated = truncated != 0
	if err := validateRequestCacheMetadata(trace.CacheStatus, trace.CacheReason); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(attemptsJSON), &trace.Attempts); err != nil {
		return nil, fmt.Errorf("unmarshal request trace %s attempts: %w", trace.RequestID, err)
	}
	if trace.Attempts == nil {
		trace.Attempts = []model.RequestAttempt{}
	}
	trace.ActualModel = truncateRequestTraceText(model.LastRequestActualModel(trace), maxRequestTraceLabelLength)
	trace.ChannelID, trace.ChannelName = model.LastRequestChannel(trace)
	trace.ChannelName = truncateRequestTraceText(trace.ChannelName, maxRequestTraceLabelLength)
	return trace, nil
}

func validateRequestCacheMetadata(status, reason string) error {
	if !model.ValidRequestCacheStatus(status) {
		return errors.New("request trace cache_status is invalid")
	}
	if !model.ValidRequestCacheReason(reason) {
		return errors.New("request trace cache_reason is invalid")
	}
	return nil
}

func (s *SQLStore) applyRequestTraceFilter(qb *QueryBuilder, filter *model.RequestTraceFilter) {
	if filter == nil {
		return
	}
	if filter.AuthTokenID != nil {
		qb.Where("auth_token_id = ?", *filter.AuthTokenID)
	}
	if filter.Model != "" {
		qb.Where(exactTextEqual(s.driverName, "model"), filter.Model)
	}
	if filter.ClientProtocol != "" {
		qb.Where(exactTextEqual(s.driverName, "client_protocol"), filter.ClientProtocol)
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		needle := "%" + search + "%"
		qb.Where("(request_id LIKE ? OR model LIKE ?)", needle, needle)
	}
	if outcome := strings.TrimSpace(filter.Outcome); outcome != "" {
		qb.Where("outcome = ?", outcome)
	}
	if filter.UpstreamFailuresOnly {
		qb.Where("upstream_attempt_count > upstream_success_count")
	}
}

func requestTraceTimeBounds(since, until time.Time) (int64, int64) {
	sinceMs := since.UnixMilli()
	untilMs := until.UnixMilli()
	if until.IsZero() {
		untilMs = time.Now().UnixMilli()
	}
	if since.IsZero() {
		sinceMs = 0
	}
	return sinceMs, untilMs
}

func normalizeTraceListLimit(limit int) int {
	if limit <= 0 {
		return defaultTraceListLimit
	}
	return limit
}

// ListRequestTraces reads a bounded summary page. It parses attempt metadata
// only for retried rows, plus legacy rows missing scalar channel/model fields.
func (s *SQLStore) ListRequestTraces(
	ctx context.Context,
	since, until time.Time,
	limit, offset int,
	filter *model.RequestTraceFilter,
) ([]*model.RequestTrace, int, error) {
	limit = normalizeTraceListLimit(limit)
	offset = max(offset, 0)
	sinceMs, untilMs := requestTraceTimeBounds(since, until)

	applyRangeAndFilter := func(qb *QueryBuilder) {
		qb.Where("started_at >= ?", sinceMs)
		qb.Where("started_at <= ?", untilMs)
		s.applyRequestTraceFilter(qb, filter)
	}

	listQB := NewQueryBuilder(`SELECT ` + requestTraceSummaryColumns + `,
		CASE WHEN attempt_count > 1 OR ((actual_model = '' OR (channel_id = 0 AND channel_name = ''))
		AND upstream_attempt_count > 0 AND truncated = 0)
		THEN attempts_json ELSE '' END FROM request_traces`)
	applyRangeAndFilter(listQB)
	listQuery, listArgs := listQB.BuildWithSuffix("ORDER BY started_at DESC, request_id DESC LIMIT ? OFFSET ?")
	listArgs = append(listArgs, limit, offset)

	rows, err := s.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list request traces: %w", err)
	}
	defer func() { _ = rows.Close() }()

	traces := make([]*model.RequestTrace, 0, limit)
	for rows.Next() {
		var attemptsJSON string
		trace, err := scanRequestTraceSummary(rows, &attemptsJSON)
		if err != nil {
			return nil, 0, fmt.Errorf("scan request trace summary: %w", err)
		}
		if attemptsJSON != "" {
			var summaries []model.RequestAttemptSummary
			if json.Unmarshal([]byte(attemptsJSON), &summaries) == nil {
				sort.SliceStable(summaries, func(i, j int) bool {
					if summaries[i].Number != summaries[j].Number {
						return summaries[i].Number < summaries[j].Number
					}
					return summaries[i].StartedAt < summaries[j].StartedAt
				})
				trace.AttemptSummaries = summaries
			}
			if (trace.ActualModel == "" || (trace.ChannelID == 0 && trace.ChannelName == "")) &&
				trace.UpstreamAttemptCount > 0 && !trace.Truncated {
				var attempts []model.RequestAttempt
				if json.Unmarshal([]byte(attemptsJSON), &attempts) == nil {
					trace.Attempts = attempts
					trace.ActualModel = truncateRequestTraceText(model.LastRequestActualModel(trace), maxRequestTraceLabelLength)
					trace.ChannelID, trace.ChannelName = model.LastRequestChannel(trace)
					trace.ChannelName = truncateRequestTraceText(trace.ChannelName, maxRequestTraceLabelLength)
					trace.Attempts = nil
				}
			}
		}
		traces = append(traces, trace)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate request traces: %w", err)
	}

	countQB := NewQueryBuilder(`SELECT COUNT(*) FROM request_traces`)
	applyRangeAndFilter(countQB)
	countQuery, countArgs := countQB.Build()
	var total int
	if err := s.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count request traces: %w", err)
	}
	return traces, total, nil
}

// GetRequestTraceStats aggregates only persisted summary counters.
func (s *SQLStore) GetRequestTraceStats(
	ctx context.Context,
	since, until time.Time,
	filter *model.RequestTraceFilter,
) (*model.RequestTraceStats, error) {
	sinceMs, untilMs := requestTraceTimeBounds(since, until)
	qb := NewQueryBuilder(`
		SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN outcome = 'success' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN outcome = 'error' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN outcome = 'canceled' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(upstream_attempt_count), 0),
			COALESCE(SUM(upstream_success_count), 0)
		FROM request_traces`).
		Where("started_at >= ?", sinceMs).
		Where("started_at <= ?", untilMs)
	s.applyRequestTraceFilter(qb, filter)
	query, args := qb.Build()

	var stats model.RequestTraceStats
	if err := s.QueryRowContext(ctx, query, args...).Scan(
		&stats.Requests,
		&stats.Successes,
		&stats.Errors,
		&stats.Canceled,
		&stats.UpstreamAttempts,
		&stats.UpstreamSuccesses,
	); err != nil {
		return nil, fmt.Errorf("aggregate request trace stats: %w", err)
	}
	return &stats, nil
}

// CleanupRequestTracesBefore deletes summaries older than cutoff in bounded
// batches. CleanupLogsBefore invokes it so request history follows log retention.
func (s *SQLStore) CleanupRequestTracesBefore(ctx context.Context, cutoff time.Time) error {
	cutoffMs := cutoff.UnixMilli()
	const batchSize = 5000
	for {
		query := `DELETE FROM request_traces WHERE request_id IN (
			SELECT request_id FROM request_traces WHERE started_at < ? LIMIT ?
		)`
		if s.IsMySQL() {
			query = `DELETE FROM request_traces WHERE started_at < ? ORDER BY started_at LIMIT ?`
		}
		result, err := s.ExecContext(ctx, query, cutoffMs, batchSize)
		if err != nil {
			return fmt.Errorf("cleanup request traces: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count cleaned request traces: %w", err)
		}
		if affected < batchSize {
			return nil
		}
	}
}
