package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

type browserImportPreviewRequest struct {
	Document        json.RawMessage `json:"document"`
	EntryIndex      int             `json:"entry_index"`
	TargetAccountID int64           `json:"target_account_id"`
	SiteName        string          `json:"site_name"`
	AccountLabel    string          `json:"account_label"`
	Mode            string          `json:"mode"`
	SourceConfirmed bool            `json:"source_confirmed"`
}

type browserImportVerification struct {
	Status              string `json:"status"`
	Scope               string `json:"scope"`
	CheckedAt           int64  `json:"checked_at"`
	IdentityKnown       bool   `json:"identity_known"`
	UserID              int64  `json:"user_id,omitempty"`
	Username            string `json:"username,omitempty"`
	ModelCount          int    `json:"model_count"`
	ModelCountKnown     bool   `json:"model_count_known"`
	RoutingKeyAvailable bool   `json:"routing_key_available"`
}

type browserImportPreviewContext struct {
	Format             string                    `json:"format"`
	SourceOrigin       string                    `json:"source_origin"`
	BaseURL            string                    `json:"base_url"`
	SiteName           string                    `json:"site_name"`
	AccountLabel       string                    `json:"account_label"`
	CredentialType     string                    `json:"credential_type"`
	CapturedAt         int64                     `json:"captured_at"`
	TargetSiteID       int64                     `json:"target_site_id"`
	TargetAccountID    int64                     `json:"target_account_id"`
	TargetSiteName     string                    `json:"target_site_name"`
	TargetAccountLabel string                    `json:"target_account_label"`
	Mode               string                    `json:"mode"`
	Verification       browserImportVerification `json:"verification"`
}

type browserImportApplyResult struct {
	SiteID    int64  `json:"site_id"`
	AccountID int64  `json:"account_id"`
	Action    string `json:"action"`
	Verified  bool   `json:"verified"`
}

type browserImportPlan struct {
	source          browserImportEntry // No credential is retained in source.
	site            *model.Site
	targetAccountID int64
	siteName        string
	accountLabel    string
	credentialType  string
	credential      provider.Credentials
	fingerprint     string
	action          string
	items           []configPreviewItem
	metadata        browserImportPreviewContext
}

func (s *Server) previewBrowserImport(ctx context.Context, request browserImportPreviewRequest) (*configPreview, error) {
	if !request.SourceConfirmed {
		return nil, previewFailure("", "source_confirmation_required")
	}
	if request.Mode != "verify" && request.Mode != "save_disabled" || request.EntryIndex < 0 || request.TargetAccountID < 0 {
		return nil, previewFailure("", "invalid_request")
	}
	document, err := decodeBrowserImportDocument(request.Document)
	if err != nil {
		return nil, err
	}
	if request.EntryIndex >= len(document.Entries) {
		return nil, previewFailure("", "invalid_request")
	}
	entry := document.Entries[request.EntryIndex]
	if entry.Problem != "" {
		return nil, previewFailure(fmt.Sprintf("accounts[%d]", request.EntryIndex), entry.Problem)
	}
	if s.siteControl.locked() {
		return nil, previewFailure("", "credential_locked")
	}
	if err = s.browserImportPending(entry.BaseURL, ""); err != nil {
		return nil, err
	}
	state, err := s.loadBrowserImportState(ctx, storage.ConfigurationSource(s.store), []browserImportEntry{entry})
	if err != nil {
		return nil, err
	}
	plan, err := s.planBrowserImport(state, entry, request, document)
	if err != nil {
		return nil, err
	}
	if plan.action != "conflict" && request.Mode == "verify" {
		verificationCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		if s.baseCtx != nil {
			stopServerCancel := context.AfterFunc(s.baseCtx, cancel)
			defer stopServerCancel()
		}
		if err = s.verifyBrowserImportPlan(verificationCtx, plan); err != nil {
			return nil, err
		}
		current, err := s.loadBrowserImportState(verificationCtx, storage.ConfigurationSource(s.store), []browserImportEntry{plan.source})
		if err != nil {
			return nil, err
		}
		if browserImportFingerprint(current, plan.source.BaseURL, plan.siteName) != plan.fingerprint {
			return nil, previewFailure("", "preview_stale")
		}
		if err = plan.checkVerifiedIdentity(current); err != nil {
			return nil, err
		}
		if err = plan.setVerifiedCredentialAction(current, request.TargetAccountID > 0); err != nil {
			return nil, err
		}
		if verificationCtx.Err() != nil {
			return nil, browserImportVerificationFailure(verificationCtx.Err())
		}
	}
	if ctx.Err() != nil {
		return nil, previewFailure("", "cancelled")
	}
	// The only secret capture is the bounded, prepared credential. Neither the
	// complete external backup nor other accounts' decrypted values survive.
	raw, err := json.Marshal(struct {
		Source     browserImportEntry
		Credential provider.Credentials
		Metadata   browserImportPreviewContext
	}{plan.source, plan.credential, plan.metadata})
	if err != nil {
		return nil, previewFailure("", "invalid_preview")
	}
	return s.newConfigPreviewWithBrowserImport(len(raw), plan.items, &plan.metadata, func(applyCtx context.Context) (*configPreviewResult, error) {
		return s.applyBrowserImportPlan(applyCtx, *plan)
	})
}

func (s *Server) planBrowserImport(state *browserImportState, entry browserImportEntry, request browserImportPreviewRequest, document *browserImportDocument) (*browserImportPlan, error) {
	siteName, err := browserImportTargetName(request.SiteName, entry.SiteName)
	if err != nil {
		return nil, err
	}
	accountLabel, err := browserImportTargetName(request.AccountLabel, entry.AccountLabel)
	if err != nil {
		return nil, err
	}
	plan := &browserImportPlan{source: entry, siteName: siteName, accountLabel: accountLabel, credentialType: entry.CredentialType, credential: entry.Credential, action: "create"}
	plan.source.Credential = provider.Credentials{}
	plan.metadata = browserImportPreviewContext{Format: document.Format, SourceOrigin: entry.SourceOrigin, BaseURL: entry.BaseURL, SiteName: entry.SiteName, AccountLabel: entry.AccountLabel, CredentialType: entry.CredentialType, CapturedAt: document.CapturedAt, TargetSiteName: siteName, TargetAccountLabel: accountLabel, Mode: request.Mode, Verification: browserImportVerification{Status: "unverified", Scope: "none"}}
	plan.site = &model.Site{Name: siteName, BaseURL: entry.BaseURL, Platform: entry.Platform, Enabled: true, UseSystemProxy: true, Timezone: "Asia/Shanghai", TagsJSON: "[]", LastProbeStatus: "unknown"}
	matches := state.sitesFor(entry.BaseURL)
	if len(matches) > 1 {
		plan.conflict("ambiguous_identity")
	} else if len(matches) == 1 {
		plan.site = matches[0]
		plan.siteName = plan.site.Name
		plan.metadata.TargetSiteID, plan.metadata.TargetSiteName = plan.site.ID, plan.site.Name
		oldAdapter, oldErr := s.siteControl.adapter(plan.site)
		newAdapter, newErr := s.siteControl.adapter(&model.Site{BaseURL: entry.BaseURL, Platform: entry.Platform})
		if oldErr != nil || newErr != nil || oldAdapter.ID() != newAdapter.ID() {
			plan.conflict("platform_conflict")
		}
	}
	if request.TargetAccountID > 0 {
		var target *model.SiteAccount
		for _, account := range state.accounts {
			if account.ID == request.TargetAccountID {
				target = account
				break
			}
		}
		if target == nil || plan.site.ID == 0 || target.SiteID != plan.site.ID {
			return nil, previewFailure("", "target_site_mismatch")
		}
		if state.locked[target.ID] {
			return nil, previewFailure("", "credential_locked")
		}
		plan.targetAccountID, plan.accountLabel = target.ID, target.Label
		plan.metadata.TargetAccountID, plan.metadata.TargetAccountLabel = target.ID, target.Label
		old := state.credentials[target.ID]
		plan.credential = browserImportRetainOmittedCredential(entry, target.CredentialType, old)
		same := browserImportSameCredential(target.CredentialType, old, plan.credentialType, plan.credential)
		if request.Mode == "save_disabled" && !same {
			return nil, previewFailure("", "unverified_update_forbidden")
		}
		if plan.action != "conflict" {
			plan.action = "update"
			if same {
				plan.action = "unchanged"
			}
		}
	} else if plan.site.ID > 0 && plan.action != "conflict" {
		var exact []*model.SiteAccount
		needsTarget := false
		for _, account := range state.accounts {
			if account.SiteID != plan.site.ID {
				continue
			}
			old, known := state.credentials[account.ID]
			if known && account.CredentialType == entry.CredentialType && browserImportPrimaryCredential(account.CredentialType, old) == browserImportPrimaryCredential(entry.CredentialType, entry.Credential) {
				exact = append(exact, account)
			}
			if account.Label == accountLabel || known && entry.CredentialType != model.CredentialTypeAPIKey && old.UserID > 0 && old.UserID == entry.Credential.UserID {
				needsTarget = true
			}
		}
		switch {
		case len(exact) > 1:
			plan.conflict("ambiguous_identity")
		case len(exact) == 1:
			account := exact[0]
			old := state.credentials[account.ID]
			plan.credential = browserImportRetainOmittedCredential(entry, account.CredentialType, old)
			if !browserImportSameCredential(account.CredentialType, old, plan.credentialType, plan.credential) {
				plan.conflict("target_required")
			} else {
				plan.action, plan.targetAccountID, plan.accountLabel = "unchanged", account.ID, account.Label
				plan.metadata.TargetAccountID, plan.metadata.TargetAccountLabel = account.ID, account.Label
			}
		case needsTarget:
			plan.conflict("target_required")
		}
	}
	if plan.site.ID == 0 {
		for _, existing := range state.sites {
			if existing.DeletedAt == 0 && existing.Name == plan.siteName {
				plan.conflict("site_name_conflict")
				break
			}
		}
	}
	plan.fingerprint = browserImportFingerprint(state, entry.BaseURL, plan.siteName)
	if plan.action != "conflict" {
		plan.setItems()
	}
	return plan, nil
}

func browserImportRetainOmittedCredential(entry browserImportEntry, oldKind string, old provider.Credentials) provider.Credentials {
	credential := entry.Credential
	if entry.CredentialType != model.CredentialTypeAPIKey && !entry.RefreshPresent {
		credential.RefreshToken = old.RefreshToken
	}
	// A prior expiry describes the prior access token. Retain it only when the
	// primary is identical; a replacement token gets its own supplied/JWT expiry.
	if !entry.ExpiryPresent && oldKind == entry.CredentialType && browserImportPrimaryCredential(oldKind, old) == browserImportPrimaryCredential(entry.CredentialType, credential) {
		credential.ExpiresAt = old.ExpiresAt
	}
	return credential
}

func browserImportSameCredential(oldKind string, old provider.Credentials, newKind string, next provider.Credentials) bool {
	return oldKind == newKind && browserImportPrimaryCredential(oldKind, old) == browserImportPrimaryCredential(newKind, next) && old.RefreshToken == next.RefreshToken && old.EffectiveExpiresAt() == next.EffectiveExpiresAt()
}

// Verification can normalize the credential type, expiry, or a profile identity.
// Decide against the final frozen material, never solely the original primary.
func (plan *browserImportPlan) setVerifiedCredentialAction(state *browserImportState, explicitTarget bool) error {
	if plan.action == "conflict" || plan.targetAccountID == 0 {
		return nil
	}
	for _, account := range state.accounts {
		if account.ID != plan.targetAccountID {
			continue
		}
		old, known := state.credentials[account.ID]
		if !known {
			return previewFailure("", "credential_locked")
		}
		same := browserImportSameCredential(account.CredentialType, old, plan.credentialType, plan.credential)
		if plan.metadata.Verification.IdentityKnown && old.UserID != plan.credential.UserID {
			same = false
		}
		if !same && !explicitTarget {
			plan.conflict("target_required")
			return nil
		}
		plan.action = "update"
		if same {
			plan.action = "unchanged"
		}
		plan.setItems()
		return nil
	}
	return previewFailure("", "preview_stale")
}

func browserImportTargetName(value, fallback string) (string, error) {
	if strings.TrimSpace(value) == "" {
		value = fallback
	}
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 191 || strings.ContainsFunc(value, unicode.IsControl) {
		return "", previewFailure("", "invalid_name")
	}
	return value, nil
}

func (plan *browserImportPlan) conflict(code string) {
	plan.action = "conflict"
	plan.items = []configPreviewItem{{Key: "accounts[0]", Entity: "account", Label: plan.accountLabel, Action: "conflict", Message: code}}
}

func (plan *browserImportPlan) setItems() {
	preserved := []string{"enabled", "auto_checkin", "auto_refresh", "timezone", "balance_observations", "checkin_history", "models", "binding", "channels"}
	siteAction := "preserve"
	siteFields := []string{}
	if plan.site.ID == 0 {
		siteAction, siteFields = "create", []string{"name", "base_url", "platform", "enabled", "use_system_proxy", "timezone"}
	}
	plan.items = []configPreviewItem{{Key: "sites[0]", Entity: "site", Label: plan.siteName, Action: siteAction, Fields: siteFields, PreservedFields: []string{"enabled", "proxy_url", "timezone"}}}
	fields, message := []string{}, "browser_import_unchanged"
	switch plan.action {
	case "create":
		fields, message = []string{"label", "credential_type", "credentials", "enabled", "auto_checkin", "auto_refresh"}, "browser_import_create_disabled"
		preserved = []string{}
	case "update":
		fields, message = []string{"credential_type", "credentials", "runtime_state"}, "browser_import_update"
	}
	plan.items = append(plan.items, configPreviewItem{Key: "accounts[0]", Entity: "account", Label: plan.accountLabel, Action: plan.action, Fields: fields, PreservedFields: preserved, Message: message})
}

func (plan *browserImportPlan) checkVerifiedIdentity(state *browserImportState) error {
	verification := plan.metadata.Verification
	if !verification.IdentityKnown {
		return nil
	}
	for _, account := range state.accounts {
		if account.SiteID != plan.site.ID || account.CredentialType == model.CredentialTypeAPIKey {
			continue
		}
		old, known := state.credentials[account.ID]
		if !known || old.UserID <= 0 {
			continue
		}
		if account.ID == plan.targetAccountID && old.UserID != verification.UserID {
			return previewFailure("", "identity_conflict")
		}
		if account.ID != plan.targetAccountID && old.UserID == verification.UserID {
			plan.conflict("target_required")
		}
	}
	return nil
}

// Include matching identities and conflicting site names, but exclude changing
// observations. A fresh balance/check-in must survive a later credential apply.
func browserImportFingerprint(state *browserImportState, baseURL, siteName string) string {
	type row struct {
		ID     int64
		Fields map[string]any
	}
	sites, accounts := []row{}, []row{}
	matching := map[int64]bool{}
	for _, site := range state.sites {
		base, _ := normalizeBrowserImportURL(site.BaseURL, false)
		if base != baseURL && site.Name != siteName {
			continue
		}
		matching[site.ID] = base == baseURL
		fields := siteImportFields(site)
		fields["deleted_at"] = site.DeletedAt
		sites = append(sites, row{site.ID, fields})
	}
	for _, account := range state.accounts {
		if matching[account.SiteID] {
			fields := accountImportFields(account)
			fields["credential_ciphertext"], fields["credential_key_version"], fields["deleted_at"] = account.CredentialCiphertext, account.CredentialKeyVersion, account.DeletedAt
			accounts = append(accounts, row{account.ID, fields})
		}
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].ID < sites[j].ID })
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].ID < accounts[j].ID })
	raw, _ := json.Marshal(struct{ Sites, Accounts []row }{sites, accounts})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (s *Server) applyBrowserImportPlan(ctx context.Context, plan browserImportPlan) (*configPreviewResult, error) {
	result := &browserImportApplyResult{Action: plan.action, Verified: plan.metadata.Verification.Status == "verified"}
	err := storage.WithConfigurationTransaction(ctx, s.store, func(txCtx context.Context, txStore storage.Store) error {
		if s.siteControl.locked() {
			return previewFailure("", "credential_locked")
		}
		state, err := s.loadBrowserImportState(txCtx, txStore, []browserImportEntry{plan.source})
		if err != nil {
			return err
		}
		if browserImportFingerprint(state, plan.source.BaseURL, plan.siteName) != plan.fingerprint {
			return previewFailure("", "preview_stale")
		}
		if plan.action == "conflict" {
			return previewFailure("", "preview_conflict")
		}
		siteID := plan.site.ID
		if siteID == 0 {
			site := *plan.site
			created, err := txStore.CreateSite(txCtx, &site)
			if err != nil {
				return previewFailure("sites[0]", "apply_failed")
			}
			siteID = created.ID
		}
		result.SiteID, result.AccountID = siteID, plan.targetAccountID
		if plan.action == "unchanged" {
			return nil
		}
		sealed, err := s.siteControl.cipher.Seal(plan.credential)
		if err != nil {
			return previewFailure("", "credential_locked")
		}
		if plan.action == "create" {
			account, err := txStore.CreateSiteAccount(txCtx, &model.SiteAccount{SiteID: siteID, Label: plan.accountLabel, CredentialType: plan.credentialType, CredentialCiphertext: sealed, CredentialKeyVersion: s.siteControl.cipher.Version(), Enabled: false, AutoCheckin: false, AutoRefresh: false, Timezone: "Asia/Shanghai", Status: model.SiteAccountStatusUnknown, LastRefreshStatus: "unknown", LastCheckinStatus: "unknown"})
			if err != nil {
				return previewFailure("accounts[0]", "apply_failed")
			}
			result.AccountID = account.ID
			return nil
		}
		// A dedicated narrow write avoids the generic account toggle's channel
		// cascade and never replays old balance or check-in observations.
		if err = txStore.RepairSiteAccountCredential(txCtx, plan.targetAccountID, plan.credentialType, sealed, s.siteControl.cipher.Version()); err != nil {
			return previewFailure("accounts[0]", "apply_failed")
		}
		return nil
	})
	if err != nil && !storage.IsConfigurationCacheDegraded(err) {
		return nil, err
	}
	if plan.action != "unchanged" {
		s.siteControl.pricingSourceChanged(result.SiteID)
	}
	view := &configPreviewResult{Items: plan.items, BrowserImport: result}
	if storage.IsConfigurationCacheDegraded(err) {
		view.Warning = "cache_sync_degraded"
	}
	return view, nil
}
