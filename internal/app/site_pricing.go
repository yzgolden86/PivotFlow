package app

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/yzgolden86/PivotFlow/internal/site/provider"
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
// stored cost match what the upstream actually deducts.
type sitePricingCache struct {
	mu      sync.RWMutex
	entries map[int64]*sitePricingEntry
	// byChannel maps a projected channel to its site and pricing group. Rebuilt
	// from site_channel_bindings, which is the only channel→site path.
	byChannel   map[int64]channelBinding
	channelsAt  time.Time
	channelsTTL time.Duration
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
		entries:     make(map[int64]*sitePricingEntry),
		byChannel:   make(map[int64]channelBinding),
		channelsTTL: 5 * time.Minute,
	}
}

// invalidate drops every cached table. Called when sites, accounts or channels
// change, since a re-projection can move a token to a different group.
func (c *sitePricingCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries = make(map[int64]*sitePricingEntry)
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
				return
			}
		}
	}

	c.entries[siteID] = &sitePricingEntry{pricing: pricing, fetchedAt: now, failed: failed}
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
		s.sitePricing.store(binding.siteID, provider.SitePricing{}, true, now)
		if provider.ErrorCode(err) != provider.CodeUnsupported {
			log.Printf("[PRICING] 站点 %d 价目表读取失败，回退本地估算: %v", binding.siteID, err)
		}
		return channelPricing{}, false
	}
	s.sitePricing.store(binding.siteID, pricing, false, now)
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
			continue
		}
		return pricing, nil
	}
	return provider.SitePricing{}, lastErr
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
	if s == nil || s.sitePricing == nil || res == nil || channelID <= 0 {
		return 0, false
	}
	// Bounded: the table is cached per site, so this only reaches the upstream
	// once per site per TTL, never on the hot path of every request.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	cp, ok := s.resolveChannelPricing(ctx, channelID)
	if !ok {
		return 0, false
	}
	return computeSiteCost(cp, modelName, res)
}

// computeSiteCost prices one request with the site's own ratios.
//
// Returns false when this site does not price the model, so the caller falls
// back to the local vendor-list estimate instead of recording a wrong number.
//
// Cache accounting mirrors the upstream: cache reads use cache_ratio, and cache
// writes (5m and 1h buckets) use cache_creation_ratio. New API does not
// distinguish the two write TTLs, so both are charged at the same ratio.
func computeSiteCost(cp channelPricing, modelName string, res *fwResult) (float64, bool) {
	if res == nil {
		return 0, false
	}
	price, ok := sitePriceFor(cp.pricing, modelName)
	if !ok {
		return 0, false
	}
	groupRatio := cp.pricing.RatioFor(cp.group)

	// Per-call models ignore token counts entirely.
	if price.QuotaType == 1 {
		return price.PerCallUSD(groupRatio), true
	}

	inputPerM, outputPerM, cacheReadPerM, cacheWritePerM := price.USDPerMillion(groupRatio)
	const perMillion = 1_000_000.0

	// Cached reads are billed at their own ratio, so they must not also be
	// counted as ordinary input. Upstreams report cache reads separately.
	billableInput := res.InputTokens
	if billableInput < 0 {
		billableInput = 0
	}
	// Prefer the split buckets, but fall back to the combined field: some
	// upstreams report only the total, and missing it would undercount cache
	// writes to zero.
	cacheWriteTokens := res.Cache5mInputTokens + res.Cache1hInputTokens
	if cacheWriteTokens == 0 {
		cacheWriteTokens = res.CacheCreationInputTokens
	}

	total := float64(billableInput)*inputPerM/perMillion +
		float64(res.OutputTokens)*outputPerM/perMillion +
		float64(res.CacheReadInputTokens)*cacheReadPerM/perMillion +
		float64(cacheWriteTokens)*cacheWritePerM/perMillion
	if total < 0 {
		return 0, false
	}
	return total, true
}

// sitePriceFor finds the price row for a model, tolerating case differences the
// way the rest of the routing layer does.
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
	return provider.ModelPrice{}, false
}
