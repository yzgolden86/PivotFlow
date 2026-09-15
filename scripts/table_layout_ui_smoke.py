"""Verify table proportions and the route diagnostics dialog with synthetic data."""

import argparse
import json
import sys
from functools import partial
from http.server import ThreadingHTTPServer
from pathlib import Path
from threading import Thread
from urllib.parse import urlparse

from playwright.sync_api import expect, sync_playwright

import docs_screenshots as fixtures
from console_visual_regression import ConsoleFixtures, QuietHandler


ROOT = Path(__file__).resolve().parents[1]
NOW = 1_786_579_200_000


class QuietHTTPServer(ThreadingHTTPServer):
    daemon_threads = True

    def handle_error(self, request, client_address):
        exception = sys.exc_info()[1]
        if isinstance(exception, ConnectionAbortedError):
            return
        super().handle_error(request, client_address)


def diagnostic_channel(channel_id: int, name: str, candidate: bool = True) -> dict:
    return {
        "channel_id": channel_id,
        "channel_name": name,
        "enabled": True,
        "base_priority": 100,
        "effective_priority": 98.5,
        "success_rate": 0.997,
        "health_sample_count": 120,
        "exact_model_match": True,
        "fuzzy_model_match": False,
        "model_eligible_key_count": 3,
        "active_key_count": 3 if candidate else 0,
        "enabled_key_count": 3,
        "rpm_limit": 120,
        "current_rpm": 12,
        "max_concurrency": 16,
        "active_concurrency": 2,
        "candidate": candidate,
        "candidate_position": 1 if candidate else 0,
        "higher_priority_count": 0,
        "same_priority_count": 2,
        "estimated_traffic_share": 0.34 if candidate else 0,
        "actual_requests": 34 if candidate else 0,
        "actual_share": 0.34 if candidate else 0,
        "reasons": [] if candidate else [
            {"code": "cooldown", "message": "渠道冷却至 10:32:00", "blocking": True},
            {"code": "no_active_key", "message": "所有允许当前模型的 Key 当前不可用", "blocking": True},
        ],
    }


ROUTE_DIAGNOSTIC = {
    "model": "gpt-5.4",
    "client_protocol": "openai",
    "route_strategy": "sticky",
    "pool_mode": "exact",
    "health_score_enabled": True,
    "target": diagnostic_channel(101, "星河主路由", candidate=False),
    "candidates": [
        diagnostic_channel(102, "远岚 Codex"),
        diagnostic_channel(103, "晨光容灾"),
        diagnostic_channel(104, "长名称渠道 / 备用路由"),
    ],
    "sticky": {
        "channel_id": 102,
        "channel_name": "远岚 Codex",
        "remembered_at": "2026-09-15T09:30:00+08:00",
        "expires_at": "2026-09-15T10:30:00+08:00",
        "in_candidate_pool": True,
    },
    "actual_window": "今日（本机时区）",
    "actual_total_requests": 100,
    "summary": ["目标渠道当前处于冷却中。", "候选池中有 3 个可用渠道。"],
}

AUTH_TOKENS = {
    "tokens": [{
        "id": 201,
        "token": "",
        "token_hint": "sk-...201",
        "token_recoverable": True,
        "description": "家中 Codex",
        "created_at": "2026-09-01T09:00:00+08:00",
        "expires_at": 0,
        "last_used_at": NOW,
        "is_active": True,
        "success_count": 120,
        "failure_count": 3,
        "prompt_tokens_total": 120000,
        "completion_tokens_total": 30000,
        "effective_cost_usd": 1.25,
        "cost_used_usd": 1.25,
        "cost_limit_usd": 10,
        "allowed_models": [],
        "allowed_channel_ids": [],
        "channel_restriction_mode": "allow",
        "max_concurrency": 5,
    }],
    "duration_seconds": 86400,
    "is_today": True,
}

STATS = {
    "stats": [{
        "channel_id": 101,
        "channel_name": "星河主路由 / 长名称渠道",
        "channel_priority": 100,
        "actual_cost_multiplier_min": 0.8,
        "actual_cost_multiplier_max": 1.2,
        "model": "gpt-5.4-sol",
        "success": 98,
        "error": 2,
        "total": 100,
        "avg_first_byte_time_seconds": 0.42,
        "avg_duration_seconds": 2.3,
        "peak_rpm": 16,
        "avg_rpm": 8,
        "recent_rpm": 10,
        "total_input_tokens": 120000,
        "total_output_tokens": 30000,
        "total_cost": 1.2,
        "effective_cost": 1.15,
        "health_timeline": [],
    }],
    "duration_seconds": 86400,
    "rpm_stats": {"peak_rpm": 16, "peak_qps": .3, "avg_rpm": 8, "avg_qps": .15, "recent_rpm": 10, "recent_qps": .18},
    "is_today": True,
    "balance_history": [],
}

SITE_MODELS = [{
    "site_account_id": 11,
    "model": "gpt-5.4-sol",
    "route_type": "openai_response",
    "source": "site_discovery",
    "disabled": False,
    "stale": False,
    "last_seen_at": NOW,
    "created_at": NOW - 86400000,
    "updated_at": NOW,
}]

CHECKINS = [{
    "id": 1,
    "run_id": 1,
    "site_account_id": 11,
    "provider_id": "new-api-family",
    "local_day": "2026-09-15",
    "trigger_scope": "scheduled",
    "status": "success",
    "reward_text": "签到成功，余额 +0.10 USD",
    "balance_before": 82.54,
    "balance_after": 82.64,
    "balance_delta": .10,
    "balance_currency": "USD",
    "started_at": NOW - 60000,
    "finished_at": NOW,
    "attempt_no": 1,
}]


class LayoutFixtures(ConsoleFixtures):
    @staticmethod
    def respond(route, data, status=200):
        payload = fixtures.envelope(data) if status == 200 else {"success": False, "error": data}
        route.fulfill(status=status, content_type="application/json", body=json.dumps(payload, ensure_ascii=False))

    def route(self, route):
        path = urlparse(route.request.url).path
        if path == "/admin/site-inventory":
            # Exercise the widest checkin action cell: an external checkin link
            # plus the primary action button in the same row.
            sites = [
                {**site, "external_checkin_url": "https://checkin.example.net"}
                if site["id"] == 1 else site
                for site in fixtures.SITES
            ]
            accounts = [
                {**account, "last_checkin_status": "browser_required"}
                if account["id"] == 11 else account
                for accounts in fixtures.ACCOUNTS.values()
                for account in accounts
            ]
            self.respond(route, {"sites": sites, "accounts": accounts})
            return
        if path.startswith("/admin/channels/") and path.endswith("/route-diagnostics"):
            self.respond(route, ROUTE_DIAGNOSTIC)
            return
        if path == "/admin/auth-tokens":
            self.respond(route, AUTH_TOKENS)
            return
        if path == "/admin/stats":
            self.respond(route, STATS)
            return
        if path == "/admin/stats/filter-options":
            self.respond(route, {"channel_names": ["星河主路由"], "models": ["gpt-5.4-sol"]})
            return
        if path == "/admin/logs/bootstrap":
            self.respond(route, {
                "channel_test_content": "test",
                "auth_tokens": AUTH_TOKENS["tokens"],
                "models": ["gpt-5.4-sol", "claude-sonnet-4-6"],
                "channels": [{"id": 101, "name": "星河主路由"}],
                "status_codes": [200, 429],
            })
            return
        if path == "/admin/site-models":
            self.respond(route, SITE_MODELS)
            return
        if path.startswith("/admin/site-accounts/") and path.endswith("/checkin-runs"):
            self.respond(route, CHECKINS)
            return
        if path == "/admin/checkin-attempts":
            self.respond(route, CHECKINS)
            return
        super().route(route)


def open_page(browser, base, route: str):
    context = browser.new_context(
        viewport={"width": 1510, "height": 950},
        color_scheme="light", locale="zh-CN", timezone_id="Asia/Shanghai", reduced_motion="reduce",
    )
    context.add_init_script(
        "localStorage.setItem('pivotflow_token','ui-test-only');"
        "localStorage.setItem('pivotflow_web_role','admin');"
    )
    data = LayoutFixtures()
    context.route("**/admin/**", data.route)
    context.route("**/dashboard/**", data.route)
    page = context.new_page()
    errors = []
    page.on("pageerror", lambda error: errors.append(str(error)))
    page.goto(f"{base}/web/console/#/{route}", wait_until="networkidle")
    return context, page, errors


def assert_no_overflow(page, selector: str):
    result = page.evaluate(
        """selector => Array.from(document.querySelectorAll(selector)).map(element => ({
            text: element.textContent.trim().slice(0, 80),
            clientWidth: element.clientWidth,
            scrollWidth: element.scrollWidth
        })).filter(item => item.scrollWidth > item.clientWidth + 1)""",
        selector,
    )
    assert not result, result


def assert_grid_alignment(page, head_selector: str, row_selector: str):
    result = page.evaluate(
        """([headSelector, rowSelector]) => {
            const tracks = selector => (getComputedStyle(document.querySelector(selector))
                .gridTemplateColumns || 'none').split(' ').map(Number);
            const head = tracks(headSelector);
            const row = tracks(rowSelector);
            return {
                headColumns: head.length,
                rowColumns: row.length,
                head,
                row,
                mismatches: head.map((width, index) => ({
                    index,
                    head: width,
                    row: row[index] ?? null
                })).filter(item => item.row === null || Math.abs(item.head - item.row) > 1)
            };
        }""",
        [head_selector, row_selector],
    )
    assert result["headColumns"] == result["rowColumns"], result
    assert not result["mismatches"], result


def assert_fits_desktop_viewport(page, selectors: str):
    result = page.evaluate(
        """selectors => Array.from(document.querySelectorAll(selectors)).flatMap(element => {
            const rect = element.getBoundingClientRect();
            const issues = [];
            if (rect.left < -1 || rect.right > window.innerWidth + 1) {
                issues.push({selector: element.className, issue: 'outside-viewport', left: rect.left, right: rect.right, viewport: window.innerWidth});
            }
            if (element.scrollWidth > element.clientWidth + 1) {
                issues.push({selector: element.className, issue: 'horizontal-scroll', clientWidth: element.clientWidth, scrollWidth: element.scrollWidth});
            }
            return issues;
        })""",
        selectors,
    )
    assert not result, result


def assert_last_column_alignment(page, head_selector: str, row_selector: str):
    result = page.evaluate(
        """([headSelector, rowSelector]) => {
            const head = document.querySelector(headSelector);
            const row = document.querySelector(rowSelector);
            if (!head || !row || !head.lastElementChild || !row.lastElementChild) {
                return {missing: true, head: Boolean(head), row: Boolean(row)};
            }
            const headBox = head.lastElementChild.getBoundingClientRect();
            const rowBox = row.lastElementChild.getBoundingClientRect();
            return {
                missing: false,
                headLeft: headBox.left,
                rowLeft: rowBox.left,
                headRight: headBox.right,
                rowRight: rowBox.right,
                leftDelta: Math.abs(headBox.left - rowBox.left),
                rightDelta: Math.abs(headBox.right - rowBox.right)
            };
        }""",
        [head_selector, row_selector],
    )
    assert not result["missing"], result
    assert result["leftDelta"] <= 1.5, result
    assert result["rightDelta"] <= 1.5, result


def assert_record_heads_fill_panel(page):
    result = page.evaluate(
        """() => Array.from(document.querySelectorAll('.record-head')).map(head => {
            const panel = head.parentElement;
            const headBox = head.getBoundingClientRect();
            const panelBox = panel.getBoundingClientRect();
            const row = panel.querySelector('.record-row');
            const rowBox = row ? row.getBoundingClientRect() : null;
            return {
                className: head.className,
                headWidth: headBox.width,
                panelWidth: panelBox.width,
                rowWidth: rowBox ? rowBox.width : null,
                headScrollWidth: head.scrollWidth,
                rowScrollWidth: row ? row.scrollWidth : null,
                delta: Math.abs(headBox.width - panelBox.width)
            };
        }).filter(item => item.delta > 2.5)"""
    )
    assert not result, result


def assert_channel_success_rates(page):
    rates = page.evaluate(
        """() => Array.from(document.querySelectorAll('.channel-routing strong'))
            .map(element => element.textContent.trim())
            .filter(text => text.endsWith('%'))
            .map(text => Number(text.slice(0, -1)))"""
    )
    assert rates, "No channel success rates were rendered"
    assert all(0 <= rate <= 100.01 for rate in rates), rates


def capture(page, path: Path):
    page.screenshot(path=str(path), full_page=True, type="jpeg", quality=92)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, default=ROOT / ".tmp-table-layout-ui")
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    server = QuietHTTPServer(("127.0.0.1", 0), partial(QuietHandler, directory=str(ROOT)))
    thread = Thread(target=server.serve_forever, daemon=True)
    thread.start()
    captures = []
    try:
        with sync_playwright() as playwright:
            browser = playwright.chromium.launch(headless=True)
            pages = [
                ("accounts", "accounts", ".account-grid"),
                ("sites", "sites", ".site-row"),
                ("checkins", "checkins", ".checkin-grid, .checkin-account-grid"),
                ("channels", "channels", ".channel-row"),
                ("tokens", "tokens", ".token-row"),
                ("logs", "logs", ".log-grid"),
                ("stats", "stats", ".stats-grid"),
                ("models", "models", ".model-card"),
            ]
            for name, route, selector in pages:
                context, page, errors = open_page(browser, f"http://127.0.0.1:{server.server_port}", route)
                try:
                    expect(page.locator(selector).first).to_be_visible()
                    assert not errors, errors
                    assert_fits_desktop_viewport(page, ".records-panel, .channel-list, .site-list, .token-table-wrap")
                    assert_record_heads_fill_panel(page)
                    assert_no_overflow(page, ".row-actions, .account-actions, .checkin-actions, .log-model-name, .model-row-action")
                    if name == "accounts":
                        assert_grid_alignment(page, ".record-head.account-grid", ".record-row.account-grid")
                        assert_last_column_alignment(page, ".record-head.account-grid", ".record-row.account-grid")
                        assert page.locator(".record-row.account-grid .account-actions").count() > 0
                        assert page.locator(".record-row.account-grid .account-actions button").count() > 0
                    elif name == "checkins":
                        assert_grid_alignment(page, ".record-head.checkin-account-grid", ".record-row.checkin-account-grid")
                        assert_last_column_alignment(page, ".record-head.checkin-account-grid", ".record-row.checkin-account-grid")
                    elif name == "logs":
                        assert_grid_alignment(page, ".record-head.log-grid", ".record-row.log-grid")
                        assert_last_column_alignment(page, ".record-head.log-grid", ".record-row.log-grid")
                        assert_no_overflow(page, ".record-row.log-grid > div:first-child strong, .log-status .status-badge, .log-model-name")
                        with page.expect_request(lambda request: "/admin/logs" in request.url and "search=glm" in request.url) as request_info:
                            page.locator(".log-search input").fill("glm")
                        assert "search=glm" in request_info.value.url
                        expect(page.locator(".log-search input")).to_have_value("glm")
                    elif name == "stats":
                        assert_grid_alignment(page, ".record-head.stats-grid", ".record-row.stats-grid")
                        assert_last_column_alignment(page, ".record-head.stats-grid", ".record-row.stats-grid")
                    elif name == "models":
                        expect(page.locator(".model-card").first).to_be_visible()
                        page.locator(".model-layout-toggle button").nth(1).click()
                        assert_grid_alignment(page, ".record-head.model-grid", ".record-row.model-grid")
                        assert_last_column_alignment(page, ".record-head.model-grid", ".record-row.model-grid")
                    elif name == "tokens":
                        assert_grid_alignment(page, ".token-table-head", ".token-row")
                        assert_last_column_alignment(page, ".token-table-head", ".token-row")
                    elif name == "channels":
                        assert_channel_success_rates(page)
                    capture(page, args.out / f"{name}.jpg")
                    captures.append(str((args.out / f"{name}.jpg").resolve()))
                finally:
                    context.close()

            context, page, errors = open_page(browser, f"http://127.0.0.1:{server.server_port}", "channels")
            try:
                page.get_by_role("button", name="诊断 星河主路由 的路由").click()
                dialog = page.get_by_role("dialog", name="星河主路由 · 路由诊断")
                expect(dialog).to_be_visible()
                expect(dialog.locator(".route-diagnostic-channel").first).to_be_visible()
                header = dialog.locator(".route-diagnostic-candidates > header")
                header_box = header.bounding_box()
                assert header_box and header_box["height"] <= 34, header_box
                assert_no_overflow(page, ".route-diagnostic-candidates > header")
                assert_no_overflow(page, ".row-actions, .account-actions, .log-model-name")
                assert page.evaluate("() => document.documentElement.scrollWidth <= document.documentElement.clientWidth")
                assert not errors, errors
                capture(page, args.out / "route-diagnostics.jpg")
                captures.append(str((args.out / "route-diagnostics.jpg").resolve()))
            finally:
                context.close()
            browser.close()
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    (args.out / "report.json").write_text(json.dumps({"passed": True, "captures": captures}, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"passed": True, "captures": len(captures), "output": str(args.out.resolve())}, ensure_ascii=False))


if __name__ == "__main__":
    main()
