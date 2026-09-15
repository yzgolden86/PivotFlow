"""Verify actual per-Key multiplier labels in the built statistics console.

The test uses isolated synthetic API responses. It does not connect to a live
backend or read production statistics.
"""

import argparse
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

STATS = [
    {
        "channel_id": 101,
        "channel_name": "免费试用池",
        "model": "gpt-5.4-mini",
        "success": 28,
        "error": 0,
        "total": 28,
        "avg_first_byte_time_seconds": 0.31,
        "avg_duration_seconds": 1.28,
        "peak_rpm": 9,
        "avg_rpm": 3.5,
        "total_input_tokens": 24_800,
        "total_output_tokens": 6_200,
        "total_cost": 0.062,
        "effective_cost": 0,
        "cost_multiplier": 1,
        "actual_cost_multiplier_min": 0,
        "actual_cost_multiplier_max": 0,
    },
    {
        "channel_id": 102,
        "channel_name": "标准主线路",
        "model": "claude-sonnet-4-6",
        "success": 96,
        "error": 2,
        "total": 98,
        "avg_first_byte_time_seconds": 0.48,
        "avg_duration_seconds": 2.16,
        "peak_rpm": 18,
        "avg_rpm": 7.2,
        "total_input_tokens": 81_300,
        "total_output_tokens": 18_900,
        "total_cost": 0.42,
        "effective_cost": 0.42,
        "cost_multiplier": 1,
        "actual_cost_multiplier_min": 1,
        "actual_cost_multiplier_max": 1,
    },
    {
        "channel_id": 103,
        "channel_name": "混合 Key 线路",
        "model": "gemini-3-pro",
        "success": 72,
        "error": 1,
        "total": 73,
        "avg_first_byte_time_seconds": 0.39,
        "avg_duration_seconds": 1.84,
        "peak_rpm": 13,
        "avg_rpm": 5.6,
        "total_input_tokens": 65_700,
        "total_output_tokens": 14_300,
        "total_cost": 0.31,
        "effective_cost": 0.298,
        "cost_multiplier": 1,
        "actual_cost_multiplier_min": 0.8,
        "actual_cost_multiplier_max": 1.2,
    },
    {
        "channel_id": 104,
        "channel_name": "旧版兼容线路",
        "model": "glm-5.3",
        "success": 39,
        "error": 1,
        "total": 40,
        "avg_first_byte_time_seconds": 0.62,
        "avg_duration_seconds": 2.72,
        "peak_rpm": 8,
        "avg_rpm": 2.9,
        "total_input_tokens": 32_600,
        "total_output_tokens": 8_400,
        "total_cost": 0.18,
        "effective_cost": 0.135,
        "cost_multiplier": 0.75,
    },
]


class StatsFixtures(ConsoleFixtures):
    @staticmethod
    def respond(route, data):
        route.fulfill(
            status=200,
            content_type="application/json",
            body=json.dumps(fixtures.envelope(data), ensure_ascii=False),
        )

    def route(self, route):
        path = urlparse(route.request.url).path
        if path == "/admin/stats":
            self.respond(route, {
                "stats": STATS,
                "duration_seconds": 86_400,
                "rpm_stats": {
                    "peak_rpm": 32,
                    "peak_qps": 1.1,
                    "avg_rpm": 12.4,
                    "avg_qps": 0.21,
                    "recent_rpm": 16,
                    "recent_qps": 0.27,
                },
                "is_today": True,
            })
            return
        if path == "/admin/stats/filter-options":
            self.respond(route, {
                "channel_names": [item["channel_name"] for item in STATS],
                "models": [item["model"] for item in STATS],
            })
            return
        super().route(route)


def measure(page):
    return page.evaluate("""() => ({
      viewport: document.documentElement.clientWidth,
      scrollWidth: document.documentElement.scrollWidth
    })""")


def check_page(browser, base, out, *, name, width, mobile):
    context = browser.new_context(
        viewport={"width": width, "height": 844 if mobile else 900},
        color_scheme="light",
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
    data = StatsFixtures()
    context.route("**/admin/**", data.route)
    context.route("**/dashboard/**", data.route)
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    try:
        page.goto(f"{base}/web/console/#/stats", wait_until="networkidle")
        expect(page.get_by_role("heading", name="用量统计", exact=True)).to_be_visible()
        expect(page.locator(".stats-records .record-row")).to_have_count(4)
        labels = page.locator(".stats-records .record-row > div:last-child span")
        expect(labels).to_have_text([
            "免费",
            "标准倍率",
            "0.8x–1.2x 实际倍率",
            "0.75x 倍率",
        ])
        free_row = page.locator(".stats-records .record-row").filter(has_text="免费试用池")
        expect(free_row.locator("> div:last-child strong")).to_have_text("$0.0000")
        dimensions = measure(page)
        assert dimensions["scrollWidth"] <= dimensions["viewport"], dimensions
        assert not errors, errors
        screenshot = out / f"{name}.png"
        page.screenshot(path=str(screenshot), full_page=True, animations="disabled")
        return str(screenshot.resolve())
    finally:
        context.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=ROOT / ".tmp-stats-multiplier-ui")
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
                captures.append(check_page(browser, base, args.out, name="desktop", width=1440, mobile=False))
                captures.append(check_page(browser, base, args.out, name="mobile", width=390, mobile=True))
            finally:
                browser.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    report = {"passed": True, "captures": captures}
    (args.out / "report.json").write_text(
        json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8"
    )
    print(json.dumps({"passed": True, "captures": len(captures), "output": str(args.out.resolve())}, ensure_ascii=False))


if __name__ == "__main__":
    main()
