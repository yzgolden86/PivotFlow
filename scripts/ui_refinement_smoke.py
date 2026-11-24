"""Regression checks for the approved UI refinement using synthetic APIs.

Serve the real built console, intercept every management/model endpoint, and
close the owned server. Evidence is limited to visible-viewport JPEGs.
"""
import argparse
import copy
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
from model_routes_smoke import ModelRoutesData
from ui_accessibility_smoke import StaticHandler
from ui_refinement_fixtures import runtime_metrics
from request_cache_smoke import request_cache_settings


def cost_summary(coverage='incomplete'):
    total = 1113 if coverage == 'incomplete' else 4 if coverage == 'complete' else 0
    known = total if coverage == 'complete' else 0
    return {
        'version': 1, 'currency': 'USD', 'reconciliation': 'not_reconciled',
        'successful_requests': total, 'estimated_requests': known,
        'zero_requests': 0, 'local_free_requests': 0, 'partial_requests': 0,
        'unknown_requests': total - known, 'historical_requests': total - known,
        'site_pricing_requests': known, 'local_estimate_requests': 0, 'mixed_requests': 0,
        'recorded_cost_usd': 12 if known else 0, 'estimated_cost_usd': 12 if known else 0,
        'partial_cost_usd': 0, 'historical_cost_usd': 0,
    }


class RefinementData(ModelRoutesData):
    def __init__(self, case):
        super().__init__()
        self.case = case
        self.fail_runtime = False
        self.settings_writes = []
        self.site_facts = None
        self.scoped_bindings = None
        self.settings = request_cache_settings()
        self.sites = copy.deepcopy(fixtures.SITES)
        self.accounts = [copy.deepcopy(account) for accounts in fixtures.ACCOUNTS.values() for account in accounts]
        self.tokens = self.tokens[:5]
        for token in self.tokens:
            token.update(token_recoverable=True, expires_at=0, cost_limit_usd=0)
        if case == 'tokens-density':
            for token in self.tokens:
                token.update(cost_used_usd=2, effective_cost_usd=1)
        if case.startswith('manual-checkin'):
            self.sites = [self.sites[1]]
            method = 'unavailable' if case.endswith('unavailable') else 'disabled' if case.endswith('disabled') else 'unknown'
            self.sites[0].update(external_checkin_url='https://manual.example.invalid/checkin', checkin_method=method)
            self.accounts = [account for account in self.accounts if account['site_id'] == self.sites[0]['id']]
        if case == 'model-test-shared-facts':
            self.sites = self.sites[:2]
            current = copy.deepcopy(self.accounts[0])
            current.update(id=101, site_id=self.sites[0]['id'], enabled=True, label='current-eligible-account')
            stale = copy.deepcopy(current)
            stale.update(id=102, label='stale-account')
            disabled = copy.deepcopy(current)
            disabled.update(id=103, label='disabled-account', enabled=False)
            second_site = copy.deepcopy(current)
            second_site.update(id=104, site_id=self.sites[1]['id'], label='second-site-current-account')
            self.accounts = [current, stale, disabled, second_site]
            self.channels = []
            fact = {'site_account_id': 101, 'model': 'shared-current-model', 'route_type': 'openai_chat',
                    'source': 'models_endpoint', 'disabled': False, 'stale': False, 'last_seen_at': 100, 'created_at': 1, 'updated_at': 100}
            self.site_facts = [fact, {**fact, 'site_account_id': 102, 'stale': True, 'last_seen_at': 200},
                               {**fact, 'site_account_id': 103, 'last_seen_at': 300},
                               {**fact, 'site_account_id': 104, 'last_seen_at': 50}]
        if case.startswith('model-test-scoped-channel-'):
            self.sites = self.sites[:2]
            first = copy.deepcopy(self.accounts[0])
            first.update(id=101, site_id=self.sites[0]['id'], enabled=True, label='selected-site-stale-account')
            second = copy.deepcopy(first)
            second.update(id=104, site_id=self.sites[1]['id'], label='other-site-channel-account')
            self.accounts = [first, second]
            channel = copy.deepcopy(self.channels[1])
            channel.update(id=501, name='other-site-only-channel', site_account_id=104,
                           models=[{'model': 'site-scoped-model', 'disabled': False, 'redirect': ''}])
            self.scoped_bindings = []
            if case.endswith('binding'):
                second['enabled'] = False
                channel['enabled'] = False
                channel.pop('site_account_id', None)
                self.scoped_bindings = [{'id': 601, 'site_account_id': 104, 'channel_id': 501,
                                         'projection_key': 'synthetic-projection', 'ownership': 'projected',
                                         'status': 'active', 'last_sync_status': 'success', 'created_at': 1, 'updated_at': 100}]
            self.channels = [channel]
            self.site_facts = [{'site_account_id': 101, 'model': 'site-scoped-model', 'route_type': 'openai_chat',
                                'source': 'models_endpoint', 'disabled': False, 'stale': True,
                                'last_seen_at': 100, 'created_at': 1, 'updated_at': 100}]
        if case == 'connect-invalid-config':
            for site in self.sites:
                site['enabled'] = False
            for account in self.accounts:
                account['enabled'] = False
            self.channels = self.channels[:2]
            self.channels[0].update(urls=[], enabled=True)
            self.channels[1].update(key_count=0, enabled=True, auth_type='api_key')
            self.site_facts = [{'site_account_id': self.accounts[0]['id'], 'model': 'stale-disabled-owner-model',
                                'route_type': 'openai_chat', 'source': 'models_endpoint', 'disabled': False,
                                'stale': True, 'last_seen_at': 1, 'created_at': 1, 'updated_at': 1}]

    def permits_mutation(self, request):
        path = urlsplit(request.url).path
        return ((self.case == 'site-probe' and request.method == 'POST' and re.fullmatch(r'/admin/sites/\d+/probe', path))
                or (self.case == 'site-batch' and request.method == 'PATCH' and re.fullmatch(r'/admin/sites/\d+', path))
                or (self.case == 'settings-units' and request.method == 'POST' and path == '/admin/settings/batch'))

    def route(self, route):
        parsed = urlsplit(route.request.url)
        path, query = parsed.path, parse_qs(parsed.query)
        if path == '/admin/settings':
            self.calls.append({'path': path, 'method': route.request.method})
            self.fulfill(route, self.settings)
            return
        if path == '/admin/runtime-metrics':
            self.calls.append({'path': path, 'method': route.request.method})
            if self.fail_runtime:
                route.fulfill(status=503, content_type='application/json', body='{"success":false,"error":"synthetic_runtime_unavailable"}')
            else:
                metrics = runtime_metrics(self.case in ('health-issues', 'health-stale-issues'))
                if self.case == 'health-retained-loss':
                    metrics['logs'].update(write_failed=5, dropped=4)
                self.fulfill(route, metrics)
            return
        if path == '/admin/site-inventory':
            self.calls.append({'path': path, 'method': route.request.method})
            self.fulfill(route, {'sites': self.sites, 'accounts': self.accounts})
            return
        if path == '/admin/site-models' and self.site_facts is not None:
            self.calls.append({'path': path, 'method': route.request.method, 'query': query})
            self.fulfill(route, self.site_facts, len(self.site_facts))
            return
        if path == '/admin/site-channel-bindings' and self.scoped_bindings is not None:
            self.calls.append({'path': path, 'method': route.request.method})
            self.fulfill(route, self.scoped_bindings, len(self.scoped_bindings))
            return
        if path == '/admin/site-pricing' and self.scoped_bindings is not None:
            self.calls.append({'path': path, 'method': route.request.method, 'query': query})
            site_id = int(query['site_id'][0])
            price = 1 if site_id == self.sites[0]['id'] else 10
            self.fulfill(route, {'site_id': site_id, 'available': True, 'group_ratio': {'default': 1},
                                 'models': [{'model': 'site-scoped-model', 'quota_type': 0, 'input_price': price,
                                             'output_price': price * 2, 'model_ratio': 1, 'completion_ratio': 2,
                                             'per_call_price': 0, 'groups': [], 'source': 'synthetic'}]})
            return
        if self.case == 'site-probe' and path.endswith('/probe'):
            self.calls.append({'path': path, 'method': route.request.method})
            self.fulfill(route, {'matched': False, 'provider_id': ''})
            return
        if self.case == 'site-batch' and route.request.method == 'PATCH':
            self.calls.append({'path': path, 'method': route.request.method})
            if int(path.rsplit('/', 1)[-1]) == self.sites[0]['id']:
                self.fulfill(route, self.sites[0])
            else:
                route.fulfill(status=503, content_type='application/json', body='{"success":false,"error":"synthetic_update_failed"}')
            return
        if path == '/admin/settings/batch':
            payload = route.request.post_data_json
            self.settings_writes.append(payload)
            if isinstance(payload, dict):
                for setting in self.settings:
                    if setting['key'] in payload:
                        setting['value'] = payload[setting['key']]
            self.calls.append({'path': path, 'method': route.request.method})
            self.fulfill(route, {})
            return
        if path == '/admin/channels':
            offset = int(query.get('offset', ['0'])[0])
            limit = int(query.get('limit', ['50'])[0])
            self.calls.append({'path': path, 'method': route.request.method, 'query': query})
            self.fulfill(route, self.channels[offset:offset + limit], len(self.channels))
            return
        if path == '/admin/dashboard' and self.case == 'dashboard-costs':
            dashboard = copy.deepcopy(fixtures.DASHBOARD)
            dashboard['totals'].update(effective_cost=90, cost=90)
            dashboard['model_usage'] = [
                {'key': f'synthetic-cost-model-{n}', 'label': f'synthetic-cost-model-{n}',
                 'requests': 10, 'success': 10, 'errors': 0, 'input_tokens': 100,
                 'output_tokens': 20, 'effective_cost': 10, 'share': 1 / 9}
                for n in range(1, 10)
            ]
            self.calls.append({'path': path, 'method': route.request.method})
            self.fulfill(route, dashboard)
            return
        if path == '/dashboard/summary':
            dashboard = copy.deepcopy(fixtures.DASHBOARD)
            coverage = 'complete' if self.case == 'cost-complete' else 'empty' if self.case == 'cost-empty' else 'incomplete'
            dashboard['cost_summary'] = None if self.case == 'cost-missing' else cost_summary(coverage)
            self.calls.append({'path': path, 'method': route.request.method})
            self.fulfill(route, dashboard)
            return
        super().route(route)


class Runner:
    def __init__(self, browser, base, output, cases, wallpaper_fixture=None):
        self.browser, self.base, self.output, self.cases = browser, base, output, cases
        self.wallpaper = wallpaper_fixture.read_bytes() if wallpaper_fixture else None
        self.results, self.events, self.metrics = [], [], {}

    def check(self, condition, message):
        if not condition:
            raise AssertionError(message)

    def case(self, name, callback, path='/tokens', width=1440, height=900, theme='light', touch=False, wallpaper=False, glass=False):
        if self.cases and name not in self.cases:
            return
        context = self.browser.new_context(viewport={'width': width, 'height': height},
                                           color_scheme=theme, reduced_motion='reduce', has_touch=touch)
        context.set_default_timeout(6000)
        context.set_default_navigation_timeout(30000)
        context.add_init_script("localStorage.setItem('pivotflow_token','synthetic-ui-refinement-only');localStorage.setItem('pivotflow_web_role','admin');")
        context.add_init_script(f"localStorage.setItem('pivotflow_theme',{json.dumps(theme)});")
        if wallpaper:
            appearance = {'preference': theme, 'backgroundImage': f'{self.base}/synthetic-wallpaper.jpg',
                          'backgroundDim': 15, 'surfaceOpacity': 78 if glass else 100, 'surfaceBlur': 18}
            context.add_init_script(f"localStorage.setItem('pivotflow_appearance',JSON.stringify({json.dumps(appearance)}));")
        data = RefinementData(name)
        page = context.new_page()
        page.on('pageerror', lambda error: self.events.append({'case': name, 'pageerror': str(error)}))
        origin = urlsplit(self.base).netloc

        def intercept(route):
            parsed = urlsplit(route.request.url)
            if parsed.netloc != origin:
                self.events.append({'case': name, 'external_blocked': parsed.netloc})
                route.abort('blockedbyclient')
            elif parsed.path == '/synthetic-wallpaper.jpg' and self.wallpaper:
                route.fulfill(status=200, content_type='image/jpeg', body=self.wallpaper)
            elif parsed.path.startswith(('/admin/', '/dashboard/')):
                if route.request.method != 'GET' and not data.permits_mutation(route.request):
                    self.events.append({'case': name, 'unexpected_mutation': parsed.path})
                    route.abort('blockedbyclient')
                else:
                    data.route(route)
            elif parsed.path.startswith(('/v1/', '/v1beta/')):
                self.events.append({'case': name, 'unexpected_model_request': parsed.path})
                route.abort('blockedbyclient')
            else:
                route.continue_()

        context.route('**/*', intercept)
        started = time.perf_counter()
        result = {'case': name, 'pass': False}
        try:
            page.goto(f'{self.base}/web/console/#{path}', wait_until='networkidle')
            expect(page.locator('h1')).to_be_visible()
            callback(page, data)
            self.check(page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'), 'Page overflows horizontally')
            result['pass'] = True
        except Exception as error:
            result['error'] = f'{type(error).__name__}: {error}'[:2200]
        finally:
            try:
                page.screenshot(path=str(self.output / f'{name}.jpg'), type='jpeg', quality=82, full_page=False)
                result['evidence'] = f'{name}.jpg'
            except Exception as error:
                result['screenshot_error'] = str(error)[:300]
                result['pass'] = False
            result['seconds'] = round(time.perf_counter() - started, 3)
            self.results.append(result)
            context.unroute_all(behavior='ignoreErrors')
            context.close()


def regression_cases(runner):
    def dashboard_costs(page, data):
        expect(page.locator('.donut-chart > div > strong')).to_have_text('$90.00')
        expect(page.locator('.distribution-legend')).to_contain_text('其他模型')
        count = page.locator('.distribution-legend > div').count()
        runner.check(page.locator('.donut-chart-segment').count() == count, 'Every chart segment needs a legend row')
        runner.metrics['model_costs'] = {'total': 90, 'legend_rows': count, 'segments': count}
    runner.case('dashboard-costs', dashboard_costs, path='/')

    def cost_state(page, data):
        disclosure = page.locator('.token-instance-costs')
        if disclosure.count():
            runner.check(not disclosure.evaluate('(element) => element.open'), 'Instance-wide costs should start as secondary information')
            disclosure.locator(':scope > summary').click()
        panel = page.locator('.cost-summary-panel')
        state = panel.locator('.cost-summary-panel__state')
        expect(panel).to_be_visible()
        expected = {'cost-incomplete': ('incomplete', '覆盖不完整'),
                    'cost-complete': ('complete', '覆盖完整'),
                    'cost-empty': ('empty', '暂无成功请求'),
                    'cost-missing': ('missing', '依据缺失')}[data.case]
        expect(state).to_have_text(expected[1])
        expect(state).to_have_class(f'cost-summary-panel__state cost-summary-panel__state--{expected[0]}')
        if data.case == 'cost-incomplete':
            expect(panel).to_contain_text('1113 条待核对')
            runner.check('当时无可用模型价格' not in panel.inner_text(), 'Historical unknown coverage must not imply missing current prices')
    for case in ('cost-incomplete', 'cost-complete', 'cost-empty', 'cost-missing'):
        runner.case(case, cost_state)

    def channel_selection(page, data):
        rows = page.locator('.channel-row')
        expect(rows.first).to_be_visible()
        rows.first.get_by_role('checkbox').check()
        expect(page.locator('.batch-toolbar')).to_contain_text('已选择 1 项')
        page.get_by_role('button', name='下一页', exact=True).click()
        expect(rows.first).to_contain_text('251')
        expect(page.locator('.batch-toolbar')).to_contain_text('已选择 1 项')
        page.get_by_role('checkbox', name='选择当前页全部渠道（50 条）', exact=True).check()
        expect(page.locator('.batch-toolbar')).to_contain_text('已选择 51 项')
        page.get_by_role('checkbox', name='选择当前页全部渠道（50 条）', exact=True).uncheck()
        expect(page.locator('.batch-toolbar')).to_contain_text('已选择 1 项')
        page.get_by_role('button', name='上一页', exact=True).click()
        expect(rows.first.get_by_role('checkbox')).to_be_checked()
    runner.case('channel-selection', channel_selection, path='/channels')

    def connect_history(page, data):
        page.get_by_role('link', name='请求历史', exact=True).click()
        expect(page.get_by_role('tab', name='完整请求', exact=True)).to_have_attribute('aria-selected', 'true')
    runner.case('connect-history', connect_history, path='/connect')

    def routing_layout(page, data):
        expect(page.locator('.model-route-model').first).to_be_visible()
        sizes = page.evaluate("""() => {
          const inventory = document.querySelector('.model-route-inventory-results');
          const workspace = document.querySelector('.model-route-workspace');
          const pagination = document.querySelector('.model-route-inventory .pagination');
          const rect = workspace.getBoundingClientRect();
          return {documentHeight:document.documentElement.scrollHeight, viewport:innerHeight,
            inventoryHeight:inventory.clientHeight, inventoryScroll:inventory.scrollHeight,
            workspaceTop:rect.top, workspaceBottom:rect.bottom, paginationBottom:pagination.getBoundingClientRect().bottom};
        }""")
        runner.check(sizes['documentHeight'] <= sizes['viewport'] + 1, 'Routing page must stay inside desktop viewport')
        runner.check(sizes['inventoryScroll'] > sizes['inventoryHeight'], 'Long model inventory needs its own scroll area')
        runner.check(sizes['paginationBottom'] <= sizes['viewport'], 'Model pagination must remain visible')
        page.locator('.model-route-inventory-results').evaluate('(element) => element.scrollTop = 1000')
        runner.check(page.evaluate('scrollY') == 0, 'Inventory scroll must not scroll the whole page')
        runner.check(page.locator('.model-route-workspace').bounding_box()['y'] == sizes['workspaceTop'], 'Inventory scroll moved diagnostics')
        page.locator('.model-route-model').first.click()
        expect(page.locator('.model-route-channel').first).to_be_visible()
        page.screenshot(path=str(runner.output / f'{data.case}-selected.jpg'), type='jpeg', quality=82, full_page=False)
        inventory_scroll = page.locator('.model-route-inventory-results').evaluate('(element) => element.scrollTop')
        page.locator('.model-route-workspace').evaluate('(element) => element.scrollTop = 800')
        runner.check(page.evaluate('scrollY') == 0, 'Diagnostics scroll must not scroll the page')
        runner.check(page.locator('.model-route-inventory-results').evaluate('(element) => element.scrollTop') == inventory_scroll, 'Diagnostics scroll moved inventory')
        runner.metrics[data.case] = sizes
    for case, width, height, theme in (('routing-layout', 1440, 900, 'light'), ('routing-short-dark', 1280, 600, 'dark')):
        runner.case(case, routing_layout, path='/routing', width=width, height=height, theme=theme)

    def routing_mobile(page, data):
        chooser = page.get_by_role('button', name='更换模型', exact=True)
        expect(chooser).to_be_visible()
        expect(chooser).to_have_attribute('aria-expanded', 'false')
        expect(page.locator('.model-route-inventory-browser')).not_to_be_visible()
        chooser.click()
        expect(page.get_by_role('button', name='收起清单', exact=True)).to_have_attribute('aria-expanded', 'true')
        page.get_by_role('textbox', name='搜索模型或别名', exact=True).fill('model-alpha')
        expect(page.locator('.model-route-model')).to_have_count(1)
        page.locator('.model-route-model').click()
        expect(page.get_by_role('button', name='更换模型', exact=True)).to_have_attribute('aria-expanded', 'false')
        expect(page.get_by_role('textbox', name='请求模型', exact=True)).to_have_value('model-alpha')
    runner.case('routing-mobile', routing_mobile, path='/routing?model=group-core', width=390, height=844, touch=True)

    def routing_unicode_input(page, data):
        maximum = '😀' * 512
        model = page.get_by_role('textbox', name='请求模型', exact=True)
        model.fill(maximum)
        expect(model).to_have_value(maximum)
        model.press('End')
        model.press('x')
        expect(model).to_have_value(maximum)
        page.get_by_role('button', name='查看路由', exact=True).click()
        expect(page.locator('.model-route-snapshot h2')).to_have_text(maximum)
        runner.check(any(call['path'] == '/admin/routing/diagnostics' and call['query'].get('model') == [maximum] for call in data.calls), 'A 512-code-point model was changed before diagnosis')
        search = page.get_by_role('textbox', name='搜索模型或别名', exact=True)
        search.fill(maximum)
        expect(search).to_have_value(maximum)
        expect(page.locator('.model-route-inventory .model-route-empty-state')).to_be_visible()
        runner.check(any(call['path'] == '/admin/routing/models' and call['query'].get('search') == [maximum] for call in data.calls), '512-code-point inventory search was truncated')
        token = page.get_by_role('textbox', name='搜索路由令牌', exact=True)
        token.fill(maximum)
        expect(token).to_have_value(maximum)
        token.press('End')
        token.press('x')
        expect(token).to_have_value(maximum)
    runner.case('routing-unicode-input', routing_unicode_input, path='/routing')

    def connect_mobile(page, data):
        expect(page.get_by_role('heading', name='接入配置', exact=True)).to_be_visible()
        guide = page.locator('.connect-guide')
        runner.check(not guide.evaluate('(element) => element.open'), 'Connection guide must start collapsed')
        button = page.get_by_role('button', name='读取现有令牌', exact=True)
        expect(button).to_be_enabled()
        runner.check(button.bounding_box()['y'] + button.bounding_box()['height'] < 844, 'Primary token action must be in first mobile viewport')
        runner.check(not any(call['path'].endswith('/reveal') for call in data.calls), 'Existing token must not be revealed automatically')
        runner.check('3 / 5' not in guide.inner_text(), 'Configuration readiness must not be reported as completed tests')
        page.screenshot(path=str(runner.output / 'connect-mobile-default.jpg'), type='jpeg', quality=82, full_page=False)
        guide.locator(':scope > summary').click()
        expect(guide.locator('ol')).to_be_visible()
    runner.case('connect-mobile', connect_mobile, path='/connect', width=390, height=844, touch=True)

    def connect_invalid_config(page, data):
        guide = page.locator('.connect-guide')
        summary = guide.locator(':scope > summary')
        expect(summary).to_contain_text('0 个可用账号')
        expect(summary).to_contain_text('0 个可用渠道')
        expect(summary).to_contain_text('0 个已知模型')
        guide.locator(':scope > summary').click()
        expect(guide.locator('ol > li.is-ready')).to_have_count(0)
        runner.check(not any(call['method'] != 'GET' for call in data.calls), 'Inspecting readiness performed an action')
    runner.case('connect-invalid-config', connect_invalid_config, path='/connect')

    def model_test_shared_facts(page, data):
        summary = page.get_by_role('region', name='模型清单摘要', exact=True)
        expect(summary.locator('strong').first).to_have_text('1')
        page.get_by_role('textbox', name='搜索模型清单', exact=True).fill('shared-current-model')
        expect(page.locator('.model-card-grid')).to_contain_text('shared-current-model')
        expect(page.locator('.model-card-grid')).to_contain_text('current-eligible-account')
        action_label = re.compile(r'^(?:直测|渠道测试) shared-current-model$')
        action = page.locator('.model-card-grid').get_by_role('button', name=action_label)
        expect(action).to_be_enabled()
        action.click()
        expect(page.get_by_role('tab', name='连通测试', exact=True)).to_have_attribute('aria-selected', 'true')
        expect(page.get_by_role('combobox', name='站点账号', exact=True)).to_have_value('101')
        expect(page.get_by_role('combobox', name='模型', exact=True)).to_have_value('shared-current-model')
        page.get_by_role('tab', name='模型清单', exact=True).click()
        page.get_by_role('combobox', name='模型站点', exact=True).select_option(str(data.sites[1]['id']))
        expect(page.locator('.model-card-grid')).to_contain_text('second-site-current-account')
        page.locator('.model-card-grid').get_by_role('button', name=action_label).click()
        expect(page.get_by_role('combobox', name='站点账号', exact=True)).to_have_value('104')
        page.get_by_role('tab', name='模型清单', exact=True).click()
        page.get_by_role('group', name='模型状态筛选', exact=True).get_by_role('button', name='过期', exact=True).click()
        expect(page.locator('.model-card-grid')).to_contain_text('stale-account')
        expect(page.locator('.model-card-grid').get_by_role('button', name=action_label)).to_be_disabled()
        runner.check(not any(call['method'] != 'GET' for call in data.calls), 'Choosing a model started a billed test')
    runner.case('model-test-shared-facts', model_test_shared_facts, path='/models')

    def model_test_scoped_channel(page, data):
        action_label = '渠道测试 site-scoped-model'
        chooser = page.get_by_role('combobox', name='模型站点', exact=True)
        status = page.get_by_role('group', name='模型状态筛选', exact=True)
        expect(page.locator('.model-card-grid').get_by_role('button', name=action_label, exact=True)).to_be_enabled()
        expect(page.locator('.model-card-grid .model-pricing-strip')).to_contain_text('$10')
        runner.check(any(call['path'] == '/admin/site-pricing' and call.get('query', {}).get('site_id') == [str(data.sites[1]['id'])] for call in data.calls), 'Direct-test channel owner pricing was never requested')
        chooser.select_option(str(data.sites[0]['id']))
        expect(page.locator('.model-card-grid')).to_have_count(0)
        expect(page.get_by_text('没有符合条件的模型', exact=True)).to_be_visible()
        status.get_by_role('button', name='全部', exact=True).click()
        card = page.locator('.model-card-grid')
        expect(card).to_contain_text('selected-site-stale-account')
        expect(card.get_by_role('button', name=action_label, exact=True)).to_be_disabled()
        expect(card).not_to_contain_text('other-site-only-channel')
        expect(card.locator('.model-pricing-strip')).not_to_contain_text('$10')
        page.get_by_role('button', name='列表视图', exact=True).click()
        records = page.locator('.model-records')
        expect(records.get_by_role('button', name=action_label, exact=True)).to_be_disabled()
        expect(records).not_to_contain_text('other-site-only-channel')
        chooser.select_option(str(data.sites[1]['id']))
        status.get_by_role('button', name='可用', exact=True).click()
        expect(records).to_contain_text('other-site-only-channel')
        expect(records.locator('.model-price-line')).to_contain_text('$10')
        records.get_by_role('button', name=action_label, exact=True).click()
        expect(page.get_by_role('tab', name='连通测试', exact=True)).to_have_attribute('aria-selected', 'true')
        expect(page.get_by_role('combobox', name='渠道', exact=True)).to_have_value('501')
        expect(page.get_by_role('combobox', name='模型', exact=True)).to_have_value('site-scoped-model')
        runner.check(not any(call['method'] != 'GET' for call in data.calls), 'Inspecting a scoped model started a billed test')
    for name in ('model-test-scoped-channel-direct', 'model-test-scoped-channel-binding'):
        runner.case(name, model_test_scoped_channel, path='/models')

    def tokens_density(page, data):
        expect(page.get_by_role('region', name='令牌摘要')).to_contain_text('$10.00')
        expect(page.get_by_role('region', name='令牌摘要')).to_contain_text('累计额度用量')
        runner.check(not page.locator('.token-instance-costs').evaluate('(element) => element.open'), 'Global logs must start collapsed')
        expect(page.locator('.token-table-head')).to_contain_text('今日日志费用')
        runner.check(page.locator('.token-row').first.inner_text().count('创建时间') == 0, 'Row repeats the visible date header')
        trigger = page.get_by_role('button', name='令牌用量 的说明', exact=True)
        trigger.focus()
        expect(page.get_by_role('tooltip')).to_be_visible()
        page.keyboard.press('Escape')
        expect(page.get_by_role('tooltip')).not_to_be_visible()
        expect(trigger).to_be_focused()
    runner.case('tokens-density', tokens_density)

    def manual_checkin(page, data):
        expect(page.get_by_role('link', name='打开签到页', exact=True)).to_have_attribute('href', 'https://manual.example.invalid/checkin')
        expect(page.get_by_role('button', name='立即签到', exact=True)).to_have_count(0)
    for name in ('manual-checkin', 'manual-checkin-disabled', 'manual-checkin-unavailable'):
        runner.case(name, manual_checkin, path='/checkins')

    def site_probe(page, data):
        page.get_by_role('button', name=re.compile('^探测 ')).first.click()
        expect(page.locator('.operation-notice--warning')).to_contain_text('未识别到受支持的平台')
    runner.case('site-probe', site_probe, path='/sites')

    def site_batch(page, data):
        page.get_by_role('checkbox', name='选择当前页站点', exact=True).check()
        page.locator('.batch-toolbar').get_by_role('button', name='禁用', exact=True).click()
        expect(page.locator('.operation-notice--warning')).to_contain_text('1 个成功，2 个失败')
    runner.case('site-batch', site_batch, path='/sites')

    def mapping_validation(page, data):
        page.get_by_role('button', name='新增映射', exact=True).click()
        field = page.locator('#alias-canonical-0')
        expect(field).to_have_attribute('aria-invalid', 'true')
        page.get_by_role('button', name='保存更改', exact=True).click()
        expect(field).to_be_focused()
        runner.check(not data.settings_writes, 'An empty canonical name reached settings API')
        field.fill('DeepSeek-V4.1-Flash')
        page.get_by_role('button', name='新增映射', exact=True).click()
        field.fill('deepseek-v4.1-flash')
        expect(field).to_have_attribute('aria-invalid', 'true')
        page.get_by_role('button', name='保存更改', exact=True).click()
        expect(field).to_be_focused()
        runner.check(not data.settings_writes, 'Case-folded duplicate canonical names reached API')
    runner.case('mapping-validation', mapping_validation, path='/system?section=runtime&group=routing')

    def settings_units(page, data):
        capacity = page.locator('[data-setting-key="request_cache_max_bytes"] input')
        entry = page.locator('[data-setting-key="request_cache_max_entry_bytes"] input')
        expect(capacity).to_have_value('32')
        expect(entry).to_have_value('1024')
        expect(page.locator('[data-setting-key="request_cache_max_bytes"]')).to_contain_text('MiB')
        expect(page.locator('[data-setting-key="request_cache_max_entry_bytes"]')).to_contain_text('KiB')
        capacity.fill('48.5')
        entry.fill('128.5')
        page.on('dialog', lambda dialog: dialog.accept())
        page.get_by_role('button', name='保存更改', exact=True).click()
        page.wait_for_function("document.querySelector('.settings-savebar') === null")
        runner.check(len(data.settings_writes) == 1, 'Settings batch count differs')
        runner.check(data.settings_writes[0].get('request_cache_max_bytes') == '50855936', 'MiB conversion lost bytes')
        runner.check(data.settings_writes[0].get('request_cache_max_entry_bytes') == '131584', 'KiB conversion lost bytes')
    runner.case('settings-units', settings_units, path='/system?section=runtime&group=cache')

    def settings_invalid_units(page, data):
        capacity = page.locator('[data-setting-key="request_cache_max_bytes"] input')
        capacity.fill('129')
        expect(capacity).to_have_attribute('aria-invalid', 'true')
        page.locator('[data-setting-key="request_cache_max_entry_bytes"] input').fill('128')
        page.get_by_role('button', name='保存更改', exact=True).click()
        expect(capacity).to_be_focused()
        runner.check(not data.settings_writes, 'An invalid capacity draft allowed a partial settings batch')
    runner.case('settings-invalid-units', settings_invalid_units, path='/system?section=runtime&group=cache')

    def wallpaper_validation(page, data):
        field = page.get_by_role('textbox', name='背景壁纸图片地址', exact=True)
        field.fill('ht')
        field.press('Tab')
        expect(field).to_have_attribute('aria-invalid', 'true')
        expect(page.locator('#appearance-wallpaper-error')).to_contain_text('未应用')
        runner.check(page.evaluate("JSON.parse(localStorage.getItem('pivotflow_appearance') || '{}').backgroundImage || ''") == '', 'Invalid wallpaper changed the applied appearance')
        runner.check(page.locator('.operation-notice--success').count() == 0, 'Invalid wallpaper reported success')
        page.screenshot(path=str(runner.output / 'wallpaper-validation-invalid.jpg'), type='jpeg', quality=82, full_page=False)
        image_url = f'{runner.base}/web/favicon.svg'
        field.fill(image_url)
        field.press('Tab')
        expect(page.locator('.operation-notice--success')).to_contain_text('背景壁纸已应用')
        expect(page.locator('html')).to_have_attribute('data-wallpaper', 'on')
        field.fill('')
        field.press('Tab')
        expect(page.locator('.operation-notice--success')).to_contain_text('背景壁纸已清除')
        expect(page.locator('html')).to_have_attribute('data-wallpaper', 'off')
    runner.case('wallpaper-validation', wallpaper_validation, path='/system?section=runtime&group=appearance')

    def health_normal(page, data):
        expect(page.get_by_role('region', name='当前运行状态')).to_be_visible()
        expect(page.locator('#runtime-attention')).to_have_count(0)
        runner.check(page.locator('.health-card-explanation:visible').count() == 0, 'Normal cards repeat static explanations')
        runner.check(page.locator('#runtime-billing .health-metrics > div:visible').count() <= 2, 'Normal billing card is not compact')
        for title in ('维护与恢复', '其他组件'):
            section = page.locator('details.health-section').filter(has_text=title)
            runner.check(not section.evaluate('(element) => element.open'), f'{title} should start collapsed')
    runner.case('health-normal', health_normal, path='/health')
    runner.case('health-mobile-dark', health_normal, path='/health', width=390, height=844, theme='dark')

    def health_issues(page, data):
        attention = page.locator('#runtime-attention')
        expect(attention).to_be_visible()
        expect(attention).to_contain_text('2 条结算事件仍仅在内存')
        expect(attention).to_contain_text('5 条最终未保存')
        expect(attention).to_contain_text('4 条被丢弃')
        expect(attention).to_contain_text('3 个任务失败')
        expect(attention).to_contain_text('副本')
        attention.get_by_role('link').first.click()
        runner.check(page.evaluate("location.hash.startsWith('#/health')"), 'A component jump left the hash-router health page')
        expect(page.locator('#runtime-billing')).to_be_visible()
        expect(page.locator('#runtime-billing')).to_be_focused()
        runner.check(page.evaluate("location.hash.endsWith('component=billing')"), 'Component link cannot be shared or restored')
    runner.case('health-issues', health_issues, path='/health')

    def health_retained_loss(page, data):
        overview = page.locator('.health-overview')
        expect(overview).to_have_class('health-overview health-overview--warning')
        expect(overview).to_contain_text('需要关注')
        expect(page.locator('#runtime-attention')).to_contain_text('5 条最终未保存')
        page.get_by_role('link', name='系统概览', exact=True).click()
        expect(page.locator('.runtime-summary')).to_have_class('runtime-summary runtime-summary--warning')
    runner.case('health-retained-loss', health_retained_loss, path='/health')

    def health_deeplink(page, data):
        target = page.locator('#runtime-debug_cleanup')
        expect(target).to_be_visible()
        expect(target).to_be_focused()
        runner.check(target.evaluate('(element) => element.closest("details").open'), 'Component deep link did not reveal its folded section')
        previous_reads = sum(call['path'] == '/admin/runtime-metrics' for call in data.calls)
        refresh = page.get_by_role('button', name='刷新', exact=True)
        refresh.click()
        expect(refresh).to_be_enabled()
        runner.check(sum(call['path'] == '/admin/runtime-metrics' for call in data.calls) > previous_reads, 'Refresh did not get a new snapshot')
        expect(target).not_to_be_focused()
        runner.check(page.evaluate('scrollY') < 150, 'A new health snapshot scrolled back to the old component target')
    runner.case('health-deeplink', health_deeplink, path='/health?component=debug_cleanup', width=390, height=844, touch=True)

    def health_stale(page, data):
        expect(page.locator('.health-overview')).to_be_visible()
        data.fail_runtime = True
        page.get_by_role('button', name='刷新', exact=True).click()
        expect(page.locator('.health-page')).to_have_attribute('data-snapshot-status', 'stale')
        expect(page.locator('.health-refresh-error')).to_contain_text('当前状态未知')
        expect(page.locator('#runtime-attention')).to_contain_text('快照已过期')
        expect(page.locator('.health-overview')).to_have_class('health-overview health-overview--unknown')
        if data.case == 'health-stale-issues':
            expect(page.locator('#runtime-attention')).to_contain_text('2 条结算事件仍仅在内存')
    runner.case('health-stale', health_stale, path='/health')
    runner.case('health-stale-issues', health_stale, path='/health')

    def import_navigation(page, data):
        expect(page.get_by_role('navigation', name='主导航')).not_to_contain_text('浏览器导入')
        page.get_by_role('link', name='导入账号', exact=True).click()
        expect(page.get_by_role('heading', name='导入账号', exact=True)).to_be_visible()
        sources = page.get_by_role('group', name='账号资料来源')
        sources.get_by_role('button', name='浏览器页面助手', exact=True).click()
        expect(page.locator('.browser-import-helper')).to_have_attribute('open', '')
        sources.get_by_role('button', name='All API Hub 文件', exact=True).click()
        runner.check(not page.locator('.browser-import-helper').evaluate('(element) => element.open'), 'Switching source did not collapse helper')
    runner.case('import-navigation', import_navigation, path='/accounts')

    if runner.wallpaper:
        def photo_help(page, data):
            expect(page.locator('html')).to_have_attribute('data-wallpaper', 'on')
            expect(page.locator('html')).to_have_attribute('data-glass', 'on')
            if data.case.startswith('connect-'):
                guide = page.locator('.connect-guide')
                runner.check(not guide.evaluate('(element) => element.open'), 'Mobile guide obscures primary controls')
                button = page.get_by_role('button', name='读取现有令牌', exact=True)
                expect(button).to_be_enabled()
                runner.check(button.bounding_box()['y'] + button.bounding_box()['height'] < page.viewport_size['height'], 'Primary connection action is below the mobile viewport')
            page.screenshot(path=str(runner.output / f'{data.case}-default.jpg'), type='jpeg', quality=82, full_page=False)
            trigger = page.locator('.help-tip-trigger:visible').first
            trigger.click()
            tooltip = page.get_by_role('tooltip')
            expect(tooltip).to_be_visible()
            bounds = tooltip.bounding_box()
            runner.check(bounds['x'] >= 0 and bounds['y'] >= 0 and bounds['x'] + bounds['width'] <= page.viewport_size['width'] + 1 and bounds['y'] + bounds['height'] <= page.viewport_size['height'] + 1, 'Help tooltip is clipped by the viewport')
            page.mouse.move(1, 1)
            expect(tooltip).to_be_visible()
            page.screenshot(path=str(runner.output / f'{data.case}-help.jpg'), type='jpeg', quality=82, full_page=False)
            page.keyboard.press('Escape')
            expect(tooltip).not_to_be_visible()
        for name, path, width, height, theme, touch in (
            ('routing-photo-light-glass', '/routing?model=group-core', 1440, 900, 'light', False),
            ('tokens-photo-dark-glass', '/tokens', 1440, 900, 'dark', False),
            ('health-photo-light-glass', '/health', 1440, 900, 'light', False),
            ('connect-photo-mobile-glass', '/connect', 390, 844, 'dark', True),
        ):
            runner.case(name, photo_help, path=path, width=width, height=height, theme=theme, touch=touch, wallpaper=True, glass=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--case', action='append', default=[])
    parser.add_argument('--wallpaper-fixture', type=Path, help='Local JPEG used only by synthetic photo/glass checks')
    args = parser.parse_args()
    root, output = args.root.resolve(), args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(('127.0.0.1', 0), partial(StaticHandler, root=root))
    server.daemon_threads = True
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    result = {'pass': False, 'server_closed': False}
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True, args=['--no-proxy-server', '--disable-background-networking'])
            try:
                runner = Runner(browser, f'http://127.0.0.1:{server.server_port}', output, args.case, args.wallpaper_fixture)
                regression_cases(runner)
                result.update(checks=runner.results, metrics=runner.metrics, unexpected_events=runner.events)
            finally:
                browser.close()
    except Exception as error:
        result['fatal_error'] = f'{type(error).__name__}: {error}'
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    result['server_closed'] = not thread.is_alive()
    result['pass'] = bool(result.get('checks')) and all(check['pass'] for check in result['checks']) and not result.get('unexpected_events') and not result.get('fatal_error') and result['server_closed']
    (output / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'pass': result['pass'], 'checks': len(result.get('checks', [])), 'unexpected_events': len(result.get('unexpected_events', [])), 'server_closed': result['server_closed'], 'fatal_error': result.get('fatal_error', '')}, ensure_ascii=False))
    raise SystemExit(0 if result['pass'] else 1)


if __name__ == '__main__':
    main()
