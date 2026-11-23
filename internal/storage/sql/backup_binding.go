package sql

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// RestoreDisabledSiteChannelBindings preserves the schema's historical shared
// channel links. Portable new links still use the stricter single-owner restore.
func (s *SQLStore) RestoreDisabledSiteChannelBindings(ctx context.Context, channelID int64) error {
	if channelID <= 0 {
		return errors.New("invalid_binding_reference")
	}
	_, err := s.ExecContext(ctx, `UPDATE site_channel_bindings
		SET ownership='manual',status='disabled',updated_at=? WHERE channel_id=?`, siteNow(), channelID)
	return err
}

// RestoreSiteChannelBinding restores a portable link without creating another
// channel or transferring automatic synchronization from the source instance.
func (s *SQLStore) RestoreSiteChannelBinding(ctx context.Context, input *model.SiteChannelBinding) error {
	if input == nil || input.SiteAccountID <= 0 || input.ChannelID <= 0 ||
		strings.TrimSpace(input.ProjectionKey) == "" || utf8.RuneCountInString(input.ProjectionKey) > 191 ||
		utf8.RuneCountInString(input.PricingGroup) > 64 || strings.ContainsAny(input.ProjectionKey+input.PricingGroup, "\x00\r\n") {
		return errors.New("invalid_binding")
	}
	return s.WithTransaction(ctx, func(tx *sql.Tx) error {
		var accountEnabled, siteEnabled, channelEnabled bool
		var accountDeleted, siteDeleted int64
		var accountStatus string
		err := s.queryRowTx(ctx, tx, `SELECT a.enabled,a.status,a.deleted_at,s.enabled,s.deleted_at
			FROM site_accounts a JOIN sites s ON s.id=a.site_id WHERE a.id=?`, input.SiteAccountID).
			Scan(&accountEnabled, &accountStatus, &accountDeleted, &siteEnabled, &siteDeleted)
		if errors.Is(err, sql.ErrNoRows) || accountDeleted != 0 || siteDeleted != 0 {
			return errors.New("invalid_binding_reference")
		}
		if err != nil {
			return err
		}
		if err := s.queryRowTx(ctx, tx, "SELECT enabled FROM channels WHERE id=?", input.ChannelID).Scan(&channelEnabled); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("invalid_binding_reference")
			}
			return err
		}
		var competing int
		if err := s.queryRowTx(ctx, tx, `SELECT COUNT(1) FROM site_channel_bindings
			WHERE channel_id=? AND NOT (site_account_id=? AND projection_key=?)`, input.ChannelID, input.SiteAccountID, input.ProjectionKey).Scan(&competing); err != nil {
			return err
		}
		if competing != 0 {
			return errors.New("binding_channel_conflict")
		}
		var id int64
		err = s.queryRowTx(ctx, tx, "SELECT id FROM site_channel_bindings WHERE site_account_id=? AND projection_key=?", input.SiteAccountID, input.ProjectionKey).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		status := "active"
		if !accountEnabled || !siteEnabled || !channelEnabled || accountStatus == model.SiteAccountStatusExpired || accountStatus == model.SiteAccountStatusDisabled {
			status = "disabled"
		}
		now := siteNow()
		const manualReason = "manually managed channel; automatic synchronization is skipped"
		if id == 0 {
			_, err = s.insertID(ctx, tx, "site_channel_bindings", "site_account_id,projection_key,channel_id,pricing_group,ownership,status,last_projected_hash,last_sync_status,last_sync_error,created_at,updated_at",
				[]any{input.SiteAccountID, input.ProjectionKey, input.ChannelID, input.PricingGroup, "manual", status, "", "manual", manualReason, now, now})
			return err
		}
		_, err = s.execTx(ctx, tx, `UPDATE site_channel_bindings SET channel_id=?,pricing_group=?,ownership='manual',status=?,
			last_projected_hash='',last_sync_status='manual',last_sync_error=?,updated_at=? WHERE id=?`,
			input.ChannelID, input.PricingGroup, status, manualReason, now, id)
		return err
	})
}
