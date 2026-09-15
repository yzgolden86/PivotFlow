"""Exercise the channel editor Key/model workflow with synthetic fixtures.

This smoke test uses the built console only. It never sends credentials or
model requests to a real upstream service.
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

DISCOVERED_MODELS = [
    {"model": "gpt-5.4"},
    {"model": "claude-sonnet-4-6"},
    {"model": "gemini-3-pro"},
]


class ChannelEditorFixtures(ConsoleFixtures):
    def __init__(self):
        super().__init__()
        self.created_payload = None
        self.discovery_calls = []

    @staticmethod
    def respond(route, data, status=200):
        payload = fixtures.envelope(data) if status == 200 else {"success": False, "error": data}
        route.fulfill(status=status, content_type="application/json", body=json.dumps(payload, ensure_ascii=False))

    def route(self, route):
        parsed = urlparse(route.request.url)
        path = parsed.path
        if path == "/admin/channels/models/fetch":
            self.discovery_calls.append(route.request.post_data_json)
            self.respond(route, {"models": DISCOVERED_MODELS, "protocol": "openai"})
            return
        if path == "/admin/channels" and route.request.method == "POST":
            self.created_payload = route.request.post_data_json
            created = copy.deepcopy(fixtures.CHANNELS[0])
            created["id"] = 900
            created["name"] = self.created_payload["name"]
            self.respond(route, created)
            return
        super().route(route)


def open_editor(browser, base, width, mobile=False):
    context = browser.new_context(
        viewport={"width": width, "height": 900 if not mobile else 844},
        color_scheme="light", is_mobile=mobile, has_touch=mobile,
        locale="zh-CN", timezone_id="Asia/Shanghai", reduced_motion="reduce",
    )
    context.add_init_script(
        "localStorage.setItem('pivotflow_token','ui-test-only');"
        "localStorage.setItem('pivotflow_web_role','admin');"
    )
    data = ChannelEditorFixtures()
    context.route("**/admin/**", data.route)
    context.route("**/dashboard/**", data.route)
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.goto(f"{base}/web/console/#/channels", wait_until="networkidle")
    page.get_by_role("button", name="其他来源", exact=True).click()
    page.get_by_role("menuitem", name="手工渠道", exact=True).click()
    dialog = page.get_by_role("dialog", name="添加渠道")
    expect(dialog).to_be_visible()
    return context, page, data, errors, dialog


def measure(page):
    return page.evaluate("""() => ({
      viewport: document.documentElement.clientWidth,
      scrollWidth: document.documentElement.scrollWidth
    })""")


def exercise(page, dialog, data, screenshot_path):
    dialog.get_by_label("渠道名称", exact=True).fill("Key 范围回归渠道")
    dialog.get_by_label("上游 URL", exact=True).fill("https://api.example.net")
    default_multiplier = dialog.locator("label").filter(has_text="渠道默认倍率").locator("input")
    default_multiplier.fill("1.25")

    dialog.locator(".key-editor-empty").click()
    key_input = dialog.get_by_label("API Key", exact=True)
    key_input.fill("sk-ui-test-key")
    dialog.get_by_label("备注", exact=True).fill("免费测试 Key")
    key_multiplier = dialog.locator("label").filter(has_text="Key 成本倍率").locator("input")
    expect(key_multiplier).to_have_value("1.25")
    key_multiplier.fill("0")

    discover = dialog.get_by_role("button", name="获取模型", exact=True)
    expect(discover).to_be_enabled()
    discover.click()
    expect(dialog.locator(".editable-model-row")).to_have_count(3)
    expect(dialog.get_by_text("3 个候选，已勾选 0 个；保存时只提交勾选模型", exact=True)).to_be_visible()
    dialog.get_by_role("button", name="全选搜索结果", exact=True).click()
    expect(dialog.get_by_text("3 个候选，已勾选 3 个；保存时只提交勾选模型", exact=True)).to_be_visible()

    restrict = dialog.get_by_role("radio", name="指定模型", exact=True)
    restrict.check()
    scope = dialog.locator(".key-model-scope")
    expect(scope).to_be_visible()
    scope.get_by_label("claude-sonnet-4-6", exact=True).check()
    assert scope.locator("input:checked").evaluate_all("items => items.map(item => item.nextElementSibling.textContent)") == ["gpt-5.4", "claude-sonnet-4-6"]

    pause = dialog.get_by_role("radio", name="暂停路由", exact=True)
    pause.check()
    expect(dialog.get_by_text("不会参与任何请求 · 恢复后仍限定 2 个模型", exact=True)).to_be_visible()
    expect(dialog.locator(".key-model-scope")).to_have_count(0)
    dialog.screenshot(path=str(screenshot_path), type="jpeg", quality=90)
    restrict.check()
    expect(dialog.locator(".key-model-scope")).to_be_visible()
    assert dialog.locator(".key-model-scope input:checked").evaluate_all("items => items.map(item => item.nextElementSibling.textContent)") == ["gpt-5.4", "claude-sonnet-4-6"]

    dialog.get_by_role("button", name="保存渠道", exact=True).click()
    expect(dialog).to_have_count(0)
    assert data.created_payload is not None, "channel save payload was not sent"
    assert data.created_payload["cost_multiplier"] == 1.25
    assert data.created_payload["api_keys"] == [{
        "api_key": "sk-ui-test-key", "note": "免费测试 Key",
        "allowed_models": ["gpt-5.4", "claude-sonnet-4-6"],
        "model_scope_empty": False, "cost_multiplier": 0,
    }], data.created_payload
    assert data.discovery_calls and data.discovery_calls[0]["api_keys"] == ["sk-ui-test-key"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=ROOT / ".tmp-channel-key-editor-ui")
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
                for name, width, mobile in [("desktop", 1440, False), ("mobile", 390, True)]:
                    context, page, data, errors, dialog = open_editor(browser, base, width, mobile)
                    try:
                        dialog.screenshot(path=str(args.out / f"{name}-before.jpg"), type="jpeg", quality=90)
                        captures.append(str((args.out / f"{name}-before.jpg").resolve()))
                        if mobile:
                            assert measure(page)["scrollWidth"] <= measure(page)["viewport"], measure(page)
                        after_path = args.out / f"{name}-after.jpg"
                        exercise(page, dialog, data, after_path)
                        assert not errors, errors
                        captures.append(str(after_path.resolve()))
                        assert measure(page)["scrollWidth"] <= measure(page)["viewport"], measure(page)
                    finally:
                        context.close()
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
