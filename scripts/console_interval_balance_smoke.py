"""Synthetic interval-balance UI checks; main owns browser execution/builds.

Uses the existing owned static server and guarded layout runner. Management
responses are intercepted synthetic GETs; mutations, external requests, model
calls and action endpoints are blocked. Evidence is visible-viewport JPEG only.
The fixtures describe the aggregated API contract, not a backend scheduler or
storage test. This script cannot establish retention, leasing, midnight cutoff,
raw-sample aggregation or legacy/interval precedence in the real backend.

After the main-owned build:
  python -B scripts/console_interval_balance_smoke.py --root . \
      --output SCRATCH/console-interval-balance
Repeat --case NAME for a subset. --help and --list-cases use only the stdlib
and do not start a server, import Playwright or execute a browser.
"""

import argparse
import copy
import json
import math
import re
import time
from datetime import date, datetime, timedelta, timezone
from decimal import Decimal
from functools import partial
from http.server import ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import parse_qs, urlsplit


AS_OF_DAY = '2026-11-28'
INTERVAL_KEY = 'site_balance_sample_interval_minutes'
ALLOWED_INTERVALS = ('15', '30', '60', '120', '360', '720', '1440')
SCHEDULE = {'enabled': True, 'time': '00:05', 'timezone': 'Asia/Shanghai',
            'interval_minutes': 60}
SCENARIOS = ('partial', 'zero', 'incomplete', 'baseline', 'coverage', 'refresh', 'settings')
READ_PATHS = frozenset((
    '/dashboard/session', '/dashboard/summary', '/admin/verify',
    '/admin/dashboard', '/admin/runtime-metrics', '/admin/settings',
    '/admin/stats', '/admin/stats/filter-options', '/admin/metrics',
    # Read-only shell preloads; these never perform a check-in or model read.
    '/admin/site-inventory', '/admin/checkin-attempts', '/admin/site-tasks',
))
MAX_CASE_READS = 60
BEIJING_TIME = timezone(timedelta(hours=8), name='Asia/Shanghai')


def case_specs():
    return [(f'interval-{scenario}-{width}', scenario, width,
             '/system?section=runtime&group=automation' if scenario == 'settings' else '/stats')
            for width in (1440, 390) for scenario in SCENARIOS]


def timestamp(day, clock):
    return int(datetime.fromisoformat(f'{day}T{clock}:00+08:00').timestamp() * 1000)


def observation_clock(fact):
    # Use the actual observation, including its 12-second fixture offset. Do
    # not share the product formatter or infer the time from scheduled_at.
    return datetime.fromtimestamp(fact['updated_at'] / 1000, BEIJING_TIME).strftime('%H:%M:%S')


def clock_pattern(clock):
    hour, minute, second = clock.split(':')
    display_hour = f'0?{int(hour)}' if int(hour) < 10 else hour
    return re.compile(r'(?<!\d)' + display_hour + ':' + minute + ':' + second + r'(?!\d)')


def point(day, balance, delta, status, *, clock='00:05', known=2, basis=None):
    scheduled = timestamp(day, clock)
    value = {'day': day, 'currency': 'USD', 'balance': balance, 'daily_delta': delta,
             'comparison_status': status, 'accounts': 2, 'known_accounts': known,
             'scheduled_at': scheduled, 'updated_at': scheduled + 12_000}
    if basis is not None:
        value['change_basis'] = basis
    return value


def balance_points(scenario, phase=0):
    # Earlier days are legacy opening samples. The first hourly day must use
    # its own opening observation (120), not the legacy previous-day 107.125.
    legacy = [point('2026-11-26', 100, None, 'baseline'),
              point('2026-11-27', 107.125, 7.125, 'complete')]
    latest = point(AS_OF_DAY, 121.75, 1.75, 'partial', clock='09:05', basis='first_sample')
    if scenario == 'zero':
        latest.update(balance=120, daily_delta=0)
    elif scenario == 'incomplete':
        latest = point(AS_OF_DAY, None, None, 'incomplete', clock='09:05', known=1)
    elif scenario == 'baseline':
        latest = point(AS_OF_DAY, 120, None, 'baseline', clock='08:05')
    elif scenario == 'coverage':
        # Equal counts do not establish identical account identities/currencies.
        latest = point(AS_OF_DAY, 130, None, 'coverage_changed', clock='09:05')
    elif scenario == 'refresh' and phase:
        latest = point(AS_OF_DAY, 116.875, -3.125, 'partial', clock='10:05', basis='first_sample')
    return legacy + [latest]


def read_allowed(path, method):
    # Exact paths avoid the historical false match on "checkin-attempts".
    return method == 'GET' and path in READ_PATHS


def runtime_types():
    global expect, rect, reachable
    from playwright.sync_api import expect, sync_playwright
    from console_layout_smoke import LayoutData, LayoutRunner, rect, reachable
    from console_visual_regression import setting
    from ui_refinement_smoke import StaticHandler, cost_summary

    class IntervalData(LayoutData):
        def __init__(self, name):
            super().__init__(name)
            self.scenario = next(spec[1] for spec in case_specs() if spec[0] == name)
            self.phase = 0
            self.stats_reads = []
            overrides = {
                INTERVAL_KEY: ('60', 'int'),
                'site_daily_balance_enabled': ('true', 'bool'),
                'site_daily_balance_time': ('00:05', 'string'),
            }
            self.settings = [entry for entry in self.settings if entry['key'] not in overrides]
            for key, (value, kind) in overrides.items():
                entry = setting(key, value, kind)
                entry['requires_restart'] = True
                self.settings.append(entry)

        def route(self, route):
            request = route.request
            parsed = urlsplit(request.url)
            if not read_allowed(parsed.path, request.method):
                self.unexpected.append({'case': self.case, 'unexpected_api': parsed.path,
                                        'method': request.method})
                route.abort('blockedbyclient')
                return
            if parsed.path == '/admin/stats':
                call = {'path': parsed.path, 'method': request.method,
                        'query': parse_qs(parsed.query)}
                self.calls.append(call)
                history = balance_points(self.scenario, self.phase)
                self.stats_reads.append({'phase': self.phase, 'history': copy.deepcopy(history),
                                         'query': call['query']})
                self.fulfill(route, {
                    'stats': [], 'duration_seconds': 86400, 'is_today': True, 'rpm_stats': {},
                    'balance_history': history, 'balance_as_of_day': AS_OF_DAY,
                    'balance_schedule': copy.deepcopy(SCHEDULE),
                    'cost_summary': cost_summary('empty'),
                })
                return
            super().route(route)

    class UTCContextBrowser:
        """Retain all existing guards while making local-time mistakes visible."""

        def __init__(self, guarded_browser):
            self.guarded_browser = guarded_browser

        def new_context(self, **kwargs):
            kwargs['timezone_id'] = 'UTC'
            return self.guarded_browser.new_context(**kwargs)

    class IntervalRunner(LayoutRunner):
        def __init__(self, *args):
            self.fixture_data = {}
            super().__init__(*args)
            self.browser = UTCContextBrowser(self.browser)

        def make_data(self, name):
            data = IntervalData(name)
            data.unexpected = self.events
            self.fixture_data[name] = data
            return data

    return sync_playwright, StaticHandler, IntervalRunner


def activate(runner, locator, label):
    reachable(runner, locator, label)
    if locator.page.viewport_size['width'] == 390:
        locator.tap()
    else:
        locator.click()


def visible_text(runner, locator, label):
    """Inspect rendered text lines, including clipping by ancestor elements."""
    expect(locator).to_be_visible()
    problems = locator.evaluate("""element => {
      const issues = [], walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
      let node;
      while ((node = walker.nextNode())) {
        if (!node.textContent.trim()) continue;
        const range = document.createRange(); range.selectNodeContents(node);
        for (const r of range.getClientRects()) {
          if (!r.width || !r.height) continue;
          if (r.left < -2 || r.top < -2 || r.right > innerWidth + 2 || r.bottom > innerHeight + 2)
            issues.push('viewport');
          for (let p = node.parentElement; p; p = p.parentElement) {
            const s = getComputedStyle(p), b = p.getBoundingClientRect();
            if (/^(hidden|clip|auto|scroll)$/.test(s.overflowX)
                && (r.left < b.left - 2 || r.right > b.right + 2)) issues.push('ancestor-x');
            if (/^(hidden|clip|auto|scroll)$/.test(s.overflowY)
                && (r.top < b.top - 2 || r.bottom > b.bottom + 2)) issues.push('ancestor-y');
          }
        }
      }
      return [...new Set(issues)];
    }""")
    runner.check(not problems, f'{label} text is visible without viewport/ancestor clipping: {problems}')


def explanation(runner, page, trigger, patterns, photo):
    activate(runner, trigger, 'Balance explanation trigger')
    expect(trigger).to_have_attribute('aria-expanded', 'true')
    tooltip = page.get_by_role('tooltip')
    expect(tooltip).to_have_count(1)
    for pattern in patterns:
        expect(tooltip).to_contain_text(re.compile(pattern))
    box = rect(tooltip)
    viewport = page.viewport_size
    runner.check(box['x'] >= -1 and box['y'] >= -1
                 and box['right'] <= viewport['width'] + 1
                 and box['bottom'] <= viewport['height'] + 1,
                 'Full balance explanation fits the visible viewport in both dimensions')
    visible_text(runner, tooltip, 'Balance explanation')
    described = trigger.get_attribute('aria-describedby') or ''
    runner.check(tooltip.get_attribute('id') in described.split(),
                 'Explanation is associated with its native trigger')
    runner.photo(page, photo)
    page.keyboard.press('Escape')
    expect(tooltip).to_have_count(0)
    expect(trigger).to_have_attribute('aria-expanded', 'false')


def calendar(runner, page, days):
    panel = page.get_by_role('region', name='余额变化', exact=True)
    control = panel.get_by_role('group', name='余额变化区间', exact=True)
    activate(runner, control.get_by_role('button', name=f'{days}日', exact=True), f'{days}-day range')
    expect(control.get_by_role('button', name=f'{days}日', exact=True)).to_have_attribute('aria-pressed', 'true')
    expect(control.get_by_role('button', name=f'{21 - days}日', exact=True)).to_have_attribute('aria-pressed', 'false')
    start = date.fromisoformat(AS_OF_DAY) - timedelta(days=days - 1)
    expect(panel).to_have_attribute('data-balance-start-day', start.isoformat())
    expect(panel).to_have_attribute('data-balance-end-day', AS_OF_DAY)
    expected = [(start + timedelta(days=index)).isoformat() for index in range(days)]
    observed = panel.locator('.balance-history-hit[data-day]').evaluate_all('(nodes) => nodes.map(n => n.dataset.day)')
    runner.check(observed == expected, f'{days}-day calendar includes missing days and ends on the server day')
    return panel


def select_day(runner, page, day, fact=None):
    panel = page.get_by_role('region', name='余额变化', exact=True)
    hit = panel.locator(f'.balance-history-hit[data-day="{day}"]')
    reachable(runner, hit, f'{day} chart action')
    if page.viewport_size['width'] == 390:
        hit.tap()
    else:
        hit.focus()
        expect(hit).to_be_focused()
        page.keyboard.press('Enter')
    expect(panel.locator('.balance-history-crosshair')).to_have_attribute('data-day', day)
    value = panel.locator('.balance-history-readout-value[data-currency="USD"]')
    expect(value).to_have_attribute('data-day', day)
    expect(value).to_have_attribute('data-status', fact['comparison_status'] if fact else 'missing')
    amount = fact['daily_delta'] if fact else None
    if amount is None:
        expect(value).to_have_attribute('data-value', '')
        expect(value.locator('b')).to_have_text('USD —')
        expect(value.locator('b')).not_to_contain_text(re.compile(r'0\.00'))
    else:
        runner.check(float(value.get_attribute('data-value')) == amount,
                     f'{day} retains the exact original signed amount {amount}')
        places = max(2, -Decimal(str(amount)).as_tuple().exponent)
        number = re.escape(f'{abs(amount):.{places}f}')
        prefix = r'\+[^\d\n]{0,8}' if amount > 0 else r'[-−][^\d\n]{0,8}' if amount < 0 else r'(?<![\d.])'
        runner.check(bool(re.search(prefix + number + r'(?!\d)', value.locator('b').inner_text().replace(',', ''))),
                     f'{day} visible text preserves precision and sign, including a genuine zero')
    if fact and fact.get('change_basis'):
        expect(value).to_have_attribute('data-change-basis', fact['change_basis'])
        expect(value).to_contain_text('当日首次采样' if fact['change_basis'] == 'first_sample' else '前日最后采样')
    elif fact and fact['comparison_status'] in ('incomplete', 'baseline', 'coverage_changed'):
        patterns = {'incomplete': '不完整|未知', 'baseline': '基线|首次|首日',
                    'coverage_changed': '账号范围|覆盖|币种'}
        expect(value).to_contain_text(re.compile(patterns[fact['comparison_status']]))
    if fact and day == AS_OF_DAY:
        clock = observation_clock(fact)
        observation = value.locator('.balance-history-sample-time')
        observation_day = datetime.fromtimestamp(fact['updated_at'] / 1000, BEIJING_TIME).strftime('%Y-%m-%d')
        expect(observation).to_have_text(f'实际采样：{observation_day} {clock}（北京时间）')
        runner.record(expected_observation_clock=clock, expected_observation_updated_at=fact['updated_at'],
                      observation_timezone='Asia/Shanghai', browser_timezone='UTC')
    visible_text(runner, value, f'{day} balance readout')
    if panel.locator('.balance-history-readout-value').count() == 1:
        readout = rect(panel.locator('.balance-history-readout'))
        content = rect(value)
        runner.check(content['width'] >= readout['width'] - 24,
                     'A single currency uses the readout width instead of leaving an empty second column')
    return value


def plotted_values(runner, panel, facts, days):
    """Verify actual SVG path endpoints/glyphs, not just readout attributes."""
    start = date.fromisoformat(AS_OF_DAY) - timedelta(days=days - 1)
    valid = [fact for fact in facts if fact['daily_delta'] is not None
             and fact['comparison_status'] in ('complete', 'partial')]
    scale = max([abs(fact['daily_delta']) for fact in valid] + [1])
    expected = [(80 + (date.fromisoformat(fact['day']) - start).days / (days - 1) * 840,
                 150 - fact['daily_delta'] / scale * 116) for fact in valid]
    svg = panel.locator('svg.balance-history-overlay')
    expect(svg).to_be_visible()
    observed = svg.evaluate("""svg => {
      const result = [];
      for (const p of svg.querySelectorAll('path.balance-history-line[data-currency="USD"]')) {
        const length = p.getTotalLength();
        if (!Number.isFinite(length) || length <= 0) throw Error('Invalid balance path');
        for (const distance of [0, length]) {
          const point = p.getPointAtLength(distance); result.push([point.x, point.y]);
        }
      }
      for (const p of svg.querySelectorAll('line.balance-history-isolated[data-currency="USD"]'))
        result.push([(Number(p.getAttribute('x1')) + Number(p.getAttribute('x2'))) / 2,
                     Number(p.getAttribute('y1'))]);
      return result;
    }""")
    runner.check(len(observed) == len(expected) and all(
        all(math.isfinite(value) for value in actual)
        and any(abs(actual[0] - wanted[0]) < .1 and abs(actual[1] - wanted[1]) < .1 for wanted in expected)
        for actual in observed) and all(
        any(abs(actual[0] - wanted[0]) < .1 and abs(actual[1] - wanted[1]) < .1 for actual in observed)
        for wanted in expected),
        'Rendered SVG uses the measured signed daily/partial values; unknown/baseline days create no glyph or zero')
    runner.record(plotted_points=observed)


def balance_case(runner, page, data):
    expect(page.get_by_role('heading', name='数据面板', exact=True)).to_be_visible()
    panel = calendar(runner, page, 14)
    explanation(runner, page, panel.get_by_role('button', name='余额变化 的说明', exact=True),
                (r'每小时|每\s*60\s*分钟', r'00:05', r'北京时间|Asia/Shanghai',
                 r'当日首次采样', r'前日最后采样', r'部分时段|日内'), 'hourly-explanation')
    facts = balance_points(data.scenario)
    for days in (7, 14):
        panel = calendar(runner, page, days)
        plotted_values(runner, panel, facts, days)
        select_day(runner, page, '2026-11-27', facts[1])
        select_day(runner, page, '2026-11-25')
        select_day(runner, page, AS_OF_DAY, facts[-1])
        runner.photo(page, f'today-{days}-days')
        page.keyboard.press('Escape')
        expect(panel.locator('.balance-history-crosshair')).to_have_count(0)
    if data.scenario == 'refresh':
        previous_reads = len(data.stats_reads)
        data.phase = 1
        refresh = page.get_by_role('button', name='刷新数据面板', exact=True)
        activate(runner, refresh, 'Same-day balance refresh')
        expect(refresh).to_be_enabled()
        panel = calendar(runner, page, 14)
        later = balance_points('refresh', 1)
        refreshed_value = select_day(runner, page, AS_OF_DAY, later[-1])
        expect(refreshed_value).not_to_contain_text(clock_pattern(observation_clock(facts[-1])))
        runner.record(before_refresh_observation_clock=observation_clock(facts[-1]),
                      after_refresh_observation_clock=observation_clock(later[-1]))
        runner.check(len(data.stats_reads) == previous_reads + 1
                     and data.stats_reads[-1]['phase'] == 1,
                     'One explicit refresh reads a new synthetic snapshot on the same calendar day')
        plotted_values(runner, panel, later, 14)
        runner.photo(page, 'same-day-refreshed-negative')
        select_day(runner, page, '2026-11-27', later[1])
        runner.check(data.stats_reads[-1]['history'][:-1] == facts[:-1],
                     'Earlier legacy-day payloads are unchanged by the same-day interval refresh')
        select_day(runner, page, AS_OF_DAY, later[-1])
    runner.check(bool(data.stats_reads), 'Balance case consumed the synthetic interval API fixture')
    runner.record(scenario=data.scenario, interval_minutes=60, as_of_day=AS_OF_DAY,
                  latest_point=balance_points(data.scenario, data.phase)[-1])


def settings_case(runner, page, data):
    expect(page.get_by_role('heading', name='系统设置', exact=True)).to_be_visible()
    groups = page.get_by_role('complementary', name='设置分类', exact=True)
    automation = groups.get_by_role('button', name=re.compile(r'^自动任务'))
    expect(automation).to_have_attribute('aria-pressed', 'true')
    row = page.locator(f'[data-setting-key="{INTERVAL_KEY}"]')
    interval = row.get_by_role('combobox')
    expect(interval).to_have_value('60')
    expect(row.locator('.setting-control > span')).to_have_count(0)
    runner.check(interval.get_attribute('aria-label') not in ('', None, INTERVAL_KEY),
                 'Sampling interval has a human-readable accessible label')
    options = interval.locator('option').evaluate_all('(nodes) => nodes.map(n => ({value:n.value, text:n.textContent, disabled:n.disabled}))')
    runner.check(tuple(option['value'] for option in options) == ALLOWED_INTERVALS
                 and all(not option['disabled'] and option['text'].strip() for option in options),
                 'Interval offers exactly 15/30/60/120/360/720/1440 minutes, with no disabled/zero default')
    anchor_row = page.locator('[data-setting-key="site_daily_balance_time"]')
    anchor = anchor_row.locator('input[type="time"]')
    expect(anchor).to_have_value('00:05')
    enabled = page.locator('[data-setting-key="site_daily_balance_enabled"]').get_by_role('switch')
    expect(enabled).to_have_attribute('aria-checked', 'true')
    explanation(runner, page, row.locator('.help-tip-trigger'),
                (r'分钟|小时',), 'interval-setting-explanation')
    explanation(runner, page, anchor_row.locator('.help-tip-trigger'),
                (r'基准|对齐|起点', r'北京|Asia/Shanghai'), 'phase-anchor-explanation')
    for value in ALLOWED_INTERVALS:
        reachable(runner, interval, f'{value}-minute draft option')
        interval.select_option(value)
        expect(interval).to_have_value(value)
        expect(anchor).to_have_value('00:05')
    interval.select_option('120')
    savebar = page.locator('footer.settings-savebar')
    expect(savebar.get_by_role('status')).to_have_text(re.compile(r'^1\s*项未保存$'))
    expect(page.get_by_role('button', name='保存更改', exact=True)).to_be_enabled()
    runner.photo(page, 'interval-draft')
    # Changing category must retain the draft. Saving is intentionally never invoked.
    activate(runner, groups.get_by_role('button', name=re.compile(r'^请求超时')), 'Other settings category')
    expect(row).to_have_count(0)
    activate(runner, automation, 'Return to automation settings')
    expect(interval).to_have_value('120')
    expect(anchor).to_have_value('00:05')
    interval.select_option('60')
    expect(savebar).to_have_count(0)
    reachable(runner, interval, 'Restored hourly sampling interval')
    runner.photo(page, 'hourly-default-restored')
    runner.check(not data.settings_writes, 'Only settings drafts were changed; no save/reset was issued')
    runner.record(interval_options=options, phase_anchor='00:05', saved_settings=0)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument('--output', type=Path)
    parser.add_argument('--case', action='append', default=[], help='Repeat for a strict subset of --list-cases')
    parser.add_argument('--list-cases', action='store_true')
    parser.add_argument('--max-seconds', type=int, default=420)
    args = parser.parse_args()
    specs = case_specs()
    names = [spec[0] for spec in specs]
    unknown = sorted(set(args.case) - set(names))
    if unknown or len(args.case) != len(set(args.case)):
        parser.error(f'Unknown or duplicate --case selection: {unknown or args.case}')
    if args.list_cases:
        print('\n'.join(name for name in names if not args.case or name in args.case))
        return
    if args.output is None:
        parser.error('--output is required unless --list-cases is used')
    if not 1 <= args.max_seconds <= 1800:
        parser.error('--max-seconds must be between 1 and 1800')
    root, output = args.root.resolve(), args.output.resolve()
    if not (root / 'web/console/index.html').is_file():
        parser.error('--root must contain the main-owned built web/console/index.html')
    output.mkdir(parents=True, exist_ok=True)
    selected = [spec for spec in specs if not args.case or spec[0] in args.case]
    result = {'pass': False, 'checks': [], 'metrics': {}, 'assertions': [],
              'unexpected_events': [], 'evidence': [], 'fatal_error': None,
              'server_closed': False, 'requested_cases': [spec[0] for spec in selected],
              'fixture_policy': 'synthetic aggregated API only; GET allowlist; no backend/production calls'}
    server = thread = runner = None
    started = time.monotonic()
    try:
        sync_playwright, static_handler, runner_type = runtime_types()
        while True:
            server = ThreadingHTTPServer(('127.0.0.1', 0), partial(static_handler, root=root))
            if server.server_port != 10080:
                break
            server.server_close()
        server.daemon_threads = True
        thread = Thread(target=server.serve_forever, daemon=True)
        thread.start()
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True,
                args=['--no-proxy-server', '--disable-background-networking'])
            try:
                runner = runner_type(browser, f'http://127.0.0.1:{server.server_port}', output,
                                     args.case, None, args.max_seconds)
                for name, scenario, width, path in selected:
                    callback = settings_case if scenario == 'settings' else balance_case
                    def checked(page, data, callback=callback):
                        callback(runner, page, data)
                        runner.check(len(data.calls) <= MAX_CASE_READS, 'Case stays within its synthetic API read budget')
                        runner.check(all(read_allowed(call['path'], call['method']) for call in data.calls),
                                     'Every recorded management request is an allowed synthetic GET')
                    runner.case(name, checked, path=path, width=width,
                                height=900 if width == 1440 else 844,
                                theme='light' if width == 1440 else 'dark', touch=width == 390)
            finally:
                browser.close()
    except Exception as error:
        result['fatal_error'] = f'{type(error).__name__}: {error}'[:2400]
    finally:
        if server is not None:
            if thread is not None and thread.is_alive():
                server.shutdown()
            server.server_close()
        if thread is not None:
            thread.join(timeout=5)
        result['server_closed'] = thread is None or not thread.is_alive()
    if runner is not None:
        result.update(checks=runner.results, metrics=runner.metrics, assertions=runner.assertions,
                      unexpected_events=runner.events)
        for name, data in runner.fixture_data.items():
            result['metrics'].setdefault(name, {}).update(api_reads=data.calls, stats_reads=data.stats_reads)
            if len(data.calls) > MAX_CASE_READS:
                result['unexpected_events'].append({'case': name, 'api_budget_exceeded': len(data.calls)})
        evidence = [check['evidence'] for check in runner.results if check.get('evidence')]
        evidence.extend(image for metric in runner.metrics.values() for image in metric.get('evidence', []))
        result['evidence'] = list(dict.fromkeys(evidence))
    ran_names = [check['case'] for check in result['checks']]
    result['pass'] = (ran_names == result['requested_cases'] and bool(ran_names)
                      and all(check['pass'] for check in result['checks'])
                      and not result['unexpected_events'] and not result['fatal_error']
                      and result['server_closed'])
    result['seconds'] = round(time.monotonic() - started, 3)
    (output / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2, allow_nan=False), encoding='utf-8')
    print(json.dumps({key: result[key] for key in ('pass', 'requested_cases', 'fatal_error', 'server_closed', 'seconds')},
                     ensure_ascii=False))
    raise SystemExit(0 if result['pass'] else 1)


if __name__ == '__main__':
    main()
