import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { donutSlicePath, donutSlices } from './donutGeometry.ts'

// 回归：环形图切片必须用圆弧路径画，不能退回「整圆 + stroke-dasharray 切段」。
//
// 历史 bug：Chromium 给闭合圆按 dash 切段时，末端会在接缝处被裁成一条斜切口，
// 「渠道用量」「模型用量」两个饼图在十二点方向露出一块白色缺口——同一份数据用
// 圆弧路径渲染则严丝合缝（实测缺口区域 347 个近白像素 → 0）。

// "M x y A r r rot large sweep x y"，取首尾坐标对比；整圆两段弧时最后一对仍是起点。
function endpoints(d: string): { start: [number, number]; end: [number, number] } {
  const numbers = (d.match(/-?\d+(?:\.\d+)?/g) ?? []).map(Number)
  assert.ok(numbers.length >= 4, `路径缺少坐标：${d}`)
  return { start: [numbers[0], numbers[1]], end: [numbers[numbers.length - 2], numbers[numbers.length - 1]] }
}

test('相邻切片端点重合，整圈首尾相接不留缝', () => {
  const shares = [0.34, 0.29, 0.21, 0.12, 0.04]
  const slices = donutSlices(shares, (share) => share)
  const paths = slices.map((slice) => donutSlicePath(slice.start, slice.share, 38))
  for (let index = 1; index < paths.length; index += 1) {
    assert.deepEqual(endpoints(paths[index]).start, endpoints(paths[index - 1]).end, `第 ${index} 段起点应与上一段终点重合`)
  }
  assert.deepEqual(endpoints(paths[paths.length - 1]).end, endpoints(paths[0]).start, '最后一段终点应回到第一段起点')
})

test('整圆用两段半圆闭合', () => {
  const path = donutSlicePath(0, 1, 38)
  assert.equal(path.match(/A/g)?.length, 2, '单条弧无法从起点回到起点，必须拆成两段')
  assert.deepEqual(endpoints(path).end, endpoints(path).start)
})

test('零占比切片不画弧', () => {
  const path = donutSlicePath(0.5, 0, 38)
  assert.ok(!path.includes('A'), `零占比不应产生弧段：${path}`)
})

test('占比缺失时退化成 0，不把 NaN 写进路径', () => {
  const slices = donutSlices([NaN, 0.5], (share) => share)
  assert.deepEqual(
    slices.map((slice) => slice.share),
    [0, 0.5],
  )
  assert.ok(!donutSlicePath(slices[0].start, slices[0].share, 38).includes('NaN'))
})

test('过半占比使用 large-arc-flag', () => {
  assert.ok(donutSlicePath(0, 0.6, 38).includes('A 38 38 0 1 1 '), '超过半圈必须置 large-arc-flag')
  assert.ok(donutSlicePath(0, 0.4, 38).includes('A 38 38 0 0 1 '), '不足半圈不应置 large-arc-flag')
})

test('饼图切片不再依赖 stroke-dasharray', () => {
  for (const file of ['StatsPage.tsx', 'DashboardPage.tsx']) {
    const source = readFileSync(new URL(`./${file}`, import.meta.url), 'utf8')
    assert.ok(!source.includes('strokeDasharray'), `${file} 不应再用 dash 切圆画环形图`)
    assert.ok(source.includes('donutSlicePath'), `${file} 应通过 donutSlicePath 生成切片`)
  }
})
