package sql

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/yzgolden86/PivotFlow/internal/model"
)

func validDailyBalanceIdentity(sample *model.SiteAccountDailyBalance) bool {
	if sample == nil || sample.SiteAccountID <= 0 || sample.ScheduledAt <= 0 {
		return false
	}
	day, err := time.Parse("2006-01-02", sample.LocalDay)
	return err == nil && day.Format("2006-01-02") == sample.LocalDay
}

func (s *SQLStore) UpdateSiteAccountBalance(ctx context.Context, id int64, balance float64, currency string, updatedAt int64) error {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if id <= 0 || updatedAt <= 0 || math.IsNaN(balance) || math.IsInf(balance, 0) || currency == "" || len(currency) > 16 {
		return errors.New("invalid balance observation")
	}
	_, err := s.ExecContext(ctx, `UPDATE site_accounts SET balance=?,balance_currency=?,balance_updated_at=? WHERE id=? AND deleted_at=0 AND balance_updated_at<?`, balance, currency, updatedAt, id, updatedAt)
	return err
}

func (s *SQLStore) CreateSiteAccountDailyBalance(ctx context.Context, sample *model.SiteAccountDailyBalance) (bool, error) {
	if !validDailyBalanceIdentity(sample) || sample.Status != "unknown" || sample.Balance != nil || len(sample.Currency) > 16 {
		return false, errors.New("invalid daily balance claim")
	}
	query := `INSERT INTO site_account_daily_balances(site_account_id,local_day,currency,balance,status,error_code,scheduled_at,collected_at,created_at) VALUES(?,?,?,NULL,'unknown','',?,0,?) ON CONFLICT(site_account_id,local_day) DO NOTHING`
	if s.IsMySQL() {
		query = `INSERT INTO site_account_daily_balances(site_account_id,local_day,currency,balance,status,error_code,scheduled_at,collected_at,created_at) VALUES(?,?,?,NULL,'unknown','',?,0,?)`
	}
	result, err := s.ExecContext(ctx, query, sample.SiteAccountID, sample.LocalDay, strings.ToUpper(strings.TrimSpace(sample.Currency)), sample.ScheduledAt, siteNow())
	if err != nil {
		var duplicate *mysqlDriver.MySQLError
		if s.IsMySQL() && errors.As(err, &duplicate) && duplicate.Number == 1062 {
			return false, nil
		}
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (s *SQLStore) CompleteSiteAccountDailyBalance(ctx context.Context, sample *model.SiteAccountDailyBalance) (bool, error) {
	if !validDailyBalanceIdentity(sample) || sample.CollectedAt < sample.ScheduledAt || len(sample.Currency) > 16 || len(sample.ErrorCode) > 64 {
		return false, errors.New("invalid daily balance completion")
	}
	currency := strings.ToUpper(strings.TrimSpace(sample.Currency))
	switch sample.Status {
	case "success":
		if sample.Balance == nil || math.IsNaN(*sample.Balance) || math.IsInf(*sample.Balance, 0) || currency == "" {
			return false, errors.New("daily balance success requires a finite balance and currency")
		}
	case "failed":
		if sample.Balance != nil {
			return false, errors.New("failed daily balance must remain unknown")
		}
	default:
		return false, errors.New("invalid daily balance status")
	}
	result, err := s.ExecContext(ctx, `UPDATE site_account_daily_balances SET currency=?,balance=?,status=?,error_code=?,collected_at=? WHERE site_account_id=? AND local_day=? AND scheduled_at=? AND status='unknown'`, currency, sample.Balance, sample.Status, sample.ErrorCode, sample.CollectedAt, sample.SiteAccountID, sample.LocalDay, sample.ScheduledAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (s *SQLStore) ListSiteAccountDailyBalances(ctx context.Context, sinceDay, untilDay string) ([]*model.SiteAccountDailyBalance, error) {
	// Soft-deleting an account must not rewrite previously recorded coverage.
	query := `SELECT site_account_id,local_day,currency,balance,status,error_code,scheduled_at,collected_at,created_at FROM site_account_daily_balances WHERE 1=1`
	args := make([]any, 0, 2)
	if sinceDay != "" {
		query += " AND local_day>=?"
		args = append(args, sinceDay)
	}
	if untilDay != "" {
		query += " AND local_day<=?"
		args = append(args, untilDay)
	}
	query += " ORDER BY local_day,currency,site_account_id"
	rows, err := s.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]*model.SiteAccountDailyBalance, 0)
	for rows.Next() {
		item := &model.SiteAccountDailyBalance{}
		var balance sql.NullFloat64
		if err := rows.Scan(&item.SiteAccountID, &item.LocalDay, &item.Currency, &balance, &item.Status, &item.ErrorCode, &item.ScheduledAt, &item.CollectedAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		if balance.Valid {
			item.Balance = &balance.Float64
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
