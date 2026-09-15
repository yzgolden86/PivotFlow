"""Capture and verify the upgraded model, statistics, and trend interfaces."""

import argparse
import json
from functools import partial
from http.server import ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import urlparse

from playwright.sync_api import expect, sync_playwright

import docs_screenshots as fixtures
from console_visual_regression import QuietHandler
from table_layout_ui_smoke import LayoutFixtures, QuietHTTPServer


ROOT = Path(__file__).resolve().parents[1]

STATS = {
    "stats": [
        {
            "channel_id": 101,
            "channel_name": "星河主路由",
            "model": "gpt-5.4-sol",
            "success": 96,
            "error": 4,
            "total": 100,
            "avg_first_byte_time_seconds": 0.41,
            "avg_duration_seconds": 2.1,
            "peak_rpm": 18,
            "avg_rpm": 7.2,
            "total_input_tokens": 128_000,
            "total_output_tokens": 32_000,
            "total_cost": 1.28,
            "effective_cost": 1.15,
            "actual_cost_multiplier_min": 0.8,
            "actual_cost_multiplier_max": 1.2,
        },
        {
            "channel_id": 102,
            "channel_name": "远岚 Codex",
            "model": "claude-sonnet-4-6",
            "success": 78,
            "error": 2,
            "total": 80,
            "avg_first_byte_time_seconds": 0.34,
            "avg_duration_seconds": 1.8,
            "peak_rpm": 14,
            "avg_rpm": 5.4,
            "total_input_tokens": 92_000,
            "total_output_tokens": 24_000,
            "total_cost": 0.86,
            "effective_cost": 0.79,
            "actual_cost_multiplier_min": 0.9,
            "actual_cost_multiplier_max": 1,
        },
        {
            "channel_id": 103,
            "channel_name": "晨光容灾",
            "model": "gemini-3-pro",
            "success": 47,
            "error": 3,
            "total": 50,
            "avg_first_byte_time_seconds": 0.52,
            "avg_duration_seconds": 2.4,
            "peak_rpm": 9,
            "avg_rpm": 3.1,
            "total_input_tokens": 61_000,
            "total_output_tokens": 12_000,
            "total_cost": 0.42,
            "effective_cost": 0,
            "actual_cost_multiplier_min": 0,
            "actual_cost_multiplier_max": 0,
        },
    ],
    "duration_seconds": 86_400,
    "rpm_stats": {"peak_rpm": 24, "peak_qps": 0.4, "avg_rpm": 9.6, "avg_qps": 0.16, "recent_rpm": 12, "recent_qps": 0.2},
    "is_today": True,
    "balance_history": [
        {"day": f"2026-09-{day:02d}", "currency": "USD", "balance": [120.4, 122.1, 121.8, 125.2, 125.2, 127.6, 126.9][day - 9], "accounts": 4}
        for day in range(9, 16)
    ],
}

MODEL_CHANNELS = [
    {**fixtures.CHANNELS[0], "site_account_id": 11, "models": [{"model": "gpt-5.4-sol"}]},
    {**fixtures.CHANNELS[1], "site_account_id": 21, "models": [{"model": "claude-sonnet-4-6"}]},
    {**fixtures.CHANNELS[2], "site_account_id": 31, "models": [{"model": "gemini-3-pro"}]},
]

ANALYTICS_SITE_MODELS = [
    {
        "site_account_id": 11,
        "model": "gpt-5.4-sol",
        "route_type": "openai_response",
        "source": "site_discovery",
        "disabled": False,
        "stale": False,
        "last_seen_at": fixtures.NOW,
        "created_at": fixtures.NOW - 86_400_000,
        "updated_at": fixtures.NOW,
    },
    {
        "site_account_id": 21,
        "model": "claude-sonnet-4-6",
        "route_type": "anthropic",
        "source": "site_discovery",
        "disabled": False,
        "stale": False,
        "last_seen_at": fixtures.NOW,
        "created_at": fixtures.NOW - 86_400_000,
        "updated_at": fixtures.NOW,
    },
    {
        "site_account_id": 31,
        "model": "gemini-3-pro",
        "route_type": "gemini",
        "source": "site_discovery",
        "disabled": False,
        "stale": False,
        "last_seen_at": fixtures.NOW,
        "created_at": fixtures.NOW - 86_400_000,
        "updated_at": fixtures.NOW,
    },
]

SITE_CHANNEL_BINDINGS = [{
    "id": 1,
    "site_account_id": 11,
    "projection_key": "site:11:primary",
    "channel_id": 101,
    "pricing_group": "vip",
    "ownership": "projected",
    "status": "active",
    "last_sync_status": "success",
    "created_at": fixtures.NOW,
    "updated_at": fixtures.NOW,
}]

SITE_PRICING = {
    "site_id": 1,
    "available": True,
    "group_ratio": {"default": 1, "vip": .8},
    "models": [{
        "model": "gpt-5.4-sol",
        "quota_type": 0,
        "per_call_price": 0,
        "model_ratio": 1.25,
        "completion_ratio": 4,
        "cache_ratio": .1,
        "cache_creation_ratio": 1.25,
        "groups": ["vip"],
        "input_price": 2.5,
        "output_price": 10,
        "cache_read_price": .25,
        "cache_write_price": 3.125,
    }],
}


class AnalyticsFixtures(LayoutFixtures):
    @staticmethod
    def respond(route, data):
        route.fulfill(status=200, content_type="application/json", body=json.dumps({"success": True, "data": data}, ensure_ascii=False))

    def route(self, route):
        path = urlparse(route.request.url).path
        if path == "/admin/channels":
            self.respond(route, MODEL_CHANNELS)
            return
        if path == "/admin/site-channel-bindings":
            self.respond(route, SITE_CHANNEL_BINDINGS)
            return
        if path == "/admin/site-pricing":
            self.respond(route, SITE_PRICING)
            return
        if path == "/admin/site-models":
            self.respond(route, ANALYTICS_SITE_MODELS)
            return
        if path == "/admin/stats":
            self.respond(route, STATS)
            return
        if path == "/admin/stats/filter-options":
            self.respond(route, {"channel_names": [item["channel_name"] for item in STATS["stats"]], "models": [item["model"] for item in STATS["stats"]]})
            return
        super().route(route)


def open_page(browser, base, route):
    context = browser.new_context(viewport={"width": 1510, "height": 950}, locale="zh-CN", timezone_id="Asia/Shanghai", reduced_motion="reduce")
    context.add_init_script("localStorage.setItem('pivotflow_token','ui-test-only');localStorage.setItem('pivotflow_web_role','admin');")
    data = AnalyticsFixtures()
    context.route("**/admin/**", data.route)
    context.route("**/dashboard/**", data.route)
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.goto(f"{base}/web/console/#/{route}", wait_until="networkidle")
    return context, page, errors


def assert_no_overflow(page, selector):
    result = page.evaluate(
        """selector => Array.from(document.querySelectorAll(selector)).map(element => ({
            text: element.textContent.trim(),
            clientWidth: element.clientWidth,
            scrollWidth: element.scrollWidth
        })).filter(item => item.scrollWidth > item.clientWidth + 1)""",
        selector,
    )
    assert not result, result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=ROOT / ".tmp-analytics-ui")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), partial(QuietHandler, directory=str(ROOT)))
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    captures = []
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            base = f"http://127.0.0.1:{server.server_port}"

            context, page, errors = open_page(browser, base, "models")
            try:
                expect(page.get_by_role("heading", name="模型测试", exact=True)).to_be_visible()
                expect(page.locator(".model-card").first).to_be_visible()
                expect(page.locator(".model-vendor").first).to_be_visible()
                expect(page.locator(".model-vendor-tabs").first).to_be_visible()
                expect(page.locator(".model-vendor-tab").first).to_be_visible()
                assert page.locator(".model-vendor-tab").count() >= 4
                expect(page.locator(".model-vendor-tab", has_text="Claude")).to_be_visible()
                expect(page.locator(".model-vendor-tab", has_text="Gemini")).to_be_visible()
                expect(page.locator(".model-pricing-strip").first).to_be_visible()
                expect(page.locator(".model-card > header button").first).to_be_visible()
                expect(page.locator(".model-pricing-strip").first).to_contain_text("输入")
                expect(page.locator(".model-pricing-strip").first).to_contain_text("$2.00/M")
                expect(page.locator(".model-pricing-strip").first).to_contain_text("$8.00/M")
                page.locator(".model-vendor-tab", has_text="OpenAI").click()
                expect(page.locator(".model-card")).to_have_count(1)
                page.locator(".model-vendor-tab", has_text="Claude").click()
                expect(page.locator(".model-card")).to_have_count(1)
                page.locator(".model-vendor-tab", has_text="全部").click()
                expect(page.locator(".model-card")).to_have_count(3)
                page.locator('.model-layout-toggle button[title="列表视图"]').click()
                expect(page.locator(".model-price-line").first).to_be_visible()
                expect(page.locator(".model-price-line").first).to_contain_text("输入 $2.00/M")
                expect(page.locator(".model-price-line").first).to_contain_text("输出 $8.00/M")
                expect(page.locator(".model-price-line").first).to_contain_text("倍率 1.25×/4×")
                assert_no_overflow(page, ".model-vendor-tab, .model-card > header strong, .model-card > header code, .model-card-meta dd, .model-pricing-strip strong, .model-card > footer span, .model-price-line")
                assert not errors, errors
                page.screenshot(path=str(args.out / "models.png"), full_page=True, animations="disabled")
                captures.append(str((args.out / "models.png").resolve()))
            finally:
                context.close()

            context, page, errors = open_page(browser, base, "stats")
            try:
                expect(page.get_by_role("heading", name="用量统计", exact=True)).to_be_visible()
                expect(page.locator(".stats-distribution-list > div").first).to_be_visible()
                expect(page.locator(".balance-change-panel").first).to_be_visible()
                expect(page.locator(".balance-change-bar").first).to_be_visible()
                assert page.locator(".balance-change-bar").count() >= 3
                assert page.locator(".balance-change-bar.gain").count() > 0
                assert page.locator(".balance-change-bar.loss").count() > 0
                page.locator(".balance-change-cell").nth(2).hover()
                tooltip = page.locator(".balance-change-tooltip")
                expect(tooltip).to_be_visible()
                expect(tooltip).to_contain_text("变化")
                expect(tooltip).to_contain_text("余额")
                cell_box = page.locator(".balance-change-cell").nth(2).bounding_box()
                tooltip_box = tooltip.bounding_box()
                assert cell_box and tooltip_box
                cell_center = cell_box["x"] + cell_box["width"] / 2
                tooltip_center = tooltip_box["x"] + tooltip_box["width"] / 2
                assert abs(cell_center - tooltip_center) <= 12, {"cell": cell_box, "tooltip": tooltip_box}
                highlight = page.evaluate(
                    """() => {
                      const cell = document.querySelectorAll('.balance-change-cell')[2]
                      const style = getComputedStyle(cell)
                      const guide = getComputedStyle(cell, '::before')
                      return { background: style.backgroundColor, guideWidth: parseFloat(guide.width), guideOpacity: guide.opacity }
                    }"""
                )
                # 悬停高亮必须是柱子附近的窄条，不能铺满整个日期格子形成多余方框。
                assert highlight["background"] in ("rgba(0, 0, 0, 0)", "transparent"), highlight
                assert highlight["guideWidth"] <= 20, highlight
                assert highlight["guideOpacity"] == "1", highlight
                expect(page.locator(".stats-records .record-row").first).to_be_visible()
                assert_no_overflow(page, ".stats-distribution-list strong, .stats-distribution-list > div > span:last-child")
                assert not errors, errors
                page.screenshot(path=str(args.out / "stats.png"), full_page=True, animations="disabled")
                captures.append(str((args.out / "stats.png").resolve()))
            finally:
                context.close()

            context, page, errors = open_page(browser, base, "trend")
            try:
                expect(page.get_by_role("heading", name="消费趋势", exact=True)).to_be_visible()
                expect(page.locator(".combined-trend-chart").first).to_be_visible()
                expect(page.locator(".combined-trend-legend > span").first).to_be_visible()
                expect(page.locator(".combined-trend-curve").first).to_be_visible()
                assert page.locator(".combined-trend-curve").count() == 3
                curve_path = page.locator(".combined-trend-curve").first.get_attribute("d") or ""
                assert "C " in curve_path and " L " not in curve_path, curve_path
                assert "NaN" not in curve_path and "Infinity" not in curve_path, curve_path[:200]
                assert curve_path.count("C ") == 23, curve_path.count("C ")
                # 曲线应明显圆润：相邻三次贝塞尔之间不存在共线折角，
                # 用相邻切向量夹角做粗略校验，避免退回折线观感。
                tangent_angles = page.evaluate(
                    """() => {
                      const path = document.querySelector('.combined-trend-curve')
                      const length = path.getTotalLength()
                      const points = Array.from({ length: 240 }, (_, index) => path.getPointAtLength(length * index / 239))
                      const angles = []
                      for (let index = 2; index < points.length; index += 1) {
                        const a = Math.atan2(points[index - 1].y - points[index - 2].y, points[index - 1].x - points[index - 2].x)
                        const b = Math.atan2(points[index].y - points[index - 1].y, points[index].x - points[index - 1].x)
                        let delta = Math.abs(b - a)
                        if (delta > Math.PI) delta = Math.PI * 2 - delta
                        angles.push(delta)
                      }
                      return angles
                    }"""
                )
                assert max(tangent_angles) < .25, max(tangent_angles)
                assert page.locator(".combined-trend-bar").count() == 0
                assert page.locator(".trend-metric-tabs").count() == 0
                assert page.locator(".trend-area").count() == 0
                assert not errors, errors
                page.screenshot(path=str(args.out / "trend-curve.png"), full_page=True, animations="disabled")
                captures.append(str((args.out / "trend-curve.png").resolve()))
                hover_count = page.locator(".combined-trend-hover-column").count()
                page.locator(".combined-trend-hover-column").nth(min(2, hover_count - 1)).hover()
                curve_tooltip = page.locator(".combined-trend-tooltip")
                expect(curve_tooltip).to_be_visible()
                expect(curve_tooltip).to_contain_text("请求量")
                page.screenshot(path=str(args.out / "trend-curve-hover.png"), full_page=True, animations="disabled")
                captures.append(str((args.out / "trend-curve-hover.png").resolve()))
                page.get_by_role("button", name="柱状图").click()
                expect(page.locator(".combined-trend-bar").first).to_be_visible()
                assert page.locator(".combined-trend-bar").count() >= 3
                bucket_count = page.locator(".combined-trend-bucket").count()
                page.locator(".combined-trend-bucket").nth(min(2, bucket_count - 1)).hover()
                tooltip = page.locator(".combined-trend-tooltip")
                expect(tooltip).to_be_visible()
                expect(tooltip).to_contain_text("请求量")
                expect(tooltip).to_contain_text("Token")
                expect(tooltip).to_contain_text("费用")
                expect(page.locator(".trend-breakdown-list > div").first).to_be_visible()
                assert_no_overflow(page, ".combined-trend-legend > span, .combined-trend-axis span, .trend-breakdown-list strong, .trend-breakdown-list em")
                assert not errors, errors
                page.screenshot(path=str(args.out / "trend-hover.png"), full_page=True, animations="disabled")
                captures.append(str((args.out / "trend-hover.png").resolve()))
            finally:
                context.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)

    (args.out / "report.json").write_text(json.dumps({"passed": True, "captures": captures}, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"passed": True, "captures": len(captures), "output": str(args.out.resolve())}, ensure_ascii=False))


if __name__ == "__main__":
    main()
