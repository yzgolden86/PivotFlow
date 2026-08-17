package storage

import (
	"context"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// Site control-plane data is authoritative in the primary database in hybrid
// mode. Keeping it together avoids exposing partially synchronized credentials;
// SQLite-only deployments use SQLStore directly and retain local performance.
func (h *HybridStore) ListSites(ctx context.Context, f model.SiteListFilter) ([]*model.Site, error) {
	return h.mysql.ListSites(ctx, f)
}
func (h *HybridStore) GetSite(ctx context.Context, id int64) (*model.Site, error) {
	return h.mysql.GetSite(ctx, id)
}
func (h *HybridStore) CreateSite(ctx context.Context, v *model.Site) (*model.Site, error) {
	return h.mysql.CreateSite(ctx, v)
}
func (h *HybridStore) UpdateSite(ctx context.Context, id int64, v *model.Site) (*model.Site, error) {
	return h.mysql.UpdateSite(ctx, id, v)
}
func (h *HybridStore) DeleteSite(ctx context.Context, id int64) error {
	return h.mysql.DeleteSite(ctx, id)
}
func (h *HybridStore) ListSiteAccounts(ctx context.Context, id int64, d bool) ([]*model.SiteAccount, error) {
	return h.mysql.ListSiteAccounts(ctx, id, d)
}
func (h *HybridStore) GetSiteAccount(ctx context.Context, id int64) (*model.SiteAccount, error) {
	return h.mysql.GetSiteAccount(ctx, id)
}
func (h *HybridStore) CreateSiteAccount(ctx context.Context, v *model.SiteAccount) (*model.SiteAccount, error) {
	return h.mysql.CreateSiteAccount(ctx, v)
}
func (h *HybridStore) UpdateSiteAccount(ctx context.Context, id int64, v *model.SiteAccount) (*model.SiteAccount, error) {
	return h.mysql.UpdateSiteAccount(ctx, id, v)
}
func (h *HybridStore) UpdateSiteAccountCredential(ctx context.Context, id int64, credentialType, ciphertext, keyVersion string) error {
	return h.mysql.UpdateSiteAccountCredential(ctx, id, credentialType, ciphertext, keyVersion)
}
func (h *HybridStore) DeleteSiteAccount(ctx context.Context, id int64) error {
	return h.mysql.DeleteSiteAccount(ctx, id)
}
func (h *HybridStore) ReplaceSiteAccountModels(ctx context.Context, id int64, v []model.SiteAccountModel) error {
	return h.mysql.ReplaceSiteAccountModels(ctx, id, v)
}
func (h *HybridStore) ListSiteAccountModels(ctx context.Context, f model.SiteModelFilter) ([]model.SiteAccountModel, error) {
	return h.mysql.ListSiteAccountModels(ctx, f)
}
func (h *HybridStore) UpsertSiteAnnouncements(ctx context.Context, v []model.SiteAnnouncement) error {
	return h.mysql.UpsertSiteAnnouncements(ctx, v)
}
func (h *HybridStore) ListSiteAnnouncements(ctx context.Context, f model.SiteAnnouncementFilter) ([]*model.SiteAnnouncement, int, error) {
	return h.mysql.ListSiteAnnouncements(ctx, f)
}
func (h *HybridStore) MarkSiteAnnouncementRead(ctx context.Context, id int64) error {
	return h.mysql.MarkSiteAnnouncementRead(ctx, id)
}
func (h *HybridStore) MarkAllSiteAnnouncementsRead(ctx context.Context, id int64) error {
	return h.mysql.MarkAllSiteAnnouncementsRead(ctx, id)
}
func (h *HybridStore) CreateCheckinRun(ctx context.Context, v *model.CheckinRun) (*model.CheckinRun, error) {
	return h.mysql.CreateCheckinRun(ctx, v)
}
func (h *HybridStore) UpdateCheckinRun(ctx context.Context, v *model.CheckinRun) error {
	return h.mysql.UpdateCheckinRun(ctx, v)
}
func (h *HybridStore) GetCheckinRun(ctx context.Context, id int64) (*model.CheckinRun, error) {
	return h.mysql.GetCheckinRun(ctx, id)
}
func (h *HybridStore) ListCheckinAttempts(ctx context.Context, id int64, limit int) ([]*model.CheckinAttempt, error) {
	return h.mysql.ListCheckinAttempts(ctx, id, limit)
}
func (h *HybridStore) CreateCheckinAttempt(ctx context.Context, v *model.CheckinAttempt) (*model.CheckinAttempt, error) {
	return h.mysql.CreateCheckinAttempt(ctx, v)
}
func (h *HybridStore) UpdateCheckinAttempt(ctx context.Context, v *model.CheckinAttempt) error {
	return h.mysql.UpdateCheckinAttempt(ctx, v)
}
func (h *HybridStore) HasDailyCheckinAttempt(ctx context.Context, id int64, day string) (bool, error) {
	return h.mysql.HasDailyCheckinAttempt(ctx, id, day)
}
func (h *HybridStore) CreateSiteTask(ctx context.Context, v *model.SiteTask) error {
	return h.mysql.CreateSiteTask(ctx, v)
}
func (h *HybridStore) UpdateSiteTask(ctx context.Context, v *model.SiteTask) (bool, error) {
	return h.mysql.UpdateSiteTask(ctx, v)
}
func (h *HybridStore) GetSiteTask(ctx context.Context, id string) (*model.SiteTask, error) {
	return h.mysql.GetSiteTask(ctx, id)
}
func (h *HybridStore) CancelSiteTask(ctx context.Context, id string, now int64) (bool, error) {
	return h.mysql.CancelSiteTask(ctx, id, now)
}
func (h *HybridStore) AcquireSiteTaskLease(ctx context.Context, k, o string, n, u int64) (bool, error) {
	return h.mysql.AcquireSiteTaskLease(ctx, k, o, n, u)
}
func (h *HybridStore) RenewSiteTaskLease(ctx context.Context, k, o string, u, n int64) (bool, error) {
	return h.mysql.RenewSiteTaskLease(ctx, k, o, u, n)
}
func (h *HybridStore) ReleaseSiteTaskLease(ctx context.Context, k, o string) error {
	return h.mysql.ReleaseSiteTaskLease(ctx, k, o)
}
func (h *HybridStore) GetWebhookConfig(ctx context.Context) (*model.WebhookConfig, error) {
	return h.mysql.GetWebhookConfig(ctx)
}
func (h *HybridStore) UpsertWebhookConfig(ctx context.Context, v *model.WebhookConfig) error {
	return h.mysql.UpsertWebhookConfig(ctx, v)
}
func (h *HybridStore) GetWebhookEventState(ctx context.Context, key string) (*model.WebhookEventState, error) {
	return h.mysql.GetWebhookEventState(ctx, key)
}
func (h *HybridStore) UpsertWebhookEventState(ctx context.Context, v *model.WebhookEventState) error {
	return h.mysql.UpsertWebhookEventState(ctx, v)
}
func (h *HybridStore) GetBackupConfig(ctx context.Context) (*model.BackupConfig, error) {
	return h.mysql.GetBackupConfig(ctx)
}
func (h *HybridStore) UpsertBackupConfig(ctx context.Context, v *model.BackupConfig) error {
	return h.mysql.UpsertBackupConfig(ctx, v)
}
func (h *HybridStore) GetSiteChannelBinding(ctx context.Context, id int64, key string) (*model.SiteChannelBinding, error) {
	return h.mysql.GetSiteChannelBinding(ctx, id, key)
}
func (h *HybridStore) ListSiteChannelBindings(ctx context.Context) ([]*model.SiteChannelBinding, error) {
	return h.mysql.ListSiteChannelBindings(ctx)
}
func (h *HybridStore) UpsertSiteProjection(ctx context.Context, v model.SiteProjectionInput) (*model.SiteProjectionResult, error) {
	return h.mysql.UpsertSiteProjection(ctx, v)
}

func (h *HybridStore) DeactivateSiteProjectionsExcept(ctx context.Context, siteAccountID int64, activeProjectionKeys []string) error {
	return h.mysql.DeactivateSiteProjectionsExcept(ctx, siteAccountID, activeProjectionKeys)
}

func (h *HybridStore) PruneSiteProjectionsExcept(ctx context.Context, siteAccountID int64, activeProjectionKeys []string) error {
	return h.mysql.PruneSiteProjectionsExcept(ctx, siteAccountID, activeProjectionKeys)
}
