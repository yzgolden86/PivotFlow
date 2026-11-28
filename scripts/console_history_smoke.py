"""Verify request-history loading and actual upstream models with synthetic APIs.

The built React console is served locally; all admin calls are intercepted and
no real credentials, databases, upstream requests or mutations are used.
"""
import argparse
import copy
import json
import re
from functools import partial
from http.server import ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import parse_qs, urlsplit

from playwright.sync_api import expect, sync_playwright

import docs_screenshots as fixtures
from model_routes_smoke import history_stats
from ui_accessibility_smoke import StaticHandler
from ui_refinement_smoke import RefinementData, Runner


CANONICAL = 'deepseek-v4.1-flash'
ACTUAL = 'channel1/DeepSeek-V4.1-Flash-1017'
CHANNEL = 'synthetic fallback channel'


class HistoryData(RefinementData):
    def __init__(self, case):
        super().__init__(case)
        self.pending_stats = []
        self.hold_stats = case == 'history-slow-stats'
        self.fail_stats = case == 'history-stats-error'
        self.channel_name = '超长渠道_' + 'very-long-name/' * 12 if case.startswith('history-channel-long') else CHANNEL
        self.request_traces = []
        for number in range(1, 42):
            started_at = fixtures.NOW - number * 1000
            if number % 5 == 0:
                outcome, upstream_attempts, upstream_successes = 'error', 2, 0
                attempt_summaries = [
                    {'number': 1, 'started_at': started_at, 'status_code': 503, 'outcome': 'error', 'upstream': True},
                    {'number': 2, 'started_at': started_at + 500, 'status_code': 429, 'outcome': 'error', 'upstream': True},
                ]
            elif number % 2 == 0:
                outcome, upstream_attempts, upstream_successes = 'success', 1, 1
                attempt_summaries = [
                    {'number': 1, 'started_at': started_at, 'status_code': 200, 'outcome': 'success', 'upstream': True},
                ]
            else:
                outcome, upstream_attempts, upstream_successes = 'success', 2, 1
                attempt_summaries = [
                    {'number': 1, 'started_at': started_at, 'status_code': 401, 'outcome': 'error', 'upstream': True},
                    {'number': 2, 'started_at': started_at + 500, 'status_code': 200, 'outcome': 'success', 'upstream': True},
                ]
            self.request_traces.append({
                **copy.deepcopy(fixtures.REQUEST_TRACES[0]),
                'request_id': f'pf_req_{number:032x}', 'model': CANONICAL,
                'actual_model': ACTUAL, 'started_at': started_at,
                'channel_id': 102, 'channel_name': self.channel_name,
                'outcome': outcome, 'final_status': 200 if outcome == 'success' else 503,
                'attempt_count': upstream_attempts, 'upstream_attempt_count': upstream_attempts,
                'upstream_success_count': upstream_successes,
                'attempt_summaries': attempt_summaries, 'truncated': False,
            })
        if case == 'history-channel-empty':
            self.request_traces = self.request_traces[:3]
            for index, trace in enumerate(self.request_traces):
                trace.update(channel_id=0, channel_name='')
                if index < 2:
                    trace.update(actual_model='', upstream_attempt_count=0, upstream_success_count=0, attempt_count=0, attempt_summaries=[])
                else:
                    trace['truncated'] = True
            self.request_traces[0].update(cache_status='hit', cache_reason='reused')
        if case.startswith('history-layout-'):
            trace = self.request_traces[0]
            trace.update(attempt_count=8, upstream_attempt_count=8, upstream_success_count=1)
            trace['attempt_summaries'] = [
                {'number': index + 1, 'started_at': trace['started_at'] + index * 1000,
                 'status_code': 200 if index == 7 else 403,
                 'outcome': 'success' if index == 7 else 'error', 'upstream': True}
                for index in range(8)
            ]

    def release_stats(self):
        self.hold_stats = False
        for route in self.pending_stats:
            self.fulfill(route, history_stats(self.request_traces))
        self.pending_stats.clear()

    def route(self, route):
        parsed = urlsplit(route.request.url)
        path, query = parsed.path, parse_qs(parsed.query)
        if path == '/admin/request-traces':
            self.calls.append({'path': path, 'method': route.request.method, 'query': query})
            items = self._history_items(query)
            offset = int(query.get('offset', ['0'])[0])
            limit = int(query.get('limit', ['20'])[0])
            summaries = [{key: value for key, value in item.items() if key != 'attempts'} for item in items[offset:offset + limit]]
            self.fulfill(route, summaries, len(items))
            return
        if path == '/admin/request-traces/stats':
            self.calls.append({'path': path, 'method': route.request.method, 'query': query})
            if self.hold_stats:
                self.pending_stats.append(route)
            elif self.fail_stats:
                route.fulfill(status=503, content_type='application/json', body='{"success":false,"error":"synthetic_stats_unavailable"}')
            else:
                self.fulfill(route, history_stats(self._history_items(query)))
            return
        if path.startswith('/admin/request-traces/'):
            self.calls.append({'path': path, 'method': route.request.method, 'query': query})
            request_id = path.rsplit('/', 1)[-1]
            trace = next((item for item in self.request_traces if item['request_id'] == request_id), self.request_traces[0])
            self.fulfill(route, {**trace, 'attempts': [
                fixtures.trace_attempt(1, trace['started_at'], outcome='error', reason='upstream_auth', status_code=401, duration=.2, actual_model='vendor/failed-variant', channel_id=101, channel_name='synthetic primary channel'),
                fixtures.trace_attempt(2, trace['started_at'] + 250, outcome='success', reason='', status_code=200, duration=.5, actual_model=ACTUAL, channel_id=102, channel_name=trace['channel_name']),
            ]})
            return
        if path == '/admin/logs':
            self.calls.append({'path': path, 'method': route.request.method, 'query': query})
            entry = {
                'id': 1, 'time': fixtures.NOW, 'channel_id': 101, 'channel_name': 'synthetic channel',
                'model': CANONICAL, 'actual_model': ACTUAL, 'request_id': self.request_traces[0]['request_id'],
                'log_source': 'proxy', 'status_code': 200, 'duration': .5, 'first_byte_time': .1,
                'client_protocol': 'openai', 'upstream_protocol': 'openai', 'auth_token_id': 7,
                'auth_token_description': 'synthetic token', 'input_tokens': 10, 'output_tokens': 5,
                'cost': 0, 'cost_multiplier': 1, 'effective_cost': 0,
            }
            self.fulfill(route, [entry], 1)
            return
        super().route(route)

    def _history_items(self, query):
        items = super()._history_items(query)
        if query.get('upstream_failures') == ['1']:
            items = [item for item in items if item['upstream_attempt_count'] > item['upstream_success_count']]
        return items


def history_cases(runner):
    def desktop_layout(page, data):
        expect(page.locator('.request-ledger-row').first).to_be_visible()
        sizes = page.locator('.request-filter-bar > select').evaluate_all('''fields => fields.map((el, index) => {
          const style = getComputedStyle(el), canvas = document.createElement('canvas'), context = canvas.getContext('2d');
          context.font = `${style.fontWeight} ${style.fontSize} ${style.fontFamily}`;
          return {index, width: el.getBoundingClientRect().width,
            required: context.measureText(el.selectedOptions[0].text).width + parseFloat(style.paddingLeft) + parseFloat(style.paddingRight) + 18};
        })''')
        runner.metrics.setdefault('desktop_filter_sizes', {})[str(page.viewport_size['width'])] = sizes
        runner.check(all(item['width'] >= item['required'] for item in sizes),
                     f'Selected filter labels must fit beside their native arrows: {sizes}')
        row = page.locator('.request-ledger-row').first
        geometry = row.evaluate('''el => {
          const row = el.getBoundingClientRect(), attempt = el.children[2].getBoundingClientRect();
          return {start: (attempt.x-row.x)/row.width, share: attempt.width/row.width};
        }''')
        runner.check(geometry['start'] <= .57 and geometry['share'] >= .30,
                     f'Retry sequence needs space before timing column: {geometry}')
        sequence = row.locator('.request-attempt-sequence')
        expect(sequence.locator('.request-attempt-step')).to_have_count(8)
        sequence.evaluate('el => { el.scrollLeft = el.scrollWidth }')
        runner.check(sequence.locator('.request-attempt-step').last.evaluate('el => {const a=el.getBoundingClientRect(),b=el.parentElement.getBoundingClientRect();return a.right<=b.right+1&&a.x>=b.x-1}'),
                     'The final success must remain reachable in a long retry sequence')
    for width in (1920, 1440, 1280, 1024):
        runner.case(f'history-layout-{width}', desktop_layout, path='/logs?view=requests', width=width)

    def independent_loading(page, data):
        rows = page.locator('.request-ledger-row')
        try:
            expect(rows.first).to_be_visible(timeout=2500)
            runner.check(bool(data.pending_stats), 'The statistics call was not actually held')
            expect(rows.first).to_contain_text(CANONICAL)
            expect(rows.first).to_contain_text(ACTUAL)
            expect(page.locator('.request-stats-strip')).to_have_attribute('aria-busy', 'true')
            runner.metrics['list_ready_while_stats_pending'] = True
        finally:
            data.release_stats()
        expect(page.locator('.request-stats-strip')).to_have_attribute('aria-busy', 'false')
        expect(page.locator('.request-stat-card').first).to_contain_text('41')
    runner.case('history-slow-stats', independent_loading, path='/logs?view=requests', wait_until='domcontentloaded')

    def failed_stats(page, data):
        expect(page.locator('.request-ledger-row').first).to_be_visible()
        expect(page.get_by_role('button', name='重试请求汇总', exact=True)).to_be_visible()
        expect(page.locator('.request-stat-card').first.locator('strong')).to_have_text('—')
        data.fail_stats = False
        page.get_by_role('button', name='重试请求汇总', exact=True).click()
        expect(page.locator('.request-stat-card').first.locator('strong')).to_have_text('41')
        expect(page.locator('.request-ledger-row')).to_have_count(20)
    runner.case('history-stats-error', failed_stats, path='/logs?view=requests')

    def models_and_details(page, data):
        row = page.locator('.request-ledger-row').first
        expect(row).to_contain_text(CANONICAL)
        expect(row).to_contain_text(ACTUAL)
        expect(row.locator('.request-channel-name')).to_have_text(CHANNEL)
        expect(row).not_to_contain_text(data.request_traces[0]['request_id'])
        runner.check(not any(call['path'].startswith('/admin/request-traces/') and call['path'] != '/admin/request-traces/stats' for call in data.calls), 'Channel labels requested per-row details')
        reads_before = sum(call['path'] == '/admin/request-traces' for call in data.calls)
        stats_before = sum(call['path'] == '/admin/request-traces/stats' for call in data.calls)
        row.click()
        expect(page.locator('.request-detail-facts > span').filter(has_text='实际渠道')).to_contain_text(CHANNEL)
        expect(page.locator('.request-detail-hero')).to_contain_text(data.request_traces[0]['request_id'])
        actual = page.locator('.request-detail-actual-model')
        expect(actual).to_contain_text(ACTUAL)
        runner.check(actual.evaluate('(element) => element.scrollWidth <= element.clientWidth + 1'), 'Detail clips the actual upstream model')
        expect(page.locator('.request-timeline')).to_contain_text('vendor/failed-variant')
        runner.check(sum(call['path'] == '/admin/request-traces' for call in data.calls) == reads_before, 'Opening detail fetched the request list again')
        runner.check(sum(call['path'] == '/admin/request-traces/stats' for call in data.calls) == stats_before, 'Opening detail aggregated the range again')
    runner.case('history-models-detail', models_and_details, path='/logs?view=requests')
    runner.case('history-models-mobile', models_and_details, path='/logs?view=requests', width=390, height=844, theme='dark', touch=True)

    def visible_attempt_failures(page, data):
        row = page.locator('.request-ledger-row').first
        expect(row.locator('.status-badge--success')).to_be_visible()
        steps = row.locator('.request-attempt-step')
        expect(steps).to_have_count(2)
        expect(steps.nth(0)).to_contain_text('401')
        expect(steps.nth(0)).to_contain_text('失败')
        expect(steps.nth(1)).to_contain_text('200')
        expect(steps.nth(1)).to_contain_text('成功')
        expect(steps.nth(0).locator('time')).to_be_visible()
        expect(steps.nth(1).locator('time')).to_be_visible()
        expect(row).to_have_class(re.compile('request-ledger-row--retry-error'))

        page.get_by_role('combobox', name='上游尝试状态').select_option('1')
        expect(page.locator('.request-ledger-row').first).to_be_visible()
        runner.check(any(call.get('query', {}).get('upstream_failures') == ['1'] for call in data.calls if call['path'] == '/admin/request-traces'), 'Failed-attempt filter was not sent to the server')
        runner.check(all('request-ledger-row--retry-error' in value for value in page.locator('.request-ledger-row').evaluate_all('rows => rows.map(row => row.className)')), 'Failure-only filter included a request with no failed attempt')

        page.get_by_role('combobox', name='最终结果').select_option('error')
        expect(page.locator('.request-ledger-row').first.locator('.status-badge--danger')).to_be_visible()
        expect(page.locator('.request-ledger-row')).to_have_count(8)
    runner.case('history-attempt-visibility', visible_attempt_failures, path='/logs?view=requests')
    runner.case('history-attempt-visibility-mobile', visible_attempt_failures, path='/logs?view=requests', width=390, height=844, theme='dark', touch=True)

    def page_without_aggregation(page, data):
        expect(page.locator('.request-ledger-row')).to_have_count(20)
        expect(page.locator('.request-stat-card').first).to_contain_text('41')
        stats_before = sum(call['path'] == '/admin/request-traces/stats' for call in data.calls)
        page.get_by_role('button', name='下一页', exact=True).click()
        expect(page.locator('.request-ledger-row').first).to_have_attribute('aria-label', re.compile('pf_req_00000000000000000000000000000015'))
        runner.check(sum(call['path'] == '/admin/request-traces/stats' for call in data.calls) == stats_before, 'Pagination repeated whole-range aggregation')
    runner.case('history-pagination', page_without_aggregation, path='/logs?view=requests')

    def lightweight_tokens(page, data):
        expect(page.locator('.request-ledger-row').first).to_be_visible()
        expect(page.get_by_role('combobox', name='完整请求访问令牌').locator('option')).not_to_have_count(1)
        reads = [call for call in data.calls if call['path'] == '/admin/auth-tokens']
        runner.check(bool(reads), 'Token options never loaded')
        runner.check(all('range' not in call.get('query', {}) for call in reads), 'Token dropdown requested RPM/cost range statistics')
    runner.case('history-lightweight-tokens', lightweight_tokens, path='/logs?view=requests')

    def channel_search(page, data):
        request_id = data.request_traces[0]['request_id']
        page.get_by_role('textbox', name='搜索完整请求', exact=True).fill(request_id)
        expect(page.locator('.request-ledger-row')).to_have_count(1)
        expect(page.locator('.request-channel-name')).to_have_text(CHANNEL)
        expect(page.locator('.request-ledger-row')).not_to_contain_text(request_id)
        page.locator('.request-ledger-row').click()
        expect(page.locator('.request-detail-hero')).to_contain_text(request_id)
    runner.case('history-channel-search', channel_search, path='/logs?view=requests')

    def long_channel(page, data):
        name = page.locator('.request-channel-name').first
        expect(name).to_have_text(data.channel_name)
        runner.check(data.channel_name in name.get_attribute('title'), 'Long channel name is unavailable in the tooltip')
        runner.check(name.evaluate('el => {const r=el.getBoundingClientRect(),p=el.parentElement.getBoundingClientRect();return r.x>=p.x-1&&r.right<=p.right+1&&getComputedStyle(el).textOverflow==="ellipsis";}'), 'Long channel name overflows the identity column')
        page.locator('.request-ledger-row').first.click()
        expect(page.locator('.request-detail-facts > span').filter(has_text='实际渠道')).to_have_attribute('title', re.compile(re.escape(data.channel_name)))
    runner.case('history-channel-long', long_channel, path='/logs?view=requests')
    runner.case('history-channel-long-mobile', long_channel, path='/logs?view=requests', width=390, height=844, theme='dark', touch=True)

    def channel_empty(page, data):
        labels = page.locator('.request-channel-name')
        expect(labels).to_have_text(['缓存命中 · 未请求上游', '未请求上游', '渠道未记录'])
        runner.check(not any(call['path'].startswith('/admin/request-traces/') and call['path'] != '/admin/request-traces/stats' for call in data.calls), 'Unknown channels requested per-row details')
    runner.case('history-channel-empty', channel_empty, path='/logs?view=requests')

    def mobile_filters(page, data):
        expect(page.locator('.request-ledger-row').first).to_be_visible()
        dimensions = page.locator('.request-filter-bar > select,.request-token-search,.request-search,.request-model-filter').evaluate_all('''fields => fields.map(el => {
          const box=el.getBoundingClientRect(),parent=el.parentElement.getBoundingClientRect(),style=getComputedStyle(el);
          return {className:el.className,height:box.height,width:box.width,parentWidth:parent.width,
            flexBasis:style.flexBasis,parentDirection:getComputedStyle(el.parentElement).flexDirection};
        })''')
        runner.metrics['mobile_request_filter_geometry'] = dimensions
        runner.metrics.setdefault('mobile_request_filter_geometries', []).append({'width': page.viewport_size['width'], 'fields': dimensions})
        runner.check(all(item['height'] <= 48 for item in dimensions), 'Single-line request filters expand into tall boxes')
        if page.viewport_size['width'] <= 620:
            runner.check(all(item['width'] >= item['parentWidth'] - 34 for item in dimensions), 'Mobile search and model filters do not fill their available row')
            clear = page.get_by_role('button', name='清除完整请求筛选', exact=True).bounding_box()
            refresh = page.get_by_role('button', name='刷新完整请求', exact=True).bounding_box()
            runner.check(abs(clear['y'] - refresh['y']) <= 1, 'Mobile clear and refresh actions occupy separate rows')
    runner.case('history-mobile-filters', mobile_filters, path='/logs?view=requests&range=this_month', width=390, height=844, touch=True)
    runner.case('history-mobile-filters-600', mobile_filters, path='/logs?view=requests&range=this_month', width=600, height=900, touch=True)
    runner.case('history-mobile-filters-620', mobile_filters, path='/logs?view=requests&range=this_month', width=620, height=900, touch=True)
    runner.case('history-mobile-filters-760', mobile_filters, path='/logs?view=requests&range=this_month', width=760, height=900, touch=True)

    def upstream_model_visible(page, data):
        row = page.locator('.log-records .record-row').first
        expect(row).to_contain_text(CANONICAL)
        expect(row).to_contain_text(ACTUAL)
        actual = row.locator('.log-actual-model')
        expect(actual).to_be_visible()
        runner.check(actual.evaluate('(element) => getComputedStyle(element).whiteSpace !== "nowrap"'), 'Actual upstream variant can be entirely hidden in a single ellipsized request-name line')
    runner.case('upstream-model-visible', upstream_model_visible, path='/logs?view=history')
    runner.case('upstream-model-mobile', upstream_model_visible, path='/logs?view=history', width=390, height=844, theme='dark', touch=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--case', action='append', default=[])
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
                runner = Runner(browser, f'http://127.0.0.1:{server.server_port}', output, args.case, data_factory=HistoryData)
                history_cases(runner)
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
