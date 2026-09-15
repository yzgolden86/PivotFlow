"""Verify the built OAuth quota panel with isolated synthetic fixtures.

No live backend, credentials, upstream quota API, or production data are used.
Run: py -3 scripts/oauth_usage_ui_smoke.py --out .tmp-oauth-usage-ui
"""

import argparse
import copy
import json
from functools import partial
from http.server import ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import urlparse

from playwright.sync_api import expect, sync_playwright

import docs_screenshots as fixtures
from console_visual_regression import ConsoleFixtures, QuietHandler


ROOT = Path(__file__).resolve().parents[1]

OAUTH_CHANNEL = {
    "id": 201,
    "name": "Codex Plus · 主账号",
    "auth_type": "codex_oauth",
    "protocol_transform_mode": "auto",
    "urls": [{"url": "https://chatgpt.com/backend-api/codex", "protocols": ["codex"]}],
    "priority": 90,
    "rpm_limit": 0,
    "max_concurrency": 4,
    "enabled": True,
    "models": [{"model": "gpt-5.4"}, {"model": "gpt-5.3-codex-spark"}],
    "daily_cost_limit": 0,
    "cost_multiplier": 1,
    "key_count": 0,
    "success_rate": 0.992,
    "websockets": True,
    "retry_other_keys_on_failure": False,
}

OAUTH_USAGE = {
    "provider": "codex",
    "plan_type": "plus",
    "windows": [
        {"limit_name": "Codex", "kind": "primary", "used_percent": 24, "remaining_percent": 76,
         "limit_window_seconds": 18_000, "reset_at": 1_789_123_600},
        {"limit_name": "Codex", "kind": "secondary", "used_percent": 72, "remaining_percent": 28,
         "limit_window_seconds": 604_800, "reset_at": 1_789_555_200},
        {"limit_name": "Codex Spark", "kind": "primary", "used_percent": 100, "remaining_percent": 0,
         "limit_window_seconds": 18_000, "reset_at": 1_789_105_600},
    ],
}


class OAuthFixtures(ConsoleFixtures):
    def __init__(self):
        super().__init__()
        self.usage = copy.deepcopy(OAUTH_USAGE)
        self.usage_calls = 0
        self.fail_usage = False
        self.hold_usage = False
        self.pending_usage = None

    @staticmethod
    def respond(route, data, status=200):
        payload = fixtures.envelope(data) if status == 200 else {"success": False, "error": data}
        route.fulfill(status=status, content_type="application/json", body=json.dumps(payload, ensure_ascii=False))

    def complete_usage(self, route):
        self.usage_calls += 1
        if self.fail_usage:
            self.respond(route, "上游额度服务暂时不可用，请稍后重试", 502)
        else:
            self.respond(route, self.usage)

    def route(self, route):
        path = urlparse(route.request.url).path
        if path == "/admin/channels":
            self.respond(route, [OAUTH_CHANNEL])
        elif path == "/admin/channels/201/editor":
            self.respond(route, {
                "channel": OAUTH_CHANNEL,
                "keys": [],
                "model_stats": {"available": False, "items": []},
                "url_stats": {"available": False, "items": []},
                "features": {"scheduled_check_enabled": False},
            })
        elif path == "/admin/channels/201/oauth-usage":
            if self.hold_usage:
                self.pending_usage = route
            else:
                self.complete_usage(route)
        else:
            super().route(route)


def open_editor(browser, base, *, width=1440, dark=False, mobile=False):
    context = browser.new_context(
        viewport={"width": width, "height": 900 if not mobile else 844},
        color_scheme="dark" if dark else "light",
        is_mobile=mobile,
        has_touch=mobile,
        locale="zh-CN",
        timezone_id="Asia/Shanghai",
        reduced_motion="reduce",
    )
    context.add_init_script(
        "localStorage.setItem('pivotflow_token','ui-test-only');"
        "localStorage.setItem('pivotflow_web_role','admin');"
    )
    data = OAuthFixtures()
    context.route("**/admin/**", data.route)
    context.route("**/dashboard/**", data.route)
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.goto(f"{base}/web/console/#/channels", wait_until="networkidle")
    page.get_by_role("button", name="编辑 Codex Plus · 主账号", exact=True).click()
    dialog = page.get_by_role("dialog")
    expect(dialog.get_by_text("OAuth 额度", exact=True)).to_be_visible()
    expect(dialog.get_by_text("Codex OAuth · plus", exact=True)).to_be_visible()
    expect(dialog.locator(".oauth-usage-row")).to_have_count(3)
    expect(dialog.locator(".oauth-usage-row--normal")).to_have_count(1)
    expect(dialog.locator(".oauth-usage-row--warning")).to_have_count(1)
    expect(dialog.locator(".oauth-usage-row--critical")).to_have_count(1)
    assert dialog.locator(".oauth-usage-track").evaluate_all(
        "elements => elements.map(element => Number(element.getAttribute('aria-valuenow')))"
    ) == [24, 72, 100]
    return context, page, data, errors, dialog


def capture(dialog, out, name):
    path = out / f"{name}.jpg"
    dialog.screenshot(path=str(path), type="jpeg", quality=90)
    return str(path.resolve())


def check_workflow(page, data, dialog):
    data.hold_usage = True
    dialog.get_by_role("button", name="刷新额度", exact=True).click()
    expect(dialog.get_by_text("正在刷新，当前数据仍可继续参考", exact=True)).to_be_visible()
    expect(dialog.locator(".oauth-usage-row")).to_have_count(3)
    assert data.pending_usage is not None
    pending = data.pending_usage
    data.pending_usage = None
    data.hold_usage = False
    data.complete_usage(pending)
    expect(dialog.get_by_text("正在刷新，当前数据仍可继续参考", exact=True)).to_have_count(0)

    data.fail_usage = True
    dialog.get_by_role("button", name="刷新额度", exact=True).click()
    expect(dialog.get_by_text("上游额度服务暂时不可用，请稍后重试", exact=True)).to_be_visible()
    data.fail_usage = False
    dialog.get_by_role("button", name="重试", exact=True).click()
    expect(dialog.locator(".oauth-usage-row")).to_have_count(3)
    expect(dialog.get_by_text("上游额度服务暂时不可用，请稍后重试", exact=True)).to_have_count(0)

    viewport_width = page.evaluate("document.documentElement.clientWidth")
    box = dialog.bounding_box()
    assert box and box["x"] >= 0 and box["x"] + box["width"] <= viewport_width + 1, box
    assert not data.pending_usage


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=ROOT / ".tmp-oauth-usage-ui")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), partial(QuietHandler, directory=str(ROOT)))
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    captures = []
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            try:
                base = f"http://127.0.0.1:{server.server_port}"
                for name, width, dark, mobile in [
                    ("desktop-light", 1440, False, False),
                    ("desktop-dark", 1440, True, False),
                    ("mobile", 390, False, True),
                ]:
                    context, page, data, errors, dialog = open_editor(
                        browser, base, width=width, dark=dark, mobile=mobile
                    )
                    try:
                        captures.append(capture(dialog, args.out, name))
                        check_workflow(page, data, dialog)
                        assert not errors, errors
                    finally:
                        context.close()
            finally:
                browser.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    report = {"passed": True, "captures": captures}
    (args.out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"passed": True, "captures": len(captures), "output": str(args.out.resolve())}, ensure_ascii=False))


if __name__ == "__main__":
    main()
