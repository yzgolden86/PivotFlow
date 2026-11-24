export type NoticeTone = 'success' | 'warning' | 'error'
export type BatchOutcome = 'success' | 'partial' | 'failed' | 'cancelled' | 'unknown'

export interface BatchOutcomeCounts {
  success: number
  partial: number
  failed: number
  cancelled: number
  unknown: number
}

export function countBatchOutcomes(statuses: readonly string[]): BatchOutcomeCounts {
  const counts: BatchOutcomeCounts = { success: 0, partial: 0, failed: 0, cancelled: 0, unknown: 0 }
  for (const status of statuses) {
    const outcome: BatchOutcome = status === 'success' || status === 'partial' || status === 'failed' || status === 'cancelled'
      ? status
      : status === 'unknown'
        ? 'unknown'
        : 'unknown'
    counts[outcome] += 1
  }
  return counts
}

export function batchNoticeTone(counts: BatchOutcomeCounts): NoticeTone {
  const total = counts.success + counts.partial + counts.failed + counts.cancelled + counts.unknown
  if (total > 0 && counts.success === total) return 'success'
  if (total > 0 && counts.failed === total) return 'error'
  return 'warning'
}

export function probeNotice(result: { matched: boolean; provider_id: string; system_name?: string }): {
  message: string
  tone: NoticeTone
} {
  if (!result.matched) return { message: '未识别到受支持的平台', tone: 'warning' }
  const detail = result.system_name?.trim()
  return {
    message: detail ? `站点探测成功：${detail}` : `站点探测成功：${result.provider_id}`,
    tone: 'success',
  }
}
