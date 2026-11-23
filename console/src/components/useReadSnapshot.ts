import { useCallback, useEffect, useRef, useState } from 'react'

interface ReadSnapshot<T> { key: string; data?: T; loading: boolean; error: string }

// A read belongs to its exact scope. Aborted, timed-out and late manual refresh
// responses cannot replace a newer scope. The timeout also ends the UI's wait
// when a transport fails to settle its rejected promise after an abort.
export default function useReadSnapshot<T>(key: string, read: (signal: AbortSignal) => Promise<T>, enabled = true, timeoutMs = 12_000) {
  const [state, setState] = useState<ReadSnapshot<T>>({ key: '', loading: false, error: '' })
  const sequence = useRef(0)
  const pending = useRef<{ controller: AbortController; timer: number } | null>(null)
  const cancel = useCallback(() => {
    sequence.current++
    if (pending.current) {
      window.clearTimeout(pending.current.timer)
      pending.current.controller.abort()
      pending.current = null
    }
  }, [])

  const refresh = useCallback(() => {
    cancel()
    if (!enabled) return
    const generation = sequence.current
    const controller = new AbortController()
    setState((current) => ({ key, data: current.key === key ? current.data : undefined, loading: true, error: '' }))
    const timer = window.setTimeout(() => {
      controller.abort()
      if (generation === sequence.current) setState((current) => ({ ...current, key, loading: false, error: '读取超过 12 秒，请重试当前范围。' }))
    }, timeoutMs)
    pending.current = { controller, timer }
    void read(controller.signal).then((data) => {
      if (!controller.signal.aborted && generation === sequence.current) setState({ key, data, loading: false, error: '' })
    }).catch((reason) => {
      if (!controller.signal.aborted && generation === sequence.current) setState((current) => ({ ...current, key, loading: false, error: reason instanceof Error ? reason.message : '当前快照读取失败' }))
    }).finally(() => {
      window.clearTimeout(timer)
      if (generation === sequence.current) pending.current = null
    })
  }, [cancel, enabled, key, read, timeoutMs])

  useEffect(() => { refresh(); return cancel }, [cancel, refresh])
  const current = state.key === key && enabled ? state : { key, loading: enabled, error: '' } as ReadSnapshot<T>
  return { ...current, refresh, stale: Boolean(current.data && current.error) }
}
