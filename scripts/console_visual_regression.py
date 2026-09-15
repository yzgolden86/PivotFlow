"""Exercise built console assets with isolated, synthetic API fixtures.

The live login smoke test remains in console_ui_smoke.py.
"""

import argparse
import copy
import json
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import urlparse

from playwright.sync_api import expect, sync_playwright

import docs_screenshots as fixtures


ROOT = Path(__file__).resolve().parents[1]
PAGES = {"dashboard": "/", "sites": "/sites", "accounts": "/accounts", "channels": "/channels", "logs": "/logs", "settings": "/system"}
PAGE_TITLES = {"dashboard": "系统概览", "sites": "站点管理", "accounts": "账号管理", "channels": "渠道分发", "logs": "请求日志", "settings": "系统设置"}


def setting(key, value, kind="int", default=None):
    return {"key": key, "value": value, "value_type": kind, "description": key,
            "default_value": value if default is None else default, "editable": True, "requires_restart": False}


SETTINGS = [
    setting("max_key_retries", "3", default="2"),
    setting("route_strategy", "balanced", "string"),
    setting("model_alias_groups", "[]", "json"),
    setting("upstream_first_byte_timeout", "30"),
    setting("anthropic_first_byte_timeout", "25"),
    setting("non_stream_timeout", "120", default="60"),
    setting("log_retention_days", "30"),
    setting("auto_refresh_interval_seconds", "0"),
    setting("site_daily_checkin_time", "08:00", "string"),
    setting("site_daily_announcement_time", "09:00", "string"),
    setting("cooldown_fallback_enabled", "true", "bool"),
]

LOGS = [{
    "id": index, "time": 1788577200 - index * 60, "log_source": "proxy",
    "channel_id": 101, "channel_name": "星河节点 / 主账号 / 长名称渠道",
    "status_code": 200 if index != 3 else 429, "message": "" if index != 3 else "upstream rate limit",
    "model": "glm-5.3" if index % 2 else "claude-sonnet-4-6",
    "actual_model": "z-ai/glm-5.3" if index % 2 else "claude-sonnet-4-6",
    "client_protocol": "anthropic", "upstream_protocol": "openai", "duration": 2.1,
    "first_byte_time": 0.4, "input_tokens": 1250, "output_tokens": 200, "cost": 0.0034,
    "cost_multiplier": 1, "cost_source": "site_pricing", "cost_status": "actual",
} for index in range(1, 7)]


class ConsoleFixtures:
    def __init__(self):
        self.settings = copy.deepcopy(SETTINGS)
        self.updates = []
        self.settings_error = False
        self.batch_error = False
        self.hold_batch = False
        self.pending_batch = None

    def route(self, route):
        path = urlparse(route.request.url).path
        if path == "/admin/settings" and self.settings_error:
            route.fulfill(status=500, content_type="application/json", body=json.dumps({"success": False, "error": "Synthetic connection failure"}))
            return
        if path == "/admin/settings/batch" and self.batch_error:
            route.fulfill(status=500, content_type="application/json", body=json.dumps({"success": False, "error": "Synthetic save failure"}))
            return
        if path == "/admin/settings":
            payload = fixtures.envelope(self.settings)
        elif path == "/admin/settings/batch":
            if self.hold_batch:
                self.pending_batch = route
                return
            updates = route.request.post_data_json
            self.updates.append(updates)
            for item in self.settings:
                if item["key"] in updates:
                    item["value"] = updates[item["key"]]
            payload = fixtures.envelope({"message": "设置已保存"})
        elif path.startswith("/admin/settings/") and path.endswith("/reset"):
            key = path.split("/")[-2]
            for item in self.settings:
                if item["key"] == key:
                    item["value"] = item["default_value"]
            payload = fixtures.envelope({"message": "已恢复默认值"})
        elif path == "/admin/model-alias-inventory":
            payload = fixtures.envelope({"candidates": [], "suggestions": [], "total_models": 0})
        elif path == "/admin/logs":
            payload = fixtures.envelope(LOGS, len(LOGS))
        elif path == "/admin/logs/bootstrap":
            payload = fixtures.envelope({"channel_test_content": "test", "auth_tokens": [], "models": ["glm-5.3", "claude-sonnet-4-6"], "channels": [{"id": 101, "name": "星河节点"}], "status_codes": [200, 429]})
        else:
            fixtures.mock_admin(route)
            return
        route.fulfill(status=200, content_type="application/json", body=json.dumps(payload, ensure_ascii=False))


class QuietHandler(SimpleHTTPRequestHandler):
    def log_message(self, format, *args):
        pass


def prepare(browser, base, width, *, dark=False, mobile=False):
    context = browser.new_context(viewport={"width": width, "height": 900 if not mobile else 844},
                                  has_touch=mobile, is_mobile=mobile, device_scale_factor=1,
                                  color_scheme="dark" if dark else "light")
    context.add_init_script("localStorage.setItem('pivotflow_token','ui-test-only');localStorage.setItem('pivotflow_web_role','admin');")
    data = ConsoleFixtures()
    context.route("**/admin/**", data.route)
    context.route("**/dashboard/**", data.route)
    return context, data


def capture(page, out, name):
    page.screenshot(path=str(out / f"{name}.png"), full_page=True, animations="disabled")
    return page.evaluate("""() => ({width: innerWidth, scrollWidth: document.documentElement.scrollWidth,
      viewport: document.documentElement.clientWidth, title: document.querySelector('h1')?.textContent})""")


def check_dashboard_layout(page):
    expect(page.get_by_role("navigation", name="概览快捷操作")).to_have_count(0)
    icons = page.locator(".dashboard-grid .panel-title-icon")
    expect(icons).to_have_count(3)
    # Measure each pair in one frame, including during sidebar resize motion.
    positions = icons.evaluate_all("""elements => elements.map(element => {
      const box = element.getBoundingClientRect();
      const symbol = element.querySelector('svg').getBoundingClientRect();
      return {title: element.parentElement.textContent, width: box.width, height: box.height,
        dx: symbol.x + symbol.width / 2 - box.x - box.width / 2,
        dy: symbol.y + symbol.height / 2 - box.y - box.height / 2};
    })""")
    for position in positions:
        assert abs(position["width"] - 28) < 0.5 and abs(position["height"] - 28) < 0.5, position
        assert abs(position["dx"]) < 0.5 and abs(position["dy"]) < 0.5, position


def check_tooltips(page, mobile):
    tip = page.locator(".setting-row .help-tip-trigger").first
    tip.wait_for(state="visible")
    if mobile:
        tip.tap()
    else:
        tip.hover()
        expect(page.get_by_role("tooltip")).to_be_visible()
        tip.click()
    expect(page.get_by_role("tooltip")).to_be_visible()
    box = page.get_by_role("tooltip").bounding_box()
    width = page.evaluate("document.documentElement.clientWidth")
    assert box["x"] >= 0 and box["x"] + box["width"] <= width
    if mobile:
        tip.tap()
    else:
        tip.click()
    expect(page.get_by_role("tooltip")).to_have_count(0)
    tip.click()
    page.get_by_role("heading", name="系统设置", exact=True).click()
    expect(page.get_by_role("tooltip")).to_have_count(0)
    page.keyboard.press("Tab")
    tip.focus()
    expect(page.get_by_role("tooltip")).to_be_visible()
    page.keyboard.press("Escape")
    expect(page.get_by_role("tooltip")).to_have_count(0)
    tip.click()
    page.keyboard.press("Escape")
    expect(page.get_by_role("tooltip")).to_have_count(0)


def check_interactions(browser, base, out, report):
    """Exercise stateful workflows that screenshots alone cannot certify."""
    context, data = prepare(browser, base, 1440)
    page = context.new_page()
    page.on("pageerror", lambda error: report["errors"].append(str(error)))
    try:
        page.goto(f"{base}/web/console/#/system", wait_until="networkidle")
        expect(page.get_by_role("heading", name="系统设置", exact=True)).to_be_visible()

        # A reset must remove only its own dirty marker, preserving other edits.
        page.get_by_role("radio", name="粘性轮询", exact=True).click()
        page.get_by_label("单渠道密钥重试次数", exact=True).fill("5")
        expect(page.get_by_role("button", name="保存更改", exact=True)).to_be_visible()
        expect(page.locator(".setting-row--dirty")).to_have_count(2)
        page.once("dialog", lambda dialog: dialog.accept())
        page.get_by_role("button", name="重置 单渠道密钥重试次数", exact=True).click()
        expect(page.locator(".setting-row--dirty")).to_have_count(1)
        expect(page.get_by_role("radio", name="粘性轮询", exact=True)).to_have_attribute("aria-checked", "true")
        page.locator(".settings-modified-filter input").check()
        expect(page.locator(".setting-row")).to_have_count(1)
        expect(page.get_by_role("radio", name="粘性轮询", exact=True)).to_be_visible()
        page.locator(".settings-modified-filter input").uncheck()

        data.batch_error = True
        page.once("dialog", lambda dialog: dialog.accept())
        page.get_by_role("button", name="保存更改", exact=True).click()
        expect(page.get_by_text("Synthetic save failure", exact=True)).to_be_visible()
        expect(page.locator(".setting-row--dirty")).to_have_count(1)
        data.batch_error = False
        data.hold_batch = True
        page.once("dialog", lambda dialog: dialog.accept())
        page.get_by_role("button", name="保存更改", exact=True).click()
        expect(page.get_by_role("button", name="刷新系统设置", exact=True)).to_be_disabled()
        expect(page.get_by_label("单渠道密钥重试次数", exact=True)).to_be_disabled()
        expect(page.get_by_role("button", name="放弃修改", exact=True)).to_be_disabled()
        assert data.pending_batch is not None
        data.hold_batch = False
        data.route(data.pending_batch)
        expect(page.get_by_role("button", name="保存更改", exact=True)).to_have_count(0)
        if not data.updates or data.updates[-1].get("route_strategy") != "sticky":
            raise AssertionError(f"unexpected settings payloads: {data.updates}")

        # Discard restores a new draft without touching the server.
        page.get_by_label("单渠道密钥重试次数", exact=True).fill("7")
        page.once("dialog", lambda dialog: dialog.accept())
        page.get_by_role("button", name="放弃修改", exact=True).click()
        expect(page.locator(".setting-row--dirty")).to_have_count(0)
        expect(page.get_by_label("单渠道密钥重试次数", exact=True)).to_have_value("2")

        # Drafts survive category changes; search and modified-only combine.
        page.get_by_role("button", name="新增映射", exact=True).click()
        page.get_by_label("统一名称", exact=True).press_sequentially("glm-5.3")
        expect(page.get_by_label("统一名称", exact=True)).to_be_focused()
        page.get_by_label("上游名称，每行一个", exact=True).fill("z-ai/glm-5.3")
        page.locator(".settings-groups").get_by_role("button", name="请求超时").click()
        page.get_by_label("非流式请求总时限", exact=True).fill("180")
        page.locator(".settings-modified-filter input").check()
        expect(page.locator(".model-alias-panel")).to_be_visible()
        expect(page.locator(".setting-row")).to_have_count(1)
        page.get_by_label("搜索全部设置", exact=True).fill("非流式")
        expect(page.locator(".model-alias-panel")).to_have_count(0)
        expect(page.get_by_label("非流式请求总时限", exact=True)).to_have_value("180")
        page.get_by_role("button", name="清除设置搜索", exact=True).click()
        expect(page.locator(".model-alias-panel")).to_be_visible()
        page.once("dialog", lambda dialog: dialog.dismiss())
        page.get_by_role("button", name="刷新系统设置", exact=True).click()
        expect(page.get_by_label("非流式请求总时限", exact=True)).to_have_value("180")
        page.once("dialog", lambda dialog: dialog.accept())
        page.get_by_role("button", name="放弃修改", exact=True).click()
        expect(page.get_by_label("非流式请求总时限", exact=True)).to_have_value("120")

        # Refresh errors are rendered in place while preserving the current page.
        data.settings_error = True
        page.get_by_role("button", name="刷新系统设置", exact=True).click()
        expect(page.get_by_text("Synthetic connection failure", exact=True)).to_be_visible()
        data.settings_error = False

        # Theme picker updates the root theme and closes after selection.
        page.get_by_role("button", name="选择界面主题", exact=True).click()
        page.get_by_role("menuitemradio", name="暗色", exact=True).click()
        expect(page.locator("html")).to_have_attribute("data-theme", "dark")
        page.get_by_role("button", name="选择界面主题", exact=True).click()
        page.get_by_role("menuitemradio", name="亮色", exact=True).click()
        expect(page.locator("html")).to_have_attribute("data-theme", "light")
        page.get_by_role("button", name="选择界面主题", exact=True).click()
        page.keyboard.press("Escape")
        expect(page.get_by_role("menu", name="界面主题")).to_have_count(0)
        page.get_by_role("button", name="选择界面主题", exact=True).click()
        page.locator("main h1").click()
        expect(page.get_by_role("menu", name="界面主题")).to_have_count(0)

        # Dashboard deep links carry the selected reporting range into logs.
        page.goto(f"{base}/web/console/#/", wait_until="networkidle")
        page.get_by_role("radio", name="本周", exact=True).click()
        page.get_by_role("link", name="路由成功率，查看详情", exact=True).click()
        page.wait_for_url(f"{base}/web/console/#/logs?range=this_week")
        expect(page.get_by_role("combobox", name="日志时间范围", exact=True)).to_have_value("this_week")
        page.get_by_role("combobox", name="日志模型", exact=True).select_option("claude-sonnet-4-6")
        expect(page.get_by_role("button", name="清除日志筛选", exact=True)).to_be_visible()
        page.get_by_role("button", name="清除日志筛选", exact=True).click()
        expect(page.get_by_role("combobox", name="日志时间范围", exact=True)).to_have_value("today")
        expect(page.get_by_role("combobox", name="日志模型", exact=True)).to_have_value("")
        expect(page.get_by_role("button", name="清除日志筛选", exact=True)).to_have_count(0)

        # Desktop collapse survives a reload; mobile drawer restores focus on close.
        collapse = page.get_by_role("button", name="收起侧栏", exact=True)
        collapse.click()
        expect(page.locator(".app-shell")).to_have_class("app-shell app-shell--collapsed")
        page.reload(wait_until="networkidle")
        expect(page.locator(".app-shell")).to_have_class("app-shell app-shell--collapsed")
        page.set_viewport_size({"width": 390, "height": 844})
        page.get_by_role("button", name="打开导航", exact=True).click()
        expect(page.get_by_role("button", name="关闭导航", exact=True).last).to_be_focused()
        expect(page.locator(".sidebar-theme-button span")).to_have_text("亮色")
        capture(page, out, "drawer-collapsed-preference-mobile")
        page.keyboard.press("Shift+Tab")
        expect(page.get_by_role("button", name="退出登录", exact=True)).to_be_focused()
        page.keyboard.press("Tab")
        expect(page.get_by_role("button", name="关闭导航", exact=True).last).to_be_focused()
        page.keyboard.press("Escape")
        expect(page.get_by_role("button", name="打开导航", exact=True)).to_be_focused()

        # Expanded alias editing must fit too, not only its empty state.
        page.goto(f"{base}/web/console/#/system", wait_until="networkidle")
        page.get_by_role("button", name="新增映射", exact=True).click()
        page.get_by_label("统一名称", exact=True).fill("glm-5.3")
        page.get_by_label("上游名称，每行一个", exact=True).fill("z-ai/glm-5.3")
        record = capture(page, out, "alias-edit-mobile")
        assert record["scrollWidth"] <= record["viewport"]
        page.once("dialog", lambda dialog: dialog.accept())
        page.get_by_role("button", name="放弃修改", exact=True).click()
    finally:
        context.close()


def run(base, out, capture_only):
    report = {"pages": [], "errors": [], "checks": []}
    with sync_playwright() as playwright:
        browser = playwright.chromium.launch(headless=True)
        try:
            for width, dark in [(2560, False), (1440, False), (1024, False), (390, False), (320, False), (1440, True), (390, True)]:
                context, data = prepare(browser, base, width, dark=dark, mobile=width < 600)
                page = context.new_page()
                page.on("pageerror", lambda error: report["errors"].append(str(error)))
                for name, path in PAGES.items():
                    page.goto(f"{base}/web/console#{path if path.startswith('/') else f'/{path}'}", wait_until="networkidle")
                    expect(page.locator("main h1")).to_have_text(PAGE_TITLES[name])
                    record = capture(page, out, f"{name}-{width}-{'dark' if dark else 'light'}")
                    report["pages"].append(record)
                    assert record["scrollWidth"] <= record["viewport"], (name, record)
                    if name == "dashboard" and not capture_only:
                        check_dashboard_layout(page)
                        report["checks"].append(f"dashboard-layout-{width}-{'dark' if dark else 'light'}")
                if not capture_only:
                    check_tooltips(page, width < 600)
                    report["checks"].append(f"tooltips-{width}-{'dark' if dark else 'light'}")
                context.close()
            if not capture_only:
                check_interactions(browser, base, out, report)
                report["checks"].append("stateful-interactions")
            assert not report["errors"], report["errors"]
        finally:
            browser.close()
    (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"pages": len(report["pages"]), "checks": report["checks"], "errors": report["errors"]}, ensure_ascii=True))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", default=".tmp-quality-review/ui")
    parser.add_argument("--capture-only", action="store_true")
    args = parser.parse_args()
    out = (ROOT / args.output).resolve()
    out.mkdir(parents=True, exist_ok=True)
    with ThreadingHTTPServer(("127.0.0.1", 0), partial(QuietHandler, directory=str(ROOT))) as server:
        worker = Thread(target=server.serve_forever, daemon=True)
        worker.start()
        try:
            run(f"http://127.0.0.1:{server.server_port}", out, args.capture_only)
        finally:
            server.shutdown()
            worker.join()


if __name__ == "__main__":
    main()
