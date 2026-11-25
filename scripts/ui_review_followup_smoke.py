"""Check the screenshot-reported spacing, truncation and refresh interactions.

Serves the built console with synthetic management responses. No gateway,
upstream, demo database or real credentials are used.
"""
import argparse
import copy
import json
from functools import partial
from http.server import ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import urlsplit

from playwright.sync_api import expect, sync_playwright

from admin_operations_smoke import OperationsData
import docs_screenshots as fixtures
from ui_accessibility_smoke import StaticHandler
from ui_refinement_smoke import Runner


class ReviewData(OperationsData):
    def __init__(self, case):
        super().__init__('account-sync-results' if case.startswith('review-notices') else case)
        self.accounts[0].update(status='error', last_error='browser_required: 合成的较长验证原因，需要在上游完成验证后重试；保留完整原因以便检查，不应撑高列表。')
        for number in range(7, 11):
            extra = copy.deepcopy(self.accounts[0])
            extra.update(id=number, label=f'同站点的长名称账号 {number}', status='healthy', last_error='')
            self.accounts.append(extra)
        self.sites[0]['name'] = '五账号合成站点'

    def permits_mutation(self, request):
        return ((self.case in ('review-checkin-failure', 'review-checkin-unknown') and request.method == 'POST'
                 and urlsplit(request.url).path == '/admin/site-accounts/1/checkin') or super().permits_mutation(request))

    def route(self, route):
        path = urlsplit(route.request.url).path
        if self.case in ('review-checkin-failure', 'review-checkin-unknown') and path == '/admin/site-accounts/1/checkin':
            self.calls.append({'path': path, 'method': route.request.method})
            if self.case == 'review-checkin-unknown':
                route.abort('connectionreset')
            else:
                self.fulfill(route, {'task_id': 'st_synthetic_checkin_failure'})
            return
        if path == '/admin/site-tasks/st_synthetic_checkin_failure':
            self.fulfill(route, {'id': 'st_synthetic_checkin_failure', 'kind': 'checkin', 'site_id': 1,
                                 'site_account_id': 1, 'status': 'failed', 'created_at': fixtures.NOW,
                                 'finished_at': fixtures.NOW + 1, 'error': 'browser_required'})
            return
        if path.startswith('/admin/request-traces'):
            self.calls.append({'path': path, 'method': route.request.method})
        super().route(route)


def rect(locator):
    return locator.evaluate('e=>{const r=e.getBoundingClientRect();return {x:r.x,y:r.y,w:r.width,h:r.height}}')


def checkins(runner, page, data):
    status = page.locator('.account-error-message').first
    expect(status).to_contain_text('合成的较长验证原因')
    measured = status.evaluate('e=>({height:e.getBoundingClientRect().height,line:parseFloat(getComputedStyle(e).lineHeight),title:e.title,overflow:getComputedStyle(e).overflow})')
    runner.check(measured['height'] <= measured['line'] + 1, f'Account reason expands beyond its single summary line: {measured}')
    runner.check('合成的较长验证原因' in measured['title'], 'Hover must preserve the full human-readable reason')
    status.click()
    popover = page.get_by_role('dialog', name='查看 合成账号 1 状态说明', exact=True)
    expect(popover).to_contain_text('保留完整原因以便检查')
    page.keyboard.press('Escape')
    expect(popover).to_have_count(0)
    expect(status).to_be_focused()
    api = page.locator('.checkin-account-grid.record-row').filter(has=page.get_by_role('link', name='合成账号 4', exact=True))
    label = api.locator('.checkin-unsupported > span')
    runner.check(label.evaluate('e=>e.getBoundingClientRect().height <= parseFloat(getComputedStyle(e).lineHeight)+1'), 'Unsupported API Key label wraps')
    expect(api.locator('.checkin-unsupported')).to_have_attribute('title', '模型 API Key 不支持服务端签到')
    if page.viewport_size['width'] > 880:
        heading = rect(page.locator('.checkin-account-records .record-head > span').last)
        slots = page.locator('.checkin-actions').first.locator(':scope > .checkin-action-open, :scope > .checkin-action-primary')
        first, last = rect(slots.first), rect(slots.last)
        runner.check(abs(heading['x'] + heading['w']/2 - (first['x'] + last['x'] + last['w'])/2) <= 2,
                     f'Operation heading is not centered over its action slots: {heading}, {first}, {last}')
    runner.metrics[data.case] = measured


def log_toolbar(runner, page, data):
    refresh = page.get_by_role('button', name='刷新完整请求', exact=True)
    expect(refresh).to_be_enabled()
    if page.viewport_size['width'] >= 1440:
        centers = page.locator('.request-filter-bar > *').evaluate_all('(items)=>items.map(e=>{const r=e.getBoundingClientRect();return r.y+r.height/2})')
        runner.check(max(centers)-min(centers) <= 2, f'Wide desktop filter/refresh controls wrap: {centers}')
        runner.metrics[data.case] = {'control_centers': centers}
    before = len([call for call in data.calls if call['path'] == '/admin/request-traces'])
    stats_before = len([call for call in data.calls if call['path'] == '/admin/request-traces/stats'])
    refresh.click()
    expect(refresh).to_be_enabled()
    page.wait_for_function('!document.querySelector(".request-stats-strip")?.matches("[aria-busy=true]")')
    runner.check(len([call for call in data.calls if call['path'] == '/admin/request-traces']) > before, 'Refresh did not reload the request list')
    runner.check(len([call for call in data.calls if call['path'] == '/admin/request-traces/stats']) > stats_before, 'Refresh did not reload statistics')
    page.get_by_role('textbox', name='精确请求模型', exact=True).fill('a-long-exact-model-filter')
    page.get_by_role('button', name='筛选模型', exact=True).click()
    expect(page.get_by_role('button', name='清除完整请求筛选', exact=True)).to_be_visible()
    if page.viewport_size['width'] >= 1440:
        centers = page.locator('.request-filter-bar > *').evaluate_all('(items)=>items.map(e=>{const r=e.getBoundingClientRect();return r.y+r.height/2})')
        runner.check(max(centers)-min(centers) <= 2, 'Active filters push refresh onto a second line')


def settings_spacing(runner, page, data):
    nav = page.locator('.settings-groups')
    expect(nav).to_be_visible()
    buttons = nav.locator('button')
    boxes = buttons.evaluate_all('(items)=>items.map(e=>{const r=e.getBoundingClientRect();return {y:r.y,h:r.height}})')
    if page.viewport_size['width'] > 900:
        runner.check(min(item['h'] for item in boxes) >= 40, f'Settings categories remain cramped: {boxes}')
        runner.check(nav.evaluate('e=>e.scrollHeight<=e.clientHeight+1'), 'Settings categories require avoidable scrolling')
        editor = rect(page.locator('.settings-editor'))
        sidebar = rect(nav)
        runner.check(abs(editor['y']+editor['h']-sidebar['y']-sidebar['h']) <= 2, 'Settings columns lost their aligned bottoms')
    runner.metrics[data.case] = {'buttons': boxes, 'nav': rect(nav)}


def sites_and_selection(runner, page, data):
    row = page.locator('.site-row[data-site-id="1"]')
    expect(row).to_be_visible()
    expect(row.locator('.site-account-summary')).to_have_text('4/5')
    expect(row.locator('.site-account-summary a, .site-account-links')).to_have_count(0)
    checkbox = row.get_by_role('checkbox')
    initial = checkbox.evaluate('e=>({width:e.getBoundingClientRect().width,border:parseFloat(getComputedStyle(e).borderWidth),color:getComputedStyle(e).borderColor,background:getComputedStyle(e).backgroundColor})')
    runner.check(initial['width'] >= 18 and initial['border'] >= 2, f'Unchecked selector is still faint/small: {initial}')
    checkbox.focus()
    page.keyboard.press('Space')
    expect(checkbox).to_be_checked()
    expect(row).to_have_class('site-row row-selected')
    checked = checkbox.evaluate('e=>({background:getComputedStyle(e).backgroundColor,outline:getComputedStyle(e).outlineStyle})')
    runner.check(checked['background'] != initial['background'], 'Checked selector has no distinct fill')
    runner.check(checked['outline'] != 'none', 'Keyboard focus is not visible')
    page.keyboard.press('Space')
    expect(checkbox).not_to_be_checked()
    row.get_by_role('button', name='查看 五账号合成站点 详情', exact=True).click()
    expect(page.get_by_role('dialog')).to_be_visible()
    for number in range(7, 11):
        expect(page.get_by_role('dialog').get_by_role('link', name=f'同站点的长名称账号 {number}', exact=True)).to_be_visible()
    runner.metrics[data.case] = {'unchecked': initial, 'checked': checked}


def token_spacing(runner, page, data):
    head = page.locator('.token-table-head')
    expect(head).to_contain_text('今日费用')
    expect(head).not_to_contain_text('日志费用')
    cost, limits = head.locator(':scope > span').nth(4), head.locator(':scope > span').nth(5)
    a, b = rect(cost), rect(limits)
    inset = limits.evaluate('e=>parseFloat(getComputedStyle(e).paddingInlineStart)')
    runner.check(b['x'] + inset - a['x'] - a['w'] >= 28, 'Token costs remain too close to limits')
    runner.metrics[data.case] = {'cost': a, 'limits': b, 'inset': inset}


def channel_spacing(runner, page, data):
    limits = page.locator('.channel-limits').first
    expect(limits).to_be_visible()
    inset = limits.evaluate('e=>parseFloat(getComputedStyle(e).paddingInlineStart)')
    runner.check(inset >= 14, 'Channel RPM/key column has no additional inset')
    runner.metrics[data.case] = {'limits_inset': inset}


def compact_notices(runner, page, data):
    for number in range(1, 7):
        page.get_by_role('checkbox', name=f'选择 合成账号 {number}', exact=True).check()
    page.locator('.batch-toolbar').get_by_role('button', name='同步路由', exact=True).click()
    toast = page.locator('body > .operation-notice:not(.maintenance-page-notice)')
    expect(toast).to_contain_text('批量同步路由结果')
    notice = page.locator('.maintenance-page-notice')
    expect(notice).to_contain_text('操作失败或部分完成')
    summary = notice.locator('.maintenance-page-notice__text')
    runner.check(summary.evaluate('e=>e.getBoundingClientRect().height <= parseFloat(getComputedStyle(e).lineHeight)*2+1'), 'Inline operation summary exceeds two lines')
    expect(notice.locator('.maintenance-page-notice__results')).to_have_count(0)
    details = notice.get_by_role('button', name='查看逐项操作结果', exact=True)
    details.hover()
    popover = page.get_by_role('dialog', name='查看逐项操作结果', exact=True)
    expect(popover).to_be_visible()
    expect(popover).to_contain_text('合成渠道失败 4')
    expect(popover).to_contain_text('上游请求失败')
    expect(popover.get_by_role('link', name='更新凭证', exact=True)).to_be_visible()
    page.keyboard.press('Escape')
    expect(popover).to_have_count(0)
    details.click()
    expect(popover).to_be_visible()
    links = popover.get_by_role('link')
    expect(links.first).to_be_focused()
    page.keyboard.press('Tab')
    expect(links.nth(1)).to_be_focused()
    page.keyboard.press('Shift+Tab')
    expect(links.first).to_be_focused()
    page.keyboard.press('Tab')
    page.keyboard.press('Tab')
    expect(popover).to_have_count(0)
    details.focus()
    page.keyboard.press('Enter')
    expect(popover).to_be_visible()
    runner.metrics[data.case] = {'summary': summary.inner_text(), 'toast': toast.inner_text()}


def checkin_feedback(runner, page, data):
    row = page.locator('.record-row.checkin-account-grid').filter(has=page.get_by_role('link', name='合成账号 1', exact=True))
    button = row.get_by_role('button', name='立即签到', exact=True)
    button.click()
    toast = page.locator('body > .operation-notice:not(.maintenance-page-notice)')
    expect(toast).to_contain_text('结果待确认' if data.case.endswith('unknown') else '浏览器验证')
    notice = page.locator('.maintenance-page-notice')
    expect(notice).to_be_visible()
    if data.case.endswith('unknown'):
        expect(button).to_be_disabled()
        expect(notice).to_contain_text('已阻止重复操作')
        notice.get_by_role('button', name='查看逐项操作结果', exact=True).click()
        popover = page.get_by_role('dialog')
        expect(popover.get_by_role('button', name='重新读取活动任务', exact=True)).to_be_focused()
        page.keyboard.press('Tab')
        expect(popover.get_by_role('button', name='核实后解除锁定', exact=True)).to_be_focused()
        page.keyboard.press('Escape')
        expect(button).to_be_disabled()
    else:
        expect(button).to_be_enabled()
    runner.check(len([call for call in data.calls if call['method'] == 'POST']) == 1, 'Checkin was retried automatically')


def cases(runner):
    for width in (1920, 1440, 390):
        runner.case(f'review-checkins-{width}', lambda page, data: checkins(runner, page, data), '/checkins', width=width)
    for width in (1920, 1440, 1280, 390):
        runner.case(f'review-log-toolbar-{width}', lambda page, data: log_toolbar(runner, page, data), '/logs?view=requests', width=width)
    for height in (1080, 900, 768):
        runner.case(f'review-settings-{height}', lambda page, data: settings_spacing(runner, page, data), '/system', width=1440, height=height)
    for theme in ('light', 'dark'):
        runner.case(f'review-sites-{theme}', lambda page, data: sites_and_selection(runner, page, data), '/sites', theme=theme)
    runner.case('review-tokens', lambda page, data: token_spacing(runner, page, data), '/tokens', width=1920)
    runner.case('review-channels', lambda page, data: channel_spacing(runner, page, data), '/channels', width=1920)
    for width in (1920, 390):
        runner.case(f'review-notices-{width}', lambda page, data: compact_notices(runner, page, data), '/accounts', width=width)
    for name in ('review-checkin-failure', 'review-checkin-unknown'):
        runner.case(name, lambda page, data: checkin_feedback(runner, page, data), '/checkins', width=1440)


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
                runner = Runner(browser, f'http://127.0.0.1:{server.server_port}', output, args.case, data_factory=ReviewData)
                cases(runner)
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
