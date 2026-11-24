import test from 'node:test'
import assert from 'node:assert/strict'
import { batchNoticeTone, countBatchOutcomes, probeNotice } from './batchOutcome.ts'

test('an unmatched probe is a warning rather than the default success notice', () => {
  const notice = probeNotice({ matched: false, provider_id: '' })
  assert.equal(notice.tone, 'warning')
  assert.match(notice.message, /未识别/)
})

test('a matched probe is reported as success only when the backend matched it', () => {
  assert.equal(probeNotice({ matched: true, provider_id: 'new-api-family' }).tone, 'success')
})

test('recognized task outcomes are counted without treating unknown as success', () => {
  assert.deepEqual(countBatchOutcomes(['success', 'partial', 'failed', 'cancelled', 'unknown', 'future-status']), {
    success: 1,
    partial: 1,
    failed: 1,
    cancelled: 1,
    unknown: 2,
  })
})

test('batch tone distinguishes complete success, partial failure, and total failure', () => {
  assert.equal(batchNoticeTone(countBatchOutcomes(['success', 'success'])), 'success')
  assert.equal(batchNoticeTone(countBatchOutcomes(['success', 'failed'])), 'warning')
  assert.equal(batchNoticeTone(countBatchOutcomes(['success', 'unknown'])), 'warning')
  assert.equal(batchNoticeTone(countBatchOutcomes(['failed', 'failed'])), 'error')
})
