import { useCallback, useEffect, useRef, useState } from 'react'
import { AlertCircle, CheckCircle2, Clock3, KeyRound, Play, Power, RefreshCw, ShieldCheck, Square, Trash2 } from 'lucide-react'
import { deleteChannelKey, getChannelKeyHealth, setChannelKeyEnabled, testChannel } from '../api'
import HelpTip from '../components/HelpTip'
import type { ChannelKeyHealthItem, ChannelKeyHealthSnapshot } from '../types'
import { EmptyState, ErrorState, LoadingState } from './shared'
import { Modal, siteErrorMessage } from './siteShared'
import { keyHealthLabel, keyNeedsAttention, matchesKeyFilter, mergeKeyHealth } from './channelKeyHealth'

export default function ChannelKeysModal({ channelId, close, changed }: { channelId: number; close: () => void; changed: () => void }) {
  const [snapshot, setSnapshot] = useState<ChannelKeyHealthSnapshot | null>(null)
  const [filter, setFilter] = useState('all')
  const [model, setModel] = useState('')
  const [protocol, setProtocol] = useState('openai')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [checking, setChecking] = useState<number | null>(null)
  const [progress, setProgress] = useState({ done: 0, total: 0 })
  const locked = useRef(false)
  const mounted = useRef(true)
  const stop = useRef(false)
  const request = useRef<AbortController | null>(null)
  const bodyRef = useRef<HTMLDivElement>(null)

  const refresh = useCallback(async (signal?: AbortSignal) => {
    const next = await getChannelKeyHealth(channelId, signal)
    if (!mounted.current || signal?.aborted) return
    setSnapshot((previous) => mergeKeyHealth(next, previous))
    setModel((current) => next.models.includes(current) ? current : next.models[0] || '')
  }, [channelId])

  useEffect(() => {
    mounted.current = true
    const controller = new AbortController()
    void refresh(controller.signal).catch((reason) => {
      if (!controller.signal.aborted) setError(siteErrorMessage(reason))
    }).finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => { mounted.current = false; stop.current = true; controller.abort(); request.current?.abort() }
  }, [refresh])

  // Keep keyboard navigation inside this dialog and restore the entry focus.
  useEffect(() => {
    const opener = document.activeElement as HTMLElement | null
    const dialog = bodyRef.current?.closest<HTMLElement>('[role="dialog"]')
    dialog?.querySelector<HTMLElement>('button')?.focus()
    const trapFocus = (event: KeyboardEvent) => {
      if (event.key !== 'Tab' || !dialog) return
      const items = [...dialog.querySelectorAll<HTMLElement>('button:not(:disabled), select:not(:disabled), [tabindex="0"]')].filter((item) => item.getClientRects().length > 0)
      const first = items[0], last = items[items.length - 1]
      if (event.shiftKey && (document.activeElement === first || !dialog.contains(document.activeElement))) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && (document.activeElement === last || !dialog.contains(document.activeElement))) { event.preventDefault(); first?.focus() }
    }
    document.addEventListener('keydown', trapFocus)
    return () => { document.removeEventListener('keydown', trapFocus); opener?.focus() }
  }, [])

  const reload = async () => {
    if (locked.current) return
    locked.current = true; setBusy(true); setError('')
    try { await refresh() } catch (reason) { if (mounted.current) setError(siteErrorMessage(reason)) }
    finally { locked.current = false; if (mounted.current) { setLoading(false); setBusy(false) } }
  }

  const runChecks = async (targets: ChannelKeyHealthItem[]) => {
    if (locked.current || !model || !targets.length) return
    if (targets.length > 1 && !window.confirm(`将使用 ${model} 逐个复检 ${targets.length} 个 Key，可能产生少量模型费用。继续吗？`)) return
    locked.current = true; stop.current = false
    setBusy(true); setError(''); setNotice(''); setProgress({ done: 0, total: targets.length })
    let done = 0, healthy = 0, unobserved = 0
    try {
      for (const key of targets) {
        if (stop.current || !mounted.current) break
        setChecking(key.id)
        const controller = new AbortController()
        request.current = controller
        const result = await testChannel(channelId, { model, client_protocol: protocol, stream: false, content: 'Reply only OK.', max_tokens: 64, key_index: key.key_index, key_id: key.id }, controller.signal)
        if (!mounted.current) break
        const health = result.key_health
        if (health) {
          if (health.status === 'healthy') healthy += 1
          setSnapshot((current) => current && ({ ...current, keys: current.keys.map((item) => item.id === key.id && health.checked_at >= item.health.checked_at ? { ...item, health } : item) }))
        } else unobserved += 1
        done += 1
        setProgress({ done, total: targets.length })
      }
      if (mounted.current) {
        setNotice(`${stop.current ? '已停止后续复检。' : '复检结束。'}已检查 ${done} 个，正常 ${healthy} 个，需关注 ${done - healthy - unobserved} 个。${unobserved ? `另有 ${unobserved} 个因本地限制或配置问题未取得检测结果，保留原状态。` : ''}`)
        await refresh()
        changed()
      }
    } catch (reason) {
      if (mounted.current) setError(`复检已暂停：${siteErrorMessage(reason)}。已完成的结果已保留，请刷新后重试。`)
    } finally {
      request.current = null; locked.current = false
      if (mounted.current) { setBusy(false); setChecking(null) }
    }
  }

  const manage = async (key: ChannelKeyHealthItem, remove: boolean) => {
    if (locked.current) return
    const action = remove ? '删除' : key.disabled ? '启用' : '停用'
    const consequence = remove ? '删除后无法撤销。' : key.disabled ? '启用后可参与路由，但不会清除检测结果或剩余冷却。' : '停用后不再参与路由，可随时恢复。'
    const lastKey = remove && snapshot?.keys.length === 1 ? '这是最后一个 Key，删除后此渠道将无法提供服务。' : ''
    if (!window.confirm(`确定${action} Key #${key.key_index + 1}（${key.masked_key}）？${consequence}${lastKey}`)) return
    locked.current = true; setBusy(true); setError(''); setNotice('')
    try {
      if (remove) await deleteChannelKey(channelId, key.key_index, key.id)
      else await setChannelKeyEnabled(channelId, key.key_index, key.id, key.disabled)
      if (mounted.current) { setNotice(`已${action} Key #${key.key_index + 1}`); await refresh(); changed() }
    } catch (reason) { if (mounted.current) setError(siteErrorMessage(reason)) }
    finally { locked.current = false; if (mounted.current) setBusy(false) }
  }

  const keys = snapshot?.keys || []
  const visible = keys.filter((key) => matchesKeyFilter(key, filter))
  const targets = visible.filter((key) => !key.disabled).slice(0, 20)
  const filters = [
    { id: 'all', label: '全部 Key', count: keys.length },
    { id: 'attention', label: '需关注', count: keys.filter(keyNeedsAttention).length },
    { id: 'unknown', label: '未检测', count: keys.filter((key) => !key.health.checked_at).length },
    { id: 'disabled', label: '已停用', count: keys.filter((key) => key.disabled).length },
  ]

  return <Modal title={`Key 健康管理${snapshot ? ` · ${snapshot.channel_name}` : ''}`} close={close} wide>
    <div className="key-health-panel" ref={bodyRef}>
      <div className="key-health-intro"><span className="key-health-emblem"><ShieldCheck size={23} aria-hidden="true" /></span><div className="heading-with-hint"><strong>找到需要处理的 Key</strong><HelpTip label="Key 健康状态" text="正常调用会自动更新状态；未使用的 Key 可手动复检。检测结果与停用、冷却分别显示。" /></div><button type="button" className="icon-button icon-button--surface" aria-label="刷新 Key 状态" title="只读取状态，不发起模型调用" disabled={busy} onClick={() => void reload()}><RefreshCw size={16} /></button></div>
      {error && <div className="key-health-message key-health-message--error" role="alert"><AlertCircle size={16} /><span>{error}</span></div>}
      {notice && <div className="key-health-message" role="status"><CheckCircle2 size={16} /><span>{notice}</span></div>}
      {loading ? <LoadingState label="正在读取 Key 状态" /> : !snapshot ? <ErrorState message="Key 状态暂时不可用" retry={() => void reload()} /> : <>
        <div className="key-health-summary" role="group" aria-label="按 Key 状态筛选">{filters.map((item) => <button type="button" key={item.id} aria-pressed={filter === item.id} className={`${filter === item.id ? 'is-active' : ''} ${item.id === 'attention' && item.count ? 'has-issues' : ''}`} onClick={() => setFilter(item.id)} disabled={busy}><span>{item.label}</span><strong>{item.count}</strong></button>)}</div>
        <div className="key-health-probe">
          <div className="key-health-probe-controls"><label>复检模型<select aria-label="复检模型" value={model} disabled={busy} onChange={(event) => setModel(event.target.value)}>{!snapshot.models.length && <option value="">请先为渠道配置模型</option>}{snapshot.models.map((name) => <option value={name} key={name}>{name}</option>)}</select></label><label>请求协议<select aria-label="复检请求协议" value={protocol} disabled={busy} onChange={(event) => setProtocol(event.target.value)}><option value="openai">OpenAI</option><option value="anthropic">Anthropic</option><option value="gemini">Gemini</option><option value="codex">Codex</option></select></label><button type="button" className="primary-button" disabled={busy || !model || !targets.length} onClick={() => void runChecks(targets)}><Play size={14} />复检当前筛选 ({targets.length})</button></div>
          <p><Clock3 size={13} aria-hidden="true" />会实际调用所选模型，可能产生费用。批量每次最多 20 个，逐个执行并跳过已停用 Key；已停用 Key 仍可单独复检。</p>
          {checking !== null && <div className="key-health-progress" role="status" aria-live="polite"><RefreshCw size={14} className="spin" /><span>正在复检 · 已完成 {progress.done}/{progress.total}</span><button className="secondary-button" type="button" onClick={() => { stop.current = true; setNotice('将完成当前请求，然后停止后续复检。') }}><Square size={12} />停止后续</button></div>}
        </div>
        {visible.length === 0 ? <EmptyState label={keys.length ? '此筛选下没有 Key' : '此渠道尚未配置 Key，请在渠道编辑中添加'} /> : <div className="key-health-list">{visible.map((key) => {
          const status = keyHealthLabel(key)
          const cooling = key.cooldown_until * 1000 > Date.now()
          const scopeLabel = key.model_scope_empty ? '不参与路由' : key.allowed_models?.length ? `限定 ${key.allowed_models.length} 个模型` : '全部模型'
          const multiplier = key.cost_multiplier ?? 1
          return <article className={`key-health-card${key.disabled ? ' is-disabled' : ''}`} key={key.id} aria-label={`Key #${key.key_index + 1}`}>
            <div className="key-health-card-main"><span className="key-health-key-icon"><KeyRound size={17} aria-hidden="true" /></span><div className="key-health-identity"><strong>Key #{key.key_index + 1}<code>{key.masked_key}</code></strong><span title={key.note}>{key.note || '未添加备注'}</span></div><div className="key-health-badges"><span className={`status-badge status-badge--${status.tone}`}>{status.label}</span><span className="status-badge status-badge--muted">{scopeLabel}</span><span className="status-badge status-badge--muted">倍率 ×{multiplier}</span>{key.disabled && <span className="status-badge status-badge--muted">已停用</span>}{cooling && <span className="status-badge status-badge--warning" title={`冷却至 ${new Date(key.cooldown_until * 1000).toLocaleString()}`}>冷却中</span>}</div></div>
            <p className="key-health-reason">{key.health.reason || '尚无检测记录，不代表可用或失效。可点击复检，或等待实际请求。'}</p>
            <footer><span className="key-health-time">{key.health.checked_at ? <>最近检测 <time dateTime={new Date(key.health.checked_at).toISOString()}>{new Date(key.health.checked_at).toLocaleString()}</time>{key.health.status_code > 0 && <small>状态码 {key.health.status_code}</small>}</> : '最近检测 —'}</span><div className="key-health-actions"><button className="secondary-button" type="button" disabled={busy || !model} aria-label={`复检 Key #${key.key_index + 1}`} onClick={() => void runChecks([key])}><RefreshCw size={13} className={checking === key.id ? 'spin' : ''} />复检</button><button className="secondary-button" type="button" disabled={busy} aria-label={`${key.disabled ? '启用' : '停用'} Key #${key.key_index + 1}`} onClick={() => void manage(key, false)}><Power size={13} />{key.disabled ? '启用' : '停用'}</button><button className="icon-button icon-button--surface danger-button" type="button" disabled={busy} title="删除此 Key" aria-label={`删除 Key #${key.key_index + 1}`} onClick={() => void manage(key, true)}><Trash2 size={15} /></button></div></footer>
          </article>
        })}</div>}
        <p className="key-health-footnote">仅代表最近一次请求结果，不查询精确余额，也不保证所有模型可用。限流、权限或网络问题不等于 Key 失效；冷却结束不会自动变为正常，复检成功后才更新。</p>
      </>}
    </div>
  </Modal>
}
