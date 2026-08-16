import { useCallback, useEffect, useState } from 'react'
import { BellRing, CheckCircle2, RefreshCw, Send, Trash2, WalletCards } from 'lucide-react'
import { getWebhookConfig, testWebhook, updateWebhookConfig } from '../api'
import type { WebhookConfig } from '../types'
import { ErrorState, LoadingState, OperationNotice, formatTime } from './shared'
import { siteErrorMessage, StatusBadge } from './siteShared'

type FormState = Pick<WebhookConfig,
  'enabled' | 'low_balance_enabled' | 'low_balance_threshold' | 'checkin_failure_enabled' | 'cooldown_minutes'
>

const defaults: FormState = {
  enabled: false,
  low_balance_enabled: true,
  low_balance_threshold: 10,
  checkin_failure_enabled: true,
  cooldown_minutes: 360,
}

export function WebhookSettingsPanel() {
  const [config, setConfig] = useState<WebhookConfig | null>(null)
  const [form, setForm] = useState<FormState>(defaults)
  const [endpoint, setEndpoint] = useState('')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true); setError('')
    try {
      const data = await getWebhookConfig(signal)
      setConfig(data)
      setForm({
        enabled: data.enabled,
        low_balance_enabled: data.low_balance_enabled,
        low_balance_threshold: data.low_balance_threshold,
        checkin_failure_enabled: data.checkin_failure_enabled,
        cooldown_minutes: data.cooldown_minutes,
      })
    } catch (reason) {
      if (!signal?.aborted) setError(siteErrorMessage(reason))
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  const save = async (event: React.FormEvent) => {
    event.preventDefault(); setSaving(true); setError(''); setNotice('')
    try {
      const data = await updateWebhookConfig({ ...form, ...(endpoint.trim() ? { url: endpoint.trim() } : {}) })
      setConfig(data); setEndpoint(''); setNotice('通知设置已保存')
    } catch (reason) { setError(siteErrorMessage(reason)) }
    finally { setSaving(false) }
  }

  const clearEndpoint = async () => {
    if (!window.confirm('移除当前 Webhook 地址并停用通知？')) return
    setSaving(true); setError(''); setNotice('')
    try {
      const data = await updateWebhookConfig({ ...form, enabled: false, url: '' })
      setConfig(data); setForm((current) => ({ ...current, enabled: false })); setEndpoint(''); setNotice('Webhook 地址已移除')
    } catch (reason) { setError(siteErrorMessage(reason)) }
    finally { setSaving(false) }
  }

  const sendTest = async () => {
    setTesting(true); setError(''); setNotice('')
    try {
      const result = await testWebhook()
      setNotice(`测试通知发送成功${result.attempts > 1 ? `，第 ${result.attempts} 次送达` : ''}`)
      await load()
    } catch (reason) { setError(siteErrorMessage(reason)) }
    finally { setTesting(false) }
  }

  if (loading && !config) return <LoadingState label="正在加载通知设置" />
  if (error && !config) return <ErrorState message={error} retry={() => void load()} />

  return <section className="settings-page">
    {notice && <OperationNotice onDismiss={() => setNotice('')}><CheckCircle2 size={15} />{notice}</OperationNotice>}
    {error && <div className="inline-error">{error}</div>}

    <form className="webhook-settings" onSubmit={save}>
      <header className="settings-section-header">
        <span className="settings-section-icon"><BellRing size={19} /></span>
        <div><h2>通用 Webhook</h2><p>{config?.url_configured ? config.url_masked || '地址已加密保存' : '尚未配置接收地址'}</p></div>
        <div className="settings-header-status"><StatusBadge status={config?.last_delivery_status} /><Toggle checked={form.enabled} setChecked={(value) => setForm((current) => ({ ...current, enabled: value }))} label="启用 Webhook" /></div>
      </header>

      <div className="webhook-endpoint-row">
        <label><span>接收地址</span><input type="url" value={endpoint} onChange={(event) => setEndpoint(event.target.value)} placeholder="https://hooks.example.com/..." /></label>
        {config?.url_configured && <button className="icon-button icon-button--surface danger-button" type="button" onClick={() => void clearEndpoint()} disabled={saving} aria-label="移除 Webhook 地址" title="移除地址"><Trash2 size={16} /></button>}
      </div>

      <div className="notification-rules">
        <section className="notification-rule">
          <span className="rule-icon"><WalletCards size={18} /></span>
          <div><strong>余额低</strong><small>余额刷新成功后检测</small></div>
          <label className="rule-number"><span>阈值</span><input type="number" min="0" step="0.01" value={form.low_balance_threshold} onChange={(event) => setForm((current) => ({ ...current, low_balance_threshold: Number(event.target.value) }))} /></label>
          <Toggle checked={form.low_balance_enabled} setChecked={(value) => setForm((current) => ({ ...current, low_balance_enabled: value }))} label="余额低通知" />
        </section>
        <section className="notification-rule">
          <span className="rule-icon rule-icon--danger"><BellRing size={18} /></span>
          <div><strong>签到失败</strong><small>仅 failed，排除不支持和浏览器验证</small></div>
          <span className="rule-spacer" />
          <Toggle checked={form.checkin_failure_enabled} setChecked={(value) => setForm((current) => ({ ...current, checkin_failure_enabled: value }))} label="签到失败通知" />
        </section>
      </div>

      <footer className="webhook-settings-footer">
        <div className="delivery-state">
          <span>通知冷却</span><select value={form.cooldown_minutes} onChange={(event) => setForm((current) => ({ ...current, cooldown_minutes: Number(event.target.value) }))}><option value={30}>30 分钟</option><option value={60}>1 小时</option><option value={360}>6 小时</option><option value={720}>12 小时</option><option value={1440}>24 小时</option></select>
          <span className="delivery-last">{config?.last_delivery_at ? `最近发送 ${formatTime(config.last_delivery_at)}` : '尚无发送记录'}{config?.last_error ? ` · ${config.last_error}` : ''}</span>
        </div>
        <div><button className="secondary-button" type="button" onClick={() => void sendTest()} disabled={!config?.url_configured || testing}><Send size={15} />{testing ? '发送中' : '测试'}</button><button className="primary-button" type="submit" disabled={saving}>{saving ? <RefreshCw className="spin" size={15} /> : null}{saving ? '保存中' : '保存设置'}</button></div>
      </footer>
	</form>
	</section>
}

export default function SettingsPage() {
	return <div className="workspace-page"><header className="page-header"><h1>系统设置</h1></header><WebhookSettingsPanel /></div>
}

function Toggle({ checked, setChecked, label }: { checked: boolean; setChecked: (value: boolean) => void; label: string }) {
  return <button className={`switch${checked ? ' switch--on' : ''}`} type="button" role="switch" aria-checked={checked} aria-label={label} title={label} onClick={() => setChecked(!checked)}><i /></button>
}
