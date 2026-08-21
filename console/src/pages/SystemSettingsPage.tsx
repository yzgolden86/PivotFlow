import { useCallback, useEffect, useMemo, useState } from 'react'
import { BellRing, CheckCircle2, RefreshCw, RotateCcw, Save, Search, SlidersHorizontal } from 'lucide-react'
import { getSystemSettings, resetSystemSetting, updateSystemSettings } from '../api'
import type { SystemSetting } from '../types'
import { EmptyState, ErrorState, LoadingState, OperationNotice } from './shared'
import { WebhookSettingsPanel } from './SettingsPage'

const groups = [
  ['routing', '路由与重试'], ['cooldown', '冷却规则'], ['limits', '并发与限制'],
  ['health', '健康检测'], ['logs', '日志与巡检'], ['websocket', 'WebSocket'], ['updates', '更新与目录'], ['advanced', '高级设置'], ['other', '其他'],
] as const

export default function SystemSettingsPage() {
	const [section, setSection] = useState<'runtime' | 'notifications'>('runtime')
  const [settings, setSettings] = useState<SystemSetting[]>([])
  const [values, setValues] = useState<Record<string, string>>({})
  const [dirty, setDirty] = useState<Set<string>>(new Set())
  const [query, setQuery] = useState('')
  const [group, setGroup] = useState('routing')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true); setError('')
    try {
      const data = await getSystemSettings(signal)
      setSettings(data)
      setValues(Object.fromEntries(data.map((setting) => [setting.key, setting.value])))
      setDirty(new Set())
	} catch (reason) { if (!signal?.aborted) setError(reason instanceof Error ? reason.message : '系统设置加载失败') }
    finally { if (!signal?.aborted) setLoading(false) }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  const counts = useMemo(() => Object.fromEntries(groups.map(([key]) => [key, settings.filter((item) => settingGroup(item.key) === key).length])), [settings])
  const visible = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    return settings.filter((setting) => (group === 'all' || settingGroup(setting.key) === group) && (!normalized || `${setting.key} ${setting.description}`.toLowerCase().includes(normalized)))
  }, [group, query, settings])

  const change = (key: string, value: string) => {
    setValues((current) => ({ ...current, [key]: value }))
    setDirty((current) => { const next = new Set(current); const original = settings.find((item) => item.key === key)?.value; if (value === original) next.delete(key); else next.add(key); return next })
  }

  const save = async () => {
    if (!dirty.size) return
    const restartRequired = Array.from(dirty).some((key) => settings.find((item) => item.key === key)?.requires_restart)
    const consequence = restartRequired ? '其中包含需要重启的设置，服务会短暂中断请求。' : '这些设置保存后立即生效。'
    if (!window.confirm(`保存 ${dirty.size} 项系统参数？${consequence}`)) return
    setSaving(true); setError(''); setNotice('')
    try {
      const updates = Object.fromEntries(Array.from(dirty).map((key) => [key, values[key]]))
      const result = await updateSystemSettings(updates)
      setSettings((current) => current.map((setting) => Object.prototype.hasOwnProperty.call(updates, setting.key) ? { ...setting, value: updates[setting.key] } : setting))
      setNotice(result.message)
      setDirty(new Set())
    } catch (reason) { setError(reason instanceof Error ? reason.message : '保存失败') }
    finally { setSaving(false) }
  }

  const reset = async (setting: SystemSetting) => {
    const consequence = setting.requires_restart ? '服务会自动重启。' : '修改会立即生效。'
    if (!window.confirm(`将“${setting.description || setting.key}”恢复为默认值？${consequence}`)) return
    setSaving(true); setError(''); setNotice('')
    try { const result = await resetSystemSetting(setting.key); change(setting.key, setting.default_value); setSettings((current) => current.map((item) => item.key === setting.key ? { ...item, value: setting.default_value } : item)); setDirty(new Set()); setNotice(result.message) }
    catch (reason) { setError(reason instanceof Error ? reason.message : '重置失败') }
    finally { setSaving(false) }
  }

  if (loading && !settings.length) return <div className="workspace-page"><LoadingState label="正在加载系统设置" /></div>
  if (error && !settings.length) return <div className="workspace-page"><ErrorState message={error} retry={() => void load()} /></div>

  return <div className="workspace-page system-settings-page">
	<header className="page-header"><h1>系统设置</h1>{section === 'runtime' && <div className="header-controls"><button className="primary-button" type="button" onClick={() => void save()} disabled={!dirty.size || saving}><Save size={15} />{saving ? '保存中' : `保存${dirty.size ? ` (${dirty.size})` : ''}`}</button><button className="icon-button icon-button--surface" type="button" onClick={() => void load()} aria-label="刷新系统设置"><RefreshCw size={17} /></button></div>}</header>
	<div className="view-tabs" role="tablist" aria-label="系统设置分类"><button type="button" role="tab" aria-selected={section === 'runtime'} className={section === 'runtime' ? 'is-active' : ''} onClick={() => setSection('runtime')}><SlidersHorizontal size={15} />运行参数</button><button type="button" role="tab" aria-selected={section === 'notifications'} className={section === 'notifications' ? 'is-active' : ''} onClick={() => setSection('notifications')}><BellRing size={15} />通知</button></div>
	{section === 'notifications' ? <WebhookSettingsPanel /> : <>
	{notice && <OperationNotice onDismiss={() => setNotice('')}><CheckCircle2 size={15} />{notice}</OperationNotice>}
	{error && <OperationNotice tone="error">{error}</OperationNotice>}
	<div className="settings-layout">
      <aside className="settings-groups" aria-label="参数分类">
        {groups.map(([key, label]) => <button className={group === key ? 'is-active' : ''} type="button" onClick={() => setGroup(key)} key={key}><span>{label}</span><strong>{counts[key] || 0}</strong></button>)}
      </aside>
      <section className="settings-editor">
        <label className="search-field settings-search"><Search size={16} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索参数名称" /></label>
        {!visible.length ? <EmptyState label="没有符合条件的系统参数" /> : <div className="setting-list">{visible.map((setting) => <article className={`setting-row${dirty.has(setting.key) ? ' setting-row--dirty' : ''}`} key={setting.key}>
          <div><strong>{settingLabel(setting)}</strong><code>{setting.key}</code></div>
          <SettingInput setting={setting} value={values[setting.key] ?? ''} change={(value) => change(setting.key, value)} />
          <span className="setting-default">默认 {setting.default_value || '空'}</span>
          <button className="icon-button icon-button--surface" type="button" onClick={() => void reset(setting)} disabled={!setting.editable || saving || values[setting.key] === setting.default_value} aria-label={`重置 ${setting.key}`} title="恢复默认值"><RotateCcw size={15} /></button>
        </article>)}</div>}
      </section>
	</div></>}
  </div>
}

function SettingInput({ setting, value, change }: { setting: SystemSetting; value: string; change: (value: string) => void }) {
  if (setting.value_type === 'bool') return <select value={normalizeBool(value)} onChange={(event) => change(event.target.value)} disabled={!setting.editable}><option value="true">启用</option><option value="false">停用</option></select>
  if (setting.value_type === 'json') return <textarea rows={3} value={value} onChange={(event) => change(event.target.value)} disabled={!setting.editable} />
  const type = ['int', 'float', 'duration'].includes(setting.value_type) ? 'number' : 'text'
  return <input type={type} step={setting.value_type === 'float' ? '0.1' : '1'} value={value} onChange={(event) => change(event.target.value)} disabled={!setting.editable} />
}

function normalizeBool(value: string): string { return value === 'true' || value === '1' ? 'true' : 'false' }
function settingGroup(key: string): string {
  if (/^channel_(test_content|check_interval_hours)$/.test(key)) return 'health'
  if (/cooldown|retry|overload|rate_limit/.test(key)) return key.includes('cooldown') ? 'cooldown' : 'routing'
  if (/concurr|limit|max_body|max_image|max_key/.test(key)) return 'limits'
  if (/log|check_interval|debug/.test(key)) return 'logs'
  if (/websocket|responses_ws/.test(key)) return 'websocket'
  if (/update|catalog/.test(key)) return 'updates'
  if (/antigravity|model_fuzzy|health_|ttfb_|auto_refresh|log_channel_click_action/.test(key)) return 'advanced'
  if (/timeout|proxy|priority|strategy/.test(key)) return 'routing'
  return 'other'
}

function settingLabel(setting: SystemSetting): string {
  if (setting.key === 'channel_test_content') return '健康检测提示词'
  if (setting.key === 'channel_check_interval_hours') return '渠道健康检测间隔'
  return setting.description || setting.key
}
