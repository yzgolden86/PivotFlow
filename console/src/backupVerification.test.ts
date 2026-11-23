import assert from 'node:assert/strict'
import { test } from 'node:test'
import { backupErrorMessage, backupUploadFeedback, backupVerificationRecord, parseBackupVerification } from './backupVerification.ts'

const evidence = {
  verified_at: 1_700_000_000_000,
  bytes: 2400,
  sha256: 'a'.repeat(64),
  type: 'connections',
  warning_count: 0,
}

const record = {
  last_sync_at: 1_700_000_000_000,
  last_verified_at: 1_700_000_000_000,
  last_verified_bytes: 2400,
  last_verified_sha256: 'a'.repeat(64),
  last_verified_type: 'connections',
  last_verified_warning_count: 0,
}

test('回读证据只接受完整的时间、长度、摘要、范围和警告计数', () => {
  assert.deepEqual(parseBackupVerification(evidence), evidence)
  assert.equal(parseBackupVerification({ ...evidence, sha256: 'B'.repeat(64) })?.sha256, 'b'.repeat(64))
  for (const field of Object.keys(evidence)) {
    const partial: Record<string, unknown> = { ...evidence }
    delete partial[field]
    assert.equal(parseBackupVerification(partial), undefined, `missing ${field}`)
  }
  for (const invalid of [
    { verified_at: 1_700_000_000 }, { verified_at: 0 }, { verified_at: 8_640_000_000_000_001 }, { verified_at: '1700000000000' },
    { bytes: 0 }, { bytes: 33_554_433 }, { bytes: 1.5 }, { bytes: '2400' },
    { sha256: 'a'.repeat(63) }, { sha256: 'g'.repeat(64) }, { sha256: '' },
    { type: 'full' }, { type: '' }, { type: ['connections'] }, { warning_count: -1 }, { warning_count: 0.5 }, { warning_count: '0' },
  ]) assert.equal(parseBackupVerification({ ...evidence, ...invalid }), undefined)
  assert.equal(parseBackupVerification(null), undefined)
  assert.equal(parseBackupVerification([]), undefined)
})

test('旧上传记录、没有记录与完整校验具有不同的结果', () => {
  assert.equal(backupVerificationRecord({}).state, 'unverified')
  assert.equal(backupVerificationRecord({ last_sync_at: 1_700_000_000_000 }).state, 'legacy')
  assert.equal(backupVerificationRecord({ last_sync_at: 0, last_verified_at: 0, last_verified_bytes: 0, last_verified_sha256: '', last_verified_type: '', last_verified_warning_count: 0 }).state, 'unverified')
  const verified = backupVerificationRecord(record)
  assert.equal(verified.state, 'verified')
  assert.deepEqual(verified.proof, evidence)
})

test('旧程序再次上传、逆序时间与畸形记录不能把最新文件标为已校验', () => {
  const later = backupVerificationRecord({ ...record, last_sync_at: 1_700_000_010_000 })
  assert.equal(later.state, 'stale')
  assert.equal(later.proof?.verified_at, 1_700_000_000_000)
  assert.equal(backupVerificationRecord({ ...record, last_sync_at: 1_699_999_990_000 }).state, 'invalid')
  assert.equal(backupVerificationRecord({ ...record, last_verified_warning_count: undefined }).state, 'invalid')
  assert.equal(backupVerificationRecord({ ...record, last_verified_sha256: 'bad' }).state, 'invalid')
  assert.equal(backupVerificationRecord({ ...record, last_sync_at: '1700000000000' }).state, 'invalid')
})

test('待保存的新观察不会把之前的持久摘要误称为最新文件', () => {
  const pending = backupVerificationRecord({ ...record, warnings: ['backup_status_pending'] })
  assert.equal(pending.state, 'pending')
  assert.deepEqual(pending.proof, evidence)
  assert.ok(pending.warnings.length > 0)
})

test('上传成功标志缺少证据时只报告待核对', () => {
  const feedback = backupUploadFeedback({ status: 'success', last_sync_at: 1_700_000_000_000 })
  assert.equal(feedback.verified, false)
  assert.equal(feedback.tone, 'warning')
  assert.equal(feedback.proof, undefined)
})

test('内容校验与记录持久化分别反馈，遗漏条目或副本故障保持可见', () => {
  const success = backupUploadFeedback({ status: 'success', verification: evidence, metadata_saved: true, warnings: [] })
  assert.equal(success.verified, true)
  assert.equal(success.metadataSaved, true)
  assert.equal(success.tone, 'success')
  const unsaved = backupUploadFeedback({ status: 'success', verification: evidence, metadata_saved: false, warnings: ['backup_status_not_saved'] })
  assert.equal(unsaved.verified, true)
  assert.equal(unsaved.metadataSaved, false)
  assert.equal(unsaved.tone, 'warning')
  assert.deepEqual(unsaved.proof, evidence)
  assert.ok(unsaved.warnings.length > 0)
  for (const changed of [
    { warnings: ['backup_config_changed'] },
    { warnings: ['backup_status_cache_degraded'] },
    { verification: { ...evidence, warning_count: 2 } },
    { metadata_saved: undefined },
    { metadata_saved: 'true' },
  ]) assert.equal(backupUploadFeedback({ status: 'success', verification: evidence, metadata_saved: true, ...changed }).tone, 'warning')
})

test('畸形响应与远端正文不会被反馈当成安全证据或直接展示', () => {
  const feedback = backupUploadFeedback({ status: 'other', verification: evidence, metadata_saved: true, warnings: ['synthetic-private-response'] })
  assert.equal(feedback.verified, false)
  assert.equal(feedback.tone, 'warning')
  assert.ok(!JSON.stringify(feedback).includes('synthetic-private-response'))
  assert.ok(!backupErrorMessage(new Error('webdav_http_502: synthetic-private-response')).includes('synthetic-private-response'))
  assert.ok(!backupErrorMessage(new Error('synthetic-private-response')).includes('synthetic-private-response'))
  assert.match(backupErrorMessage(new Error('webdav_verify_mismatch')), /校验|内容/)
})

test('较早校验回执被新上传取代时明确说明不保存或重试旧记录', () => {
  const feedback = backupUploadFeedback({ status: 'success', verification: evidence, metadata_saved: false, warnings: ['backup_status_superseded'] })
  assert.equal(feedback.verified, true)
  assert.equal(feedback.metadataSaved, false)
  assert.equal(feedback.tone, 'warning')
  assert.match(feedback.message, /更新/)
  assert.match(feedback.message, /较早|旧记录/)
  assert.doesNotMatch(feedback.message, /自动重试/)
})
