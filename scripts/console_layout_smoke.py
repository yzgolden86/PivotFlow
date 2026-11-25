"""Bounded console-layout regressions against built assets and synthetic data.

Run after the main-owned build. No gateway, database, Vite, upstream probes, or
mutations are used. All evidence images are visible-viewport JPEGs. --list-cases
lists the coverage without starting Chromium or the owned static server.
"""

import argparse
import copy
import hashlib
import json
import re
import time
from functools import partial
from http.server import ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import parse_qs, urlsplit

from playwright.sync_api import expect, sync_playwright

import docs_screenshots as fixtures
from console_visual_regression import setting
from model_routes_smoke import MODEL
from ui_refinement_smoke import RefinementData, Runner, StaticHandler, cost_summary


READ_PATHS = frozenset((
    '/dashboard/session', '/dashboard/summary', '/admin/verify',
    '/admin/dashboard', '/admin/runtime-metrics', '/admin/settings',
    '/admin/routing/models', '/admin/routing/diagnostics', '/admin/auth-tokens',
    '/admin/sites', '/admin/site-inventory', '/admin/site-models',
    '/admin/site-channel-bindings', '/admin/site-pricing', '/admin/channels',
    '/admin/checkin-attempts', '/admin/site-tasks', '/admin/announcements',
    '/admin/model-alias-inventory', '/admin/stats', '/admin/stats/filter-options',
    '/admin/request-cache', '/admin/active-requests', '/admin/appearance/wallpaper-bytes',
))
TIMEOUT_GROUPS = {
    '全局默认': ('upstream_first_byte_timeout', 'stream_timeout', 'non_stream_timeout'),
    'Anthropic': ('anthropic_first_byte_timeout', 'anthropic_non_stream_timeout'),
    'Codex': ('codex_first_byte_timeout', 'codex_non_stream_timeout'),
    'OpenAI': ('openai_first_byte_timeout', 'openai_non_stream_timeout'),
    'Gemini': ('gemini_first_byte_timeout', 'gemini_non_stream_timeout'),
    '连接复用': ('upstream_connection_reuse_limit_seconds',),
}
RELATED = (
    ('dashboard', '/', '系统概览', '.dashboard-grid', '.data-panel'),
    ('accounts', '/accounts', '账号管理', '.account-records', '.account-records .record-row'),
    ('sites', '/sites', '站点管理', '.site-list', '.site-row'),
    ('models', '/models', '模型测试', '.model-card-grid', '.model-card'),
    ('announcements', '/announcements', '公告中心', '.announcement-list', '.announcement-row'),
)


class LayoutData(RefinementData):
    """Literal facts sufficient to exercise layout and read-only interaction."""

    def __init__(self, case):
        super().__init__(case)
        self.unexpected = []
        self.wallpaper = None
        self.wallpaper_url = None
        self.task_kind = 'checkin'
        self.task_status = 'running'
        self.task_reads = 0
        prototype = copy.deepcopy(self.inventory[0])
        self.inventory = []
        for number in range(571):
            item = copy.deepcopy(prototype)
            name = MODEL if number == 0 else f'layout-model-{number:03d}'
            if number == 49:
                name = 'vendor/' + '长名称🚦-' * 60
            item.update(model=name, aliases=[f'layout-alias-{number:03d}'], alias_count=1,
                        kind='model', configured_channels=3, enabled_channels=2)
            self.inventory.append(item)
        self.channels = self.channels[:8]
        self.site_facts = []
        for number, channel in enumerate(self.channels):
            name = f'layout-model-{number + 1:03d}'
            channel['models'] = [{'model': name, 'disabled': False, 'redirect': ''}]
            self.site_facts.append({'site_account_id': self.accounts[0]['id'], 'model': name,
                                    'route_type': 'openai_chat', 'source': 'models_endpoint',
                                    'disabled': False, 'stale': False, 'last_seen_at': fixtures.NOW,
                                    'created_at': fixtures.NOW, 'updated_at': fixtures.NOW})
        for site in self.sites:
            site.update(base_url=f'https://layout-site-{site["id"]}.example.invalid',
                        external_checkin_url=f'https://layout-site-{site["id"]}.example.invalid/checkin',
                        checkin_method='unknown')
        self.accounts[0].update(last_checkin_status='failed', credential_type='access_token',
                                enabled=True, label='layout-checkin-account')
        if case.startswith('maintenance-cross-kind-'):
            self.task_kind = 'model_refresh' if '-model-refresh-' in case else 'refresh'
            self.task_status = 'unknown'
            self.accounts[1].update(last_checkin_status='failed', credential_type='access_token',
                                    enabled=True, label='layout-unrelated-checkin-account')
        for token in self.tokens:
            token.update(token='', token_hint=f'layout-hint-{token["id"]}',
                         cost_used_usd=2, effective_cost_usd=1, token_recoverable=False)
        keys = {item['key'] for item in self.settings}
        for group_keys in TIMEOUT_GROUPS.values():
            for key in group_keys:
                if key not in keys:
                    self.settings.append(setting(key, '30' if 'first_byte' in key else '120'))
        self.balance_history = [
            {'day': f'2026-11-{day:02d}', 'currency': currency, 'balance': balance,
             'daily_delta': None if index == 0 else balance - values[index - 1],
             'comparison_status': 'baseline' if index == 0 else 'complete',
             'accounts': 2, 'known_accounts': 2, 'scheduled_at': fixtures.NOW,
             'updated_at': fixtures.NOW}
            for currency, values in (('USD', (12, -4, 9, 17)), ('CNY', (120, 90, 140, 110)))
            for index, (day, balance) in enumerate(zip(range(20, 24), values))
        ]
        announcement_content = '用于检查公告卡片、正文摘要与操作的真实布局。'
        self.announcements = [
            {'id': number, 'site_id': self.sites[number - 1]['id'],
             'source_key': f'layout-announcement-{number}', 'title': f'布局回归公告 {number}',
             'content_markdown': announcement_content, 'level': 'info',
             'source_url': f'{self.sites[number - 1]["base_url"]}/announcements/{number}',
             'upstream_created_at': fixtures.NOW, 'upstream_updated_at': fixtures.NOW,
             'first_seen_at': fixtures.NOW, 'last_seen_at': fixtures.NOW, 'read_at': fixtures.NOW,
             'content_hash': hashlib.sha256(announcement_content.encode('utf-8')).hexdigest(),
             'created_at': fixtures.NOW, 'updated_at': fixtures.NOW}
            for number in (1, 2)
        ]

    def permits_mutation(self, request):
        return False

    def route(self, route):
        request = route.request
        parsed = urlsplit(request.url)
        path, query = parsed.path, parse_qs(parsed.query)
        task_read = re.fullmatch(r'/admin/site-tasks/layout-persisted-task', path)
        account_read = re.fullmatch(r'/admin/sites/\d+/accounts', path)
        wallpaper_read = path == '/admin/appearance/wallpaper-bytes'
        if (request.method != 'GET' or (path not in READ_PATHS and not task_read and not account_read)
                or (wallpaper_read and (not self.wallpaper or not self.wallpaper_url
                                        or parse_qs(parsed.query, keep_blank_values=True)
                                        != {'url': [self.wallpaper_url]}))):
            self.unexpected.append({'case': self.case, 'unexpected_api': path, 'method': request.method})
            route.abort('blockedbyclient')
            return
        call = {'path': path, 'method': request.method, 'query': query}
        if wallpaper_read:
            self.calls.append(call)
            route.fulfill(status=200, content_type='image/jpeg', body=self.wallpaper,
                          headers={'Cache-Control': 'no-store'})
        elif path == '/admin/site-tasks' or task_read:
            self.calls.append(call)
            if task_read:
                self.task_reads += 1
            task = {'id': 'layout-persisted-task', 'kind': self.task_kind, 'site_id': self.sites[0]['id'],
                    'site_account_id': self.accounts[0]['id'], 'status': self.task_status,
                    'created_at': fixtures.NOW, 'started_at': fixtures.NOW,
                    'error': 'synthetic_observation_interrupted' if self.task_status == 'unknown' else ''}
            self.fulfill(route, task if task_read else [task] if self.case.startswith('maintenance-') else [])
        elif path == '/admin/checkin-attempts':
            self.calls.append(call)
            self.fulfill(route, [], 0)
        elif path == '/admin/announcements':
            self.calls.append(call)
            items = [item for item in self.announcements
                     if not query.get('site_id') or item['site_id'] == int(query['site_id'][0])]
            if query.get('unread', ['false'])[0] == 'true':
                items = [item for item in items if not item['read_at']]
            offset, limit = int(query.get('offset', ['0'])[0]), int(query.get('limit', ['30'])[0])
            self.fulfill(route, items[offset:offset + limit], len(items))
        elif path == '/admin/site-pricing':
            self.calls.append(call)
            self.fulfill(route, {'site_id': int(query.get('site_id', ['1'])[0]),
                                 'available': False, 'models': [], 'group_ratio': {}})
        elif path == '/admin/stats':
            self.calls.append(call)
            stats = [{'channel_name': '布局合成渠道', 'model': 'layout-model-001', 'total': 10,
                      'success': 9, 'error': 1, 'effective_cost': 1, 'total_input_tokens': 100,
                      'total_output_tokens': 20}]
            self.fulfill(route, {'stats': stats, 'duration_seconds': 3600, 'is_today': True,
                                 'balance_history': self.balance_history, 'cost_summary': cost_summary('complete'),
                                 'balance_schedule': {'enabled': True, 'time': '00:05', 'timezone': 'Asia/Shanghai'},
                                 'rpm_stats': {'peak_rpm': 2, 'peak_qps': 1, 'avg_rpm': 1,
                                               'avg_qps': .1, 'recent_rpm': 1, 'recent_qps': .1}})
        elif path == '/admin/stats/filter-options':
            self.calls.append(call)
            self.fulfill(route, {'channel_names': ['布局合成渠道'], 'models': ['layout-model-001']})
        elif path == '/admin/dashboard':
            self.calls.append(call)
            dashboard = copy.deepcopy(fixtures.DASHBOARD)
            dashboard['cost_summary'] = cost_summary('complete')
            self.fulfill(route, dashboard)
        else:
            super().route(route)


class GuardedContext:
    """Apply the round's stricter all-request guard around the shared runner."""

    def __init__(self, context, events, origin):
        self.context, self.events, self.origin = context, events, origin

    def __getattr__(self, name):
        return getattr(self.context, name)

    def route(self, pattern, handler):
        def guarded(route):
            request, parsed = route.request, urlsplit(route.request.url)
            allowed_static = parsed.path.startswith('/web/console/') or parsed.path in (
                '/web/brand-mark.svg', '/web/favicon.svg', '/synthetic-wallpaper.jpg')
            if request.method != 'GET':
                self.events.append({'unexpected_mutation': parsed.path, 'method': request.method})
                route.abort('blockedbyclient')
            elif parsed.netloc == self.origin and not allowed_static and not parsed.path.startswith(
                    ('/admin/', '/dashboard/', '/v1/', '/v1beta/')):
                self.events.append({'unexpected_local_request': parsed.path})
                route.abort('blockedbyclient')
            else:
                handler(route)
        self.context.route(pattern, guarded)


class GuardedBrowser:
    def __init__(self, browser, events, base):
        self.browser, self.events, self.origin = browser, events, urlsplit(base).netloc

    def new_context(self, **kwargs):
        context = self.browser.new_context(**kwargs, service_workers='block')
        if hasattr(context, 'route_web_socket'):
            def block(socket):
                self.events.append({'unexpected_websocket': socket.url})
                socket.close(code=1000, reason='Synthetic layout harness blocks sockets')
            context.route_web_socket('**/*', block)
        return GuardedContext(context, self.events, self.origin)


class LayoutRunner(Runner):
    def __init__(self, browser, base, output, cases, wallpaper_fixture, max_seconds):
        super().__init__(browser, base, output, cases, wallpaper_fixture, data_factory=self.make_data)
        if self.wallpaper is not None and not self.wallpaper.startswith(b'\xff\xd8\xff'):
            raise ValueError('--wallpaper-fixture must contain a local JPEG')
        self.browser = GuardedBrowser(browser, self.events, base)
        self.deadline = time.monotonic() + max_seconds
        self.assertions, self.active_case = [], ''

    def make_data(self, name):
        data = LayoutData(name)
        data.unexpected = self.events
        if 'photo' in name:
            data.wallpaper = self.wallpaper
            data.wallpaper_url = f'{self.base}/synthetic-wallpaper.jpg'
        return data

    def check(self, condition, message):
        self.assertions.append({'case': self.active_case, 'pass': bool(condition), 'check': message})
        super().check(condition, message)

    def case(self, name, callback, **kwargs):
        if self.cases and name not in self.cases:
            return
        self.active_case = name
        if time.monotonic() >= self.deadline:
            self.results.append({'case': name, 'pass': False, 'error': 'Bounded suite time budget exhausted'})
            return
        def checked(page, data):
            if kwargs.get('wallpaper'):
                expect(page.locator('html')).to_have_attribute('data-wallpaper', 'on')
                expect(page.locator('html')).to_have_attribute('data-glass', 'on')
            callback(page, data)
        super().case(name, checked, **kwargs)

    def record(self, **metrics):
        self.metrics.setdefault(self.active_case, {}).update(metrics)

    def photo(self, page, suffix):
        filename = f'{self.active_case}-{suffix}.jpg'
        page.screenshot(path=str(self.output / filename), type='jpeg', quality=82,
                        full_page=False, timeout=6000)
        self.metrics.setdefault(self.active_case, {}).setdefault('evidence', []).append(filename)


def rect(locator):
    return locator.evaluate("""element => {
      const r = element.getBoundingClientRect(), s = getComputedStyle(element);
      return {x:r.x, y:r.y, width:r.width, height:r.height, right:r.right, bottom:r.bottom,
        clientHeight:element.clientHeight, scrollHeight:element.scrollHeight,
        clientWidth:element.clientWidth, scrollWidth:element.scrollWidth,
        scrollTop:element.scrollTop, overflowY:s.overflowY, overflowX:s.overflowX,
        background:s.backgroundColor};
    }""")


def contains(outer, inner, tolerance=2):
    return (inner['x'] >= outer['x'] - tolerance and inner['y'] >= outer['y'] - tolerance
            and inner['right'] <= outer['right'] + tolerance and inner['bottom'] <= outer['bottom'] + tolerance)


def overlap(first, second):
    return (min(first['right'], second['right']) - max(first['x'], second['x']) > 1
            and min(first['bottom'], second['bottom']) - max(first['y'], second['y']) > 1)


def reachable(runner, locator, label):
    expect(locator).to_be_visible()
    locator.scroll_into_view_if_needed(timeout=6000)
    box = rect(locator)
    view = locator.page.viewport_size
    runner.check(box['width'] > 0 and box['height'] > 0 and box['x'] >= -1
                 and box['right'] <= view['width'] + 1 and box['y'] >= -1
                 and box['bottom'] <= view['height'] + 1, f'{label} fits the visible viewport')
    hit = locator.evaluate("""element => {
      const r = element.getBoundingClientRect();
      const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2);
      return !!hit && (element === hit || element.contains(hit));
    }""")
    runner.check(hit, f'{label} is not obscured at its action point')
    return box


def action_group(runner, group, label):
    group.scroll_into_view_if_needed(timeout=6000)
    controls = group.locator('button:visible, a:visible, input:visible, select:visible')
    runner.check(controls.count() > 0, f'{label} has visible controls')
    for index in range(min(controls.count(), 12)):
        control = controls.nth(index)
        reachable(runner, control, f'{label} control {index + 1}')
    # Compare in one scroll state: a wide table may pan while revealing actions.
    boxes = [rect(controls.nth(index)) for index in range(min(controls.count(), 12))]
    for index, first in enumerate(boxes):
        for second in boxes[index + 1:]:
            runner.check(not overlap(first, second), f'{label} controls do not overlap')


def full_text(runner, locator, expected):
    expect(locator).to_have_text(expected)
    clipped = locator.evaluate("""(element, text) => {
      const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
      let node;
      while ((node = walker.nextNode())) {
        if (!node.textContent.includes(text)) continue;
        const range = document.createRange(); range.selectNodeContents(node);
        for (const r of range.getClientRects()) {
          for (let parent = node.parentElement; parent; parent = parent.parentElement) {
            const s = getComputedStyle(parent), p = parent.getBoundingClientRect();
            if (['hidden','clip','auto','scroll'].includes(s.overflowX) && (r.left < p.left - 1 || r.right > p.right + 1)) return true;
            if (['hidden','clip','auto','scroll'].includes(s.overflowY) && (r.top < p.top - 1 || r.bottom > p.bottom + 1)) return true;
          }
        }
        return false;
      }
      return true;
    }""", expected)
    runner.check(not clipped, f'The full {expected} label is rendered without clipping')


def navigate(page, path, title):
    page.evaluate('(path) => { location.hash = path }', path)
    expect(page.locator('h1')).to_have_text(title)


def routing_validation(runner, page, data):
    field = page.get_by_role('textbox', name='请求模型', exact=True)
    submit = page.get_by_role('button', name='查看路由', exact=True)
    field.fill('')
    reachable(runner, submit, 'Diagnostic submit')
    before = rect(submit)
    reads = sum(call['path'] == '/admin/routing/diagnostics' for call in data.calls)
    submit.click()
    expect(field).to_have_attribute('aria-invalid', 'true')
    error_id = field.get_attribute('aria-describedby')
    runner.check(bool(error_id), 'Invalid request model has an accessible error association')
    error = page.locator(f'[id="{error_id}"]')
    expect(error).to_have_attribute('role', 'alert')
    expect(error).to_contain_text('1–512')
    after = rect(submit)
    runner.check(abs(before['x'] - after['x']) <= 1 and abs(before['y'] - after['y']) <= 1,
                 'Request-model validation does not move the submit button')
    runner.check(rect(error)['y'] >= after['bottom'] - 1, 'Validation occupies a separate row below controls')
    if page.viewport_size['width'] > 480:
        runner.check(abs(rect(field)['bottom'] - after['bottom']) <= 1, 'Model input and submit have aligned bottoms')
    runner.check(reads == sum(call['path'] == '/admin/routing/diagnostics' for call in data.calls),
                 'Invalid submit does not issue diagnostics')
    runner.photo(page, 'invalid')
    field.fill('invalid\u0001model')
    expect(field).to_have_attribute('aria-invalid', 'true')
    runner.check(abs(rect(submit)['y'] - before['y']) <= 1, 'Control-character validation also keeps submit position stable')
    field.fill('🚦' * 513)
    expect(field).to_have_value('🚦' * 512)
    expect(field).not_to_have_attribute('aria-invalid', 'true')
    runner.record(submit_before=before, submit_after=after, bounded_codepoints=512)


def routing_inventory(runner, page, data):
    rows = page.locator('.model-route-model')
    expect(rows).to_have_count(50)
    expect(page.locator('.model-route-snapshot')).to_be_visible()
    inventory = page.locator('.model-route-inventory-results')
    workspace = page.locator('.model-route-workspace')
    pager = page.get_by_role('navigation', name='模型清单分页', exact=True)
    start, right_before = rect(inventory), rect(workspace)
    runner.check(start['scrollHeight'] > start['clientHeight'] and start['overflowY'] in ('auto', 'scroll'),
                 'The 571-model inventory uses bounded internal scrolling')
    runner.check(right_before['bottom'] <= page.viewport_size['height'] + 1, 'Desktop diagnosis fits the viewport workspace')
    before = page.evaluate('({x:scrollX,y:scrollY})')
    inventory.evaluate('(element) => { element.scrollTop = element.scrollHeight }')
    page.wait_for_function("document.querySelector('.model-route-inventory-results').scrollTop > 0")
    end, right_after = rect(inventory), rect(workspace)
    runner.check(end['scrollTop'] + end['clientHeight'] >= end['scrollHeight'] - 2, 'Inventory reaches its final visible row')
    runner.check(page.evaluate('({x:scrollX,y:scrollY})') == before, 'Inventory scroll does not move the document')
    runner.check(all(abs(right_before[key] - right_after[key]) <= 1 for key in ('x', 'y', 'width', 'height', 'scrollTop')),
                 'Inventory scroll preserves right-hand geometry and scroll position')
    reachable(runner, rows.last, 'Long final model name')
    action_group(runner, pager, 'Inventory paging')
    runner.photo(page, 'inventory-bottom')
    pager.get_by_role('textbox', name='跳转页码', exact=True).fill('12')
    pager.get_by_role('textbox', name='跳转页码', exact=True).press('Enter')
    expect(rows).to_have_count(21)
    expect(pager).to_contain_text('551–571')
    expect(pager.get_by_role('button', name='下一页', exact=True)).to_be_disabled()
    runner.check(data.calls[-1]['method'] == 'GET', 'Inventory paging stays read-only')
    runner.record(inventory_before=start, inventory_after=end, workspace_before=right_before,
                  workspace_after=right_after, total=571, page_size=50, last_page_rows=21)


def checkins(runner, page, data):
    row = page.locator('.checkin-account-grid.record-row').filter(has_text='layout-checkin-account')
    expect(row).to_be_visible()
    actions = row.locator('.checkin-actions')
    link = actions.get_by_role('link', name='打开签到页', exact=True)
    reachable(runner, link, 'Manual checkin link')
    full_text(runner, link, '打开签到页')
    expect(link).to_have_attribute('href', data.sites[0]['external_checkin_url'])
    expect(actions.get_by_role('button', name='立即签到', exact=True)).to_be_enabled()
    action_group(runner, actions, 'Checkin actions')
    runner.check(contains(rect(row), rect(actions)), 'Checkin action column stays inside its row')
    runner.record(action_geometry=rect(actions), manual_link_geometry=rect(link))


def maintenance(runner, page, data):
    button = page.locator('.checkin-account-grid.record-row').filter(has_text='layout-checkin-account').get_by_role(
        'button', name='立即签到', exact=True)
    expect(button).to_be_disabled()
    page.wait_for_function("document.querySelector('.maintenance-page-notice')?.textContent.includes('进行中')")
    runner.check(data.task_reads > 0, 'A persisted server task is actually observed')
    for path, title in (('/accounts', '账号管理'), ('/sites', '站点管理'), ('/channels', '渠道分发'),
                        ('/announcements', '公告中心'), ('/checkins', '签到中心')):
        navigate(page, path, title)
        expect(page.get_by_text('站点维护任务', exact=True)).not_to_be_visible()
        panel = page.get_by_role('region', name='站点维护任务', exact=True)
        expect(panel).not_to_be_visible()
        runner.check(not panel.count() or not panel.is_visible(), f'{path} has no visible historical maintenance panel')
    expect(button).to_be_disabled()
    data.task_status = 'unknown'
    expect(page.locator('.maintenance-page-notice')).to_contain_text('结果待确认', timeout=6000)
    expect(button).to_be_disabled()
    observe = page.get_by_role('button', name='重新观察原任务', exact=True)
    reachable(runner, observe, 'Unknown-task recovery')
    runner.photo(page, 'unknown-retained')
    data.task_status = 'success'
    observe.click()
    expect(button).to_be_enabled(timeout=6000)
    expect(page.get_by_text('站点维护任务', exact=True)).not_to_be_visible()
    runner.check(not any(call['method'] != 'GET' for call in data.calls), 'Task observation and recovery issue only reads')
    runner.record(task_reads=data.task_reads, navigation_pages=5, unknown_blocked_duplicate=True)


def checkin_blocker_recovery(runner, page, data):
    rows = page.locator('.checkin-account-grid.record-row')
    blocked = rows.filter(has_text='layout-checkin-account')
    unrelated = rows.filter(has_text='layout-unrelated-checkin-account')
    submit = blocked.get_by_role('button', name='立即签到', exact=True)
    expect(submit).to_be_disabled()
    expect(unrelated.get_by_role('button', name='立即签到', exact=True)).to_be_enabled()
    expect(unrelated.locator('.checkin-blocker-recovery')).to_have_count(0)
    link = blocked.locator('a.checkin-blocker-recovery')
    expect(link).to_have_count(1)
    expect(page.locator('a.checkin-blocker-recovery')).to_have_count(1)
    account_id = data.accounts[0]['id']
    if data.task_kind == 'refresh':
        destination, title, operation = f'#/accounts?focus_account_id={account_id}', '账号管理', '刷新余额'
    else:
        destination, title, operation = '#/channels', '渠道分发', '同步模型与路由'
    expect(link).to_have_attribute('href', destination)
    link_box = reachable(runner, link, 'Cross-kind checkin blocker recovery link')
    runner.check(contains(rect(blocked), link_box), 'Recovery link remains inside its blocked account row')
    runner.check(bool(link.inner_text().strip()), 'Recovery link exposes a visible label')
    runner.check(not overlap(link_box, rect(submit)), 'Recovery link does not overlap the disabled checkin button')
    manual = blocked.get_by_role('link', name='打开签到页', exact=True)
    runner.check(not overlap(link_box, rect(manual)), 'Recovery link does not overlap the manual checkin link')
    runner.photo(page, 'blocked-account-recovery')
    link.click()
    expect(page.locator('h1')).to_have_text(title)
    actual = urlsplit(page.evaluate('location.hash.slice(1)'))
    expected = urlsplit(destination[1:])
    runner.check(actual.path == expected.path and parse_qs(actual.query) == parse_qs(expected.query),
                 'Blocker recovery follows the origin page and exact account scope')
    notice = page.locator('.maintenance-page-notice')
    expect(notice).to_contain_text('结果待确认')
    expect(notice).to_contain_text(f'账号 #{account_id}')
    expect(notice).to_contain_text(operation)
    observe = notice.get_by_role('button', name='重新观察原任务', exact=True)
    reachable(runner, observe, 'Origin-page scoped task recovery')
    runner.check(data.task_reads > 0, 'Cross-kind persisted task was read rather than locally invented')
    runner.check(not any(call['method'] != 'GET' for call in data.calls), 'Following blocker recovery issues only reads')
    runner.record(task_kind=data.task_kind, task_status=data.task_status, account_id=account_id,
                  origin=destination, blocker_link_geometry=link_box, unrelated_account_enabled=True,
                  touch=page.viewport_size['width'] <= 720)


def tokens(runner, page, data):
    rows = page.locator('.token-row')
    expect(rows).to_have_count(5)
    expect(page.get_by_text('全实例代理日志费用', exact=True)).not_to_be_visible()
    wrapper, heading = page.locator('.token-table-wrap'), page.locator('.token-table-head')
    if page.viewport_size['width'] >= 901:
        expect(heading).to_be_visible()
        head_box, row_box = rect(heading), rect(rows.first)
        runner.check(abs(head_box['x'] - row_box['x']) <= 1 and abs(head_box['width'] - row_box['width']) <= 1,
                     'Token heading and rows share the same table bounds')
        runner.check(head_box['background'] not in ('transparent', 'rgba(0, 0, 0, 0)'), 'Token table heading has a visible surface')
        columns, cells = heading.locator(':scope > *'), rows.first.locator(':scope > *')
        runner.check(columns.count() == cells.count(), 'Token heading and rows expose matching columns')
        offsets = [abs(rect(columns.nth(index))['x'] - rect(cells.nth(index))['x']) for index in range(columns.count())]
        runner.check(max(offsets) <= 3, 'Token heading columns align with their corresponding cells')
        runner.record(column_offsets=offsets)
    action_group(runner, rows.first.locator('.row-actions'), 'Token row actions')
    details = rows.first.get_by_role('button', name=re.compile('^查看 .* 详情$'))
    details.click()
    dialog = page.get_by_role('dialog')
    expect(dialog).to_be_visible()
    expect(dialog).to_contain_text(data.tokens[0]['description'])
    runner.photo(page, 'token-details')
    dialog.get_by_role('button', name='关闭弹窗', exact=True).click()
    search = page.get_by_role('textbox', name='搜索令牌', exact=True)
    search.fill(data.tokens[0]['description'])
    expect(rows).to_have_count(1)
    runner.record(retained_rows=1, instance_cost_panel_removed=True)


def balances(runner, page, data):
    # Stage2 changes two separate charts into one plot. Reuse the rendered-path
    # oracle rather than dropping the original currency/sign/keyboard checks.
    import console_alignment_daily_smoke as balance_checks
    balance_checks.expect = expect
    metrics = balance_checks.shared_chart_geometry(runner, page, data.balance_history)
    for currency in ('USD', 'CNY'):
        runner.check(len(metrics['series'][currency]['edges']) == 2,
                     f'{currency} still plots three consecutive actual daily changes as smooth cubics')
    for day in ('2026-11-21', '2026-11-22'):
        balance_checks.shared_readout(runner, page, data.balance_history, day)
        runner.photo(page, f'balance-{day}')
    runner.record(shared_plot=metrics, currencies=['USD', 'CNY'], daily_samples_per_currency=4,
                  comparable_changes_per_currency=3)


def health(runner, page, data):
    target = page.locator('#runtime-debug_cleanup')
    expect(target).to_be_visible()
    expect(target).to_be_focused()
    runner.check(target.evaluate('(element) => element.closest("details").open'), 'Health deep link reveals its containing section')
    for section in page.locator('.health-section:visible').all():
        heading = section.locator(':scope > header h2, :scope > summary h2').first
        expect(heading).to_be_visible()
        heading.scroll_into_view_if_needed()
        runner.check(contains(rect(section), rect(heading)), 'Health section heading is attached inside its panel')
        runner.check(rect(section)['background'] not in ('transparent', 'rgba(0, 0, 0, 0)'), 'Health section heading belongs to a surfaced panel')
    websocket = page.locator('#runtime-websocket')
    expect(websocket).to_be_visible()
    websocket.locator('h3').scroll_into_view_if_needed()
    runner.check(contains(rect(websocket), rect(websocket.locator('h3'))), 'WebSocket heading stays inside its component panel')
    target.scroll_into_view_if_needed()
    runner.record(deep_link=page.evaluate('location.hash'), revealed_component='debug_cleanup')


def settings(runner, page, data):
    group_buttons = page.get_by_role('complementary', name='设置分类').get_by_role('button')
    expect(page.get_by_role('button', name=re.compile('^请求超时'))).to_have_attribute('aria-pressed', 'true')
    for title, keys in TIMEOUT_GROUPS.items():
        heading = page.get_by_role('heading', name=title, exact=True)
        expect(heading).to_have_count(1)
        section = heading.locator('xpath=ancestor::section[1]')
        for key in keys:
            expect(section.locator(f'[data-setting-key="{key}"]')).to_have_count(1)
    content = page.locator('.settings-editor-content')
    initial = rect(content)
    if page.viewport_size['width'] > 900:
        runner.check(initial['scrollHeight'] > initial['clientHeight'] and initial['overflowY'] in ('auto', 'scroll'), 'Desktop settings have a bounded scrolling editor')
        runner.check(initial['height'] < page.viewport_size['height'], 'Settings editor height is bounded by the desktop viewport')
    else:
        runner.check(initial['overflowY'] == 'visible', 'Mobile settings use document flow')
    local = page.get_by_role('navigation', name='当前设置分组', exact=True)
    runner.photo(page, 'timeout-groups')
    local.get_by_role('button', name=re.compile('^Gemini')).click()
    expect(page.get_by_role('heading', name='Gemini', exact=True)).to_be_focused()
    field = page.locator('[data-setting-key="gemini_first_byte_timeout"] input')
    reachable(runner, field, 'Protocol timeout draft')
    field.fill('47')
    # A footer inside main is not an implicit contentinfo landmark. Scope its
    # actual status and actions instead of matching a landmark or spaced copy.
    footer = page.locator('footer.settings-savebar')
    expect(footer.get_by_role('status')).to_have_text(re.compile(r'^1\s*项未保存$'))
    modified = page.locator('.settings-modified-filter')
    expect(modified).to_have_text(re.compile(r'^仅(?:显示修改|看已修改)\s*1$'))
    save = page.get_by_role('button', name='保存更改', exact=True)
    reachable(runner, save, 'Settings save action')
    expect(save).to_be_enabled()
    group_buttons.filter(has_text='路由策略').click()
    modified.get_by_role('checkbox').check()
    expect(modified.get_by_role('checkbox')).to_be_checked()
    expect(field).to_have_value('47')
    expect(page.locator('.settings-editor h2')).to_have_text('未保存修改')
    field.focus()
    expect(field).to_be_focused()
    modified.get_by_role('checkbox').uncheck()
    expect(modified.get_by_role('checkbox')).not_to_be_checked()
    search = page.get_by_role('textbox', name='搜索全部设置', exact=True)
    reachable(runner, search, 'Settings search across categories')
    search.fill('request_cache_max_bytes')
    capacity = page.locator('[data-setting-key="request_cache_max_bytes"] input')
    expect(capacity).to_have_value('32')
    reachable(runner, capacity, 'Invalid capacity draft')
    capacity.fill('129')
    expect(capacity).to_have_attribute('aria-invalid', 'true')
    expect(footer.get_by_role('status')).to_have_text(re.compile(r'^2\s*项未保存$'))
    group_buttons.filter(has_text='路由策略').click()
    expect(capacity).to_have_count(0)
    modified.get_by_role('checkbox').check()
    expect(modified.get_by_role('checkbox')).to_be_checked()
    expect(modified).to_have_text(re.compile(r'^仅(?:显示修改|看已修改)\s*2$'))
    expect(page.locator('.settings-editor h2')).to_have_text('未保存修改')
    expect(field).to_have_value('47')
    expect(capacity).to_have_value('129')
    expect(capacity).to_have_attribute('aria-invalid', 'true')
    error_id = capacity.get_attribute('aria-describedby')
    runner.check(bool(error_id), 'Retained invalid settings draft has an accessible error association')
    error = page.locator(f'[id="{error_id}"]')
    expect(error).to_have_attribute('role', 'alert')
    expect(error).to_contain_text('128')
    reachable(runner, capacity, 'Retained invalid draft in modified settings')
    reachable(runner, error, 'Retained invalid draft error')
    runner.photo(page, 'dirty-draft')
    reachable(runner, save, 'Settings validation recovery action')
    save.click()
    expect(capacity).to_be_focused()
    expect(capacity).to_have_value('129')
    expect(capacity).to_have_attribute('aria-invalid', 'true')
    expect(footer.get_by_role('status')).to_have_text(re.compile(r'^2\s*项未保存$'))
    reachable(runner, capacity, 'Invalid draft revealed after saving is blocked')
    runner.photo(page, 'invalid-save-recovery')
    runner.check(not data.settings_writes, 'Reading and accessing dirty drafts does not save settings')
    runner.record(editor_geometry=initial, timeout_sections=list(TIMEOUT_GROUPS), retained_draft='47',
                  retained_invalid_draft='129', dirty_drafts=2, invalid_save_blocked=True)


def related(runner, page, data, profile):
    name, _, title, surface_selector, rows_selector = profile
    expect(page.locator('h1')).to_have_text(title)
    surface, rows = page.locator(surface_selector).first, page.locator(rows_selector)
    expect(surface).to_be_visible()
    expect(rows.first).to_be_visible()
    runner.check(rows.count() > 0, f'{name} shows actual fixture content')
    account_scroller = name == 'accounts' and page.viewport_size['width'] > 880
    if account_scroller:
        surface.evaluate('(element) => { element.scrollLeft = 0 }')
        start, first_before = rect(surface), rect(rows.first)
        document_x = page.evaluate('scrollX')
        runner.check(start['x'] >= -1 and start['right'] <= page.viewport_size['width'] + 1,
                     'Account table scroll viewport is contained horizontally')
        runner.check(start['overflowX'] in ('auto', 'scroll')
                     and start['scrollWidth'] > start['clientWidth'],
                     'Wide account table uses an internal horizontal scroller')
        scrolled = surface.evaluate('element => { element.scrollLeft = element.scrollWidth; return element.scrollLeft }')
        first_after = rect(rows.first)
        runner.check(scrolled > 0 and abs(first_before['x'] - first_after['x'] - scrolled) <= 2,
                     'Account rows pan inside their scroll container')
        runner.check(page.evaluate('scrollX') == document_x
                     and page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'),
                     'Panning accounts does not scroll or widen the document')
        runner.record(account_scroll_viewport=start, account_scroll_distance=scrolled)
        surface.evaluate('(element) => { element.scrollLeft = 0 }')
    for index in sorted({0, min(1, rows.count() - 1), rows.count() - 1}):
        row = rows.nth(index)
        row.scroll_into_view_if_needed()
        box = rect(row)
        if account_scroller:
            reachable(runner, row.locator(':scope > div').first, f'Account row {index + 1} identity')
        else:
            runner.check(box['x'] >= -1 and box['right'] <= page.viewport_size['width'] + 1,
                         f'{name} content row {index + 1} fits horizontally')
        controls = row.locator('.row-actions, .account-actions, .site-actions, .model-card-actions, footer').first
        if controls.count() and controls.locator('a:visible, button:visible').count():
            action_group(runner, controls, f'{name} row {index + 1}')
            if account_scroller:
                outer, actions = rect(surface), rect(controls)
                runner.check(actions['x'] >= outer['x'] - 2 and actions['right'] <= outer['right'] + 2,
                             f'Account row {index + 1} actions can be revealed within the scroll viewport')
    if name == 'models':
        page.get_by_role('textbox', name='搜索模型清单', exact=True).fill('layout-model-001')
        expect(page.locator('.model-card')).to_have_count(1)
    elif name == 'announcements':
        page.get_by_role('combobox', name='公告站点', exact=True).select_option(str(data.sites[0]['id']))
        expect(page.locator('.announcement-row')).to_have_count(1)
    runner.photo(page, 'content-end')
    page.get_by_role('heading', name=title, exact=True).scroll_into_view_if_needed()
    action_group(runner, page.locator('.header-controls'), f'{name} page header')
    runner.record(content_rows=rows.count(), viewport=page.viewport_size)


def case_specs(wallpaper=False):
    specs = []
    def add(name, callback, path, width=1440, height=900, theme='light', photo=False):
        specs.append((name, callback, {'path': path, 'width': width, 'height': height,
                                      'theme': theme, 'touch': width <= 720,
                                      'wallpaper': photo, 'glass': photo}))
    for width, height in ((1440, 900), (390, 844), (320, 900)):
        add(f'routing-error-stable-{width}', routing_validation, '/routing', width, height)
    add('routing-inventory-scroll-1440', routing_inventory, f'/routing?model={MODEL}')
    add('routing-inventory-scroll-1000', routing_inventory, f'/routing?model={MODEL}', 1000, 700)
    for width, height in ((1440, 900), (1000, 900), (390, 844)):
        add(f'checkin-actions-{width}', checkins, '/checkins', width, height)
    add('maintenance-cross-kind-refresh-1440', checkin_blocker_recovery, '/checkins')
    add('maintenance-cross-kind-model-refresh-390', checkin_blocker_recovery, '/checkins', 390, 844)
    for width, height in ((1440, 900), (390, 844)):
        add(f'maintenance-persisted-navigation-{width}', maintenance, '/checkins', width, height)
        add(f'tokens-surface-header-{width}', tokens, '/tokens', width, height)
        add(f'stats-currency-balance-lines-{width}', balances, '/stats', width, height)
        add(f'health-panel-headings-deeplink-{width}', health, '/health?component=debug_cleanup', width, height)
    for width, height in ((1440, 900), (1000, 700), (390, 844)):
        add(f'settings-timeout-draft-workspace-{width}', settings, '/system?section=runtime&group=timeouts', width, height)
    for profile in RELATED:
        def callback(runner, page, data, profile=profile):
            related(runner, page, data, profile)
        for width, height in ((1440, 900), (390, 844)):
            add(f'related-{profile[0]}-{width}', callback, profile[1], width, height)
    if wallpaper:
        add('routing-photo-inventory-scroll', routing_inventory, f'/routing?model={MODEL}', photo=True)
        add('tokens-photo-surface-header', tokens, '/tokens', theme='dark', photo=True)
        add('settings-photo-timeout-workspace', settings, '/system?section=runtime&group=timeouts', photo=True)
    return specs


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument('--output', type=Path)
    parser.add_argument('--case', action='append', default=[])
    parser.add_argument('--wallpaper-fixture', type=Path, help='Local JPEG intercepted as synthetic wallpaper')
    parser.add_argument('--max-seconds', type=int, default=240, help='Suite admission budget; no new case starts after this deadline')
    parser.add_argument('--list-cases', action='store_true')
    args = parser.parse_args()
    specs = case_specs(bool(args.wallpaper_fixture))
    names = {name for name, _, _ in specs}
    if set(args.case) - names:
        parser.error('Unknown case(s): ' + ', '.join(sorted(set(args.case) - names)))
    if args.list_cases:
        print('\n'.join(name for name, _, _ in specs))
        return
    if not args.output or not 1 <= args.max_seconds <= 900:
        parser.error('--output is required and --max-seconds must be between 1 and 900')
    root, output = args.root.resolve(), args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    result = {'pass': False, 'checks': [], 'evidence': [], 'metrics': {},
              'unexpected_events': [], 'server_closed': False}
    server, thread, runner = None, None, None
    try:
        server = ThreadingHTTPServer(('127.0.0.1', 0), partial(StaticHandler, root=root))
        # Windows can allocate 10080 for port 0; Chromium blocks that port
        # before any page or request interception can run.
        while server.server_port == 10080:
            server.server_close()
            server = ThreadingHTTPServer(('127.0.0.1', 0), partial(StaticHandler, root=root))
        server.daemon_threads = True
        thread = Thread(target=server.serve_forever, daemon=True)
        thread.start()
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True, args=['--no-proxy-server', '--disable-background-networking'])
            try:
                runner = LayoutRunner(browser, f'http://127.0.0.1:{server.server_port}', output,
                                      args.case, args.wallpaper_fixture, args.max_seconds)
                for name, callback, options in specs:
                    runner.case(name, lambda page, data, callback=callback: callback(runner, page, data), **options)
            finally:
                browser.close()
    except Exception as error:
        result['fatal_error'] = f'{type(error).__name__}: {error}'[:2200]
    finally:
        if runner is not None:
            result.update(checks=runner.results, metrics=runner.metrics, unexpected_events=runner.events,
                          assertions=runner.assertions)
            result['evidence'] = [check['evidence'] for check in runner.results if check.get('evidence')]
            result['evidence'].extend(image for metrics in runner.metrics.values() for image in metrics.get('evidence', []))
        if server is not None:
            if thread is not None and thread.is_alive():
                server.shutdown()
            server.server_close()
        if thread is not None:
            thread.join(timeout=5)
        result['server_closed'] = thread is None or not thread.is_alive()
        result['pass'] = (bool(result['checks']) and all(check['pass'] for check in result['checks'])
                          and not result['unexpected_events'] and not result.get('fatal_error')
                          and result['server_closed'])
        (output / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'pass': result['pass'], 'checks': len(result['checks']),
                      'unexpected_events': len(result['unexpected_events']),
                      'server_closed': result['server_closed'], 'fatal_error': result.get('fatal_error', '')}, ensure_ascii=False))
    raise SystemExit(0 if result['pass'] else 1)


if __name__ == '__main__':
    main()
