import { useCallback, useEffect, useRef, useState } from 'react'
import { getRuntimeMetrics } from '../api'
import type { RuntimeMetrics } from '../types'
import { runtimeDisplayState, runtimeFreshness } from './runtimeHealth'

/** A page owns one bounded read at a time. Errors retain the previous snapshot
 * and only a successful read can make it current again. */
export function useRuntimeMetrics(autoRefresh = true) {
  const [snapshot, setSnapshot] = useState<RuntimeMetrics | null>(null)
  const [error, setError] = useState('')
  const [refreshing, setRefreshing] = useState(autoRefresh)
  const [receivedAt, setReceivedAt] = useState(0)
  const [now, setNow] = useState(Date.now)
  const runRef = useRef<(() => void) | null>(null)
  const refresh = useCallback(() => runRef.current?.(), [])

  useEffect(() => {
    let alive = true
    let controller: AbortController | null = null
    let timer: number | undefined
    const visible = () => document.visibilityState !== 'hidden'
    const clearTimer = () => {
      if (timer !== undefined) window.clearTimeout(timer)
      timer = undefined
    }
    const schedule = () => {
      clearTimer()
      if (alive && autoRefresh && visible()) timer = window.setTimeout(() => void run(), 10_000)
    }
    const run = async () => {
      if (!alive || controller) return
      setNow(Date.now())
      const requestController = new AbortController()
      controller = requestController
      let timedOut = false
      setRefreshing(true)
      const timeout = window.setTimeout(() => {
        timedOut = true
        requestController.abort()
      }, 8_000)
      try {
        const data = await getRuntimeMetrics(requestController.signal)
        if (!alive || requestController.signal.aborted) return
        const fetchedAt = Date.now()
        setSnapshot(data)
        setReceivedAt(fetchedAt)
        setNow(fetchedAt)
        setError('')
      } catch (reason) {
        if (alive) setError(timedOut ? '运行指标请求超时（8 秒），请检查连接后重试' : reason instanceof Error ? reason.message : '运行指标读取失败')
      } finally {
        window.clearTimeout(timeout)
        if (alive) {
          controller = null
          setRefreshing(false)
          schedule()
        }
      }
    }
    const visibilityChanged = () => {
      clearTimer()
      if (visible()) {
        setNow(Date.now())
        if (autoRefresh) void run()
      }
    }
    runRef.current = () => void run()
    setRefreshing(false)
    if (autoRefresh && visible()) void run()
    const clock = window.setInterval(() => { if (visible()) setNow(Date.now()) }, 1_000)
    document.addEventListener('visibilitychange', visibilityChanged)
    return () => {
      alive = false
      clearTimer()
      window.clearInterval(clock)
      document.removeEventListener('visibilitychange', visibilityChanged)
      controller?.abort()
      runRef.current = null
    }
  }, [autoRefresh])

  const freshness = runtimeFreshness(receivedAt, Boolean(error), !autoRefresh, now)
  return { snapshot, error, refreshing, receivedAt, freshness, state: runtimeDisplayState(snapshot, freshness), refresh }
}
