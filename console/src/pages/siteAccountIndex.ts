import type { SiteAccount } from '../types'

export function buildSiteAccountIndex(accounts: readonly SiteAccount[]) {
  const index = new Map<number, { accounts: SiteAccount[]; healthy: number }>()
  for (const account of accounts) {
    let group = index.get(account.site_id)
    if (!group) {
      group = { accounts: [], healthy: 0 }
      index.set(account.site_id, group)
    }
    group.accounts.push(account)
    if (account.status === 'healthy') group.healthy++
  }
  return index
}
