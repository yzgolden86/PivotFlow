"""Stage 2 synthetic UI checks; only main executes browser/server commands.

Each invocation admits at most 360 requests; each case is capped at 120 or 200.
Groups keep new-context assets/reloads inside that global limit. All API traffic
is synthetic GET, no upstream/probe/paid call, no data/configuration mutation.
Evidence is viewport JPEG in the external stage 2 archive. --help is lazy.
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
from urllib.parse import parse_qs, urlencode, urlsplit

import console_alignment_daily_smoke as shared


ARCHIVE = Path('E:/Dev/tools/api/archive/console-ui-polish-2026-11-24')
MAX_SUITE_REQUESTS = 360
SPECIAL_MODEL = 'vendor/模型 + & # ? % 🚦'
NEW_PRESETS = {'sand': '暖砂', 'ice': '冰川', 'rose': '蔷薇', 'ink': '水墨'}
OLD_PRESETS = ['松石绿', '海湾蓝', '珊瑚红', 'Anthropic', '紫罗兰', '石墨灰', '松林绿', '梅子紫']
NEW_FONTS = ['simhei', 'fangsong', 'aptos']
OLD_FONTS = ['system', 'yahei', 'pingfang', 'dengxian', 'harmony', 'notoserif',
             'songti', 'kaiti', 'segoe', 'consolas', 'cascadia', 'sarasa']
NAV_LABELS = ['系统概览', '站点管理', '账号管理', '签到中心', '公告中心', '渠道分发',
              '模型路由', '接入配置', '令牌管理', '请求日志', '数据面板', '运行健康', '模型测试', '系统设置']
CORE_CASES = ('stage2-shared-plot', 'stage2-navigation-copy', 'stage2-settings-natural')
GROUPS = {'core': CORE_CASES,
          'routing': ('stage2-routing-roundtrip',),
          'appearance': ('stage2-appearance-persistence',),
          'appearance-boot': ('stage2-boot-sand-simhei', 'stage2-boot-ice-fangsong', 'stage2-boot-rose-aptos'),
          'appearance-contrast': ('stage2-primary-contrast',),
          'chart-edges': ('stage2-chart-edges',)}
BOOT_OBSERVER = """(() => {
  window.__stage2Boot=[];
  const snapshot=()=>{const r=document.documentElement;
    window.__stage2Boot.push({preset:r.dataset.themePreset,font:r.dataset.themeFont,
      theme:r.dataset.theme,ms:performance.now()});};
  const install=()=>{if(!document.documentElement)return false;
    new MutationObserver(snapshot).observe(document.documentElement,
      {attributes:true,attributeFilter:['data-theme-preset','data-theme-font','data-theme']});
    snapshot();return true;};
  if(!install()){const ready=new MutationObserver(()=>{if(install())ready.disconnect();});
    ready.observe(document,{childList:true});}
})();"""


def navigate(page, path, title=None):
    page.evaluate('(path)=>{location.hash=path}', path)
    if title:
        expect(page.locator('h1')).to_have_text(title)


def viewport(runner, page, width, height, suffix):
    page.set_viewport_size({'width': width, 'height': height})
    runner.check(page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'),
                 f'{suffix} has no document horizontal overflow')


def chart_case(runner, page, data):
    runner.record(phase='shared-plot-desktop')
    # One currency has a missing calendar day; USD has explicit null/status gaps.
    shared.signed_chart(runner, page, data)
    metrics = shared.shared_chart_geometry(runner, page, data.balance_history)
    scales = [item['scale'] for item in metrics['series'].values()]
    runner.check(all(scale is not None for scale in scales) and abs(scales[0] - scales[1]) > .1,
                 'Different CNY/USD magnitudes produce independent original-currency axes')
    shared.shared_readout(runner, page, data.balance_history, '2026-11-14')
    expect(page.locator('.balance-history-readout-value[data-currency="CNY"]')).to_have_attribute('data-status', 'missing')
    hit = page.locator('.balance-history-hit[data-day="2026-11-12"]')
    hit.hover()
    expect(page.locator('.balance-history-crosshair')).to_have_attribute('data-day', '2026-11-12')
    runner.photo(page, 'desktop-hover')
    viewport(runner, page, 390, 844, 'Shared mobile plot')
    shared.shared_chart_geometry(runner, page, data.balance_history)
    runner.record(phase='shared-plot-mobile-tap')
    page.evaluate("""()=>{window.__stage2TouchEvents=[];
      for(const type of ['pointerdown','pointerup','touchstart','touchend','mousedown','mouseup','click','focusin','focusout','mouseover','mouseout'])
        document.addEventListener(type,event=>{const hit=event.target.closest?.('.balance-history-hit');
          if(hit){window.__stage2TouchEvents.push({type,day:hit.dataset.day,active:document.activeElement?.dataset?.day,
            crosshair:document.querySelector('.balance-history-crosshair')?.dataset.day});
            window.__stage2TouchEvents=window.__stage2TouchEvents.slice(-40);}},true);
    }""")
    page.locator('.balance-history-hit[data-day="2026-11-11"]').tap()
    expect(page.locator('.balance-history-crosshair')).to_have_attribute('data-day', '2026-11-11')
    # Exercise real touch pinning before keyboard focus can mask a lost tap.
    page.locator('.balance-history-hit[data-day="2026-11-12"]').tap()
    expect(page.locator('.balance-history-crosshair')).to_have_attribute('data-day', '2026-11-12')
    page.mouse.move(0, 0)
    expect(page.locator('.balance-history-crosshair')).to_have_attribute('data-day', '2026-11-12')
    page.get_by_role('combobox', name='左轴币种').focus()
    expect(page.locator('.balance-history-crosshair')).to_have_attribute('data-day', '2026-11-12')
    page.locator('.balance-history-hit[data-day="2026-11-13"]').hover()
    expect(page.locator('.balance-history-crosshair')).to_have_attribute('data-day', '2026-11-13')
    page.mouse.move(0, 0)
    expect(page.locator('.balance-history-crosshair')).to_have_attribute('data-day', '2026-11-12')
    shared.shared_readout(runner, page, data.balance_history, '2026-11-11')
    page.keyboard.press('Tab')
    expect(page.locator('.balance-history-hit[data-day="2026-11-12"]')).to_be_focused()
    page.keyboard.press('Escape')
    page.get_by_role('combobox', name='左轴币种').focus()
    expect(page.locator('.balance-history-crosshair')).to_have_count(0)
    runner.photo(page, 'mobile-exact-value')


def refresh_chart(page, data, points):
    data.balance_history = copy.deepcopy(points)
    previous = sum(call['path'] == '/admin/stats' for call in data.calls)
    with page.expect_response(lambda response: urlsplit(response.url).path == '/admin/stats'):
        page.get_by_role('button', name='刷新数据面板', exact=True).click()
    expect(page.get_by_role('button', name='刷新数据面板', exact=True)).to_be_enabled()
    if sum(call['path'] == '/admin/stats' for call in data.calls) <= previous:
        raise AssertionError('Refresh did not observe the new synthetic stats snapshot')


def chart_edges(runner, page, data):
    shared.signed_chart(runner, page, data)
    for name, points in (
        ('baseline', [shared.daily_point(24, 'USD', 120, None, 'baseline')]),
        ('single', [shared.daily_point(24, 'USD', 120, -2.25)]),
        ('zero', [shared.daily_point(day, 'CNY', 100, 0) for day in range(22, 25)]),
        ('all-unknown', [shared.daily_point(24, 'USD', None, None, 'overflow')]),
    ):
        refresh_chart(page, data, points)
        shared.shared_chart_geometry(runner, page, points)
        shared.shared_readout(runner, page, points, '2026-11-24')
        runner.photo(page, name)
    points = shared.daily_points('signed') + [shared.daily_point(day, 'EUR', 80 + day, .125 * (day - 20))
                                             for day in range(11, 25)]
    refresh_chart(page, data, points)
    left, right = page.get_by_role('combobox', name='左轴币种'), page.get_by_role('combobox', name='右轴币种')
    for control in (left, right):
        runner.check(set(control.locator('option').evaluate_all('(nodes)=>nodes.map(n=>n.value)')) >= {'CNY', 'USD', 'EUR'},
                     'All sampled currencies remain selectable in either original-unit axis')
    right.select_option('EUR')
    shared.shared_chart_geometry(runner, page, points)
    shared.shared_readout(runner, page, points, '2026-11-24')
    expect(page.locator('.balance-history-currency-axis[data-currency="EUR"]')).to_be_visible()
    expect(page.locator('path.balance-history-line[data-currency="USD"]')).to_have_count(0)
    runner.photo(page, 'third-currency')
    refresh_chart(page, data, [])
    shared.empty_chart(runner, page, data)
    expect(page.locator('svg.balance-history-overlay circle')).to_have_count(0)
    runner.photo(page, 'empty')


def nav_geometry(runner, page, suffix, require_fit):
    sidebar, nav = page.locator('#primary-sidebar'), page.get_by_role('navigation', name='主导航')
    expect(sidebar).to_be_visible()
    links = nav.locator('a.nav-item')
    runner.check(links.count() == 14, 'All fourteen global entries remain available')
    actual = links.evaluate_all('(nodes)=>nodes.map(n=>n.textContent.trim() || n.title)')
    runner.check(set(actual) == set(NAV_LABELS), 'No navigation entry is omitted or renamed away')
    geometry = shared.box(nav)
    runner.check(geometry['scrollWidth'] <= geometry['clientWidth'] + 1, 'Global navigation has no horizontal overflow')
    if require_fit:
        runner.check(geometry['scrollHeight'] <= geometry['clientHeight'] + 1,
                     f'{suffix} expanded/collapsed desktop navigation fits without scrolling')
        for link in links.all():
            rect = shared.box(link)
            runner.check(rect['y'] >= geometry['y'] - 1 and rect['bottom'] <= geometry['bottom'] + 1,
                         f'{suffix} {link.get_attribute("href")} is fully inside navigation bounds')
    else:
        runner.check(geometry['overflowY'] in ('auto', 'scroll') or geometry['scrollHeight'] <= geometry['clientHeight'] + 1,
                     'Short/mobile/zoom navigation preserves a usable overflow fallback')
        for link in links.all():
            link.focus()
            expect(link).to_be_focused()
            shared.reachable(runner, link, f'{suffix} keyboard navigation')
    runner.record(**{suffix: geometry})
    runner.photo(page, suffix)


def clean_copy(runner, page, label):
    headings = page.locator('main h1')
    expect(headings).to_have_count(1)
    # Detect rendered repeated page title/eyebrow; preserve unrelated useful small.
    duplicates = page.locator('main').evaluate("""main=>{
      const title=main.querySelector('h1')?.textContent.trim();
      return Array.from(main.querySelectorAll('.eyebrow,[class*=eyebrow]')).filter(el=>{
        const r=el.getBoundingClientRect(),s=getComputedStyle(el);
        return r.width>0&&r.height>0&&s.visibility!=='hidden'&&el.textContent.trim();
      }).map(el=>({text:el.textContent.trim(),same:el.textContent.trim()===title}));
    }""")
    runner.check(not duplicates, f'{label} has one page title and no repeated visible eyebrow')
    runner.record(**{f'copy-{label}': {'visible_eyebrows': duplicates}})


def nav_copy(runner, page, data):
    for width, height in ((1440, 900), (1366, 768), (1280, 720)):
        viewport(runner, page, width, height, f'nav-{width}')
        for collapsed in (False, True):
            if collapsed:
                page.get_by_role('button', name='收起侧栏', exact=True).click()
            nav_geometry(runner, page, f'nav-{width}-{"collapsed" if collapsed else "expanded"}', True)
        page.get_by_role('button', name='展开侧栏', exact=True).click()
    for width, height in ((1024, 560), (720, 450), (390, 844)):
        viewport(runner, page, width, height, f'nav-{width}x{height}')
        if width <= 880:
            page.get_by_role('button', name='打开导航', exact=True).click()
        nav_geometry(runner, page, f'nav-{width}x{height}-reachable', False)
        if width <= 880:
            page.locator('.sidebar-mobile-close').click()
    viewport(runner, page, 1440, 900, 'Copy audit desktop')
    # Do not enter /models: its 121 owner pricing reads belong to the existing audit.
    for label, path, title in (
        ('connect', '/connect', '接入配置'), ('routing', '/routing', '模型路由'),
        ('health', '/health', '运行健康'), ('tokens', '/tokens', '令牌管理'),
        ('cache', '/system?section=runtime&group=cache', '系统设置'),
        ('browser-import', '/browser-import', '导入账号'),
    ):
        navigate(page, path, title)
        clean_copy(runner, page, label)
        runner.photo(page, f'copy-{label}')
        if label == 'connect':
            expect(page.locator('.connect-eyebrow,.connect-intro,.connect-next-step')).to_have_count(0)
            expect(page.get_by_role('combobox', name='下游访问令牌')).to_be_visible()
            expect(page.locator('.connect-billing-warning')).to_contain_text('计费')
            expect(page.get_by_role('button', name='发送验证请求', exact=True)).to_be_visible()
            expect(page.get_by_role('button', name='发送验证请求', exact=True)).to_be_disabled()
        if label == 'cache':
            expect(page.locator('[data-setting-key="request_cache_max_bytes"]')).to_be_visible()
        if label == 'browser-import':
            expect(page.locator('.browser-import-intro > p')).to_have_count(0)
            expect(page.get_by_role('list', name='导入步骤', exact=True)).to_be_visible()


def card_geometry(runner, page, group, suffix):
    cards = page.locator('.settings-workspace-section[data-settings-section]')
    runner.check(cards.count() >= 3, f'{group} exposes its real setting groups')
    geometries = cards.evaluate_all("""nodes=>nodes.map(card=>{
      const r=card.getBoundingClientRect(),last=card.querySelector('.setting-row:last-child')?.getBoundingClientRect();
      const s=getComputedStyle(card);
      return {id:card.dataset.settingsSection,x:r.x,y:r.y,right:r.right,bottom:r.bottom,height:r.height,
        lastBottom:last?.bottom,borderBottom:parseFloat(s.borderBottomWidth),paddingBottom:parseFloat(s.paddingBottom)};
    })""")
    columns = {}
    for card in geometries:
        key = next((x for x in columns if abs(x - card['x']) < 2), card['x'])
        columns.setdefault(key, []).append(card)
        runner.check(card['lastBottom'] is not None and abs(card['bottom'] - card['lastBottom'] - card['borderBottom']) <= 3,
                     f'{card["id"]} ends at its final field without stretched shared-row space')
    runner.check(len(columns) == (2 if page.viewport_size['width'] >= 1280 else 1),
                 'Settings cards use two natural desktop columns or one responsive stack')
    tops = [min(card['y'] for card in column) for column in columns.values()]
    runner.check(max(tops) - min(tops) <= 2, 'Natural card columns align their first-card tops')
    for column in columns.values():
        ordered = sorted(column, key=lambda card: card['y'])
        for a, b in zip(ordered, ordered[1:]):
            runner.check(abs(b['y'] - a['bottom'] - 12) <= 2, 'Successive same-column cards retain the 12px natural gap')
    runner.record(**{suffix: geometries})


def settings_natural(runner, page, data):
    for width, height in ((1440, 900), (1366, 768), (1280, 720), (1024, 560), (720, 450), (390, 844)):
        viewport(runner, page, width, height, f'settings-{width}x{height}')
        for group in ('health', 'timeouts'):
            navigate(page, '/system?section=runtime&group=' + group, '系统设置')
            expect(page.locator(f'.settings-workspace-section[data-settings-section^="{group}-"]').first).to_be_visible()
            suffix = f'{group}-{width}x{height}'
            shared.settings_geometry(runner, page, suffix)
            card_geometry(runner, page, group, suffix + '-cards')
            local = page.get_by_role('navigation', name='当前设置分组').get_by_role('button').first
            local.click()
            expect(page.locator('.settings-workspace-section h3').first).to_be_focused()
            categories = page.locator('.settings-groups')
            runner.check(categories.get_by_role('button').count() == 12, 'All twelve settings categories remain available')
            if width >= 1280:
                geometry = shared.box(categories)
                runner.check(geometry['scrollHeight'] <= geometry['clientHeight'] + 1, 'Compact desktop settings categories fit without scrolling')
            runner.photo(page, suffix)
    # Keep meaningful client validation/reachability checks; no valid-save mutation.
    shared.responsive_settings_case(runner, page, data)
    reset = page.locator('[data-setting-key="request_cache_max_bytes"]').get_by_role('button', name=re.compile('重置|恢复'))
    expect(reset).to_have_count(1)
    expect(reset).to_be_enabled()
    shared.reachable(runner, reset, 'Invalid field reset control')
    # Server-backed reset is outside the read-only audit. Discard is local.
    page.once('dialog', lambda dialog: dialog.accept())
    page.get_by_role('button', name='放弃修改', exact=True).click()
    expect(page.locator('.settings-savebar')).to_have_count(0)
    navigate(page, '/system?section=runtime&group=timeouts', '系统设置')
    search = page.get_by_role('textbox', name='搜索全部设置', exact=True)
    search.fill('超时')
    expect(page.locator('[data-setting-key="upstream_first_byte_timeout"]')).to_be_visible()
    card_geometry(runner, page, 'timeouts-search', 'search-cards')
    search.fill('')


def query_contract(runner, page, expected, path):
    actual = urlsplit(page.evaluate('location.hash.slice(1)'))
    runner.check(actual.path == path and all(parse_qs(actual.query).get(key) == [str(value)] for key, value in expected.items()),
                 'Bidirectional link retains exact model, protocol, token and frontend channel context')


def routing_roundtrip(runner, page, data):
    runner.record(phase='channel-list-details')
    token_id = data.tokens[0]['id']
    context = {'model': SPECIAL_MODEL, 'client_protocol': 'codex', 'token_id': token_id}
    row = page.locator('.channel-row').filter(has=page.get_by_text(data.channels[0]['name'], exact=True))
    expect(row).to_be_visible()
    identity = row.locator('.channel-identity')
    expect(identity).to_contain_text(data.channels[0]['name'])
    expect(identity).to_contain_text('#1000')
    expect(identity).to_contain_text(re.compile('Key|api_key'))
    runner.check(identity.locator('.resource-details-trigger').count() == 0, 'Channel identity keeps name/ID and Key/status without a third details row')
    lines = identity.locator('.channel-identity-copy').evaluate("""el=>Array.from(el.children).map(n=>{
      const r=n.getBoundingClientRect();return {top:r.top,bottom:r.bottom,height:r.height,scrollHeight:n.scrollHeight,clientHeight:n.clientHeight};})""")
    runner.check(len(lines) == 2 and lines[0]['bottom'] <= lines[1]['top'] + 1
                 and all(line['scrollHeight'] <= line['clientHeight'] + 1 for line in lines),
                 'Channel name/ID and Key/status occupy two nonoverlapping full-label lines')
    details = row.locator('.row-actions .resource-details-trigger')
    shared.reachable(runner, details, 'Channel right-side details action')
    details.click()
    expect(page.get_by_role('dialog')).to_be_visible()
    page.get_by_role('dialog').get_by_role('button', name='关闭弹窗', exact=True).click()
    link = row.locator('a.channel-diagnosis-link')
    expect(link).to_have_attribute('aria-label', '渠道诊断 ' + data.channels[0]['name'])
    link.click()
    query_contract(runner, page, {**context, 'diagnose_channel_id': 1000}, '/channels')
    modal = page.get_by_role('dialog')
    runner.record(phase='channel-diagnosis-selectors', diagnostic_selectors=modal.locator('select').evaluate_all(
        '(nodes)=>nodes.map(n=>({value:n.value,ariaLabel:n.getAttribute("aria-label"),label:n.closest("label")?.textContent,options:Array.from(n.options).map(o=>({value:o.value,selected:o.selected}))}))'))
    expect(modal).to_contain_text('渠道诊断')
    expect(modal.get_by_role('textbox', name='渠道诊断请求模型', exact=True)).to_have_value(SPECIAL_MODEL)
    expect(modal.get_by_label('客户端协议', exact=True)).to_have_value('codex')
    expect(modal.get_by_label('访问令牌', exact=True)).to_have_value(str(token_id))
    expect(modal.locator('.route-diagnostic-channel').first).to_be_visible()
    runner.photo(page, 'channel-diagnosis')
    modal.locator('.channel-route-workspace-link').click()
    query_contract(runner, page, {**context, 'target_channel_id': 1000}, '/routing')
    expect(page.locator('.model-route-target-context')).to_contain_text(re.compile('1000|' + data.channels[0]['name']))
    card_link = page.locator('.model-route-channel[data-channel-id="1000"] footer .model-route-channel-diagnosis-link')
    expect(card_link).to_be_visible()
    card_link.click()
    query_contract(runner, page, {**context, 'diagnose_channel_id': 1000}, '/channels')
    expect(page.get_by_role('dialog').get_by_role('textbox', name='渠道诊断请求模型', exact=True)).to_have_value(SPECIAL_MODEL)
    page.get_by_role('dialog').locator('.channel-route-workspace-link').click()
    page.locator('.model-route-target-context .model-route-channel-diagnosis-link').click()
    query_contract(runner, page, {**context, 'diagnose_channel_id': 1000}, '/channels')
    runner.photo(page, 'roundtrip')
    runner.record(phase='channel-diagnosis-disabled-target')
    # Off-page known disabled channel remains the requested target, with its truthful blocker.
    navigate(page, '/channels?' + urlencode({**context, 'diagnose_channel_id': 1120}))
    expect(page.get_by_role('dialog')).to_contain_text('1120')
    expect(page.get_by_role('dialog')).to_contain_text(re.compile('停用|禁用|阻断|未排序'))
    runner.photo(page, 'disabled-target')
    runner.record(phase='channel-diagnosis-missing-target')
    # Match the backend's precise not-found failure, then verify recovery.
    navigate(page, '/channels?' + urlencode({**context, 'diagnose_channel_id': 999999}))
    expect(page.get_by_role('dialog')).to_contain_text(re.compile('不存在|未找到'))
    expect(page.get_by_role('dialog').get_by_role('button', name=re.compile('重试'))).to_be_enabled()
    expect(page.get_by_role('dialog').get_by_role('button', name='关闭弹窗', exact=True)).to_be_enabled()
    before = sum('route-diagnostics' in call['path'] or call['path'] == '/admin/routing/diagnostics' for call in data.calls)
    runner.record(phase='channel-diagnosis-invalid-context')
    navigate(page, '/channels?diagnose_channel_id=not-a-number&model=%E0%A4%A&client_protocol=bogus&token_id=bad')
    recovery = page.get_by_role('dialog').locator('.channel-route-recovery[role="alert"]')
    expect(recovery).to_be_visible()
    expect(recovery).to_contain_text(re.compile('无效|参数|无法'))
    expect(recovery.get_by_role('link', name='返回渠道列表重新选择', exact=True)).to_be_visible()
    after = sum('route-diagnostics' in call['path'] or call['path'] == '/admin/routing/diagnostics' for call in data.calls)
    runner.check(before == after, 'Invalid raw link parameters do not start diagnostics or choose another channel')
    runner.record(phase='channel-diagnosis-empty-context')
    navigate(page, '/channels?' + urlencode({'diagnose_channel_id': 1000, 'model': '', 'client_protocol': 'codex', 'token_id': token_id}))
    expect(page.get_by_role('dialog')).to_contain_text('请输入请求模型')
    runner.check(after == sum('route-diagnostics' in call['path'] or call['path'] == '/admin/routing/diagnostics' for call in data.calls),
                 'An empty model stays empty without automatic diagnosis or fallback')
    runner.record(phase='channel-diagnosis-mobile-actions')
    viewport(runner, page, 390, 844, 'Channel identity mobile')
    navigate(page, '/channels?' + urlencode(context), '渠道分发')
    expect(page.get_by_role('dialog')).to_have_count(0)
    shared.reachable(runner, row.locator('.row-actions .resource-details-trigger'), 'Mobile full-label channel details action')
    identity_geometry = identity.locator('.channel-identity-copy').evaluate("""el=>Array.from(el.children).map(child=>{
      const range=document.createRange();range.selectNodeContents(child);
      const rects=Array.from(range.getClientRects()).filter(r=>r.width>0&&r.height>0);
      return {text:child.textContent,tops:rects.map(r=>r.top),bottoms:rects.map(r=>r.bottom)};
    })""")
    runner.check(len(identity_geometry) == 2 and all(entry['tops'] and max(entry['tops']) - min(entry['tops']) <= 10
                                                   for entry in identity_geometry),
                 'Mobile channel identity preserves two text lines without wrapping an action into identity')
    runner.photo(page, 'mobile-channel-actions')
    for call in data.calls:
        if call['path'] == '/admin/routing/diagnostics':
            runner.check(not {'channel_id', 'target_channel_id', 'diagnose_channel_id'} & call.get('query', {}).keys(),
                         'Frontend target context never becomes an unsupported backend model filter')
            runner.check(call['query'].get('model') == [SPECIAL_MODEL] and call['query'].get('client_protocol') == ['codex']
                         and call['query'].get('token_id') == [str(token_id)], 'Model diagnosis preserves exact original context in its GET')
    runner.record(route_roundtrip={'model': SPECIAL_MODEL, 'protocol': 'codex', 'token_id': token_id,
                                  'targets': [1000, 1120, 999999], 'probe_calls': 0})


def appearance_state(page):
    return page.evaluate("""()=>{const r=document.documentElement,s=getComputedStyle(r);
      return {preset:r.dataset.themePreset,font:r.dataset.themeFont,theme:r.dataset.theme,
        stored:JSON.parse(localStorage.getItem('pivotflow_appearance')||'{}'),
        fontFamily:getComputedStyle(document.body).fontFamily,
        colors:Object.fromEntries(['--bg','--green','--blue','--surface','--surface-muted','--surface-strong','--text','--text-secondary','--text-muted','--border','--sidebar','--sidebar-text']
          .map(key=>[key,s.getPropertyValue(key).trim()])),boot:window.__stage2Boot};}""")


def primary_contrast(runner, page, data):
    samples = {}
    for preset, label in NEW_PRESETS.items():
        navigate(page, '/system?section=runtime&group=appearance', '系统设置')
        panel = page.locator('.appearance-panel')
        panel.locator('.appearance-option-list button').filter(has=page.get_by_text(label, exact=True)).click()
        panel.get_by_role('button', name='暗色', exact=True).click()
        expect(page.locator('html')).to_have_attribute('data-theme', 'dark')
        navigate(page, '/routing', '模型路由')
        button = page.get_by_role('button', name='查看路由', exact=True)
        expect(button).to_be_visible()
        expect(button).to_be_enabled()
        runner.record(phase=f'primary-contrast-{preset}')
        values = button.evaluate(r"""el=>{
          const s=getComputedStyle(el),root=getComputedStyle(document.documentElement);
          const canvas=document.createElement('canvas');canvas.width=canvas.height=1;
          const ctx=canvas.getContext('2d',{willReadFrequently:true});
          const rgba=value=>{ctx.clearRect(0,0,1,1);ctx.fillStyle=value;ctx.fillRect(0,0,1,1);return Array.from(ctx.getImageData(0,0,1,1).data);};
          const luminance=rgb=>rgb.slice(0,3).map(v=>{v/=255;return v<=.04045?v/12.92:((v+.055)/1.055)**2.4;})
            .reduce((sum,v,i)=>sum+v*[.2126,.7152,.0722][i],0);
          const colors=s.backgroundImage.match(/(?:color\([^)]*\)|rgba?\([^)]*\)|#[0-9a-fA-F]{3,8})/g)||[];
          const foreground=rgba(s.color),stops=colors.map(rgba),ratios=[];
          for(let index=0;index<stops.length-1;index++)for(let step=0;step<=20;step++){
            const t=step/20,rgb=stops[index].map((value,i)=>value*(1-t)+stops[index+1][i]*t);
            const a=luminance(foreground),b=luminance(rgb);ratios.push((Math.max(a,b)+.05)/(Math.min(a,b)+.05));}
          return {text:el.textContent,color:s.color,background:s.backgroundImage,opacity:s.opacity,filter:s.filter,
            foreground,stops,minimum:ratios.length?Math.min(...ratios):null,
            preset:document.documentElement.dataset.themePreset,primaryForeground:root.getPropertyValue('--primary-foreground').trim()};
        }""")
        samples[preset] = values
        runner.record(primary_contrast=samples)
        runner.photo(page, f'primary-dark-{preset}')
        runner.check(values['preset'] == preset and len(values['stops']) >= 2
                     and values['foreground'][3] == 255 and all(stop[3] == 255 for stop in values['stops']),
                     'Contrast uses the real enabled primary button foreground and computed opaque gradient stops')
        runner.check(values['minimum'] is not None and values['minimum'] >= 4.5,
                     f'{preset} dark primary label contrast is at least 4.5 across the rendered gradient; actual {values["minimum"]}')


def appearance_case(runner, page, data):
    panel = page.locator('.appearance-panel')
    expect(panel).to_be_visible()
    options = panel.locator('.appearance-option-list button')
    runner.check(options.count() == 12, 'Eight existing and four new presets remain selectable')
    for label in OLD_PRESETS + list(NEW_PRESETS.values()):
        expect(options.filter(has=page.get_by_text(label, exact=True))).to_have_count(1)
    fonts = page.get_by_role('combobox', name='界面字体')
    runner.check(set(fonts.locator('option').evaluate_all('(nodes)=>nodes.map(n=>n.value)')) == set(OLD_FONTS + NEW_FONTS),
                 'All twelve existing and three new local fonts are exposed')
    samples = {}
    for index, (preset, label) in enumerate(NEW_PRESETS.items()):
        options.filter(has=page.get_by_text(label, exact=True)).click()
        font = NEW_FONTS[index % 3]
        fonts.select_option(font)
        for mode, name in (('light', '亮色'), ('dark', '暗色')):
            panel.get_by_role('button', name=name, exact=True).click()
            expect(page.locator('html')).to_have_attribute('data-theme-preset', preset)
            expect(page.locator('html')).to_have_attribute('data-theme-font', font)
            expect(page.locator('html')).to_have_attribute('data-theme', mode)
            state = appearance_state(page)
            runner.check(state['stored'].get('preset') == preset and state['stored'].get('font') == font
                         and state['stored'].get('preference') == mode, 'Preset/font/mode persist together in browser storage')
            runner.check(all(state['colors'].values()) and len(state['fontFamily'].split(',')) >= 2,
                         'Every new appearance provides complete color variables and explicit local font fallbacks')
            samples[f'{preset}-{mode}'] = state
            viewport(runner, page, 1280, 720, f'{preset}-{font}-{mode}')
            nav_geometry(runner, page, f'{preset}-{font}-{mode}-nav', True)
        runner.check(samples[f'{preset}-light']['colors'] != samples[f'{preset}-dark']['colors'],
                     'Light and dark resolve separate complete palettes for each new preset')
    verify_boot(runner, page, data, 'ink', 'simhei')
    panel.get_by_role('button', name='恢复默认', exact=True).click()
    expect(page.locator('html')).to_have_attribute('data-theme-preset', 'jade')
    expect(page.locator('html')).to_have_attribute('data-theme-font', 'system')
    runner.record(appearance_samples=samples, reload_fonts=['simhei'], remote_font_requests=0)


def verify_boot(runner, page, data, preset, font):
    expected = appearance_state(page)
    runner.record(phase=f'boot-before-entry-{preset}-{font}')
    data.hold_entry = True
    runner.capture_pre_entry = True
    try:
        inspect_visual_boot(runner, page, data, preset, font, expected)
    finally:
        data.release_entry()
        runner.capture_pre_entry = False
    runner.record(phase=f'boot-after-entry-{preset}-{font}')
    expect(page.locator('.appearance-panel')).to_be_visible()
    state = appearance_state(page)
    runner.check((state['preset'], state['font'], state['theme']) == (preset, font, expected['theme']),
                 'Refresh retains the selected new preset, local font and mode')
    meaningful = [entry for entry in state['boot'] if entry.get('preset') and entry.get('font')]
    runner.check(bool(meaningful) and all(entry['preset'] == preset and entry['font'] == font for entry in meaningful),
                 'Inline boot and runtime retain saved appearance without a default preset/font frame')
    runner.photo(page, f'boot-{preset}-{font}')
    runner.record(boot=state, entry_lifecycle={'held': data.entry_holds, 'released': data.entry_releases,
                                           'pending': data.held_entry is not None})


def inspect_visual_boot(runner, page, data, preset, font, expected):
    page.reload(wait_until='commit')
    deadline = time.monotonic() + 4
    while data.held_entry is None and time.monotonic() < deadline:
        page.wait_for_timeout(10)
    runner.check(data.held_entry is not None, 'Reload really holds the built entry module before runtime theme/React')
    boot = page.locator('.console-boot')
    expect(boot).to_be_visible()
    page.wait_for_function("""()=>{const links=Array.from(document.querySelectorAll('link[rel=stylesheet]'));
      const root=getComputedStyle(document.documentElement);
      return links.length>0 && links.every(link=>link.sheet) && root.getPropertyValue('--bg').trim()
        && root.getPropertyValue('--font-ui').trim();}""", timeout=3000)
    page.evaluate('()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))')
    runner.check(page.locator('h1').count() == 0, 'Visual boot evidence is captured before React renders a page')
    visual = boot.evaluate("""(el,expected)=>{
      const s=getComputedStyle(el),spinner=getComputedStyle(el.querySelector('i')),root=getComputedStyle(document.documentElement);
      const normalize=value=>{const probe=document.createElement('span');probe.style.color=value;el.append(probe);
        const color=getComputedStyle(probe).color;probe.remove();return color;};
      return {background:s.backgroundColor,color:s.color,font:s.fontFamily,border:spinner.borderRightColor,accent:spinner.borderTopColor,
        expected:{background:normalize(expected.colors['--bg']),color:normalize(expected.colors['--text-secondary']),font:expected.fontFamily,
          border:normalize(root.getPropertyValue('--border-strong')),accent:normalize(root.getPropertyValue('--green'))},
        root:{bg:root.getPropertyValue('--bg').trim(),font:root.getPropertyValue('--font-ui').trim()},
        preset:document.documentElement.dataset.themePreset,fontId:document.documentElement.dataset.themeFont};
    }""", expected)
    runner.record(boot_visual=visual)
    runner.photo(page, f'boot-before-entry-{preset}-{font}')
    runner.check((visual['preset'], visual['fontId']) == (preset, font), 'Inline boot alone reads persisted preset and font')
    for key in ('background', 'color', 'font', 'border', 'accent'):
        runner.check(visual[key] == visual['expected'][key],
                     f'Pre-entry boot {key} matches saved palette/font computed styles, without hardcoded default flash')


def boot_case(runner, page, data, preset, font):
    panel = page.locator('.appearance-panel')
    panel.locator('.appearance-option-list button').filter(has=page.get_by_text(NEW_PRESETS[preset], exact=True)).click()
    page.get_by_role('combobox', name='界面字体').select_option(font)
    panel.get_by_role('button', name='暗色', exact=True).click()
    expect(page.locator('html')).to_have_attribute('data-theme-font', font)
    verify_boot(runner, page, data, preset, font)


def actual_balance_contract(page, snapshot, runner=None):
    """Main's real-preview suite can use this read-only oracle on its API facts.

    This helper performs no requests and does not start an application/browser.
    It replaces the old actual-preview two-SVG/circle assertions without
    introducing synthetic data into the real-preview response.
    """
    from playwright.sync_api import expect as actual_expect
    shared.expect = actual_expect
    assertions = []
    if runner is None:
        class ActualChecks:
            def check(self, condition, message):
                assertions.append({'pass': bool(condition), 'check': message})
                if not condition:
                    raise AssertionError(message)
        runner = ActualChecks()
    points = snapshot.get('balance_history', [])
    if not points:
        raise AssertionError('Actual demo snapshot has no daily facts to verify')
    metrics = shared.shared_chart_geometry(runner, page, points)
    for day in metrics['calendar']:
        shared.shared_readout(runner, page, points, day)
    metrics['assertions'] = assertions
    return metrics


def case_specs():
    specs = {
        'stage2-shared-plot': (chart_case, '/stats', 120, True),
        'stage2-navigation-copy': (nav_copy, '/connect', 200, False),
        'stage2-settings-natural': (settings_natural, '/system?section=runtime&group=health', 120, False),
        'stage2-routing-roundtrip': (routing_roundtrip, '/channels?' + urlencode(
            {'model': SPECIAL_MODEL, 'client_protocol': 'codex', 'token_id': 1}), 120, False),
        'stage2-appearance-persistence': (appearance_case, '/system?section=runtime&group=appearance', 200, False),
        'stage2-chart-edges': (chart_edges, '/stats', 120, False),
        'stage2-primary-contrast': (primary_contrast, '/system?section=runtime&group=appearance', 120, False),
    }
    for preset, font in (('sand', 'simhei'), ('ice', 'fangsong'), ('rose', 'aptos')):
        specs[f'stage2-boot-{preset}-{font}'] = (partial(boot_case, preset=preset, font=font),
            '/system?section=runtime&group=appearance', 120, False)
    return specs


def runtime_types():
    global expect
    DailyRunner, StaticHandler, sync_playwright = shared.runtime_types()
    expect = shared.expect
    from console_visual_regression import setting
    from model_routes_smoke import build_diagnostic, diagnostic_channel

    class Stage2Data(DailyRunner.fixture_type):
        def __init__(self, case):
            super().__init__(case)
            self.expected_errors = []
            self.hold_entry = False; self.held_entry = None; self.entry_holds = 0; self.entry_releases = 0
            if case == 'stage2-shared-plot':
                self.balance_history = [dict(p, daily_delta=p['daily_delta'] * 100 if p['daily_delta'] is not None else None,
                                            balance=p['balance'] * 100 if p['balance'] is not None else None)
                    if p['currency'] == 'CNY' else p for p in self.balance_history
                    if not (p['currency'] == 'CNY' and p['day'] == '2026-11-14')]
            health = {'channel_test_content': ('hi', 'string'), 'channel_check_interval_hours': ('0', 'float'),
                      'enable_health_score': ('true', 'bool'), 'success_rate_penalty_weight': ('10', 'float'),
                      'health_score_window_minutes': ('30', 'int'), 'health_score_update_interval': ('30', 'int'),
                      'health_min_confident_sample': ('20', 'int'), 'enable_ttfb_score': ('true', 'bool'),
                      'ttfb_penalty_weight': ('1', 'float'), 'ttfb_max_slow_ratio': ('4', 'float'),
                      'ttfb_min_confident_sample': ('10', 'int')}
            self.settings = [s for s in self.settings if s['key'] not in health]
            self.settings.extend(setting(key, value, kind) for key, (value, kind) in health.items())
            if case == 'stage2-routing-roundtrip':
                self.tokens[0]['id'] = 1
                for channel in self.channels:
                    channel.update(enabled=True, auth_type='api_key', key_count=2,
                                   models=[{'model': SPECIAL_MODEL, 'disabled': False, 'redirect_model': ''}])
                self.channels[-1]['enabled'] = False

        def route(self, route):
            parsed = urlsplit(route.request.url)
            path, query = parsed.path, parse_qs(parsed.query, keep_blank_values=True)
            matched = re.fullmatch(r'/admin/channels/(\d+)/route-diagnostics', path)
            if matched or path == '/admin/routing/diagnostics':
                self.calls.append({'path': path, 'method': route.request.method, 'query': query})
                if route.request.method != 'GET' or set(query) - {'model', 'client_protocol', 'token_id', 'offset', 'limit'}:
                    self.unexpected.append({'case': self.case, 'unexpected_diagnostic_request': path, 'query': query})
                    route.abort('blockedbyclient'); return
                model, protocol, token = query.get('model', [''])[0], query.get('client_protocol', [''])[0], query.get('token_id', [''])[0]
                if matched:
                    channel_id = int(matched[1])
                    target = diagnostic_channel(channel_id)
                    target.update(channel_name=next((c['name'] for c in self.channels if c['id'] == channel_id), f'渠道 #{channel_id}'),
                                  candidate=True, attemptable=True, attempt_status='ready')
                    warnings = []
                    if channel_id == 1120:
                        target.update(enabled=False, candidate=False, attemptable=False, attempt_status='blocked',
                                      reasons=[{'stage': 'candidate', 'code': 'channel_disabled', 'message': '渠道已停用，不参与路由', 'blocking': True}])
                    if channel_id == 999999:
                        self.expected_errors.append({'path': path, 'status': 404, 'error': 'routing_channel_not_found', 'synthetic': True})
                        route.fulfill(status=404, content_type='application/json',
                                      body='{"success":false,"error":"routing_channel_not_found"}')
                        return
                    self.fulfill(route, {'version': 1, 'model': model, 'client_protocol': protocol,
                        'route_strategy': 'weighted', 'pool_mode': 'exact', 'health_score_enabled': True,
                        'target': target, 'candidates': [target] if target and target['candidate'] else [],
                        'actual_total_requests': 12, 'sticky': None, 'warnings': warnings,
                        'summary': ['渠道不存在，返回渠道列表恢复。'] if target is None else ['只读渠道诊断合成观察。']})
                else:
                    payload = build_diagnostic(model, protocol, token, 0, 50, channel_count=1)
                    target = diagnostic_channel(1000); target.update(channel_name=self.channels[0]['name'])
                    payload['channels'] = [target]; payload['total'] = 1
                    self.fulfill(route, payload)
            else:
                super().route(route)

        def expected_console_error(self, message):
            # Only Chromium's precise resource-status message for the intentional
            # synthetic 404 is expected. App errors, other paths/statuses, and
            # every pageerror still fail the runner and remain fully visible.
            path = urlsplit(message.location.get('url', '')).path
            expected = (self.case == 'stage2-routing-roundtrip'
                        and path == '/admin/channels/999999/route-diagnostics'
                        and message.text == 'Failed to load resource: the server responded with a status of 404 (Not Found)')
            if expected:
                self.expected_errors.append({'path': path, 'console': message.text, 'synthetic': True})
            return expected
        def intercept_static(self, route):
            path = urlsplit(route.request.url).path
            if self.hold_entry and re.fullmatch(r'/web/console/assets/index-[^/]+\.js', path):
                if self.held_entry is not None:
                    raise AssertionError('Only one entry module may be held during visual boot')
                self.held_entry = route; self.entry_holds += 1
                return True
            return False

        def release_entry(self):
            self.hold_entry = False
            if self.held_entry is not None:
                route, self.held_entry = self.held_entry, None
                route.continue_(); self.entry_releases += 1

        def finish_for_close(self):
            self.hold_entry = False
            if self.held_entry is not None:
                route, self.held_entry = self.held_entry, None
                route.abort('aborted'); self.entry_releases += 1

        def failure_diagnostics(self, page):
            return page.evaluate("""()=>({
              href:location.hash,active:{tag:document.activeElement?.tagName,day:document.activeElement?.dataset?.day},
              chart:{crosshair:document.querySelector('.balance-history-crosshair')?.dataset.day,
                readouts:Array.from(document.querySelectorAll('.balance-history-readout-value')).map(n=>({currency:n.dataset.currency,day:n.dataset.day,value:n.dataset.value,status:n.dataset.status,text:n.textContent})),
                touchEvents:window.__stage2TouchEvents||[]},
              modal:{selects:Array.from(document.querySelectorAll('[role=dialog] select')).map(n=>({value:n.value,ariaLabel:n.getAttribute('aria-label'),label:n.closest('label')?.textContent})),
                model:document.querySelector('[aria-label="渠道诊断请求模型"]')?.value},
              boot:{visible:!!document.querySelector('.console-boot'),dataset:document.documentElement.dataset}
            })""")
    return DailyRunner, Stage2Data, StaticHandler, sync_playwright


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path(__file__).resolve().parents[1])
    parser.add_argument('--output', type=Path, help='External stage2 evidence directory; default archive/evidence/<group>')
    parser.add_argument('--group', choices=GROUPS, default='core')
    parser.add_argument('--case', action='append', default=[], help='Repeat to select focused cases; global limit still 360')
    parser.add_argument('--max-seconds', type=int, default=240)
    parser.add_argument('--list-cases', action='store_true')
    args = parser.parse_args()
    specs = case_specs()
    if args.list_cases:
        print('\n'.join(specs)); return
    if set(args.case) - specs.keys() or not 1 <= args.max_seconds <= 900:
        parser.error('Unknown case or --max-seconds outside 1..900')
    names = list(dict.fromkeys(args.case or GROUPS[args.group]))
    root = args.root.resolve()
    output = (args.output or ARCHIVE / 'evidence' / args.group).resolve()
    if output == root or root in output.parents:
        parser.error('Stage2 evidence must be external to the repository worktree')
    output.mkdir(parents=True, exist_ok=True)
    result = {'pass': False, 'checks': [], 'evidence': [], 'metrics': {}, 'assertions': [],
              'unexpected_events': [], 'server_closed': False, 'fatal_error': None,
              'limits': {'suite_requests': 360, 'case_requests': {name: specs[name][2] for name in names}}}
    server = thread = runner = None
    try:
        if not (root / 'web/console/index.html').is_file():
            raise FileNotFoundError('Main-built console assets are required')
        Runner, Data, StaticHandler, sync_playwright = runtime_types()
        for _ in range(8):
            server = ThreadingHTTPServer(('127.0.0.1', 0), partial(StaticHandler, root=root))
            if server.server_port != 10080:
                break
            server.server_close(); server = None
        if server is None:
            raise RuntimeError('No browser-safe owned static port')
        server.daemon_threads = True
        thread = Thread(target=server.serve_forever, daemon=True); thread.start()
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True, args=['--no-proxy-server', '--disable-background-networking'])
            try:
                runner = Runner(browser, f'http://127.0.0.1:{server.server_port}', output, names, args.max_seconds,
                                data_factory=Data, suite_limit=MAX_SUITE_REQUESTS, init_script=BOOT_OBSERVER)
                for name in names:
                    callback, path, budget, touch = specs[name]
                    runner.case_limit = budget
                    runner.case(name, callback, path, 1440, 900, touch=touch)
            finally:
                browser.close()
    except Exception as error:
        result['fatal_error'] = f'{type(error).__name__}: {error}'[:2200]
    finally:
        if runner is not None:
            result.update(checks=runner.results, metrics=runner.metrics, assertions=runner.assertions,
                          unexpected_events=runner.events, total_requests=runner.request_count)
            result['evidence'] = sorted({image for metrics in runner.metrics.values() for image in metrics.get('evidence', [])})
        if server is not None:
            if thread is not None and thread.is_alive():
                server.shutdown()
            server.server_close()
        if thread is not None:
            thread.join(timeout=5)
        result['server_closed'] = thread is None or not thread.is_alive()
        result['pass'] = len(result['checks']) == len(names) and all(c['pass'] for c in result['checks']) and not result['unexpected_events'] and result['fatal_error'] is None and result['server_closed']
        (output / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2, allow_nan=False), encoding='utf-8')
    print(json.dumps({key: result[key] for key in ('pass', 'server_closed', 'fatal_error')}
                     | {'checks': len(result['checks']), 'unexpected_events': len(result['unexpected_events'])}))
    raise SystemExit(0 if result['pass'] else 1)


if __name__ == '__main__':
    main()
