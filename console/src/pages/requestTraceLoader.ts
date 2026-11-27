import type { RequestTrace } from '../types'

type ReadTrace = (requestId: string, signal: AbortSignal) => Promise<RequestTrace>

const RETRY_DELAYS = [400, 700, 900]
const abortError = () => new DOMException('Aborted', 'AbortError')

function isTransientNotFound(reason: unknown): boolean {
  const message = reason instanceof Error ? reason.message : String(reason ?? '')
  // The API currently exposes plain messages, not HTTP status fields. Avoid
  // retrying proxy HTML, malformed payloads or other errors mentioning a 404.
  return /^(?:request trace not found|not[_ ]found(?::.*)?|HTTP 404|请求失败 \(404\))$/i.test(message.trim())
}

export class RequestTracePendingError extends Error {
  constructor() {
    super('请求记录尚未就绪，请稍后重新检查。')
    this.name = 'RequestTracePendingError'
  }
}

// Instantiate in the mounted history owner, never at module scope: saved
// details must not survive the page/session that authorized their reads.
export function createRequestTraceLoader(read: ReadTrace) {
  type Run = {
    id: string
    controller: AbortController
    promise: Promise<RequestTrace>
    resolve: (value: RequestTrace) => void
    reject: (reason: unknown) => void
    consumers: number
    retries: number
    done: boolean
    deadline?: ReturnType<typeof setTimeout>
    backoff?: ReturnType<typeof setTimeout>
  }
  const cache = new Map<string, RequestTrace>()
  const pending = new Map<string, Run>()
  const queue = new Set<Run>()
  // Count cancelled physical reads until they actually settle. An uncooperative
  // transport must not turn rapid modal changes into unlimited background work.
  let active = 0

  function peek(id: string): RequestTrace | undefined {
    const value = cache.get(id)
    if (value) { cache.delete(id); cache.set(id, value) }
    return value
  }

  function finish(run: Run, result: { value: RequestTrace } | { error: unknown }) {
    if (run.done) return
    run.done = true
    clearTimeout(run.deadline)
    clearTimeout(run.backoff)
    queue.delete(run)
    if (pending.get(run.id) === run) pending.delete(run.id)
    if ('value' in result) {
      cache.set(run.id, result.value)
      if (cache.size > 32) cache.delete(cache.keys().next().value!)
      run.resolve(result.value)
    } else {
      run.controller.abort()
      run.reject(result.error)
    }
  }

  function failed(run: Run, error: unknown) {
    if (run.done) return
    if (!isTransientNotFound(error)) { finish(run, { error }); return }
    if (run.retries === RETRY_DELAYS.length) {
      finish(run, { error: new RequestTracePendingError() })
      return
    }
    run.backoff = setTimeout(() => {
      if (!run.done) { queue.add(run); pump() }
    }, RETRY_DELAYS[run.retries++])
  }

  function pump() {
    while (active < 2 && queue.size) {
      const run = queue.values().next().value!
      queue.delete(run)
      if (run.done) continue
      active++
      let reading: Promise<RequestTrace>
      try { reading = Promise.resolve(read(run.id, run.controller.signal)) }
      catch (error) { reading = Promise.reject(error) }
      void reading.then(
        (value) => finish(run, { value }),
        (error: unknown) => failed(run, error),
      ).finally(() => { active--; pump() })
    }
  }

  function subscribe(run: Run, signal?: AbortSignal): Promise<RequestTrace> {
    run.consumers++
    return new Promise((resolve, reject) => {
      let settled = false
      const detach = () => {
        if (settled) return false
        settled = true
        signal?.removeEventListener('abort', cancel)
        run.consumers--
        return true
      }
      const cancel = () => {
        if (!detach()) return
        reject(abortError())
        if (!run.consumers) finish(run, { error: abortError() })
      }
      signal?.addEventListener('abort', cancel, { once: true })
      void run.promise.then(
        (value) => { if (detach()) resolve(value) },
        (error: unknown) => { if (detach()) reject(error) },
      )
    })
  }

  return {
    peek,
    load(requestId: string, signal?: AbortSignal): Promise<RequestTrace> {
      if (signal?.aborted) return Promise.reject(abortError())
      const saved = peek(requestId)
      if (saved) return Promise.resolve(saved)
      let run = pending.get(requestId)
      if (!run) {
        let resolve!: Run['resolve'], reject!: Run['reject']
        const promise = new Promise<RequestTrace>((yes, no) => { resolve = yes; reject = no })
        run = { id: requestId, controller: new AbortController(), promise, resolve, reject,
          consumers: 0, retries: 0, done: false }
        const current = run
        run.deadline = setTimeout(() => {
          finish(current, { error: new DOMException('请求详情读取超过 8 秒，请重试。', 'TimeoutError') })
        }, 8_000)
        pending.set(requestId, run)
        queue.add(run)
      }
      const result = subscribe(run, signal)
      pump()
      return result
    },
    clear() {
      for (const run of pending.values()) finish(run, { error: abortError() })
      cache.clear()
    },
  }
}
