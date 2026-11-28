import assert from 'node:assert/strict'
import test from 'node:test'
import { balanceHistoryNotice, balanceHistoryOverlay, type BalanceHistoryInput } from './balanceHistoryChart.ts'

const schedule = { enabled: true, time: '00:05', timezone: 'Asia/Shanghai' }
function charts(status: string, delta: number | null = null) {
  const observation: BalanceHistoryInput = { day: '2026-11-28', currency: 'USD', balance: 15,
    accounts: 1, known_accounts: status === 'incomplete' ? 0 : 1, updated_at: 1,
    comparison_status: status, daily_delta: delta }
  return balanceHistoryOverlay([observation], 14, '2026-11-28').series
}

test('a saved first baseline is distinguishable from no samples and from a measured zero', () => {
  assert.equal(balanceHistoryNotice(charts('baseline'), schedule).state, 'baseline')
  assert.equal(balanceHistoryNotice([], schedule).state, 'empty')
  assert.equal(balanceHistoryNotice(charts('complete', 0), schedule).state, 'ready')
  assert.equal(balanceHistoryNotice(charts('complete', -3.5), schedule).state, 'ready')
})

test('failed sampling, changed cohort and missing previous day each keep a distinct reason', () => {
  assert.equal(balanceHistoryNotice(charts('incomplete'), schedule).state, 'incomplete')
  assert.equal(balanceHistoryNotice(charts('coverage_changed'), schedule).state, 'coverage_changed')
  assert.equal(balanceHistoryNotice(charts('missing_previous'), schedule).state, 'missing_previous')
  assert.equal(balanceHistoryNotice(charts('complete', null), schedule).state, 'unavailable')
})

test('disabled collection is actionable but never suppresses valid saved comparisons', () => {
  assert.equal(balanceHistoryNotice([], { ...schedule, enabled: false }).state, 'disabled')
  assert.equal(balanceHistoryNotice(charts('complete', 4), { ...schedule, enabled: false }).state, 'ready')
})

test('only the displayed currencies and calendar contribute to the empty-state explanation', () => {
  const overlay = balanceHistoryOverlay([{ day: '2026-11-01', currency: 'USD', balance: 20,
    accounts: 1, updated_at: 1, daily_delta: -5, comparison_status: 'complete' }], 7, '2026-11-28')
  assert.equal(balanceHistoryNotice(overlay.series, schedule).state, 'empty')
  assert.equal(balanceHistoryNotice([...charts('baseline'), ...charts('incomplete')], schedule).state, 'incomplete')
})

test('subdaily baselines wait for the next sample without requiring the next day', () => {
  for (const interval_minutes of [15, 30, 60, 120, 360, 720]) {
    const notice = balanceHistoryNotice(charts('baseline'), { ...schedule, interval_minutes })
    assert.equal(notice.state, 'baseline')
    assert.match(notice.description, /下一次.*采样/)
    assert.match(notice.description, /当天|当日/)
    assert.doesNotMatch(notice.description, /还需要下一天|明天|连续两天/)
  }
  assert.match(balanceHistoryNotice(charts('baseline'), { ...schedule, interval_minutes: 60 }).description, /每小时/)
})

test('explicit daily and missing legacy intervals keep next-day baseline semantics', () => {
  for (const dailySchedule of [schedule, { ...schedule, interval_minutes: 1440 }]) {
    const notice = balanceHistoryNotice(charts('baseline'), dailySchedule)
    assert.equal(notice.state, 'baseline')
    assert.equal(notice.description, '每日 00:05 · 北京时间。这里记录固定采样时点之间的净变化，当天实时余额变化会在下一次成功采样后参与比较。 最新基线日期为 2026-11-28；还需要下一天相同账号集合的完整记录才能生成第一笔日差额。')
    assert.doesNotMatch(notice.description, /当天再次采样|自当日首次采样/)
  }
})

test('daily and legacy unavailable notices preserve their original comparison descriptions', () => {
  const timing = '每日 00:05 · 北京时间。这里记录固定采样时点之间的净变化，当天实时余额变化会在下一次成功采样后参与比较。'
  const descriptions = {
    incomplete: '本日或前日有账号未成功采样；请检查账号凭证、上游余额查询及服务端采样任务。缺失记录不会计作零。',
    coverage_changed: '连续两天的账号集合不同，不能直接计算日差额；相同账号集合连续采样成功后恢复比较。',
    missing_previous: '缺少前一天的采样，不能将隔日余额直接作为单日变化。',
  }
  for (const dailySchedule of [schedule, { ...schedule, interval_minutes: 1440 }]) {
    for (const [status, description] of Object.entries(descriptions)) {
      assert.equal(balanceHistoryNotice(charts(status), dailySchedule).description, `${timing} ${description}`)
    }
    const disabled = balanceHistoryNotice([], { ...dailySchedule, enabled: false })
    assert.equal(disabled.title, '每日余额采样已关闭')
    assert.equal(disabled.description, '可在系统设置的自动任务中开启。已有余额不代表已保存固定时间的每日采样。')
  }
})

test('malformed intervals never promise daily or same-day baseline comparisons', () => {
  for (const interval_minutes of [undefined, null, '60', true, 0, 45, 60.5, NaN, Infinity]) {
    const notice = balanceHistoryNotice(charts('baseline'), { ...schedule, interval_minutes })
    assert.equal(notice.state, 'baseline')
    assert.match(notice.description, /采样时间未提供/)
    assert.match(notice.description, /等待.*可比较/)
    assert.doesNotMatch(notice.description, /下一天|当天再次|自当日首次|每天保留/)
    assert.doesNotMatch(balanceHistoryNotice(charts('missing_previous'), { ...schedule, interval_minutes }).description, /当天取得|日内净变化/)
  }
})

test('finite partial comparisons including measured zero suppress the waiting notice even when sampling is disabled', () => {
  for (const delta of [-3, 0, 4]) {
    assert.equal(balanceHistoryNotice(charts('partial', delta), { ...schedule, interval_minutes: 60 }).state, 'ready')
    assert.equal(balanceHistoryNotice(charts('partial', delta), { ...schedule, enabled: false, interval_minutes: 60 }).state, 'ready')
  }
  assert.equal(balanceHistoryNotice(charts('partial', null), { ...schedule, interval_minutes: 60 }).state, 'unavailable')
})
