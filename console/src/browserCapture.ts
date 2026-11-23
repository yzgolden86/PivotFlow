import { safeBrowserImportURL } from './browserImportTypes.ts'

export interface BrowserCaptureOptions { expectedOrigin?: string; platform?: string }

// This function is intentionally self-contained. Its source, after TypeScript
// compilation, is the exact artifact downloaded or put in a bookmark.
function browserCaptureRuntime(options: BrowserCaptureOptions): string {
  const refuse = (message: string, status: string) => {
    if (typeof document !== 'undefined' && document.body) {
      const notice = document.createElement('div')
      notice.style.cssText = 'position:fixed;inset:20px auto auto 20px;max-width:480px;padding:18px;z-index:2147483647;background:#fff;color:#17201c;border:1px solid #c9d2d8;border-radius:8px;font:14px/1.6 sans-serif'
      const text = document.createElement('p'); text.textContent = message
      const close = document.createElement('button'); close.type = 'button'; close.textContent = '关闭'; close.onclick = () => notice.remove()
      notice.append(text, close); document.body.append(notice)
    }
    return status
  }
  if (window.top !== window.self) return refuse('请在站点的顶层页面运行浏览器助手。', 'frame_refused')
  const origin = window.location.origin
  if (!/^https?:\/\//u.test(origin) || options.expectedOrigin && options.expectedOrigin !== origin) return refuse('当前来源与助手的期望来源不符，请回到已核对的站点。', 'origin_refused')
  if (document.getElementById('pf-browser-helper')) return 'already_open'

  const element = <K extends keyof HTMLElementTagNameMap>(tag: K, text = '') => {
    const node = document.createElement(tag)
    if (text) node.textContent = text
    return node
  }
  const priorFocus = document.activeElement
  const dialog = element('dialog')
  dialog.id = 'pf-browser-helper'
  dialog.setAttribute('aria-label', '浏览器页面助手')
  const styles = element('style')
  styles.textContent = `#pf-browser-helper{box-sizing:border-box;max-width:min(700px,calc(100vw - 24px));width:700px;max-height:calc(100dvh - 24px);overflow:auto;padding:22px;border:1px solid #c9d2d8;border-radius:10px;background:#fff;color:#17201c;font:14px/1.6 "Segoe UI",sans-serif;box-shadow:0 18px 60px #0004}#pf-browser-helper::backdrop{background:#101b1880}#pf-browser-helper *{box-sizing:border-box}#pf-browser-helper h2{margin:0 0 8px;font-size:21px}#pf-browser-helper p{margin:0 0 14px}#pf-browser-helper label{display:grid;gap:5px;min-width:0;margin:0 0 12px;font-weight:600}#pf-browser-helper input,#pf-browser-helper select{min-width:0;width:100%;padding:8px 10px;border:1px solid #c9d2d8;border-radius:5px;background:#fff;color:#17201c;font:inherit}#pf-browser-helper input:focus,#pf-browser-helper select:focus,#pf-browser-helper button:focus-visible{outline:3px solid #3271c8;outline-offset:2px}#pf-browser-helper input[readonly]{background:#f4f6f8}#pf-browser-helper .pf-helper-grid{display:grid;grid-template-columns:1fr 1fr;gap:0 14px}#pf-browser-helper button{padding:9px 14px;border:1px solid #c9d2d8;border-radius:6px;background:#f4f6f8;color:#17201c;font:inherit;cursor:pointer}#pf-browser-helper button:disabled{opacity:.55;cursor:default}#pf-browser-helper .pf-helper-primary{background:#0f7753;color:#fff;border-color:#0f7753}#pf-browser-helper .pf-helper-actions{display:flex;gap:10px;flex-wrap:wrap}#pf-browser-helper .pf-helper-status{white-space:pre-wrap;overflow-wrap:anywhere;color:#46554f;padding:10px 0}#pf-browser-helper [hidden]{display:none!important}@media(max-width:520px){#pf-browser-helper{padding:16px}#pf-browser-helper .pf-helper-grid{grid-template-columns:1fr}}`
  const title = element('h2', '导出当前站点账号资料')
  const description = element('p', '点击读取后，仅检查已知的浏览器存储键。资料下载到本机，再由你选择导入 PivotFlow。Cookie 请手动填写。')
  const form = element('form')
  form.addEventListener('submit', (event) => event.preventDefault())
  const field = (name: string, value = '', kind = 'text') => {
    const label = element('label'), caption = element('span', name), input = element('input')
    input.type = kind; input.value = value; input.autocomplete = 'off'
    label.append(caption, input)
    return { label, input }
  }
  const originField = field('来源 origin', origin); originField.input.readOnly = true
  const base = field('API 基础地址', origin); base.input.maxLength = 2048
  const name = field('站点名称', document.title.slice(0, 191) || '当前站点')
  const account = field('账号标签', '主账号')
  const platformLabel = element('label'), platform = element('select')
  platformLabel.append(element('span', '站点平台'), platform)
  for (const [id, label] of [['new-api-family', 'New API / One API'], ['anyrouter', 'AnyRouter'], ['veloera', 'Veloera'], ['sub2api', 'Sub2API'], ['openai-compatible', 'OpenAI Compatible']]) {
    const option = element('option', label); option.value = id; platform.append(option)
  }
  platform.value = options.platform || 'new-api-family'
  const typeLabel = element('label'), type = element('select')
  typeLabel.append(element('span', '凭证类型'), type)
  const candidateLabel = element('label'), candidate = element('select')
  candidateLabel.append(element('span', '选择凭证来源'), candidate)
  candidateLabel.hidden = true
  const primary = field('主凭证（默认隐藏）', '', 'password'); primary.input.maxLength = 16384
  const user = field('上游用户 ID（提示值）'); user.input.inputMode = 'numeric'
  const refresh = field('Refresh Token（仅 Sub2API，可选）', '', 'password'); refresh.input.maxLength = 16384
  const expiry = field('访问令牌过期时间（毫秒，可选）'); expiry.input.inputMode = 'numeric'
  const status = element('p'); status.className = 'pf-helper-status'; status.setAttribute('role', 'status')
  const read = element('button', '读取当前页资料'); read.type = 'button'
  const download = element('button', '导出 JSON 文件'); download.type = 'button'; download.className = 'pf-helper-primary'; download.disabled = true
  const close = element('button', '关闭助手'); close.type = 'button'
  const actions = element('div'); actions.className = 'pf-helper-actions'; actions.append(read, download, close)
  const grid = element('div'); grid.className = 'pf-helper-grid'; grid.append(name.label, account.label, platformLabel, typeLabel)
  form.append(originField.label, base.label, grid, candidateLabel, primary.label, user.label, refresh.label, expiry.label, status, actions)
  dialog.append(styles, title, description, form)
  document.body.append(dialog)
  dialog.showModal()

  let candidates: { label: string; value: string }[] = []
  const ownedURLs = new Set<string>()
  const timers = new Set<number>()
  const clearSecrets = () => {
    primary.input.value = ''; refresh.input.value = ''; expiry.input.value = ''; user.input.value = ''
    candidates = []; candidate.replaceChildren(); candidateLabel.hidden = true; download.disabled = true
  }
  const cleanup = () => {
    clearSecrets()
    for (const timer of timers) window.clearTimeout(timer)
    for (const url of ownedURLs) URL.revokeObjectURL(url)
    timers.clear(); ownedURLs.clear(); dialog.close(); dialog.remove()
    if (priorFocus instanceof HTMLElement && priorFocus.isConnected) priorFocus.focus({ preventScroll: true })
  }
  close.onclick = cleanup
  dialog.addEventListener('cancel', (event) => { event.preventDefault(); cleanup() })
  const changeType = () => {
    clearSecrets()
    primary.label.querySelector('span')!.textContent = type.value === 'cookie' ? 'Cookie（手动填写，默认隐藏）' : type.value === 'api_key' ? 'API Key（默认隐藏）' : 'Access Token（默认隐藏）'
    user.label.hidden = type.value === 'api_key' || platform.value === 'sub2api'
    refresh.label.hidden = expiry.label.hidden = !(platform.value === 'sub2api' && type.value === 'access_token')
    status.textContent = '确认来源和平台后，可读取当前页资料或手动填写。'
  }
  const changePlatform = () => {
    type.replaceChildren()
    const choices = platform.value === 'openai-compatible' ? [['api_key', 'API Key']] : platform.value === 'sub2api' ? [['access_token', 'Access Token'], ['api_key', 'API Key']] : [['access_token', 'Access Token'], ['cookie', 'Cookie（手动填写）'], ['api_key', 'API Key']]
    for (const [id, label] of choices) { const option = element('option', label); option.value = id; type.append(option) }
    changeType()
  }
  platform.onchange = changePlatform
  type.onchange = changeType
  changePlatform()
  primary.input.oninput = () => { download.disabled = !primary.input.value.trim() }
  candidate.onchange = () => {
    const index = Number(candidate.value)
    primary.input.value = candidate.value !== '' && candidates[index] ? candidates[index].value : ''
    download.disabled = !primary.input.value.trim()
    status.textContent = primary.input.value ? '已选择凭证来源，请核对账号标签与用户 ID。' : '请明确选择一份凭证，或手动填写。'
  }
  const usable = (value: unknown): string => {
    if (typeof value !== 'string' || new TextEncoder().encode(value).length > 16384 || /[\u0000-\u001f\u007f-\u009f]/u.test(value)) return ''
    return value.trim().replace(/^Bearer\s+/u, '')
  }
  read.onclick = () => {
    clearSecrets()
    let limited = false
    const stored = (key: string) => {
      try {
        const value = window.localStorage.getItem(key)
        if (value && value.length <= 16384) return value
        if (value) limited = true
      } catch { limited = true }
      return ''
    }
    const add = (label: string, value: unknown) => {
      const token = usable(value)
      if (token && !/^[{[]/u.test(token) && !candidates.some((entry) => entry.value === token)) candidates.push({ label, value: token })
    }
    const rawUser = stored('user')
    if (rawUser) {
      try {
        const info = JSON.parse(rawUser) as Record<string, unknown>
        if (info && typeof info === 'object' && !Array.isArray(info)) {
          if (typeof info.username === 'string' && Array.from(info.username).length <= 191 && !/[\u0000-\u001f\u007f-\u009f]/u.test(info.username)) account.input.value = info.username
          const id = Number(info.id)
          if (Number.isSafeInteger(id) && id > 0) user.input.value = String(id)
          if (type.value !== 'cookie') add('user.access_token', info.access_token)
        }
      } catch { limited = true }
    }
    if (type.value !== 'cookie') {
      for (const key of ['auth_token', 'access_token', 'token']) {
        const value = stored(key)
        if (value.startsWith('"')) {
          try { add(key, JSON.parse(value)) } catch { limited = true }
        } else add(key, value)
      }
    }
    if (platform.value === 'sub2api' && type.value === 'access_token') {
      refresh.input.value = usable(stored('refresh_token'))
      const value = stored('expires_at')
      if (/^\d+$/u.test(value) && Number.isSafeInteger(Number(value)) && Number(value) > 0) expiry.input.value = value
    }
    if (candidates.length > 1) {
      const prompt = element('option', '请选择一份凭证来源'); prompt.value = ''; candidate.append(prompt)
      candidates.forEach((entry, index) => { const option = element('option', entry.label); option.value = String(index); candidate.append(option) })
      candidate.value = ''; candidateLabel.hidden = false
      status.textContent = '发现多份凭证候选，请明确选择一份；不会自动混合身份。'
    } else if (candidates.length === 1) {
      primary.input.value = candidates[0].value; download.disabled = false
      status.textContent = `已读取 ${candidates[0].label}，请核对来源和账号。`
    } else {
      status.textContent = type.value === 'cookie' ? '账号提示已读取。Cookie 需要手动填写；助手不会读取页面 Cookie。' : '未找到可用的已知凭证，可在上方手动填写。'
    }
    if (limited) status.textContent += ' 部分存储不可读或超过限制，未继续扫描其他键。'
  }
  download.onclick = () => {
    try {
      const rawBase = base.input.value.trim()
      const url = new URL(rawBase)
      const rawPath = rawBase.replace(/^https?:\/\/[^/]+/iu, '')
      if (!['http:', 'https:'].includes(url.protocol) || /^https?:\/\/[^/]*@/iu.test(rawBase) || /[\\?#\u0000-\u001f\u007f]/u.test(rawBase) || url.origin !== origin || rawPath.split('/').some((part) => { const decoded = decodeURIComponent(part); return decoded === '.' || decoded === '..' || /[\\\u0000-\u001f\u007f]/u.test(decoded) })) throw new Error('base')
      const siteName = name.input.value.trim(), accountLabel = account.input.value.trim()
      if (!siteName || !accountLabel || [siteName, accountLabel].some((value) => Array.from(value).length > 191 || /[\u0000-\u001f\u007f-\u009f]/u.test(value))) throw new Error('name')
      const token = type.value === 'access_token' ? usable(primary.input.value) : primary.input.value.trim()
      if (!token || new TextEncoder().encode(token).length > 16384 || /[\u0000-\u001f\u007f-\u009f]/u.test(token)) throw new Error('credential')
      const credential: Record<string, string | number> = { [type.value]: token }
      if (type.value !== 'api_key') {
        const id = Number(user.input.value)
        if (user.input.value && (!/^\d+$/u.test(user.input.value) || !Number.isSafeInteger(id) || id <= 0)) throw new Error('identity')
        if (type.value === 'cookie' && !id) throw new Error('identity')
        if (id > 0) credential.user_id = id
      }
      if (platform.value === 'sub2api' && type.value === 'access_token') {
        const value = usable(refresh.input.value)
        if (refresh.input.value && !value) throw new Error('refresh')
        if (value) credential.refresh_token = value
        if (expiry.input.value) {
          const expiresAt = Number(expiry.input.value)
          if (!/^\d+$/u.test(expiry.input.value) || !Number.isSafeInteger(expiresAt) || expiresAt <= 0) throw new Error('expiry')
          credential.expires_at = expiresAt
        }
      }
      const document = { format: 'pivotflow-browser-bridge', version: '1.0', captured_at: Date.now(), entries: [{ source_origin: origin, base_url: url.href.replace(/\/+$/u, ''), site_name: siteName, platform: platform.value, account_label: accountLabel, credential_type: type.value, credential }] }
      const objectURL = URL.createObjectURL(new Blob([JSON.stringify(document, null, 2)], { type: 'application/json' }))
      ownedURLs.add(objectURL)
      const anchor = element('a'); anchor.href = objectURL; anchor.download = 'pivotflow-browser-import.json'
      dialog.append(anchor); anchor.click(); anchor.remove()
      const timer = window.setTimeout(() => { URL.revokeObjectURL(objectURL); ownedURLs.delete(objectURL); timers.delete(timer) }, 0)
      timers.add(timer)
      clearSecrets(); status.textContent = 'JSON 已下载，表单中的凭证已清空。请在 PivotFlow 的浏览器导入页选择文件。'
    } catch { status.textContent = '无法导出。请核对同来源 API 地址、名称、所选凭证及正整数用户 ID／过期时间。' }
  }
  read.focus()
  return 'ready'
}

export function buildBrowserCaptureScript(options: BrowserCaptureOptions = {}): string {
  const expectedOrigin = options.expectedOrigin?.trim() ? safeBrowserImportURL(options.expectedOrigin, true) : ''
  if (options.expectedOrigin?.trim() && !expectedOrigin) throw new Error('期望来源需要是无路径、账号信息和查询参数的 HTTP(S) origin。')
  const platform = options.platform || 'new-api-family'
  if (!['new-api-family', 'anyrouter', 'veloera', 'sub2api', 'openai-compatible'].includes(platform)) throw new Error('请选择已支持的站点平台。')
  return `(${browserCaptureRuntime.toString()})(${JSON.stringify({ expectedOrigin, platform })});`
}

export function buildBrowserCaptureBookmarklet(options: BrowserCaptureOptions = {}): string {
  // A string completion value in a javascript: bookmark replaces the current
  // document. void keeps this helper on the user's original source page.
  return `javascript:${encodeURIComponent('void ' + buildBrowserCaptureScript(options))}`
}
