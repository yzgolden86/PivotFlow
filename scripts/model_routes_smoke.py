"""Exercise the synthetic model-routing and request-history console flows.

The script serves the real built React app from an owned local HTTP server.
Admin responses are literal synthetic DTOs, external traffic is blocked, and
the browser remains read-only. Screenshots are visible-viewport JPEGs, never
full-page PNGs.
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
from urllib.parse import parse_qs, urlencode, urlsplit

from playwright.sync_api import expect, sync_playwright

import docs_screenshots as fixtures
from console_visual_regression import SETTINGS
from ui_accessibility_smoke import ROOT, Runner, StaticHandler, SyntheticData


MODEL = 'group-core'
SECOND_MODEL = 'model-alpha'
LONG_HISTORY_MODEL = '历史精确边界-' + ('完整请求模型名' * 40)
BOUNDARY_MODEL = '模型边界' * 128
ALIAS_60 = 'alias-outside-preview-060'
HISTORY_MODEL = 'history-exact-model'


def inventory_item(model, kind, aliases, configured, enabled):
    return {
        'model': model,
        'kind': kind,
        'aliases': aliases,
        'alias_count': len(aliases),
        'configured_channels': configured,
        'enabled_channels': enabled,
    }


def build_inventory():
    aliases = [f'alias-row-{number:03d}' for number in range(1, 61)]
    aliases[59] = ALIAS_60
    items = [
        inventory_item(MODEL, 'group', aliases, 18, 12),
        inventory_item(SECOND_MODEL, 'model', ['alias-alpha-only'], 4, 2),
        inventory_item('stored-legacy-name', 'model', [], 2, 1),
        inventory_item('*', 'wildcard', [], 1, 1),
        inventory_item('model-disabled', 'model', [], 2, 0),
    ]
    items.extend(
        inventory_item(f'model-{number:03d}', 'model', [], 2, 1)
        for number in range(1, 100)
    )
    return items


def route_reason(stage, message, blocking=False):
    return {'stage': stage, 'code': f'synthetic_{stage}', 'message': message, 'blocking': blocking}


def diagnostic_channel(number):
    special = number - 201
    candidate = special not in (2,)
    statuses = ('ready', 'blocked', 'unknown', 'probe')
    attempt_status = statuses[max(special, 0) % len(statuses)]
    blocked_stage = 'attempt' if attempt_status == 'blocked' else ''
    reasons = [
        route_reason('admission', '准入阶段：令牌允许当前模型和客户端协议。'),
    ]
    if candidate:
        reasons.append(route_reason('candidate', '候选阶段：进入静态合成候选层级，权重来自 DTO。'))
    else:
        reasons.append(route_reason('candidate', '候选阶段：当前配置未进入候选池。', True))
    if attempt_status == 'blocked':
        reasons.append(route_reason('attempt', '尝试阶段：渠道日费用上限会阻断下一次上游尝试。', True))
    elif attempt_status == 'probe':
        reasons.append(route_reason('attempt', '尝试阶段：仅在全渠道冷却时提前试探。'))
    else:
        reasons.append(route_reason('attempt', '尝试阶段：当前状态允许交给上游。'))
    health_samples = 0 if special == 2 else 40 + max(special, 0)
    health_rate = 0 if health_samples == 0 else 0.91 + (max(special, 0) % 7) * 0.01
    return {
        'channel_id': number,
        'channel_name': f'合成路由渠道 {number}',
        'enabled': True,
        'base_priority': 100 + number % 3,
        'effective_priority': 87.5 + number % 11,
        'success_rate': health_rate,
        'health_sample_count': health_samples,
        'exact_model_match': True,
        'fuzzy_model_match': number % 2 == 0,
        'model_eligible_key_count': 6,
        'active_key_count': 5,
        'enabled_key_count': 6,
        'routing_weight': 3,
        'attemptable': attempt_status == 'ready',
        'attempt_status': attempt_status,
        'blocked_stage': blocked_stage,
        'credential_kind': 'api_key',
        'failure_penalty': 1.5,
        'ttfb_penalty': 0.4,
        'ttfb_sample_count': 18,
        'average_ttfb_seconds': 0.72,
        'url_count': 5,
        'compatible_url_count': 4,
        'urls': [
            {
                'index': 1,
                'status': 'compatible',
                'declared': True,
                'protocols': ['openai', 'codex'],
                'disabled': False,
                'cooling': False,
                'fallback': False,
            },
            {
                'index': 2,
                'status': 'cooling',
                'declared': False,
                'protocols': ['openai'],
                'disabled': False,
                'cooling': True,
                'fallback': True,
                'retry_after': '2026-11-23T10:05:00+08:00',
            },
        ],
        'possible_upstream_models': ['upstream/synthetic-v1', 'upstream/synthetic-v2'],
        'possible_upstream_model_count': 3,
        'cost': {
            'channel_multiplier': 1.25,
            'key_multiplier_min': 1,
            'key_multiplier_max': 1.2,
            'key_multiplier_count': 3,
            'daily_used_usd': 7.25,
            'daily_limit_usd': 30,
            'site_binding_known': True,
            'site_bound': True,
            'price_status': 'cached',
            'price_cached_at': '2026-11-23T09:55:00+08:00',
        },
        'available_time_start': '08:00',
        'available_time_end': '22:00',
        'channel_cooldown_until': '',
        'model_cooldown_until': '',
        'actual_known': True,
        'rpm_limit': 120,
        'current_rpm': 17,
        'max_concurrency': 16,
        'active_concurrency': 4,
        'candidate': candidate,
        'candidate_position': 1 + max(special, 0) % 3 if candidate else None,
        'higher_priority_count': 1 if special == 3 else 0,
        'same_priority_count': 2,
        'estimated_traffic_share': 0.375 if special == 3 else 0.125,
        'actual_requests': 41 + max(special, 0),
        'actual_share': 0.25,
        'reasons': reasons,
    }


def build_diagnostic(model, client_protocol, token_id, offset, limit, marker='', channel_count=105):
    channels = [diagnostic_channel(number) for number in range(201, 201 + channel_count)]
    aliases = build_inventory()[0]['aliases']
    return {
        'version': 1,
        'observed_at': '2026-11-23T10:00:00+08:00',
        'model': model,
        'client_protocol': client_protocol,
        'canonical_model': MODEL,
        'group_names': aliases[:50] if model in (MODEL, SECOND_MODEL) else [model],
        'group_name_count': len(aliases) if model in (MODEL, SECOND_MODEL) else 1,
        'policy': {
            'route_strategy': 'sticky',
            'pool_mode': 'exact',
            'health_score_enabled': True,
            'ttfb_score_enabled': True,
            'health_window_minutes': 30,
            'candidate_count': 3,
            'attemptable_count': 2,
            'attempt_unknown_count': 1,
            'tier_count': 2,
            'weight_reference': '同层权重参考',
            'actual_window': 'today',
            'actual_known': True,
            'actual_total_requests': 184,
            'sticky': {
                'channel_id': 201,
                'channel_name': '合成路由渠道 201',
                'remembered_at': '2026-11-23T09:50:00+08:00',
                'expires_at': '2026-11-23T10:20:00+08:00',
                'in_candidate_pool': True,
                'forward_status': 'eligible',
            },
        },
        'token': {
            'id': int(token_id) if str(token_id).isdigit() else None,
            'description': f'合成路由令牌 {token_id}' if token_id else '全局管理员观察',
            'scope': 'token' if token_id else 'global',
            'status': 'active',
            'admission': 'allowed',
            'permission_source': 'explicit',
            'model_allowed': True,
            'protocol_allowed': True,
            'channel_restricted': False,
            'cost_used_usd': 4.75,
            'cost_limit_usd': 20,
            'max_concurrency': 4,
            'active_concurrency': 1,
            'reasons': [
                route_reason('admission', '令牌准入原因直接显示在诊断区域，不依赖悬停。'),
            ],
        },
        'channels': channels[offset:offset + limit],
        'total': len(channels),
        'offset': offset,
        'limit': limit,
        'summary': [marker or '静态合成诊断：候选、权重、费用与尝试状态均直接来自 DTO。'],
        'warnings': ['history_model_identity_too_long'] if len(model) > 191 else [],
    }


def request_trace(number):
    return {
        'request_id': f'hist-{number:03d}',
        'started_at': 1788577200 - number * 60,
        'auth_token_id': 101,
        'model': HISTORY_MODEL,
        'client_protocol': 'codex',
        'transport': 'http',
        'is_streaming': True,
        'final_status': 200 if number % 3 else 429,
        'outcome': 'success' if number % 3 else 'error',
        'duration': 1.25,
        'wait_duration': 0.05,
        'attempt_count': 1,
        'upstream_attempt_count': 1,
        'upstream_success_count': 1 if number % 3 else 0,
        'truncated': False,
        'attempts': [
            {
                'number': 1,
                'started_at': 1788577200 - number * 60,
                'duration': 1.25,
                'first_byte_time': 0.2,
                'channel_id': 201,
                'channel_name': '合成路由渠道 201',
                'key_index': 0,
                'url_index': 0,
                'endpoint': 'https://synthetic.example.invalid/v1/chat/completions',
                'actual_model': 'upstream/synthetic-v1',
                'upstream_protocol': 'openai',
                'transport': 'http',
                'status_code': 200 if number % 3 else 429,
                'outcome': 'success' if number % 3 else 'error',
                'upstream': True,
                'response_committed': False,
                'reason': '' if number % 3 else 'upstream_rate_limit',
                'retry': 'none',
                'cooldown': 'none',
                'cooldown_until': 0,
            },
        ],
    }


def history_stats(items):
    return {
        'requests': len(items),
        'successes': sum(item['outcome'] == 'success' for item in items),
        'errors': sum(item['outcome'] == 'error' for item in items),
        'canceled': sum(item['outcome'] == 'canceled' for item in items),
        'upstream_attempts': sum(item['upstream_attempt_count'] for item in items),
        'upstream_successes': sum(item['upstream_success_count'] for item in items),
    }


class ModelRoutesData(SyntheticData):
    """Synthetic admin API with literal routing, history, and editor fixtures."""

    def __init__(self):
        self.calls = []
        self.inventory = build_inventory()
        self.diagnostic_channel_count = 105
        self.tokens = []
        self.channels = []
        self.request_traces = [request_trace(number) for number in range(1, 121)]
        self.fail_diagnostics = 0
        self.abort_diagnostics_once = False
        self.hold_diagnostics = False
        self.pending_diagnostics = []
        self.diagnostics_delay_seconds = 0
        self.fail_tokens = False
        self.site_model_reads = 0
        self.token_writes = []
        retained_models = [f'legacy-selected-name-{number:03d}' for number in range(1, 56)]
        for number in range(1, 106):
            token = copy.deepcopy(fixtures.AUTH_TOKENS[0])
            token.update(
                id=number,
                description=f'合成路由令牌 token-search-{number:03d}',
                token='',
                token_hint=f'synthetic-hint-{number:03d}',
                is_active=True,
                allowed_models=retained_models + [MODEL] if number == 1 else [MODEL],
                allowed_channel_ids=[201] if number == 1 else [],
                channel_restriction_mode='deny',
            )
            self.tokens.append(token)
        for number in range(201, 306):
            channel = copy.deepcopy(fixtures.CHANNELS[0])
            channel.update(
                id=number,
                name=f'合成路由渠道 {number}',
                enabled=True,
                models=[{'model': MODEL, 'disabled': False, 'redirect': ''}],
            )
            self.channels.append(channel)

    def fulfill(self, route, data, count=None):
        route.fulfill(
            status=200,
            content_type='application/json',
            body=json.dumps(fixtures.envelope(data, count), ensure_ascii=False),
        )

    def fulfill_diagnostic(self, route, marker=''):
        parsed = urlsplit(route.request.url)
        query = parse_qs(parsed.query)
        model = query.get('model', [''])[0]
        protocol = query.get('client_protocol', ['openai'])[0]
        token_id = query.get('token_id', [''])[0]
        offset = int(query.get('offset', ['0'])[0])
        limit = int(query.get('limit', ['20'])[0])
        self.fulfill(route, build_diagnostic(model, protocol, token_id, offset, limit, marker, self.diagnostic_channel_count))

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
            route.fulfill(
                status=200,
                content_type='application/json',
                body=json.dumps(fixtures.envelope({'role': 'admin'}), ensure_ascii=False),
            )
            return
        if path == '/admin/routing/models':
            items = list(self.inventory)
            if query.get('enabled_only', ['false'])[0] == 'true':
                items = [item for item in items if item['kind'] != 'wildcard' and item['enabled_channels'] > 0]
            if query.get('search'):
                needle = query['search'][0].casefold()
                items = [
                    item for item in items
                    if needle in item['model'].casefold()
                    or any(needle in alias.casefold() for alias in item['aliases'])
                ]
            offset = int(query.get('offset', ['0'])[0])
            limit = int(query.get('limit', ['50'])[0])
            self.fulfill(route, {
                'version': 1,
                'items': items[offset:offset + limit],
                'total': len(items),
                'offset': offset,
                'limit': limit,
            })
            return
        if path == '/admin/routing/diagnostics':
            if self.abort_diagnostics_once:
                self.abort_diagnostics_once = False
                route.abort('aborted')
                return
            if self.fail_diagnostics:
                self.fail_diagnostics -= 1
                route.fulfill(
                    status=503,
                    content_type='application/json',
                    body='{"success":false,"error":"synthetic_route_diagnostic_unavailable"}',
                )
                return
            if self.diagnostics_delay_seconds:
                time.sleep(self.diagnostics_delay_seconds)
            if self.hold_diagnostics:
                self.pending_diagnostics.append(route)
                return
            self.fulfill_diagnostic(route)
            return
        if path == '/admin/auth-tokens' and route.request.method == 'GET':
            if self.fail_tokens:
                route.fulfill(
                    status=503,
                    content_type='application/json',
                    body='{"success":false,"error":"synthetic_route_tokens_unavailable"}',
                )
                return
            self.fulfill(route, {'tokens': self.tokens, 'is_today': True})
            return
        if path == '/admin/auth-tokens' and route.request.method == 'POST':
            payload = route.request.post_data_json or {}
            self.token_writes.append(payload)
            token = {
                **self.tokens[0],
                **payload,
                'id': 999,
                'token': 'synthetic-created-token',
                'token_hint': 'synthetic-created-hint',
            }
            self.fulfill(route, token)
            return
        if path == '/admin/request-traces':
            items = self._history_items(query)
            offset = int(query.get('offset', ['0'])[0])
            limit = int(query.get('limit', ['20'])[0])
            self.fulfill(route, items[offset:offset + limit], len(items))
            return
        if path == '/admin/request-traces/stats':
            self.fulfill(route, history_stats(self._history_items(query)))
            return
        if path.startswith('/admin/request-traces/'):
            request_id = path.rsplit('/', 1)[-1]
            item = next(
                (trace for trace in self.request_traces if trace['request_id'] == request_id),
                self.request_traces[0],
            )
            self.fulfill(route, item)
            return
        if path == '/admin/channels' and route.request.method == 'GET':
            self.fulfill(route, self.channels, len(self.channels))
            return
        if path.startswith('/admin/channels/') and path.endswith('/editor'):
            channel_id = int(path.split('/')[3])
            channel = next(
                (item for item in self.channels if item['id'] == channel_id),
                self.channels[0],
            )
            self.fulfill(route, {
                'channel': channel,
                'keys': [],
                'model_stats': {'available': True, 'items': []},
                'url_stats': {'available': True, 'items': []},
                'features': {'scheduled_check_enabled': False},
            })
            return
        if path == '/admin/settings':
            self.fulfill(route, SETTINGS)
            return
        if path == '/admin/model-alias-inventory':
            self.fulfill(route, {'candidates': [], 'suggestions': [], 'total_models': 0})
            return
        if path == '/admin/site-models':
            self.site_model_reads += 1
            self.fulfill(route, [], 0)
            return
        if path == '/admin/logs/bootstrap':
            self.fulfill(route, {
                'channel_test_content': '',
                'auth_tokens': [],
                'models': [],
                'channels': [],
                'status_codes': [],
            })
            return
        if path == '/admin/logs':
            self.fulfill(route, [], 0)
            return
        fixtures.mock_admin(route)

    def _history_items(self, query):
        items = list(self.request_traces)
        if query.get('outcome'):
            items = [item for item in items if item['outcome'] == query['outcome'][0]]
        if query.get('auth_token_id'):
            token_id = int(query['auth_token_id'][0])
            items = [item for item in items if item['auth_token_id'] == token_id]
        if query.get('model'):
            items = [item for item in items if item['model'] == query['model'][0]]
        if query.get('client_protocol'):
            items = [
                item for item in items
                if item['client_protocol'] == query['client_protocol'][0]
            ]
        if query.get('search'):
            needle = query['search'][0].casefold()
            items = [
                item for item in items
                if needle in item['request_id'].casefold() or needle in item['model'].casefold()
            ]
        return items


def hash_parts(page):
    raw = page.evaluate('() => location.hash.slice(1)')
    parsed = urlsplit(raw)
    return parsed.path, parse_qs(parsed.query)


def link_parts(link):
    href = link.get_attribute('href') or ''
    parsed = urlsplit(href.split('#', 1)[-1])
    return parsed.path, parse_qs(parsed.query)


def wait_until(runner, predicate, message, timeout=5):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        # Playwright's sync transport only dispatches the React request/route
        # events while its message loop is running; Python sleep starves it.
        runner.current_page.wait_for_timeout(50)
    runner.check(False, message)


def forbidden_read_effect(call):
    if call['method'] != 'GET':
        return True
    segments = {
        segment.casefold()
        for segment in call['path'].split('/')
        if segment
    }
    return bool(segments & {'check', 'probe', 'reveal', 'test'})


def screenshot(runner, page, name):
    path = runner.output / f'{name}.jpg'
    page.screenshot(path=str(path), type='jpeg', quality=82, animations='disabled')
    runner.check(path.read_bytes()[:2] == b'\xff\xd8', f'{name} is not a JPEG')
    runner.record(name, screenshot=str(path), bytes=path.stat().st_size)
    return path


class ModelRoutesRunner(Runner):
    def __init__(self, browser, base, output, root, case_names=None):
        super().__init__(browser, base, output, case_names)
        self.root = root
        self.evidence = []

    def record(self, name, **values):
        self.evidence.append({'case': name, **values})

    def case(self, name, callback, path='/routing', width=1440, height=900, theme='light'):
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
            "localStorage.setItem('pivotflow_token','synthetic-model-routes-only');"
            "localStorage.setItem('pivotflow_web_role','admin');"
        )
        context.add_init_script(
            f"localStorage.setItem('pivotflow_theme', {json.dumps(theme)});"
        )
        data = ModelRoutesData()
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
            forbidden_calls = [
                call for call in data.calls if forbidden_read_effect(call)
            ]
            mutation_calls = [
                call for call in forbidden_calls if call['method'] != 'GET'
            ]
            read_effect_calls = [
                call for call in forbidden_calls if call['method'] == 'GET'
            ]
            self.check(not mutation_calls, f'Routing views issued mutations: {mutation_calls[:3]}')
            self.check(
                not read_effect_calls,
                f'Routing views issued reveal/check/probe/test reads: {read_effect_calls[:3]}',
            )
            self.record(name, admin_calls=len(data.calls), seconds=round(time.perf_counter() - started, 3))
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
            for pending in data.pending_diagnostics:
                try:
                    pending.abort('aborted')
                except Exception:
                    pass
            context.unroute_all(behavior='ignoreErrors')
            context.close()


def route_cases(runner):
    def route_exists(page, data):
        expect(page.get_by_role('link', name='模型路由', exact=True)).to_be_visible()
        expect(page.locator('h1')).to_have_text('模型路由')
        runner.record('route-exists', url=page.url, h1=page.locator('h1').inner_text())

    runner.case('route-exists', route_exists, path='/routing')


def inventory_cases(runner):
    def inventory(page, data):
        inventory_rows = page.locator('button[aria-label^="查看 "][aria-label$=" 的路由"]')
        expect(inventory_rows).to_have_count(50)
        expect(page.get_by_role('navigation', name='模型清单分页', exact=True)).to_be_visible()
        runner.check(
            not any(call['path'] == '/admin/routing/diagnostics' for call in data.calls),
            'Inventory load must not auto-select or diagnose the first model',
        )
        pager = page.get_by_role('navigation', name='模型清单分页', exact=True)
        pager.get_by_role('textbox', name='跳转页码', exact=True).fill('3')
        pager.get_by_role('textbox', name='跳转页码', exact=True).press('Enter')
        expect(inventory_rows).to_have_count(4)
        search = page.get_by_label('搜索模型或别名', exact=True)
        search.fill(ALIAS_60)
        expect(inventory_rows).to_have_count(1)
        expect(inventory_rows.first).to_contain_text(MODEL)
        last_inventory_call = [
            call for call in data.calls if call['path'] == '/admin/routing/models'
        ][-1]
        runner.check(
            last_inventory_call['query'].get('search') == [ALIAS_60],
            'Alias search must be performed by the inventory API, not a client preview',
        )
        inventory_rows.first.click()
        expect(page.locator('h1')).to_have_text('模型路由')
        path, query = hash_parts(page)
        runner.check(path == '/routing', f'Selected model route was {path}')
        runner.check(query.get('model') == [MODEL], f'Model was not persisted: {query}')
        runner.check(query.get('client_protocol') == ['openai'], f'Default protocol missing: {query}')
        runner.check('token_id' not in query, 'Selecting a model must not select a token')
        expect(inventory_rows.first).to_have_attribute('aria-pressed', 'true')
        expect(page.get_by_role('region', name='模型诊断', exact=True)).to_be_visible()
        group = page.locator('details.model-route-group')
        expect(group.locator('summary')).to_contain_text('60 项')
        expect(group.locator('summary')).to_contain_text('预览 50 项')
        group.locator('summary').click()
        alias_buttons = group.locator('button.model-route-alias')
        expect(alias_buttons).to_have_count(50)
        runner.check(
            not any(alias_buttons.nth(index).inner_text() == ALIAS_60 for index in range(50)),
            'The 50-item preview must not claim to contain all 60 aliases',
        )
        alias_buttons.first.click()
        path, query = hash_parts(page)
        runner.check(query.get('model') == ['alias-row-001'], f'Alias selection was lost: {query}')
        runner.record('inventory', model=MODEL, alias_count=60, rendered_alias_preview=50)

    runner.case('inventory-canonical-page-search-selection', inventory, path='/routing')


def diagnostic_cases(runner):
    scope_path = '/routing?' + urlencode({
        'model': MODEL,
        'client_protocol': 'codex',
        'token_id': '101',
    })

    def observations(page, data):
        diagnosis = page.get_by_role('region', name='模型诊断', exact=True)
        expect(diagnosis).to_contain_text('快照')
        expect(diagnosis).to_contain_text('令牌准入原因直接显示在诊断区域，不依赖悬停')
        cards = page.locator('.model-route-channel')
        expect(cards).to_have_count(20)
        current = page.locator('.model-route-channel[data-channel-id="201"]')
        expect(current).to_have_attribute('data-candidate', 'true')
        expect(current).to_contain_text('第 1 层')
        expect(current).to_contain_text('当前可尝试')
        expect(current).to_contain_text('轮询权重')
        expect(current).to_contain_text('候选阶段')
        expect(current).to_contain_text('尝试阶段')
        summary = current.locator('summary')
        summary.focus()
        page.keyboard.press('Enter')
        expect(current.locator('details.model-route-observations')).to_have_attribute('open', '')
        expect(current).to_contain_text('$7.25 / $30.00')
        expect(current).to_contain_text('已有站点价目缓存')
        expect(current).to_contain_text('URL 1')
        expect(current).to_contain_text('保留重试兜底')
        no_health = page.locator('.model-route-channel[data-channel-id="203"]')
        expect(no_health).to_contain_text('无健康样本')
        runner.check('100.0%' not in no_health.inner_text(), 'Zero samples must not become inferred 100% health')
        blocked = page.locator('.model-route-channel[data-channel-id="202"]')
        expect(blocked).to_contain_text('尝试被阻断')
        expect(blocked).to_contain_text('阻断阶段：尝试')
        expect(blocked).to_contain_text('日费用上限会阻断下一次上游尝试')
        conditional = page.locator('.model-route-channel[data-channel-id="204"]')
        expect(conditional).to_contain_text('条件权重参考 37.5%')
        expect(conditional).to_contain_text('提前试探')
        expect(page.get_by_text('符合基础优先级条件，可以沿用最近成功渠道。', exact=False)).to_be_visible()
        screenshot(runner, page, 'desktop-1440-light-diagnostics')
        runner.record(
            'candidate-attempt-observations',
            candidates=3,
            rendered_channels=cards.count(),
            no_health_channel=203,
        )

    runner.case(
        'diagnostics-candidate-attempt-observations',
        observations,
        path=scope_path,
    )

    def exact_links(page, data):
        expect(page.get_by_role('region', name='相关渠道', exact=True)).to_be_visible()
        diagnostic_call = [
            call for call in data.calls if call['path'] == '/admin/routing/diagnostics'
        ][-1]
        runner.check(diagnostic_call['query'].get('model') == [MODEL], 'Diagnostic model scope changed')
        runner.check(
            diagnostic_call['query'].get('client_protocol') == ['codex'],
            'Diagnostic protocol scope changed',
        )
        runner.check(diagnostic_call['query'].get('token_id') == ['101'], 'Diagnostic token scope changed')
        history = page.get_by_role('link', name='查看请求历史', exact=True)
        history_path, history_query = link_parts(history)
        runner.check(history_path == '/logs', f'History link path was {history_path}')
        expected = {
            'view': ['requests'],
            'range': ['today'],
            'model': [MODEL],
            'client_protocol': ['codex'],
            'auth_token_id': ['101'],
        }
        for key, value in expected.items():
            runner.check(history_query.get(key) == value, f'History link {key} was {history_query.get(key)}')
        route_settings = page.get_by_role('link', name='路由设置', exact=True)
        route_path, route_query = link_parts(route_settings)
        runner.check(route_path == '/system', f'Routing settings path was {route_path}')
        runner.check(
            route_query == {'section': ['runtime'], 'group': ['routing']},
            f'Routing settings query was {route_query}',
        )
        health_settings = page.get_by_role('link', name='健康设置', exact=True)
        health_path, health_query = link_parts(health_settings)
        runner.check(health_path == '/system', f'Health settings path was {health_path}')
        runner.check(
            health_query == {'section': ['runtime'], 'group': ['health']},
            f'Health settings query was {health_query}',
        )
        editor = page.locator('.model-route-channel[data-channel-id="201"]').get_by_role(
            'link', name='编辑渠道', exact=True,
        )
        editor_path, editor_query = link_parts(editor)
        runner.check(editor_path == '/channels', f'Editor path was {editor_path}')
        runner.check(
            editor_query == {'focus_channel_id': ['201']},
            f'Editor query was {editor_query}',
        )
        runner.record(
            'exact-links',
            history=history.get_attribute('href'),
            routing_settings=route_settings.get_attribute('href'),
            health_settings=health_settings.get_attribute('href'),
            editor=editor.get_attribute('href'),
        )
        token_search = page.get_by_label('搜索路由令牌', exact=True)
        token_search.fill('token-search-105')
        token_scope = page.get_by_label('路由令牌范围', exact=True)
        expect(token_scope.locator('option').filter(has_text='token-search-105')).to_have_count(1)
        token_scope.select_option('105')
        wait_until(
            runner,
            lambda: hash_parts(page)[1].get('token_id') == ['105'],
            'Searching beyond the 50-token preview did not update the token scope',
        )
        wait_until(
            runner,
            lambda: any(
                call['path'] == '/admin/routing/diagnostics'
                and call['query'].get('token_id') == ['105']
                for call in data.calls
            ),
            'The token selected beyond the preview was not sent exactly',
        )
        runner.record('token-search-beyond-preview', selected_token=105)

    runner.case('diagnostics-exact-scope-links', exact_links, path=scope_path)

    def no_model(page, data):
        expect(page.get_by_label('请求模型', exact=True)).to_have_value('')
        expect(page.get_by_label('路由令牌范围', exact=True)).to_have_value('')
        expect(page.get_by_text('从一个请求模型开始', exact=True)).to_be_visible()
        runner.check(
            not any(call['path'] == '/admin/routing/diagnostics' for call in data.calls),
            'An empty scope must not diagnose or auto-select the first model',
        )
        path, query = hash_parts(page)
        runner.check(path == '/routing', f'Empty-state path was {path}')
        runner.check('model' not in query, f'Empty state invented a model: {query}')
        runner.record('no-selected-model', diagnostic_calls=0)

    runner.case('diagnostics-no-model-empty', no_model, path='/routing')

    def missing_token(page, data):
        scope = page.get_by_label('路由令牌范围', exact=True)
        expect(scope).to_have_value('9999')
        expect(scope.locator('option:checked')).to_contain_text('所选令牌未载入')
        data.fail_tokens = True
        page.get_by_role('button', name='刷新路由快照', exact=True).click()
        alert = page.get_by_role('alert')
        expect(alert).to_contain_text('令牌清单读取失败')
        expect(scope).to_have_value('9999')
        runner.check(
            scope.locator('option:checked').inner_text() != '全局管理员观察',
            'A missing selected token must never widen to the global scope',
        )
        screenshot(runner, page, 'desktop-1440-light-missing-token')
        runner.record('missing-token', selected=scope.input_value(), widened_to_global=False)

    runner.case(
        'diagnostics-missing-token-never-global',
        missing_token,
        path='/routing?' + urlencode({
            'model': MODEL,
            'client_protocol': 'openai',
            'token_id': '9999',
        }),
    )

    def stale_race(page, data):
        data.hold_diagnostics = True
        page.get_by_role('button', name=f'查看 {MODEL} 的路由', exact=True).click()
        wait_until(runner, lambda: len(data.pending_diagnostics) >= 1, 'First diagnostic request was not held')
        page.get_by_role('button', name=f'查看 {SECOND_MODEL} 的路由', exact=True).click()
        wait_until(runner, lambda: len(data.pending_diagnostics) >= 2, 'Second diagnostic request was not held')
        first, second = data.pending_diagnostics[:2]
        data.fulfill_diagnostic(second, '新快照：第二次模型选择必须保留。')
        expect(page.locator('.model-route-snapshot h2')).to_have_text(SECOND_MODEL)
        expect(page.locator('.model-route-summary')).to_contain_text('新快照')
        try:
            data.fulfill_diagnostic(first, '旧快照：不得覆盖新模型。')
        except Exception:
            pass
        page.wait_for_timeout(100)
        expect(page.locator('.model-route-snapshot h2')).to_have_text(SECOND_MODEL)
        expect(page.locator('.model-route-summary')).not_to_contain_text('旧快照')
        data.pending_diagnostics.clear()
        data.hold_diagnostics = True
        refresh = page.get_by_role('button', name='刷新路由快照', exact=True)
        refresh.click()
        wait_until(runner, lambda: len(data.pending_diagnostics) >= 1, 'First refresh was not held')
        refresh.click()
        wait_until(runner, lambda: len(data.pending_diagnostics) >= 2, 'Second refresh was not held')
        first_refresh, second_refresh = data.pending_diagnostics[:2]
        data.fulfill_diagnostic(second_refresh, '刷新最新：后发请求生效。')
        expect(page.locator('.model-route-summary')).to_contain_text('刷新最新')
        try:
            data.fulfill_diagnostic(first_refresh, '刷新过期：不得回写。')
        except Exception:
            pass
        page.wait_for_timeout(100)
        expect(page.locator('.model-route-summary')).to_contain_text('刷新最新')
        expect(page.locator('.model-route-summary')).not_to_contain_text('刷新过期')
        runner.record('stale-races', held_requests=4, final_model=SECOND_MODEL)

    runner.case('diagnostics-model-refresh-stale-races', stale_race, path='/routing')

    def error_recovery(page, data):
        data.abort_diagnostics_once = True
        page.get_by_role('button', name=f'查看 {MODEL} 的路由', exact=True).click()
        alert = page.get_by_role('alert')
        expect(alert).to_contain_text('数据加载失败')
        data.fail_diagnostics = 1
        page.get_by_role('button', name='重试', exact=True).click()
        expect(page.get_by_role('alert')).to_contain_text('synthetic_route_diagnostic_unavailable')
        page.get_by_role('button', name='重试', exact=True).click()
        expect(page.locator('.model-route-snapshot h2')).to_have_text(MODEL)
        runner.record('abort-error-recovery', recovered=True)

    runner.case('diagnostics-abort-error-recovery', error_recovery, path='/routing')

    def channel_pagination(page, data):
        cards = page.locator('.model-route-channel')
        expect(cards).to_have_count(20)
        pager = page.get_by_role('navigation', name='相关渠道分页', exact=True)
        pager.get_by_role('textbox', name='跳转页码', exact=True).fill('6')
        pager.get_by_role('textbox', name='跳转页码', exact=True).press('Enter')
        expect(cards).to_have_count(5)
        expect(cards.last).to_have_attribute('data-channel-id', '305')
        offsets = [
            call['query'].get('offset', ['0'])[0]
            for call in data.calls
            if call['path'] == '/admin/routing/diagnostics'
        ]
        runner.check(offsets[-1] == '100', f'Last diagnostic page offset was {offsets[-1]}')
        runner.record('channel-pagination', total=105, last_page_rows=5, offset=100)

    runner.case('diagnostics-105-channel-pagination', channel_pagination, path=scope_path)

    def timeout_bounded(page, data):
        data.hold_diagnostics = True
        started = time.perf_counter()
        page.get_by_role('button', name=f'查看 {MODEL} 的路由', exact=True).click()
        expect(page.get_by_role('alert')).to_contain_text('读取超过 12 秒', timeout=16000)
        elapsed = time.perf_counter() - started
        runner.check(11.5 <= elapsed < 16, f'Diagnostic wait was not bounded by the 12-second timeout: {elapsed:.2f}')
        for pending in data.pending_diagnostics:
            try:
                data.fulfill_diagnostic(pending, '过期超时响应：不得恢复为成功。')
            except Exception:
                pass
        expect(page.locator('.model-route-snapshot')).to_have_count(0)
        data.hold_diagnostics = False
        page.get_by_role('button', name='重试', exact=True).click()
        expect(page.locator('.model-route-snapshot h2')).to_have_text(MODEL)
        expect(page.locator('.model-route-summary')).not_to_contain_text('过期超时响应')
        runner.record('timeout-bounded', seconds=round(elapsed, 3), timeout_ms=12000, recovered=True)

    runner.case('diagnostics-wait-bounded-12s', timeout_bounded, path='/routing')


def boundary_cases(runner):
    long_path = '/routing?' + urlencode({
        'model': LONG_HISTORY_MODEL,
        'client_protocol': 'openai',
    })

    def long_and_boundary(page, data):
        runner.check(len(LONG_HISTORY_MODEL) > 191, 'Long model fixture must exceed the 191 boundary')
        long_history = page.get_by_role('link', name='普通请求历史', exact=True)
        long_path_value, long_query = link_parts(long_history)
        runner.check(long_path_value == '/logs', f'Ordinary history path was {long_path_value}')
        runner.check('model' not in long_query, f'Long model was truncated into history URL: {long_query}')
        expect(page.get_by_text('191', exact=False).first).to_be_visible()
        boundary_path = '/routing?' + urlencode({
            'model': BOUNDARY_MODEL,
            'client_protocol': 'openai',
        })
        page.goto(f'{runner.base}/web/console/#{boundary_path}', wait_until='networkidle')
        expect(page.get_by_label('请求模型', exact=True)).to_have_value(BOUNDARY_MODEL)
        expect(page.locator('.model-route-snapshot h2')).to_contain_text(BOUNDARY_MODEL)
        boundary_calls = [
            call for call in data.calls
            if call['path'] == '/admin/routing/diagnostics'
            and call['query'].get('model') == [BOUNDARY_MODEL]
        ]
        runner.check(boundary_calls, 'The 512-character Unicode model was not diagnosed exactly')
        before = len(boundary_calls)
        model_input = page.get_by_label('请求模型', exact=True)
        model_input.fill(BOUNDARY_MODEL + '超')
        expect(model_input).to_have_value(BOUNDARY_MODEL)
        page.get_by_role('button', name='查看路由', exact=True).click()
        invalid_path = '/routing?' + urlencode({'model': BOUNDARY_MODEL + '超', 'client_protocol': 'openai'})
        page.goto(f'{runner.base}/web/console/#{invalid_path}', wait_until='networkidle')
        expect(page.locator('.model-route-alert')).to_contain_text('512')
        after = sum(
            call['path'] == '/admin/routing/diagnostics'
            and call['query'].get('model') == [BOUNDARY_MODEL + '超']
            for call in data.calls
        )
        runner.check(after == 0, 'A 513-character model must not trigger a widened diagnostic read')
        screenshot(runner, page, 'desktop-1440-light-unicode-boundary')
        runner.record(
            'unicode-boundary',
            long_model_chars=len(LONG_HISTORY_MODEL),
            accepted_chars=len(BOUNDARY_MODEL),
            rejected_chars=len(BOUNDARY_MODEL) + 1,
            boundary_reads=before,
        )

    runner.case('routing-long-and-512-unicode-boundaries', long_and_boundary, path=long_path)


def history_cases(runner):
    deep_path = '/logs?' + urlencode({
        'view': 'requests',
        'range': 'today',
        'model': HISTORY_MODEL,
        'client_protocol': 'codex',
        'auth_token_id': '101',
        'page': '2',
        'search': 'hist',
        'outcome': 'error',
    })

    def deep_link(page, data):
        expect(page.get_by_label('精确请求模型', exact=True)).to_have_value(HISTORY_MODEL)
        expect(page.get_by_label('历史客户端协议', exact=True)).to_have_value('codex')
        expect(page.get_by_label('完整请求访问令牌', exact=True)).to_have_value('101')
        expect(page.get_by_label('完整请求时间范围', exact=True)).to_have_value('today')
        expect(page.get_by_label('搜索完整请求', exact=True)).to_have_value('hist')
        expect(page.get_by_label('最终结果', exact=True)).to_have_value('error')
        wait_until(
            runner,
            lambda: any(
                call['path'] == '/admin/request-traces'
                for call in data.calls
            ),
            'Request history list did not load',
        )
        list_call = [
            call for call in data.calls if call['path'] == '/admin/request-traces'
        ][-1]
        runner.check(list_call['query'].get('offset') == ['20'], f'Restored page offset was {list_call["query"]}')
        for key, value in {
            'range': 'today',
            'model': HISTORY_MODEL,
            'client_protocol': 'codex',
            'auth_token_id': '101',
            'search': 'hist',
            'outcome': 'error',
        }.items():
            runner.check(list_call['query'].get(key) == [value], f'History list {key} was {list_call["query"].get(key)}')
        stats_call = [
            call for call in data.calls if call['path'] == '/admin/request-traces/stats'
        ][-1]
        filter_keys = ('range', 'model', 'client_protocol', 'auth_token_id', 'search', 'outcome')
        runner.check(
            all(list_call['query'].get(key) == stats_call['query'].get(key) for key in filter_keys),
            'History list and stats used different filter queries',
        )
        outcome = page.get_by_label('最终结果', exact=True)
        outcome.select_option('success')
        wait_until(
            runner,
            lambda: hash_parts(page)[1].get('outcome') == ['success'],
            'Changing history filters did not update the URL',
        )
        expect(page.get_by_label('精确请求模型', exact=True)).to_have_value(HISTORY_MODEL)
        page.go_back()
        expect(page.get_by_label('最终结果', exact=True)).to_have_value('error')
        expect(page.get_by_label('精确请求模型', exact=True)).to_have_value(HISTORY_MODEL)
        expect(page.get_by_label('历史客户端协议', exact=True)).to_have_value('codex')
        expect(page.get_by_label('完整请求访问令牌', exact=True)).to_have_value('101')
        detail = page.locator(
            'a[aria-label^="查看请求"], button[aria-label^="查看请求"]'
        ).first
        detail.click()
        expect(page.get_by_role('dialog')).to_be_visible()
        path, query = hash_parts(page)
        runner.check(path == '/logs', f'History detail path was {path}')
        runner.check(query.get('request'), f'History detail URL lacked request: {query}')
        for key, value in {
            'model': HISTORY_MODEL,
            'client_protocol': 'codex',
            'auth_token_id': '101',
            'range': 'today',
            'page': '2',
            'search': 'hist',
            'outcome': 'error',
        }.items():
            runner.check(query.get(key) == [value], f'Detail URL dropped {key}: {query}')
        screenshot(runner, page, 'desktop-1440-light-request-history-detail')
        runner.record('history-deep-link', filters=query, detail_retained_scope=True)

    runner.case('history-back-forward-and-detail-scope', deep_link, path=deep_path)

    def invalid_scope_blocks_reads(page, data):
        start_index = len(data.calls)
        invalid_paths = [
            ('model-too-long', '/logs?' + urlencode({
                'view': 'requests',
                'range': 'today',
                'model': LONG_HISTORY_MODEL,
                'client_protocol': 'codex',
                'auth_token_id': '101',
            })),
            ('repeated-model', '/logs?view=requests&model=a&model=b&client_protocol=openai&range=today'),
            ('invalid-token', '/logs?view=requests&model=valid&client_protocol=openai&auth_token_id=0'),
            ('invalid-protocol', '/logs?view=requests&model=valid&client_protocol=invalid&range=today'),
        ]
        for name, path in invalid_paths:
            page.goto(f'{runner.base}/web/console/#{path}', wait_until='networkidle')
            expect(page.get_by_role('alert')).to_be_visible()
            if name == 'model-too-long':
                expect(page.get_by_role('alert')).to_contain_text('191')
            history_calls = [
                call for call in data.calls[start_index:]
                if call['path'] in ('/admin/request-traces', '/admin/request-traces/stats')
            ]
            runner.check(not history_calls, f'{name} widened the invalid scope into history reads')
            start_index = len(data.calls)
        runner.record('invalid-history-scope', blocked_cases=len(invalid_paths), history_reads=0)

    runner.case(
        'history-invalid-scope-blocks-widening',
        invalid_scope_blocks_reads,
        path='/logs?view=requests&range=today',
    )


def navigation_cases(runner):
    def settings_groups(page, data):
        groups = page.locator('aside[aria-label="设置分类"] button')
        routing_group = groups.filter(has_text='路由策略')
        health_group = groups.filter(has_text='健康检测')
        expect(routing_group).to_have_attribute('aria-pressed', 'true')
        expect(page.locator('.settings-editor h2')).to_contain_text('路由策略')
        health_group.click()
        wait_until(
            runner,
            lambda: hash_parts(page)[1].get('group') == ['health'],
            'Health settings did not update the group URL',
        )
        expect(health_group).to_have_attribute('aria-pressed', 'true')
        expect(page.locator('.settings-editor h2')).to_contain_text('健康检测')
        page.go_back()
        expect(routing_group).to_have_attribute('aria-pressed', 'true')
        expect(page.get_by_role('tab', name='运行设置', exact=True)).to_have_attribute(
            'aria-selected', 'true',
        )
        expect(page.locator('.settings-savebar')).not_to_be_visible()
        screenshot(runner, page, 'desktop-1440-light-settings-routing')
        runner.record('settings-groups', restored_after_back=True)

    runner.case(
        'settings-group-deep-link-and-back',
        settings_groups,
        path='/system?section=runtime&group=routing',
    )

    def channel_editor(page, data):
        editor = page.locator('.model-route-channel[data-channel-id="201"]').get_by_role(
            'link', name='编辑渠道', exact=True,
        )
        editor.click()
        expect(page.get_by_role('dialog', name='编辑渠道', exact=True)).to_be_visible()
        path, query = hash_parts(page)
        runner.check(path == '/channels', f'Channel editor route was {path}')
        runner.check(query == {'focus_channel_id': ['201']}, f'Channel focus query was {query}')
        expect(page.get_by_label('渠道名称', exact=True)).to_have_value('合成路由渠道 201')
        screenshot(runner, page, 'desktop-1440-light-channel-editor')
        runner.record('channel-editor', channel_id=201, snapshot_read=True)

    runner.case(
        'channel-editor-focus-link',
        channel_editor,
        path='/routing?' + urlencode({
            'model': MODEL,
            'client_protocol': 'openai',
        }),
    )


def responsive_cases(runner):
    dimensions = [
        (320, 900, 'light'),
        (390, 844, 'light'),
        (720, 450, 'light'),
        (1440, 900, 'light'),
        (1440, 900, 'dark'),
    ]
    scope_path = '/routing?' + urlencode({
        'model': MODEL,
        'client_protocol': 'openai',
        'token_id': '101',
    })

    def layout(page, data):
        viewport = page.viewport_size
        runner.check(page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'),
                     'Model routing page has horizontal overflow')
        runner.check(page.get_by_role('region', name='模型清单', exact=True).is_visible(),
                     'Inventory is not visible')
        runner.check(page.get_by_role('region', name='模型诊断', exact=True).is_visible(),
                     'Diagnosis is not visible')
        focus_targets = [
            page.get_by_label('请求模型', exact=True),
            page.get_by_role('button', name='查看路由', exact=True),
            page.get_by_label('客户端协议', exact=True),
            page.get_by_label('搜索路由令牌', exact=True),
            page.get_by_label('路由令牌范围', exact=True),
            page.get_by_role('button', name='刷新路由快照', exact=True),
            page.get_by_role('link', name='查看请求历史', exact=True),
        ]
        for target in focus_targets:
            target.focus()
            expect(target).to_be_focused()
            runner.check(target.evaluate('(element) => element.tabIndex >= 0'),
                         'A routing control is not keyboard reachable')
        page.evaluate('scrollTo(0, 0)')
        page.wait_for_timeout(80)
        screenshot(
            runner,
            page,
            f'routing-{viewport["width"]}x{viewport["height"]}-{page.evaluate("document.documentElement.dataset.theme")}',
        )
        if viewport['width'] <= 720:
            page.locator('.model-route-scope').scroll_into_view_if_needed()
            screenshot(runner, page, f'routing-{viewport["width"]}x{viewport["height"]}-scope')
            page.locator('.model-route-channel').first.scroll_into_view_if_needed()
            screenshot(runner, page, f'routing-{viewport["width"]}x{viewport["height"]}-channel')
            runner.check(page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'),
                         'Scrolled routing controls or channels overflow the viewport')
        runner.record(
            'responsive',
            width=viewport['width'],
            height=viewport['height'],
            theme=page.evaluate("document.documentElement.dataset.theme"),
            focus_targets=len(focus_targets),
            overflow=False,
        )

    for width, height, theme in dimensions:
        runner.case(
            f'routing-layout-{width}-{height}-{theme}',
            layout,
            path=scope_path,
            width=width,
            height=height,
            theme=theme,
        )


def pagination_shrink_cases(runner):
    def inventory_shrink(page, data):
        rows = page.locator('.model-route-model')
        expect(rows).to_have_count(50)
        pager = page.get_by_role('navigation', name='模型清单分页', exact=True)
        pager.get_by_role('textbox', name='跳转页码', exact=True).fill('3')
        pager.get_by_role('textbox', name='跳转页码', exact=True).press('Enter')
        expect(rows).to_have_count(4)
        data.inventory = data.inventory[:1]
        page.get_by_role('button', name='刷新路由快照', exact=True).click()
        expect(rows).to_have_count(1)
        expect(rows.first).to_contain_text(MODEL)
        reads = [call for call in data.calls if call['path'] == '/admin/routing/models']
        runner.check(reads[-1]['query'].get('offset') == ['0'], 'Shrunk model inventory did not return to the first valid page')
        runner.record('inventory-shrink', total=1, restored_offset=0)

    runner.case('inventory-shrink-restores-valid-page', inventory_shrink)

    def channels_shrink(page, data):
        cards = page.locator('.model-route-channel')
        expect(cards).to_have_count(20)
        pager = page.get_by_role('navigation', name='相关渠道分页', exact=True)
        pager.get_by_role('textbox', name='跳转页码', exact=True).fill('6')
        pager.get_by_role('textbox', name='跳转页码', exact=True).press('Enter')
        expect(cards).to_have_count(5)
        expect(cards.first).to_have_attribute('data-channel-id', '301')
        data.diagnostic_channel_count = 5
        page.get_by_role('button', name='刷新路由快照', exact=True).click()
        expect(cards.first).to_have_attribute('data-channel-id', '201')
        expect(cards).to_have_count(5)
        reads = [call for call in data.calls if call['path'] == '/admin/routing/diagnostics']
        runner.check(reads[-1]['query'].get('offset') == ['0'], 'Shrunk channel pool did not return to a valid page')
        runner.record('channels-shrink', total=5, restored_offset=0)

    runner.case('diagnostics-shrink-restores-valid-page', channels_shrink,
                path='/routing?' + urlencode({'model': MODEL, 'client_protocol': 'openai', 'token_id': '101'}))

    def history_shrink(page, data):
        rows = page.locator('.request-ledger-row')
        expect(rows).to_have_count(20)
        data.request_traces = data.request_traces[:3]
        page.get_by_role('button', name='刷新完整请求', exact=True).click()
        expect(rows).to_have_count(3)
        _, query = hash_parts(page)
        runner.check('page' not in query, 'Shrunk history must restore its URL page as well as the list')
        reads = [call for call in data.calls if call['path'] == '/admin/request-traces']
        runner.check(reads[-1]['query'].get('offset') == ['0'], 'Shrunk history list did not reload its first page')
        runner.record('history-shrink', total=3, restored_offset=0)

    runner.case('history-shrink-restores-valid-page', history_shrink,
                path='/logs?view=requests&range=today&page=6')


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
            route_loaded: entries.some(entry => entry.name.includes('/ModelRoutesPage-')
              && entry.name.endsWith('.js')),
          };
        }""")
        assets = runner.root / 'web/console/assets'
        chunks = [
            path for path in assets.iterdir()
            if path.is_file() and path.suffix == '.js'
            and path.name.startswith('ModelRoutesPage-')
        ]
        runner.check(chunks, 'Built ModelRoutesPage route chunk is missing')
        chunk = max(chunks, key=lambda path: path.stat().st_size)
        route_sizes = {
            'decoded_bytes': chunk.stat().st_size,
            'gzip_bytes': len(gzip.compress(chunk.read_bytes(), mtime=0)),
        }
        runner.check(session['route_loaded'], 'The browser did not load the ModelRoutesPage chunk')
        runner.check(session['encoded_bytes'] <= 400000, 'Console session exceeds 400000 encoded bytes')
        runner.check(session['resources'] <= 45, 'Console session exceeds the 45-resource budget')
        runner.check(route_sizes['gzip_bytes'] <= 150000, 'ModelRoutesPage exceeds 150000 gzip bytes')
        runner.check(route_sizes['decoded_bytes'] <= 450000, 'ModelRoutesPage exceeds 450000 decoded bytes')
        runner.metrics['routing_session'] = session
        runner.metrics['model_routes_chunk'] = route_sizes
        runner.record('routing-resource-budgets', session=session, route=route_sizes)

    scope_path = '/routing?' + urlencode({
        'model': MODEL,
        'client_protocol': 'openai',
    })
    runner.case('routing-resource-budgets', budgets, path=scope_path)


def token_form_cases(runner):
    def token_models(page, data):
        page.get_by_role('button', name='创建令牌', exact=True).click()
        dialog = page.get_by_role('dialog', name='创建令牌', exact=True)
        expect(dialog).to_be_visible()
        selection = dialog.locator('.selection-panel').first
        expect(selection.locator('.selection-option')).to_have_count(50)
        expect(dialog.get_by_role('button', name='全选本页模型', exact=True)).to_be_visible()
        model_calls = [
            call for call in data.calls if call['path'] == '/admin/routing/models'
        ]
        runner.check(model_calls, 'Token form did not read the routing model inventory')
        runner.check(
            model_calls[-1]['query'].get('enabled_only') == ['true'],
            'Token form must request enabled-only model options',
        )
        runner.check(
            model_calls[-1]['query'].get('limit') == ['50'],
            'Token form must request 50 model options at a time',
        )
        runner.check(data.site_model_reads == 0, 'Token form must not read raw /admin/site-models')
        option_text = '\n'.join(selection.locator('code').all_inner_texts())
        runner.check('alias-row-' not in option_text, 'Aliases must not become canonical token options')
        runner.check('*' not in selection.locator('code').all_inner_texts(), 'Wildcard must not be selectable')
        pager = dialog.get_by_role('navigation', name='模型选择分页', exact=True)
        pager.get_by_role('textbox', name='跳转页码', exact=True).fill('2')
        pager.get_by_role('textbox', name='跳转页码', exact=True).press('Enter')
        expect(selection.locator('.selection-option')).to_have_count(50)
        search = dialog.get_by_label('搜索可选模型', exact=True)
        search.fill(ALIAS_60)
        expect(selection.locator('.selection-option')).to_have_count(1)
        expect(selection.locator('code')).to_have_text(MODEL)
        search_call = [
            call for call in data.calls if call['path'] == '/admin/routing/models'
        ][-1]
        runner.check(
            search_call['query'].get('search') == [ALIAS_60],
            'Token form must search aliases on the server',
        )
        dialog.get_by_role('button', name='关闭弹窗', exact=True).click()
        page.goto(
            f'{runner.base}/web/console/#/tokens?token=1',
            wait_until='networkidle',
        )
        edit_dialog = page.get_by_role('dialog', name='编辑令牌', exact=True)
        expect(edit_dialog).to_be_visible()
        edited_selection = edit_dialog.locator('.selection-panel').first
        retained = edited_selection.locator('details.selection-retained')
        expect(retained.locator('summary')).to_contain_text('本页之外的原有选择 · 55 项')
        retained.locator('summary').click()
        expect(retained.locator('.selection-option')).to_have_count(50)
        legacy_option = retained.locator('.selection-option').filter(
            has_text='legacy-selected-name-001',
        )
        expect(legacy_option.get_by_role('checkbox')).to_be_checked()
        expect(retained.get_by_text('预览最多 50 项', exact=False)).to_be_visible()
        runner.check(data.site_model_reads == 0, 'Editing a token must not read raw /admin/site-models')
        runner.record(
            'token-model-selector',
            canonical_options=102,
            page_size=50,
            alias_search=ALIAS_60,
            retained_legacy=55,
            retained_preview=50,
        )

    runner.case('token-model-selector-canonical-search-preserved', token_models, path='/tokens')


def all_cases(runner):
    route_cases(runner)
    inventory_cases(runner)
    diagnostic_cases(runner)
    boundary_cases(runner)
    history_cases(runner)
    navigation_cases(runner)
    responsive_cases(runner)
    pagination_shrink_cases(runner)
    resource_cases(runner)
    token_form_cases(runner)


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
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(
                headless=True,
                args=['--no-proxy-server', '--disable-background-networking'],
            )
            try:
                runner = ModelRoutesRunner(browser, base, args.output, root, args.case)
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
