package app

import (
	"context"
	"slices"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

type browserImportAccountCandidate struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
	Match string `json:"match"`
}

type browserImportCandidate struct {
	Index             int                             `json:"index"`
	SourceOrigin      string                          `json:"source_origin"`
	BaseURL           string                          `json:"base_url"`
	SiteName          string                          `json:"site_name"`
	Platform          string                          `json:"platform"`
	AccountLabel      string                          `json:"account_label"`
	CredentialType    string                          `json:"credential_type"`
	RefreshConfigured bool                            `json:"refresh_configured"`
	Eligible          bool                            `json:"eligible"`
	Problem           string                          `json:"problem,omitempty"`
	DuplicateOf       *int                            `json:"duplicate_of,omitempty"`
	TargetSiteID      int64                           `json:"target_site_id"`
	Candidates        []browserImportAccountCandidate `json:"candidates"`
}

type browserImportInspection struct {
	Version         int                      `json:"version"`
	Format          string                   `json:"format"`
	CapturedAt      int64                    `json:"captured_at"`
	IgnoredSections []string                 `json:"ignored_sections"`
	Entries         []browserImportCandidate `json:"entries"`
}

type browserImportState struct {
	sites       []*model.Site
	accounts    []*model.SiteAccount
	credentials map[int64]provider.Credentials
	locked      map[int64]bool
}

func (s *Server) loadBrowserImportState(ctx context.Context, store storage.Store, entries []browserImportEntry) (*browserImportState, error) {
	state := &browserImportState{credentials: map[int64]provider.Credentials{}, locked: map[int64]bool{}}
	var err error
	if state.sites, err = store.ListSites(ctx, model.SiteListFilter{IncludeDeleted: true}); err != nil {
		return nil, previewFailure("", "request_failed")
	}
	if state.accounts, err = store.ListSiteAccounts(ctx, 0, false); err != nil {
		return nil, previewFailure("", "request_failed")
	}
	wantedURLs := map[string]bool{}
	for _, entry := range entries {
		if entry.BaseURL != "" && entry.Problem == "" {
			wantedURLs[entry.BaseURL] = true
		}
	}
	wantedSites := map[int64]bool{}
	for _, site := range state.sites {
		base, _ := normalizeBrowserImportURL(site.BaseURL, false)
		if site.DeletedAt == 0 && wantedURLs[base] {
			wantedSites[site.ID] = true
		}
	}
	for _, account := range state.accounts {
		if err = ctx.Err(); err != nil {
			return nil, previewFailure("", "observation_interrupted")
		}
		if !wantedSites[account.SiteID] {
			continue
		}
		var credential provider.Credentials
		if s.siteControl.locked() || s.siteControl.cipher.Open(account.CredentialCiphertext, &credential) != nil {
			state.locked[account.ID] = true
			continue
		}
		state.credentials[account.ID] = credential
	}
	return state, nil
}

func (s *Server) inspectBrowserImport(ctx context.Context, document *browserImportDocument) (*browserImportInspection, error) {
	state, err := s.loadBrowserImportState(ctx, storage.ConfigurationSource(s.store), document.Entries)
	if err != nil {
		return nil, err
	}
	inspection := &browserImportInspection{Version: 1, Format: document.Format, CapturedAt: document.CapturedAt, IgnoredSections: document.IgnoredSections, Entries: []browserImportCandidate{}}
	for index, entry := range document.Entries {
		candidate := browserImportCandidate{Index: index, SourceOrigin: entry.SourceOrigin, BaseURL: entry.BaseURL, SiteName: entry.SiteName, Platform: entry.Platform, AccountLabel: entry.AccountLabel, CredentialType: entry.CredentialType, RefreshConfigured: entry.Credential.RefreshToken != "", Eligible: entry.Problem == "", Problem: entry.Problem, DuplicateOf: entry.DuplicateOf, Candidates: []browserImportAccountCandidate{}}
		if candidate.Eligible {
			matches := state.sitesFor(entry.BaseURL)
			if len(matches) > 1 {
				candidate.Eligible, candidate.Problem = false, "ambiguous_identity"
			} else if len(matches) == 1 {
				candidate.TargetSiteID = matches[0].ID
				for _, account := range state.accounts {
					if account.SiteID != matches[0].ID {
						continue
					}
					match := "same_site"
					old, known := state.credentials[account.ID]
					switch {
					case known && account.CredentialType == entry.CredentialType && browserImportPrimaryCredential(account.CredentialType, old) == browserImportPrimaryCredential(entry.CredentialType, entry.Credential):
						match = "credential"
					case known && old.UserID > 0 && entry.Credential.UserID == old.UserID && entry.CredentialType != model.CredentialTypeAPIKey:
						match = "user_id"
					case account.Label == entry.AccountLabel:
						match = "label"
					}
					candidate.Candidates = append(candidate.Candidates, browserImportAccountCandidate{ID: account.ID, Label: account.Label, Match: match})
				}
				slices.SortFunc(candidate.Candidates, func(a, b browserImportAccountCandidate) int {
					if a.ID < b.ID {
						return -1
					}
					if a.ID > b.ID {
						return 1
					}
					return 0
				})
			}
		}
		inspection.Entries = append(inspection.Entries, candidate)
	}
	return inspection, nil
}

func (state *browserImportState) sitesFor(baseURL string) []*model.Site {
	result := []*model.Site{}
	for _, site := range state.sites {
		base, err := normalizeBrowserImportURL(site.BaseURL, false)
		if err == nil && site.DeletedAt == 0 && base == baseURL {
			result = append(result, site)
		}
	}
	return result
}
