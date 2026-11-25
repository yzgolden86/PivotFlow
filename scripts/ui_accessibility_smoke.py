"""Exercise real console keyboard/reading flows using synthetic, blocked APIs.

No live gateway, database, login, upstream or WebDAV is used. Screenshots are
visible-viewport JPEGs. The owned static server is closed even after a failure.
"""
import argparse
import copy
import gzip
import json
from functools import partial
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread
import time
from urllib.parse import parse_qs, urlparse
from urllib.request import ProxyHandler, build_opener

from playwright.sync_api import expect, sync_playwright

import docs_screenshots as fixtures
from console_visual_regression import SETTINGS


ROOT = Path(__file__).resolve().parents[1]
LONG_NAME = '长名称维护对象-' + '模型与渠道需要完整读取-' * 12
LONG_URL = 'https://synthetic.example.invalid/' + 'long-path-without-break-' * 18
LONG_MODEL = 'synthetic-model-' + 'long-name-' * 24


class StaticHandler(BaseHTTPRequestHandler):
    def __init__(self, *args, root, **kwargs):
        self.root = root
        super().__init__(*args, **kwargs)

    def log_message(self, *args):
        pass

    def do_GET(self):
        path = urlparse(self.path).path
        if not (path.startswith('/web/console/') or path in ('/web/brand-mark.svg', '/web/favicon.svg')):
            self.send_error(404)
            return
        relative = path.lstrip('/')
        if relative.endswith('/'):
            relative += 'index.html'
        target = (self.root / relative).resolve()
        if not target.is_relative_to((self.root / 'web').resolve()) or not target.is_file():
            self.send_error(404)
            return
        data = target.read_bytes()
        types = {'.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8',
                 '.css': 'text/css; charset=utf-8', '.svg': 'image/svg+xml', '.woff2': 'font/woff2'}
        compressed = 'gzip' in self.headers.get('Accept-Encoding', '')
        if compressed:
            data = gzip.compress(data, mtime=0)
        self.send_response(200)
        self.send_header('Content-Type', types.get(target.suffix, 'application/octet-stream'))
        self.send_header('Content-Length', str(len(data)))
        self.send_header('Cache-Control', 'no-store')
        if compressed:
            self.send_header('Content-Encoding', 'gzip')
        self.end_headers()
        try:
            self.wfile.write(data)
        except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
            pass


class SyntheticData:
    def __init__(self):
        self.calls = []
        self.fail_models = False
        self.fail_channels = False
        self.fail_inventory = False
        self.hold_models = False
        self.pending_models = []
        self.token_writes = []
        self.key_checks = []
        self.sites = []
        self.accounts = []
        self.channels = []
        self.tokens = []
        self.models = []
        for number in range(1, 121):
            site = copy.deepcopy(fixtures.SITES[0])
            site.update(id=number, name=f'合成站点 {number:03d}', base_url=f'https://site-{number}.example.invalid',
                        use_system_proxy=True, proxy_url='', external_checkin_url='')
            self.sites.append(site)
            for offset in range(2):
                account = copy.deepcopy(fixtures.ACCOUNTS[1][0])
                account.update(id=number * 10 + offset, site_id=number, label=f'合成账号 {number:03d}-{offset}')
                self.accounts.append(account)
            channel = copy.deepcopy(fixtures.CHANNELS[0])
            channel.update(id=number, name=f'合成渠道 {number:03d}', key_count=120, effective_key_count=110,
                           urls=[{'url': f'https://channel-{number}.example.invalid', 'exact': False, 'protocols': ['openai', 'codex']}],
                           models=[{'model': f'model-{number:04d}', 'disabled': False, 'redirect': ''}],
                           rpm_limit=12, max_concurrency=3, daily_cost_limit=2.5, enabled=True)
            self.channels.append(channel)
        self.sites[-1].update(name=LONG_NAME, base_url=LONG_URL, last_error='合成探测说明：' + LONG_NAME)
        self.channels[0].update(name=LONG_NAME, urls=[{'url': LONG_URL, 'exact': False, 'protocols': ['openai', 'codex']}],
                                models=[{'model': LONG_MODEL, 'disabled': False, 'redirect': ''}])
        for number in range(1, 152):
            token = copy.deepcopy(fixtures.AUTH_TOKENS[0])
            token.update(id=number, description=f'合成令牌 {number:03d}', token='', token_hint=f'opaque-hint-{number}',
                         token_recoverable=True, created_at=f'2026-11-20T08:{number // 60:02d}:{number % 60:02d}+08:00',
                         allowed_models=['model-0001', LONG_MODEL], allowed_channel_ids=[1, 120],
                         allowed_protocols=['openai', 'codex'], channel_restriction_mode='deny',
                         cost_limit_usd=9.5, max_concurrency=3, last_used_at=fixtures.NOW,
                         expires_at=fixtures.NOW + 86400000)
            self.tokens.append(token)
        self.tokens[-1]['description'] = LONG_NAME
        for number in range(2000):
            self.models.append({'site_account_id': 10, 'model': f'model-{number:04d}', 'route_type': 'openai',
                                'source': 'synthetic', 'enabled': True, 'created_at': fixtures.NOW, 'updated_at': fixtures.NOW})

    def fulfill(self, route, data, count=None):
        route.fulfill(status=200, content_type='application/json',
                      body=json.dumps(fixtures.envelope(data, count), ensure_ascii=False))

    def route(self, route):
        parsed = urlparse(route.request.url)
        path, query = parsed.path, parse_qs(parsed.query)
        self.calls.append({'path': path, 'method': route.request.method, 'query': query})
        if path == '/admin/site-inventory':
            if self.fail_inventory:
                route.fulfill(status=503, content_type='application/json', body='{"success":false,"error":"synthetic_inventory_unavailable"}')
                return
            self.fulfill(route, {'sites': self.sites, 'accounts': self.accounts})
        elif path == '/admin/channels':
            if self.fail_channels:
                route.fulfill(status=503, content_type='application/json', body='{"success":false,"error":"synthetic_channel_unavailable"}')
                return
            items = self.channels
            if query.get('search'):
                needle = query['search'][0].lower()
                items = [c for c in items if needle in c['name'].lower() or any(needle in u['url'] for u in c['urls'])]
            offset, limit = int(query.get('offset', ['0'])[0]), int(query.get('limit', ['50'])[0])
            self.fulfill(route, items[offset:offset + limit], len(items))
        elif path == '/admin/site-models':
            if self.hold_models:
                self.pending_models.append(route)
                return
            if self.fail_models:
                route.fulfill(status=503, content_type='application/json', body='{"success":false,"error":"synthetic_model_unavailable"}')
                return
            offset, limit = int(query.get('offset', ['0'])[0]), int(query.get('limit', ['1000'])[0])
            self.fulfill(route, self.models[offset:offset + limit], len(self.models))
        elif path == '/admin/routing/models':
            # Token permissions now use configured enabled models, rather than
            # the unrelated account-discovery records used by global search.
            models = {}
            for channel in self.channels:
                if not channel['enabled']:
                    continue
                for entry in channel['models']:
                    if entry['disabled'] or entry['model'] == '*':
                        continue
                    name = entry['model']
                    item = models.setdefault(name, {'model': name, 'kind': 'model', 'aliases': [],
                                                    'alias_count': 0, 'configured_channels': 0,
                                                    'enabled_channels': 0})
                    item['configured_channels'] += 1
                    item['enabled_channels'] += 1
            items = sorted(models.values(), key=lambda item: item['model'].casefold())
            if query.get('search'):
                needle = query['search'][0].casefold()
                items = [item for item in items if needle in item['model'].casefold()]
            offset, limit = int(query.get('offset', ['0'])[0]), int(query.get('limit', ['50'])[0])
            self.fulfill(route, {'version': 1, 'items': items[offset:offset + limit],
                                 'total': len(items), 'offset': offset, 'limit': limit})
        elif path == '/admin/auth-tokens' and route.request.method == 'GET':
            self.fulfill(route, {'tokens': self.tokens, 'is_today': True})
        elif path == '/admin/auth-tokens' and route.request.method == 'POST':
            payload = route.request.post_data_json
            self.token_writes.append(payload)
            token = {**self.tokens[0], **payload, 'id': 999, 'token': 'synthetic-created-value', 'token_hint': 'synthetic-created-hint'}
            self.fulfill(route, token)
        elif path.startswith('/admin/auth-tokens/') and route.request.method == 'DELETE':
            target = int(path.rsplit('/', 1)[-1])
            self.tokens = [t for t in self.tokens if t['id'] != target]
            self.fulfill(route, {'message': 'synthetic deleted'})
        elif path == '/admin/settings':
            self.fulfill(route, SETTINGS)
        elif path == '/admin/model-alias-inventory':
            self.fulfill(route, {'candidates': [], 'suggestions': [], 'total_models': 0})
        elif path == '/admin/channels/1/test' and route.request.method == 'POST':
            self.key_checks.append(route.request.post_data_json)
            self.fulfill(route, {'key_health': {'status': 'healthy', 'checked_at': fixtures.NOW,
                                               'status_code': 200, 'reason': 'synthetic-only'}})
        elif path.endswith('/key-health'):
            self.fulfill(route, {'channel_id': 1, 'channel_name': LONG_NAME, 'models': ['model-0001'],
                                'keys': [{'id': n, 'key_index': n - 1, 'masked_key': f'masked-{n}', 'note': '',
                                          'disabled': False, 'allowed_models': [], 'cost_multiplier': 1,
                                          'model_scope_empty': False, 'cooldown_until': 0,
                                          'health': {'status': 'unknown', 'checked_at': 0, 'status_code': 0, 'reason': ''}}
                                         for n in range(1, 121)]})
        else:
            fixtures.mock_admin(route)


class Runner:
    def __init__(self, browser, base, output, case_names=None):
        self.browser, self.base, self.output = browser, base, output
        self.results = []
        self.metrics = {}
        self.events = []
        self.case_names = case_names or []

    def case(self, name, callback, path='/tokens', width=1440, height=900, theme='light'):
        if self.case_names and name not in self.case_names:
            return
        context = self.browser.new_context(viewport={'width': width, 'height': height},
                                           color_scheme=theme, reduced_motion='reduce')
        context.set_default_timeout(5000)
        context.set_default_navigation_timeout(30000)
        context.add_init_script("localStorage.setItem('pivotflow_token','synthetic-accessibility-only');localStorage.setItem('pivotflow_web_role','admin');")
        context.add_init_script(f"localStorage.setItem('pivotflow_theme', {json.dumps(theme)});")
        data = SyntheticData()
        page = context.new_page()
        page.on('pageerror', lambda error: self.events.append({'case': name, 'error': str(error)}))
        origin = urlparse(self.base).netloc

        def intercept(route):
            parsed = urlparse(route.request.url)
            if parsed.netloc != origin:
                self.events.append({'case': name, 'external_blocked': parsed.netloc})
                route.abort('blockedbyclient')
            elif parsed.path.startswith(('/admin/', '/dashboard/')):
                data.route(route)
            else:
                route.continue_()

        context.route('**/*', intercept)
        started = time.perf_counter()
        try:
            page.goto(f'{self.base}/web/console/#{path}', wait_until='networkidle')
            expect(page.locator('h1')).to_be_visible()
            callback(page, data)
            self.results.append({'name': name, 'pass': True, 'seconds': round(time.perf_counter() - started, 3)})
        except Exception as error:
            self.results.append({'name': name, 'pass': False, 'error': str(error)[:1800],
                                 'seconds': round(time.perf_counter() - started, 3),
                                 'layout': page.evaluate("""() => ({path:location.hash, viewport:innerWidth,
                                   scroll:document.documentElement.scrollWidth,
                                   overflow:[...document.querySelectorAll('main *')].map(e => ({e,r:e.getBoundingClientRect()}))
                                     .filter(({r}) => r.width && (r.right>innerWidth+1 || r.left < -1))
                                     .slice(0,12).map(({e,r}) => ({tag:e.tagName,class:e.className,left:r.left,right:r.right}))})"""),
                                 'focus': page.evaluate("""() => ({active: {tag:document.activeElement?.tagName,
                                   label:document.activeElement?.getAttribute('aria-label'), class:document.activeElement?.className},
                                   menus:[...document.querySelectorAll('[role=menu]')].map(e => ({visible:getComputedStyle(e).visibility,
                                     inert:!!e.closest('[inert]'), items:[...e.querySelectorAll('button')].map(b => ({text:b.textContent,
                                       focused:b===document.activeElement, visible:getComputedStyle(b).visibility, inert:!!b.closest('[inert]')}))}))})""")})
        finally:
            context.close()

    def check(self, condition, message):
        if not condition:
            raise AssertionError(message)


def focus_cases(runner):
    def modal_lock(page, data):
        page.get_by_role('button', name='创建令牌', exact=True).click()
        expect(page.get_by_role('dialog', name='创建令牌', exact=True)).to_be_visible()
        runner.check(page.evaluate("document.body.style.overflow === 'hidden'"), 'Modal must lock background scrolling')
        page.keyboard.press('Escape')
        expect(page.get_by_role('button', name='创建令牌', exact=True)).to_be_focused()
        runner.check(page.evaluate("document.body.style.overflow !== 'hidden'"), 'Last Modal must restore body overflow')
    runner.case('modal-scroll-lock-and-return', modal_lock)

    def nested_escape(page, data):
        page.get_by_role('button', name='创建令牌', exact=True).click()
        page.keyboard.press('Control+k')
        expect(page.get_by_role('dialog', name='全局搜索', exact=True)).to_be_visible()
        page.keyboard.press('Escape')
        expect(page.get_by_role('dialog', name='创建令牌', exact=True)).to_be_visible()
        runner.check(page.evaluate("document.body.style.overflow === 'hidden'"), 'Closing search must retain parent scroll lock')
        runner.check(page.evaluate("document.querySelector('.console-modal').contains(document.activeElement)"), 'Search must return focus into parent Modal')
        page.keyboard.press('Escape')
        expect(page.get_by_role('button', name='创建令牌', exact=True)).to_be_focused()
    runner.case('nested-search-escape-only-top-layer', nested_escape)

    def non_top_removal(page, data):
        page.get_by_role('button', name='创建令牌', exact=True).click()
        page.keyboard.press('Control+k')
        search = page.get_by_role('dialog', name='全局搜索', exact=True)
        expect(search).to_be_visible()
        page.locator('.console-modal > header button').dispatch_event('click')
        expect(page.get_by_role('dialog', name='创建令牌', exact=True)).not_to_be_visible()
        expect(search.get_by_role('combobox')).to_be_focused()
        runner.check(page.evaluate("document.body.style.overflow === 'hidden'"), 'Removing a lower layer must keep the upper scroll lock')
        page.keyboard.press('Escape')
        expect(page.locator('#main-content')).to_be_focused()
    runner.case('programmatic-parent-removal-keeps-top-focus-and-fallback', non_top_removal)

    def search_trap(page, data):
        page.get_by_role('button', name='全局搜索', exact=True).click()
        page.get_by_role('button', name='关闭全局搜索', exact=True).focus()
        for _ in range(18):
            page.keyboard.press('Tab')
            runner.check(page.evaluate("document.querySelector('.search-dialog').contains(document.activeElement)"), 'Tab escaped the search dialog')
        page.keyboard.press('Escape')
        expect(page.get_by_role('button', name='全局搜索', exact=True)).to_be_focused()
    runner.case('search-tab-contained-and-return', search_trap, path='/sites')

    def drawer_search(page, data):
        page.get_by_role('button', name='打开导航', exact=True).click()
        page.get_by_role('button', name='全局搜索', exact=True).click()
        expect(page.get_by_role('dialog', name='全局搜索', exact=True)).to_be_visible()
        page.keyboard.press('Escape')
        expect(page.get_by_role('dialog', name='控制台导航', exact=True)).to_be_visible()
        expect(page.get_by_role('button', name='全局搜索', exact=True)).to_be_focused()
        page.keyboard.press('Escape')
        expect(page.get_by_role('button', name='打开导航', exact=True)).to_be_focused()
    runner.case('drawer-nested-search-keeps-navigation', drawer_search, path='/sites', width=390, height=844)

    def drawer_backdrop(page, data):
        opener = page.get_by_role('button', name='打开导航', exact=True)
        opener.click()
        drawer = page.get_by_role('dialog', name='控制台导航', exact=True)
        expect(drawer).to_be_visible()
        backdrop = page.locator('.sidebar-scrim')
        point = backdrop.evaluate('(e) => {const r=e.getBoundingClientRect(); return {x:r.right-6,y:r.bottom-6}}')
        runner.metrics['drawer_backdrop'] = backdrop.evaluate(
            '(e) => ({inert: !!e.closest("[inert]"), width: e.getBoundingClientRect().width})')
        runner.metrics['drawer_backdrop']['pointer_target'] = page.evaluate(
            'p => document.elementFromPoint(p.x,p.y)?.className', point)
        page.mouse.click(point['x'], point['y'])
        expect(drawer).not_to_be_visible()
        expect(opener).to_be_focused()
        runner.check(page.evaluate("document.body.style.overflow !== 'hidden'"), 'Backdrop dismissal must release the final scroll lock')
        opener.click()
        page.get_by_role('button', name='全局搜索', exact=True).click()
        search = page.get_by_role('dialog', name='全局搜索', exact=True)
        expect(search).to_be_visible()
        page.mouse.click(point['x'], point['y'])
        expect(search).not_to_be_visible()
        expect(drawer).to_be_visible()
        runner.check(page.evaluate("document.body.style.overflow === 'hidden'"), 'Search pointer dismissal must retain the drawer lock')
        page.mouse.click(point['x'], point['y'])
        expect(drawer).not_to_be_visible()
        expect(opener).to_be_focused()
    runner.case('mobile-backdrop-pointer-dismissal-and-nested-search', drawer_backdrop, path='/sites', width=390, height=844)

    def live_notice(page, data):
        data.fail_inventory = True
        page.get_by_role('button', name='刷新站点', exact=True).click()
        notice = page.locator('.operation-notice')
        expect(notice).to_contain_text('synthetic_inventory_unavailable')
        page.locator('.resource-details-trigger').first.click()
        dialog = page.get_by_role('dialog', name='站点详情', exact=True)
        expect(dialog).to_be_visible()
        content = notice.locator('.operation-notice__content')
        runner.check(content.evaluate('(e) => !e.closest("[inert]")'), 'Operation text must stay exposed while the modal is open')
        session = page.context.new_cdp_session(page)
        try:
            nodes = session.send('Accessibility.getFullAXTree')['nodes']
        finally:
            session.detach()
        exposed = [node for node in nodes if not node.get('ignored')]
        runner.check(any('synthetic_inventory_unavailable' in node.get('name', {}).get('value', '') for node in exposed),
                     'Chromium accessibility tree must include the operation error')
        runner.check(notice.get_by_role('button', name='关闭提示', exact=True, include_hidden=True).evaluate(
            '(e) => !!e.closest("[inert]")'), 'Background notice controls must remain isolated')
        runner.check(page.evaluate("document.body.style.overflow === 'hidden'"), 'A live region must not release the modal lock')
        page.keyboard.press('Escape')
        expect(dialog).not_to_be_visible()
        expect(notice.get_by_role('button', name='关闭提示', exact=True)).to_be_visible()
        notice.get_by_role('button', name='关闭提示', exact=True).click()
        expect(notice).not_to_be_visible()
    runner.case('operation-live-region-exposed-with-background-controls-isolated', live_notice, path='/sites')

    def menu_keyboard(page, data):
        button = page.get_by_role('button', name='选择界面主题', exact=True)
        button.focus()
        page.keyboard.press('Enter')
        menu = page.get_by_role('menu', name='界面主题', exact=True)
        expect(menu.get_by_role('menuitemradio').first).to_be_focused()
        page.keyboard.press('End')
        expect(menu.get_by_role('menuitemradio').last).to_be_focused()
        page.keyboard.press('ArrowDown')
        expect(menu.get_by_role('menuitemradio').first).to_be_focused()
        page.keyboard.press('Escape')
        expect(button).to_be_focused()
    runner.case('theme-menu-keyboard-and-return', menu_keyboard, path='/sites')

    def menu_tab(page, data):
        opener = page.get_by_role('button', name='选择界面主题', exact=True)
        opener.click()
        page.keyboard.press('Tab')
        expect(page.get_by_role('menu', name='界面主题', exact=True)).not_to_be_visible()
        expect(page.get_by_role('button', name='退出登录', exact=True)).to_be_focused()
        opener.click()
        page.keyboard.press('Shift+Tab')
        expect(page.get_by_role('link', name='系统设置', exact=True)).to_be_focused()
    runner.case('menu-tab-continues-on-both-sides-of-trigger', menu_tab, path='/sites')

    def key_return(page, data):
        opener = page.locator('.channel-key-health-link').first
        opener.click()
        expect(page.get_by_role('dialog')).to_be_visible()
        page.keyboard.press('Escape')
        expect(opener).to_be_focused()
    runner.case('key-health-single-focus-owner', key_return, path='/channels')

    def disabled_return(page, data):
        page.get_by_role('button', name='创建令牌', exact=True).click()
        page.evaluate("document.querySelector('.header-controls .primary-button').disabled = true")
        page.keyboard.press('Escape')
        expect(page.locator('#main-content')).to_be_focused()
    runner.case('removed-or-disabled-opener-fallback', disabled_return)

    def tab_boundaries(page, data):
        page.get_by_role('button', name='创建令牌', exact=True).click()
        dialog = page.get_by_role('dialog', name='创建令牌', exact=True)
        dialog.get_by_role('button', name='保存', exact=True).focus()
        page.keyboard.press('Tab')
        expect(dialog.get_by_role('button', name='关闭弹窗', exact=True)).to_be_focused()
        page.keyboard.press('Shift+Tab')
        expect(dialog.get_by_role('button', name='保存', exact=True)).to_be_focused()
        runner.check(page.evaluate("document.querySelector('.console-modal').contains(document.activeElement)"), 'Focus must remain within the dialog')
    runner.case('modal-tab-boundaries', tab_boundaries)

    def wrap_visibility(page, data):
        page.get_by_role('button', name='创建令牌', exact=True).click()
        dialog = page.get_by_role('dialog', name='创建令牌', exact=True)
        expect(dialog.locator('.selection-panel').first.locator('.selection-option')).to_have_count(50)
        dialog.get_by_role('button', name='关闭弹窗', exact=True).focus()
        page.keyboard.press('Shift+Tab')
        save = dialog.get_by_role('button', name='保存', exact=True)
        expect(save).to_be_focused()
        expect(save).to_be_in_viewport(ratio=1)
        runner.check(save.evaluate('(e) => {const dialog=e.closest("[role=dialog]");return e.getBoundingClientRect().top >= dialog.querySelector("header").getBoundingClientRect().bottom}'),
                     'Keyboard focus must not sit behind the sticky dialog header')
    runner.case('keyboard-wrap-keeps-focused-footer-visible-in-short-dialog', wrap_visibility, width=320, height=450)

    def hint_escape(page, data):
        page.locator('.channel-key-health-link').first.click()
        page.get_by_role('button', name='Key 健康状态 的说明', exact=True).hover()
        expect(page.get_by_role('tooltip')).to_be_visible()
        page.keyboard.press('Escape')
        expect(page.get_by_role('tooltip')).not_to_be_visible()
        expect(page.get_by_role('dialog')).to_be_visible()
    runner.case('escape-dismisses-hint-before-dialog', hint_escape, path='/channels')

    def drawer_menu(page, data):
        page.get_by_role('button', name='打开导航', exact=True).click()
        page.get_by_role('button', name='选择界面主题', exact=True).click()
        expect(page.get_by_role('menu', name='界面主题', exact=True)).to_be_visible()
        page.keyboard.press('Escape')
        expect(page.get_by_role('menu', name='界面主题', exact=True)).not_to_be_visible()
        expect(page.get_by_role('dialog', name='控制台导航', exact=True)).to_be_visible()
        expect(page.get_by_role('button', name='选择界面主题', exact=True)).to_be_focused()
    runner.case('escape-dismisses-drawer-menu-before-drawer', drawer_menu, path='/sites', width=390, height=844)


def search_cases(runner):
    def arrows_enter(page, data):
        page.keyboard.press('Control+k')
        search = page.get_by_label('全局搜索内容', exact=True)
        expect(search).to_be_focused()
        page.keyboard.press('ArrowDown')
        expect(page.locator('.search-result[aria-selected="true"]')).to_contain_text('站点管理')
        page.keyboard.press('Enter')
        expect(page.locator('h1')).to_have_text('站点管理')
        expect(page.locator('#main-content')).to_be_focused()
    runner.case('search-arrows-enter-route-and-main-focus', arrows_enter)

    def composition(page, data):
        page.keyboard.press('Control+k')
        search = page.get_by_label('全局搜索内容', exact=True)
        page.evaluate("document.querySelector('.search-dialog input').dispatchEvent(new KeyboardEvent('keydown', {key:'Enter',bubbles:true,isComposing:true}))")
        expect(page.get_by_role('dialog', name='全局搜索', exact=True)).to_be_visible()
        expect(search).to_be_focused()
        page.keyboard.press('Enter')
        expect(page.locator('h1')).to_have_text('系统概览')
    runner.case('search-ime-does-not-activate-result', composition)

    def reset(page, data):
        page.get_by_role('button', name='全局搜索', exact=True).click()
        page.get_by_label('全局搜索内容', exact=True).fill('model-0010')
        page.keyboard.press('Escape')
        expect(page.get_by_role('button', name='全局搜索', exact=True)).to_be_focused()
        page.get_by_role('button', name='全局搜索', exact=True).click()
        expect(page.get_by_label('全局搜索内容', exact=True)).to_have_value('')
    runner.case('search-escape-clears-query', reset, path='/sites')

    def partial_retry(page, data):
        data.fail_models = True
        page.keyboard.press('Control+k')
        expect(page.locator('.search-resource-error')).to_contain_text('模型')
        page.get_by_label('全局搜索内容', exact=True).fill('合成站点 002')
        expect(page.locator('.search-result[href^="#/sites"]')).to_contain_text('合成站点 002')
        data.fail_models = False
        page.get_by_role('button', name='重试资源', exact=True).click()
        expect(page.locator('.search-resource-error')).not_to_be_visible()
        page.get_by_label('全局搜索内容', exact=True).fill('model-0999')
        expect(page.locator('.search-result')).to_contain_text('model-0999')
        runner.check(sum(c['path'] == '/admin/site-models' for c in data.calls) == 2, 'Failed models must be retried once')
        runner.check(sum(c['path'] == '/admin/channels' for c in data.calls) == 1, 'Successful channels must not be reloaded on model retry')
    runner.case('search-partial-error-retains-sites-and-retries-only-failed-source', partial_retry, path='/sites')

    def truncated(page, data):
        page.keyboard.press('Control+k')
        page.get_by_label('全局搜索内容', exact=True).fill('model-0')
        expect(page.locator('.search-result')).to_have_count(14)
        expect(page.locator('.search-result-summary')).to_contain_text('14')
        expect(page.locator('.search-resource-limit')).to_contain_text('1000')
        expect(page.locator('.search-resource-limit')).to_contain_text('2000')
    runner.case('search-result-and-resource-truncation-visible', truncated, path='/sites')

    def reopen(page, data):
        data.hold_models = True
        with page.expect_request('**/admin/site-models?**'):
            page.keyboard.press('Control+k')
        page.keyboard.press('Escape')
        expect(page.get_by_role('dialog', name='全局搜索', exact=True)).not_to_be_visible()
        data.hold_models = False
        page.keyboard.press('Control+k')
        page.get_by_label('全局搜索内容', exact=True).fill('model-0999')
        expect(page.locator('.search-result')).to_contain_text('model-0999')
        for old in data.pending_models:
            try:
                data.fulfill(old, [])
            except Exception:
                pass  # The browser legitimately aborted the superseded read.
        expect(page.locator('.search-result')).to_contain_text('model-0999')
    runner.case('search-reopen-ignores-superseded-resource-read', reopen, path='/sites')


    def refreshed_inventory(page, data):
        page.keyboard.press('Control+k')
        dialog = page.get_by_role('dialog', name='全局搜索', exact=True)
        dialog.get_by_role('combobox').fill(LONG_NAME)
        expect(dialog.locator('[role="option"][href="#/sites?focus_site_id=120"]')).to_be_visible()
        page.keyboard.press('Escape')
        marker = '搜索已刷新站点唯一'
        data.sites[-1]['name'] = marker
        page.get_by_role('button', name='刷新站点', exact=True).click()
        expect(page.locator('.site-row').first).to_contain_text(marker)
        page.keyboard.press('Control+k')
        dialog.get_by_role('combobox').fill(marker)
        expect(dialog.locator('[role="option"][href="#/sites?focus_site_id=120"]')).to_contain_text(marker)
        runner.check(not any(call['method'] != 'GET' for call in data.calls), 'Search freshness must remain read-only')
    runner.case('reopened-search-reads-inventory-refreshed-by-resource-page', refreshed_inventory, path='/sites')


def list_cases(runner):
    def token_window(page, data):
        expect(page.locator('.token-row')).to_have_count(50)
        pager = page.get_by_role('navigation', name='令牌分页', exact=True)
        started = time.perf_counter()
        pager.get_by_role('button', name='下一页', exact=True).click()
        expect(page.locator('.token-row')).to_have_count(50)
        expect(page.locator('.token-row').first).to_contain_text('合成令牌 101')
        runner.metrics.setdefault('list_samples', {})['tokens'] = {
            'loaded': len(data.tokens), 'rendered_rows': page.locator('.token-row').count(),
            'dom_nodes': page.locator('*').count(), 'next_page_ms': round((time.perf_counter() - started) * 1000, 2)}
        pager.get_by_role('textbox', name='跳转页码', exact=True).fill('4')
        pager.get_by_role('textbox', name='跳转页码', exact=True).press('Enter')
        expect(page.locator('.token-row')).to_have_count(1)
        expect(page.locator('.token-row')).to_contain_text('合成令牌 001')
        pager.get_by_role('textbox', name='跳转页码', exact=True).fill('0')
        pager.get_by_role('textbox', name='跳转页码', exact=True).press('Enter')
        expect(pager.get_by_role('textbox', name='跳转页码', exact=True)).to_have_attribute('aria-invalid', 'true')
        expect(page.locator('.token-row')).to_have_count(1)
        page.on('dialog', lambda dialog: dialog.accept())
        page.locator('.token-row').get_by_role('button', name='删除 合成令牌 001', exact=True).click()
        expect(page.locator('.token-row')).to_have_count(50)
        expect(pager).to_contain_text('3 / 3')
        page.get_by_role('textbox', name='搜索令牌', exact=True).fill('合成令牌 002')
        expect(page.locator('.token-row')).to_have_count(1)
        expect(page.locator('.token-row')).to_contain_text('合成令牌 002')
    runner.case('token-pagination-jump-invalid-delete-last-page-and-filter', token_window)

    def fast_model_jump(page, data):
        page.clock.install(time='2026-11-23T00:00:00Z')
        page.clock.pause_at('2026-11-23T00:00:01Z')
        page.get_by_role('button', name='创建令牌', exact=True).click()
        dialog = page.get_by_role('dialog', name='创建令牌', exact=True)
        dialog.get_by_role('textbox', name='名称', exact=True).fill('快速翻页验证')
        panel = dialog.locator('.selection-panel').first
        current = panel.locator('.selection-list').first
        expect(current.locator('.selection-option')).to_have_count(50)
        first_model = current.locator('code').first.inner_text()
        pager = dialog.get_by_role('navigation', name='模型选择分页', exact=True)
        pager.get_by_role('textbox', name='跳转页码', exact=True).fill('2')
        pager.get_by_role('textbox', name='跳转页码', exact=True).press('Enter')
        expect(pager).to_contain_text('2 / 3')
        expect(current.locator('.selection-option')).to_have_count(50)
        expect(current.locator('code').first).not_to_have_text(first_model)
        second_model = current.locator('code').first.inner_text()
        page.clock.run_for(300)
        expect(pager).to_contain_text('2 / 3')
        expect(current.locator('code').first).to_have_text(second_model)
        expect(panel.get_by_role('textbox', name='搜索可选模型', exact=True)).to_have_value('')
        reads_before_search = [call for call in data.calls if call['path'] == '/admin/routing/models']
        runner.check([call['query'].get('offset') for call in reads_before_search] == [['0'], ['50']],
                     'Settling the initial empty search must not request page one again')
        panel.get_by_role('textbox', name='搜索可选模型', exact=True).fill(first_model)
        page.clock.run_for(300)
        expect(current.locator('code')).to_have_count(1)
        expect(current.locator('code')).to_have_text(first_model)
        reads_after_search = [call for call in data.calls if call['path'] == '/admin/routing/models']
        runner.check(reads_after_search[-1]['query'].get('offset') == ['0']
                     and reads_after_search[-1]['query'].get('search') == [first_model],
                     'A changed search must still debounce and reset its own result page')
        runner.check(not data.token_writes, 'Paging and settling reads must never implicitly save a token')
        runner.metrics.setdefault('list_samples', {})['initial_model_read'] = {
            'settled_ms': 300, 'page_retained': 2,
            'offsets_before_search': [int(call['query']['offset'][0]) for call in reads_before_search],
            'search_reset_offset': 0, 'implicit_writes': len(data.token_writes)}
    runner.case('token-model-fast-jump-survives-initial-search-settlement', fast_model_jump)

    def permissions(page, data):
        page.get_by_role('button', name='创建令牌', exact=True).click()
        dialog = page.get_by_role('dialog', name='创建令牌', exact=True)
        dialog.get_by_role('textbox', name='名称', exact=True).fill('分页选择验证')
        panels = dialog.locator('.selection-panel')
        expect(panels.nth(0).locator('.selection-option')).to_have_count(50)
        expect(panels.nth(1).locator('.selection-option')).to_have_count(50)
        panels.nth(0).get_by_role('checkbox').first.check()
        first_model = panels.nth(0).locator('.selection-option code').first.inner_text()
        model_pager = dialog.get_by_role('navigation', name='模型选择分页', exact=True)
        started = time.perf_counter()
        model_pager.get_by_role('textbox', name='跳转页码', exact=True).fill('2')
        model_pager.get_by_role('textbox', name='跳转页码', exact=True).press('Enter')
        runner.check(not data.token_writes, 'Jump Enter must not submit the token form')
        expect(panels.nth(0).locator('.selection-list').first.locator('.selection-option code').first).not_to_have_text(first_model)
        panels.nth(0).get_by_role('checkbox').first.check()
        second_model = panels.nth(0).locator('.selection-option code').first.inner_text()
        runner.metrics.setdefault('list_samples', {})['permissions'] = {
            'unrelated_site_models': len(data.models), 'configured_channels': len(data.channels),
            'rendered_models': panels.nth(0).locator('.selection-list').first.locator('.selection-option').count(),
            'rendered_channels': panels.nth(1).locator('.selection-option').count(),
            'dom_nodes': dialog.locator('*').count(), 'model_page_jump_ms': round((time.perf_counter() - started) * 1000, 2)}
        panels.nth(0).get_by_role('textbox', name='搜索可选模型', exact=True).fill(first_model)
        expect(panels.nth(0).locator('.selection-list').first.locator('.selection-option code').first).to_have_text(first_model)
        expect(panels.nth(0).get_by_role('checkbox').first).to_be_checked()
        panels.nth(0).get_by_role('textbox', name='搜索可选模型', exact=True).fill('no-matching-synthetic-model')
        expect(panels.nth(0)).to_contain_text('没有匹配')
        panels.nth(1).get_by_role('checkbox').first.check()
        channel_pager = dialog.get_by_role('navigation', name='渠道选择分页', exact=True)
        channel_pager.get_by_role('button', name='下一页', exact=True).click()
        panels.nth(1).get_by_role('checkbox').first.check()
        dialog.get_by_role('button', name='保存', exact=True).click()
        expect(page.get_by_role('dialog', name='令牌已创建', exact=True)).to_be_visible()
        runner.check(len(data.token_writes) == 1, 'One explicit save must produce one write')
        runner.check(set(data.token_writes[0]['allowed_models']) == {first_model, second_model}, 'Selected models must survive paging and non-matching filters')
        runner.check(data.token_writes[0]['allowed_channel_ids'] == [1, 51], 'Selected channel IDs must survive paging')
        model_reads = [call for call in data.calls if call['path'] == '/admin/routing/models']
        runner.check(model_reads and all(call['query'].get('enabled_only') == ['true']
                                        and call['query'].get('limit') == ['50'] for call in model_reads),
                     'Token models must use the bounded enabled canonical inventory')
        runner.check(not any(call['path'] == '/admin/site-models' for call in data.calls),
                     'Token permissions must not load unrelated account-discovery models')
    runner.case('permission-paging-retains-exact-selections-without-implicit-save', permissions)

    def key_window(page, data):
        page.locator('.channel-key-health-link').first.click()
        expect(page.locator('.key-health-card')).to_have_count(50)
        initial_nodes = page.get_by_role('dialog').locator('*').count()
        pager = page.get_by_role('navigation', name='Key 分页', exact=True)
        started = time.perf_counter()
        pager.get_by_role('textbox', name='跳转页码', exact=True).fill('3')
        pager.get_by_role('textbox', name='跳转页码', exact=True).press('Enter')
        expect(page.locator('.key-health-card')).to_have_count(20)
        expect(page.locator('.key-health-card').first).to_contain_text('Key #101')
        runner.metrics.setdefault('list_samples', {})['keys'] = {
            'loaded': 120, 'rendered_first_page': 50, 'last_page_rows': page.locator('.key-health-card').count(),
            'first_page_dialog_nodes': initial_nodes, 'page_jump_ms': round((time.perf_counter() - started) * 1000, 2)}
        expect(page.get_by_role('button', name='检测当前筛选 (20)', exact=True)).to_be_visible()
        runner.check(not any(c['method'] == 'POST' for c in data.calls), 'Paging Key health must never start checks')
    runner.case('key-paging-keeps-full-filter-check-targets-and-no-side-effects', key_window, path='/channels')

    def key_batch(page, data):
        page.on('dialog', lambda dialog: dialog.accept())
        page.locator('.channel-key-health-link').first.click()
        pager = page.get_by_role('navigation', name='Key 分页', exact=True)
        pager.get_by_role('textbox', name='跳转页码', exact=True).fill('3')
        pager.get_by_role('textbox', name='跳转页码', exact=True).press('Enter')
        expect(page.locator('.key-health-card').first).to_contain_text('Key #101')
        page.get_by_role('button', name='检测当前筛选 (20)', exact=True).click()
        expect(page.get_by_text('已检查 20 个', exact=False)).to_be_visible()
        runner.check([item['key_id'] for item in data.key_checks] == list(range(1, 21)),
                     'A batch launched on page 3 must still check the first 20 full-filter Keys')
        runner.check([item['key_index'] for item in data.key_checks] == list(range(20)),
                     'Pagination must not alter the original Key indices sent for detection')
    runner.case('synthetic-key-batch-on-page-three-retains-full-filter-ids', key_batch, path='/channels')

    def selection(page, data):
        page.get_by_role('checkbox', name='选择当前页站点', exact=True).check()
        expect(page.locator('.batch-selection-summary')).to_contain_text('50')
        page.get_by_role('navigation', name='站点分页', exact=True).get_by_role('button', name='下一页', exact=True).click()
        page.get_by_role('checkbox', name='选择当前页站点', exact=True).check()
        expect(page.locator('.batch-selection-summary')).to_contain_text('100')
        page.get_by_role('textbox', name='搜索站点', exact=True).fill('no-matching-site')
        expect(page.locator('.batch-selection-summary')).to_contain_text('筛选外 100 项')
        page.get_by_role('button', name='清空选择', exact=True).click()
        expect(page.locator('.batch-toolbar')).not_to_be_visible()
        runner.check(not any(c['method'] != 'GET' for c in data.calls), 'Clear selection must not mutate sites')
    runner.case('site-selection-explains-page-and-outside-filter-scope', selection, path='/sites')

    def account_selection(page, data):
        page.get_by_role('checkbox', name='选择当前页账号', exact=True).check()
        page.get_by_role('navigation', name='账号分页', exact=True).get_by_role('button', name='下一页', exact=True).click()
        page.get_by_role('checkbox', name='选择当前页账号', exact=True).check()
        page.get_by_role('textbox', name='搜索账号', exact=True).fill('no-matching-account')
        expect(page.locator('.batch-selection-summary')).to_contain_text('筛选外 100 项')
        page.get_by_role('button', name='清空选择', exact=True).click()
        expect(page.locator('.batch-toolbar')).not_to_be_visible()
    runner.case('account-selection-explains-page-and-outside-filter-scope', account_selection, path='/accounts')


    def positive_filter(page, data):
        for path, row, pager_label, query_label, query in [
            ('/sites', '.site-row', '站点分页', '搜索站点', '合成站点 001'),
            ('/accounts', '.account-records .record-row', '账号分页', '搜索账号', '合成账号 001-0'),
        ]:
            page.evaluate('path => { location.hash = path }', path)
            expect(page.locator(row).first).to_be_visible()
            pager = page.get_by_role('navigation', name=pager_label, exact=True)
            last_page = 3 if path == '/sites' else 5
            pager.get_by_role('textbox', name='跳转页码', exact=True).fill(str(last_page))
            pager.get_by_role('textbox', name='跳转页码', exact=True).press('Enter')
            expect(pager).to_contain_text(f'{last_page} / {last_page}')
            page.get_by_role('textbox', name=query_label, exact=True).fill(query)
            expect(page.locator(row)).to_have_count(1)
            expect(page.locator(row)).to_contain_text(query)
            expect(pager).to_contain_text('1 / 1')
        runner.check(not any(call['method'] != 'GET' for call in data.calls), 'Filtering from a later page must not mutate resources')
    runner.case('site-and-account-positive-filter-from-last-page', positive_filter, path='/sites')


def detail_cases(runner):
    def inspect_details(page, data):
        for path, title, row in [('/sites', '站点详情', '.site-row'), ('/channels', '渠道详情', '.channel-row'), ('/tokens', '令牌详情', '.token-row')]:
            page.evaluate('path => { location.hash = path }', path)
            expect(page.locator(row).first).to_be_visible()
            opener = page.locator(row).first.locator('.resource-details-trigger')
            expect(opener).to_contain_text('详情')
            opener.click()
            dialog = page.get_by_role('dialog', name=title, exact=True)
            expect(dialog).to_be_visible()
            expect(dialog.locator('.resource-details')).to_contain_text(LONG_NAME)
            if path != '/tokens':
                expect(dialog.get_by_role('link', name=LONG_URL, exact=True)).to_be_visible()
            else:
                expect(dialog.locator('.resource-details')).to_contain_text('opaque-hint-151')
                expect(dialog.locator('.resource-details')).to_contain_text('9.50')
                expect(dialog.locator('.resource-details')).to_contain_text('排除')
                runner.check(not any('reveal' in c['path'] for c in data.calls), 'Read-only details must not recover the full token')
            runner.check(page.evaluate("document.documentElement.scrollWidth <= innerWidth + 1"), 'Page overflowed viewport')
            runner.check(dialog.evaluate('(e) => e.scrollWidth <= e.clientWidth + 1'), 'Dialog has horizontal overflow')
            runner.check(dialog.locator('header').first.evaluate('(e) => e.scrollWidth <= e.clientWidth + 1'), 'Dialog title or close is clipped')
            for fact in dialog.locator('.resource-details dd').all():
                runner.check(fact.evaluate('(e) => e.scrollWidth <= e.clientWidth + 1'), 'A fact is horizontally clipped')
            viewport = page.viewport_size
            page.screenshot(path=str(runner.output / f'{viewport["width"]}-{viewport["height"]}-{page.evaluate("document.documentElement.dataset.theme")}-{path[1:]}.jpg'),
                            type='jpeg', quality=82, animations='disabled')
            dialog.get_by_role('button', name='关闭弹窗', exact=True).click()
            expect(opener).to_be_focused()
            if viewport['width'] <= 390:
                expect(page.locator(row).first.get_by_role('button', name='编辑 ' + LONG_NAME, exact=True)).to_be_visible()
                expect(page.locator(row).first.get_by_role('button', name='删除 ' + LONG_NAME, exact=True)).to_be_visible()
        runner.check(not any(c['method'] != 'GET' for c in data.calls), 'Viewing details must remain read-only')

    for width, height, theme in [(320, 740, 'light'), (320, 740, 'dark'), (390, 844, 'light'), (390, 844, 'dark'),
                                  (1440, 900, 'light'), (1440, 900, 'dark'), (720, 450, 'light'), (720, 450, 'dark')]:
        runner.case(f'details-{width}-{height}-{theme}', inspect_details, path='/sites', width=width, height=height, theme=theme)

    def settings_tabs(page, data):
        tabs = page.get_by_role('tablist', name='系统设置分类', exact=True)
        tabs.get_by_role('tab', name='运行设置', exact=True).focus()
        page.keyboard.press('ArrowRight')
        expect(tabs.get_by_role('tab', name='通知设置', exact=True)).to_be_focused()
        expect(tabs.get_by_role('tab', name='通知设置', exact=True)).to_have_attribute('aria-selected', 'true')
        expect(page.get_by_role('tabpanel', name='通知设置', exact=True)).to_be_visible()
        page.keyboard.press('Home')
        expect(tabs.get_by_role('tab', name='运行设置', exact=True)).to_be_focused()
        expect(page.get_by_role('tabpanel', name='运行设置', exact=True)).to_be_visible()
        page.keyboard.press('ArrowLeft')
        expect(tabs.get_by_role('tab', name='系统访问', exact=True)).to_be_focused()
        page.keyboard.press('Home')
        page.keyboard.press('End')
        expect(tabs.get_by_role('tab', name='系统访问', exact=True)).to_be_focused()
        runner.check(tabs.locator('[tabindex="0"]').count() == 1, 'Exactly one settings tab belongs to the Tab sequence')
    runner.case('settings-classification-keyboard-and-panel-name', settings_tabs, path='/system')

    def long_title(page, data):
        page.locator('.channel-key-health-link').first.click()
        dialog = page.get_by_role('dialog')
        expect(dialog).to_be_visible()
        runner.check(dialog.locator('header').first.evaluate('(e) => e.scrollWidth <= e.clientWidth + 1'), 'Long title/close must fit within dialog')
        runner.check(dialog.evaluate('(e) => e.scrollWidth <= e.clientWidth + 1'), 'Key dialog must not horizontally scroll')
        page.keyboard.press('Escape')
    runner.case('long-key-dialog-title-and-pagination-fit-short-mobile', long_title, path='/channels', width=320, height=450)

    def short_search(page, data):
        data.fail_models = data.fail_channels = True
        page.keyboard.press('Control+k')
        dialog = page.get_by_role('dialog', name='全局搜索', exact=True)
        expect(dialog).to_be_visible()
        expect(dialog.locator('.search-resource-error')).to_contain_text('渠道')
        expect(dialog.locator('.search-resource-error')).to_contain_text('模型')
        dialog.get_by_role('combobox').fill('合成')
        runner.check(dialog.evaluate('(e) => e.getBoundingClientRect().bottom <= innerHeight'), 'Search must fit a short viewport')
        runner.check(dialog.evaluate('(e) => e.scrollWidth <= e.clientWidth + 1'), 'Search must not scroll horizontally')
        expect(dialog.get_by_role('button', name='关闭全局搜索', exact=True)).to_be_in_viewport()
        page.keyboard.press('Escape')
    runner.case('short-mobile-search-failures-remain-readable-and-closeable', short_search, path='/sites', width=320, height=450)

    def safe_details(page, data):
        data.tokens[-1].update(token='synthetic-full-value-must-stay-private', token_hint='')
        page.reload(wait_until='networkidle')
        page.locator('.resource-details-trigger').first.click()
        dialog = page.get_by_role('dialog', name='令牌详情', exact=True)
        expect(dialog).to_be_visible()
        expect(dialog).not_to_contain_text('synthetic-full-value-must-stay-private')
        expect(dialog).to_contain_text('未提供提示')
        runner.check(not any('reveal' in c['path'] for c in data.calls), 'Missing hint must not reveal or fall back to a full token')
        page.keyboard.press('Escape')
        data.sites[-1].update(base_url='https://synthetic-user:synthetic-pass@site.example.invalid/path',
                              proxy_url='https://synthetic-proxy-user:synthetic-proxy-pass@proxy.example.invalid')
        page.evaluate("location.hash = '/sites'")
        page.reload(wait_until='networkidle')
        page.locator('.resource-details-trigger').first.click()
        dialog = page.get_by_role('dialog', name='站点详情', exact=True)
        expect(dialog).to_be_visible()
        runner.check(urlparse(data.sites[-1]['base_url']).password not in dialog.inner_text(), 'URL user info must stay hidden')
        runner.check(urlparse(data.sites[-1]['proxy_url']).password not in dialog.inner_text(), 'Proxy credentials must stay hidden')
        expect(dialog.get_by_role('link', name='https://site.example.invalid/path', exact=True)).to_be_visible()
        expect(dialog).to_contain_text('自定义代理已配置')
        runner.check(not any(c['method'] != 'GET' for c in data.calls), 'Safe details remain read-only')
    runner.case('details-hide-url-credentials-and-never-fallback-to-full-token', safe_details)


    for name, value in [
        ('non-http', 'ftp://synthetic-user:synthetic-detail-password@site.example.invalid/path'),
        ('malformed', 'https://synthetic-user:synthetic-detail-password@/path'),
    ]:
        def protected_url(page, data, value=value):
            data.sites[-1]['base_url'] = value
            page.reload(wait_until='networkidle')
            page.locator('.resource-details-trigger').first.click()
            dialog = page.get_by_role('dialog', name='站点详情', exact=True)
            expect(dialog).to_be_visible()
            password = value.split('://', 1)[1].rsplit('@', 1)[0].partition(':')[2]
            expect(dialog).not_to_contain_text(password)
            expect(dialog).not_to_contain_text('synthetic-user:')
            expect(dialog).to_contain_text('地址中的登录凭证已隐藏')
            runner.check(not any(call['method'] != 'GET' for call in data.calls), 'Malformed or non-HTTP details must remain read-only')
        runner.case(f'details-redact-{name}-url-userinfo', protected_url, path='/sites')

    def absent_cooldown(page, data):
        data.channels[0].pop('cooldown_until', None)
        page.reload(wait_until='networkidle')
        page.locator('.resource-details-trigger').first.click()
        dialog = page.get_by_role('dialog', name='渠道详情', exact=True)
        expect(dialog).to_be_visible()
        fact = dialog.locator('.resource-detail-fact').filter(has=page.get_by_text('渠道冷却结束', exact=True))
        expect(fact.locator('dd')).to_have_text('当前没有渠道冷却')
    runner.case('channel-details-distinguish-absent-cooldown-from-unknown', absent_cooldown, path='/channels')


def metric_cases(runner, root):
    assets = root / 'web/console/assets'
    paths = [p for p in assets.iterdir() if p.is_file()]
    route_prefixes = ('DashboardPage-', 'ChannelsPage-', 'ModelRoutesPage-', 'BrowserImportPage-', 'LogsPage-', 'StatsPage-', 'HealthPage-',
                      'ModelTestPage-', 'SitesPage-', 'AccountsPageV2-', 'CheckinsPage-',
                      'AnnouncementsPage-', 'TokensPage-', 'ConnectPage-', 'SystemSettingsPageV2-')
    runner.metrics['build'] = {
        'asset_files': len(paths),
        'all_asset_gzip_bytes': sum(len(gzip.compress(p.read_bytes(), mtime=0)) for p in paths),
        'route_chunks': {p.name.split('-')[0]: {'decoded_bytes': p.stat().st_size,
                        'gzip_bytes': len(gzip.compress(p.read_bytes(), mtime=0))}
                        for p in paths if p.suffix == '.js' and p.name.startswith(route_prefixes)},
        'non_prefetched_detail': {p.name: {'decoded_bytes': p.stat().st_size,
                                'gzip_bytes': len(gzip.compress(p.read_bytes(), mtime=0))}
                                for p in paths if p.name.startswith('AnnouncementDetail-')},
    }

    def resources(page, data):
        page.wait_for_function("""() => performance.getEntriesByType('resource')
          .some(e => e.name.includes('/web/console/assets/SitesPage-') && e.name.endsWith('.js'))""")
        page.wait_for_load_state('networkidle')
        runner.metrics['session'] = page.evaluate("""() => {
          const entries = performance.getEntriesByType('resource').filter(e => e.name.includes('/web/console/'));
          return {resources: entries.length, encoded_bytes: entries.reduce((s,e) => s+e.encodedBodySize,0),
            decoded_bytes: entries.reduce((s,e) => s+e.decodedBodySize,0), dom_nodes: document.querySelectorAll('*').length,
            page_chunks: entries.filter(e => e.name.endsWith('.js')).map(e => e.name.split('/').pop())};
        }""")
        session = runner.metrics['session']
        runner.check(session['encoded_bytes'] <= 400000, 'Console session exceeds 400000 encoded bytes')
        runner.check(session['resources'] <= 45, 'Console session exceeds the existing 45-resource budget')
        runner.check(not any(name.startswith(('ChannelsPage-', 'LogsPage-', 'StatsPage-', 'SystemSettingsPageV2-')) for name in session['page_chunks']),
                     'Initial Sites view eagerly loads unrelated page code')
        page.get_by_role('link', name='账号管理', exact=True).hover()
        page.wait_for_function("""() => performance.getEntriesByType('resource')
          .some(e => e.name.includes('/web/console/assets/AccountsPageV2-') && e.name.endsWith('.js'))""")
        runner.metrics['intent_preload'] = {'trigger': 'navigation_hover', 'page': '/accounts', 'loaded': True}
        for name, sizes in {**runner.metrics['build']['route_chunks'], **runner.metrics['build']['non_prefetched_detail']}.items():
            runner.check(sizes['gzip_bytes'] <= 150000 and sizes['decoded_bytes'] <= 450000,
                         f'Route {name} exceeds gzip/decoded budget')
    runner.case('prefetch-resource-measurement', resources, path='/sites')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--group', choices=['focus', 'search', 'lists', 'details', 'metrics', 'all'], default='all')
    parser.add_argument('--root', type=Path, default=ROOT)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--case', action='append', default=[])
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    root = args.root.resolve()
    server = ThreadingHTTPServer(('127.0.0.1', 0), partial(StaticHandler, root=root))
    server.daemon_threads = True
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    base = f'http://127.0.0.1:{server.server_port}'
    runner = None
    try:
        opener = build_opener(ProxyHandler({}))
        with opener.open(base + '/web/console/', timeout=5) as response:
            if response.status != 200:
                raise RuntimeError('Owned server did not become ready')
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True, args=['--no-proxy-server', '--disable-background-networking'])
            try:
                runner = Runner(browser, base, args.output, args.case)
                if args.group in ('focus', 'all'):
                    focus_cases(runner)
                if args.group in ('search', 'all'):
                    search_cases(runner)
                if args.group in ('lists', 'all'):
                    list_cases(runner)
                if args.group in ('details', 'all'):
                    detail_cases(runner)
                if args.group in ('metrics', 'all'):
                    metric_cases(runner, root)
            finally:
                browser.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    result = {'group': args.group, 'checks': runner.results if runner else [],
              'metrics': runner.metrics if runner else {}, 'unexpected_events': runner.events if runner else [],
              'server_closed': not thread.is_alive()}
    (args.output / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    passed = sum(item['pass'] for item in result['checks'])
    print(json.dumps({'passed': passed, 'total': len(result['checks']), 'events': len(result['unexpected_events']),
                      'server_closed': result['server_closed'], 'result': str(args.output / 'result.json')}))
    for item in result['checks']:
        if not item['pass']:
            print(item['name'] + ': ' + item['error'].splitlines()[0])
    return 0 if result['checks'] and passed == len(result['checks']) and not result['unexpected_events'] and result['server_closed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
