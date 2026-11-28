package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/yzgolden86/PivotFlow/internal/model"
	"github.com/yzgolden86/PivotFlow/internal/site/provider"
	"github.com/yzgolden86/PivotFlow/internal/util"
)

// sitePricingTTL bounds how stale a site price table may get. Operators change
// ratios rarely; an hour keeps one request per site per hour at worst.
const sitePricingTTL = time.Hour

// sitePricingFailureTTL throttles retries after a site refuses the endpoint, so
// a site without /api/pricing is not probed on every request.
const sitePricingFailureTTL = 15 * time.Minute

// channelPricing is the resolved price table for one channel: the site's own
// ratios plus the group multiplier that channel's upstream token belongs to.
type channelPricing struct {
	pricing provider.SitePricing
	group   string
}

// sitePricingCache holds each site's declared price table.
//
// Cost is otherwise computed from vendor list prices, which have no relation to
// a relay's configured 倍率. Reading the site's own table is what makes the
// stored estimate reflect the site's declared prices instead of vendor prices.
type sitePricingCache struct {
	mu      sync.RWMutex
	entries map[int64]*sitePricingEntry
	// byChannel maps a projected channel to its site and pricing group. Rebuilt
	// from site_channel_bindings, which is the only channel→site path.
	byChannel   map[int64]channelBinding
	channelsAt  time.Time
	channelsTTL time.Duration
	// generations retires in-flight reads when a site's pricing source changes.
	// A flight captures the generation before it starts and may only commit a
	// result while that generation is still current.
	generations map[int64]uint64
	// unsupportedLogged throttles the reason log for sites whose pricing is
	// permanently unavailable (platform without the endpoint, account without
	// a management credential). Re-armed by a later success or a full drop.
	unsupportedLogged map[int64]bool
}

type sitePricingEntry struct {
	pricing   provider.SitePricing
	fetchedAt time.Time
	// failed records that the site rejected or lacks the endpoint, so the next
	// lookup returns nothing cheaply instead of retrying immediately.
	failed bool
}

type channelBinding struct {
	siteID int64
	group  string
}

func newSitePricingCache() *sitePricingCache {
	return &sitePricingCache{
		entries:           make(map[int64]*sitePricingEntry),
		byChannel:         make(map[int64]channelBinding),
		generations:       make(map[int64]uint64),
		unsupportedLogged: make(map[int64]bool),
		channelsTTL:       5 * time.Minute,
	}
}

// invalidate drops all pricing state, for a full cache reset only.
func (c *sitePricingCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = make(map[int64]*sitePricingEntry)
	c.byChannel = make(map[int64]channelBinding)
	c.channelsAt = time.Time{}
	c.unsupportedLogged = make(map[int64]bool)
	for siteID := range c.generations {
		c.generations[siteID]++
	}
	c.mu.Unlock()
}

// invalidateSite is reserved for changes to a site's pricing source, not
// route reconciliation. Other sites retain their valid and negative entries.
func (c *sitePricingCache) invalidateSite(siteID int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.entries, siteID)
	delete(c.unsupportedLogged, siteID)
	c.generations[siteID]++
	c.mu.Unlock()
}

// currentGeneration returns the site's pricing-source generation, creating the
// entry so a later invalidate can retire a read that is already in flight.
func (c *sitePricingCache) currentGeneration(siteID int64) uint64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generations == nil {
		c.generations = make(map[int64]uint64)
	}
	generation := c.generations[siteID]
	c.generations[siteID] = generation
	return generation
}

func (c *sitePricingCache) generationChanged(siteID int64, generation uint64) bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.generations[siteID] != generation
}

// invalidateBindings drops only the channel→site bindings, keeping cached
// price tables. It is the hook for channel-list invalidation: channel edits,
// model refreshes and credential refresh callbacks change which channels
// exist, but never the site-wide price tables themselves. Dropping tables
// there would force one refetch (and its 15-minute failure window) per
// unrelated admin operation, which is why logs filled with 本地估算.
func (c *sitePricingCache) invalidateBindings() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.byChannel = make(map[int64]channelBinding)
	c.channelsAt = time.Time{}
	c.mu.Unlock()
}

// lookup returns the cached table for a site, and whether it is usable.
func (c *sitePricingCache) lookup(siteID int64, now time.Time) (provider.SitePricing, bool) {
	if c == nil {
		return provider.SitePricing{}, false
	}
	c.mu.RLock()
	entry, ok := c.entries[siteID]
	c.mu.RUnlock()
	if !ok {
		return provider.SitePricing{}, false
	}
	ttl := sitePricingTTL
	if entry.failed {
		ttl = sitePricingFailureTTL
	}
	if now.Sub(entry.fetchedAt) > ttl {
		return provider.SitePricing{}, false
	}
	if entry.failed {
		// Fresh negative result: report "resolved, unusable" so the caller does
		// not attempt a fetch, but still falls back to local estimation.
		return provider.SitePricing{}, true
	}
	return entry.pricing, true
}

func (c *sitePricingCache) store(siteID int64, pricing provider.SitePricing, failed bool, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.storeLocked(siteID, pricing, failed, now)
}

// storeForGeneration commits a flight result only while the generation it was
// read under is still current. Returns whether the result was accepted.
func (c *sitePricingCache) storeForGeneration(siteID int64, generation uint64, pricing provider.SitePricing, failed bool, now time.Time) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generations[siteID] != generation {
		return false
	}
	return c.storeLocked(siteID, pricing, failed, now)
}

func (c *sitePricingCache) storeLocked(siteID int64, pricing provider.SitePricing, failed bool, now time.Time) bool {
	// 防御性覆盖：并发 cache miss 可能触发多次 fetchSitePricing，有的成功、
	// 有的超时或失败；若无条件覆盖，失败结果能覆盖刚写入的成功缓存，导致
	// 同渠道在秒级内交替使用站点价目表和本地估算。
	// 规则：失败结果不覆盖尚未过期的成功缓存，避免已有可用价格被随机失败污染。
	if failed {
		existing, ok := c.entries[siteID]
		if ok && !existing.failed {
			ttl := sitePricingTTL
			if now.Sub(existing.fetchedAt) <= ttl {
				// 已有成功结果且未过期，拒绝失败结果覆盖
				return false
			}
		}
	}

	c.entries[siteID] = &sitePricingEntry{pricing: pricing, fetchedAt: now, failed: failed}
	if !failed {
		// 成功后重新武装「不可用原因」日志：凭证补齐等修复值得再次可见。
		delete(c.unsupportedLogged, siteID)
	}
	return true
}

// shouldLogUnsupported reports whether the caller should log why this site's
// pricing is unavailable: once per site until it next succeeds or the cache is
// fully dropped. Permanent conditions (platform without the endpoint, account
// without a management credential) would otherwise repeat once per failure TTL.
func (c *sitePricingCache) shouldLogUnsupported(siteID int64) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unsupportedLogged == nil {
		c.unsupportedLogged = make(map[int64]bool)
	}
	if c.unsupportedLogged[siteID] {
		return false
	}
	c.unsupportedLogged[siteID] = true
	return true
}

func (c *sitePricingCache) channelBindingsFresh(now time.Time) (map[int64]channelBinding, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.channelsAt.IsZero() || now.Sub(c.channelsAt) > c.channelsTTL {
		return nil, false
	}
	return c.byChannel, true
}

func (c *sitePricingCache) storeChannelBindings(bindings map[int64]channelBinding, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.byChannel = bindings
	c.channelsAt = now
	c.mu.Unlock()
}

// resolveChannelPricing finds the site price table covering one channel.
//
// Returns false whenever the site's own prices are unavailable for any reason —
// a manual channel with no site binding, a site whose adapter has no pricing
// endpoint, an account without a management credential, or a fetch failure.
// The caller must then fall back to local estimation rather than guess.
func (s *Server) resolveChannelPricing(ctx context.Context, channelID int64) (channelPricing, bool) {
	if s == nil || s.sitePricing == nil || s.siteControl == nil || channelID <= 0 {
		return channelPricing{}, false
	}

	now := time.Now()
	bindings, ok := s.sitePricing.channelBindingsFresh(now)
	if !ok {
		var err error
		bindings, err = s.loadChannelBindings(ctx)
		if err != nil {
			return channelPricing{}, false
		}
		s.sitePricing.storeChannelBindings(bindings, now)
	}

	binding, bound := bindings[channelID]
	if !bound || binding.siteID <= 0 {
		// Manual channels are not owned by a site; there is no site table to read.
		return channelPricing{}, false
	}

	if pricing, resolved := s.sitePricing.lookup(binding.siteID, now); resolved {
		if len(pricing.Models) == 0 {
			return channelPricing{}, false
		}
		return channelPricing{pricing: pricing, group: binding.group}, true
	}

	pricing, err := s.fetchSitePricing(ctx, binding.siteID)
	if err != nil {
		if provider.ErrorCode(err) == provider.CodeUnsupported {
			// 长期不可用的原因也要可见：平台无价目表接口、账号缺管理凭证
			// 或站点未暴露该端点都会让站点持续走本地估算。每站点只提示
			// 一次（成功后重新提示），避免每 15 分钟刷屏。
			if s.sitePricing.shouldLogUnsupported(binding.siteID) {
				log.Printf("[PRICING] 站点 %d 价目表不可用（%v），费用将持续按本地估算；可检查站点平台是否支持或账号是否配置管理凭证", binding.siteID, err)
			}
		} else {
			log.Printf("[PRICING] 站点 %d 价目表读取失败，回退本地估算: %v", binding.siteID, err)
		}
		return channelPricing{}, false
	}
	return channelPricing{pricing: pricing, group: binding.group}, true
}

// loadChannelBindings builds the channel→(site, group) map from projections.
func (s *Server) loadChannelBindings(ctx context.Context) (map[int64]channelBinding, error) {
	bindings, err := s.store.ListSiteChannelBindings(ctx)
	if err != nil {
		return nil, err
	}
	accounts, err := s.store.ListSiteAccounts(ctx, 0, false)
	if err != nil {
		return nil, err
	}
	siteByAccount := make(map[int64]int64, len(accounts))
	for _, account := range accounts {
		if account != nil {
			siteByAccount[account.ID] = account.SiteID
		}
	}

	out := make(map[int64]channelBinding, len(bindings))
	for _, binding := range bindings {
		if binding == nil || binding.ChannelID <= 0 || binding.Ownership != "projected" {
			continue
		}
		if siteID, ok := siteByAccount[binding.SiteAccountID]; ok && siteID > 0 {
			out[binding.ChannelID] = channelBinding{siteID: siteID, group: binding.PricingGroup}
		}
	}
	return out, nil
}

// fetchSitePricing reads one site's table through its provider adapter.
func (s *Server) fetchSitePricing(ctx context.Context, siteID int64) (provider.SitePricing, error) {
	if err := ctx.Err(); err != nil {
		return provider.SitePricing{}, err
	}
	for {
		generation := s.sitePricing.currentGeneration(siteID)
		// The generation is part of the key: after invalidateSite, new waiters
		// start (or join) the read for the new source instead of attaching to a
		// flight that can only return the retired table.
		key := strconv.FormatInt(siteID, 10) + ":" + strconv.FormatUint(generation, 10)
		result := s.siteControl.pricingFlights.DoChan(key, func() (any, error) {
			type pricingResult struct {
				pricing provider.SitePricing
				err     error
			}
			done := make(chan pricingResult, 1)
			if !s.siteControl.runAsync("pricing:"+newSiteTaskID(), func(parent context.Context) {
				readCtx, cancel := context.WithTimeout(parent, 8*time.Second)
				defer cancel()
				// The flight owns the cache commit. A waiter may cancel (or
				// disappear), but the read and its result still belong here;
				// otherwise the cancellation would look like a site failure.
				commit := func(pricing provider.SitePricing, err error) {
					if s.sitePricing == nil {
						return
					}
					if err != nil {
						if errors.Is(err, context.Canceled) || errors.Is(err, errSiteTaskStopped) ||
							errors.Is(readCtx.Err(), context.Canceled) {
							return
						}
						s.sitePricing.storeForGeneration(siteID, generation, provider.SitePricing{}, true, time.Now())
						return
					}
					s.sitePricing.storeForGeneration(siteID, generation, pricing, false, time.Now())
				}
				release, ok := s.siteControl.acquireSiteGate(readCtx, siteID)
				if !ok {
					err := readCtx.Err()
					commit(provider.SitePricing{}, err)
					done <- pricingResult{err: err}
					return
				}
				defer release()
				pricing, err := s.fetchSitePricingUnshared(readCtx, siteID)
				commit(pricing, err)
				done <- pricingResult{pricing: pricing, err: err}
			}) {
				return provider.SitePricing{}, errSiteTaskStopped
			}
			loaded := <-done
			return loaded.pricing, loaded.err
		})
		select {
		case <-ctx.Done():
			return provider.SitePricing{}, ctx.Err()
		case result := <-result:
			if s.sitePricing.generationChanged(siteID, generation) {
				// The source changed while this flight was running. Its table
				// may describe the old endpoint, so retry under the new
				// generation rather than return or cache retired data.
				continue
			}
			if result.Err != nil {
				return provider.SitePricing{}, result.Err
			}
			return result.Val.(provider.SitePricing), nil
		}
	}
}

func (s *Server) fetchSitePricingUnshared(ctx context.Context, siteID int64) (provider.SitePricing, error) {
	site, err := s.store.GetSite(ctx, siteID)
	if err != nil {
		return provider.SitePricing{}, err
	}
	adapter, err := s.siteControl.adapter(site)
	if err != nil {
		return provider.SitePricing{}, err
	}
	pricer, ok := adapter.(provider.PricingProvider)
	if !ok {
		return provider.SitePricing{}, &provider.Error{Code: provider.CodeUnsupported, Message: "adapter has no pricing endpoint"}
	}

	// Any account of the site with a management credential can read the table;
	// the prices are site-wide, not per account.
	accounts, err := s.store.ListSiteAccounts(ctx, siteID, false)
	if err != nil {
		return provider.SitePricing{}, err
	}
	var lastErr error = &provider.Error{Code: provider.CodeUnsupported, Message: "no account with a management credential"}
	for _, account := range accounts {
		if account == nil || !account.Enabled {
			continue
		}
		creds, err := s.siteControl.credentials(account)
		if err != nil {
			lastErr = err
			if provider.ErrorCode(err) == provider.CodeRateLimited || ctx.Err() != nil {
				break
			}
			continue
		}
		// /api/pricing needs a management session; a model-call key cannot read it.
		if creds.AccessToken == "" && creds.Cookie == "" {
			continue
		}
		pricing, err := pricer.FetchPricing(ctx, provider.AccountRequest{
			BaseURL:     site.BaseURL,
			ProxyURL:    siteProxyURL(site),
			Credentials: creds,
		})
		if err != nil {
			lastErr = err
			if provider.ErrorCode(err) == provider.CodeRateLimited || ctx.Err() != nil {
				break
			}
			continue
		}
		return pricing, nil
	}
	return provider.SitePricing{}, lastErr
}

type sitePricingModelPayload struct {
	Model              string   `json:"model"`
	QuotaType          int      `json:"quota_type"`
	PerCallPrice       float64  `json:"per_call_price"`
	PerCallPriceValid  *bool    `json:"per_call_price_valid,omitempty"`
	ModelRatio         float64  `json:"model_ratio"`
	CompletionRatio    float64  `json:"completion_ratio"`
	CacheRatio         float64  `json:"cache_ratio"`
	CacheCreationRatio float64  `json:"cache_creation_ratio"`
	Groups             []string `json:"groups"`
	InputPrice         float64  `json:"input_price"`
	OutputPrice        float64  `json:"output_price"`
	CacheReadPrice     float64  `json:"cache_read_price"`
	CacheWritePrice    float64  `json:"cache_write_price"`
}

type sitePricingPayload struct {
	SiteID     int64                     `json:"site_id"`
	Available  bool                      `json:"available"`
	Models     []sitePricingModelPayload `json:"models"`
	GroupRatio map[string]float64        `json:"group_ratio"`
}

func buildSitePricingPayload(siteID int64, pricing provider.SitePricing) sitePricingPayload {
	models := make([]sitePricingModelPayload, 0, len(pricing.Models))
	for _, price := range pricing.Models {
		input, output, cacheRead, cacheWrite := price.USDPerMillion(1)
		groups := append([]string(nil), price.Groups...)
		if groups == nil {
			groups = []string{}
		}
		models = append(models, sitePricingModelPayload{
			Model: price.Model, QuotaType: price.QuotaType, PerCallPrice: price.PerCallPrice,
			PerCallPriceValid: price.PerCallPriceValid,
			ModelRatio:        price.ModelRatio, CompletionRatio: price.CompletionRatio,
			CacheRatio: price.CacheRatio, CacheCreationRatio: price.CacheCreationRatio,
			Groups: groups, InputPrice: input, OutputPrice: output,
			CacheReadPrice: cacheRead, CacheWritePrice: cacheWrite,
		})
	}
	groupRatio := make(map[string]float64, len(pricing.GroupRatio))
	for group, ratio := range pricing.GroupRatio {
		groupRatio[group] = ratio
	}
	return sitePricingPayload{
		SiteID: siteID, Available: len(models) > 0, Models: models, GroupRatio: groupRatio,
	}
}

// handleSitePricing exposes the site's own billing table to the console. The
// table is optional upstream capability; unavailable sites return an empty,
// successful payload instead of turning the model page into an error state.
func (s *Server) handleSitePricing(c *gin.Context) {
	if s == nil || s.sitePricing == nil {
		RespondJSON(c, http.StatusOK, sitePricingPayload{Models: []sitePricingModelPayload{}, GroupRatio: map[string]float64{}})
		return
	}
	siteID, err := strconv.ParseInt(c.Query("site_id"), 10, 64)
	if err != nil || siteID <= 0 {
		RespondErrorMsg(c, http.StatusBadRequest, "invalid_request")
		return
	}

	now := time.Now()
	pricing, cached := s.sitePricing.lookup(siteID, now)
	if !cached || c.Query("refresh") == "true" {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
		fetched, fetchErr := s.fetchSitePricing(ctx, siteID)
		cancel()
		if fetchErr != nil {
			pricing = provider.SitePricing{}
		} else {
			pricing = fetched
		}
	}
	RespondJSON(c, http.StatusOK, buildSitePricingPayload(siteID, pricing))
}

// Cost source labels recorded on each log row, so the console can say whether a
// figure came from the site's own price table or from a local estimate.
const (
	costSourceSite  = "site_pricing"
	costSourceLocal = "local_estimate"
)

func costSourceLabel(fromSite bool) string {
	if fromSite {
		return costSourceSite
	}
	return costSourceLocal
}

// siteSourcedCost prices a request with the owning site's declared ratios.
// Returns false when the site's prices are unavailable for any reason.
func (s *Server) siteSourcedCost(modelName string, channelID int64, res *fwResult) (float64, bool) {
	estimate, ok := s.siteSourcedEstimate(modelName, channelID, res)
	return estimate.cost, ok
}

func (s *Server) siteSourcedEstimate(modelName string, channelID int64, res *fwResult) (siteCostEstimate, bool) {
	if s == nil || s.sitePricing == nil || res == nil || channelID <= 0 {
		return siteCostEstimate{}, false
	}
	// Bounded: the table is cached per site, so this only reaches the upstream
	// once per site per TTL, never on the hot path of every request.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	cp, ok := s.resolveChannelPricing(ctx, channelID)
	if !ok {
		return siteCostEstimate{}, false
	}
	if mapped, exists := cp.pricing.DetermineBillingModel(modelName); exists {
		modelName = mapped
	}
	return computeSiteCostEstimate(cp, modelName, res)
}

// computeSiteCost prices one request with the site's own ratios.
//
// Returns false when this site does not price the model, so the caller falls
// back to the local vendor-list estimate instead of recording a wrong number.
//
// Cache accounting mirrors the upstream: cache reads use their own term, while
// the 5-minute and 1-hour write buckets are priced independently. Legacy ratio
// pricing has one cache-creation ratio, so USDPerMillionAll maps it to both.
func computeSiteCost(cp channelPricing, modelName string, res *fwResult) (float64, bool) {
	estimate, ok := computeSiteCostEstimate(cp, modelName, res)
	return estimate.cost, ok
}

func computeSiteCostEstimate(cp channelPricing, modelName string, res *fwResult) (siteCostEstimate, bool) {
	if res == nil {
		return siteCostEstimate{}, false
	}
	price, ok := sitePriceFor(cp.pricing, modelName)
	if !ok {
		return siteCostEstimate{}, false
	}
	groupRatio := siteGroupRatioFor(cp.pricing, price, cp.group)

	// Per-call models use model_price as their complete charge. Only a
	// per-token expression that could not be evaluated must refuse the row;
	// otherwise its display model_ratio is known to be unrelated to billing.
	if price.QuotaType == 1 {
		cost := price.PerCallUSD(groupRatio)
		if !model.IsValidCostAmount(cost) {
			return siteCostEstimate{}, false
		}
		return siteCostEstimate{cost: cost, basis: model.CostBasisPerCall}, true
	}
	if price.BillingExprUnsupported {
		return siteCostEstimate{}, false
	}

	inputPerM, outputPerM, cacheReadPerM, cacheWrite5mPerM, cacheWrite1hPerM := price.USDPerMillionAll(groupRatio)
	const perMillion = 1_000_000.0

	// Cached reads are billed at their own ratio, so they must not also be
	// counted as ordinary input. Upstreams report cache reads separately.
	billableInput := res.InputTokens
	if billableInput < 0 {
		billableInput = 0
	}
	// Prefer the split buckets, but fall back to the combined field: some
	// upstreams report only the total and do not identify its TTL. In that case
	// attribute it to the 5-minute bucket, matching the parser's normalization.
	cacheWrite5mTokens := res.Cache5mInputTokens
	cacheWrite1hTokens := res.Cache1hInputTokens
	if cacheWrite5mTokens+cacheWrite1hTokens == 0 {
		cacheWrite5mTokens = res.CacheCreationInputTokens
	}

	total := float64(billableInput)*inputPerM/perMillion +
		float64(res.OutputTokens)*outputPerM/perMillion +
		float64(res.CacheReadInputTokens)*cacheReadPerM/perMillion +
		float64(cacheWrite5mTokens)*cacheWrite5mPerM/perMillion +
		float64(cacheWrite1hTokens)*cacheWrite1hPerM/perMillion
	if !model.IsValidCostAmount(total) {
		return siteCostEstimate{}, false
	}
	breakdown := &util.StandardCostBreakdown{
		Input:      util.CostComponent{PricePerMillion: inputPerM, Quantity: billableInput, Cost: float64(billableInput) * inputPerM / perMillion},
		Output:     util.CostComponent{PricePerMillion: outputPerM, Quantity: res.OutputTokens, Cost: float64(res.OutputTokens) * outputPerM / perMillion},
		CacheRead:  util.CostComponent{PricePerMillion: cacheReadPerM, Quantity: res.CacheReadInputTokens, Cost: float64(res.CacheReadInputTokens) * cacheReadPerM / perMillion},
		CacheWrite: util.CostComponent{Quantity: cacheWrite5mTokens + cacheWrite1hTokens, Cost: (float64(cacheWrite5mTokens)*cacheWrite5mPerM + float64(cacheWrite1hTokens)*cacheWrite1hPerM) / perMillion},
		Total:      total,
	}
	if breakdown.CacheWrite.Quantity > 0 {
		breakdown.CacheWrite.PricePerMillion = breakdown.CacheWrite.Cost * perMillion / float64(breakdown.CacheWrite.Quantity)
	}
	return siteCostEstimate{cost: total, basis: model.CostBasisToken, breakdown: breakdown}, true
}

// siteGroupRatioFor resolves the upstream group multiplier for one price row.
// Binding data wins when it is usable. Projections created before groups were
// persisted can still carry an empty/default group, so a model enabled in
// exactly one priced group is a safe inference; multiple candidates are not.
func siteGroupRatioFor(pricing provider.SitePricing, price provider.ModelPrice, group string) float64 {
	group = strings.TrimSpace(group)
	if group != "" && group != "default" {
		if ratio, ok := pricing.GroupRatio[group]; ok && ratio > 0 {
			return ratio
		}
	}

	var inferred string
	candidates := 0
	for _, enabledGroup := range price.Groups {
		enabledGroup = strings.TrimSpace(enabledGroup)
		if enabledGroup == "" || enabledGroup == "default" {
			continue
		}
		if ratio, ok := pricing.GroupRatio[enabledGroup]; !ok || ratio <= 0 {
			continue
		}
		if candidates == 0 || enabledGroup != inferred {
			candidates++
		}
		inferred = enabledGroup
	}
	if candidates == 1 {
		return pricing.GroupRatio[inferred]
	}
	return pricing.RatioFor(group)
}

// sitePriceFor finds the price row for a model, tolerating case differences the
// way the rest of the routing layer does.
//
// New API forks often expose a channel-scoped display name (for example
// "channel1/DeepSeek-V4.1-Flash-1017") while the request may carry only the
// model suffix. A basename match is accepted only when it identifies exactly
// one price row; ambiguous names fall back to local estimation rather than
// silently picking another channel's price.
func sitePriceFor(pricing provider.SitePricing, modelName string) (provider.ModelPrice, bool) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return provider.ModelPrice{}, false
	}
	for _, price := range pricing.Models {
		if price.Model == modelName {
			return price, true
		}
	}
	for _, price := range pricing.Models {
		if strings.EqualFold(price.Model, modelName) {
			return price, true
		}
	}

	wantedBase := sitePriceBaseName(modelName)
	if wantedBase == "" {
		return provider.ModelPrice{}, false
	}
	var match provider.ModelPrice
	matches := 0
	for _, price := range pricing.Models {
		if !strings.EqualFold(sitePriceBaseName(price.Model), wantedBase) {
			continue
		}
		if matches == 0 || price.Model != match.Model {
			matches++
		}
		match = price
	}
	if matches == 1 {
		return match, true
	}
	return provider.ModelPrice{}, false
}

func sitePriceBaseName(modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if index := strings.LastIndex(modelName, "/"); index >= 0 {
		modelName = strings.TrimSpace(modelName[index+1:])
	}
	return modelName
}
