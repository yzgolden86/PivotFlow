package model

// SiteAccountDailyBalance freezes one scheduled observation per account/day.
// Unknown includes a claimed operation interrupted before it could finish.
// A failed or interrupted read always retains a NULL balance.
type SiteAccountDailyBalance struct {
	SiteAccountID int64    `json:"site_account_id"`
	LocalDay      string   `json:"local_day"`
	Currency      string   `json:"currency"`
	Balance       *float64 `json:"balance"`
	Status        string   `json:"status"`
	ErrorCode     string   `json:"error_code,omitempty"`
	ScheduledAt   int64    `json:"scheduled_at"`
	CollectedAt   int64    `json:"collected_at"`
	CreatedAt     int64    `json:"created_at"`
}
