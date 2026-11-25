import { useEffect, useMemo, useState } from 'react'
import { AlertTriangle, CheckCircle2, RefreshCw, Search } from 'lucide-react'
import HelpTip from '../components/HelpTip'
import {
  canApplyPreview,
  configPreviewEntityLabel,
  configPreviewErrorMessage,
  configPreviewFieldLabel,
  configPreviewMessageLabel,
  configPreviewStatusLabel,
  configPreviewWarningLabel,
  filterPreviewItems,
  previewActionLabel,
  previewActions,
  previewExpiresAt,
  safeConfigPreviewLocation,
  summarizePreviewItems,
  type ConfigPreview,
  type ConfigPreviewAction,
} from '../configPreview.ts'
import '../configPreview.css'

interface ConfigPreviewPanelProps {
  preview: ConfigPreview
  applying?: boolean
  applyMessage?: string
  onApply?: () => void
  onReload?: () => void
  reloadLabel?: string
  reloading?: boolean
  onRecheck?: () => void
  recheckLabel?: string
  rechecking?: boolean
  disableApply?: boolean
}

export default function ConfigPreviewPanel({
  preview,
  applying = false,
  applyMessage = '',
  onApply,
  onReload,
  reloadLabel = '重新生成预览',
  reloading = false,
  onRecheck,
  recheckLabel = '重新读取状态',
  rechecking = false,
  disableApply = false,
}: ConfigPreviewPanelProps) {
  const [search, setSearch] = useState('')
  const [action, setAction] = useState<ConfigPreviewAction | 'all'>('all')
  const [clock, setClock] = useState(Date.now())

  useEffect(() => {
    if (preview.status !== 'ready' || previewExpiresAt(preview.expires_at) === undefined) return
    const timer = window.setInterval(() => setClock(Date.now()), 1_000)
    return () => window.clearInterval(timer)
  }, [preview.expires_at, preview.status])

  const summary = useMemo(() => summarizePreviewItems(preview.items), [preview.items])
  const normalizedPreview = useMemo(() => ({ ...preview, summary }), [preview, summary])
  const visibleItems = useMemo(() => filterPreviewItems(preview.items, search, action), [action, preview.items, search])
  const expiry = previewExpiresAt(preview.expires_at)
  const expired = expiry !== undefined && expiry <= clock
  const isApplying = applying || preview.status === 'applying'
  const safePreviewError = preview.error ? configPreviewErrorMessage(preview.error) : ''
  const resultWarning = preview.result?.warning ? configPreviewWarningLabel(preview.result.warning) : ''
  const applyAllowed = canApplyPreview(normalizedPreview, clock) && !disableApply && !reloading && !rechecking && !isApplying
  const resultTotal = preview.result?.backup
    ? preview.result.backup.sites
      + preview.result.backup.accounts
      + preview.result.backup.channels
      + (preview.result.backup.bindings || 0)
      + preview.result.backup.tokens
      + preview.result.backup.settings
    : 0

  let blockedReason = ''
  if (preview.status === 'unknown') {
    blockedReason = preview.error?.startsWith('commit_unknown')
      ? '数据库提交结果待确认，请人工核对当前配置；请勿重复提交。'
      : '操作结果待确认，请重新读取状态；请勿重复提交。'
  }
  else if (safePreviewError) blockedReason = `此预览不能应用：${safePreviewError}`
  else if (isApplying) blockedReason = '预览正在应用，请稍后重新读取状态。'
  else if (expired) blockedReason = '预览已过期，请重新生成后再确认。'
  else if (summary.conflict > 0) blockedReason = '预览包含冲突或错误条目，修正来源配置后请重新生成。'
  else if (preview.status === 'stale') blockedReason = '配置已发生变化，此预览不能应用，请重新生成。'
  else if (preview.status === 'failed') blockedReason = '上次应用失败，请根据安全错误位置核对后重新生成预览。'
  else if (preview.status === 'applied') blockedReason = '此预览已经应用，无需重复提交。'
  else if (!preview.can_apply) blockedReason = '服务器当前不允许应用此预览，请重新生成。'
  else if (reloading) blockedReason = '正在重新生成预览，请等待新的差异结果。'

  return (
    <section className="config-preview-panel" aria-label="配置差异预览">
      <header className="config-preview-header">
        <div>
          <div className="heading-with-hint"><h3>确认将发生的变更</h3><HelpTip label="变更预览说明" text={`${preview.kind === 'backup' ? '备份导入' : preview.kind === 'browser_import' ? '浏览器导入' : '站点渠道同步'}：预览只展示字段名与结果摘要，不包含凭证、请求规则、代理地址或设置值。`} /></div>
        </div>
        <span className={`config-preview-status config-preview-status--${preview.status}`}>
          {preview.status === 'applied'
            ? <CheckCircle2 size={14} />
            : isApplying
              ? <RefreshCw className="spin" size={14} />
              : preview.status === 'failed' || preview.status === 'stale' || preview.status === 'unknown'
                ? <AlertTriangle size={14} />
                : null}
          {configPreviewStatusLabel(preview.status)}
        </span>
      </header>

      {safePreviewError && (
        <div className="config-preview-error" role="alert">
          <AlertTriangle size={15} />
          <span>{safePreviewError}</span>
        </div>
      )}

      <div className="config-preview-counts" role="group" aria-label="按变更类型筛选">
        <button type="button" className={action === 'all' ? 'is-active' : ''} aria-pressed={action === 'all'} onClick={() => setAction('all')}>
          <span>全部</span><strong>{preview.items.length}</strong>
        </button>
        {previewActions.map((itemAction) => (
          <button
            type="button"
            className={`config-preview-count config-preview-count--${itemAction}${action === itemAction ? ' is-active' : ''}`}
            aria-pressed={action === itemAction}
            aria-label={`${previewActionLabel(itemAction)} ${summary[itemAction]} 项`}
            onClick={() => setAction(itemAction)}
            key={itemAction}
          >
            <span>{previewActionLabel(itemAction)}</span><strong>{summary[itemAction]}</strong>
          </button>
        ))}
      </div>

      <div className="config-preview-tools">
        <label className="search-field">
          <Search size={15} />
          <input
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="搜索对象、字段或说明"
            aria-label="搜索预览条目"
          />
          <span>{visibleItems.length}/{preview.items.length}</span>
        </label>
        {expiry !== undefined && (
          <span className={`config-preview-expiry${expired ? ' is-expired' : ''}`}>
            {expired ? '预览已过期' : `有效至 ${formatPreviewTime(expiry)}`}
          </span>
        )}
      </div>

      <div className="config-preview-items" aria-live="polite">
        {visibleItems.length === 0 ? (
          <div className="config-preview-empty">没有符合当前筛选条件的条目</div>
        ) : visibleItems.map((item) => {
          const location = safeConfigPreviewLocation(item.key)
          return (
            <article className={`config-preview-item config-preview-item--${item.action}`} key={item.key}>
              <div className="config-preview-item-main">
                <span className="config-preview-entity">{configPreviewEntityLabel(item.entity)}</span>
                <strong>{item.label || configPreviewEntityLabel(item.entity)}</strong>
                {location && <span className="config-preview-location">条目位置：{location}</span>}
              </div>
              <span className={`config-preview-action config-preview-action--${item.action}`}>{previewActionLabel(item.action)}</span>
              <div className="config-preview-fields">
                {item.fields.length > 0 && <div><span>变更字段</span>{item.fields.map((field) => <span className="config-preview-field" key={`field-${field}`}>{configPreviewFieldLabel(field)}</span>)}</div>}
                {item.preserved_fields.length > 0 && <div><span>保留字段</span>{item.preserved_fields.map((field) => <span className="config-preview-field" key={`preserved-${field}`}>{configPreviewFieldLabel(field)}</span>)}</div>}
                {item.fields.length === 0 && item.preserved_fields.length === 0 && <span className="config-preview-no-fields">没有字段变化</span>}
              </div>
              {item.message && <p className="config-preview-message">{configPreviewMessageLabel(item.message)}</p>}
            </article>
          )
        })}
      </div>

      {preview.status === 'applied' && (
        <div className="config-preview-result" role="status">
          <CheckCircle2 size={16} />
          {preview.result?.backup
            ? `已应用，共更新 ${resultTotal} 项${preview.result.backup.restart_required ? '；系统设置变化需要重启服务' : ''}`
            : '此预览已成功应用'}
        </div>
      )}
      {resultWarning && (
        <div className="config-preview-warning" role="status">
          <AlertTriangle size={15} />
          <span>{resultWarning}</span>
        </div>
      )}
      {blockedReason && <div className="config-preview-blocked" role="status"><AlertTriangle size={15} />{blockedReason}</div>}
      {applyMessage && <div className="config-preview-apply-message" role="status">{applyMessage}</div>}

      {(onApply || onReload || onRecheck) && (
        <footer className="config-preview-footer">
          <span>{isApplying ? '正在应用已确认的固定快照' : `共 ${preview.items.length} 项，筛选后 ${visibleItems.length} 项`}</span>
          <div>
            {onRecheck && (preview.status === 'unknown' || preview.status === 'applying') && <button className="secondary-button" type="button" onClick={onRecheck} disabled={applying || rechecking}>{rechecking ? <RefreshCw className="spin" size={15} /> : <RefreshCw size={15} />}{recheckLabel}</button>}
            {onReload && <button className="secondary-button" type="button" onClick={onReload} disabled={isApplying || reloading || rechecking}>{reloading ? <RefreshCw className="spin" size={15} /> : <RefreshCw size={15} />}{reloadLabel}</button>}
            {onApply && <button className="primary-button" type="button" onClick={onApply} disabled={!applyAllowed}>{isApplying ? <RefreshCw className="spin" size={15} /> : <CheckCircle2 size={15} />}{isApplying ? '应用中' : preview.status === 'applied' ? '已应用' : '确认应用'}</button>}
          </div>
        </footer>
      )}
    </section>
  )
}

function formatPreviewTime(value: number): string {
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(new Date(value))
}
