package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/yzgolden86/PivotFlow/internal/cooldown"
	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/storage"
)

// A plan contains only deterministic local writes. Discovery and network calls
// are complete before it is created; the transaction rechecks its snapshot.
type backupPlan struct {
	items                           []configPreviewItem
	fingerprint                     string
	steps                           []backupPlanStep
	siteIDs, accountIDs, channelIDs map[int64]int64
	disabledChannels                map[int64]bool
	siteEnabled, accountAvailable   map[int64]bool
	channelEnabled                  map[int64]bool
}
type backupPlanStep struct {
	key string
	run func(context.Context, storage.Store, *backupImportResult) error
}
type backupState struct {
	sites    []*model.Site
	accounts []*model.SiteAccount
	channels []*model.Config
	keys     map[int64][]*model.APIKey
	tokens   []*model.AuthToken
	settings []*model.SystemSetting
	bindings []*model.SiteChannelBinding
	webhook  *model.WebhookConfig
	target   *model.BackupConfig
	models   map[int64][]model.SiteAccountModel
}

func (s *Server) previewBackupDocument(ctx context.Context, document *backupDocument) (*configPreview, error) {
	// Freeze caller-owned objects as well as HTTP documents. It is impossible
	// to substitute another import by mutating the original after preview.
	raw, err := json.Marshal(document)
	if err != nil || len(raw) > backupMaxImportBytes {
		return nil, previewFailure("", "invalid_backup_document")
	}
	var frozen backupDocument
	if json.Unmarshal(raw, &frozen) != nil {
		return nil, previewFailure("", "invalid_backup_document")
	}
	plan, err := s.planBackup(ctx, storage.ConfigurationSource(s.store), &frozen)
	if err != nil {
		return nil, err
	}
	// The initial write closures are discarded after planning. Retain only the
	// frozen input, safe items and fingerprint; application replans in its tx.
	fingerprint, items := plan.fingerprint, plan.items
	return s.newConfigPreview("backup", len(raw)+len(fingerprint), items, func(applyCtx context.Context) (*configPreviewResult, error) {
		var result backupImportResult
		err := storage.WithConfigurationTransaction(applyCtx, s.store, func(txCtx context.Context, txStore storage.Store) error {
			current, loadErr := s.planBackup(txCtx, txStore, &frozen)
			if loadErr != nil {
				return loadErr
			}
			if current.fingerprint != fingerprint {
				return previewFailure("", "preview_stale")
			}
			for _, item := range current.items {
				if item.Action == "conflict" {
					return previewFailure(item.Key, "preview_conflict")
				}
			}
			result = backupImportResult{}
			for _, step := range current.steps {
				if runErr := step.run(txCtx, txStore, &result); runErr != nil {
					return previewFailure(step.key, "apply_failed")
				}
			}
			return nil
		})
		warning := ""
		if storage.IsConfigurationCacheDegraded(err) {
			warning, err = "cache_sync_degraded", nil
		}
		if err != nil {
			return nil, err
		}
		s.InvalidateChannelListCache()
		s.InvalidateAllAPIKeysCache()
		if s.authService != nil {
			if reloadErr := s.authService.ReloadAuthTokens(); reloadErr != nil {
				// Configuration already committed. A runtime reload failure must
				// remain visible without returning raw storage errors or retrying.
				if warning == "cache_sync_degraded" {
					warning = "cache_sync_degraded_token_reload_failed"
				} else {
					warning = "token_reload_failed"
				}
				log.Print("[WARN] 配置已提交，但访问令牌权限重载失败；需核对运行状态")
			}
		}
		return &configPreviewResult{Items: items, Backup: &result, Warning: warning}, nil
	})
}

func (s *Server) readBackupState(ctx context.Context, store storage.Store, connections, settings bool) (*backupState, error) {
	state := &backupState{models: map[int64][]model.SiteAccountModel{}}
	var err error
	if connections {
		if state.sites, err = store.ListSites(ctx, model.SiteListFilter{}); err != nil {
			return nil, err
		}
		if state.accounts, err = store.ListSiteAccounts(ctx, 0, false); err != nil {
			return nil, err
		}
		if state.channels, err = store.ListConfigs(ctx); err != nil {
			return nil, err
		}
		if state.keys, err = store.GetAllAPIKeys(ctx); err != nil {
			return nil, err
		}
		if state.tokens, err = store.ListAuthTokens(ctx); err != nil {
			return nil, err
		}
		if state.bindings, err = store.ListSiteChannelBindings(ctx); err != nil {
			return nil, err
		}
		for _, a := range state.accounts {
			if a == nil {
				continue
			}
			if state.models[a.ID], err = store.ListSiteAccountModels(ctx, model.SiteModelFilter{SiteAccountID: a.ID, IncludeDisabled: true}); err != nil {
				return nil, err
			}
		}
	}
	if settings {
		if state.settings, err = store.ListAllSettings(ctx); err != nil {
			return nil, err
		}
		if state.webhook, err = store.GetWebhookConfig(ctx); err != nil && err.Error() != "not found" {
			return nil, err
		}
		if state.target, err = store.GetBackupConfig(ctx); err != nil && err.Error() != "not found" {
			return nil, err
		}
	}
	return state, nil
}

func (b *backupState) fingerprint() string {
	values := map[string]any{}
	for _, v := range b.sites {
		if v != nil {
			values[fmt.Sprintf("site:%d", v.ID)] = siteImportFields(v)
		}
	}
	for _, v := range b.accounts {
		if v != nil {
			// Eligibility controls UpdateSiteAccount's bound-channel cascade.
			// An intervening expiry must not add an unpreviewed deactivation.
			eligible := v.Enabled && v.Status != model.SiteAccountStatusExpired && v.Status != model.SiteAccountStatusDisabled
			values[fmt.Sprintf("account:%d", v.ID)] = []any{accountImportFields(v), v.CredentialCiphertext, v.CredentialKeyVersion, eligible, v.DeletedAt}
		}
	}
	for _, v := range b.channels {
		if v != nil {
			values[fmt.Sprintf("channel:%d", v.ID)] = []any{channelImportFields(v), v.OAuthCredential}
		}
	}
	for id, keys := range b.keys {
		values[fmt.Sprintf("keys:%d", id)] = keyImportFields(keys)
	}
	for _, v := range b.tokens {
		if v != nil {
			values[fmt.Sprintf("token:%d", v.ID)] = []any{tokenImportFields(v), v.Token, v.TokenCiphertext}
		}
	}
	for _, v := range b.settings {
		if v != nil {
			values["setting:"+v.Key] = v.Value
		}
	}
	for _, v := range b.bindings {
		if v != nil {
			values[fmt.Sprintf("binding:%d", v.ID)] = []any{v.SiteAccountID, v.ProjectionKey, v.ChannelID, v.Ownership, v.Status, v.PricingGroup, v.LastProjectedHash}
		}
	}
	for id, v := range b.models {
		values[fmt.Sprintf("models:%d", id)] = modelImportFields(v)
	}
	if v := b.webhook; v != nil {
		copyValue := *v
		copyValue.CreatedAt, copyValue.UpdatedAt, copyValue.LastDeliveryAt = 0, 0, 0
		copyValue.LastDeliveryStatus, copyValue.LastError = "", ""
		values["webhook"] = []any{copyValue, v.URLCiphertext, v.TelegramBotCiphertext, v.TelegramChatCiphertext}
	}
	if v := b.target; v != nil {
		copyValue := *v
		copyValue.CreatedAt, copyValue.UpdatedAt = 0, 0
		clearBackupObservations(&copyValue)
		values["target"] = []any{copyValue, v.PasswordCiphertext}
	}
	raw, _ := json.Marshal(values)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

func importFields(value any, exclude ...string) map[string]any {
	raw, _ := json.Marshal(value)
	result := map[string]any{}
	_ = json.Unmarshal(raw, &result)
	for _, key := range exclude {
		delete(result, key)
	}
	return result
}
func siteImportFields(v *model.Site) map[string]any {
	return importFields(v, "id", "created_at", "updated_at", "deleted_at", "last_probe_status", "last_error", "checkin_method", "checkin_method_checked_at")
}
func accountImportFields(v *model.SiteAccount) map[string]any {
	return map[string]any{"site_id": v.SiteID, "label": v.Label, "credential_type": v.CredentialType, "enabled": v.Enabled, "auto_checkin": v.AutoCheckin, "auto_refresh": v.AutoRefresh, "timezone": v.Timezone}
}
func channelImportFields(v *model.Config) map[string]any {
	return importFields(v, "id", "created_at", "updated_at", "key_count", "cooldown_until", "cooldown_duration_ms")
}
func keyImportFields(keys []*model.APIKey) []map[string]any {
	items := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		if key != nil {
			items = append(items, importFields(key, "id", "channel_id", "created_at", "updated_at", "health", "cooldown_until", "cooldown_duration_ms"))
		}
	}
	return items
}
func modelImportFields(models []model.SiteAccountModel) []map[string]any {
	items := make([]map[string]any, 0, len(models))
	for _, v := range models {
		items = append(items, map[string]any{"model": v.Model, "route_type": v.RouteType, "source": v.Source, "disabled": v.Disabled, "stale": v.Stale})
	}
	slices.SortFunc(items, func(a, b map[string]any) int {
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		return strings.Compare(string(x), string(y))
	})
	return items
}
func tokenImportFields(v *model.AuthToken) map[string]any {
	return map[string]any{"description": v.Description, "expires_at": v.ExpiresAt, "is_active": v.IsActive, "allowed_models": v.AllowedModels, "allowed_channel_ids": v.AllowedChannelIDs, "channel_restriction_mode": v.ChannelRestrictionMode, "cost_limit_usd": v.CostLimitUSD(), "max_concurrency": v.MaxConcurrency, "allowed_protocols": v.AllowedProtocols}
}
func changedImportFields(before, after map[string]any) []string {
	result := []string{}
	keys := make(map[string]struct{}, len(before)+len(after))
	for key := range before {
		keys[key] = struct{}{}
	}
	for key := range after {
		keys[key] = struct{}{}
	}
	for key := range keys {
		value := after[key]
		a, _ := json.Marshal(value)
		b, _ := json.Marshal(before[key])
		if string(a) != string(b) {
			result = append(result, key)
		}
	}
	slices.Sort(result)
	return result
}
func importLabel(label string) string {
	runes := []rune(strings.Map(func(r rune) rune {
		if r < 32 {
			return -1
		}
		return r
	}, label))
	if len(runes) > 120 {
		runes = runes[:120]
	}
	return string(runes)
}
func normalizedBackupSiteURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}
func (p *backupPlan) add(key, entity, label string, before, after map[string]any, preserved []string, problem string, run func(context.Context, storage.Store, *backupImportResult) error) {
	item := configPreviewItem{Key: key, Entity: entity, Label: importLabel(label), Fields: changedImportFields(before, after), PreservedFields: preserved, Action: "update"}
	if before == nil {
		item.Action = "create"
	} else if len(item.Fields) == 0 {
		item.Action = "unchanged"
	}
	if problem != "" {
		item.Action, item.Message = "conflict", problem
	}
	p.items = append(p.items, item)
	if problem == "" && item.Action != "unchanged" && run != nil {
		p.steps = append(p.steps, backupPlanStep{key: key, run: run})
	}
}

func (s *Server) planBackup(ctx context.Context, store storage.Store, document *backupDocument) (*backupPlan, error) {
	if document == nil || document.Version != backupSchemaVersion {
		return nil, previewFailure("", "unsupported_backup_version")
	}
	typ, err := normalizeBackupType(document.Type)
	if err != nil {
		return nil, previewFailure("", "invalid_backup_type")
	}
	connections := typ == "all" || typ == "connections"
	settings := typ == "all" || typ == "settings"
	if connections && document.Connections == nil {
		return nil, previewFailure("connections", "invalid_backup_document")
	}
	if (connections || document.Notification != nil || document.BackupTarget != nil) && (s.siteControl == nil || s.siteControl.cipher == nil) {
		return nil, previewFailure("", "credential_locked")
	}
	state, err := s.readBackupState(ctx, store, connections, settings)
	if err != nil {
		return nil, previewFailure("", "snapshot_failed")
	}
	p := &backupPlan{fingerprint: state.fingerprint(), siteIDs: map[int64]int64{}, accountIDs: map[int64]int64{}, channelIDs: map[int64]int64{}, disabledChannels: map[int64]bool{}, siteEnabled: map[int64]bool{}, accountAvailable: map[int64]bool{}, channelEnabled: map[int64]bool{}}
	if connections {
		s.planBackupConnections(p, state, document.Connections)
	}
	if settings {
		s.planBackupSettings(p, state, document)
	}
	return p, nil
}

func (s *Server) planBackupConnections(p *backupPlan, state *backupState, input *backupConnections) {
	sitesByURL := map[string][]*model.Site{}
	sitesByName := map[string]*model.Site{}
	for _, v := range state.sites {
		if v != nil {
			u := normalizedBackupSiteURL(v.BaseURL)
			sitesByURL[u] = append(sitesByURL[u], v)
			sitesByName[v.Name] = v
		}
	}
	seenIDs := map[int64]bool{}
	seenNames := map[string]bool{}
	seenURLs := map[string]bool{}
	for index, item := range input.Sites {
		key := fmt.Sprintf("sites[%d]", index)
		if item == nil {
			p.add(key, "site", "", nil, nil, nil, "invalid_entry", nil)
			continue
		}
		v := *item
		v.Name = strings.TrimSpace(v.Name)
		v.BaseURL = normalizedBackupSiteURL(v.BaseURL)
		if v.Timezone == "" {
			v.Timezone = "Asia/Shanghai"
		}
		if v.TagsJSON == "" {
			v.TagsJSON = "[]"
		}
		if v.Platform == "" {
			v.Platform = model.SitePlatformUnknown
		}
		problem := ""
		var existing *model.Site
		if v.ID <= 0 || v.Name == "" || v.BaseURL == "" || !json.Valid([]byte(v.TagsJSON)) {
			problem = "invalid_site"
		}
		if seenIDs[v.ID] || seenNames[v.Name] || seenURLs[v.BaseURL] {
			problem = "duplicate_identity"
		}
		seenIDs[v.ID], seenNames[v.Name], seenURLs[v.BaseURL] = true, true, true
		matches := sitesByURL[v.BaseURL]
		if len(matches) > 1 {
			problem = "ambiguous_identity"
		} else if len(matches) == 1 {
			existing = matches[0]
		}
		if byName := sitesByName[v.Name]; byName != nil && (existing == nil || existing.ID != byName.ID) {
			problem = "site_name_conflict"
		}
		var before map[string]any
		if existing != nil {
			before = siteImportFields(existing)
			p.siteIDs[v.ID] = existing.ID
			v.LastProbeStatus, v.LastError, v.CheckinMethod, v.CheckinMethodCheckedAt = existing.LastProbeStatus, existing.LastError, existing.CheckinMethod, existing.CheckinMethodCheckedAt
		} else {
			p.siteIDs[v.ID] = 0
			v.LastProbeStatus, v.LastError, v.CheckinMethod, v.CheckinMethodCheckedAt = "unknown", "", "", 0
		}
		oldID := v.ID
		p.siteEnabled[oldID] = v.Enabled
		targetID := p.siteIDs[oldID]
		p.add(key, "site", v.Name, before, siteImportFields(&v), []string{"runtime_state", "historical_statistics"}, problem, func(ctx context.Context, st storage.Store, r *backupImportResult) error {
			copyValue := v
			copyValue.ID, copyValue.DeletedAt = targetID, 0
			var saved *model.Site
			var err error
			if targetID > 0 {
				saved, err = st.UpdateSite(ctx, targetID, &copyValue)
			} else {
				saved, err = st.CreateSite(ctx, &copyValue)
			}
			if err == nil {
				p.siteIDs[oldID] = saved.ID
				r.Sites++
			}
			return err
		})
	}
	seenIDs = map[int64]bool{}
	seenNames = map[string]bool{}
	for index, item := range input.Accounts {
		key := fmt.Sprintf("accounts[%d]", index)
		if item.Account == nil {
			p.add(key, "account", "", nil, nil, nil, "invalid_entry", nil)
			continue
		}
		v := *item.Account
		v.Label = strings.TrimSpace(v.Label)
		oldID, oldSite := v.ID, v.SiteID
		siteID, hasSite := p.siteIDs[oldSite]
		problem := ""
		identity := fmt.Sprintf("%d:%s", oldSite, v.Label)
		if v.ID <= 0 || v.Label == "" || !hasSite {
			problem = "missing_reference"
		}
		if seenIDs[v.ID] || seenNames[identity] {
			problem = "duplicate_identity"
		}
		seenIDs[v.ID], seenNames[identity] = true, true
		creds := item.Credentials
		if v.CredentialType != model.CredentialTypeAPIKey && v.CredentialType != model.CredentialTypeAccessToken && v.CredentialType != model.CredentialTypeCookie {
			problem = "unsupported_credential_type"
		}
		if creds.Token() == "" && creds.Cookie == "" {
			problem = "credential_required"
		}
		var existing *model.SiteAccount
		for _, a := range state.accounts {
			if a != nil && siteID > 0 && a.SiteID == siteID && a.Label == v.Label {
				if existing != nil {
					problem = "ambiguous_identity"
				}
				existing = a
			}
		}
		v.SiteID = siteID
		var before map[string]any
		if existing != nil {
			before = accountImportFields(existing)
			var oldCreds provider.Credentials
			if s.siteControl.cipher.Open(existing.CredentialCiphertext, &oldCreds) != nil {
				problem = "credential_locked"
			}
			before["credentials"] = oldCreds
			p.accountIDs[oldID] = existing.ID
			// Keep current observations; importing configuration is not a new balance observation.
			next := *existing
			next.Label, next.SiteID, next.CredentialType, next.Enabled, next.AutoCheckin, next.AutoRefresh, next.Timezone = v.Label, v.SiteID, v.CredentialType, v.Enabled, v.AutoCheckin, v.AutoRefresh, v.Timezone
			v = next
		} else {
			p.accountIDs[oldID] = 0
			v.Status, v.LastRefreshStatus, v.LastCheckinStatus = "unknown", "unknown", "unknown"
			v.Balance = nil
			v.BalanceUpdatedAt, v.LastRefreshAt, v.LastCheckinAt = 0, 0, 0
			v.ConsecutiveFailures = 0
			v.LastError = ""
			if v.BalanceCurrency == "" {
				v.BalanceCurrency = "USD"
			}
		}
		after := accountImportFields(&v)
		p.accountAvailable[oldID] = p.siteEnabled[oldSite] && v.Enabled && v.Status != model.SiteAccountStatusExpired && v.Status != model.SiteAccountStatusDisabled
		after["credentials"] = creds
		targetID := p.accountIDs[oldID]
		if existing != nil && (!v.Enabled || v.Status == model.SiteAccountStatusExpired || v.Status == model.SiteAccountStatusDisabled) && len(changedImportFields(before, after)) > 0 {
			for _, binding := range state.bindings {
				if binding == nil || binding.SiteAccountID != targetID {
					continue
				}
				for _, channel := range state.channels {
					if channel != nil && channel.ID == binding.ChannelID && channel.Enabled && !p.disabledChannels[channel.ID] {
						p.disabledChannels[channel.ID] = true
						p.items = append(p.items, configPreviewItem{Key: fmt.Sprintf("accounts[%d]/channel:%d", index, channel.ID), Entity: "channel", Label: importLabel(channel.Name), Action: "deactivate", Fields: []string{"enabled"}, PreservedFields: []string{"request_history", "statistics"}, Message: "account_disables_channel"})
					}
				}
			}
		}
		p.add(key, "account", v.Label, before, after, []string{"balance_observations", "checkin_history", "usage_statistics"}, problem, func(ctx context.Context, st storage.Store, r *backupImportResult) error {
			sealed, err := s.siteControl.cipher.Seal(creds)
			if err != nil {
				return err
			}
			next := v
			next.ID, next.SiteID, next.DeletedAt = targetID, p.siteIDs[oldSite], 0
			next.CredentialCiphertext, next.CredentialKeyVersion = sealed, s.siteControl.cipher.Version()
			var saved *model.SiteAccount
			if targetID > 0 {
				saved, err = st.UpdateSiteAccount(ctx, targetID, &next)
				if err == nil {
					err = st.UpdateSiteAccountCredential(ctx, targetID, next.CredentialType, sealed, next.CredentialKeyVersion)
				}
			} else {
				saved, err = st.CreateSiteAccount(ctx, &next)
			}
			if err == nil {
				p.accountIDs[oldID] = saved.ID
				r.Accounts++
			}
			return err
		})
	}
	models := map[int64][]model.SiteAccountModel{}
	// Every exported account owns a complete list, including an empty one.
	for id := range p.accountIDs {
		models[id] = nil
	}
	seenModels := map[string]bool{}
	for index, v := range input.Models {
		identity := fmt.Sprintf("%d:%s", v.SiteAccountID, v.Model)
		_, ok := p.accountIDs[v.SiteAccountID]
		if !ok || strings.TrimSpace(v.Model) == "" || seenModels[identity] {
			p.add(fmt.Sprintf("models[%d]", index), "models", "模型清单", nil, nil, nil, "invalid_model_reference", nil)
			continue
		}
		seenModels[identity] = true
		models[v.SiteAccountID] = append(models[v.SiteAccountID], v)
	}
	modelIDs := make([]int64, 0, len(models))
	for id := range models {
		modelIDs = append(modelIDs, id)
	}
	slices.Sort(modelIDs)
	for _, id := range modelIDs {
		values := models[id]
		targetID := p.accountIDs[id]
		before := map[string]any{"models": modelImportFields(state.models[targetID])}
		after := map[string]any{"models": modelImportFields(values)}
		p.add(fmt.Sprintf("models/account:%d", id), "models", "账号模型清单", before, after, nil, "", func(ctx context.Context, st storage.Store, _ *backupImportResult) error {
			next := append([]model.SiteAccountModel(nil), values...)
			for i := range next {
				next[i].SiteAccountID = p.accountIDs[id]
			}
			return st.RestoreSiteAccountModels(ctx, p.accountIDs[id], next)
		})
	}
	s.planBackupChannels(p, state, input.Channels)
	s.planBackupBindings(p, state, input.Bindings)
	s.planBackupTokens(p, state, input.Tokens)
}

func validBackupChannel(c *model.Config, keys []model.APIKey) bool {
	if strings.TrimSpace(c.Name) == "" || c.URLs.Normalize() != nil || c.NormalizeAvailableTime() != nil || model.NormalizeAuthType(c.AuthType) == "" || model.NormalizeProtocolTransformMode(c.ProtocolTransformMode) == "" || c.RPMLimit < 0 || c.MaxConcurrency < 0 || c.DailyCostLimit < 0 || c.CostMultiplier < 0 {
		return false
	}
	if validateCustomRequestRules(c.CustomRequestRules) != nil || cooldown.NormalizeCooldownDetectionRules(c.CooldownDetectionRules) != nil {
		return false
	}
	if _, err := normalizeChannelProxyURL(c.ProxyURL); err != nil {
		return false
	}
	if c.CustomRequestRules != nil && c.CustomRequestRules.IsEmpty() {
		c.CustomRequestRules = nil
	}
	if c.CooldownDetectionRules != nil && c.CooldownDetectionRules.IsEmpty() {
		c.CooldownDetectionRules = nil
	}
	for _, u := range c.URLs {
		if normalizedBackupSiteURL(u.URL) == "" {
			return false
		}
	}
	seen := map[string]bool{}
	for index := range c.ModelEntries {
		m := &c.ModelEntries[index]
		if m.Validate() != nil || seen[strings.ToLower(m.Model)] {
			return false
		}
		seen[strings.ToLower(m.Model)] = true
		if m.RedirectModel == m.Model {
			m.RedirectModel = ""
		}
	}
	if len(seen) == 0 {
		return false
	}
	if c.ScheduledCheckModel != "" && !seen[strings.ToLower(c.ScheduledCheckModel)] {
		return false
	}
	seenKeys := map[int]bool{}
	for _, k := range keys {
		if k.KeyIndex < 0 || seenKeys[k.KeyIndex] || strings.TrimSpace(k.APIKey) == "" || strings.ContainsAny(k.APIKey, "\x00\r\n") || k.CostMultiplier < 0 || !model.IsValidKeyStrategy(k.KeyStrategy) {
			return false
		}
		for _, m := range k.AllowedModels {
			if strings.TrimSpace(m) == "" || (!seen["*"] && m != "*" && !seen[strings.ToLower(m)]) {
				return false
			}
		}
		seenKeys[k.KeyIndex] = true
	}
	if c.GetAuthType() == model.AuthTypeAPIKey {
		return c.OAuthCredential == ""
	}
	return c.OAuthCredential != "" && len(keys) == 0
}

func (s *Server) planBackupChannels(p *backupPlan, state *backupState, input []backupChannel) {
	byName := map[string]*model.Config{}
	for _, v := range state.channels {
		if v != nil {
			byName[v.Name] = v
		}
	}
	seenIDs := map[int64]bool{}
	seenNames := map[string]bool{}
	for index, item := range input {
		key := fmt.Sprintf("channels[%d]", index)
		if item.Config == nil {
			p.add(key, "channel", "", nil, nil, nil, "invalid_entry", nil)
			continue
		}
		v := item.Config.Clone()
		oldID := v.ID
		v.Name = strings.TrimSpace(v.Name)
		v.OAuthCredential = item.OAuthCredential
		v.AuthType = model.NormalizeAuthType(v.AuthType)
		v.ProtocolTransformMode = model.NormalizeProtocolTransformMode(v.ProtocolTransformMode)
		keys := append([]model.APIKey(nil), item.APIKeys...)
		for i := range keys {
			if keys[i].KeyStrategy == "" {
				keys[i].KeyStrategy = model.KeyStrategySequential
			}
			if !keys[i].CostMultiplierSet && keys[i].CostMultiplier == 0 {
				keys[i].CostMultiplier = v.CostMultiplier
			}
		}
		problem := ""
		if oldID <= 0 || v.AuthType == "" || v.ProtocolTransformMode == "" || !validBackupChannel(v, keys) {
			problem = "invalid_channel"
		}
		if seenIDs[oldID] || seenNames[v.Name] {
			problem = "duplicate_identity"
		}
		seenIDs[oldID], seenNames[v.Name] = true, true
		var before map[string]any
		targetID := int64(0)
		if existing := byName[v.Name]; existing != nil {
			targetID = existing.ID
			before = channelImportFields(existing)
			before["oauth_credential"] = existing.OAuthCredential
			before["api_keys"] = keyImportFields(state.keys[targetID])
			if existing.GetAuthType() != v.GetAuthType() {
				problem = "auth_type_conflict"
			}
		}
		if p.disabledChannels[targetID] {
			v.Enabled = false
		}
		p.channelIDs[oldID] = targetID
		p.channelEnabled[oldID] = v.Enabled
		keyPointers := make([]*model.APIKey, 0, len(keys))
		for i := range keys {
			keyPointers = append(keyPointers, &keys[i])
		}
		after := channelImportFields(v)
		after["oauth_credential"] = v.OAuthCredential
		after["api_keys"] = keyImportFields(keyPointers)
		if len(changedImportFields(before, after)) > 0 {
			for _, binding := range state.bindings {
				if binding != nil && targetID > 0 && binding.ChannelID == targetID && binding.Ownership == "projected" {
					before["ownership"] = "projected"
					after["ownership"] = "manual"
				}
			}
		}
		p.add(key, "channel", v.Name, before, after, []string{"request_history", "statistics"}, problem, func(ctx context.Context, st storage.Store, r *backupImportResult) error {
			next := v.Clone()
			next.ID = targetID
			var saved *model.Config
			var err error
			if targetID > 0 {
				saved, err = st.UpdateConfig(ctx, targetID, next)
				if err == nil && next.GetAuthType() != model.AuthTypeAPIKey {
					err = st.UpdateOAuthCredential(ctx, targetID, next.OAuthCredential)
				}
			} else {
				saved, err = st.CreateConfig(ctx, next)
			}
			if err != nil {
				return err
			}
			p.channelIDs[oldID] = saved.ID
			if next.GetAuthType() == model.AuthTypeAPIKey {
				// Runtime observations are not backup configuration. Keep the
				// latest observations for an unchanged credential, and start a new
				// credential without stale health or cooldown from the backup.
				currentKeys, readErr := st.GetAPIKeys(ctx, saved.ID)
				if readErr != nil {
					return readErr
				}
				currentByIndex := make(map[int]*model.APIKey, len(currentKeys))
				for _, currentKey := range currentKeys {
					if currentKey != nil {
						currentByIndex[currentKey.KeyIndex] = currentKey
					}
				}
				if err = st.DeleteAllAPIKeys(ctx, saved.ID); err != nil {
					return err
				}
				nextKeys := make([]*model.APIKey, 0, len(keys))
				for _, k := range keys {
					copyKey := k
					copyKey.ID, copyKey.ChannelID = 0, saved.ID
					copyKey.CooldownUntil, copyKey.CooldownDurationMs = 0, 0
					copyKey.Health = model.APIKeyHealth{}
					if current := currentByIndex[k.KeyIndex]; current != nil && current.APIKey == k.APIKey {
						copyKey.CooldownUntil, copyKey.CooldownDurationMs = current.CooldownUntil, current.CooldownDurationMs
						copyKey.Health = current.Health
					}
					nextKeys = append(nextKeys, &copyKey)
				}
				if err = st.CreateAPIKeysBatch(ctx, nextKeys); err != nil {
					return err
				}
			}
			// Take over existing links without applying portable-link exclusivity
			// to the historical multiple-bindings-per-channel schema.
			if !next.Enabled {
				err = st.RestoreDisabledSiteChannelBindings(ctx, saved.ID)
			} else {
				err = st.MarkSiteProjectionManual(ctx, saved.ID)
			}
			if err != nil {
				return err
			}
			r.Channels++
			return nil
		})
	}
}

func bindingImportFields(accountID, channelID int64, projectionKey, pricingGroup, ownership, status string) map[string]any {
	return map[string]any{"site_account_id": accountID, "channel_id": channelID, "projection_key": projectionKey, "pricing_group": pricingGroup, "ownership": ownership, "status": status}
}

func (s *Server) planBackupBindings(p *backupPlan, state *backupState, input []backupBinding) {
	seen := map[string]bool{}
	seenChannels := map[int64]bool{}
	for index, binding := range input {
		key := fmt.Sprintf("bindings[%d]", index)
		accountID, hasAccount := p.accountIDs[binding.SiteAccountID]
		channelID, hasChannel := p.channelIDs[binding.ChannelID]
		identity := fmt.Sprintf("%d:%s", binding.SiteAccountID, binding.ProjectionKey)
		problem := ""
		if !hasAccount || !hasChannel || binding.SiteAccountID <= 0 || binding.ChannelID <= 0 {
			problem = "invalid_binding_reference"
		} else if strings.TrimSpace(binding.ProjectionKey) == "" || utf8.RuneCountInString(binding.ProjectionKey) > 191 || utf8.RuneCountInString(binding.PricingGroup) > 64 || strings.ContainsAny(binding.ProjectionKey+binding.PricingGroup, "\x00\r\n") {
			problem = "invalid_binding"
		}
		if seen[identity] {
			problem = "duplicate_identity"
		} else if seenChannels[binding.ChannelID] {
			problem = "binding_channel_conflict"
		}
		seen[identity], seenChannels[binding.ChannelID] = true, true
		var before map[string]any
		for _, existing := range state.bindings {
			if existing == nil {
				continue
			}
			if channelID > 0 && existing.ChannelID == channelID && (existing.SiteAccountID != accountID || existing.ProjectionKey != binding.ProjectionKey) {
				problem = "binding_channel_conflict"
			}
			if accountID > 0 && existing.SiteAccountID == accountID && existing.ProjectionKey == binding.ProjectionKey {
				before = bindingImportFields(existing.SiteAccountID, existing.ChannelID, existing.ProjectionKey, existing.PricingGroup, existing.Ownership, existing.Status)
			}
		}
		status := "active"
		if !p.accountAvailable[binding.SiteAccountID] || !p.channelEnabled[binding.ChannelID] {
			status = "disabled"
		}
		previewAccountID, previewChannelID := accountID, channelID
		if previewAccountID == 0 {
			previewAccountID = -binding.SiteAccountID
		}
		if previewChannelID == 0 {
			previewChannelID = -binding.ChannelID
		}
		after := bindingImportFields(previewAccountID, previewChannelID, binding.ProjectionKey, binding.PricingGroup, "manual", status)
		p.add(key, "binding", "渠道绑定", before, after, []string{"request_history", "statistics"}, problem, func(ctx context.Context, st storage.Store, r *backupImportResult) error {
			link := &model.SiteChannelBinding{SiteAccountID: p.accountIDs[binding.SiteAccountID], ChannelID: p.channelIDs[binding.ChannelID], ProjectionKey: binding.ProjectionKey, PricingGroup: binding.PricingGroup}
			if err := st.RestoreSiteChannelBinding(ctx, link); err != nil {
				return err
			}
			r.Bindings++
			return nil
		})
		if problem == "" && p.items[len(p.items)-1].Action != "unchanged" {
			p.items[len(p.items)-1].Message = "binding_manual_restore"
		}
	}
}

func (s *Server) planBackupTokens(p *backupPlan, state *backupState, input []backupToken) {
	byHash := map[string]*model.AuthToken{}
	for _, v := range state.tokens {
		if v != nil {
			byHash[v.Token] = v
		}
	}
	seen := map[string]bool{}
	for index, item := range input {
		key := fmt.Sprintf("tokens[%d]", index)
		plain := strings.TrimSpace(item.PlainToken)
		if plain == "" {
			p.items = append(p.items, configPreviewItem{Key: key, Entity: "token", Label: importLabel(item.Description), Action: "preserve", Message: "unrecoverable_token", Fields: []string{}, PreservedFields: []string{"existing_token"}})
			continue
		}
		hash := model.HashToken(plain)
		existing := byHash[hash]
		problem := ""
		if seen[hash] {
			problem = "duplicate_identity"
		}
		seen[hash] = true
		mode, err := model.NormalizeChannelRestrictionMode(item.ChannelRestrictionMode)
		if err != nil {
			problem = "invalid_token"
		}
		v := &model.AuthToken{Token: hash, Description: item.Description, ExpiresAt: item.ExpiresAt, IsActive: item.IsActive, AllowedModels: item.AllowedModels, ChannelRestrictionMode: mode, MaxConcurrency: item.MaxConcurrency, AllowedProtocols: item.AllowedProtocols}
		v.SetCostLimitUSD(item.CostLimitUSD)
		if item.CostLimitUSD < 0 || math.IsNaN(item.CostLimitUSD) || math.IsInf(item.CostLimitUSD, 0) || v.ValidateUsageLimits() != nil {
			problem = "invalid_token"
		}
		for _, name := range v.AllowedModels {
			if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "\x00\r\n") {
				problem = "invalid_token"
			}
		}
		for _, protocol := range v.AllowedProtocols {
			if !slices.Contains([]string{"openai", "anthropic", "codex", "gemini"}, protocol) {
				problem = "invalid_token"
			}
		}
		for _, id := range item.AllowedChannelIDs {
			mapped, ok := p.channelIDs[id]
			if !ok || id <= 0 {
				problem = "missing_channel_reference"
			}
			if mapped == 0 {
				mapped = -id
			}
			v.AllowedChannelIDs = append(v.AllowedChannelIDs, mapped)
		}
		var before map[string]any
		preserved := []string{"used_cost", "usage_statistics", "last_used_at"}
		if existing != nil {
			before = tokenImportFields(existing)
			v.ID = existing.ID
			if item.AllowedProtocols == nil {
				v.AllowedProtocols = existing.AllowedProtocols
				preserved = append(preserved, "legacy_protocol_restrictions")
			}
		}
		p.add(key, "token", v.Description, before, tokenImportFields(v), preserved, problem, func(ctx context.Context, st storage.Store, r *backupImportResult) error {
			next := *v
			next.AllowedChannelIDs = remapBackupChannelIDs(item.AllowedChannelIDs, p.channelIDs)
			sealed, err := s.siteControl.cipher.Seal(plain)
			if err != nil {
				return err
			}
			next.TokenCiphertext, next.TokenHint = sealed, model.MaskToken(plain)
			if existing != nil {
				current, readErr := st.GetAuthToken(ctx, existing.ID)
				if readErr != nil {
					return readErr
				}
				next.LastUsedAt = current.LastUsedAt
				err = st.UpdateAuthToken(ctx, &next)
			} else {
				err = st.CreateAuthToken(ctx, &next)
			}
			if err == nil {
				r.Tokens++
			}
			return err
		})
	}
}

func (s *Server) planBackupSettings(p *backupPlan, state *backupState, document *backupDocument) {
	settings := map[string]*model.SystemSetting{}
	for _, v := range state.settings {
		if v != nil {
			settings[v.Key] = v
		}
	}
	seen := map[string]bool{}
	for index, v := range document.Settings {
		key := fmt.Sprintf("settings[%d]", index)
		existing := settings[v.Key]
		if existing == nil || isContainerManagedUpdateSetting(v.Key) {
			p.items = append(p.items, configPreviewItem{Key: key, Entity: "setting", Label: importLabel(v.Key), Action: "preserve", Message: "managed_or_unknown_setting", Fields: []string{}, PreservedFields: []string{"current_setting"}})
			continue
		}
		problem := ""
		if seen[v.Key] {
			problem = "duplicate_identity"
		}
		seen[v.Key] = true
		if validateSettingValue(v.Key, existing.ValueType, v.Value) != nil {
			problem = "invalid_setting"
		}
		p.add(key, "setting", v.Key, map[string]any{"value": existing.Value}, map[string]any{"value": v.Value}, nil, problem, func(ctx context.Context, st storage.Store, r *backupImportResult) error {
			if err := st.UpdateSetting(ctx, v.Key, v.Value); err != nil {
				return err
			}
			r.Settings++
			r.Restart = true
			return nil
		})
	}
	if v := document.Notification; v != nil {
		problem := ""
		var before map[string]any
		if state.webhook != nil {
			old, err := s.backupNotificationValue(state.webhook)
			if err != nil {
				problem = "credential_locked"
			} else {
				before = importFields(old)
			}
		}
		if v.CooldownMinutes < 0 || v.LowBalanceThreshold < 0 || (v.WebhookURL != "" && validateWebDAVURL(v.WebhookURL) != nil) || (v.WebhookEnabled && v.WebhookURL == "") || (v.TelegramEnabled && (v.TelegramBotToken == "" || v.TelegramChatID == "")) {
			problem = "invalid_notification"
		}
		p.add("notification", "notification", "通知设置", before, importFields(v), []string{"notification_history"}, problem, func(ctx context.Context, st storage.Store, _ *backupImportResult) error {
			return s.backupWriter(st).importNotification(ctx, v)
		})
	}
	if v := document.BackupTarget; v != nil {
		problem := ""
		var before map[string]any
		if state.target != nil {
			old, err := s.backupWriterValue(state.target)
			if err != nil {
				if v.Password == "" {
					problem = "credential_locked"
				} else {
					// The current cipher was checked before planning. An explicit
					// replacement can repair the old envelope; compare safe settings
					// without attempting to recover its unavailable plaintext.
					previous := *state.target
					previous.PasswordCiphertext = ""
					old, _ = s.backupWriterValue(&previous)
					before = importFields(old)
				}
			} else {
				before = importFields(old)
			}
		}
		if _, err := normalizeBackupType(v.ExportType); err != nil {
			problem = "invalid_backup_target"
		}
		if v.AutoSyncIntervalHours < 1 || v.AutoSyncIntervalHours > 720 || ((v.Enabled || v.AutoSyncEnabled) && v.FileURL == "") || (v.FileURL != "" && validateWebDAVURL(v.FileURL) != nil) {
			problem = "invalid_backup_target"
		}
		p.add("backup_target", "backup_target", "WebDAV 设置", before, importFields(v), []string{"backup_sync_history"}, problem, func(ctx context.Context, st storage.Store, _ *backupImportResult) error {
			return s.backupWriter(st).importBackupTarget(ctx, v)
		})
	}
}

// This narrowly constructed writer has no runtime caches, goroutines or copied
// mutexes; the reused credential serializers write only through the tx store.
func (s *Server) backupWriter(st storage.Store) *Server {
	writer := &Server{store: st}
	if s.siteControl != nil {
		writer.siteControl = &siteControlService{store: st, cipher: s.siteControl.cipher}
		writer.siteControl.webhookSender.Clients.AllowPrivate = s.siteControl.webhookSender.Clients.AllowPrivate
	}
	return writer
}
func (s *Server) backupNotificationValue(v *model.WebhookConfig) (*backupNotification, error) {
	n := &backupNotification{WebhookEnabled: v.Enabled, TelegramEnabled: v.TelegramEnabled, TelegramUseSystemProxy: v.TelegramUseSystemProxy, LowBalanceEnabled: v.LowBalanceEnabled, LowBalanceThreshold: v.LowBalanceThreshold, CheckinFailureEnabled: v.CheckinFailureEnabled, CooldownMinutes: v.CooldownMinutes}
	if v.URLCiphertext != "" {
		var secret webhookSecret
		if err := s.siteControl.cipher.Open(v.URLCiphertext, &secret); err != nil {
			return nil, err
		}
		n.WebhookURL = secret.URL
	}
	for ciphertext, target := range map[string]*string{v.TelegramBotCiphertext: &n.TelegramBotToken, v.TelegramChatCiphertext: &n.TelegramChatID} {
		if ciphertext != "" {
			var secret telegramSecret
			if err := s.siteControl.cipher.Open(ciphertext, &secret); err != nil {
				return nil, err
			}
			*target = secret.Value
		}
	}
	return n, nil
}
func (s *Server) backupWriterValue(v *model.BackupConfig) (*backupTarget, error) {
	t := &backupTarget{Enabled: v.Enabled, FileURL: v.FileURL, Username: v.Username, ExportType: v.ExportType, AutoSyncEnabled: v.AutoSyncEnabled, AutoSyncIntervalHours: v.AutoSyncIntervalHours}
	if v.PasswordCiphertext != "" {
		var secret backupPasswordSecret
		if err := s.siteControl.cipher.Open(v.PasswordCiphertext, &secret); err != nil {
			return nil, err
		}
		t.Password = secret.Value
	}
	return t, nil
}

func (s *Server) HandleBackupPreview(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, backupMaxImportBytes)
	var request struct {
		Data json.RawMessage `json:"data"`
	}
	if c.ShouldBindJSON(&request) != nil || len(request.Data) == 0 {
		RespondErrorMsg(c, 400, "invalid_backup_document")
		return
	}
	var doc backupDocument
	if json.Unmarshal(request.Data, &doc) != nil {
		RespondErrorMsg(c, 400, "invalid_backup_document")
		return
	}
	p, err := s.previewBackupDocument(c.Request.Context(), &doc)
	if err != nil {
		RespondErrorMsg(c, 400, err.Error())
		return
	}
	RespondJSON(c, 200, p)
}
