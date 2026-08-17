package sql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

func siteNow() int64 { return time.Now().UnixMilli() }

func (s *SQLStore) insertID(ctx context.Context, tx *sql.Tx, table string, columns string, values []any) (int64, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, columns, placeholders)
	if s.IsPostgres() {
		query += " RETURNING id"
		var id int64
		if err := s.queryRowTx(ctx, tx, query, values...).Scan(&id); err != nil {
			return 0, err
		}
		return id, nil
	}
	result, err := s.execTx(ctx, tx, query, values...)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func scanSite(scanner interface{ Scan(...any) error }) (*model.Site, error) {
	var site model.Site
	var enabled, useSystemProxy int
	if err := scanner.Scan(&site.ID, &site.Name, &site.Platform, &site.BaseURL, &enabled, &site.Timezone, &useSystemProxy, &site.ProxyURL, &site.ExternalCheckinURL, &site.TagsJSON, &site.LastProbeStatus, &site.LastError, &site.CreatedAt, &site.UpdatedAt, &site.DeletedAt); err != nil {
		return nil, err
	}
	site.Enabled = enabled != 0
	site.UseSystemProxy = useSystemProxy != 0
	return &site, nil
}

const siteColumns = `id, name, platform, base_url, enabled, timezone, use_system_proxy, proxy_url, external_checkin_url, tags_json, last_probe_status, last_error, created_at, updated_at, deleted_at`

func (s *SQLStore) ListSites(ctx context.Context, filter model.SiteListFilter) ([]*model.Site, error) {
	query := "SELECT " + siteColumns + " FROM sites"
	if !filter.IncludeDeleted {
		query += " WHERE deleted_at = 0"
	}
	query += " ORDER BY updated_at DESC, id DESC"
	rows, err := s.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]*model.Site, 0)
	for rows.Next() {
		item, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *SQLStore) GetSite(ctx context.Context, id int64) (*model.Site, error) {
	row := s.QueryRowContext(ctx, "SELECT "+siteColumns+" FROM sites WHERE id = ?", id)
	site, err := scanSite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("not found")
	}
	return site, err
}

func (s *SQLStore) CreateSite(ctx context.Context, site *model.Site) (*model.Site, error) {
	if site == nil {
		return nil, errors.New("site cannot be nil")
	}
	now := siteNow()
	if strings.TrimSpace(site.Timezone) == "" {
		site.Timezone = "Asia/Shanghai"
	}
	if strings.TrimSpace(site.Platform) == "" {
		site.Platform = model.SitePlatformUnknown
	}
	if strings.TrimSpace(site.TagsJSON) == "" {
		site.TagsJSON = "[]"
	}
	if site.LastProbeStatus == "" {
		site.LastProbeStatus = "unknown"
	}
	if site.LastError == "" {
		site.LastError = ""
	}
	var id int64
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		// The table has a historical UNIQUE(name) constraint while deletion is
		// soft. Release a tombstoned row left by older versions before inserting
		// the replacement, otherwise users can never reuse a deleted site name.
		var existingID, deletedAt int64
		queryErr := s.queryRowTx(ctx, tx, "SELECT id,deleted_at FROM sites WHERE name=?", site.Name).Scan(&existingID, &deletedAt)
		if queryErr != nil && !errors.Is(queryErr, sql.ErrNoRows) {
			return queryErr
		}
		if queryErr == nil {
			if deletedAt == 0 {
				return errors.New("site_name_exists")
			}
			if _, err := s.execTx(ctx, tx, "UPDATE sites SET name=?,updated_at=? WHERE id=? AND deleted_at<>0", deletedSiteName(existingID, now), now, existingID); err != nil {
				return err
			}
		}
		var err error
		id, err = s.insertID(ctx, tx, "sites", "name,platform,base_url,enabled,timezone,use_system_proxy,proxy_url,external_checkin_url,tags_json,last_probe_status,last_error,created_at,updated_at,deleted_at", []any{site.Name, site.Platform, site.BaseURL, site.Enabled, site.Timezone, site.UseSystemProxy, site.ProxyURL, site.ExternalCheckinURL, site.TagsJSON, site.LastProbeStatus, site.LastError, now, now, 0})
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetSite(ctx, id)
}

func (s *SQLStore) UpdateSite(ctx context.Context, id int64, site *model.Site) (*model.Site, error) {
	if site == nil {
		return nil, errors.New("site cannot be nil")
	}
	now := siteNow()
	_, err := s.ExecContext(ctx, `UPDATE sites SET name=?, platform=?, base_url=?, enabled=?, timezone=?, use_system_proxy=?, proxy_url=?, external_checkin_url=?, tags_json=?, last_probe_status=?, last_error=?, updated_at=? WHERE id=? AND deleted_at=0`, site.Name, site.Platform, site.BaseURL, site.Enabled, site.Timezone, site.UseSystemProxy, site.ProxyURL, site.ExternalCheckinURL, site.TagsJSON, site.LastProbeStatus, site.LastError, now, id)
	if err != nil {
		return nil, err
	}
	return s.GetSite(ctx, id)
}

func (s *SQLStore) DeleteSite(ctx context.Context, id int64) error {
	now := siteNow()
	return s.WithTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := s.execTx(ctx, tx, "UPDATE sites SET name=?,enabled=0,deleted_at=?,updated_at=? WHERE id=? AND deleted_at=0", deletedSiteName(id, now), now, now, id); err != nil {
			return err
		}
		if _, err := s.execTx(ctx, tx, "UPDATE site_accounts SET enabled=0, status=?, updated_at=? WHERE site_id=? AND deleted_at=0", model.SiteAccountStatusDisabled, now, id); err != nil {
			return err
		}
		if _, err := s.execTx(ctx, tx, "UPDATE channels SET enabled=0, updated_at=? WHERE id IN (SELECT b.channel_id FROM site_channel_bindings b JOIN site_accounts a ON a.id=b.site_account_id WHERE a.site_id=? AND b.channel_id IS NOT NULL)", now, id); err != nil {
			return err
		}
		_, err := s.execTx(ctx, tx, "UPDATE site_channel_bindings SET status='disabled', last_sync_status='success', last_sync_error='', updated_at=? WHERE site_account_id IN (SELECT id FROM site_accounts WHERE site_id=?)", now, id)
		return err
	})
}

func deletedSiteName(id, deletedAt int64) string {
	return fmt.Sprintf("__deleted_site_%d_%d", id, deletedAt)
}

func scanSiteAccount(scanner interface{ Scan(...any) error }) (*model.SiteAccount, error) {
	var a model.SiteAccount
	var enabled, autoCheckin, autoRefresh int
	var balance sql.NullFloat64
	if err := scanner.Scan(&a.ID, &a.SiteID, &a.Label, &a.CredentialType, &a.CredentialCiphertext, &a.CredentialKeyVersion, &enabled, &autoCheckin, &autoRefresh, &a.Timezone, &a.Status, &balance, &a.BalanceCurrency, &a.BalanceUpdatedAt, &a.LastRefreshAt, &a.LastRefreshStatus, &a.ConsecutiveFailures, &a.LastCheckinAt, &a.LastCheckinStatus, &a.LastError, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt); err != nil {
		return nil, err
	}
	a.Enabled, a.AutoCheckin, a.AutoRefresh = enabled != 0, autoCheckin != 0, autoRefresh != 0
	if balance.Valid {
		a.Balance = &balance.Float64
	}
	a.CredentialConfigured = a.CredentialCiphertext != ""
	return &a, nil
}

const siteAccountColumns = `id, site_id, label, credential_type, credential_ciphertext, credential_key_version, enabled, auto_checkin, auto_refresh, timezone, status, balance, balance_currency, balance_updated_at, last_refresh_at, last_refresh_status, consecutive_failures, last_checkin_at, last_checkin_status, last_error, created_at, updated_at, deleted_at`

func (s *SQLStore) ListSiteAccounts(ctx context.Context, siteID int64, includeDeleted bool) ([]*model.SiteAccount, error) {
	query := "SELECT " + siteAccountColumns + " FROM site_accounts WHERE 1=1"
	args := make([]any, 0, 1)
	if siteID > 0 {
		query += " AND site_id=?"
		args = append(args, siteID)
	}
	if !includeDeleted {
		query += " AND deleted_at=0"
	}
	query += " ORDER BY id ASC"
	rows, err := s.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]*model.SiteAccount, 0)
	for rows.Next() {
		item, err := scanSiteAccount(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *SQLStore) GetSiteAccount(ctx context.Context, id int64) (*model.SiteAccount, error) {
	row := s.QueryRowContext(ctx, "SELECT "+siteAccountColumns+" FROM site_accounts WHERE id=?", id)
	a, err := scanSiteAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("not found")
	}
	return a, err
}

func (s *SQLStore) CreateSiteAccount(ctx context.Context, account *model.SiteAccount) (*model.SiteAccount, error) {
	if account == nil {
		return nil, errors.New("site account cannot be nil")
	}
	now := siteNow()
	if account.Status == "" {
		account.Status = model.SiteAccountStatusUnknown
	}
	if account.BalanceCurrency == "" {
		account.BalanceCurrency = "CNY"
	}
	if account.LastError == "" {
		account.LastError = ""
	}
	var id int64
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		var exists int
		if err := s.queryRowTx(ctx, tx, "SELECT COUNT(1) FROM sites WHERE id=? AND deleted_at=0", account.SiteID).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return errors.New("site not found")
		}
		if err := s.queryRowTx(ctx, tx, "SELECT COUNT(1) FROM site_accounts WHERE site_id=? AND label=? AND deleted_at=0", account.SiteID, account.Label).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			return errors.New("site account label already exists")
		}
		var insertErr error
		id, insertErr = s.insertID(ctx, tx, "site_accounts", "site_id, label, credential_type, credential_ciphertext, credential_key_version, enabled, auto_checkin, auto_refresh, timezone, status, balance, balance_currency, balance_updated_at, last_refresh_at, last_refresh_status, consecutive_failures, last_checkin_at, last_checkin_status, last_error, created_at, updated_at, deleted_at", []any{account.SiteID, account.Label, account.CredentialType, account.CredentialCiphertext, account.CredentialKeyVersion, account.Enabled, account.AutoCheckin, account.AutoRefresh, account.Timezone, account.Status, account.Balance, account.BalanceCurrency, account.BalanceUpdatedAt, account.LastRefreshAt, account.LastRefreshStatus, account.ConsecutiveFailures, account.LastCheckinAt, account.LastCheckinStatus, account.LastError, now, now, 0})
		return insertErr
	})
	if err != nil {
		return nil, err
	}
	return s.GetSiteAccount(ctx, id)
}

func (s *SQLStore) UpdateSiteAccount(ctx context.Context, id int64, account *model.SiteAccount) (*model.SiteAccount, error) {
	if account == nil {
		return nil, errors.New("site account cannot be nil")
	}
	now := siteNow()
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := s.execTx(ctx, tx, `UPDATE site_accounts SET label=?, enabled=?, auto_checkin=?, auto_refresh=?, timezone=?, status=?, balance=?, balance_currency=?, balance_updated_at=?, last_refresh_at=?, last_refresh_status=?, consecutive_failures=?, last_checkin_at=?, last_checkin_status=?, last_error=?, updated_at=? WHERE id=? AND deleted_at=0`, account.Label, account.Enabled, account.AutoCheckin, account.AutoRefresh, account.Timezone, account.Status, account.Balance, account.BalanceCurrency, account.BalanceUpdatedAt, account.LastRefreshAt, account.LastRefreshStatus, account.ConsecutiveFailures, account.LastCheckinAt, account.LastCheckinStatus, account.LastError, now, id); err != nil {
			return err
		}
		if account.Enabled && account.Status != model.SiteAccountStatusExpired && account.Status != model.SiteAccountStatusDisabled {
			return nil
		}
		if _, err := s.execTx(ctx, tx, "UPDATE channels SET enabled=0, updated_at=? WHERE id IN (SELECT channel_id FROM site_channel_bindings WHERE site_account_id=? AND channel_id IS NOT NULL)", now, id); err != nil {
			return err
		}
		_, err := s.execTx(ctx, tx, "UPDATE site_channel_bindings SET status='disabled', updated_at=? WHERE site_account_id=?", now, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetSiteAccount(ctx, id)
}

func (s *SQLStore) UpdateSiteAccountCredential(ctx context.Context, id int64, credentialType, ciphertext, keyVersion string) error {
	if strings.TrimSpace(ciphertext) == "" || strings.TrimSpace(keyVersion) == "" {
		return errors.New("credential payload is required")
	}
	result, err := s.ExecContext(ctx, `UPDATE site_accounts SET credential_type=?, credential_ciphertext=?, credential_key_version=?, updated_at=? WHERE id=? AND deleted_at=0`, strings.TrimSpace(credentialType), ciphertext, strings.TrimSpace(keyVersion), siteNow(), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected == 0 {
		return errors.New("not found")
	}
	return err
}

func (s *SQLStore) DeleteSiteAccount(ctx context.Context, id int64) error {
	now := siteNow()
	return s.WithTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := s.execTx(ctx, tx, "UPDATE site_accounts SET enabled=0, status=?, deleted_at=?, updated_at=? WHERE id=? AND deleted_at=0", model.SiteAccountStatusDisabled, now, now, id); err != nil {
			return err
		}
		if _, err := s.execTx(ctx, tx, "UPDATE channels SET enabled=0, updated_at=? WHERE id IN (SELECT channel_id FROM site_channel_bindings WHERE site_account_id=? AND channel_id IS NOT NULL)", now, id); err != nil {
			return err
		}
		_, err := s.execTx(ctx, tx, "UPDATE site_channel_bindings SET status='disabled', updated_at=? WHERE site_account_id=?", now, id)
		return err
	})
}

func (s *SQLStore) ReplaceSiteAccountModels(ctx context.Context, accountID int64, models []model.SiteAccountModel) error {
	now := siteNow()
	return s.WithTransaction(ctx, func(tx *sql.Tx) error {
		if _, err := s.execTx(ctx, tx, "UPDATE site_account_models SET stale=1, updated_at=? WHERE site_account_id=?", now, accountID); err != nil {
			return err
		}
		for _, item := range models {
			_, err := s.execTx(ctx, tx, `INSERT INTO site_account_models(site_account_id, model, route_type, source, disabled, stale, last_seen_at, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(site_account_id,model) DO UPDATE SET route_type=excluded.route_type, source=excluded.source, stale=0, last_seen_at=excluded.last_seen_at, updated_at=excluded.updated_at`, accountID, item.Model, item.RouteType, item.Source, item.Disabled, false, now, now, now)
			if err != nil && !s.IsSQLite() && !s.IsPostgres() {
				_, err = s.execTx(ctx, tx, `INSERT INTO site_account_models(site_account_id, model, route_type, source, disabled, stale, last_seen_at, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE route_type=VALUES(route_type), source=VALUES(source), stale=0, last_seen_at=VALUES(last_seen_at), updated_at=VALUES(updated_at)`, accountID, item.Model, item.RouteType, item.Source, item.Disabled, false, now, now, now)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SQLStore) ListSiteAccountModels(ctx context.Context, filter model.SiteModelFilter) ([]model.SiteAccountModel, error) {
	query := "SELECT m.site_account_id,m.model,m.route_type,m.source,m.disabled,m.stale,m.last_seen_at,m.created_at,m.updated_at FROM site_account_models m JOIN site_accounts a ON a.id=m.site_account_id WHERE a.deleted_at=0"
	args := make([]any, 0, 4)
	if filter.SiteID > 0 {
		query += " AND a.site_id=?"
		args = append(args, filter.SiteID)
	}
	if filter.SiteAccountID > 0 {
		query += " AND m.site_account_id=?"
		args = append(args, filter.SiteAccountID)
	}
	if !filter.IncludeDisabled {
		query += " AND m.disabled=0"
	}
	query += " ORDER BY m.model ASC"
	if filter.Limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, filter.Limit, max(0, filter.Offset))
	}
	rows, err := s.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]model.SiteAccountModel, 0)
	for rows.Next() {
		var x model.SiteAccountModel
		var disabled, stale int
		if err := rows.Scan(&x.SiteAccountID, &x.Model, &x.RouteType, &x.Source, &disabled, &stale, &x.LastSeenAt, &x.CreatedAt, &x.UpdatedAt); err != nil {
			return nil, err
		}
		x.Disabled = disabled != 0
		x.Stale = stale != 0
		result = append(result, x)
	}
	return result, rows.Err()
}

func (s *SQLStore) UpsertSiteAnnouncements(ctx context.Context, announcements []model.SiteAnnouncement) error {
	now := siteNow()
	return s.WithTransaction(ctx, func(tx *sql.Tx) error {
		for _, a := range announcements {
			if a.FirstSeenAt == 0 {
				a.FirstSeenAt = now
			}
			a.LastSeenAt = now
			if a.CreatedAt == 0 {
				a.CreatedAt = now
			}
			a.UpdatedAt = now
			query := `INSERT INTO site_announcements(site_id,source_key,title,content_markdown,level,source_url,upstream_created_at,upstream_updated_at,first_seen_at,last_seen_at,read_at,content_hash,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(site_id,source_key) DO UPDATE SET title=excluded.title,content_markdown=excluded.content_markdown,level=excluded.level,source_url=excluded.source_url,upstream_created_at=excluded.upstream_created_at,upstream_updated_at=excluded.upstream_updated_at,last_seen_at=excluded.last_seen_at,content_hash=excluded.content_hash,updated_at=excluded.updated_at`
			_, err := s.execTx(ctx, tx, query, a.SiteID, a.SourceKey, a.Title, a.ContentMarkdown, a.Level, a.SourceURL, a.UpstreamCreatedAt, a.UpstreamUpdatedAt, a.FirstSeenAt, a.LastSeenAt, a.ReadAt, a.ContentHash, a.CreatedAt, a.UpdatedAt)
			if err != nil && s.IsMySQL() {
				_, err = s.execTx(ctx, tx, `INSERT INTO site_announcements(site_id,source_key,title,content_markdown,level,source_url,upstream_created_at,upstream_updated_at,first_seen_at,last_seen_at,read_at,content_hash,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE title=VALUES(title),content_markdown=VALUES(content_markdown),level=VALUES(level),source_url=VALUES(source_url),upstream_created_at=VALUES(upstream_created_at),upstream_updated_at=VALUES(upstream_updated_at),last_seen_at=VALUES(last_seen_at),content_hash=VALUES(content_hash),updated_at=VALUES(updated_at)`, a.SiteID, a.SourceKey, a.Title, a.ContentMarkdown, a.Level, a.SourceURL, a.UpstreamCreatedAt, a.UpstreamUpdatedAt, a.FirstSeenAt, a.LastSeenAt, a.ReadAt, a.ContentHash, a.CreatedAt, a.UpdatedAt)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SQLStore) ListSiteAnnouncements(ctx context.Context, filter model.SiteAnnouncementFilter) ([]*model.SiteAnnouncement, int, error) {
	where := " WHERE 1=1"
	args := []any{}
	if filter.SiteID > 0 {
		where += " AND site_id=?"
		args = append(args, filter.SiteID)
	}
	if filter.Unread != nil {
		if *filter.Unread {
			where += " AND read_at=0"
		} else {
			where += " AND read_at>0"
		}
	}
	var count int
	if err := s.QueryRowContext(ctx, "SELECT COUNT(1) FROM site_announcements"+where, args...).Scan(&count); err != nil {
		return nil, 0, err
	}
	query := "SELECT id,site_id,source_key,title,content_markdown,level,source_url,upstream_created_at,upstream_updated_at,first_seen_at,last_seen_at,read_at,content_hash,created_at,updated_at FROM site_announcements" + where + " ORDER BY last_seen_at DESC"
	if filter.Limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, filter.Limit, max(0, filter.Offset))
	}
	rows, err := s.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]*model.SiteAnnouncement, 0)
	for rows.Next() {
		a := new(model.SiteAnnouncement)
		if err := rows.Scan(&a.ID, &a.SiteID, &a.SourceKey, &a.Title, &a.ContentMarkdown, &a.Level, &a.SourceURL, &a.UpstreamCreatedAt, &a.UpstreamUpdatedAt, &a.FirstSeenAt, &a.LastSeenAt, &a.ReadAt, &a.ContentHash, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, 0, err
		}
		result = append(result, a)
	}
	return result, count, rows.Err()
}

func (s *SQLStore) MarkSiteAnnouncementRead(ctx context.Context, id int64) error {
	_, err := s.ExecContext(ctx, "UPDATE site_announcements SET read_at=?,updated_at=? WHERE id=?", siteNow(), siteNow(), id)
	return err
}
func (s *SQLStore) MarkAllSiteAnnouncementsRead(ctx context.Context, siteID int64) error {
	now := siteNow()
	if siteID > 0 {
		_, err := s.ExecContext(ctx, "UPDATE site_announcements SET read_at=?,updated_at=? WHERE site_id=? AND read_at=0", now, now, siteID)
		return err
	}
	_, err := s.ExecContext(ctx, "UPDATE site_announcements SET read_at=?,updated_at=? WHERE read_at=0", now, now)
	return err
}

func (s *SQLStore) CreateCheckinRun(ctx context.Context, run *model.CheckinRun) (*model.CheckinRun, error) {
	if run == nil {
		return nil, errors.New("checkin run cannot be nil")
	}
	now := siteNow()
	var id int64
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = s.insertID(ctx, tx, "checkin_runs", "trigger_type,local_day,timezone,status,total,success_count,already_count,browser_required_count,unsupported_count,failed_count,started_at,finished_at,last_error", []any{run.Trigger, run.LocalDay, run.Timezone, run.Status, run.Total, run.SuccessCount, run.AlreadyCount, run.BrowserRequiredCount, run.UnsupportedCount, run.FailedCount, now, 0, run.LastError})
		return err
	})
	if err != nil {
		return nil, err
	}
	run.ID = id
	run.StartedAt = now
	return run, nil
}
func (s *SQLStore) UpdateCheckinRun(ctx context.Context, run *model.CheckinRun) error {
	if run == nil {
		return errors.New("checkin run cannot be nil")
	}
	_, err := s.ExecContext(ctx, "UPDATE checkin_runs SET status=?,total=?,success_count=?,already_count=?,browser_required_count=?,unsupported_count=?,failed_count=?,finished_at=?,last_error=? WHERE id=?", run.Status, run.Total, run.SuccessCount, run.AlreadyCount, run.BrowserRequiredCount, run.UnsupportedCount, run.FailedCount, run.FinishedAt, run.LastError, run.ID)
	return err
}
func (s *SQLStore) GetCheckinRun(ctx context.Context, id int64) (*model.CheckinRun, error) {
	r := new(model.CheckinRun)
	err := s.QueryRowContext(ctx, "SELECT id,trigger_type,local_day,timezone,status,total,success_count,already_count,browser_required_count,unsupported_count,failed_count,started_at,finished_at,last_error FROM checkin_runs WHERE id=?", id).Scan(&r.ID, &r.Trigger, &r.LocalDay, &r.Timezone, &r.Status, &r.Total, &r.SuccessCount, &r.AlreadyCount, &r.BrowserRequiredCount, &r.UnsupportedCount, &r.FailedCount, &r.StartedAt, &r.FinishedAt, &r.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("not found")
	}
	return r, err
}
func (s *SQLStore) ListCheckinAttempts(ctx context.Context, accountID int64, limit int) ([]*model.CheckinAttempt, error) {
	q := "SELECT id,run_id,site_account_id,provider_id,local_day,trigger_scope,status,reward_text,balance_before,balance_after,balance_delta,balance_currency,message,error_code,retry_after_at,started_at,finished_at,attempt_no FROM checkin_attempts WHERE site_account_id=? ORDER BY id DESC"
	args := []any{accountID}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := s.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]*model.CheckinAttempt, 0)
	for rows.Next() {
		a := new(model.CheckinAttempt)
		var balanceBefore, balanceAfter, balanceDelta sql.NullFloat64
		if err := rows.Scan(&a.ID, &a.RunID, &a.SiteAccountID, &a.ProviderID, &a.LocalDay, &a.TriggerScope, &a.Status, &a.RewardText, &balanceBefore, &balanceAfter, &balanceDelta, &a.BalanceCurrency, &a.Message, &a.ErrorCode, &a.RetryAfterAt, &a.StartedAt, &a.FinishedAt, &a.AttemptNo); err != nil {
			return nil, err
		}
		if balanceBefore.Valid {
			a.BalanceBefore = &balanceBefore.Float64
		}
		if balanceAfter.Valid {
			a.BalanceAfter = &balanceAfter.Float64
		}
		if balanceDelta.Valid {
			a.BalanceDelta = &balanceDelta.Float64
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *SQLStore) CreateCheckinAttempt(ctx context.Context, a *model.CheckinAttempt) (*model.CheckinAttempt, error) {
	if a == nil {
		return nil, errors.New("checkin attempt cannot be nil")
	}
	var id int64
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		var err error
		id, err = s.insertID(ctx, tx, "checkin_attempts", "run_id,site_account_id,provider_id,local_day,trigger_scope,status,reward_text,balance_before,balance_after,balance_delta,balance_currency,message,error_code,retry_after_at,started_at,finished_at,attempt_no", []any{a.RunID, a.SiteAccountID, a.ProviderID, a.LocalDay, a.TriggerScope, a.Status, a.RewardText, a.BalanceBefore, a.BalanceAfter, a.BalanceDelta, a.BalanceCurrency, a.Message, a.ErrorCode, a.RetryAfterAt, a.StartedAt, a.FinishedAt, a.AttemptNo})
		return err
	})
	if err != nil {
		return nil, err
	}
	a.ID = id
	return a, nil
}
func (s *SQLStore) UpdateCheckinAttempt(ctx context.Context, a *model.CheckinAttempt) error {
	_, err := s.ExecContext(ctx, "UPDATE checkin_attempts SET status=?,reward_text=?,balance_before=?,balance_after=?,balance_delta=?,balance_currency=?,message=?,error_code=?,retry_after_at=?,started_at=?,finished_at=?,attempt_no=? WHERE id=?", a.Status, a.RewardText, a.BalanceBefore, a.BalanceAfter, a.BalanceDelta, a.BalanceCurrency, a.Message, a.ErrorCode, a.RetryAfterAt, a.StartedAt, a.FinishedAt, a.AttemptNo, a.ID)
	return err
}
func (s *SQLStore) HasDailyCheckinAttempt(ctx context.Context, accountID int64, localDay string) (bool, error) {
	var n int
	err := s.QueryRowContext(ctx, "SELECT COUNT(1) FROM checkin_attempts WHERE site_account_id=? AND local_day=? AND trigger_scope='daily'", accountID, localDay).Scan(&n)
	return n > 0, err
}

func (s *SQLStore) CreateSiteTask(ctx context.Context, t *model.SiteTask) error {
	_, err := s.ExecContext(ctx, "INSERT INTO site_tasks(id,kind,status,site_id,site_account_id,progress_json,result_ref,error,created_at,started_at,finished_at,cancelled_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", t.ID, t.Kind, t.Status, t.SiteID, t.SiteAccountID, t.ProgressJSON, t.ResultRef, t.Error, t.CreatedAt, t.StartedAt, t.FinishedAt, t.CancelledAt)
	return err
}
func (s *SQLStore) UpdateSiteTask(ctx context.Context, t *model.SiteTask) (bool, error) {
	result, err := s.ExecContext(ctx, "UPDATE site_tasks SET status=?,progress_json=?,result_ref=?,error=?,started_at=?,finished_at=?,cancelled_at=? WHERE id=? AND status IN ('queued','running')", t.Status, t.ProgressJSON, t.ResultRef, t.Error, t.StartedAt, t.FinishedAt, t.CancelledAt, t.ID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}
func (s *SQLStore) GetSiteTask(ctx context.Context, id string) (*model.SiteTask, error) {
	t := new(model.SiteTask)
	err := s.QueryRowContext(ctx, "SELECT id,kind,status,site_id,site_account_id,progress_json,result_ref,error,created_at,started_at,finished_at,cancelled_at FROM site_tasks WHERE id=?", id).Scan(&t.ID, &t.Kind, &t.Status, &t.SiteID, &t.SiteAccountID, &t.ProgressJSON, &t.ResultRef, &t.Error, &t.CreatedAt, &t.StartedAt, &t.FinishedAt, &t.CancelledAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("not found")
	}
	return t, err
}
func (s *SQLStore) CancelSiteTask(ctx context.Context, id string, now int64) (bool, error) {
	result, err := s.ExecContext(ctx, "UPDATE site_tasks SET status='cancelled',cancelled_at=?,finished_at=? WHERE id=? AND status IN ('queued','running')", now, now, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}
func (s *SQLStore) AcquireSiteTaskLease(ctx context.Context, key, owner string, now, until int64) (bool, error) {
	return s.lease(ctx, key, owner, now, until, false)
}
func (s *SQLStore) RenewSiteTaskLease(ctx context.Context, key, owner string, until, now int64) (bool, error) {
	return s.lease(ctx, key, owner, now, until, true)
}
func (s *SQLStore) lease(ctx context.Context, key, owner string, now, until int64, renew bool) (bool, error) {
	if renew {
		r, err := s.ExecContext(ctx, "UPDATE site_task_leases SET lease_until=?,updated_at=? WHERE task_key=? AND owner_id=? AND lease_until>=?", until, now, key, owner, now)
		if err != nil {
			return false, err
		}
		n, _ := r.RowsAffected()
		return n == 1, nil
	}
	_, err := s.ExecContext(ctx, "INSERT INTO site_task_leases(task_key,owner_id,lease_until,updated_at) VALUES(?,?,?,?)", key, owner, until, now)
	if err == nil {
		return true, nil
	}
	r, err := s.ExecContext(ctx, "UPDATE site_task_leases SET owner_id=?,lease_until=?,updated_at=? WHERE task_key=? AND lease_until<?", owner, until, now, key, now)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n == 1, nil
}
func (s *SQLStore) ReleaseSiteTaskLease(ctx context.Context, key, owner string) error {
	_, err := s.ExecContext(ctx, "DELETE FROM site_task_leases WHERE task_key=? AND owner_id=?", key, owner)
	return err
}

func (s *SQLStore) GetWebhookConfig(ctx context.Context) (*model.WebhookConfig, error) {
	var config model.WebhookConfig
	var enabled, telegramEnabled, telegramUseSystemProxy, lowBalanceEnabled, checkinFailureEnabled int
	err := s.QueryRowContext(ctx, `SELECT id,enabled,url_ciphertext,url_key_version,telegram_enabled,telegram_bot_ciphertext,telegram_bot_key_version,telegram_chat_ciphertext,telegram_chat_key_version,telegram_use_system_proxy,low_balance_enabled,low_balance_threshold,checkin_failure_enabled,cooldown_minutes,last_delivery_status,last_delivery_at,last_error,created_at,updated_at FROM webhook_endpoints WHERE id=1`).Scan(
		&config.ID, &enabled, &config.URLCiphertext, &config.URLKeyVersion, &telegramEnabled, &config.TelegramBotCiphertext, &config.TelegramBotKeyVersion, &config.TelegramChatCiphertext, &config.TelegramChatKeyVersion, &telegramUseSystemProxy, &lowBalanceEnabled, &config.LowBalanceThreshold, &checkinFailureEnabled, &config.CooldownMinutes, &config.LastDeliveryStatus, &config.LastDeliveryAt, &config.LastError, &config.CreatedAt, &config.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("not found")
	}
	if err != nil {
		return nil, err
	}
	config.Enabled = enabled != 0
	config.TelegramEnabled = telegramEnabled != 0
	config.TelegramUseSystemProxy = telegramUseSystemProxy != 0
	config.TelegramConfigured = strings.TrimSpace(config.TelegramBotCiphertext) != "" && strings.TrimSpace(config.TelegramChatCiphertext) != ""
	config.LowBalanceEnabled = lowBalanceEnabled != 0
	config.CheckinFailureEnabled = checkinFailureEnabled != 0
	config.URLConfigured = strings.TrimSpace(config.URLCiphertext) != ""
	return &config, nil
}

func (s *SQLStore) UpsertWebhookConfig(ctx context.Context, config *model.WebhookConfig) error {
	if config == nil {
		return errors.New("webhook config is required")
	}
	now := siteNow()
	if config.ID == 0 {
		config.ID = 1
	}
	if config.CreatedAt == 0 {
		config.CreatedAt = now
	}
	config.UpdatedAt = now
	args := []any{1, config.Enabled, config.URLCiphertext, config.URLKeyVersion, config.TelegramEnabled, config.TelegramBotCiphertext, config.TelegramBotKeyVersion, config.TelegramChatCiphertext, config.TelegramChatKeyVersion, config.TelegramUseSystemProxy, config.LowBalanceEnabled, config.LowBalanceThreshold, config.CheckinFailureEnabled, config.CooldownMinutes, config.LastDeliveryStatus, config.LastDeliveryAt, config.LastError, config.CreatedAt, config.UpdatedAt}
	query := `INSERT INTO webhook_endpoints(id,enabled,url_ciphertext,url_key_version,telegram_enabled,telegram_bot_ciphertext,telegram_bot_key_version,telegram_chat_ciphertext,telegram_chat_key_version,telegram_use_system_proxy,low_balance_enabled,low_balance_threshold,checkin_failure_enabled,cooldown_minutes,last_delivery_status,last_delivery_at,last_error,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	if s.IsMySQL() {
		query += ` ON DUPLICATE KEY UPDATE enabled=VALUES(enabled),url_ciphertext=VALUES(url_ciphertext),url_key_version=VALUES(url_key_version),telegram_enabled=VALUES(telegram_enabled),telegram_bot_ciphertext=VALUES(telegram_bot_ciphertext),telegram_bot_key_version=VALUES(telegram_bot_key_version),telegram_chat_ciphertext=VALUES(telegram_chat_ciphertext),telegram_chat_key_version=VALUES(telegram_chat_key_version),telegram_use_system_proxy=VALUES(telegram_use_system_proxy),low_balance_enabled=VALUES(low_balance_enabled),low_balance_threshold=VALUES(low_balance_threshold),checkin_failure_enabled=VALUES(checkin_failure_enabled),cooldown_minutes=VALUES(cooldown_minutes),last_delivery_status=VALUES(last_delivery_status),last_delivery_at=VALUES(last_delivery_at),last_error=VALUES(last_error),updated_at=VALUES(updated_at)`
	} else {
		query += ` ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled,url_ciphertext=excluded.url_ciphertext,url_key_version=excluded.url_key_version,telegram_enabled=excluded.telegram_enabled,telegram_bot_ciphertext=excluded.telegram_bot_ciphertext,telegram_bot_key_version=excluded.telegram_bot_key_version,telegram_chat_ciphertext=excluded.telegram_chat_ciphertext,telegram_chat_key_version=excluded.telegram_chat_key_version,telegram_use_system_proxy=excluded.telegram_use_system_proxy,low_balance_enabled=excluded.low_balance_enabled,low_balance_threshold=excluded.low_balance_threshold,checkin_failure_enabled=excluded.checkin_failure_enabled,cooldown_minutes=excluded.cooldown_minutes,last_delivery_status=excluded.last_delivery_status,last_delivery_at=excluded.last_delivery_at,last_error=excluded.last_error,updated_at=excluded.updated_at`
	}
	_, err := s.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLStore) GetWebhookEventState(ctx context.Context, eventKey string) (*model.WebhookEventState, error) {
	var state model.WebhookEventState
	err := s.QueryRowContext(ctx, `SELECT event_key,event_type,site_account_id,status,attempts,last_attempt_at,delivered_at,last_error,updated_at FROM webhook_event_states WHERE event_key=?`, eventKey).Scan(
		&state.EventKey, &state.EventType, &state.SiteAccountID, &state.Status, &state.Attempts, &state.LastAttemptAt, &state.DeliveredAt, &state.LastError, &state.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("not found")
	}
	return &state, err
}

func (s *SQLStore) UpsertWebhookEventState(ctx context.Context, state *model.WebhookEventState) error {
	if state == nil || strings.TrimSpace(state.EventKey) == "" {
		return errors.New("webhook event state is required")
	}
	state.UpdatedAt = siteNow()
	args := []any{state.EventKey, state.EventType, state.SiteAccountID, state.Status, state.Attempts, state.LastAttemptAt, state.DeliveredAt, state.LastError, state.UpdatedAt}
	query := `INSERT INTO webhook_event_states(event_key,event_type,site_account_id,status,attempts,last_attempt_at,delivered_at,last_error,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`
	if s.IsMySQL() {
		query += ` ON DUPLICATE KEY UPDATE event_type=VALUES(event_type),site_account_id=VALUES(site_account_id),status=VALUES(status),attempts=VALUES(attempts),last_attempt_at=VALUES(last_attempt_at),delivered_at=VALUES(delivered_at),last_error=VALUES(last_error),updated_at=VALUES(updated_at)`
	} else {
		query += ` ON CONFLICT(event_key) DO UPDATE SET event_type=excluded.event_type,site_account_id=excluded.site_account_id,status=excluded.status,attempts=excluded.attempts,last_attempt_at=excluded.last_attempt_at,delivered_at=excluded.delivered_at,last_error=excluded.last_error,updated_at=excluded.updated_at`
	}
	_, err := s.ExecContext(ctx, query, args...)
	return err
}

func (s *SQLStore) GetSiteChannelBinding(ctx context.Context, siteAccountID int64, projectionKey string) (*model.SiteChannelBinding, error) {
	var binding model.SiteChannelBinding
	err := s.QueryRowContext(ctx, "SELECT id,site_account_id,projection_key,COALESCE(channel_id,0),ownership,status,last_projected_hash,last_sync_status,last_sync_error,created_at,updated_at FROM site_channel_bindings WHERE site_account_id=? AND projection_key=?", siteAccountID, projectionKey).Scan(&binding.ID, &binding.SiteAccountID, &binding.ProjectionKey, &binding.ChannelID, &binding.Ownership, &binding.Status, &binding.LastProjectedHash, &binding.LastSyncStatus, &binding.LastSyncError, &binding.CreatedAt, &binding.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("not found")
	}
	return &binding, err
}

func (s *SQLStore) ListSiteChannelBindings(ctx context.Context) ([]*model.SiteChannelBinding, error) {
	rows, err := s.QueryContext(ctx, "SELECT id,site_account_id,projection_key,COALESCE(channel_id,0),ownership,status,last_projected_hash,last_sync_status,last_sync_error,created_at,updated_at FROM site_channel_bindings ORDER BY id ASC")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	bindings := make([]*model.SiteChannelBinding, 0)
	for rows.Next() {
		var binding model.SiteChannelBinding
		if err := rows.Scan(&binding.ID, &binding.SiteAccountID, &binding.ProjectionKey, &binding.ChannelID, &binding.Ownership, &binding.Status, &binding.LastProjectedHash, &binding.LastSyncStatus, &binding.LastSyncError, &binding.CreatedAt, &binding.UpdatedAt); err != nil {
			return nil, err
		}
		bindings = append(bindings, &binding)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bindings, nil
}

func (s *SQLStore) projectionSourceHashTx(ctx context.Context, tx *sql.Tx, channelID int64) (string, bool, error) {
	var urls model.ChannelURLs
	var authType string
	var enabled bool
	if err := s.queryRowTx(ctx, tx, "SELECT url,auth_type,enabled FROM channels WHERE id=?", channelID).Scan(&urls, &authType, &enabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	if model.NormalizeAuthType(authType) != model.AuthTypeAPIKey || len(urls) != 1 {
		return "", true, nil
	}
	rows, err := tx.QueryContext(ctx, s.q("SELECT model FROM channel_models WHERE channel_id=? AND disabled=0 ORDER BY model ASC"), normalizeSQLArgs([]any{channelID})...)
	if err != nil {
		return "", false, err
	}
	models := make([]string, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return "", false, err
		}
		models = append(models, name)
	}
	if err := rows.Close(); err != nil {
		return "", false, err
	}
	keyRows, err := tx.QueryContext(ctx, s.q("SELECT key_index,api_key,disabled FROM api_keys WHERE channel_id=? ORDER BY key_index ASC"), normalizeSQLArgs([]any{channelID})...)
	if err != nil {
		return "", false, err
	}
	var apiKey string
	validKeys := true
	count := 0
	for keyRows.Next() {
		var index int
		var key string
		var disabled bool
		if err := keyRows.Scan(&index, &key, &disabled); err != nil {
			_ = keyRows.Close()
			return "", false, err
		}
		count++
		if index != 0 || disabled {
			validKeys = false
		}
		apiKey = key
	}
	if err := keyRows.Close(); err != nil {
		return "", false, err
	}
	if count != 1 || !validKeys {
		return "", true, nil
	}
	return model.SiteProjectionSourceHash(urls[0].URL, urls[0].Protocols, models, apiKey, enabled), true, nil
}

func (s *SQLStore) UpsertSiteProjection(ctx context.Context, input model.SiteProjectionInput) (*model.SiteProjectionResult, error) {
	if input.SiteAccountID <= 0 || strings.TrimSpace(input.ProjectionKey) == "" || strings.TrimSpace(input.BaseURL) == "" || strings.TrimSpace(input.APIKey) == "" {
		return nil, errors.New("invalid projection input")
	}
	if len(input.Models) == 0 {
		return nil, errors.New("projection requires at least one model")
	}
	input.SourceHash = model.SiteProjectionSourceHash(input.BaseURL, input.Protocols, input.Models, input.APIKey, input.Enabled)
	now := siteNow()
	channelName := strings.TrimSpace(input.Name)
	if channelName == "" {
		channelName = fmt.Sprintf("site/account/%d/%s", input.SiteAccountID, input.ProjectionKey)
	}
	urls := model.ChannelURLs{{URL: strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"), Protocols: append([]string(nil), input.Protocols...)}}
	if err := urls.Normalize(); err != nil {
		return nil, fmt.Errorf("normalize projection url: %w", err)
	}
	entries := make([]model.ModelEntry, 0, len(input.Models))
	for _, name := range input.Models {
		entry := model.ModelEntry{Model: strings.TrimSpace(name)}
		if err := entry.Validate(); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	channel := &model.Config{Name: channelName, AuthType: model.AuthTypeAPIKey, URLs: urls, Priority: 0, Enabled: input.Enabled, ProtocolTransformMode: model.ProtocolTransformModeAuto, ModelEntries: entries}
	var binding model.SiteChannelBinding
	action := "created"
	err := s.WithTransaction(ctx, func(tx *sql.Tx) error {
		if err := s.queryRowTx(ctx, tx, "SELECT id FROM site_channel_bindings WHERE site_account_id=? AND projection_key=?", input.SiteAccountID, input.ProjectionKey).Scan(&binding.ID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if binding.ID > 0 {
			action = "updated"
			if err := s.queryRowTx(ctx, tx, "SELECT COALESCE(channel_id,0),ownership,status,last_projected_hash,last_sync_status,last_sync_error,created_at FROM site_channel_bindings WHERE id=?", binding.ID).Scan(&binding.ChannelID, &binding.Ownership, &binding.Status, &binding.LastProjectedHash, &binding.LastSyncStatus, &binding.LastSyncError, &binding.CreatedAt); err != nil {
				return err
			}
			if binding.Ownership == "manual" {
				action = "conflict"
				binding.LastSyncStatus = "conflict"
				binding.LastSyncError = "projection binding is manual"
				_, err := s.execTx(ctx, tx, "UPDATE site_channel_bindings SET last_sync_status='conflict',last_sync_error=?,updated_at=? WHERE id=?", binding.LastSyncError, now, binding.ID)
				return err
			}
			channel.ID = binding.ChannelID
		}
		if channel.ID > 0 {
			actualHash, exists, err := s.projectionSourceHashTx(ctx, tx, channel.ID)
			if err != nil {
				return err
			}
			if !exists {
				channel.ID = 0
				action = "recreated"
			} else if actualHash != binding.LastProjectedHash && !input.Force {
				action = "conflict"
				binding.LastSyncStatus = "conflict"
				binding.LastSyncError = "projected channel changed outside site control"
				_, err := s.execTx(ctx, tx, "UPDATE site_channel_bindings SET last_sync_status='conflict',last_sync_error=?,updated_at=? WHERE id=?", binding.LastSyncError, now, binding.ID)
				return err
			} else if actualHash == binding.LastProjectedHash && input.SourceHash == binding.LastProjectedHash && !input.Force {
				action = "unchanged"
				binding.LastSyncStatus = "success"
				binding.LastSyncError = ""
				_, err := s.execTx(ctx, tx, "UPDATE site_channel_bindings SET status='active',last_sync_status='success',last_sync_error='',updated_at=? WHERE id=?", now, binding.ID)
				return err
			}
		}
		if channel.ID > 0 {
			if input.Force {
				channel.Name = s.uniqueProjectionChannelNameTx(ctx, tx, channel.Name, input.SiteAccountID, input.ProjectionKey, channel.ID)
				if _, err := s.execTx(ctx, tx, `UPDATE channels SET name=?,url=?,auth_type=?,enabled=?,protocol_transform_mode=?,updated_at=? WHERE id=?`, channel.Name, channel.URLs, channel.AuthType, channel.Enabled, channel.ProtocolTransformMode, now, channel.ID); err != nil {
					return err
				}
			} else {
				if _, err := s.execTx(ctx, tx, `UPDATE channels SET url=?,auth_type=?,enabled=?,protocol_transform_mode=?,updated_at=? WHERE id=?`, channel.URLs, channel.AuthType, channel.Enabled, channel.ProtocolTransformMode, now, channel.ID); err != nil {
					return err
				}
			}
			if err := s.saveModelEntriesTx(ctx, tx, channel.ID, channel.ModelEntries); err != nil {
				return err
			}
			if _, err := s.execTx(ctx, tx, "DELETE FROM api_keys WHERE channel_id=?", channel.ID); err != nil {
				return err
			}
		} else {
			// Projection channels are generated from upstream keys. Different keys
			// frequently share the same display name, but channels.name is unique.
			// Keep the user-facing name and append a stable key identity only when
			// another channel already owns the requested name.
			channel.Name = s.uniqueProjectionChannelNameTx(ctx, tx, channel.Name, input.SiteAccountID, input.ProjectionKey)
			id, err := s.insertID(ctx, tx, "channels", "name,url,priority,rpm_limit,max_concurrency,auth_type,oauth_credential,websockets,protocol_transform_mode,enabled,scheduled_check_enabled,scheduled_check_model,daily_cost_limit,cost_multiplier,custom_request_rules,cooldown_detection_rules,proxy_url,retry_other_keys_on_failure,created_at,updated_at", []any{channel.Name, channel.URLs, 0, 0, 0, channel.AuthType, "", false, channel.ProtocolTransformMode, channel.Enabled, false, "", 0, 1, nil, nil, "", false, now, now})
			if err != nil {
				return err
			}
			channel.ID = id
			if err := s.saveModelEntriesTx(ctx, tx, channel.ID, channel.ModelEntries); err != nil {
				return err
			}
		}
		if action == "conflict" || action == "unchanged" {
			return nil
		}
		if err := s.execAPIKeyTx(ctx, tx, channel.ID, input.APIKey); err != nil {
			return err
		}
		if binding.ID == 0 {
			id, err := s.insertID(ctx, tx, "site_channel_bindings", "site_account_id,projection_key,channel_id,ownership,status,last_projected_hash,last_sync_status,last_sync_error,created_at,updated_at", []any{input.SiteAccountID, input.ProjectionKey, channel.ID, "projected", "active", input.SourceHash, "success", "", now, now})
			if err != nil {
				return err
			}
			binding.ID = id
			binding.CreatedAt = now
		} else {
			if _, err := s.execTx(ctx, tx, "UPDATE site_channel_bindings SET channel_id=?,status='active',last_projected_hash=?,last_sync_status='success',last_sync_error='',updated_at=? WHERE id=?", channel.ID, input.SourceHash, now, binding.ID); err != nil {
				return err
			}
		}
		binding.SiteAccountID, binding.ProjectionKey, binding.ChannelID = input.SiteAccountID, input.ProjectionKey, channel.ID
		binding.Ownership, binding.Status, binding.LastProjectedHash, binding.LastSyncStatus = "projected", "active", input.SourceHash, "success"
		binding.LastSyncError = ""
		binding.UpdatedAt = now
		return nil
	})
	if err != nil {
		return nil, err
	}
	if channel.ID == 0 && binding.ChannelID > 0 {
		channel.ID = binding.ChannelID
	}
	loaded, err := s.GetConfig(ctx, channel.ID)
	if err != nil {
		return nil, err
	}
	return &model.SiteProjectionResult{Binding: &binding, Channel: loaded, Action: action}, nil
}

func (s *SQLStore) uniqueProjectionChannelNameTx(ctx context.Context, tx *sql.Tx, base string, accountID int64, projectionKey string, currentID ...int64) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = fmt.Sprintf("site/account/%d/%s", accountID, projectionKey)
	}
	available := func(name string) bool {
		var id int64
		err := s.queryRowTx(ctx, tx, "SELECT id FROM channels WHERE name=?", name).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return true
		}
		if len(currentID) > 0 && currentID[0] > 0 && err == nil && id == currentID[0] {
			return true
		}
		return false
	}
	if available(base) {
		return base
	}
	identity := strings.TrimSpace(strings.TrimPrefix(projectionKey, "key:"))
	if identity == "" {
		identity = model.HashToken(projectionKey)[:10]
	}
	candidate := fmt.Sprintf("%s / %s", base, identity)
	if available(candidate) {
		return candidate
	}
	return fmt.Sprintf("%s / %d-%s", base, accountID, model.HashToken(projectionKey)[:10])
}

func (s *SQLStore) execAPIKeyTx(ctx context.Context, tx *sql.Tx, channelID int64, apiKey string) error {
	_, err := s.execTx(ctx, tx, `INSERT INTO api_keys(channel_id,key_index,api_key,note,key_strategy,cooldown_until,cooldown_duration_ms,disabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, channelID, 0, apiKey, "projected", model.KeyStrategySequential, 0, 0, false, siteNow(), siteNow())
	return err
}

func (s *SQLStore) DeactivateSiteProjectionsExcept(ctx context.Context, siteAccountID int64, activeProjectionKeys []string) error {
	active := make(map[string]struct{}, len(activeProjectionKeys))
	for _, key := range activeProjectionKeys {
		active[strings.TrimSpace(key)] = struct{}{}
	}
	return s.WithTransaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id,projection_key,COALESCE(channel_id,0),ownership FROM site_channel_bindings WHERE site_account_id=?", siteAccountID)
		if err != nil {
			return err
		}
		type item struct {
			id, channelID  int64
			key, ownership string
		}
		var items []item
		for rows.Next() {
			var v item
			if err := rows.Scan(&v.id, &v.key, &v.channelID, &v.ownership); err != nil {
				_ = rows.Close()
				return err
			}
			items = append(items, v)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		now := siteNow()
		for _, v := range items {
			if v.ownership != "projected" {
				continue
			}
			if _, ok := active[v.key]; ok {
				continue
			}
			if v.channelID > 0 {
				if _, err := s.execTx(ctx, tx, "UPDATE channels SET enabled=0,updated_at=? WHERE id=?", now, v.channelID); err != nil {
					return err
				}
			}
			if _, err := s.execTx(ctx, tx, "UPDATE site_channel_bindings SET status='inactive',last_sync_status='success',last_sync_error='upstream key removed or disabled',updated_at=? WHERE id=?", now, v.id); err != nil {
				return err
			}
		}
		return nil
	})
}

// PruneSiteProjectionsExcept removes projected channels whose upstream key no
// longer exists. Manual channels and manual bindings are deliberately left
// untouched. This is used by an explicit route synchronization, where the
// upstream account is the source of truth for projected channels.
func (s *SQLStore) PruneSiteProjectionsExcept(ctx context.Context, siteAccountID int64, activeProjectionKeys []string) error {
	active := make(map[string]struct{}, len(activeProjectionKeys))
	for _, key := range activeProjectionKeys {
		active[strings.TrimSpace(key)] = struct{}{}
	}
	rows, err := s.QueryContext(ctx, "SELECT id,projection_key,COALESCE(channel_id,0),ownership FROM site_channel_bindings WHERE site_account_id=?", siteAccountID)
	if err != nil {
		return err
	}
	type item struct {
		bindingID, channelID int64
		key, ownership       string
	}
	items := make([]item, 0)
	for rows.Next() {
		var value item
		if err := rows.Scan(&value.bindingID, &value.key, &value.channelID, &value.ownership); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, value)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, value := range items {
		if value.ownership != "projected" {
			continue
		}
		if _, ok := active[value.key]; ok {
			continue
		}
		if value.channelID > 0 {
			var references int
			if err := s.QueryRowContext(ctx, "SELECT COUNT(1) FROM site_channel_bindings WHERE channel_id=? AND id<>?", value.channelID, value.bindingID).Scan(&references); err != nil {
				return err
			}
			if references == 0 {
				if err := s.DeleteConfig(ctx, value.channelID); err != nil {
					return err
				}
			}
		}
		if _, err := s.ExecContext(ctx, "DELETE FROM site_channel_bindings WHERE id=? AND ownership='projected'", value.bindingID); err != nil {
			return err
		}
	}
	return nil
}
