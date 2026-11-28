package app

import (
	"context"
	"testing"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

type balanceWindowStore struct {
	storage.Store
	from, until string
}

func (s *balanceWindowStore) ListSiteAccountDailyBalances(_ context.Context, from, until string) ([]*model.SiteAccountDailyBalance, error) {
	s.from, s.until = from, until
	return nil, nil
}

func (s *balanceWindowStore) ListSiteAccountBalanceSamples(_ context.Context, from, until string) ([]*model.SiteAccountDailyBalance, error) {
	s.from, s.until = from, until
	return nil, nil
}

func TestBalanceWindowIndependentOfRequestRange(t *testing.T) {
	end := time.Date(2026, 11, 25, 17, 30, 0, 0, time.UTC) // Nov 26 in Shanghai.
	for _, name := range []string{"today", "this_week", "this_month", "last_month"} {
		t.Run(name, func(t *testing.T) {
			store := &balanceWindowStore{}
			server := &Server{store: store}
			server.siteBalanceHistory(context.Background(), end.Add(-time.Hour), end, name)
			if store.from != "2026-11-12" || store.until != "2026-11-26" {
				t.Fatalf("requested %s..%s, want 14 visible days plus prior baseline through server Shanghai day", store.from, store.until)
			}
		})
	}
}
