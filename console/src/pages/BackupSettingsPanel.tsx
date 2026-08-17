import { useCallback, useEffect, useRef, useState } from 'react'
import {
  CheckCircle2, CloudDownload, CloudUpload, DatabaseBackup, Download, FileJson, RefreshCw,
  Save, ShieldAlert, Trash2, Upload,
} from 'lucide-react'
import {
  exportBackup, getBackupWebDAV, importBackup, restoreBackupFromWebDAV,
  updateBackupWebDAV, uploadBackupToWebDAV,
} from '../api'
import type { BackupDocument, BackupImportResult, BackupType, BackupWebDAVConfig } from '../types'
import { ErrorState, LoadingState, OperationNotice, formatTime } from './shared'
import { siteErrorMessage } from './siteShared'

type WebDAVForm = Pick<BackupWebDAVConfig,
  'enabled' | 'file_url' | 'username' | 'export_type' | 'auto_sync_enabled' | 'auto_sync_interval_hours'
>

const defaultWebDAV: WebDAVForm = {
  enabled: false,
  file_url: '',
  username: '',
  export_type: 'all',
  auto_sync_enabled: false,
  auto_sync_interval_hours: 24,
}

const backupTypes: Array<{ value: BackupType; label: string; detail: string; tone: string }> = [
  { value: 'all', label: '完整备份', detail: '站点、账号、路由、令牌、通知与系统设置', tone: 'green' },
  { value: 'connections', label: '连接与路由', detail: '站点、账号凭证、渠道、上游密钥与下游令牌', tone: 'blue' },
  { value: 'settings', label: '系统设置', detail: '运行参数、通知设置与 WebDAV 配置', tone: 'amber' },
]

export function BackupSettingsPanel() {
  const inputRef = useRef<HTMLInputElement>(null)
  const [config, setConfig] = useState<BackupWebDAVConfig | null>(null)
  const [form, setForm] = useState<WebDAVForm>(defaultWebDAV)
  const [password, setPassword] = useState('')
  const [clearPassword, setClearPassword] = useState(false)
  const [pendingFile, setPendingFile] = useState<File | null>(null)
  const [pendingDocument, setPendingDocument] = useState<BackupDocument | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true)
    setError('')
    try {
      const data = await getBackupWebDAV(signal)
      setConfig(data)
      setForm({
        enabled: data.enabled,
        file_url: data.file_url || '',
        username: data.username || '',
        export_type: data.export_type || 'all',
        auto_sync_enabled: data.auto_sync_enabled,
        auto_sync_interval_hours: data.auto_sync_interval_hours || 24,
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

  const run = async (key: string, action: () => Promise<string>) => {
    setBusy(key)
    setError('')
    setNotice('')
    try { setNotice(await action()) }
    catch (reason) { setError(siteErrorMessage(reason)) }
    finally { setBusy('') }
  }

  const download = (type: BackupType) => void run(`download-${type}`, async () => {
    const document = await exportBackup(type)
    const blob = new Blob([JSON.stringify(document, null, 2)], { type: 'application/json' })
    const href = URL.createObjectURL(blob)
    const link = window.document.createElement('a')
    link.href = href
    link.download = `pivotflow-${type}-${new Date().toISOString().slice(0, 10)}.json`
    link.click()
    URL.revokeObjectURL(href)
    return `${backupTypeLabel(type)}已导出`
  })

  const inspectFile = async (file?: File) => {
    setError('')
    setNotice('')
    setPendingFile(null)
    setPendingDocument(null)
    if (!file) return
    if (file.size > 32 * 1024 * 1024) { setError('备份文件不能超过 32 MB'); return }
    try {
      const parsed = JSON.parse(await file.text()) as BackupDocument
      if (!parsed || parsed.version !== '1.0' || !['all', 'connections', 'settings'].includes(parsed.type)) throw new Error('unsupported')
      setPendingFile(file)
      setPendingDocument(parsed)
    } catch {
      setError('无法识别该备份文件，请选择 PivotFlow 导出的 JSON 文件')
    }
  }

  const restoreLocal = () => {
    if (!pendingDocument || !pendingFile) return
    if (!window.confirm(`从“${pendingFile.name}”导入${backupTypeLabel(pendingDocument.type)}？同名配置会被更新，现有日志和统计不会删除。`)) return
    void run('import-local', async () => {
      const result = await importBackup(pendingDocument)
      setPendingFile(null)
      setPendingDocument(null)
      if (inputRef.current) inputRef.current.value = ''
      return importSummary(result)
    })
  }

  const saveWebDAV = (event: React.FormEvent) => {
    event.preventDefault()
    void run('save-webdav', async () => {
      const data = await updateBackupWebDAV({
        ...form,
        ...(password.trim() ? { password } : {}),
        ...(clearPassword ? { clear_password: true } : {}),
      })
      setConfig(data)
      setPassword('')
      setClearPassword(false)
      return 'WebDAV 设置已保存'
    })
  }

  const uploadWebDAV = () => void run('upload-webdav', async () => {
    await uploadBackupToWebDAV(form.export_type)
    await load()
    return '备份已上传到 WebDAV'
  })

  const restoreWebDAV = () => {
    if (!window.confirm('从 WebDAV 恢复备份？同名配置会被更新，导入系统设置时服务可能重启。')) return
    void run('restore-webdav', async () => {
      const result = await restoreBackupFromWebDAV()
      await load()
      return importSummary(result)
    })
  }

  if (loading && !config) return <LoadingState label="正在加载备份设置" />
  if (error && !config) return <ErrorState message={error} retry={() => void load()} />

  return <section className="backup-settings">
    {notice && <OperationNotice onDismiss={() => setNotice('')}><CheckCircle2 size={15} />{notice}</OperationNotice>}
    {error && <OperationNotice tone="error" onDismiss={() => setError('')}>{error}</OperationNotice>}

    <div className="backup-security-note"><ShieldAlert size={18} /><div><strong>备份包含敏感凭证</strong><p>完整备份和连接备份包含账号凭证、上游密钥与下游令牌。请仅保存到可信设备或受保护的 WebDAV 空间。</p></div></div>

    <section className="backup-block">
      <header className="backup-block-header"><span className="backup-block-icon backup-block-icon--green"><DatabaseBackup size={20} /></span><div><h2>导出配置</h2><p>按用途选择备份范围，不会导出请求日志、公告缓存和运行统计</p></div></header>
      <div className="backup-type-grid">
        {backupTypes.map((item) => <article className={`backup-type-card backup-type-card--${item.tone}`} key={item.value}><span><FileJson size={19} /></span><div><strong>{item.label}</strong><p>{item.detail}</p></div><button className="secondary-button" type="button" onClick={() => download(item.value)} disabled={Boolean(busy)}>{busy === `download-${item.value}` ? <RefreshCw className="spin" size={15} /> : <Download size={15} />}导出</button></article>)}
      </div>
    </section>

    <section className="backup-block">
      <header className="backup-block-header"><span className="backup-block-icon backup-block-icon--blue"><Upload size={20} /></span><div><h2>从文件导入</h2><p>先检查文件类型与范围，确认后再写入当前实例</p></div></header>
      <div className={`backup-dropzone${pendingFile ? ' has-file' : ''}`} onDragOver={(event) => event.preventDefault()} onDrop={(event) => { event.preventDefault(); void inspectFile(event.dataTransfer.files[0]) }}>
        <input ref={inputRef} type="file" accept="application/json,.json" onChange={(event) => void inspectFile(event.target.files?.[0])} />
        <FileJson size={28} />
        <div>{pendingFile ? <><strong>{pendingFile.name}</strong><small>{backupTypeLabel(pendingDocument?.type || 'all')} · {formatBytes(pendingFile.size)} · 版本 {pendingDocument?.version}</small></> : <><strong>拖入备份文件，或点击选择</strong><small>支持 PivotFlow JSON 备份，最大 32 MB</small></>}</div>
        {pendingFile && <button className="icon-button icon-button--surface" type="button" onClick={(event) => { event.stopPropagation(); setPendingFile(null); setPendingDocument(null); if (inputRef.current) inputRef.current.value = '' }} aria-label="移除备份文件" title="移除"><Trash2 size={16} /></button>}
      </div>
      <footer className="backup-block-footer"><span>导入不会删除日志、公告与历史统计</span><button className="primary-button" type="button" onClick={restoreLocal} disabled={!pendingDocument || Boolean(busy)}>{busy === 'import-local' ? <RefreshCw className="spin" size={15} /> : <Upload size={15} />}开始导入</button></footer>
    </section>

    <form className="backup-block webdav-form" onSubmit={saveWebDAV}>
      <header className="backup-block-header"><span className="backup-block-icon backup-block-icon--amber"><CloudUpload size={20} /></span><div><h2>WebDAV 备份</h2><p>用于 NAS、坚果云等支持 WebDAV 的存储空间</p></div><BackupSwitch checked={form.enabled} change={(enabled) => setForm((current) => ({ ...current, enabled }))} label="启用 WebDAV" /></header>
      <div className="webdav-fields">
        <label className="webdav-url"><span>文件地址</span><input type="url" value={form.file_url} onChange={(event) => setForm((current) => ({ ...current, file_url: event.target.value }))} placeholder="https://dav.example.com/PivotFlow/backup.json" /></label>
        <label><span>用户名</span><input value={form.username} onChange={(event) => setForm((current) => ({ ...current, username: event.target.value }))} autoComplete="username" /></label>
        <label><span>密码</span><input type="password" value={password} onChange={(event) => { setPassword(event.target.value); setClearPassword(false) }} autoComplete="new-password" placeholder={config?.password_configured ? '留空保持现有密码' : 'WebDAV 密码'} /></label>
        <label><span>备份范围</span><select value={form.export_type} onChange={(event) => setForm((current) => ({ ...current, export_type: event.target.value as BackupType }))}>{backupTypes.map((item) => <option value={item.value} key={item.value}>{item.label}</option>)}</select></label>
        <label><span>自动备份间隔</span><select value={form.auto_sync_interval_hours} onChange={(event) => setForm((current) => ({ ...current, auto_sync_interval_hours: Number(event.target.value) }))}><option value={6}>每 6 小时</option><option value={12}>每 12 小时</option><option value={24}>每天</option><option value={72}>每 3 天</option><option value={168}>每周</option></select></label>
        <div className="webdav-switch-field"><span>定时备份</span><BackupSwitch checked={form.auto_sync_enabled} change={(auto_sync_enabled) => setForm((current) => ({ ...current, auto_sync_enabled }))} label="启用定时备份" /></div>
      </div>
      {config?.password_configured && <label className="webdav-clear-password"><input type="checkbox" checked={clearPassword} onChange={(event) => { setClearPassword(event.target.checked); if (event.target.checked) setPassword('') }} /><span>清除已保存的 WebDAV 密码</span></label>}
      <footer className="backup-block-footer webdav-footer"><div><span>{config?.last_sync_at ? `最近同步 ${formatTime(config.last_sync_at)}` : '尚未同步'}</span>{config?.last_error && <em>{config.last_error}</em>}</div><div><button className="secondary-button" type="button" onClick={restoreWebDAV} disabled={!config?.enabled || Boolean(busy)}><CloudDownload size={15} />从 WebDAV 恢复</button><button className="secondary-button" type="button" onClick={uploadWebDAV} disabled={!config?.enabled || Boolean(busy)}>{busy === 'upload-webdav' ? <RefreshCw className="spin" size={15} /> : <CloudUpload size={15} />}立即上传</button><button className="primary-button" type="submit" disabled={Boolean(busy)}>{busy === 'save-webdav' ? <RefreshCw className="spin" size={15} /> : <Save size={15} />}保存设置</button></div></footer>
    </form>
  </section>
}

function BackupSwitch({ checked, change, label }: { checked: boolean; change: (value: boolean) => void; label: string }) {
  return <button className={`switch${checked ? ' switch--on' : ''}`} type="button" role="switch" aria-checked={checked} aria-label={label} title={label} onClick={() => change(!checked)}><i /></button>
}

function backupTypeLabel(type: BackupType): string { return backupTypes.find((item) => item.value === type)?.label || '备份' }
function formatBytes(value: number): string { return value < 1024 * 1024 ? `${Math.max(1, Math.round(value / 1024))} KB` : `${(value / 1024 / 1024).toFixed(1)} MB` }
function importSummary(result: BackupImportResult): string {
  const total = result.sites + result.accounts + result.channels + result.tokens + result.settings
  return `导入完成，共更新 ${total} 项${result.restart_required ? '，服务将自动重启' : ''}`
}
