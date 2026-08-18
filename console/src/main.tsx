import React from 'react'
import ReactDOM from 'react-dom/client'
import { HashRouter } from 'react-router-dom'
import App from './App'
import { applyTheme, readThemePreference } from './theme'
import './styles.css'

declare global {
  interface Window {
    __PIVOTFLOW_AUTH_REDIRECTING__?: boolean
  }
}

const TOKEN_KEY = 'pivotflow_token'
const EXPIRY_KEY = 'pivotflow_token_expiry'

applyTheme(readThemePreference())

function loginURL(error?: string): string {
  const redirect = `${window.location.pathname}${window.location.search}${window.location.hash}`
  const params = new URLSearchParams({ redirect })
  if (error) params.set('error', error)
  return `/web/auth/?${params}`
}

function clearSession(): void {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(EXPIRY_KEY)
  localStorage.removeItem('pivotflow_web_role')
}

function redirectToLogin(error?: string): void {
  clearSession()
  window.__PIVOTFLOW_AUTH_REDIRECTING__ = true
  window.location.replace(loginURL(error))
}

function renderConsole(): void {
  ReactDOM.createRoot(document.getElementById('root')!).render(
    <React.StrictMode><HashRouter><App /></HashRouter></React.StrictMode>,
  )
}

async function bootstrap(): Promise<void> {
  const token = localStorage.getItem(TOKEN_KEY)
  const expiry = Number(localStorage.getItem(EXPIRY_KEY) || 0)
  if (!token || (expiry > 0 && Date.now() >= expiry)) {
    redirectToLogin()
    return
  }

  try {
    const response = await fetch('/dashboard/session', { cache: 'no-store', headers: { Authorization: `Bearer ${token}` } })
    const payload = await response.json().catch(() => null) as { success?: boolean; data?: { role?: string } } | null
    if (!response.ok || !payload?.success || payload.data?.role !== 'admin') {
      redirectToLogin(response.status === 403 ? '请使用管理员密码登录' : undefined)
      return
    }
    localStorage.setItem('pivotflow_web_role', 'admin')
    renderConsole()
  } catch (_) {
    const root = document.getElementById('root')!
    root.innerHTML = '<div class="console-boot" role="alert"><div><img src="/web/brand-mark.svg" alt=""><span>控制台连接失败，请刷新页面重试</span></div></div>'
  }
}

if (!window.__PIVOTFLOW_AUTH_REDIRECTING__) void bootstrap()
