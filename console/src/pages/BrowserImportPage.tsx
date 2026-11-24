import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowRight, CheckCircle2, Download, ExternalLink, FileInput, FolderInput, RefreshCw, Square, Trash2 } from 'lucide-react'
import { useLocation, useNavigate } from 'react-router-dom'
import { getSiteInventory, invalidateSiteInventory } from '../api'
import { buildBrowserCaptureBookmarklet, buildBrowserCaptureScript } from '../browserCapture'
import {
  BROWSER_IMPORT_MAX_BYTES, BROWSER_IMPORT_PAGE_SIZE, BROWSER_IMPORT_VERIFY_TIMEOUT_MS,
  browserImportFailureState, browserImportHref, browserImportNextLinks, browserImportOperationCurrent,
  browserImportRouteState, createBrowserImportPreview, decodeBrowserImportFile, inspectBrowserImport, newBrowserImportOperation,
  readBrowserImportCredential, rewriteBrowserImportCredential, validateBrowserImportText,
  type BrowserImportCandidate, type BrowserImportCredentialDraft, type BrowserImportInspection,
  type BrowserImportMode, type BrowserImportOperation,
} from '../browserImport'
import { safeBrowserImportText, safeBrowserImportURL } from '../browserImportTypes'
import {
  applyConfigPreview, canApplyPreview, configPreviewErrorMessage, discardConfigPreview,
  getConfigPreview, markPreviewObservationUnknown, mergePreviewApplyResult, type ConfigPreview,
} from '../configPreview'
import type { Site } from '../types'
import HelpTip from '../components/HelpTip'
import ConfigPreviewPanel from './ConfigPreviewPanel'
import { PageHeader, Pagination, SecretInput, formatTime } from './shared'
import '../browserImport.css'

const EMPTY_DRAFT: BrowserImportCredentialDraft = { primary: '', userId: '', refreshToken: '', expiresAt: '' }
const platformNames: Record<string, string> = { 'new-api-family': 'New API / One API', anyrouter: 'AnyRouter', veloera: 'Veloera', sub2api: 'Sub2API', 'openai-compatible': 'OpenAI Compatible' }
const credentialNames: Record<string, string> = { api_key: 'API Key', access_token: 'Access Token', cookie: 'Cookie' }
const matchNames: Record<string, string> = { credential: '主凭证相同', user_id: '用户 ID 提示相同', label: '账号标签相同', same_site: '同一完整站点地址' }

export default function BrowserImportPage() {
  const location = useLocation(), navigate = useNavigate()
  const route = browserImportRouteState(location.search)
  const routeRef = useRef(route); routeRef.current = route
  const [text, setText] = useState(''), documentRef = useRef('')
  const [filename, setFilename] = useState('')
  const [fileSummary, setFileSummary] = useState<{ format: string; count: number } | null>(null)
  const [inspection, setInspection] = useState<BrowserImportInspection | null>(null)
  const [selectedIndex, setSelectedIndex] = useState(-1)
  const [page, setPage] = useState(1)
  const [targetId, setTargetId] = useState(0), targetIntent = useRef(route.accountId)
  const [siteName, setSiteName] = useState(''), [accountLabel, setAccountLabel] = useState('')
  const [draft, setDraft] = useState<BrowserImportCredentialDraft>(EMPTY_DRAFT)
  const [draftDirty, setDraftDirty] = useState(false), [needsCheck, setNeedsCheck] = useState(false)
  const [confirmed, setConfirmed] = useState(false)
  const [phase, setPhase] = useState<'' | 'inspect' | 'verify'>('')
  const [failure, setFailure] = useState<ReturnType<typeof browserImportFailureState> | null>(null)
  const [notice, setNotice] = useState(''), [seedNotice, setSeedNotice] = useState('')
  const [preview, setPreview] = useState<ConfigPreview | null>(null), previewRef = useRef<ConfigPreview | null>(null)
  const [applying, setApplying] = useState(false), applyRef = useRef(false)
  const [rechecking, setRechecking] = useState(false), [removing, setRemoving] = useState(false)
  const [applyMessage, setApplyMessage] = useState(''), [acknowledged, setAcknowledged] = useState(false)
  const [helperOpen, setHelperOpen] = useState(new URLSearchParams(location.search).get('helper') === '1')
  const [sourceChoice, setSourceChoice] = useState<'all-api-hub' | 'browser'>(() => new URLSearchParams(location.search).get('helper') === '1' ? 'browser' : 'all-api-hub')
  const [expectedOrigin, setExpectedOrigin] = useState(''), [helperPlatform, setHelperPlatform] = useState('new-api-family')
  const [helperNotice, setHelperNotice] = useState(''), [bookmarkText, setBookmarkText] = useState('')
  const [sites, setSites] = useState<Site[]>([])
  const mounted = useRef(true), generation = useRef(0)
  const operation = useRef<BrowserImportOperation | null>(null)
  const readSequence = useRef(0), readController = useRef<AbortController | null>(null)
  const ownedURLs = useRef(new Set<string>())
  const errorRegion = useRef<HTMLDivElement>(null), helperSection = useRef<HTMLDetailsElement>(null)
  const selected = inspection?.entries.find((entry) => entry.index === selectedIndex)
  const selectedSite = sites.find((site) => site.id === selected?.target_site_id)
  const selectedTarget = selected?.candidates.find((candidate) => candidate.id === targetId)
  const frozen = applying || preview?.status === 'applying' || preview?.status === 'unknown'
  previewRef.current = preview

  const storePreview = useCallback((value: ConfigPreview | null) => { previewRef.current = value; setPreview(value); setAcknowledged(false) }, [])
  const writePreviewRoute = useCallback((id = '') => {
    const current = routeRef.current
    navigate(browserImportHref({ accountId: current.accountId, siteId: current.siteId, previewId: id }).slice(1), { replace: true })
  }, [navigate])

  const cancelOperation = useCallback(() => {
    generation.current++
    operation.current?.controller.abort(); operation.current = null
    readSequence.current++; readController.current?.abort()
    setPhase(''); setRechecking(false)
  }, [])

  const invalidate = useCallback(() => {
    if (applyRef.current || previewRef.current?.status === 'applying' || previewRef.current?.status === 'unknown') return false
    cancelOperation(); setConfirmed(false); setFailure(null); setNotice(''); setApplyMessage('')
    const previous = previewRef.current
    if (previous) {
      storePreview(null); writePreviewRoute()
      void discardConfigPreview(previous.id).catch(() => undefined)
    }
    return true
  }, [cancelOperation, storePreview, writePreviewRoute])

  useEffect(() => {
    mounted.current = true
    return () => {
      mounted.current = false; generation.current++; operation.current?.controller.abort(); readController.current?.abort()
      documentRef.current = ''
      for (const url of ownedURLs.current) URL.revokeObjectURL(url)
      ownedURLs.current.clear()
      // Keep a known preview ID recoverable for ten minutes after navigation.
      // The original document and its secrets belong only to this page.
    }
  }, [])
  useEffect(() => { if (failure) errorRegion.current?.focus({ preventScroll: true }) }, [failure])
  useEffect(() => {
    const safeRoute = browserImportHref({ ...browserImportRouteState(location.search), helper: new URLSearchParams(location.search).get('helper') === '1' })
    if (safeRoute.slice('#/browser-import'.length) !== location.search) navigate(safeRoute.slice(1), { replace: true })
  }, [location.search, navigate])
  useEffect(() => {
    if (!route.accountId && !route.siteId) return
    const controller = new AbortController()
    void getSiteInventory(controller.signal).then((inventory) => {
      if (!mounted.current || controller.signal.aborted) return
      setSites(inventory.sites)
      const account = inventory.accounts.find((item) => item.id === route.accountId)
      const site = inventory.sites.find((item) => item.id === (account?.site_id || route.siteId))
      if (site) {
        const base = safeBrowserImportURL(site.base_url)
        if (base) setExpectedOrigin((current) => current || new URL(base).origin)
        if (platformNames[site.platform]) setHelperPlatform(site.platform)
      }
    }).catch(() => undefined)
    return () => controller.abort()
  }, [route.accountId, route.siteId])

  const reloadPreview = useCallback(async (id?: string) => {
    const requested = id || previewRef.current?.id
    if (!requested) return
    const sequence = ++readSequence.current
    readController.current?.abort()
    const controller = new AbortController(); readController.current = controller
    setRechecking(true); setFailure(null)
    try {
      const value = await getConfigPreview(requested, controller.signal)
      if (!mounted.current || controller.signal.aborted || sequence !== readSequence.current) return
      if (value.kind !== 'browser_import' || !value.browser_import) throw new Error('invalid_response')
      storePreview(value); writePreviewRoute(value.id)
      setApplyMessage(value.status === 'unknown' ? '结果待确认。请在账号页核对实际配置，并保留同一预览编号。' : '')
    } catch (reason) {
      if (!mounted.current || controller.signal.aborted || sequence !== readSequence.current) return
      setApplyMessage(configPreviewErrorMessage(reason))
      if (previewRef.current?.id !== requested) setFailure(browserImportFailureState(reason))
    } finally { if (mounted.current && sequence === readSequence.current) setRechecking(false) }
  }, [storePreview, writePreviewRoute])

  useEffect(() => {
    const current = previewRef.current
    if (current && (applyRef.current || current.status === 'applying' || current.status === 'unknown') && route.previewId !== current.id) {
      writePreviewRoute(current.id)
      setApplyMessage('请先核对原预览结果，再更换来源或预览。')
      return
    }
    if (!route.previewId || route.previewId === current?.id) return
    cancelOperation(); setText(''); documentRef.current = ''; setInspection(null); setSelectedIndex(-1); setDraft(EMPTY_DRAFT); setConfirmed(false)
    void reloadPreview(route.previewId)
  }, [route.previewId, cancelOperation, reloadPreview, writePreviewRoute])

  const chooseEntry = (entry: BrowserImportCandidate, sourceText: string, keepNames = false) => {
    setSelectedIndex(entry.index)
    const target = entry.candidates.find((candidate) => candidate.id === targetIntent.current)
    setTargetId(target?.id || 0)
    if ((routeRef.current.accountId && !target) || (routeRef.current.siteId && routeRef.current.siteId !== entry.target_site_id)) setSeedNotice('初始目标与此条目的完整地址不符，已清除不匹配的目标。请核对当前来源和账号。')
    else setSeedNotice('')
    if (!keepNames) { setSiteName(entry.site_name); setAccountLabel(entry.account_label) }
    setDraft(readBrowserImportCredential(sourceText, entry.index)); setDraftDirty(false); setNeedsCheck(false)
    setExpectedOrigin((current) => current || entry.source_origin)
  }

  const chooseFile = async (file?: File) => {
    if (!file || !invalidate()) return
    const sequence = generation.current
    setInspection(null); setSelectedIndex(-1); setFileSummary(null); setDraft(EMPTY_DRAFT); setPage(1); setText(''); documentRef.current = ''
    setFilename(safeBrowserImportText(file.name) || '已选择的 JSON 文件')
    if (file.size > BROWSER_IMPORT_MAX_BYTES) { setFailure(browserImportFailureState('browser_import_too_large')); return }
    try {
      const value = await decodeBrowserImportFile(file)
      if (!mounted.current || sequence !== generation.current) return
      const summary = validateBrowserImportText(value)
      setText(value); documentRef.current = value; setFileSummary(summary)
      setSourceChoice(summary.format === 'all-api-hub' ? 'all-api-hub' : 'browser')
      setNotice('文件已选择。请点击服务器检查，读取当前目标匹配。')
    } catch (reason) { if (mounted.current && sequence === generation.current) setFailure(browserImportFailureState(reason)) }
  }

  const inspect = async () => {
    if (frozen || !documentRef.current) return
    const oldIndex = selectedIndex
    if (!invalidate()) return
    let sourceText = documentRef.current
    try {
      if (draftDirty && oldIndex >= 0) sourceText = rewriteBrowserImportCredential(sourceText, oldIndex, draft)
    } catch (reason) { setFailure(browserImportFailureState(reason)); return }
    documentRef.current = sourceText; setText(sourceText)
    const job = newBrowserImportOperation(generation.current); operation.current = job
    setPhase('inspect'); setNotice('')
    try {
      const result = await inspectBrowserImport(sourceText, AbortSignal.any([job.controller.signal, AbortSignal.timeout(15_000)]))
      if (!mounted.current || !browserImportOperationCurrent(job, generation.current, operation.current)) return
      setInspection(result)
      const next = result.entries.find((entry) => entry.index === oldIndex && entry.eligible) || result.entries.find((entry) => entry.eligible)
      if (next) { chooseEntry(next, sourceText, next.index === oldIndex); setPage(Math.floor(next.index / BROWSER_IMPORT_PAGE_SIZE) + 1) }
      else { setSelectedIndex(-1); setDraft(EMPTY_DRAFT); setNotice('没有可导入条目，请核对原文件中各项原因。') }
      setConfirmed(false); setNeedsCheck(false)
    } catch (reason) {
      if (mounted.current && browserImportOperationCurrent(job, generation.current, operation.current)) setFailure(browserImportFailureState(reason))
    } finally { if (mounted.current && operation.current === job) { operation.current = null; setPhase('') } }
  }

  const submitPreview = async (mode: BrowserImportMode) => {
    if (!selected?.eligible || !confirmed || needsCheck || frozen || phase || !documentRef.current) return
    const job = newBrowserImportOperation(generation.current); operation.current = job
    setPhase('verify'); setFailure(null); setNotice('')
    const signal = AbortSignal.any([job.controller.signal, AbortSignal.timeout(BROWSER_IMPORT_VERIFY_TIMEOUT_MS)])
    try {
      const value = await createBrowserImportPreview({ document: documentRef.current, entry_index: selected.index, target_account_id: targetId, site_name: siteName, account_label: accountLabel, mode, source_confirmed: confirmed }, signal)
      if (!mounted.current || !browserImportOperationCurrent(job, generation.current, operation.current)) {
        if (!['unknown', 'applying'].includes(value.status)) void discardConfigPreview(value.id).catch(() => undefined)
        return
      }
      const previous = previewRef.current
      if (previous && previous.id !== value.id && !['unknown', 'applying'].includes(previous.status)) void discardConfigPreview(previous.id).catch(() => undefined)
      storePreview(value); writePreviewRoute(value.id)
    } catch (reason) {
      if (mounted.current && browserImportOperationCurrent(job, generation.current, operation.current)) setFailure(browserImportFailureState(signal.aborted ? 'provider_timeout' : reason))
    } finally { if (mounted.current && operation.current === job) { operation.current = null; setPhase('') } }
  }

  const stop = () => { cancelOperation(); setFailure(browserImportFailureState('cancelled')) }
  const changeDraft = (field: keyof BrowserImportCredentialDraft, value: string) => {
    if (!invalidate()) return
    setDraft((current) => ({ ...current, [field]: value })); setDraftDirty(true); setNeedsCheck(true)
  }

  const apply = async () => {
    const current = previewRef.current
    if (!current || applyRef.current || rechecking || !canApplyPreview(current)) return
    applyRef.current = true; setApplying(true); setApplyMessage(''); setFailure(null)
    storePreview({ ...current, status: 'applying', can_apply: false })
    try {
      const result = await applyConfigPreview(current.id)
      if (!mounted.current || previewRef.current?.id !== current.id) return
      storePreview(mergePreviewApplyResult(current, result)); invalidateSiteInventory()
    } catch {
      if (!mounted.current || previewRef.current?.id !== current.id) return
      storePreview(markPreviewObservationUnknown(current)); setApplyMessage('应用响应未确认。正在读取同一预览的结果。')
      await reloadPreview(current.id)
    } finally { applyRef.current = false; if (mounted.current) setApplying(false) }
  }

  const removePreview = async () => {
    const current = previewRef.current
    if (!current || applying || current.status === 'applying' || removing || current.status === 'unknown' && !acknowledged) return
    setRemoving(true)
    try {
      await discardConfigPreview(current.id, undefined, current.status === 'unknown' && acknowledged)
      if (!mounted.current || previewRef.current?.id !== current.id) return
      storePreview(null); writePreviewRoute(); setApplyMessage(''); setConfirmed(false)
      if (current.status === 'unknown') { setInspection(null); setSelectedIndex(-1); setNeedsCheck(true) }
      setNotice('预览已移除。请重新检查当前目标后继续。')
    } catch (reason) { if (mounted.current) setApplyMessage(configPreviewErrorMessage(reason)) }
    finally { if (mounted.current) setRemoving(false) }
  }

  const downloadHelper = () => {
    try {
      const source = buildBrowserCaptureScript({ expectedOrigin, platform: helperPlatform })
      const objectURL = URL.createObjectURL(new Blob([source], { type: 'text/javascript' })); ownedURLs.current.add(objectURL)
      const link = document.createElement('a'); link.href = objectURL; link.download = 'pivotflow-browser-helper.js'; document.body.append(link); link.click(); link.remove()
      window.setTimeout(() => { URL.revokeObjectURL(objectURL); ownedURLs.current.delete(objectURL) }, 0)
      setHelperNotice('助手脚本已下载。请在已登录的上游顶层页面运行，并核对来源。')
    } catch { setHelperNotice('期望来源或平台无效，请填写无路径和账号信息的 HTTP(S) origin。') }
  }
  const copyBookmark = async () => {
    try {
      const source = buildBrowserCaptureBookmarklet({ expectedOrigin, platform: helperPlatform })
      setBookmarkText(source)
      try { await navigator.clipboard.writeText(source); setHelperNotice('书签脚本已复制，可粘贴到浏览器书签的网址栏。') }
      catch { setHelperNotice('请手动复制下方书签脚本，并粘贴到浏览器书签的网址栏。') }
    } catch { setHelperNotice('期望来源或平台无效，请重新核对助手参数。') }
  }

  const entries = inspection?.entries || []
  const rows = entries.slice((page - 1) * BROWSER_IMPORT_PAGE_SIZE, page * BROWSER_IMPORT_PAGE_SIZE)
  const links = preview ? browserImportNextLinks(preview) : []
  const canPreview = Boolean(selected?.eligible && confirmed && !needsCheck && !phase && !frozen)
  const metadata = preview?.browser_import

  return <div className="browser-import-page">
    <PageHeader icon={FolderInput} title="导入账号" tone="blue" actions={<>
      <a className="secondary-button" href="#/accounts">账号管理<ArrowRight size={14} /></a>
      <button className="secondary-button" type="button" onClick={() => { setSourceChoice('browser'); setHelperOpen(true); helperSection.current?.scrollIntoView({ block: 'nearest' }) }}>获取页面助手<Download size={14} /></button>
    </>} />
    <div className="browser-import-intro"><p>从 All API Hub 或已登录的上游页面导入账号，核对差异后保存。</p><ol aria-label="导入步骤"><li>1 来源</li><li>2 目标</li><li>3 确认</li></ol></div>
    {failure && <div ref={errorRegion} tabIndex={-1} className="browser-import-error" role="alert"><strong>{failure.label}</strong><div>
      {failure.code === 'browser_required' && selected && <><a className="text-button" href={selected.source_origin} target="_blank" rel="noopener noreferrer">打开来源站点<ExternalLink size={14} /></a><button className="text-button" type="button" onClick={() => void submitPreview('verify')} disabled={!canPreview}>重新验证</button></>}
      {failure.pendingPreviewId && <button className="text-button" type="button" onClick={() => void reloadPreview(failure.pendingPreviewId)}>读取原预览</button>}
    </div></div>}
    {notice && <p className="browser-import-notice" role="status">{notice}</p>}

    <div className="browser-import-columns">
      <section className="browser-import-source browser-import-card" aria-labelledby="browser-import-source-title">
        <header className="browser-import-card-header"><span>01</span><div><div className="heading-with-hint"><h2 id="browser-import-source-title">选择来源资料</h2><HelpTip label="账号导入范围" text="只导入账号认证资料。余额、模型和签到事实由服务端另行读取；All API Hub 的其他备份部分不会恢复到 PivotFlow。" /></div></div></header>
        <div className="browser-import-methods" role="group" aria-label="账号资料来源">
          <button type="button" className={sourceChoice === 'all-api-hub' ? 'is-selected' : ''} aria-pressed={sourceChoice === 'all-api-hub'} disabled={frozen} onClick={() => { setSourceChoice('all-api-hub'); setHelperOpen(false) }}><FileInput size={16} /><strong>All API Hub 文件</strong></button>
          <button type="button" className={sourceChoice === 'browser' ? 'is-selected' : ''} aria-pressed={sourceChoice === 'browser'} disabled={frozen} onClick={() => { setSourceChoice('browser'); setHelperOpen(true) }}><Download size={16} /><strong>浏览器页面助手</strong></button>
        </div>
        <p className="browser-import-method-hint">{sourceChoice === 'all-api-hub' ? '在 All API Hub 导出明文账号备份，再选择 JSON 文件。' : '在已登录的上游页面运行助手，导出账号 JSON 后选择文件。'}</p>
        <label className="browser-import-file"><FileInput size={22} /><span>导入账号文件</span><input type="file" accept=".json,application/json,text/plain" disabled={frozen} onChange={(event) => void chooseFile(event.target.files?.[0])} /><small>轻量 JSON / All API Hub 2.0–4.0 明文备份 · 最多 2 MiB、64 条</small></label>
        {filename && <p className="browser-import-file-state">{filename}{fileSummary && <span>{fileSummary.format === 'all-api-hub' ? 'All API Hub 账号' : 'PivotFlow 轻量资料'} · {fileSummary.count} 条</span>}</p>}
        <div className="browser-import-source-actions"><button className="primary-button" type="button" disabled={!text || Boolean(phase) || frozen} onClick={() => void inspect()}>{phase === 'inspect' ? <RefreshCw className="spin" size={15} /> : <CheckCircle2 size={15} />}{phase === 'inspect' ? '正在检查' : '服务器检查'}</button><span>检查只读，不修改账号</span></div>
        <details ref={helperSection} className="browser-import-helper" open={helperOpen} onToggle={(event) => setHelperOpen(event.currentTarget.open)}>
          <summary>从浏览器取得资料</summary><p>在已登录的上游页面运行助手，再点击读取和导出。助手只下载本地文件；Cookie 手动填入。</p>
          <label>期望来源 origin（可选）<input type="text" value={expectedOrigin} onChange={(event) => { setExpectedOrigin(event.target.value); setBookmarkText('') }} placeholder="https://上游站点" maxLength={2048} autoComplete="off" /></label>
          <label>助手平台<select value={helperPlatform} onChange={(event) => { setHelperPlatform(event.target.value); setBookmarkText('') }}>{Object.entries(platformNames).map(([value, label]) => <option value={value} key={value}>{label}</option>)}</select></label>
          <div className="browser-import-helper-actions"><button className="secondary-button" type="button" onClick={downloadHelper}><Download size={14} />下载助手脚本</button><button className="secondary-button" type="button" onClick={() => void copyBookmark()}>复制书签脚本</button></div>
          {helperNotice && <p role="status">{helperNotice}</p>}{bookmarkText && <label>书签脚本<textarea value={bookmarkText} readOnly rows={2} onFocus={(event) => event.target.select()} /></label>}
        </details>
        {inspection && <div className="browser-import-inventory"><header><strong>来源条目</strong><span>{entries.filter((entry) => entry.eligible).length} 条可选 / {entries.length} 条</span></header>
          {inspection.ignored_sections.length > 0 && <p className="browser-import-muted">已忽略其他备份部分：{inspection.ignored_sections.join('、')}</p>}
          <div className="browser-import-entries">{rows.map((entry) => <button type="button" key={entry.index} data-entry-index={entry.index} className={`browser-import-entry${entry.index === selectedIndex ? ' is-selected' : ''}`} aria-pressed={entry.index === selectedIndex} disabled={!entry.eligible || frozen} onClick={() => { if (invalidate()) { targetIntent.current = 0; chooseEntry(entry, documentRef.current); setConfirmed(false) } }}>
            <span className="browser-import-entry-number">{entry.index + 1}</span><span className="browser-import-entry-copy"><strong>{entry.account_label || '无效条目'}</strong><span>{entry.site_name || '待核对站点'} · {credentialNames[entry.credential_type] || '待核对凭证'}</span><small>{entry.base_url || entry.source_origin || '地址无效'}</small>{!entry.eligible && <small className="browser-import-entry-problem">{browserImportFailureState(entry.problem || 'invalid_request').label}</small>}{entry.duplicate_of !== undefined && <small>与第 {entry.duplicate_of + 1} 条重复</small>}</span>
          </button>)}</div>
          <Pagination page={page} pageSize={BROWSER_IMPORT_PAGE_SIZE} total={entries.length} onPage={setPage} label="来源条目分页" />
        </div>}
      </section>

      <section className="browser-import-target browser-import-card" aria-labelledby="browser-import-target-title">
        <header className="browser-import-card-header"><span>02</span><div><div className="heading-with-hint"><h2 id="browser-import-target-title">核对来源与目标</h2><HelpTip label="账号保存方式" text="每次处理一条资料。更新凭证时保留既有开关、历史和渠道配置；新账号默认停用。请核对预览后再应用。" /></div><p>新账号默认停用</p></div></header>
        {selected ? <>
          <dl className="browser-import-source-facts"><div><dt>浏览器来源</dt><dd><a href={selected.source_origin} target="_blank" rel="noopener noreferrer">{selected.source_origin}<ExternalLink size={13} /></a></dd></div><div><dt>完整 API 地址</dt><dd>{selected.base_url}</dd></div><div><dt>平台 / 资料</dt><dd>{platformNames[selected.platform] || selected.platform} · {credentialNames[selected.credential_type]}</dd></div><div><dt>导出时间</dt><dd>{inspection?.captured_at ? formatTime(inspection.captured_at) : '原文件未提供'}</dd></div></dl>
          {seedNotice && <p className="browser-import-caution" role="status">{seedNotice}</p>}
          <div className="browser-import-form">
            {selected.target_site_id ? <div className="browser-import-existing"><span>目标站点</span><strong>{selectedSite?.name || selected.site_name} · #{selected.target_site_id}</strong><small>保留现有站点名称与设置</small></div> : <label>目标站点名称<input value={siteName} maxLength={191} disabled={frozen} onChange={(event) => { if (invalidate()) setSiteName(event.target.value) }} /></label>}
            <label>目标账号<select value={targetId} disabled={frozen} onChange={(event) => {
              if (!invalidate()) return
              const id = Number(event.target.value); targetIntent.current = id; setTargetId(id); setSeedNotice('')
              navigate(browserImportHref({ accountId: id, siteId: selected.target_site_id }).slice(1), { replace: true })
            }}><option value={0}>匹配主凭证或新建停用账号</option>{selected.candidates.map((candidate) => <option value={candidate.id} key={candidate.id}>{candidate.label}</option>)}</select></label>
            {selectedTarget ? <p className="browser-import-target-match">账号 #{selectedTarget.id} · {matchNames[selectedTarget.match]}。保留既有账号标签与开关。</p> : <label>目标账号名称<input value={accountLabel} maxLength={191} disabled={frozen} onChange={(event) => { if (invalidate()) setAccountLabel(event.target.value) }} /></label>}
            <label>凭证修正（{credentialNames[selected.credential_type]}）<SecretInput key={selected.index} value={draft.primary} change={(value) => changeDraft('primary', value)} disabled={frozen} placeholder="仅显示当前文件中的凭证" /></label>
            {selected.credential_type !== 'api_key' && selected.platform !== 'sub2api' && <label>上游用户 ID（提示值）<input inputMode="numeric" value={draft.userId} disabled={frozen} onChange={(event) => changeDraft('userId', event.target.value)} /></label>}
            {selected.platform === 'sub2api' && selected.credential_type === 'access_token' && <><label>Refresh Token（可选）<SecretInput key={selected.index} value={draft.refreshToken} change={(value) => changeDraft('refreshToken', value)} disabled={frozen} /></label><label>访问令牌过期时间（毫秒，可选）<input inputMode="numeric" value={draft.expiresAt} disabled={frozen} onChange={(event) => changeDraft('expiresAt', event.target.value)} /></label></>}
            <p className="browser-import-muted">修正凭证后请重新点击服务器检查。此处读取的是当前文件，既有加密凭证不会回传。</p>
            {needsCheck && <p className="browser-import-caution" role="status">资料已改变，需要重新检查后再确认来源。</p>}
            <label className="browser-import-confirm"><input type="checkbox" checked={confirmed} disabled={needsCheck || frozen || phase === 'inspect'} onChange={(event) => { if (!event.target.checked && !invalidate()) return; setConfirmed(event.target.checked) }} /><span>我已核对来源、完整 API 地址与目标账号</span></label>
            <div className="browser-import-preview-actions"><button className="primary-button" type="button" disabled={!canPreview} onClick={() => void submitPreview('verify')}>{phase === 'verify' ? <RefreshCw className="spin" size={15} /> : <CheckCircle2 size={15} />}验证并生成预览</button><button className="secondary-button" type="button" disabled={!canPreview || Boolean(selectedTarget && selectedTarget.match !== 'credential')} onClick={() => void submitPreview('save_disabled')}>停用保存预览</button>{phase === 'verify' && <button className="secondary-button" type="button" onClick={stop}><Square size={14} />停止验证</button>}</div>
            <p className="browser-import-muted">验证只读取本次账号或模型信息，最多等待 90 秒。停用保存不会请求上游。</p>
          </div>
        </> : <div className="browser-import-empty"><FolderInput size={28} /><strong>{metadata ? '已恢复固定预览' : '先检查来源文件'}</strong><p>{metadata ? '来源与目标在下方预览中。原文件仅保留在原页面内存，重新生成时请再次选择文件。' : '完成服务器检查后，在左侧选择一条可用资料，再核对完整地址和具体账号。'}</p></div>}
      </section>
    </div>

    {preview && <div className="browser-import-preview-wrap">
      {metadata && <section className="browser-import-verification" aria-label="导入来源与验证范围"><div><span>03 · 固定预览</span><strong>{metadata.target_site_name} / {metadata.target_account_label}</strong><p>{metadata.source_origin} → {metadata.base_url}</p></div><dl><div><dt>本次验证</dt><dd>{metadata.verification.status === 'verified' ? metadata.verification.scope === 'models' ? `模型读取通过${metadata.verification.model_count_known ? ` · ${metadata.verification.model_count} 个` : ''}` : '账号读取通过' : '未验证，停用保存'}</dd></div><div><dt>身份范围</dt><dd>{metadata.verification.identity_known ? `${metadata.verification.username || '当前用户'} · ID ${metadata.verification.user_id}` : '无法确认稳定用户 ID，请核对具体目标'}</dd></div><div><dt>验证时间</dt><dd>{metadata.verification.checked_at ? formatTime(metadata.verification.checked_at) : '未请求上游'}</dd></div></dl></section>}
      <ConfigPreviewPanel preview={preview} applying={applying} onApply={() => void apply()} onRecheck={() => void reloadPreview()} rechecking={rechecking} applyMessage={applyMessage} />
      {preview.status === 'unknown' && <div className="browser-import-acknowledgement"><a href={metadata?.target_account_id ? `#/accounts?focus_account_id=${metadata.target_account_id}` : '#/accounts'}>打开账号页核对实际结果<ExternalLink size={14} /></a><label className="browser-import-confirm"><input type="checkbox" checked={acknowledged} onChange={(event) => setAcknowledged(event.target.checked)} /><span>我已在账号页核对实际导入结果</span></label><p>核对后移除只解除此预览的等待状态，不会再次应用或撤回已提交的内容。</p></div>}
      <div className="browser-import-result-actions">{links.map((link) => <a className="secondary-button" href={link.href} key={link.href}>{link.label}<ArrowRight size={14} /></a>)}<button className="text-button" type="button" disabled={applying || preview.status === 'applying' || removing || preview.status === 'unknown' && !acknowledged} onClick={() => void removePreview()}><Trash2 size={14} />{removing ? '正在移除' : preview.status === 'unknown' ? '核对后移除预览' : '移除预览'}</button></div>
    </div>}
    {rechecking && !preview && <p className="browser-import-notice" role="status"><RefreshCw className="spin" size={15} />正在读取原预览</p>}
  </div>
}
