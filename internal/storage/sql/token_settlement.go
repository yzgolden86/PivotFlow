package sql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// ErrTokenSettlementConflict reports that an event ID was reused with a
// different receipt payload.
var ErrTokenSettlementConflict = errors.New("token settlement id conflict")

const applyTokenSettlementStatsQuery = `
	UPDATE auth_tokens
	SET
		success_count = success_count + 1,
		prompt_tokens_total = prompt_tokens_total + ?,
		completion_tokens_total = completion_tokens_total + ?,
		cache_read_tokens_total = cache_read_tokens_total + ?,
		cache_creation_tokens_total = cache_creation_tokens_total + ?,
		total_cost_usd = total_cost_usd + ?,
		effective_cost_usd = effective_cost_usd + ?,
		cost_used_microusd = cost_used_microusd + ?,
		stream_avg_ttfb = CASE
			WHEN ? = 1 THEN ((stream_avg_ttfb * stream_count) + ?) / (stream_count + 1)
			ELSE stream_avg_ttfb
		END,
		stream_count = stream_count + CASE WHEN ? = 1 THEN 1 ELSE 0 END,
		non_stream_avg_rt = CASE
			WHEN ? = 1 THEN ((non_stream_avg_rt * non_stream_count) + ?) / (non_stream_count + 1)
			ELSE non_stream_avg_rt
		END,
		non_stream_count = non_stream_count + CASE WHEN ? = 1 THEN 1 ELSE 0 END,
		last_used_at = CASE
			WHEN last_used_at < ? THEN ?
			ELSE last_used_at
		END
	WHERE token = ?
`

const syncTokenAccountingQuery = `
	UPDATE auth_tokens
	SET
		success_count = ?,
		failure_count = ?,
		stream_avg_ttfb = ?,
		non_stream_avg_rt = ?,
		stream_count = ?,
		non_stream_count = ?,
		prompt_tokens_total = ?,
		completion_tokens_total = ?,
		cache_read_tokens_total = ?,
		cache_creation_tokens_total = ?,
		total_cost_usd = ?,
		effective_cost_usd = ?,
		cost_used_microusd = ?,
		last_used_at = CASE
			WHEN last_used_at < ? THEN ?
			ELSE last_used_at
		END
	WHERE token = ?
`

func validateTokenSettlement(event model.TokenSettlement) error {
	if event.ID == "" || strings.TrimSpace(event.ID) != event.ID {
		return errors.New("token settlement id must be non-empty and trimmed")
	}
	if len(event.ID) > 191 {
		return errors.New("token settlement id exceeds 191 bytes")
	}
	if event.TokenHash == "" || strings.TrimSpace(event.TokenHash) != event.TokenHash {
		return errors.New("token settlement token_hash must be non-empty and trimmed")
	}
	if len(event.TokenHash) > 100 {
		return errors.New("token settlement token_hash exceeds 100 bytes")
	}
	if event.OccurredAt <= 0 {
		return errors.New("token settlement occurred_at must be a positive Unix millisecond timestamp")
	}
	if !nonNegativeFinite(event.Duration) {
		return errors.New("token settlement duration must be finite and nonnegative")
	}
	if !nonNegativeFinite(event.FirstByteTime) {
		return errors.New("token settlement first_byte_time must be finite and nonnegative")
	}
	if event.PromptTokens < 0 {
		return errors.New("token settlement prompt_tokens must be nonnegative")
	}
	if event.CompletionTokens < 0 {
		return errors.New("token settlement completion_tokens must be nonnegative")
	}
	if event.CacheReadTokens < 0 {
		return errors.New("token settlement cache_read_tokens must be nonnegative")
	}
	if event.CacheCreationTokens < 0 {
		return errors.New("token settlement cache_creation_tokens must be nonnegative")
	}
	if err := model.ValidateTokenSettlementCosts(event.CostUSD, event.EffectiveCostUSD); err != nil {
		return err
	}
	return nil
}

func nonNegativeFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func tokenSettlementMicroUSD(usd float64) int64 {
	return int64(math.Round(usd * 1_000_000))
}

func (s *SQLStore) tokenSettlementInsertQuery() string {
	if s.IsMySQL() {
		return `
			INSERT IGNORE INTO token_settlements (
				id, token_hash, occurred_at, duration, is_streaming, first_byte_time,
				prompt_tokens, completion_tokens, cache_read_tokens, cache_creation_tokens,
				cost_usd, effective_cost_usd, cost_invalid
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`
	}
	return `
		INSERT INTO token_settlements (
			id, token_hash, occurred_at, duration, is_streaming, first_byte_time,
			prompt_tokens, completion_tokens, cache_read_tokens, cache_creation_tokens,
			cost_usd, effective_cost_usd, cost_invalid
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`
}

func (s *SQLStore) existingTokenSettlementMatches(ctx context.Context, tx *sql.Tx, event model.TokenSettlement) (bool, error) {
	var (
		existing            model.TokenSettlement
		existingIsStreaming int
		existingCostInvalid int
	)
	query := `
		SELECT
			token_hash, occurred_at, duration, is_streaming, first_byte_time,
			prompt_tokens, completion_tokens, cache_read_tokens, cache_creation_tokens,
			cost_usd, effective_cost_usd, cost_invalid
		FROM token_settlements
		WHERE id = ?
	`
	if !s.IsSQLite() {
		// A concurrent INSERT IGNORE waits for the winning transaction, while
		// this transaction's earlier consistent read may predate its commit.
		query += " FOR UPDATE"
	}
	err := s.queryRowTx(ctx, tx, query, event.ID).Scan(
		&existing.TokenHash,
		&existing.OccurredAt,
		&existing.Duration,
		&existingIsStreaming,
		&existing.FirstByteTime,
		&existing.PromptTokens,
		&existing.CompletionTokens,
		&existing.CacheReadTokens,
		&existing.CacheCreationTokens,
		&existing.CostUSD,
		&existing.EffectiveCostUSD,
		&existingCostInvalid,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, errors.New("token settlement insert was ignored without an existing receipt")
	}
	if err != nil {
		return false, err
	}

	return existing.TokenHash == event.TokenHash &&
		existing.OccurredAt == event.OccurredAt &&
		existing.Duration == event.Duration &&
		(existingIsStreaming != 0) == event.IsStreaming &&
		existing.FirstByteTime == event.FirstByteTime &&
		existing.PromptTokens == event.PromptTokens &&
		existing.CompletionTokens == event.CompletionTokens &&
		existing.CacheReadTokens == event.CacheReadTokens &&
		existing.CacheCreationTokens == event.CacheCreationTokens &&
		existing.CostUSD == event.CostUSD &&
		existing.EffectiveCostUSD == event.EffectiveCostUSD &&
		(existingCostInvalid != 0) == event.CostInvalid, nil
}

// ApplyTokenSettlement atomically records one globally unique successful event
// and applies the same cumulative accounting fields as a successful
// UpdateTokenStats call. An identical duplicate receipt is a successful no-op;
// reusing the event ID with different accounting data returns a conflict.
func (s *SQLStore) ApplyTokenSettlement(ctx context.Context, event model.TokenSettlement) error {
	if err := validateTokenSettlement(event); err != nil {
		return err
	}

	return s.WithTransaction(ctx, func(tx *sql.Tx) error {
		requireToken := func() error {
			var exists int
			err := s.queryRowTx(ctx, tx, `
				SELECT 1 FROM auth_tokens WHERE token = ? LIMIT 1
			`, event.TokenHash).Scan(&exists)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("apply token settlement: %w", model.ErrAuthTokenNotFound)
			}
			if err != nil {
				return fmt.Errorf("check settlement token: %w", err)
			}
			return nil
		}

		result, err := s.execTx(ctx, tx, s.tokenSettlementInsertQuery(),
			event.ID,
			event.TokenHash,
			event.OccurredAt,
			event.Duration,
			event.IsStreaming,
			event.FirstByteTime,
			event.PromptTokens,
			event.CompletionTokens,
			event.CacheReadTokens,
			event.CacheCreationTokens,
			event.CostUSD,
			event.EffectiveCostUSD,
			event.CostInvalid,
		)
		if err != nil {
			return fmt.Errorf("insert token settlement receipt: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read token settlement insert result: %w", err)
		}
		if rows == 0 {
			matches, err := s.existingTokenSettlementMatches(ctx, tx, event)
			if err != nil {
				return fmt.Errorf("inspect existing token settlement: %w", err)
			}
			if !matches {
				return fmt.Errorf("%w: id=%s", ErrTokenSettlementConflict, event.ID)
			}
			if err := requireToken(); err != nil {
				return err
			}
			return nil
		}
		if err := requireToken(); err != nil {
			return err
		}

		streamUpdateFlag := event.IsStreaming && event.FirstByteTime > 0
		nonStreamUpdateFlag := !event.IsStreaming
		costMicroUSD := tokenSettlementMicroUSD(event.EffectiveCostUSD)
		result, err = s.execTx(ctx, tx, applyTokenSettlementStatsQuery,
			event.PromptTokens,
			event.CompletionTokens,
			event.CacheReadTokens,
			event.CacheCreationTokens,
			event.CostUSD,
			event.EffectiveCostUSD,
			costMicroUSD,
			streamUpdateFlag, event.FirstByteTime,
			streamUpdateFlag,
			nonStreamUpdateFlag, event.Duration,
			nonStreamUpdateFlag,
			event.OccurredAt, event.OccurredAt,
			event.TokenHash,
		)
		if err != nil {
			return fmt.Errorf("update token statistics from settlement: %w", err)
		}
		rows, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read token statistics update result: %w", err)
		}
		if rows == 0 {
			return fmt.Errorf("update token statistics: %w", model.ErrAuthTokenNotFound)
		}
		return nil
	})
}

func accountingValueRegresses(canonical, current float64) bool {
	if canonical >= current {
		return false
	}
	tolerance := math.Max(math.Abs(current)*1e-14, 1e-12)
	return current-canonical > tolerance
}

func tokenAccountingRegresses(current, canonical *model.AuthToken) bool {
	return canonical.SuccessCount < current.SuccessCount ||
		canonical.FailureCount < current.FailureCount ||
		canonical.StreamCount < current.StreamCount ||
		canonical.NonStreamCount < current.NonStreamCount ||
		canonical.PromptTokensTotal < current.PromptTokensTotal ||
		canonical.CompletionTokensTotal < current.CompletionTokensTotal ||
		canonical.CacheReadTokensTotal < current.CacheReadTokensTotal ||
		canonical.CacheCreationTokensTotal < current.CacheCreationTokensTotal ||
		accountingValueRegresses(canonical.TotalCostUSD, current.TotalCostUSD) ||
		accountingValueRegresses(canonical.EffectiveCostUSD, current.EffectiveCostUSD) ||
		canonical.CostUsedMicroUSD < current.CostUsedMicroUSD
}

// SyncTokenAccounting copies authoritative cumulative statistics to a replica.
// Cumulative counters, rather than use time, determine whether a snapshot is
// stale. Last-used time is merged independently so a newer use timestamp is
// never rolled back by delayed accounting.
func (s *SQLStore) SyncTokenAccounting(ctx context.Context, canonical *model.AuthToken) error {
	if canonical == nil {
		return errors.New("canonical auth token cannot be nil")
	}
	if canonical.Token == "" {
		return errors.New("canonical auth token hash cannot be empty")
	}

	return s.WithTransaction(ctx, func(tx *sql.Tx) error {
		query := `SELECT ` + strings.TrimSpace(authTokenSelectColumns) + ` FROM auth_tokens WHERE token = ?`
		current, err := scanAuthToken(s.queryRowTx(ctx, tx, query, canonical.Token))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sync token accounting: %w", model.ErrAuthTokenNotFound)
		}
		if err != nil {
			return fmt.Errorf("load replica token accounting: %w", err)
		}

		currentLastUsed := int64(0)
		if current.LastUsedAt != nil {
			currentLastUsed = *current.LastUsedAt
		}
		canonicalLastUsed := int64(0)
		if canonical.LastUsedAt != nil {
			canonicalLastUsed = *canonical.LastUsedAt
		}
		if tokenAccountingRegresses(current, canonical) {
			if canonicalLastUsed > currentLastUsed {
				_, err = s.execTx(ctx, tx, `
					UPDATE auth_tokens SET last_used_at = ? WHERE token = ?
				`, canonicalLastUsed, canonical.Token)
				if err != nil {
					return fmt.Errorf("merge canonical token last used: %w", err)
				}
			}
			return nil
		}

		_, err = s.execTx(ctx, tx, syncTokenAccountingQuery,
			canonical.SuccessCount,
			canonical.FailureCount,
			canonical.StreamAvgTTFB,
			canonical.NonStreamAvgRT,
			canonical.StreamCount,
			canonical.NonStreamCount,
			canonical.PromptTokensTotal,
			canonical.CompletionTokensTotal,
			canonical.CacheReadTokensTotal,
			canonical.CacheCreationTokensTotal,
			canonical.TotalCostUSD,
			canonical.EffectiveCostUSD,
			canonical.CostUsedMicroUSD,
			canonicalLastUsed, canonicalLastUsed,
			canonical.Token,
		)
		if err != nil {
			return fmt.Errorf("copy canonical token accounting: %w", err)
		}
		return nil
	})
}
