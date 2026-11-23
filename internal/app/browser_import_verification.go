package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

func (s *Server) verifyBrowserImportPlan(ctx context.Context, plan *browserImportPlan) error {
	if plan.targetAccountID > 0 {
		key := fmt.Sprintf("site:%d:account:%d:browser_import", plan.site.ID, plan.targetAccountID)
		owner, now := newSiteTaskID(), time.Now()
		acquired, err := s.siteControl.store.AcquireSiteTaskLease(ctx, key, owner, now.UnixMilli(), now.Add(2*time.Minute).UnixMilli())
		if err != nil {
			return previewFailure("", "request_failed")
		}
		if !acquired {
			return previewFailure("", "conflict")
		}
		defer func() {
			releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			_ = s.siteControl.store.ReleaseSiteTaskLease(releaseCtx, key, owner)
		}()
	}
	runCtx, finish, err := s.siteControl.acquireManualSiteTask(ctx, plan.site.ID)
	if err != nil {
		return browserImportVerificationFailure(err)
	}
	defer finish()
	current, err := s.loadBrowserImportState(runCtx, storage.ConfigurationSource(s.store), []browserImportEntry{plan.source})
	if err != nil {
		return err
	}
	if browserImportFingerprint(current, plan.source.BaseURL, plan.siteName) != plan.fingerprint {
		return previewFailure("", "preview_stale")
	}
	adapter, err := s.siteControl.adapter(plan.site)
	if err != nil {
		return previewFailure("", "unsupported_platform")
	}
	if capabilities := adapter.Capabilities(); len(capabilities.CredentialTypes) > 0 && !containsCredentialType(capabilities.CredentialTypes, plan.credentialType) {
		return previewFailure("", "unsupported_credential_type")
	}
	credentials := plan.credential
	refreshToken := credentials.RefreshToken
	// Full prepareAccountCredential also rotates sessions and can reveal
	// masked routing keys through POST. Neither belongs in import verification.
	credentials.RefreshToken, credentials.Password = "", ""
	if plan.credentialType != model.CredentialTypeAPIKey {
		credentials.APIKey = ""
		if resolver, ok := adapter.(provider.ManagementCredentialResolver); ok {
			resolveCtx, cancel := context.WithTimeout(runCtx, 25*time.Second)
			credentials, err = resolver.ResolveManagementCredentials(resolveCtx, provider.AccountRequest{BaseURL: plan.site.BaseURL, ProxyURL: siteProxyURL(plan.site), Credentials: credentials})
			cancel()
			if err != nil {
				return browserImportVerificationFailure(err)
			}
		}
		credentials.RefreshToken, credentials.Password, credentials.APIKey = "", "", ""
		profileCtx, cancel := context.WithTimeout(runCtx, 45*time.Second)
		snapshot, err := adapter.RefreshAccount(profileCtx, provider.AccountRequest{BaseURL: plan.site.BaseURL, ProxyURL: siteProxyURL(plan.site), Credentials: credentials})
		cancel()
		if err != nil {
			return browserImportVerificationFailure(err)
		}
		plan.metadata.Verification = browserImportVerification{Status: "verified", Scope: "account", CheckedAt: time.Now().UnixMilli(), IdentityKnown: snapshot.UserID > 0, UserID: snapshot.UserID}
		if snapshot.UserID > 0 {
			credentials.UserID = snapshot.UserID
		}
		if utf8.ValidString(snapshot.Username) && utf8.RuneCountInString(snapshot.Username) <= 191 && !strings.ContainsFunc(snapshot.Username, unicode.IsControl) {
			plan.metadata.Verification.Username = snapshot.Username
		}
		if plan.credentialType == model.CredentialTypeCookie && credentials.Cookie == "" && credentials.AccessToken != "" {
			plan.credentialType = model.CredentialTypeAccessToken
		}
	} else {
		credentials.UserID = 0
		modelsCtx, cancel := context.WithTimeout(runCtx, 45*time.Second)
		models, err := adapter.ListModels(modelsCtx, provider.AccountRequest{BaseURL: plan.site.BaseURL, ProxyURL: siteProxyURL(plan.site), Credentials: credentials})
		cancel()
		if err != nil {
			return browserImportVerificationFailure(err)
		}
		plan.metadata.Verification = browserImportVerification{Status: "verified", Scope: "models", CheckedAt: time.Now().UnixMilli(), ModelCount: len(models), ModelCountKnown: true, RoutingKeyAvailable: true}
	}
	if runCtx.Err() != nil {
		return browserImportVerificationFailure(runCtx.Err())
	}
	credentials.RefreshToken, credentials.Password = refreshToken, ""
	if credentials.ExpiresAt == 0 {
		credentials.ExpiresAt = credentials.EffectiveExpiresAt()
	}
	plan.credential = credentials
	return nil
}

func browserImportVerificationFailure(err error) error {
	if errors.Is(err, context.Canceled) {
		return previewFailure("", "cancelled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return previewFailure("", provider.CodeTimeout)
	}
	return previewFailure("", safeSiteSyncErrorCode(err))
}
