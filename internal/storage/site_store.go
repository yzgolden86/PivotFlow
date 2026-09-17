package storage

import (
	"context"

	"github.com/yzgolden86/PivotFlow/internal/model"
)

// SiteStore is the durable control-plane contract. It is embedded by Store so
// handlers and services cannot bypass the storage transaction boundary.
type SiteStore interface {
	ListSites(ctx context.Context, filter model.SiteListFilter) ([]*model.Site, error)
	GetSite(ctx context.Context, id int64) (*model.Site, error)
	CreateSite(ctx context.Context, site *model.Site) (*model.Site, error)
	UpdateSite(ctx context.Context, id int64, site *model.Site) (*model.Site, error)
	// UpdateSiteCheckinMethod records the check-in capability discovered from the
	// site's public status endpoint, without rewriting the rest of the row.
	UpdateSiteCheckinMethod(ctx context.Context, siteID int64, method string, checkedAt int64) error
	DeleteSite(ctx context.Context, id int64) error

	// ListSiteAccounts lists one site's accounts. A zero siteID lists accounts
	// across all sites for control-plane aggregate views.
	ListSiteAccounts(ctx context.Context, siteID int64, includeDeleted bool) ([]*model.SiteAccount, error)
	GetSiteAccount(ctx context.Context, id int64) (*model.SiteAccount, error)
	CreateSiteAccount(ctx context.Context, account *model.SiteAccount) (*model.SiteAccount, error)
	UpdateSiteAccount(ctx context.Context, id int64, account *model.SiteAccount) (*model.SiteAccount, error)
	UpdateSiteAccountCredential(ctx context.Context, id int64, credentialType, ciphertext, keyVersion string) error
	DeleteSiteAccount(ctx context.Context, id int64) error

	ReplaceSiteAccountModels(ctx context.Context, accountID int64, models []model.SiteAccountModel) error
	MergeSiteAccountModels(ctx context.Context, accountID int64, models []model.SiteAccountModel) error
	ListSiteAccountModels(ctx context.Context, filter model.SiteModelFilter) ([]model.SiteAccountModel, error)
	UpsertSiteAccountBalanceSnapshot(ctx context.Context, snapshot *model.SiteAccountBalanceSnapshot) error
	ListSiteAccountBalanceSnapshots(ctx context.Context, sinceDay, untilDay string) ([]*model.SiteAccountBalanceSnapshot, error)

	UpsertSiteAnnouncements(ctx context.Context, announcements []model.SiteAnnouncement) error
	ListSiteAnnouncements(ctx context.Context, filter model.SiteAnnouncementFilter) ([]*model.SiteAnnouncement, int, error)
	MarkSiteAnnouncementRead(ctx context.Context, id int64) error
	MarkAllSiteAnnouncementsRead(ctx context.Context, siteID int64) error

	CreateCheckinRun(ctx context.Context, run *model.CheckinRun) (*model.CheckinRun, error)
	UpdateCheckinRun(ctx context.Context, run *model.CheckinRun) error
	GetCheckinRun(ctx context.Context, id int64) (*model.CheckinRun, error)
	ListCheckinAttempts(ctx context.Context, accountID int64, limit int) ([]*model.CheckinAttempt, error)
	ListCheckinAttemptsBatch(ctx context.Context, accountIDs []int64, perAccountLimit int) ([]*model.CheckinAttempt, error)
	CreateCheckinAttempt(ctx context.Context, attempt *model.CheckinAttempt) (*model.CheckinAttempt, error)
	UpdateCheckinAttempt(ctx context.Context, attempt *model.CheckinAttempt) error
	// GetDailyCheckinAttempt returns the scheduled (daily) check-in row for one
	// account and local day, or nil when the day has no scheduled attempt yet.
	// The table holds at most one such row per account and day, so callers that
	// need to retry must update the row they get back rather than insert.
	GetDailyCheckinAttempt(ctx context.Context, accountID int64, localDay string) (*model.CheckinAttempt, error)

	CreateSiteTask(ctx context.Context, task *model.SiteTask) error
	UpdateSiteTask(ctx context.Context, task *model.SiteTask) (bool, error)
	GetSiteTask(ctx context.Context, id string) (*model.SiteTask, error)
	CancelSiteTask(ctx context.Context, id string, now int64) (bool, error)

	// DeleteFinishedSiteTasks removes up to limit terminal tasks that finished
	// before the cutoff, and reports how many rows went away. Callers loop until
	// a pass returns fewer than the limit.
	DeleteFinishedSiteTasks(ctx context.Context, finishedBefore int64, limit int) (int64, error)
	// DeleteFinishedCheckinRuns removes up to limit finished check-in runs older
	// than the cutoff, together with the per-account attempts that belong to
	// them. Callers loop until a pass returns fewer than the limit.
	DeleteFinishedCheckinRuns(ctx context.Context, finishedBefore int64, limit int) (int64, error)
	// DeleteExpiredSiteTaskLeases removes up to limit leases whose window has
	// already closed. An expired lease excludes nobody, so the row is dead
	// weight once nothing renews it.
	DeleteExpiredSiteTaskLeases(ctx context.Context, expiredBefore int64, limit int) (int64, error)

	AcquireSiteTaskLease(ctx context.Context, taskKey, ownerID string, now, leaseUntil int64) (bool, error)
	RenewSiteTaskLease(ctx context.Context, taskKey, ownerID string, leaseUntil, now int64) (bool, error)
	ReleaseSiteTaskLease(ctx context.Context, taskKey, ownerID string) error

	GetWebhookConfig(ctx context.Context) (*model.WebhookConfig, error)
	UpsertWebhookConfig(ctx context.Context, config *model.WebhookConfig) error
	GetWebhookEventState(ctx context.Context, eventKey string) (*model.WebhookEventState, error)
	UpsertWebhookEventState(ctx context.Context, state *model.WebhookEventState) error

	GetSiteChannelBinding(ctx context.Context, siteAccountID int64, projectionKey string) (*model.SiteChannelBinding, error)
	ListSiteChannelBindings(ctx context.Context) ([]*model.SiteChannelBinding, error)
	MarkSiteProjectionManual(ctx context.Context, channelID int64) error
	SetSiteProjectionOwnership(ctx context.Context, channelID int64, ownership string) error
	UpsertSiteProjection(ctx context.Context, input model.SiteProjectionInput) (*model.SiteProjectionResult, error)
	DeactivateSiteProjectionsExcept(ctx context.Context, siteAccountID int64, activeProjectionKeys []string) error
	PruneSiteProjectionsExcept(ctx context.Context, siteAccountID int64, activeProjectionKeys []string) error
}
