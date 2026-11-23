package model

import (
	"fmt"
	"math"
)

// TokenSettlement is the durable accounting record for one successful request.
// ID is globally unique and OccurredAt is the event occurrence time in Unix
// milliseconds.
type TokenSettlement struct {
	ID                  string  `json:"id"`
	TokenHash           string  `json:"token_hash"`
	OccurredAt          int64   `json:"occurred_at"`
	Duration            float64 `json:"duration"`
	IsStreaming         bool    `json:"is_streaming"`
	FirstByteTime       float64 `json:"first_byte_time"`
	PromptTokens        int64   `json:"prompt_tokens"`
	CompletionTokens    int64   `json:"completion_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CostUSD             float64 `json:"cost_usd"`
	EffectiveCostUSD    float64 `json:"effective_cost_usd"`
	// CostInvalid marks zero cost fields as an unknown-price placeholder, not
	// as a genuinely free request.
	CostInvalid bool `json:"cost_invalid,omitempty"`
}

// ValidateTokenSettlementCosts validates the cost fields accepted by token
// settlement storage. The effective cost must also convert to int64 microUSD.
func ValidateTokenSettlementCosts(costUSD, effectiveCostUSD float64) error {
	if math.IsNaN(costUSD) || math.IsInf(costUSD, 0) || costUSD < 0 {
		return fmt.Errorf("token settlement cost_usd must be finite and nonnegative")
	}
	if math.IsNaN(effectiveCostUSD) || math.IsInf(effectiveCostUSD, 0) || effectiveCostUSD < 0 {
		return fmt.Errorf("token settlement effective_cost_usd must be finite and nonnegative")
	}
	scaled := math.Round(effectiveCostUSD * 1_000_000)
	if math.IsInf(scaled, 0) || scaled >= float64(math.MaxInt64) {
		return fmt.Errorf("token settlement effective_cost_usd exceeds int64 micro-dollar range")
	}
	return nil
}
