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

func (s *SQLStore) CreateSiteAccountBalanceSample(ctx context.Context, sample *model.SiteAccountDailyBalance) (bool, error) {
	if !validDailyBalanceIdentity(sample) || sample.Status != "unknown" || sample.Balance != nil || sample.CollectedAt != 0 || sample.ErrorCode != "" || len(sample.Currency) > 16 {
		return false, errors.New("invalid balance sample claim")
	}
	query := `INSERT INTO site_account_balance_samples(site_account_id,local_day,currency,balance,status,error_code,scheduled_at,collected_at,created_at) VALUES(?,?,?,NULL,'unknown','',?,0,?) ON CONFLICT(site_account_id,scheduled_at) DO NOTHING`
	if s.IsMySQL() {
		query = `INSERT INTO site_account_balance_samples(site_account_id,local_day,currency,balance,status,error_code,scheduled_at,collected_at,created_at) VALUES(?,?,?,NULL,'unknown','',?,0,?)`
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

func (s *SQLStore) CompleteSiteAccountBalanceSample(ctx context.Context, sample *model.SiteAccountDailyBalance) (bool, error) {
	if !validDailyBalanceIdentity(sample) || sample.CollectedAt < sample.ScheduledAt || len(sample.Currency) > 16 || len(sample.ErrorCode) > 64 {
		return false, errors.New("invalid balance sample completion")
	}
	currency := strings.ToUpper(strings.TrimSpace(sample.Currency))
	switch sample.Status {
	case "success":
		if sample.Balance == nil || math.IsNaN(*sample.Balance) || math.IsInf(*sample.Balance, 0) || currency == "" {
			return false, errors.New("balance sample success requires a finite balance and currency")
		}
	case "failed":
		if sample.Balance != nil {
			return false, errors.New("failed balance sample must remain unknown")
		}
	default:
		return false, errors.New("invalid balance sample status")
	}
	result, err := s.ExecContext(ctx, `UPDATE site_account_balance_samples SET currency=?,balance=?,status=?,error_code=?,collected_at=? WHERE site_account_id=? AND scheduled_at=? AND local_day=? AND status='unknown'`, currency, sample.Balance, sample.Status, sample.ErrorCode, sample.CollectedAt, sample.SiteAccountID, sample.ScheduledAt, sample.LocalDay)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (s *SQLStore) ListSiteAccountBalanceSamples(ctx context.Context, sinceDay, untilDay string) ([]*model.SiteAccountDailyBalance, error) {
	query := `SELECT site_account_id,local_day,currency,balance,status,error_code,scheduled_at,collected_at,created_at FROM site_account_balance_samples WHERE 1=1`
	args := make([]any, 0, 2)
	if sinceDay != "" {
		query += " AND local_day>=?"
		args = append(args, sinceDay)
	}
	if untilDay != "" {
		query += " AND local_day<=?"
		args = append(args, untilDay)
	}
	query += " ORDER BY local_day,scheduled_at,currency,site_account_id"
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

func (s *SQLStore) DeleteSiteAccountBalanceSamplesBefore(ctx context.Context, beforeDay string, limit int) (int64, error) {
	day, err := time.Parse("2006-01-02", beforeDay)
	if err != nil || day.Format("2006-01-02") != beforeDay || limit <= 0 {
		return 0, errors.New("invalid balance sample cleanup bounds")
	}
	var removed int64
	err = s.WithTransaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, s.q(`SELECT site_account_id,scheduled_at FROM site_account_balance_samples WHERE local_day<? ORDER BY local_day,scheduled_at,site_account_id LIMIT ?`), beforeDay, limit)
		if err != nil {
			return err
		}
		keys := make([][2]int64, 0)
		for rows.Next() {
			var key [2]int64
			if err := rows.Scan(&key[0], &key[1]); err != nil {
				_ = rows.Close()
				return err
			}
			keys = append(keys, key)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		// Explicit composite keys avoid DELETE LIMIT and MySQL's restriction on
		// LIMIT inside an IN subquery. Small statements also bound placeholders.
		var total int64
		for start := 0; start < len(keys); start += 400 {
			batch := keys[start:min(start+400, len(keys))]
			args := make([]any, 0, 2*len(batch))
			for _, key := range batch {
				args = append(args, key[0], key[1])
			}
			query := `DELETE FROM site_account_balance_samples WHERE (site_account_id,scheduled_at) IN (` + strings.TrimSuffix(strings.Repeat("(?,?),", len(batch)), ",") + `)`
			result, err := s.execTx(ctx, tx, query, args...)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			total += count
		}
		removed = total
		return nil
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}
