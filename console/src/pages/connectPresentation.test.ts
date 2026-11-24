import assert from 'node:assert/strict'
import test from 'node:test'
import type { Channel, Site, SiteAccount, SiteAccountModel } from '../types.ts'
import { buildConnectionGuide, type ConnectConfigurationInput } from './connectPresentation.ts'

function site(overrides: Partial<Site> = {}): Site {
  return {
    id: 1,
    name: 'source',
    platform: 'new-api',
    base_url: 'https://source.example',
    enabled: true,
    timezone: 'Asia/Shanghai',
    use_system_proxy: false,
    tags_json: '[]',
    last_probe_status: 'healthy',
    created_at: 1,
    updated_at: 1,
    ...overrides,
  }
}

function account(overrides: Partial<SiteAccount> = {}): SiteAccount {
  return {
    id: 1,
    site_id: 1,
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
    last_checkin_status: 'unsupported',
    created_at: 1,
    updated_at: 1,
    ...overrides,
  }
}

function fact(overrides: Partial<SiteAccountModel> = {}): SiteAccountModel {
  return {
    site_account_id: 1,
    model: 'gpt-test',
    route_type: 'openai',
    source: 'account',
    disabled: false,
    stale: false,
    last_seen_at: 1,
    created_at: 1,
    updated_at: 1,
    ...overrides,
  }
}

function channel(overrides: Partial<Channel> = {}): Channel {
  return {
    id: 1,
    name: 'channel',
    auth_type: 'api_key',
    protocol_transform_mode: 'auto',
    urls: [{ url: 'https://upstream.example' }],
    priority: 1,
    rpm_limit: 0,
    max_concurrency: 0,
    enabled: true,
    models: [{ model: 'gpt-test' }],
    daily_cost_limit: 0,
    cost_multiplier: 1,
    key_count: 1,
    ...overrides,
  }
}

function configuration(overrides: Partial<ConnectConfigurationInput> = {}): ConnectConfigurationInput {
  return {
    sites: [],
    accounts: [],
    facts: [],
    channels: [],
    ...overrides,
  }
}

test('disabled sites, stale facts, and disabled channels do not count as usable configuration', () => {
  const guide = buildConnectionGuide({
    configuration: configuration({
      sites: [site({ id: 1, enabled: false }), site({ id: 2, enabled: true })],
      accounts: [account({ id: 1, site_id: 1 }), account({ id: 2, site_id: 2 })],
      facts: [fact({ site_account_id: 1 }), fact({ site_account_id: 2, stale: true })],
      channels: [channel({ enabled: false })],
    }),
    tokens: 1,
    configured: true,
    verified: false,
    loading: false,
  })

  assert.equal(guide.configuration.usableAccounts, 0)
  assert.equal(guide.configuration.usableChannels, 0)
  assert.equal(guide.configurationReadyCount, 0)
  assert.equal(guide.prerequisites.every((step) => step.state !== 'ready'), true)
})

test('partial reads stay unknown and are not counted as configured', () => {
  const guide = buildConnectionGuide({
    configuration: configuration({
      sites: [site()],
      accounts: [account()],
      facts: null,
    }),
    tokens: 1,
    configured: false,
    verified: false,
    loading: false,
  })

  assert.equal(guide.configuration.partial, true)
  assert.equal(guide.configuration.unknownSources, 1)
  assert.equal(guide.prerequisites[1].state, 'unknown')
  assert.equal(guide.summary.includes('未知'), true)
})

test('current facts cannot confirm models when the owning sites read is missing', () => {
  const guide = buildConnectionGuide({
    configuration: configuration({
      sites: null,
      accounts: [account()],
      facts: [fact()],
      channels: [],
    }),
    tokens: 1,
    configured: false,
    verified: false,
    loading: false,
  })

  assert.equal(guide.prerequisites[1].state, 'unknown')
})

test('current facts cannot confirm models when the owning accounts read is missing', () => {
  const guide = buildConnectionGuide({
    configuration: configuration({
      sites: [site()],
      accounts: null,
      facts: [fact()],
      channels: [],
    }),
    tokens: 1,
    configured: false,
    verified: false,
    loading: false,
  })

  assert.equal(guide.prerequisites[1].state, 'unknown')
})

test('usable account ownership requires enabled ownership and current facts', () => {
  const guide = buildConnectionGuide({
    configuration: configuration({
      sites: [site()],
      accounts: [account()],
      facts: [fact()],
    }),
    tokens: 1,
    configured: true,
    verified: false,
    loading: false,
  })

  assert.equal(guide.configuration.usableAccounts, 1)
  assert.equal(guide.configuration.usableSites, 1)
  assert.equal(guide.configuration.modelCount, 1)
  assert.deepEqual(guide.prerequisites.map((step) => step.state), ['ready', 'ready', 'pending'])
})

test('large configuration counts stay compact in the collapsed summary', () => {
  const accounts = Array.from({ length: 120 }, (_, index) => account({ id: index + 1, label: `account-${index}` }))
  const facts = accounts.map((item, index) => fact({ site_account_id: item.id, model: `model-${index}` }))
  const guide = buildConnectionGuide({
    configuration: configuration({ sites: [site()], accounts, facts }),
    tokens: 1,
    configured: true,
    verified: false,
    loading: false,
  })

  assert.equal(guide.configuration.usableAccounts, 120)
  assert.equal(guide.summary.includes('99+'), true)
})

test('the five-step guide keeps client generation ready without claiming a successful call', () => {
  const guide = buildConnectionGuide({
    configuration: configuration({
      sites: [site()],
      accounts: [account()],
      facts: [fact()],
    }),
    tokens: 1,
    configured: true,
    verified: false,
    loading: false,
  })

  assert.equal(guide.steps.length, 5)
  assert.equal(guide.steps[3].scope, 'configuration')
  assert.equal(guide.steps[3].ready, true)
  assert.equal(guide.steps[4].ready, false)
  assert.equal(guide.verification.detail.includes('单独执行'), true)
})
