"""Verify built Key health UI with isolated synthetic fixtures and JPEG screenshots.

No live backend, real credentials, model calls, or production data are used.
Run: py -3 scripts/key_health_ui_smoke.py --out .tmp-key-health-ui
"""

import argparse
import copy
import json
import re
from functools import partial
from http.server import ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import parse_qs, urlparse

from playwright.sync_api import expect, sync_playwright

import docs_screenshots as fixtures
from console_visual_regression import ConsoleFixtures, QuietHandler


ROOT = Path(__file__).resolve().parents[1]


class KeyFixtures(ConsoleFixtures):
    def __init__(self):
        super().__init__()
        self.snapshot = copy.deepcopy(fixtures.KEY_HEALTH)
        self.calls = []
        self.mutations = []
        self.hold = False
        self.pending = None
        self.fail_read = False
        self.local_limit = False
        self.clock = max(key["health"]["checked_at"] for key in self.snapshot["keys"])

    @staticmethod
    def respond(route, data, status=200):
        payload = fixtures.envelope(data) if status == 200 else {"success": False, "error": data}
        route.fulfill(status=status, content_type="application/json", body=json.dumps(payload, ensure_ascii=False))

    def complete_probe(self, route):
        body = route.request.post_data_json
        key = next((item for item in self.snapshot["keys"] if item["id"] == body["key_id"] and item["key_index"] == body["key_index"]), None)
        if key is None:
            self.respond(route, "Key 列表已变化，请刷新后重试", 409)
        elif self.local_limit:
            self.respond(route, {"success": False, "rpm_limited": True, "status_code": 429})
        else:
            self.clock += 1000
            key["health"] = {"status": "healthy", "reason": fixtures.KEY_HEALTH["keys"][0]["health"]["reason"], "status_code": 200, "checked_at": self.clock}
            self.respond(route, {"success": True, "key_health": key["health"]})

    def route(self, route):
        parsed = urlparse(route.request.url)
        path = parsed.path
        if path == "/admin/channels":
            channels = copy.deepcopy(fixtures.CHANNELS)
            channels[0]["key_count"] = len(self.snapshot["keys"])
            channels[0]["key_health_issue_count"] = sum(bool(key["health"]["checked_at"]) and key["health"]["status"] != "healthy" for key in self.snapshot["keys"])
            route.fulfill(content_type="application/json", body=json.dumps(fixtures.envelope(channels, len(channels)), ensure_ascii=False))
        elif path == "/admin/channels/101/key-health":
            self.respond(route, "测试用读取失败，请重试" if self.fail_read else self.snapshot, 500 if self.fail_read else 200)
        elif path == "/admin/channels/101/test":
            self.calls.append(route.request.post_data_json)
            if self.hold:
                self.pending = route
            else:
                self.complete_probe(route)
        elif path in ("/admin/channels/101/key-enable", "/admin/channels/101/key-disable") or path.startswith("/admin/channels/101/keys/"):
            if route.request.method == "DELETE":
                body = {"key_id": int(parse_qs(parsed.query)["key_id"][0]), "key_index": int(path.split("/")[-1])}
            else:
                body = route.request.post_data_json
            key = next((item for item in self.snapshot["keys"] if item["id"] == body["key_id"] and item["key_index"] == body["key_index"]), None)
            if not key:
                self.respond(route, "Key 列表已变化，请刷新后重试", 409)
                return
            self.mutations.append({"path": path, **body})
            if route.request.method == "DELETE":
                self.snapshot["keys"].remove(key)
                for index, item in enumerate(self.snapshot["keys"]):
                    item["key_index"] = index
            else:
                key["disabled"] = path.endswith("key-disable")
            self.respond(route, {"ok": True})
        else:
            super().route(route)


def open_page(browser, base, *, width=1440, dark=False, mobile=False):
    context = browser.new_context(viewport={"width": width, "height": 844 if mobile else 1050},
                                  color_scheme="dark" if dark else "light", is_mobile=mobile, has_touch=mobile,
                                  locale="zh-CN", timezone_id="Asia/Shanghai", reduced_motion="reduce")
    context.add_init_script("localStorage.setItem('pivotflow_token','ui-test-only');localStorage.setItem('pivotflow_web_role','admin');")
    data = KeyFixtures()
    context.route("**/admin/**", data.route)
    context.route("**/dashboard/**", data.route)
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.goto(f"{base}/web/console/#/channels", wait_until="networkidle")
    expect(page.get_by_role("heading", name="渠道分发", exact=True)).to_be_visible()
    # Inspect rendered UI after hydration, then use its semantic entry point.
    assert "健康管理" in page.locator("main").inner_text()
    page.locator(".channel-key-health-link").first.click()
    expect(page.get_by_role("dialog")).to_be_visible()
    expect(page.locator(".key-health-card")).to_have_count(6)
    return context, page, data, errors


def capture(page, out, name):
    page.screenshot(path=str(out / f"{name}.jpg"), type="jpeg", quality=88, animations="disabled")
    metrics = page.evaluate("""() => {
      const dialog = document.querySelector('[role="dialog"]');
      return {width: innerWidth, pageWidth: document.documentElement.scrollWidth,
        dialogWidth: dialog.clientWidth, dialogContentWidth: dialog.scrollWidth,
        theme: document.documentElement.dataset.theme,
        icons: [...dialog.querySelectorAll('.key-health-emblem, .key-health-key-icon')].map(el => {
          const a=el.getBoundingClientRect(), b=el.querySelector('svg').getBoundingClientRect();
          return {dx: b.x+b.width/2-a.x-a.width/2, dy: b.y+b.height/2-a.y-a.height/2};
        })};
    }""")
    assert metrics["pageWidth"] <= metrics["width"], metrics
    assert metrics["dialogContentWidth"] <= metrics["dialogWidth"] + 1, metrics
    assert all(abs(icon["dx"]) < 0.6 and abs(icon["dy"]) < 0.6 for icon in metrics["icons"]), metrics
    assert "sk-health-secret" not in page.content()
    return {"screenshot": f"{name}.jpg", **metrics}


def check_workflows(page, data):
    dialog = page.get_by_role("dialog")
    page.get_by_role("button", name=re.compile("需关注")).click()
    expect(page.locator(".key-health-card")).to_have_count(3)
    page.get_by_role("button", name="复检 Key #2", exact=True).click()
    expect(page.locator(".key-health-card")).to_have_count(2)
    assert data.calls[-1] == {"model": "gpt-5.4", "client_protocol": "openai", "stream": False, "content": "Reply only OK.", "max_tokens": 64, "key_index": 1, "key_id": 702}
    page.once("dialog", lambda prompt: prompt.dismiss())
    page.get_by_role("button", name=re.compile("复检当前筛选")).click()
    assert len(data.calls) == 1, "dismissed batch called upstream"

    page.get_by_role("button", name=re.compile("已停用 1")).click()
    expect(page.locator(".key-health-card")).to_have_count(1)
    expect(page.get_by_role("button", name=re.compile("复检当前筛选"))).to_be_disabled()
    page.get_by_role("button", name="复检 Key #6", exact=True).click()
    expect(page.locator(".key-health-card").get_by_text("正常", exact=True)).to_be_visible()
    assert data.snapshot["keys"][-1]["disabled"], "probe enabled a disabled Key"
    page.once("dialog", lambda prompt: prompt.accept())
    page.get_by_role("button", name="启用 Key #6", exact=True).click()
    expect(page.locator(".key-health-card")).to_have_count(0)

    page.get_by_role("button", name=re.compile("全部 Key")).click()
    before = len(data.calls)
    data.local_limit = True
    page.get_by_role("button", name="复检 Key #3", exact=True).click()
    expect(page.locator('.key-health-message[role="status"]')).to_contain_text("保留原状态")
    expect(page.locator(".key-health-card").nth(2).get_by_text("额度不足", exact=True)).to_be_visible()
    data.local_limit = False
    data.hold = True
    page.once("dialog", lambda prompt: prompt.accept())
    page.get_by_role("button", name=re.compile("复检当前筛选")).click()
    expect(page.get_by_role("button", name="停止后续")).to_be_visible()
    page.get_by_role("button", name="停止后续").click()
    assert data.pending is not None
    data.complete_probe(data.pending)
    data.pending = None
    expect(page.get_by_role("button", name="刷新 Key 状态")).to_be_enabled()
    assert len(data.calls) == before + 2, "stop did not cancel subsequent probes"
    data.hold = False

    page.once("dialog", lambda prompt: prompt.accept())
    page.get_by_role("button", name="停用 Key #3", exact=True).click()
    expect(page.locator(".key-health-card").nth(2).get_by_text("已停用", exact=True)).to_be_visible()
    page.once("dialog", lambda prompt: prompt.accept())
    page.get_by_role("button", name="删除 Key #2", exact=True).click()
    expect(page.locator(".key-health-card")).to_have_count(5)
    assert data.mutations[-1]["key_id"] == 702
    expect(page.locator(".key-health-card").nth(1).get_by_text("额度不足", exact=True)).to_be_visible()

    # An external edit after opening the panel cannot target the shifted key.
    data.snapshot["keys"].pop(0)
    for index, key in enumerate(data.snapshot["keys"]):
        key["key_index"] = index
    page.once("dialog", lambda prompt: prompt.accept())
    page.get_by_role("button", name="删除 Key #1", exact=True).click()
    expect(page.get_by_role("alert")).to_contain_text("Key 列表已变化")
    page.get_by_role("button", name="刷新 Key 状态").click()
    expect(page.locator(".key-health-card")).to_have_count(4)

    data.fail_read = True
    page.get_by_role("button", name="刷新 Key 状态").click()
    expect(page.get_by_role("alert")).to_contain_text("测试用读取失败")
    data.fail_read = False
    page.get_by_role("button", name="刷新 Key 状态").click()
    expect(page.get_by_role("alert")).to_have_count(0)

    page.get_by_role("button", name="关闭弹窗").focus()
    page.keyboard.press("Shift+Tab")
    assert dialog.locator(":focus").count() == 1, "focus escaped dialog"
    page.keyboard.press("Tab")
    expect(page.get_by_role("button", name="关闭弹窗")).to_be_focused()
    page.keyboard.press("Escape")
    expect(dialog).to_have_count(0)
    expect(page.locator(".channel-key-health-link").first).to_be_focused()


def check_batch_limit(browser, base):
    context, page, data, errors = open_page(browser, base)
    try:
        base_key = copy.deepcopy(fixtures.KEY_HEALTH["keys"][4])
        data.snapshot["keys"] = []
        for index in range(25):
            key = {**copy.deepcopy(base_key), "id": 900 + index, "key_index": index, "disabled": index == 0}
            data.snapshot["keys"].append(key)
        page.get_by_role("button", name="刷新 Key 状态").click()
        expect(page.locator(".key-health-card")).to_have_count(25)
        page.once("dialog", lambda prompt: prompt.accept())
        page.get_by_role("button", name="复检当前筛选 (20)").click()
        expect(page.locator('.key-health-message[role="status"]')).to_contain_text("已检查 20 个", timeout=30000)
        assert len(data.calls) == 20 and all(call["key_id"] != 900 for call in data.calls)
        assert not errors, errors
    finally:
        context.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=ROOT / ".tmp-key-health-ui")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), partial(QuietHandler, directory=str(ROOT)))
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    captures = []
    try:
        with sync_playwright() as p:
            browser = p.chromium.launch(headless=True)
            try:
                base = f"http://127.0.0.1:{server.server_port}"
                for name, width, dark, mobile in [("desktop-light", 1440, False, False), ("desktop-dark", 1440, True, False), ("mobile", 390, False, True)]:
                    context, page, data, errors = open_page(browser, base, width=width, dark=dark, mobile=mobile)
                    try:
                        captures.append(capture(page, args.out, name))
                        page.get_by_role("button", name=re.compile("需关注")).click()
                        expect(page.locator(".key-health-card")).to_have_count(3)
                        if mobile:
                            page.get_by_role("button", name="复检 Key #2", exact=True).scroll_into_view_if_needed()
                        captures.append(capture(page, args.out, name + "-attention"))
                        page.get_by_role("button", name=re.compile("全部 Key")).click()
                        check_workflows(page, data)
                        assert not errors, errors
                    finally:
                        context.close()
                check_batch_limit(browser, base)
            finally:
                browser.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    (args.out / "report.json").write_text(json.dumps({"passed": True, "captures": captures}, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"passed": True, "captures": len(captures), "output": str(args.out.resolve())}, ensure_ascii=False))


if __name__ == "__main__":
    main()
