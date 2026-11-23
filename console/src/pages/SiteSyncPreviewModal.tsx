import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { AlertTriangle, CheckCircle2, RefreshCw, Search, XCircle } from 'lucide-react'
import { cancelSiteTask, getSiteInventory, getSiteTask } from '../api'
import {
  addSitePreviewAccounts,
  applyConfigPreview,
  configPreviewErrorMessage,
  discardConfigPreview,
  getConfigPreview,
  mapWithConcurrency,
  MAX_CONFIG_PREVIEW_ACCOUNTS,
  markPreviewObservationUnknown,
  mergePreviewApplyResult,
  startSiteSyncPreview,
  syncPreviewReference,
  toggleSitePreviewAccount,
  type ConfigPreview,
} from '../configPreview.ts'
import type { Site, SiteAccount, SiteTask } from '../types.ts'
import ConfigPreviewPanel from './ConfigPreviewPanel'
import { EmptyState, LoadingState } from './shared'
import { Modal, siteErrorMessage } from './siteShared'
import { browserImportId } from '../browserImportTypes'

type AccountPreviewPhase = 'starting' | 'running' | 'ready' | 'applying' | 'applied' | 'failed' | 'unknown' | 'cancelled'

interface AccountPreviewState {
  accountId: number
  phase: AccountPreviewPhase
  taskId?: string
  taskStatus?: SiteTask['status']
  preview?: ConfigPreview
  message?: string
  applyMessage?: string
}

interface PreviewJob {
  controller: AbortController
  taskId?: string
}

export default function SiteSyncPreviewModal({
  close,
  onApplied,
  initialAccountId,
}: {
  close: () => void
  onApplied?: () => void
  initialAccountId?: number
}) {
  const [sites, setSites] = useState<Site[]>([])
  const [accounts, setAccounts] = useState<SiteAccount[]>([])
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [states, setStates] = useState<Map<number, AccountPreviewState>>(new Map())
  const [loading, setLoading] = useState(true)
  const [generating, setGenerating] = useState(false)
  const [rechecking, setRechecking] = useState<Set<number>>(new Set())
  const [applyingAccount, setApplyingAccount] = useState<number | null>(null)
  const [error, setError] = useState('')
  const [accountSearch, setAccountSearch] = useState('')
  const [seedNotice, setSeedNotice] = useState('')
  const initialSeed = useRef(browserImportId(initialAccountId))
  const seedHandled = useRef(false)
  const mounted = useRef(true)
  const generation = useRef(0)
  const jobs = useRef(new Map<number, PreviewJob>())
  const statesRef = useRef<Map<number, AccountPreviewState>>(new Map())
  const discardedPreviewIDs = useRef(new Set<string>())
  const recheckingAccounts = useRef(new Set<number>())
  const applyingRef = useRef(false)

  const discardPreviewByID = useCallback((previewID: string) => {
    if (!previewID || discardedPreviewIDs.current.has(previewID)) return
    discardedPreviewIDs.current.add(previewID)
    void discardConfigPreview(previewID).catch(() => undefined)
  }, [])

  const discardStoredPreviews = useCallback((current: Map<number, AccountPreviewState>, readyOnly: boolean) => {
    for (const state of current.values()) {
      const stored = state.preview
      if (!stored || state.phase === 'applying' || stored.status === 'applying') continue
      if (readyOnly && stored.status !== 'ready' && state.phase !== 'ready') continue
      discardPreviewByID(stored.id)
    }
  }, [discardPreviewByID])

  const cancelJobs = useCallback(() => {
    generation.current += 1
    for (const job of jobs.current.values()) {
      job.controller.abort()
      if (job.taskId) void cancelSiteTask(job.taskId).catch(() => undefined)
    }
    jobs.current.clear()
  }, [])

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false
      cancelJobs()
      discardStoredPreviews(statesRef.current, false)
    }
  }, [cancelJobs, discardStoredPreviews])

  useEffect(() => {
    statesRef.current = states
  }, [states])

  const loadData = useCallback(async (signal?: AbortSignal) => {
    setLoading(true)
    setError('')
    try {
      const inventory = await getSiteInventory(signal, { force: true })
      if (signal?.aborted || !mounted.current) return
      setSites(inventory.sites)
      setAccounts(inventory.accounts)
      if (!seedHandled.current && initialSeed.current) {
        seedHandled.current = true
        const target = inventory.accounts.find((account) => account.id === initialSeed.current)
        if (target) {
          setAccountSearch(target.label)
          setSelected(new Set(target.enabled ? [target.id] : []))
          setSeedNotice(target.enabled ? `已预选账号 ${target.label}。请显式点击生成差异预览，再核对上游结果。` : `目标账号 ${target.label} 已停用。请先到账号页核对并启用，再生成渠道差异预览。`)
        } else setSeedNotice('链接中的目标账号不存在，请重新选择当前账号。')
      }
    } catch (reason) {
      if (!signal?.aborted && mounted.current) setError(siteErrorMessage(reason))
    } finally {
      if (!signal?.aborted && mounted.current) setLoading(false)
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void loadData(controller.signal)
    return () => controller.abort()
  }, [loadData])

  const siteMap = useMemo(() => new Map(sites.map((site) => [site.id, site])), [sites])
  const accountMap = useMemo(() => new Map(accounts.map((account) => [account.id, account])), [accounts])
  const filteredAccounts = useMemo(() => {
    const needle = accountSearch.trim().toLocaleLowerCase()
    if (!needle) return accounts
    return accounts.filter((account) => {
      const site = siteMap.get(account.site_id)
      return [account.label, account.credential_type, site?.name, site?.base_url]
        .filter(Boolean)
        .some((value) => String(value).toLocaleLowerCase().includes(needle))
    })
  }, [accountSearch, accounts, siteMap])

  const updateState = useCallback((accountId: number, patch: Partial<AccountPreviewState>) => {
    if (!mounted.current) return
    setStates((current) => {
      const next = new Map(current)
      next.set(accountId, { accountId, phase: 'starting', ...next.get(accountId), ...patch })
      statesRef.current = next
      return next
    })
  }, [])

  const stateValues = [...states.values()]
  const active = stateValues.some((state) => ['starting', 'running', 'applying'].includes(state.phase))
  const anyApplying = stateValues.some((state) => state.phase === 'applying')
  const completedCount = stateValues.filter((state) => ['ready', 'applied', 'failed', 'unknown', 'cancelled'].includes(state.phase)).length
  const readyCount = stateValues.filter((state) => state.phase === 'ready').length
  const appliedCount = stateValues.filter((state) => state.phase === 'applied').length
  const issueCount = stateValues.filter((state) => ['failed', 'unknown', 'cancelled'].includes(state.phase)).length
  const selectionAtLimit = selected.size >= MAX_CONFIG_PREVIEW_ACCOUNTS

  const clearGenerated = useCallback(() => {
    cancelJobs()
    discardStoredPreviews(statesRef.current, false)
    statesRef.current = new Map()
    setStates(new Map())
    setError('')
  }, [cancelJobs, discardStoredPreviews])

  const toggle = (accountId: number) => {
    if (active) return
    clearGenerated()
    const result = toggleSitePreviewAccount(selected, accountId)
    setSelected(result.selected)
    setError(result.limited ? `单次最多选择 ${MAX_CONFIG_PREVIEW_ACCOUNTS} 个站点账号` : '')
  }

  const selectAll = () => {
    if (active) return
    clearGenerated()
    const available = filteredAccounts.filter((account) => account.enabled).map((account) => account.id)
    if (available.length > 0 && available.every((id) => selected.has(id))) {
      const next = new Set(selected)
      available.forEach((id) => next.delete(id))
      setSelected(next)
      setError('')
      return
    }
    const result = addSitePreviewAccounts(selected, available)
    setSelected(result.selected)
    setError(result.limited ? `全选已限制为 ${MAX_CONFIG_PREVIEW_ACCOUNTS} 个站点账号` : '')
  }

  const generatePreviews = async () => {
    const targets = accounts
      .filter((account) => selected.has(account.id) && account.enabled)
      .slice(0, MAX_CONFIG_PREVIEW_ACCOUNTS)
    if (!targets.length) {
      setError('请至少选择一个启用中的站点账号')
      return
    }
    discardStoredPreviews(statesRef.current, false)
    cancelJobs()
    const token = generation.current
    const initial = new Map<number, AccountPreviewState>()
    targets.forEach((account) => initial.set(account.id, { accountId: account.id, phase: 'starting' }))
    statesRef.current = initial
    setStates(initial)
    setGenerating(true)
    setError('')

    await mapWithConcurrency(targets, async (account) => {
      if (!mounted.current || token !== generation.current) return
      const controller = new AbortController()
      const job: PreviewJob = { controller }
      jobs.current.set(account.id, job)
      let discoveredPreviewID = ''
      try {
        const queued = await startSiteSyncPreview(account.id, controller.signal)
        if (controller.signal.aborted || token !== generation.current) {
          void cancelSiteTask(queued.task_id).catch(() => undefined)
          return
        }
        job.taskId = queued.task_id
        updateState(account.id, { phase: 'running', taskId: queued.task_id, taskStatus: 'queued' })
        const task = await waitForPreviewTask(queued.task_id, controller.signal, (current, signal) => {
          if (signal.aborted || token !== generation.current) return
          updateState(account.id, { phase: 'running', taskStatus: current.status, taskId: current.id })
        })
        if (task.status === 'success') discoveredPreviewID = syncPreviewReference(task.result_ref)
        if (controller.signal.aborted || token !== generation.current) {
          discardPreviewByID(discoveredPreviewID)
          return
        }
        if (task.status !== 'success') {
          const phase: AccountPreviewPhase = task.status === 'unknown'
            ? 'unknown'
            : task.status === 'cancelled'
              ? 'cancelled'
              : 'failed'
          updateState(account.id, {
            phase,
            taskStatus: task.status,
            message: task.status === 'unknown'
              ? '任务结果待确认，请保留任务编号并通过原有任务进度检查'
              : task.error
                ? configPreviewErrorMessage(task.error)
                : '同步预览生成失败，请重新尝试',
          })
          return
        }
        if (!discoveredPreviewID) throw new Error('invalid_response')
        const preview = await getConfigPreview(discoveredPreviewID, controller.signal)
        if (controller.signal.aborted || token !== generation.current) {
          discardPreviewByID(preview.id)
          return
        }
        updateState(account.id, { phase: 'ready', taskStatus: 'success', preview, message: '' })
      } catch (reason) {
        if (controller.signal.aborted || token !== generation.current) {
          discardPreviewByID(discoveredPreviewID)
          return
        }
        discardPreviewByID(discoveredPreviewID)
        updateState(account.id, {
          phase: 'unknown',
          message: job.taskId
            ? `任务结果待确认：${configPreviewErrorMessage(reason)}`
            : configPreviewErrorMessage(reason),
        })
      } finally {
        if (jobs.current.get(account.id) === job) jobs.current.delete(account.id)
      }
    }, 3)

    if (mounted.current && token === generation.current) setGenerating(false)
  }

  const cancelGeneration = () => {
    cancelJobs()
    setGenerating(false)
    setStates((current) => {
      const next = new Map(current)
      for (const [accountId, state] of next) {
        if (['starting', 'running'].includes(state.phase)) next.set(accountId, { ...state, phase: 'cancelled', message: '预览生成已取消' })
      }
      statesRef.current = next
      return next
    })
  }

  const applyPreview = async (accountId: number, preview: ConfigPreview) => {
    if (applyingRef.current) return
    applyingRef.current = true
    setApplyingAccount(accountId)
    const token = generation.current
    updateState(accountId, { phase: 'applying', applyMessage: '' })
    try {
      const result = await applyConfigPreview(preview.id, AbortSignal.timeout(30_000))
      if (!mounted.current || token !== generation.current) return
      const merged = mergePreviewApplyResult(preview, result)
      const phase: AccountPreviewPhase = result.status === 'applied'
        ? 'applied'
        : result.status === 'applying'
          ? 'applying'
          : result.status === 'unknown'
            ? 'unknown'
            : 'failed'
      updateState(accountId, {
        phase,
        preview: merged,
        applyMessage: result.status === 'applied'
          ? '已应用该账号的固定预览快照'
          : result.status === 'applying'
            ? '该账号的预览仍在应用，请稍后重新读取状态'
            : result.status === 'unknown'
              ? '提交结果待确认，请人工核对当前配置，请勿重复提交'
              : result.status === 'stale'
                ? '配置已发生变化，旧预览不能应用，请重新生成'
                : '应用失败，请根据安全错误位置核对后重新生成预览',
      })
      if (result.status === 'applied') onApplied?.()
    } catch {
      if (!mounted.current || token !== generation.current) return
      updateState(accountId, {
        phase: 'unknown',
        preview: markPreviewObservationUnknown(preview),
        applyMessage: '应用响应待确认，正在重新读取预览状态…',
      })
      try {
        const current = await getConfigPreview(preview.id)
        if (!mounted.current || token !== generation.current) return
        if (current.status === 'applied') {
          updateState(accountId, {
            phase: 'applied',
            preview: current,
            applyMessage: '已重新读取并确认应用完成',
          })
          onApplied?.()
        } else if (current.status === 'ready' && current.can_apply) {
          updateState(accountId, {
            phase: 'ready',
            preview: current,
            applyMessage: '服务器已确认预览仍可应用；请核对后再次手动确认',
          })
        } else if (current.status === 'applying') {
          updateState(accountId, {
            phase: 'applying',
            preview: current,
            applyMessage: '该账号的预览仍在应用，请稍后重新读取状态',
          })
        } else if (current.status === 'unknown') {
          updateState(accountId, {
            phase: 'unknown',
            preview: current,
            applyMessage: '提交结果待确认，请人工核对当前配置，请勿重复提交',
          })
        } else if (current.status === 'stale') {
          updateState(accountId, {
            phase: 'failed',
            preview: current,
            applyMessage: '配置已发生变化，旧预览不能应用，请重新生成',
          })
        } else if (current.status === 'failed') {
          updateState(accountId, {
            phase: 'failed',
            preview: current,
            applyMessage: '应用失败，请根据安全错误位置核对后重新生成预览',
          })
        } else {
          updateState(accountId, {
            phase: 'ready',
            preview: current,
            applyMessage: '预览当前不能应用，请重新生成预览',
          })
        }
      } catch {
        if (!mounted.current || token !== generation.current) return
        updateState(accountId, {
          phase: 'unknown',
          applyMessage: '操作结果待确认，重新读取失败；请稍后手动复查，请勿重复提交',
        })
      }
    } finally {
      applyingRef.current = false
      if (mounted.current) setApplyingAccount((current) => current === accountId ? null : current)
    }
  }

  const recheckPreview = async (accountId: number, previewID: string) => {
    if (recheckingAccounts.current.has(accountId)) return
    const token = generation.current
    recheckingAccounts.current.add(accountId)
    setRechecking((current) => new Set(current).add(accountId))
    updateState(accountId, { applyMessage: '正在重新读取应用状态…' })
    try {
      const current = await getConfigPreview(previewID)
      if (!mounted.current || token !== generation.current) return
      if (current.status === 'applied') {
        updateState(accountId, {
          phase: 'applied',
          preview: current,
          applyMessage: '已重新读取并确认应用完成',
        })
        onApplied?.()
      } else if (current.status === 'ready' && current.can_apply) {
        updateState(accountId, {
          phase: 'ready',
          preview: current,
          applyMessage: '服务器已确认预览仍可应用；请核对后再次手动确认',
        })
      } else if (current.status === 'applying') {
        updateState(accountId, {
          phase: 'applying',
          preview: current,
          applyMessage: '该账号的预览仍在应用，请稍后再次读取状态',
        })
      } else if (current.status === 'unknown') {
        updateState(accountId, {
          phase: 'unknown',
          preview: current,
          applyMessage: '提交结果待确认，请人工核对当前配置，请勿重复提交',
        })
      } else if (current.status === 'stale') {
        updateState(accountId, {
          phase: 'failed',
          preview: current,
          applyMessage: '配置已发生变化，旧预览不能应用，请重新生成',
        })
      } else {
        updateState(accountId, {
          phase: current.status === 'ready' ? 'ready' : 'failed',
          preview: current,
          applyMessage: '预览当前不能应用，请根据安全错误位置核对后重新生成',
        })
      }
    } catch (reason) {
      if (mounted.current && token === generation.current) {
        updateState(accountId, {
          applyMessage: `重新读取应用状态失败：${configPreviewErrorMessage(reason)}`,
        })
      }
    } finally {
      recheckingAccounts.current.delete(accountId)
      if (mounted.current) {
        setRechecking((current) => {
          const next = new Set(current)
          next.delete(accountId)
          return next
        })
      }
    }
  }

  const handleClose = () => {
    discardStoredPreviews(statesRef.current, false)
    cancelJobs()
    close()
  }

  const generatedStates = stateValues
    .filter((state) => accountMap.has(state.accountId))
    .sort((left, right) => {
      const leftIndex = accounts.findIndex((account) => account.id === left.accountId)
      const rightIndex = accounts.findIndex((account) => account.id === right.accountId)
      return leftIndex - rightIndex
    })

  return (
    <Modal title="同步站点渠道" close={handleClose} wide>
      {error && <div className="inline-error modal-error">{error}</div>}
      {loading ? <LoadingState label="正在加载站点账号" /> : (
        <div className="site-sync-dialog site-sync-preview-dialog">
          <div className="site-sync-toolbar">
            <span>先为每个账号读取上游模型与渠道差异。单次最多选择 {MAX_CONFIG_PREVIEW_ACCOUNTS} 个账号，确认后只应用该账号已预览的固定快照。</span>
            <div><button className="text-button" type="button" onClick={selectAll} disabled={active}>{filteredAccounts.some((account) => account.enabled) && filteredAccounts.filter((account) => account.enabled).every((account) => selected.has(account.id)) ? '取消全选' : '全选搜索结果'}</button><button className="text-button" type="button" onClick={() => { clearGenerated(); setSelected(new Set()) }} disabled={active}>清空</button></div>
          </div>
          {seedNotice && <p className="form-help" role="status">{seedNotice}{accounts.some((account) => account.id === initialSeed.current) && <a className="entity-link" href={`#/accounts?focus_account_id=${initialSeed.current}`} onClick={handleClose}>核对目标账号</a>}</p>}
          <label className="search-field site-sync-search"><Search size={15} /><input value={accountSearch} onChange={(event) => setAccountSearch(event.target.value)} placeholder="搜索账号、站点名称或网址" aria-label="搜索站点账号" /><span>{filteredAccounts.length}/{accounts.length}</span></label>

          {(generating || stateValues.length > 0) && <div className="site-sync-progress" aria-live="polite"><div className="site-sync-progress-head"><strong>{generating ? `正在生成差异预览 · ${completedCount}/${stateValues.length}` : `已生成 ${readyCount} 个待确认 · ${appliedCount} 个已应用 · ${issueCount} 个需处理`}</strong><span>{stateValues.length ? Math.round((completedCount / stateValues.length) * 100) : 0}%</span></div><div className="site-sync-progress-track"><i style={{ width: `${stateValues.length ? Math.round((completedCount / stateValues.length) * 100) : 0}%` }} /></div></div>}

          {!accounts.length ? <EmptyState label="还没有站点账号" /> : (
            <div className="site-sync-list site-sync-account-list">
              {!filteredAccounts.length ? <EmptyState label="没有匹配的站点账号" /> : filteredAccounts.map((account) => {
                const site = siteMap.get(account.site_id)
                const state = states.get(account.id)
                return (
                  <div className={`site-sync-row${selected.has(account.id) ? ' is-selected' : ''}`} key={account.id}>
                    <input
                      type="checkbox"
                      checked={selected.has(account.id)}
                      disabled={!account.enabled || active || (selectionAtLimit && !selected.has(account.id))}
                      onChange={() => toggle(account.id)}
                      aria-label={`选择 ${account.label}`}
                      title={selectionAtLimit && !selected.has(account.id) ? `单次最多选择 ${MAX_CONFIG_PREVIEW_ACCOUNTS} 个账号` : undefined}
                    />
                    <div>
                      <a className="entity-link" href={`#/accounts?focus_account_id=${account.id}${['expired', 'error'].includes(account.status) ? '&open_credential=1' : ''}`} onClick={handleClose}><strong>{account.label}</strong></a>
                      <span><a className="entity-chip" href={`#/sites?focus_site_id=${account.site_id}`} onClick={handleClose}>{site?.name || `站点 #${account.site_id}`}</a> · {account.credential_type === 'api_key' ? '模型 API Key' : '站点登录'}</span>
                    </div>
                    <div><AccountStateBadge state={state} /><span>{state?.message || (state?.taskId ? `任务 ${state.taskId}` : account.enabled ? '可生成预览' : '账号已停用')}</span></div>
                  </div>
                )
              })}
            </div>
          )}

          {generatedStates.length > 0 && (
            <section className="site-sync-preview-results" aria-label="账号差异预览">
              {generatedStates.map((state) => {
                const account = accountMap.get(state.accountId)
                const site = account ? siteMap.get(account.site_id) : undefined
                if (!account) return null
                return (
                  <article className="site-sync-preview-result" key={state.accountId}>
                    <header>
                      <div><span>{site?.name || `站点 #${account.site_id}`}</span><strong>{account.label}</strong></div>
                      <AccountStateBadge state={state} />
                    </header>
                    {state.preview ? (
                      <ConfigPreviewPanel
                        preview={state.preview}
                        applying={applyingAccount === state.accountId}
                        applyMessage={state.applyMessage}
                        onApply={() => void applyPreview(state.accountId, state.preview!)}
                        onReload={active ? undefined : generatePreviews}
                        reloadLabel="重新生成此批预览"
                        reloading={generating}
                        disableApply={state.phase === 'unknown' || (anyApplying && state.phase !== 'applying')}
                        onRecheck={() => void recheckPreview(state.accountId, state.preview!.id)}
                        rechecking={rechecking.has(state.accountId)}
                      />
                    ) : (
                      <div className={`site-sync-preview-state site-sync-preview-state--${state.phase}`} role="status">
                        {state.phase === 'unknown' ? <AlertTriangle size={16} /> : state.phase === 'cancelled' || state.phase === 'failed' ? <XCircle size={16} /> : state.phase === 'applied' ? <CheckCircle2 size={16} /> : <RefreshCw size={16} className={active ? 'spin' : undefined} />}
                        <span>{state.message || phaseLabel(state.phase)}</span>
                      </div>
                    )}
                  </article>
                )
              })}
            </section>
          )}

          <footer>
            <span>{generating ? `最多同时预览 3 个账号 · 已完成 ${completedCount}/${stateValues.length}` : generatedStates.length ? `已选择 ${selected.size}/${MAX_CONFIG_PREVIEW_ACCOUNTS} 个账号，可逐项确认应用` : `已选择 ${selected.size}/${MAX_CONFIG_PREVIEW_ACCOUNTS} 个账号`}</span>
            <div>
              {generating && <button className="secondary-button" type="button" onClick={cancelGeneration}>取消生成</button>}
              <button className="secondary-button" type="button" onClick={handleClose}>关闭</button>
              <button className="primary-button" type="button" onClick={() => void generatePreviews()} disabled={active || !selected.size}>{generating ? <RefreshCw className="spin" size={15} /> : <RefreshCw size={15} />}{generating ? '生成中' : generatedStates.length ? '重新生成预览' : '生成差异预览'}</button>
            </div>
          </footer>
        </div>
      )}
    </Modal>
  )
}

async function waitForPreviewTask(
  taskId: string,
  signal: AbortSignal,
  observe: (task: SiteTask, signal: AbortSignal) => void,
  timeoutMs = 120_000,
): Promise<SiteTask> {
  const startedAt = Date.now()
  while (Date.now() - startedAt < timeoutMs) {
    const task = await getSiteTask(taskId, signal)
    observe(task, signal)
    if (['success', 'partial', 'failed', 'cancelled', 'unknown'].includes(task.status)) return task
    await abortableDelay(700, signal)
  }
  throw new Error('task_timeout')
}

function abortableDelay(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal.aborted) {
      reject(new DOMException('Aborted', 'AbortError'))
      return
    }
    const timer = window.setTimeout(() => {
      signal.removeEventListener('abort', onAbort)
      resolve()
    }, ms)
    const onAbort = () => {
      window.clearTimeout(timer)
      reject(new DOMException('Aborted', 'AbortError'))
    }
    signal.addEventListener('abort', onAbort, { once: true })
  })
}

function AccountStateBadge({ state }: { state?: AccountPreviewState }) {
  if (!state) return <span className="status-badge status-badge--muted">未预览</span>
  const tone = state.phase === 'ready'
    ? 'success'
    : state.phase === 'applied'
      ? 'success'
      : state.phase === 'failed'
        ? 'danger'
        : state.phase === 'unknown' || state.phase === 'cancelled'
          ? 'warning'
          : 'warning'
  return <span className={`status-badge status-badge--${tone}`}>{phaseLabel(state.phase)}</span>
}

function phaseLabel(phase: AccountPreviewPhase): string {
  const labels: Record<AccountPreviewPhase, string> = {
    starting: '排队中',
    running: '读取中',
    ready: '待确认',
    applying: '应用中',
    applied: '已应用',
    failed: '失败',
    unknown: '结果待确认',
    cancelled: '已取消',
  }
  return labels[phase]
}
