package model

// DashboardSnapshot is the bounded, single-request payload used by the new
// management console. It intentionally contains aggregates rather than logs.
type DashboardSnapshot struct {
	Range           string               `json:"range"`
	StartsAt        int64                `json:"starts_at"`
	EndsAt          int64                `json:"ends_at"`
	GeneratedAt     int64                `json:"generated_at"`
	Totals          DashboardTotals      `json:"totals"`
	Balances        []DashboardBalance   `json:"balances"`
	ModelUsage      []DashboardUsage     `json:"model_usage"`
	SiteUsage       []DashboardSiteUsage `json:"site_usage"`
	ClientUsage     []DashboardUsage     `json:"client_usage"`
	Trend           []MetricPoint        `json:"trend"`
	UnreadNotices   int                  `json:"unread_notices"`
	SiteCount       int                  `json:"site_count"`
	EnabledSites    int                  `json:"enabled_sites"`
	AccountCount    int                  `json:"account_count"`
	HealthyAccounts int                  `json:"healthy_accounts"`
	ChannelCount    int                  `json:"channel_count"`
	EnabledChannels int                  `json:"enabled_channels"`
}

type DashboardTotals struct {
	Requests            int     `json:"requests"`
	Success             int     `json:"success"`
	Errors              int     `json:"errors"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	Cost                float64 `json:"cost"`
	EffectiveCost       float64 `json:"effective_cost"`
}

type DashboardBalance struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
	Accounts int     `json:"accounts"`
}

type DashboardUsage struct {
	Key           string  `json:"key"`
	Label         string  `json:"label"`
	Requests      int     `json:"requests"`
	Success       int     `json:"success"`
	Errors        int     `json:"errors"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	EffectiveCost float64 `json:"effective_cost"`
	Share         float64 `json:"share"`
}

type DashboardSiteUsage struct {
	DashboardUsage
	SiteID int64 `json:"site_id"`
}
