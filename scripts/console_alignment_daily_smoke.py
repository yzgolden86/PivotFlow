"""Bounded alignment, shared pagination and signed daily-balance browser checks.

Main runs this after building the console. The only server is an owned static
asset server; every API response is synthetic. No configuration writes, gateway
probe, upstream call, database or Vite is used. Evidence is viewport JPEG only.
--help and --list-cases do not import Playwright or start a server/browser.
"""

import argparse
import asyncio
import base64
import copy
import json
import math
import re
import time
import traceback
from datetime import datetime
from decimal import Decimal
from functools import partial
from http.server import ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import parse_qs, urlsplit


SCHEDULE = {'enabled': True, 'time': '00:05', 'timezone': 'Asia/Shanghai'}
SYNTHETIC_TOKEN = 'alignment-synthetic-downstream-token-only'
MODEL = 'group-core'
MODEL_RACE_CASE = 'audit-models-race-1440'
RACE_FRESH_MODEL = 'alignment-race-fresh-catalogue'
RACE_OLD_MODEL = 'alignment-race-initial-old'
RACE_CHOSEN_MODEL = 'alignment-race-user-model'
FIXTURE_OWNER_COUNT = 121
MAX_CASE_REQUESTS = 120
MAX_SUITE_REQUESTS = 4000
# The model catalog reads one price snapshot for every fixture owner. The prior
# evidence used about 52 asset/bootstrap reads; 64 covers those and 15 covers
# the bounded navigate-away/return exercise. 121 + 64 + 15 = 200, never > 250.
MODEL_AUDIT_ASSET_API_ALLOWANCE = 64
MODEL_AUDIT_NAVIGATION_ALLOWANCE = 15
MAX_MODEL_AUDIT_REQUESTS = 250
PRICING_RESPONSE_DELAY = .04
PRICING_FETCH_OBSERVER = """(() => {
  const nativeFetch=window.fetch;
  window.__alignmentPricingPhase='cancel';
  window.__alignmentPricingFetches=[];
  window.fetch=function(input,init) {
    const url=new URL(input instanceof Request ? input.url : String(input),location.href);
    if(url.origin!==location.origin || url.pathname!=='/admin/site-pricing')
      return Reflect.apply(nativeFetch,this,[input,init]);
    const rows=window.__alignmentPricingFetches, phase=window.__alignmentPricingPhase;
    const row={phase,site_id:Number(url.searchParams.get('site_id')),
      method:String(init?.method || (input instanceof Request ? input.method : 'GET')).toUpperCase(),
      status:'pending',ms:performance.now()};
    rows.push(row);
    row.inflight_at_start=rows.filter(item=>item.phase===phase && item.status==='pending').length;
    const signal=init?.signal || (input instanceof Request ? input.signal : null);
    const aborted=()=>{row.status='aborted';};
    signal?.addEventListener('abort',aborted,{once:true});
    if(signal?.aborted) aborted();
    return Reflect.apply(nativeFetch,this,[input,init]).then(response=>{
      if(row.status==='pending') row.status='completed';
      return response;
    },error=>{
      if(row.status==='pending') row.status='failed';
      row.error_name=error.name;
      throw error;
    }).finally(()=>signal?.removeEventListener('abort',aborted));
  };
})();"""
READ_PATHS = frozenset((
    '/dashboard/session', '/dashboard/summary', '/admin/verify',
    '/admin/dashboard', '/admin/runtime-metrics', '/admin/settings',
    '/admin/routing/models', '/admin/routing/diagnostics', '/admin/auth-tokens',
    '/admin/sites', '/admin/site-inventory', '/admin/site-models',
    '/admin/site-channel-bindings', '/admin/site-pricing', '/admin/channels',
    '/admin/checkin-attempts', '/admin/site-tasks', '/admin/announcements',
    '/admin/model-alias-inventory', '/admin/stats', '/admin/stats/filter-options', '/admin/metrics',
    '/admin/request-cache', '/admin/active-requests', '/admin/logs/bootstrap',
    '/admin/logs', '/admin/logs/stats', '/admin/request-traces',
    '/admin/request-traces/stats', '/admin/request-traces/options',
    '/admin/request-traces/filter-options', '/admin/system-access-tokens',
))
VISUAL_PAGES = (
    ('dashboard', '/', '系统概览'), ('channels', '/channels', '渠道分发'),
    ('routing', '/routing?model=group-core', '模型路由'),
    ('logs', '/logs?view=requests', '请求日志'), ('stats', '/stats', '数据面板'),
    ('health', '/health', '运行健康'), ('models', '/models', '模型测试'),
    ('sites', '/sites', '站点管理'), ('accounts', '/accounts', '账号管理'),
    ('checkins', '/checkins', '签到中心'), ('announcements', '/announcements', '公告中心'),
    ('tokens', '/tokens', '令牌管理'), ('connect', '/connect', '接入配置'),
    ('settings', '/system?section=runtime&group=timeouts', '系统设置'),
)


def daily_point(day, currency, balance, delta, status='complete', known=2):
    scheduled = int(datetime.fromisoformat(f'2026-11-{day:02d}T00:05:00+08:00').timestamp() * 1000)
    return {'day': f'2026-11-{day:02d}', 'currency': currency,
            'balance': balance, 'daily_delta': delta, 'accounts': 2,
            'known_accounts': known, 'comparison_status': status,
            'scheduled_at': scheduled, 'updated_at': scheduled + 60_000}


def daily_points(case):
    if 'baseline' in case:
        return [daily_point(24, 'USD', 123, None, 'baseline')]
    if 'empty' in case:
        return []
    # USD totals stay positive even when daily deltas are negative. Drawing the
    # old totals, deriving our null deltas, or joining gaps must fail this case.
    points = [daily_point(*entry) for entry in (
        (10, 'USD', 100, None, 'baseline'),
        (11, 'USD', 111, 11), (12, 'USD', 104, -7), (13, 'USD', 104, 0),
        (14, 'USD', 109, 5), (15, 'USD', 99, -10),
        (16, 'USD', None, None, 'incomplete', 1),
        (17, 'USD', 108, None, 'missing_previous'),
        (18, 'USD', 113, 5), (19, 'USD', 112, -1),
        # Equal counts with a changed account cohort still cannot be compared.
        (20, 'USD', 115, None, 'coverage_changed'),
        (21, 'USD', 119, 4), (22, 'USD', 117, -2),
        (23, 'USD', None, None, 'overflow'),
        (24, 'USD', 118, None, 'missing_previous'),
    )]
    balance = 500
    points.append(daily_point(10, 'CNY', balance, None, 'baseline'))
    for day, delta in zip(range(11, 25), (5, -2, 0, 4, -3, 1, -1, 6, -4, 2, 0, -2, 3, 1)):
        balance += delta
        points.append(daily_point(day, 'CNY', balance, delta))
    return points


def read_allowed(path, method, case, token_ids=()):
    """Exact synthetic reads; segment matching never mistakes checkin-attempts."""
    if method != 'GET':
        return False
    if path in READ_PATHS:
        return True
    if re.fullmatch(r'/admin/sites/\d+/accounts', path):
        return True
    if re.fullmatch(r'/admin/request-traces/[A-Za-z0-9_-]+', path):
        return not set(path.split('/')) & {'check', 'probe', 'reveal', 'test'}
    if case.startswith('connect-'):
        if path in ('/v1/models', '/v1beta/models'):
            return True
        return path in {f'/admin/auth-tokens/{token_id}/reveal' for token_id in token_ids}
    return False


def case_request_budget(case, known_site_count):
    if not case.startswith('audit-models-'):
        return MAX_CASE_REQUESTS
    budget = known_site_count + MODEL_AUDIT_ASSET_API_ALLOWANCE + MODEL_AUDIT_NAVIGATION_ALLOWANCE
    if not 0 < known_site_count or budget > MAX_MODEL_AUDIT_REQUESTS:
        raise AssertionError('Model audit fixture exceeds the bounded request allowance')
    return budget


class SyntheticPricingReads:
    """Defer synthetic route fulfillment while the sync transport keeps pumping.

    Route callbacks only enqueue. No callback sleeps, background Playwright
    thread, actual price server, or upstream request is used.
    """

    def __init__(self, known_ids, fulfill, events, case, clock=time.monotonic, allow_refresh=False):
        self.known_ids = set(known_ids)
        self.fulfill, self.events, self.case, self.clock = fulfill, events, case, clock
        self.pending, self.phases, self.history = {}, {}, []
        self.cancelled_routes = []; self.settled_cancellations = 0; self.closing = False
        self.allow_refresh = allow_refresh
        self.serial = 0; self.epoch = clock(); self.expected_cancellation = False
        self.begin_phase('cancel')

    def begin_phase(self, phase):
        if self.pending or self.cancelled_routes:
            raise AssertionError('Pricing phase changed with unfinished synthetic reads')
        self.phase = phase
        self.phases[phase] = {'started': [], 'completed': [], 'cancelled': [], 'max_inflight': 0}
        self.expected_cancellation = False

    def note(self, entry, event):
        self.history.append({'phase': entry['phase'], 'site_id': entry['site_id'],
                             'event': event, 'ms': round((self.clock() - self.epoch) * 1000, 3)})

    def enqueue(self, route, query):
        if self.closing:
            self.events.append({'case': self.case, 'pricing_read_after_cleanup': query})
            route.abort('blockedbyclient'); return
        values = query.get('site_id', [])
        site_id = int(values[0]) if len(values) == 1 and values[0].isdigit() else None
        allowed_query = set(query) == {'site_id'} or (self.allow_refresh
            and set(query) == {'site_id', 'refresh'} and query['refresh'] == ['true'])
        if not allowed_query or site_id not in self.known_ids or values != [str(site_id)]:
            self.events.append({'case': self.case, 'unexpected_pricing_query': query})
            route.abort('blockedbyclient'); return
        state = self.phases[self.phase]
        if site_id in state['started']:
            self.events.append({'case': self.case, 'duplicate_price_read': site_id, 'phase': self.phase})
        self.serial += 1
        entry = {'route': route, 'request': route.request, 'site_id': site_id,
                 'phase': self.phase, 'due': self.clock() + PRICING_RESPONSE_DELAY}
        self.pending[self.serial] = entry
        state['started'].append(site_id)
        state['max_inflight'] = max(state['max_inflight'], len(self.pending))
        self.note(entry, 'start')

    def failed(self, entry, failure):
        expected = (entry['phase'] == 'cancel' and self.expected_cancellation
                    and 'ERR_ABORTED' in str(failure))
        self.phases[entry['phase']]['cancelled'].append(entry['site_id'])
        self.note(entry, 'cancel' if expected else 'unexpected-failure')
        if not expected:
            self.events.append({'case': self.case, 'unexpected_pricing_failure': str(failure)[:400],
                                'site_id': entry['site_id'], 'phase': entry['phase']})

    def request_failed(self, request):
        for serial, entry in list(self.pending.items()):
            if entry['request'] == request:
                del self.pending[serial]
                self.failed(entry, request.failure)
                # Network failure does not resolve Playwright's held route
                # handler. Settle it outside this event callback while live.
                self.cancelled_routes.append(entry)

    def cancel_observed(self, site_ids):
        # A held route can delay Chromium's requestfailed event. The read-only
        # fetch observer proves the real AbortSignal fired before we settle it.
        for serial, entry in list(self.pending.items()):
            if entry['phase'] != 'cancel' or entry['site_id'] not in site_ids:
                continue
            del self.pending[serial]
            self.failed(entry, 'net::ERR_ABORTED (observed originating AbortSignal)')
            self.cancelled_routes.append(entry)
        self.settle_cancelled()

    def settle_cancelled(self):
        while self.cancelled_routes:
            entry = self.cancelled_routes.pop(0)
            try:
                entry['route'].abort('aborted')
            except Exception as error:
                self.events.append({'case': self.case, 'pricing_abort_error': str(error)[:400],
                                    'site_id': entry['site_id']})
                # A throwing route API can leave its handled future pending.
                # This only settles an already failed/aborted synthetic route;
                # fallback never releases an otherwise live price request.
                if 'ERR_ABORTED' not in str(entry['request'].failure):
                    raise
                entry['route'].fallback()
            self.settled_cancellations += 1
            self.note(entry, 'route-settled')

    def release_due(self, force=False):
        self.settle_cancelled()
        for serial, entry in list(self.pending.items()):
            if serial not in self.pending or (not force and entry['due'] > self.clock()):
                continue
            # Stop counting before fulfillment can dispatch the next queued GET.
            del self.pending[serial]
            try:
                self.fulfill(entry['route'], entry['site_id'])
            except Exception as error:
                self.failed(entry, entry['request'].failure or f'{type(error).__name__}: {error}')
                self.cancelled_routes.append(entry)
            else:
                self.phases[entry['phase']]['completed'].append(entry['site_id'])
                self.note(entry, 'complete')
        self.settle_cancelled()

    def finish_for_close(self):
        self.closing = True
        for serial, entry in list(self.pending.items()):
            del self.pending[serial]
            self.failed(entry, 'Unfinished synthetic price read at context cleanup')
            self.cancelled_routes.append(entry)
        self.settle_cancelled()

    def snapshot(self):
        return {'known_site_ids': sorted(self.known_ids), 'response_delay_ms': PRICING_RESPONSE_DELAY * 1000,
                'active_reads': len(self.pending), 'phases': copy.deepcopy(self.phases),
                'unsettled_cancelled_routes': len(self.cancelled_routes),
                'settled_cancellations': self.settled_cancellations,
                'events': list(self.history)}


NUMBER = r'[-+]?(?:\d*\.\d+|\d+\.?\d*)(?:[eE][-+]?\d+)?'


def cubic_segments(path):
    """Parse actual rendered absolute M/C geometry; reject legacy polylines."""
    tokens = re.findall(r'[A-Za-z]|' + NUMBER, path)
    remainder = re.sub(r'[A-Za-z]|' + NUMBER, '', path)
    if remainder.strip(' ,\t\r\n'):
        raise AssertionError('Invalid SVG path text')
    current, segments, index, command = None, [], 0, None
    while index < len(tokens):
        if tokens[index].isalpha():
            command = tokens[index]
            index += 1
        elif command != 'C':
            raise AssertionError('Implicit line commands are not a cubic daily curve')
        if command not in ('M', 'C'):
            raise AssertionError(f'Expected shape-preserving cubic commands, got {command}')
        count = 2 if command == 'M' else 6
        if index + count > len(tokens):
            raise AssertionError('Truncated SVG cubic path')
        values = [float(token) for token in tokens[index:index + count]]
        if not all(math.isfinite(value) for value in values):
            raise AssertionError('Non-finite SVG geometry')
        index += count
        if command == 'M':
            if current is not None:
                raise AssertionError('Separate gaps must have separate path elements')
            current = tuple(values)
        else:
            if current is None:
                raise AssertionError('Cubic segment has no start point')
            segment = (current, tuple(values[:2]), tuple(values[2:4]), tuple(values[4:]))
            segments.append(segment)
            current = segment[-1]
    if not segments:
        raise AssertionError('A visible curve needs at least one cubic segment')
    return segments


def cubic_at(values, t):
    return ((1 - t) ** 3 * values[0] + 3 * (1 - t) ** 2 * t * values[1]
            + 3 * (1 - t) * t * t * values[2] + t ** 3 * values[3])


def extrema_parameters(values):
    a = 3 * (-values[0] + 3 * values[1] - 3 * values[2] + values[3])
    b = 6 * (values[0] - 2 * values[1] + values[2])
    c = 3 * (values[1] - values[0])
    if abs(a) < 1e-10:
        roots = [] if abs(b) < 1e-10 else [-c / b]
    else:
        discriminant = b * b - 4 * a * c
        roots = [] if discriminant < 0 else [(-b + math.sqrt(discriminant)) / (2 * a),
                                             (-b - math.sqrt(discriminant)) / (2 * a)]
    return [0, 1] + [root for root in roots if 0 < root < 1]


def validate_cubic(segment):
    """Use derivative extrema, rather than sampling past a narrow overshoot."""
    xs, ys = [point[0] for point in segment], [point[1] for point in segment]
    if not xs[3] > xs[0]:
        raise AssertionError('Daily curve must advance to the next date')
    for values, label in ((xs, 'date'), (ys, 'delta')):
        lower, upper = min(values[0], values[3]), max(values[0], values[3])
        extrema = [cubic_at(values, t) for t in extrema_parameters(values)]
        if min(extrema) < lower - .04 or max(extrema) > upper + .04:
            raise AssertionError(f'Cubic curve invents an extremum outside adjacent {label} facts')
    positions = [cubic_at(xs, index / 32) for index in range(33)]
    if any(second < first - .02 for first, second in zip(positions, positions[1:])):
        raise AssertionError('Cubic curve reverses date order')


def box(locator):
    return locator.evaluate("""element => {
      const r=element.getBoundingClientRect(), s=getComputedStyle(element);
      return {x:r.x,y:r.y,width:r.width,height:r.height,right:r.right,bottom:r.bottom,
        clientWidth:element.clientWidth,scrollWidth:element.scrollWidth,
        clientHeight:element.clientHeight,scrollHeight:element.scrollHeight,
        overflowX:s.overflowX,overflowY:s.overflowY};
    }""")


def no_overflow(runner, locator, label):
    expect(locator).to_be_visible()
    geometry = box(locator)
    runner.check(geometry['scrollWidth'] <= geometry['clientWidth'] + 1,
                 f'{label} has no horizontal overflow')
    return geometry


def reachable(runner, locator, label):
    expect(locator).to_be_visible()
    locator.scroll_into_view_if_needed(timeout=4000)
    geometry, viewport = box(locator), locator.page.viewport_size
    runner.check(geometry['x'] >= -1 and geometry['right'] <= viewport['width'] + 1
                 and geometry['y'] >= -1 and geometry['bottom'] <= viewport['height'] + 1,
                 f'{label} fits the visible viewport')
    runner.check(locator.evaluate("""element => {
      const r=element.getBoundingClientRect(), hit=document.elementFromPoint(r.x+r.width/2,r.y+r.height/2);
      return !!hit && (hit===element || element.contains(hit));
    }"""), f'{label} is reachable without an overlay')
    return geometry


def settings_geometry(runner, page, phase):
    page.evaluate('window.scrollTo(0, 0)')
    width, height = page.viewport_size['width'], page.viewport_size['height']
    document_flow = width <= 900 or height <= 600
    workspace = no_overflow(runner, page.locator('.settings-layout'), f'Settings {phase} workspace')
    categories = page.get_by_role('complementary', name='设置分类')
    expect(categories).to_be_visible()
    navigation = box(categories)
    editor = no_overflow(runner, page.locator('.settings-editor'), 'Settings editor')
    side_by_side = navigation['right'] <= editor['x'] + 4
    if side_by_side:
        runner.check(navigation['scrollWidth'] <= navigation['clientWidth'] + 1,
                     'Desktop settings categories have no horizontal overflow')
        runner.check(abs(navigation['bottom'] - editor['bottom']) <= 3,
                     f'Settings {phase} navigation and editor have equal bottoms')
        runner.check(abs(navigation['y'] - editor['y']) <= 3, 'Settings columns share their top edge')
    else:
        runner.check(width <= 900 and navigation['bottom'] <= editor['y'] + 3,
                     'Mobile settings keep categories above their flowing content')
        runner.check(navigation['scrollWidth'] <= navigation['clientWidth'] + 1
                     or navigation['overflowX'] in ('auto', 'scroll'),
                     'Stacked categories keep excess labels in a usable local scroll strip')
        runner.check(navigation['x'] >= -1 and navigation['right'] <= width + 1,
                     'Category scroll strip remains inside the viewport')
    fill_bottom = workspace['bottom']
    savebar = page.locator('.settings-savebar')
    if savebar.is_visible():
        fill_bottom = max(fill_bottom, box(savebar)['bottom'])
    runner.check(fill_bottom >= height - 28,
                 f'Settings {phase} fills the available viewport')
    flow = None
    if not document_flow:
        runner.check(workspace['bottom'] <= height + 48,
                     'Desktop settings content is bounded by the workspace')
    else:
        content = no_overflow(runner, page.locator('.settings-editor-content'), 'Flowing settings content')
        runner.check(content['overflowY'] == 'visible' and content['bottom'] <= editor['bottom'] + 3,
                     'Short or stacked settings grow in document flow without a clipped editor')
        flow = page.evaluate("""() => {
          const root=document.scrollingElement;
          window.scrollTo({top:root.scrollHeight,behavior:'instant'});
          return {scrollHeight:root.scrollHeight,clientHeight:root.clientHeight,scrollY:window.scrollY,
            overflowY:getComputedStyle(root).overflowY};
        }""")
        page.evaluate('window.scrollTo(0, 0)')
        runner.check(flow['scrollHeight'] >= workspace['bottom'] - 1
                     and flow['overflowY'] not in ('hidden', 'clip'),
                     'Document scrolling contains the complete settings workspace')
        scroll_range = max(0, flow['scrollHeight'] - flow['clientHeight'])
        runner.check(scroll_range <= 1 or abs(flow['scrollY'] - scroll_range) <= 2,
                     'Long settings content can reach the document scroll boundary')
    runner.record(**{phase: {'workspace': workspace, 'navigation': navigation, 'editor': editor,
                            'equal_bottom_applicable': side_by_side,
                            'document_flow': document_flow, 'document': flow, 'fill_bottom': fill_bottom,
                            'bottom_difference': round(navigation['bottom'] - editor['bottom'], 3)}})


def settings_case(runner, page, data):
    expect(page.locator('.settings-editor-content .setting-row').first).to_be_visible()
    settings_geometry(runner, page, 'timeouts')
    runner.photo(page, 'timeouts')
    page.get_by_role('complementary', name='设置分类').get_by_role('button', name=re.compile('^自动任务')).click()
    enabled = page.get_by_role('switch', name='每日余额统计', exact=True)
    clock = page.locator('[data-setting-key="site_daily_balance_time"] input[type="time"]')
    expect(enabled).to_have_attribute('aria-checked', 'true')
    expect(clock).to_have_value('00:05')
    settings_geometry(runner, page, 'daily-short')
    clock.fill('00:06')
    savebar = page.locator('.settings-savebar')
    expect(savebar).to_contain_text('未保存')
    reachable(runner, savebar.get_by_role('button', name=re.compile('保存')), 'Dirty settings save control')
    settings_geometry(runner, page, 'daily-dirty')
    runner.photo(page, 'daily-dirty')
    search = page.get_by_role('textbox', name='搜索全部设置', exact=True)
    search.fill('alignment-no-such-setting')
    expect(page.locator('.settings-editor')).to_contain_text('没有符合条件的设置')
    settings_geometry(runner, page, 'empty-search')
    search.fill('')
    expect(clock).to_have_value('00:06')
    runner.check(not data.settings_writes, 'Changing a draft never writes real settings')


def responsive_settings_case(runner, page, data):
    settings_case(runner, page, data)
    page.get_by_role('complementary', name='设置分类').get_by_role('button', name=re.compile('^请求缓存')).click()
    capacity_row = page.locator('[data-setting-key="request_cache_max_bytes"]')
    capacity = capacity_row.locator('input[type="text"]')
    capacity.fill('129')
    expect(capacity).to_have_attribute('aria-invalid', 'true')
    alert = capacity_row.get_by_role('alert')
    expect(alert).to_be_visible()
    settings_geometry(runner, page, 'invalid-capacity')
    reachable(runner, capacity, 'Invalid capacity input')
    reachable(runner, alert, 'Invalid capacity explanation')
    save = page.locator('.settings-savebar').get_by_role('button', name='保存更改', exact=True)
    reachable(runner, save, 'Save control with invalid settings')
    expect(save).to_be_enabled()
    # Client validation prevents the confirmation dialog and every API write.
    save.click()
    expect(capacity).to_be_focused()
    runner.check(not data.settings_writes, 'Invalid responsive settings never reach the settings API')
    reachable(runner, capacity, 'Focused invalid capacity after save attempt')
    expect(capacity).to_have_attribute('aria-invalid', 'true')
    settings_geometry(runner, page, 'invalid-save-feedback')
    reachable(runner, alert, 'Validation feedback after save attempt')
    runner.photo(page, 'invalid-save-feedback')


def connect_geometry(runner, page, phase):
    page.evaluate('window.scrollTo(0, 0)')
    workspace = no_overflow(runner, page.locator('.connect-workspace'), 'Connection workspace')
    selection = no_overflow(runner, page.locator('.connect-selection'), 'Connection selection')
    output = no_overflow(runner, page.locator('.connect-output'), 'Connection output stack')
    config = no_overflow(runner, page.locator('.connect-config'), 'Configuration panel')
    probe = no_overflow(runner, page.locator('.connect-probe'), 'Probe panel')
    paired = selection['right'] <= output['x'] + 4
    if paired:
        runner.check(abs(selection['bottom'] - output['bottom']) <= 3,
                     f'Connection {phase} selection and output stack have equal bottoms')
    else:
        runner.check(page.viewport_size['width'] == 390 and selection['bottom'] <= output['y'] + 3,
                     'Mobile connection panels follow a usable vertical reading order')
    runner.check(config['bottom'] <= probe['y'] + 3, 'Probe follows configuration without overlapping')
    runner.check(abs(output['bottom'] - probe['bottom']) <= 3, 'Probe anchors the bottom of the right stack')
    runner.check(workspace['bottom'] >= max(selection['bottom'], output['bottom']) - 3,
                 'Connection workspace contains both columns')
    runner.record(**{phase: {'selection': selection, 'output': output, 'config': config, 'probe': probe,
                            'equal_bottom_applicable': paired,
                            'bottom_difference': round(selection['bottom'] - output['bottom'], 3)}})


def connect_case(runner, page, data):
    trigger = page.get_by_role('button', name='读取现有令牌', exact=True)
    expect(trigger).to_be_enabled()
    connect_geometry(runner, page, 'before-secret')
    runner.photo(page, 'before-secret')
    trigger.click()
    expect(page.get_by_role('button', name='重新读取令牌', exact=True)).to_be_enabled()
    models = page.locator('#connect-model')
    expect(models).to_be_enabled()
    expect(models.locator('option', has_text=MODEL)).to_have_count(1)
    models.select_option(MODEL)
    expect(page.locator('.connect-code')).to_be_visible()
    connect_geometry(runner, page, 'after-secret-config')
    runner.photo(page, 'after-secret-config')
    for title in ('环境变量与启动', '终端验证', '配置文件'):
        page.get_by_role('group', name='配置内容').get_by_role('button', name=title, exact=True).click()
        expect(page.locator('.connect-code')).to_be_visible()
        connect_geometry(runner, page, 'artifact-' + title)
    clients = page.get_by_role('group', name='客户端', exact=True)
    clients.get_by_role('button', name=re.compile('^OpenAI SDK')).click()
    expect(models).to_be_enabled()
    connect_geometry(runner, page, 'openai-sdk')
    expect(page.get_by_role('button', name='发送验证请求', exact=True)).to_be_enabled()
    # Deliberately do not press this button, run downloaded commands or open a
    # documentation link. Model listings above are fulfilled in the interceptor.
    runner.check(data.synthetic_reveals > 0 and data.synthetic_model_reads > 0,
                 'Config exercised a synthetic secret and synthetic model list')
    runner.check(not any(call['method'] != 'GET' for call in data.calls),
                 'Connection exercise never sends a paid gateway probe')
    runner.photo(page, 'configured-no-probe')


def pagination_geometry(runner, pager, phase):
    pager.scroll_into_view_if_needed(timeout=4000)
    no_overflow(runner, pager, f'{phase} pagination')
    controls = pager.locator('.pagination-controls')
    no_overflow(runner, controls, f'{phase} pagination control row')
    no_overflow(runner, pager.locator('.pagination-jump'), f'{phase} jump group')
    previous = pager.get_by_role('button', name='上一页', exact=True)
    following = pager.get_by_role('button', name='下一页', exact=True)
    number = controls.locator(':scope > strong')
    jump = pager.get_by_role('textbox', name='跳转页码', exact=True)
    submit = pager.get_by_role('button', name='跳转', exact=True)
    geometry = [reachable(runner, control, f'{phase} control {index}')
                for index, control in enumerate((previous, number, following, jump, submit))]
    centers = [item['y'] + item['height'] / 2 for item in geometry]
    runner.check(max(centers) - min(centers) <= 4,
                 f'{phase} previous/page/next/input/jump stay on one control row')
    for index, first in enumerate(geometry):
        for second in geometry[index + 1:]:
            intersection = (min(first['right'], second['right']) - max(first['x'], second['x']),
                            min(first['bottom'], second['bottom']) - max(first['y'], second['y']))
            runner.check(not (intersection[0] > 1 and intersection[1] > 1),
                         f'{phase} pagination controls do not overlap')
    runner.record(**{phase: {'control_geometry': geometry, 'center_spread': max(centers) - min(centers)}})


def pagination_case(runner, page, data, kind):
    label = {'routing': '模型清单分页', 'accounts': '账号分页', 'sites': '站点分页', 'channels': '分页'}[kind]
    rows = {'routing': '.model-route-model', 'accounts': '.record-row.account-grid',
            'sites': '.site-row', 'channels': '.channel-row'}[kind]
    if kind == 'routing':
        choice = page.locator('.model-route-mobile-choice button')
        if choice.is_visible() and choice.get_attribute('aria-expanded') != 'true':
            choice.click()
    pager = page.get_by_role('navigation', name=label, exact=True)
    expect(page.locator(rows)).to_have_count(50)
    expected_pages = 12 if kind == 'routing' else 3
    page_number = pager.locator('.pagination-controls > strong')
    expect(page_number).to_have_text(f'1 / {expected_pages}')
    expect(pager.get_by_role('button', name='上一页', exact=True)).to_be_disabled()
    pagination_geometry(runner, pager, 'first')
    pager.get_by_role('button', name='下一页', exact=True).click()
    expect(page_number).to_have_text(f'2 / {expected_pages}')
    pagination_geometry(runner, pager, 'second')
    jump = pager.get_by_role('textbox', name='跳转页码', exact=True)
    jump.fill('not-a-page'); jump.press('Enter')
    expect(jump).to_have_attribute('aria-invalid', 'true')
    error = pager.get_by_role('alert')
    expect(error).to_be_visible()
    pagination_geometry(runner, pager, 'invalid-jump')
    runner.check(box(error)['y'] >= box(pager.locator('.pagination-controls'))['bottom'] - 1,
                 'Jump validation occupies its own row below the controls')
    expect(jump).to_be_focused()
    runner.photo(page, 'invalid-jump')
    jump.fill(str(expected_pages)); jump.press('Enter')
    expect(page_number).to_have_text(f'{expected_pages} / {expected_pages}')
    expect(page.locator(rows)).to_have_count(21)
    expect(pager.get_by_role('button', name='下一页', exact=True)).to_be_disabled()
    expect(error).to_have_count(0)
    pagination_geometry(runner, pager, 'last')
    if kind == 'routing':
        requests = [call for call in data.calls if call['path'] == '/admin/routing/models']
        runner.check(any(call.get('query', {}).get('offset') == ['550']
                         and call.get('query', {}).get('limit') == ['50'] for call in requests),
                     'Narrow inventory really requests the final server page with limit 50')
    runner.photo(page, 'last-page')


def shared_chart_geometry(runner, page, points):
    """Validate rendered facts, independent currency scales and cubic extrema.

    Coordinates come from SVG paths, not invisible/mock point metadata. This
    also serves the older layout case, which has a smaller literal fixture.
    """
    panel = page.locator('.balance-history-lines')
    expect(panel).to_be_visible()
    svg = panel.locator('svg.balance-history-overlay')
    expect(svg).to_have_count(1)
    expect(panel.locator('.balance-history-line-plot')).to_have_count(1)
    expect(svg.locator('circle')).to_have_count(0)
    svg.scroll_into_view_if_needed()
    hits = panel.locator('.balance-history-hit[data-day]')
    days = hits.evaluate_all('(nodes)=>nodes.map(n=>n.dataset.day)')
    rendered_start = panel.get_attribute('data-balance-start-day')
    rendered_end = panel.get_attribute('data-balance-end-day')
    assert rendered_start and rendered_end, 'Rendered balance panel must expose its authoritative calendar bounds'
    first = datetime.fromisoformat(rendered_start).toordinal()
    last = datetime.fromisoformat(rendered_end).toordinal()
    expected_days = [datetime.fromordinal(day).date().isoformat() for day in range(first, last + 1)]
    runner.check(days == expected_days, 'One bounded shared calendar includes every date, including absent samples')
    positions = hits.evaluate_all('(nodes)=>nodes.map(n=>Number(n.dataset.x))')
    runner.check(all(abs(x - (500 if len(days) == 1 else 80 + index / (len(days) - 1) * 840)) < .05
                     for index, x in enumerate(positions)), 'Date hit targets align with both original-currency paths')
    zero = svg.locator('.balance-history-zero')
    expect(zero).to_have_count(1)
    zero_y = float(zero.get_attribute('y1'))
    runner.check(abs(zero_y - 150) < .05 and zero.get_attribute('y1') == zero.get_attribute('y2'),
                 'Both original-currency axes share the centered horizontal zero')
    axes = panel.locator('.balance-history-currency-axis[data-currency]')
    currencies = axes.evaluate_all('(nodes)=>nodes.map(n=>n.dataset.currency)')
    runner.check(len(currencies) == len(set(currencies)) and 0 < len(currencies) <= 2,
                 'Each selected original currency has exactly one independent axis')
    if len(currencies) == 2:
        runner.check(set(axes.evaluate_all('(nodes)=>nodes.map(n=>n.dataset.side)')) == {'left', 'right'},
                     'Two currencies have separate left and right unit axes')
    metrics = {'calendar': days, 'zero_y': zero_y, 'currencies': currencies, 'series': {}}
    for currency in currencies:
        facts = {p['day']: p['daily_delta'] for p in points if p['currency'] == currency
                 and p['day'] in days and p.get('comparison_status') == 'complete'
                 and isinstance(p.get('daily_delta'), (int, float)) and math.isfinite(p['daily_delta'])}
        expected_edges = {(a, b) for a, b in zip(days, days[1:]) if a in facts and b in facts}
        expected_curve_days = {day for edge in expected_edges for day in edge}
        paths = svg.locator(f'path.balance-history-line[data-currency="{currency}"]')
        rendered, edges, ratios, cubic_count = {}, set(), [], 0
        styles = set()
        for path in paths.all():
            raw = path.get_attribute('d') or ''
            runner.check('NaN' not in raw and 'Infinity' not in raw, 'Currency curve geometry is finite')
            styles.add(path.get_attribute('data-style'))
            segments = cubic_segments(raw)
            endpoints = [point for segment in segments for point in (segment[0], segment[-1])]
            mapped = []
            for x, y in endpoints:
                index = 0 if len(days) == 1 else round((x - 80) / 840 * (len(days) - 1))
                runner.check(0 <= index < len(days), 'Curve endpoint stays in the shared date domain')
                day = days[index]
                expected_x = 500 if len(days) == 1 else 80 + 840 * index / (len(days) - 1)
                runner.check(abs(x - expected_x) < .06 and day in facts,
                             f'{currency} endpoint is a real comparable value on its exact shared date')
                rendered[day] = {'x': x, 'y': y}; mapped.append(day)
                amount = facts[day]
                if amount == 0:
                    runner.check(abs(y - zero_y) < .05, f'{currency} actual zero lies on the shared zero')
                else:
                    runner.check((zero_y - y) * amount > 0, f'{currency} signed daily value appears on the truthful side of zero')
                    ratios.append((zero_y - y) / amount)
            for index, segment in enumerate(segments):
                validate_cubic(segment); cubic_count += 1
                edges.add((mapped[index * 2], mapped[index * 2 + 1]))
        runner.check(set(rendered) == expected_curve_days,
                     f'{currency} draws every connected trusted day, with no glyph for isolated/unknown/baseline observations')
        runner.check(edges == expected_edges and cubic_count == len(expected_edges),
                     f'{currency} shape-preserving cubics break at unknown values and missing calendar dates')
        if ratios:
            runner.check(max(ratios) - min(ratios) < max(ratios) * .005,
                         f'{currency} original values retain one independent symmetric vertical scale')
        ticks = panel.locator(
            f'.balance-history-currency-axis[data-currency="{currency}"] .balance-history-tick').evaluate_all(
                '(nodes)=>nodes.map(n=>({value:Number(n.dataset.value),y:Number(n.dataset.y),text:n.textContent}))')
        expected_tick_count = 0 if not facts else 1 if all(value == 0 for value in facts.values()) else 5
        runner.check(len(ticks) == expected_tick_count and all(math.isfinite(t['value']) and math.isfinite(t['y']) for t in ticks),
                     f'{currency} axis represents original-value bounds, with truthful empty/all-zero states')
        tick_values = [t['value'] for t in ticks]
        runner.check(not tick_values or abs(min(tick_values) + max(tick_values)) < .00001 and 0 in tick_values,
                     f'{currency} displayed original-unit ticks are symmetric around zero')
        if facts and any(value != 0 for value in facts.values()):
            runner.check(abs(max(tick_values) - max(abs(value) for value in facts.values())) < .00001,
                         f'{currency} axis labels retain its own actual daily magnitudes')
        for tick in ticks:
            if tick['value'] == 0:
                runner.check(abs(tick['y'] - zero_y) < .05, 'Each unit-axis zero aligns with the shared zero line')
            elif ratios:
                runner.check(abs((zero_y - tick['y']) / tick['value'] - ratios[0]) < ratios[0] * .005,
                             f'{currency} curve uses its displayed original-currency axis, with no conversion')
        metrics['series'][currency] = {'days': sorted(rendered), 'edges': sorted(edges),
                                      'scale': ratios[0] if ratios else None, 'styles': sorted(styles)}
    if len(currencies) == 2 and all(metrics['series'][currency]['days'] for currency in currencies):
        runner.check(metrics['series'][currencies[0]]['styles'] == ['solid'] and metrics['series'][currencies[1]]['styles'] == ['dashed'],
                     'Left and right currencies retain distinct solid/dashed identities')
        colors = svg.locator('path.balance-history-line').evaluate_all('(nodes)=>nodes.map(n=>({currency:n.dataset.currency,stroke:getComputedStyle(n).stroke,dash:getComputedStyle(n).strokeDasharray}))')
        by_currency = {entry['currency']: entry for entry in colors}
        if set(by_currency) == {'CNY', 'USD'}:
            runner.check(by_currency['CNY']['stroke'] != by_currency['USD']['stroke']
                         and by_currency['CNY']['dash'] != by_currency['USD']['dash'],
                         'Computed strokes distinguish both currencies with color and line pattern')
    return metrics


def shared_readout(runner, page, points, day):
    hit = page.locator(f'.balance-history-hit[data-day="{day}"]')
    reachable(runner, hit, f'Daily readout {day}')
    hit.focus()
    expect(hit).to_be_focused()
    expect(page.locator('.balance-history-crosshair')).to_have_attribute('data-day', day)
    expect(page.locator('.balance-history-readout-value')).to_have_count(page.locator('.balance-history-currency-axis').count())
    for value in page.locator('.balance-history-readout-value[data-currency]').all():
        currency = value.get_attribute('data-currency')
        expect(value).to_have_attribute('data-day', day)
        runner.check(not value.evaluate('(element) => element.hasAttribute("title")'), 'Per-day readouts keep diagnostic explanations out of hover tooltips')
        expect(value).not_to_contain_text(re.compile('未知|无记录|已记录|采样|差额|基线|账号范围|覆盖|溢出'))
        fact = next((p for p in points if p['day'] == day and p['currency'] == currency), None)
        if fact is None:
            expect(value).to_have_attribute('data-status', 'missing')
            expect(value).to_have_attribute('data-value', '')
            expect(value.locator('b')).to_have_text(f'{currency} —')
        else:
            expect(value).to_have_attribute('data-status', fact['comparison_status'])
            if fact['daily_delta'] is not None and fact['comparison_status'] == 'complete':
                # Require the exact formatted original amount, including decimals and sign.
                amount = fact['daily_delta']
                runner.check(float(value.get_attribute('data-value')) == amount, 'Exact daily readout retains the raw original amount')
                text = value.inner_text().replace(',', '')
                places = max(2, -Decimal(str(amount)).as_tuple().exponent)
                number = re.escape(f'{abs(amount):.{places}f}')
                pattern = (r'\+[^\d\n]{0,8}' if amount > 0 else r'[-−][^\d\n]{0,8}' if amount < 0 else r'(?<![\d.])') + number + r'(?!\d)'
                runner.check(bool(re.search(pattern, text)), f'{currency} {day} readout preserves exact signed daily delta {amount}')
            else:
                expect(value.locator('b')).to_have_text(f'{currency} —')
                runner.check(not re.search(r'(?:\+|\$|¥|￥)?\b0\.00\b', value.inner_text()),
                             'Unknown comparison readout never invents a zero delta')


def signed_chart(runner, page, data):
    panel = page.locator('.balance-history-lines')
    page.get_by_role('button', name='余额变化 的说明').click()
    tip = page.get_by_role('tooltip')
    expect(tip).to_contain_text('00:05')
    expect(tip).to_contain_text(re.compile('Asia/Shanghai|北京时间'))
    page.get_by_role('button', name='余额变化 的说明').click()
    runner.record(shared_plot=shared_chart_geometry(runner, page, data.balance_history))
    shared_readout(runner, page, data.balance_history, '2026-11-11')
    page.keyboard.press('Tab')
    expect(page.locator('.balance-history-hit[data-day="2026-11-12"]')).to_be_focused()
    shared_readout(runner, page, data.balance_history, '2026-11-12')
    runner.photo(page, 'keyboard-negative')
    for day in (13, 16, 17, 20, 23):
        shared_readout(runner, page, data.balance_history, f'2026-11-{day:02d}')
    page.keyboard.press('Escape')
    expect(page.locator('.balance-history-crosshair')).to_have_count(0)


def baseline_chart(runner, page, data):
    runner.record(shared_plot=shared_chart_geometry(runner, page, data.balance_history))
    expect(page.locator('path.balance-history-line')).to_have_count(0)
    shared_readout(runner, page, data.balance_history, '2026-11-24')
    runner.photo(page, 'baseline-readout')


def empty_chart(runner, page, data):
    panel = page.locator('.balance-history-lines')
    expect(panel).to_be_visible()
    expect(panel.get_by_role('button', name='余额变化 的说明')).to_be_visible()
    page.get_by_role('button', name='余额变化 的说明').click()
    expect(page.get_by_role('tooltip')).to_contain_text('00:05')
    runner.check(bool(panel.get_attribute('data-balance-start-day')) and bool(panel.get_attribute('data-balance-end-day')),
                 'Empty samples keep the selected fixed calendar visible')
    runner.check(page.locator('path.balance-history-line').count() == 0,
                 'No sampled data produces no fabricated curve')


def model_pricing_audit(runner, page, data):
    pricing = data.pricing_reads

    def pump_until(predicate, label, timeout=8, release=True):
        deadline = min(time.monotonic() + timeout, runner.deadline)
        while not predicate() and time.monotonic() < deadline:
            if release:
                pricing.release_due()
            # Unlike time.sleep in a route callback, this dispatches browser
            # requests/aborts and leaves the page running during deferred reads.
            page.wait_for_timeout(10)
        runner.check(predicate(), label)

    # First mount leaves a genuinely unfinished queue; no response is released.
    pump_until(lambda: bool(pricing.phases['cancel']['started']), 'Model pricing starts before navigation',
               release=False)
    page.wait_for_timeout(50)
    initial = list(pricing.phases['cancel']['started'])
    runner.check(0 < len(initial) <= 3 < len(pricing.known_ids),
                 'Initial model pricing keeps at most three reads in flight and leaves queued owners')
    runner.check(pricing.phases['cancel']['max_inflight'] <= 3, 'Navigation batch respects pricing concurrency 3')
    pricing.expected_cancellation = True
    page.evaluate("location.hash='/sites'")
    expect(page.get_by_role('heading', name='站点管理', exact=True)).to_be_visible()
    observed_aborts = page.evaluate("window.__alignmentPricingFetches.filter(row=>row.phase==='cancel' && row.status==='aborted').map(row=>row.site_id)")
    runner.check(sorted(observed_aborts) == sorted(initial),
                 'Leaving models aborts each originating price-fetch signal')
    pricing.cancel_observed(observed_aborts)
    # Release anything left after unmount: an uncancelled worker must not start
    # another queued owner merely because a held response settles.
    pricing.release_due(force=True)
    until = min(time.monotonic() + .25, runner.deadline)
    while time.monotonic() < until:
        pricing.release_due()
        page.wait_for_timeout(10)
    cancelled = pricing.phases['cancel']
    runner.check(cancelled['started'] == initial, 'Navigation cancels all queued pricing reads before they start')
    runner.check(sorted(cancelled['cancelled']) == sorted(initial) and not cancelled['completed'],
                 'Navigation aborts the unfinished synthetic pricing requests')
    runner.check(not pricing.pending, 'Navigation leaves no held synthetic price response')

    pricing.begin_phase('complete')
    page.evaluate("window.__alignmentPricingPhase='complete'")
    page.evaluate("location.hash='/models'")
    expect(page.get_by_role('heading', name='模型测试', exact=True)).to_be_visible()
    pump_until(lambda: len(pricing.phases['complete']['completed']) == len(pricing.known_ids),
               'Model pricing finishes every known fixture owner', timeout=15)
    page.wait_for_timeout(30)
    complete = pricing.phases['complete']
    runner.check(set(complete['started']) == pricing.known_ids
                 and len(complete['started']) == len(pricing.known_ids),
                 'Complete catalog issues exactly one GET for each known site, with no duplicates or missing owner')
    runner.check(set(complete['completed']) == pricing.known_ids and not complete['cancelled'],
                 'All known site price reads finish without dropping a price summary')
    runner.check(0 < complete['max_inflight'] <= 3 and not pricing.pending,
                 'Complete catalog keeps synthetic pricing concurrency at most 3')
    fetches = page.evaluate('window.__alignmentPricingFetches')
    complete_fetches = [row for row in fetches if row['phase'] == 'complete']
    runner.check(len(complete_fetches) == len(pricing.known_ids)
                 and {row['site_id'] for row in complete_fetches} == pricing.known_ids
                 and all(row['method'] == 'GET' and row['status'] == 'completed' for row in complete_fetches),
                 'Browser price fetches agree with one completed GET for every known site')
    runner.check(max(row['inflight_at_start'] for row in fetches) <= 3,
                 'Application price-fetch concurrency never exceeds 3 in either phase')
    runner.record(pricing_reads=pricing.snapshot(), browser_price_fetches=fetches)


def visual_case(runner, page, data, title):
    expect(page.get_by_role('heading', name=title, exact=True)).to_be_visible()
    if data.pricing_reads is not None:
        model_pricing_audit(runner, page, data)
    runner.check(page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'),
                 'Main page remains inside the viewport')
    controls = page.locator('.header-controls button:visible, .header-controls a:visible')
    for control in controls.all()[:5]:
        reachable(runner, control, 'Page header action')
    page.evaluate('window.scrollTo(0, 0)'); runner.photo(page, 'top')
    page.evaluate('window.scrollTo(0, document.documentElement.scrollHeight)')
    runner.photo(page, 'bottom-viewport')
    runner.record(page_extent=page.evaluate('({width:innerWidth,height:innerHeight,scrollHeight:document.documentElement.scrollHeight})'))


def model_catalogue_race(runner, page, data):
    deadline = min(time.monotonic() + 5, runner.deadline)
    while len(data.held_catalogue) < 2 and time.monotonic() < deadline:
        page.wait_for_timeout(10)
    if len(data.held_catalogue) != 2:
        raise AssertionError('Initial catalogue reads did not reach the owned interception boundary')
    expect(page.get_by_text('正在汇总站点与渠道模型', exact=True)).to_be_visible()
    refresh = page.get_by_role('button', name='刷新模型数据', exact=True)
    expect(refresh).to_be_enabled()
    refresh.click()
    marker = page.locator('.model-card strong').filter(has_text=re.compile('^' + RACE_FRESH_MODEL + '$'))
    expect(marker).to_be_visible()
    expect(page.get_by_text('正在汇总站点与渠道模型', exact=True)).to_have_count(0)
    runner.photo(page, 'refreshed-catalogue')

    page.get_by_role('tab', name='连通测试', exact=True).click()
    channel = page.get_by_role('combobox', name='渠道', exact=True)
    channel.select_option(str(data.channels[1]['id']))
    model = page.get_by_role('combobox', name='模型', exact=True)
    model.select_option(RACE_CHOSEN_MODEL)
    page.get_by_role('group', name='测试目标').get_by_role('button', name='站点账号直测', exact=True).click()
    account = page.get_by_role('combobox', name='站点账号', exact=True)
    account.select_option(str(data.accounts[1]['id']))
    model.select_option(RACE_CHOSEN_MODEL)
    expect(page.get_by_role('button', name='开始测试', exact=True)).to_be_enabled()
    runner.photo(page, 'fresh-user-selection-no-probe')

    pricing = data.pricing_reads
    deadline = min(time.monotonic() + 15, runner.deadline)
    while len(pricing.phases['complete']['completed']) < len(pricing.known_ids) and time.monotonic() < deadline:
        pricing.release_due()
        page.wait_for_timeout(10)
    state = pricing.phases['complete']
    runner.check(len(state['completed']) == len(pricing.known_ids)
                 and set(state['started']) == pricing.known_ids
                 and len(state['started']) == len(pricing.known_ids),
                 'Refreshed race catalogue completes one synthetic price GET per known owner')
    runner.check(state['max_inflight'] <= 3, 'Refreshed race pricing stays at concurrency 3')

    data.settle_initial_catalogue(deliver_old=True)
    page.wait_for_timeout(100)
    page.get_by_role('tab', name='模型清单', exact=True).click()
    expect(marker).to_be_visible()
    expect(page.locator('.model-card strong').filter(has_text=re.compile('^' + RACE_OLD_MODEL + '$'))).to_have_count(0)
    page.get_by_role('tab', name='连通测试', exact=True).click()
    expect(account).to_have_value(str(data.accounts[1]['id']))
    expect(model).to_have_value(RACE_CHOSEN_MODEL)
    expect(page.get_by_role('button', name='开始测试', exact=True)).to_be_enabled()
    expect(page.get_by_text('正在汇总站点与渠道模型', exact=True)).to_have_count(0)
    expect(page.locator('.operation-notice--error')).to_have_count(0)
    page.get_by_role('group', name='测试目标').get_by_role('button', name='路由渠道', exact=True).click()
    expect(channel).to_have_value(str(data.channels[1]['id']))
    model.select_option(RACE_CHOSEN_MODEL)
    runner.photo(page, 'current-selection-after-old-load')
    runner.record(catalogue_race={'fresh_marker': RACE_FRESH_MODEL, 'old_marker': RACE_OLD_MODEL,
        'chosen_channel': data.channels[1]['id'], 'chosen_account': data.accounts[1]['id'],
        'chosen_model': RACE_CHOSEN_MODEL,
        'initial_reads': {path: entry['status'] for path, entry in data.held_catalogue.items()}},
        pricing_reads=pricing.snapshot())


def case_specs(visual_audit=False, include_race=False):
    specs = []
    for width, height in ((1440, 900), (390, 844)):
        options = {'width': width, 'height': height, 'touch': width == 390}
        specs.extend((
            (f'settings-equal-bottom-{width}', settings_case, {**options, 'path': '/system?section=runtime&group=timeouts'}),
            (f'connect-equal-bottom-{width}', connect_case, {**options, 'path': '/connect'}),
            (f'stats-daily-signed-{width}', signed_chart, {**options, 'path': '/stats'}),
            (f'stats-daily-baseline-{width}', baseline_chart, {**options, 'path': '/stats'}),
            (f'stats-daily-empty-{width}', empty_chart, {**options, 'path': '/stats'}),
        ))
        for kind, path in (('routing', '/routing?model=group-core'), ('channels', '/channels'),
                           ('accounts', '/accounts'), ('sites', '/sites')):
            callback = partial(pagination_case, kind=kind)
            specs.append((f'pagination-{kind}-{width}', callback, {**options, 'path': path}))
        if visual_audit:
            for name, path, title in VISUAL_PAGES:
                specs.append((f'audit-{name}-{width}', partial(visual_case, title=title), {**options, 'path': path}))
    for name, width, height in (('settings-short-window-1024x560', 1024, 560),
                                ('settings-zoom-equivalent-720x450', 720, 450)):
        specs.append((name, responsive_settings_case, {'width': width, 'height': height,
                     'touch': False, 'path': '/system?section=runtime&group=timeouts'}))
    if include_race:
        specs.append((MODEL_RACE_CASE, model_catalogue_race,
                      {'width': 1440, 'height': 900, 'touch': False, 'path': '/models'}))
    return specs


def runtime_types():
    # Loading existing harness modules is deferred so --help is dependency-free.
    global expect
    from playwright.sync_api import expect, sync_playwright
    from console_layout_smoke import LayoutData, GuardedBrowser
    from ui_refinement_smoke import Runner, StaticHandler, cost_summary
    from console_visual_regression import setting

    class DailyData(LayoutData):
        def __init__(self, case):
            super().__init__(case)
            self.synthetic_reveals = 0; self.synthetic_model_reads = 0
            self.balance_history = daily_points(case)
            self.balance_schedule = copy.deepcopy(SCHEDULE)
            keys = {'site_daily_balance_enabled': ('true', 'bool'), 'site_daily_balance_time': ('00:05', 'string')}
            self.settings = [entry for entry in self.settings if entry['key'] not in keys]
            self.settings.extend(setting(key, value, kind) for key, (value, kind) in keys.items())
            self.channels = [dict(copy.deepcopy(self.channels[index % len(self.channels)]),
                                  id=1000 + index, name=f'alignment-channel-{index:03d}') for index in range(FIXTURE_OWNER_COUNT)]
            self.accounts = [dict(copy.deepcopy(self.accounts[index % len(self.accounts)]),
                                  id=2000 + index, label=f'alignment-account-{index:03d}') for index in range(FIXTURE_OWNER_COUNT)]
            self.sites = [dict(copy.deepcopy(self.sites[index % len(self.sites)]),
                               id=3000 + index, name=f'alignment-site-{index:03d}') for index in range(FIXTURE_OWNER_COUNT)]
            for index, account in enumerate(self.accounts):
                account['site_id'] = self.sites[index]['id']
            for index, channel in enumerate(self.channels):
                channel['site_account_id'] = self.accounts[index]['id']
            self.site_facts = [{'site_account_id': account['id'], 'model': MODEL,
                'route_type': 'openai_chat', 'source': 'models_endpoint', 'disabled': False,
                'stale': False, 'last_seen_at': self.balance_history[-1]['scheduled_at'] if self.balance_history else 1,
                'created_at': 1, 'updated_at': 1} for account in self.accounts[:8]]
            for token in self.tokens:
                token.update(token='', token_recoverable=True, enabled=True, expires_at=0,
                             allowed_models=[], allowed_protocols=[], allowed_channel_ids=[],
                             token_hint=f'alignment-hint-{token["id"]}')
            self.pricing_reads = None
            self.held_catalogue = {}; self.catalogue_read_counts = {}
            if case == MODEL_RACE_CASE:
                for site in self.sites:
                    site['enabled'] = True
                for account in self.accounts:
                    account.update(enabled=True, credential_configured=True)
                for channel in self.channels:
                    channel.update(enabled=True, auth_type='api_key', key_count=1,
                        urls=[{'url': 'https://alignment-owned.example.invalid', 'protocols': ['openai']}],
                        models=[{'model': name, 'disabled': False, 'redirect': ''}
                                for name in ('alignment-race-channel-default', RACE_CHOSEN_MODEL)])
                prototype = copy.deepcopy(self.site_facts[0])
                self.site_facts = [dict(prototype, site_account_id=account['id'], model=name)
                    for account in self.accounts[:2] for name in (RACE_FRESH_MODEL, RACE_CHOSEN_MODEL)]
                self.old_site_facts = [dict(prototype, site_account_id=account['id'], model=RACE_OLD_MODEL)
                                       for account in self.accounts[:2]]
            if case.startswith('audit-models-'):
                self.pricing_reads = SyntheticPricingReads(
                    [site['id'] for site in self.sites],
                    lambda route, site_id: self.fulfill(route, {'site_id': site_id,
                        'available': False, 'models': [], 'group_ratio': {}}), self.unexpected, case,
                    allow_refresh=case == MODEL_RACE_CASE)
                if case == MODEL_RACE_CASE:
                    self.pricing_reads.begin_phase('complete')

        def catalogue_request_failed(self, request):
            for entry in self.held_catalogue.values():
                if entry['request'] == request and entry['status'] == 'held':
                    entry['status'] = 'aborted'

        def settle_initial_catalogue(self, deliver_old=False):
            for path, entry in self.held_catalogue.items():
                if entry['status'] not in ('held', 'aborted'):
                    continue
                route = entry['route']
                if deliver_old and entry['status'] == 'held':
                    payload = self.old_site_facts if path == '/admin/site-models' else []
                    self.fulfill(route, payload, len(payload))
                    entry['status'] = 'old-delivered'
                else:
                    try:
                        route.abort('aborted')
                    except Exception as error:
                        self.unexpected.append({'case': self.case, 'catalogue_abort_error': str(error)[:400]})
                        if 'ERR_ABORTED' not in str(entry['request'].failure):
                            raise
                        route.fallback()
                    entry['status'] = 'abort-settled'

        def route(self, route):
            request = route.request; parsed = urlsplit(request.url)
            path, query = parsed.path, parse_qs(parsed.query)
            if not read_allowed(path, request.method, self.case, [item['id'] for item in self.tokens]):
                self.unexpected.append({'case': self.case, 'unexpected_api': path, 'method': request.method})
                route.abort('blockedbyclient'); return
            call = {'path': path, 'method': request.method, 'query': query}
            if self.case == MODEL_RACE_CASE and path in ('/admin/site-models', '/admin/site-channel-bindings'):
                self.calls.append(call)
                self.catalogue_read_counts[path] = self.catalogue_read_counts.get(path, 0) + 1
                if self.catalogue_read_counts[path] == 1:
                    self.held_catalogue[path] = {'route': route, 'request': request, 'status': 'held'}
                else:
                    payload = self.site_facts if path == '/admin/site-models' else []
                    self.fulfill(route, payload, len(payload))
            elif path == '/admin/site-pricing' and self.pricing_reads is not None:
                self.calls.append(call)
                self.pricing_reads.enqueue(route, query)
            elif path.endswith('/reveal'):
                self.calls.append(call); self.synthetic_reveals += 1
                self.fulfill(route, {'id': int(path.split('/')[3]), 'token': SYNTHETIC_TOKEN, 'token_hint': 'alignment-hint'})
            elif path in ('/v1/models', '/v1beta/models'):
                self.calls.append(call); self.synthetic_model_reads += 1
                data = {'models': [{'name': 'models/' + MODEL}]} if path.startswith('/v1beta/') else {
                    'object': 'list', 'data': [{'id': MODEL, 'object': 'model', 'owned_by': 'synthetic'},
                                             {'id': 'alignment-second-model', 'object': 'model', 'owned_by': 'synthetic'}]}
                route.fulfill(status=200, content_type='application/json', body=json.dumps(data))
            elif path == '/admin/stats':
                self.calls.append(call)
                self.fulfill(route, {'stats': [{'channel_name': '布局合成渠道', 'model': 'layout-model-001',
                    'total': 10, 'success': 9, 'error': 1, 'effective_cost': 1,
                    'total_input_tokens': 100, 'total_output_tokens': 20}],
                    'duration_seconds': 86400, 'is_today': True, 'rpm_stats': {},
                    'balance_history': self.balance_history, 'balance_schedule': self.balance_schedule,
                    'balance_as_of_day': '2026-11-24',
                    'cost_summary': cost_summary('complete')})
            elif path == '/admin/metrics':
                self.calls.append(call)
                self.fulfill(route, [
                    {'ts': f'2026-11-{day:02d}T12:00:00+08:00', 'success': 8 + day % 5,
                     'error': day % 2, 'effective_cost': day * .03,
                     'input_tokens': day * 1200, 'output_tokens': day * 240}
                    for day in range(1, 25)
                ])
            elif path.startswith('/admin/request-traces') or path.startswith('/admin/logs') or path == '/admin/system-access-tokens':
                # These optional audit pages retain the shared fixture convention
                # without allowing an unhandled admin GET to reach the server.
                from model_routes_smoke import ModelRoutesData
                ModelRoutesData.route(self, route)
            else:
                super().route(route)

    class DailyRunner(Runner):
        def __init__(self, browser, base, output, cases, max_seconds, data_factory=None,
                     suite_limit=MAX_SUITE_REQUESTS, case_limit=None, init_script=''):
            self.fixture_factory = data_factory or DailyData
            super().__init__(browser, base, output, cases, data_factory=self.fixture_factory)
            self.browser = GuardedBrowser(browser, self.events, base)
            self.deadline = time.monotonic() + max_seconds
            self.active_case = ''; self.assertions = []; self.request_count = 0
            self.suite_limit, self.case_limit, self.init_script = suite_limit, case_limit, init_script

        def check(self, condition, message):
            self.assertions.append({'case': self.active_case, 'pass': bool(condition), 'check': message})
            super().check(condition, message)
            if time.monotonic() > self.deadline:
                raise AssertionError('Bounded suite time budget exhausted')

        def record(self, **metrics):
            self.metrics.setdefault(self.active_case, {}).update(metrics)

        def photo(self, page, suffix):
            name = f'{self.active_case}-{suffix}.jpg'
            if getattr(self, 'capture_pre_entry', False):
                # The deliberately held entry keeps document.fonts.ready pending.
                # Capture the actual Chromium viewport rather than wait for a
                # readiness condition that cannot finish until after evidence.
                session = page.context.new_cdp_session(page)
                try:
                    # CDPSession.send has no public timeout argument. Bound this
                    # one native call using the same sync transport adapter.
                    image = session._sync(asyncio.wait_for(session._impl_obj.send('Page.captureScreenshot', {
                        'format': 'jpeg', 'quality': 82, 'captureBeyondViewport': False, 'fromSurface': True}), 3))
                    (self.output / name).write_bytes(base64.b64decode(image['data']))
                finally:
                    session.detach()
            else:
                page.screenshot(path=str(self.output / name), type='jpeg', quality=82,
                                full_page=False, animations='disabled', timeout=5000)
            self.metrics.setdefault(self.active_case, {}).setdefault('evidence', []).append(name)

        def case(self, name, callback, path, width, height, touch=False):
            if self.cases and name not in self.cases:
                return
            self.active_case = name
            self.capture_pre_entry = False
            if time.monotonic() >= self.deadline:
                self.results.append({'case': name, 'pass': False, 'error': 'Bounded suite time budget exhausted'})
                return
            context = self.browser.new_context(viewport={'width': width, 'height': height},
                color_scheme='light', reduced_motion='reduce', has_touch=touch)
            context.set_default_timeout(4000); context.set_default_navigation_timeout(15000)
            context.add_init_script("localStorage.setItem('pivotflow_token','alignment-synthetic-admin-only');"
                                    "localStorage.setItem('pivotflow_web_role','admin');"
                                    "if(!localStorage.getItem('pivotflow_theme')) localStorage.setItem('pivotflow_theme','light');")
            if self.init_script:
                context.add_init_script(self.init_script)
            data = self.fixture_factory(name); data.unexpected = self.events
            request_budget = self.case_limit or case_request_budget(name, len(data.sites))
            if data.pricing_reads is not None:
                data.pricing_reads.events = self.events
                context.add_init_script(PRICING_FETCH_OBSERVER)
            page = context.new_page()
            if data.pricing_reads is not None:
                page.on('requestfailed', data.pricing_reads.request_failed)
            if name == MODEL_RACE_CASE:
                page.on('requestfailed', data.catalogue_request_failed)
            page.on('pageerror', lambda error: self.events.append({'case': name, 'pageerror': str(error)[:1000]}))
            def console_event(message):
                if message.type != 'error':
                    return
                expected = getattr(data, 'expected_console_error', None)
                if expected is None or not expected(message):
                    self.events.append({'case': name, 'console_error': message.text[:1000]})
            page.on('console', console_event)
            origin = urlsplit(self.base).netloc
            requested = []

            def intercept(route):
                request = route.request; parsed = urlsplit(request.url)
                requested.append({'path': parsed.path, 'method': request.method})
                self.request_count += 1
                if len(requested) > request_budget or self.request_count > self.suite_limit:
                    self.events.append({'case': name, 'request_budget_exceeded': len(requested),
                                        'case_limit': request_budget, 'suite_requests': self.request_count,
                                        'suite_limit': self.suite_limit})
                    route.abort('blockedbyclient')
                elif parsed.netloc != origin:
                    self.events.append({'case': name, 'external_blocked': parsed.netloc}); route.abort('blockedbyclient')
                elif request.method != 'GET':
                    self.events.append({'case': name, 'unexpected_mutation': parsed.path, 'method': request.method}); route.abort('blockedbyclient')
                elif parsed.path.startswith(('/admin/', '/dashboard/', '/v1/', '/v1beta/')):
                    data.route(route)
                elif parsed.path.startswith('/web/console/') or parsed.path in ('/web/brand-mark.svg', '/web/favicon.svg'):
                    static_interceptor = getattr(data, 'intercept_static', None)
                    if static_interceptor is None or not static_interceptor(route):
                        route.continue_()
                else:
                    self.events.append({'case': name, 'unexpected_static_request': parsed.path}); route.abort('blockedbyclient')

            context.route('**/*', intercept)
            started = time.monotonic(); result = {'case': name, 'pass': False}
            try:
                # The model audit pumps deferred responses explicitly after DOM
                # navigation; waiting for networkidle here would deadlock them.
                page.goto(f'{self.base}/web/console/#{path}',
                          wait_until='domcontentloaded' if data.pricing_reads is not None else 'networkidle')
                expect(page.locator('h1')).to_be_visible()
                callback(self, page, data)
                self.check(page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'), 'Page has no horizontal overflow')
                self.check(not data.settings_writes and not data.token_writes, 'Fixture interaction stayed read-only')
                self.record(requests=requested, api_reads=data.calls, request_count=len(requested),
                            fixture='synthetic-only', balance_schedule=data.balance_schedule)
                result['pass'] = True
            except Exception as error:
                message = f'{type(error).__name__}: {error}'
                result['error'] = message if len(message) <= 6000 else message[:3500] + '\n[bounded middle omitted]\n' + message[-2400:]
                result['traceback'] = traceback.format_exc(limit=7)[-2500:]
                result['phase'] = self.metrics.get(name, {}).get('phase', 'initial-load')
                diagnostics = getattr(data, 'failure_diagnostics', None)
                if diagnostics is not None:
                    try:
                        self.record(failure_diagnostics=diagnostics(page))
                    except Exception as diagnostic_error:
                        result['diagnostic_error'] = str(diagnostic_error)[:400]
                self.record(requests=requested, api_reads=data.calls, request_count=len(requested))
            finally:
                try:
                    self.photo(page, 'final'); result['evidence'] = f'{name}-final.jpg'
                except Exception as error:
                    result['screenshot_error'] = str(error)[:300]; result['pass'] = False
                result['seconds'] = round(time.monotonic() - started, 3)
                self.results.append(result)
                try:
                    cleanup = getattr(data, 'finish_for_close', None)
                    if cleanup is not None:
                        try:
                            cleanup()
                        except Exception as error:
                            result['pass'] = False
                            self.events.append({'case': name, 'fixture_cleanup_error': str(error)[:400]})
                    if data.pricing_reads is not None:
                        try:
                            if name == MODEL_RACE_CASE:
                                data.settle_initial_catalogue()
                            data.pricing_reads.finish_for_close()
                        except Exception as error:
                            result['pass'] = False
                            self.events.append({'case': name, 'pricing_cleanup_error': str(error)[:400]})
                    # Waiting after held pricing routes settle drains their
                    # handlers before context closure can reject API tasks.
                    context.unroute_all(behavior='wait' if data.pricing_reads is not None or self.suite_limit <= 360 else 'ignoreErrors')
                finally:
                    context.close()
                    self.record(request_budget={'case_limit': request_budget, 'suite_limit': self.suite_limit,
                        'known_fixture_sites': len(data.sites),
                        'model_asset_api_allowance': MODEL_AUDIT_ASSET_API_ALLOWANCE if data.pricing_reads is not None else 0,
                        'model_navigation_allowance': MODEL_AUDIT_NAVIGATION_ALLOWANCE if data.pricing_reads is not None else 0})
                    if data.pricing_reads is not None:
                        self.record(pricing_reads=data.pricing_reads.snapshot())
                    if hasattr(data, 'expected_errors'):
                        self.record(expected_errors=data.expected_errors)

    DailyRunner.fixture_type = DailyData
    return DailyRunner, StaticHandler, sync_playwright


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument('--output', type=Path)
    parser.add_argument('--case', action='append', default=[],
                        help='Repeat to select cases; audit-models-race-1440 is explicit-only')
    parser.add_argument('--list-cases', action='store_true')
    parser.add_argument('--visual-audit', action='store_true', help='Add viewport evidence for all 14 main pages at both widths')
    parser.add_argument('--max-seconds', type=int, default=300, help='Suite admission/check budget, 1 to 900 seconds')
    args = parser.parse_args()
    specs = case_specs(args.visual_audit, include_race=MODEL_RACE_CASE in args.case)
    names = {name for name, _, _ in specs}
    if set(args.case) - names:
        parser.error('Unknown cases: ' + ', '.join(sorted(set(args.case) - names)))
    if args.list_cases:
        print('\n'.join(name for name, _, _ in specs)); return
    if not args.output or not 1 <= args.max_seconds <= 900:
        parser.error('--output is required; --max-seconds must be between 1 and 900')
    root, output = args.root.resolve(), args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    result = {'pass': False, 'checks': [], 'evidence': [], 'metrics': {}, 'assertions': [],
              'unexpected_events': [], 'server_closed': False, 'fatal_error': None}
    server = thread = runner = None
    try:
        if not (root / 'web/console/index.html').is_file():
            raise FileNotFoundError('Main-owned built web/console/index.html is required')
        DailyRunner, StaticHandler, sync_playwright = runtime_types()
        # Chromium prohibits 10080 even when Windows chooses it for port zero.
        for _ in range(8):
            server = ThreadingHTTPServer(('127.0.0.1', 0), partial(StaticHandler, root=root))
            if server.server_port != 10080:
                break
            server.server_close(); server = None
        if server is None:
            raise RuntimeError('Could not allocate a browser-safe owned loopback port')
        server.daemon_threads = True
        thread = Thread(target=server.serve_forever, daemon=True); thread.start()
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True, args=['--no-proxy-server', '--disable-background-networking'])
            try:
                runner = DailyRunner(browser, f'http://127.0.0.1:{server.server_port}', output, args.case, args.max_seconds)
                for name, callback, options in specs:
                    runner.case(name, callback, **options)
            finally:
                browser.close()
    except Exception as error:
        result['fatal_error'] = f'{type(error).__name__}: {error}'[:2200]
    finally:
        if runner is not None:
            result.update(checks=runner.results, metrics=runner.metrics, assertions=runner.assertions,
                          unexpected_events=runner.events)
            result['evidence'] = sorted({image for metrics in runner.metrics.values() for image in metrics.get('evidence', [])})
        if server is not None:
            if thread is not None and thread.is_alive():
                server.shutdown()
            server.server_close()
        if thread is not None:
            thread.join(timeout=5)
        result['server_closed'] = thread is None or not thread.is_alive()
        expected = len(set(args.case)) if args.case else len(specs)
        result['pass'] = (len(result['checks']) == expected and all(item['pass'] for item in result['checks'])
                          and not result['unexpected_events'] and result['fatal_error'] is None and result['server_closed'])
        (output / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2, allow_nan=False), encoding='utf-8')
    print(json.dumps({key: result[key] for key in ('pass', 'server_closed', 'fatal_error')}
                     | {'checks': len(result['checks']), 'unexpected_events': len(result['unexpected_events'])}, ensure_ascii=False))
    raise SystemExit(0 if result['pass'] else 1)


if __name__ == '__main__':
    main()
