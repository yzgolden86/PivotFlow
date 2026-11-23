import type { BackupVerification } from './types.ts'

export type BackupRecordState = 'unverified' | 'legacy' | 'verified' | 'stale' | 'invalid' | 'pending'

export interface BackupVerificationRecord {
  state: BackupRecordState
  proof?: BackupVerification
  lastUploadAt?: number
  warnings: string[]
}

export interface BackupUploadFeedback {
  tone: 'success' | 'warning'
  message: string
  verified: boolean
  metadataSaved?: boolean
  proof?: BackupVerification
  warnings: string[]
}

function objectValue(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

function verificationTime(value: unknown): value is number {
  // This API stores Unix milliseconds. Seconds and string coercions would
  // silently turn malformed evidence into an apparently verified record.
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 1_000_000_000_000 && value <= 8_640_000_000_000_000
}

export function parseBackupVerification(value: unknown): BackupVerification | undefined {
  const proof = objectValue(value)
  if (!verificationTime(proof.verified_at)
    || typeof proof.bytes !== 'number' || !Number.isSafeInteger(proof.bytes) || proof.bytes <= 0 || proof.bytes > 32 * 1024 * 1024
    || typeof proof.sha256 !== 'string' || !/^[a-f0-9]{64}$/i.test(proof.sha256)
    || typeof proof.type !== 'string' || !['all', 'connections', 'settings'].includes(proof.type)
    || typeof proof.warning_count !== 'number' || !Number.isSafeInteger(proof.warning_count) || proof.warning_count < 0) return undefined
  return {
    verified_at: proof.verified_at, bytes: proof.bytes, sha256: proof.sha256.toLowerCase(),
    type: proof.type as BackupVerification['type'], warning_count: proof.warning_count,
  }
}

const backupWarningLabels: Record<string, string> = {
  backup_config_changed: '上传期间 WebDAV 设置已变化；本次校验属于之前的目标，未记入当前设置',
  backup_has_warnings: '导出存在不可恢复或遗漏的条目，请核对 JSON 警告和恢复预览',
  backup_status_not_saved: '文件已校验，但记录尚未保存；服务会自动重试保存记录',
  backup_status_unknown: '文件已校验，记录的保存结果待确认；请重新读取状态，避免重复上传',
  backup_status_cache_degraded: '主库已保存校验记录，但缓存副本同步失败，请核对副本状态',
  backup_status_pending: '新的内容校验已经完成，记录仍待保存；下面的持久摘要属于之前的记录',
  backup_status_superseded: '已有更新的上传记录；本次较早校验未写入设置，请核对最新记录',
}

function warningMessages(value: unknown): string[] {
  if (value === undefined || value === null) return []
  if (!Array.isArray(value)) return ['备份状态警告需要核对，请重新读取设置']
  return [...new Set(value.map((code) => typeof code === 'string'
    ? backupWarningLabels[code.trim().toLowerCase()] || '备份存在需要核对的状态警告'
    : '备份存在需要核对的状态警告'))]
}

export function backupVerificationRecord(value: unknown): BackupVerificationRecord {
  const config = objectValue(value)
  const evidence = {
    verified_at: config.last_verified_at, bytes: config.last_verified_bytes, sha256: config.last_verified_sha256,
    type: config.last_verified_type, warning_count: config.last_verified_warning_count,
  }
  const proof = parseBackupVerification(evidence)
  const lastUploadAt = verificationTime(config.last_sync_at) ? config.last_sync_at : undefined
  const hasEvidence = Object.values(evidence).some((field) => field !== undefined && field !== null && field !== '' && field !== 0)
  const warnings = warningMessages(config.warnings)
  const pending = Array.isArray(config.warnings) && config.warnings.some((code) => typeof code === 'string' && code.trim().toLowerCase() === 'backup_status_pending')
  let state: BackupRecordState
  if (pending) state = 'pending'
  else if (proof) state = lastUploadAt === proof.verified_at ? 'verified' : lastUploadAt && lastUploadAt > proof.verified_at ? 'stale' : 'invalid'
  else if (hasEvidence || (config.last_sync_at !== undefined && config.last_sync_at !== null && config.last_sync_at !== 0 && !lastUploadAt)) state = 'invalid'
  else state = lastUploadAt ? 'legacy' : 'unverified'
  return { state, ...(proof ? { proof } : {}), ...(lastUploadAt ? { lastUploadAt } : {}), warnings }
}

export function backupUploadFeedback(value: unknown): BackupUploadFeedback {
  const result = objectValue(value)
  const proof = result.status === 'success' ? parseBackupVerification(result.verification) : undefined
  const metadataSaved = typeof result.metadata_saved === 'boolean' ? result.metadata_saved : undefined
  const warnings = warningMessages(result.warnings)
  if (proof?.warning_count && !warnings.includes(backupWarningLabels.backup_has_warnings)) warnings.push(backupWarningLabels.backup_has_warnings)
  if (proof && metadataSaved !== true && warnings.length === 0) warnings.push('内容已校验，记录的保存状态尚未确认，请重新读取设置')
  const message = !proof
    ? result.status === 'success' ? '服务器已报告上传成功，尚无完整的回读校验证据；请重新备份并核对记录' : '上传结果待核对，请重新读取备份状态'
    : metadataSaved === true ? '文件已回读校验，校验记录已保存' : '本次文件已回读校验，校验记录尚未确认保存'
  return {
    tone: proof && metadataSaved === true && warnings.length === 0 ? 'success' : 'warning',
    message: [message, ...warnings].join('；'), verified: Boolean(proof),
    ...(metadataSaved !== undefined ? { metadataSaved } : {}), ...(proof ? { proof } : {}), warnings,
  }
}

const backupErrorLabels: Record<string, string> = {
  backup_in_progress: '已有备份正在上传或校验，请等待本次操作结束',
  invalid_backup_type: '备份范围无效，请重新选择范围',
  credential_locked: '备份凭证无法读取，请检查服务器主密钥配置',
  backup_too_large: '备份超过 32 MiB，已停止操作；请缩小备份范围或检查远端文件',
  webdav_not_configured: '尚未启用或配置 WebDAV，请先保存连接设置',
  webdav_request_failed: '无法连接 WebDAV，请检查地址、网络和读写权限',
  webdav_verify_mismatch: '上传后的回读内容与发送文件不一致；请检查其他写入者或缓存，再重新备份',
  webdav_verify_timeout: '上传后回读校验超时；请检查 WebDAV 的读取延迟和网络，再重新备份',
  webdav_verify_cancelled: '回读校验已取消，本次尚未取得成功证据；请重新读取状态',
  webdav_html_response: 'WebDAV 返回了网页而不是备份文件，请检查 WebDAV 地址和认证',
  webdav_http_401: 'WebDAV 认证失败，请检查用户名、应用密码和 WebDAV 地址',
  webdav_http_403: 'WebDAV 拒绝访问，请检查目标目录的读写权限',
  webdav_http_404: 'WebDAV 目录或备份文件不存在，请检查路径和文件可见性',
  webdav_http_405: 'WebDAV 不允许此文件操作，请检查是否填写了 WebDAV 服务地址',
  webdav_http_409: 'WebDAV 目标父目录不存在或路径冲突，请先核对目录',
  webdav_http_423: 'WebDAV 文件仍被锁定，请核对其他写入任务后重试',
  webdav_http_425: 'WebDAV 文件暂时尚不可读取，请稍后重新备份',
  webdav_http_429: 'WebDAV 读取受到限流，请降低备份频率后重试',
  webdav_http_502: 'WebDAV 网关返回 502，请检查服务端、目标路径和代理配置',
  webdav_http_504: 'WebDAV 网关超时，请检查服务状态和网络',
  webdav_http_507: 'WebDAV 存储空间不足，请释放远端空间后重试',
  backup_settings_save_failed: 'WebDAV 设置保存失败，请重新读取设置后再操作',
  backup_settings_result_unknown: 'WebDAV 设置的保存结果待确认，请重新读取设置，避免重复保存',
}

export function backupErrorMessage(reason: unknown): string {
  const raw = reason instanceof Error ? reason.message : String(reason || '')
  const code = raw.trim().toLowerCase().split(/[:\s]/, 1)[0]
  if (backupErrorLabels[code]) return backupErrorLabels[code]
  const http = /^webdav_http_([1-5]\d{2})$/.exec(code)
  if (http) return `WebDAV 返回 HTTP ${http[1]}，请检查服务状态和读写权限后重试`
  return '备份操作失败，请检查连接设置和服务状态后重试'
}
