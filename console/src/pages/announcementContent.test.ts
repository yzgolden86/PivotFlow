import assert from 'node:assert/strict'
import { test } from 'node:test'

import type { Site } from '../types.ts'
import { resolveAnnouncementContentURL, resolveAnnouncementSourceURL } from './announcementContent.ts'

const site = (base_url: string): Pick<Site, 'base_url'> => ({ base_url })

test('站内相对路径按站点 base_url 解析', () => {
  assert.equal(
    resolveAnnouncementSourceURL('/notice/1', site('https://api.example.com')),
    'https://api.example.com/notice/1',
  )
})

test('base_url 末尾带斜杠也拼得对，不会出现双斜杠', () => {
  assert.equal(
    resolveAnnouncementSourceURL('/notice/1', site('https://api.example.com/')),
    'https://api.example.com/notice/1',
  )
})

test('纯 /api/ 路径不拼给站点，直接回站点根', () => {
  // 上游给的 /api/xxx 指向接口而不是公告页，拼出来只会 404。
  assert.equal(
    resolveAnnouncementSourceURL('/api/notice', site('https://api.example.com/')),
    'https://api.example.com',
  )
})

test('绝对地址原样保留，不被站点 base_url 改写', () => {
  assert.equal(
    resolveAnnouncementSourceURL('https://blog.example.com/p/1', site('https://api.example.com')),
    'https://blog.example.com/p/1',
  )
})

test('没有站点时绝对地址仍可解析，相对路径只能放弃', () => {
  assert.equal(resolveAnnouncementSourceURL('https://blog.example.com/p/1'), 'https://blog.example.com/p/1')
  assert.equal(resolveAnnouncementSourceURL('/notice/1'), null)
})

test('base_url 是空串时按「没有站点」处理', () => {
  assert.equal(resolveAnnouncementSourceURL('/notice/1', site('')), null)
  assert.equal(
    resolveAnnouncementSourceURL('https://blog.example.com/p/1', site('')),
    'https://blog.example.com/p/1',
  )
})

test('空值与解析不出来的都返回 null', () => {
  assert.equal(resolveAnnouncementSourceURL(undefined, site('https://api.example.com')), null)
  assert.equal(resolveAnnouncementSourceURL('   ', site('https://api.example.com')), null)
  assert.equal(resolveAnnouncementSourceURL('not a url'), null)
})

test('javascript: 一律拒绝，别让它进 href', () => {
  assert.equal(resolveAnnouncementSourceURL('javascript:alert(1)'), null)
  assert.equal(resolveAnnouncementContentURL('javascript:alert(1)'), null)
})

test('正文放行 mailto，查看原文不放行', () => {
  assert.equal(resolveAnnouncementContentURL('mailto:a@b.com'), 'mailto:a@b.com')
  assert.equal(resolveAnnouncementSourceURL('mailto:a@b.com'), null)
})

test('正文里的站内相对路径同样按 base_url 解析', () => {
  assert.equal(
    resolveAnnouncementContentURL('/docs/a', site('https://api.example.com')),
    'https://api.example.com/docs/a',
  )
})
