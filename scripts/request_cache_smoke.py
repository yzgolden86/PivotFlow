"""Exercise the built PivotFlow request-cache console flow.

The harness serves only the owned built console, intercepts synthetic admin
traffic in Chromium, and never starts a gateway, database, upstream, Vite, or
frontend development server. Screenshots are viewport JPEGs.
"""

import argparse
import copy
import gzip
import json
import time
from functools import partial
from http.server import ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import parse_qs, urlsplit

from playwright.sync_api import expect, sync_playwright

import docs_screenshots as fixtures
from console_visual_regression import SETTINGS
from ui_accessibility_smoke import ROOT, Runner, StaticHandler, SyntheticData


SETTINGS_URL = '/system?section=runtime&group=cache'
HISTORY_URL = '/logs?view=requests'
SYNTHETIC_ACCESS_TOKEN = 'synthetic-request-cache-admin-only'
SYNTHETIC_RESOURCE_SENTINEL = 'synthetic-request-cache-resource-payload-do-not-persist'
SYNTHETIC_SECRET_SENTINEL = 'synthetic-request-cache-hidden-secret-do-not-persist'
SESSION_ENCODED_BUDGET = 400_000
SESSION_RESOURCE_BUDGET = 45
PAGE_ROUTE_PREFIXES = (
    'DashboardPage-',
    'ChannelsPage-',
    'ModelRoutesPage-',
    'BrowserImportPage-',
    'LogsPage-',
    'StatsPage-',
    'HealthPage-',
    'ModelTestPage-',
    'SitesPage-',
    'AccountsPageV2-',
    'CheckinsPage-',
    'AnnouncementsPage-',
    'TokensPage-',
    'ConnectPage-',
    'SystemSettingsPageV2-',
)

REQUEST_CACHE_REASONS = (
    'disabled',
    'not_opted_in',
    'client_bypass',
    'invalid_mode',
    'request_no_store',
    'request_no_cache',
    'request_query',
    'request_encoding',
    'streaming',
    'unsupported_endpoint',
    'nondeterministic',
    'tools_or_state',
    'unsupported_request',
    'no_identity',
    'route_unavailable',
    'channel_changed',
    'custom_rules',
    'protocol_transform',
    'empty',
    'expired',
    'client_refresh',
    'unsafe_response',
    'response_no_store',
    'response_too_large',
    'capture_budget',
    'invalidated',
    'upstream_error',
    'canceled',
    'write_failed',
    'reused',
    'stored',
)

REQUEST_CACHE_INVALIDATION_REASONS = (
    'manual_clear',
    'routing_changed',
    'keys_changed',
    'aliases_changed',
    'permissions_changed',
)

CASE_NAMES = (
    'settings-navigation-and-copy',
    'snapshot-refresh-failure-recovery',
    'malformed-snapshot-recovery',
    'read-only-and-storage-audit',
    'clear-cancel',
    'clear-confirmed-success',
    'clear-failure-unknown-recovery',
    'clear-timeout-unknown',
    'unmount-aborts-stale-read',
    'history-cache-diagnostics',
    'responsive-320x900',
    'responsive-390x844',
    'responsive-768x1024',
    'responsive-1440x900',
    'responsive-short-390x480',
    'resource-budgets',
)


def fixed_counts(keys):
    return {key: 0 for key in keys}


def cache_observation(
    *,
    observed_at=fixtures.NOW,
    process_started_at=fixtures.NOW - 3_600_000,
    enabled=False,
    ttl_seconds=60,
    max_entries=256,
    max_bytes=33_554_432,
    max_entry_bytes=1_048_576,
    stats=None,
    invalidation_counts=None,
    reason_counts=None,
    last_invalidation_reason='routing_changed',
    last_invalidation_at=fixtures.NOW - 1_000,
):
    invalidations = fixed_counts(REQUEST_CACHE_INVALIDATION_REASONS)
    reasons = fixed_counts(REQUEST_CACHE_REASONS)
    if invalidation_counts:
        invalidations.update(invalidation_counts)
    if reason_counts:
        reasons.update(reason_counts)
    observation_stats = {
        'entries': 3,
        'bytes': 1_024,
        'expired_entries': 1,
        'captures': 0,
        'capture_bytes': 0,
        'lookups': 10,
        'hits': 6,
        'misses': 4,
        'refreshes': 1,
        'bypasses': 8,
        'stored': 4,
        'expired': 1,
        'evicted': 0,
        'rejected': 0,
        'clears': 0,
        'invalidations': invalidations,
        'reasons': reasons,
        'last_invalidation_reason': last_invalidation_reason,
        'last_invalidation_at': last_invalidation_at,
    }
    if stats:
        observation_stats.update(stats)
    return {
        'version': 1,
        'observed_at': observed_at,
        'process_started_at': process_started_at,
        'config': {
            'enabled': enabled,
            'ttl_seconds': ttl_seconds,
            'max_entries': max_entries,
            'max_bytes': max_bytes,
            'max_entry_bytes': max_entry_bytes,
        },
        'stats': observation_stats,
    }


def cleared_observation(observation):
    cleared = copy.deepcopy(observation)
    cleared['observed_at'] = observation['observed_at'] + 1_000
    cleared['stats'].update({
        'entries': 0,
        'bytes': 0,
        'expired_entries': 0,
        'captures': 0,
        'capture_bytes': 0,
        'clears': observation['stats']['clears'] + 1,
        'last_invalidation_reason': 'manual_clear',
        'last_invalidation_at': cleared['observed_at'],
    })
    cleared['stats']['invalidations']['manual_clear'] += 1
    return cleared


def system_setting(key, value, kind, default, runtime_effect):
    return {
        'key': key,
        'value': value,
        'value_type': kind,
        'description': key,
        'default_value': default,
        'updated_at': fixtures.NOW,
        'editable': True,
        'runtime_effect': runtime_effect,
        'requires_restart': True,
    }


def request_cache_settings():
    settings = [
        copy.deepcopy(item)
        for item in SETTINGS
        if not item['key'].startswith('request_cache_')
    ]
    settings.extend([
        system_setting(
            'request_cache_enabled',
            'false',
            'bool',
            'false',
            'enables cache lookups and stores after the next process start',
        ),
        system_setting(
            'request_cache_ttl_seconds',
            '60',
            'int',
            '60',
            'changes entry expiration after the next process start',
        ),
        system_setting(
            'request_cache_max_entries',
            '256',
            'int',
            '256',
            'changes the resident entry limit after the next process start',
        ),
        system_setting(
            'request_cache_max_bytes',
            '33554432',
            'int',
            '33554432',
            (
                SYNTHETIC_RESOURCE_SENTINEL
                + ' changes total response-body capacity after the next process start'
            ),
        ),
        system_setting(
            'request_cache_max_entry_bytes',
            '1048576',
            'int',
            '1048576',
            SYNTHETIC_SECRET_SENTINEL + ' next process start',
        ),
    ])
    return settings


def trace_attempt(*, number, started_at, outcome='success', status_code=200):
    return {
        'number': number,
        'started_at': started_at,
        'duration': 0.42,
        'first_byte_time': 0.18,
        'channel_id': 101,
        'channel_name': '合成缓存路由',
        'key_index': 0,
        'url_index': 0,
        'endpoint': 'https://synthetic.example.invalid/v1/messages',
        'actual_model': 'synthetic-upstream-model',
        'upstream_protocol': 'anthropic',
        'transport': 'http',
        'status_code': status_code,
        'outcome': outcome,
        'upstream': True,
        'response_committed': outcome == 'success',
        'reason': '',
        'retry': 'none',
        'cooldown': 'none',
        'cooldown_until': 0,
    }


def request_cache_traces():
    base = {
        'auth_token_id': 1,
        'client_protocol': 'anthropic',
        'transport': 'http',
        'is_streaming': False,
        'final_status': 200,
        'outcome': 'success',
        'duration': 0.42,
        'wait_duration': 0,
        'truncated': False,
    }
    hit = {
        **base,
        'request_id': 'cache-hit-synthetic',
        'started_at': fixtures.NOW - 30_000,
        'model': SYNTHETIC_RESOURCE_SENTINEL,
        'attempt_count': 0,
        'upstream_attempt_count': 0,
        'upstream_success_count': 0,
        'attempts': [],
        'cache_status': 'hit',
        'cache_reason': 'reused',
    }
    expired = {
        **base,
        'request_id': 'cache-expired-synthetic',
        'started_at': fixtures.NOW - 40_000,
        'model': 'synthetic-cache-expired-model',
        'attempt_count': 1,
        'upstream_attempt_count': 1,
        'upstream_success_count': 1,
        'attempts': [
            trace_attempt(number=1, started_at=fixtures.NOW - 39_900),
        ],
        'cache_status': 'miss',
        'cache_reason': 'expired',
    }
    old = {
        **base,
        'request_id': 'cache-old-without-fields',
        'started_at': fixtures.NOW - 50_000,
        'model': 'synthetic-old-request-model',
        'attempt_count': 1,
        'upstream_attempt_count': 1,
        'upstream_success_count': 1,
    }
    return [hit, expired, old]


class RequestCacheData(SyntheticData):
    def __init__(self):
        super().__init__()
        self.calls = []
        self.http_calls = []
        self.settings = request_cache_settings()
        self.observation = cache_observation()
        self.read_count = 0
        self.delete_count = 0
        self.fail_reads = 0
        self.malformed_reads = 0
        self.hold_reads = 0
        self.pending_reads = []
        self.fail_clears = 0
        self.hold_clears = 0
        self.pending_clears = []
        self.expected_console_failures = []
        self.traces = request_cache_traces()

    def fulfill(self, route, data, count=None):
        route.fulfill(
            status=200,
            content_type='application/json',
            body=json.dumps(fixtures.envelope(data, count), ensure_ascii=False),
        )

    def fulfill_failure(self, route, error, status=503):
        if status == 503 and error in (
            'synthetic_request_cache_read_failed', 'synthetic_request_cache_clear_failed',
        ):
            self.expected_console_failures.append({'url': route.request.url, 'status': status})
        route.fulfill(
            status=status,
            content_type='application/json',
            body=json.dumps({'success': False, 'error': error}, ensure_ascii=False),
        )

    def route(self, route):
        parsed = urlsplit(route.request.url)
        path = parsed.path
        query = parse_qs(parsed.query)
        self.http_calls.append({
            'method': route.request.method,
            'path': path,
            'query': query,
            'url': route.request.url,
            'body': route.request.post_data,
        })

        if path == '/admin/request-cache':
            if route.request.method == 'GET':
                self.handle_read(route)
                return
            if route.request.method == 'DELETE':
                self.handle_clear(route)
                return
            self.fulfill_failure(route, 'method_not_allowed', 405)
            return

        if path == '/admin/settings':
            if route.request.method == 'GET':
                self.fulfill(route, copy.deepcopy(self.settings))
            else:
                self.fulfill_failure(route, 'method_not_allowed', 405)
            return

        if path == '/admin/request-traces':
            self.handle_trace_list(route, query)
            return
        if path == '/admin/request-traces/stats':
            self.handle_trace_stats(route, query)
            return
        if path.startswith('/admin/request-traces/'):
            self.handle_trace_detail(route, path)
            return

        super().route(route)

    def handle_read(self, route):
        self.read_count += 1
        if self.hold_reads:
            self.hold_reads -= 1
            self.pending_reads.append(route)
            return
        if self.fail_reads:
            self.fail_reads -= 1
            self.fulfill_failure(route, 'synthetic_request_cache_read_failed')
            return
        if self.malformed_reads:
            self.malformed_reads -= 1
            self.fulfill(route, {'version': 1})
            return
        self.fulfill(route, copy.deepcopy(self.observation))

    def handle_clear(self, route):
        self.delete_count += 1
        if self.fail_clears:
            self.fail_clears -= 1
            self.fulfill_failure(route, 'synthetic_request_cache_clear_failed')
            return
        if self.hold_clears:
            self.hold_clears -= 1
            self.pending_clears.append(route)
            return
        self.observation = cleared_observation(self.observation)
        self.fulfill(route, copy.deepcopy(self.observation))

    def filtered_traces(self, query):
        traces = list(self.traces)
        if query.get('outcome'):
            traces = [
                trace for trace in traces
                if trace['outcome'] == query['outcome'][-1]
            ]
        if query.get('auth_token_id'):
            token_id = int(query['auth_token_id'][-1])
            traces = [
                trace for trace in traces
                if trace['auth_token_id'] == token_id
            ]
        if query.get('client_protocol'):
            protocol = query['client_protocol'][-1]
            traces = [
                trace for trace in traces
                if trace['client_protocol'] == protocol
            ]
        if query.get('search'):
            needle = query['search'][-1].lower()
            traces = [
                trace for trace in traces
                if needle in trace['request_id'].lower()
                or needle in trace.get('model', '').lower()
            ]
        if query.get('model'):
            model = query['model'][-1]
            traces = [trace for trace in traces if trace.get('model', '') == model]
        return traces

    def trace_stats(self, traces):
        return {
            'requests': len(traces),
            'successes': sum(trace['outcome'] == 'success' for trace in traces),
            'errors': sum(trace['outcome'] == 'error' for trace in traces),
            'canceled': sum(trace['outcome'] == 'canceled' for trace in traces),
            'upstream_attempts': sum(trace['upstream_attempt_count'] for trace in traces),
            'upstream_successes': sum(trace['upstream_success_count'] for trace in traces),
        }

    def handle_trace_list(self, route, query):
        traces = self.filtered_traces(query)
        offset = int(query.get('offset', ['0'])[-1])
        limit = int(query.get('limit', ['20'])[-1])
        self.fulfill(route, traces[offset:offset + limit], len(traces))

    def handle_trace_stats(self, route, query):
        self.fulfill(route, self.trace_stats(self.filtered_traces(query)))

    def handle_trace_detail(self, route, path):
        request_id = path.rsplit('/', 1)[-1]
        detail = next(
            (trace for trace in self.traces if trace['request_id'] == request_id),
            None,
        )
        if detail is None:
            self.fulfill_failure(route, 'not_found', 404)
            return
        self.fulfill(route, copy.deepcopy(detail))


def wait_until(page, predicate, message, timeout=5):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        page.wait_for_timeout(50)
    raise AssertionError(message)


def visible_viewport_jpeg(runner, page, name):
    path = runner.output / f'{name}.jpg'
    page.screenshot(
        path=str(path),
        type='jpeg',
        quality=82,
        animations='disabled',
        full_page=False,
    )
    runner.check(path.read_bytes()[:2] == b'\xff\xd8', f'{name} is not a JPEG')
    runner.record(name, screenshot=str(path), bytes=path.stat().st_size)


def cache_config_values(page):
    values = page.locator('.request-cache-config dl > div')
    return {
        label: values.filter(has_text=label).first.locator('dd').inner_text().strip()
        for label in ('有效期', '条目上限', '正文总容量', '单条响应上限', '客户端选择')
    }


def assert_number_range(runner, page, label, minimum, maximum):
    control = page.get_by_label(label, exact=True)
    expect(control).to_have_attribute('type', 'number')
    expect(control).to_have_attribute('min', str(minimum))
    expect(control).to_have_attribute('max', str(maximum))
    runner.record(
        f'range-{label}',
        minimum=minimum,
        maximum=maximum,
    )


def click_setting_group(page, label):
    page.locator('.settings-groups').get_by_role(
        'button',
        name=label,
        exact=False,
    ).click()


class RequestCacheRunner(Runner):
    def __init__(self, browser, base, output, root, case_names=None):
        super().__init__(browser, base, output, case_names)
        self.root = root
        self.evidence = []
        self.current_page = None

    def record(self, name, **values):
        self.evidence.append({'case': name, **values})

    def case(
        self,
        name,
        callback,
        *,
        path=SETTINGS_URL,
        width=1440,
        height=900,
        theme='light',
        expected_cache_deletes=0,
        expected_cache_failures=0,
    ):
        if self.case_names and name not in self.case_names:
            return

        context = self.browser.new_context(
            viewport={'width': width, 'height': height},
            color_scheme=theme,
            reduced_motion='reduce',
        )
        context.set_default_timeout(5_000)
        context.set_default_navigation_timeout(30_000)
        context.add_init_script(
            f"localStorage.setItem('pivotflow_token', {json.dumps(SYNTHETIC_ACCESS_TOKEN)});"
            "localStorage.setItem('pivotflow_web_role', 'admin');"
        )
        context.add_init_script(
            f"localStorage.setItem('pivotflow_theme', {json.dumps(theme)});"
        )

        data = RequestCacheData()
        page = context.new_page()
        self.current_page = page
        failure_budget = {'remaining': expected_cache_failures}
        origin = urlsplit(self.base).netloc

        page.on(
            'pageerror',
            lambda error: self.events.append({
                'case': name,
                'pageerror': str(error)[:1_000],
            }),
        )

        def console_event(message):
            if message.type not in ('error', 'warning'):
                return
            location = message.location
            for failure in data.expected_console_failures:
                if (message.type == 'error'
                    and location.get('url') == failure['url']
                    and message.text.startswith(
                        f"Failed to load resource: the server responded with a status of {failure['status']} "
                    )):
                    data.expected_console_failures.remove(failure)
                    self.record(name, expected_http_failure=failure)
                    return
            self.events.append({
                'case': name,
                'console': {
                    'type': message.type,
                    'text': message.text[:1_000],
                    'location': message.location,
                },
            })

        page.on('console', console_event)

        def request_failed(request):
            parsed = urlsplit(request.url)
            if (
                parsed.path == '/admin/request-cache'
                and failure_budget['remaining'] > 0
            ):
                failure_budget['remaining'] -= 1
                return
            self.events.append({
                'case': name,
                'requestfailed': {
                    'method': request.method,
                    'url': request.url,
                    'failure': request.failure,
                },
            })

        page.on('requestfailed', request_failed)

        def intercept(route):
            parsed = urlsplit(route.request.url)
            if parsed.netloc != origin:
                self.events.append({
                    'case': name,
                    'external_blocked': parsed.netloc,
                    'url': route.request.url,
                })
                route.abort('blockedbyclient')
                return
            if parsed.path.startswith(('/admin/', '/dashboard/')):
                try:
                    data.route(route)
                except Exception as error:
                    self.events.append({
                        'case': name,
                        'intercept_error': {
                            'path': parsed.path,
                            'error': str(error)[:1_000],
                        },
                    })
                    route.abort('failed')
                return
            if parsed.path.startswith('/web/'):
                route.continue_()
                return
            self.events.append({
                'case': name,
                'unexpected_same_origin_request': route.request.url,
            })
            route.abort('blockedbyclient')

        context.route('**/*', intercept)
        started = time.perf_counter()
        try:
            page.goto(f'{self.base}/web/console/#{path}', wait_until='networkidle')
            expect(page.locator('h1')).to_be_visible()
            callback(page, data)

            writes = [
                call for call in data.http_calls
                if call['method'] != 'GET'
            ]
            cache_deletes = [
                call for call in writes
                if call['method'] == 'DELETE'
                and call['path'] == '/admin/request-cache'
            ]
            self.check(
                len(cache_deletes) == expected_cache_deletes,
                (
                    f'Expected {expected_cache_deletes} confirmed cache clear '
                    f'request(s), observed {len(cache_deletes)}'
                ),
            )
            self.check(
                len(writes) == len(cache_deletes),
                f'Unexpected non-GET admin request(s): {writes[:4]}',
            )
            self.check(
                all(call['body'] is None for call in cache_deletes),
                'Confirmed cache clear must not send a mutation body',
            )

            storage = page.evaluate("""sentinels => ({
              localStorage: Object.fromEntries(
                Object.keys(localStorage).map(key => [key, localStorage.getItem(key) || ''])
              ),
              sessionStorage: Object.fromEntries(
                Object.keys(sessionStorage).map(key => [key, sessionStorage.getItem(key) || ''])
              ),
              cookies: document.cookie,
            })""", [SYNTHETIC_RESOURCE_SENTINEL, SYNTHETIC_SECRET_SENTINEL])
            storage_blob = json.dumps(storage, ensure_ascii=False)
            for sentinel in (SYNTHETIC_RESOURCE_SENTINEL, SYNTHETIC_SECRET_SENTINEL):
                self.check(
                    sentinel not in storage_blob,
                    f'Synthetic secret sentinel leaked into browser storage: {sentinel}',
                )

            evidence = {
                'admin_calls': len(data.http_calls),
                'read_count': data.read_count,
                'delete_count': data.delete_count,
                'storage_keys': {
                    'local': sorted(storage['localStorage']),
                    'session': sorted(storage['sessionStorage']),
                },
            }
            self.record(name, **evidence)
            self.results.append({
                'name': name,
                'pass': True,
                'seconds': round(time.perf_counter() - started, 3),
                **evidence,
            })
        except Exception as error:
            try:
                layout = page.evaluate("""() => ({
                  path: location.hash,
                  viewport: {width: innerWidth, height: innerHeight},
                  scroll_width: document.documentElement.scrollWidth,
                  scroll_top: document.scrollingElement?.scrollTop || 0,
                  cache_rects: [...document.querySelectorAll('.request-cache-panel, .request-cache-panel button')]
                    .map(element => ({
                      label: element.tagName === 'BUTTON' ? element.textContent.trim() : element.className,
                      top: element.getBoundingClientRect().top,
                      bottom: element.getBoundingClientRect().bottom,
                      height: element.getBoundingClientRect().height,
                    })),
                  overflow: [...document.querySelectorAll('main *')]
                    .map(element => ({
                      element,
                      rect: element.getBoundingClientRect(),
                    }))
                    .filter(({rect}) => rect.width && (
                      rect.right > innerWidth + 1 || rect.left < -1
                    ))
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
                })""")
                visible_viewport_jpeg(self, page, f'{name}-failed')
            except Exception:
                layout = {}
            self.results.append({
                'name': name,
                'pass': False,
                'error': str(error)[:1_800],
                'seconds': round(time.perf_counter() - started, 3),
                'layout': layout,
            })
            self.record(
                name,
                failed=True,
                calls=data.http_calls[-20:],
                read_count=data.read_count,
                delete_count=data.delete_count,
            )
        finally:
            for pending in data.pending_reads + data.pending_clears:
                try:
                    pending.abort('aborted')
                except Exception:
                    pass
            context.unroute_all(behavior='ignoreErrors')
            context.close()


def setting_cases(runner):
    def navigation_and_copy(page, data):
        expect(page.locator('h1')).to_have_text('系统设置')
        cache_group = page.locator('.settings-groups').get_by_role(
            'button',
            name='请求缓存',
            exact=False,
        )
        expect(cache_group).to_have_attribute('aria-pressed', 'true')
        expect(page.locator('.settings-editor-heading h2')).to_have_text('请求缓存')
        expect(page.locator('.request-cache-panel')).to_be_visible()

        switch = page.get_by_role('switch', name='启用请求缓存', exact=True)
        expect(switch).to_have_attribute('aria-checked', 'false')
        expect(page.locator('.request-cache-panel')).to_contain_text('当前默认关闭')
        expect(page.locator('.request-cache-contract')).to_contain_text(
            '默认关闭，客户端必须显式选择。'
        )
        expect(page.locator('.request-cache-contract')).to_contain_text('temperature=0')
        expect(page.locator('.request-cache-contract')).to_contain_text('不新增上游用量或费用')

        help_trigger = page.get_by_role(
            'button',
            name='启用请求缓存 的说明',
            exact=True,
        )
        help_trigger.click()
        help = page.get_by_role('tooltip')
        expect(help).to_contain_text('默认关闭')
        expect(help).to_contain_text('X-PivotFlow-Cache: use')
        expect(help).to_contain_text('refresh')
        expect(help).to_contain_text('bypass')
        expect(help).to_contain_text('重启生效')

        assert_number_range(runner, page, '缓存有效期', 5, 3_600)
        assert_number_range(runner, page, '最大缓存条目', 1, 4_096)
        assert_number_range(runner, page, '缓存正文总容量', 1_048_576, 134_217_728)
        assert_number_range(runner, page, '单条响应容量', 4_096, 1_048_576)

        initial_values = cache_config_values(page)
        runner.check(initial_values['有效期'] == '60 秒', 'Current TTL was not 60 seconds')
        runner.check(initial_values['条目上限'] == '256 条', 'Current entry limit was not 256')
        runner.check(initial_values['正文总容量'] == '32 MiB', 'Current byte capacity was not 32 MiB')
        runner.check(initial_values['单条响应上限'] == '1 MiB', 'Current entry capacity was not 1 MiB')
        runner.check(initial_values['客户端选择'] == '全部请求绕过', 'Default-off selection copy was missing')

        page.get_by_label('缓存有效期', exact=True).fill('90')
        expect(page.locator('.request-cache-distinction')).to_contain_text(
            '上方有 1 项未保存修改。'
        )
        expect(page.locator('footer[aria-label="未保存的设置"]')).to_contain_text(
            '1 项未保存'
        )
        draft_values = cache_config_values(page)
        runner.check(
            draft_values['有效期'] == '60 秒',
            'An unsaved draft must not replace the current backend snapshot',
        )
        runner.check(data.read_count == 1, 'Editing a setting triggered an unnecessary snapshot read')

        click_setting_group(page, '路由策略')
        expect(page.get_by_role('heading', name='路由策略', exact=True)).to_be_visible()
        click_setting_group(page, '请求缓存')
        expect(page.get_by_role('heading', name='请求缓存', exact=True)).to_be_visible()
        expect(page.locator('.request-cache-panel')).to_be_visible()
        runner.record(
            'settings-navigation',
            url='#/system?section=runtime&group=cache',
            supported_ranges={
                'ttl_seconds': [5, 3_600],
                'max_entries': [1, 4_096],
                'max_bytes': [1_048_576, 134_217_728],
                'max_entry_bytes': [4_096, 1_048_576],
            },
        )

    runner.case('settings-navigation-and-copy', navigation_and_copy)

    def refresh_failure_recovery(page, data):
        panel = page.locator('.request-cache-panel')
        expect(panel).to_contain_text('当前默认关闭')
        data.fail_reads = 1
        page.get_by_role('button', name='刷新观察', exact=True).click()
        alert = panel.get_by_role('alert')
        expect(alert).to_contain_text('刷新失败')
        expect(panel).to_contain_text('下方仍是上次成功读取的快照')
        readout = panel.locator('.request-cache-readout')
        expect(readout).to_contain_text('上次观察时已关闭')
        expect(readout).not_to_contain_text('当前生效配置')
        expect(readout).not_to_contain_text('当前默认关闭')

        data.observation = cache_observation(ttl_seconds=75)
        retry = alert.get_by_role('button', name='刷新确认', exact=True)
        retry.focus()
        expect(retry).to_be_focused()
        retry.press('Enter')
        expect(alert).to_have_count(0)
        expect(page.get_by_role('heading', name='请求缓存运行状态', exact=True)).to_be_focused()
        expect(page.get_by_role('button', name='刷新观察', exact=True)).to_be_enabled()
        page.keyboard.press('Tab')
        expect(page.get_by_role('button', name='刷新观察', exact=True)).to_be_focused()
        expect(panel).not_to_contain_text('下方仍是上次成功读取的快照')
        runner.check(
            cache_config_values(page)['有效期'] == '75 秒',
            'Successful retry did not replace the stale snapshot',
        )
        runner.check(data.read_count == 3, 'Snapshot retry count was not bounded to three reads')
        runner.record('snapshot-recovery', reads=data.read_count, ttl_seconds=75)

    runner.case('snapshot-refresh-failure-recovery', refresh_failure_recovery)

    def malformed_recovery(page, data):
        data.malformed_reads = 1
        page.reload(wait_until='networkidle')
        panel = page.locator('.request-cache-panel')
        alert = panel.get_by_role('alert')
        expect(alert).to_contain_text('读取请求缓存状态失败')
        expect(panel).to_contain_text('当前状态未知')
        expect(panel).to_contain_text('请求缓存数据')

        alert.get_by_role('button', name='重新读取', exact=True).click()
        expect(panel.get_by_role('alert')).to_have_count(0)
        expect(panel).to_contain_text('当前默认关闭')
        runner.check(data.read_count == 3, 'Malformed snapshot retry count was not bounded')
        runner.record('malformed-dto-recovery', reads=data.read_count)

    runner.case('malformed-snapshot-recovery', malformed_recovery)

    def read_only_storage(page, data):
        click_setting_group(page, '路由策略')
        click_setting_group(page, '请求缓存')
        expect(page.locator('.request-cache-panel')).to_be_visible()
        runner.check(data.delete_count == 0, 'Read-only navigation issued a cache clear')
        runner.check(data.read_count == 2, 'Read-only remount did not refresh the snapshot exactly once')
        runner.check(
            all(call['method'] == 'GET' for call in data.http_calls),
            'Read-only setting inspection issued a mutation',
        )
        runner.record(
            'read-only-storage',
            methods=[call['method'] for call in data.http_calls],
            secret_sentinels_checked=2,
        )

    runner.case('read-only-and-storage-audit', read_only_storage)


def clear_cases(runner):
    def cancel_clear(page, data):
        dialog_messages = []

        def dismiss_dialog(dialog):
            dialog_messages.append(dialog.message)
            dialog.dismiss()

        page.once('dialog', dismiss_dialog)
        page.get_by_role('button', name='清空缓存', exact=True).click()
        expect(page.locator('.request-cache-panel')).to_contain_text('当前默认关闭')
        runner.check(
            dialog_messages == [
                '清空当前进程中的全部请求缓存条目？进程累计计数会保留，此操作不会自动重试。'
            ],
            'Clear confirmation copy changed',
        )
        runner.check(data.delete_count == 0, 'Canceling the confirmation issued DELETE')
        runner.record('clear-cancel', dialogs=dialog_messages, deletes=data.delete_count)

    runner.case('clear-cancel', cancel_clear)

    def confirmed_clear(page, data):
        dialog_messages = []

        def accept_dialog(dialog):
            dialog_messages.append(dialog.message)
            dialog.accept()

        page.once('dialog', accept_dialog)
        page.get_by_role('button', name='清空缓存', exact=True).click()
        panel = page.locator('.request-cache-panel')
        expect(panel.get_by_role('status')).to_contain_text(
            '请求缓存已清空；进程累计计数保留。'
        )
        expect(panel).to_contain_text('其中过期但仍驻留 0 条')
        reads_after_clear = data.read_count
        page.wait_for_timeout(600)
        runner.check(data.delete_count == 1, 'Confirmed clear must issue exactly one DELETE')
        runner.check(data.read_count == reads_after_clear, 'Confirmed clear retried a snapshot read automatically')
        runner.check(dialog_messages, 'Confirmed clear did not show a confirmation')

        click_setting_group(page, '路由策略')
        click_setting_group(page, '请求缓存')
        expect(page.locator('.request-cache-panel')).to_contain_text(
            '其中过期但仍驻留 0 条'
        )
        runner.check(data.delete_count == 1, 'Remounting after clear repeated the DELETE')
        runner.record(
            'clear-confirmed-success',
            deletes=data.delete_count,
            reads=data.read_count,
            pre_clear_values_resurrected=False,
        )

    runner.case(
        'clear-confirmed-success',
        confirmed_clear,
        expected_cache_deletes=1,
    )

    def failed_clear(page, data):
        data.fail_clears = 1
        page.once('dialog', lambda dialog: dialog.accept())
        page.get_by_role('button', name='清空缓存', exact=True).click()
        panel = page.locator('.request-cache-panel')
        alert = panel.get_by_role('alert')
        expect(alert).to_contain_text('清理请求失败，结果未知')
        expect(alert).to_contain_text('系统未自动重试')
        expect(panel).to_contain_text('下方仍是上次成功读取的快照')
        page.wait_for_timeout(600)
        runner.check(data.delete_count == 1, 'Clear failure was retried automatically')
        runner.check(data.read_count == 1, 'Clear failure triggered an automatic snapshot read')

        alert.get_by_role('button', name='刷新确认', exact=True).click()
        expect(panel.get_by_role('alert')).to_have_count(0)
        expect(panel).not_to_contain_text('下方仍是上次成功读取的快照')
        runner.check(data.delete_count == 1, 'Manual refresh retried the failed DELETE')
        runner.record(
            'clear-failure-recovery',
            deletes=data.delete_count,
            reads=data.read_count,
        )

    runner.case(
        'clear-failure-unknown-recovery',
        failed_clear,
        expected_cache_deletes=1,
    )

    def timeout_clear(page, data):
        data.hold_clears = 1
        page.once('dialog', lambda dialog: dialog.accept())
        page.get_by_role('button', name='清空缓存', exact=True).click()
        expect(page.get_by_role('button', name='正在清空', exact=True)).to_be_visible()
        panel = page.locator('.request-cache-panel')
        alert = panel.get_by_role('alert')
        expect(alert).to_contain_text(
            '清理请求超时，结果未知；请刷新确认。系统未自动重试。',
            timeout=9_500,
        )
        expect(panel).to_contain_text('下方仍是上次成功读取的快照')
        runner.check(data.delete_count == 1, 'Timed-out clear sent more than one DELETE')
        runner.check(data.read_count == 1, 'Timed-out clear issued an automatic read')
        runner.record(
            'clear-timeout-unknown',
            product_timeout_ms=8_000,
            deletes=data.delete_count,
            reads=data.read_count,
            stale_snapshot_retained=True,
        )

    runner.case(
        'clear-timeout-unknown',
        timeout_clear,
        expected_cache_deletes=1,
        expected_cache_failures=1,
    )

    def unmount_stale_read(page, data):
        old_observation = copy.deepcopy(data.observation)
        data.hold_reads = 1
        page.get_by_role('button', name='刷新观察', exact=True).click()
        wait_until(
            page,
            lambda: len(data.pending_reads) == 1,
            'Refresh request was not held for the stale-response test',
        )
        click_setting_group(page, '路由策略')
        data.observation = cache_observation(
            ttl_seconds=77,
            max_entries=128,
            max_bytes=16_777_216,
        )
        try:
            data.pending_reads[0].fulfill(
                status=200,
                content_type='application/json',
                body=json.dumps(
                    fixtures.envelope(old_observation),
                    ensure_ascii=False,
                ),
            )
        except Exception:
            pass
        data.pending_reads.clear()
        click_setting_group(page, '请求缓存')
        panel = page.locator('.request-cache-panel')
        expect(panel).to_contain_text('当前默认关闭')
        values = cache_config_values(page)
        runner.check(values['有效期'] == '77 秒', 'Aborted old read replaced the new snapshot')
        runner.check(values['条目上限'] == '128 条', 'New snapshot entry limit was lost')
        runner.check(data.read_count == 3, 'Unmount stale-read scenario issued unexpected reads')
        runner.record(
            'unmount-stale-read',
            reads=data.read_count,
            retained_ttl_seconds=77,
        )

    runner.case(
        'unmount-aborts-stale-read',
        unmount_stale_read,
        expected_cache_failures=1,
    )


def history_cases(runner):
    def cache_diagnostics(page, data):
        hit_row = page.get_by_role(
            'button',
            name='查看请求 cache-hit-synthetic 详情',
            exact=True,
        )
        expect(hit_row).to_be_visible()
        expect(hit_row).to_contain_text('缓存命中')
        expect(hit_row).to_contain_text('0 次尝试 · 上游 0 次')

        expired_row = page.get_by_role(
            'button',
            name='查看请求 cache-expired-synthetic 详情',
            exact=True,
        )
        expect(expired_row).to_contain_text('缓存未命中')

        old_row = page.get_by_role(
            'button',
            name='查看请求 cache-old-without-fields 详情',
            exact=True,
        )
        expect(old_row).to_be_visible()
        runner.check(
            old_row.locator('.request-cache-badge').count() == 0,
            'Old request without cache fields invented a cache badge',
        )

        hit_row.click()
        hit_dialog = page.get_by_role(
            'dialog',
            name='完整请求 cache-hit-synthetic',
            exact=True,
        )
        expect(hit_dialog).to_be_visible()
        expect(hit_dialog).to_contain_text('缓存命中 · 已复用缓存响应')
        expect(hit_dialog).to_contain_text(
            '命中缓存，本次未调用上游，不新增上游用量或费用。'
        )
        expect(hit_dialog).to_contain_text('0 次尝试 · 0 次已发上游 · 0 次上游成功')
        expect(hit_dialog).to_contain_text('缓存命中，未执行上游尝试')
        expect(hit_dialog).to_contain_text(SYNTHETIC_RESOURCE_SENTINEL)

        page.goto(
            f'{runner.base}/web/console/#{HISTORY_URL}&request=cache-expired-synthetic',
            wait_until='networkidle',
        )
        expired_dialog = page.get_by_role(
            'dialog',
            name='完整请求 cache-expired-synthetic',
            exact=True,
        )
        expect(expired_dialog).to_be_visible()
        expect(expired_dialog).to_contain_text('缓存未命中 · 缓存条目已过期')
        expect(expired_dialog).to_contain_text(
            '本次未命中缓存，按正常代理路径继续处理；用量与费用以上游实际调用为准。'
        )
        expect(expired_dialog).to_contain_text(
            '1 次尝试 · 1 次已发上游 · 1 次上游成功'
        )

        page.goto(
            f'{runner.base}/web/console/#{HISTORY_URL}&request=cache-old-without-fields',
            wait_until='networkidle',
        )
        old_dialog = page.get_by_role(
            'dialog',
            name='完整请求 cache-old-without-fields',
            exact=True,
        )
        expect(old_dialog).to_be_visible()
        runner.check(
            old_dialog.locator('.request-cache-diagnostic').count() == 0,
            'Old trace displayed a cache diagnostic without cache fields',
        )
        expect(old_dialog).to_contain_text('没有可用的时间线事件')
        expect(old_dialog).to_contain_text(
            '不根据空白明细推断成功；最终结果以上方保存的请求摘要为准。'
        )
        runner.record(
            'history-cache-diagnostics',
            hit_attempts=0,
            hit_upstream_attempts=0,
            hit_reason='reused',
            expired_reason='expired',
            old_trace_has_cache_fields=False,
        )

    runner.case(
        'history-cache-diagnostics',
        cache_diagnostics,
        path=HISTORY_URL,
    )


def responsive_cases(runner):
    viewports = (
        ('responsive-320x900', 320, 900),
        ('responsive-390x844', 390, 844),
        ('responsive-768x1024', 768, 1_024),
        ('responsive-1440x900', 1_440, 900),
        ('responsive-short-390x480', 390, 480),
    )

    def make_responsive_case(name):
        def responsive(page, data):
            panel = page.locator('.request-cache-panel')
            expect(panel).to_be_visible()
            expect(page.get_by_role('switch', name='启用请求缓存', exact=True)).to_be_visible()
            refresh = page.get_by_role('button', name='刷新观察', exact=True)
            clear = page.get_by_role('button', name='清空缓存', exact=True)
            refresh.scroll_into_view_if_needed()
            expect(refresh).to_be_in_viewport()
            expect(clear).to_be_in_viewport()
            refresh.focus()
            expect(refresh).to_be_focused()
            clear.focus()
            expect(clear).to_be_focused()
            expect(clear).to_be_in_viewport()
            layout = page.evaluate("""() => ({
              viewport: {width: innerWidth, height: innerHeight},
              scroll_width: document.documentElement.scrollWidth,
              panel_width: document.querySelector('.request-cache-panel')?.getBoundingClientRect().width || 0,
            })""")
            runner.check(
                layout['scroll_width'] <= layout['viewport']['width'] + 1,
                f'{name} overflows horizontally: {layout}',
            )
            runner.check(layout['panel_width'] > 0, f'{name} lost the request-cache panel')
            visible_viewport_jpeg(runner, page, name)
            runner.record(name, layout=layout)

        return responsive

    for name, width, height in viewports:
        runner.case(
            name,
            make_responsive_case(name),
            width=width,
            height=height,
        )


def resource_cases(runner):
    def budgets(page, data):
        page.wait_for_function(
            """prefixes => prefixes.every(prefix =>
              performance.getEntriesByType('resource').some(entry =>
                entry.name.includes('/web/console/assets/' + prefix)
                && entry.name.endsWith('.js')
              )
            )""",
            arg=PAGE_ROUTE_PREFIXES,
        )
        page.wait_for_load_state('networkidle')
        session = page.evaluate("""() => {
          const entries = performance.getEntriesByType('resource')
            .filter(entry => entry.name.includes('/web/console/'));
          return {
            resources: entries.length,
            encoded_bytes: entries.reduce(
              (sum, entry) => sum + entry.encodedBodySize,
              0,
            ),
            decoded_bytes: entries.reduce(
              (sum, entry) => sum + entry.decodedBodySize,
              0,
            ),
            js_assets: entries
              .filter(entry => entry.name.endsWith('.js'))
              .map(entry => entry.name.split('/').pop()),
          };
        }""")
        missing_route_chunks = [
            prefix for prefix in PAGE_ROUTE_PREFIXES
            if not any(
                name and name.startswith(prefix)
                for name in session['js_assets']
            )
        ]
        runner.check(
            not missing_route_chunks,
            f'Normal route chunks did not load: {missing_route_chunks}',
        )
        route_chunks = [
            name for name in session['js_assets']
            if name and any(
                name.startswith(prefix)
                for prefix in PAGE_ROUTE_PREFIXES
            )
        ]
        runner.check(
            session['encoded_bytes'] <= SESSION_ENCODED_BUDGET,
            (
                f"Console session exceeded {SESSION_ENCODED_BUDGET} encoded bytes: "
                f"{session['encoded_bytes']}"
            ),
        )
        runner.check(
            session['resources'] <= SESSION_RESOURCE_BUDGET,
            (
                f"Console session exceeded {SESSION_RESOURCE_BUDGET} resources: "
                f"{session['resources']}"
            ),
        )

        assets = runner.root / 'web/console/assets'
        route_sizes = {}
        for name in route_chunks:
            path = next(assets.glob(name), None)
            if path is None:
                continue
            route_sizes[name] = {
                'decoded_bytes': path.stat().st_size,
                'gzip_bytes': len(gzip.compress(path.read_bytes(), mtime=0)),
            }
        runner.metrics['request_cache_session'] = session
        runner.metrics['request_cache_route'] = route_sizes
        runner.record(
            'resource-budgets',
            session=session,
            route_chunks=route_sizes,
        )

    runner.case('resource-budgets', budgets)


def all_cases(runner):
    setting_cases(runner)
    clear_cases(runner)
    history_cases(runner)
    responsive_cases(runner)
    resource_cases(runner)


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
    server = ThreadingHTTPServer(
        ('127.0.0.1', 0),
        partial(StaticHandler, root=root),
    )
    server.daemon_threads = True
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    base = f'http://127.0.0.1:{server.server_port}'
    runner = None
    fatal_error = ''

    try:
        console_index = root / 'web/console/index.html'
        if not console_index.is_file():
            raise RuntimeError('Built console is missing; integrate the frontend build before running')
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(
                headless=True,
                args=['--no-proxy-server', '--disable-background-networking'],
            )
            try:
                runner = RequestCacheRunner(
                    browser,
                    base,
                    args.output,
                    root,
                    args.case,
                )
                all_cases(runner)
            finally:
                browser.close()
    except Exception as error:
        fatal_error = f'{type(error).__name__}: {error}'
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)

    checks = runner.results if runner else []
    events = runner.events if runner else []
    server_closed = not thread.is_alive()
    result = {
        'pass': (
            bool(checks)
            and all(item['pass'] for item in checks)
            and not events
            and server_closed
            and not fatal_error
        ),
        'checks': checks,
        'evidence': runner.evidence if runner else [],
        'metrics': runner.metrics if runner else {},
        'unexpected_events': events,
        'server_closed': server_closed,
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
