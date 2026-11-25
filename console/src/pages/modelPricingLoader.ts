import type { SitePricingSnapshot } from '../types'

type ReadPricing = (siteId: number, refresh: boolean, signal: AbortSignal) => Promise<SitePricingSnapshot>
type PublishPricing = (siteId: number, snapshot: SitePricingSnapshot) => void
type PricingOptions = { signal?: AbortSignal; refresh?: boolean }

export function createModelPricingLoader(read: ReadPricing) {
  type Run = {
    ids: number[]
    next: number
    remaining: number
    controller: AbortController
    refresh: boolean
    publish: PublishPricing
    resolve: () => void
    detach: () => void
  }
  let current: Run | null = null
  // Cancelled reads still occupy their slots until their promises settle.
  // This also bounds replacement loads when a transport ignores cancellation.
  let active = 0

  const cancel = (run: Run) => {
    if (current === run) current = null
    run.detach()
    run.controller.abort()
    run.resolve()
  }
  const abort = () => { if (current) cancel(current) }

  const readOne = async (run: Run, siteId: number) => {
    try {
      const value = await read(siteId, run.refresh, run.controller.signal)
      if (current === run && !run.controller.signal.aborted
          && value && !Array.isArray(value) && Array.isArray(value.models)) {
        run.publish(siteId, { ...value, group_ratio: value.group_ratio || {} })
      }
    } catch {
      // One unavailable site's management prices must not stop other sites.
    } finally {
      active--
      run.remaining--
      if (current === run && run.remaining === 0) {
        current = null
        run.detach()
        run.resolve()
      }
      // Avoid recursive pumping when a reader throws synchronously.
      queueMicrotask(pump)
    }
  }

  function pump() {
    const run = current
    if (!run) return
    while (current === run && !run.controller.signal.aborted && active < 3 && run.next < run.ids.length) {
      const siteId = run.ids[run.next++]
      active++
      void readOne(run, siteId)
    }
  }

  return {
    abort,
    load(siteIds: readonly number[], publish: PublishPricing, options: PricingOptions = {}): Promise<void> {
      abort()
      const ids = [...new Set(siteIds.filter((siteId) => Number.isSafeInteger(siteId) && siteId > 0))]
      if (!ids.length || options.signal?.aborted) return Promise.resolve()
      let resolve!: () => void
      const done = new Promise<void>((complete) => { resolve = complete })
      const run: Run = { ids, next: 0, remaining: ids.length, controller: new AbortController(),
        refresh: options.refresh ?? false, publish, resolve, detach: () => {} }
      current = run
      if (options.signal) {
        const parent = options.signal
        const onAbort = () => cancel(run)
        parent.addEventListener('abort', onAbort, { once: true })
        run.detach = () => parent.removeEventListener('abort', onAbort)
      }
      pump()
      return done
    },
  }
}
