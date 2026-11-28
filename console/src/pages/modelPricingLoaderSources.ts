import type { Channel, Site, SiteAccount, SiteAccountModel, SiteChannelBinding } from '../types.ts'
import { catalogTestSelection, channelSiteId, eligibleChannelTestTargets, factsForSite, type CatalogStatusFilter } from './modelTestEligibility.ts'

export function catalogPricingFacts(
  facts: readonly SiteAccountModel[],
  siteId: number,
  status: CatalogStatusFilter,
  accounts: ReadonlyMap<number, SiteAccount>,
  sites: ReadonlyMap<number, Site>,
): SiteAccountModel[] {
  return factsForSite(facts, siteId, accounts).filter((fact) => {
    if (status === 'all') return true
    if (status === 'stale') return fact.stale && !fact.disabled
    if (status === 'disabled') return fact.disabled
    return Boolean(catalogTestSelection(fact.model, fact, [], accounts, sites))
  })
}

export function modelPricingSources(
  channels: readonly Channel[],
  facts: readonly SiteAccountModel[],
  bindings: readonly SiteChannelBinding[],
  accounts: ReadonlyMap<number, SiteAccount>,
  status: CatalogStatusFilter = 'all',
): Array<{ siteId: number; group: string }> {
  const bindingByChannel = new Map(bindings.filter((binding) => binding.channel_id).map((binding) => [binding.channel_id as number, binding]))
  const sources: Array<{ siteId: number; group: string }> = []
  const historicalSites = status === 'stale' || status === 'disabled'
    ? new Set(facts.map((fact) => accounts.get(fact.site_account_id)?.site_id)) : null
  for (const channel of channels) {
    const binding = bindingByChannel.get(channel.id)
    const siteId = channelSiteId(channel, binding, accounts)
    if (siteId && (!historicalSites || historicalSites.has(siteId))) sources.push({ siteId, group: binding?.pricing_group || 'default' })
  }
  for (const fact of facts) {
    const siteId = accounts.get(fact.site_account_id)?.site_id
    if (siteId) sources.push({ siteId, group: 'default' })
  }
  return sources
}

export function catalogPricingSiteIds(
  channels: readonly Channel[],
  facts: readonly SiteAccountModel[],
  bindings: readonly SiteChannelBinding[],
  accounts: ReadonlyMap<number, SiteAccount>,
): number[] {
  const targetIds = new Set(eligibleChannelTestTargets(channels).map((target) => target.id))
  const sources = modelPricingSources(channels.filter((channel) => targetIds.has(channel.id)), facts, bindings, accounts)
  // Unrepresented bindings can belong to slow providers; they must not occupy
  // the first pricing slots ahead of models that actually appear in the catalog.
  return [...new Set(sources.map((source) => source.siteId).filter((siteId) => Number.isSafeInteger(siteId) && siteId > 0))]
}
