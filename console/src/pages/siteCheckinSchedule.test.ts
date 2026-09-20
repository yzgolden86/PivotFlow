import assert from 'node:assert/strict'
import { test } from 'node:test'

import { checkinScheduleDetail, checkinScheduleTitle, checkinTimezoneLabel } from './siteCheckinSchedule.ts'

test('时区与本地相同就不显示', () => {
  // 绝大多数站点就是 Asia/Shanghai；显示它等于没显示，只会把状态挤掉。
  assert.equal(checkinTimezoneLabel('Asia/Shanghai', 'Asia/Shanghai'), '')
})

test('时区与本地不同才显示，且大小写不敏感地判等', () => {
  assert.equal(checkinTimezoneLabel('America/New_York', 'Asia/Shanghai'), 'America/New_York')
  assert.equal(checkinTimezoneLabel('asia/shanghai', 'Asia/Shanghai'), '')
})

test('本地时区取不到时不擅自隐藏，宁可显示出来', () => {
  assert.equal(checkinTimezoneLabel('Asia/Shanghai', ''), 'Asia/Shanghai')
})

test('时区为空（账号与站点都没配）时返回空串', () => {
  assert.equal(checkinTimezoneLabel(undefined, 'Asia/Shanghai'), '')
  assert.equal(checkinTimezoneLabel('   ', 'Asia/Shanghai'), '')
})

test('状态排在前，时区只在需要时追加', () => {
  assert.equal(checkinScheduleDetail('需人机验证', 'Asia/Shanghai', 'Asia/Shanghai'), '需人机验证')
  assert.equal(checkinScheduleDetail('需人机验证', 'America/New_York', 'Asia/Shanghai'), '需人机验证 · America/New_York')
})

test('状态与本地相同时区都没有时给「—」，不留空白格', () => {
  assert.equal(checkinScheduleDetail('', 'Asia/Shanghai', 'Asia/Shanghai'), '—')
  assert.equal(checkinScheduleDetail('', undefined, 'Asia/Shanghai'), '—')
})

test('tooltip 始终带全量信息，包含被隐藏的时区', () => {
  assert.equal(
    checkinScheduleTitle('需人机验证', 'Asia/Shanghai', 'Asia/Shanghai'),
    '需人机验证 · Asia/Shanghai（与本地时区相同）',
  )
  assert.equal(checkinScheduleTitle('', 'America/New_York', 'Asia/Shanghai'), 'America/New_York')
})

test('tooltip 在什么都没配置时也给一句兜底文案', () => {
  assert.equal(checkinScheduleTitle('', undefined, 'Asia/Shanghai'), '暂无签到状态信息')
})
