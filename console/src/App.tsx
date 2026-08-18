import { lazy, Suspense, useEffect, useMemo, useState } from 'react'
import type { LucideIcon } from 'lucide-react'
import {
  Activity,
  BarChart3,
  Bell,
  CalendarCheck2,
  FlaskConical,
  Gauge,
  Globe2,
  KeyRound,
  LogOut,
  Menu,
  Monitor,
  Moon,
  PanelLeftClose,
  Route as RouteIcon,
  ScrollText,
  Settings,
  Sun,
  TrendingUp,
  Users,
  X,
} from 'lucide-react'
import { Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { getCheckinAttemptsBatch, getSiteInventory } from './api'
import GlobalSearch from './components/GlobalSearch'
import { applyTheme, readThemePreference, resolveTheme } from './theme'
import type { ResolvedTheme, ThemePreference } from './theme'

const loadDashboardPage = () => import('./pages/DashboardPage')
const loadChannelsPage = () => import('./pages/ChannelsPage')
const loadLogsPage = () => import('./pages/LogsPage')
const loadStatsPage = () => import('./pages/StatsPage')
const loadModelTestPage = () => import('./pages/ModelTestPage')
const loadSitesPage = () => import('./pages/SitesPage')
const loadAccountsPage = () => import('./pages/AccountsPageV2')
const loadCheckinsPage = () => import('./pages/CheckinsPage')
const loadAnnouncementsPage = () => import('./pages/AnnouncementsPage')
const loadTokensPage = () => import('./pages/TokensPage')
const loadTrendPage = () => import('./pages/TrendPage')
const loadSystemSettingsPage = () => import('./pages/SystemSettingsPageV2')

const DashboardPage = lazy(loadDashboardPage)
const ChannelsPage = lazy(loadChannelsPage)
const LogsPage = lazy(loadLogsPage)
const StatsPage = lazy(loadStatsPage)
const ModelTestPage = lazy(loadModelTestPage)
const SitesPage = lazy(loadSitesPage)
const AccountsPage = lazy(loadAccountsPage)
const CheckinsPage = lazy(loadCheckinsPage)
const AnnouncementsPage = lazy(loadAnnouncementsPage)
const TokensPage = lazy(loadTokensPage)
const TrendPage = lazy(loadTrendPage)
const SystemSettingsPage = lazy(loadSystemSettingsPage)

const pageLoaders: Record<string, () => Promise<unknown>> = {
  '/': loadDashboardPage,
  '/channels': loadChannelsPage,
  '/logs': loadLogsPage,
  '/stats': loadStatsPage,
  '/models': loadModelTestPage,
  '/sites': loadSitesPage,
  '/accounts': loadAccountsPage,
  '/checkins': loadCheckinsPage,
  '/announcements': loadAnnouncementsPage,
  '/tokens': loadTokensPage,
  '/trend': loadTrendPage,
  '/system': loadSystemSettingsPage,
}

function preloadPage(path: string) {
  void pageLoaders[path]?.().catch(() => undefined)
}

interface NavEntry {
  label: string
  href: string
  icon: LucideIcon
}

interface NavGroup {
  label: string
  entries: NavEntry[]
}

const navigation: NavGroup[] = [
  {
    label: '工作台',
    entries: [{ label: '系统概览', href: '/', icon: Gauge }],
  },
  {
    label: '站点',
    entries: [
	  { label: '站点管理', href: '/sites', icon: Globe2 },
      { label: '账号管理', href: '/accounts', icon: Users },
      { label: '签到中心', href: '/checkins', icon: CalendarCheck2 },
      { label: '公告中心', href: '/announcements', icon: Bell },
    ],
  },
  {
    label: '路由',
    entries: [
      { label: '渠道分发', href: '/channels', icon: RouteIcon },
      { label: '令牌管理', href: '/tokens', icon: KeyRound },
    ],
  },
  {
    label: '观测',
    entries: [
      { label: '请求日志', href: '/logs', icon: ScrollText },
      { label: '用量统计', href: '/stats', icon: BarChart3 },
      { label: '消费趋势', href: '/trend', icon: TrendingUp },
    ],
  },
  {
    label: '工具',
    entries: [
      { label: '模型测试', href: '/models', icon: FlaskConical },
	  { label: '系统设置', href: '/system', icon: Settings },
    ],
  },
]

function App() {
  const [mobileOpen, setMobileOpen] = useState(false)
  const [collapsed, setCollapsed] = useState(false)
  const [themePreference, setThemePreference] = useState<ThemePreference>(readThemePreference)
  const [resolvedTheme, setResolvedTheme] = useState<ResolvedTheme>(() => resolveTheme(readThemePreference()))
  const [themePickerOpen, setThemePickerOpen] = useState(false)
  const location = useLocation()

  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const apply = () => {
      const next = applyTheme(themePreference)
      setResolvedTheme(next)
    }
    apply()
    media.addEventListener?.('change', apply)
    return () => media.removeEventListener?.('change', apply)
  }, [themePreference])

  useEffect(() => {
    const open = () => setThemePickerOpen(true)
    window.addEventListener('fusion:open-theme-picker', open)
    return () => window.removeEventListener('fusion:open-theme-picker', open)
  }, [])

  useEffect(() => {
    const update = (event: Event) => {
      const preference = (event as CustomEvent<ThemePreference>).detail
      if (preference === 'light' || preference === 'dark' || preference === 'system') setThemePreference(preference)
    }
    window.addEventListener('fusion:theme-changed', update)
    return () => window.removeEventListener('fusion:theme-changed', update)
  }, [])

  useEffect(() => {
    setMobileOpen(false)
    window.scrollTo({ top: 0, behavior: 'auto' })
  }, [location.pathname])

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void Promise.allSettled([
        loadSitesPage(),
        loadAccountsPage(),
        loadCheckinsPage(),
        loadChannelsPage(),
        getSiteInventory(),
        getCheckinAttemptsBatch(100),
      ])
    }, 600)
    return () => window.clearTimeout(timer)
  }, [])

  const shellClass = useMemo(
    () => `app-shell${collapsed ? ' app-shell--collapsed' : ''}`,
    [collapsed],
  )

  const logout = async () => {
    const token = localStorage.getItem('pivotflow_token')
    try {
      await fetch('/logout', {
        method: 'POST',
        headers: token ? { Authorization: `Bearer ${token}` } : undefined,
      })
    } finally {
      localStorage.removeItem('pivotflow_token')
      localStorage.removeItem('pivotflow_token_expiry')
      localStorage.removeItem('pivotflow_web_role')
      window.location.replace('/web/auth/')
    }
  }

  return (
    <div className={shellClass}>
      <button
        className="mobile-menu-button icon-button"
        type="button"
        onClick={() => setMobileOpen(true)}
        aria-label="打开导航"
        title="打开导航"
      >
        <Menu size={19} />
      </button>

      {mobileOpen && (
        <button
          className="sidebar-scrim"
          type="button"
          aria-label="关闭导航"
          onClick={() => setMobileOpen(false)}
        />
      )}

      <aside className={`sidebar${mobileOpen ? ' sidebar--open' : ''}`}>
        <div className="sidebar-brand">
          <img className="brand-mark" src="/web/brand-mark.svg" alt="" />
          {!collapsed && (
            <div className="brand-copy">
              <strong>枢衡</strong>
            </div>
          )}
          <button
            className="sidebar-mobile-close icon-button icon-button--dark"
            type="button"
            onClick={() => setMobileOpen(false)}
            aria-label="关闭导航"
            title="关闭导航"
          >
            <X size={18} />
          </button>
        </div>

        <GlobalSearch />

        <nav className="sidebar-nav" aria-label="主导航">
          {navigation.map((group) => (
            <div className="nav-group" key={group.label}>
              {!collapsed && <div className="nav-group-label">{group.label}</div>}
              {group.entries.map((entry) => {
                const Icon = entry.icon
                const active = location.pathname === entry.href
                const content = (
                  <>
                    <Icon size={18} aria-hidden="true" />
                    {!collapsed && <span>{entry.label}</span>}
                  </>
                )

                return <a className={`nav-item${active ? ' nav-item--active' : ''}`} href={`#${entry.href}`} key={entry.label} title={collapsed ? entry.label : undefined} onMouseEnter={() => preloadPage(entry.href)} onFocus={() => preloadPage(entry.href)}>{content}</a>
              })}
            </div>
          ))}
        </nav>

        <div className="sidebar-footer">
          <div className="sidebar-actions">
            <div className="sidebar-theme-wrap">
              <button
                className="sidebar-theme-button"
                type="button"
                onClick={() => setThemePickerOpen((value) => !value)}
                aria-label="选择界面主题"
                title="选择界面主题"
                aria-expanded={themePickerOpen}
              >
                {resolvedTheme === 'light' ? <Sun size={17} /> : <Moon size={17} />}
                {!collapsed && <span>{themePreference === 'system' ? '跟随系统' : themePreference === 'dark' ? '暗色' : '亮色'}</span>}
              </button>
              {themePickerOpen && <div className="theme-picker" role="menu" aria-label="界面主题">
                {([['system', '跟随系统', Monitor], ['light', '亮色', Sun], ['dark', '暗色', Moon]] as const).map(([value, label, Icon]) => <button className={themePreference === value ? 'is-selected' : ''} type="button" role="menuitemradio" aria-checked={themePreference === value} onClick={() => { setThemePreference(value); setThemePickerOpen(false) }} key={value}><Icon size={16} /><span>{label}</span>{themePreference === value && <i />}</button>)}
              </div>}
            </div>
            <button
              className="icon-button icon-button--dark"
              type="button"
              onClick={() => void logout()}
              aria-label="退出登录"
              title="退出登录"
            >
              <LogOut size={17} />
            </button>
            <button
              className="icon-button icon-button--dark sidebar-collapse"
              type="button"
              onClick={() => setCollapsed((value) => !value)}
              aria-label={collapsed ? '展开侧栏' : '收起侧栏'}
              title={collapsed ? '展开侧栏' : '收起侧栏'}
            >
              <PanelLeftClose className={collapsed ? 'icon-flipped' : ''} size={17} />
            </button>
          </div>
        </div>
      </aside>

      <main className="main-content">
        <Suspense fallback={<PageLoading />}>
          <Routes>
            <Route path="/" element={<DashboardPage />} />
            <Route path="/channels" element={<ChannelsPage />} />
            <Route path="/logs" element={<LogsPage />} />
            <Route path="/stats" element={<StatsPage />} />
            <Route path="/models" element={<ModelTestPage />} />
            <Route path="/model-test" element={<ModelTestPage />} />
            <Route path="/advanced" element={<Navigate to="/system" replace />} />
			<Route path="/settings" element={<Navigate to="/system" replace />} />
            <Route path="/system" element={<SystemSettingsPage />} />
            <Route path="/tokens" element={<TokensPage />} />
            <Route path="/trend" element={<TrendPage />} />
			<Route path="/fingerprints" element={<Navigate to="/models" replace />} />
            <Route path="/sites" element={<SitesPage />} />
            <Route path="/accounts" element={<AccountsPage />} />
            <Route path="/checkins" element={<CheckinsPage />} />
            <Route path="/announcements" element={<AnnouncementsPage />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </Suspense>
      </main>
    </div>
  )
}

function PageLoading() {
  return (
    <div className="page-loading" role="status">
      <Activity size={19} />
      <span>正在载入控制台</span>
    </div>
  )
}

export default App
