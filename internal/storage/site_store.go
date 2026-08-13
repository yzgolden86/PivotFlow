package storage

import (
	"context"

	"ccLoad/internal/model"
)

// SiteStore is the durable control-plane contract. It is embedded by Store so
// handlers and services cannot bypass the storage transaction boundary.
type SiteStore interface {
	ListSites(ctx context.Context, filter model.SiteListFilter) ([]*model.Site, error)
	GetSite(ctx context.Context, id int64) (*model.Site, error)
	CreateSite(ctx context.Context, site *model.Site) (*model.Site, error)
	UpdateSite(ctx context.Context, id int64, site *model.Site) (*model.Site, error)
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
	ListSiteAccountModels(ctx context.Context, filter model.SiteModelFilter) ([]model.SiteAccountModel, error)

	UpsertSiteAnnouncements(ctx context.Context, announcements []model.SiteAnnouncement) error
	ListSiteAnnouncements(ctx context.Context, filter model.SiteAnnouncementFilter) ([]*model.SiteAnnouncement, int, error)
	MarkSiteAnnouncementRead(ctx context.Context, id int64) error
	MarkAllSiteAnnouncementsRead(ctx context.Context, siteID int64) error

	CreateCheckinRun(ctx context.Context, run *model.CheckinRun) (*model.CheckinRun, error)
	UpdateCheckinRun(ctx context.Context, run *model.CheckinRun) error
	GetCheckinRun(ctx context.Context, id int64) (*model.CheckinRun, error)
	ListCheckinAttempts(ctx context.Context, accountID int64, limit int) ([]*model.CheckinAttempt, error)
	CreateCheckinAttempt(ctx context.Context, attempt *model.CheckinAttempt) (*model.CheckinAttempt, error)
	UpdateCheckinAttempt(ctx context.Context, attempt *model.CheckinAttempt) error
	HasDailyCheckinAttempt(ctx context.Context, accountID int64, localDay string) (bool, error)

	CreateSiteTask(ctx context.Context, task *model.SiteTask) error
	UpdateSiteTask(ctx context.Context, task *model.SiteTask) (bool, error)
	GetSiteTask(ctx context.Context, id string) (*model.SiteTask, error)
	CancelSiteTask(ctx context.Context, id string, now int64) (bool, error)

	AcquireSiteTaskLease(ctx context.Context, taskKey, ownerID string, now, leaseUntil int64) (bool, error)
	RenewSiteTaskLease(ctx context.Context, taskKey, ownerID string, leaseUntil, now int64) (bool, error)
	ReleaseSiteTaskLease(ctx context.Context, taskKey, ownerID string) error

	GetWebhookConfig(ctx context.Context) (*model.WebhookConfig, error)
	UpsertWebhookConfig(ctx context.Context, config *model.WebhookConfig) error
	GetWebhookEventState(ctx context.Context, eventKey string) (*model.WebhookEventState, error)
	UpsertWebhookEventState(ctx context.Context, state *model.WebhookEventState) error

	GetSiteChannelBinding(ctx context.Context, siteAccountID int64, projectionKey string) (*model.SiteChannelBinding, error)
	ListSiteChannelBindings(ctx context.Context) ([]*model.SiteChannelBinding, error)
	UpsertSiteProjection(ctx context.Context, input model.SiteProjectionInput) (*model.SiteProjectionResult, error)
	DeactivateSiteProjectionsExcept(ctx context.Context, siteAccountID int64, activeProjectionKeys []string) error
}
