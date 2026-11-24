import type { Channel, Site, SiteAccount, SiteAccountModel, SiteChannelBinding } from '../types.ts'

export type ModelTestTargetKind = 'site_account' | 'channel'

export type CatalogStatusFilter = 'all' | 'available' | 'stale' | 'disabled'

export interface ModelTestTarget {
  kind: ModelTestTargetKind
  id: number
  models: string[]
}

export interface CatalogTestSelection {
  kind: ModelTestTargetKind
  id: number
  model: string
}

function uniqueModels(models: Iterable<string>): string[] {
  const seen = new Set<string>()
  const result: string[] = []
  for (const raw of models) {
    const model = raw.trim()
    if (!model || seen.has(model)) continue
    seen.add(model)
    result.push(model)
  }
  return result
}

interface RankedCatalogFact {
  fact: SiteAccountModel
  eligible: boolean
  current: boolean
  recency: number
  stableID: number
}

/**
 * Ranks the per-account facts that share a model so a single representative can
 * be chosen for the catalog card. An eligible fact (enabled site + enabled
 * account + current, non-disabled, non-stale) always outranks a stale or
 * disabled fact that happens to sit later in the facts list.
 */
function rankCatalogFacts(
  facts: readonly SiteAccountModel[],
  accounts: ReadonlyMap<number, SiteAccount>,
  sites: ReadonlyMap<number, Site>,
): RankedCatalogFact[] {
  return facts.map((fact) => {
    const account = accounts.get(fact.site_account_id)
    const site = account ? sites.get(account.site_id) : undefined
    return {
      fact,
      eligible: Boolean(account?.enabled && account.credential_configured && site?.enabled && !fact.disabled && !fact.stale),
      current: !fact.disabled && !fact.stale,
      recency: fact.last_seen_at,
      stableID: fact.site_account_id,
    }
  })
}

function byRecencyThenStableID(left: RankedCatalogFact, right: RankedCatalogFact): number {
  if (left.recency !== right.recency) return right.recency - left.recency
  return left.stableID - right.stableID
}

function byEligibilityPriority(left: RankedCatalogFact, right: RankedCatalogFact): number {
  if (left.eligible !== right.eligible) return left.eligible ? -1 : 1
  if (left.current !== right.current) return left.current ? -1 : 1
  return byRecencyThenStableID(left, right)
}

/**
 * Picks the fact that should drive a model's catalog card action. Deterministic
 * for equal priority: most recent `last_seen_at`, then lowest site-account id.
 */
export function representativeCatalogFact(
  facts: readonly SiteAccountModel[],
  accounts: ReadonlyMap<number, SiteAccount>,
  sites: ReadonlyMap<number, Site>,
): SiteAccountModel | undefined {
  return rankCatalogFacts(facts, accounts, sites).sort(byEligibilityPriority)[0]?.fact
}

/**
 * Resolves which fact to display under the catalog status filter so that the
 * explicit 过期/停用 views keep surfacing stale or disabled facts even when an
 * eligible fact exists for the same model.
 */
export function catalogFactForStatus(
  facts: readonly SiteAccountModel[],
  status: CatalogStatusFilter,
  accounts: ReadonlyMap<number, SiteAccount>,
  sites: ReadonlyMap<number, Site>,
): SiteAccountModel | undefined {
  if (status === 'stale' || status === 'disabled') {
    const matches = rankCatalogFacts(facts, accounts, sites)
      .filter((item) => status === 'stale' ? item.fact.stale && !item.fact.disabled : item.fact.disabled)
      .sort(byRecencyThenStableID)
    return matches[0]?.fact
  }
  return representativeCatalogFact(facts, accounts, sites)
}

/**
 * Narrows a model's grouped facts to a single site so the catalog representative
 * is chosen from the selected site. Without this, a newer fact from another site
 * could win the global representative and hide the selected site's account.
 */
export function factsForSite(
  facts: readonly SiteAccountModel[],
  siteId: number,
  accounts: ReadonlyMap<number, SiteAccount>,
): SiteAccountModel[] {
  if (!siteId) return [...facts]
  return facts.filter((fact) => accounts.get(fact.site_account_id)?.site_id === siteId)
}

/**
 * Resolves a channel's owning site with the same authoritative priority the
 * pricing summary uses: the channel's direct site_account_id first, then an
 * existing site-channel binding. Returns undefined when neither maps to a known
 * account, so an unattributable channel is never offered under a site filter.
 */
export function channelSiteId(
  channel: Channel,
  binding: SiteChannelBinding | undefined,
  accounts: ReadonlyMap<number, SiteAccount>,
): number | undefined {
  const ownerID = channel.site_account_id || binding?.site_account_id || 0
  return accounts.get(ownerID)?.site_id
}

/**
 * Narrows a model's channel fallbacks to the selected site. The all-sites
 * filter (`siteId === 0`) preserves the full list, and disabled channels stay
 * in scope because direct channel testing is intentionally allowed for them.
 */
export function channelsForSite(
  channels: readonly Channel[],
  siteId: number,
  bindings: readonly SiteChannelBinding[],
  accounts: ReadonlyMap<number, SiteAccount>,
): Channel[] {
  if (!siteId) return [...channels]
  const bindingByChannel = new Map(
    bindings
      .filter((binding) => Boolean(binding.channel_id))
      .map((binding) => [binding.channel_id as number, binding]),
  )
  return channels.filter(
    (channel) => channelSiteId(channel, bindingByChannel.get(channel.id), accounts) === siteId,
  )
}

export function eligibleChannelTestTargets(channels: readonly Channel[]): ModelTestTarget[] {
  const targets: ModelTestTarget[] = []
  for (const channel of channels) {
    const models = uniqueModels(channel.models.filter((item) => !item.disabled).map((item) => item.model))
    const hasURL = channel.urls.some((item) => Boolean(item.url.trim()))
    const hasCredentialSource = channel.auth_type !== 'api_key' || channel.key_count > 0
    if (!models.length || !hasURL || !hasCredentialSource) continue
    targets.push({ kind: 'channel', id: channel.id, models })
  }
  return targets
}

export function eligibleAccountTestTargets(
  accounts: readonly SiteAccount[],
  sites: readonly Site[],
  facts: readonly SiteAccountModel[],
): ModelTestTarget[] {
  const siteByID = new Map(sites.map((site) => [site.id, site]))
  return accounts.flatMap((account) => {
    const site = siteByID.get(account.site_id)
    if (!account.enabled || !account.credential_configured || !site?.enabled) return []
    const models = uniqueModels(
      facts
        .filter((fact) => fact.site_account_id === account.id && !fact.disabled && !fact.stale)
        .map((fact) => fact.model),
    )
    return models.length ? [{ kind: 'site_account' as const, id: account.id, models }] : []
  })
}

export function catalogTestSelection(
  model: string,
  fact: SiteAccountModel | undefined,
  channels: readonly Channel[],
  accounts: ReadonlyMap<number, SiteAccount>,
  sites: ReadonlyMap<number, Site>,
): CatalogTestSelection | null {
  if (fact) {
    const account = accounts.get(fact.site_account_id)
    const site = account ? sites.get(account.site_id) : undefined
    if (
      account?.enabled &&
      account.credential_configured &&
      site?.enabled &&
      !fact.disabled &&
      !fact.stale &&
      fact.model === model
    ) {
      return { kind: 'site_account', id: account.id, model }
    }
  }

  const channelTarget = eligibleChannelTestTargets(channels).find((target) => target.models.includes(model))
  return channelTarget ? { kind: 'channel', id: channelTarget.id, model } : null
}

export function catalogTestDisabledReason(
  fact: SiteAccountModel | undefined,
  channels: readonly Channel[],
  accounts: ReadonlyMap<number, SiteAccount>,
  sites: ReadonlyMap<number, Site>,
): string {
  if (fact?.disabled) return '站点模型事实已停用，且没有可测试渠道'
  if (fact?.stale) return '站点模型事实已过期，请刷新账号模型或选择可测试渠道'
  if (fact) {
    const account = accounts.get(fact.site_account_id)
    if (!account) return '站点账号已停用或不在当前可用清单'
    if (!account.enabled) return '站点账号已停用，且没有可测试渠道'
    const site = sites.get(account.site_id)
    if (!site) return '账号关联的站点不存在'
    if (!site.enabled) return '站点已停用'
    if (!account.credential_configured) return '站点账号缺少可用凭证，无法直测，且没有可测试渠道'
  }
  if (!channels.length) return '没有当前可测试渠道'
  return '渠道缺少可用模型、URL 或凭证'
}

export function eligibleModelNames(
  channelTargets: readonly ModelTestTarget[],
  accountTargets: readonly ModelTestTarget[],
): Set<string> {
  return new Set([...channelTargets, ...accountTargets].flatMap((target) => target.models).sort((left, right) => left.localeCompare(right)))
}
