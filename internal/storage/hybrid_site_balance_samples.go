package storage

import (
	"context"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// Interval observations are durable history: reads and writes use the primary,
// independently of the optional SQLite replica and its retention window.
func (h *HybridStore) CreateSiteAccountBalanceSample(ctx context.Context, sample *model.SiteAccountDailyBalance) (bool, error) {
	return h.mysql.CreateSiteAccountBalanceSample(ctx, sample)
}

func (h *HybridStore) CompleteSiteAccountBalanceSample(ctx context.Context, sample *model.SiteAccountDailyBalance) (bool, error) {
	return h.mysql.CompleteSiteAccountBalanceSample(ctx, sample)
}

func (h *HybridStore) ListSiteAccountBalanceSamples(ctx context.Context, sinceDay, untilDay string) ([]*model.SiteAccountDailyBalance, error) {
	return h.mysql.ListSiteAccountBalanceSamples(ctx, sinceDay, untilDay)
}

func (h *HybridStore) DeleteSiteAccountBalanceSamplesBefore(ctx context.Context, beforeDay string, limit int) (int64, error) {
	return h.mysql.DeleteSiteAccountBalanceSamplesBefore(ctx, beforeDay, limit)
}
