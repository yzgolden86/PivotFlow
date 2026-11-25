package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/credential"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	sitewebhook "github.com/yzgolden86/PivotFlow/internal/site/webhook"
	"github.com/yzgolden86/PivotFlow/internal/storage"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/singleflight"
)

type siteControlService struct {
	store                  storage.Store
	configService          *ConfigService
	cipher                 *credential.Cipher
	registry               *provider.Registry
	baseCtx                context.Context
	wg                     *sync.WaitGroup
	taskMu                 sync.Mutex
	submitMu               sync.Mutex
	activeSiteTasks        map[string]*siteTaskRun
	workerSlots            chan struct{}
	pricingFlights         singleflight.Group
	webhookMu              sync.Mutex
	tasks                  map[string]context.CancelFunc
	taskStopCauses         map[string]context.CancelCauseFunc
	cancelledRefreshUntil  sync.Map // account ID -> next scheduled refresh opportunity
	stopped                bool
	webhookSender          sitewebhook.Sender
	onProjectionChanged    func()
	onModelAliasesChanged  func(context.Context) error
	onPricingSourceChanged func(int64)
	// siteGates holds one cap-1 semaphore per site ID so every upstream site
	// operation (scheduled refresh/checkin/announcement, manual refresh) for
	// the same site queues up instead of hitting CF-protected upstreams
	// concurrently (map[int64]chan struct{}).
	siteGates     sync.Map
	requestBudget *provider.RequestBudget

	// History retention bookkeeping. historyPruneAt is the unix-ms timestamp of
	// the last sweep; zero means "never run", which is why the first scheduler
	// tick after startup sweeps. Guarded by historyPruneMu rather than taskMu so
	// housekeeping never contends with the task registry.
	historyPruneMu sync.Mutex
	historyPruneAt int64
}

const credentialRefreshLead = 2 * time.Minute

func newSiteControlService(store storage.Store, baseCtx context.Context, wg *sync.WaitGroup) *siteControlService {
	cipher, err := credential.NewFromEnv()
	if errors.Is(err, credential.ErrCredentialLocked) {
		cipher = nil
	} else if err != nil {
		// Keep the control plane available for read-only site inspection, but
		// refuse all credential operations until the key is corrected.
		cipher = nil
	}
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	budget := provider.NewRequestBudget()
	clients := provider.ClientFactory{Budget: budget}
	return &siteControlService{
		store:  store,
		cipher: cipher,
		registry: provider.NewRegistry(
			provider.NewSub2API(clients),
			provider.NewVeloera(clients),
			provider.NewAnyRouter(clients),
			provider.NewNewAPI(clients),
			provider.NewOpenAICompatible(clients),
		),
		baseCtx:       baseCtx,
		wg:            wg,
		tasks:         make(map[string]context.CancelFunc),
		webhookSender: sitewebhook.Sender{Clients: provider.ClientFactory{}, Timeout: 5 * time.Second},
		requestBudget: budget,
	}
}

func (s *siteControlService) locked() bool { return s == nil || s.cipher == nil }

// siteCascadeResult tells the console how much the site toggle carried along,
// so it can say so instead of silently changing rows the user did not click.
type siteCascadeResult struct {
	Enabled  bool `json:"enabled"`
	Accounts int  `json:"accounts"`
	Channels int  `json:"channels"`
}

// siteWithCascade keeps the PATCH response shape a Site while adding the
// cascade summary, so existing clients that read Site fields keep working.
type siteWithCascade struct {
	*model.Site
	Cascade *siteCascadeResult `json:"cascade,omitempty"`
}

func cascadeVerb(enable bool) string {
	if enable {
		return "启用"
	}
	return "停用"
}

func (s *siteControlService) projectionChanged() {
	if s != nil && s.onProjectionChanged != nil {
		s.onProjectionChanged()
	}
}

func (s *siteControlService) pricingSourceChanged(siteID int64) {
	if s != nil && s.onPricingSourceChanged != nil {
		s.onPricingSourceChanged(siteID)
	}
}

// siteProxyURL centralizes the effective transport choice for all upstream
// management calls. An explicit site proxy always wins; otherwise sites may
// opt out of the process-level HTTP(S)_PROXY environment configuration.
func siteProxyURL(site *model.Site) string {
	if site == nil {
		return ""
	}
	if proxyURL := strings.TrimSpace(site.ProxyURL); proxyURL != "" {
		return proxyURL
	}
	if site.UseSystemProxy {
		return ""
	}
	return provider.DirectProxyURL
}

func newSiteTaskID() string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("st_%d", time.Now().UnixNano())
	}
	return "st_" + hex.EncodeToString(raw[:])
}

// newSiteTask builds a queued task without storing it. Callers that must take a
// lease before the work can start use this, so work that never runs leaves no
// row behind. The scheduled announcement refresh is the reason it exists: it is
// gated by a 26-hour lease, so persisting before the lease check wrote a
// "cancelled" row on every scheduler tick — hundreds per site per day whose
// only content was "another task already did this".
func newSiteTask(kind string, siteID, accountID int64, total int) *model.SiteTask {
	progress, _ := json.Marshal(gin.H{"completed": 0, "total": total})
	return &model.SiteTask{ID: newSiteTaskID(), Kind: kind, Status: model.SiteTaskStatusQueued, SiteID: siteID, SiteAccountID: accountID, ProgressJSON: string(progress), CreatedAt: time.Now().UnixMilli()}
}

// RouteSyncOutcome contains only configured channel identity and safe codes.
type RouteSyncOutcome struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func setSiteTaskRoutes(task *model.SiteTask, routes []RouteSyncOutcome) {
	progress := make(map[string]json.RawMessage)
	_ = json.Unmarshal([]byte(task.ProgressJSON), &progress)
	if progress == nil {
		progress = make(map[string]json.RawMessage)
	}
	if routes == nil {
		routes = []RouteSyncOutcome{}
	}
	progress["routes"], _ = json.Marshal(routes)
	data, _ := json.Marshal(progress)
	task.ProgressJSON = string(data)
}

// Discovery retains a separate cause for each unresolved projection; a single
// aggregate error cannot safely be attributed to every routing key.
type routingModelDiscoveryError struct {
	cause        error
	byProjection map[string]error
}

func (e *routingModelDiscoveryError) Error() string { return e.cause.Error() }
func (e *routingModelDiscoveryError) Unwrap() error { return e.cause }

func routeProgressErrorCode(err error) string {
	code := provider.ErrorCode(err)
	switch code {
	case provider.CodeUnsupported, provider.CodeBrowserRequired, provider.CodeExpired,
		provider.CodeUserIDRequired, provider.CodeTimeout, provider.CodeRateLimited,
		provider.CodeInvalidResponse, provider.CodeRequestFailed, provider.CodeRoutingKeyUnavailable:
		return code
	default:
		return provider.CodeRequestFailed
	}
}

func appendRouteProgress(routes *[]RouteSyncOutcome, name, status string, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	outcome := RouteSyncOutcome{Name: name, Status: status}
	if err != nil {
		outcome.Error = routeProgressErrorCode(err)
	}
	*routes = append(*routes, outcome)
}

func (s *siteControlService) updateTask(ctx context.Context, task *model.SiteTask, status, resultRef, message string) {
	if ctx != nil && ctx.Err() != nil && status != model.SiteTaskStatusSuccess && status != model.SiteTaskStatusPartial {
		if errors.Is(context.Cause(ctx), errSiteTaskLeaseLost) {
			status, message = model.SiteTaskStatusFailed, "site_task_lease_lost"
		} else if errors.Is(context.Cause(ctx), errSiteTaskStopped) {
			status, message = model.SiteTaskStatusCancelled, "server_shutting_down"
		} else if errors.Is(ctx.Err(), context.Canceled) {
			status, message = model.SiteTaskStatusCancelled, "cancelled"
		} else {
			status, message = model.SiteTaskStatusFailed, provider.CodeTimeout
		}
	}
	task.Status, task.ResultRef, task.Error = status, resultRef, message
	now := time.Now().UnixMilli()
	if status == model.SiteTaskStatusRunning && task.StartedAt == 0 {
		task.StartedAt = now
	}
	if status == model.SiteTaskStatusSuccess || status == model.SiteTaskStatusPartial || status == model.SiteTaskStatusFailed || status == model.SiteTaskStatusCancelled || status == "unknown" {
		task.FinishedAt = now
		if task.SiteAccountID > 0 || task.SiteID > 0 {
			var progress map[string]json.RawMessage
			_ = json.Unmarshal([]byte(task.ProgressJSON), &progress)
			if _, hasRoutes := progress["routes"]; !hasRoutes {
				if _, hasItems := progress["items"]; !hasItems {
					task.ProgressJSON = `{"completed":1,"total":1}`
				}
			}
		}
	}
	if status == model.SiteTaskStatusCancelled {
		task.CancelledAt = now
	}
	persistCtx := ctx
	if persistCtx == nil || persistCtx.Err() != nil {
		var cancel context.CancelFunc
		persistCtx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
	}
	_, _ = s.store.UpdateSiteTask(persistCtx, task)
}

func (s *siteControlService) leaseContext(parent context.Context, taskKey, ownerID string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				ok, err := s.store.RenewSiteTaskLease(ctx, taskKey, ownerID, now.Add(siteTaskLeaseDuration).UnixMilli(), now.UnixMilli())
				if err != nil || !ok {
					cancel(errSiteTaskLeaseLost)
					return
				}
			}
		}
	}()
	return ctx, func() {
		cancel(nil)
		<-done
	}
}

// acquireSiteGate serializes all upstream operations against one site. A
// manual refresh must not run concurrently with a scheduled refresh/checkin
// on the same site: bursty parallel requests to CF-protected upstreams get
// throttled and blow the client timeout, flagging healthy accounts as
// provider_timeout. The gate is in-process state; the store lease keeps
// covering cross-restart mutual exclusion per account and kind.
func (s *siteControlService) acquireSiteGate(ctx context.Context, siteID int64) (func(), bool) {
	gate, _ := s.siteGates.LoadOrStore(siteID, make(chan struct{}, 1))
	select {
	case gate.(chan struct{}) <- struct{}{}:
		return func() { <-gate.(chan struct{}) }, true
	case <-ctx.Done():
		return nil, false
	}
}

// retryOnTimeout wraps one site management upstream call with the same
// bounded backoff the check-in loop uses: a request throttled by the upstream
// often only fails once, so a timed-out call is retried twice (1s, 2s) before
// the failure is recorded on the account. The timeout applies per attempt.
func retryOnTimeout[T any](ctx context.Context, perTryTimeout time.Duration, call func(ctx context.Context) (T, error)) (T, error) {
	var zero T
	var last T
	var err error
	for try := 1; try <= 3; try++ {
		callCtx, cancel := context.WithTimeout(ctx, perTryTimeout)
		last, err = call(callCtx)
		cancel()
		if err == nil || provider.ErrorCode(err) != provider.CodeTimeout {
			return last, err
		}
		if try < 3 {
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(time.Duration(try) * time.Second):
			}
		}
	}
	return last, err
}

func (s *siteControlService) adapter(site *model.Site) (provider.SiteAdapter, error) {
	id := strings.ToLower(strings.TrimSpace(site.Platform))
	if id == "new-api" || id == "newapi" || id == "new-api-family" || id == "one-api" || id == "oneapi" || id == "one-hub" || id == "onehub" || id == "done-hub" || id == "donehub" || id == "voapi" || id == "axon-hub" || id == "axonhub" {
		id = model.SitePlatformNewAPIFamily
	}
	if id == model.SitePlatformNewAPIFamily && site != nil {
		if parsed, err := url.Parse(strings.TrimSpace(site.BaseURL)); err == nil {
			host := strings.ToLower(parsed.Hostname())
			if strings.Contains(host, "anyrouter") || strings.Contains(host, "agentrouter") || strings.Contains(host, "air-outer") {
				id = model.SitePlatformAnyRouter
			}
		}
	}
	if id == "any-router" {
		id = model.SitePlatformAnyRouter
	}
	if id == "sub2-api" {
		id = model.SitePlatformSub2API
	}
	if id == "openai" || id == "openai-compatible-api" || id == "openai_compatible" {
		id = model.SitePlatformOpenAICompatible
	}
	return s.registry.Get(id)
}

func (s *siteControlService) credentials(account *model.SiteAccount) (provider.Credentials, error) {
	if s.locked() {
		return provider.Credentials{}, credential.ErrCredentialLocked
	}
	var creds provider.Credentials
	if err := s.cipher.Open(account.CredentialCiphertext, &creds); err != nil {
		return provider.Credentials{}, err
	}
	return creds, nil
}

func (s *siteControlService) decorateAccountCredentialMetadata(account *model.SiteAccount) {
	if account == nil || s.locked() {
		return
	}
	creds, err := s.credentials(account)
	if err != nil {
		return
	}
	account.CredentialRefreshConfigured = strings.TrimSpace(creds.RefreshToken) != ""
	account.CredentialExpiresAt = creds.EffectiveExpiresAt()
}

func (s *siteControlService) preserveCredentialRefresh(account *model.SiteAccount, next *provider.Credentials) {
	if account == nil || next == nil || s.locked() {
		return
	}
	existing, err := s.credentials(account)
	if err != nil {
		return
	}
	if strings.TrimSpace(next.RefreshToken) == "" {
		next.RefreshToken = existing.RefreshToken
	}
	// An empty expires_at from the editor intentionally clears the previous
	// manual timestamp. JWT claims or a refresh response may repopulate it.
}

func (s *siteControlService) persistCredentials(ctx context.Context, account *model.SiteAccount, creds provider.Credentials) error {
	if creds.ExpiresAt == 0 {
		creds.ExpiresAt = creds.EffectiveExpiresAt()
	}
	sealed, err := s.cipher.Seal(creds)
	if err != nil {
		return err
	}
	if err := s.store.UpdateSiteAccountCredential(ctx, account.ID, account.CredentialType, sealed, s.cipher.Version()); err != nil {
		return err
	}
	account.CredentialCiphertext = sealed
	account.CredentialKeyVersion = s.cipher.Version()
	account.CredentialRefreshConfigured = strings.TrimSpace(creds.RefreshToken) != ""
	account.CredentialExpiresAt = creds.EffectiveExpiresAt()
	return nil
}

func credentialsChanged(before, after provider.Credentials) bool {
	return before.AccessToken != after.AccessToken || before.RefreshToken != after.RefreshToken || before.ExpiresAt != after.ExpiresAt || before.APIKey != after.APIKey || before.Cookie != after.Cookie || before.UserID != after.UserID
}

func (s *siteControlService) refreshExpiredCredentials(ctx context.Context, account *model.SiteAccount, site *model.Site, adapter provider.SiteAdapter, creds provider.Credentials) (provider.Credentials, error) {
	refresher, ok := adapter.(provider.CredentialRefresher)
	if !ok || strings.TrimSpace(creds.RefreshToken) == "" {
		return provider.Credentials{}, &provider.Error{Code: provider.CodeExpired, Message: "credential refresh is unavailable"}
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	refreshed, err := refresher.RefreshCredentials(refreshCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds})
	cancel()
	if err != nil {
		return provider.Credentials{}, err
	}
	if err := s.persistCredentials(ctx, account, refreshed); err != nil {
		return provider.Credentials{}, err
	}
	return refreshed, nil
}

func (s *siteControlService) operationCredentials(ctx context.Context, account *model.SiteAccount, site *model.Site, adapter provider.SiteAdapter) (provider.Credentials, error) {
	creds, err := s.credentials(account)
	if err != nil {
		return provider.Credentials{}, err
	}
	if _, ok := adapter.(provider.CredentialRefresher); ok && creds.RefreshDue(time.Now(), credentialRefreshLead) {
		refreshed, refreshErr := s.refreshExpiredCredentials(ctx, account, site, adapter, creds)
		if refreshErr != nil {
			return provider.Credentials{}, refreshErr
		}
		creds = refreshed
	}
	if account.CredentialType == model.CredentialTypeAPIKey || creds.UserID > 0 {
		return creds, nil
	}
	resolver, ok := adapter.(provider.ManagementCredentialResolver)
	if !ok {
		return creds, nil
	}
	resolveCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	resolved, err := resolver.ResolveManagementCredentials(resolveCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds})
	cancel()
	if err != nil {
		return provider.Credentials{}, err
	}
	if !credentialsChanged(creds, resolved) {
		return resolved, nil
	}
	if err := s.persistCredentials(ctx, account, resolved); err != nil {
		return provider.Credentials{}, err
	}
	return resolved, nil
}

func normalizeSiteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/")
	// Users commonly paste a panel page such as /console/personal instead of
	// the upstream origin. Management APIs and projected routes belong to the
	// origin, so discard known web-console paths while preserving deployments
	// hosted below an ordinary reverse-proxy prefix.
	if index := strings.Index(strings.ToLower(u.Path), "/console"); index >= 0 {
		u.Path = strings.TrimRight(u.Path[:index], "/")
	}
	u.RawQuery, u.Fragment = "", ""
	normalized := strings.TrimRight(u.String(), "/")
	if err := provider.ValidateBaseURL(normalized, false); err != nil {
		return "", err
	}
	return normalized, nil
}

func (s *siteControlService) createSite(ctx context.Context, req siteCreateRequest) (*model.Site, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 191 {
		return nil, errors.New("invalid site name")
	}
	baseURL, err := normalizeSiteURL(req.BaseURL)
	if err != nil {
		return nil, err
	}
	timezone := strings.TrimSpace(req.Timezone)
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return nil, errors.New("invalid timezone")
	}
	platform := strings.TrimSpace(req.Platform)
	if platform == "" {
		platform = model.SitePlatformUnknown
	}
	tags, _ := json.Marshal(req.Tags)
	if len(req.Tags) == 0 {
		tags = []byte("[]")
	}
	useSystemProxy := true
	if req.UseSystemProxy != nil {
		useSystemProxy = *req.UseSystemProxy
	}
	return s.store.CreateSite(ctx, &model.Site{Name: name, BaseURL: baseURL, Platform: platform, Enabled: true, Timezone: timezone, UseSystemProxy: useSystemProxy, ProxyURL: strings.TrimSpace(req.ProxyURL), ExternalCheckinURL: strings.TrimSpace(req.ExternalCheckinURL), TagsJSON: string(tags), LastProbeStatus: "unknown"})
}

type siteCreateRequest struct {
	Name               string                `json:"name"`
	BaseURL            string                `json:"base_url"`
	Platform           string                `json:"platform"`
	Timezone           string                `json:"timezone"`
	UseSystemProxy     *bool                 `json:"use_system_proxy"`
	ProxyURL           string                `json:"proxy_url"`
	ExternalCheckinURL string                `json:"external_checkin_url"`
	Tags               []string              `json:"tags"`
	Account            *accountCreateRequest `json:"account,omitempty"`
}
type sitePatchRequest struct {
	Name               *string `json:"name"`
	BaseURL            *string `json:"base_url"`
	Platform           *string `json:"platform"`
	Timezone           *string `json:"timezone"`
	UseSystemProxy     *bool   `json:"use_system_proxy"`
	ProxyURL           *string `json:"proxy_url"`
	ExternalCheckinURL *string `json:"external_checkin_url"`
	Enabled            *bool   `json:"enabled"`
}
type accountCreateRequest struct {
	Label          string               `json:"label"`
	CredentialType string               `json:"credential_type"`
	Credential     provider.Credentials `json:"credential"`
	Enabled        *bool                `json:"enabled"`
	AutoCheckin    *bool                `json:"auto_checkin"`
	AutoRefresh    *bool                `json:"auto_refresh"`
	Timezone       string               `json:"timezone"`
}
type accountPatchRequest struct {
	Label          *string               `json:"label"`
	CredentialType *string               `json:"credential_type"`
	Credential     *provider.Credentials `json:"credential"`
	Enabled        *bool                 `json:"enabled"`
	AutoCheckin    *bool                 `json:"auto_checkin"`
	AutoRefresh    *bool                 `json:"auto_refresh"`
	Timezone       *string               `json:"timezone"`
}

type accountCredentialVerifyRequest struct {
	CredentialType string               `json:"credential_type"`
	Credential     provider.Credentials `json:"credential"`
}

func (s *siteControlService) createAccount(ctx context.Context, siteID int64, req accountCreateRequest) (*model.SiteAccount, error) {
	if s.locked() {
		return nil, credential.ErrCredentialLocked
	}
	label := strings.TrimSpace(req.Label)
	if label == "" || len([]rune(label)) > 191 {
		return nil, errors.New("invalid account label")
	}
	site, err := s.store.GetSite(ctx, siteID)
	if err != nil {
		return nil, errors.New("site not found")
	}
	credentialType := requestedCredentialType(req.CredentialType, req.Credential)
	if strings.TrimSpace(site.Platform) == "" || strings.EqualFold(site.Platform, model.SitePlatformUnknown) {
		detectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		result, detectErr := s.registry.Detect(detectCtx, site.BaseURL)
		cancel()
		if detectErr == nil && result.Matched {
			if result.ProviderID == model.SitePlatformOpenAICompatible && credentialType != model.CredentialTypeAPIKey {
				return nil, &provider.Error{Code: provider.CodeUnsupported, Message: "management provider was not detected; OpenAI Compatible only accepts an API key"}
			}
			site.Platform = result.ProviderID
			site.LastProbeStatus = "success"
			site.LastError = ""
			if updated, updateErr := s.store.UpdateSite(ctx, site.ID, site); updateErr == nil {
				site = updated
				s.pricingSourceChanged(site.ID)
			}
		} else if credentialType == model.CredentialTypeAPIKey && strings.TrimSpace(req.Credential.APIKey) != "" {
			// A model-call key does not require a management-plane provider. If
			// fingerprinting is inconclusive (proxy, WAF, or a non-New-API
			// frontend), preserve the useful OpenAI-compatible routing path.
			site.Platform = model.SitePlatformOpenAICompatible
			site.LastProbeStatus = "unknown"
			site.LastError = ""
			if updated, updateErr := s.store.UpdateSite(ctx, site.ID, site); updateErr == nil {
				site = updated
				s.pricingSourceChanged(site.ID)
			}
		} else if strings.TrimSpace(site.Platform) == "" || strings.EqualFold(site.Platform, model.SitePlatformUnknown) {
			if detectErr != nil {
				return nil, detectErr
			}
			return nil, &provider.Error{
				Code:    provider.CodeUnsupported,
				Message: "site provider is not registered; automatic detection did not identify a supported management API",
			}
		}
	}
	credType, prepared, err := s.prepareAccountCredential(ctx, site, req.CredentialType, req.Credential)
	if err != nil {
		return nil, err
	}
	sealed, err := s.cipher.Seal(prepared)
	if err != nil {
		return nil, err
	}
	enabled, autoCheckin, autoRefresh := true, true, true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.AutoCheckin != nil {
		autoCheckin = *req.AutoCheckin
	}
	if req.AutoRefresh != nil {
		autoRefresh = *req.AutoRefresh
	}
	adapter, _ := s.adapter(site)
	if credType == model.CredentialTypeAPIKey || (adapter != nil && !adapter.Capabilities().ServerCheckin) {
		autoCheckin = false
	}
	if credType == model.CredentialTypeAPIKey {
		autoRefresh = false
	}
	account, err := s.store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: siteID, Label: label, CredentialType: credType, CredentialCiphertext: sealed, CredentialKeyVersion: s.cipher.Version(), Enabled: enabled, AutoCheckin: autoCheckin, AutoRefresh: autoRefresh, Timezone: strings.TrimSpace(req.Timezone), Status: model.SiteAccountStatusUnknown, LastRefreshStatus: "unknown", LastCheckinStatus: "unknown"})
	if err == nil {
		s.pricingSourceChanged(siteID)
	}
	return account, err
}

func (s *siteControlService) prepareAccountCredential(ctx context.Context, site *model.Site, requestedType string, credentials provider.Credentials) (string, provider.Credentials, error) {
	credType := requestedCredentialType(requestedType, credentials)
	if credType != model.CredentialTypeAccessToken && credType != model.CredentialTypeAPIKey && credType != model.CredentialTypeCookie && credType != model.CredentialTypeUsernamePassword {
		return "", provider.Credentials{}, errors.New("unsupported credential_type")
	}
	adapter, adapterErr := s.adapter(site)
	if adapterErr != nil {
		return "", provider.Credentials{}, adapterErr
	}
	// Older console builds exposed Session Cookie for every platform. Treat a
	// Sub2API cookie-shaped submission as its JWT auth token so existing users
	// can repair the account without re-creating the site.
	if adapter.ID() == model.SitePlatformSub2API && credType == model.CredentialTypeCookie && strings.TrimSpace(credentials.Cookie) != "" {
		credType = model.CredentialTypeAccessToken
		credentials.AccessToken = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(credentials.Cookie), "Bearer "))
		credentials.Cookie = ""
		credentials.UserID = 0
	}
	if credType == model.CredentialTypeAccessToken {
		credentials.AccessToken = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(credentials.AccessToken), "Bearer "))
	}
	credentials.RefreshToken = strings.TrimSpace(credentials.RefreshToken)
	if credentials.ExpiresAt == 0 {
		credentials.ExpiresAt = credentials.EffectiveExpiresAt()
	}
	capabilities := adapter.Capabilities()
	if len(capabilities.CredentialTypes) > 0 && !containsCredentialType(capabilities.CredentialTypes, credType) {
		return "", provider.Credentials{}, &provider.Error{Code: provider.CodeUnsupported, Message: "this credential type is not supported by the selected platform"}
	}
	if refresher, ok := adapter.(provider.CredentialRefresher); ok && credType != model.CredentialTypeAPIKey && credentials.RefreshDue(time.Now(), credentialRefreshLead) {
		refreshCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		refreshed, refreshErr := refresher.RefreshCredentials(refreshCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: credentials})
		cancel()
		if refreshErr != nil {
			return "", provider.Credentials{}, refreshErr
		}
		credentials = refreshed
	}
	if credType == model.CredentialTypeUsernamePassword {
		authenticator, ok := adapter.(provider.AccountAuthenticator)
		if !ok {
			return "", provider.Credentials{}, &provider.Error{Code: provider.CodeUnsupported, Message: "this site does not support password login"}
		}
		loginCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
		loggedIn, loginErr := authenticator.Login(loginCtx, provider.LoginRequest{
			BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site),
			Username: credentials.Username, Password: credentials.Password,
		})
		cancel()
		if loginErr != nil {
			return "", provider.Credentials{}, loginErr
		}
		credentials = loggedIn
		if strings.TrimSpace(loggedIn.AccessToken) == "" && strings.TrimSpace(loggedIn.Cookie) != "" {
			credType = model.CredentialTypeCookie
		} else {
			credType = model.CredentialTypeAccessToken
		}
	}
	if credType == model.CredentialTypeCookie && strings.TrimSpace(credentials.Cookie) == "" {
		return "", provider.Credentials{}, errors.New("credential is required")
	}
	if credType != model.CredentialTypeCookie && strings.TrimSpace(credentials.Token()) == "" {
		return "", provider.Credentials{}, errors.New("credential is required")
	}
	if credType != model.CredentialTypeAPIKey {
		if resolver, ok := adapter.(provider.ManagementCredentialResolver); ok {
			resolveCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
			resolved, resolveErr := resolver.ResolveManagementCredentials(resolveCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: credentials})
			cancel()
			if resolveErr != nil {
				return "", provider.Credentials{}, resolveErr
			}
			credentials = resolved
		}
		if credType == model.CredentialTypeCookie && credentials.UserID <= 0 {
			return "", provider.Credentials{}, &provider.Error{Code: provider.CodeUserIDRequired, Message: "New API management credential requires the upstream user ID"}
		}
	}
	// A management session can usually enumerate its model-call keys. Store the
	// first enabled key with the session so later channel synchronization never
	// asks the user to paste it again.
	if credType != model.CredentialTypeAPIKey {
		if keyProvider, ok := adapter.(provider.RoutingKeyProvider); ok {
			keyCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			keys, keyErr := keyProvider.ListRoutingKeys(keyCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: credentials})
			cancel()
			if keyErr == nil {
				for _, item := range keys {
					if item.Enabled && strings.TrimSpace(item.Key) != "" {
						credentials.APIKey = strings.TrimSpace(item.Key)
						break
					}
				}
			}
		}
	}
	credentials.Password = ""
	return credType, credentials, nil
}

func requestedCredentialType(requestedType string, credentials provider.Credentials) string {
	credType := strings.TrimSpace(requestedType)
	if credType != "" {
		return credType
	}
	if credentials.AccessToken != "" {
		return model.CredentialTypeAccessToken
	}
	if credentials.APIKey != "" {
		return model.CredentialTypeAPIKey
	}
	if credentials.Cookie != "" {
		return model.CredentialTypeCookie
	}
	if credentials.Username != "" {
		return model.CredentialTypeUsernamePassword
	}
	return ""
}

func containsCredentialType(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *siteControlService) refreshAccount(ctx context.Context, task *model.SiteTask, accountID int64, modelRefresh bool) {
	s.refreshAccountWithOptions(ctx, task, accountID, modelRefresh, true)
}

func (s *siteControlService) refreshAccountScheduled(ctx context.Context, task *model.SiteTask, accountID int64, modelRefresh bool) {
	s.refreshAccountWithOptions(ctx, task, accountID, modelRefresh, false)
}

func (s *siteControlService) refreshAccountWithOptions(ctx context.Context, task *model.SiteTask, accountID int64, modelRefresh, manual bool) {
	account, err := s.store.GetSiteAccount(ctx, accountID)
	if err != nil {
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", "not_found")
		return
	}
	if modelRefresh {
		// Keep the route progress shape even when discovery fails before any
		// routing key is available; terminal task updates preserve it.
		setSiteTaskRoutes(task, nil)
		defer func() {
			if ctx.Err() != nil {
				return
			}
			// Reload after credential/projection updates; only the refresh result
			// belongs to this finalizer. Balance freshness has its own timestamp.
			latest, saveErr := s.store.GetSiteAccount(ctx, accountID)
			if saveErr == nil {
				latest.LastRefreshAt = time.Now().UnixMilli()
				latest.LastRefreshStatus = task.Status
				_, saveErr = s.store.UpdateSiteAccount(ctx, accountID, latest)
			}
			if saveErr != nil {
				log.Printf("[SITE] persist model refresh result for account %d: %v", accountID, saveErr)
				s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", siteTaskError(saveErr))
			}
		}()
	}
	site, err := s.store.GetSite(ctx, account.SiteID)
	if err != nil {
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", "not_found")
		return
	}
	adapter, err := s.adapter(site)
	if err != nil {
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", siteTaskError(err))
		return
	}
	creds, err := s.operationCredentials(ctx, account, site, adapter)
	if err != nil {
		code := provider.ErrorCode(err)
		if errors.Is(err, credential.ErrCredentialLocked) {
			code = "credential_locked"
		}
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", code)
		return
	}
	if modelRefresh {
		keys, keyErr := s.routingSnapshots(ctx, account, site, adapter, creds)
		if keyErr != nil {
			s.updateTask(ctx, task, model.SiteTaskStatusPartial, fmt.Sprintf("site_account:%d", accountID), siteTaskError(keyErr))
			return
		}
		modelsByProjection, facts, modelErr := s.routingModels(ctx, account, site, adapter, creds, keys)
		if modelErr != nil && len(modelsByProjection) == 0 {
			// The refresh reached model discovery but produced no usable snapshot.
			// Keep the old facts as an explicit stale snapshot so the console can
			// show that this account needs another refresh without routing them as
			// current models.
			if mergeErr := s.store.MergeSiteAccountModels(ctx, account.ID, nil); mergeErr != nil {
				s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", siteTaskError(mergeErr))
				return
			}
			routes := make([]RouteSyncOutcome, 0, len(keys))
			routeNames := s.siteProjectionNames(ctx, account.ID)
			var discoveryErr *routingModelDiscoveryError
			if errors.As(modelErr, &discoveryErr) {
				for index, item := range keys {
					projectionKey := stableProjectionKey(item, index)
					name := routeNames[projectionKey]
					if name == "" {
						name = routeDisplayName(site, account, item)
					}
					if cause := discoveryErr.byProjection[projectionKey]; cause != nil {
						appendRouteProgress(&routes, name, "failed", cause)
					}
				}
			}
			setSiteTaskRoutes(task, routes)
			s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", siteTaskError(modelErr))
			return
		}
		var previousFacts []model.SiteAccountModel
		aliasSnapshotOK := true
		if modelErr == nil {
			snapshot, snapshotErr := s.store.ListSiteAccountModels(ctx, model.SiteModelFilter{SiteAccountID: account.ID})
			if snapshotErr != nil {
				// Without the pre-replace snapshot there is no positive evidence
				// of which members this sync removed.
				log.Printf("[WARN] model refresh could not snapshot prior account models for account %d", accountID)
				aliasSnapshotOK = false
			} else {
				previousFacts = snapshot
			}
		}
		if modelErr == nil {
			// Keep vanished observations stale until projected channels and alias
			// cleanup succeed. They are durable retry evidence even after restart
			// or repeated setting CAS contention; current routing uses projections.
			if err := s.store.MergeSiteAccountModels(ctx, account.ID, facts); err != nil {
				s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", siteTaskError(err))
				return
			}
		} else {
			// A partial refresh must not erase evidence from unresolved keys; keep
			// those older facts marked stale until a complete refresh succeeds.
			if err := s.store.MergeSiteAccountModels(ctx, account.ID, facts); err != nil {
				s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", siteTaskError(err))
				return
			}
		}
		account.Status, account.LastError, account.ConsecutiveFailures = model.SiteAccountStatusHealthy, "", 0
		_, _ = s.store.UpdateSiteAccount(ctx, account.ID, account)
		activeProjectionKeys := make([]string, 0, len(keys))
		syncErrors := make([]string, 0)
		// Keep the configured channel identity beside each projection key so the
		// result can explain which route was updated or removed without exposing
		// routing keys, credentials, or upstream response bodies.
		existingRouteNames := s.siteProjectionNames(ctx, account.ID)
		projectionOwnership := s.siteProjectionOwnership(ctx, account.ID)
		routeOutcomes := make([]RouteSyncOutcome, 0, len(keys)+len(existingRouteNames))
		if !aliasSnapshotOK {
			syncErrors = append(syncErrors, "model_alias_snapshot_failed")
		}
		if modelErr != nil {
			syncErrors = append(syncErrors, siteTaskError(modelErr))
		}
		for index, item := range keys {
			projectionKey := stableProjectionKey(item, index)
			routeName := existingRouteNames[projectionKey]
			if routeName == "" {
				routeName = routeDisplayName(site, account, item)
			}
			pricingGroup := strings.TrimSpace(item.Group)
			models, resolved := modelsByProjection[projectionKey]
			if resolved && len(models) == 0 {
				activeProjectionKeys = append(activeProjectionKeys, projectionKey)
				if projectionOwnership[projectionKey] == "manual" {
					routeOutcomes = append(routeOutcomes, RouteSyncOutcome{Name: routeName, Status: "unchanged"})
					continue
				}
				result, err := s.clearEmptySiteProjection(ctx, account.ID, projectionKey)
				if err != nil {
					syncErrors = append(syncErrors, siteTaskError(err))
					routeOutcomes = append(routeOutcomes, RouteSyncOutcome{Name: routeName, Status: "failed", Error: safeSiteTaskError(siteTaskError(err))})
				} else if result != nil {
					status := "unchanged"
					if result.Action == "deactivated" {
						status = "deactivated"
					}
					if result.Channel != nil && result.Channel.Name != "" {
						routeName = result.Channel.Name
					}
					routeOutcomes = append(routeOutcomes, RouteSyncOutcome{Name: routeName, Status: status})
				}
				continue
			}
			activeProjectionKeys = append(activeProjectionKeys, projectionKey)
			if !resolved {
				discoveryErr := modelErr
				var typed *routingModelDiscoveryError
				if errors.As(modelErr, &typed) {
					discoveryErr = typed.byProjection[projectionKey]
				}
				code := provider.ErrorCode(discoveryErr)
				if code == provider.CodeExpired {
					code = provider.CodeRequestFailed
				}
				if code == "" {
					code = provider.CodeUnsupported
				}
				routeOutcomes = append(routeOutcomes, RouteSyncOutcome{Name: routeName, Status: "failed", Error: safeSiteTaskError(code)})
				continue
			}
			keyCreds := creds
			keyCreds.APIKey = item.Key
			name := routeDisplayName(site, account, item)
			// Automatic account refresh reconciles generated projected channels,
			// but never overrides a binding that an administrator has taken over.
			result, err := s.projectAccountWithModelsForProtocolsOwnership(ctx, account, site, keyCreds, projectionKey, name, item.Protocols, models, true, false, pricingGroup)
			if err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("%s: %s", routingKeyLabel(item, projectionKey), siteTaskError(err)))
				routeOutcomes = append(routeOutcomes, RouteSyncOutcome{Name: routeName, Status: "failed", Error: safeSiteTaskError(siteTaskError(err))})
			} else {
				status := "unchanged"
				if result != nil {
					if result.Channel != nil && result.Channel.Name != "" {
						routeName = result.Channel.Name
					}
					switch result.Action {
					case "created", "updated", "recreated":
						status = "updated"
					case "deactivated":
						status = "deactivated"
					}
				}
				routeOutcomes = append(routeOutcomes, RouteSyncOutcome{Name: routeName, Status: status})
			}
		}
		if err := s.store.DeactivateSiteProjectionsExcept(ctx, account.ID, activeProjectionKeys); err != nil {
			setSiteTaskRoutes(task, routeOutcomes)
			s.updateTask(ctx, task, model.SiteTaskStatusPartial, fmt.Sprintf("site_account:%d", accountID), siteTaskError(err))
			return
		}
		for projectionKey, name := range existingRouteNames {
			if slices.Contains(activeProjectionKeys, projectionKey) {
				continue
			}
			status := "deactivated"
			if projectionOwnership[projectionKey] == "manual" {
				status = "unchanged"
			}
			routeOutcomes = append(routeOutcomes, RouteSyncOutcome{Name: name, Status: status})
		}
		setSiteTaskRoutes(task, routeOutcomes)
		s.projectionChanged()
		aliasReconcileOK := true
		if modelErr == nil && len(previousFacts) > 0 {
			if err := s.reconcileModelAliasesAfterSiteRefresh(ctx, previousFacts, facts); err != nil {
				aliasReconcileOK = false
				log.Printf("[WARN] model alias cleanup after account %d model refresh failed", accountID)
				syncErrors = append(syncErrors, "model_alias_reconcile_failed")
			}
		}
		if modelErr == nil && aliasReconcileOK && len(syncErrors) == 0 {
			// Only discard the positive disappearance evidence after all cleanup
			// has settled. A failed deactivation leaves it available for the next sync.
			if err := s.store.ReplaceSiteAccountModels(ctx, account.ID, facts); err != nil {
				syncErrors = append(syncErrors, "model_snapshot_finalize_failed")
			}
		}
		if len(syncErrors) > 0 {
			s.updateTask(ctx, task, model.SiteTaskStatusPartial, fmt.Sprintf("site_account:%d", accountID), strings.Join(syncErrors, "; "))
			return
		}
		s.updateTask(ctx, task, model.SiteTaskStatusSuccess, fmt.Sprintf("site_account:%d", accountID), "")
		return
	}

	request := provider.RefreshAccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds}
	snapshot, err := retryOnTimeout(ctx, 35*time.Second, func(callCtx context.Context) (provider.AccountSnapshot, error) {
		return adapter.RefreshAccount(callCtx, request)
	})
	if provider.ErrorCode(err) == provider.CodeExpired {
		if refreshed, refreshErr := s.refreshExpiredCredentials(ctx, account, site, adapter, creds); refreshErr == nil {
			creds = refreshed
			request.Credentials = creds
			snapshot, err = retryOnTimeout(ctx, 35*time.Second, func(callCtx context.Context) (provider.AccountSnapshot, error) {
				return adapter.RefreshAccount(callCtx, request)
			})
		} else {
			err = refreshErr
		}
	}
	now := time.Now().UnixMilli()
	account.LastRefreshAt = now
	if err != nil {
		account.LastRefreshStatus = "failed"
		account.LastError = provider.ErrorCode(err)
		if provider.ErrorCode(err) == provider.CodeExpired {
			account.Status = model.SiteAccountStatusExpired
		} else {
			account.Status = model.SiteAccountStatusError
		}
		account.ConsecutiveFailures++
		_, _ = s.store.UpdateSiteAccount(ctx, account.ID, account)
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", siteTaskError(err))
		return
	}
	account.LastRefreshStatus, account.Status, account.LastError, account.ConsecutiveFailures = "success", model.SiteAccountStatusHealthy, "", 0
	applyBalanceSnapshot(account, snapshot, now)
	s.recordBalanceSnapshot(ctx, account, site, now)
	_, _ = s.store.UpdateSiteAccount(ctx, account.ID, account)
	s.evaluateLowBalance(account, site)
	s.updateTask(ctx, task, model.SiteTaskStatusSuccess, fmt.Sprintf("site_account:%d", accountID), "")
}

func (s *siteControlService) routingSnapshots(ctx context.Context, account *model.SiteAccount, site *model.Site, adapter provider.SiteAdapter, creds provider.Credentials) ([]provider.RoutingKeySnapshot, error) {
	if account.CredentialType == model.CredentialTypeAPIKey {
		if strings.TrimSpace(creds.APIKey) == "" {
			return nil, &provider.Error{Code: provider.CodeRoutingKeyUnavailable, Message: "routing API key is unavailable"}
		}
		return []provider.RoutingKeySnapshot{{ID: "account", Name: account.Label, Key: strings.TrimSpace(creds.APIKey), Enabled: true}}, nil
	}
	keyProvider, ok := adapter.(provider.RoutingKeyProvider)
	if !ok {
		return nil, &provider.Error{Code: provider.CodeRoutingKeyUnavailable, Message: "routing API key discovery is unavailable"}
	}
	keyRequest := provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds}
	keys, err := retryOnTimeout(ctx, 20*time.Second, func(callCtx context.Context) ([]provider.RoutingKeySnapshot, error) {
		return keyProvider.ListRoutingKeys(callCtx, keyRequest)
	})
	if provider.ErrorCode(err) == provider.CodeExpired {
		if refreshed, refreshErr := s.refreshExpiredCredentials(ctx, account, site, adapter, creds); refreshErr == nil {
			creds = refreshed
			keyRequest.Credentials = creds
			keys, err = retryOnTimeout(ctx, 20*time.Second, func(callCtx context.Context) ([]provider.RoutingKeySnapshot, error) {
				return keyProvider.ListRoutingKeys(callCtx, keyRequest)
			})
		} else {
			err = refreshErr
		}
	}
	if err != nil {
		return nil, err
	}
	filtered := make([]provider.RoutingKeySnapshot, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key.Enabled && strings.TrimSpace(key.Key) != "" {
			key.Key = strings.TrimSpace(key.Key)
			identity := stableProjectionKey(key, len(filtered))
			if _, exists := seen[identity]; exists {
				continue
			}
			seen[identity] = struct{}{}
			filtered = append(filtered, key)
		}
	}
	if len(filtered) == 0 {
		// The upstream successfully returned an empty/disabled key set. During an
		// explicit route sync this is authoritative and must prune stale projected
		// channels. Request and authentication failures have already returned above.
		return []provider.RoutingKeySnapshot{}, nil
	}
	return filtered, nil
}

// Empty model discovery clears only generated routing state. The channel,
// credential metadata, logs and traces remain available as historical records.
func (s *siteControlService) clearEmptySiteProjection(ctx context.Context, accountID int64, projectionKey string) (*model.SiteProjectionResult, error) {
	bindings, err := s.store.ListSiteChannelBindings(ctx)
	if err != nil {
		return nil, err
	}
	for _, binding := range bindings {
		if binding.SiteAccountID != accountID || binding.ProjectionKey != projectionKey || binding.Ownership != "projected" || binding.ChannelID == 0 {
			continue
		}
		channel, err := s.store.GetConfig(ctx, binding.ChannelID)
		if err != nil {
			return nil, err
		}
		keys, err := s.store.GetAPIKeys(ctx, channel.ID)
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 || len(channel.URLs) == 0 {
			return nil, errors.New("invalid projection input")
		}
		return s.store.UpsertSiteProjection(ctx, model.SiteProjectionInput{SiteAccountID: accountID,
			ProjectionKey: projectionKey, BaseURL: channel.URLs[0].URL, Protocols: channel.URLs[0].Protocols,
			APIKey: keys[0].APIKey, Force: true})
	}
	return nil, nil
}

func (s *siteControlService) siteProjectionNames(ctx context.Context, accountID int64) map[string]string {
	names := make(map[string]string)
	bindings, err := s.store.ListSiteChannelBindings(ctx)
	if err != nil {
		return names
	}
	for _, binding := range bindings {
		if binding == nil || binding.SiteAccountID != accountID || binding.ChannelID <= 0 {
			continue
		}
		channel, err := s.store.GetConfig(ctx, binding.ChannelID)
		if err != nil || channel == nil || strings.TrimSpace(channel.Name) == "" {
			continue
		}
		names[binding.ProjectionKey] = strings.TrimSpace(channel.Name)
	}
	return names
}

func (s *siteControlService) siteProjectionOwnership(ctx context.Context, accountID int64) map[string]string {
	ownership := make(map[string]string)
	bindings, err := s.store.ListSiteChannelBindings(ctx)
	if err != nil {
		return ownership
	}
	for _, binding := range bindings {
		if binding != nil && binding.SiteAccountID == accountID {
			ownership[binding.ProjectionKey] = binding.Ownership
		}
	}
	return ownership
}

func routeDisplayName(site *model.Site, account *model.SiteAccount, item provider.RoutingKeySnapshot) string {
	name := fmt.Sprintf("%s / %s", site.Name, account.Label)
	if group := strings.TrimSpace(item.Group); group != "" {
		name += " / " + group
	}
	if keyName := strings.TrimSpace(item.Name); keyName != "" && keyName != strings.TrimSpace(item.Group) && !strings.EqualFold(keyName, strings.TrimSpace(account.Label)) {
		name += " / " + keyName
	}
	return name
}

func stableProjectionKey(item provider.RoutingKeySnapshot, index int) string {
	identity := strings.TrimSpace(item.ID)
	if identity == "" {
		identity = model.HashToken(item.Key)[:12]
	}
	return "key:" + identity
}

func routingKeyLabel(item provider.RoutingKeySnapshot, fallback string) string {
	for _, value := range []string{item.Group, item.Name, item.ID} {
		if label := strings.TrimSpace(value); label != "" {
			return label
		}
	}
	return fallback
}

func (s *siteControlService) routingModels(ctx context.Context, account *model.SiteAccount, site *model.Site, adapter provider.SiteAdapter, managementCreds provider.Credentials, keys []provider.RoutingKeySnapshot) (map[string][]string, []model.SiteAccountModel, error) {
	modelsByProjection := make(map[string][]string, len(keys))
	type unresolvedKey struct {
		projectionKey string
		item          provider.RoutingKeySnapshot
		err           error
	}
	unresolved := make([]unresolvedKey, 0)
	snapshotNames := make(map[int][]string, len(keys))
	keyNames := make(map[int][]string, len(keys))
	keyErrors := make(map[int]error, len(keys))
	managementNames := make(map[int][]string, len(keys))
	managementAttempted := make(map[int]bool, len(keys))
	var endpointSignature string
	endpointSnapshotKnown := false
	endpointVaries := false
	for index, item := range keys {
		names := normalizedModelNames(item.Models)
		snapshotNames[index] = names
		items, err := retryOnTimeout(ctx, 30*time.Second, func(callCtx context.Context) ([]provider.ModelSnapshot, error) {
			return adapter.ListModels(callCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: provider.Credentials{APIKey: strings.TrimSpace(item.Key)}})
		})
		if err != nil {
			keyErrors[index] = err
			continue
		}
		names = modelSnapshotNames(items)
		keyNames[index] = names
		signature := modelNamesSignature(names)
		if !endpointSnapshotKnown {
			endpointSignature = signature
			endpointSnapshotKnown = true
		} else if signature != endpointSignature {
			endpointVaries = true
		}
	}
	if modelProvider, ok := adapter.(provider.RoutingModelProvider); ok {
		for index, item := range keys {
			if names, known := keyNames[index]; known && len(names) == 0 {
				// A successful exact-key empty list is already scoped. Neither a
				// token snapshot nor a management-wide list may repopulate it.
				continue
			}
			// A missing endpoint needs a management fallback. An identical model
			// list across grouped keys is ambiguous even when the token snapshot
			// also contains models: several New API forks serialize the site-wide
			// model list into every token. Ask the management API for the exact
			// group and treat that result as authoritative when available.
			needsFallback := len(keyNames[index]) == 0 || (!endpointVaries && strings.TrimSpace(item.Group) != "")
			if !needsFallback {
				continue
			}
			managementAttempted[index] = true
			items, err := retryOnTimeout(ctx, 30*time.Second, func(callCtx context.Context) ([]provider.ModelSnapshot, error) {
				return modelProvider.ListModelsForRoutingKey(callCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: managementCreds}, item)
			})
			if err != nil {
				if keyErrors[index] == nil {
					keyErrors[index] = err
				}
				continue
			}
			managementNames[index] = modelSnapshotNames(items)
		}
	}
	for index, item := range keys {
		projectionKey := stableProjectionKey(item, index)
		if names, known := keyNames[index]; known && len(names) == 0 {
			modelsByProjection[projectionKey] = names
			continue
		}
		if names, known := managementNames[index]; known && len(names) == 0 {
			modelsByProjection[projectionKey] = names
			continue
		}
		// Always ask the upstream model endpoint with this exact routing key.
		// Some management APIs return a shared model list on every key, which
		// would otherwise leak one group's models into all projected channels.
		names := keyNames[index]
		// If the per-key endpoint genuinely varies, it is the most precise
		// source (the endpoint may apply hidden provider-side policy). When all
		// keys return the same list, treat explicit management-side model limits
		// as authoritative; otherwise a shared unscoped /models response leaks
		// every model into every group.
		snapshotIsScoped := len(snapshotNames[index]) > 0 && (len(keyNames[index]) == 0 || modelNamesSignature(snapshotNames[index]) != modelNamesSignature(keyNames[index]))
		if !endpointVaries {
			if len(managementNames[index]) > 0 {
				names = managementNames[index]
			} else if len(snapshotNames[index]) > 0 && (!managementAttempted[index] || snapshotIsScoped) {
				names = snapshotNames[index]
			} else if managementAttempted[index] {
				// Do not fall back to a known-unscoped endpoint after a grouped
				// management lookup failed; an incomplete sync is safer than
				// routing a key to models its group cannot use.
				names = nil
			}
		}
		if len(names) == 0 && len(snapshotNames[index]) > 0 && (endpointVaries || !managementAttempted[index] || snapshotIsScoped) {
			names = snapshotNames[index]
		}
		if len(names) == 0 && len(managementNames[index]) > 0 {
			names = managementNames[index]
		}
		if len(names) == 0 && len(keys) == 1 {
			// A single-key deployment can safely use the snapshot returned by
			// the management endpoint, or the management credential itself when
			// the key endpoint is unavailable.
			names = normalizedModelNames(item.Models)
			if len(names) == 0 {
				items, err := retryOnTimeout(ctx, 30*time.Second, func(callCtx context.Context) ([]provider.ModelSnapshot, error) {
					return adapter.ListModels(callCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: managementCreds})
				})
				if err == nil {
					names = modelSnapshotNames(items)
					if len(names) == 0 {
						modelsByProjection[projectionKey] = names
						continue
					}
				} else if keyErrors[index] == nil || provider.ErrorCode(keyErrors[index]) == provider.CodeUnsupported {
					// Nothing else explained the empty result, so this is the only
					// real cause. Dropping it reported a rejected credential as
					// "the site does not support this", which sends the operator
					// looking at the wrong thing entirely.
					keyErrors[index] = err
				}
			}
		}
		if len(names) == 0 {
			unresolved = append(unresolved, unresolvedKey{projectionKey: projectionKey, item: item, err: keyErrors[index]})
			continue
		}
		modelsByProjection[projectionKey] = names
	}
	if len(unresolved) > 0 {
		byProjection := make(map[string]error, len(unresolved))
		for _, item := range unresolved {
			cause := item.err
			if cause == nil {
				cause = &provider.Error{Code: provider.CodeUnsupported, Message: "routing key returned no models"}
			}
			if provider.ErrorCode(cause) == provider.CodeExpired {
				cause = &provider.Error{Code: provider.CodeRequestFailed, Message: provider.ErrorMessage(cause)}
			}
			byProjection[item.projectionKey] = cause
		}
		first := unresolved[0]
		detail := routingKeyLabel(first.item, first.projectionKey)
		if first.err != nil {
			code := provider.ErrorCode(first.err)
			// A per-key /models request can return 401 even while the management
			// session is valid. Do not surface that as an expired management token.
			if code == provider.CodeExpired {
				code = provider.CodeRequestFailed
			}
			cause := &provider.Error{Code: code, Message: fmt.Sprintf("unable to discover models for routing key %s: %s", detail, provider.ErrorMessage(first.err))}
			return modelsByProjection, siteAccountModelsFromProjection(account.ID, modelsByProjection), &routingModelDiscoveryError{cause: cause, byProjection: byProjection}
		}
		cause := &provider.Error{Code: provider.CodeUnsupported, Message: fmt.Sprintf("routing key %s returned no models", detail)}
		return modelsByProjection, siteAccountModelsFromProjection(account.ID, modelsByProjection), &routingModelDiscoveryError{cause: cause, byProjection: byProjection}
	}

	return modelsByProjection, siteAccountModelsFromProjection(account.ID, modelsByProjection), nil
}

func siteAccountModelsFromProjection(accountID int64, modelsByProjection map[string][]string) []model.SiteAccountModel {
	seen := map[string]model.SiteAccountModel{}
	now := time.Now().UnixMilli()
	for _, names := range modelsByProjection {
		for _, name := range names {
			seen[name] = model.SiteAccountModel{SiteAccountID: accountID, Model: name, RouteType: "openai_chat", Source: "routing_key_models", LastSeenAt: now}
		}
	}
	facts := make([]model.SiteAccountModel, 0, len(seen))
	for _, fact := range seen {
		facts = append(facts, fact)
	}
	return facts
}

func modelSnapshotNames(items []provider.ModelSnapshot) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Model)
	}
	return normalizedModelNames(names)
}

func normalizedModelNames(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, exists := seen[item]; exists {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func modelNamesSignature(names []string) string {
	canonical := append([]string(nil), names...)
	slices.Sort(canonical)
	return strings.Join(canonical, "\x00")
}

func (s *siteControlService) refreshModels(ctx context.Context, account *model.SiteAccount, site *model.Site, adapter provider.SiteAdapter, creds provider.Credentials) error {
	items, err := adapter.ListModels(ctx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds})
	if err != nil {
		return err
	}
	facts := make([]model.SiteAccountModel, 0, len(items))
	now := time.Now().UnixMilli()
	for _, item := range items {
		facts = append(facts, model.SiteAccountModel{SiteAccountID: account.ID, Model: item.Model, RouteType: item.RouteType, Source: item.Source, LastSeenAt: now})
	}
	return s.store.ReplaceSiteAccountModels(ctx, account.ID, facts)
}

func (s *siteControlService) ensureRoutingKey(ctx context.Context, account *model.SiteAccount, site *model.Site, adapter provider.SiteAdapter, creds provider.Credentials) (provider.Credentials, error) {
	if strings.TrimSpace(creds.APIKey) != "" {
		return creds, nil
	}
	keyProvider, ok := adapter.(provider.RoutingKeyProvider)
	if !ok {
		return creds, &provider.Error{Code: provider.CodeRoutingKeyUnavailable, Message: "routing API key is unavailable"}
	}
	keyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	keys, err := keyProvider.ListRoutingKeys(keyCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds})
	cancel()
	if err != nil {
		if provider.ErrorCode(err) == provider.CodeUnsupported {
			return creds, &provider.Error{Code: provider.CodeRoutingKeyUnavailable, Message: "routing API key is unavailable"}
		}
		return creds, err
	}
	for _, item := range keys {
		if item.Enabled && strings.TrimSpace(item.Key) != "" {
			creds.APIKey = strings.TrimSpace(item.Key)
			break
		}
	}
	if creds.APIKey == "" {
		return creds, &provider.Error{Code: provider.CodeRoutingKeyUnavailable, Message: "routing API key is unavailable"}
	}
	sealed, err := s.cipher.Seal(creds)
	if err != nil {
		return creds, err
	}
	if err := s.store.UpdateSiteAccountCredential(ctx, account.ID, account.CredentialType, sealed, s.cipher.Version()); err != nil {
		return creds, err
	}
	account.CredentialCiphertext = sealed
	account.CredentialKeyVersion = s.cipher.Version()
	return creds, nil
}

func (s *siteControlService) projectAccount(ctx context.Context, account *model.SiteAccount, site *model.Site, adapter provider.SiteAdapter, creds provider.Credentials, projectionKey, name string, force bool) (*model.SiteProjectionResult, error) {
	var err error
	creds, err = s.ensureRoutingKey(ctx, account, site, adapter, creds)
	if err != nil {
		return nil, err
	}
	models, err := s.store.ListSiteAccountModels(ctx, model.SiteModelFilter{SiteAccountID: account.ID, Limit: 1000})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(models))
	for _, item := range models {
		if !item.Disabled && !item.Stale && strings.TrimSpace(item.Model) != "" {
			names = append(names, item.Model)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("models_required")
	}
	projectionKey = strings.TrimSpace(projectionKey)
	if projectionKey == "" {
		projectionKey = "default"
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("%s / %s", site.Name, account.Label)
	}
	return s.projectAccountWithModels(ctx, account, site, creds, projectionKey, name, names, force)
}

func (s *siteControlService) projectAccountWithModels(ctx context.Context, account *model.SiteAccount, site *model.Site, creds provider.Credentials, projectionKey, name string, names []string, force bool) (*model.SiteProjectionResult, error) {
	return s.projectAccountWithModelsForProtocols(ctx, account, site, creds, projectionKey, name, nil, names, force, "")
}

// pricingGroup 是上游令牌所属分组，仅在按路由 Key 投影时才有值。
// 站点价目表按分组给倍率，落库后才能用站点自身价格算费用。
func (s *siteControlService) projectAccountWithModelsForProtocols(ctx context.Context, account *model.SiteAccount, site *model.Site, creds provider.Credentials, projectionKey, name string, protocols, names []string, force bool, pricingGroup string) (*model.SiteProjectionResult, error) {
	return s.projectAccountWithModelsForProtocolsOwnership(ctx, account, site, creds, projectionKey, name, protocols, names, force, force, pricingGroup)
}

func (s *siteControlService) projectAccountWithModelsForProtocolsOwnership(
	ctx context.Context,
	account *model.SiteAccount,
	site *model.Site,
	creds provider.Credentials,
	projectionKey, name string,
	protocols, names []string,
	force, overrideManual bool,
	pricingGroup string,
) (*model.SiteProjectionResult, error) {
	filtered := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, item := range names {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		filtered = append(filtered, item)
	}
	if len(filtered) == 0 {
		return nil, errors.New("models_required")
	}
	if strings.TrimSpace(projectionKey) == "" {
		projectionKey = "default"
	}
	if strings.TrimSpace(name) == "" {
		name = fmt.Sprintf("%s / %s", site.Name, account.Label)
	}
	protocols = projectedRoutingProtocols(site, protocols)
	baseURL := routingBaseURL(site.BaseURL)
	sourceHash := model.SiteProjectionSourceHash(baseURL, protocols, filtered, creds.APIKey, account.Enabled)
	result, err := s.store.UpsertSiteProjection(ctx, model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: projectionKey, Name: name, BaseURL: baseURL, Protocols: protocols, Models: filtered, APIKey: creds.APIKey, SourceHash: sourceHash, PricingGroup: pricingGroup, Enabled: account.Enabled, Force: force, OverrideManual: overrideManual})
	if err != nil {
		return nil, err
	}
	s.projectionChanged()
	return result, nil
}

func projectedRoutingProtocols(site *model.Site, protocols []string) []string {
	protocols = normalizedModelNames(protocols)
	if len(protocols) > 0 {
		return protocols
	}
	if site != nil {
		if site.Platform == model.SitePlatformAnyRouter {
			return nil
		}
		if parsed, err := url.Parse(strings.TrimSpace(site.BaseURL)); err == nil {
			host := strings.ToLower(parsed.Hostname())
			if strings.Contains(host, "anyrouter") || strings.Contains(host, "agentrouter") || strings.Contains(host, "air-outer") {
				return nil
			}
		}
	}
	return []string{"openai"}
}

func routingBaseURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimRight(strings.TrimSpace(raw), "/")
	}
	cleanPath := strings.TrimRight(u.Path, "/")
	if index := strings.Index(strings.ToLower(cleanPath), "/console"); index >= 0 {
		cleanPath = strings.TrimRight(cleanPath[:index], "/")
	}
	if strings.HasSuffix(cleanPath, "/v1/models") {
		cleanPath = strings.TrimSuffix(cleanPath, "/models")
	}
	cleanPath = strings.TrimSuffix(cleanPath, "/v1")
	u.Path = strings.TrimRight(cleanPath, "/")
	u.RawPath, u.RawQuery, u.Fragment = "", "", ""
	return strings.TrimRight(u.String(), "/")
}

func siteTaskError(err error) string {
	code := provider.ErrorCode(err)
	detail := provider.ErrorMessage(err)
	if detail == "" || detail == code {
		return code
	}
	if strings.HasPrefix(detail, code+": ") {
		return detail
	}
	return code + ": " + detail
}

// applyBalanceSnapshot updates balance freshness only when a successful upstream
// refresh actually returned a balance. Callers must check the refresh error first.
// Keep this separate from LastRefreshAt: a best-effort check-in balance probe must
// not postpone the scheduled account/model sync or change the check-in outcome.
func applyBalanceSnapshot(account *model.SiteAccount, snapshot provider.AccountSnapshot, updatedAt int64) {
	if account == nil || snapshot.Balance == nil {
		return
	}
	balance := *snapshot.Balance
	account.Balance = &balance
	if currency := strings.TrimSpace(snapshot.Currency); currency != "" {
		account.BalanceCurrency = currency
	}
	account.BalanceUpdatedAt = updatedAt
}

// recordBalanceSnapshot persists the latest known balance for the account's
// local calendar day. It is deliberately best-effort: failing to write history
// must not turn a successful upstream balance refresh into a failed task.
func (s *siteControlService) recordBalanceSnapshot(ctx context.Context, account *model.SiteAccount, site *model.Site, updatedAt int64) {
	if s == nil || s.store == nil || account == nil || account.Balance == nil || updatedAt <= 0 {
		return
	}
	currency := strings.ToUpper(strings.TrimSpace(account.BalanceCurrency))
	if currency == "" {
		currency = "CNY"
	}
	day := time.UnixMilli(updatedAt).In(loadSiteLocation(account.Timezone, site.Timezone)).Format("2006-01-02")
	err := s.store.UpsertSiteAccountBalanceSnapshot(ctx, &model.SiteAccountBalanceSnapshot{
		SiteAccountID: account.ID,
		LocalDay:      day,
		Currency:      currency,
		Balance:       *account.Balance,
		UpdatedAt:     updatedAt,
	})
	if err != nil {
		log.Printf("[SITE] persist balance snapshot for account %d on %s: %v", account.ID, day, err)
	}
}

// siteCheckinMethodTTL bounds how long a discovered check-in capability is
// trusted. The upstream status endpoint can flip — an operator switches check-in
// on, or turns Turnstile on — so the remembered answer expires instead of sticking.
const siteCheckinMethodTTL = 6 * time.Hour

// cachedCheckinMethod returns the site's remembered capability while it is still
// fresh. It is a pure read, so the decision stays testable without a store.
func cachedCheckinMethod(site *model.Site, now time.Time) (provider.CheckinMethod, bool) {
	if site == nil || site.CheckinMethod == "" || site.CheckinMethod == provider.CheckinMethodUnknown {
		return provider.CheckinMethod{}, false
	}
	if site.CheckinMethodCheckedAt <= 0 || now.UnixMilli()-site.CheckinMethodCheckedAt >= siteCheckinMethodTTL.Milliseconds() {
		return provider.CheckinMethod{}, false
	}
	return provider.CheckinMethod{Status: site.CheckinMethod, Source: "cache"}, true
}

// resolveCheckinMethod answers "how can this site be checked in?" from the cached
// site fact when it is fresh, and otherwise probes the public status endpoint and
// remembers the answer. The capability belongs to the site, not to an account, so
// caching it here spares every account a probe on every attempt — a site with five
// accounts used to re-ask the same public question five times a day, and up to
// eighty times when the check-in kept failing.
//
// It stays deliberately best-effort: when the probe fails, or the site does not
// publish the flag, callers fall back to attempting the check-in the historical
// way. An "unknown" answer is reported as not-known so nobody acts on a guess.
func (s *siteControlService) resolveCheckinMethod(ctx context.Context, site *model.Site, adapter provider.SiteAdapter) (provider.CheckinMethod, bool) {
	if method, ok := cachedCheckinMethod(site, time.Now()); ok {
		return method, true
	}
	prober, ok := adapter.(provider.CheckinMethodProvider)
	if !ok {
		return provider.CheckinMethod{}, false
	}
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	method, err := prober.DiscoverCheckin(probeCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site)})
	if err != nil {
		log.Printf("[SITE] discover check-in method for site %d: %v", site.ID, err)
		return provider.CheckinMethod{}, false
	}
	if method.Status == "" || method.Status == provider.CheckinMethodUnknown {
		// Nothing worth remembering: the site does not publish the flag.
		return method, false
	}
	checkedAt := time.Now().UnixMilli()
	if err := s.store.UpdateSiteCheckinMethod(ctx, site.ID, method.Status, checkedAt); err != nil {
		log.Printf("[SITE] persist check-in method for site %d: %v", site.ID, err)
	}
	// Keep the caller's copy consistent so it does not probe again in this run.
	site.CheckinMethod = method.Status
	site.CheckinMethodCheckedAt = checkedAt
	return method, true
}

// rememberCheckinUnavailable records that the site answered 404 for its check-in
// endpoint, which means it does not serve one.
//
// The public status endpoint can advertise check-in as available while the route
// it implies answers 404 — AnyRouter builds are the known case, where every
// check-in path is absent and each attempt is a POST that can only be rejected.
// Without this the result reads as an ordinary recoverable failure, so the
// scheduler keeps re-posting on its retry cadence, and for a site sitting behind
// a WAF that request stream is indistinguishable from probing. Writing the site
// fact lets the next run skip the request entirely.
//
// It expires on the usual TTL, so a site that later grows the route is picked up
// again. The write is detached from the caller's context: it has to survive the
// attempt that just finished, including one whose deadline expired mid-flight.
func (s *siteControlService) rememberCheckinUnavailable(ctx context.Context, site *model.Site) {
	if site == nil {
		return
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	checkedAt := time.Now().UnixMilli()
	if err := s.store.UpdateSiteCheckinMethod(persistCtx, site.ID, provider.CheckinMethodUnavailable, checkedAt); err != nil {
		log.Printf("[SITE] persist check-in unavailable for site %d: %v", site.ID, err)
		return
	}
	// Keep the caller's copy consistent so the rest of this run agrees with what
	// was just stored.
	site.CheckinMethod = provider.CheckinMethodUnavailable
	site.CheckinMethodCheckedAt = checkedAt
}

// prepareCheckinAttempt persists the row this run reports through. The table
// holds one row per account, local day, and trigger scope, so a scheduled
// check-in that is retried later in the day has to reuse the row its earlier
// attempt created — a second insert is rejected by the unique key. Reusing it
// also keeps the account's history to one entry per day and lets attempt_no
// count the retries, which is what the scheduler paces against.
func (s *siteControlService) prepareCheckinAttempt(ctx context.Context, run *model.CheckinRun, account *model.SiteAccount, adapter provider.SiteAdapter, day, triggerScope string, balanceBefore *float64) (*model.CheckinAttempt, error) {
	attempt := &model.CheckinAttempt{
		RunID:           run.ID,
		SiteAccountID:   account.ID,
		ProviderID:      adapter.ID(),
		LocalDay:        day,
		TriggerScope:    triggerScope,
		Status:          "running",
		AttemptNo:       1,
		StartedAt:       time.Now().UnixMilli(),
		BalanceBefore:   balanceBefore,
		BalanceCurrency: account.BalanceCurrency,
	}
	if triggerScope == "daily" {
		existing, err := s.store.GetDailyCheckinAttempt(ctx, account.ID, day)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			attempt.ID = existing.ID
			attempt.AttemptNo = existing.AttemptNo + 1
			if attempt.BalanceBefore == nil {
				attempt.BalanceBefore = existing.BalanceBefore
			}
			// Every other field stays at its zero value on purpose: the row is
			// reset to "running" so a stale reward or error from the previous
			// attempt cannot leak into the new result.
			if err := s.store.UpdateCheckinAttempt(ctx, attempt); err != nil {
				return nil, err
			}
			return attempt, nil
		}
	}
	return s.store.CreateCheckinAttempt(ctx, attempt)
}

func (s *siteControlService) checkin(ctx context.Context, task *model.SiteTask, accountID int64) {
	s.checkinWithTrigger(ctx, task, accountID, "manual", "manual:"+task.ID)
}

func (s *siteControlService) checkinWithTrigger(ctx context.Context, task *model.SiteTask, accountID int64, trigger, triggerScope string) {
	account, err := s.store.GetSiteAccount(ctx, accountID)
	if err != nil {
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", "not_found")
		return
	}
	site, err := s.store.GetSite(ctx, account.SiteID)
	if err != nil {
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", "not_found")
		return
	}
	adapter, err := s.adapter(site)
	if err != nil {
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", provider.ErrorCode(err))
		return
	}
	creds, err := s.operationCredentials(ctx, account, site, adapter)
	if err != nil {
		code := provider.ErrorCode(err)
		if errors.Is(err, credential.ErrCredentialLocked) {
			code = "credential_locked"
		}
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", code)
		return
	}
	day := time.Now().In(loadSiteLocation(account.Timezone, site.Timezone)).Format("2006-01-02")
	run, err := s.store.CreateCheckinRun(ctx, &model.CheckinRun{Trigger: trigger, LocalDay: day, Timezone: loadSiteLocation(account.Timezone, site.Timezone).String(), Status: model.SiteTaskStatusRunning, Total: 1})
	if err != nil {
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", err.Error())
		return
	}
	var attempt *model.CheckinAttempt
	// Cancellation cannot write through the cancelled execution context. Leave
	// a finished attempt so the scheduler does not mistake it for a crashed run
	// and send the same side-effecting POST on its next tick.
	defer func() {
		if ctx.Err() == nil {
			return
		}
		persistCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		run.FinishedAt = time.Now().UnixMilli()
		if attempt != nil && checkinSettled(attempt.Status) {
			run.Status = model.SiteTaskStatusSuccess
		} else {
			run.Status = model.SiteTaskStatusCancelled
			run.SuccessCount, run.AlreadyCount, run.FailedCount, run.BrowserRequiredCount = 0, 0, 0, 0
			if attempt != nil {
				attempt.Status, attempt.ErrorCode = model.SiteTaskStatusCancelled, "cancelled"
				attempt.Message = "任务已取消；已提交的上游操作不会撤回，请核实签到结果"
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					run.Status, run.FailedCount = model.SiteTaskStatusFailed, 1
					attempt.Status, attempt.ErrorCode = provider.CheckinFailed, provider.CodeTimeout
				}
			}
		}
		if attempt != nil {
			attempt.FinishedAt = run.FinishedAt
			_ = s.store.UpdateCheckinAttempt(persistCtx, attempt)
		}
		_ = s.store.UpdateCheckinRun(persistCtx, run)
	}()
	var balanceBefore *float64
	if account.Balance != nil {
		value := *account.Balance
		balanceBefore = &value
	}
	preRefreshCtx, cancelPreRefresh := context.WithTimeout(ctx, 20*time.Second)
	preSnapshot, preRefreshErr := adapter.RefreshAccount(preRefreshCtx, provider.RefreshAccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds})
	cancelPreRefresh()
	if preRefreshErr == nil && preSnapshot.Balance != nil {
		value := *preSnapshot.Balance
		balanceBefore = &value
		applyBalanceSnapshot(account, preSnapshot, time.Now().UnixMilli())
		s.recordBalanceSnapshot(ctx, account, site, account.BalanceUpdatedAt)
	}
	attempt, err = s.prepareCheckinAttempt(ctx, run, account, adapter, day, triggerScope, balanceBefore)
	if err != nil {
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", err.Error())
		return
	}
	var result provider.CheckinResult
	// Discover the site's check-in method before spending a request. A site that
	// publishes check-in as disabled rejects every POST, so recording that as an
	// explicit "unsupported" beats surfacing an opaque upstream error. A
	// Turnstile site is still attempted on purpose: the upstream may answer
	// "already checked" once a human finished the browser step, and the
	// challenge only matters when the attempt actually fails.
	method, methodKnown := s.resolveCheckinMethod(ctx, site, adapter)
	// Two remembered facts mean "do not spend the request": the site says it
	// switched check-in off, or a previous attempt found no such route. They are
	// kept apart because they read differently to an operator, but the scheduler
	// treats them the same.
	skipCheckin := methodKnown && (method.Status == provider.CheckinMethodDisabled || method.Status == provider.CheckinMethodUnavailable)
	if skipCheckin {
		message, reason := "站点已关闭签到", "站点已关闭签到（checkin_enabled=false）"
		if method.Status == provider.CheckinMethodUnavailable {
			message, reason = "站点没有签到接口", "站点没有签到接口（签到端点返回 404）"
		}
		result = provider.CheckinResult{Status: provider.CheckinUnsupported, Message: message}
		err = &provider.Error{Code: provider.CodeUnsupported, Message: reason}
	}
	for try := 1; !skipCheckin && try <= 3; try++ {
		result, err = adapter.Checkin(ctx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds})
		if err == nil || provider.ErrorCode(err) == provider.CodeRateLimited || provider.ErrorCode(err) == provider.CodeBrowserRequired || provider.ErrorCode(err) == provider.CodeUnsupported || provider.ErrorCode(err) == provider.CodeExpired || provider.ErrorCode(err) == provider.CodeUserIDRequired {
			break
		}
		if try < 3 {
			select {
			case <-ctx.Done():
				err = ctx.Err()
				try = 3
			case <-time.After(time.Duration(try) * time.Second):
			}
		}
	}
	if provider.ErrorCode(err) == provider.CodeBrowserRequired {
		if statusProvider, ok := adapter.(provider.CheckinStatusProvider); ok {
			statusCtx, cancelStatus := context.WithTimeout(ctx, 15*time.Second)
			checkedToday, statusErr := statusProvider.CheckedInToday(statusCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds})
			cancelStatus()
			if statusErr == nil && checkedToday {
				result = provider.CheckinResult{Status: provider.CheckinAlreadyChecked, Message: "上游记录今日已签到"}
				err = nil
			}
		}
	}
	if methodKnown && method.Status == provider.CheckinMethodTurnstile && provider.ErrorCode(err) == provider.CodeBrowserRequired {
		// The public status endpoint already named the blocker: an interactive
		// Turnstile challenge. Say so, instead of leaving the operator to guess
		// whether the credential or the site is at fault.
		result.Message = turnstileCheckinMessage(adapter)
	}
	// A 404 is a fact about the site, not about this attempt: the route does not
	// exist here, so no retry can succeed and every one of them is another
	// request the site's guard gets to count. Remember it the way a published
	// capability is remembered, so the next scheduled run skips the request
	// instead of re-posting on the ordinary retry cadence.
	if provider.ErrorStatusCode(err) == http.StatusNotFound {
		s.rememberCheckinUnavailable(ctx, site)
	}
	attempt.Status = result.Status
	attempt.Message = result.Message
	attempt.RewardText = result.RewardText
	attempt.FinishedAt = time.Now().UnixMilli()
	if err != nil {
		attempt.ErrorCode = provider.ErrorCode(err)
	}
	run.FinishedAt = attempt.FinishedAt
	switch result.Status {
	case provider.CheckinSuccess:
		run.Status = model.SiteTaskStatusSuccess
		run.SuccessCount = 1
		account.LastCheckinStatus = provider.CheckinSuccess
		account.LastCheckinAt = attempt.FinishedAt
		refreshCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		snapshot, refreshErr := adapter.RefreshAccount(refreshCtx, provider.RefreshAccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds})
		cancel()
		if refreshErr == nil && snapshot.Balance != nil {
			after := *snapshot.Balance
			attempt.BalanceAfter = &after
			if balanceBefore != nil {
				delta := after - *balanceBefore
				attempt.BalanceDelta = &delta
				if delta > 0.000001 {
					attempt.RewardText = fmt.Sprintf("+%.2f %s", delta, fallbackString(strings.TrimSpace(snapshot.Currency), account.BalanceCurrency))
				}
			}
			applyBalanceSnapshot(account, snapshot, time.Now().UnixMilli())
			s.recordBalanceSnapshot(ctx, account, site, account.BalanceUpdatedAt)
			attempt.BalanceCurrency = account.BalanceCurrency
			account.LastRefreshAt = account.BalanceUpdatedAt
			account.LastRefreshStatus = "success"
			account.Status = model.SiteAccountStatusHealthy
			account.LastError = ""
			account.ConsecutiveFailures = 0
		}
	case provider.CheckinAlreadyChecked:
		run.Status = model.SiteTaskStatusSuccess
		run.AlreadyCount = 1
		account.LastCheckinStatus = provider.CheckinAlreadyChecked
		account.LastCheckinAt = attempt.FinishedAt
	case provider.CheckinBrowserRequired:
		// A challenge that no server-side retry can clear. Give it its own
		// outcome so last_checkin_status and browser_required_count read
		// "waiting for a human" instead of "broken", and so the failure webhook
		// stays quiet: the console already flags the account as needing
		// attention, and a Turnstile site would otherwise page every day.
		run.Status = model.SiteTaskStatusFailed
		run.BrowserRequiredCount = 1
		account.LastCheckinStatus = provider.CheckinBrowserRequired
	default:
		run.Status = model.SiteTaskStatusFailed
		run.FailedCount = 1
		account.LastCheckinStatus = result.Status
	}
	// The check-in result is authoritative even if cancellation interrupts the
	// optional balance read that follows it. Persist the observed side effect.
	persistCtx := ctx
	if ctx.Err() != nil && checkinSettled(attempt.Status) {
		var cancel context.CancelFunc
		persistCtx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
	}
	_ = s.store.UpdateCheckinAttempt(persistCtx, attempt)
	_ = s.store.UpdateCheckinRun(persistCtx, run)
	_, _ = s.store.UpdateSiteAccount(persistCtx, account.ID, account)
	if result.Status == provider.CheckinSuccess && attempt.BalanceAfter != nil {
		s.evaluateLowBalance(account, site)
	}
	if result.Status == provider.CheckinFailed && ctx.Err() == nil {
		s.notifyCheckinFailure(account, site, day, attempt.ErrorCode)
	}
	if err != nil {
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "checkin_run:"+fmt.Sprint(run.ID), provider.ErrorCode(err))
		return
	}
	s.updateTask(ctx, task, model.SiteTaskStatusSuccess, "checkin_run:"+fmt.Sprint(run.ID), "")
}

// turnstileCheckinMessage explains that a Turnstile challenge needs a human.
//
// Only an adapter that implements CheckinStatusProvider can reconcile the day
// afterwards, so only those may promise a re-check. Veloera sat behind the
// promise without implementing it until its own status route was wired up, and
// AnyRouter cannot implement it at all — its upstream publishes no status
// endpoint. Promising there would leave the operator waiting on a
// reconciliation that never runs.
func turnstileCheckinMessage(adapter provider.SiteAdapter) string {
	if _, ok := adapter.(provider.CheckinStatusProvider); ok {
		return "站点启用了 Turnstile 人机验证，服务端无法自动完成；请在浏览器完成签到后由系统复核"
	}
	return "站点启用了 Turnstile 人机验证，服务端无法自动完成；请在浏览器完成签到"
}

func loadSiteLocation(accountTZ, siteTZ string) *time.Location {
	tz := strings.TrimSpace(accountTZ)
	if tz == "" {
		tz = strings.TrimSpace(siteTZ)
	}
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.FixedZone(tz, 8*60*60)
	}
	return loc
}

func (s *siteControlService) refreshAnnouncements(ctx context.Context, siteID int64) error {
	site, err := s.store.GetSite(ctx, siteID)
	if err != nil {
		return err
	}
	adapter, err := s.adapter(site)
	if err != nil {
		return err
	}
	if !adapter.Capabilities().Announcements {
		return &provider.Error{Code: provider.CodeUnsupported, Message: "该站点不提供公告接口"}
	}
	request := provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site)}
	var selectedAccount *model.SiteAccount
	accounts, listErr := s.store.ListSiteAccounts(ctx, siteID, false)
	if listErr == nil {
		for _, account := range accounts {
			if !account.Enabled {
				continue
			}
			credentials, credentialErr := s.operationCredentials(ctx, account, site, adapter)
			if credentialErr == nil {
				request.Credentials = credentials
				selectedAccount = account
				break
			}
		}
	}
	items, err := adapter.ListAnnouncements(ctx, request)
	if provider.ErrorCode(err) == provider.CodeExpired && selectedAccount != nil {
		if refreshed, refreshErr := s.refreshExpiredCredentials(ctx, selectedAccount, site, adapter, request.Credentials); refreshErr == nil {
			request.Credentials = refreshed
			items, err = adapter.ListAnnouncements(ctx, request)
		} else {
			err = refreshErr
		}
	}
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	out := make([]model.SiteAnnouncement, 0, len(items))
	for _, item := range items {
		out = append(out, model.SiteAnnouncement{SiteID: siteID, SourceKey: item.SourceKey, Title: item.Title, ContentMarkdown: item.ContentMarkdown, Level: item.Level, SourceURL: resolveAnnouncementSourceURL(site.BaseURL, item.SourceURL), UpstreamCreatedAt: item.UpstreamCreatedAt, UpstreamUpdatedAt: item.UpstreamUpdatedAt, FirstSeenAt: now, LastSeenAt: now, ContentHash: item.ContentHash})
	}
	return s.store.UpsertSiteAnnouncements(ctx, out)
}

func resolveAnnouncementSourceURL(baseURL, sourceURL string) string {
	sourceURL = strings.TrimSpace(sourceURL)
	if sourceURL == "" {
		return ""
	}
	parsed, err := url.Parse(sourceURL)
	if err != nil {
		return ""
	}
	if parsed.IsAbs() {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return ""
		}
		return parsed.String()
	}
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/")
	if err != nil || base.Scheme == "" || base.Host == "" {
		return ""
	}
	// Provider announcement endpoints return JSON rather than a readable page.
	// Link to the upstream site itself when no public announcement page exists.
	if strings.HasPrefix(parsed.Path, "/api/") {
		return strings.TrimRight(base.String(), "/")
	}
	return base.ResolveReference(parsed).String()
}

func (s *siteControlService) handleSites(c *gin.Context) {
	ctx := c.Request.Context()
	switch c.Request.Method {
	case http.MethodGet:
		sites, err := s.store.ListSites(ctx, model.SiteListFilter{IncludeDeleted: c.Query("include_deleted") == "true"})
		if err != nil {
			RespondError(c, 500, err)
			return
		}
		RespondJSON(c, 200, sites)
	case http.MethodPost:
		var req siteCreateRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			RespondErrorMsg(c, 400, "invalid_request")
			return
		}
		site, err := s.createSite(ctx, req)
		if err != nil {
			RespondErrorMsg(c, 400, err.Error())
			return
		}
		if req.Account != nil {
			if _, err := s.createAccount(ctx, site.ID, *req.Account); err != nil {
				_ = s.store.DeleteSite(context.Background(), site.ID)
				if errors.Is(err, credential.ErrCredentialLocked) {
					RespondErrorMsg(c, http.StatusLocked, "credential_locked")
					return
				}
				if provider.ErrorCode(err) != provider.CodeRequestFailed || provider.ErrorStatusCode(err) > 0 {
					respondSiteProviderError(c, http.StatusBadRequest, err)
					return
				}
				RespondErrorMsg(c, http.StatusBadRequest, err.Error())
				return
			}
		}
		RespondJSON(c, 201, site)
	}
}
func (s *siteControlService) handleSiteByID(c *gin.Context) {
	id, err := ParseInt64Param(c, "id")
	if err != nil {
		RespondErrorMsg(c, 400, "invalid_request")
		return
	}
	ctx := c.Request.Context()
	site, err := s.store.GetSite(ctx, id)
	if err != nil {
		RespondErrorMsg(c, 404, "not_found")
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
		RespondJSON(c, 200, site)
	case http.MethodPatch:
		var req sitePatchRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			RespondErrorMsg(c, 400, "invalid_request")
			return
		}
		previous := *site
		if req.Name != nil {
			site.Name = strings.TrimSpace(*req.Name)
		}
		if req.BaseURL != nil {
			site.BaseURL, err = normalizeSiteURL(*req.BaseURL)
			if err != nil {
				RespondErrorMsg(c, 400, err.Error())
				return
			}
		}
		if req.Platform != nil {
			site.Platform = strings.TrimSpace(*req.Platform)
		}
		if req.Timezone != nil {
			site.Timezone = strings.TrimSpace(*req.Timezone)
		}
		if req.UseSystemProxy != nil {
			site.UseSystemProxy = *req.UseSystemProxy
		}
		if req.ProxyURL != nil {
			site.ProxyURL = strings.TrimSpace(*req.ProxyURL)
		}
		if req.ExternalCheckinURL != nil {
			site.ExternalCheckinURL = strings.TrimSpace(*req.ExternalCheckinURL)
		}
		// 记录切换前的状态：只有真正发生启停变化时才做级联。
		enabledChanged := req.Enabled != nil && *req.Enabled != site.Enabled
		if req.Enabled != nil {
			site.Enabled = *req.Enabled
		}
		out, err := s.store.UpdateSite(ctx, id, site)
		if err != nil {
			RespondError(c, 400, err)
			return
		}

		// 站点启停级联到其账号与投影渠道：禁用站点却留着渠道继续路由，
		// 与用户的意图相反。恢复时只放开被级联停用的，手动停用的保持原状。
		var cascade *siteCascadeResult
		if enabledChanged {
			accounts, channels, cascadeErr := s.store.CascadeSiteSuspend(ctx, id, site.Enabled)
			if cascadeErr != nil {
				log.Printf("[WARN] 站点 %d 级联%s失败: %v", id, cascadeVerb(site.Enabled), cascadeErr)
			} else {
				cascade = &siteCascadeResult{Accounts: accounts, Channels: channels, Enabled: site.Enabled}
				if accounts > 0 || channels > 0 {
					log.Printf("[SITE] 站点 %d 已%s，级联%s %d 个账号、%d 个渠道",
						id, cascadeVerb(site.Enabled), cascadeVerb(site.Enabled), accounts, channels)
				}
			}
		}

		s.projectionChanged()
		if previous.BaseURL != site.BaseURL || previous.Platform != site.Platform || previous.ProxyURL != site.ProxyURL || previous.UseSystemProxy != site.UseSystemProxy || enabledChanged {
			s.pricingSourceChanged(id)
		}
		if cascade != nil {
			// 保持返回体仍是一个 Site：级联结果作为附加字段挂上去，
			// 换成 {site, cascade} 会破坏既有前端契约。
			RespondJSON(c, 200, siteWithCascade{Site: out, Cascade: cascade})
			return
		}
		RespondJSON(c, 200, out)
	case http.MethodDelete:
		if err := s.store.DeleteSite(ctx, id); err != nil {
			RespondError(c, 500, err)
			return
		}
		s.projectionChanged()
		s.pricingSourceChanged(id)
		RespondJSON(c, 200, gin.H{"id": id, "deleted": true})
	}
}
