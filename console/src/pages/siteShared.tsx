import { useEffect } from 'react'
import { X } from 'lucide-react'
import type { SiteAccount } from '../types'

export function Modal({ title, children, close, wide = false }: {
  title: string
  children: React.ReactNode
  close: () => void
  wide?: boolean
}) {
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => { if (event.key === 'Escape') close() }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [close])
  return (
    <div className="modal-layer" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) close() }}>
      <section className={`console-modal${wide ? ' console-modal--wide' : ''}`} role="dialog" aria-modal="true" aria-label={title}>
        <header><h2>{title}</h2><button className="icon-button icon-button--surface" type="button" onClick={close} aria-label="关闭弹窗"><X size={17} /></button></header>
        {children}
      </section>
    </div>
  )
}

export function StatusBadge({ status }: { status?: string }) {
  return <span className={`status-badge status-badge--${statusTone(status)}`}>{statusLabel(status)}</span>
}

export function statusTone(status?: string): string {
  if (['healthy', 'success', 'already_checked', 'active'].includes(status || '')) return 'success'
  if (['degraded', 'partial', 'browser_required', 'running', 'queued'].includes(status || '')) return 'warning'
  if (['expired', 'error', 'failed'].includes(status || '')) return 'danger'
  return 'muted'
}

export function statusLabel(status?: string): string {
  const labels: Record<string, string> = {
    healthy: '正常', degraded: '降级', expired: '已过期', disabled: '已禁用', active: '启用', inactive: '已停用', error: '异常', unknown: '未知',
    success: '成功', failed: '失败', already_checked: '已签到', browser_required: '需浏览器', unsupported: '不支持',
    running: '运行中', queued: '排队中', partial: '部分成功', cancelled: '已取消', never: '未发送',
  }
  return labels[status || ''] || status || '未知'
}

export function formatAccountBalance(account: SiteAccount): string {
  if (account.balance == null) return '—'
  const currency = (account.balance_currency || 'USD').toUpperCase()
  const symbol = currency === 'CNY' || currency === 'RMB' ? '￥' : currency === 'EUR' ? '€' : currency === 'GBP' ? '£' : currency === 'JPY' ? '¥' : currency === 'USD' || currency === 'USDT' ? '$' : `${currency} `
  return `${symbol}${new Intl.NumberFormat('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(account.balance)}`
}

export function siteErrorMessage(reason: unknown): string {
  const raw = reason instanceof Error ? reason.message : String(reason || '请求失败')
	const separator = raw.indexOf(': ')
	const code = separator > 0 ? raw.slice(0, separator) : raw
	const detail = separator > 0 ? raw.slice(separator + 2).trim() : ''
  const labels: Record<string, string> = {
	credential_locked: '凭证加密密钥不可用，请检查数据目录是否可写或主密钥配置是否正确', browser_required: '该站点需要打开浏览器完成验证',
    provider_timeout: '站点请求超时', provider_rate_limited: '站点触发限流，请稍后再试', api_key_required: '投影需要 API Key',
		models_required: '请先同步账号模型', routing_api_key_unavailable: '没有读取到可用的模型 API Key，请先在上游站点创建一个 Key 后再次同步', unsupported: '当前站点不支持此操作', conflict: '已有任务运行或投影存在冲突', expired: '系统访问令牌或登录会话已失效，请在账号管理中更新凭证',
		user_id_required: '无法识别或验证上游用户 ID，请核对用户个人中心显示的数字 ID', credential_required: '请填写新的登录凭证', site_name_exists: '已有同名站点，请修改名称或检查未删除的数据',
	request_failed: '上游请求失败', invalid_response: '上游返回了无法识别的数据', webdav_html_response: 'WebDAV 返回了网页而不是备份文件，请填写 WebDAV 的完整文件地址，不要填写网页登录地址',
		'cookie credential requires session cookie and user_id': 'Session Cookie 账号必须填写上游用户 ID',
  }
	const label = labels[code]
	if (code.startsWith('webdav_http_')) {
		const status = code.slice('webdav_http_'.length)
		return detail || `WebDAV 请求失败（HTTP ${status}）`
	}
	return label ? detail ? `${label}：${detail}` : label : raw
}
