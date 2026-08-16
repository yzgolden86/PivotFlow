package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
)

type siteControlService struct {
	store               storage.Store
	cipher              *credential.Cipher
	registry            *provider.Registry
	baseCtx             context.Context
	wg                  *sync.WaitGroup
	taskMu              sync.Mutex
	webhookMu           sync.Mutex
	tasks               map[string]context.CancelFunc
	stopped             bool
	webhookSender       sitewebhook.Sender
	onProjectionChanged func()
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
	return &siteControlService{
		store:  store,
		cipher: cipher,
		registry: provider.NewRegistry(
			provider.NewSub2API(provider.ClientFactory{}),
			provider.NewVeloera(provider.ClientFactory{}),
			provider.NewAnyRouter(provider.ClientFactory{}),
			provider.NewNewAPI(provider.ClientFactory{}),
			provider.NewOpenAICompatible(provider.ClientFactory{}),
		),
		baseCtx:       baseCtx,
		wg:            wg,
		tasks:         make(map[string]context.CancelFunc),
		webhookSender: sitewebhook.Sender{Clients: provider.ClientFactory{}, Timeout: 5 * time.Second},
	}
}

func (s *siteControlService) locked() bool { return s == nil || s.cipher == nil }

func (s *siteControlService) projectionChanged() {
	if s != nil && s.onProjectionChanged != nil {
		s.onProjectionChanged()
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

func (s *siteControlService) createTask(ctx context.Context, kind string, siteID, accountID int64, total int) (*model.SiteTask, error) {
	progress, _ := json.Marshal(gin.H{"completed": 0, "total": total})
	task := &model.SiteTask{ID: newSiteTaskID(), Kind: kind, Status: model.SiteTaskStatusQueued, SiteID: siteID, SiteAccountID: accountID, ProgressJSON: string(progress), CreatedAt: time.Now().UnixMilli()}
	if err := s.store.CreateSiteTask(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *siteControlService) updateTask(ctx context.Context, task *model.SiteTask, status, resultRef, message string) {
	task.Status, task.ResultRef, task.Error = status, resultRef, message
	now := time.Now().UnixMilli()
	if status == model.SiteTaskStatusRunning {
		task.StartedAt = now
	}
	if status == model.SiteTaskStatusSuccess || status == model.SiteTaskStatusPartial || status == model.SiteTaskStatusFailed || status == model.SiteTaskStatusCancelled {
		task.FinishedAt = now
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
	ctx, cancel := context.WithCancel(parent)
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
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() {
		cancel()
		<-done
	}
}

func (s *siteControlService) adapter(site *model.Site) (provider.SiteAdapter, error) {
	id := strings.ToLower(strings.TrimSpace(site.Platform))
	if id == "new-api" || id == "newapi" || id == "new-api-family" || id == "one-api" || id == "oneapi" || id == "one-hub" || id == "onehub" || id == "done-hub" || id == "donehub" || id == "voapi" || id == "axon-hub" || id == "axonhub" {
		id = model.SitePlatformNewAPIFamily
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
	if next.ExpiresAt == 0 {
		next.ExpiresAt = next.EffectiveExpiresAt()
		if next.ExpiresAt == 0 && strings.TrimSpace(next.AccessToken) == strings.TrimSpace(existing.AccessToken) {
			next.ExpiresAt = existing.EffectiveExpiresAt()
		}
	}
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
	if err := provider.ValidateBaseURL(raw, false); err != nil {
		return "", err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery, u.Fragment = "", ""
	return strings.TrimRight(u.String(), "/"), nil
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
	return s.store.CreateSiteAccount(ctx, &model.SiteAccount{SiteID: siteID, Label: label, CredentialType: credType, CredentialCiphertext: sealed, CredentialKeyVersion: s.cipher.Version(), Enabled: enabled, AutoCheckin: autoCheckin, AutoRefresh: autoRefresh, Timezone: strings.TrimSpace(req.Timezone), Status: model.SiteAccountStatusUnknown, LastRefreshStatus: "unknown", LastCheckinStatus: "unknown"})
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
	if modelRefresh {
		keys, keyErr := s.routingSnapshots(ctx, account, site, adapter, creds)
		if keyErr != nil {
			s.updateTask(ctx, task, model.SiteTaskStatusPartial, fmt.Sprintf("site_account:%d", accountID), siteTaskError(keyErr))
			return
		}
		modelsByProjection, facts, modelErr := s.routingModels(ctx, account, site, adapter, creds, keys)
		if modelErr != nil && len(modelsByProjection) == 0 {
			s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", siteTaskError(modelErr))
			return
		}
		if len(facts) > 0 || modelErr == nil {
			if err := s.store.ReplaceSiteAccountModels(ctx, account.ID, facts); err != nil {
				s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", siteTaskError(err))
				return
			}
		}
		account.Status, account.LastError, account.ConsecutiveFailures = model.SiteAccountStatusHealthy, "", 0
		_, _ = s.store.UpdateSiteAccount(ctx, account.ID, account)
		activeProjectionKeys := make([]string, 0, len(keys))
		syncErrors := make([]string, 0)
		if modelErr != nil {
			syncErrors = append(syncErrors, siteTaskError(modelErr))
		}
		for index, item := range keys {
			projectionKey := stableProjectionKey(item, index)
			activeProjectionKeys = append(activeProjectionKeys, projectionKey)
			models, resolved := modelsByProjection[projectionKey]
			if !resolved || len(models) == 0 {
				continue
			}
			keyCreds := creds
			keyCreds.APIKey = item.Key
			name := fmt.Sprintf("%s / %s", site.Name, account.Label)
			if strings.TrimSpace(item.Group) != "" {
				name += " / " + strings.TrimSpace(item.Group)
			}
			if strings.TrimSpace(item.Name) != "" && strings.TrimSpace(item.Name) != strings.TrimSpace(item.Group) && !strings.EqualFold(strings.TrimSpace(item.Name), strings.TrimSpace(account.Label)) {
				name += " / " + strings.TrimSpace(item.Name)
			}
			if _, err := s.projectAccountWithModels(ctx, account, site, keyCreds, projectionKey, name, models, false); err != nil {
				syncErrors = append(syncErrors, fmt.Sprintf("%s: %s", routingKeyLabel(item, projectionKey), siteTaskError(err)))
			}
		}
		if err := s.store.DeactivateSiteProjectionsExcept(ctx, account.ID, activeProjectionKeys); err != nil {
			s.updateTask(ctx, task, model.SiteTaskStatusPartial, fmt.Sprintf("site_account:%d", accountID), siteTaskError(err))
			return
		}
		s.projectionChanged()
		if len(syncErrors) > 0 {
			s.updateTask(ctx, task, model.SiteTaskStatusPartial, fmt.Sprintf("site_account:%d", accountID), strings.Join(syncErrors, "; "))
			return
		}
		s.updateTask(ctx, task, model.SiteTaskStatusSuccess, fmt.Sprintf("site_account:%d", accountID), "")
		return
	}

	callCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	snapshot, err := adapter.RefreshAccount(callCtx, provider.RefreshAccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds})
	if provider.ErrorCode(err) == provider.CodeExpired {
		if refreshed, refreshErr := s.refreshExpiredCredentials(ctx, account, site, adapter, creds); refreshErr == nil {
			creds = refreshed
			retryCtx, retryCancel := context.WithTimeout(ctx, 35*time.Second)
			snapshot, err = adapter.RefreshAccount(retryCtx, provider.RefreshAccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds})
			retryCancel()
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
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "", provider.ErrorCode(err))
		return
	}
	account.LastRefreshStatus, account.Status, account.LastError, account.ConsecutiveFailures = "success", model.SiteAccountStatusHealthy, "", 0
	if snapshot.Balance != nil {
		account.Balance = snapshot.Balance
		if strings.TrimSpace(snapshot.Currency) != "" {
			account.BalanceCurrency = snapshot.Currency
		}
		account.BalanceUpdatedAt = now
	}
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
	keyCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	keys, err := keyProvider.ListRoutingKeys(keyCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds})
	cancel()
	if provider.ErrorCode(err) == provider.CodeExpired {
		if refreshed, refreshErr := s.refreshExpiredCredentials(ctx, account, site, adapter, creds); refreshErr == nil {
			creds = refreshed
			retryCtx, retryCancel := context.WithTimeout(ctx, 20*time.Second)
			keys, err = keyProvider.ListRoutingKeys(retryCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds})
			retryCancel()
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
		return nil, &provider.Error{Code: provider.CodeRoutingKeyUnavailable, Message: "no enabled routing API key found"}
	}
	return filtered, nil
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
	var endpointSignature string
	endpointSnapshotKnown := false
	endpointVaries := false
	for index, item := range keys {
		names := normalizedModelNames(item.Models)
		snapshotNames[index] = names
		modelCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		items, err := adapter.ListModels(modelCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: provider.Credentials{APIKey: strings.TrimSpace(item.Key)}})
		cancel()
		if err != nil {
			keyErrors[index] = err
			continue
		}
		names = modelSnapshotNames(items)
		keyNames[index] = names
		if len(names) == 0 {
			continue
		}
		signature := modelNamesSignature(names)
		if !endpointSnapshotKnown {
			endpointSignature = signature
			endpointSnapshotKnown = true
		} else if signature != endpointSignature {
			endpointVaries = true
		}
	}
	for index, item := range keys {
		projectionKey := stableProjectionKey(item, index)
		// Always ask the upstream model endpoint with this exact routing key.
		// Some management APIs return a shared model list on every key, which
		// would otherwise leak one group's models into all projected channels.
		names := keyNames[index]
		// If the per-key endpoint genuinely varies, it is the most precise
		// source (the endpoint may apply hidden provider-side policy). When all
		// keys return the same list, treat explicit management-side model limits
		// as authoritative; otherwise a shared unscoped /models response leaks
		// every model into every group.
		if !endpointVaries && len(snapshotNames[index]) > 0 {
			names = snapshotNames[index]
		}
		if len(names) == 0 && len(snapshotNames[index]) > 0 {
			names = snapshotNames[index]
		}
		if len(names) == 0 && len(keys) == 1 {
			// A single-key deployment can safely use the snapshot returned by
			// the management endpoint, or the management credential itself when
			// the key endpoint is unavailable.
			names = normalizedModelNames(item.Models)
			if len(names) == 0 {
				modelCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				items, err := adapter.ListModels(modelCtx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: managementCreds})
				cancel()
				if err == nil {
					names = modelSnapshotNames(items)
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
		first := unresolved[0]
		detail := routingKeyLabel(first.item, first.projectionKey)
		if first.err != nil {
			return modelsByProjection, siteAccountModelsFromProjection(account.ID, modelsByProjection), &provider.Error{Code: provider.ErrorCode(first.err), Message: fmt.Sprintf("unable to discover models for routing key %s: %s", detail, provider.ErrorMessage(first.err))}
		}
		return modelsByProjection, siteAccountModelsFromProjection(account.ID, modelsByProjection), &provider.Error{Code: provider.CodeUnsupported, Message: fmt.Sprintf("routing key %s returned no models", detail)}
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
	baseURL := routingBaseURL(site.BaseURL)
	sourceHash := model.SiteProjectionSourceHash(baseURL, []string{"openai"}, filtered, creds.APIKey, account.Enabled)
	result, err := s.store.UpsertSiteProjection(ctx, model.SiteProjectionInput{SiteAccountID: account.ID, ProjectionKey: projectionKey, Name: name, BaseURL: baseURL, Protocols: []string{"openai"}, Models: filtered, APIKey: creds.APIKey, SourceHash: sourceHash, Enabled: account.Enabled, Force: force})
	if err != nil {
		return nil, err
	}
	s.projectionChanged()
	return result, nil
}

func routingBaseURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimRight(strings.TrimSpace(raw), "/")
	}
	cleanPath := strings.TrimRight(u.Path, "/")
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
		account.Balance = &value
		if strings.TrimSpace(preSnapshot.Currency) != "" {
			account.BalanceCurrency = strings.TrimSpace(preSnapshot.Currency)
		}
	}
	attempt := &model.CheckinAttempt{RunID: run.ID, SiteAccountID: account.ID, ProviderID: adapter.ID(), LocalDay: day, TriggerScope: triggerScope, Status: "running", AttemptNo: 1, StartedAt: time.Now().UnixMilli(), BalanceBefore: balanceBefore, BalanceCurrency: account.BalanceCurrency}
	attempt, _ = s.store.CreateCheckinAttempt(ctx, attempt)
	var result provider.CheckinResult
	for try := 1; try <= 3; try++ {
		attempt.AttemptNo = try
		result, err = adapter.Checkin(ctx, provider.AccountRequest{BaseURL: site.BaseURL, ProxyURL: siteProxyURL(site), Credentials: creds})
		if err == nil || provider.ErrorCode(err) == provider.CodeBrowserRequired || provider.ErrorCode(err) == provider.CodeUnsupported || provider.ErrorCode(err) == provider.CodeExpired || provider.ErrorCode(err) == provider.CodeUserIDRequired {
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
			if strings.TrimSpace(snapshot.Currency) != "" {
				account.BalanceCurrency = strings.TrimSpace(snapshot.Currency)
			}
			attempt.BalanceCurrency = account.BalanceCurrency
			account.Balance = &after
			account.BalanceUpdatedAt = attempt.FinishedAt
			account.LastRefreshAt = attempt.FinishedAt
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
	default:
		run.Status = model.SiteTaskStatusFailed
		run.FailedCount = 1
		account.LastCheckinStatus = result.Status
	}
	_ = s.store.UpdateCheckinAttempt(ctx, attempt)
	_ = s.store.UpdateCheckinRun(ctx, run)
	_, _ = s.store.UpdateSiteAccount(ctx, account.ID, account)
	if result.Status == provider.CheckinSuccess && attempt.BalanceAfter != nil {
		s.evaluateLowBalance(account, site)
	}
	if result.Status == provider.CheckinFailed {
		s.notifyCheckinFailure(account, site, day, attempt.ErrorCode)
	}
	if err != nil {
		s.updateTask(ctx, task, model.SiteTaskStatusFailed, "checkin_run:"+fmt.Sprint(run.ID), provider.ErrorCode(err))
		return
	}
	s.updateTask(ctx, task, model.SiteTaskStatusSuccess, "checkin_run:"+fmt.Sprint(run.ID), "")
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
		out = append(out, model.SiteAnnouncement{SiteID: siteID, SourceKey: item.SourceKey, Title: item.Title, ContentMarkdown: item.ContentMarkdown, Level: item.Level, SourceURL: item.SourceURL, UpstreamCreatedAt: item.UpstreamAt, FirstSeenAt: now, LastSeenAt: now, ContentHash: item.ContentHash})
	}
	return s.store.UpsertSiteAnnouncements(ctx, out)
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
		if req.Enabled != nil {
			site.Enabled = *req.Enabled
		}
		out, err := s.store.UpdateSite(ctx, id, site)
		if err != nil {
			RespondError(c, 400, err)
			return
		}
		s.projectionChanged()
		RespondJSON(c, 200, out)
	case http.MethodDelete:
		if err := s.store.DeleteSite(ctx, id); err != nil {
			RespondError(c, 500, err)
			return
		}
		s.projectionChanged()
		RespondJSON(c, 200, gin.H{"id": id, "deleted": true})
	}
}
