import test from 'node:test'
import assert from 'node:assert/strict'
import type { Channel, Site, SiteAccount, SiteAccountModel, SiteChannelBinding } from '../types.ts'
import {
  catalogFactForStatus,
  catalogTestDisabledReason,
  catalogTestSelection,
  channelsForSite,
  eligibleAccountTestTargets,
  eligibleChannelTestTargets,
  eligibleModelNames,
  factsForSite,
  representativeCatalogFact,
} from './modelTestEligibility.ts'

const channel = (overrides: Partial<Channel> = {}): Channel => ({
  id: 10,
  name: 'channel',
  auth_type: 'api_key',
  protocol_transform_mode: 'auto',
  urls: [{ url: 'https://channel.example' }],
  priority: 0,
  rpm_limit: 0,
  max_concurrency: 0,
  enabled: true,
  models: [{ model: 'channel-model' }],
  daily_cost_limit: 0,
  cost_multiplier: 1,
  key_count: 1,
  ...overrides,
})

const site = (overrides: Partial<Site> = {}): Site => ({
  id: 20,
  name: 'site',
  platform: 'new-api-family',
  base_url: 'https://site.example',
  enabled: true,
  timezone: 'Asia/Shanghai',
  use_system_proxy: false,
  tags_json: '[]',
  last_probe_status: 'success',
  created_at: 1,
  updated_at: 1,
  ...overrides,
})

const account = (overrides: Partial<SiteAccount> = {}): SiteAccount => ({
  id: 30,
  site_id: 20,
  label: 'account',
  credential_type: 'access_token',
  credential_configured: true,
  credential_refresh_configured: false,
  enabled: true,
  auto_checkin: false,
  auto_refresh: false,
  status: 'healthy',
  balance_currency: 'USD',
  last_refresh_status: 'success',
  consecutive_failures: 0,
  last_checkin_status: 'unknown',
  created_at: 1,
  updated_at: 1,
  ...overrides,
})

const fact = (overrides: Partial<SiteAccountModel> = {}): SiteAccountModel => ({
  site_account_id: 30,
  model: 'site-model',
  route_type: 'openai_chat',
  source: 'models_endpoint',
  disabled: false,
  stale: false,
  last_seen_at: 1,
  created_at: 1,
  updated_at: 1,
  ...overrides,
})

const siteBinding = (overrides: Partial<SiteChannelBinding> = {}): SiteChannelBinding => ({
  id: 1,
  site_account_id: 30,
  projection_key: 'projection',
  channel_id: undefined,
  pricing_group: 'default',
  ownership: 'manual',
  status: 'active',
  last_sync_status: 'success',
  created_at: 1,
  updated_at: 1,
  ...overrides,
})

test('direct channel testing keeps a disabled channel when backend testing can still use it', () => {
  const result = eligibleChannelTestTargets([channel({ enabled: false })])
  assert.deepEqual(result, [{ kind: 'channel', id: 10, models: ['channel-model'] }])
})

test('channel targets require a URL, model, and usable credential source', () => {
  const targets = eligibleChannelTestTargets([
    channel({ id: 1, key_count: 0 }),
    channel({ id: 2, urls: [] }),
    channel({ id: 3, models: [] }),
    channel({ id: 4, auth_type: 'codex_oauth', key_count: 0 }),
  ])
  assert.deepEqual(targets, [{ kind: 'channel', id: 4, models: ['channel-model'] }])
})

test('site-account testing excludes disabled sites, disabled accounts, and stale facts', () => {
  const targets = eligibleAccountTestTargets(
    [account(), account({ id: 31, enabled: false })],
    [site(), site({ id: 21, enabled: false }), site({ id: 22 })],
    [
      fact(),
      fact({ model: 'stale-model', stale: true }),
      fact({ model: 'disabled-model', disabled: true }),
      fact({ site_account_id: 32, model: 'disabled-site-model' }),
    ],
  )
  assert.deepEqual(targets, [{ kind: 'site_account', id: 30, models: ['site-model'] }])
})

test('catalog action falls back to a direct channel test when account facts are stale', () => {
  const selection = catalogTestSelection(
    'shared-model',
    fact({ model: 'shared-model', stale: true }),
    [channel({ models: [{ model: 'shared-model' }] })],
    new Map([[30, account()]]),
    new Map([[20, site()]]),
  )
  assert.deepEqual(selection, { kind: 'channel', id: 10, model: 'shared-model' })
})

test('catalog action is unavailable when no current account or channel can test the model', () => {
  const selection = catalogTestSelection(
    'site-model',
    fact(),
    [],
    new Map(),
    new Map([[20, site()]]),
  )
  assert.equal(selection, null)
})

test('eligible model counts use the same targets as the selectors and card buttons', () => {
  const channelTargets = eligibleChannelTestTargets([
    channel({ models: [{ model: 'shared-model' }, { model: 'channel-only' }] }),
  ])
  const accountTargets = eligibleAccountTestTargets(
    [account()],
    [site()],
    [fact({ model: 'shared-model' }), fact({ model: 'account-only' })],
  )
  assert.deepEqual([...eligibleModelNames(channelTargets, accountTargets)], ['account-only', 'channel-only', 'shared-model'])
})

const accountMap = (entries: SiteAccount[]) => new Map(entries.map((item) => [item.id, item]))
const siteMap = (entries: Site[]) => new Map(entries.map((item) => [item.id, item]))

test('catalog representative keeps an eligible fact testable when a stale fact for the same model comes later', () => {
  const accounts = accountMap([account(), account({ id: 31 })])
  const sites = siteMap([site()])
  const eligible = fact({ site_account_id: 30, model: 'shared', last_seen_at: 10 })
  const stale = fact({ site_account_id: 31, model: 'shared', stale: true, last_seen_at: 99 })

  // Both array orderings must select the eligible account; the buggy catalog
  // kept whichever fact appeared last.
  assert.equal(representativeCatalogFact([eligible, stale], accounts, sites)?.site_account_id, 30)
  assert.equal(representativeCatalogFact([stale, eligible], accounts, sites)?.site_account_id, 30)

  // The representative feeds the same selection function as the card buttons,
  // so a fact counted by eligibleModelNames must remain actionable.
  assert.deepEqual(
    catalogTestSelection('shared', representativeCatalogFact([eligible, stale], accounts, sites), [], accounts, sites),
    { kind: 'site_account', id: 30, model: 'shared' },
  )
})

test('catalog representative prefers an eligible fact over a disabled fact in either order', () => {
  const accounts = accountMap([account(), account({ id: 31 })])
  const sites = siteMap([site()])
  const eligible = fact({ site_account_id: 30, model: 'shared', last_seen_at: 10 })
  const disabled = fact({ site_account_id: 31, model: 'shared', disabled: true, last_seen_at: 99 })

  assert.equal(representativeCatalogFact([eligible, disabled], accounts, sites)?.site_account_id, 30)
  assert.equal(representativeCatalogFact([disabled, eligible], accounts, sites)?.site_account_id, 30)
})

test('catalog representative prefers an enabled site over a disabled-site fact with a newer timestamp', () => {
  const accounts = accountMap([account(), account({ id: 31, site_id: 21 })])
  const sites = siteMap([site(), site({ id: 21, enabled: false })])
  const enabledSite = fact({ site_account_id: 30, model: 'shared', last_seen_at: 10 })
  const disabledSite = fact({ site_account_id: 31, model: 'shared', last_seen_at: 99 })

  assert.equal(representativeCatalogFact([disabledSite, enabledSite], accounts, sites)?.site_account_id, 30)
  assert.equal(representativeCatalogFact([enabledSite, disabledSite], accounts, sites)?.site_account_id, 30)
})

test('catalog representative is deterministic among several eligible facts using recency then stable id', () => {
  const accounts = accountMap([account(), account({ id: 31 })])
  const sites = siteMap([site()])
  const older = fact({ site_account_id: 30, model: 'shared', last_seen_at: 10 })
  const newer = fact({ site_account_id: 31, model: 'shared', last_seen_at: 20 })
  assert.equal(representativeCatalogFact([older, newer], accounts, sites)?.site_account_id, 31)

  const tieHigherID = fact({ site_account_id: 31, model: 'shared', last_seen_at: 20 })
  const tieLowerID = fact({ site_account_id: 30, model: 'shared', last_seen_at: 20 })
  assert.equal(representativeCatalogFact([tieHigherID, tieLowerID], accounts, sites)?.site_account_id, 30)
})

test('catalog status filters still surface stale and disabled facts for inspection', () => {
  const accounts = accountMap([account(), account({ id: 31 }), account({ id: 32 })])
  const sites = siteMap([site()])
  const eligible = fact({ site_account_id: 30, model: 'shared', last_seen_at: 30 })
  const stale = fact({ site_account_id: 31, model: 'shared', stale: true, last_seen_at: 20 })
  const disabled = fact({ site_account_id: 32, model: 'shared', disabled: true, last_seen_at: 10 })
  const facts = [eligible, stale, disabled]

  assert.equal(catalogFactForStatus(facts, 'available', accounts, sites)?.site_account_id, 30)
  assert.equal(catalogFactForStatus(facts, 'stale', accounts, sites)?.site_account_id, 31)
  assert.equal(catalogFactForStatus(facts, 'disabled', accounts, sites)?.site_account_id, 32)
})

test('site-account testing requires a configured credential, mirroring the backend ciphertext requirement', () => {
  const targets = eligibleAccountTestTargets(
    [account(), account({ id: 31, credential_configured: false })],
    [site()],
    [fact(), fact({ site_account_id: 31, model: 'no-credential-model' })],
  )
  assert.deepEqual(targets, [{ kind: 'site_account', id: 30, models: ['site-model'] }])
})

test('catalog representative prefers a credentialed account over an uncredentialed one with a newer timestamp', () => {
  const accounts = accountMap([account(), account({ id: 31, credential_configured: false })])
  const sites = siteMap([site()])
  const credentialed = fact({ site_account_id: 30, model: 'shared', last_seen_at: 10 })
  const uncredentialed = fact({ site_account_id: 31, model: 'shared', last_seen_at: 99 })

  assert.equal(representativeCatalogFact([uncredentialed, credentialed], accounts, sites)?.site_account_id, 30)
  assert.equal(representativeCatalogFact([credentialed, uncredentialed], accounts, sites)?.site_account_id, 30)
})

test('catalog action refuses direct testing when the account lacks a configured credential', () => {
  const accounts = accountMap([account({ credential_configured: false })])
  const sites = siteMap([site()])
  assert.equal(catalogTestSelection('site-model', fact(), [], accounts, sites), null)
})

test('catalog action still falls back to a testable channel when the account lacks a credential', () => {
  const accounts = accountMap([account({ credential_configured: false })])
  const sites = siteMap([site()])
  const selection = catalogTestSelection(
    'shared-model',
    fact({ model: 'shared-model' }),
    [channel({ models: [{ model: 'shared-model' }] })],
    accounts,
    sites,
  )
  assert.deepEqual(selection, { kind: 'channel', id: 10, model: 'shared-model' })
})

test('catalog disabled reason names the missing account credential when there is no fallback', () => {
  const accounts = accountMap([account({ credential_configured: false })])
  const sites = siteMap([site()])
  const reason = catalogTestDisabledReason(fact(), [], accounts, sites)
  assert.match(reason, /凭证/)
})

test('site scoping keeps a selected second site visible when two sites share a model', () => {
  const accounts = accountMap([account({ id: 30, site_id: 20 }), account({ id: 31, site_id: 21 })])
  const sites = siteMap([site({ id: 20 }), site({ id: 21 })])
  const firstSite = fact({ site_account_id: 30, model: 'shared', last_seen_at: 99 })
  const secondSite = fact({ site_account_id: 31, model: 'shared', last_seen_at: 10 })

  // The global representative would pick the newer first-site fact, making the
  // selected second site's account disappear under the site filter.
  assert.equal(representativeCatalogFact([firstSite, secondSite], accounts, sites)?.site_account_id, 30)

  const scoped = factsForSite([firstSite, secondSite], 21, accounts)
  assert.deepEqual(scoped.map((item) => item.site_account_id), [31])
  assert.deepEqual(
    catalogTestSelection('shared', representativeCatalogFact(scoped, accounts, sites), [], accounts, sites),
    { kind: 'site_account', id: 31, model: 'shared' },
  )
})

test('two-site channel fallback uses direct channel ownership and never leaks site B into site A', () => {
  const accounts = accountMap([account({ id: 30, site_id: 20 }), account({ id: 31, site_id: 21 })])
  const sites = siteMap([site({ id: 20 }), site({ id: 21 })])
  const staleA = fact({ site_account_id: 30, model: 'shared', stale: true })
  // A disabled channel still allows direct channel testing.
  const channelB = channel({ id: 10, name: 'B-channel', site_account_id: 31, enabled: false, models: [{ model: 'shared' }] })
  const scopedA = channelsForSite([channelB], 20, [], accounts)
  const scopedB = channelsForSite([channelB], 21, [], accounts)
  const scopedAll = channelsForSite([channelB], 0, [], accounts)

  assert.deepEqual(scopedA, [])
  assert.deepEqual(scopedB.map((item) => item.id), [10])
  assert.deepEqual(scopedAll.map((item) => item.id), [10])
  // Selected site A must not borrow site B's channel for its stale fact.
  assert.equal(catalogTestSelection('shared', staleA, scopedA, accounts, sites), null)
  // Selected site B is permitted, including direct testing of a disabled channel.
  assert.deepEqual(
    catalogTestSelection('shared', undefined, scopedB, accounts, sites),
    { kind: 'channel', id: 10, model: 'shared' },
  )
  // The all-sites view still permits the fallback.
  assert.deepEqual(
    catalogTestSelection('shared', staleA, scopedAll, accounts, sites),
    { kind: 'channel', id: 10, model: 'shared' },
  )
})

test('two-site channel scoping honors binding ownership and direct ownership outranks it', () => {
  const accounts = accountMap([account({ id: 30, site_id: 20 }), account({ id: 31, site_id: 21 })])
  const sites = siteMap([site({ id: 20 }), site({ id: 21 })])
  const staleA = fact({ site_account_id: 30, model: 'shared', stale: true })

  // No direct channel site link: ownership comes from the site-channel binding.
  const boundToB = channel({ id: 10, name: 'bound-B', models: [{ model: 'shared' }] })
  const bindingToB = siteBinding({ channel_id: 10, site_account_id: 31 })
  assert.deepEqual(channelsForSite([boundToB], 20, [bindingToB], accounts), [])
  assert.deepEqual(channelsForSite([boundToB], 21, [bindingToB], accounts).map((item) => item.id), [10])
  assert.equal(catalogTestSelection('shared', staleA, channelsForSite([boundToB], 20, [bindingToB], accounts), accounts, sites), null)
  assert.deepEqual(
    catalogTestSelection('shared', staleA, channelsForSite([boundToB], 21, [bindingToB], accounts), accounts, sites),
    { kind: 'channel', id: 10, model: 'shared' },
  )

  // A conflicting binding must not override the channel's direct site link,
  // matching modelPricingSummary's ownership priority.
  const directA = channel({ id: 11, name: 'direct-A', site_account_id: 30, models: [{ model: 'shared' }] })
  const bindingToBForDirect = siteBinding({ channel_id: 11, site_account_id: 31 })
  assert.deepEqual(channelsForSite([directA], 20, [bindingToBForDirect], accounts).map((item) => item.id), [11])
  assert.deepEqual(channelsForSite([directA], 21, [bindingToBForDirect], accounts), [])
})

test('channel ownership survives a disabled account so its direct-test channel stays scoped to the site', () => {
  const disabledOwner = account({ id: 31, site_id: 21, enabled: false })
  const accounts = accountMap([disabledOwner])
  const sites = siteMap([site({ id: 21 })])
  // A disabled-owner channel can still be a valid direct-test target when the
  // channel itself is disabled too.
  const direct = channel({ id: 10, site_account_id: 31, enabled: false, models: [{ model: 'shared' }] })
  const bound = channel({ id: 11, models: [{ model: 'shared' }] })
  const binding = siteBinding({ channel_id: 11, site_account_id: 31 })

  assert.deepEqual(channelsForSite([direct], 21, [], accounts).map((item) => item.id), [10])
  assert.deepEqual(channelsForSite([bound], 21, [binding], accounts).map((item) => item.id), [11])
  assert.deepEqual(channelsForSite([direct], 20, [], accounts), [])
  assert.deepEqual(
    catalogTestSelection('shared', undefined, channelsForSite([direct], 21, [], accounts), accounts, sites),
    { kind: 'channel', id: 10, model: 'shared' },
  )

  // A disabled owner is not a direct account-test choice; the reason names it.
  assert.deepEqual(
    eligibleAccountTestTargets([disabledOwner], [site({ id: 21 })], [fact({ site_account_id: 31, model: 'shared' })]),
    [],
  )
  assert.match(catalogTestDisabledReason(fact({ site_account_id: 31, model: 'shared' }), [], accounts, sites), /账号已停用/)
})
