import { parseByteSettingValue, type ByteSettingKey } from '../settingsFields.ts'
import { BALANCE_SAMPLE_INTERVALS } from './balanceHistoryChart.ts'

export type SettingsGroupKey = 'appearance' | 'automation' | 'routing' | 'cache' | 'timeouts' | 'cooldown' | 'capacity' | 'health' | 'logs' | 'websocket' | 'maintenance' | 'advanced'

const balanceSamplingSettings: Record<string, { label: string; help: string; options?: Array<[string, string]> }> = {
  site_daily_balance_enabled: { label: '余额自动采样', help: '按采样间隔读取启用账号的余额，每天保留截至最新成功采样的净变化。关闭后保留历史记录。' },
  site_daily_balance_time: { label: '采样基准时间', help: '按北京时间以此时间为采样相位，之后按间隔重复。例如每小时、基准 00:05 时，在 00:05、01:05、02:05 等时点采样；失败保留缺口。修改后重启生效。' },
  site_balance_sample_interval_minutes: {
    label: '余额采样间隔', help: '默认每小时（60 分钟）采样，按采样基准时间重复。每日图仍为一天一点，显示截至当天最新成功采样的净变化；缺少前日比较时可展示自当日首次采样以来的变化。修改后重启生效。',
    options: BALANCE_SAMPLE_INTERVALS.map((minutes) => [String(minutes), minutes === 1440 ? '每日' : minutes === 60 ? '每小时' : minutes < 60 ? `每 ${minutes} 分钟` : `每 ${minutes / 60} 小时`]),
  },
}

export function balanceSamplingSetting(key: string): typeof balanceSamplingSettings[string] | undefined {
  return Object.hasOwn(balanceSamplingSettings, key) ? balanceSamplingSettings[key] : undefined
}

export function settingGroup(key: string): SettingsGroupKey {
  if (['site_daily_checkin_time', 'site_daily_announcement_time', 'site_daily_balance_enabled', 'site_daily_balance_time', 'site_balance_sample_interval_minutes'].includes(key)) return 'automation'
  if (/^request_cache_/.test(key)) return 'cache'
  if (key === 'cooldown_fallback_enabled') return 'routing'
  if (/^cooldown_|global_cooldown/.test(key)) return 'cooldown'
  if (/websocket|responses_ws/.test(key)) return 'websocket'
  if (/timeout|connection_reuse/.test(key)) return 'timeouts'
  if (/max_concurrency|max_body|max_image_body/.test(key)) return 'capacity'
  if (/channel_test|channel_check|enable_health|success_rate|health_|ttfb_/.test(key)) return 'health'
  if (/log_|debug_log|auto_refresh/.test(key)) return 'logs'
  if (/update|catalog/.test(key)) return 'maintenance'
  if (/antigravity/.test(key)) return 'advanced'
  return 'routing'
}

export interface SettingsWorkspaceSection<T> {
  id: string
  title: string
  caption?: string
  settings: T[]
}

const groupTitles: Record<SettingsGroupKey, string> = {
  appearance: '外观', automation: '自动任务', routing: '路由策略', cache: '请求缓存',
  timeouts: '请求超时', cooldown: '故障冷却', capacity: '并发容量', health: '健康检测',
  logs: '日志界面', websocket: '长连接', maintenance: '更新维护', advanced: '高级兼容',
}

function workspaceSection(key: string): { id: string; title: string; caption?: string } {
  const group = settingGroup(key)
  if (group === 'timeouts') {
    if (key.includes('connection_reuse')) return { id: 'timeouts-connections', title: '连接复用', caption: '控制已有上游连接的复用时限。' }
    const protocol = /^(anthropic|codex|openai|gemini)_(first_byte|non_stream)_timeout$/.exec(key)?.[1]
    if (protocol) {
      const titles: Record<string, string> = { anthropic: 'Anthropic', codex: 'Codex', openai: 'OpenAI', gemini: 'Gemini' }
      const title = titles[protocol]
      return { id: `timeouts-${protocol}`, title, caption: '设为 0 时继承对应的全局超时。' }
    }
    if (['upstream_first_byte_timeout', 'stream_timeout', 'non_stream_timeout'].includes(key)) return { id: 'timeouts-defaults', title: '全局默认', caption: '首字等待与请求总时限分别控制。' }
    return { id: 'timeouts-other', title: '其他超时' }
  }
  if (group === 'health') {
    if (/ttfb/.test(key)) return { id: 'health-latency', title: '首字延迟评分' }
    if (/channel_test|channel_check/.test(key)) return { id: 'health-checks', title: '自动检测' }
    return { id: 'health-ranking', title: '健康度排序' }
  }
  if (group === 'cooldown') return key === 'global_cooldown_detection_rules'
    ? { id: 'cooldown-rules', title: '错误识别规则' } : { id: 'cooldown-durations', title: '冷却时长' }
  if (group === 'logs') return /^debug_log/.test(key)
    ? { id: 'logs-debug', title: '原始报文' } : { id: 'logs-history', title: '历史与刷新' }
  if (group === 'websocket') return /connections/.test(key)
    ? { id: 'websocket-connections', title: '长连接预算' } : { id: 'websocket-sessions', title: '执行会话与内容' }
  return { id: group, title: groupTitles[group] }
}

export function groupSettingsForWorkspace<T extends { key: string }>(settings: T[]): SettingsWorkspaceSection<T>[] {
  const sections = new Map<string, SettingsWorkspaceSection<T>>()
  for (const setting of settings) {
    const section = workspaceSection(setting.key)
    if (!sections.has(section.id)) sections.set(section.id, { ...section, settings: [] })
    sections.get(section.id)!.settings.push(setting)
  }
  // Defaults lead the timeout workspace even when the server sends protocol keys first.
  const result = [...sections.values()]
  const defaults = result.findIndex((section) => section.id === 'timeouts-defaults')
  if (defaults > 0) result.unshift(...result.splice(defaults, 1))
  return result
}

export function editByteSetting(key: ByteSettingKey, draft: string, originalValue: string) {
  const parsed = parseByteSettingValue(key, draft)
  if (!parsed.ok) return { draft, value: null, error: parsed.error, dirty: true }
  const value = String(parsed.bytes)
  return { draft, value, error: '', dirty: value !== originalValue }
}
