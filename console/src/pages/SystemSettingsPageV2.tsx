import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Activity, BellRing, CalendarClock, Check, CheckCircle2, Clock3, ExternalLink, FileClock, Gauge, Network,
  DatabaseBackup, RefreshCw, RotateCcw, Route, Save, Search, ShieldAlert, Sun, Moon, Monitor,
  Palette, PanelsTopLeft, SlidersHorizontal, TimerReset, Type, Wrench,
} from 'lucide-react'
import { checkForUpdates, getSystemSettings, resetSystemSetting, updateSystemSettings } from '../api'
import type { SystemSetting } from '../types'
import { EmptyState, ErrorState, LoadingState, OperationNotice } from './shared'
import { WebhookSettingsPanel } from './SettingsPage'
import { BackupSettingsPanel } from './BackupSettingsPanel'
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
  const [section, setSection] = useState<'runtime' | 'notifications' | 'backup'>('runtime')
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
    if (!window.confirm(`保存 ${dirty.size} 项设置？服务会自动重启并短暂中断请求。`)) return
    setSaving(true)
    setError('')
    setNotice('')
    try {
      await updateSystemSettings(Object.fromEntries(Array.from(dirty).map((key) => [key, values[key]])))
      setNotice('设置已保存，服务正在重启')
      setDirty(new Set())
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const reset = async (setting: SystemSetting) => {
    if (!window.confirm(`将“${settingLabel(setting)}”恢复为默认值？服务会自动重启。`)) return
    setSaving(true)
    setError('')
    setNotice('')
    try {
      await resetSystemSetting(setting.key)
      setValues((current) => ({ ...current, [setting.key]: setting.default_value }))
      setDirty(new Set())
      setNotice('已恢复默认值，服务正在重启')
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
    </div>

    {section === 'notifications' ? <WebhookSettingsPanel /> : section === 'backup' ? <BackupSettingsPanel /> : <>
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
          {group === 'advanced' && !query.trim() && <div className="settings-risk-note"><ShieldAlert size={17} /><span>这些设置用于特殊兼容场景。不了解具体影响时请保持默认值。</span></div>}

          {!visible.length ? <EmptyState label="没有符合条件的设置" /> : <div className="setting-list">
            {visible.map((setting) => <article className={`setting-row${dirty.has(setting.key) ? ' setting-row--dirty' : ''}`} key={setting.key}>
              <div className="setting-copy"><strong>{settingLabel(setting)}</strong><p>{settingHelp(setting)}</p><code>{setting.key}</code></div>
              <div className="setting-control">
                <SettingInput setting={setting} value={values[setting.key] ?? ''} change={(value) => change(setting.key, value)} />
                {setting.value_type !== 'bool' && settingUnit(setting.key) && <span>{settingUnit(setting.key)}</span>}
              </div>
              <div className="setting-actions">
                <span>默认值：{formatDefault(setting)}</span>
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
  return <div className="upstream-update-panel">
    <div className="upstream-update-head"><div><strong>PivotFlow 上游版本</strong><p>仅检查官方发布信息，不会自动下载、替换或重启当前程序。</p></div><button className="secondary-button" type="button" onClick={check} disabled={checking}><RefreshCw className={checking ? 'spin' : ''} size={16} />{checking ? '检查中' : '立即检查'}</button></div>
    <div className="upstream-update-grid"><div><small>当前运行</small><strong>{info?.version || '当前构建'}</strong></div><div><small>最新已知</small><strong>{info?.latest_version || '尚未检查'}</strong></div><div><small>状态</small><strong className={info?.has_update ? 'has-update' : ''}>{info?.has_update ? '发现新版本' : info?.error ? '检查失败' : '已是最新或尚未检查'}</strong></div></div>
    {info?.error && <div className="upstream-update-error">{info.error}</div>}
    <footer>{info?.last_check && <span>最近检查 {new Date(info.last_check).toLocaleString('zh-CN')}</span>}{info?.release_url && <a href={info.release_url} target="_blank" rel="noreferrer">查看发布说明<ExternalLink size={14} /></a>}</footer>
  </div>
}

function SettingInput({ setting, value, change }: { setting: SystemSetting; value: string; change: (value: string) => void }) {
  if (setting.value_type === 'bool') return <button className={`setting-switch${normalizeBool(value) === 'true' ? ' is-on' : ''}`} type="button" role="switch" aria-checked={normalizeBool(value) === 'true'} onClick={() => change(normalizeBool(value) === 'true' ? 'false' : 'true')} disabled={!setting.editable}><span>{normalizeBool(value) === 'true' ? '已启用' : '已停用'}</span><i /></button>
  if (setting.key === 'site_daily_checkin_time') return <input type="time" step="60" value={value} onChange={(event) => change(event.target.value)} disabled={!setting.editable} />
  const options = settingOptions(setting.key)
  if (options) return <select value={value} onChange={(event) => change(event.target.value)} disabled={!setting.editable}>{options.map(([optionValue, label]) => <option value={optionValue} key={optionValue}>{label}</option>)}</select>
  if (setting.value_type === 'json') return <textarea rows={4} value={value} onChange={(event) => change(event.target.value)} disabled={!setting.editable} spellCheck={false} />
  const type = ['int', 'float', 'duration'].includes(setting.value_type) ? 'number' : 'text'
  return <input type={type} step={setting.value_type === 'float' ? '0.1' : '1'} value={value} onChange={(event) => change(event.target.value)} disabled={!setting.editable} />
}

function normalizeBool(value: string): string { return value === 'true' || value === '1' ? 'true' : 'false' }

function settingGroup(key: string): GroupKey {
  if (key === 'site_daily_checkin_time') return 'automation'
  if (key === 'cooldown_fallback_enabled') return 'routing'
  if (/^cooldown_|global_cooldown/.test(key)) return 'cooldown'
  if (/timeout|connection_reuse/.test(key)) return 'timeouts'
  if (/max_concurrency|max_body|max_image_body/.test(key)) return 'capacity'
  if (/channel_test|channel_check|enable_health|success_rate|health_|ttfb_/.test(key)) return 'health'
  if (/log_|debug_log|auto_refresh|channel_stats_range/.test(key)) return 'logs'
  if (/websocket|responses_ws/.test(key)) return 'websocket'
  if (/update|catalog/.test(key)) return 'maintenance'
  if (/antigravity/.test(key)) return 'advanced'
  return 'routing'
}

const labels: Record<string, string> = {
  max_key_retries: '单渠道密钥重试次数', model_fuzzy_match: '模型名称模糊匹配', cooldown_fallback_enabled: '全渠道冷却时继续尝试',
  upstream_first_byte_timeout: '全局首字超时', upstream_connection_reuse_limit_seconds: '上游连接复用时限', stream_timeout: '流式请求总时限', non_stream_timeout: '非流式请求总时限',
  anthropic_first_byte_timeout: 'Anthropic 首字超时', anthropic_non_stream_timeout: 'Anthropic 非流式超时', codex_first_byte_timeout: 'Codex 首字超时', codex_non_stream_timeout: 'Codex 非流式超时', openai_first_byte_timeout: 'OpenAI 首字超时', openai_non_stream_timeout: 'OpenAI 非流式超时', gemini_first_byte_timeout: 'Gemini 首字超时', gemini_non_stream_timeout: 'Gemini 非流式超时',
  cooldown_auth_seconds: '认证错误冷却', cooldown_server_seconds: '上游服务错误冷却', cooldown_timeout_seconds: '请求超时冷却', cooldown_rate_limit_seconds: '限流错误冷却', cooldown_max_seconds: '冷却时间上限', cooldown_min_seconds: '冷却时间下限', global_cooldown_detection_rules: '全局冷却识别规则',
  max_concurrency: '最大并发请求数', max_body_bytes: '普通请求体上限', max_image_body_bytes: '图片请求体上限',
  channel_test_content: '健康检测提示词', channel_check_interval_hours: '自动健康检测间隔', enable_health_score: '按渠道健康度动态排序', success_rate_penalty_weight: '失败率惩罚权重', health_score_window_minutes: '健康度统计窗口', health_score_update_interval: '健康度刷新间隔', health_min_confident_sample: '健康度可信样本量', enable_ttfb_score: '加入首字延迟评分', ttfb_penalty_weight: '首字延迟惩罚权重', ttfb_max_slow_ratio: '首字慢速比上限', ttfb_min_confident_sample: '首字评分可信样本量',
  site_daily_checkin_time: '每日自动签到时间',
  log_retention_days: '请求日志保留时间', debug_log_enabled: '记录上游原始报文', debug_log_retention_minutes: '原始报文保留时间', auto_refresh_interval_seconds: '页面自动刷新间隔', log_channel_click_action: '日志中的渠道点击行为', channel_stats_range: '渠道统计默认范围',
  responses_ws_max_sessions: '最大执行会话数', responses_ws_session_ttl_minutes: '空闲会话保留时间', responses_ws_max_transcript_bytes: '会话内容总容量', responses_ws_max_connections: '最大长连接数', responses_ws_max_connections_per_token: '单密钥最大长连接数',
  model_catalog_sync_interval_hours: '模型价格目录同步间隔', auto_update_interval_hours: '上游检查间隔', auto_update_channel: '上游检查通道', antigravity_sensitive_words: 'Antigravity 敏感词兼容',
}

const helps: Record<string, string> = {
  max_key_retries: '一次请求在同一渠道内最多尝试多少把上游密钥。', model_fuzzy_match: '精确匹配失败后尝试兼容带日期或版本后缀的模型名。', cooldown_fallback_enabled: '所有渠道都在冷却时，仍选择当前最优渠道进行最后一次尝试。',
  channel_test_content: '自动巡检渠道时发送的最小测试内容。', channel_check_interval_hours: '设为 0 可关闭定时巡检，小数支持分钟级间隔。', enable_health_score: '根据近期成功率动态调整同优先级渠道的顺序。', enable_ttfb_score: '在健康度排序中考虑首字响应速度。',
  site_daily_checkin_time: '到达该时间后，为当天尚未执行过的账号触发签到；分别按账号或站点时区计算。',
  debug_log_enabled: '会记录上游请求与响应原文，仅建议排障时短暂开启。', auto_refresh_interval_seconds: '设为 0 关闭；打开弹窗时不会打断当前操作。', model_catalog_sync_interval_hours: '从 models.dev 更新价格信息，不影响站点模型列表。', auto_update_interval_hours: '仅非容器部署生效，设为 0 关闭后台检查；融合版不会自动替换程序。',
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
  if (key === 'log_channel_click_action') return [['edit', '打开渠道编辑'], ['navigate', '跳转渠道页面']]
  if (key === 'channel_stats_range') return [['today', '今日'], ['week', '本周'], ['month', '本月']]
  return null
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
