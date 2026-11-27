import { useCallback, useEffect, useId, useRef, useState } from 'react'
import {
  CheckCircle2, CircleAlert, Clock3, CloudDownload, CloudUpload, DatabaseBackup, Download, FileJson, RefreshCw,
  Save, ShieldAlert, ShieldCheck, Trash2, Upload,
} from 'lucide-react'
import {
  exportBackup, getBackupWebDAV, updateBackupWebDAV, uploadBackupToWebDAV,
} from '../api'
import {
  applyConfigPreview, configPreviewErrorMessage, discardConfigPreview, getConfigPreview, markPreviewObservationUnknown, mergePreviewApplyResult,
  previewBackup, previewBackupWebDAV, type ConfigPreview,
} from '../configPreview.ts'
import HelpTip from '../components/HelpTip'
import {
  backupErrorMessage, backupUploadFeedback, backupVerificationRecord,
  type BackupRecordState, type BackupUploadFeedback,
} from '../backupVerification.ts'
import type { BackupDocument, BackupType, BackupVerification, BackupWebDAVConfig } from '../types'
import ConfigPreviewPanel from './ConfigPreviewPanel'
import { ErrorState, LoadingState, OperationNotice, formatTime } from './shared'
import { Modal } from './siteShared'
import './backupPresentation.css'

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
  { value: 'all', label: '全部配置', detail: '站点、账号、模型、绑定、路由、令牌、通知与系统设置；不含运行历史和计费回执', tone: 'green' },
  { value: 'connections', label: '连接与路由', detail: '站点、账号凭证、渠道、上游密钥与下游令牌', tone: 'blue' },
  { value: 'settings', label: '系统设置', detail: '运行参数、通知设置与 WebDAV 配置', tone: 'amber' },
]

export function BackupSettingsPanel() {
  const webdavURLId = useId()
  const inputRef = useRef<HTMLInputElement>(null)
  const [config, setConfig] = useState<BackupWebDAVConfig | null>(null)
  const [form, setForm] = useState<WebDAVForm>(defaultWebDAV)
  const [password, setPassword] = useState('')
  const [clearPassword, setClearPassword] = useState(false)
  const [pendingFile, setPendingFile] = useState<File | null>(null)
  const [pendingDocument, setPendingDocument] = useState<BackupDocument | null>(null)
  const [preview, setPreview] = useState<ConfigPreview | null>(null)
  const [previewSource, setPreviewSource] = useState<'local' | 'webdav'>('local')
  const [previewTitle, setPreviewTitle] = useState('')
  const [previewBusy, setPreviewBusy] = useState<'' | 'local' | 'webdav'>('')
  const [applyingPreview, setApplyingPreview] = useState(false)
  const [recheckingPreview, setRecheckingPreview] = useState(false)
  const [applyMessage, setApplyMessage] = useState('')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState('')
  const [error, setError] = useState('')
  const [noticeState, setNoticeState] = useState<{ text: string; tone: 'success' | 'warning' }>({ text: '', tone: 'success' })
  const [latestUpload, setLatestUpload] = useState<BackupUploadFeedback | null>(null)
  const setNotice = useCallback((text: string, tone: 'success' | 'warning' = 'success') => setNoticeState({ text, tone }), [])
  const { text: notice, tone: noticeTone } = noticeState
  const operationSequence = useRef(0)
  const previewController = useRef<AbortController | null>(null)
  const previewRef = useRef<ConfigPreview | null>(null)
  const applyingRef = useRef(false)

  const replacePreview = useCallback((next: ConfigPreview | null) => {
    previewRef.current = next
    setPreview(next)
  }, [])

  const discardPreview = useCallback((candidate: ConfigPreview | null) => {
    if (!candidate || candidate.status === 'applying' || applyingRef.current) return
    void discardConfigPreview(candidate.id).catch(() => undefined)
  }, [])

  const clearPendingLocalFile = useCallback(() => {
    setPendingFile(null)
    setPendingDocument(null)
    if (inputRef.current) inputRef.current.value = ''
  }, [])

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
      if (!signal?.aborted) setError(backupErrorMessage(reason))
    } finally {
      if (!signal?.aborted) setLoading(false)
    }
  }, [])

  useEffect(() => {
    const controller = new AbortController()
    void load(controller.signal)
    return () => controller.abort()
  }, [load])
  useEffect(() => () => {
    operationSequence.current += 1
    previewController.current?.abort()
    const current = previewRef.current
    if (current && current.status !== 'applying' && !applyingRef.current) {
      void discardConfigPreview(current.id).catch(() => undefined)
    }
  }, [])

  const run = async (key: string, action: () => Promise<string | { message: string; tone: 'success' | 'warning' }>) => {
    setBusy(key)
    setError('')
    setNotice('')
    try {
      const result = await action()
      if (typeof result === 'string') setNotice(result)
      else setNotice(result.message, result.tone)
    }
    catch (reason) { setError(backupErrorMessage(reason)) }
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
    const sequence = ++operationSequence.current
    previewController.current?.abort()
    discardPreview(previewRef.current)
    setError('')
    setNotice('')
    setPendingFile(null)
    setPendingDocument(null)
    replacePreview(null)
    setPreviewBusy('')
    setApplyingPreview(false)
    setApplyMessage('')
    if (!file) return
    if (file.size > 32 * 1024 * 1024) { setError('备份文件不能超过 32 MB'); return }
    try {
      const parsed = JSON.parse(await file.text()) as BackupDocument
      if (sequence !== operationSequence.current) return
      if (!parsed || parsed.version !== '1.0' || !['all', 'connections', 'settings'].includes(parsed.type)) throw new Error('unsupported')
      setPendingFile(file)
      setPendingDocument(parsed)
    } catch {
      if (sequence !== operationSequence.current) return
      setError('无法识别该备份文件，请选择 PivotFlow 导出的 JSON 文件')
    }
  }

  const stageLocalPreview = async () => {
    if (!pendingDocument || !pendingFile) return
    const sequence = ++operationSequence.current
    const controller = new AbortController()
    previewController.current?.abort()
    previewController.current = controller
    discardPreview(previewRef.current)
    replacePreview(null)
    setPreviewBusy('local')
    setError('')
    setNotice('')
    try {
      const next = await previewBackup(pendingDocument, controller.signal)
      if (controller.signal.aborted || sequence !== operationSequence.current) {
        discardPreview(next)
        return
      }
      replacePreview(next)
      setPreviewSource('local')
      setPreviewTitle(`预览导入“${pendingFile.name}”`)
      setApplyMessage('')
      clearPendingLocalFile()
    } catch (reason) {
      if (!controller.signal.aborted && sequence === operationSequence.current) setError(configPreviewErrorMessage(reason))
    } finally {
      if (sequence === operationSequence.current) setPreviewBusy('')
    }
  }

  const stageWebDAVPreview = async () => {
    const sequence = ++operationSequence.current
    const controller = new AbortController()
    previewController.current?.abort()
    previewController.current = controller
    discardPreview(previewRef.current)
    replacePreview(null)
    setPreviewBusy('webdav')
    setError('')
    setNotice('')
    try {
      const next = await previewBackupWebDAV(controller.signal)
      if (controller.signal.aborted || sequence !== operationSequence.current) {
        discardPreview(next)
        return
      }
      replacePreview(next)
      setPreviewSource('webdav')
      setPreviewTitle('预览 WebDAV 恢复')
      setApplyMessage('')
    } catch (reason) {
      if (!controller.signal.aborted && sequence === operationSequence.current) setError(configPreviewErrorMessage(reason))
    } finally {
      if (sequence === operationSequence.current) setPreviewBusy('')
    }
  }

  const dismissPreview = () => {
    operationSequence.current += 1
    previewController.current?.abort()
    discardPreview(previewRef.current)
    replacePreview(null)
    setApplyingPreview(false)
    setApplyMessage('')
  }

  const recheckPreview = async () => {
    const currentPreview = previewRef.current
    if (!currentPreview || applyingRef.current || recheckingPreview) return
    const sequence = operationSequence.current
    setRecheckingPreview(true)
    setApplyMessage('正在重新读取应用状态…')
    try {
      const current = await getConfigPreview(currentPreview.id)
      if (sequence !== operationSequence.current) return
      replacePreview(current)
      if (current.status === 'applied') {
        setNotice('已确认配置应用完成')
        setApplyMessage('')
        if (previewSource === 'local') clearPendingLocalFile()
        await load()
      } else if (current.status === 'ready' && current.can_apply) {
        setApplyMessage('服务器已确认预览仍可应用；请核对后再次手动确认')
      } else if (current.status === 'applying') {
        setApplyMessage('预览仍在应用，请稍后再次读取状态')
      } else if (current.status === 'unknown') {
        setApplyMessage('提交结果待确认，请人工核对当前配置，请勿重复提交')
      } else if (current.status === 'stale') {
        setApplyMessage('配置已发生变化，旧预览不能应用，请重新生成预览')
      } else {
        setApplyMessage('预览当前不能应用，请根据安全错误位置核对后重新生成预览')
      }
    } catch (reason) {
      if (sequence === operationSequence.current) {
        setApplyMessage(`重新读取应用状态失败：${configPreviewErrorMessage(reason)}`)
      }
    } finally {
      if (sequence === operationSequence.current) setRecheckingPreview(false)
    }
  }

  const applyPreview = async () => {
    const currentPreview = previewRef.current
    if (!currentPreview || applyingRef.current) return
    applyingRef.current = true
    const sequence = operationSequence.current
    const previewID = currentPreview.id
    setApplyingPreview(true)
    setApplyMessage('')
    try {
      const result = await applyConfigPreview(previewID, AbortSignal.timeout(30_000))
      if (sequence !== operationSequence.current) return
      const merged = mergePreviewApplyResult(currentPreview, result)
      replacePreview(merged)
      if (result.status === 'applied') {
        setNotice(result.backup?.restart_required ? '配置已应用，系统设置发生变化，服务将重启' : '已应用预览中的固定快照')
        if (previewSource === 'local') clearPendingLocalFile()
        await load()
      } else {
        setApplyMessage(
          result.status === 'stale'
            ? '配置已发生变化，请重新生成预览'
            : result.status === 'unknown'
              ? '提交结果待确认，请人工核对当前配置，请勿重复提交'
              : result.status === 'applying'
                ? '预览仍在应用，请稍后重新读取状态'
                : '应用失败，请根据安全错误位置核对后重新生成预览',
        )
      }
    } catch {
      if (sequence !== operationSequence.current) return
      replacePreview(markPreviewObservationUnknown(currentPreview))
      setApplyMessage('应用响应待确认，正在重新读取预览状态…')
      try {
        const current = await getConfigPreview(previewID)
        if (sequence !== operationSequence.current) return
        replacePreview(current)
        if (current.status === 'applied') {
          setNotice('已确认配置应用完成')
          if (previewSource === 'local') clearPendingLocalFile()
          await load()
        } else if (current.status === 'ready' && current.can_apply) {
          setApplyMessage('服务器已确认预览仍可应用；请核对后再次手动确认')
        } else if (current.status === 'applying') {
          setApplyMessage('预览仍在应用，请稍后重新读取状态')
        } else if (current.status === 'unknown') {
          setApplyMessage('提交结果待确认，请人工核对当前配置，请勿重复提交')
        } else if (current.status === 'stale') {
          setApplyMessage('配置已发生变化，旧预览不能应用，请重新生成')
        } else if (current.status === 'failed') {
          setApplyMessage('应用失败，请根据安全错误位置核对后重新生成预览')
        } else {
          setApplyMessage('预览状态仍不能应用，请重新生成预览')
        }
      } catch {
        if (sequence === operationSequence.current) {
          setApplyMessage('操作结果待确认，重新读取失败；请稍后手动复查，请勿重复提交')
        }
      }
    } finally {
      if (sequence === operationSequence.current) setApplyingPreview(false)
      applyingRef.current = false
    }
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
      setLatestUpload(null)
      setPassword('')
      setClearPassword(false)
      const warnings = backupVerificationRecord(data).warnings
      return warnings.length
        ? { message: ['WebDAV 设置已保存', ...warnings].join('；'), tone: 'warning' }
        : 'WebDAV 设置已保存'
    })
  }

  const uploadWebDAV = () => void run('upload-webdav', async () => {
    setLatestUpload(null)
    const response = await uploadBackupToWebDAV(form.export_type)
    const feedback = backupUploadFeedback(response)
    setLatestUpload(feedback)
    await load()
    return feedback
  })

  const verification = backupVerificationRecord(config)
  const observedProof = latestUpload?.proof
  const showObservedProof = observedProof && (latestUpload?.metadataSaved !== true || verification.state !== 'verified'
    || verification.proof?.verified_at !== observedProof.verified_at || verification.proof?.sha256 !== observedProof.sha256
    || verification.proof?.bytes !== observedProof.bytes || verification.proof?.type !== observedProof.type
    || verification.proof?.warning_count !== observedProof.warning_count)

  if (loading && !config) return <LoadingState label="正在加载备份设置" />
  if (error && !config) return <ErrorState message={error} retry={() => void load()} />

  return <>
    <section className="backup-settings">
    {notice && <OperationNotice className="backup-operation-notice" tone={noticeTone} persistent={noticeTone === 'warning'} onDismiss={() => setNotice('')}>{noticeTone === 'success' ? <CheckCircle2 size={15} /> : <CircleAlert size={15} />}<span>{notice}</span></OperationNotice>}
    {error && <OperationNotice className="backup-operation-notice" tone="error" onDismiss={() => setError('')}>{error}</OperationNotice>}

    <div className="backup-security-note"><ShieldAlert size={18} /><div><div className="heading-with-hint"><strong>备份包含敏感凭证</strong><HelpTip label="备份凭证" text="全部配置和连接备份包含账号凭证、上游密钥与下游令牌。配置备份不包含日志或结算回执，恢复整台实例需要按部署文档备份数据库与主密钥。" /></div><p>请保存到可信设备或受保护的 WebDAV 空间。</p></div></div>

    <section className="backup-block">
      <header className="backup-block-header"><span className="backup-block-icon backup-block-icon--green"><DatabaseBackup size={20} /></span><div><div className="heading-with-hint"><h2>导出配置</h2><HelpTip label="导出配置" text="用于配置迁移，不包含请求日志、计费日志、结算回执、余额观察和运行统计。恢复整台实例需要成组备份数据库及主密钥。" /></div></div></header>
      <div className="backup-type-grid">
        {backupTypes.map((item) => <article className={`backup-type-card backup-type-card--${item.tone}`} key={item.value}><span><FileJson size={19} /></span><div className="heading-with-hint"><strong>{item.label}</strong><HelpTip label={item.label} text={item.detail} /></div><button className="secondary-button" type="button" onClick={() => download(item.value)} disabled={Boolean(busy)}>{busy === `download-${item.value}` ? <RefreshCw className="spin" size={15} /> : <Download size={15} />}导出</button></article>)}
      </div>
    </section>

    <section className="backup-block">
      <header className="backup-block-header"><span className="backup-block-icon backup-block-icon--blue"><Upload size={20} /></span><div><div className="heading-with-hint"><h2>从配置文件恢复</h2><HelpTip label="从配置文件恢复" text="先预览将新增、更新、保留、冲突与停用的条目，再确认应用同一份快照。支持 PivotFlow JSON 备份，最大 32 MB。" /></div></div></header>
      <div className={`backup-dropzone${pendingFile ? ' has-file' : ''}`} onDragOver={(event) => event.preventDefault()} onDrop={(event) => { event.preventDefault(); void inspectFile(event.dataTransfer.files[0]) }}>
        <input ref={inputRef} type="file" accept="application/json,.json" onChange={(event) => void inspectFile(event.target.files?.[0])} />
        <FileJson size={28} />
        <div>{pendingFile ? <><strong>{pendingFile.name}</strong><small>{backupTypeLabel(pendingDocument?.type || 'all')} · {formatBytes(pendingFile.size)} · 版本 {pendingDocument?.version}</small></> : <><strong>拖入备份文件，或点击选择</strong><small>支持 PivotFlow JSON 备份，最大 32 MB</small></>}</div>
        {pendingFile && <button className="icon-button icon-button--surface" type="button" onClick={(event) => { event.stopPropagation(); void inspectFile(undefined) }} aria-label="移除备份文件" title="移除"><Trash2 size={16} /></button>}
      </div>
      <footer className="backup-block-footer backup-import-footer"><button className="primary-button" type="button" onClick={() => void stageLocalPreview()} disabled={!pendingDocument || Boolean(busy) || Boolean(previewBusy)}>{previewBusy === 'local' ? <RefreshCw className="spin" size={15} /> : <Upload size={15} />}{previewBusy === 'local' ? '正在预览' : '预览导入'}</button></footer>
    </section>

    <form className="backup-block webdav-form" onSubmit={saveWebDAV}>
      <header className="backup-block-header"><span className="backup-block-icon backup-block-icon--amber"><CloudUpload size={20} /></span><div><h2>WebDAV 备份</h2></div><BackupSwitch checked={form.enabled} change={(enabled) => setForm((current) => ({ ...current, enabled }))} label="启用 WebDAV" /></header>
      <div className="webdav-fields">
        <div className="webdav-url"><div className="heading-with-hint"><label htmlFor={webdavURLId}>WebDAV 地址</label><HelpTip label="WebDAV 地址" text="填写目录地址会自动保存为 pivotflow-backup.json；也支持指定 JSON 文件地址。操作使用已保存的连接设置，更改地址或凭证后请先保存。" /></div><input id={webdavURLId} type="url" value={form.file_url} onChange={(event) => setForm((current) => ({ ...current, file_url: event.target.value }))} placeholder="https://dav.example.com/PivotFlow" /></div>
        <label><span>用户名</span><input value={form.username} onChange={(event) => setForm((current) => ({ ...current, username: event.target.value }))} autoComplete="username" /></label>
        <label><span>密码</span><input type="password" value={password} onChange={(event) => { setPassword(event.target.value); setClearPassword(false) }} autoComplete="new-password" placeholder={config?.password_configured ? '留空保持现有密码' : 'WebDAV 密码'} /></label>
        <label><span>备份范围</span><select value={form.export_type} onChange={(event) => setForm((current) => ({ ...current, export_type: event.target.value as BackupType }))}>{backupTypes.map((item) => <option value={item.value} key={item.value}>{item.label}</option>)}</select></label>
        <label><span>自动备份间隔</span><select value={form.auto_sync_interval_hours} onChange={(event) => setForm((current) => ({ ...current, auto_sync_interval_hours: Number(event.target.value) }))}><option value={6}>每 6 小时</option><option value={12}>每 12 小时</option><option value={24}>每天</option><option value={72}>每 3 天</option><option value={168}>每周</option></select></label>
        <div className="webdav-switch-field"><span>定时备份</span><BackupSwitch checked={form.auto_sync_enabled} change={(auto_sync_enabled) => setForm((current) => ({ ...current, auto_sync_enabled }))} label="启用定时备份" /></div>
      </div>
      {config?.password_configured && <label className="webdav-clear-password"><input type="checkbox" checked={clearPassword} onChange={(event) => { setClearPassword(event.target.checked); if (event.target.checked) setPassword('') }} /><span>清除已保存的 WebDAV 密码</span></label>}
      <section className={`backup-verification backup-verification--${verification.state}`} aria-label="已保存的 WebDAV 校验记录">
        <header className="backup-verification-header">
          <div>{verification.state === 'verified' ? <ShieldCheck size={19} /> : <Clock3 size={19} />}<strong>已保存的校验记录</strong><HelpTip label="回读校验" text={`${verification.state === 'unverified' ? `${verificationStateDetails.unverified} ` : ''}上传后读取同一目标，核对完整字节数及 SHA-256。内容校验不等于所有配置均可恢复；请核对备份范围和遗漏警告。`} /></div>
          <span className={`backup-verification-badge${verification.proof?.warning_count ? ' has-warnings' : ''}`}>{verificationStateLabels[verification.state]}</span>
        </header>
        {verification.state !== 'verified' && verification.state !== 'unverified' && <p className="backup-verification-description">{verificationStateDetails[verification.state]}</p>}
        {verification.lastUploadAt && verification.state !== 'verified' && <p className="backup-verification-upload">最近上传记录 {formatTime(verification.lastUploadAt)}</p>}
        {verification.proof && <VerificationDetails proof={verification.proof} historical={verification.state !== 'verified'} />}
        {verification.warnings.map((warning) => <p className="backup-verification-warning" key={warning}><CircleAlert size={15} /><span>{warning}</span></p>)}
        {config?.last_error && <p className="backup-verification-error"><CircleAlert size={15} /><span><strong>最近操作失败：</strong>{backupErrorMessage(config.last_error)}</span></p>}
      </section>
      {showObservedProof && observedProof && <section className="backup-verification backup-verification--observed" aria-label="本次上传的校验回执"><header className="backup-verification-header"><div><ShieldCheck size={19} /><strong>本次上传的校验回执</strong></div><span className="backup-verification-badge">内容已校验</span></header><p className="backup-verification-description">{latestUpload?.metadataSaved === true ? '服务器已报告记录保存成功；当前设置尚未读到对应记录。' : '本次目标内容已校验，结果尚未可靠记入当前设置。此回执仅在当前页面保留。'}</p><VerificationDetails proof={observedProof} /></section>}
      <footer className="backup-block-footer webdav-footer"><div><span>更改连接设置后，请先保存。</span></div><div><button className="secondary-button" type="button" onClick={() => void stageWebDAVPreview()} disabled={!config?.enabled || Boolean(busy) || Boolean(previewBusy)}>{previewBusy === 'webdav' ? <RefreshCw className="spin" size={15} /> : <CloudDownload size={15} />}{previewBusy === 'webdav' ? '正在预览' : '预览恢复'}</button><button className="secondary-button" type="button" onClick={uploadWebDAV} disabled={!config?.enabled || Boolean(busy)}>{busy === 'upload-webdav' ? <RefreshCw className="spin" size={15} /> : <CloudUpload size={15} />}立即上传</button><button className="primary-button" type="submit" disabled={Boolean(busy)}>{busy === 'save-webdav' ? <RefreshCw className="spin" size={15} /> : <Save size={15} />}保存设置</button></div></footer>
    </form>
    </section>
    {preview && (
      <Modal title={previewTitle || '确认配置变更'} close={dismissPreview} wide>
        <div className="config-preview-modal-body">
          <ConfigPreviewPanel
            preview={preview}
            applying={applyingPreview}
            applyMessage={applyMessage}
            onApply={() => void applyPreview()}
            onRecheck={() => void recheckPreview()}
            rechecking={recheckingPreview}
            onReload={previewSource === 'local'
              ? () => {
                  dismissPreview()
                  setNotice('已清除本地备份文件内容，请重新选择文件后生成预览')
                }
              : () => void stageWebDAVPreview()}
            reloadLabel={previewSource === 'local' ? '重新选择文件' : '重新生成预览'}
            reloading={Boolean(previewBusy)}
          />
        </div>
      </Modal>
    )}
  </>
}

function BackupSwitch({ checked, change, label }: { checked: boolean; change: (value: boolean) => void; label: string }) {
  return <button className={`switch${checked ? ' switch--on' : ''}`} type="button" role="switch" aria-checked={checked} aria-label={label} title={label} onClick={() => change(!checked)}><i /></button>
}

const verificationStateLabels: Record<BackupRecordState, string> = {
  unverified: '尚未校验', legacy: '旧版上传记录', verified: '已回读校验', stale: '最新上传尚未校验',
  invalid: '记录需核对', pending: '新校验记录待保存',
}

const verificationStateDetails: Record<BackupRecordState, string> = {
  unverified: '尚无内容校验证据。上传后会读取目标文件并核对完整内容。',
  legacy: '此记录只有上传时间，尚无回读校验证据。请重新备份并核对新记录。',
  verified: '这次记录的文件内容与发送字节一致。备份范围和遗漏项见下方。',
  stale: '已有更新的上传记录。下面的摘要仅证明较早的文件，最新上传仍需校验。',
  invalid: '校验字段不完整或时间不一致，不能据此确认内容。请重新备份并核对。',
  pending: '新文件已完成内容校验，记录仍待保存。请先核对状态，避免立即重复上传。',
}

function VerificationDetails({ proof, historical = false }: { proof: BackupVerification; historical?: boolean }) {
  return <div className="backup-verification-details">
    <details className="backup-verification-proof"><summary>{historical ? '历史校验详情' : '查看校验详情'}</summary><dl className="backup-verification-facts">
      <div><dt>备份范围</dt><dd>{backupTypeLabel(proof.type)}</dd></div>
      <div><dt>文件大小</dt><dd>{formatBytes(proof.bytes)}</dd></div>
      <div><dt>{historical ? '历史校验时间' : '校验时间'}</dt><dd>{formatTime(proof.verified_at)}</dd></div>
    </dl>
    <div className="backup-verification-digest"><span>SHA-256</span><code>{proof.sha256}</code></div></details>
    {proof.warning_count > 0 && <p className="backup-verification-warning"><ShieldAlert size={15} /><span>导出有 {proof.warning_count} 条不可恢复或遗漏记录，请核对 JSON 警告和恢复预览。</span></p>}
  </div>
}

function backupTypeLabel(type: BackupType): string { return backupTypes.find((item) => item.value === type)?.label || '备份' }
function formatBytes(value: number): string { return value < 1024 * 1024 ? `${Math.max(1, Math.round(value / 1024))} KB` : `${(value / 1024 / 1024).toFixed(1)} MB` }
