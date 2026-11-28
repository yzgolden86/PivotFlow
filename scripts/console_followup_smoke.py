"""Read-only balance, delayed-price and detail-dialog follow-up checks.

Uses built assets and synthetic intercepted API responses only. Evidence is
visible-viewport JPEG; no real gateway, database or upstream is contacted.
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

from console_layout_smoke import LayoutData, LayoutRunner, rect, reachable
from model_routes_smoke import build_diagnostic, diagnostic_channel
from ui_refinement_smoke import StaticHandler, cost_summary


class FollowupData(LayoutData):
    def __init__(self, case):
        super().__init__(case)
        self.held_prices = []
        self.hold_prices = case.startswith('model-pricing-delay-')
        self.fail_prices = '-failure-' in case
        if case.startswith('model-badges-wide-font-'):
            self.accounts[0]['enabled'] = False
            self.channels = []
            self.scoped_bindings = []
            for fact in self.site_facts:
                fact['route_type'] = 'openai_response'
        if case.startswith('model-pricing-account-scope-'):
            first = copy.deepcopy(self.accounts[0])
            second = copy.deepcopy(next(account for account in self.accounts if account['site_id'] != first['site_id']))
            first['enabled'] = second['enabled'] = True
            self.accounts = [first, second]
            prototype = self.site_facts[0]
            self.site_facts = [{**prototype, 'site_account_id': account['id']} for account in self.accounts]
            self.channels = []
            self.scoped_bindings = []
            self.pricing_snapshot['site_id'] = second['site_id']
            for price in self.pricing_snapshot['models']:
                price['groups'] = ['default']
        if case.startswith('model-pricing-invalid-call-'):
            price = next(price for price in self.pricing_snapshot['models'] if price['quota_type'] == 1)
            price.update(per_call_price=0, per_call_price_valid=False)
        if case.startswith('details-'):
            self.channels[0]['models'] = [
                {'model': f'vendor/detailed-model-{index:03d}', 'redirect_model': '', 'disabled': False}
                for index in range(80)
            ]
            self.channels[0]['urls'] = [{'url': 'https://long-address.example.invalid/' + 'segment-' * 35,
                                        'exact': False, 'protocols': ['openai', 'codex']}]

    def release_prices(self):
        self.hold_prices = False
        pending, self.held_prices = self.held_prices, []
        for route in pending:
            if self.fail_prices:
                route.fulfill(status=503, content_type='application/json',
                              body='{"success":false,"error":"synthetic_price_unavailable"}')
            else:
                super().route(route)

    def route(self, route):
        path = urlsplit(route.request.url).path
        if route.request.method == 'GET' and re.fullmatch(r'/admin/channels/201/route-diagnostics', path):
            query = parse_qs(urlsplit(route.request.url).query)
            diagnostic = build_diagnostic(query['model'][0], query.get('client_protocol', ['openai'])[0], '', 0, 15)
            policy = diagnostic['policy']
            self.calls.append({'path': path, 'method': 'GET', 'query': query})
            self.fulfill(route, {**policy, 'model': query['model'][0], 'client_protocol': query.get('client_protocol', ['openai'])[0],
                                 'target': diagnostic_channel(201), 'candidates': diagnostic['channels'],
                                 'token': diagnostic['token'], 'summary': diagnostic['summary'],
                                 'warnings': [], 'observed_at': diagnostic['observed_at']})
            return
        if path == '/admin/site-pricing' and self.hold_prices:
            self.held_prices.append(route)
            return
        if path == '/admin/stats' and self.case.startswith('balance-state-'):
            state = self.case.split('-')[2]
            point = {'day': '2026-11-28', 'currency': 'USD', 'balance': 123,
                     'daily_delta': 0 if state == 'zero' else None,
                     'comparison_status': 'complete' if state == 'zero' else state,
                     'accounts': 2, 'known_accounts': 1 if state == 'incomplete' else 2,
                     'updated_at': 1795795500000, 'scheduled_at': 1795795500000}
            self.calls.append({'path': path, 'method': route.request.method})
            self.fulfill(route, {'stats': [], 'duration_seconds': 3600, 'is_today': True,
                                'balance_history': [] if state in ('empty', 'disabled') else [point],
                                'balance_as_of_day': '2026-11-28',
                                'balance_schedule': {'enabled': state != 'disabled', 'time': '00:05',
                                                     'timezone': 'Asia/Shanghai'},
                                'cost_summary': cost_summary('empty')})
            return
        super().route(route)


class FollowupRunner(LayoutRunner):
    def make_data(self, name):
        data = FollowupData(name)
        data.unexpected = self.events
        return data


def price_geometry(runner, page, data):
    cards = page.locator('.model-card')
    expect(cards).to_have_count(8)
    first = cards.filter(has=page.locator('header code[title="layout-model-001"]'))
    strip = first.locator('.model-pricing-strip')
    expect(strip).to_have_attribute('aria-busy', 'true')
    expect(strip).to_contain_text('加载中')
    expect(first.locator('header button')).to_be_enabled()
    if '-list-' in data.case:
        page.get_by_role('button', name='列表视图', exact=True).click()
        items = page.locator('.model-records .record-row')
        prices = items.first.locator('.model-price-line')
    else:
        items, prices = cards, strip
    expect(prices).to_have_attribute('aria-busy', 'true')
    page.get_by_role('heading', name='模型测试', exact=True).scroll_into_view_if_needed()
    before = items.evaluate_all('(nodes) => nodes.map(node => {const r=node.getBoundingClientRect(); return [r.x,r.y,r.width,r.height]})')
    runner.photo(page, 'pending')
    data.release_prices()
    expect(prices).to_have_attribute('aria-busy', 'false')
    expect(prices).to_contain_text('读取失败' if data.fail_prices else '$2.00/M')
    after = items.evaluate_all('(nodes) => nodes.map(node => {const r=node.getBoundingClientRect(); return [r.x,r.y,r.width,r.height]})')
    runner.check(len(before) == len(after) and all(abs(a - b) <= 1 for left, right in zip(before, after)
                                                  for a, b in zip(left, right)),
                 'All catalog card/list bounds stay fixed when delayed prices settle')
    if data.fail_prices:
        expect(prices).not_to_contain_text('$0.00')
    elif '-list-' not in data.case:
        help_button = first.get_by_role('button', name='layout-model-001 价目 的说明', exact=True)
        help_button.click()
        tooltip = page.get_by_role('tooltip')
        expect(tooltip).to_contain_text('输入 $2.00/M')
        expect(tooltip).to_contain_text('输出 $8.00/M')
        box = rect(tooltip)
        runner.check(box['x'] >= -1 and box['right'] <= page.viewport_size['width'] + 1
                     and box['y'] >= -1 and box['bottom'] <= page.viewport_size['height'] + 1,
                     'Complete upstream prices are accessible inside the viewport')
        page.keyboard.press('Escape')
        expect(tooltip).to_have_count(0)
    if '-refresh-' in data.case:
        data.hold_prices, data.fail_prices = True, True
        page.get_by_role('button', name='刷新模型数据', exact=True).click()
        expect(prices).to_have_attribute('aria-busy', 'true')
        data.release_prices()
        expect(prices).to_have_attribute('aria-busy', 'false')
        expect(prices).to_have_attribute('data-state', 'stale')
        expect(prices).to_contain_text('$2.00/M')
        first.get_by_role('button', name='layout-model-001 价目 的说明', exact=True).click()
        expect(page.get_by_role('tooltip')).to_contain_text('旧价目')
        runner.photo(page, 'retained-stale-price')
        page.keyboard.press('Escape')
    runner.record(before=before, after=after, failure=data.fail_prices)


def narrow_model_badges(runner, page, data):
    page.evaluate('document.documentElement.style.setProperty("--font-ui", "Courier New, monospace")')
    page.get_by_role('group', name='模型状态筛选', exact=True).get_by_role('button', name='全部', exact=True).click()
    cards = page.locator('.model-card')
    expect(cards).to_have_count(8)
    expect(cards.first.locator('.status-badge')).to_have_text('无测试目标')
    geometry = cards.evaluate_all('''nodes => nodes.map(card => {
      const row = card.querySelector('.model-card-badges');
      const box = row.getBoundingClientRect();
      return {height: card.getBoundingClientRect().height, rowHeight: box.height,
        badges: Array.from(row.children, badge => {
          const b = badge.getBoundingClientRect();
          const range = document.createRange(); range.selectNodeContents(badge);
          return {text: badge.textContent, left: b.left - box.left, right: b.right - box.left,
            top: b.top - box.top, bottom: b.bottom - box.top,
            textHeight: range.getBoundingClientRect().height};
        }), width: box.width};
    })''')
    runner.record(badge_geometry=geometry)
    runner.photo(page, 'wide-font')
    runner.check(all(abs(card['height'] - 288) <= 1 and abs(card['rowHeight'] - 25) <= 1
                     for card in geometry), 'Wide fonts retain equal 288px cards and 25px badge rows')
    runner.check(all(badge['top'] >= -1 and badge['bottom'] <= card['rowHeight'] + 1
                     and badge['left'] >= -1 and badge['right'] <= card['width'] + 1
                     and badge['textHeight'] <= 25
                     for card in geometry for badge in card['badges']),
                 'Every badge and its text stay on one line inside the fixed badge row')
    for card in cards.all():
        labels = card.locator('.model-meta-badge')
        model = card.locator('header code').get_attribute('title')
        expect(labels.nth(0)).to_have_attribute('aria-label', f'{model} 路由协议 OpenAI Responses 的说明')
        expect(labels.nth(1)).to_have_attribute('aria-label', f'{model} 模型来源 站点模型接口 的说明')
        for badge, full_label in zip(labels.all(), ('OpenAI Responses', '站点模型接口')):
            text = badge.locator('.model-meta-badge-label')
            expect(text).to_have_text(full_label)
            runner.check('Courier New' in text.evaluate('node => getComputedStyle(node).fontFamily'),
                         'The regression exercises the wider user-selected UI font')
    for badge, full_label in zip(cards.first.locator('.model-meta-badge').all(), ('OpenAI Responses', '站点模型接口')):
        badge.scroll_into_view_if_needed()
        box = badge.bounding_box()
        page.touchscreen.tap(box['x'] + box['width'] / 2, box['y'] + box['height'] / 2)
        tooltip = page.get_by_role('tooltip')
        expect(tooltip).to_have_text(full_label)
        runner.check(rect(tooltip)['right'] <= page.viewport_size['width'] + 1,
                     'Tapped badge discloses its full label inside the viewport')
        page.keyboard.press('Escape')
        expect(tooltip).to_have_count(0)
        badge.focus()
        page.keyboard.press('Tab')
        page.keyboard.press('Shift+Tab')
        expect(badge).to_be_focused()
        expect(tooltip).to_have_text(full_label)
        page.keyboard.press('Escape')
        expect(tooltip).to_have_count(0)
    runner.check(all(card['badges'][index]['right'] <= card['badges'][index + 1]['left'] + 1
                     for card in geometry for index in range(2)), 'Adjacent badge bounds do not overlap')


def account_pricing_scope(runner, page, data):
    card = page.locator('.model-card')
    expect(card).to_have_count(1)
    expect(card.locator('.model-pricing-strip')).to_contain_text('$2.50/M')
    chooser = page.get_by_role('combobox', name='模型站点', exact=True)
    chooser.select_option(str(data.accounts[0]['site_id']))
    expect(card.locator('.model-pricing-strip')).to_contain_text('暂无价目')
    expect(card.locator('.model-pricing-strip')).not_to_contain_text('$2.50/M')
    chooser.select_option(str(data.accounts[1]['site_id']))
    expect(card.locator('.model-pricing-strip')).to_contain_text('$2.50/M')
    runner.record(sites=[account['site_id'] for account in data.accounts], account_only=True)


def invalid_per_call(runner, page, data):
    card = page.locator('.model-card').filter(has=page.locator('header code[title="layout-model-004"]'))
    price = card.locator('.model-pricing-strip')
    expect(price).to_have_attribute('aria-busy', 'false')
    expect(price).not_to_contain_text('$0.00')
    expect(price).to_contain_text('暂无价目')
    free = page.locator('.model-card').filter(has=page.locator('header code[title="layout-model-003"]'))
    expect(free.locator('.model-pricing-strip')).to_contain_text('$0.00/M')
    runner.record(malformed_per_call_not_free=True, explicit_free_token_preserved=True)


def balance_state(runner, page, data):
    state = data.case.split('-')[2]
    titles = {'baseline': '首日余额已记录', 'incomplete': '余额采样尚未完整',
              'empty': '等待首次余额采样', 'disabled': '每日余额采样已关闭'}
    panel = page.get_by_role('region', name='余额变化', exact=True)
    expect(panel).to_be_visible()
    if state == 'zero':
        expect(panel.locator('.balance-history-no-values')).to_have_count(0)
        expect(panel.locator('.balance-history-isolated')).to_have_count(1)
        runner.check(panel.locator('.balance-history-zero').evaluate('node => getComputedStyle(node).strokeOpacity') != '0',
                     'A measured zero retains its genuine zero reference')
    else:
        expect(panel.get_by_text(titles[state], exact=True)).to_be_visible()
        expect(panel.locator('.balance-history-line, .balance-history-isolated')).to_have_count(0)
        for zero in panel.locator('.balance-history-zero').all():
            runner.check(zero.evaluate('node => getComputedStyle(node).strokeOpacity') == '0',
                         'Absent comparison is never drawn as a zero-value line')
        help_button = panel.get_by_role('button', name='余额记录状态 的说明', exact=True)
        help_button.click()
        expect(page.get_by_role('tooltip')).to_be_visible()
        expect(page.get_by_role('tooltip')).to_contain_text('自动任务' if state == 'disabled' else '00:05')
        if state == 'baseline':
            expect(page.get_by_role('tooltip')).to_contain_text('2026-11-28')
        runner.photo(page, 'explanation')
        page.keyboard.press('Escape')
        expect(page.get_by_role('tooltip')).to_have_count(0)
    panel.get_by_role('button', name='7日', exact=True).click()
    expect(panel).to_have_attribute('data-balance-start-day', '2026-11-22')
    panel.get_by_role('button', name='14日', exact=True).click()
    expect(panel).to_have_attribute('data-balance-start-day', '2026-11-15')
    if state in ('baseline', 'incomplete') and page.viewport_size['width'] <= 700:
        hit = panel.locator('.balance-history-hit').nth(6)
        hit.scroll_into_view_if_needed()
        box = hit.bounding_box()
        page.touchscreen.tap(box['x'] + box['width'] / 2, box['y'] + box['height'] / 2)
        expect(panel.locator('.balance-history-crosshair')).to_have_attribute('data-day', hit.get_attribute('data-day'))
        page.keyboard.press('Escape')
    runner.record(state=state, range_days=[7, 14])


def entity_details(runner, page, data):
    entity = data.case.split('-')[1]
    opener = page.locator('.resource-details-trigger').first
    opener.click()
    dialog = page.get_by_role('dialog')
    expect(dialog).to_be_visible()
    facts = dialog.locator('.resource-detail-fact')
    expect(facts.first).to_be_visible()
    labels = {
        'sites': ['名称', '站点 ID', '站点地址', '平台', '状态', '时区', '代理方式', '账号', '最近探测', '探测说明', '签到方式', '签到方式检查时间', '辅助签到地址', '创建时间', '更新时间'],
        'tokens': ['名称', '令牌 ID', '令牌提示', '状态', '允许的模型', '允许的协议', '渠道限制模式', '渠道范围', '最大并发', '费用软限额', '今日费用', '限额使用金额', '请求统计', 'Token 统计', '创建时间', '最后使用', '到期时间'],
        'channels': ['名称', '渠道 ID', '状态', '来源', '同步归属', '关联账号', '认证类型', '协议转换', '地址与声明协议', '模型与映射', '基础优先级', '有效优先级', '窗口成功率', '成本倍率', 'RPM 限制', '最大并发', '每日费用软限额', 'Key 数量', '冷却范围', '渠道冷却结束', '可用时段', '代理方式', '自动健康检测'],
    }
    runner.check(facts.locator('dt').all_text_contents() == labels[entity], 'Exact entity facts remain present without replacement or duplication')
    name = opener.get_attribute('aria-label').removeprefix('查看 ').removesuffix(' 详情')
    fixture_names = [item['description' if entity == 'tokens' else 'name']
                     for item in {'sites': data.sites, 'channels': data.channels, 'tokens': data.tokens}[entity]]
    runner.check(name in fixture_names, 'The opened detail belongs to an actual fixture entity')
    expect(facts.first.locator('dd')).to_have_text(name)
    columns = dialog.locator('.resource-details-grid').first.evaluate('node => getComputedStyle(node).gridTemplateColumns.split(" ").length')
    runner.check(columns == (2 if page.viewport_size['width'] > 700 else 1),
                 'Entity facts use two desktop columns and one mobile column')
    overflow = facts.evaluate_all('(nodes) => nodes.filter(node => node.scrollWidth > node.clientWidth + 1).map(node => node.innerText)')
    runner.check(not overflow, f'Long entity values remain inside their fact cells: {overflow}')
    clipped = facts.locator('dd, a, code').evaluate_all('''nodes => nodes.filter(node => {
      const style = getComputedStyle(node);
      return ['hidden', 'clip'].includes(style.overflowX) && node.scrollWidth > node.clientWidth + 1;
    }).map(node => node.textContent)''')
    runner.check(not clipped, f'Entity values are not silently clipped: {clipped}')
    if entity == 'channels':
        address = dialog.get_by_role('link', name=data.channels[0]['urls'][0]['url'], exact=True)
        expect(address).to_have_attribute('href', data.channels[0]['urls'][0]['url'])
        address.scroll_into_view_if_needed()
        runner.check(address.evaluate('''node => {const box=node.closest('dd').getBoundingClientRect();
          return Array.from(node.getClientRects()).every(r=>r.x>=box.x-1&&r.right<=box.right+1)}'''),
                     'Every wrapped address fragment remains within its value column')
        runner.photo(page, 'full-address')
        models = dialog.locator('.resource-detail-list').filter(has_text='detailed-model-079')
        expect(models).to_have_count(1)
        state = rect(models)
        runner.check(state['scrollHeight'] > state['clientHeight'] and state['overflowY'] == 'auto',
                     'Long channel model lists use bounded local scrolling')
        models.focus()
        expect(models).to_be_focused()
        page.keyboard.press('End')
        expect(models.get_by_text('vendor/detailed-model-079', exact=True)).to_be_in_viewport()
    close = dialog.get_by_role('button', name='关闭弹窗', exact=True)
    reachable(runner, close, 'Entity detail close action')
    close.focus()
    page.keyboard.press('Tab')
    runner.check(dialog.evaluate('node => node.contains(document.activeElement)'), 'Tab focus remains in entity detail modal')
    runner.photo(page, 'dialog')
    page.keyboard.press('Escape')
    expect(dialog).to_have_count(0)
    expect(opener).to_be_focused()
    runner.record(fact_labels=labels[entity], columns=columns, entity=entity)


def channel_diagnostics(runner, page, data):
    dialog = page.get_by_role('dialog')
    expect(dialog).to_be_visible()
    expect(dialog.locator('.route-diagnostic-channel').first).to_be_visible()
    expect(dialog.get_by_role('textbox', name='渠道诊断请求模型', exact=True)).to_have_value('layout-model-001')
    expect(dialog.get_by_role('combobox', name='客户端协议', exact=True)).to_have_value('openai')
    expect(dialog.locator('.route-diagnostic-summary > div')).to_have_count(4)
    region = dialog.get_by_role('region', name='候选池 · 优先级层', exact=True)
    state = rect(region)
    runner.check(state['scrollHeight'] > state['clientHeight'], 'Large diagnostic candidate pools scroll locally')
    candidates = region.locator('.route-diagnostic-channel')
    expect(candidates).to_have_count(15)
    runner.check(candidates.locator('.channel-diagnostic-identity > small').all_text_contents()
                 == [f'#{number}' for number in range(201, 216)], 'Exact candidate identities remain present in order')
    metric_labels = ['优先级层', '可用 / 模型 Key', '轮询权重', '尝试状态', '健康观察', 'RPM', '并发']
    for candidate in dialog.locator('.route-diagnostic-channel').all():
        runner.check(candidate.locator('.route-diagnostic-metrics dt').all_text_contents() == metric_labels,
                     'Each diagnostic card retains all seven labeled metrics')
    blocked = candidates.nth(1).locator('.route-diagnostic-reasons')
    blocked.locator('summary').click()
    expect(blocked).to_contain_text('渠道日费用上限会阻断下一次上游尝试')
    runner.photo(page, 'blocking-reasons')
    region.focus()
    page.keyboard.press('End')
    page.wait_for_function('node => node.scrollTop > 0', arg=region.element_handle())
    runner.check(region.evaluate('node => node.scrollTop > 0'), 'Keyboard End scrolls the diagnostic candidate pool')
    candidates.last.locator('.channel-diagnostic-identity').scroll_into_view_if_needed()
    expect(candidates.last.locator('.channel-diagnostic-identity')).to_be_in_viewport()
    for selector in ('.channel-diagnostic-toolbar', '.route-diagnostic-summary', '.route-diagnostic-metrics'):
        overflow = dialog.locator(selector).evaluate_all('(nodes) => nodes.some(node => node.scrollWidth > node.clientWidth + 1)')
        runner.check(not overflow, f'Diagnostic {selector} does not overflow')
    link = dialog.get_by_role('link', name='模型路由', exact=True)
    query = parse_qs(urlsplit(link.get_attribute('href').split('#', 1)[-1]).query)
    runner.check(query.get('model') == ['layout-model-001'] and query.get('client_protocol') == ['openai'],
                 'Model route link preserves exact diagnostic scope')
    runner.photo(page, 'diagnostic')
    close = dialog.get_by_role('button', name='关闭弹窗', exact=True)
    reachable(runner, close, 'Diagnostic close action')
    close.focus()
    page.keyboard.press('Escape')
    expect(dialog).to_have_count(0)


def cases():
    specs = []
    for width in (320, 390):
        specs.append((f'model-badges-wide-font-{width}', narrow_model_badges, '/models', width))
    for width in (1440, 390):
        for state in ('baseline', 'zero', 'incomplete', 'empty', 'disabled'):
            specs.append((f'balance-state-{state}-{width}', balance_state, '/stats', width))
        for mode in ('cards', 'list', 'failure', 'refresh'):
            specs.append((f'model-pricing-delay-{mode}-{width}', price_geometry, '/models', width))
        specs.append((f'model-pricing-account-scope-{width}', account_pricing_scope, '/models', width))
        specs.append((f'model-pricing-invalid-call-{width}', invalid_per_call, '/models', width))
        for entity in ('sites', 'channels', 'tokens'):
            specs.append((f'details-{entity}-{width}', entity_details, f'/{entity}', width))
        specs.append((f'diagnostics-details-{width}', channel_diagnostics,
                      '/channels?diagnose_channel_id=201&model=layout-model-001&client_protocol=openai', width))
    return specs


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--case', action='append', default=[])
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(('127.0.0.1', 0), partial(StaticHandler, root=args.root.resolve()))
    while server.server_port == 10080:
        server.server_close()
        server = ThreadingHTTPServer(('127.0.0.1', 0), partial(StaticHandler, root=args.root.resolve()))
    server.daemon_threads = True
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    result, runner = {'pass': False, 'server_closed': False}, None
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True, args=['--no-proxy-server', '--disable-background-networking'])
            try:
                runner = FollowupRunner(browser, f'http://127.0.0.1:{server.server_port}', output, args.case, None, 300)
                for name, callback, path, width in cases():
                    def checked(page, data, callback=callback):
                        try:
                            callback(runner, page, data)
                        finally:
                            data.release_prices()
                            page.wait_for_load_state('networkidle', timeout=4000)
                    runner.case(name, checked,
                                path=path, width=width, height=900 if width > 700 else 844,
                                theme='light' if width > 700 else 'dark', touch=width <= 700,
                                wait_until='domcontentloaded' if name.startswith('model-pricing-delay-') else 'networkidle')
            finally:
                browser.close()
    except Exception as error:
        result['fatal_error'] = f'{type(error).__name__}: {error}'
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    if runner:
        result.update(checks=runner.results, metrics=runner.metrics, assertions=runner.assertions,
                      unexpected_events=runner.events)
    result['server_closed'] = not thread.is_alive()
    result['pass'] = (bool(result.get('checks')) and all(check['pass'] for check in result['checks'])
                      and not result.get('unexpected_events') and not result.get('fatal_error') and result['server_closed'])
    (output / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'pass': result['pass'], 'checks': len(result.get('checks', [])),
                      'unexpected_events': len(result.get('unexpected_events', [])),
                      'fatal_error': result.get('fatal_error', ''), 'server_closed': result['server_closed']}, ensure_ascii=False))
    raise SystemExit(0 if result['pass'] else 1)


if __name__ == '__main__':
    main()
