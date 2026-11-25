"""Verify management action columns, individual sync results and model visibility.

Uses the real built console and an owned static server. Management responses are
synthetic; only explicitly scoped synthetic task submissions may use POST. No
gateway, upstream, real credentials, database or paused demo server is used.
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
from ui_accessibility_smoke import StaticHandler
from ui_refinement_smoke import RefinementData, Runner


OUTCOMES = {
    1: ('success', ''), 2: ('failed', 'expired'),
    3: ('failed', 'browser_required'), 4: ('partial', 'request_failed'),
    5: ('failed', 'provider_timeout'), 6: ('success', ''),
}


class OperationsData(RefinementData):
    def __init__(self, case):
        super().__init__(case)
        self.sites, self.accounts, self.channels = [], [], []
        self.tasks = {}
        for number in range(1, 7):
            site = copy.deepcopy(fixtures.SITES[0])
            site.update(id=number, name=f'合成站点 {number}', enabled=True,
                        base_url=f'https://site-{number}.example.invalid',
                        platform='new-api-family', checkin_method='api',
                        external_checkin_url=f'https://site-{number}.example.invalid/checkin' if number % 2 else '')
            self.sites.append(site)
            account = copy.deepcopy(fixtures.ACCOUNTS[1][0])
            account.update(id=number, site_id=number, label=f'合成账号 {number}',
                           enabled=True, status='healthy', credential_type='access_token',
                           credential_configured=True, last_error='', auto_checkin=True,
                           last_checkin_status='success', last_checkin_at=fixtures.NOW,
                           last_refresh_at=fixtures.NOW)
            self.accounts.append(account)
            channel = copy.deepcopy(fixtures.CHANNELS[0])
            channel.update(id=200 + number, name=f'合成站点 {number} / 合成账号 {number}',
                           source='site_sync', site_account_id=number,
                           site_sync_ownership='manual' if number % 2 else 'projected',
                           enabled=number != 2, key_count=2, effective_key_count=2,
                           urls=[{'url': f'https://site-{number}.example.invalid', 'exact': False, 'protocols': ['openai']}],
                           models=[{'model': 'group-active', 'disabled': False, 'redirect': ''}])
            self.channels.append(channel)
        self.accounts[1].update(status='expired', last_error='expired')
        self.accounts[2].update(last_checkin_status='browser_required')
        self.accounts[3].update(credential_type='api_key', auto_checkin=False)
        self.sites[4].update(checkin_method='unavailable')
        self.accounts[5].update(enabled=False)
        if case == 'checkin-current-error':
            self.accounts[0].update(status='error', last_error='provider_timeout')
        if case.startswith('sites-'):
            self.accounts = self.accounts[:5]
            self.accounts[0]['label'] = '合成账号 · 需要省略的长名称甲'
            second = copy.deepcopy(self.accounts[0])
            second.update(id=7, label='合成账号 · 需要省略的长名称乙')
            self.accounts.append(second)
            for number in range(8, 11):
                extra = copy.deepcopy(second)
                extra.update(id=number, label=f'同站点的第 {number - 5} 个账号')
                self.accounts.append(extra)
            self.sites[1].update(last_probe_status='error', last_error='上游暂时无法访问，请稍后重新探测')
            self.sites[2].update(last_probe_status='unknown', checkin_method='turnstile')
            self.sites[3].update(last_probe_status='healthy', last_error='', checkin_method='api')
        self.inventory = [
            {'model': 'group-active', 'kind': 'group', 'aliases': ['vendor/active'],
             'alias_count': 1, 'configured_channels': 2, 'enabled_channels': 1},
            {'model': 'group-disabled', 'kind': 'group', 'aliases': ['vendor/disabled'],
             'alias_count': 1, 'configured_channels': 1, 'enabled_channels': 0},
            {'model': 'model-removed-upstream', 'kind': 'model', 'aliases': [],
             'alias_count': 0, 'configured_channels': 0, 'enabled_channels': 0},
            {'model': '*', 'kind': 'wildcard', 'aliases': [], 'alias_count': 0,
             'configured_channels': 1, 'enabled_channels': 1},
        ]
        self.tokens = self.tokens[:4]
        for index, token in enumerate(self.tokens):
            token.update(is_active=index != 1, allowed_models=[], token_recoverable=True)

    def permits_mutation(self, request):
        return (self.case in ('account-sync-results', 'account-single-result')
                and request.method == 'POST'
                and re.fullmatch(r'/admin/site-accounts/[1-6]/models/refresh', urlsplit(request.url).path))

    def route(self, route):
        path = urlsplit(route.request.url).path
        match = re.fullmatch(r'/admin/site-accounts/([1-6])/models/refresh', path)
        if match:
            number = int(match[1])
            task_id = f'st_synthetic_sync_{number}'
            status, error = OUTCOMES[number]
            routes = []
            if status in ('success', 'partial'):
                routes.append({'name': f'合成渠道已更新 {number}', 'status': 'updated'})
            if status == 'partial' or number == 5:
                routes.append({'name': f'合成渠道失败 {number}', 'status': 'failed', 'error': error})
            self.tasks[task_id] = {'id': task_id, 'kind': 'model_refresh',
                'site_id': number, 'site_account_id': number, 'status': status,
                'created_at': fixtures.NOW + number, 'started_at': fixtures.NOW + number,
                'finished_at': fixtures.NOW + number + 1, 'error': error,
                'result_ref': f'site_account:{number}' if status in ('success', 'partial') else '',
                'progress': {'completed': len(routes), 'total': len(routes), 'routes': routes}}
            self.calls.append({'path': path, 'method': route.request.method})
            self.fulfill(route, {'task_id': task_id})
            return
        if path.startswith('/admin/site-tasks/st_synthetic_sync_'):
            self.calls.append({'path': path, 'method': route.request.method})
            self.fulfill(route, self.tasks[path.rsplit('/', 1)[1]])
            return
        if path == '/admin/site-tasks':
            self.calls.append({'path': path, 'method': route.request.method})
            self.fulfill(route, [])
            return
        if path == '/admin/site-inventory':
            self.calls.append({'path': path, 'method': route.request.method})
            self.fulfill(route, {'sites': self.sites, 'accounts': self.accounts, 'latest_checkins': {}})
            return
        if path == '/admin/checkin-attempts' and self.case in ('checkin-current-error', 'checkin-repaired-history'):
            self.calls.append({'path': path, 'method': route.request.method})
            self.fulfill(route, [{'id': 701, 'site_account_id': 1, 'provider_id': 'newapi',
                'status': 'failed', 'error_code': 'expired', 'attempt_no': 1, 'local_day': '2026-11-24',
                'started_at': fixtures.NOW - 1000, 'finished_at': fixtures.NOW,
                'trigger_scope': 'manual'}])
            return
        super().route(route)


def horizontal_alignment(runner, elements, label):
    boxes = elements.evaluate_all('(items)=>items.map(e=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height}})')
    runner.check(len(boxes) > 1, f'{label}: fixture must include mixed rows')
    runner.check(max(box['x'] for box in boxes) - min(box['x'] for box in boxes) <= 1,
                 f'{label}: columns do not align: {boxes}')
    return boxes


def action_geometry(runner, page, data):
    name = data.case.split('-')[0]
    if name in ('sites', 'channels'):
        row = '.site-row' if name == 'sites' else '.channel-row'
        detail = '.site-details-action' if name == 'sites' else '.channel-details-action'
        expect(page.locator(row)).to_have_count(6)
        boxes = horizontal_alignment(runner, page.locator(detail), f'{name} details')
        if name == 'sites' and page.viewport_size['width'] > 900:
            columns = {}
            for selector in ('.site-account-summary', '.site-probe', '.row-actions'):
                columns[selector] = horizontal_alignment(runner, page.locator(f'{row} > {selector}'), f'Sites {selector}')
            geometry = page.locator(row).evaluate_all('''rows => rows.map(row => {
              const base = row.getBoundingClientRect();
              const box = e => {const r=e.getBoundingClientRect();return {x:r.x,y:r.y-base.y,w:r.width,h:r.height}};
              return {height:base.height, count:box(row.querySelector('.site-account-summary strong')),
                probe:box(row.querySelector('.site-probe .status-badge')),
                actions:[...row.querySelector('.row-actions').children].map(box)};
            })''')
            for key in ('count', 'probe'):
                offsets = [item[key]['y'] for item in geometry]
                runner.check(max(offsets) - min(offsets) <= 1, f'Sites {key} shifts with different account/probe content: {geometry}')
            runner.check(max(item['height'] for item in geometry) - min(item['height'] for item in geometry) <= 1,
                         f'Sites row height changes with different account counts: {geometry}')
            for index in range(8):
                positions = [item['actions'][index]['x'] for item in geometry]
                runner.check(max(positions) - min(positions) <= 1, f'Sites action slot {index} shifts across rows')
            runner.check(page.locator('.site-account-links').count() == 0, 'Site account summary must only show the count')
            runner.metrics[data.case] = {'columns': columns, 'geometry': geometry}
        if name == 'channels':
            manual = page.locator(row).filter(has=page.locator('button[title="恢复自动同步"]'))
            runner.check(manual.count() == 3, 'Mixed ownership fixture must show three restore controls')
            runner.check(manual.evaluate_all('(rows)=>rows.every(row=>row.querySelector(".row-actions").firstElementChild.title==="恢复自动同步")'),
                         'Restore automatic sync must be the first action')
        if page.viewport_size['width'] > 900:
            container = page.locator('.site-list' if name == 'sites' else '.channel-list')
            container.evaluate('(element)=>element.scrollLeft=0')
            last_action = page.locator(row).first.locator('.row-actions .danger-button')
            last_action.focus()
            box = last_action.bounding_box()
            runner.check(box and box['x'] >= 0 and box['x'] + box['width'] <= page.viewport_size['width'] + 1,
                         f'{name} offscreen action cannot be reached through keyboard focus')
            runner.metrics.setdefault(data.case, {}).update(details=boxes, horizontal_pan=container.evaluate('(element)=>element.scrollLeft'))
        if page.viewport_size['width'] <= 900:
            page.locator(detail).first.click()
            expect(page.get_by_role('dialog')).to_be_visible()
            page.get_by_role('button', name='关闭弹窗', exact=True).click()
    elif name == 'accounts':
        expect(page.locator('.record-row.account-grid')).to_have_count(6)
        if page.viewport_size['width'] > 880:
            geometries = page.locator('.account-actions').evaluate_all('(rows)=>rows.map(row=>[...row.children].map(e=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y,h:r.height}}))')
            runner.check(all(max(item['y'] + item['h']/2 for item in row) - min(item['y'] + item['h']/2 for item in row) <= 1 for row in geometries),
                         f'Account delete action wraps: {geometries}')
            runner.metrics[data.case] = {'actions': geometries}
    else:
        expect(page.locator('.record-row.checkin-account-grid')).to_have_count(6)
        horizontal_alignment(runner, page.locator('.checkin-action-primary'), 'Checkin primary/status slot')
        horizontal_alignment(runner, page.locator('a.checkin-action-open'), 'Open upstream checkin page')
        runner.check(page.locator('.checkin-actions a[href*="browser-import"]').count() == 0,
                     'Routine checkin recovery must use native account management')


def account_results(runner, page, data):
    page.get_by_role('checkbox', name='选择当前页账号', exact=True).check()
    page.locator('.batch-toolbar[aria-label="账号批量操作"]').get_by_role('button', name='同步路由', exact=True).click()
    page.get_by_text('部分成功 1', exact=False).wait_for()
    runner.metrics[data.case] = page.evaluate("""()=>({
      routeResults:[...document.querySelectorAll('.account-route-result')].map(e=>({status:e.dataset.status,text:e.innerText,visible:!!e.getClientRects().length})),
      rows:[...document.querySelectorAll('article[data-account-id]')].map(e=>({id:e.dataset.accountId,status:e.querySelector('.status-badge')?.innerText,result:e.querySelector('.account-route-result')?.dataset.status})),
      notice:document.querySelector('.maintenance-page-notice')?.innerText||''
    })""")
    for number, (status, _) in OUTCOMES.items():
        result = page.locator(f'article[data-account-id="{number}"] .account-route-result')
        expect(result).to_have_attribute('data-status', status)
        result.get_by_role('button').click()
        details = page.get_by_role('dialog', name=f'查看 合成账号 {number} 渠道结果', exact=True)
        expect(details).to_be_visible()
        if status == 'partial':
            expect(details).to_contain_text(f'合成渠道失败 {number}')
            expect(details).to_contain_text('上游请求失败')
        elif status == 'success':
            expect(details).to_contain_text(f'合成渠道已更新 {number}')
        elif number == 5:
            expect(details).to_contain_text(f'合成渠道失败 {number}')
            expect(details).not_to_contain_text('合成渠道已更新')
        else:
            expect(details.locator('.account-route-breakdown')).to_have_count(0)
            expect(details).not_to_contain_text('合成渠道已更新')
        page.keyboard.press('Escape')
        expect(details).to_have_count(0)
    notice = page.locator('.maintenance-page-notice')
    notice.get_by_role('button', name='查看逐项操作结果', exact=True).click()
    details = page.get_by_role('dialog', name='查看逐项操作结果', exact=True)
    for number in (2, 3, 4, 5):
        expect(details).to_contain_text(f'合成账号 {number}')
    recovery = details.locator('a[href*="focus_account_id=2"][href*="open_credential=1"]')
    expect(recovery).to_be_visible()
    recovery.click()
    expect(page.get_by_role('dialog', name='更新凭证 · 合成账号 2', exact=True)).to_be_visible()
    runner.check('/browser-import' not in page.url, 'Expired manual account recovery opened plugin import')
    page.get_by_role('button', name='关闭弹窗', exact=True).click()
    submitted = [call for call in data.calls if call['method'] == 'POST']
    runner.check(len(submitted) == 6, 'Batch sync must submit each selected account exactly once')
    runner.metrics[data.case].update(submitted=len(submitted), outcomes=OUTCOMES)


def single_result(runner, page, data):
    row = page.locator('article[data-account-id="1"]')
    row.get_by_role('button', name='同步路由', exact=True).click()
    expect(row.locator('.account-route-result')).to_have_attribute('data-status', 'success')
    expect(row.locator('.account-route-result')).to_contain_text('路由已同步')


def checkin_retry(runner, page, data):
    row = page.locator('.checkin-account-grid.record-row').filter(has=page.get_by_role('link', name='合成账号 1', exact=True))
    expect(row.get_by_role('button', name='立即签到', exact=True)).to_be_enabled()
    expect(row.get_by_role('link', name='更新凭证', exact=True)).to_have_count(0)
    page.get_by_role('button', name='记录', exact=True).click()
    history = page.locator('.checkin-grid.record-row')
    expect(history).to_have_count(1)
    expect(history.get_by_role('button', name='重试', exact=True)).to_be_enabled()
    expect(history.get_by_role('link', name='更新凭证', exact=True)).to_have_count(0)
    runner.metrics[data.case] = {'account_primary': '立即签到', 'history_primary': '重试',
                               'account_status': data.accounts[0]['status'], 'historical_error': 'expired'}


def current_inventory(runner, page, data):
    expect(page.locator('.model-route-model')).to_have_count(1)
    expect(page.locator('.model-route-model strong')).to_have_text('group-active')
    calls = [call for call in data.calls if call['path'] == '/admin/routing/models']
    runner.check(calls and calls[-1]['query'].get('enabled_only') == ['true'],
                 'Public model inventory must request current enabled supplies')
    data.inventory[0]['enabled_channels'] = 0
    page.get_by_role('button', name='刷新路由快照', exact=True).click()
    expect(page.locator('.model-route-model')).to_have_count(0)
    runner.check(page.locator('.model-route-inventory').get_by_text('group-disabled', exact=True).count() == 0,
                 'Disabled-only canonical group remained in public inventory')


def token_power(runner, page, data):
    buttons = page.locator('.token-row .resource-power-button')
    expect(buttons).to_have_count(4)
    expect(buttons.nth(0)).to_have_attribute('aria-pressed', 'true')
    expect(buttons.nth(1)).to_have_attribute('aria-pressed', 'false')
    colors = buttons.evaluate_all('(items)=>items.map(e=>({foreground:getComputedStyle(e).color,background:getComputedStyle(e).backgroundColor,border:getComputedStyle(e).borderColor}))')
    runner.check(colors[0]['background'] != colors[1]['background'], 'Enabled token power state has no distinct solid fill')
    def luminance(color):
        values = [float(value) for value in re.findall(r'[\d.]+', color)]
        runner.check(len(values) in (3, 4) and (len(values) == 3 or values[3] == 1), f'Power state must use opaque colors: {color}')
        linear = [value / 255 / 12.92 if value / 255 <= .04045 else ((value / 255 + .055) / 1.055) ** 2.4 for value in values[:3]]
        return sum(value * weight for value, weight in zip(linear, (.2126, .7152, .0722)))
    contrasts = []
    for color in colors:
        foreground, background = luminance(color['foreground']), luminance(color['background'])
        contrast = (max(foreground, background) + .05) / (min(foreground, background) + .05)
        runner.check(contrast >= 3, f'Power icon contrast is below 3:1: {color}, {contrast}')
        contrasts.append(round(contrast, 2))
    runner.metrics[data.case] = {'colors': colors, 'icon_contrast': contrasts}


def all_cases(runner):
    runner.case('model-current-inventory', lambda page, data: current_inventory(runner, page, data), path='/routing')
    runner.case('account-sync-results', lambda page, data: account_results(runner, page, data), path='/accounts')
    runner.case('account-single-result', lambda page, data: single_result(runner, page, data), path='/accounts')
    for case in ('checkin-current-error', 'checkin-repaired-history'):
        runner.case(case, lambda page, data: checkin_retry(runner, page, data), path='/checkins')
    for name, path in (('sites', '/sites'), ('channels', '/channels'), ('accounts', '/accounts'), ('checkins', '/checkins')):
        for width in (1440, 1280, 1024, 900, 768, 390):
            runner.case(f'{name}-actions-{width}', lambda page, data: action_geometry(runner, page, data),
                        path=path, width=width, height=900 if width > 900 else 844, touch=width == 390)
    for theme in ('light', 'dark'):
        runner.case(f'tokens-power-{theme}', lambda page, data: token_power(runner, page, data), path='/tokens', theme=theme)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument('--output', type=Path, required=True)
    parser.add_argument('--case', action='append', default=[])
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(('127.0.0.1', 0), partial(StaticHandler, root=args.root.resolve()))
    server.daemon_threads = True
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    result = {'pass': False}
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True, args=['--no-proxy-server', '--disable-background-networking'])
            try:
                runner = Runner(browser, f'http://127.0.0.1:{server.server_port}', output, args.case, data_factory=OperationsData)
                all_cases(runner)
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
    (output / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps({'pass': result['pass'], 'checks': len(result.get('checks', [])), 'server_closed': result['server_closed'], 'fatal_error': result.get('fatal_error', '')}, ensure_ascii=False))
    for check in result.get('checks', []):
        if not check['pass']:
            print(f'{check["case"]}: {check.get("error", "")[:650]}')
    return 0 if result['pass'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
