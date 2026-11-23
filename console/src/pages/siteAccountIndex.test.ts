import assert from 'node:assert/strict'
import { test } from 'node:test'
import type { SiteAccount } from '../types.ts'
import { buildSiteAccountIndex } from './siteAccountIndex.ts'

function account(id: number, siteId: number, status: string, enabled = true): SiteAccount {
  return { id, site_id: siteId, label: `synthetic-${id}`, status, enabled,
    credential_type: 'api_key', credential_configured: true, credential_refresh_configured: false,
    auto_checkin: false, auto_refresh: false, balance_currency: 'USD',
    last_refresh_status: 'unknown', consecutive_failures: 0, last_checkin_status: 'unknown',
    created_at: 1, updated_at: 1 }
}

test('site grouping retains account order and the existing healthy-status definition', () => {
  const accounts = [account(1, 10, 'healthy'), account(2, 20, 'expired'), account(3, 10, 'healthy', false), account(4, 10, 'unknown')]
  const index = buildSiteAccountIndex(accounts)
  assert.deepEqual(index.get(10)?.accounts.map((item) => item.id), [1, 3, 4])
  assert.equal(index.get(10)?.healthy, 2)
  assert.deepEqual(index.get(20)?.accounts.map((item) => item.id), [2])
  assert.equal(index.get(20)?.healthy, 0)
  assert.equal(index.has(30), false)
  assert.deepEqual(accounts.map((item) => item.id), [1, 2, 3, 4])
})

test('an empty inventory has no fabricated sites or healthy accounts', () => {
  assert.equal(buildSiteAccountIndex([]).size, 0)
})
