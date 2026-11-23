"""Exercise the synthetic browser-import bridge console flow.

The script serves the real built React app from an owned local HTTP server,
answers the browser-import and shared config-preview admin endpoints with
literal synthetic DTOs, blocks every external origin, and keeps the browser
read-only. Screenshots are visible-viewport JPEGs, never full-page PNGs.

The downloaded page helper is executed for real on a second controlled
origin, so finite localStorage reads, wrong-origin and iframe refusal, the
export download, close cleanup, and the zero-network guarantee are observed
from behaviour rather than from the script source text.

No live gateway, database, login, upstream, Vite dev server, or production
process is used. The owned static server is closed even after a failure.
"""
import argparse
import copy
import gzip
import json
import re
import time
from functools import partial
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import parse_qs, urlencode, urlparse, urlsplit

from playwright.sync_api import expect, sync_playwright

import docs_screenshots as fixtures
from console_visual_regression import SETTINGS
from ui_accessibility_smoke import ROOT, Runner, StaticHandler, SyntheticData


# Sentinels that stand in for real credentials. They are fed to the console
# only inside request bodies; the harness fails the run if any of them reach a
# URL, localStorage, sessionStorage, or rendered text.
SECRET_API_KEY = 'synthetic-secret-apikey-0001'
SECRET_ACCESS_TOKEN = 'synthetic-secret-accesstoken-0002'
SECRET_COOKIE = 'synthetic-secret-cookie-0003'
SECRET_REFRESH = 'synthetic-secret-refresh-0004'
SECRETS = (SECRET_API_KEY, SECRET_ACCESS_TOKEN, SECRET_COOKIE, SECRET_REFRESH)

SOURCE_ORIGIN = 'https://source.example'
SOURCE_BASE_URL = 'https://source.example'
SOURCE_SITE_NAME = 'Source'
SOURCE_PLATFORM = 'openai-compatible'
SOURCE_ACCOUNT_LABEL = 'demo'

ROUTE_PATH = '/browser-import'
NAV_LABEL = '浏览器导入'
PREVIEW_REGION = '配置差异预览'


def pfilter(value):
    return re.compile(value)


def entry(index, source_origin, base_url, site_name, platform, account_label,
          credential_type, credential, *, user_id='', refresh_configured=False,
          expires_at=None):
    item = {
        'source_origin': source_origin,
        'base_url': base_url,
        'site_name': site_name,
        'platform': platform,
        'account_label': account_label,
        'credential_type': credential_type,
        'credential': {credential_type: credential},
    }
    if user_id:
        item['credential']['user_id'] = int(user_id)
    if refresh_configured:
        item['credential']['refresh_token'] = SECRET_REFRESH
    if expires_at is not None:
        item['credential']['expires_at'] = expires_at
    return {'index': index, 'entry': item}


def build_pivotflow_doc(count=6, captured_at=1795392000000):
    """Return the lightweight bridge document the page hands to /inspect."""
    entries = []
    entries.append(entry(
        0, SOURCE_ORIGIN, SOURCE_BASE_URL, SOURCE_SITE_NAME, SOURCE_PLATFORM,
        SOURCE_ACCOUNT_LABEL, 'api_key', SECRET_API_KEY, user_id='88001',
    ))
    entries.append(entry(
        1, SOURCE_ORIGIN, SOURCE_BASE_URL, SOURCE_SITE_NAME, SOURCE_PLATFORM,
        SOURCE_ACCOUNT_LABEL, 'api_key', SECRET_API_KEY, user_id='88001',
    ))
    entries.append(entry(
        2, 'https://plain.example', 'https://plain.example', 'Plain',
        'unsupported-provider', 'plain-account', 'api_key', SECRET_API_KEY,
    ))
    entries.append(entry(
        3, 'https://token.example', 'https://token.example', 'Token Site',
        'sub2api', 'token-account', 'access_token', SECRET_ACCESS_TOKEN,
        user_id='88003', refresh_configured=True,
    ))
    entries.append(entry(
        4, 'https://cookie.example', 'https://cookie.example', 'Cookie Site',
        'new-api-family', 'cookie-account', 'cookie', SECRET_COOKIE, user_id='88004',
    ))
    while len(entries) < count:
        number = len(entries)
        entries.append(entry(
            number, 'https://multi.example', 'https://multi.example',
            f'Multi {number:02d}', 'openai-compatible', f'user-{number:02d}',
            'api_key', SECRET_API_KEY, user_id=f'88{number:03d}',
        ))
    return {
        'format': 'pivotflow-browser-bridge',
        'version': '1.0',
        'captured_at': captured_at,
        'entries': [item['entry'] for item in entries[:count]],
    }


def build_all_api_hub_doc(version='4.0'):
    accounts = [
        {
            'site_name': 'Hub Source',
            'site_url': 'https://hub.example',
            'site_type': 'new-api',
            'account_info': {'username': 'hub-user', 'id': 701, 'access_token': SECRET_ACCESS_TOKEN},
            'authType': 'access_token',
        },
        {
            'site_name': 'Hub Cookie',
            'site_url': 'https://hub-cookie.example',
            'site_type': 'veloera',
            'account_info': {'username': 'hub-cookie-user', 'id': 702},
            'cookieAuth': {'sessionCookie': SECRET_COOKIE},
            'authType': 'cookie',
        },
    ]
    document = {
        'version': version,
        'exported_at': 1795392000000,
        'accounts': {'accounts': accounts},
        'unknown_section': {'should': 'be ignored'},
    }
    return document


def inspect_entry(index, *, source_origin=SOURCE_ORIGIN, base_url=SOURCE_BASE_URL,
                  site_name=SOURCE_SITE_NAME, platform=SOURCE_PLATFORM,
                  account_label=SOURCE_ACCOUNT_LABEL, credential_type='api_key',
                  refresh_configured=False, eligible=True, problem='',
                  duplicate_of=None, target_site_id=0, candidates=None):
    item = {
        'index': index,
        'source_origin': source_origin,
        'base_url': base_url,
        'site_name': site_name,
        'platform': platform,
        'account_label': account_label,
        'credential_type': credential_type,
        'refresh_configured': refresh_configured,
        'eligible': eligible,
        'target_site_id': target_site_id,
        'candidates': candidates or [],
    }
    if problem:
        item['problem'] = problem
    if duplicate_of is not None:
        item['duplicate_of'] = duplicate_of
    return item


def build_inspect(count=6, fmt='pivotflow-browser-bridge', ignored=None):
    """Literal /inspect DTO kept consistent with the synthetic documents."""
    if fmt == 'all-api-hub':
        return {'version': 1, 'format': fmt, 'captured_at': 0,
                'ignored_sections': ['unknown_section'], 'entries': [
                    inspect_entry(0, source_origin='https://hub.example', base_url='https://hub.example',
                                  site_name='Hub Source', platform='new-api-family', account_label='hub-user', credential_type='access_token'),
                    inspect_entry(1, source_origin='https://hub-cookie.example', base_url='https://hub-cookie.example',
                                  site_name='Hub Cookie', platform='veloera', account_label='hub-cookie-user', credential_type='cookie'),
                ]}
    entries = [
        inspect_entry(
            0, target_site_id=1,
            candidates=[
                {'id': 101, 'label': SOURCE_ACCOUNT_LABEL, 'match': 'credential'},
                {'id': 102, 'label': 'demo-backup', 'match': 'user_id'},
            ],
        ),
        inspect_entry(1, duplicate_of=0, target_site_id=1, eligible=False, problem='duplicate_identity',
                      candidates=[{'id': 101, 'label': SOURCE_ACCOUNT_LABEL, 'match': 'credential'}]),
        inspect_entry(
            2, source_origin='https://plain.example', base_url='https://plain.example',
            site_name='Plain', platform='unsupported-provider',
            account_label='plain-account', eligible=False,
            problem='unsupported_platform',
        ),
        inspect_entry(
            3, source_origin='https://token.example', base_url='https://token.example',
            site_name='Token Site', platform='sub2api', account_label='token-account',
            credential_type='access_token', refresh_configured=True,
            target_site_id=3, candidates=[],
        ),
        inspect_entry(
            4, source_origin='https://cookie.example', base_url='https://cookie.example',
            site_name='Cookie Site', platform='new-api-family',
            account_label='cookie-account', credential_type='cookie',
            target_site_id=4, candidates=[],
        ),
    ]
    while len(entries) < count:
        number = len(entries)
        entries.append(inspect_entry(
            number, source_origin='https://multi.example', base_url='https://multi.example',
            site_name=f'Multi {number:02d}', account_label=f'user-{number:02d}',
            candidates=[
                {'id': 200 + number, 'label': f'multi-{number:02d}-a', 'match': 'same_site'},
                {'id': 300 + number, 'label': f'multi-{number:02d}-b', 'match': 'label'},
            ],
        ))
    return {
        'version': 1,
        'format': fmt,
        'captured_at': 1795392000000,
        'ignored_sections': list(ignored or []),
        'entries': entries[:count],
    }


PREVIEW_ITEM_SITE = {
    'key': 'sites[1]',
    'entity': 'site',
    'label': SOURCE_SITE_NAME,
    'action': 'update',
    'fields': ['name', 'base_url', 'platform'],
    'preserved_fields': ['enabled', 'use_system_proxy', 'timezone'],
    'message': '',
}
PREVIEW_ITEM_ACCOUNT = {
    'key': 'accounts[101]',
    'entity': 'account',
    'label': SOURCE_ACCOUNT_LABEL,
    'action': 'update',
    'fields': ['credential_type', 'credentials'],
    'preserved_fields': ['enabled', 'auto_checkin', 'auto_refresh'],
    'message': '',
}


def build_preview(preview_id, *, mode='verify', status='ready', action='update',
                  error='', can_apply=True, verified=True, verification_status='verified',
                  scope='models', model_count=12, identity_known=False,
                  target_account_id=101, result=None):
    items = [copy.deepcopy(PREVIEW_ITEM_SITE), copy.deepcopy(PREVIEW_ITEM_ACCOUNT)]
    items[1]['action'] = action
    payload = {
        'id': preview_id,
        'kind': 'browser_import',
        'expires_at': int(time.time() * 1000) + 600_000,
        'can_apply': can_apply and status == 'ready',
        'items': items,
        'summary': None,
        'status': status,
        'browser_import': {
            'format': 'pivotflow-browser-bridge',
            'source_origin': SOURCE_ORIGIN,
            'base_url': SOURCE_BASE_URL,
            'site_name': SOURCE_SITE_NAME,
            'account_label': SOURCE_ACCOUNT_LABEL,
            'credential_type': 'api_key',
            'captured_at': 1795392000000,
            'target_site_id': 1,
            'target_account_id': target_account_id,
            'target_site_name': SOURCE_SITE_NAME,
            'target_account_label': SOURCE_ACCOUNT_LABEL,
            'mode': mode,
            'verification': {
                'status': verification_status,
                'scope': scope,
                'checked_at': 1795392000000,
                'identity_known': identity_known,
                'user_id': 88001,
                'username': SOURCE_ACCOUNT_LABEL,
                'model_count': model_count,
                'model_count_known': True,
                'routing_key_available': verified,
            },
        },
    }
    if error:
        payload['error'] = error
    if result is not None:
        payload['result'] = result
    return payload


def build_apply_result(preview_id, action='update', verified=True):
    return {
        'preview_id': preview_id,
        'status': 'applied',
        'items': [copy.deepcopy(PREVIEW_ITEM_SITE), copy.deepcopy(PREVIEW_ITEM_ACCOUNT)],
        'browser_import': {'site_id': 1, 'account_id': 101, 'action': action, 'verified': verified},
    }


class BrowserImportData(SyntheticData):
    """Synthetic admin API for the browser-import page and shared previews."""

    def __init__(self):
        super().__init__()
        self.calls = []
        self.inspect_bodies = []
        self.preview_bodies = []
        self.apply_bodies = []
        self.inspect_dto = build_inspect()
        self.inspect_count = 6
        self.inspect_ignored = []
        self.preview_dto = None
        self.preview_status = 'ready'
        self.preview_error = ''
        self.preview_mode = 'verify'
        self.hold_preview = False
        self.pending_previews = []
        self.drop_apply_response_once = False
        self.dropped_apply_status = 'applied'
        self.fail_preview = False
        self.browser_required_once = False
        self.apply_calls = 0
        self.task_snapshots = {}
        self.sites = [
            {
                'id': 1, 'name': SOURCE_SITE_NAME, 'platform': SOURCE_PLATFORM,
                'base_url': SOURCE_BASE_URL, 'enabled': True, 'timezone': 'Asia/Shanghai',
                'tags_json': '[]', 'use_system_proxy': True, 'last_probe_status': 'healthy',
                'created_at': fixtures.NOW - 86_400_000 * 30, 'updated_at': fixtures.NOW,
            },
            {
                'id': 3, 'name': 'Token Site', 'platform': 'sub2api',
                'base_url': 'https://token.example', 'enabled': True, 'timezone': 'Asia/Shanghai',
                'tags_json': '[]', 'use_system_proxy': True, 'last_probe_status': 'healthy',
                'created_at': fixtures.NOW - 86_400_000 * 12, 'updated_at': fixtures.NOW,
            },
        ]
        self.accounts = [
            {
                'id': 101, 'site_id': 1, 'label': SOURCE_ACCOUNT_LABEL, 'credential_type': 'api_key',
                'credential_configured': True, 'enabled': True, 'auto_checkin': False,
                'auto_refresh': True, 'status': 'healthy', 'balance': 41.20,
                'balance_currency': 'USD', 'balance_updated_at': fixtures.NOW,
                'last_refresh_at': fixtures.NOW, 'last_refresh_status': 'success',
                'consecutive_failures': 0, 'last_checkin_status': 'unsupported',
                'created_at': fixtures.NOW - 86_400_000 * 30, 'updated_at': fixtures.NOW,
            },
            {
                'id': 102, 'site_id': 1, 'label': 'demo-backup', 'credential_type': 'api_key',
                'credential_configured': True, 'enabled': True, 'auto_checkin': False,
                'auto_refresh': False, 'status': 'unknown', 'balance': 0,
                'balance_currency': 'USD', 'balance_updated_at': 0,
                'last_refresh_status': 'unknown', 'consecutive_failures': 0,
                'last_checkin_status': 'unsupported',
                'created_at': fixtures.NOW - 86_400_000 * 4, 'updated_at': fixtures.NOW,
            },
        ]

    def fulfill(self, route, data, count=None):
        route.fulfill(
            status=200,
            content_type='application/json',
            body=json.dumps(fixtures.envelope(data, count), ensure_ascii=False),
        )

    def fulfill_raw(self, route, payload, status=200):
        route.fulfill(
            status=status,
            content_type='application/json',
            body=json.dumps(payload, ensure_ascii=False),
        )

    def route(self, route):
        parsed = urlsplit(route.request.url)
        path, query = parsed.path, parse_qs(parsed.query)
        self.calls.append({
            'path': path,
            'method': route.request.method,
            'query': query,
            'url': route.request.url,
        })
        if path == '/dashboard/session':
            self.fulfill(route, {'role': 'admin'})
            return
        if path == '/admin/browser-import/inspect':
            body = route.request.post_data_json or {}
            self.inspect_bodies.append(body)
            document = body.get('document')
            if isinstance(document, str):
                try:
                    parsed_document = json.loads(document)
                    fmt = parsed_document.get('format', '')
                    if not fmt and parsed_document.get('version') in ('2.0', '3.0', '4.0'):
                        fmt = 'all-api-hub'
                except ValueError:
                    fmt = ''
            elif isinstance(document, dict):
                fmt = document.get('format', '')
                if not fmt and document.get('version') in ('2.0', '3.0', '4.0'):
                    fmt = 'all-api-hub'
            else:
                fmt = ''
            dto = build_inspect(self.inspect_count, fmt or 'pivotflow-browser-bridge', self.inspect_ignored)
            self.inspect_dto = dto
            self.fulfill(route, dto)
            return
        if path == '/admin/browser-import/preview':
            body = route.request.post_data_json or {}
            self.preview_bodies.append(body)
            if self.fail_preview:
                self.fulfill_raw(route, {'success': False, 'error': 'synthetic_preview_unavailable'}, 503)
                return
            if self.hold_preview:
                self.pending_previews.append(route)
                return
            preview_id = f'bpv-{len(self.preview_bodies):04d}'
            error = self.preview_error
            if self.browser_required_once:
                self.browser_required_once = False
                error = 'browser_required'
            if error:
                self.fulfill_raw(route, {'success': False, 'error': error}, 400)
                return
            dto = build_preview(
                preview_id,
                mode=self.preview_mode,
                status='failed' if (error and error != 'browser_required') else 'ready',
                error=error,
                can_apply=not error,
                verified=self.preview_mode == 'verify' and not error,
                verification_status='verified' if self.preview_mode == 'verify' and not error else 'unverified',
                scope='models' if self.preview_mode == 'verify' else 'none',
            )
            self.preview_dto = dto
            self.fulfill(route, dto)
            return
        if path.startswith('/admin/config-previews'):
            remainder = path[len('/admin/config-previews'):].strip('/')
            preview_id = remainder.split('/')[0] if remainder else ''
            if route.request.method == 'GET':
                if self.preview_dto is None:
                    self.preview_dto = build_preview('bpv-restored', status=self.preview_status)
                dto = dict(self.preview_dto)
                dto['id'] = preview_id or dto['id']
                dto['status'] = self.preview_status
                dto['can_apply'] = self.preview_status == 'ready'
                self.fulfill(route, dto)
                return
            if route.request.method == 'POST' and path.endswith('/apply'):
                self.apply_calls += 1
                self.apply_bodies.append({'id': preview_id, 'body': route.request.post_data_json})
                if self.drop_apply_response_once:
                    self.drop_apply_response_once = False
                    self.preview_status = self.dropped_apply_status
                    route.abort('aborted')
                    return
                self.preview_status = 'applied'
                self.fulfill(route, build_apply_result(preview_id or 'bpv-restored'))
                return
            if route.request.method == 'DELETE':
                self.fulfill(route, {'deleted': True})
                return
        if path == '/admin/site-inventory':
            self.fulfill(route, {'sites': self.sites, 'accounts': self.accounts})
            return
        if path == '/admin/site-tasks':
            self.fulfill(route, [{**task, 'status': 'queued', 'error': ''} for task in self.task_snapshots.values()])
            return
        if path.startswith('/admin/site-tasks/') and route.request.method == 'GET':
            task_id = path.rsplit('/', 1)[-1]
            if task_id in self.task_snapshots:
                self.fulfill(route, self.task_snapshots[task_id])
                return
        if path == '/admin/checkin-attempts':
            self.fulfill(route, [])
            return
        if path == '/admin/sites':
            self.fulfill(route, self.sites, len(self.sites))
            return
        if path == '/admin/channels':
            self.fulfill(route, [], 0)
            return
        if path == '/admin/settings':
            self.fulfill(route, SETTINGS)
            return
        fixtures.mock_admin(route)


HELPER_SOURCE_PAGE = """<!doctype html>
<html lang="zh"><head><meta charset="utf-8"><title>Source Page</title></head>
<body><h1>Source Page</h1>
<p id="note">Controlled synthetic origin for the page helper.</p>
<button id="source-focus" type="button">Source control</button>
</body></html>
"""

HELPER_FRAME_PAGE = """<!doctype html>
<html lang="zh"><head><meta charset="utf-8"><title>Frame Host</title></head>
<body>
<iframe id="target" src="/source" width="640" height="480"></iframe>
</body></html>
"""


class HelperHandler(BaseHTTPRequestHandler):
    """Serve the controlled helper origin: a page and an iframe host."""

    def log_message(self, *args):
        pass

    def do_GET(self):
        path = urlparse(self.path).path
        if path in ('/', '/source'):
            body = HELPER_SOURCE_PAGE
        elif path == '/frame':
            body = HELPER_FRAME_PAGE
        else:
            self.send_error(404)
            return
        data = body.encode('utf-8')
        self.send_response(200)
        self.send_header('Content-Type', 'text/html; charset=utf-8')
        self.send_header('Content-Length', str(len(data)))
        self.send_header('Cache-Control', 'no-store')
        self.end_headers()
        self.wfile.write(data)


def hash_parts(page):
    raw = page.evaluate('() => location.hash.slice(1)')
    parsed = urlsplit(raw)
    return parsed.path, parse_qs(parsed.query)


def wait_until(runner, predicate, message, timeout=5):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return True
        runner.current_page.wait_for_timeout(50)
    runner.check(False, message)
    return False


def mutating_call(call):
    path, method = call['path'], call['method']
    if method == 'DELETE':
        # Releasing a safe preview cache record does not write account data.
        # Unknown-result acknowledgement is checked separately below.
        return re.fullmatch(r'/admin/config-previews/[A-Za-z0-9_-]{1,80}', path) is None
    if method in ('PUT', 'PATCH'):
        return True
    if method == 'POST' and path.endswith('/apply') and path.startswith('/admin/config-previews'):
        return True
    if method == 'POST' and path.startswith(('/admin/sites', '/admin/site-accounts', '/admin/channels')):
        return True
    return False


def screenshot(runner, page, name):
    path = runner.output / f'{name}.jpg'
    page.screenshot(path=str(path), type='jpeg', quality=82, animations='disabled')
    runner.check(path.read_bytes()[:2] == b'\xff\xd8', f'{name} is not a JPEG')
    runner.record(name, screenshot=str(path), bytes=path.stat().st_size)
    return path


class BrowserImportRunner(Runner):
    def __init__(self, browser, base, output, root, helper_base, case_names=None):
        super().__init__(browser, base, output, case_names)
        self.root = root
        self.helper_base = helper_base
        self.evidence = []
        self.current_page = None

    def record(self, name, **values):
        self.evidence.append({'case': name, **values})

    def case(self, name, callback, path=ROUTE_PATH, width=1440, height=900,
             theme='light', read_only=True):
        if self.case_names and name not in self.case_names:
            return
        context = self.browser.new_context(
            viewport={'width': width, 'height': height},
            color_scheme=theme,
            reduced_motion='reduce',
        )
        context.set_default_timeout(5000)
        context.set_default_navigation_timeout(30000)
        context.add_init_script(
            "localStorage.setItem('pivotflow_token','synthetic-browser-import-only');"
            "localStorage.setItem('pivotflow_web_role','admin');"
        )
        context.add_init_script(
            f"localStorage.setItem('pivotflow_theme', {json.dumps(theme)});"
        )
        data = BrowserImportData()
        page = context.new_page()
        self.current_page = page
        page.on(
            'pageerror',
            lambda error: self.events.append({'case': name, 'pageerror': str(error)}),
        )
        origin = urlsplit(self.base).netloc

        def intercept(route):
            parsed = urlsplit(route.request.url)
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
            if read_only:
                writes = [call for call in data.calls if mutating_call(call)]
                self.check(not writes, f'Read-only import flow issued writes: {writes[:3]}')
            self.record(name, admin_calls=len(data.calls),
                        seconds=round(time.perf_counter() - started, 3))
            self.results.append({
                'name': name,
                'pass': True,
                'seconds': round(time.perf_counter() - started, 3),
            })
        except Exception as error:
            self.results.append({
                'name': name,
                'pass': False,
                'error': str(error)[:1800],
                'seconds': round(time.perf_counter() - started, 3),
                'layout': page.evaluate("""() => ({
                  path: location.hash,
                  viewport: {width: innerWidth, height: innerHeight},
                  scroll: document.documentElement.scrollWidth,
                  overflow: [...document.querySelectorAll('main *')]
                    .map(element => ({element, rect: element.getBoundingClientRect()}))
                    .filter(({rect}) => rect.width && (rect.right > innerWidth + 1 || rect.left < -1))
                    .slice(0, 12)
                    .map(({element, rect}) => ({
                      tag: element.tagName,
                      class: element.className,
                      left: rect.left,
                      right: rect.right,
                    })),
                  active: {
                    tag: document.activeElement?.tagName,
                    label: document.activeElement?.getAttribute('aria-label'),
                  },
                })"""),
            })
            self.record(name, failed=True, calls=data.calls[-12:])
        finally:
            for pending in data.pending_previews:
                try:
                    pending.abort('aborted')
                except Exception:
                    pass
            context.unroute_all(behavior='ignoreErrors')
            context.close()


def route_cases(runner):
    def route_exists(page, data):
        expect(page.get_by_role('link', name=NAV_LABEL, exact=True)).to_be_visible()
        expect(page.locator('h1')).to_have_text(NAV_LABEL)
        runner.record('route-exists', url=page.url, h1=page.locator('h1').inner_text())

    runner.case('route-exists', route_exists, path=ROUTE_PATH)

    def recovery(page, data):
        preview_id = 'bpv-restored'
        data.preview_dto = build_preview(preview_id, status='ready')
        page.goto(f'{runner.base}/web/console/#{ROUTE_PATH}?preview_id={preview_id}',
                  wait_until='networkidle')
        expect(page.get_by_role('region', name=PREVIEW_REGION, exact=True)).to_be_visible()
        reads = [call for call in data.calls if call['path'].startswith('/admin/config-previews')]
        runner.check(reads, 'Recovering a preview must GET the same preview id')
        runner.check(all(call['method'] == 'GET' for call in reads),
                     'Recovering a preview must not apply or discard it')
        path, query = hash_parts(page)
        runner.check(query.get('preview_id') == [preview_id], f'preview_id lost from URL: {query}')
        runner.record('route-recovery', preview_id=preview_id)

    runner.case('route-recovery-preview-id', recovery, path=ROUTE_PATH)

    def target_ids(page, data):
        page.goto(f'{runner.base}/web/console/#{ROUTE_PATH}?account_id=101&site_id=1',
                  wait_until='networkidle')
        expect(page.locator('h1')).to_have_text(NAV_LABEL)
        path, query = hash_parts(page)
        runner.check(query.get('account_id') == ['101'], f'account_id seed lost: {query}')
        runner.check(query.get('site_id') == ['1'], f'site_id seed lost: {query}')
        runner.check(all(re.fullmatch(r'\d+', value[0]) for key, value in query.items()
                         if key in ('account_id', 'site_id')),
                     'Target seeds must stay positive integer IDs')
        runner.record('route-target-ids', query=query)

    runner.case('route-target-ids-are-ints', target_ids, path=ROUTE_PATH)

    def route_cleanup(page, data):
        raw_query = urlencode({'account_id': '101', 'site_id': '1', 'helper': '1',
                               'credential': SECRET_API_KEY, 'preview_id': '../unsafe'})
        page.goto(f'{runner.base}/web/console/#{ROUTE_PATH}?{raw_query}', wait_until='networkidle')
        _, query = hash_parts(page)
        runner.check(query == {'account_id': ['101'], 'site_id': ['1'], 'helper': ['1']},
                     'The import route did not remove unknown or invalid query values')
        runner.check(not any(call['path'].startswith('/admin/config-previews/') for call in data.calls),
                     'An invalid preview ID must not reach the recovery API')
        runner.record('route-cleanup', retained=sorted(query))

    runner.case('route-removes-untrusted-query', route_cleanup)

    def success_links(page, data):
        data.preview_dto = build_preview(
            'bpv-applied', status='applied',
            result=build_apply_result('bpv-applied'),
        )
        data.preview_status = 'applied'
        page.goto(f'{runner.base}/web/console/#{ROUTE_PATH}?preview_id=bpv-applied',
                  wait_until='networkidle')
        expect(page.get_by_role('region', name=PREVIEW_REGION, exact=True)).to_be_visible()
        hrefs = page.evaluate(
            "() => [...document.querySelectorAll('a[href]')].map(a => a.getAttribute('href'))"
        )
        joined = ' '.join(hrefs)
        runner.check('focus_account_id=101' in joined,
                     f'Applied result must link the focused account: {hrefs}')
        runner.check('sync_account_id=101' in joined,
                     f'Applied result must link the channel sync target: {hrefs}')
        runner.check(any(href.endswith('#/connect') for href in hrefs),
                     f'Applied result must link client access: {hrefs}')
        runner.record('success-links', links=hrefs[:12])

    runner.case('success-links', success_links, path=ROUTE_PATH)


def file_cases(runner):
    def choose_file(page, name, payload):
        path = runner.output / 'documents' / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(payload, ensure_ascii=False), encoding='utf-8')
        page.locator('input[type="file"]').first.set_input_files(str(path))
        return path

    def lightweight_document(page, data):
        document = build_pivotflow_doc(count=5)
        data.inspect_count = 5
        choose_file(page, 'pivotflow.json', document)
        inspect = page.get_by_role('button', name=pfilter('服务器检查|检查'))
        inspect.first.click()
        wait_until(runner, lambda: bool(data.inspect_bodies), 'The page never inspected the document')
        body = data.inspect_bodies[-1]
        runner.check(isinstance(body.get('document'), str),
                     'The console must send the original file text, not a parsed object')
        text = body['document']
        runner.check('pivotflow-browser-bridge' in text, 'Inspection must keep the raw version')
        expect(page.get_by_text(SOURCE_SITE_NAME, exact=False).first).to_be_visible()
        expect(page.get_by_text(SOURCE_ORIGIN, exact=False).first).to_be_visible()
        runner.check(not any(mutating_call(call) for call in data.calls),
                     'Choosing and inspecting a file must not write business data')
        runner.record('file-pivotflow', inspected=len(data.inspect_bodies), entries=5)

    runner.case('file-pivotflow-document', lightweight_document)

    def hub_versions(page, data):
        found = []
        for version in ('4.0', '3.0', '2.0'):
            document = build_all_api_hub_doc(version)
            choose_file(page, f'hub-{version}.json', document)
            page.get_by_role('button', name=pfilter('服务器检查|检查')).first.click()
            wait_until(runner, lambda: len(data.inspect_bodies) > len(found),
                       f'No inspection for All API Hub {version}')
            found.append(version)
            text = data.inspect_bodies[-1]['document']
            runner.check(version in text, f'All API Hub {version} text was not preserved')
        expect(page.get_by_text('Hub Source', exact=False).first).to_be_visible()
        runner.record('file-hub-versions', versions=found)

    runner.case('file-all-api-hub-versions', hub_versions)

    def sixty_four_entries(page, data):
        data.inspect_count = 64
        choose_file(page, 'pivotflow-64.json', build_pivotflow_doc(count=64))
        page.get_by_role('button', name=pfilter('服务器检查|检查')).first.click()
        wait_until(runner, lambda: bool(data.inspect_bodies), 'The 64-entry document was not inspected')
        pager = page.get_by_role('navigation', name=pfilter('分页'))
        expect(pager).to_be_visible()
        rows = page.locator('[data-entry-index], .browser-import-entry')
        expect(rows).to_have_count(10)
        jump = pager.get_by_role('textbox', name=pfilter('跳转|页码'))
        jump.fill('7')
        jump.press('Enter')
        expect(rows).to_have_count(4)
        runner.check(page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'),
                     'The 64-entry candidate list overflows the viewport')
        runner.record('file-64-entries', page_size=10, last_page=4)

    runner.case('file-64-entries-10-per-page', sixty_four_entries)

    def source_confirmation(page, data):
        choose_file(page, 'pivotflow-source.json', build_pivotflow_doc(count=5))
        page.get_by_role('button', name=pfilter('服务器检查|检查')).first.click()
        wait_until(runner, lambda: bool(data.inspect_bodies), 'The document was not inspected')
        confirm = page.get_by_role('checkbox', name=pfilter('来源|已核对|确认'))
        expect(confirm.first).to_be_visible()
        expect(confirm.first).not_to_be_checked()
        verify_button = page.get_by_role('button', name=pfilter('验证.*预览'))
        runner.check(verify_button.first.is_disabled(),
                     'Verification must stay disabled before the source is confirmed')
        confirm.first.check()
        expect(confirm.first).to_be_checked()
        ok_button = page.get_by_role('button', name=pfilter('验证.*预览|停用保存预览'))
        runner.check(not ok_button.first.is_disabled(),
                     'Confirming the source must enable generating a preview')
        runner.record('source-confirmation', confirmed=True)

    runner.case('file-source-confirmation', source_confirmation)

    def invalid_utf8(page, data):
        path = runner.output / 'documents' / 'invalid-utf8.json'
        path.parent.mkdir(parents=True, exist_ok=True)
        raw = json.dumps(build_pivotflow_doc(count=1), ensure_ascii=False).encode('utf-8')
        path.write_bytes(raw.replace(SECRET_API_KEY.encode('utf-8'), b'\xff', 1))
        page.locator('input[type="file"]').first.set_input_files(str(path))
        expect(page.get_by_role('alert')).to_contain_text('UTF-8')
        expect(page.get_by_role('button', name='服务器检查', exact=True)).to_be_disabled()
        runner.check(not data.inspect_bodies and not data.preview_bodies,
                     'Damaged UTF-8 must be rejected before an import request')
        runner.record('file-invalid-utf8', rejected=True, import_requests=0)

    runner.case('file-invalid-utf8-rejected', invalid_utf8)


def preview_cases(runner):
    def prepare(page, data, count=5):
        path = runner.output / 'documents' / 'preview.json'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(build_pivotflow_doc(count=count), ensure_ascii=False),
                        encoding='utf-8')
        page.locator('input[type="file"]').first.set_input_files(str(path))
        page.get_by_role('button', name=pfilter('服务器检查|检查')).first.click()
        wait_until(runner, lambda: bool(data.inspect_bodies), 'The document was not inspected')
        page.get_by_role('checkbox', name=pfilter('来源|已核对|确认')).first.check()

    def verify_preview(page, data):
        prepare(page, data)
        page.get_by_role('button', name=pfilter('验证并生成预览')).first.click()
        wait_until(runner, lambda: bool(data.preview_bodies), 'No preview request was sent')
        body = data.preview_bodies[-1]
        runner.check(body.get('source_confirmed') is True,
                     'A preview must only be requested after explicit source confirmation')
        runner.check(body.get('mode') == 'verify', f'Wrong preview mode: {body.get("mode")}')
        expect(page.get_by_role('region', name=PREVIEW_REGION, exact=True)).to_be_visible()
        runner.record('preview-verify', mode=body.get('mode'), entry_index=body.get('entry_index'))

    runner.case('preview-verify-creates', verify_preview)

    def save_disabled(page, data):
        data.preview_mode = 'save_disabled'
        prepare(page, data)
        before = len(data.calls)
        page.get_by_role('button', name=pfilter('停用保存预览')).first.click()
        wait_until(runner, lambda: bool(data.preview_bodies), 'No save-disabled preview was sent')
        body = data.preview_bodies[-1]
        runner.check(body.get('mode') == 'save_disabled',
                     f'Save-disabled mode was not requested: {body.get("mode")}')
        external = [call for call in data.calls[before:] if call['path'].startswith('/admin/browser-import/preview')]
        runner.check(external, 'Save-disabled preview must call the preview endpoint')
        for path in ('/admin/sites', '/admin/site-accounts', '/admin/channels'):
            runner.check(not any(call['path'].startswith(path) and call['method'] != 'GET'
                                 for call in data.calls),
                         f'Save-disabled mode must not touch {path}')
        runner.record('preview-save-disabled', mode=body.get('mode'))

    runner.case('preview-save-disabled-silent', save_disabled)

    def browser_required(page, data):
        data.browser_required_once = True
        prepare(page, data)
        page.get_by_role('button', name=pfilter('验证并生成预览')).first.click()
        wait_until(runner, lambda: bool(data.preview_bodies), 'No preview attempt was sent')
        wait_until(
            runner,
            lambda: page.get_by_text(pfilter('浏览器|browser_required|WAF|挑战')).count() > 0,
            'A browser challenge must be stated explicitly',
        )
        retry = page.get_by_role('button', name=pfilter('重新验证|重试|再次验证'))
        expect(retry.first).to_be_visible()
        runner.check(not any(mutating_call(call) for call in data.calls),
                     'A browser challenge must not mutate account status')
        retry.first.click()
        wait_until(runner, lambda: len(data.preview_bodies) >= 2,
                   'Explicit retry must send a second bounded verification')
        runner.record('preview-browser-required', attempts=len(data.preview_bodies))

    runner.case('preview-browser-required-retry', browser_required)

    def target_and_repair(page, data):
        prepare(page, data)
        target = page.get_by_role('combobox', name=pfilter('目标账号|账号'))
        if target.count():
            target.first.select_option(label=SOURCE_ACCOUNT_LABEL)
        field = page.get_by_label('凭证修正（API Key）', exact=True)
        field.fill(SECRET_API_KEY + '-repaired')
        field.press('Tab')
        before = len(data.inspect_bodies)
        page.get_by_role('button', name=pfilter('服务器检查|重新核对|检查')).first.click()
        wait_until(runner, lambda: len(data.inspect_bodies) > before,
                   'Repairing a credential must re-inspect the document text')
        confirm = page.get_by_role('checkbox', name=pfilter('来源|已核对|确认'))
        if confirm.count():
            runner.check(not confirm.first.is_checked(),
                         'Changing the credential must clear the source confirmation')
        runner.check(
            page.get_by_role('region', name=PREVIEW_REGION, exact=True).count() == 0,
            'Changing the credential must discard the stale preview',
        )
        repaired = json.loads(data.inspect_bodies[-1]['document'])
        runner.check(repaired['entries'][0]['credential']['api_key'] == SECRET_API_KEY + '-repaired',
                     'The re-inspection did not receive the edited credential in the selected document entry')
        confirm.first.check()
        page.get_by_role('button', name='验证并生成预览', exact=True).click()
        wait_until(runner, lambda: bool(data.preview_bodies), 'The repaired target was not previewed')
        runner.check(data.preview_bodies[-1]['target_account_id'] == 101,
                     'Re-inspection lost the explicitly selected target account')
        runner.record('preview-target-repair', inspections=len(data.inspect_bodies))

    runner.case('preview-exact-target-repair', target_and_repair)


def race_cases(runner):
    def prepare(page, data):
        path = runner.output / 'documents' / 'race.json'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(build_pivotflow_doc(count=5), ensure_ascii=False),
                        encoding='utf-8')
        page.locator('input[type="file"]').first.set_input_files(str(path))
        page.get_by_role('button', name=pfilter('服务器检查|检查')).first.click()
        wait_until(runner, lambda: bool(data.inspect_bodies), 'The document was not inspected')
        page.get_by_role('checkbox', name=pfilter('来源|已核对|确认')).first.check()

    def cancellation(page, data):
        data.hold_preview = True
        prepare(page, data)
        page.get_by_role('button', name=pfilter('验证并生成预览')).first.click()
        wait_until(runner, lambda: bool(data.pending_previews), 'The held preview never arrived')
        stop = page.get_by_role('button', name=pfilter('停止验证|取消|停止'))
        expect(stop.first).to_be_visible()
        stop.first.click()
        runner.check(len(data.preview_bodies) == 1,
                     'Stopping verification must not invent another preview request')
        runner.check(page.get_by_text(pfilter('已取消|停止|取消')).count() > 0,
                     'Cancelling must surface a cancellable state')
        runner.record('race-cancellation', pending=len(data.pending_previews))

    runner.case('race-cancellation', cancellation)

    def late_response(page, data):
        data.hold_preview = True
        prepare(page, data)
        page.get_by_role('button', name=pfilter('验证并生成预览')).first.click()
        wait_until(runner, lambda: bool(data.pending_previews), 'The held preview never arrived')
        page.get_by_role('button', name=pfilter('停止验证|取消|停止')).first.click()
        path = runner.output / 'documents' / 'race-other.json'
        path.write_text(json.dumps(build_pivotflow_doc(count=4), ensure_ascii=False),
                        encoding='utf-8')
        page.locator('input[type="file"]').first.set_input_files(str(path))
        page.get_by_role('button', name=pfilter('服务器检查|检查')).first.click()
        wait_until(runner, lambda: len(data.inspect_bodies) >= 2,
                   'Replacing the file must re-inspect')
        for pending in list(data.pending_previews):
            try:
                pending.abort('aborted')
            except Exception:
                pass
            data.pending_previews.remove(pending)
        runner.check(
            page.get_by_role('region', name=PREVIEW_REGION, exact=True).count() == 0,
            'A late preview must not attach itself to the new selection',
        )
        runner.record('race-late-response', inspections=len(data.inspect_bodies))

    runner.case('race-late-response', late_response)

    def input_edit(page, data):
        prepare(page, data)
        page.get_by_role('button', name=pfilter('验证并生成预览')).first.click()
        wait_until(runner, lambda: bool(data.preview_bodies), 'No preview was sent')
        expect(page.get_by_role('region', name=PREVIEW_REGION, exact=True)).to_be_visible()
        label = page.get_by_role('textbox', name=pfilter('目标名称|名称|站点名称'))
        if label.count():
            label.first.fill('Renamed Target')
            label.first.press('Tab')
        confirm = page.get_by_role('checkbox', name=pfilter('来源|已核对|确认'))
        if confirm.count():
            runner.check(not confirm.first.is_checked(),
                         'Editing the target must clear the source confirmation')
        runner.record('race-input-edit', previews=len(data.preview_bodies))

    runner.case('race-input-edit', input_edit)

    def stale_preview(page, data):
        data.preview_dto = build_preview('bpv-stale', status='stale', can_apply=False,
                                         error='preview_stale')
        data.preview_status = 'stale'
        page.goto(f'{runner.base}/web/console/#{ROUTE_PATH}?preview_id=bpv-stale',
                  wait_until='networkidle')
        expect(page.get_by_role('region', name=PREVIEW_REGION, exact=True)).to_be_visible()
        apply_button = page.get_by_role('button', name=pfilter('确认应用'))
        if apply_button.count():
            runner.check(apply_button.first.is_disabled(),
                         'A stale preview must not be applicable')
        runner.check(page.get_by_text(pfilter('重新生成|已失效|变化')).count() > 0,
                     'A stale preview must tell the user to regenerate')
        runner.record('race-stale-preview', status='stale')

    runner.case('race-stale-preview', stale_preview)


def apply_cases(runner):
    def prepare_applied_ready(page, data):
        data.preview_dto = build_preview('bpv-apply', status='ready')
        data.preview_status = 'ready'
        page.goto(f'{runner.base}/web/console/#{ROUTE_PATH}?preview_id=bpv-apply',
                  wait_until='networkidle')
        expect(page.get_by_role('region', name=PREVIEW_REGION, exact=True)).to_be_visible()

    def response_lost(page, data):
        data.drop_apply_response_once = True
        prepare_applied_ready(page, data)
        page.get_by_role('button', name=pfilter('确认应用')).first.click()
        wait_until(runner, lambda: data.apply_calls >= 1, 'No apply request was sent')
        page.wait_for_timeout(300)
        runner.check(data.apply_calls == 1,
                     f'A lost apply response must not trigger a second apply: {data.apply_calls}')
        gets = [call for call in data.calls
                if call['path'].startswith('/admin/config-previews') and call['method'] == 'GET']
        runner.check(gets, 'A lost apply response must be recovered by GET on the same preview id')
        runner.record('apply-response-lost', applies=data.apply_calls, gets=len(gets))

    runner.case('apply-response-lost-then-get', response_lost, read_only=False)

    def no_repeat(page, data):
        prepare_applied_ready(page, data)
        page.get_by_role('button', name=pfilter('确认应用')).first.click()
        wait_until(runner, lambda: data.apply_calls >= 1, 'No apply request was sent')
        wait_until(runner, lambda: page.get_by_text(pfilter('已应用')).count() > 0,
                   'The applied state was not shown')
        button = page.get_by_role('button', name=pfilter('确认应用|已应用'))
        runner.check(button.first.is_disabled(), 'An applied preview must not be re-applicable')
        runner.check(data.apply_calls == 1, 'An applied preview was applied twice')
        runner.record('apply-no-repeat', applies=data.apply_calls)

    runner.case('apply-no-repeat', no_repeat, read_only=False)

    def unknown_no_replacement(page, data):
        data.preview_dto = build_preview('bpv-unknown', status='applying', can_apply=False)
        data.preview_status = 'unknown'
        page.goto(f'{runner.base}/web/console/#{ROUTE_PATH}?preview_id=bpv-unknown',
                  wait_until='networkidle')
        expect(page.get_by_role('region', name=PREVIEW_REGION, exact=True)).to_be_visible()
        runner.check(page.get_by_text(pfilter('待确认|人工核对|请勿重复')).count() > 0,
                     'An unknown result must ask for manual confirmation')
        runner.check(not any(call['path'] == '/admin/browser-import/preview' for call in data.calls),
                     'An unknown result must not silently create a replacement preview')
        runner.record('apply-unknown-no-replacement', status='unknown')

    runner.case('apply-unknown-no-replacement', unknown_no_replacement)

    def unknown_acknowledgement(page, data):
        data.preview_dto = build_preview('bpv-ack', status='unknown', can_apply=False)
        data.preview_status = 'unknown'
        page.goto(f'{runner.base}/web/console/#{ROUTE_PATH}?preview_id=bpv-ack', wait_until='networkidle')
        discard = page.get_by_role('button', name='核对后移除预览', exact=True)
        expect(discard).to_be_disabled()
        runner.check(not any(call['method'] == 'DELETE' for call in data.calls),
                     'Unknown previews must not be discarded automatically')
        page.get_by_role('checkbox', name='我已在账号页核对实际导入结果', exact=True).check()
        expect(discard).to_be_enabled()
        discard.click()
        wait_until(runner, lambda: any(call['method'] == 'DELETE' for call in data.calls),
                   'Acknowledging the result did not remove the original preview')
        deletes = [call for call in data.calls if call['method'] == 'DELETE']
        runner.check(len(deletes) == 1 and deletes[0]['path'] == '/admin/config-previews/bpv-ack'
                     and deletes[0]['query'] == {'acknowledged': ['true']},
                     'Unknown cleanup must carry explicit acknowledgement for the same ID')
        runner.check(data.apply_calls == 0 and not data.preview_bodies,
                     'Acknowledgement must not apply again or create a new preview')
        runner.record('apply-unknown-ack', deletions=1, applies=0)

    runner.case('apply-unknown-requires-acknowledgement', unknown_acknowledgement)

    def frozen_inputs(page, data):
        path = runner.output / 'documents' / 'frozen.json'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(build_pivotflow_doc(count=1), ensure_ascii=False), encoding='utf-8')
        page.locator('input[type="file"]').first.set_input_files(str(path))
        page.get_by_role('button', name='服务器检查', exact=True).click()
        confirm = page.get_by_role('checkbox', name='我已核对来源、完整 API 地址与目标账号', exact=True)
        expect(confirm).to_be_visible()
        confirm.check()
        page.get_by_role('button', name='验证并生成预览', exact=True).click()
        region = page.get_by_role('region', name=PREVIEW_REGION, exact=True)
        expect(region).to_be_visible()
        data.drop_apply_response_once = True
        data.dropped_apply_status = 'unknown'
        region.get_by_role('button', name='确认应用', exact=True).click()
        expect(page.get_by_role('button', name='核对后移除预览', exact=True)).to_be_visible()
        expect(page.get_by_label('凭证修正（API Key）', exact=True)).to_be_disabled()
        expect(page.get_by_role('button', name='显示内容', exact=True)).to_be_disabled()
        expect(page.locator('input[type="file"]').first).to_be_disabled()
        expect(confirm).to_be_disabled()
        expect(page.get_by_role('combobox', name='目标账号', exact=True)).to_be_disabled()
        runner.check(len(data.preview_bodies) == 1 and data.apply_calls == 1,
                     'Unknown-state input protection triggered another operation')
        runner.record('apply-frozen-inputs', protected=5)

    runner.case('apply-unknown-freezes-inputs', frozen_inputs, read_only=False)

    def discard(page, data):
        data.preview_dto = build_preview('bpv-discard', status='ready')
        data.preview_status = 'ready'
        page.goto(f'{runner.base}/web/console/#{ROUTE_PATH}?preview_id=bpv-discard',
                  wait_until='networkidle')
        expect(page.get_by_role('region', name=PREVIEW_REGION, exact=True)).to_be_visible()
        discard_button = page.get_by_role('button', name=pfilter('解除|丢弃|删除|移除'))
        if discard_button.count():
            discard_button.first.click()
            wait_until(
                runner,
                lambda: any(call['method'] == 'DELETE' and call['path'].startswith('/admin/config-previews')
                            for call in data.calls),
                'Discarding a preview must call DELETE on the preview id',
            )
        runner.record('apply-discard', deletes=sum(
            1 for call in data.calls
            if call['method'] == 'DELETE' and call['path'].startswith('/admin/config-previews')
        ))

    runner.case('apply-discard', discard, read_only=False)


def secret_cases(runner):
    def no_secret_leak(page, data):
        path = runner.output / 'documents' / 'secret.json'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(build_pivotflow_doc(count=5), ensure_ascii=False),
                        encoding='utf-8')
        page.locator('input[type="file"]').first.set_input_files(str(path))
        page.get_by_role('button', name=pfilter('服务器检查|检查')).first.click()
        wait_until(runner, lambda: bool(data.inspect_bodies), 'The document was not inspected')
        page.get_by_role('checkbox', name=pfilter('来源|已核对|确认')).first.check()
        page.get_by_role('button', name=pfilter('验证并生成预览')).first.click()
        wait_until(runner, lambda: bool(data.preview_bodies), 'No preview was sent')
        page.wait_for_timeout(200)
        state = page.evaluate("""() => ({
          hash: location.hash,
          href: location.href,
          local: JSON.stringify(localStorage),
          session: JSON.stringify(sessionStorage),
          text: document.body.innerText,
        })""")
        for secret in SECRETS:
            for surface in ('hash', 'href', 'local', 'session', 'text'):
                runner.check(secret not in state[surface],
                             f'Credential reached {surface} outside the request body')
        runner.record('secret-free', surfaces=('url', 'storage', 'dom'))

    runner.case('secret-free-url-storage-ui', no_secret_leak)

    def changing_source_remasks(page, data):
        path = runner.output / 'documents' / 'source-mask.json'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(build_pivotflow_doc(count=5), ensure_ascii=False), encoding='utf-8')
        page.locator('input[type="file"]').first.set_input_files(str(path))
        page.get_by_role('button', name='服务器检查', exact=True).click()
        primary = page.get_by_label('凭证修正（API Key）', exact=True)
        expect(primary).to_have_attribute('type', 'password')
        page.locator('.browser-import-target').get_by_role('button', name='显示内容', exact=True).click()
        expect(primary).to_have_attribute('type', 'text')
        page.locator('button[data-entry-index="3"]').click()
        replacement = page.get_by_label('凭证修正（Access Token）', exact=True)
        expect(replacement).to_have_value(SECRET_ACCESS_TOKEN)
        expect(replacement).to_have_attribute('type', 'password')
        expect(page.get_by_label('Refresh Token（可选）', exact=True)).to_have_attribute('type', 'password')
        page.locator('button[data-entry-index="0"]').click()
        expect(page.get_by_label('凭证修正（API Key）', exact=True)).to_have_attribute('type', 'password')
        runner.record('source-mask-reset', source_changes=2, default_mask_restored=True)

    runner.case('secret-source-switch-restores-mask', changing_source_remasks)


def find_download_trigger(page):
    for factory in (
        lambda: page.get_by_role('button', name=pfilter('下载.*脚本|下载助手')),
        lambda: page.get_by_role('link', name=pfilter('下载.*脚本|下载助手')),
        lambda: page.get_by_role('button', name=pfilter('下载')),
        lambda: page.get_by_role('link', name=pfilter('下载')),
    ):
        locator = factory()
        if locator.count():
            return locator.first
    return None


def helper_cases(runner):
    def helper_behaviour(page, data):
        trigger = find_download_trigger(page)
        runner.check(trigger is not None, 'The page must offer a download for the helper script')

        # Bake the expected origin to the controlled helper origin so the
        # positive path can run on a real, separate origin.
        origin_field = page.get_by_role('textbox', name=pfilter('期望来源|来源 origin|origin'))
        if origin_field.count():
            origin_field.first.fill(runner.helper_base)
        with page.expect_download() as download_info:
            trigger.click()
        download = download_info.value
        target = runner.output / 'helper-downloaded.js'
        download.save_as(str(target))
        source = target.read_text(encoding='utf-8')
        runner.check('require(' not in source and 'import ' not in source.splitlines()[0],
                     'The helper must be self-contained with no module imports')
        for secret in SECRETS:
            runner.check(secret not in source, 'The helper script must not embed real secrets')
        runner.record('helper-download', bytes=len(source))

        context = runner.browser.new_context(viewport={'width': 1280, 'height': 900})
        context.set_default_timeout(5000)
        context.add_init_script("""
          localStorage.setItem('user', JSON.stringify({id: 88001, username: 'demo',
            access_token: %s}));
          localStorage.setItem('auth_token', %s);
          localStorage.setItem('access_token', %s);
          localStorage.setItem('token', %s);
          localStorage.setItem('refresh_token', %s);
          localStorage.setItem('expires_at', '1795392000000');
          localStorage.setItem('unrelated', JSON.stringify({nested: {token: %s}}));
        """ % tuple(json.dumps(value) for value in (
            SECRET_ACCESS_TOKEN, SECRET_ACCESS_TOKEN, SECRET_ACCESS_TOKEN,
            SECRET_API_KEY, SECRET_REFRESH, SECRET_COOKIE,
        )))
        context.add_init_script("""(() => {
          window.__bridgeReads = [];
          const read = Storage.prototype.getItem;
          Storage.prototype.getItem = function(key) {
            window.__bridgeReads.push(key);
            return read.call(this, key);
          };
        })();""")
        helper_page = context.new_page()
        network = []
        helper_page.on('request', lambda request: network.append(request.url)
                       if request.url.startswith(('http://', 'https://')) else None)
        helper_page.on('websocket', lambda socket: network.append(socket.url))
        context.route('**/*', lambda route: route.continue_()
                      if urlsplit(route.request.url).netloc == urlsplit(runner.helper_base).netloc
                      else route.abort('blockedbyclient'))
        helper_page.on('pageerror',
                       lambda error: runner.events.append({'case': 'helper-exec', 'pageerror': str(error)}))
        try:
            helper_page.goto(f'{runner.helper_base}/source', wait_until='networkidle')
            network.clear()
            helper_page.locator('#source-focus').focus()
            helper_page.add_script_tag(content=source)
            read_button = helper_page.get_by_role('button', name=pfilter('读取|读取当前页'))
            expect(read_button.first).to_be_visible()
            read_button.first.click()
            reads = helper_page.evaluate('() => window.__bridgeReads')
            runner.check(reads == ['user', 'auth_token', 'access_token', 'token'],
                         'The helper must read only the finite known keys for this platform')
            body = helper_page.inner_text('body')
            runner.check(SOURCE_ORIGIN not in body, 'The helper must not hard-code a foreign origin')
            runner.check(helper_page.get_by_text(pfilter('候选|选择|多份')).count() > 0
                         or helper_page.get_by_role('radio').count() > 0
                         or helper_page.get_by_role('combobox').count() > 0,
                         'Multiple local candidates must be resolved by explicit choice')
            runner.check('unrelated' not in body,
                         'The helper must not recurse into unrelated objects')
            runner.check(
                helper_page.get_by_label(pfilter('cookie|Cookie')).count() > 0
                or 'cookie' in body.casefold(),
                'Cookie credentials must be entered manually, not read from storage',
            )

            export_button = helper_page.get_by_role('button', name='导出 JSON 文件', exact=True)
            expect(export_button).to_be_disabled()
            candidate = helper_page.get_by_role('combobox', name='选择凭证来源', exact=True)
            candidate.select_option('0')
            expect(export_button).to_be_enabled()

            def export_file(name):
                with helper_page.expect_download() as export_info:
                    export_button.click()
                export_path = runner.output / name
                export_info.value.save_as(str(export_path))
                payload = json.loads(export_path.read_text(encoding='utf-8'))
                expect(export_button).to_be_disabled()
                runner.check(helper_page.locator('input[type="password"]').evaluate_all('(nodes) => nodes.every(node => !node.value)'),
                             'Exporting must clear all credential input values')
                return payload

            exported = export_file('helper-export.json')
            runner.check(exported.get('format') == 'pivotflow-browser-bridge',
                         f'Exported helper file has the wrong format: {exported.get("format")}')
            entry = exported['entries'][0]
            runner.check(entry['source_origin'] == runner.helper_base and entry['base_url'] == runner.helper_base,
                         'The helper export must keep the current origin and complete base address')
            runner.check(entry['credential'] == {'access_token': SECRET_ACCESS_TOKEN, 'user_id': 88001},
                         'Explicit candidate selection exported the wrong credential or mixed refresh fields')

            helper_page.get_by_role('combobox', name='站点平台', exact=True).select_option('sub2api')
            read_button.first.click()
            helper_page.get_by_role('combobox', name='选择凭证来源', exact=True).select_option('0')
            sub2api = export_file('helper-sub2api-export.json')['entries'][0]
            runner.check(sub2api['platform'] == 'sub2api' and sub2api['credential']['refresh_token'] == SECRET_REFRESH
                         and sub2api['credential']['expires_at'] == 1795392000000,
                         'Sub2API export lost its explicitly supported refresh fields')

            helper_page.get_by_role('combobox', name='站点平台', exact=True).select_option('new-api-family')
            helper_page.get_by_role('combobox', name='凭证类型', exact=True).select_option('cookie')
            previous_reads = len(helper_page.evaluate('() => window.__bridgeReads'))
            read_button.first.click()
            runner.check(helper_page.evaluate('() => window.__bridgeReads')[previous_reads:] == ['user'],
                         'Cookie mode must not scan token or refresh storage keys')
            expect(export_button).to_be_disabled()
            helper_page.get_by_label('Cookie（手动填写，默认隐藏）', exact=True).fill(SECRET_COOKIE)
            cookie = export_file('helper-cookie-export.json')['entries'][0]
            runner.check(cookie['credential'] == {'cookie': SECRET_COOKIE, 'user_id': 88001},
                         'Manual Cookie export included a different authentication identity')

            helper_page.evaluate("() => { Storage.prototype.getItem = () => { throw new DOMException('blocked', 'SecurityError') } }")
            read_button.first.click()
            expect(helper_page.get_by_role('status')).to_contain_text('存储不可读')
            expect(export_button).to_be_disabled()
            runner.check(network == [], f'The helper must make zero network requests: {network[:3]}')

            close_button = helper_page.get_by_role('button', name=pfilter('关闭|完成'))
            if close_button.count():
                close_button.first.click()
            helper_page.wait_for_timeout(120)
            cleaned = helper_page.inner_text('body')
            runner.check(SECRET_ACCESS_TOKEN not in cleaned and SECRET_API_KEY not in cleaned,
                         'Closing the helper must clear secrets from the DOM')
            expect(helper_page.locator('#source-focus')).to_be_focused()
            runner.record('helper-exec', network=len(network), known_storage_keys=reads,
                          explicit_choice=True, exports=3, restricted_storage=True, focus_restored=True)
        finally:
            context.close()

        # Wrong-origin refusal: run the same helper on an unrelated origin.
        wrong = runner.browser.new_context(viewport={'width': 1280, 'height': 900})
        wrong.set_default_timeout(5000)
        wrong.add_init_script("localStorage.setItem('token', %s);" % json.dumps(SECRET_API_KEY))
        wrong.add_init_script("window.__bridgeReads = []; Storage.prototype.getItem = function(key) { window.__bridgeReads.push(key); return null }")
        wrong.route(
            '**/*',
            lambda route: route.fulfill(
                status=200,
                content_type='text/html; charset=utf-8',
                body='<html lang="zh"><body><h1>未确认来源</h1></body></html>',
            ),
        )
        wrong_page = wrong.new_page()
        try:
            wrong_page.goto(f'{runner.base}/web/console/', wait_until='networkidle')
            wrong_page.add_script_tag(content=source)
            wrong_page.wait_for_timeout(120)
            body = wrong_page.inner_text('body')
            runner.check(SECRET_API_KEY not in body,
                         'The helper must refuse to read on an unconfirmed origin')
            runner.check(wrong_page.get_by_role('button', name=pfilter('读取')).count() == 0,
                         'A wrong origin must not expose a working read button')
            runner.check(wrong_page.evaluate('() => window.__bridgeReads.length') == 0,
                         'Wrong-origin refusal must happen before reading storage')
            runner.record('helper-wrong-origin', refused=True)
        finally:
            wrong.close()

        # Iframe refusal: the same helper must not operate inside a frame.
        frame_context = runner.browser.new_context(viewport={'width': 1280, 'height': 900})
        frame_context.set_default_timeout(5000)
        frame_context.add_init_script("window.__bridgeReads = []; Storage.prototype.getItem = function(key) { window.__bridgeReads.push(key); return null }")
        frame_page = frame_context.new_page()
        try:
            frame_page.goto(f'{runner.helper_base}/frame', wait_until='networkidle')
            frame = frame_page.frames[-1]
            frame.add_script_tag(content=source)
            frame_page.wait_for_timeout(120)
            frame_body = frame_page.inner_text('body')
            runner.check(SECRET_API_KEY not in frame_body,
                         'The helper must refuse to read inside an iframe')
            runner.check(frame.evaluate('() => window.__bridgeReads.length') == 0,
                         'Iframe refusal must happen before reading storage')
            runner.record('helper-iframe', refused=True)
        finally:
            frame_context.close()

    runner.case('helper-actual-execution', helper_behaviour,
                path=ROUTE_PATH + '?helper=1')


def responsive_cases(runner):
    dimensions = [
        (320, 900, 'light'),
        (390, 844, 'light'),
        (768, 1024, 'light'),
        (1280, 900, 'light'),
        (1280, 480, 'light'),
    ]

    def layout(page, data):
        path = runner.output / 'documents' / 'layout.json'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(build_pivotflow_doc(count=6), ensure_ascii=False),
                        encoding='utf-8')
        page.locator('input[type="file"]').first.set_input_files(str(path))
        page.get_by_role('button', name=pfilter('服务器检查|检查')).first.click()
        wait_until(runner, lambda: bool(data.inspect_bodies), 'The document was not inspected')
        viewport = page.viewport_size
        runner.check(page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'),
                     'The browser-import page has horizontal overflow')
        expect(page.locator('h1')).to_have_text(NAV_LABEL)
        screenshot(
            runner,
            page,
            f'browser-import-{viewport["width"]}x{viewport["height"]}'
            f'-{page.evaluate("document.documentElement.dataset.theme || " + json.dumps("light"))}',
        )
        runner.record('responsive', width=viewport['width'], height=viewport['height'])

    for width, height, theme in dimensions:
        runner.case(
            f'browser-import-layout-{width}-{height}',
            layout,
            path=ROUTE_PATH,
            width=width,
            height=height,
            theme=theme,
        )


def keyboard_cases(runner):
    def focus_order(page, data):
        path = runner.output / 'documents' / 'focus.json'
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(build_pivotflow_doc(count=6), ensure_ascii=False),
                        encoding='utf-8')
        page.locator('input[type="file"]').first.set_input_files(str(path))
        page.get_by_role('button', name=pfilter('服务器检查|检查')).first.click()
        wait_until(runner, lambda: bool(data.inspect_bodies), 'The document was not inspected')
        targets = [
            page.locator('input[type="file"]').first,
            page.get_by_role('button', name=pfilter('服务器检查|检查')).first,
            page.get_by_role('checkbox', name=pfilter('来源|已核对|确认')).first,
        ]
        focused = 0
        for target in targets:
            target.focus()
            expect(target).to_be_focused()
            runner.check(target.evaluate('(element) => element.tabIndex >= 0'),
                         'A browser-import control is not keyboard reachable')
            focused += 1
        runner.record('keyboard-focus', targets=focused)

    runner.case('keyboard-focus', focus_order)


def resource_cases(runner):
    def budgets(page, data):
        page.wait_for_load_state('networkidle')
        session = page.evaluate("""() => {
          const entries = performance.getEntriesByType('resource')
            .filter(entry => entry.name.includes('/web/console/'));
          return {
            resources: entries.length,
            encoded_bytes: entries.reduce((sum, entry) => sum + entry.encodedBodySize, 0),
            decoded_bytes: entries.reduce((sum, entry) => sum + entry.decodedBodySize, 0),
            route_loaded: entries.some(entry => entry.name.includes('/BrowserImportPage-')
              && entry.name.endsWith('.js')),
          };
        }""")
        assets = runner.root / 'web/console/assets'
        chunks = [
            path for path in assets.iterdir()
            if path.is_file() and path.suffix == '.js'
            and path.name.startswith('BrowserImportPage-')
        ]
        runner.check(chunks, 'Built BrowserImportPage route chunk is missing')
        chunk = max(chunks, key=lambda path: path.stat().st_size)
        route_sizes = {
            'decoded_bytes': chunk.stat().st_size,
            'gzip_bytes': len(gzip.compress(chunk.read_bytes(), mtime=0)),
        }
        runner.check(session['route_loaded'], 'The browser did not load the BrowserImportPage chunk')
        runner.check(session['encoded_bytes'] <= 450000,
                     'Console session exceeds 450000 encoded bytes')
        runner.check(route_sizes['gzip_bytes'] <= 35840,
                     'BrowserImportPage exceeds 35 KiB gzip')
        runner.metrics['browser_import_session'] = session
        runner.metrics['browser_import_chunk'] = route_sizes
        runner.record('browser-import-resource-budgets', session=session, route=route_sizes)

    runner.case('browser-import-resource-budgets', budgets, path=ROUTE_PATH)


def integration_cases(runner):
    def entry_links(page, data):
        header = page.locator('main .page-header')
        expect(header.get_by_role('link', name=NAV_LABEL, exact=True)).to_have_attribute('href', '#/browser-import')
        page.goto(f'{runner.base}/web/console/#/accounts?site_id=1&focus_account_id=101&open_credential=1', wait_until='networkidle')
        expect(page.locator('main .page-header').get_by_role('link', name=NAV_LABEL, exact=True)).to_have_attribute('href', '#/browser-import?site_id=1')
        repair = page.get_by_role('link', name='从浏览器资料修复此账号', exact=True)
        expect(repair).to_have_attribute('href', '#/browser-import?account_id=101&site_id=1')
        repair.click()
        expect(page.locator('h1')).to_have_text(NAV_LABEL)
        _, query = hash_parts(page)
        runner.check(query == {'account_id': ['101'], 'site_id': ['1']},
                     'The credential repair entry lost the actual account identity')
        runner.record('integration-entry-links', account_id=101, site_id=1)

    runner.case('integration-sites-accounts-credential-links', entry_links, path='/sites')

    def sync_seed(page, data):
        dialog = page.get_by_role('dialog', name='同步站点渠道', exact=True)
        expect(dialog).to_be_visible()
        expect(dialog.get_by_role('checkbox', name='选择 demo', exact=True)).to_be_checked()
        expect(dialog.get_by_role('button', name='生成差异预览', exact=True)).to_be_enabled()
        runner.check(not any(call['method'] != 'GET' and call['path'].startswith('/admin/site-accounts/') for call in data.calls),
                     'A channel entry must only preselect, never start an upstream preview')
        runner.record('integration-sync-seed', account_id=101, selected=True, upstream_requests=0)

    runner.case('integration-channel-preselect-only', sync_seed, path='/channels?sync_account_id=101')

    def disabled_seed(page, data):
        data.accounts[0]['enabled'] = False
        page.reload(wait_until='networkidle')
        dialog = page.get_by_role('dialog', name='同步站点渠道', exact=True)
        expect(dialog).to_be_visible()
        selected = dialog.get_by_role('checkbox', name='选择 demo', exact=True)
        expect(selected).to_be_disabled()
        expect(selected).not_to_be_checked()
        expect(dialog.get_by_role('button', name='生成差异预览', exact=True)).to_be_disabled()
        expect(dialog.get_by_role('status')).to_contain_text('已停用')
        expect(dialog.get_by_role('link', name='核对目标账号', exact=True)).to_have_attribute('href', '#/accounts?focus_account_id=101')
        runner.record('integration-sync-disabled', account_id=101, selected=False)

    runner.case('integration-channel-disabled-account', disabled_seed, path='/channels?sync_account_id=101')

    def invalid_seed(page, data):
        for value in ('-1', '1.5', '0', '9007199254740992', 'opaque'):
            page.goto(f'{runner.base}/web/console/#/channels?sync_account_id={value}', wait_until='networkidle')
            runner.check(page.get_by_role('dialog').count() == 0,
                         f'Invalid account seed opened a synchronization dialog: {value}')
        runner.record('integration-sync-invalid', rejected=5)

    runner.case('integration-channel-invalid-seed', invalid_seed, path='/channels')

    def task_recovery(page, data):
        definitions = [
            ('browser-101', 'refresh', 1, 101, 'failed', 'browser_required'),
            ('expired-102', 'refresh', 1, 102, 'failed', 'expired'),
            ('site-only', 'announcement_refresh', 1, 0, 'partial', 'browser_required'),
            ('unknown-102', 'model_refresh', 1, 102, 'unknown', 'expired'),
            ('generic-101', 'model_refresh', 1, 101, 'failed', 'operation_failed'),
        ]
        data.task_snapshots = {name: {'id': name, 'kind': kind, 'site_id': site_id,
                                    'site_account_id': account_id, 'status': status,
                                    'error': error, 'created_at': fixtures.NOW}
                               for name, kind, site_id, account_id, status, error in definitions}
        page.reload(wait_until='networkidle')
        region = page.get_by_role('region', name='逐项任务结果', exact=True)
        expect(region).to_be_visible()
        links = region.get_by_role('link', name='浏览器修复凭证', exact=True)
        expect(links).to_have_count(2)
        hrefs = links.evaluate_all('(nodes) => nodes.map(node => node.getAttribute("href")).sort()')
        runner.check(hrefs == ['#/browser-import?account_id=101&site_id=1', '#/browser-import?account_id=102&site_id=1'],
                     'Browser recovery must stay tied to the actual failed account')
        runner.record('integration-task-recovery', links=2, other_results_without_links=3)

    runner.case('integration-task-account-recovery', task_recovery, path='/accounts')


def all_cases(runner):
    route_cases(runner)
    file_cases(runner)
    preview_cases(runner)
    race_cases(runner)
    apply_cases(runner)
    secret_cases(runner)
    helper_cases(runner)
    responsive_cases(runner)
    keyboard_cases(runner)
    resource_cases(runner)
    integration_cases(runner)


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument('--root', type=Path, default=ROOT)
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--case', action='append', default=[])
    return parser.parse_args()


def main():
    args = parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    root = args.root.resolve()
    server = ThreadingHTTPServer(('127.0.0.1', 0), partial(StaticHandler, root=root))
    server.daemon_threads = True
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    helper_server = ThreadingHTTPServer(('127.0.0.1', 0), HelperHandler)
    helper_server.daemon_threads = True
    helper_thread = Thread(target=helper_server.serve_forever, daemon=True)
    helper_thread.start()
    base = f'http://127.0.0.1:{server.server_port}'
    helper_base = f'http://127.0.0.1:{helper_server.server_port}'
    runner = None
    fatal_error = ''
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(
                headless=True,
                args=['--no-proxy-server', '--disable-background-networking'],
            )
            try:
                runner = BrowserImportRunner(browser, base, args.output, root, helper_base, args.case)
                all_cases(runner)
            finally:
                browser.close()
    except Exception as error:
        fatal_error = f'{type(error).__name__}: {error}'
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
        helper_server.shutdown()
        helper_server.server_close()
        helper_thread.join(timeout=5)

    checks = runner.results if runner else []
    events = runner.events if runner else []
    server_closed = not thread.is_alive() and not helper_thread.is_alive()
    result = {
        'pass': bool(checks) and all(item['pass'] for item in checks) and not events
                and server_closed and not fatal_error,
        'checks': checks,
        'evidence': runner.evidence if runner else [],
        'metrics': runner.metrics if runner else {},
        'server_closed': server_closed,
        'unexpected_events': events,
        'fatal_error': fatal_error,
    }
    result_path = args.output / 'result.json'
    result_path.write_text(
        json.dumps(result, ensure_ascii=False, indent=2) + '\n',
        encoding='utf-8',
    )
    print(json.dumps({
        'pass': result['pass'],
        'passed': sum(item['pass'] for item in checks),
        'total': len(checks),
        'events': len(events),
        'server_closed': server_closed,
        'result': str(result_path),
    }, ensure_ascii=False))
    for item in checks:
        if not item['pass']:
            print(item['name'] + ': ' + item['error'].splitlines()[0])
    if fatal_error:
        print(fatal_error)
    return 0 if result['pass'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
