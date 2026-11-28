import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { Channel, Site, SiteAccount, SiteAccountModel, SiteChannelBinding } from '../types.ts'
import { catalogPricingFacts, catalogPricingSiteIds, modelPricingSources } from './modelPricingLoaderSources.ts'

const channel = (overrides: Partial<Channel> = {}): Channel => ({
  id: 10, name: 'synthetic-channel', auth_type: 'api_key', protocol_transform_mode: 'auto',
  urls: [{ url: 'https://channel.example' }], priority: 0, rpm_limit: 0, max_concurrency: 0,
  enabled: true, models: [{ model: 'synthetic-model' }], daily_cost_limit: 0, cost_multiplier: 1, key_count: 1,
  ...overrides,
})
const account = (id: number, site_id: number): SiteAccount => ({
  id, site_id, label: 'synthetic-account', credential_type: 'access_token', credential_configured: true,
  credential_refresh_configured: false, enabled: false, auto_checkin: false, auto_refresh: false,
  status: 'healthy', balance_currency: 'USD', last_refresh_status: 'success', consecutive_failures: 0,
  last_checkin_status: 'unknown', created_at: 1, updated_at: 1,
})
const fact = (site_account_id: number): SiteAccountModel => ({
  site_account_id, model: 'synthetic-model', route_type: 'openai_chat', source: 'models_endpoint',
  disabled: false, stale: false, last_seen_at: 1, created_at: 1, updated_at: 1,
})
const binding = (site_account_id: number, channel_id?: number): SiteChannelBinding => ({
  id: site_account_id, site_account_id, channel_id, projection_key: 'synthetic', pricing_group: 'vip',
  ownership: 'managed', status: 'active', last_sync_status: 'success', created_at: 1, updated_at: 1,
})

test('catalog sources start with represented channels and skip binding-only sites so unrelated providers cannot delay their prices', () => {
  const accounts = new Map([account(1, 101), account(2, 102), account(3, 103)].map((item) => [item.id, item]))
  assert.deepEqual(catalogPricingSiteIds(
    [channel({ site_account_id: 2 }), channel({ id: 11, site_account_id: 2 })],
    [fact(3), fact(3)], [binding(1), binding(2, 10)], accounts,
  ), [102, 103])
})

test('direct ownership and disabled-account binding fallback match the catalog while untestable channels start no reads', () => {
  const accounts = new Map([account(1, 101), account(2, 102), account(4, 104), account(13, 113), account(14, 114)].map((item) => [item.id, item]))
  const channels = [
    channel({ site_account_id: 4 }),
    channel({ id: 11, site_account_id: 2 }),
    channel({ id: 12 }),
    channel({ id: 13, site_account_id: 13, models: [{ model: 'disabled-model', disabled: true }] }),
    channel({ id: 14, site_account_id: 14, urls: [] }),
  ]
  const bindings = [binding(1, 10), binding(1, 12)]
  assert.deepEqual(modelPricingSources(channels.slice(0, 3), [fact(2)], bindings, accounts), [
    { siteId: 104, group: 'vip' }, { siteId: 102, group: 'default' },
    { siteId: 101, group: 'vip' }, { siteId: 102, group: 'default' },
  ])
  assert.deepEqual(catalogPricingSiteIds(channels, [], bindings, accounts), [104, 102, 101])
})

test('account-only pricing includes both available sites and selected-site filtering excludes the other site', () => {
  const accounts = new Map([1, 2, 3].map((id) => [id, { ...account(id, 100 + id), enabled: true }]))
  const sites = new Map([101, 102, 103].map((id) => [id, { id, enabled: true } as Site]))
  const facts = [fact(1), fact(2), { ...fact(3), stale: true }]
  const all = catalogPricingFacts(facts, 0, 'available', accounts, sites)
  assert.deepEqual(all.map((item) => item.site_account_id), [1, 2])
  assert.deepEqual(modelPricingSources([], all, [], accounts), [
    { siteId: 101, group: 'default' }, { siteId: 102, group: 'default' },
  ])
  assert.deepEqual(catalogPricingFacts(facts, 102, 'available', accounts, sites).map((item) => item.site_account_id), [2])
})

test('all facts within the chosen stale or disabled status contribute without borrowing facts from other statuses or sites', () => {
  const accounts = new Map([1, 2, 3, 4, 5].map((id) => [id, { ...account(id, id === 4 ? 102 : 101), enabled: true }]))
  const sites = new Map([101, 102].map((id) => [id, { id, enabled: true } as Site]))
  const facts = [fact(1), { ...fact(2), stale: true }, { ...fact(3), stale: true },
    { ...fact(4), stale: true }, { ...fact(5), disabled: true, stale: true }]
  assert.deepEqual(catalogPricingFacts(facts, 101, 'stale', accounts, sites).map((item) => item.site_account_id), [2, 3])
  assert.deepEqual(catalogPricingFacts(facts, 101, 'disabled', accounts, sites).map((item) => item.site_account_id), [5])
  assert.deepEqual(catalogPricingFacts(facts, 101, 'all', accounts, sites).map((item) => item.site_account_id), [1, 2, 3, 5])
})

test('historical pricing does not borrow channel sources outside the matching stale or disabled facts', () => {
  const accounts = new Map([1, 2, 3].map((id) => [id, { ...account(id, 100 + id), enabled: true }]))
  const sites = new Map([101, 102, 103].map((id) => [id, { id, enabled: true } as Site]))
  const facts = [fact(1), { ...fact(2), stale: true }, { ...fact(3), disabled: true }]
  const channels = [1, 2, 3].map((id) => channel({ id, site_account_id: id }))
  const stale = catalogPricingFacts(facts, 0, 'stale', accounts, sites)
  const disabled = catalogPricingFacts(facts, 0, 'disabled', accounts, sites)
  assert.deepEqual(modelPricingSources(channels, stale, [], accounts, 'stale'), [
    { siteId: 102, group: 'default' }, { siteId: 102, group: 'default' },
  ])
  assert.deepEqual(modelPricingSources(channels, disabled, [], accounts, 'disabled'), [
    { siteId: 103, group: 'default' }, { siteId: 103, group: 'default' },
  ])
})
