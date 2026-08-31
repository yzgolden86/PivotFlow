package sql

import (
	"context"
	"fmt"
	"time"
)

// ClearSiteAccountSuspendMark drops the cascade mark for one account.
// Called only from the account PATCH path: an explicit toggle by the user must
// survive the next site enable/disable cycle.
func (s *SQLStore) ClearSiteAccountSuspendMark(ctx context.Context, accountID int64) error {
	if _, err := s.ExecContext(ctx, `
		UPDATE site_accounts SET suspended_by_site = 0 WHERE id = ?
	`, accountID); err != nil {
		return fmt.Errorf("clear site account suspend mark: %w", err)
	}
	return nil
}

// CascadeSiteSuspend stops or restores everything a site owns.
//
// Disabling: every still-enabled account of the site, and every projected
// channel bound to those accounts, is marked suspended_by_site and turned off.
// Rows already disabled are left alone and stay unmarked, so a manual stop is
// distinguishable from a cascade stop.
//
// Enabling: only rows carrying the mark are turned back on, and the mark is
// cleared. Anything the user disabled by hand before the cascade stays off.
//
// Both directions run in one transaction: a half-applied cascade would leave
// channels routing traffic to a site the user believes is off.
func (s *SQLStore) CascadeSiteSuspend(ctx context.Context, siteID int64, enable bool) (int, int, error) {
	tx, err := s.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("begin cascade transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	nowUnix := timeToUnix(time.Now())

	// Channels are reached through site_channel_bindings; the channels table has
	// no site_id. Only projected channels are touched — a manually created
	// channel that happens to point at the same upstream is not owned by the
	// site and must not be flipped underneath the user.
	var channelSQL, accountSQL string
	if enable {
		channelSQL = `
			UPDATE channels
			SET enabled = 1, suspended_by_site = 0, updated_at = ?
			WHERE suspended_by_site = 1
			  AND id IN (
			    SELECT b.channel_id FROM site_channel_bindings b
			    JOIN site_accounts a ON a.id = b.site_account_id
			    WHERE a.site_id = ? AND b.channel_id IS NOT NULL
			  )`
		accountSQL = `
			UPDATE site_accounts
			SET enabled = 1, suspended_by_site = 0, updated_at = ?
			WHERE site_id = ? AND suspended_by_site = 1`
	} else {
		channelSQL = `
			UPDATE channels
			SET enabled = 0, suspended_by_site = 1, updated_at = ?
			WHERE enabled = 1
			  AND id IN (
			    SELECT b.channel_id FROM site_channel_bindings b
			    JOIN site_accounts a ON a.id = b.site_account_id
			    WHERE a.site_id = ? AND b.channel_id IS NOT NULL
			  )`
		accountSQL = `
			UPDATE site_accounts
			SET enabled = 0, suspended_by_site = 1, updated_at = ?
			WHERE site_id = ? AND enabled = 1`
	}

	// Channels first: while disabling, this stops traffic before the accounts
	// that own them disappear from the enabled set.
	channelResult, err := tx.ExecContext(ctx, s.q(channelSQL), nowUnix, siteID)
	if err != nil {
		return 0, 0, fmt.Errorf("cascade channels: %w", err)
	}
	channelRows, _ := channelResult.RowsAffected()

	accountResult, err := tx.ExecContext(ctx, s.q(accountSQL), nowUnix, siteID)
	if err != nil {
		return 0, 0, fmt.Errorf("cascade accounts: %w", err)
	}
	accountRows, _ := accountResult.RowsAffected()

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("commit cascade: %w", err)
	}
	return int(accountRows), int(channelRows), nil
}
