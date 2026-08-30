import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Activity, BellRing, CalendarClock, Check, CheckCircle2, ChevronRight, Clock3, ExternalLink, FileClock, Gauge, Network,
  DatabaseBackup, RefreshCw, RotateCcw, Route, Save, Search, ShieldAlert, Sun, Moon, Monitor,
  KeyRound, Palette, PanelsTopLeft, Pencil, Play, Plus, Power, SlidersHorizontal, TimerReset, Trash2, Type, Wrench,
} from 'lucide-react'
import { checkForUpdates, createSystemAccessToken, deleteSystemAccessToken, getModelAliasInventory, getSystemAccessTokens, getSystemSettings, resetSystemSetting, updateSystemAccessToken, updateSystemSettings } from '../api'
import type { ModelAliasCandidate, ModelAliasInventory, ModelAliasSuggestion, SystemAccessToken, SystemSetting } from '../types'
import { EmptyState, ErrorState, LoadingState, OperationNotice } from './shared'
import { WebhookSettingsPanel } from './SettingsPage'
import { BackupSettingsPanel } from './BackupSettingsPanel'
import { Modal } from './siteShared'
import { applyThemeCustomization, readThemeCustomization, resetThemeCustomization } from '../theme'
import type { ThemeCustomization, ThemeFont, ThemePreference, ThemePreset, ThemeRadius } from '../theme'

const groups = [
  { key: 'appearance', label: '外观', description: '主题与显示偏好', icon: Sun },
  { key: 'automation', label: '自动任务', description: '签到与后台任务时间', icon: CalendarClock },
  { key: 'routing', label: '路由策略', description: '重试、兜底与模型匹配', icon: Route },
  { key: 'timeouts', label: '请求超时', description: '连接、首字与总时限', icon: Clock3 },
  { key: 'cooldown', label: '故障冷却', description: '异常退避与恢复节奏', icon: TimerReset },
  { key: 'capacity', label: '并发容量', description: '并发数与请求体限制', icon: Gauge },
  { key: 'health', label: '健康检测', description: '巡检、排序与模型目录', icon: Activity },
  { key: 'logs', label: '日志界面', description: '保留周期与页面行为', icon: FileClock },
  { key: 'websocket', label: '长连接', description: '会话数量与资源上限', icon: Network },
  { key: 'maintenance', label: '更新维护', description: '版本检查与目录同步', icon: Wrench },
  { key: 'advanced', label: '高级兼容', description: '仅在明确需要时调整', icon: ShieldAlert },
] as const

type GroupKey = typeof groups[number]['key']

export default function SystemSettingsPageV2() {
  const [section, setSection] = useState<'runtime' | 'notifications' | 'backup' | 'system-access'>('runtime')
  const [settings, setSettings] = useState<SystemSetting[]>([])
  const [values, setValues] = useState<Record<string, string>>({})
  const [dirty, setDirty] = useState<Set<string>>(new Set())
  const [query, setQuery] = useState('')
  const [group, setGroup] = useState<GroupKey>('routing')
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [appearance, setAppearance] = useState<ThemeCustomization>(readThemeCustomization)
  const [versionInfo, setVersionInfo] = useState<{ version: string; latest_version?: string; has_update?: boolean; release_url?: string; last_check?: string; message?: string; error?: string } | null>(null)
  const [checkingVersion, setCheckingVersion] = useState(false)

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true)
    setError('')
    try {
      const data = await getSystemSettings(signal)
      setSettings(data)
      setValues(Object.fromEntries(data.map((setting) => [setting.key, setting.value])))
      setDirty(new Set())
    } catch (reason) {
      if (!signal?.aborted) setError(reason instanceof Error ? reason.message : '系统设置加载失败')
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])

  useEffect(() => {
    const update = () => setAppearance(readThemeCustomization())
    window.addEventListener('fusion:theme-changed', update)
    return () => window.removeEventListener('fusion:theme-changed', update)
  }, [])

  const counts = useMemo(() => Object.fromEntries(groups.map(({ key }) => [key, settings.filter((item) => settingGroup(item.key) === key).length])), [settings])
  const activeGroup = groups.find((item) => item.key === group) || groups[0]
  const visible = useMemo(() => {
    const normalized = query.trim().toLowerCase()
    return settings.filter((setting) => {
      if (setting.key === 'model_alias_groups') return false
      const searchable = `${settingLabel(setting)} ${settingHelp(setting)} ${setting.key} ${setting.description}`.toLowerCase()
      return normalized ? searchable.includes(normalized) : settingGroup(setting.key) === group
    })
  }, [group, query, settings])

  const change = (key: string, value: string) => {
    setValues((current) => ({ ...current, [key]: value }))
    setDirty((current) => {
      const next = new Set(current)
      const original = settings.find((item) => item.key === key)?.value
      if (value === original) next.delete(key)
      else next.add(key)
      return next
    })
  }

  const save = async () => {
    if (!dirty.size) return
    if (!window.confirm(`保存 ${dirty.size} 项设置？`)) return
    setSaving(true)
    setError('')
    setNotice('')
    try {
      const updates = Object.fromEntries(Array.from(dirty).map((key) => [key, values[key]]))
      const result = await updateSystemSettings(updates)
      setSettings((current) => current.map((setting) => Object.prototype.hasOwnProperty.call(updates, setting.key) ? { ...setting, value: updates[setting.key] } : setting))
      setNotice(result.message)
      setDirty(new Set())
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const reset = async (setting: SystemSetting) => {
    if (!window.confirm(`将“${settingLabel(setting)}”恢复为默认值？`)) return
    setSaving(true)
    setError('')
    setNotice('')
    try {
      const result = await resetSystemSetting(setting.key)
      setValues((current) => ({ ...current, [setting.key]: setting.default_value }))
      setSettings((current) => current.map((item) => item.key === setting.key ? { ...item, value: setting.default_value } : item))
      setDirty(new Set())
      setNotice(result.message)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '重置失败')
    } finally {
      setSaving(false)
    }
  }

  const changeAppearance = (patch: Partial<ThemeCustomization>, label: string) => {
    const next = { ...appearance, ...patch }
    setAppearance(next)
    applyThemeCustomization(next)
    window.dispatchEvent(new CustomEvent('fusion:theme-changed', { detail: next.preference }))
    setNotice(`已应用${label}`)
  }

  const resetAppearance = () => {
    const next = resetThemeCustomization()
    setAppearance(next)
    window.dispatchEvent(new CustomEvent('fusion:theme-changed', { detail: next.preference }))
    setNotice('外观已恢复默认')
  }

  const checkUpdates = async () => {
    setCheckingVersion(true)
    setError('')
    try {
      const result = await checkForUpdates()
      setVersionInfo(result)
      setNotice(result.error ? '上游版本检查未完成' : '已完成 PivotFlow 上游版本检查')
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '上游版本检查失败')
    } finally {
      setCheckingVersion(false)
    }
  }

  if (loading && !settings.length) return <div className="workspace-page"><LoadingState label="正在加载系统设置" /></div>
  if (error && !settings.length) return <div className="workspace-page"><ErrorState message={error} retry={() => void load()} /></div>

  const ActiveGroupIcon = activeGroup.icon

  return <div className="workspace-page system-settings-page system-settings-page--v2">
    <header className="page-header">
      <h1>系统设置</h1>
      {section === 'runtime' && group !== 'appearance' && <div className="header-controls">
        <button className="primary-button" type="button" onClick={() => void save()} disabled={!dirty.size || saving}><Save size={17} />{saving ? '保存中' : `保存${dirty.size ? ` (${dirty.size})` : ''}`}</button>
        <button className="icon-button icon-button--surface" type="button" onClick={() => void load()} aria-label="刷新系统设置" title="刷新"><RefreshCw size={18} /></button>
      </div>}
    </header>

    <div className="view-tabs system-settings-tabs" role="tablist" aria-label="系统设置分类">
      <button type="button" role="tab" aria-selected={section === 'runtime'} className={section === 'runtime' ? 'is-active' : ''} onClick={() => setSection('runtime')}><SlidersHorizontal size={17} />运行设置</button>
      <button type="button" role="tab" aria-selected={section === 'notifications'} className={section === 'notifications' ? 'is-active' : ''} onClick={() => setSection('notifications')}><BellRing size={17} />通知设置</button>
      <button type="button" role="tab" aria-selected={section === 'backup'} className={section === 'backup' ? 'is-active' : ''} onClick={() => setSection('backup')}><DatabaseBackup size={17} />导入导出</button>
      <button type="button" role="tab" aria-selected={section === 'system-access'} className={section === 'system-access' ? 'is-active' : ''} onClick={() => setSection('system-access')}><KeyRound size={17} />系统访问</button>
    </div>

    {section === 'notifications' ? <WebhookSettingsPanel /> : section === 'backup' ? <BackupSettingsPanel /> : section === 'system-access' ? <SystemAccessTokensPanel /> : <>
      {notice && <OperationNotice onDismiss={() => setNotice('')}><CheckCircle2 size={16} />{notice}</OperationNotice>}
      {error && <OperationNotice tone="error">{error}</OperationNotice>}
      <div className="settings-layout">
        <aside className="settings-groups" aria-label="设置分类">
          {groups.map(({ key, label, description, icon: Icon }) => <button className={group === key && !query.trim() ? 'is-active' : ''} type="button" onClick={() => { setGroup(key); setQuery('') }} key={key}>
            <span className="settings-group-icon"><Icon size={18} /></span>
            <span className="settings-group-copy"><strong>{label}</strong><small>{description}</small></span>
            <em>{counts[key] || 0}</em>
          </button>)}
        </aside>

        <section className="settings-editor">
          <div className="settings-editor-toolbar">
            <div className="settings-editor-heading">
              <span><ActiveGroupIcon size={19} /></span>
              <div><h2>{query.trim() ? '搜索结果' : activeGroup.label}</h2><p>{query.trim() ? `在全部设置中查找“${query.trim()}”` : activeGroup.description}</p></div>
            </div>
            <label className="search-field settings-search"><Search size={17} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索全部设置" /></label>
          </div>

          {group === 'appearance' && !query.trim() ? <AppearancePanel customization={appearance} change={changeAppearance} reset={resetAppearance} /> : <>
          {group === 'routing' && !query.trim() && <ModelAliasPanel value={values.model_alias_groups || '[]'} change={(value) => change('model_alias_groups', value)} />}
          {group === 'advanced' && !query.trim() && <div className="settings-risk-note"><ShieldAlert size={17} /><span>这些设置用于特殊兼容场景。不了解具体影响时请保持默认值。</span></div>}

          {!visible.length ? <EmptyState label="没有符合条件的设置" /> : <div className="setting-list">
            {visible.map((setting) => <article className={`setting-row${dirty.has(setting.key) ? ' setting-row--dirty' : ''}`} key={setting.key}>
              <div className="setting-copy"><strong>{settingLabel(setting)}</strong><p>{settingHelp(setting)}</p><code>{setting.key}</code></div>
              <div className="setting-control">
                <SettingInput setting={setting} value={values[setting.key] ?? ''} change={(value) => change(setting.key, value)} />
                {setting.value_type !== 'bool' && settingUnit(setting.key) && <span>{settingUnit(setting.key)}</span>}
              </div>
              <div className="setting-actions">
                <div className="setting-activation">
                  <small>默认值：{formatDefault(setting)}</small>
                </div>
                <button className="icon-button icon-button--surface" type="button" onClick={() => void reset(setting)} disabled={!setting.editable || saving || values[setting.key] === setting.default_value} aria-label={`重置 ${settingLabel(setting)}`} title="恢复默认值"><RotateCcw size={16} /></button>
              </div>
            </article>)}
          </div>}
          {group === 'maintenance' && !query.trim() && <UpdatePanel info={versionInfo} checking={checkingVersion} check={checkUpdates} />}
          </>}
        </section>
      </div>
    </>}
  </div>
}

function formatSystemTokenDate(value?: number): string {
  if (!value) return '未使用'
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(new Date(value))
}

const systemAccessScopeLabels: Record<string, string> = {
  'channels.read': '渠道状态',
  'routes.read': '路由诊断',
  'logs.read': '错误日志',
  'metrics.read': '运行指标',
}

function SystemAccessTokensPanel() {
  const [tokens, setTokens] = useState<SystemAccessToken[]>([])
  const [scopes, setScopes] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [editing, setEditing] = useState<SystemAccessToken | 'new' | null>(null)
  const [createdSecret, setCreatedSecret] = useState('')
  const [copyingSecret, setCopyingSecret] = useState(false)

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true); setError('')
    try { const data = await getSystemAccessTokens(signal); setTokens(data.tokens || []); setScopes(data.scopes || []) }
    catch (reason) { if (!signal?.aborted) setError(reason instanceof Error ? reason.message : '系统访问令牌加载失败') }
    finally { if (!signal?.aborted) setLoading(false) }
  }, [])

  useEffect(() => { const controller = new AbortController(); void load(controller.signal); return () => controller.abort() }, [load])

  const save = async (value: { description: string; scopes: string[]; expires_at: number; is_active: boolean }) => {
    setSaving(true); setError(''); setNotice('')
    try {
      if (editing === 'new') {
        const created = await createSystemAccessToken(value); setTokens((current) => [created, ...current]); setCreatedSecret(created.token || ''); setNotice('系统访问令牌已创建')
      } else if (editing) {
        const updated = await updateSystemAccessToken(editing.id, value); setTokens((current) => current.map((item) => item.id === updated.id ? { ...item, ...updated } : item)); setNotice('系统访问令牌已更新')
      }
      setEditing(null)
    } catch (reason) { setError(reason instanceof Error ? reason.message : '保存系统访问令牌失败') }
    finally { setSaving(false) }
  }

  const toggle = async (token: SystemAccessToken) => {
    try { const updated = await updateSystemAccessToken(token.id, { is_active: !token.is_active }); setTokens((current) => current.map((item) => item.id === updated.id ? { ...item, ...updated } : item)); setNotice(updated.is_active ? '令牌已启用' : '令牌已停用') }
    catch (reason) { setError(reason instanceof Error ? reason.message : '更新令牌状态失败') }
  }

  const remove = async (token: SystemAccessToken) => {
    if (!window.confirm(`删除系统访问令牌“${token.description}”？`)) return
    try { await deleteSystemAccessToken(token.id); setTokens((current) => current.filter((item) => item.id !== token.id)); setNotice('系统访问令牌已删除') }
    catch (reason) { setError(reason instanceof Error ? reason.message : '删除系统访问令牌失败') }
  }

  const copyCreatedSecret = async () => {
    setCopyingSecret(true); setError('')
    try {
      if (!navigator.clipboard?.writeText) throw new Error('当前浏览器不允许访问剪贴板')
      await navigator.clipboard.writeText(createdSecret)
      setCreatedSecret('')
      setNotice('完整系统令牌已复制，请妥善保存')
    } catch (reason) {
      setError(reason instanceof Error ? `复制失败：${reason.message}` : '复制失败，请检查浏览器剪贴板权限')
    } finally { setCopyingSecret(false) }
  }

  if (loading) return <section className="system-access-panel"><LoadingState label="正在加载系统访问令牌" /></section>
  return <section className="system-access-panel" aria-label="系统访问令牌">
    <header className="system-access-head"><div><strong>系统访问令牌</strong><p>用于外部 AI 诊断客户端访问 `/system-api/*`。它与模型调用 Key 完全独立，仅在创建完成时显示一次完整令牌。</p></div><button className="primary-button" type="button" onClick={() => setEditing('new')}><Plus size={16} />创建系统令牌</button></header>
    {notice && <OperationNotice onDismiss={() => setNotice('')}><CheckCircle2 size={16} />{notice}</OperationNotice>}
    {error && <OperationNotice tone="error">{error}</OperationNotice>}
    {!tokens.length ? <div className="system-access-empty"><KeyRound size={22} /><strong>还没有系统访问令牌</strong><span>创建后可为诊断脚本分配独立权限，避免使用管理员会话。</span></div> : <div className="system-access-list">{tokens.map((token) => <article className="system-access-row" key={token.id}><span className={`token-icon${token.is_active ? '' : ' token-icon--off'}`}><KeyRound size={17} /></span><div className="system-access-identity"><strong>{token.description}</strong><code>{token.token_hint}</code><span>{token.scopes.map((scope) => systemAccessScopeLabels[scope] || scope).join(' · ')}</span></div><div><strong>{token.is_active ? '已启用' : '已停用'}</strong><span>最后使用：{formatSystemTokenDate(token.last_used_at)}</span></div><div><strong>{formatSystemTokenDate(token.created_at)}</strong><span>{token.expires_at ? `到期：${formatSystemTokenDate(token.expires_at)}` : '永不过期'}</span></div><div className="row-actions"><button className="icon-button icon-button--surface" type="button" onClick={() => void toggle(token)} aria-label={token.is_active ? '停用系统令牌' : '启用系统令牌'} title={token.is_active ? '停用' : '启用'}>{token.is_active ? <Power size={16} /> : <Play size={16} />}</button><button className="icon-button icon-button--surface" type="button" onClick={() => setEditing(token)} aria-label="编辑系统令牌" title="编辑"><Pencil size={16} /></button><button className="icon-button icon-button--surface danger-button" type="button" onClick={() => void remove(token)} aria-label="删除系统令牌" title="删除"><Trash2 size={16} /></button></div></article>)}</div>}
    {editing && <SystemAccessTokenForm token={editing === 'new' ? undefined : editing} scopes={scopes} saving={saving} close={() => setEditing(null)} save={save} />}
    {createdSecret && <Modal title="系统令牌已创建" close={() => setCreatedSecret('')}><div className="secret-reveal"><p>完整令牌只显示这一次，请立即保存。关闭后无法恢复。</p><code>{createdSecret}</code><button className="primary-button" type="button" disabled={copyingSecret} onClick={() => void copyCreatedSecret()}><Check size={15} />{copyingSecret ? '正在复制' : '复制并关闭'}</button></div></Modal>}
  </section>
}

function SystemAccessTokenForm({ token, scopes, saving, close, save }: { token?: SystemAccessToken; scopes: string[]; saving: boolean; close: () => void; save: (value: { description: string; scopes: string[]; expires_at: number; is_active: boolean }) => void }) {
  const [description, setDescription] = useState(token?.description || '')
  const [selected, setSelected] = useState<string[]>(token?.scopes || scopes)
  const [expires, setExpires] = useState(token?.expires_at ? toDateTimeLocal(token.expires_at) : '')
  const [active, setActive] = useState(token?.is_active ?? true)
  return <Modal title={token ? '编辑系统访问令牌' : '创建系统访问令牌'} close={close}><form className="console-form system-access-form" onSubmit={(event) => { event.preventDefault(); void save({ description: description.trim(), scopes: selected, expires_at: expires ? new Date(expires).getTime() : 0, is_active: active }) }}><label><span>名称</span><input required maxLength={120} value={description} onChange={(event) => setDescription(event.target.value)} placeholder="例如：排障机器人" /></label><label><span>权限范围</span><div className="system-access-scope-list">{scopes.map((scope) => <label className="checkbox-field" key={scope}><input type="checkbox" checked={selected.includes(scope)} onChange={() => setSelected((current) => current.includes(scope) ? current.filter((item) => item !== scope) : [...current, scope])} /><span>{systemAccessScopeLabels[scope] || scope}<small>{scope}</small></span></label>)}</div></label><label><span>到期时间（可选）</span><input type="datetime-local" value={expires} onChange={(event) => setExpires(event.target.value)} /></label><label className="checkbox-field"><input type="checkbox" checked={active} onChange={(event) => setActive(event.target.checked)} />{token ? '启用此令牌' : '创建后启用'}</label><footer><button className="secondary-button" type="button" onClick={close}>取消</button><button className="primary-button" type="submit" disabled={saving || !selected.length}>{saving ? '保存中' : '保存'}</button></footer></form></Modal>
}

function toDateTimeLocal(value: number): string { const date = new Date(value - new Date(value).getTimezoneOffset() * 60_000); return date.toISOString().slice(0, 16) }

type AliasDraft = { canonical: string; aliases: string; enabled: boolean }

function parseAliasDraft(value: string): AliasDraft[] {
  try {
    const parsed = JSON.parse(value || '[]') as Array<{ canonical?: string; aliases?: string[]; enabled?: boolean }>
    if (!Array.isArray(parsed)) return []
    return parsed.map((item) => ({ canonical: item.canonical || '', aliases: Array.isArray(item.aliases) ? item.aliases.join('\n') : '', enabled: item.enabled !== false }))
  } catch {
    return []
  }
}

function serializeAliasDraft(groups: AliasDraft[]): string {
  return JSON.stringify(groups.map((group) => ({
    canonical: group.canonical.trim(),
    aliases: group.aliases.split(/[\n,]/).map((item) => item.trim()).filter(Boolean),
    enabled: group.enabled,
  })), null, 2)
}

function aliasMembers(group: AliasDraft): string[] {
  return group.aliases.split(/[\n,]/).map((item) => item.trim()).filter(Boolean)
}

function withMembers(group: AliasDraft, members: string[]): AliasDraft {
  return { ...group, aliases: Array.from(new Set(members)).join('\n') }
}

function ModelAliasPanel({ value, change }: { value: string; change: (value: string) => void }) {
  const groups = useMemo(() => parseAliasDraft(value), [value])
  const [inventory, setInventory] = useState<ModelAliasInventory | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [pickerFor, setPickerFor] = useState<number | null>(null)
  const [filter, setFilter] = useState('')
  const [expanded, setExpanded] = useState<number | null>(null)

  const reload = useCallback((signal?: AbortSignal) => {
    setLoading(true)
    getModelAliasInventory(signal)
      .then((data) => { setInventory(data); setError('') })
      .catch((err: Error) => { if (err.name !== 'AbortError') setError(err.message) })
      .finally(() => { if (!signal?.aborted) setLoading(false) })
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    reload(controller.signal)
    return () => controller.abort()
  }, [reload])

  const commit = (next: AliasDraft[]) => change(serializeAliasDraft(next))
  const update = (index: number, patch: Partial<AliasDraft>) => commit(groups.map((group, itemIndex) => itemIndex === index ? { ...group, ...patch } : group))
  // 新增的那条直接展开，否则点了「新增映射」看不到任何可填的地方。
  const add = () => { commit([...groups, { canonical: '', aliases: '', enabled: true }]); setExpanded(groups.length); setFilter('') }
  const remove = (index: number) => {
    commit(groups.filter((_, itemIndex) => itemIndex !== index))
    // 删除会让后续条目索引前移，展开态跟着挪，避免展开到别人身上。
    setExpanded((current) => current === null ? null : current === index ? null : current > index ? current - 1 : current)
  }

  // 带上原始 index：过滤后仍要能改到正确的那一条。
  const visibleGroups = useMemo(() => {
    const entries = groups.map((group, index) => ({ group, index }))
    const needle = filter.trim().toLowerCase()
    if (!needle) return entries
    return entries.filter(({ group }) => group.canonical.toLowerCase().includes(needle) || group.aliases.toLowerCase().includes(needle))
  }, [groups, filter])

  // Adopting a suggestion either extends the group that already owns the model
  // or appends a new group, so two canonical names never compete for one model.
  const adopt = (suggestion: ModelAliasSuggestion) => {
    if (suggestion.extends_canonical) {
      const target = groups.findIndex((group) => group.canonical.trim() === suggestion.extends_canonical)
      if (target >= 0) {
        commit(groups.map((group, index) => index === target ? withMembers(group, [...aliasMembers(group), ...suggestion.members]) : group))
        return
      }
    }
    const members = suggestion.members.filter((member) => member !== suggestion.canonical)
    commit([...groups, withMembers({ canonical: suggestion.canonical, aliases: '', enabled: true }, members)])
  }

  const usableSuggestions = useMemo(() => {
    if (!inventory) return []
    return inventory.suggestions.filter((suggestion) => suggestion.members.length > 0)
  }, [inventory])

  return <section className="model-alias-panel" aria-label="模型统一映射">
    <header>
      <div><strong>模型统一映射</strong><p>给多个上游名称设置一个稳定入口。请求统一名称时，系统会按渠道实际存在的模型名发送。</p></div>
      <div className="model-alias-header-actions">
        <button className="icon-button icon-button--surface" type="button" onClick={() => reload()} disabled={loading} title="重新读取渠道模型清单" aria-label="刷新模型清单"><RefreshCw size={15} className={loading ? 'is-spinning' : undefined} /></button>
        <button className="secondary-button" type="button" onClick={add}><Plus size={15} />新增映射</button>
      </div>
    </header>

    {error && <div className="model-alias-inline-error" role="alert">读取渠道模型清单失败：{error}</div>}

    {!!usableSuggestions.length && <div className="model-alias-suggestions">
      <header><Wrench size={15} /><strong>检测到 {usableSuggestions.length} 组可合并的名称</strong><span>来自当前启用渠道，点击即可套用</span></header>
      <ul>
        {usableSuggestions.map((suggestion) => <li key={`${suggestion.canonical}-${suggestion.members.join('|')}`}>
          <div className="model-alias-suggestion-body">
            <strong>{suggestion.canonical}{suggestion.extends_canonical && <em className="model-alias-suggestion-tag">补进已有映射</em>}</strong>
            <div className="model-alias-suggestion-members">{suggestion.members.map((member) => <code key={member}>{member}</code>)}</div>
            <small>{suggestion.reason}</small>
          </div>
          <button className="secondary-button" type="button" onClick={() => adopt(suggestion)}><Check size={15} />套用</button>
        </li>)}
      </ul>
    </div>}

    {groups.length > 4 && <label className="model-alias-filter"><Search size={14} /><input value={filter} onChange={(event) => setFilter(event.target.value)} placeholder={`在 ${groups.length} 条映射中搜索统一名称或上游名称`} aria-label="搜索映射" /></label>}

    {!groups.length ? <div className="model-alias-empty">暂未配置统一模型名称。可直接套用上方建议，或新增映射后从渠道模型清单中挑选。</div> : <div className="model-alias-list">
      {visibleGroups.map(({ group, index }) => {
        const members = aliasMembers(group)
        // 默认折叠成一行：映射条数多时全部展开会把设置页拖得很长。
        // 展开态由 index 记录，新增的那条自动展开。
        const open = expanded === index
        return <article className={`model-alias-row${open ? ' is-open' : ''}`} key={`${index}-${group.canonical}`}>
          <div className="model-alias-summary">
            <button className="model-alias-disclosure" type="button" aria-expanded={open} onClick={() => setExpanded(open ? null : index)}>
              <ChevronRight size={15} className="model-alias-caret" />
              <strong>{group.canonical || '未命名映射'}</strong>
              <span>{members.length ? `${members.length} 个上游名称` : '尚未选择上游名称'}</span>
            </button>
            <div className="model-alias-actions">
              <label className="checkbox-field"><input type="checkbox" checked={group.enabled} onChange={(event) => update(index, { enabled: event.target.checked })} />启用</label>
              <button className="icon-button icon-button--surface danger-button" type="button" onClick={() => remove(index)} aria-label={`删除 ${group.canonical || '未命名映射'}`} title="删除映射"><Trash2 size={15} /></button>
            </div>
          </div>
          {open && <div className="model-alias-fields">
            <label>统一名称<input value={group.canonical} onChange={(event) => update(index, { canonical: event.target.value })} placeholder="例如 glm-5.2" list="model-alias-known-names" /></label>
            <div className="model-alias-member-field">
              <div className="model-alias-member-head"><span>上游名称</span><button className="text-button" type="button" onClick={() => setPickerFor(index)} disabled={!inventory?.candidates.length}><Search size={14} />从渠道挑选{inventory ? `（${inventory.total_models}）` : ''}</button></div>
              {members.length ? <div className="model-alias-member-chips">
                {members.map((member) => <span className="model-alias-chip" key={member}>{member}<button type="button" onClick={() => update(index, withMembers(group, members.filter((item) => item !== member)))} aria-label={`移除 ${member}`} title="移除">×</button></span>)}
              </div> : <p className="model-alias-member-empty">尚未选择上游名称</p>}
              <textarea rows={2} value={group.aliases} onChange={(event) => update(index, { aliases: event.target.value })} placeholder={'也可直接粘贴，每行一个\nGLM-5.2\nz.ai/glm-5.2'} aria-label="上游名称，每行一个" />
            </div>
          </div>}
        </article>
      })}
      {!visibleGroups.length && <div className="model-alias-empty">没有匹配「{filter}」的映射</div>}
    </div>}

    <datalist id="model-alias-known-names">{(inventory?.candidates || []).map((candidate) => <option value={candidate.model} key={candidate.model} />)}</datalist>

    {pickerFor !== null && inventory && <ModelPickerModal
      candidates={inventory.candidates}
      selected={aliasMembers(groups[pickerFor] || { canonical: '', aliases: '', enabled: true })}
      close={() => setPickerFor(null)}
      apply={(members) => { const target = groups[pickerFor]; if (target) update(pickerFor, withMembers(target, members)); setPickerFor(null) }}
    />}
  </section>
}

function ModelPickerModal({ candidates, selected, close, apply }: { candidates: ModelAliasCandidate[]; selected: string[]; close: () => void; apply: (members: string[]) => void }) {
  const [query, setQuery] = useState('')
  const [picked, setPicked] = useState<string[]>(selected)

  // Selections survive re-filtering, so several searches can build one group.
  const toggle = (model: string) => setPicked((current) => current.includes(model) ? current.filter((item) => item !== model) : [...current, model])

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase()
    if (!needle) return candidates
    return candidates.filter((candidate) => candidate.model.toLowerCase().includes(needle) || candidate.channel_names.some((name) => name.toLowerCase().includes(needle)))
  }, [candidates, query])

  return <Modal title="从渠道模型中挑选" close={close}>
    <div className="model-picker">
      <div className="model-picker-toolbar">
        <label className="model-picker-search"><Search size={15} /><input autoFocus value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索模型或渠道名称" aria-label="搜索模型" /></label>
        <span className="model-picker-count">已选 {picked.length} 项{query.trim() && ` · 匹配 ${visible.length}`}</span>
      </div>
      {!visible.length ? <EmptyState label="没有匹配的模型，换个关键词或先在渠道中同步模型清单" /> : <ul className="model-picker-list">
        {visible.map((candidate) => <li key={candidate.model}>
          <label className="checkbox-field">
            <input type="checkbox" checked={picked.includes(candidate.model)} onChange={() => toggle(candidate.model)} />
            <span className="model-picker-entry">
              <strong>{candidate.model}</strong>
              <small>{candidate.channel_count} 个渠道{candidate.channel_names.length ? ` · ${candidate.channel_names.join('、')}` : ''}{candidate.mapped_to ? ` · 已映射到 ${candidate.mapped_to}` : ''}</small>
            </span>
          </label>
        </li>)}
      </ul>}
      <footer className="model-picker-footer">
        <button className="text-button" type="button" onClick={() => setPicked([])} disabled={!picked.length}>清空已选</button>
        <div><button className="secondary-button" type="button" onClick={close}>取消</button><button className="primary-button" type="button" onClick={() => apply(picked)}>确定（{picked.length}）</button></div>
      </footer>
    </div>
  </Modal>
}

function AppearancePanel({ customization, change, reset }: { customization: ThemeCustomization; change: (patch: Partial<ThemeCustomization>, label: string) => void; reset: () => void }) {
  const modes: Array<[ThemePreference, string, typeof Sun]> = [
    ['system', '跟随系统', Monitor],
    ['light', '亮色', Sun],
    ['dark', '暗色', Moon],
  ]
  const presets: Array<[ThemePreset, string, string[]]> = [
    ['jade', '松石绿', ['#10865d', '#3271c8', '#bd7914']],
    ['ocean', '海湾蓝', ['#14758f', '#436fbd', '#c07a20']],
    ['coral', '珊瑚红', ['#b9533f', '#2d7b78', '#bc741b']],
    ['anthropic', 'Anthropic', ['#a84c32', '#657a80', '#b0782c']],
  ]
  const fonts: Array<[ThemeFont, string, string]> = [
    ['modern', '几何无衬线', '枢衡 PivotFlow 0123'],
    ['system', '系统原生', '枢衡 PivotFlow 0123'],
    ['serif', '人文宋体', '枢衡 PivotFlow 0123'],
  ]
  const radii: Array<[ThemeRadius, string]> = [
    ['compact', '利落'],
    ['balanced', '均衡'],
    ['soft', '柔和'],
  ]
  return <div className="appearance-panel">
    <div className="appearance-intro"><div><strong>外观偏好</strong><span>保存在当前浏览器</span></div><button className="secondary-button" type="button" onClick={reset}><RotateCcw size={15} />恢复默认</button></div>

    <div className="appearance-workbench">
      <div className="appearance-controls">
        <section className="appearance-control-group">
          <header><span><PanelsTopLeft size={17} /></span><strong>明暗模式</strong></header>
          <div className="appearance-segmented">{modes.map(([value, label, Icon]) => <button className={customization.preference === value ? 'is-selected' : ''} type="button" aria-pressed={customization.preference === value} onClick={() => change({ preference: value }, label)} key={value}><Icon size={16} />{label}</button>)}</div>
        </section>

        <section className="appearance-control-group">
          <header><span><Palette size={17} /></span><strong>主题配色</strong></header>
          <div className="appearance-option-list">{presets.map(([value, label, colors]) => <button className={customization.preset === value ? 'is-selected' : ''} type="button" aria-pressed={customization.preset === value} onClick={() => change({ preset: value }, label)} key={value}><span className="theme-swatches">{colors.map((color) => <i style={{ background: color }} key={color} />)}</span><strong>{label}</strong>{customization.preset === value && <Check size={16} />}</button>)}</div>
        </section>

        <section className="appearance-control-group">
          <header><span><Type size={17} /></span><strong>字体风格</strong></header>
          <div className="appearance-font-list">{fonts.map(([value, label, sample]) => <button className={`${customization.font === value ? 'is-selected ' : ''}theme-font-sample theme-font-sample--${value}`} type="button" aria-pressed={customization.font === value} onClick={() => change({ font: value }, label)} key={value}><span>{sample}</span><strong>{label}</strong>{customization.font === value && <Check size={16} />}</button>)}</div>
        </section>

        <section className="appearance-control-group">
          <header><span><PanelsTopLeft size={17} /></span><strong>边角风格</strong></header>
          <div className="appearance-radius-list">{radii.map(([value, label]) => <button className={customization.radius === value ? 'is-selected' : ''} type="button" aria-pressed={customization.radius === value} onClick={() => change({ radius: value }, label)} key={value}><i className={`radius-shape radius-shape--${value}`} /><strong>{label}</strong>{customization.radius === value && <Check size={16} />}</button>)}</div>
        </section>
      </div>

      <div className="appearance-preview" aria-label="主题预览">
        <aside><span className="appearance-preview-mark">P</span><i /><i /><i /><i /></aside>
        <main>
          <header><div><small>系统概览</small><strong>运行状态</strong></div><span><Sun size={15} /></span></header>
          <div className="appearance-preview-metrics"><article><small>站点余额</small><strong>$ 286.40</strong><em>+ 12.8</em></article><article><small>今日请求</small><strong>1,284</strong><em>99.6%</em></article></div>
          <div className="appearance-preview-chart"><span /><span /><span /><span /><span /><span /><span /><span /></div>
          <footer><span>智能路由</span><strong>状态正常</strong></footer>
        </main>
      </div>
    </div>
  </div>
}

function UpdatePanel({ info, checking, check }: { info: { version: string; latest_version?: string; has_update?: boolean; release_url?: string; last_check?: string; message?: string; error?: string } | null; checking: boolean; check: () => void }) {
  const officialReleaseURL = info?.latest_version ? `https://github.com/yzgolden86/PivotFlow/releases/tag/${encodeURIComponent(info.latest_version)}` : info?.release_url
  return <div className="upstream-update-panel">
    <div className="upstream-update-head"><div><strong>PivotFlow 上游版本</strong><p>仅检查官方发布信息，不会自动下载、替换或重启当前程序。</p></div><button className="secondary-button" type="button" onClick={check} disabled={checking}><RefreshCw className={checking ? 'spin' : ''} size={16} />{checking ? '检查中' : '立即检查'}</button></div>
    <div className="upstream-update-grid"><div><small>当前运行</small><strong>{info?.version || '当前构建'}</strong></div><div><small>最新已知</small><strong>{info?.latest_version || '尚未检查'}</strong></div><div><small>状态</small><strong className={info?.has_update ? 'has-update' : ''}>{info?.has_update ? '发现新版本' : info?.error ? '检查失败' : '已是最新或尚未检查'}</strong></div></div>
    {info?.error && <div className="upstream-update-error">{info.error}</div>}
    <footer>{info?.last_check && <span>最近检查 {new Date(info.last_check).toLocaleString('zh-CN')}</span>}{officialReleaseURL && <a href={officialReleaseURL} target="_blank" rel="noreferrer">查看发布说明<ExternalLink size={14} /></a>}</footer>
  </div>
}

function SettingInput({ setting, value, change }: { setting: SystemSetting; value: string; change: (value: string) => void }) {
  if (setting.value_type === 'bool') return <button className={`setting-switch${normalizeBool(value) === 'true' ? ' is-on' : ''}`} type="button" role="switch" aria-checked={normalizeBool(value) === 'true'} onClick={() => change(normalizeBool(value) === 'true' ? 'false' : 'true')} disabled={!setting.editable}><span>{normalizeBool(value) === 'true' ? '已启用' : '已停用'}</span><i /></button>
  if (setting.key === 'site_daily_checkin_time' || setting.key === 'site_daily_announcement_time') return <input type="time" step="60" value={value} onChange={(event) => change(event.target.value)} disabled={!setting.editable} />
  const options = settingOptions(setting.key)
  const tips = optionTips[setting.key]
  // 有逐项说明时用分段按钮：原生 <option> 的 title 在各浏览器表现不一致，
  // 而策略之间的取舍必须讲清楚，光看名字看不出来。
  if (options && tips) return <div className="setting-choice" role="radiogroup" aria-label={settingLabel(setting)}>
    {options.map(([optionValue, label]) => <button
      className={`setting-choice-option${value === optionValue ? ' is-selected' : ''}`}
      type="button"
      role="radio"
      aria-checked={value === optionValue}
      disabled={!setting.editable}
      onClick={() => change(optionValue)}
      key={optionValue}
    >
      <span className="setting-choice-label">{label}{value === optionValue && <Check size={14} />}</span>
      {tips[optionValue] && <span className="setting-choice-tip" role="tooltip">{tips[optionValue]}</span>}
    </button>)}
  </div>
  if (options) return <select value={value} onChange={(event) => change(event.target.value)} disabled={!setting.editable}>{options.map(([optionValue, label]) => <option value={optionValue} key={optionValue}>{label}</option>)}</select>
  if (setting.value_type === 'json') return <textarea rows={4} value={value} onChange={(event) => change(event.target.value)} disabled={!setting.editable} spellCheck={false} />
  const type = ['int', 'float', 'duration'].includes(setting.value_type) ? 'number' : 'text'
  return <input type={type} step={setting.value_type === 'float' ? '0.1' : '1'} value={value} onChange={(event) => change(event.target.value)} disabled={!setting.editable} />
}

function normalizeBool(value: string): string { return value === 'true' || value === '1' ? 'true' : 'false' }

function settingGroup(key: string): GroupKey {
  if (key === 'site_daily_checkin_time' || key === 'site_daily_announcement_time') return 'automation'
  if (key === 'cooldown_fallback_enabled') return 'routing'
  if (/^cooldown_|global_cooldown/.test(key)) return 'cooldown'
  if (/timeout|connection_reuse/.test(key)) return 'timeouts'
  if (/max_concurrency|max_body|max_image_body/.test(key)) return 'capacity'
  if (/channel_test|channel_check|enable_health|success_rate|health_|ttfb_/.test(key)) return 'health'
  if (/log_|debug_log|auto_refresh/.test(key)) return 'logs'
  if (/websocket|responses_ws/.test(key)) return 'websocket'
  if (/update|catalog/.test(key)) return 'maintenance'
  if (/antigravity/.test(key)) return 'advanced'
  return 'routing'
}

const labels: Record<string, string> = {
  max_key_retries: '单渠道密钥重试次数', model_fuzzy_match: '模型名称模糊匹配', cooldown_fallback_enabled: '全渠道冷却时继续尝试',
  route_strategy: '渠道选择策略',
  upstream_first_byte_timeout: '全局首字超时', upstream_connection_reuse_limit_seconds: '上游连接复用时限', stream_timeout: '流式请求总时限', non_stream_timeout: '非流式请求总时限',
  anthropic_first_byte_timeout: 'Anthropic 首字超时', anthropic_non_stream_timeout: 'Anthropic 非流式超时', codex_first_byte_timeout: 'Codex 首字超时', codex_non_stream_timeout: 'Codex 非流式超时', openai_first_byte_timeout: 'OpenAI 首字超时', openai_non_stream_timeout: 'OpenAI 非流式超时', gemini_first_byte_timeout: 'Gemini 首字超时', gemini_non_stream_timeout: 'Gemini 非流式超时',
  cooldown_auth_seconds: '认证错误冷却', cooldown_server_seconds: '上游服务错误冷却', cooldown_timeout_seconds: '请求超时冷却', cooldown_rate_limit_seconds: '限流错误冷却', cooldown_max_seconds: '冷却时间上限', cooldown_min_seconds: '冷却时间下限', global_cooldown_detection_rules: '全局冷却识别规则',
  max_concurrency: '最大并发请求数', max_body_bytes: '普通请求体上限', max_image_body_bytes: '图片请求体上限',
  channel_test_content: '健康检测提示词', channel_check_interval_hours: '自动健康检测间隔', enable_health_score: '按渠道健康度动态排序', success_rate_penalty_weight: '失败率惩罚权重', health_score_window_minutes: '健康度统计窗口', health_score_update_interval: '健康度刷新间隔', health_min_confident_sample: '健康度可信样本量', enable_ttfb_score: '加入首字延迟评分', ttfb_penalty_weight: '首字延迟惩罚权重', ttfb_max_slow_ratio: '首字慢速比上限', ttfb_min_confident_sample: '首字评分可信样本量',
  site_daily_checkin_time: '每日自动签到时间',
  site_daily_announcement_time: '每日自动刷新公告时间',
  log_retention_days: '请求日志保留时间', debug_log_enabled: '记录上游原始报文', debug_log_retention_minutes: '原始报文保留时间', auto_refresh_interval_seconds: '日志页面自动刷新间隔',
  responses_ws_max_sessions: '最大执行会话数', responses_ws_session_ttl_minutes: '空闲会话保留时间', responses_ws_max_transcript_bytes: '会话内容总容量', responses_ws_max_connections: '最大长连接数', responses_ws_max_connections_per_token: '单密钥最大长连接数',
  model_catalog_sync_interval_hours: '模型价格目录同步间隔', auto_update_interval_hours: '上游检查间隔', auto_update_channel: '上游检查通道', antigravity_sensitive_words: 'Antigravity 敏感词兼容',
}

const helps: Record<string, string> = {
  max_key_retries: '一次请求在同一渠道内最多尝试多少把上游密钥。', model_fuzzy_match: '精确匹配失败后尝试兼容带日期或版本后缀的模型名。', cooldown_fallback_enabled: '所有渠道都在冷却时，仍选择当前最优渠道进行最后一次尝试。',
  route_strategy: '同一模型有多个可用渠道时如何挑选。两种策略都先比优先级，只在优先级相同的渠道之间才有差别。鼠标移到选项上看详细取舍。',
  channel_test_content: '自动巡检渠道时发送的最小测试内容。', channel_check_interval_hours: '设为 0 可关闭定时巡检，小数支持分钟级间隔。', enable_health_score: '根据近期成功率动态调整同优先级渠道的顺序。', enable_ttfb_score: '在健康度排序中考虑首字响应速度。',
  site_daily_checkin_time: '到达该时间后，为当天尚未执行过的账号触发签到；分别按账号或站点时区计算。',
  site_daily_announcement_time: '到达该时间后，每个站点每天刷新一次公告；按站点时区计算。',
  debug_log_enabled: '会记录上游请求与响应原文，仅建议排障时短暂开启。', auto_refresh_interval_seconds: '日志页面按此间隔刷新历史请求和进行中请求；设为 0 关闭，隐藏浏览器标签页时会暂停。', model_catalog_sync_interval_hours: '从 models.dev 更新价格信息，不影响站点模型列表。', auto_update_interval_hours: '仅非容器部署生效，设为 0 关闭后台检查；融合版不会自动替换程序。',
  antigravity_sensitive_words: '使用零宽字符处理特定 systemInstruction 关键词。', global_cooldown_detection_rules: '没有渠道专属规则时使用的状态码与错误文本识别规则。',
}

function settingLabel(setting: SystemSetting): string { return labels[setting.key] || setting.description?.replace(/\([^)]*\)/g, '').trim() || setting.key }
function settingHelp(setting: SystemSetting): string { return helps[setting.key] || humanizeDescription(setting.description) }
function humanizeDescription(description: string): string {
  const cleaned = (description || '').replace(/\([^)]*\)/g, '').replace(/（[^）]*）/g, '').trim()
  return cleaned && cleaned !== description ? cleaned : description || '调整此项运行参数。'
}

function settingOptions(key: string): Array<[string, string]> | null {
  if (key === 'auto_update_channel') return [['stable', '稳定版'], ['preview', '稳定版与预览版']]
  if (key === 'route_strategy') return [['balanced', '均衡轮询'], ['sticky', '粘性轮询']]
  return null
}

// 逐个选项的悬浮说明。两种策略的取舍差别较大，只靠名字看不出来。
const optionTips: Record<string, Record<string, string>> = {
  route_strategy: {
    balanced: '先比优先级，只在优先级相同的渠道之间按权重（有效 Key 数）平滑轮询。每个请求换一个渠道，负载摊得最匀，单个渠道抖动的影响最小；代价是不复用上游的会话亲和性。',
    sticky: '先比优先级，然后固定使用上次成功的渠道，直到它失败才切到下一个（失败时本次请求内会依次尝试其余候选，不会直接报错）。命中缓存和会话连续性更好；代价是流量会集中在少数渠道上。',
  },
}

function settingUnit(key: string): string {
  if (key === 'max_body_bytes' || key === 'max_image_body_bytes' || key === 'responses_ws_max_transcript_bytes') return '字节'
  if (/interval_hours/.test(key)) return '小时'
  if (/minutes/.test(key)) return '分钟'
  if (/seconds|timeout|update_interval$/.test(key)) return '秒'
  if (/concurrency|connections|sessions|sample|retries/.test(key)) return '个'
  if (key === 'log_retention_days') return '天'
  return ''
}

function formatDefault(setting: SystemSetting): string {
  if (setting.value_type === 'bool') return normalizeBool(setting.default_value) === 'true' ? '启用' : '停用'
  const option = settingOptions(setting.key)?.find(([value]) => value === setting.default_value)
  const unit = settingUnit(setting.key)
  return option?.[1] || `${setting.default_value || '空'}${unit ? ` ${unit}` : ''}`
}
