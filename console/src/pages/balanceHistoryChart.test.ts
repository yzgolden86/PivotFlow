import assert from 'node:assert/strict'
import test from 'node:test'
import type { BalanceHistoryInput } from './balanceHistoryChart.ts'
import { balanceHistoryCharts, balanceHistoryOverlay, balanceScheduleCaption, dailyComparisonLabel, formatBalanceAmount, formatDailyDelta, selectBalanceCurrencies } from './balanceHistoryChart.ts'

type DailyPoint = BalanceHistoryInput

function point(day: string, daily_delta: number | null, comparison_status = 'complete', currency = 'USD'): DailyPoint {
  return { day, currency, balance: 500, accounts: 2, known_accounts: 2, daily_delta, comparison_status, scheduled_at: 100, updated_at: 110 }
}

test('only trusted server deltas determine signed points, with separate currencies and a centered symmetric zero scale', () => {
  const source = [point('2026-11-03', 5), point('2026-11-02', -10), point('2026-11-02', 300, 'complete', 'CNY')]
  const before = structuredClone(source)
  const charts = balanceHistoryCharts(source)
  assert.deepEqual(source, before)
  assert.equal(charts.length, 2)
  const usd = charts.find((chart) => chart.currency === 'USD')!
  assert.deepEqual(usd.points.map((item) => item.dailyDelta), [-10, 5])
  assert.equal(usd.zeroY, 150)
  assert.ok(usd.points[0].y! > usd.zeroY && usd.points[1].y! < usd.zeroY)
  assert.equal(usd.zeroY - usd.points[1].y!, (usd.points[0].y! - usd.zeroY) / 2)
  assert.equal(usd.ticks[0].value, -usd.ticks.at(-1)!.value)
  assert.equal(usd.ticks[0].y + usd.ticks.at(-1)!.y, 300)
  assert.equal(usd.latest.dailyDelta, 5)
  assert.equal(charts.find((chart) => chart.currency === 'CNY')!.latest.dailyDelta, 300)
  assert.ok(!('delta' in usd), 'the chart must not expose a derived total-balance difference')
})

test('legacy balances and equal account counts never supply missing daily deltas', () => {
  const source = [point('2026-11-01', null), point('2026-11-02', null)]
  source[1].balance = 900
  for (const item of source) { delete item.daily_delta; delete item.comparison_status }
  const [chart] = balanceHistoryCharts(source)
  assert.deepEqual(chart.points.map((item) => item.dailyDelta), [null, null])
  assert.deepEqual(chart.paths, [])
  assert.equal(chart.hasData, false)
  assert.equal(chart.unknownPoints, 2)
})

test('a single trusted daily delta is a centered real point, while baseline remains unavailable', () => {
  const [chart] = balanceHistoryCharts([point('2026-11-01', -3)])
  assert.equal(chart.points[0].x, 500)
  assert.ok(chart.points[0].y! > chart.zeroY)
  assert.equal(chart.latest.dailyDelta, -3)
  assert.deepEqual(chart.paths, [])
  const [baseline] = balanceHistoryCharts([point('2026-11-01', null, 'baseline')])
  assert.equal(baseline.latest.dailyDelta, null)
  assert.equal(baseline.latest.y, null)
  assert.equal(baseline.baselinePoints, 1)
  assert.equal(baseline.unknownPoints, 0)
})

test('missing dates break paths but retain valid server deltas and real daily spacing on either side', () => {
  const [chart] = balanceHistoryCharts([point('2026-11-01', 8), point('2026-11-02', 6), point('2026-11-04', -3), point('2026-11-05', 2)])
  assert.equal(chart.paths.length, 2)
  assert.equal(chart.points[2].dailyDelta, -3)
  assert.equal(chart.missingDays, 1)
  assert.ok(Math.abs((chart.points[1].x - chart.points[0].x) * 2 - (chart.points[2].x - chart.points[1].x)) < 1e-8)
  assert.ok(chart.paths.every((path) => (path.match(/C/g) ?? []).length === 1))
})

test('all untrusted comparison states reject finite delta values and break paths, even with equal account counts', () => {
  for (const status of ['baseline', 'missing_previous', 'coverage_changed', 'incomplete', 'overflow', 'unexpected', undefined]) {
    const middle = point('2026-11-02', 999)
    middle.comparison_status = status
    const [chart] = balanceHistoryCharts([point('2026-11-01', 1), middle, point('2026-11-03', 2)])
    assert.equal(chart.points[1].dailyDelta, null, `untrusted ${status}`)
    assert.equal(chart.points[1].y, null)
    assert.deepEqual(chart.paths, [])
    assert.equal(chart.coverageChanges, status === 'coverage_changed' ? 1 : 0)
  }
})

test('non-finite or absent complete deltas stay unknown; real zero is plotted on the centered zero line', () => {
  for (const value of [null, NaN, Infinity, -Infinity, undefined, '2']) {
    const invalid = { ...point('2026-11-02', 1), daily_delta: value } as unknown as DailyPoint
    const [chart] = balanceHistoryCharts([point('2026-11-01', -2), invalid, point('2026-11-03', 0)])
    assert.equal(chart.points[1].dailyDelta, null)
    assert.equal(chart.points[2].dailyDelta, 0)
    assert.equal(chart.points[2].y, chart.zeroY)
    assert.deepEqual(chart.paths, [])
    assert.equal(chart.unknownPoints, 1)
  }
  const [zero] = balanceHistoryCharts([point('2026-11-01', 0), point('2026-11-02', 0)])
  assert.equal(zero.hasData, true)
  assert.equal(zero.paths.length, 1)
  assert.ok(zero.points.every((item) => item.y === 150))
})

test('backend cohort trust is authoritative: changed display counts or unknown balances never trigger client inference', () => {
  const source = [point('2026-11-01', -5), { ...point('2026-11-02', 7), accounts: 3, known_accounts: 3, balance: null }]
  const [chart] = balanceHistoryCharts(source)
  assert.deepEqual(chart.points.map((item) => item.dailyDelta), [-5, 7])
  assert.equal(chart.paths.length, 1)
  assert.equal(chart.coverageChanges, 0)
  assert.equal(chart.latest.balance, null)
  assert.equal(chart.latest.knownAccounts, 3)
})

test('fourteen calendar days retain the first visible server delta without requiring a hidden client baseline', () => {
  const source = Array.from({ length: 24 }, (_, index) => point(`2026-11-${String(index + 1).padStart(2, '0')}`, index % 2 ? -index : index))
  const [chart] = balanceHistoryCharts(source)
  assert.equal(chart.points.length, 14)
  assert.equal(chart.startDay, '2026-11-11')
  assert.equal(chart.endDay, '2026-11-24')
  assert.equal(chart.points[0].dailyDelta, 10)
  assert.equal(chart.latest.dailyDelta, -23)
})

test('seven and fourteen day overlays share a trailing calendar window and use only visible values for each scale', () => {
  const source = Array.from({ length: 21 }, (_, index) => [
    point(`2026-11-${String(index + 1).padStart(2, '0')}`, index < 14 ? 10_000 : index - 15),
    point(`2026-11-${String(index + 1).padStart(2, '0')}`, index < 14 ? 80_000 : (index - 15) * 7, 'complete', 'CNY'),
  ]).flat()
  const before = structuredClone(source)
  const week = balanceHistoryOverlay(source, 7)
  const fortnight = balanceHistoryOverlay(source, 14)
  assert.equal(week.startDay, '2026-11-15')
  assert.equal(week.endDay, '2026-11-21')
  assert.equal(week.days.length, 7)
  assert.equal(fortnight.startDay, '2026-11-08')
  assert.equal(fortnight.days.length, 14)
  for (const chart of week.series) {
    assert.deepEqual(chart.points.map((item) => item.day), week.days.map((item) => item.day))
    assert.deepEqual(chart.points.map((item) => item.x), week.days.map((item) => item.x))
    assert.equal(chart.ticks[0].value, chart.currency === 'USD' ? 5 : 35)
    assert.equal(chart.points[0].dailyDelta, chart.currency === 'USD' ? -1 : -7)
  }
  assert.deepEqual(source, before)
  assert.deepEqual(balanceHistoryOverlay(source), fortnight)
})

test('seven days count calendar gaps, retaining absent currencies and unknown days without extending the window', () => {
  const source = [point('2026-10-30', 20, 'complete', 'EUR'), point('2026-11-01', 4),
    point('2026-11-02', 2), point('2026-11-04', null, 'incomplete'),
    point('2026-11-06', -3), point('2026-11-07', -1), point('2026-11-08', 1)]
  const overlay = balanceHistoryOverlay(source, 7)
  assert.equal(overlay.startDay, '2026-11-02')
  assert.equal(overlay.endDay, '2026-11-08')
  assert.equal(overlay.days.length, 7)
  const usd = overlay.series.find((chart) => chart.currency === 'USD')!
  assert.equal(usd.missingDays, 2)
  assert.equal(usd.unknownPoints, 1)
  assert.equal(usd.paths.length, 1)
  assert.equal(usd.isolatedPoints.length, 1)
  assert.ok(!usd.points.some((item) => item.day === '2026-11-01'))
  const eur = overlay.series.find((chart) => chart.currency === 'EUR')!
  assert.equal(eur.latest, null)
  assert.equal(eur.hasData, false)
  assert.equal(eur.missingDays, 7)
})

test('conflicting duplicate daily deltas cannot invent an authoritative value; newer server observations win', () => {
  const older = point('2026-11-01', 8)
  const newer = { ...point('2026-11-01', -3), updated_at: 120 }
  const [chart] = balanceHistoryCharts([newer, older])
  assert.equal(chart.points.length, 1)
  assert.equal(chart.latest.dailyDelta, -3)
  const [conflict] = balanceHistoryCharts([older, point('2026-11-01', -3)])
  assert.equal(conflict.latest.dailyDelta, null)
})

test('invalid dates are not normalized and unknown currencies never inherit USD', () => {
  const charts = balanceHistoryCharts([point('2026-02-30', 2), point('bad-date', 3), point('2026-11-01', 4, 'complete', '')])
  assert.equal(charts.length, 1)
  assert.equal(charts[0].currency, '')
  assert.equal(charts[0].latest.dailyDelta, 4)
  assert.deepEqual(balanceHistoryCharts([]), [])
})

type XY = { x: number; y: number }
function cubics(path: string): Array<{ from: XY; c1: XY; c2: XY; to: XY }> {
  const tokens = path.match(/[MC]|-?\d+(?:\.\d+)?/g) ?? []
  assert.equal(tokens.shift(), 'M')
  let from = { x: Number(tokens.shift()), y: Number(tokens.shift()) }
  const segments = []
  while (tokens.length) {
    assert.equal(tokens.shift(), 'C', 'adjacent daily points must use smooth cubic geometry')
    const c1 = { x: Number(tokens.shift()), y: Number(tokens.shift()) }
    const c2 = { x: Number(tokens.shift()), y: Number(tokens.shift()) }
    const to = { x: Number(tokens.shift()), y: Number(tokens.shift()) }
    segments.push({ from, c1, c2, to })
    from = to
  }
  return segments
}

function cubicAt(from: number, c1: number, c2: number, to: number, t: number): number {
  return (1 - t) ** 3 * from + 3 * (1 - t) ** 2 * t * c1 + 3 * (1 - t) * t ** 2 * c2 + t ** 3 * to
}

test('smooth cubic curves pass through measured points without overshoot or invented extrema, including flats and reversals', () => {
  const examples = [[-10, 15, -2, 20, -8], [0, 0, 2, 2, -2], [1, 2, 30, 31, 100], [-100, -31, -30, -2, -1]]
  for (const values of examples) {
    const [chart] = balanceHistoryCharts(values.map((value, index) => point(`2026-11-0${index + 1}`, value)))
    assert.equal(chart.paths.length, 1)
    const segments = cubics(chart.paths[0])
    assert.equal(segments.length, values.length - 1)
    segments.forEach((segment, index) => {
      assert.ok(Math.abs(segment.from.y - chart.points[index].y!) < 0.001)
      assert.ok(Math.abs(segment.to.y - chart.points[index + 1].y!) < 0.001)
      let previousY = segment.from.y
      let previousX = segment.from.x
      for (let step = 0; step <= 100; step += 1) {
        const t = step / 100
        const y = cubicAt(segment.from.y, segment.c1.y, segment.c2.y, segment.to.y, t)
        const x = cubicAt(segment.from.x, segment.c1.x, segment.c2.x, segment.to.x, t)
        assert.ok(y >= Math.min(segment.from.y, segment.to.y) - 0.001 && y <= Math.max(segment.from.y, segment.to.y) + 0.001)
        assert.ok(x >= segment.from.x - 0.001 && x <= segment.to.x + 0.001, 'curve must stay in its measured date interval')
        assert.ok(x >= previousX - 0.001, 'curve must never double back along the date axis')
        assert.ok((y - previousY) * Math.sign(segment.to.y - segment.from.y) >= -0.001, 'no false extrema within a day interval')
        previousY = y
        previousX = x
      }
    })
    for (let index = 1; index < segments.length; index += 1) {
      const previous = segments[index - 1]
      const next = segments[index]
      const outgoing = (previous.to.y - previous.c2.y) / (previous.to.x - previous.c2.x)
      const incoming = (next.c1.y - next.from.y) / (next.c1.x - next.from.x)
      assert.ok(Math.abs(outgoing - incoming) < 0.001, 'adjacent cubics share the same tangent')
    }
  }
})

test('extreme finite signed deltas and subnormal deltas never overflow geometry or lose their server values', () => {
  for (const magnitude of [Number.MAX_VALUE, Number.MIN_VALUE]) {
    const [chart] = balanceHistoryCharts([point('2026-11-01', -magnitude), point('2026-11-02', magnitude), point('2026-11-03', 0)])
    assert.ok(chart.points.every((item) => Number.isFinite(item.x) && Number.isFinite(item.y)))
    assert.deepEqual(chart.points.map((item) => item.dailyDelta), [-magnitude, magnitude, 0])
    assert.ok(chart.paths.every((path) => !/NaN|Infinity/.test(path)))
    assert.ok(chart.ticks.every((tick) => Number.isFinite(tick.value) && Number.isFinite(tick.y)))
    assert.equal(cubics(chart.paths[0]).length, 2)
  }
})

test('signed amount labels preserve known zero, unknown, currencies and tiny negative values', () => {
  assert.equal(formatBalanceAmount(null, 'USD'), '未知')
  assert.equal(formatBalanceAmount(NaN, 'USD'), '未知')
  assert.equal(formatBalanceAmount(0, 'USD', 2, true), 'US$0.00')
  assert.equal(formatBalanceAmount(-0.000001, 'USD', 4, true), '> -US$0.0001')
  assert.equal(formatBalanceAmount(0.000001, 'USD', 4, true), '< US$0.0001')
  assert.equal(formatBalanceAmount(-2, 'CNY', 2, true), '-¥2.00')
  assert.equal(formatBalanceAmount(3, '', 2, true), '+3.00（币种未知）')
  assert.equal(formatBalanceAmount(2, 'USD', 2, true), '+US$2.00')
})

test('sampling captions use the actual schedule, explicitly show disabled sampling, and do not invent legacy defaults', () => {
  assert.equal(balanceScheduleCaption({ enabled: true, time: '00:05', timezone: 'Asia/Shanghai' }), '每日 00:05 · 北京时间')
  assert.equal(balanceScheduleCaption({ enabled: true, time: '14:20', timezone: 'UTC' }), '每日 14:20 · UTC')
  assert.equal(balanceScheduleCaption({ enabled: false, time: '00:05', timezone: 'Asia/Shanghai' }), '自动采样已关闭')
  for (const invalid of [undefined, null, {}, [], { enabled: true, time: '25:05', timezone: 'Asia/Shanghai' }, { enabled: true, time: '0:05', timezone: 'Asia/Shanghai' }, { enabled: true, time: '00:05' }]) {
    assert.equal(balanceScheduleCaption(invalid), '采样时间未提供')
  }
})

test('signed zero formatting never labels a zero delta as a decrease', () => {
  assert.equal(formatBalanceAmount(-0, 'USD', 2, true), 'US$0.00')
})

test('comparison readouts distinguish baseline, missing previous day, changed cohorts, incomplete samples and overflow', () => {
  const expected = {
    baseline: '首日基线，暂无日差额', missing_previous: '缺少前日采样', coverage_changed: '账号范围变化，无法比较',
    incomplete: '采样不完整，差额未知', overflow: '差额超出数值范围', unexpected: '日差额依据未提供',
  }
  for (const [status, label] of Object.entries(expected)) {
    const [chart] = balanceHistoryCharts([point('2026-11-01', null, status)])
    assert.equal(dailyComparisonLabel(chart.latest), label)
  }
  for (const [value, label] of [[3, '净增加'], [-3, '净减少'], [0, '无净变化']] as const) {
    const [chart] = balanceHistoryCharts([point('2026-11-01', value)])
    assert.equal(dailyComparisonLabel(chart.latest), label)
  }
  const [invalid] = balanceHistoryCharts([point('2026-11-01', NaN)])
  assert.equal(dailyComparisonLabel(invalid.latest), '日差额无效')
})

test('overlay uses one full calendar date domain for currencies with different start/end dates and gaps', () => {
  const source = [point('2026-11-01', 2), point('2026-11-03', 4), point('2026-11-02', 20, 'complete', 'CNY'), point('2026-11-03', -40, 'complete', 'CNY')]
  const before = structuredClone(source)
  const overlay = balanceHistoryOverlay(source)
  assert.deepEqual(source, before)
  assert.deepEqual(overlay.days.map((day) => day.day), ['2026-11-01', '2026-11-02', '2026-11-03'])
  assert.deepEqual(overlay.days.map((day) => day.x), [80, 500, 920])
  const usd = overlay.series.find((item) => item.currency === 'USD')!
  const cny = overlay.series.find((item) => item.currency === 'CNY')!
  assert.equal(usd.points[1].x, cny.points[1].x)
  assert.equal(cny.points[0].x, 500)
  assert.equal(usd.startDay, cny.startDay)
  assert.equal(usd.endDay, cny.endDay)
  assert.equal(usd.paths.length, 0)
  assert.equal(cny.paths.length, 1)
  assert.equal(usd.missingDays, 1)
  assert.equal(cny.missingDays, 1)
})

test('overlay keeps original currencies and independent scales without conversion or summed totals', () => {
  const overlay = balanceHistoryOverlay([point('2026-11-01', -2), point('2026-11-02', 4), point('2026-11-01', -200, 'complete', 'CNY'), point('2026-11-02', 400, 'complete', 'CNY')])
  const usd = overlay.series.find((item) => item.currency === 'USD')!
  const cny = overlay.series.find((item) => item.currency === 'CNY')!
  assert.deepEqual(usd.points.map((item) => item.y), cny.points.map((item) => item.y))
  assert.equal(usd.ticks[0].value, 4)
  assert.equal(cny.ticks[0].value, 400)
  assert.equal(overlay.zeroY, 150)
  assert.ok(overlay.series.every((item) => item.zeroY === 150))
  assert.ok(!('total' in overlay) && !('delta' in overlay))
})

test('one common fourteen-day cap keeps older currencies selectable but outside-window values never affect scale', () => {
  const overlay = balanceHistoryOverlay([point('2026-11-01', 99999, 'complete', 'EUR'), point('2026-11-11', 2), point('2026-11-24', -3), point('2026-11-23', 40, 'complete', 'CNY')])
  assert.equal(overlay.days.length, 14)
  assert.equal(overlay.startDay, '2026-11-11')
  assert.equal(overlay.endDay, '2026-11-24')
  assert.equal(overlay.series.length, 3)
  const euro = overlay.series.find((item) => item.currency === 'EUR')!
  assert.deepEqual(euro.points, [])
  assert.deepEqual(euro.ticks, [])
  assert.equal(euro.hasData, false)
  assert.equal(euro.missingDays, 14)
  assert.equal(overlay.series.find((item) => item.currency === 'USD')!.ticks[0].value, 3)
})

test('currency selection defaults CNY/USD, preserves extra and unknown currencies, and prevents duplicate axes', () => {
  const currencies = ['EUR', 'USD', '', 'CNY', 'JPY']
  const before = currencies.slice()
  assert.deepEqual(selectBalanceCurrencies(currencies), ['CNY', 'USD'])
  assert.deepEqual(selectBalanceCurrencies(currencies, ['JPY', 'EUR']), ['JPY', 'EUR'])
  assert.deepEqual(selectBalanceCurrencies(currencies, ['USD', 'USD']), ['USD', 'CNY'])
  assert.deepEqual(selectBalanceCurrencies(currencies, ['', 'JPY']), ['', 'JPY'])
  assert.deepEqual(selectBalanceCurrencies(['EUR']), ['EUR'])
  assert.deepEqual(selectBalanceCurrencies([]), [])
  assert.deepEqual(selectBalanceCurrencies(['CNY', 'USD'], ['removed', 'USD']), ['USD', 'CNY'])
  assert.deepEqual(currencies, before)
})

test('overlay handles single dates, all-zero currency lines and entirely unknown data without invented values', () => {
  const single = balanceHistoryOverlay([point('2026-11-01', 8), point('2026-11-01', null, 'baseline', 'CNY')])
  assert.deepEqual(single.days, [{ day: '2026-11-01', x: 500 }])
  assert.equal(single.series.find((item) => item.currency === 'USD')!.isolatedPoints.length, 1)
  assert.equal(single.series.find((item) => item.currency === 'CNY')!.isolatedPoints.length, 0)
  const zero = balanceHistoryOverlay([point('2026-11-01', 0), point('2026-11-02', 0), point('2026-11-02', 0, 'complete', 'CNY')])
  assert.ok(zero.series.flatMap((item) => item.points).every((item) => item.y === 150))
  assert.ok(zero.series.every((item) => item.ticks.every((tick) => tick.value === 0)), 'an all-zero series has no invented nonzero money scale')
  const unknown = balanceHistoryOverlay([point('2026-11-01', null, 'incomplete'), point('2026-11-02', null, 'coverage_changed', 'CNY')])
  assert.ok(unknown.series.every((item) => !item.hasData && !item.paths.length && !item.isolatedPoints.length && !item.ticks.length))
  assert.deepEqual(balanceHistoryOverlay([]), { series: [], days: [], startDay: '', endDay: '', zeroY: 150 })
})

test('overlay paths smooth actual samples without joining currency-specific holes or drawing invalid singleton values', () => {
  const overlay = balanceHistoryOverlay([point('2026-11-01', -5), point('2026-11-02', 10), point('2026-11-03', null, 'incomplete'), point('2026-11-04', -2), point('2026-11-05', 20), point('2026-11-02', 200, 'complete', 'CNY')])
  const usd = overlay.series.find((item) => item.currency === 'USD')!
  assert.equal(usd.paths.length, 2)
  assert.equal(usd.isolatedPoints.length, 0)
  const cny = overlay.series.find((item) => item.currency === 'CNY')!
  assert.equal(cny.paths.length, 0)
  assert.equal(cny.isolatedPoints[0].day, '2026-11-02')
  for (const path of usd.paths) for (const segment of cubics(path)) {
    for (let step = 0; step <= 100; step += 1) {
      const y = cubicAt(segment.from.y, segment.c1.y, segment.c2.y, segment.to.y, step / 100)
      assert.ok(y >= Math.min(segment.from.y, segment.to.y) - .001 && y <= Math.max(segment.from.y, segment.to.y) + .001)
    }
  }
})

test('daily readouts retain precise original values even when another day is large or the number is subnormal', () => {
  assert.equal(formatDailyDelta(.004, 'USD'), '+US$0.004')
  assert.equal(formatDailyDelta(-.123456, 'CNY'), '-¥0.123456')
  assert.equal(formatDailyDelta(200, 'USD'), '+US$200.00')
  assert.equal(formatDailyDelta(-0, 'USD'), 'US$0.00')
  assert.equal(formatDailyDelta(Number.MIN_VALUE, 'USD'), '+5e-324 USD')
  assert.equal(formatDailyDelta(null, 'USD'), '未知')
})

test('explicit server day fixes the entire 7/14 calendar even with one old observation', () => {
  const source = [point('2026-11-22', -3)]
  const week = balanceHistoryOverlay(source, 7, '2026-11-26')
  const fortnight = balanceHistoryOverlay(source, 14, '2026-11-26')
  assert.equal(week.startDay, '2026-11-20')
  assert.equal(week.endDay, '2026-11-26')
  assert.equal(week.days.length, 7)
  assert.equal(fortnight.startDay, '2026-11-13')
  assert.equal(fortnight.days.length, 14)
  assert.equal(week.series[0].missingDays, 6)
  assert.deepEqual(week.series[0].points.map((item) => item.dailyDelta), [-3])
  assert.deepEqual(week.series[0].paths, [], 'absent days must never become a fabricated line')
})

test('empty and future-only samples retain the explicit calendar without inventing observations', () => {
  const empty = balanceHistoryOverlay([], 7, '2026-03-02')
  assert.equal(empty.startDay, '2026-02-24')
  assert.equal(empty.endDay, '2026-03-02')
  assert.equal(empty.days.length, 7)
  assert.deepEqual(empty.series, [])
  const future = balanceHistoryOverlay([point('2026-11-27', 999)], 14, '2026-11-26')
  assert.equal(future.days.length, 14)
  assert.ok(future.series.every((chart) => chart.points.length === 0 && !chart.hasData))
})
